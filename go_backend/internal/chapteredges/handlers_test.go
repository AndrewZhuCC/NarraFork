package chapteredges

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

func TestCreateChapterEdgeValidationErrorsUseTSShape(t *testing.T) {
	sqlDB := openChapterEdgeTestDB(t)
	defer sqlDB.Close()
	router := chapterEdgeTestRouter(sqlDB)

	cases := []struct {
		name      string
		body      string
		wantError string
	}{
		{
			name:      "self edge",
			body:      `{"sourceId":"chapter-a","targetId":"chapter-a","type":"dependency"}`,
			wantError: "Cannot create a chapter edge to itself",
		},
		{
			name:      "cross project",
			body:      `{"sourceId":"chapter-a","targetId":"chapter-other","type":"dependency"}`,
			wantError: "Cannot create chapter edge between chapters of different projects",
		},
		{
			name:      "invalid type",
			body:      `{"sourceId":"chapter-a","targetId":"chapter-b","type":"sidequest"}`,
			wantError: "Invalid type: sidequest",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := performChapterEdgeRequest(router, http.MethodPost, "/", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			body := decodeJSONBody(t, w)
			if body["code"] != "VALIDATION_ERROR" || body["error"] != tc.wantError {
				t.Fatalf("unexpected TS-compatible error body: %#v", body)
			}
			if _, ok := body["message"]; ok {
				t.Fatalf("error body should use TS shape {error, code}, got %#v", body)
			}
		})
	}
}

func TestCreateChapterEdgeUsesTSMetadataContract(t *testing.T) {
	sqlDB := openChapterEdgeTestDB(t)
	defer sqlDB.Close()
	router := chapterEdgeTestRouter(sqlDB)

	w := performChapterEdgeRequest(router, http.MethodPost, "/", `{"sourceId":"chapter-a","targetId":"chapter-b","type":"dependency","metadata":{"description":"needs upstream","extra":"ignored"}}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected create to succeed, got %d: %s", w.Code, w.Body.String())
	}
	body := decodeJSONBody(t, w)
	metadata, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("response metadata should be an object, got %#v", body["metadata"])
	}
	if metadata["description"] != "needs upstream" || metadata["lastSyncedCommit"] != nil {
		t.Fatalf("unexpected dependency metadata: %#v", metadata)
	}
	if _, ok := metadata["extra"]; ok {
		t.Fatalf("unknown metadata fields should be stripped to match TS contract: %#v", metadata)
	}
}

func TestCreateChapterEdgeRejectsTooLongDescription(t *testing.T) {
	sqlDB := openChapterEdgeTestDB(t)
	defer sqlDB.Close()
	router := chapterEdgeTestRouter(sqlDB)

	body := `{"sourceId":"chapter-a","targetId":"chapter-b","type":"dependency","metadata":{"description":"` + strings.Repeat("x", 501) + `"}}`
	w := performChapterEdgeRequest(router, http.MethodPost, "/", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected long description to return 400, got %d: %s", w.Code, w.Body.String())
	}
	decoded := decodeJSONBody(t, w)
	if decoded["code"] != "VALIDATION_ERROR" || decoded["error"] != "metadata.description must be at most 500 characters" {
		t.Fatalf("unexpected long description error body: %#v", decoded)
	}
}

func TestCreateChapterEdgeRejectsDuplicates(t *testing.T) {
	sqlDB := openChapterEdgeTestDB(t)
	defer sqlDB.Close()
	router := chapterEdgeTestRouter(sqlDB)

	body := `{"sourceId":"chapter-a","targetId":"chapter-b","type":"dependency"}`
	first := performChapterEdgeRequest(router, http.MethodPost, "/", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("expected first create to succeed, got %d: %s", first.Code, first.Body.String())
	}

	second := performChapterEdgeRequest(router, http.MethodPost, "/", body)
	if second.Code != http.StatusBadRequest {
		t.Fatalf("expected duplicate to return 400, got %d: %s", second.Code, second.Body.String())
	}
	decoded := decodeJSONBody(t, second)
	if decoded["code"] != "VALIDATION_ERROR" || decoded["error"] != "Chapter edge already exists" {
		t.Fatalf("unexpected duplicate error body: %#v", decoded)
	}
}

func TestChapterEdgeManualOperationsOnlyAllowDependency(t *testing.T) {
	sqlDB := openChapterEdgeTestDB(t)
	defer sqlDB.Close()
	router := chapterEdgeTestRouter(sqlDB)

	for _, edgeType := range []string{"fork", "merge", "review", "cherry_pick"} {
		w := performChapterEdgeRequest(router, http.MethodPost, "/", `{"sourceId":"chapter-a","targetId":"chapter-b","type":"`+edgeType+`"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected manual %s create to be rejected, got %d: %s", edgeType, w.Code, w.Body.String())
		}
		decoded := decodeJSONBody(t, w)
		if decoded["code"] != "VALIDATION_ERROR" || decoded["error"] != "Manual chapter edge create is only supported for dependency edges" {
			t.Fatalf("unexpected manual create rejection for %s: %#v", edgeType, decoded)
		}
	}

	dependency := performChapterEdgeRequest(router, http.MethodPost, "/", `{"sourceId":"chapter-a","targetId":"chapter-b","type":"dependency"}`)
	if dependency.Code != http.StatusCreated {
		t.Fatalf("expected dependency edge create to succeed, got %d: %s", dependency.Code, dependency.Body.String())
	}
	dependencyID := stringBodyValue(t, dependency, "id")
	dependencyDelete := performChapterEdgeRequest(router, http.MethodDelete, "/"+dependencyID, "")
	if dependencyDelete.Code != http.StatusOK {
		t.Fatalf("expected dependency edge delete to succeed, got %d: %s", dependencyDelete.Code, dependencyDelete.Body.String())
	}

	_, err := sqlDB.Exec(`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('edge-fork', 'project-1', 'chapter-a', 'chapter-b', 'fork', '{}', 'now')`)
	if err != nil {
		t.Fatalf("insert system-owned fork edge: %v", err)
	}
	forkDelete := performChapterEdgeRequest(router, http.MethodDelete, "/edge-fork", "")
	if forkDelete.Code != http.StatusBadRequest {
		t.Fatalf("expected manual fork delete to be rejected, got %d: %s", forkDelete.Code, forkDelete.Body.String())
	}
	decoded := decodeJSONBody(t, forkDelete)
	if decoded["code"] != "VALIDATION_ERROR" || decoded["error"] != "Manual chapter edge delete is only supported for dependency edges" {
		t.Fatalf("unexpected manual delete rejection: %#v", decoded)
	}
	var forkCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM chapter_edges WHERE id = 'edge-fork'`).Scan(&forkCount); err != nil {
		t.Fatalf("count fork edge: %v", err)
	}
	if forkCount != 1 {
		t.Fatalf("system-owned fork edge was deleted despite rejection")
	}
}

func TestListChapterEdgesRejectsInvalidType(t *testing.T) {
	sqlDB := openChapterEdgeTestDB(t)
	defer sqlDB.Close()
	router := chapterEdgeTestRouter(sqlDB)

	w := performChapterEdgeRequest(router, http.MethodGet, "/?projectId=project-1&type=sidequest", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	decoded := decodeJSONBody(t, w)
	if decoded["code"] != "VALIDATION_ERROR" || decoded["error"] != "Invalid type: sidequest" {
		t.Fatalf("unexpected invalid list type error: %#v", decoded)
	}
}

func openChapterEdgeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = sqlDB.Exec(`
		CREATE TABLE chapters (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			parent_chapter_id TEXT,
			merged_into_chapter_id TEXT,
			updated_at TEXT
		);
		CREATE TABLE chapter_edges (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			type TEXT NOT NULL,
			metadata TEXT,
			created_at TEXT NOT NULL
		);
		INSERT INTO chapters (id, project_id, updated_at) VALUES
			('chapter-a', 'project-1', '2026-05-14T00:00:00Z'),
			('chapter-b', 'project-1', '2026-05-14T00:00:00Z'),
			('chapter-other', 'project-2', '2026-05-14T00:00:00Z');
	`)
	if err != nil {
		sqlDB.Close()
		t.Fatalf("create test schema: %v", err)
	}
	return sqlDB
}

func chapterEdgeTestRouter(sqlDB *sql.DB) http.Handler {
	router := chi.NewRouter()
	NewHandler(sqlDB).RegisterRoutes(router)
	return router
}

func performChapterEdgeRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	reader = strings.NewReader(body)
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func decodeJSONBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode JSON body: %v; raw=%s", err, w.Body.String())
	}
	return body
}

func stringBodyValue(t *testing.T, w *httptest.ResponseRecorder, key string) string {
	t.Helper()
	body := decodeJSONBody(t, w)
	value, _ := body[key].(string)
	if value == "" {
		t.Fatalf("response missing string %q: %#v", key, body)
	}
	return value
}

func assertNullStringValue(t *testing.T, sqlDB *sql.DB, query, want string) {
	t.Helper()
	var value sql.NullString
	if err := sqlDB.QueryRow(query).Scan(&value); err != nil {
		t.Fatalf("query value: %v", err)
	}
	if !value.Valid || value.String != want {
		t.Fatalf("expected %q, got valid=%v value=%q", want, value.Valid, value.String)
	}
}

func assertNullStringInvalid(t *testing.T, sqlDB *sql.DB, query string) {
	t.Helper()
	var value sql.NullString
	if err := sqlDB.QueryRow(query).Scan(&value); err != nil {
		t.Fatalf("query value: %v", err)
	}
	if value.Valid {
		t.Fatalf("expected NULL, got %q", value.String)
	}
}
