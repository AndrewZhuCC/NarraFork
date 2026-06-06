package narrators

import (
	"testing"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/ws"
)

func TestAgentEventBroadcastsFrontendMeterQuotaQueueUseNarratorWSFields(t *testing.T) {
	hub := ws.NewHub()
	frames := []map[string]any{}
	hub.SetNarratorBroadcastHook(func(_ string, payload any, _ []byte) {
		if item, ok := payload.(map[string]any); ok {
			frames = append(frames, item)
		}
	})
	service := &Service{Hub: hub}
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:       "context_usage",
		Usage:      &agentbasic.UsageSnapshot{PromptTokens: 50, InputTokens: 50, ContextWindow: 100, Estimated: true},
		Percentage: 0,
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:        "web_search",
		ToolUseID:   "search-1",
		Message:     "completed",
		OutputIndex: intPtrForFrontendContract(1),
		Metadata:    map[string]any{"query": "narrafork", "queries": []string{"narrafork", "go backend"}, "action": "search"},
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:        "image_generation",
		ToolUseID:   "image-1",
		Message:     "completed",
		OutputIndex: intPtrForFrontendContract(2),
		Metadata:    map[string]any{"revisedPrompt": "draw a graph", "result": "image-data"},
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type: "metering",
		Metadata: map[string]any{
			"unit":       "request",
			"unitPlural": "requests",
			"usage":      float64(3),
			"limit":      float64(10),
		},
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:     "quota_balance",
		Message:  "12 credits",
		Provider: "kiro",
		Model:    "claude-sonnet-4.5",
		Metadata: map[string]any{
			"quotaBalance":         "12 credits",
			"detailedQuotaBalance": "12 of 20 credits",
		},
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:     "nkp_quota_balance",
		Metadata: map[string]any{"quotaBalance": "8"},
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:     "nkp_queue_status",
		Message:  "waiting",
		Metadata: map[string]any{"position": float64(2), "queueDepth": float64(5)},
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:     "queue_status",
		Message:  "Waiting for capacity",
		Metadata: map[string]any{"position": float64(3), "queueDepth": float64(8)},
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:       "retrying",
		Message:    "provider busy",
		Attempt:    2,
		MaxRetries: 5,
		DelayMs:    750,
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:            "tool_call",
		ToolUseID:       "tool-1",
		ToolName:        "Bash",
		Input:           map[string]any{"command": "go test ./..."},
		StreamStartedAt: int64(1770000000000),
		ParentToolUseID: "agent-1",
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:      "tool_use_chunk",
		ToolUseID: "tool-1",
		ToolName:  "Bash",
		Streaming: &agentbasic.ToolUseChunk{
			ToolUseID:            "tool-1",
			Name:                 "Bash",
			InputCharsTotal:      24,
			ExtractedFilePath:    "main.go",
			ContentCharsReceived: 12,
			ExtractedFields:      map[string]string{"command": "go test ./..."},
			StreamingFieldName:   "command",
			StreamingFieldDelta:  "go test",
		},
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{Type: "tool_output", ToolUseID: "tool-1", Output: "live"})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{Type: "tool_progress", ToolUseID: "tool-1", Elapsed: 5})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{Type: "tool_long_running", ToolUseID: "tool-1", Elapsed: 60000})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:         "tool_result",
		ToolUseID:    "tool-1",
		ToolName:     "Bash",
		Output:       "ok",
		DurationMs:   42,
		UpdatedInput: map[string]any{"command": "go test ./internal/agentbasic"},
		Metadata:     map[string]any{"truncated": false},
		SideCars:     []agentbasic.AgentSideCar{{Target: "tool_result", Source: "test", Content: "detail"}},
	})

	contextUsage := requireFrameType(t, frames, "context_usage")
	if intNumber(contextUsage["promptTokens"]) != 50 || intNumber(contextUsage["contextWindow"]) != 100 || contextUsage["isEstimated"] != true || intNumber(contextUsage["percentage"]) != 50 {
		t.Fatalf("context_usage should expose useNarratorWS top-level fields: %#v", contextUsage)
	}
	webSearch := requireFrameType(t, frames, "web_search")
	if webSearch["id"] != "search-1" || webSearch["status"] != "completed" || webSearch["query"] != "narrafork" || intNumber(webSearch["outputIndex"]) != 1 {
		t.Fatalf("web_search should expose useNarratorWS fields: %#v", webSearch)
	}
	imageGeneration := requireFrameType(t, frames, "image_generation")
	if imageGeneration["id"] != "image-1" || imageGeneration["status"] != "completed" || imageGeneration["revisedPrompt"] != "draw a graph" || intNumber(imageGeneration["outputIndex"]) != 2 {
		t.Fatalf("image_generation should expose useNarratorWS fields: %#v", imageGeneration)
	}
	metering := requireFrameType(t, frames, "metering")
	meteringMetadata, _ := metering["metadata"].(map[string]any)
	if metering["unit"] != "request" || metering["unitPlural"] != "requests" || intNumber(metering["usage"]) != 3 || intNumber(meteringMetadata["limit"]) != 10 {
		t.Fatalf("metering should expose useNarratorWS fields and preserve metadata: %#v", metering)
	}
	quota := requireFrameType(t, frames, "quota_balance")
	if quota["quotaBalance"] != "12 credits" || quota["detailedQuotaBalance"] != "12 of 20 credits" || quota["provider"] != "kiro" || quota["model"] != "claude-sonnet-4.5" {
		t.Fatalf("quota_balance should expose current frontend fields: %#v", quota)
	}
	nkpQuota := requireFrameType(t, frames, "nkp_quota_balance")
	if nkpQuota["quotaBalance"] != "8" {
		t.Fatalf("nkp_quota_balance should expose quotaBalance: %#v", nkpQuota)
	}
	nkpQueue := requireFrameType(t, frames, "nkp_queue_status")
	if intNumber(nkpQueue["position"]) != 2 || intNumber(nkpQueue["queueDepth"]) != 5 {
		t.Fatalf("nkp_queue_status should expose queue position/depth: %#v", nkpQueue)
	}
	queue := requireFrameType(t, frames, "queue_status")
	if intNumber(queue["position"]) != 3 || intNumber(queue["queueDepth"]) != 8 || queue["queueMessage"] != "Waiting for capacity" {
		t.Fatalf("queue_status should expose queue position/depth/message: %#v", queue)
	}
	warning := requireFrameType(t, frames, "warning")
	if warning["message"] != "provider busy" || intNumber(warning["retryCount"]) != 2 || intNumber(warning["maxRetries"]) != 5 || intNumber(warning["delayMs"]) != 750 {
		t.Fatalf("retrying should fan out useNarratorWS warning frame: %#v", warning)
	}
	started := requireFrameType(t, frames, "tool_started")
	if started["toolUseId"] != "tool-1" || intNumber(started["streamStartedAt"]) != 1770000000000 || started["input"] == nil || started["parentToolUseId"] != "agent-1" {
		t.Fatalf("tool_started should expose streamStartedAt/input/parentToolUseId: %#v", started)
	}
	chunk := requireFrameType(t, frames, "tool_use_chunk")
	streamingField, _ := chunk["streamingField"].(map[string]any)
	if intNumber(chunk["inputCharsTotal"]) != 24 || chunk["extractedFields"] == nil || streamingField["name"] != "command" || streamingField["delta"] != "go test" {
		t.Fatalf("tool_use_chunk should expose streaming field extraction: %#v", chunk)
	}
	output := requireFrameType(t, frames, "tool_output")
	if output["output"] != "live" {
		t.Fatalf("tool_output should expose live output: %#v", output)
	}
	progress := requireFrameType(t, frames, "tool_progress")
	if intNumber(progress["elapsed"]) != 5 {
		t.Fatalf("tool_progress should expose elapsed: %#v", progress)
	}
	longRunning := requireFrameType(t, frames, "tool_long_running")
	if intNumber(longRunning["elapsed"]) != 60000 {
		t.Fatalf("tool_long_running should expose elapsed: %#v", longRunning)
	}
	completed := requireFrameType(t, frames, "tool_completed")
	if completed["toolUseId"] != "tool-1" || completed["output"] != "ok" || intNumber(completed["durationMs"]) != 42 || completed["updatedInput"] == nil || completed["metadata"] == nil || completed["sideCars"] == nil {
		t.Fatalf("tool_completed should preserve TS optional fields: %#v", completed)
	}
}

func requireFrameType(t *testing.T, frames []map[string]any, typ string) map[string]any {
	t.Helper()
	for _, frame := range frames {
		if frame["type"] == typ {
			return frame
		}
	}
	t.Fatalf("missing frame type %s in %#v", typ, frames)
	return nil
}

func intPtrForFrontendContract(value int) *int { return &value }

func intNumber(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
