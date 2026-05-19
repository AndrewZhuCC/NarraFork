package agentbasic

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	CodexRebuildHistoryRetryCode = "codex_quota_failover_rebuild"
	codexSessionAffinityTTL      = 6 * time.Hour
	codexMaxSessionAffinity      = 2000
)

type codexRuntimeErrorClassification struct {
	Failover       bool
	DisabledReason string
	QuotaResetsAt  float64
}

type codexRebuildHistoryRetryError struct {
	PreviousCredentialID string
	Operation            string
	Cause                error
}

func (e *codexRebuildHistoryRetryError) Error() string {
	if e == nil || e.Cause == nil {
		return "Codex credential quota exhausted; retrying requires rebuilt history"
	}
	return e.Cause.Error()
}

func (e *codexRebuildHistoryRetryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func isCodexRebuildHistoryRetryError(err error) bool {
	var target *codexRebuildHistoryRetryError
	return errors.As(err, &target)
}

func codexRebuildHistoryRetryMetadata(err error) map[string]any {
	var target *codexRebuildHistoryRetryError
	if !errors.As(err, &target) {
		return nil
	}
	metadata := map[string]any{"code": CodexRebuildHistoryRetryCode}
	if target.PreviousCredentialID != "" {
		metadata["previousCredentialId"] = target.PreviousCredentialID
	}
	if target.Operation != "" {
		metadata["operation"] = target.Operation
	}
	return metadata
}

type codexSessionAffinityEntry struct {
	CredentialID string
	LastUsedAt   time.Time
}

var codexSessionAffinity = struct {
	sync.Mutex
	entries map[string]codexSessionAffinityEntry
}{entries: map[string]codexSessionAffinityEntry{}}

func CodexStickySessionCount() int {
	codexSessionAffinity.Lock()
	defer codexSessionAffinity.Unlock()
	pruneCodexSessionAffinityLocked(time.Now())
	return len(codexSessionAffinity.entries)
}

func codexStickyEnabled(provider Provider, sessionKey string) bool {
	if strings.TrimSpace(sessionKey) == "" {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(provider.LoadBalancingMode))
	if mode == "" {
		mode = "tier-balanced"
	}
	switch mode {
	case "balanced", "tier-balanced", "tiered_sticky":
		return true
	default:
		return false
	}
}

func codexCredentialPoolForSession(provider Provider, sessionKey string) []Provider {
	pool := append([]Provider(nil), provider.CredentialPool...)
	if len(pool) == 0 || !codexStickyEnabled(provider, sessionKey) {
		return pool
	}
	codexSessionAffinity.Lock()
	defer codexSessionAffinity.Unlock()
	pruneCodexSessionAffinityLocked(time.Now())
	binding, ok := codexSessionAffinity.entries[sessionKey]
	if !ok || binding.CredentialID == "" {
		return pool
	}
	for i, candidate := range pool {
		if candidate.CredentialID != binding.CredentialID {
			continue
		}
		if i == 0 {
			codexSessionAffinity.entries[sessionKey] = codexSessionAffinityEntry{CredentialID: binding.CredentialID, LastUsedAt: time.Now()}
			return pool
		}
		preferred := pool[i]
		copy(pool[1:i+1], pool[0:i])
		pool[0] = preferred
		codexSessionAffinity.entries[sessionKey] = codexSessionAffinityEntry{CredentialID: binding.CredentialID, LastUsedAt: time.Now()}
		return pool
	}
	delete(codexSessionAffinity.entries, sessionKey)
	return pool
}

func bindCodexSession(sessionKey string, provider Provider, credentialID string) {
	if !codexStickyEnabled(provider, sessionKey) || strings.TrimSpace(credentialID) == "" {
		return
	}
	codexSessionAffinity.Lock()
	defer codexSessionAffinity.Unlock()
	codexSessionAffinity.entries[sessionKey] = codexSessionAffinityEntry{CredentialID: credentialID, LastUsedAt: time.Now()}
	pruneCodexSessionAffinityLocked(time.Now())
}

func unbindCodexSession(sessionKey string) {
	if strings.TrimSpace(sessionKey) == "" {
		return
	}
	codexSessionAffinity.Lock()
	defer codexSessionAffinity.Unlock()
	delete(codexSessionAffinity.entries, sessionKey)
}

func unbindCodexSessionsByCredential(credentialID string) {
	if strings.TrimSpace(credentialID) == "" {
		return
	}
	codexSessionAffinity.Lock()
	defer codexSessionAffinity.Unlock()
	for sessionKey, binding := range codexSessionAffinity.entries {
		if binding.CredentialID == credentialID {
			delete(codexSessionAffinity.entries, sessionKey)
		}
	}
}

func pruneCodexSessionAffinityLocked(now time.Time) {
	for sessionKey, binding := range codexSessionAffinity.entries {
		if now.Sub(binding.LastUsedAt) > codexSessionAffinityTTL {
			delete(codexSessionAffinity.entries, sessionKey)
		}
	}
	if len(codexSessionAffinity.entries) <= codexMaxSessionAffinity {
		return
	}
	type item struct {
		key  string
		last time.Time
	}
	items := make([]item, 0, len(codexSessionAffinity.entries))
	for key, binding := range codexSessionAffinity.entries {
		items = append(items, item{key: key, last: binding.LastUsedAt})
	}
	for len(codexSessionAffinity.entries) > codexMaxSessionAffinity {
		oldestIndex := 0
		for i := range items {
			if items[i].last.Before(items[oldestIndex].last) {
				oldestIndex = i
			}
		}
		delete(codexSessionAffinity.entries, items[oldestIndex].key)
		items = append(items[:oldestIndex], items[oldestIndex+1:]...)
	}
}

func classifyCodexRuntimeError(err error) codexRuntimeErrorClassification {
	if err == nil {
		return codexRuntimeErrorClassification{}
	}
	classification := codexRuntimeErrorClassification{}
	message := strings.ToLower(err.Error())
	var httpErr *providerHTTPError
	if errors.As(err, &httpErr) {
		message += "\n" + strings.ToLower(httpErr.Body)
		switch {
		case httpErr.StatusCode == http.StatusUnauthorized || httpErr.StatusCode == http.StatusForbidden:
			classification.Failover = true
			classification.DisabledReason = "banned"
		case httpErr.StatusCode == http.StatusPaymentRequired || httpErr.StatusCode == http.StatusTooManyRequests:
			classification.Failover = true
			classification.DisabledReason = "quota_exhausted"
		case httpErr.StatusCode >= 500:
			classification.Failover = true
		}
	}
	if isCodexQuotaMessage(message) {
		classification.Failover = true
		classification.DisabledReason = "quota_exhausted"
	}
	if strings.Contains(message, "unauthorized") || strings.Contains(message, "forbidden") || strings.Contains(message, "401") || strings.Contains(message, "403") {
		classification.Failover = true
		if classification.DisabledReason == "" {
			classification.DisabledReason = "banned"
		}
	}
	if strings.Contains(message, "500") || strings.Contains(message, "502") || strings.Contains(message, "503") || strings.Contains(message, "504") || strings.Contains(message, "eof") || strings.Contains(message, "connection reset") {
		classification.Failover = true
	}
	classification.QuotaResetsAt = codexQuotaResetsAtFromError(err)
	return classification
}

func isCodexQuotaMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "usage_limit_reached") ||
		(strings.Contains(message, "usage limit") && strings.Contains(message, "reached")) ||
		strings.Contains(message, "insufficient_balance") ||
		strings.Contains(message, "insufficient balance") ||
		(strings.Contains(message, "402") && strings.Contains(message, "insufficient")) ||
		strings.Contains(message, "quota") ||
		strings.Contains(message, "exhausted") ||
		strings.Contains(message, `"plan_type":"free"`)
}

func codexQuotaResetsAtFromError(err error) float64 {
	if err == nil {
		return 0
	}
	body := err.Error()
	var httpErr *providerHTTPError
	if errors.As(err, &httpErr) && strings.TrimSpace(httpErr.Body) != "" {
		body += "\n" + httpErr.Body
	}
	if resetAt := numericJSONPath(body, "resets_at"); resetAt > 0 {
		return resetAt * 1000
	}
	if resetAt := numericJSONPath(body, "reset_at"); resetAt > 0 {
		return resetAt * 1000
	}
	return 0
}

func numericJSONPath(raw string, key string) float64 {
	pattern := regexp.MustCompile(fmt.Sprintf(`"%s"\s*:\s*(\d+(?:\.\d+)?)`, regexp.QuoteMeta(key)))
	match := pattern.FindStringSubmatch(raw)
	if len(match) < 2 {
		return 0
	}
	var value float64
	if _, err := fmt.Sscanf(match[1], "%f", &value); err != nil {
		return 0
	}
	return value
}

func codexResponsesAPIError(parsed responsesStreamEvent) *apiErrorPayload {
	if parsed.Error != nil && (parsed.Error.Message != "" || parsed.Error.Code != nil || parsed.Error.Type != "") {
		return parsed.Error
	}
	if parsed.Response.Error != nil && (parsed.Response.Error.Message != "" || parsed.Response.Error.Code != nil || parsed.Response.Error.Type != "") {
		return parsed.Response.Error
	}
	return nil
}

func codexFailoverErrorFromResponsesEvent(parsed responsesStreamEvent) error {
	payload := codexResponsesAPIError(parsed)
	if payload == nil {
		return nil
	}
	encoded, _ := json.Marshal(payload)
	message := strings.TrimSpace(payload.Message)
	if len(encoded) > 0 {
		if message == "" {
			message = string(encoded)
		} else {
			message += ": " + string(encoded)
		}
	}
	if message == "" {
		message = "Codex Responses API error"
	}
	err := errors.New(message)
	classification := classifyCodexRuntimeError(err)
	if classification.Failover {
		return err
	}
	return nil
}
