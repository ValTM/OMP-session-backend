package httpapi

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omp-session-viewer/backend/internal/ompstore"

	_ "modernc.org/sqlite"
)

func TestSessionsEndpoint(t *testing.T) {
	store := testStore(t)
	defer store.Close()

	server := NewServer(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?q=cdc", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "omp --resume abc") {
		t.Fatalf("expected resume command in response: %s", res.Body.String())
	}
}

func TestSessionsEndpointMessageCountBucket(t *testing.T) {
	store := testStore(t)
	defer store.Close()

	server := NewServer(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/sessions?messageCountBucket=150%2B", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"total":0`) {
		t.Fatalf("expected message count bucket to filter response: %s", res.Body.String())
	}
}

func testStore(t *testing.T) *ompstore.Store {
	t.Helper()
	tmp := t.TempDir()
	rollout := filepath.Join(tmp, "session.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"message","id":"m1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"cdc"}]}}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE threads (id TEXT PRIMARY KEY, updated_at INTEGER NOT NULL, rollout_path TEXT NOT NULL, cwd TEXT NOT NULL, source_kind TEXT NOT NULL);
		CREATE TABLE stage1_outputs (thread_id TEXT PRIMARY KEY, source_updated_at INTEGER NOT NULL, raw_memory TEXT NOT NULL, rollout_summary TEXT NOT NULL, rollout_slug TEXT, generated_at INTEGER NOT NULL);
		INSERT INTO threads VALUES ('abc', 1767225600, ?, '/tmp/project', 'cli');
	`, rollout)
	if err != nil {
		t.Fatal(err)
	}
	return ompstore.NewForTest(db)
}
