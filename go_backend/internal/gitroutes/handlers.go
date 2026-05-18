package gitroutes

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type NarratorBroadcaster interface {
	BroadcastNarrator(narratorID string, payload any)
}

type Handler struct {
	DB          *sql.DB
	Git         *gitx.Service
	Settings    *config.Store
	Broadcaster NarratorBroadcaster
}

func NewHandler(sqlDB *sql.DB, git *gitx.Service, settings ...*config.Store) *Handler {
	h := &Handler{DB: sqlDB, Git: git}
	if len(settings) > 0 {
		h.Settings = settings[0]
	}
	return h
}

var generateCommitMessage = func(ctx context.Context, settings *config.Store, model, systemPrompt, prompt, cwd string) (string, error) {
	result, err := agentbasic.Generate(ctx, settings, model, []agentbasic.Message{{Role: "user", Content: prompt}}, cwd, agentbasic.WithSystemPrompt(systemPrompt), agentbasic.WithMaxTurns(1), agentbasic.WithToolScope("readonly"))
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/{chapterId}/git/status", httpx.Handler(h.status))
	r.Post("/{chapterId}/git/stage", httpx.Handler(h.stage))
	r.Post("/{chapterId}/git/unstage", httpx.Handler(h.unstage))
	r.Post("/{chapterId}/git/commit", httpx.Handler(h.commit))
	r.Post("/{chapterId}/git/discard", httpx.Handler(h.discard))
	r.Get("/{chapterId}/git/diff", httpx.Handler(h.diff))
	r.Get("/{chapterId}/git/stash/list", httpx.Handler(h.stashList))
	r.Post("/{chapterId}/git/stash", httpx.Handler(h.stash))
	r.Get("/{chapterId}/git/log", httpx.Handler(h.log))
	r.Post("/{chapterId}/git/reset", httpx.Handler(h.reset))
	r.Post("/{chapterId}/git/ai-commit-message", httpx.Handler(h.aiCommitMessage))
}

type filesBody struct {
	Files []string `json:"files"`
	All   bool     `json:"all"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) error {
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	status, err := h.Git.StatusSummary(r.Context(), worktree)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, status)
	return nil
}

func (h *Handler) stage(w http.ResponseWriter, r *http.Request) error {
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	var body filesBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if err := validateFiles(body.Files); err != nil {
		return err
	}
	if body.All {
		err = h.Git.StageAll(r.Context(), worktree)
	} else {
		err = h.Git.StageFiles(r.Context(), worktree, body.Files)
	}
	if err != nil {
		return err
	}
	h.broadcastGitStatus(r.Context(), chi.URLParam(r, "chapterId"), worktree, "stage")
	return h.status(w, r)
}

func (h *Handler) unstage(w http.ResponseWriter, r *http.Request) error {
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	var body filesBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if err := validateFiles(body.Files); err != nil {
		return err
	}
	if body.All {
		err = h.Git.UnstageAll(r.Context(), worktree)
	} else {
		err = h.Git.UnstageFiles(r.Context(), worktree, body.Files)
	}
	if err != nil {
		return err
	}
	h.broadcastGitStatus(r.Context(), chi.URLParam(r, "chapterId"), worktree, "unstage")
	return h.status(w, r)
}

func (h *Handler) commit(w http.ResponseWriter, r *http.Request) error {
	chapterID := chi.URLParam(r, "chapterId")
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	sha, err := h.Git.Commit(r.Context(), worktree, body.Message)
	if err != nil {
		return err
	}
	if err := h.recordCommit(r.Context(), chapterID, worktree, sha, body.Message); err != nil {
		return err
	}
	status, _ := h.Git.StatusSummary(r.Context(), worktree)
	h.broadcastGitStatus(r.Context(), chapterID, worktree, "commit")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"commitSha": sha, "status": status, "commitSync": commitSyncMetadata("manual")})
	return nil
}

func (h *Handler) discard(w http.ResponseWriter, r *http.Request) error {
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	var body filesBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if err := validateFiles(body.Files); err != nil {
		return err
	}
	if body.All {
		err = h.Git.DiscardAll(r.Context(), worktree)
	} else {
		err = h.Git.DiscardFiles(r.Context(), worktree, body.Files)
	}
	if err != nil {
		return err
	}
	h.broadcastGitStatus(r.Context(), chi.URLParam(r, "chapterId"), worktree, "discard")
	return h.status(w, r)
}

func (h *Handler) diff(w http.ResponseWriter, r *http.Request) error {
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	file := r.URL.Query().Get("file")
	if file == "" {
		return httpx.BadRequest("file is required")
	}
	if err := validateFiles([]string{file}); err != nil {
		return err
	}
	result, err := h.Git.FileDiff(r.Context(), worktree, file, r.URL.Query().Get("staged") == "true")
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) stashList(w http.ResponseWriter, r *http.Request) error {
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	items, err := h.Git.StashList(r.Context(), worktree)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) stash(w http.ResponseWriter, r *http.Request) error {
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	var body struct {
		Action  string `json:"action"`
		Message string `json:"message"`
		Index   int    `json:"index"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	hasConflicts := false
	switch body.Action {
	case "push":
		err = h.Git.Stash(r.Context(), worktree, body.Message)
	case "pop":
		var result gitx.StashResult
		result, err = h.Git.StashPop(r.Context(), worktree)
		hasConflicts = result.HasConflicts
	case "apply":
		var result gitx.StashResult
		result, err = h.Git.StashApply(r.Context(), worktree, body.Index)
		hasConflicts = result.HasConflicts
	case "drop":
		err = h.Git.StashDrop(r.Context(), worktree, body.Index)
	default:
		return httpx.BadRequest("unsupported stash action")
	}
	if err != nil {
		return err
	}
	status, _ := h.Git.StatusSummary(r.Context(), worktree)
	h.broadcastGitStatus(r.Context(), chi.URLParam(r, "chapterId"), worktree, "stash")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"hasConflicts": hasConflicts, "status": status})
	return nil
}

func (h *Handler) log(w http.ResponseWriter, r *http.Request) error {
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.Git.Log(r.Context(), worktree, limit)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) reset(w http.ResponseWriter, r *http.Request) error {
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	var body struct {
		Target string `json:"target"`
		Mode   string `json:"mode"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.Target == "" {
		return httpx.BadRequest("target is required")
	}
	if err := h.Git.Reset(r.Context(), worktree, body.Target, body.Mode); err != nil {
		return err
	}
	h.broadcastGitStatus(r.Context(), chi.URLParam(r, "chapterId"), worktree, "reset")
	return h.status(w, r)
}

func (h *Handler) aiCommitMessage(w http.ResponseWriter, r *http.Request) error {
	worktree, err := h.worktree(r)
	if err != nil {
		return err
	}
	diff, err := h.Git.FullDiff(r.Context(), worktree)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"message": ""})
		return nil
	}

	message := "chore: update files"
	model := ""
	if h.Settings != nil {
		model = strings.TrimSpace(h.Settings.Core().Agent.DefaultModel)
	}
	if model != "" {
		systemPrompt := `You are a git commit message generator. Given a git diff, generate a concise commit message following the Conventional Commits format.
Rules:
- Use format: <type>(<optional scope>): <description>
- Types: feat, fix, refactor, style, docs, test, chore, perf, ci, build
- Description should be lowercase, imperative mood, no period at end
- If the diff covers multiple changes, summarize the primary change
- Keep the message under 72 characters
- Reply with ONLY the commit message, nothing else`
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		generated, genErr := generateCommitMessage(ctx, h.Settings, model, systemPrompt, "<diff>\n"+diff+"\n</diff>", worktree)
		if genErr == nil {
			if cleaned := sanitizeCommitMessage(generated); cleaned != "" && len(cleaned) <= 200 {
				message = cleaned
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"message": message})
	return nil
}

func (h *Handler) broadcastGitStatus(ctx context.Context, chapterID, worktree, source string) {
	if h.DB == nil || h.Git == nil || h.Broadcaster == nil || strings.TrimSpace(chapterID) == "" || strings.TrimSpace(worktree) == "" {
		return
	}
	status, err := h.Git.StatusSummary(ctx, worktree)
	if err != nil {
		return
	}
	baseBranch := ""
	fallbacks := []string{}
	if err := h.DB.QueryRowContext(ctx, `SELECT COALESCE(base_branch, '') FROM chapters WHERE id = ?`, chapterID).Scan(&baseBranch); err != nil {
		fallbacks = append(fallbacks, "chapter_base_branch_query_failed")
	}
	commitsAhead := 0
	if strings.TrimSpace(baseBranch) != "" {
		count, err := h.Git.CountCommitsBetween(ctx, worktree, baseBranch, "HEAD")
		if err == nil {
			commitsAhead = count
		} else {
			fallbacks = append(fallbacks, "commits_ahead_count_failed")
		}
	}
	narratorIDs := h.narratorIDsForChapter(ctx, chapterID)
	for _, narratorID := range narratorIDs {
		payload := map[string]any{
			"type":           "git_status",
			"narratorId":     narratorID,
			"chapterId":      chapterID,
			"toolUseId":      "",
			"source":         source,
			"status":         gitStatusWithoutFiles(status),
			"commitsAhead":   commitsAhead,
			"baseBranch":     baseBranch,
			"linesAdded":     status.LinesAdded,
			"linesRemoved":   status.LinesRemoved,
			"degraded":       len(fallbacks) > 0,
			"fallbacks":      fallbacks,
			"tsParitySource": "go-git-route",
		}
		h.Broadcaster.BroadcastNarrator(narratorID, payload)
	}
}

func (h *Handler) narratorIDsForChapter(ctx context.Context, chapterID string) []string {
	rows, err := h.DB.QueryContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ?`, chapterID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var narratorID string
		if err := rows.Scan(&narratorID); err == nil && strings.TrimSpace(narratorID) != "" {
			ids = append(ids, narratorID)
		}
	}
	return ids
}

func gitStatusWithoutFiles(status gitx.StatusSummary) map[string]any {
	return map[string]any{
		"hasChanges":   status.HasChanges,
		"staged":       status.Staged,
		"unstaged":     status.Unstaged,
		"untracked":    status.Untracked,
		"totalFiles":   status.TotalFiles,
		"headSha":      status.HeadSHA,
		"branch":       status.Branch,
		"linesAdded":   status.LinesAdded,
		"linesRemoved": status.LinesRemoved,
	}
}

func (h *Handler) recordCommit(ctx context.Context, chapterID, worktree, sha, fallbackMessage string) error {
	sha = strings.TrimSpace(sha)
	if h.DB == nil || sha == "" {
		return nil
	}
	exists := 0
	if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = ? AND sha = ?`, chapterID, sha).Scan(&exists); err != nil {
		return err
	}
	message := strings.TrimSpace(fallbackMessage)
	fullMessage := ""
	authorName := ""
	authorEmail := ""
	authoredAt := db.NowISO()
	if h.Git != nil {
		if commit, err := h.Git.ShowCommit(ctx, worktree, sha); err == nil {
			message = firstNonEmpty(commit.Message, message, sha)
			fullMessage = commit.FullMessage
			authorName = commit.AuthorName
			authorEmail = commit.AuthorEmail
			authoredAt = firstNonEmpty(commit.AuthoredAt, authoredAt)
		}
	}
	filesChanged, linesAdded, linesRemoved := 0, 0, 0
	if h.Git != nil {
		if files, added, removed, err := h.Git.CommitDiffStats(ctx, worktree, sha); err == nil {
			filesChanged = files
			linesAdded = added
			linesRemoved = removed
		}
	}
	now := db.NowISO()
	if exists == 0 {
		if _, err := h.DB.ExecContext(ctx, `INSERT INTO chapter_commits (id, chapter_id, sha, message, full_message, author_name, author_email, authored_at, source, files_changed, lines_added, lines_removed, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'manual', ?, ?, ?, ?)`, id.New(), chapterID, sha, firstNonEmpty(message, sha), nullableString(fullMessage), nullableString(authorName), nullableString(authorEmail), authoredAt, filesChanged, linesAdded, linesRemoved, now); err != nil {
			return err
		}
	}
	count := 0
	if h.Git != nil {
		if gitCount, err := h.Git.CommitCount(ctx, worktree); err == nil {
			count = gitCount
		}
	}
	if count <= 0 {
		if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = ?`, chapterID).Scan(&count); err != nil {
			return err
		}
	}
	_, err := h.DB.ExecContext(ctx, `UPDATE chapters SET head_commit_sha = ?, commit_count = ?, updated_at = ? WHERE id = ?`, nullableString(sha), count, now, chapterID)
	return err
}

func commitSyncMetadata(source string) map[string]any {
	return map[string]any{
		"recorded":          true,
		"source":            source,
		"mode":              "manual-route-record-and-background-watch",
		"tsParity":          true,
		"backgroundSync":    true,
		"worktreeWatcher":   true,
		"graphReadRefresh":  true,
		"fallback":          false,
		"syncEventContract": "git_status",
		"message":           "Go records commits created through this route, refreshes chapter metadata immediately, and the server-level worktree watcher syncs external commits/status in the background.",
	}
}

func sanitizeCommitMessage(message string) string {
	message = strings.TrimSpace(message)
	message = strings.Trim(message, "\"'`“”")
	message = strings.TrimSpace(strings.Split(message, "\n")[0])
	return strings.Trim(message, "\"'`“”")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func nullableString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: strings.TrimSpace(value) != ""}
}

func (h *Handler) worktree(r *http.Request) (string, error) {
	chapterID := chi.URLParam(r, "chapterId")
	var worktree sql.NullString
	if err := h.DB.QueryRowContext(r.Context(), `SELECT worktree_path FROM chapters WHERE id = ?`, chapterID).Scan(&worktree); err != nil {
		if err == sql.ErrNoRows {
			return "", httpx.NotFound("Chapter")
		}
		return "", err
	}
	if !worktree.Valid || worktree.String == "" {
		return "", httpx.BadRequest("Chapter has no active worktree")
	}
	return worktree.String, nil
}

func validateFiles(files []string) error {
	for _, file := range files {
		if strings.Contains(file, "..") || strings.HasPrefix(file, "/") || strings.HasPrefix(file, "\\") {
			return httpx.BadRequest("invalid file path: " + file)
		}
	}
	return nil
}
