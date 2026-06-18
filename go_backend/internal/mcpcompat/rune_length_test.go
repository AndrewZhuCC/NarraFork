package mcpcompat

import (
	"strings"
	"testing"
)

// TestMCPStringValidationUsesJSStringLength pins MCP server-config string length
// validation to JavaScript String.prototype.length (UTF-16 code units), matching
// the TS MCP server schema in server/routes/mcp.ts (command/cwd/args items
// .max(500), url/env values .max(2000), env keys .max(200)). Two divergences are
// covered: byte-vs-character (500 CJK = 1500 bytes but 500 UTF-16 units, must
// pass) and rune-vs-UTF-16 (an emoji is 1 rune but 2 UTF-16 units, so 251 emoji =
// 502 units must be rejected even though a rune-count check would see only 251).
func TestMCPStringValidationUsesJSStringLength(t *testing.T) {
	t.Run("validateOptionalString UTF-16 limit", func(t *testing.T) {
		cmd500 := strings.Repeat("令", 500)
		if _, err := validateOptionalString(cmd500, "command", 500, false); err != nil {
			t.Fatalf("500 CJK-char command should pass: %v", err)
		}
		cmd501 := strings.Repeat("令", 501)
		if _, err := validateOptionalString(cmd501, "command", 500, false); err == nil {
			t.Fatal("501 CJK-char command should be rejected")
		}
		// 250 emoji = 500 UTF-16 units pass; 251 emoji = 502 units reject.
		if _, err := validateOptionalString(strings.Repeat("😀", 250), "command", 500, false); err != nil {
			t.Fatalf("250-emoji command (500 UTF-16 units) should pass: %v", err)
		}
		if _, err := validateOptionalString(strings.Repeat("😀", 251), "command", 500, false); err == nil {
			t.Fatal("251-emoji command (502 UTF-16 units) should be rejected")
		}
	})

	t.Run("stringArrayValueStrict item UTF-16 limit", func(t *testing.T) {
		arg500 := strings.Repeat("参", 500)
		if _, err := stringArrayValueStrict([]any{arg500}, "args", 50, 500); err != nil {
			t.Fatalf("500 CJK-char arg should pass: %v", err)
		}
		arg501 := strings.Repeat("参", 501)
		if _, err := stringArrayValueStrict([]any{arg501}, "args", 50, 500); err == nil {
			t.Fatal("501 CJK-char arg should be rejected")
		}
		// 251 emoji = 502 UTF-16 units reject.
		if _, err := stringArrayValueStrict([]any{strings.Repeat("😀", 251)}, "args", 50, 500); err == nil {
			t.Fatal("251-emoji arg (502 UTF-16 units) should be rejected")
		}
	})

	t.Run("stringObjectValueStrict key and value UTF-16 limits", func(t *testing.T) {
		key200 := strings.Repeat("键", 200)
		val2000 := strings.Repeat("值", 2000)
		if _, err := stringObjectValueStrict(map[string]any{key200: val2000}, "env", 200, 2000); err != nil {
			t.Fatalf("200-char key / 2000-char value should pass: %v", err)
		}
		key201 := strings.Repeat("键", 201)
		if _, err := stringObjectValueStrict(map[string]any{key201: "v"}, "env", 200, 2000); err == nil {
			t.Fatal("201 CJK-char env key should be rejected")
		}
		val2001 := strings.Repeat("值", 2001)
		if _, err := stringObjectValueStrict(map[string]any{"k": val2001}, "env", 200, 2000); err == nil {
			t.Fatal("2001 CJK-char env value should be rejected")
		}
		// 101 emoji = 202 UTF-16 units exceeds the 200-char key limit.
		if _, err := stringObjectValueStrict(map[string]any{strings.Repeat("😀", 101): "v"}, "env", 200, 2000); err == nil {
			t.Fatal("101-emoji env key (202 UTF-16 units) should be rejected")
		}
		// 1001 emoji = 2002 UTF-16 units exceeds the 2000-char value limit.
		if _, err := stringObjectValueStrict(map[string]any{"k": strings.Repeat("😀", 1001)}, "env", 200, 2000); err == nil {
			t.Fatal("1001-emoji env value (2002 UTF-16 units) should be rejected")
		}
	})

	// toolPermission toolName is z.string().min(1).max(200) in TS
	// (mcpToolPermissionInputSchema / mcpToolPermissionPatchSchema). Go previously
	// used byte length here, wrongly rejecting a <=200-character non-ASCII name.
	t.Run("normalizeToolPermissions toolName UTF-16 limit", func(t *testing.T) {
		name200 := strings.Repeat("具", 200) // 200 UTF-16 units, 600 bytes
		if _, err := normalizeToolPermissions([]any{map[string]any{"toolName": name200, "behavior": "allow"}}); err != nil {
			t.Fatalf("200 CJK-char toolName should pass max(200): %v", err)
		}
		name201 := strings.Repeat("具", 201)
		if _, err := normalizeToolPermissions([]any{map[string]any{"toolName": name201, "behavior": "allow"}}); err == nil {
			t.Fatal("201 CJK-char toolName should be rejected")
		}
	})

	t.Run("applyToolPermissionPatch toolName UTF-16 limit", func(t *testing.T) {
		server := map[string]any{}
		name200 := strings.Repeat("具", 200)
		if err := applyToolPermissionPatch(server, map[string]any{"toolName": name200, "behavior": "allow"}); err != nil {
			t.Fatalf("200 CJK-char patch toolName should pass max(200): %v", err)
		}
		if err := applyToolPermissionPatch(map[string]any{}, map[string]any{"toolName": strings.Repeat("具", 201), "behavior": "allow"}); err == nil {
			t.Fatal("201 CJK-char patch toolName should be rejected")
		}
	})
}
