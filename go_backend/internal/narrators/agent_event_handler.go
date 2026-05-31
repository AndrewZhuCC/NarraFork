package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	narratorID             string
	narrator               Narrator
	draftMessageID         *string
	blocks                 []map[string]any
	assistantText          string
	lastMessage            *Message
	completed              bool
	errored                bool
	errorMessage           string
	errorCode              string
	hadToolUses            bool
	skipGoalContinuation   bool
	goalTurnStartedAt      time.Time
	goalTokenUsageBaseline *agentbasic.UsageSnapshot
	lastTokenUsage         *agentbasic.UsageSnapshot
	pendingApiRequestIDs   []string
	apiRequests            map[string]apiRequestRunState
	streamedBytes          int
	providerPrefix         string
	planApprovedMode       string
	planApprovedPrompt     string
	planApprovedBy         string
	sideCarToolCount       int
}

func (state *agentRunState) resetAssistantDraft() {
	if state == nil {
		return
	}
	if state.draftMessageID != nil {
		*state.draftMessageID = ""
	}
	state.blocks = nil
	state.assistantText = ""
	state.hadToolUses = false
}

func (s *Service) handleAgentEvent(ctx context.Context, state *agentRunState, event agentbasic.AgentEvent) error {
	if strings.TrimSpace(event.Provider) != "" {
		state.providerPrefix = event.Provider
	}
	s.broadcastAgentEvent(state.narratorID, event)
	if state.narrator.IsBackground && state.narrator.ParentNarratorID != nil && (event.Type == "stream_text" || event.Type == "stream_reasoning") && event.Text != "" {
		state.streamedBytes += len([]byte(event.Text))
		s.broadcastBackgroundTaskOutput(*state.narrator.ParentNarratorID, state.narratorID, state.streamedBytes)
	}
	switch event.Type {
	case "stream_text", "stream_reasoning", "tool_use_chunk", "retrying", "web_search", "image_generation", "output_truncated", "model_switched", "tool_inventory":
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
		return nil
	case "quota_balance":
		s.persistCustomApiQuotaBalance(
			state.providerPrefix,
			event.Message,
			stringFromMetadata(event.Metadata, "detailedQuotaBalance"),
		)
		s.persistNugQuotaBalance(state.providerPrefix, event.Message)
		return nil
	case "nkp_quota_balance":
		s.persistNKPQuotaBalance(state.providerPrefix, event.Metadata)
		return nil
	case "block_complete":
		if event.Block == nil {
			return nil
		}
		if err := s.ensureAssistantDraft(ctx, state); err != nil {
			return err
		}
		block := contentBlockToMap(event.Block)
		if blockID, _ := block["id"].(string); block["type"] == "tool_use" && blockID != "" {
			state.hadToolUses = true
			upsertToolUseBlock(state, block)
		} else {
			state.blocks = append(state.blocks, block)
		}
		if block["type"] == "text" {
			state.assistantText = strings.TrimSpace(stringFromAny(block["text"]))
		}
		return s.updateAssistantDraft(ctx, state.narratorID, *state.draftMessageID, state.assistantText, state.blocks)
	case "tool_call":
		state.hadToolUses = true
		if err := s.ensureAssistantDraft(ctx, state); err != nil {
			return err
		}
		block := map[string]any{"type": "tool_use", "id": event.ToolUseID, "name": event.ToolName, "input": event.Input}
		upsertToolUseBlock(state, block)
		if err := s.updateAssistantDraft(ctx, state.narratorID, *state.draftMessageID, state.assistantText, state.blocks); err != nil {
			return err
		}
		if toolRequiresApproval(state.narrator.PermissionMode, event.ToolName) {
			return nil
		}
		return s.recordRunningToolCall(ctx, state.narratorID, *state.draftMessageID, state.narrator, event)
	case "tool_result":
		if err := s.ensureAssistantDraft(ctx, state); err != nil {
			return err
		}
		return s.finishToolCallFromEvent(ctx, state, event)
	case "assistant_message":
		if err := s.ensureAssistantDraft(ctx, state); err != nil {
			return err
		}
		if strings.TrimSpace(event.Text) != "" {
			state.assistantText = event.Text
		}
		if err := s.updateAssistantDraft(ctx, state.narratorID, *state.draftMessageID, state.assistantText, state.blocks); err != nil {
			return err
		}
		_ = s.updateAssistantMessageUsage(ctx, state, *state.draftMessageID, event)
		_ = s.linkPendingAPIRequestsToMessage(ctx, state, *state.draftMessageID)
		msg, err := s.messageByID(ctx, state.narratorID, *state.draftMessageID)
		if err != nil {
			return err
		}
		if state.narrator.IsBackground && state.narrator.ParentNarratorID != nil {
			if err := s.finishBackgroundTaskRecord(ctx, *state.narrator.ParentNarratorID, state.narratorID, "completed", state.assistantText); err != nil {
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
		return s.finishAgentRunError(ctx, state, state.errorMessage)
	default:
		return nil
	}
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
		Model:        firstNonEmptyString(strings.TrimSpace(event.Model), ptrString(state.narrator.Model)),
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
	model := firstNonEmptyString(strings.TrimSpace(event.Model), request.Model, ptrString(state.narrator.Model))
	credentialID := firstNonEmptyString(strings.TrimSpace(event.CredentialID), request.CredentialID)
	durationMs := event.DurationMs
	if durationMs == 0 && !request.StartedAt.IsZero() {
		durationMs = time.Since(request.StartedAt).Milliseconds()
	}
	usage := event.Usage
	messageID := ""
	if state.draftMessageID != nil {
		messageID = strings.TrimSpace(*state.draftMessageID)
	}
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
		"context_percent":             nullablePositiveFloat(event.Percentage),
		"meter_usage":                 nullablePositiveFloat(floatFromMetadata(event.Metadata, "meterUsage")),
		"meter_unit":                  nullableTrimmedString(stringFromMetadata(event.Metadata, "meterUnit")),
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
	model := firstNonEmptyString(ptrString(state.narrator.Model), event.Model)
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
		"turn_usage_json":             usageTurnJSON(usage),
	}
	sets := []string{}
	args := []any{}
	for _, column := range []string{"provider", "credential_id", "model", "tokens_in", "output_tokens", "cached_input_tokens", "cache_creation_input_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "reasoning_tokens", "cost_usd", "duration_ms", "turn_usage_json"} {
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
	if state.draftMessageID != nil && strings.TrimSpace(*state.draftMessageID) != "" {
		return nil
	}
	msg, err := s.addMessageWithBlocks(ctx, state.narratorID, "assistant", "", []map[string]any{}, nil)
	if err != nil {
		return err
	}
	if state.draftMessageID == nil {
		state.draftMessageID = &msg.ID
	} else {
		*state.draftMessageID = msg.ID
	}
	return nil
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
		if event.Type == "context_usage" && event.Percentage == 0 && event.Usage.ContextWindow > 0 {
			inputTokens := event.Usage.PromptTokens
			if inputTokens == 0 {
				inputTokens = event.Usage.InputTokens
			}
			payload["percentage"] = float64(inputTokens) / float64(event.Usage.ContextWindow) * 100
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

func (s *Service) persistNugQuotaBalance(providerPrefix, quotaBalance string) {
	providerPrefix = strings.TrimSpace(providerPrefix)
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
	persistQuotaCacheEntry("nug-quotas.json", providerID, map[string]any{"balance": balance})
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
		return map[string]any{"type": "tool_use", "id": block.ToolUseID, "name": block.ToolName, "input": block.Input}
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
		state.blocks = append(state.blocks, block)
		return
	}
	for i, existing := range state.blocks {
		if existing["type"] == "tool_use" && stringFromAny(existing["id"]) == idValue {
			state.blocks[i] = block
			return
		}
	}
	state.blocks = append(state.blocks, block)
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
	_, err := s.DB.ExecContext(ctx, `INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, status, execution_started_at, is_background, created_at) VALUES (?, ?, ?, ?, ?, ?, 'running', ?, ?, ?)`, id.New(), narratorID, messageID, event.ToolUseID, event.ToolName, string(inputJSON), now, isBackground, now)
	return err
}

func (s *Service) finishToolCallFromEvent(ctx context.Context, state *agentRunState, event agentbasic.AgentEvent) error {
	if event.ToolRun == nil {
		return nil
	}
	run := *event.ToolRun
	if run.Recorded {
		return s.persistToolResultSideCars(ctx, state.narratorID, *state.draftMessageID, event.ToolUseID, event.SideCars)
	}
	status := toolRunDBStatus(run.Status, event.IsError || strings.TrimSpace(run.Error) != "")
	outputJSON, _ := json.Marshal(map[string]any{"output": run.Output})
	inputJSON := toolRunArgumentsInputJSON(run.Arguments)
	now := db.NowISO()
	res, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = ?, input_json = COALESCE(?, input_json), output_json = ?, execution_started_at = COALESCE(execution_started_at, ?), completed_at = ?, error_message = ?, result_message_id = ? WHERE narrator_id = ? AND tool_use_id = ?`, status, inputJSON, string(outputJSON), now, now, nullableTrimmedString(run.Error), *state.draftMessageID, state.narratorID, run.ID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		if err := s.persistToolRun(ctx, state.narratorID, *state.draftMessageID, run); err != nil {
			return err
		}
		return s.persistToolResultSideCars(ctx, state.narratorID, *state.draftMessageID, event.ToolUseID, event.SideCars)
	}
	var toolCallID sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, state.narratorID, run.ID).Scan(&toolCallID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("query finished tool call: %w", err)
		}
	}
	if err := s.persistToolResultSideCars(ctx, state.narratorID, *state.draftMessageID, event.ToolUseID, event.SideCars); err != nil {
		return err
	}
	return s.persistFileSnapshot(ctx, state.narratorID, *state.draftMessageID, sqlNullString(toolCallID), run)
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
	return state.narrator
}

func (s *Service) backgroundRunParent(ctx context.Context, state *agentRunState, latest Narrator) (string, bool) {
	if latest.ParentNarratorID == nil || strings.TrimSpace(*latest.ParentNarratorID) == "" {
		if state.narrator.ParentNarratorID == nil || strings.TrimSpace(*state.narrator.ParentNarratorID) == "" {
			return "", latest.IsBackground
		}
		parentID := strings.TrimSpace(*state.narrator.ParentNarratorID)
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

func (s *Service) finishAgentRunSuccess(ctx context.Context, state *agentRunState) error {
	state.completed = true
	if state.draftMessageID != nil && strings.TrimSpace(*state.draftMessageID) != "" {
		msg, err := s.messageByID(ctx, state.narratorID, *state.draftMessageID)
		if err == nil {
			msg.ToolCalls, _ = s.loadToolCalls(ctx, msg.ID)
			state.lastMessage = &msg
		}
	}
	// TS treats goal usage accounting as best-effort; never fail a completed run
	// because active-goal counters could not be persisted.
	_ = s.accountGoalUsageForTurn(ctx, state)
	if state.narrator.ChapterID != nil {
		if isReview, reset, message := s.checkAndResetReviewGitState(ctx, *state.narrator.ChapterID); isReview {
			if reset && strings.TrimSpace(message) != "" {
				_, _ = s.addMessage(ctx, state.narratorID, "user", message, nil)
			} else if !reset {
				_ = s.concludeReviewChapter(ctx, *state.narrator.ChapterID)
			}
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
	}
	return nil
}

func (s *Service) finishAgentRunSilentDisconnect(ctx context.Context, state *agentRunState) error {
	state.completed = true
	state.errored = true
	message := "Provider stream disconnected before completion"
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
	}
	return nil
}

func (s *Service) finishAgentRunCancelled(ctx context.Context, state *agentRunState) error {
	state.completed = true
	state.errored = true
	message := "Cancelled"
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
	}
	return nil
}

func (s *Service) finishAgentRunError(ctx context.Context, state *agentRunState, message string) error {
	state.completed = true
	state.errored = true
	if strings.TrimSpace(message) == "" {
		message = "agent run failed"
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
		s.broadcastNarratorError(state.narratorID, message, errorCode)
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
