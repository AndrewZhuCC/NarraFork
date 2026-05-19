package chapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type SplitNewForkInput struct {
	Title       string
	Description *string
	InheritMode string
}

type SplitInput struct {
	CommitSHA       string
	NewFork         SplitNewForkInput
	AnchorCommitSHA *string
	AxisOffset      *float64
	CrossOffset     *float64
	Locale          string
}

type SplitResult struct {
	PrefixChapter       Chapter        `json:"prefixChapter"`
	ContinuationChapter Chapter        `json:"continuationChapter"`
	NewForkChapter      Chapter        `json:"newForkChapter"`
	CommitSHA           string         `json:"commitSha"`
	Warnings            []string       `json:"warnings,omitempty"`
	Fallbacks           []ForkFallback `json:"fallbacks,omitempty"`
}

type splitGitArtifact struct {
	BranchName   string
	WorktreePath string
}

type splitMessageRef struct {
	MessageID string
	IsCompact int
	CreatedAt string
	Seq       int
}

type splitContextCutoff struct {
	Seq       int
	MessageID string
	CommitSHA string
	Strategy  string
	Exact     bool
}

type splitCommitMessageRef struct {
	Seq       int
	MessageID string
	CommitSHA string
	Source    string
}

type splitNarratorSeed struct {
	ID                                string
	Title                             sql.NullString
	Model                             sql.NullString
	SystemPrompt                      sql.NullString
	PermissionMode                    sql.NullString
	ReasoningEffort                   sql.NullString
	FastMode                          int
	RelaxedPlan                       int
	PlanReflectionAutoApproveOverride string
	DangerReflectionOverride          string
}

func (s *Service) SplitAtCommit(ctx context.Context, chapterID string, input SplitInput) (SplitResult, error) {
	if s == nil || s.DB == nil || s.Git == nil {
		return SplitResult{}, httpx.BadRequest("Chapter split requires database and git services")
	}
	source, err := s.Get(ctx, chapterID)
	if err != nil {
		return SplitResult{}, err
	}
	if source.Status == "merged" || source.Status == "abandoned" {
		return SplitResult{}, httpx.BadRequest("Cannot split merged or abandoned chapters")
	}
	newForkTitle := strings.TrimSpace(input.NewFork.Title)
	if newForkTitle == "" {
		return SplitResult{}, httpx.BadRequest("newFork.title is required")
	}
	inheritMode := normalizeInheritMode(input.NewFork.InheritMode)
	if inheritMode == "" {
		return SplitResult{}, httpx.BadRequest("invalid inheritMode")
	}
	commitSHA := strings.TrimSpace(input.CommitSHA)
	if commitSHA == "" {
		return SplitResult{}, httpx.BadRequest("commitSha is required")
	}

	project, err := s.getProject(ctx, source.ProjectID)
	if err != nil {
		return SplitResult{}, err
	}
	if !project.GitPath.Valid || strings.TrimSpace(project.GitPath.String) == "" {
		return SplitResult{}, httpx.BadRequest("Project has no git repository configured")
	}
	gitPath := strings.TrimSpace(project.GitPath.String)
	sourceHead, err := s.splitSourceHead(ctx, source, gitPath)
	if err != nil {
		return SplitResult{}, err
	}
	commit, err := s.Git.ShowCommit(ctx, gitPath, commitSHA)
	if err != nil {
		return SplitResult{}, err
	}
	commitSHA = strings.TrimSpace(commit.SHA)
	if commitSHA == "" {
		return SplitResult{}, httpx.BadRequest("commitSha could not be resolved")
	}
	if strings.TrimSpace(sourceHead) == commitSHA {
		return SplitResult{}, httpx.BadRequest("Cannot split at the chapter HEAD; use fork instead")
	}
	if !s.Git.IsAncestor(ctx, gitPath, commitSHA, sourceHead) {
		return SplitResult{}, httpx.BadRequest("commitSha must be an ancestor of the chapter HEAD")
	}

	warnings := []string{}
	fallbacks := []ForkFallback{}
	fallback := func(feature, message string) {
		fallbacks = append(fallbacks, ForkFallback{Feature: feature, Fallback: true, Supported: false, Message: message})
		warnings = append(warnings, message)
	}
	if inheritMode == "compressed" {
		fallback("compressedContext", "Go backend does not generate an AI context summary for compressed split forks yet; it records a fallback summary and copies bounded split message refs.")
	}
	prefixTitle := splitPrefixTitle(source.Title, commitSHA)
	prefixArtifact, prefixRollback, err := s.prepareSplitBranchWorktree(ctx, gitPath, prefixTitle, "prefix", commitSHA)
	if err != nil {
		return SplitResult{}, err
	}
	rollbacks := []func() error{prefixRollback}

	newForkArtifact, newForkRollback, err := s.prepareSplitBranchWorktree(ctx, gitPath, newForkTitle, "fork", commitSHA)
	if err != nil {
		if rollbackErr := runSplitRollbacks(rollbacks); rollbackErr != nil {
			return SplitResult{}, fmt.Errorf("create split new fork worktree: %w; rollback failed: %v", err, rollbackErr)
		}
		return SplitResult{}, err
	}
	rollbacks = append(rollbacks, newForkRollback)

	prefixHead, _ := s.Git.HeadCommit(ctx, prefixArtifact.WorktreePath)
	if strings.TrimSpace(prefixHead) == "" {
		prefixHead = commitSHA
	}
	prefixCount, _ := s.Git.CommitCount(ctx, prefixArtifact.WorktreePath)
	newForkHead, _ := s.Git.HeadCommit(ctx, newForkArtifact.WorktreePath)
	if strings.TrimSpace(newForkHead) == "" {
		newForkHead = commitSHA
	}
	newForkCount, _ := s.Git.CommitCount(ctx, newForkArtifact.WorktreePath)

	now := db.NowISO()
	prefixID := id.New()
	newForkID := id.New()
	prefixStatus := "frozen"
	if source.Role == "trunk" {
		prefixStatus = "active"
	}
	prefixAxis := source.AxisOffset
	prefixCross := source.CrossOffset
	newForkAxis := source.AxisOffset
	if input.AxisOffset != nil {
		newForkAxis = *input.AxisOffset
	}
	newForkCross := source.CrossOffset + 100
	if input.CrossOffset != nil {
		newForkCross = *input.CrossOffset
	}
	newForkAnchor := commitSHA
	if input.AnchorCommitSHA != nil && strings.TrimSpace(*input.AnchorCommitSHA) != "" {
		newForkAnchor = strings.TrimSpace(*input.AnchorCommitSHA)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		if rollbackErr := runSplitRollbacks(rollbacks); rollbackErr != nil {
			return SplitResult{}, fmt.Errorf("begin split transaction: %w; rollback failed: %v", err, rollbackErr)
		}
		return SplitResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	contextCutoff, cutoffWarning, err := s.resolveSplitContextCutoffTx(ctx, tx, source.ID, gitPath, commitSHA)
	if err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("resolve split context cutoff: %w", err))
	}
	if strings.TrimSpace(cutoffWarning) != "" {
		fallback("splitContextCutoff", cutoffWarning)
	}

	prefixForkPoint := nullableJSON(source.ForkPoint)
	if _, err := tx.ExecContext(ctx, `INSERT INTO chapters (id, project_id, title, description, status, role, branch, worktree_path, base_branch, parent_chapter_id, fork_point, head_commit_sha, start_commit_sha, commit_count, anchor_commit_sha, axis_offset, cross_offset, container_config, is_root, last_accessed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`, prefixID, source.ProjectID, prefixTitle, nullablePtr(source.Description), prefixStatus, source.Role, prefixArtifact.BranchName, prefixArtifact.WorktreePath, source.BaseBranch, nullablePtr(source.ParentChapterID), prefixForkPoint, nullableString(prefixHead), nullableString(commitSHA), prefixCount, nullableString(commitSHA), prefixAxis, prefixCross, nullableJSON(source.ContainerConfig), now, now, now); err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("insert prefix chapter: %w", err))
	}

	continuationForkPoint := map[string]any{"commitSha": commitSHA, "split": true, "prefixChapterId": prefixID}
	if _, err := tx.ExecContext(ctx, `UPDATE chapters SET parent_chapter_id = ?, fork_point = ?, start_commit_sha = ?, anchor_commit_sha = ?, updated_at = ? WHERE id = ?`, prefixID, jsonObjectString(continuationForkPoint), nullableString(commitSHA), nullableString(commitSHA), now, source.ID); err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("update continuation chapter: %w", err))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chapter_edges SET target_id = ? WHERE project_id = ? AND target_id = ? AND type = 'fork'`, prefixID, source.ProjectID, source.ID); err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("redirect incoming fork edges: %w", err))
	}
	if err := ensureSplitInheritedParentEdgeTx(ctx, tx, source, prefixID, commitSHA, now); err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("sync inherited parent edge: %w", err))
	}
	if err := s.syncSplitExistingChildForksTx(ctx, tx, source.ProjectID, source.ID, prefixID, commitSHA, gitPath, now); err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("sync split child fork parents: %w", err))
	}
	if err := insertForkEdgeTx(ctx, tx, source.ProjectID, prefixID, source.ID, map[string]any{"commitSha": commitSHA, "splitRole": "continuation"}, now); err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("insert continuation fork edge: %w", err))
	}

	newForkPoint := map[string]any{"commitSha": commitSHA, "split": true, "prefixChapterId": prefixID, "continuationChapterId": source.ID}
	if len(fallbacks) > 0 {
		newForkPoint["fallbacks"] = fallbacks
	}
	newForkRole := input.NewForkRole()
	if _, err := tx.ExecContext(ctx, `INSERT INTO chapters (id, project_id, title, description, status, role, branch, worktree_path, base_branch, parent_chapter_id, fork_point, head_commit_sha, start_commit_sha, commit_count, anchor_commit_sha, axis_offset, cross_offset, container_config, is_root, last_accessed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`, newForkID, source.ProjectID, newForkTitle, nullablePtr(input.NewFork.Description), newForkRole, newForkArtifact.BranchName, newForkArtifact.WorktreePath, prefixArtifact.BranchName, prefixID, jsonObjectString(newForkPoint), nullableString(newForkHead), nullableString(commitSHA), newForkCount, nullableString(newForkAnchor), newForkAxis, newForkCross, nullableJSON(source.ContainerConfig), now, now, now); err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("insert new fork chapter: %w", err))
	}
	edgeMetadata := map[string]any{"commitSha": commitSHA, "inheritMode": inheritMode, "splitRole": "newFork"}
	if len(fallbacks) > 0 {
		edgeMetadata["fallbacks"] = fallbacks
	}
	if err := insertForkEdgeTx(ctx, tx, source.ProjectID, prefixID, newForkID, edgeMetadata, now); err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("insert new fork edge: %w", err))
	}
	if err := s.clonePrimaryNarratorsForSplitTx(ctx, tx, splitNarratorCloneInput{
		SourceChapterID:  source.ID,
		PrefixChapterID:  prefixID,
		NewForkChapterID: newForkID,
		PrefixTitle:      prefixTitle,
		NewForkTitle:     newForkTitle,
		PrefixCwd:        prefixArtifact.WorktreePath,
		NewForkCwd:       newForkArtifact.WorktreePath,
		ContextCutoff:    contextCutoff,
		InheritMode:      inheritMode,
		Fallbacks:        fallbacks,
		Now:              now,
	}); err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("clone split narrators: %w", err))
	}

	prefixChapter, err := getChapterTx(ctx, tx, prefixID)
	if err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("load prefix chapter: %w", err))
	}
	continuationChapter, err := getChapterTx(ctx, tx, source.ID)
	if err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("load continuation chapter: %w", err))
	}
	newForkChapter, err := getChapterTx(ctx, tx, newForkID)
	if err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("load new fork chapter: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return SplitResult{}, s.rollbackSplitFailure(rollbacks, fmt.Errorf("commit split transaction: %w", err))
	}
	committed = true

	if source.ContainerConfig != nil {
		s.startChapterContainersBackground(newForkID)
	}
	result := SplitResult{PrefixChapter: prefixChapter, ContinuationChapter: continuationChapter, NewForkChapter: newForkChapter, CommitSHA: commitSHA, Warnings: warnings, Fallbacks: fallbacks}
	if len(warnings) > 0 {
		result.PrefixChapter.Warnings = warnings
		result.NewForkChapter.Warnings = warnings
		result.NewForkChapter.Fallbacks = fallbacks
	}
	return result, nil
}

func (input SplitInput) NewForkRole() string {
	return "branch"
}

func (s *Service) splitSourceHead(ctx context.Context, source Chapter, gitPath string) (string, error) {
	if source.WorktreePath != nil && strings.TrimSpace(*source.WorktreePath) != "" {
		return s.Git.HeadCommit(ctx, strings.TrimSpace(*source.WorktreePath))
	}
	if strings.TrimSpace(source.Branch) != "" {
		return s.Git.GetRefCommit(ctx, gitPath, source.Branch)
	}
	return s.Git.HeadCommit(ctx, gitPath)
}

func (s *Service) prepareSplitBranchWorktree(ctx context.Context, gitPath, title, role, commitSHA string) (splitGitArtifact, func() error, error) {
	slug := slugify(title)
	short := id.Must(6)
	branchName := fmt.Sprintf("chapter/%s-%s-%s", slug, role, short)
	worktreePath := filepath.Join(gitPath, ".worktrees", fmt.Sprintf("%s-%s-%s", slug, role, short))
	if err := s.Git.CreateBranch(ctx, gitPath, branchName, commitSHA); err != nil {
		return splitGitArtifact{}, nil, err
	}
	branchCreated := true
	worktreeCreated := false
	rollback := func() error {
		errs := []string{}
		if worktreeCreated {
			if err := s.Git.RemoveWorktree(ctx, gitPath, worktreePath); err != nil {
				errs = append(errs, "remove worktree "+worktreePath+": "+err.Error())
			}
		}
		if branchCreated {
			if err := s.Git.DeleteBranch(ctx, gitPath, branchName); err != nil {
				errs = append(errs, "delete branch "+branchName+": "+err.Error())
			}
		}
		if len(errs) > 0 {
			return errors.New(strings.Join(errs, "; "))
		}
		return nil
	}
	if err := s.Git.CreateWorktree(ctx, gitPath, worktreePath, branchName); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return splitGitArtifact{}, nil, fmt.Errorf("create split worktree: %w; rollback failed: %v", err, rollbackErr)
		}
		return splitGitArtifact{}, nil, err
	}
	worktreeCreated = true
	return splitGitArtifact{BranchName: branchName, WorktreePath: worktreePath}, rollback, nil
}

func (s *Service) rollbackSplitFailure(rollbacks []func() error, cause error) error {
	if rollbackErr := runSplitRollbacks(rollbacks); rollbackErr != nil {
		return fmt.Errorf("%w; rollback failed: %v", cause, rollbackErr)
	}
	return cause
}

func runSplitRollbacks(rollbacks []func() error) error {
	errs := []string{}
	for i := len(rollbacks) - 1; i >= 0; i-- {
		if rollbacks[i] == nil {
			continue
		}
		if err := rollbacks[i](); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func getChapterTx(ctx context.Context, tx *sql.Tx, chapterID string) (Chapter, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+chapterSelectColumns+` FROM chapters WHERE id = ?`, chapterID)
	ch, err := scanChapter(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Chapter{}, httpx.NotFound("Chapter")
	}
	return ch, err
}

func insertForkEdgeTx(ctx context.Context, tx *sql.Tx, projectID, sourceID, targetID string, metadata map[string]any, createdAt string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES (?, ?, ?, ?, 'fork', ?, ?)`, id.New(), projectID, sourceID, targetID, jsonObjectString(metadata), createdAt)
	return err
}

func ensureSplitInheritedParentEdgeTx(ctx context.Context, tx *sql.Tx, source Chapter, prefixID, commitSHA, now string) error {
	parentID := ""
	if source.ParentChapterID != nil {
		parentID = strings.TrimSpace(*source.ParentChapterID)
	}
	if parentID == "" {
		_, err := tx.ExecContext(ctx, `DELETE FROM chapter_edges WHERE project_id = ? AND target_id = ? AND type = 'fork'`, source.ProjectID, prefixID)
		return err
	}
	if err := syncSplitForkEdgeParentTx(ctx, tx, source.ProjectID, parentID, prefixID, map[string]any{"commitSha": commitSHA, "splitRole": "prefix"}, now); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE chapters SET parent_chapter_id = ?, updated_at = ? WHERE id = ?`, parentID, now, prefixID)
	return err
}

func (s *Service) syncSplitExistingChildForksTx(ctx context.Context, tx *sql.Tx, projectID, sourceID, prefixID, splitSHA, gitPath, now string) error {
	rows, err := tx.QueryContext(ctx, `SELECT c.id, c.fork_point, c.start_commit_sha FROM chapters c WHERE c.project_id = ? AND c.id <> ? AND (c.parent_chapter_id = ? OR EXISTS (SELECT 1 FROM chapter_edges e WHERE e.project_id = ? AND e.source_id = ? AND e.target_id = c.id AND e.type = 'fork')) ORDER BY c.created_at ASC, c.id ASC`, projectID, sourceID, sourceID, projectID, sourceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type childRow struct {
		id        string
		forkPoint sql.NullString
		start     sql.NullString
	}
	children := []childRow{}
	for rows.Next() {
		var child childRow
		if err := rows.Scan(&child.id, &child.forkPoint, &child.start); err != nil {
			return err
		}
		children = append(children, child)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, child := range children {
		forkCommit := splitChildForkCommitSHA(child.forkPoint, child.start)
		desiredParent := sourceID
		if s.splitChildForkBelongsToPrefix(ctx, gitPath, forkCommit, splitSHA) {
			desiredParent = prefixID
		}
		if _, err := tx.ExecContext(ctx, `UPDATE chapters SET parent_chapter_id = ?, updated_at = ? WHERE id = ?`, desiredParent, now, child.id); err != nil {
			return err
		}
		metadata := map[string]any{"splitRole": "existingChild", "splitCommitSha": splitSHA}
		if forkCommit != "" {
			metadata["commitSha"] = forkCommit
		}
		if err := syncSplitForkEdgeParentTx(ctx, tx, projectID, desiredParent, child.id, metadata, now); err != nil {
			return err
		}
	}
	return nil
}

func syncSplitForkEdgeParentTx(ctx context.Context, tx *sql.Tx, projectID, parentID, childID string, metadata map[string]any, now string) error {
	result, err := tx.ExecContext(ctx, `UPDATE chapter_edges SET source_id = ? WHERE project_id = ? AND target_id = ? AND type = 'fork'`, parentID, projectID, childID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err == nil && changed > 0 {
		return nil
	}
	return insertForkEdgeTx(ctx, tx, projectID, parentID, childID, metadata, now)
}

func splitChildForkCommitSHA(forkPoint sql.NullString, start sql.NullString) string {
	if forkPoint.Valid && strings.TrimSpace(forkPoint.String) != "" {
		raw := strings.TrimSpace(forkPoint.String)
		var decoded map[string]any
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			if commitSHA, ok := decoded["commitSha"].(string); ok && strings.TrimSpace(commitSHA) != "" {
				return strings.TrimSpace(commitSHA)
			}
		} else {
			return raw
		}
	}
	if start.Valid && strings.TrimSpace(start.String) != "" {
		return strings.TrimSpace(start.String)
	}
	return ""
}

func (s *Service) splitChildForkBelongsToPrefix(ctx context.Context, gitPath, childForkSHA, splitSHA string) bool {
	relation := s.splitCommitRelation(ctx, gitPath, childForkSHA, splitSHA)
	return relation == "exact" || relation == "before"
}

func splitPrefixTitle(title, commitSHA string) string {
	base := strings.TrimSpace(title)
	if base == "" {
		base = "Chapter"
	}
	short := commitSHA
	if len(short) > 8 {
		short = short[:8]
	}
	candidate := base + " prefix " + short
	if len(candidate) > 200 {
		candidate = strings.TrimSpace(candidate[:200])
	}
	return candidate
}

type splitNarratorCloneInput struct {
	SourceChapterID  string
	PrefixChapterID  string
	NewForkChapterID string
	PrefixTitle      string
	NewForkTitle     string
	PrefixCwd        string
	NewForkCwd       string
	ContextCutoff    splitContextCutoff
	InheritMode      string
	Fallbacks        []ForkFallback
	Now              string
}

func (s *Service) clonePrimaryNarratorsForSplitTx(ctx context.Context, tx *sql.Tx, input splitNarratorCloneInput) error {
	seed, hasSeed, err := splitPrimaryNarratorSeedTx(ctx, tx, input.SourceChapterID)
	if err != nil {
		return err
	}
	model, permission := s.splitDefaultNarratorSettings()
	if hasSeed {
		if seed.Model.Valid && strings.TrimSpace(seed.Model.String) != "" {
			model = strings.TrimSpace(seed.Model.String)
		}
		if seed.PermissionMode.Valid && strings.TrimSpace(seed.PermissionMode.String) != "" {
			permission = strings.TrimSpace(seed.PermissionMode.String)
		}
	}
	prefixRefs := []splitMessageRef{}
	if hasSeed {
		prefixRefs, err = splitRefsUpToSeqTx(ctx, tx, seed.ID, input.ContextCutoff.Seq)
		if err != nil {
			return err
		}
	}
	prefixNarratorID := id.New()
	prefixForkMessageID := lastSplitRefMessageID(prefixRefs)
	prefixLastMessageAt := lastSplitRefCreatedAt(prefixRefs)
	prefixSystemPrompt := any(nil)
	prefixReasoningEffort := any(nil)
	planReflection := "inherit"
	dangerReflection := "inherit"
	fastMode := 0
	relaxedPlan := 0
	if hasSeed {
		prefixSystemPrompt = nullableNullString(seed.SystemPrompt)
		prefixReasoningEffort = nullableNullString(seed.ReasoningEffort)
		planReflection = firstNonEmptyString(seed.PlanReflectionAutoApproveOverride, "inherit")
		dangerReflection = firstNonEmptyString(seed.DangerReflectionOverride, "inherit")
		fastMode = seed.FastMode
		relaxedPlan = seed.RelaxedPlan
	}
	if err := insertSplitNarratorTx(ctx, tx, splitInsertNarratorInput{
		ID: prefixNarratorID, ChapterID: input.PrefixChapterID, Title: input.PrefixTitle,
		InheritMode: "full", ParentNarratorID: seedIDOrEmpty(seed, hasSeed), ForkMessageID: prefixForkMessageID,
		Model: model, PermissionMode: permission, SystemPrompt: prefixSystemPrompt, ReasoningEffort: prefixReasoningEffort,
		FastMode: fastMode, RelaxedPlan: relaxedPlan, PlanReflectionAutoApproveOverride: planReflection,
		DangerReflectionOverride: dangerReflection, MessageCount: len(prefixRefs), LastMessageAt: prefixLastMessageAt,
		Cwd: input.PrefixCwd, Now: input.Now,
	}); err != nil {
		return err
	}
	if err := insertSplitRefsTx(ctx, tx, prefixNarratorID, prefixRefs); err != nil {
		return err
	}

	newForkRefs := []splitMessageRef{}
	if input.InheritMode == "full" || input.InheritMode == "compressed" {
		newForkRefs = prefixRefs
	}
	contextSummary := any(nil)
	newForkSystemPrompt := prefixSystemPrompt
	if input.InheritMode == "compressed" {
		summary := fmt.Sprintf("Go backend compressed split fallback: copied %d prefix message reference(s) instead of generating an AI context summary.", len(newForkRefs))
		contextSummary = summary
		if prompt, ok := newForkSystemPrompt.(string); ok && strings.TrimSpace(prompt) != "" {
			newForkSystemPrompt = prompt + "\n\n## Previous Context Summary\n\n" + summary
		}
	}
	newForkNarratorID := id.New()
	if err := insertSplitNarratorTx(ctx, tx, splitInsertNarratorInput{
		ID: newForkNarratorID, ChapterID: input.NewForkChapterID, Title: input.NewForkTitle,
		InheritMode: input.InheritMode, ParentNarratorID: prefixNarratorID, ForkMessageID: lastSplitRefMessageID(newForkRefs),
		ContextSummary: contextSummary, Model: model, PermissionMode: permission, SystemPrompt: newForkSystemPrompt,
		ReasoningEffort: prefixReasoningEffort, FastMode: fastMode, RelaxedPlan: relaxedPlan,
		PlanReflectionAutoApproveOverride: planReflection, DangerReflectionOverride: dangerReflection,
		MessageCount: len(newForkRefs), LastMessageAt: lastSplitRefCreatedAt(newForkRefs), Cwd: input.NewForkCwd, Now: input.Now,
	}); err != nil {
		return err
	}
	return insertSplitRefsTx(ctx, tx, newForkNarratorID, newForkRefs)
}

func splitPrimaryNarratorSeedTx(ctx context.Context, tx *sql.Tx, chapterID string) (splitNarratorSeed, bool, error) {
	var seed splitNarratorSeed
	err := tx.QueryRowContext(ctx, `SELECT id, title, model, system_prompt, permission_mode, reasoning_effort, COALESCE(fast_mode, 0), COALESCE(relaxed_plan, 0), COALESCE(plan_reflection_auto_approve_override, 'inherit'), COALESCE(danger_reflection_override, 'inherit') FROM narrators WHERE chapter_id = ? AND variant = 'primary' ORDER BY created_at ASC LIMIT 1`, chapterID).Scan(&seed.ID, &seed.Title, &seed.Model, &seed.SystemPrompt, &seed.PermissionMode, &seed.ReasoningEffort, &seed.FastMode, &seed.RelaxedPlan, &seed.PlanReflectionAutoApproveOverride, &seed.DangerReflectionOverride)
	if errors.Is(err, sql.ErrNoRows) {
		return splitNarratorSeed{}, false, nil
	}
	return seed, err == nil, err
}

func (s *Service) resolveSplitContextCutoffTx(ctx context.Context, tx *sql.Tx, sourceChapterID, gitPath, splitSHA string) (splitContextCutoff, string, error) {
	seed, hasSeed, err := splitPrimaryNarratorSeedTx(ctx, tx, sourceChapterID)
	if err != nil {
		return splitContextCutoff{}, "", err
	}
	if !hasSeed {
		return splitContextCutoff{Strategy: "fallback-empty"}, "No primary narrator seed was available for split cutoff; copied zero split message refs.", nil
	}
	hasRefs, err := splitNarratorHasRefsTx(ctx, tx, seed.ID)
	if err != nil {
		return splitContextCutoff{}, "", err
	}
	if !hasRefs {
		return splitContextCutoff{Strategy: "fallback-empty"}, "Primary narrator had no message refs for split cutoff; copied zero split message refs.", nil
	}
	candidates, err := splitCommitMessageRefCandidatesTx(ctx, tx, sourceChapterID, seed.ID)
	if err != nil {
		return splitContextCutoff{}, "", err
	}
	relationCache := map[string]string{}
	var exact splitCommitMessageRef
	var ancestor splitCommitMessageRef
	firstUnsafeSeq := 0
	for _, candidate := range candidates {
		relation, ok := relationCache[candidate.CommitSHA]
		if !ok {
			relation = s.splitCommitRelation(ctx, gitPath, candidate.CommitSHA, splitSHA)
			relationCache[candidate.CommitSHA] = relation
		}
		if relation == "after" || relation == "unrelated" {
			if firstUnsafeSeq == 0 || candidate.Seq < firstUnsafeSeq {
				firstUnsafeSeq = candidate.Seq
			}
			continue
		}
		if firstUnsafeSeq != 0 && candidate.Seq >= firstUnsafeSeq {
			continue
		}
		if relation == "exact" {
			exact = candidate
			continue
		}
		if relation == "before" {
			ancestor = candidate
		}
	}
	if exact.Seq > 0 {
		return splitContextCutoff{Seq: exact.Seq, MessageID: exact.MessageID, CommitSHA: exact.CommitSHA, Strategy: "exact:" + exact.Source, Exact: true}, "", nil
	}
	if ancestor.Seq > 0 {
		warning := fmt.Sprintf("No exact commit/message association was found for split commit %s; used explicit ancestor commit %s at message %s (seq %d) as a safe cutoff instead of comparing commit AuthoredAt to message created_at.", splitShortSHA(splitSHA), splitShortSHA(ancestor.CommitSHA), ancestor.MessageID, ancestor.Seq)
		return splitContextCutoff{Seq: ancestor.Seq, MessageID: ancestor.MessageID, CommitSHA: ancestor.CommitSHA, Strategy: "fallback-ancestor:" + ancestor.Source}, warning, nil
	}
	warning := fmt.Sprintf("No exact commit/message association was found for split commit %s; copied zero split message refs instead of guessing from commit AuthoredAt/message created_at timestamps.", splitShortSHA(splitSHA))
	return splitContextCutoff{Strategy: "fallback-empty"}, warning, nil
}

func splitNarratorHasRefsTx(ctx context.Context, tx *sql.Tx, narratorID string) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM narrator_message_refs WHERE narrator_id = ? LIMIT 1`, narratorID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if isSplitAssociationSchemaMissing(err) {
		return false, nil
	}
	return err == nil, err
}

func splitCommitMessageRefCandidatesTx(ctx context.Context, tx *sql.Tx, sourceChapterID, narratorID string) ([]splitCommitMessageRef, error) {
	candidates := []splitCommitMessageRef{}
	messageCandidates, err := splitMessageCommitCandidatesTx(ctx, tx, narratorID)
	if err != nil {
		return nil, err
	}
	candidates = append(candidates, messageCandidates...)
	commitCandidates, err := splitChapterCommitMessageCandidatesTx(ctx, tx, sourceChapterID, narratorID)
	if err != nil {
		return nil, err
	}
	candidates = append(candidates, commitCandidates...)
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Seq < candidates[j].Seq
	})
	return candidates, nil
}

func splitMessageCommitCandidatesTx(ctx context.Context, tx *sql.Tx, narratorID string) ([]splitCommitMessageRef, error) {
	rows, err := tx.QueryContext(ctx, `SELECT r.seq, r.message_id, m.commit_sha FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND m.commit_sha IS NOT NULL AND m.commit_sha <> '' ORDER BY r.seq ASC`, narratorID)
	if err != nil {
		if isSplitAssociationSchemaMissing(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	return scanSplitCommitMessageRefs(rows, "narrator_messages.commit_sha")
}

func splitChapterCommitMessageCandidatesTx(ctx context.Context, tx *sql.Tx, sourceChapterID, narratorID string) ([]splitCommitMessageRef, error) {
	rows, err := tx.QueryContext(ctx, `SELECT r.seq, r.message_id, c.sha FROM chapter_commits c JOIN narrator_message_refs r ON r.message_id = c.narrator_message_id AND r.narrator_id = ? WHERE c.chapter_id = ? AND c.sha IS NOT NULL AND c.sha <> '' AND c.narrator_message_id IS NOT NULL AND c.narrator_message_id <> '' ORDER BY r.seq ASC`, narratorID, sourceChapterID)
	if err != nil {
		if isSplitAssociationSchemaMissing(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	return scanSplitCommitMessageRefs(rows, "chapter_commits.narrator_message_id")
}

func scanSplitCommitMessageRefs(rows *sql.Rows, source string) ([]splitCommitMessageRef, error) {
	refs := []splitCommitMessageRef{}
	seen := map[string]bool{}
	for rows.Next() {
		var ref splitCommitMessageRef
		if err := rows.Scan(&ref.Seq, &ref.MessageID, &ref.CommitSHA); err != nil {
			return nil, err
		}
		ref.CommitSHA = strings.TrimSpace(ref.CommitSHA)
		if ref.CommitSHA == "" {
			continue
		}
		ref.Source = source
		key := fmt.Sprintf("%d:%s:%s", ref.Seq, ref.MessageID, ref.CommitSHA)
		if seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (s *Service) splitCommitRelation(ctx context.Context, gitPath, candidateSHA, splitSHA string) string {
	candidateSHA = strings.TrimSpace(candidateSHA)
	splitSHA = strings.TrimSpace(splitSHA)
	if candidateSHA == "" || splitSHA == "" {
		return "unrelated"
	}
	resolved := candidateSHA
	if s != nil && s.Git != nil && strings.TrimSpace(gitPath) != "" {
		if full, err := s.Git.GetRefCommit(ctx, gitPath, candidateSHA); err == nil && strings.TrimSpace(full) != "" {
			resolved = strings.TrimSpace(full)
		}
	}
	if splitSameCommitSHA(resolved, splitSHA) {
		return "exact"
	}
	if s != nil && s.Git != nil && strings.TrimSpace(gitPath) != "" {
		if s.Git.IsAncestor(ctx, gitPath, resolved, splitSHA) {
			return "before"
		}
		if s.Git.IsAncestor(ctx, gitPath, splitSHA, resolved) {
			return "after"
		}
	}
	return "unrelated"
}

func isSplitAssociationSchemaMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "no such column") || strings.Contains(msg, "does not exist")
}

func splitSameCommitSHA(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return (len(a) >= 7 && strings.HasPrefix(b, a)) || (len(b) >= 7 && strings.HasPrefix(a, b))
}

func splitShortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func splitRefsUpToSeqTx(ctx context.Context, tx *sql.Tx, narratorID string, cutoffSeq int) ([]splitMessageRef, error) {
	if cutoffSeq <= 0 {
		return []splitMessageRef{}, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.message_id, COALESCE(r.is_compact, 0), m.created_at, r.seq FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND r.seq <= ? ORDER BY r.seq ASC`, narratorID, cutoffSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []splitMessageRef{}
	for rows.Next() {
		var ref splitMessageRef
		if err := rows.Scan(&ref.MessageID, &ref.IsCompact, &ref.CreatedAt, &ref.Seq); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

type splitInsertNarratorInput struct {
	ID                                string
	ChapterID                         string
	Title                             string
	InheritMode                       string
	ParentNarratorID                  string
	ForkMessageID                     string
	ContextSummary                    any
	Model                             string
	PermissionMode                    string
	SystemPrompt                      any
	ReasoningEffort                   any
	FastMode                          int
	RelaxedPlan                       int
	PlanReflectionAutoApproveOverride string
	DangerReflectionOverride          string
	MessageCount                      int
	LastMessageAt                     string
	Cwd                               string
	Now                               string
}

func insertSplitNarratorTx(ctx context.Context, tx *sql.Tx, input splitInsertNarratorInput) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO narrators (
		id, chapter_id, type, title, inherit_mode, parent_narrator_id, fork_message_id,
		context_summary, model, system_prompt, permission_mode, reasoning_effort, fast_mode,
		relaxed_plan, plan_reflection_auto_approve_override, danger_reflection_override,
		message_count, last_message_at, status, substatus, plan_mode, cwd, prune_enabled,
		variant, traits, is_background, is_ask_in_passing, message_version, created_at, updated_at
	) VALUES (?, ?, 'primary', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'idle', '[]', 0, ?, 1, 'primary', '[]', 0, 0, 0, ?, ?)`,
		input.ID, input.ChapterID, input.Title, input.InheritMode, nullableString(input.ParentNarratorID), nullableString(input.ForkMessageID), nullableAny(input.ContextSummary), input.Model, nullableAny(input.SystemPrompt), input.PermissionMode, nullableAny(input.ReasoningEffort), input.FastMode, input.RelaxedPlan, firstNonEmptyString(input.PlanReflectionAutoApproveOverride, "inherit"), firstNonEmptyString(input.DangerReflectionOverride, "inherit"), input.MessageCount, nullableString(input.LastMessageAt), nullableString(input.Cwd), input.Now, input.Now)
	return err
}

func insertSplitRefsTx(ctx context.Context, tx *sql.Tx, narratorID string, refs []splitMessageRef) error {
	for i, ref := range refs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, ?)`, id.New(), narratorID, ref.MessageID, i+1, ref.IsCompact); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) splitDefaultNarratorSettings() (string, string) {
	if s != nil && s.Settings != nil {
		core := s.Settings.Core()
		return firstNonEmptyString(core.Agent.DefaultModel, config.DefaultAgentModel), firstNonEmptyString(core.Agent.DefaultPermissionMode, "acceptEdits")
	}
	return config.DefaultAgentModel, "acceptEdits"
}

func seedIDOrEmpty(seed splitNarratorSeed, ok bool) string {
	if !ok {
		return ""
	}
	return seed.ID
}

func lastSplitRefMessageID(refs []splitMessageRef) string {
	if len(refs) == 0 {
		return ""
	}
	return refs[len(refs)-1].MessageID
}

func lastSplitRefCreatedAt(refs []splitMessageRef) string {
	if len(refs) == 0 {
		return ""
	}
	return refs[len(refs)-1].CreatedAt
}

func nullableNullString(v sql.NullString) any {
	if !v.Valid || strings.TrimSpace(v.String) == "" {
		return nil
	}
	return v.String
}

func nullableAny(v any) any {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return nullableString(s)
	}
	return v
}
