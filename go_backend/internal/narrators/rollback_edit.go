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
	"strings"

	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type deleteMessagesAfterResult struct {
	DeletedCount      int64
	DeletedMessageIDs []string
	AffectedFiles     []string
}

type deleteMessageBlockResult struct {
	MessageDeleted   bool
	MessageID        string
	PrivateMessageID string
	AffectedFiles    []string
}

type copyOnWriteOverrides struct {
	ContentText *string
	ContentJSON any
	// EditedAt/EditedBy/OriginalContentJSON support assistant message text edits
	// that persist without regenerating (migration 0042). A non-nil pointer means "set this column"; the inner
	// pointer/value may itself be nil to write SQL NULL.
	EditedAt            *string
	EditedBy            **string
	OriginalContentJSON *any
	SetEditMetadata     bool
}

type editAndRegenerateResult struct {
	Message           Message
	MessageID         string
	PrivateMessageID  string
	DeletedMessageIDs []string
	AffectedFiles     []string
}

type storedMessageForMutation struct {
	Message
	RawContentJSON string
	// OriginalContentRaw is the raw JSON of original_content_json, preserved so
	// copy-on-write re-inserts can carry the first-edit snapshot forward verbatim.
	OriginalContentRaw *string
}

type optionalNarratorTables struct {
	NarratorSidecars bool
	NarratorPatches  bool
	APIRequests      bool
	ChapterCommits   bool
}

type rollbackToolUseResult struct {
	AffectedFiles []string
}

type rollbackFilePlan struct {
	States        map[string]*string
	AffectedFiles []string
}

type rollbackFileBackup struct {
	FilePath string
	AbsPath  string
	Existed  bool
	Content  []byte
	Mode     os.FileMode
}

func (s *Service) RollbackToBlock(ctx context.Context, narratorID, messageID string, blockIndex int, revert bool) (map[string]any, error) {
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	if err := activeNarratorMutationError(narrator, "rollback"); err != nil {
		return nil, err
	}
	message, err := s.messageForNarratorRef(ctx, narratorID, messageID)
	if err != nil {
		return nil, err
	}
	blocks := messageContentBlocks(message.ContentJSON)
	if blockIndex < 0 || blockIndex >= len(blocks) {
		return nil, httpx.BadRequest(fmt.Sprintf("block index %d out of range", blockIndex))
	}

	deleted, err := s.deleteMessagesAfter(ctx, narratorID, messageID, true, revert)
	if err != nil {
		return nil, err
	}
	if s.Hub != nil && len(deleted.DeletedMessageIDs) > 0 {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "messages_deleted", "narratorId": narratorID, "deletedMessageIds": deleted.DeletedMessageIDs})
	}

	var blockResult *deleteMessageBlockResult
	for idx := len(blocks) - 1; idx > blockIndex; idx-- {
		result, err := s.deleteMessageBlock(ctx, narratorID, messageID, idx, true, revert)
		if err != nil {
			return nil, err
		}
		blockResult = &result
		deleted.AffectedFiles = append(deleted.AffectedFiles, result.AffectedFiles...)
		if result.MessageDeleted {
			break
		}
		if result.PrivateMessageID != "" && result.PrivateMessageID != messageID {
			messageID = result.PrivateMessageID
		}
	}
	deleted.AffectedFiles = uniqueSortedStrings(deleted.AffectedFiles)

	payload := map[string]any{
		"ok":                true,
		"deletedCount":      deleted.DeletedCount,
		"deletedMessageIds": deleted.DeletedMessageIDs,
		"affectedFiles":     deleted.AffectedFiles,
		"reverted":          revert,
	}
	if blockResult != nil {
		payload["messageDeleted"] = blockResult.MessageDeleted
		payload["messageId"] = blockResult.MessageID
		if blockResult.PrivateMessageID != "" {
			payload["privateMessageId"] = blockResult.PrivateMessageID
		}
		if blockResult.MessageDeleted {
			if s.Hub != nil {
				s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "messages_deleted", "narratorId": narratorID, "deletedMessageIds": []string{blockResult.MessageID}})
			}
		} else if updated, err := s.messageForNarratorRef(ctx, narratorID, blockResult.PrivateMessageID); err == nil {
			payload["message"] = updated.Message
			s.broadcastMessageUpdated(ctx, narratorID, updated.Message)
			if blockResult.PrivateMessageID != blockResult.MessageID && s.Hub != nil {
				s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "full_reload", "narratorId": narratorID})
			}
		}
	}
	return payload, nil
}

func (s *Service) EditAndRegenerate(ctx context.Context, narratorID, messageID, newContent string, rollback, regenerate bool) (editAndRegenerateResult, error) {
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return editAndRegenerateResult{}, err
	}
	if err := activeNarratorMutationError(narrator, "edit message"); err != nil {
		return editAndRegenerateResult{}, err
	}
	message, err := s.messageForNarratorRef(ctx, narratorID, messageID)
	if err != nil {
		return editAndRegenerateResult{}, err
	}
	if message.Role != "user" {
		return editAndRegenerateResult{}, httpx.BadRequest("can only edit user messages")
	}

	newBlocks := buildEditedUserContentJSON(message.ContentJSON, newContent, message.NarratorID)
	privateMessageID, err := s.copyOnWriteMessage(ctx, narratorID, messageID, copyOnWriteOverrides{ContentText: &newContent, ContentJSON: newBlocks})
	if err != nil {
		return editAndRegenerateResult{}, err
	}
	updated, err := s.messageForNarratorRef(ctx, narratorID, privateMessageID)
	if err != nil {
		return editAndRegenerateResult{}, err
	}
	s.broadcastMessageUpdated(ctx, narratorID, updated.Message)
	if privateMessageID != messageID && s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "full_reload", "narratorId": narratorID})
	}

	deleted, err := s.deleteMessagesAfter(ctx, narratorID, privateMessageID, true, rollback)
	if err != nil {
		return editAndRegenerateResult{}, err
	}
	if s.Hub != nil && len(deleted.DeletedMessageIDs) > 0 {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "messages_deleted", "narratorId": narratorID, "deletedMessageIds": deleted.DeletedMessageIDs})
	}
	if regenerate {
		if err := s.startNarratorRun(ctx, narratorID, narrator); err != nil {
			return editAndRegenerateResult{}, err
		}
	}
	return editAndRegenerateResult{Message: updated.Message, MessageID: messageID, PrivateMessageID: privateMessageID, DeletedMessageIDs: deleted.DeletedMessageIDs, AffectedFiles: deleted.AffectedFiles}, nil
}

func (s *Service) deleteMessagesAfter(ctx context.Context, narratorID, messageID string, preserveConversationID, revert bool) (deleteMessagesAfterResult, error) {
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return deleteMessagesAfterResult{}, err
	}
	var targetSeq int
	if err := s.DB.QueryRowContext(ctx, `SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, messageID).Scan(&targetSeq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return deleteMessagesAfterResult{}, httpx.NotFound("Message")
		}
		return deleteMessagesAfterResult{}, err
	}

	rows, err := s.DB.QueryContext(ctx, `SELECT id, message_id FROM narrator_message_refs WHERE narrator_id = ? AND seq > ? ORDER BY seq ASC`, narratorID, targetSeq)
	if err != nil {
		return deleteMessagesAfterResult{}, err
	}
	refIDs := []string{}
	messageIDs := []string{}
	for rows.Next() {
		var refID, removedMessageID string
		if err := rows.Scan(&refID, &removedMessageID); err != nil {
			_ = rows.Close()
			return deleteMessagesAfterResult{}, err
		}
		refIDs = append(refIDs, refID)
		messageIDs = append(messageIDs, removedMessageID)
	}
	if err := rows.Close(); err != nil {
		return deleteMessagesAfterResult{}, err
	}
	messageIDs = uniqueStrings(messageIDs)
	if len(refIDs) == 0 {
		return deleteMessagesAfterResult{DeletedMessageIDs: []string{}, AffectedFiles: []string{}}, nil
	}

	var filePlan rollbackFilePlan
	if revert {
		var err error
		filePlan, err = s.planRebuildFilesExcludingMessages(ctx, narratorID, narrator, messageIDs)
		if err != nil {
			return deleteMessagesAfterResult{}, err
		}
	}

	tables, err := narratorOptionalTables(ctx, s.DB)
	if err != nil {
		return deleteMessagesAfterResult{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return deleteMessagesAfterResult{}, err
	}
	defer tx.Rollback()
	placeholders, refArgs := inPlaceholders(refIDs)
	if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_message_refs WHERE id IN (`+placeholders+`)`, refArgs...); err != nil {
		return deleteMessagesAfterResult{}, err
	}

	orphanIDs, err := s.orphanMessageIDs(ctx, tx, messageIDs, true)
	if err != nil {
		return deleteMessagesAfterResult{}, err
	}
	if len(orphanIDs) > 0 {
		if err := s.deleteOrphanedMessages(ctx, tx, orphanIDs, tables); err != nil {
			return deleteMessagesAfterResult{}, err
		}
	}
	if err := updateNarratorMessageStatsTx(ctx, tx, narratorID, preserveConversationID); err != nil {
		return deleteMessagesAfterResult{}, err
	}
	var backups []rollbackFileBackup
	affectedFiles := []string{}
	if revert {
		var err error
		backups, affectedFiles, err = applyRollbackFilePlan(narratorWorkingDir(ctx, s.DB, narrator), filePlan)
		if err != nil {
			return deleteMessagesAfterResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		if len(backups) > 0 {
			if restoreErr := restoreRollbackFileBackups(backups); restoreErr != nil {
				return deleteMessagesAfterResult{}, fmt.Errorf("commit rollback mutation: %w; restore files: %v", err, restoreErr)
			}
		}
		return deleteMessagesAfterResult{}, err
	}
	return deleteMessagesAfterResult{DeletedCount: int64(len(refIDs)), DeletedMessageIDs: messageIDs, AffectedFiles: affectedFiles}, nil
}

func (s *Service) deleteMessageBlock(ctx context.Context, narratorID, messageID string, blockIndex int, preserveConversationID, revert bool) (deleteMessageBlockResult, error) {
	message, err := s.messageForNarratorRef(ctx, narratorID, messageID)
	if err != nil {
		return deleteMessageBlockResult{}, err
	}
	blocks := messageContentBlocks(message.ContentJSON)
	if blockIndex < 0 || blockIndex >= len(blocks) {
		return deleteMessageBlockResult{}, httpx.BadRequest("block index out of range")
	}
	removedBlock := blocks[blockIndex]
	remaining := append([]map[string]any{}, blocks[:blockIndex]...)
	remaining = append(remaining, blocks[blockIndex+1:]...)
	removedToolUseIDs := toolUseIDsFromBlocks([]map[string]any{removedBlock})
	var narratorForRevert Narrator
	var filePlan rollbackFilePlan
	if revert && len(removedToolUseIDs) > 0 {
		narrator, err := s.Get(ctx, narratorID)
		if err != nil {
			return deleteMessageBlockResult{}, err
		}
		narratorForRevert = narrator
		filePlan, err = s.planRebuildFilesExcludingToolUses(ctx, narratorID, narrator, stringSet(removedToolUseIDs))
		if err != nil {
			return deleteMessageBlockResult{}, err
		}
	}

	isShared, err := s.messageSharedByMultipleNarrators(ctx, messageID)
	if err != nil {
		return deleteMessageBlockResult{}, err
	}
	tables, err := narratorOptionalTables(ctx, s.DB)
	if err != nil {
		return deleteMessageBlockResult{}, err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return deleteMessageBlockResult{}, err
	}
	defer tx.Rollback()
	result := deleteMessageBlockResult{MessageID: messageID, PrivateMessageID: messageID, AffectedFiles: []string{}}
	if len(remaining) == 0 {
		result.MessageDeleted = true
		if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, messageID); err != nil {
			return deleteMessageBlockResult{}, err
		}
		if err := s.cleanToolUseBlocks(ctx, tx, narratorID, messageID, []map[string]any{removedBlock}, tables); err != nil {
			return deleteMessageBlockResult{}, err
		}
		orphanIDs, err := s.orphanMessageIDs(ctx, tx, []string{messageID}, false)
		if err != nil {
			return deleteMessageBlockResult{}, err
		}
		if err := s.deleteOrphanedMessages(ctx, tx, orphanIDs, tables); err != nil {
			return deleteMessageBlockResult{}, err
		}
	} else if isShared {
		newID, err := s.copyMessageForNarratorTx(ctx, tx, narratorID, message, copyOnWriteOverrides{ContentText: stringPtr(contentTextFromMapBlocks(remaining)), ContentJSON: remaining})
		if err != nil {
			return deleteMessageBlockResult{}, err
		}
		result.PrivateMessageID = newID
		if err := s.cleanToolUseBlocks(ctx, tx, narratorID, newID, []map[string]any{removedBlock}, tables); err != nil {
			return deleteMessageBlockResult{}, err
		}
	} else {
		contentText := contentTextFromMapBlocks(remaining)
		data, _ := json.Marshal(remaining)
		if _, err := tx.ExecContext(ctx, `UPDATE narrator_messages SET content_json = ?, content_text = ? WHERE id = ?`, string(data), nullableTrimmedString(contentText), messageID); err != nil {
			return deleteMessageBlockResult{}, err
		}
		if err := s.cleanToolUseBlocks(ctx, tx, narratorID, messageID, []map[string]any{removedBlock}, tables); err != nil {
			return deleteMessageBlockResult{}, err
		}
	}
	if err := updateNarratorMessageStatsTx(ctx, tx, narratorID, preserveConversationID); err != nil {
		return deleteMessageBlockResult{}, err
	}
	var backups []rollbackFileBackup
	if revert && len(removedToolUseIDs) > 0 {
		var affected []string
		var err error
		backups, affected, err = applyRollbackFilePlan(narratorWorkingDir(ctx, s.DB, narratorForRevert), filePlan)
		if err != nil {
			return deleteMessageBlockResult{}, err
		}
		result.AffectedFiles = affected
	}
	if err := tx.Commit(); err != nil {
		if len(backups) > 0 {
			if restoreErr := restoreRollbackFileBackups(backups); restoreErr != nil {
				return deleteMessageBlockResult{}, fmt.Errorf("commit rollback mutation: %w; restore files: %v", err, restoreErr)
			}
		}
		return deleteMessageBlockResult{}, err
	}
	return result, nil
}

func (s *Service) copyOnWriteMessage(ctx context.Context, narratorID, messageID string, overrides copyOnWriteOverrides) (string, error) {
	message, err := s.messageForNarratorRef(ctx, narratorID, messageID)
	if err != nil {
		return "", err
	}
	isShared, err := s.messageSharedByMultipleNarrators(ctx, messageID)
	if err != nil {
		return "", err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	privateID := messageID
	if isShared {
		privateID, err = s.copyMessageForNarratorTx(ctx, tx, narratorID, message, overrides)
		if err != nil {
			return "", err
		}
	} else {
		contentJSON := message.ContentJSON
		if overrides.ContentJSON != nil {
			contentJSON = overrides.ContentJSON
		}
		data, _ := json.Marshal(contentJSON)
		contentText := message.ContentText
		if overrides.ContentText != nil {
			contentText = overrides.ContentText
		}
		setClauses := []string{"content_json = ?", "content_text = ?"}
		args := []any{string(data), nullableStringPtr(contentText)}
		if overrides.SetEditMetadata {
			setClauses = append(setClauses, "edited_at = ?", "edited_by = ?")
			args = append(args, nullableStringPtr(overrides.EditedAt))
			args = append(args, editedByArg(overrides.EditedBy))
			if overrides.OriginalContentJSON != nil {
				setClauses = append(setClauses, "original_content_json = ?")
				args = append(args, originalContentJSONArg(*overrides.OriginalContentJSON))
			}
		}
		args = append(args, messageID)
		if _, err := tx.ExecContext(ctx, `UPDATE narrator_messages SET `+strings.Join(setClauses, ", ")+` WHERE id = ?`, args...); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE narrators SET message_version = message_version + 1, updated_at = ? WHERE id = ?`, db.NowISO(), narratorID); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return privateID, nil
}

func (s *Service) copyMessageForNarratorTx(ctx context.Context, tx *sql.Tx, narratorID string, message storedMessageForMutation, overrides copyOnWriteOverrides) (string, error) {
	newID := id.New()
	contentJSON := message.ContentJSON
	if overrides.ContentJSON != nil {
		contentJSON = overrides.ContentJSON
	}
	contentData, _ := json.Marshal(contentJSON)
	contentText := message.ContentText
	if overrides.ContentText != nil {
		contentText = overrides.ContentText
	}
	var editedAtArg, editedByVal, originalContentArg any
	if overrides.SetEditMetadata {
		editedAtArg = nullableStringPtr(overrides.EditedAt)
		editedByVal = editedByArg(overrides.EditedBy)
		if overrides.OriginalContentJSON != nil {
			originalContentArg = originalContentJSONArg(*overrides.OriginalContentJSON)
		} else {
			// Preserve the existing original content when the caller does not
			// explicitly override it (e.g. re-editing an already-edited message).
			originalContentArg = nullableStringPtr(message.OriginalContentRaw)
		}
	} else {
		editedAtArg = nullableStringPtr(message.EditedAt)
		editedByVal = nullableStringPtr(message.EditedBy)
		originalContentArg = nullableStringPtr(message.OriginalContentRaw)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_messages (id, narrator_id, sdk_message_uuid, parent_tool_use_id, role, content_json, content_text, model, commit_sha, command_text, created_by, edited_at, edited_by, original_content_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, newID, narratorID, nullableStringPtr(message.MessageUUID), nullableStringPtr(message.ParentToolUseID), message.Role, string(contentData), nullableStringPtr(contentText), nullableStringPtr(message.Model), nullableStringPtr(message.CommitSHA), nullableStringPtr(message.CommandText), nullableStringPtr(message.CreatedBy), editedAtArg, editedByVal, originalContentArg, message.CreatedAt); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE narrator_message_refs SET message_id = ? WHERE narrator_id = ? AND message_id = ?`, newID, narratorID, message.ID); err != nil {
		return "", err
	}
	if err := s.copyToolCallsForMessageTx(ctx, tx, narratorID, message.ID, newID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE narrators SET fork_message_id = CASE WHEN fork_message_id = ? THEN ? ELSE fork_message_id END, prune_boundary_message_id = CASE WHEN prune_boundary_message_id = ? THEN ? ELSE prune_boundary_message_id END, message_version = message_version + 1, updated_at = ? WHERE id = ?`, message.ID, newID, message.ID, newID, db.NowISO(), narratorID); err != nil {
		return "", err
	}
	return newID, nil
}

func (s *Service) copyToolCallsForMessageTx(ctx context.Context, tx *sql.Tx, narratorID, oldMessageID, newMessageID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT tool_use_id, tool_name, input_json, output_json, status, permission_started_at, permission_suggestions, is_background, created_at, execution_started_at, completed_at, result_message_id, error_message, permission_decided_by, permission_decided_at, permission_deny_message, permission_decision_reason FROM narrator_tool_calls WHERE message_id = ?`, oldMessageID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var toolUseID, toolName, status string
		var inputJSON, outputJSON, permissionStartedAt, permissionSuggestions, createdAt, executionStartedAt, completedAt, resultMessageID, errorMessage, decidedBy, decidedAt, denyMessage, decisionReason sql.NullString
		var isBackground sql.NullInt64
		if err := rows.Scan(&toolUseID, &toolName, &inputJSON, &outputJSON, &status, &permissionStartedAt, &permissionSuggestions, &isBackground, &createdAt, &executionStartedAt, &completedAt, &resultMessageID, &errorMessage, &decidedBy, &decidedAt, &denyMessage, &decisionReason); err != nil {
			return err
		}
		resultMessage := nullStringPtr(resultMessageID)
		if resultMessage != nil && *resultMessage == oldMessageID {
			resultMessage = &newMessageID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, permission_started_at, permission_suggestions, is_background, created_at, execution_started_at, completed_at, result_message_id, error_message, permission_decided_by, permission_decided_at, permission_deny_message, permission_decision_reason) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id.New(), narratorID, newMessageID, toolUseID, toolName, nullStringAny(inputJSON), nullStringAny(outputJSON), status, nullStringAny(permissionStartedAt), nullStringAny(permissionSuggestions), nullIntAny(isBackground), nullStringAny(createdAt), nullStringAny(executionStartedAt), nullStringAny(completedAt), nullableStringPtr(resultMessage), nullStringAny(errorMessage), nullStringAny(decidedBy), nullStringAny(decidedAt), nullStringAny(denyMessage), nullStringAny(decisionReason)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Service) messageForNarratorRef(ctx context.Context, narratorID, messageID string) (storedMessageForMutation, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT m.id, m.narrator_id, m.sdk_message_uuid, m.parent_tool_use_id, m.role, COALESCE(m.content_json, '[]'), m.content_text, m.model, m.commit_sha, m.command_text, m.created_by, m.edited_at, m.edited_by, m.original_content_json, m.created_at, r.seq FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND r.message_id = ?`, narratorID, messageID)
	msg, err := scanMessage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storedMessageForMutation{}, httpx.NotFound("Message")
		}
		return storedMessageForMutation{}, err
	}
	calls, err := s.loadToolCalls(ctx, msg.ID)
	if err != nil {
		return storedMessageForMutation{}, err
	}
	msg.ToolCalls = calls
	raw, _ := json.Marshal(msg.ContentJSON)
	stored := storedMessageForMutation{Message: msg, RawContentJSON: string(raw)}
	if msg.OriginalContentJSON != nil {
		originalRaw, _ := json.Marshal(msg.OriginalContentJSON)
		text := string(originalRaw)
		stored.OriginalContentRaw = &text
	}
	return stored, nil
}

func (s *Service) messageSharedByMultipleNarrators(ctx context.Context, messageID string) (bool, error) {
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_message_refs WHERE message_id = ?`, messageID).Scan(&count); err != nil {
		return false, err
	}
	return count > 1, nil
}

func (s *Service) planRebuildFilesExcludingMessages(ctx context.Context, narratorID string, narrator Narrator, messageIDs []string) (rollbackFilePlan, error) {
	if len(messageIDs) == 0 {
		return rollbackFilePlan{AffectedFiles: []string{}}, nil
	}
	messageSet := stringSet(messageIDs)
	cwd := narratorWorkingDir(ctx, s.DB, narrator)
	calls, err := queryOrderedFileToolCalls(ctx, s.DB, narratorID, cwd, nil)
	if err != nil {
		return rollbackFilePlan{}, err
	}
	exclude := map[string]struct{}{}
	for _, call := range calls {
		if _, ok := messageSet[call.MessageID]; ok {
			exclude[call.ToolUseID] = struct{}{}
		}
	}
	return s.planRebuildFilesExcludingToolUses(ctx, narratorID, narrator, exclude)
}

func (s *Service) planRebuildFilesExcludingToolUses(ctx context.Context, narratorID string, narrator Narrator, exclude map[string]struct{}) (rollbackFilePlan, error) {
	if len(exclude) == 0 {
		return rollbackFilePlan{AffectedFiles: []string{}}, nil
	}
	exists, err := sqlTableExists(ctx, s.DB, "narrator_file_snapshots")
	if err != nil || !exists {
		return rollbackFilePlan{AffectedFiles: []string{}}, err
	}
	cwd := narratorWorkingDir(ctx, s.DB, narrator)
	calls, err := queryOrderedFileToolCalls(ctx, s.DB, narratorID, cwd, nil)
	if err != nil {
		return rollbackFilePlan{}, err
	}
	snapshots, err := querySnapshotOriginals(ctx, s.DB, narratorID)
	if err != nil {
		return rollbackFilePlan{}, err
	}
	affectedSet := map[string]struct{}{}
	for _, call := range calls {
		if _, ok := exclude[call.ToolUseID]; !ok {
			continue
		}
		key := canonicalToolCallFilePath(call, snapshots)
		if key != "" {
			affectedSet[key] = struct{}{}
		}
	}
	affected := sortedMapKeys(affectedSet)
	if len(affected) == 0 {
		return rollbackFilePlan{AffectedFiles: []string{}}, nil
	}
	states, err := rebuildFileStatesExcluding(ctx, s.DB, narratorID, cwd, affected, exclude, nil)
	if err != nil {
		return rollbackFilePlan{}, err
	}
	return rollbackFilePlan{States: states, AffectedFiles: affected}, nil
}

func (s *Service) rebuildFilesExcludingToolUses(ctx context.Context, narratorID string, narrator Narrator, exclude map[string]struct{}) (rollbackToolUseResult, error) {
	plan, err := s.planRebuildFilesExcludingToolUses(ctx, narratorID, narrator, exclude)
	if err != nil {
		return rollbackToolUseResult{}, err
	}
	backups, files, err := applyRollbackFilePlan(narratorWorkingDir(ctx, s.DB, narrator), plan)
	if err != nil {
		return rollbackToolUseResult{}, err
	}
	_ = backups
	return rollbackToolUseResult{AffectedFiles: files}, nil
}

func applyRollbackFilePlan(cwd string, plan rollbackFilePlan) ([]rollbackFileBackup, []string, error) {
	if len(plan.States) == 0 {
		return nil, []string{}, nil
	}
	backups, err := backupRollbackFiles(cwd, plan.States)
	if err != nil {
		return nil, nil, err
	}
	files, err := writeRebuiltFileStates(cwd, plan.States, false)
	if err != nil {
		if restoreErr := restoreRollbackFileBackups(backups); restoreErr != nil {
			return nil, files, fmt.Errorf("apply rollback file plan: %w; restore files: %v", err, restoreErr)
		}
		return nil, files, err
	}
	return backups, uniqueSortedStrings(files), nil
}

func backupRollbackFiles(cwd string, states map[string]*string) ([]rollbackFileBackup, error) {
	keys := make([]string, 0, len(states))
	for filePath := range states {
		keys = append(keys, filePath)
	}
	sort.Strings(keys)
	backups := make([]rollbackFileBackup, 0, len(keys))
	for _, filePath := range keys {
		absPath := resolveSnapshotFilePath(cwd, filePath)
		backup := rollbackFileBackup{FilePath: filePath, AbsPath: absPath}
		info, err := os.Stat(absPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				backups = append(backups, backup)
				continue
			}
			return nil, err
		}
		if info.IsDir() {
			return nil, fmt.Errorf("cannot rollback file %s: path is a directory", filePath)
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil, err
		}
		backup.Existed = true
		backup.Content = data
		backup.Mode = info.Mode().Perm()
		backups = append(backups, backup)
	}
	return backups, nil
}

func restoreRollbackFileBackups(backups []rollbackFileBackup) error {
	var errs []error
	for i := len(backups) - 1; i >= 0; i-- {
		backup := backups[i]
		if !backup.Existed {
			if err := os.Remove(backup.AbsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, fmt.Errorf("remove %s: %w", backup.FilePath, err))
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(backup.AbsPath), 0o755); err != nil {
			errs = append(errs, fmt.Errorf("create parent for %s: %w", backup.FilePath, err))
			continue
		}
		mode := backup.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(backup.AbsPath, backup.Content, mode); err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", backup.FilePath, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Service) orphanMessageIDs(ctx context.Context, tx *sql.Tx, candidateIDs []string, includeChildToolSubtree bool) ([]string, error) {
	candidateIDs = uniqueStrings(candidateIDs)
	if len(candidateIDs) == 0 {
		return []string{}, nil
	}
	placeholders, args := inPlaceholders(candidateIDs)
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(content_json, '[]') FROM narrator_messages WHERE id IN (`+placeholders+`) AND NOT EXISTS (SELECT 1 FROM narrator_message_refs WHERE message_id = narrator_messages.id)`, args...)
	if err != nil {
		return nil, err
	}
	orphanIDs := []string{}
	toolUseIDs := []string{}
	for rows.Next() {
		var idValue, raw string
		if err := rows.Scan(&idValue, &raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		orphanIDs = append(orphanIDs, idValue)
		toolUseIDs = append(toolUseIDs, toolUseIDsFromRawContent(raw)...)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if includeChildToolSubtree {
		childIDs, err := s.childMessageIDsForToolUses(ctx, tx, toolUseIDs, 1000)
		if err != nil {
			return nil, err
		}
		orphanIDs = append(orphanIDs, childIDs...)
	}
	return uniqueStrings(orphanIDs), nil
}

func (s *Service) childMessageIDsForToolUses(ctx context.Context, tx *sql.Tx, toolUseIDs []string, limit int) ([]string, error) {
	frontier := uniqueStrings(toolUseIDs)
	seenToolUses := map[string]struct{}{}
	seenMessages := map[string]struct{}{}
	children := []string{}
	for len(frontier) > 0 && len(children) < limit {
		current := []string{}
		for _, toolUseID := range frontier {
			if _, ok := seenToolUses[toolUseID]; ok || strings.TrimSpace(toolUseID) == "" {
				continue
			}
			seenToolUses[toolUseID] = struct{}{}
			current = append(current, toolUseID)
		}
		if len(current) == 0 {
			break
		}
		placeholders, args := inPlaceholders(current)
		rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(content_json, '[]') FROM narrator_messages WHERE parent_tool_use_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		nextToolUses := []string{}
		for rows.Next() {
			var messageID, raw string
			if err := rows.Scan(&messageID, &raw); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if _, ok := seenMessages[messageID]; !ok {
				seenMessages[messageID] = struct{}{}
				children = append(children, messageID)
			}
			nextToolUses = append(nextToolUses, toolUseIDsFromRawContent(raw)...)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		frontier = nextToolUses
	}
	return uniqueStrings(children), nil
}

func (s *Service) deleteOrphanedMessages(ctx context.Context, tx *sql.Tx, orphanIDs []string, tables optionalNarratorTables) error {
	orphanIDs = uniqueStrings(orphanIDs)
	if len(orphanIDs) == 0 {
		return nil
	}
	placeholders, args := inPlaceholders(orphanIDs)
	if _, err := tx.ExecContext(ctx, `UPDATE narrators SET fork_message_id = NULL WHERE fork_message_id IN (`+placeholders+`)`, args...); err != nil {
		return err
	}
	placeholders, args = inPlaceholders(orphanIDs)
	if _, err := tx.ExecContext(ctx, `UPDATE narrators SET prune_boundary_message_id = NULL WHERE prune_boundary_message_id IN (`+placeholders+`)`, args...); err != nil {
		return err
	}
	if tables.ChapterCommits {
		placeholders, args = inPlaceholders(orphanIDs)
		if _, err := tx.ExecContext(ctx, `UPDATE chapter_commits SET narrator_message_id = NULL WHERE narrator_message_id IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	if tables.APIRequests {
		placeholders, args = inPlaceholders(orphanIDs)
		if _, err := tx.ExecContext(ctx, `UPDATE api_requests SET message_id = NULL WHERE message_id IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	if tables.NarratorPatches {
		placeholders, args = inPlaceholders(orphanIDs)
		if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_patches WHERE message_id IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	if tables.NarratorSidecars {
		placeholders, args = inPlaceholders(orphanIDs)
		if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_sidecars WHERE message_id IN (`+placeholders+`)`, args...); err != nil {
			return err
		}
	}
	placeholders, args = inPlaceholders(orphanIDs)
	if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_message_refs WHERE message_id IN (`+placeholders+`)`, args...); err != nil {
		return err
	}
	placeholders, args = inPlaceholders(orphanIDs)
	if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_tool_calls WHERE message_id IN (`+placeholders+`)`, args...); err != nil {
		return err
	}
	placeholders, args = inPlaceholders(orphanIDs)
	_, err := tx.ExecContext(ctx, `DELETE FROM narrator_messages WHERE id IN (`+placeholders+`)`, args...)
	return err
}

func (s *Service) cleanToolUseBlocks(ctx context.Context, tx *sql.Tx, narratorID, messageID string, blocks []map[string]any, tables optionalNarratorTables) error {
	toolUseIDs := toolUseIDsFromBlocks(blocks)
	if len(toolUseIDs) == 0 {
		return nil
	}
	placeholders, args := inPlaceholders(toolUseIDs)
	args = append(args, messageID)
	if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_tool_calls WHERE tool_use_id IN (`+placeholders+`) AND message_id = ?`, args...); err != nil {
		return err
	}
	childIDs, err := s.childMessageIDsForToolUses(ctx, tx, toolUseIDs, 1000)
	if err != nil {
		return err
	}
	if len(childIDs) == 0 {
		return nil
	}
	placeholders, args = inPlaceholders(childIDs)
	args = append(args, narratorID)
	var otherRefCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_message_refs WHERE message_id IN (`+placeholders+`) AND narrator_id != ?`, args...).Scan(&otherRefCount); err != nil {
		return err
	}
	if otherRefCount == 0 {
		return s.deleteOrphanedMessages(ctx, tx, childIDs, tables)
	}
	placeholders, args = inPlaceholders(childIDs)
	args = append(args, narratorID)
	_, err = tx.ExecContext(ctx, `DELETE FROM narrator_message_refs WHERE message_id IN (`+placeholders+`) AND narrator_id = ?`, args...)
	return err
}

func updateNarratorMessageStatsTx(ctx context.Context, tx *sql.Tx, narratorID string, preserveConversationID bool) error {
	now := db.NowISO()
	setConversation := "api_conversation_id = NULL, "
	if preserveConversationID {
		setConversation = ""
	}
	_, err := tx.ExecContext(ctx, `UPDATE narrators SET `+setConversation+`prune_boundary_message_id = NULL, pruned_percent = NULL, message_count = (SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ?), message_version = message_version + 1, last_message_at = (SELECT MAX(m.created_at) FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ?), updated_at = ? WHERE id = ?`, narratorID, narratorID, now, narratorID)
	return err
}

func (s *Service) broadcastMessageUpdated(ctx context.Context, narratorID string, msg Message) {
	if s.Hub == nil {
		return
	}
	s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "message_updated", "narratorId": narratorID, "message": msg})
	if msg.ParentToolUseID == nil || strings.TrimSpace(*msg.ParentToolUseID) == "" {
		return
	}
	var parentNarratorID sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT parent_narrator_id FROM narrators WHERE id = ?`, msg.NarratorID).Scan(&parentNarratorID); err != nil || !parentNarratorID.Valid || strings.TrimSpace(parentNarratorID.String) == "" || parentNarratorID.String == narratorID {
		return
	}
	s.Hub.BroadcastNarrator(parentNarratorID.String, map[string]any{"type": "message_updated", "narratorId": parentNarratorID.String, "message": msg, "childNarratorId": msg.NarratorID, "parentToolUseId": *msg.ParentToolUseID})
}

func buildEditedUserContentJSON(contentJSON any, newContent string, fallbackUploadNarratorID string) []map[string]any {
	blocks := messageContentBlocks(contentJSON)
	newBlocks := []map[string]any{}
	insertedText := false
	for _, block := range blocks {
		switch stringFromAny(block["type"]) {
		case "text":
			if !insertedText {
				newBlocks = append(newBlocks, map[string]any{"type": "text", "text": newContent})
				insertedText = true
			}
		case "image":
			imageID := stringFromAny(block["imageId"])
			filename := stringFromAny(block["filename"])
			mediaType := stringFromAny(block["mediaType"])
			if imageID == "" || filename == "" || mediaType == "" {
				continue
			}
			preserved := map[string]any{"type": "image", "imageId": imageID, "filename": filename, "mediaType": mediaType}
			uploadNarratorID := stringFromAny(block["uploadNarratorId"])
			if uploadNarratorID == "" {
				uploadNarratorID = fallbackUploadNarratorID
			}
			if uploadNarratorID != "" {
				preserved["uploadNarratorId"] = uploadNarratorID
			}
			newBlocks = append(newBlocks, preserved)
		}
	}
	if !insertedText {
		newBlocks = append(newBlocks, map[string]any{"type": "text", "text": newContent})
	}
	return newBlocks
}

// buildEditedAssistantContentJSON mirrors the TypeScript buildEditedAssistantContentJson:
// replace the first text block with the edited text, drop any additional text blocks
// (the single-textarea edit consolidates text), and preserve all non-text blocks.
func buildEditedAssistantContentJSON(contentJSON any, newContent string) []map[string]any {
	blocks := messageContentBlocks(contentJSON)
	newBlocks := []map[string]any{}
	replacedText := false
	for _, block := range blocks {
		if stringFromAny(block["type"]) == "text" {
			if !replacedText {
				updated := map[string]any{}
				for k, v := range block {
					updated[k] = v
				}
				updated["text"] = newContent
				newBlocks = append(newBlocks, updated)
				replacedText = true
			}
			continue
		}
		newBlocks = append(newBlocks, block)
	}
	if !replacedText {
		newBlocks = append(newBlocks, map[string]any{"type": "text", "text": newContent})
	}
	return newBlocks
}

func editedByArg(editedBy **string) any {
	if editedBy == nil {
		return nil
	}
	return nullableStringPtr(*editedBy)
}

func originalContentJSONArg(original any) any {
	if original == nil {
		return nil
	}
	if s, ok := original.(string); ok {
		return s
	}
	data, err := json.Marshal(original)
	if err != nil {
		return nil
	}
	return string(data)
}

// EditAssistantMessage edits the displayed text of an assistant message without
// truncating following messages or re-running the agent. The edit metadata
// (edited_at / edited_by / original_content_json) lives on the message row and is
// never sent to the AI provider. Shared messages are copy-on-write protected.
func (s *Service) EditAssistantMessage(ctx context.Context, narratorID, messageID, newContent string, editedBy *string) (Message, error) {
	target, err := s.messageForNarratorRef(ctx, narratorID, messageID)
	if err != nil {
		return Message{}, err
	}
	if target.Role != "assistant" {
		return Message{}, httpx.NotFound("Can only edit assistant messages")
	}

	newBlocks := buildEditedAssistantContentJSON(target.ContentJSON, newContent)
	now := db.NowISO()
	editedByPtr := editedBy
	overrides := copyOnWriteOverrides{
		ContentText:     &newContent,
		ContentJSON:     newBlocks,
		SetEditMetadata: true,
		EditedAt:        &now,
		EditedBy:        &editedByPtr,
	}
	// Preserve the original content the first time a message is edited so the
	// front-end can always reveal the unedited version.
	if target.EditedAt == nil {
		original := any(target.RawContentJSON)
		overrides.OriginalContentJSON = &original
	}

	privateMessageID, err := s.copyOnWriteMessage(ctx, narratorID, messageID, overrides)
	if err != nil {
		return Message{}, err
	}
	updated, err := s.messageForNarratorRef(ctx, narratorID, privateMessageID)
	if err != nil {
		return Message{}, err
	}
	s.broadcastMessageUpdated(ctx, narratorID, updated.Message)
	if privateMessageID != messageID && s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "full_reload", "narratorId": narratorID})
	}
	return updated.Message, nil
}

// RestoreAssistantMessage reverts a previously-edited assistant message back to
// its original content, clearing the edit metadata so the message looks like it
// was never edited. Mirrors the TypeScript restoreAssistantMessage service.
func (s *Service) RestoreAssistantMessage(ctx context.Context, narratorID, messageID string) (Message, error) {
	target, err := s.messageForNarratorRef(ctx, narratorID, messageID)
	if err != nil {
		return Message{}, err
	}
	if target.Role != "assistant" {
		return Message{}, httpx.NotFound("Can only restore assistant messages")
	}
	// Nothing to restore if the message was never edited.
	if target.EditedAt == nil {
		return target.Message, nil
	}

	originalBlocks := []map[string]any{}
	if blocks, ok := target.OriginalContentJSON.([]map[string]any); ok {
		originalBlocks = blocks
	} else if rawBlocks, ok := target.OriginalContentJSON.([]any); ok {
		for _, raw := range rawBlocks {
			if block, ok := raw.(map[string]any); ok {
				originalBlocks = append(originalBlocks, block)
			}
		}
	}
	originalText := contentTextFromTextBlocksJoined(originalBlocks, "\n\n")

	clearedOriginal := any(nil)
	var clearedEditedBy *string
	overrides := copyOnWriteOverrides{
		ContentText:         &originalText,
		ContentJSON:         originalBlocks,
		SetEditMetadata:     true,
		EditedAt:            nil,
		EditedBy:            &clearedEditedBy,
		OriginalContentJSON: &clearedOriginal,
	}

	privateMessageID, err := s.copyOnWriteMessage(ctx, narratorID, messageID, overrides)
	if err != nil {
		return Message{}, err
	}
	updated, err := s.messageForNarratorRef(ctx, narratorID, privateMessageID)
	if err != nil {
		return Message{}, err
	}
	s.broadcastMessageUpdated(ctx, narratorID, updated.Message)
	if privateMessageID != messageID && s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "full_reload", "narratorId": narratorID})
	}
	return updated.Message, nil
}

func contentTextFromTextBlocksJoined(blocks []map[string]any, sep string) string {
	texts := []string{}
	for _, block := range blocks {
		if stringFromAny(block["type"]) != "text" {
			continue
		}
		if text := stringFromAny(block["text"]); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, sep)
}

func contentTextFromMapBlocks(blocks []map[string]any) string {
	texts := []string{}
	for _, block := range blocks {
		if stringFromAny(block["type"]) != "text" {
			continue
		}
		if text := stringFromAny(block["text"]); strings.TrimSpace(text) != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

func toolUseIDsFromBlocks(blocks []map[string]any) []string {
	ids := []string{}
	for _, block := range blocks {
		if stringFromAny(block["type"]) != "tool_use" {
			continue
		}
		if toolUseID := strings.TrimSpace(stringFromAny(block["id"])); toolUseID != "" {
			ids = append(ids, toolUseID)
		}
	}
	return uniqueStrings(ids)
}

func toolUseIDsFromRawContent(raw string) []string {
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		return nil
	}
	return toolUseIDsFromBlocks(blocks)
}

func narratorOptionalTables(ctx context.Context, sqlDB *sql.DB) (optionalNarratorTables, error) {
	var tables optionalNarratorTables
	var err error
	if tables.NarratorSidecars, err = sqlTableExists(ctx, sqlDB, "narrator_sidecars"); err != nil {
		return tables, err
	}
	if tables.NarratorPatches, err = sqlTableExists(ctx, sqlDB, "narrator_patches"); err != nil {
		return tables, err
	}
	if tables.APIRequests, err = sqlTableExists(ctx, sqlDB, "api_requests"); err != nil {
		return tables, err
	}
	if tables.ChapterCommits, err = sqlTableExists(ctx, sqlDB, "chapter_commits"); err != nil {
		return tables, err
	}
	return tables, nil
}

func sqlTableExists(ctx context.Context, sqlDB *sql.DB, table string) (bool, error) {
	columns, err := db.TableColumnSet(ctx, sqlDB, table)
	if err != nil {
		return false, err
	}
	return len(columns) > 0, nil
}

func stringSet(values []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func uniqueSortedStrings(values []string) []string {
	values = uniqueStrings(values)
	sort.Strings(values)
	return values
}

func nullStringAny(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func nullIntAny(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}
