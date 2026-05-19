package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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

func TestRegisterValidatesInputsBeforeDatabaseAccess(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	service := NewService(nil, settings)

	cases := []struct {
		name     string
		username string
		password string
		language string
	}{
		{name: "short username", username: "ab", password: "password123", language: "en"},
		{name: "invalid username character", username: "alice@example", password: "password123", language: "en"},
		{name: "short password", username: "alice", password: "pass123", language: "en"},
		{name: "long password", username: "alice", password: strings.Repeat("a", 129), language: "en"},
		{name: "empty language", username: "alice", password: "password123", language: ""},
		{name: "long language", username: "alice", password: "password123", language: "very-long-lang"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Register(context.Background(), tc.username, tc.password, tc.language)
			assertAppError(t, err, http.StatusBadRequest)
		})
	}
}

func TestRegisterConcurrentBootstrapCreatesAtMostOneAdmin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sqlDB := openRegisterTestDB(t)
	service := NewService(sqlDB, settings)

	const registrations = 16
	start := make(chan struct{})
	results := make(chan AuthResult, registrations)
	errs := make(chan error, registrations)
	var wg sync.WaitGroup
	for i := 0; i < registrations; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			result, err := service.Register(
				context.Background(),
				fmt.Sprintf("user%02d", i),
				"password123",
				"en",
			)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("register concurrently: %v", err)
		}
	}
	adminResults := 0
	for result := range results {
		if result.User.Role == "admin" {
			adminResults++
		}
	}
	if adminResults > 1 {
		t.Fatalf("expected at most one admin result, got %d", adminResults)
	}

	var userCount, adminCount, preferenceCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&adminCount); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM user_preferences`).Scan(&preferenceCount); err != nil {
		t.Fatalf("count preferences: %v", err)
	}
	if userCount != registrations {
		t.Fatalf("expected %d users, got %d", registrations, userCount)
	}
	if adminCount != 1 {
		t.Fatalf("expected exactly one stored admin, got %d", adminCount)
	}
	if preferenceCount != registrations {
		t.Fatalf("expected %d preferences, got %d", registrations, preferenceCount)
	}
}

func TestRegisterClosedAllowsOnlyBootstrapAdmin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := settings.Patch(map[string]any{"auth": map[string]any{"registrationOpen": false}}); err != nil {
		t.Fatalf("close registration: %v", err)
	}
	sqlDB := openRegisterTestDB(t)
	service := NewService(sqlDB, settings)

	first, err := service.Register(context.Background(), "alice", "password123", "en")
	if err != nil {
		t.Fatalf("bootstrap register: %v", err)
	}
	if first.User.Role != "admin" {
		t.Fatalf("expected bootstrap user to be admin, got %q", first.User.Role)
	}

	_, err = service.Register(context.Background(), "bob", "password123", "en")
	assertAppError(t, err, http.StatusForbidden)

	var userCount, preferenceCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM user_preferences`).Scan(&preferenceCount); err != nil {
		t.Fatalf("count preferences: %v", err)
	}
	if userCount != 1 || preferenceCount != 1 {
		t.Fatalf("expected only bootstrap user and preference, got users=%d preferences=%d", userCount, preferenceCount)
	}
}

func TestLoginValidatesRequiredFieldsBeforeQuery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	service := NewService(nil, settings)

	_, err = service.Login(context.Background(), "", "password123")
	assertAppError(t, err, http.StatusBadRequest)

	_, err = service.Login(context.Background(), "alice", "")
	assertAppError(t, err, http.StatusBadRequest)
}

func TestUpdateProfileValidatesAndClearsGitFields(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create users schema: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO users (id, username, role, git_username, git_email, created_at) VALUES ('u1', 'alice', 'user', 'Alice', 'alice@example.com', 'now')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	service := NewService(sqlDB, nil)

	longGitUsername := strings.Repeat("a", 101)
	err = service.UpdateProfile(context.Background(), "u1", &longGitUsername, nil)
	assertAppError(t, err, http.StatusBadRequest)

	badEmail := "not-an-email"
	err = service.UpdateProfile(context.Background(), "u1", nil, &badEmail)
	assertAppError(t, err, http.StatusBadRequest)

	empty := ""
	if err := service.UpdateProfile(context.Background(), "u1", &empty, &empty); err != nil {
		t.Fatalf("clear profile: %v", err)
	}
	var gitUsername, gitEmail sql.NullString
	if err := sqlDB.QueryRow(`SELECT git_username, git_email FROM users WHERE id = 'u1'`).Scan(&gitUsername, &gitEmail); err != nil {
		t.Fatalf("query profile: %v", err)
	}
	if gitUsername.Valid || gitEmail.Valid {
		t.Fatalf("expected git fields to be cleared, got username=%#v email=%#v", gitUsername, gitEmail)
	}
}

func openRegisterTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, role TEXT NOT NULL, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT NOT NULL)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT NOT NULL UNIQUE, language TEXT, created_at TEXT, updated_at TEXT)`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create auth schema: %v", err)
		}
	}
	return sqlDB
}

func assertAppError(t *testing.T, err error, status int) {
	t.Helper()
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T: %v", err, err)
	}
	if appErr.Status != status {
		t.Fatalf("expected status %d, got %#v", status, appErr)
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
