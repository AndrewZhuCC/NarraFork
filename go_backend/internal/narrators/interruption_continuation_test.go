package narrators

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestInterruptionContinueMessageMatchesTS exercises the helper directly,
// mirroring TS tool.interruptionContinue (i18n.ts:231-234).
func TestInterruptionContinueMessageMatchesTS(t *testing.T) {
	if got := interruptionContinueMessage("en"); got != "Your previous response was cut off by the completion token limit. Please continue from where you left off." {
		t.Fatalf("en interruption continue = %q", got)
	}
	if got := interruptionContinueMessage("zh-CN"); got != "你上一条回复因 completion token 限制被截断，请从中断处继续。" {
		t.Fatalf("zh interruption continue = %q", got)
	}
	if got := interruptionContinueMessage("fr"); got != interruptionContinueMessage("en") {
		t.Fatalf("unknown locale should fall back to en")
	}
}

// sseChunk formats one OpenAI-style SSE data line.
func sseTruncatedResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"partial answer that got cut"}}]}`+"\n\n")
	fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`+"\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func sseFinalResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"final complete answer"}}]}`+"\n\n")
	fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func countInterruptionContinueMessages(t *testing.T, service *Service, narratorID, locale string) int {
	t.Helper()
	want := interruptionContinueMessage(locale)
	rows, err := service.DB.Query(`SELECT COALESCE(content_text, '') FROM narrator_messages WHERE narrator_id = ? AND role = 'user'`, narratorID)
	if err != nil {
		t.Fatalf("query user messages: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if strings.Contains(content, want) {
			count++
		}
	}
	return count
}

// TestRunBasicAgentAutoContinuesAfterOutputTruncated verifies the session loop
// injects an interruptionContinue user message and re-runs after a
// completion-token-limit truncation, then completes once the model finishes.
func TestRunBasicAgentAutoContinuesAfterOutputTruncated(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		n := requestCount
		mu.Unlock()
		if n == 1 {
			sseTruncatedResponse(w)
			return
		}
		sseFinalResponse(w)
	}))
	defer server.Close()

	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	// zh owner so the injected message is localized.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := service.DB.Exec(`CREATE TABLE user_preferences (user_id TEXT PRIMARY KEY, language TEXT)`); err != nil {
		t.Fatalf("create user_preferences: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES ('owner-msg', ?, 'user', NULL, 'user-zh', ?)`, narratorID, now); err != nil {
		t.Fatalf("insert owner message: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO user_preferences (user_id, language) VALUES ('user-zh', 'zh-CN')`); err != nil {
		t.Fatalf("insert pref: %v", err)
	}

	service.runBasicAgent(context.Background(), narratorID)

	if got := countInterruptionContinueMessages(t, service, narratorID, "zh-CN"); got != 1 {
		t.Fatalf("expected exactly 1 interruptionContinue message, got %d", got)
	}
	mu.Lock()
	total := requestCount
	mu.Unlock()
	if total < 2 {
		t.Fatalf("expected at least 2 model requests (truncated + continuation), got %d", total)
	}
	var status string
	if err := service.DB.QueryRow(`SELECT status FROM narrators WHERE id = ?`, narratorID).Scan(&status); err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	if status != "idle" {
		t.Fatalf("expected narrator idle after continuation, got %s", status)
	}
}

// TestRunBasicAgentInterruptionContinuationBoundedByMaxRetries verifies the
// auto-continuation stops after maxInterruptionRetries when the model keeps
// hitting the completion-token limit.
func TestRunBasicAgentInterruptionContinuationBoundedByMaxRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseTruncatedResponse(w)
	}))
	defer server.Close()

	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	service.runBasicAgent(context.Background(), narratorID)

	if got := countInterruptionContinueMessages(t, service, narratorID, "en"); got != maxInterruptionRetries {
		t.Fatalf("expected exactly %d interruptionContinue messages (bounded), got %d", maxInterruptionRetries, got)
	}
}

// TestRunBasicAgentNoInterruptionContinuationWhenNotTruncated verifies a normal
// completion does not trigger interruption continuation.
func TestRunBasicAgentNoInterruptionContinuationWhenNotTruncated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseFinalResponse(w)
	}))
	defer server.Close()

	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	service.runBasicAgent(context.Background(), narratorID)

	if got := countInterruptionContinueMessages(t, service, narratorID, "en"); got != 0 {
		t.Fatalf("expected no interruptionContinue messages for a normal completion, got %d", got)
	}
}
