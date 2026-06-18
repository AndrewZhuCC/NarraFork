package narrators

import (
	"context"
	"testing"
)

// TestRecallSanitizeQueryMatchesTS pins recallSanitizeQuery to the TypeScript
// sanitizeQuery (server/services/search-service.ts):
// query.replace(/['"*(){}[\]^~@:;!&|,<>\\]/g, "").trim(). Special FTS/SQL
// characters are stripped (NOT replaced with spaces) and the result is trimmed.
func TestRecallSanitizeQueryMatchesTS(t *testing.T) {
	cases := map[string]string{
		"hello":             "hello",
		"  hello  ":         "hello",
		"foo bar":           "foo bar",
		"foo*bar":           "foobar",
		"a&b|c":             "abc",
		"(group)":           "group",
		"[brackets]":        "brackets",
		"a'b\"c":            "abc",
		"x^y~z@n:m;k!l,p":   "xyznmklp",
		"<tag>":             "tag",
		"back\\slash":       "backslash",
		"{}()[]^~@:;!&|,<>": "",
		"***":               "",
		"  &|  ":            "",
		"":                  "",
		"café 文字":           "café 文字",
	}
	for in, want := range cases {
		if got := recallSanitizeQuery(in); got != want {
			t.Fatalf("recallSanitizeQuery(%q)=%q want %q", in, got, want)
		}
	}
}

// TestRecallSearchEmptyAfterSanitization verifies that a query consisting only
// of special characters is reported as empty after sanitization (matching TS
// handleSearch), rather than being run as a literal LIKE search.
func TestRecallSearchEmptyAfterSanitization(t *testing.T) {
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	ctx := context.Background()
	out, status, err := service.executeRecallTool(ctx, narratorID, map[string]any{"action": "search", "query": "&|()*"})
	if err != nil {
		t.Fatalf("recall search err: %v", err)
	}
	if status != "" {
		t.Fatalf("status = %q, want empty (non-error single-query path)", status)
	}
	if out != "Query is empty after sanitization." {
		t.Fatalf("out = %q, want empty-after-sanitization message", out)
	}
}
