package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"narrafork-go/internal/compat/bunfinal"
	"narrafork-go/internal/db"

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

func TestSearchReportsShortQueryLikeModeWithoutFallbacks(t *testing.T) {
	sqlDB := openSearchTestDB(t)
	defer sqlDB.Close()
	createSearchBaseTables(t, sqlDB)
	insertSearchRows(t, sqlDB)

	response := runSearchRequest(t, sqlDB, "/api/search?q=Al&entities=chapters")

	if degraded, ok := response["degraded"].(bool); !ok || degraded {
		t.Fatalf("expected degraded=false for normal short-query LIKE mode, got %#v", response["degraded"])
	}
	fallbacks := response["fallbacks"].([]any)
	if len(fallbacks) != 0 {
		t.Fatalf("short-query LIKE mode should not emit fallback diagnostics, got %#v", fallbacks)
	}
	metadata := response["searchMetadata"].(map[string]any)
	metadataFallbacks := metadata["fallbacks"].([]any)
	if metadata["shortQuery"] != true || metadata["mode"] != "like-short-query" || len(metadataFallbacks) != 0 {
		t.Fatalf("short query should expose mode without fallback noise, got %#v", metadata)
	}
	results := response["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("expected one chapter result, got %d: %#v", len(results), results)
	}
}

type searchRoundtripArtifact struct {
	Cases []searchRoundtripCase `json:"cases"`
}

type searchRoundtripCase struct {
	ID               string         `json:"id"`
	DBState          string         `json:"dbState"`
	Query            string         `json:"query"`
	Entities         []string       `json:"entities"`
	ExpectedMetadata map[string]any `json:"expectedMetadata"`
}

func TestBunFinalSearchRoundtripCases(t *testing.T) {
	data, err := bunfinal.ReadSearchRoundtripCases()
	if err != nil {
		t.Fatalf("read search roundtrip artifact: %v", err)
	}
	var artifact searchRoundtripArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("decode search roundtrip artifact: %v", err)
	}
	if len(artifact.Cases) == 0 {
		t.Fatal("search roundtrip artifact should contain cases")
	}
	for _, tc := range artifact.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			sqlDB := openSearchTestDB(t)
			defer sqlDB.Close()
			createSearchBaseTables(t, sqlDB)
			if tc.DBState == "fts-ready" {
				if _, err := db.EnsureSQLiteFTS(context.Background(), sqlDB, db.EnsureSQLiteFTSOptions{SkipUncleanShutdownRebuild: true}); err != nil {
					t.Skipf("SQLite FTS5/trigram is not available for search roundtrip case: %v", err)
				}
			}
			insertSearchRows(t, sqlDB)
			target := "/api/search?q=" + url.QueryEscape(tc.Query)
			if len(tc.Entities) > 0 {
				target += "&entities=" + url.QueryEscape(strings.Join(tc.Entities, ","))
			}
			response := runSearchRequest(t, sqlDB, target)
			assertSearchRoundtripMetadata(t, response, tc.ExpectedMetadata)
			if tc.ExpectedMetadata["requestShouldFail"] != false {
				assertSearchRoundtripEntities(t, response, tc.Entities)
			}
		})
	}
}

func openSearchTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
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

func assertSearchRoundtripMetadata(t *testing.T, response map[string]any, expected map[string]any) {
	t.Helper()
	metadata, _ := response["searchMetadata"].(map[string]any)
	for key, want := range expected {
		switch key {
		case "degraded":
			if response["degraded"] != want || metadata["degraded"] != want {
				t.Fatalf("degraded metadata mismatch: response=%#v metadata=%#v want=%#v", response["degraded"], metadata["degraded"], want)
			}
		case "ftsReady":
			if metadata["ftsReady"] != want {
				t.Fatalf("ftsReady metadata mismatch: got %#v want %#v in %#v", metadata["ftsReady"], want, metadata)
			}
		case "shortQuery":
			if metadata["shortQuery"] != want {
				t.Fatalf("shortQuery metadata mismatch: got %#v want %#v in %#v", metadata["shortQuery"], want, metadata)
			}
		case "requestShouldFail":
			if want != false {
				t.Fatalf("unsupported requestShouldFail expectation: %#v", want)
			}
		}
	}
}

func assertSearchRoundtripEntities(t *testing.T, response map[string]any, entities []string) {
	t.Helper()
	results, _ := response["results"].([]any)
	for _, entity := range entities {
		wantType := strings.TrimSuffix(entity, "s")
		found := false
		for _, item := range results {
			mapped, _ := item.(map[string]any)
			if mapped["type"] == wantType {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected search results to include entity %q in %#v", entity, results)
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
