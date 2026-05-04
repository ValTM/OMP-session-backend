package ompstore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestListSessionsSearchesSummaryAndPrompt(t *testing.T) {
	tmp := t.TempDir()
	rollout := filepath.Join(tmp, "session.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"session","version":3,"id":"abc","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}
{"type":"message","id":"m1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Find CDC datastream session"}]}}
{"type":"message","id":"m2","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"Found it"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	db := setupDB(t, rollout)
	store := NewForTest(db)
	defer store.Close()

	result, err := store.ListSessions(context.Background(), ListSessionsFilter{Query: "cdc datastream", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("expected one match, got %d", result.Total)
	}
	if got := stringValue(result.Items[0].FirstUserPrompt); got != "Find CDC datastream session" {
		t.Fatalf("unexpected first prompt %q", got)
	}
	if result.Items[0].MessageCount != 2 {
		t.Fatalf("expected 2 readable messages, got %d", result.Items[0].MessageCount)
	}
}

func TestParseSessionFileHidesThinking(t *testing.T) {
	tmp := t.TempDir()
	rollout := filepath.Join(tmp, "session.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"message","id":"m1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"secret"},{"type":"text","text":"visible"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed := ParseSessionFile(rollout, false)
	if len(parsed.Messages) != 1 {
		t.Fatalf("expected one message, got %d", len(parsed.Messages))
	}
	if parsed.Messages[0].Text != "visible" {
		t.Fatalf("unexpected message text %q", parsed.Messages[0].Text)
	}
}
func TestListSessionsMessageCountFilters(t *testing.T) {
	tmp := t.TempDir()
	emptyRollout := filepath.Join(tmp, "empty.jsonl")
	if err := os.WriteFile(emptyRollout, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	nonEmptyRollout := filepath.Join(tmp, "non-empty.jsonl")
	if err := os.WriteFile(nonEmptyRollout, []byte(`{"type":"message","id":"m1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	db := setupDB(t, nonEmptyRollout)
	if _, err := db.Exec(`INSERT INTO threads VALUES ('empty', 1767225601, ?, '/tmp/project', 'cli')`, emptyRollout); err != nil {
		t.Fatal(err)
	}
	store := NewForTest(db)
	defer store.Close()

	withoutEmpty, err := store.ListSessions(context.Background(), ListSessionsFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if withoutEmpty.Total != 1 {
		t.Fatalf("expected empty sessions hidden by default, got %d", withoutEmpty.Total)
	}

	withEmpty, err := store.ListSessions(context.Background(), ListSessionsFilter{Limit: 10, IncludeEmptyMessages: true, MessageCountBucket: "0-25"})
	if err != nil {
		t.Fatal(err)
	}
	if withEmpty.Total != 2 {
		t.Fatalf("expected empty sessions included, got %d", withEmpty.Total)
	}
}

func TestMessageCountBucketMatchesBoundaries(t *testing.T) {
	cases := []struct {
		name         string
		count        int
		bucket       string
		includeEmpty bool
		want         bool
	}{
		{name: "all allows empty after include-empty policy", count: 0, bucket: "all", includeEmpty: false, want: true},
		{name: "zero excluded from first bucket unless included", count: 0, bucket: "0-25", includeEmpty: false, want: false},
		{name: "zero included in first bucket when requested", count: 0, bucket: "0-25", includeEmpty: true, want: true},
		{name: "twenty five belongs to first bucket", count: 25, bucket: "0-25", includeEmpty: false, want: true},
		{name: "twenty six belongs to second bucket", count: 26, bucket: "25-75", includeEmpty: false, want: true},
		{name: "seventy five belongs to second bucket", count: 75, bucket: "25-75", includeEmpty: false, want: true},
		{name: "seventy six belongs to third bucket", count: 76, bucket: "75-150", includeEmpty: false, want: true},
		{name: "one fifty belongs to third bucket", count: 150, bucket: "75-150", includeEmpty: false, want: true},
		{name: "one fifty one belongs to final bucket", count: 151, bucket: "150+", includeEmpty: false, want: true},
		{name: "one fifty excluded from final bucket", count: 150, bucket: "150+", includeEmpty: false, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := messageCountBucketMatches(tc.count, tc.bucket, tc.includeEmpty)
			if got != tc.want {
				t.Fatalf("messageCountBucketMatches(%d, %q, %t) = %t, want %t", tc.count, tc.bucket, tc.includeEmpty, got, tc.want)
			}
		})
	}
}

func TestParseSessionFileStripsToolResultLineAnchors(t *testing.T) {
	tmp := t.TempDir()
	rollout := filepath.Join(tmp, "session.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"message","id":"m1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"toolResult","toolName":"read","content":[{"type":"text","text":"448fe|first line\n 449yc|context line\n*450zz|match line\nnot-an-anchor|kept"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed := ParseSessionFile(rollout, false)
	if len(parsed.Messages) != 1 {
		t.Fatalf("expected one message, got %d", len(parsed.Messages))
	}
	text := parsed.Messages[0].Text
	for _, unwanted := range []string{"448fe|", "449yc|", "450zz|"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("expected %q to be stripped from %q", unwanted, text)
		}
	}
	for _, wanted := range []string{"first line", "context line", "match line", "not-an-anchor|kept"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("expected %q in %q", wanted, text)
		}
	}
}

func setupDB(t *testing.T, rollout string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE threads (
			id TEXT PRIMARY KEY,
			updated_at INTEGER NOT NULL,
			rollout_path TEXT NOT NULL,
			cwd TEXT NOT NULL,
			source_kind TEXT NOT NULL
		);
		CREATE TABLE stage1_outputs (
			thread_id TEXT PRIMARY KEY,
			source_updated_at INTEGER NOT NULL,
			raw_memory TEXT NOT NULL,
			rollout_summary TEXT NOT NULL,
			rollout_slug TEXT,
			generated_at INTEGER NOT NULL
		);
		INSERT INTO threads VALUES ('abc', 1767225600, ?, '/tmp/project', 'cli');
		INSERT INTO stage1_outputs VALUES ('abc', 1767225600, '', 'CDC Datastream work', 'cdc_datastream', 1767225600);
	`, rollout)
	if err != nil {
		t.Fatal(err)
	}
	return db
}
