package terminals

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/auth"
)

// TestTerminalTabListReorderAndViewStateRoundTrip covers the success paths of the
// terminal tab list (chapter filter + ordering), tab reorder persistence, and the
// view-state GET default fallback + PUT/GET round-trip. Existing terminal tests
// cover create/delete/validation but not these read/reorder success paths.
func TestTerminalTabListReorderAndViewStateRoundTrip(t *testing.T) {
	sqlDB := openTerminalTabRoundtripDB(t)
	settings := loadTerminalsTestSettings(t)
	authService := auth.NewService(sqlDB, settings)
	result, err := authService.Register(context.Background(), "member", "password123", "en")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	token := result.Token

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(authService.Middleware)
		NewHandler(sqlDB, nil).RegisterRoutes(r)
	})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// Create two tabs for chapter-1 (and one for another chapter to prove filtering).
	tab1 := createTab(t, do, `{"chapterId":"chapter-1","name":"Alpha"}`)
	tab2 := createTab(t, do, `{"chapterId":"chapter-1","name":"Beta"}`)
	_ = createTab(t, do, `{"chapterId":"chapter-2","name":"Other"}`)

	// List for chapter-1 returns exactly the two tabs, in sort order.
	listResp := do(http.MethodGet, "/tabs?chapterId=chapter-1", "")
	if listResp.Code != http.StatusOK {
		t.Fatalf("list tabs: %d %s", listResp.Code, listResp.Body.String())
	}
	var tabs []map[string]any
	if err := json.Unmarshal(listResp.Body.Bytes(), &tabs); err != nil {
		t.Fatalf("decode tabs: %v", err)
	}
	if len(tabs) != 2 {
		t.Fatalf("expected 2 chapter-1 tabs, got %d (%#v)", len(tabs), tabs)
	}
	if tabs[0]["id"] != tab1 || tabs[1]["id"] != tab2 {
		t.Fatalf("unexpected initial tab order: %v, %v", tabs[0]["id"], tabs[1]["id"])
	}

	// Reorder: put tab2 first.
	reorder := do(http.MethodPut, "/tabs/reorder", `{"ids":["`+tab2+`","`+tab1+`"]}`)
	if reorder.Code != http.StatusOK {
		t.Fatalf("reorder tabs: %d %s", reorder.Code, reorder.Body.String())
	}
	listResp2 := do(http.MethodGet, "/tabs?chapterId=chapter-1", "")
	var tabs2 []map[string]any
	if err := json.Unmarshal(listResp2.Body.Bytes(), &tabs2); err != nil {
		t.Fatalf("decode tabs2: %v", err)
	}
	if tabs2[0]["id"] != tab2 || tabs2[1]["id"] != tab1 {
		t.Fatalf("reorder did not persist: %v, %v", tabs2[0]["id"], tabs2[1]["id"])
	}

	// view-state GET with no stored row returns the single-layout default.
	vsResp := do(http.MethodGet, "/view-state?chapterId=chapter-1", "")
	if vsResp.Code != http.StatusOK {
		t.Fatalf("get view-state default: %d %s", vsResp.Code, vsResp.Body.String())
	}
	var vsDefault map[string]any
	if err := json.Unmarshal(vsResp.Body.Bytes(), &vsDefault); err != nil {
		t.Fatalf("decode view-state default: %v", err)
	}
	if vsDefault["layout"] != "single" || vsDefault["activeTabId"] != nil {
		t.Fatalf("unexpected default view-state: %#v", vsDefault)
	}

	// PUT a split layout, then GET reflects it (round-trip persistence).
	putResp := do(http.MethodPut, "/view-state", `{"chapterId":"chapter-1","layout":"split-h","activeTabId":"`+tab2+`"}`)
	if putResp.Code != http.StatusOK {
		t.Fatalf("put view-state: %d %s", putResp.Code, putResp.Body.String())
	}
	vsResp2 := do(http.MethodGet, "/view-state?chapterId=chapter-1", "")
	var vsStored map[string]any
	if err := json.Unmarshal(vsResp2.Body.Bytes(), &vsStored); err != nil {
		t.Fatalf("decode stored view-state: %v", err)
	}
	if vsStored["layout"] != "split-h" || vsStored["activeTabId"] != tab2 {
		t.Fatalf("view-state round-trip mismatch: %#v", vsStored)
	}
}

func createTab(t *testing.T, do func(method, path, body string) *httptest.ResponseRecorder, body string) string {
	t.Helper()
	resp := do(http.MethodPost, "/tabs", body)
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("create tab failed: %d %s", resp.Code, resp.Body.String())
	}
	var tab map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &tab); err != nil {
		t.Fatalf("decode created tab: %v raw=%s", err, resp.Body.String())
	}
	id, _ := tab["id"].(string)
	if id == "" {
		t.Fatalf("created tab missing id: %#v", tab)
	}
	return id
}

func openTerminalTabRoundtripDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, language TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE terminal_tabs (
			id TEXT PRIMARY KEY,
			chapter_id TEXT,
			narrator_id TEXT,
			name TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
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
			t.Fatalf("exec tab roundtrip schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}
