package narrators

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

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
			model TEXT,
			commit_sha TEXT,
			command_text TEXT,
			created_by TEXT,
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
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": "goal-update-1", "type": "function", "function": map[string]any{"name": "UpdateGoal", "arguments": `{"goal_id":"` + goalID + `","status":"complete","evidence":"Verified event loop goal reflection completed successfully."}`}}}}}}})
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
	if status != "complete" {
		t.Fatalf("expected goal complete, got %s", status)
	}
}

func TestRunBasicAgentExitPlanReflectionWorksThroughEventLoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
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
	service.runBasicAgent(context.Background(), narratorID)
	var planMode int
	if err := service.DB.QueryRow(`SELECT plan_mode FROM narrators WHERE id = ?`, narratorID).Scan(&planMode); err != nil {
		t.Fatalf("load plan mode: %v", err)
	}
	if planMode != 0 {
		t.Fatalf("expected plan mode disabled after ExitPlanMode, got %d", planMode)
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
	_, err := service.DB.Exec(`UPDATE narrators SET permission_mode = 'trust-all' WHERE id = ?`, narratorID)
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
	blocks := latestAssistantBlocks(t, service, narratorID)
	if len(blocks) < 2 || blocks[0]["type"] != "tool_use" || blocks[len(blocks)-1]["text"] != "final text" {
		t.Fatalf("unexpected assistant blocks: %#v", blocks)
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

func waitForToolStatus(t *testing.T, service *Service, narratorID, toolUseID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		err := service.DB.QueryRow(`SELECT status FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, narratorID, toolUseID).Scan(&status)
		if err == nil && status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tool %s did not reach status %s", toolUseID, want)
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

func TestSubagentDetachAttachLifecycleFallbacks(t *testing.T) {
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
	if attached["attached"] != true || attached["fallback"] != true {
		t.Fatalf("attach should be explicit fallback without active waiter: %#v", attached)
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
