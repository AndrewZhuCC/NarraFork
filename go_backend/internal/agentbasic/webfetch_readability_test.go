package agentbasic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFetchReadabilityExtractsArticleText verifies the heuristic readability
// extractor returns the title (# header), meta description (> excerpt), and the
// <article> body text while stripping nav/footer boilerplate and HTML tags.
func TestFetchReadabilityExtractsArticleText(t *testing.T) {
	page := `<html><head><title>Headline Title</title>` +
		`<meta name="description" content="A short summary."></head>` +
		`<body><nav>menu junk link</nav>` +
		`<article><h1>Headline</h1><p>Real paragraph one.</p>` +
		`<nav class="inline-toc">inline nav junk</nav>` +
		`<p>Real paragraph two.</p></article>` +
		`<footer>footer junk text</footer></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	out := fetchReadabilityHTTP(context.Background(), server.URL, 20000)
	if !strings.Contains(out, "# Headline Title") {
		t.Fatalf("expected title header, got: %s", out)
	}
	if !strings.Contains(out, "> A short summary.") {
		t.Fatalf("expected excerpt, got: %s", out)
	}
	if !strings.Contains(out, "Real paragraph one.") || !strings.Contains(out, "Real paragraph two.") {
		t.Fatalf("expected article body, got: %s", out)
	}
	if strings.Contains(out, "menu junk") || strings.Contains(out, "footer junk") || strings.Contains(out, "inline nav junk") {
		t.Fatalf("boilerplate must be stripped, got: %s", out)
	}
	if strings.Contains(out, "<article>") || strings.Contains(out, "<p>") {
		t.Fatalf("HTML tags must not appear in readability output, got: %s", out)
	}
}

// TestFetchReadabilityFallsBackToDensityBlock verifies that without
// <article>/<main>, the highest-text-density block is selected and sidebar/nav
// boilerplate is excluded.
func TestFetchReadabilityFallsBackToDensityBlock(t *testing.T) {
	page := `<html><head><title>Doc</title></head><body>` +
		`<div class="sidebar"><a href="/a">nav a</a><a href="/b">nav b</a></div>` +
		`<div class="content"><p>The main content body text is quite long and meaningful here.</p>` +
		`<p>It contains several sentences of real readable prose for the reader.</p></div>` +
		`</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	out := fetchReadabilityHTTP(context.Background(), server.URL, 20000)
	if !strings.Contains(out, "main content body text") || !strings.Contains(out, "real readable prose") {
		t.Fatalf("expected density-selected content body, got: %s", out)
	}
	if strings.Contains(out, "nav a") || strings.Contains(out, "nav b") {
		t.Fatalf("sidebar boilerplate must be excluded, got: %s", out)
	}
}

// TestFetchReadabilityTruncatesAtMaxLength verifies UTF-16 truncation with the
// TS-compatible suffix.
func TestFetchReadabilityTruncatesAtMaxLength(t *testing.T) {
	long := strings.Repeat("word ", 5000)
	page := `<html><head><title>T</title></head><body><article><p>` + long + `</p></article></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	out := fetchReadabilityHTTP(context.Background(), server.URL, 200)
	if !strings.Contains(out, "[Content truncated at 200 characters]") {
		t.Fatalf("expected truncation suffix, got tail: %q", out[max(0, len(out)-80):])
	}
}

// TestFetchReadabilityNoReadableText verifies the TS-compatible failure message
// when there is no extractable text.
func TestFetchReadabilityNoReadableText(t *testing.T) {
	page := `<html><head><title>Empty</title></head><body><script>var x=1;</script></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	out := fetchReadabilityHTTP(context.Background(), server.URL, 20000)
	if !strings.Contains(out, "Failed to extract article content") {
		t.Fatalf("expected no-readable-text message, got: %s", out)
	}
}

// TestWebFetchDefaultModeIsReadability verifies the tool dispatch routes an
// absent mode to readability (TS default), returning extracted text rather than
// raw HTML.
func TestWebFetchDefaultModeIsReadability(t *testing.T) {
	page := `<html><head><title>Dispatch</title></head><body>` +
		`<article><p>dispatched article content here</p></article></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	out := ExecuteTool(context.Background(), t.TempDir(), "WebFetch", `{"url":"`+server.URL+`"}`)
	if !strings.Contains(out, "# Dispatch") || !strings.Contains(out, "dispatched article content here") {
		t.Fatalf("default WebFetch should extract readability text, got: %s", out)
	}
	if strings.Contains(out, "Status:") || strings.Contains(out, "<article>") {
		t.Fatalf("default WebFetch must not return raw HTTP/HTML, got: %s", out)
	}
}
