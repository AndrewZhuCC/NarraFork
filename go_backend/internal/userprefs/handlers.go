package userprefs

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	tabs, err := h.recentTabs(r, userID)
	if err != nil {
		return err
	}
	key := tabKey(tab)
	idx := indexByKey(tabs, key)
	if idx >= 0 {
		tabs = append(tabs[:idx], tabs[idx+1:]...)
	} else if updateOnly {
		h.writeRecentTabs(w, http.StatusOK, userID, tabs)
		return nil
	}
	tabs = append([]map[string]any{tab}, tabs...)
	if err := h.saveRecentTabs(r, userID, tabs); err != nil {
		return err
	}
	h.writeRecentTabs(w, http.StatusOK, userID, tabs)
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
	h.writeRecentTabs(w, http.StatusOK, userID, filtered)
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
	h.writeRecentTabs(w, http.StatusOK, userID, tabs)
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
	h.writeRecentTabs(w, http.StatusOK, userID, tabs)
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
	h.writeRecentTabs(w, http.StatusOK, userID, tabs)
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
	title, err := requiredStringField(tab, "title", "recentTab", 0, 200)
	if err != nil {
		return nil, false, err
	}
	clean["title"] = title
	if err := optionalStringField(tab, "subtitle", "recentTab", 0, 200); err != nil {
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
		if len(text) > 500 {
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
	if len(text) < minLen {
		return httpx.ValidationError(key + " must not be empty")
	}
	if len(text) > maxLen {
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
	if len(text) < minLen {
		return "", httpx.ValidationError(path + "." + key + " must not be empty")
	}
	if len(text) > maxLen {
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
	if len(text) < minLen {
		return httpx.ValidationError(path + "." + key + " must not be empty")
	}
	if len(text) > maxLen {
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
		if len(text) > 100 {
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
	if len(value) <= 4 {
		return strings.Repeat("*", len(value))
	}
	return "********" + value[len(value)-4:]
}

func stringValue(value any) string {
	s, _ := value.(string)
	return s
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

func (h *Handler) writeRecentTabs(w http.ResponseWriter, status int, userID string, tabs []map[string]any) {
	h.broadcastRecentTabsSnapshot(userID, tabs)
	httpx.WriteJSON(w, status, tabs)
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
