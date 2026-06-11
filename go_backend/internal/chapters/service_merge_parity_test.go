package chapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/config"
	gitx "narrafork-go/internal/git"
	graphx "narrafork-go/internal/graph"

	_ "modernc.org/sqlite"
)

func TestFallbackRootTargetIDReturnsQueryErrors(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT)`); err != nil {
		t.Fatalf("create chapters: %v", err)
	}
	service := &Service{DB: database}

	_, err = service.fallbackRootTargetID(context.Background(), "project-1")
	if err == nil || !strings.Contains(err.Error(), "is_root") {
		t.Fatalf("expected root target query error, got %v", err)
	}
}

func TestMergeAutoCommitsDirtySourceWithoutRequiredReview(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupMergeParityRepo(t)
	preMergeHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)

	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}
	result, err := svc.Merge(ctx, "source", "target", "merge", "", "user-1")
	if err != nil {
		t.Fatalf("Merge should auto-commit dirty source when review is not required: %v", err)
	}
	commitSHA, _ := result["mergeCommitSha"].(string)
	if ok, _ := result["ok"].(bool); !ok || commitSHA == "" || commitSHA == preMergeHead {
		t.Fatalf("unexpected merge result: %#v", result)
	}
	var autoCommitSHA, autoCommitMessage string
	if err := database.QueryRow(`SELECT sha, message FROM chapter_commits WHERE chapter_id = 'source' AND source = 'auto'`).Scan(&autoCommitSHA, &autoCommitMessage); err != nil {
		t.Fatalf("query source auto commit row: %v", err)
	}
	if autoCommitSHA == sourceHead || strings.TrimSpace(autoCommitMessage) == "" || !strings.Contains(autoCommitMessage, "Auto-commit Source changes before merge into main") {
		t.Fatalf("auto commit row = sha:%s message:%q, sourceHead=%s", autoCommitSHA, autoCommitMessage, sourceHead)
	}
	assertMergeSideEffects(t, database, repo, "merge", commitSHA, preMergeHead)
}

func TestRulerMergeLifecycleDBGitGraphProof(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupMergeParityRepo(t)
	preMergeHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	if err := os.Remove(filepath.Join(sourceWT, "dirty.txt")); err != nil {
		t.Fatalf("clean source worktree fixture: %v", err)
	}
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)

	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}
	result, err := svc.Merge(ctx, "source", "target", "merge", "Ruler merge lifecycle proof", "user-1")
	if err != nil {
		t.Fatalf("Ruler delegated merge should succeed: %v", err)
	}
	commitSHA, _ := result["mergeCommitSha"].(string)
	if ok, _ := result["ok"].(bool); !ok || commitSHA == "" || commitSHA == preMergeHead {
		t.Fatalf("unexpected merge result: %#v", result)
	}
	assertMergeSideEffects(t, database, repo, "merge", commitSHA, preMergeHead)
	assertMergeGraphSurface(t, database, commitSHA, preMergeHead)
}

func TestMergeAutoCommitWaitsForSourceWorktreeLock(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupMergeParityRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	gitService := &gitx.Service{Timeout: 10 * time.Second}
	svc := &Service{DB: database, Git: gitService}
	type mergeOutcome struct {
		result map[string]any
		err    error
	}
	done := make(chan mergeOutcome, 1)

	if err := gitService.WithWorktreeLock(sourceWT, func() error {
		go func() {
			result, err := svc.Merge(ctx, "source", "target", "merge", "", "user-1")
			done <- mergeOutcome{result: result, err: err}
		}()
		select {
		case outcome := <-done:
			t.Fatalf("merge returned before source worktree lock release: result=%#v err=%v", outcome.result, outcome.err)
		case <-time.After(50 * time.Millisecond):
		}
		return nil
	}); err != nil {
		t.Fatalf("hold source worktree lock: %v", err)
	}

	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("merge after source lock release: %v", outcome.err)
		}
		if ok, _ := outcome.result["ok"].(bool); !ok {
			t.Fatalf("merge result after source lock release = %#v", outcome.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("merge did not finish after source worktree lock release")
	}
}

func TestMergeRequiredReviewRejectsDirtySourceWithoutAutoCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupMergeParityRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, true)

	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}
	_, err := svc.Merge(ctx, "source", "target", "merge", "", "user-1")
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("merge error = %v, want uncommitted changes rejection", err)
	}
	if _, err := os.Stat(sourceWT); err != nil {
		t.Fatalf("required-review rejected source worktree should remain: %v", err)
	}
	var autoCommits, mergeCommits int
	_ = database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'source' AND source = 'auto'`).Scan(&autoCommits)
	_ = database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'target' AND source = 'merge'`).Scan(&mergeCommits)
	if autoCommits != 0 || mergeCommits != 0 {
		t.Fatalf("required-review dirty rejection should not record commits, auto=%d merge=%d", autoCommits, mergeCommits)
	}
}

func TestMergeRejectsDirtyTarget(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, _ := setupMergeParityRepo(t)
	runGitChapterTest(t, sourceWT, "add", ".")
	runGitChapterTest(t, sourceWT, "commit", "-m", "source clean")
	sourceHead := strings.TrimSpace(runGitChapterTest(t, sourceWT, "rev-parse", "HEAD"))
	writeChapterTestFile(t, repo, "target-dirty.txt", "dirty target\n")
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)

	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}
	_, err := svc.Merge(ctx, "source", "target", "merge", "", "user-1")
	if err == nil || !strings.Contains(err.Error(), "MERGE_DIRTY_TARGET") {
		t.Fatalf("merge error = %v, want MERGE_DIRTY_TARGET", err)
	}
}

func TestMergeRejectsStaleApprovedReview(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, reviewedHead := setupMergeParityRepo(t)
	runGitChapterTest(t, sourceWT, "add", ".")
	runGitChapterTest(t, sourceWT, "commit", "-m", "after review")
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, reviewedHead, true)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, fork_point, head_commit_sha, start_commit_sha, commit_count, is_root, created_at, updated_at)
		VALUES ('review', 'project', 'Review', 'active', 'review', 'review/source', NULL, 'chapter/source', ?, ?, ?, 1, 0, 'now', 'now')`, `{"commitSha":"`+reviewedHead+`"}`, reviewedHead, reviewedHead); err != nil {
		t.Fatalf("insert review chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO review_conclusions (id, review_chapter_id, source_chapter_id, verdict, findings_json, created_at) VALUES ('rc1', 'review', 'source', 'approve', '[]', 'now')`); err != nil {
		t.Fatalf("insert review conclusion: %v", err)
	}

	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}
	_, err := svc.Merge(ctx, "source", "target", "merge", "", "")
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("merge error = %v, want stale review rejection", err)
	}
}

func TestRequiredReviewRejectsMissingAndNonApprovedConclusions(t *testing.T) {
	cases := []struct {
		name     string
		verdict  string
		wantText string
	}{
		{name: "missing review", wantText: "No review found"},
		{name: "request changes", verdict: "request_changes", wantText: "Latest review verdict: request_changes"},
		{name: "comment only", verdict: "comment_only", wantText: "Latest review verdict: comment_only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database := openMergeParityDB(t)
			if tc.verdict != "" {
				if _, err := database.Exec(`INSERT INTO review_conclusions (id, review_chapter_id, source_chapter_id, verdict, findings_json, created_at) VALUES ('rc1', 'review', 'source', ?, '[]', 'now')`, tc.verdict); err != nil {
					t.Fatalf("insert review conclusion: %v", err)
				}
			}
			svc := &Service{DB: database}
			err := svc.ensureSourceReadyForRequiredReview(context.Background(), Chapter{ID: "source", Branch: "chapter/source"}, "/repo")
			if err == nil || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("required review error = %v, want %q", err, tc.wantText)
			}
		})
	}
}

func TestAIResolveOneShotBasicSuccessPayloadAndSideEffects(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead, preMergeHead := setupMergeConflictRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)

	provider, calls := newScriptedOpenAIProvider(t,
		func(w http.ResponseWriter, _ *http.Request) {
			writeOpenAIToolCallSSE(t, w, "call_write", "Write", `{"file_path":"shared.txt","content":"resolved by ai\n"}`)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeOpenAIToolCallSSE(t, w, "call_add", "Bash", `{"command":"git add shared.txt","description":"Mark resolved conflict"}`)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeOpenAITextSSE(t, w, "Resolved conflicts with the one-shot basic resolver.")
		},
	)
	settings := newMergeParityAISettings(t, provider.URL)
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}, Settings: settings}

	result, err := svc.AIResolve(ctx, "source", "target", "merge", "AI resolved merge", "user-1", "en")
	if err != nil {
		t.Fatalf("AIResolve: %v", err)
	}
	if resolved, _ := result["resolved"].(bool); !resolved {
		t.Fatalf("AIResolve resolved = false: %#v", result)
	}
	if got := result["narratorId"]; got != "narrator-target" {
		t.Fatalf("narratorId = %#v, want narrator-target", got)
	}
	if msg, _ := result["assistantMessage"].(string); !strings.Contains(msg, "one-shot basic") {
		t.Fatalf("assistantMessage = %q, want provider text", msg)
	}
	if persisted, _ := result["transcriptPersisted"].(bool); !persisted || result["transcriptMode"] != "one-shot-basic-fallback" {
		t.Fatalf("AIResolve transcript fallback metadata missing: %#v", result)
	}
	assertAIResolveCompatibilityContract(t, result, "one-shot-basic-fallback", "")
	if *calls != 3 {
		t.Fatalf("provider calls = %d, want 3 one-shot-basic loop calls", *calls)
	}
	mergeResult, _ := result["mergeResult"].(map[string]any)
	commitSHA, _ := mergeResult["commitSha"].(string)
	if ok, _ := mergeResult["success"].(bool); !ok || commitSHA == "" || commitSHA == preMergeHead {
		t.Fatalf("mergeResult = %#v, preMergeHead=%s", mergeResult, preMergeHead)
	}
	if head := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD")); head != commitSHA {
		t.Fatalf("target HEAD = %s, want merge commit %s", head, commitSHA)
	}
	assertMergeSideEffects(t, database, repo, "merge", commitSHA, preMergeHead)
	assertMergeSummaryLatest(t, database, "source", commitSHA, true)
	assertAIResolveTranscriptFallback(t, database, commitSHA)
}

func TestAIResolveOneShotBasicTranscriptFailureReturnsWarningWithoutRollback(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead, preMergeHead := setupMergeConflictRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	if _, err := database.Exec(`CREATE TRIGGER fail_ai_resolve_transcript BEFORE INSERT ON narrator_messages WHEN NEW.content_json LIKE '%ai_resolve_request%' BEGIN SELECT RAISE(ABORT, 'ai resolve transcript blocked'); END;`); err != nil {
		t.Fatalf("create transcript trigger: %v", err)
	}
	provider, calls := newScriptedOpenAIProvider(t,
		func(w http.ResponseWriter, _ *http.Request) {
			writeOpenAIToolCallSSE(t, w, "call_write", "Write", `{"file_path":"shared.txt","content":"resolved by ai\n"}`)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeOpenAIToolCallSSE(t, w, "call_add", "Bash", `{"command":"git add shared.txt","description":"Mark resolved conflict"}`)
		},
		func(w http.ResponseWriter, _ *http.Request) {
			writeOpenAITextSSE(t, w, "Resolved conflicts but transcript storage failed.")
		},
	)
	settings := newMergeParityAISettings(t, provider.URL)
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}, Settings: settings}

	result, err := svc.AIResolve(ctx, "source", "target", "merge", "AI resolved merge", "user-1", "en")
	if err != nil {
		t.Fatalf("AIResolve transcript warning path should not error: %v", err)
	}
	if resolved, _ := result["resolved"].(bool); !resolved {
		t.Fatalf("AIResolve should stay resolved despite transcript failure: %#v", result)
	}
	if persisted, _ := result["transcriptPersisted"].(bool); persisted {
		t.Fatalf("transcriptPersisted should be false when trigger blocks transcript: %#v", result)
	}
	if warning, _ := result["transcriptWarning"].(string); !strings.Contains(warning, "ai resolve transcript blocked") {
		t.Fatalf("missing transcript warning: %#v", result)
	}
	if *calls != 3 {
		t.Fatalf("provider calls = %d, want 3 one-shot-basic loop calls", *calls)
	}
	mergeResult, _ := result["mergeResult"].(map[string]any)
	commitSHA, _ := mergeResult["commitSha"].(string)
	if head := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD")); head != commitSHA || head == preMergeHead {
		t.Fatalf("target HEAD = %s, want new merge commit %s and not pre-merge %s", head, commitSHA, preMergeHead)
	}
	assertMergeSideEffects(t, database, repo, "merge", commitSHA, preMergeHead)
	assertMergeSummaryLatest(t, database, "source", commitSHA, true)
	var transcriptCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE content_json GLOB '*"type":"ai_resolve_request"*' OR content_json GLOB '*"type":"ai_resolve_result"*'`).Scan(&transcriptCount); err != nil {
		t.Fatalf("count AI resolve transcript messages: %v", err)
	}
	if transcriptCount != 0 {
		t.Fatalf("failed transcript transaction should leave no AI resolve messages, count=%d", transcriptCount)
	}
}

func TestAIResolveOneShotBasicProviderFailureAbortsMergeWithoutPartialState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead, preMergeHead := setupMergeConflictRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	provider, calls := newScriptedOpenAIProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"resolver unavailable"}}`, http.StatusBadRequest)
	})
	settings := newMergeParityAISettings(t, provider.URL)
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}, Settings: settings}

	result, err := svc.AIResolve(ctx, "source", "target", "merge", "AI resolved merge", "user-1", "en")
	if err != nil {
		t.Fatalf("AIResolve should return failure payload, got error: %v", err)
	}
	if resolved, _ := result["resolved"].(bool); resolved {
		t.Fatalf("AIResolve resolved = true on provider failure: %#v", result)
	}
	if _, ok := result["narratorId"]; ok {
		t.Fatalf("provider failure should not return narratorId: %#v", result)
	}
	if _, ok := result["assistantMessage"]; ok {
		t.Fatalf("provider failure should not return assistantMessage: %#v", result)
	}
	assertAIResolveCompatibilityContract(t, result, "one-shot-basic-fallback", "merge.aiResolveOneShotBasic")
	if *calls != 1 {
		t.Fatalf("provider calls = %d, want 1", *calls)
	}
	assertMergeAbortedWithoutPartialState(t, database, repo, preMergeHead)
}

func assertAIResolveCompatibilityContract(t *testing.T, result map[string]any, mode, fallbackFeature string) {
	t.Helper()
	if got, _ := result["mode"].(string); got != mode {
		t.Fatalf("AIResolve mode = %q, want %q in %#v", got, mode, result)
	}
	if uses, ok := result["usesNarratorSession"].(bool); !ok || uses {
		t.Fatalf("AIResolve should expose usesNarratorSession=false, got %#v", result)
	}
	if requires, ok := result["requiresFullNarrator"].(bool); !ok || requires {
		t.Fatalf("AIResolve should expose requiresFullNarrator=false, got %#v", result)
	}
	if fallbackFeature == "" {
		if _, ok := result["fallbacks"]; ok {
			t.Fatalf("successful AIResolve contract should not include fallbacks: %#v", result)
		}
		return
	}
	if got, _ := result["fallbackCode"].(string); got != fallbackFeature {
		t.Fatalf("AIResolve fallbackCode = %q, want %q in %#v", got, fallbackFeature, result)
	}
	fallbacks, ok := result["fallbacks"].([]ForkFallback)
	if !ok || len(fallbacks) != 1 {
		t.Fatalf("AIResolve fallbacks = %#v, want one ForkFallback", result["fallbacks"])
	}
	fallback := fallbacks[0]
	if fallback.Feature != fallbackFeature || !fallback.Fallback || fallback.Supported || strings.TrimSpace(fallback.Message) == "" {
		t.Fatalf("AIResolve fallback entry mismatch: %#v", fallback)
	}
}

func TestAIResolveMissingPrimaryNarratorReturnsExplicitFallbackContract(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead, preMergeHead := setupMergeConflictRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	if _, err := database.Exec(`DELETE FROM narrators WHERE chapter_id = 'target'`); err != nil {
		t.Fatalf("delete target narrator: %v", err)
	}
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}

	result, err := svc.AIResolve(ctx, "source", "target", "merge", "AI resolved merge", "user-1", "en")
	if err != nil {
		t.Fatalf("AIResolve missing-narrator fallback should not error: %v", err)
	}
	if resolved, _ := result["resolved"].(bool); resolved {
		t.Fatalf("missing narrator should not resolve: %#v", result)
	}
	if errText, _ := result["error"].(string); errText != "Target chapter has no primary narrator" {
		t.Fatalf("missing narrator error = %q, result=%#v", errText, result)
	}
	assertAIResolveCompatibilityContract(t, result, "one-shot-basic-fallback", "merge.aiResolveMissingNarrator")
	assertMergeAbortedWithoutPartialState(t, database, repo, preMergeHead)
}

func TestMergeSummaryFallbackSuccessMarksHistoricalAndUpdatesNarratorRefs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	preMergeHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	oldBlocks := []map[string]any{
		{"type": "text", "text": "old merge summary"},
		{"type": "merge_summary", "sourceChapterId": "source", "commitSha": "old-sha", "isLatest": true, "mergeRound": 1},
	}
	oldJSON, _ := json.Marshal(oldBlocks)
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('old-summary', 'narrator-target', 'user', ?, 'old merge summary', 'now')`, string(oldJSON)); err != nil {
		t.Fatalf("insert old summary: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES ('old-ref', 'narrator-target', 'old-summary', 1, 0)`); err != nil {
		t.Fatalf("insert old summary ref: %v", err)
	}
	if _, err := database.Exec(`UPDATE narrators SET message_count = 1 WHERE id = 'narrator-target'`); err != nil {
		t.Fatalf("seed narrator count: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO users (id, username, password_hash, role, avatar_color, avatar_image_id, created_at) VALUES ('user-1', 'alice', 'hash', 'user', '#123456', 'avatar-1', 'now')`); err != nil {
		t.Fatalf("insert merging user: %v", err)
	}
	var narratorBroadcasts []struct {
		narratorID string
		payload    map[string]any
	}
	var generatedInput mergeSummaryGenerationInput
	svc := &Service{
		DB:  database,
		Git: &gitx.Service{Timeout: 10 * time.Second},
		MergeSummaryGenerator: func(ctx context.Context, input mergeSummaryGenerationInput) (string, error) {
			generatedInput = input
			return "Model generated merge summary.", nil
		},
		BroadcastNarrator: func(narratorID string, payload any) {
			item, ok := payload.(map[string]any)
			if !ok {
				t.Fatalf("narrator broadcast payload should be map, got %#v", payload)
			}
			narratorBroadcasts = append(narratorBroadcasts, struct {
				narratorID string
				payload    map[string]any
			}{narratorID: narratorID, payload: item})
		},
	}

	result, err := svc.Merge(ctx, "source", "target", "merge", "Documented merge summary", "user-1")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	commitSHA, _ := result["mergeCommitSha"].(string)
	if commitSHA == "" || commitSHA == preMergeHead {
		t.Fatalf("mergeCommitSha = %q preMergeHead=%s result=%#v", commitSHA, preMergeHead, result)
	}

	oldBlock := mergeSummaryBlockByMessage(t, database, "old-summary")
	if latest, _ := oldBlock["isLatest"].(bool); latest {
		t.Fatalf("old summary should be historical: %#v", oldBlock)
	}
	newBlock := assertMergeSummaryLatest(t, database, "source", commitSHA, true)
	if round := intFromJSONNumber(newBlock["mergeRound"]); round != 2 {
		t.Fatalf("mergeRound = %d, want 2 in %#v", round, newBlock)
	}
	if newBlock["mergedBy"] != "alice" {
		t.Fatalf("mergedBy = %#v, want username alice in %#v", newBlock["mergedBy"], newBlock)
	}
	if newBlock["summary"] != "Model generated merge summary." || newBlock["summarySource"] != "model" {
		t.Fatalf("summary model output not persisted, got %#v", newBlock)
	}
	if generatedInput.NarratorID != "narrator-target" || generatedInput.MergedBy != "alice" || generatedInput.Source.ID != "source" || generatedInput.Target.ID != "target" || generatedInput.Locale != "en" {
		t.Fatalf("unexpected model generation input: %#v", generatedInput)
	}
	summaryInput, ok := newBlock["summaryInput"].(map[string]any)
	if !ok || summaryInput["preCollected"] != true {
		t.Fatalf("summaryInput should be pre-collected metadata, got %#v in %#v", newBlock["summaryInput"], newBlock)
	}
	commits, ok := summaryInput["commits"].([]any)
	if !ok || !containsAnyString(commits, "source committed") {
		t.Fatalf("summaryInput commits = %#v, want source committed", summaryInput["commits"])
	}
	if !containsString(generatedInput.Context.Commits, "source committed") {
		t.Fatalf("model generation commits = %#v, want source committed", generatedInput.Context.Commits)
	}
	diffStat, _ := summaryInput["diffStat"].(string)
	if !strings.Contains(diffStat, "source.txt") {
		t.Fatalf("summaryInput diffStat = %q, want source.txt", diffStat)
	}
	if !strings.Contains(generatedInput.Context.DiffStat, "source.txt") {
		t.Fatalf("model generation diffStat = %q, want source.txt", generatedInput.Context.DiffStat)
	}
	var contentText, createdBy string
	if err := database.QueryRow(`SELECT content_text, created_by FROM narrator_messages WHERE content_json LIKE ?`, "%"+commitSHA+"%").Scan(&contentText, &createdBy); err != nil {
		t.Fatalf("query new merge summary message: %v", err)
	}
	if !strings.Contains(contentText, `"chapter/source" → "main" by alice (merge)`) {
		t.Fatalf("content_text = %q, want merged-by username header", contentText)
	}
	if !strings.Contains(contentText, "Model generated merge summary.") {
		t.Fatalf("content_text = %q, want model-generated summary", contentText)
	}
	if createdBy != "user-1" {
		t.Fatalf("created_by = %q, want stable user id user-1", createdBy)
	}
	var messageCount, messageVersion int
	if err := database.QueryRow(`SELECT message_count, message_version FROM narrators WHERE id = 'narrator-target'`).Scan(&messageCount, &messageVersion); err != nil {
		t.Fatalf("query narrator counters: %v", err)
	}
	if messageCount != 2 || messageVersion != 1 {
		t.Fatalf("narrator counters = count:%d version:%d, want 2/1", messageCount, messageVersion)
	}
	var maxSeq int
	if err := database.QueryRow(`SELECT MAX(seq) FROM narrator_message_refs WHERE narrator_id = 'narrator-target'`).Scan(&maxSeq); err != nil {
		t.Fatalf("query max seq: %v", err)
	}
	if maxSeq != 2 {
		t.Fatalf("max ref seq = %d, want 2", maxSeq)
	}
	if len(narratorBroadcasts) != 1 {
		t.Fatalf("expected one narrator message broadcast, got %#v", narratorBroadcasts)
	}
	broadcast := narratorBroadcasts[0]
	if broadcast.narratorID != "narrator-target" || broadcast.payload["type"] != "message" || broadcast.payload["narratorId"] != "narrator-target" {
		t.Fatalf("unexpected narrator broadcast envelope: %#v", narratorBroadcasts)
	}
	broadcastMessage, ok := broadcast.payload["message"].(map[string]any)
	if !ok {
		t.Fatalf("broadcast message should be map, got %#v", broadcast.payload["message"])
	}
	if broadcastMessage["role"] != "user" || broadcastMessage["createdBy"] != "user-1" || broadcastMessage["narratorId"] != "narrator-target" {
		t.Fatalf("unexpected broadcast message identity fields: %#v", broadcastMessage)
	}
	if intFromJSONNumber(broadcastMessage["seq"]) != 2 {
		t.Fatalf("broadcast seq = %#v, want 2 in %#v", broadcastMessage["seq"], broadcastMessage)
	}
	broadcastText, _ := broadcastMessage["contentText"].(string)
	if !strings.Contains(broadcastText, contentText) {
		t.Fatalf("broadcast contentText = %q, want persisted summary %q", broadcastText, contentText)
	}
	creator, ok := broadcastMessage["creator"].(map[string]any)
	if !ok {
		t.Fatalf("broadcast creator should be populated with avatar info, got %#v", broadcastMessage["creator"])
	}
	if creator["id"] != "user-1" || creator["username"] != "alice" || creator["avatarColor"] != "#123456" || creator["avatarImageId"] != "avatar-1" {
		t.Fatalf("unexpected broadcast creator payload: %#v", creator)
	}
	contentBlocks, ok := broadcastMessage["contentJson"].([]map[string]any)
	if !ok || len(contentBlocks) != 2 || contentBlocks[1]["mergedBy"] != "alice" {
		t.Fatalf("unexpected broadcast contentJson: %#v", broadcastMessage["contentJson"])
	}
}

func TestMergeSummaryPromptBuilderMatchesTypeScriptLocaleContract(t *testing.T) {
	description := "Implements parity proof"
	input := mergeSummaryGenerationInput{
		Source: Chapter{
			Branch:      "chapter/source",
			Title:       "Source title",
			Description: &description,
		},
		Target:    Chapter{Branch: "main"},
		Strategy:  "merge",
		CommitSHA: "abc123",
		MergedBy:  "alice",
		Locale:    "en",
		Context: mergeSummaryContext{
			Commits:  []string{"first commit", "second commit"},
			DiffStat: " source.go | 2 ++",
		},
	}
	expectedEnglishUserText := "Branch: chapter/source\n" +
		"Merged into: main\n" +
		"Strategy: merge\n" +
		"Merge commit: abc123\n" +
		"Merged by: alice\n" +
		"Chapter title: Source title\n" +
		"Description: Implements parity proof\n" +
		"\n" +
		"Commits (2):\n" +
		"1. first commit\n" +
		"2. second commit\n" +
		"\n" +
		"Diff summary:\n" +
		" source.go | 2 ++"
	if got := buildMergeSummaryUserTextGo(input); got != expectedEnglishUserText {
		t.Fatalf("english merge summary user text mismatch:\n%s", got)
	}
	expectedEnglishPrompt := `You are a merge summary generator. Given the commit history of a branch that was just merged, produce a concise summary describing what the branch accomplished. This summary will be injected into the parent branch's narrator context so it can be aware of the merged work.

Focus on:
- What features, fixes, or changes were implemented
- Key files and modules affected
- Any notable technical decisions

Keep it under 300 words. Be factual and specific. Output ONLY the summary text. Always respond in English.`
	if got := mergeSummarySystemPromptGo("en"); got != expectedEnglishPrompt {
		t.Fatalf("english merge summary system prompt mismatch:\n%s", got)
	}

	input.Locale = "zh-CN"
	expectedChineseUserText := "分支: chapter/source\n" +
		"合并到: main\n" +
		"策略: merge\n" +
		"合并提交: abc123\n" +
		"合并者: alice\n" +
		"章节标题: Source title\n" +
		"描述: Implements parity proof\n" +
		"\n" +
		"提交记录 (2):\n" +
		"1. first commit\n" +
		"2. second commit\n" +
		"\n" +
		"变更统计:\n" +
		" source.go | 2 ++"
	if got := buildMergeSummaryUserTextGo(input); got != expectedChineseUserText {
		t.Fatalf("chinese merge summary user text mismatch:\n%s", got)
	}
	expectedChinesePrompt := `你是一个合并摘要生成器。根据刚刚合并的分支的提交历史，生成一个简洁的摘要，描述该分支完成了什么工作。此摘要将注入到父分支的叙述者上下文中，使其感知到已合并的工作内容。

重点关注：
- 实现了哪些功能、修复或变更
- 涉及的关键文件和模块
- 任何值得注意的技术决策

控制在 300 字以内。保持客观和具体。只输出摘要文本。始终使用简体中文回复。`
	if got := mergeSummarySystemPromptGo("zh-CN"); got != expectedChinesePrompt {
		t.Fatalf("chinese merge summary system prompt mismatch:\n%s", got)
	}
}

func TestMergeSummaryUsesUserLanguageForPromptAndHeader(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	if _, err := database.Exec(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('user-1', 'alice', 'hash', 'user', 'now')`); err != nil {
		t.Fatalf("insert merging user: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO user_preferences (id, user_id, language, updated_at) VALUES ('pref-user-1', 'user-1', 'zh-CN', 'now')`); err != nil {
		t.Fatalf("insert user language: %v", err)
	}
	var generatedInput mergeSummaryGenerationInput
	var generatedUserText, generatedSystemPrompt string
	svc := &Service{
		DB:  database,
		Git: &gitx.Service{Timeout: 10 * time.Second},
		MergeSummaryGenerator: func(ctx context.Context, input mergeSummaryGenerationInput) (string, error) {
			generatedInput = input
			generatedUserText = buildMergeSummaryUserTextGo(input)
			generatedSystemPrompt = mergeSummarySystemPromptGo(input.Locale)
			return "模型生成的合并摘要。", nil
		},
	}

	result, err := svc.Merge(ctx, "source", "target", "merge", "Fallback", "user-1")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	commitSHA, _ := result["mergeCommitSha"].(string)
	if commitSHA == "" {
		t.Fatalf("expected merge commit sha, result=%#v", result)
	}
	if generatedInput.Locale != "zh-CN" {
		t.Fatalf("generated locale = %q, want zh-CN in %#v", generatedInput.Locale, generatedInput)
	}
	for _, want := range []string{"分支: chapter/source", "合并到: main", "提交记录 (2):", "变更统计:"} {
		if !strings.Contains(generatedUserText, want) {
			t.Fatalf("generated user text missing %q:\n%s", want, generatedUserText)
		}
	}
	if !strings.Contains(generatedSystemPrompt, "始终使用简体中文回复") || strings.Contains(generatedSystemPrompt, "Always respond in English") {
		t.Fatalf("generated system prompt should be zh-CN TS prompt, got:\n%s", generatedSystemPrompt)
	}
	var contentText string
	if err := database.QueryRow(`SELECT content_text FROM narrator_messages WHERE content_json LIKE ?`, "%"+commitSHA+"%").Scan(&contentText); err != nil {
		t.Fatalf("query merge summary content text: %v", err)
	}
	if !strings.Contains(contentText, `[分支已合并] "chapter/source" → "main" by alice (merge)`) {
		t.Fatalf("content_text = %q, want localized TS header", contentText)
	}
}

func TestMergeSummaryModelGenerationRecordsAPIRequest(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	settings := newMergeParityAISettings(t, "http://127.0.0.1")
	if _, err := settings.Patch(map[string]any{"agent": map[string]any{"summaryModel": "openai:gpt-test"}}); err != nil {
		t.Fatalf("patch summary model: %v", err)
	}
	svc := &Service{
		DB:       database,
		Git:      &gitx.Service{Timeout: 10 * time.Second},
		Settings: settings,
		MergeSummaryGenerator: func(ctx context.Context, input mergeSummaryGenerationInput) (string, error) {
			return "Tracked merge summary.", nil
		},
	}

	result, err := svc.Merge(ctx, "source", "target", "merge", "Tracked summary", "user-1")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("unexpected merge result: %#v", result)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM api_requests`).Scan(&count); err != nil {
		t.Fatalf("count api requests: %v", err)
	}
	if count != 1 {
		t.Fatalf("api request count = %d, want 1", count)
	}
	var narratorID, kind, provider, model string
	var durationMS int
	var errorMessage sql.NullString
	if err := database.QueryRow(`SELECT narrator_id, kind, provider, model, duration_ms, error_message FROM api_requests LIMIT 1`).Scan(&narratorID, &kind, &provider, &model, &durationMS, &errorMessage); err != nil {
		t.Fatalf("load api request: %v", err)
	}
	if narratorID != "narrator-target" || kind != "merge_summary" || provider != "openai" || model != "gpt-test" {
		t.Fatalf("unexpected tracked api request identity: narrator=%q kind=%q provider=%q model=%q", narratorID, kind, provider, model)
	}
	if durationMS < 0 || errorMessage.Valid {
		t.Fatalf("unexpected tracked api request duration/error: duration=%d error=%v", durationMS, errorMessage)
	}
}

func TestMergeSummaryTransientErrorsRetryAndTrackAttempts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	settings := newMergeParityAISettings(t, "http://127.0.0.1")
	if _, err := settings.Patch(map[string]any{"agent": map[string]any{"summaryModel": "openai:gpt-test"}}); err != nil {
		t.Fatalf("patch summary model: %v", err)
	}
	attempts := 0
	var summaryEvents []map[string]any
	svc := &Service{
		DB:                    database,
		Git:                   &gitx.Service{Timeout: 10 * time.Second},
		Settings:              settings,
		SummaryRetryBaseDelay: -1,
		MergeSummaryGenerator: func(ctx context.Context, input mergeSummaryGenerationInput) (string, error) {
			attempts++
			if attempts < 3 {
				return "", fmt.Errorf("rate limit 429")
			}
			return "Retried model summary.", nil
		},
		BroadcastMergeProgress: func(payload any) {
			item, ok := payload.(map[string]any)
			if !ok {
				t.Fatalf("summary model broadcast payload should be map, got %#v", payload)
			}
			summaryEvents = append(summaryEvents, item)
		},
	}

	result, err := svc.Merge(ctx, "source", "target", "merge", "Retry summary", "user-1")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	commitSHA, _ := result["mergeCommitSha"].(string)
	if commitSHA == "" {
		t.Fatalf("expected merge commit sha, result=%#v", result)
	}
	if attempts != 3 {
		t.Fatalf("summary generator attempts = %d, want 3", attempts)
	}
	newBlock := assertMergeSummaryLatest(t, database, "source", commitSHA, true)
	if newBlock["summary"] != "Retried model summary." || newBlock["summarySource"] != "model" {
		t.Fatalf("summary retry output not persisted as model output: %#v", newBlock)
	}
	if len(summaryEvents) != 0 {
		t.Fatalf("transient retry success should not broadcast summary_model_error/unavailable, got %#v", summaryEvents)
	}
	var total, errors int
	if err := database.QueryRow(`SELECT COUNT(*), SUM(CASE WHEN error_message IS NOT NULL THEN 1 ELSE 0 END) FROM api_requests WHERE kind = 'merge_summary'`).Scan(&total, &errors); err != nil {
		t.Fatalf("count api requests: %v", err)
	}
	if total != 3 || errors != 2 {
		t.Fatalf("api request attempts/errors = %d/%d, want 3/2", total, errors)
	}
}

func TestMergeSummaryProviderUnavailableBroadcastsWithoutErrorBroadcast(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	settings := newMergeParityAISettings(t, "http://127.0.0.1")
	if _, err := settings.Patch(map[string]any{"agent": map[string]any{"summaryModel": "openai:gpt-test"}}); err != nil {
		t.Fatalf("patch summary model: %v", err)
	}
	attempts := 0
	var summaryEvents []map[string]any
	svc := &Service{
		DB:       database,
		Git:      &gitx.Service{Timeout: 10 * time.Second},
		Settings: settings,
		MergeSummaryGenerator: func(ctx context.Context, input mergeSummaryGenerationInput) (string, error) {
			attempts++
			return "", fmt.Errorf("summary provider not configured")
		},
		BroadcastMergeProgress: func(payload any) {
			item, ok := payload.(map[string]any)
			if !ok {
				t.Fatalf("summary model broadcast payload should be map, got %#v", payload)
			}
			summaryEvents = append(summaryEvents, item)
		},
	}

	result, err := svc.Merge(ctx, "source", "target", "merge", "Unavailable fallback", "user-1")
	if err != nil {
		t.Fatalf("Merge should survive summary provider unavailable: %v", err)
	}
	commitSHA, _ := result["mergeCommitSha"].(string)
	if commitSHA == "" {
		t.Fatalf("expected merge commit sha, result=%#v", result)
	}
	if attempts != 1 {
		t.Fatalf("provider-unavailable should not retry, attempts=%d", attempts)
	}
	newBlock := assertMergeSummaryLatest(t, database, "source", commitSHA, true)
	if newBlock["summary"] != "Unavailable fallback" || newBlock["summarySource"] != "deterministic_fallback" {
		t.Fatalf("provider-unavailable fallback not persisted: %#v", newBlock)
	}
	if len(summaryEvents) != 1 || summaryEvents[0]["type"] != "summary_model_unavailable" || summaryEvents[0]["error"] != "summary provider not configured" {
		t.Fatalf("expected one summary_model_unavailable broadcast, got %#v", summaryEvents)
	}
	var total, errors int
	if err := database.QueryRow(`SELECT COUNT(*), SUM(CASE WHEN error_message IS NOT NULL THEN 1 ELSE 0 END) FROM api_requests WHERE kind = 'merge_summary'`).Scan(&total, &errors); err != nil {
		t.Fatalf("count api requests: %v", err)
	}
	if total != 1 || errors != 1 {
		t.Fatalf("api request attempts/errors = %d/%d, want 1/1", total, errors)
	}
}

func TestMergeSummaryModelErrorFallsBackAndBroadcasts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	var summaryEvents []map[string]any
	svc := &Service{
		DB:  database,
		Git: &gitx.Service{Timeout: 10 * time.Second},
		MergeSummaryGenerator: func(ctx context.Context, input mergeSummaryGenerationInput) (string, error) {
			return "", fmt.Errorf("summary provider failed")
		},
		BroadcastMergeProgress: func(payload any) {
			item, ok := payload.(map[string]any)
			if !ok {
				t.Fatalf("summary model broadcast payload should be map, got %#v", payload)
			}
			summaryEvents = append(summaryEvents, item)
		},
	}

	result, err := svc.Merge(ctx, "source", "target", "merge", "Fallback merge summary", "user-1")
	if err != nil {
		t.Fatalf("Merge should survive summary model errors: %v", err)
	}
	commitSHA, _ := result["mergeCommitSha"].(string)
	if commitSHA == "" {
		t.Fatalf("expected merge commit sha, result=%#v", result)
	}
	newBlock := assertMergeSummaryLatest(t, database, "source", commitSHA, true)
	if newBlock["summary"] != "Fallback merge summary" || newBlock["summarySource"] != "deterministic_fallback" {
		t.Fatalf("summary fallback not persisted after model error: %#v", newBlock)
	}
	if len(summaryEvents) != 1 || summaryEvents[0]["type"] != "summary_model_error" || summaryEvents[0]["error"] != "summary provider failed" {
		t.Fatalf("expected one summary_model_error broadcast, got %#v", summaryEvents)
	}
}

func TestMergeRecordMergedSyncsSourceTargetEdgeCommitRowsAndEvent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	preMergeHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	var broadcasts []map[string]any
	svc := &Service{
		DB:  database,
		Git: &gitx.Service{Timeout: 10 * time.Second},
		BroadcastChapterLifecycle: func(payload any) {
			item, ok := payload.(map[string]any)
			if !ok {
				t.Fatalf("chapter broadcast payload should be map, got %#v", payload)
			}
			broadcasts = append(broadcasts, item)
		},
	}

	result, err := svc.Merge(ctx, "source", "target", "merge", "Record merged fixture", "user-1")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	commitSHA, _ := result["mergeCommitSha"].(string)
	if ok, _ := result["ok"].(bool); !ok || commitSHA == "" {
		t.Fatalf("unexpected merge result: %#v", result)
	}
	assertMergeSideEffects(t, database, repo, "merge", commitSHA, preMergeHead)
	if len(broadcasts) != 1 || broadcasts[0]["type"] != "chapter:merged" || broadcasts[0]["sourceId"] != "source" || broadcasts[0]["targetId"] != "target" || broadcasts[0]["projectId"] != "project" || broadcasts[0]["userId"] != "user-1" {
		t.Fatalf("expected chapter:merged broadcast, got %#v", broadcasts)
	}
}

func TestMergeCleansSourceWorktreeAndTerminalState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	if _, err := database.Exec(`INSERT INTO terminals (id, chapter_id) VALUES ('term-source', 'source')`); err != nil {
		t.Fatalf("insert source terminal: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO terminal_tabs (id, chapter_id) VALUES ('tab-source', 'source')`); err != nil {
		t.Fatalf("insert source terminal tab: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO terminal_view_state (id, chapter_id) VALUES ('view-source', 'source')`); err != nil {
		t.Fatalf("insert source terminal view state: %v", err)
	}
	terminalRuntime := &mergeRecordingTerminalKiller{}
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}, TerminalRuntime: terminalRuntime}

	result, err := svc.Merge(ctx, "source", "target", "merge", "Cleanup source worktree fixture", "user-1")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("unexpected merge result: %#v", result)
	}
	if _, err := os.Stat(sourceWT); !os.IsNotExist(err) {
		t.Fatalf("source worktree should be removed after merge, stat err=%v", err)
	}
	if len(terminalRuntime.killed) != 1 || terminalRuntime.killed[0] != "term-source" {
		t.Fatalf("terminal kills = %#v, want term-source", terminalRuntime.killed)
	}
	for label, query := range map[string]string{
		"terminals":           `SELECT COUNT(*) FROM terminals WHERE chapter_id = 'source'`,
		"terminal tabs":       `SELECT COUNT(*) FROM terminal_tabs WHERE chapter_id = 'source'`,
		"terminal view state": `SELECT COUNT(*) FROM terminal_view_state WHERE chapter_id = 'source'`,
	} {
		var count int
		if err := database.QueryRow(query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", label, err)
		}
		if count != 0 {
			t.Fatalf("%s after merge cleanup = %d, want 0", label, count)
		}
	}
}

func TestMergeDatabaseUpdateFailureRollsBackGitAndLeavesDBUnchanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	preMergeHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	if _, err := database.Exec(`CREATE TRIGGER fail_record_merged_update BEFORE UPDATE OF status ON chapters WHEN NEW.status = 'merged' BEGIN SELECT RAISE(ABORT, 'record merged blocked'); END;`); err != nil {
		t.Fatalf("create recordMerged trigger: %v", err)
	}
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}

	result, err := svc.Merge(ctx, "source", "target", "merge", "Record merged blocked", "user-1")
	if err != nil {
		t.Fatalf("Merge should return rollback payload, got error: %v", err)
	}
	if ok, _ := result["ok"].(bool); ok {
		t.Fatalf("rollback result ok = true: %#v", result)
	}
	if warning, _ := result["warning"].(string); !strings.Contains(warning, "Merge rolled back") {
		t.Fatalf("warning = %q, want rollback warning in %#v", warning, result)
	}
	assertMergeAbortedWithoutPartialState(t, database, repo, preMergeHead)
}

func TestMergePostCommitRecordMergedFailureKeepsGitAndCoreDBState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	preMergeHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	if _, err := database.Exec(`CREATE TRIGGER fail_post_commit_record BEFORE INSERT ON chapter_commits BEGIN SELECT RAISE(ABORT, 'chapter commit insert blocked'); END;`); err != nil {
		t.Fatalf("create post-commit trigger: %v", err)
	}
	svc := &Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}}

	result, err := svc.Merge(ctx, "source", "target", "merge", "Post commit blocked", "user-1")
	if err != nil {
		t.Fatalf("Merge should return success with warning, got error: %v", err)
	}
	commitSHA, _ := result["mergeCommitSha"].(string)
	if ok, _ := result["ok"].(bool); !ok || commitSHA == "" || commitSHA == preMergeHead {
		t.Fatalf("expected success payload with merge commit, got %#v", result)
	}
	if warning, _ := result["warning"].(string); !strings.Contains(warning, "post-merge follow-up failed") {
		t.Fatalf("warning = %q, want post-merge warning in %#v", warning, result)
	}
	assertMergeCoreSideEffectsWithoutCommitRow(t, database, repo, commitSHA, preMergeHead)
}

func setupMergeParityRepo(t *testing.T) (repo string, sourceWT string, sourceHead string) {
	t.Helper()
	repo = t.TempDir()
	runGitChapterTest(t, repo, "init")
	runGitChapterTest(t, repo, "checkout", "-b", "main")
	runGitChapterTest(t, repo, "config", "user.name", "Test User")
	runGitChapterTest(t, repo, "config", "user.email", "test@example.com")
	writeChapterTestFile(t, repo, "base.txt", "base\n")
	runGitChapterTest(t, repo, "add", ".")
	runGitChapterTest(t, repo, "commit", "-m", "base")
	runGitChapterTest(t, repo, "branch", "chapter/source")
	writeChapterTestFile(t, repo, ".git/info/exclude", ".worktrees/\n")
	sourceWT = filepath.Join(repo, ".worktrees", "source")
	runGitChapterTest(t, repo, "worktree", "add", sourceWT, "chapter/source")
	writeChapterTestFile(t, sourceWT, "source.txt", "committed\n")
	runGitChapterTest(t, sourceWT, "add", ".")
	runGitChapterTest(t, sourceWT, "commit", "-m", "source committed")
	sourceHead = strings.TrimSpace(runGitChapterTest(t, sourceWT, "rev-parse", "HEAD"))
	writeChapterTestFile(t, sourceWT, "dirty.txt", "dirty\n")
	return repo, sourceWT, sourceHead
}

func TestDeleteChapterReturnsNarratorArchiveErrorWithoutDeletingChapter(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO projects (id, name) VALUES ('project', 'Project')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('delete-me', 'project', 'Delete Me', 'active', 'branch', 'chapter/delete-me', 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, created_at, updated_at) VALUES ('narrator-delete', 'delete-me', 'primary', 'test-model', 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_narrator_archive BEFORE UPDATE ON narrators WHEN OLD.chapter_id = 'delete-me' BEGIN SELECT RAISE(ABORT, 'archive blocked'); END;`); err != nil {
		t.Fatalf("create archive trigger: %v", err)
	}
	s := &Service{DB: database}

	err := s.Delete(context.Background(), "delete-me")
	if err == nil || !strings.Contains(err.Error(), "archive chapter narrators") {
		t.Fatalf("expected narrator archive error, got %v", err)
	}
	var chapterCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapters WHERE id = 'delete-me'`).Scan(&chapterCount); err != nil {
		t.Fatalf("count chapter: %v", err)
	}
	if chapterCount != 1 {
		t.Fatalf("failed archive should not delete chapter, count=%d", chapterCount)
	}
	var narratorChapterID string
	if err := database.QueryRow(`SELECT chapter_id FROM narrators WHERE id = 'narrator-delete'`).Scan(&narratorChapterID); err != nil {
		t.Fatalf("load narrator chapter: %v", err)
	}
	if narratorChapterID != "delete-me" {
		t.Fatalf("failed archive should keep narrator chapter reference, got %q", narratorChapterID)
	}
}

func TestDeleteChapterInterruptsNarratorsBeforeArchiving(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO projects (id, name) VALUES ('project', 'Project')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('delete-me', 'project', 'Delete Me', 'active', 'branch', 'chapter/delete-me', 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, created_at, updated_at) VALUES ('narrator-delete', 'delete-me', 'primary', 'test-model', 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`CREATE TABLE interrupt_log (narrator_id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create interrupt log: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_archive_without_interrupt BEFORE UPDATE ON narrators WHEN OLD.chapter_id = 'delete-me' AND (SELECT COUNT(*) FROM interrupt_log WHERE narrator_id = OLD.id) = 0 BEGIN SELECT RAISE(ABORT, 'interrupt missing'); END;`); err != nil {
		t.Fatalf("create archive trigger: %v", err)
	}
	interrupted := []string{}
	service := &Service{
		DB: database,
		CancelNarratorRun: func(narratorID string) bool {
			interrupted = append(interrupted, narratorID)
			if _, err := database.Exec(`INSERT INTO interrupt_log (narrator_id) VALUES (?)`, narratorID); err != nil {
				t.Fatalf("record interrupt: %v", err)
			}
			return true
		},
	}

	if err := service.Delete(context.Background(), "delete-me"); err != nil {
		t.Fatalf("delete chapter: %v", err)
	}
	if len(interrupted) != 1 || interrupted[0] != "narrator-delete" {
		t.Fatalf("expected narrator interrupt before archive, got %#v", interrupted)
	}
	var narratorChapterID sql.NullString
	if err := database.QueryRow(`SELECT chapter_id FROM narrators WHERE id = 'narrator-delete'`).Scan(&narratorChapterID); err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	if narratorChapterID.Valid {
		t.Fatalf("expected detached narrator after archive, chapter=%v", narratorChapterID)
	}
}

func TestDeleteChapterRemovesRecentTabsAndBroadcastsSnapshots(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO projects (id, name) VALUES ('project', 'Project')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('delete-me', 'project', 'Delete Me', 'active', 'branch', 'chapter/delete-me', 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO user_preferences (id, user_id, recent_tabs, updated_at) VALUES ('pref1', 'u1', '[{"type":"chapter","id":"delete-me","title":"Delete Me"},{"type":"narrator","id":"n1","chapterId":"delete-me"},{"type":"project","id":"project","title":"Project"}]', 'now')`); err != nil {
		t.Fatalf("insert user preferences: %v", err)
	}

	var chapterBroadcasts []map[string]any
	var userBroadcasts []struct {
		userID  string
		payload map[string]any
	}
	recordChapterBroadcast := func(payload any) {
		item, ok := payload.(map[string]any)
		if !ok {
			t.Fatalf("chapter broadcast payload should be map, got %#v", payload)
		}
		if item["type"] == "chapter:abandoned" {
			chapterBroadcasts = append(chapterBroadcasts, item)
		}
	}
	service := &Service{
		DB:                        database,
		BroadcastMergeProgress:    recordChapterBroadcast,
		BroadcastChapterLifecycle: recordChapterBroadcast,
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
	if err := service.Delete(context.Background(), "delete-me"); err != nil {
		t.Fatalf("delete chapter: %v", err)
	}
	var tabs string
	if err := database.QueryRow(`SELECT recent_tabs FROM user_preferences WHERE user_id = 'u1'`).Scan(&tabs); err != nil {
		t.Fatalf("load recent tabs: %v", err)
	}
	if strings.Contains(tabs, "delete-me") || !strings.Contains(tabs, `"type":"project"`) {
		t.Fatalf("recent_tabs should remove chapter references and keep unrelated tabs, got %s", tabs)
	}
	if len(chapterBroadcasts) != 1 || chapterBroadcasts[0]["type"] != "chapter:abandoned" || chapterBroadcasts[0]["chapterId"] != "delete-me" || chapterBroadcasts[0]["projectId"] != "project" {
		t.Fatalf("expected chapter:abandoned broadcast, got %#v", chapterBroadcasts)
	}
	if len(userBroadcasts) != 1 || userBroadcasts[0].userID != "u1" || userBroadcasts[0].payload["type"] != "user:recent_tabs_snapshot" {
		t.Fatalf("expected recent-tabs user broadcast, got %#v", userBroadcasts)
	}
	items, ok := userBroadcasts[0].payload["tabs"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["type"] != "project" || items[0]["id"] != "project" {
		t.Fatalf("expected broadcast snapshot to keep only project tab, got %#v", userBroadcasts[0].payload["tabs"])
	}
}

func TestDeleteChapterArchivesNarratorsBeforeResourceCleanup(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO projects (id, name) VALUES ('project', 'Project')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('delete-me', 'project', 'Delete Me', 'active', 'branch', 'chapter/delete-me', 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, created_at, updated_at) VALUES ('narrator-delete', 'delete-me', 'primary', 'test-model', 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO terminal_tabs (id, chapter_id) VALUES ('tab-delete', 'delete-me')`); err != nil {
		t.Fatalf("insert terminal tab: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_terminal_cleanup_before_archive BEFORE DELETE ON terminal_tabs WHEN OLD.chapter_id = 'delete-me' AND EXISTS (SELECT 1 FROM narrators WHERE id = 'narrator-delete' AND chapter_id = 'delete-me') BEGIN SELECT RAISE(ABORT, 'archive missing before terminal cleanup'); END;`); err != nil {
		t.Fatalf("create terminal cleanup trigger: %v", err)
	}
	service := &Service{DB: database}

	if err := service.Delete(context.Background(), "delete-me"); err != nil {
		t.Fatalf("delete chapter: %v", err)
	}
	var narratorChapterID sql.NullString
	if err := database.QueryRow(`SELECT chapter_id FROM narrators WHERE id = 'narrator-delete'`).Scan(&narratorChapterID); err != nil {
		t.Fatalf("load narrator chapter: %v", err)
	}
	if narratorChapterID.Valid {
		t.Fatalf("expected narrator archived before resource cleanup, chapter=%q", narratorChapterID.String)
	}
}

func TestDeleteChapterDeletesTargetMergeSessions(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO projects (id, name) VALUES ('project', 'Project')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('delete-me', 'project', 'Delete Me', 'active', 'branch', 'chapter/delete-me', 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO merge_sessions (id, target_chapter_id, source_chapter_ids, strategy, status, current_index, merged_count, created_at, updated_at) VALUES ('merge-delete', 'delete-me', '[]', 'merge', 'running', 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert merge session: %v", err)
	}
	service := &Service{DB: database}

	if err := service.Delete(context.Background(), "delete-me"); err != nil {
		t.Fatalf("delete chapter: %v", err)
	}
	var sessionCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM merge_sessions WHERE target_chapter_id = 'delete-me'`).Scan(&sessionCount); err != nil {
		t.Fatalf("count merge sessions: %v", err)
	}
	if sessionCount != 0 {
		t.Fatalf("expected target merge sessions deleted, count=%d", sessionCount)
	}
}

func TestDeleteChapterRecentTabsCleanupFailureIsNonFatal(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO projects (id, name) VALUES ('project', 'Project')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('delete-me', 'project', 'Delete Me', 'active', 'branch', 'chapter/delete-me', 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO user_preferences (id, user_id, recent_tabs, updated_at) VALUES ('pref1', 'u1', '[{"type":"chapter","id":"delete-me","title":"Delete Me"}]', 'now')`); err != nil {
		t.Fatalf("insert user preferences: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_recent_tab_cleanup BEFORE UPDATE ON user_preferences WHEN OLD.user_id = 'u1' BEGIN SELECT RAISE(ABORT, 'recent tabs blocked'); END;`); err != nil {
		t.Fatalf("create recent-tabs trigger: %v", err)
	}
	var chapterBroadcasts []map[string]any
	service := &Service{
		DB: database,
		BroadcastChapterLifecycle: func(payload any) {
			item, ok := payload.(map[string]any)
			if !ok {
				t.Fatalf("chapter broadcast payload should be map, got %#v", payload)
			}
			chapterBroadcasts = append(chapterBroadcasts, item)
		},
	}

	if err := service.Delete(context.Background(), "delete-me"); err != nil {
		t.Fatalf("delete chapter should ignore recent-tabs cleanup failure: %v", err)
	}
	var chapterCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapters WHERE id = 'delete-me'`).Scan(&chapterCount); err != nil {
		t.Fatalf("count chapter: %v", err)
	}
	if chapterCount != 0 {
		t.Fatalf("expected chapter deleted despite recent-tabs cleanup failure, count=%d", chapterCount)
	}
	if len(chapterBroadcasts) != 1 || chapterBroadcasts[0]["type"] != "chapter:abandoned" {
		t.Fatalf("expected chapter abandoned broadcast despite recent-tabs cleanup failure, got %#v", chapterBroadcasts)
	}
}

func TestUnmergeBroadcastsWakeAndReloadsAffectedNarrators(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repo, sourceWT, sourceHead := setupCleanMergeParityRepo(t)
	preMergeHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)
	var lifecycleEvents []map[string]any
	recordLifecycle := func(payload any) {
		item, ok := payload.(map[string]any)
		if !ok {
			t.Fatalf("lifecycle payload should be map, got %#v", payload)
		}
		lifecycleEvents = append(lifecycleEvents, item)
	}
	var narratorEvents []map[string]any
	svc := &Service{
		DB:                        database,
		Git:                       &gitx.Service{Timeout: 10 * time.Second},
		BroadcastMergeProgress:    recordLifecycle,
		BroadcastChapterLifecycle: recordLifecycle,
		BroadcastNarrator: func(narratorID string, payload any) {
			item, ok := payload.(map[string]any)
			if !ok {
				t.Fatalf("narrator payload should be map, got %#v", payload)
			}
			item["_narratorID"] = narratorID
			narratorEvents = append(narratorEvents, item)
		},
	}

	mergeResult, err := svc.Merge(ctx, "source", "target", "merge", "Summary to remove", "user-1")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	commitSHA, _ := mergeResult["mergeCommitSha"].(string)
	if commitSHA == "" {
		t.Fatalf("expected merge commit sha, result=%#v", mergeResult)
	}
	lifecycleEvents = nil
	narratorEvents = nil
	unmergeResult, err := svc.Unmerge(ctx, "source")
	if err != nil {
		t.Fatalf("Unmerge: %v", err)
	}
	if ok, _ := unmergeResult["ok"].(bool); !ok {
		t.Fatalf("unexpected unmerge result: %#v", unmergeResult)
	}
	if !containsEvent(lifecycleEvents, "chapter:woken", "chapterId", "source") {
		t.Fatalf("expected chapter:woken event after unmerge, got %#v", lifecycleEvents)
	}
	if !containsEvent(narratorEvents, "full_reload", "_narratorID", "narrator-target") {
		t.Fatalf("expected narrator full_reload after unmerge summary cleanup, got %#v", narratorEvents)
	}
	var targetHead sql.NullString
	var targetCount int
	if err := database.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'target'`).Scan(&targetHead, &targetCount); err != nil {
		t.Fatalf("query target commit metadata after unmerge: %v", err)
	}
	expectedTargetCount := gitCommitCountForTest(t, repo)
	if !targetHead.Valid || targetHead.String != preMergeHead || targetCount != expectedTargetCount {
		t.Fatalf("target commit metadata after unmerge = head:%v count:%d, want head:%s count:%d", targetHead, targetCount, preMergeHead, expectedTargetCount)
	}
	var targetPreMergeRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'target' AND sha = ?`, preMergeHead).Scan(&targetPreMergeRows); err != nil {
		t.Fatalf("count target synced pre-merge commit row: %v", err)
	}
	if targetPreMergeRows == 0 {
		t.Fatalf("expected target commit sync to retain pre-merge head row %s", preMergeHead)
	}
	var sourceWorktree, sourceHeadCache sql.NullString
	var sourceCount int
	if err := database.QueryRow(`SELECT worktree_path, head_commit_sha, commit_count FROM chapters WHERE id = 'source'`).Scan(&sourceWorktree, &sourceHeadCache, &sourceCount); err != nil {
		t.Fatalf("query source commit metadata after unmerge: %v", err)
	}
	if !sourceWorktree.Valid || strings.TrimSpace(sourceWorktree.String) == "" {
		t.Fatalf("expected source worktree restored after unmerge, got %v", sourceWorktree)
	}
	expectedSourceWorktree := filepath.Join(repo, ".worktrees", "source")
	if filepath.Clean(sourceWorktree.String) != filepath.Clean(expectedSourceWorktree) {
		t.Fatalf("source worktree path after unmerge = %s, want TS branch-suffix path %s", sourceWorktree.String, expectedSourceWorktree)
	}
	if _, err := os.Stat(sourceWorktree.String); err != nil {
		t.Fatalf("expected restored source worktree at %s: %v", sourceWorktree.String, err)
	}
	expectedSourceCount := gitCommitCountForTest(t, sourceWorktree.String)
	if !sourceHeadCache.Valid || sourceHeadCache.String != sourceHead || sourceCount != expectedSourceCount {
		t.Fatalf("source commit metadata after unmerge = head:%v count:%d, want head:%s count:%d", sourceHeadCache, sourceCount, sourceHead, expectedSourceCount)
	}
	var sourceHeadRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'source' AND sha = ?`, sourceHead).Scan(&sourceHeadRows); err != nil {
		t.Fatalf("count source synced head commit row: %v", err)
	}
	if sourceHeadRows == 0 {
		t.Fatalf("expected source commit sync to record restored head row %s", sourceHead)
	}
	var summaryCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE content_json LIKE ? AND content_json LIKE ?`, "%merge_summary%", "%"+commitSHA+"%").Scan(&summaryCount); err != nil {
		t.Fatalf("count removed merge summary: %v", err)
	}
	if summaryCount != 0 {
		t.Fatalf("expected merge summary for %s removed after unmerge, count=%d", commitSHA, summaryCount)
	}
}

func TestCleanupMergeSummaryDeletesCurrentAndRestoresPreviousLatest(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, message_count, message_version, created_at, updated_at) VALUES ('narrator-target', 'target', 'primary', 'test-model', 2, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	oldBlocks := []map[string]any{
		{"type": "text", "text": "old summary"},
		{"type": "merge_summary", "sourceChapterId": "source", "commitSha": "old-sha", "isLatest": false},
	}
	oldJSON, _ := json.Marshal(oldBlocks)
	currentBlocks := []map[string]any{
		{"type": "text", "text": "current summary"},
		{"type": "merge_summary", "sourceChapterId": "source", "commitSha": "abc123", "isLatest": true},
	}
	currentJSON, _ := json.Marshal(currentBlocks)
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('summary-old', 'narrator-target', 'user', ?, 'old summary', 'old-time')`, string(oldJSON)); err != nil {
		t.Fatalf("insert old summary: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('summary-current', 'narrator-target', 'user', ?, 'current summary', 'new-time')`, string(currentJSON)); err != nil {
		t.Fatalf("insert current summary: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES ('ref-old', 'narrator-target', 'summary-old', 1, 0), ('ref-current', 'narrator-target', 'summary-current', 2, 0)`); err != nil {
		t.Fatalf("insert summary refs: %v", err)
	}
	var reloads []map[string]any
	service := &Service{
		DB: database,
		BroadcastNarrator: func(narratorID string, payload any) {
			item, ok := payload.(map[string]any)
			if !ok {
				t.Fatalf("full_reload payload should be map, got %#v", payload)
			}
			item["_narratorID"] = narratorID
			reloads = append(reloads, item)
		},
	}

	if err := service.cleanupMergeSummaryForMerge(context.Background(), "source", "abc123"); err != nil {
		t.Fatalf("cleanup merge summary: %v", err)
	}
	var currentMessages, currentRefs int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE id = 'summary-current'`).Scan(&currentMessages); err != nil {
		t.Fatalf("count current messages: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE id = 'ref-current'`).Scan(&currentRefs); err != nil {
		t.Fatalf("count current refs: %v", err)
	}
	if currentMessages != 0 || currentRefs != 0 {
		t.Fatalf("expected current summary and ref deleted, messages=%d refs=%d", currentMessages, currentRefs)
	}
	oldBlock := mergeSummaryBlockByMessage(t, database, "summary-old")
	if oldBlock["isLatest"] != true {
		t.Fatalf("expected previous merge summary restored as latest, got %#v", oldBlock)
	}
	var messageCount, messageVersion int
	if err := database.QueryRow(`SELECT message_count, message_version FROM narrators WHERE id = 'narrator-target'`).Scan(&messageCount, &messageVersion); err != nil {
		t.Fatalf("query narrator counters: %v", err)
	}
	if messageCount != 1 || messageVersion != 1 {
		t.Fatalf("unexpected narrator counters after cleanup: count=%d version=%d", messageCount, messageVersion)
	}
	if len(reloads) != 1 || reloads[0]["type"] != "full_reload" || reloads[0]["narratorId"] != "narrator-target" || reloads[0]["_narratorID"] != "narrator-target" {
		t.Fatalf("expected narrator full_reload after summary cleanup, got %#v", reloads)
	}
}

func TestCleanupMergeSummaryReturnsNarratorCountUpdateErrors(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, message_count, created_at, updated_at) VALUES ('narrator-target', 'target', 'primary', 'test-model', 1, 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('summary-1', 'narrator-target', 'user', '[{"type":"merge_summary","sourceChapterId":"source","commitSha":"abc123","isLatest":true}]', 'summary', 'now')`); err != nil {
		t.Fatalf("insert summary message: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES ('ref-1', 'narrator-target', 'summary-1', 1, 0)`); err != nil {
		t.Fatalf("insert summary ref: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_merge_summary_count_update BEFORE UPDATE OF message_count ON narrators BEGIN SELECT RAISE(ABORT, 'message count update blocked'); END;`); err != nil {
		t.Fatalf("create message count trigger: %v", err)
	}
	service := &Service{DB: database}

	err := service.cleanupMergeSummaryForMerge(context.Background(), "source", "abc123")
	if err == nil || !strings.Contains(err.Error(), "update merge summary narrator count") {
		t.Fatalf("expected narrator count update error, got %v", err)
	}
	var messageCount, refCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE id = 'summary-1'`).Scan(&messageCount); err != nil {
		t.Fatalf("count summary messages: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE id = 'ref-1'`).Scan(&refCount); err != nil {
		t.Fatalf("count summary refs: %v", err)
	}
	if messageCount != 1 || refCount != 1 {
		t.Fatalf("failed count update should roll back summary cleanup, messages=%d refs=%d", messageCount, refCount)
	}
}

func TestInjectMergeSummaryFallbackReturnsHistoricalUpdateErrors(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('target', 'project', 'Target', 'active', 'trunk', 'main', 'main', 1, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert target chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, created_at, updated_at) VALUES ('narrator-target', 'target', 'primary', 'test-model', 'now', 'now')`); err != nil {
		t.Fatalf("insert target narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('old-summary', 'narrator-target', 'user', '[{"type":"merge_summary","sourceChapterId":"source","isLatest":true}]', 'old summary', 'now')`); err != nil {
		t.Fatalf("insert old summary: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_merge_summary_historical BEFORE UPDATE OF content_json ON narrator_messages WHEN OLD.id = 'old-summary' BEGIN SELECT RAISE(ABORT, 'merge summary historical update blocked'); END;`); err != nil {
		t.Fatalf("create historical trigger: %v", err)
	}
	service := &Service{DB: database}

	err := service.injectMergeSummaryFallback(context.Background(), Chapter{ID: "source", Branch: "feature"}, Chapter{ID: "target", Branch: "main"}, "merge", "abc123", "merged", "user-1", mergeSummaryContext{})
	if err == nil || !strings.Contains(err.Error(), "mark merge summary historical") {
		t.Fatalf("expected historical update error, got %v", err)
	}
	var messageCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = 'narrator-target' AND content_json LIKE '%merge_summary%'`).Scan(&messageCount); err != nil {
		t.Fatalf("count merge summary messages: %v", err)
	}
	if messageCount != 1 {
		t.Fatalf("historical update failure should not insert new summary, count=%d", messageCount)
	}
}

func TestInjectMergeSummaryFallbackReturnsSequenceQueryErrors(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('target', 'project', 'Target', 'active', 'trunk', 'main', 'main', 1, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert target chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, created_at, updated_at) VALUES ('narrator-target', 'target', 'primary', 'test-model', 'now', 'now')`); err != nil {
		t.Fatalf("insert target narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('existing-message', 'narrator-target', 'user', '[{"type":"text","text":"existing"}]', 'existing', 'now')`); err != nil {
		t.Fatalf("insert existing message: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES ('existing-ref', 'narrator-target', 'existing-message', 0, 0)`); err != nil {
		t.Fatalf("insert existing ref: %v", err)
	}
	for _, stmt := range []string{
		`ALTER TABLE narrator_message_refs RENAME TO narrator_message_refs_real`,
		`CREATE VIEW narrator_message_refs AS SELECT id, narrator_id, message_id, json_extract('not-json', '$') AS seq, is_compact FROM narrator_message_refs_real`,
		`CREATE TRIGGER narrator_message_refs_insert INSTEAD OF INSERT ON narrator_message_refs BEGIN INSERT INTO narrator_message_refs_real (id, narrator_id, message_id, seq, is_compact) VALUES (NEW.id, NEW.narrator_id, NEW.message_id, NEW.seq, NEW.is_compact); END;`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("prepare broken ref view: %v", err)
		}
	}
	service := &Service{DB: database}

	err := service.injectMergeSummaryFallback(context.Background(), Chapter{ID: "source", Branch: "feature"}, Chapter{ID: "target", Branch: "main"}, "merge", "abc123", "merged", "user-1", mergeSummaryContext{})
	if err == nil || !strings.Contains(err.Error(), "query merge summary sequence") {
		t.Fatalf("expected merge summary sequence error, got %v", err)
	}
	var messageCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = 'narrator-target' AND content_json LIKE '%merge_summary%'`).Scan(&messageCount); err != nil {
		t.Fatalf("count merge summary messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("sequence query failure should roll back merge summary message, count=%d", messageCount)
	}
	var refCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs_real WHERE narrator_id = 'narrator-target'`).Scan(&refCount); err != nil {
		t.Fatalf("count backing refs: %v", err)
	}
	if refCount != 1 {
		t.Fatalf("sequence query failure should not insert fallback ref, count=%d", refCount)
	}
}

func TestCleanupMergedSourceWorktreeReturnsTerminalDeleteErrors(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path) VALUES ('project', 'Project', '/repo')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO terminal_tabs (id, chapter_id) VALUES ('tab-source', 'source')`,
		`CREATE TRIGGER fail_merged_source_tab_cleanup BEFORE DELETE ON terminal_tabs WHEN OLD.chapter_id = 'source' BEGIN SELECT RAISE(ABORT, 'merged source tab cleanup blocked'); END;`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("prepare terminal cleanup fixture: %v", err)
		}
	}
	worktree := "/outside/source"
	s := &Service{DB: database}

	err := s.cleanupMergedSourceWorktree(context.Background(), Chapter{ID: "source", ProjectID: "project", WorktreePath: &worktree})
	if err == nil || !strings.Contains(err.Error(), "delete merged source terminal tabs") {
		t.Fatalf("expected terminal tab cleanup error, got %v", err)
	}
	var tabCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM terminal_tabs WHERE chapter_id = 'source'`).Scan(&tabCount); err != nil {
		t.Fatalf("count source tabs: %v", err)
	}
	if tabCount != 1 {
		t.Fatalf("failed cleanup should leave terminal tab for retry, count=%d", tabCount)
	}
}

func TestRecordChapterCommitReturnsInsertErrors(t *testing.T) {
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('commit-target', 'project', 'Commit Target', 'active', 'branch', 'main', 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`); err != nil {
		t.Fatalf("insert commit target: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_chapter_commit_insert BEFORE INSERT ON chapter_commits BEGIN SELECT RAISE(ABORT, 'chapter commit insert blocked'); END;`); err != nil {
		t.Fatalf("create commit trigger: %v", err)
	}
	service := &Service{DB: database}
	if err := service.recordChapterCommit(context.Background(), "commit-target", "abc123", "message", "manual"); err == nil {
		t.Fatalf("expected chapter commit insert failure")
	}
}

func TestRefreshChapterGitMetadataReturnsUpdateErrors(t *testing.T) {
	repo := newForkParityRepo(t)
	database := openMergeParityDB(t)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('refresh', 'project', 'Refresh', 'active', 'branch', 'main', ?, 'main', 0, 0, 0, 0, 0, 0, 'now', 'now')`, repo); err != nil {
		t.Fatalf("insert refresh chapter: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_refresh_metadata BEFORE UPDATE OF head_commit_sha ON chapters BEGIN SELECT RAISE(ABORT, 'refresh metadata update blocked'); END;`); err != nil {
		t.Fatalf("create refresh trigger: %v", err)
	}
	h := &Handler{Service: &Service{DB: database, Git: gitx.NewService()}}
	if _, err := h.refreshChapterGitMetadata(context.Background(), "refresh", repo); err == nil {
		t.Fatalf("expected refresh metadata update failure")
	}
}

func setupCleanMergeParityRepo(t *testing.T) (repo string, sourceWT string, sourceHead string) {
	t.Helper()
	repo, sourceWT, _ = setupMergeParityRepo(t)
	runGitChapterTest(t, sourceWT, "add", ".")
	runGitChapterTest(t, sourceWT, "commit", "-m", "source clean")
	sourceHead = strings.TrimSpace(runGitChapterTest(t, sourceWT, "rev-parse", "HEAD"))
	return repo, sourceWT, sourceHead
}

func setupMergeConflictRepo(t *testing.T) (repo string, sourceWT string, sourceHead string, targetHead string) {
	t.Helper()
	repo = t.TempDir()
	runGitChapterTest(t, repo, "init")
	runGitChapterTest(t, repo, "checkout", "-b", "main")
	runGitChapterTest(t, repo, "config", "user.name", "Test User")
	runGitChapterTest(t, repo, "config", "user.email", "test@example.com")
	writeChapterTestFile(t, repo, "base.txt", "base\n")
	writeChapterTestFile(t, repo, "shared.txt", "base\n")
	runGitChapterTest(t, repo, "add", ".")
	runGitChapterTest(t, repo, "commit", "-m", "base")
	runGitChapterTest(t, repo, "branch", "chapter/source")
	writeChapterTestFile(t, repo, ".git/info/exclude", ".worktrees/\n")
	writeChapterTestFile(t, repo, "shared.txt", "target\n")
	runGitChapterTest(t, repo, "add", ".")
	runGitChapterTest(t, repo, "commit", "-m", "target change")
	targetHead = strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD"))
	sourceWT = filepath.Join(repo, ".worktrees", "source")
	runGitChapterTest(t, repo, "worktree", "add", sourceWT, "chapter/source")
	writeChapterTestFile(t, sourceWT, "shared.txt", "source\n")
	runGitChapterTest(t, sourceWT, "add", ".")
	runGitChapterTest(t, sourceWT, "commit", "-m", "source change")
	sourceHead = strings.TrimSpace(runGitChapterTest(t, sourceWT, "rev-parse", "HEAD"))
	return repo, sourceWT, sourceHead, targetHead
}

func newMergeParityAISettings(t *testing.T, baseURL string) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if _, err := settings.Patch(map[string]any{
		"openaiProviders": []any{map[string]any{
			"prefix":       "openai",
			"apiKey":       "test-key",
			"baseUrl":      baseURL,
			"defaultModel": "gpt-test",
		}},
	}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	return settings
}

func newScriptedOpenAIProvider(t *testing.T, handlers ...func(http.ResponseWriter, *http.Request)) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected provider path: %s", r.URL.Path)
		}
		if calls >= len(handlers) {
			t.Fatalf("unexpected extra provider call %d", calls+1)
		}
		handler := handlers[calls]
		calls++
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func writeOpenAIToolCallSSE(t *testing.T, w http.ResponseWriter, idValue, name, arguments string) {
	t.Helper()
	writeOpenAISSE(t, w, map[string]any{
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []map[string]any{{
					"index": 0,
					"id":    idValue,
					"type":  "function",
					"function": map[string]any{
						"name":      name,
						"arguments": arguments,
					},
				}},
			},
		}},
	})
	writeOpenAISSE(t, w, map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func writeOpenAITextSSE(t *testing.T, w http.ResponseWriter, text string) {
	t.Helper()
	writeOpenAISSE(t, w, map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": text}}},
	})
	writeOpenAISSE(t, w, map[string]any{
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func writeOpenAISSE(t *testing.T, w http.ResponseWriter, payload map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal SSE payload: %v", err)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func assertMergeCoreSideEffectsWithoutCommitRow(t *testing.T, database *sql.DB, repo, commitSHA, preMergeHead string) {
	t.Helper()
	var sourceStatus string
	var sourceWorktree, mergedInto, mergeCommit, mergeStrategy, preMerge sql.NullString
	if err := database.QueryRow(`SELECT status, worktree_path, merged_into_chapter_id, merge_commit_sha, merge_strategy, pre_merge_target_sha FROM chapters WHERE id = 'source'`).Scan(&sourceStatus, &sourceWorktree, &mergedInto, &mergeCommit, &mergeStrategy, &preMerge); err != nil {
		t.Fatalf("query source core merge fields: %v", err)
	}
	if sourceStatus != "merged" || sourceWorktree.Valid || !mergedInto.Valid || mergedInto.String != "target" || !mergeCommit.Valid || mergeCommit.String != commitSHA || !mergeStrategy.Valid || mergeStrategy.String != "merge" || !preMerge.Valid || preMerge.String != preMergeHead {
		t.Fatalf("source core merge fields = status:%s worktree:%v into:%v sha:%v strategy:%v pre:%v", sourceStatus, sourceWorktree, mergedInto, mergeCommit, mergeStrategy, preMerge)
	}
	var targetHead sql.NullString
	if err := database.QueryRow(`SELECT head_commit_sha FROM chapters WHERE id = 'target'`).Scan(&targetHead); err != nil {
		t.Fatalf("query target core merge metadata: %v", err)
	}
	if !targetHead.Valid || targetHead.String != commitSHA {
		t.Fatalf("target head = %v, want %s", targetHead, commitSHA)
	}
	if head := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD")); head != commitSHA {
		t.Fatalf("target git HEAD = %s, want merge commit %s", head, commitSHA)
	}
	var edgeCount, commitCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_edges WHERE source_id = 'source' AND target_id = 'target' AND type = 'merge'`).Scan(&edgeCount); err != nil {
		t.Fatalf("count merge edges: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'target' AND sha = ?`, commitSHA).Scan(&commitCount); err != nil {
		t.Fatalf("count merge commit rows: %v", err)
	}
	if edgeCount != 1 || commitCount != 0 {
		t.Fatalf("post-commit failure should leave one core edge and no commit row, edge=%d commitRows=%d", edgeCount, commitCount)
	}
}

func assertMergeSideEffects(t *testing.T, database *sql.DB, repo, strategy, commitSHA, preMergeHead string) {
	t.Helper()
	var sourceStatus string
	var sourceWorktree, mergedInto, mergeCommit, mergeStrategy, preMerge sql.NullString
	if err := database.QueryRow(`SELECT status, worktree_path, merged_into_chapter_id, merge_commit_sha, merge_strategy, pre_merge_target_sha FROM chapters WHERE id = 'source'`).Scan(&sourceStatus, &sourceWorktree, &mergedInto, &mergeCommit, &mergeStrategy, &preMerge); err != nil {
		t.Fatalf("query source merge fields: %v", err)
	}
	if sourceStatus != "merged" || sourceWorktree.Valid || !mergedInto.Valid || mergedInto.String != "target" || !mergeCommit.Valid || mergeCommit.String != commitSHA || !mergeStrategy.Valid || mergeStrategy.String != strategy || !preMerge.Valid || preMerge.String != preMergeHead {
		t.Fatalf("source merge fields = status:%s worktree:%v into:%v sha:%v strategy:%v pre:%v", sourceStatus, sourceWorktree, mergedInto, mergeCommit, mergeStrategy, preMerge)
	}
	var targetHead sql.NullString
	var targetCount int
	if err := database.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'target'`).Scan(&targetHead, &targetCount); err != nil {
		t.Fatalf("query target git metadata: %v", err)
	}
	expectedCount := gitCommitCountForTest(t, repo)
	if !targetHead.Valid || targetHead.String != commitSHA || targetCount != expectedCount {
		t.Fatalf("target metadata = head:%v count:%d, want %s/%d", targetHead, targetCount, commitSHA, expectedCount)
	}
	var edgeMetadata string
	if err := database.QueryRow(`SELECT metadata FROM chapter_edges WHERE source_id = 'source' AND target_id = 'target' AND type = 'merge'`).Scan(&edgeMetadata); err != nil {
		t.Fatalf("query merge edge: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(edgeMetadata), &metadata); err != nil {
		t.Fatalf("decode edge metadata %q: %v", edgeMetadata, err)
	}
	if metadata["mergeCommitSha"] != commitSHA || metadata["strategy"] != strategy || metadata["status"] != "completed" {
		t.Fatalf("edge metadata = %#v, want commit/strategy/status", metadata)
	}
	var commitMessage, commitSource string
	if err := database.QueryRow(`SELECT message, source FROM chapter_commits WHERE chapter_id = 'target' AND sha = ?`, commitSHA).Scan(&commitMessage, &commitSource); err != nil {
		t.Fatalf("query merge chapter commit: %v", err)
	}
	if strings.TrimSpace(commitMessage) == "" {
		t.Fatalf("commit message should be recorded")
	}
	if commitSource != "merge" {
		t.Fatalf("commit source = %q, want merge", commitSource)
	}
}

func assertMergeGraphSurface(t *testing.T, database *sql.DB, commitSHA, preMergeHead string) {
	t.Helper()
	router := chi.NewRouter()
	graphx.NewHandler(database).RegisterRoutes(router)
	req := httptest.NewRequest(http.MethodGet, "/project/graph", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("graph status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode graph response: %v", err)
	}
	if body["degraded"] != false {
		t.Fatalf("merge lifecycle graph should not be degraded: %#v", body["fallbacks"])
	}
	nodes := map[string]map[string]any{}
	for _, raw := range body["nodes"].([]any) {
		node := raw.(map[string]any)
		nodes[node["id"].(string)] = node
	}
	sourceData := nodes["source"]["data"].(map[string]any)
	if sourceData["status"] != "merged" || sourceData["worktreePath"] != nil {
		t.Fatalf("source graph node should show merged/null-worktree state: %#v", sourceData)
	}
	targetData := nodes["target"]["data"].(map[string]any)
	if targetData["role"] != "trunk" || targetData["headCommitSha"] != commitSHA {
		t.Fatalf("target graph node should expose merge head: %#v", targetData)
	}
	edges := map[string]map[string]any{}
	for _, raw := range body["edges"].([]any) {
		edge := raw.(map[string]any)
		edges[edge["source"].(string)+"->"+edge["target"].(string)+":"+edge["type"].(string)] = edge
	}
	mergeEdge := edges["source->target:merge"]
	if mergeEdge == nil {
		t.Fatalf("merge edge missing from graph: %#v", body["edges"])
	}
	metadata := mergeEdge["metadata"].(map[string]any)
	if metadata["mergeCommitSha"] != commitSHA || metadata["strategy"] != "merge" || metadata["status"] != "completed" {
		t.Fatalf("merge edge metadata mismatch: %#v", metadata)
	}
}

func assertMergeAbortedWithoutPartialState(t *testing.T, database *sql.DB, repo, preMergeHead string) {
	t.Helper()
	if head := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "HEAD")); head != preMergeHead {
		t.Fatalf("target HEAD after aborted merge = %s, want %s", head, preMergeHead)
	}
	status := strings.TrimSpace(runGitChapterTest(t, repo, "status", "--porcelain"))
	if status != "" {
		t.Fatalf("target worktree should be clean after aborted merge, status=%q", status)
	}
	var sourceStatus string
	var sourceWorktree, mergedInto, mergeCommit, mergeStrategy, preMerge sql.NullString
	if err := database.QueryRow(`SELECT status, worktree_path, merged_into_chapter_id, merge_commit_sha, merge_strategy, pre_merge_target_sha FROM chapters WHERE id = 'source'`).Scan(&sourceStatus, &sourceWorktree, &mergedInto, &mergeCommit, &mergeStrategy, &preMerge); err != nil {
		t.Fatalf("query source after aborted merge: %v", err)
	}
	if sourceStatus != "active" || !sourceWorktree.Valid || mergedInto.Valid || mergeCommit.Valid || mergeStrategy.Valid || preMerge.Valid {
		t.Fatalf("source after aborted merge = status:%s worktree:%v into:%v sha:%v strategy:%v pre:%v", sourceStatus, sourceWorktree, mergedInto, mergeCommit, mergeStrategy, preMerge)
	}
	for label, query := range map[string]string{
		"merge edges":     `SELECT COUNT(*) FROM chapter_edges WHERE type = 'merge'`,
		"merge commits":   `SELECT COUNT(*) FROM chapter_commits WHERE source IN ('merge', 'cherry_pick')`,
		"merge summaries": `SELECT COUNT(*) FROM narrator_messages WHERE content_json LIKE '%merge_summary%'`,
	} {
		var count int
		if err := database.QueryRow(query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", label, err)
		}
		if count != 0 {
			t.Fatalf("%s after aborted merge = %d, want 0", label, count)
		}
	}
}

func assertAIResolveTranscriptFallback(t *testing.T, database *sql.DB, commitSHA string) {
	t.Helper()
	rows, err := database.Query(`SELECT m.role, m.content_json, r.seq FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = 'narrator-target' AND (m.content_json GLOB '*"type":"ai_resolve_request"*' OR m.content_json GLOB '*"type":"ai_resolve_result"*') ORDER BY r.seq ASC`)
	if err != nil {
		t.Fatalf("query AI resolve transcript: %v", err)
	}
	defer rows.Close()
	type transcriptMessage struct {
		role string
		raw  string
		seq  int
	}
	messages := []transcriptMessage{}
	for rows.Next() {
		var msg transcriptMessage
		if err := rows.Scan(&msg.role, &msg.raw, &msg.seq); err != nil {
			t.Fatalf("scan AI resolve transcript: %v", err)
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate AI resolve transcript: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("AI resolve transcript messages = %d, want 2: %#v", len(messages), messages)
	}
	if messages[0].role != "user" || messages[1].role != "assistant" || messages[0].seq >= messages[1].seq {
		t.Fatalf("AI resolve transcript role/sequence mismatch: %#v", messages)
	}
	if !strings.Contains(messages[0].raw, "ai_resolve_request") || !strings.Contains(messages[0].raw, "one-shot-basic-fallback") || !strings.Contains(messages[0].raw, "chapter/source") {
		t.Fatalf("AI resolve request transcript missing metadata: %s", messages[0].raw)
	}
	if !strings.Contains(messages[1].raw, "ai_resolve_result") || !strings.Contains(messages[1].raw, commitSHA) || !strings.Contains(messages[1].raw, "one-shot basic") {
		t.Fatalf("AI resolve result transcript missing metadata: %s", messages[1].raw)
	}
	var messageCount, messageVersion int
	if err := database.QueryRow(`SELECT message_count, message_version FROM narrators WHERE id = 'narrator-target'`).Scan(&messageCount, &messageVersion); err != nil {
		t.Fatalf("query narrator counters after AI resolve transcript: %v", err)
	}
	if messageCount != 3 || messageVersion != 2 {
		t.Fatalf("narrator counters after AI resolve transcript = count %d version %d, want count 3 version 2", messageCount, messageVersion)
	}
}

func assertMergeSummaryLatest(t *testing.T, database *sql.DB, sourceID, commitSHA string, wantLatest bool) map[string]any {
	t.Helper()
	var messageID string
	if err := database.QueryRow(`SELECT id FROM narrator_messages WHERE content_json LIKE '%merge_summary%' AND content_json LIKE ? AND content_json LIKE ? ORDER BY created_at DESC LIMIT 1`, "%"+sourceID+"%", "%"+commitSHA+"%").Scan(&messageID); err != nil {
		t.Fatalf("query merge summary for %s/%s: %v", sourceID, commitSHA, err)
	}
	block := mergeSummaryBlockByMessage(t, database, messageID)
	if latest, _ := block["isLatest"].(bool); latest != wantLatest {
		t.Fatalf("merge summary latest = %v, want %v in %#v", latest, wantLatest, block)
	}
	if block["sourceChapterId"] != sourceID || block["commitSha"] != commitSHA {
		t.Fatalf("merge summary block = %#v, want source %s commit %s", block, sourceID, commitSHA)
	}
	return block
}

func mergeSummaryBlockByMessage(t *testing.T, database *sql.DB, messageID string) map[string]any {
	t.Helper()
	var raw string
	if err := database.QueryRow(`SELECT content_json FROM narrator_messages WHERE id = ?`, messageID).Scan(&raw); err != nil {
		t.Fatalf("query summary message %s: %v", messageID, err)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		t.Fatalf("decode summary blocks %s: %v", raw, err)
	}
	for _, block := range blocks {
		if block["type"] == "merge_summary" {
			return block
		}
	}
	t.Fatalf("merge_summary block not found in %s", raw)
	return nil
}

func containsAnyString(values []any, expected string) bool {
	for _, value := range values {
		if text, ok := value.(string); ok && text == expected {
			return true
		}
	}
	return false
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsEvent(events []map[string]any, eventType, field, value string) bool {
	for _, event := range events {
		if event["type"] == eventType && event[field] == value {
			return true
		}
	}
	return false
}

func intFromJSONNumber(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	default:
		return 0
	}
}

func gitCommitCountForTest(t *testing.T, repo string) int {
	t.Helper()
	var count int
	if _, err := fmt.Sscanf(strings.TrimSpace(runGitChapterTest(t, repo, "rev-list", "--count", "HEAD")), "%d", &count); err != nil {
		t.Fatalf("parse git commit count: %v", err)
	}
	return count
}

func openMergeParityDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT)`,
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, created_at TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT, merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT, container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT, start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER, anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER, panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT, last_accessed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT, type TEXT, metadata TEXT, created_at TEXT)`,
		`CREATE TABLE chapter_commits (id TEXT PRIMARY KEY, chapter_id TEXT, sha TEXT, message TEXT, full_message TEXT, author_name TEXT, author_email TEXT, authored_at TEXT, source TEXT, narrator_id TEXT, narrator_message_id TEXT, files_changed INTEGER, lines_added INTEGER, lines_removed INTEGER, created_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, variant TEXT, model TEXT, status TEXT, substatus TEXT, message_count INTEGER DEFAULT 0, message_version INTEGER DEFAULT 0, last_message_at TEXT, updated_at TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, role TEXT, content_json TEXT, content_text TEXT, created_by TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, seq INTEGER, is_compact INTEGER)`,
		`CREATE TABLE api_requests (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, kind TEXT DEFAULT 'narrator' NOT NULL, provider TEXT, credential_id TEXT, model TEXT, input_tokens INTEGER DEFAULT 0, output_tokens INTEGER DEFAULT 0, cached_input_tokens INTEGER DEFAULT 0, cache_creation_input_tokens INTEGER DEFAULT 0, cache_creation_5m_tokens INTEGER DEFAULT 0, cache_creation_1h_tokens INTEGER DEFAULT 0, reasoning_tokens INTEGER DEFAULT 0, ttft_ms INTEGER, duration_ms INTEGER, cost_usd REAL, context_percent REAL, meter_usage REAL, meter_unit TEXT, error_message TEXT, raw_dump_json TEXT, created_at TEXT NOT NULL)`,
		`CREATE TABLE review_conclusions (id TEXT PRIMARY KEY, review_chapter_id TEXT, source_chapter_id TEXT, verdict TEXT, findings_json TEXT, created_at TEXT)`,
		`CREATE TABLE merge_sessions (id TEXT PRIMARY KEY, target_chapter_id TEXT, source_chapter_ids TEXT, strategy TEXT, status TEXT, current_index INTEGER, merged_count INTEGER, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT, name TEXT, status TEXT, graph_opened INTEGER, graph_x REAL, graph_y REAL, graph_width REAL, graph_height REAL, created_at TEXT)`,
		`CREATE TABLE container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, container_id TEXT, service_name TEXT, status TEXT, host_port INTEGER, container_port INTEGER, proxy_label TEXT, container_ip TEXT, volume_name TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE exploration_groups (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, base_chapter_id TEXT, status TEXT, decided_chapter_id TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE terminal_tabs (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE terminal_view_state (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, language TEXT, recent_tabs TEXT, updated_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("schema: %v\n%s", err, stmt)
		}
	}
	return database
}

func insertMergeParityRows(t *testing.T, database *sql.DB, repo, sourceWT, sourceHead string, requireReview bool) {
	t.Helper()
	settings := `{}`
	if requireReview {
		settings = `{"requireReviewBeforeMerge":true}`
	}
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, chapter_settings) VALUES ('project', 'Project', ?, 'main', ?)`, repo, settings); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	mainHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "main"))
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, head_commit_sha, start_commit_sha, commit_count, is_root, created_at, updated_at)
		VALUES ('target', 'project', 'Target', 'active', 'trunk', 'main', ?, 'main', ?, ?, 1, 1, 'now', 'now')`, repo, mainHead, mainHead); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, parent_chapter_id, fork_point, head_commit_sha, start_commit_sha, commit_count, is_root, created_at, updated_at)
		VALUES ('source', 'project', 'Source', 'active', 'branch', 'chapter/source', ?, 'main', 'target', ?, ?, ?, 2, 0, 'now', 'now')`, sourceWT, `{"commitSha":"`+sourceHead+`"}`, sourceHead, sourceHead); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, model, created_at, updated_at) VALUES ('narrator-target', 'target', 'primary', 'test-model', 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
}

type mergeRecordingTerminalKiller struct {
	killed []string
}

func (r *mergeRecordingTerminalKiller) Kill(_ context.Context, terminalID string) error {
	r.killed = append(r.killed, terminalID)
	return nil
}

func runGitChapterTest(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}

func writeChapterTestFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
