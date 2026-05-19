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
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/compat/bunfinal"
	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
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

func TestGenerateTLSCreatesCertificateAndEnablesSettings(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}
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
	if certPath == "" || keyPath == "" || body["serverRestarting"] != true {
		t.Fatalf("unexpected TLS response: %#v", body)
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
}

func TestPatchProviderRemovalPurgesCachesAndAgentRefs(t *testing.T) {
	store := newTestStore(t)
	oldProvider := map[string]any{"id": "old", "name": "Old", "prefix": "oldp", "protocol": "openai"}
	keepProvider := map[string]any{"id": "keep", "name": "Keep", "prefix": "keepp", "protocol": "openai"}
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
