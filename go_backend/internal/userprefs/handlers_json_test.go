package userprefs

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"

	_ "modernc.org/sqlite"
)

func TestUserPreferencesLoadDecodesJSONFields(t *testing.T) {
	sqlDB := newUserPrefsTestDB(t)
	_, err := sqlDB.Exec(`INSERT INTO user_preferences (id, user_id, recent_tabs, commands, graph_viewports, gateway_config, created_at, updated_at) VALUES ('pref1', 'u1', ?, ?, ?, ?, 'now', 'now')`,
		`[{"type":"narrator","id":"n1"}]`,
		`[{"name":"ship","prompt":"/ship"}]`,
		`{"p1":{"x":1,"y":2}}`,
		`{"enabled":true}`,
	)
	if err != nil {
		t.Fatalf("insert prefs: %v", err)
	}

	prefs, err := (&Handler{DB: sqlDB}).load(httptest.NewRequest("GET", "/", nil), "u1")
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	if _, ok := prefs["recentTabs"].([]any); !ok {
		t.Fatalf("recentTabs = %#v, want array", prefs["recentTabs"])
	}
	if _, ok := prefs["commands"].([]any); !ok {
		t.Fatalf("commands = %#v, want array", prefs["commands"])
	}
	if _, ok := prefs["graphViewports"].(map[string]any); !ok {
		t.Fatalf("graphViewports = %#v, want object", prefs["graphViewports"])
	}
	if _, ok := prefs["gatewayConfig"].(map[string]any); !ok {
		t.Fatalf("gatewayConfig = %#v, want object", prefs["gatewayConfig"])
	}
}

func TestUserPreferencesLoadRedactsNotificationAndGatewaySecrets(t *testing.T) {
	sqlDB := newUserPrefsTestDB(t)
	gatewayConfig := `{"platforms":[{"platform":"telegram","enabled":true,"token":"telegram-token-1234","botToken":"bot-token-5678","appToken":"app-token-9012","appSecret":"app-secret-3456","secret":"signing-secret-7890","clientSecret":"client-secret-2468","appId":"public-app"}]}`
	_, err := sqlDB.Exec(`INSERT INTO user_preferences (id, user_id, notify_dingtalk_webhook, notify_dingtalk_secret, notify_feishu_webhook, notify_feishu_secret, gateway_config, created_at, updated_at) VALUES ('pref1', 'u1', ?, ?, ?, ?, ?, 'now', 'now')`,
		"https://oapi.dingtalk.com/robot/send?access_token=abcdef1234",
		"dingtalk-secret-5678",
		"https://open.feishu.cn/hook/feishu9876",
		"feishu-secret-4321",
		gatewayConfig,
	)
	if err != nil {
		t.Fatalf("insert prefs: %v", err)
	}

	prefs, err := (&Handler{DB: sqlDB}).load(httptest.NewRequest(http.MethodGet, "/", nil), "u1")
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	assertMasked(t, prefs["notifyDingtalkWebhook"], "https://oapi.dingtalk.com/robot/send?access_token=abcdef1234")
	assertMasked(t, prefs["notifyDingtalkSecret"], "dingtalk-secret-5678")
	assertMasked(t, prefs["notifyFeishuWebhook"], "https://open.feishu.cn/hook/feishu9876")
	assertMasked(t, prefs["notifyFeishuSecret"], "feishu-secret-4321")

	gateway, _ := prefs["gatewayConfig"].(map[string]any)
	platforms, _ := gateway["platforms"].([]any)
	if len(platforms) != 1 {
		t.Fatalf("expected one gateway platform, got %#v", gateway["platforms"])
	}
	platform, _ := platforms[0].(map[string]any)
	assertMasked(t, platform["token"], "telegram-token-1234")
	assertMasked(t, platform["botToken"], "bot-token-5678")
	assertMasked(t, platform["appToken"], "app-token-9012")
	assertMasked(t, platform["appSecret"], "app-secret-3456")
	assertMasked(t, platform["secret"], "signing-secret-7890")
	assertMasked(t, platform["clientSecret"], "client-secret-2468")
	if platform["appId"] != "public-app" {
		t.Fatalf("non-secret appId should be preserved, got %#v", platform["appId"])
	}
}

func TestPatchUserPreferencesSavesGatewayConfigAndPreservesMaskedSecrets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := newUserPrefsTestDB(t)
	_, err := sqlDB.Exec(`INSERT INTO users (id, username, role, created_at) VALUES ('u1', 'alice', 'admin', 'now')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	originalGateway := `{"enabled":true,"platforms":[{"platform":"telegram","enabled":true,"token":"telegram-token-1234","botToken":"bot-token-5678","clientSecret":"client-secret-2468"}]}`
	_, err = sqlDB.Exec(`INSERT INTO user_preferences (id, user_id, notify_dingtalk_webhook, notify_dingtalk_secret, gateway_config, created_at, updated_at) VALUES ('pref1', 'u1', ?, ?, ?, 'now', 'now')`,
		"https://oapi.dingtalk.com/robot/send?access_token=abcdef1234",
		"dingtalk-secret-5678",
		originalGateway,
	)
	if err != nil {
		t.Fatalf("insert prefs: %v", err)
	}
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	authService := auth.NewService(sqlDB, settings)
	token, err := authService.CreateToken("u1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	payload := []byte(`{"notifyDingtalkWebhook":"********1234","notifyDingtalkSecret":"********5678","gatewayConfig":{"enabled":false,"defaultPermissionMode":"allowByDefault","platforms":[{"platform":"telegram","enabled":true,"token":"********1234","botToken":"********5678","clientSecret":"********2468"},{"platform":"discord","enabled":true,"token":"discord-token-new"}]}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/user-preferences", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wrapped := authService.Middleware(httpx.Handler((&Handler{DB: sqlDB}).patch))

	wrapped.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	assertMasked(t, response["notifyDingtalkWebhook"], "https://oapi.dingtalk.com/robot/send?access_token=abcdef1234")
	assertMasked(t, response["notifyDingtalkSecret"], "dingtalk-secret-5678")

	var storedWebhook, storedSecret, storedGateway string
	if err := sqlDB.QueryRow(`SELECT notify_dingtalk_webhook, notify_dingtalk_secret, gateway_config FROM user_preferences WHERE user_id = 'u1'`).Scan(&storedWebhook, &storedSecret, &storedGateway); err != nil {
		t.Fatalf("query stored prefs: %v", err)
	}
	if storedWebhook != "https://oapi.dingtalk.com/robot/send?access_token=abcdef1234" || storedSecret != "dingtalk-secret-5678" {
		t.Fatalf("masked notification patch should preserve originals, got webhook=%q secret=%q", storedWebhook, storedSecret)
	}
	var storedGatewayConfig map[string]any
	if err := json.Unmarshal([]byte(storedGateway), &storedGatewayConfig); err != nil {
		t.Fatalf("decode stored gateway config: %v", err)
	}
	if storedGatewayConfig["enabled"] != false {
		t.Fatalf("gatewayConfig was not saved, got %#v", storedGatewayConfig["enabled"])
	}
	if storedGatewayConfig["defaultPermissionMode"] != "acceptEdits" {
		t.Fatalf("legacy defaultPermissionMode was not normalized, got %#v", storedGatewayConfig["defaultPermissionMode"])
	}
	storedPlatforms, _ := storedGatewayConfig["platforms"].([]any)
	telegram, _ := storedPlatforms[0].(map[string]any)
	if telegram["token"] != "telegram-token-1234" || telegram["botToken"] != "bot-token-5678" || telegram["clientSecret"] != "client-secret-2468" {
		t.Fatalf("masked gateway patch should preserve telegram secrets, got %#v", telegram)
	}
	discord, _ := storedPlatforms[1].(map[string]any)
	if discord["token"] != "discord-token-new" {
		t.Fatalf("new gateway platform token should be saved, got %#v", discord)
	}
}

func TestPatchUserPreferencesRejectsInsecureNotificationWebhook(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := newUserPrefsTestDB(t)
	_, err := sqlDB.Exec(`INSERT INTO users (id, username, role, created_at) VALUES ('u1', 'alice', 'admin', 'now')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO user_preferences (id, user_id, created_at, updated_at) VALUES ('pref1', 'u1', 'now', 'now')`)
	if err != nil {
		t.Fatalf("insert prefs: %v", err)
	}
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	authService := auth.NewService(sqlDB, settings)
	token, err := authService.CreateToken("u1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/user-preferences", bytes.NewReader([]byte(`{"notifyDingtalkWebhook":"http://insecure.example/hook"}`)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wrapped := authService.Middleware(httpx.Handler((&Handler{DB: sqlDB}).patch))

	wrapped.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", response["code"])
	}
}

func TestPatchUserPreferencesRejectsInvalidTypedFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := newUserPrefsTestDB(t)
	_, err := sqlDB.Exec(`INSERT INTO users (id, username, role, created_at) VALUES ('u1', 'alice', 'admin', 'now')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO user_preferences (id, user_id, created_at, updated_at) VALUES ('pref1', 'u1', 'now', 'now')`)
	if err != nil {
		t.Fatalf("insert prefs: %v", err)
	}
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	authService := auth.NewService(sqlDB, settings)
	token, err := authService.CreateToken("u1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	wrapped := authService.Middleware(httpx.Handler((&Handler{DB: sqlDB}).patch))

	cases := []string{
		`{"notifyOnDone":"yes"}`,
		`{"language":"fr"}`,
		`{"terminalFontSize":7}`,
		`{"commands":{}}`,
		`{"commands":[{"name":"bad space","prompt":"ok"}]}`,
		`{"commands":[{"name":"good","prompt":""}]}`,
		`{"commands":[{"name":"good","prompt":"ok","runBashFirst":"yes"}]}`,
		`{"commands":[{"name":"good","prompt":"ok","modelOverride":{"model":"gpt","mode":"forever"}}]}`,
		`{"gatewayConfig":{"enabled":"yes"}}`,
		`{"gatewayConfig":{"sessionIdleMinutes":43201}}`,
		`{"gatewayConfig":{"platforms":[{"platform":"telegram"}]}}`,
		`{"gatewayConfig":{"platforms":[{"platform":"unknown","enabled":true}]}}`,
	}
	for _, payload := range cases {
		req := httptest.NewRequest(http.MethodPatch, "/api/user-preferences", bytes.NewReader([]byte(payload)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d body=%s", payload, w.Code, w.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response for %s: %v", payload, err)
		}
		if response["code"] != "VALIDATION_ERROR" {
			t.Fatalf("expected VALIDATION_ERROR for %s, got %#v", payload, response["code"])
		}
	}
}

func newUserPrefsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	statements := []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, role TEXT, created_at TEXT)`,
		`CREATE TABLE user_preferences (
			id TEXT PRIMARY KEY, user_id TEXT UNIQUE, auto_load_older_messages INTEGER DEFAULT 0,
			fast_mode_default INTEGER DEFAULT 0, language TEXT, word_wrap_markdown INTEGER DEFAULT 0,
			word_wrap_code INTEGER DEFAULT 0, word_wrap_diff INTEGER DEFAULT 0,
			reply_in_user_language INTEGER DEFAULT 0, show_token_usage INTEGER DEFAULT 0,
			show_output_stats INTEGER DEFAULT 0, terminal_theme TEXT, terminal_font_size INTEGER,
			recent_tabs TEXT DEFAULT '[]', add_subagent_to_recent_tabs INTEGER DEFAULT 0, notify_on_done INTEGER DEFAULT 0,
			notify_on_waiting INTEGER DEFAULT 0, notify_pwa_enabled INTEGER DEFAULT 0,
			notify_sound_enabled INTEGER DEFAULT 0, notify_sound_type TEXT, notify_sound_builtin TEXT,
			notify_sound_file_id TEXT, notify_dingtalk_enabled INTEGER DEFAULT 0, notify_dingtalk_webhook TEXT,
			notify_dingtalk_secret TEXT, notify_feishu_enabled INTEGER DEFAULT 0, notify_feishu_webhook TEXT,
			notify_feishu_secret TEXT, commands TEXT DEFAULT '[]', graph_viewports TEXT DEFAULT '{}', send_mode TEXT,
			setup_wizard_completed INTEGER DEFAULT 0, gateway_config TEXT DEFAULT '{}', created_at TEXT, updated_at TEXT
		)`,
	}
	for _, stmt := range statements {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create test schema: %v", err)
		}
	}
	return sqlDB
}

func assertMasked(t *testing.T, value any, raw string) {
	t.Helper()
	got, ok := value.(string)
	if !ok {
		t.Fatalf("expected masked string for %q, got %#v", raw, value)
	}
	if got != maskSecret(raw) {
		t.Fatalf("expected %q to be masked as %q, got %q", raw, maskSecret(raw), got)
	}
}
