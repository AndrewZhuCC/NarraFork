package chapters

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	gitx "narrafork-go/internal/git"
)

func TestCommitsRouteSyncsGitHistoryIntoDatabase(t *testing.T) {
	repo, _, _, baseHead, sourceHead := setupDependencySyncRepo(t)
	database := openMergeParityDB(t)
	database.SetMaxOpenConns(1)
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, chapter_settings) VALUES ('project', 'Project', ?, 'main', '{}')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, head_commit_sha, start_commit_sha, commit_count, is_root, created_at, updated_at)
		VALUES ('source', 'project', 'Source', 'active', 'branch', 'main', ?, 'main', ?, ?, 1, 1, 'now', 'now')`, repo, baseHead, baseHead); err != nil {
		t.Fatalf("insert source chapter: %v", err)
	}

	handler := NewHandler(&Service{DB: database, Git: gitx.NewService()})
	w := serveChapterRouteJSON(t, handler, http.MethodGet, "/source/commits?limit=10", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 commits, got %d body=%s", w.Code, w.Body.String())
	}
	var commits []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &commits); err != nil {
		t.Fatalf("decode commits: %v", err)
	}
	if len(commits) < 2 {
		t.Fatalf("expected synced commit history, got %#v", commits)
	}
	if commits[0]["sha"] != sourceHead {
		t.Fatalf("latest commit should be first, got %#v", commits[0])
	}
	if commits[0]["source"] != "manual" {
		t.Fatalf("synced commit should preserve DB source metadata, got %#v", commits[0])
	}
	if commits[0]["filesChanged"] == nil || commits[0]["linesAdded"] == nil {
		t.Fatalf("synced commit should include diff stats, got %#v", commits[0])
	}
	var storedHead string
	var commitCount int
	if err := database.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'source'`).Scan(&storedHead, &commitCount); err != nil {
		t.Fatalf("load synced chapter metadata: %v", err)
	}
	if storedHead != sourceHead || commitCount < 2 {
		t.Fatalf("expected synced chapter metadata, got head=%q count=%d", storedHead, commitCount)
	}
	var storedCommits int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'source'`).Scan(&storedCommits); err != nil {
		t.Fatalf("count chapter commits: %v", err)
	}
	if storedCommits < 2 {
		t.Fatalf("expected commits inserted into DB, count=%d", storedCommits)
	}
}

func TestDependencyStatusAndSyncUpstreamUpdateEdgeMetadata(t *testing.T) {
	repo, sourceWT, targetWT, baseHead, sourceHead := setupDependencySyncRepo(t)
	database := openMergeParityDB(t)
	database.SetMaxOpenConns(1)
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, chapter_settings) VALUES ('project', 'Project', ?, 'main', '{}')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, head_commit_sha, start_commit_sha, commit_count, is_root, created_at, updated_at)
		VALUES ('source', 'project', 'Source', 'active', 'branch', 'main', ?, 'main', ?, ?, 2, 1, 'now', 'now'),
		       ('target', 'project', 'Target', 'active', 'branch', 'feature', ?, 'main', ?, ?, 1, 0, 'now', 'now')`, sourceWT, sourceHead, sourceHead, targetWT, baseHead, baseHead); err != nil {
		t.Fatalf("insert chapters: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('edge-1', 'project', 'source', 'target', 'dependency', '{"lastSyncedCommit":"` + baseHead + `"}', 'now')`); err != nil {
		t.Fatalf("insert dependency edge: %v", err)
	}

	handler := NewHandler(&Service{DB: database, Git: gitx.NewService()})
	statusBody := serveChapterRouteJSON(t, handler, http.MethodGet, "/target/dependency-status", "")
	if statusBody.Code != http.StatusOK {
		t.Fatalf("expected 200 dependency status, got %d body=%s", statusBody.Code, statusBody.Body.String())
	}
	var statuses []map[string]any
	if err := json.Unmarshal(statusBody.Body.Bytes(), &statuses); err != nil {
		t.Fatalf("decode dependency status: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("unexpected dependency status response: %#v", statuses)
	}
	item := statuses[0]
	if item["hasUpdates"] != true || item["newCommitCount"] != float64(1) || item["lastSyncedCommit"] != baseHead || item["sourceHeadCommitSha"] != sourceHead {
		t.Fatalf("dependency status should surface upstream updates, got %#v", item)
	}

	syncBody := serveChapterRouteJSON(t, handler, http.MethodPost, "/target/sync-upstream", `{"edgeId":"edge-1","strategy":"rebase"}`)
	if syncBody.Code != http.StatusOK {
		t.Fatalf("expected 200 sync-upstream, got %d body=%s", syncBody.Code, syncBody.Body.String())
	}
	var syncPayload map[string]any
	if err := json.Unmarshal(syncBody.Body.Bytes(), &syncPayload); err != nil {
		t.Fatalf("decode sync-upstream: %v", err)
	}
	if syncPayload["success"] != true {
		t.Fatalf("expected successful sync-upstream, got %#v", syncPayload)
	}
	dependencySync, ok := syncPayload["dependencySync"].(map[string]any)
	if !ok || dependencySync["lastSyncedCommit"] != sourceHead || dependencySync["sourceHeadCommitSha"] != sourceHead {
		t.Fatalf("sync response should include dependency sync metadata, got %#v", syncPayload)
	}
	var rawMetadata string
	if err := database.QueryRow(`SELECT metadata FROM chapter_edges WHERE id = 'edge-1'`).Scan(&rawMetadata); err != nil {
		t.Fatalf("load updated edge metadata: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
		t.Fatalf("decode updated edge metadata: %v", err)
	}
	if metadata["lastSyncedCommit"] != sourceHead || metadata["lastSyncStrategy"] != "rebase" {
		t.Fatalf("edge metadata should be updated after sync, got %#v", metadata)
	}
	targetHead := strings.TrimSpace(runGitChapterTest(t, targetWT, "rev-parse", "HEAD"))
	if targetHead != sourceHead {
		t.Fatalf("target worktree should fast-forward/rebase to source head, got %s want %s", targetHead, sourceHead)
	}
	var storedTargetHead string
	var storedTargetCommitCount int
	if err := database.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'target'`).Scan(&storedTargetHead, &storedTargetCommitCount); err != nil {
		t.Fatalf("load synced target metadata: %v", err)
	}
	if storedTargetHead != sourceHead || storedTargetCommitCount < 2 {
		t.Fatalf("target DB metadata should match synced git head, head=%q count=%d", storedTargetHead, storedTargetCommitCount)
	}
	var storedTargetHeadCommit int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'target' AND sha = ?`, sourceHead).Scan(&storedTargetHeadCommit); err != nil {
		t.Fatalf("count synced target head commit: %v", err)
	}
	if storedTargetHeadCommit != 1 {
		t.Fatalf("target commit history should include synced source head, count=%d", storedTargetHeadCommit)
	}

	statusBody = serveChapterRouteJSON(t, handler, http.MethodGet, "/target/dependency-status", "")
	if statusBody.Code != http.StatusOK {
		t.Fatalf("expected 200 dependency status after sync, got %d body=%s", statusBody.Code, statusBody.Body.String())
	}
	if err := json.Unmarshal(statusBody.Body.Bytes(), &statuses); err != nil {
		t.Fatalf("decode dependency status after sync: %v", err)
	}
	item = statuses[0]
	if item["hasUpdates"] != false || item["newCommitCount"] != float64(0) {
		t.Fatalf("dependency status should clear after sync, got %#v", item)
	}
}

func TestSyncUpstreamConflictDoesNotAdvanceDependencyMetadataOrDBState(t *testing.T) {
	repo, sourceWT, targetWT, baseHead, sourceHead, targetHead := setupDependencyConflictRepo(t)
	database := openMergeParityDB(t)
	database.SetMaxOpenConns(1)
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, chapter_settings) VALUES ('project', 'Project', ?, 'main', '{}')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, head_commit_sha, start_commit_sha, commit_count, is_root, created_at, updated_at)
		VALUES ('source', 'project', 'Source', 'active', 'branch', 'main', ?, 'main', ?, ?, 2, 1, 'now', 'now'),
		       ('target', 'project', 'Target', 'active', 'branch', 'feature', ?, 'main', ?, ?, 2, 0, 'now', 'now')`, sourceWT, sourceHead, baseHead, targetWT, targetHead, baseHead); err != nil {
		t.Fatalf("insert chapters: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('edge-conflict', 'project', 'source', 'target', 'dependency', '{"lastSyncedCommit":"` + baseHead + `"}', 'now')`); err != nil {
		t.Fatalf("insert dependency edge: %v", err)
	}

	handler := NewHandler(&Service{DB: database, Git: gitx.NewService()})
	syncBody := serveChapterRouteJSON(t, handler, http.MethodPost, "/target/sync-upstream", `{"edgeId":"edge-conflict","strategy":"merge"}`)
	if syncBody.Code != http.StatusOK {
		t.Fatalf("expected 200 sync-upstream conflict payload, got %d body=%s", syncBody.Code, syncBody.Body.String())
	}
	var syncPayload map[string]any
	if err := json.Unmarshal(syncBody.Body.Bytes(), &syncPayload); err != nil {
		t.Fatalf("decode conflict sync-upstream: %v", err)
	}
	if syncPayload["success"] != false || syncPayload["dependencySync"] != nil {
		t.Fatalf("conflict response should not claim dependency sync success, got %#v", syncPayload)
	}
	result, ok := syncPayload["result"].(map[string]any)
	conflicts, conflictsOK := result["conflictFiles"].([]any)
	if !ok || !conflictsOK || len(conflicts) == 0 {
		t.Fatalf("conflict response should expose conflict files, got %#v", syncPayload)
	}
	conflictFile, _ := conflicts[0].(string)
	if conflictFile != "story.txt" {
		t.Fatalf("expected story.txt conflict, got %#v", result["conflictFiles"])
	}

	var rawMetadata string
	if err := database.QueryRow(`SELECT metadata FROM chapter_edges WHERE id = 'edge-conflict'`).Scan(&rawMetadata); err != nil {
		t.Fatalf("load conflict edge metadata: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
		t.Fatalf("decode conflict edge metadata: %v", err)
	}
	if metadata["lastSyncedCommit"] != baseHead || metadata["lastSyncStrategy"] != nil {
		t.Fatalf("conflict should not advance edge metadata, got %#v", metadata)
	}
	var storedTargetHead string
	if err := database.QueryRow(`SELECT head_commit_sha FROM chapters WHERE id = 'target'`).Scan(&storedTargetHead); err != nil {
		t.Fatalf("load conflict target metadata: %v", err)
	}
	if storedTargetHead != targetHead {
		t.Fatalf("conflict should not refresh target DB head, got %s want %s", storedTargetHead, targetHead)
	}
	gitHead := strings.TrimSpace(runGitChapterTest(t, targetWT, "rev-parse", "HEAD"))
	if gitHead != targetHead {
		t.Fatalf("conflict should leave target HEAD at pre-merge commit, got %s want %s", gitHead, targetHead)
	}
	status := runGitChapterTest(t, targetWT, "status", "--porcelain")
	if !strings.Contains(status, "UU story.txt") {
		t.Fatalf("target worktree should remain in explicit conflict state, status=%q", status)
	}

	statusBody := serveChapterRouteJSON(t, handler, http.MethodGet, "/target/dependency-status", "")
	if statusBody.Code != http.StatusOK {
		t.Fatalf("expected 200 dependency status after conflict, got %d body=%s", statusBody.Code, statusBody.Body.String())
	}
	var statuses []map[string]any
	if err := json.Unmarshal(statusBody.Body.Bytes(), &statuses); err != nil {
		t.Fatalf("decode dependency status after conflict: %v", err)
	}
	if len(statuses) != 1 || statuses[0]["hasUpdates"] != true || statuses[0]["lastSyncedCommit"] != baseHead {
		t.Fatalf("conflict should keep dependency status unsynced, got %#v", statuses)
	}
}

func setupDependencySyncRepo(t *testing.T) (repo, sourceWT, targetWT, baseHead, sourceHead string) {
	t.Helper()
	repo = t.TempDir()
	runGitChapterTest(t, repo, "init", "-b", "main")
	runGitChapterTest(t, repo, "config", "user.email", "test@example.com")
	runGitChapterTest(t, repo, "config", "user.name", "Test User")
	writeChapterTestFile(t, repo, "story.txt", "base\n")
	runGitChapterTest(t, repo, "add", "story.txt")
	runGitChapterTest(t, repo, "commit", "-m", "base")
	baseHead = strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	runGitChapterTest(t, repo, "branch", "feature")
	targetWT = filepath.Join(t.TempDir(), "feature-worktree")
	runGitChapterTest(t, repo, "worktree", "add", targetWT, "feature")
	writeChapterTestFile(t, repo, "story.txt", "main update\n")
	runGitChapterTest(t, repo, "add", "story.txt")
	runGitChapterTest(t, repo, "commit", "-m", "main update")
	sourceHead = strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	sourceWT = repo
	return repo, sourceWT, targetWT, baseHead, sourceHead
}

func setupDependencyConflictRepo(t *testing.T) (repo, sourceWT, targetWT, baseHead, sourceHead, targetHead string) {
	t.Helper()
	repo = t.TempDir()
	runGitChapterTest(t, repo, "init", "-b", "main")
	runGitChapterTest(t, repo, "config", "user.email", "test@example.com")
	runGitChapterTest(t, repo, "config", "user.name", "Test User")
	writeChapterTestFile(t, repo, "story.txt", "base\n")
	runGitChapterTest(t, repo, "add", "story.txt")
	runGitChapterTest(t, repo, "commit", "-m", "base")
	baseHead = strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	runGitChapterTest(t, repo, "branch", "feature")
	targetWT = filepath.Join(t.TempDir(), "feature-worktree")
	runGitChapterTest(t, repo, "worktree", "add", targetWT, "feature")
	writeChapterTestFile(t, repo, "story.txt", "main update\n")
	runGitChapterTest(t, repo, "add", "story.txt")
	runGitChapterTest(t, repo, "commit", "-m", "main update")
	sourceHead = strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	writeChapterTestFile(t, targetWT, "story.txt", "feature update\n")
	runGitChapterTest(t, targetWT, "add", "story.txt")
	runGitChapterTest(t, targetWT, "commit", "-m", "feature update")
	targetHead = strings.TrimSpace(runGitChapterTest(t, targetWT, "rev-parse", "HEAD"))
	sourceWT = repo
	return repo, sourceWT, targetWT, baseHead, sourceHead, targetHead
}

func serveChapterRouteJSON(t *testing.T, handler *Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := newChapterRouter(handler)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
