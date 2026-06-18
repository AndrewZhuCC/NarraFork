package chapters

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

// TestCreateChapterTitleLengthUsesJSStringLength pins the chapter create handler's
// title/description length validation to JavaScript String.prototype.length
// (UTF-16 code units), matching the TS createChapterSchema
// (server/lib/validators/chapters.ts): title z.string().min(1).max(200),
// description z.string().max(2000). Two divergences are covered: byte-vs-character
// (a 200-char CJK title is 600 UTF-8 bytes but 200 UTF-16 units, must pass) and
// rune-vs-UTF-16 (an emoji is 1 rune but 2 UTF-16 units, so 101 emoji = 202 units
// must be rejected even though a rune-count check would see only 101).
func TestCreateChapterTitleLengthUsesJSStringLength(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	// projects table with no matching row: a title that passes length validation
	// reaches Create and fails with 404 "Project not found", NOT a 400 length error.
	if _, err := sqlDB.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT)`); err != nil {
		t.Fatalf("create projects table: %v", err)
	}
	h := NewHandler(&Service{DB: sqlDB})

	// 200 CJK chars = 600 bytes = 200 UTF-16 units: must pass the 200-char limit.
	title200 := strings.Repeat("章", 200)
	w := serveChapterCreate(h, `{"projectId":"missing","title":"`+title200+`"}`)
	if bytes.Contains(w.Body.Bytes(), []byte("title must be at most 200 characters")) {
		t.Fatalf("200-char CJK title should pass length validation, got %s", w.Body.String())
	}

	// 201 CJK chars must still be rejected with the length error.
	title201 := strings.Repeat("章", 201)
	w2 := serveChapterCreate(h, `{"projectId":"missing","title":"`+title201+`"}`)
	if w2.Code != http.StatusBadRequest || !bytes.Contains(w2.Body.Bytes(), []byte("title must be at most 200 characters")) {
		t.Fatalf("201-char CJK title should be rejected with length error, got %d %s", w2.Code, w2.Body.String())
	}

	// 100 emoji = 200 UTF-16 units: must pass (boundary).
	emoji100 := strings.Repeat("😀", 100)
	w3 := serveChapterCreate(h, `{"projectId":"missing","title":"`+emoji100+`"}`)
	if bytes.Contains(w3.Body.Bytes(), []byte("title must be at most 200 characters")) {
		t.Fatalf("100-emoji title (200 UTF-16 units) should pass, got %s", w3.Body.String())
	}

	// 101 emoji = 202 UTF-16 units: must be rejected. A rune-count check would see
	// only 101 and wrongly accept it, diverging from the TS UTF-16 semantics.
	emoji101 := strings.Repeat("😀", 101)
	w4 := serveChapterCreate(h, `{"projectId":"missing","title":"`+emoji101+`"}`)
	if w4.Code != http.StatusBadRequest || !bytes.Contains(w4.Body.Bytes(), []byte("title must be at most 200 characters")) {
		t.Fatalf("101-emoji title (202 UTF-16 units) should be rejected, got %d %s", w4.Code, w4.Body.String())
	}
}

func serveChapterCreate(h *Handler, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/chapters", h.RegisterRoutes)
	req := httptest.NewRequest(http.MethodPost, "/chapters", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestCreateChapterBaseBranchValidatesGitBranchName pins the chapter create
// baseBranch validation to the TS createChapterSchema baseBranch: gitBranchName
// (server/lib/validators/common.ts: z.string().regex(/^[a-zA-Z0-9._\-/]+$/)). A
// present non-empty baseBranch with invalid characters must be rejected with 400
// "Invalid branch name" before any project/git work; an empty/omitted baseBranch
// is allowed (falls back to the project default).
func TestCreateChapterBaseBranchValidatesGitBranchName(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT)`); err != nil {
		t.Fatalf("create projects table: %v", err)
	}
	h := NewHandler(&Service{DB: sqlDB})

	// Invalid branch names (spaces, special chars) must be rejected with 400
	// "Invalid branch name" before the project lookup (no matching project row).
	for _, bad := range []string{"bad branch", "feat!", "a~b", "with:colon", "star*"} {
		w := serveChapterCreate(h, `{"projectId":"missing","title":"T","baseBranch":"`+bad+`"}`)
		if w.Code != http.StatusBadRequest || !bytes.Contains(w.Body.Bytes(), []byte("Invalid branch name")) {
			t.Fatalf("baseBranch %q should be rejected with 'Invalid branch name', got %d %s", bad, w.Code, w.Body.String())
		}
	}

	// A well-formed baseBranch passes the regex and proceeds to the project lookup
	// (which 404s here since no project exists) — i.e. NOT a branch-name 400.
	for _, ok := range []string{"main", "feature/new-thing", "release-1.2.3", "user_branch"} {
		w := serveChapterCreate(h, `{"projectId":"missing","title":"T","baseBranch":"`+ok+`"}`)
		if bytes.Contains(w.Body.Bytes(), []byte("Invalid branch name")) {
			t.Fatalf("valid baseBranch %q should pass the regex, got %d %s", ok, w.Code, w.Body.String())
		}
	}

	// Omitted baseBranch is allowed (no branch-name rejection).
	w := serveChapterCreate(h, `{"projectId":"missing","title":"T"}`)
	if bytes.Contains(w.Body.Bytes(), []byte("Invalid branch name")) {
		t.Fatalf("omitted baseBranch should be allowed, got %d %s", w.Code, w.Body.String())
	}
}
