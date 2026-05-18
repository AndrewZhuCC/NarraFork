package misccompat

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestShareHTMLPreviewEscapesContentAndSetsSecurityHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "page.html")
	if err := os.WriteFile(path, []byte(`<h1>Hello</h1><script>alert("x")</script>`), 0o644); err != nil {
		t.Fatalf("write html fixture: %v", err)
	}
	withShareRecord(t, "html-share", path, "page.html")

	w := servePublicShareRequest(t, http.MethodGet, "/api/shares/html-share/preview")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("expected html content type, got %q", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Fatalf("expected restrictive CSP, got %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff, got %q", got)
	}
	body := w.Body.String()
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("expected escaped script content, got %q", body)
	}
}

func TestSharePreviewRejectsUnsupportedTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(path, []byte("zip-ish"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	withShareRecord(t, "zip-share", path, "archive.zip")

	w := servePublicShareRequest(t, http.MethodGet, "/api/shares/zip-share/preview")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected unsupported preview to return 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestShareDownloadUsesAttachmentAndSafeHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	withShareRecord(t, "txt-share", path, "报告.txt")

	w := servePublicShareRequest(t, http.MethodGet, "/api/shares/txt-share")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	disposition := w.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, "filename*=") {
		t.Fatalf("expected attachment disposition with encoded filename, got %q", disposition)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("expected no-cache, got %q", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff, got %q", got)
	}
}

func withShareRecord(t *testing.T, shareID, storagePath, originalName string) {
	t.Helper()
	previous := shareRecords
	shareRecords = map[string]map[string]any{
		shareID: {
			"id":           shareID,
			"storagePath":  storagePath,
			"originalName": originalName,
			"expiresAt":    time.Now().Add(time.Hour),
		},
	}
	t.Cleanup(func() { shareRecords = previous })
}

func servePublicShareRequest(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Route("/api/shares", NewHandler(nil).RegisterPublicShares)
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
