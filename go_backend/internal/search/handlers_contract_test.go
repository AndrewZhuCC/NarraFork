package search

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSearchReportsDegradedMetadataWhenFTSTablesAreMissing(t *testing.T) {
	sqlDB := openSearchContractDB(t)
	defer sqlDB.Close()
	createSearchContractBaseTables(t, sqlDB)
	insertSearchContractFixtures(t, sqlDB)

	h := NewHandler(sqlDB)
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=Alpha&entities=chapters,messages,narrators", nil)
	w := httptest.NewRecorder()

	if err := h.search(w, req); err != nil {
		t.Fatalf("search returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	results, _ := body["results"].([]any)
	if len(results) != 3 {
		t.Fatalf("expected LIKE fallback results for all entities, got %#v", body["results"])
	}
	metadata, _ := body["searchMetadata"].(map[string]any)
	if metadata["degraded"] != true || metadata["ftsReady"] != false || metadata["mode"] != "degraded-like-fallback" || metadata["shortQuery"] != false {
		t.Fatalf("expected degraded FTS->LIKE search metadata, got %#v", metadata)
	}
	requested, _ := metadata["requestedEntities"].([]any)
	if len(requested) != 3 || requested[0] != "chapters" || requested[1] != "messages" || requested[2] != "narrators" {
		t.Fatalf("requested entities should be stable and sorted, got %#v", requested)
	}
	fallbacks, _ := metadata["fallbacks"].([]any)
	if len(fallbacks) != 3 {
		t.Fatalf("expected one fallback per requested FTS entity, got %#v", fallbacks)
	}
	seen := map[string]bool{}
	for _, item := range fallbacks {
		fallback, _ := item.(map[string]any)
		seen[fallback["entity"].(string)] = true
		if fallback["from"] != "fts5" || fallback["to"] != "like" || fallback["reason"] != "fts_unavailable" {
			t.Fatalf("unexpected fallback metadata: %#v", fallback)
		}
	}
	for _, entity := range []string{"chapters", "messages", "narrators"} {
		if !seen[entity] {
			t.Fatalf("missing fallback metadata for %s: %#v", entity, fallbacks)
		}
	}
}

func TestSearchShortQueriesUseLikeWithoutDegradedMetadata(t *testing.T) {
	sqlDB := openSearchContractDB(t)
	defer sqlDB.Close()
	createSearchContractBaseTables(t, sqlDB)
	insertSearchContractFixtures(t, sqlDB)

	h := NewHandler(sqlDB)
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=Al&entities=chapters", nil)
	w := httptest.NewRecorder()

	if err := h.search(w, req); err != nil {
		t.Fatalf("search returned error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	metadata, _ := body["searchMetadata"].(map[string]any)
	if metadata["degraded"] != false || metadata["ftsReady"] != nil || metadata["shortQuery"] != true || metadata["mode"] != "like-short-query" {
		t.Fatalf("short LIKE search should not report degraded FTS or claim FTS readiness, got %#v", metadata)
	}
	fallbacks, _ := metadata["fallbacks"].([]any)
	if len(fallbacks) != 0 {
		t.Fatalf("short LIKE search should not emit fallback diagnostics, got %#v", fallbacks)
	}
}

func TestSearchUsesFTSWhenTablesAreReady(t *testing.T) {
	sqlDB := openSearchContractDB(t)
	defer sqlDB.Close()
	createSearchContractBaseTables(t, sqlDB)
	createSearchContractFTS(t, sqlDB)
	insertSearchContractFixtures(t, sqlDB)

	h := NewHandler(sqlDB)
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=Alpha&entities=chapters,messages,narrators", nil)
	w := httptest.NewRecorder()

	if err := h.search(w, req); err != nil {
		t.Fatalf("search returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	metadata, _ := body["searchMetadata"].(map[string]any)
	if metadata["degraded"] != false || metadata["ftsReady"] != true || metadata["mode"] != "fts5-with-like-fallback" || metadata["shortQuery"] != false {
		t.Fatalf("expected ready FTS metadata, got %#v", metadata)
	}
	fallbacks, _ := metadata["fallbacks"].([]any)
	if len(fallbacks) != 0 {
		t.Fatalf("FTS-ready search should not fall back, got %#v", fallbacks)
	}
	results, _ := body["results"].([]any)
	if len(results) != 3 {
		t.Fatalf("expected FTS results for all entities, got %#v", body["results"])
	}
	for _, raw := range results {
		result, _ := raw.(map[string]any)
		if score, ok := result["matchScore"].(float64); !ok || score <= 0 {
			t.Fatalf("FTS result should expose positive TS-style rank score, got %#v", result)
		}
		if snippet, ok := result["snippet"].(string); !ok || snippet == "" {
			t.Fatalf("FTS result should expose a non-empty TS-style snippet, got %#v", result)
		}
	}
}

func TestSearchFTSSupportsCJKTrigramSubstrings(t *testing.T) {
	sqlDB := openSearchContractDB(t)
	defer sqlDB.Close()
	createSearchContractBaseTables(t, sqlDB)
	createSearchContractFTS(t, sqlDB)
	insertSearchContractFixtures(t, sqlDB)
	insertSearchContractCJKFixtures(t, sqlDB)

	h := NewHandler(sqlDB)
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=%E5%8F%99%E4%BA%8B%E5%88%86%E5%8F%89&entities=chapters,messages,narrators", nil)
	w := httptest.NewRecorder()

	if err := h.search(w, req); err != nil {
		t.Fatalf("search returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	metadata, _ := body["searchMetadata"].(map[string]any)
	if metadata["degraded"] != false || metadata["ftsReady"] != true || metadata["shortQuery"] != false {
		t.Fatalf("expected ready non-degraded CJK FTS metadata, got %#v", metadata)
	}
	results, _ := body["results"].([]any)
	seen := map[string]bool{}
	for _, raw := range results {
		result, _ := raw.(map[string]any)
		resultType := result["type"].(string)
		seen[resultType] = true
		if snippet, ok := result["snippet"].(string); !ok || snippet == "" {
			t.Fatalf("CJK trigram result should expose a non-empty snippet, got %#v", result)
		}
		switch resultType {
		case "chapter", "narrator":
			title, _ := result["title"].(string)
			if !strings.Contains(title, "叙事分叉") {
				t.Fatalf("CJK trigram result should match title text, got %#v", result)
			}
		case "message":
			snippet, _ := result["snippet"].(string)
			if !strings.Contains(snippet, "叙事分叉") {
				t.Fatalf("CJK trigram message should expose matching snippet, got %#v", result)
			}
		}
	}
	for _, entityType := range []string{"chapter", "message", "narrator"} {
		if !seen[entityType] {
			t.Fatalf("expected CJK FTS results to include %s, got %#v", entityType, results)
		}
	}
}

func TestSearchLikeChapterSnippetMatchesTypeScriptFallback(t *testing.T) {
	sqlDB := openSearchContractDB(t)
	defer sqlDB.Close()
	createSearchContractBaseTables(t, sqlDB)
	insertSearchContractFixtures(t, sqlDB)
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, description, status, role, created_at, updated_at) VALUES ('c_desc', 'p1', 'Plain Title', 'Needle description body', 'active', 'branch', '2026-05-14T00:00:00Z', '2026-05-15T00:00:00Z')`); err != nil {
		t.Fatalf("insert description-only chapter: %v", err)
	}

	h := NewHandler(sqlDB)
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=Needle&entities=chapters", nil)
	w := httptest.NewRecorder()

	if err := h.search(w, req); err != nil {
		t.Fatalf("search returned error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	results, _ := body["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("expected one LIKE fallback chapter result, got %#v", results)
	}
	result := results[0].(map[string]any)
	if result["id"] != "c_desc" || result["matchField"] != "description" || result["snippet"] != "Needle description body" {
		t.Fatalf("LIKE fallback chapter snippet should match TS substr(description,title) contract, got %#v", result)
	}
}

func TestSearchSanitizesSpecialCharactersWithoutFailing(t *testing.T) {
	sqlDB := openSearchContractDB(t)
	defer sqlDB.Close()
	createSearchContractBaseTables(t, sqlDB)
	insertSearchContractFixtures(t, sqlDB)

	h := NewHandler(sqlDB)
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=Alpha%20%5Bdemo%5D%20!!!&entities=chapters", nil)
	w := httptest.NewRecorder()

	if err := h.search(w, req); err != nil {
		t.Fatalf("search returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	metadata, _ := body["searchMetadata"].(map[string]any)
	if metadata["mode"] == "" {
		t.Fatalf("expected machine-readable search mode, got %#v", metadata)
	}
}

func TestSearchIgnoresInvalidEntitiesAndReturnsEmptyResultSet(t *testing.T) {
	sqlDB := openSearchContractDB(t)
	defer sqlDB.Close()
	createSearchContractBaseTables(t, sqlDB)
	insertSearchContractFixtures(t, sqlDB)

	h := NewHandler(sqlDB)
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=Alpha&entities=projects,unknown", nil)
	w := httptest.NewRecorder()

	if err := h.search(w, req); err != nil {
		t.Fatalf("search returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	results := body["results"].([]any)
	projects := body["projects"].([]any)
	if len(results) != 0 || len(projects) != 0 {
		t.Fatalf("invalid entities should match TS empty-result behavior, got %#v", body)
	}
	metadata := body["searchMetadata"].(map[string]any)
	requested := metadata["requestedEntities"].([]any)
	if len(requested) != 0 || metadata["degraded"] != false {
		t.Fatalf("invalid entities should not create requested entities or degraded metadata, got %#v", metadata)
	}
}

func openSearchContractDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return sqlDB
}

func createSearchContractBaseTables(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, description TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, title TEXT, status TEXT, model TEXT, last_message_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, content_text TEXT, role TEXT, created_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec %s: %v", stmt, err)
		}
	}
}

func createSearchContractFTS(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE VIRTUAL TABLE chapters_fts USING fts5(title, description, content='chapters', content_rowid='rowid', tokenize='trigram')`,
		`CREATE VIRTUAL TABLE narrators_fts USING fts5(title, content='narrators', content_rowid='rowid', tokenize='trigram')`,
		`CREATE VIRTUAL TABLE narrator_messages_fts USING fts5(content_text, content='narrator_messages', content_rowid='rowid', tokenize='trigram')`,
		`CREATE TRIGGER chapters_fts_insert AFTER INSERT ON chapters BEGIN INSERT INTO chapters_fts(rowid, title, description) VALUES (new.rowid, new.title, new.description); END;`,
		`CREATE TRIGGER narrators_fts_insert AFTER INSERT ON narrators WHEN new.title IS NOT NULL BEGIN INSERT INTO narrators_fts(rowid, title) VALUES (new.rowid, new.title); END;`,
		`CREATE TRIGGER narrator_messages_fts_insert AFTER INSERT ON narrator_messages BEGIN INSERT INTO narrator_messages_fts(rowid, content_text) VALUES (new.rowid, new.content_text); END;`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec %s: %v", stmt, err)
		}
	}
}

func insertSearchContractFixtures(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO projects (id, name, description) VALUES ('p1', 'Alpha Project', 'demo')`,
		`INSERT INTO chapters (id, project_id, title, description, status, role, created_at, updated_at) VALUES ('c1', 'p1', 'Alpha Chapter', 'demo chapter', 'active', 'branch', '2026-05-14T00:00:00Z', '2026-05-14T00:00:00Z')`,
		`INSERT INTO narrators (id, chapter_id, title, status, model, last_message_at, created_at, updated_at) VALUES ('n1', 'c1', 'Alpha Narrator', 'idle', 'openai:gpt-4o-mini', '2026-05-14T00:00:00Z', '2026-05-14T00:00:00Z', '2026-05-14T00:00:00Z')`,
		`INSERT INTO narrator_messages (id, narrator_id, content_text, role, created_at) VALUES ('m1', 'n1', 'Alpha message body', 'user', '2026-05-14T00:00:00Z')`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec %s: %v", stmt, err)
		}
	}
}

func insertSearchContractCJKFixtures(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO projects (id, name, description) VALUES ('p_cjk', '中文项目', 'demo')`,
		`INSERT INTO chapters (id, project_id, title, description, status, role, created_at, updated_at) VALUES ('c_cjk', 'p_cjk', '叙事分叉章节', '中文检索正文', 'active', 'branch', '2026-05-14T00:00:00Z', '2026-05-16T00:00:00Z')`,
		`INSERT INTO narrators (id, chapter_id, title, status, model, last_message_at, created_at, updated_at) VALUES ('n_cjk', 'c_cjk', '叙事分叉叙述者', 'idle', 'openai:gpt-4o-mini', '2026-05-16T00:00:00Z', '2026-05-14T00:00:00Z', '2026-05-16T00:00:00Z')`,
		`INSERT INTO narrator_messages (id, narrator_id, content_text, role, created_at) VALUES ('m_cjk', 'n_cjk', '这是一条叙事分叉消息', 'assistant', '2026-05-16T00:00:00Z')`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec %s: %v", stmt, err)
		}
	}
}
