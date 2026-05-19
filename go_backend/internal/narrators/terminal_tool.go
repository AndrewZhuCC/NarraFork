package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/db"
	"narrafork-go/internal/id"
	"narrafork-go/internal/terminaldb"
)

type terminalAgentRuntime interface {
	Ensure(ctx context.Context, terminalID string) error
	Write(ctx context.Context, terminalID string, data string) error
	Snapshot(ctx context.Context, terminalID string) (map[string]any, error)
}

type terminalToolRow struct {
	ID         string
	Name       string
	Status     string
	Cwd        sql.NullString
	CreatedAt  sql.NullString
	ChapterID  sql.NullString
	NarratorID sql.NullString
}

var terminalReadCursors sync.Map

const (
	terminalReadWaitForTimeout = 30 * time.Second
	terminalReadPollInterval   = 250 * time.Millisecond
	terminalDefaultMaxLength   = 20000
	terminalMaxLength          = 100000
)

var terminalANSIRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*(\x07|\x1b\\)`)

func (s *Service) executeContextualAgentTool(ctx context.Context, narratorID string, narrator Narrator, call agentbasic.ToolCall) (agentbasic.ToolRun, bool, error) {
	run := agentbasic.ToolRun{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments, Status: "completed"}
	if strings.TrimSpace(call.Function.Name) != "Terminal" {
		return run, false, nil
	}
	args := map[string]any{}
	if strings.TrimSpace(call.Function.Arguments) != "" {
		if err := jsonUnmarshalObject(call.Function.Arguments, &args); err != nil {
			run.Status = "error"
			run.Output = "invalid tool arguments: " + err.Error()
			return run, true, nil
		}
	}
	output, status, err := s.executeTerminalAgentTool(ctx, narratorID, narrator, args)
	if err != nil {
		return run, true, err
	}
	run.Output = output
	if status != "" {
		run.Status = status
	}
	return run, true, nil
}

func jsonUnmarshalObject(raw string, out *map[string]any) error {
	return json.Unmarshal([]byte(raw), out)
}

func (s *Service) executeTerminalAgentTool(ctx context.Context, narratorID string, narrator Narrator, args map[string]any) (string, string, error) {
	action := strings.ToLower(strings.TrimSpace(stringFromAny(args["action"])))
	if action == "" {
		return "action is required", "error", nil
	}
	switch action {
	case "list":
		return s.terminalToolList(ctx, narratorID, narrator)
	case "create":
		return s.terminalToolCreate(ctx, narratorID, narrator, strings.TrimSpace(stringFromAny(args["name"])))
	case "rename":
		return s.terminalToolRename(ctx, narratorID, narrator, strings.TrimSpace(stringFromAny(args["terminal_id"])), strings.TrimSpace(stringFromAny(args["name"])))
	case "read":
		return s.terminalToolRead(ctx, narratorID, narrator, args)
	case "write":
		return s.terminalToolWrite(ctx, narratorID, narrator, args)
	default:
		return "unknown terminal action: " + action, "error", nil
	}
}

func (s *Service) terminalToolList(ctx context.Context, narratorID string, narrator Narrator) (string, string, error) {
	chapterID := narratorChapterID(narrator)
	rows, err := s.terminalRowsForNarrator(ctx, narratorID, chapterID)
	if err != nil {
		return "", "", err
	}
	lines := []string{}
	for _, row := range rows {
		if row.Status == "exited" {
			continue
		}
		cwd := "unknown"
		if row.Cwd.Valid && strings.TrimSpace(row.Cwd.String) != "" {
			cwd = row.Cwd.String
		}
		created := "unknown"
		if row.CreatedAt.Valid && strings.TrimSpace(row.CreatedAt.String) != "" {
			created = row.CreatedAt.String
		}
		lines = append(lines, fmt.Sprintf("- %s | %s | %s | cwd: %s | created: %s", row.ID, row.Name, firstNonEmptyString(row.Status, "unknown"), cwd, created))
	}
	if len(lines) == 0 {
		return "No terminals found for this narrator or its chapter.", "", nil
	}
	return fmt.Sprintf("Found %d terminal(s):\n%s", len(lines), strings.Join(lines, "\n")), "", nil
}

func (s *Service) terminalToolCreate(ctx context.Context, narratorID string, narrator Narrator, name string) (string, string, error) {
	if s.TerminalRuntime == nil {
		return "Terminal runtime is unavailable in this Go narrator session.", "error", nil
	}
	if name == "" {
		name = "Terminal"
	}
	chapterID := narratorChapterID(narrator)
	cwdValue := ""
	cwd, err := terminaldb.ResolveCwd(ctx, s.DB, chapterID, narratorID)
	if err == nil && cwd.Valid {
		cwdValue = strings.TrimSpace(cwd.String)
	}
	if cwdValue == "" {
		cwdValue = firstNonEmptyString(ptrString(narrator.Cwd), userHomeDir())
	}
	terminalID := id.New()
	now := db.NowISO()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO terminals (id, chapter_id, narrator_id, name, cwd, status, created_at) VALUES (?, ?, ?, ?, ?, 'running', ?)`, terminalID, nullableToolString(chapterID), nullableToolString(narratorID), name, nullableToolString(cwdValue), now); err != nil {
		return "", "", err
	}
	if err := s.TerminalRuntime.Ensure(ctx, terminalID); err != nil {
		_, _ = s.DB.ExecContext(context.Background(), `DELETE FROM terminals WHERE id = ?`, terminalID)
		return "Failed to start terminal runtime: " + err.Error(), "error", nil
	}
	return fmt.Sprintf("Terminal created successfully.\n- ID: %s\n- Name: %s\n- CWD: %s", terminalID, name, firstNonEmptyString(cwdValue, "unknown")), "", nil
}

func (s *Service) terminalToolRename(ctx context.Context, narratorID string, narrator Narrator, terminalIDOrName string, name string) (string, string, error) {
	if terminalIDOrName == "" {
		return "terminal_id is required for rename", "error", nil
	}
	if name == "" {
		return "name is required for rename", "error", nil
	}
	row, err := s.resolveAccessibleTerminal(ctx, narratorID, narratorChapterID(narrator), terminalIDOrName)
	if err != nil {
		return err.Error(), "error", nil
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE terminals SET name = ? WHERE id = ?`, name, row.ID); err != nil {
		return "", "", err
	}
	return fmt.Sprintf("Terminal renamed to %q.", name), "", nil
}

func (s *Service) terminalToolRead(ctx context.Context, narratorID string, narrator Narrator, args map[string]any) (string, string, error) {
	if s.TerminalRuntime == nil {
		return "Terminal runtime is unavailable in this Go narrator session.", "error", nil
	}
	terminalIDOrName := strings.TrimSpace(stringFromAny(args["terminal_id"]))
	if terminalIDOrName == "" {
		return "terminal_id is required for read", "error", nil
	}
	row, err := s.resolveAccessibleTerminal(ctx, narratorID, narratorChapterID(narrator), terminalIDOrName)
	if err != nil {
		return err.Error(), "error", nil
	}
	if row.Status != "exited" {
		if err := s.TerminalRuntime.Ensure(ctx, row.ID); err != nil {
			return "Failed to attach terminal runtime: " + err.Error(), "error", nil
		}
	}
	waitFor := strings.TrimSpace(stringFromAny(args["wait_for"]))
	lastNLines := anyInt(args["last_n_lines"], 0)
	maxLength := normalizeTerminalMaxLength(anyInt(args["max_length"], terminalDefaultMaxLength))
	key := terminalCursorKey(narratorID, row.ID)
	waitTimedOut := false
	if waitFor != "" {
		deadline := time.Now().Add(terminalReadWaitForTimeout)
		for {
			text, snapErr := s.terminalSnapshotPlainText(ctx, row.ID)
			if snapErr != nil {
				return "", "", snapErr
			}
			start := terminalCursorOffset(key, text)
			if strings.Contains(text[start:], waitFor) {
				break
			}
			if time.Now().After(deadline) {
				waitTimedOut = true
				break
			}
			select {
			case <-ctx.Done():
				return "Terminal read aborted.", "error", nil
			case <-time.After(terminalReadPollInterval):
			}
		}
	}
	text, err := s.terminalSnapshotPlainText(ctx, row.ID)
	if err != nil {
		return "", "", err
	}
	start := terminalCursorOffset(key, text)
	output := ""
	if lastNLines > 0 {
		output = lastTerminalLines(text, lastNLines)
	} else {
		output = text[start:]
	}
	terminalReadCursors.Store(key, len(text))
	if strings.TrimSpace(output) == "" {
		output = "No new terminal output."
	}
	if waitTimedOut {
		output = "[wait_for timed out]\n" + output
	}
	return truncateTerminalOutput(output, maxLength), "", nil
}

func (s *Service) terminalToolWrite(ctx context.Context, narratorID string, narrator Narrator, args map[string]any) (string, string, error) {
	if s.TerminalRuntime == nil {
		return "Terminal runtime is unavailable in this Go narrator session.", "error", nil
	}
	terminalIDOrName := strings.TrimSpace(stringFromAny(args["terminal_id"]))
	if terminalIDOrName == "" {
		return "terminal_id is required for write", "error", nil
	}
	input, ok := args["input"].(string)
	if !ok {
		return "input is required for write", "error", nil
	}
	row, err := s.resolveAccessibleTerminal(ctx, narratorID, narratorChapterID(narrator), terminalIDOrName)
	if err != nil {
		return err.Error(), "error", nil
	}
	if text, snapErr := s.terminalSnapshotPlainText(ctx, row.ID); snapErr == nil {
		terminalReadCursors.Store(terminalCursorKey(narratorID, row.ID), len(text))
	}
	if err := s.TerminalRuntime.Write(ctx, row.ID, input); err != nil {
		return "Failed to write terminal input: " + err.Error(), "error", nil
	}
	return fmt.Sprintf("Wrote %d byte(s) to terminal %s.", len(input), row.ID), "", nil
}

func (s *Service) terminalRowsForNarrator(ctx context.Context, narratorID string, chapterID string) ([]terminalToolRow, error) {
	query := `SELECT id, name, status, cwd, created_at, chapter_id, narrator_id FROM terminals WHERE narrator_id = ?`
	args := []any{narratorID}
	if chapterID != "" {
		query += ` OR chapter_id = ?`
		args = append(args, chapterID)
	}
	query += ` ORDER BY created_at ASC`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []terminalToolRow{}
	seen := map[string]bool{}
	for rows.Next() {
		row, err := scanTerminalToolRow(rows)
		if err != nil {
			return nil, err
		}
		if seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		items = append(items, row)
	}
	return items, rows.Err()
}

func (s *Service) resolveAccessibleTerminal(ctx context.Context, narratorID string, chapterID string, idOrName string) (terminalToolRow, error) {
	row, found, err := s.terminalRowByID(ctx, idOrName)
	if err != nil {
		return terminalToolRow{}, err
	}
	if found {
		if terminalRowAccessible(row, narratorID, chapterID) {
			return row, nil
		}
		return terminalToolRow{}, fmt.Errorf("access denied: terminal %s does not belong to this narrator or its chapter", idOrName)
	}
	rows, err := s.terminalRowsForNarrator(ctx, narratorID, chapterID)
	if err != nil {
		return terminalToolRow{}, err
	}
	for _, item := range rows {
		if strings.EqualFold(item.Name, idOrName) {
			return item, nil
		}
	}
	return terminalToolRow{}, fmt.Errorf("terminal not found: %s", idOrName)
}

func (s *Service) terminalRowByID(ctx context.Context, terminalID string) (terminalToolRow, bool, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, name, status, cwd, created_at, chapter_id, narrator_id FROM terminals WHERE id = ?`, terminalID)
	item, err := scanTerminalToolRow(row)
	if err == sql.ErrNoRows {
		return terminalToolRow{}, false, nil
	}
	if err != nil {
		return terminalToolRow{}, false, err
	}
	return item, true, nil
}

func scanTerminalToolRow(row interface{ Scan(dest ...any) error }) (terminalToolRow, error) {
	var item terminalToolRow
	if err := row.Scan(&item.ID, &item.Name, &item.Status, &item.Cwd, &item.CreatedAt, &item.ChapterID, &item.NarratorID); err != nil {
		return terminalToolRow{}, err
	}
	return item, nil
}

func terminalRowAccessible(row terminalToolRow, narratorID string, chapterID string) bool {
	if row.NarratorID.Valid && row.NarratorID.String == narratorID {
		return true
	}
	return chapterID != "" && row.ChapterID.Valid && row.ChapterID.String == chapterID
}

func (s *Service) terminalSnapshotPlainText(ctx context.Context, terminalID string) (string, error) {
	snapshot, err := s.TerminalRuntime.Snapshot(ctx, terminalID)
	if err != nil {
		return "", err
	}
	return extractTerminalPlainText(stringFromAny(snapshot["data"])), nil
}

func narratorChapterID(narrator Narrator) string {
	if narrator.ChapterID == nil {
		return ""
	}
	return strings.TrimSpace(*narrator.ChapterID)
}

func terminalCursorKey(narratorID string, terminalID string) string {
	return narratorID + ":" + terminalID
}

func terminalCursorOffset(key string, text string) int {
	if value, ok := terminalReadCursors.Load(key); ok {
		if offset, ok := value.(int); ok && offset >= 0 && offset <= len(text) {
			return offset
		}
	}
	return 0
}

func extractTerminalPlainText(value string) string {
	plain := terminalANSIRe.ReplaceAllString(value, "")
	lines := strings.Split(plain, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[:end], "\n")
}

func lastTerminalLines(text string, count int) string {
	if count <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= count {
		return text
	}
	return strings.Join(lines[len(lines)-count:], "\n")
}

func normalizeTerminalMaxLength(value int) int {
	if value <= 0 {
		return terminalDefaultMaxLength
	}
	if value > terminalMaxLength {
		return terminalMaxLength
	}
	return value
}

func truncateTerminalOutput(value string, maxLength int) string {
	if len(value) <= maxLength {
		return value
	}
	return value[:maxLength] + "\n[truncated]"
}

func nullableToolString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func userHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}
