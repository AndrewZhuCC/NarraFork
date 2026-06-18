package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

// TestPutDraftPromotesNarratorRecentTab verifies the W634 gap fix: a newly created
// draft promotes the narrator's recent tab to the front of the unpinned section
// across all users (mirroring TS syncNarratorDraftToRecentTabs promote), while a
// draft UPDATE (draft already existed) does NOT re-promote.
func TestPutDraftPromotesNarratorRecentTab(t *testing.T) {
	sqlDB := openDraftSyncTestDB(t)
	defer sqlDB.Close()

	insertDraftSyncNarrator(t, sqlDB, "nar_a")
	insertDraftSyncNarrator(t, sqlDB, "nar_b")
	setDraftSyncRecentTabs(t, sqlDB, "user_1", []map[string]any{
		{"type": "narrator", "id": "nar_a"},
		{"type": "narrator", "id": "nar_b"},
	})

	service := &Service{DB: sqlDB}

	// New draft on nar_b -> promoted to front.
	w := serveDraftSyncPut(service, "nar_b", `{"text":"hello"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("put draft got %d body=%s", w.Code, w.Body.String())
	}
	got := getDraftSyncRecentTabs(t, sqlDB, "user_1")
	if len(got) != 2 || got[0]["id"] != "nar_b" || got[1]["id"] != "nar_a" {
		t.Fatalf("new draft should promote nar_b to front, got %#v", got)
	}

	// nar_b already has a draft now; UPDATING it must NOT promote.
	setDraftSyncRecentTabs(t, sqlDB, "user_1", []map[string]any{
		{"type": "narrator", "id": "nar_a"},
		{"type": "narrator", "id": "nar_b"},
	})
	w2 := serveDraftSyncPut(service, "nar_b", `{"text":"hello again"}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("update draft got %d body=%s", w2.Code, w2.Body.String())
	}
	got2 := getDraftSyncRecentTabs(t, sqlDB, "user_1")
	if got2[0]["id"] != "nar_a" || got2[1]["id"] != "nar_b" {
		t.Fatalf("updating an existing draft must NOT re-promote, got %#v", got2)
	}
}

// TestPutDraftPromoteRespectsPins verifies pinned tabs stay ahead of the promoted
// narrator tab.
func TestPutDraftPromoteRespectsPins(t *testing.T) {
	sqlDB := openDraftSyncTestDB(t)
	defer sqlDB.Close()
	insertDraftSyncNarrator(t, sqlDB, "nar_pinned")
	insertDraftSyncNarrator(t, sqlDB, "nar_target")
	insertDraftSyncNarrator(t, sqlDB, "nar_other")
	setDraftSyncRecentTabs(t, sqlDB, "user_1", []map[string]any{
		{"type": "narrator", "id": "nar_pinned", "pinned": true},
		{"type": "narrator", "id": "nar_other"},
		{"type": "narrator", "id": "nar_target"},
	})
	service := &Service{DB: sqlDB}
	if w := serveDraftSyncPut(service, "nar_target", `{"text":"draft"}`); w.Code != http.StatusOK {
		t.Fatalf("put draft got %d body=%s", w.Code, w.Body.String())
	}
	got := getDraftSyncRecentTabs(t, sqlDB, "user_1")
	if got[0]["id"] != "nar_pinned" || got[1]["id"] != "nar_target" || got[2]["id"] != "nar_other" {
		t.Fatalf("promote should respect pins (pinned first, then target), got %#v", got)
	}
}

// TestPutTitleSyncsRecentTabTitle verifies the W635-followup: a narrator title
// update propagates into every user's recent_tabs (mirroring TS
// syncNarratorTitleToRecentTabs), covering both a direct narrator tab and a
// chapter tab whose narratorId matches.
func TestPutTitleSyncsRecentTabTitle(t *testing.T) {
	sqlDB := openDraftSyncTestDB(t)
	defer sqlDB.Close()
	insertDraftSyncNarrator(t, sqlDB, "nar_x")
	setDraftSyncRecentTabs(t, sqlDB, "user_1", []map[string]any{
		{"type": "narrator", "id": "nar_x", "title": "Old"},
		{"type": "chapter", "id": "chap_1", "narratorId": "nar_x", "title": "Old chapter tab"},
		{"type": "narrator", "id": "nar_other", "title": "Untouched"},
	})
	service := &Service{DB: sqlDB}
	// Exercise the sync directly (the HTTP title handler additionally calls
	// UpdateTextField->Get, which needs the full narrators schema; this test
	// targets the recent_tabs title-sync side effect in isolation).
	service.syncTitleToRecentTabs(context.Background(), "nar_x", "New Title")
	got := getDraftSyncRecentTabs(t, sqlDB, "user_1")
	if got[0]["title"] != "New Title" {
		t.Fatalf("narrator tab title should sync, got %#v", got[0])
	}
	if got[1]["title"] != "New Title" {
		t.Fatalf("chapter tab (matching narratorId) title should sync, got %#v", got[1])
	}
	if got[2]["title"] != "Untouched" {
		t.Fatalf("unrelated tab title must not change, got %#v", got[2])
	}
}

// ── test helpers ────────────────────────────────────────────────────────────

func openDraftSyncTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, traits TEXT NOT NULL DEFAULT '[]', updated_at TEXT)`,
		`CREATE TABLE user_preferences (user_id TEXT PRIMARY KEY, recent_tabs TEXT, updated_at TEXT)`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create draft-sync schema: %v", err)
		}
	}
	return sqlDB
}

func insertDraftSyncNarrator(t *testing.T, sqlDB *sql.DB, id string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, traits) VALUES (?, '[]')`, id); err != nil {
		t.Fatalf("insert narrator %s: %v", id, err)
	}
}

func setDraftSyncRecentTabs(t *testing.T, sqlDB *sql.DB, userID string, tabs []map[string]any) {
	t.Helper()
	data, _ := json.Marshal(tabs)
	if _, err := sqlDB.Exec(`INSERT INTO user_preferences (user_id, recent_tabs) VALUES (?, ?) ON CONFLICT(user_id) DO UPDATE SET recent_tabs = excluded.recent_tabs`, userID, string(data)); err != nil {
		t.Fatalf("set recent tabs: %v", err)
	}
}

func getDraftSyncRecentTabs(t *testing.T, sqlDB *sql.DB, userID string) []map[string]any {
	t.Helper()
	var raw string
	if err := sqlDB.QueryRow(`SELECT recent_tabs FROM user_preferences WHERE user_id = ?`, userID).Scan(&raw); err != nil {
		t.Fatalf("read recent tabs: %v", err)
	}
	var tabs []map[string]any
	if err := json.Unmarshal([]byte(raw), &tabs); err != nil {
		t.Fatalf("decode recent tabs: %v", err)
	}
	return tabs
}

func serveDraftSyncPut(service *Service, narratorID, body string) *httptest.ResponseRecorder {
	h := NewHandler(service)
	r := chi.NewRouter()
	r.Route("/narrators", h.RegisterRoutes)
	req := httptest.NewRequest(http.MethodPut, "/narrators/"+narratorID+"/draft", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
