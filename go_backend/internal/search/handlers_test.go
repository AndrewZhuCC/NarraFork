package search

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSearchReportsDegradedFallbackWhenFTSTablesAreMissing(t *testing.T) {
	sqlDB := openSearchTestDB(t)
	defer sqlDB.Close()
	createSearchBaseTables(t, sqlDB)
	insertSearchRows(t, sqlDB)

	response := runSearchRequest(t, sqlDB, "/api/search?q=AlphaBeta&entities=chapters,messages,narrators")

	if degraded, ok := response["degraded"].(bool); !ok || !degraded {
		t.Fatalf("expected degraded=true, got %#v", response["degraded"])
	}
	fallbacks := response["fallbacks"].([]any)
	if len(fallbacks) != 3 {
		t.Fatalf("expected fallback metadata for 3 entity groups, got %d: %#v", len(fallbacks), fallbacks)
	}
	for _, raw := range fallbacks {
		fallback := raw.(map[string]any)
		if fallback["from"] != "fts5" || fallback["to"] != "like" {
			t.Fatalf("expected explicit fts5->like fallback, got %#v", fallback)
		}
		if fallback["reason"] != "fts_unavailable" {
			t.Fatalf("expected fts_unavailable reason, got %#v", fallback)
		}
		if _, ok := fallback["error"].(string); !ok {
			t.Fatalf("expected original FTS error in fallback metadata, got %#v", fallback)
		}
	}
	results := response["results"].([]any)
	if len(results) != 3 {
		t.Fatalf("expected LIKE fallback to return 3 results, got %d: %#v", len(results), results)
	}
}

func TestSearchReportsShortQueryFallbackWithoutDegradedFlag(t *testing.T) {
	sqlDB := openSearchTestDB(t)
	defer sqlDB.Close()
	createSearchBaseTables(t, sqlDB)
	insertSearchRows(t, sqlDB)

	response := runSearchRequest(t, sqlDB, "/api/search?q=Al&entities=chapters")

	if degraded, ok := response["degraded"].(bool); !ok || degraded {
		t.Fatalf("expected degraded=false for normal short-query LIKE fallback, got %#v", response["degraded"])
	}
	fallbacks := response["fallbacks"].([]any)
	if len(fallbacks) != 1 {
		t.Fatalf("expected one fallback entry, got %d: %#v", len(fallbacks), fallbacks)
	}
	fallback := fallbacks[0].(map[string]any)
	if fallback["reason"] != "query_too_short_for_trigram" {
		t.Fatalf("expected short-query fallback reason, got %#v", fallback)
	}
	results := response["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("expected one chapter result, got %d: %#v", len(results), results)
	}
}

func openSearchTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return sqlDB
}

func createSearchBaseTables(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, description TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, title TEXT, status TEXT, model TEXT, last_message_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, content_text TEXT, role TEXT, created_at TEXT)`,
	}
	for _, statement := range statements {
		if _, err := sqlDB.Exec(statement); err != nil {
			t.Fatalf("exec schema statement %q: %v", statement, err)
		}
	}
}

func insertSearchRows(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO projects (id, name, description) VALUES ('p1', 'AlphaBeta project', 'demo')`,
		`INSERT INTO chapters (id, project_id, title, description, status, role, created_at, updated_at) VALUES ('c1', 'p1', 'AlphaBeta chapter', 'chapter body', 'active', 'branch', '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z')`,
		`INSERT INTO narrators (id, chapter_id, title, status, model, last_message_at, created_at, updated_at) VALUES ('n1', 'c1', 'AlphaBeta narrator', 'idle', 'test-model', '2026-01-03T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-03T00:00:00Z')`,
		`INSERT INTO narrator_messages (id, narrator_id, content_text, role, created_at) VALUES ('m1', 'n1', 'AlphaBeta message', 'user', '2026-01-04T00:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := sqlDB.Exec(statement); err != nil {
			t.Fatalf("exec data statement %q: %v", statement, err)
		}
	}
}

func runSearchRequest(t *testing.T, sqlDB *sql.DB, target string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	if err := NewHandler(sqlDB).search(w, req); err != nil {
		t.Fatalf("search handler returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, w.Body.String())
	}
	return response
}
