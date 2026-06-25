package agentbasic

import (
	"context"
	"strings"
	"testing"
)

// TestWebFetchModeDescriptionReflectsActualDispatch pins the model-facing
// WebFetch `mode` schema description to the actual Go dispatch (executeTool's
// WebFetch branch): readability is the DEFAULT and is supported, raw and dom are
// supported, only smart/screenshot are rejected. The description was previously
// stale ("Go runtime supports raw (default) and dom ... readability, smart, and
// screenshot are rejected explicitly"), which actively misled the model into
// believing readability — the actual default — was rejected and raw was the
// default. The capability reason (tool_inventory.go) already said "readability
// (default)", so the schema description contradicted both dispatch and capability.
func TestWebFetchModeDescriptionReflectsActualDispatch(t *testing.T) {
	var webFetch toolSpec
	for _, spec := range toolSpecs() {
		if spec.Name == "WebFetch" {
			webFetch = spec
			break
		}
	}
	if webFetch.Name == "" {
		t.Fatal("WebFetch tool spec not found")
	}
	props, _ := webFetch.Parameters["properties"].(map[string]any)
	modeSpec, _ := props["mode"].(map[string]any)
	modeDesc, _ := modeSpec["description"].(string)
	if modeDesc == "" {
		t.Fatalf("WebFetch mode description missing: %#v", modeSpec)
	}
	// Must advertise readability as the default and supported.
	if !strings.Contains(modeDesc, "readability (default)") {
		t.Fatalf("mode description must advertise readability as the default, got: %q", modeDesc)
	}
	// Must NOT carry the stale wording that called readability rejected or raw the default.
	if strings.Contains(modeDesc, "raw (default)") {
		t.Fatalf("mode description must not claim raw is the default, got: %q", modeDesc)
	}
	if strings.Contains(modeDesc, "readability, smart, and screenshot are rejected") {
		t.Fatalf("mode description must not claim readability is rejected, got: %q", modeDesc)
	}
	// smart/screenshot remain the rejected modes.
	if !strings.Contains(modeDesc, "smart and screenshot") {
		t.Fatalf("mode description should still flag smart and screenshot as unsupported, got: %q", modeDesc)
	}
}

// TS web-fetch contract (server/lib/agent/tools/web-fetch.ts:88-101):
//   - http/https/data schemes are accepted (no error, scheme echoed back);
//   - an unsupported scheme yields the exact "Unsupported protocol: <scheme>: —
//     only http://, https://, and data: URLs are supported. Use the Read tool
//     for local files." message;
//   - a scheme-less / relative URL is rejected as "Invalid URL: <url>" (TS uses
//     `new URL(url)` which throws on relative URLs).
func TestValidateWebFetchURLMatchesTS(t *testing.T) {
	for _, ok := range []string{
		"http://example.com/page",
		"https://example.com",
		"data:text/html,<p>hi</p>",
	} {
		_, scheme, msg := validateWebFetchURL(ok)
		if msg != "" {
			t.Fatalf("expected %q to be accepted, got error %q", ok, msg)
		}
		if scheme == "" {
			t.Fatalf("expected a non-empty scheme for %q", ok)
		}
	}

	// Unsupported scheme -> echoes the scheme + Read-tool guidance.
	_, _, fileMsg := validateWebFetchURL("file:///etc/passwd")
	wantFile := "Unsupported protocol: file: — only http://, https://, and data: URLs are supported. Use the Read tool for local files."
	if fileMsg != wantFile {
		t.Fatalf("file URL message mismatch:\n got: %q\nwant: %q", fileMsg, wantFile)
	}
	_, _, ftpMsg := validateWebFetchURL("ftp://host/x")
	if !strings.HasPrefix(ftpMsg, "Unsupported protocol: ftp:") || !strings.Contains(ftpMsg, "Use the Read tool for local files") {
		t.Fatalf("ftp URL should report unsupported protocol with guidance, got %q", ftpMsg)
	}

	// Relative / scheme-less URL -> "Invalid URL: <url>" (echoes the input).
	rel := "not-a-url/page"
	_, _, relMsg := validateWebFetchURL(rel)
	if relMsg != "Invalid URL: "+rel {
		t.Fatalf("relative URL should echo as Invalid URL, got %q", relMsg)
	}
}

// TestWebFetchUnsupportedProtocolMessageAcrossModes verifies all three WebFetch
// execution paths (readability default, dom, raw) surface the TS-aligned
// "Unsupported protocol" message — previously each used different, less helpful
// wording and dropped the Read-tool guidance.
func TestWebFetchUnsupportedProtocolMessageAcrossModes(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"default-readability", `{"url":"file:///etc/passwd"}`},
		{"dom", `{"url":"file:///etc/passwd","mode":"dom"}`},
		{"raw", `{"url":"file:///etc/passwd","mode":"raw"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch", tc.args)
			if !strings.Contains(out, "Unsupported protocol: file:") {
				t.Fatalf("%s: expected 'Unsupported protocol: file:', got: %s", tc.name, out)
			}
			if !strings.Contains(out, "Use the Read tool for local files") {
				t.Fatalf("%s: expected Read-tool guidance, got: %s", tc.name, out)
			}
			// Guard against the old inconsistent wording.
			if strings.Contains(out, "only http, https, and data URLs are supported for dom mode") {
				t.Fatalf("%s: must not use the old dom-mode-specific wording, got: %s", tc.name, out)
			}
		})
	}
}

// TestWebFetchRelativeURLReportsInvalidURL verifies a scheme-less URL is
// reported as "Invalid URL: <url>" (echoing the input) rather than being
// misclassified as an empty unsupported protocol, matching the TS new URL()
// rejection path.
func TestWebFetchRelativeURLReportsInvalidURL(t *testing.T) {
	out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch", `{"url":"example.com/page"}`)
	if !strings.Contains(out, "Invalid URL: example.com/page") {
		t.Fatalf("relative URL should report 'Invalid URL: example.com/page', got: %s", out)
	}
}
