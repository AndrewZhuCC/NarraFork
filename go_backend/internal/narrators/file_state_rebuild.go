package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"narrafork-go/internal/httpx"
)

type orderedFileToolCall struct {
	ToolUseID        string
	ToolName         string
	MessageID        string
	Input            map[string]any
	RawFilePath      string
	ResolvedFilePath string
	Seq              int
	CreatedAt        string
	Output           map[string]any
}

type rebuiltFileState struct {
	FilePath string
	Content  *string
}

func queryOrderedFileToolCalls(ctx context.Context, sqlDB *sql.DB, narratorID string, cwd string, maxSeq *int) ([]orderedFileToolCall, error) {
	query := `SELECT tc.tool_use_id, tc.tool_name, COALESCE(tc.input_json, ''), COALESCE(tc.output_json, ''), tc.message_id, refs.seq, tc.created_at
FROM narrator_tool_calls tc
INNER JOIN narrator_message_refs refs ON refs.narrator_id = ? AND refs.message_id = tc.message_id
WHERE tc.narrator_id = ? AND tc.status = 'success' AND LOWER(tc.tool_name) IN ('write', 'edit', 'bash')`
	args := []any{narratorID, narratorID}
	if maxSeq != nil {
		query += ` AND refs.seq <= ?`
		args = append(args, *maxSeq)
	}
	query += ` ORDER BY refs.seq ASC, tc.created_at ASC`

	rows, err := sqlDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	calls := []orderedFileToolCall{}
	for rows.Next() {
		var call orderedFileToolCall
		var inputRaw, outputRaw string
		if err := rows.Scan(&call.ToolUseID, &call.ToolName, &inputRaw, &outputRaw, &call.MessageID, &call.Seq, &call.CreatedAt); err != nil {
			return nil, err
		}
		call.Input = map[string]any{}
		if strings.TrimSpace(inputRaw) != "" {
			_ = json.Unmarshal([]byte(inputRaw), &call.Input)
		}
		call.Output = map[string]any{}
		if strings.TrimSpace(outputRaw) != "" {
			_ = json.Unmarshal([]byte(outputRaw), &call.Output)
		}
		call.RawFilePath = toolCallInputFilePath(call.Input)
		if strings.EqualFold(call.ToolName, "Bash") && call.RawFilePath == "" {
			entries := toolCallOutputSnapshotEntries(call.Output)
			for _, entry := range entries {
				bashCall := call
				bashCall.Input = map[string]any{"__bash_snapshot_content": entry.Content, "__bash_snapshot_exists": entry.Exists}
				bashCall.RawFilePath = entry.FilePath
				bashCall.ResolvedFilePath = resolveSnapshotFilePath(cwd, bashCall.RawFilePath)
				if bashCall.RawFilePath != "" {
					calls = append(calls, bashCall)
				}
			}
			continue
		}
		call.ResolvedFilePath = resolveSnapshotFilePath(cwd, call.RawFilePath)
		if call.RawFilePath == "" {
			continue
		}
		calls = append(calls, call)
	}
	return calls, rows.Err()
}

func rebuildFileState(ctx context.Context, sqlDB *sql.DB, narratorID, filePath, cwd string, maxSeq *int) (*string, error) {
	snapshots, err := querySnapshotOriginals(ctx, sqlDB, narratorID)
	if err != nil {
		return nil, err
	}
	content, ok := snapshots[filePath]
	if !ok {
		resolved := resolveSnapshotFilePath(cwd, filePath)
		content = snapshots[resolved]
	}
	calls, err := queryOrderedFileToolCalls(ctx, sqlDB, narratorID, cwd, maxSeq)
	if err != nil {
		return nil, err
	}
	for _, call := range calls {
		if !toolCallAffectsFile(call, filePath, cwd) {
			continue
		}
		content = applyFileToolCall(content, call)
	}
	return content, nil
}

func rebuildFileStatesUpToSeq(ctx context.Context, sqlDB *sql.DB, narratorID, cwd string, maxSeq *int) (map[string]*string, error) {
	return rebuildFileStatesExcluding(ctx, sqlDB, narratorID, cwd, nil, nil, maxSeq)
}

func rebuildFileStatesExcluding(ctx context.Context, sqlDB *sql.DB, narratorID, cwd string, filePaths []string, exclude map[string]struct{}, maxSeq *int) (map[string]*string, error) {
	snapshots, err := querySnapshotOriginals(ctx, sqlDB, narratorID)
	if err != nil {
		return nil, err
	}
	calls, err := queryOrderedFileToolCalls(ctx, sqlDB, narratorID, cwd, maxSeq)
	if err != nil {
		return nil, err
	}

	filter := map[string]struct{}{}
	for _, path := range filePaths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		filter[path] = struct{}{}
		if resolved := resolveSnapshotFilePath(cwd, path); resolved != "" {
			filter[resolved] = struct{}{}
		}
	}
	useFilter := len(filter) > 0

	states := map[string]*string{}
	for _, call := range calls {
		if exclude != nil {
			if _, skip := exclude[call.ToolUseID]; skip {
				continue
			}
		}
		key := canonicalToolCallFilePath(call, snapshots)
		if key == "" {
			continue
		}
		if useFilter {
			if _, ok := filter[key]; !ok {
				if _, ok := filter[call.RawFilePath]; !ok {
					if _, ok := filter[call.ResolvedFilePath]; !ok {
						continue
					}
				}
			}
		}
		if _, ok := states[key]; !ok {
			states[key] = snapshots[key]
			if _, ok := snapshots[key]; !ok {
				if value, ok := snapshots[call.RawFilePath]; ok {
					states[key] = value
				} else if value, ok := snapshots[call.ResolvedFilePath]; ok {
					states[key] = value
				}
			}
		}
		states[key] = applyFileToolCall(states[key], call)
	}

	if useFilter {
		for _, path := range filePaths {
			key := canonicalFilePathForSnapshots(path, cwd, snapshots)
			if key == "" {
				continue
			}
			if _, ok := states[key]; !ok {
				states[key] = snapshots[key]
			}
		}
	}
	return states, nil
}

func querySnapshotOriginals(ctx context.Context, sqlDB *sql.DB, narratorID string) (map[string]*string, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT file_path, original_content FROM narrator_file_snapshots WHERE narrator_id = ?`, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := map[string]*string{}
	for rows.Next() {
		var filePath string
		var original sql.NullString
		if err := rows.Scan(&filePath, &original); err != nil {
			return nil, err
		}
		if original.Valid {
			value := original.String
			items[filePath] = &value
		} else {
			items[filePath] = nil
		}
	}
	return items, rows.Err()
}

func getAffectedFilePaths(calls []orderedFileToolCall) []string {
	seen := map[string]struct{}{}
	for _, call := range calls {
		if call.RawFilePath == "" {
			continue
		}
		seen[call.RawFilePath] = struct{}{}
		if call.ResolvedFilePath != "" {
			seen[call.ResolvedFilePath] = struct{}{}
		}
	}
	items := make([]string, 0, len(seen))
	for path := range seen {
		items = append(items, path)
	}
	sort.Strings(items)
	return items
}

func applyFileToolCall(current *string, call orderedFileToolCall) *string {
	name := strings.ToLower(strings.TrimSpace(call.ToolName))
	switch name {
	case "bash":
		return applyBashFileSnapshotToolCall(current, call)
	case "write":
		content, ok := call.Input["content"].(string)
		if !ok {
			return current
		}
		return stringPtr(content)
	case "edit":
		newString, ok := call.Input["new_string"].(string)
		if !ok {
			return current
		}
		oldString, _ := call.Input["old_string"].(string)
		if oldString == "" {
			return stringPtr(newString)
		}
		if current == nil {
			return nil
		}
		content := normalizeToolEditLineEndings(*current)
		oldNormalized := normalizeToolEditLineEndings(oldString)
		newNormalized := normalizeToolEditLineEndings(newString)
		if !strings.Contains(content, oldNormalized) {
			return current
		}
		if boolFromAny(call.Input["replace_all"]) {
			return stringPtr(strings.ReplaceAll(content, oldNormalized, newNormalized))
		}
		return stringPtr(strings.Replace(content, oldNormalized, newNormalized, 1))
	default:
		return current
	}
}

func applyBashFileSnapshotToolCall(current *string, call orderedFileToolCall) *string {
	if exists, ok := call.Input["__bash_snapshot_exists"].(bool); ok && !exists {
		return nil
	}
	if content, ok := call.Input["__bash_snapshot_content"].(string); ok {
		return stringPtr(content)
	}
	return current
}

func toolCallInputFilePath(input map[string]any) string {
	for _, key := range []string{"file_path", "filePath", "path"} {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type bashSnapshotOutputEntry struct {
	FilePath string
	Content  string
	Exists   bool
}

func toolCallOutputSnapshotEntries(output map[string]any) []bashSnapshotOutputEntry {
	items, ok := output["bashSnapshots"].([]any)
	if !ok {
		return nil
	}
	entries := []bashSnapshotOutputEntry{}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		filePath := strings.TrimSpace(stringFromAny(entry["filePath"]))
		content, _ := entry["content"].(string)
		exists := boolFromAny(entry["exists"])
		if filePath != "" {
			entries = append(entries, bashSnapshotOutputEntry{FilePath: filePath, Content: content, Exists: exists})
		}
	}
	return entries
}

func canonicalToolCallFilePath(call orderedFileToolCall, snapshots map[string]*string) string {
	if _, ok := snapshots[call.RawFilePath]; ok {
		return call.RawFilePath
	}
	if _, ok := snapshots[call.ResolvedFilePath]; ok {
		return call.ResolvedFilePath
	}
	if call.ResolvedFilePath != "" {
		return call.ResolvedFilePath
	}
	return call.RawFilePath
}

func canonicalFilePathForSnapshots(filePath, cwd string, snapshots map[string]*string) string {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return ""
	}
	if _, ok := snapshots[filePath]; ok {
		return filePath
	}
	resolved := resolveSnapshotFilePath(cwd, filePath)
	if _, ok := snapshots[resolved]; ok {
		return resolved
	}
	return resolved
}

func toolCallAffectsFile(call orderedFileToolCall, filePath, cwd string) bool {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return false
	}
	if call.RawFilePath == filePath || call.ResolvedFilePath == filePath {
		return true
	}
	resolved := resolveSnapshotFilePath(cwd, filePath)
	return resolved != "" && (call.RawFilePath == resolved || call.ResolvedFilePath == resolved)
}

func resolveSnapshotFilePath(cwd, filePath string) string {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return ""
	}
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}
	base := strings.TrimSpace(cwd)
	if base == "" {
		return filepath.Clean(filePath)
	}
	return filepath.Clean(filepath.Join(base, filePath))
}

func narratorWorkingDir(ctx context.Context, sqlDB *sql.DB, narrator Narrator) string {
	if narrator.ChapterID != nil && strings.TrimSpace(*narrator.ChapterID) != "" {
		var worktree sql.NullString
		if err := sqlDB.QueryRowContext(ctx, `SELECT worktree_path FROM chapters WHERE id = ?`, *narrator.ChapterID).Scan(&worktree); err == nil && worktree.Valid && strings.TrimSpace(worktree.String) != "" {
			return strings.TrimSpace(worktree.String)
		}
	}
	if narrator.Cwd != nil {
		return strings.TrimSpace(*narrator.Cwd)
	}
	return ""
}

func writeRebuiltFileStates(cwd string, states map[string]*string, skipMissing bool) ([]string, error) {
	files := make([]string, 0, len(states))
	keys := make([]string, 0, len(states))
	for filePath := range states {
		keys = append(keys, filePath)
	}
	sort.Strings(keys)
	for _, filePath := range keys {
		content := states[filePath]
		if content == nil && skipMissing {
			continue
		}
		if err := applySnapshotContent(resolveSnapshotFilePath(cwd, filePath), content); err != nil {
			return files, err
		}
		files = append(files, filePath)
	}
	return files, nil
}

func sortedMapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalizeToolEditLineEndings(value string) string {
	return strings.ReplaceAll(value, "\r\n", "\n")
}

func boolFromAny(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func activeNarratorMutationError(n Narrator, action string) error {
	if n.Status == "working" || n.Status == "waiting" {
		return httpConflict("Cannot " + action + " while narrator is running")
	}
	return nil
}

func httpConflict(message string) error {
	return httpx.Error(http.StatusConflict, "CONFLICT", message)
}
