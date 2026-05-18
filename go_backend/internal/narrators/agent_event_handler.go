package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/db"
	"narrafork-go/internal/id"
)

type agentRunState struct {
	narratorID     string
	narrator       Narrator
	draftMessageID *string
	blocks         []map[string]any
	assistantText  string
	lastMessage    *Message
	completed      bool
	errored        bool
	errorMessage   string
	streamedBytes  int
}

func (s *Service) handleAgentEvent(ctx context.Context, state *agentRunState, event agentbasic.AgentEvent) error {
	s.broadcastAgentEvent(state.narratorID, event)
	if state.narrator.IsBackground && state.narrator.ParentNarratorID != nil && (event.Type == "stream_text" || event.Type == "stream_reasoning") && event.Text != "" {
		state.streamedBytes += len([]byte(event.Text))
		s.broadcastBackgroundTaskOutput(*state.narrator.ParentNarratorID, state.narratorID, state.streamedBytes)
	}
	switch event.Type {
	case "stream_text", "stream_reasoning", "tool_use_chunk", "context_usage", "retrying", "api_request_start", "api_request_end", "web_search", "image_generation", "output_truncated", "model_switched", "turn_complete", "tool_inventory":
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
			upsertToolUseBlock(state, block)
		} else {
			state.blocks = append(state.blocks, block)
		}
		if block["type"] == "text" {
			state.assistantText = strings.TrimSpace(stringFromAny(block["text"]))
		}
		return s.updateAssistantDraft(ctx, state.narratorID, *state.draftMessageID, state.assistantText, state.blocks)
	case "tool_call":
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
	case "error", "retryable_error", "invalid_state":
		state.errored = true
		state.errorMessage = firstNonEmptyString(event.Message, event.Reason, "agent run failed")
		if isCancelledError(errors.New(state.errorMessage)) {
			return s.finishAgentRunCancelled(ctx, state)
		}
		return s.finishAgentRunError(ctx, state, state.errorMessage)
	default:
		return nil
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
	}
	if event.Percentage != 0 {
		payload["percentage"] = event.Percentage
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
	s.Hub.BroadcastNarrator(narratorID, payload)
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
		return nil
	}
	status := toolRunDBStatus(run.Status, event.IsError || strings.TrimSpace(run.Error) != "")
	outputJSON, _ := json.Marshal(map[string]any{"output": run.Output})
	now := db.NowISO()
	res, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = ?, output_json = ?, execution_started_at = COALESCE(execution_started_at, ?), completed_at = ?, error_message = ?, result_message_id = ? WHERE narrator_id = ? AND tool_use_id = ?`, status, string(outputJSON), now, now, nullableTrimmedString(run.Error), *state.draftMessageID, state.narratorID, run.ID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		if err := s.persistToolRun(ctx, state.narratorID, *state.draftMessageID, run); err != nil {
			return err
		}
		return nil
	}
	var toolCallID sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, state.narratorID, run.ID).Scan(&toolCallID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("query finished tool call: %w", err)
		}
	}
	return s.persistFileSnapshot(ctx, state.narratorID, *state.draftMessageID, sqlNullString(toolCallID), run)
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
	if state.narrator.ChapterID != nil {
		if isReview, reset, message := s.checkAndResetReviewGitState(ctx, *state.narrator.ChapterID); isReview {
			if reset && strings.TrimSpace(message) != "" {
				_, _ = s.addMessage(ctx, state.narratorID, "user", message, nil)
			} else if !reset {
				_ = s.concludeReviewChapter(ctx, *state.narrator.ChapterID)
			}
		}
	}
	var err error
	if state.narrator.IsBackground {
		result := state.assistantText
		if strings.TrimSpace(result) == "" {
			result = "Completed"
		}
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '[]', background_status = 'completed', background_result = ?, background_completed_at = ?, error_message = NULL, updated_at = ? WHERE id = ?`, result, db.NowISO(), db.NowISO(), state.narratorID)
	} else if state.narrator.Type == "subagent" || state.narrator.Variant == "subagent" {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["manual_override"]', error_message = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), state.narratorID)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["unread"]', error_message = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), state.narratorID)
	}
	if err != nil {
		return err
	}
	if state.narrator.IsBackground && state.narrator.ParentNarratorID != nil {
		result := state.assistantText
		if strings.TrimSpace(result) == "" {
			result = "Completed"
		}
		if err := s.finishBackgroundTaskRecord(ctx, *state.narrator.ParentNarratorID, state.narratorID, "completed", result); err != nil {
			return err
		}
	}
	if s.Hub != nil {
		substatus := []string{}
		if !state.narrator.IsBackground && (state.narrator.Type == "subagent" || state.narrator.Variant == "subagent") {
			substatus = []string{"manual_override"}
		} else if !state.narrator.IsBackground {
			substatus = []string{"unread"}
		}
		backgroundStatus := nullableTrimmedString("")
		if state.narrator.IsBackground {
			backgroundStatus = nullableTrimmedString("completed")
		}
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "status", "narratorId": state.narratorID, "status": "idle", "substatus": substatus, "isBackground": state.narrator.IsBackground, "backgroundStatus": backgroundStatus})
	}
	if !state.narrator.IsBackground && state.narrator.Type != "subagent" && state.narrator.Variant != "subagent" {
		_, _ = s.consumeNextBufferedMessage(context.Background(), state.narratorID)
	}
	return nil
}

func (s *Service) finishAgentRunCancelled(ctx context.Context, state *agentRunState) error {
	state.completed = true
	state.errored = true
	message := "Cancelled"
	var err error
	if state.narrator.IsBackground {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '[]', background_status = 'cancelled', background_result = ?, background_completed_at = ?, error_message = NULL, updated_at = ? WHERE id = ?`, message, db.NowISO(), db.NowISO(), state.narratorID)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '[]', error_message = NULL, updated_at = ? WHERE id = ?`, db.NowISO(), state.narratorID)
	}
	if err != nil {
		return err
	}
	if state.narrator.IsBackground && state.narrator.ParentNarratorID != nil {
		if err := s.finishBackgroundTaskRecord(ctx, *state.narrator.ParentNarratorID, state.narratorID, "cancelled", message); err != nil {
			return err
		}
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "status", "narratorId": state.narratorID, "status": "idle", "substatus": []string{}, "isBackground": state.narrator.IsBackground, "backgroundStatus": nullableTrimmedString("cancelled")})
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
	var err error
	if state.narrator.IsBackground {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["error"]', background_status = 'failed', background_result = ?, background_completed_at = ?, error_message = ?, updated_at = ? WHERE id = ?`, message, db.NowISO(), message, db.NowISO(), state.narratorID)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["error"]', error_message = ?, updated_at = ? WHERE id = ?`, message, db.NowISO(), state.narratorID)
	}
	if err != nil {
		return err
	}
	if state.narrator.IsBackground && state.narrator.ParentNarratorID != nil {
		if err := s.finishBackgroundTaskRecord(ctx, *state.narrator.ParentNarratorID, state.narratorID, "failed", message); err != nil {
			return err
		}
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(state.narratorID, map[string]any{"type": "status", "narratorId": state.narratorID, "status": "idle", "substatus": []string{"error"}, "isBackground": state.narrator.IsBackground, "backgroundStatus": nullableTrimmedString("failed")})
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
