package mcpcompat

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/chapters"
	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/narrators"
)

type Handler struct {
	DB        *sql.DB
	Settings  *config.Store
	Chapters  *chapters.Service
	Narrators *narrators.Service
	mu        sync.RWMutex
	state     map[string]mcpRuntimeState
}

type mcpRuntimeState struct {
	Status string
	Tools  []any
	Error  string
}

func NewHandler(sqlDB *sql.DB, chapterService *chapters.Service, narratorService *narrators.Service, stores ...*config.Store) *Handler {
	var store *config.Store
	if len(stores) > 0 {
		store = stores[0]
	}
	return &Handler{DB: sqlDB, Settings: store, Chapters: chapterService, Narrators: narratorService, state: map[string]mcpRuntimeState{}}
}

func writeMCPSettingsStorageDisabled(w http.ResponseWriter) {
	httpx.WriteFeatureDisabled(w, "mcp.serverSettingsStorage", "MCP server settings storage is not available in this go_backend runtime")
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/", httpx.Handler(h.handleProtocol))
	r.Get("/servers", httpx.Handler(h.listServers))
	r.Post("/servers", httpx.Handler(h.createServer))
	r.Post("/servers/import", httpx.Handler(h.importServers))
	r.Get("/servers/{id}", httpx.Handler(h.getServer))
	r.Patch("/servers/{id}", httpx.Handler(h.updateServer))
	r.Delete("/servers/{id}", httpx.Handler(h.deleteServer))
	r.Post("/servers/{id}/connect", httpx.Handler(h.connectServer))
	r.Post("/servers/{id}/disconnect", httpx.Handler(h.disconnectServer))
	r.Post("/servers/test", httpx.Handler(h.testConnection))
	r.Get("/tools", httpx.Handler(h.listTools))
}

func (h *Handler) handleProtocol(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      any            `json:"id"`
		Method  string         `json:"method"`
		Params  map[string]any `json:"params"`
	}
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	if strings.HasPrefix(req.Method, "notifications/") && req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
	switch req.Method {
	case "initialize":
		return h.writeRPCResult(w, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "narrafork", "version": "0.1.0-go"},
		})
	case "tools/list":
		return h.writeRPCResult(w, req.ID, map[string]any{"tools": h.embeddedTools()})
	case "tools/call":
		result := h.callEmbeddedTool(r.Context(), req.Params)
		return h.writeRPCResult(w, req.ID, result)
	default:
		return h.writeRPCError(w, req.ID, -32601, "Method not found")
	}
}

func (h *Handler) writeRPCResult(w http.ResponseWriter, idValue any, result any) error {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": idValue, "result": result})
	return nil
}

func (h *Handler) writeRPCError(w http.ResponseWriter, idValue any, code int, message string) error {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": idValue, "error": map[string]any{"code": code, "message": message}})
	return nil
}

func (h *Handler) embeddedTools() []map[string]any {
	return []map[string]any{
		{
			"name":        "narrafork_list_chapters",
			"description": "List chapters in a project",
			"inputSchema": objectSchema(map[string]any{"projectId": stringSchema("Project ID")}, []string{"projectId"}),
		},
		{
			"name":        "narrafork_fork_chapter",
			"description": "Fork an existing chapter into a new branch",
			"inputSchema": objectSchema(map[string]any{"chapterId": stringSchema("Source chapter ID to fork from"), "title": stringSchema("Title for the new chapter")}, []string{"chapterId", "title"}),
		},
		{
			"name":        "narrafork_merge_chapter",
			"description": "Merge a chapter into a target chapter",
			"inputSchema": objectSchema(map[string]any{"chapterId": stringSchema("Source chapter ID to merge"), "targetChapterId": stringSchema("Target chapter ID to merge into"), "strategy": enumSchema([]string{"merge", "squash", "cherry-pick"}, "Merge strategy")}, []string{"chapterId", "targetChapterId"}),
		},
		{
			"name":        "narrafork_check_conflicts",
			"description": "Check for merge conflicts between two chapters",
			"inputSchema": objectSchema(map[string]any{"chapterId": stringSchema("Source chapter ID"), "targetChapterId": stringSchema("Target chapter ID")}, []string{"chapterId", "targetChapterId"}),
		},
		{
			"name":        "narrafork_abandon_chapter",
			"description": "Mark a chapter as abandoned",
			"inputSchema": objectSchema(map[string]any{"chapterId": stringSchema("Chapter ID to abandon")}, []string{"chapterId"}),
		},
		{
			"name":        "narrafork_list_narrators",
			"description": "List narrators for a chapter",
			"inputSchema": objectSchema(map[string]any{"chapterId": stringSchema("Chapter ID")}, []string{"chapterId"}),
		},
		{
			"name":        "narrafork_get_context_summary",
			"description": "Get a compact context summary and recent messages for a narrator",
			"inputSchema": objectSchema(map[string]any{"narratorId": stringSchema("Narrator ID to get context from")}, []string{"narratorId"}),
		},
	}
}

func (h *Handler) callEmbeddedTool(ctx context.Context, params map[string]any) map[string]any {
	name := strings.TrimSpace(stringOr(params["name"], ""))
	arguments, _ := params["arguments"].(map[string]any)
	if arguments == nil {
		arguments = map[string]any{}
	}
	switch name {
	case "narrafork_list_chapters":
		if h.Chapters == nil {
			return mcpErrorResult("Chapter service is not available")
		}
		projectID := strings.TrimSpace(stringOr(arguments["projectId"], ""))
		if projectID == "" {
			return mcpErrorResult("projectId is required")
		}
		list, err := h.Chapters.List(ctx, projectID)
		if err != nil {
			return mcpErrorResult(err.Error())
		}
		brief := []map[string]any{}
		for _, ch := range list {
			brief = append(brief, map[string]any{"id": ch.ID, "title": ch.Title, "status": ch.Status, "branch": ch.Branch})
		}
		return mcpTextResult(jsonPretty(brief), false)
	case "narrafork_fork_chapter":
		if h.Chapters == nil {
			return mcpErrorResult("Chapter service is not available")
		}
		chapterID := strings.TrimSpace(stringOr(arguments["chapterId"], ""))
		title := strings.TrimSpace(stringOr(arguments["title"], ""))
		if chapterID == "" || title == "" {
			return mcpErrorResult("chapterId and title are required")
		}
		chapter, err := h.Chapters.Fork(ctx, chapterID, chapters.ForkInput{Title: &title})
		if err != nil {
			return mcpErrorResult(err.Error())
		}
		return mcpTextResult(jsonPretty(chapter), false)
	case "narrafork_merge_chapter":
		if h.Chapters == nil {
			return mcpErrorResult("Chapter service is not available")
		}
		chapterID := strings.TrimSpace(stringOr(arguments["chapterId"], ""))
		targetID := strings.TrimSpace(stringOr(arguments["targetChapterId"], ""))
		strategy := strings.TrimSpace(stringOr(arguments["strategy"], "merge"))
		if chapterID == "" || targetID == "" {
			return mcpErrorResult("chapterId and targetChapterId are required")
		}
		result, err := h.Chapters.Merge(ctx, chapterID, targetID, strategy, "", "")
		if err != nil {
			return mcpErrorResult(err.Error())
		}
		return mcpTextResult(jsonPretty(result), false)
	case "narrafork_check_conflicts":
		if h.Chapters == nil {
			return mcpErrorResult("Chapter service is not available")
		}
		chapterID := strings.TrimSpace(stringOr(arguments["chapterId"], ""))
		targetID := strings.TrimSpace(stringOr(arguments["targetChapterId"], ""))
		if chapterID == "" || targetID == "" {
			return mcpErrorResult("chapterId and targetChapterId are required")
		}
		result, err := h.Chapters.MergeCheck(ctx, chapterID, targetID)
		if err != nil {
			return mcpErrorResult(err.Error())
		}
		return mcpTextResult(jsonPretty(result), false)
	case "narrafork_abandon_chapter":
		if h.Chapters == nil {
			return mcpErrorResult("Chapter service is not available")
		}
		chapterID := strings.TrimSpace(stringOr(arguments["chapterId"], ""))
		if chapterID == "" {
			return mcpErrorResult("chapterId is required")
		}
		chapter, err := h.Chapters.Update(ctx, chapterID, map[string]any{"status": "abandoned"})
		if err != nil {
			return mcpErrorResult(err.Error())
		}
		return mcpTextResult(jsonPretty(chapter), false)
	case "narrafork_list_narrators":
		if h.Narrators == nil {
			return mcpErrorResult("Narrator service is not available")
		}
		chapterID := strings.TrimSpace(stringOr(arguments["chapterId"], ""))
		if chapterID == "" {
			return mcpErrorResult("chapterId is required")
		}
		list, err := h.Narrators.List(ctx, chapterID, "", false)
		if err != nil {
			return mcpErrorResult(err.Error())
		}
		brief := []map[string]any{}
		for _, n := range list {
			brief = append(brief, map[string]any{"id": n.ID, "type": n.Type, "status": n.Status, "model": n.Model, "messageCount": n.MessageCount})
		}
		return mcpTextResult(jsonPretty(brief), false)
	case "narrafork_get_context_summary":
		if h.Narrators == nil {
			return mcpErrorResult("Narrator service is not available")
		}
		narratorID := strings.TrimSpace(stringOr(arguments["narratorId"], ""))
		if narratorID == "" {
			return mcpErrorResult("narratorId is required")
		}
		narrator, err := h.Narrators.Get(ctx, narratorID)
		if err != nil {
			return mcpErrorResult(err.Error())
		}
		messages, _ := h.Narrators.Messages(ctx, narratorID, 20)
		return mcpTextResult(narratorContextSummaryText(narrator, messages), false)
	default:
		return mcpErrorResult("Unknown tool: " + name)
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func enumSchema(values []string, description string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": description}
}

func mcpTextResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError}
}

func mcpErrorResult(message string) map[string]any {
	return mcpTextResult("Error: "+message, true)
}

func jsonPretty(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

func (h *Handler) listServers(w http.ResponseWriter, r *http.Request) error {
	servers, err := h.loadServers(r)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"servers": servers})
	return nil
}

func (h *Handler) createServer(w http.ResponseWriter, r *http.Request) error {
	if h.Settings == nil {
		writeMCPSettingsStorageDisabled(w)
		return nil
	}
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	server, err := normalizeServerConfig(body, id.Short())
	if err != nil {
		return err
	}
	servers := h.Settings.MCPServers()
	servers = append(servers, server)
	if err := h.Settings.SetMCPServers(servers); err != nil {
		return err
	}
	setDefaultServerRuntimeFields(server)
	h.applyState(server)
	httpx.WriteJSON(w, http.StatusCreated, server)
	return nil
}

func (h *Handler) getServer(w http.ResponseWriter, r *http.Request) error {
	return h.writeServer(w, r, chi.URLParam(r, "id"), http.StatusOK)
}

func (h *Handler) updateServer(w http.ResponseWriter, r *http.Request) error {
	if h.Settings == nil {
		writeMCPSettingsStorageDisabled(w)
		return nil
	}
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	serverID := chi.URLParam(r, "id")
	servers := h.Settings.MCPServers()
	idx := indexServerByID(servers, serverID)
	if idx < 0 {
		return httpx.NotFound("MCP server")
	}
	updated, err := applyServerPatch(servers[idx], body)
	if err != nil {
		return err
	}
	servers[idx] = updated
	if err := h.Settings.SetMCPServers(servers); err != nil {
		return err
	}
	setDefaultServerRuntimeFields(updated)
	h.applyState(updated)
	httpx.WriteJSON(w, http.StatusOK, updated)
	return nil
}

func (h *Handler) deleteServer(w http.ResponseWriter, r *http.Request) error {
	if h.Settings == nil {
		writeMCPSettingsStorageDisabled(w)
		return nil
	}
	serverID := chi.URLParam(r, "id")
	servers := h.Settings.MCPServers()
	idx := indexServerByID(servers, serverID)
	if idx < 0 {
		return httpx.NotFound("MCP server")
	}
	servers = append(servers[:idx], servers[idx+1:]...)
	if err := h.Settings.SetMCPServers(servers); err != nil {
		return err
	}
	h.mu.Lock()
	delete(h.state, serverID)
	h.mu.Unlock()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) connectServer(w http.ResponseWriter, r *http.Request) error {
	server, err := h.serverMap(r, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	result := probeMcpServer(r.Context(), server)
	server["status"] = "error"
	server["tools"] = []any{}
	server["error"] = result.Error
	if result.OK {
		server["status"] = "connected"
		server["tools"] = result.Tools
		delete(server, "error")
	}
	h.setState(stringOr(server["id"], ""), mcpRuntimeState{Status: stringOr(server["status"], "error"), Tools: result.Tools, Error: result.Error})
	httpx.WriteJSON(w, http.StatusOK, server)
	return nil
}

func (h *Handler) disconnectServer(w http.ResponseWriter, r *http.Request) error {
	h.mu.Lock()
	delete(h.state, chi.URLParam(r, "id"))
	h.mu.Unlock()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) testConnection(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	result := probeMcpServer(r.Context(), body)
	payload := map[string]any{"ok": result.OK, "tools": result.Tools}
	if !result.OK {
		payload["error"] = result.Error
	}
	httpx.WriteJSON(w, http.StatusOK, payload)
	return nil
}

func (h *Handler) listTools(w http.ResponseWriter, r *http.Request) error {
	h.mu.RLock()
	states := make(map[string]mcpRuntimeState, len(h.state))
	for id, state := range h.state {
		states[id] = state
	}
	h.mu.RUnlock()
	servers, err := h.loadServers(r)
	if err != nil {
		return err
	}
	tools := []any{}
	for _, server := range servers {
		state, ok := states[stringOr(server["id"], "")]
		if !ok || state.Status != "connected" {
			continue
		}
		for _, item := range state.Tools {
			tool, ok := item.(map[string]any)
			if !ok {
				continue
			}
			entry := map[string]any{}
			for key, value := range tool {
				entry[key] = value
			}
			entry["serverId"] = server["id"]
			entry["serverName"] = server["name"]
			entry["source"] = stringOr(server["name"], "")
			tools = append(tools, entry)
		}
	}
	metadata := agentbasic.ToolInventoryMetadataForOptions(agentbasic.ToolInventoryOptions{Scope: agentbasic.ToolScopeNormal})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tools": tools, "capabilities": metadata.Capabilities})
	return nil
}

func (h *Handler) setState(serverID string, state mcpRuntimeState) {
	if serverID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state[serverID] = state
}

func (h *Handler) applyState(server map[string]any) {
	serverID := stringOr(server["id"], "")
	if serverID == "" {
		return
	}
	h.mu.RLock()
	state, ok := h.state[serverID]
	h.mu.RUnlock()
	if !ok {
		return
	}
	server["status"] = state.Status
	server["tools"] = state.Tools
	if state.Error != "" {
		server["error"] = state.Error
	} else {
		delete(server, "error")
	}
}

type mcpProbeResult struct {
	OK    bool
	Tools []any
	Error string
}

func probeMcpServer(parent context.Context, config map[string]any) mcpProbeResult {
	if stringOr(config["transport"], "stdio") != "stdio" {
		return mcpProbeResult{OK: false, Tools: []any{}, Error: "only stdio MCP transport is implemented in go_backend compatibility mode"}
	}
	command := strings.TrimSpace(stringOr(config["command"], ""))
	if command == "" {
		return mcpProbeResult{OK: false, Tools: []any{}, Error: "stdio MCP server requires command"}
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, stringSlice(config["args"])...)
	if cwd := strings.TrimSpace(stringOr(config["cwd"], "")); cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = mergedEnv(config["env"])
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return mcpProbeResult{OK: false, Tools: []any{}, Error: err.Error()}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return mcpProbeResult{OK: false, Tools: []any{}, Error: err.Error()}
	}
	stderr := &limitedStringBuilder{limit: 100000}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return mcpProbeResult{OK: false, Tools: []any{}, Error: err.Error()}
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}()
	responses := make(chan map[string]any, 8)
	readErrs := make(chan error, 1)
	go readJSONRPCLines(ctx, stdout, responses, readErrs)
	if err := writeJSONLine(stdin, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "narrafork-go", "version": "0.1.0"}}}); err != nil {
		return mcpProbeResult{OK: false, Tools: []any{}, Error: err.Error()}
	}
	if _, err := waitForJSONRPCID(ctx, responses, readErrs, 1); err != nil {
		return mcpProbeResult{OK: false, Tools: []any{}, Error: withStderr(err, stderr.String())}
	}
	_ = writeJSONLine(stdin, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{}})
	if err := writeJSONLine(stdin, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}}); err != nil {
		return mcpProbeResult{OK: false, Tools: []any{}, Error: err.Error()}
	}
	response, err := waitForJSONRPCID(ctx, responses, readErrs, 2)
	if err != nil {
		return mcpProbeResult{OK: false, Tools: []any{}, Error: withStderr(err, stderr.String())}
	}
	if errObj, ok := response["error"].(map[string]any); ok {
		return mcpProbeResult{OK: false, Tools: []any{}, Error: fmt.Sprint(errObj["message"])}
	}
	result, _ := response["result"].(map[string]any)
	tools := arrayValue(result["tools"])
	return mcpProbeResult{OK: true, Tools: normalizeMcpTools(tools)}
}

func writeJSONLine(stdin interface{ Write([]byte) (int, error) }, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = stdin.Write(data)
	return err
}

func readJSONRPCLines(ctx context.Context, stdout interface{ Read([]byte) (int, error) }, responses chan<- map[string]any, readErrs chan<- error) {
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var msg map[string]any
			if json.Unmarshal(line, &msg) == nil {
				select {
				case responses <- msg:
				case <-ctx.Done():
					return
				}
			}
		}
		if err != nil {
			select {
			case readErrs <- err:
			default:
			}
			return
		}
	}
}

func waitForJSONRPCID(ctx context.Context, responses <-chan map[string]any, readErrs <-chan error, idValue int) (map[string]any, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case err := <-readErrs:
			if err != nil {
				return nil, err
			}
		case msg := <-responses:
			idRaw, ok := msg["id"]
			if !ok {
				continue
			}
			if intNumber(idRaw) == idValue {
				return msg, nil
			}
		}
	}
}

func normalizeMcpTools(tools []any) []any {
	out := []any{}
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringOr(tool["name"], ""))
		if name == "" {
			continue
		}
		entry := map[string]any{"name": name}
		if description := stringOr(tool["description"], ""); description != "" {
			entry["description"] = description
		}
		if schema, ok := tool["inputSchema"]; ok {
			entry["inputSchema"] = schema
		}
		out = append(out, entry)
	}
	return out
}

func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return []string{}
	}
	out := []string{}
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mergedEnv(v any) []string {
	env := os.Environ()
	object := objectValue(v)
	for key, value := range object {
		if s, ok := value.(string); ok {
			env = append(env, key+"="+s)
		}
	}
	return env
}

func intNumber(v any) int {
	switch typed := v.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return 0
	}
}

func withStderr(err error, stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err.Error()
	}
	return err.Error() + ": " + stderr
}

type limitedStringBuilder struct {
	builder strings.Builder
	limit   int
}

func (w *limitedStringBuilder) Write(p []byte) (int, error) {
	remaining := w.limit - w.builder.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = w.builder.Write(p[:remaining])
		} else {
			_, _ = w.builder.Write(p)
		}
	}
	return len(p), nil
}

func (w *limitedStringBuilder) String() string { return w.builder.String() }

func (h *Handler) importServers(w http.ResponseWriter, r *http.Request) error {
	if h.Settings == nil {
		writeMCPSettingsStorageDisabled(w)
		return nil
	}
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	jsonValue, ok := body["json"]
	if !ok || jsonValue == nil {
		return httpx.BadRequest("Invalid JSON")
	}
	if raw, ok := jsonValue.(string); ok {
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return httpx.BadRequest("Invalid JSON")
		}
		jsonValue = decoded
	}
	root, ok := jsonValue.(map[string]any)
	if !ok {
		return httpx.BadRequest("Invalid JSON")
	}
	serverMap, err := normalizeImportServerMap(root)
	if err != nil {
		return err
	}
	servers := h.Settings.MCPServers()
	existingNames := map[string]struct{}{}
	for _, server := range servers {
		if name := strings.TrimSpace(stringOr(server["name"], "")); name != "" {
			existingNames[strings.ToLower(name)] = struct{}{}
		}
	}
	added := 0
	skipped := 0
	for name, cfg := range serverMap {
		name = strings.TrimSpace(name)
		if name == "" || cfg == nil {
			continue
		}
		lowerName := strings.ToLower(name)
		if _, exists := existingNames[lowerName]; exists {
			skipped++
			continue
		}
		body := map[string]any{"id": id.Short(), "name": name, "transport": detectTransport(cfg), "enabled": enabledBool(cfg)}
		copyOptionalServerFields(body, cfg)
		server, err := normalizeServerConfig(body, stringOr(body["id"], id.Short()))
		if err != nil {
			return err
		}
		servers = append(servers, server)
		existingNames[lowerName] = struct{}{}
		added++
	}
	if added > 0 {
		if err := h.Settings.SetMCPServers(servers); err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": skipped})
	return nil
}

func normalizeImportServerMap(root map[string]any) (map[string]map[string]any, error) {
	if raw, ok := root["mcpServers"]; ok {
		if serverMap := importMap(raw); len(serverMap) > 0 {
			return serverMap, nil
		}
	}
	if raw, ok := root["servers"]; ok {
		if serverMap := importMap(raw); len(serverMap) > 0 {
			return serverMap, nil
		}
	}
	if isSingleServerConfig(root) {
		return map[string]map[string]any{deriveImportServerName(root): root}, nil
	}
	serverMap := importMap(root)
	if len(serverMap) == 0 {
		return nil, httpx.BadRequest("Unrecognized format")
	}
	return serverMap, nil
}

func importMap(raw any) map[string]map[string]any {
	object, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]map[string]any{}
	for name, value := range object {
		cfg, ok := value.(map[string]any)
		if ok {
			out[name] = cfg
		}
	}
	return out
}

func isSingleServerConfig(cfg map[string]any) bool {
	if _, ok := cfg["url"].(string); ok {
		return true
	}
	if _, ok := cfg["command"].(string); ok {
		return true
	}
	transport := stringOr(cfg["transport"], stringOr(cfg["type"], ""))
	return transport == "stdio" || transport == "sse" || transport == "streamable-http"
}

func deriveImportServerName(cfg map[string]any) string {
	if rawURL, ok := cfg["url"].(string); ok && rawURL != "" {
		if parsed, err := url.Parse(rawURL); err == nil && parsed.Hostname() != "" {
			if parsed.Hostname() == "localhost" && parsed.Port() != "" {
				return "localhost-" + parsed.Port()
			}
			return parsed.Hostname()
		}
	}
	if command, ok := cfg["command"].(string); ok && strings.TrimSpace(command) != "" {
		command = strings.TrimRight(strings.ReplaceAll(command, "\\", "/"), "/")
		parts := strings.Split(command, "/")
		if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
			return last
		}
	}
	return "imported-server"
}

func detectTransport(cfg map[string]any) string {
	raw := stringOr(cfg["transport"], stringOr(cfg["type"], ""))
	switch raw {
	case "sse":
		return "sse"
	case "streamable-http":
		return "streamable-http"
	case "stdio":
		return "stdio"
	}
	if stringOr(cfg["url"], "") != "" && stringOr(cfg["command"], "") == "" {
		return "streamable-http"
	}
	return "stdio"
}

func arrayValue(v any) []any {
	if arr, ok := v.([]any); ok {
		return arr
	}
	return []any{}
}

func stringArrayValue(v any) []any {
	arr, ok := v.([]any)
	if !ok {
		return []any{}
	}
	out := []any{}
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func objectValue(v any) map[string]any {
	if object, ok := v.(map[string]any); ok {
		return object
	}
	return map[string]any{}
}

func stringObjectValue(v any) map[string]any {
	object, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{}
	for key, value := range object {
		if s, ok := value.(string); ok {
			out[key] = s
		}
	}
	return out
}

func (h *Handler) loadServers(_ *http.Request) ([]map[string]any, error) {
	if h.Settings == nil {
		return []map[string]any{}, nil
	}
	items := h.Settings.MCPServers()
	for _, item := range items {
		setDefaultServerRuntimeFields(item)
		h.applyState(item)
	}
	return items, nil
}

func (h *Handler) writeServer(w http.ResponseWriter, r *http.Request, serverID string, status int) error {
	server, err := h.serverMap(r, serverID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, status, server)
	return nil
}

func (h *Handler) serverMap(_ *http.Request, serverID string) (map[string]any, error) {
	if h.Settings == nil {
		return nil, httpx.NotFound("MCP server")
	}
	for _, server := range h.Settings.MCPServers() {
		if stringOr(server["id"], "") != serverID {
			continue
		}
		setDefaultServerRuntimeFields(server)
		h.applyState(server)
		return server, nil
	}
	return nil, httpx.NotFound("MCP server")
}

func indexServerByID(servers []map[string]any, serverID string) int {
	for idx, server := range servers {
		if stringOr(server["id"], "") == serverID {
			return idx
		}
	}
	return -1
}

func setDefaultServerRuntimeFields(server map[string]any) {
	if _, ok := server["status"]; !ok {
		server["status"] = "disconnected"
	}
	if _, ok := server["tools"]; !ok {
		server["tools"] = []any{}
	}
}

func normalizeServerConfig(input map[string]any, fallbackID string) (map[string]any, error) {
	serverID := strings.TrimSpace(stringOr(input["id"], fallbackID))
	if serverID == "" {
		serverID = id.Short()
	}
	name := strings.TrimSpace(stringOr(input["name"], "Untitled"))
	if name == "" {
		name = "Untitled"
	}
	transport := stringOr(input["transport"], "stdio")
	if transport != "stdio" && transport != "streamable-http" && transport != "sse" {
		return nil, httpx.BadRequest("transport must be one of stdio, streamable-http, or sse")
	}
	server := map[string]any{"id": serverID, "name": name, "transport": transport, "enabled": boolValue(input["enabled"], true)}
	copyOptionalServerFields(server, input)
	if value, ok := input["defaultBehavior"]; ok && value != nil {
		behavior, err := normalizeMcpBehavior(value)
		if err != nil {
			return nil, err
		}
		server["defaultBehavior"] = behavior
	}
	if value, ok := input["toolPermissions"]; ok && value != nil {
		permissions, err := normalizeToolPermissions(value)
		if err != nil {
			return nil, err
		}
		if len(permissions) > 0 {
			server["toolPermissions"] = permissions
		}
	}
	return server, nil
}

func copyOptionalServerFields(dst map[string]any, src map[string]any) {
	for _, key := range []string{"command", "cwd", "url"} {
		if value, ok := src[key].(string); ok && value != "" {
			dst[key] = value
		}
	}
	if args := stringArrayValue(src["args"]); len(args) > 0 {
		dst["args"] = args
	}
	if env := stringObjectValue(src["env"]); len(env) > 0 {
		dst["env"] = env
	}
	if headers := stringObjectValue(src["headers"]); len(headers) > 0 {
		dst["headers"] = headers
	}
}

func applyServerPatch(server map[string]any, patch map[string]any) (map[string]any, error) {
	updated := map[string]any{}
	for key, value := range server {
		if key == "status" || key == "tools" || key == "error" {
			continue
		}
		updated[key] = value
	}
	serverID := stringOr(server["id"], "")
	for _, key := range []string{"name", "transport", "command", "args", "cwd", "env", "url", "headers", "enabled", "defaultBehavior", "toolPermissions"} {
		value, ok := patch[key]
		if !ok {
			continue
		}
		if value == nil {
			delete(updated, key)
			continue
		}
		updated[key] = value
	}
	if toolPatch, ok := patch["toolPermissionPatch"].(map[string]any); ok {
		if err := applyToolPermissionPatch(updated, toolPatch); err != nil {
			return nil, err
		}
	}
	updated["id"] = serverID
	return normalizeServerConfig(updated, serverID)
}

func applyToolPermissionPatch(server map[string]any, patch map[string]any) error {
	toolName := strings.TrimSpace(stringOr(patch["toolName"], ""))
	if toolName == "" {
		return httpx.BadRequest("toolName is required")
	}
	current, err := normalizeToolPermissions(server["toolPermissions"])
	if err != nil {
		return err
	}
	idx := -1
	for i, item := range current {
		rule, _ := item.(map[string]any)
		if rule["toolName"] == toolName {
			idx = i
			break
		}
	}
	_, hasBehavior := patch["behavior"]
	if hasBehavior && patch["behavior"] == nil {
		if idx >= 0 {
			current = append(current[:idx], current[idx+1:]...)
		}
	} else if hasBehavior {
		behavior, err := normalizeMcpBehavior(patch["behavior"])
		if err != nil {
			return err
		}
		rule := map[string]any{"toolName": toolName, "behavior": behavior}
		if enabled, ok := patch["enabled"].(bool); ok {
			rule["enabled"] = enabled
		}
		if idx >= 0 {
			current[idx] = rule
		} else {
			current = append(current, rule)
		}
	} else if enabled, ok := patch["enabled"].(bool); ok && idx >= 0 {
		if rule, ok := current[idx].(map[string]any); ok {
			rule["enabled"] = enabled
		}
	}
	if len(current) > 0 {
		server["toolPermissions"] = current
	} else {
		delete(server, "toolPermissions")
	}
	return nil
}

func normalizeToolPermissions(value any) ([]any, error) {
	items := arrayValue(value)
	permissions := []any{}
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			return nil, httpx.BadRequest("toolPermissions must contain objects")
		}
		toolName := strings.TrimSpace(stringOr(raw["toolName"], ""))
		if toolName == "" {
			return nil, httpx.BadRequest("toolPermissions.toolName is required")
		}
		behavior, err := normalizeMcpBehavior(raw["behavior"])
		if err != nil {
			return nil, err
		}
		entry := map[string]any{"toolName": toolName, "behavior": behavior}
		if enabled, ok := raw["enabled"].(bool); ok {
			entry["enabled"] = enabled
		}
		permissions = append(permissions, entry)
	}
	return permissions, nil
}

func normalizeMcpBehavior(value any) (string, error) {
	behavior := stringOr(value, "")
	if behavior == "allow" {
		behavior = "readWrite"
	}
	switch behavior {
	case "readOnly", "readWrite", "ask", "deny":
		return behavior, nil
	default:
		return "", httpx.BadRequest("behavior must be one of readOnly, readWrite, ask, or deny")
	}
}

func boolValue(value any, fallback bool) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return fallback
}

func enabledBool(cfg map[string]any) bool {
	if disabled, ok := cfg["disabled"].(bool); ok && disabled {
		return false
	}
	return boolValue(cfg["enabled"], true)
}

func narratorContextSummaryText(n narrators.Narrator, messages []narrators.Message) string {
	var b strings.Builder
	model := ""
	if n.Model != nil {
		model = *n.Model
	}
	b.WriteString("Narrator: ")
	b.WriteString(n.Type)
	if model != "" {
		b.WriteString(" (")
		b.WriteString(model)
		b.WriteString(")")
	}
	b.WriteString("\nStatus: ")
	b.WriteString(n.Status)
	b.WriteString("\nMessages: ")
	b.WriteString(fmt.Sprint(n.MessageCount))
	b.WriteString("\n\nContext summary:\n")
	if n.ContextSummary != nil && strings.TrimSpace(*n.ContextSummary) != "" {
		b.WriteString(*n.ContextSummary)
	} else {
		b.WriteString("No stored context summary is available in go_backend yet.")
	}
	if len(messages) > 0 {
		b.WriteString("\n\nRecent messages:\n")
		for _, msg := range messages {
			text := ""
			if msg.ContentText != nil {
				text = *msg.ContentText
			} else if msg.ContentJSON != nil {
				data, _ := json.Marshal(msg.ContentJSON)
				text = string(data)
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			if len(text) > 500 {
				text = text[:500] + "…"
			}
			b.WriteString("- ")
			b.WriteString(msg.Role)
			b.WriteString(": ")
			b.WriteString(text)
			b.WriteString("\n")
			if b.Len() > 8000 {
				b.WriteString("…")
				break
			}
		}
	}
	return b.String()
}

func stringOr(v any, fallback string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fallback
}
