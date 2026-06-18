package agentbasic

import (
	"encoding/json"
	"strconv"
	"strings"
)

// paymentRequiredErrorInfo mirrors the TS PaymentRequiredErrorInfo shape
// (server/lib/agent/error-handling.ts) used to drive the NUG recharge flow.
type paymentRequiredErrorInfo struct {
	Message  string
	Balance  *float64
	Required *float64
}

// getPaymentRequiredErrorInfo detects an "insufficient balance / payment
// required" provider error and extracts the embedded balance/required figures.
// It mirrors getPaymentRequiredErrorInfo in server/lib/agent/error-handling.ts:
// the error qualifies when the HTTP status set contains 402 or the message text
// contains "insufficient_quota" / "insufficient quota" / "payment required".
// Returns nil when the error does not look like a payment-required failure.
func getPaymentRequiredErrorInfo(err error) *paymentRequiredErrorInfo {
	if err == nil {
		return nil
	}
	// Match against the full message (err text + HTTP body + provider) so a
	// 402 surfaced only in the response body still qualifies, matching the TS
	// extractErrorMessage + collectStatusCodes behaviour.
	lower := retryRuleMatchText(err)
	statusCodes := retryRuleStatusCodes(err)
	looksLikePaymentRequired := statusCodes[402] ||
		strings.Contains(lower, "insufficient_quota") ||
		strings.Contains(lower, "insufficient quota") ||
		strings.Contains(lower, "payment required")
	if !looksLikePaymentRequired {
		return nil
	}

	// extractErrorMessage(err) equivalent: providerHTTPError.Error() already
	// embeds the response body, so err.Error() carries any JSON payload.
	raw := err.Error()

	info := &paymentRequiredErrorInfo{Message: raw}
	if obj := parseEmbeddedErrorObject(raw); obj != nil {
		info.Balance = numberField(obj, "balance")
		info.Required = numberField(obj, "required")
	}
	return info
}

// parseEmbeddedErrorObject extracts the `error` object from the first embedded
// JSON object found in message, mirroring TS parseEmbeddedJSON + parsed.error.
func parseEmbeddedErrorObject(message string) map[string]any {
	start := strings.Index(message, "{")
	end := strings.LastIndex(message, "}")
	if start < 0 || end <= start {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(message[start:end+1]), &parsed); err != nil {
		return nil
	}
	errVal, ok := parsed["error"]
	if !ok {
		return nil
	}
	errObj, ok := errVal.(map[string]any)
	if !ok {
		return nil
	}
	return errObj
}

// numberField returns the numeric value of key in obj, accepting JSON numbers
// or numeric strings, mirroring the TS numberField helper.
func numberField(obj map[string]any, key string) *float64 {
	if obj == nil {
		return nil
	}
	switch v := obj[key].(type) {
	case float64:
		return &v
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return &f
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return &f
		}
	}
	return nil
}
