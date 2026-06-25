package narrators

import (
	"context"
	"strings"
	"testing"

	"narrafork-go/internal/agentbasic"
)

// TestCleanProviderErrorMessageForDisplay covers the display-layer extraction
// helper in isolation, mirroring TS extractApiErrorMessage
// (server/lib/agent/openai-provider.ts:1346) while preserving the
// "<provider> provider returned status <N>: " prefix that Go's
// providerHTTPError.Error() emits.
func TestCleanProviderErrorMessageForDisplay(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		want       string
		wantSubstr string // when set, assert Contains instead of equality
		notSubstr  string // when set, assert NOT Contains
	}{
		{
			name: "standard openai error extracts message and keeps prefix",
			raw:  `openai provider returned status 400: {"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`,
			want: "openai provider returned status 400: invalid api key",
		},
		{
			name:       "standard openai error drops raw json envelope",
			raw:        `openai provider returned status 400: {"error":{"message":"invalid api key","type":"invalid_request_error"}}`,
			notSubstr:  `{"error"`,
			wantSubstr: "invalid api key",
		},
		{
			name: "flat message format",
			raw:  `anthropic provider returned status 429: {"message":"flat msg"}`,
			want: "anthropic provider returned status 429: flat msg",
		},
		{
			name: "no message field keeps raw for diagnostics",
			raw:  `openai provider returned status 400: {"error":{"code":"x"}}`,
			want: `openai provider returned status 400: {"error":{"code":"x"}}`,
		},
		{
			name: "non json passes through unchanged",
			raw:  "final success status blocked",
			want: "final success status blocked",
		},
		{
			name: "empty string passes through",
			raw:  "",
			want: "",
		},
		{
			name: "prefix message suffix all preserved",
			raw:  `prefix {"error":{"message":"boom"}} trailing`,
			want: "prefix boom trailing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanProviderErrorMessageForDisplay(tc.raw)
			if tc.want != "" && got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if tc.want == "" && tc.wantSubstr == "" && tc.notSubstr == "" && got != "" {
				t.Fatalf("got %q want empty", got)
			}
			if tc.wantSubstr != "" && !strings.Contains(got, tc.wantSubstr) {
				t.Fatalf("got %q should contain %q", got, tc.wantSubstr)
			}
			if tc.notSubstr != "" && strings.Contains(got, tc.notSubstr) {
				t.Fatalf("got %q should NOT contain %q", got, tc.notSubstr)
			}
		})
	}
}

// TestCleanProviderErrorMessageForDisplayTruncates verifies the 1000-rune
// defensive cap (well below the 64KB raw body Go could otherwise surface).
func TestCleanProviderErrorMessageForDisplayTruncates(t *testing.T) {
	longMsg := strings.Repeat("x", 2000)
	raw := `openai provider returned status 400: {"error":{"message":"` + longMsg + `"}}`
	got := cleanProviderErrorMessageForDisplay(raw)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncated result to end with ellipsis, got len=%d", len([]rune(got)))
	}
	if runes := []rune(got); len(runes) != 1003 {
		t.Fatalf("expected 1000 runes + ellipsis, got %d", len(runes))
	}
}

// TestNarratorErrorDisplayExtractsProviderMessage verifies the end-to-end
// display extraction: a terminal error event carrying a verbose provider body
// persists a clean error_message (no raw JSON envelope) while still flowing
// through finishAgentRunError.
func TestNarratorErrorDisplayExtractsProviderMessage(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	hub, events := captureNarratorBroadcasts()
	service.Hub = hub
	ctx := context.Background()
	narrator, err := service.Get(ctx, narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	event := agentbasic.AgentEvent{
		Type:    "error",
		Message: `anthropic provider returned status 400: {"error":{"message":"bad request detail","type":"invalid_request_error"}}`,
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	if err := service.handleAgentEvent(ctx, state, event); err != nil {
		t.Fatalf("handle error event: %v", err)
	}

	var status, substatus, persistedError string
	if err := service.DB.QueryRow(`SELECT status, substatus, COALESCE(error_message, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &persistedError); err != nil {
		t.Fatalf("query persisted error: %v", err)
	}
	if status != "idle" || substatus != `["error"]` {
		t.Fatalf("unexpected terminal status: status=%s substatus=%s", status, substatus)
	}
	if !strings.Contains(persistedError, "bad request detail") {
		t.Fatalf("persisted error should contain clean message, got %q", persistedError)
	}
	if strings.Contains(persistedError, `{"error"`) {
		t.Fatalf("persisted error should not embed raw JSON envelope, got %q", persistedError)
	}
	// Prefix preserved for context.
	if !strings.Contains(persistedError, "anthropic provider returned status 400") {
		t.Fatalf("persisted error should preserve provider/status prefix, got %q", persistedError)
	}
	broadcast := findBroadcast(events(), narratorID, "narrator_error")
	if broadcast == nil || !strings.Contains(stringFromAny(broadcast["error"]), "bad request detail") || strings.Contains(stringFromAny(broadcast["error"]), `{"error"`) {
		t.Fatalf("narrator_error broadcast should carry clean message, got %#v", broadcast)
	}
}

// TestContextOverflowMatchesBeforeDisplayClean is a regression guard: a verbose
// provider body whose JSON code is context_length_exceeded must still be routed
// to the overflow path. The overflow detection runs on the raw event message
// BEFORE the display clean, so the matching token must survive.
func TestContextOverflowMatchesBeforeDisplayClean(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, events := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}
	ctx := context.Background()
	for i, content := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if _, err := service.addMessage(ctx, narratorID, role, content, nil); err != nil {
			t.Fatalf("add message %d: %v", i, err)
		}
	}
	narrator, err := service.Get(ctx, narratorID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	service.contextOverflowRetry = func(_ context.Context, _ string) {}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	event := agentbasic.AgentEvent{
		Type:    "error",
		Message: `openai provider returned status 400: {"error":{"code":"context_length_exceeded","message":"too long"}}`,
	}
	if err := service.handleAgentEvent(ctx, state, event); err != nil {
		t.Fatalf("handle overflow event: %v", err)
	}
	if !hasBroadcast(events(), narratorID, "context_length_exceeded") || !hasBroadcast(events(), narratorID, "compacting") {
		t.Fatalf("overflow detection must fire on raw body before display clean, got %#v", events())
	}
	if hasBroadcast(events(), narratorID, "narrator_error") {
		t.Fatalf("overflow path should not reach finishAgentRunError, got %#v", events())
	}
	if state.errored {
		t.Fatalf("overflow path should not mark state errored")
	}
}

// TestCancelledErrorMatchesBeforeDisplayClean is a regression guard: the
// cancellation check at handleAgentEvent runs on the raw state.errorMessage
// before the display clean is applied. A "context canceled" terminal error
// must route to the cancelled path (substatus cleared, error_message NULL).
func TestCancelledErrorMatchesBeforeDisplayClean(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	ctx := context.Background()
	narrator, err := service.Get(ctx, narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator}
	if err := service.handleAgentEvent(ctx, state, agentbasic.AgentEvent{Type: "error", Message: "context canceled"}); err != nil {
		t.Fatalf("handle cancelled error: %v", err)
	}
	var status, substatus, persistedError string
	if err := service.DB.QueryRow(`SELECT status, substatus, COALESCE(error_message, '') FROM narrators WHERE id = ?`, narratorID).Scan(&status, &substatus, &persistedError); err != nil {
		t.Fatalf("query cancelled status: %v", err)
	}
	if status != "idle" || substatus != "[]" || persistedError != "" {
		t.Fatalf("cancelled path expected substatus=[] error_message empty, got status=%s substatus=%s error=%q", status, substatus, persistedError)
	}
}
