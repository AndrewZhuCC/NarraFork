package systemroutes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/config"
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
	if body["supported"] != false || body["fallback"] != true || body["code"] != "WEIXIN_QR_UNSUPPORTED" {
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

	r := chi.NewRouter()
	h := &Handler{Settings: store}
	h.RegisterProviders(r)

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
	r := chi.NewRouter()
	h := &Handler{Settings: store}
	h.RegisterProviders(r)
	payload := []byte(`{"models":["anthropic/claude-test"]}`)
	req := httptest.NewRequest(http.MethodPost, "/cline/enabled-models", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
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

func decodeJSONBody(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
