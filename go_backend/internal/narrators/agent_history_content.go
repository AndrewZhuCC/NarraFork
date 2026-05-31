package narrators

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"narrafork-go/internal/agentbasic"
)

func agentMessagesFromStoredMessage(msg Message) []agentbasic.Message {
	role := msg.Role
	blocks := messageContentBlocks(msg.ContentJSON)
	text := messageTextForAgent(msg, blocks)
	switch role {
	case "assistant":
		assistant := agentbasic.Message{Role: "assistant", Content: text, ContentBlocks: blocks, ToolCalls: toolCallsForAgent(msg, blocks)}
		messages := []agentbasic.Message{assistant}
		messages = append(messages, toolResultMessagesFromToolCalls(msg.ToolCalls)...)
		if sideCarText := appendSideCarsForAgent("", sideCarsForUserMessage(msg.SideCars)); sideCarText != "" {
			messages = append(messages, agentbasic.Message{Role: "user", Content: sideCarText})
		}
		return messages
	case "user":
		messages := toolResultMessagesFromBlocks(blocks)
		userBlocks := agentUserBlocksForProvider(nonToolResultBlocks(blocks), text)
		userSideCars := sideCarsForUserMessage(msg.SideCars)
		if sideCarText := formatSideCarsForAgent(userSideCars); sideCarText != "" {
			userBlocks = append(userBlocks, map[string]any{"type": "text", "text": sideCarText})
		}
		if strings.TrimSpace(text) != "" || len(userBlocks) > 0 || len(userSideCars) > 0 {
			messages = append(messages, agentbasic.Message{Role: "user", Content: appendSideCarsForAgent(text, userSideCars), ContentBlocks: userBlocks})
		}
		return messages
	case "sys":
		if strings.TrimSpace(text) == "" && len(blocks) == 0 {
			return nil
		}
		return []agentbasic.Message{{Role: "system", Content: text, ContentBlocks: blocks}}
	default:
		return nil
	}
}

func enrichAgentHistoryImageBlocks(msg Message, defaultNarratorID string) Message {
	blocks := messageContentBlocks(msg.ContentJSON)
	if len(blocks) == 0 {
		return msg
	}
	changed := false
	for _, block := range blocks {
		if historyStringFromAny(block["type"]) != "image" {
			continue
		}
		if historyStringFromAny(block["base64"]) != "" || historyStringFromAny(block["data"]) != "" {
			continue
		}
		if enrichAgentHistoryImageBlock(block, defaultNarratorID) {
			changed = true
		}
	}
	if changed {
		msg.ContentJSON = blocks
	}
	return msg
}

func enrichAgentHistoryImageBlock(block map[string]any, defaultNarratorID string) bool {
	imageID := historyStringFromAny(block["imageId"])
	if imageID == "" {
		return false
	}
	owner := historyFirstNonEmptyString(historyStringFromAny(block["uploadNarratorId"]), defaultNarratorID)
	path, ok := agentHistoryImagePath(owner, imageID)
	if !ok {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() <= 0 || info.Size() > maxMessageImageBytes {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return false
	}
	mediaType := historyStringFromAny(block["mediaType"])
	if mediaType == "" {
		mediaType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(strings.ToLower(mediaType), "image/") {
		return false
	}
	block["base64"] = base64.StdEncoding.EncodeToString(data)
	block["mediaType"] = mediaType
	if historyStringFromAny(block["format"]) == "" {
		block["format"] = strings.TrimPrefix(strings.ToLower(mediaType), "image/")
	}
	if historyStringFromAny(block["uploadNarratorId"]) == "" && owner != "" {
		block["uploadNarratorId"] = owner
	}
	return true
}

func agentHistoryImagePath(owner, imageID string) (string, bool) {
	if !safePathSegment(owner) || !safePathSegment(imageID) {
		return "", false
	}
	dir := filepath.Join(narratorUploadsRoot(), owner)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), imageID) {
			continue
		}
		return filepath.Join(dir, entry.Name()), true
	}
	return "", false
}

func messageContentBlocks(content any) []map[string]any {
	switch items := content.(type) {
	case []map[string]any:
		return items
	case []any:
		blocks := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if block, ok := item.(map[string]any); ok {
				blocks = append(blocks, block)
			}
		}
		return blocks
	default:
		return nil
	}
}

func messageTextForAgent(msg Message, blocks []map[string]any) string {
	parts := []string{}
	for _, block := range blocks {
		switch historyStringFromAny(block["type"]) {
		case "text":
			if text := historyStringFromAny(block["text"]); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		case "reasoning":
			if msg.Role == "assistant" {
				if text := historyStringFromAny(block["text"]); strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	text := ""
	if len(parts) > 0 {
		text = strings.Join(parts, "\n")
	}
	if msg.ContentText != nil {
		contentText := *msg.ContentText
		if msg.Role == "user" && hasTextFileBlock(blocks) && strings.Contains(contentText, "<attached_files>") {
			return contentText
		}
		if text == "" {
			return contentText
		}
	}
	return text
}

func toolCallsForAgent(msg Message, blocks []map[string]any) []agentbasic.ToolCall {
	calls := []agentbasic.ToolCall{}
	seen := map[string]bool{}
	for _, block := range blocks {
		if historyStringFromAny(block["type"]) != "tool_use" {
			continue
		}
		id := historyStringFromAny(block["id"])
		name := historyStringFromAny(block["name"])
		if strings.TrimSpace(id) == "" || strings.TrimSpace(name) == "" || seen[id] {
			continue
		}
		calls = append(calls, newAgentToolCall(id, name, jsonArgString(block["input"])))
		seen[id] = true
	}
	for _, record := range msg.ToolCalls {
		if !completedToolStatus(record.Status) || strings.TrimSpace(record.ToolUseID) == "" || seen[record.ToolUseID] {
			continue
		}
		calls = append(calls, newAgentToolCall(record.ToolUseID, record.ToolName, jsonArgString(record.InputJSON)))
		seen[record.ToolUseID] = true
	}
	return calls
}

func agentUserBlocksForProvider(blocks []map[string]any, effectiveText string) []map[string]any {
	if !strings.Contains(effectiveText, "<attached_files>") {
		return blocks
	}
	out := make([]map[string]any, 0, len(blocks)+1)
	replacedText := false
	for _, block := range blocks {
		if historyStringFromAny(block["type"]) != "text" {
			out = append(out, block)
			continue
		}
		if replacedText {
			continue
		}
		updated := map[string]any{}
		for key, value := range block {
			updated[key] = value
		}
		updated["text"] = effectiveText
		out = append(out, updated)
		replacedText = true
	}
	if !replacedText {
		out = append(out, map[string]any{"type": "text", "text": effectiveText})
	}
	return out
}

func hasTextFileBlock(blocks []map[string]any) bool {
	for _, block := range blocks {
		if historyStringFromAny(block["type"]) == "text_file" {
			return true
		}
	}
	return false
}

func nonToolResultBlocks(blocks []map[string]any) []map[string]any {
	filtered := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		if historyStringFromAny(block["type"]) != "tool_result" {
			filtered = append(filtered, block)
		}
	}
	return filtered
}

func toolResultMessagesFromToolCalls(records []ToolCallRecord) []agentbasic.Message {
	messages := []agentbasic.Message{}
	for _, record := range records {
		if !completedToolStatus(record.Status) || strings.TrimSpace(record.ToolUseID) == "" {
			continue
		}
		content := appendSideCarsForAgent(outputTextForAgent(record.OutputJSON), sideCarsForToolResult(record.SideCars, record.ToolUseID))
		messages = append(messages, agentbasic.Message{Role: "tool", ToolCallID: record.ToolUseID, Content: content})
	}
	return messages
}

func toolResultMessagesFromBlocks(blocks []map[string]any) []agentbasic.Message {
	messages := []agentbasic.Message{}
	for _, block := range blocks {
		if historyStringFromAny(block["type"]) != "tool_result" {
			continue
		}
		toolUseID := historyFirstNonEmptyString(historyStringFromAny(block["tool_use_id"]), historyStringFromAny(block["toolUseId"]), historyStringFromAny(block["id"]))
		if strings.TrimSpace(toolUseID) == "" {
			continue
		}
		messages = append(messages, agentbasic.Message{Role: "tool", ToolCallID: toolUseID, Content: outputTextForAgent(firstNonNil(block["content"], block["text"]))})
	}
	return messages
}

func sideCarsForToolResult(sideCars []SideCarRecord, toolUseID string) []SideCarRecord {
	filtered := make([]SideCarRecord, 0, len(sideCars))
	for _, sideCar := range sideCars {
		if sideCar.Target != "tool_result" {
			continue
		}
		if sideCar.ToolUseID != nil && strings.TrimSpace(*sideCar.ToolUseID) != "" && strings.TrimSpace(*sideCar.ToolUseID) != toolUseID {
			continue
		}
		filtered = append(filtered, sideCar)
	}
	sortSideCars(filtered)
	return filtered
}

func sideCarsForUserMessage(sideCars []SideCarRecord) []SideCarRecord {
	filtered := make([]SideCarRecord, 0, len(sideCars))
	for _, sideCar := range sideCars {
		if sideCar.Target == "user_message" {
			filtered = append(filtered, sideCar)
		}
	}
	sortSideCars(filtered)
	return filtered
}

func appendSideCarsForAgent(baseText string, sideCars []SideCarRecord) string {
	sideCarText := formatSideCarsForAgent(sideCars)
	if sideCarText == "" {
		return baseText
	}
	if strings.TrimSpace(baseText) == "" {
		return sideCarText
	}
	return baseText + "\n\n" + sideCarText
}

func formatSideCarsForAgent(sideCars []SideCarRecord) string {
	formatted := []string{}
	sortSideCars(sideCars)
	for _, sideCar := range sideCars {
		if strings.TrimSpace(sideCar.Content) == "" {
			continue
		}
		source := sideCar.Source
		if strings.TrimSpace(source) == "" {
			source = "sidecar"
		}
		formatted = append(formatted, fmt.Sprintf("<side_car source=\"%s\">\n%s\n</side_car>", escapeSideCarAttr(source), sideCar.Content))
	}
	return strings.Join(formatted, "\n\n")
}

func sortSideCars(sideCars []SideCarRecord) {
	sort.SliceStable(sideCars, func(i, j int) bool {
		if sideCars[i].OrderIndex != sideCars[j].OrderIndex {
			return sideCars[i].OrderIndex < sideCars[j].OrderIndex
		}
		return sideCars[i].CreatedAt < sideCars[j].CreatedAt
	})
}

func escapeSideCarAttr(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "\"", "&quot;", "<", "&lt;")
	return replacer.Replace(value)
}

func completedToolStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "fail", "error", "completed":
		return true
	default:
		return false
	}
}

func newAgentToolCall(id, name, args string) agentbasic.ToolCall {
	call := agentbasic.ToolCall{ID: id, Type: "function"}
	call.Function.Name = name
	call.Function.Arguments = args
	return call
}

func jsonArgString(value any) string {
	if value == nil {
		return "{}"
	}
	if text, ok := value.(string); ok {
		if strings.TrimSpace(text) == "" {
			return "{}"
		}
		return text
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 {
		return "{}"
	}
	return string(data)
}

func outputTextForAgent(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case map[string]any:
		for _, key := range []string{"output", "content", "text", "error"} {
			if text := historyStringFromAny(typed[key]); text != "" {
				return text
			}
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func historyStringFromAny(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func historyFirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
