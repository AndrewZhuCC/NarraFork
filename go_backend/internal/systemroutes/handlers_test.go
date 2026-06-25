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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
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

func TestProviderModelAndNugRoutesRemainAuthenticatedUserCompatible(t *testing.T) {
	router, userToken := newProviderRoutesTestRouter(t)
	cases := []struct {
		method     string
		path       string
		wantStatus int
	}{
		{http.MethodPost, "/openai/models/refresh", http.StatusOK},
		{http.MethodPost, "/openai/providers/openai-test/models/refresh", http.StatusNotFound},
		{http.MethodPost, "/anthropic/models/refresh", http.StatusOK},
		{http.MethodPost, "/anthropic/providers/anthropic-test/models/refresh", http.StatusNotFound},
		{http.MethodPost, "/nkp/models/refresh", http.StatusOK},
		{http.MethodPost, "/nkp/providers/nkp-test/models/refresh", http.StatusNotFound},
		{http.MethodPost, "/nug/models/refresh", http.StatusOK},
		{http.MethodPost, "/nug/providers/nug-test/models/refresh", http.StatusNotFound},
		{http.MethodPost, "/nug/providers/nug-test/login", http.StatusNotFound},
		{http.MethodPost, "/nug/providers/nug-test/api-key", http.StatusNotFound},
		{http.MethodGet, "/nug/providers/nug-test/oauth/start", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+userToken)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("expected %d for authenticated user, got %d body=%s", tc.wantStatus, w.Code, w.Body.String())
			}
			if w.Code == http.StatusForbidden {
				t.Fatalf("route should match TS authenticated-user boundary, got forbidden body=%s", w.Body.String())
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

func TestKiroModelsRefreshReturnsExplicitFeatureDisabledFallback(t *testing.T) {
	router, adminToken := newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "admin")
	req := httptest.NewRequest(http.MethodPost, "/kiro/models/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 feature disabled, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "providers.kiro.modelsRefresh" || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected Kiro models refresh fallback body: %#v", body)
	}
	reason, _ := body["reason"].(string)
	if !strings.Contains(reason, "Kiro model refresh requires the TypeScript Kiro plugin bridge") || body["error"] != reason || body["message"] != reason {
		t.Fatalf("Kiro models refresh fallback should expose reason/error/message, got %#v", body)
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
		t.Fatalf("expected fallback 200 for Kiro MCP search, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	reason, _ := body["reason"].(string)
	errorPayload, _ := body["error"].(map[string]any)
	if body["fallback"] != true || !strings.Contains(reason, "Kiro MCP search unavailable in go_backend") || body["message"] != reason || errorPayload["message"] != reason {
		t.Fatalf("Kiro MCP search fallback should expose reason/message and JSON-RPC error, got %#v", body)
	}
}

func TestKiroMcpPopulatedBridgeMatchesFrontendShapeAndTSHeaders(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	writeKiroTestCredentials(t, []map[string]any{{
		"id":          "kiro-live",
		"accessToken": "access-live",
		"region":      "credential-region",
		"profileArn":  "arn:aws:codewhisperer:profile/test",
	}})
	writeKiroTestConfig(t, map[string]any{
		"region":        "config-region",
		"apiRegion":     "api-region",
		"kiroVersion":   "9.9.9",
		"systemVersion": "darwin#14.4",
	})

	originalClient := kiroMCPHTTPClient
	defer func() { kiroMCPHTTPClient = originalClient }()
	type capturedRequest struct {
		URL        string
		Auth       string
		UserAgent  string
		Attempt    string
		Invocation string
		OS         string
		OSVersion  string
		Body       map[string]any
	}
	captured := []capturedRequest{}
	kiroMCPHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode Kiro MCP request body: %v", err)
		}
		captured = append(captured, capturedRequest{
			URL:        req.URL.String(),
			Auth:       req.Header.Get("Authorization"),
			UserAgent:  req.Header.Get("User-Agent"),
			Attempt:    req.Header.Get("amz-sdk-request"),
			Invocation: req.Header.Get("amz-sdk-invocation-id"),
			OS:         req.Header.Get("x-amzn-codewhisperer-os"),
			OSVersion:  req.Header.Get("x-amzn-codewhisperer-os-version"),
			Body:       body,
		})
		if body["method"] == "tools/list" {
			return kiroTestResponse(http.StatusOK, `{"jsonrpc":"2.0","id":"tools-id","result":{"tools":[{"name":"web_search","description":"Search web","inputSchema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}]}}`), nil
		}
		return kiroTestResponse(http.StatusOK, `{"jsonrpc":"2.0","id":"search-id","result":{"content":[{"type":"text","text":"search result"}],"isError":false}}`), nil
	})}

	router, adminToken := newProviderRoutesTestRouterWithStore(t, store, "admin")
	req := httptest.NewRequest(http.MethodGet, "/kiro/mcp-tools", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mcp-tools status = %d body=%s", w.Code, w.Body.String())
	}
	var toolsBody map[string]any
	decodeJSONBody(t, w, &toolsBody)
	tools, _ := toolsBody["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "web_search" {
		t.Fatalf("populated mcp-tools should mirror result.tools at top level for frontend, got %#v", toolsBody)
	}
	resultPayload, _ := toolsBody["result"].(map[string]any)
	resultTools, _ := resultPayload["tools"].([]any)
	if len(resultTools) != 1 {
		t.Fatalf("populated mcp-tools should preserve JSON-RPC result.tools, got %#v", toolsBody)
	}

	req = httptest.NewRequest(http.MethodPost, "/kiro/mcp-search", strings.NewReader(`{"query":"contract parity"}`))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mcp-search status = %d body=%s", w.Code, w.Body.String())
	}
	var searchBody map[string]any
	decodeJSONBody(t, w, &searchBody)
	searchResult, _ := searchBody["result"].(map[string]any)
	content, _ := searchResult["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "search result" {
		t.Fatalf("populated mcp-search should return raw JSON-RPC tool-call result, got %#v", searchBody)
	}

	if len(captured) != 2 {
		t.Fatalf("expected two Kiro MCP upstream requests, got %#v", captured)
	}
	for idx, req := range captured {
		if req.URL != "https://q.api-region.amazonaws.com" || req.Auth != "Bearer access-live" || req.UserAgent != "AmazonQ-For-CLI/Version/9.9.9" || req.OS != "darwin" || req.OSVersion != "14.4" || req.Attempt != "attempt=1; max=3" || req.Invocation == "" {
			t.Fatalf("request %d did not match TS Kiro MCP headers/region: %#v", idx, req)
		}
		if req.Body["profileArn"] != "arn:aws:codewhisperer:profile/test" {
			t.Fatalf("request %d should include profileArn from credential, got %#v", idx, req.Body)
		}
	}
	if captured[0].Body["method"] != "tools/list" {
		t.Fatalf("first request method = %#v", captured[0].Body)
	}
	params, _ := captured[1].Body["params"].(map[string]any)
	arguments, _ := params["arguments"].(map[string]any)
	if captured[1].Body["method"] != "tools/call" || params["name"] != "web_search" || arguments["query"] != "contract parity" {
		t.Fatalf("search request should call web_search with query, got %#v", captured[1].Body)
	}
}

func TestNugLoginReturnsAPIKeyWithoutMutatingSettings(t *testing.T) {

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
	if provider["apiKey"] != nil || provider["nugUsername"] != nil || provider["nugUserId"] != nil {
		t.Fatalf("login route should not persist credentials; frontend saves explicitly, got %#v", provider)
	}
}

func TestNugAuthMissingFieldsMatchTSValidation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("missing-field validation should not call upstream: %s", r.URL.Path)
	}))
	defer upstream.Close()

	store := newSystemRoutesTestStore(t)
	if _, err := store.Patch(map[string]any{
		"nugProviders": []any{map[string]any{
			"id":      "nug-login-fixture",
			"name":    "NUG Login Fixture",
			"baseUrl": upstream.URL,
			"apiKey":  "",
		}},
	}); err != nil {
		t.Fatalf("seed nug provider: %v", err)
	}
	router, userToken := newProviderRoutesTestRouterWithStore(t, store, "user")

	cases := []struct {
		name       string
		path       string
		body       string
		error      string
		statusCode int
	}{
		{name: "login missing credentials", path: "/nug/providers/nug-login-fixture/login", body: `{}`, error: "Missing username or password", statusCode: http.StatusBadRequest},
		{name: "api key missing session", path: "/nug/providers/nug-login-fixture/api-key", body: `{}`, error: "Missing sessionToken", statusCode: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+userToken)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.statusCode {
				t.Fatalf("expected %d, got %d body=%s", tc.statusCode, w.Code, w.Body.String())
			}
			var response map[string]any
			decodeJSONBody(t, w, &response)
			if response["error"] != tc.error {
				t.Fatalf("unexpected validation body: %#v", response)
			}
		})
	}
}

func TestNugOAuthCallbackPersistsCredentialsAndRedirects(t *testing.T) {
	nugOAuthMu.Lock()
	nugOAuthStates = map[string]nugOAuthState{}
	nugOAuthMu.Unlock()
	t.Cleanup(func() {
		nugOAuthMu.Lock()
		nugOAuthStates = map[string]nugOAuthState{}
		nugOAuthMu.Unlock()
	})

	var tokenRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/oauth/token":
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected token method: %s", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&tokenRequest); err != nil {
				t.Fatalf("decode token body: %v", err)
			}
			if tokenRequest["code"] != "oauth-code" || tokenRequest["client_id"] != "client-id" || tokenRequest["client_secret"] != "client-secret" {
				t.Fatalf("unexpected token request: %#v", tokenRequest)
			}
			if tokenRequest["redirect_uri"] != "https://narrafork.example/api/nug/oauth/callback" {
				t.Fatalf("callback redirect_uri should match oauth/start value, got %#v", tokenRequest)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"api_key":   "nug-oauth-api-key",
				"device_id": "nug-oauth-device",
				"username":  "oauth-alice",
				"user_id":   "nug-oauth-user-1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := newSystemRoutesTestStore(t)
	if _, err := store.Patch(map[string]any{
		"nugProviders": []any{map[string]any{
			"id":                "nug-oauth",
			"name":              "NUG OAuth",
			"prefix":            "nugoauth",
			"apiKey":            "",
			"baseUrl":           upstream.URL,
			"oauthClientId":     "client-id",
			"oauthClientSecret": "client-secret",
		}},
	}); err != nil {
		t.Fatalf("seed nug provider: %v", err)
	}

	router, adminToken := newProviderRoutesTestRouterWithStore(t, store, "admin")
	startReq := httptest.NewRequest(http.MethodGet, "/nug/providers/nug-oauth/oauth/start", nil)
	startReq.Header.Set("Authorization", "Bearer "+adminToken)
	startReq.Header.Set("X-Forwarded-Host", "narrafork.example")
	startReq.Header.Set("X-Forwarded-Proto", "https")
	startW := httptest.NewRecorder()
	router.ServeHTTP(startW, startReq)
	if startW.Code != http.StatusOK {
		t.Fatalf("expected oauth start 200, got %d body=%s", startW.Code, startW.Body.String())
	}
	var startBody map[string]any
	decodeJSONBody(t, startW, &startBody)
	state, _ := startBody["state"].(string)
	if state == "" {
		t.Fatalf("missing oauth state: %#v", startBody)
	}
	authorizeURL, _ := url.Parse(startBody["authorizeUrl"].(string))
	if authorizeURL.Path != "/oauth/authorize" || authorizeURL.Query().Get("client_id") != "client-id" || authorizeURL.Query().Get("state") != state || authorizeURL.Query().Get("redirect_uri") != "https://narrafork.example/api/nug/oauth/callback" {
		t.Fatalf("unexpected authorize URL: %#v", startBody)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/nug/oauth/callback?code=oauth-code&state="+url.QueryEscape(state), nil)
	callbackW := httptest.NewRecorder()
	callbackHandler := &Handler{Settings: store}
	if err := callbackHandler.NugOAuthCallback(callbackW, callbackReq); err != nil {
		t.Fatalf("oauth callback: %v", err)
	}
	if callbackW.Code != http.StatusFound {
		t.Fatalf("expected callback redirect, got %d body=%s", callbackW.Code, callbackW.Body.String())
	}
	if got := callbackW.Header().Get("Location"); got != "/settings/providers?oauth_success=nug-oauth" {
		t.Fatalf("unexpected callback Location: %q", got)
	}

	raw := store.Raw()
	providers, _ := raw["nugProviders"].([]any)
	provider, _ := providers[0].(map[string]any)
	if provider["apiKey"] != "nug-oauth-api-key" || provider["oauthDeviceId"] != "nug-oauth-device" || provider["nugUsername"] != "oauth-alice" || provider["nugUserId"] != "nug-oauth-user-1" {
		t.Fatalf("expected persisted oauth credentials, got %#v", provider)
	}
}

func TestCodexUsageQueueClearReturnsFeatureDisabledWithoutQueueRuntime(t *testing.T) {
	router, adminToken := newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "admin")
	req := httptest.NewRequest(http.MethodPost, "/codex/usage-queue/clear", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 feature disabled, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "providers.codex.usageQueueClear" || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected Codex usage queue clear body: %#v", body)
	}
}

func TestCodexStatusPaginatesAvailableAndUnavailableEntries(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	if _, err := store.Patch(map[string]any{"codex": map[string]any{
		"proxy":                  "http://proxy.example.test:8080",
		"loadBalancingMode":      "balanced",
		"defaultReasoningEffort": "high",
		"useWebSocket":           false,
		"useWebSearch":           false,
		"useImageGeneration":     false,
		"tierOrder":              []any{"team", "pro", "invalid", "team"},
	}}); err != nil {
		t.Fatalf("patch codex settings: %v", err)
	}
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
			"accessToken": "available-access-" + strconv.Itoa(i),
			"accountId":   "available-account-" + strconv.Itoa(i),
			"email":       "available-" + strconv.Itoa(i) + "@example.com",
			"sub":         "available-sub-" + strconv.Itoa(i),
			"usage":       map[string]any{"plan_type": "pro", "remaining_percent": 80 - i},
		})
	}
	for i := 0; i < 3; i++ {
		credentials = append(credentials, map[string]any{
			"id":          "unavailable-" + strconv.Itoa(i),
			"displayName": "Unavailable " + strconv.Itoa(i),
			"accessToken": "unavailable-access-" + strconv.Itoa(i),
			"accountId":   "unavailable-account-" + strconv.Itoa(i),
			"email":       "unavailable-" + strconv.Itoa(i) + "@example.com",
			"sub":         "unavailable-sub-" + strconv.Itoa(i),
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
	if body["availableTotal"] != float64(25) || body["unavailableTotal"] != float64(3) || body["total"] != float64(28) || body["available"] != float64(25) {
		t.Fatalf("unexpected totals: available=%#v unavailable=%#v total=%#v", body["availableTotal"], body["unavailableTotal"], body["total"])
	}
	if body["globalProxy"] != "http://proxy.example.test:8080" || body["proxy"] != body["globalProxy"] {
		t.Fatalf("status should expose frontend globalProxy alias and legacy proxy: %#v", body)
	}
	if body["loadBalancingMode"] != "balanced" || body["defaultReasoningEffort"] != "high" || body["useWebSocket"] != false || body["useWebSearch"] != false || body["useImageGeneration"] != false {
		t.Fatalf("status should expose codex settings fields: %#v", body)
	}
	tierOrder, _ := body["tierOrder"].([]any)
	if strings.Join(anyStrings(tierOrder), ",") != "team,pro" {
		t.Fatalf("unexpected sanitized tier order: %#v", tierOrder)
	}
	effectiveTierOrder, _ := body["effectiveTierOrder"].([]any)
	if strings.Join(anyStrings(effectiveTierOrder), ",") != "team,pro,prolite,plus,free,other" {
		t.Fatalf("unexpected effective tier order: %#v", effectiveTierOrder)
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

// TestPaginateCodexStatusEntriesMatchesTSSlicePage pins paginateCodexStatusEntries
// to the TS codex-manager slicePage contract: pageSize<=0 or page<1 disables
// pagination (returns all), and an out-of-range high page clamps to the LAST
// page (safePage = min(page, maxPage)) rather than returning an empty slice.
func TestPaginateCodexStatusEntriesMatchesTSSlicePage(t *testing.T) {
	mk := func(n int) []map[string]any {
		out := make([]map[string]any, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, map[string]any{"id": strconv.Itoa(i)})
		}
		return out
	}
	ids := func(entries []map[string]any) []string {
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, e["id"].(string))
		}
		return out
	}
	join := func(s []string) string { return strings.Join(s, ",") }

	five := mk(5)

	// pageSize <= 0 -> no pagination (full list), regardless of page.
	if got := paginateCodexStatusEntries(five, 3, 0); join(ids(got)) != "0,1,2,3,4" {
		t.Fatalf("pageSize=0 should return all entries, got %v", ids(got))
	}
	// page < 1 -> no pagination (full list).
	if got := paginateCodexStatusEntries(five, 0, 2); join(ids(got)) != "0,1,2,3,4" {
		t.Fatalf("page=0 should return all entries, got %v", ids(got))
	}
	// page 1, pageSize 2 -> first page.
	if got := paginateCodexStatusEntries(five, 1, 2); join(ids(got)) != "0,1" {
		t.Fatalf("page 1 size 2, got %v", ids(got))
	}
	// page 3, pageSize 2 -> last partial page (entry 4).
	if got := paginateCodexStatusEntries(five, 3, 2); join(ids(got)) != "4" {
		t.Fatalf("page 3 size 2 should be last page [4], got %v", ids(got))
	}
	// Out-of-range high page must clamp to the LAST page, not return empty
	// (the key TS parity fix): maxPage = ceil(5/2) = 3 -> page 99 clamps to 3 -> [4].
	if got := paginateCodexStatusEntries(five, 99, 2); join(ids(got)) != "4" {
		t.Fatalf("out-of-range page should clamp to last page [4], got %v", ids(got))
	}
	// Exact multiple: 4 entries, pageSize 2, page 99 -> maxPage 2 -> [2,3].
	if got := paginateCodexStatusEntries(mk(4), 99, 2); join(ids(got)) != "2,3" {
		t.Fatalf("out-of-range page on exact multiple should be last page [2,3], got %v", ids(got))
	}
	// Empty input with pagination -> empty slice (maxPage clamps to 1, start 0 >= 0).
	if got := paginateCodexStatusEntries([]map[string]any{}, 5, 2); len(got) != 0 {
		t.Fatalf("empty input should stay empty, got %v", ids(got))
	}
}

func TestCodexStatusFiltersInvalidAndDuplicatePersistedCredentials(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	dataDir, err := narraforkDataDir()
	if err != nil {
		t.Fatalf("data dir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	credentials := []map[string]any{
		{
			"id":           "codex-contract",
			"displayName":  "Codex Contract Fixture",
			"accessToken":  "codex-access-fixture",
			"refreshToken": "codex-refresh-fixture",
			"accountId":    "codex-account-fixture",
			"email":        "codex-contract@example.com",
			"sub":          "codex-sub-fixture",
		},
		{
			"id":          "codex-invalid-no-token",
			"displayName": "Codex Invalid No Token",
		},
		{
			"id":           "codex-duplicate-refresh",
			"displayName":  "Codex Duplicate Refresh",
			"accessToken":  "codex-duplicate-access",
			"refreshToken": "codex-refresh-fixture",
			"accountId":    "codex-duplicate-account",
			"email":        "codex-duplicate@example.com",
			"sub":          "codex-duplicate-sub",
		},
	}
	data, _ := json.Marshal(credentials)
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), data, 0o600); err != nil {
		t.Fatalf("write codex credentials: %v", err)
	}

	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	req := httptest.NewRequest(http.MethodGet, "/codex/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 ok, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["total"] != float64(1) || body["availableTotal"] != float64(1) || body["currentId"] != "codex-contract" {
		t.Fatalf("unexpected filtered codex status: %#v", body)
	}
	entries, _ := body["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected one normalized credential, got %#v", entries)
	}
	entry, _ := entries[0].(map[string]any)
	if entry["id"] != "codex-contract" || entry["disabled"] != false || entry["priority"] != float64(0) {
		t.Fatalf("unexpected normalized credential: %#v", entry)
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

func TestCodexSettingsMutationsMatchTSValidation(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	router, adminToken := newProviderRoutesTestRouterWithStore(t, store, "admin")
	post := func(path, payload string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var body map[string]any
		decodeJSONBody(t, w, &body)
		return w.Code, body
	}

	status, body := post("/codex/use-websocket", `{}`)
	if status != http.StatusOK || body["ok"] != true || body["useWebSocket"] != true {
		t.Fatalf("missing useWebSocket should default true like TS, status=%d body=%#v", status, body)
	}
	status, body = post("/codex/use-web-search", `{}`)
	if status != http.StatusOK || body["ok"] != true || body["useWebSearch"] != true {
		t.Fatalf("missing useWebSearch should default true like TS, status=%d body=%#v", status, body)
	}
	status, body = post("/codex/use-image-generation", `{}`)
	if status != http.StatusOK || body["ok"] != true || body["useImageGeneration"] != true {
		t.Fatalf("missing useImageGeneration should default true like TS, status=%d body=%#v", status, body)
	}

	for _, tc := range []struct {
		path    string
		payload string
	}{
		{"/codex/use-websocket", `{"useWebSocket":"yes"}`},
		{"/codex/use-web-search", `{"useWebSearch":"yes"}`},
		{"/codex/use-image-generation", `{"useImageGeneration":"yes"}`},
		{"/codex/load-balancing-mode", `{"mode":"tiered_sticky"}`},
		{"/codex/tier-order", `{"tierOrder":["team","invalid"]}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			status, body := post(tc.path, tc.payload)
			if status != http.StatusBadRequest {
				t.Fatalf("expected TS-style 400, got %d body=%#v", status, body)
			}
		})
	}

	status, body = post("/codex/tier-order", `{"tierOrder":["team","pro","team"]}`)
	if status != http.StatusOK || body["ok"] != true {
		t.Fatalf("valid tierOrder should succeed, status=%d body=%#v", status, body)
	}
	tierOrder, _ := body["tierOrder"].([]any)
	if strings.Join(anyStrings(tierOrder), ",") != "team,pro" {
		t.Fatalf("valid tierOrder should be normalized after TS validation, got %#v", tierOrder)
	}
	// POST /tier-order must return the full effectiveTierOrder (normalized prefix
	// + remaining ALL_CODEX_TIER_ORDER tiers appended), matching TS
	// snapshot.effectiveTierOrder and Go's own GET /codex/status. Go previously
	// echoed `normalized` here, dropping the unspecified tiers.
	postEffective, _ := body["effectiveTierOrder"].([]any)
	if strings.Join(anyStrings(postEffective), ",") != "team,pro,prolite,plus,free,other" {
		t.Fatalf("POST tier-order effectiveTierOrder should append remaining tiers, got %#v", postEffective)
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

// TestAdminUpdateUserErrorPrecedenceMatchesTS pins the TS admin update-user error
// ordering: field-shape validation (safeParse) runs before any business rule, and
// among business rules self-demote/last-admin (role) precede username-taken. Go
// previously validated/checked username before role and fetched the target up
// front, so these precedences diverged.
func TestAdminUpdateUserErrorPrecedenceMatchesTS(t *testing.T) {
	// (a) self-demote takes precedence over an also-taken username (TS checks role
	// business rules before username uniqueness).
	router, token, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{{"admin-1", "root", "admin"}, {"admin-2", "ops", "admin"}}, "admin-1")
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/admin-1", strings.NewReader(`{"role":"user","username":"ops"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if w.Code != http.StatusBadRequest || body["code"] != "SELF_DEMOTE" {
		t.Fatalf("self-demote should take precedence over username-taken, got %d %#v", w.Code, body)
	}

	// (b) invalid field shape (bad role enum) is rejected before the 404 for a
	// missing target (TS safeParse precedes the update/404).
	router2, token2, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{{"admin-1", "root", "admin"}}, "admin-1")
	badReq := httptest.NewRequest(http.MethodPatch, "/admin/users/missing", strings.NewReader(`{"role":"superuser"}`))
	badReq.Header.Set("Authorization", "Bearer "+token2)
	badReq.Header.Set("Content-Type", "application/json")
	badW := httptest.NewRecorder()
	router2.ServeHTTP(badW, badReq)
	if badW.Code != http.StatusBadRequest {
		t.Fatalf("invalid role on a missing target should be 400 (validation before 404), got %d body=%s", badW.Code, badW.Body.String())
	}
}

// TestAdminUpdateUserEmptyBodyUsesValidationErrorCode pins the "no fields" rejection
// code against TS (server/routes/admin.ts): `throw new AppError("At least one field
// is required", 400, "VALIDATION_ERROR")`. Go previously used BadRequest, which
// emits code BAD_REQUEST instead of VALIDATION_ERROR.
func TestAdminUpdateUserEmptyBodyUsesValidationErrorCode(t *testing.T) {
	router, token, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{{"admin-1", "root", "admin"}}, "admin-1")
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/admin-1", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if w.Code != http.StatusBadRequest || body["code"] != "VALIDATION_ERROR" || body["error"] != "At least one field is required" {
		t.Fatalf("expected 400 VALIDATION_ERROR \"At least one field is required\", got %d %#v", w.Code, body)
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

func TestClineProviderEndpointsExposeFrontendContractShape(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	if _, err := store.Patch(map[string]any{
		"clineProviders": []any{map[string]any{
			"id":            "cline1",
			"name":          "Cline",
			"prefix":        "cline",
			"accessToken":   "provider-token",
			"enabledModels": []any{"anthropic/claude-sonnet"},
		}},
	}); err != nil {
		t.Fatalf("patch Cline provider settings: %v", err)
	}
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.WriteFile(filepath.Join(dataDir, "cline-models.json"), []byte(`{"cline1":[{"id":"openai/gpt-test","name":"GPT Test","contextLength":128000,"pricing":{"prompt":"0.1","completion":"0.2"}},{"id":"anthropic/claude-sonnet","name":"Claude Sonnet Coding","contextLength":200000}]}`), 0o600); err != nil {
		t.Fatalf("write cline model cache: %v", err)
	}
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	get := func(path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s expected 200, got %d body=%s", path, w.Code, w.Body.String())
		}
		var body map[string]any
		decodeJSONBody(t, w, &body)
		return body
	}

	status := get("/cline/status")
	if status["authenticated"] != false || status["pendingAuth"] != false || status["totalModels"] != float64(2) {
		t.Fatalf("status should report auth flags and cached model-pool count: %#v", status)
	}
	providers, ok := status["providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("status should include provider array: %#v", status)
	}
	provider, _ := providers[0].(map[string]any)
	if provider["id"] != "cline1" || provider["prefix"] != "cline" || provider["hasToken"] != true {
		t.Fatalf("unexpected provider status shape: %#v", provider)
	}

	count := get("/cline/pool/count")
	if count["count"] != float64(2) {
		t.Fatalf("pool count should use cached model pool: %#v", count)
	}
	search := get("/cline/pool/search?q=claude%20coding&limit=1")
	models, ok := search["models"].([]any)
	if !ok || len(models) != 1 || search["total"] != float64(1) {
		t.Fatalf("pool search should return limited models and total matches: %#v", search)
	}
	first, _ := models[0].(map[string]any)
	if first["id"] != "anthropic/claude-sonnet" || first["contextLength"] != float64(200000) {
		t.Fatalf("pool search should preserve frontend model fields: %#v", first)
	}

	missing := get("/cline/providers/missing/models")
	missingModels, ok := missing["models"].([]any)
	if !ok || len(missingModels) != 0 || missing["fromCache"] != true {
		t.Fatalf("missing provider cache should return empty models array/fromCache: %#v", missing)
	}

	recommended := get("/cline/recommended-models")
	if rec, ok := recommended["recommended"].([]any); !ok || len(rec) == 0 {
		t.Fatalf("recommended models should include recommended array: %#v", recommended)
	}
	if free, ok := recommended["free"].([]any); !ok || len(free) == 0 {
		t.Fatalf("recommended models should include free array: %#v", recommended)
	}
}

func TestClineRecommendedModelsPrefersCachedFrontendShape(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir cline cache dir: %v", err)
	}
	cache := map[string]any{
		"recommended": []any{map[string]any{
			"id":          "cached/recommended-model",
			"name":        "Cached Recommended Model",
			"description": "cached recommended description",
			"tags":        []any{"CACHED"},
		}},
		"free": []any{map[string]any{
			"id":   "cached/free-model",
			"name": "Cached Free Model",
			"tags": []any{"FREE"},
		}},
	}
	cacheBytes, err := json.Marshal(cache)
	if err != nil {
		t.Fatalf("marshal cline recommended cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "cline-recommended-models.json"), cacheBytes, 0o600); err != nil {
		t.Fatalf("write cline recommended cache: %v", err)
	}

	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	req := httptest.NewRequest(http.MethodGet, "/cline/recommended-models", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected recommended models 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	recommended, _ := body["recommended"].([]any)
	free, _ := body["free"].([]any)
	if len(recommended) != 1 || len(free) != 1 {
		t.Fatalf("recommended models should expose TS frontend arrays from cache, got %#v", body)
	}
	recommendedModel, _ := recommended[0].(map[string]any)
	freeModel, _ := free[0].(map[string]any)
	if recommendedModel["id"] != "cached/recommended-model" || recommendedModel["description"] != "cached recommended description" || freeModel["id"] != "cached/free-model" {
		t.Fatalf("recommended models cache shape drifted: %#v", body)
	}
	if tags, ok := recommendedModel["tags"].([]any); !ok || len(tags) != 1 || tags[0] != "CACHED" {
		t.Fatalf("recommended model tags should remain a frontend array, got %#v", recommendedModel)
	}
}

func TestClineReadEndpointsDoNotMutateDefaultProvider(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	providers, _ := store.Raw()["clineProviders"].([]any)
	if len(providers) != 1 {
		t.Fatalf("provider route registration should create the TS-style default Cline provider, got %#v", providers)
	}
	initialProvider, _ := providers[0].(map[string]any)
	initialProviderID := stringMap(initialProvider, "id", "")
	readPaths := []string{
		"/cline/status",
		"/cline/models",
		"/cline/pool/search?q=claude&limit=5",
		"/cline/pool/count",
	}
	for _, path := range readPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected read-only Cline endpoint 200, got %d body=%s", w.Code, w.Body.String())
			}
			providers, _ := store.Raw()["clineProviders"].([]any)
			provider, _ := providers[0].(map[string]any)
			if len(providers) != 1 || stringMap(provider, "id", "") != initialProviderID {
				t.Fatalf("%s should not create or replace Cline providers, got %#v", path, providers)
			}
		})
	}
}

func TestProviderMissingResponseIncludesProviderID(t *testing.T) {
	r, token := newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "admin")
	req := httptest.NewRequest(http.MethodGet, "/nkp/providers/missing/quota", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing provider should return 404, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["error"] != `Provider "missing" not found` {
		t.Fatalf("missing provider response should include provider id, got %#v", body)
	}
}

func TestClineUnauthenticatedUserInfoAndBalanceMatchTSStatus(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	refreshReq := httptest.NewRequest(http.MethodPost, "/cline/user-info/refresh", nil)
	refreshReq.Header.Set("Authorization", "Bearer "+token)
	refreshW := httptest.NewRecorder()
	r.ServeHTTP(refreshW, refreshReq)
	if refreshW.Code != http.StatusOK {
		t.Fatalf("unauthenticated user-info refresh should return 200, got %d body=%s", refreshW.Code, refreshW.Body.String())
	}
	var refreshBody map[string]any
	decodeJSONBody(t, refreshW, &refreshBody)
	if refreshBody["authenticated"] != false {
		t.Fatalf("expected unauthenticated status body, got %#v", refreshBody)
	}

	balanceReq := httptest.NewRequest(http.MethodGet, "/cline/balance", nil)
	balanceReq.Header.Set("Authorization", "Bearer "+token)
	balanceW := httptest.NewRecorder()
	r.ServeHTTP(balanceW, balanceReq)
	if balanceW.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unauthenticated balance should return TS-safe 422, got %d body=%s", balanceW.Code, balanceW.Body.String())
	}
	var balanceBody map[string]any
	decodeJSONBody(t, balanceW, &balanceBody)
	if balanceBody["error"] != "Not authenticated or balance unavailable" {
		t.Fatalf("expected TS balance auth error, got %#v", balanceBody)
	}
}

func TestClineRecommendedModelsRefreshFetchesAndPersistsCache(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cline/api/v1/ai/cline/recommended-models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"recommended":[{"id":"cline/recommended-fixture","name":"Cline Recommended Fixture","description":"Fixture recommended model","tags":["BEST"]}],"free":[{"id":"cline/free-fixture","name":"Cline Free Fixture","tags":["FREE"]},{"id":"cline/free-live-fixture","name":"Cline Free Live Fixture","description":"Fixture live free model","tags":["FREE","LIVE"]}]}`))
	}))
	defer upstream.Close()
	t.Setenv("NARRAFORK_CLINE_RECOMMENDED_MODELS_URL", upstream.URL+"/cline/api/v1/ai/cline/recommended-models")
	store := newSystemRoutesTestStore(t)
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	refreshReq := httptest.NewRequest(http.MethodGet, "/cline/recommended-models?refresh=1", nil)
	refreshReq.Header.Set("Authorization", "Bearer "+token)
	refreshW := httptest.NewRecorder()
	r.ServeHTTP(refreshW, refreshReq)
	if refreshW.Code != http.StatusOK {
		t.Fatalf("expected recommended refresh 200, got %d body=%s", refreshW.Code, refreshW.Body.String())
	}
	var refreshBody map[string]any
	decodeJSONBody(t, refreshW, &refreshBody)
	assertClineRecommendedFixtureBody(t, refreshBody)

	cachedReq := httptest.NewRequest(http.MethodGet, "/cline/recommended-models", nil)
	cachedReq.Header.Set("Authorization", "Bearer "+token)
	cachedW := httptest.NewRecorder()
	r.ServeHTTP(cachedW, cachedReq)
	if cachedW.Code != http.StatusOK {
		t.Fatalf("expected recommended cache 200, got %d body=%s", cachedW.Code, cachedW.Body.String())
	}
	var cachedBody map[string]any
	decodeJSONBody(t, cachedW, &cachedBody)
	assertClineRecommendedFixtureBody(t, cachedBody)
}

func assertClineRecommendedFixtureBody(t *testing.T, body map[string]any) {
	t.Helper()
	recommended, _ := body["recommended"].([]any)
	free, _ := body["free"].([]any)
	if len(recommended) != 1 || len(free) != 2 {
		t.Fatalf("expected fixture recommended/free arrays, got %#v", body)
	}
	recommendedModel, _ := recommended[0].(map[string]any)
	freeOne, _ := free[0].(map[string]any)
	freeTwo, _ := free[1].(map[string]any)
	if recommendedModel["id"] != "cline/recommended-fixture" || recommendedModel["description"] != "Fixture recommended model" {
		t.Fatalf("recommended fixture model drifted: %#v", recommendedModel)
	}
	if freeOne["id"] != "cline/free-fixture" || freeTwo["id"] != "cline/free-live-fixture" || freeTwo["description"] != "Fixture live free model" {
		t.Fatalf("free fixture models drifted: %#v", body)
	}
	if tags, ok := recommendedModel["tags"].([]any); !ok || len(tags) != 1 || tags[0] != "BEST" {
		t.Fatalf("recommended fixture tags drifted: %#v", recommendedModel)
	}
}

func TestClineProviderRefreshResponseMatchesTSCountShape(t *testing.T) {
	body := providerModelRefreshResponse("cline", []map[string]any{{"id": "a"}, {"id": "b"}}, map[string]any{"ignored": true})
	if body["count"] != 2 || body["fromCache"] != false {
		t.Fatalf("Cline provider refresh should return TS count/fromCache shape, got %#v", body)
	}
	if _, ok := body["models"]; ok {
		t.Fatalf("Cline provider refresh should not expose models in TS-compatible provider-specific response, got %#v", body)
	}
}

func TestClineProviderRefreshFetchesOpenRouterShapeAndCachesModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openrouter/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-CLIENT-TYPE") != "extension" || r.Header.Get("HTTP-Referer") != "https://cline.bot" {
			http.Error(w, "missing Cline OpenRouter headers", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"cline/refresh-b","name":"Cline Refresh B","context_length":200000,"pricing":{"prompt":"0.000002","completion":"0.000004"}},{"id":"cline/refresh-a","name":"Cline Refresh A","context_length":100000,"pricing":{"prompt":"0.000001","completion":"0.000003"}},{"id":"cline/refresh-a","name":"Duplicate"}]}`))
	}))
	defer upstream.Close()
	t.Setenv("NARRAFORK_CLINE_OPENROUTER_MODELS_URL", upstream.URL+"/openrouter/api/v1/models")
	store := newSystemRoutesTestStore(t)
	provider := map[string]any{"id": "cline1", "name": "Cline", "prefix": "clinecache", "enabledModels": []any{}}
	if _, err := store.Patch(map[string]any{"clineProviders": []any{provider}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	refreshReq := httptest.NewRequest(http.MethodPost, "/cline/providers/cline1/models/refresh", nil)
	refreshReq.Header.Set("Authorization", "Bearer "+token)
	refreshW := httptest.NewRecorder()
	r.ServeHTTP(refreshW, refreshReq)
	if refreshW.Code != http.StatusOK {
		t.Fatalf("expected refresh 200, got %d body=%s", refreshW.Code, refreshW.Body.String())
	}
	var refreshBody map[string]any
	decodeJSONBody(t, refreshW, &refreshBody)
	if refreshBody["count"] != float64(3) || refreshBody["fromCache"] != false {
		t.Fatalf("unexpected refresh body: %#v", refreshBody)
	}
	if _, ok := refreshBody["models"]; ok {
		t.Fatalf("provider-specific Cline refresh should not return models: %#v", refreshBody)
	}

	modelsReq := httptest.NewRequest(http.MethodGet, "/cline/providers/cline1/models", nil)
	modelsReq.Header.Set("Authorization", "Bearer "+token)
	modelsW := httptest.NewRecorder()
	r.ServeHTTP(modelsW, modelsReq)
	if modelsW.Code != http.StatusOK {
		t.Fatalf("expected cached models 200, got %d body=%s", modelsW.Code, modelsW.Body.String())
	}
	var modelsBody map[string]any
	decodeJSONBody(t, modelsW, &modelsBody)
	if modelsBody["fromCache"] != true {
		t.Fatalf("expected cached response, got %#v", modelsBody)
	}
	models, _ := modelsBody["models"].([]any)
	if len(models) != 3 {
		t.Fatalf("expected provider-specific cached models to preserve TS duplicates, got %#v", modelsBody)
	}
	first, _ := models[0].(map[string]any)
	second, _ := models[1].(map[string]any)
	third, _ := models[2].(map[string]any)
	if first["id"] != "cline/refresh-a" || second["id"] != "cline/refresh-a" || third["id"] != "cline/refresh-b" {
		t.Fatalf("expected sorted Cline refresh models with provider-specific duplicates preserved, got %#v", models)
	}
	if first["contextLength"] != float64(100000) || first["promptPrice"] != "0.000001" || first["completionPrice"] != "0.000003" {
		t.Fatalf("expected normalized Cline model metadata, got %#v", first)
	}

	allReq := httptest.NewRequest(http.MethodGet, "/cline/models", nil)
	allReq.Header.Set("Authorization", "Bearer "+token)
	allW := httptest.NewRecorder()
	r.ServeHTTP(allW, allReq)
	if allW.Code != http.StatusOK {
		t.Fatalf("expected aggregate Cline models 200, got %d body=%s", allW.Code, allW.Body.String())
	}
	var allBody map[string]any
	decodeJSONBody(t, allW, &allBody)
	allModels, _ := allBody["models"].([]any)
	if len(allModels) != 2 {
		t.Fatalf("expected aggregate Cline models to dedupe by id, got %#v", allBody)
	}
}

func TestProviderModelRefreshReturnsFrontendContractShape(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/openai/v1/models", "/openai/api/v1/models", "/openai/models":
			if r.Header.Get("Authorization") != "Bearer openai-key" {
				t.Fatalf("unexpected OpenAI auth header: %s", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-b"},{"id":"gpt-a"}]}`))
		case "/anthropic/v1/models", "/anthropic/models":
			if r.Header.Get("x-api-key") != "anthropic-key" || r.Header.Get("anthropic-version") == "" {
				t.Fatalf("unexpected Anthropic headers api=%s version=%s", r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"))
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-b","display_name":"Claude B"},{"id":"claude-a"}]}`))
		case "/nkp/v1/kiro/models":
			if r.Header.Get("Authorization") != "Bearer nkp-key" {
				t.Fatalf("unexpected NKP auth header: %s", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"nkp-b","model_name":"Claude NKP B"},{"model_id":"nkp-a","model_short_name":"NKP A"}]}`))
		case "/nkp/v1/quota":
			if r.Header.Get("Authorization") != "Bearer nkp-key" {
				t.Fatalf("unexpected NKP quota auth header: %s", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"quotaBalance":12.34,"quotaTotalGranted":56.78}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := newSystemRoutesTestStore(t)
	if _, err := store.Patch(map[string]any{
		"openaiProviders":    []any{map[string]any{"id": "oa", "name": "OpenAI", "prefix": "openai", "baseUrl": upstream.URL + "/openai/v1", "apiKey": "openai-key"}},
		"anthropicProviders": []any{map[string]any{"id": "anth", "name": "Anthropic", "prefix": "anthropic", "baseUrl": upstream.URL + "/anthropic", "apiKey": "anthropic-key"}},
		"nkpProviders":       []any{map[string]any{"id": "nkp", "name": "NKP", "prefix": "nkp", "baseUrl": upstream.URL + "/nkp", "apiKey": "nkp-key"}},
	}); err != nil {
		t.Fatalf("patch provider settings: %v", err)
	}
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	cases := []struct {
		path        string
		idKey       string
		wantFirstID string
		wantExtra   string
	}{
		{path: "/openai/providers/oa/models/refresh", idKey: "id", wantFirstID: "gpt-a"},
		{path: "/anthropic/providers/anth/models/refresh", idKey: "id", wantFirstID: "claude-a", wantExtra: "resolvedBaseUrl"},
		{path: "/nkp/providers/nkp/models/refresh", idKey: "model_id", wantFirstID: "nkp-a"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			decodeJSONBody(t, w, &body)
			models, ok := body["models"].([]any)
			if !ok || len(models) != 2 || body["fromCache"] != false {
				t.Fatalf("refresh response should expose frontend models/fromCache shape: %#v", body)
			}
			first, _ := models[0].(map[string]any)
			if first[tc.idKey] != tc.wantFirstID {
				t.Fatalf("unexpected first model for %s: %#v", tc.path, models)
			}
			if tc.wantExtra != "" && body[tc.wantExtra] == nil {
				t.Fatalf("expected extra field %s in response: %#v", tc.wantExtra, body)
			}
		})
	}

	quotaReq := httptest.NewRequest(http.MethodGet, "/nkp/providers/nkp/quota", nil)
	quotaReq.Header.Set("Authorization", "Bearer "+token)
	quotaW := httptest.NewRecorder()
	r.ServeHTTP(quotaW, quotaReq)
	if quotaW.Code != http.StatusOK {
		t.Fatalf("expected NKP quota 200, got %d body=%s", quotaW.Code, quotaW.Body.String())
	}
	var quotaBody map[string]any
	decodeJSONBody(t, quotaW, &quotaBody)
	if quotaBody["quotaBalance"] != float64(12.34) || quotaBody["quotaTotalGranted"] != float64(56.78) {
		t.Fatalf("unexpected NKP quota response: %#v", quotaBody)
	}

	quotasReq := httptest.NewRequest(http.MethodGet, "/nkp/quotas", nil)
	quotasReq.Header.Set("Authorization", "Bearer "+token)
	quotasW := httptest.NewRecorder()
	r.ServeHTTP(quotasW, quotasReq)
	if quotasW.Code != http.StatusOK {
		t.Fatalf("expected NKP quotas 200, got %d body=%s", quotasW.Code, quotasW.Body.String())
	}
	var quotasBody map[string]map[string]any
	decodeJSONBody(t, quotasW, &quotasBody)
	cachedQuota := quotasBody["nkp"]
	if cachedQuota["quotaBalance"] != float64(12.34) || cachedQuota["quotaTotalGranted"] != float64(56.78) || len(cachedQuota) != 2 {
		t.Fatalf("unexpected NKP cached quota: %#v", quotasBody)
	}

	req := httptest.NewRequest(http.MethodPost, "/openai/models/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected all-provider refresh 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if _, ok := body["results"].([]any); !ok || body["fromCache"] != false {
		t.Fatalf("all-provider refresh should expose results/models/fromCache shape: %#v", body)
	}
	if models, ok := body["models"].([]any); !ok || len(models) == 0 {
		t.Fatalf("all-provider refresh should include aggregate models: %#v", body)
	}
}

func TestNugQuotaEndpointCachesProviderQuota(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/quota" || r.Header.Get("Authorization") != "Bearer nug-key" {
			t.Fatalf("unexpected upstream request path=%s auth=%s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"balance":"12.5","totalGranted":"50","extra":{"plan":"trial","reset":"soon"}}`))
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
	if quota["balance"] != float64(12.5) || quota["totalGranted"] != float64(50) || !strings.Contains(stringMap(quota, "detailedQuotaBalance", ""), "plan: trial") {
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
	if cached["balance"] != float64(12.5) || cached["totalGranted"] != float64(50) || !strings.Contains(stringMap(cached, "detailedQuotaBalance", ""), "reset: soon") || cached["extra"] == nil {
		t.Fatalf("unexpected cached quota: %#v", quotas)
	}
	if _, err := os.Stat(filepath.Join(home, ".narrafork", "nug-quotas.json")); err != nil {
		t.Fatalf("expected nug quota cache file: %v", err)
	}
}

func TestProviderGlobalRefreshReportsMissingKeyResults(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/openai/v1/models":
			if r.Header.Get("Authorization") != "Bearer openai-key" {
				t.Fatalf("unexpected OpenAI auth header: %s", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-b"},{"id":"gpt-a"},{"id":"gpt-a"}]}`))
		case "/anthropic/v1/models":
			if r.Header.Get("x-api-key") != "anthropic-key" || r.Header.Get("anthropic-version") == "" {
				t.Fatalf("unexpected Anthropic headers: key=%s version=%s", r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"))
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-b"},{"id":"claude-a"},{"id":"claude-a"}]}`))
		case "/nkp/v1/kiro/models":
			if r.Header.Get("Authorization") != "Bearer nkp-key" {
				t.Fatalf("unexpected NKP auth header: %s", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"nkp-b","model_name":"Claude B"},{"model_id":"nkp-a","model_name":"Claude A"},{"id":"nkp-a","model_name":"Claude A"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := newSystemRoutesTestStore(t)
	if _, err := store.Patch(map[string]any{
		"openaiProviders": []any{
			map[string]any{"id": "openai-ok", "name": "OpenAI OK", "baseUrl": upstream.URL + "/openai/v1", "apiKey": "openai-key"},
			map[string]any{"id": "openai-no-key", "name": "OpenAI No Key", "baseUrl": upstream.URL + "/openai/v1", "apiKey": ""},
		},
		"anthropicProviders": []any{
			map[string]any{"id": "anthropic-ok", "name": "Anthropic OK", "baseUrl": upstream.URL + "/anthropic", "apiKey": "anthropic-key"},
			map[string]any{"id": "anthropic-no-key", "name": "Anthropic No Key", "baseUrl": upstream.URL + "/anthropic", "apiKey": ""},
		},
		"nkpProviders": []any{
			map[string]any{"id": "nkp-ok", "name": "NKP OK", "baseUrl": upstream.URL + "/nkp", "apiKey": "nkp-key"},
			map[string]any{"id": "nkp-no-key", "name": "NKP No Key", "baseUrl": upstream.URL + "/nkp", "apiKey": ""},
		},
	}); err != nil {
		t.Fatalf("seed provider settings: %v", err)
	}
	router, userToken := newProviderRoutesTestRouterWithStore(t, store, "user")

	cases := []struct {
		path          string
		okProviderID  string
		badProviderID string
		expectedError string
		modelKey      string
		expectedIDs   string
	}{
		{path: "/openai/models/refresh", okProviderID: "openai-ok", badProviderID: "openai-no-key", expectedError: `OpenAI API key not configured for provider "OpenAI No Key"`, modelKey: "id", expectedIDs: "gpt-a,gpt-b"},
		{path: "/anthropic/models/refresh", okProviderID: "anthropic-ok", badProviderID: "anthropic-no-key", expectedError: `Anthropic API key not configured for provider "Anthropic No Key"`, modelKey: "id", expectedIDs: "claude-a,claude-b"},
		{path: "/nkp/models/refresh", okProviderID: "nkp-ok", badProviderID: "nkp-no-key", expectedError: `NKP API key not configured for provider "NKP No Key"`, modelKey: "model_id", expectedIDs: "nkp-a,nkp-b"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+userToken)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
			}
			var payload map[string]any
			decodeJSONBody(t, w, &payload)
			if payload["fromCache"] != false {
				t.Fatalf("expected fromCache false: %#v", payload)
			}
			results, _ := payload["results"].([]any)
			if len(results) != 2 {
				t.Fatalf("expected two provider results: %#v", payload)
			}
			var okResult, badResult map[string]any
			for _, item := range results {
				result, _ := item.(map[string]any)
				switch result["providerId"] {
				case tc.okProviderID:
					okResult = result
				case tc.badProviderID:
					badResult = result
				}
			}
			if okResult == nil || okResult["count"] != float64(2) {
				t.Fatalf("unexpected success provider result: %#v", results)
			}
			if badResult == nil || badResult["count"] != float64(0) || badResult["error"] != tc.expectedError {
				t.Fatalf("unexpected no-key provider result: %#v", results)
			}
			models, _ := payload["models"].([]any)
			ids := make([]string, 0, len(models))
			for _, item := range models {
				model, _ := item.(map[string]any)
				ids = append(ids, stringMap(model, tc.modelKey, ""))
			}
			if strings.Join(ids, ",") != tc.expectedIDs {
				t.Fatalf("unexpected aggregate models: %#v", models)
			}
		})
	}

	providerSpecificNoKey := []struct {
		path          string
		expectedError string
	}{
		{path: "/openai/providers/openai-no-key/models/refresh", expectedError: `OpenAI API key not configured for provider "OpenAI No Key"`},
		{path: "/anthropic/providers/anthropic-no-key/models/refresh", expectedError: `Anthropic API key not configured for provider "Anthropic No Key"`},
		{path: "/nkp/providers/nkp-no-key/models/refresh", expectedError: `NKP API key not configured for provider "NKP No Key"`},
	}
	for _, tc := range providerSpecificNoKey {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+userToken)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
			}
			var payload map[string]any
			decodeJSONBody(t, w, &payload)
			if payload["error"] != tc.expectedError {
				t.Fatalf("unexpected provider-specific no-key error: %#v", payload)
			}
		})
	}
}

func TestNugProviderEndpointsExposeFrontendContractShape(t *testing.T) {
	upstreamHits := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer nug-key" {
			t.Fatalf("unexpected NUG auth header for %s: %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		upstreamHits[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"models":[{"id":"z-model","owned_by":"nug"},{"id":"a-model","owned_by":"nug"},{"id":"z-model","owned_by":"dupe"}]}`))
		case "/v1/quota":
			_, _ = w.Write([]byte(`{"quotaBalance":"12.5","quotaTotalGranted":"50","username":"nug-user","role":"admin"}`))
		case "/v1/channels/health":
			_, _ = w.Write([]byte(`{"channels":[{"channelType":"kiro","totalCredentials":2,"availableCredentials":1,"disabledCredentials":1,"availabilityRate":0.5,"currentConcurrency":1,"maxConcurrency":4,"queueDepth":0}]}`))
		case "/v1/usage":
			if r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("offset") != "0" || r.URL.Query().Get("period") != "month" {
				t.Fatalf("unexpected usage query: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":[{"channel":"kiro","model_id":"claude","tokensIn":"11","completionTokens":5,"cacheCreationTokens":"2","cachedInputTokens":"3","quota_cost":"0.4","meter_usage":"0.5","status":"ok","duration_ms":"99","timestamp":"2026-05-26T00:00:00Z","raw_dump_json":"SECRET","api_key":"SECRET","extra":{"plan":"trial","token":"secret","safe_count":7}}],"count":"1"}`))
		case "/v1/usage/summary":
			if r.URL.Query().Get("period") != "month" {
				t.Fatalf("unexpected summary period: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"request_count":"3","total_meter_usage":"4.5","total_quota_cost":"6.7","total_input_tokens":"8","total_output_tokens":"9","total_cache_write_tokens":"10","total_cache_read_tokens":"11"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	store := newSystemRoutesTestStore(t)
	if _, err := store.Patch(map[string]any{
		"nugProviders": []any{
			map[string]any{"id": "nug-1", "name": "NUG One", "prefix": "nug", "baseUrl": upstream.URL + "/api/v1", "apiKey": "nug-key"},
			map[string]any{"id": "nug-no-key", "name": "NUG No Key", "prefix": "nugmissing", "baseUrl": upstream.URL + "/api/v1", "apiKey": ""},
		},
	}); err != nil {
		t.Fatalf("patch NUG settings: %v", err)
	}
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.WriteFile(filepath.Join(dataDir, "nug-models-providers.json"), []byte(`{"nug-1":[{"id":"cached-model","owned_by":"cache"}]}`), 0o600); err != nil {
		t.Fatalf("write nug model cache: %v", err)
	}
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	doJSONWithStatus := func(method, path string, body io.Reader, expectedStatus int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != expectedStatus {
			t.Fatalf("%s %s expected %d, got %d body=%s", method, path, expectedStatus, w.Code, w.Body.String())
		}
		var payload map[string]any
		decodeJSONBody(t, w, &payload)
		return payload
	}
	doJSON := func(method, path string, body io.Reader) map[string]any {
		t.Helper()
		return doJSONWithStatus(method, path, body, http.StatusOK)
	}

	cached := doJSON(http.MethodGet, "/nug/providers/nug-1/models", nil)
	models, ok := cached["models"].([]any)
	if !ok || len(models) != 1 || cached["fromCache"] != true || upstreamHits["/v1/models"] != 0 {
		t.Fatalf("cached provider models should be array/fromCache without upstream hit: body=%#v hits=%#v", cached, upstreamHits)
	}
	noKeyRefresh := doJSONWithStatus(http.MethodPost, "/nug/providers/nug-no-key/models/refresh", nil, http.StatusInternalServerError)
	if noKeyRefresh["error"] != `NUG API key not configured for provider "NUG No Key"` || upstreamHits["/v1/models"] != 0 {
		t.Fatalf("provider no-key refresh should fail before upstream with TS-compatible error: body=%#v hits=%#v", noKeyRefresh, upstreamHits)
	}

	aggregate := doJSON(http.MethodPost, "/nug/models/refresh", nil)
	results, ok := aggregate["results"].([]any)
	if !ok || len(results) != 2 || aggregate["fromCache"] != false {
		t.Fatalf("aggregate refresh should expose results/models/fromCache: %#v", aggregate)
	}
	var noKeyResult map[string]any
	for _, item := range results {
		result, _ := item.(map[string]any)
		if result["providerId"] == "nug-no-key" {
			noKeyResult = result
		}
	}
	if noKeyResult == nil || noKeyResult["count"] != float64(0) || noKeyResult["error"] != `NUG API key not configured for provider "NUG No Key"` {
		t.Fatalf("aggregate refresh should expose TS-compatible missing-key result: %#v", results)
	}
	models, ok = aggregate["models"].([]any)
	if !ok || len(models) != 2 {
		t.Fatalf("aggregate refresh should expose unique refreshed models: %#v", aggregate)
	}
	first, _ := models[0].(map[string]any)
	if first["id"] != "a-model" {
		t.Fatalf("NUG refreshed models should be sorted by id: %#v", models)
	}

	quota := doJSON(http.MethodGet, "/nug/providers/nug-1/quota", nil)
	if quota["balance"] != float64(12.5) || quota["totalGranted"] != float64(50) || quota["username"] != "nug-user" || quota["detailedQuotaBalance"] != nil {
		t.Fatalf("quota should normalize numeric aliases and preserve TS null detail field: %#v", quota)
	}
	channels := doJSON(http.MethodGet, "/nug/providers/nug-1/channels/health", nil)
	if list, ok := channels["channels"].([]any); !ok || len(list) != 1 {
		t.Fatalf("channels health should expose channels array: %#v", channels)
	}

	usage := doJSON(http.MethodGet, "/nug/providers/nug-1/usage?range=30days", nil)
	events, ok := usage["events"].([]any)
	if !ok || len(events) != 1 || usage["total"] != float64(1) {
		t.Fatalf("usage should normalize data/count fallback into events/total: %#v", usage)
	}
	event, _ := events[0].(map[string]any)
	if event["id"] != "usage-0" || event["channelType"] != "kiro" || event["model"] != "claude" || event["inputTokens"] != float64(11) || event["cacheReadInputTokens"] != float64(3) {
		t.Fatalf("usage event should expose frontend camelCase fields: %#v", event)
	}
	if event["raw_dump_json"] != nil || event["api_key"] != nil {
		t.Fatalf("usage event should not expose raw or sensitive upstream fields: %#v", event)
	}
	extra, _ := event["extra"].(map[string]any)
	if extra["plan"] != "trial" || extra["safe_count"] != float64(7) || extra["token"] != nil {
		t.Fatalf("usage event should expose only sanitized extra details: %#v", event)
	}

	summary := doJSON(http.MethodGet, "/nug/providers/nug-1/usage/summary?range=30days", nil)
	if summary["requestCount"] != float64(3) || summary["totalQuotaCost"] != float64(6.7) || summary["totalCacheReadTokens"] != float64(11) {
		t.Fatalf("summary should expose numeric camelCase totals: %#v", summary)
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

func TestUsageHistoryStatsFailOpenOnMissingUsageColumns(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE api_requests (id TEXT PRIMARY KEY, provider TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create api_requests: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO api_requests (id, provider, created_at) VALUES ('req-min', 'openai', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert minimal api request: %v", err)
	}
	h := &Handler{DB: sqlDB}
	req := httptest.NewRequest(http.MethodGet, "/api/usage-history/stats", nil)
	w := httptest.NewRecorder()

	if err := h.usageHistoryStats(w, req); err != nil {
		t.Fatalf("usage stats should fail open on missing usage columns: %v", err)
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["totalRequests"] != float64(1) || body["totalInputTokens"] != float64(0) || body["totalTokens"] != float64(0) || body["totalCost"] != float64(0) {
		t.Fatalf("unexpected minimal usage stats payload: %#v", body)
	}

	w = httptest.NewRecorder()
	if err := h.usageHistoryTimeseries(w, req); err != nil {
		t.Fatalf("usage timeseries should fail open on missing usage columns: %v", err)
	}
	body = map[string]any{}
	decodeJSONBody(t, w, &body)
	points, _ := body["points"].([]any)
	if len(points) == 0 {
		t.Fatalf("timeseries should return zero-filled points, got %#v", body)
	}

	w = httptest.NewRecorder()
	if err := h.usageHistoryProviders(w, req); err != nil {
		t.Fatalf("providers should use available provider column: %v", err)
	}
	body = map[string]any{}
	decodeJSONBody(t, w, &body)
	providers, _ := body["providers"].([]any)
	if len(providers) != 1 || providers[0] != "openai" {
		t.Fatalf("unexpected providers payload: %#v", body)
	}
}

func TestUsageHistoryListAndDetailExposeFrontendFields(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	stmts := []string{
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, title TEXT, chapter_id TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, title TEXT, project_id TEXT)`,
		`CREATE TABLE api_requests (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			message_id TEXT,
			kind TEXT,
			provider TEXT,
			credential_id TEXT,
			model TEXT,
			input_tokens INTEGER,
			output_tokens INTEGER,
			cached_input_tokens INTEGER,
			cache_creation_input_tokens INTEGER,
			cache_creation_5m_tokens INTEGER,
			cache_creation_1h_tokens INTEGER,
			reasoning_tokens INTEGER,
			ttft_ms INTEGER,
			duration_ms INTEGER,
			cost_usd REAL,
			context_percent REAL,
			meter_usage REAL,
			meter_unit TEXT,
			error_message TEXT,
			raw_dump_json TEXT,
			created_at TEXT
		)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create usage history schema: %v", err)
		}
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, title, project_id) VALUES ('chap-1', 'Chapter One', 'proj-1')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, title, chapter_id) VALUES ('nar-1', 'Narrator One', 'chap-1')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	rawDump := `{"request":{"url":"https://example.test"},"response":{"status":500,"error":"boom"}}`
	createdAt := time.Now().UTC().Format(time.RFC3339)
	if _, err := sqlDB.Exec(`INSERT INTO api_requests (id, narrator_id, message_id, kind, provider, credential_id, model, input_tokens, output_tokens, cached_input_tokens, cache_creation_input_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens, reasoning_tokens, ttft_ms, duration_ms, cost_usd, context_percent, meter_usage, meter_unit, error_message, raw_dump_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "req-1", "nar-1", "msg-1", "narrator", "openai", "cred-1", "gpt-test", 11, 7, 3, 5, 50, 100, 2, 123, 456, 0.0123, 42.5, 9.5, "credits", "failed", rawDump, createdAt); err != nil {
		t.Fatalf("insert api request: %v", err)
	}
	h := &Handler{DB: sqlDB}
	listReq := httptest.NewRequest(http.MethodGet, "/api/usage-history?page=1&pageSize=10", nil)
	listW := httptest.NewRecorder()
	if err := h.usageHistoryList(listW, listReq); err != nil {
		t.Fatalf("usage history list: %v", err)
	}
	var listBody map[string]any
	decodeJSONBody(t, listW, &listBody)
	records, _ := listBody["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("expected one record, got %#v", listBody)
	}
	record, _ := records[0].(map[string]any)
	if record["credentialId"] != "cred-1" || record["credentialName"] != "cred-1" || record["narratorTitle"] != "Narrator One" || record["chapterTitle"] != "Chapter One" || record["chapterId"] != "chap-1" || record["projectId"] != "proj-1" {
		t.Fatalf("list record should expose frontend identity fields: %#v", record)
	}
	if record["cacheCreation5mTokens"] != float64(50) || record["cacheCreation1hTokens"] != float64(100) || record["hasRawDump"] != true {
		t.Fatalf("list record should expose cache/raw fields: %#v", record)
	}
	if _, ok := record["rawDump"].(map[string]any); ok {
		t.Fatalf("list record should not include full raw dump payload: %#v", record)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/usage-history/req-1", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "req-1")
	detailReq = detailReq.WithContext(context.WithValue(detailReq.Context(), chi.RouteCtxKey, rctx))
	detailW := httptest.NewRecorder()
	if err := h.usageHistoryGet(detailW, detailReq); err != nil {
		t.Fatalf("usage history detail: %v", err)
	}
	var detail map[string]any
	decodeJSONBody(t, detailW, &detail)
	raw, _ := detail["rawDump"].(map[string]any)
	request, _ := raw["request"].(map[string]any)
	if request["url"] != "https://example.test" || detail["cacheCreation5mTokens"] != float64(50) || detail["cacheCreation1hTokens"] != float64(100) {
		t.Fatalf("detail should parse raw dump and expose cache tiers: %#v", detail)
	}
}

// TestUsageHistoryListValidatesPageAndPageSizeLikeTS pins the page/pageSize query
// validation to the TS listQuerySchema (z.coerce.number().int().positive() and
// .positive().max(100), default 1/50): invalid or out-of-range values reject with
// 400 instead of silently clamping (the previous intQuery behavior). The
// validation runs before any DB access, so the api_requests table is enough.
func TestUsageHistoryListValidatesPageAndPageSizeLikeTS(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE api_requests (id TEXT PRIMARY KEY, provider TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create api_requests: %v", err)
	}
	h := &Handler{DB: sqlDB}

	for _, bad := range []string{
		"page=0", "page=-1", "page=abc", "page=5.5", "page=50x", "page=",
		"pageSize=0", "pageSize=-1", "pageSize=abc", "pageSize=5.5", "pageSize=50x", "pageSize=101", "pageSize=1000", "pageSize=",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/usage-history?"+bad, nil)
		w := httptest.NewRecorder()
		err := h.usageHistoryList(w, req)
		// usageHistoryList returns an *httpx.AppError (400) for invalid params,
		// which the router converts to a 400 response; a returned error is the
		// validation-failure signal here.
		if err == nil {
			t.Fatalf("usage-history?%s should reject with a 400 error, got nil (status %d body=%s)", bad, w.Code, w.Body.String())
		}
		if !strings.Contains(err.Error(), "must be a positive integer") && !strings.Contains(err.Error(), "must be at most") {
			t.Fatalf("usage-history?%s should reject with a bounds error, got %v", bad, err)
		}
	}

	// Absent params (defaults 1/50) and in-range values succeed. z.coerce.number()
	// uses JS Number() coercion, so scientific-notation and hex/octal integer
	// literals that strconv.ParseFloat rejected must now be accepted like TS
	// (page=0x2 -> 2, pageSize=1e1 -> 10).
	for _, ok := range []string{"", "page=1", "page=2&pageSize=100", "pageSize=1", "page=0x2", "pageSize=1e1", "page=%2010%20"} {
		path := "/api/usage-history"
		if ok != "" {
			path += "?" + ok
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		if err := h.usageHistoryList(w, req); err != nil {
			t.Fatalf("usage-history %q should succeed, got error %v", path, err)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("usage-history %q should be 200, got %d body=%s", path, w.Code, w.Body.String())
		}
	}
}

// TestUsageHistoryTimeseriesValidatesGranularityLikeTS pins granularity to the TS
// timeSeriesQuerySchema enum (z.enum(["hour","day","month"]).default("day")): an
// absent param defaults to "day", a present out-of-enum value rejects with 400
// rather than silently falling back to "day".
func TestUsageHistoryTimeseriesValidatesGranularityLikeTS(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE api_requests (id TEXT PRIMARY KEY, provider TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create api_requests: %v", err)
	}
	h := &Handler{DB: sqlDB}

	for _, bad := range []string{"granularity=year", "granularity=minute", "granularity=Day", "granularity=", "granularity=hours"} {
		req := httptest.NewRequest(http.MethodGet, "/api/usage-history/time-series?"+bad, nil)
		w := httptest.NewRecorder()
		err := h.usageHistoryTimeseries(w, req)
		if err == nil {
			t.Fatalf("time-series?%s should reject with a 400 error, got nil (status %d body=%s)", bad, w.Code, w.Body.String())
		}
		if !strings.Contains(err.Error(), "granularity must be one of") {
			t.Fatalf("time-series?%s should reject with the granularity enum error, got %v", bad, err)
		}
	}

	for _, ok := range []string{"", "granularity=hour", "granularity=day", "granularity=month"} {
		path := "/api/usage-history/time-series"
		if ok != "" {
			path += "?" + ok
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		if err := h.usageHistoryTimeseries(w, req); err != nil {
			t.Fatalf("time-series %q should succeed, got error %v", path, err)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("time-series %q should be 200, got %d body=%s", path, w.Code, w.Body.String())
		}
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

func TestKiroProviderEndpointsExposeFrontendContractShape(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	writeKiroTestCredentials(t, []map[string]any{
		{
			"id":                "cred-a",
			"priority":          2,
			"accessToken":       "access-a",
			"email":             "a@example.com",
			"displayName":       "Account A",
			"subscriptionTitle": "Pro",
			"region":            "us-west-2",
			"profileArn":        "arn:test",
			"meterUsage":        42,
			"availableModels":   []any{"claude-z", "claude-a"},
		},
		{"id": "cred-b", "disabled": true, "availableModels": []any{"claude-a", "claude-b"}},
	})
	usagePath := filepath.Join(os.Getenv("HOME"), ".narrafork", "kiro", "kiro_usage.json")
	if err := os.WriteFile(usagePath, []byte(`{"cred-a":{"id":"cred-a","currentUsage":7,"usageLimit":100,"remaining":93,"usagePercentage":7,"queriedAt":"2026-05-26T00:00:00Z"}}`), 0o600); err != nil {
		t.Fatalf("write Kiro usage cache: %v", err)
	}
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	req := httptest.NewRequest(http.MethodGet, "/kiro/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	var status map[string]any
	decodeJSONBody(t, w, &status)
	if status["available"] != true || status["globalProxy"] == nil {
		t.Fatalf("status should expose available and globalProxy fields: %#v", status)
	}
	snapshot, _ := status["snapshot"].(map[string]any)
	if snapshot["currentId"] != "cred-a" || snapshot["total"] != float64(2) || snapshot["available"] != float64(1) || snapshot["loadBalancingMode"] != "priority" {
		t.Fatalf("unexpected snapshot summary: %#v", snapshot)
	}
	entries, ok := snapshot["entries"].([]any)
	if !ok || len(entries) != 2 {
		t.Fatalf("snapshot should expose entries array: %#v", snapshot)
	}
	entry, _ := entries[0].(map[string]any)
	if entry["id"] != "cred-a" || entry["hasProfileArn"] != true || entry["meterUsage"] != float64(42) {
		t.Fatalf("credential snapshot should expose frontend fields: %#v", entry)
	}
	usageCache, _ := status["usageCache"].(map[string]any)
	if cached, _ := usageCache["cred-a"].(map[string]any); cached["currentUsage"] != float64(7) {
		t.Fatalf("status should expose usage cache: %#v", usageCache)
	}

	req = httptest.NewRequest(http.MethodGet, "/kiro/models?credentialId=cred-a", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected models 200, got %d body=%s", w.Code, w.Body.String())
	}
	var modelsBody map[string]any
	decodeJSONBody(t, w, &modelsBody)
	models, ok := modelsBody["models"].([]any)
	if !ok || len(models) != 2 || modelsBody["fromCache"] != true || modelsBody["credentialId"] != "cred-a" {
		t.Fatalf("models should expose cached credential model array: %#v", modelsBody)
	}

	req = httptest.NewRequest(http.MethodGet, "/kiro/credentials/cred-a/usage", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected usage 200, got %d body=%s", w.Code, w.Body.String())
	}
	var usage map[string]any
	decodeJSONBody(t, w, &usage)
	if usage["id"] != "cred-a" || usage["currentUsage"] != float64(7) || usage["remaining"] != float64(93) {
		t.Fatalf("usage should return cached usage entry: %#v", usage)
	}

	req = httptest.NewRequest(http.MethodPost, "/kiro/models/refresh", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected Kiro model refresh disabled 501, got %d body=%s", w.Code, w.Body.String())
	}
	var disabled map[string]any
	decodeJSONBody(t, w, &disabled)
	if disabled["feature"] != "providers.kiro.modelsRefresh" || disabled["supported"] != false || disabled["fallback"] != true {
		t.Fatalf("refresh should expose explicit feature-disabled payload: %#v", disabled)
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

func writeKiroTestConfig(t *testing.T, config map[string]any) {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".narrafork", "kiro", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir kiro config dir: %v", err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal kiro config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write kiro config: %v", err)
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

func TestCodexCredentialMissingResponsesMatchTS(t *testing.T) {
	r, token := newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "admin")
	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		statusCode int
	}{
		{name: "disable", method: http.MethodPost, path: "/codex/credentials/missing/disable", statusCode: http.StatusNotFound},
		{name: "enable", method: http.MethodPost, path: "/codex/credentials/missing/enable", statusCode: http.StatusNotFound},
		{name: "reset", method: http.MethodPost, path: "/codex/credentials/missing/reset", statusCode: http.StatusNotFound},
		{name: "update", method: http.MethodPatch, path: "/codex/credentials/missing", body: `{"displayName":"Missing"}`, statusCode: http.StatusNotFound},
		{name: "delete", method: http.MethodDelete, path: "/codex/credentials/missing", statusCode: http.StatusNotFound},
		{name: "refresh", method: http.MethodPost, path: "/codex/credentials/missing/refresh", statusCode: http.StatusBadRequest},
		{name: "usage", method: http.MethodPost, path: "/codex/credentials/missing/usage", statusCode: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.Header.Set("Authorization", "Bearer "+token)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.statusCode {
				t.Fatalf("expected %d, got %d body=%s", tc.statusCode, w.Code, w.Body.String())
			}
			var response map[string]any
			decodeJSONBody(t, w, &response)
			if response["error"] != "Credential not found: missing" {
				t.Fatalf("unexpected missing credential body: %#v", response)
			}
		})
	}

	batchReq := httptest.NewRequest(http.MethodDelete, "/codex/credentials/batch", strings.NewReader(`{"ids":["missing"]}`))
	batchReq.Header.Set("Authorization", "Bearer "+token)
	batchReq.Header.Set("Content-Type", "application/json")
	batchW := httptest.NewRecorder()
	r.ServeHTTP(batchW, batchReq)
	if batchW.Code != http.StatusOK {
		t.Fatalf("expected batch delete 200, got %d body=%s", batchW.Code, batchW.Body.String())
	}
	var batchBody map[string]any
	decodeJSONBody(t, batchW, &batchBody)
	removed, _ := batchBody["removed"].([]any)
	notFound, _ := batchBody["notFound"].([]any)
	if len(removed) != 0 || len(notFound) != 1 || notFound[0] != "missing" {
		t.Fatalf("unexpected batch delete body: %#v", batchBody)
	}
}

// TestCodexCredentialBatchDeleteRejectsNonArrayWithTSMessage locks the
// DELETE /codex/credentials/batch validation against TS (server/routes/codex.ts):
// `if (!Array.isArray(ids) || ids.length === 0) throw "ids must be a non-empty
// array"`. A missing OR non-array `ids` (string/number/object) must return this
// exact message. Go previously decoded into a typed []string, so non-array values
// failed with a generic "invalid JSON" message.
func TestCodexCredentialBatchDeleteRejectsNonArrayWithTSMessage(t *testing.T) {
	r, token := newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "admin")
	for _, tc := range []struct{ name, body string }{
		{"missing ids", `{}`},
		{"ids string", `{"ids":"foo"}`},
		{"ids number", `{"ids":123}`},
		{"ids object", `{"ids":{"a":1}}`},
		{"ids empty array", `{"ids":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, "/codex/credentials/batch", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "ids must be a non-empty array") {
				t.Fatalf("expected 400 with TS message for %s, got %d body=%s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

// TestBatchKillTerminalsValidatesIdsLikeTS locks POST /admin/terminals/batch-kill
// validation against TS (server/routes/admin.ts): `if (!Array.isArray(ids) ||
// ids.length === 0 || !ids.every(id => typeof id === "string")) throw 400 "ids
// must be a non-empty string array"`. Go previously decoded into a typed []string
// and ran NO non-empty/string check, so an empty array silently returned 200.
func TestBatchKillTerminalsValidatesIdsLikeTS(t *testing.T) {
	h := &Handler{TerminalRuntime: &fakeRuntimeCleanupTerminalRuntime{}}
	for _, tc := range []struct{ name, body string }{
		{"missing ids", `{}`},
		{"ids string", `{"ids":"foo"}`},
		{"ids number", `{"ids":123}`},
		{"ids empty array", `{"ids":[]}`},
		{"ids non-string element", `{"ids":["a",123]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/admin/terminals/batch-kill", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			err := h.batchKillTerminals(w, req)
			var appErr *httpx.AppError
			if !errors.As(err, &appErr) || appErr.Status != http.StatusBadRequest {
				t.Fatalf("expected 400 AppError for %s, got %T %v", tc.name, err, err)
			}
			if appErr.Message != "ids must be a non-empty string array" {
				t.Fatalf("expected TS message for %s, got %q", tc.name, appErr.Message)
			}
		})
	}
}

func TestCodexCredentialDeleteUnhealthyRemovesAcrossPages(t *testing.T) {
	r, token := newProviderRoutesTestRouterWithStore(t, newSystemRoutesTestStore(t), "admin")
	dataDir, err := narraforkDataDir()
	if err != nil {
		t.Fatalf("data dir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	seed := []map[string]any{
		{"id": "ok", "accessToken": "at-ok", "priority": float64(0), "disabled": false},
		{"id": "manual", "accessToken": "at-manual", "priority": float64(1), "disabled": true, "disabledReason": "manual"},
		{"id": "quota", "accessToken": "at-quota", "priority": float64(2), "disabled": true, "disabledReason": "quota_exhausted"},
		{"id": "failed", "accessToken": "at-failed", "priority": float64(3), "disabled": true, "disabledReason": "too_many_failures"},
		{"id": "banned", "accessToken": "at-banned", "priority": float64(4), "disabled": true, "disabledReason": "banned"},
	}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), data, 0o600); err != nil {
		t.Fatalf("write codex credentials: %v", err)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/codex/status?unavailablePage=1&pageSize=1", nil)
	statusReq.Header.Set("Authorization", "Bearer "+token)
	statusW := httptest.NewRecorder()
	r.ServeHTTP(statusW, statusReq)
	if statusW.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", statusW.Code, statusW.Body.String())
	}
	var statusBody map[string]any
	decodeJSONBody(t, statusW, &statusBody)
	if numericMapOr(statusBody, "unhealthyTotal", 0) != 2 {
		t.Fatalf("expected unhealthyTotal=2, got %#v", statusBody)
	}
	if entries, _ := statusBody["unavailableEntries"].([]any); len(entries) != 1 {
		t.Fatalf("expected paged unavailable entries, got %#v", statusBody["unavailableEntries"])
	}

	req := httptest.NewRequest(http.MethodDelete, "/codex/credentials/unhealthy", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected delete unhealthy 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	removed, _ := body["removed"].([]any)
	if len(removed) != 2 || removed[0] != "failed" || removed[1] != "banned" {
		t.Fatalf("unexpected delete unhealthy body: %#v", body)
	}

	updated, err := loadCodexCredentials()
	if err != nil {
		t.Fatalf("load updated credentials: %v", err)
	}
	ids := []string{}
	for _, entry := range updated {
		ids = append(ids, stringMap(entry, "id", ""))
	}
	if strings.Join(ids, ",") != "ok,manual,quota" {
		t.Fatalf("unexpected remaining credentials: %#v", ids)
	}
}

func TestCodexCredentialUpdateEmptyAndInvalidBodyMatchesTS(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	dataDir, err := narraforkDataDir()
	if err != nil {
		t.Fatalf("data dir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	seed := []map[string]any{{
		"id":           "codex-contract",
		"displayName":  "Codex Contract Fixture",
		"accessToken":  "codex-access-fixture",
		"refreshToken": "codex-refresh-fixture",
		"accountId":    "codex-account-fixture",
		"email":        "codex-contract@example.com",
		"sub":          "codex-sub-fixture",
		"priority":     float64(0),
	}}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), data, 0o600); err != nil {
		t.Fatalf("write codex credentials: %v", err)
	}

	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	cases := []struct {
		name       string
		path       string
		body       string
		statusCode int
		error      string
	}{
		{name: "empty existing", path: "/codex/credentials/codex-contract", statusCode: http.StatusOK},
		{name: "invalid existing", path: "/codex/credentials/codex-contract", body: `{`, statusCode: http.StatusOK},
		{name: "invalid missing", path: "/codex/credentials/missing", body: `{`, statusCode: http.StatusNotFound, error: "Credential not found: missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(http.MethodPatch, tc.path, body)
			req.Header.Set("Authorization", "Bearer "+token)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.statusCode {
				t.Fatalf("expected %d, got %d body=%s", tc.statusCode, w.Code, w.Body.String())
			}
			var response map[string]any
			decodeJSONBody(t, w, &response)
			if tc.error != "" {
				if response["error"] != tc.error {
					t.Fatalf("unexpected error body: %#v", response)
				}
			} else if response["ok"] != true {
				t.Fatalf("expected ok response, got %#v", response)
			}
		})
	}
}

func TestCodexImportValidationMatchesTS(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	cases := []struct {
		name          string
		body          map[string]any
		expectedError string
	}{
		{
			name:          "missing credentials",
			body:          map[string]any{},
			expectedError: "No credentials provided",
		},
		{
			name: "credentials without token",
			body: map[string]any{"credentials": []any{map[string]any{
				"email": "codex-no-token@example.com",
			}}},
			expectedError: "No valid credentials: each credential must have a non-empty refreshToken or accessToken",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/codex/import", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			decodeJSONBody(t, w, &body)
			if body["error"] != tc.expectedError {
				t.Fatalf("unexpected import error: %#v", body)
			}
		})
	}
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

// TestCodexImportWrapsSingleObjectCredentialsLikeTS locks the /codex/import
// credentials coercion against TS (server/routes/codex.ts): `credentials ===
// undefined ? [] : Array.isArray(credentials) ? credentials : [credentials]`.
// A single (non-array) object credentials value must be wrapped and imported, not
// rejected. Go previously decoded credentials into a typed []any, so a
// single-object value failed the unmarshal with a generic "invalid JSON" message.
func TestCodexImportWrapsSingleObjectCredentialsLikeTS(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	accessToken := testJWT(map[string]any{
		"sub":                "user-single-obj",
		"email":              "single-obj@example.com",
		"chatgpt_account_id": "acc-single-obj",
	})
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	// credentials is a single object (NOT an array) -> TS wraps to [credentials].
	payload, _ := json.Marshal(map[string]any{"credentials": map[string]any{
		"refresh_token": "",
		"access_token":  accessToken,
	}})
	req := httptest.NewRequest(http.MethodPost, "/codex/import", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for single-object credentials, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeJSONBody(t, w, &body)
	if body["added"] != float64(1) {
		t.Fatalf("expected single-object credentials to import, got %#v", body)
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

func TestClineEnabledModelsRejectsNonArrayWithTSMessage(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	provider := map[string]any{"id": "cline1", "name": "Cline", "prefix": "cline", "enabledModels": []any{}}
	if _, err := store.Patch(map[string]any{"clineProviders": []any{provider}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")

	// Mirror TS `!Array.isArray(body.models) -> 400 "models must be an array"`: a
	// missing OR non-array models (string/number/object/null) returns this exact
	// message. Go previously decoded into a typed []string, so non-array values
	// failed with a generic "invalid JSON" message.
	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing models", `{}`},
		{"models string", `{"models":"foo"}`},
		{"models number", `{"models":123}`},
		{"models object", `{"models":{"a":1}}`},
		{"models null", `{"models":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/cline/enabled-models", bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for %s, got %d body=%s", tc.name, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "models must be an array") {
				t.Fatalf("expected TS message for %s, got body=%s", tc.name, w.Body.String())
			}
		})
	}

	// An empty array is a valid request (count 0).
	req := httptest.NewRequest(http.MethodPost, "/cline/enabled-models", bytes.NewReader([]byte(`{"models":[]}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for empty array, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestClineBrowserAuthFlowPersistsCredentialsAndAutoAddsFreeModels(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	provider := map[string]any{
		"id":            "cline1",
		"name":          "Cline",
		"prefix":        "clinecache",
		"enabledModels": []any{},
	}
	if _, err := store.Patch(map[string]any{"clineProviders": []any{provider}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.WriteFile(filepath.Join(dataDir, "cline-models.json"), []byte(`{"cline1":[{"id":"cline/free-fixture","contextLength":123456}]}`), 0o600); err != nil {
		t.Fatalf("write cline cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "cline-recommended-models.json"), []byte(`{"recommended":[],"free":[{"id":"cline/free-fixture","name":"Cline Free Fixture","tags":["FREE"]}]}`), 0o600); err != nil {
		t.Fatalf("write cline recommended cache: %v", err)
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/authorize" {
			http.NotFound(w, r)
			return
		}
		callbackURL := r.URL.Query().Get("callback_url")
		if r.URL.Query().Get("client_type") != "extension" || callbackURL != "http://localhost:19876/auth/callback" || r.URL.Query().Get("redirect_uri") != callbackURL {
			http.Error(w, "unexpected authorize query: "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		redirectURL := "http://" + r.Host + "/oauth/authorize?callback_url=" + url.QueryEscape(callbackURL) + "&state=browser-fixture"
		_ = json.NewEncoder(w).Encode(map[string]any{"redirect_url": redirectURL})
	}))
	defer fixture.Close()
	defer (&Handler{Settings: store}).stopClineBrowserAuth()
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	payload, err := json.Marshal(map[string]any{"apiBaseUrl": fixture.URL})
	if err != nil {
		t.Fatalf("marshal auth browser payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/cline/auth/browser", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected browser auth 200, got %d body=%s", w.Code, w.Body.String())
	}
	var startBody map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &startBody); err != nil {
		t.Fatalf("decode start body: %v", err)
	}
	authorizeURL, ok := startBody["authorizeUrl"].(string)
	if !ok || authorizeURL == "" {
		t.Fatalf("missing authorizeUrl: %#v", startBody)
	}
	parsedAuthorizeURL, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("parse authorizeUrl: %v", err)
	}
	callbackBase := parsedAuthorizeURL.Query().Get("callback_url")
	if callbackBase != "http://localhost:19876/auth/callback" {
		t.Fatalf("authorize URL should preserve callback_url, got %s", authorizeURL)
	}
	callbackData, err := json.Marshal(map[string]any{
		"accessToken":  "cline-browser-access-token",
		"refreshToken": "cline-browser-refresh-token",
		"email":        "browser@example.com",
		"name":         "Browser User",
		"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal callback data: %v", err)
	}
	callbackResp, err := http.Get(callbackBase + "?code=" + url.QueryEscape(base64.StdEncoding.EncodeToString(callbackData)))
	if err != nil {
		t.Fatalf("call local callback server: %v", err)
	}
	defer callbackResp.Body.Close()
	callbackBody, _ := io.ReadAll(callbackResp.Body)
	if callbackResp.StatusCode != http.StatusOK || !strings.Contains(string(callbackBody), "Authorization successful") {
		t.Fatalf("unexpected callback response: status=%d body=%s", callbackResp.StatusCode, string(callbackBody))
	}

	providers, _ := store.Raw()["clineProviders"].([]any)
	storedProvider, _ := providers[0].(map[string]any)
	if storedProvider["accessToken"] != "cline-browser-access-token" {
		t.Fatalf("provider access token not stored: %#v", storedProvider)
	}
	enabled := stringList(storedProvider["enabledModels"])
	if len(enabled) != 1 || enabled[0] != "cline/free-fixture" {
		t.Fatalf("free model should be auto-enabled after browser callback: %#v", storedProvider)
	}
	agent, _ := store.Raw()["agent"].(map[string]any)
	windows, _ := agent["modelContextWindows"].(map[string]any)
	if windows["clinecache:cline/free-fixture"] != float64(123456) {
		t.Fatalf("browser callback should populate context window from Cline cache: %#v", windows)
	}
}

func TestClineAuthCallbackPersistsCredentialsAndAutoAddsFreeModels(t *testing.T) {
	store := newSystemRoutesTestStore(t)
	provider := map[string]any{
		"id":            "cline1",
		"name":          "Cline",
		"prefix":        "clinecache",
		"enabledModels": []any{},
	}
	if _, err := store.Patch(map[string]any{"clineProviders": []any{provider}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.WriteFile(filepath.Join(dataDir, "cline-models.json"), []byte(`{"cline1":[{"id":"cline/free-fixture","contextLength":123456}]}`), 0o600); err != nil {
		t.Fatalf("write cline cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "cline-recommended-models.json"), []byte(`{"recommended":[],"free":[{"id":"cline/free-fixture","name":"Cline Free Fixture","tags":["FREE"]}]}`), 0o600); err != nil {
		t.Fatalf("write cline recommended cache: %v", err)
	}
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	callbackData, err := json.Marshal(map[string]any{
		"accessToken":  "cline-access-token",
		"refreshToken": "cline-refresh-token",
		"email":        "alice@example.com",
		"name":         "Alice Cline",
		"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal callback data: %v", err)
	}
	callbackURL := "http://localhost:19876/auth/callback?code=" + url.QueryEscape(base64.StdEncoding.EncodeToString(callbackData))
	payload, err := json.Marshal(map[string]any{"callbackUrl": callbackURL})
	if err != nil {
		t.Fatalf("marshal callback payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/cline/auth/callback", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
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
	if body["ok"] != true || body["email"] != "alice@example.com" || body["displayName"] != "Alice Cline" {
		t.Fatalf("unexpected callback response: %#v", body)
	}

	providers, _ := store.Raw()["clineProviders"].([]any)
	storedProvider, _ := providers[0].(map[string]any)
	if storedProvider["accessToken"] != "cline-access-token" {
		t.Fatalf("provider access token not stored: %#v", storedProvider)
	}
	enabled := stringList(storedProvider["enabledModels"])
	if len(enabled) != 1 || enabled[0] != "cline/free-fixture" {
		t.Fatalf("free model should be auto-enabled after callback: %#v", storedProvider)
	}
	agent, _ := store.Raw()["agent"].(map[string]any)
	windows, _ := agent["modelContextWindows"].(map[string]any)
	if windows["clinecache:cline/free-fixture"] != float64(123456) {
		t.Fatalf("callback should populate context window from Cline cache: %#v", windows)
	}
	credsRaw, err := os.ReadFile(filepath.Join(dataDir, "cline-credentials.json"))
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	var creds map[string]any
	if err := json.Unmarshal(credsRaw, &creds); err != nil {
		t.Fatalf("decode credentials: %v", err)
	}
	if creds["accessToken"] != "cline-access-token" || creds["refreshToken"] != "cline-refresh-token" {
		t.Fatalf("callback credentials not persisted: %#v", creds)
	}
}

func TestClineInvalidRefreshClearsCredentialsAndKeepsBalance422(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cline/api/v1/auth/refresh" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad refresh body", http.StatusBadRequest)
			return
		}
		if body["refreshToken"] != "cline-invalid-refresh-token" || body["grantType"] != "refresh_token" {
			http.Error(w, "bad refresh payload", http.StatusBadRequest)
			return
		}
		http.Error(w, "invalid refresh token", http.StatusUnauthorized)
	}))
	defer upstream.Close()
	t.Setenv("NARRAFORK_CLINE_API_BASE_URL", upstream.URL+"/cline")
	store := newSystemRoutesTestStore(t)
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	credentialsPath := filepath.Join(dataDir, "cline-credentials.json")
	creds := map[string]any{
		"accessToken":  "cline-expired-access-token",
		"refreshToken": "cline-invalid-refresh-token",
		"expiresAt":    float64(time.Now().Add(-time.Hour).Unix()),
		"email":        "cline-expired@example.com",
		"displayName":  "Cline Expired User",
		"userId":       "cline-refresh-user-fixture",
		"startedAt":    float64(time.Now().UnixMilli()),
	}
	credsRaw, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal expired credentials: %v", err)
	}
	if err := os.WriteFile(credentialsPath, credsRaw, 0o600); err != nil {
		t.Fatalf("write expired credentials: %v", err)
	}

	balanceReq := httptest.NewRequest(http.MethodGet, "/cline/balance", nil)
	balanceReq.Header.Set("Authorization", "Bearer "+token)
	balanceW := httptest.NewRecorder()
	r.ServeHTTP(balanceW, balanceReq)
	if balanceW.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected balance 422, got %d body=%s", balanceW.Code, balanceW.Body.String())
	}
	var balanceBody map[string]any
	decodeJSONBody(t, balanceW, &balanceBody)
	if balanceBody["error"] != "Not authenticated or balance unavailable" {
		t.Fatalf("unexpected balance body after invalid refresh: %#v", balanceBody)
	}
	if _, err := os.Stat(credentialsPath); !os.IsNotExist(err) {
		t.Fatalf("invalid refresh should remove credentials, stat err=%v", err)
	}
}

func TestClineExpiredCredentialsRefreshBeforeBalance(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/cline/api/v1/auth/refresh":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad refresh body", http.StatusBadRequest)
				return
			}
			if body["refreshToken"] != "cline-expired-refresh-token" || body["grantType"] != "refresh_token" {
				http.Error(w, "bad refresh token", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"accessToken":"cline-refreshed-access-token","refreshToken":"cline-refreshed-refresh-token","expiresAt":"2099-01-01T00:00:00Z","userInfo":{"email":"cline-refreshed@example.com","name":"Cline Refreshed User"}}}`))
		case "/cline/api/v1/users/cline-refresh-user-fixture/balance":
			if r.Header.Get("Authorization") != "Bearer workos:cline-refreshed-access-token" {
				http.Error(w, "bad refreshed auth", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"balance":654321,"userId":"cline-refresh-user-fixture"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	t.Setenv("NARRAFORK_CLINE_API_BASE_URL", upstream.URL+"/cline")
	store := newSystemRoutesTestStore(t)
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	creds := map[string]any{
		"accessToken":  "cline-expired-access-token",
		"refreshToken": "cline-expired-refresh-token",
		"expiresAt":    float64(time.Now().Add(-time.Hour).Unix()),
		"email":        "cline-expired@example.com",
		"displayName":  "Cline Expired User",
		"userId":       "cline-refresh-user-fixture",
		"startedAt":    float64(time.Now().UnixMilli()),
	}
	credsRaw, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal expired credentials: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "cline-credentials.json"), credsRaw, 0o600); err != nil {
		t.Fatalf("write expired credentials: %v", err)
	}

	balanceReq := httptest.NewRequest(http.MethodGet, "/cline/balance", nil)
	balanceReq.Header.Set("Authorization", "Bearer "+token)
	balanceW := httptest.NewRecorder()
	r.ServeHTTP(balanceW, balanceReq)
	if balanceW.Code != http.StatusOK {
		t.Fatalf("expected balance 200, got %d body=%s", balanceW.Code, balanceW.Body.String())
	}
	var balanceBody map[string]any
	decodeJSONBody(t, balanceW, &balanceBody)
	if balanceBody["balance"] != float64(654321) || balanceBody["userId"] != "cline-refresh-user-fixture" {
		t.Fatalf("unexpected balance body after refresh: %#v", balanceBody)
	}
	storedRaw, err := os.ReadFile(filepath.Join(dataDir, "cline-credentials.json"))
	if err != nil {
		t.Fatalf("read refreshed credentials: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(storedRaw, &stored); err != nil {
		t.Fatalf("decode refreshed credentials: %v", err)
	}
	if stored["accessToken"] != "cline-refreshed-access-token" || stored["refreshToken"] != "cline-refreshed-refresh-token" {
		t.Fatalf("refresh did not persist new tokens: %#v", stored)
	}
	if stored["email"] != "cline-refreshed@example.com" || stored["displayName"] != "Cline Refreshed User" || stored["userId"] != "cline-refresh-user-fixture" {
		t.Fatalf("refresh did not preserve account metadata: %#v", stored)
	}
}

func TestClineUserInfoRefreshAndBalanceUseAccountFixture(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer workos:cline-access-token") {
			http.Error(w, "bad cline auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/cline/api/v1/users/me":
			_, _ = w.Write([]byte(`{"data":{"id":"cline-user-fixture","email":"cline-updated@example.com","displayName":"Cline Updated User","name":"Cline Updated Fallback"}}`))
		case "/cline/api/v1/users/cline-user-fixture/balance":
			_, _ = w.Write([]byte(`{"success":true,"data":{"balance":123456,"userId":"cline-user-fixture"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	t.Setenv("NARRAFORK_CLINE_API_BASE_URL", upstream.URL+"/cline")
	store := newSystemRoutesTestStore(t)
	r, token := newProviderRoutesTestRouterWithStore(t, store, "admin")
	dataDir := filepath.Join(os.Getenv("HOME"), ".narrafork")
	if err := os.WriteFile(filepath.Join(dataDir, "cline-recommended-models.json"), []byte(`{"recommended":[],"free":[]}`), 0o600); err != nil {
		t.Fatalf("write cline recommended cache: %v", err)
	}
	callbackData, err := json.Marshal(map[string]any{
		"accessToken":  "cline-access-token",
		"refreshToken": "cline-refresh-token",
		"email":        "alice@example.com",
		"name":         "Alice Cline",
		"expiresAt":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("marshal callback data: %v", err)
	}
	callbackURL := "http://localhost:19876/auth/callback?code=" + url.QueryEscape(base64.StdEncoding.EncodeToString(callbackData))
	payload, err := json.Marshal(map[string]any{"callbackUrl": callbackURL})
	if err != nil {
		t.Fatalf("marshal callback payload: %v", err)
	}
	callbackReq := httptest.NewRequest(http.MethodPost, "/cline/auth/callback", bytes.NewReader(payload))
	callbackReq.Header.Set("Content-Type", "application/json")
	callbackReq.Header.Set("Authorization", "Bearer "+token)
	callbackW := httptest.NewRecorder()
	r.ServeHTTP(callbackW, callbackReq)
	if callbackW.Code != http.StatusOK {
		t.Fatalf("expected callback 200, got %d body=%s", callbackW.Code, callbackW.Body.String())
	}

	refreshReq := httptest.NewRequest(http.MethodPost, "/cline/user-info/refresh", nil)
	refreshReq.Header.Set("Authorization", "Bearer "+token)
	refreshW := httptest.NewRecorder()
	r.ServeHTTP(refreshW, refreshReq)
	if refreshW.Code != http.StatusOK {
		t.Fatalf("expected user-info refresh 200, got %d body=%s", refreshW.Code, refreshW.Body.String())
	}
	var refreshBody map[string]any
	decodeJSONBody(t, refreshW, &refreshBody)
	if refreshBody["authenticated"] != true || refreshBody["email"] != "cline-updated@example.com" || refreshBody["displayName"] != "Cline Updated User" || refreshBody["userId"] != "cline-user-fixture" {
		t.Fatalf("unexpected user-info refresh body: %#v", refreshBody)
	}

	balanceReq := httptest.NewRequest(http.MethodGet, "/cline/balance", nil)
	balanceReq.Header.Set("Authorization", "Bearer "+token)
	balanceW := httptest.NewRecorder()
	r.ServeHTTP(balanceW, balanceReq)
	if balanceW.Code != http.StatusOK {
		t.Fatalf("expected balance 200, got %d body=%s", balanceW.Code, balanceW.Body.String())
	}
	var balanceBody map[string]any
	decodeJSONBody(t, balanceW, &balanceBody)
	if balanceBody["balance"] != float64(123456) || balanceBody["userId"] != "cline-user-fixture" {
		t.Fatalf("unexpected balance body: %#v", balanceBody)
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

func anyStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s, ok := value.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func decodeJSONBody(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
