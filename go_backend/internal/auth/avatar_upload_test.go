package auth

import (
	"bytes"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/config"
)

func newAvatarTestHandler(t *testing.T) (*chi.Mux, *Service, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create users: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u1','alice','h','user','now')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	svc := NewService(sqlDB, settings)
	token, err := svc.CreateToken("u1", "user")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	router := chi.NewRouter()
	NewHandler(svc).RegisterRoutes(router)
	return router, svc, token
}

func avatarUploadRequest(t *testing.T, token, filename, contentType string, size int) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(map[string][]string)
	hdr["Content-Disposition"] = []string{`form-data; name="file"; filename="` + filename + `"`}
	if contentType != "" {
		hdr["Content-Type"] = []string{contentType}
	}
	part, err := mw.CreatePart(hdr)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte{0x42}, size)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/me/avatar", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// TestUploadAvatarMatchesTSMimeSizeAndReplacement pins the PATCH /me/avatar handler
// to the TS saveAvatarImage (server/lib/uploads.ts): the avatar MIME allowlist is
// {png, jpeg, webp} (GIF is NOT allowed for avatars, unlike inline images), the max
// size is 2MB (MAX_AVATAR_SIZE) returning a clean 400, and a new upload removes the
// previous avatar so only one file per user exists.
func TestUploadAvatarMatchesTSMimeSizeAndReplacement(t *testing.T) {
	router, _, token := newAvatarTestHandler(t)
	avatarDir := filepath.Join(os.Getenv("HOME"), ".narrafork", "uploads", "avatars", "u1")

	// GIF must be rejected for avatars (TS AVATAR_MIME_TYPES excludes image/gif).
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, avatarUploadRequest(t, token, "a.gif", "image/gif", 16))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Unsupported avatar image type") {
		t.Fatalf("GIF avatar should be rejected, got %d %s", rec.Code, rec.Body.String())
	}

	// PNG within 2MB succeeds.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, avatarUploadRequest(t, token, "a.png", "image/png", 1024))
	if rec.Code != http.StatusOK {
		t.Fatalf("small PNG avatar should succeed, got %d %s", rec.Code, rec.Body.String())
	}
	firstFiles, _ := os.ReadDir(avatarDir)
	if len(firstFiles) != 1 {
		t.Fatalf("expected exactly one avatar file after first upload, got %d", len(firstFiles))
	}

	// A second upload (WebP) must REPLACE the first (old dir removed), leaving one file.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, avatarUploadRequest(t, token, "b.webp", "image/webp", 2048))
	if rec.Code != http.StatusOK {
		t.Fatalf("second avatar upload should succeed, got %d %s", rec.Code, rec.Body.String())
	}
	secondFiles, _ := os.ReadDir(avatarDir)
	if len(secondFiles) != 1 {
		t.Fatalf("second upload should replace the first (one file), got %d: %v", len(secondFiles), dirNames(secondFiles))
	}
	if secondFiles[0].Name() == firstFiles[0].Name() {
		t.Fatalf("new avatar should have a different file than the replaced one")
	}

	// Over 2MB must be rejected with a clean 400 (TS MAX_AVATAR_SIZE = 2MB).
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, avatarUploadRequest(t, token, "big.png", "image/png", 2*1024*1024+1))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Avatar too large") {
		t.Fatalf("over-2MB avatar should be rejected with 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func dirNames(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
