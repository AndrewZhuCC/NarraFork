package chapters

import (
	"context"
	"database/sql"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	gitx "narrafork-go/internal/git"
)

func TestAIResolveCleanMergeRecordMergedFailureRollsBackGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	preMergeHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	createAIResolveRecordMergedFailureTrigger(t, database)
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}

	result, err := svc.AIResolve(ctx, "source", "target", "merge", "Record merged blocked", "user-1", "en")
	if err != nil {
		t.Fatalf("AIResolve should return rollback payload, got error: %v", err)
	}
	assertAIResolveRecordMergedRollbackPayload(t, result)
	assertMergeAbortedWithoutPartialState(t, database, repo, preMergeHead)
}

func TestAIResolvePostCommitRecordMergedFailureKeepsGitAndCoreDBState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	preMergeHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	if _, err := database.Exec(`CREATE TRIGGER fail_ai_resolve_post_commit_record BEFORE INSERT ON chapter_commits BEGIN SELECT RAISE(ABORT, 'chapter commit insert blocked'); END;`); err != nil {
		t.Fatalf("create post-commit trigger: %v", err)
	}
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}

	result, err := svc.AIResolve(ctx, "source", "target", "merge", "Post commit blocked", "user-1", "en")
	if err != nil {
		t.Fatalf("AIResolve should return success with warning, got error: %v", err)
	}
	if resolved, _ := result["resolved"].(bool); !resolved {
		t.Fatalf("expected resolved success after core DB commit, got %#v", result)
	}
	mergeResult, _ := result["mergeResult"].(map[string]any)
	commitSHA, _ := mergeResult["commitSha"].(string)
	if success, _ := mergeResult["success"].(bool); !success || commitSHA == "" || commitSHA == preMergeHead {
		t.Fatalf("expected successful mergeResult with new commit, got %#v", result)
	}
	if warning, _ := result["warning"].(string); !strings.Contains(warning, "post-merge follow-up failed") {
		t.Fatalf("warning = %q, want post-merge warning in %#v", warning, result)
	}
	assertMergeCoreSideEffectsWithoutCommitRow(t, database, repo, commitSHA, preMergeHead)
}

func TestAIResolveConflictResolutionRecordMergedFailureRollsBackGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead, preMergeHead := setupMergeConflictRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	createAIResolveRecordMergedFailureTrigger(t, database)
	provider, calls := newScriptedOpenAIProvider(t,
		func(w http.ResponseWriter, _ *http.Request) {
			writeOpenAIToolCallSSE(t, w, "call_write", "Write", `{"file_path":"shared.txt","content":"resolved by ai\n"}`)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeOpenAIToolCallSSE(t, w, "call_add", "Bash", `{"command":"git add shared.txt","description":"Mark resolved conflict"}`)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeOpenAITextSSE(t, w, "Resolved conflicts before recordMerged failed.")
		},
	)
	settings := newMergeParityAISettings(t, provider.URL)
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}, Settings: settings}

	result, err := svc.AIResolve(ctx, "source", "target", "merge", "AI resolved merge", "user-1", "en")
	if err != nil {
		t.Fatalf("AIResolve should return rollback payload, got error: %v", err)
	}
	if *calls != 3 {
		t.Fatalf("provider calls = %d, want 3 one-shot-basic loop calls", *calls)
	}
	assertAIResolveRecordMergedRollbackPayload(t, result)
	assertMergeAbortedWithoutPartialState(t, database, repo, preMergeHead)
}

func createAIResolveRecordMergedFailureTrigger(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`CREATE TRIGGER fail_ai_resolve_record_merged_update BEFORE UPDATE OF status ON chapters WHEN NEW.status = 'merged' BEGIN SELECT RAISE(ABORT, 'record merged blocked'); END;`); err != nil {
		t.Fatalf("create recordMerged trigger: %v", err)
	}
}

func assertAIResolveRecordMergedRollbackPayload(t *testing.T, result map[string]any) {
	t.Helper()
	if resolved, _ := result["resolved"].(bool); resolved {
		t.Fatalf("rollback result resolved = true: %#v", result)
	}
	if success, _ := result["success"].(bool); success {
		t.Fatalf("rollback result success = true: %#v", result)
	}
	mergeResult, _ := result["mergeResult"].(map[string]any)
	if success, _ := mergeResult["success"].(bool); success {
		t.Fatalf("rollback mergeResult success = true: %#v", result)
	}
	warning, _ := result["warning"].(string)
	errorText, _ := result["error"].(string)
	rollbackText := strings.ToLower(warning + " " + errorText)
	if !strings.Contains(rollbackText, "rollback") && !strings.Contains(rollbackText, "rolled back") {
		t.Fatalf("rollback payload missing warning/error: %#v", result)
	}
}
