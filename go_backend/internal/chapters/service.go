package chapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/jsondb"
	"narrafork-go/internal/narrators"
	"narrafork-go/internal/terminalruntime"
)

type TerminalKiller interface {
	Kill(ctx context.Context, terminalID string) error
}

type Service struct {
	DB                     *sql.DB
	Git                    *gitx.Service
	Narrators              *narrators.Service
	TerminalRuntime        TerminalKiller
	Settings               *config.Store
	BroadcastMergeProgress func(payload any)
	MergeDecisionTimeout   time.Duration
	MergeDecisionInterval  time.Duration
	// BrowserToolAutoEnableEnabled keeps the container-ready browser tool injection
	// disabled unless an explicit cutover opts into auto-enabling it.
	BrowserToolAutoEnableEnabled bool
}

type Chapter struct {
	ID                    string         `json:"id"`
	ProjectID             string         `json:"projectId"`
	Title                 string         `json:"title"`
	Description           *string        `json:"description"`
	Status                string         `json:"status"`
	Role                  string         `json:"role"`
	Branch                string         `json:"branch"`
	WorktreePath          *string        `json:"worktreePath"`
	BaseBranch            string         `json:"baseBranch"`
	ParentChapterID       *string        `json:"parentChapterId"`
	ForkPoint             any            `json:"forkPoint"`
	MergedIntoChapterID   *string        `json:"mergedIntoChapterId"`
	MergeCommitSHA        *string        `json:"mergeCommitSha"`
	MergeStrategy         *string        `json:"mergeStrategy"`
	PreMergeTargetSHA     *string        `json:"preMergeTargetSha"`
	ContainerConfig       any            `json:"containerConfig"`
	ExplorationGroupID    *string        `json:"explorationGroupId"`
	IsRoot                bool           `json:"isRoot"`
	HeadCommitSHA         *string        `json:"headCommitSha"`
	StartCommitSHA        *string        `json:"startCommitSha"`
	CommitCount           int            `json:"commitCount"`
	Color                 *string        `json:"color"`
	GroupLabel            *string        `json:"groupLabel"`
	Pinned                bool           `json:"pinned"`
	AnchorCommitSHA       *string        `json:"anchorCommitSha"`
	AxisOffset            float64        `json:"axisOffset"`
	CrossOffset           float64        `json:"crossOffset"`
	PanelExpanded         bool           `json:"panelExpanded"`
	PanelWidth            *float64       `json:"panelWidth"`
	PanelHeight           *float64       `json:"panelHeight"`
	ReviewSourceChapterID *string        `json:"reviewSourceChapterId"`
	ReviewStatus          *string        `json:"reviewStatus"`
	LastAccessedAt        *string        `json:"lastAccessedAt"`
	CreatedAt             string         `json:"createdAt"`
	UpdatedAt             string         `json:"updatedAt"`
	PanelActiveTab        *string        `json:"panelActiveTab"`
	PanelFilters          map[string]any `json:"panelFilters"`
	PanelRefreshNeeded    bool           `json:"panelRefreshNeeded"`
	PanelLastRefreshAt    *time.Time     `json:"panelLastRefreshAt"`
	PanelSearchQuery      *string        `json:"panelSearchQuery"`
	PanelSortBy           *string        `json:"panelSortBy"`
	PanelSortDirection    *string        `json:"panelSortDirection"`
	PanelFilterTags       []string       `json:"panelFilterTags"`
	Warnings              []string       `json:"warnings"`
	Fallbacks             []ForkFallback `json:"fallbacks"`
}

func (s *Service) EnableBrowserToolAutoEnable() {
	if s == nil {
		return
	}
	s.BrowserToolAutoEnableEnabled = true
}

func (s *Service) DisableBrowserToolAutoEnable() {
	if s == nil {
		return
	}
	s.BrowserToolAutoEnableEnabled = false
}

type ForkFallback struct {
	Feature   string `json:"feature"`
	Fallback  bool   `json:"fallback"`
	Supported bool   `json:"supported"`
	Message   string `json:"message"`
}

type CreateInput struct {
	ProjectID   string
	Title       string
	Description *string
	BaseBranch  string
}

type ForkInput struct {
	Title             *string
	Description       *string
	Role              string
	InheritMode       string
	ForkAtMessageUUID string
	StartCommitSHA    *string
	AnchorCommitSHA   *string
	AxisOffset        *float64
	CrossOffset       *float64
	Locale            string
}

type ReviewInput struct {
	Title           *string
	Locale          string
	AnchorCommitSHA *string
	AxisOffset      *float64
	CrossOffset     *float64
	UserID          string
}

type ChapterGitStatus struct {
	CommitsAhead int    `json:"commitsAhead"`
	BaseBranch   string `json:"baseBranch"`
	LinesAdded   int    `json:"linesAdded"`
	LinesRemoved int    `json:"linesRemoved"`
}

type BatchMergeInput struct {
	BaseChapterID    string
	SourceChapterIDs []string
	Title            string
	Strategy         string
	TargetChapterID  string
	Locale           string
	UserID           string
}

type BatchMergeResult struct {
	MergeSessionID  string `json:"mergeSessionId"`
	TargetChapterID string `json:"targetChapterId"`
	CreatedTarget   bool   `json:"createdTarget"`
	Status          string `json:"status"`
}

type CleanupError struct {
	ChapterID string `json:"chapterId"`
	Error     string `json:"error"`
}

type CleanupReport struct {
	Cleaned []string       `json:"cleaned"`
	Skipped []string       `json:"skipped"`
	Errors  []CleanupError `json:"errors"`
}

type RootInput struct {
	ProjectID      string
	Title          string
	GitPath        string
	DefaultBranch  string
	AutoNarrator   bool
	NarratorModel  string
	PermissionMode string
}

type projectRow struct {
	ID              string
	Name            string
	GitPath         sql.NullString
	DefaultBranch   sql.NullString
	StartupScript   sql.NullString
	CopyFiles       sql.NullString
	ChapterSettings sql.NullString
}

func NewService(sqlDB *sql.DB, git *gitx.Service, narratorService *narrators.Service, terminalRuntime *terminalruntime.Manager, settings *config.Store) *Service {
	return &Service{DB: sqlDB, Git: git, Narrators: narratorService, TerminalRuntime: terminalRuntime, Settings: settings}
}

func (s *Service) SetMergeProgressBroadcaster(broadcast func(payload any)) {
	s.BroadcastMergeProgress = broadcast
}

func (s *Service) CleanupStaleMergeSessions(ctx context.Context) (int64, error) {
	if s == nil || s.DB == nil {
		return 0, nil
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE merge_sessions SET status = 'error', error = 'Server restarted during merge', updated_at = ? WHERE status IN ('running', 'ai_resolving', 'waiting_decision')`, db.NowISO())
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return rows, nil
}

func (s *Service) CreateRootChapter(ctx context.Context, input RootInput) (Chapter, error) {
	now := db.NowISO()
	chapterID := id.New()
	head, _ := s.Git.HeadCommit(ctx, input.GitPath)
	count, _ := s.Git.CommitCount(ctx, input.GitPath)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, is_root, head_commit_sha, commit_count, last_accessed_at, created_at, updated_at)
		VALUES (?, ?, ?, 'active', 'trunk', ?, ?, ?, 1, ?, ?, ?, ?, ?)`, chapterID, input.ProjectID, input.Title, input.DefaultBranch, input.GitPath, input.DefaultBranch, nullableString(head), count, now, now, now)
	if err != nil {
		return Chapter{}, err
	}
	if input.AutoNarrator && s.Narrators != nil {
		_, _ = s.Narrators.Create(ctx, narrators.CreateInput{ChapterID: &chapterID, Title: &input.Title, Model: input.NarratorModel, PermissionMode: input.PermissionMode, Cwd: &input.GitPath})
	}
	return s.Get(ctx, chapterID)
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Chapter, error) {
	if strings.TrimSpace(input.Title) == "" {
		return Chapter{}, httpx.BadRequest("title is required")
	}
	project, err := s.getProject(ctx, input.ProjectID)
	if err != nil {
		return Chapter{}, err
	}
	if !project.GitPath.Valid || project.GitPath.String == "" {
		return Chapter{}, httpx.BadRequest("Project has no git repository configured")
	}
	gitPath := project.GitPath.String
	if !s.Git.IsRepo(ctx, gitPath) {
		return Chapter{}, httpx.BadRequest("Project path is not a git repository")
	}
	baseBranch := input.BaseBranch
	if baseBranch == "" && project.DefaultBranch.Valid {
		baseBranch = project.DefaultBranch.String
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	if !s.Git.BranchExists(ctx, gitPath, baseBranch) {
		current, err := s.Git.CurrentBranch(ctx, gitPath)
		if err != nil {
			return Chapter{}, err
		}
		baseBranch = current
	}
	slug := slugify(input.Title)
	short := id.Must(6)
	branchName := fmt.Sprintf("chapter/%s-%s", slug, short)
	worktreePath := filepath.Join(gitPath, ".worktrees", slug+"-"+short)
	if err := s.Git.CreateBranch(ctx, gitPath, branchName, baseBranch); err != nil {
		return Chapter{}, err
	}
	if err := s.Git.CreateWorktree(ctx, gitPath, worktreePath, branchName); err != nil {
		_ = s.Git.DeleteBranch(ctx, gitPath, branchName)
		return Chapter{}, err
	}
	now := db.NowISO()
	head, _ := s.Git.HeadCommit(ctx, worktreePath)
	count, _ := s.Git.CommitCount(ctx, worktreePath)
	chapterID := id.New()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO chapters (id, project_id, title, description, status, role, branch, worktree_path, base_branch, head_commit_sha, commit_count, last_accessed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', 'branch', ?, ?, ?, ?, ?, ?, ?, ?)`, chapterID, input.ProjectID, input.Title, nullablePtr(input.Description), branchName, worktreePath, baseBranch, nullableString(head), count, now, now, now)
	if err != nil {
		_ = s.Git.RemoveWorktree(ctx, gitPath, worktreePath)
		_ = s.Git.DeleteBranch(ctx, gitPath, branchName)
		return Chapter{}, err
	}
	if s.Narrators != nil {
		core := s.Settings.Core()
		_, _ = s.Narrators.Create(ctx, narrators.CreateInput{ChapterID: &chapterID, Title: &input.Title, Model: core.Agent.DefaultModel, PermissionMode: core.Agent.DefaultPermissionMode, Cwd: &worktreePath})
	}
	return s.Get(ctx, chapterID)
}

func (s *Service) Fork(ctx context.Context, sourceID string, input ForkInput) (Chapter, error) {
	source, err := s.Get(ctx, sourceID)
	if err != nil {
		return Chapter{}, err
	}
	if source.Status != "active" {
		return Chapter{}, httpx.BadRequest("Can only fork active chapters")
	}
	title := source.Title + " fork"
	if input.Title != nil && strings.TrimSpace(*input.Title) != "" {
		title = strings.TrimSpace(*input.Title)
	}
	inheritMode := normalizeInheritMode(input.InheritMode)
	if inheritMode == "" {
		return Chapter{}, httpx.BadRequest("invalid inheritMode")
	}
	project, err := s.getProject(ctx, source.ProjectID)
	if err != nil {
		return Chapter{}, err
	}
	if !project.GitPath.Valid || strings.TrimSpace(project.GitPath.String) == "" {
		return Chapter{}, httpx.BadRequest("Project has no git repository configured")
	}
	gitPath := project.GitPath.String
	parentCwd := gitPath
	if source.WorktreePath != nil && strings.TrimSpace(*source.WorktreePath) != "" {
		parentCwd = strings.TrimSpace(*source.WorktreePath)
	}
	primaryNarratorID, err := s.primaryNarratorID(ctx, sourceID)
	if err != nil {
		return Chapter{}, err
	}
	fallbacks := []ForkFallback{}
	warnings := []string{}
	fallback := func(feature, message string) {
		fallbacks = append(fallbacks, ForkFallback{Feature: feature, Fallback: true, Supported: false, Message: message})
		warnings = append(warnings, message)
	}
	baseBranch := source.Branch
	forkStart, commitFallback, err := s.resolveForkCommit(ctx, source, gitPath, parentCwd, primaryNarratorID, input)
	if err != nil {
		return Chapter{}, err
	}
	if commitFallback != "" {
		fallback("forkAtMessageCommit", commitFallback)
	}
	slug := slugify(title)
	short := id.Must(6)
	branchName := fmt.Sprintf("chapter/%s-%s", slug, short)
	worktreePath := filepath.Join(gitPath, ".worktrees", slug+"-"+short)
	if err := s.Git.CreateBranch(ctx, gitPath, branchName, forkStart); err != nil {
		return Chapter{}, err
	}
	if err := s.Git.CreateWorktree(ctx, gitPath, worktreePath, branchName); err != nil {
		_ = s.Git.DeleteBranch(ctx, gitPath, branchName)
		return Chapter{}, err
	}
	chapterID := id.New()
	restoreApplied := 0
	if strings.TrimSpace(input.ForkAtMessageUUID) != "" && primaryNarratorID != "" {
		applied, skipped, restoreErr := s.restoreFileStatesAtForkMessage(ctx, primaryNarratorID, strings.TrimSpace(input.ForkAtMessageUUID), parentCwd, worktreePath)
		restoreApplied = applied
		if restoreErr != nil {
			fallback("fileSnapshotRestore", "File snapshot restore failed: "+restoreErr.Error()+". The forked worktree may be missing uncommitted parent changes.")
		} else if skipped > 0 {
			fallback("fileSnapshotRestorePartial", fmt.Sprintf("File snapshot restore skipped %d file(s) outside the worktree or without restorable content.", skipped))
		}
	}
	now := db.NowISO()
	role := input.Role
	if role != "exploration" {
		role = "branch"
	}
	head, _ := s.Git.HeadCommit(ctx, worktreePath)
	count, _ := s.Git.CommitCount(ctx, worktreePath)
	anchor := forkStart
	if input.AnchorCommitSHA != nil && strings.TrimSpace(*input.AnchorCommitSHA) != "" {
		anchor = strings.TrimSpace(*input.AnchorCommitSHA)
	}
	axis := source.AxisOffset
	if input.AxisOffset != nil {
		axis = *input.AxisOffset
	}
	cross := source.CrossOffset + 100
	if input.CrossOffset != nil {
		cross = *input.CrossOffset
	}
	forkPoint := map[string]any{"commitSha": forkStart}
	if strings.TrimSpace(input.ForkAtMessageUUID) != "" {
		forkPoint["narratorMessageUuid"] = strings.TrimSpace(input.ForkAtMessageUUID)
	}
	if restoreApplied > 0 {
		forkPoint["restoredFileSnapshotCount"] = restoreApplied
	}
	if len(fallbacks) > 0 {
		forkPoint["fallbacks"] = fallbacks
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO chapters (id, project_id, title, description, status, role, branch, worktree_path, base_branch, parent_chapter_id, fork_point, head_commit_sha, start_commit_sha, commit_count, anchor_commit_sha, axis_offset, cross_offset, container_config, is_root, last_accessed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`, chapterID, source.ProjectID, title, nullablePtr(input.Description), role, branchName, worktreePath, baseBranch, sourceID, jsonObjectString(forkPoint), nullableString(head), nullableString(forkStart), count, nullableString(anchor), axis, cross, nullableJSON(source.ContainerConfig), now, now, now)
	if err != nil {
		_ = s.Git.RemoveWorktree(ctx, gitPath, worktreePath)
		_ = s.Git.DeleteBranch(ctx, gitPath, branchName)
		return Chapter{}, err
	}
	edgeMetadata := map[string]any{"commitSha": forkStart, "inheritMode": inheritMode}
	if strings.TrimSpace(input.ForkAtMessageUUID) != "" {
		edgeMetadata["narratorMessageUuid"] = strings.TrimSpace(input.ForkAtMessageUUID)
	}
	if len(fallbacks) > 0 {
		edgeMetadata["fallbacks"] = fallbacks
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES (?, ?, ?, ?, 'fork', ?, ?)`, id.New(), source.ProjectID, sourceID, chapterID, jsonObjectString(edgeMetadata), now); err != nil {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM chapters WHERE id = ?`, chapterID)
		_ = s.Git.RemoveWorktree(ctx, gitPath, worktreePath)
		_ = s.Git.DeleteBranch(ctx, gitPath, branchName)
		return Chapter{}, err
	}
	if project.CopyFiles.Valid && strings.TrimSpace(project.CopyFiles.String) != "" && source.WorktreePath != nil {
		copied, skipped, copyErr := copyConfiguredFiles(*source.WorktreePath, worktreePath, project.CopyFiles.String)
		if copyErr != nil {
			fallback("copyFiles", "copyFiles failed: "+copyErr.Error())
		} else if skipped > 0 {
			fallback("copyFilesPartial", fmt.Sprintf("copyFiles copied %d item(s) and skipped %d invalid or missing item(s).", copied, skipped))
		}
	}
	if s.Narrators != nil && primaryNarratorID != "" {
		_, narratorErr := s.Narrators.ForkNarratorWithOptions(ctx, primaryNarratorID, strings.TrimSpace(input.ForkAtMessageUUID), narrators.ForkNarratorOptions{Title: title, NewChapterID: &chapterID, InheritMode: inheritMode, Cwd: &worktreePath, Locale: input.Locale})
		if narratorErr != nil {
			fallback("narratorContext", "Narrator context fork failed: "+narratorErr.Error())
		} else if inheritMode == "compressed" {
			fallback("compressedContext", "Go backend does not generate an AI context summary for compressed fork yet; it records a fallback summary and bounded parent message refs instead.")
		}
	} else if s.Narrators != nil {
		core := s.Settings.Core()
		_, _ = s.Narrators.Create(ctx, narrators.CreateInput{ChapterID: &chapterID, Title: &title, Model: core.Agent.DefaultModel, PermissionMode: core.Agent.DefaultPermissionMode, Cwd: &worktreePath})
		fallback("narratorContext", "No primary parent narrator was found; created a fresh narrator for the fork.")
	}
	if source.ContainerConfig != nil {
		s.startChapterContainersBackground(chapterID)
	}
	if project.StartupScript.Valid && strings.TrimSpace(project.StartupScript.String) != "" {
		scriptWarnings := runStartupScript(ctx, worktreePath, chapterID, project.StartupScript.String)
		for _, warning := range scriptWarnings {
			fallback("startupScript", warning)
		}
	}
	if len(fallbacks) > 0 {
		forkPoint["fallbacks"] = fallbacks
		edgeMetadata["fallbacks"] = fallbacks
		if _, err := s.DB.ExecContext(ctx, `UPDATE chapters SET fork_point = ?, updated_at = ? WHERE id = ?`, jsonObjectString(forkPoint), db.NowISO(), chapterID); err != nil {
			return Chapter{}, err
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE chapter_edges SET metadata = ? WHERE source_id = ? AND target_id = ? AND type = 'fork'`, jsonObjectString(edgeMetadata), sourceID, chapterID); err != nil {
			return Chapter{}, err
		}
	}
	chapter, err := s.Get(ctx, chapterID)
	if err != nil {
		return Chapter{}, err
	}
	chapter.Warnings = warnings
	chapter.Fallbacks = fallbacks
	return chapter, nil
}

func normalizeInheritMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "full"
	}
	if mode == "full" || mode == "compressed" || mode == "fresh" {
		return mode
	}
	return ""
}

func (s *Service) primaryNarratorID(ctx context.Context, chapterID string) (string, error) {
	var narratorID string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ? AND variant = 'primary' ORDER BY created_at ASC LIMIT 1`, chapterID).Scan(&narratorID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return narratorID, err
}

func (s *Service) resolveForkCommit(ctx context.Context, source Chapter, gitPath, parentCwd, narratorID string, input ForkInput) (string, string, error) {
	if input.StartCommitSHA != nil && strings.TrimSpace(*input.StartCommitSHA) != "" {
		return strings.TrimSpace(*input.StartCommitSHA), "", nil
	}
	headPath := parentCwd
	if strings.TrimSpace(headPath) == "" {
		headPath = gitPath
	}
	fallbackHead := func(message string) (string, string, error) {
		head, err := s.Git.HeadCommit(ctx, headPath)
		if err != nil && headPath != gitPath {
			head, err = s.Git.HeadCommit(ctx, gitPath)
		}
		return head, message, err
	}
	forkMessageUUID := strings.TrimSpace(input.ForkAtMessageUUID)
	if forkMessageUUID == "" {
		return fallbackHead("")
	}
	if narratorID == "" {
		return fallbackHead("No primary narrator was available for forkAtMessageUuid; forked from parent HEAD instead.")
	}
	var messageID string
	var commit sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, commit_sha FROM narrator_messages WHERE sdk_message_uuid = ? OR id = ? ORDER BY created_at DESC LIMIT 1`, forkMessageUUID, forkMessageUUID).Scan(&messageID, &commit)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", httpx.BadRequest("Fork message not found")
	}
	if err != nil {
		return "", "", err
	}
	if commit.Valid && strings.TrimSpace(commit.String) != "" {
		return strings.TrimSpace(commit.String), "", nil
	}
	var seq int
	err = s.DB.QueryRowContext(ctx, `SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ? LIMIT 1`, narratorID, messageID).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", httpx.BadRequest("Fork message not found in parent narrator refs")
	}
	if err != nil {
		return "", "", err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT m.commit_sha FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND r.seq <= ? AND m.commit_sha IS NOT NULL AND m.commit_sha <> '' ORDER BY r.seq DESC LIMIT 1`, narratorID, seq).Scan(&commit)
	if err == nil && commit.Valid && strings.TrimSpace(commit.String) != "" {
		return strings.TrimSpace(commit.String), "", nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	return fallbackHead("No commitSha was recorded at or before forkAtMessageUuid; forked from parent HEAD instead.")
}

type orderedForkToolCall struct {
	ToolName  string
	InputJSON any
}

func (s *Service) restoreFileStatesAtForkMessage(ctx context.Context, narratorID, forkMessageUUID, parentCwd, worktreePath string) (int, int, error) {
	var messageID string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM narrator_messages WHERE sdk_message_uuid = ? OR id = ? ORDER BY created_at DESC LIMIT 1`, forkMessageUUID, forkMessageUUID).Scan(&messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, httpx.BadRequest("Fork message not found")
	}
	if err != nil {
		return 0, 0, err
	}
	var maxSeq int
	if err := s.DB.QueryRowContext(ctx, `SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ? LIMIT 1`, narratorID, messageID).Scan(&maxSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, httpx.BadRequest("Fork message not found in parent narrator refs")
		}
		return 0, 0, err
	}
	states, err := s.rebuildFileStatesUpToSeq(ctx, narratorID, maxSeq)
	if err != nil {
		return 0, 0, err
	}
	applied := 0
	skipped := 0
	for rawPath, content := range states {
		if content == nil {
			skipped++
			continue
		}
		targetPath, ok := forkTargetPath(parentCwd, worktreePath, rawPath)
		if !ok {
			skipped++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return applied, skipped, err
		}
		if err := os.WriteFile(targetPath, []byte(*content), 0o644); err != nil {
			return applied, skipped, err
		}
		applied++
	}
	return applied, skipped, nil
}

func (s *Service) rebuildFileStatesUpToSeq(ctx context.Context, narratorID string, maxSeq int) (map[string]*string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT tc.tool_name, tc.input_json FROM narrator_tool_calls tc JOIN narrator_message_refs r ON r.message_id = tc.message_id AND r.narrator_id = tc.narrator_id WHERE tc.narrator_id = ? AND r.seq <= ? AND tc.status IN ('success', 'completed') AND tc.tool_name IN ('Write', 'Edit') ORDER BY r.seq ASC, tc.created_at ASC`, narratorID, maxSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	callsByFile := map[string][]orderedForkToolCall{}
	for rows.Next() {
		var toolName string
		var inputRaw sql.NullString
		if err := rows.Scan(&toolName, &inputRaw); err != nil {
			return nil, err
		}
		input, _ := jsondb.ObjectOrNil(inputRaw).(map[string]any)
		filePath, _ := input["file_path"].(string)
		if strings.TrimSpace(filePath) == "" {
			continue
		}
		callsByFile[filePath] = append(callsByFile[filePath], orderedForkToolCall{ToolName: toolName, InputJSON: input})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	snapshots := map[string]*string{}
	snapRows, err := s.DB.QueryContext(ctx, `SELECT file_path, original_content FROM narrator_file_snapshots WHERE narrator_id = ?`, narratorID)
	if err != nil {
		return nil, err
	}
	for snapRows.Next() {
		var filePath string
		var original sql.NullString
		if err := snapRows.Scan(&filePath, &original); err != nil {
			snapRows.Close()
			return nil, err
		}
		if original.Valid {
			value := original.String
			snapshots[filePath] = &value
		} else {
			snapshots[filePath] = nil
		}
	}
	if err := snapRows.Close(); err != nil {
		return nil, err
	}
	states := map[string]*string{}
	for filePath, calls := range callsByFile {
		content := snapshots[filePath]
		for _, call := range calls {
			content = applyForkToolCall(content, call)
		}
		states[filePath] = content
	}
	return states, nil
}

func applyForkToolCall(current *string, call orderedForkToolCall) *string {
	input, _ := call.InputJSON.(map[string]any)
	switch call.ToolName {
	case "Write":
		content, ok := input["content"].(string)
		if !ok {
			return current
		}
		return &content
	case "Edit":
		newString, ok := input["new_string"].(string)
		if !ok {
			return current
		}
		oldString, _ := input["old_string"].(string)
		if oldString == "" {
			return &newString
		}
		if current == nil {
			return current
		}
		replaceAll, _ := input["replace_all"].(bool)
		oldNormalized := strings.ReplaceAll(oldString, "\r\n", "\n")
		newNormalized := strings.ReplaceAll(newString, "\r\n", "\n")
		content := strings.ReplaceAll(*current, "\r\n", "\n")
		count := 1
		if replaceAll {
			count = -1
		}
		updated := strings.Replace(content, oldNormalized, newNormalized, count)
		return &updated
	default:
		return current
	}
}

func forkTargetPath(parentCwd, worktreePath, rawPath string) (string, bool) {
	cleanRaw := strings.TrimSpace(rawPath)
	if cleanRaw == "" {
		return "", false
	}
	var rel string
	if filepath.IsAbs(cleanRaw) {
		absParent, err := filepath.Abs(parentCwd)
		if err != nil {
			return "", false
		}
		absRaw, err := filepath.Abs(cleanRaw)
		if err != nil || !isInsidePath(absParent, absRaw) {
			return "", false
		}
		rel, err = filepath.Rel(absParent, absRaw)
		if err != nil {
			return "", false
		}
	} else {
		rel = filepath.Clean(cleanRaw)
	}
	if rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return "", false
	}
	target := filepath.Join(worktreePath, rel)
	absWorktree, err := filepath.Abs(worktreePath)
	if err != nil {
		return "", false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil || !isInsidePath(absWorktree, absTarget) {
		return "", false
	}
	return absTarget, true
}

func copyConfiguredFiles(srcDir, destDir, rawCopyFiles string) (int, int, error) {
	var files []string
	if err := json.Unmarshal([]byte(rawCopyFiles), &files); err != nil {
		return 0, 0, err
	}
	copied := 0
	skipped := 0
	for _, file := range files {
		rel := filepath.Clean(strings.TrimSpace(file))
		if rel == "" || rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			skipped++
			continue
		}
		src := filepath.Join(srcDir, rel)
		dest := filepath.Join(destDir, rel)
		if !isInsidePathMust(srcDir, src) || !isInsidePathMust(destDir, dest) {
			skipped++
			continue
		}
		if err := copyPath(src, dest); err != nil {
			skipped++
			continue
		}
		copied++
	}
	return copied, skipped, nil
}

func copyPath(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(src, path)
			if err != nil {
				return err
			}
			target := filepath.Join(dest, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			return copyFile(path, target)
		})
	}
	return copyFile(src, dest)
}

func copyFile(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func runStartupScript(ctx context.Context, worktreePath, chapterID, script string) []string {
	warnings := []string{}
	if isRiskyStartupScript(script, worktreePath) {
		warnings = append(warnings, "Startup script appears to start NarraFork or a dev server from the forked worktree; this can lock the shared database.")
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", script)
	cmd.Dir = worktreePath
	cmd.Env = append(os.Environ(), "NARRAFORK_CHAPTER_ID="+chapterID, "NARRAFORK_STARTUP_SCRIPT=1")
	output := newLimitedCombinedWriter(containerCommandOutputLimit)
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	if cctx.Err() != nil {
		warnings = append(warnings, "Startup script timed out after 60s; fork completed without waiting for it.")
		return warnings
	}
	if err != nil {
		msg := strings.TrimSpace(output.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		if msg == "" {
			msg = err.Error()
		}
		warnings = append(warnings, "Startup script failed: "+msg)
	}
	return warnings
}

func isRiskyStartupScript(script, worktreePath string) bool {
	normalized := strings.ToLower(script)
	looksLikeDev := regexp.MustCompile(`\b(bun|npm|pnpm|yarn)\s+(run\s+)?dev\b`).MatchString(normalized)
	return strings.Contains(normalized, "narrafork") || (looksLikeDev && strings.Contains(strings.ToLower(worktreePath), "narrafork"))
}

func isInsidePathMust(base, candidate string) bool {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	return isInsidePath(absBase, absCandidate)
}

func isInsidePath(base, candidate string) bool {
	rel, err := filepath.Rel(base, candidate)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

func (s *Service) CreateReview(ctx context.Context, sourceID string, input ReviewInput) (Chapter, error) {
	source, err := s.Get(ctx, sourceID)
	if err != nil {
		return Chapter{}, err
	}
	if source.Status != "active" {
		return Chapter{}, httpx.BadRequest("Can only review active chapters")
	}
	project, err := s.getProject(ctx, source.ProjectID)
	if err != nil {
		return Chapter{}, err
	}
	if !project.GitPath.Valid || project.GitPath.String == "" {
		return Chapter{}, httpx.BadRequest("Project has no git repository configured")
	}
	gitPath := project.GitPath.String
	sourceWorktree := gitPath
	if source.WorktreePath != nil && *source.WorktreePath != "" {
		sourceWorktree = *source.WorktreePath
	}
	sourceHead, err := s.Git.HeadCommit(ctx, sourceWorktree)
	if err != nil {
		return Chapter{}, err
	}
	title := "Review: " + source.Title
	if len(title) > 168 {
		title = title[:168]
	}
	if input.Title != nil && strings.TrimSpace(*input.Title) != "" {
		title = strings.TrimSpace(*input.Title)
	}
	slug := slugify(title)
	short := id.Must(6)
	branchName := fmt.Sprintf("review/%s-%s", slug, short)
	worktreePath := filepath.Join(gitPath, ".worktrees", slug+"-"+short)
	cleanup := reviewCreateCleanup{s: s, gitPath: gitPath, branchName: branchName, worktreePath: worktreePath}
	fail := func(err error) (Chapter, error) {
		if cleanupErr := cleanup.rollback(ctx); cleanupErr != nil {
			return Chapter{}, fmt.Errorf("%w; rollback failed: %v", err, cleanupErr)
		}
		return Chapter{}, err
	}
	if err := s.Git.CreateBranch(ctx, gitPath, branchName, sourceHead); err != nil {
		return Chapter{}, err
	}
	cleanup.branchCreated = true
	if err := s.Git.CreateWorktree(ctx, gitPath, worktreePath, branchName); err != nil {
		return fail(err)
	}
	cleanup.worktreeCreated = true
	_ = s.copyReviewDirtyFiles(ctx, sourceWorktree, worktreePath)
	now := db.NowISO()
	chapterID := id.New()
	cleanup.chapterID = chapterID
	anchor := sourceHead
	if input.AnchorCommitSHA != nil && strings.TrimSpace(*input.AnchorCommitSHA) != "" {
		anchor = strings.TrimSpace(*input.AnchorCommitSHA)
	} else if source.AnchorCommitSHA != nil && *source.AnchorCommitSHA != "" {
		anchor = *source.AnchorCommitSHA
	}
	axis := source.AxisOffset
	if input.AxisOffset != nil {
		axis = *input.AxisOffset
	}
	cross := source.CrossOffset + 100
	if input.CrossOffset != nil {
		cross = *input.CrossOffset
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, parent_chapter_id, fork_point, head_commit_sha, start_commit_sha, anchor_commit_sha, axis_offset, cross_offset, review_source_chapter_id, review_status, is_root, last_accessed_at, created_at, updated_at)
		VALUES (?, ?, ?, 'active', 'review', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'reviewing', 0, ?, ?, ?)`,
		chapterID, source.ProjectID, title, branchName, worktreePath, source.Branch, sourceID,
		jsonObjectString(map[string]any{"commitSha": sourceHead}), nullableString(sourceHead), nullableString(sourceHead), nullableString(anchor),
		axis, cross, sourceID, now, now, now)
	if err != nil {
		return fail(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES (?, ?, ?, ?, 'review', ?, ?)`, id.New(), source.ProjectID, sourceID, chapterID, jsonObjectString(map[string]any{"commitSha": sourceHead}), now); err != nil {
		return fail(err)
	}
	if s.Narrators != nil {
		model, permission := s.reviewDefaultNarratorSettings()
		narrator, err := s.Narrators.Create(ctx, narrators.CreateInput{ChapterID: &chapterID, Title: &title, Model: model, PermissionMode: permission, Cwd: &worktreePath})
		if err != nil {
			return fail(err)
		}
		cleanup.narratorID = narrator.ID
		if err := s.initializeReviewNarrator(ctx, narrator.ID, gitPath, source, sourceHead, input); err != nil {
			return fail(err)
		}
	}
	return s.Get(ctx, chapterID)
}

type reviewCreateCleanup struct {
	s            *Service
	gitPath      string
	branchName   string
	worktreePath string
	chapterID    string
	narratorID   string

	branchCreated   bool
	worktreeCreated bool
}

func (c *reviewCreateCleanup) rollback(ctx context.Context) error {
	if c == nil || c.s == nil {
		return nil
	}
	errs := []error{}
	if c.chapterID != "" || c.narratorID != "" {
		if err := c.rollbackDB(ctx); err != nil {
			errs = append(errs, fmt.Errorf("database cleanup: %w", err))
		}
	}
	if c.gitPath != "" && c.worktreePath != "" {
		if _, statErr := os.Stat(c.worktreePath); statErr == nil {
			if c.worktreeCreated && c.s.Git != nil {
				if err := c.s.Git.RemoveWorktree(ctx, c.gitPath, c.worktreePath); err != nil {
					if removeErr := os.RemoveAll(c.worktreePath); removeErr != nil {
						errs = append(errs, fmt.Errorf("remove worktree %s: %w; remove partial path: %v", c.worktreePath, err, removeErr))
					}
				}
			} else if removeErr := os.RemoveAll(c.worktreePath); removeErr != nil {
				errs = append(errs, fmt.Errorf("remove partial worktree path %s: %w", c.worktreePath, removeErr))
			}
		} else if !os.IsNotExist(statErr) {
			errs = append(errs, fmt.Errorf("stat worktree %s: %w", c.worktreePath, statErr))
		}
	}
	if c.branchCreated && c.s.Git != nil && c.gitPath != "" && c.branchName != "" {
		if c.s.Git.BranchExists(ctx, c.gitPath, c.branchName) {
			if err := c.s.Git.DeleteBranch(ctx, c.gitPath, c.branchName); err != nil {
				errs = append(errs, fmt.Errorf("delete branch %s: %w", c.branchName, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (c *reviewCreateCleanup) rollbackDB(ctx context.Context) error {
	if c.s.DB == nil {
		return nil
	}
	tx, err := c.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	narratorIDs, err := c.rollbackNarratorIDs(ctx, tx)
	if err != nil {
		return err
	}
	for _, narratorID := range narratorIDs {
		for _, table := range []string{"narrator_tool_calls", "narrator_sidecars", "narrator_goals"} {
			if err := reviewRollbackDeleteWhere(ctx, tx, table, "narrator_id", narratorID); err != nil {
				return err
			}
		}
		if err := reviewRollbackDeleteWhere(ctx, tx, "narrator_message_refs", "narrator_id", narratorID); err != nil {
			return err
		}
		if err := reviewRollbackDeleteWhere(ctx, tx, "narrator_messages", "narrator_id", narratorID); err != nil {
			return err
		}
		if err := reviewRollbackDeleteWhere(ctx, tx, "narrators", "id", narratorID); err != nil {
			return err
		}
	}
	if c.chapterID != "" {
		if err := reviewRollbackDeleteChapterEdges(ctx, tx, c.chapterID); err != nil {
			return err
		}
		if err := reviewRollbackDeleteWhere(ctx, tx, "chapters", "id", c.chapterID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (c *reviewCreateCleanup) rollbackNarratorIDs(ctx context.Context, tx *sql.Tx) ([]string, error) {
	columns, err := db.TableColumnSet(ctx, tx, "narrators")
	if err != nil || len(columns) == 0 {
		return nil, err
	}
	clauses := []string{}
	args := []any{}
	if c.narratorID != "" && columns["id"] {
		clauses = append(clauses, "id = ?")
		args = append(args, c.narratorID)
	}
	if c.chapterID != "" && columns["chapter_id"] {
		clauses = append(clauses, "chapter_id = ?")
		args = append(args, c.chapterID)
	}
	if len(clauses) == 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM narrators WHERE `+strings.Join(clauses, " OR "), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var idValue string
		if err := rows.Scan(&idValue); err != nil {
			return nil, err
		}
		if !seen[idValue] {
			seen[idValue] = true
			ids = append(ids, idValue)
		}
	}
	return ids, rows.Err()
}

func reviewRollbackDeleteWhere(ctx context.Context, tx *sql.Tx, table, column, value string) error {
	columns, err := db.TableColumnSet(ctx, tx, table)
	if err != nil || len(columns) == 0 || !columns[column] {
		return err
	}
	dialect := db.SQLiteDialect{}
	_, err = tx.ExecContext(ctx, `DELETE FROM `+dialect.QuoteIdent(table)+` WHERE `+dialect.QuoteIdent(column)+` = ?`, value)
	return err
}

func reviewRollbackDeleteChapterEdges(ctx context.Context, tx *sql.Tx, chapterID string) error {
	columns, err := db.TableColumnSet(ctx, tx, "chapter_edges")
	if err != nil || len(columns) == 0 {
		return err
	}
	if !columns["source_id"] || !columns["target_id"] {
		return nil
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM chapter_edges WHERE source_id = ? OR target_id = ?`, chapterID, chapterID)
	return err
}

func (s *Service) reviewDefaultNarratorSettings() (string, string) {
	if s != nil && s.Settings != nil {
		core := s.Settings.Core()
		return firstNonEmptyString(core.Agent.DefaultModel, config.DefaultAgentModel), firstNonEmptyString(core.Agent.DefaultPermissionMode, "default")
	}
	return config.DefaultAgentModel, "default"
}

func (s *Service) initializeReviewNarrator(ctx context.Context, narratorID, gitPath string, source Chapter, sourceHead string, input ReviewInput) error {
	diffContext := s.buildReviewDiffContext(ctx, gitPath, source, sourceHead)
	prompt := buildReviewSystemPrompt(diffContext, input.Locale)
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrators SET system_prompt = ?, updated_at = ? WHERE id = ?`, prompt, db.NowISO(), narratorID); err != nil {
		return fmt.Errorf("initialize review narrator system prompt: %w", err)
	}
	if input.UserID != "" {
		if _, err := s.Narrators.AddUserMessage(ctx, narratorID, input.UserID, reviewStartMessage(input.Locale)); err != nil {
			return fmt.Errorf("initialize review narrator start message: %w", err)
		}
	}
	return nil
}

func (s *Service) BatchMerge(ctx context.Context, input BatchMergeInput) (BatchMergeResult, error) {
	if strings.TrimSpace(input.BaseChapterID) == "" {
		return BatchMergeResult{}, httpx.BadRequest("baseChapterId is required")
	}
	sourceIDs := dedupeNonEmpty(input.SourceChapterIDs)
	if len(sourceIDs) == 0 {
		return BatchMergeResult{}, httpx.BadRequest("sourceChapterIds is required")
	}
	strategy := strings.TrimSpace(input.Strategy)
	if strategy == "" {
		strategy = "merge"
	}
	if strategy != "merge" && strategy != "squash" && strategy != "cherry-pick" {
		return BatchMergeResult{}, httpx.BadRequest("strategy must be merge, squash, or cherry-pick")
	}
	base, err := s.Get(ctx, input.BaseChapterID)
	if err != nil {
		return BatchMergeResult{}, err
	}
	targetID := strings.TrimSpace(input.TargetChapterID)
	createdTarget := false
	if targetID == "" {
		title := strings.TrimSpace(input.Title)
		if title == "" {
			title = "Batch merge: " + base.Title
		}
		target, err := s.Fork(ctx, input.BaseChapterID, ForkInput{Title: &title})
		if err != nil {
			return BatchMergeResult{}, err
		}
		targetID = target.ID
		createdTarget = true
	} else {
		target, err := s.Get(ctx, targetID)
		if err != nil {
			return BatchMergeResult{}, err
		}
		if target.ProjectID != base.ProjectID {
			return BatchMergeResult{}, httpx.BadRequest("Target chapter must be in the same project as the base chapter")
		}
		if target.Status != "active" {
			return BatchMergeResult{}, httpx.BadRequest("Target chapter must be active")
		}
		if target.WorktreePath == nil || strings.TrimSpace(*target.WorktreePath) == "" {
			return BatchMergeResult{}, httpx.BadRequest("Target chapter has no worktree")
		}
	}
	for _, sourceID := range sourceIDs {
		if sourceID == targetID {
			return BatchMergeResult{}, httpx.BadRequest("cannot merge target chapter into itself")
		}
	}

	sourceJSON, err := json.Marshal(sourceIDs)
	if err != nil {
		return BatchMergeResult{}, err
	}
	now := db.NowISO()
	mergeSessionID := id.New()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO merge_sessions (id, target_chapter_id, source_chapter_ids, strategy, status, current_index, merged_count, locale, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'running', 0, 0, ?, ?, ?)`, mergeSessionID, targetID, string(sourceJSON), strategy, nullableString(input.Locale), now, now)
	if err != nil {
		return BatchMergeResult{}, err
	}

	s.broadcastMergeProgress(map[string]any{
		"type":             "merge:started",
		"mergeSessionId":   mergeSessionID,
		"projectId":        base.ProjectID,
		"targetChapterId":  targetID,
		"sourceChapterIds": sourceIDs,
	})
	go s.processBatchMergeQueue(mergeSessionID, base.ProjectID, targetID, sourceIDs, strategy, createdTarget, input.Locale, input.UserID)

	return BatchMergeResult{MergeSessionID: mergeSessionID, TargetChapterID: targetID, CreatedTarget: createdTarget, Status: "running"}, nil
}

func (s *Service) processBatchMergeQueue(mergeSessionID, projectID, targetID string, sourceIDs []string, strategy string, createdTarget bool, locale, userID string) {
	ctx := context.Background()
	total := len(sourceIDs)
	mergedCount := 0
	currentSourceID := ""
	for i, sourceID := range sourceIDs {
		currentSourceID = sourceID
		if err := s.updateMergeSession(ctx, mergeSessionID, map[string]any{
			"status":                    "running",
			"current_index":             i,
			"current_source_chapter_id": sourceID,
			"error":                     nil,
		}); err != nil {
			s.failBatchMerge(ctx, mergeSessionID, projectID, targetID, currentSourceID, err.Error(), createdTarget)
			return
		}

		result, err := s.Merge(ctx, sourceID, targetID, strategy, "", userID)
		if err != nil {
			s.failBatchMerge(ctx, mergeSessionID, projectID, targetID, currentSourceID, err.Error(), createdTarget)
			return
		}
		if ok, _ := result["ok"].(bool); ok {
			mergedCount++
			_ = s.updateMergeSession(ctx, mergeSessionID, map[string]any{
				"merged_count":   mergedCount,
				"conflict_files": nil,
			})
			s.broadcastMergeProgress(map[string]any{
				"type":            "merge:step_ok",
				"mergeSessionId":  mergeSessionID,
				"projectId":       projectID,
				"sourceChapterId": sourceID,
				"index":           i,
				"total":           total,
				"commitSha":       firstNonEmptyAny(result["mergeCommitSha"], result["commitSha"]),
			})
			continue
		}

		conflictFiles := stringSliceFromAny(result["conflictFiles"])
		if len(conflictFiles) == 0 {
			conflictFiles = []string{}
		}
		if err := s.updateMergeSession(ctx, mergeSessionID, map[string]any{
			"status":         "waiting_decision",
			"conflict_files": conflictFiles,
		}); err != nil {
			s.failBatchMerge(ctx, mergeSessionID, projectID, targetID, currentSourceID, err.Error(), createdTarget)
			return
		}
		s.broadcastMergeProgress(map[string]any{
			"type":            "merge:conflict",
			"mergeSessionId":  mergeSessionID,
			"projectId":       projectID,
			"sourceChapterId": sourceID,
			"index":           i,
			"total":           total,
			"conflictFiles":   conflictFiles,
		})

		decision := s.waitForMergeDecision(ctx, mergeSessionID)
		if decision != "continue" {
			reason := "User cancelled on conflict"
			if decision == "timeout" {
				reason = "Decision timeout"
			}
			s.broadcastMergeProgress(map[string]any{"type": "merge:cancelled", "mergeSessionId": mergeSessionID, "projectId": projectID, "reason": reason})
			s.rollbackBatchMerge(ctx, mergeSessionID, targetID, reason, createdTarget)
			return
		}

		_ = s.updateMergeSession(ctx, mergeSessionID, map[string]any{"status": "ai_resolving"})
		s.broadcastMergeProgress(map[string]any{"type": "merge:ai_resolving", "mergeSessionId": mergeSessionID, "projectId": projectID, "sourceChapterId": sourceID})
		aiResult, err := s.AIResolve(ctx, sourceID, targetID, strategy, "", userID, locale)
		if err != nil {
			s.failBatchMerge(ctx, mergeSessionID, projectID, targetID, currentSourceID, err.Error(), createdTarget)
			return
		}
		if resolved, _ := aiResult["resolved"].(bool); !resolved {
			reason := stringValueOr(aiResult["error"], "AI resolution failed")
			s.broadcastMergeProgress(map[string]any{"type": "merge:error", "mergeSessionId": mergeSessionID, "projectId": projectID, "sourceChapterId": sourceID, "error": reason})
			s.broadcastMergeProgress(map[string]any{"type": "merge:cancelled", "mergeSessionId": mergeSessionID, "projectId": projectID, "reason": reason})
			s.rollbackBatchMerge(ctx, mergeSessionID, targetID, reason, createdTarget)
			return
		}
		mergedCount++
		_ = s.updateMergeSession(ctx, mergeSessionID, map[string]any{
			"status":         "running",
			"merged_count":   mergedCount,
			"conflict_files": nil,
		})
		s.broadcastMergeProgress(map[string]any{
			"type":            "merge:step_ok",
			"mergeSessionId":  mergeSessionID,
			"projectId":       projectID,
			"sourceChapterId": sourceID,
			"index":           i,
			"total":           total,
			"commitSha":       mergeCommitFromAIResult(aiResult),
		})
	}

	if err := s.updateMergeSession(ctx, mergeSessionID, map[string]any{
		"status":                    "completed",
		"current_index":             total,
		"merged_count":              mergedCount,
		"current_source_chapter_id": nil,
		"conflict_files":            nil,
		"error":                     nil,
	}); err != nil {
		s.failBatchMerge(ctx, mergeSessionID, projectID, targetID, currentSourceID, err.Error(), createdTarget)
		return
	}
	s.broadcastMergeProgress(map[string]any{"type": "merge:completed", "mergeSessionId": mergeSessionID, "projectId": projectID, "targetChapterId": targetID, "mergedCount": mergedCount})
}

func (s *Service) updateMergeSession(ctx context.Context, mergeSessionID string, fields map[string]any) error {
	sets := make([]string, 0, len(fields)+1)
	args := make([]any, 0, len(fields)+2)
	for column, value := range fields {
		sets = append(sets, column+" = ?")
		switch column {
		case "conflict_files":
			if value == nil {
				args = append(args, nil)
			} else {
				encoded, err := json.Marshal(value)
				if err != nil {
					return err
				}
				args = append(args, string(encoded))
			}
		default:
			args = append(args, value)
		}
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, db.NowISO(), mergeSessionID)
	_, err := s.DB.ExecContext(ctx, `UPDATE merge_sessions SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

func (s *Service) waitForMergeDecision(ctx context.Context, mergeSessionID string) string {
	timeout := s.MergeDecisionTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	interval := s.MergeDecisionInterval
	if interval <= 0 {
		interval = time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		var status string
		err := s.DB.QueryRowContext(ctx, `SELECT status FROM merge_sessions WHERE id = ?`, mergeSessionID).Scan(&status)
		if err != nil {
			return "cancel"
		}
		switch status {
		case "running":
			return "continue"
		case "cancelled", "error", "completed":
			return "cancel"
		}
		if time.Now().After(deadline) {
			_ = s.updateMergeSession(ctx, mergeSessionID, map[string]any{"status": "cancelled", "error": "Decision timeout"})
			return "timeout"
		}
		select {
		case <-ctx.Done():
			return "cancel"
		case <-time.After(interval):
		}
	}
}

func (s *Service) failBatchMerge(ctx context.Context, mergeSessionID, projectID, targetID, sourceID, reason string, createdTarget bool) {
	slog.Error("Batch merge failed", "mergeSessionId", mergeSessionID, "sourceChapterId", sourceID, "error", reason)
	s.broadcastMergeProgress(map[string]any{"type": "merge:error", "mergeSessionId": mergeSessionID, "projectId": projectID, "sourceChapterId": sourceID, "error": reason})
	s.rollbackBatchMerge(ctx, mergeSessionID, targetID, reason, createdTarget)
}

func (s *Service) rollbackBatchMerge(ctx context.Context, mergeSessionID, targetID, reason string, createdTarget bool) {
	if createdTarget {
		if err := s.Delete(ctx, targetID); err != nil {
			slog.Error("Failed to rollback batch merge target", "mergeSessionId", mergeSessionID, "targetChapterId", targetID, "error", err)
		}
	}
	_ = s.updateMergeSession(ctx, mergeSessionID, map[string]any{"status": "error", "error": reason})
}

func (s *Service) broadcastMergeProgress(payload map[string]any) {
	if s.BroadcastMergeProgress != nil {
		s.BroadcastMergeProgress(payload)
	}
}

func dedupeNonEmpty(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func (s *Service) List(ctx context.Context, projectID string) ([]Chapter, error) {
	query := `SELECT ` + chapterSelectColumns + ` FROM chapters WHERE 1=1`
	args := []any{}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY created_at ASC`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chapters := []Chapter{}
	for rows.Next() {
		ch, err := scanChapter(rows)
		if err != nil {
			return nil, err
		}
		chapters = append(chapters, ch)
	}
	return chapters, rows.Err()
}

func (s *Service) Get(ctx context.Context, chapterID string) (Chapter, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+chapterSelectColumns+` FROM chapters WHERE id = ?`, chapterID)
	ch, err := scanChapter(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Chapter{}, httpx.NotFound("Chapter")
	}
	return ch, err
}

func (s *Service) Update(ctx context.Context, chapterID string, fields map[string]any) (Chapter, error) {
	existing, err := s.Get(ctx, chapterID)
	if err != nil {
		return Chapter{}, err
	}
	if _, ok := fields["status"]; ok && existing.IsRoot {
		return Chapter{}, httpx.ValidationError("Cannot change root chapter status")
	}
	if _, ok := fields["role"]; ok && existing.IsRoot {
		return Chapter{}, httpx.ValidationError("Cannot change root chapter role")
	}
	if role, ok := fields["role"]; ok && strings.TrimSpace(fmt.Sprint(role)) == "trunk" && !existing.IsRoot {
		return Chapter{}, httpx.ValidationError("Trunk role is reserved for the root chapter")
	}

	allowed := map[string]string{"title": "title", "description": "description", "status": "status", "role": "role", "color": "color", "groupLabel": "group_label", "mergedIntoChapterId": "merged_into_chapter_id", "mergeCommitSha": "merge_commit_sha", "mergeStrategy": "merge_strategy"}
	sets := []string{}
	args := []any{}
	for key, val := range fields {
		if key == "containerConfig" {
			sets = append(sets, "container_config = ?")
			args = append(args, nullableJSON(val))
			continue
		}
		col, ok := allowed[key]
		if !ok {
			continue
		}
		sets = append(sets, col+" = ?")
		args = append(args, val)
	}
	if len(sets) == 0 {
		return existing, nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, db.NowISO(), chapterID)
	_, err = s.DB.ExecContext(ctx, `UPDATE chapters SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return Chapter{}, err
	}
	return s.Get(ctx, chapterID)
}

func (s *Service) Delete(ctx context.Context, chapterID string) error {
	ch, err := s.Get(ctx, chapterID)
	if err != nil {
		return err
	}
	if ch.IsRoot {
		return httpx.BadRequest("Cannot delete root chapter")
	}
	if err := s.cleanupChapterResources(ctx, ch, true); err != nil {
		return err
	}
	now := db.NowISO()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := archiveChapterNarrators(ctx, tx, chapterID, now); err != nil {
		return fmt.Errorf("archive chapter narrators: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chapters SET parent_chapter_id = NULL WHERE parent_chapter_id = ?`, chapterID); err != nil {
		return fmt.Errorf("clear child chapter parent references: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chapters SET merged_into_chapter_id = NULL WHERE merged_into_chapter_id = ?`, chapterID); err != nil {
		return fmt.Errorf("clear chapter merge references: %w", err)
	}
	if err := cleanupChapterRecentTabs(ctx, tx, chapterID); err != nil {
		return fmt.Errorf("cleanup chapter recent tabs: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM chapters WHERE id = ?`, chapterID); err != nil {
		return err
	}
	return tx.Commit()
}

func archiveChapterNarrators(ctx context.Context, tx *sql.Tx, chapterID, now string) error {
	columns, err := db.TableColumnSet(ctx, tx, "narrators")
	if err != nil || len(columns) == 0 {
		return err
	}
	if !columns["chapter_id"] {
		return nil
	}
	sets := []string{"chapter_id = NULL"}
	args := []any{}
	if columns["status"] {
		sets = append(sets, "status = 'archived'")
	}
	if columns["updated_at"] {
		sets = append(sets, "updated_at = ?")
		args = append(args, now)
	}
	args = append(args, chapterID)
	_, err = tx.ExecContext(ctx, `UPDATE narrators SET `+strings.Join(sets, ", ")+` WHERE chapter_id = ?`, args...)
	return err
}

func cleanupChapterRecentTabs(ctx context.Context, tx *sql.Tx, chapterID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT user_id, recent_tabs FROM user_preferences`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type update struct{ userID, tabs string }
	updates := []update{}
	for rows.Next() {
		var userID, rawTabs string
		if err := rows.Scan(&userID, &rawTabs); err != nil {
			return err
		}
		var tabs []map[string]any
		if json.Unmarshal([]byte(rawTabs), &tabs) != nil {
			continue
		}
		filtered := make([]map[string]any, 0, len(tabs))
		changed := false
		for _, tab := range tabs {
			if chapterRecentTabReferences(tab, chapterID) {
				changed = true
				continue
			}
			filtered = append(filtered, tab)
		}
		if changed {
			data, _ := json.Marshal(filtered)
			updates = append(updates, update{userID: userID, tabs: string(data)})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE user_preferences SET recent_tabs = ?, updated_at = ? WHERE user_id = ?`, item.tabs, db.NowISO(), item.userID); err != nil {
			return err
		}
	}
	return nil
}

func chapterRecentTabReferences(tab map[string]any, chapterID string) bool {
	return (tab["type"] == "chapter" && tab["id"] == chapterID) || tab["chapterId"] == chapterID
}

func (s *Service) Dormant(ctx context.Context, chapterID string) error {
	return withChapterContainerLock(chapterID, func() error {
		ch, err := s.Get(ctx, chapterID)
		if err != nil {
			return err
		}
		if ch.IsRoot {
			return httpx.BadRequest("Cannot make root chapter dormant")
		}
		if ch.Status != "active" {
			return httpx.BadRequest("Can only make active chapters dormant")
		}
		if ch.WorktreePath == nil || strings.TrimSpace(*ch.WorktreePath) == "" {
			return httpx.BadRequest("Chapter has no worktree")
		}
		project, err := s.getProject(ctx, ch.ProjectID)
		if err != nil {
			return err
		}
		if !project.GitPath.Valid || strings.TrimSpace(project.GitPath.String) == "" {
			return httpx.BadRequest("Project has no git repository configured")
		}
		if err := s.killAndDeleteChapterTerminals(ctx, chapterID); err != nil {
			return err
		}
		if ch.ContainerConfig != nil {
			paused, err := s.pauseChapterContainersLocked(ctx, ch)
			if err != nil {
				s.broadcastContainerLifecycle(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
			} else if paused {
				s.broadcastContainerLifecycle(map[string]any{"type": "container:paused", "chapterId": chapterID})
			}
		}
		if _, err := s.Git.AutoCommit(ctx, *ch.WorktreePath, "auto-save before dormant"); err != nil {
			s.Git.AbortMerge(ctx, *ch.WorktreePath)
			_, _ = s.Git.AutoCommit(ctx, *ch.WorktreePath, "auto-save before dormant (after merge abort)")
		}
		if safeWorktreePath(project.GitPath.String, *ch.WorktreePath) {
			_ = s.Git.RemoveWorktree(ctx, project.GitPath.String, *ch.WorktreePath)
		}
		_, err = s.DB.ExecContext(ctx, `UPDATE chapters SET status = 'dormant', worktree_path = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), chapterID)
		if err == nil {
			s.broadcastContainerLifecycle(map[string]any{"type": "chapter:dormant", "chapterId": chapterID, "projectId": ch.ProjectID})
		}
		return err
	})
}

func (s *Service) Wake(ctx context.Context, chapterID string) error {
	return withChapterContainerLock(chapterID, func() error {
		ch, err := s.Get(ctx, chapterID)
		if err != nil {
			return err
		}
		if ch.Status != "dormant" && ch.Status != "merged" {
			return httpx.BadRequest("Can only wake dormant or merged chapters")
		}
		project, err := s.getProject(ctx, ch.ProjectID)
		if err != nil {
			return err
		}
		if !project.GitPath.Valid || strings.TrimSpace(project.GitPath.String) == "" {
			return httpx.BadRequest("Project has no git repository configured")
		}
		branchSuffix := strings.Join(strings.Split(ch.Branch, "/")[1:], "/")
		if strings.TrimSpace(branchSuffix) == "" {
			branchSuffix = slugify(ch.Title) + "-" + id.Must(6)
		}
		worktreePath := filepath.Join(project.GitPath.String, ".worktrees", branchSuffix)
		if err := s.Git.CreateWorktree(ctx, project.GitPath.String, worktreePath, ch.Branch); err != nil {
			return err
		}
		now := db.NowISO()
		if ch.Status == "merged" {
			tx, err := s.DB.BeginTx(ctx, nil)
			if err != nil {
				_ = s.Git.RemoveWorktree(ctx, project.GitPath.String, worktreePath)
				return err
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, `UPDATE chapters SET status = 'active', worktree_path = ?, merged_into_chapter_id = NULL, merge_commit_sha = NULL, merge_strategy = NULL, pre_merge_target_sha = NULL, last_accessed_at = ?, updated_at = ? WHERE id = ?`, worktreePath, now, now, chapterID); err != nil {
				_ = s.Git.RemoveWorktree(ctx, project.GitPath.String, worktreePath)
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM chapter_edges WHERE source_id = ? AND type = 'merge'`, chapterID); err != nil {
				_ = s.Git.RemoveWorktree(ctx, project.GitPath.String, worktreePath)
				return err
			}
			if err := tx.Commit(); err != nil {
				_ = s.Git.RemoveWorktree(ctx, project.GitPath.String, worktreePath)
				return err
			}
		} else if _, err := s.DB.ExecContext(ctx, `UPDATE chapters SET status = 'active', worktree_path = ?, last_accessed_at = ?, updated_at = ? WHERE id = ?`, worktreePath, now, now, chapterID); err != nil {
			_ = s.Git.RemoveWorktree(ctx, project.GitPath.String, worktreePath)
			return err
		}
		if ch.ContainerConfig != nil {
			eventType, err := s.unpauseOrStartChapterContainersLocked(ctx, chapterID)
			if err != nil {
				s.broadcastContainerLifecycle(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
			} else if eventType != "" {
				if eventType == "container:started" {
					_ = s.handleContainerReadyNarratorIntegration(ctx, chapterID)
				}
				s.broadcastContainerLifecycle(map[string]any{"type": eventType, "chapterId": chapterID})
			}
		}
		s.broadcastContainerLifecycle(map[string]any{"type": "chapter:woken", "chapterId": chapterID, "projectId": ch.ProjectID})
		return nil
	})
}

func (s *Service) BatchCleanup(ctx context.Context, chapterIDs []string, force, deleteBranch bool) (CleanupReport, error) {
	report := CleanupReport{Cleaned: []string{}, Skipped: []string{}, Errors: []CleanupError{}}
	for _, chapterID := range chapterIDs {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		ch, err := s.Get(ctx, chapterID)
		if err != nil {
			report.Errors = append(report.Errors, CleanupError{ChapterID: chapterID, Error: err.Error()})
			continue
		}
		if ch.IsRoot || ch.Status == "merged" || ch.Status == "abandoned" {
			report.Skipped = append(report.Skipped, chapterID)
			continue
		}
		if ch.WorktreePath != nil && !force {
			status, err := s.Git.Status(ctx, *ch.WorktreePath)
			if err != nil {
				report.Errors = append(report.Errors, CleanupError{ChapterID: chapterID, Error: err.Error()})
				continue
			}
			if !status.Clean {
				report.Skipped = append(report.Skipped, chapterID)
				continue
			}
		}
		if err := s.cleanupChapterResources(ctx, ch, deleteBranch); err != nil {
			report.Errors = append(report.Errors, CleanupError{ChapterID: chapterID, Error: err.Error()})
			continue
		}
		report.Cleaned = append(report.Cleaned, chapterID)
	}
	return report, nil
}

func (s *Service) killAndDeleteChapterTerminals(ctx context.Context, chapterID string) error {
	if s.TerminalRuntime != nil {
		rows, err := s.DB.QueryContext(ctx, `SELECT id FROM terminals WHERE chapter_id = ?`, chapterID)
		if err != nil {
			return err
		}
		terminalIDs := []string{}
		for rows.Next() {
			var terminalID string
			if err := rows.Scan(&terminalID); err != nil {
				rows.Close()
				return err
			}
			terminalIDs = append(terminalIDs, terminalID)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, terminalID := range terminalIDs {
			_ = s.TerminalRuntime.Kill(ctx, terminalID)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM terminal_tabs WHERE chapter_id = ?`, chapterID); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM terminal_view_state WHERE chapter_id = ?`, chapterID); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM terminals WHERE chapter_id = ?`, chapterID); err != nil {
		return err
	}
	return nil
}

func (s *Service) cleanupChapterResources(ctx context.Context, ch Chapter, deleteBranch bool) error {
	return withChapterContainerLock(ch.ID, func() error {
		return s.cleanupChapterResourcesLocked(ctx, ch, deleteBranch)
	})
}

func (s *Service) cleanupChapterResourcesLocked(ctx context.Context, ch Chapter, deleteBranch bool) error {
	chapterID := ch.ID
	if err := s.killAndDeleteChapterTerminals(ctx, chapterID); err != nil {
		return err
	}
	if err := s.removeChapterContainersLocked(ctx, ch, deleteBranch); err != nil {
		return err
	}
	s.broadcastContainerLifecycle(map[string]any{"type": "container:stopped", "chapterId": chapterID})
	project, err := s.getProject(ctx, ch.ProjectID)
	if err == nil && project.GitPath.Valid && s.Git != nil {
		if ch.WorktreePath != nil && safeWorktreePath(project.GitPath.String, *ch.WorktreePath) {
			_ = s.Git.RemoveWorktree(ctx, project.GitPath.String, *ch.WorktreePath)
		}
		if deleteBranch && ch.Branch != "" {
			_ = s.Git.DeleteBranch(ctx, project.GitPath.String, ch.Branch)
		}
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE chapters SET status = 'abandoned', worktree_path = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), chapterID)
	if err == nil {
		s.broadcastContainerLifecycle(map[string]any{"type": "chapter:abandoned", "chapterId": chapterID, "projectId": ch.ProjectID})
	}
	return err
}

func (s *Service) MergeCheck(ctx context.Context, sourceID, targetID string) (map[string]any, error) {
	source, target, err := s.mergePair(ctx, sourceID, targetID)
	if err != nil {
		return nil, err
	}
	if err := validateMergePair(source, target, false); err != nil {
		return nil, err
	}
	project, err := s.getProject(ctx, source.ProjectID)
	if err != nil {
		return nil, err
	}
	if !project.GitPath.Valid || strings.TrimSpace(project.GitPath.String) == "" {
		return nil, httpx.BadRequest("Project has no git repository configured")
	}
	baseSHA, err := s.Git.GetMergeBase(ctx, project.GitPath.String, target.Branch, source.Branch)
	if err != nil {
		return nil, err
	}
	hasConflicts, conflictFiles, out, err := s.Git.MergeTree(ctx, project.GitPath.String, baseSHA, target.Branch, source.Branch)
	if err != nil {
		return nil, err
	}
	isFastForward := !hasConflicts && s.Git.IsAncestor(ctx, project.GitPath.String, target.Branch, source.Branch)
	return map[string]any{
		"ok":            !hasConflicts,
		"canMerge":      !hasConflicts,
		"hasConflicts":  hasConflicts,
		"conflictFiles": conflictFiles,
		"sourceBranch":  source.Branch,
		"targetBranch":  target.Branch,
		"isFastForward": isFastForward,
		"output":        out,
	}, nil
}

func (s *Service) Merge(ctx context.Context, sourceID, targetID, strategy, message, userID string) (map[string]any, error) {
	strategy = stringOr(strategy, "merge")
	if strategy != "merge" && strategy != "squash" && strategy != "cherry-pick" {
		return nil, httpx.BadRequest("strategy must be merge, squash, or cherry-pick")
	}
	source, target, err := s.mergePair(ctx, sourceID, targetID)
	if err != nil {
		return nil, err
	}
	if err := validateMergePair(source, target, true); err != nil {
		return nil, err
	}
	project, err := s.getProject(ctx, source.ProjectID)
	if err != nil {
		return nil, err
	}
	if !project.GitPath.Valid || strings.TrimSpace(project.GitPath.String) == "" {
		return nil, httpx.BadRequest("Project has no git path")
	}
	requireReview, err := s.reviewRequiredBeforeMerge(ctx, source.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.prepareSourceBeforeMerge(ctx, source, target, project.GitPath.String, requireReview); err != nil {
		return nil, err
	}
	if strings.TrimSpace(message) == "" {
		message = fmt.Sprintf("Merge %s into %s", source.Branch, target.Branch)
	}

	targetWorktree := *target.WorktreePath
	var response map[string]any
	lockErr := s.Git.WithWorktreeLock(targetWorktree, func() error {
		status, err := s.Git.Status(ctx, targetWorktree)
		if err != nil {
			return err
		}
		if !status.Clean {
			return mergeDirtyValidationError("target")
		}
		preMergeHead, err := s.Git.HeadCommit(ctx, targetWorktree)
		if err != nil {
			return err
		}

		var result gitx.MergeResult
		if strategy == "cherry-pick" {
			baseSHA, err := s.Git.GetMergeBase(ctx, project.GitPath.String, target.Branch, source.Branch)
			if err != nil {
				return err
			}
			result, err = s.Git.CherryPick(ctx, targetWorktree, project.GitPath.String, source.Branch, baseSHA)
		} else {
			fastForward := strategy == "merge" && s.Git.IsAncestor(ctx, project.GitPath.String, target.Branch, source.Branch)
			result, err = s.Git.MergeWithStrategy(ctx, targetWorktree, source.Branch, strategy, message, fastForward)
		}
		if err != nil {
			if strategy != "cherry-pick" {
				s.Git.AbortMerge(ctx, targetWorktree)
			}
			response = map[string]any{"ok": false, "success": false, "hasConflicts": true, "output": result.Output, "error": err.Error()}
			return nil
		}
		if !result.Success {
			if strategy != "cherry-pick" {
				s.Git.AbortMerge(ctx, targetWorktree)
			}
			response = map[string]any{"ok": false, "success": false, "canMerge": false, "hasConflicts": true, "conflictFiles": result.ConflictFiles, "output": result.Output}
			return nil
		}
		head := result.CommitSHA
		if head == "" {
			head, _ = s.Git.HeadCommit(ctx, targetWorktree)
		}
		if err := s.recordMerged(ctx, source, target, strategy, head, preMergeHead, message, userID); err != nil {
			if retryErr := s.recordMerged(ctx, source, target, strategy, head, preMergeHead, message, userID); retryErr != nil {
				if resetErr := s.Git.Reset(ctx, targetWorktree, preMergeHead, "hard"); resetErr != nil {
					response = map[string]any{"ok": true, "success": true, "sourceChapterId": source.ID, "targetChapterId": target.ID, "mergeCommitSha": head, "output": result.Output, "warning": fmt.Sprintf("CRITICAL: Git merge succeeded but database update failed, and git rollback also failed. Manual intervention required. DB error: %v", retryErr)}
					return nil
				}
				response = map[string]any{"ok": false, "success": false, "warning": fmt.Sprintf("Merge rolled back: database update failed after git merge. Please retry. (%v)", retryErr)}
				return nil
			}
		}
		response = map[string]any{"ok": true, "success": true, "sourceChapterId": source.ID, "targetChapterId": target.ID, "mergeCommitSha": head, "commitSha": head, "isFastForward": result.IsFastForward, "output": result.Output}
		return nil
	})
	if lockErr != nil {
		return nil, lockErr
	}
	return response, nil
}

func (s *Service) AIResolve(ctx context.Context, sourceID, targetID, strategy, message, userID, locale string) (map[string]any, error) {
	strategy = stringOr(strategy, "merge")
	if strategy != "merge" && strategy != "squash" && strategy != "cherry-pick" {
		return nil, httpx.BadRequest("strategy must be merge, squash, or cherry-pick")
	}
	if strategy == "cherry-pick" {
		result, err := s.Merge(ctx, sourceID, targetID, strategy, message, userID)
		if err != nil {
			return nil, err
		}
		if ok, _ := result["ok"].(bool); ok {
			return map[string]any{"resolved": true, "mergeResult": result}, nil
		}
		return map[string]any{"resolved": false, "error": "Go basic AI resolver cannot continue a multi-commit cherry-pick conflict yet", "mergeResult": result}, nil
	}
	source, target, err := s.mergePair(ctx, sourceID, targetID)
	if err != nil {
		return nil, err
	}
	if err := validateMergePair(source, target, true); err != nil {
		return nil, err
	}
	project, err := s.getProject(ctx, source.ProjectID)
	if err != nil {
		return nil, err
	}
	if !project.GitPath.Valid || strings.TrimSpace(project.GitPath.String) == "" {
		return nil, httpx.BadRequest("Project has no git path")
	}
	if message == "" {
		message = fmt.Sprintf("Merge %s into %s", source.Branch, target.Branch)
	}
	requireReview, err := s.reviewRequiredBeforeMerge(ctx, source.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.prepareSourceBeforeMerge(ctx, source, target, project.GitPath.String, requireReview); err != nil {
		return nil, err
	}
	status, err := s.Git.Status(ctx, *target.WorktreePath)
	if err != nil {
		return nil, err
	}
	if !status.Clean {
		return nil, mergeDirtyValidationError("target")
	}
	preMergeHead, err := s.Git.HeadCommit(ctx, *target.WorktreePath)
	if err != nil {
		return nil, err
	}
	mergeResult, err := s.Git.MergeNoCommit(ctx, *target.WorktreePath, source.Branch, strategy)
	if err != nil {
		return nil, err
	}
	if mergeResult.Success {
		commitSHA, err := s.Git.AutoCommit(ctx, *target.WorktreePath, message)
		if err != nil {
			s.Git.AbortMerge(ctx, *target.WorktreePath)
			return nil, err
		}
		if err := s.recordMerged(ctx, source, target, strategy, commitSHA, preMergeHead, message, userID); err != nil {
			return nil, err
		}
		return map[string]any{"resolved": true, "mergeResult": map[string]any{"success": true, "commitSha": commitSHA}}, nil
	}

	narratorID, model, err := s.primaryNarratorModel(ctx, target.ID)
	if err != nil {
		s.Git.AbortMerge(ctx, *target.WorktreePath)
		return nil, err
	}
	if narratorID == "" {
		s.Git.AbortMerge(ctx, *target.WorktreePath)
		return map[string]any{"resolved": false, "error": "Target chapter has no primary narrator", "remainingFiles": mergeResult.ConflictFiles}, nil
	}
	if model == "" && s.Settings != nil {
		model = s.Settings.Core().Agent.DefaultModel
	}
	prompt := buildConflictResolutionPromptGo(mergeResult.ConflictFiles, source.Branch, target.Branch, locale)
	_ = userID
	result, err := agentbasic.Generate(ctx, s.Settings, model, []agentbasic.Message{{Role: "user", Content: prompt}}, *target.WorktreePath)
	if err != nil {
		s.Git.AbortMerge(ctx, *target.WorktreePath)
		return map[string]any{"resolved": false, "error": err.Error(), "remainingFiles": mergeResult.ConflictFiles}, nil
	}
	remaining, err := s.Git.ConflictFiles(ctx, *target.WorktreePath)
	if err != nil {
		s.Git.AbortMerge(ctx, *target.WorktreePath)
		return nil, err
	}
	if len(remaining) > 0 {
		s.Git.AbortMerge(ctx, *target.WorktreePath)
		files := []string{}
		for _, file := range remaining {
			files = append(files, file.File)
		}
		return map[string]any{"resolved": false, "error": "Narrator could not resolve all conflicts", "remainingFiles": files}, nil
	}
	commitSHA, err := s.Git.AutoCommit(ctx, *target.WorktreePath, message)
	if err != nil {
		s.Git.AbortMerge(ctx, *target.WorktreePath)
		return nil, err
	}
	if err := s.recordMerged(ctx, source, target, strategy, commitSHA, preMergeHead, message, userID); err != nil {
		return nil, err
	}
	return map[string]any{"resolved": true, "mergeResult": map[string]any{"success": true, "commitSha": commitSHA}, "narratorId": narratorID, "assistantMessage": result.Text}, nil
}

func (s *Service) recordMerged(ctx context.Context, source, target Chapter, strategy, commitSHA, preMergeHead, message, userID string) error {
	now := db.NowISO()
	count := 0
	if target.WorktreePath != nil && *target.WorktreePath != "" {
		count, _ = s.Git.CommitCount(ctx, *target.WorktreePath)
	}
	metadata := jsonObjectString(map[string]any{"mergeCommitSha": nullableString(commitSHA), "strategy": strategy})
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE chapters SET status = 'merged', worktree_path = NULL, merged_into_chapter_id = ?, merge_strategy = ?, merge_commit_sha = ?, pre_merge_target_sha = ?, updated_at = ? WHERE id = ?`, target.ID, strategy, nullableString(commitSHA), nullableString(preMergeHead), now, source.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chapters SET head_commit_sha = ?, commit_count = ?, updated_at = ? WHERE id = ?`, nullableString(commitSHA), count, now, target.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES (?, ?, ?, ?, 'merge', ?, ?)`, id.New(), source.ProjectID, source.ID, target.ID, metadata, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if commitSHA != "" {
		sourceName := "merge"
		if strategy == "cherry-pick" {
			sourceName = "cherry_pick"
		}
		if err := s.recordChapterCommit(ctx, target.ID, commitSHA, firstNonEmptyString(message, fmt.Sprintf("Merge %s (%s)", source.Branch, strategy)), sourceName); err != nil {
			return err
		}
	}
	cleanupErr := s.cleanupMergedSourceWorktree(ctx, source)
	summaryErr := s.injectMergeSummaryFallback(ctx, source, target, strategy, commitSHA, message, userID)
	if cleanupErr != nil {
		return cleanupErr
	}
	return summaryErr
}

func (s *Service) cleanupMergedSourceWorktree(ctx context.Context, source Chapter) error {
	if source.WorktreePath == nil || strings.TrimSpace(*source.WorktreePath) == "" {
		return nil
	}
	project, err := s.getProject(ctx, source.ProjectID)
	if err != nil || !project.GitPath.Valid || strings.TrimSpace(project.GitPath.String) == "" {
		return nil
	}
	if s.TerminalRuntime != nil {
		rows, err := s.DB.QueryContext(ctx, `SELECT id FROM terminals WHERE chapter_id = ?`, source.ID)
		if err != nil {
			return fmt.Errorf("query source chapter terminals: %w", err)
		}
		ids := []string{}
		for rows.Next() {
			var terminalID string
			if scanErr := rows.Scan(&terminalID); scanErr != nil {
				_ = rows.Close()
				return fmt.Errorf("scan source chapter terminal: %w", scanErr)
			}
			ids = append(ids, terminalID)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close source chapter terminals: %w", err)
		}
		for _, terminalID := range ids {
			_ = s.TerminalRuntime.Kill(ctx, terminalID)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM terminal_tabs WHERE chapter_id = ?`, source.ID); err != nil {
		return fmt.Errorf("delete merged source terminal tabs: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM terminal_view_state WHERE chapter_id = ?`, source.ID); err != nil {
		return fmt.Errorf("delete merged source terminal view state: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM terminals WHERE chapter_id = ?`, source.ID); err != nil {
		return fmt.Errorf("delete merged source terminals: %w", err)
	}
	s.removeChapterContainersIfPossible(ctx, source)
	if safeWorktreePath(project.GitPath.String, *source.WorktreePath) {
		_ = s.Git.RemoveWorktree(ctx, project.GitPath.String, *source.WorktreePath)
	}
	return nil
}

func (s *Service) recordChapterCommit(ctx context.Context, chapterID, sha, fallbackMessage, source string) error {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return nil
	}
	message := fallbackMessage
	fullMessage := ""
	authorName := ""
	authorEmail := ""
	authoredAt := db.NowISO()
	if ch, err := s.Get(ctx, chapterID); err == nil {
		repoPath := ""
		if ch.WorktreePath != nil && strings.TrimSpace(*ch.WorktreePath) != "" {
			repoPath = *ch.WorktreePath
		} else if project, projectErr := s.getProject(ctx, ch.ProjectID); projectErr == nil && project.GitPath.Valid {
			repoPath = project.GitPath.String
		}
		if strings.TrimSpace(repoPath) != "" {
			if commit, err := s.Git.ShowCommit(ctx, repoPath, sha); err == nil {
				message = firstNonEmptyString(commit.Message, fallbackMessage)
				fullMessage = commit.FullMessage
				authorName = commit.AuthorName
				authorEmail = commit.AuthorEmail
				authoredAt = firstNonEmptyString(commit.AuthoredAt, authoredAt)
			}
		}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO chapter_commits (id, chapter_id, sha, message, full_message, author_name, author_email, authored_at, source, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id.New(), chapterID, sha, firstNonEmptyString(message, sha), nullableString(fullMessage), nullableString(authorName), nullableString(authorEmail), authoredAt, firstNonEmptyString(source, "manual"), db.NowISO())
	return err
}

func (s *Service) injectMergeSummaryFallback(ctx context.Context, source, target Chapter, strategy, commitSHA, message, userID string) error {
	var narratorID string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ? AND variant = 'primary' ORDER BY created_at ASC LIMIT 1`, target.ID).Scan(&narratorID)
	if err != nil || narratorID == "" {
		return nil
	}
	if err := s.markExistingMergeSummariesHistorical(ctx, source.ID); err != nil {
		return err
	}
	mergeRound, err := s.mergeSummaryRound(ctx, source.ID)
	if err != nil {
		return err
	}
	summary := firstNonEmptyString(message, fmt.Sprintf("Merged %s into %s using %s.", source.Branch, target.Branch, strategy))
	header := fmt.Sprintf(`[Merged] "%s" → "%s" (%s)`, source.Branch, target.Branch, strategy)
	contentText := header + "\n\n" + summary
	blocks := []map[string]any{
		{"type": "text", "text": contentText},
		{
			"type":            "merge_summary",
			"sourceBranch":    source.Branch,
			"sourceChapterId": source.ID,
			"targetBranch":    target.Branch,
			"strategy":        strategy,
			"commitSha":       nullableString(commitSHA),
			"mergedBy":        nullableString(userID),
			"mergeRound":      mergeRound,
			"isLatest":        true,
			"summary":         summary,
		},
	}
	contentJSON, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	now := db.NowISO()
	messageID := id.New()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_by, created_at) VALUES (?, ?, 'user', ?, ?, ?, ?)`, messageID, narratorID, string(contentJSON), contentText, nullableString(userID), now); err != nil {
		return err
	}
	var seq sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM narrator_message_refs WHERE narrator_id = ?`, narratorID).Scan(&seq); err != nil {
		return fmt.Errorf("query merge summary sequence: %w", err)
	}
	nextSeq := int64(1)
	if seq.Valid {
		nextSeq = seq.Int64 + 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, 0)`, id.New(), narratorID, messageID, nextSeq); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE narrators SET message_count = (SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ?), message_version = message_version + 1, last_message_at = ?, updated_at = ? WHERE id = ?`, narratorID, now, now, narratorID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Service) markExistingMergeSummariesHistorical(ctx context.Context, sourceID string) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, content_json FROM narrator_messages WHERE content_json LIKE ? AND content_json LIKE ?`, "%merge_summary%", "%"+sourceID+"%")
	if err != nil {
		return fmt.Errorf("query existing merge summaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var messageID, raw string
		if err := rows.Scan(&messageID, &raw); err != nil {
			return fmt.Errorf("scan existing merge summary: %w", err)
		}
		var blocks []map[string]any
		if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
			continue
		}
		changed := false
		for _, block := range blocks {
			if block["type"] == "merge_summary" {
				block["isLatest"] = false
				changed = true
			}
		}
		if !changed {
			continue
		}
		data, err := json.Marshal(blocks)
		if err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE narrator_messages SET content_json = ? WHERE id = ?`, string(data), messageID); err != nil {
			return fmt.Errorf("mark merge summary historical: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate existing merge summaries: %w", err)
	}
	return nil
}

func (s *Service) mergeSummaryRound(ctx context.Context, sourceID string) (int, error) {
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_messages WHERE content_json LIKE ? AND content_json LIKE ?`, "%merge_summary%", "%"+sourceID+"%").Scan(&count); err != nil {
		return 0, fmt.Errorf("count merge summaries: %w", err)
	}
	return count + 1, nil
}

func (s *Service) primaryNarratorModel(ctx context.Context, chapterID string) (string, string, error) {
	var narratorID string
	var model sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, model FROM narrators WHERE chapter_id = ? AND variant = 'primary' ORDER BY created_at ASC LIMIT 1`, chapterID).Scan(&narratorID, &model)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if model.Valid {
		return narratorID, model.String, nil
	}
	return narratorID, "", nil
}

func (s *Service) Unmerge(ctx context.Context, sourceID string) (map[string]any, error) {
	source, err := s.Get(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if source.Status != "merged" {
		return nil, httpx.BadRequest("Can only unmerge chapters with merged status")
	}
	if source.MergedIntoChapterID == nil || *source.MergedIntoChapterID == "" {
		return nil, httpx.BadRequest("Chapter has no merge target recorded")
	}
	if source.MergeCommitSHA == nil || *source.MergeCommitSHA == "" {
		return nil, httpx.BadRequest("Chapter has no merge commit SHA recorded — cannot unmerge")
	}
	target, err := s.Get(ctx, *source.MergedIntoChapterID)
	if err != nil {
		return nil, err
	}
	if target.WorktreePath == nil || *target.WorktreePath == "" {
		return nil, httpx.BadRequest("Target chapter has no active worktree — cannot unmerge")
	}
	status, err := s.Git.Status(ctx, *target.WorktreePath)
	if err != nil {
		return nil, err
	}
	if !status.Clean {
		return nil, httpx.BadRequest("Target worktree has uncommitted changes — cannot unmerge")
	}

	mergeSHA := strings.TrimSpace(*source.MergeCommitSHA)
	headSHA, err := s.Git.HeadCommit(ctx, *target.WorktreePath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(headSHA) == mergeSHA {
		resetTarget := mergeSHA + "~1"
		if source.PreMergeTargetSHA != nil && strings.TrimSpace(*source.PreMergeTargetSHA) != "" {
			resetTarget = strings.TrimSpace(*source.PreMergeTargetSHA)
		}
		if err := s.Git.Reset(ctx, *target.WorktreePath, resetTarget, "hard"); err != nil {
			return nil, err
		}
	} else {
		isMerge, err := s.Git.IsMergeCommit(ctx, *target.WorktreePath, mergeSHA)
		if err != nil {
			return nil, err
		}
		if isMerge {
			_, err = s.Git.RevertMergeCommit(ctx, *target.WorktreePath, mergeSHA)
		} else {
			_, err = s.Git.RevertCommit(ctx, *target.WorktreePath, mergeSHA)
		}
		if err != nil {
			return nil, httpx.Error(409, "UNMERGE_CONFLICT", fmt.Sprintf("Cannot automatically unmerge %s: revert conflicts with later target commits; resolve manually in the target worktree", shortSHA(mergeSHA)))
		}
	}

	worktreePath := ""
	if source.WorktreePath != nil {
		worktreePath = strings.TrimSpace(*source.WorktreePath)
	}
	if worktreePath == "" {
		project, err := s.getProject(ctx, source.ProjectID)
		if err != nil {
			return nil, err
		}
		if !project.GitPath.Valid || strings.TrimSpace(project.GitPath.String) == "" {
			return nil, httpx.BadRequest("Project has no git path")
		}
		slug := slugify(source.Title)
		short := id.Must(6)
		worktreePath = filepath.Join(project.GitPath.String, ".worktrees", slug+"-"+short)
		if err := s.Git.CreateWorktree(ctx, project.GitPath.String, worktreePath, source.Branch); err != nil {
			return nil, err
		}
	}

	now := db.NowISO()
	targetHead, err := s.Git.HeadCommit(ctx, *target.WorktreePath)
	if err != nil {
		return nil, err
	}
	targetCount, err := s.Git.CommitCount(ctx, *target.WorktreePath)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE chapters SET head_commit_sha = ?, commit_count = ?, updated_at = ? WHERE id = ?`, nullableString(targetHead), targetCount, now, target.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chapters SET status = 'active', worktree_path = ?, merged_into_chapter_id = NULL, merge_commit_sha = NULL, merge_strategy = NULL, pre_merge_target_sha = NULL, last_accessed_at = ?, updated_at = ? WHERE id = ?`, worktreePath, now, now, source.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chapter_edges WHERE source_id = ? AND type = 'merge'`, source.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.cleanupMergeSummaryForMerge(ctx, source.ID, mergeSHA); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (s *Service) cleanupMergeSummaryForMerge(ctx context.Context, sourceID, commitSHA string) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM narrator_messages WHERE content_json LIKE ? AND content_json LIKE ? AND content_json LIKE ?`, "%merge_summary%", "%"+sourceID+"%", "%"+commitSHA+"%")
	if err != nil {
		return err
	}
	messageIDs := []string{}
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			rows.Close()
			return err
		}
		messageIDs = append(messageIDs, messageID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(messageIDs) == 0 {
		return nil
	}

	narratorSet := map[string]bool{}
	for _, messageID := range messageIDs {
		refRows, err := s.DB.QueryContext(ctx, `SELECT narrator_id FROM narrator_message_refs WHERE message_id = ?`, messageID)
		if err != nil {
			return err
		}
		for refRows.Next() {
			var narratorID string
			if err := refRows.Scan(&narratorID); err != nil {
				refRows.Close()
				return err
			}
			narratorSet[narratorID] = true
		}
		if err := refRows.Close(); err != nil {
			return err
		}
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, messageID := range messageIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_message_refs WHERE message_id = ?`, messageID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_messages WHERE id = ?`, messageID); err != nil {
			return err
		}
	}
	for narratorID := range narratorSet {
		if _, err := tx.ExecContext(ctx, `UPDATE narrators SET message_count = (SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ?), message_version = message_version + 1, updated_at = ? WHERE id = ?`, narratorID, db.NowISO(), narratorID); err != nil {
			return fmt.Errorf("update merge summary narrator count: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	var latestID, contentJSON string
	err = s.DB.QueryRowContext(ctx, `SELECT id, content_json FROM narrator_messages WHERE content_json LIKE ? AND content_json LIKE ? ORDER BY created_at DESC LIMIT 1`, "%merge_summary%", "%"+sourceID+"%").Scan(&latestID, &contentJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(contentJSON), &blocks); err != nil {
		return nil
	}
	changed := false
	for _, block := range blocks {
		if block["type"] == "merge_summary" {
			block["isLatest"] = true
			changed = true
		}
	}
	if !changed {
		return nil
	}
	data, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE narrator_messages SET content_json = ? WHERE id = ?`, string(data), latestID)
	return err
}

func (s *Service) reviewRequiredBeforeMerge(ctx context.Context, projectID string) (bool, error) {
	project, err := s.getProject(ctx, projectID)
	if err != nil {
		return false, err
	}
	if !project.ChapterSettings.Valid || project.ChapterSettings.String == "" {
		return false, nil
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(project.ChapterSettings.String), &settings); err != nil {
		return false, nil
	}
	required, _ := settings["requireReviewBeforeMerge"].(bool)
	return required, nil
}

func (s *Service) prepareSourceBeforeMerge(ctx context.Context, source, target Chapter, gitPath string, requireReview bool) error {
	if err := s.ensureMergeWorktreeClean(ctx, source, "source"); err != nil {
		return err
	}
	if requireReview {
		return s.ensureSourceReadyForRequiredReview(ctx, source, gitPath)
	}
	return nil
}

func (s *Service) ensureMergeWorktreeClean(ctx context.Context, chapter Chapter, kind string) error {
	if chapter.WorktreePath == nil || strings.TrimSpace(*chapter.WorktreePath) == "" {
		return nil
	}
	status, err := s.Git.Status(ctx, strings.TrimSpace(*chapter.WorktreePath))
	if err != nil {
		return err
	}
	if !status.Clean {
		return mergeDirtyValidationError(kind)
	}
	return nil
}

func mergeDirtyValidationError(kind string) error {
	if kind == "target" {
		return httpx.ValidationError("MERGE_DIRTY_TARGET")
	}
	return httpx.ValidationError("MERGE_DIRTY_SOURCE")
}

func (s *Service) ensureSourceReadyForRequiredReview(ctx context.Context, source Chapter, gitPath string) error {
	if source.WorktreePath != nil && strings.TrimSpace(*source.WorktreePath) != "" {
		status, err := s.Git.Status(ctx, *source.WorktreePath)
		if err != nil {
			return err
		}
		if !status.Clean {
			return httpx.BadRequest("Source chapter has uncommitted changes. Commit them and run a new review before merging.")
		}
	}
	approved, reviewedSHA, verdict, err := s.latestApprovedReview(ctx, source.ID)
	if err != nil {
		return err
	}
	if !approved {
		if verdict != "" {
			return httpx.BadRequest("Review approval required before merge. Latest review verdict: " + verdict)
		}
		return httpx.BadRequest("Review approval required before merge. No review found.")
	}
	sourceHead, err := s.sourceHead(ctx, source, gitPath)
	if err != nil {
		return err
	}
	if reviewedSHA != "" && sourceHead != "" && reviewedSHA != sourceHead {
		return httpx.BadRequest("Review approval is stale: the source branch changed after the approved review. Run a new review before merging.")
	}
	return nil
}

func (s *Service) latestApprovedReview(ctx context.Context, sourceID string) (bool, string, string, error) {
	var verdict, reviewChapterID string
	err := s.DB.QueryRowContext(ctx, `SELECT verdict, review_chapter_id FROM review_conclusions WHERE source_chapter_id = ? ORDER BY created_at DESC LIMIT 1`, sourceID).Scan(&verdict, &reviewChapterID)
	if err == sql.ErrNoRows {
		return false, "", "", nil
	}
	if err != nil {
		return false, "", "", err
	}
	if verdict != "approve" {
		return false, "", verdict, nil
	}
	reviewedSHA, err := s.reviewedCommitSHA(ctx, reviewChapterID)
	return true, reviewedSHA, verdict, err
}

func (s *Service) reviewedCommitSHA(ctx context.Context, reviewChapterID string) (string, error) {
	var forkPoint, start, head sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT fork_point, start_commit_sha, head_commit_sha FROM chapters WHERE id = ?`, reviewChapterID).Scan(&forkPoint, &start, &head)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if forkPoint.Valid && strings.TrimSpace(forkPoint.String) != "" {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(forkPoint.String), &decoded); err == nil {
			if commitSHA, ok := decoded["commitSha"].(string); ok && strings.TrimSpace(commitSHA) != "" {
				return strings.TrimSpace(commitSHA), nil
			}
		}
		return strings.TrimSpace(forkPoint.String), nil
	}
	if start.Valid && strings.TrimSpace(start.String) != "" {
		return strings.TrimSpace(start.String), nil
	}
	if head.Valid && strings.TrimSpace(head.String) != "" {
		return strings.TrimSpace(head.String), nil
	}
	return "", nil
}

func (s *Service) sourceHead(ctx context.Context, source Chapter, gitPath string) (string, error) {
	if source.WorktreePath != nil && strings.TrimSpace(*source.WorktreePath) != "" {
		head, err := s.Git.HeadCommit(ctx, *source.WorktreePath)
		return strings.TrimSpace(head), err
	}
	head, err := s.Git.GetRefCommit(ctx, gitPath, source.Branch)
	return strings.TrimSpace(head), err
}

func (s *Service) hasApprovedReview(ctx context.Context, sourceID string) (bool, error) {
	approved, _, _, err := s.latestApprovedReview(ctx, sourceID)
	return approved, err
}

func (s *Service) mergePair(ctx context.Context, sourceID, targetID string) (Chapter, Chapter, error) {
	source, err := s.Get(ctx, sourceID)
	if err != nil {
		return Chapter{}, Chapter{}, err
	}
	if targetID == "" && source.ParentChapterID != nil {
		targetID = *source.ParentChapterID
	}
	if targetID == "" {
		rootID, err := s.fallbackRootTargetID(ctx, source.ProjectID)
		if err != nil {
			return Chapter{}, Chapter{}, err
		}
		targetID = rootID
	}
	if targetID == "" {
		return Chapter{}, Chapter{}, httpx.BadRequest("targetChapterId is required")
	}
	target, err := s.Get(ctx, targetID)
	return source, target, err
}

func (s *Service) fallbackRootTargetID(ctx context.Context, projectID string) (string, error) {
	var targetID string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM chapters WHERE project_id = ? AND is_root = 1 LIMIT 1`, projectID).Scan(&targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return targetID, nil
}

func validateMergePair(source, target Chapter, allowDormantSource bool) error {
	if source.ID == target.ID {
		return httpx.BadRequest("Cannot merge a chapter into itself")
	}
	if source.ProjectID != target.ProjectID {
		return httpx.BadRequest("Cannot merge chapters from different projects")
	}
	if allowDormantSource {
		if source.Status != "active" && source.Status != "dormant" {
			return httpx.BadRequest("Source chapter must be active or dormant")
		}
	} else if source.Status != "active" {
		return httpx.BadRequest("Source chapter must be active")
	}
	if target.Status != "active" {
		return httpx.BadRequest("Target chapter must be active")
	}
	if target.WorktreePath == nil || strings.TrimSpace(*target.WorktreePath) == "" {
		return httpx.BadRequest("Target chapter has no worktree")
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

func (s *Service) GitStatus(ctx context.Context, chapterID string) (gitx.Status, error) {
	ch, err := s.Get(ctx, chapterID)
	if err != nil {
		return gitx.Status{}, err
	}
	if ch.WorktreePath == nil {
		return gitx.Status{}, httpx.BadRequest("Chapter has no worktree path")
	}
	return s.Git.Status(ctx, *ch.WorktreePath)
}

func (s *Service) ChapterGitStatus(ctx context.Context, chapterID string) (ChapterGitStatus, error) {
	ch, err := s.Get(ctx, chapterID)
	if err != nil {
		return ChapterGitStatus{}, err
	}
	result := ChapterGitStatus{BaseBranch: ch.BaseBranch}
	if ch.WorktreePath == nil || strings.TrimSpace(*ch.WorktreePath) == "" {
		return result, nil
	}
	status, err := s.Git.StatusSummary(ctx, *ch.WorktreePath)
	if err != nil {
		return ChapterGitStatus{}, err
	}
	result.LinesAdded = status.LinesAdded
	result.LinesRemoved = status.LinesRemoved
	if strings.TrimSpace(ch.BaseBranch) != "" {
		if ahead, err := s.Git.CountCommitsBetween(ctx, *ch.WorktreePath, ch.BaseBranch, "HEAD"); err == nil {
			result.CommitsAhead = ahead
		}
	}
	return result, nil
}

func (s *Service) Commits(ctx context.Context, chapterID string, limit int) ([]gitx.Commit, error) {
	limit = normalizeChapterCommitLimit(limit)
	if s.DB != nil && s.Git != nil {
		if _, err := s.syncChapterCommits(ctx, chapterID, limit); err == nil {
			if commits, loaded, err := s.loadChapterCommitsFromDB(ctx, chapterID, limit); err == nil && loaded {
				return commits, nil
			}
		}
	}
	ch, err := s.Get(ctx, chapterID)
	if err != nil {
		return nil, err
	}
	cwd, ref, err := s.chapterGitLocation(ctx, ch)
	if err != nil {
		return nil, err
	}
	if s.Git == nil {
		return nil, httpx.BadRequest("Git service unavailable")
	}
	return s.Git.LogRef(ctx, cwd, ref, limit)
}

func (s *Service) Commit(ctx context.Context, chapterID, sha string) (gitx.Commit, error) {
	if s.DB != nil && s.Git != nil {
		if commit, loaded, err := s.loadChapterCommitFromDB(ctx, chapterID, sha); err == nil && loaded {
			return commit, nil
		}
		if _, err := s.syncChapterCommits(ctx, chapterID, normalizeChapterCommitLimit(50)); err == nil {
			if commit, loaded, err := s.loadChapterCommitFromDB(ctx, chapterID, sha); err == nil && loaded {
				return commit, nil
			}
		}
	}
	ch, err := s.Get(ctx, chapterID)
	if err != nil {
		return gitx.Commit{}, err
	}
	cwd, ref, err := s.chapterGitLocation(ctx, ch)
	if err != nil {
		return gitx.Commit{}, err
	}
	if s.Git == nil {
		return gitx.Commit{}, httpx.BadRequest("Git service unavailable")
	}
	if strings.TrimSpace(ref) == "" {
		return s.Git.ShowCommit(ctx, cwd, sha)
	}
	return s.Git.ShowCommit(ctx, cwd, sha)
}

const chapterCommitSyncMaxLimit = 200

func normalizeChapterCommitLimit(limit int) int {
	if limit <= 0 || limit > chapterCommitSyncMaxLimit {
		return 50
	}
	return limit
}

func (s *Service) chapterGitLocation(ctx context.Context, ch Chapter) (string, string, error) {
	if ch.WorktreePath != nil && strings.TrimSpace(*ch.WorktreePath) != "" {
		return *ch.WorktreePath, "", nil
	}
	if s.DB == nil {
		return "", "", httpx.BadRequest("Chapter has no active worktree")
	}
	project, err := s.getProject(ctx, ch.ProjectID)
	if err != nil {
		return "", "", err
	}
	if !project.GitPath.Valid || strings.TrimSpace(project.GitPath.String) == "" {
		return "", "", httpx.BadRequest("Chapter has no active worktree")
	}
	ref := firstNonEmptyString(ch.Branch, ch.BaseBranch, "HEAD")
	return project.GitPath.String, ref, nil
}

func (s *Service) syncChapterCommits(ctx context.Context, chapterID string, limit int) (int, error) {
	if s.DB == nil || s.Git == nil {
		return 0, nil
	}
	ch, err := s.Get(ctx, chapterID)
	if err != nil {
		return 0, err
	}
	cwd, ref, err := s.chapterGitLocation(ctx, ch)
	if err != nil {
		return 0, err
	}
	commits, err := s.Git.LogRef(ctx, cwd, ref, normalizeChapterCommitLimit(limit))
	if err != nil {
		if isSQLiteMissingTableError(err) {
			return 0, nil
		}
		return 0, err
	}
	if len(commits) == 0 {
		return 0, nil
	}
	existing, err := s.chapterCommitSHASet(ctx, chapterID)
	if err != nil {
		if isSQLiteMissingTableError(err) {
			return 0, nil
		}
		return 0, err
	}
	inserted := 0
	now := db.NowISO()
	for _, commit := range commits {
		if existing[commit.SHA] {
			continue
		}
		fullMessage := commit.FullMessage
		authorName := commit.AuthorName
		authorEmail := commit.AuthorEmail
		authoredAt := commit.AuthoredAt
		if detail, detailErr := s.Git.ShowCommit(ctx, cwd, commit.SHA); detailErr == nil {
			fullMessage = firstNonEmptyString(detail.FullMessage, fullMessage)
			authorName = firstNonEmptyString(detail.AuthorName, authorName)
			authorEmail = firstNonEmptyString(detail.AuthorEmail, authorEmail)
			authoredAt = firstNonEmptyString(detail.AuthoredAt, authoredAt)
		}
		filesChanged, linesAdded, linesRemoved := 0, 0, 0
		if statsFiles, statsAdded, statsRemoved, statsErr := s.Git.CommitDiffStats(ctx, cwd, commit.SHA); statsErr == nil {
			filesChanged, linesAdded, linesRemoved = statsFiles, statsAdded, statsRemoved
		}
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO chapter_commits (id, chapter_id, sha, message, full_message, author_name, author_email, authored_at, source, narrator_id, narrator_message_id, files_changed, lines_added, lines_removed, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id.New(), chapterID, commit.SHA, firstNonEmptyString(commit.Message, commit.SHA), nullableString(fullMessage), nullableString(authorName), nullableString(authorEmail), authoredAt, "manual", nullableString(commit.NarratorID), nullableString(commit.NarratorMessageID), filesChanged, linesAdded, linesRemoved, now); err != nil {
			if isSQLiteMissingTableError(err) {
				return inserted, nil
			}
			return inserted, err
		}
		existing[commit.SHA] = true
		inserted++
	}
	if len(commits) > 0 {
		head := commits[0].SHA
		count, countErr := s.Git.CommitCountRef(ctx, cwd, ref)
		if countErr != nil || count <= 0 {
			count, countErr = s.chapterCommitCount(ctx, chapterID)
			if countErr != nil {
				if isSQLiteMissingTableError(countErr) {
					return inserted, nil
				}
				return inserted, countErr
			}
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE chapters SET head_commit_sha = ?, commit_count = ?, updated_at = ? WHERE id = ?`, nullableString(head), count, db.NowISO(), chapterID); err != nil {
			if !isSQLiteMissingTableError(err) {
				return inserted, err
			}
		}
	}
	return inserted, nil
}

func (s *Service) loadChapterCommitsFromDB(ctx context.Context, chapterID string, limit int) ([]gitx.Commit, bool, error) {
	if s.DB == nil {
		return nil, false, nil
	}
	if limit <= 0 || limit > chapterCommitSyncMaxLimit {
		limit = 50
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT sha, message, COALESCE(full_message, ''), COALESCE(author_name, ''), COALESCE(author_email, ''), authored_at, COALESCE(source, ''), COALESCE(narrator_id, ''), COALESCE(narrator_message_id, ''), files_changed, lines_added, lines_removed FROM chapter_commits WHERE chapter_id = ? ORDER BY authored_at DESC, created_at DESC LIMIT ?`, chapterID, limit)
	if err != nil {
		if isSQLiteMissingTableError(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer rows.Close()
	commits := []gitx.Commit{}
	for rows.Next() {
		var sha, message, fullMessage, authorName, authorEmail, authoredAt, source, narratorID, narratorMessageID string
		var filesChanged, linesAdded, linesRemoved sql.NullInt64
		if err := rows.Scan(&sha, &message, &fullMessage, &authorName, &authorEmail, &authoredAt, &source, &narratorID, &narratorMessageID, &filesChanged, &linesAdded, &linesRemoved); err != nil {
			return nil, false, err
		}
		commits = append(commits, gitx.Commit{
			SHA:               sha,
			Message:           message,
			FullMessage:       fullMessage,
			AuthorName:        authorName,
			AuthorEmail:       authorEmail,
			AuthoredAt:        authoredAt,
			Source:            source,
			NarratorID:        narratorID,
			NarratorMessageID: narratorMessageID,
			FilesChanged:      nullableIntPtr(filesChanged),
			LinesAdded:        nullableIntPtr(linesAdded),
			LinesRemoved:      nullableIntPtr(linesRemoved),
		})
	}
	return commits, true, rows.Err()
}

func (s *Service) loadChapterCommitFromDB(ctx context.Context, chapterID, sha string) (gitx.Commit, bool, error) {
	if s.DB == nil {
		return gitx.Commit{}, false, nil
	}
	row := s.DB.QueryRowContext(ctx, `SELECT sha, message, COALESCE(full_message, ''), COALESCE(author_name, ''), COALESCE(author_email, ''), authored_at, COALESCE(source, ''), COALESCE(narrator_id, ''), COALESCE(narrator_message_id, ''), files_changed, lines_added, lines_removed FROM chapter_commits WHERE chapter_id = ? AND sha = ? LIMIT 1`, chapterID, sha)
	var record struct {
		sha, message, fullMessage, authorName, authorEmail, authoredAt, source, narratorID, narratorMessageID string
		filesChanged, linesAdded, linesRemoved                                                                sql.NullInt64
	}
	if err := row.Scan(&record.sha, &record.message, &record.fullMessage, &record.authorName, &record.authorEmail, &record.authoredAt, &record.source, &record.narratorID, &record.narratorMessageID, &record.filesChanged, &record.linesAdded, &record.linesRemoved); err != nil {
		if errors.Is(err, sql.ErrNoRows) || isSQLiteMissingTableError(err) {
			return gitx.Commit{}, false, nil
		}
		return gitx.Commit{}, false, err
	}
	return gitx.Commit{
		SHA:               record.sha,
		Message:           record.message,
		FullMessage:       record.fullMessage,
		AuthorName:        record.authorName,
		AuthorEmail:       record.authorEmail,
		AuthoredAt:        record.authoredAt,
		Source:            record.source,
		NarratorID:        record.narratorID,
		NarratorMessageID: record.narratorMessageID,
		FilesChanged:      nullableIntPtr(record.filesChanged),
		LinesAdded:        nullableIntPtr(record.linesAdded),
		LinesRemoved:      nullableIntPtr(record.linesRemoved),
	}, true, nil
}

func (s *Service) chapterCommitSHASet(ctx context.Context, chapterID string) (map[string]bool, error) {
	set := map[string]bool{}
	if s.DB == nil {
		return set, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT sha FROM chapter_commits WHERE chapter_id = ?`, chapterID)
	if err != nil {
		if isSQLiteMissingTableError(err) {
			return set, nil
		}
		return set, err
	}
	defer rows.Close()
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return set, err
		}
		if strings.TrimSpace(sha) != "" {
			set[sha] = true
		}
	}
	return set, rows.Err()
}

func (s *Service) chapterCommitCount(ctx context.Context, chapterID string) (int, error) {
	if s.DB == nil {
		return 0, nil
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = ?`, chapterID).Scan(&count); err != nil {
		if isSQLiteMissingTableError(err) {
			return 0, nil
		}
		return 0, err
	}
	return count, nil
}

func nullableIntPtr(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	v := int(value.Int64)
	return &v
}

func isSQLiteMissingTableError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table")
}

func isSQLiteMissingColumnError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such column")
}

func (s *Service) getProject(ctx context.Context, projectID string) (projectRow, error) {
	var p projectRow
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, git_path, default_branch, startup_script, copy_files, chapter_settings FROM projects WHERE id = ?`, projectID).Scan(&p.ID, &p.Name, &p.GitPath, &p.DefaultBranch, &p.StartupScript, &p.CopyFiles, &p.ChapterSettings)
	if err != nil && strings.Contains(err.Error(), "no such column") {
		err = s.DB.QueryRowContext(ctx, `SELECT id, name, git_path, default_branch, chapter_settings FROM projects WHERE id = ?`, projectID).Scan(&p.ID, &p.Name, &p.GitPath, &p.DefaultBranch, &p.ChapterSettings)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return projectRow{}, httpx.NotFound("Project")
		}
		return projectRow{}, err
	}
	return p, nil
}

const chapterSelectColumns = `id, project_id, title, description, status, role, branch, worktree_path,
base_branch, parent_chapter_id, fork_point, merged_into_chapter_id, merge_commit_sha,
merge_strategy, pre_merge_target_sha, container_config, exploration_group_id, is_root,
head_commit_sha, start_commit_sha, COALESCE(commit_count, 0), color, group_label,
COALESCE(pinned, 0), anchor_commit_sha, COALESCE(axis_offset, 0), COALESCE(cross_offset, 0),
COALESCE(panel_expanded, 0), panel_width, panel_height, review_source_chapter_id,
review_status, last_accessed_at, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanChapter(row rowScanner) (Chapter, error) {
	var ch Chapter
	var desc, worktree, parent, forkPoint, mergedInto, mergeCommit, mergeStrategy, preMergeTarget sql.NullString
	var containerConfig, explorationGroup, head, start, color, group, anchor, reviewSource, reviewStatus, lastAccess sql.NullString
	var isRoot, pinned, panelExpanded int
	var panelWidth, panelHeight sql.NullFloat64
	err := row.Scan(&ch.ID, &ch.ProjectID, &ch.Title, &desc, &ch.Status, &ch.Role, &ch.Branch, &worktree,
		&ch.BaseBranch, &parent, &forkPoint, &mergedInto, &mergeCommit, &mergeStrategy, &preMergeTarget,
		&containerConfig, &explorationGroup, &isRoot, &head, &start, &ch.CommitCount, &color, &group,
		&pinned, &anchor, &ch.AxisOffset, &ch.CrossOffset, &panelExpanded, &panelWidth, &panelHeight,
		&reviewSource, &reviewStatus, &lastAccess, &ch.CreatedAt, &ch.UpdatedAt)
	if err != nil {
		return Chapter{}, err
	}
	ch.Description = nullStringPtr(desc)
	ch.WorktreePath = nullStringPtr(worktree)
	ch.ParentChapterID = nullStringPtr(parent)
	ch.ForkPoint = decodeForkPoint(forkPoint)
	ch.MergedIntoChapterID = nullStringPtr(mergedInto)
	ch.MergeCommitSHA = nullStringPtr(mergeCommit)
	ch.MergeStrategy = nullStringPtr(mergeStrategy)
	ch.PreMergeTargetSHA = nullStringPtr(preMergeTarget)
	ch.ContainerConfig = jsondb.ObjectOrNil(containerConfig)
	ch.ExplorationGroupID = nullStringPtr(explorationGroup)
	ch.IsRoot = isRoot != 0
	ch.HeadCommitSHA = nullStringPtr(head)
	ch.StartCommitSHA = nullStringPtr(start)
	ch.Color = nullStringPtr(color)
	ch.GroupLabel = nullStringPtr(group)
	ch.Pinned = pinned != 0
	ch.AnchorCommitSHA = nullStringPtr(anchor)
	ch.PanelExpanded = panelExpanded != 0
	if panelWidth.Valid {
		ch.PanelWidth = &panelWidth.Float64
	}
	if panelHeight.Valid {
		ch.PanelHeight = &panelHeight.Float64
	}
	ch.ReviewSourceChapterID = nullStringPtr(reviewSource)
	ch.ReviewStatus = nullStringPtr(reviewStatus)
	ch.LastAccessedAt = nullStringPtr(lastAccess)
	return ch, nil
}

func decodeForkPoint(raw sql.NullString) any {
	decoded := jsondb.DecodeNull(raw)
	switch value := decoded.(type) {
	case nil:
		return nil
	case map[string]any:
		return value
	case string:
		if strings.TrimSpace(value) != "" {
			return map[string]any{"commitSha": strings.TrimSpace(value)}
		}
	}
	return nil
}

func jsonObjectString(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func buildConflictResolutionPromptGo(conflictFiles []string, sourceBranch, targetBranch, locale string) string {
	var builder strings.Builder
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		builder.WriteString("你正在处理一次 Git 合并冲突。请直接修改工作区中的冲突文件，保留正确的最终内容，删除所有冲突标记。\n\n")
		builder.WriteString(fmt.Sprintf("源分支：%s\n目标分支：%s\n\n冲突文件：\n", sourceBranch, targetBranch))
	} else {
		builder.WriteString("You are resolving a Git merge conflict. Edit the conflicted files in the working tree, keep the correct final content, and remove all conflict markers.\n\n")
		builder.WriteString(fmt.Sprintf("Source branch: %s\nTarget branch: %s\n\nConflicted files:\n", sourceBranch, targetBranch))
	}
	if len(conflictFiles) == 0 {
		builder.WriteString("  - (git did not report file names; inspect the repository status)\n")
	} else {
		for _, file := range conflictFiles {
			builder.WriteString("  - ")
			builder.WriteString(file)
			builder.WriteString("\n")
		}
	}
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		builder.WriteString("\n完成后不要提交；后端会检查冲突是否清空并自动提交。")
	} else {
		builder.WriteString("\nDo not create a commit; the backend will verify that conflicts are cleared and commit automatically.")
	}
	return builder.String()
}

func (s *Service) buildReviewDiffContext(ctx context.Context, gitPath string, source Chapter, sourceHead string) string {
	const maxDiffChars = 80000
	parts := []string{}
	totalLen := 0
	addPart := func(text, truncation string) bool {
		if strings.TrimSpace(text) == "" {
			return true
		}
		remaining := maxDiffChars - totalLen
		if remaining <= 0 {
			return false
		}
		if len(text) <= remaining {
			parts = append(parts, text)
			totalLen += len(text)
			return true
		}
		parts = append(parts, truncateAtLine(text, remaining), truncation)
		totalLen = maxDiffChars
		return false
	}
	baseRef := strings.TrimSpace(source.BaseBranch)
	if source.StartCommitSHA != nil && strings.TrimSpace(*source.StartCommitSHA) != "" {
		baseRef = strings.TrimSpace(*source.StartCommitSHA)
	}
	if baseRef != "" && sourceHead != "" {
		if committed, err := s.Git.DiffBetweenRefs(ctx, gitPath, baseRef, sourceHead); err == nil {
			addPart(committed, "\n\n[Diff truncated — committed changes exceed size limit. Use the Read and Grep tools to examine specific files in detail.]\n")
		}
	}
	if totalLen < maxDiffChars {
		sourceWorktree := gitPath
		if source.WorktreePath != nil && strings.TrimSpace(*source.WorktreePath) != "" {
			sourceWorktree = strings.TrimSpace(*source.WorktreePath)
		}
		if uncommitted, err := s.Git.FullDiff(ctx, sourceWorktree); err == nil && strings.TrimSpace(uncommitted) != "" {
			header := "\n--- Uncommitted changes in source worktree ---\n"
			remaining := maxDiffChars - totalLen
			if len(header) < remaining {
				parts = append(parts, header)
				totalLen += len(header)
				addPart(uncommitted, "\n\n[Uncommitted diff truncated. Use the Read and Grep tools to examine specific files in detail.]\n")
			}
		}
	}
	if len(parts) == 0 {
		return "No code changes detected in the source chapter."
	}
	return strings.Join(parts, "\n")
}

func buildReviewSystemPrompt(diffContext, locale string) string {
	instructions := `You are an independent code reviewer. Your task is to review the code changes shown below.

You have NO prior knowledge or context about these changes — this is intentional, to ensure your review is free from preconceptions.

You CAN:
- Read the diff and related files to understand the changes
- Search the codebase to understand context and patterns
- Run tests to verify code behavior
- Run linters or type checkers

You MUST NOT modify any files. If you modify files during verification, the system will automatically revert all changes and ask you to re-output your review conclusion.

When you have completed your review, you MUST call the ConcludeReview tool to submit your structured conclusion. Do NOT output your conclusion as plain text — always use the tool. Your conclusion should include:
- Overall verdict (approve / request_changes / comment_only)
- Key findings ordered by severity (critical > major > minor > suggestion), with file paths and line numbers where applicable`
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		instructions = `你是一个独立的代码审查者。你的任务是审查下方展示的代码变更。

你对这些变更没有任何先验知识或上下文 — 这是有意为之的，以确保你的审查不受先入为主的影响。

你可以：
- 阅读 diff 和相关文件以理解变更
- 搜索代码库以理解上下文和模式
- 运行测试来验证代码行为
- 运行 linter 或类型检查器

你不得修改任何文件。如果你在验证过程中修改了文件，系统会自动回退所有变更并要求你重新输出审查结论。

当你完成审查后，你必须调用 ConcludeReview 工具提交结构化结论。不要以纯文本形式输出结论 — 始终使用该工具。结论应包含：
- 总体评价（approve / request_changes / comment_only）
- 关键发现按严重程度排列（critical > major > minor > suggestion），包含文件路径和行号`
	}
	return fmt.Sprintf("%s\n\n---\n\n## Code Changes to Review\n\n```diff\n%s\n```", instructions, diffContext)
}

func reviewStartMessage(locale string) string {
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		return "请开始你的代码审查。"
	}
	return "Please begin your code review now."
}

func truncateAtLine(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	if maxLen <= 0 {
		return ""
	}
	if idx := strings.LastIndex(text[:maxLen], "\n"); idx > 0 {
		return text[:idx]
	}
	return text[:maxLen]
}

func (s *Service) copyReviewDirtyFiles(ctx context.Context, sourceWorktree, targetWorktree string) error {
	status, err := s.Git.GetStatus(ctx, sourceWorktree)
	if err != nil || strings.TrimSpace(status) == "" {
		return err
	}
	for _, line := range strings.Split(status, "\n") {
		if len(line) < 3 {
			continue
		}
		x, y := line[0], line[1]
		filePath := strings.TrimSpace(line[3:])
		if filePath == "" {
			continue
		}
		if (x == 'R' || y == 'R') && strings.Contains(filePath, " -> ") {
			parts := strings.Split(filePath, " -> ")
			filePath = strings.TrimSpace(parts[len(parts)-1])
		}
		clean := filepath.Clean(filePath)
		if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			continue
		}
		src := filepath.Join(sourceWorktree, clean)
		dst := filepath.Join(targetWorktree, clean)
		if y == 'D' || (x == 'D' && y == ' ') {
			_ = os.RemoveAll(dst)
			continue
		}
		info, statErr := os.Stat(src)
		if statErr != nil {
			continue
		}
		if info.IsDir() {
			_ = copyDir(src, dst)
			continue
		}
		_ = copyFileWithMode(src, dst, info.Mode().Perm())
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		return copyFileWithMode(path, target, info.Mode().Perm())
	})
}

func copyFileWithMode(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func slugify(input string) string {
	s := strings.ToLower(strings.TrimSpace(input))
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = strings.Trim(re.ReplaceAllString(s, "-"), "-")
	if s == "" {
		s = "chapter"
	}
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return s
}

func safeWorktreePath(repoPath, worktreePath string) bool {
	base, err := filepath.Abs(filepath.Join(repoPath, ".worktrees"))
	if err != nil {
		return false
	}
	candidate, err := filepath.Abs(worktreePath)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(base, candidate)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func stringValueOr(value any, fallback string) string {
	if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}

func firstNonEmptyAny(values ...any) string {
	for _, value := range values {
		if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func mergeCommitFromAIResult(result map[string]any) string {
	mergeResult, ok := result["mergeResult"].(map[string]any)
	if !ok {
		return ""
	}
	return firstNonEmptyAny(mergeResult["mergeCommitSha"], mergeResult["commitSha"])
}

func nullablePtr(v *string) any {
	if v == nil {
		return nil
	}
	if *v == "" {
		return nil
	}
	return *v
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullableJSON(v any) any {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) == "" || strings.TrimSpace(s) == "null" {
			return nil
		}
		return s
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(data)
}
