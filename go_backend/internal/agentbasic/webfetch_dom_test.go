package agentbasic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

const webFetchDomTestPage = `<!DOCTYPE html>
<html>
<head>
	<title>Test Page</title>
	<style>.hidden { display: none; }</style>
	<script>console.log("tracking pixel");</script>
	<meta name="robots" content="noindex">
</head>
<body>
	<header><nav>Navigation</nav></header>
	<main>
		<article class="post" id="lead">
			<h1>Article Title</h1>
			<p class="intro">Visible paragraph text.</p>
			<noscript>enable javascript</noscript>
		</article>
		<aside class="sidebar">Sidebar content</aside>
	</main>
	<script src="/analytics.js"></script>
</body>
</html>`

func newWebFetchDomServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWebFetchDomModeCleansHTMLWithoutBrowser covers the dom mode no-selector
// path: it fetches over HTTP, extracts the body, discards script/style/noscript
// (and their content), and returns visible text/structure. This must work
// without a browser, matching the TypeScript fetchDom HTTP-fetch fallback.
func TestWebFetchDomModeCleansHTMLWithoutBrowser(t *testing.T) {
	srv := newWebFetchDomServer(t, webFetchDomTestPage)

	out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
		`{"url":"`+srv.URL+`","mode":"dom"}`)

	// Visible content is preserved.
	if !strings.Contains(out, "Article Title") || !strings.Contains(out, "Visible paragraph text.") {
		t.Fatalf("dom output should keep visible text, got: %s", out)
	}
	// Discarded tags' content must be gone.
	for _, leaked := range []string{"tracking pixel", "display: none", "enable javascript", "analytics.js"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("dom output must discard script/style/noscript content, leaked %q in: %s", leaked, out)
		}
	}
	// Structural/semantic tags survive.
	if !strings.Contains(out, "<article") || !strings.Contains(out, "<p") {
		t.Fatalf("dom output should keep structural tags, got: %s", out)
	}
}

// TestWebFetchDomModeWithSelector covers the dom mode CSS-selector subset:
// tag, .class, #id, tag.class extraction, and the no-match message.
func TestWebFetchDomModeWithSelector(t *testing.T) {
	srv := newWebFetchDomServer(t, webFetchDomTestPage)

	run := func(selector string) string {
		return ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
			`{"url":"`+srv.URL+`","mode":"dom","selector":"`+selector+`"}`)
	}

	// Tag selector.
	if out := run("h1"); !strings.Contains(out, "Article Title") || strings.Contains(out, "Sidebar content") {
		t.Fatalf("h1 selector should isolate the heading, got: %s", out)
	}
	// Class selector.
	if out := run(".intro"); !strings.Contains(out, "Visible paragraph text.") || strings.Contains(out, "Article Title") {
		t.Fatalf(".intro selector should isolate the intro paragraph, got: %s", out)
	}
	// ID selector.
	if out := run("#lead"); !strings.Contains(out, "Article Title") {
		t.Fatalf("#lead selector should match the article, got: %s", out)
	}
	// tag.class selector.
	if out := run("aside.sidebar"); !strings.Contains(out, "Sidebar content") {
		t.Fatalf("aside.sidebar selector should match the sidebar, got: %s", out)
	}
	// No match.
	if out := run(".does-not-exist"); !strings.Contains(out, "No elements found matching selector") {
		t.Fatalf("missing selector should report no elements, got: %s", out)
	}
	// Unsupported compound selector.
	if out := run("div p"); !strings.Contains(out, "unsupported CSS selector") {
		t.Fatalf("compound selector should be rejected, got: %s", out)
	}
}

// TestWebFetchDomModeTruncatesAtMaxLength covers the maxLength truncation marker.
func TestWebFetchDomModeTruncatesAtMaxLength(t *testing.T) {
	body := "<html><body><p>" + strings.Repeat("x", 500) + "</p></body></html>"
	srv := newWebFetchDomServer(t, body)

	out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
		`{"url":"`+srv.URL+`","mode":"dom","max_length":50}`)
	if !strings.Contains(out, "[DOM truncated at 50 characters]") {
		t.Fatalf("expected truncation marker, got: %s", out)
	}
}

// TestWebFetchDomModeTruncatesByUTF16CodeUnits proves the dom truncation cuts at
// JS string length (UTF-16 code units) like the TS fetchDom (cleaned.slice(0,
// maxLength)), not at Go byte offsets. With CJK content (each char = 3 UTF-8
// bytes but 1 UTF-16 code unit) a byte-based cut would keep far fewer characters
// and could split a rune mid-sequence (invalid UTF-8).
func TestWebFetchDomModeTruncatesByUTF16CodeUnits(t *testing.T) {
	// 100 CJK chars inside a <p>; cleaned content is the 100 chars.
	body := "<html><body><p>" + strings.Repeat("配", 100) + "</p></body></html>"
	srv := newWebFetchDomServer(t, body)

	out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
		`{"url":"`+srv.URL+`","mode":"dom","max_length":10}`)
	if !strings.Contains(out, "[DOM truncated at 10 characters]") {
		t.Fatalf("expected UTF-16 truncation marker, got: %s", out)
	}
	// The kept prefix (before the marker) must be exactly 10 UTF-16 code units —
	// a byte cut at offset 10 would keep only ~3 CJK chars and corrupt a rune.
	head, _, _ := strings.Cut(out, "\n\n[DOM truncated")
	if n := len(utf16.Encode([]rune(head))); n != 10 {
		t.Fatalf("expected 10 UTF-16 code units kept, got %d: %q", n, head)
	}
	if !utf8.ValidString(head) {
		t.Fatalf("truncated output must remain valid UTF-8, got: %q", head)
	}
}

// TestWebFetchDomModeRejectsUnsupportedScheme confirms dom mode only fetches
// http/https/data schemes (file: is rejected like the Read-tool boundary) and
// mirrors the TS web-fetch "Unsupported protocol" message including the
// Read-tool guidance.
func TestWebFetchDomModeRejectsUnsupportedScheme(t *testing.T) {
	out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
		`{"url":"file:///etc/passwd","mode":"dom"}`)
	if !strings.Contains(out, "Unsupported protocol: file:") || !strings.Contains(out, "Use the Read tool for local files") {
		t.Fatalf("dom mode should reject file URLs with the TS-aligned message, got: %s", out)
	}
}

// TestWebFetchDomModeUsesCustomProxyPolicy confirms dom mode consumes the same
// WebFetch proxy policy as raw mode (capability webFetch.policy =
// "permission-policy-and-proxy-consumed"): the HTTP fetch for dom extraction is
// routed through the configured custom proxy.
func TestWebFetchDomModeUsesCustomProxyPolicy(t *testing.T) {
	proxyHit := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHit = true
		if r.URL.Host != "dom-target.example" || r.URL.Path != "/page" {
			t.Fatalf("unexpected proxied dom URL: host=%q path=%q", r.URL.Host, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><p>proxied dom body</p><script>x()</script></body></html>"))
	}))
	defer proxy.Close()

	ctx := ContextWithToolRuntimeSettings(context.Background(),
		ToolRuntimeSettings{WebFetchProxy: WebFetchProxyPolicy{Mode: "custom", URL: proxy.URL}})
	out := ExecuteTool(ctx, t.TempDir(), "WebFetch",
		`{"url":"http://dom-target.example/page","mode":"dom"}`)
	if !proxyHit {
		t.Fatalf("dom mode should route through the configured proxy, output=%s", out)
	}
	if !strings.Contains(out, "proxied dom body") {
		t.Fatalf("dom output should contain proxied body, got: %s", out)
	}
	if strings.Contains(out, "x()") {
		t.Fatalf("dom output should discard script content, got: %s", out)
	}
}

// TestWebFetchDomModeHandlesDataURL covers dom mode over a data: URL, matching
// the TS fetchDom -> httpFetchHtml data: handling. The inline HTML is parsed and
// cleaned (script content discarded) without any network fetch.
func TestWebFetchDomModeHandlesDataURL(t *testing.T) {
	dataURL := "data:text/html,<html><body><h1>Inline</h1><script>bad()</script><p>kept</p></body></html>"
	out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch",
		`{"url":"`+dataURL+`","mode":"dom"}`)
	if !strings.Contains(out, "Inline") || !strings.Contains(out, "kept") {
		t.Fatalf("dom data URL should keep visible text, got: %s", out)
	}
	if strings.Contains(out, "bad()") {
		t.Fatalf("dom data URL should discard script content, got: %s", out)
	}
}
