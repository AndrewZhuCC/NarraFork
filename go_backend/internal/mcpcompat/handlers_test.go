package mcpcompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/chapters"
	"narrafork-go/internal/config"
	"narrafork-go/internal/narrators"
)

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("NF_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var req map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		idValue := req["id"]
		switch stringOr(req["method"], "") {
		case "initialize":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": idValue, "result": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}}})
		case "notifications/initialized":
			continue
		case "tools/list":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": idValue, "result": map[string]any{"tools": []any{
				map[string]any{"name": "read_file", "description": "Read a file", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []any{"path"}}},
				map[string]any{"name": "ask_tool", "description": "Ask first", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
				map[string]any{"name": "write_file", "description": "Write a file", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
			}}})
		case "tools/call":
			params, _ := req["params"].(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			text := "called " + stringOr(params["name"], "") + " " + stringOr(args["path"], "")
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": idValue, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": false}})
		default:
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": idValue, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	os.Exit(0)
}

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

func TestCreateEnabledStdioServerConnectsAndListsExternalTools(t *testing.T) {
	handler, store := newTestHandler(t)
	createBody := map[string]any{
		"name":      "Live Tools",
		"transport": "stdio",
		"command":   os.Args[0],
		"args":      []any{"-test.run=TestMCPHelperProcess"},
		"env":       map[string]any{"NF_MCP_HELPER": "1"},
		"enabled":   true,
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
	if created["status"] != "connected" {
		t.Fatalf("enabled stdio create should probe and connect server, got %#v", created)
	}
	tools, _ := created["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("enabled stdio create should discover helper tools, got %#v", created)
	}
	if len(store.MCPServers()) != 1 {
		t.Fatalf("settings mcpServers length = %d, want 1", len(store.MCPServers()))
	}

	w = serveMCP(handler, http.MethodGet, "/tools", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /tools status = %d, body=%s", w.Code, w.Body.String())
	}
	payload := decodeResponse(t, w)
	listedTools, _ := payload["tools"].([]any)
	if len(listedTools) == 0 {
		t.Fatalf("GET /tools should expose connected helper tools, got %#v", payload)
	}
	firstTool, _ := listedTools[0].(map[string]any)
	if firstTool["source"] != "external" || firstTool["serverName"] != "Live Tools" || firstTool["serverId"] != serverID {
		t.Fatalf("MCP tool list should expose TS-compatible source/server metadata, got %#v", firstTool)
	}
}

func TestCreateUpdateDeleteServerPersistsSettings(t *testing.T) {
	handler, store := newTestHandler(t)

	createBody := map[string]any{
		"name":            "local-tools",
		"transport":       "stdio",
		"command":         "bunx",
		"args":            []string{"@example/mcp"},
		"enabled":         false,
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

func TestMCPServerValidationMatchesTSSchema(t *testing.T) {
	handler, _ := newTestHandler(t)
	cases := []struct {
		name string
		path string
		body map[string]any
	}{
		{name: "empty-name", path: "/servers", body: map[string]any{"name": "", "enabled": false}},
		{name: "invalid-enabled", path: "/servers", body: map[string]any{"name": "bad", "enabled": "yes"}},
		{name: "invalid-args", path: "/servers", body: map[string]any{"name": "bad", "args": []any{1}, "enabled": false}},
		{name: "invalid-env", path: "/servers", body: map[string]any{"name": "bad", "env": map[string]any{"A": 1}, "enabled": false}},
		{name: "invalid-url", path: "/servers", body: map[string]any{"name": "bad", "transport": "streamable-http", "url": "not-a-url", "enabled": false}},
		{name: "invalid-tool-permissions", path: "/servers", body: map[string]any{"name": "bad", "toolPermissions": "not-an-array", "enabled": false}},
		{name: "invalid-tool-permission-entry", path: "/servers", body: map[string]any{"name": "bad", "toolPermissions": []any{map[string]any{"toolName": "", "behavior": "ask"}}, "enabled": false}},
		{name: "invalid-test-enabled", path: "/servers/test", body: map[string]any{"name": "bad", "enabled": "yes"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serveMCP(handler, http.MethodPost, tc.path, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
			}
		})
	}

	w := serveMCP(handler, http.MethodPost, "/servers", map[string]any{
		"name":            "compat",
		"transport":       "stdio",
		"command":         "node",
		"args":            []any{"server.js"},
		"enabled":         false,
		"defaultBehavior": "allow",
		"toolPermissions": []any{map[string]any{"toolName": "read_file", "behavior": "allow", "enabled": true}},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected valid create to return 201, got %d body=%s", w.Code, w.Body.String())
	}
	payload := decodeResponse(t, w)
	if payload["defaultBehavior"] != "readWrite" {
		t.Fatalf("defaultBehavior should normalize allow to readWrite: %#v", payload)
	}
	perms, _ := payload["toolPermissions"].([]any)
	if len(perms) != 1 || perms[0].(map[string]any)["behavior"] != "readWrite" {
		t.Fatalf("toolPermissions should normalize allow to readWrite: %#v", payload)
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
		"dupe":  map[string]any{"command": "ignored"},
		"local": map[string]any{"command": "node", "args": []any{"server.js"}, "env": map[string]any{"A": "1", "Nil": nil}},
		"web":   map[string]any{"url": "https://example.com/mcp", "headers": map[string]any{"Authorization": "Bearer token", "Nil": nil}},
	}}}
	w := serveMCP(handler, http.MethodPost, "/servers/import", body)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /servers/import status = %d, body=%s", w.Code, w.Body.String())
	}
	payload := decodeResponse(t, w)
	if payload["added"] != float64(1) || payload["skipped"] != float64(2) {
		t.Fatalf("import payload = %#v, want added=1 skipped=2", payload)
	}
	servers := store.MCPServers()
	if len(servers) != 2 {
		t.Fatalf("settings mcpServers length = %d, want 2", len(servers))
	}
	if servers[1]["name"] != "local" || servers[1]["transport"] != "stdio" {
		t.Fatalf("only stdio import should be persisted, got %#v", servers[1])
	}
	env := servers[1]["env"].(map[string]any)
	if _, ok := env["Nil"]; ok {
		t.Fatalf("nil env should be filtered: %#v", env)
	}
}

func TestListToolsReportsExternalInjectionPartialCapability(t *testing.T) {
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
	if !ok || external["supported"] != true || !strings.Contains(stringOr(external["reason"], ""), "no connected stdio MCP tools") {
		t.Fatalf("expected partial external MCP injection capability: %#v", caps)
	}
}

func TestReadJSONRPCLinesRejectsOversizedLine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	responses := make(chan map[string]any, 1)
	readErrs := make(chan error, 1)
	go readJSONRPCLines(ctx, strings.NewReader(strings.Repeat("x", maxMCPJSONRPCLineBytes+1)+"\n"), responses, readErrs)
	select {
	case err := <-readErrs:
		if err == nil || !strings.Contains(err.Error(), "exceeded") {
			t.Fatalf("expected oversized line error, got %v", err)
		}
	case msg := <-responses:
		t.Fatalf("oversized line should not produce response: %#v", msg)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for oversized line error")
	}
}

func TestMCPToolOutputTruncatesLargeText(t *testing.T) {
	output, isError := mcpToolOutput(map[string]any{
		"isError": false,
		"content": []any{map[string]any{
			"type": "text",
			"text": strings.Repeat("x", maxMCPToolOutputBytes+1024),
		}},
	})
	if isError {
		t.Fatalf("unexpected isError=true")
	}
	if !strings.Contains(output, "[truncated: MCP tool output exceeded") {
		t.Fatalf("expected truncation marker, got suffix %q", output[len(output)-80:])
	}
	if len(output) > maxMCPToolOutputBytes+200 {
		t.Fatalf("truncated output too large: %d", len(output))
	}
}

func TestForkChapterToolSchemaIncludesInheritMode(t *testing.T) {
	handler := &Handler{}
	for _, tool := range handler.embeddedTools() {
		if tool["name"] != "narrafork_fork_chapter" {
			continue
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		inheritMode, _ := properties["inheritMode"].(map[string]any)
		enum, _ := inheritMode["enum"].([]string)
		if strings.Join(enum, ",") != "full,compressed,fresh" {
			t.Fatalf("fork inheritMode enum = %#v", inheritMode["enum"])
		}
		return
	}
	t.Fatalf("missing narrafork_fork_chapter tool")
}

func TestForkChapterToolRejectsInvalidInheritMode(t *testing.T) {
	handler := &Handler{Chapters: &chapters.Service{}}
	for _, tc := range []struct {
		name string
		mode any
		want string
	}{
		{name: "enum", mode: "summary", want: "inheritMode must be full, compressed, or fresh"},
		{name: "type", mode: 1, want: "inheritMode must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := handler.callEmbeddedTool(context.Background(), map[string]any{
				"name": "narrafork_fork_chapter",
				"arguments": map[string]any{
					"chapterId":   "chapter-1",
					"title":       "Fork",
					"inheritMode": tc.mode,
				},
			})
			if result["isError"] != true {
				t.Fatalf("expected MCP error result, got %#v", result)
			}
			content, _ := result["content"].([]any)
			if len(content) == 0 {
				t.Fatalf("missing MCP error content: %#v", result)
			}
			block, _ := content[0].(map[string]any)
			if !strings.Contains(stringOr(block["text"], ""), tc.want) {
				t.Fatalf("unexpected MCP error content: %#v", result)
			}
		})
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

func specNamesContain(specs []agentbasic.ExternalToolSpec, name string) bool {
	for _, spec := range specs {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func TestMCPBehaviorPermissionMatrixMatchesTS(t *testing.T) {
	cases := []struct {
		behavior string
		mode     string
		want     string
	}{
		{"readOnly", "default", "allow"},
		{"readOnly", "readOnly", "allow"},
		{"readOnly", "acceptEdits", "allow"},
		{"readOnly", "dontAsk", "deny"},
		{"readWrite", "default", "ask"},
		{"readWrite", "readOnly", "deny"},
		{"readWrite", "acceptEdits", "allow"},
		{"readWrite", "bypassPermissions", "allow"},
		{"readWrite", "dontAsk", "deny"},
		{"ask", "default", "ask"},
		{"ask", "acceptEdits", "ask"},
		{"ask", "dontAsk", "deny"},
		{"deny", "default", "deny"},
		{"allow", "acceptEdits", "allow"},
		{"allow", "readOnly", "deny"},
	}
	for _, tc := range cases {
		if got := mapMcpBehaviorToPermissionDecision(tc.behavior, tc.mode); got != tc.want {
			t.Fatalf("behavior=%s mode=%s got %s want %s", tc.behavior, tc.mode, got, tc.want)
		}
	}
}

func TestAgentMCPToolSpecsAndExecuteUseConnectedStdioServer(t *testing.T) {
	handler, store := newTestHandler(t)
	if err := store.SetMCPServers([]map[string]any{{
		"id":              "srv_fs",
		"name":            "Filesystem",
		"transport":       "stdio",
		"command":         os.Args[0],
		"args":            []any{"-test.run=TestMCPHelperProcess"},
		"env":             map[string]any{"NF_MCP_HELPER": "1"},
		"enabled":         true,
		"defaultBehavior": "ask",
		"toolPermissions": []any{
			map[string]any{"toolName": "read_file", "behavior": "readOnly"},
			map[string]any{"toolName": "write_file", "behavior": "deny"},
		},
	}}); err != nil {
		t.Fatalf("seed stdio server: %v", err)
	}
	w := serveMCP(handler, http.MethodPost, "/servers/srv_fs/connect", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("connect status = %d body=%s", w.Code, w.Body.String())
	}
	payload := decodeResponse(t, w)
	if payload["status"] != "connected" {
		t.Fatalf("connect payload = %#v", payload)
	}
	specs := handler.AgentToolSpecs(context.Background(), narrators.ExternalToolContext{PermissionMode: "default"})
	if !specNamesContain(specs, "mcp__filesystem__read_file") || !specNamesContain(specs, "mcp__filesystem__ask_tool") || specNamesContain(specs, "mcp__filesystem__write_file") {
		t.Fatalf("unexpected primary agent MCP specs: %#v", specs)
	}
	exploreSpecs := handler.AgentToolSpecs(context.Background(), narrators.ExternalToolContext{Type: "subagent", Variant: "subagent", SubagentType: "explore"})
	if !specNamesContain(exploreSpecs, "mcp__filesystem__read_file") || specNamesContain(exploreSpecs, "mcp__filesystem__ask_tool") {
		t.Fatalf("explore subagent should only see readOnly MCP tools: %#v", exploreSpecs)
	}
	generalSpecs := handler.AgentToolSpecs(context.Background(), narrators.ExternalToolContext{Type: "subagent", Variant: "subagent", SubagentType: "general"})
	if !specNamesContain(generalSpecs, "mcp__filesystem__read_file") || !specNamesContain(generalSpecs, "mcp__filesystem__ask_tool") || specNamesContain(generalSpecs, "mcp__filesystem__write_file") {
		t.Fatalf("general subagent should see non-denied MCP tools: %#v", generalSpecs)
	}
	customSpecs := handler.AgentToolSpecs(context.Background(), narrators.ExternalToolContext{Type: "subagent", Variant: "subagent", SubagentType: "audit", ToolAccess: "custom", CustomTools: []string{"mcp__filesystem__ask_tool"}})
	if specNamesContain(customSpecs, "mcp__filesystem__read_file") || !specNamesContain(customSpecs, "mcp__filesystem__ask_tool") {
		t.Fatalf("custom subagent should only see explicitly listed MCP tools: %#v", customSpecs)
	}
	action, _, handled := handler.ResolveToolPermission("mcp__filesystem__read_file", "readOnly")
	if !handled || action != "allow" {
		t.Fatalf("readOnly MCP permission = action=%s handled=%v", action, handled)
	}
	run, ok := handler.ExecuteTool(context.Background(), "mcp__filesystem__read_file", map[string]any{"path": "README.md"})
	if !ok || run.Status != "completed" || !strings.Contains(run.Output, "called read_file README.md") {
		t.Fatalf("unexpected MCP run: ok=%v run=%#v", ok, run)
	}
}

func TestManualConnectDisabledStdioServerStillInjectsLikeTS(t *testing.T) {
	handler, store := newTestHandler(t)
	if err := store.SetMCPServers([]map[string]any{{
		"id":        "srv_disabled",
		"name":      "Disabled Tools",
		"transport": "stdio",
		"command":   os.Args[0],
		"args":      []any{"-test.run=TestMCPHelperProcess"},
		"env":       map[string]any{"NF_MCP_HELPER": "1"},
		"enabled":   false,
	}}); err != nil {
		t.Fatalf("seed disabled stdio server: %v", err)
	}
	w := serveMCP(handler, http.MethodPost, "/servers/srv_disabled/connect", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("connect status = %d body=%s", w.Code, w.Body.String())
	}
	payload := decodeResponse(t, w)
	if payload["status"] != "connected" || payload["enabled"] != false {
		t.Fatalf("manual connect should connect even disabled servers like TS, got %#v", payload)
	}
	specs := handler.AgentToolSpecs(context.Background(), narrators.ExternalToolContext{})
	if !specNamesContain(specs, "mcp__disabled_tools__read_file") {
		t.Fatalf("manual-connected disabled stdio server should inject tools like TS, got %#v", specs)
	}
	w = serveMCP(handler, http.MethodPatch, "/servers/srv_disabled", map[string]any{"enabled": false})
	if w.Code != http.StatusOK {
		t.Fatalf("disable patch status = %d body=%s", w.Code, w.Body.String())
	}
	specs = handler.AgentToolSpecs(context.Background(), narrators.ExternalToolContext{})
	if specNamesContain(specs, "mcp__disabled_tools__read_file") {
		t.Fatalf("explicit disable update should clear runtime state, got %#v", specs)
	}
}

func TestNonStdioTransportCreateTestAndConnectReportCompatibilityDegradation(t *testing.T) {
	handler, store := newTestHandler(t)
	create := serveMCP(handler, http.MethodPost, "/servers", map[string]any{
		"name":      "http-tools",
		"transport": "streamable-http",
		"url":       "https://example.com/mcp",
		"enabled":   true,
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create non-stdio status = %d body=%s", create.Code, create.Body.String())
	}
	created := decodeResponse(t, create)
	if created["status"] != "error" || !strings.Contains(stringOr(created["error"], ""), "only stdio MCP transport") {
		t.Fatalf("enabled non-stdio create should persist metadata but expose degradation, got %#v", created)
	}
	if specs := handler.AgentToolSpecs(context.Background(), narrators.ExternalToolContext{}); len(specs) != 0 {
		t.Fatalf("non-stdio metadata must not inject agent tools, got %#v", specs)
	}

	test := serveMCP(handler, http.MethodPost, "/servers/test", map[string]any{
		"name":      "sse-tools",
		"transport": "sse",
		"url":       "https://example.com/sse",
		"enabled":   true,
	})
	if test.Code != http.StatusOK {
		t.Fatalf("test non-stdio status = %d body=%s", test.Code, test.Body.String())
	}
	tested := decodeResponse(t, test)
	if tested["ok"] != false || !strings.Contains(stringOr(tested["error"], ""), "only stdio MCP transport") {
		t.Fatalf("non-stdio test should return explicit degradation without saving, got %#v", tested)
	}
	if len(store.MCPServers()) != 1 {
		t.Fatalf("test connection must not persist non-stdio server, settings=%#v", store.MCPServers())
	}

	serverID, _ := created["id"].(string)
	w := serveMCP(handler, http.MethodPost, "/servers/"+serverID+"/connect", nil)
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
		reason, _ := payload["reason"].(string)
		if reason != "MCP server settings storage is not available in this go_backend runtime" || payload["error"] != reason || payload["message"] != reason {
			t.Fatalf("MCP fallback payload should expose reason/error/message, got %#v", payload)
		}
	}
	w := serveMCP(handler, http.MethodGet, "/servers", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /servers status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}
