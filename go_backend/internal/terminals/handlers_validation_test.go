package terminals

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

// Wave 282: Go terminal tab create must mirror the TypeScript
// createTerminalTabSchema refine — exactly one of chapterId or narratorId is
// required (XOR). Missing and ambiguous targets both return 400.
func TestCreateTabRequiresExactlyOneTargetLikeTypeScript(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	router := chi.NewRouter()
	NewHandler(sqlDB, nil).RegisterRoutes(router)

	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"missing target", `{"name":"Shell"}`, http.StatusBadRequest},
		{"ambiguous target", `{"name":"Shell","chapterId":"chapter-1","narratorId":"nar-1"}`, http.StatusBadRequest},
		{"chapter only", `{"name":"Shell","chapterId":"chapter-1"}`, http.StatusCreated},
		{"narrator only", `{"name":"Shell","narratorId":"nar-1"}`, http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/tabs", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("create tab %q status = %d body=%s, want %d", tc.name, rec.Code, rec.Body.String(), tc.status)
			}
		})
	}
}

// Wave 282: Go terminal view-state PUT must mirror the TypeScript
// updateTerminalViewStateSchema — at least one of chapterId or narratorId is
// required, and layout (when provided) must be a valid enum value.
func TestPutViewStateValidatesTargetAndLayoutLikeTypeScript(t *testing.T) {
	sqlDB := openTerminalViewStateValidationDB(t)
	settings := loadTerminalsTestSettings(t)
	authService := auth.NewService(sqlDB, settings)
	result, err := authService.Register(context.Background(), "member", "password123", "en")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(authService.Middleware)
		NewHandler(sqlDB, nil).RegisterRoutes(r)
	})

	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"missing target", `{"layout":"single"}`, http.StatusBadRequest},
		{"invalid layout", `{"chapterId":"chapter-1","layout":"hexa"}`, http.StatusBadRequest},
		{"invalid panel assignment value", `{"chapterId":"chapter-1","panelAssignments":{"a":1}}`, http.StatusBadRequest},
		{"invalid panel assignment array item", `{"chapterId":"chapter-1","panelAssignments":{"a":["t1",2]}}`, http.StatusBadRequest},
		{"valid chapter single", `{"chapterId":"chapter-1","layout":"single"}`, http.StatusOK},
		{"valid narrator split", `{"narratorId":"nar-1","layout":"split-h"}`, http.StatusOK},
		{"valid panel assignments string and array", `{"chapterId":"chapter-1","panelAssignments":{"left":"t1","right":["t2","t3"]}}`, http.StatusOK},
		{"valid null panel assignments", `{"chapterId":"chapter-1","panelAssignments":null}`, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/view-state", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+result.Token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("put view-state %q status = %d body=%s, want %d", tc.name, rec.Code, rec.Body.String(), tc.status)
			}
		})
	}
}

func openTerminalViewStateValidationDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, language TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE terminal_view_state (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			chapter_id TEXT,
			narrator_id TEXT,
			layout TEXT NOT NULL,
			active_tab_id TEXT,
			panel_assignments TEXT,
			updated_at TEXT NOT NULL,
			UNIQUE(user_id, chapter_id),
			UNIQUE(user_id, narrator_id)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec view-state schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}

func loadTerminalsTestSettings(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return settings
}
