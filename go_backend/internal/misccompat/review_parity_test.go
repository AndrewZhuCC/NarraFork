package misccompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

func TestLatestReviewConclusionUsesTSFindingsJsonShape(t *testing.T) {
	sqlDB := openReviewCompatDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`INSERT INTO review_conclusions (id, review_chapter_id, source_chapter_id, verdict, findings_json, created_at) VALUES ('rc1', 'review', 'source', 'approve', '[{"severity":"minor","message":"ok"}]', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert conclusion: %v", err)
	}
	payload, err := (&Handler{DB: sqlDB}).latestReviewConclusion(context.Background(), "review", "review")
	if err != nil {
		t.Fatalf("latestReviewConclusion: %v", err)
	}
	conclusion, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("payload = %#v", payload)
	}
	if conclusion["findingsJson"] == nil || conclusion["verdict"] != "approve" || conclusion["reviewChapterId"] != "review" {
		t.Fatalf("conclusion not TS shaped: %#v", conclusion)
	}
}

func TestReviewClosureRoutesPromoteDismissAndConvert(t *testing.T) {
	sqlDB := openReviewCompatDB(t)
	defer sqlDB.Close()
	seedReviewCompatRows(t, sqlDB, "reviewing")
	handler := &Handler{DB: sqlDB}
	router := chi.NewRouter()
	handler.RegisterReviews(router)

	w := serveReviewCompat(router, http.MethodPost, "/review/promote", "")
	if w.Code != http.StatusOK {
		t.Fatalf("promote status=%d body=%s", w.Code, w.Body.String())
	}
	promote := decodeReviewCompat(t, w)
	chapter, _ := promote["chapter"].(map[string]any)
	if chapter["role"] != "branch" || chapter["reviewStatus"] != "converted" {
		t.Fatalf("promote should convert review to branch, got %#v", promote)
	}
	var edgeType string
	if err := sqlDB.QueryRow(`SELECT type FROM chapter_edges WHERE target_id = 'review'`).Scan(&edgeType); err != nil {
		t.Fatalf("load promoted edge: %v", err)
	}
	if edgeType != "fork" {
		t.Fatalf("promote should convert review edge to fork, got %q", edgeType)
	}

	seedReviewCompatRows(t, sqlDB, "reviewing")
	w = serveReviewCompat(router, http.MethodPost, "/review/dismiss", "")
	if w.Code != http.StatusOK {
		t.Fatalf("dismiss status=%d body=%s", w.Code, w.Body.String())
	}
	var chapterStatus, reviewStatus, narratorStatus string
	if err := sqlDB.QueryRow(`SELECT status, review_status FROM chapters WHERE id = 'review'`).Scan(&chapterStatus, &reviewStatus); err != nil {
		t.Fatalf("load dismissed chapter: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT status FROM narrators WHERE chapter_id = 'review'`).Scan(&narratorStatus); err != nil {
		t.Fatalf("load dismissed narrator: %v", err)
	}
	if chapterStatus != "abandoned" || reviewStatus != "dismissed" || narratorStatus != "archived" {
		t.Fatalf("dismiss should archive review, chapter=%s review=%s narrator=%s", chapterStatus, reviewStatus, narratorStatus)
	}

	seedReviewCompatRows(t, sqlDB, "concluded")
	w = serveReviewCompat(router, http.MethodPost, "/review/convert-to-subagent", "")
	if w.Code != http.StatusOK {
		t.Fatalf("convert status=%d body=%s", w.Code, w.Body.String())
	}
	convert := decodeReviewCompat(t, w)
	narrator, _ := convert["narrator"].(map[string]any)
	if narrator["type"] != "subagent" || narrator["variant"] != "subagent:review" || narrator["parentNarratorId"] != "source_narrator" {
		t.Fatalf("convert should re-home review narrator under source, got %#v", convert)
	}
}

func openReviewCompatDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	stmts := []string{
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, role TEXT, status TEXT, review_status TEXT, review_source_chapter_id TEXT, worktree_path TEXT, branch TEXT, updated_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, type TEXT, variant TEXT, subagent_type TEXT, parent_narrator_id TEXT, cwd TEXT, status TEXT, substatus TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, source_id TEXT, target_id TEXT, type TEXT)`,
		`CREATE TABLE review_conclusions (id TEXT PRIMARY KEY, review_chapter_id TEXT, source_chapter_id TEXT, verdict TEXT, findings_json TEXT, created_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}

func seedReviewCompatRows(t *testing.T, sqlDB *sql.DB, reviewStatus string) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM chapter_edges`,
		`DELETE FROM narrators`,
		`DELETE FROM chapters`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("reset review compat rows: %v", err)
		}
	}
	stmts := []string{
		`INSERT INTO chapters (id, project_id, role, status, branch, updated_at) VALUES ('source', 'p1', 'branch', 'active', 'main', 'now')`,
		`INSERT INTO chapters (id, project_id, role, status, review_status, review_source_chapter_id, branch, updated_at) VALUES ('review', 'p1', 'review', 'active', '` + reviewStatus + `', 'source', 'review-branch', 'now')`,
		`INSERT INTO narrators (id, chapter_id, type, variant, status, created_at, updated_at) VALUES ('source_narrator', 'source', 'primary', 'primary', 'idle', 'now', 'now')`,
		`INSERT INTO narrators (id, chapter_id, type, variant, status, created_at, updated_at) VALUES ('review_narrator', 'review', 'primary', 'primary', 'working', 'now', 'now')`,
		`INSERT INTO chapter_edges (id, source_id, target_id, type) VALUES ('edge_review', 'source', 'review', 'review')`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("seed review compat row: %v\n%s", err, stmt)
		}
	}
}

func serveReviewCompat(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func decodeReviewCompat(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode review response: %v body=%s", err, w.Body.String())
	}
	return body
}
