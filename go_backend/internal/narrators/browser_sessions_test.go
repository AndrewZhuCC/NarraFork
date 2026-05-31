package narrators

import (
	"context"
	"database/sql"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/ws"
)

func TestBrowserSessionPersistenceIsDisabledByDefault(t *testing.T) {
	mainDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open main sqlite: %v", err)
	}
	mainDB.SetMaxOpenConns(1)
	if err := createNarratorTestSchema(mainDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _ = mainDB.Close() })

	service := NewService(mainDB, ws.NewHub(), nil)
	if service.BrowserSessionPersistenceEnabled {
		t.Fatal("browser session persistence should be disabled by default")
	}
	if err := service.EnsureBrowserSessionStorage(context.Background()); err != nil {
		t.Fatalf("ensure browser storage without opt-in: %v", err)
	}
	if ok, err := tableExistsOn(context.Background(), mainDB, "narrator_browser_sessions"); err != nil || ok {
		t.Fatalf("shared DB should stay untouched by default-off browser storage: ok=%v err=%v", ok, err)
	}
}

func TestBrowserSessionObserverBroadcastsWithoutPersistence(t *testing.T) {
	mainDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open main sqlite: %v", err)
	}
	mainDB.SetMaxOpenConns(1)
	if err := createNarratorTestSchema(mainDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _ = mainDB.Close() })

	hub := ws.NewHub()
	broadcasts := []map[string]any{}
	hub.SetNarratorBroadcastHook(func(narratorID string, payload any, data []byte) {
		if narratorID != "nar_browser" {
			return
		}
		if mapped, ok := payload.(map[string]any); ok {
			broadcasts = append(broadcasts, mapped)
		}
	})
	service := NewService(mainDB, hub, nil)
	service.BrowserSessionEvent(context.Background(), agentbasic.BrowserSessionEvent{
		Type: "created",
		Snapshot: agentbasic.BrowserSessionSnapshot{
			ID:           "browser-1",
			NarratorID:   "nar_browser",
			Status:       "active",
			URL:          "https://example.test",
			Title:        "Example",
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
			LastActivity: time.Now().UTC(),
			TtlMs:        60000,
		},
	})

	if ok, err := tableExistsOn(context.Background(), mainDB, "narrator_browser_sessions"); err != nil || ok {
		t.Fatalf("default-off browser observer should not create shared table: ok=%v err=%v", ok, err)
	}
	if len(broadcasts) < 2 {
		t.Fatalf("expected count and lifecycle broadcasts without persistence, got %#v", broadcasts)
	}
	if broadcasts[0]["type"] != "browser_session_count" || broadcasts[1]["type"] != "browser_session_created" {
		t.Fatalf("unexpected broadcasts without persistence: %#v", broadcasts)
	}
}

func newBrowserSessionTestService(t *testing.T) (*Service, *sql.DB, *sql.DB) {
	t.Helper()
	mainDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open main sqlite: %v", err)
	}
	mainDB.SetMaxOpenConns(1)
	if err := createNarratorTestSchema(mainDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	runtimeDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open runtime sqlite: %v", err)
	}
	runtimeDB.SetMaxOpenConns(1)
	service := NewService(mainDB, ws.NewHub(), nil)
	service.EnableBrowserSessionPersistence()
	service.SetRuntimeDB(runtimeDB)
	t.Cleanup(func() {
		_ = runtimeDB.Close()
		_ = mainDB.Close()
	})
	return service, mainDB, runtimeDB
}

func TestEnsureBrowserSessionStorageCreatesRuntimeSidecarTable(t *testing.T) {
	_, mainDB, sqlDB := newBrowserSessionTestService(t)
	if ok, err := tableExistsOn(context.Background(), mainDB, "narrator_browser_sessions"); err != nil || ok {
		t.Fatalf("shared DB should not contain narrator_browser_sessions after service init: ok=%v err=%v", ok, err)
	}
	var name string
	if err := sqlDB.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'narrator_browser_sessions'`).Scan(&name); err != nil {
		t.Fatalf("browser session table should be created by NewService: %v", err)
	}
	cols, err := tableColumnSet(context.Background(), sqlDB, "narrator_browser_sessions")
	if err != nil {
		t.Fatalf("inspect browser table: %v", err)
	}
	for _, col := range []string{"narrator_id", "ttl_ms", "expires_at_ms", "last_activity_ms", "headless", "metadata_json"} {
		if !cols[col] {
			t.Fatalf("expected browser session column %s in %#v", col, cols)
		}
	}
}

func TestBrowserSessionObserverPersistsLifecycleArtifactsAndBroadcastsCount(t *testing.T) {
	service, _, sqlDB := newBrowserSessionTestService(t)
	broadcasts := []map[string]any{}
	service.Hub.SetNarratorBroadcastHook(func(narratorID string, payload any, data []byte) {
		if mapped, ok := payload.(map[string]any); ok && mapped["type"] == "browser_session_count" {
			broadcasts = append(broadcasts, mapped)
		}
	})
	now := time.Now().UTC()
	service.BrowserSessionEvent(context.Background(), agentbasic.BrowserSessionEvent{
		Type: "created",
		Snapshot: agentbasic.BrowserSessionSnapshot{
			ID:                    "browser-1",
			NarratorID:            "nar_browser",
			Status:                "active",
			URL:                   "https://example.test",
			Title:                 "Example",
			Headless:              true,
			CreatedAt:             now,
			UpdatedAt:             now,
			LastActivity:          now,
			TtlMs:                 60000,
			ExpiresAt:             now.Add(time.Minute),
			NetworkCaptureEnabled: true,
			NetworkRequestCount:   3,
			ConsoleMessageCount:   2,
		},
	})
	service.BrowserSessionEvent(context.Background(), agentbasic.BrowserSessionEvent{
		Type: "artifact_saved",
		Snapshot: agentbasic.BrowserSessionSnapshot{
			ID:             "browser-1",
			NarratorID:     "nar_browser",
			Status:         "active",
			URL:            "https://example.test",
			Title:          "Example",
			Headless:       true,
			CreatedAt:      now,
			UpdatedAt:      now,
			LastActivity:   now,
			TtlMs:          60000,
			ExpiresAt:      now.Add(time.Minute),
			ScreenshotMime: "image/png",
			ScreenshotData: []byte("png-data"),
		},
	})
	var status string
	var ttlMs, expiresAtMs, lastActivityMs int64
	var screenshot string
	if err := sqlDB.QueryRow(`SELECT status, ttl_ms, expires_at_ms, last_activity_ms, screenshot_data_base64 FROM narrator_browser_sessions WHERE id = 'browser-1'`).Scan(&status, &ttlMs, &expiresAtMs, &lastActivityMs, &screenshot); err != nil {
		t.Fatalf("query browser row: %v", err)
	}
	if status != "active" || ttlMs != 60000 || expiresAtMs <= 0 || lastActivityMs <= 0 || screenshot != base64.StdEncoding.EncodeToString([]byte("png-data")) {
		t.Fatalf("unexpected persisted browser row: status=%s ttlMs=%d expires=%d last=%d screenshot=%q", status, ttlMs, expiresAtMs, lastActivityMs, screenshot)
	}
	service.BrowserSessionEvent(context.Background(), agentbasic.BrowserSessionEvent{Type: "updated", Snapshot: agentbasic.BrowserSessionSnapshot{ID: "browser-1", NarratorID: "nar_browser", Status: "active", URL: "https://example.test/next", Title: "Next", CreatedAt: now, UpdatedAt: now.Add(time.Second), LastActivity: now.Add(time.Second), TtlMs: 120000, ExpiresAt: now.Add(2 * time.Minute), TraceMetadata: map[string]any{"status": "active", "startedAt": now.UnixMilli()}}})
	service.BrowserSessionEvent(context.Background(), agentbasic.BrowserSessionEvent{Type: "artifact_saved", Snapshot: agentbasic.BrowserSessionSnapshot{ID: "browser-1", NarratorID: "nar_browser", Status: "active", URL: "https://example.test/next", Title: "Next", CreatedAt: now, UpdatedAt: now.Add(2 * time.Second), LastActivity: now.Add(2 * time.Second), TtlMs: 120000, ExpiresAt: now.Add(2 * time.Minute), TraceMetadata: map[string]any{"status": "stopped", "filePath": "/tmp/trace.json", "fileSize": 1234, "durationMs": 10}}})
	count, err := service.activeBrowserSessionCount(context.Background(), "nar_browser")
	if err != nil || count != 1 {
		t.Fatalf("active browser count = %d err=%v", count, err)
	}
	var tracingStatus, tracingRaw string
	if err := sqlDB.QueryRow(`SELECT tracing_status, tracing_json FROM narrator_browser_sessions WHERE id = 'browser-1'`).Scan(&tracingStatus, &tracingRaw); err != nil {
		t.Fatalf("query trace metadata: %v", err)
	}
	if tracingStatus != "stopped" || !strings.Contains(tracingRaw, "/tmp/trace.json") {
		t.Fatalf("trace metadata not persisted: status=%s raw=%s", tracingStatus, tracingRaw)
	}
	service.BrowserSessionEvent(context.Background(), agentbasic.BrowserSessionEvent{Type: "closed", Snapshot: agentbasic.BrowserSessionSnapshot{ID: "browser-1", NarratorID: "nar_browser", Status: "closed", UpdatedAt: now, CreatedAt: now, LastActivity: now}})
	count, err = service.activeBrowserSessionCount(context.Background(), "nar_browser")
	if err != nil || count != 0 {
		t.Fatalf("active browser count after close = %d err=%v", count, err)
	}
	if len(broadcasts) < 3 || broadcasts[len(broadcasts)-1]["activeBrowserSessions"] != 0 {
		t.Fatalf("expected browser_session_count broadcasts including final zero, got %#v", broadcasts)
	}
}

func TestBrowserSessionObserverDoesNotPersistOversizedScreenshots(t *testing.T) {
	service, _, sqlDB := newBrowserSessionTestService(t)
	now := time.Now().UTC()
	large := make([]byte, maxPersistedBrowserScreenshotBytes+1)
	service.BrowserSessionEvent(context.Background(), agentbasic.BrowserSessionEvent{Type: "artifact_saved", Snapshot: agentbasic.BrowserSessionSnapshot{ID: "browser-large", NarratorID: "nar_browser", Status: "active", CreatedAt: now, UpdatedAt: now, LastActivity: now, TtlMs: 60000, ExpiresAt: now.Add(time.Minute), ScreenshotMime: "image/png", ScreenshotData: large}})
	var screenshot sql.NullString
	var metadata string
	if err := sqlDB.QueryRow(`SELECT screenshot_data_base64, metadata_json FROM narrator_browser_sessions WHERE id = 'browser-large'`).Scan(&screenshot, &metadata); err != nil {
		t.Fatalf("query large screenshot row: %v", err)
	}
	if screenshot.Valid && screenshot.String != "" {
		t.Fatalf("oversized screenshot should not be persisted, got %d chars", len(screenshot.String))
	}
	if !strings.Contains(metadata, "screenshotTooLarge") {
		t.Fatalf("oversized screenshot metadata missing: %s", metadata)
	}
}
