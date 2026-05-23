package fs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/config"
)

func TestBrowseReturnsFrontendContractFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	r := chi.NewRouter()
	r.Route("/fs", NewHandler(settings).RegisterRoutes)
	req := httptest.NewRequest(http.MethodGet, "/fs/browse?path="+dir, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode browse body: %v", err)
	}
	if body["path"] != dir || body["parent"] != filepath.Dir(dir) || body["sep"] != string(os.PathSeparator) {
		t.Fatalf("unexpected browse metadata: %#v", body)
	}
	if _, ok := body["entries"].([]any); !ok {
		t.Fatalf("entries should be an array, got %#v", body["entries"])
	}
	if _, ok := body["drives"].([]any); !ok {
		t.Fatalf("drives should be an array, got %#v", body["drives"])
	}
}

func TestBrowseHonorsShowHidden(t *testing.T) {
	settings := loadFSTestSettings(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".secret"), 0o755); err != nil {
		t.Fatalf("mkdir hidden: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "visible"), 0o755); err != nil {
		t.Fatalf("mkdir visible: %v", err)
	}

	body := serveFSJSON(t, settings, http.MethodGet, "/fs/browse?path="+dir, "")
	if entryNames(body)[".secret"] {
		t.Fatalf("hidden entry should be omitted by default: %#v", body["entries"])
	}
	if !entryNames(body)["visible"] {
		t.Fatalf("visible entry missing: %#v", body["entries"])
	}
	body = serveFSJSON(t, settings, http.MethodGet, "/fs/browse?path="+dir+"&showHidden=1", "")
	if !entryNames(body)[".secret"] {
		t.Fatalf("hidden entry should be included with showHidden=1: %#v", body["entries"])
	}
}

func TestMkdirRejectsUnsafeNamesAndCreatesSingleDirectory(t *testing.T) {
	settings := loadFSTestSettings(t)
	parent := t.TempDir()
	for _, name := range []string{"../escape", "nested/child", "nested\\child", "bad<name", "..", string([]byte{'b', 'a', 'd', 0x01})} {
		w := serveFSRaw(t, settings, http.MethodPost, "/fs/mkdir", `{"parent":"`+parent+`","name":"`+name+`"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("mkdir %q should be rejected, got %d body=%s", name, w.Code, w.Body.String())
		}
	}
	w := serveFSRaw(t, settings, http.MethodPost, "/fs/mkdir", `{"parent":"`+parent+`","name":"child"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("mkdir valid got %d body=%s", w.Code, w.Body.String())
	}
	if info, err := os.Stat(filepath.Join(parent, "child")); err != nil || !info.IsDir() {
		t.Fatalf("expected child directory, info=%#v err=%v", info, err)
	}
}

func TestRevealValidatesPathAndOpensSupportedPlatforms(t *testing.T) {
	settings := loadFSTestSettings(t)
	missing := filepath.Join(t.TempDir(), "missing")
	w := serveFSRaw(t, settings, http.MethodPost, "/fs/reveal", `{"path":"`+missing+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing reveal path should be rejected, got %d body=%s", w.Code, w.Body.String())
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("unsupported platform should be tested separately")
	}
	dir := t.TempDir()
	w = serveFSRaw(t, settings, http.MethodPost, "/fs/reveal", `{"path":"`+dir+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("supported reveal should succeed, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode reveal body: %v", err)
	}
	if body["ok"] != true || body["path"] != dir {
		t.Fatalf("unexpected reveal success body: %#v", body)
	}
}

func TestRevealUnsupportedPlatformReturnsFeatureDisabled(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" || runtime.GOOS == "linux" {
		t.Skip("current platform now supports reveal")
	}
	settings := loadFSTestSettings(t)
	dir := t.TempDir()
	w := serveFSRaw(t, settings, http.MethodPost, "/fs/reveal", `{"path":"`+dir+`"}`)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("unsupported reveal should not fake success, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode reveal body: %v", err)
	}
	if body["ok"] != false || body["code"] != "FS_REVEAL_UNSUPPORTED" || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected reveal unsupported body: %#v", body)
	}
	reason, _ := body["reason"].(string)
	if reason != "Opening the system file manager is not supported on this platform by go_backend." || body["error"] != reason || body["message"] != reason {
		t.Fatalf("unsupported reveal should expose reason/error/message, got %#v", body)
	}
}

func TestPreviewUsesTextSizeLimitAndInlineHeaders(t *testing.T) {
	settings := loadFSTestSettings(t)
	dir := t.TempDir()
	largeText := filepath.Join(dir, "large.log")
	if err := os.WriteFile(largeText, []byte(strings.Repeat("x", maxTextPreview+1)), 0o644); err != nil {
		t.Fatalf("write large text: %v", err)
	}
	w := serveFSRaw(t, settings, http.MethodGet, "/fs/preview?path="+largeText, "")
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large text preview should be 413, got %d body=%s", w.Code, w.Body.String())
	}
	var tooLarge map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &tooLarge); err != nil {
		t.Fatalf("decode too-large preview body: %v body=%s", err, w.Body.String())
	}
	reason, _ := tooLarge["reason"].(string)
	if tooLarge["ok"] != false || tooLarge["code"] != "FS_PREVIEW_TOO_LARGE" || reason == "" || tooLarge["error"] != reason || tooLarge["message"] != reason || tooLarge["size"] != float64(maxTextPreview+1) || tooLarge["maxBytes"] != float64(maxTextPreview) {
		t.Fatalf("large preview should expose bounded diagnostics, got %#v", tooLarge)
	}
	smallText := filepath.Join(dir, "small.log")
	if err := os.WriteFile(smallText, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write small text: %v", err)
	}
	w = serveFSRaw(t, settings, http.MethodGet, "/fs/preview?path="+smallText, "")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || w.Header().Get("Content-Disposition") != "inline" {
		t.Fatalf("small text preview headers/status mismatch: code=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
}

func loadFSTestSettings(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return settings
}

func serveFSRaw(t *testing.T, settings *config.Store, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/fs", NewHandler(settings).RegisterRoutes)
	reader := bytes.NewReader([]byte(body))
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func serveFSJSON(t *testing.T, settings *config.Store, method, path, body string) map[string]any {
	t.Helper()
	w := serveFSRaw(t, settings, method, path, body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode body: %v body=%s", err, w.Body.String())
	}
	return decoded
}

func entryNames(body map[string]any) map[string]bool {
	entries, _ := body["entries"].([]any)
	names := map[string]bool{}
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		name, _ := entry["name"].(string)
		if name != "" {
			names[name] = true
		}
	}
	return names
}
