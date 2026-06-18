package userprefs

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/jsondb"
)

type UserBroadcaster interface {
	BroadcastUser(userID string, payload any)
}

type recentTabsPresenceProvider interface {
	NarratorPresenceBatch(narratorIDs []string) map[string][]map[string]any
}

type Handler struct {
	DB          *sql.DB
	Broadcaster UserBroadcaster
}

func NewHandler(sqlDB *sql.DB, broadcasters ...UserBroadcaster) *Handler {
	var broadcaster UserBroadcaster
	if len(broadcasters) > 0 {
		broadcaster = broadcasters[0]
	}
	return &Handler{DB: sqlDB, Broadcaster: broadcaster}
}

const maxRecentTabs = 20

var containerStatusPriority = map[string]int{
	"removed": 0,
	"stopped": 1,
	"created": 2,
	"paused":  3,
	"running": 4,
}

var recentTabsLocks sync.Map

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(h.get))
	r.Patch("/", httpx.Handler(h.patch))
	r.Put("/recent-tabs", httpx.Handler(h.upsertRecentTab))
	r.Delete("/recent-tabs/{type}/{id}", httpx.Handler(h.removeRecentTab))
	r.Patch("/recent-tabs/move", httpx.Handler(h.moveRecentTab))
	r.Patch("/recent-tabs/pin", httpx.Handler(h.pinRecentTab))
	r.Post("/recent-tabs/clear", httpx.Handler(h.clearRecentTabs))
	r.Patch("/graph-viewports", httpx.Handler(h.saveGraphViewport))
}

var boolFields = map[string]bool{
	"auto_load_older_messages": true, "fast_mode_default": true, "word_wrap_markdown": true,
	"word_wrap_code": true, "word_wrap_diff": true, "reply_in_user_language": true,
	"show_token_usage": true, "show_output_stats": true, "add_subagent_to_recent_tabs": true,
	"notify_on_done": true, "notify_on_waiting": true, "notify_pwa_enabled": true,
	"notify_sound_enabled": true, "notify_dingtalk_enabled": true, "notify_feishu_enabled": true,
	"setup_wizard_completed": true,
}

var prefFieldMap = map[string]string{
	"autoLoadOlderMessages":   "auto_load_older_messages",
	"fastModeDefault":         "fast_mode_default",
	"language":                "language",
	"wordWrapMarkdown":        "word_wrap_markdown",
	"wordWrapCode":            "word_wrap_code",
	"wordWrapDiff":            "word_wrap_diff",
	"replyInUserLanguage":     "reply_in_user_language",
	"showTokenUsage":          "show_token_usage",
	"showOutputStats":         "show_output_stats",
	"terminalTheme":           "terminal_theme",
	"terminalFontSize":        "terminal_font_size",
	"addSubagentToRecentTabs": "add_subagent_to_recent_tabs",
	"notifyOnDone":            "notify_on_done",
	"notifyOnWaiting":         "notify_on_waiting",
	"notifyPwaEnabled":        "notify_pwa_enabled",
	"notifySoundEnabled":      "notify_sound_enabled",
	"notifySoundType":         "notify_sound_type",
	"notifySoundBuiltin":      "notify_sound_builtin",
	"notifySoundFileId":       "notify_sound_file_id",
	"notifyDingtalkEnabled":   "notify_dingtalk_enabled",
	"notifyDingtalkWebhook":   "notify_dingtalk_webhook",
	"notifyDingtalkSecret":    "notify_dingtalk_secret",
	"notifyFeishuEnabled":     "notify_feishu_enabled",
	"notifyFeishuWebhook":     "notify_feishu_webhook",
	"notifyFeishuSecret":      "notify_feishu_secret",
	"commands":                "commands",
	"sendMode":                "send_mode",
	"setupWizardCompleted":    "setup_wizard_completed",
	"gatewayConfig":           "gateway_config",
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	if err := h.ensure(r, userID); err != nil {
		return err
	}
	pref, err := h.load(r, userID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, pref)
	return nil
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	if err := h.ensure(r, userID); err != nil {
		return err
	}
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if err := validatePreferencePatch(body); err != nil {
		return err
	}
	if err := h.preserveMaskedSecrets(r, userID, body); err != nil {
		return err
	}
	sets := []string{}
	args := []any{}
	for key, value := range body {
		col, ok := prefFieldMap[key]
		if !ok {
			continue
		}
		sets = append(sets, col+" = ?")
		args = append(args, encodeValue(col, value))
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, db.NowISO(), userID)
		_, err := h.DB.ExecContext(r.Context(), `UPDATE user_preferences SET `+strings.Join(sets, ", ")+` WHERE user_id = ?`, args...)
		if err != nil {
			return err
		}
	}
	pref, err := h.load(r, userID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, pref)
	return nil
}

func (h *Handler) upsertRecentTab(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var rawTab map[string]any
	if err := httpx.DecodeLooseJSON(r, &rawTab); err != nil {
		return err
	}
	tab, updateOnly, err := validateUpsertRecentTab(rawTab)
	if err != nil {
		return err
	}
	tabs, err := h.mutateRecentTabs(r, userID, func(tabs []map[string]any) ([]map[string]any, error) {
		idx := indexByKey(tabs, tabKey(tab))
		if idx < 0 && updateOnly && stringValue(tab["type"]) == "narrator" {
			narratorID := stringValue(tab["id"])
			idx = indexByRepresentedNarrator(tabs, narratorID)
		}
		if idx >= 0 {
			if updateOnly {
				patch := map[string]any{}
				for key, value := range tab {
					if key == "type" || key == "id" {
						continue
					}
					if text, ok := value.(string); ok && text == "" {
						continue
					}
					patch[key] = value
				}
				mergeMapInPlace(tabs[idx], patch)
			} else {
				// Tab already exists: merge fields in place but KEEP its current
				// position, mirroring TS (server/routes/user-preferences.ts):
				// "Re-visiting an existing tab must not reorder the list - only
				// manual moves/pins change order. Only brand-new tabs are inserted
				// at the top." Previously Go promoted the tab to the front of the
				// unpinned section here, which reordered on every revisit.
				mergeMapInPlace(tabs[idx], tab)
			}
		} else if updateOnly {
			return tabs, nil
		} else if wsID := stringValue(tab["workspaceId"]); wsID != "" {
			headerIdx := indexByKey(tabs, "workspace:"+wsID)
			if headerIdx >= 0 {
				insertIdx := headerIdx + 1
				for insertIdx < len(tabs) && stringValue(tabs[insertIdx]["workspaceId"]) == wsID {
					insertIdx++
				}
				tabs = insertTabs(tabs, insertIdx, []map[string]any{tab})
			} else {
				delete(tab, "workspaceId")
				tabs = insertTopLevelTabRespectingPins(tabs, tab)
			}
		} else {
			tabs = insertTopLevelTabRespectingPins(tabs, tab)
		}
		if len(tabs) > maxRecentTabs {
			tabs = trimRecentTabs(tabs, maxRecentTabs)
		}
		tabs = regroupWorkspaces(tabs)
		return tabs, nil
	})
	if err != nil {
		return err
	}
	h.writeRecentTabs(r, w, http.StatusOK, userID, tabs)
	return nil
}

func (h *Handler) removeRecentTab(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	tabType := chi.URLParam(r, "type")
	tabID := chi.URLParam(r, "id")
	if err := validateRemoveRecentTab(tabType, tabID); err != nil {
		return err
	}
	tabs, err := h.mutateRecentTabs(r, userID, func(tabs []map[string]any) ([]map[string]any, error) {
		if tabType == "workspace" {
			for _, tab := range tabs {
				if stringValue(tab["workspaceId"]) == tabID {
					delete(tab, "workspaceId")
				}
			}
			if err := h.deleteWorkspaceRecord(r.Context(), tabID); err != nil {
				return nil, err
			}
		}
		key := tabType + ":" + tabID
		filtered := make([]map[string]any, 0, len(tabs))
		for _, tab := range tabs {
			if tabKey(tab) != key {
				filtered = append(filtered, tab)
			}
		}
		return filtered, nil
	})
	if err != nil {
		return err
	}
	h.writeRecentTabs(r, w, http.StatusOK, userID, tabs)
	return nil
}

func (h *Handler) moveRecentTab(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body struct {
		Key      string `json:"key"`
		ToIndex  *int   `json:"toIndex"`
		Position string `json:"position"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if err := validateMoveRecentTab(body.Key, body.ToIndex, body.Position); err != nil {
		return err
	}
	tabs, err := h.mutateRecentTabs(r, userID, func(tabs []map[string]any) ([]map[string]any, error) {
		idx := indexByKey(tabs, body.Key)
		if idx < 0 {
			return tabs, nil
		}
		movedGroup, remaining := extractMovableGroup(tabs, idx)
		tabs = remaining
		switch body.Position {
		case "top":
			tabs = insertTabs(tabs, 0, movedGroup)
		case "above_idle":
			if wsID := stringValue(movedGroup[0]["workspaceId"]); wsID != "" && len(movedGroup) == 1 {
				putBackAt := clampInt(idx, 0, len(tabs))
				tabs = insertTabs(tabs, putBackAt, movedGroup)
				if headerIdx := indexByKey(tabs, "workspace:"+wsID); headerIdx >= 0 {
					movedGroup, tabs = extractMovableGroup(tabs, headerIdx)
				} else {
					// Header missing: the child was already put back at putBackAt above.
					// Clear movedGroup so the early-return below leaves the list as-is.
					movedGroup = nil
				}
			}
			if len(movedGroup) == 0 {
				return tabs, nil
			}
			statusMap := h.narratorStatusMap(r.Context(), tabNarratorIDs(tabs))
			firstIdleIdx := firstIdleTopLevelIndex(tabs, statusMap)
			if firstIdleIdx < 0 {
				tabs = append(tabs, movedGroup...)
			} else {
				tabs = insertTabs(tabs, firstIdleIdx, movedGroup)
			}
		default:
			if body.ToIndex != nil {
				tabs = insertTabs(tabs, clampInt(*body.ToIndex, 0, len(tabs)), movedGroup)
			}
		}
		tabs = regroupWorkspaces(tabs)
		return tabs, nil
	})
	if err != nil {
		return err
	}
	h.writeRecentTabs(r, w, http.StatusOK, userID, tabs)
	return nil
}

func (h *Handler) pinRecentTab(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := httpx.DecodeJSON(r, &raw); err != nil {
		return err
	}
	key, _ := raw["key"].(string)
	if strings.TrimSpace(key) == "" || httpx.JSStringLength(key) > 100 {
		return httpx.ValidationError("key is required")
	}
	// Mirror TS pinRecentTabSchema `pinned: z.boolean()` (required): a missing or
	// non-boolean value must reject, not silently decode to false (which would
	// UNPIN the tab).
	pinned, ok := raw["pinned"].(bool)
	if !ok {
		return httpx.ValidationError("pinned must be a boolean")
	}
	tabs, err := h.mutateRecentTabs(r, userID, func(tabs []map[string]any) ([]map[string]any, error) {
		idx := indexByKey(tabs, key)
		if idx < 0 {
			return tabs, nil
		}
		if pinned {
			tabs[idx]["pinned"] = true
		} else {
			delete(tabs[idx], "pinned")
		}
		tab := tabs[idx]
		tabs = append(tabs[:idx], tabs[idx+1:]...)
		insertIdx := 0
		for insertIdx < len(tabs) && boolValue(tabs[insertIdx]["pinned"]) {
			insertIdx++
		}
		tabs = insertTabs(tabs, insertIdx, []map[string]any{tab})
		tabs = regroupWorkspaces(tabs)
		return tabs, nil
	})
	if err != nil {
		return err
	}
	h.writeRecentTabs(r, w, http.StatusOK, userID, tabs)
	return nil
}

func (h *Handler) clearRecentTabs(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body struct {
		Scope      string `json:"scope"`
		KeepTabKey string `json:"keepTabKey"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if body.Scope != "all" && body.Scope != "projects" && body.Scope != "inactive_narrators" {
		return httpx.ValidationError("scope is not supported")
	}
	tabs, err := h.mutateRecentTabs(r, userID, func(tabs []map[string]any) ([]map[string]any, error) {
		isKept := func(tab map[string]any) bool {
			return body.KeepTabKey != "" && tabKey(tab) == body.KeepTabKey
		}
		if body.Scope == "all" {
			if body.KeepTabKey == "" {
				return []map[string]any{}, nil
			}
			kept := []map[string]any{}
			for _, tab := range tabs {
				if isKept(tab) {
					kept = append(kept, tab)
				}
			}
			return kept, nil
		}
		if body.Scope == "projects" {
			filtered := make([]map[string]any, 0, len(tabs))
			for _, tab := range tabs {
				if stringValue(tab["type"]) != "project" || isKept(tab) {
					filtered = append(filtered, tab)
				}
			}
			return filtered, nil
		}

		statusMap := h.narratorInfoMap(r.Context(), tabNarratorIDs(tabs))
		childrenByWorkspace := map[string][]map[string]any{}
		workspaceIDs := map[string]struct{}{}
		for _, tab := range tabs {
			if stringValue(tab["type"]) == "workspace" {
				workspaceIDs[stringValue(tab["id"])] = struct{}{}
			}
			if wsID := stringValue(tab["workspaceId"]); wsID != "" {
				childrenByWorkspace[wsID] = append(childrenByWorkspace[wsID], tab)
			}
		}
		activeWorkspaces := map[string]struct{}{}
		for wsID := range workspaceIDs {
			for _, child := range childrenByWorkspace[wsID] {
				if narratorInfoActive(statusMap[getTabNarratorID(child)]) {
					activeWorkspaces[wsID] = struct{}{}
					break
				}
			}
		}
		for wsID := range workspaceIDs {
			if _, active := activeWorkspaces[wsID]; active || body.KeepTabKey == "workspace:"+wsID {
				continue
			}
			if err := h.deleteWorkspaceRecord(r.Context(), wsID); err != nil {
				return nil, err
			}
		}
		filtered := make([]map[string]any, 0, len(tabs))
		for _, tab := range tabs {
			if isKept(tab) || stringValue(tab["type"]) == "project" {
				filtered = append(filtered, tab)
				continue
			}
			if stringValue(tab["type"]) == "workspace" {
				if _, ok := activeWorkspaces[stringValue(tab["id"])]; ok {
					filtered = append(filtered, tab)
				}
				continue
			}
			if wsID := stringValue(tab["workspaceId"]); wsID != "" {
				if _, ok := activeWorkspaces[wsID]; ok {
					filtered = append(filtered, tab)
				}
				continue
			}
			if narratorInfoActive(statusMap[getTabNarratorID(tab)]) {
				filtered = append(filtered, tab)
			}
		}
		filtered = regroupWorkspaces(filtered)
		return filtered, nil
	})
	if err != nil {
		return err
	}
	h.writeRecentTabs(r, w, http.StatusOK, userID, tabs)
	return nil
}

func (h *Handler) saveGraphViewport(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body struct {
		ProjectID string         `json:"projectId"`
		Viewport  map[string]any `json:"viewport"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.ProjectID == "" {
		return httpx.BadRequest("projectId is required")
	}
	pref, err := h.load(r, userID)
	if err != nil {
		return err
	}
	viewports := decodeObject(pref["graphViewports"])
	if viewports == nil {
		viewports = map[string]any{}
	}
	viewports[body.ProjectID] = body.Viewport
	data, _ := json.Marshal(viewports)
	_, err = h.DB.ExecContext(r.Context(), `UPDATE user_preferences SET graph_viewports = ?, updated_at = ? WHERE user_id = ?`, string(data), db.NowISO(), userID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) ensure(r *http.Request, userID string) error {
	now := db.NowISO()
	_, err := h.DB.ExecContext(r.Context(), `INSERT OR IGNORE INTO user_preferences (id, user_id, created_at, updated_at) VALUES (?, ?, ?, ?)`, id.New(), userID, now, now)
	return err
}

func (h *Handler) load(r *http.Request, userID string) (map[string]any, error) {
	out, err := h.loadRaw(r, userID)
	if err != nil {
		return nil, err
	}
	if tabs := recentTabsFromAny(out["recentTabs"]); len(tabs) > 0 {
		out["recentTabs"] = tabsToAny(h.enrichTabs(r.Context(), cloneTabs(tabs)))
	}
	redactPreferenceSecrets(out)
	return out, nil
}

func (h *Handler) loadRaw(r *http.Request, userID string) (map[string]any, error) {
	if err := h.ensure(r, userID); err != nil {
		return nil, err
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT * FROM user_preferences WHERE user_id = ? LIMIT 1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return map[string]any{}, nil
	}
	cols, _ := rows.Columns()
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for i, col := range cols {
		key := snakeToCamel(col)
		val := normalizeValue(values[i])
		if boolFields[col] {
			val = toBool(values[i])
		}
		out[key] = val
	}
	out["recentTabs"] = decodeArray(out["recentTabs"])
	out["commands"] = decodeArray(out["commands"])
	out["gatewayConfig"] = decodeObject(out["gatewayConfig"])
	return out, nil
}

var preferenceSecretFields = []string{
	"notifyDingtalkWebhook",
	"notifyDingtalkSecret",
	"notifyFeishuWebhook",
	"notifyFeishuSecret",
}

var preferenceWebhookFields = map[string]bool{
	"notifyDingtalkWebhook": true,
	"notifyFeishuWebhook":   true,
}

var commandNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var gatewayPlatformSecretFields = []string{"token", "botToken", "appToken", "appSecret", "secret", "clientSecret"}

func validateUpsertRecentTab(tab map[string]any) (map[string]any, bool, error) {
	clean := map[string]any{}
	tabType, err := requiredStringField(tab, "type", "recentTab", 1, 50)
	if err != nil {
		return nil, false, err
	}
	switch tabType {
	case "chapter", "narrator", "project", "workspace", "subagent":
		clean["type"] = tabType
	default:
		return nil, false, httpx.ValidationError("recentTab.type is not supported")
	}
	idValue, err := requiredStringField(tab, "id", "recentTab", 1, 50)
	if err != nil {
		return nil, false, err
	}
	clean["id"] = idValue
	if err := optionalStringField(tab, "narratorId", "recentTab", 1, 50); err != nil {
		return nil, false, err
	}
	if value, ok := tab["narratorId"]; ok {
		clean["narratorId"] = value
	}
	if err := optionalStringField(tab, "parentNarratorId", "recentTab", 1, 50); err != nil {
		return nil, false, err
	}
	if value, ok := tab["parentNarratorId"]; ok {
		clean["parentNarratorId"] = value
	}
	if value, ok := tab["workspaceId"]; ok {
		if value != nil {
			if err := optionalStringField(tab, "workspaceId", "recentTab", 1, 50); err != nil {
				return nil, false, err
			}
		}
		clean["workspaceId"] = value
	}
	title, err := requiredStringField(tab, "title", "recentTab", 0, 1000)
	if err != nil {
		return nil, false, err
	}
	clean["title"] = title
	if err := optionalStringField(tab, "subtitle", "recentTab", 0, 1000); err != nil {
		return nil, false, err
	}
	if value, ok := tab["subtitle"]; ok {
		clean["subtitle"] = value
	}
	if err := optionalStringField(tab, "status", "recentTab", 0, 50); err != nil {
		return nil, false, err
	}
	if value, ok := tab["status"]; ok {
		clean["status"] = value
	}
	lastVisitedAt, ok := tab["lastVisitedAt"].(float64)
	if !ok {
		return nil, false, httpx.ValidationError("recentTab.lastVisitedAt must be a number")
	}
	clean["lastVisitedAt"] = lastVisitedAt
	if err := optionalBoolField(tab, "pinned", "recentTab"); err != nil {
		return nil, false, err
	}
	if value, ok := tab["pinned"]; ok {
		clean["pinned"] = value
	}
	if err := optionalBoolField(tab, "updateOnly", "recentTab"); err != nil {
		return nil, false, err
	}
	updateOnly, _ := tab["updateOnly"].(bool)
	return clean, updateOnly, nil
}

func validatePreferencePatch(patch map[string]any) error {
	for key, value := range patch {
		column, ok := prefFieldMap[key]
		if ok && boolFields[column] {
			if _, valid := value.(bool); !valid {
				return httpx.ValidationError(key + " must be a boolean")
			}
		}
	}
	if err := validateEnumPreference(patch, "language", []string{"en", "zh-CN"}); err != nil {
		return err
	}
	if err := validateEnumPreference(patch, "sendMode", []string{"enter", "ctrl+enter"}); err != nil {
		return err
	}
	if err := validateEnumPreference(patch, "notifySoundType", []string{"builtin", "custom"}); err != nil {
		return err
	}
	if err := validateStringPreference(patch, "terminalTheme", 1, 50, false); err != nil {
		return err
	}
	if err := validateStringPreference(patch, "notifySoundBuiltin", 0, 50, false); err != nil {
		return err
	}
	if err := validateStringPreference(patch, "notifySoundFileId", 0, 50, true); err != nil {
		return err
	}
	if err := validateTerminalFontSize(patch); err != nil {
		return err
	}
	if err := validateCommandsPreference(patch); err != nil {
		return err
	}
	if err := validateGatewayConfigPreference(patch); err != nil {
		return err
	}
	for _, field := range preferenceSecretFields {
		value, ok := patch[field]
		if !ok {
			continue
		}
		text := stringValue(value)
		if jsStringLength(text) > 500 {
			return httpx.ValidationError(field + " must be at most 500 characters")
		}
		if preferenceWebhookFields[field] && text != "" && !strings.HasPrefix(text, "*") && !strings.HasPrefix(text, "https://") {
			return httpx.ValidationError(field + " must start with https://")
		}
	}
	return nil
}

func validateEnumPreference(patch map[string]any, key string, allowed []string) error {
	value, ok := patch[key]
	if !ok {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(key + " must be a string")
	}
	for _, item := range allowed {
		if text == item {
			return nil
		}
	}
	return httpx.ValidationError(key + " is not supported")
}

func validateStringPreference(patch map[string]any, key string, minLen int, maxLen int, nullable bool) error {
	value, ok := patch[key]
	if !ok {
		return nil
	}
	if value == nil && nullable {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(key + " must be a string")
	}
	if jsStringLength(text) < minLen {
		return httpx.ValidationError(key + " must not be empty")
	}
	if jsStringLength(text) > maxLen {
		return httpx.ValidationError(key + " must be at most " + strconv.Itoa(maxLen) + " characters")
	}
	return nil
}

func validateTerminalFontSize(patch map[string]any) error {
	value, ok := patch["terminalFontSize"]
	if !ok {
		return nil
	}
	number, ok := value.(float64)
	if !ok || number != math.Trunc(number) {
		return httpx.ValidationError("terminalFontSize must be an integer")
	}
	if number < 8 || number > 32 {
		return httpx.ValidationError("terminalFontSize must be between 8 and 32")
	}
	patch["terminalFontSize"] = int(number)
	return nil
}

func validateCommandsPreference(patch map[string]any) error {
	value, ok := patch["commands"]
	if !ok {
		return nil
	}
	commands, ok := value.([]any)
	if !ok {
		return httpx.ValidationError("commands must be an array")
	}
	if len(commands) > 100 {
		return httpx.ValidationError("commands must contain at most 100 items")
	}
	for i, item := range commands {
		command, ok := item.(map[string]any)
		if !ok {
			return httpx.ValidationError("commands[" + strconv.Itoa(i) + "] must be an object")
		}
		if err := validateCommandDefinition(command, "commands["+strconv.Itoa(i)+"]"); err != nil {
			return err
		}
	}
	return nil
}

func validateCommandDefinition(command map[string]any, path string) error {
	name, err := requiredStringField(command, "name", path, 1, 50)
	if err != nil {
		return err
	}
	if !commandNameRe.MatchString(name) {
		return httpx.ValidationError(path + ".name must contain only letters, numbers, underscores, or dashes")
	}
	if _, err := requiredStringField(command, "prompt", path, 1, 400000); err != nil {
		return err
	}
	if err := optionalStringField(command, "description", path, 0, 500); err != nil {
		return err
	}
	if err := optionalBoolField(command, "runBashFirst", path); err != nil {
		return err
	}
	if err := optionalStringField(command, "bashCommand", path, 0, 400000); err != nil {
		return err
	}
	if err := validateCommandParams(command, path); err != nil {
		return err
	}
	return validateCommandModelOverride(command, path)
}

func validateCommandParams(command map[string]any, path string) error {
	value, ok := command["params"]
	if !ok {
		return nil
	}
	params, ok := value.([]any)
	if !ok {
		return httpx.ValidationError(path + ".params must be an array")
	}
	if len(params) > 20 {
		return httpx.ValidationError(path + ".params must contain at most 20 items")
	}
	for i, item := range params {
		param, ok := item.(map[string]any)
		paramPath := path + ".params[" + strconv.Itoa(i) + "]"
		if !ok {
			return httpx.ValidationError(paramPath + " must be an object")
		}
		if _, err := requiredStringField(param, "name", paramPath, 1, 50); err != nil {
			return err
		}
		if err := optionalStringField(param, "description", paramPath, 0, 500); err != nil {
			return err
		}
		if err := optionalBoolField(param, "required", paramPath); err != nil {
			return err
		}
		if err := optionalStringField(param, "defaultValue", paramPath, 0, 1000); err != nil {
			return err
		}
	}
	return nil
}

func validateCommandModelOverride(command map[string]any, path string) error {
	value, ok := command["modelOverride"]
	if !ok {
		return nil
	}
	override, ok := value.(map[string]any)
	if !ok {
		return httpx.ValidationError(path + ".modelOverride must be an object")
	}
	if _, err := requiredStringField(override, "model", path+".modelOverride", 1, 100); err != nil {
		return err
	}
	mode, err := requiredStringField(override, "mode", path+".modelOverride", 1, 20)
	if err != nil {
		return err
	}
	if mode != "temporary" && mode != "permanent" {
		return httpx.ValidationError(path + ".modelOverride.mode is not supported")
	}
	return nil
}

func requiredStringField(item map[string]any, key string, path string, minLen int, maxLen int) (string, error) {
	value, ok := item[key]
	if !ok {
		return "", httpx.ValidationError(path + "." + key + " is required")
	}
	text, ok := value.(string)
	if !ok {
		return "", httpx.ValidationError(path + "." + key + " must be a string")
	}
	if jsStringLength(text) < minLen {
		return "", httpx.ValidationError(path + "." + key + " must not be empty")
	}
	if jsStringLength(text) > maxLen {
		return "", httpx.ValidationError(path + "." + key + " must be at most " + strconv.Itoa(maxLen) + " characters")
	}
	return text, nil
}

func optionalStringField(item map[string]any, key string, path string, minLen int, maxLen int) error {
	value, ok := item[key]
	if !ok {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	if jsStringLength(text) < minLen {
		return httpx.ValidationError(path + "." + key + " must not be empty")
	}
	if jsStringLength(text) > maxLen {
		return httpx.ValidationError(path + "." + key + " must be at most " + strconv.Itoa(maxLen) + " characters")
	}
	return nil
}

func optionalBoolField(item map[string]any, key string, path string) error {
	value, ok := item[key]
	if !ok {
		return nil
	}
	if _, ok := value.(bool); !ok {
		return httpx.ValidationError(path + "." + key + " must be a boolean")
	}
	return nil
}

func optionalIntRangeField(item map[string]any, key string, path string, minValue int, maxValue int) error {
	value, ok := item[key]
	if !ok {
		return nil
	}
	number, ok := value.(float64)
	if !ok || number != math.Trunc(number) {
		return httpx.ValidationError(path + "." + key + " must be an integer")
	}
	if number < float64(minValue) || number > float64(maxValue) {
		return httpx.ValidationError(path + "." + key + " must be between " + strconv.Itoa(minValue) + " and " + strconv.Itoa(maxValue))
	}
	item[key] = int(number)
	return nil
}

func optionalEnumField(item map[string]any, key string, path string, allowed []string) error {
	value, ok := item[key]
	if !ok {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be a string")
	}
	for _, allowedValue := range allowed {
		if text == allowedValue {
			return nil
		}
	}
	return httpx.ValidationError(path + "." + key + " is not supported")
}

func validateGatewayConfigPreference(patch map[string]any) error {
	value, ok := patch["gatewayConfig"]
	if !ok {
		return nil
	}
	gateway, ok := value.(map[string]any)
	if !ok {
		return httpx.ValidationError("gatewayConfig must be an object")
	}
	path := "gatewayConfig"
	if err := optionalBoolField(gateway, "enabled", path); err != nil {
		return err
	}
	if err := optionalStringField(gateway, "defaultProjectId", path, 0, 50); err != nil {
		return err
	}
	if err := optionalStringField(gateway, "defaultChapterId", path, 0, 50); err != nil {
		return err
	}
	if err := validateGatewayPermissionMode(gateway); err != nil {
		return err
	}
	if err := optionalIntRangeField(gateway, "sessionIdleMinutes", path, 0, 43200); err != nil {
		return err
	}
	if err := optionalIntRangeField(gateway, "rateLimitPerMinute", path, 0, 1000); err != nil {
		return err
	}
	if err := optionalBoolField(gateway, "streaming", path); err != nil {
		return err
	}
	platformsValue, ok := gateway["platforms"]
	if !ok {
		return nil
	}
	platforms, ok := platformsValue.([]any)
	if !ok {
		return httpx.ValidationError("gatewayConfig.platforms must be an array")
	}
	if len(platforms) > 10 {
		return httpx.ValidationError("gatewayConfig.platforms must contain at most 10 items")
	}
	for i, item := range platforms {
		platform, ok := item.(map[string]any)
		platformPath := "gatewayConfig.platforms[" + strconv.Itoa(i) + "]"
		if !ok {
			return httpx.ValidationError(platformPath + " must be an object")
		}
		if err := validateGatewayPlatformConfig(platform, platformPath); err != nil {
			return err
		}
	}
	return nil
}

func validateGatewayPermissionMode(gateway map[string]any) error {
	value, ok := gateway["defaultPermissionMode"]
	if !ok {
		return nil
	}
	mode, ok := value.(string)
	if !ok {
		return httpx.ValidationError("gatewayConfig.defaultPermissionMode must be a string")
	}
	switch mode {
	case "allowByDefault":
		gateway["defaultPermissionMode"] = "acceptEdits"
		return nil
	case "denyByDefault":
		gateway["defaultPermissionMode"] = "dontAsk"
		return nil
	case "plan":
		gateway["defaultPermissionMode"] = "default"
		return nil
	case "default", "acceptEdits", "bypassPermissions", "readOnly", "dontAsk":
		return nil
	default:
		return httpx.ValidationError("gatewayConfig.defaultPermissionMode is not supported")
	}
}

func validateGatewayPlatformConfig(platform map[string]any, path string) error {
	platformName, err := requiredStringField(platform, "platform", path, 1, 50)
	if err != nil {
		return err
	}
	allowedPlatforms := []string{"telegram", "discord", "slack", "feishu", "webhook", "weixin", "qqbot"}
	allowed := false
	for _, item := range allowedPlatforms {
		if platformName == item {
			allowed = true
			break
		}
	}
	if !allowed {
		return httpx.ValidationError(path + ".platform is not supported")
	}
	if err := optionalBoolField(platform, "enabled", path); err != nil {
		return err
	}
	if _, ok := platform["enabled"]; !ok {
		return httpx.ValidationError(path + ".enabled is required")
	}
	for _, field := range []string{"token", "botToken", "appToken", "appSecret", "secret", "clientSecret"} {
		if err := optionalStringField(platform, field, path, 0, 500); err != nil {
			return err
		}
	}
	for _, field := range []string{"appId", "accountId"} {
		if err := optionalStringField(platform, field, path, 0, 200); err != nil {
			return err
		}
	}
	if err := optionalStringField(platform, "baseUrl", path, 0, 500); err != nil {
		return err
	}
	if err := optionalEnumField(platform, "dmPolicy", path, []string{"open", "allowlist", "disabled"}); err != nil {
		return err
	}
	if err := optionalEnumField(platform, "groupPolicy", path, []string{"open", "allowlist", "disabled"}); err != nil {
		return err
	}
	if err := optionalBoolField(platform, "markdownSupport", path); err != nil {
		return err
	}
	if err := optionalBoolField(platform, "sandbox", path); err != nil {
		return err
	}
	if err := validateGatewayStringArray(platform, "allowedGroups", path); err != nil {
		return err
	}
	if err := validateGatewayStringArray(platform, "allowedUsers", path); err != nil {
		return err
	}
	return validateGatewaySTT(platform, path)
}

func validateGatewayStringArray(platform map[string]any, key string, path string) error {
	value, ok := platform[key]
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return httpx.ValidationError(path + "." + key + " must be an array")
	}
	if len(items) > 50 {
		return httpx.ValidationError(path + "." + key + " must contain at most 50 items")
	}
	for i, item := range items {
		text, ok := item.(string)
		itemPath := path + "." + key + "[" + strconv.Itoa(i) + "]"
		if !ok {
			return httpx.ValidationError(itemPath + " must be a string")
		}
		if jsStringLength(text) > 100 {
			return httpx.ValidationError(itemPath + " must be at most 100 characters")
		}
	}
	return nil
}

func validateGatewaySTT(platform map[string]any, path string) error {
	value, ok := platform["stt"]
	if !ok {
		return nil
	}
	stt, ok := value.(map[string]any)
	if !ok {
		return httpx.ValidationError(path + ".stt must be an object")
	}
	sttPath := path + ".stt"
	if _, err := requiredStringField(stt, "apiKey", sttPath, 0, 500); err != nil {
		return err
	}
	if err := optionalStringField(stt, "baseUrl", sttPath, 0, 500); err != nil {
		return err
	}
	return optionalStringField(stt, "model", sttPath, 0, 100)
}

func (h *Handler) preserveMaskedSecrets(r *http.Request, userID string, patch map[string]any) error {
	if !patchContainsMaskedSecret(patch) {
		return nil
	}
	current, err := h.loadRaw(r, userID)
	if err != nil {
		return err
	}
	for _, field := range preferenceSecretFields {
		if value, ok := patch[field]; ok && isMaskedSecret(value) {
			patch[field] = stringValue(current[field])
		}
	}
	incomingGateway, ok := patch["gatewayConfig"].(map[string]any)
	if !ok {
		return nil
	}
	currentGateway, _ := current["gatewayConfig"].(map[string]any)
	preserveGatewayPlatformSecrets(currentGateway, incomingGateway)
	return nil
}

func patchContainsMaskedSecret(patch map[string]any) bool {
	for _, field := range preferenceSecretFields {
		if value, ok := patch[field]; ok && isMaskedSecret(value) {
			return true
		}
	}
	gateway, ok := patch["gatewayConfig"].(map[string]any)
	return ok && gatewayConfigHasMaskedSecrets(gateway)
}

func redactPreferenceSecrets(pref map[string]any) {
	for _, field := range preferenceSecretFields {
		if _, ok := pref[field]; ok {
			pref[field] = maskSecret(stringValue(pref[field]))
		}
	}
	gateway, ok := pref["gatewayConfig"].(map[string]any)
	if ok {
		maskGatewayPlatformSecrets(gateway)
	}
}

func gatewayConfigHasMaskedSecrets(gateway map[string]any) bool {
	for _, platform := range gatewayPlatformMaps(gateway) {
		for _, field := range gatewayPlatformSecretFields {
			if isMaskedSecret(platform[field]) {
				return true
			}
		}
	}
	return false
}

func maskGatewayPlatformSecrets(gateway map[string]any) {
	platforms := gatewayPlatformMaps(gateway)
	if platforms == nil {
		return
	}
	for _, platform := range platforms {
		for _, field := range gatewayPlatformSecretFields {
			platform[field] = maskSecret(stringValue(platform[field]))
		}
	}
}

func preserveGatewayPlatformSecrets(current, incoming map[string]any) {
	incomingPlatforms := gatewayPlatformMaps(incoming)
	if len(incomingPlatforms) == 0 {
		return
	}
	currentByPlatform := map[string]map[string]any{}
	for _, platform := range gatewayPlatformMaps(current) {
		if key := stringValue(platform["platform"]); key != "" {
			currentByPlatform[key] = platform
		}
	}
	for _, platform := range incomingPlatforms {
		original := currentByPlatform[stringValue(platform["platform"])]
		for _, field := range gatewayPlatformSecretFields {
			if isMaskedSecret(platform[field]) {
				platform[field] = stringValue(original[field])
			}
		}
	}
}

func gatewayPlatformMaps(gateway map[string]any) []map[string]any {
	if gateway == nil {
		return nil
	}
	items, ok := gateway["platforms"].([]any)
	if !ok {
		return nil
	}
	platforms := make([]map[string]any, 0, len(items))
	for _, item := range items {
		platform, ok := item.(map[string]any)
		if ok {
			platforms = append(platforms, platform)
		}
	}
	return platforms
}

func isMaskedSecret(value any) bool {
	return strings.HasPrefix(stringValue(value), "*")
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	// Mirror the TS user-preferences maskSecret (server/routes/user-preferences.ts):
	// `val.length<=4 -> "*".repeat(val.length)`, else `"*".repeat(8)+val.slice(-4)`,
	// measured in UTF-16 code units. Go previously used byte length + a byte suffix
	// slice, which for a non-ASCII gateway token/secret would emit the wrong star
	// count and could cut mid-rune, producing invalid UTF-8 in the masked value.
	if jsStringLength(value) <= 4 {
		return strings.Repeat("*", jsStringLength(value))
	}
	return "********" + httpx.JSStringSliceFromEnd(value, 4)
}

func stringValue(value any) string {
	s, _ := value.(string)
	return s
}

// jsStringLength mirrors JavaScript String.prototype.length (and therefore the
// Zod .min()/.max() string-length checks used by the TS user-preferences route),
// which counts UTF-16 code units. utf8.RuneCountInString would be too permissive
// for astral characters (e.g. an emoji is 2 UTF-16 units in JS but 1 rune in Go),
// so input that TS rejects at a .max() boundary must also be rejected here.
func jsStringLength(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func (h *Handler) recentTabs(r *http.Request, userID string) ([]map[string]any, error) {
	pref, err := h.loadRaw(r, userID)
	if err != nil {
		return nil, err
	}
	return recentTabsFromAny(pref["recentTabs"]), nil
}

func (h *Handler) mutateRecentTabs(r *http.Request, userID string, mutate func([]map[string]any) ([]map[string]any, error)) ([]map[string]any, error) {
	lock := userRecentTabsLock(userID)
	lock.Lock()
	defer lock.Unlock()
	tabs, err := h.recentTabs(r, userID)
	if err != nil {
		return nil, err
	}
	migrateTabTypes(tabs)
	updated, err := mutate(tabs)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		updated = []map[string]any{}
	}
	if err := h.saveRecentTabs(r, userID, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func (h *Handler) saveRecentTabs(r *http.Request, userID string, tabs []map[string]any) error {
	if err := h.ensure(r, userID); err != nil {
		return err
	}
	data, _ := json.Marshal(tabs)
	_, err := h.DB.ExecContext(r.Context(), `UPDATE user_preferences SET recent_tabs = ?, updated_at = ? WHERE user_id = ?`, string(data), db.NowISO(), userID)
	return err
}

func (h *Handler) writeRecentTabs(r *http.Request, w http.ResponseWriter, status int, userID string, tabs []map[string]any) {
	enriched := h.enrichTabs(r.Context(), cloneTabs(tabs))
	h.broadcastRecentTabsSnapshot(userID, enriched)
	httpx.WriteJSON(w, status, enriched)
}

func (h *Handler) broadcastRecentTabsSnapshot(userID string, tabs []map[string]any) {
	if h == nil || h.Broadcaster == nil || strings.TrimSpace(userID) == "" {
		return
	}
	h.Broadcaster.BroadcastUser(userID, map[string]any{
		"type":     "user:recent_tabs_snapshot",
		"tabs":     tabs,
		"revision": time.Now().UnixMilli(),
	})
}

func userRecentTabsLock(userID string) *sync.Mutex {
	key := strings.TrimSpace(userID)
	if key == "" {
		key = "__anonymous__"
	}
	lock, _ := recentTabsLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func recentTabsFromAny(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		if tabs, ok := value.([]map[string]any); ok {
			return cloneTabs(tabs)
		}
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if tab, ok := item.(map[string]any); ok {
			result = append(result, cloneMap(tab))
		}
	}
	return result
}

func cloneTabs(tabs []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(tabs))
	for _, tab := range tabs {
		out = append(out, cloneMap(tab))
	}
	return out
}

func tabsToAny(tabs []map[string]any) []any {
	out := make([]any, 0, len(tabs))
	for _, tab := range tabs {
		out = append(out, tab)
	}
	return out
}

func cloneMap(item map[string]any) map[string]any {
	out := make(map[string]any, len(item))
	for key, value := range item {
		out[key] = value
	}
	return out
}

func mergeMapInPlace(dst map[string]any, src map[string]any) {
	for key, value := range src {
		dst[key] = value
	}
}

func migrateTabTypes(tabs []map[string]any) {
	for _, tab := range tabs {
		if stringValue(tab["type"]) == "session" {
			tab["type"] = "narrator"
		}
	}
}

func getTabNarratorID(tab map[string]any) string {
	switch stringValue(tab["type"]) {
	case "narrator", "subagent":
		return stringValue(tab["id"])
	case "chapter":
		return stringValue(tab["narratorId"])
	default:
		return ""
	}
}

func indexByRepresentedNarrator(tabs []map[string]any, narratorID string) int {
	for i, tab := range tabs {
		if getTabNarratorID(tab) == narratorID {
			return i
		}
	}
	return -1
}

func tabNarratorIDs(tabs []map[string]any) []string {
	ids := make([]string, 0, len(tabs))
	seen := map[string]struct{}{}
	for _, tab := range tabs {
		id := getTabNarratorID(tab)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func regroupWorkspaces(tabs []map[string]any) []map[string]any {
	childrenByWorkspace := map[string][]map[string]any{}
	// orphanOrder records workspace IDs in the order their first child appears in
	// the tab list, matching the TS regroupWorkspaces, which appends orphan children
	// in childrenByWs Map insertion order (first-appearance order). Sorting the IDs
	// (e.g. alphabetically) would diverge whenever two or more workspaces are orphaned.
	orphanOrder := make([]string, 0)
	for _, tab := range tabs {
		if wsID := stringValue(tab["workspaceId"]); wsID != "" {
			if _, seen := childrenByWorkspace[wsID]; !seen {
				orphanOrder = append(orphanOrder, wsID)
			}
			childrenByWorkspace[wsID] = append(childrenByWorkspace[wsID], tab)
		}
	}
	if len(childrenByWorkspace) == 0 {
		return tabs
	}
	topLevel := make([]map[string]any, 0, len(tabs))
	for _, tab := range tabs {
		if stringValue(tab["workspaceId"]) == "" {
			topLevel = append(topLevel, tab)
		}
	}
	result := make([]map[string]any, 0, len(tabs))
	headerIDs := map[string]struct{}{}
	for _, tab := range topLevel {
		result = append(result, tab)
		if stringValue(tab["type"]) != "workspace" {
			continue
		}
		wsID := stringValue(tab["id"])
		headerIDs[wsID] = struct{}{}
		result = append(result, childrenByWorkspace[wsID]...)
	}
	for _, wsID := range orphanOrder {
		if _, ok := headerIDs[wsID]; ok {
			continue
		}
		for _, child := range childrenByWorkspace[wsID] {
			delete(child, "workspaceId")
			result = append(result, child)
		}
	}
	return result
}

// trimRecentTabs mirrors the TS trimRecentTabs (server/routes/user-preferences.ts):
// when the tab list exceeds max, it removes tabs from the END but pin/type-aware:
// first the last unpinned subagent tab, then the last unpinned tab, and only as a
// last resort (all remaining pinned) the very last tab. This preserves pinned
// tabs (and prefers dropping transient subagent tabs) instead of the prior naive
// tabs[:max] truncation, which could drop pinned tabs sitting at the tail.
func trimRecentTabs(tabs []map[string]any, max int) []map[string]any {
	if max < 0 {
		max = 0
	}
	if len(tabs) <= max {
		return tabs
	}
	result := append([]map[string]any{}, tabs...)
	removeFirstMatching := func(predicate func(map[string]any) bool) bool {
		for i := len(result) - 1; i >= 0; i-- {
			if predicate(result[i]) {
				result = append(result[:i], result[i+1:]...)
				return true
			}
		}
		return false
	}
	for len(result) > max {
		if removeFirstMatching(func(tab map[string]any) bool {
			return stringValue(tab["type"]) == "subagent" && !boolValue(tab["pinned"])
		}) {
			continue
		}
		if removeFirstMatching(func(tab map[string]any) bool {
			return !boolValue(tab["pinned"])
		}) {
			continue
		}
		result = result[:len(result)-1]
	}
	return result
}

func getPinnedSectionEndIndex(tabs []map[string]any) int {
	idx := 0
	for idx < len(tabs) {
		tab := tabs[idx]
		if stringValue(tab["workspaceId"]) != "" {
			idx++
			continue
		}
		if !boolValue(tab["pinned"]) {
			break
		}
		idx++
		if stringValue(tab["type"]) == "workspace" {
			wsID := stringValue(tab["id"])
			for idx < len(tabs) && stringValue(tabs[idx]["workspaceId"]) == wsID {
				idx++
			}
		}
	}
	return idx
}

func insertTopLevelTabRespectingPins(tabs []map[string]any, tab map[string]any) []map[string]any {
	return insertTabs(tabs, getPinnedSectionEndIndex(tabs), []map[string]any{tab})
}

func extractMovableGroup(tabs []map[string]any, idx int) ([]map[string]any, []map[string]any) {
	if idx < 0 || idx >= len(tabs) {
		return nil, tabs
	}
	end := idx + 1
	if stringValue(tabs[idx]["type"]) == "workspace" {
		wsID := stringValue(tabs[idx]["id"])
		for end < len(tabs) && stringValue(tabs[end]["workspaceId"]) == wsID {
			end++
		}
	}
	moved := append([]map[string]any{}, tabs[idx:end]...)
	remaining := make([]map[string]any, 0, len(tabs)-len(moved))
	remaining = append(remaining, tabs[:idx]...)
	remaining = append(remaining, tabs[end:]...)
	return moved, remaining
}

func insertTabs(tabs []map[string]any, index int, items []map[string]any) []map[string]any {
	if len(items) == 0 {
		return tabs
	}
	index = clampInt(index, 0, len(tabs))
	out := make([]map[string]any, 0, len(tabs)+len(items))
	out = append(out, tabs[:index]...)
	out = append(out, items...)
	out = append(out, tabs[index:]...)
	return out
}

func firstIdleTopLevelIndex(tabs []map[string]any, statusMap map[string]string) int {
	for i, tab := range tabs {
		if boolValue(tab["pinned"]) || stringValue(tab["workspaceId"]) != "" {
			continue
		}
		if stringValue(tab["type"]) == "workspace" {
			wsID := stringValue(tab["id"])
			children := []map[string]any{}
			for j := i + 1; j < len(tabs) && stringValue(tabs[j]["workspaceId"]) == wsID; j++ {
				children = append(children, tabs[j])
			}
			if len(children) == 0 {
				continue
			}
			allIdle := true
			for _, child := range children {
				status := statusMap[getTabNarratorID(child)]
				if status != "idle" {
					allIdle = false
					break
				}
			}
			if allIdle {
				return i
			}
			continue
		}
		if statusMap[getTabNarratorID(tab)] == "idle" {
			return i
		}
	}
	return -1
}

func clampInt(value int, minValue int, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func validateRemoveRecentTab(tabType string, tabID string) error {
	switch tabType {
	case "chapter", "narrator", "session", "project", "workspace", "subagent":
	default:
		return httpx.ValidationError("recentTab.type is not supported")
	}
	// Mirror the TS removeRecentTabSchema `id: z.string().min(1).max(50)` using
	// UTF-16 code-unit length (httpx.JSStringLength), consistent with the rest of
	// this file (e.g. moveRecentTab key). Go previously used byte length, which
	// would wrongly reject a <=50-character CJK id whose UTF-8 byte count exceeds
	// 50.
	if strings.TrimSpace(tabID) == "" || httpx.JSStringLength(tabID) > 50 {
		return httpx.ValidationError("recentTab.id is required")
	}
	return nil
}

func validateMoveRecentTab(key string, toIndex *int, position string) error {
	if strings.TrimSpace(key) == "" || httpx.JSStringLength(key) > 100 {
		return httpx.ValidationError("key is required")
	}
	if toIndex == nil && position == "" {
		return httpx.ValidationError("toIndex or position required")
	}
	if toIndex != nil && (*toIndex < 0 || *toIndex > maxRecentTabs) {
		return httpx.ValidationError("toIndex must be between 0 and 20")
	}
	if position != "" && position != "top" && position != "above_idle" {
		return httpx.ValidationError("position is not supported")
	}
	return nil
}

type recentTabNarratorInfo struct {
	Status        string
	Substatus     []string
	Traits        []string
	HasDraft      bool
	HasActiveGoal bool
}

func (h *Handler) enrichTabs(ctx context.Context, tabs []map[string]any) []map[string]any {
	if len(tabs) == 0 || h == nil || h.DB == nil {
		return tabs
	}
	migrateTabTypes(tabs)
	narratorIDs := tabNarratorIDs(tabs)
	if len(narratorIDs) > 0 {
		infoMap := h.narratorInfoMap(ctx, narratorIDs)
		for _, tab := range tabs {
			narratorID := getTabNarratorID(tab)
			info, ok := infoMap[narratorID]
			if !ok {
				delete(tab, "substatus")
				delete(tab, "hasDraft")
				delete(tab, "hasActiveGoal")
				continue
			}
			if info.Status != "" {
				tab["status"] = info.Status
			}
			tab["substatus"] = info.Substatus
			if info.HasDraft {
				tab["hasDraft"] = true
			} else {
				delete(tab, "hasDraft")
			}
			if info.HasActiveGoal {
				tab["hasActiveGoal"] = true
			} else {
				delete(tab, "hasActiveGoal")
			}
		}
		for narratorID, count := range h.activeTerminalCounts(ctx, narratorIDs) {
			for _, tab := range tabs {
				if getTabNarratorID(tab) == narratorID {
					tab["activeTerminalCount"] = count
				}
			}
		}
		if provider, ok := h.Broadcaster.(recentTabsPresenceProvider); ok {
			presence := provider.NarratorPresenceBatch(narratorIDs)
			for _, tab := range tabs {
				if viewers := presence[getTabNarratorID(tab)]; len(viewers) > 0 {
					tab["viewers"] = viewers
				}
			}
		}
	}
	chapterIDs := recentTabChapterIDs(tabs)
	if len(chapterIDs) > 0 {
		statuses := h.containerStatusMap(ctx, chapterIDs)
		for _, tab := range tabs {
			if stringValue(tab["type"]) != "chapter" {
				continue
			}
			if status := statuses[stringValue(tab["id"])]; status != "" {
				tab["containerStatus"] = status
			}
		}
	}
	return tabs
}

func (h *Handler) narratorStatusMap(ctx context.Context, narratorIDs []string) map[string]string {
	info := h.narratorInfoMap(ctx, narratorIDs)
	out := make(map[string]string, len(info))
	for id, item := range info {
		out[id] = item.Status
	}
	return out
}

func (h *Handler) narratorInfoMap(ctx context.Context, narratorIDs []string) map[string]recentTabNarratorInfo {
	ids := uniqueStrings(narratorIDs)
	out := map[string]recentTabNarratorInfo{}
	if len(ids) == 0 || h == nil || h.DB == nil {
		return out
	}
	cols := h.tableColumns(ctx, "narrators")
	if !cols["id"] {
		return out
	}
	statusExpr := "''"
	if cols["status"] {
		statusExpr = "status"
	}
	substatusExpr := "'[]'"
	if cols["substatus"] {
		substatusExpr = "substatus"
	}
	traitsExpr := "'[]'"
	if cols["traits"] {
		traitsExpr = "traits"
	}
	args := stringsToAny(ids)
	rows, err := h.DB.QueryContext(ctx, `SELECT id, `+statusExpr+`, `+substatusExpr+`, `+traitsExpr+` FROM narrators WHERE id IN (`+sqlPlaceholders(len(ids))+`)`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var idValue string
		var statusValue, substatusValue, traitsValue sql.NullString
		if err := rows.Scan(&idValue, &statusValue, &substatusValue, &traitsValue); err != nil {
			continue
		}
		traits := decodeStringArray(traitsValue.String)
		out[idValue] = recentTabNarratorInfo{
			Status:    statusValue.String,
			Substatus: decodeStringArray(substatusValue.String),
			Traits:    traits,
			HasDraft:  hasDraftTrait(traits),
		}
	}
	goalCols := h.tableColumns(ctx, "narrator_goals")
	if goalCols["narrator_id"] && goalCols["status"] {
		goalRows, err := h.DB.QueryContext(ctx, `SELECT narrator_id FROM narrator_goals WHERE status = 'active' AND narrator_id IN (`+sqlPlaceholders(len(ids))+`) GROUP BY narrator_id`, args...)
		if err == nil {
			defer goalRows.Close()
			for goalRows.Next() {
				var narratorID string
				if err := goalRows.Scan(&narratorID); err == nil {
					info := out[narratorID]
					info.HasActiveGoal = true
					out[narratorID] = info
				}
			}
		}
	}
	return out
}

func narratorInfoActive(info recentTabNarratorInfo) bool {
	if info.Status == "working" || info.Status == "waiting" {
		return true
	}
	if info.Status != "idle" {
		return false
	}
	for _, tag := range info.Substatus {
		if tag == "unread" || tag == "error" {
			return true
		}
	}
	return false
}

func (h *Handler) activeTerminalCounts(ctx context.Context, narratorIDs []string) map[string]int {
	ids := uniqueStrings(narratorIDs)
	out := map[string]int{}
	if len(ids) == 0 || h == nil || h.DB == nil {
		return out
	}
	cols := h.tableColumns(ctx, "terminals")
	if !cols["narrator_id"] || !cols["status"] {
		return out
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT narrator_id, COUNT(*) FROM terminals WHERE status = 'running' AND narrator_id IN (`+sqlPlaceholders(len(ids))+`) GROUP BY narrator_id`, stringsToAny(ids)...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var narratorID string
		var count int
		if err := rows.Scan(&narratorID, &count); err == nil {
			out[narratorID] = count
		}
	}
	return out
}

func recentTabChapterIDs(tabs []map[string]any) []string {
	ids := []string{}
	seen := map[string]struct{}{}
	for _, tab := range tabs {
		if stringValue(tab["type"]) != "chapter" {
			continue
		}
		idValue := stringValue(tab["id"])
		if idValue == "" {
			continue
		}
		if _, ok := seen[idValue]; ok {
			continue
		}
		seen[idValue] = struct{}{}
		ids = append(ids, idValue)
	}
	return ids
}

func (h *Handler) containerStatusMap(ctx context.Context, chapterIDs []string) map[string]string {
	ids := uniqueStrings(chapterIDs)
	out := map[string]string{}
	if len(ids) == 0 || h == nil || h.DB == nil {
		return out
	}
	cols := h.tableColumns(ctx, "container_instances")
	if !cols["chapter_id"] || !cols["status"] {
		return out
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT chapter_id, status FROM container_instances WHERE chapter_id IN (`+sqlPlaceholders(len(ids))+`)`, stringsToAny(ids)...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var chapterID, status string
		if err := rows.Scan(&chapterID, &status); err != nil {
			continue
		}
		if out[chapterID] == "" || containerStatusPriority[status] > containerStatusPriority[out[chapterID]] {
			out[chapterID] = status
		}
	}
	return out
}

func (h *Handler) deleteWorkspaceRecord(ctx context.Context, workspaceID string) error {
	if strings.TrimSpace(workspaceID) == "" || h == nil || h.DB == nil {
		return nil
	}
	cols := h.tableColumns(ctx, "workspaces")
	if !cols["id"] {
		return nil
	}
	_, err := h.DB.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, workspaceID)
	return err
}

func (h *Handler) tableColumns(ctx context.Context, table string) map[string]bool {
	cols := map[string]bool{}
	if h == nil || h.DB == nil || strings.TrimSpace(table) == "" {
		return cols
	}
	rows, err := h.DB.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return cols
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err == nil {
			cols[name] = true
		}
	}
	return cols
}

func sqlPlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func stringsToAny(items []string) []any {
	args := make([]any, 0, len(items))
	for _, item := range items {
		args = append(args, item)
	}
	return args
}

func uniqueStrings(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func decodeStringArray(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...)
	case []any:
		out := []string{}
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case string:
		if strings.TrimSpace(typed) == "" {
			return []string{}
		}
		var out []string
		if err := json.Unmarshal([]byte(typed), &out); err == nil {
			return out
		}
	}
	return []string{}
}

func hasDraftTrait(traits []string) bool {
	for _, trait := range traits {
		if !strings.HasPrefix(trait, "draft:") {
			continue
		}
		encoded := strings.TrimPrefix(trait, "draft:")
		data, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			continue
		}
		if strings.TrimSpace(stringValue(payload["text"])) != "" {
			return true
		}
	}
	return false
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	case string:
		return typed == "true" || typed == "1"
	default:
		return false
	}
}

func currentUserID(r *http.Request) (string, error) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		return "", httpx.Unauthorized("Authentication required")
	}
	return claims.Subject, nil
}

func encodeValue(column string, value any) any {
	if boolFields[column] {
		if b, ok := value.(bool); ok && b {
			return 1
		}
		return 0
	}
	if column == "commands" || column == "gateway_config" {
		data, _ := json.Marshal(value)
		return string(data)
	}
	return value
}

func tabKey(tab map[string]any) string {
	typeValue, _ := tab["type"].(string)
	idValue, _ := tab["id"].(string)
	return typeValue + ":" + idValue
}

func indexByKey(tabs []map[string]any, key string) int {
	for i, tab := range tabs {
		if tabKey(tab) == key {
			return i
		}
	}
	return -1
}

func normalizeValue(v any) any {
	switch typed := v.(type) {
	case []byte:
		return string(typed)
	case int64:
		return typed
	default:
		return typed
	}
}

func toBool(v any) bool {
	switch typed := v.(type) {
	case bool:
		return typed
	case int64:
		return typed != 0
	case int:
		return typed != 0
	case []byte:
		return string(typed) != "0"
	default:
		return false
	}
}

func decodeArray(v any) []any {
	return jsondb.Array(v)
}

func decodeObject(v any) map[string]any {
	return jsondb.ObjectMap(v)
}

var snakeRe = regexp.MustCompile(`_([a-z])`)

func snakeToCamel(s string) string {
	return snakeRe.ReplaceAllStringFunc(s, func(match string) string {
		return strings.ToUpper(match[1:])
	})
}
