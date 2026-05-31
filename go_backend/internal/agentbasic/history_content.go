package agentbasic

import (
	"encoding/json"
	"strings"
)

func messageTextFromBlocks(blocks []map[string]any, fallback string) string {
	parts := []string{}
	for _, block := range blocks {
		switch stringValue(block["type"]) {
		case "text":
			if text := stringValue(block["text"]); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		case "reasoning":
			// Providers that cannot replay native reasoning blocks still need a safe,
			// lossy text fallback rather than dropping the only assistant content.
			if text := stringValue(block["text"]); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return fallback
}

func toolCallsFromBlocks(blocks []map[string]any, fallback []ToolCall) []ToolCall {
	calls := []ToolCall{}
	seen := map[string]bool{}
	for _, block := range blocks {
		if stringValue(block["type"]) != "tool_use" {
			continue
		}
		id := stringValue(block["id"])
		name := stringValue(block["name"])
		if strings.TrimSpace(id) == "" || strings.TrimSpace(name) == "" || seen[id] {
			continue
		}
		calls = append(calls, newFunctionToolCall(id, name, jsonStringValue(block["input"])))
		seen[id] = true
	}
	for _, call := range fallback {
		if strings.TrimSpace(call.ID) == "" || seen[call.ID] {
			continue
		}
		calls = append(calls, call)
		seen[call.ID] = true
	}
	return calls
}

func jsonStringValue(value any) string {
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

func webSearchContextFromBlocks(blocks []map[string]any) string {
	parts := []string{}
	for _, block := range blocks {
		if stringValue(block["type"]) != "web_search" {
			continue
		}
		parts = append(parts, webSearchSummary(block))
	}
	return strings.Join(parts, "\n")
}

func webSearchSummary(block map[string]any) string {
	if action, ok := block["action"].(map[string]any); ok {
		switch stringValue(action["type"]) {
		case "open_page":
			if url := stringValue(action["url"]); url != "" {
				return "[Web search: opened " + url + "]"
			}
		case "find_in_page":
			parts := []string{}
			if pattern := stringValue(action["pattern"]); pattern != "" {
				parts = append(parts, "'"+pattern+"'")
			}
			if url := stringValue(action["url"]); url != "" {
				parts = append(parts, url)
			}
			if len(parts) > 0 {
				return "[Web search: find " + strings.Join(parts, " in ") + "]"
			}
		}
		if query := stringValue(action["query"]); query != "" {
			return "[Web search: " + query + "]"
		}
		if queries := stringSliceValue(action["queries"]); len(queries) > 0 {
			return "[Web search: " + strings.Join(queries, ", ") + "]"
		}
	}
	if query := stringValue(block["query"]); query != "" {
		return "[Web search: " + query + "]"
	}
	if queries := stringSliceValue(block["queries"]); len(queries) > 0 {
		return "[Web search: " + strings.Join(queries, ", ") + "]"
	}
	return "[Web search: unknown]"
}

func webSearchActionFromBlock(block map[string]any) (map[string]any, bool) {
	if action, ok := block["action"].(map[string]any); ok && len(action) > 0 {
		return action, true
	}
	query := stringValue(block["query"])
	queries := stringSliceValue(block["queries"])
	if query == "" && len(queries) == 0 {
		return nil, false
	}
	action := map[string]any{"type": "search"}
	if query != "" {
		action["query"] = query
	}
	if len(queries) > 0 {
		action["queries"] = queries
	}
	return action, true
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		items := []string{}
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				items = append(items, text)
			}
		}
		return items
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	default:
		return nil
	}
}

func imageBlockData(block map[string]any) (string, string, bool) {
	mediaType := firstNonEmpty(stringValue(block["mediaType"]), stringValue(block["media_type"]))
	data := firstNonEmpty(stringValue(block["base64"]), stringValue(block["data"]))
	if source, ok := block["source"].(map[string]any); ok {
		mediaType = firstNonEmpty(mediaType, stringValue(source["media_type"]), stringValue(source["mediaType"]))
		data = firstNonEmpty(data, stringValue(source["data"]), stringValue(source["base64"]))
	}
	if strings.HasPrefix(data, "data:") {
		if idx := strings.Index(data, ";base64,"); idx > len("data:") {
			mediaType = firstNonEmpty(mediaType, data[len("data:"):idx])
			data = data[idx+len(";base64,"):]
		}
	}
	format := stringValue(block["format"])
	if mediaType == "" && format != "" {
		mediaType = "image/" + strings.TrimPrefix(format, "image/")
	}
	mediaType = strings.TrimSpace(mediaType)
	data = strings.TrimSpace(data)
	if mediaType == "" || data == "" {
		return "", "", false
	}
	return mediaType, data, true
}

func imageBlockDataURL(block map[string]any) (string, bool) {
	mediaType, data, ok := imageBlockData(block)
	if !ok {
		return "", false
	}
	return "data:" + mediaType + ";base64," + data, true
}

func contentBlockToolResults(blocks []map[string]any) []Message {
	messages := []Message{}
	for _, block := range blocks {
		if stringValue(block["type"]) != "tool_result" {
			continue
		}
		toolUseID := firstNonEmpty(stringValue(block["tool_use_id"]), stringValue(block["toolUseId"]), stringValue(block["id"]))
		if strings.TrimSpace(toolUseID) == "" {
			continue
		}
		messages = append(messages, Message{Role: "tool", ToolCallID: toolUseID, Content: blockOutputText(block)})
	}
	return messages
}

func blockOutputText(block map[string]any) string {
	content := block["content"]
	if text := stringValue(content); text != "" {
		return text
	}
	if text := stringValue(block["text"]); text != "" {
		return text
	}
	if content == nil {
		return ""
	}
	data, err := json.Marshal(content)
	if err != nil {
		return ""
	}
	return string(data)
}

func anthropicContentFromBlocks(blocks []map[string]any, fallbackText string, fallbackToolCalls []ToolCall) []any {
	parts := []any{}
	seenToolUses := map[string]bool{}
	for _, block := range blocks {
		switch stringValue(block["type"]) {
		case "thinking":
			thinking := stringValue(block["thinking"])
			if thinking != "" {
				parts = append(parts, map[string]any{"type": "thinking", "thinking": thinking, "signature": stringValue(block["signature"])})
			}
		case "reasoning":
			text := stringValue(block["text"])
			if text == "" {
				continue
			}
			part := map[string]any{"type": "thinking", "thinking": text}
			if sig := anthropicSignature(block["providerMetadata"]); sig != "" {
				part["signature"] = sig
			} else {
				part["signature"] = ""
			}
			parts = append(parts, part)
		case "redacted_thinking":
			if data := firstNonEmpty(stringValue(block["data"]), stringValue(block["text"])); data != "" {
				parts = append(parts, map[string]any{"type": "redacted_thinking", "data": data})
			}
		case "text":
			if text := stringValue(block["text"]); text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
		case "image":
			mediaType, data, ok := imageBlockData(block)
			if ok {
				parts = append(parts, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": mediaType,
						"data":       data,
					},
				})
			}
		case "tool_use":
			id := stringValue(block["id"])
			name := stringValue(block["name"])
			if id == "" || name == "" {
				continue
			}
			parts = append(parts, map[string]any{"type": "tool_use", "id": id, "name": name, "input": jsonObjectValue(block["input"])})
			seenToolUses[id] = true
		case "tool_result":
			toolUseID := firstNonEmpty(stringValue(block["tool_use_id"]), stringValue(block["toolUseId"]), stringValue(block["id"]))
			if toolUseID != "" {
				part := map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": blockOutputText(block)}
				if boolValue(block["is_error"]) || boolValue(block["isError"]) {
					part["is_error"] = true
				}
				parts = append(parts, part)
			}
		}
	}
	for _, call := range fallbackToolCalls {
		if call.ID == "" || seenToolUses[call.ID] {
			continue
		}
		parts = append(parts, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": jsonObjectValue(call.Function.Arguments)})
	}
	if len(parts) == 0 && strings.TrimSpace(fallbackText) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": fallbackText})
	}
	return parts
}

func anthropicSignature(metadata any) string {
	outer, ok := metadata.(map[string]any)
	if !ok {
		return ""
	}
	anthropic, ok := outer["anthropic"].(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(anthropic["signature"])
}

func jsonObjectValue(value any) any {
	if value == nil {
		return map[string]any{}
	}
	if text, ok := value.(string); ok {
		var decoded any
		if err := json.Unmarshal([]byte(text), &decoded); err == nil && decoded != nil {
			return decoded
		}
		return map[string]any{}
	}
	return value
}
