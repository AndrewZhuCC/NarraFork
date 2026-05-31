package narrators

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/auth"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/simplecontent"
)

type presenceSnapshotter interface {
	NarratorIDsWithPresence() []string
}

type Handler struct {
	Service  *Service
	Presence presenceSnapshotter
}

func NewHandler(service *Service) *Handler { return &Handler{Service: service} }

func NewHandlerWithPresence(service *Service, presence presenceSnapshotter) *Handler {
	return &Handler{Service: service, Presence: presence}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(h.list))
	r.Post("/", httpx.Handler(h.create))
	r.Post("/permissions/{requestId}/approve", httpx.Handler(h.decidePermission("approved")))
	r.Post("/permissions/{requestId}/deny", httpx.Handler(h.decidePermission("denied")))
	r.Post("/permissions/{requestId}/stop-reflection", httpx.Handler(h.stopDangerReflection))
	r.Post("/permissions/{requestId}/stop-plan-reflection", httpx.Handler(h.decidePermission("cancelled")))
	r.Patch("/whitelist-dirs/{entryId}", httpx.Handler(h.updateRule("narrator_whitelist_dirs", "access_level")))
	r.Delete("/whitelist-dirs/{entryId}", httpx.Handler(h.deleteRule("narrator_whitelist_dirs")))
	r.Patch("/blacklist-dirs/{entryId}", httpx.Handler(h.updateRule("narrator_blacklist_dirs", "deny_level")))
	r.Delete("/blacklist-dirs/{entryId}", httpx.Handler(h.deleteRule("narrator_blacklist_dirs")))
	r.Patch("/cmd-whitelist/{entryId}", httpx.Handler(h.updateRule("narrator_whitelist_cmds", "pattern")))
	r.Delete("/cmd-whitelist/{entryId}", httpx.Handler(h.deleteRule("narrator_whitelist_cmds")))
	r.Patch("/cmd-blacklist/{entryId}", httpx.Handler(h.updateRule("narrator_blacklist_cmds", "pattern")))
	r.Delete("/cmd-blacklist/{entryId}", httpx.Handler(h.deleteRule("narrator_blacklist_cmds")))
	r.Get("/{id}", httpx.Handler(h.get))
	r.Get("/{id}/draft", httpx.Handler(h.getDraft))
	r.Put("/{id}/draft", httpx.Handler(h.putDraft))
	r.Get("/{id}/usage-stats", httpx.Handler(h.usageStats))
	r.Get("/{id}/commands", httpx.Handler(h.commands))
	r.Get("/{id}/messages", httpx.Handler(h.messages))
	r.Post("/{id}/messages", httpx.Handler(h.postMessage))
	r.Delete("/{id}/messages/batch-blocks", httpx.Handler(h.deleteMessageBlocks))
	r.Delete("/{id}/messages/{messageId}", httpx.Handler(h.deleteMessage))
	r.Delete("/{id}/messages/{messageId}/blocks/{blockIndex}", httpx.Handler(h.deleteMessageBlock))
	r.Delete("/{id}/error-messages/{messageId}", httpx.Handler(h.dismissErrorMessage))
	r.Get("/{id}/tool-calls/{toolUseId}", httpx.Handler(h.toolCallDetail))
	r.Get("/{id}/permissions", httpx.Handler(h.permissions))
	r.Get("/{id}/goals", httpx.Handler(h.getGoals))
	r.Post("/{id}/goals", httpx.Handler(h.addGoal))
	r.Patch("/{id}/goals/{goalId}", httpx.Handler(h.updateGoal))
	r.Delete("/{id}/goals/{goalId}", httpx.Handler(h.removeGoal))
	r.Put("/{id}/goals/reorder", httpx.Handler(h.reorderGoals))
	r.Delete("/{id}/goals", httpx.Handler(h.clearGoals))
	r.Get("/{id}/buffer", httpx.Handler(h.getBuffer))
	r.Patch("/{id}/buffer/{messageId}", httpx.Handler(h.updateBuffer))
	r.Delete("/{id}/buffer/{messageId}", httpx.Handler(h.removeBuffer))
	r.Put("/{id}/buffer/reorder", httpx.Handler(h.reorderBuffer))
	r.Delete("/{id}/buffer", httpx.Handler(h.clearBuffer))
	r.Get("/{id}/whitelist-dirs", httpx.Handler(h.listRules("narrator_whitelist_dirs")))
	r.Post("/{id}/whitelist-dirs", httpx.Handler(h.createRule("narrator_whitelist_dirs", "path", "access_level", "readOnly")))
	r.Get("/{id}/blacklist-dirs", httpx.Handler(h.listRules("narrator_blacklist_dirs")))
	r.Post("/{id}/blacklist-dirs", httpx.Handler(h.createRule("narrator_blacklist_dirs", "path", "deny_level", "denyAll")))
	r.Get("/{id}/cmd-whitelist", httpx.Handler(h.listRules("narrator_whitelist_cmds")))
	r.Post("/{id}/cmd-whitelist", httpx.Handler(h.createRule("narrator_whitelist_cmds", "pattern", "", "")))
	r.Get("/{id}/cmd-blacklist", httpx.Handler(h.listRules("narrator_blacklist_cmds")))
	r.Post("/{id}/cmd-blacklist", httpx.Handler(h.createRule("narrator_blacklist_cmds", "pattern", "deny_prompt", "")))
	r.Get("/{id}/custom-traits", httpx.Handler(h.customTraits))
	r.Put("/{id}/custom-traits/subagent-model-restriction", httpx.Handler(h.setSubagentModelRestriction))
	r.Delete("/{id}/custom-traits/subagent-model-restriction", httpx.Handler(h.clearSubagentModelRestriction))
	r.Put("/{id}/custom-traits/disabled-tools", httpx.Handler(h.setDisabledTools))
	r.Delete("/{id}/custom-traits/disabled-tools", httpx.Handler(h.clearDisabledTools))
	r.Get("/{id}/file-modifications", httpx.Handler(h.fileModifications))
	r.Get("/{id}/patches", httpx.Handler(h.patches))
	r.Get("/{id}/patches/{snapshotId}/diff", httpx.Handler(h.fileDiff))
	r.Post("/{id}/revert-file", httpx.Handler(h.revertFile))
	r.Post("/{id}/revert", httpx.Handler(h.revertAll))
	r.Post("/{id}/unrevert", httpx.Handler(h.unrevertAll))
	r.Get("/{id}/delete-preview", httpx.Handler(h.deletePreview))
	r.Get("/{id}/rollback-preview", httpx.Handler(h.rollbackPreview))
	r.Get("/{id}/permission-file-preview", httpx.Handler(h.permissionFilePreview))
	r.Patch("/{id}/title", httpx.Handler(h.updateTitle))
	r.Patch("/{id}/cwd", httpx.Handler(h.updateCwd))
	r.Post("/{id}/generate-title", httpx.Handler(h.generateTitle))
	r.Patch("/{id}/model", httpx.Handler(h.updateModel))
	r.Patch("/{id}/permission-mode", httpx.Handler(h.updatePermissionMode))
	r.Patch("/{id}/reasoning-effort", httpx.Handler(h.updateReasoningEffort))
	r.Patch("/{id}/fast-mode", httpx.Handler(h.updateBool("fast_mode", "fastMode")))
	r.Patch("/{id}/relaxed-plan", httpx.Handler(h.updateBool("relaxed_plan", "relaxedPlan")))
	r.Patch("/{id}/reflection-overrides", httpx.Handler(h.updateReflectionOverrides))
	r.Patch("/{id}/prune-enabled", httpx.Handler(h.updateBool("prune_enabled", "pruneEnabled")))
	r.Patch("/{id}/archive", httpx.Handler(h.archive))
	r.Patch("/{id}/unarchive", httpx.Handler(h.unarchive))
	r.Patch("/{id}/mark-read", httpx.Handler(h.markRead))
	r.Post("/{id}/interrupt", httpx.Handler(h.interrupt))
	r.Post("/{id}/detach", httpx.Handler(h.detachSubagent))
	r.Post("/{id}/attach", httpx.Handler(h.attachSubagent))
	r.Post("/{id}/leave", httpx.Handler(h.leave))
	r.Get("/{id}/background-tasks", httpx.Handler(h.backgroundTasks))
	r.Post("/{id}/background-tasks/{taskId}/cancel", httpx.Handler(h.cancelBackgroundTask))
	r.Get("/{id}/background-tasks/{taskId}/output", httpx.Handler(h.backgroundTaskOutput))
	r.Post("/{id}/update-conclusion", httpx.Handler(h.updateConclusion))
	r.Post("/{id}/suggest-answers", httpx.Handler(h.suggestAnswers))
	r.Post("/permissions/{requestId}/reflect-question", httpx.Handler(h.reflectQuestion))
	r.Post("/{id}/retry", httpx.Handler(h.retry))
	r.Post("/{id}/continue", httpx.Handler(h.continueRun))
	r.Post("/{id}/rollback/{messageId}", httpx.Handler(h.rollbackMessage))
	r.Post("/{id}/edit-and-regenerate/{messageId}", httpx.Handler(h.editAndRegenerate))
	r.Post("/{id}/fork", httpx.Handler(h.forkNarrator))
	r.Post("/{id}/fork-messages", httpx.Handler(h.forkMessages))
	r.Get("/{id}/browser-sessions", httpx.Handler(h.listBrowserSessions))
	r.Patch("/{id}/browser-sessions/{sessionId}/ttl", httpx.Handler(h.setBrowserSessionTtl))
	r.Delete("/{id}/browser-sessions/{sessionId}", httpx.Handler(h.closeBrowserSession))
	r.Get("/{id}/browser-sessions/{sessionId}/screenshot", httpx.Handler(h.browserSessionScreenshot))
	r.Post("/{id}/browser-sessions/{sessionId}/stop-tracing", httpx.Handler(h.stopBrowserTracing))
	r.Post("/{id}/ask-in-passing/start", httpx.Handler(h.startAskInPassing))
	r.Post("/{id}/ask-in-passing", httpx.Handler(h.askInPassing))
	r.Delete("/{id}/ask-in-passing/{messageId}", httpx.Handler(h.cancelAskInPassing))
	r.Post("/{id}/promote", httpx.Handler(h.promoteNarrator))
	r.Post("/{id}/compact", httpx.Handler(h.compact))
	r.Get("/{id}/compact/{messageId}", httpx.Handler(h.compactSummary))
	r.Patch("/{id}/compact/{messageId}", httpx.Handler(h.updateCompactSummary))
	r.Delete("/{id}/compact/{messageId}", httpx.Handler(h.deleteCompactMessage))
	r.Post("/{id}/segment-compact", httpx.Handler(h.segmentCompact))
	r.Get("/{id}/segment-compact/{messageId}", httpx.Handler(h.segmentCompactSummary))
	r.Get("/{id}/segment-compact/{messageId}/messages", httpx.Handler(h.segmentCompactMessages))
	r.Patch("/{id}/segment-compact/{messageId}", httpx.Handler(h.updateSegmentCompactSummary))
	r.Delete("/{id}/segment-compact/{messageId}", httpx.Handler(h.deleteSegmentCompact))
	r.Post("/{id}/clear-context", httpx.Handler(h.clearContext))
	r.Post("/{id}/plan", httpx.Handler(h.plan))
	r.Post("/{id}/plan-mode/enter", httpx.Handler(h.setPlanMode(true)))
	r.Post("/{id}/plan-mode/exit", httpx.Handler(h.setPlanMode(false)))
	r.Delete("/{id}", httpx.Handler(h.deleteNarrator))
}

type createRequest struct {
	ChapterID                         *string `json:"chapterId"`
	Title                             *string `json:"title"`
	Model                             string  `json:"model"`
	SystemPrompt                      *string `json:"systemPrompt"`
	PermissionMode                    string  `json:"permissionMode"`
	StartInPlanMode                   *bool   `json:"startInPlanMode"`
	ReasoningEffort                   *string `json:"reasoningEffort"`
	FastMode                          *bool   `json:"fastMode"`
	RelaxedPlan                       *bool   `json:"relaxedPlan"`
	PlanReflectionAutoApproveOverride string  `json:"planReflectionAutoApproveOverride"`
	DangerReflectionOverride          string  `json:"dangerReflectionOverride"`
	Cwd                               *string `json:"cwd"`
}

type messageRequest struct {
	Message  string `json:"message"`
	Text     string `json:"text"`
	Priority bool   `json:"priority"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	if h.Service == nil || h.Service.DB == nil {
		return httpx.NotImplemented("Narrator runtime unavailable")
	}
	if shouldUsePaginatedNarratorList(r) {
		return h.listPaginated(w, r)
	}
	chapterID := strings.TrimSpace(r.URL.Query().Get("chapterId"))
	if chapterID == "" {
		return httpx.ValidationError("chapterId or standalone=true is required")
	}
	items, err := h.Service.List(r.Context(), chapterID, r.URL.Query().Get("status"), false)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func shouldUsePaginatedNarratorList(r *http.Request) bool {
	query := r.URL.Query()
	if query.Get("standalone") == "true" || query.Get("standalone") == "all" || query.Has("limit") || query.Has("cursor") {
		return true
	}
	for _, key := range []string{"filter", "hasTerminals", "hasContainers", "hasRunningContainers", "hasViewers"} {
		if query.Has(key) {
			return true
		}
	}
	return false
}

func (h *Handler) listPaginated(w http.ResponseWriter, r *http.Request) error {
	if h.Service == nil || h.Service.DB == nil {
		return httpx.NotImplemented("Narrator runtime unavailable")
	}
	query := r.URL.Query()
	// Without a presence snapshot provider (for example in narrow handler tests),
	// hasViewers safely falls back to the normal list instead of forcing empty.
	limit := queryInt(query.Get("limit"), 20)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	where, args := paginatedNarratorWhere(query.Get("standalone"), query.Get("status"), query.Get("filter"), query)
	if query.Get("hasViewers") == "true" && h.Presence != nil {
		ids := h.Presence.NarratorIDsWithPresence()
		if len(ids) == 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": []any{}, "hasMore": false, "nextCursor": nil, "totalCount": 0})
			return nil
		}
		where += " AND narrators.id IN (" + placeholders(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	var totalCount int
	if err := h.Service.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM narrators `+where, args...).Scan(&totalCount); err != nil {
		return err
	}
	sortBy, sortColumn := narratorSortColumn(query.Get("sortBy"))
	sortOrder := "DESC"
	cmp := "<"
	if query.Get("sortOrder") == "asc" {
		sortOrder = "ASC"
		cmp = ">"
	}
	listWhere := where
	listArgs := append([]any{}, args...)
	if cursorWhere, cursorArgs := narratorCursorWhere(query.Get("cursor"), sortColumn, cmp, sortBy == "messageCount"); cursorWhere != "" {
		listWhere += " AND " + cursorWhere
		listArgs = append(listArgs, cursorArgs...)
	}
	listArgs = append(listArgs, limit+1)
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT `+narratorSelectColumns+` FROM narrators `+listWhere+` ORDER BY `+sortColumn+` `+sortOrder+`, id `+sortOrder+` LIMIT ?`, listArgs...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Narrator{}
	for rows.Next() {
		item, err := scanNarrator(rows)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	enriched, err := h.enrichNarratorList(r.Context(), items)
	if err != nil {
		return err
	}
	var nextCursor any
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = encodeNarratorCursor(sortBy, last)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": enriched, "hasMore": hasMore, "nextCursor": nextCursor, "totalCount": totalCount})
	return nil
}

func paginatedNarratorWhere(standalone, status, filter string, values url.Values) (string, []any) {
	conditions := []string{"variant = 'primary'"}
	args := []any{}
	if status == "archived" {
		conditions = append(conditions, "status = 'archived'")
	} else {
		conditions = append(conditions, "status != 'archived'")
	}
	if standalone == "true" || filter == "standalone" {
		conditions = append(conditions, "chapter_id IS NULL")
	} else if filter == "chapter" {
		conditions = append(conditions, "chapter_id IS NOT NULL")
	}
	if values.Get("hasTerminals") == "true" {
		conditions = append(conditions, "EXISTS (SELECT 1 FROM terminals WHERE terminals.narrator_id = narrators.id AND terminals.status = 'running')")
	}
	if values.Get("hasRunningContainers") == "true" {
		conditions = append(conditions, "(EXISTS (SELECT 1 FROM container_instances WHERE container_instances.chapter_id = narrators.chapter_id AND container_instances.status = 'running') OR (NOT EXISTS (SELECT 1 FROM container_instances WHERE container_instances.chapter_id = narrators.chapter_id) AND EXISTS (SELECT 1 FROM chapters WHERE chapters.id = narrators.chapter_id AND TRIM(COALESCE(chapters.container_config, '')) NOT IN ('', 'null', '{}', '[]'))))")
	} else if values.Get("hasContainers") == "true" {
		conditions = append(conditions, "(EXISTS (SELECT 1 FROM container_instances WHERE container_instances.chapter_id = narrators.chapter_id) OR EXISTS (SELECT 1 FROM chapters WHERE chapters.id = narrators.chapter_id AND TRIM(COALESCE(chapters.container_config, '')) NOT IN ('', 'null', '{}', '[]'))) ")
	}
	return "WHERE " + strings.Join(conditions, " AND "), args
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func narratorSortColumn(sortBy string) (string, string) {
	switch sortBy {
	case "createdAt":
		return "createdAt", "created_at"
	case "title":
		return "title", "title"
	case "messageCount":
		return "messageCount", "message_count"
	default:
		return "updatedAt", "updated_at"
	}
}

func narratorCursorWhere(cursor, sortColumn, cmp string, numeric bool) (string, []any) {
	if strings.TrimSpace(cursor) == "" {
		return "", nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", nil
	}
	var decoded struct {
		Value any    `json:"v"`
		ID    string `json:"id"`
	}
	if json.Unmarshal(data, &decoded) != nil || decoded.ID == "" {
		return "", nil
	}
	if numeric {
		value := 0
		switch typed := decoded.Value.(type) {
		case float64:
			value = int(typed)
		case string:
			value, _ = strconv.Atoi(typed)
		}
		return "(" + sortColumn + " " + cmp + " ? OR (" + sortColumn + " = ? AND id " + cmp + " ?))", []any{value, value, decoded.ID}
	}
	value := stringValue(decoded.Value)
	return "(" + sortColumn + " " + cmp + " ? OR (" + sortColumn + " = ? AND id " + cmp + " ?))", []any{value, value, decoded.ID}
}

func encodeNarratorCursor(sortBy string, n Narrator) any {
	value := any(n.UpdatedAt)
	switch sortBy {
	case "createdAt":
		value = n.CreatedAt
	case "title":
		if n.Title != nil {
			value = *n.Title
		} else {
			value = ""
		}
	case "messageCount":
		value = n.MessageCount
	}
	data, err := json.Marshal(map[string]any{"v": value, "id": n.ID})
	if err != nil {
		return nil
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func (h *Handler) enrichNarratorList(ctx context.Context, narrators []Narrator) ([]map[string]any, error) {
	items := make([]map[string]any, 0, len(narrators))
	if len(narrators) == 0 {
		return items, nil
	}
	narratorIDs := []string{}
	chapterIDs := []string{}
	for _, n := range narrators {
		narratorIDs = append(narratorIDs, n.ID)
		if n.ChapterID != nil && *n.ChapterID != "" {
			chapterIDs = append(chapterIDs, *n.ChapterID)
		}
	}
	chapterMap, containerCounts, err := h.chapterSummaries(ctx, uniqueStrings(chapterIDs))
	if err != nil {
		return nil, err
	}
	terminalCounts, err := h.runningTerminalCounts(ctx, uniqueStrings(narratorIDs))
	if err != nil {
		return nil, err
	}
	activeGoals, err := h.activeGoalNarrators(ctx, uniqueStrings(narratorIDs))
	if err != nil {
		return nil, err
	}
	for _, n := range narrators {
		item := narratorJSONMap(n)
		item["chapter"] = nil
		item["containerCount"] = 0
		item["runningContainerCount"] = 0
		if n.ChapterID != nil {
			if chapter := chapterMap[*n.ChapterID]; chapter != nil {
				item["chapter"] = chapter
			}
			if counts := containerCounts[*n.ChapterID]; counts != nil {
				item["containerCount"] = counts["total"]
				item["runningContainerCount"] = counts["running"]
			}
		}
		item["activeTerminalCount"] = terminalCounts[n.ID]
		item["viewers"] = []any{}
		item["hasActiveGoal"] = activeGoals[n.ID]
		items = append(items, item)
	}
	return items, nil
}

func (h *Handler) chapterSummaries(ctx context.Context, chapterIDs []string) (map[string]map[string]any, map[string]map[string]int, error) {
	chapters := map[string]map[string]any{}
	containerCounts := map[string]map[string]int{}
	if len(chapterIDs) == 0 {
		return chapters, containerCounts, nil
	}
	placeholders, args := inPlaceholders(chapterIDs)
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT c.id, c.title, c.project_id, p.name, c.status, c.role, c.container_config FROM chapters c LEFT JOIN projects p ON p.id = c.project_id WHERE c.id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	fallbackContainerConfig := map[string]bool{}
	for rows.Next() {
		var id, title, projectID, status, role string
		var projectName, containerConfig sql.NullString
		if err := rows.Scan(&id, &title, &projectID, &projectName, &status, &role, &containerConfig); err != nil {
			return nil, nil, err
		}
		chapters[id] = map[string]any{"id": id, "title": title, "projectId": projectID, "projectName": nullString(projectName), "status": status, "role": role}
		fallbackContainerConfig[id] = hasContainerConfigRaw(containerConfig)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	persistedCounts, err := h.persistedContainerCounts(ctx, chapterIDs)
	if err != nil {
		return nil, nil, err
	}
	for id, counts := range persistedCounts {
		containerCounts[id] = counts
	}
	for id, hasConfig := range fallbackContainerConfig {
		if hasConfig && containerCounts[id] == nil {
			containerCounts[id] = map[string]int{"total": 1, "running": 1}
		}
	}
	return chapters, containerCounts, nil
}

func (h *Handler) persistedContainerCounts(ctx context.Context, chapterIDs []string) (map[string]map[string]int, error) {
	counts := map[string]map[string]int{}
	if len(chapterIDs) == 0 {
		return counts, nil
	}
	placeholders, args := inPlaceholders(chapterIDs)
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT chapter_id, COUNT(*), COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END), 0) FROM container_instances WHERE chapter_id IN (`+placeholders+`) GROUP BY chapter_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var chapterID string
		var total, running int
		if err := rows.Scan(&chapterID, &total, &running); err != nil {
			return nil, err
		}
		counts[chapterID] = map[string]int{"total": total, "running": running}
	}
	return counts, rows.Err()
}

func (h *Handler) runningTerminalCounts(ctx context.Context, narratorIDs []string) (map[string]int, error) {
	counts := map[string]int{}
	if len(narratorIDs) == 0 {
		return counts, nil
	}
	placeholders, args := inPlaceholders(narratorIDs)
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT narrator_id, COUNT(*) FROM terminals WHERE status = 'running' AND narrator_id IN (`+placeholders+`) GROUP BY narrator_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var narratorID string
		var count int
		if err := rows.Scan(&narratorID, &count); err != nil {
			return nil, err
		}
		counts[narratorID] = count
	}
	return counts, rows.Err()
}

func (h *Handler) activeGoalNarrators(ctx context.Context, narratorIDs []string) (map[string]bool, error) {
	active := map[string]bool{}
	if len(narratorIDs) == 0 {
		return active, nil
	}
	placeholders, args := inPlaceholders(narratorIDs)
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT narrator_id FROM narrator_goals WHERE status IN ('active', 'in_progress') AND narrator_id IN (`+placeholders+`) GROUP BY narrator_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var narratorID string
		if err := rows.Scan(&narratorID); err != nil {
			return nil, err
		}
		active[narratorID] = true
	}
	return active, rows.Err()
}

func narratorJSONMap(n Narrator) map[string]any {
	data, err := json.Marshal(n)
	if err != nil {
		return map[string]any{"id": n.ID}
	}
	out := map[string]any{}
	if json.Unmarshal(data, &out) != nil {
		return map[string]any{"id": n.ID}
	}
	return out
}

func hasContainerConfigRaw(v sql.NullString) bool {
	if !v.Valid {
		return false
	}
	raw := strings.TrimSpace(v.String)
	if raw == "" || raw == "null" || raw == "{}" || raw == "[]" {
		return false
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(raw), &decoded) == nil {
		return len(decoded) > 0
	}
	return true
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func inPlaceholders(values []string) (string, []any) {
	placeholders := make([]string, 0, len(values))
	args := make([]any, 0, len(values))
	for _, value := range values {
		placeholders = append(placeholders, "?")
		args = append(args, value)
	}
	return strings.Join(placeholders, ","), args
}

func queryInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var req createRequest
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	if err := validateCreateRequest(req); err != nil {
		return err
	}
	n, err := h.Service.Create(r.Context(), CreateInput{
		ChapterID:                         req.ChapterID,
		Title:                             req.Title,
		Model:                             req.Model,
		SystemPrompt:                      req.SystemPrompt,
		PermissionMode:                    req.PermissionMode,
		StartInPlanMode:                   req.StartInPlanMode,
		ReasoningEffort:                   req.ReasoningEffort,
		FastMode:                          req.FastMode,
		RelaxedPlan:                       req.RelaxedPlan,
		PlanReflectionAutoApproveOverride: req.PlanReflectionAutoApproveOverride,
		DangerReflectionOverride:          req.DangerReflectionOverride,
		Cwd:                               req.Cwd,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, n)
	return nil
}

func validateCreateRequest(req createRequest) error {
	if req.SystemPrompt != nil && len(*req.SystemPrompt) > 10000 {
		return httpx.BadRequest("systemPrompt must be at most 10000 characters")
	}
	if req.ReasoningEffort != nil && !validReasoningEffort(*req.ReasoningEffort) {
		return httpx.BadRequest("reasoningEffort must be one of: none, low, medium, high, xhigh, or null")
	}
	if req.PlanReflectionAutoApproveOverride != "" && !validBooleanOverride(req.PlanReflectionAutoApproveOverride) {
		return httpx.BadRequest("planReflectionAutoApproveOverride must be inherit, on, or off")
	}
	if req.DangerReflectionOverride != "" && !validBooleanOverride(req.DangerReflectionOverride) {
		return httpx.BadRequest("dangerReflectionOverride must be inherit, on, or off")
	}
	return nil
}

func validReasoningEffort(value string) bool {
	switch value {
	case "none", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func validBooleanOverride(value string) bool {
	switch value {
	case "inherit", "on", "off":
		return true
	default:
		return false
	}
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	n, err := h.Service.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, n)
	return nil
}

func (h *Handler) messages(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	query := r.URL.Query()
	var page MessagePage
	var err error
	if around := strings.TrimSpace(query.Get("around")); around != "" {
		before := queryInt(query.Get("before"), 5)
		after := queryInt(query.Get("after"), 20)
		page, err = h.Service.MessagesAround(r.Context(), narratorID, around, before, after)
	} else if afterCursor := strings.TrimSpace(query.Get("catchUpCursor")); afterCursor != "" || strings.TrimSpace(query.Get("after")) != "" {
		catchUpCursor, parseErr := parseCatchUpCursorQuery(query.Get("catchUpCursor"), query.Get("after"))
		if parseErr != nil {
			return parseErr
		}
		limit := queryInt(query.Get("limit"), 40)
		catchUpPage, err := h.Service.MessagesAfter(r.Context(), narratorID, catchUpCursor, limit)
		if err != nil {
			return err
		}
		meta, err := h.messagePageMeta(r.Context(), narratorID)
		if err != nil {
			return err
		}
		response := map[string]any{"topLevel": catchUpPage.TopLevel, "orphanChildren": catchUpPage.OrphanChildren, "hitLimit": catchUpPage.HitLimit, "cursor": catchUpPage.Cursor, "messages": catchUpPage.TopLevel}
		for key, value := range meta {
			response[key] = value
		}
		httpx.WriteJSON(w, http.StatusOK, response)
		return nil
	} else {
		limit := queryInt(query.Get("limit"), 50)
		direction := query.Get("direction")
		page, err = h.Service.MessagesCursor(r.Context(), narratorID, limit, query.Get("cursor"), direction)
	}
	if err != nil {
		return err
	}
	meta, err := h.messagePageMeta(r.Context(), narratorID)
	if err != nil {
		return err
	}
	response := map[string]any{"messages": page.Messages, "hasMore": page.HasMore, "nextCursor": page.NextCursor, "hasMoreAfter": page.HasMoreAfter, "prevCursor": page.PrevCursor}
	for key, value := range meta {
		response[key] = value
	}
	httpx.WriteJSON(w, http.StatusOK, response)
	return nil
}

func parseCatchUpCursorQuery(rawCursor, rawAfter string) (CatchUpCursor, error) {
	rawCursor = strings.TrimSpace(rawCursor)
	if rawCursor != "" {
		decoded, err := url.QueryUnescape(rawCursor)
		if err != nil {
			return CatchUpCursor{}, httpx.BadRequest("invalid catchUpCursor")
		}
		var cursor CatchUpCursor
		if err := json.Unmarshal([]byte(decoded), &cursor); err != nil {
			return CatchUpCursor{}, httpx.BadRequest("invalid catchUpCursor")
		}
		return cursor, nil
	}
	rawAfter = strings.TrimSpace(rawAfter)
	if strings.HasPrefix(rawAfter, "{") {
		decoded, err := url.QueryUnescape(rawAfter)
		if err != nil {
			return CatchUpCursor{}, httpx.BadRequest("invalid after cursor")
		}
		var cursor CatchUpCursor
		if err := json.Unmarshal([]byte(decoded), &cursor); err != nil {
			return CatchUpCursor{}, httpx.BadRequest("invalid after cursor")
		}
		return cursor, nil
	}
	return CatchUpCursor{ParentLastMessageID: nullableStringPtrValue(rawAfter), ChildAnchors: []CatchUpChildAnchor{}}, nil
}

func (h *Handler) messagePageMeta(ctx context.Context, narratorID string) (map[string]any, error) {
	var pruneBoundary sql.NullString
	var prunedPercent sql.NullInt64
	var messageVersion int
	if err := h.Service.DB.QueryRowContext(ctx, `SELECT prune_boundary_message_id, pruned_percent, COALESCE(message_version, 0) FROM narrators WHERE id = ?`, narratorID).Scan(&pruneBoundary, &prunedPercent, &messageVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpx.NotFound("Narrator")
		}
		return nil, err
	}
	return map[string]any{"pruneBoundaryMessageId": nullString(pruneBoundary), "prunedPercent": nullInt(prunedPercent), "messageVersion": messageVersion}, nil
}

func (h *Handler) deleteMessage(w http.ResponseWriter, r *http.Request) error {
	deleted, err := h.deleteMessageAndFollowingRefs(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "messageId"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "deletedCount": deleted})
	return nil
}

func (h *Handler) dismissErrorMessage(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "messageId")
	if err := h.ensureMessageBlockType(r.Context(), narratorID, messageID, "system", "error", "Message is not an error notice"); err != nil {
		return err
	}
	deleted, err := h.deleteMessageRef(r.Context(), narratorID, messageID)
	if err != nil {
		return err
	}
	if deleted > 0 {
		if _, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET error_message = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), narratorID); err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": deleted > 0})
	return nil
}

func (h *Handler) deleteCompactMessage(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "messageId")
	if err := h.ensureMessageBlockType(r.Context(), narratorID, messageID, "system", "compact", "Message is not a compact message"); err != nil {
		return err
	}
	deleted, err := h.deleteMessageRef(r.Context(), narratorID, messageID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": deleted > 0})
	return nil
}

func (h *Handler) deleteMessageBlock(w http.ResponseWriter, r *http.Request) error {
	blockIndex, err := strconv.Atoi(chi.URLParam(r, "blockIndex"))
	if err != nil || blockIndex < 0 {
		return httpx.BadRequest("invalid blockIndex")
	}
	messageDeleted, err := h.deleteMessageBlockCore(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "messageId"), blockIndex)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "messageDeleted": messageDeleted})
	return nil
}

func (h *Handler) deleteMessageBlocks(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Blocks []struct {
			MessageID  string `json:"messageId"`
			BlockIndex int    `json:"blockIndex"`
		} `json:"blocks"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	narratorID := chi.URLParam(r, "id")
	deleted := 0
	failed := 0
	for _, block := range body.Blocks {
		if block.MessageID == "" || block.BlockIndex < 0 {
			failed++
			continue
		}
		if _, err := h.deleteMessageBlockCore(r.Context(), narratorID, block.MessageID, block.BlockIndex); err != nil {
			failed++
		} else {
			deleted++
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": failed == 0, "deleted": deleted, "failed": failed})
	return nil
}

func (h *Handler) updateCompactSummary(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Summary string `json:"summary"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if err := h.updateMessageSummary(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "messageId"), body.Summary); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) deleteMessageAndFollowingRefs(ctx context.Context, narratorID, messageID string) (int64, error) {
	var targetSeq int
	if err := h.Service.DB.QueryRowContext(ctx, `SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, messageID).Scan(&targetSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpx.NotFound("Message")
		}
		return 0, err
	}
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT message_id FROM narrator_message_refs WHERE narrator_id = ? AND seq >= ? ORDER BY seq ASC`, narratorID, targetSeq)
	if err != nil {
		return 0, err
	}
	messageIDs := []string{}
	for rows.Next() {
		var idValue string
		if err := rows.Scan(&idValue); err != nil {
			rows.Close()
			return 0, err
		}
		messageIDs = append(messageIDs, idValue)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(messageIDs) == 0 {
		return 0, nil
	}
	tx, err := h.Service.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM narrator_message_refs WHERE narrator_id = ? AND seq >= ?`, narratorID, targetSeq)
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()
	uniqueIDs := uniqueStrings(messageIDs)
	placeholders, args := inPlaceholders(uniqueIDs)
	args = append(args, narratorID)
	if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_messages WHERE id IN (`+placeholders+`) AND narrator_id = ? AND NOT EXISTS (SELECT 1 FROM narrator_message_refs WHERE message_id = narrator_messages.id)`, args...); err != nil {
		return 0, err
	}
	if count > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE narrators SET api_conversation_id = NULL, prune_boundary_message_id = NULL, pruned_percent = NULL, message_count = CASE WHEN message_count > ? THEN message_count - ? ELSE 0 END, message_version = message_version + 1, updated_at = ? WHERE id = ?`, count, count, db.NowISO(), narratorID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func (h *Handler) deleteMessageRef(ctx context.Context, narratorID, messageID string) (int64, error) {
	tx, err := h.Service.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, messageID)
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()
	if count > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_messages WHERE id = ? AND narrator_id = ? AND NOT EXISTS (SELECT 1 FROM narrator_message_refs WHERE message_id = ?)`, messageID, narratorID, messageID); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE narrators SET message_count = CASE WHEN message_count > 0 THEN message_count - ? ELSE 0 END, message_version = message_version + 1, updated_at = ? WHERE id = ?`, count, db.NowISO(), narratorID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func (h *Handler) ensureMessageBlockType(ctx context.Context, narratorID, messageID, role, blockType, message string) error {
	actualRole, blocks, err := h.messageRoleAndBlocks(ctx, narratorID, messageID)
	if err != nil {
		return err
	}
	if role != "" && actualRole != role {
		return httpx.BadRequest(message)
	}
	for _, block := range blocks {
		if stringValue(block["type"]) == blockType {
			return nil
		}
	}
	return httpx.BadRequest(message)
}

func (h *Handler) messageRoleAndBlocks(ctx context.Context, narratorID, messageID string) (string, []map[string]any, error) {
	var role string
	var raw string
	if err := h.Service.DB.QueryRowContext(ctx, `SELECT m.role, m.content_json FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND r.message_id = ?`, narratorID, messageID).Scan(&role, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, httpx.NotFound("Message")
		}
		return "", nil, err
	}
	blocks := []map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return role, blocks, nil
	}
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		return "", nil, err
	}
	return role, blocks, nil
}

func (h *Handler) deleteMessageBlockCore(ctx context.Context, narratorID, messageID string, blockIndex int) (bool, error) {
	result, err := h.Service.deleteMessageBlock(ctx, narratorID, messageID, blockIndex, false, true)
	if err != nil {
		return false, err
	}
	if result.MessageDeleted {
		if h.Service.Hub != nil {
			h.Service.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "messages_deleted", "narratorId": narratorID, "deletedMessageIds": []string{result.MessageID}})
		}
		return true, nil
	}
	updated, err := h.Service.messageForNarratorRef(ctx, narratorID, result.PrivateMessageID)
	if err == nil {
		h.Service.broadcastMessageUpdated(ctx, narratorID, updated.Message)
		if result.PrivateMessageID != result.MessageID && h.Service.Hub != nil {
			h.Service.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "full_reload", "narratorId": narratorID})
		}
	}
	return false, nil
}

func contentTextFromBlocks(blocks []any) string {
	texts := []string{}
	for _, block := range blocks {
		if m, ok := block.(map[string]any); ok {
			if text, ok := m["text"].(string); ok && strings.TrimSpace(text) != "" {
				texts = append(texts, text)
			}
		}
	}
	return strings.Join(texts, "\n")
}

func (h *Handler) updateMessageSummary(ctx context.Context, narratorID, messageID, summary string) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return httpx.BadRequest("summary is required")
	}
	var raw string
	if err := h.Service.DB.QueryRowContext(ctx, `SELECT content_json FROM narrator_messages WHERE id = ? AND narrator_id = ?`, messageID, narratorID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("Message")
		}
		return err
	}
	var blocks []map[string]any
	_ = json.Unmarshal([]byte(raw), &blocks)
	if len(blocks) == 0 {
		return httpx.BadRequest("message is not a compact message")
	}
	block := blocks[0]
	blockType := stringValue(block["type"])
	if blockType != "compact" && blockType != "segment_compact" {
		return httpx.BadRequest("message is not a compact message")
	}
	block["status"] = "compacted"
	block["summary"] = summary
	content := []map[string]any{block}
	data, _ := json.Marshal(content)
	contentText := "[Compact] " + summary
	if blockType == "segment_compact" {
		contentText = "[Segment Compact]\n" + summary
	}
	tx, err := h.Service.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE narrator_messages SET content_json = ?, content_text = ? WHERE id = ? AND narrator_id = ?`, string(data), contentText, messageID, narratorID)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return httpx.NotFound("Message")
	}
	now := db.NowISO()
	if blockType == "compact" {
		_, err = tx.ExecContext(ctx, `UPDATE narrators SET context_summary = ?, api_conversation_id = NULL, message_version = message_version + 1, updated_at = ? WHERE id = ?`, summary, now, narratorID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE narrators SET api_conversation_id = NULL, message_version = message_version + 1, updated_at = ? WHERE id = ?`, now, narratorID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (h *Handler) segmentCompactRefs(ctx context.Context, narratorID string, messageIDs []string, fromID, toID string) ([]map[string]any, error) {
	query := `SELECT r.message_id, r.seq, m.role, COALESCE(m.content_text, ''), m.created_at FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND r.segment_compact_id IS NULL`
	args := []any{narratorID}
	if len(messageIDs) > 0 {
		placeholders, idArgs := inPlaceholders(messageIDs)
		query += ` AND r.message_id IN (` + placeholders + `)`
		args = append(args, idArgs...)
	}
	if fromID != "" {
		query += ` AND r.seq >= COALESCE((SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?), r.seq)`
		args = append(args, narratorID, fromID)
	}
	if toID != "" {
		query += ` AND r.seq <= COALESCE((SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?), r.seq)`
		args = append(args, narratorID, toID)
	}
	query += ` ORDER BY r.seq ASC LIMIT 200`
	rows, err := h.Service.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []map[string]any{}
	for rows.Next() {
		var idValue, role, text, createdAt string
		var seq int
		if err := rows.Scan(&idValue, &seq, &role, &text, &createdAt); err != nil {
			return nil, err
		}
		refs = append(refs, map[string]any{"messageId": idValue, "seq": seq, "role": role, "text": text, "createdAt": createdAt})
	}
	return refs, rows.Err()
}

const compactFallbackSummaryReason = "TS-equivalent model-generated compaction is not implemented in go_backend; summaries are caller-provided or deterministic from refs."

func (h *Handler) buildSummaryFromRefs(ctx context.Context, refs []map[string]any, fallback string) (string, error) {
	parts := []string{}
	for _, ref := range refs {
		text := strings.TrimSpace(stringValue(ref["text"]))
		if text != "" {
			parts = append(parts, text)
		}
		if len(parts) >= 8 {
			break
		}
	}
	fallbackPrefix := fallback + " fallback summary generated by Go backend because AI compact summaries are not available yet."
	if len(parts) == 0 {
		return fallbackPrefix + " No prior messages were available to summarize.", nil
	}
	joined := strings.Join(parts, "\n")
	if len(joined) > 4000 {
		joined = joined[:4000] + "\n..."
	}
	return fallbackPrefix + "\n\n" + joined, nil
}

func (h *Handler) createCompactMessage(ctx context.Context, narratorID, summary, kind string, insertSeq *int) (Message, error) {
	textPrefix := "[Compact]"
	role := "system"
	isCompact := true
	content := []map[string]any{{"type": "compact", "status": "compacted", "summary": summary}}
	if kind == "segment_compact" {
		textPrefix = "[Segment Compact]"
		role = "user"
		isCompact = false
		content = []map[string]any{{"type": "segment_compact", "status": "compacted", "summary": summary}}
	}
	text := textPrefix
	if summary != "" {
		text += "\n" + summary
	}
	return h.Service.addMessageWithBlocksAtSeq(ctx, narratorID, role, text, content, nil, insertSeq, isCompact)
}

func (h *Handler) broadcastCompactEvent(narratorID string, payload map[string]any) {
	if h.Service == nil || h.Service.Hub == nil {
		return
	}
	payload["narratorId"] = narratorID
	h.Service.Hub.BroadcastNarrator(narratorID, payload)
}

func minSeqFromRefs(refs []map[string]any) *int {
	if len(refs) == 0 {
		return nil
	}
	minSeq := intValue(refs[0]["seq"])
	for _, ref := range refs[1:] {
		seq := intValue(ref["seq"])
		if seq < minSeq {
			minSeq = seq
		}
	}
	return &minSeq
}

func (h *Handler) segmentCompact(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	var body struct {
		MessageIDs []string `json:"messageIds"`
		FromID     string   `json:"fromMessageId"`
		ToID       string   `json:"toMessageId"`
		Summary    string   `json:"summary"`
	}
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	refs, err := h.segmentCompactRefs(r.Context(), narratorID, body.MessageIDs, body.FromID, body.ToID)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return httpx.BadRequest("no matching messages found for segment compact")
	}
	h.broadcastCompactEvent(narratorID, map[string]any{"type": "compacting", "mode": "blocking"})
	summary := strings.TrimSpace(body.Summary)
	if summary == "" {
		summary, err = h.buildSummaryFromRefs(r.Context(), refs, "Segment compact")
		if err != nil {
			h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "mode": "blocking"})
			return err
		}
	}
	msg, err := h.createCompactMessage(r.Context(), narratorID, summary, "segment_compact", minSeqFromRefs(refs))
	if err != nil {
		h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "mode": "blocking"})
		return err
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if idValue := stringValue(ref["messageId"]); idValue != "" {
			ids = append(ids, idValue)
		}
	}
	if len(ids) > 0 {
		placeholders, args := inPlaceholders(ids)
		args = append([]any{msg.ID, narratorID}, args...)
		_, err = h.Service.DB.ExecContext(r.Context(), `UPDATE narrator_message_refs SET segment_compact_id = ? WHERE narrator_id = ? AND message_id IN (`+placeholders+`)`, args...)
		if err != nil {
			h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "messageId": msg.ID, "mode": "blocking"})
			return err
		}
	}
	if _, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET api_conversation_id = NULL, message_version = message_version + 1, updated_at = ? WHERE id = ?`, db.NowISO(), narratorID); err != nil {
		h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "messageId": msg.ID, "mode": "blocking"})
		return err
	}
	h.broadcastCompactEvent(narratorID, map[string]any{"type": "message", "message": msg})
	h.broadcastCompactEvent(narratorID, map[string]any{"type": "segment_compact_hide", "hiddenMessageIds": ids})
	h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_done", "isSegment": true, "mode": "blocking"})
	summarySource := "caller"
	summaryQuality := "caller"
	if body.Summary == "" {
		summarySource = "deterministic_fallback"
		summaryQuality = "deterministic_preview"
	}
	payload := map[string]any{
		"ok":                    true,
		"message":               msg,
		"summary":               summary,
		"messageCount":          len(ids),
		"fallbackSummary":       body.Summary == "",
		"summarySource":         summarySource,
		"summaryQuality":        summaryQuality,
		"modelSummaryGenerated": false,
	}
	if body.Summary == "" {
		payload["fallbackReason"] = compactFallbackSummaryReason
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) listBrowserSessions(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	browserDB := h.Service.browserDB()
	if ok, err := tableExistsOn(r.Context(), browserDB, "narrator_browser_sessions"); err != nil || !ok {
		httpx.WriteJSON(w, http.StatusOK, browserSessionItemsFromSnapshots(agentbasic.LiveBrowserSessionSnapshots(narratorID)))
		return nil
	}
	items, err := h.loadBrowserSessions(r.Context(), narratorID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) closeBrowserSession(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	sessionID := chi.URLParam(r, "sessionId")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	liveClosed := agentbasic.CloseBrowserSession(r.Context(), narratorID, sessionID, "closed_by_api")
	browserDB := h.Service.browserDB()
	hasStorage, err := tableExistsOn(r.Context(), browserDB, "narrator_browser_sessions")
	if err != nil {
		return err
	}
	if !hasStorage {
		if liveClosed {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
			return nil
		}
		writeBrowserSessionsDisabled(w)
		return nil
	}
	result, err := browserDB.ExecContext(r.Context(), `UPDATE narrator_browser_sessions SET status = 'closed', closed_at = COALESCE(closed_at, ?), updated_at = ? WHERE narrator_id = ? AND id = ?`, db.NowISO(), db.NowISO(), narratorID, sessionID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 && !liveClosed {
		return httpx.NotFound("Browser session")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) setBrowserSessionTtl(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	sessionID := chi.URLParam(r, "sessionId")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	var body map[string]any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	ttlNumber, ok := body["ttlMs"].(float64)
	if !ok || float64(int64(ttlNumber)) != ttlNumber {
		return httpx.ValidationError("ttlMs must be an integer number of milliseconds")
	}
	ttlMs := int64(ttlNumber)
	if ttlMs < int64(time.Second/time.Millisecond) || ttlMs > int64(24*time.Hour/time.Millisecond) {
		return httpx.ValidationError("ttlMs must be between 1000 and 86400000")
	}
	expiresAtTime := time.Now().UTC().Add(time.Duration(ttlMs) * time.Millisecond)
	expiresAt := any(expiresAtTime.Format(time.RFC3339Nano))
	expiresAtMs := any(expiresAtTime.UnixMilli())
	ttlValue := any(ttlMs / 1000)
	ttlMsValue := any(ttlMs)
	liveSnapshot, liveUpdated, liveErr := agentbasic.SetBrowserSessionTTL(r.Context(), narratorID, sessionID, ttlMs)
	if liveErr != nil {
		return liveErr
	}
	if liveUpdated {
		expiresAt = liveSnapshot.ExpiresAt.UTC().Format(time.RFC3339Nano)
		expiresAtMs = liveSnapshot.ExpiresAt.UnixMilli()
		ttlValue = liveSnapshot.TtlMs / 1000
		ttlMsValue = liveSnapshot.TtlMs
	}
	browserDB := h.Service.browserDB()
	hasStorage, err := tableExistsOn(r.Context(), browserDB, "narrator_browser_sessions")
	if err != nil {
		return err
	}
	if !hasStorage {
		if liveUpdated {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "session": browserSessionItemFromSnapshot(liveSnapshot)})
			return nil
		}
		writeBrowserSessionsDisabled(w)
		return nil
	}
	columns, _ := tableColumnSet(r.Context(), browserDB, "narrator_browser_sessions")
	sets := []string{"ttl_seconds = ?", "expires_at = ?", "updated_at = ?"}
	args := []any{ttlValue, expiresAt, db.NowISO()}
	if columns["ttl_ms"] {
		sets = append(sets, "ttl_ms = ?")
		args = append(args, ttlMsValue)
	}
	if columns["expires_at_ms"] {
		sets = append(sets, "expires_at_ms = ?")
		args = append(args, expiresAtMs)
	}
	args = append(args, narratorID, sessionID)
	result, err := browserDB.ExecContext(r.Context(), `UPDATE narrator_browser_sessions SET `+strings.Join(sets, ", ")+` WHERE narrator_id = ? AND id = ?`, args...)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 && !liveUpdated {
		return httpx.NotFound("Browser session")
	}
	session := browserSessionItemFromSnapshot(liveSnapshot)
	if !liveUpdated {
		session, err = h.loadBrowserSessionItem(r.Context(), narratorID, sessionID)
		if err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "session": session})
	return nil
}

func (h *Handler) stopBrowserTracing(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	sessionID := chi.URLParam(r, "sessionId")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	liveSnapshot, liveHandled, liveErr := agentbasic.StopBrowserSessionTracing(r.Context(), narratorID, sessionID)
	if liveErr != nil {
		return liveErr
	}
	if liveHandled && liveSnapshot.TraceMetadata != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	}
	browserDB := h.Service.browserDB()
	hasStorage, err := tableExistsOn(r.Context(), browserDB, "narrator_browser_sessions")
	if err != nil {
		return err
	}
	if !hasStorage {
		writeBrowserSessionsDisabled(w)
		return nil
	}
	var raw sql.NullString
	err = browserDB.QueryRowContext(r.Context(), `SELECT tracing_json FROM narrator_browser_sessions WHERE narrator_id = ? AND id = ?`, narratorID, sessionID).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("Browser session")
		}
		return err
	}
	trace := map[string]any{}
	if decoded, ok := decodeAny(raw).(map[string]any); ok {
		trace = decoded
	}
	trace["status"] = "stopped"
	trace["stoppedAt"] = db.NowISO()
	data, _ := json.Marshal(trace)
	_, err = browserDB.ExecContext(r.Context(), `UPDATE narrator_browser_sessions SET tracing_status = 'stopped', tracing_json = ?, updated_at = ? WHERE narrator_id = ? AND id = ?`, string(data), db.NowISO(), narratorID, sessionID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) browserSessionScreenshot(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	sessionID := chi.URLParam(r, "sessionId")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	if data, _, liveHandled, liveErr := agentbasic.CaptureBrowserSessionScreenshot(r.Context(), narratorID, sessionID); liveErr != nil {
		return liveErr
	} else if liveHandled && len(data) > 0 {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return nil
	}
	browserDB := h.Service.browserDB()
	hasStorage, err := tableExistsOn(r.Context(), browserDB, "narrator_browser_sessions")
	if err != nil {
		return err
	}
	if !hasStorage {
		writeBrowserSessionsDisabled(w)
		return nil
	}
	var mime, dataRaw sql.NullString
	err = browserDB.QueryRowContext(r.Context(), `SELECT screenshot_mime, screenshot_data_base64 FROM narrator_browser_sessions WHERE narrator_id = ? AND id = ? AND status != 'closed'`, narratorID, sessionID).Scan(&mime, &dataRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("Browser session screenshot")
		}
		return err
	}
	if !dataRaw.Valid || strings.TrimSpace(dataRaw.String) == "" {
		return httpx.NotFound("Browser session screenshot")
	}
	image, err := base64.StdEncoding.DecodeString(dataRaw.String)
	if err != nil {
		return err
	}
	contentType := "image/png"
	if mime.Valid && strings.TrimSpace(mime.String) != "" {
		contentType = mime.String
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image)
	return nil
}

func (h *Handler) fileModifications(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	narrator, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	cwd := narratorWorkingDir(r.Context(), h.Service.DB, narrator)
	type snapshotRow struct {
		ID        string
		FilePath  string
		Original  *string
		CreatedAt string
	}
	snapshotRows, err := h.Service.DB.QueryContext(r.Context(), `SELECT id, file_path, original_content, created_at FROM narrator_file_snapshots WHERE narrator_id = ? ORDER BY created_at ASC`, narratorID)
	if err != nil {
		return err
	}
	defer snapshotRows.Close()
	snapshots := []snapshotRow{}
	snapshotOriginals := map[string]*string{}
	for snapshotRows.Next() {
		var snap snapshotRow
		var original sql.NullString
		if err := snapshotRows.Scan(&snap.ID, &snap.FilePath, &original, &snap.CreatedAt); err != nil {
			return err
		}
		if original.Valid {
			value := original.String
			snap.Original = &value
		}
		snapshotOriginals[snap.FilePath] = snap.Original
		snapshots = append(snapshots, snap)
	}
	if err := snapshotRows.Err(); err != nil {
		return err
	}
	if len(snapshots) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"files": []any{}, "timeline": []any{}})
		return nil
	}
	allToolCalls, err := queryOrderedFileToolCalls(r.Context(), h.Service.DB, narratorID, cwd, nil)
	if err != nil {
		return err
	}
	timeline, fromSeq, toSeq, err := h.fileModificationTimeline(r.Context(), narratorID, r.URL.Query().Get("fromMessageId"), r.URL.Query().Get("upToMessageId"), allToolCalls)
	if err != nil {
		return err
	}
	isRangeFiltered := fromSeq != nil || toSeq != nil
	grouped := map[string][]orderedFileToolCall{}
	for _, call := range allToolCalls {
		if fromSeq != nil && call.Seq <= *fromSeq {
			continue
		}
		if toSeq != nil && call.Seq > *toSeq {
			continue
		}
		key := canonicalToolCallFilePath(call, snapshotOriginals)
		if key == "" {
			continue
		}
		grouped[key] = append(grouped[key], call)
	}
	files := []map[string]any{}
	for _, snap := range snapshots {
		ops := grouped[snap.FilePath]
		if isRangeFiltered && len(ops) == 0 {
			continue
		}
		lastModifiedAt := snap.CreatedAt
		if len(ops) > 0 {
			lastModifiedAt = ops[len(ops)-1].CreatedAt
		}
		operations := []map[string]any{}
		for _, op := range ops {
			operations = append(operations, map[string]any{"toolUseId": op.ToolUseID, "toolName": op.ToolName, "messageId": op.MessageID, "createdAt": op.CreatedAt})
		}
		files = append(files, map[string]any{"filePath": snap.FilePath, "snapshotId": snap.ID, "originalExists": snap.Original != nil, "editCount": len(ops), "lastModifiedAt": lastModifiedAt, "operations": operations})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"files": files, "timeline": timeline})
	return nil
}

func (h *Handler) fileModificationTimeline(ctx context.Context, narratorID, fromMessageID, upToMessageID string, calls []orderedFileToolCall) ([]map[string]any, *int, *int, error) {
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT refs.message_id, refs.seq, msg.role, msg.created_at FROM narrator_message_refs refs INNER JOIN narrator_messages msg ON msg.id = refs.message_id WHERE refs.narrator_id = ? AND (msg.parent_tool_use_id IS NULL OR TRIM(msg.parent_tool_use_id) = '') ORDER BY refs.seq ASC`, narratorID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	hasEdits := map[string]struct{}{}
	for _, call := range calls {
		hasEdits[call.MessageID] = struct{}{}
	}
	timeline := []map[string]any{}
	var fromSeq *int
	var toSeq *int
	for rows.Next() {
		var messageID, role, createdAt string
		var seq int
		if err := rows.Scan(&messageID, &seq, &role, &createdAt); err != nil {
			return nil, nil, nil, err
		}
		_, edit := hasEdits[messageID]
		timeline = append(timeline, map[string]any{"messageId": messageID, "createdAt": createdAt, "seq": seq, "role": role, "hasEdits": edit})
		if fromMessageID != "" && messageID == fromMessageID {
			value := seq
			fromSeq = &value
		}
		if upToMessageID != "" && messageID == upToMessageID {
			value := seq
			toSeq = &value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	return timeline, fromSeq, toSeq, nil
}

func (h *Handler) patches(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	if ok, err := h.tableExists(r.Context(), "narrator_file_snapshots"); err != nil {
		return err
	} else if ok {
		return h.writeFileSnapshots(w, r, narratorID)
	}
	if ok, err := h.tableExists(r.Context(), "narrator_patches"); err != nil {
		return err
	} else if ok {
		return h.writeLegacyPatches(w, r, narratorID)
	}
	reason := "No narrator patch or snapshot storage table is available in this database"
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"patches":   []any{},
		"snapshots": []any{},
		"fallback":  true,
		"supported": false,
		"reason":    reason,
		"error":     reason,
		"message":   reason,
	})
	return nil
}

func (h *Handler) writeFileSnapshots(w http.ResponseWriter, r *http.Request, narratorID string) error {
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT id, narrator_id, file_path, original_content, created_at FROM narrator_file_snapshots WHERE narrator_id = ? ORDER BY created_at ASC`, narratorID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idValue, rowNarratorID, filePath, createdAt string
		var original sql.NullString
		if err := rows.Scan(&idValue, &rowNarratorID, &filePath, &original, &createdAt); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": idValue, "narratorId": rowNarratorID, "filePath": filePath, "originalContent": nullString(original), "createdAt": createdAt})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) writeLegacyPatches(w http.ResponseWriter, r *http.Request, narratorID string) error {
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT id, narrator_id, message_id, tool_use_id, before_hash, after_hash, files_json, created_at FROM narrator_patches WHERE narrator_id = ? ORDER BY created_at ASC`, narratorID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idValue, rowNarratorID, createdAt string
		var messageID, toolUseID, beforeHash, afterHash, filesRaw sql.NullString
		if err := rows.Scan(&idValue, &rowNarratorID, &messageID, &toolUseID, &beforeHash, &afterHash, &filesRaw, &createdAt); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": idValue, "narratorId": rowNarratorID, "messageId": nullString(messageID), "toolUseId": nullString(toolUseID), "beforeHash": nullString(beforeHash), "afterHash": nullString(afterHash), "files": decodeAny(filesRaw), "createdAt": createdAt, "source": "narrator_patches"})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) fileDiff(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	narrator, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	cwd := narratorWorkingDir(r.Context(), h.Service.DB, narrator)
	var filePath string
	var original sql.NullString
	err = h.Service.DB.QueryRowContext(r.Context(), `SELECT file_path, original_content FROM narrator_file_snapshots WHERE narrator_id = ? AND id = ?`, narratorID, chi.URLParam(r, "snapshotId")).Scan(&filePath, &original)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("File snapshot")
		}
		return err
	}
	var originalContent *string
	if fromMessageID := strings.TrimSpace(r.URL.Query().Get("fromMessageId")); fromMessageID != "" {
		seq, ok, err := h.narratorMessageSeq(r.Context(), narratorID, fromMessageID)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.NotFound("fromMessageId")
		}
		states, err := rebuildFileStatesUpToSeq(r.Context(), h.Service.DB, narratorID, cwd, &seq)
		if err != nil {
			return err
		}
		originalContent = states[filePath]
		if _, ok := states[filePath]; !ok {
			originalContent = nullStringPtr(original)
		}
	} else {
		originalContent = nullStringPtr(original)
	}
	var currentContent *string
	if upToMessageID := strings.TrimSpace(r.URL.Query().Get("upToMessageId")); upToMessageID != "" {
		seq, ok, err := h.narratorMessageSeq(r.Context(), narratorID, upToMessageID)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.NotFound("Message")
		}
		states, err := rebuildFileStatesUpToSeq(r.Context(), h.Service.DB, narratorID, cwd, &seq)
		if err != nil {
			return err
		}
		currentContent = states[filePath]
		if _, ok := states[filePath]; !ok {
			currentContent = nullStringPtr(original)
		}
	} else {
		currentContent, err = rebuildFileState(r.Context(), h.Service.DB, narratorID, filePath, cwd, nil)
		if err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"filePath": filePath, "original": originalContent, "current": currentContent})
	return nil
}

func (h *Handler) narratorMessageSeq(ctx context.Context, narratorID, messageID string) (int, bool, error) {
	var seq int
	err := h.Service.DB.QueryRowContext(ctx, `SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, messageID).Scan(&seq)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return seq, true, nil
}

func (h *Handler) revertFile(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	narrator, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	if err := activeNarratorMutationError(narrator, "revert"); err != nil {
		return err
	}
	cwd := narratorWorkingDir(r.Context(), h.Service.DB, narrator)
	var body struct {
		FilePath string `json:"filePath"`
	}
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	filePath := strings.TrimSpace(body.FilePath)
	if filePath == "" {
		return httpx.BadRequest("filePath is required")
	}
	var original sql.NullString
	err = h.Service.DB.QueryRowContext(r.Context(), `SELECT original_content FROM narrator_file_snapshots WHERE narrator_id = ? AND file_path = ?`, narratorID, filePath).Scan(&original)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("File snapshot")
		}
		return err
	}
	if err := applySnapshotContent(resolveSnapshotFilePath(cwd, filePath), nullStringPtr(original)); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "filePath": filePath, "originalExists": original.Valid})
	return nil
}

func (h *Handler) revertAll(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	narrator, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	if err := activeNarratorMutationError(narrator, "revert"); err != nil {
		return err
	}
	cwd := narratorWorkingDir(r.Context(), h.Service.DB, narrator)
	var body struct {
		MessageID string `json:"messageId"`
	}
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	messageID := strings.TrimSpace(body.MessageID)
	if messageID != "" && messageID != "__all__" {
		seq, ok, err := h.narratorMessageSeq(r.Context(), narratorID, messageID)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.NotFound("Message")
		}
		allCalls, err := queryOrderedFileToolCalls(r.Context(), h.Service.DB, narratorID, cwd, nil)
		if err != nil {
			return err
		}
		exclude := map[string]struct{}{}
		affected := []string{}
		seen := map[string]struct{}{}
		for _, call := range allCalls {
			if call.Seq < seq {
				continue
			}
			exclude[call.ToolUseID] = struct{}{}
			for _, path := range []string{call.RawFilePath, call.ResolvedFilePath} {
				if path == "" {
					continue
				}
				if _, ok := seen[path]; !ok {
					seen[path] = struct{}{}
					affected = append(affected, path)
				}
			}
		}
		if len(affected) == 0 {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"fileCount": 0, "files": []any{}})
			return nil
		}
		states, err := rebuildFileStatesExcluding(r.Context(), h.Service.DB, narratorID, cwd, affected, exclude, nil)
		if err != nil {
			return err
		}
		files, err := writeRebuiltFileStates(cwd, states, false)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"fileCount": len(files), "files": files})
		return nil
	}
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT file_path, original_content FROM narrator_file_snapshots WHERE narrator_id = ?`, narratorID)
	if err != nil {
		return err
	}
	defer rows.Close()
	files := []string{}
	for rows.Next() {
		var filePath string
		var original sql.NullString
		if err := rows.Scan(&filePath, &original); err != nil {
			return err
		}
		if applySnapshotContent(resolveSnapshotFilePath(cwd, filePath), nullStringPtr(original)) == nil {
			files = append(files, filePath)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"fileCount": len(files), "files": files})
	return nil
}

func (h *Handler) unrevertAll(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	narrator, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	if err := activeNarratorMutationError(narrator, "unrevert"); err != nil {
		return err
	}
	cwd := narratorWorkingDir(r.Context(), h.Service.DB, narrator)
	states, err := rebuildFileStatesUpToSeq(r.Context(), h.Service.DB, narratorID, cwd, nil)
	if err != nil {
		return err
	}
	files, err := writeRebuiltFileStates(cwd, states, true)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "fileCount": len(files), "files": files})
	return nil
}

func (h *Handler) deletePreview(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	narrator, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	messageID := strings.TrimSpace(r.URL.Query().Get("messageId"))
	if messageID == "" {
		return httpx.BadRequest("messageId query param is required")
	}
	seq, ok, err := h.narratorMessageSeq(r.Context(), narratorID, messageID)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.NotFound("Message")
	}
	cwd := narratorWorkingDir(r.Context(), h.Service.DB, narrator)
	allCalls, err := queryOrderedFileToolCalls(r.Context(), h.Service.DB, narratorID, cwd, nil)
	if err != nil {
		return err
	}
	snapshots, err := querySnapshotOriginals(r.Context(), h.Service.DB, narratorID)
	if err != nil {
		return err
	}
	exclude := map[string]struct{}{}
	affectedSet := map[string]struct{}{}
	for _, call := range allCalls {
		if call.Seq < seq {
			continue
		}
		exclude[call.ToolUseID] = struct{}{}
		key := canonicalToolCallFilePath(call, snapshots)
		if key != "" {
			affectedSet[key] = struct{}{}
		}
	}
	affected := sortedMapKeys(affectedSet)
	if len(affected) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"affectedFiles": []any{}, "toolCallCount": 0})
		return nil
	}
	currentStates, err := rebuildFileStatesExcluding(r.Context(), h.Service.DB, narratorID, cwd, affected, map[string]struct{}{}, nil)
	if err != nil {
		return err
	}
	revertedStates, err := rebuildFileStatesExcluding(r.Context(), h.Service.DB, narratorID, cwd, affected, exclude, nil)
	if err != nil {
		return err
	}
	files := []map[string]any{}
	for _, filePath := range affected {
		current := currentStates[filePath]
		reverted := revertedStates[filePath]
		files = append(files, map[string]any{"filePath": filePath, "currentContent": current, "revertedContent": reverted, "willBeDeleted": reverted == nil})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"affectedFiles": files, "toolCallCount": len(exclude)})
	return nil
}

func (h *Handler) rollbackPreview(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	narrator, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	messageID := strings.TrimSpace(r.URL.Query().Get("messageId"))
	if messageID == "" {
		return httpx.BadRequest("messageId query param is required")
	}
	blockIndex, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("blockIndex")))
	if err != nil || blockIndex < 0 {
		return httpx.BadRequest("blockIndex must be a non-negative integer")
	}
	seq, ok, err := h.narratorMessageSeq(r.Context(), narratorID, messageID)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.NotFound("Message")
	}
	var contentRaw string
	if err := h.Service.DB.QueryRowContext(r.Context(), `SELECT content_json FROM narrator_messages WHERE id = ? AND narrator_id = ?`, messageID, narratorID).Scan(&contentRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("Message")
		}
		return err
	}
	blocks := []any{}
	_ = json.Unmarshal([]byte(contentRaw), &blocks)
	if blockIndex >= len(blocks) {
		return httpx.BadRequest("blockIndex out of range")
	}
	truncatedToolUseIDs := map[string]struct{}{}
	for i := blockIndex + 1; i < len(blocks); i++ {
		block, _ := blocks[i].(map[string]any)
		if stringFromAny(block["type"]) == "tool_use" {
			if toolUseID := stringFromAny(block["id"]); toolUseID != "" {
				truncatedToolUseIDs[toolUseID] = struct{}{}
			}
		}
	}
	deletedMessageCount, err := h.deletedMessageCountAfterSeq(r.Context(), narratorID, seq)
	if err != nil {
		return err
	}
	cwd := narratorWorkingDir(r.Context(), h.Service.DB, narrator)
	allCalls, err := queryOrderedFileToolCalls(r.Context(), h.Service.DB, narratorID, cwd, nil)
	if err != nil {
		return err
	}
	snapshots, err := querySnapshotOriginals(r.Context(), h.Service.DB, narratorID)
	if err != nil {
		return err
	}
	exclude := map[string]struct{}{}
	affectedSet := map[string]struct{}{}
	for _, call := range allCalls {
		_, truncated := truncatedToolUseIDs[call.ToolUseID]
		if !truncated && call.Seq <= seq {
			continue
		}
		exclude[call.ToolUseID] = struct{}{}
		key := canonicalToolCallFilePath(call, snapshots)
		if key != "" {
			affectedSet[key] = struct{}{}
		}
	}
	affected := sortedMapKeys(affectedSet)
	if len(affected) == 0 {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"affectedFiles": []any{}, "toolCallCount": 0, "deletedBlockCount": len(blocks) - blockIndex - 1, "deletedMessageCount": deletedMessageCount})
		return nil
	}
	currentStates, err := rebuildFileStatesExcluding(r.Context(), h.Service.DB, narratorID, cwd, affected, map[string]struct{}{}, nil)
	if err != nil {
		return err
	}
	revertedStates, err := rebuildFileStatesExcluding(r.Context(), h.Service.DB, narratorID, cwd, affected, exclude, nil)
	if err != nil {
		return err
	}
	files := []map[string]any{}
	for _, filePath := range affected {
		current := currentStates[filePath]
		reverted := revertedStates[filePath]
		files = append(files, map[string]any{"filePath": filePath, "currentContent": current, "revertedContent": reverted, "willBeDeleted": reverted == nil})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"affectedFiles": files, "toolCallCount": len(exclude), "deletedBlockCount": len(blocks) - blockIndex - 1, "deletedMessageCount": deletedMessageCount})
	return nil
}

func (h *Handler) deletedMessageCountAfterSeq(ctx context.Context, narratorID string, seq int) (int, error) {
	var count int
	if err := h.Service.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ? AND seq > ?`, narratorID, seq).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (h *Handler) permissionFilePreview(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	toolUseID := strings.TrimSpace(r.URL.Query().Get("toolUseId"))
	if toolUseID == "" {
		return httpx.BadRequest("toolUseId is required")
	}
	var ownerNarratorID, toolName string
	var inputRaw sql.NullString
	err := h.Service.DB.QueryRowContext(r.Context(), `SELECT tc.narrator_id, tc.tool_name, tc.input_json FROM narrator_tool_calls tc LEFT JOIN narrators n ON n.id = tc.narrator_id WHERE (tc.narrator_id = ? OR n.parent_narrator_id = ?) AND tc.tool_use_id = ? ORDER BY tc.created_at DESC LIMIT 1`, narratorID, narratorID, toolUseID).Scan(&ownerNarratorID, &toolName, &inputRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("Tool call")
		}
		return err
	}
	input := map[string]any{}
	if decoded, ok := decodeAny(inputRaw).(map[string]any); ok {
		input = decoded
	}
	ownerNarrator, err := h.Service.Get(r.Context(), ownerNarratorID)
	if err != nil {
		return err
	}
	filePath := permissionPreviewFilePath(input)
	cwd := narratorWorkingDir(r.Context(), h.Service.DB, ownerNarrator)
	if filePath != "" && !filepath.IsAbs(filePath) {
		filePath = resolveToolFilePath(cwd, filePath)
	}
	current, err := rebuildFileState(r.Context(), h.Service.DB, ownerNarratorID, filePath, cwd, nil)
	if err != nil {
		return err
	}
	preview := applyFileToolCall(current, orderedFileToolCall{ToolName: toolName, Input: input, RawFilePath: filePath, ResolvedFilePath: resolveSnapshotFilePath(cwd, filePath)})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"filePath": filePath, "currentContent": current, "previewContent": preview, "toolName": toolName, "inputJson": input})
	return nil
}

func (h *Handler) snapshotPreviewFiles(ctx context.Context, narratorID string) ([]map[string]any, error) {
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT id, file_path, created_at FROM narrator_file_snapshots WHERE narrator_id = ? ORDER BY created_at DESC`, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idValue, filePath, createdAt string
		if err := rows.Scan(&idValue, &filePath, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": idValue, "snapshotId": idValue, "filePath": filePath, "createdAt": createdAt})
	}
	return items, rows.Err()
}

func permissionPreviewFilePath(input map[string]any) string {
	for _, key := range []string{"filePath", "file_path", "path"} {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func permissionPreviewContent(toolName string, current any, input map[string]any) any {
	name := strings.ToLower(strings.TrimSpace(toolName))
	switch name {
	case "write":
		if content, ok := input["content"].(string); ok {
			return content
		}
	case "edit":
		currentText, _ := current.(string)
		oldString, _ := input["old_string"].(string)
		newString, _ := input["new_string"].(string)
		if oldString != "" && strings.Contains(currentText, oldString) {
			return strings.Replace(currentText, oldString, newString, 1)
		}
	}
	return nil
}

func readSmallTextFile(path string) any {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > 1024*1024 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return string(data)
}

func applySnapshotContent(filePath string, content *string) error {
	if strings.TrimSpace(filePath) == "" {
		return httpx.BadRequest("filePath is required")
	}
	if content == nil {
		if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filePath, []byte(*content), 0o644)
}

func (h *Handler) forkNarrator(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ForkMessageUUID string `json:"forkMessageUuid"`
		Title           string `json:"title"`
		InheritMode     string `json:"inheritMode"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	narrator, err := h.Service.ForkNarrator(r.Context(), chi.URLParam(r, "id"), body.ForkMessageUUID, body.Title, body.InheritMode)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, narrator)
	return nil
}

func (h *Handler) forkMessages(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		MessageIDs []string `json:"messageIds"`
		Title      string   `json:"title"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	narrator, err := h.Service.ForkFromMessages(r.Context(), chi.URLParam(r, "id"), body.MessageIDs, body.Title)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, narrator)
	return nil
}

func (h *Handler) startAskInPassing(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	var body struct {
		SourceMessageID   string `json:"sourceMessageId"`
		SourceMessageUUID string `json:"sourceMessageUuid"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	sourceMessageID := strings.TrimSpace(body.SourceMessageID)
	if sourceMessageID == "" {
		return httpx.ValidationError("sourceMessageId is required")
	}
	block := map[string]any{
		"type":              "ask_in_passing",
		"status":            "pending",
		"sourceMessageId":   sourceMessageID,
		"sourceMessageUuid": nullableMapString(strings.TrimSpace(body.SourceMessageUUID)),
	}
	userID := "system"
	if claims := auth.UserFromContext(r.Context()); claims != nil {
		userID = claims.Subject
	}
	msg, err := h.Service.addMessageWithBlocks(r.Context(), narratorID, "system", askInPassingContentText(""), []map[string]any{block}, &userID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"messageId": msg.ID})
	return nil
}

func (h *Handler) askInPassing(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	parent, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	var body struct {
		Question         string `json:"question"`
		PendingMessageID string `json:"pendingMessageId"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	question := strings.TrimSpace(body.Question)
	if question == "" {
		return httpx.ValidationError("question is required")
	}
	if len(question) > 10000 {
		return httpx.ValidationError("question must be at most 10000 characters")
	}
	pendingMessageID := strings.TrimSpace(body.PendingMessageID)
	if pendingMessageID == "" {
		return httpx.ValidationError("pendingMessageId is required")
	}
	pendingBlock, err := h.pendingAskInPassingBlock(r.Context(), narratorID, pendingMessageID)
	if err != nil {
		return err
	}
	sourceMessageID := stringValue(pendingBlock["sourceMessageId"])
	sourceMessageUUID := stringValue(pendingBlock["sourceMessageUuid"])
	if sourceMessageID == "" && sourceMessageUUID == "" {
		return httpx.ValidationError("Pending ask-in-passing message is missing source reference")
	}
	title := firstLine(question)
	if len(title) > 80 {
		title = title[:80]
	}
	model := ""
	if parent.Model != nil {
		model = *parent.Model
	}
	child, err := h.Service.Create(r.Context(), CreateInput{
		Title:                             &title,
		Model:                             model,
		PermissionMode:                    "readOnly",
		Cwd:                               parent.Cwd,
		SystemPrompt:                      parent.SystemPrompt,
		ReasoningEffort:                   parent.ReasoningEffort,
		FastMode:                          &parent.FastMode,
		RelaxedPlan:                       &parent.RelaxedPlan,
		PlanReflectionAutoApproveOverride: parent.PlanReflectionAutoApproveOverride,
		DangerReflectionOverride:          parent.DangerReflectionOverride,
	})
	if err != nil {
		return err
	}
	traits := addStringTrait(child.Traits, "ask-in-passing")
	if _, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET permission_mode = 'readOnly', is_ask_in_passing = 1, traits = ?, updated_at = ? WHERE id = ?`, traits, db.NowISO(), child.ID); err != nil {
		return err
	}
	claims := auth.UserFromContext(r.Context())
	userID := "system"
	if claims != nil {
		userID = claims.Subject
	}
	if _, err := h.Service.AddUserMessage(r.Context(), child.ID, userID, question); err != nil {
		return err
	}
	if err := h.resolveAskInPassingPendingMessage(r.Context(), narratorID, pendingMessageID, sourceMessageID, sourceMessageUUID, question, child.ID); err != nil {
		return err
	}
	updated, err := h.Service.Get(r.Context(), child.ID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, updated)
	return nil
}

func (h *Handler) cancelAskInPassing(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "messageId")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	if _, err := h.pendingAskInPassingBlock(r.Context(), narratorID, messageID); err != nil {
		return err
	}
	if _, err := h.deleteMessageRef(r.Context(), narratorID, messageID); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func askInPassingContentText(question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return "[Ask in passing]"
	}
	return "[Ask in passing] " + question
}

func nullableMapString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.IndexAny(value, "\r\n"); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return value
}

func (h *Handler) pendingAskInPassingBlock(ctx context.Context, narratorID, messageID string) (map[string]any, error) {
	var role string
	var raw string
	if err := h.Service.DB.QueryRowContext(ctx, `SELECT m.role, m.content_json FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND r.message_id = ?`, narratorID, messageID).Scan(&role, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, httpx.ValidationError("Pending ask-in-passing message not found")
		}
		return nil, err
	}
	if role != "system" {
		return nil, httpx.ValidationError("Pending ask-in-passing message not found")
	}
	blocks := []map[string]any{}
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		return nil, err
	}
	for _, block := range blocks {
		if stringValue(block["type"]) == "ask_in_passing" && stringValue(block["status"]) == "pending" {
			return block, nil
		}
	}
	return nil, httpx.ValidationError("Pending ask-in-passing message not found")
}

func (h *Handler) resolveAskInPassingPendingMessage(ctx context.Context, narratorID, messageID, sourceMessageID, sourceMessageUUID, question, targetNarratorID string) error {
	resolvedBlock := map[string]any{
		"type":              "ask_in_passing",
		"status":            "resolved",
		"sourceMessageId":   nullableMapString(sourceMessageID),
		"sourceMessageUuid": nullableMapString(sourceMessageUUID),
		"question":          question,
		"targetNarratorId":  targetNarratorID,
		"createdAt":         db.NowISO(),
	}
	contentJSON, _ := json.Marshal([]map[string]any{resolvedBlock})
	tx, err := h.Service.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE narrator_messages SET content_json = ?, content_text = ? WHERE narrator_id = ? AND id = ?`, string(contentJSON), askInPassingContentText(question), narratorID, messageID)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return httpx.ValidationError("Pending ask-in-passing message not found")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE narrators SET message_version = message_version + 1, updated_at = ? WHERE id = ?`, db.NowISO(), narratorID); err != nil {
		return err
	}
	return tx.Commit()
}

func (h *Handler) promoteNarrator(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	narrator, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	traits := removeStringTrait(narrator.Traits, "ask-in-passing")
	_, err = h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET permission_mode = 'default', is_ask_in_passing = 0, traits = ?, updated_at = ? WHERE id = ?`, traits, db.NowISO(), narratorID)
	if err != nil {
		return err
	}
	updated, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	if h.Service.Hub != nil {
		h.Service.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "permission_mode_changed", "narratorId": narratorID, "permissionMode": "default"})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"type": "unlocked", "narrator": updated})
	return nil
}

func addStringTrait(current []string, trait string) string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range current {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	if !seen[trait] {
		out = append(out, trait)
	}
	data, _ := json.Marshal(out)
	return string(data)
}

func removeStringTrait(current []string, trait string) string {
	out := []string{}
	for _, item := range current {
		if item != trait {
			out = append(out, item)
		}
	}
	data, _ := json.Marshal(out)
	return string(data)
}

func (h *Handler) compactSummary(w http.ResponseWriter, r *http.Request) error {
	messageID := chi.URLParam(r, "messageId")
	var contentText sql.NullString
	var contentJSON sql.NullString
	err := h.Service.DB.QueryRowContext(r.Context(), `SELECT m.content_text, m.content_json FROM narrator_messages m JOIN narrator_message_refs r ON r.message_id = m.id WHERE m.id = ? AND r.narrator_id = ?`, messageID, chi.URLParam(r, "id")).Scan(&contentText, &contentJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("Compact summary")
		}
		return err
	}
	summary := compactSummaryFromContentJSON(contentJSON)
	if summary == "" {
		summary = sqlNullString(contentText)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"summary": summary})
	return nil
}

func removeSubstatus(items []string, value string) string {
	filtered := []string{}
	for _, item := range items {
		if item != value {
			filtered = append(filtered, item)
		}
	}
	data, _ := json.Marshal(filtered)
	return string(data)
}

func sqlNullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func compactSummaryFromContentJSON(raw sql.NullString) string {
	if !raw.Valid || raw.String == "" {
		return ""
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw.String), &blocks); err != nil {
		return ""
	}
	for _, block := range blocks {
		if summary, ok := block["summary"].(string); ok && strings.TrimSpace(summary) != "" {
			return summary
		}
		if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func persistedDangerReflectionSuggestion(raw sql.NullString) (string, string, bool) {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return "", "", false
	}
	var suggestions []any
	if err := json.Unmarshal([]byte(raw.String), &suggestions); err != nil {
		return "", "", false
	}
	for _, suggestion := range suggestions {
		record, ok := suggestion.(map[string]any)
		if !ok || stringValue(record["type"]) != "danger_reflection" {
			continue
		}
		status := stringValue(record["status"])
		if status == "" {
			status = "running"
		}
		return status, stringValue(record["reason"]), true
	}
	return "", "", false
}

func dangerReflectionDecisionFromStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "confirmed", "allow":
		return "allow"
	case "aborted":
		return "aborted"
	default:
		return "deny"
	}
}

func abortPersistedDangerReflectionSuggestions(raw sql.NullString, reason, resolvedAt, requestID string) (string, bool) {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return "", false
	}
	var suggestions []any
	if err := json.Unmarshal([]byte(raw.String), &suggestions); err != nil {
		return "", false
	}
	changed := false
	for index, suggestion := range suggestions {
		record, ok := suggestion.(map[string]any)
		if !ok || stringValue(record["type"]) != "danger_reflection" {
			continue
		}
		status := stringValue(record["status"])
		if status == "" {
			status = "running"
		}
		if status != "running" && status != "awaiting_user" {
			continue
		}
		if stringValue(record["requestId"]) == "" {
			record["requestId"] = requestID
		}
		record["status"] = "aborted"
		record["reason"] = reason
		record["resolvedAt"] = resolvedAt
		suggestions[index] = record
		changed = true
	}
	if !changed {
		return "", false
	}
	data, err := json.Marshal(suggestions)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (h *Handler) stopPersistedDangerReflection(ctx context.Context, requestID, reason string) (bool, error) {
	var narratorID, toolUseID, status, parentNarratorID sql.NullString
	var suggestionsRaw sql.NullString
	err := h.Service.DB.QueryRowContext(ctx, `SELECT tc.narrator_id, tc.tool_use_id, tc.status, tc.permission_suggestions, n.parent_narrator_id FROM narrator_tool_calls tc LEFT JOIN narrators n ON n.id = tc.narrator_id WHERE tc.id = ? OR tc.tool_use_id = ? ORDER BY tc.created_at DESC LIMIT 1`, requestID, requestID).Scan(&narratorID, &toolUseID, &status, &suggestionsRaw, &parentNarratorID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	existingStatus, existingReason, ok := persistedDangerReflectionSuggestion(suggestionsRaw)
	if !ok {
		return false, nil
	}
	message := strings.TrimSpace(reason)
	if message == "" {
		message = "Danger reflection was interrupted before manual takeover; no live reflection loop remains"
	}
	now := db.NowISO()
	abortedSuggestions, changed := abortPersistedDangerReflectionSuggestions(suggestionsRaw, message, now, requestID)
	if changed {
		if strings.TrimSpace(status.String) == "pending" {
			_, err = h.Service.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = 'fail', error_message = ?, permission_decided_by = 'aborted', permission_decided_at = ?, permission_decision_reason = ?, permission_suggestions = ? WHERE id = ? OR tool_use_id = ?`, message, now, message, abortedSuggestions, requestID, requestID)
		} else {
			_, err = h.Service.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET permission_decision_reason = ?, permission_suggestions = ? WHERE id = ? OR tool_use_id = ?`, message, abortedSuggestions, requestID, requestID)
		}
		if err != nil {
			return false, err
		}
		h.broadcastDangerReflectionResolved(sqlNullString(narratorID), sqlNullString(parentNarratorID), requestID, sqlNullString(toolUseID), "aborted", message)
		return true, nil
	}
	resolvedReason := strings.TrimSpace(reason)
	if resolvedReason == "" {
		resolvedReason = existingReason
	}
	h.broadcastDangerReflectionResolved(sqlNullString(narratorID), sqlNullString(parentNarratorID), requestID, sqlNullString(toolUseID), dangerReflectionDecisionFromStatus(existingStatus), resolvedReason)
	return true, nil
}

func (h *Handler) broadcastDangerReflectionResolved(narratorID, parentNarratorID, requestID, toolUseID, decision, reason string) {
	if h.Service == nil || h.Service.Hub == nil || strings.TrimSpace(narratorID) == "" {
		return
	}
	targets := []string{narratorID}
	if strings.TrimSpace(parentNarratorID) != "" && parentNarratorID != narratorID {
		targets = append(targets, parentNarratorID)
	}
	for _, targetID := range targets {
		h.Service.Hub.BroadcastNarrator(targetID, map[string]any{"type": "danger_reflection_resolved", "narratorId": targetID, "requestId": requestID, "toolUseId": toolUseID, "decision": decision, "reason": reason})
	}
}

func (h *Handler) stopDangerReflection(w http.ResponseWriter, r *http.Request) error {
	requestID := chi.URLParam(r, "requestId")
	handled, err := h.stopPersistedDangerReflection(r.Context(), requestID, "")
	if err != nil {
		return err
	}
	if handled {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
		return nil
	}
	return h.decidePermission("cancelled")(w, r)
}

func (h *Handler) decidePermission(status string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		requestID := chi.URLParam(r, "requestId")
		claims := auth.UserFromContext(r.Context())
		decider := "system"
		if claims != nil {
			decider = claims.Subject
		}
		var body struct {
			Reason       string         `json:"reason"`
			Message      string         `json:"message"`
			Answers      map[string]any `json:"answers"`
			UpdatedPlan  string         `json:"updatedPlan"`
			CompactAfter bool           `json:"compactAfter"`
		}
		if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
			return err
		}
		var updatedInput any
		var err error
		if status == "approved" {
			updatedInput, err = h.mergePermissionInputUpdates(r.Context(), requestID, body.Answers, body.UpdatedPlan, body.CompactAfter)
			if err != nil {
				return err
			}
		}
		dbStatus := permissionDecisionDBStatus(status)
		_, err = h.Service.DB.ExecContext(r.Context(), `UPDATE narrator_tool_calls SET status = ?, permission_decided_by = ?, permission_decided_at = ?, permission_decision_reason = ?, permission_deny_message = ? WHERE id = ? OR tool_use_id = ?`, dbStatus, decider, db.NowISO(), nullableString(body.Reason), nullableString(body.Message), requestID, requestID)
		if err != nil {
			return err
		}
		if h.Service.Hub != nil {
			narratorID, ownerNarratorID, toolUseID, err := h.permissionTargetDetails(r.Context(), requestID)
			if err != nil {
				return err
			}
			if narratorID != "" {
				payload := map[string]any{"type": "permission_resolved", "narratorId": narratorID, "requestId": requestID, "toolUseId": toolUseID, "decision": map[bool]string{true: "allow", false: "deny"}[status == "approved"]}
				if ownerNarratorID != "" && ownerNarratorID != narratorID {
					payload["subagentNarratorId"] = ownerNarratorID
				}
				if updatedInput != nil {
					payload["updatedInput"] = updatedInput
				}
				h.Service.Hub.BroadcastNarrator(narratorID, payload)
			}
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
		return nil
	}
}

func (h *Handler) mergePermissionInputUpdates(ctx context.Context, requestID string, answers map[string]any, updatedPlan string, compactAfter bool) (any, error) {
	updatedPlan = strings.TrimSpace(updatedPlan)
	if len(answers) == 0 && updatedPlan == "" && !compactAfter {
		return nil, nil
	}
	var inputRaw, toolName sql.NullString
	err := h.Service.DB.QueryRowContext(ctx, `SELECT input_json, tool_name FROM narrator_tool_calls WHERE id = ? OR tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, requestID, requestID).Scan(&inputRaw, &toolName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	input := map[string]any{}
	if inputRaw.Valid && strings.TrimSpace(inputRaw.String) != "" {
		if err := json.Unmarshal([]byte(inputRaw.String), &input); err != nil {
			input = map[string]any{"raw": inputRaw.String}
		}
	}
	if len(answers) > 0 {
		input["answers"] = answers
	}
	if strings.EqualFold(sqlNullString(toolName), "ExitPlanMode") {
		if updatedPlan != "" {
			originalPlan := strings.TrimSpace(stringFromAny(input["plan"]))
			input["plan"] = updatedPlan
			input["planSource"] = "updatedPlan"
			if originalPlan != "" && originalPlan != updatedPlan {
				if diff := computePlanLineDiff(originalPlan, updatedPlan); diff != "" {
					input["planDiff"] = diff
				}
			}
		}
		if compactAfter {
			input["compactAfter"] = true
		}
	}
	data, _ := json.Marshal(input)
	_, err = h.Service.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET input_json = ? WHERE id = ? OR tool_use_id = ?`, string(data), requestID, requestID)
	if err != nil {
		return nil, err
	}
	return input, nil
}

func computePlanLineDiff(oldText, newText string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")
	if len(oldLines) == 1 && oldLines[0] == "" {
		oldLines = []string{}
	}
	if len(newLines) == 1 && newLines[0] == "" {
		newLines = []string{}
	}
	m, n := len(oldLines), len(newLines)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	type diffLine struct {
		kind string
		text string
	}
	diffLines := []diffLine{}
	for i, j := 0, 0; i < m || j < n; {
		if i < m && j < n && oldLines[i] == newLines[j] {
			diffLines = append(diffLines, diffLine{kind: "keep", text: oldLines[i]})
			i++
			j++
		} else if j < n && (i >= m || dp[i][j+1] >= dp[i+1][j]) {
			diffLines = append(diffLines, diffLine{kind: "add", text: newLines[j]})
			j++
		} else {
			diffLines = append(diffLines, diffLine{kind: "del", text: oldLines[i]})
			i++
		}
	}
	changed := []int{}
	for i, line := range diffLines {
		if line.kind != "keep" {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return ""
	}
	const contextLines = 2
	hunks := []string{}
	hunkStart := planDiffMaxInt(0, changed[0]-contextLines)
	hunkEnd := planDiffMinInt(len(diffLines)-1, changed[0]+contextLines)
	emit := func(start, end int) string {
		lines := []string{}
		for i := start; i <= end; i++ {
			line := diffLines[i]
			switch line.kind {
			case "add":
				lines = append(lines, "+ "+line.text)
			case "del":
				lines = append(lines, "- "+line.text)
			default:
				lines = append(lines, "  "+line.text)
			}
		}
		return strings.Join(lines, "\n")
	}
	for _, idx := range changed[1:] {
		nextStart := planDiffMaxInt(0, idx-contextLines)
		nextEnd := planDiffMinInt(len(diffLines)-1, idx+contextLines)
		if nextStart <= hunkEnd+1 {
			hunkEnd = nextEnd
			continue
		}
		hunks = append(hunks, emit(hunkStart, hunkEnd))
		hunkStart, hunkEnd = nextStart, nextEnd
	}
	hunks = append(hunks, emit(hunkStart, hunkEnd))
	return strings.Join(hunks, "\n...\n")
}

func planDiffMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func planDiffMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (h *Handler) permissionTarget(ctx context.Context, requestID string) (string, string, error) {
	targetID, _, toolUseID, err := h.permissionTargetDetails(ctx, requestID)
	return targetID, toolUseID, err
}

func (h *Handler) permissionTargetDetails(ctx context.Context, requestID string) (string, string, string, error) {
	var targetID, ownerID, toolUseID sql.NullString
	err := h.Service.DB.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(n.parent_narrator_id, ''), tc.narrator_id), tc.narrator_id, tc.tool_use_id FROM narrator_tool_calls tc LEFT JOIN narrators n ON n.id = tc.narrator_id WHERE tc.id = ? OR tc.tool_use_id = ? ORDER BY tc.created_at DESC LIMIT 1`, requestID, requestID).Scan(&targetID, &ownerID, &toolUseID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", nil
		}
		return "", "", "", fmt.Errorf("query permission target: %w", err)
	}
	return sqlNullString(targetID), sqlNullString(ownerID), sqlNullString(toolUseID), nil
}

func (h *Handler) interrupt(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	interrupted := h.Service.CancelRun(narratorID)
	current, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	if !interrupted && (current.Status == "working" || current.Status == "waiting") {
		interrupted = true
	}
	if interrupted {
		substatus := `["interrupted"]`
		if (current.Type == "subagent" || current.Variant == "subagent") && !current.IsBackground {
			substatus = `["manual_override"]`
		}
		if _, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET status = 'idle', substatus = ?, updated_at = ? WHERE id = ?`, substatus, db.NowISO(), narratorID); err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"interrupted": interrupted})
	return nil
}

func (h *Handler) retry(w http.ResponseWriter, r *http.Request) error {
	msg, deletedIDs, err := h.Service.RetryLastUserMessage(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg, "deletedMessageIds": deletedIDs})
	return nil
}

func (h *Handler) continueRun(w http.ResponseWriter, r *http.Request) error {
	claims := auth.UserFromContext(r.Context())
	userID := "system"
	if claims != nil {
		userID = claims.Subject
	}
	msg, err := h.Service.AddUserMessage(r.Context(), chi.URLParam(r, "id"), userID, "Continue.")
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
	return nil
}

func (h *Handler) compact(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	var body struct {
		Summary         string `json:"summary"`
		BeforeMessageID string `json:"beforeMessageId"`
	}
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	h.broadcastCompactEvent(narratorID, map[string]any{"type": "compacting", "mode": "blocking"})
	summary := strings.TrimSpace(body.Summary)
	refs, err := h.segmentCompactRefs(r.Context(), narratorID, nil, "", "")
	if err != nil {
		h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "mode": "blocking"})
		return err
	}
	var insertSeq *int
	if body.BeforeMessageID != "" {
		var seq int
		if err := h.Service.DB.QueryRowContext(r.Context(), `SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, body.BeforeMessageID).Scan(&seq); err != nil {
			h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "mode": "blocking"})
			if errors.Is(err, sql.ErrNoRows) {
				return httpx.NotFound("Message")
			}
			return err
		}
		insertSeq = &seq
	}
	if summary == "" {
		summary, err = h.buildSummaryFromRefs(r.Context(), refs, "Context compact")
		if err != nil {
			h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "mode": "blocking"})
			return err
		}
	}
	msg, err := h.createCompactMessage(r.Context(), narratorID, summary, "compact", insertSeq)
	if err != nil {
		h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "mode": "blocking"})
		return err
	}
	if _, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET context_summary = ?, api_conversation_id = NULL, prune_boundary_message_id = NULL, pruned_percent = NULL, updated_at = ? WHERE id = ?`, summary, db.NowISO(), narratorID); err != nil {
		h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "messageId": msg.ID, "mode": "blocking"})
		return err
	}
	h.broadcastCompactEvent(narratorID, map[string]any{"type": "message", "message": msg})
	h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_done", "mode": "blocking"})
	summarySource := "caller"
	summaryQuality := "caller"
	if body.Summary == "" {
		summarySource = "deterministic_fallback"
		summaryQuality = "deterministic_preview"
	}
	payload := map[string]any{
		"ok":                    true,
		"message":               msg,
		"summary":               summary,
		"fallbackSummary":       body.Summary == "",
		"summarySource":         summarySource,
		"summaryQuality":        summaryQuality,
		"modelSummaryGenerated": false,
	}
	if body.Summary == "" {
		payload["fallbackReason"] = compactFallbackSummaryReason
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) segmentCompactSummary(w http.ResponseWriter, r *http.Request) error {
	msg, err := h.Service.messageByID(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "messageId"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"messageId": msg.ID, "summary": nullablePtr(msg.ContentText), "message": msg})
	return nil
}

func (h *Handler) segmentCompactMessages(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT message_id, seq FROM narrator_message_refs WHERE narrator_id = ? AND segment_compact_id = ? ORDER BY seq ASC`, chi.URLParam(r, "id"), chi.URLParam(r, "messageId"))
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var messageID string
		var seq int
		if err := rows.Scan(&messageID, &seq); err != nil {
			return err
		}
		items = append(items, map[string]any{"messageId": messageID, "seq": seq})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": items})
	return rows.Err()
}

func (h *Handler) updateSegmentCompactSummary(w http.ResponseWriter, r *http.Request) error {
	return h.updateCompactSummary(w, r)
}

func (h *Handler) clearContext(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	content := []map[string]any{{"type": "compact", "status": "compacted", "summary": ""}}
	msg, err := h.Service.addMessageWithBlocksAtSeq(r.Context(), narratorID, "system", "[Context cleared]", content, nil, nil, true)
	if err != nil {
		h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "mode": "blocking"})
		return err
	}
	_, err = h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET context_summary = NULL, api_conversation_id = NULL, prune_boundary_message_id = NULL, pruned_percent = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), narratorID)
	if err != nil {
		h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_failed", "messageId": msg.ID, "mode": "blocking"})
		return err
	}
	h.broadcastCompactEvent(narratorID, map[string]any{"type": "message", "message": msg})
	h.broadcastCompactEvent(narratorID, map[string]any{"type": "compact_done", "mode": "blocking"})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
	return nil
}

func (h *Handler) plan(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	traits, err := h.narratorTraits(r.Context(), narratorID)
	if err != nil {
		return err
	}
	var body map[string]any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	content := firstString(stringValue(body["plan"]), stringValue(body["content"]), stringValue(body["text"]), stringValue(body["prompt"]))
	state := parsePlanTrait(traits)
	if strings.TrimSpace(content) == "" && state != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"plan": stringValue(state["content"]), "state": state})
		return nil
	}
	if strings.TrimSpace(content) == "" {
		content, err = h.buildCurrentPlan(r.Context(), narratorID)
		if err != nil {
			return err
		}
	}
	state = map[string]any{"content": content, "updatedAt": db.NowISO(), "source": "api"}
	if err := h.saveNarratorTraits(r.Context(), narratorID, upsertPlanTrait(traits, state)); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"plan": content, "state": state})
	return nil
}

func (h *Handler) setPlanMode(enabled bool) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		payload, err := h.Service.setNarratorPlanMode(r.Context(), chi.URLParam(r, "id"), enabled)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, payload)
		return nil
	}
}

func (h *Handler) deleteNarrator(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if err := h.Service.RemoveNarrator(r.Context(), narratorID); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "id": narratorID})
	return nil
}

func (h *Handler) emptyList(w http.ResponseWriter, _ *http.Request) error {
	httpx.WriteJSON(w, http.StatusOK, []any{})
	return nil
}

func (h *Handler) notImplemented(feature string) httpx.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error { return httpx.NotImplemented(feature) }
}

func (h *Handler) postMessage(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	claims := auth.UserFromContext(r.Context())
	userID := "system"
	if claims != nil {
		userID = claims.Subject
	}
	narrator, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	if narrator.Status == "archived" {
		if _, err := h.Service.UpdateTextField(r.Context(), narratorID, "status", "idle"); err != nil {
			return err
		}
	}
	payload, err := h.parseMessagePayload(r, narratorID)
	if err != nil {
		return err
	}
	finalMessage := payload.ContentText
	commandText := ""
	slashInput := payload.RawText
	if strings.TrimSpace(slashInput) == "" {
		slashInput = payload.ContentText
	}
	cmd, err := h.resolveSlashCommand(r.Context(), slashInput, narratorID, userID)
	if err != nil {
		return err
	}
	if cmd.Resolved && cmd.LoadSkill != "" {
		skillPrompt, found, err := h.Service.skillCommandPrompt(r.Context(), narratorID, cmd.LoadSkill, cmd.SkillInput)
		if err != nil {
			return err
		}
		if !found {
			cleanupUploadedTextFileBlocks(payload.ContentBlocks)
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"skillName": cmd.LoadSkill, "loaded": false})
			return nil
		}
		finalMessage = skillPrompt + payload.AttachmentHint
		commandText = cmd.RawCommand
	}
	if cmd.Resolved && cmd.BashCommand != "" && cmd.ExpandedPrompt == "" {
		defer cleanupUploadedTextFileBlocks(payload.ContentBlocks)
		result, err := h.Service.HandleBashCommand(r.Context(), narratorID, cmd.BashCommand, cmd.RawCommand, userID)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusCreated, result)
		return nil
	}
	if cmd.Resolved && cmd.GoalCommand {
		defer cleanupUploadedTextFileBlocks(payload.ContentBlocks)
		result, err := h.handleGoalSlashCommand(r.Context(), narratorID, cmd, userID)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, result)
		return nil
	}
	if cmd.Resolved && cmd.ExpandedPrompt != "" {
		finalMessage = cmd.ExpandedPrompt + payload.AttachmentHint
		commandText = cmd.RawCommand
		if cmd.BashCommand != "" {
			if narrator.Status == "working" || narrator.Status == "waiting" {
				return httpx.BadRequest("Commands with Run Bash first cannot be queued while the narrator is working")
			}
			if _, err := h.Service.HandleBashCommand(r.Context(), narratorID, cmd.BashCommand, "/bash "+cmd.BashCommand, userID); err != nil {
				return err
			}
		}
		if model := strings.TrimSpace(stringValue(cmd.ModelOverride["model"])); model != "" {
			if strings.TrimSpace(stringValue(cmd.ModelOverride["mode"])) == "temporary" {
				restore := "__default__"
				if narrator.Model != nil && strings.TrimSpace(*narrator.Model) != "" {
					restore = *narrator.Model
				}
				if err := h.Service.SetTemporaryModelRestore(r.Context(), narratorID, restore); err != nil {
					return err
				}
			}
			if _, err := h.Service.UpdateTextField(r.Context(), narratorID, "model", model); err != nil {
				return err
			}
		}
	}
	contentBlocks := payload.ContentBlocks
	if finalMessage != payload.ContentText {
		contentBlocks = replaceTextBlock(contentBlocks, strings.TrimSuffix(finalMessage, payload.AttachmentHint))
	}
	if narratorAcceptsBufferedMessages(narrator.Status) || h.Service.isRunActive(narratorID) {
		bufferText := strings.TrimSuffix(finalMessage, payload.AttachmentHint)
		buffered, err := h.Service.BufferUserMessage(r.Context(), narratorID, userID, bufferText, contentBlocks, payload.Priority, commandText)
		cleanupUploadedTextFileBlocks(contentBlocks)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "no such table") {
				reason := "narrator is busy and buffered message storage is unavailable"
				httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": false, "queued": false, "fallback": true, "status": "busy", "reason": reason, "error": reason, "message": reason})
				return nil
			}
			return err
		}
		httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true, "queued": true, "buffered": true, "fallback": false, "bufferedAt": buffered.BufferedAt, "id": buffered.ID})
		return nil
	}
	msg, err := h.Service.AddUserMessageWithContentAndCommand(r.Context(), narratorID, userID, finalMessage, contentBlocks, commandText)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, msg)
	return nil
}

func (h *Handler) handleGoalSlashCommand(ctx context.Context, narratorID string, cmd slashCommandResult, userID string) (map[string]any, error) {
	_, _ = h.Service.addMessageWithBlocksWithCommand(ctx, narratorID, "user", cmd.RawCommand, []map[string]any{{"type": "text", "text": cmd.RawCommand}}, &userID, cmd.RawCommand)
	switch cmd.GoalAction {
	case "list":
		goals, err := h.Service.loadGoals(ctx, narratorID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"goalCommand": true, "action": "list", "goals": goals}, nil
	case "add":
		if strings.TrimSpace(cmd.GoalObjective) == "" {
			return nil, httpx.BadRequest("Goal objective is required")
		}
		payload, err := h.Service.addGoal(ctx, narratorID, cmd.GoalObjective, userID)
		if err != nil {
			return nil, err
		}
		payload["goalCommand"] = true
		payload["action"] = "add"
		return payload, nil
	case "resume":
		payload, err := h.Service.resumeGoal(ctx, narratorID)
		if err != nil {
			return nil, err
		}
		payload["goalCommand"] = true
		payload["action"] = "resume"
		return payload, nil
	case "complete":
		payload, err := h.Service.completeActiveGoalBasic(ctx, narratorID)
		if err != nil {
			return nil, err
		}
		payload["goalCommand"] = true
		payload["action"] = "complete"
		return payload, nil
	default:
		return nil, httpx.BadRequest("unsupported goal command")
	}
}

func replaceTextBlock(blocks []map[string]any, text string) []map[string]any {
	out := make([]map[string]any, len(blocks))
	copy(out, blocks)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i]["type"] == "text" {
			clone := map[string]any{}
			for key, value := range out[i] {
				clone[key] = value
			}
			clone["text"] = text
			out[i] = clone
			return out
		}
	}
	return append(out, map[string]any{"type": "text", "text": text})
}

func (h *Handler) updateTitle(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	title := firstString(stringValue(body["title"]), stringValue(body["name"]))
	narratorID := chi.URLParam(r, "id")
	n, err := h.Service.UpdateTextField(r.Context(), narratorID, "title", nullableString(title))
	if err != nil {
		return err
	}
	if h.Service.Hub != nil {
		h.Service.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "title_updated", "narratorId": narratorID, "title": title})
	}
	httpx.WriteJSON(w, http.StatusOK, n)
	return nil
}

func (h *Handler) updateModel(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	model := firstString(stringValue(body["model"]), stringValue(body["value"]))
	n, err := h.Service.UpdateTextField(r.Context(), chi.URLParam(r, "id"), "model", nullableString(model))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, n)
	return nil
}

func (h *Handler) updatePermissionMode(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	mode := firstString(stringValue(body["permissionMode"]), stringValue(body["mode"]), stringValue(body["value"]))
	normalized, ok := normalizePermissionModeStrict(mode)
	if !ok {
		return httpx.BadRequest("permissionMode must be one of default, acceptEdits, bypassPermissions, readOnly, or dontAsk")
	}
	narratorID := chi.URLParam(r, "id")
	narrator, err := h.Service.Get(r.Context(), narratorID)
	if err != nil {
		return err
	}
	if narrator.IsAskInPassing || containsString(narrator.Traits, "ask-in-passing") {
		return httpx.ValidationError("Cannot change permission mode of an ask-in-passing narrator. Use promote to unlock.")
	}
	n, err := h.Service.UpdateTextField(r.Context(), narratorID, "permission_mode", normalized)
	if err != nil {
		return err
	}
	if normalized == "bypassPermissions" {
		if _, err := h.Service.reprocessPendingPermissionsForBypass(r.Context(), narratorID); err != nil {
			return err
		}
	}
	if h.Service.Hub != nil {
		h.Service.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "permission_mode_changed", "narratorId": narratorID, "permissionMode": normalized})
	}
	httpx.WriteJSON(w, http.StatusOK, n)
	return nil
}

func (h *Handler) archive(w http.ResponseWriter, r *http.Request) error {
	n, err := h.Service.UpdateTextField(r.Context(), chi.URLParam(r, "id"), "status", "archived")
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, n)
	return nil
}

func (h *Handler) unarchive(w http.ResponseWriter, r *http.Request) error {
	n, err := h.Service.UpdateTextField(r.Context(), chi.URLParam(r, "id"), "status", "idle")
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, n)
	return nil
}

func (h *Handler) markRead(w http.ResponseWriter, r *http.Request) error {
	_, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET updated_at = ? WHERE id = ?`, db.NowISO(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) detachSubagent(w http.ResponseWriter, r *http.Request) error {
	payload, err := h.Service.DetachSubagent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	status := http.StatusOK
	if payload["detached"] != true {
		status = http.StatusBadRequest
	}
	httpx.WriteJSON(w, status, payload)
	return nil
}

func (h *Handler) attachSubagent(w http.ResponseWriter, r *http.Request) error {
	payload, err := h.Service.AttachSubagent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	status := http.StatusOK
	if payload["attached"] != true {
		status = http.StatusBadRequest
	}
	httpx.WriteJSON(w, status, payload)
	return nil
}

func (h *Handler) leave(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	_, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET substatus = '[]', updated_at = ? WHERE id = ? AND (substatus LIKE '%interrupted%' OR substatus LIKE '%manual_override%')`, db.NowISO(), narratorID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) cancelBackgroundTask(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	taskID := chi.URLParam(r, "taskId")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	now := db.NowISO()
	res, err := h.Service.DB.ExecContext(r.Context(), `UPDATE background_tasks SET status = 'cancelled', output = COALESCE(output, 'Cancelled'), output_bytes = CASE WHEN output IS NULL THEN 9 ELSE output_bytes END, completed_at = COALESCE(completed_at, ?), updated_at = ? WHERE parent_narrator_id = ? AND id = ? AND status NOT IN ('completed', 'failed', 'cancelled', 'timeout')`, now, now, narratorID, taskID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows > 0 {
		var taskType string
		var subagentID, toolUseID, output sql.NullString
		if err := h.Service.DB.QueryRowContext(r.Context(), `SELECT COALESCE(type, ''), subagent_narrator_id, tool_use_id, output FROM background_tasks WHERE parent_narrator_id = ? AND id = ?`, narratorID, taskID).Scan(&taskType, &subagentID, &toolUseID, &output); err != nil {
			return err
		}
		cancelID := taskID
		if subagentID.Valid && strings.TrimSpace(subagentID.String) != "" {
			cancelID = subagentID.String
		}
		if strings.TrimSpace(taskType) == "bash" {
			h.Service.cancelBackgroundBashTask(taskID)
		} else {
			h.Service.CancelRun(cancelID)
			if injectErr := h.Service.injectBackgroundSubagentResult(r.Context(), narratorID, taskID, sqlNullString(toolUseID), "cancelled", firstNonEmptyString(sqlNullString(output), "Cancelled")); injectErr != nil {
				return injectErr
			}
		}
		if _, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET status = CASE WHEN status = 'working' THEN 'idle' ELSE status END, substatus = CASE WHEN status = 'working' THEN '["interrupted"]' ELSE substatus END, background_status = 'cancelled', background_completed_at = COALESCE(background_completed_at, ?), updated_at = ? WHERE parent_narrator_id = ? AND is_background = 1 AND id = ?`, now, now, narratorID, cancelID); err != nil {
			return err
		}
		if h.Service.Hub != nil {
			h.Service.broadcastBackgroundTaskStatus(narratorID, taskID, "cancelled", nullStringPtr(output), sqlNullString(toolUseID))
		}
	}
	if rows == 0 {
		childResult, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET status = CASE WHEN status = 'working' THEN 'idle' ELSE status END, substatus = CASE WHEN status = 'working' THEN '["interrupted"]' ELSE substatus END, background_status = 'cancelled', background_completed_at = COALESCE(background_completed_at, ?), updated_at = ? WHERE parent_narrator_id = ? AND is_background = 1 AND id = ? AND COALESCE(background_status, status) NOT IN ('completed', 'failed', 'cancelled', 'timeout')`, now, now, narratorID, taskID)
		if err != nil {
			return err
		}
		rows, _ = childResult.RowsAffected()
		if rows > 0 {
			h.Service.CancelRun(taskID)
			if h.Service.Hub != nil {
				h.Service.broadcastBackgroundTaskStatus(narratorID, taskID, "cancelled", nil, "")
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": rows > 0, "status": map[bool]string{true: "cancelled", false: "not_found"}[rows > 0]})
	return nil
}

func (h *Handler) backgroundTasks(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	items, err := h.listBackgroundTasks(r.Context(), narratorID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) backgroundTaskOutput(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	payload, found, err := h.backgroundTaskOutputPayload(r.Context(), narratorID, chi.URLParam(r, "taskId"))
	if err != nil {
		return err
	}
	if !found {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"output": nil, "status": "not_found"})
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) updateConclusion(w http.ResponseWriter, r *http.Request) error {
	payload, err := h.Service.UpdateSubagentConclusion(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	status := http.StatusOK
	if payload["ok"] != true {
		status = http.StatusBadRequest
	}
	httpx.WriteJSON(w, status, payload)
	return nil
}

func (h *Handler) suggestAnswers(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	questions := parseAskQuestions(body["questions"])
	answers, err := h.Service.GenerateAskUserQuestionAnswers(r.Context(), chi.URLParam(r, "id"), questions, "suggest")
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"answers": answers})
	return nil
}

func (h *Handler) reflectQuestion(w http.ResponseWriter, r *http.Request) error {
	requestID := chi.URLParam(r, "requestId")
	narratorID, toolUseID, err := h.permissionTarget(r.Context(), requestID)
	if err != nil {
		return err
	}
	if narratorID == "" || toolUseID == "" {
		return httpx.NotFound("Permission request")
	}
	answers, err := h.Service.ReflectPendingAskUserQuestion(r.Context(), narratorID, toolUseID, false)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return httpx.NotFound("Permission request")
		}
		return httpx.BadRequest(err.Error())
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "answers": answers})
	return nil
}

func (h *Handler) rollbackMessage(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	blockIndex := anyInt(body["blockIndex"], -1)
	if blockIndex < 0 {
		blockIndex = anyInt(body["index"], -1)
	}
	if blockIndex < 0 {
		return httpx.BadRequest("blockIndex is required")
	}
	revert := true
	if _, ok := body["revert"]; ok {
		revert = anyBool(body["revert"])
	}
	payload, err := h.Service.RollbackToBlock(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "messageId"), blockIndex, revert)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) editAndRegenerate(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	content, ok := firstPresentString(body, "content", "newContent", "text", "message")
	if !ok {
		return httpx.BadRequest("content is required")
	}
	rollback := true
	if _, ok := body["rollback"]; ok {
		rollback = anyBool(body["rollback"])
	}
	regenerate := true
	if _, ok := body["regenerate"]; ok {
		regenerate = anyBool(body["regenerate"])
	} else if _, ok := body["rerun"]; ok {
		regenerate = anyBool(body["rerun"])
	}
	result, err := h.Service.EditAndRegenerate(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "messageId"), content, rollback, regenerate)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"message":           result.Message,
		"messageId":         result.MessageID,
		"privateMessageId":  result.PrivateMessageID,
		"deletedMessageIds": result.DeletedMessageIDs,
		"affectedFiles":     result.AffectedFiles,
		"rollback":          rollback,
		"regenerate":        regenerate,
	})
	return nil
}

func writeNarratorFeatureDisabled(w http.ResponseWriter, feature, reason string) {
	httpx.WriteFeatureDisabled(w, feature, reason)
}

func writeBrowserSessionsDisabled(w http.ResponseWriter) {
	writeNarratorFeatureDisabled(w, "narrator.browserSessions", "Browser session runtime storage is not available in this go_backend database")
}

func (h *Handler) deleteSegmentCompact(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "messageId")
	if err := h.ensureMessageBlockType(r.Context(), narratorID, messageID, "", "segment_compact", "Message is not a segment compact message"); err != nil {
		return err
	}
	tx, err := h.Service.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `UPDATE narrator_message_refs SET segment_compact_id = NULL WHERE narrator_id = ? AND segment_compact_id = ?`, narratorID, messageID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, messageID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM narrator_messages WHERE narrator_id = ? AND id = ?`, narratorID, messageID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE narrators SET api_conversation_id = NULL, message_version = message_version + 1, updated_at = ? WHERE id = ?`, db.NowISO(), narratorID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) getDraft(w http.ResponseWriter, r *http.Request) error {
	traits, err := h.narratorTraits(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, draftTraitResponse(traits))
	return nil
}

func (h *Handler) putDraft(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Text     string  `json:"text"`
		SourceID *string `json:"sourceId"`
	}
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	narratorID := chi.URLParam(r, "id")
	traits, err := h.narratorTraits(r.Context(), narratorID)
	if err != nil {
		return err
	}
	userID := ""
	if claims := auth.UserFromContext(r.Context()); claims != nil {
		userID = claims.Subject
	}
	now := time.Now().UTC().Format(time.RFC3339)
	updated := upsertDraftTrait(traits, map[string]any{"text": body.Text, "updatedAt": now, "updatedBy": userID, "sourceId": body.SourceID})
	if strings.TrimSpace(body.Text) == "" {
		updated = removeTraitPrefix(traits, draftTraitPrefix)
	}
	if err := h.saveNarratorTraits(r.Context(), narratorID, updated); err != nil {
		return err
	}
	response := draftTraitResponse(updated)
	if h.Service.Hub != nil {
		h.Service.Hub.BroadcastNarrator(narratorID, mergeMaps(map[string]any{"type": "draft_changed", "narratorId": narratorID}, response))
	}
	httpx.WriteJSON(w, http.StatusOK, mergeMaps(map[string]any{"ok": true, "traits": publicTraitList(updated)}, response))
	return nil
}

func (h *Handler) usageStats(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	includeSubagents := r.URL.Query().Get("includeSubagents") != "false"
	stats, err := h.loadUsageStats(r.Context(), narratorID, includeSubagents)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, stats)
	return nil
}

func (h *Handler) commands(w http.ResponseWriter, r *http.Request) error {
	commands := []map[string]any{{"name": "goal", "prompt": "/goal <objective>", "description": "Add or manage long-running goals", "source": "user"}}
	userID := ""
	if claims := auth.UserFromContext(r.Context()); claims != nil {
		userID = claims.Subject
	}
	userCommands, _ := h.userCommands(r.Context(), userID)
	narratorID := chi.URLParam(r, "id")
	projectCommands, _ := h.projectCommandsForNarrator(r.Context(), narratorID)
	commands = append(commands, mergeCommandLists(userCommands, projectCommands)...)
	skillRoot, _ := h.Service.skillRootForNarrator(r.Context(), narratorID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"commands": commands, "skills": simplecontent.SlashSkillSummaries(skillRoot), "tools": optionalToolMenuItems()})
	return nil
}

func (h *Handler) userCommands(ctx context.Context, userID string) ([]map[string]any, error) {
	if userID == "" {
		return []map[string]any{}, nil
	}
	var raw string
	err := h.Service.DB.QueryRowContext(ctx, `SELECT commands FROM user_preferences WHERE user_id = ?`, userID).Scan(&raw)
	if err == sql.ErrNoRows {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseCommandArray(raw, "user"), nil
}

func (h *Handler) projectCommandsForNarrator(ctx context.Context, narratorID string) ([]map[string]any, error) {
	var raw sql.NullString
	err := h.Service.DB.QueryRowContext(ctx, `SELECT p.chapter_settings FROM narrators n JOIN chapters c ON c.id = n.chapter_id JOIN projects p ON p.id = c.project_id WHERE n.id = ?`, narratorID).Scan(&raw)
	if err == sql.ErrNoRows || !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return []map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	settings := map[string]any{}
	if err := json.Unmarshal([]byte(raw.String), &settings); err != nil {
		return []map[string]any{}, nil
	}
	data, _ := json.Marshal(settings["commands"])
	return parseCommandArray(string(data), "project"), nil
}

func parseCommandArray(raw string, source string) []map[string]any {
	items := []map[string]any{}
	if strings.TrimSpace(raw) == "" || raw == "null" {
		return items
	}
	parsed := []map[string]any{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return items
	}
	for _, cmd := range parsed {
		if stringValue(cmd["name"]) == "" || stringValue(cmd["prompt"]) == "" {
			continue
		}
		cmd["source"] = source
		items = append(items, cmd)
	}
	return items
}

func mergeCommandLists(userCommands, projectCommands []map[string]any) []map[string]any {
	byName := map[string]map[string]any{}
	originalName := map[string]string{}
	order := []string{}
	for _, cmd := range userCommands {
		name := strings.ToLower(stringValue(cmd["name"]))
		if name == "" {
			continue
		}
		if _, exists := byName[name]; !exists {
			order = append(order, name)
		}
		byName[name] = cmd
		originalName[name] = name
	}
	for _, cmd := range projectCommands {
		name := strings.ToLower(stringValue(cmd["name"]))
		if name == "" {
			continue
		}
		if _, exists := byName[name]; !exists {
			order = append(order, name)
		}
		byName[name] = cmd
		originalName[name] = name
	}
	out := make([]map[string]any, 0, len(order))
	for _, name := range order {
		out = append(out, byName[originalName[name]])
	}
	return out
}

type slashCommandResult struct {
	Resolved       bool
	RawCommand     string
	ExpandedPrompt string
	BashCommand    string
	LoadSkill      string
	SkillInput     string
	GoalCommand    bool
	GoalAction     string
	GoalObjective  string
	ModelOverride  map[string]any
}

func parseSlashCommandInput(prompt string) (string, string, bool) {
	trimmed := strings.TrimSpace(prompt)
	if !strings.HasPrefix(trimmed, "/") {
		return "", "", false
	}
	body := strings.TrimPrefix(trimmed, "/")
	name, input, ok := strings.Cut(body, " ")
	if !ok {
		name = body
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", false
	}
	for _, r := range name {
		if !(r == '_' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return "", "", false
		}
	}
	return name, strings.TrimSpace(input), true
}

func (h *Handler) resolveSlashCommand(ctx context.Context, prompt, narratorID, userID string) (slashCommandResult, error) {
	name, input, ok := parseSlashCommandInput(prompt)
	if !ok {
		return slashCommandResult{}, nil
	}
	lower := strings.ToLower(name)
	switch lower {
	case "skill", "load-skill":
		parts := strings.Fields(input)
		if len(parts) == 0 {
			return slashCommandResult{}, nil
		}
		return slashCommandResult{Resolved: true, LoadSkill: parts[0], SkillInput: strings.TrimSpace(strings.TrimPrefix(input, parts[0])), RawCommand: prompt}, nil
	case "bash":
		if input == "" {
			return slashCommandResult{}, nil
		}
		return slashCommandResult{Resolved: true, BashCommand: input, RawCommand: prompt}, nil
	case "goal":
		return resolveGoalSlashCommand(prompt, input), nil
	}
	userCommands, err := h.userCommands(ctx, userID)
	if err != nil {
		return slashCommandResult{}, err
	}
	projectCommands, err := h.projectCommandsForNarrator(ctx, narratorID)
	if err != nil {
		return slashCommandResult{}, err
	}
	for _, cmd := range mergeCommandLists(userCommands, projectCommands) {
		if strings.ToLower(stringValue(cmd["name"])) != lower {
			continue
		}
		expanded := stringValue(cmd["prompt"])
		bashCommand := ""
		if boolValue(cmd["runBashFirst"]) {
			bashCommand = stringValue(cmd["bashCommand"])
		}
		values := map[string]string{"input": input}
		if params := commandParams(cmd["params"]); len(params) > 0 {
			parsed := parseCommandArgs(input, params)
			for k, v := range parsed {
				values[k] = v
			}
		}
		if strings.Contains(expanded, "{{") {
			expanded = replaceCommandPlaceholders(expanded, values)
		} else if input != "" && len(commandParams(cmd["params"])) == 0 {
			expanded = strings.TrimSpace(expanded) + "\n\n" + input
		}
		if bashCommand != "" {
			bashCommand = replaceCommandPlaceholders(bashCommand, values)
		}
		modelOverride, _ := cmd["modelOverride"].(map[string]any)
		return slashCommandResult{Resolved: true, ExpandedPrompt: strings.TrimSpace(expanded), BashCommand: strings.TrimSpace(bashCommand), RawCommand: prompt, ModelOverride: modelOverride}, nil
	}
	return slashCommandResult{}, nil
}

func resolveGoalSlashCommand(raw, input string) slashCommandResult {
	lower := strings.ToLower(strings.TrimSpace(input))
	action := "add"
	objective := strings.TrimSpace(input)
	switch lower {
	case "":
		action = "list"
		objective = ""
	case "resume":
		action = "resume"
		objective = ""
	case "done", "complete":
		action = "complete"
		objective = ""
	}
	return slashCommandResult{Resolved: true, GoalCommand: true, GoalAction: action, GoalObjective: objective, RawCommand: raw}
}

func commandParams(value any) []map[string]string {
	raw, _ := value.([]any)
	params := []map[string]string{}
	for _, item := range raw {
		m, _ := item.(map[string]any)
		name := strings.TrimSpace(stringValue(m["name"]))
		if name == "" {
			continue
		}
		params = append(params, map[string]string{"name": name, "defaultValue": stringValue(m["defaultValue"])})
	}
	return params
}

func parseCommandArgs(input string, params []map[string]string) map[string]string {
	tokens := strings.Fields(input)
	result := map[string]string{}
	for i, param := range params {
		name := param["name"]
		if i == len(params)-1 {
			value := strings.Join(tokens[minInt(i, len(tokens)):], " ")
			if value == "" {
				value = param["defaultValue"]
			}
			result[name] = value
		} else if i < len(tokens) {
			result[name] = tokens[i]
		} else {
			result[name] = param["defaultValue"]
		}
	}
	return result
}

func replaceCommandPlaceholders(template string, values map[string]string) string {
	result := template
	for key, value := range values {
		result = strings.ReplaceAll(result, "{{"+key+"}}", value)
	}
	return result
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func loadGlobalSkillSummaries() []map[string]any {
	home, err := os.UserHomeDir()
	if err != nil {
		return []map[string]any{}
	}
	root := filepath.Join(home, ".narrafork", "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		return []map[string]any{}
	}
	items := []map[string]any{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dir := filepath.Join(root, name)
		if _, err := os.Stat(filepath.Join(dir, ".disabled")); err == nil {
			continue
		}
		content, ok := readToolTextFile(filepath.Join(dir, "SKILL.md"))
		if !ok {
			continue
		}
		items = append(items, map[string]any{"name": name, "description": firstNonEmptyParagraph(content), "source": "global", "truncated": strings.Contains(content, "\n[truncated]")})
	}
	return items
}

func optionalToolMenuItems() []map[string]any {
	supportedOptional := map[string]bool{}
	for _, tool := range agentbasic.ToolMenuCapabilities() {
		if stringValue(tool["category"]) != agentbasic.ToolCategoryOptional || tool["supported"] != true {
			continue
		}
		supportedOptional[stringValue(tool["name"])] = true
	}
	items := []map[string]any{}
	for _, item := range []map[string]any{
		{"id": "terminal", "toolName": "Terminal", "descriptionEn": "Interactive terminal — read buffer, send input, list terminals", "descriptionZh": "交互式终端 — 读取缓冲区、发送输入、列出终端"},
		{"id": "share_file", "toolName": "ShareFile", "descriptionEn": "Share files/directories — generate temporary download links", "descriptionZh": "分享文件/目录 — 生成临时下载链接"},
		{"id": "recall", "toolName": "Recall", "descriptionEn": "Search and browse all narrator conversations", "descriptionZh": "搜索和浏览所有叙述者对话"},
		{"id": "browser", "toolName": "Browser", "descriptionEn": "Interactive browser — navigate, click, fill forms, take screenshots", "descriptionZh": "交互式浏览器 — 导航、点击、填写表单、截图"},
		{"id": "fork_narrator", "toolName": "ForkNarrator", "descriptionEn": "Fork the current narrator session into a new chapter", "descriptionZh": "将当前叙述者会话分叉到新章节"},
		{"id": "narrafork_admin", "toolName": "NarraForkAdmin", "descriptionEn": "Admin settings management — get and modify all NarraFork settings", "descriptionZh": "管理员设置管理 — 获取和修改所有 NarraFork 设置"},
	} {
		if supportedOptional[stringValue(item["toolName"])] {
			items = append(items, item)
		}
	}
	return items
}

func firstNonEmptyParagraph(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.Trim(strings.TrimSpace(line), "# -\t")
		if line != "" && line != "---" && !strings.Contains(line, ":") {
			return line
		}
	}
	return ""
}

func (h *Handler) toolCallDetail(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	var ownerNarratorID string
	var inputRaw, outputRaw sql.NullString
	row := h.Service.DB.QueryRowContext(r.Context(), `SELECT tc.narrator_id, tc.input_json, tc.output_json FROM narrator_tool_calls tc LEFT JOIN narrators n ON n.id = tc.narrator_id WHERE (tc.narrator_id = ? OR n.parent_narrator_id = ?) AND tc.tool_use_id = ? ORDER BY tc.created_at DESC LIMIT 1`, narratorID, narratorID, chi.URLParam(r, "toolUseId"))
	if err := row.Scan(&ownerNarratorID, &inputRaw, &outputRaw); err != nil {
		if err == sql.ErrNoRows {
			return httpx.NotFound("Tool call")
		}
		return err
	}
	toolUseID := chi.URLParam(r, "toolUseId")
	payload := map[string]any{"inputJson": decodeNullableJSON(inputRaw), "outputJson": decodeNullableJSON(outputRaw)}
	if sideCars, err := h.Service.loadSideCarsForToolUse(r.Context(), toolUseID); err == nil && len(sideCars) > 0 {
		payload["sideCars"] = sideCars
	}
	if ownerNarratorID != narratorID {
		payload["subagentNarratorId"] = ownerNarratorID
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) permissions(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT tc.id, tc.narrator_id, tc.tool_name, tc.tool_use_id, tc.input_json, tc.permission_decision_reason, tc.permission_suggestions FROM narrator_tool_calls tc LEFT JOIN narrators n ON n.id = tc.narrator_id WHERE (tc.narrator_id = ? OR n.parent_narrator_id = ?) AND tc.status = 'pending' ORDER BY tc.created_at ASC`, narratorID, narratorID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idValue, ownerNarratorID, toolName, toolUseID string
		var inputRaw, decisionReason, suggestionsRaw sql.NullString
		if err := rows.Scan(&idValue, &ownerNarratorID, &toolName, &toolUseID, &inputRaw, &decisionReason, &suggestionsRaw); err != nil {
			return err
		}
		suggestions := decodeNullableJSON(suggestionsRaw)
		if shouldHidePendingPermission(suggestions) {
			continue
		}
		item := map[string]any{"id": idValue, "toolName": toolName, "toolUseId": toolUseID, "inputJson": decodeNullableJSON(inputRaw), "decisionReason": nullString(decisionReason), "suggestions": suggestions}
		if ownerNarratorID != narratorID {
			item["subagentNarratorId"] = ownerNarratorID
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func decodeNullableJSON(raw sql.NullString) any {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var out any
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		return raw.String
	}
	return out
}

func shouldHidePendingPermission(suggestions any) bool {
	status := reflectionStatus(suggestions)
	return status != "" && status != "awaiting_user"
}

func reflectionStatus(suggestions any) string {
	items, ok := suggestions.([]any)
	if !ok {
		return ""
	}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typeValue := stringValue(entry["type"])
		if typeValue == "danger_reflection" || typeValue == "plan_reflection" || typeValue == "goal_reflection" || typeValue == "question_reflection" {
			if status := stringValue(entry["status"]); status != "" {
				return status
			}
			return "running"
		}
	}
	return ""
}

func (h *Handler) getGoals(w http.ResponseWriter, r *http.Request) error {
	goals, err := h.Service.loadGoals(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"goals": goals})
	return nil
}

func (h *Handler) addGoal(w http.ResponseWriter, r *http.Request) error {
	claims := auth.UserFromContext(r.Context())
	var body struct {
		Objective string `json:"objective"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	createdBy := ""
	if claims != nil {
		createdBy = claims.Subject
	}
	payload, err := h.Service.addGoal(r.Context(), chi.URLParam(r, "id"), body.Objective, createdBy)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, payload)
	return nil
}

func (h *Handler) updateGoal(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	var objective *string
	if value, ok := body["objective"].(string); ok {
		objective = &value
	}
	status, _ := body["status"].(string)
	payload, err := h.Service.updateGoalDirect(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "goalId"), objective, status)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) removeGoal(w http.ResponseWriter, r *http.Request) error {
	payload, err := h.Service.removeGoal(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "goalId"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) clearGoals(w http.ResponseWriter, r *http.Request) error {
	payload, err := h.Service.clearOpenGoals(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) reorderGoals(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		OrderedIDs []string `json:"orderedIds"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	payload, err := h.Service.reorderGoals(r.Context(), chi.URLParam(r, "id"), body.OrderedIDs)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) loadGoals(r *http.Request, narratorID string) ([]map[string]any, error) {
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT id, narrator_id, objective, status, sort_order, tokens_used, time_used_seconds, created_by, completed_at, created_at, updated_at FROM narrator_goals WHERE narrator_id = ? ORDER BY sort_order ASC, created_at ASC`, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idValue, narratorIDValue, objective, status, createdAt, updatedAt string
		var sortOrder, tokensUsed, timeUsedSeconds int
		var createdBy, completedAt sql.NullString
		if err := rows.Scan(&idValue, &narratorIDValue, &objective, &status, &sortOrder, &tokensUsed, &timeUsedSeconds, &createdBy, &completedAt, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": idValue, "narratorId": narratorIDValue, "objective": objective, "status": status, "sortOrder": sortOrder, "tokensUsed": tokensUsed, "timeUsedSeconds": timeUsedSeconds, "createdBy": nullString(createdBy), "completedAt": nullString(completedAt), "createdAt": createdAt, "updatedAt": updatedAt})
	}
	return items, rows.Err()
}

func (h *Handler) getBuffer(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	items, err := h.loadBuffer(r, narratorID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) updateBuffer(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "messageId")
	if _, err := h.Service.Get(r.Context(), narratorID); err != nil {
		return err
	}
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	sets := []string{}
	args := []any{}
	if text, ok := body["text"].(string); ok {
		sets = append(sets, "text = ?")
		args = append(args, text)
	}
	if commandText, ok := body["commandText"].(string); ok {
		sets = append(sets, "command_text = ?")
		args = append(args, nullableString(commandText))
	}
	if _, ok := body["priority"]; ok {
		sets = append(sets, "priority = ?")
		args = append(args, boolInt(boolValue(body["priority"])))
	}
	if images, ok := body["images"].([]any); ok {
		data, _ := json.Marshal(images)
		sets = append(sets, "images_json = ?")
		args = append(args, string(data))
	}
	if paths, ok := body["textFilePaths"].([]any); ok {
		data, _ := json.Marshal(paths)
		sets = append(sets, "text_file_paths_json = ?")
		args = append(args, string(data))
	}
	if len(sets) == 0 {
		return httpx.BadRequest("no buffer fields to update")
	}
	args = append(args, messageID, narratorID)
	result, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrator_buffered_messages SET `+strings.Join(sets, ", ")+` WHERE id = ? AND narrator_id = ?`, args...)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return httpx.NotFound("Buffered message")
	}
	items, err := h.loadBuffer(r, narratorID)
	if err != nil {
		return err
	}
	h.Service.broadcastBufferSet(r.Context(), narratorID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "message": findByID(items, messageID), "buffer": items})
	return nil
}
func (h *Handler) removeBuffer(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	messageID := chi.URLParam(r, "messageId")
	result, err := h.Service.DB.ExecContext(r.Context(), `DELETE FROM narrator_buffered_messages WHERE id = ? AND narrator_id = ?`, messageID, narratorID)
	if err != nil {
		return err
	}
	removed, _ := result.RowsAffected()
	if removed > 0 {
		cleanupBufferedTextFiles(messageID)
	}
	items, err := h.loadBuffer(r, narratorID)
	if err != nil {
		return err
	}
	h.Service.broadcastBufferSet(r.Context(), narratorID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": removed > 0, "removed": removed > 0, "buffer": items})
	return nil
}
func (h *Handler) clearBuffer(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT id FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var idValue string
		if err := rows.Scan(&idValue); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, idValue)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	result, err := h.Service.DB.ExecContext(r.Context(), `DELETE FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID)
	if err != nil {
		return err
	}
	for _, idValue := range ids {
		cleanupBufferedTextFiles(idValue)
	}
	cleared, _ := result.RowsAffected()
	h.Service.broadcastBufferSet(r.Context(), narratorID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": cleared, "buffer": []any{}})
	return nil
}
func (h *Handler) reorderBuffer(w http.ResponseWriter, r *http.Request) error {
	narratorID := chi.URLParam(r, "id")
	var body struct {
		OrderedIDs []string `json:"orderedIds"`
		MessageIDs []string `json:"messageIds"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	ids := body.OrderedIDs
	if len(ids) == 0 {
		ids = body.MessageIDs
	}
	currentIDs, err := h.bufferIDs(r.Context(), narratorID)
	if err != nil {
		return err
	}
	if !sameBufferIDSet(currentIDs, ids) {
		return httpx.BadRequest("Invalid reorder: ids do not match the current queue")
	}
	tx, err := h.Service.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, idValue := range ids {
		if _, err := tx.ExecContext(r.Context(), `UPDATE narrator_buffered_messages SET seq = ? WHERE id = ? AND narrator_id = ?`, i, idValue, narratorID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	items, err := h.loadBuffer(r, narratorID)
	if err != nil {
		return err
	}
	h.Service.broadcastBufferSet(r.Context(), narratorID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "buffer": items})
	return nil
}

func (h *Handler) bufferIDs(ctx context.Context, narratorID string) ([]string, error) {
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT id FROM narrator_buffered_messages WHERE narrator_id = ? ORDER BY seq ASC, buffered_at ASC`, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var idValue string
		if err := rows.Scan(&idValue); err != nil {
			return nil, err
		}
		ids = append(ids, idValue)
	}
	return ids, rows.Err()
}

func sameBufferIDSet(currentIDs, requestedIDs []string) bool {
	if len(currentIDs) == 0 || len(currentIDs) != len(requestedIDs) {
		return false
	}
	remaining := map[string]int{}
	for _, idValue := range currentIDs {
		remaining[idValue]++
	}
	for _, idValue := range requestedIDs {
		if remaining[idValue] == 0 {
			return false
		}
		remaining[idValue]--
	}
	return true
}

func (h *Handler) loadBuffer(r *http.Request, narratorID string) ([]map[string]any, error) {
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, buffered_at, priority, seq FROM narrator_buffered_messages WHERE narrator_id = ? ORDER BY seq ASC, buffered_at ASC LIMIT 200`, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idValue, text, bufferedAt string
		var imagesRaw, commandText, createdBy, creatorRaw, textFilePathsRaw sql.NullString
		var priority, seq int
		if err := rows.Scan(&idValue, &text, &imagesRaw, &commandText, &createdBy, &creatorRaw, &textFilePathsRaw, &bufferedAt, &priority, &seq); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": idValue, "text": text, "images": decodeAny(imagesRaw), "commandText": nullString(commandText), "createdBy": nullString(createdBy), "creator": decodeAny(creatorRaw), "textFilePaths": decodeAny(textFilePathsRaw), "bufferedAt": bufferedAt, "priority": priority != 0, "seq": seq})
	}
	return items, rows.Err()
}

func (h *Handler) listBackgroundTasks(ctx context.Context, narratorID string) ([]map[string]any, error) {
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT id, subagent_narrator_id, title, command, status, output, started_at, completed_at, created_at, updated_at FROM background_tasks WHERE parent_narrator_id = ? ORDER BY created_at DESC LIMIT 100`, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	seenChildren := map[string]bool{}
	for rows.Next() {
		var idValue, status, createdAt, updatedAt, startedAt string
		var childID, title, command, output, completedAt sql.NullString
		if err := rows.Scan(&idValue, &childID, &title, &command, &status, &output, &startedAt, &completedAt, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if childID.Valid && childID.String != "" {
			seenChildren[childID.String] = true
		}
		items = append(items, map[string]any{"id": idValue, "narratorId": narratorID, "childNarratorId": nullString(childID), "title": nullString(title), "prompt": nullString(command), "status": status, "cancelRequested": status == "cancelled", "output": nullString(output), "result": nullString(output), "errorMessage": nil, "startedAt": startedAt, "completedAt": nullString(completedAt), "createdAt": createdAt, "updatedAt": updatedAt, "source": "background_tasks"})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	childRows, err := h.Service.DB.QueryContext(ctx, `SELECT id, title, status, background_status, background_result, error_message, background_completed_at, created_at, updated_at FROM narrators WHERE parent_narrator_id = ? AND is_background = 1 ORDER BY created_at DESC LIMIT 100`, narratorID)
	if err != nil {
		return nil, err
	}
	defer childRows.Close()
	for childRows.Next() {
		var idValue, status, createdAt, updatedAt string
		var title, backgroundStatus, backgroundResult, errorMessage, completedAt sql.NullString
		if err := childRows.Scan(&idValue, &title, &status, &backgroundStatus, &backgroundResult, &errorMessage, &completedAt, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if seenChildren[idValue] {
			continue
		}
		taskStatus := status
		if backgroundStatus.Valid && strings.TrimSpace(backgroundStatus.String) != "" {
			taskStatus = backgroundStatus.String
		}
		items = append(items, map[string]any{"id": idValue, "narratorId": narratorID, "childNarratorId": idValue, "title": nullString(title), "status": taskStatus, "cancelRequested": taskStatus == "cancelled", "output": nullString(backgroundResult), "result": decodeAny(backgroundResult), "errorMessage": nullString(errorMessage), "completedAt": nullString(completedAt), "createdAt": createdAt, "updatedAt": updatedAt, "source": "background_narrator"})
	}
	return items, childRows.Err()
}

func (h *Handler) backgroundTaskOutputPayload(ctx context.Context, narratorID, taskID string) (map[string]any, bool, error) {
	var status string
	var taskOutput, completedAt sql.NullString
	err := h.Service.DB.QueryRowContext(ctx, `SELECT status, output, completed_at FROM background_tasks WHERE parent_narrator_id = ? AND id = ?`, narratorID, taskID).Scan(&status, &taskOutput, &completedAt)
	if err == nil {
		output := nullString(taskOutput)
		if status == "running" {
			if liveOutput, ok := h.Service.liveBackgroundBashOutput(taskID); ok {
				output = liveOutput
			}
		}
		return map[string]any{"status": status, "output": output, "result": output, "errorMessage": nil, "completedAt": nullString(completedAt)}, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	var childStatus string
	var backgroundStatus, backgroundResult, childError sql.NullString
	err = h.Service.DB.QueryRowContext(ctx, `SELECT status, background_status, background_result, error_message FROM narrators WHERE parent_narrator_id = ? AND is_background = 1 AND id = ?`, narratorID, taskID).Scan(&childStatus, &backgroundStatus, &backgroundResult, &childError)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	messages, err := h.backgroundNarratorTranscript(ctx, taskID)
	if err != nil {
		return nil, false, err
	}
	status = childStatus
	if backgroundStatus.Valid && strings.TrimSpace(backgroundStatus.String) != "" {
		status = backgroundStatus.String
	}
	output := any(messages)
	if strings.TrimSpace(messages) == "" {
		output = nullString(backgroundResult)
	}
	return map[string]any{"status": status, "output": output, "result": decodeAny(backgroundResult), "errorMessage": nullString(childError)}, true, nil
}

func (h *Handler) backgroundNarratorTranscript(ctx context.Context, narratorID string) (string, error) {
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT role, content_text FROM narrator_messages WHERE narrator_id = ? ORDER BY created_at ASC LIMIT 200`, narratorID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	parts := []string{}
	for rows.Next() {
		var role string
		var text sql.NullString
		if err := rows.Scan(&role, &text); err != nil {
			return "", err
		}
		if text.Valid && strings.TrimSpace(text.String) != "" {
			parts = append(parts, role+": "+text.String)
		}
	}
	return strings.Join(parts, "\n"), rows.Err()
}

func (h *Handler) tableExists(ctx context.Context, table string) (bool, error) {
	return tableExistsOn(ctx, h.Service.DB, table)
}

func tableExistsOn(ctx context.Context, sqlDB *sql.DB, table string) (bool, error) {
	if sqlDB == nil {
		return false, nil
	}
	var name string
	err := sqlDB.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return name == table, nil
}

func (h *Handler) loadBrowserSessionItem(ctx context.Context, narratorID, sessionID string) (map[string]any, error) {
	items, err := h.loadBrowserSessions(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if idValue, _ := item["id"].(string); idValue == sessionID {
			return item, nil
		}
	}
	return nil, httpx.NotFound("Browser session")
}

func (h *Handler) loadBrowserSessions(ctx context.Context, narratorID string) ([]map[string]any, error) {
	browserDB := h.Service.browserDB()
	if ok, err := tableExistsOn(ctx, browserDB, "narrator_browser_sessions"); err != nil || !ok {
		return browserSessionItemsFromSnapshots(agentbasic.LiveBrowserSessionSnapshots(narratorID)), nil
	}
	columns, err := tableColumnSet(ctx, browserDB, "narrator_browser_sessions")
	if err != nil {
		return nil, err
	}
	nowISO := db.NowISO()
	nowMs := time.Now().UnixMilli()
	if columns["expires_at_ms"] {
		if _, err := browserDB.ExecContext(ctx, `UPDATE narrator_browser_sessions SET status = 'expired', closed_at = COALESCE(closed_at, ?), updated_at = ? WHERE narrator_id = ? AND status IN ('active', 'running', 'idle') AND expires_at_ms IS NOT NULL AND expires_at_ms > 0 AND expires_at_ms <= ?`, nowISO, nowISO, narratorID, nowMs); err != nil {
			return nil, err
		}
	} else if columns["expires_at"] {
		if _, err := browserDB.ExecContext(ctx, `UPDATE narrator_browser_sessions SET status = 'expired', closed_at = COALESCE(closed_at, ?), updated_at = ? WHERE narrator_id = ? AND status IN ('active', 'running', 'idle') AND expires_at IS NOT NULL AND expires_at <= ?`, nowISO, nowISO, narratorID, nowISO); err != nil {
			return nil, err
		}
	}
	selectCol := func(name, fallback string) string {
		if columns[name] {
			return name
		}
		return fallback + " AS " + name
	}
	screenshotExpr := "''"
	if columns["screenshot_data_base64"] {
		screenshotExpr = "screenshot_data_base64"
	}
	query := `SELECT id, ` +
		selectCol("status", "'active'") + `, ` +
		selectCol("url", "NULL") + `, ` +
		selectCol("title", "NULL") + `, ` +
		selectCol("ttl_seconds", "NULL") + `, ` +
		selectCol("ttl_ms", "NULL") + `, ` +
		selectCol("expires_at", "NULL") + `, ` +
		selectCol("expires_at_ms", "NULL") + `, ` +
		selectCol("last_activity_ms", "NULL") + `, ` +
		selectCol("headless", "NULL") + `, ` +
		`CASE WHEN ` + screenshotExpr + ` IS NOT NULL AND LENGTH(` + screenshotExpr + `) > 0 THEN 1 ELSE 0 END AS has_screenshot, ` +
		selectCol("tracing_status", "NULL") + `, ` +
		selectCol("tracing_json", "NULL") + `, ` +
		selectCol("metadata_json", "NULL") + `, ` +
		selectCol("created_at", "NULL") + `, ` +
		selectCol("updated_at", "NULL") + `, ` +
		selectCol("closed_at", "NULL") +
		` FROM narrator_browser_sessions WHERE narrator_id = ? ORDER BY updated_at DESC LIMIT 50`
	rows, err := browserDB.QueryContext(ctx, query, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	itemsByID := map[string]map[string]any{}
	order := []string{}
	for rows.Next() {
		var idValue string
		var status, urlValue, title, expiresAt, tracingStatus, tracingRaw, metadataRaw, createdAt, updatedAt, closedAt sql.NullString
		var ttlSeconds, ttlMs, expiresAtMs, lastActivityMs, headless sql.NullInt64
		var hasScreenshot int
		if err := rows.Scan(&idValue, &status, &urlValue, &title, &ttlSeconds, &ttlMs, &expiresAt, &expiresAtMs, &lastActivityMs, &headless, &hasScreenshot, &tracingStatus, &tracingRaw, &metadataRaw, &createdAt, &updatedAt, &closedAt); err != nil {
			return nil, err
		}
		item := browserSessionItemFromRow(narratorID, idValue, status, urlValue, title, ttlSeconds, ttlMs, expiresAt, expiresAtMs, lastActivityMs, headless, hasScreenshot > 0, tracingStatus, tracingRaw, metadataRaw, createdAt, updatedAt, closedAt)
		itemsByID[idValue] = item
		order = append(order, idValue)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, item := range browserSessionItemsFromSnapshots(agentbasic.LiveBrowserSessionSnapshots(narratorID)) {
		idValue, _ := item["id"].(string)
		if idValue == "" {
			continue
		}
		if _, exists := itemsByID[idValue]; !exists {
			order = append([]string{idValue}, order...)
		}
		itemsByID[idValue] = item
	}
	items := make([]map[string]any, 0, len(itemsByID))
	seen := map[string]bool{}
	for _, idValue := range order {
		if seen[idValue] {
			continue
		}
		seen[idValue] = true
		if item := itemsByID[idValue]; item != nil {
			items = append(items, item)
		}
	}
	return items, nil
}

func browserSessionItemsFromSnapshots(snapshots []agentbasic.BrowserSessionSnapshot) []map[string]any {
	items := make([]map[string]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		items = append(items, browserSessionItemFromSnapshot(snapshot))
	}
	return items
}

func browserSessionItemFromSnapshot(snapshot agentbasic.BrowserSessionSnapshot) map[string]any {
	trace := any(nil)
	if snapshot.TracingActive {
		startedAt := int64(0)
		if snapshot.TracingStartedAt != nil {
			startedAt = snapshot.TracingStartedAt.UnixMilli()
		}
		trace = map[string]any{"active": true, "startedAt": startedAt}
	}
	metadata := map[string]any{
		"source":              "live",
		"chromePath":          snapshot.ChromePath,
		"consoleMessageCount": snapshot.ConsoleMessageCount,
	}
	if snapshot.Reason != "" {
		metadata["reason"] = snapshot.Reason
	}
	return map[string]any{
		"id":                    snapshot.ID,
		"narratorId":            snapshot.NarratorID,
		"status":                firstString(snapshot.Status, "active"),
		"url":                   snapshot.URL,
		"title":                 snapshot.Title,
		"lastActivity":          snapshot.LastActivity.UnixMilli(),
		"ttlMs":                 snapshot.TtlMs,
		"ttlSeconds":            snapshot.TtlMs / 1000,
		"expiresAt":             snapshot.ExpiresAt.UnixMilli(),
		"expiresAtIso":          snapshot.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"headless":              snapshot.Headless,
		"tracing":               trace,
		"tracingStatus":         map[bool]string{true: "active", false: ""}[snapshot.TracingActive],
		"networkRequestCount":   snapshot.NetworkRequestCount,
		"networkCaptureEnabled": snapshot.NetworkCaptureEnabled,
		"hasScreenshot":         len(snapshot.ScreenshotData) > 0 || snapshot.ScreenshotFilePath != "",
		"metadata":              metadata,
		"createdAt":             snapshot.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updatedAt":             snapshot.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"closedAt":              nil,
	}
}

func browserSessionItemFromRow(narratorID, idValue string, status, urlValue, title sql.NullString, ttlSeconds, ttlMs sql.NullInt64, expiresAt sql.NullString, expiresAtMs, lastActivityMs, headless sql.NullInt64, hasScreenshot bool, tracingStatus, tracingRaw, metadataRaw, createdAt, updatedAt, closedAt sql.NullString) map[string]any {
	lastMs := lastActivityMs.Int64
	if !lastActivityMs.Valid || lastMs <= 0 {
		lastMs = browserParseTimeMs(updatedAt, createdAt)
	}
	ttlValue := ttlMs.Int64
	if (!ttlMs.Valid || ttlValue <= 0) && ttlSeconds.Valid {
		ttlValue = ttlSeconds.Int64 * 1000
	}
	expiresValue := expiresAtMs.Int64
	if !expiresAtMs.Valid || expiresValue <= 0 {
		expiresValue = browserParseTimeMs(expiresAt)
		if expiresValue <= 0 && lastMs > 0 && ttlValue > 0 {
			expiresValue = lastMs + ttlValue
		}
	}
	metadata := decodeAny(metadataRaw)
	if metadata == nil {
		metadata = map[string]any{}
	}
	return map[string]any{
		"id":                    idValue,
		"narratorId":            narratorID,
		"status":                firstString(browserSQLString(status), "active"),
		"url":                   nullString(urlValue),
		"title":                 nullString(title),
		"lastActivity":          lastMs,
		"ttlMs":                 ttlValue,
		"ttlSeconds":            ttlValue / 1000,
		"expiresAt":             expiresValue,
		"expiresAtIso":          nullString(expiresAt),
		"headless":              headless.Valid && headless.Int64 != 0,
		"tracing":               browserTracePayload(tracingStatus, tracingRaw),
		"tracingStatus":         nullString(tracingStatus),
		"networkRequestCount":   browserMetadataInt(metadata, "networkRequestCount"),
		"networkCaptureEnabled": browserMetadataBool(metadata, "networkCaptureEnabled"),
		"hasScreenshot":         hasScreenshot,
		"trace":                 decodeAny(tracingRaw),
		"metadata":              metadata,
		"createdAt":             nullString(createdAt),
		"updatedAt":             nullString(updatedAt),
		"closedAt":              nullString(closedAt),
	}
}

func browserSQLString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func browserTracePayload(status sql.NullString, raw sql.NullString) any {
	if nullString(status) != "active" {
		return nil
	}
	decoded, _ := decodeAny(raw).(map[string]any)
	startedAt := int64(0)
	if decoded != nil {
		switch value := decoded["startedAt"].(type) {
		case float64:
			startedAt = int64(value)
		case int64:
			startedAt = value
		case string:
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				startedAt = parsed.UnixMilli()
			}
		}
	}
	return map[string]any{"active": true, "startedAt": startedAt}
}

func browserParseTimeMs(values ...sql.NullString) int64 {
	for _, value := range values {
		if !value.Valid || strings.TrimSpace(value.String) == "" {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339Nano, value.String); err == nil {
			return parsed.UnixMilli()
		}
		if parsed, err := time.Parse(time.RFC3339, value.String); err == nil {
			return parsed.UnixMilli()
		}
	}
	return 0
}

func browserMetadataInt(value any, key string) int64 {
	m, _ := value.(map[string]any)
	switch raw := m[key].(type) {
	case float64:
		return int64(raw)
	case int64:
		return raw
	case int:
		return int64(raw)
	}
	return 0
}

func browserMetadataBool(value any, key string) bool {
	m, _ := value.(map[string]any)
	if raw, ok := m[key].(bool); ok {
		return raw
	}
	return false
}

func (h *Handler) buildCurrentPlan(ctx context.Context, narratorID string) (string, error) {
	rows, err := h.Service.DB.QueryContext(ctx, `SELECT role, content_text FROM narrator_messages WHERE narrator_id = ? AND content_text IS NOT NULL AND TRIM(content_text) != '' ORDER BY created_at DESC LIMIT 12`, narratorID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	parts := []string{}
	for rows.Next() {
		var role string
		var text string
		if err := rows.Scan(&role, &text); err != nil {
			return "", err
		}
		if len(text) > 500 {
			text = text[:500] + "…"
		}
		parts = append(parts, "- "+role+": "+strings.TrimSpace(text))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(parts) == 0 {
		return "No plan has been recorded yet.", nil
	}
	return "Current plan context:\n" + strings.Join(parts, "\n"), nil
}

func (h *Handler) loadUsageStats(ctx context.Context, narratorID string, includeSubagents bool) (map[string]any, error) {
	stats := zeroUsageStatsMap()
	if h == nil || h.Service == nil || h.Service.DB == nil {
		return stats, nil
	}
	apiColumns, err := db.TableColumnSet(ctx, h.Service.DB, "api_requests")
	if err != nil {
		return nil, err
	}
	if len(apiColumns) > 0 && apiColumns["narrator_id"] {
		where := "ar.narrator_id = ?"
		args := []any{narratorID}
		join := ""
		if includeSubagents {
			if narratorColumns, err := db.TableColumnSet(ctx, h.Service.DB, "narrators"); err != nil {
				return nil, err
			} else if narratorColumns["id"] && narratorColumns["parent_narrator_id"] {
				join = " LEFT JOIN narrators n ON n.id = ar.narrator_id"
				where = "(ar.narrator_id = ? OR n.parent_narrator_id = ?)"
				args = append(args, narratorID)
			}
		}
		query := fmt.Sprintf(
			`SELECT COUNT(*), %s, %s, %s, %s, %s, %s, %s, %s, %s, %s FROM api_requests ar%s WHERE %s`,
			usageSumExpr(apiColumns, "input_tokens"),
			usageSumExpr(apiColumns, "output_tokens"),
			usageSumExpr(apiColumns, "cache_creation_input_tokens"),
			usageSumExpr(apiColumns, "cached_input_tokens"),
			usageSumExpr(apiColumns, "cache_creation_5m_tokens"),
			usageSumExpr(apiColumns, "cache_creation_1h_tokens"),
			usageSumExpr(apiColumns, "reasoning_tokens"),
			usageSumFloatExpr(apiColumns, "cost_usd"),
			usageAvgFloatExpr(apiColumns, "duration_ms"),
			usageAvgFloatExpr(apiColumns, "ttft_ms"),
			join,
			where,
		)
		var totalRequests, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, cache5mTokens, cache1hTokens, reasoningTokens int64
		var totalCost, averageDuration, averageTtft float64
		if err := h.Service.DB.QueryRowContext(ctx, query, args...).Scan(&totalRequests, &inputTokens, &outputTokens, &cacheCreationTokens, &cacheReadTokens, &cache5mTokens, &cache1hTokens, &reasoningTokens, &totalCost, &averageDuration, &averageTtft); err != nil {
			return nil, err
		}
		applyUsageStats(stats, totalRequests, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, cache5mTokens, cache1hTokens, reasoningTokens, totalCost, averageDuration, averageTtft)
	}
	toolCallCount, err := h.loadUsageToolCallCount(ctx, narratorID, includeSubagents)
	if err != nil {
		return nil, err
	}
	stats["toolCallCount"] = toolCallCount
	return stats, nil
}

func zeroUsageStatsMap() map[string]any {
	return map[string]any{
		"totalRequests":              int64(0),
		"totalInputTokens":           int64(0),
		"totalOutputTokens":          int64(0),
		"totalCacheCreationTokens":   int64(0),
		"totalCacheReadTokens":       int64(0),
		"totalCacheCreation5mTokens": int64(0),
		"totalCacheCreation1hTokens": int64(0),
		"totalReasoningTokens":       int64(0),
		"totalTokens":                int64(0),
		"totalCost":                  0.0,
		"averageDurationMs":          0.0,
		"averageTtftMs":              0.0,
		"totalCostUsd":               0.0,
		"inputTokens":                int64(0),
		"outputTokens":               int64(0),
		"cacheCreationTokens":        int64(0),
		"cacheReadTokens":            int64(0),
		"reasoningTokens":            int64(0),
		"requestCount":               int64(0),
		"toolCallCount":              int64(0),
	}
}

func usageSumExpr(columns map[string]bool, column string) string {
	if !columns[column] {
		return "0"
	}
	return "COALESCE(SUM(ar." + column + "), 0)"
}

func usageSumFloatExpr(columns map[string]bool, column string) string {
	if !columns[column] {
		return "0"
	}
	return "COALESCE(SUM(ar." + column + "), 0)"
}

func usageAvgFloatExpr(columns map[string]bool, column string) string {
	if !columns[column] {
		return "0"
	}
	return "COALESCE(AVG(ar." + column + "), 0)"
}

func applyUsageStats(stats map[string]any, totalRequests, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, cache5mTokens, cache1hTokens, reasoningTokens int64, totalCost, averageDuration, averageTtft float64) {
	totalTokens := inputTokens + outputTokens + cacheCreationTokens + cacheReadTokens
	stats["totalRequests"] = totalRequests
	stats["totalInputTokens"] = inputTokens
	stats["totalOutputTokens"] = outputTokens
	stats["totalCacheCreationTokens"] = cacheCreationTokens
	stats["totalCacheReadTokens"] = cacheReadTokens
	stats["totalCacheCreation5mTokens"] = cache5mTokens
	stats["totalCacheCreation1hTokens"] = cache1hTokens
	stats["totalReasoningTokens"] = reasoningTokens
	stats["totalTokens"] = totalTokens
	stats["totalCost"] = totalCost
	stats["averageDurationMs"] = averageDuration
	stats["averageTtftMs"] = averageTtft
	stats["totalCostUsd"] = totalCost
	stats["inputTokens"] = inputTokens
	stats["outputTokens"] = outputTokens
	stats["cacheCreationTokens"] = cacheCreationTokens
	stats["cacheReadTokens"] = cacheReadTokens
	stats["reasoningTokens"] = reasoningTokens
	stats["requestCount"] = totalRequests
}

func (h *Handler) loadUsageToolCallCount(ctx context.Context, narratorID string, includeSubagents bool) (int64, error) {
	columns, err := db.TableColumnSet(ctx, h.Service.DB, "narrator_tool_calls")
	if err != nil || len(columns) == 0 || !columns["narrator_id"] {
		return 0, err
	}
	where := "tc.narrator_id = ?"
	args := []any{narratorID}
	join := ""
	if includeSubagents {
		if narratorColumns, err := db.TableColumnSet(ctx, h.Service.DB, "narrators"); err != nil {
			return 0, err
		} else if narratorColumns["id"] && narratorColumns["parent_narrator_id"] {
			join = " LEFT JOIN narrators n ON n.id = tc.narrator_id"
			where = "(tc.narrator_id = ? OR n.parent_narrator_id = ?)"
			args = append(args, narratorID)
		}
	}
	var count int64
	if err := h.Service.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_tool_calls tc`+join+` WHERE `+where, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (h *Handler) listRules(table string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT * FROM `+table+` WHERE narrator_id = ? ORDER BY created_at ASC`, chi.URLParam(r, "id"))
		if err != nil {
			return err
		}
		defer rows.Close()
		items, err := rowsToMaps(rows)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, items)
		return nil
	}
}

func (h *Handler) createRule(table, primaryField, secondaryField, defaultSecondary string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var body map[string]any
		if err := httpx.DecodeLooseJSON(r, &body); err != nil {
			return err
		}
		primary, _ := body[snakeToCamel(primaryField)].(string)
		if primary == "" {
			primary, _ = body[primaryField].(string)
		}
		if primary == "" {
			return httpx.BadRequest(primaryField + " is required")
		}
		entryID := id.New()
		now := db.NowISO()
		enabled := 1
		if v, ok := body["enabled"].(bool); ok && !v {
			enabled = 0
		}
		if secondaryField == "" {
			_, err := h.Service.DB.ExecContext(r.Context(), `INSERT INTO `+table+` (id, narrator_id, `+primaryField+`, enabled, created_at) VALUES (?, ?, ?, ?, ?)`, entryID, chi.URLParam(r, "id"), primary, enabled, now)
			if err != nil {
				return err
			}
		} else {
			secondary, _ := body[snakeToCamel(secondaryField)].(string)
			if secondary == "" {
				secondary = defaultSecondary
			}
			_, err := h.Service.DB.ExecContext(r.Context(), `INSERT INTO `+table+` (id, narrator_id, `+primaryField+`, `+secondaryField+`, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?)`, entryID, chi.URLParam(r, "id"), primary, secondary, enabled, now)
			if err != nil {
				return err
			}
		}
		rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT * FROM `+table+` WHERE id = ?`, entryID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items, err := rowsToMaps(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return httpx.NotFound("Rule")
		}
		httpx.WriteJSON(w, http.StatusCreated, items[0])
		return nil
	}
}

func (h *Handler) updateRule(table, secondaryField string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var body map[string]any
		if err := httpx.DecodeLooseJSON(r, &body); err != nil {
			return err
		}
		sets := []string{}
		args := []any{}
		if enabled, ok := body["enabled"].(bool); ok {
			sets = append(sets, "enabled = ?")
			if enabled {
				args = append(args, 1)
			} else {
				args = append(args, 0)
			}
		}
		if secondaryField != "" {
			if value, ok := body[snakeToCamel(secondaryField)]; ok {
				sets = append(sets, secondaryField+" = ?")
				args = append(args, value)
			}
		}
		if len(sets) > 0 {
			args = append(args, chi.URLParam(r, "entryId"))
			if _, err := h.Service.DB.ExecContext(r.Context(), `UPDATE `+table+` SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
				return err
			}
		}
		return h.ok(w, r)
	}
}

func (h *Handler) deleteRule(table string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		_, err := h.Service.DB.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE id = ?`, chi.URLParam(r, "entryId"))
		if err != nil {
			return err
		}
		return h.ok(w, r)
	}
}

const (
	draftTraitPrefix                    = "draft:"
	planTraitPrefix                     = "plan-state:"
	subagentModelRestrictionTraitPrefix = "custom-subagent-models:"
	disabledToolsTraitPrefix            = "custom-disabled-tools:"
)

func (h *Handler) narratorTraits(ctx context.Context, narratorID string) ([]string, error) {
	var raw string
	if err := h.Service.DB.QueryRowContext(ctx, `SELECT traits FROM narrators WHERE id = ?`, narratorID).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil, httpx.NotFound("Narrator")
		}
		return nil, err
	}
	traits := []string{}
	_ = json.Unmarshal([]byte(raw), &traits)
	if traits == nil {
		traits = []string{}
	}
	return traits, nil
}

func (h *Handler) saveNarratorTraits(ctx context.Context, narratorID string, traits []string) error {
	data, _ := json.Marshal(traits)
	res, err := h.Service.DB.ExecContext(ctx, `UPDATE narrators SET traits = ?, updated_at = ? WHERE id = ?`, string(data), db.NowISO(), narratorID)
	if err != nil {
		return err
	}
	if count, _ := res.RowsAffected(); count == 0 {
		return httpx.NotFound("Narrator")
	}
	return nil
}

func encodeTrait(prefix string, payload any) string {
	data, _ := json.Marshal(payload)
	return prefix + base64.RawURLEncoding.EncodeToString(data)
}

func decodeTrait(trait, prefix string) map[string]any {
	if !strings.HasPrefix(trait, prefix) {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(trait, prefix))
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

func removeTraitPrefix(traits []string, prefix string) []string {
	out := []string{}
	for _, trait := range traits {
		if !strings.HasPrefix(trait, prefix) {
			out = append(out, trait)
		}
	}
	return out
}

func publicTraitList(traits []string) []string {
	return removeTraitPrefix(traits, draftTraitPrefix)
}

func upsertDraftTrait(traits []string, draft map[string]any) []string {
	out := removeTraitPrefix(traits, draftTraitPrefix)
	if strings.TrimSpace(stringValue(draft["text"])) == "" {
		return out
	}
	out = append(out, encodeTrait(draftTraitPrefix, draft))
	return out
}

func parseDraftTrait(traits []string) map[string]any {
	for _, trait := range traits {
		draft := decodeTrait(trait, draftTraitPrefix)
		if strings.TrimSpace(stringValue(draft["text"])) != "" {
			return draft
		}
	}
	return nil
}

func draftTraitResponse(traits []string) map[string]any {
	draft := parseDraftTrait(traits)
	if draft == nil {
		return map[string]any{"hasDraft": false, "text": "", "updatedAt": nil, "updatedBy": nil, "sourceId": nil}
	}
	return map[string]any{"hasDraft": true, "text": stringValue(draft["text"]), "updatedAt": nullableAnyString(draft["updatedAt"]), "updatedBy": nullableAnyString(draft["updatedBy"]), "sourceId": nullableAnyString(draft["sourceId"])}
}

func parsePlanTrait(traits []string) map[string]any {
	for _, trait := range traits {
		plan := decodeTrait(trait, planTraitPrefix)
		if strings.TrimSpace(stringValue(plan["content"])) != "" {
			return plan
		}
	}
	return nil
}

func upsertPlanTrait(traits []string, plan map[string]any) []string {
	out := removeTraitPrefix(traits, planTraitPrefix)
	if strings.TrimSpace(stringValue(plan["content"])) == "" {
		return out
	}
	out = append(out, encodeTrait(planTraitPrefix, plan))
	return out
}

func mergeMaps(base map[string]any, extras map[string]any) map[string]any {
	for key, value := range extras {
		base[key] = value
	}
	return base
}

func nullableAnyString(value any) any {
	if s := strings.TrimSpace(stringValue(value)); s != "" {
		return s
	}
	return nil
}

func normalizePoolKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	builder := strings.Builder{}
	lastDash := false
	for _, r := range value {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if allowed {
			builder.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func normalizeSubagentModelRestriction(input any) map[string]any {
	raw := input
	if m, ok := input.(map[string]any); ok {
		if pools, exists := m["pools"]; exists {
			raw = pools
		}
	}
	pools := map[string]any{}
	if rawPools, ok := raw.(map[string]any); ok {
		for rawKey, rawValue := range rawPools {
			key := normalizePoolKey(rawKey)
			if key == "" {
				continue
			}
			entries := []map[string]any{}
			if list, ok := rawValue.([]any); ok {
				seen := map[string]bool{}
				for _, item := range list {
					model := ""
					purpose := ""
					switch v := item.(type) {
					case string:
						model = strings.TrimSpace(v)
					case map[string]any:
						model = strings.TrimSpace(stringValue(v["model"]))
						purpose = strings.TrimSpace(stringValue(v["purpose"]))
					}
					if model == "" || seen[model] {
						continue
					}
					seen[model] = true
					entry := map[string]any{"model": model}
					if purpose != "" {
						entry["purpose"] = purpose
					}
					entries = append(entries, entry)
					if len(entries) >= 50 {
						break
					}
				}
			}
			pools[key] = entries
		}
	}
	return map[string]any{"version": 1, "pools": pools}
}

func (h *Handler) normalizeDisabledTools(ctx context.Context, input any) map[string]any {
	raw := input
	if m, ok := input.(map[string]any); ok {
		if tools, exists := m["tools"]; exists {
			raw = tools
		}
	}
	known := map[string]bool{}
	for _, tool := range h.availableTraitTools(ctx) {
		known[stringValue(tool["name"])] = true
	}
	seen := map[string]bool{}
	tools := []string{}
	if rawTools, ok := raw.([]any); ok {
		for _, item := range rawTools {
			name := strings.TrimSpace(stringValue(item))
			if name == "" || seen[name] || !known[name] {
				continue
			}
			seen[name] = true
			tools = append(tools, name)
		}
	}
	return map[string]any{"version": 1, "tools": tools}
}

func (h *Handler) customTraitsResponse(ctx context.Context, traits []string) map[string]any {
	return map[string]any{"subagentModelRestriction": parseEncodedTrait(traits, subagentModelRestrictionTraitPrefix), "disabledTools": parseEncodedTrait(traits, disabledToolsTraitPrefix), "availableModels": h.availableModelsForTraits(), "availableTools": h.availableTraitTools(ctx)}
}

func parseEncodedTrait(traits []string, prefix string) map[string]any {
	for _, trait := range traits {
		if decoded := decodeTrait(trait, prefix); decoded != nil {
			return decoded
		}
	}
	return nil
}

func (h *Handler) customTraits(w http.ResponseWriter, r *http.Request) error {
	traits, err := h.narratorTraits(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, h.customTraitsResponse(r.Context(), traits))
	return nil
}

func (h *Handler) setSubagentModelRestriction(w http.ResponseWriter, r *http.Request) error {
	var body any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	restriction := normalizeSubagentModelRestriction(body)
	return h.updateCustomTrait(w, r, subagentModelRestrictionTraitPrefix, restriction, len(restriction["pools"].(map[string]any)) == 0)
}

func (h *Handler) clearSubagentModelRestriction(w http.ResponseWriter, r *http.Request) error {
	return h.updateCustomTrait(w, r, subagentModelRestrictionTraitPrefix, nil, true)
}

func (h *Handler) setDisabledTools(w http.ResponseWriter, r *http.Request) error {
	var body any
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	return h.updateCustomTrait(w, r, disabledToolsTraitPrefix, h.normalizeDisabledTools(r.Context(), body), false)
}

func (h *Handler) clearDisabledTools(w http.ResponseWriter, r *http.Request) error {
	return h.updateCustomTrait(w, r, disabledToolsTraitPrefix, nil, true)
}

func (h *Handler) updateCustomTrait(w http.ResponseWriter, r *http.Request, prefix string, payload any, remove bool) error {
	narratorID := chi.URLParam(r, "id")
	traits, err := h.narratorTraits(r.Context(), narratorID)
	if err != nil {
		return err
	}
	updated := removeTraitPrefix(traits, prefix)
	if !remove {
		updated = append(updated, encodeTrait(prefix, payload))
	}
	if err := h.saveNarratorTraits(r.Context(), narratorID, updated); err != nil {
		return err
	}
	custom := h.customTraitsResponse(r.Context(), updated)
	if h.Service.Hub != nil {
		h.Service.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "custom_traits_changed", "narratorId": narratorID, "traits": publicTraitList(updated), "customTraits": custom})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "traits": publicTraitList(updated), "customTraits": custom})
	return nil
}

func (h *Handler) availableModelsForTraits() []map[string]any {
	seen := map[string]bool{}
	models := []map[string]any{}
	add := func(model string, purpose string) {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		item := map[string]any{"model": model}
		if purpose != "" {
			item["purpose"] = purpose
		}
		models = append(models, item)
	}
	if h.Service == nil || h.Service.Settings == nil {
		return models
	}
	raw := h.Service.Settings.Raw()
	if agent, ok := raw["agent"].(map[string]any); ok {
		add(stringValue(agent["defaultModel"]), "default")
		add(stringValue(agent["summaryModel"]), "summary")
		if custom, ok := agent["customModels"].([]any); ok {
			for _, item := range custom {
				if model, ok := item.(map[string]any); ok {
					add(stringValue(model["value"]), stringValue(model["label"]))
				}
			}
		}
	}
	for _, field := range []string{"clineProviders", "openaiProviders", "anthropicProviders", "nkpProviders", "nugProviders"} {
		providers, _ := raw[field].([]any)
		for _, item := range providers {
			provider, ok := item.(map[string]any)
			if !ok || boolValue(provider["disabled"]) {
				continue
			}
			prefix := firstString(stringValue(provider["prefix"]), defaultProviderPrefix(field))
			addPrefixedModelList(add, prefix, provider["enabledModels"])
			add(stringValue(provider["defaultModel"]), stringValue(provider["name"]))
		}
	}
	return models
}

func addPrefixedModelList(add func(string, string), prefix string, value any) {
	for _, modelID := range anyStringList(value) {
		if strings.Contains(modelID, ":") {
			add(modelID, prefix)
		} else {
			add(prefix+":"+modelID, prefix)
		}
	}
}

func (h *Handler) availableTraitTools(ctx context.Context) []map[string]any {
	externalTools := []agentbasic.ExternalToolSpec{}
	if h != nil && h.Service != nil && h.Service.ExternalTools != nil {
		externalTools = h.Service.ExternalTools.AgentToolSpecs(ctx, ExternalToolContext{})
	}
	return agentbasic.ToolMenuCapabilitiesForOptions(agentbasic.ToolInventoryOptions{ExternalTools: externalTools, MCPExternalToolInjection: h != nil && h.Service != nil && h.Service.ExternalTools != nil})
}

func anyStringList(value any) []string {
	raw, _ := value.([]any)
	out := []string{}
	for _, item := range raw {
		if s := strings.TrimSpace(stringValue(item)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func defaultProviderPrefix(field string) string {
	switch field {
	case "clineProviders":
		return "cline"
	case "anthropicProviders":
		return "anthropic"
	case "nkpProviders":
		return "nkp"
	case "nugProviders":
		return "nug"
	default:
		return "openai"
	}
}

func boolValue(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPresentString(values map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if ok {
			return text, true
		}
	}
	return "", false
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}
func (h *Handler) ok(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) updateCwd(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Cwd string `json:"cwd"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	_, err := h.Service.UpdateTextField(r.Context(), chi.URLParam(r, "id"), "cwd", body.Cwd)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "cwd": body.Cwd, "changed": true})
	return nil
}
func (h *Handler) generateTitle(w http.ResponseWriter, r *http.Request) error {
	n, err := h.Service.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	title := "Narrator"
	if n.Title != nil {
		title = *n.Title
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"title": title})
	return nil
}
func (h *Handler) updateReasoningEffort(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ReasoningEffort *string `json:"reasoningEffort"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	_, err := h.Service.UpdateTextField(r.Context(), chi.URLParam(r, "id"), "reasoning_effort", nullableStringValue(body.ReasoningEffort))
	if err != nil {
		return err
	}
	return h.ok(w, r)
}
func (h *Handler) updateBool(column, jsonKey string) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var body map[string]any
		if err := httpx.DecodeLooseJSON(r, &body); err != nil {
			return err
		}
		value := 0
		boolValue := body[jsonKey] == true
		if boolValue {
			value = 1
		}
		narratorID := chi.URLParam(r, "id")
		_, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET `+column+` = ?, updated_at = ? WHERE id = ?`, value, db.NowISO(), narratorID)
		if err != nil {
			return err
		}
		if h.Service.Hub != nil && jsonKey == "relaxedPlan" {
			h.Service.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "relaxed_plan_changed", "narratorId": narratorID, "relaxedPlan": boolValue})
		}
		return h.ok(w, r)
	}
}
func (h *Handler) updateReflectionOverrides(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	narratorID := chi.URLParam(r, "id")
	_, err := h.Service.DB.ExecContext(r.Context(), `UPDATE narrators SET plan_reflection_auto_approve_override = COALESCE(?, plan_reflection_auto_approve_override), danger_reflection_override = COALESCE(?, danger_reflection_override), updated_at = ? WHERE id = ?`, body["planReflectionAutoApproveOverride"], body["dangerReflectionOverride"], db.NowISO(), narratorID)
	if err != nil {
		return err
	}
	if h.Service.Hub != nil {
		payload := map[string]any{"type": "reflection_overrides_changed", "narratorId": narratorID}
		if _, ok := body["planReflectionAutoApproveOverride"]; ok {
			payload["planReflectionAutoApproveOverride"] = body["planReflectionAutoApproveOverride"]
		}
		if _, ok := body["dangerReflectionOverride"]; ok {
			payload["dangerReflectionOverride"] = body["dangerReflectionOverride"]
		}
		h.Service.Hub.BroadcastNarrator(narratorID, payload)
	}
	return h.ok(w, r)
}

func findByID(items []map[string]any, idValue string) map[string]any {
	for _, item := range items {
		if item["id"] == idValue {
			return item
		}
	}
	return nil
}
func nullString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}
func nullInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}
func nullableStringValue(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func rowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		item := map[string]any{}
		for i, col := range cols {
			if col == "enabled" {
				item[snakeToCamel(col)] = normalize(values[i]) != int64(0)
			} else {
				item[snakeToCamel(col)] = normalize(values[i])
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func normalize(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case int64:
		return x
	default:
		return x
	}
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

const maxMessageImageBytes = 20 * 1024 * 1024
const maxMessageTextFileBytes = 100 * 1024 * 1024

type parsedMessagePayload struct {
	ContentText    string
	RawText        string
	AttachmentHint string
	ContentBlocks  []map[string]any
	Priority       bool
}

type savedTextFile struct {
	Filename string
	FilePath string
	Size     int64
}

func (h *Handler) parseMessagePayload(r *http.Request, narratorID string) (parsedMessagePayload, error) {
	contentType := r.Header.Get("Content-Type")
	if !strings.Contains(contentType, "multipart/form-data") {
		message, priority, err := parseMessageRequest(r)
		if err != nil {
			return parsedMessagePayload{}, err
		}
		return parsedMessagePayload{ContentText: message, RawText: message, ContentBlocks: []map[string]any{{"type": "text", "text": message}}, Priority: priority}, nil
	}
	if err := r.ParseMultipartForm(140 << 20); err != nil {
		return parsedMessagePayload{}, httpx.BadRequest("invalid multipart form")
	}
	message := r.FormValue("message")
	blocks := []map[string]any{}
	imageHeaders := r.MultipartForm.File["images"]
	if len(imageHeaders) > 10 {
		return parsedMessagePayload{}, httpx.BadRequest("Maximum 10 images per message")
	}
	for _, header := range imageHeaders {
		block, err := saveMultipartImage(narratorID, header)
		if err != nil {
			return parsedMessagePayload{}, err
		}
		blocks = append(blocks, block)
	}
	savedTextFiles := []savedTextFile{}
	textFileHeaders := r.MultipartForm.File["textFiles"]
	if len(textFileHeaders) > 10 {
		return parsedMessagePayload{}, httpx.BadRequest("Maximum 10 text files per message")
	}
	if len(textFileHeaders) > 0 {
		narrator, err := h.Service.Get(r.Context(), narratorID)
		if err != nil {
			return parsedMessagePayload{}, err
		}
		cwd := ""
		if narrator.Cwd != nil {
			cwd = *narrator.Cwd
		}
		if strings.TrimSpace(cwd) == "" {
			return parsedMessagePayload{}, httpx.BadRequest("narrator cwd is required for text file attachments")
		}
		for _, header := range textFileHeaders {
			saved, err := saveMultipartTextFile(cwd, header)
			if err != nil {
				return parsedMessagePayload{}, err
			}
			savedTextFiles = append(savedTextFiles, saved)
			blocks = append(blocks, map[string]any{"type": "text_file", "filename": saved.Filename, "size": saved.Size, "filePath": saved.FilePath})
		}
	}
	blocks = append(blocks, map[string]any{"type": "text", "text": message})
	effectiveText := message
	if strings.TrimSpace(effectiveText) == "" && len(imageHeaders) > 0 {
		effectiveText = "[user sent image(s)]"
	}
	attachmentHint := attachedFilesHint(savedTextFiles)
	return parsedMessagePayload{ContentText: effectiveText + attachmentHint, RawText: message, AttachmentHint: attachmentHint, ContentBlocks: blocks, Priority: r.FormValue("priority") == "true"}, nil
}

func saveMultipartImage(narratorID string, header *multipart.FileHeader) (map[string]any, error) {
	if header == nil {
		return nil, httpx.BadRequest("invalid image upload")
	}
	if header.Size > maxMessageImageBytes {
		return nil, httpx.BadRequest("image too large")
	}
	mediaType := header.Header.Get("Content-Type")
	if !isAllowedImageType(mediaType) {
		file, err := header.Open()
		if err != nil {
			return nil, err
		}
		probe := make([]byte, 512)
		n, _ := file.Read(probe)
		_ = file.Close()
		mediaType = http.DetectContentType(probe[:n])
	}
	if !isAllowedImageType(mediaType) {
		return nil, httpx.BadRequest("Unsupported image type")
	}
	if !safePathSegment(narratorID) {
		return nil, httpx.BadRequest("invalid narrator ID")
	}
	imageID := id.Must(8)
	dir := filepath.Join(narratorUploadsRoot(), narratorID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	filename := sanitizeFilename(header.Filename)
	ext := imageExt(mediaType, filepath.Ext(filename))
	filePath := filepath.Join(dir, imageID+ext)
	if err := copyMultipartFile(header, filePath, maxMessageImageBytes); err != nil {
		return nil, err
	}
	return map[string]any{"type": "image", "imageId": imageID, "filename": filename, "mediaType": mediaType}, nil
}

func cleanupUploadedTextFileBlocks(blocks []map[string]any) {
	for _, block := range blocks {
		if block["type"] != "text_file" {
			continue
		}
		filePath := strings.TrimSpace(stringFromAny(block["filePath"]))
		if filePath == "" || !isNarraForkAttachedPath(filePath) {
			continue
		}
		_ = os.Remove(filePath)
	}
}

func isNarraForkAttachedPath(path string) bool {
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	return filepath.Base(dir) == "attached" && filepath.Base(filepath.Dir(dir)) == ".narrafork"
}

func saveMultipartTextFile(cwd string, header *multipart.FileHeader) (savedTextFile, error) {
	if header == nil {
		return savedTextFile{}, httpx.BadRequest("invalid text file upload")
	}
	if header.Size > maxMessageTextFileBytes {
		return savedTextFile{}, httpx.BadRequest("text file too large")
	}
	filename := sanitizeFilename(header.Filename)
	if filename == "" {
		filename = "attachment.txt"
	}
	dir := filepath.Join(cwd, ".narrafork", "attached")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return savedTextFile{}, err
	}
	filePath := filepath.Join(dir, filename)
	if _, err := os.Stat(filePath); err == nil {
		ext := filepath.Ext(filename)
		base := strings.TrimSuffix(filename, ext)
		filePath = filepath.Join(dir, base+"_"+id.Must(8)+ext)
	}
	if err := copyMultipartFile(header, filePath, maxMessageTextFileBytes); err != nil {
		return savedTextFile{}, err
	}
	return savedTextFile{Filename: filename, FilePath: filePath, Size: header.Size}, nil
}

func copyMultipartFile(header *multipart.FileHeader, dst string, limit int64) error {
	src, err := header.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, io.LimitReader(src, limit+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}
	if written > limit {
		_ = os.Remove(dst)
		return httpx.BadRequest("uploaded file is too large")
	}
	return nil
}

func attachedFilesHint(files []savedTextFile) string {
	if len(files) == 0 {
		return ""
	}
	lines := []string{}
	for _, file := range files {
		lines = append(lines, "- "+file.FilePath+" ("+file.Filename+", "+formatBytes(file.Size)+")")
	}
	return "\n\n<attached_files>\nThe user has attached the following files for your reference. Use the Read tool to access their contents when needed.\n" + strings.Join(lines, "\n") + "\n</attached_files>"
}

func isAllowedImageType(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func imageExt(mediaType, fallback string) string {
	switch mediaType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	if fallback == "" {
		return ".bin"
	}
	return fallback
}

func sanitizeFilename(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		default:
			return r
		}
	}, value)
	return value
}

func safePathSegment(value string) bool {
	return value != "" && !strings.ContainsAny(value, `/\\`) && value != "." && value != ".."
}

func narratorUploadsRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".narrafork", "uploads")
}

func formatBytes(size int64) string {
	const unit = int64(1024)
	if size < unit {
		return strconv.FormatInt(size, 10) + " B"
	}
	value := float64(size)
	units := []string{"KB", "MB", "GB"}
	for _, suffix := range units {
		value = value / float64(unit)
		if value < float64(unit) || suffix == "GB" {
			return strconv.FormatFloat(value, 'f', 1, 64) + " " + suffix
		}
	}
	return strconv.FormatInt(size, 10) + " B"
}

func parseMessageRequest(r *http.Request) (string, bool, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return "", false, httpx.BadRequest("invalid multipart form")
		}
		return r.FormValue("message"), r.FormValue("priority") == "true", nil
	}
	if strings.Contains(contentType, "text/plain") {
		data, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
		if err != nil {
			return "", false, err
		}
		return string(data), false, nil
	}
	var req messageRequest
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return "", false, err
	}
	if req.Message != "" {
		return req.Message, req.Priority, nil
	}
	return req.Text, req.Priority, nil
}

func parseMessage(r *http.Request) (string, error) {
	message, _, err := parseMessageRequest(r)
	return message, err
}
