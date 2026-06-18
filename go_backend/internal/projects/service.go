package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"narrafork-go/internal/chapters"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/jsondb"
)

type CloneStreamerFunc func(
	ctx context.Context,
	cloneURL, gitPath, branch string,
	credentials *gitx.CloneCredentials,
	progress func(string),
) error

type Service struct {
	DB                           *sql.DB
	Git                          *gitx.Service
	Chapters                     *chapters.Service
	Settings                     *config.Store
	BroadcastUser                func(userID string, payload any)
	ProxyCacheRefresher          func(ctx context.Context) error
	CloneStreamer                CloneStreamerFunc
	CloneFailureDirectoryRemover func(path string) error
	GitignoreCommitter           func(ctx context.Context, gitPath string) error
	ContainerRemover             func(ctx context.Context, containerIDs []string) error
	WorktreeRemover              func(ctx context.Context, repoPath, worktreePath string) error
	WorktreePruner               func(ctx context.Context, repoPath string) error
}

type DeleteCleanupError struct {
	Scope string `json:"scope"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error"`
}

type DeleteResult struct {
	OK            bool                 `json:"ok"`
	ProjectID     string               `json:"projectId"`
	CleanupErrors []DeleteCleanupError `json:"cleanupErrors,omitempty"`
}

type projectDeleteChapter struct {
	ID           string
	WorktreePath sql.NullString
	IsRoot       bool
}

type Project struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     *string `json:"description"`
	Status          string  `json:"status"`
	FlowMode        string  `json:"flowMode"`
	GitPath         *string `json:"gitPath"`
	RemoteURL       *string `json:"remoteUrl"`
	DefaultBranch   *string `json:"defaultBranch"`
	StartupScript   *string `json:"startupScript"`
	CopyFiles       *string `json:"copyFiles"`
	ChapterSettings any     `json:"chapterSettings"`
	ProxyDomain     *string `json:"proxyDomain"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
}

type CreateInput struct {
	Name          string
	Description   *string
	RepoMode      string
	GitPath       string
	FlowMode      string
	CloneURL      string
	CloneBranch   string
	CloneUsername string
	ClonePassword string
	Progress      func(string)
}

func NewService(sqlDB *sql.DB, git *gitx.Service, chapterService *chapters.Service, settings *config.Store) *Service {
	return &Service{DB: sqlDB, Git: git, Chapters: chapterService, Settings: settings}
}

func (s *Service) SetUserBroadcaster(broadcast func(userID string, payload any)) {
	s.BroadcastUser = broadcast
}

func (s *Service) SetProxyCacheRefresher(refresh func(ctx context.Context) error) {
	s.ProxyCacheRefresher = refresh
}

func (s *Service) cloneStreamingWithCredentials(ctx context.Context, cloneURL, gitPath, branch string, credentials *gitx.CloneCredentials, progress func(string)) error {
	if s.CloneStreamer != nil {
		return s.CloneStreamer(ctx, cloneURL, gitPath, branch, credentials, progress)
	}
	if s.Git == nil {
		return errors.New("git service unavailable")
	}
	return s.Git.CloneStreamingWithCredentials(ctx, cloneURL, gitPath, branch, credentials, progress)
}

func (s *Service) removeCloneFailureDirectory(path string) error {
	if s.CloneFailureDirectoryRemover != nil {
		return s.CloneFailureDirectoryRemover(path)
	}
	return os.RemoveAll(path)
}

func (s *Service) currentBranch(ctx context.Context, gitPath string) (string, error) {
	if s.Git == nil {
		return "", errors.New("git service unavailable")
	}
	return s.Git.CurrentBranch(ctx, gitPath)
}

func (s *Service) initializeProjectBackup(ctx context.Context, projectID, gitPath string) error {
	var syncErr error
	if _, err := s.SyncBackup(ctx, projectID); err != nil {
		syncErr = err
	}
	if err := s.commitGitignoreIfDirty(ctx, gitPath); err != nil {
		if syncErr != nil {
			return fmt.Errorf("sync project backup: %w; update .gitignore: %v", syncErr, err)
		}
		return err
	}
	return syncErr
}

func (s *Service) commitGitignoreIfDirty(ctx context.Context, gitPath string) error {
	if err := ensureGitignoreEntries(gitPath); err != nil {
		return err
	}
	if s.GitignoreCommitter != nil {
		return s.GitignoreCommitter(ctx, gitPath)
	}
	if s.Git == nil {
		return errors.New("git service unavailable")
	}
	return s.Git.CommitGitignoreIfDirty(ctx, gitPath)
}

func ensureGitignoreEntries(gitPath string) error {
	entries := []string{".narrafork/", ".worktrees/"}
	gitignorePath := filepath.Join(gitPath, ".gitignore")
	content := ""
	if existing, err := os.ReadFile(gitignorePath); err == nil {
		content = string(existing)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	missing := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !strings.Contains(content, entry) {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	prefix := strings.TrimRight(content, "\r\n")
	if prefix != "" {
		prefix += "\n"
	}
	return os.WriteFile(gitignorePath, []byte(prefix+strings.Join(missing, "\n")+"\n"), 0o644)
}

func (s *Service) List(ctx context.Context, status string) ([]Project, error) {
	query := `SELECT ` + projectSelectColumns + ` FROM projects`
	args := []any{}
	if status == "active" || status == "archived" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Project, error) {
	if strings.TrimSpace(input.Name) == "" {
		return Project{}, httpx.BadRequest("name is required")
	}
	// Mirror the TS createProjectSchema enums/regex (server/lib/validators/projects.ts)
	// that the length-only handler validation does not cover: flowMode is
	// z.enum(["classic","ruler"]).default("classic") so a present non-empty value
	// must be one of the two (empty falls back to classic below), and cloneBranch is
	// gitBranchName.optional() so a present value must match the branch-name charset.
	if input.FlowMode != "" && input.FlowMode != "classic" && input.FlowMode != "ruler" {
		return Project{}, httpx.BadRequest("flowMode must be classic or ruler")
	}
	if input.CloneBranch != "" && !gitBranchNamePattern.MatchString(input.CloneBranch) {
		return Project{}, httpx.BadRequest("Invalid branch name")
	}
	// Mirror the TS createProjectSchema repoMode: z.enum(["existing","init","clone"])
	// — it is REQUIRED (no .default()/.optional()), so a missing or out-of-enum
	// value must reject with 400 rather than silently defaulting to "existing".
	mode := input.RepoMode
	if mode != "existing" && mode != "init" && mode != "clone" {
		return Project{}, httpx.BadRequest("repoMode must be existing, init, or clone")
	}
	gitPath, err := expandPath(input.GitPath)
	if err != nil {
		return Project{}, err
	}
	if gitPath == "" {
		return Project{}, httpx.BadRequest("gitPath is required")
	}
	remoteURL := ""
	if mode == "clone" {
		if strings.TrimSpace(input.CloneURL) == "" {
			return Project{}, httpx.BadRequest("cloneUrl is required when repoMode is clone")
		}
		var credentials *gitx.CloneCredentials
		if strings.TrimSpace(input.CloneUsername) != "" || input.ClonePassword != "" {
			credentials = &gitx.CloneCredentials{Username: input.CloneUsername, Password: input.ClonePassword}
		}
		if err := s.cloneStreamingWithCredentials(ctx, input.CloneURL, gitPath, input.CloneBranch, credentials, input.Progress); err != nil {
			if isGitCredentialError(err) {
				_ = s.removeCloneFailureDirectory(gitPath)
			}
			return Project{}, err
		}
		remoteURL = gitx.RemoteURLWithoutCredentials(input.CloneURL)
	} else if mode == "init" {
		if err := s.Git.InitRepo(ctx, gitPath); err != nil {
			return Project{}, err
		}
	} else if mode == "existing" {
		if !s.Git.IsRepo(ctx, gitPath) {
			return Project{}, httpx.BadRequest("Path is not a git repository: " + gitPath)
		}
	} else {
		return Project{}, httpx.BadRequest("repoMode must be existing, init, or clone")
	}
	defaultBranch, err := s.currentBranch(ctx, gitPath)
	if err != nil || defaultBranch == "" {
		defaultBranch = "main"
	}
	flowMode := input.FlowMode
	if flowMode != "ruler" {
		flowMode = "classic"
	}
	now := db.NowISO()
	projectID := id.New()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO projects (id, name, description, status, flow_mode, git_path, remote_url, default_branch, created_at, updated_at) VALUES (?, ?, ?, 'active', ?, ?, ?, ?, ?, ?)`, projectID, input.Name, nullablePtr(input.Description), flowMode, gitPath, nullableString(remoteURL), defaultBranch, now, now)
	if err != nil {
		return Project{}, err
	}
	if s.Chapters != nil {
		core := config.Core{}
		if s.Settings != nil {
			core = s.Settings.Core()
		}
		_, _ = s.Chapters.CreateRootChapter(ctx, chapters.RootInput{ProjectID: projectID, Title: input.Name, GitPath: gitPath, DefaultBranch: defaultBranch, AutoNarrator: true, NarratorModel: core.Agent.DefaultModel, PermissionMode: core.Agent.DefaultPermissionMode})
	}
	if err := s.initializeProjectBackup(ctx, projectID, gitPath); err != nil {
		log.Printf("projects: failed to initialize project backup DB for %s: %v", projectID, err)
	}
	return s.Get(ctx, projectID)
}

func (s *Service) Get(ctx context.Context, projectID string) (Project, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+projectSelectColumns+` FROM projects WHERE id = ?`, projectID)
	p, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, httpx.NotFound("Project")
	}
	return p, err
}

func (s *Service) Update(ctx context.Context, projectID string, fields map[string]any) (Project, error) {
	if err := validateProjectUpdateFields(fields); err != nil {
		return Project{}, err
	}
	allowed := map[string]string{
		"name": "name", "description": "description", "status": "status", "defaultBranch": "default_branch",
		"startupScript": "startup_script", "copyFiles": "copy_files", "proxyDomain": "proxy_domain",
	}
	sets := []string{}
	args := []any{}
	for key, val := range fields {
		if key == "chapterSettings" {
			merged, err := s.mergedChapterSettings(ctx, projectID, val)
			if err != nil {
				return Project{}, err
			}
			sets = append(sets, "chapter_settings = ?")
			args = append(args, mustJSON(merged))
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
		return s.Get(ctx, projectID)
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, db.NowISO(), projectID)
	_, err := s.DB.ExecContext(ctx, `UPDATE projects SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return Project{}, err
	}
	return s.Get(ctx, projectID)
}

func (s *Service) mergedChapterSettings(ctx context.Context, projectID string, incoming any) (map[string]any, error) {
	var raw sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT chapter_settings FROM projects WHERE id = ?`, projectID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("Project")
	}
	if err != nil {
		return nil, err
	}
	merged := map[string]any{}
	if current, ok := jsondb.DecodeNull(raw).(map[string]any); ok {
		for key, value := range current {
			merged[key] = value
		}
	}
	next, err := validateProjectChapterSettings(incoming)
	if err != nil {
		return nil, err
	}
	for key, value := range next {
		merged[key] = value
	}
	return merged, nil
}

var gitBranchNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._\-/]+$`)

func validateProjectUpdateFields(fields map[string]any) error {
	if value, ok := fields["name"]; ok {
		name, valid := value.(string)
		if !valid || strings.TrimSpace(name) == "" || httpx.JSStringLength(name) > 200 {
			return httpx.ValidationError("name must be a non-empty string up to 200 characters")
		}
	}
	if value, ok := fields["description"]; ok {
		// TS updateProjectSchema.description is z.string().max(2000).optional() —
		// optional but NOT nullable, so an explicit null is rejected with 400
		// ("expected string, received null") rather than clearing the column.
		description, valid := value.(string)
		if !valid || httpx.JSStringLength(description) > 2000 {
			return httpx.ValidationError("description must be a string up to 2000 characters")
		}
	}
	if value, ok := fields["status"]; ok {
		status, valid := value.(string)
		if !valid || (status != "active" && status != "archived") {
			return httpx.ValidationError("status must be active or archived")
		}
	}
	if value, ok := fields["defaultBranch"]; ok {
		// gitBranchName.optional() is non-nullable: a present null is a 400, not a
		// branch clear (TS rejects null before the regex refine).
		branch, valid := value.(string)
		if !valid || !gitBranchNamePattern.MatchString(branch) {
			return httpx.ValidationError("Invalid branch name")
		}
	}
	for _, key := range []string{"startupScript", "copyFiles"} {
		if value, ok := fields[key]; ok && value != nil {
			text, valid := value.(string)
			if !valid || httpx.JSStringLength(text) > 5000 {
				return httpx.ValidationError(key + " must be a string up to 5000 characters")
			}
		}
	}
	if value, ok := fields["proxyDomain"]; ok && value != nil {
		domain, valid := value.(string)
		if !valid || httpx.JSStringLength(domain) > 200 {
			return httpx.ValidationError("proxyDomain must be a string up to 200 characters")
		}
	}
	return nil
}

func validateProjectChapterSettings(value any) (map[string]any, error) {
	settings, ok := jsondb.DecodeValue(value).(map[string]any)
	if !ok {
		return nil, httpx.ValidationError("chapterSettings must be an object")
	}
	allowed := map[string]bool{
		"autoCreateNarrator":       true,
		"commands":                 true,
		"routines":                 true,
		"whitelistDirs":            true,
		"blacklistDirs":            true,
		"commandWhitelist":         true,
		"commandBlacklist":         true,
		"requireReviewBeforeMerge": true,
	}
	filtered := map[string]any{}
	for key, item := range settings {
		if !allowed[key] {
			continue
		}
		filtered[key] = item
	}
	// Mirror the TypeScript updateProjectSchema.chapterSettings Zod bounds so that
	// nested arrays/objects are rejected with 400 before persistence, matching the
	// settings-route policy validators.
	if err := validateChapterSettingsCommands(filtered["commands"]); err != nil {
		return nil, err
	}
	if err := validateChapterSettingsPathRules(filtered["whitelistDirs"], "whitelistDirs", true); err != nil {
		return nil, err
	}
	if err := validateChapterSettingsPathRules(filtered["blacklistDirs"], "blacklistDirs", false); err != nil {
		return nil, err
	}
	if err := validateChapterSettingsCommandRules(filtered["commandWhitelist"], "commandWhitelist", false); err != nil {
		return nil, err
	}
	if err := validateChapterSettingsCommandRules(filtered["commandBlacklist"], "commandBlacklist", true); err != nil {
		return nil, err
	}
	// Mirror the remaining TS chapterSettings fields:
	// autoCreateNarrator/requireReviewBeforeMerge are z.boolean().optional() and
	// routines is z.object({disabledRoutines, enabledRoutines: z.array(z.string())
	// .optional()}).optional(). Go previously allow-listed these keys but never
	// type-checked them, so a non-boolean flag or a malformed routines object was
	// persisted instead of being rejected with 400 as TS Zod does.
	if err := chapterSettingsOptionalBool(filtered, "autoCreateNarrator", "chapterSettings"); err != nil {
		return nil, err
	}
	if err := chapterSettingsOptionalBool(filtered, "requireReviewBeforeMerge", "chapterSettings"); err != nil {
		return nil, err
	}
	if err := validateChapterSettingsRoutines(filtered["routines"]); err != nil {
		return nil, err
	}
	return filtered, nil
}

// validateChapterSettingsRoutines mirrors the TS chapterSettings.routines schema:
// z.object({disabledRoutines: z.array(z.string()).optional(), enabledRoutines:
// z.array(z.string()).optional()}).optional().
func validateChapterSettingsRoutines(value any) error {
	if value == nil {
		return nil
	}
	routines, ok := value.(map[string]any)
	if !ok {
		return httpx.ValidationError("chapterSettings.routines must be an object")
	}
	for _, key := range []string{"disabledRoutines", "enabledRoutines"} {
		raw, ok := routines[key]
		if !ok || raw == nil {
			continue
		}
		items, ok := raw.([]any)
		if !ok {
			return httpx.ValidationError("chapterSettings.routines." + key + " must be an array")
		}
		for i, item := range items {
			if _, ok := item.(string); !ok {
				return httpx.ValidationError("chapterSettings.routines." + key + "[" + strconv.Itoa(i) + "] must be a string")
			}
		}
	}
	return nil
}

var chapterCommandNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validateChapterSettingsCommands mirrors z.array(commandSchema).max(100).
func validateChapterSettingsCommands(value any) error {
	if value == nil {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return httpx.ValidationError("chapterSettings.commands must be an array")
	}
	if len(items) > 100 {
		return httpx.ValidationError("chapterSettings.commands must contain at most 100 items")
	}
	for i, item := range items {
		path := "chapterSettings.commands[" + strconv.Itoa(i) + "]"
		cmd, ok := item.(map[string]any)
		if !ok {
			return httpx.ValidationError(path + " must be an object")
		}
		if err := chapterSettingsRequiredString(cmd, "name", path, 1, 50); err != nil {
			return err
		}
		if name, _ := cmd["name"].(string); !chapterCommandNamePattern.MatchString(name) {
			return httpx.ValidationError(path + ".name must contain only letters, numbers, hyphens, and underscores")
		}
		if err := chapterSettingsRequiredString(cmd, "prompt", path, 1, 400000); err != nil {
			return err
		}
		if err := chapterSettingsOptionalStringMax(cmd, "description", path, 500); err != nil {
			return err
		}
		if err := chapterSettingsOptionalBool(cmd, "runBashFirst", path); err != nil {
			return err
		}
		if err := chapterSettingsOptionalStringMax(cmd, "bashCommand", path, 400000); err != nil {
			return err
		}
		if err := validateChapterSettingsCommandParams(cmd["params"], path); err != nil {
			return err
		}
		if err := validateChapterSettingsModelOverride(cmd["modelOverride"], path); err != nil {
			return err
		}
	}
	return nil
}

func validateChapterSettingsCommandParams(value any, parentPath string) error {
	if value == nil {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return httpx.ValidationError(parentPath + ".params must be an array")
	}
	if len(items) > 20 {
		return httpx.ValidationError(parentPath + ".params must contain at most 20 items")
	}
	for i, item := range items {
		path := parentPath + ".params[" + strconv.Itoa(i) + "]"
		param, ok := item.(map[string]any)
		if !ok {
			return httpx.ValidationError(path + " must be an object")
		}
		if err := chapterSettingsRequiredString(param, "name", path, 1, 50); err != nil {
			return err
		}
		if err := chapterSettingsOptionalStringMax(param, "description", path, 500); err != nil {
			return err
		}
		if err := chapterSettingsOptionalBool(param, "required", path); err != nil {
			return err
		}
		if err := chapterSettingsOptionalStringMax(param, "defaultValue", path, 1000); err != nil {
			return err
		}
	}
	return nil
}

func validateChapterSettingsModelOverride(value any, parentPath string) error {
	if value == nil {
		return nil
	}
	override, ok := value.(map[string]any)
	if !ok {
		return httpx.ValidationError(parentPath + ".modelOverride must be an object")
	}
	path := parentPath + ".modelOverride"
	if err := chapterSettingsRequiredString(override, "model", path, 1, 100); err != nil {
		return err
	}
	mode, ok := override["mode"].(string)
	if !ok || (mode != "temporary" && mode != "permanent") {
		return httpx.ValidationError(path + ".mode must be temporary or permanent")
	}
	return nil
}

// validateChapterSettingsPathRules mirrors z.array(whitelist/blacklistDirEntrySchema).max(50).
func validateChapterSettingsPathRules(value any, key string, whitelist bool) error {
	if value == nil {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return httpx.ValidationError("chapterSettings." + key + " must be an array")
	}
	if len(items) > 50 {
		return httpx.ValidationError("chapterSettings." + key + " must contain at most 50 items")
	}
	for i, item := range items {
		path := "chapterSettings." + key + "[" + strconv.Itoa(i) + "]"
		rule, ok := item.(map[string]any)
		if !ok {
			return httpx.ValidationError(path + " must be an object")
		}
		if err := chapterSettingsRequiredTrimmedString(rule, "path", path, 1, 4096); err != nil {
			return err
		}
		if whitelist {
			if err := chapterSettingsOptionalEnum(rule, "accessLevel", path, []string{"readOnly", "readWrite", "full"}); err != nil {
				return err
			}
		} else {
			if err := chapterSettingsOptionalEnum(rule, "denyLevel", path, []string{"denyWrite", "denyAll"}); err != nil {
				return err
			}
		}
		if err := chapterSettingsOptionalBool(rule, "enabled", path); err != nil {
			return err
		}
	}
	return nil
}

// validateChapterSettingsCommandRules mirrors z.array(command(White|Black)listEntrySchema).max(50).
func validateChapterSettingsCommandRules(value any, key string, includeDenyPrompt bool) error {
	if value == nil {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return httpx.ValidationError("chapterSettings." + key + " must be an array")
	}
	if len(items) > 50 {
		return httpx.ValidationError("chapterSettings." + key + " must contain at most 50 items")
	}
	for i, item := range items {
		path := "chapterSettings." + key + "[" + strconv.Itoa(i) + "]"
		rule, ok := item.(map[string]any)
		if !ok {
			return httpx.ValidationError(path + " must be an object")
		}
		if err := chapterSettingsRequiredTrimmedString(rule, "pattern", path, 1, 200); err != nil {
			return err
		}
		if includeDenyPrompt {
			if err := chapterSettingsOptionalStringMax(rule, "denyPrompt", path, 2000); err != nil {
				return err
			}
		}
		if err := chapterSettingsOptionalBool(rule, "enabled", path); err != nil {
			return err
		}
	}
	return nil
}

func chapterSettingsRequiredString(item map[string]any, key, path string, minLen, maxLen int) error {
	value, ok := item[key]
	if !ok {
		return httpx.ValidationError(path + "." + key + " is required")
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	count := httpx.JSStringLength(text)
	if count < minLen {
		return httpx.ValidationError(path + "." + key + " must not be empty")
	}
	if count > maxLen {
		return httpx.ValidationError(path + "." + key + " must be at most " + strconv.Itoa(maxLen) + " characters")
	}
	return nil
}

func chapterSettingsRequiredTrimmedString(item map[string]any, key, path string, minLen, maxLen int) error {
	value, ok := item[key]
	if !ok {
		return httpx.ValidationError(path + "." + key + " is required")
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	text = strings.TrimSpace(text)
	count := httpx.JSStringLength(text)
	if count < minLen {
		return httpx.ValidationError(path + "." + key + " must not be empty")
	}
	if count > maxLen {
		return httpx.ValidationError(path + "." + key + " must be at most " + strconv.Itoa(maxLen) + " characters")
	}
	return nil
}

func chapterSettingsOptionalStringMax(item map[string]any, key, path string, maxLen int) error {
	value, ok := item[key]
	if !ok || value == nil {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	if httpx.JSStringLength(text) > maxLen {
		return httpx.ValidationError(path + "." + key + " must be at most " + strconv.Itoa(maxLen) + " characters")
	}
	return nil
}

func chapterSettingsOptionalBool(item map[string]any, key, path string) error {
	value, ok := item[key]
	if !ok || value == nil {
		return nil
	}
	if _, ok := value.(bool); !ok {
		return httpx.ValidationError(path + "." + key + " must be a boolean")
	}
	return nil
}

func chapterSettingsOptionalEnum(item map[string]any, key, path string, allowed []string) error {
	value, ok := item[key]
	if !ok || value == nil {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	for _, candidate := range allowed {
		if text == candidate {
			return nil
		}
	}
	return httpx.ValidationError(path + "." + key + " must be one of " + strings.Join(allowed, ", "))
}

func (s *Service) Delete(ctx context.Context, projectID string) (DeleteResult, error) {
	project, err := s.Get(ctx, projectID)
	if err != nil {
		return DeleteResult{}, err
	}
	chaptersForDelete, err := s.projectDeleteChapters(ctx, projectID)
	if err != nil {
		return DeleteResult{}, err
	}
	chapterIDs := make([]string, 0, len(chaptersForDelete))
	for _, ch := range chaptersForDelete {
		chapterIDs = append(chapterIDs, ch.ID)
	}
	narratorIDs, err := s.projectNarratorIDs(ctx, projectID)
	if err != nil {
		return DeleteResult{}, err
	}
	terminalIDs, err := s.projectTerminalIDs(ctx, chapterIDs, narratorIDs)
	if err != nil {
		return DeleteResult{}, err
	}
	containerIDs, err := s.projectContainerIDs(ctx, chapterIDs)
	if err != nil {
		return DeleteResult{}, err
	}

	result := DeleteResult{OK: true, ProjectID: projectID, CleanupErrors: []DeleteCleanupError{}}
	for _, terminalID := range terminalIDs {
		if killer := s.terminalKiller(); killer != nil {
			if err := killer.Kill(ctx, terminalID); err != nil {
				result.addCleanupError("terminal", terminalID, err)
			}
		}
	}
	if len(containerIDs) > 0 {
		if err := s.removeContainers(ctx, containerIDs); err != nil {
			result.addCleanupError("containers", strings.Join(containerIDs, ","), err)
		}
	}
	if project.GitPath != nil && *project.GitPath != "" && s.canCleanWorktrees() {
		for _, ch := range chaptersForDelete {
			if ch.IsRoot || !ch.WorktreePath.Valid || ch.WorktreePath.String == "" {
				continue
			}
			if !safeProjectWorktreePath(*project.GitPath, ch.WorktreePath.String) {
				result.addCleanupError("worktree", ch.ID, fmt.Errorf("unsafe worktree path: %s", ch.WorktreePath.String))
				continue
			}
			if err := s.removeWorktree(ctx, *project.GitPath, ch.WorktreePath.String); err != nil {
				result.addCleanupError("worktree", ch.ID, err)
			}
		}
		if err := s.pruneWorktrees(ctx, *project.GitPath); err != nil {
			result.addCleanupError("worktree-prune", projectID, err)
		}
	}

	recentTabUpdates, err := s.deleteProjectRows(ctx, projectID, chapterIDs, narratorIDs)
	if err != nil {
		return result, err
	}
	s.broadcastRecentTabsSnapshots(recentTabUpdates)
	if project.ProxyDomain != nil && strings.TrimSpace(*project.ProxyDomain) != "" && s.ProxyCacheRefresher != nil {
		if err := s.ProxyCacheRefresher(ctx); err != nil {
			result.addCleanupError("proxy-cache", projectID, err)
		}
	}
	for _, cleanupErr := range result.CleanupErrors {
		log.Printf("project delete cleanup warning: project=%s scope=%s id=%s error=%s", projectID, cleanupErr.Scope, cleanupErr.ID, cleanupErr.Error)
	}
	return result, nil
}

type terminalKiller interface {
	Kill(ctx context.Context, terminalID string) error
}

func (r *DeleteResult) addCleanupError(scope, id string, err error) {
	if err == nil {
		return
	}
	r.CleanupErrors = append(r.CleanupErrors, DeleteCleanupError{Scope: scope, ID: id, Error: err.Error()})
}

func (s *Service) terminalKiller() terminalKiller {
	if s.Chapters == nil || s.Chapters.TerminalRuntime == nil {
		return nil
	}
	return s.Chapters.TerminalRuntime
}

func (s *Service) removeContainers(ctx context.Context, containerIDs []string) error {
	if s.ContainerRemover != nil {
		return s.ContainerRemover(ctx, containerIDs)
	}
	return removeProjectContainers(ctx, containerIDs)
}

func (s *Service) canCleanWorktrees() bool {
	return s.Git != nil || s.WorktreeRemover != nil || s.WorktreePruner != nil
}

func (s *Service) removeWorktree(ctx context.Context, repoPath, worktreePath string) error {
	if s.WorktreeRemover != nil {
		return s.WorktreeRemover(ctx, repoPath, worktreePath)
	}
	if s.Git == nil {
		return nil
	}
	return s.Git.RemoveWorktree(ctx, repoPath, worktreePath)
}

func (s *Service) pruneWorktrees(ctx context.Context, repoPath string) error {
	if s.WorktreePruner != nil {
		return s.WorktreePruner(ctx, repoPath)
	}
	if s.Git == nil {
		return nil
	}
	return s.Git.PruneWorktrees(ctx, repoPath)
}

func (s *Service) projectDeleteChapters(ctx context.Context, projectID string) ([]projectDeleteChapter, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, worktree_path, COALESCE(is_root, 0) FROM chapters WHERE project_id = ? ORDER BY is_root ASC, created_at ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []projectDeleteChapter{}
	for rows.Next() {
		var ch projectDeleteChapter
		var isRoot int
		if err := rows.Scan(&ch.ID, &ch.WorktreePath, &isRoot); err != nil {
			return nil, err
		}
		ch.IsRoot = isRoot != 0
		items = append(items, ch)
	}
	return items, rows.Err()
}

func (s *Service) projectNarratorIDs(ctx context.Context, projectID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `WITH RECURSIVE project_narrators(id) AS (
		SELECT n.id FROM narrators n JOIN chapters c ON c.id = n.chapter_id WHERE c.project_id = ?
		UNION
		SELECT child.id FROM narrators child JOIN project_narrators parent ON child.parent_narrator_id = parent.id
	)
	SELECT id FROM project_narrators`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func (s *Service) projectTerminalIDs(ctx context.Context, chapterIDs, narratorIDs []string) ([]string, error) {
	clauses := []string{}
	args := []any{}
	if len(chapterIDs) > 0 {
		clauses = append(clauses, `chapter_id IN (`+placeholders(len(chapterIDs))+`)`)
		args = append(args, stringsToAny(chapterIDs)...)
	}
	if len(narratorIDs) > 0 {
		clauses = append(clauses, `narrator_id IN (`+placeholders(len(narratorIDs))+`)`)
		args = append(args, stringsToAny(narratorIDs)...)
	}
	if len(clauses) == 0 {
		return []string{}, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM terminals WHERE `+strings.Join(clauses, ` OR `), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func (s *Service) projectContainerIDs(ctx context.Context, chapterIDs []string) ([]string, error) {
	if len(chapterIDs) == 0 {
		return []string{}, nil
	}
	args := stringsToAny(chapterIDs)
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT container_id FROM container_instances WHERE chapter_id IN (`+placeholders(len(chapterIDs))+`) AND TRIM(COALESCE(container_id, '')) <> ''`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStrings(rows)
}

func (s *Service) deleteProjectRows(ctx context.Context, projectID string, chapterIDs, narratorIDs []string) ([]recentTabsCleanupUpdate, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	deletedWorkspaceIDs, recentTabUpdates, err := cleanupProjectJSONReferences(ctx, tx, projectID, chapterIDs, narratorIDs)
	if err != nil {
		return nil, err
	}
	if len(narratorIDs) > 0 {
		if err := s.deleteNarratorRows(ctx, tx, narratorIDs); err != nil {
			return nil, err
		}
	}
	if len(chapterIDs) > 0 {
		if err := s.deleteChapterRows(ctx, tx, projectID, chapterIDs); err != nil {
			return nil, err
		}
	}
	if len(deletedWorkspaceIDs) > 0 {
		if err := deleteByIDs(ctx, tx, "workspaces", "id", deletedWorkspaceIDs); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM exploration_groups WHERE project_id = ?`, projectID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM hooks WHERE project_id = ?`, projectID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gateway_session_mappings WHERE project_id = ?`, projectID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM volume_snapshot_applications WHERE snapshot_id IN (SELECT id FROM volume_snapshots WHERE project_id = ?)`, projectID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM volume_snapshots WHERE project_id = ?`, projectID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, projectID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return recentTabUpdates, nil
}

func (s *Service) deleteNarratorRows(ctx context.Context, tx *sql.Tx, narratorIDs []string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE narrators SET parent_narrator_id = NULL, fork_message_id = NULL, prune_boundary_message_id = NULL WHERE id IN (`+placeholders(len(narratorIDs))+`)`, stringsToAny(narratorIDs)...); err != nil {
		return err
	}
	for _, table := range []string{"terminal_view_state", "terminal_tabs", "terminals", "narrator_goals", "narrator_file_snapshots", "narrator_patches", "narrator_buffered_messages", "narrator_sidecars", "narrator_tool_calls", "narrator_message_refs", "api_requests", "narrator_whitelist_dirs", "narrator_blacklist_dirs", "narrator_whitelist_cmds", "narrator_blacklist_cmds", "benchmark_task_results"} {
		if err := deleteByIDs(ctx, tx, table, "narrator_id", narratorIDs); err != nil {
			return err
		}
	}
	if err := deleteByIDs(ctx, tx, "background_tasks", "parent_narrator_id", narratorIDs); err != nil {
		return err
	}
	if err := deleteByIDs(ctx, tx, "background_tasks", "subagent_narrator_id", narratorIDs); err != nil {
		return err
	}
	if err := deleteByIDs(ctx, tx, "gateway_session_mappings", "narrator_id", narratorIDs); err != nil {
		return err
	}
	if err := deleteByIDs(ctx, tx, "chapter_commits", "narrator_id", narratorIDs); err != nil {
		return err
	}
	if err := deleteByIDs(ctx, tx, "narrator_messages", "narrator_id", narratorIDs); err != nil {
		return err
	}
	return deleteByIDs(ctx, tx, "narrators", "id", narratorIDs)
}

func (s *Service) deleteChapterRows(ctx context.Context, tx *sql.Tx, projectID string, chapterIDs []string) error {
	for _, table := range []string{"terminal_view_state", "terminal_tabs", "terminals", "container_instances", "port_allocations", "volume_snapshot_applications"} {
		if err := deleteByIDs(ctx, tx, table, "chapter_id", chapterIDs); err != nil {
			return err
		}
	}
	if err := deleteByIDs(ctx, tx, "chapter_commits", "chapter_id", chapterIDs); err != nil {
		return err
	}
	if err := deleteByIDs(ctx, tx, "review_conclusions", "review_chapter_id", chapterIDs); err != nil {
		return err
	}
	if err := deleteByIDs(ctx, tx, "review_conclusions", "source_chapter_id", chapterIDs); err != nil {
		return err
	}
	if err := deleteByIDs(ctx, tx, "volume_snapshots", "source_chapter_id", chapterIDs); err != nil {
		return err
	}
	if err := deleteByIDs(ctx, tx, "gateway_session_mappings", "chapter_id", chapterIDs); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM merge_sessions WHERE target_chapter_id IN (`+placeholders(len(chapterIDs))+`) OR current_source_chapter_id IN (`+placeholders(len(chapterIDs))+`)`, append(stringsToAny(chapterIDs), stringsToAny(chapterIDs)...)...); err != nil {
		return err
	}
	if err := deleteMergeSessionsBySources(ctx, tx, chapterIDs); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chapter_edges WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chapters SET parent_chapter_id = NULL, merged_into_chapter_id = NULL WHERE project_id = ?`, projectID); err != nil {
		return err
	}
	return deleteByIDs(ctx, tx, "chapters", "id", chapterIDs)
}

func deleteByIDs(ctx context.Context, tx *sql.Tx, table, column string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+column+` IN (`+placeholders(len(ids))+`)`, stringsToAny(ids)...)
	return err
}

func deleteMergeSessionsBySources(ctx context.Context, tx *sql.Tx, chapterIDs []string) error {
	if len(chapterIDs) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM merge_sessions WHERE EXISTS (SELECT 1 FROM json_each(merge_sessions.source_chapter_ids) WHERE value IN (`+placeholders(len(chapterIDs))+`))`, stringsToAny(chapterIDs)...)
	return err
}

func cleanupProjectJSONReferences(ctx context.Context, tx *sql.Tx, projectID string, chapterIDs, narratorIDs []string) ([]string, []recentTabsCleanupUpdate, error) {
	deletedWorkspaceIDs, err := referencedWorkspaceIDs(ctx, tx, projectID, chapterIDs, narratorIDs)
	if err != nil {
		return nil, nil, err
	}
	recentTabUpdates, err := cleanupRecentTabs(ctx, tx, projectID, chapterIDs, narratorIDs, deletedWorkspaceIDs)
	if err != nil {
		return nil, nil, err
	}
	return deletedWorkspaceIDs, recentTabUpdates, nil
}

func referencedWorkspaceIDs(ctx context.Context, tx *sql.Tx, projectID string, chapterIDs, narratorIDs []string) ([]string, error) {
	ids := stringSet(append(append([]string{projectID}, chapterIDs...), narratorIDs...))
	rows, err := tx.QueryContext(ctx, `SELECT id, tree FROM workspaces`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deleted := []string{}
	for rows.Next() {
		var workspaceID, rawTree string
		if err := rows.Scan(&workspaceID, &rawTree); err != nil {
			return nil, err
		}
		var tree any
		if json.Unmarshal([]byte(rawTree), &tree) == nil && jsonValueReferences(tree, ids) {
			deleted = append(deleted, workspaceID)
		}
	}
	return deleted, rows.Err()
}

type recentTabsCleanupUpdate struct {
	userID string
	tabs   string
	items  []map[string]any
}

func cleanupRecentTabs(ctx context.Context, tx *sql.Tx, projectID string, chapterIDs, narratorIDs, workspaceIDs []string) ([]recentTabsCleanupUpdate, error) {
	projectIDs := stringSet([]string{projectID})
	chapterSet := stringSet(chapterIDs)
	narratorSet := stringSet(narratorIDs)
	workspaceSet := stringSet(workspaceIDs)
	rows, err := tx.QueryContext(ctx, `SELECT user_id, recent_tabs FROM user_preferences`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	updates := []recentTabsCleanupUpdate{}
	for rows.Next() {
		var userID, rawTabs string
		if err := rows.Scan(&userID, &rawTabs); err != nil {
			return nil, err
		}
		var tabs []map[string]any
		if json.Unmarshal([]byte(rawTabs), &tabs) != nil {
			continue
		}
		filtered := make([]map[string]any, 0, len(tabs))
		changed := false
		for _, tab := range tabs {
			if recentTabReferences(tab, projectIDs, chapterSet, narratorSet, workspaceSet) {
				changed = true
				continue
			}
			filtered = append(filtered, tab)
		}
		if changed {
			data, _ := json.Marshal(filtered)
			updates = append(updates, recentTabsCleanupUpdate{userID: userID, tabs: string(data), items: filtered})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, item := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE user_preferences SET recent_tabs = ?, updated_at = ? WHERE user_id = ?`, item.tabs, db.NowISO(), item.userID); err != nil {
			return nil, err
		}
	}
	return updates, nil
}

func (s *Service) broadcastRecentTabsSnapshots(updates []recentTabsCleanupUpdate) {
	if s.BroadcastUser == nil {
		return
	}
	revision := time.Now().UnixMilli()
	for _, update := range updates {
		s.BroadcastUser(update.userID, map[string]any{
			"type":     "user:recent_tabs_snapshot",
			"tabs":     update.items,
			"revision": revision,
		})
	}
}

func recentTabReferences(tab map[string]any, projectIDs, chapterIDs, narratorIDs, workspaceIDs map[string]bool) bool {
	typeValue := stringField(tab, "type")
	idValue := stringField(tab, "id")
	if typeValue == "project" && projectIDs[idValue] {
		return true
	}
	if typeValue == "chapter" && chapterIDs[idValue] {
		return true
	}
	if (typeValue == "narrator" || typeValue == "subagent") && narratorIDs[idValue] {
		return true
	}
	if typeValue == "workspace" && workspaceIDs[idValue] {
		return true
	}
	if chapterIDs[stringField(tab, "chapterId")] || narratorIDs[stringField(tab, "narratorId")] || narratorIDs[stringField(tab, "parentNarratorId")] || workspaceIDs[stringField(tab, "workspaceId")] {
		return true
	}
	return false
}

func jsonValueReferences(value any, ids map[string]bool) bool {
	switch typed := value.(type) {
	case string:
		return ids[typed]
	case []any:
		for _, item := range typed {
			if jsonValueReferences(item, ids) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if jsonValueReferences(item, ids) {
				return true
			}
		}
	}
	return false
}

func removeProjectContainers(ctx context.Context, containerIDs []string) error {
	args := append([]string{"rm", "-f", "-v"}, containerIDs...)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "podman", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func safeProjectWorktreePath(repoPath, worktreePath string) bool {
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

func scanStrings(rows *sql.Rows) ([]string, error) {
	items := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		if value != "" {
			items = append(items, value)
		}
	}
	return items, rows.Err()
}

func stringsToAny(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

func stringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func stringField(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}

func (s *Service) BackupStatus(ctx context.Context, projectID string) (map[string]any, error) {
	project, err := s.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.GitPath == nil || *project.GitPath == "" {
		return nil, httpx.BadRequest("Project has no git path configured")
	}
	path := projectDBPath(*project.GitPath)
	if _, statErr := os.Stat(path); statErr != nil {
		return map[string]any{
			"projectId":    projectID,
			"backupExists": false,
			"backupPath":   path,
			"scope":        projectBackupScope(),
			"synced":       map[string]any{},
		}, nil
	}
	pdb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer pdb.Close()
	synced := map[string]any{}
	for _, table := range projectBackupTables {
		count, err := countRows(ctx, pdb, table.name)
		if err == nil {
			synced[table.name] = count
		}
	}
	return map[string]any{"projectId": projectID, "backupExists": true, "backupPath": path, "scope": projectBackupScope(), "synced": synced}, nil
}

func (s *Service) SyncBackup(ctx context.Context, projectID string) (map[string]any, error) {
	project, err := s.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.GitPath == nil || *project.GitPath == "" {
		return nil, httpx.BadRequest("Project has no git path configured")
	}
	path := projectDBPath(*project.GitPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	pdb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer pdb.Close()
	if err := ensureProjectBackupSchema(ctx, pdb); err != nil {
		return nil, err
	}
	tx, err := pdb.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	synced := map[string]any{}
	for _, table := range projectBackupTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table.name); err != nil {
			return nil, err
		}
		count, err := copyProjectRows(ctx, s.DB, tx, table, projectID)
		if err != nil {
			return nil, err
		}
		synced[table.name] = count
	}
	metadata, _ := json.Marshal(map[string]any{"projectId": projectID, "scope": projectBackupScope(), "synced": synced, "syncedAt": db.NowISO()})
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO project_sync_metadata (key,value) VALUES ('last_sync', ?)`, string(metadata)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return map[string]any{"ok": true, "projectId": projectID, "backupPath": path, "scope": projectBackupScope(), "synced": synced}, nil
}

func (s *Service) ImportBackup(ctx context.Context, gitPath string) (map[string]any, error) {
	path, err := expandPath(gitPath)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, httpx.BadRequest("gitPath is required")
	}
	// Mirror the TS /api/projects/import route (server/routes/project-db.ts):
	// after `~` expansion + resolve, it checks `!existsSync(gitPath)` and throws a
	// 400 ValidationError "Path does not exist: <resolved>" BEFORE attempting the
	// import. Go previously skipped the directory check and reported a 404
	// "Project database" not-found instead, diverging on both status and message.
	if _, statErr := os.Stat(path); statErr != nil {
		return nil, httpx.BadRequest("Path does not exist: " + path)
	}
	backupPath := projectDBPath(path)
	if _, err := os.Stat(backupPath); err != nil {
		return nil, httpx.NotFound("Project database")
	}
	pdb, err := sql.Open("sqlite", backupPath)
	if err != nil {
		return nil, err
	}
	defer pdb.Close()
	if err := ensureProjectBackupSchema(ctx, pdb); err != nil {
		return nil, err
	}
	projectID, err := firstProjectID(ctx, pdb)
	if err != nil {
		return nil, err
	}
	var exists int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, projectID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("query existing project before import: %w", err)
	}
	backupCounts := map[string]any{}
	for _, table := range projectBackupTables {
		if count, err := countRows(ctx, pdb, table.name); err == nil {
			backupCounts[table.name] = count
		}
	}
	if exists > 0 {
		return map[string]any{"ok": true, "skipped": true, "projectId": projectID, "reason": "already exists", "scope": projectBackupScope(), "available": backupCounts}, nil
	}
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return nil, err
	}
	defer conn.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	imported := map[string]any{}
	for _, table := range projectBackupTables {
		if ok, _ := tableExists(ctx, pdb, table.name); !ok {
			continue
		}
		count, err := copyAllRows(ctx, pdb, tx, table.name, table.columns)
		if err != nil {
			return nil, err
		}
		if table.name == "projects" && count > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE projects SET git_path = ? WHERE id = ?`, path, projectID); err != nil {
				return nil, err
			}
		}
		imported[table.name] = count
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return map[string]any{"ok": true, "skipped": false, "projectId": projectID, "scope": projectBackupScope(), "imported": imported}, nil
}

func projectDBPath(gitPath string) string { return filepath.Join(gitPath, ".narrafork", "project.db") }

type projectBackupColumn struct {
	name string
	typ  string
}

type projectBackupTable struct {
	name    string
	columns []string
	where   string
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

const projectNarratorIDSubquery = `
	SELECT n.id FROM narrators n
	LEFT JOIN narrators parent ON parent.id = n.parent_narrator_id
	LEFT JOIN chapters c ON c.id = n.chapter_id
	LEFT JOIN chapters parent_c ON parent_c.id = parent.chapter_id
	WHERE c.project_id = ? OR parent_c.project_id = ?
`

const projectNarratorWhere = `narrator_id IN (` + projectNarratorIDSubquery + `)`

const projectMessageWhere = `(
	narrator_id IN (` + projectNarratorIDSubquery + `)
	OR id IN (
		SELECT r.message_id FROM narrator_message_refs r
		WHERE r.narrator_id IN (` + projectNarratorIDSubquery + `)
	)
)`

var projectBackupSchemas = map[string][]projectBackupColumn{
	"projects": cols(
		col("id", "TEXT PRIMARY KEY"), col("name", "TEXT NOT NULL"), col("description", "TEXT"),
		col("status", "TEXT NOT NULL DEFAULT 'active'"), col("flow_mode", "TEXT NOT NULL DEFAULT 'classic'"),
		col("git_path", "TEXT"), col("remote_url", "TEXT"), col("default_branch", "TEXT DEFAULT 'main'"),
		col("startup_script", "TEXT"), col("copy_files", "TEXT"), col("chapter_settings", "TEXT"),
		col("proxy_domain", "TEXT"), col("created_at", "TEXT NOT NULL"), col("updated_at", "TEXT NOT NULL"),
	),
	"exploration_groups": cols(
		col("id", "TEXT PRIMARY KEY"), col("project_id", "TEXT NOT NULL"), col("title", "TEXT NOT NULL"),
		col("description", "TEXT"), col("base_chapter_id", "TEXT"), col("status", "TEXT NOT NULL DEFAULT 'active'"),
		col("decided_chapter_id", "TEXT"), col("created_at", "TEXT NOT NULL"), col("updated_at", "TEXT NOT NULL"),
	),
	"chapters": cols(
		col("id", "TEXT PRIMARY KEY"), col("project_id", "TEXT NOT NULL"), col("title", "TEXT NOT NULL"),
		col("description", "TEXT"), col("status", "TEXT NOT NULL DEFAULT 'active'"), col("role", "TEXT NOT NULL DEFAULT 'branch'"),
		col("branch", "TEXT NOT NULL"), col("worktree_path", "TEXT"), col("base_branch", "TEXT NOT NULL"),
		col("parent_chapter_id", "TEXT"), col("fork_point", "TEXT"), col("merged_into_chapter_id", "TEXT"),
		col("merge_commit_sha", "TEXT"), col("merge_strategy", "TEXT"), col("pre_merge_target_sha", "TEXT"),
		col("container_config", "TEXT"), col("exploration_group_id", "TEXT"), col("is_root", "INTEGER DEFAULT 0"),
		col("head_commit_sha", "TEXT"), col("start_commit_sha", "TEXT"), col("commit_count", "INTEGER DEFAULT 0"),
		col("color", "TEXT"), col("group_label", "TEXT"), col("pinned", "INTEGER DEFAULT 0"),
		col("anchor_commit_sha", "TEXT"), col("axis_offset", "REAL DEFAULT 0"), col("cross_offset", "REAL DEFAULT 0"),
		col("panel_expanded", "INTEGER DEFAULT 0"), col("panel_width", "REAL"), col("panel_height", "REAL"),
		col("review_source_chapter_id", "TEXT"), col("review_status", "TEXT"), col("last_accessed_at", "TEXT"),
		col("created_at", "TEXT NOT NULL"), col("updated_at", "TEXT NOT NULL"),
	),
	"chapter_edges": cols(
		col("id", "TEXT PRIMARY KEY"), col("project_id", "TEXT NOT NULL"), col("source_id", "TEXT NOT NULL"),
		col("target_id", "TEXT NOT NULL"), col("type", "TEXT NOT NULL"), col("metadata", "TEXT"),
		col("created_at", "TEXT NOT NULL"),
	),
	"chapter_commits": cols(
		col("id", "TEXT PRIMARY KEY"), col("chapter_id", "TEXT NOT NULL"), col("sha", "TEXT NOT NULL"),
		col("message", "TEXT NOT NULL"), col("full_message", "TEXT"), col("author_name", "TEXT"),
		col("author_email", "TEXT"), col("authored_at", "TEXT NOT NULL"), col("source", "TEXT NOT NULL DEFAULT 'manual'"),
		col("narrator_id", "TEXT"), col("narrator_message_id", "TEXT"), col("files_changed", "INTEGER"),
		col("lines_added", "INTEGER"), col("lines_removed", "INTEGER"), col("created_at", "TEXT NOT NULL"),
	),
	"narrators": cols(
		col("id", "TEXT PRIMARY KEY"), col("chapter_id", "TEXT"), col("api_conversation_id", "TEXT"),
		col("fork_message_id", "TEXT"), col("type", "TEXT NOT NULL DEFAULT 'primary'"), col("subagent_type", "TEXT"),
		col("title", "TEXT"), col("inherit_mode", "TEXT NOT NULL DEFAULT 'fresh'"), col("parent_narrator_id", "TEXT"),
		col("context_summary", "TEXT"), col("model", "TEXT DEFAULT 'claude-sonnet-4.5'"), col("pending_model_restore", "TEXT"),
		col("system_prompt", "TEXT"), col("permission_mode", "TEXT DEFAULT 'default'"), col("previous_permission_mode", "TEXT"),
		col("plan_file_id", "TEXT"), col("reasoning_effort", "TEXT"), col("fast_mode", "INTEGER NOT NULL DEFAULT 0"),
		col("relaxed_plan", "INTEGER NOT NULL DEFAULT 0"), col("plan_reflection_auto_approve_override", "TEXT NOT NULL DEFAULT 'inherit'"),
		col("danger_reflection_override", "TEXT NOT NULL DEFAULT 'inherit'"), col("message_count", "INTEGER DEFAULT 0"),
		col("total_cost_usd", "REAL DEFAULT 0"), col("last_message_at", "TEXT"), col("status", "TEXT NOT NULL DEFAULT 'idle'"),
		col("substatus", "TEXT NOT NULL DEFAULT '[]'"), col("plan_mode", "INTEGER NOT NULL DEFAULT 0"),
		col("cwd", "TEXT"), col("error_message", "TEXT"), col("todos_json", "TEXT"), col("todos_tool_use_id", "TEXT"),
		col("prune_boundary_message_id", "TEXT"), col("pruned_percent", "INTEGER"), col("prune_enabled", "INTEGER NOT NULL DEFAULT 1"),
		col("enabled_tools", "TEXT"), col("variant", "TEXT NOT NULL DEFAULT 'primary'"), col("traits", "TEXT NOT NULL DEFAULT '[]'"),
		col("is_background", "INTEGER NOT NULL DEFAULT 0"), col("background_status", "TEXT"), col("background_result", "TEXT"),
		col("background_completed_at", "TEXT"), col("is_ask_in_passing", "INTEGER NOT NULL DEFAULT 0"), col("turn_started_at", "TEXT"),
		col("message_version", "INTEGER NOT NULL DEFAULT 0"), col("created_at", "TEXT NOT NULL"), col("updated_at", "TEXT NOT NULL"),
	),
	"narrator_goals": cols(
		col("id", "TEXT PRIMARY KEY"), col("narrator_id", "TEXT NOT NULL"), col("objective", "TEXT NOT NULL"),
		col("status", "TEXT NOT NULL DEFAULT 'pending'"), col("sort_order", "INTEGER NOT NULL DEFAULT 0"),
		col("tokens_used", "INTEGER NOT NULL DEFAULT 0"), col("time_used_seconds", "INTEGER NOT NULL DEFAULT 0"),
		col("created_by", "TEXT"), col("completed_at", "TEXT"), col("created_at", "TEXT NOT NULL"), col("updated_at", "TEXT NOT NULL"),
	),
	"narrator_messages": cols(
		col("id", "TEXT PRIMARY KEY"), col("narrator_id", "TEXT NOT NULL"), col("sdk_message_uuid", "TEXT"),
		col("parent_tool_use_id", "TEXT"), col("role", "TEXT NOT NULL"), col("content_json", "TEXT NOT NULL"),
		col("content_text", "TEXT"), col("tokens_in", "INTEGER"), col("cost_usd", "REAL"), col("turn_usage_json", "TEXT"),
		col("provider", "TEXT"), col("model", "TEXT"), col("output_tokens", "INTEGER"), col("cached_input_tokens", "INTEGER"),
		col("cache_creation_input_tokens", "INTEGER"), col("cache_creation_5m_tokens", "INTEGER"),
		col("cache_creation_1h_tokens", "INTEGER"), col("reasoning_tokens", "INTEGER"), col("ttft_ms", "INTEGER"),
		col("duration_ms", "INTEGER"), col("context_percent", "REAL"), col("meter_usage", "REAL"), col("meter_unit", "TEXT"),
		col("commit_sha", "TEXT"), col("command_text", "TEXT"), col("created_by", "TEXT"), col("created_at", "TEXT NOT NULL"),
	),
	"narrator_message_refs": cols(
		col("id", "TEXT PRIMARY KEY"), col("narrator_id", "TEXT NOT NULL"), col("message_id", "TEXT NOT NULL"),
		col("seq", "INTEGER NOT NULL"), col("is_compact", "INTEGER NOT NULL DEFAULT 0"), col("pruned_percent", "INTEGER"),
		col("segment_compact_id", "TEXT"),
	),
	"narrator_sidecars": cols(
		col("id", "TEXT PRIMARY KEY"), col("narrator_id", "TEXT NOT NULL"), col("message_id", "TEXT"),
		col("tool_use_id", "TEXT"), col("target", "TEXT NOT NULL"), col("source", "TEXT NOT NULL"),
		col("content", "TEXT NOT NULL"), col("order_index", "INTEGER NOT NULL DEFAULT 0"), col("created_at", "TEXT NOT NULL"),
	),
	"narrator_tool_calls": cols(
		col("id", "TEXT PRIMARY KEY"), col("narrator_id", "TEXT NOT NULL"), col("message_id", "TEXT NOT NULL"),
		col("tool_use_id", "TEXT NOT NULL"), col("tool_name", "TEXT NOT NULL"), col("input_json", "TEXT"), col("output_json", "TEXT"),
		col("status", "TEXT NOT NULL DEFAULT 'initializing'"), col("duration_ms", "INTEGER"), col("stream_started_at", "TEXT"),
		col("permission_started_at", "TEXT"), col("execution_started_at", "TEXT"), col("completed_at", "TEXT"),
		col("error_message", "TEXT"), col("permission_decided_by", "TEXT"), col("permission_decided_at", "TEXT"),
		col("permission_deny_message", "TEXT"), col("permission_decision_reason", "TEXT"), col("permission_suggestions", "TEXT"),
		col("is_background", "INTEGER NOT NULL DEFAULT 0"), col("input_tokens", "INTEGER NOT NULL DEFAULT 0"),
		col("output_tokens", "INTEGER NOT NULL DEFAULT 0"), col("cache_creation_tokens", "INTEGER NOT NULL DEFAULT 0"),
		col("cache_read_tokens", "INTEGER NOT NULL DEFAULT 0"), col("cache_creation_5m_tokens", "INTEGER NOT NULL DEFAULT 0"),
		col("cache_creation_1h_tokens", "INTEGER NOT NULL DEFAULT 0"), col("input_cost", "REAL NOT NULL DEFAULT 0"),
		col("output_cost", "REAL NOT NULL DEFAULT 0"), col("cache_creation_cost", "REAL NOT NULL DEFAULT 0"),
		col("cache_read_cost", "REAL NOT NULL DEFAULT 0"), col("total_cost", "REAL NOT NULL DEFAULT 0"), col("provider", "TEXT"),
		col("model", "TEXT"), col("result_message_id", "TEXT"), col("created_at", "TEXT NOT NULL"),
	),
	"narrator_file_snapshots": cols(
		col("id", "TEXT PRIMARY KEY"), col("narrator_id", "TEXT NOT NULL"), col("file_path", "TEXT NOT NULL"),
		col("original_content", "TEXT"), col("created_at", "TEXT NOT NULL"),
	),
	"narrator_patches": cols(
		col("id", "TEXT PRIMARY KEY"), col("narrator_id", "TEXT NOT NULL"), col("message_id", "TEXT NOT NULL"),
		col("tool_use_id", "TEXT NOT NULL"), col("before_hash", "TEXT NOT NULL"), col("after_hash", "TEXT NOT NULL"),
		col("files_json", "TEXT NOT NULL"), col("created_at", "TEXT NOT NULL"),
	),
	"narrator_buffered_messages": cols(
		col("id", "TEXT PRIMARY KEY"), col("narrator_id", "TEXT NOT NULL"), col("text", "TEXT NOT NULL"),
		col("images_json", "TEXT"), col("command_text", "TEXT"), col("created_by", "TEXT"), col("creator_json", "TEXT"),
		col("text_file_paths_json", "TEXT"), col("priority", "INTEGER NOT NULL DEFAULT 0"), col("seq", "INTEGER NOT NULL"),
		col("buffered_at", "TEXT NOT NULL"),
	),
	"background_tasks": cols(
		col("id", "TEXT PRIMARY KEY"), col("parent_narrator_id", "TEXT NOT NULL"), col("type", "TEXT NOT NULL"),
		col("status", "TEXT NOT NULL"), col("command", "TEXT"), col("exit_code", "INTEGER"), col("subagent_narrator_id", "TEXT"),
		col("subagent_type", "TEXT"), col("tool_use_id", "TEXT"), col("alias", "TEXT"), col("title", "TEXT"),
		col("output", "TEXT"), col("output_bytes", "INTEGER NOT NULL DEFAULT 0"), col("output_truncated", "INTEGER NOT NULL DEFAULT 0"),
		col("notified", "INTEGER NOT NULL DEFAULT 0"), col("started_at", "TEXT NOT NULL"), col("completed_at", "TEXT"),
		col("created_at", "TEXT NOT NULL"), col("updated_at", "TEXT NOT NULL"),
	),
	"merge_sessions": cols(
		col("id", "TEXT PRIMARY KEY"), col("target_chapter_id", "TEXT NOT NULL"), col("source_chapter_ids", "TEXT NOT NULL"),
		col("strategy", "TEXT NOT NULL DEFAULT 'merge'"), col("status", "TEXT NOT NULL"), col("current_index", "INTEGER NOT NULL DEFAULT 0"),
		col("merged_count", "INTEGER NOT NULL DEFAULT 0"), col("current_source_chapter_id", "TEXT"), col("conflict_files", "TEXT"),
		col("error", "TEXT"), col("locale", "TEXT"), col("created_at", "TEXT NOT NULL"), col("updated_at", "TEXT NOT NULL"),
	),
	"review_conclusions": cols(
		col("id", "TEXT PRIMARY KEY"), col("review_chapter_id", "TEXT NOT NULL"), col("source_chapter_id", "TEXT NOT NULL"),
		col("verdict", "TEXT NOT NULL"), col("findings_json", "TEXT"), col("created_at", "TEXT NOT NULL"),
	),
	"terminals": cols(
		col("id", "TEXT PRIMARY KEY"), col("chapter_id", "TEXT"), col("narrator_id", "TEXT"), col("name", "TEXT NOT NULL"),
		col("cwd", "TEXT"), col("dtach_socket", "TEXT"), col("status", "TEXT NOT NULL DEFAULT 'running'"), col("exit_code", "INTEGER"),
		col("graph_opened", "INTEGER NOT NULL DEFAULT 0"), col("graph_x", "REAL"), col("graph_y", "REAL"),
		col("graph_width", "REAL"), col("graph_height", "REAL"), col("created_at", "TEXT NOT NULL"),
	),
	"terminal_tabs": cols(
		col("id", "TEXT PRIMARY KEY"), col("chapter_id", "TEXT"), col("narrator_id", "TEXT"), col("name", "TEXT NOT NULL"),
		col("sort_order", "INTEGER NOT NULL DEFAULT 0"), col("created_at", "TEXT NOT NULL"),
	),
	"terminal_view_state": cols(
		col("id", "TEXT PRIMARY KEY"), col("user_id", "TEXT NOT NULL"), col("chapter_id", "TEXT"), col("narrator_id", "TEXT"),
		col("layout", "TEXT NOT NULL DEFAULT 'single'"), col("active_tab_id", "TEXT"), col("panel_assignments", "TEXT"),
		col("updated_at", "TEXT NOT NULL"),
	),
	"container_instances": cols(
		col("id", "TEXT PRIMARY KEY"), col("chapter_id", "TEXT NOT NULL"), col("container_id", "TEXT"),
		col("service_name", "TEXT NOT NULL"), col("status", "TEXT NOT NULL DEFAULT 'created'"), col("host_port", "INTEGER"),
		col("container_port", "INTEGER"), col("proxy_label", "TEXT"), col("container_ip", "TEXT"), col("volume_name", "TEXT"),
		col("created_at", "TEXT NOT NULL"), col("updated_at", "TEXT NOT NULL"),
	),
	"port_allocations": cols(
		col("port", "INTEGER PRIMARY KEY"), col("chapter_id", "TEXT"), col("service_name", "TEXT"), col("allocated_at", "TEXT NOT NULL"),
	),
	"volume_snapshots": cols(
		col("id", "TEXT PRIMARY KEY"), col("project_id", "TEXT NOT NULL"), col("name", "TEXT NOT NULL"),
		col("description", "TEXT"), col("source_chapter_id", "TEXT"), col("service_name", "TEXT NOT NULL"),
		col("container_path", "TEXT NOT NULL"), col("size_bytes", "INTEGER"), col("created_by", "TEXT"),
		col("created_at", "TEXT NOT NULL"), col("updated_at", "TEXT NOT NULL"),
	),
	"volume_snapshot_applications": cols(
		col("id", "TEXT PRIMARY KEY"), col("snapshot_id", "TEXT NOT NULL"), col("chapter_id", "TEXT NOT NULL"),
		col("applied_at", "TEXT NOT NULL"), col("applied_by", "TEXT"),
	),
}

var projectBackupTables = []projectBackupTable{
	projectTable("projects", "id = ?"),
	projectTable("exploration_groups", "project_id = ?"),
	projectTable("chapters", "project_id = ?"),
	projectTable("chapter_edges", "project_id = ?"),
	projectTable("chapter_commits", "chapter_id IN (SELECT id FROM chapters WHERE project_id = ?)"),
	projectTable("narrators", `id IN (`+projectNarratorIDSubquery+`)`),
	projectTable("narrator_goals", projectNarratorWhere),
	projectTable("narrator_messages", projectMessageWhere),
	projectTable("narrator_message_refs", projectNarratorWhere),
	projectTable("narrator_sidecars", projectNarratorWhere),
	projectTable("narrator_tool_calls", projectNarratorWhere),
	projectTable("narrator_file_snapshots", projectNarratorWhere),
	projectTable("narrator_patches", projectNarratorWhere),
	projectTable("narrator_buffered_messages", projectNarratorWhere),
	projectTable("background_tasks", `parent_narrator_id IN (`+projectNarratorIDSubquery+`) OR subagent_narrator_id IN (`+projectNarratorIDSubquery+`)`),
	projectTable("merge_sessions", "target_chapter_id IN (SELECT id FROM chapters WHERE project_id = ?)"),
	projectTable("review_conclusions", `review_chapter_id IN (SELECT id FROM chapters WHERE project_id = ?) OR source_chapter_id IN (SELECT id FROM chapters WHERE project_id = ?)`),
	projectTable("terminals", `chapter_id IN (SELECT id FROM chapters WHERE project_id = ?) OR narrator_id IN (`+projectNarratorIDSubquery+`)`),
	projectTable("terminal_tabs", `chapter_id IN (SELECT id FROM chapters WHERE project_id = ?) OR narrator_id IN (`+projectNarratorIDSubquery+`)`),
	projectTable("terminal_view_state", `chapter_id IN (SELECT id FROM chapters WHERE project_id = ?) OR narrator_id IN (`+projectNarratorIDSubquery+`)`),
	projectTable("container_instances", "chapter_id IN (SELECT id FROM chapters WHERE project_id = ?)"),
	projectTable("port_allocations", "chapter_id IN (SELECT id FROM chapters WHERE project_id = ?)"),
	projectTable("volume_snapshots", "project_id = ?"),
	projectTable("volume_snapshot_applications", "chapter_id IN (SELECT id FROM chapters WHERE project_id = ?)"),
}

func col(name, typ string) projectBackupColumn { return projectBackupColumn{name: name, typ: typ} }

func cols(columns ...projectBackupColumn) []projectBackupColumn { return columns }

func projectTable(name, where string) projectBackupTable {
	return projectBackupTable{name: name, columns: projectBackupColumnNames(name), where: where}
}

func projectBackupColumnNames(table string) []string {
	columns := projectBackupSchemas[table]
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, column.name)
	}
	return names
}

func projectBackupScope() []string {
	scope := make([]string, 0, len(projectBackupTables))
	for _, table := range projectBackupTables {
		scope = append(scope, table.name)
	}
	return scope
}

func ensureProjectBackupSchema(ctx context.Context, pdb *sql.DB) error {
	for _, table := range projectBackupTables {
		columns := projectBackupSchemas[table.name]
		defs := make([]string, 0, len(columns))
		for _, column := range columns {
			defs = append(defs, column.name+" "+column.typ)
		}
		if _, err := pdb.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+table.name+` (`+strings.Join(defs, ", ")+")"); err != nil {
			return err
		}
		if err := ensureProjectBackupColumns(ctx, pdb, table.name, columns); err != nil {
			return err
		}
	}
	_, err := pdb.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS project_sync_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
	return err
}

func ensureProjectBackupColumns(ctx context.Context, pdb *sql.DB, table string, columns []projectBackupColumn) error {
	rows, err := pdb.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, column := range columns {
		if existing[column.name] {
			continue
		}
		stmt := "ALTER " + "TABLE " + table + " ADD COLUMN " + column.name + " " + alterColumnType(column.typ)
		if _, err := pdb.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func alterColumnType(typ string) string {
	upper := strings.ToUpper(typ)
	if strings.Contains(upper, "PRIMARY KEY") {
		return strings.TrimSpace(strings.ReplaceAll(typ, "PRIMARY KEY", ""))
	}
	if strings.Contains(upper, "NOT NULL") && !strings.Contains(upper, "DEFAULT") {
		return strings.TrimSpace(strings.ReplaceAll(typ, "NOT NULL", ""))
	}
	return typ
}

func copyProjectRows(ctx context.Context, src sqlQueryer, dst sqlExecer, table projectBackupTable, projectID string) (int, error) {
	return copyFilteredRows(ctx, src, dst, table.name, table.columns, table.where, projectID)
}

func copyAllRows(ctx context.Context, src sqlQueryer, dst sqlExecer, table string, columns []string) (int, error) {
	return copyFilteredRows(ctx, src, dst, table, columns, "", nil)
}

func copyFilteredRows(ctx context.Context, src sqlQueryer, dst sqlExecer, table string, columns []string, where string, arg any) (int, error) {
	query := `SELECT ` + strings.Join(columns, ",") + ` FROM ` + table
	args := []any{}
	if where != "" {
		query += ` WHERE ` + where
		for range strings.Count(where, "?") {
			args = append(args, arg)
		}
	}
	rows, err := src.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	insert := `INSERT OR REPLACE INTO ` + table + ` (` + strings.Join(columns, ",") + `) VALUES (` + placeholders(len(columns)) + `)`
	count := 0
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return count, err
		}
		if _, err := dst.ExecContext(ctx, insert, values...); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}

func firstProjectID(ctx context.Context, pdb *sql.DB) (string, error) {
	var idValue string
	if err := pdb.QueryRowContext(ctx, `SELECT id FROM projects LIMIT 1`).Scan(&idValue); err != nil {
		if err == sql.ErrNoRows {
			return "", httpx.NotFound("Project database")
		}
		return "", err
	}
	return idValue, nil
}

func countRows(ctx context.Context, db *sql.DB, table string) (int, error) {
	if exists, err := tableExists(ctx, db, table); err != nil || !exists {
		return 0, err
	}
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count)
	return count, err
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

const projectSelectColumns = `id, name, description, status, flow_mode, git_path, remote_url,
default_branch, startup_script, copy_files, chapter_settings, proxy_domain, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanProject(row rowScanner) (Project, error) {
	var p Project
	var description, gitPath, remoteURL, defaultBranch, startupScript, copyFiles, chapterSettings, proxyDomain sql.NullString
	if err := row.Scan(&p.ID, &p.Name, &description, &p.Status, &p.FlowMode, &gitPath, &remoteURL, &defaultBranch, &startupScript, &copyFiles, &chapterSettings, &proxyDomain, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return Project{}, err
	}
	p.Description = nullStringPtr(description)
	p.GitPath = nullStringPtr(gitPath)
	p.RemoteURL = nullStringPtr(remoteURL)
	p.DefaultBranch = nullStringPtr(defaultBranch)
	p.StartupScript = nullStringPtr(startupScript)
	p.CopyFiles = nullStringPtr(copyFiles)
	p.ProxyDomain = nullStringPtr(proxyDomain)
	p.ChapterSettings = jsondb.ObjectOrNil(chapterSettings)
	return p, nil
}

func expandPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(path, "~"), "/"))
	}
	return filepath.Abs(path)
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func stringOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullableSQL(v sql.NullString) any {
	if !v.Valid || v.String == "" {
		return nil
	}
	return v.String
}

func nullablePtr(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
