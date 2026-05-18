package agentbasic

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"narrafork-go/internal/config"
)

func TestReflectionToolsAreNotInNormalToolDefinitions(t *testing.T) {
	reflectionNames := map[string]bool{
		"DangerConfirm": false, "DangerCancel": false,
		"ExitPlanConfirm": false, "ExitPlanRevise": false,
		"GoalCompleteConfirm": false, "GoalCompleteRevise": false,
	}
	for _, spec := range reflectionToolSpecs() {
		if _, ok := reflectionNames[spec.Name]; ok {
			reflectionNames[spec.Name] = true
		}
	}
	for name, found := range reflectionNames {
		if !found {
			t.Fatalf("expected reflection tool spec for %s", name)
		}
	}
	for _, tool := range toolDefinitions() {
		fn, _ := tool["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if _, ok := reflectionNames[name]; ok {
			t.Fatalf("reflection-only tool %s must not be exposed in normal tool definitions", name)
		}
	}
}

func TestReflectionToolDefinitionsExposeSchemas(t *testing.T) {
	byName := map[string]toolSpec{}
	for _, spec := range reflectionToolSpecs() {
		byName[spec.Name] = spec
	}
	goalConfirmProps, _ := byName["GoalCompleteConfirm"].Parameters["properties"].(map[string]any)
	if goalConfirmProps["confirm"] == nil || goalConfirmProps["evidence"] == nil || goalConfirmProps["reflection"] == nil {
		t.Fatalf("GoalCompleteConfirm should expose confirm/evidence/reflection: %#v", goalConfirmProps)
	}
	goalReviseProps, _ := byName["GoalCompleteRevise"].Parameters["properties"].(map[string]any)
	if goalReviseProps["confirm"] == nil || goalReviseProps["feedback"] == nil {
		t.Fatalf("GoalCompleteRevise should expose confirm/feedback: %#v", goalReviseProps)
	}
	exitConfirmProps, _ := byName["ExitPlanConfirm"].Parameters["properties"].(map[string]any)
	if exitConfirmProps["confirm"] == nil || exitConfirmProps["reflection"] == nil {
		t.Fatalf("ExitPlanConfirm should expose confirm/reflection: %#v", exitConfirmProps)
	}
	exitReviseProps, _ := byName["ExitPlanRevise"].Parameters["properties"].(map[string]any)
	if exitReviseProps["confirm"] == nil || exitReviseProps["feedback"] == nil {
		t.Fatalf("ExitPlanRevise should expose confirm/feedback: %#v", exitReviseProps)
	}
}

func TestReviewToolScopeIncludesConcludeReviewAndNormalTools(t *testing.T) {
	tools := toolDefinitions("review")
	if !toolListHasFunction(tools, "ConcludeReview") || !toolListHasFunction(tools, "Read") {
		t.Fatalf("review scope should include ConcludeReview and normal tools: %#v", tools)
	}
	if toolListHasFunction(toolDefinitions("normal"), "ConcludeReview") {
		t.Fatalf("ConcludeReview leaked into normal tool scope")
	}
}

func toolListHasFunction(tools []map[string]any, name string) bool {
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == name {
			return true
		}
	}
	return false
}

func TestToolDefinitionsUseRequestedScope(t *testing.T) {
	normal := toolDefinitions()
	for _, tool := range normal {
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == "GoalCompleteConfirm" {
			t.Fatalf("reflection tool leaked into normal tools")
		}
	}
	reflection := toolDefinitions("reflection")
	found := false
	for _, tool := range reflection {
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == "GoalCompleteConfirm" {
			found = true
		}
		if fn["name"] == "Bash" {
			t.Fatalf("normal tool leaked into reflection tools")
		}
	}
	if !found {
		t.Fatalf("expected GoalCompleteConfirm in reflection tool scope")
	}
}

func TestToolDefinitionsIncludeGateParameters(t *testing.T) {
	byName := map[string]toolSpec{}
	for _, spec := range toolSpecs() {
		byName[spec.Name] = spec
	}
	updateProps, _ := byName["UpdateGoal"].Parameters["properties"].(map[string]any)
	if updateProps["evidence"] == nil || updateProps["reflection"] == nil {
		t.Fatalf("UpdateGoal should expose evidence/reflection parameters: %#v", updateProps)
	}
	exitProps, _ := byName["ExitPlanMode"].Parameters["properties"].(map[string]any)
	if exitProps["plan"] == nil || exitProps["allowedPrompts"] == nil {
		t.Fatalf("ExitPlanMode should expose plan/allowedPrompts parameters: %#v", exitProps)
	}
	agentProps, _ := byName["Agent"].Parameters["properties"].(map[string]any)
	if agentProps["timeout"] == nil {
		t.Fatalf("Agent should expose foreground timeout parameter: %#v", agentProps)
	}
}

func TestToolDefinitionsIncludeSubagentTools(t *testing.T) {
	wanted := map[string]bool{"Agent": false, "Await": false, "TeamStatus": false, "Send": false, "AskUserQuestion": false, "WebSearch": false, "Browser": false, "StartPipeline": false, "EndPipeline": false}
	for _, spec := range toolSpecs() {
		if _, ok := wanted[spec.Name]; ok {
			wanted[spec.Name] = true
			if spec.Parameters == nil {
				t.Fatalf("tool %s has nil parameters", spec.Name)
			}
		}
	}
	for name, found := range wanted {
		if !found {
			t.Fatalf("expected tool definition for %s", name)
		}
	}
}

func testConfigStore(t *testing.T, patch map[string]any) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if patch != nil {
		if _, err := store.Patch(patch); err != nil {
			t.Fatalf("patch config: %v", err)
		}
	}
	return store
}

func TestResolveProviderAndModelReturnsOpenAIAdapter(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"openaiProviders": []any{
			map[string]any{
				"prefix":       "openai",
				"apiKey":       "test-key",
				"baseUrl":      "https://example.invalid/v1",
				"defaultModel": "gpt-test",
			},
		},
	})
	resolution, err := ResolveProviderAndModel(store, "openai:gpt-test")
	if err != nil {
		t.Fatalf("ResolveProviderAndModel returned error: %v", err)
	}
	if resolution.Provider != "openai" || resolution.Model != "gpt-test" {
		t.Fatalf("unexpected resolution: %#v", resolution)
	}
	if _, ok := resolution.Adapter.(*OpenAIChatCompletionsAdapter); !ok {
		t.Fatalf("expected OpenAIChatCompletionsAdapter, got %T", resolution.Adapter)
	}
}

func TestResolveProviderAndModelReturnsAnthropicAdapter(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"anthropicProviders": []any{
			map[string]any{
				"prefix":       "anthropic",
				"apiKey":       "test-key",
				"baseUrl":      "https://example.invalid/v1",
				"defaultModel": "claude-test",
			},
		},
	})
	resolution, err := ResolveProviderAndModel(store, "anthropic:claude-test")
	if err != nil {
		t.Fatalf("ResolveProviderAndModel returned error: %v", err)
	}
	if resolution.Provider != "anthropic" || resolution.Model != "claude-test" {
		t.Fatalf("unexpected resolution: %#v", resolution)
	}
	if _, ok := resolution.Adapter.(*AnthropicMessagesAdapter); !ok {
		t.Fatalf("expected AnthropicMessagesAdapter, got %T", resolution.Adapter)
	}
}

func TestResolveProviderFallsBackFromUnsupportedKiroToConfiguredOpenAI(t *testing.T) {
	raw := map[string]any{
		"openaiProviders": []any{
			map[string]any{
				"id":           "openai-test",
				"prefix":       "openai",
				"apiKey":       "test-key",
				"baseUrl":      "https://example.invalid/v1",
				"defaultModel": "gpt-test",
			},
		},
	}
	provider, err := resolveProviderFromRaw(raw, "kiro:claude-sonnet-4.5")
	if err != nil {
		t.Fatalf("resolveProviderFromRaw returned error: %v", err)
	}
	if provider.Prefix != "openai" || provider.Kind != "openai" || provider.Model != "gpt-test" {
		t.Fatalf("unexpected fallback provider: %#v", provider)
	}
}

func TestResolveProviderFallsBackFromUnsupportedKiroToOpenAIEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	t.Setenv("OPENAI_BASE_URL", "https://env.example.invalid/v1")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	provider, err := resolveProviderFromRaw(map[string]any{}, "kiro:claude-sonnet-4.5")
	if err != nil {
		t.Fatalf("resolveProviderFromRaw returned error: %v", err)
	}
	if provider.Prefix != "openai" || provider.Kind != "openai" || provider.Model != "gpt-4o-mini" || provider.APIKey != "env-key" {
		t.Fatalf("unexpected env fallback provider: %#v", provider)
	}
}

func TestResolveProviderReportsUnsupportedKiroWhenNoFallbackExists(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	_, err := resolveProviderFromRaw(map[string]any{}, "kiro:claude-sonnet-4.5")
	if err == nil || !strings.Contains(err.Error(), "not supported by go_backend runtime") {
		t.Fatalf("expected unsupported Kiro fallback error, got: %v", err)
	}
}

func TestAnthropicToolLoopUsesToolsAndSystemPrompt(t *testing.T) {
	var mu sync.Mutex
	requests := []map[string]any{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		requests = append(requests, body)
		count := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{
					{
						"type":  "tool_use",
						"id":    "tool-1",
						"name":  "Bash",
						"input": map[string]any{"command": "ignored"},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "final answer"}},
		})
	}))
	defer server.Close()

	store := testConfigStore(t, map[string]any{
		"anthropicProviders": []any{
			map[string]any{
				"prefix":       "anthropic",
				"apiKey":       "test-key",
				"baseUrl":      server.URL,
				"defaultModel": "claude-test",
			},
		},
	})
	result, err := Generate(
		context.Background(),
		store,
		"anthropic:claude-test",
		[]Message{{Role: "user", Content: "please run a tool"}},
		t.TempDir(),
		WithSystemPrompt("custom system prompt"),
		WithMaxTurns(4),
		WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
			return ToolRun{Output: "tool output"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if result.Text != "final answer" {
		t.Fatalf("unexpected result text: %q", result.Text)
	}
	if len(result.ToolRuns) != 1 {
		t.Fatalf("expected one tool run, got %d", len(result.ToolRuns))
	}
	if result.ToolRuns[0].ID != "tool-1" || result.ToolRuns[0].Name != "Bash" {
		t.Fatalf("tool run was not normalized from tool call: %#v", result.ToolRuns[0])
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected two Anthropic requests, got %d", len(requests))
	}
	systemBlocks, ok := requests[0]["system"].([]any)
	if !ok || len(systemBlocks) == 0 {
		t.Fatalf("system prompt not sent as Anthropic block: %#v", requests[0]["system"])
	}
	firstSystemBlock, _ := systemBlocks[0].(map[string]any)
	if firstSystemBlock["text"] != "custom system prompt" || firstSystemBlock["cache_control"] == nil {
		t.Fatalf("system prompt block not sent with cache_control: %#v", requests[0]["system"])
	}
	tools, ok := requests[0]["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("expected tool definitions in first request: %#v", requests[0]["tools"])
	}
	messages, ok := requests[1]["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("expected messages in second request: %#v", requests[1]["messages"])
	}
	last, ok := messages[len(messages)-1].(map[string]any)
	if !ok || last["role"] != "user" {
		t.Fatalf("expected final message to be user tool_result: %#v", messages[len(messages)-1])
	}
	content, ok := last["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected one tool_result block: %#v", last["content"])
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "tool_result" || block["tool_use_id"] != "tool-1" || block["content"] != "tool output" {
		t.Fatalf("unexpected tool_result block: %#v", content[0])
	}
}

func TestGenerateUsesOpenAIAdapterToolLoop(t *testing.T) {
	var mu sync.Mutex
	requests := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		requests = append(requests, body)
		count := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]any{{
							"id":   "call-1",
							"type": "function",
							"function": map[string]any{
								"name":      "Bash",
								"arguments": `{"command":"ignored"}`,
							},
						}},
					},
				}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "final answer"}}},
		})
	}))
	defer server.Close()

	store := testConfigStore(t, map[string]any{
		"openaiProviders": []any{
			map[string]any{
				"prefix":       "openai",
				"apiKey":       "test-key",
				"baseUrl":      server.URL,
				"defaultModel": "gpt-test",
			},
		},
	})
	result, err := Generate(
		context.Background(),
		store,
		"openai:gpt-test",
		[]Message{{Role: "user", Content: "please run a tool"}},
		t.TempDir(),
		WithSystemPrompt("openai system prompt"),
		WithMaxTurns(4),
		WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
			return ToolRun{Output: "tool output"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if result.Text != "final answer" {
		t.Fatalf("unexpected result text: %q", result.Text)
	}
	if len(result.ToolRuns) != 1 || result.ToolRuns[0].ID != "call-1" || result.ToolRuns[0].Name != "Bash" {
		t.Fatalf("unexpected tool runs: %#v", result.ToolRuns)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected two OpenAI requests, got %d", len(requests))
	}
	messages, ok := requests[1]["messages"].([]any)
	if !ok || len(messages) < 4 {
		t.Fatalf("expected loop messages in second request: %#v", requests[1]["messages"])
	}
	last, ok := messages[len(messages)-1].(map[string]any)
	if !ok || last["role"] != "tool" || last["tool_call_id"] != "call-1" || last["content"] != "tool output" {
		t.Fatalf("unexpected final tool message: %#v", messages[len(messages)-1])
	}
}

func TestRunAgentLoopOpenAIAdapterEmitsEventSequence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "adapter text"}}},
		})
	}))
	defer server.Close()
	store := testConfigStore(t, map[string]any{
		"openaiProviders": []any{map[string]any{"prefix": "openai", "apiKey": "test-key", "baseUrl": server.URL, "defaultModel": "gpt-test"}},
	})
	stream, err := RunAgentLoop(context.Background(), store, "openai:gpt-test", []Message{{Role: "user", Content: "hello"}}, t.TempDir(), WithRetryBaseDelay(0))
	if err != nil {
		t.Fatalf("RunAgentLoop returned error: %v", err)
	}
	events := collectEvents(stream)
	want := []string{"stream_text", "block_complete", "assistant_message", "done"}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %#v, want %#v", got, want)
	}
}

func TestRunAgentLoopAnthropicAdapterEmitsEventSequence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "anthropic text"}},
		})
	}))
	defer server.Close()
	store := testConfigStore(t, map[string]any{
		"anthropicProviders": []any{map[string]any{"prefix": "anthropic", "apiKey": "test-key", "baseUrl": server.URL, "defaultModel": "claude-test"}},
	})
	stream, err := RunAgentLoop(context.Background(), store, "anthropic:claude-test", []Message{{Role: "user", Content: "hello"}}, t.TempDir(), WithRetryBaseDelay(0))
	if err != nil {
		t.Fatalf("RunAgentLoop returned error: %v", err)
	}
	events := collectEvents(stream)
	want := []string{"stream_text", "block_complete", "assistant_message", "done"}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %#v, want %#v", got, want)
	}
}

func TestChatCompletionsUsesReflectionToolScope(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "done"}}},
		})
	}))
	defer server.Close()
	provider := Provider{Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-4o-mini"}
	_, err := doChatRequest(
		context.Background(),
		&http.Client{},
		provider,
		[]Message{{Role: "system", Content: "reflect system"}, {Role: "user", Content: "reflect"}},
		"",
		"reflection",
	)
	if err != nil {
		t.Fatalf("doChatRequest returned error: %v", err)
	}
	tools, ok := requestBody["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("expected reflection tools: %#v", requestBody["tools"])
	}
	seenGoalConfirm := false
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		if fn["name"] == "GoalCompleteConfirm" {
			seenGoalConfirm = true
		}
		if fn["name"] == "Bash" {
			t.Fatalf("normal tool should not be sent in reflection scope")
		}
	}
	if !seenGoalConfirm {
		t.Fatalf("expected GoalCompleteConfirm in reflection tools: %#v", tools)
	}
}

func TestChatCompletionsSendsSupportedReasoningEffort(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "done"}}},
		})
	}))
	defer server.Close()

	provider := Provider{Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-5-test"}
	result, err := doChatRequest(
		context.Background(),
		&http.Client{},
		provider,
		[]Message{{Role: "system", Content: "openai system"}, {Role: "user", Content: "hello"}},
		"xhigh",
		"normal",
	)
	if err != nil {
		t.Fatalf("doChatRequest returned error: %v", err)
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content != "done" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if requestBody["reasoning_effort"] != "high" {
		t.Fatalf("expected xhigh to be clamped to high, got %#v", requestBody["reasoning_effort"])
	}
	messages, ok := requestBody["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("expected messages in request: %#v", requestBody["messages"])
	}
	first, ok := messages[0].(map[string]any)
	if !ok || first["role"] != "system" || first["content"] != "openai system" {
		t.Fatalf("custom system prompt not sent as first message: %#v", messages[0])
	}
}

func TestGrepToolSupportsModesContextAndCaseInsensitive(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "src"), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "src", "a.go"), []byte("one\nTwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "src", "b.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Grep", `{"pattern":"two","glob":"**/*.go","-i":true,"context":1}`)
	if !strings.Contains(out, "src/a.go:1:one") || !strings.Contains(out, "src/a.go:2:Two") || strings.Contains(out, "b.txt") {
		t.Fatalf("unexpected grep context output: %s", out)
	}
	out = ExecuteTool(context.Background(), cwd, "Grep", `{"pattern":"two","-i":true,"output_mode":"files_with_matches"}`)
	if !strings.Contains(out, "src/a.go") || !strings.Contains(out, "src/b.txt") {
		t.Fatalf("unexpected files_with_matches output: %s", out)
	}
	out = ExecuteTool(context.Background(), cwd, "Grep", `{"pattern":"two","-i":true,"output_mode":"count","head_limit":1}`)
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Fatalf("expected head_limit to keep one count entry, got: %s", out)
	}
}

func TestGlobToolSupportsRecursivePatterns(t *testing.T) {
	cwd := t.TempDir()
	for _, file := range []string{"main.go", "internal/app/server.go", "internal/app/server.ts", "README.md"} {
		full := filepath.Join(cwd, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir fixture: %v", err)
		}
		if err := os.WriteFile(full, []byte(file), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	out := ExecuteTool(context.Background(), cwd, "Glob", `{"pattern":"**/*.go"}`)
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "internal/app/server.go") || strings.Contains(out, "server.ts") {
		t.Fatalf("unexpected recursive glob output: %s", out)
	}
	out = ExecuteTool(context.Background(), cwd, "Glob", `{"pattern":"*.md"}`)
	if !strings.Contains(out, "README.md") || strings.Contains(out, "server.go") {
		t.Fatalf("unexpected basename glob output: %s", out)
	}
}

func TestEditToolCreatesFileWhenOldStringEmpty(t *testing.T) {
	cwd := t.TempDir()
	out := ExecuteTool(context.Background(), cwd, "Edit", `{"file_path":"new/deep/file.txt","old_string":"","new_string":"brand new"}`)
	if !strings.Contains(out, "created") {
		t.Fatalf("expected create success, got: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(cwd, "new", "deep", "file.txt"))
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if string(data) != "brand new" {
		t.Fatalf("unexpected created content: %q", string(data))
	}
}

func TestEditToolRequiresUniqueMatchUnlessReplaceAll(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "edit.txt")
	if err := os.WriteFile(path, []byte("foo\nfoo\nbar\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Edit", `{"file_path":"edit.txt","old_string":"foo","new_string":"baz"}`)
	if !strings.Contains(out, "multiple matches") {
		t.Fatalf("expected non-unique match error, got: %s", out)
	}
	out = ExecuteTool(context.Background(), cwd, "Edit", `{"file_path":"edit.txt","old_string":"foo","new_string":"baz","replace_all":true}`)
	if !strings.Contains(out, "2 replacements") {
		t.Fatalf("expected replace_all success, got: %s", out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if string(data) != "baz\nbaz\nbar\n" {
		t.Fatalf("unexpected edited content: %q", string(data))
	}
}

func TestWriteAndEditToolsEnforceSizeLimits(t *testing.T) {
	cwd := t.TempDir()
	huge := strings.Repeat("x", maxToolWriteBytes+1)
	writePayload, _ := json.Marshal(map[string]string{"file_path": "huge.txt", "content": huge})
	out := ExecuteTool(context.Background(), cwd, "Write", string(writePayload))
	if !strings.Contains(out, "too large") {
		t.Fatalf("expected oversized write rejection, got: %s", out)
	}

	path := filepath.Join(cwd, "large-edit.txt")
	if err := os.WriteFile(path, []byte(huge), 0o644); err != nil {
		t.Fatalf("write large edit fixture: %v", err)
	}
	out = ExecuteTool(context.Background(), cwd, "Edit", `{"file_path":"large-edit.txt","old_string":"x","new_string":"y"}`)
	if !strings.Contains(out, "file is too large to edit") {
		t.Fatalf("expected oversized edit rejection, got: %s", out)
	}
}

func TestEditToolRejectsIdenticalReplacement(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "edit.txt"), []byte("foo\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Edit", `{"file_path":"edit.txt","old_string":"foo","new_string":"foo"}`)
	if !strings.Contains(out, "No changes to apply") {
		t.Fatalf("expected identical replacement rejection, got: %s", out)
	}
}

func TestReadToolStreamsLargeFilesWithLineOffset(t *testing.T) {
	cwd := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 180000; i++ {
		_, _ = fmt.Fprintf(&b, "line-%06d abcdefghijklmnopqrstuvwxyz\n", i)
	}
	if err := os.WriteFile(filepath.Join(cwd, "large.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"large.txt","offset":179999,"limit":2}`)
	if !strings.Contains(out, "179999│line-179999") || !strings.Contains(out, "180000│line-180000") {
		t.Fatalf("unexpected streamed read output: %s", out)
	}
}

func TestReadToolListsDirectories(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "dir", "nested"), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "dir", "file.txt"), []byte("file"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"dir"}`)
	if !strings.Contains(out, "Directory:") || !strings.Contains(out, "file.txt") || !strings.Contains(out, "nested/") {
		t.Fatalf("unexpected directory listing: %s", out)
	}
}

func TestReadToolSupportsOffsetAndLimit(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "sample.txt"), []byte("line1\nline2\nline3\nline4\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"sample.txt","offset":2,"limit":2}`)
	if out != "     2│line2\n     3│line3" {
		t.Fatalf("expected numbered line offset read, got %q", out)
	}
	full := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"sample.txt","offset":3,"limit":-1}`)
	if full != "     3│line3\n     4│line4\n     5│" {
		t.Fatalf("expected numbered read to EOF, got %q", full)
	}
}

func TestBashToolRejectsEmptyCommand(t *testing.T) {
	out := ExecuteTool(context.Background(), t.TempDir(), "Bash", `{"command":"   "}`)
	if !strings.Contains(out, "command is required") {
		t.Fatalf("expected empty command error, got: %s", out)
	}
}

func TestBashToolHonorsTimeout(t *testing.T) {
	out := ExecuteTool(context.Background(), t.TempDir(), "Bash", `{"command":"sleep 2","timeout":1000}`)
	if !strings.Contains(out, "[timeout]") {
		t.Fatalf("expected timeout marker, got: %s", out)
	}
}

func TestBrowserToolSchemaIncludesRodParameters(t *testing.T) {
	var browser *toolSpec
	for _, spec := range toolSpecs() {
		if spec.Name == "Browser" {
			copy := spec
			browser = &copy
			break
		}
	}
	if browser == nil {
		t.Fatalf("expected Browser tool spec")
	}
	props, _ := browser.Parameters["properties"].(map[string]any)
	for _, key := range []string{"selector", "value", "key", "state", "direction", "amount", "timeout", "ttl_ms", "max_length", "file_path", "full_page", "headless", "clear", "include_details", "capture_network", "wait_after_ms", "categories", "coordinate"} {
		if props[key] == nil {
			t.Fatalf("Browser tool should expose %s: %#v", key, props)
		}
	}
}

func TestFindChromePathUsesEnvVar(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "chrome")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUPPETEER_EXECUTABLE_PATH", fake)
	path, err := findChromePath()
	if err != nil {
		t.Fatalf("expected env chrome path, got error: %v", err)
	}
	if path != fake {
		t.Fatalf("expected %s, got %s", fake, path)
	}
}

func TestScanPuppeteerCacheFindsChromeExecutable(t *testing.T) {
	cache := t.TempDir()
	older := filepath.Join(cache, "chrome", "linux-100", "chrome-old", "chrome")
	newer := filepath.Join(cache, "chrome", "linux-999", "chrome-new", "chrome")
	if err := os.MkdirAll(filepath.Dir(older), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(newer), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(older, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := scanPuppeteerCache(cache); got != newer {
		t.Fatalf("expected newest puppeteer chrome %s, got %s", newer, got)
	}
}

func TestBrowserToolRodSessionLifecycle(t *testing.T) {
	if os.Getenv("NARRAFORK_GO_BROWSER_INTEGRATION") != "1" {
		t.Skip("set NARRAFORK_GO_BROWSER_INTEGRATION=1 to run rod browser integration test")
	}
	if _, err := findChromePath(); err != nil {
		t.Skipf("Chrome/Chromium not available: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ping" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("pong"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/next" {
			_, _ = w.Write([]byte(`<html><head><title>Next</title></head><body><h1>Next Page</h1></body></html>`))
			return
		}
		_, _ = w.Write([]byte(`<html><head><title>Home</title></head><body><h1>Hello Browser</h1><input id="name" data-role="target"><select id="choice" onchange="document.querySelector('#selected').textContent = this.value"><option value="one">One</option><option value="two">Two</option></select><button id="copy" onclick="document.querySelector('#out').textContent = document.querySelector('#name').value">Copy</button><button id="hover-target" data-hover="ready" onmouseover="document.querySelector('#hover-state').textContent = 'hovered'">Hover</button><p id="out"></p><p id="selected"></p><p id="hover-state"></p><script>console.log('console ready'); fetch('/api/ping').then(r => r.text()).then(txt => window.fetchResult = txt); window.ready = true;</script></body></html>`))
	}))
	defer server.Close()

	cwd := t.TempDir()
	out := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"launch","url":"`+server.URL+`","capture_network":true,"ttl_ms":4000}`)
	var launch map[string]any
	if err := json.Unmarshal([]byte(out), &launch); err != nil {
		t.Fatalf("expected launch JSON, got %q: %v", out, err)
	}
	sessionID, _ := launch["session_id"].(string)
	if sessionID == "" || launch["title"] != "Home" || !strings.Contains(stringValue(launch["text"]), "Hello Browser") {
		t.Fatalf("unexpected launch payload: %#v", launch)
	}
	defer ExecuteTool(context.Background(), cwd, "Browser", `{"action":"close","session_id":"`+sessionID+`"}`)

	if out := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"wait","session_id":"`+sessionID+`","selector":"#name","state":"visible"}`); !strings.Contains(out, sessionID) {
		t.Fatalf("unexpected wait output: %s", out)
	}
	if out := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"wait","session_id":"`+sessionID+`","selector":"#missing","state":"hidden"}`); !strings.Contains(out, sessionID) {
		t.Fatalf("unexpected hidden-wait output: %s", out)
	}
	if out := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"select","session_id":"`+sessionID+`","selector":"#choice","value":"two"}`); !strings.Contains(out, sessionID) {
		t.Fatalf("unexpected select output: %s", out)
	}
	if out := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"hover","session_id":"`+sessionID+`","selector":"#copy"}`); !strings.Contains(out, sessionID) {
		t.Fatalf("unexpected hover output: %s", out)
	}
	fill := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"fill","session_id":"`+sessionID+`","selector":"#name","value":"Ada"}`)
	if !strings.Contains(fill, `"session_id": "`+sessionID+`"`) {
		t.Fatalf("unexpected fill output: %s", fill)
	}
	click := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"click","session_id":"`+sessionID+`","selector":"#copy"}`)
	if !strings.Contains(click, `"session_id": "`+sessionID+`"`) {
		t.Fatalf("unexpected click output: %s", click)
	}
	text := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"get_text","session_id":"`+sessionID+`","selector":"#out"}`)
	if !strings.Contains(text, "Ada") {
		t.Fatalf("unexpected text output: %s", text)
	}
	attr := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"get_attribute","session_id":"`+sessionID+`","selector":"#name","value":"id"}`)
	if !strings.Contains(attr, `"value": "name"`) {
		t.Fatalf("unexpected attribute output: %s", attr)
	}
	console := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"get_console","session_id":"`+sessionID+`","max_length":5000}`)
	if !strings.Contains(console, "console") && !strings.Contains(console, "ready") {
		t.Fatalf("unexpected console output: %s", console)
	}
	network := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"get_network","session_id":"`+sessionID+`","value":"/api/ping","include_details":true,"max_length":5000}`)
	if !strings.Contains(network, "/api/ping") {
		t.Fatalf("unexpected network output: %s", network)
	}
	eval := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"evaluate_capture","session_id":"`+sessionID+`","value":"(() => { console.log('capture log'); return window.ready === true; })()","clear":true,"wait_after_ms":50}`)
	if !strings.Contains(eval, "capture log") || !strings.Contains(eval, "true") {
		t.Fatalf("unexpected evaluate_capture output: %s", eval)
	}
	if out := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"network_stop","session_id":"`+sessionID+`"}`); !strings.Contains(out, `"networkCaptureEnabled": false`) {
		t.Fatalf("unexpected network_stop output: %s", out)
	}
	if out := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"network_start","session_id":"`+sessionID+`","clear":true}`); !strings.Contains(out, `"networkCaptureEnabled": true`) {
		t.Fatalf("unexpected network_start output: %s", out)
	}
	if out := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"set_ttl","session_id":"`+sessionID+`","ttl_ms":5000}`); !strings.Contains(out, `"ttlMs": 5000`) {
		t.Fatalf("unexpected set_ttl output: %s", out)
	}
	if out := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"scroll","session_id":"`+sessionID+`","amount":100}`); !strings.Contains(out, sessionID) {
		t.Fatalf("unexpected scroll output: %s", out)
	}
	shot := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"screenshot","session_id":"`+sessionID+`","file_path":"shot.png"}`)
	if !strings.Contains(shot, `"mime": "image/png"`) {
		t.Fatalf("unexpected screenshot output: %s", shot)
	}
	if _, err := os.Stat(filepath.Join(cwd, "shot.png")); err != nil {
		t.Fatalf("expected screenshot file: %v", err)
	}

	out = ExecuteTool(context.Background(), cwd, "Browser", `{"action":"perf_start","session_id":"`+sessionID+`"}`)
	if !strings.Contains(out, `"tracingStatus": "active"`) {
		t.Fatalf("unexpected perf_start output: %s", out)
	}
	out = ExecuteTool(context.Background(), cwd, "Browser", `{"action":"navigate","session_id":"`+sessionID+`","url":"`+server.URL+`/next"}`)
	var nav map[string]any
	if err := json.Unmarshal([]byte(out), &nav); err != nil {
		t.Fatalf("expected navigate JSON, got %q: %v", out, err)
	}
	if nav["title"] != "Next" || !strings.Contains(stringValue(nav["text"]), "Next Page") {
		t.Fatalf("unexpected navigate payload: %#v", nav)
	}
	traceOut := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"perf_stop","session_id":"`+sessionID+`","file_path":"trace.json"}`)
	if !strings.Contains(traceOut, `"file_path": `) || !strings.Contains(traceOut, `"durationMs":`) {
		t.Fatalf("unexpected perf_stop output: %s", traceOut)
	}
	if _, err := os.Stat(filepath.Join(cwd, "trace.json")); err != nil {
		t.Fatalf("expected trace file: %v", err)
	}
	listed := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"list_sessions"}`)
	if !strings.Contains(listed, sessionID) || !strings.Contains(listed, `"ttlMs": 5000`) {
		t.Fatalf("expected session in list: %s", listed)
	}
	closed := ExecuteTool(context.Background(), cwd, "Browser", `{"action":"close","session_id":"`+sessionID+`"}`)
	if !strings.Contains(closed, `"ok": true`) {
		t.Fatalf("expected close success: %s", closed)
	}
}

func TestBrowserToolReportsUnsupportedActions(t *testing.T) {
	out := ExecuteTool(context.Background(), t.TempDir(), "Browser", `{"action":"unsupported_action"}`)
	if !strings.Contains(out, "unsupported") {
		t.Fatalf("expected unsupported action message, got: %s", out)
	}
}

func TestDuckSearchResultsAndDomainFilters(t *testing.T) {
	payload := map[string]any{
		"Heading":      "Example",
		"AbstractURL":  "https://example.com/docs",
		"AbstractText": "Example docs",
		"RelatedTopics": []any{
			map[string]any{"Text": "Go docs - The Go Programming Language", "FirstURL": "https://go.dev/doc/"},
			map[string]any{"Text": "Blocked", "FirstURL": "https://blocked.example/item"},
		},
	}
	results := duckSearchResults(payload)
	if len(results) != 3 {
		t.Fatalf("expected 3 parsed results, got %d: %#v", len(results), results)
	}
	if !domainMatches("https://pkg.go.dev/fmt", []string{"go.dev"}, nil) {
		t.Fatalf("expected subdomain to match allowed domain")
	}
	if domainMatches("https://blocked.example/item", nil, []string{"blocked.example"}) {
		t.Fatalf("expected blocked domain to be rejected")
	}
}

func TestShareFileToolCopiesFileUnderSharesRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "artifact.txt"), []byte("hello share"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	out := ExecuteTool(context.Background(), cwd, "ShareFile", `{"path":"artifact.txt","preview":true}`)
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", out, err)
	}
	shareID, _ := payload["id"].(string)
	if shareID == "" || payload["url"] != "/api/shares/"+shareID || payload["previewUrl"] != "/api/shares/"+shareID+"/preview" {
		t.Fatalf("unexpected share payload: %#v", payload)
	}
	sharedPath := filepath.Join(home, ".narrafork", "shares", shareID, "artifact.txt")
	data, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Fatalf("read shared file: %v", err)
	}
	if string(data) != "hello share" {
		t.Fatalf("unexpected shared content: %q", string(data))
	}
}

func TestShareFileToolCompressesSingleFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "artifact.txt"), []byte("hello gzip"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "ShareFile", `{"path":"artifact.txt","compress":true,"preview":true}`)
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", out, err)
	}
	if payload["compressed"] != true || payload["previewUrl"] != nil || payload["fileName"] != "artifact.txt.gz" {
		t.Fatalf("unexpected compressed payload: %#v", payload)
	}
	shareID, _ := payload["id"].(string)
	file, err := os.Open(filepath.Join(home, ".narrafork", "shares", shareID, "artifact.txt.gz"))
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("open gzip reader: %v", err)
	}
	data, err := io.ReadAll(gz)
	_ = gz.Close()
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if string(data) != "hello gzip" {
		t.Fatalf("unexpected gzip content: %q", string(data))
	}
}

func TestShareFileToolPackagesMultiplePathsAsZip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "dir", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "ShareFile", `{"path":["a.txt","dir"]}`)
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", out, err)
	}
	if payload["compressed"] != true || payload["fileName"] != "shared-files.zip" {
		t.Fatalf("unexpected zip payload: %#v", payload)
	}
	shareID, _ := payload["id"].(string)
	zr, err := zip.OpenReader(filepath.Join(home, ".narrafork", "shares", shareID, "shared-files.zip"))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, file := range zr.File {
		names[file.Name] = true
	}
	if !names["a.txt"] || !names["dir/b.txt"] {
		t.Fatalf("zip missing expected files: %#v", names)
	}
}

func TestShareFileToolRejectsEscapingPath(t *testing.T) {
	cwd := t.TempDir()
	out := ExecuteTool(context.Background(), cwd, "ShareFile", `{"path":"../secret.txt"}`)
	if !strings.Contains(out, "path escapes working directory") {
		t.Fatalf("expected path escape rejection, got: %s", out)
	}
}

func TestWebFetchToolFetchesAndTruncatesHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("abcdefghijklmnopqrstuvwxyz"))
	}))
	defer server.Close()

	out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch", `{"url":"`+server.URL+`","max_length":10}`)
	if !strings.Contains(out, "Status: 200 OK") {
		t.Fatalf("expected status line, got: %s", out)
	}
	if !strings.Contains(out, "abcdefghij") || !strings.Contains(out, "[truncated]") {
		t.Fatalf("expected truncated response body, got: %s", out)
	}
}

func TestWebFetchToolRejectsNonHTTPURL(t *testing.T) {
	out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch", `{"url":"file:///etc/passwd"}`)
	if !strings.Contains(out, "only http and https") {
		t.Fatalf("expected non-HTTP rejection, got: %s", out)
	}
}

func TestChatReasoningEffortFiltersUnsupportedValues(t *testing.T) {
	if got := chatReasoningEffort("gpt-5-test", "invalid"); got != "" {
		t.Fatalf("expected invalid effort to be dropped, got %q", got)
	}
	if got := chatReasoningEffort("gpt-4o-mini", "high"); got != "" {
		t.Fatalf("expected unsupported model effort to be dropped, got %q", got)
	}
	if got := chatReasoningEffort("gpt-5-test", "none"); got != "" {
		t.Fatalf("expected none effort to be omitted, got %q", got)
	}
}

func TestAnthropicToolLoopHonorsMaxTurns(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{
				{
					"type":  "tool_use",
					"id":    "tool-1",
					"name":  "Bash",
					"input": map[string]any{"command": "ignored"},
				},
			},
		})
	}))
	defer server.Close()

	store := testConfigStore(t, map[string]any{
		"anthropicProviders": []any{
			map[string]any{
				"prefix":       "anthropic",
				"apiKey":       "test-key",
				"baseUrl":      server.URL,
				"defaultModel": "claude-test",
			},
		},
	})
	_, err := Generate(
		context.Background(),
		store,
		"anthropic:claude-test",
		[]Message{{Role: "user", Content: "please loop"}},
		t.TempDir(),
		WithSystemPrompt("custom system prompt"),
		WithMaxTurns(1),
		WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
			return ToolRun{Output: "tool output"}, nil
		}),
	)
	if err == nil || !strings.Contains(err.Error(), "maximum rounds") {
		t.Fatalf("expected max rounds error, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("expected one request due to maxTurns=1, got %d", requests)
	}
}
