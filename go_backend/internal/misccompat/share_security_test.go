package misccompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
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

func TestShareFileToolCreatedShareResolvesForDownloadAndPreview(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetShareRecords(t)
	cwd := t.TempDir()
	fixture := []byte(`<h1>Hello</h1><script>alert("x")</script>`)
	if err := os.WriteFile(filepath.Join(cwd, "artifact.html"), fixture, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	out := agentbasic.ExecuteTool(context.Background(), cwd, "ShareFile", `{"path":"artifact.html","preview":true}`)
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("expected tool JSON output, got %q: %v", out, err)
	}
	shareID, _ := payload["id"].(string)
	if shareID == "" {
		t.Fatalf("expected share id in payload: %#v", payload)
	}
	if payload["url"] != "/api/shares/"+shareID || payload["previewUrl"] != "/api/shares/"+shareID+"/preview" {
		t.Fatalf("unexpected share links: %#v", payload)
	}
	if _, err := os.Stat(shareMetadataPath(shareID)); err != nil {
		t.Fatalf("expected tool to write disk metadata: %v", err)
	}

	download := servePublicShareRequest(t, http.MethodGet, "/api/shares/"+shareID)
	if download.Code != http.StatusOK {
		t.Fatalf("expected tool-created share download 200, got %d body=%s", download.Code, download.Body.String())
	}
	if got := download.Body.String(); got != string(fixture) {
		t.Fatalf("unexpected downloaded content: %q", got)
	}

	preview := servePublicShareRequest(t, http.MethodGet, "/api/shares/"+shareID+"/preview")
	if preview.Code != http.StatusOK {
		t.Fatalf("expected tool-created share preview 200, got %d body=%s", preview.Code, preview.Body.String())
	}
	if body := preview.Body.String(); strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatalf("expected escaped preview body, got %q", body)
	}
}

func TestResolveShareRejectsInvalidShareIDTraversal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetShareRecords(t)
	outsideDir := filepath.Join(filepath.Dir(sharesRoot()), "escape")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("create escaped dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.html"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write escaped file: %v", err)
	}
	metadata := shareDiskMetadata{
		ID:           "../escape",
		OriginalName: "secret.html",
		FileName:     "secret.html",
		SizeBytes:    6,
		ExpiresAt:    time.Now().Add(time.Hour).Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(sharesRoot()), "escape.json"), data, 0o644); err != nil {
		t.Fatalf("write escaped metadata: %v", err)
	}

	path, name := resolveShare("../escape")
	if path != "" || name != "" {
		t.Fatalf("invalid share id resolved to path=%q name=%q", path, name)
	}
	if shareRecords.len() != 0 {
		t.Fatalf("invalid share id should not populate memory records")
	}
}

func TestResolveShareRejectsMetadataFileNameTraversal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetShareRecords(t)
	shareID := "safe-share"
	shareDir := shareDirPath(shareID)
	if err := os.MkdirAll(shareDir, 0o755); err != nil {
		t.Fatalf("create share dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shareDir, "safe.html"), []byte("safe"), 0o644); err != nil {
		t.Fatalf("write safe file: %v", err)
	}
	outsidePath := filepath.Join(filepath.Dir(shareDir), "secret.html")
	if err := os.WriteFile(outsidePath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	metadata := shareDiskMetadata{
		ID:           shareID,
		OriginalName: "secret.html",
		FileName:     "../secret.html",
		SizeBytes:    6,
		ExpiresAt:    time.Now().Add(time.Hour).Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(shareMetadataPath(shareID), data, 0o644); err != nil {
		t.Fatalf("write metadata: %v", err)
	}

	path, name := resolveShare(shareID)
	if path != "" || name != "" {
		t.Fatalf("traversing metadata resolved to path=%q name=%q", path, name)
	}
	if shareRecords.len() != 0 {
		t.Fatalf("traversing metadata should not populate memory records")
	}
}

func TestServeUploadRejectsTraversalOutsideUploadsRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	home := os.Getenv("HOME")
	uploadsParent := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(uploadsParent, 0o755); err != nil {
		t.Fatalf("create uploads parent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uploadsParent, "settings.json"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write parent file: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/uploads/../settings/settings", nil)
	err := serveUploadFile(w, req, "..", "settings")
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Status != http.StatusNotFound || appErr.Code != "NOT_FOUND" {
		t.Fatalf("expected not found app error, got %#v", appErr)
	}
}

func TestServeUploadRejectsSymlinkEscapeOutsideUploadsRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := uploadsRoot()
	outside := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create uploads root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "abc.png"), []byte("outside-image"), 0o644); err != nil {
		t.Fatalf("write outside upload fixture: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not available: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/uploads/link/abc", nil)
	err := serveUploadFile(w, req, "link", "abc")
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Status != http.StatusNotFound || appErr.Code != "NOT_FOUND" {
		t.Fatalf("expected not found for symlink escape, got %#v", appErr)
	}
}

func TestServeUploadFileServesWithinUploadsRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(uploadsRoot(), "chapter-1")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("create uploads dir: %v", err)
	}
	filePath := filepath.Join(path, "abc.png")
	if err := os.WriteFile(filePath, []byte("image-data"), 0o644); err != nil {
		t.Fatalf("write upload file: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/uploads/chapter-1/abc", nil)
	if err := serveUploadFile(w, req, "chapter-1", "abc"); err != nil {
		t.Fatalf("serve upload: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected png content type, got %q", got)
	}
}

func TestCreateShareRejectsOversizeWithoutResidue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings := loadShareTestSettings(t)
	if _, err := settings.Patch(map[string]any{"shares": map[string]any{"maxFileSizeMb": float64(1)}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	resetShareRecords(t)

	workDir, err := os.MkdirTemp(".", "share-test-*")
	if err != nil {
		t.Fatalf("create working dir fixture: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })
	path := filepath.Join(workDir, "large.bin")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1024*1024+1)), 0o644); err != nil {
		t.Fatalf("write large fixture: %v", err)
	}

	w := serveCreateShareRequest(t, settings, path, 24)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", w.Code, w.Body.String())
	}
	if shareRecords.len() != 0 {
		t.Fatalf("oversize share should not create memory record")
	}
	assertSharesRootEmpty(t)
}

func TestCreateShareRejectsSensitiveHomePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := loadShareTestSettings(t)
	resetShareRecords(t)
	previousWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working dir: %v", err)
	}
	if err := os.Chdir(home); err != nil {
		t.Fatalf("chdir home: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousWorkingDir) })
	settingsPath := filepath.Join(home, ".narrafork", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatalf("create settings dir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write settings fixture: %v", err)
	}

	w := serveCreateShareRequest(t, settings, filepath.Join(".narrafork", "settings.json"), 24)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not shareable") {
		t.Fatalf("expected sensitive path error, got %s", w.Body.String())
	}
	if shareRecords.len() != 0 {
		t.Fatalf("sensitive path should not create share record")
	}
}

func TestCreateShareRejectsPathOutsideWorkingDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings := loadShareTestSettings(t)
	resetShareRecords(t)

	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	w := serveCreateShareRequest(t, settings, path, 24)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "inside the current working directory") {
		t.Fatalf("expected cwd boundary error, got %s", w.Body.String())
	}
	if shareRecords.len() != 0 {
		t.Fatalf("outside path should not create share record")
	}
	assertSharesRootEmpty(t)
}

func TestCreateShareRejectsSymlinkEscapeOutsideWorkingDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings := loadShareTestSettings(t)
	resetShareRecords(t)
	workDir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(workDir, "link")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	previousWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working dir: %v", err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("chdir work dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousWorkingDir) })

	w := serveCreateShareRequest(t, settings, filepath.Join("link", "secret.txt"), 24)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "inside the current working directory") {
		t.Fatalf("expected cwd boundary error, got %s", w.Body.String())
	}
	if shareRecords.len() != 0 {
		t.Fatalf("symlink escape should not create share record")
	}
	assertSharesRootEmpty(t)
}

func TestCreateShareRejectsFinalSymlinkToSensitiveHomePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := loadShareTestSettings(t)
	resetShareRecords(t)
	workDir := t.TempDir()
	settingsPath := filepath.Join(home, ".narrafork", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatalf("create settings dir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write settings fixture: %v", err)
	}
	if err := os.Symlink(settingsPath, filepath.Join(workDir, "settings-link.json")); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	previousWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working dir: %v", err)
	}
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("chdir work dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousWorkingDir) })

	w := serveCreateShareRequest(t, settings, "settings-link.json", 24)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if shareRecords.len() != 0 {
		t.Fatalf("sensitive symlink should not create share record")
	}
	assertSharesRootEmpty(t)
}

func TestExpiredShareIsNotAccessibleAndDeletesDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := shareDirPath("expired-share")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create share dir: %v", err)
	}
	path := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("write expired share: %v", err)
	}
	withShareRecordExpiresAt(t, "expired-share", path, "old.txt", time.Now().Add(-time.Hour))

	w := servePublicShareRequest(t, http.MethodGet, "/api/shares/expired-share")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected expired share to return 404, got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected expired share directory to be removed, stat err=%v", err)
	}
	if shareRecords.len() != 0 {
		t.Fatalf("expected expired record to be deleted")
	}
}

func TestStaleShareDiskWithoutRecordReturnsNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetShareRecords(t)
	dir := shareDirPath("stale-share")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create stale share dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale share: %v", err)
	}

	w := servePublicShareRequest(t, http.MethodGet, "/api/shares/stale-share")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected stale disk share to return 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestCleanupStaleSharesRemovesDiskFallbackAndMemoryRecords(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetShareRecords(t)
	dir := shareDirPath("stale-share")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create stale share dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.html"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale share: %v", err)
	}
	metadata := shareDiskMetadata{
		ID:           "stale-share",
		OriginalName: "stale.html",
		FileName:     "stale.html",
		SizeBytes:    5,
		ExpiresAt:    time.Now().Add(time.Hour).Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(shareMetadataPath("stale-share"), data, 0o644); err != nil {
		t.Fatalf("write stale metadata: %v", err)
	}
	if err := os.WriteFile(shareMetadataPath("stale-share")+".tmp", data, 0o644); err != nil {
		t.Fatalf("write stale metadata tmp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sharesRoot(), "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatalf("write unrelated file: %v", err)
	}
	shareRecords.set(shareRecord{ID: "memory-share", StoragePath: filepath.Join(dir, "stale.html"), OriginalName: "stale.html", ExpiresAt: time.Now().Add(time.Hour)})

	removed, err := CleanupStaleShares()
	if err != nil {
		t.Fatalf("cleanup stale shares: %v", err)
	}
	if removed != 3 {
		t.Fatalf("expected 3 stale entries removed, got %d", removed)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected stale share dir removed, stat err=%v", err)
	}
	if _, err := os.Stat(shareMetadataPath("stale-share")); !os.IsNotExist(err) {
		t.Fatalf("expected stale metadata removed, stat err=%v", err)
	}
	if _, err := os.Stat(shareMetadataPath("stale-share") + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected stale temp metadata removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(sharesRoot(), "keep.txt")); err != nil {
		t.Fatalf("expected unrelated file to remain: %v", err)
	}
	if shareRecords.len() != 0 {
		t.Fatalf("stale cleanup should clear memory records")
	}
}

func TestCleanupSharesClearsMemoryAndDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := shareDirPath("cleanup-share")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create share dir: %v", err)
	}
	path := filepath.Join(dir, "cleanup.txt")
	if err := os.WriteFile(path, []byte("cleanup"), 0o644); err != nil {
		t.Fatalf("write cleanup share: %v", err)
	}
	withShareRecord(t, "cleanup-share", path, "cleanup.txt")

	req := httptest.NewRequest(http.MethodPost, "/api/storage/cleanup", strings.NewReader(`{"target":"shares"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := NewHandler(nil).cleanupStorage(w, req); err != nil {
		t.Fatalf("cleanup shares: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected cleanup 200, got %d body=%s", w.Code, w.Body.String())
	}
	if shareRecords.len() != 0 {
		t.Fatalf("cleanup should clear share records")
	}
	assertSharesRootEmpty(t)
}

func TestShareRecordStoreConcurrentAccessDoesNotPanic(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := newShareRecordStore()
	base := t.TempDir()
	var wg sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				shareID := "share-" + strconv.Itoa(worker) + "-" + strconv.Itoa(i)
				store.set(shareRecord{ID: shareID, OriginalName: "file.txt", StoragePath: filepath.Join(base, shareID, "file.txt"), ExpiresAt: time.Now().Add(time.Hour)})
				store.get(shareID, time.Now())
				store.delete(shareID)
			}
		}()
	}
	wg.Wait()
}

func withShareRecord(t *testing.T, shareID, storagePath, originalName string) {
	t.Helper()
	withShareRecordExpiresAt(t, shareID, storagePath, originalName, time.Now().Add(time.Hour))
}

func withShareRecordExpiresAt(t *testing.T, shareID, storagePath, originalName string, expiresAt time.Time) {
	t.Helper()
	previous := shareRecords
	shareRecords = newShareRecordStore()
	shareRecords.set(shareRecord{ID: shareID, StoragePath: storagePath, OriginalName: originalName, Size: 0, ExpiresAt: expiresAt, CreatedBy: "test-user"})
	t.Cleanup(func() { shareRecords = previous })
}

func resetShareRecords(t *testing.T) {
	t.Helper()
	previous := shareRecords
	shareRecords = newShareRecordStore()
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

func serveCreateShareRequest(t *testing.T, settings *config.Store, path string, expiryHours int) *httptest.ResponseRecorder {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create users table: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO users (id) VALUES ('share-user')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	authService := auth.NewService(database, settings)
	token, err := authService.CreateToken("share-user", "user")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	body, err := json.Marshal(map[string]any{"path": path, "expiryHours": expiryHours})
	if err != nil {
		t.Fatalf("marshal share request: %v", err)
	}
	router := chi.NewRouter()
	handler := NewHandler(database, settings)
	router.Route("/api/shares", func(r chi.Router) {
		r.Use(authService.Middleware)
		handler.RegisterShareCreate(r)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/shares/", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func loadShareTestSettings(t *testing.T) *config.Store {
	t.Helper()
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return settings
}

func assertSharesRootEmpty(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(sharesRoot())
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read shares root: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("expected shares root to be empty, got %v", names)
	}
}
