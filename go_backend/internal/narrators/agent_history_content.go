package narrators

import (
	"encoding/json"
	"fmt"
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
		return messages
	case "user":
		messages := toolResultMessagesFromBlocks(blocks)
		userBlocks := nonToolResultBlocks(blocks)
		if strings.TrimSpace(text) != "" || len(userBlocks) > 0 {
			messages = append(messages, agentbasic.Message{Role: "user", Content: text, ContentBlocks: userBlocks})
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

func messageContentBlocks(content any) []map[string]any {
	items, ok := content.([]any)
	if !ok {
		return nil
	}
	blocks := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if block, ok := item.(map[string]any); ok {
			blocks = append(blocks, block)
		}
	}
	return blocks
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
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	if msg.ContentText != nil {
		return *msg.ContentText
	}
	return ""
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
		messages = append(messages, agentbasic.Message{Role: "tool", ToolCallID: record.ToolUseID, Content: outputTextForAgent(record.OutputJSON)})
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
