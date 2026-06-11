package settings

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/compat/bunfinal"
	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/restart"
)

func newTestStore(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return store
}

func TestGetSettingsIncludesTSOptionalProviderArraysWithoutPersistingDefaults(t *testing.T) {
	store := newTestStore(t)
	for _, field := range []string{"nkpProviders", "nugProviders", "clineProviders"} {
		if _, ok := store.Raw()[field]; ok {
			t.Fatalf("test fixture should not persist optional default %s", field)
		}
	}
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	if err := h.get(w, req); err != nil {
		t.Fatalf("get settings returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	for _, field := range []string{"nkpProviders", "nugProviders", "clineProviders"} {
		items, ok := body[field].([]any)
		if !ok || len(items) != 0 {
			t.Fatalf("expected %s empty array in response, got %#v", field, body[field])
		}
		if _, persisted := store.Raw()[field]; persisted {
			t.Fatalf("%s should be response-only and not persisted into settings defaults", field)
		}
	}
}

func TestPatchSettingsIncludesAugmentedResponseShape(t *testing.T) {
	store := newTestStore(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve test home: %v", err)
	}
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "custom-api-quotas.json"), []byte(`{"provider":{"quotaBalance":"$42.50","detailedQuotaBalance":"42.50 credits","fetchedAt":1700000000000}}`), 0o600); err != nil {
		t.Fatalf("write custom api quota cache: %v", err)
	}

	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(`{"agent":{"summaryModel":"kiro:claude-haiku-4.5"}}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch settings returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	for _, key := range []string{
		"anthropicModelsGrouped",
		"builtinModelContextWindows",
		"clineModelsGrouped",
		"codexAvailable",
		"codexModels",
		"customApiQuotas",
		"kiroModels",
		"lanAddresses",
		"nkpModelsGrouped",
		"nkpQuotas",
		"nugModelsGrouped",
		"openaiModels",
		"openaiModelsGrouped",
		"summaryModelAvailable",
	} {
		if _, ok := body[key]; !ok {
			t.Fatalf("PATCH response missing TS-compatible augmented key %s in %#v", key, body)
		}
	}
	if _, persisted := store.Raw()["customApiQuotas"]; persisted {
		t.Fatalf("augmented response keys should not be persisted into settings")
	}
}

func TestGetSettingsIncludesCustomApiQuotaCacheShape(t *testing.T) {
	store := newTestStore(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve test home: %v", err)
	}
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "custom-api-quotas.json"), []byte(`{"provider":{"quotaBalance":"$42.50","detailedQuotaBalance":"42.50 credits\nreset soon","fetchedAt":1700000000000}}`), 0o600); err != nil {
		t.Fatalf("write custom api quota cache: %v", err)
	}

	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	if err := h.get(w, req); err != nil {
		t.Fatalf("get settings returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	quotas, ok := body["customApiQuotas"].(map[string]any)
	if !ok {
		t.Fatalf("expected customApiQuotas object, got %#v", body["customApiQuotas"])
	}
	quota, ok := quotas["provider"].(map[string]any)
	if !ok {
		t.Fatalf("expected provider quota entry, got %#v", quotas)
	}
	if quota["quotaBalance"] != "$42.50" || quota["detailedQuotaBalance"] != "42.50 credits\nreset soon" || quota["fetchedAt"] != float64(1700000000000) || len(quota) != 3 {
		t.Fatalf("unexpected custom API quota shape: %#v", quota)
	}
}

func TestGetSettingsMatchesTSKiroAndNugQuotaResponseShape(t *testing.T) {
	store := newTestStore(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve test home: %v", err)
	}
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "nug-quotas.json"), []byte(`{"provider":{"balance":12,"totalGranted":34}}`), 0o600); err != nil {
		t.Fatalf("write nug quota cache: %v", err)
	}
	customQuotaFixture := `{"openai-secret-fixture":{"quotaBalance":"$42.50","detailedQuotaBalance":"42.50 credits\nreset soon","fetchedAt":1700000000000}}`
	if err := os.WriteFile(filepath.Join(dataDir, "custom-api-quotas.json"), []byte(customQuotaFixture), 0o600); err != nil {
		t.Fatalf("write custom API quota cache: %v", err)
	}
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	if err := h.get(w, req); err != nil {
		t.Fatalf("get settings returned error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if body["kiroModels"] != nil {
		t.Fatalf("fresh Go settings should mirror TS unavailable Kiro null, got %#v", body["kiroModels"])
	}
	if _, ok := body["nugQuotas"]; ok {
		t.Fatalf("TS settings response does not expose top-level nugQuotas, got %#v", body["nugQuotas"])
	}
	customApiQuotas, ok := body["customApiQuotas"].(map[string]any)
	if !ok {
		t.Fatalf("expected customApiQuotas object, got %#v", body["customApiQuotas"])
	}
	customApiQuota, ok := customApiQuotas["openai-secret-fixture"].(map[string]any)
	if !ok {
		t.Fatalf("expected custom API quota fixture, got %#v", customApiQuotas)
	}
	if customApiQuota["quotaBalance"] != "$42.50" || customApiQuota["detailedQuotaBalance"] != "42.50 credits\nreset soon" || customApiQuota["fetchedAt"] != float64(1700000000000) || len(customApiQuota) != 3 {
		t.Fatalf("unexpected custom API quota shape: %#v", customApiQuota)
	}

	kiroDir := filepath.Join(dataDir, "kiro")
	if err := os.MkdirAll(kiroDir, 0o700); err != nil {
		t.Fatalf("mkdir kiro dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(kiroDir, "credentials.json"), []byte(`[{"availableModels":["kiro-model-a"]}]`), 0o600); err != nil {
		t.Fatalf("write kiro credentials: %v", err)
	}
	w = httptest.NewRecorder()
	if err := h.get(w, req); err != nil {
		t.Fatalf("get settings with kiro credentials returned error: %v", err)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings response with kiro credentials: %v", err)
	}
	models, ok := body["kiroModels"].([]any)
	if !ok || len(models) != 1 {
		t.Fatalf("expected Kiro credentials to produce model array, got %#v", body["kiroModels"])
	}
}

type bunFinalSettingsRoundtripArtifact struct {
	Cases []bunFinalSettingsRoundtripCase `json:"cases"`
}

type bunFinalSettingsRoundtripCase struct {
	ID                      string         `json:"id"`
	Method                  string         `json:"method"`
	Path                    string         `json:"path"`
	Body                    map[string]any `json:"body"`
	RequiredResponsePaths   []string       `json:"requiredResponsePaths"`
	DisallowedResponsePaths []string       `json:"disallowedResponsePaths"`
	RequiredPersistedPaths  []string       `json:"requiredPersistedPaths"`
	ExpectedErrorCode       string         `json:"expectedErrorCode"`
}

func TestBunFinalSettingsRoundtripCases(t *testing.T) {
	data, err := bunfinal.ReadSettingsRoundtripCases()
	if err != nil {
		t.Fatalf("read settings roundtrip artifact: %v", err)
	}
	var artifact bunFinalSettingsRoundtripArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("decode settings roundtrip artifact: %v", err)
	}
	if len(artifact.Cases) == 0 {
		t.Fatal("settings roundtrip artifact should contain cases")
	}
	for _, tc := range artifact.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			store := newTestStore(t)
			h := &Handler{Store: store}
			r := chi.NewRouter()
			r.Route("/api/settings", h.RegisterRoutes)
			var body *bytes.Reader
			if tc.Body != nil {
				encoded, err := json.Marshal(tc.Body)
				if err != nil {
					t.Fatalf("encode fixture body: %v", err)
				}
				body = bytes.NewReader(encoded)
			} else {
				body = bytes.NewReader(nil)
			}
			req := httptest.NewRequest(tc.Method, tc.Path, body)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			var response map[string]any
			if strings.TrimSpace(w.Body.String()) != "" {
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode response status=%d body=%s: %v", w.Code, w.Body.String(), err)
				}
			}
			if tc.ExpectedErrorCode != "" {
				if w.Code < 400 {
					t.Fatalf("expected error %s, got status=%d body=%s", tc.ExpectedErrorCode, w.Code, w.Body.String())
				}
				if response["code"] != tc.ExpectedErrorCode {
					t.Fatalf("expected error code %s, got %#v", tc.ExpectedErrorCode, response)
				}
				return
			}
			if w.Code < 200 || w.Code >= 300 {
				t.Fatalf("expected success, got status=%d body=%s", w.Code, w.Body.String())
			}
			for _, path := range tc.RequiredResponsePaths {
				if _, ok := bunFinalSettingsLookupPath(response, path); !ok {
					t.Fatalf("required response path %q missing from %#v", path, response)
				}
			}
			for _, path := range tc.DisallowedResponsePaths {
				if value, ok := bunFinalSettingsLookupPath(response, path); ok && value != nil {
					t.Fatalf("disallowed response path %q present with %#v in %#v", path, value, response)
				}
			}
			for _, path := range tc.RequiredPersistedPaths {
				persisted, ok := bunFinalSettingsLookupPath(store.Raw(), path)
				if !ok {
					t.Fatalf("required persisted path %q missing from %#v", path, store.Raw())
				}
				if expected, ok := bunFinalSettingsLookupPath(tc.Body, path); ok && persisted != expected {
					t.Fatalf("persisted path %q=%#v, want %#v", path, persisted, expected)
				}
			}
		})
	}
}

func bunFinalSettingsLookupPath(root map[string]any, path string) (any, bool) {
	if root == nil {
		return nil, false
	}
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) > 0 && parts[0] == "settings" {
		if _, ok := root["settings"]; !ok {
			parts = parts[1:]
		}
	}
	var current any = root
	for _, part := range parts {
		if part == "" {
			continue
		}
		mapped, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = mapped[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func TestContextThresholdsMatchesFrontendShape(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/settings/context-thresholds?model=test&provider=openai", nil)
	w := httptest.NewRecorder()

	if err := h.contextThresholds(w, req); err != nil {
		t.Fatalf("contextThresholds returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["pruneStart"] != float64(95) || body["compactStart"] != float64(99) {
		t.Fatalf("unexpected thresholds: %#v", body)
	}
	if _, ok := body["warning"]; ok {
		t.Fatalf("legacy warning key should not be returned: %#v", body)
	}
	if _, ok := body["critical"]; ok {
		t.Fatalf("legacy critical key should not be returned: %#v", body)
	}
}

func TestContextThresholdsUsesConfiguredLargeTier(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Patch(map[string]any{"agent": map[string]any{
		"modelContextWindows": map[string]any{"openai:huge": float64(1000000)},
		"contextThresholds": map[string]any{
			"standard": map[string]any{"pruneStart": float64(80), "compactStart": float64(90)},
			"large":    map[string]any{"pruneStart": float64(70), "compactStart": float64(85)},
		},
	}})
	if err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/api/settings/context-thresholds?model=huge&provider=openai", nil)
	w := httptest.NewRecorder()

	if err := h.contextThresholds(w, req); err != nil {
		t.Fatalf("contextThresholds returned error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["pruneStart"] != float64(70) || body["compactStart"] != float64(85) {
		t.Fatalf("unexpected large thresholds: %#v", body)
	}
}

func TestTestModelRejectsOverlongPrompt(t *testing.T) {
	h := &Handler{}
	payload, _ := json.Marshal(map[string]any{
		"model":  "missing:model",
		"prompt": strings.Repeat("x", testModelPromptMaxChars+1),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/test-model", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := h.testModel(w, req)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Status != http.StatusBadRequest || appErr.Code != "VALIDATION_ERROR" {
		t.Fatalf("unexpected validation error: %#v", appErr)
	}
}

func TestTestModelRejectsUnresolvedModelBeforeGenerate(t *testing.T) {
	h := &Handler{Store: newTestStore(t)}
	payload, _ := json.Marshal(map[string]any{"model": "missing:model", "prompt": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/test-model", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := h.testModel(w, req)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Status != http.StatusBadRequest {
		t.Fatalf("expected unresolved model to return 400, got %#v", appErr)
	}
}

func TestTestModelMapsGenerateFailureToBadGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream failed", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	store := newTestStore(t)
	if _, err := store.Patch(map[string]any{
		"openaiProviders": []any{map[string]any{
			"id":           "test-provider",
			"name":         "Test Provider",
			"prefix":       "openai",
			"apiKey":       "sk-test",
			"baseUrl":      upstream.URL,
			"defaultModel": "gpt-test",
		}},
	}); err != nil {
		t.Fatalf("patch provider settings: %v", err)
	}

	h := &Handler{Store: store}
	payload, _ := json.Marshal(map[string]any{"model": "openai:gpt-test", "prompt": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/api/settings/test-model", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	httpx.Handler(h.testModel).ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected generate failure to return 502, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "BAD_GATEWAY" || body["error"] == "" {
		t.Fatalf("unexpected bad gateway body: %#v", body)
	}
}

func TestPatchRejectsInvalidServerPort(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(`{"server":{"port":70000}}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := h.patch(w, req)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, "server.port") {
		t.Fatalf("unexpected validation error: %#v", appErr)
	}
}

func TestPatchRejectsNullServerTLS(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(`{"server":{"tls":null}}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := h.patch(w, req)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, "server.tls") {
		t.Fatalf("unexpected validation error: %#v", appErr)
	}
}

func TestPatchRejectsMissingTLSFilesAsValidationError(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	missingCert := filepath.Join(t.TempDir(), "missing-cert.pem")
	missingKey := filepath.Join(t.TempDir(), "missing-key.pem")
	payload, _ := json.Marshal(map[string]any{"server": map[string]any{"tls": map[string]any{"enabled": true, "certFile": missingCert, "keyFile": missingKey}}})
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := h.patch(w, req)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, "TLS certificate file not found") {
		t.Fatalf("unexpected TLS validation error: %#v", appErr)
	}
}

func TestPatchRejectsInvalidContainerProxyPort(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(`{"containers":{"proxy":{"port":70000}}}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := h.patch(w, req)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, "containers.proxy.port") {
		t.Fatalf("unexpected validation error: %#v", appErr)
	}
}

func TestPatchRejectsInvalidAgentScalarSettings(t *testing.T) {
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"agent":{"defaultModel":""}}`, field: "agent.defaultModel"},
		{payload: `{"agent":{"summaryModel":1}}`, field: "agent.summaryModel"},
		{payload: `{"agent":{"summaryModel":null}}`, field: "agent.summaryModel"},
		{payload: `{"agent":{"defaultPermissionMode":"unknown"}}`, field: "agent.defaultPermissionMode"},
		{payload: `{"agent":{"dangerReflectionLevel":"extreme"}}`, field: "agent.dangerReflectionLevel"},
		{payload: `{"agent":{"defaultStartInPlanMode":"yes"}}`, field: "agent.defaultStartInPlanMode"},
		{payload: `{"agent":{"defaultReasoningEffort":"extreme"}}`, field: "agent.defaultReasoningEffort"},
		{payload: `{"agent":{"defaultReasoningEffort":null}}`, field: "agent.defaultReasoningEffort"},
		{payload: `{"agent":{"maxTurns":0}}`, field: "agent.maxTurns"},
		{payload: `{"agent":{"retryBackoffCeilMs":999}}`, field: "agent.retryBackoffCeilMs"},
		{payload: `{"agent":{"firstTokenTimeoutMs":600001}}`, field: "agent.firstTokenTimeoutMs"},
		{payload: `{"agent":{"questionReflectionTimeoutMs":9999}}`, field: "agent.questionReflectionTimeoutMs"},
		{payload: `{"agent":{"questionReflectionTimeoutMs":3600001}}`, field: "agent.questionReflectionTimeoutMs"},
		{payload: `{"agent":{"autoCompactKeepPairs":26}}`, field: "agent.autoCompactKeepPairs"},
		{payload: `{"agent":{"contextThresholds":{"standard":{"pruneStart":49}}}}`, field: "agent.contextThresholds.standard.pruneStart"},
		{payload: `{"agent":{"contextThresholds":{"large":{"compactStart":9}}}}`, field: "agent.contextThresholds.large.compactStart"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchNormalizesDangerReflectionLevelCompatibility(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}

	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(`{"agent":{"dangerReflectionLevel":"off"}}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch dangerReflectionLevel off: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	agent, _ := body["agent"].(map[string]any)
	if agent["dangerReflectionLevel"] != "off" || agent["dangerReflectionEnabled"] != false {
		t.Fatalf("level off did not sync enabled false: %#v", agent)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(`{"agent":{"dangerReflectionEnabled":true}}`)))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch legacy dangerReflectionEnabled true: %v", err)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode second patch response: %v", err)
	}
	agent, _ = body["agent"].(map[string]any)
	if agent["dangerReflectionLevel"] != "standard" || agent["dangerReflectionEnabled"] != true {
		t.Fatalf("legacy enabled true did not sync standard level: %#v", agent)
	}
}

func TestPatchRejectsInvalidAgentPromptAndAggregations(t *testing.T) {
	longPrompt := strings.Repeat("x", 50001)
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"agent":{"defaultSystemPrompt":` + strconv.Quote(longPrompt) + `}}`, field: "agent.defaultSystemPrompt"},
		{payload: `{"agent":{"modelAggregations":"bad"}}`, field: "agent.modelAggregations"},
		{payload: `{"agent":{"modelAggregations":[{"id":"","name":"Agg","models":["openai:gpt"],"routingMode":"priority"}]}}`, field: "agent.modelAggregations[0].id"},
		{payload: `{"agent":{"modelAggregations":[{"id":"agg","name":"","models":["openai:gpt"],"routingMode":"priority"}]}}`, field: "agent.modelAggregations[0].name"},
		{payload: `{"agent":{"modelAggregations":[{"id":"agg","name":"Agg","models":[],"routingMode":"priority"}]}}`, field: "agent.modelAggregations[0].models"},
		{payload: `{"agent":{"modelAggregations":[{"id":"agg","name":"Agg","models":[1],"routingMode":"priority"}]}}`, field: "agent.modelAggregations[0].models[0]"},
		{payload: `{"agent":{"modelAggregations":[{"id":"agg","name":"Agg","models":["openai:gpt"],"routingMode":"random"}]}}`, field: "agent.modelAggregations[0].routingMode"},
		{payload: `{"agent":{"customModels":[{"value":"agg:model","label":"Agg","channelType":1}]}}`, field: "agent.customModels[0].channelType"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchRejectsInvalidAgentCustomRetryRules(t *testing.T) {
	longNote := strings.Repeat("x", 201)
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"agent":{"customRetryRules":"bad"}}`, field: "agent.customRetryRules"},
		{payload: `{"agent":{"customRetryRules":[{"domain":"api.example.test"}]}}`, field: "agent.customRetryRules[0].id"},
		{payload: `{"agent":{"customRetryRules":[{"id":"r1","statusCode":99}]}}`, field: "agent.customRetryRules[0].statusCode"},
		{payload: `{"agent":{"customRetryRules":[{"id":"r1","enabled":"yes"}]}}`, field: "agent.customRetryRules[0].enabled"},
		{payload: `{"agent":{"customRetryRules":[{"id":"r1","note":null}]}}`, field: "agent.customRetryRules[0].note"},
		{payload: `{"agent":{"customRetryRules":[{"id":"r1","note":` + strconv.Quote(longNote) + `}]}}`, field: "agent.customRetryRules[0].note"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchRejectsInvalidAgentModelSelectionSettings(t *testing.T) {
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"agent":{"customModels":[{"value":"","label":"Test"}]}}`, field: "agent.customModels[0].value"},
		{payload: `{"agent":{"hiddenModels":[1]}}`, field: "agent.hiddenModels[0]"},
		{payload: `{"agent":{"hiddenModels":null}}`, field: "agent.hiddenModels"},
		{payload: `{"agent":{"providerOrder":[1]}}`, field: "agent.providerOrder[0]"},
		{payload: `{"agent":{"disabledProviders":"openai"}}`, field: "agent.disabledProviders"},
		{payload: `{"agent":{"modelContextWindows":{"openai:gpt":0}}}`, field: "agent.modelContextWindows.openai:gpt"},
		{payload: `{"agent":{"subagentModels":{"explore":1}}}`, field: "agent.subagentModels.explore"},
		{payload: `{"agent":{"subagentAllowedModels":{"general":[1]}}}`, field: "agent.subagentAllowedModels.general[0]"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchRejectsInvalidAgentPolicySettings(t *testing.T) {
	longDenyPrompt := strings.Repeat("x", 2001)
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"agent":{"whitelistDirs":"/tmp"}}`, field: "agent.whitelistDirs"},
		{payload: `{"agent":{"whitelistDirs":[{"path":""}]}}`, field: "agent.whitelistDirs[0].path"},
		{payload: `{"agent":{"whitelistDirs":[{"path":"/tmp","accessLevel":"execute"}]}}`, field: "agent.whitelistDirs[0].accessLevel"},
		{payload: `{"agent":{"blacklistDirs":[{"path":"/tmp","denyLevel":"denyRead"}]}}`, field: "agent.blacklistDirs[0].denyLevel"},
		{payload: `{"agent":{"commandWhitelist":[{"pattern":""}]}}`, field: "agent.commandWhitelist[0].pattern"},
		{payload: `{"agent":{"commandBlacklist":[{"pattern":"rm","denyPrompt":` + strconv.Quote(longDenyPrompt) + `}]}}`, field: "agent.commandBlacklist[0].denyPrompt"},
		{payload: `{"agent":{"commandBlacklist":[{"pattern":"rm","enabled":"yes"}]}}`, field: "agent.commandBlacklist[0].enabled"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchRejectsInvalidAgentWebFetchPolicy(t *testing.T) {
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"agent":{"webFetchPolicy":"allow"}}`, field: "agent.webFetchPolicy"},
		{payload: `{"agent":{"webFetchPolicy":{"allowAll":"yes"}}}`, field: "agent.webFetchPolicy.allowAll"},
		{payload: `{"agent":{"webFetchPolicy":{"whitelist":[{"pattern":1}]}}}`, field: "agent.webFetchPolicy.whitelist[0].pattern"},
		{payload: `{"agent":{"webFetchPolicy":{"blacklist":[{"pattern":"*.internal","enabled":"yes"}]}}}`, field: "agent.webFetchPolicy.blacklist[0].enabled"},
		{payload: `{"agent":{"webFetchPolicy":{"proxy":{"mode":"random"}}}}`, field: "agent.webFetchPolicy.proxy.mode"},
		{payload: `{"agent":{"webFetchPolicy":{"proxy":{"mode":"custom","url":"ftp://proxy.example.test"}}}}`, field: "agent.webFetchPolicy.proxy.url"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchRejectsInvalidPathsChaptersContainersAndEditorSettings(t *testing.T) {
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"paths":{"defaultProjectDir":""}}`, field: "paths.defaultProjectDir"},
		{payload: `{"chapters":{"maxActiveWorktrees":0}}`, field: "chapters.maxActiveWorktrees"},
		{payload: `{"chapters":{"maxActiveContainers":-1}}`, field: "chapters.maxActiveContainers"},
		{payload: `{"chapters":{"autoSaveOnDormant":"yes"}}`, field: "chapters.autoSaveOnDormant"},
		{payload: `{"containers":{"proxy":null}}`, field: "containers.proxy"},
		{payload: `{"editor":{"type":"vim"}}`, field: "editor.type"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchRejectsInvalidCodexAndRoutineSettings(t *testing.T) {
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"codex":{"proxy":null}}`, field: "codex.proxy"},
		{payload: `{"codex":{"proxy":"ftp://proxy.example.test"}}`, field: "codex.proxy"},
		{payload: `{"codex":{"loadBalancingMode":"random"}}`, field: "codex.loadBalancingMode"},
		{payload: `{"codex":{"tierOrder":["pro","vip"]}}`, field: "codex.tierOrder[1]"},
		{payload: `{"codex":{"defaultReasoningEffort":"extreme"}}`, field: "codex.defaultReasoningEffort"},
		{payload: `{"routines":{"disabledRoutines":[1]}}`, field: "routines.disabledRoutines[0]"},
		{payload: `{"routines":{"disabledRoutines":null}}`, field: "routines.disabledRoutines"},
		{payload: `{"routines":{"enabledRoutines":"all"}}`, field: "routines.enabledRoutines"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchRejectsInvalidVNetSettings(t *testing.T) {
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"vnet":{"enabled":"yes"}}`, field: "vnet.enabled"},
		{payload: `{"vnet":{"maxPeersPerNetwork":0}}`, field: "vnet.maxPeersPerNetwork"},
		{payload: `{"vnet":{"maxMessageBytes":1023}}`, field: "vnet.maxMessageBytes"},
		{payload: `{"vnet":{"udp":{"host":""}}}`, field: "vnet.udp.host"},
		{payload: `{"vnet":{"udp":{"port":70000}}}`, field: "vnet.udp.port"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchRejectsInvalidUpdateAndShareSettings(t *testing.T) {
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"update":{"serverUrl":"not-a-url"}}`, field: "update.serverUrl"},
		{payload: `{"update":{"product":""}}`, field: "update.product"},
		{payload: `{"update":{"channel":"nightly"}}`, field: "update.channel"},
		{payload: `{"update":{"checkIntervalMinutes":-1}}`, field: "update.checkIntervalMinutes"},
		{payload: `{"update":{"autoDownload":"yes"}}`, field: "update.autoDownload"},
		{payload: `{"shares":{"defaultExpiryHours":0}}`, field: "shares.defaultExpiryHours"},
		{payload: `{"shares":{"maxFileSizeMb":"large"}}`, field: "shares.maxFileSizeMb"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchRejectsUnknownTopLevelSettingsFields(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(`{"unknownSection":{"enabled":true}}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := h.patch(w, req)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, "unsupported settings field") {
		t.Fatalf("unexpected validation error: %#v", appErr)
	}
	if w.Code != 0 && w.Code != http.StatusOK {
		t.Fatalf("patch should not have written a success response, got status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPatchStripsNestedUnknownSettingsFieldsLikeTypeScript(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	payload := []byte(`{
		"server":{"__unknownServer":true},
		"agent":{
			"__unknownAgent":"drop-me",
			"subagentModels":{"explore":"fixture-explore","general":"drop-me","__unknownSubagent":"drop-me"},
			"webFetchPolicy":{"allowAll":true,"__unknownPolicy":"drop-me"}
		},
		"customApiProviders":[{
			"id":"custom-strip","name":"Custom Strip","prefix":"strip","apiKey":"sk-strip",
			"baseUrl":"https://strip.example.test","defaultModel":"strip-model",
			"protocol":"responses-compatible","__unknownProvider":"drop-me"
		}]
	}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch settings returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, doc := range []struct {
		name string
		root map[string]any
	}{
		{name: "response", root: body},
		{name: "persisted", root: store.Raw()},
	} {
		for _, path := range []string{
			"server.__unknownServer",
			"agent.__unknownAgent",
			"agent.subagentModels.__unknownSubagent",
			"agent.subagentModels.general",
			"agent.webFetchPolicy.__unknownPolicy",
		} {
			if value, ok := bunFinalSettingsLookupPath(doc.root, path); ok {
				t.Fatalf("%s retained TS-stripped nested unknown path %s=%#v in %#v", doc.name, path, value, doc.root)
			}
		}
		providers := anySlice(doc.root["customApiProviders"])
		if len(providers) != 1 {
			t.Fatalf("%s customApiProviders length = %d, want 1", doc.name, len(providers))
		}
		provider, _ := providers[0].(map[string]any)
		if value, ok := provider["__unknownProvider"]; ok {
			t.Fatalf("%s retained TS-stripped provider unknown field: %#v in %#v", doc.name, value, provider)
		}
	}
}

func TestPatchRejectsInvalidProviderArrays(t *testing.T) {
	cases := []struct {
		payload string
		field   string
	}{
		{
			payload: `{"openaiProviders":null}`,
			field:   "openaiProviders",
		},
		{
			payload: `{"openaiProviders":[{"id":"p1","name":"OpenAI","prefix":"openai","baseUrl":"https://api.openai.test","defaultModel":"gpt-test"}]}`,
			field:   "apiKey",
		},
		{
			payload: `{"openaiProviders":[{"id":"p1","name":"OpenAI","prefix":"openai","apiKey":"sk","baseUrl":"https://api.openai.test","defaultModel":"gpt-test","apiMode":null}]}`,
			field:   "apiMode",
		},
		{
			payload: `{"customApiProviders":[{"id":"c1","name":"Custom","prefix":"custom","apiKey":"sk","baseUrl":"https://api.example.test","defaultModel":"model","protocol":"chat-compatible"}]}`,
			field:   "protocol",
		},
		{
			payload: `{"customApiProviders":[{"id":"c1","name":"Custom","prefix":"custom","apiKey":"sk","baseUrl":"https://api.example.test","defaultModel":"model"}]}`,
			field:   "protocol is required",
		},
		{
			payload: `{"customApiProviders":[{"id":"c1","name":"Custom","prefix":"custom","apiKey":"sk","baseUrl":"https://api.example.test","defaultModel":"model","protocol":"responses-compatible","proxy":null}]}`,
			field:   "proxy",
		},
		{
			payload: `{"anthropicProviders":[{"id":"a1","name":"Anthropic","prefix":"anthropic","apiKey":"sk","baseUrl":"https://api.anthropic.test","defaultModel":"claude","officialApi":"yes"}]}`,
			field:   "officialApi",
		},
		{
			payload: `{"clineProviders":[{"id":"cl1","name":"Cline","prefix":"cline","baseUrl":"https://api.cline.test","defaultModel":"model","defaultContextWindow":0}]}`,
			field:   "defaultContextWindow",
		},
		{
			payload: `{"clineProviders":[{"id":"cl1","name":"Cline","prefix":"cline","baseUrl":"https://api.cline.test","defaultModel":"model","defaultContextWindow":null}]}`,
			field:   "defaultContextWindow",
		},
		{
			payload: `{"clineProviders":[{"id":"cl1","name":"Cline","prefix":"cline","baseUrl":"https://api.cline.test","defaultModel":"model","enabledModels":[1]}]}`,
			field:   "enabledModels[0]",
		},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchNormalizesProxyURLs(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	payload := []byte(`{"codex":{"proxy":"codex-proxy.example.test:8080"},"agent":{"webFetchPolicy":{"proxy":{"mode":"custom","url":"web-proxy.example.test:8081"}}},"customApiProviders":[{"id":"a1","name":"Anth","prefix":"anth-proxy","apiKey":"sk","baseUrl":"https://anth.example.test","defaultModel":"claude-test","protocol":"anthropic-compatible","proxy":"provider-proxy.example.test:8082"}]}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch proxy settings returned error: %v", err)
	}
	raw := store.Raw()
	codexSettings, _ := raw["codex"].(map[string]any)
	if codexSettings["proxy"] != "http://codex-proxy.example.test:8080" {
		t.Fatalf("codex.proxy was not normalized: %#v", codexSettings["proxy"])
	}
	webFetchProxy, _ := nestedMap(raw, "agent", "webFetchPolicy", "proxy")
	if webFetchProxy["url"] != "http://web-proxy.example.test:8081" {
		t.Fatalf("webFetch proxy URL was not normalized: %#v", webFetchProxy["url"])
	}
	customProviders := anySlice(raw["customApiProviders"])
	customProvider, _ := customProviders[0].(map[string]any)
	if customProvider["proxy"] != "http://provider-proxy.example.test:8082" {
		t.Fatalf("custom provider proxy was not normalized: %#v", customProvider["proxy"])
	}
	anthropicProviders := anySlice(raw["anthropicProviders"])
	anthropicProvider, _ := anthropicProviders[0].(map[string]any)
	if anthropicProvider["proxy"] != "http://provider-proxy.example.test:8082" {
		t.Fatalf("derived anthropic provider proxy was not normalized: %#v", anthropicProvider["proxy"])
	}
}

func TestPatchRejectsProviderPrefixConflicts(t *testing.T) {
	cases := []struct {
		payload string
		field   string
	}{
		{
			payload: `{"customApiProviders":[{"id":"c1","name":"Custom","prefix":"kiro","apiKey":"sk","baseUrl":"https://api.example.test","defaultModel":"model","protocol":"responses-compatible"}]}`,
			field:   "Provider prefix \"kiro\" is reserved",
		},
		{
			payload: `{"openaiProviders":[{"id":"o1","name":"OpenAI","prefix":"codex","apiKey":"sk","baseUrl":"https://api.openai.test","defaultModel":"model"}]}`,
			field:   "Provider prefix \"codex\" is reserved",
		},
		{
			payload: `{"customApiProviders":[{"id":"c1","name":"Custom","prefix":"dup","apiKey":"sk","baseUrl":"https://api.example.test","defaultModel":"model","protocol":"responses-compatible"}],"nkpProviders":[{"id":"n1","name":"NKP","prefix":"dup","apiKey":"sk","baseUrl":"https://api.nkp.test","defaultModel":"model"}]}`,
			field:   "Duplicate provider prefix \"dup\"",
		},
		{
			payload: `{"openaiProviders":[{"id":"o1","name":"OpenAI","prefix":"legacydup","apiKey":"sk","baseUrl":"https://api.openai.test","defaultModel":"model"}],"nkpProviders":[{"id":"n1","name":"NKP","prefix":"legacydup","apiKey":"sk","baseUrl":"https://api.nkp.test","defaultModel":"model"}]}`,
			field:   "Duplicate provider prefix \"legacydup\"",
		},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchProviderArraysAndPrefixBreadthLikeTypeScript(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	payload := []byte(`{
		"customApiProviders":[
			{
				"id":"wave269-open","name":"Wave269 Open","prefix":"wave269open",
				"apiKey":"sk-open","baseUrl":"https://open.wave269.test","defaultModel":"gpt-wave269",
				"protocol":"responses-compatible","defaultContextWindow":32000,
				"codexAccountId":"acct-wave269","codexWebSocket":true,
				"codexWebSearch":false,"codexImageGeneration":false,"disabled":true,
				"__unknownProvider":"drop-me"
			},
			{
				"id":"wave269-anth","name":"Wave269 Anth","prefix":"wave269anth",
				"apiKey":"sk-anth","baseUrl":"https://anth.wave269.test","defaultModel":"claude-wave269",
				"protocol":"anthropic-official","defaultReasoningEffort":"high",
				"proxy":"http://proxy.wave269.test","tlsRejectUnauthorized":false
			}
		],
		"nkpProviders":[{
			"id":"wave269-nkp","name":"Wave269 NKP","prefix":"wave269nkp",
			"apiKey":"nkp-key","baseUrl":"https://nkp.wave269.test","defaultModel":"nkp-wave269",
			"disabled":true,"__unknownProvider":"drop-me"
		}],
		"nugProviders":[{
			"id":"wave269-nug","name":"Wave269 NUG","prefix":"wave269nug",
			"apiKey":"nug-key","baseUrl":"https://nug.wave269.test","defaultModel":"nug-wave269",
			"nugUsername":"wave-user","nugUserId":"wave-user-id","oauthClientId":"oauth-id",
			"oauthClientSecret":"oauth-secret","oauthDeviceId":"device-id","oauthCallbackUrl":"https://callback.wave269.test",
			"disabled":true,"__unknownProvider":"drop-me"
		}],
		"clineProviders":[{
			"id":"wave269-cline","name":"Wave269 Cline","prefix":"wave269cline",
			"baseUrl":"https://cline.wave269.test","accessToken":"cline-token","defaultModel":"cline-wave269",
			"defaultContextWindow":64000,"enabledModels":["cline/free","cline/pro"],
			"disabled":true,"__unknownProvider":"drop-me"
		}]
	}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch provider array breadth returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	raw := store.Raw()
	customProviders := anySlice(raw["customApiProviders"])
	if len(customProviders) != 2 {
		t.Fatalf("expected two custom providers, got %#v", customProviders)
	}
	customOpen, _ := customProviders[0].(map[string]any)
	if customOpen["__unknownProvider"] != nil || customOpen["protocol"] != "responses-compatible" || customOpen["disabled"] != true {
		t.Fatalf("unexpected custom OpenAI provider shape: %#v", customOpen)
	}
	openaiProviders := anySlice(raw["openaiProviders"])
	if len(openaiProviders) != 1 {
		t.Fatalf("expected one derived OpenAI provider, got %#v", openaiProviders)
	}
	openai, _ := openaiProviders[0].(map[string]any)
	if openai["id"] != "wave269-open" || openai["apiMode"] != "responses" || openai["disabled"] != true || openai["codexWebSearch"] != false || intSetting(openai["defaultContextWindow"], 0) != 32000 {
		t.Fatalf("unexpected derived OpenAI provider: %#v", openai)
	}
	anthropicProviders := anySlice(raw["anthropicProviders"])
	if len(anthropicProviders) != 1 {
		t.Fatalf("expected one derived Anthropic provider, got %#v", anthropicProviders)
	}
	anthropic, _ := anthropicProviders[0].(map[string]any)
	if anthropic["id"] != "wave269-anth" || anthropic["officialApi"] != true || anthropic["defaultReasoningEffort"] != "high" || anthropic["tlsRejectUnauthorized"] != false {
		t.Fatalf("unexpected derived Anthropic provider: %#v", anthropic)
	}
	for _, tc := range []struct {
		field string
		id    string
	}{
		{field: "nkpProviders", id: "wave269-nkp"},
		{field: "nugProviders", id: "wave269-nug"},
		{field: "clineProviders", id: "wave269-cline"},
	} {
		items := anySlice(raw[tc.field])
		if len(items) != 1 {
			t.Fatalf("expected one %s item, got %#v", tc.field, items)
		}
		provider, _ := items[0].(map[string]any)
		if provider["id"] != tc.id || provider["__unknownProvider"] != nil || provider["disabled"] != true {
			t.Fatalf("unexpected %s provider shape: %#v", tc.field, provider)
		}
	}

	conflictCases := []struct {
		payload string
		field   string
	}{
		{
			payload: `{"nkpProviders":[{"id":"n1","name":"NKP","prefix":"codex","apiKey":"sk","baseUrl":"https://api.nkp.test","defaultModel":"model"}]}`,
			field:   `Provider prefix "codex" is reserved`,
		},
		{
			payload: `{"nugProviders":[{"id":"n1","name":"NUG","prefix":"dup269","apiKey":"sk","baseUrl":"https://api.nug.test","defaultModel":"model"}],"clineProviders":[{"id":"c1","name":"Cline","prefix":"dup269","baseUrl":"https://api.cline.test","defaultModel":"model"}]}`,
			field:   `Duplicate provider prefix "dup269"`,
		},
		{
			payload: `{"customApiProviders":[{"id":"c1","name":"Custom","prefix":"dup-custom-269","apiKey":"sk","baseUrl":"https://api.example.test","defaultModel":"model","protocol":"responses-compatible"}],"clineProviders":[{"id":"cl1","name":"Cline","prefix":"dup-custom-269","baseUrl":"https://api.cline.test","defaultModel":"model"}]}`,
			field:   `Duplicate provider prefix "dup-custom-269"`,
		},
	}
	for _, tc := range conflictCases {
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		err := h.patch(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected provider prefix validation error for %s: %#v", tc.payload, appErr)
		}
	}
}

func TestPatchCustomApiProvidersSyncsLegacyProviderArrays(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	payload := []byte(`{"customApiProviders":[{"id":"o1","name":"Open","prefix":"goai","apiKey":"sk-open","baseUrl":"https://open.example.test","defaultModel":"gpt-test","protocol":"responses-compatible","defaultContextWindow":128000,"codexAccountId":"acct_1","codexWebSocket":true},{"id":"a1","name":"Anth","prefix":"ganth","apiKey":"sk-anth","baseUrl":"https://anth.example.test","defaultModel":"claude-test","protocol":"anthropic-official","defaultReasoningEffort":"high","proxy":"http://proxy.example.test","tlsRejectUnauthorized":false}]}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch customApiProviders returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	raw := store.Raw()
	openaiProviders := anySlice(raw["openaiProviders"])
	if len(openaiProviders) != 1 {
		t.Fatalf("expected one derived OpenAI provider, got %#v", openaiProviders)
	}
	openai, _ := openaiProviders[0].(map[string]any)
	if openai["id"] != "o1" || openai["apiMode"] != "responses" || openai["codexAccountId"] != "acct_1" || openai["codexWebSocket"] != true || intSetting(openai["defaultContextWindow"], 0) != 128000 {
		t.Fatalf("unexpected derived OpenAI provider: %#v", openai)
	}
	anthropicProviders := anySlice(raw["anthropicProviders"])
	if len(anthropicProviders) != 1 {
		t.Fatalf("expected one derived Anthropic provider, got %#v", anthropicProviders)
	}
	anthropic, _ := anthropicProviders[0].(map[string]any)
	if anthropic["id"] != "a1" || anthropic["officialApi"] != true || anthropic["defaultReasoningEffort"] != "high" || anthropic["proxy"] != "http://proxy.example.test" || anthropic["tlsRejectUnauthorized"] != false {
		t.Fatalf("unexpected derived Anthropic provider: %#v", anthropic)
	}
}

func TestPatchLegacyProviderArraysSyncsCustomApiProviders(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	payload := []byte(`{"openaiProviders":[{"id":"o1","name":"Open","prefix":"legacy-open","apiKey":"sk-open","baseUrl":"https://open.example.test","defaultModel":"gpt-test","responsesApi":false,"codexAccountId":"acct_1","codexWebSocket":true}],"anthropicProviders":[{"id":"a1","name":"Anth","prefix":"legacy-anth","apiKey":"sk-anth","baseUrl":"https://anth.example.test","defaultModel":"claude-test","officialApi":true,"defaultReasoningEffort":"high","proxy":"http://proxy.example.test","tlsRejectUnauthorized":false}]}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch legacy provider arrays returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	customProviders := anySlice(store.Raw()["customApiProviders"])
	if len(customProviders) != 2 {
		t.Fatalf("expected two derived custom providers, got %#v", customProviders)
	}
	openai, _ := customProviders[0].(map[string]any)
	if openai["id"] != "o1" || openai["protocol"] != "completions-compatible" || openai["codexAccountId"] != "acct_1" || openai["codexWebSocket"] != true {
		t.Fatalf("unexpected custom provider derived from OpenAI: %#v", openai)
	}
	anthropic, _ := customProviders[1].(map[string]any)
	if anthropic["id"] != "a1" || anthropic["protocol"] != "anthropic-official" || anthropic["defaultReasoningEffort"] != "high" || anthropic["proxy"] != "http://proxy.example.test" || anthropic["tlsRejectUnauthorized"] != false {
		t.Fatalf("unexpected custom provider derived from Anthropic: %#v", anthropic)
	}
}

func TestGetSettingsDerivesCustomApiProvidersFromLegacyArrays(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.Patch(map[string]any{
		"openaiProviders":    []any{map[string]any{"id": "o1", "name": "Open", "prefix": "legacy-open", "apiKey": "sk-open", "baseUrl": "https://open.example.test", "defaultModel": "gpt-test", "responsesApi": false}},
		"anthropicProviders": []any{map[string]any{"id": "a1", "name": "Anth", "prefix": "legacy-anth", "apiKey": "sk-anth", "baseUrl": "https://anth.example.test", "defaultModel": "claude-test", "officialApi": true}},
	}); err != nil {
		t.Fatalf("seed legacy provider arrays: %v", err)
	}
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()

	if err := h.get(w, req); err != nil {
		t.Fatalf("get settings returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	customProviders := anySlice(body["customApiProviders"])
	if len(customProviders) != 2 {
		t.Fatalf("expected two derived custom providers in response, got %#v", customProviders)
	}
	openai, _ := customProviders[0].(map[string]any)
	if openai["id"] != "o1" || openai["protocol"] != "completions-compatible" {
		t.Fatalf("unexpected custom provider derived from legacy OpenAI response: %#v", openai)
	}
	anthropic, _ := customProviders[1].(map[string]any)
	if anthropic["id"] != "a1" || anthropic["protocol"] != "anthropic-official" {
		t.Fatalf("unexpected custom provider derived from legacy Anthropic response: %#v", anthropic)
	}
}

func TestPatchAllowsNullableProviderDefaultReasoningEffort(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(`{"customApiProviders":[{"id":"c1","name":"Custom","prefix":"custom","apiKey":"sk","baseUrl":"https://api.example.test","defaultModel":"model","protocol":"responses-compatible","defaultReasoningEffort":null}]}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.patch(w, req); err != nil {
		t.Fatalf("expected nullable provider defaultReasoningEffort to be accepted: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestAddRetryRulePersistsUnderAgentCustomRetryRules(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/retry-rules", bytes.NewReader([]byte(`{"domain":"api.example.test","enabled":false,"extra":"ignored"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.addRetryRule(w, req); err != nil {
		t.Fatalf("addRetryRule returned error: %v", err)
	}
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode created retry rule: %v", err)
	}
	if response["domain"] != "api.example.test" || response["enabled"] != true || response["extra"] != nil {
		t.Fatalf("unexpected created retry rule response: %#v", response)
	}
	raw := store.Raw()
	if _, ok := raw["retryRules"]; ok {
		t.Fatalf("retry rules must not be persisted at top level: %#v", raw["retryRules"])
	}
	agent, _ := raw["agent"].(map[string]any)
	rules, _ := agent["customRetryRules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("expected one agent.customRetryRules entry, got %#v", agent["customRetryRules"])
	}
	rule, _ := rules[0].(map[string]any)
	if rule["domain"] != "api.example.test" || rule["enabled"] != true || rule["extra"] != nil || strings.TrimSpace(stringValue(rule["id"])) == "" {
		t.Fatalf("unexpected retry rule: %#v", rule)
	}
}

func TestAddRetryRuleRejectsInvalidRule(t *testing.T) {
	cases := []struct {
		payload string
		field   string
	}{
		{payload: `{"statusCode":99}`, field: "agent.customRetryRules[0].statusCode"},
		{payload: `{"domain":""}`, field: "agent.customRetryRules[0].domain"},
		{payload: `{"keyword":null}`, field: "agent.customRetryRules[0].keyword"},
	}
	for _, tc := range cases {
		store := newTestStore(t)
		h := &Handler{Store: store}
		req := httptest.NewRequest(http.MethodPost, "/api/settings/retry-rules", bytes.NewReader([]byte(tc.payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		err := h.addRetryRule(w, req)
		var appErr *httpx.AppError
		if !errors.As(err, &appErr) {
			t.Fatalf("expected app error for %s, got %T %v", tc.payload, err, err)
		}
		if appErr.Code != "VALIDATION_ERROR" || !strings.Contains(appErr.Message, tc.field) {
			t.Fatalf("unexpected validation error for %s: %#v", tc.payload, appErr)
		}
		agent, _ := store.Raw()["agent"].(map[string]any)
		if rules, _ := agent["customRetryRules"].([]any); len(rules) != 0 {
			t.Fatalf("invalid retry rule should not be persisted: %#v", rules)
		}
	}
}

func TestPatchRejectsAuthJWTSecret(t *testing.T) {
	store := newTestStore(t)
	authSettings, _ := store.Raw()["auth"].(map[string]any)
	originalSecret := stringValue(authSettings["jwtSecret"])
	h := &Handler{Store: store}
	payload := []byte(`{"auth":{"jwtSecret":"attacker-controlled-secret"}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := h.patch(w, req)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Code != "VALIDATION_ERROR" || appErr.Status != http.StatusBadRequest {
		t.Fatalf("unexpected error: %#v", appErr)
	}
	authSettings, _ = store.Raw()["auth"].(map[string]any)
	if stringValue(authSettings["jwtSecret"]) != originalSecret {
		t.Fatalf("jwtSecret changed despite rejected patch")
	}
}

func TestGetSettingsRedactsAuthJWTSecret(t *testing.T) {
	store := newTestStore(t)
	authSettings, _ := store.Raw()["auth"].(map[string]any)
	originalSecret := stringValue(authSettings["jwtSecret"])
	if originalSecret == "" {
		t.Fatalf("expected generated jwt secret in store")
	}
	h := &Handler{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()

	if err := h.get(w, req); err != nil {
		t.Fatalf("get returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	responseAuth, _ := body["auth"].(map[string]any)
	if responseAuth["jwtSecret"] != nil || responseAuth["jwtSecret"] == originalSecret {
		t.Fatalf("jwtSecret leaked in settings response: %#v", responseAuth["jwtSecret"])
	}
}

func TestGetSettingsRedactsTLSPassphraseAndPreservesMaskedOrEmptyPatch(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	secret := "tls-passphrase-secret"
	initialPatch := []byte(`{"server":{"tls":{"enabled":false,"certFile":"cert.pem","keyFile":"key.pem","passphrase":"tls-passphrase-secret"}}}`)
	initialReq := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(initialPatch))
	initialReq.Header.Set("Content-Type", "application/json")
	initialW := httptest.NewRecorder()
	if err := h.patch(initialW, initialReq); err != nil {
		t.Fatalf("initial TLS patch returned error: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	getW := httptest.NewRecorder()
	if err := h.get(getW, getReq); err != nil {
		t.Fatalf("get settings returned error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(getW.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	responseTLS, _ := nestedMap(body, "server", "tls")
	if responseTLS["passphrase"] != "********" {
		t.Fatalf("TLS passphrase leaked in settings response: %#v", responseTLS["passphrase"])
	}

	for _, payload := range []string{
		`{"server":{"tls":{"passphrase":"********"}}}`,
		`{"server":{"tls":{"passphrase":""}}}`,
	} {
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(payload)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if err := h.patch(w, req); err != nil {
			t.Fatalf("masked/empty TLS patch returned error for %s: %v", payload, err)
		}
		tlsSettings, _ := nestedMap(store.Raw(), "server", "tls")
		if stringValue(tlsSettings["passphrase"]) != secret {
			t.Fatalf("masked/empty TLS patch should preserve original passphrase, got %#v", tlsSettings["passphrase"])
		}
	}
}

func TestGetSettingsRedactsVNetRelayTokenAndPreservesMaskedPatch(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	secret := "relay-secret-123456"
	payload := []byte(`{"vnet":{"relayToken":"relay-secret-123456"}}`)
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	patchReq.Header.Set("Content-Type", "application/json")
	patchW := httptest.NewRecorder()

	if err := h.patch(patchW, patchReq); err != nil {
		t.Fatalf("patch relay token returned error: %v", err)
	}
	if patchW.Code != http.StatusOK {
		t.Fatalf("expected patch 200, got %d body=%s", patchW.Code, patchW.Body.String())
	}
	vnetSettings, _ := store.Raw()["vnet"].(map[string]any)
	if stringValue(vnetSettings["relayToken"]) != secret {
		t.Fatalf("relayToken was not persisted: %#v", vnetSettings["relayToken"])
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	getW := httptest.NewRecorder()
	if err := h.get(getW, getReq); err != nil {
		t.Fatalf("get returned error: %v", err)
	}
	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", getW.Code, getW.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(getW.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	responseVNet, _ := body["vnet"].(map[string]any)
	maskedToken := stringValue(responseVNet["relayToken"])
	if maskedToken == "" || maskedToken == secret || !strings.HasPrefix(maskedToken, "********") {
		t.Fatalf("relayToken leaked in settings response: %#v", responseVNet["relayToken"])
	}

	maskedPatch := []byte(`{"vnet":{"relayToken":"********1234"}}`)
	maskedReq := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(maskedPatch))
	maskedReq.Header.Set("Content-Type", "application/json")
	maskedW := httptest.NewRecorder()
	if err := h.patch(maskedW, maskedReq); err != nil {
		t.Fatalf("masked patch returned error: %v", err)
	}
	if maskedW.Code != http.StatusOK {
		t.Fatalf("expected masked patch 200, got %d body=%s", maskedW.Code, maskedW.Body.String())
	}
	vnetSettings, _ = store.Raw()["vnet"].(map[string]any)
	if stringValue(vnetSettings["relayToken"]) != secret {
		t.Fatalf("masked patch should preserve original relayToken, got %#v", vnetSettings["relayToken"])
	}
}

func TestGetSettingsRedactsProviderSecretsAndPreservesMaskedPatch(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	initialPatch := []byte(`{
		"openaiProviders":[{"id":"openai-1","name":"OpenAI","prefix":"openai","apiKey":"sk-openai-secret","baseUrl":"https://openai.example.test","defaultModel":"gpt-test"}],
		"nugProviders":[{"id":"nug-1","name":"NUG","prefix":"nug","apiKey":"sk-nug-secret","oauthClientSecret":"nug-oauth-secret","baseUrl":"https://nug.example.test","defaultModel":"nug-test"}],
		"clineProviders":[{"id":"cline-1","name":"Cline","prefix":"cline","accessToken":"cline-access-secret","baseUrl":"https://cline.example.test","defaultModel":"cline-test"}]
	}`)
	initialReq := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(initialPatch))
	initialReq.Header.Set("Content-Type", "application/json")
	initialW := httptest.NewRecorder()
	if err := h.patch(initialW, initialReq); err != nil {
		t.Fatalf("initial provider patch returned error: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	getW := httptest.NewRecorder()
	if err := h.get(getW, getReq); err != nil {
		t.Fatalf("get settings returned error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(getW.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	openai := anySlice(body["openaiProviders"])[0].(map[string]any)
	nug := anySlice(body["nugProviders"])[0].(map[string]any)
	cline := anySlice(body["clineProviders"])[0].(map[string]any)
	if stringValue(openai["apiKey"]) == "sk-openai-secret" || !strings.HasPrefix(stringValue(openai["apiKey"]), "********") {
		t.Fatalf("openai apiKey leaked in response: %#v", openai["apiKey"])
	}
	if stringValue(nug["apiKey"]) == "sk-nug-secret" || stringValue(nug["oauthClientSecret"]) == "nug-oauth-secret" {
		t.Fatalf("nug secrets leaked in response: %#v", nug)
	}
	if stringValue(cline["accessToken"]) == "cline-access-secret" || !strings.HasPrefix(stringValue(cline["accessToken"]), "********") {
		t.Fatalf("cline accessToken leaked in response: %#v", cline["accessToken"])
	}

	maskedPatch := []byte(`{
		"openaiProviders":[{"id":"openai-1","name":"OpenAI","prefix":"openai","apiKey":"********cret","baseUrl":"https://openai.example.test","defaultModel":"gpt-test"}],
		"nugProviders":[{"id":"nug-1","name":"NUG","prefix":"nug","apiKey":"********cret","oauthClientSecret":"********cret","baseUrl":"https://nug.example.test","defaultModel":"nug-test"}],
		"clineProviders":[{"id":"cline-1","name":"Cline","prefix":"cline","accessToken":"********cret","baseUrl":"https://cline.example.test","defaultModel":"cline-test"}]
	}`)
	maskedReq := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(maskedPatch))
	maskedReq.Header.Set("Content-Type", "application/json")
	maskedW := httptest.NewRecorder()
	if err := h.patch(maskedW, maskedReq); err != nil {
		t.Fatalf("masked provider patch returned error: %v", err)
	}
	stored := store.Raw()
	storedOpenAI := anySlice(stored["openaiProviders"])[0].(map[string]any)
	storedNUG := anySlice(stored["nugProviders"])[0].(map[string]any)
	storedCline := anySlice(stored["clineProviders"])[0].(map[string]any)
	if stringValue(storedOpenAI["apiKey"]) != "sk-openai-secret" {
		t.Fatalf("masked openai patch should preserve original apiKey, got %#v", storedOpenAI["apiKey"])
	}
	if stringValue(storedNUG["apiKey"]) != "sk-nug-secret" || stringValue(storedNUG["oauthClientSecret"]) != "nug-oauth-secret" {
		t.Fatalf("masked nug patch should preserve original secrets, got %#v", storedNUG)
	}
	if stringValue(storedCline["accessToken"]) != "cline-access-secret" {
		t.Fatalf("masked cline patch should preserve original accessToken, got %#v", storedCline["accessToken"])
	}
}

type fakeContainerProxyRuntime struct {
	calls []struct {
		enabled bool
		port    int
	}
	err error
}

func (f *fakeContainerProxyRuntime) EnsureContainerProxy(ctx context.Context, enabled bool, port int) error {
	f.calls = append(f.calls, struct {
		enabled bool
		port    int
	}{enabled: enabled, port: port})
	return f.err
}

func TestPatchUpdatesContainerProxyRuntime(t *testing.T) {
	store := newTestStore(t)
	fake := &fakeContainerProxyRuntime{}
	h := &Handler{Store: store, ProxyRuntime: fake}
	payload := []byte(`{"containers":{"proxy":{"enabled":true,"port":7781}}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch returned error: %v", err)
	}
	if len(fake.calls) != 1 || !fake.calls[0].enabled || fake.calls[0].port != 7781 {
		t.Fatalf("proxy runtime calls = %#v", fake.calls)
	}
}

func TestPatchAllowsAuthRegistrationOpen(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	payload := []byte(`{"auth":{"registrationOpen":false}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if store.Core().Auth.RegistrationOpen {
		t.Fatalf("registrationOpen was not updated")
	}
}

func TestPatchServerChangeWithoutRestartManagerRequiresManualRestart(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	payload := []byte(`{"server":{"host":"0.0.0.0"}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["serverRestarting"] != false || body["manualRestartRequired"] != true || body["newUrl"] != "http://localhost:7778" {
		t.Fatalf("expected manual restart response, got %#v", body)
	}
}

func TestGenerateTLSCreatesCertificateAndEnablesSettings(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
	h.Restart = restart.NewManager(restart.RuntimeAddress{Protocol: "http", Host: "127.0.0.1", Port: 7778}, nil)
	originalStartReplacement := settingsStartReplacement
	settingsStartReplacement = func(context.Context, string, []string, []string) (int, error) {
		return 4321, nil
	}
	defer func() { settingsStartReplacement = originalStartReplacement }()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/generate-tls", nil)
	w := httptest.NewRecorder()

	if err := h.generateTLS(w, req); err != nil {
		t.Fatalf("generateTLS returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	certPath, _ := body["certPath"].(string)
	keyPath, _ := body["keyPath"].(string)
	newURL, _ := body["newUrl"].(string)
	expiresAt, _ := body["expiresAt"].(string)
	if certPath == "" || keyPath == "" || body["serverRestarting"] != true || body["manualRestartRequired"] != false {
		t.Fatalf("unexpected TLS response: %#v", body)
	}
	if !strings.HasPrefix(newURL, "https://") || !strings.Contains(newURL, ":7778") {
		t.Fatalf("expected TLS newUrl for frontend redirect, got %#v", newURL)
	}
	if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
		t.Fatalf("expected RFC3339 expiresAt, got %#v: %v", expiresAt, err)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("stat key: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatalf("expected PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	if len(cert.DNSNames) == 0 || cert.DNSNames[0] != "localhost" {
		t.Fatalf("unexpected DNS names: %#v", cert.DNSNames)
	}
	tlsSettings, _ := nestedMap(store.Raw(), "server", "tls")
	if tlsSettings["enabled"] != true || tlsSettings["certFile"] != certPath || tlsSettings["keyFile"] != keyPath {
		t.Fatalf("TLS settings not persisted: %#v", tlsSettings)
	}
	if body["replacementPid"] != float64(4321) {
		t.Fatalf("expected replacementPid from restart handoff, got %#v", body)
	}
	if body["newUrl"] != "https://localhost:7778" {
		t.Fatalf("expected newUrl to point at the new server address, got %#v", body["newUrl"])
	}
	handoffURL, _ := body["handoffUrl"].(string)
	if !strings.Contains(handoffURL, "/api/gracefully_shutdown") {
		t.Fatalf("expected handoffUrl to contain graceful shutdown endpoint, got %#v", body["handoffUrl"])
	}
}

func TestPatchProviderRemovalPurgesCachesAndAgentRefs(t *testing.T) {
	store := newTestStore(t)
	oldProvider := map[string]any{"id": "old", "name": "Old", "prefix": "oldp", "apiKey": "sk-old", "baseUrl": "https://old.example.test", "defaultModel": "old-model", "protocol": "responses-compatible"}
	keepProvider := map[string]any{"id": "keep", "name": "Keep", "prefix": "keepp", "apiKey": "sk-keep", "baseUrl": "https://keep.example.test", "defaultModel": "keep-model", "protocol": "responses-compatible"}
	_, err := store.Patch(map[string]any{
		"customApiProviders": []any{oldProvider, keepProvider},
		"agent": map[string]any{
			"summaryModel":        "oldp:model-a",
			"hiddenModels":        []any{"oldp:model-a", "keepp:model-b"},
			"modelContextWindows": map[string]any{"oldp:model-a": float64(1), "keepp:model-b": float64(2)},
			"customModels":        []any{map[string]any{"value": "oldp:model-a"}, map[string]any{"value": "keepp:model-b"}},
			"subagentModels":      map[string]any{"explore": "oldp:model-a", "plan": "keepp:model-b"},
		},
	})
	if err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	cachePath := filepath.Join(dataDir(), "openai-models-providers.json")
	if err := os.WriteFile(cachePath, []byte(`{"old":[{"id":"old-model"}],"keep":[{"id":"keep-model"}]}`), 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	h := &Handler{Store: store}
	payload, _ := json.Marshal(map[string]any{"customApiProviders": []any{keepProvider}})
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	agent, _ := store.Raw()["agent"].(map[string]any)
	if agent["summaryModel"] != "" {
		t.Fatalf("stale summaryModel was not cleared: %#v", agent["summaryModel"])
	}
	if hidden, _ := agent["hiddenModels"].([]any); len(hidden) != 1 || hidden[0] != "keepp:model-b" {
		t.Fatalf("hiddenModels not purged: %#v", hidden)
	}
	windows, _ := agent["modelContextWindows"].(map[string]any)
	if _, ok := windows["oldp:model-a"]; ok || windows["keepp:model-b"] == nil {
		t.Fatalf("modelContextWindows not purged: %#v", windows)
	}
	cache := loadProviderModelCache("openai")
	if _, ok := cache["old"]; ok || len(cache["keep"]) == 0 {
		t.Fatalf("provider cache not purged correctly: %#v", cache)
	}
}
