package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
)

type narratorDeletePlan struct {
	IDs         []string
	KeepUploads map[string]bool
}

// RemoveNarrator permanently removes an archived narrator and its subagent tree.
// Shared messages are retained by handing their owner to a surviving ref owner;
// message-local upload ownership is annotated before removing the old narrator.
func (s *Service) RemoveNarrator(ctx context.Context, narratorID string) error {
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return err
	}
	if narrator.Status != "archived" {
		return httpx.BadRequest("only archived narrators can be deleted")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	plan, err := s.removeNarratorTx(ctx, tx, narratorID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	for _, idValue := range plan.IDs {
		if plan.KeepUploads[idValue] {
			continue
		}
		_ = os.RemoveAll(filepath.Join(narratorUploadsRoot(), idValue))
	}
	return nil
}

func (s *Service) removeNarratorTx(ctx context.Context, tx *sql.Tx, narratorID string) (narratorDeletePlan, error) {
	ids, err := s.collectNarratorDeleteTree(ctx, tx, narratorID)
	if err != nil {
		return narratorDeletePlan{}, err
	}
	if len(ids) == 0 {
		return narratorDeletePlan{}, httpx.NotFound("Narrator")
	}
	if err := s.validateNarratorDeleteTree(ctx, tx, ids); err != nil {
		return narratorDeletePlan{}, err
	}

	plan := narratorDeletePlan{IDs: ids, KeepUploads: map[string]bool{}}
	messageIDs, err := narratorDeleteCandidateMessageIDs(ctx, tx, ids)
	if err != nil {
		return narratorDeletePlan{}, err
	}
	if err := s.handoffSharedMessagesBeforeDelete(ctx, tx, ids, messageIDs, plan.KeepUploads); err != nil {
		return narratorDeletePlan{}, err
	}
	if err := deleteRowsWhereIn(ctx, tx, "narrator_message_refs", "narrator_id", ids); err != nil {
		return narratorDeletePlan{}, err
	}

	orphanIDs, err := narratorDeleteOrphanMessageIDs(ctx, tx, messageIDs)
	if err != nil {
		return narratorDeletePlan{}, err
	}
	if err := deleteNarratorOrphanMessages(ctx, tx, orphanIDs); err != nil {
		return narratorDeletePlan{}, err
	}
	if err := s.deleteNarratorOwnedRows(ctx, tx, ids); err != nil {
		return narratorDeletePlan{}, err
	}
	if err := deleteRowsWhereIn(ctx, tx, "narrators", "id", ids); err != nil {
		return narratorDeletePlan{}, err
	}
	return plan, nil
}

func (s *Service) collectNarratorDeleteTree(ctx context.Context, tx *sql.Tx, rootID string) ([]string, error) {
	seen := map[string]bool{}
	ids := []string{}
	frontier := []string{rootID}
	for len(frontier) > 0 {
		current := uniqueStrings(frontier)
		frontier = nil
		for _, idValue := range current {
			if seen[idValue] {
				continue
			}
			seen[idValue] = true
			ids = append(ids, idValue)
		}
		placeholders, args := inPlaceholders(current)
		rows, err := tx.QueryContext(ctx, `SELECT id FROM narrators WHERE parent_narrator_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var childID string
			if err := rows.Scan(&childID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if !seen[childID] {
				frontier = append(frontier, childID)
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func (s *Service) validateNarratorDeleteTree(ctx context.Context, tx *sql.Tx, narratorIDs []string) error {
	narratorIDs = uniqueStrings(narratorIDs)
	if len(narratorIDs) == 0 {
		return nil
	}
	placeholders, args := inPlaceholders(narratorIDs)
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(status, ''), COALESCE(is_background, 0), background_status FROM narrators WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var idValue, status string
		var isBackground int
		var backgroundStatus sql.NullString
		if err := rows.Scan(&idValue, &status, &isBackground, &backgroundStatus); err != nil {
			_ = rows.Close()
			return err
		}
		if status != "archived" {
			_ = rows.Close()
			return httpx.BadRequest(fmt.Sprintf("cannot delete narrator tree: narrator %s is %s; archive descendants before deleting", idValue, status))
		}
		if isBackground != 0 && backgroundStatus.Valid && backgroundStatus.String == "running" {
			_ = rows.Close()
			return httpx.BadRequest(fmt.Sprintf("cannot delete narrator tree: background narrator %s is still running", idValue))
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}

	if err := s.ensureNoRunningBackgroundTasks(ctx, tx, narratorIDs); err != nil {
		return err
	}
	return ensureNoRunningTerminals(ctx, tx, narratorIDs)
}

func (s *Service) ensureNoRunningBackgroundTasks(ctx context.Context, tx *sql.Tx, narratorIDs []string) error {
	placeholders, args := inPlaceholders(narratorIDs)
	queryArgs := append(append([]any{}, args...), args...)
	row := tx.QueryRowContext(ctx, `SELECT id, status FROM background_tasks WHERE (parent_narrator_id IN (`+placeholders+`) OR subagent_narrator_id IN (`+placeholders+`)) AND status IN ('running', 'pending', 'queued') LIMIT 1`, queryArgs...)
	var taskID, status string
	if err := row.Scan(&taskID, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	return httpx.BadRequest(fmt.Sprintf("cannot delete narrator tree: background task %s is %s", taskID, status))
}

func ensureNoRunningTerminals(ctx context.Context, tx *sql.Tx, narratorIDs []string) error {
	exists, err := txTableExists(ctx, tx, "terminals")
	if err != nil || !exists {
		return err
	}
	placeholders, args := inPlaceholders(narratorIDs)
	row := tx.QueryRowContext(ctx, `SELECT id, status FROM terminals WHERE narrator_id IN (`+placeholders+`) AND status = 'running' LIMIT 1`, args...)
	var terminalID, status string
	if err := row.Scan(&terminalID, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	return httpx.BadRequest(fmt.Sprintf("cannot delete narrator tree: terminal %s is %s", terminalID, status))
}

func txTableExists(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return name == table, nil
}

func narratorDeleteCandidateMessageIDs(ctx context.Context, tx *sql.Tx, narratorIDs []string) ([]string, error) {
	placeholders, args := inPlaceholders(narratorIDs)
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT message_id FROM narrator_message_refs WHERE narrator_id IN (`+placeholders+`) AND message_id IS NOT NULL`, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, messageID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	placeholders, args = inPlaceholders(narratorIDs)
	rows, err = tx.QueryContext(ctx, `SELECT id FROM narrator_messages WHERE narrator_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, messageID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return uniqueStrings(ids), nil
}

func (s *Service) handoffSharedMessagesBeforeDelete(ctx context.Context, tx *sql.Tx, narratorIDs []string, messageIDs []string, keepUploads map[string]bool) error {
	messageIDs = uniqueStrings(messageIDs)
	if len(messageIDs) == 0 {
		return nil
	}
	deleteSet := stringSet(narratorIDs)
	placeholders, args := inPlaceholders(messageIDs)
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(narrator_id, ''), COALESCE(content_json, '[]') FROM narrator_messages WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return err
	}
	type messageRow struct {
		ID      string
		OwnerID string
		RawJSON string
	}
	messages := []messageRow{}
	for rows.Next() {
		var msg messageRow
		if err := rows.Scan(&msg.ID, &msg.OwnerID, &msg.RawJSON); err != nil {
			_ = rows.Close()
			return err
		}
		messages = append(messages, msg)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, msg := range messages {
		survivorID, ok, err := survivingMessageRefOwner(ctx, tx, msg.ID, narratorIDs)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		contentRaw := msg.RawJSON
		if _, deletingOwner := deleteSet[msg.OwnerID]; deletingOwner {
			updatedRaw, keepUpload, changed, err := annotateImageUploadOwner(contentRaw, msg.OwnerID)
			if err != nil {
				return err
			}
			if keepUpload && msg.OwnerID != "" {
				keepUploads[msg.OwnerID] = true
			}
			if changed {
				contentRaw = updatedRaw
			}
			if _, err := tx.ExecContext(ctx, `UPDATE narrator_messages SET narrator_id = ?, content_json = ? WHERE id = ?`, survivorID, contentRaw, msg.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func survivingMessageRefOwner(ctx context.Context, tx *sql.Tx, messageID string, deletingNarratorIDs []string) (string, bool, error) {
	placeholders, args := inPlaceholders(deletingNarratorIDs)
	args = append([]any{messageID}, args...)
	row := tx.QueryRowContext(ctx, `SELECT narrator_id FROM narrator_message_refs WHERE message_id = ? AND narrator_id NOT IN (`+placeholders+`) ORDER BY seq ASC LIMIT 1`, args...)
	var ownerID string
	if err := row.Scan(&ownerID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return ownerID, true, nil
}

func annotateImageUploadOwner(raw string, oldOwnerID string) (string, bool, bool, error) {
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		return raw, false, false, nil
	}
	changed := false
	keepUpload := false
	for _, block := range blocks {
		if stringFromAny(block["type"]) != "image" {
			continue
		}
		owner := stringFromAny(block["uploadNarratorId"])
		if owner == "" {
			if oldOwnerID != "" {
				block["uploadNarratorId"] = oldOwnerID
				changed = true
				keepUpload = true
			}
			continue
		}
		if owner == oldOwnerID {
			keepUpload = true
		}
	}
	if !changed {
		return raw, keepUpload, false, nil
	}
	data, err := json.Marshal(blocks)
	if err != nil {
		return "", false, false, err
	}
	return string(data), keepUpload, true, nil
}

func narratorDeleteOrphanMessageIDs(ctx context.Context, tx *sql.Tx, candidateIDs []string) ([]string, error) {
	candidateIDs = uniqueStrings(candidateIDs)
	if len(candidateIDs) == 0 {
		return []string{}, nil
	}
	placeholders, args := inPlaceholders(candidateIDs)
	rows, err := tx.QueryContext(ctx, `SELECT id FROM narrator_messages WHERE id IN (`+placeholders+`) AND NOT EXISTS (SELECT 1 FROM narrator_message_refs WHERE message_id = narrator_messages.id)`, args...)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, messageID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return uniqueStrings(ids), nil
}

func deleteNarratorOrphanMessages(ctx context.Context, tx *sql.Tx, orphanIDs []string) error {
	orphanIDs = uniqueStrings(orphanIDs)
	if len(orphanIDs) == 0 {
		return nil
	}
	if err := setNullWhereIn(ctx, tx, "narrators", "fork_message_id", orphanIDs); err != nil {
		return err
	}
	if err := setNullWhereIn(ctx, tx, "narrators", "prune_boundary_message_id", orphanIDs); err != nil {
		return err
	}
	if err := setNullWhereIn(ctx, tx, "api_requests", "message_id", orphanIDs); err != nil {
		return err
	}
	if err := setNullWhereIn(ctx, tx, "chapter_commits", "narrator_message_id", orphanIDs); err != nil {
		return err
	}
	for _, table := range []string{"narrator_tool_calls", "narrator_patches", "narrator_sidecars", "narrator_message_refs"} {
		if err := deleteRowsWhereIn(ctx, tx, table, "message_id", orphanIDs); err != nil {
			return err
		}
	}
	return deleteRowsWhereIn(ctx, tx, "narrator_messages", "id", orphanIDs)
}

func (s *Service) deleteNarratorOwnedRows(ctx context.Context, tx *sql.Tx, narratorIDs []string) error {
	if err := deleteRowsWhereIn(ctx, tx, "background_tasks", "parent_narrator_id", narratorIDs); err != nil {
		return err
	}
	if err := deleteRowsWhereIn(ctx, tx, "background_tasks", "subagent_narrator_id", narratorIDs); err != nil {
		return err
	}
	for _, table := range []string{
		"narrator_tool_calls",
		"narrator_file_snapshots",
		"narrator_patches",
		"narrator_sidecars",
		"narrator_buffered_messages",
		"narrator_goals",
		"terminal_view_state",
		"terminal_tabs",
		"terminals",
		"gateway_session_mappings",
	} {
		if err := deleteRowsWhereIn(ctx, tx, table, "narrator_id", narratorIDs); err != nil {
			return err
		}
	}
	return s.deleteBrowserSessionRows(ctx, tx, narratorIDs)
}

func (s *Service) deleteBrowserSessionRows(ctx context.Context, tx *sql.Tx, narratorIDs []string) error {
	browserDB := s.browserDB()
	if browserDB == nil {
		return nil
	}
	if browserDB == s.DB {
		return deleteRowsWhereIn(ctx, tx, "narrator_browser_sessions", "narrator_id", narratorIDs)
	}
	return deleteRowsWhereInDB(ctx, browserDB, "narrator_browser_sessions", "narrator_id", narratorIDs)
}

func deleteRowsWhereIn(ctx context.Context, tx *sql.Tx, table, column string, values []string) error {
	return execWhereIn(ctx, tx, table, column, values, func(placeholders string) string {
		return fmt.Sprintf(`DELETE FROM %s WHERE %s IN (`+placeholders+`)`, table, column)
	})
}

func deleteRowsWhereInDB(ctx context.Context, sqlDB *sql.DB, table, column string, values []string) error {
	return execWhereIn(ctx, sqlDB, table, column, values, func(placeholders string) string {
		return fmt.Sprintf(`DELETE FROM %s WHERE %s IN (`+placeholders+`)`, table, column)
	})
}

func setNullWhereIn(ctx context.Context, tx *sql.Tx, table, column string, values []string) error {
	return execWhereIn(ctx, tx, table, column, values, func(placeholders string) string {
		return fmt.Sprintf(`UPDATE %s SET %s = NULL WHERE %s IN (`+placeholders+`)`, table, column, column)
	})
}

type queryExecer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func execWhereIn(ctx context.Context, dbtx queryExecer, table, column string, values []string, build func(string) string) error {
	values = uniqueStrings(values)
	if len(values) == 0 {
		return nil
	}
	columns, err := db.TableColumnSet(ctx, dbtx, table)
	if err != nil {
		return err
	}
	if len(columns) == 0 || !columns[column] {
		return nil
	}
	placeholders, args := inPlaceholders(values)
	_, err = dbtx.ExecContext(ctx, build(placeholders), args...)
	return err
}
