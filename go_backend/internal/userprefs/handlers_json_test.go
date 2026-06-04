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
	if prefs["graphViewports"] != `{"p1":{"x":1,"y":2}}` {
		t.Fatalf("graphViewports = %#v, want raw JSON string", prefs["graphViewports"])
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

type capturedUserBroadcast struct {
	UserID  string
	Payload map[string]any
}

type captureUserBroadcaster struct {
	Calls []capturedUserBroadcast
}

func (b *captureUserBroadcaster) BroadcastUser(userID string, payload any) {
	payloadMap, _ := payload.(map[string]any)
	b.Calls = append(b.Calls, capturedUserBroadcast{UserID: userID, Payload: payloadMap})
}

func TestRecentTabUpsertValidatesTSShapeAndStripsUnknownFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := newUserPrefsTestDB(t)
	_, err := sqlDB.Exec(`INSERT INTO users (id, username, role, created_at) VALUES ('u1', 'alice', 'admin', 'now')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
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
	wrapped := authService.Middleware(httpx.Handler((&Handler{DB: sqlDB}).upsertRecentTab))

	badReq := httptest.NewRequest(http.MethodPut, "/api/user-preferences/recent-tabs", bytes.NewReader([]byte(`{"type":"project","id":"p1","title":"Project"}`)))
	badReq.Header.Set("Authorization", "Bearer "+token)
	badReq.Header.Set("Content-Type", "application/json")
	badResp := httptest.NewRecorder()
	wrapped.ServeHTTP(badResp, badReq)
	if badResp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing lastVisitedAt, got %d body=%s", badResp.Code, badResp.Body.String())
	}

	payload := []byte(`{"type":"project","id":"p1","title":"Project","lastVisitedAt":1700000000000,"pinned":true,"updateOnly":false,"unknown":"drop"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/user-preferences/recent-tabs", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	wrapped.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid tab, got %d body=%s", resp.Code, resp.Body.String())
	}
	var tabs []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &tabs); err != nil {
		t.Fatalf("decode tabs: %v", err)
	}
	if len(tabs) != 1 {
		t.Fatalf("expected one recent tab, got %#v", tabs)
	}
	if _, ok := tabs[0]["unknown"]; ok {
		t.Fatalf("unknown field should be stripped, got %#v", tabs[0])
	}
	if _, ok := tabs[0]["updateOnly"]; ok {
		t.Fatalf("updateOnly should not be persisted, got %#v", tabs[0])
	}
	if tabs[0]["type"] != "project" || tabs[0]["id"] != "p1" || tabs[0]["title"] != "Project" {
		t.Fatalf("unexpected tab payload %#v", tabs[0])
	}
}

func TestRecentTabMutationsBroadcastUserScopedSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := newUserPrefsTestDB(t)
	_, err := sqlDB.Exec(`INSERT INTO users (id, username, role, created_at) VALUES ('u1', 'alice', 'admin', 'now')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
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
	broadcaster := &captureUserBroadcaster{}
	wrapped := authService.Middleware(httpx.Handler((&Handler{DB: sqlDB, Broadcaster: broadcaster}).upsertRecentTab))

	payload := []byte(`{"type":"project","id":"p1","title":"Project","lastVisitedAt":1700000000000}`)
	req := httptest.NewRequest(http.MethodPut, "/api/user-preferences/recent-tabs", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	wrapped.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}
	if len(broadcaster.Calls) != 1 {
		t.Fatalf("expected one broadcast, got %#v", broadcaster.Calls)
	}
	assertRecentTabsSnapshotBroadcast(t, broadcaster.Calls[0], "u1", 1)

	updateOnlyPayload := []byte(`{"type":"project","id":"p2","title":"Other","lastVisitedAt":1700000000001,"updateOnly":true}`)
	updateOnlyReq := httptest.NewRequest(http.MethodPut, "/api/user-preferences/recent-tabs", bytes.NewReader(updateOnlyPayload))
	updateOnlyReq.Header.Set("Authorization", "Bearer "+token)
	updateOnlyReq.Header.Set("Content-Type", "application/json")
	updateOnlyResp := httptest.NewRecorder()
	wrapped.ServeHTTP(updateOnlyResp, updateOnlyReq)
	if updateOnlyResp.Code != http.StatusOK {
		t.Fatalf("expected 200 for updateOnly missing tab, got %d body=%s", updateOnlyResp.Code, updateOnlyResp.Body.String())
	}
	if len(broadcaster.Calls) != 2 {
		t.Fatalf("expected updateOnly path to broadcast unchanged snapshot, got %#v", broadcaster.Calls)
	}
	assertRecentTabsSnapshotBroadcast(t, broadcaster.Calls[1], "u1", 1)
}

func assertRecentTabsSnapshotBroadcast(t *testing.T, call capturedUserBroadcast, userID string, wantTabs int) {
	t.Helper()
	if call.UserID != userID {
		t.Fatalf("broadcast userID = %q, want %q", call.UserID, userID)
	}
	if call.Payload["type"] != "user:recent_tabs_snapshot" {
		t.Fatalf("unexpected broadcast type: %#v", call.Payload)
	}
	tabs, ok := call.Payload["tabs"].([]map[string]any)
	if !ok || len(tabs) != wantTabs {
		t.Fatalf("broadcast tabs = %#v, want %d tabs", call.Payload["tabs"], wantTabs)
	}
	if _, ok := call.Payload["revision"].(int64); !ok {
		t.Fatalf("broadcast revision should be int64, got %#v", call.Payload["revision"])
	}
}

func TestRecentTabUpsertMatchesTSPinnedAndWorkspaceSemantics(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := newUserPrefsTestDB(t)
	token := userPrefsTestToken(t, sqlDB)
	seedRecentTabs(t, sqlDB, `[
		{"type":"project","id":"pin","title":"Pinned","lastVisitedAt":1,"pinned":true},
		{"type":"chapter","id":"c1","narratorId":"n1","title":"Old","lastVisitedAt":2},
		{"type":"project","id":"p2","title":"Project 2","lastVisitedAt":3},
		{"type":"workspace","id":"ws","title":"Workspace","lastVisitedAt":4}
	]`)
	h := &Handler{DB: sqlDB}

	tabs := callRecentTabsEndpoint(t, sqlDB, h.upsertRecentTab, http.MethodPut, "/api/user-preferences/recent-tabs", `{"type":"chapter","id":"c1","narratorId":"n1","title":"Updated","lastVisitedAt":5,"updateOnly":true}`, token)
	assertTabOrder(t, tabs, "project:pin", "chapter:c1", "project:p2", "workspace:ws")
	if tabs[1]["title"] != "Updated" {
		t.Fatalf("updateOnly should merge title without moving, got %#v", tabs[1])
	}

	tabs = callRecentTabsEndpoint(t, sqlDB, h.upsertRecentTab, http.MethodPut, "/api/user-preferences/recent-tabs", `{"type":"project","id":"p2","title":"Project 2 visited","lastVisitedAt":6}`, token)
	assertTabOrder(t, tabs, "project:pin", "project:p2", "chapter:c1", "workspace:ws")

	tabs = callRecentTabsEndpoint(t, sqlDB, h.upsertRecentTab, http.MethodPut, "/api/user-preferences/recent-tabs", `{"type":"narrator","id":"n1","title":"Workspace child","workspaceId":"ws","lastVisitedAt":7,"updateOnly":true}`, token)
	assertTabOrder(t, tabs, "project:pin", "project:p2", "workspace:ws", "chapter:c1")
	if tabs[3]["workspaceId"] != "ws" {
		t.Fatalf("updateOnly narrator fallback should attach chapter tab to workspace, got %#v", tabs[3])
	}

	tabs = callRecentTabsEndpoint(t, sqlDB, h.upsertRecentTab, http.MethodPut, "/api/user-preferences/recent-tabs", `{"type":"narrator","id":"missing-ws-child","title":"No workspace","workspaceId":"missing","lastVisitedAt":8}`, token)
	assertTabOrder(t, tabs, "project:pin", "narrator:missing-ws-child", "project:p2", "workspace:ws", "chapter:c1")
	if _, ok := tabs[1]["workspaceId"]; ok {
		t.Fatalf("orphan workspace child should be promoted to top-level, got %#v", tabs[1])
	}
}

func TestRecentTabMovePinAndClearScopesMatchTS(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := newUserPrefsTestDB(t)
	token := userPrefsTestToken(t, sqlDB)
	createRecentTabsRuntimeTables(t, sqlDB)
	_, err := sqlDB.Exec(`INSERT INTO narrators (id, status, substatus, traits) VALUES
		('n_idle','idle','[]','[]'),
		('n_work','working','[]','[]'),
		('n_unread','idle','["unread"]','[]')`)
	if err != nil {
		t.Fatalf("insert narrators: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO workspaces (id, user_id, title, tree, created_at, updated_at) VALUES ('ws','u1','Workspace','{}','now','now')`)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	h := &Handler{DB: sqlDB}

	seedRecentTabs(t, sqlDB, `[
		{"type":"project","id":"pin","title":"Pinned","lastVisitedAt":1,"pinned":true},
		{"type":"chapter","id":"idle","narratorId":"n_idle","title":"Idle","lastVisitedAt":2},
		{"type":"chapter","id":"work","narratorId":"n_work","title":"Working","lastVisitedAt":3},
		{"type":"workspace","id":"ws","title":"Workspace","lastVisitedAt":4},
		{"type":"chapter","id":"child","narratorId":"n_idle","workspaceId":"ws","title":"Child","lastVisitedAt":5}
	]`)
	tabs := callRecentTabsEndpoint(t, sqlDB, h.moveRecentTab, http.MethodPatch, "/api/user-preferences/recent-tabs/move", `{"key":"chapter:work","position":"above_idle"}`, token)
	assertTabOrder(t, tabs, "project:pin", "chapter:work", "chapter:idle", "workspace:ws", "chapter:child")

	tabs = callRecentTabsEndpoint(t, sqlDB, h.pinRecentTab, http.MethodPatch, "/api/user-preferences/recent-tabs/pin", `{"key":"chapter:idle","pinned":true}`, token)
	assertTabOrder(t, tabs, "project:pin", "chapter:idle", "chapter:work", "workspace:ws", "chapter:child")

	seedRecentTabs(t, sqlDB, `[
		{"type":"project","id":"p","title":"Project","lastVisitedAt":1},
		{"type":"workspace","id":"ws","title":"Workspace","lastVisitedAt":2},
		{"type":"chapter","id":"child","narratorId":"n_idle","workspaceId":"ws","title":"Child","lastVisitedAt":3},
		{"type":"chapter","id":"work","narratorId":"n_work","title":"Working","lastVisitedAt":4},
		{"type":"chapter","id":"unread","narratorId":"n_unread","title":"Unread","lastVisitedAt":5},
		{"type":"chapter","id":"idle","narratorId":"n_idle","title":"Idle","lastVisitedAt":6}
	]`)
	tabs = callRecentTabsEndpoint(t, sqlDB, h.clearRecentTabs, http.MethodPost, "/api/user-preferences/recent-tabs/clear", `{"scope":"projects"}`, token)
	assertTabOrder(t, tabs, "workspace:ws", "chapter:child", "chapter:work", "chapter:unread", "chapter:idle")

	seedRecentTabs(t, sqlDB, `[
		{"type":"project","id":"p","title":"Project","lastVisitedAt":1},
		{"type":"workspace","id":"ws","title":"Workspace","lastVisitedAt":2},
		{"type":"chapter","id":"child","narratorId":"n_idle","workspaceId":"ws","title":"Child","lastVisitedAt":3},
		{"type":"chapter","id":"work","narratorId":"n_work","title":"Working","lastVisitedAt":4},
		{"type":"chapter","id":"unread","narratorId":"n_unread","title":"Unread","lastVisitedAt":5},
		{"type":"chapter","id":"idle","narratorId":"n_idle","title":"Idle","lastVisitedAt":6}
	]`)
	tabs = callRecentTabsEndpoint(t, sqlDB, h.clearRecentTabs, http.MethodPost, "/api/user-preferences/recent-tabs/clear", `{"scope":"inactive_narrators"}`, token)
	assertTabOrder(t, tabs, "project:p", "chapter:work", "chapter:unread")
	var workspaceCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE id = 'ws'`).Scan(&workspaceCount); err != nil {
		t.Fatalf("count workspace: %v", err)
	}
	if workspaceCount != 0 {
		t.Fatalf("inactive workspace should be dissolved, count=%d", workspaceCount)
	}
}

func TestRecentTabsLoadAndSnapshotsAreEnriched(t *testing.T) {
	sqlDB := newUserPrefsTestDB(t)
	createRecentTabsRuntimeTables(t, sqlDB)
	seedRecentTabs(t, sqlDB, `[{"type":"chapter","id":"c1","narratorId":"n1","title":"Chapter","lastVisitedAt":1}]`)
	_, err := sqlDB.Exec(`INSERT INTO narrators (id, status, substatus, traits) VALUES ('n1','waiting','["unread"]','[]')`)
	if err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO narrator_goals (id, narrator_id, status) VALUES ('g1','n1','active')`)
	if err != nil {
		t.Fatalf("insert goal: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO terminals (id, narrator_id, status) VALUES ('t1','n1','running'), ('t2','n1','exited')`)
	if err != nil {
		t.Fatalf("insert terminals: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO container_instances (id, chapter_id, status) VALUES ('ci1','c1','paused'), ('ci2','c1','running')`)
	if err != nil {
		t.Fatalf("insert containers: %v", err)
	}

	prefs, err := (&Handler{DB: sqlDB}).load(httptest.NewRequest(http.MethodGet, "/", nil), "u1")
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	tabs := prefs["recentTabs"].([]any)
	tab := tabs[0].(map[string]any)
	if tab["status"] != "waiting" || tab["activeTerminalCount"] != 1 || tab["containerStatus"] != "running" || tab["hasActiveGoal"] != true {
		t.Fatalf("recent tab should be enriched, got %#v", tab)
	}
	substatus, ok := tab["substatus"].([]string)
	if !ok || len(substatus) != 1 || substatus[0] != "unread" {
		t.Fatalf("substatus should be enriched, got %#v", tab["substatus"])
	}
}

func userPrefsTestToken(t *testing.T, sqlDB *sql.DB) string {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT OR IGNORE INTO users (id, username, role, created_at) VALUES ('u1', 'alice', 'admin', 'now')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	token, err := auth.NewService(sqlDB, settings).CreateToken("u1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return token
}

func seedRecentTabs(t *testing.T, sqlDB *sql.DB, tabs string) {
	t.Helper()
	_, err := sqlDB.Exec(`INSERT INTO user_preferences (id, user_id, recent_tabs, created_at, updated_at) VALUES ('pref1', 'u1', ?, 'now', 'now') ON CONFLICT(user_id) DO UPDATE SET recent_tabs = excluded.recent_tabs, updated_at = excluded.updated_at`, tabs)
	if err != nil {
		t.Fatalf("seed recent tabs: %v", err)
	}
}

func callRecentTabsEndpoint(t *testing.T, sqlDB *sql.DB, handler func(http.ResponseWriter, *http.Request) error, method string, path string, body string, token string) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	auth.NewService(sqlDB, settings).Middleware(httpx.Handler(handler)).ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("%s %s status=%d body=%s", method, path, resp.Code, resp.Body.String())
	}
	var tabs []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &tabs); err != nil {
		t.Fatalf("decode recent tabs: %v body=%s", err, resp.Body.String())
	}
	return tabs
}

func assertTabOrder(t *testing.T, tabs []map[string]any, keys ...string) {
	t.Helper()
	if len(tabs) != len(keys) {
		t.Fatalf("tab count=%d want %d tabs=%#v", len(tabs), len(keys), tabs)
	}
	for i, key := range keys {
		if got := tabKey(tabs[i]); got != key {
			t.Fatalf("tab[%d]=%s want %s full=%#v", i, got, key, tabs)
		}
	}
}

func createRecentTabsRuntimeTables(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS narrators (id TEXT PRIMARY KEY, status TEXT, substatus TEXT, traits TEXT)`,
		`CREATE TABLE IF NOT EXISTS narrator_goals (id TEXT PRIMARY KEY, narrator_id TEXT, status TEXT)`,
		`CREATE TABLE IF NOT EXISTS terminals (id TEXT PRIMARY KEY, narrator_id TEXT, status TEXT)`,
		`CREATE TABLE IF NOT EXISTS container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, status TEXT)`,
		`CREATE TABLE IF NOT EXISTS workspaces (id TEXT PRIMARY KEY, user_id TEXT, title TEXT, tree TEXT, created_at TEXT, updated_at TEXT)`,
	}
	for _, stmt := range statements {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create runtime table: %v", err)
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
