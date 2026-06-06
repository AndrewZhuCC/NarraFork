package chapters

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/config"
	gitx "narrafork-go/internal/git"
)

func TestAIResolveCherryPickSuccessPayloadAndMergeSideEffects(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	preMergeHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)

	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}
	result, err := svc.AIResolve(ctx, "source", "target", "cherry-pick", "AI cherry-pick", "user-1", "en")
	if err != nil {
		t.Fatalf("AIResolve cherry-pick: %v", err)
	}
	if resolved, _ := result["resolved"].(bool); !resolved {
		t.Fatalf("AIResolve cherry-pick resolved = false: %#v", result)
	}
	mergeResult, ok := result["mergeResult"].(map[string]any)
	if !ok {
		t.Fatalf("mergeResult missing or wrong type: %#v", result)
	}
	commitSHA, _ := mergeResult["commitSha"].(string)
	mergeCommitSHA, _ := mergeResult["mergeCommitSha"].(string)
	if ok, _ := mergeResult["ok"].(bool); !ok || commitSHA == "" || mergeCommitSHA != commitSHA || commitSHA == preMergeHead {
		t.Fatalf("unexpected mergeResult = %#v, preMergeHead=%s", mergeResult, preMergeHead)
	}
	if head := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD")); head != commitSHA {
		t.Fatalf("target HEAD = %s, want cherry-pick head %s", head, commitSHA)
	}
	assertAIResolveCherryPickSideEffects(t, database, repo, commitSHA, preMergeHead)
	assertMergeSummaryLatest(t, database, "source", commitSHA, true)
}

func TestAIResolveCherryPickConflictReturnsFallbackWithoutNarratorSideEffectsAndCleanGitState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead, preMergeHead := setupMergeConflictRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	settings, calls := cherryPickAISettingsThatMustNotBeCalled(t)
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}, Settings: settings}

	result, err := svc.AIResolve(ctx, "source", "target", "cherry-pick", "AI cherry-pick", "user-1", "en")
	if err != nil {
		t.Fatalf("AIResolve cherry-pick conflict should return fallback payload, got error: %v", err)
	}
	if resolved, _ := result["resolved"].(bool); resolved {
		t.Fatalf("AIResolve cherry-pick conflict resolved = true: %#v", result)
	}
	if errText, _ := result["error"].(string); !strings.Contains(errText, "Go basic AI resolver cannot continue") {
		t.Fatalf("fallback error = %q, want Go basic AI resolver cannot continue", errText)
	}
	mergeResult, ok := result["mergeResult"].(map[string]any)
	if !ok {
		t.Fatalf("mergeResult missing or wrong type: %#v", result)
	}
	if ok, _ := mergeResult["ok"].(bool); ok {
		t.Fatalf("conflict mergeResult ok = true: %#v", mergeResult)
	}
	if success, _ := mergeResult["success"].(bool); success {
		t.Fatalf("conflict mergeResult success = true: %#v", mergeResult)
	}
	conflicts, _ := mergeResult["conflictFiles"].([]string)
	if len(conflicts) != 1 || conflicts[0] != "shared.txt" {
		t.Fatalf("conflictFiles = %#v, want [shared.txt] in %#v", conflicts, mergeResult)
	}
	if *calls != 0 {
		t.Fatalf("provider calls = %d, want 0 for cherry-pick fallback", *calls)
	}
	assertNoNarratorAISideEffects(t, database)
	assertCherryPickConflictLeftCleanGitState(t, repo, preMergeHead)
	assertMergeAbortedWithoutPartialState(t, database, repo, preMergeHead)
}

func assertAIResolveCherryPickSideEffects(t *testing.T, database *sql.DB, repo, commitSHA, preMergeHead string) {
	t.Helper()
	var sourceStatus string
	var sourceWorktree, mergedInto, mergeCommit, mergeStrategy, preMerge sql.NullString
	if err := database.QueryRow(`SELECT status, worktree_path, merged_into_chapter_id, merge_commit_sha, merge_strategy, pre_merge_target_sha FROM chapters WHERE id = 'source'`).Scan(&sourceStatus, &sourceWorktree, &mergedInto, &mergeCommit, &mergeStrategy, &preMerge); err != nil {
		t.Fatalf("query source cherry-pick fields: %v", err)
	}
	if sourceStatus != "merged" || sourceWorktree.Valid || !mergedInto.Valid || mergedInto.String != "target" || !mergeCommit.Valid || mergeCommit.String != commitSHA || !mergeStrategy.Valid || mergeStrategy.String != "cherry-pick" || !preMerge.Valid || preMerge.String != preMergeHead {
		t.Fatalf("source cherry-pick fields = status:%s worktree:%v into:%v sha:%v strategy:%v pre:%v", sourceStatus, sourceWorktree, mergedInto, mergeCommit, mergeStrategy, preMerge)
	}

	var targetHead sql.NullString
	var targetCount int
	if err := database.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'target'`).Scan(&targetHead, &targetCount); err != nil {
		t.Fatalf("query target cherry-pick metadata: %v", err)
	}
	expectedCount := gitCommitCountForTest(t, repo)
	if !targetHead.Valid || targetHead.String != commitSHA || targetCount != expectedCount {
		t.Fatalf("target metadata = head:%v count:%d, want %s/%d", targetHead, targetCount, commitSHA, expectedCount)
	}

	var edgeMetadata string
	if err := database.QueryRow(`SELECT metadata FROM chapter_edges WHERE source_id = 'source' AND target_id = 'target' AND type = 'merge'`).Scan(&edgeMetadata); err != nil {
		t.Fatalf("query cherry-pick edge: %v", err)
	}
	if !strings.Contains(edgeMetadata, commitSHA) || !strings.Contains(edgeMetadata, "cherry-pick") || !strings.Contains(edgeMetadata, "completed") {
		t.Fatalf("edge metadata = %s, want commit/strategy/status", edgeMetadata)
	}

	var commitMessage, commitSource string
	if err := database.QueryRow(`SELECT message, source FROM chapter_commits WHERE chapter_id = 'target' AND sha = ?`, commitSHA).Scan(&commitMessage, &commitSource); err != nil {
		t.Fatalf("query cherry-pick chapter commit: %v", err)
	}
	if strings.TrimSpace(commitMessage) == "" {
		t.Fatalf("commit message should be recorded")
	}
	if commitSource != "cherry_pick" {
		t.Fatalf("commit source = %q, want cherry_pick", commitSource)
	}
}

func assertNoNarratorAISideEffects(t *testing.T, database *sql.DB) {
	t.Helper()
	for label, query := range map[string]string{
		"narrator messages": `SELECT COUNT(*) FROM narrator_messages`,
		"narrator refs":     `SELECT COUNT(*) FROM narrator_message_refs`,
	} {
		var count int
		if err := database.QueryRow(query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", label, err)
		}
		if count != 0 {
			t.Fatalf("%s = %d, want 0", label, count)
		}
	}
	var messageCount, messageVersion int
	if err := database.QueryRow(`SELECT message_count, message_version FROM narrators WHERE id = 'narrator-target'`).Scan(&messageCount, &messageVersion); err != nil {
		t.Fatalf("query narrator counters: %v", err)
	}
	if messageCount != 0 || messageVersion != 0 {
		t.Fatalf("narrator counters = count:%d version:%d, want 0/0", messageCount, messageVersion)
	}
}

func assertCherryPickConflictLeftCleanGitState(t *testing.T, repo, preMergeHead string) {
	t.Helper()
	if head := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD")); head != preMergeHead {
		t.Fatalf("target HEAD after failed cherry-pick = %s, want %s", head, preMergeHead)
	}
	if status := strings.TrimSpace(runGitChapterTest(t, repo, "status", "--porcelain")); status != "" {
		t.Fatalf("target worktree should be clean after failed cherry-pick, status=%q", status)
	}
	for _, name := range []string{"CHERRY_PICK_HEAD", "MERGE_MSG", "sequencer"} {
		path := filepath.Join(repo, ".git", name)
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("failed cherry-pick left git state at %s", path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
	content, err := os.ReadFile(filepath.Join(repo, "shared.txt"))
	if err != nil {
		t.Fatalf("read shared.txt: %v", err)
	}
	if strings.Contains(string(content), "<<<<<<<") || strings.TrimSpace(string(content)) != "target" {
		t.Fatalf("shared.txt after failed cherry-pick = %q, want clean target content", string(content))
	}
}

func cherryPickAISettingsThatMustNotBeCalled(t *testing.T) (*config.Store, *int) {
	t.Helper()
	provider, calls := newScriptedOpenAIProvider(t)
	settings := newMergeParityAISettings(t, provider.URL)
	return settings, calls
}
