package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"

	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
)

func openAuthRuneTestService(t *testing.T) *Service {
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
	t.Cleanup(func() { sqlDB.Close() })
	for _, stmt := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, language TEXT, created_at TEXT, updated_at TEXT)`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create auth schema: %v", err)
		}
	}
	return NewService(sqlDB, settings)
}

// TestRegisterPasswordLengthUsesJSStringLength pins auth password length
// validation to JavaScript String.prototype.length (UTF-16 code units), matching
// the TS registerSchema (server/lib/validators/auth.ts: password .min(8).max(128)).
// Two divergences are covered: byte-vs-character (an 8-char CJK password is 24
// bytes but 8 UTF-16 units, must pass) and rune-vs-UTF-16 (an emoji is 1 rune but
// 2 UTF-16 units, so 65 emoji = 130 units must be rejected even though a
// rune-count check would see only 65).
func TestRegisterPasswordLengthUsesJSStringLength(t *testing.T) {
	service := openAuthRuneTestService(t)

	// 3 CJK chars = 9 bytes = 3 UTF-16 units. Byte-count would accept (9>=8);
	// UTF-16/char count rejects (3<8).
	_, err := service.Register(context.Background(), "alice", strings.Repeat("密", 3), "en")
	assertAppError(t, err, http.StatusBadRequest)

	// 129 CJK chars exceeds the 128-char max regardless of byte length.
	_, err = service.Register(context.Background(), "bob", strings.Repeat("码", 129), "en")
	assertAppError(t, err, http.StatusBadRequest)

	// 8 CJK chars = 24 bytes = 8 UTF-16 units: valid length, should succeed.
	if _, err := service.Register(context.Background(), "carol", strings.Repeat("口", 8), "en"); err != nil {
		t.Fatalf("8-char CJK password should be accepted, got %v", err)
	}

	// 4 emoji = 8 UTF-16 units: meets the min(8) boundary, should succeed. A
	// rune-count check would see only 4 and wrongly reject (4<8).
	if _, err := service.Register(context.Background(), "dave", strings.Repeat("😀", 4), "en"); err != nil {
		t.Fatalf("4-emoji password (8 UTF-16 units) should be accepted, got %v", err)
	}

	// 65 emoji = 130 UTF-16 units: exceeds max(128), must be rejected. A
	// rune-count check would see only 65 and wrongly accept it.
	_, err = service.Register(context.Background(), "erin", strings.Repeat("😀", 65), "en")
	assertAppError(t, err, http.StatusBadRequest)
}

// TestUpdateProfileGitUsernameUsesJSStringLength covers the gitUsername .max(100)
// UTF-16-code-unit limit (TS updateProfileSchema, server/lib/validators/auth.ts).
func TestUpdateProfileGitUsernameUsesJSStringLength(t *testing.T) {
	service := openAuthRuneTestService(t)
	if _, err := service.DB.Exec(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('u1','alice','h','user','now')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// 100 CJK chars = 300 bytes = 100 UTF-16 units; must pass .max(100) and persist.
	name100 := strings.Repeat("名", 100)
	if err := service.UpdateProfile(context.Background(), "u1", &name100, nil); err != nil {
		t.Fatalf("100-char CJK gitUsername should be accepted, got %v", err)
	}
	var stored string
	if err := service.DB.QueryRow(`SELECT git_username FROM users WHERE id='u1'`).Scan(&stored); err != nil {
		t.Fatalf("read git_username: %v", err)
	}
	if stored != name100 {
		t.Fatalf("gitUsername not persisted correctly")
	}

	// 101 CJK chars must be rejected with 400.
	name101 := strings.Repeat("名", 101)
	err := service.UpdateProfile(context.Background(), "u1", &name101, nil)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) || appErr.Status != http.StatusBadRequest {
		t.Fatalf("101-char CJK gitUsername should be rejected with 400, got %v", err)
	}

	// 51 emoji = 102 UTF-16 units: exceeds max(100), must be rejected. A
	// rune-count check would see only 51 and wrongly accept it.
	emoji51 := strings.Repeat("😀", 51)
	err = service.UpdateProfile(context.Background(), "u1", &emoji51, nil)
	if !errors.As(err, &appErr) || appErr.Status != http.StatusBadRequest {
		t.Fatalf("51-emoji gitUsername (102 UTF-16 units) should be rejected with 400, got %v", err)
	}
}

// TestRegisterLanguageLengthUsesJSStringLength pins the Register language length
// check to JavaScript String.prototype.length (UTF-16 code units), matching the TS
// registerSchema (server/lib/validators/auth.ts: language .min(1).max(10)). Go
// previously used len() (UTF-8 bytes), so a multibyte language value within the
// 10-character limit was wrongly rejected: 8 CJK chars = 24 bytes but 8 UTF-16
// units (must pass), while 11 CJK chars exceed max(10).
func TestRegisterLanguageLengthUsesJSStringLength(t *testing.T) {
	service := openAuthRuneTestService(t)

	// 8 CJK chars = 24 bytes = 8 UTF-16 units: within max(10), must be accepted.
	if _, err := service.Register(context.Background(), "alice", "password123", strings.Repeat("文", 8)); err != nil {
		t.Fatalf("8-char CJK language (24 bytes, 8 UTF-16 units) should be accepted, got %v", err)
	}

	// 11 CJK chars = 11 UTF-16 units: exceeds max(10), must be rejected with 400.
	_, err := service.Register(context.Background(), "bob", "password123", strings.Repeat("文", 11))
	assertAppError(t, err, http.StatusBadRequest)

	// Empty language fails min(1).
	_, err = service.Register(context.Background(), "carol", "password123", "")
	assertAppError(t, err, http.StatusBadRequest)
}
