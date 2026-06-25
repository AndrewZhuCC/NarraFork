package narrators

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
	"narrafork-go/internal/id"
	"narrafork-go/internal/ws"
)

func newTestNarratorService(t *testing.T, model, baseURL string) (*Service, string, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	_, err = settings.Patch(map[string]any{
		"openaiProviders": []any{map[string]any{
			"prefix":       "openai",
			"apiKey":       "test-key",
			"baseUrl":      baseURL,
			"defaultModel": strings.TrimPrefix(model, "openai:"),
		}},
	})
	if err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB, Settings: settings}
	narratorID := id.New()
	cwd := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'primary', 'Test narrator', 'fresh', ?, 'default', 'idle', '[]', 0, ?, 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, narratorID, model, cwd, now, now)
	if err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := service.addMessage(context.Background(), narratorID, "user", "Please help.", nil); err != nil {
		t.Fatalf("insert user message: %v", err)
	}
	return service, narratorID, cwd
}

func createNarratorTestSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE narrators (
			id TEXT PRIMARY KEY,
			chapter_id TEXT,
			api_conversation_id TEXT,
			fork_message_id TEXT,
			type TEXT,
			subagent_type TEXT,
			title TEXT,
			inherit_mode TEXT,
			parent_narrator_id TEXT,
			context_summary TEXT,
			model TEXT,
			system_prompt TEXT,
			permission_mode TEXT,
			previous_permission_mode TEXT,
			plan_file_id TEXT,
			reasoning_effort TEXT,
			fast_mode INTEGER DEFAULT 0,
			relaxed_plan INTEGER DEFAULT 0,
			plan_reflection_auto_approve_override TEXT DEFAULT 'inherit',
			danger_reflection_override TEXT DEFAULT 'inherit',
			message_count INTEGER DEFAULT 0,
			total_cost_usd REAL DEFAULT 0,
			last_message_at TEXT,
			status TEXT DEFAULT 'idle',
			substatus TEXT DEFAULT '[]',
			plan_mode INTEGER DEFAULT 0,
			cwd TEXT,
			prune_boundary_message_id TEXT,
			pruned_percent INTEGER,
			prune_enabled INTEGER DEFAULT 1,
			enabled_tools TEXT,

			error_message TEXT,
			todos_json TEXT,
			todos_tool_use_id TEXT,
			variant TEXT DEFAULT 'primary',
			traits TEXT DEFAULT '[]',
			is_background INTEGER DEFAULT 0,
			background_status TEXT,
			background_result TEXT,
			background_completed_at TEXT,
			is_ask_in_passing INTEGER DEFAULT 0,
			message_version INTEGER DEFAULT 0,
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE narrator_messages (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			sdk_message_uuid TEXT,
			parent_tool_use_id TEXT,
			role TEXT,
			content_json TEXT,
			content_text TEXT,
			tokens_in INTEGER,
			cost_usd REAL,
			turn_usage_json TEXT,
			provider TEXT,
			credential_id TEXT,
			model TEXT,
			output_tokens INTEGER,
			cached_input_tokens INTEGER,
			cache_creation_input_tokens INTEGER,
			cache_creation_5m_tokens INTEGER,
			cache_creation_1h_tokens INTEGER,
			reasoning_tokens INTEGER,
			ttft_ms INTEGER,
			duration_ms INTEGER,
			context_percent REAL,
			meter_usage REAL,
			meter_unit TEXT,
			commit_sha TEXT,
			command_text TEXT,
			created_by TEXT,
			edited_at TEXT,
			edited_by TEXT,
			original_content_json TEXT,
			created_at TEXT
		)`,
		`CREATE TABLE narrator_message_refs (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			message_id TEXT,
			seq INTEGER,
			is_compact INTEGER DEFAULT 0,
			segment_compact_id TEXT
		)`,
		`CREATE TABLE narrator_tool_calls (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			message_id TEXT,
			tool_use_id TEXT,
			tool_name TEXT,
			input_json TEXT,
			output_json TEXT,
			status TEXT,
			duration_ms INTEGER,
			permission_started_at TEXT,
			permission_suggestions TEXT,
			is_background INTEGER DEFAULT 0,
			created_at TEXT,
			execution_started_at TEXT,
			completed_at TEXT,
			result_message_id TEXT,
			error_message TEXT,
			permission_decided_by TEXT,
			permission_decided_at TEXT,
			permission_deny_message TEXT,
			permission_decision_reason TEXT
		)`,
		`CREATE TABLE narrator_file_snapshots (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			file_path TEXT,
			original_content TEXT,
			created_at TEXT
		)`,
		`CREATE TABLE narrator_goals (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			objective TEXT,
			status TEXT,
			sort_order INTEGER DEFAULT 0,
			tokens_used INTEGER DEFAULT 0,
			time_used_seconds INTEGER DEFAULT 0,
			created_by TEXT,
			completed_at TEXT,
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE narrator_buffered_messages (
			id TEXT PRIMARY KEY,
			narrator_id TEXT NOT NULL,
			text TEXT NOT NULL,
			images_json TEXT,
			command_text TEXT,
			created_by TEXT,
			creator_json TEXT,
			text_file_paths_json TEXT,
			priority INTEGER NOT NULL DEFAULT 0,
			seq INTEGER NOT NULL,
			buffered_at TEXT NOT NULL
		)`,
		`CREATE TABLE api_requests (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			message_id TEXT,
			kind TEXT DEFAULT 'narrator',
			provider TEXT,
			credential_id TEXT,
			model TEXT,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cached_input_tokens INTEGER DEFAULT 0,
			cache_creation_input_tokens INTEGER DEFAULT 0,
			cache_creation_5m_tokens INTEGER DEFAULT 0,
			cache_creation_1h_tokens INTEGER DEFAULT 0,
			reasoning_tokens INTEGER DEFAULT 0,
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
		`CREATE TABLE background_tasks (
			id TEXT PRIMARY KEY,
			parent_narrator_id TEXT NOT NULL,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			command TEXT,
			exit_code INTEGER,
			subagent_narrator_id TEXT,
			subagent_type TEXT,
			tool_use_id TEXT,
			alias TEXT,
			title TEXT,
			output TEXT,
			output_bytes INTEGER NOT NULL DEFAULT 0,
			output_truncated INTEGER NOT NULL DEFAULT 0,
			notified INTEGER NOT NULL DEFAULT 0,
			started_at TEXT NOT NULL,
			completed_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func runGitNarratorAgent(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return string(out)
}

func requestHasTool(body map[string]any, name string) bool {
	tools, _ := body["tools"].([]any)
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == name {
			return true
		}
	}
	return false
}

func requestHasToolResult(body map[string]any) bool {
	messages, _ := body["messages"].([]any)
	for _, item := range messages {
		msg, _ := item.(map[string]any)
		if msg["role"] == "tool" {
			return true
		}
	}
	return false
}

func requestToolResultContains(body map[string]any, toolUseID, text string) bool {
	messages, _ := body["messages"].([]any)
	for _, item := range messages {
		msg, _ := item.(map[string]any)
		if msg["role"] != "tool" || stringFromAny(msg["tool_call_id"]) != toolUseID {
			continue
		}
		if strings.Contains(stringFromAny(msg["content"]), text) {
			return true
		}
	}
	return false
}

type runtimeUpdateToolProvider struct {
	execute func(context.Context) error
}

func (p runtimeUpdateToolProvider) AgentToolSpecs(ctx context.Context, info ExternalToolContext) []agentbasic.ExternalToolSpec {
	_ = ctx
	_ = info
	return []agentbasic.ExternalToolSpec{{
		Name:        "mcp__runtime_update",
		Description: "Update narrator runtime settings for tests.",
		Parameters: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		Source: "test",
	}}
}

func (p runtimeUpdateToolProvider) ResolveToolPermission(toolName string, permissionMode string) (string, string, bool) {
	_ = permissionMode
	if toolName == "mcp__runtime_update" {
		return "allow", "", true
	}
	return "", "", false
}

func (p runtimeUpdateToolProvider) ExecuteTool(ctx context.Context, toolName string, args map[string]any) (agentbasic.ToolRun, bool) {
	_ = args
	if toolName != "mcp__runtime_update" {
		return agentbasic.ToolRun{}, false
	}
	if p.execute != nil {
		if err := p.execute(ctx); err != nil {
			return agentbasic.ToolRun{Status: "error", Output: err.Error(), Error: err.Error()}, true
		}
	}
	return agentbasic.ToolRun{Status: "completed", Output: "runtime settings updated"}, true
}

func latestAssistantBlocks(t *testing.T, service *Service, narratorID string) []map[string]any {
	t.Helper()
	var raw string
	if err := service.DB.QueryRow(`SELECT content_json FROM narrator_messages WHERE narrator_id = ? AND role = 'assistant' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&raw); err != nil {
		t.Fatalf("load assistant blocks: %v", err)
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		t.Fatalf("decode assistant blocks: %v raw=%s", err, raw)
	}
	return blocks
}

func TestRunBasicAgentBroadcastsStreamingEventsOverWebSocket(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		count := requests
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "ws-read-1", "type": "function", "function": map[string]any{"name": "Read", "arguments": `{"file_path":"ws-note.txt"}`}}}}}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ws answer"}}},
		})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if err := os.WriteFile(filepath.Join(cwd, "ws-note.txt"), []byte("ws tool content"), 0o644); err != nil {
		t.Fatalf("write ws fixture: %v", err)
	}
	hub := ws.NewHub()
	service.Hub = hub
	authService := &auth.Service{Settings: service.Settings}
	token, err := authService.CreateToken("test-user", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	wsServer := httptest.NewServer(hub.NarratorHandler(authService))
	defer wsServer.Close()
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http") + "?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "subscribe", "narratorIds": []string{narratorID}}); err != nil {
		t.Fatalf("subscribe websocket: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	service.runBasicAgent(context.Background(), narratorID)
	seenStream := false
	seenStreamEvent := false
	seenToolCall := false
	seenToolStarted := false
	seenToolResult := false
	seenToolCompleted := false
	seenMessage := false
	seenStatus := false
	seenStatusChange := false
	seenSubstatusChange := false
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for !seenStream || !seenStreamEvent || !seenToolStarted || !seenToolCompleted || !seenMessage || !seenStatus || !seenStatusChange || !seenSubstatusChange {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}
		switch msg["type"] {
		case "stream_text":
			seenStream = msg["text"] == "ws answer"
		case "stream_event":
			event, _ := msg["event"].(map[string]any)
			delta, _ := event["delta"].(map[string]any)
			seenStreamEvent = event["type"] == "content_block_delta" && delta["type"] == "text_delta" && delta["text"] == "ws answer"
		case "tool_call":
			seenToolCall = msg["toolUseId"] == "ws-read-1"
		case "tool_started":
			seenToolStarted = msg["toolUseId"] == "ws-read-1" && msg["toolName"] == "Read"
		case "tool_result":
			seenToolResult = msg["toolUseId"] == "ws-read-1" && strings.Contains(stringFromAny(msg["output"]), "ws tool content")
		case "tool_completed":
			seenToolCompleted = msg["toolUseId"] == "ws-read-1" && msg["status"] == "success" && strings.Contains(stringFromAny(msg["output"]), "ws tool content")
		case "message":
			seenMessage = true
		case "status":
			seenStatus = msg["status"] == "idle"
		case "status_change":
			seenStatusChange = msg["status"] == "idle"
		case "substatus_change":
			if substatus, ok := msg["substatus"].([]any); ok {
				seenSubstatusChange = len(substatus) == 1 && substatus[0] == "unread"
			} else if substatus, ok := msg["substatus"].([]string); ok {
				seenSubstatusChange = len(substatus) == 1 && substatus[0] == "unread"
			}
		}
	}
	if !seenStream || !seenStreamEvent || !seenToolStarted || !seenToolCompleted || !seenMessage || !seenStatus || !seenStatusChange || !seenSubstatusChange {
		t.Fatalf("expected TS-compatible stream/tool/message/status over websocket, stream=%v streamEvent=%v toolCall=%v toolStarted=%v toolResult=%v toolCompleted=%v message=%v status=%v statusChange=%v substatusChange=%v", seenStream, seenStreamEvent, seenToolCall, seenToolStarted, seenToolResult, seenToolCompleted, seenMessage, seenStatus, seenStatusChange, seenSubstatusChange)
	}
}

func TestRunBasicAgentPersistsStreamingAssistantBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "streamed answer"}}},
		})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	service.runBasicAgent(context.Background(), narratorID)
	blocks := latestAssistantBlocks(t, service, narratorID)
	if len(blocks) != 1 || blocks[0]["type"] != "text" || blocks[0]["text"] != "streamed answer" {
		t.Fatalf("unexpected assistant blocks: %#v", blocks)
	}
}

func TestRunBasicAgentAppliesRuntimeModelReasoningAndSystemPromptBeforeNextRequest(t *testing.T) {
	var mu sync.Mutex
	requests := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
		mu.Lock()
		requests = append(requests, body)
		count := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "runtime-update-1", "type": "function", "function": map[string]any{"name": "mcp__runtime_update", "arguments": `{}`}}}}}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "runtime applied"}}},
		})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-5-test-1", server.URL)
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	service.ExternalTools = runtimeUpdateToolProvider{execute: func(ctx context.Context) error {
		_, err := service.DB.ExecContext(ctx, `UPDATE narrators SET model = ?, reasoning_effort = ?, system_prompt = ?, updated_at = ? WHERE id = ?`, "openai:gpt-5-test-2", "high", "runtime replacement prompt", time.Now().UTC().Format(time.RFC3339Nano), narratorID)
		return err
	}}
	service.runBasicAgent(context.Background(), narratorID)

	mu.Lock()
	captured := append([]map[string]any(nil), requests...)
	mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("expected two provider requests, got %d: %#v", len(captured), captured)
	}
	if captured[0]["model"] != "gpt-5-test-1" {
		t.Fatalf("first request should use initial model, got %#v", captured[0]["model"])
	}
	if captured[1]["model"] != "gpt-5-test-2" {
		t.Fatalf("second request should use updated model, got %#v", captured[1]["model"])
	}
	if captured[1]["reasoning_effort"] != "high" {
		t.Fatalf("second request should use updated reasoning effort, got %#v", captured[1]["reasoning_effort"])
	}
	messages, ok := captured[1]["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("second request missing messages: %#v", captured[1]["messages"])
	}
	firstMessage, _ := messages[0].(map[string]any)
	if firstMessage["role"] != "system" || !strings.Contains(stringFromAny(firstMessage["content"]), "runtime replacement prompt") {
		t.Fatalf("second request should include updated system prompt, got %#v", firstMessage)
	}
	if !hasBroadcast(events(), narratorID, "model_switched") {
		t.Fatalf("expected model_switched broadcast, got %#v", events())
	}
	var status string
	if err := service.DB.QueryRow(`SELECT status FROM narrators WHERE id = ?`, narratorID).Scan(&status); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if status != "idle" {
		t.Fatalf("narrator should finish idle, got %s", status)
	}
	var messageCount, toolCount int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ?`, narratorID).Scan(&messageCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'runtime-update-1'`, narratorID).Scan(&toolCount); err != nil {
		t.Fatalf("count tool calls: %v", err)
	}
	if messageCount == 0 || toolCount != 1 {
		t.Fatalf("expected persisted messages and one runtime tool row, messages=%d tools=%d", messageCount, toolCount)
	}
}

func TestRunBasicAgentCanSwitchProviderPrefixBeforeFollowUpRequest(t *testing.T) {
	var initialMu sync.Mutex
	initialRequests := []map[string]any{}
	initialServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode initial provider request: %v", err)
		}
		initialMu.Lock()
		initialRequests = append(initialRequests, body)
		initialMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "provider-switch-1", "type": "function", "function": map[string]any{"name": "mcp__runtime_update", "arguments": `{}`}}}}}},
		})
	}))
	defer initialServer.Close()

	var switchedMu sync.Mutex
	switchedRequests := []map[string]any{}
	switchedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode switched provider request: %v", err)
		}
		switchedMu.Lock()
		switchedRequests = append(switchedRequests, body)
		switchedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "provider switched"}}},
		})
	}))
	defer switchedServer.Close()

	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-initial", initialServer.URL)
	_, err := service.Settings.Patch(map[string]any{
		"openaiProviders": []any{
			map[string]any{"prefix": "openai", "apiKey": "initial-key", "baseUrl": initialServer.URL, "defaultModel": "gpt-initial"},
			map[string]any{"prefix": "alt", "apiKey": "switched-key", "baseUrl": switchedServer.URL, "defaultModel": "gpt-5-switched"},
		},
	})
	if err != nil {
		t.Fatalf("patch provider settings: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	service.ExternalTools = runtimeUpdateToolProvider{execute: func(ctx context.Context) error {
		_, err := service.DB.ExecContext(ctx, `UPDATE narrators SET model = ?, reasoning_effort = ?, updated_at = ? WHERE id = ?`, "alt:gpt-5-switched", "medium", time.Now().UTC().Format(time.RFC3339Nano), narratorID)
		return err
	}}

	service.runBasicAgent(context.Background(), narratorID)

	initialMu.Lock()
	capturedInitial := append([]map[string]any(nil), initialRequests...)
	initialMu.Unlock()
	switchedMu.Lock()
	capturedSwitched := append([]map[string]any(nil), switchedRequests...)
	switchedMu.Unlock()
	if len(capturedInitial) != 1 {
		t.Fatalf("expected one initial-provider request, got %d", len(capturedInitial))
	}
	if len(capturedSwitched) != 1 {
		t.Fatalf("expected one switched-provider request, got %d", len(capturedSwitched))
	}
	if capturedInitial[0]["model"] != "gpt-initial" {
		t.Fatalf("initial request should use initial model, got %#v", capturedInitial[0]["model"])
	}
	if capturedSwitched[0]["model"] != "gpt-5-switched" || capturedSwitched[0]["reasoning_effort"] != "medium" {
		t.Fatalf("switched request should use updated provider/model/reasoning, got %#v", capturedSwitched[0])
	}
	if !hasBroadcast(events(), narratorID, "model_switched") {
		t.Fatalf("expected model_switched broadcast after provider prefix switch, got %#v", events())
	}
}

func TestRunBasicAgentReResolvesDefaultModelBeforeFollowUpRequest(t *testing.T) {
	var initialMu sync.Mutex
	initialRequests := []map[string]any{}
	initialServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode initial default-provider request: %v", err)
		}
		initialMu.Lock()
		initialRequests = append(initialRequests, body)
		initialMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "default-switch-1", "type": "function", "function": map[string]any{"name": "mcp__runtime_update", "arguments": `{}`}}}}}},
		})
	}))
	defer initialServer.Close()

	var switchedMu sync.Mutex
	switchedRequests := []map[string]any{}
	switchedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode switched default-provider request: %v", err)
		}
		switchedMu.Lock()
		switchedRequests = append(switchedRequests, body)
		switchedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "default model switched"}}},
		})
	}))
	defer switchedServer.Close()

	service, narratorID, _ := newTestNarratorService(t, "__default__", initialServer.URL)
	_, err := service.Settings.Patch(map[string]any{
		"agent": map[string]any{"defaultModel": "openai:gpt-5-default-1"},
		"openaiProviders": []any{
			map[string]any{"prefix": "openai", "apiKey": "initial-key", "baseUrl": initialServer.URL, "defaultModel": "gpt-5-default-1"},
			map[string]any{"prefix": "alt", "apiKey": "switched-key", "baseUrl": switchedServer.URL, "defaultModel": "gpt-5-default-2"},
		},
	})
	if err != nil {
		t.Fatalf("patch initial default-model settings: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	service.ExternalTools = runtimeUpdateToolProvider{execute: func(ctx context.Context) error {
		_, err := service.Settings.Patch(map[string]any{
			"agent": map[string]any{"defaultModel": "alt:gpt-5-default-2"},
		})
		return err
	}}

	service.runBasicAgent(context.Background(), narratorID)

	initialMu.Lock()
	capturedInitial := append([]map[string]any(nil), initialRequests...)
	initialMu.Unlock()
	switchedMu.Lock()
	capturedSwitched := append([]map[string]any(nil), switchedRequests...)
	switchedMu.Unlock()
	if len(capturedInitial) != 1 {
		t.Fatalf("expected one initial default-model request, got %d", len(capturedInitial))
	}
	if len(capturedSwitched) != 1 {
		t.Fatalf("expected one switched default-model request, got %d", len(capturedSwitched))
	}
	if capturedInitial[0]["model"] != "gpt-5-default-1" {
		t.Fatalf("initial request should resolve initial default model, got %#v", capturedInitial[0]["model"])
	}
	if capturedSwitched[0]["model"] != "gpt-5-default-2" {
		t.Fatalf("switched request should re-resolve updated default model, got %#v", capturedSwitched[0]["model"])
	}
	if !hasBroadcast(events(), narratorID, "model_switched") {
		t.Fatalf("expected model_switched broadcast after default-model settings update, got %#v", events())
	}
}

func TestPostMessageStartsAgentLoopAndReturnsIdle(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "post answer"}}},
		})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	router := chi.NewRouter()
	NewHandler(service).RegisterRoutes(router)
	req := httptest.NewRequest(http.MethodPost, "/"+narratorID+"/messages", bytes.NewBufferString(`{"message":"hello from post"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	var msg Message
	if err := json.Unmarshal(w.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode post message: %v body=%s", err, w.Body.String())
	}
	if msg.Role != "user" || msg.ContentText == nil || !strings.Contains(*msg.ContentText, "hello from post") {
		t.Fatalf("unexpected persisted user message: %#v", msg)
	}
	waitForAssistantText(t, service, narratorID, "post answer")
	waitForNarratorStatus(t, service, narratorID, "idle")
	mu.Lock()
	defer mu.Unlock()
	if requests == 0 {
		t.Fatalf("POST /messages did not invoke the agent provider")
	}
}

func TestRunBasicAgentFinalStatusFailurePreservesTranscriptAndPersistsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "answer before final status failure"}}},
		})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`CREATE TRIGGER fail_final_success_idle BEFORE UPDATE OF status ON narrators WHEN NEW.status = 'idle' AND NEW.substatus = '["unread"]' BEGIN SELECT RAISE(ABORT, 'final success status blocked'); END;`); err != nil {
		t.Fatalf("create final status trigger: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub

	service.runBasicAgent(context.Background(), narratorID)

	blocks := latestAssistantBlocks(t, service, narratorID)
	if len(blocks) != 1 || blocks[0]["type"] != "text" || blocks[0]["text"] != "answer before final status failure" {
		t.Fatalf("assistant transcript should survive finalization failure, got %#v", blocks)
	}
	var status, substatus, errorMessage string
	if err := service.DB.QueryRow(`SELECT status, substatus, COALESCE(error_message, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &errorMessage); err != nil {
		t.Fatalf("load narrator after finalization failure: %v", err)
	}
	if status != "idle" || substatus != `["error"]` || !strings.Contains(errorMessage, "final success status blocked") {
		t.Fatalf("narrator should finish with persisted error, status=%s substatus=%s error=%q", status, substatus, errorMessage)
	}
	var errorDisplayCount int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND role = 'disp' AND content_text LIKE '%final success status blocked%'`, narratorID).Scan(&errorDisplayCount); err != nil {
		t.Fatalf("count finalization error display messages: %v", err)
	}
	if errorDisplayCount != 1 {
		t.Fatalf("expected one persisted finalization error display message, got %d", errorDisplayCount)
	}
	if payload := findBroadcast(events(), narratorID, "narrator_error"); payload == nil || !strings.Contains(stringFromAny(payload["error"]), "final success status blocked") || payload["errorCode"] != "AGENT_ERROR" {
		t.Fatalf("expected narrator_error broadcast for finalization failure, got %#v events=%#v", payload, events())
	}
}

func TestRunBasicAgentToolResultFailurePreservesToolUseTranscriptAndPersistsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "unexpected completion"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "read-before-db-failure", "type": "function", "function": map[string]any{"name": "Read", "arguments": `{"file_path":"note.txt"}`}}}}}},
		})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if err := os.WriteFile(filepath.Join(cwd, "note.txt"), []byte("survives failure"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TRIGGER fail_tool_result_success_update BEFORE UPDATE OF status ON narrator_tool_calls WHEN NEW.status = 'success' BEGIN SELECT RAISE(ABORT, 'tool result update blocked'); END;`,
		`CREATE TRIGGER fail_tool_result_success_insert BEFORE INSERT ON narrator_tool_calls WHEN NEW.status = 'success' BEGIN SELECT RAISE(ABORT, 'tool result update blocked'); END;`,
	} {
		if _, err := service.DB.Exec(stmt); err != nil {
			t.Fatalf("create tool result trigger: %v", err)
		}
	}
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub

	service.runBasicAgent(context.Background(), narratorID)

	blocks := latestAssistantBlocks(t, service, narratorID)
	if len(blocks) != 1 || blocks[0]["type"] != "tool_use" || blocks[0]["id"] != "read-before-db-failure" || blocks[0]["name"] != "Read" {
		t.Fatalf("assistant tool_use transcript should survive tool result DB failure, got %#v", blocks)
	}
	var status, substatus, errorMessage string
	if err := service.DB.QueryRow(`SELECT status, substatus, COALESCE(error_message, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &errorMessage); err != nil {
		t.Fatalf("load narrator after tool result failure: %v", err)
	}
	if status != "idle" || substatus != `["error"]` || !strings.Contains(errorMessage, "tool result update blocked") {
		t.Fatalf("narrator should finish with persisted error, status=%s substatus=%s error=%q", status, substatus, errorMessage)
	}
	var errorDisplayCount int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND role = 'disp' AND content_text LIKE '%tool result update blocked%'`, narratorID).Scan(&errorDisplayCount); err != nil {
		t.Fatalf("count tool result error display messages: %v", err)
	}
	if errorDisplayCount != 1 {
		t.Fatalf("expected one persisted tool result error display message, got %d", errorDisplayCount)
	}
	if payload := findBroadcast(events(), narratorID, "narrator_error"); payload == nil || !strings.Contains(stringFromAny(payload["error"]), "tool result update blocked") || payload["errorCode"] != "AGENT_ERROR" {
		t.Fatalf("expected narrator_error broadcast for tool result failure, got %#v events=%#v", payload, events())
	}
}

func TestPostMessageUnsupportedProviderFailFastDoesNotFallbackToOpenAI(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "unexpected fallback"}}},
		})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "kiro:claude-sonnet-4.5", server.URL)
	router := chi.NewRouter()
	NewHandler(service).RegisterRoutes(router)
	req := httptest.NewRequest(http.MethodPost, "/"+narratorID+"/messages", bytes.NewBufferString(`{"message":"hello from kiro"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	waitForNarratorErrorContains(t, service, narratorID, "automatic fallback is disabled")
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("unsupported Kiro provider should fail fast before OpenAI fallback request; requests=%d", requests)
	}
}

func TestPostMessageWhileWorkingReturnsAcceptedBuffer(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "should not run"}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`UPDATE narrators SET status = 'working', substatus = '["calling_model"]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("mark working: %v", err)
	}
	router := chi.NewRouter()
	NewHandler(service).RegisterRoutes(router)
	req := httptest.NewRequest(http.MethodPost, "/"+narratorID+"/messages", bytes.NewBufferString(`{"message":"queue me"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 202 body: %v body=%s", err, w.Body.String())
	}
	if body["queued"] != true || body["fallback"] == true {
		t.Fatalf("expected explicit queued response, got %#v", body)
	}
	var bufferedCount int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_buffered_messages WHERE narrator_id = ? AND text = 'queue me'`, narratorID).Scan(&bufferedCount); err != nil {
		t.Fatalf("count buffered messages: %v", err)
	}
	if bufferedCount != 1 {
		t.Fatalf("expected one buffered message, got %d", bufferedCount)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("busy POST should not start a concurrent agent run; requests=%d", requests)
	}
}

func TestRunBasicAgentBackgroundCompletionUpdatesStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "background done"}}},
		})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	_, err := service.DB.Exec(`UPDATE narrators SET is_background = 1, background_status = 'running' WHERE id = ?`, narratorID)
	if err != nil {
		t.Fatalf("mark background: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)
	var status, backgroundStatus, result string
	if err := service.DB.QueryRow(`SELECT status, background_status, background_result FROM narrators WHERE id = ?`, narratorID).Scan(&status, &backgroundStatus, &result); err != nil {
		t.Fatalf("load background status: %v", err)
	}
	if status != "idle" || backgroundStatus != "completed" || result != "background done" {
		t.Fatalf("unexpected background state status=%s background=%s result=%q", status, backgroundStatus, result)
	}
}

func TestAddGoalReturnsQueryErrorWithoutInsertingFallbackState(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	stmts := []string{
		`ALTER TABLE narrator_goals RENAME TO narrator_goals_real`,
		`CREATE VIEW narrator_goals AS SELECT id, narrator_id, objective, status, broken_sort_order AS sort_order, tokens_used, time_used_seconds, created_by, completed_at, created_at, updated_at FROM narrator_goals_real`,
		`CREATE TRIGGER narrator_goals_insert INSTEAD OF INSERT ON narrator_goals BEGIN INSERT INTO narrator_goals_real (id, narrator_id, objective, status, sort_order, created_by, created_at, updated_at) VALUES (NEW.id, NEW.narrator_id, NEW.objective, NEW.status, NEW.sort_order, NEW.created_by, NEW.created_at, NEW.updated_at); END;`,
	}
	for _, stmt := range stmts {
		if _, err := service.DB.Exec(stmt); err != nil {
			t.Fatalf("prepare broken goals view: %v", err)
		}
	}

	_, err := service.addGoal(context.Background(), narratorID, "do not insert on query failure", "tester")
	if err == nil || !strings.Contains(err.Error(), "query next goal sort order") {
		t.Fatalf("expected next sort order query error, got %v", err)
	}
	var count int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_goals_real`).Scan(&count); err != nil {
		t.Fatalf("count backing goals: %v", err)
	}
	if count != 0 {
		t.Fatalf("query failure should not insert fallback goal state, count=%d", count)
	}
}

func TestRunBasicAgentPlanAndGoalReflectionToolsWorkThroughEventLoop(t *testing.T) {
	goalID := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "GoalCompleteConfirm") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "goal-confirm-1", "type": "function", "function": map[string]any{"name": "GoalCompleteConfirm", "arguments": `{"confirm":true,"evidence":"Verified event loop goal reflection completed successfully.","reflection":"safe"}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "goal updated"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "goal-update-1", "type": "function", "function": map[string]any{"name": "UpdateGoal", "arguments": `{"status":"complete"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	payload, err := service.addGoal(context.Background(), narratorID, "Complete P3 reflection coverage", "tester")
	if err != nil {
		t.Fatalf("add goal: %v", err)
	}
	goal, _ := payload["goal"].(map[string]any)
	goalID = stringFromAny(goal["id"])
	service.runBasicAgent(context.Background(), narratorID)
	started := findBroadcast(events(), narratorID, "goal_reflection_started")
	if started == nil || started["toolUseId"] != "goal-update-1" || started["toolName"] != "UpdateGoal" || started["activeGoal"] == nil {
		t.Fatalf("expected goal_reflection_started for UpdateGoal, got %#v", events())
	}
	resolved := findBroadcast(events(), narratorID, "goal_reflection_resolved")
	if resolved == nil || resolved["toolUseId"] != "goal-update-1" || resolved["decision"] != "allow" {
		t.Fatalf("expected goal_reflection_resolved allow for UpdateGoal, got %#v", events())
	}
	var status string
	if err := service.DB.QueryRow(`SELECT status FROM narrator_goals WHERE id = ?`, goalID).Scan(&status); err != nil {
		t.Fatalf("load goal: %v", err)
	}
	if status != "complete" {
		t.Fatalf("expected goal complete, got %s", status)
	}
	var toolStatus, suggestionsRaw, decisionReason string
	if err := service.DB.QueryRow(`SELECT status, COALESCE(permission_suggestions, ''), COALESCE(permission_decision_reason, '') FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'goal-update-1'`, narratorID).Scan(&toolStatus, &suggestionsRaw, &decisionReason); err != nil {
		t.Fatalf("load goal update tool call: %v", err)
	}
	if toolStatus != "success" || !strings.Contains(suggestionsRaw, "goal_reflection") || !strings.Contains(suggestionsRaw, "confirmed") || decisionReason == "" {
		t.Fatalf("unexpected goal reflection tool call status=%s suggestions=%s reason=%q", toolStatus, suggestionsRaw, decisionReason)
	}
}

func TestReflectPendingAskUserQuestionBroadcastsQuestionReflectionLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": `{"choice":"B"}`}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	input := `{"questions":[{"question":"choice","header":"Pick one","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}],"multiSelect":false}]}`
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := service.DB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-question-reflection', ?, 'ask-question-1', 'AskUserQuestion', ?, 'pending', ?)`, narratorID, input, now); err != nil {
		t.Fatalf("insert pending AskUserQuestion: %v", err)
	}
	answers, err := service.ReflectPendingAskUserQuestion(context.Background(), narratorID, "ask-question-1", false)
	if err != nil {
		t.Fatalf("reflect pending question: %v", err)
	}
	if answers["choice"] != "B" {
		t.Fatalf("expected reflected answer B, got %#v", answers)
	}
	started := findBroadcast(events(), narratorID, "question_reflection_started")
	if started == nil || started["requestId"] != "req-question-reflection" || started["toolUseId"] != "ask-question-1" || started["toolName"] != "AskUserQuestion" || started["inputJson"] == nil {
		t.Fatalf("expected question_reflection_started, got %#v", events())
	}
	resolved := findBroadcast(events(), narratorID, "question_reflection_resolved")
	if resolved == nil || resolved["requestId"] != "req-question-reflection" || resolved["toolUseId"] != "ask-question-1" || resolved["decision"] != "allow" {
		t.Fatalf("expected question_reflection_resolved allow, got %#v", events())
	}
	permissionResolved := findBroadcast(events(), narratorID, "permission_resolved")
	if permissionResolved == nil || permissionResolved["requestId"] != "req-question-reflection" || permissionResolved["decision"] != "allow" || permissionResolved["updatedInput"] == nil {
		t.Fatalf("expected permission_resolved with reflected input, got %#v", events())
	}
	var status, decidedBy, suggestions string
	if err := service.DB.QueryRow(`SELECT status, COALESCE(permission_decided_by, ''), COALESCE(permission_suggestions, '') FROM narrator_tool_calls WHERE id = 'req-question-reflection'`).Scan(&status, &decidedBy, &suggestions); err != nil {
		t.Fatalf("load reflected question tool call: %v", err)
	}
	if status != "running" || decidedBy != "reflection" || !strings.Contains(suggestions, "question_reflection") || !strings.Contains(suggestions, "confirmed") {
		t.Fatalf("unexpected reflected question status=%s decidedBy=%s suggestions=%s", status, decidedBy, suggestions)
	}
}

func TestRunBasicAgentUpdateGoalRejectsLegacyNonActiveGoalID(t *testing.T) {
	pendingGoalID := ""
	reflectionRequested := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "GoalCompleteConfirm") || requestHasTool(body, "GoalCompleteRevise") {
			reflectionRequested = true
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "unexpected reflection"}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "goal rejected"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "goal-update-nonactive-1", "type": "function", "function": map[string]any{"name": "UpdateGoal", "arguments": `{"goal_id":"` + pendingGoalID + `","status":"complete"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	activePayload, err := service.addGoal(context.Background(), narratorID, "Keep this active goal open", "tester")
	if err != nil {
		t.Fatalf("add active goal: %v", err)
	}
	pendingPayload, err := service.addGoal(context.Background(), narratorID, "Do not complete pending goal by id", "tester")
	if err != nil {
		t.Fatalf("add pending goal: %v", err)
	}
	activeGoal, _ := activePayload["goal"].(map[string]any)
	pendingGoal, _ := pendingPayload["goal"].(map[string]any)
	activeGoalID := stringFromAny(activeGoal["id"])
	pendingGoalID = stringFromAny(pendingGoal["id"])
	service.runBasicAgent(context.Background(), narratorID)
	if reflectionRequested {
		t.Fatalf("non-active legacy goal_id should be rejected before goal-completion reflection")
	}
	statuses := map[string]string{}
	rows, err := service.DB.Query(`SELECT id, status FROM narrator_goals WHERE narrator_id = ?`, narratorID)
	if err != nil {
		t.Fatalf("query goals: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var idValue, status string
		if err := rows.Scan(&idValue, &status); err != nil {
			t.Fatalf("scan goal: %v", err)
		}
		statuses[idValue] = status
	}
	if statuses[activeGoalID] != "active" || statuses[pendingGoalID] != "pending" {
		t.Fatalf("unexpected goal statuses after non-active UpdateGoal attempt: %#v", statuses)
	}
}

func TestRunBasicAgentAccountsActiveGoalUsageAndBroadcastsGoalsSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"usage": map[string]any{
				"prompt_tokens":     12,
				"completion_tokens": 5,
				"prompt_tokens_details": map[string]any{
					"cached_tokens": 4,
				},
			},
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "usage counted"}}},
		})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	if _, err := service.DB.Exec(`UPDATE narrators SET plan_mode = 1, traits = '["plan"]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("enable plan mode to suppress continuation: %v", err)
	}
	payload, err := service.addGoal(context.Background(), narratorID, "Track usage for active goal", "tester")
	if err != nil {
		t.Fatalf("add goal: %v", err)
	}
	goalID := stringFromAny(payload["goal"].(map[string]any)["id"])
	service.runBasicAgent(context.Background(), narratorID)
	state := &agentRunState{
		narratorID:        narratorID,
		goalTurnStartedAt: time.Now().Add(-2 * time.Second),
		lastTokenUsage:    &agentbasic.UsageSnapshot{InputTokens: 15, CompletionTokens: 7, CachedInputTokens: 5},
	}
	if err := service.accountGoalUsageForTurn(context.Background(), state); err != nil {
		t.Fatalf("account helper usage: %v", err)
	}
	var tokensUsed, timeUsedSeconds int
	if err := service.DB.QueryRow(`SELECT tokens_used, time_used_seconds FROM narrator_goals WHERE id = ?`, goalID).Scan(&tokensUsed, &timeUsedSeconds); err != nil {
		t.Fatalf("load goal usage: %v", err)
	}
	if tokensUsed != 30 {
		t.Fatalf("expected non-cached input + completion token deltas 30, got %d", tokensUsed)
	}
	if timeUsedSeconds < 2 {
		t.Fatalf("expected time usage to be accounted, got %d", timeUsedSeconds)
	}
	var apiMessageID, apiProvider, apiModel string
	var apiInputTokens, apiOutputTokens, apiCachedTokens int
	if err := service.DB.QueryRow(`SELECT message_id, provider, model, input_tokens, output_tokens, cached_input_tokens FROM api_requests WHERE narrator_id = ?`, narratorID).Scan(&apiMessageID, &apiProvider, &apiModel, &apiInputTokens, &apiOutputTokens, &apiCachedTokens); err != nil {
		t.Fatalf("load api request usage: %v", err)
	}
	if apiMessageID == "" || apiProvider != "openai" || apiModel != "gpt-test" || apiInputTokens != 12 || apiOutputTokens != 5 || apiCachedTokens != 4 {
		t.Fatalf("unexpected api request usage/link: message=%q provider=%q model=%q input=%d output=%d cached=%d", apiMessageID, apiProvider, apiModel, apiInputTokens, apiOutputTokens, apiCachedTokens)
	}
	var msgProvider, msgModel string
	var msgInputTokens, msgOutputTokens, msgCachedTokens int
	if err := service.DB.QueryRow(`SELECT provider, model, tokens_in, output_tokens, cached_input_tokens FROM narrator_messages WHERE id = ?`, apiMessageID).Scan(&msgProvider, &msgModel, &msgInputTokens, &msgOutputTokens, &msgCachedTokens); err != nil {
		t.Fatalf("load assistant message usage: %v", err)
	}
	if msgProvider != "openai" || msgModel != "openai:gpt-test" || msgInputTokens != 12 || msgOutputTokens != 5 || msgCachedTokens != 4 {
		t.Fatalf("unexpected assistant message usage: provider=%q model=%q input=%d output=%d cached=%d", msgProvider, msgModel, msgInputTokens, msgOutputTokens, msgCachedTokens)
	}
	seenUpdatedGoals := false
	for _, event := range events() {
		if event.NarratorID != narratorID || event.Payload["type"] != "goals_set" {
			continue
		}
		goals, _ := event.Payload["goals"].([]any)
		for _, rawGoal := range goals {
			goal, _ := rawGoal.(map[string]any)
			if goal["id"] == goalID && intNumber(goal["tokensUsed"]) == 30 && intNumber(goal["timeUsedSeconds"]) >= 2 {
				seenUpdatedGoals = true
			}
		}
	}
	if !seenUpdatedGoals {
		t.Fatalf("expected goals_set with accounted token/time usage, events=%#v", events())
	}
}

func TestAgentEventsPersistContextAndMeteringToAPIRequestAndAssistantMessage(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "kiro:claude-test", "http://example.invalid")
	ctx := context.Background()
	narrator, err := service.Get(ctx, narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	usage := &agentbasic.UsageSnapshot{InputTokens: 120, CompletionTokens: 8, ContextWindow: 240}
	meterMetadata := map[string]any{"unit": "credit", "unitPlural": "credits", "usage": float64(2.5)}
	apiMetadata := map[string]any{"meterUsage": float64(2.5), "meterUnit": "credit"}

	for _, event := range []agentbasic.AgentEvent{
		{Type: "api_request_start", RequestID: "req-meter", Provider: "kiro", Model: "claude-test", CredentialID: "cred-a"},
		{Type: "context_usage", Percentage: 37.5, Usage: usage},
		{Type: "metering", CredentialID: "cred-a", Metadata: meterMetadata},
		{Type: "api_request_end", RequestID: "req-meter", Provider: "kiro", Model: "claude-test", CredentialID: "cred-a", Usage: usage, Percentage: 37.5, Metadata: apiMetadata},
		{Type: "assistant_message", Text: "metered answer"},
	} {
		if err := service.handleAgentEvent(ctx, state, event); err != nil {
			t.Fatalf("handle %s: %v", event.Type, err)
		}
	}

	var apiMessageID, apiCredentialID, apiMeterUnit string
	var apiContextPercent, apiMeterUsage sql.NullFloat64
	if err := service.DB.QueryRow(`SELECT message_id, credential_id, context_percent, meter_usage, meter_unit FROM api_requests WHERE id = 'req-meter'`).Scan(&apiMessageID, &apiCredentialID, &apiContextPercent, &apiMeterUsage, &apiMeterUnit); err != nil {
		t.Fatalf("load metered api request: %v", err)
	}
	if apiMessageID == "" || apiCredentialID != "cred-a" || !apiContextPercent.Valid || apiContextPercent.Float64 != 37.5 || !apiMeterUsage.Valid || apiMeterUsage.Float64 != 2.5 || apiMeterUnit != "credit" {
		t.Fatalf("api request should persist credential/context/metering telemetry, message=%q credential=%q context=%#v meter=%#v unit=%q", apiMessageID, apiCredentialID, apiContextPercent, apiMeterUsage, apiMeterUnit)
	}

	var msgContextPercent, msgMeterUsage sql.NullFloat64
	var msgMeterUnit sql.NullString
	if err := service.DB.QueryRow(`SELECT context_percent, meter_usage, meter_unit FROM narrator_messages WHERE id = ?`, apiMessageID).Scan(&msgContextPercent, &msgMeterUsage, &msgMeterUnit); err != nil {
		t.Fatalf("load metered assistant message: %v", err)
	}
	if !msgContextPercent.Valid || msgContextPercent.Float64 != 37.5 || !msgMeterUsage.Valid || msgMeterUsage.Float64 != 2.5 || !msgMeterUnit.Valid || msgMeterUnit.String != "credit" {
		t.Fatalf("assistant message should persist context/metering telemetry, context=%#v meter=%#v unit=%#v", msgContextPercent, msgMeterUsage, msgMeterUnit)
	}
}

func TestAPIRequestRawDumpRespectsAgentSettings(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://example.invalid")
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	metadata := map[string]any{"rawDump": map[string]any{"request": map[string]any{"url": "https://example.test"}, "response": map[string]any{"bodyText": strings.Repeat("x", 80)}}}

	if err := service.persistAPIRequestEvent(context.Background(), state, agentbasic.AgentEvent{Type: "api_request_end", RequestID: "dump-disabled", Provider: "openai", Model: "gpt-test", Metadata: metadata}); err != nil {
		t.Fatalf("persist disabled raw dump: %v", err)
	}
	var raw sql.NullString
	if err := service.DB.QueryRow(`SELECT raw_dump_json FROM api_requests WHERE id = 'dump-disabled'`).Scan(&raw); err != nil {
		t.Fatalf("load disabled raw dump: %v", err)
	}
	if raw.Valid {
		t.Fatalf("raw dump should not persist when requestDumpEnabled=false, got %s", raw.String)
	}

	if _, err := service.Settings.Patch(map[string]any{"agent": map[string]any{"requestDumpEnabled": true, "requestDumpErrorsOnly": true, "requestDumpMaxSize": float64(32)}}); err != nil {
		t.Fatalf("patch request dump settings: %v", err)
	}
	if err := service.persistAPIRequestEvent(context.Background(), state, agentbasic.AgentEvent{Type: "api_request_end", RequestID: "dump-success-suppressed", Provider: "openai", Model: "gpt-test", Metadata: metadata}); err != nil {
		t.Fatalf("persist success raw dump: %v", err)
	}
	if err := service.DB.QueryRow(`SELECT raw_dump_json FROM api_requests WHERE id = 'dump-success-suppressed'`).Scan(&raw); err != nil {
		t.Fatalf("load success raw dump: %v", err)
	}
	if raw.Valid {
		t.Fatalf("raw dump should not persist for successful calls when errorsOnly=true, got %s", raw.String)
	}

	if err := service.persistAPIRequestEvent(context.Background(), state, agentbasic.AgentEvent{Type: "api_request_end", RequestID: "dump-error", Provider: "openai", Model: "gpt-test", Message: "provider exploded", Metadata: metadata}); err != nil {
		t.Fatalf("persist error raw dump: %v", err)
	}
	if err := service.DB.QueryRow(`SELECT raw_dump_json FROM api_requests WHERE id = 'dump-error'`).Scan(&raw); err != nil {
		t.Fatalf("load error raw dump: %v", err)
	}
	if !raw.Valid || raw.String == "" {
		t.Fatalf("raw dump should persist for failed calls when errorsOnly=true")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw.String), &parsed); err != nil {
		t.Fatalf("raw dump should be valid JSON: %v raw=%s", err, raw.String)
	}
	if parsed["truncated"] != true || intNumber(parsed["originalBytes"]) <= 32 {
		t.Fatalf("raw dump should be capped with truncation metadata, got %#v", parsed)
	}
}

func TestRunBasicAgentGoalContinuationPersistsSystemMessageAndCompletesGoal(t *testing.T) {
	var sawContinuation bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "GoalCompleteConfirm") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "goal-confirm-continuation", "type": "function", "function": map[string]any{"name": "GoalCompleteConfirm", "arguments": `{"confirm":true,"evidence":"Verified goal continuation completed with concrete evidence.","reflection":"safe"}`}}}}}}})
			return
		}
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "Continue working toward the active NarraFork goal") {
			sawContinuation = true
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "goal-update-continuation", "type": "function", "function": map[string]any{"name": "UpdateGoal", "arguments": `{"status":"complete"}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "goal updated"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "initial answer"}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	payload, err := service.addGoal(context.Background(), narratorID, "Finish the continuation objective", "tester")
	if err != nil {
		t.Fatalf("add goal: %v", err)
	}
	goalID := stringFromAny(payload["goal"].(map[string]any)["id"])
	service.runBasicAgent(context.Background(), narratorID)
	if !sawContinuation {
		t.Fatalf("expected provider history to include goal continuation prompt")
	}
	var status string
	if err := service.DB.QueryRow(`SELECT status FROM narrator_goals WHERE id = ?`, goalID).Scan(&status); err != nil {
		t.Fatalf("load goal status: %v", err)
	}
	if status != "complete" {
		t.Fatalf("expected continuation to complete goal, got %s", status)
	}
	var role, contentJSON string
	if err := service.DB.QueryRow(`SELECT role, content_json FROM narrator_messages WHERE narrator_id = ? AND role = 'system' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&role, &contentJSON); err != nil {
		t.Fatalf("load continuation message: %v", err)
	}
	if role != "system" || !strings.Contains(contentJSON, "goal_continuation") {
		t.Fatalf("expected persisted goal continuation system block, role=%s content=%s", role, contentJSON)
	}
	seenEvent := false
	for _, event := range events() {
		if event.NarratorID == narratorID && event.Payload["type"] == "goal_continuation" {
			seenEvent = true
			break
		}
	}
	if !seenEvent {
		t.Fatalf("expected goal_continuation broadcast, events=%#v", events())
	}
}

func TestRunBasicAgentGoalReflectionRevisePersistsNextSteps(t *testing.T) {
	goalID := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "GoalCompleteRevise") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "goal-revise-1", "type": "function", "function": map[string]any{"name": "GoalCompleteRevise", "arguments": `{"confirm":true,"feedback":"Need final verification.","nextSteps":"Run the final verification, then retry UpdateGoal if it passes."}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "goal still active"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "goal-update-revise-1", "type": "function", "function": map[string]any{"name": "UpdateGoal", "arguments": `{"goal_id":"` + goalID + `","status":"complete","evidence":"Verified event loop goal reflection completed successfully."}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	payload, err := service.addGoal(context.Background(), narratorID, "Complete P3 reflection coverage", "tester")
	if err != nil {
		t.Fatalf("add goal: %v", err)
	}
	goal, _ := payload["goal"].(map[string]any)
	goalID = stringFromAny(goal["id"])
	service.runBasicAgent(context.Background(), narratorID)
	var status string
	if err := service.DB.QueryRow(`SELECT status FROM narrator_goals WHERE id = ?`, goalID).Scan(&status); err != nil {
		t.Fatalf("load goal: %v", err)
	}
	if status != "active" {
		t.Fatalf("expected goal to remain active, got %s", status)
	}
	var toolStatus, suggestionsRaw, decisionReason string
	if err := service.DB.QueryRow(`SELECT status, COALESCE(permission_suggestions, ''), COALESCE(permission_decision_reason, '') FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'goal-update-revise-1'`, narratorID).Scan(&toolStatus, &suggestionsRaw, &decisionReason); err != nil {
		t.Fatalf("load goal update tool call: %v", err)
	}
	if toolStatus != "fail" || !strings.Contains(suggestionsRaw, "goal_reflection") || !strings.Contains(suggestionsRaw, "cancelled") || !strings.Contains(suggestionsRaw, "nextSteps") || !strings.Contains(decisionReason, "Next steps") {
		t.Fatalf("unexpected goal reflection revise status=%s suggestions=%s reason=%q", toolStatus, suggestionsRaw, decisionReason)
	}
}

func TestRunBasicAgentExitPlanReflectionWorksThroughEventLoop(t *testing.T) {
	var mu sync.Mutex
	sawPlanContinuation := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rawBody, _ := json.Marshal(body)
		planContinuationTurn := strings.Contains(string(rawBody), "The user approved your plan and the context has been reset")
		if planContinuationTurn {
			mu.Lock()
			sawPlanContinuation = true
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		if planContinuationTurn {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "plan exited"}}}})
			return
		}
		if requestHasTool(body, "ExitPlanConfirm") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "plan reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "plan-confirm-1", "type": "function", "function": map[string]any{"name": "ExitPlanConfirm", "arguments": `{"confirm":true,"reflection":"plan is actionable"}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "plan exited"}}}})
			return
		}
		plan := "Implement the narrator event handler, update runBasicAgent, add tests, and validate target Go packages."
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "exit-plan-1", "type": "function", "function": map[string]any{"name": "ExitPlanMode", "arguments": `{"plan":"` + plan + `"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	_, err := service.DB.Exec(`UPDATE narrators SET plan_mode = 1, traits = '["plan"]' WHERE id = ?`, narratorID)
	if err != nil {
		t.Fatalf("enter plan mode: %v", err)
	}
	done := make(chan struct{})
	go func() {
		service.runBasicAgent(context.Background(), narratorID)
		close(done)
	}()
	waitForToolStatus(t, service, narratorID, "exit-plan-1", "pending")
	var planMode int
	if err := service.DB.QueryRow(`SELECT plan_mode FROM narrators WHERE id = ?`, narratorID).Scan(&planMode); err != nil {
		t.Fatalf("load plan mode before approval: %v", err)
	}
	if planMode != 1 {
		t.Fatalf("ExitPlanMode should wait for user approval before leaving plan mode, got %d", planMode)
	}
	updatedPlan := "Implement the narrator event handler, update the approval path, persist the edited plan, and validate focused parity tests."
	var inputRaw string
	if err := service.DB.QueryRow(`SELECT input_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'exit-plan-1' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&inputRaw); err != nil {
		t.Fatalf("load ExitPlanMode input: %v", err)
	}
	input := map[string]any{}
	if err := json.Unmarshal([]byte(inputRaw), &input); err != nil {
		t.Fatalf("decode ExitPlanMode input: %v", err)
	}
	input["plan"] = updatedPlan
	input["planSource"] = "updatedPlan"
	input["compactAfter"] = true
	updatedInput, _ := json.Marshal(input)
	if _, err := service.DB.Exec(`UPDATE narrator_tool_calls SET input_json = ? WHERE narrator_id = ? AND tool_use_id = 'exit-plan-1'`, string(updatedInput), narratorID); err != nil {
		t.Fatalf("update approved ExitPlanMode input: %v", err)
	}
	_, err = service.DB.Exec(`UPDATE narrator_tool_calls SET status = 'running', permission_decided_by = 'tester', permission_decided_at = ? WHERE narrator_id = ? AND tool_use_id = 'exit-plan-1'`, time.Now().UTC().Format(time.RFC3339Nano), narratorID)
	if err != nil {
		t.Fatalf("approve ExitPlanMode: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runBasicAgent did not finish after ExitPlanMode approval")
	}
	if err := service.DB.QueryRow(`SELECT plan_mode FROM narrators WHERE id = ?`, narratorID).Scan(&planMode); err != nil {
		t.Fatalf("load plan mode after approval: %v", err)
	}
	if planMode != 0 {
		t.Fatalf("expected plan mode disabled after approved ExitPlanMode, got %d", planMode)
	}
	var contextSummary string
	if err := service.DB.QueryRow(`SELECT COALESCE(context_summary, '') FROM narrators WHERE id = ?`, narratorID).Scan(&contextSummary); err != nil {
		t.Fatalf("load plan compact summary: %v", err)
	}
	if !strings.Contains(contextSummary, updatedPlan) {
		t.Fatalf("expected approved updated plan in compact summary, got %q", contextSummary)
	}
	mu.Lock()
	seenContinuation := sawPlanContinuation
	mu.Unlock()
	if !seenContinuation {
		t.Fatalf("expected ExitPlanMode approval to abort and restart with compact continuation prompt")
	}
}

func TestRunBasicAgentExitPlanApprovalRestartsWithEditedPlanDiffPrompt(t *testing.T) {
	var mu sync.Mutex
	sawDiffContinuation := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rawBody, _ := json.Marshal(body)
		if strings.Contains(string(rawBody), "The user edited your plan before approving it") && strings.Contains(string(rawBody), "+ Validate restart") {
			mu.Lock()
			sawDiffContinuation = true
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "ExitPlanConfirm") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "plan reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "plan-confirm-diff-1", "type": "function", "function": map[string]any{"name": "ExitPlanConfirm", "arguments": `{"confirm":true,"reflection":"plan is actionable"}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "execution started"}}}})
			return
		}
		plan := "Implement the event loop approval restart path, persist continuation prompts, and validate restart behavior."
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "exit-plan-diff-1", "type": "function", "function": map[string]any{"name": "ExitPlanMode", "arguments": `{"plan":"` + plan + `"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`UPDATE narrators SET plan_mode = 1, traits = '["plan"]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("enter plan mode: %v", err)
	}
	done := make(chan struct{})
	go func() {
		service.runBasicAgent(context.Background(), narratorID)
		close(done)
	}()
	waitForToolStatus(t, service, narratorID, "exit-plan-diff-1", "pending")
	var inputRaw string
	if err := service.DB.QueryRow(`SELECT input_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'exit-plan-diff-1' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&inputRaw); err != nil {
		t.Fatalf("load ExitPlanMode input: %v", err)
	}
	input := map[string]any{}
	if err := json.Unmarshal([]byte(inputRaw), &input); err != nil {
		t.Fatalf("decode ExitPlanMode input: %v", err)
	}
	input["plan"] = "Implement the event loop approval restart path, persist continuation prompts, validate restart behavior, and add focused tests."
	input["planSource"] = "updatedPlan"
	input["planDiff"] = "- Validate restart\n+ Validate restart behavior and focused tests"
	updatedInput, _ := json.Marshal(input)
	if _, err := service.DB.Exec(`UPDATE narrator_tool_calls SET input_json = ? WHERE narrator_id = ? AND tool_use_id = 'exit-plan-diff-1'`, string(updatedInput), narratorID); err != nil {
		t.Fatalf("update ExitPlanMode input: %v", err)
	}
	if _, err := service.DB.Exec(`UPDATE narrator_tool_calls SET status = 'running', permission_decided_by = 'tester', permission_decided_at = ? WHERE narrator_id = ? AND tool_use_id = 'exit-plan-diff-1'`, time.Now().UTC().Format(time.RFC3339Nano), narratorID); err != nil {
		t.Fatalf("approve ExitPlanMode: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runBasicAgent did not finish after edited ExitPlanMode approval")
	}
	mu.Lock()
	seenContinuation := sawDiffContinuation
	mu.Unlock()
	if !seenContinuation {
		t.Fatalf("expected edited ExitPlanMode approval to restart with diff continuation prompt")
	}
}

func TestManualPlanModeExitCancelsPendingExitPlanModeApproval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "ExitPlanConfirm") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "plan reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "plan-confirm-cancel-1", "type": "function", "function": map[string]any{"name": "ExitPlanConfirm", "arguments": `{"confirm":true,"reflection":"plan is actionable"}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "plan cancelled"}}}})
			return
		}
		plan := "Implement the cancellation path by keeping plan state safe, updating pending permissions, and adding validation coverage."
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "exit-plan-cancel-1", "type": "function", "function": map[string]any{"name": "ExitPlanMode", "arguments": `{"plan":"` + plan + `"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`UPDATE narrators SET plan_mode = 1, traits = '["plan"]' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("enter plan mode: %v", err)
	}
	done := make(chan struct{})
	go func() {
		service.runBasicAgent(context.Background(), narratorID)
		close(done)
	}()
	waitForToolStatus(t, service, narratorID, "exit-plan-cancel-1", "pending")
	if _, err := service.setNarratorPlanMode(context.Background(), narratorID, false); err != nil {
		t.Fatalf("manual exit plan mode: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runBasicAgent did not finish after pending ExitPlanMode cancellation")
	}
	waitForToolStatus(t, service, narratorID, "exit-plan-cancel-1", "fail")
	var planMode int
	if err := service.DB.QueryRow(`SELECT plan_mode FROM narrators WHERE id = ?`, narratorID).Scan(&planMode); err != nil {
		t.Fatalf("load plan mode after cancellation: %v", err)
	}
	if planMode != 0 {
		t.Fatalf("expected manual plan-mode exit to clear plan mode, got %d", planMode)
	}
}

func TestRunBasicAgentDangerReflectionCanCancelDangerousTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "DangerCancel") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "danger reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "danger-cancel-1", "type": "function", "function": map[string]any{"name": "DangerCancel", "arguments": `{"confirm":true,"feedback":"Do not run this command."}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "danger cancelled"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "danger-bash-1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"touch should-not-run"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	_, err := service.DB.Exec(`UPDATE narrators SET danger_reflection_override = 'on' WHERE id = ?`, narratorID)
	if err != nil {
		t.Fatalf("enable danger reflection: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)
	if _, err := os.Stat(filepath.Join(cwd, "should-not-run")); err == nil {
		t.Fatalf("danger reflection should have prevented Bash execution")
	}
	var status, outputRaw string
	if err := service.DB.QueryRow(`SELECT status, output_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'danger-bash-1'`, narratorID).Scan(&status, &outputRaw); err != nil {
		t.Fatalf("load danger tool call: %v", err)
	}
	if status != "fail" || !strings.Contains(outputRaw, "Do not run") {
		t.Fatalf("unexpected danger tool result status=%s output=%s", status, outputRaw)
	}
}

func TestRunBasicAgentDangerReflectionBroadcastsStartedAndResolved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "DangerCancel") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "danger reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "danger-cancel-1", "type": "function", "function": map[string]any{"name": "DangerCancel", "arguments": `{"confirm":true,"feedback":"Do not run this command."}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "danger cancelled"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "danger-bash-1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"touch should-not-run"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	if _, err := service.DB.Exec(`UPDATE narrators SET danger_reflection_override = 'on' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("enable danger reflection: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)

	started := findBroadcast(events(), narratorID, "danger_reflection_started")
	if started == nil || started["toolUseId"] != "danger-bash-1" || started["toolName"] != "Bash" {
		t.Fatalf("expected danger_reflection_started for the Bash call, got %#v", events())
	}
	// The frontend ToolCallCard keys the take-over button on requestId and renders
	// danger.summary; both must be present on the started event (TS contract).
	if started["requestId"] != "danger-bash-1" {
		t.Fatalf("danger_reflection_started must carry requestId == toolUseId, got %#v", started["requestId"])
	}
	dangerPayload, ok := started["danger"].(map[string]any)
	if !ok || strings.TrimSpace(stringFromAny(dangerPayload["summary"])) == "" {
		t.Fatalf("danger_reflection_started must carry a danger payload with summary, got %#v", started["danger"])
	}
	resolved := findBroadcast(events(), narratorID, "danger_reflection_resolved")
	if resolved == nil || resolved["toolUseId"] != "danger-bash-1" || resolved["decision"] != "deny" {
		t.Fatalf("expected danger_reflection_resolved deny, got %#v", resolved)
	}
	if resolved["requestId"] != "danger-bash-1" {
		t.Fatalf("danger_reflection_resolved must carry requestId == toolUseId, got %#v", resolved["requestId"])
	}
}

func TestRunBasicAgentBypassPermissionsStillRunsDangerReflection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasTool(body, "DangerCancel") {
			if requestHasToolResult(body) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "danger reflection done"}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "danger-cancel-bypass-1", "type": "function", "function": map[string]any{"name": "DangerCancel", "arguments": `{"confirm":true,"feedback":"Bypass must still reflect."}`}}}}}}})
			return
		}
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "bypass danger cancelled"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "danger-bypass-bash-1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"rm bypass-should-not-run"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if err := os.WriteFile(filepath.Join(cwd, "bypass-should-not-run"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("seed bypass danger file: %v", err)
	}
	_, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'bypassPermissions', danger_reflection_override = 'on' WHERE id = ?`, narratorID)
	if err != nil {
		t.Fatalf("enable bypass danger reflection: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)
	if _, err := os.Stat(filepath.Join(cwd, "bypass-should-not-run")); err != nil {
		t.Fatalf("danger reflection should have prevented bypass Bash execution: %v", err)
	}
	var status, outputRaw string
	if err := service.DB.QueryRow(`SELECT status, output_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'danger-bypass-bash-1'`, narratorID).Scan(&status, &outputRaw); err != nil {
		t.Fatalf("load bypass danger tool call: %v", err)
	}
	if status != "fail" || !strings.Contains(outputRaw, "Bypass must still reflect") {
		t.Fatalf("unexpected bypass danger tool result status=%s output=%s", status, outputRaw)
	}
}

func TestRunBasicAgentReadOnlyAgentGeneralDeniedBeforeExecution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "agent denied"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "readonly-agent-general-1", "type": "function", "function": map[string]any{"name": "Agent", "arguments": `{"subagent_type":"general","prompt":"try write access"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'readOnly' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("set readOnly: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)

	var childCount int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrators WHERE parent_narrator_id = ?`, narratorID).Scan(&childCount); err != nil {
		t.Fatalf("count child narrators: %v", err)
	}
	if childCount != 0 {
		t.Fatalf("readOnly general Agent must be denied before creating a child narrator, got %d child narrators", childCount)
	}

	var status, outputRaw string
	if err := service.DB.QueryRow(`SELECT status, output_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'readonly-agent-general-1'`, narratorID).Scan(&status, &outputRaw); err != nil {
		t.Fatalf("load Agent tool call: %v", err)
	}
	if status != "fail" || !strings.Contains(outputRaw, "readOnly") {
		t.Fatalf("expected readOnly Agent denial persisted as failed tool call, status=%s output=%s", status, outputRaw)
	}
}

func TestRunBasicAgentReadOnlySendToHigherPermissionTargetDeniedBeforeExecution(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	targetID := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "send denied"}}}})
			return
		}
		mu.Lock()
		requests++
		requestNumber := requests
		mu.Unlock()
		if requestNumber == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "readonly-send-high-1", "type": "function", "function": map[string]any{"name": "Send", "arguments": `{"id":"` + targetID + `","message":"hello from low trust"}`}}}}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "target idle"}}}})
	}))
	defer server.Close()

	service, parentID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	parent, err := service.Get(context.Background(), parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	sender, err := service.createSubagentNarrator(context.Background(), parent, "explore", "Sender", "openai:gpt-test", "", cwd, "sender", false)
	if err != nil {
		t.Fatalf("create sender: %v", err)
	}
	target, err := service.createSubagentNarrator(context.Background(), parent, "general", "Target", "openai:gpt-test", "", cwd, "target", false)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	targetID = target.ID
	if _, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'acceptEdits' WHERE id = ?`, target.ID); err != nil {
		t.Fatalf("raise target permission: %v", err)
	}
	if _, err := service.addMessage(context.Background(), sender.ID, "user", "send to target", nil); err != nil {
		t.Fatalf("seed sender message: %v", err)
	}
	service.runBasicAgent(context.Background(), sender.ID)

	var targetMessageCount int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND content_text = 'hello from low trust'`, target.ID).Scan(&targetMessageCount); err != nil {
		t.Fatalf("count target messages: %v", err)
	}
	if targetMessageCount != 0 {
		t.Fatalf("readOnly Send to higher-permission target must be denied before enqueueing a target message, got %d", targetMessageCount)
	}
	var status, outputRaw string
	if err := service.DB.QueryRow(`SELECT status, output_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'readonly-send-high-1'`, sender.ID).Scan(&status, &outputRaw); err != nil {
		t.Fatalf("load Send tool call: %v", err)
	}
	if status != "fail" || !strings.Contains(outputRaw, "readOnly") {
		t.Fatalf("expected readOnly Send denial persisted as failed tool call, status=%s output=%s", status, outputRaw)
	}
}

func TestRunBasicAgentReadOnlyTeamStatusDeniedBeforeTeamMessage(t *testing.T) {
	var targetID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "team status denied"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "readonly-teamstatus-1", "type": "function", "function": map[string]any{"name": "TeamStatus", "arguments": `{"action":"send","target_id":"` + targetID + `","message":"team ping"}`}}}}}}})
	}))
	defer server.Close()

	service, parentID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	parent, err := service.Get(context.Background(), parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	sender, err := service.createSubagentNarrator(context.Background(), parent, "explore", "Sender", "openai:gpt-test", "", cwd, "sender", false)
	if err != nil {
		t.Fatalf("create sender: %v", err)
	}
	target, err := service.createSubagentNarrator(context.Background(), parent, "explore", "Target", "openai:gpt-test", "", cwd, "target", false)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	targetID = target.ID
	if _, err := service.addMessage(context.Background(), sender.ID, "user", "send team ping", nil); err != nil {
		t.Fatalf("seed sender message: %v", err)
	}
	service.runBasicAgent(context.Background(), sender.ID)

	var targetMessageCount int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND content_text = 'team ping'`, target.ID).Scan(&targetMessageCount); err != nil {
		t.Fatalf("count team target messages: %v", err)
	}
	if targetMessageCount != 0 {
		t.Fatalf("readOnly TeamStatus send must be denied before enqueueing a target message, got %d", targetMessageCount)
	}
	var status, outputRaw string
	if err := service.DB.QueryRow(`SELECT status, output_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'readonly-teamstatus-1'`, sender.ID).Scan(&status, &outputRaw); err != nil {
		t.Fatalf("load TeamStatus tool call: %v", err)
	}
	if status != "fail" || !strings.Contains(outputRaw, "readOnly") {
		t.Fatalf("expected readOnly TeamStatus denial persisted as failed tool call, status=%s output=%s", status, outputRaw)
	}
}

func TestRunBasicAgentNonPermissionWritePersistsFileSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "write complete"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "trust-write-1", "type": "function", "function": map[string]any{"name": "Write", "arguments": `{"file_path":"trusted.txt","content":"trusted"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	_, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'trust-all', danger_reflection_override = 'off' WHERE id = ?`, narratorID)
	if err != nil {
		t.Fatalf("set trust-all: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)
	if data, err := os.ReadFile(filepath.Join(cwd, "trusted.txt")); err != nil || string(data) != "trusted" {
		t.Fatalf("expected trusted write, data=%q err=%v", string(data), err)
	}
	var snapshotCount int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_file_snapshots WHERE narrator_id = ?`, narratorID).Scan(&snapshotCount); err != nil {
		t.Fatalf("count non-permission snapshots: %v", err)
	}
	if snapshotCount == 0 {
		t.Fatalf("expected non-permission Write to persist file snapshot")
	}
}

func TestAgentEventsPersistAndBroadcastSideCars(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	if _, err := service.DB.Exec(`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, target TEXT, source TEXT, content TEXT, order_index INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create narrator_sidecars: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	if err := service.handleAgentEvent(context.Background(), state, agentbasic.AgentEvent{Type: "tool_call", ToolUseID: "side-tool-1", ToolName: "Read", Input: map[string]any{"file_path": "README.md"}}); err != nil {
		t.Fatalf("handle tool call: %v", err)
	}
	if err := service.handleAgentEvent(context.Background(), state, agentbasic.AgentEvent{Type: "tool_result", ToolUseID: "side-tool-1", ToolName: "Read", Output: "read output", ToolRun: &agentbasic.ToolRun{ID: "side-tool-1", Name: "Read", Arguments: `{"file_path":"README.md"}`, Output: "read output", Status: "completed"}, SideCars: []agentbasic.AgentSideCar{{Target: "tool_result", Source: "todo_reminder", Content: "check todos", ToolUseID: "ignored", OrderIndex: 7}}}); err != nil {
		t.Fatalf("handle tool result: %v", err)
	}
	if err := service.handleAgentEvent(context.Background(), state, agentbasic.AgentEvent{Type: "sidecars", SideCars: []agentbasic.AgentSideCar{{Target: "user_message", Source: "team_message", Content: "team ping", OrderIndex: 2}}}); err != nil {
		t.Fatalf("handle sidecars: %v", err)
	}

	completed := findBroadcast(events(), narratorID, "tool_completed")
	if completed == nil || completed["sideCars"] == nil {
		t.Fatalf("expected tool_completed sideCars broadcast, got %#v", events())
	}
	sidecarsFrame := findBroadcast(events(), narratorID, "sidecars")
	if sidecarsFrame == nil || sidecarsFrame["sideCars"] == nil {
		t.Fatalf("expected standalone sidecars broadcast, got %#v", events())
	}
	var toolRows, userRows int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_sidecars WHERE narrator_id = ? AND tool_use_id = 'side-tool-1' AND target = 'tool_result' AND source = 'todo_reminder' AND content = 'check todos'`, narratorID).Scan(&toolRows); err != nil {
		t.Fatalf("count tool sidecars: %v", err)
	}
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_sidecars WHERE narrator_id = ? AND tool_use_id IS NULL AND target = 'user_message' AND source = 'team_message' AND content = 'team ping'`, narratorID).Scan(&userRows); err != nil {
		t.Fatalf("count user sidecars: %v", err)
	}
	if toolRows != 1 || userRows != 1 {
		t.Fatalf("expected persisted tool/user sidecars, tool=%d user=%d", toolRows, userRows)
	}
}

func TestRunBasicAgentInjectsPriorityBufferedMessageAsAfterToolsSideCar(t *testing.T) {
	var sawBufferedSideCar bool
	var postToolRequest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			postToolRequest = string(raw)
			sawBufferedSideCar = strings.Contains(postToolRequest, "queued follow-up") && strings.Contains(postToolRequest, "buffered_user") && strings.Contains(postToolRequest, "attached_files") && strings.Contains(postToolRequest, "/tmp/queued.txt")
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "done with buffer"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "buffer-read-1", "type": "function", "function": map[string]any{"name": "Read", "arguments": `{"file_path":"buffer.txt"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, target TEXT, source TEXT, content TEXT, order_index INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create narrator_sidecars: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "buffer.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	contentBlocks := []map[string]any{
		{"type": "image", "imageId": "img-1", "filename": "diagram.png", "mediaType": "image/png"},
		{"type": "text_file", "filename": "queued.txt", "size": 12, "filePath": "/tmp/queued.txt"},
		{"type": "text", "text": "queued follow-up"},
	}
	buffered, err := service.BufferUserMessage(context.Background(), narratorID, "tester", "queued follow-up", contentBlocks, true)
	if err != nil {
		t.Fatalf("seed buffer: %v", err)
	}
	if _, err := service.DB.Exec(`UPDATE narrator_buffered_messages SET command_text = ? WHERE id = ?`, "/review queued", buffered.ID); err != nil {
		t.Fatalf("set buffered command text: %v", err)
	}

	service.runBasicAgent(context.Background(), narratorID)
	if !sawBufferedSideCar {
		t.Fatalf("expected provider request to include buffered_user sidecar with attachment hint, got %s", postToolRequest)
	}
	var bufferedRows int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&bufferedRows); err != nil {
		t.Fatalf("count buffer: %v", err)
	}
	if bufferedRows != 0 {
		t.Fatalf("expected buffer consumed, got %d", bufferedRows)
	}
	var contentText, commandText, contentRaw string
	if err := service.DB.QueryRow(`SELECT COALESCE(content_text, ''), COALESCE(command_text, ''), COALESCE(content_json, '') FROM narrator_messages WHERE narrator_id = ? AND role = 'user' AND command_text = '/review queued' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&contentText, &commandText, &contentRaw); err != nil {
		t.Fatalf("load persisted buffered user message: %v", err)
	}
	if commandText != "/review queued" || !strings.Contains(contentText, "<attached_files>") || !strings.Contains(contentText, "/tmp/queued.txt") {
		t.Fatalf("unexpected buffered user message text=%q command=%q json=%s", contentText, commandText, contentRaw)
	}
	if !strings.Contains(contentRaw, `"type":"image"`) || !strings.Contains(contentRaw, `"type":"text_file"`) || !strings.Contains(contentRaw, `"text":"queued follow-up"`) {
		t.Fatalf("expected image/text_file/text blocks in content_json, got %s", contentRaw)
	}
	var sideCarRows int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_sidecars WHERE narrator_id = ? AND target = 'user_message' AND source = 'buffered_user' AND content LIKE '%<attached_files>%' AND content LIKE '%/tmp/queued.txt%'`, narratorID).Scan(&sideCarRows); err != nil {
		t.Fatalf("count persisted buffered sidecars: %v", err)
	}
	if sideCarRows != 1 {
		t.Fatalf("expected persisted buffered sidecar, got %d", sideCarRows)
	}
}

func TestRunBasicAgentPriorityBufferedAfterToolsSideCarStopsBeforeThirdRequest(t *testing.T) {
	var mu sync.Mutex
	requestBodies := []string{}
	postBufferedRequestIndex := -1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		bodyText := string(raw)
		mu.Lock()
		requestIndex := len(requestBodies)
		requestBodies = append(requestBodies, bodyText)
		if requestHasToolResult(body) && postBufferedRequestIndex < 0 && strings.Contains(bodyText, "queued fallback follow-up") && strings.Contains(bodyText, "buffered_user") {
			postBufferedRequestIndex = requestIndex
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch requestIndex {
		case 0:
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "fallback-read-1", "type": "function", "function": map[string]any{"name": "Read", "arguments": `{"file_path":"buffer.txt"}`}}}}}}})
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "fallback-read-2", "type": "function", "function": map[string]any{"name": "Read", "arguments": `{"file_path":"second.txt"}`}}}}}}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "finished after fallback continuation"}}}})
		}
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, target TEXT, source TEXT, content TEXT, order_index INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create narrator_sidecars: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "buffer.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write buffer fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "second.txt"), []byte("second content"), 0o644); err != nil {
		t.Fatalf("write second fixture: %v", err)
	}
	if _, err := service.BufferUserMessage(context.Background(), narratorID, "tester", "queued fallback follow-up", []map[string]any{{"type": "text", "text": "queued fallback follow-up"}}, true); err != nil {
		t.Fatalf("seed buffer: %v", err)
	}

	service.runBasicAgent(context.Background(), narratorID)

	mu.Lock()
	requests := append([]string(nil), requestBodies...)
	postIndex := postBufferedRequestIndex
	mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected soft-stop after the post-tool request and before a third provider request, got %d requests: %#v", len(requests), requests)
	}
	if postIndex != 1 {
		t.Fatalf("expected second request to carry buffered_user after-tools sidecar, index=%d bodies=%#v", postIndex, requests)
	}
	var sideCarRows int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_sidecars WHERE narrator_id = ? AND target = 'user_message' AND source = 'buffered_user' AND content LIKE '%queued fallback follow-up%'`, narratorID).Scan(&sideCarRows); err != nil {
		t.Fatalf("count persisted buffered sidecars: %v", err)
	}
	if sideCarRows != 1 {
		t.Fatalf("expected persisted buffered sidecar evidence, got %d", sideCarRows)
	}
	var toolResultRows int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_tool_calls WHERE narrator_id = ? AND status = 'success' AND result_message_id IS NOT NULL`, narratorID).Scan(&toolResultRows); err != nil {
		t.Fatalf("count completed tool results: %v", err)
	}
	if toolResultRows != 2 {
		t.Fatalf("expected soft-stop to preserve tool results before final idle, got %d completed tool results", toolResultRows)
	}
	var status string
	if err := service.DB.QueryRow(`SELECT status FROM narrators WHERE id = ?`, narratorID).Scan(&status); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if status != "idle" {
		t.Fatalf("expected final run status idle, got %q", status)
	}
}

func TestRunBasicAgentPersistsSeparateAssistantMessagesAcrossToolTurns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "final answer"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "multi-read-1", "type": "function", "function": map[string]any{"name": "Read", "arguments": `{"file_path":"multi.txt"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if err := os.WriteFile(filepath.Join(cwd, "multi.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)

	rows, err := service.DB.Query(`SELECT COALESCE(content_text, ''), COALESCE(content_json, '[]') FROM narrator_messages WHERE narrator_id = ? AND role = 'assistant' ORDER BY created_at ASC`, narratorID)
	if err != nil {
		t.Fatalf("query assistant messages: %v", err)
	}
	defer rows.Close()
	assistantTexts := []string{}
	assistantJSON := []string{}
	for rows.Next() {
		var text, contentJSON string
		if err := rows.Scan(&text, &contentJSON); err != nil {
			t.Fatalf("scan assistant: %v", err)
		}
		assistantTexts = append(assistantTexts, text)
		assistantJSON = append(assistantJSON, contentJSON)
	}
	if len(assistantTexts) != 2 {
		t.Fatalf("expected separate tool/final assistant messages, texts=%#v json=%#v", assistantTexts, assistantJSON)
	}
	if !strings.Contains(assistantJSON[0], `"tool_use"`) || strings.TrimSpace(assistantTexts[0]) != "" {
		t.Fatalf("expected first assistant to contain only tool_use, text=%q json=%s", assistantTexts[0], assistantJSON[0])
	}
	if assistantTexts[1] != "final answer" || strings.Contains(assistantJSON[1], `"tool_use"`) {
		t.Fatalf("expected second assistant final text without tool_use, text=%q json=%s", assistantTexts[1], assistantJSON[1])
	}
}

func TestAgentSideCarCollectorDrainsGoalUpdateSideCar(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	service.runMu.Lock()
	service.activeRuns = map[string]context.CancelFunc{narratorID: func() {}}
	service.runMu.Unlock()
	if _, err := service.addGoal(context.Background(), narratorID, "external goal", "user-1"); err != nil {
		t.Fatalf("add goal: %v", err)
	}
	collector := service.agentSideCarCollector(narratorID)
	sideCars := collector(context.Background(), agentbasic.SideCarRequest{Phase: "after_tools"})
	if len(sideCars) != 1 || sideCars[0].Source != "goal_update" || sideCars[0].OrderIndex != 0 || !strings.Contains(sideCars[0].Content, "action: add") || !strings.Contains(sideCars[0].Content, "1. [active] external goal") {
		t.Fatalf("expected goal_update sidecar, got %#v", sideCars)
	}
	if again := collector(context.Background(), agentbasic.SideCarRequest{Phase: "after_tools"}); len(again) != 0 {
		t.Fatalf("expected goal update notice drained, got %#v", again)
	}
}

func TestAgentSideCarCollectorDoesNotConsumeNonPriorityBufferAfterTools(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	contentBlocks := []map[string]any{{"type": "text", "text": "normal queued follow-up"}}
	if _, err := service.BufferUserMessage(context.Background(), narratorID, "tester", "normal queued follow-up", contentBlocks, false); err != nil {
		t.Fatalf("seed buffer: %v", err)
	}
	collector := service.agentSideCarCollector(narratorID)
	sideCars := collector(context.Background(), agentbasic.SideCarRequest{Phase: "after_tools"})
	if len(sideCars) != 0 {
		t.Fatalf("non-priority buffer should wait for run completion, got sidecars %#v", sideCars)
	}
	var bufferedRows int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&bufferedRows); err != nil {
		t.Fatalf("count buffer rows: %v", err)
	}
	if bufferedRows != 1 {
		t.Fatalf("non-priority buffer should remain queued, got %d rows", bufferedRows)
	}
}

func TestAgentSideCarCollectorDrainsBackgroundCompletionSideCar(t *testing.T) {
	service, parentID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := service.DB.Exec(`INSERT INTO background_tasks (id, parent_narrator_id, type, status, subagent_narrator_id, subagent_type, tool_use_id, alias, title, output, output_bytes, output_truncated, notified, started_at, created_at, updated_at) VALUES ('bg-notice-1', ?, 'agent', 'running', 'bg-notice-1', 'general', 'agent-bg-1', 'bg', 'Background Worker', NULL, 0, 0, 0, ?, ?, ?)`, parentID, now, now, now); err != nil {
		t.Fatalf("insert background task: %v", err)
	}
	if err := service.finishBackgroundTaskRecord(context.Background(), parentID, "bg-notice-1", "completed", "background result"); err != nil {
		t.Fatalf("finish background task: %v", err)
	}
	collector := service.agentSideCarCollector(parentID)
	sideCars := collector(context.Background(), agentbasic.SideCarRequest{Phase: "after_tools"})
	if len(sideCars) != 1 || sideCars[0].Source != "bg_agent" || !strings.Contains(sideCars[0].Content, `Background agent "Background Worker" (ID: bg-notice-1) completed`) || !strings.Contains(sideCars[0].Content, "Result preview: background result") {
		t.Fatalf("expected bg_agent sidecar, got %#v", sideCars)
	}
	if again := collector(context.Background(), agentbasic.SideCarRequest{Phase: "after_tools"}); len(again) != 0 {
		t.Fatalf("expected background notifications drained, got %#v", again)
	}
}

func TestAgentSideCarCollectorDrainsTeamMessageSideCar(t *testing.T) {
	service, parentID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	parent, err := service.Get(context.Background(), parentID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	receiver, err := service.createSubagentNarrator(context.Background(), parent, "general", "Receiver", "openai:gpt-test", "", ptrString(parent.Cwd), "receiver", false)
	if err != nil {
		t.Fatalf("create receiver: %v", err)
	}
	service.runMu.Lock()
	service.activeRuns = map[string]context.CancelFunc{receiver.ID: func() {}}
	service.runMu.Unlock()
	payload, err := service.teamStatusTool(context.Background(), parentID, map[string]any{"action": "broadcast", "message": "team ping"})
	if err != nil {
		t.Fatalf("broadcast team message: %v", err)
	}
	if payload["sent"] != 1 {
		t.Fatalf("expected one team message target, got %#v", payload)
	}
	collector := service.agentSideCarCollector(receiver.ID)
	sideCars := collector(context.Background(), agentbasic.SideCarRequest{Phase: "after_tools"})
	if len(sideCars) != 1 || sideCars[0].Source != "team_message" || !strings.Contains(sideCars[0].Content, "[Team broadcast from Test narrator (primary)]: team ping") {
		t.Fatalf("expected team message sidecar, got %#v", sideCars)
	}
	if again := collector(context.Background(), agentbasic.SideCarRequest{Phase: "after_tools"}); len(again) != 0 {
		t.Fatalf("expected team inbox drained, got %#v", again)
	}
}

func TestAgentSideCarCollectorInjectsRelaxedPlanReminder(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	if _, err := service.DB.Exec(`UPDATE narrators SET plan_mode = 1, relaxed_plan = 1 WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("enable relaxed plan: %v", err)
	}
	collector := service.agentSideCarCollector(narratorID)
	writeSideCars := collector(context.Background(), agentbasic.SideCarRequest{Phase: "tool_result", ToolName: "Write", ToolUseID: "relaxed-write-1", ToolInput: map[string]any{"file_path": "main.go"}, CompletedToolCount: 1})
	if len(writeSideCars) != 1 || writeSideCars[0].Source != "relaxed_plan" || !strings.Contains(writeSideCars[0].Content, "<relaxed_plan_reminder>") {
		t.Fatalf("expected relaxed plan sidecar for Write, got %#v", writeSideCars)
	}
	readSideCars := collector(context.Background(), agentbasic.SideCarRequest{Phase: "tool_result", ToolName: "Read", ToolUseID: "relaxed-read-1", ToolInput: map[string]any{"file_path": "main.go"}, CompletedToolCount: 1})
	if len(readSideCars) != 0 {
		t.Fatalf("did not expect relaxed plan sidecar for Read, got %#v", readSideCars)
	}
	shellReadOnlySideCars := collector(context.Background(), agentbasic.SideCarRequest{Phase: "tool_result", ToolName: "Bash", ToolUseID: "relaxed-bash-read-1", ToolInput: map[string]any{"command": "ls -la"}, CompletedToolCount: 1})
	if len(shellReadOnlySideCars) != 0 {
		t.Fatalf("did not expect relaxed plan sidecar for read-only shell, got %#v", shellReadOnlySideCars)
	}
	shellWriteSideCars := collector(context.Background(), agentbasic.SideCarRequest{Phase: "tool_result", ToolName: "Bash", ToolUseID: "relaxed-bash-write-1", ToolInput: map[string]any{"command": "touch marker"}, CompletedToolCount: 1})
	if len(shellWriteSideCars) != 1 || shellWriteSideCars[0].Source != "relaxed_plan" {
		t.Fatalf("expected relaxed plan sidecar for write-like shell, got %#v", shellWriteSideCars)
	}
}

func TestShellCommandLooksWriteLikeMatchesCommonTSCases(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "quoted redirection is text", command: `echo "> not a redirect"`, want: false},
		{name: "read-only pipe", command: `grep foo file.txt | wc -l`, want: false},
		{name: "biome check without write", command: `bunx @biomejs/biome check . --write=false`, want: false},
		{name: "bun read-only pm", command: `bun pm ls`, want: false},
		{name: "test comparison redirection token", command: `[[ "$a" > "$b" ]]`, want: false},
		{name: "arithmetic comparison redirection token", command: `(( value > 3 ))`, want: false},
		{name: "redirect write", command: `printf hi > out.txt`, want: true},
		{name: "find delete", command: `find . -name '*.tmp' -delete`, want: true},
		{name: "find exec remove", command: `find . -name '*.tmp' -exec rm {} \;`, want: true},
		{name: "xargs remove", command: `printf '%s\n' a | xargs rm`, want: true},
		{name: "git clone", command: `git clone https://example.com/repo.git repo`, want: true},
		{name: "git reset hard", command: `git reset --hard HEAD`, want: true},
		{name: "biome write", command: `bunx @biomejs/biome check --write .`, want: true},
		{name: "tsc output", command: `tsc --outDir dist`, want: true},
		{name: "tar extract", command: `tar -xf archive.tar`, want: true},
		{name: "npm install", command: `npm install`, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellCommandLooksWriteLike(tc.command); got != tc.want {
				t.Fatalf("shellCommandLooksWriteLike(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}

func TestBackgroundBashBroadcastsLiveOutputBytes(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	call := agentbasic.ToolCall{ID: "bash-bg-live-1", Type: "function"}
	call.Function.Name = "Bash"
	call.Function.Arguments = `{"command":"printf live-output; sleep 1; printf done","run_in_background":true,"description":"BG live"}`
	run, handled, err := service.executeContextualAgentTool(context.Background(), narratorID, narrator, call)
	if err != nil || !handled || !strings.Contains(run.Output, "Background bash task started") {
		t.Fatalf("expected background Bash to start, handled=%v err=%v output=%q", handled, err, run.Output)
	}
	var taskID string
	if err := service.DB.QueryRow(`SELECT id FROM background_tasks WHERE parent_narrator_id = ? AND tool_use_id = 'bash-bg-live-1'`, narratorID).Scan(&taskID); err != nil {
		t.Fatalf("load background bash task: %v", err)
	}
	event := waitForBroadcast(t, events, narratorID, "background_task_output")
	if event["taskId"] != taskID || intNumber(event["outputBytes"]) <= 0 {
		t.Fatalf("unexpected background_task_output event: %#v", event)
	}
	waitForBackgroundTaskStatus(t, service.DB, taskID, "completed")
}

func TestBackgroundBashCancelStopsRunningCommand(t *testing.T) {
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	marker := filepath.Join(cwd, "bg-cancel-marker.txt")
	call := agentbasic.ToolCall{ID: "bash-bg-cancel-1", Type: "function"}
	call.Function.Name = "Bash"
	call.Function.Arguments = `{"command":"(sleep 1; printf should-not-write > bg-cancel-marker.txt) & wait","run_in_background":true,"description":"BG cancel"}`
	run, handled, err := service.executeContextualAgentTool(context.Background(), narratorID, narrator, call)
	if err != nil || !handled || !strings.Contains(run.Output, "Background bash task started") {
		t.Fatalf("expected background Bash to start, handled=%v err=%v output=%q", handled, err, run.Output)
	}
	var taskID string
	if err := service.DB.QueryRow(`SELECT id FROM background_tasks WHERE parent_narrator_id = ? AND tool_use_id = 'bash-bg-cancel-1'`, narratorID).Scan(&taskID); err != nil {
		t.Fatalf("load background bash task: %v", err)
	}
	if !waitForBackgroundBashCancelRegistration(service, taskID) {
		t.Fatalf("background bash cancel handler was not registered for %s", taskID)
	}
	req := httptest.NewRequest(http.MethodPost, "/narrators/"+narratorID+"/background-tasks/"+taskID+"/cancel", nil)
	w := serveNarratorHandlerWithService(service, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected cancel 200, got %d body=%s", w.Code, w.Body.String())
	}
	waitForBackgroundTaskStatus(t, service.DB, taskID, "cancelled")
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("cancelled background bash should not complete marker write, stat err=%v", err)
	}
	awaited, err := service.awaitTool(context.Background(), narratorID, map[string]any{"type": "bash", "id": taskID})
	if err != nil {
		t.Fatalf("await cancelled background bash: %v", err)
	}
	if awaited["status"] != "cancelled" {
		t.Fatalf("expected cancelled await status, got %#v", awaited)
	}
}

func TestBackgroundBashAwaitWaitForTextMatchesLiveOutput(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	call := agentbasic.ToolCall{ID: "bash-bg-wait-text-1", Type: "function"}
	call.Function.Name = "Bash"
	call.Function.Arguments = `{"command":"printf wait-ready; sleep 2; printf done","run_in_background":true,"description":"BG wait text"}`
	run, handled, err := service.executeContextualAgentTool(context.Background(), narratorID, narrator, call)
	if err != nil || !handled || !strings.Contains(run.Output, "Background bash task started") {
		t.Fatalf("expected background Bash to start, handled=%v err=%v output=%q", handled, err, run.Output)
	}
	var taskID string
	if err := service.DB.QueryRow(`SELECT id FROM background_tasks WHERE parent_narrator_id = ? AND tool_use_id = 'bash-bg-wait-text-1'`, narratorID).Scan(&taskID); err != nil {
		t.Fatalf("load background bash task: %v", err)
	}
	started := time.Now()
	awaited, err := service.awaitTool(context.Background(), narratorID, map[string]any{"type": "bash", "id": taskID, "timeout": 1000, "wait_for_text": "wait-ready"})
	if err != nil {
		t.Fatalf("await background bash wait_for_text: %v", err)
	}
	if awaited["matched"] != true || !strings.Contains(stringFromAny(awaited["output"]), "wait-ready") {
		t.Fatalf("expected wait_for_text match in live background bash payload: %#v", awaited)
	}
	if elapsed := time.Since(started); elapsed > 1500*time.Millisecond {
		t.Fatalf("wait_for_text should return before command completion, elapsed=%s payload=%#v", elapsed, awaited)
	}
	service.cancelBackgroundBashTask(taskID)
	waitForBackgroundTaskStatus(t, service.DB, taskID, "failed")
}

func TestBackgroundBashRunInBackgroundProducesAwaitableSideCar(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	call := agentbasic.ToolCall{ID: "bash-bg-1", Type: "function"}
	call.Function.Name = "Bash"
	call.Function.Arguments = `{"command":"printf bgdone","run_in_background":true,"description":"BG smoke"}`
	run, handled, err := service.executeContextualAgentTool(context.Background(), narratorID, narrator, call)
	if err != nil || !handled {
		t.Fatalf("expected background Bash to be handled, handled=%v err=%v", handled, err)
	}
	if !strings.Contains(run.Output, "Background bash task started") || strings.Contains(run.Output, "not supported") {
		t.Fatalf("unexpected background Bash start output: %q", run.Output)
	}
	var taskID, taskType string
	if err := service.DB.QueryRow(`SELECT id, type FROM background_tasks WHERE parent_narrator_id = ? AND tool_use_id = 'bash-bg-1'`, narratorID).Scan(&taskID, &taskType); err != nil {
		t.Fatalf("load background bash task: %v", err)
	}
	if taskType != "bash" {
		t.Fatalf("background task type = %q", taskType)
	}
	waitForBackgroundTaskStatus(t, service.DB, taskID, "completed")
	awaited, err := service.awaitTool(context.Background(), narratorID, map[string]any{"type": "bash", "id": taskID, "timeout": 3000})
	if err != nil {
		t.Fatalf("await background bash: %v", err)
	}
	if awaited["status"] != "completed" || !strings.Contains(stringFromAny(awaited["output"]), "bgdone") {
		t.Fatalf("unexpected awaited background bash payload: %#v", awaited)
	}
	collector := service.agentSideCarCollector(narratorID)
	sideCars := collector(context.Background(), agentbasic.SideCarRequest{Phase: "after_tools"})
	if len(sideCars) != 1 || sideCars[0].Source != "bg_bash" || !strings.Contains(sideCars[0].Content, "Background bash") || !strings.Contains(sideCars[0].Content, "bgdone") {
		t.Fatalf("expected bg_bash sidecar with result preview, got %#v", sideCars)
	}
}

func TestRunBasicAgentInjectsTodoReminderSideCarIntoNextToolTurn(t *testing.T) {
	var sawTodoReminder bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			sawTodoReminder = requestToolResultContains(body, "todo-read-4", "<todo_reminder>") && requestToolResultContains(body, "todo-read-4", "Check parity")
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "done"}}}})
			return
		}
		toolCalls := []map[string]any{}
		for i := 1; i <= 4; i++ {
			name := fmt.Sprintf("todo-read-%d", i)
			args := fmt.Sprintf(`{"file_path":"todo-%d.txt"}`, i)
			toolCalls = append(toolCalls, map[string]any{"id": name, "type": "function", "function": map[string]any{"name": "Read", "arguments": args}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": toolCalls}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, target TEXT, source TEXT, content TEXT, order_index INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create narrator_sidecars: %v", err)
	}
	for i := 1; i <= 4; i++ {
		if err := os.WriteFile(filepath.Join(cwd, fmt.Sprintf("todo-%d.txt", i)), []byte("content"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	if _, err := service.DB.Exec(`UPDATE narrators SET todos_json = ? WHERE id = ?`, `[{"id":"p1","content":"Check parity","status":"pending","priority":"high"}]`, narratorID); err != nil {
		t.Fatalf("seed todos: %v", err)
	}

	service.runBasicAgent(context.Background(), narratorID)
	if !sawTodoReminder {
		t.Fatalf("expected fourth tool result request to include todo reminder sidecar")
	}
	var rows int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_sidecars WHERE narrator_id = ? AND tool_use_id = 'todo-read-4' AND source = 'todo_reminder' AND content LIKE '%Check parity%'`, narratorID).Scan(&rows); err != nil {
		t.Fatalf("count todo sidecars: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected persisted todo reminder sidecar, got %d", rows)
	}
}

func TestRecordedToolResultEventPersistsSideCars(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	if _, err := service.DB.Exec(`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, target TEXT, source TEXT, content TEXT, order_index INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create narrator_sidecars: %v", err)
	}
	msg, err := service.addMessageWithBlocks(context.Background(), narratorID, "assistant", "", []map[string]any{{"type": "tool_use", "id": "recorded-tool-1", "name": "Write", "input": map[string]any{"file_path": "a.txt", "content": "x"}}}, nil)
	if err != nil {
		t.Fatalf("insert assistant draft: %v", err)
	}
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator, draftMessageID: msg.ID}
	if err := service.handleAgentEvent(context.Background(), state, agentbasic.AgentEvent{Type: "tool_result", ToolUseID: "recorded-tool-1", ToolName: "Write", Output: "ok", ToolRun: &agentbasic.ToolRun{ID: "recorded-tool-1", Name: "Write", Output: "ok", Status: "completed", Recorded: true}, SideCars: []agentbasic.AgentSideCar{{Target: "tool_result", Source: "relaxed_plan", Content: "plan notice"}}}); err != nil {
		t.Fatalf("handle recorded tool result: %v", err)
	}
	var rows int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_sidecars WHERE narrator_id = ? AND message_id = ? AND tool_use_id = 'recorded-tool-1' AND target = 'tool_result' AND source = 'relaxed_plan'`, narratorID, msg.ID).Scan(&rows); err != nil {
		t.Fatalf("count recorded sidecars: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected recorded tool sidecar persisted, got %d", rows)
	}
}

func TestMessagesHydrateNarratorSideCars(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	if _, err := service.DB.Exec(`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, target TEXT, source TEXT, content TEXT, order_index INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create narrator_sidecars: %v", err)
	}
	msg, err := service.addMessageWithBlocks(context.Background(), narratorID, "assistant", "read", []map[string]any{{"type": "tool_use", "id": "read-sidecar-1", "name": "Read", "input": map[string]any{"file_path": "README.md"}}}, nil)
	if err != nil {
		t.Fatalf("insert assistant tool message: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES ('tc-sidecar-1', ?, ?, 'read-sidecar-1', 'Read', '{"file_path":"README.md"}', '{"output":"read output"}', 'success', ?)`, narratorID, msg.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert tool call: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO narrator_sidecars (id, narrator_id, message_id, tool_use_id, target, source, content, order_index, created_at) VALUES ('sidecar-1', ?, ?, 'read-sidecar-1', 'tool_result', 'test', 'sidecar detail', 0, ?)`, narratorID, msg.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert sidecar: %v", err)
	}
	messages, err := service.Messages(context.Background(), narratorID, 10)
	if err != nil {
		t.Fatalf("load messages: %v", err)
	}
	assistantMsg := messages[len(messages)-1]
	if len(assistantMsg.SideCars) != 1 || assistantMsg.SideCars[0].Content != "sidecar detail" {
		t.Fatalf("expected message sidecar hydration, got %#v", messages)
	}
	if len(assistantMsg.ToolCalls) != 1 || len(assistantMsg.ToolCalls[0].SideCars) != 1 || assistantMsg.ToolCalls[0].SideCars[0].Source != "test" {
		t.Fatalf("expected tool sidecar hydration, got %#v", assistantMsg.ToolCalls)
	}
	blocks, ok := assistantMsg.ContentJSON.([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("unexpected content blocks: %#v", messages[0].ContentJSON)
	}
	block, _ := blocks[0].(map[string]any)
	if sideCars, ok := block["sideCars"].([]SideCarRecord); !ok || len(sideCars) != 1 || sideCars[0].Content != "sidecar detail" {
		t.Fatalf("expected content_json tool_use sideCars, got %#v", block["sideCars"])
	}
}

func TestAgentHookExecutionBroadcastsTSCompatibleTelemetry(t *testing.T) {
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	input := map[string]any{
		"hook_event_name": "PreToolUse",
		"narrator_id":     narratorID,
		"cwd":             cwd,
		"tool_name":       "Write",
		"tool_use_id":     "hook-telemetry-tool",
	}
	hook := agentToolHook{ID: "hook-telemetry", Event: "PreToolUse", Type: "command", Command: sql.NullString{String: "printf telemetry-ok", Valid: true}, Timeout: 5}
	result := service.executeAgentHook(context.Background(), hook, input, cwd)
	if result.Outcome != "success" || result.Output != "telemetry-ok" {
		t.Fatalf("unexpected hook result: %#v", result)
	}
	event := findBroadcast(events(), narratorID, "hook:executed")
	if event == nil {
		t.Fatalf("expected hook:executed broadcast, got %#v", events())
	}
	if event["hookId"] != "hook-telemetry" || event["event"] != "PreToolUse" || event["hookType"] != "command" || event["outcome"] != "success" || event["narratorId"] != narratorID {
		t.Fatalf("unexpected hook telemetry payload: %#v", event)
	}
	if _, ok := event["durationMs"].(float64); !ok {
		t.Fatalf("expected numeric durationMs in hook telemetry payload: %#v", event)
	}
}

func TestRunBasicAgentPreToolUseCommandHookBlocksTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "blocked"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "hook-write-1", "type": "function", "function": map[string]any{"name": "Write", "arguments": `{"file_path":"blocked.txt","content":"should not be written"}`}}}}}}})
	}))
	defer server.Close()
	cwd := t.TempDir()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`UPDATE narrators SET cwd = ?, permission_mode = 'bypassPermissions', danger_reflection_override = 'off' WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("configure narrator: %v", err)
	}
	if _, err := service.DB.Exec(`CREATE TABLE hooks (id TEXT PRIMARY KEY, project_id TEXT, event TEXT, matcher TEXT, type TEXT, command TEXT, url TEXT, headers TEXT, timeout INTEGER, enabled INTEGER, sort_order INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create hooks table: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO hooks (id, event, matcher, type, command, timeout, enabled, sort_order, created_at) VALUES ('hook-block-write', 'PreToolUse', 'Write', 'command', 'printf "blocked by hook" >&2; exit 2', 5, 1, 0, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert hook: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)
	if _, err := os.Stat(filepath.Join(cwd, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("PreToolUse hook should block Write before file mutation, stat err=%v", err)
	}
	var status, outputRaw string
	if err := service.DB.QueryRow(`SELECT status, COALESCE(output_json, '') FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'hook-write-1' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&status, &outputRaw); err != nil {
		t.Fatalf("load blocked hook tool call: %v", err)
	}
	if status != "fail" || !strings.Contains(outputRaw, "blocked by hook") {
		t.Fatalf("expected blocked hook fail output, status=%s output=%s", status, outputRaw)
	}
}

func TestRunBasicAgentPreToolUseRunsBeforeMalformedInputGuard(t *testing.T) {
	rawToolInput := `{"file_path":"malformed.txt","content":"unterminated"`
	encodedArgs, _ := json.Marshal(map[string]string{"_raw": rawToolInput})
	malformedArgs := string(encodedArgs)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "blocked malformed"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "hook-malformed-write", "type": "function", "function": map[string]any{"name": "Write", "arguments": malformedArgs}}}}}}})
	}))
	defer server.Close()
	cwd := t.TempDir()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`UPDATE narrators SET cwd = ?, permission_mode = 'bypassPermissions', danger_reflection_override = 'off' WHERE id = ?`, cwd, narratorID); err != nil {
		t.Fatalf("configure narrator: %v", err)
	}
	if _, err := service.DB.Exec(`CREATE TABLE hooks (id TEXT PRIMARY KEY, project_id TEXT, event TEXT, matcher TEXT, type TEXT, command TEXT, url TEXT, headers TEXT, timeout INTEGER, enabled INTEGER, sort_order INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create hooks table: %v", err)
	}
	hookPayload := filepath.Join(cwd, "pre-hook-malformed.json")
	command := fmt.Sprintf("cat > %q; printf 'hook saw malformed input' >&2; exit 2", hookPayload)
	if _, err := service.DB.Exec(`INSERT INTO hooks (id, event, matcher, type, command, timeout, enabled, sort_order, created_at) VALUES ('hook-pre-malformed', 'PreToolUse', 'Write', 'command', ?, 5, 1, 0, '2026-01-01T00:00:00Z')`, command); err != nil {
		t.Fatalf("insert hook: %v", err)
	}

	service.runBasicAgent(context.Background(), narratorID)
	if _, err := os.Stat(filepath.Join(cwd, "malformed.txt")); !os.IsNotExist(err) {
		t.Fatalf("malformed Write must not create a file, stat err=%v", err)
	}
	payloadRaw, err := os.ReadFile(hookPayload)
	if err != nil {
		t.Fatalf("PreToolUse hook should receive malformed tool input before guard: %v", err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("decode hook payload: %v raw=%s", err, string(payloadRaw))
	}
	input, _ := payload["tool_input"].(map[string]any)
	if payload["hook_event_name"] != "PreToolUse" || payload["tool_name"] != "Write" || input["_raw"] != rawToolInput {
		t.Fatalf("expected malformed raw input to be visible to PreToolUse, payload=%#v", payload)
	}
	var status, outputRaw string
	if err := service.DB.QueryRow(`SELECT status, COALESCE(output_json, '') FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'hook-malformed-write' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&status, &outputRaw); err != nil {
		t.Fatalf("load malformed hook tool call: %v", err)
	}
	if status != "fail" || !strings.Contains(outputRaw, "hook saw malformed input") || strings.Contains(outputRaw, "malformed or truncated") {
		t.Fatalf("expected PreToolUse block to win before malformed guard, status=%s output=%s", status, outputRaw)
	}
}

func TestPreToolUseHTTPHookBlockDecision(t *testing.T) {
	var sawToolName string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		sawToolName = stringFromAny(input["tool_name"])
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"decision": "block", "reason": "blocked by webhook"})
	}))
	defer server.Close()
	result := executeHTTPPreToolHook(context.Background(), server.URL, sql.NullString{}, map[string]any{"tool_name": "Bash", "tool_use_id": "hook-http-1"})
	if result.Outcome != "blocked" || result.Reason != "blocked by webhook" || sawToolName != "Bash" {
		t.Fatalf("unexpected HTTP hook result=%#v sawToolName=%q", result, sawToolName)
	}
}

func TestAgentHTTPHookHonorsHeadersAndTimeoutTelemetry(t *testing.T) {
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	headerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Hook-Token"); got != "secret-token" {
			http.Error(w, "missing hook token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer headerServer.Close()
	headersRaw, _ := json.Marshal(map[string]string{"X-Hook-Token": "secret-token"})
	successHook := agentToolHook{ID: "hook-http-headers", Event: "PreToolUse", Type: "http", URL: sql.NullString{String: headerServer.URL, Valid: true}, Headers: sql.NullString{String: string(headersRaw), Valid: true}, Timeout: 5}
	input := map[string]any{"hook_event_name": "PreToolUse", "narrator_id": narratorID, "cwd": cwd, "tool_name": "Bash", "tool_use_id": "hook-http-headers-tool"}
	result := service.executeAgentHook(context.Background(), successHook, input, cwd)
	if result.Outcome != "success" {
		t.Fatalf("expected header-authenticated hook success, got %#v", result)
	}
	successEvent := waitForBroadcast(t, events, narratorID, "hook:executed")
	if successEvent["hookId"] != "hook-http-headers" || successEvent["outcome"] != "success" {
		t.Fatalf("unexpected header hook telemetry: %#v", successEvent)
	}

	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer slowServer.Close()
	timeoutHook := agentToolHook{ID: "hook-http-timeout", Event: "PostToolUse", Type: "http", URL: sql.NullString{String: slowServer.URL, Valid: true}, Timeout: 1}
	timeoutInput := map[string]any{"hook_event_name": "PostToolUse", "narrator_id": narratorID, "cwd": cwd, "tool_name": "Bash", "tool_use_id": "hook-http-timeout-tool"}
	result = service.executeAgentHook(context.Background(), timeoutHook, timeoutInput, cwd)
	if result.Outcome != "error" || result.Reason == "" {
		t.Fatalf("expected timeout hook to fail open with error result, got %#v", result)
	}
	timeoutEvent := waitForHookBroadcast(t, events, narratorID, "hook-http-timeout")
	if timeoutEvent["event"] != "PostToolUse" || timeoutEvent["hookType"] != "http" || timeoutEvent["outcome"] != "error" {
		t.Fatalf("unexpected timeout hook telemetry: %#v", timeoutEvent)
	}
}

func TestRunBasicAgentPostToolUseCommandHookReceivesPayloadAndDoesNotBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "done after post hook"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "post-hook-write-1", "type": "function", "function": map[string]any{"name": "Write", "arguments": `{"file_path":"post-hook.txt","content":"post hook data"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	if _, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'bypassPermissions', danger_reflection_override = 'off' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("configure narrator: %v", err)
	}
	if _, err := service.DB.Exec(`CREATE TABLE hooks (id TEXT PRIMARY KEY, project_id TEXT, event TEXT, matcher TEXT, type TEXT, command TEXT, url TEXT, headers TEXT, timeout INTEGER, enabled INTEGER, sort_order INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create hooks table: %v", err)
	}
	hookOutput := filepath.Join(cwd, "post-hook-payload.json")
	command := fmt.Sprintf("cat > %q; exit 2", hookOutput)
	if _, err := service.DB.Exec(`INSERT INTO hooks (id, event, matcher, type, command, timeout, enabled, sort_order, created_at) VALUES ('hook-post-write', 'PostToolUse', 'Write', 'command', ?, 5, 1, 0, '2026-01-01T00:00:00Z')`, command); err != nil {
		t.Fatalf("insert post hook: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)
	waitForAssistantText(t, service, narratorID, "done after post hook")
	waitForFileContains(t, hookOutput, `"hook_event_name":"PostToolUse"`)
	data, err := os.ReadFile(filepath.Join(cwd, "post-hook.txt"))
	if err != nil || string(data) != "post hook data" {
		t.Fatalf("PostToolUse hook should not block Write, data=%q err=%v", string(data), err)
	}
	payloadRaw, err := os.ReadFile(hookOutput)
	if err != nil {
		t.Fatalf("read post hook payload: %v", err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("decode post hook payload: %v raw=%s", err, string(payloadRaw))
	}
	if payload["hook_event_name"] != "PostToolUse" || payload["tool_name"] != "Write" || payload["tool_use_id"] != "post-hook-write-1" || payload["tool_is_error"] != false {
		t.Fatalf("unexpected post hook payload: %#v", payload)
	}
	if !strings.Contains(stringFromAny(payload["tool_output"]), "post-hook.txt") {
		t.Fatalf("expected bounded tool_output in post hook payload, got %#v", payload["tool_output"])
	}
	input, _ := payload["tool_input"].(map[string]any)
	if input["file_path"] != "post-hook.txt" || input["content"] != "post hook data" {
		t.Fatalf("unexpected post hook tool_input: %#v", input)
	}
	var status string
	if err := service.DB.QueryRow(`SELECT status FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'post-hook-write-1' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&status); err != nil {
		t.Fatalf("load post hook tool status: %v", err)
	}
	if status != "success" {
		t.Fatalf("PostToolUse hook exit 2 should not change tool result status, got %s", status)
	}
	event := waitForBroadcast(t, events, narratorID, "hook:executed")
	if event["hookId"] != "hook-post-write" || event["event"] != "PostToolUse" || event["hookType"] != "command" || event["outcome"] != "blocked" || event["narratorId"] != narratorID {
		t.Fatalf("unexpected post hook telemetry payload: %#v", event)
	}
}

func TestPostToolUseHTTPHookReceivesErrorPayload(t *testing.T) {
	seen := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		seen <- input
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"decision": "block", "reason": "post hooks are non-blocking"})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	if _, err := service.DB.Exec(`CREATE TABLE hooks (id TEXT PRIMARY KEY, project_id TEXT, event TEXT, matcher TEXT, type TEXT, command TEXT, url TEXT, headers TEXT, timeout INTEGER, enabled INTEGER, sort_order INTEGER, created_at TEXT)`); err != nil {
		t.Fatalf("create hooks table: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO hooks (id, event, matcher, type, url, timeout, enabled, sort_order, created_at) VALUES ('hook-post-http', 'PostToolUse', 'Bash', 'http', ?, 5, 1, 0, '2026-01-01T00:00:00Z')`, server.URL); err != nil {
		t.Fatalf("insert post http hook: %v", err)
	}
	call := agentbasic.ToolCall{ID: "post-hook-bash-1"}
	call.Function.Name = "Bash"
	call.Function.Arguments = `{"command":"exit 7"}`
	run := agentbasic.ToolRun{ID: call.ID, Name: "Bash", Arguments: call.Function.Arguments, Output: "command failed", Status: "error", Error: "command failed"}
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	service.runPostToolUseHooks(context.Background(), narratorID, narrator, cwd, call, run)
	var payload map[string]any
	select {
	case payload = <-seen:
	case <-time.After(2 * time.Second):
		t.Fatalf("PostToolUse HTTP hook did not run")
	}
	if payload["hook_event_name"] != "PostToolUse" || payload["tool_name"] != "Bash" || payload["tool_use_id"] != "post-hook-bash-1" || payload["tool_is_error"] != true {
		t.Fatalf("unexpected post HTTP hook payload: %#v", payload)
	}
	if !strings.Contains(stringFromAny(payload["tool_output"]), "command failed") {
		t.Fatalf("expected tool output in post HTTP hook payload: %#v", payload)
	}
}

func TestBroadcastGitStatusAfterFileMutatingTool(t *testing.T) {
	repo := t.TempDir()
	runGitNarratorAgent(t, repo, "init", "-b", "main")
	runGitNarratorAgent(t, repo, "config", "user.email", "test@example.com")
	runGitNarratorAgent(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write tracked fixture: %v", err)
	}
	runGitNarratorAgent(t, repo, "add", "tracked.txt")
	runGitNarratorAgent(t, repo, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("before\nafter\n"), 0o644); err != nil {
		t.Fatalf("mutate tracked fixture: %v", err)
	}
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	if _, err := service.DB.Exec(`CREATE TABLE chapters (id TEXT PRIMARY KEY, worktree_path TEXT, base_branch TEXT)`); err != nil {
		t.Fatalf("create chapters table: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO chapters (id, worktree_path, base_branch) VALUES ('chapter-git-track', ?, 'main')`, repo); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := service.DB.Exec(`UPDATE narrators SET chapter_id = ?, cwd = ? WHERE id = ?`, "chapter-git-track", repo, narratorID); err != nil {
		t.Fatalf("bind narrator to chapter: %v", err)
	}
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	service.broadcastGitStatusAfterTool(context.Background(), narratorID, narrator, "Write", "tool-git-track-1")
	// git_status broadcasts are throttled (800ms trailing); poll until it fires.
	event := waitForBroadcast(t, events, narratorID, "git_status")
	if event == nil || event["chapterId"] != "chapter-git-track" || event["toolUseId"] != "tool-git-track-1" {
		t.Fatalf("expected git_status broadcast for mutating tool, got %#v events=%#v", event, events())
	}
	status, _ := event["status"].(map[string]any)
	dirtyCount := intNumber(status["staged"]) + intNumber(status["unstaged"]) + intNumber(status["untracked"])
	if status == nil || status["hasChanges"] != true || dirtyCount == 0 {
		t.Fatalf("expected git_status dirty payload, got %#v", event)
	}
}

func TestBashToolSnapshotsGitStatusDiffFiles(t *testing.T) {
	repo := t.TempDir()
	runGitNarratorAgent(t, repo, "init", "-b", "main")
	runGitNarratorAgent(t, repo, "config", "user.email", "test@example.com")
	runGitNarratorAgent(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write tracked fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deleted.txt"), []byte("remove me\n"), 0o644); err != nil {
		t.Fatalf("write deleted fixture: %v", err)
	}
	runGitNarratorAgent(t, repo, "add", "tracked.txt", "deleted.txt")

	runGitNarratorAgent(t, repo, "commit", "-m", "initial")

	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	if _, err := service.DB.Exec(`UPDATE narrators SET cwd = ?, permission_mode = 'trust-all', danger_reflection_override = 'off' WHERE id = ?`, repo, narratorID); err != nil {
		t.Fatalf("configure narrator repo cwd: %v", err)
	}
	assistant, err := service.addMessageWithBlocks(context.Background(), narratorID, "assistant", "", []map[string]any{{"type": "tool_use", "id": "bash-snapshot-1", "name": "Bash", "input": map[string]any{"command": "printf 'after\\n' > tracked.txt && printf 'new\\n' > created.txt && rm deleted.txt"}}}, nil)
	if err != nil {
		t.Fatalf("insert assistant message: %v", err)
	}
	argsJSON, _ := json.Marshal(map[string]any{"command": "printf 'after\\n' > tracked.txt && printf 'new\\n' > created.txt && rm deleted.txt"})
	call := agentbasic.ToolCall{ID: "bash-snapshot-1", Type: "function"}
	call.Function.Name = "Bash"
	call.Function.Arguments = string(argsJSON)
	fileChange := prepareToolFileChange(context.Background(), repo, call)
	output := agentbasic.ExecuteTool(context.Background(), repo, "Bash", string(argsJSON))
	completeToolFileChange(context.Background(), repo, fileChange)
	run := agentbasic.ToolRun{ID: "bash-snapshot-1", Name: "Bash", Arguments: string(argsJSON), Output: output, Status: "completed", FileChange: fileChange}
	if err := service.persistToolRun(context.Background(), narratorID, assistant.ID, run); err != nil {
		t.Fatalf("persist Bash tool run: %v", err)
	}

	snapshots, err := querySnapshotOriginals(context.Background(), service.DB, narratorID)
	if err != nil {
		t.Fatalf("query snapshots: %v", err)
	}
	trackedPath := filepath.Join(repo, "tracked.txt")
	createdPath := filepath.Join(repo, "created.txt")
	deletedPath := filepath.Join(repo, "deleted.txt")
	trackedOriginal, ok := snapshots[trackedPath]
	if !ok || trackedOriginal == nil || *trackedOriginal != "before\n" {
		t.Fatalf("tracked Bash snapshot mismatch for %s: %#v all=%#v", trackedPath, trackedOriginal, snapshots)
	}
	createdOriginal, ok := snapshots[createdPath]
	if !ok || createdOriginal != nil {
		t.Fatalf("created Bash snapshot mismatch for %s: %#v all=%#v", createdPath, createdOriginal, snapshots)
	}
	deletedOriginal, ok := snapshots[deletedPath]
	if !ok || deletedOriginal == nil || *deletedOriginal != "remove me\n" {
		t.Fatalf("deleted Bash snapshot mismatch for %s: %#v all=%#v", deletedPath, deletedOriginal, snapshots)
	}
	states, err := rebuildFileStatesExcluding(context.Background(), service.DB, narratorID, repo, []string{trackedPath, createdPath, deletedPath}, nil, nil)
	if err != nil {
		t.Fatalf("rebuild Bash states: %v", err)
	}
	if state := states[trackedPath]; state == nil || *state != "after\n" {
		t.Fatalf("tracked rebuilt state mismatch: %#v", state)
	}
	if state := states[createdPath]; state == nil || *state != "new\n" {
		t.Fatalf("created rebuilt state mismatch: %#v", state)
	}
	if state := states[deletedPath]; state != nil {
		t.Fatalf("deleted rebuilt state should be nil after Bash rm, got %#v", state)
	}
}

func TestRunBasicAgentRejectsMalformedAndEmptyWriteInputsBeforeMutation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "guarded"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{
			{"id": "raw-write-1", "type": "function", "function": map[string]any{"name": "Write", "arguments": `{"_raw":"{\"file_path\":","file_path":"danger.txt","content":"mutated"}`}},
			{"id": "empty-write-1", "type": "function", "function": map[string]any{"name": "Write", "arguments": `{}`}},
		}}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	_, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'trust-all', danger_reflection_override = 'off' WHERE id = ?`, narratorID)
	if err != nil {
		t.Fatalf("set trust-all: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)
	if _, err := os.Stat(filepath.Join(cwd, "danger.txt")); !os.IsNotExist(err) {
		t.Fatalf("malformed Write input should not create danger.txt, stat err=%v", err)
	}
	rows, err := service.DB.Query(`SELECT tool_use_id, status, COALESCE(error_message, ''), output_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id IN ('raw-write-1', 'empty-write-1') ORDER BY tool_use_id`, narratorID)
	if err != nil {
		t.Fatalf("load guarded tool calls: %v", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var toolUseID, status, errorMessage, outputRaw string
		if err := rows.Scan(&toolUseID, &status, &errorMessage, &outputRaw); err != nil {
			t.Fatalf("scan guarded tool call: %v", err)
		}
		seen[toolUseID] = true
		if status != "fail" || errorMessage == "" {
			t.Fatalf("guarded tool call %s should fail with error, status=%s error=%q output=%s", toolUseID, status, errorMessage, outputRaw)
		}
		if toolUseID == "raw-write-1" && !strings.Contains(outputRaw, "malformed or truncated") {
			t.Fatalf("raw malformed guard output mismatch: %s", outputRaw)
		}
		if toolUseID == "empty-write-1" && !strings.Contains(outputRaw, "empty input") {
			t.Fatalf("empty input guard output mismatch: %s", outputRaw)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate guarded tool calls: %v", err)
	}
	if !seen["raw-write-1"] || !seen["empty-write-1"] {
		t.Fatalf("expected both guarded tool calls, got %#v", seen)
	}
}

func TestRunBasicAgentPlanModeRedirectsMarkdownWriteToPlanFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "plan written"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "plan-write-1", "type": "function", "function": map[string]any{"name": "Write", "arguments": `{"file_path":"IMPLEMENTATION.md","content":"# Plan\n\nWrite the implementation plan here."}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	planFileID := "plan1234"
	_, err := service.DB.Exec(`UPDATE narrators SET plan_mode = 1, traits = '["plan"]', relaxed_plan = 0, permission_mode = 'readOnly', plan_file_id = ?, danger_reflection_override = 'off' WHERE id = ?`, planFileID, narratorID)
	if err != nil {
		t.Fatalf("enable plan mode: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)
	if _, err := os.Stat(filepath.Join(cwd, "IMPLEMENTATION.md")); !os.IsNotExist(err) {
		t.Fatalf("plan-mode markdown redirect should not write original path, stat err=%v", err)
	}
	planPath := filepath.Join(cwd, ".narrafork", "plan-"+planFileID+".md")
	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("expected redirected plan file: %v", err)
	}
	if !strings.Contains(string(data), "Write the implementation plan") {
		t.Fatalf("unexpected plan file contents: %q", string(data))
	}
	var status, inputRaw, outputRaw string
	if err := service.DB.QueryRow(`SELECT status, input_json, output_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'plan-write-1'`, narratorID).Scan(&status, &inputRaw, &outputRaw); err != nil {
		t.Fatalf("load plan write tool call: %v", err)
	}
	if status != "success" || !strings.Contains(inputRaw, `.narrafork/plan-plan1234.md`) || !strings.Contains(outputRaw, "[Plan Mode] Note: Your write target was redirected") {
		t.Fatalf("unexpected plan redirect tool call status=%s input=%s output=%s", status, inputRaw, outputRaw)
	}
}

func TestRunBasicAgentRecordsToolCallAndResult(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		count := requests
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "read-1", "type": "function", "function": map[string]any{"name": "Read", "arguments": `{"file_path":"note.txt"}`}}}}}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "final text"}}},
		})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if err := os.WriteFile(filepath.Join(cwd, "note.txt"), []byte("tool content"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	service.runBasicAgent(context.Background(), narratorID)
	var status, outputRaw string
	if err := service.DB.QueryRow(`SELECT status, output_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'read-1'`, narratorID).Scan(&status, &outputRaw); err != nil {
		t.Fatalf("load tool call: %v", err)
	}
	if status != "success" || !strings.Contains(outputRaw, "tool content") {
		t.Fatalf("unexpected tool call status=%s output=%s", status, outputRaw)
	}
	rows, err := service.DB.Query(`SELECT COALESCE(content_text, ''), content_json FROM narrator_messages WHERE narrator_id = ? AND role = 'assistant' ORDER BY created_at ASC`, narratorID)
	if err != nil {
		t.Fatalf("load assistant rows: %v", err)
	}
	defer rows.Close()
	assistantTexts := []string{}
	assistantJSON := []string{}
	for rows.Next() {
		var text, contentJSON string
		if err := rows.Scan(&text, &contentJSON); err != nil {
			t.Fatalf("scan assistant row: %v", err)
		}
		assistantTexts = append(assistantTexts, text)
		assistantJSON = append(assistantJSON, contentJSON)
	}
	if len(assistantTexts) != 2 || !strings.Contains(assistantJSON[0], `"tool_use"`) || assistantTexts[1] != "final text" {
		t.Fatalf("unexpected assistant rows texts=%#v json=%#v", assistantTexts, assistantJSON)
	}
}

func TestRunBasicAgentPermissionFlowStillWorksWithEventLoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		messages, _ := body["messages"].([]any)
		w.Header().Set("Content-Type", "application/json")
		if len(messages) < 4 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "write-1", "type": "function", "function": map[string]any{"name": "Write", "arguments": `{"file_path":"out.txt","content":"approved"}`}}}}}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "done"}}},
		})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`UPDATE narrators SET danger_reflection_override = 'off' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("disable danger reflection for permission loop test: %v", err)
	}
	done := make(chan struct{})
	go func() {
		service.runBasicAgent(context.Background(), narratorID)
		close(done)
	}()
	waitForToolStatus(t, service, narratorID, "write-1", "pending")
	_, err := service.DB.Exec(`UPDATE narrator_tool_calls SET status = 'running', permission_decided_by = 'tester', permission_decided_at = ? WHERE narrator_id = ? AND tool_use_id = 'write-1'`, time.Now().UTC().Format(time.RFC3339Nano), narratorID)
	if err != nil {
		t.Fatalf("approve permission: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runBasicAgent did not finish")
	}
	waitForToolStatus(t, service, narratorID, "write-1", "success")
	data, err := os.ReadFile(filepath.Join(cwd, "out.txt"))
	if err != nil || string(data) != "approved" {
		t.Fatalf("expected approved write, data=%q err=%v", string(data), err)
	}
	var snapshotCount int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_file_snapshots WHERE narrator_id = ?`, narratorID).Scan(&snapshotCount); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if snapshotCount == 0 {
		t.Fatalf("expected file snapshot for approved write")
	}
}

func TestRunBasicAgentPlanModeApprovalEnablesRelaxedPlanAndExecutesWrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "implementation written"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "plan-impl-write-1", "type": "function", "function": map[string]any{"name": "Write", "arguments": `{"file_path":"src/main.go","content":"package main\n"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	planFileID := "planimpl"
	_, err := service.DB.Exec(`UPDATE narrators SET plan_mode = 1, traits = '["plan"]', relaxed_plan = 0, permission_mode = 'readOnly', plan_file_id = ?, danger_reflection_override = 'off' WHERE id = ?`, planFileID, narratorID)
	if err != nil {
		t.Fatalf("enable plan mode: %v", err)
	}
	done := make(chan struct{})
	go func() {
		service.runBasicAgent(context.Background(), narratorID)
		close(done)
	}()
	waitForToolStatus(t, service, narratorID, "plan-impl-write-1", "pending")
	var reason string
	if err := service.DB.QueryRow(`SELECT COALESCE(permission_decision_reason, '') FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'plan-impl-write-1' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&reason); err != nil {
		t.Fatalf("load plan soft-deny reason: %v", err)
	}
	if !strings.Contains(reason, ".narrafork/plan-planimpl.md") {
		t.Fatalf("expected plan soft-deny reason with plan file hint, got %q", reason)
	}
	_, err = service.DB.Exec(`UPDATE narrator_tool_calls SET status = 'running', permission_decided_by = 'tester', permission_decided_at = ? WHERE narrator_id = ? AND tool_use_id = 'plan-impl-write-1'`, time.Now().UTC().Format(time.RFC3339Nano), narratorID)
	if err != nil {
		t.Fatalf("approve plan soft-deny permission: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runBasicAgent did not finish after plan soft-deny approval")
	}
	data, err := os.ReadFile(filepath.Join(cwd, "src", "main.go"))
	if err != nil || string(data) != "package main\n" {
		t.Fatalf("expected approved implementation write, data=%q err=%v", string(data), err)
	}
	var relaxed int
	if err := service.DB.QueryRow(`SELECT relaxed_plan FROM narrators WHERE id = ?`, narratorID).Scan(&relaxed); err != nil {
		t.Fatalf("load relaxed plan state: %v", err)
	}
	if relaxed != 1 {
		t.Fatalf("expected plan soft-deny approval to enable relaxed_plan, got %d", relaxed)
	}
}

func TestRunBasicAgentBypassPermissionModeReprocessesPendingTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if requestHasToolResult(body) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "done"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "reprocess-write-1", "type": "function", "function": map[string]any{"name": "Write", "arguments": `{"file_path":"reprocessed.txt","content":"allowed after bypass"}`}}}}}}})
	}))
	defer server.Close()
	service, narratorID, cwd := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`UPDATE narrators SET danger_reflection_override = 'off' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("disable danger reflection for reprocess test: %v", err)
	}
	done := make(chan struct{})
	go func() {
		service.runBasicAgent(context.Background(), narratorID)
		close(done)
	}()
	waitForToolStatus(t, service, narratorID, "reprocess-write-1", "pending")
	if _, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'bypassPermissions' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("set bypass permission mode: %v", err)
	}
	count, err := service.reprocessPendingPermissionsForBypass(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("reprocess pending permission: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one pending permission to reprocess, got %d", count)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runBasicAgent did not finish after permission reprocess")
	}
	waitForToolStatus(t, service, narratorID, "reprocess-write-1", "success")
	data, err := os.ReadFile(filepath.Join(cwd, "reprocessed.txt"))
	if err != nil || string(data) != "allowed after bypass" {
		t.Fatalf("expected reprocessed write, data=%q err=%v", string(data), err)
	}
	var decidedBy, reason string
	if err := service.DB.QueryRow(`SELECT COALESCE(permission_decided_by, ''), COALESCE(permission_decision_reason, '') FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = 'reprocess-write-1'`, narratorID).Scan(&decidedBy, &reason); err != nil {
		t.Fatalf("load permission reprocess metadata: %v", err)
	}
	if decidedBy != "auto" || !strings.Contains(reason, "reprocessed") {
		t.Fatalf("unexpected reprocess metadata decidedBy=%q reason=%q", decidedBy, reason)
	}
}

func TestSubagentToolCreatesBackgroundNarratorWithEventLoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "child done"}}},
		})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	payload, err := service.executeAgentTool(context.Background(), narratorID, map[string]any{
		"prompt":            "explore this",
		"subagent_type":     "explore",
		"run_in_background": true,
		"description":       "Background explore",
	})
	if err != nil {
		t.Fatalf("executeAgentTool returned error: %v", err)
	}
	childID := strings.TrimSpace(stringFromAny(payload["subagentId"]))
	if childID == "" || payload["background"] != true {
		t.Fatalf("unexpected subagent payload: %#v", payload)
	}
	var typ, subagentType string
	if err := service.DB.QueryRow(`SELECT type, subagent_type FROM narrators WHERE id = ? AND parent_narrator_id = ?`, childID, narratorID).Scan(&typ, &subagentType); err != nil {
		t.Fatalf("load child narrator: %v", err)
	}
	if typ != "subagent" || subagentType != "explore" {
		t.Fatalf("unexpected child narrator typ=%s subagentType=%s", typ, subagentType)
	}
}

func TestSubagentToolForegroundAwaitsCompletionResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "foreground child done"}}},
		})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	payload, err := service.executeAgentTool(context.Background(), narratorID, map[string]any{
		"prompt":        "do foreground work",
		"subagent_type": "general",
		"description":   "Foreground general",
		"timeout":       3000,
	})
	if err != nil {
		t.Fatalf("execute foreground Agent tool: %v", err)
	}
	if payload["foreground"] != true || !strings.Contains(stringFromAny(payload["result"]), "foreground child done") {
		t.Fatalf("unexpected foreground subagent payload: %#v", payload)
	}
}

func waitForBackgroundBashCancelRegistration(service *Service, taskID string) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		service.bgMu.Lock()
		_, ok := service.bgBashCancels[taskID]
		service.bgMu.Unlock()
		if ok {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func waitForBackgroundTaskStatus(t *testing.T, sqlDB *sql.DB, taskID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	lastStatus := "<missing>"
	for time.Now().Before(deadline) {
		var status string
		err := sqlDB.QueryRow(`SELECT status FROM background_tasks WHERE id = ?`, taskID).Scan(&status)
		if err == nil {
			lastStatus = status
			if status == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background task %s status did not reach %s (last=%s)", taskID, want, lastStatus)
}

func waitForToolStatus(t *testing.T, service *Service, narratorID, toolUseID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	lastStatus := "<missing>"
	lastError := ""
	for time.Now().Before(deadline) {
		var status string
		var errorMessage sql.NullString
		err := service.DB.QueryRow(`SELECT status, error_message FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, narratorID, toolUseID).Scan(&status, &errorMessage)
		if err == nil {
			lastStatus = status
			lastError = sqlNullString(errorMessage)
			if status == want {
				return
			}
		} else {
			lastError = err.Error()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tool %s did not reach status %s (last status=%s error=%q)", toolUseID, want, lastStatus, lastError)
}

func waitForAssistantText(t *testing.T, service *Service, narratorID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var matches int
		err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_messages WHERE narrator_id = ? AND role = 'assistant' AND COALESCE(content_text, '') LIKE ?`, narratorID, "%"+want+"%").Scan(&matches)
		if err == nil && matches > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("assistant message did not contain %q", want)
}

func waitForFileContains(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			last = string(data)
			if strings.Contains(last, want) {
				return
			}
		} else {
			last = err.Error()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("file %s did not contain %q (last=%q)", path, want, last)
}

func waitForBroadcast(t *testing.T, events func() []capturedBroadcast, narratorID string, eventType string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if event := findBroadcast(events(), narratorID, eventType); event != nil {
			return event
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for broadcast %s, events=%#v", eventType, events())
	return nil
}

func waitForHookBroadcast(t *testing.T, events func() []capturedBroadcast, narratorID string, hookID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range events() {
			if event.NarratorID == narratorID && event.Payload["type"] == "hook:executed" && event.Payload["hookId"] == hookID {
				return event.Payload
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for hook broadcast %s, events=%#v", hookID, events())
	return nil
}

func waitForNarratorErrorContains(t *testing.T, service *Service, narratorID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		var status, substatus, errorMessage string
		err := service.DB.QueryRow(`SELECT status, substatus, COALESCE(error_message, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &errorMessage)
		if err == nil {
			last = fmt.Sprintf("status=%s substatus=%s error=%s", status, substatus, errorMessage)
			if status == "idle" && substatus == `["error"]` && strings.Contains(errorMessage, want) {
				return
			}
		} else {
			last = err.Error()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("narrator %s did not persist error containing %q (last=%s)", narratorID, want, last)
}

func waitForNarratorStatus(t *testing.T, service *Service, narratorID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		err := service.DB.QueryRow(`SELECT status FROM narrators WHERE id = ?`, narratorID).Scan(&status)
		if err == nil && status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("narrator %s did not reach status %s", narratorID, want)
}

func TestRunBasicAgentCancelMarksRunIdle(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	done := make(chan struct{})
	go func() {
		service.runBasicAgent(context.Background(), narratorID)
		close(done)
	}()
	waitForActiveRun(t, service, narratorID)
	if !service.CancelRun(narratorID) {
		t.Fatalf("expected active run to cancel")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runBasicAgent did not stop after cancel")
	}
	var status string
	if err := service.DB.QueryRow(`SELECT status FROM narrators WHERE id = ?`, narratorID).Scan(&status); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if status != "idle" {
		t.Fatalf("expected idle after cancel, got %s", status)
	}
}

func TestSendToolContinuationPersistsParentToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "continued"}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	parent, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(context.Background(), parent, "general", "Worker", "openai:gpt-test", "", ptrString(parent.Cwd), "worker", false)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	payload, err := service.sendTool(context.Background(), narratorID, map[string]any{"id": child.ID, "message": "continue", "_tool_use_id": "send-tool-1"})
	if err != nil {
		t.Fatalf("send tool: %v", err)
	}
	if payload["ok"] != true {
		t.Fatalf("unexpected send payload: %#v", payload)
	}
	var parentToolUseID string
	if err := service.DB.QueryRow(`SELECT COALESCE(parent_tool_use_id, '') FROM narrator_messages WHERE narrator_id = ? AND role = 'user' AND content_text = 'continue' ORDER BY created_at DESC LIMIT 1`, child.ID).Scan(&parentToolUseID); err != nil {
		t.Fatalf("load child continuation message: %v", err)
	}
	if parentToolUseID != "send-tool-1" {
		t.Fatalf("child continuation parent_tool_use_id=%q", parentToolUseID)
	}
	deadline := time.Now().Add(2 * time.Second)
	for service.isRunActive(child.ID) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
}

func TestSubagentDetachAttachLifecycleReconcilesStaleBackground(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	parent, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	child, err := service.createSubagentNarrator(context.Background(), parent, "general", "Foreground", "openai:gpt-test", "", ptrString(parent.Cwd), "fg", false)
	if err != nil {
		t.Fatalf("create subagent: %v", err)
	}
	if _, err := service.addMessageWithBlocksWithCommandAndParentToolUse(context.Background(), child.ID, "user", "launch", []map[string]any{{"type": "text", "text": "launch"}}, nil, "", "agent-1"); err != nil {
		t.Fatalf("seed child parent tool use: %v", err)
	}
	_, err = service.DB.Exec(`UPDATE narrators SET status = 'working' WHERE id = ?`, child.ID)
	if err != nil {
		t.Fatalf("mark child working: %v", err)
	}
	detached, err := service.DetachSubagent(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("detach subagent: %v", err)
	}
	if detached["detached"] != true || detached["parentNarratorId"] != narratorID {
		t.Fatalf("unexpected detach payload: %#v", detached)
	}
	var isBackground int
	var parentID sql.NullString
	if err := service.DB.QueryRow(`SELECT is_background, parent_narrator_id FROM narrators WHERE id = ?`, child.ID).Scan(&isBackground, &parentID); err != nil {
		t.Fatalf("load detached child: %v", err)
	}
	if isBackground != 1 || !parentID.Valid || parentID.String != narratorID {
		t.Fatalf("detach should keep parent and mark background, bg=%d parent=%v", isBackground, parentID)
	}
	attached, err := service.AttachSubagent(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("attach subagent: %v", err)
	}
	if attached["attached"] != false || attached["staleRecovered"] != true || attached["backgroundStatus"] != "failed" {
		t.Fatalf("attach should reconcile stale background without active run: %#v", attached)
	}
}

func TestUpdateSubagentConclusionUpdatesParentToolCallWithFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	parent, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	assistant, err := service.addMessage(context.Background(), narratorID, "assistant", "launch", nil)
	if err != nil {
		t.Fatalf("insert parent assistant: %v", err)
	}
	_, err = service.DB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES (?, ?, ?, 'agent-1', 'Agent', '{}', '{"output":"old"}', 'success', ?)`, id.New(), narratorID, assistant.ID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("insert parent tool call: %v", err)
	}
	child, err := service.createSubagentNarrator(context.Background(), parent, "review", "Review", "openai:gpt-test", "", ptrString(parent.Cwd), "reviewer", false)
	if err != nil {
		t.Fatalf("create subagent: %v", err)
	}
	userMsg, err := service.addMessage(context.Background(), child.ID, "user", "review", nil)
	if err != nil {
		t.Fatalf("insert child user: %v", err)
	}
	if _, err := service.DB.Exec(`UPDATE narrator_messages SET parent_tool_use_id = 'agent-1' WHERE id = ?`, userMsg.ID); err != nil {
		t.Fatalf("tag child user parent tool use: %v", err)
	}
	_, err = service.addMessage(context.Background(), child.ID, "assistant", "new conclusion", nil)
	if err != nil {
		t.Fatalf("insert child assistant: %v", err)
	}
	if _, err := service.DB.Exec(`UPDATE narrators SET substatus = '["manual_override"]' WHERE id = ?`, child.ID); err != nil {
		t.Fatalf("set manual override: %v", err)
	}
	payload, err := service.UpdateSubagentConclusion(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("update conclusion: %v", err)
	}
	if payload["ok"] != true || payload["manualOverrideCleared"] != true || payload["fallback"] != true {
		t.Fatalf("unexpected conclusion payload: %#v", payload)
	}
	reason, _ := payload["reason"].(string)
	if reason == "" || payload["error"] != reason || payload["message"] != reason {
		t.Fatalf("subagent conclusion fallback should expose reason/error/message, got %#v", payload)
	}
	var outputRaw string
	if err := service.DB.QueryRow(`SELECT output_json FROM narrator_tool_calls WHERE tool_use_id = 'agent-1'`).Scan(&outputRaw); err != nil {
		t.Fatalf("load updated tool call: %v", err)
	}
	if !strings.Contains(outputRaw, "new conclusion") || !strings.Contains(outputRaw, "updatedConclusion") {
		t.Fatalf("tool call conclusion not updated: %s", outputRaw)
	}
}

func TestUpdateSubagentConclusionReturnsManualOverrideClearError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	parent, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	assistant, err := service.addMessage(context.Background(), narratorID, "assistant", "launch", nil)
	if err != nil {
		t.Fatalf("insert parent assistant: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, created_at) VALUES (?, ?, ?, 'agent-1', 'Agent', '{}', '{"output":"old"}', 'success', ?)`, id.New(), narratorID, assistant.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert parent tool call: %v", err)
	}
	child, err := service.createSubagentNarrator(context.Background(), parent, "review", "Review", "openai:gpt-test", "", ptrString(parent.Cwd), "reviewer", false)
	if err != nil {
		t.Fatalf("create subagent: %v", err)
	}
	userMsg, err := service.addMessage(context.Background(), child.ID, "user", "review", nil)
	if err != nil {
		t.Fatalf("insert child user: %v", err)
	}
	if _, err := service.DB.Exec(`UPDATE narrator_messages SET parent_tool_use_id = 'agent-1' WHERE id = ?`, userMsg.ID); err != nil {
		t.Fatalf("tag child user parent tool use: %v", err)
	}
	if _, err := service.addMessage(context.Background(), child.ID, "assistant", "new conclusion", nil); err != nil {
		t.Fatalf("insert child assistant: %v", err)
	}
	if _, err := service.DB.Exec(`UPDATE narrators SET substatus = '["manual_override"]' WHERE id = ?`, child.ID); err != nil {
		t.Fatalf("set manual override: %v", err)
	}
	if _, err := service.DB.Exec(`CREATE TRIGGER fail_manual_override_clear BEFORE UPDATE OF substatus ON narrators WHEN OLD.id = '` + child.ID + `' BEGIN SELECT RAISE(ABORT, 'manual override clear blocked'); END;`); err != nil {
		t.Fatalf("create manual override trigger: %v", err)
	}

	_, err = service.UpdateSubagentConclusion(context.Background(), child.ID)
	if err == nil || !strings.Contains(err.Error(), "clear subagent manual override") {
		t.Fatalf("expected manual override clear error, got %v", err)
	}
	var substatus string
	if err := service.DB.QueryRow(`SELECT substatus FROM narrators WHERE id = ?`, child.ID).Scan(&substatus); err != nil {
		t.Fatalf("load child substatus: %v", err)
	}
	if !strings.Contains(substatus, "manual_override") {
		t.Fatalf("failed clear should leave manual_override for retry, got %s", substatus)
	}
}

func TestProviderSpecificRetryableErrorPreservesCodeThroughWSAndFinalError(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	ctx := context.Background()
	narrator, err := service.Get(ctx, narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	event := agentbasic.AgentEvent{
		Type:     "retryable_error",
		Code:     agentbasic.CodexRebuildHistoryRetryCode,
		Message:  "codex quota failover requires history rebuild",
		Provider: "codex",
		Model:    "gpt-5-codex",
		Metadata: map[string]any{"previousCredentialId": "cred-old", "nextCredentialId": "cred-next"},
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	if err := service.handleAgentEvent(ctx, state, event); err != nil {
		t.Fatalf("handle provider retryable error: %v", err)
	}

	rawRetry := findBroadcast(events(), narratorID, "retryable_error")
	if rawRetry == nil || rawRetry["code"] != agentbasic.CodexRebuildHistoryRetryCode || rawRetry["provider"] != "codex" || rawRetry["model"] != "gpt-5-codex" {
		t.Fatalf("retryable_error should preserve provider code/model metadata, got %#v", rawRetry)
	}
	metadata, _ := rawRetry["metadata"].(map[string]any)
	if metadata["previousCredentialId"] != "cred-old" || metadata["nextCredentialId"] != "cred-next" {
		t.Fatalf("retryable_error should preserve failover metadata, got %#v", rawRetry)
	}
	finalError := findBroadcast(events(), narratorID, "narrator_error")
	if finalError == nil || finalError["errorCode"] != agentbasic.CodexRebuildHistoryRetryCode || !strings.Contains(stringFromAny(finalError["error"]), "history rebuild") {
		t.Fatalf("narrator_error should surface provider-specific error code, got %#v", finalError)
	}
	var status, substatus, persistedError string
	if err := service.DB.QueryRow(`SELECT status, substatus, COALESCE(error_message, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &persistedError); err != nil {
		t.Fatalf("query persisted error status: %v", err)
	}
	if status != "idle" || substatus != `["error"]` || !strings.Contains(persistedError, "history rebuild") {
		t.Fatalf("unexpected persisted provider error: status=%s substatus=%s error=%s", status, substatus, persistedError)
	}
}

func TestFirstTokenTimeoutAndSilentDisconnectPersistRunStatus(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	ctx := context.Background()
	narrator, err := service.Get(ctx, narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	timeoutState := &agentRunState{narratorID: narratorID, narrator: narrator}
	if err := service.handleAgentEvent(ctx, timeoutState, agentbasic.AgentEvent{Type: "first_token_timeout", Message: "first token timed out"}); err != nil {
		t.Fatalf("handle first token timeout: %v", err)
	}
	var status, substatus, errorMessage string
	if err := service.DB.QueryRow(`SELECT status, substatus, COALESCE(error_message, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &errorMessage); err != nil {
		t.Fatalf("query timeout status: %v", err)
	}
	if status != "idle" || substatus != `["timeout"]` || !strings.Contains(errorMessage, "first token timed out") {
		t.Fatalf("unexpected timeout persisted state: status=%s substatus=%s error=%s", status, substatus, errorMessage)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = service.DB.Exec(`UPDATE narrators SET status = 'working', substatus = '["calling_model"]', error_message = 'old', updated_at = ? WHERE id = ?`, now, narratorID)
	if err != nil {
		t.Fatalf("reset narrator status: %v", err)
	}
	disconnectState := &agentRunState{narratorID: narratorID, narrator: narrator}
	if err := service.handleAgentEvent(ctx, disconnectState, agentbasic.AgentEvent{Type: "silent_disconnect"}); err != nil {
		t.Fatalf("handle silent disconnect: %v", err)
	}
	if err := service.DB.QueryRow(`SELECT status, substatus, COALESCE(error_message, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &errorMessage); err != nil {
		t.Fatalf("query silent disconnect status: %v", err)
	}
	if status != "idle" || substatus != `["silent_disconnect"]` || errorMessage != "" {
		t.Fatalf("unexpected silent disconnect persisted state: status=%s substatus=%s error=%s", status, substatus, errorMessage)
	}
}

func TestCustomAndReviewSubagentTypesHaveBaseSupport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	parent, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	review, err := service.createSubagentNarrator(context.Background(), parent, normalizeSubagentType("review"), "Review", "openai:gpt-test", "", ptrString(parent.Cwd), "", false)
	if err != nil {
		t.Fatalf("create review: %v", err)
	}
	custom, err := service.createSubagentNarrator(context.Background(), parent, normalizeSubagentType("Security Reviewer"), "Security", "openai:gpt-test", "", ptrString(parent.Cwd), "", false)
	if err != nil {
		t.Fatalf("create custom: %v", err)
	}
	var reviewType, reviewPerm, reviewPrompt string
	if err := service.DB.QueryRow(`SELECT subagent_type, permission_mode, system_prompt FROM narrators WHERE id = ?`, review.ID).Scan(&reviewType, &reviewPerm, &reviewPrompt); err != nil {
		t.Fatalf("load review: %v", err)
	}
	if reviewType != "review" || reviewPerm != "readOnly" || !strings.Contains(reviewPrompt, "review mode") {
		t.Fatalf("unexpected review support: type=%s perm=%s prompt=%s", reviewType, reviewPerm, reviewPrompt)
	}
	var customType, customPrompt string
	if err := service.DB.QueryRow(`SELECT subagent_type, system_prompt FROM narrators WHERE id = ?`, custom.ID).Scan(&customType, &customPrompt); err != nil {
		t.Fatalf("load custom: %v", err)
	}
	if customType != "securityreviewer" || !strings.Contains(customPrompt, "custom subagent type") {
		t.Fatalf("unexpected custom support: type=%s prompt=%s", customType, customPrompt)
	}
}

func TestBufferedMessagesDrainAfterCurrentRun(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		count := requests
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		content := "first done"
		if count >= 2 {
			content = "second done"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": content}}}})
	}))
	defer server.Close()
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.AddUserMessage(context.Background(), narratorID, "tester", "first"); err != nil {
		t.Fatalf("start first message: %v", err)
	}
	if _, err := service.AddUserMessage(context.Background(), narratorID, "tester", "second"); err != nil {
		t.Fatalf("buffer second message: %v", err)
	}
	waitForAssistantText(t, service, narratorID, "second done")
	var buffered int
	if err := service.DB.QueryRow(`SELECT COUNT(*) FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&buffered); err != nil {
		t.Fatalf("count buffer: %v", err)
	}
	if buffered != 0 {
		t.Fatalf("expected buffer drained, got %d", buffered)
	}
}

func waitForActiveRun(t *testing.T, service *Service, narratorID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		service.runMu.Lock()
		_, ok := service.activeRuns[narratorID]
		service.runMu.Unlock()
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s did not become active", narratorID)
}

// TestHandleAgentEventRecordsOutputStats verifies handleAgentEvent feeds the
// global output-stats tracker from stream_text/stream_reasoning text (UTF-16
// length) and tool_use_chunk InputCharsTotal deltas, mirroring the TS
// narrator-event-handler recordOutputChunk call sites.
func TestHandleAgentEventRecordsOutputStats(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "")
	hub, _ := captureNarratorBroadcasts()
	service.Hub = hub
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	ctx := context.Background()

	// stream_text (10 chars) + stream_reasoning (5 chars) -> 15 chars.
	_ = service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "stream_text", Text: "0123456789"})
	_ = service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "stream_reasoning", Text: "abcde"})

	// tool_use_chunk: cumulative InputCharsTotal 8 then 20 -> deltas 8 + 12 = 20.
	_ = service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "tool_use_chunk", ToolUseID: "tool_a", Streaming: &agentbasic.ToolUseChunk{ToolUseID: "tool_a", InputCharsTotal: 8}})
	_ = service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "tool_use_chunk", ToolUseID: "tool_a", Streaming: &agentbasic.ToolUseChunk{ToolUseID: "tool_a", InputCharsTotal: 20}})
	// A non-increasing cumulative value records nothing (delta <= 0).
	_ = service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "tool_use_chunk", ToolUseID: "tool_a", Streaming: &agentbasic.ToolUseChunk{ToolUseID: "tool_a", InputCharsTotal: 20}})

	_, total := hub.OutputStatsSnapshot()
	if total != 35 {
		t.Fatalf("totalChars = %d, want 35 (10 text + 5 reasoning + 20 tool-input delta)", total)
	}
}

// TestHandleAgentEventPaymentRequired verifies the payment_required event from
// a NUG provider persists status=idle + substatus=["payment_required"] with a
// JSON error_message blob the frontend parses, mirroring the TS
// narrator-session.ts payment_required branch.
func TestHandleAgentEventPaymentRequired(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "")
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	balance := 1.5
	required := 10.0
	event := agentbasic.AgentEvent{
		Type:           "payment_required",
		Message:        "Insufficient balance",
		ProviderID:     "nug12345",
		ProviderPrefix: "nug",
		Balance:        &balance,
		Required:       &required,
		ResumeAction:   "continue",
	}
	if err := service.handleAgentEvent(context.Background(), state, event); err != nil {
		t.Fatalf("handleAgentEvent: %v", err)
	}

	var status, substatus, errorMessage string
	if err := service.DB.QueryRow(`SELECT status, substatus, COALESCE(error_message, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &errorMessage); err != nil {
		t.Fatalf("load narrator row: %v", err)
	}
	if status != "idle" {
		t.Fatalf("status = %q, want idle", status)
	}
	if substatus != `["payment_required"]` {
		t.Fatalf("substatus = %q, want [\"payment_required\"]", substatus)
	}
	var blob map[string]any
	if err := json.Unmarshal([]byte(errorMessage), &blob); err != nil {
		t.Fatalf("error_message is not valid JSON (%q): %v", errorMessage, err)
	}
	if blob["type"] != "payment_required" {
		t.Fatalf("blob.type = %v, want payment_required", blob["type"])
	}
	if blob["providerId"] != "nug12345" {
		t.Fatalf("blob.providerId = %v, want nug12345", blob["providerId"])
	}
	if blob["resumeAction"] != "continue" {
		t.Fatalf("blob.resumeAction = %v, want continue", blob["resumeAction"])
	}
	if b, _ := blob["balance"].(float64); b != 1.5 {
		t.Fatalf("blob.balance = %v, want 1.5", blob["balance"])
	}

	// The structured payment_required event must be broadcast to the narrator.
	sawPaymentRequired := false
	for _, ev := range drain() {
		if ev.Payload["type"] == "payment_required" {
			sawPaymentRequired = true
			if ev.Payload["providerId"] != "nug12345" {
				t.Fatalf("broadcast providerId = %v, want nug12345", ev.Payload["providerId"])
			}
			if ev.Payload["resumeAction"] != "continue" {
				t.Fatalf("broadcast resumeAction = %v, want continue", ev.Payload["resumeAction"])
			}
		}
	}
	if !sawPaymentRequired {
		t.Fatal("expected a payment_required broadcast")
	}
}
