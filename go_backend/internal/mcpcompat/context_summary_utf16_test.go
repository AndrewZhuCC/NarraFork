package mcpcompat

import (
	"strings"
	"testing"

	"narrafork-go/internal/narrators"
)

// TestNarratorContextSummaryTruncatesPerMessageUTF16 verifies the recent-message
// preview in the narrafork_get_context_summary MCP tool output truncates each
// message by UTF-16 code units (never cutting a multi-byte rune mid-sequence into
// invalid UTF-8). The surrounding summary format is a go_backend-specific
// deterministic builder, but this model-visible output must stay valid UTF-8.
func TestNarratorContextSummaryTruncatesPerMessageUTF16(t *testing.T) {
	model := "openai:test"
	bigText := strings.Repeat("章", 600) // 600 UTF-16 units, 1800 bytes
	msg := narrators.Message{Role: "user", ContentText: &bigText}
	n := narrators.Narrator{ID: "n1", Type: "primary", Status: "idle", Model: &model, MessageCount: 1}

	out := narratorContextSummaryText(n, []narrators.Message{msg})

	if strings.ContainsRune(out, 0xFFFD) {
		t.Fatalf("context summary produced invalid UTF-8 (U+FFFD)")
	}
	// The truncated preview keeps 500 CJK runes + "…", not a 500-byte cut (~166 runes).
	if !strings.Contains(out, strings.Repeat("章", 500)+"…") {
		t.Fatalf("expected per-message preview truncated to 500 UTF-16 units + …, got %q", out)
	}
	// It must NOT contain 501 consecutive CJK chars (i.e. it was truncated).
	if strings.Contains(out, strings.Repeat("章", 501)) {
		t.Fatalf("per-message preview was not truncated at 500 units")
	}
}
