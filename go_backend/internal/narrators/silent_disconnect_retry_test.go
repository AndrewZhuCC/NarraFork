package narrators

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"narrafork-go/internal/ws"
)

// withFastTransientRetry shrinks the silent-disconnect retry backoff for the
// duration of a test so the retry integration tests do not sleep multiple
// seconds per attempt, restoring the production default afterward.
func withFastTransientRetry(t *testing.T) {
	t.Helper()
	prev := transientRetryBaseDelay
	transientRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { transientRetryBaseDelay = prev })
}

// TestRunBasicAgentSilentDisconnectRetriesThenSucceeds verifies the transient
// silent-disconnect retry (mirrors TS narrator-session.ts:1977): the provider
// stream drops without a final response on the first attempt, and the outer
// session loop re-runs the agent (after backoff) instead of finalizing
// terminally. The second attempt completes normally, so the narrator must end
// idle WITHOUT the silent_disconnect substatus.
func TestRunBasicAgentSilentDisconnectRetriesThenSucceeds(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(chunk string) {
			_, _ = w.Write([]byte(chunk))
			if flusher != nil {
				flusher.Flush()
			}
		}
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			// First attempt: stream some text, then close WITHOUT [DONE] to trigger
			// silent_disconnect.
			write("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
			return
		}
		// Second attempt: a clean completion with a final answer + [DONE].
		write("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"recovered answer\"}}]}\n\n")
		write("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		write("data: [DONE]\n\n")
	}))
	defer server.Close()

	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	service.Hub = ws.NewHub()
	withFastTransientRetry(t)
	// Keep at least one retry available, and zero out the backoff so the test does
	// not actually sleep 5s. retryBackoffCeilMs=0 makes the computed delay 0.
	if _, err := service.Settings.Patch(map[string]any{"agent": map[string]any{
		"maxTransientRetries": float64(3),
		"retryBackoffCeilMs":  float64(0),
	}}); err != nil {
		t.Fatalf("patch agent settings: %v", err)
	}

	service.runBasicAgent(context.Background(), narratorID)

	if got := atomic.LoadInt32(&attempts); got < 2 {
		t.Fatalf("expected at least 2 provider attempts (retry after silent disconnect), got %d", got)
	}

	// After a successful retry the narrator must be idle and NOT carry the
	// silent_disconnect substatus (the disconnect was recovered, not terminal).
	var narratorStatus, substatus string
	if err := service.DB.QueryRow(`SELECT status, substatus FROM narrators WHERE id = ?`, narratorID).Scan(&narratorStatus, &substatus); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if narratorStatus != "idle" {
		t.Fatalf("narrator should be idle after recovered silent disconnect, got %q", narratorStatus)
	}
	if substatus == `["silent_disconnect"]` {
		t.Fatalf("recovered silent disconnect must not leave silent_disconnect substatus, got %q", substatus)
	}

	// The recovered final answer must be persisted as the latest assistant text.
	blocks := latestAssistantBlocks(t, service, narratorID)
	var hasRecovered bool
	for _, b := range blocks {
		if b["type"] == "text" && stringFromAny(b["text"]) == "recovered answer" {
			hasRecovered = true
		}
	}
	if !hasRecovered {
		t.Fatalf("expected recovered answer in latest assistant blocks, got %#v", blocks)
	}
}

// TestRunBasicAgentSilentDisconnectExhaustsRetries verifies that when the
// provider keeps silently disconnecting, the loop gives up after
// maxTransientRetries and finalizes terminally with the silent_disconnect
// substatus (mirrors TS handleTransientError returning shouldRetry=false once
// retryCount > maxRetries, narrator-recovery.ts:315).
func TestRunBasicAgentSilentDisconnectExhaustsRetries(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		atomic.AddInt32(&attempts, 1)
		// Always disconnect without [DONE].
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer server.Close()

	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	service.Hub = ws.NewHub()
	withFastTransientRetry(t)
	// 2 retries, zero backoff: attempt 1 + 2 retries = 3 provider calls, then give up.
	if _, err := service.Settings.Patch(map[string]any{"agent": map[string]any{
		"maxTransientRetries": float64(2),
		"retryBackoffCeilMs":  float64(0),
	}}); err != nil {
		t.Fatalf("patch agent settings: %v", err)
	}

	service.runBasicAgent(context.Background(), narratorID)

	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("expected exactly 3 attempts (1 + 2 retries) before giving up, got %d", got)
	}

	var narratorStatus, substatus string
	if err := service.DB.QueryRow(`SELECT status, substatus FROM narrators WHERE id = ?`, narratorID).Scan(&narratorStatus, &substatus); err != nil {
		t.Fatalf("load narrator status: %v", err)
	}
	if narratorStatus != "idle" {
		t.Fatalf("narrator should be idle after exhausting retries, got %q", narratorStatus)
	}
	if substatus != `["silent_disconnect"]` {
		t.Fatalf("expected silent_disconnect substatus after exhausting retries, got %q", substatus)
	}
}
