package ws

import (
	"strings"
	"testing"
)

// TestToolCompletedOutputTruncatesLikeTS locks the tool_completed WS broadcast
// output against TS parity: narrator-event-handler.ts:1198 sends
// output: truncateJson(event.output, 2000) (truncateJson defined in
// narrator-messages.ts:436-441). Go previously forwarded base["output"]
// verbatim (the full, up to ~50KB tool output), so the frontend received a
// different shape/size than TS for outputs longer than 2000 characters.
func TestToolCompletedOutputTruncatesLikeTS(t *testing.T) {
	// Short output (<= 2000 UTF-16 units) passes through unchanged as a string.
	t.Run("short output unchanged", func(t *testing.T) {
		payloads := narratorBroadcastPayloads("nar-1", map[string]any{
			"type":      "tool_result",
			"toolUseId": "tool-1",
			"toolName":  "Read",
			"output":    "file contents",
		})
		completed := payloadByType(t, payloads, "tool_completed")
		if completed["output"] != "file contents" {
			t.Fatalf("short output should pass through unchanged, got %#v", completed["output"])
		}
	})

	// Long output (> 2000 units) becomes {_truncated, preview, fullLength}.
	t.Run("long output truncated to object", func(t *testing.T) {
		long := strings.Repeat("a", 5000)
		payloads := narratorBroadcastPayloads("nar-1", map[string]any{
			"type":      "tool_result",
			"toolUseId": "tool-1",
			"toolName":  "Bash",
			"output":    long,
		})
		completed := payloadByType(t, payloads, "tool_completed")
		obj, ok := completed["output"].(map[string]any)
		if !ok {
			t.Fatalf("long output should be a truncation object, got %T %#v", completed["output"], completed["output"])
		}
		if obj["_truncated"] != true {
			t.Fatalf("_truncated should be true, got %#v", obj["_truncated"])
		}
		if obj["fullLength"] != 5000 {
			t.Fatalf("fullLength should be 5000, got %#v", obj["fullLength"])
		}
		preview, _ := obj["preview"].(string)
		if len([]rune(preview)) != 2000 {
			t.Fatalf("preview should be 2000 chars, got %d", len([]rune(preview)))
		}
		if preview != long[:2000] {
			t.Fatalf("preview should be the first 2000 chars of the output")
		}
	})

	// Boundary: exactly 2000 units passes through unchanged.
	t.Run("exactly 2000 unchanged", func(t *testing.T) {
		exact := strings.Repeat("b", 2000)
		payloads := narratorBroadcastPayloads("nar-1", map[string]any{
			"type":      "tool_result",
			"toolUseId": "tool-1",
			"toolName":  "Read",
			"output":    exact,
		})
		completed := payloadByType(t, payloads, "tool_completed")
		if completed["output"] != exact {
			t.Fatalf("exactly 2000 chars should pass through unchanged, got %T", completed["output"])
		}
	})

	// Astral characters: preview is cut on UTF-16 code-unit boundary (each
	// emoji is 2 units), matching TS str.slice(0, 2000), not rune count.
	t.Run("astral chars cut on UTF-16 boundary", func(t *testing.T) {
		// 1500 emoji = 3000 UTF-16 units (each U+1F600 is a surrogate pair).
		emoji := strings.Repeat("\U0001F600", 1500)
		payloads := narratorBroadcastPayloads("nar-1", map[string]any{
			"type":      "tool_result",
			"toolUseId": "tool-1",
			"toolName":  "Bash",
			"output":    emoji,
		})
		completed := payloadByType(t, payloads, "tool_completed")
		obj, ok := completed["output"].(map[string]any)
		if !ok {
			t.Fatalf("3000-unit emoji output should be truncated, got %T", completed["output"])
		}
		if obj["fullLength"] != 3000 {
			t.Fatalf("fullLength should be 3000 UTF-16 units, got %#v", obj["fullLength"])
		}
		// preview = first 2000 units = first 1000 whole emoji (no lone surrogate).
		preview, _ := obj["preview"].(string)
		if preview != strings.Repeat("\U0001F600", 1000) {
			t.Fatalf("preview should be the first 1000 whole emoji (2000 UTF-16 units)")
		}
	})

	// Non-string output: JSON.stringify form is measured (mirrors TS).
	t.Run("non-string output stringified for length", func(t *testing.T) {
		// An object whose JSON is short stays unchanged (returned as the original value).
		short := map[string]any{"k": "v"}
		payloads := narratorBroadcastPayloads("nar-1", map[string]any{
			"type":      "tool_result",
			"toolUseId": "tool-1",
			"toolName":  "Read",
			"output":    short,
		})
		completed := payloadByType(t, payloads, "tool_completed")
		obj, ok := completed["output"].(map[string]any)
		if !ok || obj["_truncated"] == true {
			t.Fatalf("short object output should pass through unchanged, got %#v", completed["output"])
		}
		if obj["k"] != "v" {
			t.Fatalf("short object output should be the original value, got %#v", completed["output"])
		}
	})
}
