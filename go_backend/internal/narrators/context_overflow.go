package narrators

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
)

const contextOverflowKeepRecentMessages = 4

// maxContextOverflowRetries mirrors the TS MAX_CONTEXT_OVERFLOW_RETRIES constant
// (narrator-recovery.ts:26): after this many automatic compact+retry attempts the
// recovery gives up and the run is finalized as an error instead of looping
// forever. TS tracks the per-run counter on the session loop's local
// `contextOverflowRetries`; Go's finalizer-driven retry has no equivalent local,
// so the count is kept in a package-level map keyed by narrator id below.
const maxContextOverflowRetries = 2

// contextOverflowRetries tracks how many context-overflow compact+retry attempts
// each narrator has consumed in the current run. TS increments a loop-local
// counter per overflow (narrator-recovery.ts:131) and resets it to 0 whenever a
// turn completes without overflow; Go mirrors that with this guarded map because
// the overflow finalizer (finishAgentRunContextOverflow) and the retry it
// schedules run across separate goroutines/stack frames and need a shared,
// narrator-scoped counter. The counter is reset by resetContextOverflowRetries on
// every successful (non-overflow) run completion.
var (
	contextOverflowRetryMu sync.Mutex
	contextOverflowRetries = map[string]int{}
)

// incrementContextOverflowRetries records one more overflow attempt for the
// narrator and returns the new count, mirroring the TS `overflowRetries++` at the
// top of handleContextOverflow (narrator-recovery.ts:131).
func incrementContextOverflowRetries(narratorID string) int {
	contextOverflowRetryMu.Lock()
	defer contextOverflowRetryMu.Unlock()
	contextOverflowRetries[narratorID]++
	return contextOverflowRetries[narratorID]
}

// resetContextOverflowRetries clears the overflow attempt counter for a narrator.
// Mirrors the TS reset of the loop-local `contextOverflowRetries`/`transientRetries`
// to 0 on a clean (non-overflow) completion (narrator-session.ts), so a later
// independent overflow starts counting from zero again. Called from the run
// completion finalizer (finishAgentRunSuccess) in agent_event_handler.go.
func resetContextOverflowRetries(narratorID string) {
	contextOverflowRetryMu.Lock()
	delete(contextOverflowRetries, narratorID)
	contextOverflowRetryMu.Unlock()
}

// transientRetries tracks how many transient-error retries (currently driven by
// Codex/provider silent disconnects) each narrator has consumed in the current
// run. TS keeps a loop-local `transientRetries` counter on the session loop
// (narrator-session.ts:1024) that it increments per transient error
// (handleTransientError) and resets to 0 on a clean completion
// (narrator-session.ts:2044). Go mirrors that with this guarded, narrator-scoped
// map because the silent-disconnect handling and the retry the outer loop
// schedules run across separate stack frames.
var (
	transientRetryMu sync.Mutex
	transientRetries = map[string]int{}
)

// transientRetryBaseDelay is the base backoff for silent-disconnect transient
// retries, mirroring TS TRANSIENT_RETRY_BASE_MS (5000ms). The actual delay is
// min(base * 2^(attempt-1), retryBackoffCeilMs). It is a package var (not a
// const) so tests can shrink it to keep retry integration tests fast.
var transientRetryBaseDelay = 5 * time.Second

// incrementTransientRetries records one more transient retry attempt for the
// narrator and returns the new count, mirroring TS `transientRetries++`
// (narrator-session.ts:1979).
func incrementTransientRetries(narratorID string) int {
	transientRetryMu.Lock()
	defer transientRetryMu.Unlock()
	transientRetries[narratorID]++
	return transientRetries[narratorID]
}

// resetTransientRetries clears the transient retry counter for a narrator,
// mirroring the TS `transientRetries = 0` reset on a clean completion
// (narrator-session.ts:2044). Called from finishAgentRunSuccess.
func resetTransientRetries(narratorID string) {
	transientRetryMu.Lock()
	delete(transientRetries, narratorID)
	transientRetryMu.Unlock()
}

type contextOverflowRef struct {
	MessageID string
	Seq       int
	Role      string
	Text      string
	CreatedAt string
}

func isContextOverflowAgentEvent(event agentbasic.AgentEvent) bool {
	if event.Type != "invalid_state" && event.Type != "error" && event.Type != "retryable_error" {
		return false
	}
	needle := strings.ToLower(strings.TrimSpace(event.Reason + " " + event.Message + " " + event.Code))
	// Mirror the TS detection in server/lib/agent/error-handling.ts, which combines
	// CONTEXT_OVERFLOW_PATTERNS (message substrings) with isContextOverflowReason
	// (reason/code forms). Because needle merges reason+message+code and we match by
	// substring, both families collapse into one marker list here.
	markers := []string{
		// isContextOverflowReason forms (reason / code)
		"model_context_window_exceeded",
		"context_length", // covers context_length_exceeded + context_length
		"context_window", // covers context_window* reason/code
		"input_too_long",
		"prompt_too_long",
		"context_overflow",
		// CONTEXT_OVERFLOW_PATTERNS message substrings
		"exceeds the context window",
		"context window",
		"context length",
		"maximum context length",
		"maximum context",
		"input is too long",
		"prompt is too long",
		"too many tokens",
	}
	for _, marker := range markers {
		if strings.Contains(needle, marker) {
			return true
		}
	}
	return false
}

func (s *Service) finishAgentRunContextOverflow(ctx context.Context, state *agentRunState, event agentbasic.AgentEvent) error {
	// Enforce the TS MAX_CONTEXT_OVERFLOW_RETRIES cap (narrator-recovery.ts:131-135):
	// increment the per-narrator attempt counter and, once it exceeds the max, stop
	// the compact+retry loop and finalize the run as an error
	// (reason=max_retries_exceeded) instead of scheduling yet another retry. Without
	// this Go would compact+retry forever on a conversation that cannot be shrunk
	// below the model window.
	attempt := incrementContextOverflowRetries(state.narratorID)
	if attempt > maxContextOverflowRetries {
		resetContextOverflowRetries(state.narratorID)
		// Mirror the TS getContextOverflowFailureError("max_retries_exceeded")
		// user-visible message/errorCode (narrator-recovery.ts:85-89).
		state.errorCode = "context_too_long_recovery_exhausted"
		return s.finishAgentRunError(ctx, state, "Context is still too long after automatic recovery attempts")
	}
	state.completed = true
	state.errored = false
	state.skipGoalContinuation = true
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "context_length_exceeded", "narratorId": state.narratorID, "reason": event.Reason, "message": event.Message})
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "compacting", "narratorId": state.narratorID, "mode": "blocking"})
	}
	msg, summary, err := s.compactForContextOverflow(ctx, state.narratorID, contextOverflowKeepRecentMessages)
	if err != nil {
		if s.Hub != nil {
			s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "compact_failed", "narratorId": state.narratorID, "mode": "blocking", "error": err.Error()})
		}
		resetContextOverflowRetries(state.narratorID)
		return s.finishAgentRunError(ctx, state, "context length exceeded; automatic compact failed: "+err.Error())
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "message", "narratorId": state.narratorID, "message": msg})
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "compact_done", "narratorId": state.narratorID, "mode": "blocking", "messageId": msg.ID})
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'working', substatus = '["calling_model"]', error_message = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), state.narratorID); err != nil {
		return err
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "status", "narratorId": state.narratorID, "status": "working", "substatus": []string{"calling_model"}, "isBackground": state.currentNarrator().IsBackground})
	}
	_ = summary
	s.scheduleContextOverflowRetry(state.narratorID)
	return nil
}

func (s *Service) scheduleContextOverflowRetry(narratorID string) {
	s.scheduleNarratorRetry(narratorID)
}

func (s *Service) scheduleNarratorRetry(narratorID string) {
	if s.contextOverflowRetry != nil {
		s.contextOverflowRetry(context.Background(), narratorID)
		return
	}
	go s.runBasicAgent(context.Background(), narratorID)
}

func (s *Service) compactForContextOverflow(ctx context.Context, narratorID string, keepRecent int) (Message, string, error) {
	refs, err := s.contextOverflowRefs(ctx, narratorID)
	if err != nil {
		return Message{}, "", err
	}
	if len(refs) < 2 {
		return Message{}, "", fmt.Errorf("not enough messages to compact")
	}
	if keepRecent < 1 {
		keepRecent = contextOverflowKeepRecentMessages
	}
	boundaryIndex := len(refs) - keepRecent
	if boundaryIndex < 1 {
		boundaryIndex = len(refs) - 1
	}
	compactRefs := refs[:boundaryIndex]
	if len(compactRefs) == 0 {
		return Message{}, "", fmt.Errorf("no older messages available to compact")
	}
	insertSeq := refs[boundaryIndex].Seq
	summary := buildContextOverflowSummary(compactRefs)
	content := []map[string]any{{"type": "compact", "status": "compacted", "summary": summary, "subtype": "context_overflow"}}
	// Mirror the TS finalizeCompactingMessage contentText (narrator-persistence.ts):
	// `[Compact] ${summary.slice(0, 200)}...` (summary sliced to 200 UTF-16 code
	// units, always-on "..."). Go previously used `[Compact]\n` + full summary.
	text := "[Compact] " + httpx.JSStringSlicePrefix(summary, 200) + "..."
	msg, err := s.addMessageWithBlocksAtSeq(ctx, narratorID, "system", text, content, nil, &insertSeq, true)
	if err != nil {
		return Message{}, "", err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrators SET context_summary = ?, api_conversation_id = NULL, prune_boundary_message_id = NULL, pruned_percent = NULL, updated_at = ? WHERE id = ?`, summary, db.NowISO(), narratorID); err != nil {
		return Message{}, "", err
	}
	return msg, summary, nil
}

func (s *Service) contextOverflowRefs(ctx context.Context, narratorID string) ([]contextOverflowRef, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.message_id, r.seq, m.role, COALESCE(m.content_text, ''), COALESCE(m.created_at, '') FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND r.segment_compact_id IS NULL ORDER BY r.seq ASC`, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []contextOverflowRef{}
	for rows.Next() {
		var ref contextOverflowRef
		if err := rows.Scan(&ref.MessageID, &ref.Seq, &ref.Role, &ref.Text, &ref.CreatedAt); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func buildContextOverflowSummary(refs []contextOverflowRef) string {
	parts := []string{}
	for _, ref := range refs {
		text := strings.TrimSpace(ref.Text)
		if text == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(ref.Role)+": "+text)
		if len(parts) >= 12 {
			break
		}
	}
	prefix := "Context overflow fallback summary generated by Go backend because the provider reported the conversation exceeded the model context window."
	if len(parts) == 0 {
		return prefix + " No prior message text was available to summarize."
	}
	joined := strings.Join(parts, "\n")
	if len(joined) > 6000 {
		joined = joined[:6000] + "\n..."
	}
	return prefix + "\n\n" + joined
}
