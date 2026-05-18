package mcpcompat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/config"
)

func newTestHandler(t *testing.T) (*Handler, *config.Store) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return NewHandler(nil, nil, nil, store), store
}

func serveMCP(handler *Handler, method string, path string, body any) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	handler.RegisterRoutes(r)
	var payload bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&payload).Encode(body)
	}
	req := httptest.NewRequest(method, path, &payload)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
	}
	return out
}

func TestListServersReadsSettingsWithoutDatabase(t *testing.T) {
	handler, store := newTestHandler(t)
	if err := store.SetMCPServers([]map[string]any{{
		"id":        "srv_test",
		"name":      "filesystem",
		"transport": "stdio",
		"command":   "node",
		"args":      []any{"server.js"},
		"enabled":   true,
	}}); err != nil {
		t.Fatalf("seed mcpServers: %v", err)
	}

	w := serveMCP(handler, http.MethodGet, "/servers", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /servers status = %d, body=%s", w.Code, w.Body.String())
	}
	payload := decodeResponse(t, w)
	servers, ok := payload["servers"].([]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("servers = %#v, want one server", payload["servers"])
	}
	server := servers[0].(map[string]any)
	if server["id"] != "srv_test" || server["name"] != "filesystem" {
		t.Fatalf("server = %#v", server)
	}
	if server["status"] != "disconnected" {
		t.Fatalf("status = %#v, want disconnected", server["status"])
	}
}

func TestCreateUpdateDeleteServerPersistsSettings(t *testing.T) {
	handler, store := newTestHandler(t)

	createBody := map[string]any{
		"name":            "local-tools",
		"transport":       "stdio",
		"command":         "bunx",
		"args":            []string{"@example/mcp"},
		"defaultBehavior": "allow",
		"toolPermissions": []any{map[string]any{"toolName": "read_file", "behavior": "ask"}},
	}
	w := serveMCP(handler, http.MethodPost, "/servers", createBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /servers status = %d, body=%s", w.Code, w.Body.String())
	}
	created := decodeResponse(t, w)
	serverID, _ := created["id"].(string)
	if len(serverID) != 8 {
		t.Fatalf("created id = %q, want 8-char settings ID", serverID)
	}
	if created["defaultBehavior"] != "readWrite" {
		t.Fatalf("defaultBehavior = %#v, want readWrite", created["defaultBehavior"])
	}
	if len(store.MCPServers()) != 1 {
		t.Fatalf("settings mcpServers length = %d, want 1", len(store.MCPServers()))
	}

	patchBody := map[string]any{
		"enabled": false,
		"toolPermissionPatch": map[string]any{
			"toolName": "read_file",
			"behavior": nil,
		},
	}
	w = serveMCP(handler, http.MethodPatch, "/servers/"+serverID, patchBody)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH /servers/:id status = %d, body=%s", w.Code, w.Body.String())
	}
	updated := decodeResponse(t, w)
	if updated["enabled"] != false {
		t.Fatalf("enabled = %#v, want false", updated["enabled"])
	}
	if _, ok := updated["toolPermissions"]; ok {
		t.Fatalf("toolPermissions should be removed after null behavior patch: %#v", updated)
	}

	w = serveMCP(handler, http.MethodDelete, "/servers/"+serverID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE /servers/:id status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(store.MCPServers()) != 0 {
		t.Fatalf("settings mcpServers length = %d, want 0", len(store.MCPServers()))
	}
}

func TestImportServersPersistsSettingsAndSkipsDuplicates(t *testing.T) {
	handler, store := newTestHandler(t)
	if err := store.SetMCPServers([]map[string]any{{
		"id":        "existing",
		"name":      "dupe",
		"transport": "stdio",
		"enabled":   true,
	}}); err != nil {
		t.Fatalf("seed mcpServers: %v", err)
	}

	body := map[string]any{"json": map[string]any{"mcpServers": map[string]any{
		"dupe": map[string]any{"command": "ignored"},
		"web":  map[string]any{"url": "https://example.com/mcp", "headers": map[string]any{"Authorization": "Bearer token", "Nil": nil}},
	}}}
	w := serveMCP(handler, http.MethodPost, "/servers/import", body)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /servers/import status = %d, body=%s", w.Code, w.Body.String())
	}
	payload := decodeResponse(t, w)
	if payload["added"] != float64(1) || payload["skipped"] != float64(1) {
		t.Fatalf("import payload = %#v, want added=1 skipped=1", payload)
	}
	servers := store.MCPServers()
	if len(servers) != 2 {
		t.Fatalf("settings mcpServers length = %d, want 2", len(servers))
	}
	if servers[1]["transport"] != "streamable-http" {
		t.Fatalf("imported transport = %#v, want streamable-http", servers[1]["transport"])
	}
	headers := servers[1]["headers"].(map[string]any)
	if _, ok := headers["Nil"]; ok {
		t.Fatalf("nil header should be filtered: %#v", headers)
	}
}

func TestListToolsReportsExternalInjectionUnsupportedCapability(t *testing.T) {
	handler, _ := newTestHandler(t)
	w := serveMCP(handler, http.MethodGet, "/tools", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /tools status = %d, body=%s", w.Code, w.Body.String())
	}
	payload := decodeResponse(t, w)
	caps, ok := payload["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("missing capabilities payload: %#v", payload)
	}
	external, ok := caps["externalMCPToolInjection"].(map[string]any)
	if !ok || external["supported"] != false || !strings.Contains(stringOr(external["reason"], ""), "does not execute external MCP tools") {
		t.Fatalf("expected explicit unsupported external MCP injection capability: %#v", caps)
	}
}

func TestProtocolToolsListAndCallUseEmbeddedCompatibilityTools(t *testing.T) {
	handler, _ := newTestHandler(t)
	listReq := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{}}
	w := serveMCP(handler, http.MethodPost, "/", listReq)
	if w.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, body=%s", w.Code, w.Body.String())
	}
	payload := decodeResponse(t, w)
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result payload: %#v", payload)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools/list should expose embedded tools, got %#v", result)
	}
	foundContextTool := false
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		if tool["name"] == "narrafork_get_context_summary" {
			foundContextTool = true
		}
		if strings.HasPrefix(stringOr(tool["name"], ""), "mcp__") {
			t.Fatalf("embedded protocol tools must not inject external mcp__ tools: %#v", tool)
		}
	}
	if !foundContextTool {
		t.Fatalf("tools/list missing embedded context summary tool: %#v", tools)
	}

	callReq := map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "mcp__external__tool", "arguments": map[string]any{}}}
	w = serveMCP(handler, http.MethodPost, "/", callReq)
	if w.Code != http.StatusOK {
		t.Fatalf("tools/call status = %d, body=%s", w.Code, w.Body.String())
	}
	payload = decodeResponse(t, w)
	result, ok = payload["result"].(map[string]any)
	if !ok || result["isError"] != true {
		t.Fatalf("unknown external mcp tool should return MCP error result, got %#v", payload)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 || !strings.Contains(stringOr(content[0].(map[string]any)["text"], ""), "Unknown tool") {
		t.Fatalf("unexpected unknown tool response: %#v", result)
	}
}

func TestNonStdioTransportConnectionReportsCompatibilityDegradation(t *testing.T) {
	handler, store := newTestHandler(t)
	if err := store.SetMCPServers([]map[string]any{{
		"id":        "srv-http",
		"name":      "http-tools",
		"transport": "streamable-http",
		"url":       "https://example.com/mcp",
		"enabled":   true,
	}}); err != nil {
		t.Fatalf("seed non-stdio server: %v", err)
	}
	w := serveMCP(handler, http.MethodPost, "/servers/srv-http/connect", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("connect non-stdio status = %d body=%s", w.Code, w.Body.String())
	}
	payload := decodeResponse(t, w)
	if payload["status"] != "error" || !strings.Contains(stringOr(payload["error"], ""), "only stdio MCP transport") {
		t.Fatalf("expected explicit non-stdio compatibility degradation, got %#v", payload)
	}
}

func TestMutatingServersWithoutSettingsReturnsExplicitFallback(t *testing.T) {
	handler := NewHandler(nil, nil, nil)
	cases := []struct {
		method string
		path   string
		body   any
	}{
		{method: http.MethodPost, path: "/servers", body: map[string]any{"name": "x"}},
		{method: http.MethodPatch, path: "/servers/server-1", body: map[string]any{"name": "x"}},
		{method: http.MethodDelete, path: "/servers/server-1"},
		{method: http.MethodPost, path: "/servers/import", body: map[string]any{"servers": []any{}}},
	}
	for _, tc := range cases {
		w := serveMCP(handler, tc.method, tc.path, tc.body)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d, want 501; body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
		payload := decodeResponse(t, w)
		if payload["ok"] != false || payload["code"] != "FEATURE_DISABLED" || payload["feature"] != "mcp.serverSettingsStorage" || payload["supported"] != false || payload["fallback"] != true {
			t.Fatalf("unexpected MCP fallback payload for %s %s: %#v", tc.method, tc.path, payload)
		}
	}
	w := serveMCP(handler, http.MethodGet, "/servers", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /servers status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}
