package ompstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type cachedParseResult struct {
	result  ParseResult
	modTime time.Time
	size    int64
}

type Store struct {
	ompRoot string
	agentDB *sql.DB

	cacheMu sync.Mutex
	cache   map[string]cachedParseResult
}

func Open(ompRoot string) (*Store, error) {
	agentPath := filepath.Join(ompRoot, "agent.db")
	agentDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(agentPath)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	if err := agentDB.Ping(); err != nil {
		agentDB.Close()
		return nil, err
	}

	return &Store{
		ompRoot: ompRoot,
		agentDB: agentDB,
		cache:   make(map[string]cachedParseResult),
	}, nil
}

func NewForTest(agentDB *sql.DB) *Store {
	return &Store{agentDB: agentDB, cache: make(map[string]cachedParseResult)}
}

func (s *Store) Close() error {
	if s.agentDB == nil {
		return nil
	}
	return s.agentDB.Close()
}

func (s *Store) ListSessions(ctx context.Context, filter ListSessionsFilter) (ListSessionsResult, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 1000 {
		filter.Limit = 1000
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}

	rows, err := s.agentDB.QueryContext(ctx, `
		SELECT
			threads.id,
			threads.updated_at,
			threads.rollout_path,
			threads.cwd,
			threads.source_kind,
			stage1_outputs.rollout_summary,
			stage1_outputs.rollout_slug
		FROM threads
		LEFT JOIN stage1_outputs ON stage1_outputs.thread_id = threads.id
		ORDER BY threads.updated_at DESC
	`)
	if err != nil {
		return ListSessionsResult{}, err
	}
	defer rows.Close()

	var all []SessionSummary
	for rows.Next() {
		var summary SessionSummary
		var updatedAt int64
		var rolloutSummary sql.NullString
		var rolloutSlug sql.NullString
		if err := rows.Scan(&summary.ID, &updatedAt, &summary.RolloutPath, &summary.CWD, &summary.SourceKind, &rolloutSummary, &rolloutSlug); err != nil {
			return ListSessionsResult{}, err
		}
		summary.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		summary.ResumeCommand = "omp --resume " + summary.ID
		if rolloutSummary.Valid && rolloutSummary.String != "" {
			summary.Summary = &rolloutSummary.String
		}
		if rolloutSlug.Valid && rolloutSlug.String != "" {
			summary.Slug = &rolloutSlug.String
		}

		s.enrichSummary(&summary)
		if sessionMatches(summary, filter) {
			all = append(all, summary)
		}
	}
	if err := rows.Err(); err != nil {
		return ListSessionsResult{}, err
	}

	sort.SliceStable(all, func(i, j int) bool {
		if filter.Sort == "updatedAtAsc" {
			return all[i].UpdatedAt.Before(all[j].UpdatedAt)
		}
		return all[i].UpdatedAt.After(all[j].UpdatedAt)
	})

	total := len(all)
	start := min(filter.Offset, total)
	end := min(start+filter.Limit, total)
	items := all[start:end]
	if items == nil {
		items = []SessionSummary{}
	}

	return ListSessionsResult{Items: items, Total: total, Limit: filter.Limit, Offset: filter.Offset}, nil
}

func (s *Store) GetSession(ctx context.Context, id string) (SessionSummary, error) {
	row := s.agentDB.QueryRowContext(ctx, `
		SELECT
			threads.id,
			threads.updated_at,
			threads.rollout_path,
			threads.cwd,
			threads.source_kind,
			stage1_outputs.rollout_summary,
			stage1_outputs.rollout_slug
		FROM threads
		LEFT JOIN stage1_outputs ON stage1_outputs.thread_id = threads.id
		WHERE threads.id = ?
	`, id)

	var summary SessionSummary
	var updatedAt int64
	var rolloutSummary sql.NullString
	var rolloutSlug sql.NullString
	if err := row.Scan(&summary.ID, &updatedAt, &summary.RolloutPath, &summary.CWD, &summary.SourceKind, &rolloutSummary, &rolloutSlug); err != nil {
		return SessionSummary{}, err
	}
	summary.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	summary.ResumeCommand = "omp --resume " + summary.ID
	if rolloutSummary.Valid && rolloutSummary.String != "" {
		summary.Summary = &rolloutSummary.String
	}
	if rolloutSlug.Valid && rolloutSlug.String != "" {
		summary.Slug = &rolloutSlug.String
	}
	s.enrichSummary(&summary)
	return summary, nil
}

func (s *Store) GetMessages(ctx context.Context, id string, includeRaw bool, limit int, offset int) ([]SessionMessage, error) {
	session, err := s.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	parsed := ParseSessionFile(session.RolloutPath, includeRaw)
	if parsed.ParseError != nil && len(parsed.Messages) == 0 {
		return nil, errors.New(*parsed.ParseError)
	}
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	start := min(offset, len(parsed.Messages))
	end := min(start+limit, len(parsed.Messages))
	messages := parsed.Messages[start:end]
	if messages == nil {
		messages = []SessionMessage{}
	}
	return messages, nil
}

func (s *Store) ListCWDs(ctx context.Context) ([]CWDOption, error) {
	rows, err := s.agentDB.QueryContext(ctx, `
		SELECT cwd, COUNT(*)
		FROM threads
		GROUP BY cwd
		ORDER BY COUNT(*) DESC, cwd ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var options []CWDOption
	for rows.Next() {
		var option CWDOption
		if err := rows.Scan(&option.CWD, &option.Count); err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if options == nil {
		options = []CWDOption{}
	}
	return options, nil
}

func (s *Store) enrichSummary(summary *SessionSummary) {
	parsed := s.cachedParse(summary.RolloutPath)
	summary.FirstUserPrompt = parsed.FirstUserPrompt
	summary.MessageCount = parsed.MessageCount
	summary.ParseError = parsed.ParseError
	if parsed.LatestMessageAt != nil && parsed.LatestMessageAt.After(summary.UpdatedAt) {
		summary.UpdatedAt = parsed.LatestMessageAt.UTC()
	}
	summary.SearchText = strings.Join([]string{
		summary.ID,
		summary.CWD,
		summary.SourceKind,
		stringValue(summary.Slug),
		stringValue(summary.Summary),
		stringValue(summary.FirstUserPrompt),
		parsed.SearchText,
	}, " ")
}

func (s *Store) cachedParse(path string) ParseResult {
	stat, statErr := os.Stat(path)
	if statErr == nil {
		s.cacheMu.Lock()
		cached, ok := s.cache[path]
		s.cacheMu.Unlock()
		if ok && cached.modTime.Equal(stat.ModTime()) && cached.size == stat.Size() {
			return cached.result
		}
	}

	parsed := ParseSessionFile(path, false)
	if statErr == nil {
		s.cacheMu.Lock()
		s.cache[path] = cachedParseResult{result: parsed, modTime: stat.ModTime(), size: stat.Size()}
		s.cacheMu.Unlock()
	}
	return parsed
}

func sessionMatches(session SessionSummary, filter ListSessionsFilter) bool {
	if filter.CWD != "" && session.CWD != filter.CWD {
		return false
	}
	if filter.SourceKind != "" && session.SourceKind != filter.SourceKind {
		return false
	}
	if filter.From != nil && session.UpdatedAt.Before(*filter.From) {
		return false
	}
	if filter.To != nil && session.UpdatedAt.After(*filter.To) {
		return false
	}
	if !filter.IncludeEmptyMessages && session.MessageCount == 0 {
		return false
	}
	if !messageCountBucketMatches(session.MessageCount, filter.MessageCountBucket, filter.IncludeEmptyMessages) {
		return false
	}
	if strings.TrimSpace(filter.Query) == "" {
		return true
	}

	haystack := strings.ToLower(session.SearchText)
	for _, token := range strings.Fields(strings.ToLower(filter.Query)) {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

func messageCountBucketMatches(count int, bucket string, includeEmpty bool) bool {
	switch bucket {
	case "", "all":
		return true
	case "0-25":
		if includeEmpty {
			return count >= 0 && count <= 25
		}
		return count >= 1 && count <= 25
	case "25-75":
		return count > 25 && count <= 75
	case "75-150":
		return count > 75 && count <= 150
	case "150+":
		return count > 150
	default:
		return true
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func ParseTimeQuery(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("expected RFC3339 timestamp: %w", err)
	}
	return &parsed, nil
}
