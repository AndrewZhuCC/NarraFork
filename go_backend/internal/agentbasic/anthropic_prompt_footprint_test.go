package agentbasic

import "testing"

// TestAnthropicSnapshotPromptFootprint verifies the non-streaming snapshot sets
// PromptTokens to the full prompt footprint (input + cache_read + cache_creation),
// mirroring TS promptTokensFromAnthropicPayload. InputTokens stays the raw input
// so the DB input_tokens column and cost calculation are unaffected.
func TestAnthropicSnapshotPromptFootprint(t *testing.T) {
	usage := &anthropicUsage{
		InputTokens:              100,
		OutputTokens:             20,
		CacheReadInputTokens:     40,
		CacheCreationInputTokens: 10,
	}
	snap := anthropicUsageSnapshot(usage)
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snap.InputTokens != 100 {
		t.Fatalf("InputTokens must stay raw input, got %d", snap.InputTokens)
	}
	if snap.PromptTokens != 150 {
		t.Fatalf("PromptTokens must be footprint input+cacheRead+cacheCreation=150, got %d", snap.PromptTokens)
	}
}

// TestAnthropicSnapshotFootprintDerivesCacheCreation verifies the footprint uses
// the 5m+1h breakdown when cache_creation_input_tokens is not sent directly,
// mirroring TS promptTokensFromAnthropicPayload's nullish fallback.
func TestAnthropicSnapshotFootprintDerivesCacheCreation(t *testing.T) {
	usage := &anthropicUsage{InputTokens: 100, CacheReadInputTokens: 40}
	usage.CacheCreation.Ephemeral5mInputTokens = 7
	usage.CacheCreation.Ephemeral1hInputTokens = 3
	snap := anthropicUsageSnapshot(usage)
	if snap.PromptTokens != 150 {
		t.Fatalf("footprint must derive cache_creation from 5m+1h (100+40+10=150), got %d", snap.PromptTokens)
	}
}

// TestAnthropicAccumSnapshotFootprint verifies the streaming accumulator snapshot
// also reports the full prompt footprint, and that it feeds usageContextPercent.
func TestAnthropicAccumSnapshotFootprint(t *testing.T) {
	a := &anthropicUsageAccum{
		inputTokens:              90000,
		cachedInputTokens:        5000,
		cacheCreationInputTokens: 5000,
		outputTokens:             100,
	}
	snap := a.snapshot()
	if snap.PromptTokens != 100000 {
		t.Fatalf("accum footprint must be 90000+5000+5000=100000, got %d", snap.PromptTokens)
	}
	snap.ContextWindow = 200000
	pct := usageContextPercent(snap)
	if pct < 49.9 || pct > 50.1 {
		t.Fatalf("context percent must use full footprint (100000/200000=50%%), got %.2f", pct)
	}
}
