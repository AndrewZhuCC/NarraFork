package agentbasic

import (
	"testing"
	"time"
)

// TestRetryDelayBackoffMatchesTS pins retryDelay to the TS transient-retry
// backoff Math.min(TRANSIENT_RETRY_BASE_MS * 2^(attempt-1), backoffCeil)
// (server/lib/agent/loop.ts + types.ts). With the TS base (5000ms) and ceiling
// (20000ms): attempt 1->5s, 2->10s, 3->20s (capped), 4+->20s (capped). attempt
// <=0 yields 0 (no delay before the first try).
func TestRetryDelayBackoffMatchesTS(t *testing.T) {
	base := defaultRetryBaseDelay   // 5000ms (TS TRANSIENT_RETRY_BASE_MS)
	ceil := defaultRetryBackoffCeil // 20000ms (TS default backoffCeil)
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 0},
		{-1, 0},
		{1, 5000 * time.Millisecond},
		{2, 10000 * time.Millisecond},
		{3, 20000 * time.Millisecond},  // 40000 capped to 20000
		{4, 20000 * time.Millisecond},  // capped
		{10, 20000 * time.Millisecond}, // capped
	}
	for _, tc := range cases {
		if got := retryDelay(base, tc.attempt, ceil); got != tc.want {
			t.Fatalf("retryDelay(5s, %d, 20s) = %v, want %v", tc.attempt, got, tc.want)
		}
	}

	// Zero base -> no delay regardless of attempt (tests opt into this).
	if got := retryDelay(0, 5, ceil); got != 0 {
		t.Fatalf("retryDelay(0,5,ceil) = %v, want 0", got)
	}

	// A custom (smaller) ceiling clamps earlier.
	if got := retryDelay(base, 3, 8000*time.Millisecond); got != 8000*time.Millisecond {
		t.Fatalf("retryDelay with 8s ceil at attempt 3 = %v, want 8s", got)
	}

	// Constants must match the TS values.
	if defaultRetryBaseDelay != 5000*time.Millisecond {
		t.Fatalf("defaultRetryBaseDelay = %v, want 5s (TS TRANSIENT_RETRY_BASE_MS)", defaultRetryBaseDelay)
	}
	if defaultRetryBackoffCeil != 20000*time.Millisecond {
		t.Fatalf("defaultRetryBackoffCeil = %v, want 20s (TS default)", defaultRetryBackoffCeil)
	}
}

// TestNormalizeGenerateOptionsRetryDefaults verifies the production defaults wire
// the TS-matching base/ceiling, and that a non-positive ceiling restores 20s.
func TestNormalizeGenerateOptionsRetryDefaults(t *testing.T) {
	opts := normalizeGenerateOptions()
	if opts.retryBaseDelay != 5000*time.Millisecond {
		t.Fatalf("default retryBaseDelay = %v, want 5s", opts.retryBaseDelay)
	}
	if opts.retryBackoffCeil != 20000*time.Millisecond {
		t.Fatalf("default retryBackoffCeil = %v, want 20s", opts.retryBackoffCeil)
	}
	// Explicit zero/negative ceiling normalizes back to the default.
	opts = normalizeGenerateOptions(WithRetryBackoffCeil(0))
	if opts.retryBackoffCeil != 20000*time.Millisecond {
		t.Fatalf("zero ceiling should restore default, got %v", opts.retryBackoffCeil)
	}
	// A custom ceiling is honored.
	opts = normalizeGenerateOptions(WithRetryBackoffCeil(30000 * time.Millisecond))
	if opts.retryBackoffCeil != 30000*time.Millisecond {
		t.Fatalf("custom ceiling not honored, got %v", opts.retryBackoffCeil)
	}
}
