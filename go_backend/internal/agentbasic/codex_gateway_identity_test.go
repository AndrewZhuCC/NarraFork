package agentbasic

import (
	"context"
	"testing"
)

// TestParseGatewayDataEventModelCatalog verifies the modelCatalogEvent gateway
// payload maps to NugModelCatalog (mirrors TS parseGatewaySSEEvent MODEL_CATALOG).
func TestParseGatewayDataEventModelCatalog(t *testing.T) {
	event, ok := parseGatewayDataEvent(map[string]any{
		"type":      "modelCatalogEvent",
		"modelHash": "abc123",
		"models":    []any{map[string]any{"id": "m1"}, "not-an-object", map[string]any{"id": "m2"}},
	})
	if !ok || event.NugModelCatalog == nil {
		t.Fatalf("expected NugModelCatalog, got ok=%v event=%#v", ok, event)
	}
	if event.NugModelCatalog.ModelHash != "abc123" {
		t.Fatalf("unexpected modelHash %q", event.NugModelCatalog.ModelHash)
	}
	if len(event.NugModelCatalog.Models) != 2 {
		t.Fatalf("non-object model entries should be dropped, got %#v", event.NugModelCatalog.Models)
	}
	// Missing models array → not a gateway event.
	if _, ok := parseGatewayDataEvent(map[string]any{"type": "modelCatalogEvent"}); ok {
		t.Fatalf("modelCatalogEvent without models must not be recognized")
	}
}

// TestParseGatewayDataEventImageCacheAck verifies imageCacheAckEvent maps to
// NugImageCacheAck.
func TestParseGatewayDataEventImageCacheAck(t *testing.T) {
	event, ok := parseGatewayDataEvent(map[string]any{
		"type": "imageCacheAckEvent",
		"refs": []any{"ref1", "ref2", ""},
	})
	if !ok || event.NugImageCacheAck == nil {
		t.Fatalf("expected NugImageCacheAck, got ok=%v event=%#v", ok, event)
	}
	if len(event.NugImageCacheAck.Refs) != 2 {
		t.Fatalf("empty refs should be dropped, got %#v", event.NugImageCacheAck.Refs)
	}
	if _, ok := parseGatewayDataEvent(map[string]any{"type": "imageCacheAckEvent"}); ok {
		t.Fatalf("imageCacheAckEvent without refs must not be recognized")
	}
}

// TestParseGatewayDataEventQueueAndQuota verifies queue/quota events reuse the
// shared snapshot shapes.
func TestParseGatewayDataEventQueueAndQuota(t *testing.T) {
	queue, ok := parseGatewayDataEvent(map[string]any{"type": "queueEvent", "position": 3, "queueDepth": 10, "queueMessage": "waiting"})
	if !ok || queue.QueueStatus == nil || queue.QueueStatus.Position != 3 || queue.QueueStatus.QueueDepth != 10 || queue.QueueStatus.QueueMessage != "waiting" {
		t.Fatalf("unexpected queue event: ok=%v %#v", ok, queue.QueueStatus)
	}
	quota, ok := parseGatewayDataEvent(map[string]any{"type": "quotaBalanceEvent", "quotaBalance": "42", "detailedQuotaBalance": "details"})
	if !ok || quota.QuotaBalance == nil || *quota.QuotaBalance != "42" || quota.DetailedQuotaBalance == nil || *quota.DetailedQuotaBalance != "details" {
		t.Fatalf("unexpected quota event: ok=%v balance=%v detailed=%v", ok, quota.QuotaBalance, quota.DetailedQuotaBalance)
	}
	// Unknown type → not a gateway event.
	if _, ok := parseGatewayDataEvent(map[string]any{"type": "response.completed"}); ok {
		t.Fatalf("non-gateway type must not be recognized")
	}
}

// TestHandleResponsesJSONEventCapturesMessageID verifies the assistant message
// item id is surfaced as ParsedStreamEvent.MessageID (TS messageId parity).
func TestHandleResponsesJSONEventCapturesMessageID(t *testing.T) {
	out := make(chan StreamEvent, 8)
	toolsByItem := map[string]*openAIStreamingTool{}
	reasoningByIndex := map[int]*responsesReasoningAccum{}
	input := `{"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","id":"msg_abc"}}`
	if err := handleResponsesJSONEvent(context.Background(), out, toolsByItem, reasoningByIndex, []byte(input)); err != nil {
		t.Fatalf("handle event: %v", err)
	}
	close(out)
	seen := ""
	for event := range out {
		if event.Parsed.MessageID != "" {
			seen = event.Parsed.MessageID
		}
	}
	if seen != "msg_abc" {
		t.Fatalf("expected MessageID msg_abc, got %q", seen)
	}
}

// TestHandleResponsesJSONEventTopLevelUsage verifies a top-level usage (not on
// response.completed) is surfaced (TS chunk.usage extraction parity).
func TestHandleResponsesJSONEventTopLevelUsage(t *testing.T) {
	out := make(chan StreamEvent, 8)
	toolsByItem := map[string]*openAIStreamingTool{}
	reasoningByIndex := map[int]*responsesReasoningAccum{}
	input := `{"type":"response.output_text.delta","delta":"x","output_index":0,"usage":{"input_tokens":11,"output_tokens":4}}`
	if err := handleResponsesJSONEvent(context.Background(), out, toolsByItem, reasoningByIndex, []byte(input)); err != nil {
		t.Fatalf("handle event: %v", err)
	}
	close(out)
	var usage *UsageSnapshot
	for event := range out {
		if event.Parsed.Usage != nil {
			usage = event.Parsed.Usage
		}
	}
	if usage == nil || usage.InputTokens != 11 || usage.CompletionTokens != 4 {
		t.Fatalf("expected top-level usage {input:11,output:4}, got %#v", usage)
	}
}

// TestOpenAIInjectSystemPromptPrependsIdentity verifies identity injection for
// OpenAI (system role), codex (developer role), and cline.
func TestOpenAIInjectSystemPromptPrependsIdentity(t *testing.T) {
	t.Run("openai uses system role and OPENAI identity", func(t *testing.T) {
		adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Kind: "openai"}}
		history := &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}
		adapter.InjectSystemPrompt(history, "BASE PROMPT", "gpt-4o", "en")
		first, _ := history.History[0].(Message)
		if first.Role != "system" {
			t.Fatalf("openai identity must use system role, got %q", first.Role)
		}
		if !contains(first.Content, openAIIdentityPrompts["en"]) || !contains(first.Content, "BASE PROMPT") {
			t.Fatalf("identity+prompt not merged: %q", first.Content)
		}
	})
	t.Run("codex uses developer role and CODEX identity", func(t *testing.T) {
		adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Kind: "codex", APIMode: "codex"}}
		history := &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}
		adapter.InjectSystemPrompt(history, "BASE", "gpt-5-codex", "zh-CN")
		first, _ := history.History[0].(Message)
		if first.Role != "developer" {
			t.Fatalf("codex identity must use developer role, got %q", first.Role)
		}
		if !contains(first.Content, "NarraFork 叙述者") {
			t.Fatalf("codex zh-CN identity not used: %q", first.Content)
		}
	})
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestCodexWebSearchEnabledForScope verifies web_search gating by toolScope +
// UseWebSearch flag.
func TestCodexWebSearchEnabledForScope(t *testing.T) {
	on := true
	off := false
	if !codexWebSearchEnabledForScope(Provider{UseWebSearch: &on}, "normal") {
		t.Fatalf("normal scope with UseWebSearch=true should enable web_search")
	}
	if codexWebSearchEnabledForScope(Provider{UseWebSearch: &off}, "normal") {
		t.Fatalf("UseWebSearch=false must disable web_search")
	}
	if codexWebSearchEnabledForScope(Provider{UseWebSearch: &on}, "reflection") {
		t.Fatalf("reflection scope hides WebSearch, so native web_search must be disabled")
	}
	// nil UseWebSearch defaults to true.
	if !codexWebSearchEnabledForScope(Provider{}, "normal") {
		t.Fatalf("nil UseWebSearch should default to enabled in normal scope")
	}
}

// TestAnthropicNativeWebSearchEnabledForScope verifies the scope gate.
func TestAnthropicNativeWebSearchEnabledForScope(t *testing.T) {
	if !anthropicNativeWebSearchEnabledForScope(Provider{}, "normal") {
		t.Fatalf("normal scope should allow native web_search")
	}
	if anthropicNativeWebSearchEnabledForScope(Provider{}, "reflection") {
		t.Fatalf("reflection scope must not allow native web_search")
	}
}

// TestAnthropicExtractWebSearchQuery verifies query extraction from partial JSON.
func TestAnthropicExtractWebSearchQuery(t *testing.T) {
	if got := anthropicExtractWebSearchQuery(`{"query":"golang ws"`); got != "golang ws" {
		t.Fatalf("expected 'golang ws', got %q", got)
	}
	if got := anthropicExtractWebSearchQuery(`{"query":"escaped \"quote\""}`); got != `escaped "quote"` {
		t.Fatalf("expected unescaped query, got %q", got)
	}
	if got := anthropicExtractWebSearchQuery(`{"foo":`); got != "" {
		t.Fatalf("incomplete json should yield empty query, got %q", got)
	}
}
