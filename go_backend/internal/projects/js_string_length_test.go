package projects

import (
	"strings"
	"testing"
)

// TestValidateProjectUpdateFieldsUsesJSStringLength pins the project update field
// length validation to JavaScript String.prototype.length (UTF-16 code units),
// matching the TS updateProjectSchema (server/lib/validators/projects.ts:
// name .min(1).max(200), description .max(2000)). An emoji is 1 rune but 2 UTF-16
// units, so 101 emoji = 202 units must be rejected for name even though a
// rune-count check would see only 101 and wrongly accept it.
func TestValidateProjectUpdateFieldsUsesJSStringLength(t *testing.T) {
	// name: 200 CJK chars (200 UTF-16 units) pass; 201 rejected.
	if err := validateProjectUpdateFields(map[string]any{"name": strings.Repeat("项", 200)}); err != nil {
		t.Fatalf("200 CJK-char name should pass max(200): %v", err)
	}
	if err := validateProjectUpdateFields(map[string]any{"name": strings.Repeat("项", 201)}); err == nil {
		t.Fatal("201 CJK-char name should be rejected")
	}

	// name astral: 100 emoji = 200 units pass; 101 emoji = 202 units rejected.
	if err := validateProjectUpdateFields(map[string]any{"name": strings.Repeat("😀", 100)}); err != nil {
		t.Fatalf("100-emoji name (200 UTF-16 units) should pass max(200): %v", err)
	}
	if err := validateProjectUpdateFields(map[string]any{"name": strings.Repeat("😀", 101)}); err == nil {
		t.Fatal("101-emoji name (202 UTF-16 units) should be rejected by max(200)")
	}

	// description astral: 1001 emoji = 2002 units rejected; 1000 emoji = 2000 OK.
	if err := validateProjectUpdateFields(map[string]any{"description": strings.Repeat("😀", 1000)}); err != nil {
		t.Fatalf("1000-emoji description (2000 UTF-16 units) should pass max(2000): %v", err)
	}
	if err := validateProjectUpdateFields(map[string]any{"description": strings.Repeat("😀", 1001)}); err == nil {
		t.Fatal("1001-emoji description (2002 UTF-16 units) should be rejected by max(2000)")
	}
}
