package ompstore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestListSessionsUsesLatestMessageTimestampForUpdatedAt(t *testing.T) {
	tmp := t.TempDir()
	rollout := filepath.Join(tmp, "session.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"message","id":"m1","timestamp":"2026-01-03T04:05:06Z","message":{"role":"user","content":[{"type":"text","text":"newer than db"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	db := setupDB(t, rollout)
	store := NewForTest(db)
	defer store.Close()

	result, err := store.ListSessions(context.Background(), ListSessionsFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := mustParseTime(t, "2026-01-03T04:05:06Z")
	if !result.Items[0].UpdatedAt.Equal(want) {
		t.Fatalf("expected latest message timestamp %s, got %s", want, result.Items[0].UpdatedAt)
	}
}

func TestListSessionsReparsesChangedRolloutFiles(t *testing.T) {
	tmp := t.TempDir()
	rollout := filepath.Join(tmp, "session.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"message","id":"m1","timestamp":"2026-01-02T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	db := setupDB(t, rollout)
	store := NewForTest(db)
	defer store.Close()

	first, err := store.ListSessions(context.Background(), ListSessionsFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if first.Items[0].MessageCount != 1 {
		t.Fatalf("expected initial message count 1, got %d", first.Items[0].MessageCount)
	}

	if err := os.WriteFile(rollout, []byte(`{"type":"message","id":"m1","timestamp":"2026-01-02T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"first"}]}}
{"type":"message","id":"m2","timestamp":"2026-01-04T00:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"second"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := store.ListSessions(context.Background(), ListSessionsFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if second.Items[0].MessageCount != 2 {
		t.Fatalf("expected changed rollout to be reparsed, got message count %d", second.Items[0].MessageCount)
	}
	want := mustParseTime(t, "2026-01-04T00:00:00Z")
	if !second.Items[0].UpdatedAt.Equal(want) {
		t.Fatalf("expected updated time %s after reparse, got %s", want, second.Items[0].UpdatedAt)
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
func TestParseSessionFileCountsOnlyActualMessages(t *testing.T) {
	tmp := t.TempDir()
	rollout := filepath.Join(tmp, "session.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"message","id":"user-1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"message","id":"tool-call-1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"toolCall","name":"read"}]}}
{"type":"message","id":"tool-result-1","timestamp":"2026-01-01T00:00:03Z","message":{"role":"toolResult","toolName":"read","content":[{"type":"text","text":"file contents"}]}}
{"type":"message","id":"assistant-1","timestamp":"2026-01-01T00:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed := ParseSessionFile(rollout, false)
	if parsed.MessageCount != 2 {
		t.Fatalf("expected only user/assistant text messages to count, got %d", parsed.MessageCount)
	}
	if len(parsed.Messages) != 4 {
		t.Fatalf("expected tool messages to remain inspectable, got %d messages", len(parsed.Messages))
	}
	if parsed.Messages[1].Role != "toolCall" {
		t.Fatalf("expected tool call role, got %q", parsed.Messages[1].Role)
	}
	if parsed.Messages[1].Text != "Tool call: read" {
		t.Fatalf("unexpected tool call text %q", parsed.Messages[1].Text)
	}
}

func TestParseSessionFileClassifiesMixedAssistantToolTurnAsToolCall(t *testing.T) {
	tmp := t.TempDir()
	rollout := filepath.Join(tmp, "session.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"message","id":"assistant-1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"Verify nothing remains:"},{"type":"toolCall","name":"bash"},{"type":"toolCall","name":"grep"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed := ParseSessionFile(rollout, false)
	if parsed.MessageCount != 0 {
		t.Fatalf("expected mixed assistant tool turn not to count as an actual message, got %d", parsed.MessageCount)
	}
	if len(parsed.Messages) != 1 {
		t.Fatalf("expected one grouped tool call message, got %d messages", len(parsed.Messages))
	}
	if parsed.Messages[0].Role != "toolCall" {
		t.Fatalf("expected toolCall role, got %q", parsed.Messages[0].Role)
	}
	for _, expected := range []string{"Verify nothing remains:", "Tool call: bash", "Tool call: grep"} {
		if !strings.Contains(parsed.Messages[0].Text, expected) {
			t.Fatalf("expected %q in %q", expected, parsed.Messages[0].Text)
		}
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

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
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
