package misccompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
	gitx "narrafork-go/internal/git"
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
	if chapter["role"] != "branch" || chapter["reviewStatus"] != "converted" || chapter["reviewSourceChapterId"] != nil {
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
	var chapterRole, chapterStatus, reviewStatus, reviewSource, narratorStatus string
	var worktree sql.NullString
	if err := sqlDB.QueryRow(`SELECT role, status, review_status, review_source_chapter_id, worktree_path FROM chapters WHERE id = 'review'`).Scan(&chapterRole, &chapterStatus, &reviewStatus, &reviewSource, &worktree); err != nil {
		t.Fatalf("load dismissed chapter: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT status FROM narrators WHERE chapter_id = 'review'`).Scan(&narratorStatus); err != nil {
		t.Fatalf("load dismissed narrator: %v", err)
	}
	if chapterRole != "review" || chapterStatus != "abandoned" || reviewStatus != "dismissed" || reviewSource != "source" || worktree.Valid || narratorStatus != "archived" {
		t.Fatalf("dismiss should archive review, role=%s chapter=%s review=%s source=%s worktree=%v narrator=%s", chapterRole, chapterStatus, reviewStatus, reviewSource, worktree, narratorStatus)
	}

	seedReviewCompatRows(t, sqlDB, "concluded")
	w = serveReviewCompat(router, http.MethodPost, "/review/convert-to-subagent", "")
	if w.Code != http.StatusOK {
		t.Fatalf("convert status=%d body=%s", w.Code, w.Body.String())
	}
	convert := decodeReviewCompat(t, w)
	narrator, _ := convert["narrator"].(map[string]any)
	if narrator["type"] != "subagent" || narrator["variant"] != "subagent:review" || narrator["subagentType"] != "review" || narrator["parentNarratorId"] != "source_narrator" || narrator["chapterId"] != "source" || narrator["status"] != "idle" {
		t.Fatalf("convert should re-home review narrator under source, got %#v", convert)
	}
}

func TestReviewPromoteRouteKeepsGitAndConvertsReviewMetadata(t *testing.T) {
	sqlDB := openReviewCompatDB(t)
	defer sqlDB.Close()
	repo, worktree, branch := setupReviewCompatGit(t)
	seedReviewCompatRowsWithGit(t, sqlDB, "reviewing", repo, worktree, branch, repo)
	router := chi.NewRouter()
	(&Handler{DB: sqlDB, Git: gitx.NewService()}).RegisterReviews(router)

	w := serveReviewCompat(router, http.MethodPost, "/review/promote", "")
	if w.Code != http.StatusOK {
		t.Fatalf("promote status=%d body=%s", w.Code, w.Body.String())
	}

	var role, status, reviewStatus, reviewWorktree, narratorStatus, narratorType, narratorVariant, edgeType string
	var reviewSource sql.NullString
	if err := sqlDB.QueryRow(`SELECT role, status, review_status, review_source_chapter_id, worktree_path FROM chapters WHERE id = 'review'`).Scan(&role, &status, &reviewStatus, &reviewSource, &reviewWorktree); err != nil {
		t.Fatalf("load promoted chapter: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT status, type, variant FROM narrators WHERE id = 'review_narrator'`).Scan(&narratorStatus, &narratorType, &narratorVariant); err != nil {
		t.Fatalf("load promoted narrator: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT type FROM chapter_edges WHERE target_id = 'review'`).Scan(&edgeType); err != nil {
		t.Fatalf("load promoted edge: %v", err)
	}
	if role != "branch" || status != "active" || reviewStatus != "converted" || reviewSource.Valid || reviewWorktree != worktree || edgeType != "fork" {
		t.Fatalf("unexpected promoted chapter side effects role=%s status=%s review=%s source=%v worktree=%s edge=%s", role, status, reviewStatus, reviewSource, reviewWorktree, edgeType)
	}
	if narratorStatus != "working" || narratorType != "primary" || narratorVariant != "primary" {
		t.Fatalf("promote should keep primary narrator untouched, status=%s type=%s variant=%s", narratorStatus, narratorType, narratorVariant)
	}
	assertReviewCompatGitPresent(t, repo, worktree, branch)
}

func TestReviewDismissRouteCleansGitAndArchivesNarrator(t *testing.T) {
	sqlDB := openReviewCompatDB(t)
	defer sqlDB.Close()
	repo, worktree, branch := setupReviewCompatGit(t)
	seedReviewCompatRowsWithGit(t, sqlDB, "reviewing", repo, worktree, branch, repo)
	router := chi.NewRouter()
	(&Handler{DB: sqlDB, Git: gitx.NewService()}).RegisterReviews(router)

	w := serveReviewCompat(router, http.MethodPost, "/review/dismiss", "")
	if w.Code != http.StatusOK {
		t.Fatalf("dismiss status=%d body=%s", w.Code, w.Body.String())
	}

	var role, status, reviewStatus, reviewSource, narratorStatus, edgeType string
	var reviewWorktree sql.NullString
	if err := sqlDB.QueryRow(`SELECT role, status, review_status, review_source_chapter_id, worktree_path FROM chapters WHERE id = 'review'`).Scan(&role, &status, &reviewStatus, &reviewSource, &reviewWorktree); err != nil {
		t.Fatalf("load dismissed chapter: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT status FROM narrators WHERE id = 'review_narrator'`).Scan(&narratorStatus); err != nil {
		t.Fatalf("load dismissed narrator: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT type FROM chapter_edges WHERE target_id = 'review'`).Scan(&edgeType); err != nil {
		t.Fatalf("load dismissed edge: %v", err)
	}
	if role != "review" || status != "abandoned" || reviewStatus != "dismissed" || reviewSource != "source" || reviewWorktree.Valid || narratorStatus != "archived" || edgeType != "review" {
		t.Fatalf("unexpected dismiss side effects role=%s status=%s review=%s source=%s worktree=%v narrator=%s edge=%s", role, status, reviewStatus, reviewSource, reviewWorktree, narratorStatus, edgeType)
	}
	assertReviewCompatGitRemoved(t, repo, worktree, branch)
}

func TestReviewConvertRouteCleansGitAndRehomesNarrator(t *testing.T) {
	sqlDB := openReviewCompatDB(t)
	defer sqlDB.Close()
	repo, worktree, branch := setupReviewCompatGit(t)
	seedReviewCompatRowsWithGit(t, sqlDB, "concluded", repo, worktree, branch, repo)
	router := chi.NewRouter()
	(&Handler{DB: sqlDB, Git: gitx.NewService()}).RegisterReviews(router)

	w := serveReviewCompat(router, http.MethodPost, "/review/convert-to-subagent", "")
	if w.Code != http.StatusOK {
		t.Fatalf("convert status=%d body=%s", w.Code, w.Body.String())
	}

	var role, status, reviewStatus, reviewSource, edgeType string
	var reviewWorktree sql.NullString
	if err := sqlDB.QueryRow(`SELECT role, status, review_status, review_source_chapter_id, worktree_path FROM chapters WHERE id = 'review'`).Scan(&role, &status, &reviewStatus, &reviewSource, &reviewWorktree); err != nil {
		t.Fatalf("load converted chapter: %v", err)
	}
	var narratorType, narratorVariant, subagentType, parentID, chapterID, cwd, narratorStatus, substatus string
	if err := sqlDB.QueryRow(`SELECT type, variant, subagent_type, parent_narrator_id, chapter_id, cwd, status, substatus FROM narrators WHERE id = 'review_narrator'`).Scan(&narratorType, &narratorVariant, &subagentType, &parentID, &chapterID, &cwd, &narratorStatus, &substatus); err != nil {
		t.Fatalf("load converted narrator: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT type FROM chapter_edges WHERE target_id = 'review'`).Scan(&edgeType); err != nil {
		t.Fatalf("load converted edge: %v", err)
	}
	if role != "review" || status != "abandoned" || reviewStatus != "converted" || reviewSource != "source" || reviewWorktree.Valid || edgeType != "review" {
		t.Fatalf("unexpected convert chapter side effects role=%s status=%s review=%s source=%s worktree=%v edge=%s", role, status, reviewStatus, reviewSource, reviewWorktree, edgeType)
	}
	if narratorType != "subagent" || narratorVariant != "subagent:review" || subagentType != "review" || parentID != "source_narrator" || chapterID != "source" || cwd != repo || narratorStatus != "idle" || substatus != `["unread"]` {
		t.Fatalf("unexpected converted narrator type=%s variant=%s subagent=%s parent=%s chapter=%s cwd=%s status=%s substatus=%s", narratorType, narratorVariant, subagentType, parentID, chapterID, cwd, narratorStatus, substatus)
	}
	assertReviewCompatGitRemoved(t, repo, worktree, branch)
}

func openReviewCompatDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, git_path TEXT)`,
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
	seedReviewCompatRowsWithGit(t, sqlDB, reviewStatus, "", "", "review-branch", "/source-worktree")
}

func seedReviewCompatRowsWithGit(t *testing.T, sqlDB *sql.DB, reviewStatus, repo, reviewWorktree, reviewBranch, sourceWorktree string) {
	t.Helper()
	for _, stmt := range []string{
		`DELETE FROM chapter_edges`,
		`DELETE FROM narrators`,
		`DELETE FROM chapters`,
		`DELETE FROM projects`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("reset review compat rows: %v", err)
		}
	}
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, git_path) VALUES ('p1', ?)`, nullableReviewCompatString(repo)); err != nil {
		t.Fatalf("seed review compat project: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, role, status, worktree_path, branch, updated_at) VALUES ('source', 'p1', 'branch', 'active', ?, 'main', 'now')`, nullableReviewCompatString(sourceWorktree)); err != nil {
		t.Fatalf("seed source review compat chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, role, status, review_status, review_source_chapter_id, worktree_path, branch, updated_at) VALUES ('review', 'p1', 'review', 'active', ?, 'source', ?, ?, 'now')`, reviewStatus, nullableReviewCompatString(reviewWorktree), reviewBranch); err != nil {
		t.Fatalf("seed review compat chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, chapter_id, type, variant, cwd, status, created_at, updated_at) VALUES ('source_narrator', 'source', 'primary', 'primary', ?, 'idle', 'now', 'now')`, nullableReviewCompatString(sourceWorktree)); err != nil {
		t.Fatalf("seed source review compat narrator: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, chapter_id, type, variant, cwd, status, created_at, updated_at) VALUES ('review_narrator', 'review', 'primary', 'primary', ?, 'working', 'now', 'now')`, nullableReviewCompatString(reviewWorktree)); err != nil {
		t.Fatalf("seed review compat narrator: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapter_edges (id, source_id, target_id, type) VALUES ('edge_review', 'source', 'review', 'review')`); err != nil {
		t.Fatalf("seed review compat edge: %v", err)
	}
}

func nullableReviewCompatString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func setupReviewCompatGit(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()
	runGitReviewCompat(t, repo, "init", "-b", "main")
	runGitReviewCompat(t, repo, "config", "user.email", "test@example.com")
	runGitReviewCompat(t, repo, "config", "user.name", "Test User")
	writeFileReviewCompat(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runGitReviewCompat(t, repo, "add", "tracked.txt")
	runGitReviewCompat(t, repo, "commit", "-m", "initial")
	branch := "review/fixture"
	worktree := filepath.Join(repo, ".worktrees", "review-fixture")
	runGitReviewCompat(t, repo, "branch", branch)
	runGitReviewCompat(t, repo, "worktree", "add", worktree, branch)
	return repo, worktree, branch
}

func assertReviewCompatGitPresent(t *testing.T, repo, worktree, branch string) {
	t.Helper()
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("review worktree should remain: %v", err)
	}
	if branches := strings.TrimSpace(runGitReviewCompat(t, repo, "branch", "--list", branch)); branches == "" {
		t.Fatalf("review branch should remain")
	}
}

func assertReviewCompatGitRemoved(t *testing.T, repo, worktree, branch string) {
	t.Helper()
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("review worktree should be removed, err=%v", err)
	}
	if branches := strings.TrimSpace(runGitReviewCompat(t, repo, "branch", "--list", branch)); branches != "" {
		t.Fatalf("review branch should be deleted, got %q", branches)
	}
}

func runGitReviewCompat(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}

func writeFileReviewCompat(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
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
