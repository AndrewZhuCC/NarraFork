package agentbasic

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/config"
)

func TestDefaultModelSmokeWithLocalSettings(t *testing.T) {
	if os.Getenv("NARRAFORK_RUN_DEFAULT_MODEL_SMOKE") != "1" {
		t.Skip("set NARRAFORK_RUN_DEFAULT_MODEL_SMOKE=1 to run a real smoke test against the configured default model")
	}
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load local settings: %v", err)
	}
	modelRef := defaultModelSmokeRef(store, os.Getenv("NARRAFORK_DEFAULT_MODEL_SMOKE_MODEL"))
	if strings.TrimSpace(modelRef) == "" {
		t.Fatalf("no default model configured for smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := Generate(
		ctx,
		store,
		modelRef,
		[]Message{{Role: "user", Content: "Reply with OK only."}},
		t.TempDir(),
		WithMaxTurns(1),
		WithMaxChatRetries(0),
		WithEmptyResponseRetries(0),
		WithRetryBaseDelay(0),
	)
	if err != nil {
		t.Fatalf("default model smoke failed for %q: %v", modelRef, err)
	}
	if strings.TrimSpace(result.Text) == "" {
		t.Fatalf("default model smoke returned empty text for %q", modelRef)
	}
}

func TestDefaultModelSmokeRefUsesOverrideOrConfiguredDefault(t *testing.T) {
	store := testConfigStore(t, map[string]any{
		"agent": map[string]any{"defaultModel": "nkp:claude-haiku-4.5"},
	})
	if got := defaultModelSmokeRef(store, " openai:gpt-test "); got != "openai:gpt-test" {
		t.Fatalf("override model = %q", got)
	}
	if got := defaultModelSmokeRef(store, ""); got != "nkp:claude-haiku-4.5" {
		t.Fatalf("configured default model = %q", got)
	}
}

func defaultModelSmokeRef(store *config.Store, override string) string {
	if model := strings.TrimSpace(override); model != "" {
		return model
	}
	if store == nil {
		return config.DefaultAgentModel
	}
	return strings.TrimSpace(store.Core().Agent.DefaultModel)
}
