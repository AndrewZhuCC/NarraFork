package narrators

import (
	"testing"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/ws"
)

func TestAgentEventBroadcastsFrontendUseNarratorWSFields(t *testing.T) {
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
		Type:     "metering",
		Metadata: map[string]any{"unit": "request", "unitPlural": "requests", "usage": float64(3)},
	})
	service.broadcastAgentEvent("nar-frontend", agentbasic.AgentEvent{
		Type:       "retrying",
		Message:    "provider busy",
		Attempt:    2,
		MaxRetries: 5,
		DelayMs:    750,
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
	if metering["unit"] != "request" || metering["unitPlural"] != "requests" || intNumber(metering["usage"]) != 3 {
		t.Fatalf("metering should expose useNarratorWS fields: %#v", metering)
	}
	warning := requireFrameType(t, frames, "warning")
	if warning["message"] != "provider busy" || intNumber(warning["retryCount"]) != 2 || intNumber(warning["maxRetries"]) != 5 || intNumber(warning["delayMs"]) != 750 {
		t.Fatalf("retrying should fan out useNarratorWS warning frame: %#v", warning)
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
