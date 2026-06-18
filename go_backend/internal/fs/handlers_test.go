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
	if entryNames(body)["note.txt"] {
		t.Fatalf("browse entries should contain directories only, got %#v", body["entries"])
	}
}

func TestBrowseWindowsNoPathReturnsDriveRootContract(t *testing.T) {
	settings := loadFSTestSettings(t)
	oldGOOS := runtimeGOOS
	runtimeGOOS = "windows"
	t.Cleanup(func() { runtimeGOOS = oldGOOS })

	body := serveFSJSON(t, settings, http.MethodGet, "/fs/browse", "")
	if body["path"] != nil || body["parent"] != nil || body["sep"] != string(os.PathSeparator) {
		t.Fatalf("unexpected Windows drive-root metadata: %#v", body)
	}
	entries, ok := body["entries"].([]any)
	if !ok || len(entries) != 0 {
		t.Fatalf("Windows drive root should return an empty entries array, got %#v", body["entries"])
	}
	if _, ok := body["drives"].([]any); !ok {
		t.Fatalf("Windows drive root should include drives array, got %#v", body["drives"])
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
	for _, name := range []string{"../escape", "nested/child", "nested\\child", "bad<name", "bad>name", "bad:name", "bad\"name", "bad|name", "bad?name", "bad*name", "..", ".", string([]byte{'b', 'a', 'd', 0x01}), string([]byte{'b', 'a', 'd', 0x1f})} {
		bodyBytes, err := json.Marshal(map[string]string{"parent": parent, "name": name})
		if err != nil {
			t.Fatalf("marshal mkdir body for %q: %v", name, err)
		}
		w := serveFSRaw(t, settings, http.MethodPost, "/fs/mkdir", string(bodyBytes))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("mkdir %q should be rejected, got %d body=%s", name, w.Code, w.Body.String())
		}
	}
	pathOnly := serveFSRaw(t, settings, http.MethodPost, "/fs/mkdir", `{"path":"`+filepath.Join(parent, "path-only")+`"}`)
	if pathOnly.Code != http.StatusBadRequest {
		t.Fatalf("mkdir path-only body should match TS rejection, got %d body=%s", pathOnly.Code, pathOnly.Body.String())
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
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("supported reveal platform should be tested separately")
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

func TestRevealUnsupportedPlatformMatchesTSValidation(t *testing.T) {
	settings := loadFSTestSettings(t)
	oldGOOS := runtimeGOOS
	runtimeGOOS = "linux"
	t.Cleanup(func() { runtimeGOOS = oldGOOS })
	dir := t.TempDir()
	w := serveFSRaw(t, settings, http.MethodPost, "/fs/reveal", `{"path":"`+dir+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unsupported reveal should match TS 400, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode reveal body: %v", err)
	}
	if body["code"] != "VALIDATION_ERROR" || body["error"] != "Opening file manager is not supported on this platform" {
		t.Fatalf("unexpected reveal unsupported body: %#v", body)
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

	// Parity guard: the binary (image/PDF) limit is 20MB while text is 1MB, so a
	// file just over the text limit must be REJECTED as text (.log) yet ACCEPTED
	// as an image (.png) of the same size. This mirrors the TS branch
	// `stat.size > (mime ? MAX_PREVIEW_BYTES : MAX_TEXT_PREVIEW_BYTES)`.
	betweenSize := maxTextPreview + 1 // > 1MB text limit, < 20MB binary limit
	binaryMid := filepath.Join(dir, "image.png")
	if err := os.WriteFile(binaryMid, []byte(strings.Repeat("x", betweenSize)), 0o644); err != nil {
		t.Fatalf("write mid-size binary: %v", err)
	}
	w = serveFSRaw(t, settings, http.MethodGet, "/fs/preview?path="+binaryMid, "")
	if w.Code != http.StatusOK {
		t.Fatalf("mid-size image should use the 20MB binary limit and be served (200), got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Disposition") != "inline" {
		t.Fatalf("mid-size image preview headers mismatch: headers=%v", w.Header())
	}
	// The same size as a text file (.log) must be rejected by the 1MB text limit.
	textMid := filepath.Join(dir, "midsize.log")
	if err := os.WriteFile(textMid, []byte(strings.Repeat("x", betweenSize)), 0o644); err != nil {
		t.Fatalf("write mid-size text: %v", err)
	}
	w = serveFSRaw(t, settings, http.MethodGet, "/fs/preview?path="+textMid, "")
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("mid-size text should exceed the 1MB text limit and be 413, got %d body=%s", w.Code, w.Body.String())
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

// TestBrowseErrorMessagesMatchTS pins the /api/fs/browse error contract to TS
// (server/routes/fs.ts): a missing path returns "Path does not exist: <abs>",
// a file (non-directory) returns "Not a directory: <abs>". Go previously
// returned the raw os.ReadDir error string and did not distinguish the
// not-a-directory case.
func TestBrowseErrorMessagesMatchTS(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	dir := t.TempDir()

	// Missing path -> "Path does not exist".
	missing := filepath.Join(dir, "does-not-exist")
	rec := serveFSRaw(t, settings, http.MethodGet, "/fs/browse?path="+missing, "")
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "Path does not exist") {
		t.Fatalf("missing path should report 'Path does not exist', got %d body=%s", rec.Code, rec.Body.String())
	}

	// A file path -> "Not a directory".
	filePath := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	rec = serveFSRaw(t, settings, http.MethodGet, "/fs/browse?path="+filePath, "")
	if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "Not a directory") {
		t.Fatalf("file path should report 'Not a directory', got %d body=%s", rec.Code, rec.Body.String())
	}
}
