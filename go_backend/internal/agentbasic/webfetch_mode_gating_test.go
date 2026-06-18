package agentbasic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWebFetchToolGatesUnsupportedModesAndParams locks the documented runtime
// boundary after readability + dom modes were implemented (capability
// webFetch.supportedModes = readability/raw/dom). The still-unimplemented TS
// modes smart/screenshot return an explicit unsupported message rather than
// silently fetching or crashing; the purpose param (smart-only) is rejected;
// selector requires dom mode. raw fetches the raw page, and an absent mode now
// defaults to readability (matching the TS default).
func TestWebFetchToolGatesUnsupportedModesAndParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><article><p>hello world</p></article></body></html>"))
	}))
	defer server.Close()

	for _, mode := range []string{"smart", "screenshot"} {
		out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
			`{"url":"`+server.URL+`","mode":"`+mode+`"}`)
		if !strings.Contains(out, "unsupported WebFetch mode") || !strings.Contains(out, "readability (default), raw, dom") {
			t.Fatalf("mode %q should return the unsupported message, got: %s", mode, out)
		}
		// The explicit fallback must not leak the fetched page body.
		if strings.Contains(out, "hello world") {
			t.Fatalf("mode %q fallback must not fetch the page body, got: %s", mode, out)
		}
	}

	// selector without dom mode -> explicit gate message (not a fetch).
	selectorOut := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
		`{"url":"`+server.URL+`","selector":"p","mode":"readability"}`)
	if !strings.Contains(selectorOut, "selector requires dom mode") {
		t.Fatalf("selector outside dom mode should be gated, got: %s", selectorOut)
	}
	// purpose (smart-only) -> explicit gate message.
	purposeOut := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
		`{"url":"`+server.URL+`","purpose":"find the title"}`)
	if !strings.Contains(purposeOut, "purpose requires smart mode") {
		t.Fatalf("purpose param should be gated, got: %s", purposeOut)
	}

	// Explicit mode "raw" (any case) returns the raw HTTP response with status.
	rawOut := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
		`{"url":"`+server.URL+`","mode":"RAW"}`)
	if !strings.Contains(rawOut, "Status: 200 OK") || !strings.Contains(rawOut, "hello world") {
		t.Fatalf("mode raw should fetch the raw page, got: %s", rawOut)
	}
	// Absent mode defaults to readability: extracted article text, no raw HTTP
	// status header and no HTML tags.
	defaultOut := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
		`{"url":"`+server.URL+`"}`)
	if !strings.Contains(defaultOut, "hello world") {
		t.Fatalf("default mode should extract article text, got: %s", defaultOut)
	}
	if strings.Contains(defaultOut, "Status: 200 OK") || strings.Contains(defaultOut, "<article>") {
		t.Fatalf("default mode should be readability (no raw status/tags), got: %s", defaultOut)
	}
}
