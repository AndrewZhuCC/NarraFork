package userprefs

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/jsondb"
)

type Handler struct{ DB *sql.DB }

func NewHandler(sqlDB *sql.DB) *Handler { return &Handler{DB: sqlDB} }

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
	var tab map[string]any
	if err := httpx.DecodeLooseJSON(r, &tab); err != nil {
		return err
	}
	tabs, err := h.recentTabs(r, userID)
	if err != nil {
		return err
	}
	key := tabKey(tab)
	idx := indexByKey(tabs, key)
	updateOnly, _ := tab["updateOnly"].(bool)
	delete(tab, "updateOnly")
	if idx >= 0 {
		tabs = append(tabs[:idx], tabs[idx+1:]...)
	} else if updateOnly {
		httpx.WriteJSON(w, http.StatusOK, tabs)
		return nil
	}
	tabs = append([]map[string]any{tab}, tabs...)
	if err := h.saveRecentTabs(r, userID, tabs); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, tabs)
	return nil
}

func (h *Handler) removeRecentTab(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	tabs, err := h.recentTabs(r, userID)
	if err != nil {
		return err
	}
	key := chi.URLParam(r, "type") + ":" + chi.URLParam(r, "id")
	filtered := tabs[:0]
	for _, tab := range tabs {
		if tabKey(tab) != key {
			filtered = append(filtered, tab)
		}
	}
	if err := h.saveRecentTabs(r, userID, filtered); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, filtered)
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
	tabs, err := h.recentTabs(r, userID)
	if err != nil {
		return err
	}
	idx := indexByKey(tabs, body.Key)
	if idx >= 0 {
		tab := tabs[idx]
		tabs = append(tabs[:idx], tabs[idx+1:]...)
		target := 0
		if body.ToIndex != nil && *body.ToIndex >= 0 && *body.ToIndex <= len(tabs) {
			target = *body.ToIndex
		}
		tabs = append(tabs[:target], append([]map[string]any{tab}, tabs[target:]...)...)
	}
	if err := h.saveRecentTabs(r, userID, tabs); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, tabs)
	return nil
}

func (h *Handler) pinRecentTab(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body struct {
		Key    string `json:"key"`
		Pinned bool   `json:"pinned"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	tabs, err := h.recentTabs(r, userID)
	if err != nil {
		return err
	}
	if idx := indexByKey(tabs, body.Key); idx >= 0 {
		tabs[idx]["pinned"] = body.Pinned
	}
	if err := h.saveRecentTabs(r, userID, tabs); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, tabs)
	return nil
}

func (h *Handler) clearRecentTabs(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body struct {
		KeepTabKey string `json:"keepTabKey"`
	}
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	tabs := []map[string]any{}
	if body.KeepTabKey != "" {
		old, _ := h.recentTabs(r, userID)
		if idx := indexByKey(old, body.KeepTabKey); idx >= 0 {
			tabs = append(tabs, old[idx])
		}
	}
	if err := h.saveRecentTabs(r, userID, tabs); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, tabs)
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
	viewports, _ := pref["graphViewports"].(map[string]any)
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
	out["graphViewports"] = decodeObject(out["graphViewports"])
	out["gatewayConfig"] = decodeObject(out["gatewayConfig"])
	return out, nil
}

func (h *Handler) recentTabs(r *http.Request, userID string) ([]map[string]any, error) {
	pref, err := h.load(r, userID)
	if err != nil {
		return nil, err
	}
	items, _ := pref["recentTabs"].([]any)
	result := []map[string]any{}
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			result = append(result, m)
		}
	}
	return result, nil
}

func (h *Handler) saveRecentTabs(r *http.Request, userID string, tabs []map[string]any) error {
	if err := h.ensure(r, userID); err != nil {
		return err
	}
	data, _ := json.Marshal(tabs)
	_, err := h.DB.ExecContext(r.Context(), `UPDATE user_preferences SET recent_tabs = ?, updated_at = ? WHERE user_id = ?`, string(data), db.NowISO(), userID)
	return err
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
