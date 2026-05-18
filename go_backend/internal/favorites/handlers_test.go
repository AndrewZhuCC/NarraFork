package favorites

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
)

func TestCreateFavoriteDoesNotInsertWhenLookupFails(t *testing.T) {
	sqlDB := openFavoritesTestDB(t)
	settings := loadFavoritesTestSettings(t)
	authService := auth.NewService(sqlDB, settings)
	result, err := authService.Register(context.Background(), "member", "password123", "en")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(authService.Middleware)
		NewHandler(sqlDB).RegisterRoutes(r)
	})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"path":"/repo","label":"Repo"}`))
	req.Header.Set("Authorization", "Bearer "+result.Token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected lookup failure to return 500, got %d body=%s", w.Code, w.Body.String())
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM user_favorite_directories_store`).Scan(&count); err != nil {
		t.Fatalf("count favorite store rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("favorite should not be inserted when lookup fails, count=%d", count)
	}
}

func openFavoritesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, language TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE user_favorite_directories_store (id TEXT PRIMARY KEY, user_id TEXT, path TEXT, label TEXT, sort_order INTEGER, created_at TEXT)`,
		`CREATE VIEW user_favorite_directories AS SELECT id, user_id, path, label, sort_order, created_at FROM user_favorite_directories_store WHERE __narrafork_missing_favorite_lookup__()`,
		`CREATE TRIGGER user_favorite_directories_insert INSTEAD OF INSERT ON user_favorite_directories BEGIN INSERT INTO user_favorite_directories_store (id, user_id, path, label, sort_order, created_at) VALUES (NEW.id, NEW.user_id, NEW.path, NEW.label, NEW.sort_order, NEW.created_at); END;`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}

func loadFavoritesTestSettings(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return settings
}
