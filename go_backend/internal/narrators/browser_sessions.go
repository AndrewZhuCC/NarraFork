package narrators

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/db"
)

const maxPersistedBrowserScreenshotBytes = 5 * 1024 * 1024

func (s *Service) EnsureBrowserSessionStorage(ctx context.Context) error {
	browserDB := s.browserDB()
	if browserDB == nil {
		return nil
	}
	if _, err := browserDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS narrator_browser_sessions (
		id TEXT PRIMARY KEY,
		narrator_id TEXT,
		status TEXT,
		url TEXT,
		title TEXT,
		ttl_seconds INTEGER,
		ttl_ms INTEGER,
		expires_at TEXT,
		expires_at_ms INTEGER,
		last_activity_ms INTEGER,
		headless INTEGER,
		screenshot_data_base64 TEXT,
		screenshot_mime TEXT,
		tracing_status TEXT,
		tracing_json TEXT,
		metadata_json TEXT,
		created_at TEXT,
		updated_at TEXT,
		closed_at TEXT
	)`); err != nil {
		return err
	}
	columns := map[string]string{
		"narrator_id":            "TEXT",
		"status":                 "TEXT",
		"url":                    "TEXT",
		"title":                  "TEXT",
		"ttl_seconds":            "INTEGER",
		"ttl_ms":                 "INTEGER",
		"expires_at":             "TEXT",
		"expires_at_ms":          "INTEGER",
		"last_activity_ms":       "INTEGER",
		"headless":               "INTEGER",
		"screenshot_data_base64": "TEXT",
		"screenshot_mime":        "TEXT",
		"tracing_status":         "TEXT",
		"tracing_json":           "TEXT",
		"metadata_json":          "TEXT",
		"created_at":             "TEXT",
		"updated_at":             "TEXT",
		"closed_at":              "TEXT",
	}
	existing, err := tableColumnSet(ctx, browserDB, "narrator_browser_sessions")
	if err != nil {
		return err
	}
	for name, typ := range columns {
		if !existing[name] {
			if _, err := browserDB.ExecContext(ctx, `ALTER TABLE narrator_browser_sessions ADD COLUMN `+name+` `+typ); err != nil {
				return err
			}
		}
	}
	if _, err := browserDB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_narrator_browser_sessions_narrator_status ON narrator_browser_sessions (narrator_id, status)`); err != nil {
		return err
	}
	return nil
}

func tableColumnSet(ctx context.Context, sqlDB *sql.DB, table string) (map[string]bool, error) {
	return db.TableColumnSet(ctx, sqlDB, table)
}

func (s *Service) BrowserSessionEvent(ctx context.Context, event agentbasic.BrowserSessionEvent) {
	if s == nil || s.browserDB() == nil || strings.TrimSpace(event.Snapshot.NarratorID) == "" {
		return
	}
	if err := s.persistBrowserSessionEvent(ctx, event); err != nil {
		return
	}
	s.broadcastBrowserSessionCount(ctx, event.Snapshot.NarratorID)
	if s.Hub != nil {
		payload := map[string]any{
			"type":       "browser_session_" + strings.TrimSpace(event.Type),
			"narratorId": event.Snapshot.NarratorID,
			"sessionId":  event.Snapshot.ID,
			"status":     event.Snapshot.Status,
		}
		s.Hub.BroadcastNarrator(event.Snapshot.NarratorID, payload)
	}
}

func (s *Service) persistBrowserSessionEvent(ctx context.Context, event agentbasic.BrowserSessionEvent) error {
	if err := s.EnsureBrowserSessionStorage(ctx); err != nil {
		return err
	}
	browserDB := s.browserDB()
	if browserDB == nil {
		return nil
	}
	snapshot := event.Snapshot
	if strings.TrimSpace(snapshot.Status) == "" {
		snapshot.Status = "active"
	}
	metadata := map[string]any{
		"chromePath":            snapshot.ChromePath,
		"networkRequestCount":   snapshot.NetworkRequestCount,
		"consoleMessageCount":   snapshot.ConsoleMessageCount,
		"networkCaptureEnabled": snapshot.NetworkCaptureEnabled,
	}
	if snapshot.Reason != "" {
		metadata["reason"] = snapshot.Reason
	}
	if snapshot.ScreenshotFilePath != "" {
		metadata["screenshotFilePath"] = snapshot.ScreenshotFilePath
	}
	if len(snapshot.ScreenshotData) > maxPersistedBrowserScreenshotBytes {
		metadata["screenshotTooLarge"] = true
		metadata["screenshotBytes"] = len(snapshot.ScreenshotData)
		snapshot.ScreenshotData = nil
	}
	metadataJSON := jsonString(metadata, "{}")
	traceJSON := ""
	tracingStatus := ""
	if snapshot.TraceMetadata != nil {
		traceJSON = jsonString(snapshot.TraceMetadata, "{}")
		tracingStatus = stringMapValue(snapshot.TraceMetadata, "status")
	}
	if tracingStatus == "" && snapshot.TracingActive {
		tracingStatus = "active"
		trace := map[string]any{"active": true}
		if snapshot.TracingStartedAt != nil {
			trace["startedAt"] = snapshot.TracingStartedAt.UnixMilli()
		}
		traceJSON = jsonString(trace, "{}")
	}
	var screenshotBase64 any
	var screenshotMime any
	if len(snapshot.ScreenshotData) > 0 {
		screenshotBase64 = base64.StdEncoding.EncodeToString(snapshot.ScreenshotData)
		screenshotMime = browserFirstString(snapshot.ScreenshotMime, "image/png")
	}
	createdAt := isoOrNow(snapshot.CreatedAt)
	updatedAt := isoOrNow(snapshot.UpdatedAt)
	lastActivityMs := snapshot.LastActivity.UnixMilli()
	if snapshot.LastActivity.IsZero() {
		lastActivityMs = time.Now().UnixMilli()
	}
	expiresAt := ""
	expiresAtMs := int64(0)
	if !snapshot.ExpiresAt.IsZero() {
		expiresAt = snapshot.ExpiresAt.UTC().Format(time.RFC3339Nano)
		expiresAtMs = snapshot.ExpiresAt.UnixMilli()
	}
	closedAt := any(nil)
	if snapshot.Status == "closed" || snapshot.Status == "expired" {
		closedAt = db.NowISO()
	}
	_, err := browserDB.ExecContext(ctx, `INSERT INTO narrator_browser_sessions (
		id, narrator_id, status, url, title, ttl_seconds, ttl_ms, expires_at, expires_at_ms, last_activity_ms,
		headless, screenshot_data_base64, screenshot_mime, tracing_status, tracing_json, metadata_json, created_at, updated_at, closed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		narrator_id = excluded.narrator_id,
		status = excluded.status,
		url = excluded.url,
		title = excluded.title,
		ttl_seconds = excluded.ttl_seconds,
		ttl_ms = excluded.ttl_ms,
		expires_at = excluded.expires_at,
		expires_at_ms = excluded.expires_at_ms,
		last_activity_ms = excluded.last_activity_ms,
		headless = excluded.headless,
		screenshot_data_base64 = COALESCE(excluded.screenshot_data_base64, narrator_browser_sessions.screenshot_data_base64),
		screenshot_mime = COALESCE(excluded.screenshot_mime, narrator_browser_sessions.screenshot_mime),
		tracing_status = COALESCE(NULLIF(excluded.tracing_status, ''), narrator_browser_sessions.tracing_status),
		tracing_json = COALESCE(NULLIF(excluded.tracing_json, ''), narrator_browser_sessions.tracing_json),
		metadata_json = excluded.metadata_json,
		updated_at = excluded.updated_at,
		closed_at = COALESCE(excluded.closed_at, narrator_browser_sessions.closed_at)`,
		snapshot.ID,
		snapshot.NarratorID,
		snapshot.Status,
		browserNullableString(snapshot.URL),
		browserNullableString(snapshot.Title),
		nullableInt64(snapshot.TtlMs/1000),
		nullableInt64(snapshot.TtlMs),
		browserNullableString(expiresAt),
		nullableInt64(expiresAtMs),
		lastActivityMs,
		boolToInt(snapshot.Headless),
		screenshotBase64,
		screenshotMime,
		browserNullableString(tracingStatus),
		browserNullableString(traceJSON),
		metadataJSON,
		createdAt,
		updatedAt,
		closedAt,
	)
	return err
}

func (s *Service) broadcastBrowserSessionCount(ctx context.Context, narratorID string) {
	if s == nil || s.Hub == nil || s.browserDB() == nil || strings.TrimSpace(narratorID) == "" {
		return
	}
	count, err := s.activeBrowserSessionCount(ctx, narratorID)
	if err != nil {
		count = len(agentbasic.LiveBrowserSessionSnapshots(narratorID))
	}
	s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "browser_session_count", "narratorId": narratorID, "activeBrowserSessions": count})
}

func (s *Service) activeBrowserSessionCount(ctx context.Context, narratorID string) (int, error) {
	if err := s.EnsureBrowserSessionStorage(ctx); err != nil {
		return 0, err
	}
	browserDB := s.browserDB()
	if browserDB == nil {
		return 0, sql.ErrNoRows
	}
	var count int
	err := browserDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_browser_sessions WHERE narrator_id = ? AND status IN ('active', 'running', 'idle')`, narratorID).Scan(&count)
	return count, err
}

func (s *Service) importLegacyBrowserSessions(ctx context.Context) error {
	if s == nil || s.DB == nil || s.RuntimeDB == nil || s.DB == s.RuntimeDB {
		return nil
	}
	var runtimeCount int
	if err := s.RuntimeDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_browser_sessions`).Scan(&runtimeCount); err == nil && runtimeCount > 0 {
		return nil
	}
	_, err := CopyBrowserSessionStorage(ctx, s.DB, s.RuntimeDB)
	return err
}

func jsonString(value any, fallback string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(data)
}

func browserNullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isoOrNow(value time.Time) string {
	if value.IsZero() {
		return db.NowISO()
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func browserFirstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringMapValue(value map[string]any, key string) string {
	if raw, ok := value[key].(string); ok {
		return raw
	}
	return ""
}

func isMissingBrowserSessionTable(err error) bool {
	return err != nil && (errors.Is(err, sql.ErrNoRows) || strings.Contains(strings.ToLower(err.Error()), "no such table"))
}
