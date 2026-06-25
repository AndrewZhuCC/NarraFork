package misccompat

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/httpx"
	"narrafork-go/internal/narrators"
)

func TestPublicShareRegistrationExposesOnlyDownloadAndPreview(t *testing.T) {
	router := chi.NewRouter()
	h := NewHandler(nil)
	router.Route("/api/shares", h.RegisterPublicShares)

	for _, tc := range []struct {
		method string
		path   string
		match  bool
	}{
		{method: http.MethodGet, path: "/api/shares/share-id", match: true},
		{method: http.MethodGet, path: "/api/shares/share-id/preview", match: true},
	} {
		rctx := chi.NewRouteContext()
		if matched := router.Match(rctx, tc.method, tc.path); matched != tc.match {
			t.Fatalf("Match(%s %s) = %v, want %v", tc.method, tc.path, matched, tc.match)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/shares/", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("public POST /api/shares/ should not be registered, got status %d", w.Code)
	}
}

func TestStorageCleanupUnsupportedTargetsDoNotFakeSuccessOrMutate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	worktreeFile := filepath.Join(home, "repo", ".worktrees", "branch", "worktree.txt")
	if err := os.MkdirAll(filepath.Dir(worktreeFile), 0o755); err != nil {
		t.Fatalf("create worktree fixture: %v", err)
	}
	if err := os.WriteFile(worktreeFile, []byte("preserve"), 0o644); err != nil {
		t.Fatalf("write worktree fixture: %v", err)
	}
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	for _, stmt := range []string{
		`CREATE TABLE container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, status TEXT)`,
		`CREATE TABLE port_allocations (port INTEGER PRIMARY KEY, chapter_id TEXT, service_name TEXT)`,
		`INSERT INTO container_instances (id, chapter_id, status) VALUES ('ci-1', 'chapter-1', 'running')`,
		`INSERT INTO port_allocations (port, chapter_id, service_name) VALUES (10001, 'chapter-1', 'web')`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("prepare cleanup fixture: %v", err)
		}
	}

	h := NewHandler(database)
	for _, tc := range []struct {
		target  string
		code    string
		feature string
	}{
		{target: "worktrees", code: "STORAGE_CLEANUP_WORKTREES_UNSUPPORTED", feature: "storage.cleanup.worktrees"},
		{target: "containers", code: "STORAGE_CLEANUP_CONTAINERS_UNSUPPORTED", feature: "storage.cleanup.containers"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/storage/cleanup", strings.NewReader(`{"target":"`+tc.target+`"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if err := h.cleanupStorage(w, req); err != nil {
			t.Fatalf("cleanup %s: %v", tc.target, err)
		}
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("cleanup %s status = %d body=%s", tc.target, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode cleanup %s: %v", tc.target, err)
		}
		if body["ok"] != false || body["code"] != tc.code || body["feature"] != tc.feature || body["supported"] != false || body["fallback"] != true {
			t.Fatalf("cleanup %s returned fake or malformed unsupported payload: %#v", tc.target, body)
		}
		reason, _ := body["reason"].(string)
		if reason == "" || body["error"] != reason || body["message"] != reason {
			t.Fatalf("cleanup %s fallback should expose reason/error/message, got %#v", tc.target, body)
		}
		if _, err := os.Stat(worktreeFile); err != nil {
			t.Fatalf("cleanup %s should not remove storage worktree fixture: %v", tc.target, err)
		}
		assertCleanupRowCount(t, database, `SELECT COUNT(*) FROM container_instances`, 1)
		assertCleanupRowCount(t, database, `SELECT COUNT(*) FROM port_allocations`, 1)
	}
}

func TestStorageCachedReturnsLastCompletedScan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir narrafork root: %v", err)
	}

	database, err := sql.Open("sqlite", filepath.Join(root, "narrafork.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, updated_at TEXT)`); err != nil {
		t.Fatalf("create projects: %v", err)
	}

	repo := filepath.Join(home, "repo")
	for path, contents := range map[string][]byte{
		filepath.Join(root, "uploads", "nar-1", "upload.txt"):       []byte("upload"),
		filepath.Join(root, "shares", "share-1", "preview.txt"):     []byte("share"),
		filepath.Join(repo, ".worktrees", "branch", "worktree.txt"): []byte("worktree"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, updated_at) VALUES ('p1', 'Project', ?, '2026-01-01T00:00:00Z')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	h := NewHandler(database)
	if cached := h.storageCachedResponse(); cached["cached"] != false {
		t.Fatalf("initial cached response = %#v, want cached=false", cached)
	}

	w := httptest.NewRecorder()
	h.storageScan(w, httptest.NewRequest(http.MethodGet, "/api/storage/scan", nil))
	if !strings.Contains(w.Body.String(), "event:complete") {
		t.Fatalf("scan did not emit complete event: %s", w.Body.String())
	}

	cached := h.storageCachedResponse()
	if cached["cached"] != true {
		t.Fatalf("cached response after scan = %#v, want cached=true", cached)
	}
	data, ok := cached["data"].(map[string]any)
	if !ok {
		t.Fatalf("cached data missing or wrong type: %#v", cached)
	}
	categories, ok := data["categories"].([]map[string]any)
	if !ok || len(categories) != 5 {
		t.Fatalf("cached categories = %#v, want 5 storage categories", data["categories"])
	}
	seen := map[string]bool{}
	for _, category := range categories {
		key, _ := category["key"].(string)
		seen[key] = true
	}
	for _, key := range []string{"database", "uploads", "shares", "worktrees", "containers"} {
		if !seen[key] {
			t.Fatalf("cached categories missing %s: %#v", key, categories)
		}
	}
	if int64Number(data["totalBytes"]) <= 0 || int64Number(data["scannedAt"]) <= 0 {
		t.Fatalf("cached scan should include positive totalBytes and scannedAt: %#v", data)
	}
}

func TestStorageCleanupInvalidatesCompletedScanCache(t *testing.T) {
	for _, target := range []string{"shares", "uploads"} {
		t.Run(target, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			database, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			defer database.Close()
			if _, err := database.Exec(`CREATE TABLE narrators (id TEXT PRIMARY KEY); CREATE TABLE narrator_messages (narrator_id TEXT, content_json TEXT)`); err != nil {
				t.Fatalf("create upload cleanup tables: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(uploadsRoot(), "stale-upload"), 0o755); err != nil {
				t.Fatalf("create upload fixture: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(sharesRoot(), "stale-share"), 0o755); err != nil {
				t.Fatalf("create share fixture: %v", err)
			}

			h := NewHandler(database)
			h.setStorageScanCache(map[string]any{"categories": []any{}, "totalBytes": int64(1), "scannedAt": int64(123)})
			if cached := h.storageCachedResponse(); cached["cached"] != true {
				t.Fatalf("seeded cached response = %#v, want cached=true", cached)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/storage/cleanup", strings.NewReader(`{"target":"`+target+`"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			if err := h.cleanupStorage(w, req); err != nil {
				t.Fatalf("cleanup %s: %v", target, err)
			}
			if w.Code != http.StatusOK {
				t.Fatalf("cleanup %s status = %d body=%s", target, w.Code, w.Body.String())
			}
			if cached := h.storageCachedResponse(); cached["cached"] != false {
				t.Fatalf("cached response after cleanup = %#v, want cached=false", cached)
			}
		})
	}
}

func TestDatabaseCleanupInvalidatesStorageScanCache(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE api_requests (id TEXT PRIMARY KEY, raw_dump_json TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create api_requests: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO api_requests (id, raw_dump_json, created_at) VALUES ('req-old', '{"dump":true}', '2020-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert api request: %v", err)
	}

	h := NewHandler(database)
	h.setStorageScanCache(map[string]any{"categories": []any{}, "totalBytes": int64(1), "scannedAt": int64(123)})
	req := httptest.NewRequest(http.MethodPost, "/api/storage/database/cleanup", strings.NewReader(`{"target":"apiRequestDumps","olderThanDays":30}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.databaseCleanup(w, req); err != nil {
		t.Fatalf("database cleanup apiRequestDumps: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("database cleanup status = %d body=%s", w.Code, w.Body.String())
	}
	if cached := h.storageCachedResponse(); cached["cached"] != false {
		t.Fatalf("cached response after database cleanup = %#v, want cached=false", cached)
	}
	var rawDump sql.NullString
	if err := database.QueryRow(`SELECT raw_dump_json FROM api_requests WHERE id = 'req-old'`).Scan(&rawDump); err != nil {
		t.Fatalf("load cleaned request: %v", err)
	}
	if rawDump.Valid {
		t.Fatalf("raw_dump_json should be cleared, got %q", rawDump.String)
	}
}

func TestDatabaseCleanupStaleSessionsReturnsArchivedOnlyFeatureDisabledFallback(t *testing.T) {
	h := NewHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/storage/database/cleanup", strings.NewReader(`{"target":"staleSessions"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.databaseCleanup(w, req); err != nil {
		t.Fatalf("database cleanup staleSessions returned error: %v", err)
	}
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 feature disabled, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode fallback body: %v", err)
	}
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "storage.database.cleanup.staleSessions" || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected stale cleanup fallback body: %#v", body)
	}
	reason, _ := body["reason"].(string)
	if !strings.Contains(reason, "archived-only deletion policy") || body["error"] != reason || body["message"] != reason {
		t.Fatalf("stale cleanup fallback should expose archived-only reason/error/message, got %#v", body)
	}
}

func TestDatabaseCleanupArchivedSessionsMissingNarratorServiceReturnsFeatureDisabled(t *testing.T) {
	h := NewHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/storage/database/cleanup", strings.NewReader(`{"target":"archivedSessions"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.databaseCleanup(w, req); err != nil {
		t.Fatalf("database cleanup archivedSessions returned error: %v", err)
	}
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 feature disabled, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode fallback body: %v", err)
	}
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "storage.database.cleanup.archivedSessions" || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected archived cleanup fallback body: %#v", body)
	}
	reason, _ := body["reason"].(string)
	if !strings.Contains(reason, "safe narrator delete path") || !strings.Contains(reason, "h.Narrators") || body["error"] != reason || body["message"] != reason {
		t.Fatalf("archived cleanup fallback should expose missing narrator service reason, got %#v", body)
	}
}

func TestDatabaseVacuumRouteAlwaysReturnsDisabledConflict(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()

	h := NewHandler(database)
	req := httptest.NewRequest(http.MethodPost, "/api/storage/database/vacuum", nil)
	w := httptest.NewRecorder()

	if err := h.databaseVacuum(w, req); err != nil {
		t.Fatalf("database vacuum returned error: %v", err)
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 disabled conflict, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode vacuum body: %v", err)
	}
	errorMessage, _ := body["error"].(string)
	if !strings.Contains(errorMessage, "VACUUM is disabled") {
		t.Fatalf("unexpected vacuum error: %#v", body)
	}
	if body["vacuumRan"] != false || body["checkpointRan"] != false || body["optimized"] != false {
		t.Fatalf("vacuum route must not run request-path maintenance: %#v", body)
	}
}

func TestStorageCleanupUploadsPreservesMessageImageOwners(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := uploadsRoot()
	for _, dir := range []string{"avatars", "active-narrator", "referenced-deleted", "stale-deleted"} {
		path := filepath.Join(root, dir)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create upload dir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(path, "image.png"), []byte(dir), 0o644); err != nil {
			t.Fatalf("write upload fixture %s: %v", dir, err)
		}
	}

	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	for _, stmt := range []string{
		`CREATE TABLE narrators (id TEXT PRIMARY KEY)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, content_json TEXT NOT NULL)`,
		`INSERT INTO narrators (id) VALUES ('active-narrator')`,
		`INSERT INTO narrator_messages (id, narrator_id, content_json) VALUES ('msg-image', 'referenced-deleted', '[{"type":"image","imageId":"img1"}]')`,
		`INSERT INTO narrator_messages (id, narrator_id, content_json) VALUES ('msg-text', 'stale-deleted', '[{"type":"text","text":"hello"}]')`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("prepare upload cleanup fixture: %v", err)
		}
	}

	h := NewHandler(database)
	req := httptest.NewRequest(http.MethodPost, "/api/storage/cleanup", nil)
	removed, freed := h.cleanupOrphanedUploads(req)
	if removed != 1 || freed == 0 {
		t.Fatalf("cleanup removed/freed = %d/%d, want one stale directory removed", removed, freed)
	}
	for _, dir := range []string{"avatars", "active-narrator", "referenced-deleted"} {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Fatalf("expected %s to be preserved: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "stale-deleted")); !os.IsNotExist(err) {
		t.Fatalf("expected stale-deleted to be removed, stat err=%v", err)
	}
}

func TestUpdateRebasedChapterMetadataReturnsUpdateErrors(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	for _, stmt := range []string{
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, start_commit_sha TEXT, head_commit_sha TEXT, updated_at TEXT)`,
		`INSERT INTO chapters (id, start_commit_sha, head_commit_sha, updated_at) VALUES ('chapter-1', 'old', 'old', 'now')`,
		`CREATE TRIGGER fail_rebase_metadata_update BEFORE UPDATE OF head_commit_sha ON chapters BEGIN SELECT RAISE(ABORT, 'rebase metadata update blocked'); END;`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("prepare rebase metadata fixture: %v", err)
		}
	}
	h := NewHandler(database)

	err = h.updateRebasedChapterMetadata(context.Background(), "chapter-1", "trunk", "rebased")
	if err == nil || !strings.Contains(err.Error(), "rebase metadata update blocked") {
		t.Fatalf("expected rebase metadata update error, got %v", err)
	}
}

func TestDatabaseTableSummariesMarkRowCountErrorsDegraded(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE "bad-name" (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create bad-name table: %v", err)
	}
	h := NewHandler(database)
	req := httptest.NewRequest(http.MethodGet, "/api/storage/database", nil)

	tables, _ := h.databaseTableSummaries(req)
	if len(tables) != 1 {
		t.Fatalf("expected one table summary, got %#v", tables)
	}
	if tables[0]["name"] != "bad-name" || tables[0]["degraded"] != true || tables[0]["fallback"] != true {
		t.Fatalf("expected degraded table summary, got %#v", tables[0])
	}
	fallbacks, _ := tables[0]["fallbacks"].([]any)
	if len(fallbacks) != 1 || fallbacks[0] != "table_row_count_failed" {
		t.Fatalf("expected row count fallback, got %#v", tables[0])
	}
	reason, _ := tables[0]["reason"].(string)
	if reason == "" || tables[0]["error"] != reason || tables[0]["message"] != reason {
		t.Fatalf("degraded table summary should expose reason/error/message, got %#v", tables[0])
	}
}

func TestSourceChapterCwdReturnsQueryErrors(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE chapters (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create chapters: %v", err)
	}
	h := NewHandler(database)

	_, err = h.sourceChapterCwd(context.Background(), "chapter-1", "narrator-1")
	if err == nil || !strings.Contains(err.Error(), "worktree_path") {
		t.Fatalf("expected source chapter cwd query error, got %v", err)
	}
}

func TestNarratorCleanupSamplesMarkQueryErrorsDegraded(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE narrators (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create narrators: %v", err)
	}
	h := NewHandler(database)
	req := httptest.NewRequest(http.MethodGet, "/api/storage/cleanup", nil)

	samples := h.narratorCleanupSamples(req, []string{"narrator-1"}, 5)
	if len(samples) != 1 {
		t.Fatalf("expected one sample, got %#v", samples)
	}
	sample, _ := samples[0].(map[string]any)
	if sample["degraded"] != true || sample["fallback"] != true {
		t.Fatalf("expected degraded sample, got %#v", sample)
	}
	reason, _ := sample["reason"].(string)
	if reason == "" || sample["error"] != reason || sample["message"] != reason {
		t.Fatalf("degraded narrator cleanup sample should expose reason/error/message, got %#v", sample)
	}
}

func TestDatabaseCleanupPreviewMarksApiDumpStatsErrorsDegraded(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE api_requests (id TEXT PRIMARY KEY, created_at TEXT)`); err != nil {
		t.Fatalf("create api_requests: %v", err)
	}
	h := NewHandler(database)
	req := httptest.NewRequest(http.MethodPost, "/api/storage/database/preview", nil)

	preview := h.previewDatabaseCleanup(req, "apiRequestDumps", 30, 0)
	if preview["degraded"] != true || preview["fallback"] != true {
		t.Fatalf("expected degraded preview, got %#v", preview)
	}
	fallbacks, _ := preview["fallbacks"].([]any)
	found := false
	for _, fallback := range fallbacks {
		if fallback == "api_request_dump_stats_failed" {
			found = true
			break
		}
	}
	if !found || preview["count"] != int64(0) {
		t.Fatalf("unexpected degraded preview metadata: %#v", preview)
	}
	reason, _ := preview["reason"].(string)
	if reason == "" || preview["error"] != reason || preview["message"] != reason {
		t.Fatalf("degraded cleanup preview should expose reason/error/message, got %#v", preview)
	}
}

func TestDatabaseCleanupPreviewRejectsOutOfRangeZodBounds(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE api_requests (id TEXT PRIMARY KEY, raw_dump_json TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create api_requests: %v", err)
	}
	h := NewHandler(database)

	for _, tc := range []struct {
		name string
		body string
	}{
		{"olderThanDays-too-large", `{"target":"apiRequestDumps","olderThanDays":3651}`},
		{"olderThanDays-non-positive", `{"target":"apiRequestDumps","olderThanDays":0}`},
		{"sampleLimit-too-large", `{"target":"apiRequestDumps","sampleLimit":26}`},
		{"sampleLimit-negative", `{"target":"apiRequestDumps","sampleLimit":-1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/storage/database/preview", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			err := h.databaseCleanupPreview(w, req)
			var appErr *httpx.AppError
			if !errors.As(err, &appErr) || appErr.Status != http.StatusBadRequest {
				t.Fatalf("expected 400 AppError for %s, got %T %v (status w=%d)", tc.name, err, err, w.Code)
			}
		})
	}

	// In-range bounds still succeed (max olderThanDays/sampleLimit).
	req := httptest.NewRequest(http.MethodPost, "/api/storage/database/preview", strings.NewReader(`{"target":"apiRequestDumps","olderThanDays":3650,"sampleLimit":25}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.databaseCleanupPreview(w, req); err != nil {
		t.Fatalf("in-range preview returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected in-range preview 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDatabaseCleanupRejectsOutOfRangeOlderThanDays(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE api_requests (id TEXT PRIMARY KEY, raw_dump_json TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create api_requests: %v", err)
	}
	h := NewHandler(database)

	for _, tc := range []struct {
		name string
		body string
	}{
		{"too-large", `{"target":"apiRequestDumps","olderThanDays":3651}`},
		{"non-positive", `{"target":"apiRequestDumps","olderThanDays":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/storage/database/cleanup", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			err := h.databaseCleanup(w, req)
			var appErr *httpx.AppError
			if !errors.As(err, &appErr) || appErr.Status != http.StatusBadRequest {
				t.Fatalf("expected 400 AppError for %s, got %T %v (status w=%d)", tc.name, err, err, w.Code)
			}
		})
	}

	// In-range max olderThanDays still runs cleanup (apiRequestDumps clears dumps).
	req := httptest.NewRequest(http.MethodPost, "/api/storage/database/cleanup", strings.NewReader(`{"target":"apiRequestDumps","olderThanDays":3650}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.databaseCleanup(w, req); err != nil {
		t.Fatalf("in-range cleanup returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected in-range cleanup 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDatabaseCleanupInvalidTargetMatchesTSSchemaMessage(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE api_requests (id TEXT PRIMARY KEY, raw_dump_json TEXT, created_at TEXT)`); err != nil {
		t.Fatalf("create api_requests: %v", err)
	}
	h := NewHandler(database)

	// Mirror the TypeScript storage routes: an unknown `target` fails the Zod enum
	// in databasePreviewSchema/databaseCleanupSchema and returns the single unified
	// 400 message, NOT the deeper "Invalid database cleanup target" string.
	for _, tc := range []struct {
		name    string
		path    string
		body    string
		handler func(http.ResponseWriter, *http.Request) error
		want    string
	}{
		{"preview unknown target", "/api/storage/database/preview", `{"target":"bogus"}`, h.databaseCleanupPreview, "Invalid database cleanup preview request"},
		{"preview missing target", "/api/storage/database/preview", `{"olderThanDays":30}`, h.databaseCleanupPreview, "Invalid database cleanup preview request"},
		{"preview uploads target", "/api/storage/database/preview", `{"target":"uploads"}`, h.databaseCleanupPreview, "Invalid database cleanup preview request"},
		{"cleanup unknown target", "/api/storage/database/cleanup", `{"target":"bogus"}`, h.databaseCleanup, "Invalid database cleanup request"},
		{"cleanup missing target", "/api/storage/database/cleanup", `{"olderThanDays":30}`, h.databaseCleanup, "Invalid database cleanup request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			err := tc.handler(w, req)
			var appErr *httpx.AppError
			if !errors.As(err, &appErr) || appErr.Status != http.StatusBadRequest {
				t.Fatalf("expected 400 AppError for %s, got %T %v (status w=%d)", tc.name, err, err, w.Code)
			}
			if appErr.Message != tc.want {
				t.Fatalf("expected message %q for %s, got %q", tc.want, tc.name, appErr.Message)
			}
		})
	}
}

func TestDatabaseCleanupArchivedSessionsDeletesThroughNarratorService(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	database := openDatabaseCleanupNarratorTestDB(t)
	insertDatabaseCleanupNarrator(t, database, "nar-root", "", "archived", "2026-01-01T00:00:00Z")
	insertDatabaseCleanupNarrator(t, database, "nar-child", "nar-root", "archived", "2026-01-02T00:00:00Z")
	insertDatabaseCleanupNarrator(t, database, "nar-active", "", "idle", "2026-01-03T00:00:00Z")
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('msg-child', 'nar-child', 'user', '[{"type":"text","text":"child"}]', '', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert child message: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq) VALUES ('ref-child', 'nar-child', 'msg-child', 1)`); err != nil {
		t.Fatalf("insert child message ref: %v", err)
	}
	h := NewHandler(database, &narrators.Service{DB: database})
	req := httptest.NewRequest(http.MethodPost, "/api/storage/database/cleanup", strings.NewReader(`{"target":"archivedSessions"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.databaseCleanup(w, req); err != nil {
		t.Fatalf("database cleanup archivedSessions returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected cleanup 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode cleanup body: %v", err)
	}
	if body["ok"] != true || body["changed"] != true || body["vacuumRan"] != false || body["target"] != "archivedSessions" {
		t.Fatalf("unexpected archived cleanup body: %#v", body)
	}
	assertCleanupRowCount(t, database, `SELECT COUNT(*) FROM narrators WHERE id IN ('nar-root', 'nar-child')`, 0)
	assertCleanupRowCount(t, database, `SELECT COUNT(*) FROM narrators WHERE id = 'nar-active'`, 1)
	assertCleanupRowCount(t, database, `SELECT COUNT(*) FROM narrator_messages WHERE id = 'msg-child'`, 0)
	assertCleanupRowCount(t, database, `SELECT COUNT(*) FROM narrator_message_refs WHERE message_id = 'msg-child'`, 0)
}

func TestDeleteNarratorSessionsIsFeatureDisabled(t *testing.T) {
	database := openCleanupDeleteTestDB(t)
	if _, err := database.Exec(`INSERT INTO narrators (id) VALUES ('nar-1')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO terminal_tabs (id, narrator_id) VALUES ('tab-1', 'nar-1')`); err != nil {
		t.Fatalf("insert terminal tab: %v", err)
	}
	h := NewHandler(database)
	req := httptest.NewRequest(http.MethodPost, "/api/storage/cleanup", nil)

	err := h.deleteNarratorSessions(req, []string{"nar-1"})
	var appErr *httpx.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected app error, got %T %v", err, err)
	}
	if appErr.Code != "NOT_IMPLEMENTED" && appErr.Code != "FEATURE_DISABLED" {
		t.Fatalf("expected feature-disabled delete guard, got %#v", appErr)
	}
	var narratorCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrators WHERE id = 'nar-1'`).Scan(&narratorCount); err != nil {
		t.Fatalf("count narrators: %v", err)
	}
	if narratorCount != 1 {
		t.Fatalf("feature-disabled delete guard should preserve narrator, count=%d", narratorCount)
	}
}

func TestRulerMutationsMissingServicesExposeFeatureDisabledFallback(t *testing.T) {
	h := NewHandler(nil)
	router := chi.NewRouter()
	router.Route("/projects", h.RegisterRuler)
	for _, tc := range []struct {
		path    string
		body    string
		feature string
	}{
		{path: "/projects/p1/ruler/fork", body: `{"startCommitSha":"abc","title":"Fork"}`, feature: "ruler.fork"},
		{path: "/projects/p1/ruler/merge", body: `{"sourceChapterId":"ch1"}`, feature: "ruler.merge"},
		{path: "/projects/p1/ruler/abandon", body: `{"chapterId":"ch1"}`, feature: "ruler.abandon"},
		{path: "/projects/p1/ruler/rebase", body: `{"chapterId":"ch1"}`, feature: "ruler.rebase"},
		{path: "/projects/p1/ruler/rebase-resolve", body: `{"chapterId":"ch1","action":"abort"}`, feature: "ruler.rebaseResolve"},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("%s status = %d body=%s", tc.path, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", tc.path, err)
		}
		if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != tc.feature || body["supported"] != false || body["fallback"] != true {
			t.Fatalf("unexpected fallback for %s: %#v", tc.path, body)
		}
		reason, _ := body["reason"].(string)
		if reason == "" || body["error"] != reason || body["message"] != reason {
			t.Fatalf("ruler fallback for %s should expose reason/error/message, got %#v", tc.path, body)
		}
	}
}

func TestRulerMergeRejectsInvalidStrategyAndOverlongMessage(t *testing.T) {
	h := NewHandler(nil)
	router := chi.NewRouter()
	router.Route("/projects", h.RegisterRuler)
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "invalid-strategy", body: `{"sourceChapterId":"ch1","strategy":"cherry-pick"}`},
		{name: "unknown-strategy", body: `{"sourceChapterId":"ch1","strategy":"rebase"}`},
		{name: "overlong-message", body: `{"sourceChapterId":"ch1","message":"` + strings.Repeat("x", 501) + `"}`},
	} {
		req := httptest.NewRequest(http.MethodPost, "/projects/p1/ruler/merge", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d body=%s", tc.name, w.Code, w.Body.String())
		}
	}

	// Valid strategy/message values still pass validation and reach the
	// chapter-service-unavailable fallback (nil service in this handler).
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "default-strategy", body: `{"sourceChapterId":"ch1"}`},
		{name: "explicit-merge", body: `{"sourceChapterId":"ch1","strategy":"merge"}`},
		{name: "squash", body: `{"sourceChapterId":"ch1","strategy":"squash"}`},
		{name: "message-at-limit", body: `{"sourceChapterId":"ch1","message":"` + strings.Repeat("x", 500) + `"}`},
	} {
		req := httptest.NewRequest(http.MethodPost, "/projects/p1/ruler/merge", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("%s: expected 501 feature-disabled fallback, got %d body=%s", tc.name, w.Code, w.Body.String())
		}
	}
}

func openDatabaseCleanupNarratorTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	for _, stmt := range []string{
		`CREATE TABLE narrators (
			id TEXT PRIMARY KEY,
			chapter_id TEXT,
			api_conversation_id TEXT,
			fork_message_id TEXT,
			type TEXT,
			subagent_type TEXT,
			title TEXT,
			inherit_mode TEXT,
			parent_narrator_id TEXT,
			context_summary TEXT,
			model TEXT,
			system_prompt TEXT,
			permission_mode TEXT,
			previous_permission_mode TEXT,
			plan_file_id TEXT,
			reasoning_effort TEXT,
			fast_mode INTEGER DEFAULT 0,
			relaxed_plan INTEGER DEFAULT 0,
			plan_reflection_auto_approve_override TEXT DEFAULT 'inherit',
			danger_reflection_override TEXT DEFAULT 'inherit',
			message_count INTEGER DEFAULT 0,
			total_cost_usd REAL DEFAULT 0,
			last_message_at TEXT,
			status TEXT DEFAULT 'idle',
			substatus TEXT DEFAULT '[]',
			plan_mode INTEGER DEFAULT 0,
			cwd TEXT,
			prune_boundary_message_id TEXT,
			pruned_percent INTEGER,
			prune_enabled INTEGER DEFAULT 1,
			enabled_tools TEXT,
			error_message TEXT,
			todos_json TEXT,
			todos_tool_use_id TEXT,
			variant TEXT DEFAULT 'primary',
			traits TEXT DEFAULT '[]',
			is_background INTEGER DEFAULT 0,
			background_status TEXT,
			background_result TEXT,
			background_completed_at TEXT,
			is_ask_in_passing INTEGER DEFAULT 0,
			message_version INTEGER DEFAULT 0,
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE narrator_messages (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			parent_tool_use_id TEXT,
			role TEXT,
			content_json TEXT,
			content_text TEXT,
			created_at TEXT
		)`,
		`CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, seq INTEGER)`,
		`CREATE TABLE narrator_tool_calls (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT)`,
		`CREATE TABLE background_tasks (id TEXT PRIMARY KEY, parent_narrator_id TEXT, subagent_narrator_id TEXT, status TEXT)`,
		`CREATE TABLE api_requests (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, raw_dump_json TEXT, created_at TEXT)`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("create cleanup narrator schema: %v", err)
		}
	}
	return database
}

func insertDatabaseCleanupNarrator(t *testing.T, database *sql.DB, narratorID, parentID, status, updatedAt string) {
	t.Helper()
	var parent any
	if parentID != "" {
		parent = parentID
	}
	_, err := database.Exec(`INSERT INTO narrators (
		id, parent_narrator_id, type, title, inherit_mode, model, permission_mode, status, substatus,
		plan_mode, cwd, variant, traits, is_background, is_ask_in_passing, message_version,
		message_count, total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, ?, 'primary', ?, 'fresh', 'openai:test', 'default', ?, '[]', 0, '', 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`, narratorID, parent, narratorID, status, updatedAt, updatedAt)
	if err != nil {
		t.Fatalf("insert cleanup narrator %s: %v", narratorID, err)
	}
}

func assertCleanupRowCount(t *testing.T, database *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("count query %q = %d, want %d", query, got, want)
	}
}

func openCleanupDeleteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, table := range []string{"narrator_message_refs", "narrator_tool_calls", "narrator_sidecars", "api_requests", "terminal_view_state", "terminal_tabs", "terminals", "narrator_buffered_messages", "narrator_goals", "narrator_file_snapshots", "narrator_patches", "narrator_whitelist_dirs", "narrator_blacklist_dirs", "narrator_whitelist_cmds", "narrator_blacklist_cmds", "gateway_session_mappings", "narrator_messages"} {
		stmt := `CREATE TABLE ` + table + ` (id TEXT PRIMARY KEY, narrator_id TEXT)`
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("create %s: %v", table, err)
		}
	}
	if _, err := database.Exec(`CREATE TABLE narrators (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create narrators: %v", err)
	}
	return database
}

func TestNotificationSoundRegistrationIncludesAuthenticatedMethods(t *testing.T) {
	router := chi.NewRouter()
	h := NewHandler(nil)
	router.Route("/api/notification-sounds", h.RegisterNotificationSounds)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/notification-sounds/"},
		{method: http.MethodGet, path: "/api/notification-sounds/sound-id"},
		{method: http.MethodDelete, path: "/api/notification-sounds/sound-id"},
	} {
		rctx := chi.NewRouteContext()
		if !router.Match(rctx, tc.method, tc.path) {
			t.Fatalf("expected %s %s to be registered", tc.method, tc.path)
		}
	}
}

func TestNotificationSoundUploadRejectsMissingFileWithBadRequest(t *testing.T) {
	router := chi.NewRouter()
	h := NewHandler(nil)
	router.Route("/api/notification-sounds", h.RegisterNotificationSounds)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("note", "hello"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/notification-sounds/", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing file, got %d: %s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	if payload["code"] != "VALIDATION_ERROR" || payload["message"] != "Missing file in form data" || payload["error"] != "Missing file in form data" {
		t.Fatalf("unexpected notification sound diagnostics: %#v", payload)
	}
}

func TestNotificationSoundUploadMatchesTSMimeSizeAndStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	router := chi.NewRouter()
	h := NewHandler(nil)
	router.Route("/api/notification-sounds", h.RegisterNotificationSounds)

	badMime := performNotificationSoundUpload(t, router, "tone.mp3", "application/octet-stream", []byte("ID3"))
	if badMime.Code != http.StatusBadRequest {
		t.Fatalf("unsupported MIME with mp3 extension should be rejected, got %d: %s", badMime.Code, badMime.Body.String())
	}

	tooLarge := performNotificationSoundUpload(t, router, "tone.mp3", "audio/mpeg", bytes.Repeat([]byte("x"), 1024*1024+1))
	if tooLarge.Code != http.StatusBadRequest {
		t.Fatalf("oversized sound should be rejected, got %d: %s", tooLarge.Code, tooLarge.Body.String())
	}

	ok := performNotificationSoundUpload(t, router, "tone.mp3", "audio/mpeg", []byte("ID3"))
	if ok.Code != http.StatusOK {
		t.Fatalf("valid sound upload should match TS 200, got %d: %s", ok.Code, ok.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(ok.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode upload body: %v", err)
	}
	if body["id"] == "" || body["filename"] == "" || body["mediaType"] != "audio/mpeg" {
		t.Fatalf("unexpected upload response: %#v", body)
	}
}

func TestNotificationSoundsAreUserScopedAndValidateIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userADir := filepath.Join(home, ".narrafork", "notification-sounds", "user-a")
	if err := os.MkdirAll(userADir, 0o755); err != nil {
		t.Fatalf("mkdir user sound dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userADir, "sound1234.mp3"), []byte("ID3"), 0o644); err != nil {
		t.Fatalf("write user sound: %v", err)
	}
	if path, err := findSoundFile("sound1234", "user-a"); err != nil || path == "" {
		t.Fatalf("expected own sound to be found, path=%q err=%v", path, err)
	}
	if path, err := findSoundFile("sound1234", "user-b"); err != nil || path != "" {
		t.Fatalf("sound lookup must not cross user dirs, path=%q err=%v", path, err)
	}
	if _, err := findSoundFile("..", "user-a"); err == nil {
		t.Fatalf("invalid sound id should be rejected")
	}
}

func performNotificationSoundUpload(t *testing.T, router http.Handler, filename, mediaType string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	header.Set("Content-Type", mediaType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/notification-sounds/", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestHookPositiveCRUDAndUpdateValidationMatchTS(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()
	_, err = database.Exec(`CREATE TABLE hooks (
		id TEXT PRIMARY KEY,
		project_id TEXT,
		event TEXT NOT NULL,
		matcher TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL,
		command TEXT,
		url TEXT,
		headers TEXT,
		prompt TEXT,
		model TEXT,
		timeout INTEGER NOT NULL DEFAULT 30,
		enabled INTEGER NOT NULL DEFAULT 1,
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create hooks table: %v", err)
	}

	router := chi.NewRouter()
	h := NewHandler(database)
	router.Route("/hooks", h.RegisterHooks)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	created := request(http.MethodPost, "/hooks", `{"event":"PreToolUse","matcher":"Read","type":"command","command":"true","timeout":5,"enabled":true,"sortOrder":2}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	var createBody map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	hookID, _ := createBody["id"].(string)
	if hookID == "" || createBody["matcher"] != "Read" || createBody["type"] != "command" || createBody["headers"] != nil {
		t.Fatalf("unexpected create body: %#v", createBody)
	}

	updated := request(http.MethodPut, "/hooks/"+hookID, `{"matcher":"","type":"http","url":"https://example.com/hook","headers":{"X-Test":"yes"},"timeout":10,"enabled":false}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", updated.Code, updated.Body.String())
	}
	var updateBody map[string]any
	if err := json.Unmarshal(updated.Body.Bytes(), &updateBody); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	headers, _ := updateBody["headers"].(map[string]any)
	if updateBody["matcher"] != "" || updateBody["type"] != "http" || updateBody["url"] != "https://example.com/hook" || updateBody["enabled"] != false || headers["X-Test"] != "yes" {
		t.Fatalf("unexpected update body: %#v", updateBody)
	}

	clearActiveURL := request(http.MethodPut, "/hooks/"+hookID, `{"url":null}`)
	if clearActiveURL.Code != http.StatusBadRequest {
		t.Fatalf("clear active url status = %d body=%s", clearActiveURL.Code, clearActiveURL.Body.String())
	}

	// The "Stop" event is a valid TS hookEventEnum value (validators/hooks.ts:3),
	// so create and update must accept it (Go previously rejected it 400). Mirror
	// TS: POST with event:"Stop" -> 201 persisting event=="Stop"; PUT event:"Stop"
	// -> 200 persisting event=="Stop".
	stopCreated := request(http.MethodPost, "/hooks", `{"event":"Stop","type":"command","command":"echo done","timeout":5}`)
	if stopCreated.Code != http.StatusCreated {
		t.Fatalf("create Stop hook status = %d body=%s", stopCreated.Code, stopCreated.Body.String())
	}
	var stopCreateBody map[string]any
	if err := json.Unmarshal(stopCreated.Body.Bytes(), &stopCreateBody); err != nil {
		t.Fatalf("decode Stop create: %v", err)
	}
	stopHookID, _ := stopCreateBody["id"].(string)
	if stopHookID == "" || stopCreateBody["event"] != "Stop" {
		t.Fatalf("Stop hook create should persist event=Stop, got %#v", stopCreateBody)
	}
	stopUpdated := request(http.MethodPut, "/hooks/"+hookID, `{"event":"Stop"}`)
	if stopUpdated.Code != http.StatusOK {
		t.Fatalf("update to Stop status = %d body=%s", stopUpdated.Code, stopUpdated.Body.String())
	}
	var stopUpdateBody map[string]any
	if err := json.Unmarshal(stopUpdated.Body.Bytes(), &stopUpdateBody); err != nil {
		t.Fatalf("decode Stop update: %v", err)
	}
	if stopUpdateBody["event"] != "Stop" {
		t.Fatalf("update should persist event=Stop, got %#v", stopUpdateBody)
	}

	missingInvalid := request(http.MethodPut, "/hooks/missing", `{"type":"bad"}`)
	if missingInvalid.Code != http.StatusNotFound {
		t.Fatalf("missing update should 404 before body validation, got %d body=%s", missingInvalid.Code, missingInvalid.Body.String())
	}

	deleted := request(http.MethodDelete, "/hooks/"+hookID, `{}`)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", deleted.Code, deleted.Body.String())
	}
	afterDelete := request(http.MethodGet, "/hooks/"+hookID, "")
	if afterDelete.Code != http.StatusNotFound {
		t.Fatalf("detail after delete status = %d body=%s", afterDelete.Code, afterDelete.Body.String())
	}
}

func TestNotificationWebhookFailuresReturnDiagnostics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusTeapot)
	}))
	defer upstream.Close()

	router := chi.NewRouter()
	h := NewHandler(nil)
	router.Route("/api/notifications", h.RegisterNotifications)

	for _, tc := range []struct {
		name         string
		path         string
		provider     string
		expectedCode string
	}{
		{
			name:         "dingtalk",
			path:         "/api/notifications/test-dingtalk",
			provider:     "DingTalk",
			expectedCode: "NOTIFICATION_DINGTALK_WEBHOOK_FAILED",
		},
		{
			name:         "feishu",
			path:         "/api/notifications/test-feishu",
			provider:     "Feishu",
			expectedCode: "NOTIFICATION_FEISHU_WEBHOOK_FAILED",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			webhookJSON, err := json.Marshal(upstream.URL)
			if err != nil {
				t.Fatalf("marshal webhook: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"webhook":`+string(webhookJSON)+`}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusBadGateway {
				t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["ok"] != false || body["code"] != tc.expectedCode {
				t.Fatalf("unexpected diagnostic envelope: %#v", body)
			}
			reason, _ := body["reason"].(string)
			if !strings.Contains(reason, tc.provider+" webhook failed: 418") || !strings.Contains(reason, "denied") {
				t.Fatalf("unexpected reason: %#v", body)
			}
			if body["error"] != reason || body["message"] != reason {
				t.Fatalf("notification error should expose reason/error/message, got %#v", body)
			}
		})
	}
}
