package agentbasic

import (
	"encoding/json"
	"strings"
	"testing"
)

// kiroToolInput decodes a parsed tool call's JSON arguments to a map for assertions.
func kiroToolInput(t *testing.T, call ToolCall) map[string]any {
	t.Helper()
	var input map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
		t.Fatalf("decode tool input %q: %v", call.Function.Arguments, err)
	}
	return input
}

// TestKiroParseTextToolCallsExtractsLeakedXML mirrors TS kiro-reasoning.test.ts:45:
// a leaked <invoke> block (with a "count" marker line and an &amp; entity) is
// extracted as a tool call, the XML and marker are stripped from the text, and the
// CJK prose plus the blank-line separator are preserved.
func TestKiroParseTextToolCallsExtractsLeakedXML(t *testing.T) {
	content := strings.Join([]string{
		"更新 packet staging 与 correction 初始化条目，反映坐标查表已闭合。",
		"",
		"count",
		`<invoke name="Edit">`,
		`<parameter name="file_path">D:/GoSLAM Mapping Master Pro/docs/example.md</parameter>`,
		`<parameter name="old_string">old value</parameter>`,
		"<parameter name=\"new_string\">`GeneralParser<LidarPointXYZIRT>` &amp; LUT</parameter>",
		"</invoke>",
	}, "\n")

	parsed := parseKiroEvent("assistantResponseEvent", map[string]any{"content": content}, true)
	if parsed.Text != "更新 packet staging 与 correction 初始化条目，反映坐标查表已闭合。\n\n" {
		t.Fatalf("unexpected stripped text: %q", parsed.Text)
	}
	if strings.Contains(parsed.Text, "<invoke") || strings.Contains(parsed.Text, "count") {
		t.Fatalf("text must not contain invoke/count marker: %q", parsed.Text)
	}
	if len(parsed.ToolUses) != 1 {
		t.Fatalf("expected 1 extracted tool use, got %d", len(parsed.ToolUses))
	}
	tu := parsed.ToolUses[0]
	if !strings.HasPrefix(tu.ID, "kiro_xml_") {
		t.Fatalf("tool use id should start with kiro_xml_, got %q", tu.ID)
	}
	if tu.Function.Name != "Edit" {
		t.Fatalf("tool name = %q, want Edit", tu.Function.Name)
	}
	input := kiroToolInput(t, tu)
	if input["file_path"] != "D:/GoSLAM Mapping Master Pro/docs/example.md" || input["old_string"] != "old value" {
		t.Fatalf("unexpected input: %#v", input)
	}
	if input["new_string"] != "`GeneralParser<LidarPointXYZIRT>` & LUT" {
		t.Fatalf("new_string entity not decoded: %#v", input["new_string"])
	}
}

// TestKiroParseTextToolCallsStripsCallMarker mirrors TS:78 — a "call" marker line
// after a blank-line boundary is removed.
func TestKiroParseTextToolCallsStripsCallMarker(t *testing.T) {
	content := strings.Join([]string{"准备执行工具。", "", "call", `<invoke name="Bash">`, `<parameter name="command">pwd</parameter>`, "</invoke>"}, "\n")
	parsed := parseKiroEvent("assistantResponseEvent", map[string]any{"content": content}, true)
	if parsed.Text != "准备执行工具。\n\n" {
		t.Fatalf("unexpected text: %q", parsed.Text)
	}
	if strings.Contains(parsed.Text, "call") {
		t.Fatalf("call marker should be stripped: %q", parsed.Text)
	}
	if len(parsed.ToolUses) != 1 || parsed.ToolUses[0].Function.Name != "Bash" {
		t.Fatalf("expected Bash tool use, got %#v", parsed.ToolUses)
	}
	if kiroToolInput(t, parsed.ToolUses[0])["command"] != "pwd" {
		t.Fatalf("unexpected command input")
	}
}

// TestKiroParseTextToolCallsKeepsNonBlankShortText mirrors TS:99 — short text NOT
// preceded by a blank-line boundary ("ok" on its own line) is preserved.
func TestKiroParseTextToolCallsKeepsNonBlankShortText(t *testing.T) {
	content := strings.Join([]string{"准备执行工具。", "ok", `<invoke name="Bash">`, `<parameter name="command">pwd</parameter>`, "</invoke>"}, "\n")
	parsed := parseKiroEvent("assistantResponseEvent", map[string]any{"content": content}, true)
	if parsed.Text != "准备执行工具。\nok\n" {
		t.Fatalf("non-blank short text should be kept: %q", parsed.Text)
	}
	if len(parsed.ToolUses) != 1 || parsed.ToolUses[0].Function.Name != "Bash" {
		t.Fatalf("expected Bash tool use, got %#v", parsed.ToolUses)
	}
}

// TestKiroParseTextToolCallsKeepsLegitProse mirrors TS:196 — legitimate short prose
// directly before an invoke block (no blank-line boundary) is preserved.
func TestKiroParseTextToolCallsKeepsLegitProse(t *testing.T) {
	content := strings.Join([]string{"运行测试", `<invoke name="Bash">`, `<parameter name="command">bun test</parameter>`, "</invoke>"}, "\n")
	parsed := parseKiroEvent("assistantResponseEvent", map[string]any{"content": content}, true)
	if parsed.Text != "运行测试\n" {
		t.Fatalf("legit prose should be kept: %q", parsed.Text)
	}
	if len(parsed.ToolUses) != 1 || parsed.ToolUses[0].Function.Name != "Bash" {
		t.Fatalf("expected Bash tool use, got %#v", parsed.ToolUses)
	}
}

// TestKiroCoerceXMLParameterScalars mirrors TS:176 — known number/boolean params
// are coerced to typed JSON values; unknown params stay strings.
func TestKiroCoerceXMLParameterScalars(t *testing.T) {
	content := strings.Join([]string{`<invoke name="Bash">`, `<parameter name="command">pwd</parameter>`, `<parameter name="timeout">1000</parameter>`, `<parameter name="run_in_background">true</parameter>`, "</invoke>"}, "\n")
	parsed := parseKiroEvent("assistantResponseEvent", map[string]any{"content": content}, true)
	if len(parsed.ToolUses) != 1 {
		t.Fatalf("expected 1 tool use, got %d", len(parsed.ToolUses))
	}
	input := kiroToolInput(t, parsed.ToolUses[0])
	if input["command"] != "pwd" {
		t.Fatalf("command = %#v, want string pwd", input["command"])
	}
	if input["timeout"] != float64(1000) {
		t.Fatalf("timeout = %#v (%T), want number 1000", input["timeout"], input["timeout"])
	}
	if input["run_in_background"] != true {
		t.Fatalf("run_in_background = %#v, want bool true", input["run_in_background"])
	}
}

// TestParseKiroEventDedupAgainstNativeToolUse mirrors TS:124 — a leaked XML tool
// call identical to a native tool use is deduped (only the native one remains).
func TestParseKiroEventDedupAgainstNativeToolUse(t *testing.T) {
	content := `<invoke name="Bash"><parameter name="command">pwd</parameter></invoke>`
	parsed := parseKiroEvent("assistantResponseEvent", map[string]any{
		"content":  content,
		"toolUses": []any{map[string]any{"toolUseId": "native_1", "name": "Bash", "input": map[string]any{"command": "pwd"}}},
	}, true)
	if strings.TrimSpace(parsed.Text) != "" {
		t.Fatalf("text should be empty after stripping XML, got %q", parsed.Text)
	}
	if len(parsed.ToolUses) != 1 || parsed.ToolUses[0].ID != "native_1" {
		t.Fatalf("expected only the native tool use, got %#v", parsed.ToolUses)
	}
}

// TestParseKiroEventKeepsDistinctXMLAlongsideNative mirrors TS:142 — a distinct
// leaked XML tool call is kept alongside a native tool use (native first).
func TestParseKiroEventKeepsDistinctXMLAlongsideNative(t *testing.T) {
	content := strings.Join([]string{`<invoke name="Edit">`, `<parameter name="file_path">/tmp/a.txt</parameter>`, `<parameter name="old_string">a</parameter>`, `<parameter name="new_string">b</parameter>`, "</invoke>"}, "\n")
	parsed := parseKiroEvent("assistantResponseEvent", map[string]any{
		"content":  content,
		"toolUses": []any{map[string]any{"toolUseId": "native_1", "name": "Bash", "input": map[string]any{"command": "pwd"}}},
	}, true)
	if len(parsed.ToolUses) != 2 {
		t.Fatalf("expected 2 tool uses, got %d", len(parsed.ToolUses))
	}
	if parsed.ToolUses[0].ID != "native_1" || parsed.ToolUses[0].Function.Name != "Bash" {
		t.Fatalf("native tool use should be first, got %#v", parsed.ToolUses[0])
	}
	if parsed.ToolUses[1].Function.Name != "Edit" {
		t.Fatalf("second tool use should be Edit, got %#v", parsed.ToolUses[1])
	}
	input := kiroToolInput(t, parsed.ToolUses[1])
	if input["file_path"] != "/tmp/a.txt" || input["old_string"] != "a" || input["new_string"] != "b" {
		t.Fatalf("unexpected Edit input: %#v", input)
	}
}

// TestParseKiroEventKeepsXMLVerbatimWhenToolsDisabled mirrors TS:117 — with
// parseTextToolCalls disabled (no tools / plain generation), leaked XML stays
// verbatim in the text and no tool use is extracted.
func TestParseKiroEventKeepsXMLVerbatimWhenToolsDisabled(t *testing.T) {
	content := `<invoke name="Bash"><parameter name="command">pwd</parameter></invoke>`
	parsed := parseKiroEvent("assistantResponseEvent", map[string]any{"content": content}, false)
	if parsed.Text != content {
		t.Fatalf("text should be verbatim when tools disabled, got %q", parsed.Text)
	}
	if len(parsed.ToolUses) != 0 {
		t.Fatalf("no tool uses expected when tools disabled, got %#v", parsed.ToolUses)
	}
}

// TestKiroRequestHasTools verifies the plugin-bridge gating helper navigates the
// request body to detect declared tools.
func TestKiroRequestHasTools(t *testing.T) {
	withTools := map[string]any{"conversationState": map[string]any{"currentMessage": map[string]any{"userInputMessage": map[string]any{"userInputMessageContext": map[string]any{"tools": []any{map[string]any{"name": "Bash"}}}}}}}
	if !kiroRequestHasTools(withTools) {
		t.Fatal("expected hasTools=true when tools declared")
	}
	withoutTools := map[string]any{"conversationState": map[string]any{"currentMessage": map[string]any{"userInputMessage": map[string]any{"userInputMessageContext": map[string]any{"tools": []any{}}}}}}
	if kiroRequestHasTools(withoutTools) {
		t.Fatal("expected hasTools=false when tools empty")
	}
	if kiroRequestHasTools(map[string]any{}) {
		t.Fatal("expected hasTools=false for empty request")
	}
}
