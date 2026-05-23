package systemroutes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
	"narrafork-go/internal/narrators"
)

func newSystemRoutesTestStore(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return store
}

func newProviderRoutesTestRouter(t *testing.T) (*chi.Mux, string) {
	t.Helper()
	return newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "user")
}

func newProviderRoutesTestRouterWithStore(t *testing.T, store *config.Store, role string) (*chi.Mux, string) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY, role TEXT)`); err != nil {
		t.Fatalf("create users table: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO users (id, role) VALUES ('user-1', ?)`, role); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	authService := auth.NewService(sqlDB, store)
	token, err := authService.CreateToken("user-1", role)
	if err != nil {
		t.Fatalf("create user token: %v", err)
	}
	router := chi.NewRouter()
	router.Use(authService.Middleware)
	h := &Handler{Settings: store}
	h.RegisterProviders(router)
	return router, token
}

func newProviderRoutesTestRouterWithoutSettings(t *testing.T, role string) (*chi.Mux, string) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY, role TEXT)`); err != nil {
		t.Fatalf("create users table: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO users (id, role) VALUES ('user-1', ?)`, role); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	store := newSystemRoutesTestStore(t)
	authService := auth.NewService(sqlDB, store)
	token, err := authService.CreateToken("user-1", role)
	if err != nil {
		t.Fatalf("create user token: %v", err)
	}
	router := chi.NewRouter()
	router.Use(authService.Middleware)
	h := &Handler{}
	h.RegisterProviders(router)
	return router, token
}

func TestProviderSensitiveRoutesRequireAdminBoundary(t *testing.T) {
	router, userToken := newProviderRoutesTestRouter(t)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/kiro/status"},
		{http.MethodGet, "/codex/status"},
		{http.MethodPost, "/codex/import"},
		{http.MethodPost, "/cline/auth/browser"},
		{http.MethodPost, "/nug/providers/nug-test/login"},
		{http.MethodPost, "/nug/providers/nug-test/api-key"},
		{http.MethodGet, "/nug/providers/nug-test/oauth/start"},
		{http.MethodPost, "/openai/models/refresh"},
		{http.MethodPost, "/openai/providers/openai-test/models/refresh"},
		{http.MethodPost, "/anthropic/models/refresh"},
		{http.MethodPost, "/anthropic/providers/anthropic-test/models/refresh"},
		{http.MethodPost, "/nkp/models/refresh"},
		{http.MethodPost, "/nkp/providers/nkp-test/models/refresh"},
		{http.MethodPost, "/nug/models/refresh"},
		{http.MethodPost, "/nug/providers/nug-test/models/refresh"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+userToken)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 admin boundary, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			decodeJSONBody(t, w, &body)
			if body["code"] != "FORBIDDEN" || body["error"] != "Admin access required" {
				t.Fatalf("unexpected forbidden body: %#v", body)
			}
		})
	}
}

func TestKiroChatReturnsExplicitFeatureDisabledFallback(t *testing.T) {
	router, adminToken := newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "admin")
	req := httptest.NewRequest(http.MethodPost, "/kiro/chat", strings.NewReader(`{"messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 feature disabled, got %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("kiro chat fallback must not be reported as a successful SSE stream")
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "kiro.chat" || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected Kiro chat fallback body: %#v", body)
	}
	reason, _ := body["reason"].(string)
	if !strings.Contains(reason, "Kiro chat streaming is not implemented in go_backend") || body["error"] != reason || body["message"] != reason {
		t.Fatalf("Kiro chat fallback should expose reason/error/message, got %#v", body)
	}
}

func TestKiroMcpToolsFallbackExposesReason(t *testing.T) {
	router, adminToken := newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "admin")
	req := httptest.NewRequest(http.MethodGet, "/kiro/mcp-tools", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 fallback tools, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	reason, _ := body["reason"].(string)
	if body["fallback"] != true || reason == "" || body["error"] != reason || body["message"] != reason {
		t.Fatalf("Kiro MCP tools fallback should expose reason/error/message, got %#v", body)
	}
	if result, _ := body["result"].(map[string]any); len(result) == 0 {
		t.Fatalf("Kiro MCP tools fallback should keep result tools payload, got %#v", body)
	}
}

func TestKiroMcpSearchFallbackExposesReason(t *testing.T) {
	router, adminToken := newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "admin")
	req := httptest.NewRequest(http.MethodPost, "/kiro/mcp-search", strings.NewReader(`{"query":"narrafork"}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 fallback search, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	reason, _ := body["reason"].(string)
	errorPayload, _ := body["error"].(map[string]any)
	if body["fallback"] != true || !strings.Contains(reason, "Kiro MCP search unavailable in go_backend") || body["message"] != reason || errorPayload["message"] != reason {
		t.Fatalf("Kiro MCP search fallback should expose reason/message and JSON-RPC error, got %#v", body)
	}
}

func TestNugLoginPersistsReturnedAPIKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected login method: %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode login body: %v", err)
			}
			if body["username"] != "alice" || body["password"] != "secret" {
				t.Fatalf("unexpected login body: %#v", body)
			}
			http.SetCookie(w, &http.Cookie{Name: "nug_session", Value: "session-token"})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user": map[string]any{"id": "nug-user-1", "username": "alice"},
			})
		case "/api/api-keys":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected api key method: %s", r.Method)
			}
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "nug_session=session-token") {
				t.Fatalf("missing session cookie: %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"apiKey": "nug-api-key"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := newSystemRoutesTestStore(t)
	if _, err := store.Patch(map[string]any{
		"nugProviders": []any{map[string]any{
			"id":      "nug-test",
			"name":    "NUG Test",
			"prefix":  "nug",
			"baseUrl": upstream.URL,
		}},
	}); err != nil {
		t.Fatalf("seed nug provider: %v", err)
	}
	router, adminToken := newProviderRoutesTestRouterWithStore(t, store, "admin")
	payload, _ := json.Marshal(map[string]any{"username": "alice", "password": "secret"})
	req := httptest.NewRequest(http.MethodPost, "/nug/providers/nug-test/login", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected login 200, got %d body=%s", w.Code, w.Body.String())
	}
	var response map[string]any
	decodeJSONBody(t, w, &response)
	if response["apiKey"] != "nug-api-key" {
		t.Fatalf("unexpected login response: %#v", response)
	}

	raw := store.Raw()
	providers, _ := raw["nugProviders"].([]any)
	if len(providers) != 1 {
		t.Fatalf("expected one provider, got %#v", providers)
	}
	provider, _ := providers[0].(map[string]any)
	if provider["apiKey"] != "nug-api-key" {
		t.Fatalf("expected saved api key, got %#v", provider)
	}
	if provider["nugUsername"] != "alice" || provider["nugUserId"] != "nug-user-1" {
		t.Fatalf("expected saved user metadata, got %#v", provider)
	}
}

func TestCodexUsageQueueClearReturnsOkWhenQueueIsEmpty(t *testing.T) {
	router, adminToken := newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "admin")
	req := httptest.NewRequest(http.MethodPost, "/codex/usage-queue/clear", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 ok, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["ok"] != true {
		t.Fatalf("unexpected Codex usage queue clear body: %#v", body)
	}
}

func TestCodexStatusPaginatesAvailableAndUnavailableEntries(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	dataDir, err := narraforkDataDir()
	if err != nil {
		t.Fatalf("data dir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	credentials := make([]map[string]any, 0, 28)
	for i := 0; i < 25; i++ {
		id := "available-" + strconv.Itoa(i)
		credentials = append(credentials, map[string]any{
			"id":          id,
			"displayName": "Available " + strconv.Itoa(i),
			"usage":       map[string]any{"plan_type": "pro", "remaining_percent": 80 - i},
		})
	}
	for i := 0; i < 3; i++ {
		credentials = append(credentials, map[string]any{
			"id":          "unavailable-" + strconv.Itoa(i),
			"displayName": "Unavailable " + strconv.Itoa(i),
			"disabled":    true,
			"usage":       map[string]any{"plan_type": "free", "remaining_percent": i},
		})
	}
	data, _ := json.Marshal(credentials)
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), data, 0o600); err != nil {
		t.Fatalf("write codex credentials: %v", err)
	}
	router, adminToken := newProviderRoutesTestRouterWithStore(t, store, "admin")
	req := httptest.NewRequest(http.MethodGet, "/codex/status?availablePage=2&unavailablePage=1&pageSize=20", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 ok, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["availableTotal"] != float64(25) || body["unavailableTotal"] != float64(3) {
		t.Fatalf("unexpected totals: available=%#v unavailable=%#v", body["availableTotal"], body["unavailableTotal"])
	}
	availableEntries, _ := body["availableEntries"].([]any)
	unavailableEntries, _ := body["unavailableEntries"].([]any)
	entries, _ := body["entries"].([]any)
	if len(availableEntries) != 5 || len(unavailableEntries) != 3 || len(entries) != 8 {
		t.Fatalf("unexpected page lengths: available=%d unavailable=%d entries=%d", len(availableEntries), len(unavailableEntries), len(entries))
	}
	firstAvailable, _ := availableEntries[0].(map[string]any)
	if firstAvailable["id"] != "available-20" {
		t.Fatalf("expected page 2 to start with available-20, got %#v", firstAvailable["id"])
	}
	usageCache, _ := body["usageCache"].(map[string]any)
	if _, ok := usageCache["available-0"]; ok {
		t.Fatalf("usageCache should only include paged entries, got %#v", usageCache)
	}
	if _, ok := usageCache["available-20"]; !ok {
		t.Fatalf("usageCache should include current page entries, got %#v", usageCache)
	}
}

func TestKiroSettingsMutationWithoutSettingsStoreReturnsFeatureDisabled(t *testing.T) {
	router, adminToken := newProviderRoutesTestRouterWithoutSettings(t, "admin")
	req := httptest.NewRequest(http.MethodPost, "/kiro/global-proxy", strings.NewReader(`{"proxy":"https://proxy.example.test"}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 feature disabled, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "providers.kiro.settingsStore" || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected Kiro settings fallback body: %#v", body)
	}
	reason, _ := body["reason"].(string)
	if reason == "" || body["error"] != reason || body["message"] != reason {
		t.Fatalf("Kiro settings fallback should expose reason/error/message, got %#v", body)
	}
}

func TestCodexSettingsMutationWithoutSettingsStoreReturnsFeatureDisabled(t *testing.T) {
	router, adminToken := newProviderRoutesTestRouterWithoutSettings(t, "admin")
	req := httptest.NewRequest(http.MethodPost, "/codex/default-reasoning-effort", strings.NewReader(`{"reasoningEffort":"medium"}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 feature disabled, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "providers.codex.settingsStore" || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected Codex settings fallback body: %#v", body)
	}
	reason, _ := body["reason"].(string)
	if reason == "" || body["error"] != reason || body["message"] != reason {
		t.Fatalf("Codex settings fallback should expose reason/error/message, got %#v", body)
	}
}

func TestCodexQuotaOverviewRemainsAvailableToAuthenticatedUsers(t *testing.T) {
	router, userToken := newProviderRoutesTestRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/codex/quota-overview", nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected quota overview 200 for authenticated user, got %d body=%s", w.Code, w.Body.String())
	}
}

func newAdminRoutesTestRouter(t *testing.T, seedUsers []struct{ id, username, role string }, tokenUserID string) (*chi.Mux, string, *sql.DB) {
	t.Helper()
	return newAdminRoutesTestRouterWithTokenRole(t, seedUsers, tokenUserID, "")
}

func newAdminRoutesTestRouterWithTokenRole(t *testing.T, seedUsers []struct{ id, username, role string }, tokenUserID, tokenRole string) (*chi.Mux, string, *sql.DB) {
	t.Helper()
	store := newSystemRoutesTestStore(t)
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	_, err = sqlDB.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY,
		username TEXT UNIQUE,
		password_hash TEXT,
		role TEXT,
		avatar_color TEXT,
		avatar_image_id TEXT,
		created_at TEXT
	)`)
	if err != nil {
		t.Fatalf("create users table: %v", err)
	}
	for _, user := range seedUsers {
		if _, err := sqlDB.Exec(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES (?, ?, 'hash', ?, 'now')`, user.id, user.username, user.role); err != nil {
			t.Fatalf("insert user %s: %v", user.id, err)
		}
	}
	authService := auth.NewService(sqlDB, store)
	role := tokenRole
	if role == "" {
		role = "admin"
		for _, user := range seedUsers {
			if user.id == tokenUserID {
				role = user.role
			}
		}
	}
	token, err := authService.CreateToken(tokenUserID, role)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	router := chi.NewRouter()
	router.Use(authService.Middleware)
	h := &Handler{DB: sqlDB, Settings: store}
	router.Route("/admin", h.RegisterAdmin)
	return router, token, sqlDB
}

func TestAdminUpdateUserPreventsSelfDemote(t *testing.T) {
	router, token, sqlDB := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{{"admin-1", "root", "admin"}, {"admin-2", "ops", "admin"}}, "admin-1")
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/admin-1", strings.NewReader(`{"role":"user"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 self demote, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["code"] != "SELF_DEMOTE" || body["error"] != "Cannot demote yourself" {
		t.Fatalf("unexpected self demote body: %#v", body)
	}
	var role string
	if err := sqlDB.QueryRow(`SELECT role FROM users WHERE id='admin-1'`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("self demote should not update role, role=%q err=%v", role, err)
	}
}

func TestAdminUpdateUserProtectsLastAdminDemotion(t *testing.T) {
	router, token, _ := newAdminRoutesTestRouterWithTokenRole(t, []struct{ id, username, role string }{{"admin-1", "root", "admin"}, {"user-1", "member", "user"}}, "user-1", "admin")
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/admin-1", strings.NewReader(`{"role":"user"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 last-admin demote, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["code"] != "LAST_ADMIN" {
		t.Fatalf("unexpected last-admin demote body: %#v", body)
	}
}

func TestAdminDeleteUserProtectsLastAdmin(t *testing.T) {
	router, token, _ := newAdminRoutesTestRouterWithTokenRole(t, []struct{ id, username, role string }{{"admin-1", "root", "admin"}, {"user-1", "member", "user"}}, "user-1", "admin")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/admin/users/admin-1", nil)
	deleteReq.Header.Set("Authorization", "Bearer "+token)
	deleteW := httptest.NewRecorder()
	router.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 last-admin delete, got %d body=%s", deleteW.Code, deleteW.Body.String())
	}
	var deleteBody map[string]any
	decodeJSONBody(t, deleteW, &deleteBody)
	if deleteBody["code"] != "LAST_ADMIN" {
		t.Fatalf("unexpected last-admin delete body: %#v", deleteBody)
	}
}

func TestAdminUpdateUserValidatesDuplicateAndMissingUsers(t *testing.T) {
	router, token, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{{"admin-1", "root", "admin"}, {"user-1", "member", "user"}}, "admin-1")

	dupReq := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1", strings.NewReader(`{"username":"root"}`))
	dupReq.Header.Set("Authorization", "Bearer "+token)
	dupReq.Header.Set("Content-Type", "application/json")
	dupW := httptest.NewRecorder()
	router.ServeHTTP(dupW, dupReq)
	if dupW.Code != http.StatusConflict {
		t.Fatalf("expected 409 duplicate username, got %d body=%s", dupW.Code, dupW.Body.String())
	}
	var dupBody map[string]any
	decodeJSONBody(t, dupW, &dupBody)
	if dupBody["code"] != "USERNAME_TAKEN" {
		t.Fatalf("unexpected duplicate username body: %#v", dupBody)
	}

	missingReq := httptest.NewRequest(http.MethodPatch, "/admin/users/missing", strings.NewReader(`{"username":"nobody"}`))
	missingReq.Header.Set("Authorization", "Bearer "+token)
	missingReq.Header.Set("Content-Type", "application/json")
	missingW := httptest.NewRecorder()
	router.ServeHTTP(missingW, missingReq)
	if missingW.Code != http.StatusNotFound {
		t.Fatalf("expected 404 missing update target, got %d body=%s", missingW.Code, missingW.Body.String())
	}
}

func TestAdminUpdateUserReturnsUpdatedShape(t *testing.T) {
	router, token, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{{"admin-1", "root", "admin"}, {"user-1", "member", "user"}}, "admin-1")
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1", strings.NewReader(`{"username":"member2","role":"admin","password":"new-secret"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 update, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["id"] != "user-1" || body["username"] != "member2" || body["role"] != "admin" {
		t.Fatalf("unexpected updated user body: %#v", body)
	}
}

func TestAdminDeleteUserReturnsNotFoundForMissingTarget(t *testing.T) {
	router, token, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{{"admin-1", "root", "admin"}, {"admin-2", "ops", "admin"}}, "admin-1")
	req := httptest.NewRequest(http.MethodDelete, "/admin/users/missing", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 missing delete target, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestBenchmarkCompareValidationReturnsDiagnostics(t *testing.T) {
	router, token, sqlDB := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{{"admin-1", "root", "admin"}}, "admin-1")
	router.Route("/api/benchmarks", (&Handler{DB: sqlDB}).RegisterBenchmarks)
	cases := []struct {
		path string
		code string
	}{
		{"/api/benchmarks/compare", "BENCHMARK_COMPARE_RUN_IDS_REQUIRED"},
		{"/api/benchmarks/compare?runIds=run-1", "BENCHMARK_COMPARE_MIN_RUNS_REQUIRED"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 validation body, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			decodeJSONBody(t, w, &body)
			reason, _ := body["reason"].(string)
			if body["ok"] != false || body["code"] != tc.code || reason == "" || body["error"] != reason || body["message"] != reason {
				t.Fatalf("benchmark compare validation should expose diagnostics, got %#v", body)
			}
		})
	}
}

func TestBenchmarkResultRoutesRequireAdminBoundary(t *testing.T) {
	r := chi.NewRouter()
	h := &Handler{}
	r.Route("/api/benchmarks", h.RegisterBenchmarks)
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/benchmarks/runs/run-1/results"},
		{http.MethodGet, "/api/benchmarks/runs/run-1/summary"},
		{http.MethodGet, "/api/benchmarks/compare?runIds=run-1,run-2"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 admin boundary, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			decodeJSONBody(t, w, &body)
			if body["code"] != "FORBIDDEN" || body["error"] != "Admin access required" {
				t.Fatalf("unexpected forbidden body: %#v", body)
			}
		})
	}
}

func TestGatewayPublicWebhookFallbackIsRegistered(t *testing.T) {
	r := chi.NewRouter()
	h := &Handler{}
	r.Route("/api/gateway", h.RegisterGatewayPublic)

	req := httptest.NewRequest(http.MethodPost, "/api/gateway/webhook", strings.NewReader(`{"text":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 fallback, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["supported"] != false || body["fallback"] != true {
		t.Fatalf("expected supported=false fallback=true, got %#v", body)
	}
	if body["code"] != "GATEWAY_WEBHOOK_NOT_CONFIGURED" {
		t.Fatalf("unexpected code: %#v", body)
	}
	assertGatewayFallbackReason(t, body, "Webhook adapter is not configured in go_backend")
}

func TestGatewayWebhookRejectsInvalidSignature(t *testing.T) {
	t.Setenv("GATEWAY_WEBHOOK_SECRET", "test-secret")
	r := chi.NewRouter()
	h := &Handler{}
	r.Route("/api/gateway", h.RegisterGatewayPublic)

	req := httptest.NewRequest(http.MethodPost, "/api/gateway/webhook", strings.NewReader(`{"text":"hello"}`))
	req.Header.Set("X-Webhook-Signature", "bad-signature")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 invalid signature, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["code"] != "GATEWAY_WEBHOOK_INVALID_SIGNATURE" || body["supported"] != false {
		t.Fatalf("unexpected invalid signature body: %#v", body)
	}
	assertGatewayFallbackReason(t, body, "Invalid webhook signature")
}

func TestGatewayWebhookValidatesPayloadBeforeFallback(t *testing.T) {
	t.Setenv("GATEWAY_WEBHOOK_SECRET", "test-secret")
	r := chi.NewRouter()
	h := &Handler{}
	r.Route("/api/gateway", h.RegisterGatewayPublic)

	payload := `{"chat_id":"c1","user_id":"u1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/webhook", strings.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", "sha256="+gatewayTestSignature(payload, "test-secret"))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 missing text, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["code"] != "GATEWAY_WEBHOOK_MISSING_TEXT" {
		t.Fatalf("unexpected validation body: %#v", body)
	}
	assertGatewayFallbackReason(t, body, "Missing required webhook field: text")
}

func TestGatewayWebhookMappedNarratorNeedsRuntimeFallback(t *testing.T) {
	t.Setenv("GATEWAY_WEBHOOK_SECRET", "test-secret")
	sqlDB := openGatewayTestDB(t)
	defer sqlDB.Close()
	r := chi.NewRouter()
	h := &Handler{DB: sqlDB}
	r.Route("/api/gateway", h.RegisterGatewayPublic)

	payload := `{"chat_id":"chat-1","user_id":"user-1","username":"Ada","text":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/webhook", strings.NewReader(payload))
	req.Header.Set("X-Webhook-Signature", gatewayTestSignature(payload, "test-secret"))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 explicit fallback, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["code"] != "GATEWAY_WEBHOOK_RUNTIME_UNAVAILABLE" || body["narratorId"] != "narrator-1" {
		t.Fatalf("unexpected mapped fallback body: %#v", body)
	}
	assertGatewayFallbackReason(t, body, "Narrator runtime is not attached to the public gateway route")
}

func assertGatewayFallbackReason(t *testing.T, body map[string]any, expected string) {
	t.Helper()
	if body["reason"] != expected || body["error"] != expected || body["message"] != expected {
		t.Fatalf("gateway fallback should expose reason/error/message=%q, got %#v", expected, body)
	}
}

func TestGatewayWebhookCreatesStandaloneNarratorForNewSender(t *testing.T) {
	t.Setenv("GATEWAY_WEBHOOK_SECRET", "test-secret")
	sqlDB := openGatewayTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`DELETE FROM gateway_session_mappings`); err != nil {
		t.Fatalf("clear mappings: %v", err)
	}
	stub := &gatewayNarratorStub{nextID: "narrator-created"}
	r := chi.NewRouter()
	h := &Handler{DB: sqlDB, GatewayNarrators: stub}
	r.Route("/api/gateway", h.RegisterGatewayPublic)

	payload := `{"chat_id":"chat-new","user_id":"user-new","username":"Grace","text":"hello from webhook"}`
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/webhook", strings.NewReader(payload))
	req.Header.Set("X-Hub-Signature-256", "sha256="+gatewayTestSignature(payload, "test-secret"))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 webhook success, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["ok"] != true || body["fallback"] != false || body["chatId"] != "chat-new" || body["narratorId"] != "narrator-created" {
		t.Fatalf("unexpected webhook body: %#v", body)
	}
	if len(stub.created) != 1 || stub.created[0].Title == nil || *stub.created[0].Title != "Webhook: Grace" {
		t.Fatalf("expected standalone webhook narrator creation, got %#v", stub.created)
	}
	if len(stub.messages) != 1 || stub.messages[0].creator != "gateway:user-new" || stub.messages[0].text != "hello from webhook" {
		t.Fatalf("expected forwarded webhook message, got %#v", stub.messages)
	}
	var narratorID, username string
	if err := sqlDB.QueryRow(`SELECT narrator_id, username FROM gateway_session_mappings WHERE platform='webhook' AND chat_id='chat-new' AND user_id='user-new'`).Scan(&narratorID, &username); err != nil {
		t.Fatalf("query inserted mapping: %v", err)
	}
	if narratorID != "narrator-created" || username != "Grace" {
		t.Fatalf("unexpected inserted mapping narrator=%q username=%q", narratorID, username)
	}
}

type gatewayNarratorStub struct {
	nextID   string
	created  []narrators.CreateInput
	messages []gatewayNarratorMessage
}

type gatewayNarratorMessage struct {
	narratorID string
	creator    string
	text       string
}

func (s *gatewayNarratorStub) Create(_ context.Context, input narrators.CreateInput) (narrators.Narrator, error) {
	s.created = append(s.created, input)
	return narrators.Narrator{ID: s.nextID}, nil
}

func (s *gatewayNarratorStub) AddUserMessage(_ context.Context, narratorID, userID, text string) (narrators.Message, error) {
	s.messages = append(s.messages, gatewayNarratorMessage{narratorID: narratorID, creator: userID, text: text})
	return narrators.Message{ID: "message-1", NarratorID: narratorID}, nil
}

func TestNugQuotaEndpointCachesProviderQuota(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/quota" || r.Header.Get("Authorization") != "Bearer nug-key" {
			t.Fatalf("unexpected upstream request path=%s auth=%s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"balance":"12.5","totalGranted":"50"}`))
	}))
	defer upstream.Close()
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if _, err := store.Patch(map[string]any{"nugProviders": []any{map[string]any{"id": "nug-1", "name": "NUG One", "prefix": "nug", "baseUrl": upstream.URL, "apiKey": "nug-key"}}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	r := chi.NewRouter()
	h := &Handler{Settings: store}
	h.RegisterProviders(r)

	req := httptest.NewRequest(http.MethodGet, "/nug/providers/nug-1/quota", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected quota 200, got %d body=%s", w.Code, w.Body.String())
	}
	var quota map[string]any
	decodeJSONBody(t, w, &quota)
	if quota["balance"] != float64(12.5) || quota["totalGranted"] != float64(50) {
		t.Fatalf("unexpected normalized quota: %#v", quota)
	}

	req = httptest.NewRequest(http.MethodGet, "/nug/quotas", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected quotas 200, got %d body=%s", w.Code, w.Body.String())
	}
	var quotas map[string]map[string]any
	decodeJSONBody(t, w, &quotas)
	cached := quotas["nug-1"]
	if cached["balance"] != float64(12.5) || cached["totalGranted"] != float64(50) {
		t.Fatalf("unexpected cached quota: %#v", quotas)
	}
	if _, err := os.Stat(filepath.Join(home, ".narrafork", "nug-quotas.json")); err != nil {
		t.Fatalf("expected nug quota cache file: %v", err)
	}
}

func TestScanRuntimeMarksTerminalCountErrorsDegraded(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE terminals (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create terminals: %v", err)
	}
	h := &Handler{DB: sqlDB}
	req := httptest.NewRequest(http.MethodGet, "/api/runtime/scan", nil)
	w := httptest.NewRecorder()

	if err := h.scanRuntime(w, req); err != nil {
		t.Fatalf("scan runtime: %v", err)
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	terminals, _ := body["terminals"].(map[string]any)
	if terminals["degraded"] != true || terminals["fallback"] != true {
		t.Fatalf("expected degraded terminal payload, got %#v", terminals)
	}
	fallbacks, _ := terminals["fallbacks"].([]any)
	if len(fallbacks) == 0 || fallbacks[0] != "terminal_running_count_failed" {
		t.Fatalf("expected terminal count fallback, got %#v", terminals)
	}
	reason, _ := terminals["reason"].(string)
	if reason == "" || terminals["error"] != reason || terminals["message"] != reason {
		t.Fatalf("degraded terminal runtime payload should expose reason/error/message, got %#v", terminals)
	}
}

func TestScanRuntimeContainerFallbackExposesReason(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/runtime/scan", nil)
	w := httptest.NewRecorder()

	if err := h.scanRuntime(w, req); err != nil {
		t.Fatalf("scan runtime: %v", err)
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	containers, _ := body["containers"].(map[string]any)
	reason, _ := containers["reason"].(string)
	if containers["fallback"] != true || containers["supported"] != false || reason == "" || containers["error"] != reason || containers["message"] != reason {
		t.Fatalf("container runtime fallback should expose reason/error/message, got %#v", containers)
	}
}

func TestUsageHistoryStatsReturnsSummaryQueryErrors(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE api_requests (id TEXT PRIMARY KEY, created_at TEXT)`); err != nil {
		t.Fatalf("create api_requests: %v", err)
	}
	h := &Handler{DB: sqlDB}
	req := httptest.NewRequest(http.MethodGet, "/api/usage/history/stats", nil)
	w := httptest.NewRecorder()

	err = h.usageHistoryStats(w, req)
	if err == nil || !strings.Contains(err.Error(), "input_tokens") {
		t.Fatalf("expected usage summary query error, got %v", err)
	}
}

func TestGatewayStatusMarksConfiguredUserCountErrorsDegraded(t *testing.T) {
	sqlDB := openGatewayTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT)`); err != nil {
		t.Fatalf("create user_preferences: %v", err)
	}
	h := &Handler{DB: sqlDB}
	req := httptest.NewRequest(http.MethodGet, "/api/gateway/status", nil)
	w := httptest.NewRecorder()

	if err := h.gatewayStatus(w, req); err != nil {
		t.Fatalf("gateway status: %v", err)
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["degraded"] != true || body["fallback"] != true {
		t.Fatalf("expected degraded gateway status, got %#v", body)
	}
	fallbacks, _ := body["fallbacks"].([]any)
	found := false
	for _, fallback := range fallbacks {
		if fallback == "gateway_configured_users_count_failed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected configured users fallback, got %#v", body)
	}
	reason, _ := body["reason"].(string)
	if reason == "" || body["error"] != reason || body["message"] != reason {
		t.Fatalf("gateway status fallback should expose reason/error/message, got %#v", body)
	}
	webhook, _ := body["webhook"].(map[string]any)
	webhookReason, _ := webhook["reason"].(string)
	if webhook["fallback"] != true || webhookReason == "" || webhook["error"] != webhookReason || webhook["message"] != webhookReason {
		t.Fatalf("gateway webhook status fallback should expose reason/error/message, got %#v", webhook)
	}
}

func TestGatewayStatusFallbackIsExplicitWithoutDegradation(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/gateway/status", nil)
	w := httptest.NewRecorder()

	if err := h.gatewayStatus(w, req); err != nil {
		t.Fatalf("gateway status: %v", err)
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["supported"] != false || body["fallback"] != true {
		t.Fatalf("expected explicit gateway status fallback, got %#v", body)
	}
	assertGatewayFallbackReason(t, body, gatewayRuntimeFallbackReason)
	webhook, _ := body["webhook"].(map[string]any)
	if webhook["supported"] != false || webhook["fallback"] != true {
		t.Fatalf("expected explicit webhook status fallback, got %#v", webhook)
	}
	assertGatewayFallbackReason(t, webhook, gatewayWebhookFallbackReason)
}

func TestGatewayReloadFallbackIsExplicit(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/gateway/reload", nil)
	w := httptest.NewRecorder()

	if err := h.gatewayReload(w, req); err != nil {
		t.Fatalf("gateway reload: %v", err)
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	status, _ := body["status"].(map[string]any)
	if body["ok"] != true || status["supported"] != false || status["fallback"] != true {
		t.Fatalf("expected explicit gateway reload fallback, got %#v", body)
	}
	assertGatewayFallbackReason(t, status, gatewayRuntimeFallbackReason)
	webhook, _ := status["webhook"].(map[string]any)
	if webhook["supported"] != false || webhook["fallback"] != true {
		t.Fatalf("expected explicit webhook reload fallback, got %#v", webhook)
	}
	assertGatewayFallbackReason(t, webhook, gatewayWebhookFallbackReason)
}

func TestUpdateGatewayLastMessageReturnsUpdateErrors(t *testing.T) {
	sqlDB := openGatewayTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_gateway_last_message_update BEFORE UPDATE OF last_message_at ON gateway_session_mappings BEGIN SELECT RAISE(ABORT, 'gateway last message update blocked'); END;`); err != nil {
		t.Fatalf("create gateway update trigger: %v", err)
	}
	h := &Handler{DB: sqlDB}

	err := h.updateGatewayLastMessage(context.Background(), "mapping-1")
	if err == nil || !strings.Contains(err.Error(), "gateway last message update blocked") {
		t.Fatalf("expected gateway last message update error, got %v", err)
	}
}

func TestGatewayWeixinQRFallbackIsExplicit(t *testing.T) {
	r := chi.NewRouter()
	h := &Handler{}
	r.Route("/api/gateway", h.RegisterGateway)

	req := httptest.NewRequest(http.MethodPost, "/api/gateway/weixin/qr-start", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 QR fallback, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["supported"] != false || body["fallback"] != true || body["code"] != "WEIXIN_QR_UNSUPPORTED" || body["reason"] != gatewayWeixinQRUnsupportedReason || body["error"] != gatewayWeixinQRUnsupportedReason || body["message"] != gatewayWeixinQRUnsupportedReason {
		t.Fatalf("unexpected QR fallback body: %#v", body)
	}
}

func TestProviderRoutesReturnCachedLongTailShapesWithoutRefresh(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	_, err := store.Patch(map[string]any{
		"openaiProviders": []any{map[string]any{"id": "oa", "name": "OpenAI", "prefix": "openai"}},
		"nkpProviders":    []any{map[string]any{"id": "nkp", "name": "NKP", "prefix": "nkp", "baseUrl": "https://example.invalid", "apiKey": "secret"}},
	})
	if err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.WriteFile(filepath.Join(dataDir, "openai-models-providers.json"), []byte(`{"oa":[{"id":"gpt-test"}]}`), 0o600); err != nil {
		t.Fatalf("write openai cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "nkp-models-providers.json"), []byte(`{"nkp":[{"model_id":"claude-test","model_short_name":"Claude Test"}]}`), 0o600); err != nil {
		t.Fatalf("write nkp cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "nkp-quotas.json"), []byte(`{"nkp":{"quotaBalance":10,"quotaTotalGranted":20}}`), 0o600); err != nil {
		t.Fatalf("write nkp quota cache: %v", err)
	}

	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	cases := []struct {
		path string
		want string
	}{
		{"/openai/models", "models"},
		{"/nkp/models", "models"},
		{"/nkp/quotas", "nkp"},
		{"/kiro/status", "snapshot"},
		{"/codex/status", "entries"},
		{"/cline/recommended-models", "recommended"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if _, ok := body[tc.want]; !ok {
				t.Fatalf("response missing %q: %#v", tc.want, body)
			}
		})
	}
}

func TestKiroMCPRetriesSuspiciousThrottleOnLastCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeKiroTestCredentials(t, []map[string]any{{"id": "only", "accessToken": "access-only"}})
	originalClient := kiroMCPHTTPClient
	defer func() { kiroMCPHTTPClient = originalClient }()

	requestedTokens := []string{}
	kiroMCPHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestedTokens = append(requestedTokens, req.Header.Get("Authorization"))
		if len(requestedTokens) == 1 {
			return kiroTestResponse(http.StatusTooManyRequests, `{"__type":"ThrottlingException","message":"Suspicious activity detected"}`), nil
		}
		return kiroTestResponse(http.StatusOK, `{"ok":true}`), nil
	})}

	result, err := (&Handler{}).callKiroMCP(context.Background(), "tools/list", map[string]any{})
	if err != nil {
		t.Fatalf("callKiroMCP returned error: %v", err)
	}
	if result["ok"] != true {
		t.Fatalf("unexpected MCP result: %#v", result)
	}
	if strings.Join(requestedTokens, ",") != "Bearer access-only,Bearer access-only" {
		t.Fatalf("expected retry with same last credential, got %#v", requestedTokens)
	}
	entries := readKiroTestCredentials(t)
	if len(entries) != 1 || boolMap(entries[0], "disabled") || stringMap(entries[0], "disabledReason", "") != "" {
		t.Fatalf("last available credential should remain enabled, got %#v", entries)
	}
}

func TestKiroMCPDisablesSuspiciousThrottleWhenFailoverAvailable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeKiroTestCredentials(t, []map[string]any{
		{"id": "first", "accessToken": "access-first"},
		{"id": "second", "accessToken": "access-second"},
	})
	originalClient := kiroMCPHTTPClient
	defer func() { kiroMCPHTTPClient = originalClient }()

	requestedTokens := []string{}
	kiroMCPHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requestedTokens = append(requestedTokens, req.Header.Get("Authorization"))
		if len(requestedTokens) == 1 {
			return kiroTestResponse(http.StatusTooManyRequests, `{"__type":"ThrottlingException","message":"Suspicious activity detected"}`), nil
		}
		return kiroTestResponse(http.StatusOK, `{"ok":true}`), nil
	})}

	result, err := (&Handler{}).callKiroMCP(context.Background(), "tools/list", map[string]any{})
	if err != nil {
		t.Fatalf("callKiroMCP returned error: %v", err)
	}
	if result["ok"] != true {
		t.Fatalf("unexpected MCP result: %#v", result)
	}
	if strings.Join(requestedTokens, ",") != "Bearer access-first,Bearer access-second" {
		t.Fatalf("expected failover to second credential, got %#v", requestedTokens)
	}
	entries := readKiroTestCredentials(t)
	if len(entries) != 2 || !boolMap(entries[0], "disabled") || stringMap(entries[0], "disabledReason", "") != "suspicious_throttle" || stringMap(entries[0], "disabledAt", "") == "" {
		t.Fatalf("first credential should be suspicious-throttle cooled down, got %#v", entries)
	}
}

func TestLoadKiroCredentialsRecoversExpiredSuspiciousThrottle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeKiroTestCredentials(t, []map[string]any{{
		"id":             "recover",
		"accessToken":    "access-recover",
		"disabled":       true,
		"disabledReason": "suspicious_throttle",
		"disabledAt":     time.Now().Add(-kiroSuspiciousThrottleCooldown - time.Minute).Format(time.RFC3339),
	}})

	entries, err := (&Handler{}).loadKiroCredentials()
	if err != nil {
		t.Fatalf("loadKiroCredentials returned error: %v", err)
	}
	if len(entries) != 1 || boolMap(entries[0], "disabled") || stringMap(entries[0], "disabledReason", "") != "" || stringMap(entries[0], "disabledAt", "") != "" {
		t.Fatalf("expected expired suspicious throttle to recover, got %#v", entries)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func kiroTestResponse(status int, body string) *http.Response {
	statusText := http.StatusText(status)
	if statusText == "" {
		statusText = "status"
	}
	return &http.Response{
		StatusCode: status,
		Status:     strconv.Itoa(status) + " " + statusText,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func writeKiroTestCredentials(t *testing.T, entries []map[string]any) {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".narrafork", "kiro", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir kiro credentials dir: %v", err)
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal kiro credentials: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write kiro credentials: %v", err)
	}
}

func readKiroTestCredentials(t *testing.T) []map[string]any {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".narrafork", "kiro", "credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read kiro credentials: %v", err)
	}
	entries := []map[string]any{}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("decode kiro credentials: %v", err)
	}
	return entries
}

func TestCodexImportAcceptsAccessTokenOnlyCredential(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	accessToken := testJWT(map[string]any{
		"sub":                "user-access-only",
		"email":              "access-only@example.com",
		"chatgpt_account_id": "acc-access-only",
	})
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	payload, _ := json.Marshal(map[string]any{"credentials": []any{map[string]any{
		"refresh_token": "",
		"access_token":  accessToken,
	}}})
	req := httptest.NewRequest(http.MethodPost, "/codex/import", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["added"] != float64(1) || body["skipped"] != float64(0) {
		t.Fatalf("unexpected import result: %#v", body)
	}
	entries, err := loadCodexCredentials()
	if err != nil || len(entries) != 1 {
		t.Fatalf("load codex credentials len=%d err=%v", len(entries), err)
	}
	entry := entries[0]
	if stringMap(entry, "refreshToken", "") != "" || stringMap(entry, "accessToken", "") != accessToken {
		t.Fatalf("unexpected imported token fields: %#v", entry)
	}
	if stringMap(entry, "accountId", "") != "acc-access-only" || stringMap(entry, "email", "") != "access-only@example.com" {
		t.Fatalf("expected JWT metadata extraction, got %#v", entry)
	}
}

func TestCodexImportParsesAtMarkerAccountLine(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	payload, _ := json.Marshal(map[string]any{"credentials": []any{
		"marker@example.com----password----at----actual-access-token",
	}})
	req := httptest.NewRequest(http.MethodPost, "/codex/import", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	entries, err := loadCodexCredentials()
	if err != nil || len(entries) != 1 {
		t.Fatalf("load codex credentials len=%d err=%v", len(entries), err)
	}
	entry := entries[0]
	if stringMap(entry, "accessToken", "") != "actual-access-token" || stringMap(entry, "email", "") != "marker@example.com" {
		t.Fatalf("unexpected marker import entry: %#v", entry)
	}
	if stringMap(entry, "refreshToken", "") != "" {
		t.Fatalf("marker access-token import should not set refresh token: %#v", entry)
	}
}

func TestCodexImportParsesSub2ApiAccountExport(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	expiresAt := "2026-05-28T14:06:40.000Z"
	importText, _ := json.Marshal(map[string]any{
		"exported_at": "2026-05-18T14:06:47.807Z",
		"accounts": []any{map[string]any{
			"name":     "Sub2Api Plus Account",
			"platform": "openai",
			"type":     "oauth",
			"priority": float64(4),
			"credentials": map[string]any{
				"access_token":       "access-token-from-sub2api",
				"chatgpt_account_id": "acc-sub2api",
				"chatgpt_user_id":    "user-sub2api",
				"email":              "sub2api@example.com",
				"expires_at":         expiresAt,
			},
		}},
	})
	payload, _ := json.Marshal(map[string]any{"importText": string(importText)})
	req := httptest.NewRequest(http.MethodPost, "/codex/import", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	entries, err := loadCodexCredentials()
	if err != nil || len(entries) != 1 {
		t.Fatalf("load codex credentials len=%d err=%v", len(entries), err)
	}
	entry := entries[0]
	if stringMap(entry, "accessToken", "") != "access-token-from-sub2api" || stringMap(entry, "accountId", "") != "acc-sub2api" {
		t.Fatalf("unexpected sub2api import token fields: %#v", entry)
	}
	if stringMap(entry, "email", "") != "sub2api@example.com" || stringMap(entry, "sub", "") != "user-sub2api" {
		t.Fatalf("unexpected sub2api import metadata: %#v", entry)
	}
	if stringMap(entry, "displayName", "") != "Sub2Api Plus Account" || numericMapOr(entry, "priority", 0) != 4 {
		t.Fatalf("unexpected sub2api display fields: %#v", entry)
	}
	parsedExpiresAt, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		t.Fatalf("parse expiresAt: %v", err)
	}
	if numericMapOr(entry, "expiresAt", 0) != float64(parsedExpiresAt.UnixMilli()) {
		t.Fatalf("unexpected sub2api expiresAt: %#v", entry)
	}
}

func TestClineEnabledModelsStoresContextWindowsFromCache(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	provider := map[string]any{"id": "cline1", "name": "Cline", "prefix": "cline", "enabledModels": []any{}}
	_, err := store.Patch(map[string]any{"clineProviders": []any{provider}})
	if err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.WriteFile(filepath.Join(dataDir, "cline-models.json"), []byte(`{"cline1":[{"id":"anthropic/claude-test","contextLength":200000}]}`), 0o600); err != nil {
		t.Fatalf("write cline cache: %v", err)
	}
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	payload := []byte(`{"models":["anthropic/claude-test"]}`)
	req := httptest.NewRequest(http.MethodPost, "/cline/enabled-models", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	agent, _ := store.Raw()["agent"].(map[string]any)
	windows, _ := agent["modelContextWindows"].(map[string]any)
	if windows["cline:anthropic/claude-test"] != float64(200000) {
		t.Fatalf("context window not stored: %#v", windows)
	}
}

func openGatewayTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = sqlDB.Exec(`CREATE TABLE gateway_session_mappings (
		id TEXT PRIMARY KEY,
		platform TEXT NOT NULL,
		chat_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		username TEXT,
		narrator_id TEXT NOT NULL,
		app_user_id TEXT,
		project_id TEXT,
		chapter_id TEXT,
		last_message_at TEXT,
		created_at TEXT,
		updated_at TEXT
	)`)
	if err != nil {
		t.Fatalf("create gateway_session_mappings: %v", err)
	}
	_, err = sqlDB.Exec(`CREATE TABLE narrators (id TEXT PRIMARY KEY)`)
	if err != nil {
		t.Fatalf("create narrators: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO narrators (id) VALUES ('narrator-1')`)
	if err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO gateway_session_mappings (id, platform, chat_id, user_id, username, narrator_id, app_user_id, last_message_at, created_at, updated_at)
		VALUES ('mapping-1', 'webhook', 'chat-1', 'user-1', 'Ada', 'narrator-1', 'app-user-1', 'now', 'now', 'now')`)
	if err != nil {
		t.Fatalf("insert mapping: %v", err)
	}
	return sqlDB
}

func gatewayTestSignature(payload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func testJWT(payload map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	body, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body) + ".signature"
}

func decodeJSONBody(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
