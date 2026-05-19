package misccompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
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
}

func TestDeleteNarratorSessionsRollsBackWhenRelatedDeleteFails(t *testing.T) {
	database := openCleanupDeleteTestDB(t)
	if _, err := database.Exec(`INSERT INTO narrators (id) VALUES ('nar-1')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO terminal_tabs (id, narrator_id) VALUES ('tab-1', 'nar-1')`); err != nil {
		t.Fatalf("insert terminal tab: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_terminal_tab_cleanup BEFORE DELETE ON terminal_tabs BEGIN SELECT RAISE(ABORT, 'terminal tab cleanup blocked'); END;`); err != nil {
		t.Fatalf("create cleanup trigger: %v", err)
	}
	h := NewHandler(database)
	req := httptest.NewRequest(http.MethodPost, "/api/storage/cleanup", nil)

	if err := h.deleteNarratorSessions(req, []string{"nar-1"}); err == nil {
		t.Fatalf("expected cleanup delete failure")
	}
	var narratorCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrators WHERE id = 'nar-1'`).Scan(&narratorCount); err != nil {
		t.Fatalf("count narrators: %v", err)
	}
	if narratorCount != 1 {
		t.Fatalf("cleanup failure should roll back narrator delete, count=%d", narratorCount)
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
