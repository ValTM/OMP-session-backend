package ompstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
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
	seenIDs := make(map[string]bool)
	seenPaths := make(map[string]bool)
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
		seenIDs[summary.ID] = true
		seenPaths[summary.RolloutPath] = true
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

	orphanSessions, err := s.listOrphanSessionSummaries(seenIDs, seenPaths)
	if err != nil {
		return ListSessionsResult{}, err
	}
	for _, summary := range orphanSessions {
		if sessionMatches(summary, filter) {
			all = append(all, summary)
		}
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
		if errors.Is(err, sql.ErrNoRows) {
			return s.getOrphanSession(id)
		}
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

func (s *Store) GetMessages(
	ctx context.Context,
	id string,
	includeRaw bool,
	includeToolCalls bool,
	includeToolResults bool,
	limit int,
	offset int,
) (MessageListResult, error) {
	session, err := s.GetSession(ctx, id)
	if err != nil {
		return MessageListResult{}, err
	}
	parsed := ParseSessionFile(session.RolloutPath, includeRaw)
	if parsed.ParseError != nil && len(parsed.Messages) == 0 {
		return MessageListResult{}, errors.New(*parsed.ParseError)
	}
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	var filtered []SessionMessage
	var toolCallCount int
	var toolResultCount int
	for _, message := range parsed.Messages {
		switch {
		case isToolCall(message):
			toolCallCount++
			if includeToolCalls {
				filtered = append(filtered, message)
			}
		case isToolResult(message):
			toolResultCount++
			if includeToolResults {
				filtered = append(filtered, message)
			}
		default:
			filtered = append(filtered, message)
		}
	}

	total := len(filtered)
	start := min(offset, total)
	end := min(start+limit, total)
	messages := filtered[start:end]
	if messages == nil {
		messages = []SessionMessage{}
	}
	return MessageListResult{Items: messages, Total: total, Limit: limit, Offset: offset, ToolCallCount: toolCallCount, ToolResultCount: toolResultCount}, nil
}

func isToolCall(message SessionMessage) bool {
	return message.Role == "toolCall"
}

func isToolResult(message SessionMessage) bool {
	return message.Role == "toolResult"
}

func (s *Store) getOrphanSession(id string) (SessionSummary, error) {
	summaries, err := s.listOrphanSessionSummaries(nil, nil)
	if err != nil {
		return SessionSummary{}, err
	}
	for _, summary := range summaries {
		if summary.ID == id {
			return summary, nil
		}
	}
	return SessionSummary{}, sql.ErrNoRows
}

func (s *Store) listOrphanSessionSummaries(seenIDs map[string]bool, seenPaths map[string]bool) ([]SessionSummary, error) {
	if s.ompRoot == "" {
		return nil, nil
	}
	sessionsRoot := filepath.Join(s.ompRoot, "sessions")
	var summaries []SessionSummary
	err := filepath.WalkDir(sessionsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(sessionsRoot, path)
		if err != nil {
			return err
		}
		depth := pathDepth(relativePath)
		if entry.IsDir() {
			if relativePath != "." && depth >= 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if depth != 2 || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		if seenPaths != nil && seenPaths[path] {
			return nil
		}

		summary, ok := s.summaryFromRolloutPath(path)
		if !ok {
			return nil
		}
		if seenIDs != nil && seenIDs[summary.ID] {
			return nil
		}
		summaries = append(summaries, summary)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return summaries, err
}

func (s *Store) summaryFromRolloutPath(path string) (SessionSummary, bool) {
	parsed := s.cachedParse(path)
	id := parsed.SessionID
	if id == "" {
		id = sessionIDFromRolloutPath(path)
	}
	if id == "" {
		return SessionSummary{}, false
	}

	updatedAt := time.Time{}
	if stat, err := os.Stat(path); err == nil {
		updatedAt = stat.ModTime().UTC()
	}
	if parsed.SessionStartedAt != nil && updatedAt.IsZero() {
		updatedAt = parsed.SessionStartedAt.UTC()
	}

	summary := SessionSummary{
		ID:            id,
		UpdatedAt:     updatedAt,
		CWD:           parsed.SessionCWD,
		SourceKind:    "cli",
		RolloutPath:   path,
		ResumeCommand: "omp --resume " + id,
	}
	applyParsedSummary(&summary, parsed)
	return summary, true
}

func sessionIDFromRolloutPath(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	_, id, ok := strings.Cut(name, "_")
	if !ok {
		return ""
	}
	return id
}

func pathDepth(path string) int {
	if path == "." || path == "" {
		return 0
	}
	return len(strings.Split(path, string(os.PathSeparator)))
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
	applyParsedSummary(summary, parsed)
}

func applyParsedSummary(summary *SessionSummary, parsed ParseResult) {
	summary.Title = parsed.Title
	summary.FirstUserPrompt = parsed.FirstUserPrompt
	summary.MessageCount = parsed.MessageCount
	summary.MainModels = nonNilModels(parsed.MainModels)
	summary.MainUsage = parsed.MainUsage
	summary.ParseError = parsed.ParseError
	if parsed.LatestMessageAt != nil && parsed.LatestMessageAt.After(summary.UpdatedAt) {
		summary.UpdatedAt = parsed.LatestMessageAt.UTC()
	}
	summary.SearchText = strings.Join([]string{
		summary.ID,
		summary.CWD,
		summary.SourceKind,
		stringValue(summary.Title),
		stringValue(summary.Slug),
		stringValue(summary.Summary),
		stringValue(summary.FirstUserPrompt),
		strings.Join(summary.MainModels, " "),
		usageSearchText(summary.MainUsage),
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

func usageSearchText(usage *TokenUsage) string {
	if usage == nil || usage.TotalTokens == 0 {
		return ""
	}
	return fmt.Sprintf("%d tokens", usage.TotalTokens)
}

func nonNilModels(models []string) []string {
	if models == nil {
		return []string{}
	}
	return models
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
