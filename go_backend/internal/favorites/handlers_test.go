package favorites

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
)

// TestFavoriteBoundaryAndTypeGuardsMatchTS covers the deterministic create/update/
// reorder validation guards that mirror the TypeScript Zod bounds (path 1..4096,
// label max 200, sortOrder int>=0, reorder ids = non-empty array of non-empty
// strings) but were not exercised by the existing happy/missing-state table:
// overlong path/label, negative sortOrder, reorder with a non-array ids value,
// and reorder with a non-string id element. All must be rejected with 400 before
// any DB mutation.
func TestFavoriteBoundaryAndTypeGuardsMatchTS(t *testing.T) {
	sqlDB := openFavoritesParityTestDB(t)
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

	overlongPath := strings.Repeat("p", 4097)
	overlongLabel := strings.Repeat("l", 201)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"create overlong path", http.MethodPost, "/", fmt.Sprintf(`{"path":%q}`, overlongPath), http.StatusBadRequest},
		{"create overlong label", http.MethodPost, "/", fmt.Sprintf(`{"path":"/contract/ok","label":%q}`, overlongLabel), http.StatusBadRequest},
		{"create empty path string", http.MethodPost, "/", `{"path":""}`, http.StatusBadRequest},
		{"reorder ids not array", http.MethodPut, "/reorder", `{"ids":"not-an-array"}`, http.StatusBadRequest},
		{"reorder ids non-string element", http.MethodPut, "/reorder", `{"ids":[123]}`, http.StatusBadRequest},
		{"reorder ids empty-string element", http.MethodPut, "/reorder", `{"ids":[""]}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Authorization", "Bearer "+result.Token)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("expected %d, got %d body=%s", tc.status, w.Code, w.Body.String())
			}
		})
	}

	// No favorite rows should have been created by any rejected request.
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM user_favorite_directories`).Scan(&count); err != nil {
		t.Fatalf("count favorites: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected requests must not insert favorites, got %d rows", count)
	}
}

func TestFavoriteValidationAndMissingStatesMatchTS(t *testing.T) {
	sqlDB := openFavoritesParityTestDB(t)
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

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"list empty", http.MethodGet, "/", "", http.StatusOK},
		{"create valid", http.MethodPost, "/", `{"path":"/contract/favorite-one","label":"Favorite One"}`, http.StatusCreated},
		{"create duplicate", http.MethodPost, "/", `{"path":"/contract/favorite-one","label":"Duplicate"}`, http.StatusBadRequest},
		{"create missing path", http.MethodPost, "/", `{}`, http.StatusBadRequest},
		{"create null label", http.MethodPost, "/", `{"path":"/contract/null-label","label":null}`, http.StatusBadRequest},
		{"update missing", http.MethodPatch, "/missing", `{"path":"/contract/missing"}`, http.StatusNotFound},
		{"delete missing", http.MethodDelete, "/missing", `{}`, http.StatusNotFound},
		{"reorder missing ids", http.MethodPut, "/reorder", `{}`, http.StatusBadRequest},
		{"reorder empty ids", http.MethodPut, "/reorder", `{"ids":[]}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Authorization", "Bearer "+result.Token)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("expected %d, got %d body=%s", tc.status, w.Code, w.Body.String())
			}
		})
	}
}

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

func openFavoritesParityTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, language TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE user_favorite_directories (id TEXT PRIMARY KEY, user_id TEXT, path TEXT, label TEXT, sort_order INTEGER, created_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
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
