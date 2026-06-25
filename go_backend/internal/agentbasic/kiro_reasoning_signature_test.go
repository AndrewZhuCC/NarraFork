package agentbasic

import "testing"

// TestKiroReasoningContentEventCapturesSignatureAndRedacted pins the
// reasoningContentEvent parity fix (provider_kiro.go): a Kiro reasoning chunk on
// the Anthropic-backed channel carries thinking text plus a signature and/or a
// base64 redactedContent block, and all three must survive (mirrors TS
// kiro-protocol.ts:807-831). Previously Go dropped signature + redactedContent.
func TestKiroReasoningContentEventCapturesSignatureAndRedacted(t *testing.T) {
	parsed := parseKiroEvent("reasoningContentEvent", map[string]any{
		"content":         "thinking about it",
		"signature":       "sig_abc",
		"redactedContent": "redacted_blob",
	}, false)

	if parsed.Reasoning != "thinking about it" {
		t.Fatalf("reasoning text = %q, want %q", parsed.Reasoning, "thinking about it")
	}
	if got := anthropicSignature(parsed.ReasoningMetadata); got != "sig_abc" {
		t.Fatalf("reasoning signature = %q, want %q (metadata=%#v)", got, "sig_abc", parsed.ReasoningMetadata)
	}
	if parsed.RedactedThinking == nil || parsed.RedactedThinking.Data != "redacted_blob" {
		t.Fatalf("redacted thinking = %#v, want data=redacted_blob", parsed.RedactedThinking)
	}
}

// TestKiroReasoningContentEventSnakeCaseAndAliasKeys verifies the snake_case
// redacted_content alias and the reasoningSignature alias are read (TS reads
// data.signature ?? data.reasoningSignature and data.redactedContent ??
// data.redacted_content), and that a plain reasoning chunk (text-only) adds no
// signature/redacted noise.
func TestKiroReasoningContentEventSnakeCaseAndAliasKeys(t *testing.T) {
	parsed := parseKiroEvent("reasoningContentEvent", map[string]any{
		"text":               "more thought",
		"reasoningSignature": "sig_alias",
		"redacted_content":   "redacted_alias",
	}, false)
	if parsed.Reasoning != "more thought" {
		t.Fatalf("reasoning text = %q", parsed.Reasoning)
	}
	if got := anthropicSignature(parsed.ReasoningMetadata); got != "sig_alias" {
		t.Fatalf("reasoning signature alias = %q, want sig_alias", got)
	}
	if parsed.RedactedThinking == nil || parsed.RedactedThinking.Data != "redacted_alias" {
		t.Fatalf("redacted alias = %#v", parsed.RedactedThinking)
	}

	textOnly := parseKiroEvent("reasoningContentEvent", map[string]any{"content": "just text"}, false)
	if textOnly.ReasoningMetadata != nil {
		t.Fatalf("text-only reasoning should carry no metadata, got %#v", textOnly.ReasoningMetadata)
	}
	if textOnly.RedactedThinking != nil {
		t.Fatalf("text-only reasoning should carry no redacted block, got %#v", textOnly.RedactedThinking)
	}
}

// TestBuildKiroReasoningContent pins buildKiroReasoningContent against TS
// buildKiroReasoningContent (kiro-protocol.ts:60-88): concatenate reasoning text
// with "\n", keep the last signed signature, carry the first redacted block, and
// return nil when there is nothing to echo.
func TestBuildKiroReasoningContent(t *testing.T) {
	if got := buildKiroReasoningContent(nil); got != nil {
		t.Fatalf("empty blocks should yield nil, got %#v", got)
	}

	blocks := []map[string]any{
		{"type": "reasoning", "text": "first", "providerMetadata": map[string]any{"anthropic": map[string]any{"signature": "sig_1"}}},
		{"type": "reasoning", "text": "second", "providerMetadata": map[string]any{"anthropic": map[string]any{"signature": "sig_2"}}},
		{"type": "redacted_thinking", "data": "blob"},
		{"type": "text", "text": "visible"},
	}
	rc := buildKiroReasoningContent(blocks)
	if rc == nil {
		t.Fatal("expected reasoning content")
	}
	reasoningText, _ := rc["reasoningText"].(map[string]any)
	if reasoningText == nil || reasoningText["text"] != "first\nsecond" {
		t.Fatalf("reasoningText.text = %#v, want first\\nsecond", reasoningText)
	}
	if reasoningText["signature"] != "sig_2" {
		t.Fatalf("signature = %#v, want sig_2 (last signed)", reasoningText["signature"])
	}
	if rc["redactedContent"] != "blob" {
		t.Fatalf("redactedContent = %#v, want blob", rc["redactedContent"])
	}

	// redacted-only block still produces content (no reasoningText).
	redactedOnly := buildKiroReasoningContent([]map[string]any{{"type": "redacted_thinking", "data": "only"}})
	if redactedOnly == nil || redactedOnly["redactedContent"] != "only" {
		t.Fatalf("redacted-only = %#v", redactedOnly)
	}
	if _, has := redactedOnly["reasoningText"]; has {
		t.Fatalf("redacted-only should not set reasoningText, got %#v", redactedOnly)
	}
}

// TestKiroBuildHistoryReplaysReasoningContent verifies BuildHistory echoes a prior
// assistant turn's reasoning into reasoning_content and rebuilds the visible
// content from text blocks only (no reasoning duplication), mirroring TS
// buildKiroHistory reasoning_content replay.
func TestKiroBuildHistoryReplaysReasoningContent(t *testing.T) {
	adapter := &KiroProviderAdapter{Provider: Provider{Model: "claude-sonnet-4.5"}}
	built, err := adapter.BuildHistory([]Message{
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "answer",
			ContentBlocks: []map[string]any{
				{"type": "reasoning", "text": "deliberating", "providerMetadata": map[string]any{"anthropic": map[string]any{"signature": "sig_x"}}},
				{"type": "text", "text": "answer"},
			},
		},
		{Role: "user", Content: "follow up"},
	}, "claude-sonnet-4.5", "")
	if err != nil {
		t.Fatal(err)
	}
	// history[0] = user "hello", history[1] = assistant
	if len(built.History) < 2 {
		t.Fatalf("expected at least 2 history entries, got %#v", built.History)
	}
	assistant, _ := built.History[1].(map[string]any)
	body, _ := assistant["assistantResponseMessage"].(map[string]any)
	if body == nil {
		t.Fatalf("history[1] is not an assistant message: %#v", built.History[1])
	}
	if body["content"] != "answer" {
		t.Fatalf("assistant content = %#v, want \"answer\" (text-only)", body["content"])
	}
	rc, _ := body["reasoning_content"].(map[string]any)
	if rc == nil {
		t.Fatalf("assistant message missing reasoning_content: %#v", body)
	}
	reasoningText, _ := rc["reasoningText"].(map[string]any)
	if reasoningText == nil || reasoningText["text"] != "deliberating" || reasoningText["signature"] != "sig_x" {
		t.Fatalf("reasoning_content.reasoningText = %#v", reasoningText)
	}
}

// TestKiroQueueEventGatingMatchesTS pins the queueEvent gating + queueMessage
// field-name parity with TS gateway-events.ts: a queueMessage-only event still
// produces a snapshot, and an empty event (no position/message) yields none.
func TestKiroQueueEventGatingMatchesTS(t *testing.T) {
	withMessage := parseKiroEvent("queueEvent", map[string]any{"queueMessage": "Waiting for capacity"}, false)
	if withMessage.QueueStatus == nil || withMessage.QueueStatus.QueueMessage != "Waiting for capacity" {
		t.Fatalf("queueMessage-only event = %#v", withMessage.QueueStatus)
	}
	empty := parseKiroEvent("queueEvent", map[string]any{}, false)
	if empty.QueueStatus != nil {
		t.Fatalf("empty queue event should yield no snapshot, got %#v", empty.QueueStatus)
	}
}
