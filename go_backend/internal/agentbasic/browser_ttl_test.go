package agentbasic

import (
	"testing"
	"time"
)

// TestNormalizeBrowserSessionTTLMatchesTS pins normalizeBrowserSessionTTL to the
// TS normalizeSessionTtlMs (server/lib/browser/session.ts): the DEFAULT is
// applied ONLY for an absent value (the tool callers pass browserDefaultSessionTTL
// when ttl_ms is missing), while any EXPLICIT value outside [MIN=1000ms,
// MAX=86400000ms] - including 0 and negatives - is REJECTED with an error rather
// than silently defaulted. Previously Go returned the 10-minute default for any
// ttl <= 0, diverging from TS which throws for an explicit 0.
func TestNormalizeBrowserSessionTTLMatchesTS(t *testing.T) {
	// In-range values pass through unchanged.
	for _, ms := range []int64{1000, 600000, 86400000, 5000} {
		got, err := normalizeBrowserSessionTTL(time.Duration(ms) * time.Millisecond)
		if err != nil {
			t.Fatalf("ttl %dms should be valid, got err %v", ms, err)
		}
		if got != time.Duration(ms)*time.Millisecond {
			t.Fatalf("ttl %dms = %v, want unchanged", ms, got)
		}
	}

	// Out-of-range explicit values are rejected (NOT defaulted), including 0/neg.
	for _, ms := range []int64{0, -1, -5000, 999, 86400001, 999999999} {
		if _, err := normalizeBrowserSessionTTL(time.Duration(ms) * time.Millisecond); err == nil {
			t.Fatalf("ttl %dms should be rejected, got no error", ms)
		}
	}

	// The default constant is itself in range (so the absent-key path the callers
	// take, passing browserDefaultSessionTTL, validates cleanly).
	if _, err := normalizeBrowserSessionTTL(browserDefaultSessionTTL); err != nil {
		t.Fatalf("default TTL should be valid, got %v", err)
	}
	if browserDefaultSessionTTL != 10*time.Minute || browserMinSessionTTL != time.Second || browserMaxSessionTTL != 24*time.Hour {
		t.Fatalf("TTL constants drifted from TS: default=%v min=%v max=%v", browserDefaultSessionTTL, browserMinSessionTTL, browserMaxSessionTTL)
	}
}
