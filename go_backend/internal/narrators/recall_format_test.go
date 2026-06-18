package narrators

import (
	"database/sql"
	"strings"
	"testing"
)

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

// TestRecallMarshalIndentNoHTMLEscape verifies recallMarshalIndent matches
// JavaScript JSON.stringify(value, null, 2): 2-space indent, no trailing
// newline, and crucially NO HTML escaping of <, > and & (Go's default encoder
// escapes these to \u003c/\u003e/\u0026, which would diverge from the recall
// output the model sees for bash commands / HTML content).
func TestRecallMarshalIndentNoHTMLEscape(t *testing.T) {
	got, err := recallMarshalIndent(map[string]any{"command": "echo a && b > c <d>"})
	if err != nil {
		t.Fatalf("recallMarshalIndent err: %v", err)
	}
	if strings.Contains(got, "\\u003c") || strings.Contains(got, "\\u003e") || strings.Contains(got, "\\u0026") {
		t.Fatalf("expected literal <>& but got escaped: %q", got)
	}
	want := "{\n  \"command\": \"echo a && b > c <d>\"\n}"
	if got != want {
		t.Fatalf("recallMarshalIndent = %q, want %q", got, want)
	}
}

// TestRecallTruncateUTF16 verifies recallTruncate uses JavaScript String.length
// / slice (UTF-16 code units): an over-limit value keeps maxChars code units,
// appends the TS suffix, and reports the UTF-16 length, so CJK output matches
// the TS implementation rather than Go byte offsets.
func TestRecallTruncateUTF16(t *testing.T) {
	if got := recallTruncate("hello", 4000); got != "hello" {
		t.Fatalf("under-limit changed: %q", got)
	}
	if got := recallTruncate("hello", 0); got != "hello" {
		t.Fatalf("maxChars<=0 changed: %q", got)
	}
	ascii := strings.Repeat("a", 10)
	if got := recallTruncate(ascii, 4); got != "aaaa\n... (truncated, 10 chars total)" {
		t.Fatalf("ascii truncate = %q", got)
	}
	cjk := strings.Repeat("好", 10)
	want := strings.Repeat("好", 4) + "\n... (truncated, 10 chars total)"
	if got := recallTruncate(cjk, 4); got != want {
		t.Fatalf("cjk truncate = %q, want %q", got, want)
	}
}

// TestRecallParseJSONAndTruthy verifies recallParseJSON mirrors
// `raw ? JSON.parse(raw) : null` under try/catch, and recallTruthy mirrors JS
// truthiness used by `inputParsed ? ... : raw`.
func TestRecallParseJSONAndTruthy(t *testing.T) {
	if _, ok := recallParseJSON(sql.NullString{}); ok {
		t.Fatal("null raw should not parse")
	}
	if _, ok := recallParseJSON(ns("")); ok {
		t.Fatal("empty raw should not parse")
	}
	if _, ok := recallParseJSON(ns("not json")); ok {
		t.Fatal("malformed raw should not parse")
	}
	if v, ok := recallParseJSON(ns(`{"a":1}`)); !ok || v == nil {
		t.Fatalf("valid object should parse: %v %v", v, ok)
	}
	// JS truthiness.
	falsy := []any{nil, false, float64(0), ""}
	for _, f := range falsy {
		if recallTruthy(f) {
			t.Fatalf("expected falsy: %#v", f)
		}
	}
	truthy := []any{true, float64(1), "x", map[string]any{}, []any{}}
	for _, tr := range truthy {
		if !recallTruthy(tr) {
			t.Fatalf("expected truthy: %#v", tr)
		}
	}
}

// TestRecallFormatInput verifies recallFormatInput mirrors the TS Input render:
// truthy parsed JSON -> HTML-unescaped JSON.stringify; falsy/unparsed -> raw text
// (or "(none)" when NULL).
func TestRecallFormatInput(t *testing.T) {
	// Truthy object -> JSON, with literal <>&.
	got := recallFormatInput(ns(`{"command":"a && b"}`), map[string]any{"command": "a && b"}, true, 4000)
	if got != "{\n  \"command\": \"a && b\"\n}" {
		t.Fatalf("object input = %q", got)
	}
	// Malformed JSON -> parsedOK false -> raw text returned verbatim (NOT quoted).
	if got := recallFormatInput(ns("not json"), nil, false, 4000); got != "not json" {
		t.Fatalf("malformed input = %q, want raw verbatim", got)
	}
	// NULL raw + no parse -> "(none)".
	if got := recallFormatInput(sql.NullString{}, nil, false, 4000); got != "(none)" {
		t.Fatalf("null input = %q, want (none)", got)
	}
	// Parsed falsy (e.g. JSON null) -> fall back to raw text "null".
	if got := recallFormatInput(ns("null"), nil, true, 4000); got != "null" {
		t.Fatalf("falsy parsed input = %q, want raw null", got)
	}
}

// TestRecallFormatOutput verifies recallFormatOutput mirrors TS: _text string
// (including "") wins; otherwise truthy parsed object renders as JSON; falsy /
// unparsed falls back to raw (or "(none)"). It must NOT special-case "output".
func TestRecallFormatOutput(t *testing.T) {
	// _text used verbatim, no trim.
	if got := recallFormatOutput(ns(`{"_text":"  hi  "}`), 4000); got != "  hi  " {
		t.Fatalf("_text not verbatim: %q", got)
	}
	// _text empty string still wins.
	if got := recallFormatOutput(ns(`{"_text":"","output":"fallback"}`), 4000); got != "" {
		t.Fatalf("empty _text should win, got %q", got)
	}
	// No _text -> JSON render (does NOT use "output"). Keys present.
	got := recallFormatOutput(ns(`{"output":"x","a":1}`), 4000)
	if !strings.Contains(got, "\"output\": \"x\"") || !strings.Contains(got, "\"a\": 1") {
		t.Fatalf("expected JSON render, got %q", got)
	}
	// _text non-string (number) -> JSON render.
	if got := recallFormatOutput(ns(`{"_text":5}`), 4000); !strings.Contains(got, "\"_text\": 5") {
		t.Fatalf("non-string _text should render JSON, got %q", got)
	}
	// NULL raw -> "(none)".
	if got := recallFormatOutput(sql.NullString{}, 4000); got != "(none)" {
		t.Fatalf("null output = %q, want (none)", got)
	}
	// Malformed -> raw verbatim.
	if got := recallFormatOutput(ns("not json"), 4000); got != "not json" {
		t.Fatalf("malformed output = %q, want raw verbatim", got)
	}
}
