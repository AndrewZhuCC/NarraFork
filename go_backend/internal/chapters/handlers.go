package chapters

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type NarratorBroadcaster interface {
	BroadcastNarrator(narratorID string, payload any)
}

type Handler struct {
	Service     *Service
	Broadcaster NarratorBroadcaster
}

func NewHandler(service *Service, broadcaster ...NarratorBroadcaster) *Handler {
	h := &Handler{Service: service}
	if len(broadcaster) > 0 {
		h.Broadcaster = broadcaster[0]
	}
	return h
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(h.list))
	r.Post("/", httpx.Handler(h.create))
	r.Get("/merge-sessions/{sessionId}", httpx.Handler(h.mergeSession))
	r.Get("/container-setup", httpx.Handler(h.containerSetup))
	r.Get("/podman/status", httpx.Handler(h.podmanStatus))
	r.Post("/podman/install", httpx.Handler(h.adminOnly(h.podmanInstall)))
	r.Post("/cleanup", httpx.Handler(h.cleanup))
	r.Post("/batch-merge", httpx.Handler(h.batchMerge))
	r.Get("/{id}", httpx.Handler(h.get))
	r.Patch("/{id}", httpx.Handler(h.update))
	r.Delete("/{id}", httpx.Handler(h.delete))
	r.Post("/{id}/fork", httpx.Handler(h.fork))
	r.Post("/{id}/batch-fork", httpx.Handler(h.batchFork))
	r.Post("/{id}/split", httpx.Handler(h.split))
	r.Post("/{id}/cherry-pick", httpx.Handler(h.cherryPick))
	r.Get("/{id}/dependency-status", httpx.Handler(h.dependencyStatus))
	r.Post("/{id}/sync-upstream", httpx.Handler(h.syncUpstream))
	r.Post("/{id}/review", httpx.Handler(h.createReview))
	r.Get("/{id}/merge-check", httpx.Handler(h.mergeCheck))
	r.Post("/{id}/merge", httpx.Handler(h.merge))
	r.Post("/{id}/ai-resolve", httpx.Handler(h.aiResolve))
	r.Post("/{id}/unmerge", httpx.Handler(h.unmerge))
	r.Post("/{id}/dormant", httpx.Handler(h.setStatus("dormant")))
	r.Post("/{id}/wake", httpx.Handler(h.setStatus("active")))
	r.Get("/{id}/compose-info", httpx.Handler(h.composeInfo))
	r.Get("/{id}/containers", httpx.Handler(h.containers))
	r.Post("/{id}/containers/start", httpx.Handler(h.containerAction("up", "-d")))
	r.Post("/{id}/containers/stop", httpx.Handler(h.containerAction("stop")))
	r.Post("/{id}/containers/pause", httpx.Handler(h.containerAction("pause")))
	r.Post("/{id}/containers/unpause", httpx.Handler(h.containerAction("unpause")))
	r.Get("/{id}/containers/logs", httpx.Handler(h.containerLogs))
	r.Post("/{id}/containers/remove", httpx.Handler(h.containerRemove))
	r.Get("/{id}/commits", httpx.Handler(h.commits))
	r.Get("/{id}/commits/{sha}/files/*", httpx.Handler(h.commitFileDiff))
	r.Get("/{id}/commits/{sha}", httpx.Handler(h.commit))
	r.Get("/{id}/git-status", httpx.Handler(h.gitStatus))
}

type createRequest struct {
	ProjectID   string  `json:"projectId"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	BaseBranch  string  `json:"baseBranch"`
}

type forkRequest struct {
	Title           *string  `json:"title"`
	Description     *string  `json:"description"`
	Role            string   `json:"role"`
	InheritMode     string   `json:"inheritMode"`
	ForkAtMessageID string   `json:"forkAtMessageUuid"`
	AnchorCommitSHA *string  `json:"anchorCommitSha"`
	AxisOffset      *float64 `json:"axisOffset"`
	CrossOffset     *float64 `json:"crossOffset"`
}

const (
	splitTitleMaxLength       = 200
	splitDescriptionMaxLength = 2000
)

type splitRequest struct {
	CommitSHA string `json:"commitSha"`
	NewFork   struct {
		Title       string  `json:"title"`
		Description *string `json:"description"`
		InheritMode string  `json:"inheritMode"`
	} `json:"newFork"`
}

type containerRemoveRequest struct {
	DeleteVolumes bool `json:"deleteVolumes"`
}

type reviewRequest struct {
	Title           *string  `json:"title"`
	Locale          string   `json:"locale"`
	AnchorCommitSHA *string  `json:"anchorCommitSha"`
	AxisOffset      *float64 `json:"axisOffset"`
	CrossOffset     *float64 `json:"crossOffset"`
	AutoStart       *bool    `json:"autoStart"`
}

func reviewCreateResponse(chapter Chapter, autoStartRequested bool) map[string]any {
	data, _ := json.Marshal(chapter)
	payload := map[string]any{}
	_ = json.Unmarshal(data, &payload)
	autoStartSession := autoStartRequested
	startMode := "auto-start-background-session"
	if !autoStartSession {
		startMode = "seed-message-only"
	}
	payload["reviewRuntime"] = map[string]any{
		"autoStartRequested": autoStartRequested,
		"autoStartSession":   autoStartSession,
		"startMode":          startMode,
	}
	return payload
}

type batchMergeRequest struct {
	BaseChapterID    string   `json:"baseChapterId"`
	SourceChapterIDs []string `json:"sourceChapterIds"`
	Title            string   `json:"title"`
	Strategy         string   `json:"strategy"`
	TargetChapterID  string   `json:"targetChapterId"`
	Locale           string   `json:"locale"`
}

type aiResolveRequest struct {
	TargetChapterID string `json:"targetChapterId"`
	Strategy        string `json:"strategy"`
	Message         string `json:"message"`
	Locale          string `json:"locale"`
}

func (h *Handler) adminOnly(next httpx.HandlerFunc) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		claims := auth.UserFromContext(r.Context())
		if claims == nil || claims.Role != "admin" {
			return httpx.Forbidden("Admin access required")
		}
		return next(w, r)
	}
}

func (h *Handler) batchMerge(w http.ResponseWriter, r *http.Request) error {
	var req batchMergeRequest
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	userID := ""
	if claims := auth.UserFromContext(r.Context()); claims != nil {
		userID = claims.Subject
	}
	result, err := h.Service.BatchMerge(r.Context(), BatchMergeInput{
		BaseChapterID: req.BaseChapterID, SourceChapterIDs: req.SourceChapterIDs,
		Title: req.Title, Strategy: req.Strategy, TargetChapterID: req.TargetChapterID,
		Locale: req.Locale, UserID: userID,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, result)
	return nil
}

func (h *Handler) mergeSession(w http.ResponseWriter, r *http.Request) error {
	sessionID := chi.URLParam(r, "sessionId")
	row := h.Service.DB.QueryRowContext(r.Context(), `SELECT id, target_chapter_id, source_chapter_ids, strategy, status, current_index, merged_count, current_source_chapter_id, conflict_files, error, locale, created_at, updated_at FROM merge_sessions WHERE id = ?`, sessionID)
	var idValue, targetChapterID, strategy, status, createdAt, updatedAt string
	var sourceChapterIDsJSON, conflictFilesJSON, currentSourceChapterID, errorMsg, locale sql.NullString
	var currentIndex, mergedCount int
	if err := row.Scan(&idValue, &targetChapterID, &sourceChapterIDsJSON, &strategy, &status, &currentIndex, &mergedCount, &currentSourceChapterID, &conflictFilesJSON, &errorMsg, &locale, &createdAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return httpx.NotFound("MergeSession")
		}
		return err
	}
	sourceChapterIDs := []string{}
	if sourceChapterIDsJSON.Valid && strings.TrimSpace(sourceChapterIDsJSON.String) != "" {
		_ = json.Unmarshal([]byte(sourceChapterIDsJSON.String), &sourceChapterIDs)
	}
	var conflictFiles any
	if conflictFilesJSON.Valid && strings.TrimSpace(conflictFilesJSON.String) != "" {
		var parsedConflictFiles []string
		_ = json.Unmarshal([]byte(conflictFilesJSON.String), &parsedConflictFiles)
		conflictFiles = parsedConflictFiles
	}
	steps := make([]map[string]any, 0, len(sourceChapterIDs))
	for i, sourceID := range sourceChapterIDs {
		stepStatus := "pending"
		if i < currentIndex {
			stepStatus = "completed"
		} else if i == currentIndex {
			stepStatus = status
		}
		steps = append(steps, map[string]any{"index": i, "sourceChapterId": sourceID, "status": stepStatus})
	}
	currentSource := any(nil)
	if currentSourceChapterID.Valid {
		currentSource = currentSourceChapterID.String
	}
	errValue := any(nil)
	if errorMsg.Valid {
		errValue = errorMsg.String
	}
	localeValue := any(nil)
	if locale.Valid {
		localeValue = locale.String
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id":                     idValue,
		"targetChapterId":        targetChapterID,
		"sourceChapterIds":       sourceChapterIDs,
		"strategy":               strategy,
		"status":                 status,
		"currentIndex":           currentIndex,
		"mergedCount":            mergedCount,
		"currentSourceChapterId": currentSource,
		"conflictFiles":          conflictFiles,
		"error":                  errValue,
		"locale":                 localeValue,
		"createdAt":              createdAt,
		"updatedAt":              updatedAt,
		"steps":                  steps,
	})
	return nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	items, err := h.Service.List(r.Context(), r.URL.Query().Get("projectId"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var req createRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// Mirror the TypeScript createChapterSchema Zod bounds: projectId min 1,
	// title min 1 / max 200, description max 2000.
	if strings.TrimSpace(req.ProjectID) == "" {
		return httpx.BadRequest("projectId is required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return httpx.BadRequest("title is required")
	}
	if len(req.Title) > 200 {
		return httpx.BadRequest("title must be at most 200 characters")
	}
	if req.Description != nil && len(*req.Description) > 2000 {
		return httpx.BadRequest("description must be at most 2000 characters")
	}
	chapter, err := h.Service.Create(r.Context(), CreateInput{ProjectID: req.ProjectID, Title: req.Title, Description: req.Description, BaseBranch: req.BaseBranch})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, chapter)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	chapter, err := h.Service.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, chapter)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	var fields map[string]any
	if err := httpx.DecodeLooseJSON(r, &fields); err != nil {
		return err
	}
	chapter, err := h.Service.Update(r.Context(), chi.URLParam(r, "id"), fields)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, chapter)
	return nil
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	if err := h.Service.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) cleanup(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ChapterIDs   []string `json:"chapterIds"`
		Force        bool     `json:"force"`
		DeleteBranch bool     `json:"deleteBranch"`
	}
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	report, err := h.Service.BatchCleanup(r.Context(), req.ChapterIDs, req.Force, req.DeleteBranch)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, report)
	return nil
}

func (h *Handler) fork(w http.ResponseWriter, r *http.Request) error {
	var req forkRequest
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	// Mirror the TypeScript forkChapterSchema Zod bounds: title min 1 / max 200,
	// description max 2000, role enum ["branch","exploration"], crossOffset min 0,
	// inheritMode enum ["full","compressed","fresh"].
	if req.Title != nil {
		if strings.TrimSpace(*req.Title) == "" {
			return httpx.BadRequest("title must be a non-empty string")
		}
		if len(*req.Title) > 200 {
			return httpx.BadRequest("title must be at most 200 characters")
		}
	}
	if req.Description != nil && len(*req.Description) > 2000 {
		return httpx.BadRequest("description must be at most 2000 characters")
	}
	if strings.TrimSpace(req.Role) != "" && req.Role != "branch" && req.Role != "exploration" {
		return httpx.BadRequest("role must be branch or exploration")
	}
	if req.InheritMode != "" && normalizeInheritMode(req.InheritMode) == "" {
		return httpx.BadRequest("inheritMode must be full, compressed, or fresh")
	}
	if req.CrossOffset != nil && *req.CrossOffset < 0 {
		return httpx.BadRequest("crossOffset must be greater than or equal to 0")
	}
	chapter, err := h.Service.Fork(r.Context(), chi.URLParam(r, "id"), ForkInput{
		Title:             req.Title,
		Description:       req.Description,
		Role:              req.Role,
		InheritMode:       req.InheritMode,
		ForkAtMessageUUID: req.ForkAtMessageID,
		AnchorCommitSHA:   req.AnchorCommitSHA,
		AxisOffset:        req.AxisOffset,
		CrossOffset:       req.CrossOffset,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, chapter)
	return nil
}

func (h *Handler) batchFork(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Forks []struct {
			Title       string  `json:"title"`
			Description *string `json:"description"`
			InheritMode string  `json:"inheritMode"`
			Role        string  `json:"role"`
		} `json:"forks"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	// Mirror the TypeScript batchForkSchema Zod bound: forks array min 1 / max 10.
	if len(body.Forks) < 1 {
		return httpx.BadRequest("forks is required")
	}
	if len(body.Forks) > 10 {
		return httpx.BadRequest("forks must contain at most 10 items")
	}
	created := []Chapter{}
	failed := []map[string]any{}
	for _, input := range body.Forks {
		title := strings.TrimSpace(input.Title)
		if title == "" {
			failed = append(failed, map[string]any{"input": input, "error": "title is required"})
			continue
		}
		chapter, err := h.Service.Fork(r.Context(), chi.URLParam(r, "id"), ForkInput{
			Title:       &title,
			Description: input.Description,
			InheritMode: input.InheritMode,
			Role:        input.Role,
		})
		if err != nil {
			failed = append(failed, map[string]any{"input": input, "error": err.Error()})
			continue
		}
		created = append(created, chapter)
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"created": created, "failed": failed})
	return nil
}

func (h *Handler) split(w http.ResponseWriter, r *http.Request) error {
	var req splitRequest
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	commitSHA := strings.TrimSpace(req.CommitSHA)
	if commitSHA == "" {
		return httpx.BadRequest("commitSha is required")
	}
	title := strings.TrimSpace(req.NewFork.Title)
	if title == "" {
		return httpx.BadRequest("newFork.title is required")
	}
	if len(title) > splitTitleMaxLength {
		return httpx.BadRequest("newFork.title must be at most 200 characters")
	}
	if req.NewFork.Description != nil && len(*req.NewFork.Description) > splitDescriptionMaxLength {
		return httpx.BadRequest("newFork.description must be at most 2000 characters")
	}
	inheritMode := strings.TrimSpace(req.NewFork.InheritMode)
	if inheritMode != "full" && inheritMode != "compressed" && inheritMode != "fresh" {
		return httpx.BadRequest("newFork.inheritMode must be full, compressed, or fresh")
	}
	result, err := h.Service.SplitAtCommit(r.Context(), chi.URLParam(r, "id"), SplitInput{
		CommitSHA: commitSHA,
		NewFork: SplitNewForkInput{
			Title:       title,
			Description: req.NewFork.Description,
			InheritMode: inheritMode,
		},
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, result)
	return nil
}

func (h *Handler) cherryPick(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		SourceChapterID string   `json:"sourceChapterId"`
		CommitSHAs      []string `json:"commitShas"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	// Mirror the TypeScript cherryPickSchema Zod bounds: sourceChapterId min 1,
	// commitShas array min 1 with each entry min 1.
	if strings.TrimSpace(body.SourceChapterID) == "" {
		return httpx.BadRequest("sourceChapterId is required")
	}
	if len(body.CommitSHAs) == 0 {
		return httpx.BadRequest("commitShas is required")
	}
	for _, sha := range body.CommitSHAs {
		if strings.TrimSpace(sha) == "" {
			return httpx.BadRequest("commitShas entries must be non-empty")
		}
	}
	target, err := h.Service.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	if body.SourceChapterID != "" {
		if _, err := h.Service.Get(r.Context(), body.SourceChapterID); err != nil {
			return err
		}
	}
	if target.WorktreePath == nil || strings.TrimSpace(*target.WorktreePath) == "" {
		return httpx.BadRequest("Chapter has no worktree path")
	}
	result, err := h.Service.Git.CherryPickCommits(r.Context(), *target.WorktreePath, body.CommitSHAs)
	if err != nil {
		return err
	}
	chapter, updateErr := h.refreshChapterGitMetadata(r.Context(), target.ID, *target.WorktreePath)
	if updateErr != nil {
		return updateErr
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": result.Success, "chapter": chapter, "commitSha": result.CommitSHA, "conflictFiles": result.ConflictFiles, "output": result.Output})
	return nil
}

func (h *Handler) dependencyStatus(w http.ResponseWriter, r *http.Request) error {
	target, err := h.Service.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT id, source_id, metadata FROM chapter_edges WHERE target_id = ? AND type = 'dependency' ORDER BY created_at ASC`, target.ID)
	if err != nil {
		return err
	}
	type dependencyEdgeRow struct {
		edgeID   string
		sourceID string
		metadata sql.NullString
	}
	edges := []dependencyEdgeRow{}
	for rows.Next() {
		var edge dependencyEdgeRow
		if err := rows.Scan(&edge.edgeID, &edge.sourceID, &edge.metadata); err != nil {
			_ = rows.Close()
			return err
		}
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	items := []map[string]any{}
	for _, edge := range edges {
		_, _ = h.Service.syncChapterCommits(r.Context(), edge.sourceID, 50)
		source, sourceErr := h.Service.Get(r.Context(), edge.sourceID)
		if sourceErr != nil {
			return sourceErr
		}
		edgeMetadata := dependencyEdgeMetadataMap(edge.metadata)
		lastSyncedCommit := dependencyEdgeLastSyncedCommit(edgeMetadata)
		sourceHeadCommitSha := stringPtrValue(source.HeadCommitSHA)
		hasUpdates := sourceHeadCommitSha != "" && sourceHeadCommitSha != lastSyncedCommit
		newCommitCount := 0
		if hasUpdates {
			newCommitCount = dependencyNewCommitCount(r.Context(), h.Service.DB, source, lastSyncedCommit)
		}
		items = append(items, map[string]any{
			"edgeId":              edge.edgeID,
			"sourceChapterId":     edge.sourceID,
			"hasUpdates":          hasUpdates,
			"newCommitCount":      newCommitCount,
			"lastSyncedCommit":    nullableStringValue(lastSyncedCommit),
			"sourceHeadCommitSha": nullableStringValue(sourceHeadCommitSha),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) syncUpstream(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		EdgeID   string `json:"edgeId"`
		Strategy string `json:"strategy"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.EdgeID) == "" {
		return httpx.BadRequest("edgeId is required")
	}
	strategy := body.Strategy
	if strategy != "merge" && strategy != "rebase" {
		strategy = "merge"
	}
	var sourceID, targetID, edgeType string
	var rawMetadata sql.NullString
	err := h.Service.DB.QueryRowContext(r.Context(), `SELECT source_id, target_id, type, metadata FROM chapter_edges WHERE id = ?`, body.EdgeID).Scan(&sourceID, &targetID, &edgeType, &rawMetadata)
	if err != nil {
		if err == sql.ErrNoRows {
			return httpx.NotFound("ChapterEdge")
		}
		return err
	}
	if targetID != chi.URLParam(r, "id") || edgeType != "dependency" {
		return httpx.BadRequest("edge does not target this chapter")
	}
	source, err := h.Service.Get(r.Context(), sourceID)
	if err != nil {
		return err
	}
	target, err := h.Service.Get(r.Context(), targetID)
	if err != nil {
		return err
	}
	if target.WorktreePath == nil || strings.TrimSpace(*target.WorktreePath) == "" {
		return httpx.BadRequest("Chapter has no worktree path")
	}
	var result any
	if strategy == "rebase" {
		result, err = h.Service.Git.Rebase(r.Context(), *target.WorktreePath, source.Branch)
	} else {
		result, err = h.Service.Git.MergeWithStrategy(r.Context(), *target.WorktreePath, source.Branch, "merge", "Sync upstream "+source.Branch, false)
	}
	if err != nil {
		return err
	}
	success := dependencySyncResultSuccess(result)
	if !success {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": false, "strategy": strategy, "result": result})
		return nil
	}
	_, _ = h.Service.syncChapterCommits(r.Context(), source.ID, 50)
	_, _ = h.Service.syncChapterCommits(r.Context(), target.ID, 50)
	chapter, updateErr := h.refreshChapterGitMetadata(r.Context(), target.ID, *target.WorktreePath)
	if updateErr != nil {
		return updateErr
	}
	source, _ = h.Service.Get(r.Context(), sourceID)
	lastSyncedCommit := stringPtrValue(source.HeadCommitSHA)
	if err := h.updateDependencyEdgeMetadata(r.Context(), body.EdgeID, rawMetadata, lastSyncedCommit, strategy); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"strategy": strategy,
		"result":   result,
		"chapter":  chapter,
		"dependencySync": map[string]any{
			"edgeId":              body.EdgeID,
			"sourceChapterId":     sourceID,
			"targetChapterId":     targetID,
			"sourceHeadCommitSha": nullableStringValue(lastSyncedCommit),
			"lastSyncedCommit":    nullableStringValue(lastSyncedCommit),
			"strategy":            strategy,
		},
	})
	return nil
}

func dependencySyncResultSuccess(result any) bool {
	switch typed := result.(type) {
	case gitx.MergeResult:
		return typed.Success
	case gitx.RebaseResult:
		return typed.Success
	default:
		return true
	}
}

func dependencyEdgeMetadataMap(raw sql.NullString) map[string]any {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return map[string]any{}
	}
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(raw.String), &decoded); err != nil {
		return map[string]any{}
	}
	return decoded
}

func dependencyEdgeLastSyncedCommit(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	return firstStringValue(metadata, "lastSyncedCommit", "last_synced_commit")
}

func dependencyNewCommitCount(ctx context.Context, database *sql.DB, source Chapter, lastSyncedCommit string) int {
	if database == nil {
		return 0
	}
	if strings.TrimSpace(lastSyncedCommit) == "" {
		return source.CommitCount
	}
	var authoredAt, createdAt string
	err := database.QueryRowContext(ctx, `SELECT authored_at, created_at FROM chapter_commits WHERE chapter_id = ? AND sha = ? LIMIT 1`, source.ID, lastSyncedCommit).Scan(&authoredAt, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || isSQLiteMissingTableError(err) {
			return source.CommitCount
		}
		return 0
	}
	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = ? AND (authored_at > ? OR (authored_at = ? AND created_at > ?))`, source.ID, authoredAt, authoredAt, createdAt).Scan(&count); err != nil {
		return 0
	}
	if count == 0 {
		return 1
	}
	return count
}

func (h *Handler) updateDependencyEdgeMetadata(ctx context.Context, edgeID string, raw sql.NullString, lastSyncedCommit, strategy string) error {
	metadata := dependencyEdgeMetadataMap(raw)
	metadata["lastSyncedCommit"] = strings.TrimSpace(lastSyncedCommit)
	metadata["lastSyncedAt"] = db.NowISO()
	metadata["lastSyncStrategy"] = strategy
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = h.Service.DB.ExecContext(ctx, `UPDATE chapter_edges SET metadata = ? WHERE id = ?`, string(encoded), edgeID)
	return err
}

func (h *Handler) createReview(w http.ResponseWriter, r *http.Request) error {
	var req reviewRequest
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	// Mirror the TypeScript createReviewSchema Zod bounds: title min 1 / max 200,
	// locale enum ["en","zh-CN"], crossOffset min 0.
	if req.Title != nil {
		if strings.TrimSpace(*req.Title) == "" {
			return httpx.BadRequest("title must be a non-empty string")
		}
		if len(*req.Title) > 200 {
			return httpx.BadRequest("title must be at most 200 characters")
		}
	}
	if strings.TrimSpace(req.Locale) != "" && req.Locale != "en" && req.Locale != "zh-CN" {
		return httpx.BadRequest("locale must be en or zh-CN")
	}
	if req.CrossOffset != nil && *req.CrossOffset < 0 {
		return httpx.BadRequest("crossOffset must be greater than or equal to 0")
	}
	user := auth.UserFromContext(r.Context())
	userID := ""
	if user != nil {
		userID = user.Subject
	}
	chapter, err := h.Service.CreateReview(r.Context(), chi.URLParam(r, "id"), ReviewInput{
		Title:           req.Title,
		Locale:          req.Locale,
		AnchorCommitSHA: req.AnchorCommitSHA,
		AxisOffset:      req.AxisOffset,
		CrossOffset:     req.CrossOffset,
		AutoStart:       req.AutoStart,
		UserID:          userID,
	})
	if err != nil {
		return err
	}
	autoStartRequested := reviewAutoStartEnabled(req.AutoStart)
	httpx.WriteJSON(w, http.StatusCreated, reviewCreateResponse(chapter, autoStartRequested))
	return nil
}

func (h *Handler) mergeCheck(w http.ResponseWriter, r *http.Request) error {
	targetID := r.URL.Query().Get("targetChapterId")
	if targetID == "" {
		return httpx.BadRequest("targetChapterId is required")
	}
	result, err := h.Service.MergeCheck(r.Context(), chi.URLParam(r, "id"), targetID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) merge(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		TargetChapterID string `json:"targetChapterId"`
		Strategy        string `json:"strategy"`
		Message         string `json:"message"`
	}
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	// Mirror the TypeScript mergeChapterSchema Zod bound: message max 500 chars.
	if len(req.Message) > 500 {
		return httpx.BadRequest("message must be at most 500 characters")
	}
	userID := ""
	if user := auth.UserFromContext(r.Context()); user != nil {
		userID = user.Subject
	}
	result, err := h.Service.Merge(r.Context(), chi.URLParam(r, "id"), req.TargetChapterID, req.Strategy, req.Message, userID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) aiResolve(w http.ResponseWriter, r *http.Request) error {
	var req aiResolveRequest
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	userID := ""
	if user := auth.UserFromContext(r.Context()); user != nil {
		userID = user.Subject
	}
	result, err := h.Service.AIResolve(r.Context(), chi.URLParam(r, "id"), req.TargetChapterID, req.Strategy, req.Message, userID, req.Locale)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) unmerge(w http.ResponseWriter, r *http.Request) error {
	result, err := h.Service.Unmerge(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) setStatus(status string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		chapterID := chi.URLParam(r, "id")
		var err error
		if status == "dormant" {
			err = h.Service.Dormant(r.Context(), chapterID)
		} else {
			err = h.Service.Wake(r.Context(), chapterID)
		}
		if err != nil {
			return err
		}
		chapter, err := h.Service.Get(r.Context(), chapterID)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, chapter)
		return nil
	}
}

func (h *Handler) gitStatus(w http.ResponseWriter, r *http.Request) error {
	status, err := h.Service.ChapterGitStatus(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, status)
	return nil
}

func (h *Handler) commits(w http.ResponseWriter, r *http.Request) error {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	commits, err := h.Service.Commits(r.Context(), chi.URLParam(r, "id"), limit)
	if err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(commits))
	for _, commit := range commits {
		items = append(items, commitPayload(commit))
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) commit(w http.ResponseWriter, r *http.Request) error {
	chapter, err := h.Service.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	commit, err := h.Service.Commit(r.Context(), chapter.ID, chi.URLParam(r, "sha"))
	if err != nil {
		return err
	}
	payload := commitPayload(commit)
	repoPath := ""
	if chapter.WorktreePath != nil && strings.TrimSpace(*chapter.WorktreePath) != "" {
		repoPath = *chapter.WorktreePath
	} else if project, projectErr := h.Service.getProject(r.Context(), chapter.ProjectID); projectErr == nil && project.GitPath.Valid {
		repoPath = project.GitPath.String
	}
	files := []gitx.CommitFile{}
	diffInlined := false
	if strings.TrimSpace(repoPath) != "" {
		if fileList, filesErr := h.Service.Git.CommitFiles(r.Context(), repoPath, commit.SHA); filesErr == nil {
			files = fileList
			totalLines := 0
			for _, file := range files {
				totalLines += file.LinesAdded + file.LinesRemoved
			}
			if len(files) <= 30 && totalLines <= 5000 {
				diffInlined = true
				for i := range files {
					if diff, diffErr := h.Service.Git.CommitFileDiff(r.Context(), repoPath, commit.SHA, files[i].Path); diffErr == nil {
						if text, ok := diff["diff"].(string); ok {
							files[i].Diff = text
						}
					}
				}
			}
		}
	}
	payload["files"] = files
	payload["diffInlined"] = diffInlined
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func nullableIntValue(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func commitPayload(commit gitx.Commit) map[string]any {
	var authorName any
	if commit.AuthorName != "" {
		authorName = commit.AuthorName
	}
	var authorEmail any
	if commit.AuthorEmail != "" {
		authorEmail = commit.AuthorEmail
	}
	return map[string]any{
		"id":                commit.SHA,
		"sha":               commit.SHA,
		"message":           commit.Message,
		"fullMessage":       nullableStringValue(commit.FullMessage),
		"authorName":        authorName,
		"authorEmail":       authorEmail,
		"authoredAt":        commit.AuthoredAt,
		"source":            firstNonEmptyString(commit.Source, "manual"),
		"narratorId":        nullableStringValue(commit.NarratorID),
		"narratorMessageId": nullableStringValue(commit.NarratorMessageID),
		"filesChanged":      nullableIntValue(commit.FilesChanged),
		"linesAdded":        nullableIntValue(commit.LinesAdded),
		"linesRemoved":      nullableIntValue(commit.LinesRemoved),
	}
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func nullableStringValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (h *Handler) commitFileDiff(w http.ResponseWriter, r *http.Request) error {
	chapter, err := h.Service.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	filePath := chi.URLParam(r, "*")
	if filePath == "" {
		parts := strings.SplitN(r.URL.Path, "/files/", 2)
		if len(parts) == 2 {
			filePath = parts[1]
		}
	}
	decoded, decodeErr := url.PathUnescape(filePath)
	if decodeErr == nil {
		filePath = decoded
	}
	filePath = strings.TrimPrefix(filepath.Clean(filePath), string(filepath.Separator))
	if filePath == "." || filePath == "" || strings.HasPrefix(filePath, "..") {
		return httpx.BadRequest("File path is required")
	}
	repoPath := ""
	if chapter.WorktreePath != nil && strings.TrimSpace(*chapter.WorktreePath) != "" {
		repoPath = *chapter.WorktreePath
	} else if project, projectErr := h.Service.getProject(r.Context(), chapter.ProjectID); projectErr == nil && project.GitPath.Valid {
		repoPath = project.GitPath.String
	}
	if strings.TrimSpace(repoPath) == "" {
		return httpx.BadRequest("No git repository available")
	}
	result, err := h.Service.Git.CommitFileDiff(r.Context(), repoPath, chi.URLParam(r, "sha"), filePath)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, result)
	return nil
}

func (h *Handler) refreshChapterGitMetadata(ctx context.Context, chapterID, worktreePath string) (Chapter, error) {
	head, err := h.Service.Git.HeadCommit(ctx, worktreePath)
	if err != nil {
		return Chapter{}, err
	}
	count, err := h.Service.Git.CommitCount(ctx, worktreePath)
	if err != nil {
		return Chapter{}, err
	}
	if _, err := h.Service.DB.ExecContext(ctx, `UPDATE chapters SET head_commit_sha = ?, commit_count = ?, updated_at = ? WHERE id = ?`, nullableString(head), count, db.NowISO(), chapterID); err != nil {
		return Chapter{}, err
	}
	return h.Service.Get(ctx, chapterID)
}

func (h *Handler) containerSetup(w http.ResponseWriter, r *http.Request) error {
	podman := commandVersion(r.Context(), "podman", "--version")
	compose := composeVersion(r.Context())
	backend := ""
	if podman != "" {
		backend = strings.ToLower(strings.Trim(strings.TrimSpace(mustCommandOutput(r.Context(), "podman", "info", "--format", "{{.Host.RootlessNetworkCmd}}")), "'\""))
	}
	rootlessOK := backend == "pasta" || backend == "passt"
	passtVersion := commandVersion(r.Context(), "passt", "--version")
	payload := map[string]any{
		"podman":          map[string]any{"ok": podman != "", "version": podman},
		"podmanCompose":   map[string]any{"ok": compose.version != "", "version": compose.version},
		"composeProvider": map[string]any{"ok": len(compose.command) > 0, "provider": strings.Join(compose.command, " ")},
		"passt":           map[string]any{"ok": passtVersion != "", "version": passtVersion},
		"rootlessNetwork": map[string]any{"ok": rootlessOK, "backend": backend},
		"allReady":        podman != "" && len(compose.command) > 0 && rootlessOK,
	}
	if podman == "" {
		reason := "podman is not available; container runtime features will return explicit fallback responses"
		payload["fallback"] = true
		payload["supported"] = false
		payload["reason"] = reason
		payload["error"] = reason
		payload["message"] = reason
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) podmanStatus(w http.ResponseWriter, r *http.Request) error {
	platformSupported := runtime.GOOS == "linux"
	version := ""
	if platformSupported {
		version = commandVersion(r.Context(), "podman", "--version")
	}
	installed := version != ""
	payload := map[string]any{"installed": installed, "version": version, "platform": podmanStatusPlatform(), "supported": platformSupported, "fallback": !installed}
	if !platformSupported {
		reason := "Container management is only supported on Linux in go_backend."
		payload["reason"] = reason
		payload["error"] = reason
		payload["message"] = reason
	} else if !installed {
		reason := "Podman is not installed or not available on PATH."
		payload["reason"] = reason
		payload["error"] = reason
		payload["message"] = reason
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func podmanStatusPlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	default:
		return "linux"
	}
}

func (h *Handler) podmanInstall(w http.ResponseWriter, r *http.Request) error {
	if runtime.GOOS != "linux" {
		return httpx.BadRequest("Container management is only supported on Linux")
	}
	cmd := ""
	if _, err := exec.LookPath("apt-get"); err == nil {
		cmd = "sudo apt-get update && sudo apt-get install -y podman"
	} else if _, err := exec.LookPath("dnf"); err == nil {
		cmd = "sudo dnf install -y podman"
	} else if _, err := exec.LookPath("pacman"); err == nil {
		cmd = "sudo pacman -S --noconfirm podman"
	} else {
		return httpx.BadRequest("No supported package manager found")
	}
	cctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	out, err := runLimitedEnvTimeout(cctx, "", nil, 10*time.Minute, "sh", "-c", cmd)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > 500 {
			msg = msg[:500]
		}
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "installed": false, "error": msg})
		return nil
	}
	version := commandVersion(r.Context(), "podman", "--version")
	if version == "" {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"ok":        false,
			"installed": false,
			"code":      "PODMAN_INSTALL_NOT_VERIFIED",
			"error":     "Install command completed, but podman is still unavailable on PATH.",
		})
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "installed": true, "version": version})
	return nil
}

func (h *Handler) composeInfo(w http.ResponseWriter, r *http.Request) error {
	chapter, err := h.Service.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	cwd := ""
	if chapter.WorktreePath != nil {
		cwd = strings.TrimSpace(*chapter.WorktreePath)
	}
	if cwd == "" {
		return httpx.BadRequest("chapter has no worktree path")
	}
	composeFile := resolveComposeFilePath(cwd, containerConfigFrom(chapter.ContainerConfig))
	services := []map[string]any{}
	if composeFile != "" {
		services = parseComposeServicesFromFile(composeFile)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"services": services, "composeFile": nullableString(composeFile)})
	return nil
}

func writeContainerListFallback(w http.ResponseWriter, items []map[string]any, code, message string) {
	w.Header().Set("X-Narrafork-Fallback", "true")
	w.Header().Set("X-Narrafork-Fallback-Code", code)
	w.Header().Set("X-Narrafork-Fallback-Reason", message)
	httpx.WriteJSON(w, http.StatusOK, items)
}

func (h *Handler) containers(w http.ResponseWriter, r *http.Request) error {
	chapterID := chi.URLParam(r, "id")
	chapter, err := h.Service.Get(r.Context(), chapterID)
	if err != nil {
		return err
	}
	cwd := ""
	if chapter.WorktreePath != nil {
		cwd = strings.TrimSpace(*chapter.WorktreePath)
	}
	if cwd == "" {
		return httpx.BadRequest("chapter has no worktree path")
	}
	stored, err := h.Service.persistedContainerInstances(r.Context(), chapterID)
	if err != nil {
		return err
	}
	if commandVersion(r.Context(), "podman", "--version") == "" {
		writeContainerListFallback(w, stored, "PODMAN_NOT_FOUND", "podman is not available; returning persisted container state")
		return nil
	}
	cfg := containerConfigFrom(chapter.ContainerConfig)
	composeFile := resolveComposeFilePath(cwd, cfg)
	if composeFile == "" {
		writeContainerListFallback(w, stored, "COMPOSE_FILE_NOT_FOUND", "compose file not found; returning persisted container state")
		return nil
	}
	cmd := composeCommandForFile(r.Context(), composeFile)
	if len(cmd) == 0 {
		writeContainerListFallback(w, stored, "PODMAN_COMPOSE_NOT_FOUND", "podman compose is not available; returning persisted container state")
		return nil
	}
	proxyInfo, proxyUnsupported, proxyMessage := h.Service.proxyInfoForChapter(r.Context(), chapter)
	if proxyUnsupported {
		writeContainerListFallback(w, stored, "PROXY_BACKEND_UNAVAILABLE", proxyMessage)
		return nil
	}
	items, liveErr := h.Service.liveComposeContainerInstances(r.Context(), chapterID, cwd, cmd, nil, cfg, proxyInfo)
	if liveErr != nil {
		writeContainerListFallback(w, stored, "CONTAINER_STATE_UNAVAILABLE", "live container state is unavailable; returning persisted container state")
		return nil
	}
	if err := h.Service.persistContainerInstances(r.Context(), chapterID, items); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) liveContainerInstances(ctx context.Context, chapterID, cwd string) ([]map[string]any, error) {
	out, err := runLimited(ctx, cwd, "podman", "ps", "-a", "--format", "json")
	if err != nil {
		return nil, err
	}
	raw := parseContainerJSONList(out)
	items := []map[string]any{}
	needle := filepath.Base(cwd)
	for _, item := range raw {
		name := containerName(item)
		if needle != "" && name != "" && !strings.Contains(name, needle) {
			continue
		}
		items = append(items, normalizeContainerInstance(chapterID, item))
	}
	return items, nil
}

func (h *Handler) liveComposeContainerInstances(ctx context.Context, chapterID, cwd string, composeCmd []string, env map[string]string, cfg chapterContainerConfig, proxyInfo *proxyRuntimeInfo) ([]map[string]any, error) {
	args := append(append([]string{}, composeCmd[1:]...), "ps", "--format", "json")
	out, err := runLimitedEnv(ctx, cwd, env, composeCmd[0], args...)
	if err != nil {
		return nil, err
	}
	raw := parseContainerJSONList(out)
	items := []map[string]any{}
	for _, item := range raw {
		base := normalizeContainerInstance(chapterID, item)
		serviceName := firstNonEmpty(stringValue(base["serviceName"]), "container")
		containerID := stringValue(base["containerId"])
		if proxyInfo == nil {
			items = append(items, base)
			continue
		}
		containerIP := h.inspectContainerIP(ctx, containerID)
		ports := mergePortHints(containerPortsFromRaw(item), configuredPortsForService(cfg, serviceName), h.inspectContainerExposedPorts(ctx, containerID))
		if len(ports) == 0 {
			continue
		}
		for _, containerPort := range ports {
			proxyLabel := generateProxyLabel(proxyInfo.ChapterShortID, serviceName, containerPort)
			entry := map[string]any{}
			for key, value := range base {
				entry[key] = value
			}
			entry["id"] = firstNonEmpty(containerID, serviceName) + "-" + strconv.Itoa(containerPort)
			entry["hostPort"] = nil
			entry["containerPort"] = containerPort
			entry["proxyLabel"] = proxyLabel
			entry["proxyUrl"] = buildProxyURL(proxyLabel, proxyInfo.Domain, proxyInfo.Port)
			entry["containerIp"] = nullableString(containerIP)
			items = append(items, entry)
		}
	}
	return items, nil
}

func (h *Handler) persistedContainerInstances(ctx context.Context, chapterID string) ([]map[string]any, error) {
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT id, chapter_id, service_name, container_id, status, host_port, container_port, proxy_label, container_ip, volume_name, created_at, updated_at FROM container_instances WHERE chapter_id = ? ORDER BY service_name ASC, id ASC`, chapterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idValue, chapterIDValue, serviceName, status, createdAt, updatedAt string
		var containerID, proxyLabel, containerIP, volumeName sql.NullString
		var hostPort, containerPort sql.NullInt64
		if err := rows.Scan(&idValue, &chapterIDValue, &serviceName, &containerID, &status, &hostPort, &containerPort, &proxyLabel, &containerIP, &volumeName, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": idValue, "chapterId": chapterIDValue, "serviceName": serviceName, "containerId": nullSQLString(containerID), "containerName": serviceName, "status": status, "hostPort": nullSQLInt(hostPort), "containerPort": nullSQLInt(containerPort), "proxyLabel": nullSQLString(proxyLabel), "containerIp": nullSQLString(containerIP), "volumeName": nullSQLString(volumeName), "createdAt": createdAt, "updatedAt": updatedAt})
	}
	return items, rows.Err()
}

func (h *Handler) persistContainerInstances(ctx context.Context, chapterID string, items []map[string]any) error {
	now := db.NowISO()
	portAllocationUpsert := db.InsertOnConflictUpdateSQL(
		"port_allocations",
		[]string{"port", "chapter_id", "service_name", "allocated_at"},
		[]string{"port"},
		[]string{"chapter_id", "service_name", "allocated_at"},
	)
	if _, err := h.Service.DB.ExecContext(ctx, `DELETE FROM port_allocations WHERE chapter_id = ?`, chapterID); err != nil {
		return err
	}
	seen := []string{}
	for _, item := range items {
		idValue := stringValue(item["id"])
		if idValue == "" {
			continue
		}
		seen = append(seen, idValue)
		serviceName := firstNonEmpty(stringValue(item["serviceName"]), "container")
		containerID := nullableString(stringValue(item["containerId"]))
		status := normalizeContainerStatus(firstNonEmpty(stringValue(item["status"]), "stopped"))
		hostPort := nullableIntForDB(intValue(item["hostPort"]))
		containerPort := nullableIntForDB(intValue(item["containerPort"]))
		proxyLabel := nullableString(stringValue(item["proxyLabel"]))
		containerIP := nullableString(stringValue(item["containerIp"]))
		_, err := h.Service.DB.ExecContext(ctx, `INSERT INTO container_instances (id, chapter_id, service_name, container_id, status, host_port, container_port, proxy_label, container_ip, volume_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?) ON CONFLICT(id) DO UPDATE SET chapter_id = excluded.chapter_id, service_name = excluded.service_name, container_id = excluded.container_id, status = excluded.status, host_port = excluded.host_port, container_port = excluded.container_port, proxy_label = excluded.proxy_label, container_ip = excluded.container_ip, updated_at = excluded.updated_at`, idValue, chapterID, serviceName, containerID, status, hostPort, containerPort, proxyLabel, containerIP, now, now)
		if err != nil {
			return err
		}
		if hostPort != nil {
			_, err = h.Service.DB.ExecContext(ctx, portAllocationUpsert, intValue(item["hostPort"]), chapterID, serviceName, now)
			if err != nil {
				return err
			}
		}
	}
	if len(seen) == 0 {
		_, err := h.Service.DB.ExecContext(ctx, `DELETE FROM container_instances WHERE chapter_id = ?`, chapterID)
		return err
	}
	placeholders, args := chapterInPlaceholders(seen)
	args = append([]any{chapterID}, args...)
	_, err := h.Service.DB.ExecContext(ctx, `DELETE FROM container_instances WHERE chapter_id = ? AND id NOT IN (`+placeholders+`)`, args...)
	return err
}

func chapterInPlaceholders(values []string) (string, []any) {
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, value := range values {
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	return strings.Join(placeholders, ","), args
}

func nullSQLString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func nullSQLInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return int(value.Int64)
}

func nullableIntForDB(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func normalizeContainerInstance(chapterID string, raw map[string]any) map[string]any {
	name := containerName(raw)
	containerID := firstStringValue(raw, "Id", "ID", "ContainerID")
	serviceName := containerServiceName(raw, name)
	hostPort, containerPort := containerPortMapping(raw)
	item := map[string]any{}
	for key, value := range raw {
		item[key] = value
	}
	idValue := containerID
	if idValue == "" {
		idValue = name
	}
	if idValue == "" {
		idValue = serviceName
	}
	item["id"] = idValue
	item["chapterId"] = chapterID
	item["containerId"] = nullableString(containerID)
	item["containerName"] = nullableString(name)
	item["serviceName"] = serviceName
	item["status"] = normalizeContainerStatus(firstStringValue(raw, "State", "Status"))
	item["image"] = nullableString(firstStringValue(raw, "Image"))
	item["hostPort"] = nullableInt(hostPort)
	item["containerPort"] = nullableInt(containerPort)
	item["proxyLabel"] = nil
	item["proxyUrl"] = nil
	item["containerIp"] = nil
	return item
}

func containerName(raw map[string]any) string {
	if names, ok := raw["Names"].([]any); ok && len(names) > 0 {
		return stringValue(names[0])
	}
	if names, ok := raw["Names"].([]string); ok && len(names) > 0 {
		return names[0]
	}
	if name := firstStringValue(raw, "Name", "Names"); name != "" {
		return name
	}
	return ""
}

func containerServiceName(raw map[string]any, fallback string) string {
	if service := firstStringValue(raw, "Service"); service != "" {
		return service
	}
	labels, _ := raw["Labels"].(map[string]any)
	for _, key := range []string{"com.docker.compose.service", "io.podman.compose.service"} {
		if value := stringValue(labels[key]); value != "" {
			return value
		}
	}
	if fallback != "" {
		parts := strings.Split(fallback, "_")
		if len(parts) > 0 && parts[len(parts)-1] != "" {
			return parts[len(parts)-1]
		}
		return fallback
	}
	return "container"
}

func normalizeContainerStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "running") || strings.Contains(value, "up"):
		return "running"
	case strings.Contains(value, "pause"):
		return "paused"
	case strings.Contains(value, "created"):
		return "created"
	case strings.Contains(value, "remove"):
		return "removed"
	case strings.Contains(value, "exit") || strings.Contains(value, "exited") || strings.Contains(value, "dead") || strings.Contains(value, "stop"):
		return "stopped"
	default:
		return "stopped"
	}
}

func containerPortMapping(raw map[string]any) (int, int) {
	for _, key := range []string{"Ports", "Publishers"} {
		if host, target := portMappingValue(raw[key]); host != 0 || target != 0 {
			return host, target
		}
	}
	return 0, 0
}

func portMappingValue(value any) (int, int) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if host, target := portMappingValue(item); host != 0 || target != 0 {
				return host, target
			}
		}
	case map[string]any:
		host := intValue(firstAnyValue(typed, "host_port", "PublishedPort", "HostPort", "published"))
		target := intValue(firstAnyValue(typed, "container_port", "TargetPort", "ContainerPort", "target"))
		return host, target
	case string:
		return parsePortMappingString(typed)
	}
	return 0, 0
}

func parsePortMappingString(value string) (int, int) {
	if !strings.Contains(value, "->") {
		return 0, 0
	}
	parts := strings.SplitN(value, "->", 2)
	hostPart := parts[0]
	targetPart := strings.SplitN(parts[1], "/", 2)[0]
	if idx := strings.LastIndex(hostPart, ":"); idx >= 0 {
		hostPart = hostPart[idx+1:]
	}
	host, _ := strconv.Atoi(strings.TrimSpace(hostPart))
	target, _ := strconv.Atoi(strings.TrimSpace(targetPart))
	return host, target
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func firstStringValue(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(raw[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstAnyValue(raw map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := raw[key]; ok {
			return value
		}
	}
	return nil
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := strconv.Atoi(typed.String())
		return parsed
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}

func nullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func (h *Handler) containerRemove(w http.ResponseWriter, r *http.Request) error {
	var body containerRemoveRequest
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	args := []string{"down", "--remove-orphans"}
	if body.DeleteVolumes {
		args = append(args, "-v")
	}
	return h.containerAction(args...)(w, r)
}

func (h *Handler) containerAction(args ...string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if len(args) > 0 && args[0] == "up" {
			return h.containerStartBackground(w, r, args...)
		}
		chapterID := chi.URLParam(r, "id")
		return withChapterContainerLock(chapterID, func() error {
			chapter, err := h.Service.Get(r.Context(), chapterID)
			if err != nil {
				return err
			}
			cwd := ""
			if chapter.WorktreePath != nil {
				cwd = strings.TrimSpace(*chapter.WorktreePath)
			}
			if cwd == "" {
				return httpx.BadRequest("chapter has no worktree path")
			}
			if commandVersion(r.Context(), "podman", "--version") == "" {
				httpx.WriteJSON(w, http.StatusOK, containerRuntimeFallback("PODMAN_NOT_FOUND", "podman is not available; container action skipped"))
				return nil
			}
			cfg := containerConfigFrom(chapter.ContainerConfig)
			composeFile := resolveComposeFilePath(cwd, cfg)
			if composeFile == "" {
				return httpx.BadRequest("compose file not found")
			}
			proxyInfo, proxyUnsupported, proxyMessage := h.Service.proxyInfoForChapter(r.Context(), chapter)
			if proxyUnsupported {
				return httpx.BadRequest(proxyMessage)
			}
			overrideFile := ""
			if proxyInfo != nil && len(args) > 0 && args[0] == "up" {
				var overrideErr error
				overrideFile, overrideErr = writeProxyOverride(cwd, parseComposeServicesFromFile(composeFile))
				if overrideErr != nil {
					return overrideErr
				}
				if overrideFile != "" {
					defer os.Remove(overrideFile)
				}
			}
			cmd := composeCommandForFiles(r.Context(), composeFile, overrideFile)
			if len(cmd) == 0 {
				httpx.WriteJSON(w, http.StatusOK, containerRuntimeFallback("PODMAN_COMPOSE_NOT_FOUND", "podman compose is not available; container action skipped"))
				return nil
			}
			action := containerBroadcastAction(args...)
			h.broadcastContainerStatusChanged(r.Context(), chapterID, action, "starting", true, "")
			h.broadcastContainerCompat(map[string]any{"type": "container:starting", "chapterId": chapterID})

			mappings := []map[string]any{}
			if len(args) > 0 && args[0] == "up" {
				if proxyInfo != nil {
					if err := h.Service.releaseContainerPorts(r.Context(), chapterID); err != nil {
						h.broadcastContainerStatusChanged(r.Context(), chapterID, action, "error", false, err.Error())
						h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
						return err
					}
				} else {
					mappings, err = h.Service.allocateContainerPorts(r.Context(), chapterID, cfg.Ports)
					if err != nil {
						h.broadcastContainerStatusChanged(r.Context(), chapterID, action, "error", false, err.Error())
						h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
						return err
					}
				}
			}
			env := buildComposeEnv(chapterID, cfg, mappings, proxyInfo)
			fullArgs := append(append([]string{}, cmd[1:]...), args...)
			if len(args) > 0 && args[0] == "up" && len(cfg.Services) > 0 {
				fullArgs = append(fullArgs, cfg.Services...)
			}
			timeout := 60 * time.Second
			if len(args) > 0 && args[0] == "up" {
				timeout = 10 * time.Minute
			}
			var out string
			var runErr error
			if len(args) > 0 && args[0] == "up" {
				logPhase := "build"
				out, runErr = runLimitedEnvTimeoutStreaming(r.Context(), cwd, env, timeout, func(line string) {
					logPhase = h.broadcastContainerLog(chapterID, line, logPhase)
				}, cmd[0], fullArgs...)
			} else {
				out, runErr = runLimitedEnvTimeout(r.Context(), cwd, env, timeout, cmd[0], fullArgs...)
			}
			status := "ok"
			if runErr != nil {
				status = runErr.Error()
				if len(args) > 0 && args[0] == "up" && proxyInfo == nil {
					_ = h.Service.releaseContainerPorts(r.Context(), chapterID)
				}
				h.broadcastContainerStatusChanged(r.Context(), chapterID, action, "error", false, status)
				h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": status})
			} else {
				if err := h.syncContainerActionState(r.Context(), chapterID, cwd, cmd, env, cfg, proxyInfo, mappings, args...); err != nil {
					h.broadcastContainerStatusChanged(r.Context(), chapterID, action, "error", false, err.Error())
					h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
					return err
				}
				containerStatus := h.containerStatusForChapter(r.Context(), chapterID)
				if containerStatus == "" {
					containerStatus = containerSuccessStatus(action)
				}
				h.broadcastContainerStatusChanged(r.Context(), chapterID, action, containerStatus, true, "")
				h.broadcastContainerCompat(containerCompatPayload(chapterID, action, true, ""))
			}
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": runErr == nil, "status": status, "output": out, "fallback": false, "supported": true})
			return nil
		})
	}
}

func (h *Handler) containerStartBackground(w http.ResponseWriter, r *http.Request, args ...string) error {
	chapterID := chi.URLParam(r, "id")
	chapter, err := h.Service.Get(r.Context(), chapterID)
	if err != nil {
		return err
	}
	cwd := ""
	if chapter.WorktreePath != nil {
		cwd = strings.TrimSpace(*chapter.WorktreePath)
	}
	if cwd == "" {
		return httpx.BadRequest("chapter has no worktree path")
	}
	if commandVersion(r.Context(), "podman", "--version") == "" {
		httpx.WriteJSON(w, http.StatusOK, containerRuntimeFallback("PODMAN_NOT_FOUND", "podman is not available; container action skipped"))
		return nil
	}
	cfg := containerConfigFrom(chapter.ContainerConfig)
	composeFile := resolveComposeFilePath(cwd, cfg)
	if composeFile == "" {
		return httpx.BadRequest("compose file not found")
	}
	if _, proxyUnsupported, proxyMessage := h.Service.proxyInfoForChapter(r.Context(), chapter); proxyUnsupported {
		return httpx.BadRequest(proxyMessage)
	}
	if len(composeCommandForFile(r.Context(), composeFile)) == 0 {
		httpx.WriteJSON(w, http.StatusOK, containerRuntimeFallback("PODMAN_COMPOSE_NOT_FOUND", "podman compose is not available; container action skipped"))
		return nil
	}
	go h.runContainerStartJob(chapterID, args...)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "starting", "fallback": false, "supported": true})
	return nil
}

func (h *Handler) runContainerStartJob(chapterID string, args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_ = withChapterContainerLock(chapterID, func() error {
		chapter, err := h.Service.Get(ctx, chapterID)
		if err != nil {
			h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
			return err
		}
		cwd := ""
		if chapter.WorktreePath != nil {
			cwd = strings.TrimSpace(*chapter.WorktreePath)
		}
		if cwd == "" {
			err := httpx.BadRequest("chapter has no worktree path")
			h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
			return err
		}
		cfg := containerConfigFrom(chapter.ContainerConfig)
		composeFile := resolveComposeFilePath(cwd, cfg)
		if composeFile == "" {
			err := httpx.BadRequest("compose file not found")
			h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
			return err
		}
		proxyInfo, proxyUnsupported, proxyMessage := h.Service.proxyInfoForChapter(ctx, chapter)
		if proxyUnsupported {
			err := httpx.BadRequest(proxyMessage)
			h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
			return err
		}
		overrideFile := ""
		if proxyInfo != nil {
			var overrideErr error
			overrideFile, overrideErr = writeProxyOverride(cwd, parseComposeServicesFromFile(composeFile))
			if overrideErr != nil {
				h.broadcastContainerStatusChanged(ctx, chapterID, "start", "error", false, overrideErr.Error())
				h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": overrideErr.Error()})
				return overrideErr
			}
			if overrideFile != "" {
				defer os.Remove(overrideFile)
			}
		}
		cmd := composeCommandForFiles(ctx, composeFile, overrideFile)
		if len(cmd) == 0 {
			err := httpx.BadRequest("podman compose is not available; container action skipped")
			h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
			return err
		}
		action := containerBroadcastAction(args...)
		h.broadcastContainerStatusChanged(ctx, chapterID, action, "starting", true, "")
		h.broadcastContainerCompat(map[string]any{"type": "container:starting", "chapterId": chapterID})
		mappings := []map[string]any{}
		if proxyInfo != nil {
			if err := h.Service.releaseContainerPorts(ctx, chapterID); err != nil {
				h.broadcastContainerStatusChanged(ctx, chapterID, action, "error", false, err.Error())
				h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
				return err
			}
		} else {
			mappings, err = h.Service.allocateContainerPorts(ctx, chapterID, cfg.Ports)
			if err != nil {
				h.broadcastContainerStatusChanged(ctx, chapterID, action, "error", false, err.Error())
				h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
				return err
			}
		}
		env := buildComposeEnv(chapterID, cfg, mappings, proxyInfo)
		fullArgs := append(append([]string{}, cmd[1:]...), args...)
		if len(cfg.Services) > 0 {
			fullArgs = append(fullArgs, cfg.Services...)
		}
		logPhase := "build"
		_, runErr := runLimitedEnvTimeoutStreaming(ctx, cwd, env, 10*time.Minute, func(line string) {
			logPhase = h.broadcastContainerLog(chapterID, line, logPhase)
		}, cmd[0], fullArgs...)
		if runErr != nil {
			status := runErr.Error()
			if proxyInfo == nil {
				_ = h.Service.releaseContainerPorts(ctx, chapterID)
			}
			h.broadcastContainerStatusChanged(ctx, chapterID, action, "error", false, status)
			h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": status})
			return runErr
		}
		if err := h.syncContainerActionState(ctx, chapterID, cwd, cmd, env, cfg, proxyInfo, mappings, args...); err != nil {
			h.broadcastContainerStatusChanged(ctx, chapterID, action, "error", false, err.Error())
			h.broadcastContainerCompat(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
			return err
		}
		_ = h.Service.handleContainerReadyNarratorIntegration(ctx, chapterID)
		containerStatus := h.containerStatusForChapter(ctx, chapterID)
		if containerStatus == "" {
			containerStatus = containerSuccessStatus(action)
		}
		h.broadcastContainerStatusChanged(ctx, chapterID, action, containerStatus, true, "")
		h.broadcastContainerCompat(containerCompatPayload(chapterID, action, true, ""))
		return nil
	})
}

func (h *Handler) broadcastContainerStatusChanged(ctx context.Context, chapterID, action, status string, ok bool, errorMessage string) {
	if h.Broadcaster == nil || strings.TrimSpace(chapterID) == "" {
		return
	}
	narratorIDs := h.narratorIDsForChapter(ctx, chapterID)
	if len(narratorIDs) == 0 {
		return
	}
	containerStatus := h.containerStatusForChapter(ctx, chapterID)
	for _, narratorID := range narratorIDs {
		payload := map[string]any{
			"type":            "container_status_changed",
			"narratorId":      narratorID,
			"chapterId":       chapterID,
			"action":          action,
			"status":          status,
			"ok":              ok,
			"containerStatus": containerStatus,
		}
		if errorMessage != "" {
			payload["error"] = errorMessage
		}
		h.Broadcaster.BroadcastNarrator(narratorID, payload)
	}
}

func (h *Handler) narratorIDsForChapter(ctx context.Context, chapterID string) []string {
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ? ORDER BY CASE WHEN variant = 'primary' THEN 0 ELSE 1 END, created_at ASC`, chapterID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var idValue string
		if err := rows.Scan(&idValue); err == nil && strings.TrimSpace(idValue) != "" {
			ids = append(ids, idValue)
		}
	}
	return ids
}

func (h *Handler) containerStatusForChapter(ctx context.Context, chapterID string) string {
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT status FROM container_instances WHERE chapter_id = ?`, chapterID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	priority := map[string]int{"removed": 0, "stopped": 1, "created": 2, "paused": 3, "running": 4}
	bestStatus := ""
	bestPriority := -1
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			continue
		}
		status = normalizeContainerStatus(status)
		if p := priority[status]; p > bestPriority {
			bestPriority = p
			bestStatus = status
		}
	}
	return bestStatus
}

func (h *Handler) broadcastContainerCompat(payload map[string]any) {
	if h.Broadcaster == nil || payload == nil {
		return
	}
	if global, ok := h.Broadcaster.(interface{ BroadcastMergeProgress(payload any) }); ok {
		global.BroadcastMergeProgress(payload)
	}
}

func (h *Handler) broadcastContainerLog(chapterID, line, currentPhase string) string {
	cleaned := stripANSI(strings.TrimSpace(line))
	if cleaned == "" {
		return currentPhase
	}
	if currentPhase == "" {
		currentPhase = "build"
	}
	if currentPhase == "build" && containerLogIsStartLine(cleaned) {
		currentPhase = "start"
		h.broadcastContainerCompat(map[string]any{"type": "container:log", "chapterId": chapterID, "line": "", "phase": "start"})
	}
	phase := currentPhase
	if containerLogIsBuildLine(cleaned) {
		phase = "build"
	}
	h.broadcastContainerCompat(map[string]any{"type": "container:log", "chapterId": chapterID, "line": cleaned, "phase": phase})
	return currentPhase
}

func containerLogIsStartLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	for _, prefix := range []string{"Container ", "Network ", "Volume ", "Attaching to", "Gracefully stopping", "Killing "} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func containerLogIsBuildLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"step ", "--> ", "cached", "commit", "successfully tagged ", "[internal]", "[build]", "building "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for _, prefix := range []string{"RUN ", "COPY ", "FROM ", "ADD "} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return strings.HasPrefix(lower, "error building at step") || strings.HasPrefix(lower, "error: failed to solve")
}

func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func containerBroadcastAction(args ...string) string {
	if len(args) == 0 {
		return ""
	}
	switch args[0] {
	case "up":
		return "start"
	case "down":
		return "remove"
	default:
		return args[0]
	}
}

func containerSuccessStatus(action string) string {
	switch action {
	case "start", "unpause":
		return "running"
	case "pause":
		return "paused"
	case "stop", "remove":
		return "stopped"
	default:
		return "ok"
	}
}

func containerCompatPayload(chapterID, action string, ok bool, errorMessage string) map[string]any {
	if !ok {
		return map[string]any{"type": "container:error", "chapterId": chapterID, "error": errorMessage}
	}
	switch action {
	case "start":
		return map[string]any{"type": "container:started", "chapterId": chapterID}
	case "stop", "remove":
		return map[string]any{"type": "container:stopped", "chapterId": chapterID}
	case "pause":
		return map[string]any{"type": "container:paused", "chapterId": chapterID}
	case "unpause":
		return map[string]any{"type": "container:resumed", "chapterId": chapterID}
	default:
		return map[string]any{"type": "container:started", "chapterId": chapterID}
	}
}

func (h *Handler) syncContainerActionState(ctx context.Context, chapterID, cwd string, cmd []string, env map[string]string, cfg chapterContainerConfig, proxyInfo *proxyRuntimeInfo, mappings []map[string]any, args ...string) error {
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "up", "unpause":
		items, err := h.Service.liveComposeContainerInstances(ctx, chapterID, cwd, cmd, env, cfg, proxyInfo)
		if err != nil && proxyInfo == nil && len(mappings) > 0 {
			nowItems := make([]map[string]any, 0, len(mappings))
			for _, mapping := range mappings {
				serviceName := stringValue(mapping["serviceName"])
				nowItems = append(nowItems, map[string]any{
					"id":            id.New(),
					"chapterId":     chapterID,
					"serviceName":   firstNonEmpty(serviceName, "container"),
					"containerName": firstNonEmpty(serviceName, "container"),
					"status":        "running",
					"hostPort":      intValue(mapping["hostPort"]),
					"containerPort": intValue(mapping["containerPort"]),
				})
			}
			items = nowItems
			err = nil
		}
		if err != nil {
			return err
		}
		if args[0] == "up" && proxyInfo != nil && len(items) == 0 {
			return httpx.BadRequest("No proxy routes resolved from running containers. Check compose service ports or image EXPOSE settings.")
		}
		return h.Service.persistContainerInstances(ctx, chapterID, items)
	case "pause":
		_, err := h.Service.DB.ExecContext(ctx, `UPDATE container_instances SET status = 'paused', updated_at = ? WHERE chapter_id = ?`, db.NowISO(), chapterID)
		return err
	case "stop":
		_, err := h.Service.DB.ExecContext(ctx, `UPDATE container_instances SET status = 'stopped', updated_at = ? WHERE chapter_id = ?`, db.NowISO(), chapterID)
		return err
	case "down":
		return h.Service.removeContainerRecordsAndPorts(ctx, chapterID)
	default:
		return nil
	}
}

func (h *Handler) containerLogs(w http.ResponseWriter, r *http.Request) error {
	chapterID := chi.URLParam(r, "id")
	chapter, err := h.Service.Get(r.Context(), chapterID)
	if err != nil {
		return err
	}
	cwd := ""
	if chapter.WorktreePath != nil {
		cwd = strings.TrimSpace(*chapter.WorktreePath)
	}
	if cwd == "" {
		return httpx.BadRequest("chapter has no worktree path")
	}
	if commandVersion(r.Context(), "podman", "--version") == "" {
		payload := containerRuntimeFallback("PODMAN_NOT_FOUND", "podman is not available; container logs unavailable")
		payload["logs"] = ""
		httpx.WriteJSON(w, http.StatusOK, payload)
		return nil
	}
	cfg := containerConfigFrom(chapter.ContainerConfig)
	composeFile := resolveComposeFilePath(cwd, cfg)
	if composeFile == "" {
		payload := containerRuntimeFallback("COMPOSE_FILE_NOT_FOUND", "compose file not found; container logs unavailable")
		payload["logs"] = ""
		httpx.WriteJSON(w, http.StatusOK, payload)
		return nil
	}
	cmd := composeCommandForFile(r.Context(), composeFile)
	if len(cmd) == 0 {
		payload := containerRuntimeFallback("PODMAN_COMPOSE_NOT_FOUND", "podman compose is not available; container logs unavailable")
		payload["logs"] = ""
		httpx.WriteJSON(w, http.StatusOK, payload)
		return nil
	}
	tail := 200
	if parsed, parseErr := strconv.Atoi(r.URL.Query().Get("tail")); parseErr == nil && parsed > 0 {
		tail = parsed
	}
	if tail > 1000 {
		tail = 1000
	}
	args := append(append([]string{}, cmd[1:]...), "logs", "--tail", strconv.Itoa(tail))
	if svc := strings.TrimSpace(r.URL.Query().Get("service")); svc != "" && !strings.ContainsAny(svc, "/\\\x00") {
		args = append(args, svc)
	}
	out, runErr := runLimitedEnv(r.Context(), cwd, nil, cmd[0], args...)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": runErr == nil, "logs": out, "truncated": strings.Contains(out, "[truncated]")})
	return nil
}

func (h *Handler) chapterCwd(ctx context.Context, chapterID string) (string, error) {
	chapter, err := h.Service.Get(ctx, chapterID)
	if err != nil {
		return "", err
	}
	if chapter.WorktreePath != nil && *chapter.WorktreePath != "" {
		return *chapter.WorktreePath, nil
	}
	return "", httpx.BadRequest("chapter has no worktree path")
}

type composeProbe struct {
	command []string
	version string
}

func composeVersion(ctx context.Context) composeProbe {
	if v := commandVersion(ctx, "podman", "compose", "version"); v != "" {
		return composeProbe{[]string{"podman", "compose"}, v}
	}
	if v := commandVersion(ctx, "podman-compose", "--version"); v != "" {
		return composeProbe{[]string{"podman-compose"}, v}
	}
	return composeProbe{}
}
func commandVersion(ctx context.Context, name string, args ...string) string {
	out, err := runLimited(ctx, "", name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func mustCommandOutput(ctx context.Context, name string, args ...string) string {
	out, err := runLimited(ctx, "", name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func runLimited(ctx context.Context, cwd, name string, args ...string) (string, error) {
	return runLimitedEnvTimeout(ctx, cwd, nil, 60*time.Second, name, args...)
}

func runLimitedEnv(ctx context.Context, cwd string, env map[string]string, name string, args ...string) (string, error) {
	return runLimitedEnvTimeout(ctx, cwd, env, 60*time.Second, name, args...)
}

func runLimitedEnvTimeoutStreaming(ctx context.Context, cwd string, env map[string]string, timeout time.Duration, onLine func(string), name string, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		merged := os.Environ()
		for key, value := range env {
			merged = append(merged, key+"="+value)
		}
		cmd.Env = merged
	}
	writer := newLimitedCombinedWriter(containerCommandOutputLimit)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return writer.String(), err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return writer.String(), err
	}
	if err := cmd.Start(); err != nil {
		return writer.String(), err
	}
	var wg sync.WaitGroup
	var lineMu sync.Mutex
	stream := func(reader io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			_, _ = writer.Write([]byte(line + "\n"))
			if onLine != nil {
				lineMu.Lock()
				onLine(line)
				lineMu.Unlock()
			}
		}
	}
	wg.Add(2)
	go stream(stdout)
	go stream(stderr)
	err = cmd.Wait()
	wg.Wait()
	out := writer.String()
	if cctx.Err() == context.DeadlineExceeded && err != nil {
		if strings.TrimSpace(out) != "" {
			out += "\n"
		}
		out += "[timed out]"
	}
	return out, err
}

func runLimitedEnvTimeout(ctx context.Context, cwd string, env map[string]string, timeout time.Duration, name string, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		merged := os.Environ()
		for key, value := range env {
			merged = append(merged, key+"="+value)
		}
		cmd.Env = merged
	}
	writer := newLimitedCombinedWriter(containerCommandOutputLimit)
	cmd.Stdout = writer
	cmd.Stderr = writer
	err := cmd.Run()
	out := writer.String()
	if cctx.Err() == context.DeadlineExceeded && err != nil {
		if strings.TrimSpace(out) != "" {
			out += "\n"
		}
		out += "[timed out]"
	}
	return out, err
}
func hasComposeFile(cwd string) bool {
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
			return true
		}
	}
	return false
}
func parseComposeServices(cwd string) []map[string]any {
	data, ok := readCompose(cwd)
	if !ok {
		return []map[string]any{}
	}
	lines := strings.Split(data, "\n")
	inServices := false
	services := []map[string]any{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "services:") {
			inServices = true
			continue
		}
		if inServices && len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			break
		}
		if inServices && strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "#") {
			name := strings.TrimSuffix(trimmed, ":")
			if name != "" && name != "ports" && name != "environment" {
				services = append(services, map[string]any{"name": name, "ports": []any{}, "environment": map[string]any{}})
			}
		}
	}
	return services
}
func readCompose(cwd string) (string, bool) {
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		content, _, err := readComposeFileLimited(filepath.Join(cwd, name))
		if err == nil {
			return content, true
		}
	}
	return "", false
}
func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func (h *Handler) notImplemented(feature string) httpx.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error { return httpx.NotImplemented(feature) }
}
