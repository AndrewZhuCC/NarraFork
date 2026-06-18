package agentbasic

import (
	"reflect"
	"testing"
)

// TestStringListAndPathValue covers the tool-argument coercion helpers used to
// parse JSON tool calls. stringList decodes a []string or a JSON []any (trimming
// and dropping empty entries), returning nil for other types. pathValue trims a
// string value and yields "" for non-string inputs.
func TestStringListAndPathValue(t *testing.T) {
	// []any from decoded JSON: trim and drop empty/whitespace entries.
	got := stringList([]any{"  a.com ", "", "  ", "b.com"})
	if !reflect.DeepEqual(got, []string{"a.com", "b.com"}) {
		t.Fatalf("stringList([]any) = %#v", got)
	}
	// Native []string passes through unchanged.
	if got := stringList([]string{"x", "y"}); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("stringList([]string) = %#v", got)
	}
	// Non-list types yield nil.
	if got := stringList("not a list"); got != nil {
		t.Fatalf("stringList(string) should be nil, got %#v", got)
	}
	if got := stringList(nil); got != nil {
		t.Fatalf("stringList(nil) should be nil, got %#v", got)
	}
	// []any with non-string entries skips them.
	if got := stringList([]any{"keep", 42, true, "also"}); !reflect.DeepEqual(got, []string{"keep", "also"}) {
		t.Fatalf("stringList mixed = %#v", got)
	}

	// pathValue trims strings and rejects non-strings.
	if got := pathValue("  /tmp/file.txt  "); got != "/tmp/file.txt" {
		t.Fatalf("pathValue trim = %q", got)
	}
	if got := pathValue(123); got != "" {
		t.Fatalf("pathValue on non-string should be empty, got %q", got)
	}
}
