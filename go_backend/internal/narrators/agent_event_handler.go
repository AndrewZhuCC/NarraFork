package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/db"
	"narrafork-go/internal/id"
)

type apiRequestRunState struct {
	Provider     string
	Model        string
	CredentialID string
	StartedAt    time.Time
}

type agentRunState struct {
	narratorID string
	// mu guards the fields that are accessed concurrently by the agent loop
	// goroutine (the tool executor + runtime hooks run inside agentbasic.RunAgentLoop)
	// and the caller goroutine (handleAgentEvent draining the event channel).
	// agentbasic.RunAgentLoop runs the executor on its own goroutine while the
	// caller consumes events from a buffered channel, so these two goroutines run
	// concurrently and both touch draftMessageID / narrator / planApproved*.
	// Fields that are ONLY touched by the caller goroutine (handleAgentEvent and
	// the run finalizers) do not need the lock and are left unguarded.
	mu                     sync.Mutex
	draftCreateMu          sync.Mutex // serializes lazy assistant-draft creation across goroutines
	narrator               Narrator   // guarded by mu
	draftMessageID         string     // guarded by mu
	planApprovedMode       string     // guarded by mu
	planApprovedPrompt     string     // guarded by mu
	planApprovedBy         string     // guarded by mu
	blocks                 []map[string]any
	assistantText          string
	lastMessage            *Message
	completed              bool
	errored                bool
	errorMessage           string
	errorCode              string
	hadToolUses            bool
	skipGoalContinuation   bool
	outputTruncated        bool
	goalTurnStartedAt      time.Time
	goalTokenUsageBaseline *agentbasic.UsageSnapshot
	lastTokenUsage         *agentbasic.UsageSnapshot
	lastContextPercent     *float64
	lastMeterUsage         *float64
	lastMeterUnit          string
	pendingApiRequestIDs   []string
	apiRequests            map[string]apiRequestRunState
	streamedBytes          int
	providerPrefix         string
	sideCarToolCount       int
	toolUseCharsMap        map[string]int
}

// draftID returns the current assistant draft message id under the lock.
func (state *agentRunState) draftID() string {
	if state == nil {
		return ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.draftMessageID
}

// setDraftID stores the assistant draft message id under the lock.
func (state *agentRunState) setDraftID(id string) {
	if state == nil {
		return
	}
	state.mu.Lock()
	state.draftMessageID = id
	state.mu.Unlock()
}

// currentNarrator returns a copy of the narrator snapshot under the lock so
// callers never observe a torn struct while a runtime hook is replacing it.
func (state *agentRunState) currentNarrator() Narrator {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.narrator
}

// setNarrator replaces the narrator snapshot under the lock.
func (state *agentRunState) setNarrator(narrator Narrator) {
	state.mu.Lock()
	state.narrator = narrator
	state.mu.Unlock()
}

// setNarratorModel updates only the model/reasoning fields under the lock,
// mirroring the runtime-settings override hook which mutates them in place.
func (state *agentRunState) setNarratorModel(model, reasoningEffort *string) {
	state.mu.Lock()
	state.narrator.Model = model
	state.narrator.ReasoningEffort = reasoningEffort
	state.mu.Unlock()
}

// setPlanApproval records an approved ExitPlanMode continuation under the lock.
func (state *agentRunState) setPlanApproval(mode, prompt, decidedBy string) {
	if state == nil {
		return
	}
	state.mu.Lock()
	state.planApprovedMode = mode
	state.planApprovedPrompt = prompt
	state.planApprovedBy = decidedBy
	state.mu.Unlock()
}

// planApproval reads the approved ExitPlanMode continuation under the lock.
func (state *agentRunState) planApproval() (mode, prompt, decidedBy string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.planApprovedMode, state.planApprovedPrompt, state.planApprovedBy
}

// planApprovedModeValue returns just the approved continuation mode under the
// lock (the stream loop polls this to detect plan approval).
func (state *agentRunState) planApprovedModeValue() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.planApprovedMode
}

func (state *agentRunState) resetAssistantDraft() {
	if state == nil {
		return
	}
	state.setDraftID("")
	state.blocks = nil
	state.assistantText = ""
	state.hadToolUses = false
}

func (s *Service) handleAgentEvent(ctx context.Context, state *agentRunState, event agentbasic.AgentEvent) error {
	if strings.TrimSpace(event.Provider) != "" {
		state.providerPrefix = event.Provider
	}
	// Snapshot the narrator once per event: the agent loop goroutine may replace
	// state.narrator between turns via the runtime-settings/before-turn hooks, so a
	// single consistent read avoids a torn struct mid-handler.
	narrator := state.currentNarrator()
	s.broadcastAgentEvent(state.narratorID, event)
	// Mirror the TS narrator-event-handler recordOutputChunk calls: record streamed
	// assistant text and reasoning lengths (UTF-16 code units) into the global
	// output-stats rate tracker so the output_stats WS feed reports real chars/sec.
	if s.Hub != nil && event.Text != "" && (event.Type == "stream_text" || event.Type == "stream_reasoning") {
		s.Hub.RecordOutputText(event.Text)
	}
	// Mirror the TS tool-input streaming contribution: tool_use_chunk carries a
	// cumulative InputCharsTotal per tool; record the positive delta from the last
	// seen value for that toolUseId (TS narrator-event-handler.ts:609-616). Go's
	// InputCharsTotal is the byte length of the accumulating tool-input JSON, which
	// equals the UTF-16 length for the overwhelmingly ASCII tool-input JSON.
	if s.Hub != nil && event.Type == "tool_use_chunk" && event.Streaming != nil && event.Streaming.InputCharsTotal > 0 {
		toolUseID := strings.TrimSpace(event.ToolUseID)
		if toolUseID == "" {
			toolUseID = strings.TrimSpace(event.Streaming.ToolUseID)
		}
		if toolUseID != "" {
			if state.toolUseCharsMap == nil {
				state.toolUseCharsMap = map[string]int{}
			}
			prev := state.toolUseCharsMap[toolUseID]
			if delta := event.Streaming.InputCharsTotal - prev; delta > 0 {
				s.Hub.RecordOutputChunk(delta)
				state.toolUseCharsMap[toolUseID] = event.Streaming.InputCharsTotal
			}
		}
	}
	if narrator.IsBackground && narrator.ParentNarratorID != nil && (event.Type == "stream_text" || event.Type == "stream_reasoning") && event.Text != "" {
		state.streamedBytes += len([]byte(event.Text))
		s.broadcastBackgroundTaskOutput(*narrator.ParentNarratorID, state.narratorID, state.streamedBytes)
	}
	switch event.Type {
	case "retrying":
		// The raw `retrying` frame was already broadcast above (and the hub maps it
		// to a `warning` frame for this narrator's own subscribers). When this
		// narrator is a subagent, additionally notify its parent so the parent's
		// SubagentCard can render the inline retry status, mirroring the TS
		// narrator-recovery subagent_warning dual broadcast.
		s.broadcastSubagentWarning(narrator, state.narratorID, event)
		return nil
	case "model_switched":
		// The raw `model_switched` frame was already broadcast above. Mirror the TS
		// narrator-event-handler dual broadcast (narrator-event-handler.ts:1712-1730)
		// by additionally emitting the model_settings_applied companion frame so the
		// frontend's model_settings_applied handler receives the applied runtime
		// model/provider/reasoningEffort.
		s.broadcastModelSettingsApplied(state.narratorID, event)
		return nil
	case "stream_text", "stream_reasoning", "tool_use_chunk", "web_search", "image_generation", "tool_inventory":
		return nil
	case "output_truncated":
		// Mark the run as completion-token-limited so the session loop
		// (runBasicAgent) can auto-continue with an interruptionContinue prompt,
		// mirroring the TS completion-limit continuation (narrator-session.ts).
		state.outputTruncated = true
		return nil
	case "turn_complete":
		state.resetAssistantDraft()
		return nil
	case "sidecars":
		return s.persistStandaloneSideCars(ctx, state, event.SideCars)
	case "api_request_start":
		rememberAPIRequestStart(state, event)
		return nil
	case "api_request_end":
		_ = s.persistAPIRequestEvent(ctx, state, event)
		return nil
	case "context_usage":
		state.lastTokenUsage = mergeUsageSnapshot(state.lastTokenUsage, event.Usage)
		if pct := contextPercentFromEvent(event); pct > 0 {
			state.lastContextPercent = &pct
		}
		return nil
	case "metering":
		if usage := floatFromMetadata(event.Metadata, "usage"); usage > 0 {
			state.lastMeterUsage = &usage
			state.lastMeterUnit = strings.TrimSpace(stringFromMetadata(event.Metadata, "unit"))
		}
		return nil
	case "quota_balance":
		s.persistCustomApiQuotaBalance(
			state.providerPrefix,
			event.Message,
			stringFromMetadata(event.Metadata, "detailedQuotaBalance"),
		)
		s.persistNugQuotaBalance(
			state.providerPrefix,
			event.Message,
			stringFromMetadata(event.Metadata, "detailedQuotaBalance"),
		)
		return nil
	case "nkp_quota_balance":
		s.persistNKPQuotaBalance(state.providerPrefix, event.Metadata)
		return nil
	case "block_complete":
		if event.Block == nil {
			return nil
		}
		draftID, err := s.ensureDraftID(ctx, state)
		if err != nil {
			return err
		}
		block := contentBlockToMap(event.Block)
		if blockID, _ := block["id"].(string); block["type"] == "tool_use" && blockID != "" {
			state.hadToolUses = true
			upsertToolUseBlock(state, block)
		} else {
			insertAssistantBlock(state, block)
		}
		if block["type"] == "text" {
			state.assistantText = strings.TrimSpace(stringFromAny(block["text"]))
		}
		return s.updateAssistantDraft(ctx, state.narratorID, draftID, state.assistantText, state.blocks)
	case "tool_call":
		state.hadToolUses = true
		draftID, err := s.ensureDraftID(ctx, state)
		if err != nil {
			return err
		}
		block := map[string]any{"type": "tool_use", "id": event.ToolUseID, "name": event.ToolName, "input": event.Input}
		if event.StreamStartedAt != 0 {
			block["streamStartedAt"] = event.StreamStartedAt
		}
		upsertToolUseBlock(state, block)
		if err := s.updateAssistantDraft(ctx, state.narratorID, draftID, state.assistantText, state.blocks); err != nil {
			return err
		}
		if toolRequiresApproval(narrator.PermissionMode, event.ToolName) {
			return nil
		}
		return s.recordRunningToolCall(ctx, state.narratorID, draftID, narrator, event)
	case "tool_result":
		if _, err := s.ensureDraftID(ctx, state); err != nil {
			return err
		}
		return s.finishToolCallFromEvent(ctx, state, event)
	case "assistant_message":
		draftID, err := s.ensureDraftID(ctx, state)
		if err != nil {
			return err
		}
		if strings.TrimSpace(event.Text) != "" {
			state.assistantText = event.Text
		}
		if err := s.updateAssistantDraft(ctx, state.narratorID, draftID, state.assistantText, state.blocks); err != nil {
			return err
		}
		_ = s.updateAssistantMessageUsage(ctx, state, draftID, event)
		_ = s.linkPendingAPIRequestsToMessage(ctx, state, draftID)
		msg, err := s.messageByID(ctx, state.narratorID, draftID)
		if err != nil {
			return err
		}
		if narrator.IsBackground && narrator.ParentNarratorID != nil {
			if err := s.finishBackgroundTaskRecord(ctx, *narrator.ParentNarratorID, state.narratorID, "completed", state.assistantText); err != nil {
				return err
			}
		}
		s.broadcastMessage(ctx, state.narratorID, msg)
		return nil
	case "done":
		return s.finishAgentRunSuccess(ctx, state)
	case "first_token_timeout":
		state.errorMessage = firstNonEmptyString(event.Message, "Provider did not produce a response before the first-token timeout")
		return s.finishAgentRunTimeout(ctx, state, state.errorMessage)
	case "silent_disconnect":
		return s.finishAgentRunSilentDisconnect(ctx, state)
	case "payment_required":
		return s.finishAgentRunPaymentRequired(ctx, state, event)
	case "error", "retryable_error", "invalid_state":
		if isContextOverflowAgentEvent(event) {
			return s.finishAgentRunContextOverflow(ctx, state, event)
		}
		state.errored = true
		state.errorCode = strings.TrimSpace(event.Code)
		state.errorMessage = firstNonEmptyString(event.Message, event.Reason, "agent run failed")
		if isCancelledError(errors.New(state.errorMessage)) {
			return s.finishAgentRunCancelled(ctx, state)
		}
		// state.errorMessage stays raw above so the cancellation check (and the
		// upstream overflow/retry classification) keep matching the original
		// provider body. Only the user-visible display string handed to
		// finishAgentRunError is cleaned, mirroring TS extractApiErrorMessage.
		return s.finishAgentRunError(ctx, state, cleanProviderErrorMessageForDisplay(state.errorMessage))
	default:
		return nil
	}
}

// cleanProviderErrorMessageForDisplay shortens a verbose provider error string
// for user-visible display, mirroring TS extractApiErrorMessage
// (server/lib/agent/openai-provider.ts:1346). Go providerHTTPError.Error()
// embeds the full response body, e.g.
// `openai provider returned status 400: {"error":{"message":"...","type":"..."}}`,
// which can carry up to 64KB of raw JSON. This finds the first embedded JSON
// object and, when it exposes error.message or a top-level message, replaces
// the JSON span with that clean message while preserving the surrounding
// prefix/suffix (the "<provider> provider returned status <N>: " prefix).
//
// This is a DISPLAY-ONLY transform applied to the argument handed to
// finishAgentRunError. The raw text is still used for retry/overflow/cancel
// classification upstream (agent loop + handleAgentEvent overflow check +
// isCancelledError), so no matching token is lost here.
func cleanProviderErrorMessageForDisplay(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return raw
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw[start:end+1]), &parsed); err != nil {
		return raw
	}
	clean := ""
	if errObj, ok := parsed["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok && strings.TrimSpace(msg) != "" {
			clean = msg
		}
	}
	if clean == "" {
		if msg, ok := parsed["message"].(string); ok && strings.TrimSpace(msg) != "" {
			clean = msg
		}
	}
	if clean == "" {
		// No extractable message (e.g. {"error":{"code":"x"}}); keep the raw
		// text so any diagnostic detail survives.
		return raw
	}
	result := raw[:start] + clean + raw[end+1:]
	if runes := []rune(result); len(runes) > 1000 {
		return string(runes[:1000]) + "..."
	}
	return result
}

func rememberAPIRequestStart(state *agentRunState, event agentbasic.AgentEvent) {
	if state == nil {
		return
	}
	requestID := strings.TrimSpace(event.RequestID)
	if requestID == "" {
		requestID = id.New()
	}
	if state.apiRequests == nil {
		state.apiRequests = map[string]apiRequestRunState{}
	}
	state.apiRequests[requestID] = apiRequestRunState{
		Provider:     firstNonEmptyString(strings.TrimSpace(event.Provider), state.providerPrefix),
		Model:        firstNonEmptyString(strings.TrimSpace(event.Model), ptrString(state.currentNarrator().Model)),
		CredentialID: strings.TrimSpace(event.CredentialID),
		StartedAt:    time.Now(),
	}
}

func (s *Service) persistAPIRequestEvent(ctx context.Context, state *agentRunState, event agentbasic.AgentEvent) error {
	if s == nil || s.DB == nil || state == nil {
		return nil
	}
	columns, err := db.TableColumnSet(ctx, s.DB, "api_requests")
	if err != nil || len(columns) == 0 {
		return err
	}
	requestID := strings.TrimSpace(event.RequestID)
	if requestID == "" {
		requestID = id.New()
	}
	request := state.apiRequests[requestID]
	if request.StartedAt.IsZero() {
		request.StartedAt = time.Now()
	}
	provider := firstNonEmptyString(strings.TrimSpace(event.Provider), request.Provider, state.providerPrefix)
	model := firstNonEmptyString(strings.TrimSpace(event.Model), request.Model, ptrString(state.currentNarrator().Model))
	credentialID := firstNonEmptyString(strings.TrimSpace(event.CredentialID), request.CredentialID)
	durationMs := event.DurationMs
	if durationMs == 0 && !request.StartedAt.IsZero() {
		durationMs = time.Since(request.StartedAt).Milliseconds()
	}
	usage := event.Usage
	contextPercent := contextPercentFromEvent(event)
	if contextPercent <= 0 {
		contextPercent = floatFromMetadata(event.Metadata, "contextPercent")
	}
	meterUsage := firstPositiveFloat(
		floatFromMetadata(event.Metadata, "meterUsage"),
		floatFromMetadata(event.Metadata, "meter_usage"),
	)
	meterUnit := firstNonEmptyString(
		stringFromMetadata(event.Metadata, "meterUnit"),
		stringFromMetadata(event.Metadata, "meter_unit"),
	)
	messageID := strings.TrimSpace(state.draftID())
	values := map[string]any{
		"id":                          requestID,
		"narrator_id":                 state.narratorID,
		"message_id":                  nullableTrimmedString(messageID),
		"kind":                        "narrator",
		"provider":                    nullableTrimmedString(provider),
		"credential_id":               nullableTrimmedString(credentialID),
		"model":                       nullableTrimmedString(model),
		"input_tokens":                usageInputTokens(usage),
		"output_tokens":               usageCompletionTokens(usage),
		"cached_input_tokens":         usageCachedInputTokens(usage),
		"cache_creation_input_tokens": usageCacheCreationInputTokens(usage),
		"cache_creation_5m_tokens":    usageCacheCreation5mTokens(usage),
		"cache_creation_1h_tokens":    usageCacheCreation1hTokens(usage),
		"reasoning_tokens":            usageReasoningTokens(usage),
		"ttft_ms":                     nullablePositiveInt64(int64FromMetadata(event.Metadata, "ttftMs")),
		"duration_ms":                 nullablePositiveInt64(durationMs),
		"cost_usd":                    nullableCostForUsage(usage, provider, model),
		"context_percent":             nullablePositiveFloat(contextPercent),
		"meter_usage":                 nullablePositiveFloat(meterUsage),
		"meter_unit":                  nullableTrimmedString(meterUnit),
		"error_message":               nullableTrimmedString(event.Message),
		"raw_dump_json":               s.apiRequestRawDumpJSON(event),
		"created_at":                  db.NowISO(),
	}
	insertColumns := []string{}
	placeholders := []string{}
	args := []any{}
	for _, column := range []string{"id", "narrator_id", "message_id", "kind", "provider", "credential_id", "model", "input_tokens", "output_tokens", "cached_input_tokens", "cache_creation_input_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "reasoning_tokens", "ttft_ms", "duration_ms", "cost_usd", "context_percent", "meter_usage", "meter_unit", "error_message", "raw_dump_json", "created_at"} {
		if !columns[column] {
			continue
		}
		insertColumns = append(insertColumns, column)
		placeholders = append(placeholders, "?")
		args = append(args, values[column])
	}
	if len(insertColumns) == 0 {
		return nil
	}
	updateColumns := []string{}
	for _, column := range insertColumns {
		if column != "id" {
			updateColumns = append(updateColumns, column)
		}
	}
	query := db.InsertOnConflictUpdateSQL("api_requests", insertColumns, []string{"id"}, updateColumns)
	if _, err := s.DB.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	if messageID == "" {
		state.pendingApiRequestIDs = append(state.pendingApiRequestIDs, requestID)
	}
	delete(state.apiRequests, requestID)
	return nil
}

func (s *Service) apiRequestRawDumpJSON(event agentbasic.AgentEvent) any {
	if s == nil || s.Settings == nil || !s.agentSettingBool("requestDumpEnabled", false) {
		return nil
	}
	rawDump, ok := event.Metadata["rawDump"]
	if !ok {
		rawDump, ok = event.Metadata["raw_dump"]
	}
	if !ok || rawDump == nil {
		return nil
	}
	if s.agentSettingBool("requestDumpErrorsOnly", false) && strings.TrimSpace(event.Message) == "" {
		return nil
	}
	data, err := json.Marshal(rawDump)
	if err != nil {
		data, _ = json.Marshal(map[string]any{"invalidJson": true, "rawText": fmt.Sprint(rawDump)})
	}
	maxSize := s.agentSettingInt("requestDumpMaxSize", 1024*1024)
	if maxSize >= 0 && len(data) > maxSize {
		preview := string(data[:maxSize])
		data, _ = json.Marshal(map[string]any{"truncated": true, "originalBytes": len(data), "preview": preview})
	}
	return string(data)
}

func (s *Service) agentSettingBool(key string, fallback bool) bool {
	if s == nil || s.Settings == nil {
		return fallback
	}
	raw := s.Settings.Raw()
	agent, _ := raw["agent"].(map[string]any)
	value, ok := agent[key]
	if !ok {
		return fallback
	}
	if boolValue, ok := value.(bool); ok {
		return boolValue
	}
	return fallback
}

func (s *Service) agentSettingInt(key string, fallback int) int {
	if s == nil || s.Settings == nil {
		return fallback
	}
	raw := s.Settings.Raw()
	agent, _ := raw["agent"].(map[string]any)
	switch value := agent[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return int(parsed)
		}
	}
	return fallback
}

func (s *Service) updateAssistantMessageUsage(ctx context.Context, state *agentRunState, messageID string, event agentbasic.AgentEvent) error {
	if s == nil || s.DB == nil || state == nil || strings.TrimSpace(messageID) == "" {
		return nil
	}
	columns, err := db.TableColumnSet(ctx, s.DB, "narrator_messages")
	if err != nil || len(columns) == 0 {
		return err
	}
	usage := state.lastTokenUsage
	provider := firstNonEmptyString(state.providerPrefix, event.Provider)
	model := firstNonEmptyString(ptrString(state.currentNarrator().Model), event.Model)
	credentialID := strings.TrimSpace(event.CredentialID)
	values := map[string]any{
		"provider":                    nullableTrimmedString(provider),
		"credential_id":               nullableTrimmedString(credentialID),
		"model":                       nullableTrimmedString(model),
		"tokens_in":                   usageInputTokens(usage),
		"output_tokens":               usageCompletionTokens(usage),
		"cached_input_tokens":         usageCachedInputTokens(usage),
		"cache_creation_input_tokens": usageCacheCreationInputTokens(usage),
		"cache_creation_5m_tokens":    usageCacheCreation5mTokens(usage),
		"cache_creation_1h_tokens":    usageCacheCreation1hTokens(usage),
		"reasoning_tokens":            usageReasoningTokens(usage),
		"cost_usd":                    nullableCostForUsage(usage, provider, model),
		"duration_ms":                 nullablePositiveInt64(time.Since(state.goalTurnStartedAt).Milliseconds()),
		"context_percent":             nullableFloatPtr(state.lastContextPercent),
		"meter_usage":                 nullableFloatPtr(state.lastMeterUsage),
		"meter_unit":                  nullableTrimmedString(state.lastMeterUnit),
		"turn_usage_json":             usageTurnJSON(usage),
	}
	sets := []string{}
	args := []any{}
	for _, column := range []string{"provider", "credential_id", "model", "tokens_in", "output_tokens", "cached_input_tokens", "cache_creation_input_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "reasoning_tokens", "cost_usd", "duration_ms", "context_percent", "meter_usage", "meter_unit", "turn_usage_json"} {
		if !columns[column] {
			continue
		}
		if values[column] == nil && column != "cost_usd" && column != "credential_id" {
			continue
		}
		sets = append(sets, column+" = ?")
		args = append(args, values[column])
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, messageID)
	_, err = s.DB.ExecContext(ctx, `UPDATE narrator_messages SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

func (s *Service) linkPendingAPIRequestsToMessage(ctx context.Context, state *agentRunState, messageID string) error {
	if s == nil || s.DB == nil || state == nil || strings.TrimSpace(messageID) == "" || len(state.pendingApiRequestIDs) == 0 {
		return nil
	}
	columns, err := db.TableColumnSet(ctx, s.DB, "api_requests")
	if err != nil || !columns["message_id"] || !columns["id"] {
		return err
	}
	for _, requestID := range state.pendingApiRequestIDs {
		if strings.TrimSpace(requestID) == "" {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE api_requests SET message_id = ? WHERE id = ?`, messageID, requestID); err != nil {
			return err
		}
	}
	state.pendingApiRequestIDs = nil
	return nil
}

func usageInputTokens(usage *agentbasic.UsageSnapshot) int {
	if usage == nil {
		return 0
	}
	if usage.InputTokens != 0 {
		return usage.InputTokens
	}
	return usage.PromptTokens
}

func usageCompletionTokens(usage *agentbasic.UsageSnapshot) int {
	if usage == nil {
		return 0
	}
	return usage.CompletionTokens
}

func usageCachedInputTokens(usage *agentbasic.UsageSnapshot) int {
	if usage == nil {
		return 0
	}
	return usage.CachedInputTokens
}

func usageCacheCreationInputTokens(usage *agentbasic.UsageSnapshot) int {
	if usage == nil {
		return 0
	}
	return usage.CacheCreationInputTokens
}

func usageCacheCreation5mTokens(usage *agentbasic.UsageSnapshot) int {
	if usage == nil {
		return 0
	}
	return usage.CacheCreation5mTokens
}

func usageCacheCreation1hTokens(usage *agentbasic.UsageSnapshot) int {
	if usage == nil {
		return 0
	}
	return usage.CacheCreation1hTokens
}

func usageReasoningTokens(usage *agentbasic.UsageSnapshot) int {
	if usage == nil {
		return 0
	}
	return usage.ReasoningTokens
}

func usageTurnJSON(usage *agentbasic.UsageSnapshot) any {
	if usage == nil {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{
		"inputTokens":                usageInputTokens(usage),
		"outputTokens":               usageCompletionTokens(usage),
		"cachedInputTokens":          usageCachedInputTokens(usage),
		"cacheCreationInputTokens":   usageCacheCreationInputTokens(usage),
		"cacheCreation5mInputTokens": usageCacheCreation5mTokens(usage),
		"cacheCreation1hInputTokens": usageCacheCreation1hTokens(usage),
		"reasoningTokens":            usageReasoningTokens(usage),
	})
	return string(payload)
}

func nullablePositiveInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullablePositiveFloat(value float64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullableFloatPtr(value *float64) any {
	if value == nil || *value <= 0 {
		return nil
	}
	return *value
}

func firstPositiveFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func contextPercentFromEvent(event agentbasic.AgentEvent) float64 {
	if event.Percentage > 0 {
		return clampPercent(event.Percentage)
	}
	return contextPercentForUsage(event.Usage)
}

func contextPercentForUsage(usage *agentbasic.UsageSnapshot) float64 {
	if usage == nil || usage.ContextWindow <= 0 {
		return 0
	}
	inputTokens := usage.PromptTokens
	if inputTokens == 0 {
		inputTokens = usage.InputTokens
	}
	if inputTokens <= 0 {
		return 0
	}
	return clampPercent(float64(inputTokens) / float64(usage.ContextWindow) * 100)
}

func clampPercent(value float64) float64 {
	if value > 100 {
		return 100
	}
	return value
}

func nullableCostForUsage(usage *agentbasic.UsageSnapshot, provider, model string) any {
	cost, ok := costForUsage(usage, provider, model)
	if !ok {
		return nil
	}
	return cost
}

func costForUsage(usage *agentbasic.UsageSnapshot, _ string, model string) (float64, bool) {
	if usage == nil {
		return 0, false
	}
	lowerModel := strings.ToLower(model)
	var inputRate, outputRate, cacheReadRate, cacheWriteRate float64
	switch {
	case strings.Contains(lowerModel, "claude-3-5-sonnet"):
		inputRate, outputRate, cacheReadRate, cacheWriteRate = 3.0, 15.0, 0.3, 3.75
	case strings.Contains(lowerModel, "gpt-4o"):
		inputRate, outputRate, cacheReadRate, cacheWriteRate = 2.5, 10.0, 1.25, 0
	default:
		return 0, false
	}
	inputCost := float64(usageInputTokens(usage)) / 1_000_000 * inputRate
	outputCost := float64(usageCompletionTokens(usage)) / 1_000_000 * outputRate
	cacheReadCost := float64(usageCachedInputTokens(usage)) / 1_000_000 * cacheReadRate
	cacheCreationCost := float64(usageCacheCreationInputTokens(usage)) / 1_000_000 * cacheWriteRate
	return inputCost + outputCost + cacheReadCost + cacheCreationCost, true
}

func int64FromMetadata(metadata map[string]any, key string) int64 {
	if metadata == nil {
		return 0
	}
	switch v := metadata[key].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	case json.Number:
		parsed, _ := v.Int64()
		return parsed
	default:
		return 0
	}
}

func floatFromMetadata(metadata map[string]any, key string) float64 {
	if metadata == nil {
		return 0
	}
	switch v := metadata[key].(type) {
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case float64:
		return v
	case json.Number:
		parsed, _ := v.Float64()
		return parsed
	default:
		return 0
	}
}

func (s *Service) ensureAssistantDraft(ctx context.Context, state *agentRunState) error {
	_, err := s.ensureDraftID(ctx, state)
	return err
}

// ensureDraftID returns the assistant draft message id for the run, creating it
// lazily if needed. It is safe to call from both the agent loop goroutine (tool
// executor) and the caller goroutine (handleAgentEvent): draftCreateMu serializes
// the create path so concurrent callers never insert two drafts, and the id is
// published through the mu-guarded value field.
func (s *Service) ensureDraftID(ctx context.Context, state *agentRunState) (string, error) {
	if state == nil {
		return "", nil
	}
	if existing := state.draftID(); strings.TrimSpace(existing) != "" {
		return existing, nil
	}
	state.draftCreateMu.Lock()
	defer state.draftCreateMu.Unlock()
	// Re-check after acquiring the create lock: another goroutine may have created
	// the draft while we were blocked.
	if existing := state.draftID(); strings.TrimSpace(existing) != "" {
		return existing, nil
	}
	msg, err := s.addMessageWithBlocks(ctx, state.narratorID, "assistant", "", []map[string]any{}, nil)
	if err != nil {
		return "", err
	}
	state.setDraftID(msg.ID)
	return msg.ID, nil
}

func (s *Service) broadcastAgentEvent(narratorID string, event agentbasic.AgentEvent) {
	if s.Hub == nil {
		return
	}
	payload := map[string]any{"type": event.Type, "narratorId": narratorID}
	if event.Text != "" {
		payload["text"] = event.Text
	}
	if event.OutputIndex != nil {
		payload["outputIndex"] = *event.OutputIndex
	}
	if event.ToolUseID != "" {
		payload["toolUseId"] = event.ToolUseID
	}
	if event.ToolName != "" {
		payload["toolName"] = event.ToolName
	}
	if event.Input != nil {
		payload["input"] = event.Input
		payload["inputJson"] = event.Input
	}
	if event.Output != "" {
		payload["output"] = event.Output
	}
	if event.IsError {
		payload["isError"] = event.IsError
	}
	if event.DurationMs != 0 {
		payload["durationMs"] = event.DurationMs
	}
	if event.Elapsed != 0 {
		payload["elapsed"] = event.Elapsed
	}
	if event.StreamStartedAt != 0 {
		payload["streamStartedAt"] = event.StreamStartedAt
	}
	if event.PermissionStartedAt != 0 {
		payload["permissionStartedAt"] = event.PermissionStartedAt
	}
	if event.ExecutionStartedAt != 0 {
		payload["executionStartedAt"] = event.ExecutionStartedAt
	}
	if event.CompletedAt != 0 {
		payload["completedAt"] = event.CompletedAt
	}
	if event.UpdatedInput != nil {
		payload["updatedInput"] = event.UpdatedInput
	}
	if event.BrokenInputOverride != nil {
		payload["brokenInputOverride"] = event.BrokenInputOverride
	}
	if strings.TrimSpace(event.ParentToolUseID) != "" {
		payload["parentToolUseId"] = strings.TrimSpace(event.ParentToolUseID)
	}
	if event.Streaming != nil {
		payload["inputCharsTotal"] = event.Streaming.InputCharsTotal
		if event.Streaming.ContentCharsReceived != 0 {
			payload["contentCharsReceived"] = event.Streaming.ContentCharsReceived
		}
		if event.Streaming.ExtractedFilePath != "" {
			payload["extractedFilePath"] = event.Streaming.ExtractedFilePath
		}
		if len(event.Streaming.ExtractedFields) > 0 {
			payload["extractedFields"] = event.Streaming.ExtractedFields
		}
		if event.Streaming.StreamingFieldName != "" || event.Streaming.StreamingFieldDelta != "" {
			payload["streamingField"] = map[string]any{"name": event.Streaming.StreamingFieldName, "delta": event.Streaming.StreamingFieldDelta}
		}
		if len(event.Streaming.ProviderSpecificState) > 0 {
			payload["metadata"] = event.Streaming.ProviderSpecificState
		}
	}
	if event.Block != nil {
		payload["block"] = contentBlockToMap(event.Block)
	}
	if event.Message != "" {
		payload["message"] = event.Message
	}
	if event.Code != "" {
		payload["code"] = event.Code
	}
	if event.Reason != "" {
		payload["reason"] = event.Reason
	}
	if event.Attempt != 0 {
		payload["attempt"] = event.Attempt
	}
	if event.MaxRetries != 0 {
		payload["maxRetries"] = event.MaxRetries
	}
	if event.DelayMs != 0 {
		payload["delayMs"] = event.DelayMs
	}
	if event.DurationMs != 0 {
		payload["durationMs"] = event.DurationMs
	}
	if event.Usage != nil {
		payload["usage"] = event.Usage
		payload["promptTokens"] = event.Usage.PromptTokens
		payload["inputTokens"] = event.Usage.InputTokens
		payload["completionTokens"] = event.Usage.CompletionTokens
		payload["reasoningTokens"] = event.Usage.ReasoningTokens
		payload["cachedInputTokens"] = event.Usage.CachedInputTokens
		payload["cacheCreationInputTokens"] = event.Usage.CacheCreationInputTokens
		payload["cacheCreation5mTokens"] = event.Usage.CacheCreation5mTokens
		payload["cacheCreation1hTokens"] = event.Usage.CacheCreation1hTokens
		payload["contextWindow"] = event.Usage.ContextWindow
		payload["isEstimated"] = event.Usage.Estimated
		if event.Type == "context_usage" && event.Percentage == 0 {
			if pct := contextPercentForUsage(event.Usage); pct > 0 {
				payload["percentage"] = pct
			}
		}
	}
	if event.Percentage != 0 {
		payload["percentage"] = event.Percentage
	} else if event.Type == "context_usage" {
		if _, ok := payload["percentage"]; !ok {
			payload["percentage"] = 0
		}
	}
	if event.RequestID != "" {
		payload["requestId"] = event.RequestID
	}
	if event.ProviderID != "" {
		payload["providerId"] = event.ProviderID
	}
	if event.ProviderPrefix != "" {
		payload["providerPrefix"] = event.ProviderPrefix
	}
	if event.Balance != nil {
		payload["balance"] = *event.Balance
	}
	if event.Required != nil {
		payload["required"] = *event.Required
	}
	if event.ResumeAction != "" {
		payload["resumeAction"] = event.ResumeAction
	}
	if event.Provider != "" {
		payload["provider"] = event.Provider
	}
	if event.Model != "" {
		payload["model"] = event.Model
	}
	if event.Metadata != nil {
		payload["metadata"] = event.Metadata
	}
	if len(event.SideCars) > 0 {
		payload["sideCars"] = agentSideCarsPayload(event.SideCars)
	}
	if event.Type == "web_search" {
		payload["id"] = event.ToolUseID
		payload["status"] = event.Message
		if event.Metadata != nil {
			if query, ok := event.Metadata["query"]; ok {
				payload["query"] = query
			}
			if queries, ok := event.Metadata["queries"]; ok {
				payload["queries"] = queries
			}
			if action, ok := event.Metadata["action"]; ok {
				payload["action"] = action
			}
		}
	}
	if event.Type == "image_generation" {
		payload["id"] = event.ToolUseID
		payload["status"] = event.Message
		if event.Metadata != nil {
			if revisedPrompt, ok := event.Metadata["revisedPrompt"]; ok {
				payload["revisedPrompt"] = revisedPrompt
			}
			if result, ok := event.Metadata["result"]; ok {
				payload["result"] = result
			}
			if partialImageIndex, ok := event.Metadata["partialImageIndex"]; ok {
				payload["partialImageIndex"] = partialImageIndex
			}
			if partialImageB64, ok := event.Metadata["partialImageB64"]; ok {
				payload["partialImageB64"] = partialImageB64
			}
		}
	}
	if event.Type == "metering" && event.Metadata != nil {
		if unit, ok := event.Metadata["unit"]; ok {
			payload["unit"] = unit
		}
		if unitPlural, ok := event.Metadata["unitPlural"]; ok {
			payload["unitPlural"] = unitPlural
		}
		if usage, ok := event.Metadata["usage"]; ok {
			payload["usage"] = usage
		}
	}
	if event.Type == "nkp_quota_balance" && event.Metadata != nil {
		if quotaBalance, ok := event.Metadata["quotaBalance"]; ok {
			payload["quotaBalance"] = quotaBalance
		}
	}
	if event.Type == "nkp_queue_status" && event.Metadata != nil {
		if position, ok := event.Metadata["position"]; ok {
			payload["position"] = position
		}
		if queueDepth, ok := event.Metadata["queueDepth"]; ok {
			payload["queueDepth"] = queueDepth
		}
	}
	if event.Type == "queue_status" {
		if event.Metadata != nil {
			if position, ok := event.Metadata["position"]; ok {
				payload["position"] = position
			}
			if queueDepth, ok := event.Metadata["queueDepth"]; ok {
				payload["queueDepth"] = queueDepth
			}
		}
		if strings.TrimSpace(event.Message) != "" {
			payload["queueMessage"] = event.Message
		}
	}
	if event.Type == "quota_balance" {
		if strings.TrimSpace(event.Message) != "" {
			payload["quotaBalance"] = event.Message
		} else if event.Metadata != nil {
			if quotaBalance, ok := event.Metadata["quotaBalance"]; ok {
				payload["quotaBalance"] = quotaBalance
			}
		}
		payload["detailedQuotaBalance"] = stringFromMetadata(event.Metadata, "detailedQuotaBalance")
	}
	s.Hub.BroadcastNarrator(narratorID, payload)
}

func (s *Service) persistCustomApiQuotaBalance(providerPrefix, quotaBalance, detailedQuotaBalance string) {
	providerPrefix = strings.TrimSpace(providerPrefix)
	quotaBalance = strings.TrimSpace(quotaBalance)
	detailedQuotaBalance = strings.TrimSpace(detailedQuotaBalance)
	if providerPrefix == "" || quotaBalance == "" || s.Settings == nil {
		return
	}
	providerID := ""
	for _, item := range anySliceFromMap(s.Settings.Raw()["customApiProviders"]) {
		provider, ok := item.(map[string]any)
		if !ok || strings.TrimSpace(stringValue(provider["prefix"])) != providerPrefix {
			continue
		}
		providerID = strings.TrimSpace(stringValue(provider["id"]))
		break
	}
	if providerID == "" {
		return
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, ".narrafork", "custom-api-quotas.json")
	cache := map[string]map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cache)
	}
	cache[providerID] = map[string]any{
		"quotaBalance":         quotaBalance,
		"detailedQuotaBalance": nullableTrimmedString(detailedQuotaBalance),
		"fetchedAt":            time.Now().UnixMilli(),
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

func agentSideCarsPayload(sideCars []agentbasic.AgentSideCar) []map[string]any {
	items := make([]map[string]any, 0, len(sideCars))
	for _, sideCar := range sideCars {
		item := map[string]any{
			"target":  sideCar.Target,
			"source":  sideCar.Source,
			"content": sideCar.Content,
		}
		if strings.TrimSpace(sideCar.ToolUseID) != "" {
			item["toolUseId"] = strings.TrimSpace(sideCar.ToolUseID)
		}
		if sideCar.OrderIndex != 0 {
			item["orderIndex"] = sideCar.OrderIndex
		}
		items = append(items, item)
	}
	return items
}

func (s *Service) persistNugQuotaBalance(providerPrefix, quotaBalance, detailedQuotaBalance string) {
	providerPrefix = strings.TrimSpace(providerPrefix)
	detailedQuotaBalance = strings.TrimSpace(detailedQuotaBalance)
	if providerPrefix == "" || strings.TrimSpace(quotaBalance) == "" || s.Settings == nil {
		return
	}
	balance, ok := quotaBalanceNumber(quotaBalance)
	if !ok {
		return
	}
	providerID := providerIDForPrefix(s.Settings.Raw(), "nugProviders", providerPrefix)
	if providerID == "" {
		return
	}
	patch := map[string]any{"balance": balance}
	if detailedQuotaBalance != "" {
		patch["detailedQuotaBalance"] = detailedQuotaBalance
	}
	persistQuotaCacheEntry("nug-quotas.json", providerID, patch)
}

func (s *Service) persistNKPQuotaBalance(providerPrefix string, metadata map[string]any) {
	providerPrefix = strings.TrimSpace(providerPrefix)
	if providerPrefix == "" || metadata == nil || s.Settings == nil {
		return
	}
	balance, ok := quotaBalanceNumber(metadata["quotaBalance"])
	if !ok {
		return
	}
	providerID := providerIDForPrefix(s.Settings.Raw(), "nkpProviders", providerPrefix)
	if providerID == "" {
		return
	}
	persistQuotaCacheEntry("nkp-quotas.json", providerID, map[string]any{"quotaBalance": balance})
}

func providerIDForPrefix(raw map[string]any, providerField, providerPrefix string) string {
	for _, item := range anySliceFromMap(raw[providerField]) {
		provider, ok := item.(map[string]any)
		if !ok || strings.TrimSpace(stringValue(provider["prefix"])) != providerPrefix {
			continue
		}
		return strings.TrimSpace(stringValue(provider["id"]))
	}
	return ""
}

func quotaBalanceNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func persistQuotaCacheEntry(fileName, providerID string, patch map[string]any) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, ".narrafork", fileName)
	cache := map[string]map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cache)
	}
	entry := cache[providerID]
	if entry == nil {
		entry = map[string]any{}
	}
	for key, value := range patch {
		entry[key] = value
	}
	entry["fetchedAt"] = time.Now().UnixMilli()
	cache[providerID] = entry
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

func stringFromMetadata(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	if value, ok := metadata[key]; ok {
		return strings.TrimSpace(stringFromAny(value))
	}
	return ""
}

func anySliceFromMap(value any) []any {
	items, _ := value.([]any)
	return items
}

func providerPrefixFromModelValue(model string) string {
	idx := strings.Index(model, ":")
	if idx <= 0 {
		return ""
	}
	return strings.TrimSpace(model[:idx])
}

func contentBlockToMap(block *agentbasic.ContentBlock) map[string]any {
	if block == nil {
		return map[string]any{}
	}
	switch block.Type {
	case "tool_use":
		m := map[string]any{"type": "tool_use", "id": block.ToolUseID, "name": block.ToolName, "input": block.Input}
		if block.StreamStartedAt != 0 {
			m["streamStartedAt"] = block.StreamStartedAt
		}
		if block.OutputIndex != nil {
			m["outputIndex"] = *block.OutputIndex
		}
		return m
	case "tool_result":
		return map[string]any{"type": "tool_result", "tool_use_id": block.ToolUseID, "content": block.Output, "is_error": block.IsError}
	case "reasoning":
		m := map[string]any{"type": "reasoning", "text": block.Text}
		if block.ProviderMetadata != nil {
			m["providerMetadata"] = block.ProviderMetadata
		}
		if block.OutputIndex != nil {
			m["outputIndex"] = *block.OutputIndex
		}
		return m
	case "redacted_thinking":
		return map[string]any{"type": "redacted_thinking", "data": block.Data}
	case "web_search":
		return map[string]any{"type": "web_search", "id": block.ID, "status": block.Status, "query": block.Query, "queries": block.Queries}
	case "image_generation":
		return map[string]any{"type": "image_generation", "id": block.ID, "status": block.Status, "revisedPrompt": block.RevisedPrompt, "result": block.Result}
	default:
		return map[string]any{"type": firstNonEmptyString(block.Type, "text"), "text": block.Text}
	}
}

func upsertToolUseBlock(state *agentRunState, block map[string]any) {
	idValue := strings.TrimSpace(stringFromAny(block["id"]))
	if idValue == "" {
		insertAssistantBlock(state, block)
		return
	}
	for i, existing := range state.blocks {
		if existing["type"] == "tool_use" && stringFromAny(existing["id"]) == idValue {
			state.blocks[i] = block
			return
		}
	}
	insertAssistantBlock(state, block)
}

// blockOutputIndex extracts a block's numeric outputIndex, handling both the
// in-memory int form (freshly built maps) and the float64 form (JSON round-trip).
func blockOutputIndex(block map[string]any) (int, bool) {
	switch v := block["outputIndex"].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

// insertAssistantBlock inserts a freshly completed assistant block into
// state.blocks in the correct position, mirroring the TS appendBlockToMessage
// ordering contract (narrator-persistence.ts:777-804). Without this, blocks were
// appended in event-arrival order, so a reasoning/text block flushed at turn end
// (block_complete) landed AFTER a tool_use block emitted earlier during the
// stream, producing the wrong [tool_use, reasoning] order in the persisted
// assistant message.
//
// Rules (matching TS):
//  1. Block carries a numeric outputIndex → append then stable-sort all blocks by
//     outputIndex (blocks without an index sort to the end, ties keep insertion order).
//  2. reasoning without outputIndex → insert before the first non-reasoning block
//     (reasoning leads the message).
//  3. text without outputIndex → insert before the first tool_use block
//     (visible text precedes tool calls).
//  4. otherwise (tool_use, redacted_thinking, web_search, …) → append at the end.
func insertAssistantBlock(state *agentRunState, block map[string]any) {
	if state == nil || block == nil {
		return
	}
	if _, ok := blockOutputIndex(block); ok {
		state.blocks = append(state.blocks, block)
		sort.SliceStable(state.blocks, func(i, j int) bool {
			ai, aok := blockOutputIndex(state.blocks[i])
			bi, bok := blockOutputIndex(state.blocks[j])
			if !aok {
				return false // i has no index → sorts after j
			}
			if !bok {
				return true // j has no index → i sorts before
			}
			return ai < bi
		})
		return
	}
	switch block["type"] {
	case "reasoning":
		for i, existing := range state.blocks {
			if existing["type"] != "reasoning" {
				state.blocks = append(state.blocks[:i], append([]map[string]any{block}, state.blocks[i:]...)...)
				return
			}
		}
		state.blocks = append(state.blocks, block)
	case "text":
		for i, existing := range state.blocks {
			if existing["type"] == "tool_use" {
				state.blocks = append(state.blocks[:i], append([]map[string]any{block}, state.blocks[i:]...)...)
				return
			}
		}
		state.blocks = append(state.blocks, block)
	default:
		state.blocks = append(state.blocks, block)
	}
}

func (s *Service) recordRunningToolCall(ctx context.Context, narratorID, messageID string, narrator Narrator, event agentbasic.AgentEvent) error {
	if event.ToolUseID == "" || event.ToolName == "" {
		return nil
	}
	var existing sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, narratorID, event.ToolUseID).Scan(&existing); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("query running tool call: %w", err)
		}
	}
	if existing.Valid {
		return nil
	}
	inputJSON, _ := json.Marshal(event.Input)
	isBackground := 0
	if narrator.IsBackground {
		isBackground = 1
	}
	now := db.NowISO()
	streamStartedAt := any(nil)
	if event.StreamStartedAt != 0 {
		streamStartedAt = time.UnixMilli(event.StreamStartedAt).UTC().Format(time.RFC3339Nano)
	}
	columns, colErr := db.TableColumnSet(ctx, s.DB, "narrator_tool_calls")
	if colErr == nil && columns["stream_started_at"] {
		_, err := s.DB.ExecContext(ctx, `INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, status, stream_started_at, execution_started_at, is_background, created_at) SELECT ?, ?, ?, ?, ?, ?, 'running', ?, ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ?)`, id.New(), narratorID, messageID, event.ToolUseID, event.ToolName, string(inputJSON), streamStartedAt, now, isBackground, now, narratorID, event.ToolUseID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, status, execution_started_at, is_background, created_at) SELECT ?, ?, ?, ?, ?, ?, 'running', ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ?)`, id.New(), narratorID, messageID, event.ToolUseID, event.ToolName, string(inputJSON), now, isBackground, now, narratorID, event.ToolUseID)
	return err
}

func (s *Service) finishToolCallFromEvent(ctx context.Context, state *agentRunState, event agentbasic.AgentEvent) error {
	if event.ToolRun == nil {
		return nil
	}
	draftID := state.draftID()
	run := *event.ToolRun
	if run.Recorded {
		return s.persistToolResultSideCars(ctx, state.narratorID, draftID, event.ToolUseID, event.SideCars)
	}
	status := toolRunDBStatus(run.Status, event.IsError || strings.TrimSpace(run.Error) != "")
	outputJSON, _ := json.Marshal(map[string]any{"output": run.Output})
	inputJSON := toolRunArgumentsInputJSON(run.Arguments)
	now := db.NowISO()
	sets := []string{"status = ?", "input_json = COALESCE(?, input_json)", "output_json = ?", "execution_started_at = COALESCE(execution_started_at, ?)", "completed_at = ?", "error_message = ?", "result_message_id = ?"}
	args := []any{status, inputJSON, string(outputJSON), now, now, nullableTrimmedString(run.Error), draftID}
	if columns, colErr := db.TableColumnSet(ctx, s.DB, "narrator_tool_calls"); colErr == nil && columns["duration_ms"] && event.DurationMs > 0 {
		sets = append(sets, "duration_ms = ?")
		args = append(args, event.DurationMs)
	}
	args = append(args, state.narratorID, run.ID)
	res, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET `+strings.Join(sets, ", ")+` WHERE narrator_id = ? AND tool_use_id = ?`, args...)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		if err := s.persistToolRun(ctx, state.narratorID, draftID, run); err != nil {
			return err
		}
		return s.persistToolResultSideCars(ctx, state.narratorID, draftID, event.ToolUseID, event.SideCars)
	}
	_ = s.ensureGoalReflectionMetadata(ctx, state.narratorID, run)
	var toolCallID sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, state.narratorID, run.ID).Scan(&toolCallID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("query finished tool call: %w", err)
		}
	}
	if err := s.persistToolResultSideCars(ctx, state.narratorID, draftID, event.ToolUseID, event.SideCars); err != nil {
		return err
	}
	return s.persistFileSnapshot(ctx, state.narratorID, draftID, sqlNullString(toolCallID), run)
}

func (s *Service) ensureGoalReflectionMetadata(ctx context.Context, narratorID string, run agentbasic.ToolRun) error {
	if !strings.EqualFold(strings.TrimSpace(run.Name), "UpdateGoal") || strings.TrimSpace(run.ID) == "" {
		return nil
	}
	var suggestions, reason sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT permission_suggestions, permission_decision_reason FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, narratorID, run.ID).Scan(&suggestions, &reason); err != nil {
		return nil
	}
	if strings.TrimSpace(suggestions.String) != "" && strings.TrimSpace(reason.String) != "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(run.Output), &payload); err != nil {
		return nil
	}
	input := map[string]any{}
	_ = json.Unmarshal([]byte(run.Arguments), &input)
	status := "confirmed"
	if strings.TrimSpace(stringFromAny(payload["error"])) != "" || strings.TrimSpace(run.Error) != "" {
		status = "cancelled"
	}
	nextSteps := strings.TrimSpace(stringFromAny(payload["nextSteps"]))
	reasonText := firstNonEmptyString(
		strings.TrimSpace(stringFromAny(payload["reflection"])),
		strings.TrimSpace(stringFromAny(payload["completionEvidence"])),
		strings.TrimSpace(stringFromAny(payload["feedback"])),
		strings.TrimSpace(stringFromAny(payload["error"])),
		strings.TrimSpace(run.Error),
	)
	message := goalReflectionStatusReason(status, reasonText, nextSteps)
	meta := &goalReflectionContext{ToolUseID: run.ID, ToolName: run.Name, InputJSON: input}
	suggestionsJSON, _ := json.Marshal(goalReflectionSuggestions(meta, status, reasonText, nextSteps))
	_, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET permission_decision_reason = CASE WHEN permission_decision_reason IS NULL OR TRIM(permission_decision_reason) = '' THEN ? ELSE permission_decision_reason END, permission_suggestions = CASE WHEN permission_suggestions IS NULL OR TRIM(permission_suggestions) = '' THEN ? ELSE permission_suggestions END WHERE narrator_id = ? AND tool_use_id = ?`, message, string(suggestionsJSON), narratorID, run.ID)
	return err
}

func toolRunArgumentsInputJSON(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var input any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil
	}
	return string(encoded)
}

func (s *Service) latestRunNarrator(ctx context.Context, state *agentRunState) Narrator {
	if latest, err := s.Get(ctx, state.narratorID); err == nil {
		return latest
	}
	return state.currentNarrator()
}

func (s *Service) finishAgentRunReviewReset(ctx context.Context, state *agentRunState, message string) error {
	msg, err := s.addMessage(ctx, state.narratorID, "user", message, nil)
	if err != nil {
		return err
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "user_message", "narratorId": state.narratorID, "message": msg})
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'working', substatus = '["calling_model"]', error_message = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), state.narratorID); err != nil {
		return err
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "status", "narratorId": state.narratorID, "status": "working", "substatus": []string{"calling_model"}, "isBackground": state.currentNarrator().IsBackground})
	}
	s.scheduleNarratorRetry(state.narratorID)
	return nil
}

func (s *Service) backgroundRunParent(ctx context.Context, state *agentRunState, latest Narrator) (string, bool) {
	stateNarrator := state.currentNarrator()
	if latest.ParentNarratorID == nil || strings.TrimSpace(*latest.ParentNarratorID) == "" {
		if stateNarrator.ParentNarratorID == nil || strings.TrimSpace(*stateNarrator.ParentNarratorID) == "" {
			return "", latest.IsBackground
		}
		parentID := strings.TrimSpace(*stateNarrator.ParentNarratorID)
		return parentID, latest.IsBackground || s.backgroundTaskRunning(ctx, parentID, state.narratorID)
	}
	parentID := strings.TrimSpace(*latest.ParentNarratorID)
	return parentID, latest.IsBackground || s.backgroundTaskRunning(ctx, parentID, state.narratorID)
}

func (s *Service) backgroundTaskRunning(ctx context.Context, parentID, taskID string) bool {
	if s == nil || s.DB == nil || strings.TrimSpace(parentID) == "" || strings.TrimSpace(taskID) == "" {
		return false
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM background_tasks WHERE id = ? AND parent_narrator_id = ? AND type = 'agent' AND status = 'running'`, taskID, parentID).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// broadcastSubagentStatusChanged notifies the parent narrator that one of its
// foreground subagents changed status (mirrors the TypeScript subagent-executor
// subagent_status_changed broadcast). No-op for non-subagent narrators, orphan
// subagents, or when the Hub is unavailable.
func (s *Service) broadcastSubagentStatusChanged(latest Narrator, subagentID, status string, substatus []string) {
	if s.Hub == nil {
		return
	}
	if latest.Type != "subagent" && latest.Variant != "subagent" {
		return
	}
	if latest.ParentNarratorID == nil || strings.TrimSpace(*latest.ParentNarratorID) == "" {
		return
	}
	parentID := strings.TrimSpace(*latest.ParentNarratorID)
	if substatus == nil {
		substatus = []string{}
	}
	s.Hub.BroadcastNarrator(parentID, map[string]any{"type": "subagent_status_changed", "narratorId": parentID, "subagentNarratorId": subagentID, "status": status, "substatus": substatus})
}

// broadcastSubagentWarning notifies the parent narrator that one of its
// subagents emitted a retryable warning (`retrying` agent event), mirroring the
// TypeScript narrator-recovery subagent_warning dual broadcast
// (server/services/narrator-recovery.ts:354-364). It carries the retry metadata
// (retryCount/maxRetries/delayMs) so the parent's SubagentCard can render the
// inline retry status. No-op for primary narrators, orphan subagents, or when the
// Hub is unavailable.
func (s *Service) broadcastSubagentWarning(latest Narrator, subagentID string, event agentbasic.AgentEvent) {
	if s.Hub == nil {
		return
	}
	if latest.Type != "subagent" && latest.Variant != "subagent" {
		return
	}
	if latest.ParentNarratorID == nil || strings.TrimSpace(*latest.ParentNarratorID) == "" {
		return
	}
	parentID := strings.TrimSpace(*latest.ParentNarratorID)
	payload := map[string]any{
		"type":               "subagent_warning",
		"narratorId":         parentID,
		"subagentNarratorId": subagentID,
		"message":            event.Message,
	}
	// retryCount maps to the agent loop attempt counter, matching the hub's
	// retrying->warning translation (retryCount=attempt). Optional retry metadata
	// is included only when present, mirroring the TS optional fields.
	if event.Attempt != 0 {
		payload["retryCount"] = event.Attempt
	}
	if event.MaxRetries != 0 {
		payload["maxRetries"] = event.MaxRetries
	}
	if event.DelayMs != 0 {
		payload["delayMs"] = event.DelayMs
	}
	s.Hub.BroadcastNarrator(parentID, payload)
}

// broadcastModelSettingsApplied emits the model_settings_applied companion frame
// alongside a raw model_switched event, mirroring the TS narrator-event-handler
// dual broadcast (narrator-event-handler.ts:1712-1730). It carries the applied
// runtime model/provider plus the optional reasoningEffort (from the
// ModelSwitched detail or the event metadata). No-op when the Hub is unavailable.
func (s *Service) broadcastModelSettingsApplied(narratorID string, event agentbasic.AgentEvent) {
	if s.Hub == nil {
		return
	}
	payload := map[string]any{
		"type":       "model_settings_applied",
		"narratorId": narratorID,
		"model":      event.Model,
		"provider":   event.Provider,
	}
	reasoningEffort := ""
	if event.ModelSwitched != nil {
		reasoningEffort = strings.TrimSpace(event.ModelSwitched.ReasoningEffort)
	}
	if reasoningEffort == "" {
		reasoningEffort = strings.TrimSpace(stringFromMetadata(event.Metadata, "reasoningEffort"))
	}
	// reasoningEffort is optional in the TS type (reasoningEffort?: string | null);
	// only include it when present.
	if reasoningEffort != "" {
		payload["reasoningEffort"] = reasoningEffort
	}
	s.Hub.BroadcastNarrator(narratorID, payload)
}

func (s *Service) finishAgentRunSuccess(ctx context.Context, state *agentRunState) error {
	state.completed = true
	if draftID := strings.TrimSpace(state.draftID()); draftID != "" {
		msg, err := s.messageByID(ctx, state.narratorID, draftID)
		if err == nil {
			msg.ToolCalls, _ = s.loadToolCalls(ctx, msg.ID)
			state.lastMessage = &msg
		}
	}
	// TS treats goal usage accounting as best-effort; never fail a completed run
	// because active-goal counters could not be persisted.
	_ = s.accountGoalUsageForTurn(ctx, state)
	narrator := state.currentNarrator()
	if narrator.ChapterID != nil {
		if isReview, reset, message := s.checkAndResetReviewGitState(ctx, *narrator.ChapterID); isReview {
			if reset && strings.TrimSpace(message) != "" {
				return s.finishAgentRunReviewReset(ctx, state, message)
			}
			_ = s.concludeReviewChapter(ctx, *narrator.ChapterID)
		}
	}
	latest := s.latestRunNarrator(ctx, state)
	backgroundParentID, treatAsBackground := s.backgroundRunParent(ctx, state, latest)
	var err error
	if treatAsBackground {
		result := state.assistantText
		if strings.TrimSpace(result) == "" {
			result = "Completed"
		}
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '[]', background_status = 'completed', background_result = ?, background_completed_at = ?, error_message = NULL, updated_at = ? WHERE id = ?`, result, db.NowISO(), db.NowISO(), state.narratorID)
	} else if latest.Type == "subagent" || latest.Variant == "subagent" {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["manual_override"]', error_message = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), state.narratorID)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["unread"]', error_message = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), state.narratorID)
	}
	if err != nil {
		return err
	}
	if treatAsBackground && strings.TrimSpace(backgroundParentID) != "" {
		result := state.assistantText
		if strings.TrimSpace(result) == "" {
			result = "Completed"
		}
		if err := s.finishBackgroundTaskRecord(ctx, backgroundParentID, state.narratorID, "completed", result); err != nil {
			s.broadcastAgentErrorPersistenceFailure(state.narratorID, "background_task", err)
			return err
		}
	}
	if s.Hub != nil {
		substatus := []string{}
		if !treatAsBackground && (latest.Type == "subagent" || latest.Variant == "subagent") {
			substatus = []string{"manual_override"}
		} else if !treatAsBackground {
			substatus = []string{"unread"}
		}
		backgroundStatus := nullableTrimmedString("")
		if treatAsBackground {
			backgroundStatus = nullableTrimmedString("completed")
		}
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "status", "narratorId": state.narratorID, "status": "idle", "substatus": substatus, "isBackground": latest.IsBackground, "backgroundStatus": backgroundStatus})
		// Mirror the TypeScript subagent-executor broadcast: notify the parent narrator
		// that a (foreground) subagent finished so its subagent status badge updates.
		if !treatAsBackground {
			s.broadcastSubagentStatusChanged(latest, state.narratorID, "idle", substatus)
		}
	}
	if !treatAsBackground && latest.Type != "subagent" && latest.Variant != "subagent" {
		_, _ = s.consumeNextBufferedMessage(context.Background(), state.narratorID)
	}
	return nil
}

func (s *Service) finishAgentRunTimeout(ctx context.Context, state *agentRunState, message string) error {
	state.completed = true
	state.errored = true
	if strings.TrimSpace(message) == "" {
		message = "Provider did not produce a response before the first-token timeout"
	}
	// Fail any in-flight tool calls so they do not stay stuck "running" after a
	// timeout finalizes the run (mirrors TS closeNarrator → cleanupOrphanedToolCalls).
	if err := s.markInterruptedToolCalls(ctx, state.narratorID); err != nil {
		s.broadcastAgentErrorPersistenceFailure(state.narratorID, "interrupted_tool_calls", err)
	}
	latest := s.latestRunNarrator(ctx, state)
	backgroundParentID, treatAsBackground := s.backgroundRunParent(ctx, state, latest)
	if treatAsBackground {
		_, err := s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["timeout"]', background_status = 'failed', background_result = ?, background_completed_at = ?, error_message = ?, updated_at = ? WHERE id = ?`, message, db.NowISO(), message, db.NowISO(), state.narratorID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(backgroundParentID) != "" {
			if err := s.finishBackgroundTaskRecord(ctx, backgroundParentID, state.narratorID, "failed", message); err != nil {
				s.broadcastAgentErrorPersistenceFailure(state.narratorID, "background_task", err)
				return err
			}
		}
	} else {
		_, err := s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["timeout"]', error_message = ?, updated_at = ? WHERE id = ?`, message, db.NowISO(), state.narratorID)
		if err != nil {
			return err
		}
	}
	if s.Hub != nil {
		payload := map[string]any{"type": "status", "narratorId": state.narratorID, "status": "idle", "substatus": []string{"timeout"}, "isBackground": latest.IsBackground}
		if treatAsBackground {
			payload["backgroundStatus"] = "failed"
		}
		s.Hub.BroadcastNarrator(state.narratorID, payload)
		if !treatAsBackground {
			s.broadcastSubagentStatusChanged(latest, state.narratorID, "idle", []string{"timeout"})
		}
	}
	return nil
}

func (s *Service) finishAgentRunSilentDisconnect(ctx context.Context, state *agentRunState) error {
	state.completed = true
	state.errored = true
	message := "Provider stream disconnected before completion"
	// Convert any in-flight tool calls (recorded as running when their tool_call
	// event streamed, but never executed because the stream cut off) into explicit
	// failures, mirroring the TS closeNarrator → cleanupOrphanedToolCalls path that
	// runs on every terminal close. Without this, the tools stay "running" forever
	// and the UI shows them stuck executing after reload. Non-fatal.
	if err := s.markInterruptedToolCalls(ctx, state.narratorID); err != nil {
		s.broadcastAgentErrorPersistenceFailure(state.narratorID, "interrupted_tool_calls", err)
	}
	latest := s.latestRunNarrator(ctx, state)
	backgroundParentID, treatAsBackground := s.backgroundRunParent(ctx, state, latest)
	if treatAsBackground {
		_, err := s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["silent_disconnect"]', background_status = 'failed', background_result = ?, background_completed_at = ?, error_message = NULL, updated_at = ? WHERE id = ?`, message, db.NowISO(), db.NowISO(), state.narratorID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(backgroundParentID) != "" {
			if err := s.finishBackgroundTaskRecord(ctx, backgroundParentID, state.narratorID, "failed", message); err != nil {
				s.broadcastAgentErrorPersistenceFailure(state.narratorID, "background_task", err)
				return err
			}
		}
	} else {
		_, err := s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["silent_disconnect"]', error_message = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), state.narratorID)
		if err != nil {
			return err
		}
	}
	if s.Hub != nil {
		payload := map[string]any{"type": "status", "narratorId": state.narratorID, "status": "idle", "substatus": []string{"silent_disconnect"}, "isBackground": latest.IsBackground}
		if treatAsBackground {
			payload["backgroundStatus"] = "failed"
		}
		s.Hub.BroadcastNarrator(state.narratorID, payload)
		if !treatAsBackground {
			s.broadcastSubagentStatusChanged(latest, state.narratorID, "idle", []string{"silent_disconnect"})
		}
	}
	return nil
}

// markInterruptedToolCalls converts a narrator's in-flight tool calls into
// explicit interrupted failures when the user interrupts the run. It mirrors the
// TS markInterruptedToolCallsForMessage + cleanupOrphanedToolCalls pair
// (narrator-session.ts:4162-4205): for every tool call still in an interruptable
// status (initializing/pending/running), set status='fail', the internal
// errorMessage to "Narrator interrupted by user", and the MODEL-FACING output_json
// to the locale-resolved tool.interruptedByUser text. The output_json is wrapped
// as {"output": ...} to match how Go reconstructs tool_result content from stored
// output_json (agent_history_content.go:outputTextForAgent) and how the
// server-restart path stores it (service.go RecoverOnStartup). Without this, Go
// left interrupted tool calls orphaned until a server restart, which then
// mislabeled them with the server-restart text instead of the user-interrupt text.
//
// The locale is the narrator owner's locale (toolLocale), aligning with TS
// active.locale. completed_at is only set when currently NULL so an already
// finished call is never clobbered.
func (s *Service) markInterruptedToolCalls(ctx context.Context, narratorID string) error {
	if s == nil || s.DB == nil || strings.TrimSpace(narratorID) == "" {
		return nil
	}
	const interruptErrorMessage = "Narrator interrupted by user"
	output := interruptedByUserToolOutput(s.toolLocale(ctx, narratorID))
	outputJSON, err := json.Marshal(map[string]any{"output": output})
	if err != nil {
		return err
	}
	now := db.NowISO()
	_, err = s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = 'fail', error_message = ?, output_json = COALESCE(output_json, ?), completed_at = COALESCE(completed_at, ?) WHERE narrator_id = ? AND status IN ('initializing', 'pending', 'running')`, interruptErrorMessage, string(outputJSON), now, narratorID)
	return err
}

func (s *Service) finishAgentRunCancelled(ctx context.Context, state *agentRunState) error {
	state.completed = true
	state.errored = true
	message := "Cancelled"
	// Convert in-flight tool calls into explicit interrupted failures before
	// finalizing the narrator status, mirroring the TS interrupt cleanup
	// (narrator-session.ts:507/519). A failure here is non-fatal: the narrator
	// status update below must still run so the run does not stay stuck working.
	if err := s.markInterruptedToolCalls(ctx, state.narratorID); err != nil {
		s.broadcastAgentErrorPersistenceFailure(state.narratorID, "interrupted_tool_calls", err)
	}
	latest := s.latestRunNarrator(ctx, state)
	backgroundParentID, treatAsBackground := s.backgroundRunParent(ctx, state, latest)
	var err error
	if treatAsBackground {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '[]', background_status = 'cancelled', background_result = ?, background_completed_at = ?, error_message = NULL, updated_at = ? WHERE id = ?`, message, db.NowISO(), db.NowISO(), state.narratorID)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '[]', error_message = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), state.narratorID)
	}
	if err != nil {
		return err
	}
	if treatAsBackground && strings.TrimSpace(backgroundParentID) != "" {
		if err := s.finishBackgroundTaskRecord(ctx, backgroundParentID, state.narratorID, "cancelled", message); err != nil {
			s.broadcastAgentErrorPersistenceFailure(state.narratorID, "background_task", err)
			return err
		}
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "status", "narratorId": state.narratorID, "status": "idle", "substatus": []string{}, "isBackground": latest.IsBackground, "backgroundStatus": nullableTrimmedString(map[bool]string{true: "cancelled", false: ""}[treatAsBackground])})
		if !treatAsBackground {
			s.broadcastSubagentStatusChanged(latest, state.narratorID, "idle", []string{})
		}
	}
	return nil
}

func (s *Service) finishAgentRunError(ctx context.Context, state *agentRunState, message string) error {
	state.completed = true
	state.errored = true
	if strings.TrimSpace(message) == "" {
		message = "agent run failed"
	}
	// Fail any in-flight tool calls so they do not stay stuck "running" after an
	// error finalizes the run (mirrors TS closeNarrator → cleanupOrphanedToolCalls).
	// Non-fatal: the disp message + status update below must still run.
	if err := s.markInterruptedToolCalls(ctx, state.narratorID); err != nil {
		s.broadcastAgentErrorPersistenceFailure(state.narratorID, "interrupted_tool_calls", err)
	}
	_, _ = s.addMessage(ctx, state.narratorID, "disp", "AI provider call failed: "+message, nil)
	latest := s.latestRunNarrator(ctx, state)
	backgroundParentID, treatAsBackground := s.backgroundRunParent(ctx, state, latest)
	var err error
	if treatAsBackground {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["error"]', background_status = 'failed', background_result = ?, background_completed_at = ?, error_message = ?, updated_at = ? WHERE id = ?`, message, db.NowISO(), message, db.NowISO(), state.narratorID)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["error"]', error_message = ?, updated_at = ? WHERE id = ?`, message, db.NowISO(), state.narratorID)
	}
	if err != nil {
		return err
	}
	if treatAsBackground && strings.TrimSpace(backgroundParentID) != "" {
		if err := s.finishBackgroundTaskRecord(ctx, backgroundParentID, state.narratorID, "failed", message); err != nil {
			s.broadcastAgentErrorPersistenceFailure(state.narratorID, "background_task", err)
			return err
		}
	}
	if s.Hub != nil {
		errorCode := firstNonEmptyString(state.errorCode, "AGENT_ERROR")
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "status", "narratorId": state.narratorID, "status": "idle", "substatus": []string{"error"}, "isBackground": latest.IsBackground, "backgroundStatus": nullableTrimmedString(map[bool]string{true: "failed", false: ""}[treatAsBackground])})
		if !treatAsBackground {
			s.broadcastSubagentStatusChanged(latest, state.narratorID, "idle", []string{"error"})
		}
		s.broadcastNarratorError(state.narratorID, message, errorCode)
		// When the loop ends with an error while queued (buffered) messages remain,
		// notify the frontend so it keeps showing them (and re-syncs the authoritative
		// list). Mirrors the TS buffer_preserved error-path broadcast
		// (narrator-session.ts:2565-2577); the DB rows are already preserved. The
		// interrupted/cancelled path intentionally does NOT emit this because
		// auto-resume immediately consumes the first message.
		if remaining, err := s.loadBufferedMessages(ctx, state.narratorID, 200); err == nil && len(remaining) > 0 {
			s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "buffer_preserved", "narratorId": state.narratorID, "messages": bufferSummaries(remaining)})
		}
	}
	return nil
}

// finishAgentRunPaymentRequired finalizes a run that ended because a NUG
// provider reported insufficient balance / payment required. It mirrors the TS
// narrator-session.ts payment_required branch: persist status=idle with
// substatus=["payment_required"], store a JSON error_message blob the frontend
// parses (parsePersistedPaymentRequired) and broadcast the status. The
// structured payment_required WS event itself is broadcast by
// broadcastAgentEvent before this finalizer runs.
func (s *Service) finishAgentRunPaymentRequired(ctx context.Context, state *agentRunState, event agentbasic.AgentEvent) error {
	state.completed = true
	state.errored = true
	message := strings.TrimSpace(event.Message)
	if message == "" {
		message = "Insufficient balance"
	}
	blob := map[string]any{
		"type":         "payment_required",
		"message":      message,
		"resumeAction": firstNonEmptyString(event.ResumeAction, "retry"),
	}
	if event.ProviderID != "" {
		blob["providerId"] = event.ProviderID
	}
	if event.ProviderPrefix != "" {
		blob["providerPrefix"] = event.ProviderPrefix
	}
	if event.Balance != nil {
		blob["balance"] = *event.Balance
	}
	if event.Required != nil {
		blob["required"] = *event.Required
	}
	errorMessageJSON, err := json.Marshal(blob)
	if err != nil {
		return err
	}
	latest := s.latestRunNarrator(ctx, state)
	backgroundParentID, treatAsBackground := s.backgroundRunParent(ctx, state, latest)
	if treatAsBackground {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["payment_required"]', background_status = 'failed', background_result = ?, background_completed_at = ?, error_message = ?, updated_at = ? WHERE id = ?`, message, db.NowISO(), string(errorMessageJSON), db.NowISO(), state.narratorID)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["payment_required"]', error_message = ?, updated_at = ? WHERE id = ?`, string(errorMessageJSON), db.NowISO(), state.narratorID)
	}
	if err != nil {
		return err
	}
	if treatAsBackground && strings.TrimSpace(backgroundParentID) != "" {
		if err := s.finishBackgroundTaskRecord(ctx, backgroundParentID, state.narratorID, "failed", message); err != nil {
			s.broadcastAgentErrorPersistenceFailure(state.narratorID, "background_task", err)
			return err
		}
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "status", "narratorId": state.narratorID, "status": "idle", "substatus": []string{"payment_required"}, "isBackground": latest.IsBackground, "backgroundStatus": nullableTrimmedString(map[bool]string{true: "failed", false: ""}[treatAsBackground])})
		if !treatAsBackground {
			s.broadcastSubagentStatusChanged(latest, state.narratorID, "idle", []string{"payment_required"})
		}
	}
	return nil
}

func (s *Service) registerActiveRun(narratorID string, cancel context.CancelFunc) string {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.activeRuns == nil {
		s.activeRuns = map[string]context.CancelFunc{}
	}
	if s.activeRunToken == nil {
		s.activeRunToken = map[string]string{}
	}
	runToken := id.New()
	s.activeRuns[narratorID] = cancel
	s.activeRunToken[narratorID] = runToken
	return runToken
}

func (s *Service) unregisterActiveRun(narratorID string, runToken string) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.activeRunToken[narratorID] == runToken {
		delete(s.activeRuns, narratorID)
		delete(s.activeRunToken, narratorID)
		// Cancel any pending trailing git-status broadcast so it does not fire
		// after the run has finished (mirrors TS clearTimeout on finalize).
		s.clearGitTrackTimer(narratorID)
	}
}

func (s *Service) CancelRun(narratorID string) bool {
	s.runMu.Lock()
	cancel := s.activeRuns[narratorID]
	s.runMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (s *Service) isRunActive(narratorID string) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.activeRuns[narratorID] != nil
}

func isCancelledError(err error) bool {
	return errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "aborted") || strings.Contains(strings.ToLower(err.Error()), "canceled")
}
