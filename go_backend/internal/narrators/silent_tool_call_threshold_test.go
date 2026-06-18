package narrators

import (
	"testing"

	"narrafork-go/internal/config"
)

// TestAgentSilentToolCallThresholdReadsSettings verifies the silent-tool-call
// wrap-up threshold is read from agent.silentToolCallThreshold (mirroring the TS
// getSilentToolCallThreshold = settings.agent.silentToolCallThreshold), with the
// default of 20 when absent and the loop's normalize clamp (value < -1 -> -1,
// where -1 disables the wrap-up). Previously the Go loop ignored the setting and
// always used the hardcoded 20.
func TestAgentSilentToolCallThresholdReadsSettings(t *testing.T) {
	load := func(t *testing.T, value any) *config.Store {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		store, err := config.Load()
		if err != nil {
			t.Fatalf("load settings: %v", err)
		}
		if value != nil {
			if _, err := store.Patch(map[string]any{
				"agent": map[string]any{"silentToolCallThreshold": value},
			}); err != nil {
				t.Fatalf("patch settings: %v", err)
			}
		}
		return store
	}

	// Default (unpatched) -> 20, matching TS defaults.ts.
	if got := (&Service{Settings: load(t, nil)}).agentSilentToolCallThreshold(); got != 20 {
		t.Fatalf("default silentToolCallThreshold = %d, want 20", got)
	}
	// nil Settings -> fallback 20.
	if got := (&Service{}).agentSilentToolCallThreshold(); got != 20 {
		t.Fatalf("nil-settings silentToolCallThreshold = %d, want 20", got)
	}
	// Custom positive value is honored (not the hardcoded 20).
	if got := (&Service{Settings: load(t, float64(50))}).agentSilentToolCallThreshold(); got != 50 {
		t.Fatalf("custom silentToolCallThreshold = %d, want 50", got)
	}
	// -1 disables the wrap-up and is preserved.
	if got := (&Service{Settings: load(t, float64(-1))}).agentSilentToolCallThreshold(); got != -1 {
		t.Fatalf("-1 silentToolCallThreshold = %d, want -1 (disabled)", got)
	}
	// 0 (immediate wrap-up) is honored.
	if got := (&Service{Settings: load(t, float64(0))}).agentSilentToolCallThreshold(); got != 0 {
		t.Fatalf("0 silentToolCallThreshold = %d, want 0", got)
	}
}
