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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/httpx"
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

func TestStorageCleanupUnsupportedTargetsDoNotFakeSuccess(t *testing.T) {
	h := NewHandler(nil)
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

func TestDatabaseCleanupSessionTargetsReturnExplicitFeatureDisabledFallback(t *testing.T) {
	h := NewHandler(nil)
	for _, target := range []string{"archivedSessions", "staleSessions"} {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/storage/database/cleanup", strings.NewReader(`{"target":"`+target+`"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			if err := h.databaseCleanup(w, req); err != nil {
				t.Fatalf("database cleanup %s returned error: %v", target, err)
			}
			if w.Code != http.StatusNotImplemented {
				t.Fatalf("expected 501 feature disabled, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode fallback body: %v", err)
			}
			if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "storage.database.cleanup."+target || body["supported"] != false || body["fallback"] != true {
				t.Fatalf("unexpected session cleanup fallback body: %#v", body)
			}
			reason, _ := body["reason"].(string)
			if !strings.Contains(reason, "safe narrator deletion plan") || body["error"] != reason || body["message"] != reason {
				t.Fatalf("session cleanup fallback should expose reason/error/message, got %#v", body)
			}
		})
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
	if payload["code"] != "BAD_REQUEST" || payload["message"] != "missing file in form data" || payload["error"] != "missing file in form data" {
		t.Fatalf("unexpected notification sound diagnostics: %#v", payload)
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
