package auth

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/config"
)

func TestRegisterHandlerDefaultsMissingLanguageToEnglish(t *testing.T) {
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
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, language TEXT, created_at TEXT, updated_at TEXT)`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create auth schema: %v", err)
		}
	}

	router := chi.NewRouter()
	NewHandler(NewService(sqlDB, settings)).RegisterRoutes(router)

	req := httptest.NewRequest(
		http.MethodPost,
		"/register",
		strings.NewReader(`{"username":"alice","password":"password123"}`),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}
	var body AuthResult
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Language != "en" {
		t.Fatalf("expected response language en, got %q", body.Language)
	}
	var language string
	err = sqlDB.QueryRow(
		`SELECT language FROM user_preferences WHERE user_id = ?`,
		body.User.ID,
	).Scan(&language)
	if err != nil {
		t.Fatalf("query preference language: %v", err)
	}
	if language != "en" {
		t.Fatalf("expected stored language en, got %q", language)
	}
}
