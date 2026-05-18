package settings

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"narrafork-go/internal/config"
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
