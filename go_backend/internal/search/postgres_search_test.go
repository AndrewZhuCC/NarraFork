package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/db"
)

func TestPostgresSearchUsesLikeFallbackWithoutFTS(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("NARRAFORK_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set NARRAFORK_TEST_POSTGRES_DSN to run live Postgres search smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sqlDB, err := sql.Open((db.PostgresDialect{}).DriverName(), dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	schema := "narrafork_search_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	quotedSchema := (db.PostgresDialect{}).QuoteIdent(schema)
	if _, err := sqlDB.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer sqlDB.ExecContext(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	if _, err := sqlDB.ExecContext(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, description TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, title TEXT, status TEXT, model TEXT, last_message_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, content_text TEXT, role TEXT, created_at TEXT)`,
		`INSERT INTO projects (id, name, description) VALUES ('p1', 'Alpha Project', 'demo')`,
		`INSERT INTO chapters (id, project_id, title, description, status, role, created_at, updated_at) VALUES ('c1', 'p1', 'Alpha Chapter', 'demo chapter', 'active', 'branch', '2026-05-18T00:00:00Z', '2026-05-18T00:00:00Z')`,
		`INSERT INTO narrators (id, chapter_id, title, status, model, last_message_at, created_at, updated_at) VALUES ('n1', 'c1', 'Alpha Narrator', 'idle', 'test-model', '2026-05-18T00:00:00Z', '2026-05-18T00:00:00Z', '2026-05-18T00:00:00Z')`,
		`INSERT INTO narrator_messages (id, narrator_id, content_text, role, created_at) VALUES ('m1', 'n1', 'Alpha message body', 'user', '2026-05-18T00:00:00Z')`,
	} {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("exec postgres search fixture: %v\n%s", err, stmt)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=alpha&entities=chapters,messages,narrators", nil)
	w := httptest.NewRecorder()
	if err := NewHandlerWithEngine(sqlDB, db.EnginePostgres).search(w, req); err != nil {
		t.Fatalf("postgres search returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search body: %v", err)
	}
	results, _ := body["results"].([]any)
	if len(results) != 3 {
		t.Fatalf("expected postgres-like results for all entities, got %#v", body)
	}
	metadata, _ := body["searchMetadata"].(map[string]any)
	if metadata["mode"] != "postgres-like" || metadata["degraded"] != false {
		t.Fatalf("unexpected postgres search metadata: %#v", metadata)
	}
	fallbacks, _ := metadata["fallbacks"].([]any)
	if len(fallbacks) != 0 {
		t.Fatalf("postgres-like search should not attempt FTS fallback, got %#v", fallbacks)
	}
}
