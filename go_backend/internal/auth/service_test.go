package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
)

func TestLoginReturnsLanguagePreferenceQueryErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	for _, stmt := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, created_at TEXT)`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create auth schema: %v", err)
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO users (id, username, password_hash, role, avatar_color, created_at) VALUES ('u1', 'alice', ?, 'user', 'blue', 'now')`, string(hash)); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	service := NewService(sqlDB, settings)

	_, err = service.Login(context.Background(), "alice", "password123")
	if err == nil || !strings.Contains(err.Error(), "query user language") {
		t.Fatalf("expected language query error, got %v", err)
	}
}

func TestVerifyTokenExpiredUsesTSCompatibleCode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	service := NewService(nil, settings)
	claims := Claims{
		Role: "user",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(settings.Core().Auth.JWTSecret))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}

	_, err = service.VerifyToken(token)
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error for expired token, got %T: %v", err, err)
	}
	if appErr.Status != 401 || appErr.Code != "TOKEN_EXPIRED" || appErr.Message != "Token expired" {
		t.Fatalf("unexpected expired token error: %#v", appErr)
	}
}
