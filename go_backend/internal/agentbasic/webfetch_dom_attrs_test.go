package agentbasic

import (
	"context"
	"strings"
	"testing"
)

// TestWebFetchDomModeFiltersAttributes covers the dom-mode attribute allow-list
// (KEEP_ATTRS): preserved attributes like href/class/id/alt and aria-* survive,
// while non-allowed attributes (event handlers like onclick, inline style,
// data-* tracking) are stripped. This mirrors the TS sanitize-html KEEP_ATTRS
// contract and is security-relevant (event handlers must not leak through).
func TestWebFetchDomModeFiltersAttributes(t *testing.T) {
	dataURL := "data:text/html," + strings.NewReplacer(" ", "%20", `"`, "%22").Replace(
		`<html><body><a href="/go" class="link" id="x" onclick="evil()" style="color:red" data-track="123" aria-label="Go">Link</a></body></html>`)

	out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
		`{"url":"`+dataURL+`","mode":"dom"}`)

	// Allowed attributes survive.
	for _, kept := range []string{`href="/go"`, `class="link"`, `id="x"`, `aria-label="Go"`} {
		if !strings.Contains(out, kept) {
			t.Fatalf("dom should keep attribute %q, got: %s", kept, out)
		}
	}
	// Disallowed attributes are stripped.
	for _, dropped := range []string{"onclick", "evil()", "style=", "data-track"} {
		if strings.Contains(out, dropped) {
			t.Fatalf("dom should strip disallowed attribute %q, got: %s", dropped, out)
		}
	}
	// The link text content is preserved.
	if !strings.Contains(out, "Link") {
		t.Fatalf("dom should keep link text, got: %s", out)
	}
}
