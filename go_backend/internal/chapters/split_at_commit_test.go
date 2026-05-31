package chapters

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gitx "narrafork-go/internal/git"

	_ "modernc.org/sqlite"
)

func TestSplitAtCommitCreatesPrefixContinuationNewForkAndTruncatedNarrators(t *testing.T) {
	ctx := context.Background()
	repo, baseSHA, splitSHA, headSHA := newSplitRepo(t)
	sqlDB := newForkParityDB(t)
	insertSplitFixture(t, sqlDB, repo, baseSHA, splitSHA, headSHA)
	service := &Service{DB: sqlDB, Git: gitx.NewService()}

	result, err := service.SplitAtCommit(ctx, "source", SplitInput{
		CommitSHA: splitSHA,
		NewFork:   SplitNewForkInput{Title: "Alternative path", InheritMode: "full"},
	})
	if err != nil {
		t.Fatalf("SplitAtCommit: %v", err)
	}

	if result.CommitSHA != splitSHA {
		t.Fatalf("split commit = %s, want %s", result.CommitSHA, splitSHA)
	}
	if result.PrefixChapter.ID == "source" || result.PrefixChapter.ID == result.NewForkChapter.ID {
		t.Fatalf("unexpected split chapter ids: %#v", result)
	}
	if result.PrefixChapter.ParentChapterID == nil || *result.PrefixChapter.ParentChapterID != "base" {
		t.Fatalf("prefix parent = %#v, want base", result.PrefixChapter.ParentChapterID)
	}
	if result.PrefixChapter.IsRoot {
		t.Fatalf("prefix must not become root")
	}
	if result.PrefixChapter.Status != "frozen" {
		t.Fatalf("branch split prefix status = %s, want frozen", result.PrefixChapter.Status)
	}
	if result.PrefixChapter.StartCommitSHA == nil || *result.PrefixChapter.StartCommitSHA != splitSHA {
		t.Fatalf("prefix start = %#v, want split sha", result.PrefixChapter.StartCommitSHA)
	}
	if result.PrefixChapter.HeadCommitSHA == nil || *result.PrefixChapter.HeadCommitSHA != splitSHA {
		t.Fatalf("prefix head = %#v, want split sha", result.PrefixChapter.HeadCommitSHA)
	}
	if result.PrefixChapter.WorktreePath == nil || !pathExists(*result.PrefixChapter.WorktreePath) {
		t.Fatalf("prefix worktree missing: %#v", result.PrefixChapter.WorktreePath)
	}

	continuation := result.ContinuationChapter
	if continuation.ID != "source" {
		t.Fatalf("continuation id = %s, want source", continuation.ID)
	}
	if continuation.ParentChapterID == nil || *continuation.ParentChapterID != result.PrefixChapter.ID {
		t.Fatalf("continuation parent = %#v, want prefix", continuation.ParentChapterID)
	}
	if continuation.Branch != "master" || continuation.WorktreePath == nil || *continuation.WorktreePath != repo {
		t.Fatalf("continuation branch/worktree changed: branch=%s worktree=%#v", continuation.Branch, continuation.WorktreePath)
	}
	if continuation.StartCommitSHA == nil || *continuation.StartCommitSHA != splitSHA {
		t.Fatalf("continuation start = %#v, want split sha", continuation.StartCommitSHA)
	}
	if continuation.HeadCommitSHA == nil || *continuation.HeadCommitSHA != headSHA {
		t.Fatalf("continuation head = %#v, want original head", continuation.HeadCommitSHA)
	}

	if result.NewForkChapter.ParentChapterID == nil || *result.NewForkChapter.ParentChapterID != result.PrefixChapter.ID {
		t.Fatalf("new fork parent = %#v, want prefix", result.NewForkChapter.ParentChapterID)
	}
	if result.NewForkChapter.Status != "active" || result.NewForkChapter.StartCommitSHA == nil || *result.NewForkChapter.StartCommitSHA != splitSHA {
		t.Fatalf("new fork chapter unexpected: %#v", result.NewForkChapter)
	}
	if result.NewForkChapter.WorktreePath == nil || !pathExists(*result.NewForkChapter.WorktreePath) {
		t.Fatalf("new fork worktree missing: %#v", result.NewForkChapter.WorktreePath)
	}

	if got := parentChapterID(t, sqlDB, "child_prefix"); got != result.PrefixChapter.ID {
		t.Fatalf("child_prefix parent = %s, want prefix %s", got, result.PrefixChapter.ID)
	}
	if got := edgeCount(t, sqlDB, result.PrefixChapter.ID, "child_prefix", "fork"); got != 1 {
		t.Fatalf("prefix->child_prefix edge count = %d, want 1", got)
	}
	if got := edgeCount(t, sqlDB, "source", "child_prefix", "fork"); got != 0 {
		t.Fatalf("source->child_prefix edge count = %d, want 0", got)
	}
	if got := parentChapterID(t, sqlDB, "child_continuation"); got != "source" {
		t.Fatalf("child_continuation parent = %s, want source", got)
	}
	if got := edgeCount(t, sqlDB, "source", "child_continuation", "fork"); got != 1 {
		t.Fatalf("source->child_continuation edge count = %d, want 1", got)
	}

	oldTarget := ""
	if err := sqlDB.QueryRow(`SELECT target_id FROM chapter_edges WHERE id = 'edge_old'`).Scan(&oldTarget); err != nil {
		t.Fatalf("query old edge: %v", err)
	}
	if oldTarget != result.PrefixChapter.ID {
		t.Fatalf("incoming fork edge target = %s, want prefix %s", oldTarget, result.PrefixChapter.ID)
	}
	if got := edgeCount(t, sqlDB, result.PrefixChapter.ID, "source", "fork"); got != 1 {
		t.Fatalf("prefix->continuation edge count = %d, want 1", got)
	}
	if got := edgeCount(t, sqlDB, result.PrefixChapter.ID, result.NewForkChapter.ID, "fork"); got != 1 {
		t.Fatalf("prefix->new fork edge count = %d, want 1", got)
	}

	prefixNarrator := primaryNarratorID(t, sqlDB, result.PrefixChapter.ID)
	newForkNarrator := primaryNarratorID(t, sqlDB, result.NewForkChapter.ID)
	if got := refMessageIDs(t, sqlDB, prefixNarrator); strings.Join(got, ",") != "m_before,m_equal" {
		t.Fatalf("prefix refs = %#v, want before/equal cutoff", got)
	}
	if got := refMessageIDs(t, sqlDB, newForkNarrator); strings.Join(got, ",") != "m_before,m_equal" {
		t.Fatalf("new fork refs = %#v, want inherited prefix refs", got)
	}
	if got := refMessageIDs(t, sqlDB, "n_source"); strings.Join(got, ",") != "m_before,m_equal,m_after" {
		t.Fatalf("continuation refs = %#v, want original full refs", got)
	}
	if len(result.Warnings) != 0 || len(result.Fallbacks) != 0 {
		t.Fatalf("exact commit/message cutoff should not warn, warnings=%#v fallbacks=%#v", result.Warnings, result.Fallbacks)
	}
}

func TestSplitAtCommitWarnsAndUsesEmptyContextWhenNoCommitMessageAssociation(t *testing.T) {
	ctx := context.Background()
	repo, baseSHA, splitSHA, headSHA := newSplitRepo(t)
	sqlDB := newForkParityDB(t)
	insertSplitFixture(t, sqlDB, repo, baseSHA, splitSHA, headSHA)
	if _, err := sqlDB.Exec(`UPDATE narrator_messages SET commit_sha = NULL WHERE narrator_id = 'n_source'`); err != nil {
		t.Fatalf("clear message commit associations: %v", err)
	}
	service := &Service{DB: sqlDB, Git: gitx.NewService()}

	result, err := service.SplitAtCommit(ctx, "source", SplitInput{
		CommitSHA: splitSHA,
		NewFork:   SplitNewForkInput{Title: "Fallback path", InheritMode: "full"},
	})
	if err != nil {
		t.Fatalf("SplitAtCommit: %v", err)
	}
	if !containsWarning(result.Warnings, "No exact commit/message association") {
		t.Fatalf("expected explicit cutoff warning, got %#v", result.Warnings)
	}
	if !containsFallback(result.Fallbacks, "splitContextCutoff") {
		t.Fatalf("expected splitContextCutoff fallback, got %#v", result.Fallbacks)
	}
	prefixNarrator := primaryNarratorID(t, sqlDB, result.PrefixChapter.ID)
	newForkNarrator := primaryNarratorID(t, sqlDB, result.NewForkChapter.ID)
	if got := refMessageIDs(t, sqlDB, prefixNarrator); len(got) != 0 {
		t.Fatalf("fallback prefix refs = %#v, want empty safe cutoff", got)
	}
	if got := refMessageIDs(t, sqlDB, newForkNarrator); len(got) != 0 {
		t.Fatalf("fallback new fork refs = %#v, want empty safe cutoff", got)
	}
}

func TestSplitAtCommitCompressedInheritReportsSafeSummaryFallback(t *testing.T) {
	ctx := context.Background()
	repo, baseSHA, splitSHA, headSHA := newSplitRepo(t)
	sqlDB := newForkParityDB(t)
	insertSplitFixture(t, sqlDB, repo, baseSHA, splitSHA, headSHA)
	service := &Service{DB: sqlDB, Git: gitx.NewService()}

	result, err := service.SplitAtCommit(ctx, "source", SplitInput{
		CommitSHA: splitSHA,
		NewFork:   SplitNewForkInput{Title: "Compressed route", InheritMode: "compressed"},
	})
	if err != nil {
		t.Fatalf("SplitAtCommit: %v", err)
	}

	fallback, ok := fallbackByFeature(result.Fallbacks, "compressedContext")
	if !ok {
		t.Fatalf("expected compressedContext fallback, got %#v", result.Fallbacks)
	}
	if !fallback.Fallback || fallback.Supported || !strings.Contains(fallback.Message, "AI context summary") {
		t.Fatalf("compressed fallback must be explicit and unsupported, got %#v", fallback)
	}
	if !containsWarning(result.Warnings, "does not generate an AI context summary") {
		t.Fatalf("expected compressed summary warning, got %#v", result.Warnings)
	}
	if !containsFallback(result.NewForkChapter.Fallbacks, "compressedContext") {
		t.Fatalf("new fork chapter response lost compressed fallback: %#v", result.NewForkChapter.Fallbacks)
	}
	if !containsWarning(result.PrefixChapter.Warnings, "does not generate an AI context summary") {
		t.Fatalf("prefix chapter response lost compressed warning: %#v", result.PrefixChapter.Warnings)
	}

	prefixNarrator := primaryNarratorID(t, sqlDB, result.PrefixChapter.ID)
	newForkNarrator := primaryNarratorID(t, sqlDB, result.NewForkChapter.ID)
	if got := refMessageIDs(t, sqlDB, prefixNarrator); strings.Join(got, ",") != "m_before,m_equal" {
		t.Fatalf("compressed prefix refs = %#v, want cutoff refs", got)
	}
	if got := refMessageIDs(t, sqlDB, newForkNarrator); strings.Join(got, ",") != "m_before,m_equal" {
		t.Fatalf("compressed new fork refs = %#v, want bounded prefix refs", got)
	}

	var inheritMode string
	var parentNarratorID, forkMessageID, contextSummary sql.NullString
	var messageCount int
	row := sqlDB.QueryRow(
		`SELECT inherit_mode, parent_narrator_id, fork_message_id, context_summary, message_count FROM narrators WHERE id = ?`,
		newForkNarrator,
	)
	if err := row.Scan(&inheritMode, &parentNarratorID, &forkMessageID, &contextSummary, &messageCount); err != nil {
		t.Fatalf("query compressed new fork narrator: %v", err)
	}
	if inheritMode != "compressed" {
		t.Fatalf("new fork inherit_mode = %s, want compressed", inheritMode)
	}
	if !parentNarratorID.Valid || parentNarratorID.String != prefixNarrator {
		t.Fatalf("new fork parent narrator = %#v, want prefix narrator %s", parentNarratorID, prefixNarrator)
	}
	if !forkMessageID.Valid || forkMessageID.String != "m_equal" {
		t.Fatalf("new fork fork_message_id = %#v, want split cutoff message", forkMessageID)
	}
	if messageCount != 2 {
		t.Fatalf("compressed new fork message_count = %d, want 2", messageCount)
	}
	hasSummaryCount := contextSummary.Valid && strings.Contains(contextSummary.String, "copied 2 prefix message reference(s)")
	hasSummaryReason := contextSummary.Valid && strings.Contains(contextSummary.String, "instead of generating an AI context summary")
	if !hasSummaryCount || !hasSummaryReason {
		t.Fatalf("compressed fallback summary missing quality markers: %#v", contextSummary)
	}

	forkPoint := chapterForkPoint(t, sqlDB, result.NewForkChapter.ID)
	if !metadataContainsFallback(forkPoint, "compressedContext") {
		t.Fatalf("new fork fork_point lost compressed fallback metadata: %s", forkPoint)
	}
	edgeMetadata := forkEdgeMetadata(t, sqlDB, result.PrefixChapter.ID, result.NewForkChapter.ID)
	if !metadataContainsFallback(edgeMetadata, "compressedContext") {
		t.Fatalf("new fork edge lost compressed fallback metadata: %s", edgeMetadata)
	}
}

func TestSplitAtCommitAutoStartsNewForkContainers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman script uses POSIX sh")
	}
	repo, baseSHA, splitSHA, headSHA := newSplitRepo(t)
	sqlDB := newForkParityDB(t)
	insertSplitFixture(t, sqlDB, repo, baseSHA, splitSHA, headSHA)
	if _, err := sqlDB.Exec(`UPDATE chapters SET container_config = ? WHERE id = 'source'`, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`); err != nil {
		t.Fatalf("set source container config: %v", err)
	}
	prependFakePodman(t, fakePodmanScript())
	recorder := &chapterBroadcastRecorder{}
	service := &Service{DB: sqlDB, Git: gitx.NewService(), BroadcastMergeProgress: recorder.BroadcastMergeProgress}
	result, err := service.SplitAtCommit(context.Background(), "source", SplitInput{
		CommitSHA: splitSHA,
		NewFork:   SplitNewForkInput{Title: "Container split fork", InheritMode: "full"},
	})
	if err != nil {
		t.Fatalf("SplitAtCommit: %v", err)
	}
	eventually(t, func() bool { return recorder.hasGlobalEvent("container:started") }, "split new fork auto-start container:started")
	eventually(t, func() bool {
		var count int
		return sqlDB.QueryRow(`SELECT COUNT(*) FROM container_instances WHERE chapter_id = ?`, result.NewForkChapter.ID).Scan(&count) == nil && count > 0
	}, "split new fork auto-start persisted container instance")
	var prefixCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM container_instances WHERE chapter_id = ?`, result.PrefixChapter.ID).Scan(&prefixCount); err != nil {
		t.Fatalf("query prefix container count: %v", err)
	}
	if prefixCount != 0 {
		t.Fatalf("prefix chapter should not be auto-started, count=%d", prefixCount)
	}
}

func TestSplitAtCommitRollsBackDBAndGitArtifactsOnTransactionFailure(t *testing.T) {
	repo, baseSHA, splitSHA, headSHA := newSplitRepo(t)
	sqlDB := newForkParityDB(t)
	insertSplitFixture(t, sqlDB, repo, baseSHA, splitSHA, headSHA)
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_split_new_fork BEFORE INSERT ON chapters WHEN NEW.title = 'Broken Fork' BEGIN SELECT RAISE(ABORT, 'split new fork blocked'); END;`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	service := &Service{DB: sqlDB, Git: gitx.NewService()}

	_, err := service.SplitAtCommit(context.Background(), "source", SplitInput{
		CommitSHA: splitSHA,
		NewFork:   SplitNewForkInput{Title: "Broken Fork", InheritMode: "full"},
	})
	if err == nil || !strings.Contains(err.Error(), "split new fork blocked") {
		t.Fatalf("expected split transaction failure, got %v", err)
	}
	var chapterCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM chapters`).Scan(&chapterCount); err != nil {
		t.Fatalf("count chapters: %v", err)
	}
	if chapterCount != 4 {
		t.Fatalf("failed split should leave original fixture chapters only, count=%d", chapterCount)
	}
	var sourceParent, sourceStart, edgeTarget string
	if err := sqlDB.QueryRow(`SELECT parent_chapter_id, start_commit_sha FROM chapters WHERE id = 'source'`).Scan(&sourceParent, &sourceStart); err != nil {
		t.Fatalf("query source after rollback: %v", err)
	}
	if sourceParent != "base" || sourceStart != baseSHA {
		t.Fatalf("source after rollback parent/start = %s/%s, want base/%s", sourceParent, sourceStart, baseSHA)
	}
	if err := sqlDB.QueryRow(`SELECT target_id FROM chapter_edges WHERE id = 'edge_old'`).Scan(&edgeTarget); err != nil {
		t.Fatalf("query edge after rollback: %v", err)
	}
	if edgeTarget != "source" {
		t.Fatalf("old edge target after rollback = %s, want source", edgeTarget)
	}
	if got := parentChapterID(t, sqlDB, "child_prefix"); got != "source" {
		t.Fatalf("child_prefix parent after rollback = %s, want source", got)
	}
	if got := edgeCount(t, sqlDB, "source", "child_prefix", "fork"); got != 1 {
		t.Fatalf("source->child_prefix edge count after rollback = %d, want 1", got)
	}
	if branches := gitOutput(t, repo, "branch", "--list", "chapter/*"); strings.TrimSpace(branches) != "" {
		t.Fatalf("split branches leaked after rollback: %s", branches)
	}
	matches, err := filepath.Glob(filepath.Join(repo, ".worktrees", "*"))
	if err != nil {
		t.Fatalf("glob worktrees: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("split worktrees leaked after rollback: %#v", matches)
	}
}

func TestSplitRouteReturnsFullSplitResult(t *testing.T) {
	repo, baseSHA, splitSHA, headSHA := newSplitRepo(t)
	sqlDB := newForkParityDB(t)
	insertSplitFixture(t, sqlDB, repo, baseSHA, splitSHA, headSHA)
	handler := NewHandler(&Service{DB: sqlDB, Git: gitx.NewService()})
	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"commitSha":"` + splitSHA + `","newFork":{"title":"Route fork","inheritMode":"fresh"}}`)
	req := httptest.NewRequest(http.MethodPost, "/source/split", body)
	newChapterRouter(handler).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("split route status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var result SplitResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode split result: %v", err)
	}
	if result.PrefixChapter.ID == "" || result.ContinuationChapter.ID != "source" || result.NewForkChapter.ID == "" {
		t.Fatalf("route did not return full split result: %#v", result)
	}
	if result.CommitSHA != splitSHA {
		t.Fatalf("route commitSha = %s, want %s", result.CommitSHA, splitSHA)
	}
	prefix := result.PrefixChapter
	prefixStartsAtSplit := prefix.StartCommitSHA != nil && *prefix.StartCommitSHA == splitSHA
	prefixHeadsAtSplit := prefix.HeadCommitSHA != nil && *prefix.HeadCommitSHA == splitSHA
	if prefix.Status != "frozen" || !prefixStartsAtSplit || !prefixHeadsAtSplit {
		t.Fatalf("route prefix chapter does not describe the historical prefix: %#v", prefix)
	}
	continuation := result.ContinuationChapter
	continuationParent := continuation.ParentChapterID != nil && *continuation.ParentChapterID == prefix.ID
	continuationStartsAtSplit := continuation.StartCommitSHA != nil && *continuation.StartCommitSHA == splitSHA
	continuationKeepsHead := continuation.HeadCommitSHA != nil && *continuation.HeadCommitSHA == headSHA
	if !continuationParent || !continuationStartsAtSplit || !continuationKeepsHead {
		t.Fatalf("route continuation chapter does not describe the post-split continuation: %#v", continuation)
	}
	newFork := result.NewForkChapter
	newForkParent := newFork.ParentChapterID != nil && *newFork.ParentChapterID == prefix.ID
	newForkStartsAtSplit := newFork.StartCommitSHA != nil && *newFork.StartCommitSHA == splitSHA
	newForkHeadsAtSplit := newFork.HeadCommitSHA != nil && *newFork.HeadCommitSHA == splitSHA
	if !newForkParent || !newForkStartsAtSplit || !newForkHeadsAtSplit {
		t.Fatalf("route new fork chapter does not start from the split commit: %#v", newFork)
	}
	if result.NewForkChapter.WorktreePath == nil || !pathExists(*result.NewForkChapter.WorktreePath) {
		t.Fatalf("route new fork worktree missing: %#v", result.NewForkChapter.WorktreePath)
	}
	if len(result.Warnings) != 0 || len(result.Fallbacks) != 0 {
		t.Fatalf("fresh route split should be warning-free, warnings=%#v fallbacks=%#v", result.Warnings, result.Fallbacks)
	}
	if got := refMessageIDs(t, sqlDB, primaryNarratorID(t, sqlDB, result.NewForkChapter.ID)); len(got) != 0 {
		t.Fatalf("fresh route split should not copy new fork refs, got %#v", got)
	}
}

func newSplitRepo(t *testing.T) (repo, baseSHA, splitSHA, headSHA string) {
	t.Helper()
	repo = t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "story.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	runGit(t, repo, "add", "story.txt", "compose.yml")
	runGit(t, repo, "commit", "--date", "2026-01-01T00:00:00Z", "-m", "base")
	baseSHA = gitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "story.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write split file: %v", err)
	}
	runGit(t, repo, "add", "story.txt")
	runGit(t, repo, "commit", "--date", "2026-01-02T00:00:00Z", "-m", "split point")
	splitSHA = gitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "story.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write head file: %v", err)
	}
	runGit(t, repo, "add", "story.txt")
	runGit(t, repo, "commit", "--date", "2026-01-03T00:00:00Z", "-m", "head")
	headSHA = gitOutput(t, repo, "rev-parse", "HEAD")
	return repo, baseSHA, splitSHA, headSHA
}

func insertSplitFixture(t *testing.T, sqlDB *sql.DB, repo, baseSHA, splitSHA, headSHA string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, 'master', 'now', 'now')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, is_root, head_commit_sha, start_commit_sha, commit_count, axis_offset, cross_offset, created_at, updated_at) VALUES ('base', 'p1', 'Base', 'active', 'trunk', 'master', 'master', 1, ?, ?, 1, 0, 0, 'now', 'now')`, baseSHA, baseSHA); err != nil {
		t.Fatalf("insert base chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, parent_chapter_id, is_root, head_commit_sha, start_commit_sha, commit_count, axis_offset, cross_offset, created_at, updated_at) VALUES ('source', 'p1', 'Source', 'active', 'branch', 'master', ?, 'master', 'base', 0, ?, ?, 3, 10, 20, 'now', 'now')`, repo, headSHA, baseSHA); err != nil {
		t.Fatalf("insert source chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('edge_old', 'p1', 'base', 'source', 'fork', '{}', 'now')`); err != nil {
		t.Fatalf("insert old edge: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, parent_chapter_id, fork_point, is_root, head_commit_sha, start_commit_sha, commit_count, axis_offset, cross_offset, created_at, updated_at) VALUES ('child_prefix', 'p1', 'Child Prefix', 'active', 'branch', 'child/prefix', 'master', 'source', ?, 0, ?, ?, 2, 30, 40, 'now', 'now')`, `{"commitSha":"`+splitSHA+`"}`, splitSHA, splitSHA); err != nil {
		t.Fatalf("insert prefix child chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, base_branch, parent_chapter_id, fork_point, is_root, head_commit_sha, start_commit_sha, commit_count, axis_offset, cross_offset, created_at, updated_at) VALUES ('child_continuation', 'p1', 'Child Continuation', 'active', 'branch', 'child/continuation', 'master', 'source', ?, 0, ?, ?, 3, 50, 60, 'now', 'now')`, `{"commitSha":"`+headSHA+`"}`, headSHA, headSHA); err != nil {
		t.Fatalf("insert continuation child chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('edge_child_prefix', 'p1', 'source', 'child_prefix', 'fork', ?, 'now')`, `{"commitSha":"`+splitSHA+`"}`); err != nil {
		t.Fatalf("insert prefix child edge: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('edge_child_continuation', 'p1', 'source', 'child_continuation', 'fork', ?, 'now')`, `{"commitSha":"`+headSHA+`"}`); err != nil {
		t.Fatalf("insert continuation child edge: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, chapter_id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode, cwd, prune_enabled, variant, traits, is_background, is_ask_in_passing, message_version, created_at, updated_at) VALUES ('n_source', 'source', 'primary', 'Source narrator', 'fresh', 'test-model', 'default', 'idle', '[]', 0, ?, 1, 'primary', '[]', 0, 0, 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, repo); err != nil {
		t.Fatalf("insert source narrator: %v", err)
	}
	messages := []struct {
		id        string
		text      string
		commitSHA string
		createdAt string
		seq       int
	}{
		{id: "m_before", text: "before split", commitSHA: baseSHA, createdAt: "2030-01-01T00:00:00Z", seq: 1},
		{id: "m_equal", text: "at split", commitSHA: splitSHA, createdAt: "2030-01-02T00:00:00Z", seq: 2},
		{id: "m_after", text: "after split", commitSHA: headSHA, createdAt: "2026-01-01T00:00:00Z", seq: 3},
	}
	for _, msg := range messages {
		if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, commit_sha, created_at) VALUES (?, 'n_source', 'assistant', '[]', ?, ?, ?)`, msg.id, msg.text, msg.commitSHA, msg.createdAt); err != nil {
			t.Fatalf("insert message %s: %v", msg.id, err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, 'n_source', ?, ?, 0)`, "ref_"+msg.id, msg.id, msg.seq); err != nil {
			t.Fatalf("insert ref %s: %v", msg.id, err)
		}
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func primaryNarratorID(t *testing.T, sqlDB *sql.DB, chapterID string) string {
	t.Helper()
	var narratorID string
	if err := sqlDB.QueryRow(`SELECT id FROM narrators WHERE chapter_id = ? AND variant = 'primary' ORDER BY created_at ASC LIMIT 1`, chapterID).Scan(&narratorID); err != nil {
		t.Fatalf("primary narrator for %s: %v", chapterID, err)
	}
	return narratorID
}

func refMessageIDs(t *testing.T, sqlDB *sql.DB, narratorID string) []string {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT message_id FROM narrator_message_refs WHERE narrator_id = ? ORDER BY seq ASC`, narratorID)
	if err != nil {
		t.Fatalf("query refs for %s: %v", narratorID, err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan ref: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("refs rows: %v", err)
	}
	return ids
}

func edgeCount(t *testing.T, sqlDB *sql.DB, sourceID, targetID, edgeType string) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM chapter_edges WHERE source_id = ? AND target_id = ? AND type = ?`, sourceID, targetID, edgeType).Scan(&count); err != nil {
		t.Fatalf("count edge %s->%s: %v", sourceID, targetID, err)
	}
	return count
}

func parentChapterID(t *testing.T, sqlDB *sql.DB, chapterID string) string {
	t.Helper()
	var parent sql.NullString
	if err := sqlDB.QueryRow(`SELECT parent_chapter_id FROM chapters WHERE id = ?`, chapterID).Scan(&parent); err != nil {
		t.Fatalf("parent for %s: %v", chapterID, err)
	}
	if !parent.Valid {
		return ""
	}
	return parent.String
}

func containsWarning(warnings []string, needle string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, needle) {
			return true
		}
	}
	return false
}

func containsFallback(fallbacks []ForkFallback, feature string) bool {
	_, ok := fallbackByFeature(fallbacks, feature)
	return ok
}

func fallbackByFeature(fallbacks []ForkFallback, feature string) (ForkFallback, bool) {
	for _, fallback := range fallbacks {
		if fallback.Feature == feature && fallback.Fallback {
			return fallback, true
		}
	}
	return ForkFallback{}, false
}

func chapterForkPoint(t *testing.T, sqlDB *sql.DB, chapterID string) string {
	t.Helper()
	var forkPoint sql.NullString
	row := sqlDB.QueryRow(`SELECT fork_point FROM chapters WHERE id = ?`, chapterID)
	if err := row.Scan(&forkPoint); err != nil {
		t.Fatalf("fork_point for %s: %v", chapterID, err)
	}
	if !forkPoint.Valid {
		return ""
	}
	return forkPoint.String
}

func forkEdgeMetadata(t *testing.T, sqlDB *sql.DB, sourceID, targetID string) string {
	t.Helper()
	var metadata sql.NullString
	row := sqlDB.QueryRow(
		`SELECT metadata FROM chapter_edges WHERE source_id = ? AND target_id = ? AND type = 'fork' LIMIT 1`,
		sourceID,
		targetID,
	)
	if err := row.Scan(&metadata); err != nil {
		t.Fatalf("fork edge metadata %s->%s: %v", sourceID, targetID, err)
	}
	if !metadata.Valid {
		return ""
	}
	return metadata.String
}

func metadataContainsFallback(raw string, feature string) bool {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return false
	}
	fallbacks, ok := decoded["fallbacks"].([]any)
	if !ok {
		return false
	}
	for _, fallback := range fallbacks {
		entry, ok := fallback.(map[string]any)
		if !ok {
			continue
		}
		featureValue, _ := entry["feature"].(string)
		fallbackValue, _ := entry["fallback"].(bool)
		if featureValue == feature && fallbackValue {
			return true
		}
	}
	return false
}
