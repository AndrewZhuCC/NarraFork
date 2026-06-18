package terminals

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Wave 572: Go terminal tab reorder must mirror the TypeScript
// reorderTerminalTabsSchema — ids = z.array(z.string().min(1)).min(1). An empty
// ids array (or an empty-string entry) is a 400, not a silent no-op.
func TestReorderTabsValidatesIdsLikeTypeScript(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	router := chi.NewRouter()
	NewHandler(sqlDB, nil).RegisterRoutes(router)

	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"empty ids array", `{"ids":[]}`, http.StatusBadRequest},
		{"missing ids", `{}`, http.StatusBadRequest},
		{"empty string entry", `{"ids":["t1",""]}`, http.StatusBadRequest},
		{"valid ids", `{"ids":["t1","t2"]}`, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/tabs/reorder", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("reorder %q status = %d body=%s, want %d", tc.name, rec.Code, rec.Body.String(), tc.status)
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

// TestCreateTerminalNameLengthUsesJSStringLength pins the terminal create handler's
// name length validation to JavaScript String.prototype.length (UTF-16 code
// units), matching the TS createTerminalSchema
// (server/lib/validators/terminals.ts: name z.string().max(100)). An emoji is 1
// rune but 2 UTF-16 units, so 51 emoji = 102 units must be rejected even though a
// rune-count check would see only 51 and accept it.
func TestCreateTerminalNameLengthUsesJSStringLength(t *testing.T) {
	sqlDB := openTerminalBroadcastDB(t)
	router := chi.NewRouter()
	NewHandler(sqlDB, nil).RegisterRoutes(router)

	// 51 emoji = 102 UTF-16 units: must be rejected by the name length guard
	// (400) before any runtime/DB work. A rune-count check would see 51 and pass.
	emoji51 := strings.Repeat("😀", 51)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"chapterId":"chapter-1","name":"`+emoji51+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Invalid terminal create request") {
		t.Fatalf("51-emoji name (102 UTF-16 units) should be rejected with 400, got %d %s", rec.Code, rec.Body.String())
	}

	// 50 emoji = 100 UTF-16 units: passes the length guard (boundary). It must NOT
	// fail with the length-validation 400 error.
	emoji50 := strings.Repeat("😀", 50)
	req2 := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"chapterId":"chapter-1","name":"`+emoji50+`"}`))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code == http.StatusBadRequest && strings.Contains(rec2.Body.String(), "Invalid terminal create request") {
		t.Fatalf("50-emoji name (100 UTF-16 units) should pass the length guard, got %d %s", rec2.Code, rec2.Body.String())
	}
}
