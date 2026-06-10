package projects

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"narrafork-go/internal/chapters"
	gitx "narrafork-go/internal/git"

	_ "modernc.org/sqlite"
)

type fakeTerminalKiller struct {
	killed []string
	err    error
}

func (f *fakeTerminalKiller) Kill(_ context.Context, terminalID string) error {
	f.killed = append(f.killed, terminalID)
	return f.err
}

func TestDeleteCleansProjectScopedRows(t *testing.T) {
	ctx := context.Background()
	sqlDB := newProjectDeleteTestDB(t)
	killer := &fakeTerminalKiller{}
	userBroadcasts := []struct {
		userID  string
		payload map[string]any
	}{}
	service := &Service{
		DB:       sqlDB,
		Chapters: &chapters.Service{DB: sqlDB, TerminalRuntime: killer},
		BroadcastUser: func(userID string, payload any) {
			item, ok := payload.(map[string]any)
			if !ok {
				t.Fatalf("user broadcast payload should be map, got %#v", payload)
			}
			userBroadcasts = append(userBroadcasts, struct {
				userID  string
				payload map[string]any
			}{userID: userID, payload: item})
		},
	}

	result, err := service.Delete(ctx, "p1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !result.OK || result.ProjectID != "p1" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.CleanupErrors) != 0 {
		t.Fatalf("cleanup errors = %#v", result.CleanupErrors)
	}
	if len(killer.killed) != 2 || killer.killed[0] != "term_c1" || killer.killed[1] != "term_n_child" {
		t.Fatalf("killed terminals = %#v", killer.killed)
	}

	for _, table := range []string{
		"projects", "chapters", "chapter_edges", "chapter_commits", "exploration_groups",
		"narrators", "narrator_goals", "narrator_messages", "narrator_message_refs",
		"narrator_sidecars", "narrator_tool_calls", "narrator_file_snapshots",
		"narrator_patches", "narrator_buffered_messages", "background_tasks",
		"terminals", "terminal_tabs", "terminal_view_state", "container_instances",
		"port_allocations", "merge_sessions", "review_conclusions",
		"volume_snapshots", "volume_snapshot_applications", "api_requests",
		"hooks", "gateway_session_mappings", "benchmark_task_results",
		"narrator_whitelist_dirs", "narrator_blacklist_dirs", "narrator_whitelist_cmds",
		"narrator_blacklist_cmds", "workspaces",
	} {
		if got := countWhere(t, sqlDB, table, "id LIKE '%p1%' OR id IN ('c1','root1','n1','n_child','m1','ref1','side1','tool1','goal1','snap1','patch1','buf1','bg1','term_c1','term_n_child','tab1','view1','ci1','merge1','rc1','vs1','vsa1','api1','hook1','gw1','bench1','wd1','bd1','wc1','bc1','ws1','edge1','commit1')"); got != 0 {
			t.Fatalf("%s still has p1-scoped rows: %d", table, got)
		}
	}

	if got := countWhere(t, sqlDB, "projects", "id = 'p2'"); got != 1 {
		t.Fatalf("p2 project count = %d", got)
	}
	if got := countWhere(t, sqlDB, "chapters", "id = 'c2'"); got != 1 {
		t.Fatalf("p2 chapter count = %d", got)
	}
	if got := countWhere(t, sqlDB, "narrators", "id = 'n2'"); got != 1 {
		t.Fatalf("p2 narrator count = %d", got)
	}
	if got := countWhere(t, sqlDB, "workspaces", "id = 'ws2'"); got != 1 {
		t.Fatalf("unrelated workspace count = %d", got)
	}
	var tabs string
	if err := sqlDB.QueryRow(`SELECT recent_tabs FROM user_preferences WHERE user_id = 'u1'`).Scan(&tabs); err != nil {
		t.Fatalf("recent_tabs: %v", err)
	}
	if tabs != `[{"id":"p2","title":"Other","type":"project"}]` {
		t.Fatalf("recent_tabs = %s", tabs)
	}
	if len(userBroadcasts) != 1 || userBroadcasts[0].userID != "u1" || userBroadcasts[0].payload["type"] != "user:recent_tabs_snapshot" {
		t.Fatalf("expected recent-tabs snapshot broadcast, got %#v", userBroadcasts)
	}
	items, ok := userBroadcasts[0].payload["tabs"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["id"] != "p2" {
		t.Fatalf("unexpected broadcast tabs: %#v", userBroadcasts[0].payload["tabs"])
	}
}

func TestDeleteRefreshesProxyCacheAfterCommittedDelete(t *testing.T) {
	ctx := context.Background()
	sqlDB := newProjectDeleteTestDB(t)
	mustExec(t, sqlDB, `UPDATE projects SET proxy_domain = 'dev.example.test' WHERE id = 'p1'`)
	calls := 0
	service := &Service{
		DB:       sqlDB,
		Chapters: &chapters.Service{DB: sqlDB},
		ProxyCacheRefresher: func(ctx context.Context) error {
			calls++
			if got := countWhere(t, sqlDB, "projects", "id = 'p1'"); got != 0 {
				t.Fatalf("proxy cache refresh ran before committed delete, project count = %d", got)
			}
			return ctx.Err()
		},
	}

	result, err := service.Delete(ctx, "p1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(result.CleanupErrors) != 0 {
		t.Fatalf("cleanup errors = %#v", result.CleanupErrors)
	}
	if calls != 1 {
		t.Fatalf("proxy refresh calls = %d", calls)
	}
}

func TestDeleteReportsProxyCacheRefreshFailureAfterDeletingRows(t *testing.T) {
	ctx := context.Background()
	sqlDB := newProjectDeleteTestDB(t)
	mustExec(t, sqlDB, `UPDATE projects SET proxy_domain = 'dev.example.test' WHERE id = 'p1'`)
	service := &Service{
		DB:       sqlDB,
		Chapters: &chapters.Service{DB: sqlDB},
		ProxyCacheRefresher: func(context.Context) error {
			return errors.New("proxy refresh failed")
		},
	}

	result, err := service.Delete(ctx, "p1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(result.CleanupErrors) != 1 || result.CleanupErrors[0].Scope != "proxy-cache" || result.CleanupErrors[0].Error != "proxy refresh failed" {
		t.Fatalf("cleanup errors = %#v", result.CleanupErrors)
	}
	if got := countWhere(t, sqlDB, "projects", "id = 'p1'"); got != 0 {
		t.Fatalf("project p1 count = %d", got)
	}
}

func TestDeleteReturnsExternalCleanupErrorsAndStillDeletesRows(t *testing.T) {
	ctx := context.Background()
	sqlDB := newProjectDeleteTestDB(t)
	killer := &fakeTerminalKiller{err: errors.New("kill failed")}
	service := &Service{DB: sqlDB, Chapters: &chapters.Service{DB: sqlDB, TerminalRuntime: killer}}

	result, err := service.Delete(ctx, "p1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(result.CleanupErrors) != 2 {
		t.Fatalf("cleanup errors = %#v", result.CleanupErrors)
	}
	if result.CleanupErrors[0].Scope != "terminal" || result.CleanupErrors[0].Error != "kill failed" {
		t.Fatalf("first cleanup error = %#v", result.CleanupErrors[0])
	}
	if got := countWhere(t, sqlDB, "projects", "id = 'p1'"); got != 0 {
		t.Fatalf("project p1 count = %d", got)
	}
	if got := countWhere(t, sqlDB, "terminals", "id IN ('term_c1','term_n_child')"); got != 0 {
		t.Fatalf("project terminals remain: %d", got)
	}
}

func TestDeleteReportsRealGitWorktreeWarningAndStillDeletesRows(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git executable unavailable: %v", err)
	}

	ctx := context.Background()
	sqlDB := newProjectDeleteTestDB(t)
	repoPath := t.TempDir()
	runProjectDeleteGit(t, repoPath, "init")
	worktreePath := filepath.Join(repoPath, ".worktrees", "stale-non-worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir stale worktree: %v", err)
	}
	mustExec(t, sqlDB, `UPDATE projects SET git_path = '`+repoPath+`' WHERE id = 'p1'`)
	mustExec(t, sqlDB, `UPDATE chapters SET worktree_path = '`+worktreePath+`' WHERE id = 'c1'`)
	service := &Service{
		DB:       sqlDB,
		Git:      &gitx.Service{Timeout: 5 * time.Second},
		Chapters: &chapters.Service{DB: sqlDB},
	}

	result, err := service.Delete(ctx, "p1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !result.OK || result.ProjectID != "p1" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.CleanupErrors) != 1 || result.CleanupErrors[0].Scope != "worktree" || result.CleanupErrors[0].ID != "c1" {
		t.Fatalf("cleanup errors = %#v", result.CleanupErrors)
	}
	if got := countWhere(t, sqlDB, "projects", "id = 'p1'"); got != 0 {
		t.Fatalf("project p1 count = %d", got)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("stale worktree fixture should remain after failed real git cleanup: %v", err)
	}
}

func TestDeleteReportsContainerAndWorktreeWarningsAndStillDeletesRows(t *testing.T) {
	ctx := context.Background()
	sqlDB := newProjectDeleteTestDB(t)
	repoPath := t.TempDir()
	worktreePath := filepath.Join(repoPath, ".worktrees", "c1")
	mustExec(t, sqlDB, `UPDATE projects SET git_path = '`+repoPath+`' WHERE id = 'p1'`)
	mustExec(t, sqlDB, `UPDATE chapters SET worktree_path = '`+worktreePath+`' WHERE id = 'c1'`)
	mustExec(t, sqlDB, `UPDATE container_instances SET container_id = 'ctr-c1' WHERE id = 'ci1'`)

	removedContainers := []string{}
	removedWorktrees := []string{}
	prunedRepos := []string{}
	service := &Service{
		DB:       sqlDB,
		Chapters: &chapters.Service{DB: sqlDB},
		ContainerRemover: func(_ context.Context, containerIDs []string) error {
			removedContainers = append(removedContainers, containerIDs...)
			return errors.New("container cleanup failed")
		},
		WorktreeRemover: func(_ context.Context, repoPath, worktreePath string) error {
			removedWorktrees = append(removedWorktrees, repoPath+"|"+worktreePath)
			return errors.New("worktree cleanup failed")
		},
		WorktreePruner: func(_ context.Context, repoPath string) error {
			prunedRepos = append(prunedRepos, repoPath)
			return errors.New("worktree prune failed")
		},
	}

	result, err := service.Delete(ctx, "p1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(removedContainers) != 1 || removedContainers[0] != "ctr-c1" {
		t.Fatalf("removed containers = %#v", removedContainers)
	}
	if len(removedWorktrees) != 1 || removedWorktrees[0] != repoPath+"|"+worktreePath {
		t.Fatalf("removed worktrees = %#v", removedWorktrees)
	}
	if len(prunedRepos) != 1 || prunedRepos[0] != repoPath {
		t.Fatalf("pruned repos = %#v", prunedRepos)
	}
	if len(result.CleanupErrors) != 3 {
		t.Fatalf("cleanup errors = %#v", result.CleanupErrors)
	}
	if got := []string{result.CleanupErrors[0].Scope, result.CleanupErrors[1].Scope, result.CleanupErrors[2].Scope}; got[0] != "containers" || got[1] != "worktree" || got[2] != "worktree-prune" {
		t.Fatalf("cleanup error scopes = %#v, errors = %#v", got, result.CleanupErrors)
	}
	if result.CleanupErrors[0].ID != "ctr-c1" || result.CleanupErrors[1].ID != "c1" || result.CleanupErrors[2].ID != "p1" {
		t.Fatalf("cleanup error IDs = %#v", result.CleanupErrors)
	}
	if got := countWhere(t, sqlDB, "projects", "id = 'p1'"); got != 0 {
		t.Fatalf("project p1 count = %d", got)
	}
	if got := countWhere(t, sqlDB, "container_instances", "id = 'ci1'"); got != 0 {
		t.Fatalf("project container rows remain: %d", got)
	}
}

func runProjectDeleteGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func newProjectDeleteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	mustExec(t, sqlDB, `
CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, description TEXT, status TEXT, flow_mode TEXT, git_path TEXT, remote_url TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT, proxy_domain TEXT, created_at TEXT, updated_at TEXT);
CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, worktree_path TEXT, is_root INTEGER, parent_chapter_id TEXT, merged_into_chapter_id TEXT, created_at TEXT);
CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT);
CREATE TABLE chapter_commits (id TEXT PRIMARY KEY, chapter_id TEXT, narrator_id TEXT);
CREATE TABLE exploration_groups (id TEXT PRIMARY KEY, project_id TEXT);
CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, parent_narrator_id TEXT, fork_message_id TEXT, prune_boundary_message_id TEXT);
CREATE TABLE narrator_goals (id TEXT PRIMARY KEY, narrator_id TEXT);
CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT);
CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT);
CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT);
CREATE TABLE narrator_tool_calls (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT);
CREATE TABLE narrator_file_snapshots (id TEXT PRIMARY KEY, narrator_id TEXT);
CREATE TABLE narrator_patches (id TEXT PRIMARY KEY, narrator_id TEXT);
CREATE TABLE narrator_buffered_messages (id TEXT PRIMARY KEY, narrator_id TEXT);
CREATE TABLE narrator_whitelist_dirs (id TEXT PRIMARY KEY, narrator_id TEXT);
CREATE TABLE narrator_blacklist_dirs (id TEXT PRIMARY KEY, narrator_id TEXT);
CREATE TABLE narrator_whitelist_cmds (id TEXT PRIMARY KEY, narrator_id TEXT);
CREATE TABLE narrator_blacklist_cmds (id TEXT PRIMARY KEY, narrator_id TEXT);
CREATE TABLE background_tasks (id TEXT PRIMARY KEY, parent_narrator_id TEXT, subagent_narrator_id TEXT);
CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT, narrator_id TEXT);
CREATE TABLE terminal_tabs (id TEXT PRIMARY KEY, chapter_id TEXT, narrator_id TEXT);
CREATE TABLE terminal_view_state (id TEXT PRIMARY KEY, user_id TEXT, chapter_id TEXT, narrator_id TEXT);
CREATE TABLE container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, container_id TEXT);
CREATE TABLE port_allocations (id TEXT PRIMARY KEY, port INTEGER, chapter_id TEXT, service_name TEXT);
CREATE TABLE merge_sessions (id TEXT PRIMARY KEY, target_chapter_id TEXT, source_chapter_ids TEXT, current_source_chapter_id TEXT);
CREATE TABLE review_conclusions (id TEXT PRIMARY KEY, review_chapter_id TEXT, source_chapter_id TEXT);
CREATE TABLE volume_snapshots (id TEXT PRIMARY KEY, project_id TEXT, source_chapter_id TEXT);
CREATE TABLE volume_snapshot_applications (id TEXT PRIMARY KEY, snapshot_id TEXT, chapter_id TEXT);
CREATE TABLE workspaces (id TEXT PRIMARY KEY, user_id TEXT, title TEXT, tree TEXT, created_at TEXT, updated_at TEXT);
CREATE TABLE api_requests (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT);
CREATE TABLE hooks (id TEXT PRIMARY KEY, project_id TEXT);
CREATE TABLE gateway_session_mappings (id TEXT PRIMARY KEY, project_id TEXT, chapter_id TEXT, narrator_id TEXT);
CREATE TABLE benchmark_task_results (id TEXT PRIMARY KEY, narrator_id TEXT);
CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, recent_tabs TEXT, updated_at TEXT);
`)
	mustExec(t, sqlDB, `
INSERT INTO projects (id, name, status, flow_mode, created_at, updated_at) VALUES ('p1','Project','active','classic','now','now'), ('p2','Other','active','classic','now','now');
INSERT INTO chapters (id, project_id, title, is_root, created_at) VALUES ('root1','p1','Root',1,'now'), ('c1','p1','Branch',0,'now'), ('c2','p2','Other',0,'now');
INSERT INTO narrators (id, chapter_id, parent_narrator_id) VALUES ('n1','c1',NULL), ('n_child',NULL,'n1'), ('n2','c2',NULL);
INSERT INTO terminals (id, chapter_id, narrator_id) VALUES ('term_c1','c1',NULL), ('term_n_child',NULL,'n_child'), ('term2','c2',NULL);
INSERT INTO terminal_tabs (id, chapter_id, narrator_id) VALUES ('tab1','c1',NULL), ('tab2','c2',NULL);
INSERT INTO terminal_view_state (id, user_id, chapter_id, narrator_id) VALUES ('view1','u1','c1',NULL), ('view2','u1','c2',NULL);
INSERT INTO chapter_edges (id, project_id, source_id, target_id) VALUES ('edge1','p1','root1','c1'), ('edge2','p2','c2','c2');
INSERT INTO chapter_commits (id, chapter_id, narrator_id) VALUES ('commit1','c1','n1'), ('commit2','c2','n2');
INSERT INTO exploration_groups (id, project_id) VALUES ('eg_p1','p1'), ('eg_p2','p2');
INSERT INTO narrator_goals (id, narrator_id) VALUES ('goal1','n1'), ('goal2','n2');
INSERT INTO narrator_messages (id, narrator_id) VALUES ('m1','n1'), ('m2','n2');
INSERT INTO narrator_message_refs (id, narrator_id, message_id) VALUES ('ref1','n1','m1'), ('ref2','n2','m2');
INSERT INTO narrator_sidecars (id, narrator_id, message_id) VALUES ('side1','n1','m1'), ('side2','n2','m2');
INSERT INTO narrator_tool_calls (id, narrator_id, message_id) VALUES ('tool1','n1','m1'), ('tool2','n2','m2');
INSERT INTO narrator_file_snapshots (id, narrator_id) VALUES ('snap1','n1'), ('snap2','n2');
INSERT INTO narrator_patches (id, narrator_id) VALUES ('patch1','n1'), ('patch2','n2');
INSERT INTO narrator_buffered_messages (id, narrator_id) VALUES ('buf1','n_child'), ('buf2','n2');
INSERT INTO narrator_whitelist_dirs (id, narrator_id) VALUES ('wd1','n1'), ('wd2','n2');
INSERT INTO narrator_blacklist_dirs (id, narrator_id) VALUES ('bd1','n1'), ('bd2','n2');
INSERT INTO narrator_whitelist_cmds (id, narrator_id) VALUES ('wc1','n1'), ('wc2','n2');
INSERT INTO narrator_blacklist_cmds (id, narrator_id) VALUES ('bc1','n1'), ('bc2','n2');
INSERT INTO background_tasks (id, parent_narrator_id, subagent_narrator_id) VALUES ('bg1','n1','n_child'), ('bg2','n2',NULL);
INSERT INTO container_instances (id, chapter_id, container_id) VALUES ('ci1','c1',''), ('ci2','c2','');
INSERT INTO port_allocations (id, port, chapter_id, service_name) VALUES ('pa_p1',10001,'c1','web'), ('pa_p2',10002,'c2','web');
INSERT INTO merge_sessions (id, target_chapter_id, source_chapter_ids, current_source_chapter_id) VALUES ('merge1','root1','["c1"]','c1'), ('merge2','c2','["c2"]','c2');
INSERT INTO review_conclusions (id, review_chapter_id, source_chapter_id) VALUES ('rc1','c1','root1'), ('rc2','c2','c2');
INSERT INTO volume_snapshots (id, project_id, source_chapter_id) VALUES ('vs1','p1','c1'), ('vs2','p2','c2');
INSERT INTO volume_snapshot_applications (id, snapshot_id, chapter_id) VALUES ('vsa1','vs1','c1'), ('vsa2','vs2','c2');
INSERT INTO api_requests (id, narrator_id, message_id) VALUES ('api1','n1','m1'), ('api2','n2','m2');
INSERT INTO hooks (id, project_id) VALUES ('hook1','p1'), ('hook2','p2');
INSERT INTO gateway_session_mappings (id, project_id, chapter_id, narrator_id) VALUES ('gw1','p1','c1','n1'), ('gw2','p2','c2','n2');
INSERT INTO benchmark_task_results (id, narrator_id) VALUES ('bench1','n_child'), ('bench2','n2');
INSERT INTO workspaces (id, user_id, title, tree, created_at, updated_at) VALUES ('ws1','u1','Project workspace','{"kind":"leaf","narratorId":"n_child"}','now','now'), ('ws2','u1','Other workspace','{"kind":"leaf","narratorId":"n2"}','now','now');
INSERT INTO user_preferences (id, user_id, recent_tabs, updated_at) VALUES ('pref1','u1','[{"type":"project","id":"p1","title":"Project"},{"type":"chapter","id":"c1","narratorId":"n1","title":"Branch"},{"type":"workspace","id":"ws1","title":"Workspace"},{"type":"project","id":"p2","title":"Other"}]','now');
`)
	return sqlDB
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("exec failed: %v\n%s", err, query)
	}
}

func countWhere(t *testing.T, db *sql.DB, table, where string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE ` + where).Scan(&count); err != nil {
		t.Fatalf("count %s where %s: %v", table, where, err)
	}
	return count
}
