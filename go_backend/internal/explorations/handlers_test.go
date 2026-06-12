package explorations

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	chaptersvc "narrafork-go/internal/chapters"
	gitx "narrafork-go/internal/git"
)

func TestCreateExplorationGroupForksBranches(t *testing.T) {
	router, database := newExplorationTestRouter(t)
	body := map[string]any{
		"projectId":     "project",
		"title":         "Experiment",
		"baseChapterId": "root",
		"branches": []any{
			map[string]any{"title": "Approach A", "inheritMode": "fresh"},
			map[string]any{"title": "Approach B", "description": "try B", "inheritMode": "fresh"},
		},
	}
	w := postExplorationJSON(t, router, "/", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var group struct {
		ID       string `json:"id"`
		Branches []struct {
			ID                 string  `json:"id"`
			Role               string  `json:"role"`
			ExplorationGroupID *string `json:"explorationGroupId"`
			GroupLabel         *string `json:"groupLabel"`
		} `json:"branches"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &group); err != nil {
		t.Fatalf("decode group: %v", err)
	}
	if group.ID == "" || len(group.Branches) != 2 {
		t.Fatalf("expected group with two branches, got %#v", group)
	}
	for _, branch := range group.Branches {
		if branch.Role != "exploration" || branch.ExplorationGroupID == nil || *branch.ExplorationGroupID != group.ID || branch.GroupLabel == nil || *branch.GroupLabel != "Experiment" {
			t.Fatalf("unexpected branch payload: %#v", branch)
		}
	}
	var linked, forkEdges int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapters WHERE exploration_group_id = ? AND role = 'exploration' AND group_label = 'Experiment'`, group.ID).Scan(&linked); err != nil {
		t.Fatalf("count linked chapters: %v", err)
	}
	if linked != 2 {
		t.Fatalf("expected 2 linked exploration chapters, got %d", linked)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_edges WHERE project_id = 'project' AND source_id = 'root' AND type = 'fork'`).Scan(&forkEdges); err != nil {
		t.Fatalf("count fork edges: %v", err)
	}
	if forkEdges != 2 {
		t.Fatalf("expected 2 fork edges, got %d", forkEdges)
	}
}

func TestDecideExplorationGroupAbandonsUnselectedBranches(t *testing.T) {
	router, database := newExplorationTestRouter(t)
	groupID, branchIDs := createExplorationGroupFixture(t, router)
	w := postExplorationJSON(t, router, "/"+groupID+"/decide", map[string]any{"chapterId": branchIDs[0]})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var status, decided string
	if err := database.QueryRow(`SELECT status, decided_chapter_id FROM exploration_groups WHERE id = ?`, groupID).Scan(&status, &decided); err != nil {
		t.Fatalf("query group: %v", err)
	}
	if status != "decided" || decided != branchIDs[0] {
		t.Fatalf("unexpected group decision: status=%s decided=%s", status, decided)
	}
	var selectedStatus, otherStatus string
	if err := database.QueryRow(`SELECT status FROM chapters WHERE id = ?`, branchIDs[0]).Scan(&selectedStatus); err != nil {
		t.Fatalf("query selected: %v", err)
	}
	if err := database.QueryRow(`SELECT status FROM chapters WHERE id = ?`, branchIDs[1]).Scan(&otherStatus); err != nil {
		t.Fatalf("query other: %v", err)
	}
	if selectedStatus != "active" || otherStatus != "abandoned" {
		t.Fatalf("unexpected branch statuses selected=%s other=%s", selectedStatus, otherStatus)
	}
}

func TestAbandonExplorationGroupAbandonsAllBranches(t *testing.T) {
	router, database := newExplorationTestRouter(t)
	groupID, _ := createExplorationGroupFixture(t, router)
	req := httptest.NewRequest(http.MethodPost, "/"+groupID+"/abandon", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var active, abandoned int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapters WHERE exploration_group_id = ? AND status = 'active'`, groupID).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapters WHERE exploration_group_id = ? AND status = 'abandoned'`, groupID).Scan(&abandoned); err != nil {
		t.Fatalf("count abandoned: %v", err)
	}
	if active != 0 || abandoned != 2 {
		t.Fatalf("unexpected statuses active=%d abandoned=%d", active, abandoned)
	}
}

func createExplorationGroupFixture(t *testing.T, router http.Handler) (string, []string) {
	t.Helper()
	w := postExplorationJSON(t, router, "/", map[string]any{
		"projectId":     "project",
		"title":         "Decision Group",
		"baseChapterId": "root",
		"branches": []any{
			map[string]any{"title": "Decision A", "inheritMode": "fresh"},
			map[string]any{"title": "Decision B", "inheritMode": "fresh"},
		},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create fixture group: %d %s", w.Code, w.Body.String())
	}
	var group struct {
		ID       string `json:"id"`
		Branches []struct {
			ID string `json:"id"`
		} `json:"branches"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &group); err != nil {
		t.Fatalf("decode fixture group: %v", err)
	}
	if len(group.Branches) != 2 {
		t.Fatalf("expected two fixture branches, got %#v", group)
	}
	return group.ID, []string{group.Branches[0].ID, group.Branches[1].ID}
}

// Wave 292: Go exploration group create must mirror the TypeScript
// createExplorationGroupSchema Zod bounds — title max 200, description max 2000,
// branches 2..10 with each branch title 1..200 / description max 2000 /
// inheritMode enum — and reject out-of-range values with 400.
func TestCreateExplorationGroupValidatesZodBoundsLikeTypeScript(t *testing.T) {
	router, _ := newExplorationTestRouter(t)
	longText := strings.Repeat("x", 201)
	longDesc := strings.Repeat("d", 2001)
	validBranches := []any{
		map[string]any{"title": "A", "inheritMode": "fresh"},
		map[string]any{"title": "B", "inheritMode": "fresh"},
	}
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"overlong title", map[string]any{"projectId": "project", "title": longText, "baseChapterId": "root", "branches": validBranches}},
		{"overlong description", map[string]any{"projectId": "project", "title": "Exp", "description": longDesc, "baseChapterId": "root", "branches": validBranches}},
		{"too few branches", map[string]any{"projectId": "project", "title": "Exp", "baseChapterId": "root", "branches": []any{map[string]any{"title": "A"}}}},
		{"branch title overlong", map[string]any{"projectId": "project", "title": "Exp", "baseChapterId": "root", "branches": []any{map[string]any{"title": longText}, map[string]any{"title": "B"}}}},
		{"branch invalid inheritMode", map[string]any{"projectId": "project", "title": "Exp", "baseChapterId": "root", "branches": []any{map[string]any{"title": "A", "inheritMode": "partial"}, map[string]any{"title": "B"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := postExplorationJSON(t, router, "/", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("create exploration %q status = %d body=%s, want 400", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func postExplorationJSON(t *testing.T, router http.Handler, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func newExplorationTestRouter(t *testing.T) (*chi.Mux, *sql.DB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	database := openExplorationTestDB(t)
	repo := newExplorationTestRepo(t)
	seedExplorationProject(t, database, repo)
	chapterService := &chaptersvc.Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}
	handler := NewHandler(database, chapterService)
	router := chi.NewRouter()
	router.Route("/", handler.RegisterRoutes)
	return router, database
}

func openExplorationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT, merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT, container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT, start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER, anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER, panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT, last_accessed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT, type TEXT, metadata TEXT, created_at TEXT)`,
		`CREATE TABLE exploration_groups (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, base_chapter_id TEXT, status TEXT, decided_chapter_id TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, variant TEXT, model TEXT, message_count INTEGER DEFAULT 0, message_version INTEGER DEFAULT 0, last_message_at TEXT, updated_at TEXT, created_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("schema: %v\n%s", err, stmt)
		}
	}
	return database
}

func newExplorationTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runExplorationGit(t, repo, "init")
	runExplorationGit(t, repo, "checkout", "-b", "main")
	runExplorationGit(t, repo, "config", "user.name", "Test User")
	runExplorationGit(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runExplorationGit(t, repo, "add", ".")
	runExplorationGit(t, repo, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte(".worktrees/\n"), 0o644); err != nil {
		t.Fatalf("write exclude: %v", err)
	}
	return repo
}

func seedExplorationProject(t *testing.T, database *sql.DB, repo string) {
	t.Helper()
	head := runExplorationGit(t, repo, "rev-parse", "HEAD")
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, chapter_settings) VALUES ('project', 'Project', ?, 'main', '{}')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, is_root, head_commit_sha, start_commit_sha, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('root', 'project', 'Root', 'active', 'trunk', 'main', ?, 'main', 1, ?, ?, 1, 0, 0, 0, 0, 'now', 'now')`, repo, head, head); err != nil {
		t.Fatalf("insert root chapter: %v", err)
	}
}

func runExplorationGit(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(bytes.TrimSpace(out))
}
