package agentbasic

import (
	"context"
	"errors"
	"testing"
)

// TestIsTransientAgentError verifies the retry decision mirrors the TS
// isRetryableError logic (server/lib/agent/error-handling.ts): transient
// network/server errors and load-keyword 429s retry, while hard quota/billing
// failures and bare 429s (without a custom rule) do not.
func TestIsTransientAgentError(t *testing.T) {
	retryable := []struct {
		name string
		err  error
	}{
		{"anthropic 529 overloaded_error", errors.New(`anthropic provider returned status 529: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)},
		{"message-only overloaded no status", errors.New("the model is currently overloaded, please slow down")},
		{"status 529", &providerHTTPError{StatusCode: 529, Provider: "anthropic", Body: "overloaded"}},
		{"status 503 regression", errors.New("provider returned status 503")},
		{"status 500", &providerHTTPError{StatusCode: 500, Provider: "openai", Body: "internal error"}},
		{"too many requests", errors.New("Too Many Requests")},
		{"throttled", errors.New("request was throttled, slow down")},
		{"service unavailable", errors.New("503 Service Unavailable")},
		{"socket hang up", errors.New("socket hang up")},
		{"econnreset code", errors.New("read tcp: ECONNRESET")},
		{"etimedout code", errors.New("dial tcp: ETIMEDOUT")},
		{"fetch failed", errors.New("fetch failed")},
		{"network error", errors.New("network error while streaming")},
		{"stream read error", errors.New("stream read error: unexpected close")},
		{"internal server error", errors.New("the server had an error processing your request")},
		{"model temporarily unavailable", errors.New("MODEL_TEMPORARILY_UNAVAILABLE")},
		{"429 with rate limit keyword", errors.New("status 429: rate limit exceeded, please retry")},
		{"429 with overloaded keyword", &providerHTTPError{StatusCode: 429, Provider: "nug", Body: "overloaded, too many requests"}},
		// invalid-state reasons in RETRYABLE_INVALID_STATE_REASONS retry via the
		// dedicated reason-set branch (mirrors TS isRetryableInvalidStateReason).
		{"invalid_state server_error", &providerInvalidStateError{Reason: "server_error"}},
		{"invalid_state internal_error", &providerInvalidStateError{Reason: "internal_error"}},
		{"invalid_state internal_server_error", &providerInvalidStateError{Reason: "internal_server_error"}},
		{"invalid_state service_unavailable", &providerInvalidStateError{Reason: "service_unavailable"}},
		{"invalid_state temporarily_unavailable", &providerInvalidStateError{Reason: "temporarily_unavailable"}},
		{"invalid_state stream_closed_before_response_completed", &providerInvalidStateError{Reason: "stream_closed_before_response_completed"}},
		{"invalid_state stream_read_error", &providerInvalidStateError{Reason: "stream_read_error"}},
		{"invalid_state reason uppercase normalized", &providerInvalidStateError{Reason: "SERVICE_UNAVAILABLE"}},
	}
	for _, tc := range retryable {
		if !isTransientAgentError(tc.err, nil) {
			t.Errorf("expected %q to be retryable", tc.name)
		}
	}

	nonRetryable := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"context canceled", context.Canceled},
		{"context deadline", context.DeadlineExceeded},
		{"bare 429 no keyword", &providerHTTPError{StatusCode: 429, Provider: "openai", Body: "request rejected"}},
		{"insufficient_quota 429", errors.New("status 429: insufficient_quota for this account")},
		{"payment required", errors.New("402 payment required")},
		{"usage_limit_reached", errors.New("usage_limit_reached for your plan")},
		{"generic 400", errors.New("provider returned status 400: bad request")},
		{"plan_type free", errors.New(`{"plan_type":"free"}`)},
		// invalid-state reasons NOT in the retryable set must not retry.
		{"invalid_state content_filter", &providerInvalidStateError{Reason: "content_filter"}},
		{"invalid_state refusal", &providerInvalidStateError{Reason: "refusal"}},
		{"invalid_state max_tokens", &providerInvalidStateError{Reason: "max_tokens"}},
	}
	for _, tc := range nonRetryable {
		if isTransientAgentError(tc.err, nil) {
			t.Errorf("expected %q to NOT be retryable", tc.name)
		}
	}
}

// TestInvalidStateReasonRetryRespectsNonRetryableMessage verifies that a hard
// quota/billing message wins over a transient invalid-state reason, matching the
// TS order where isRetryableInvalidStateReason checks NON_RETRYABLE_PATTERNS
// before the retryable reason set.
func TestInvalidStateReasonRetryRespectsNonRetryableMessage(t *testing.T) {
	err := &providerInvalidStateError{Reason: "service_unavailable", Message: "insufficient_quota for this account"}
	if isTransientAgentError(err, nil) {
		t.Fatal("a non-retryable quota message must override a transient invalid-state reason")
	}
}

// TestIsTransientAgentErrorBare429FallsBackToCustomRules verifies that a bare
// 429 without a built-in load keyword still retries when a user custom retry
// rule matches, mirroring TS isRetryableError's custom-rule fallthrough.
func TestIsTransientAgentErrorBare429FallsBackToCustomRules(t *testing.T) {
	err := &providerHTTPError{StatusCode: 429, Provider: "custom", Body: "shard warming up"}
	if isTransientAgentError(err, nil) {
		t.Fatal("bare 429 without keyword or rule must not retry")
	}
	rules := []customRetryRule{{Keyword: "shard warming"}}
	if !isTransientAgentError(err, rules) {
		t.Fatal("bare 429 should retry when a custom keyword rule matches")
	}
}
