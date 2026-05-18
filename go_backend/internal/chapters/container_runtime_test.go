package chapters

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/config"

	_ "modernc.org/sqlite"
)

func TestLimitedCombinedWriterTruncatesContainerOutput(t *testing.T) {
	writer := newLimitedCombinedWriter(16)
	if _, err := writer.Write(bytes.Repeat([]byte("x"), 32)); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := writer.String()
	if len(out) > 64 || !strings.Contains(out, "[truncated]") {
		t.Fatalf("limited output = %q, want truncation marker", out)
	}
}

func TestCopyFileWithModeStreamsLargeFiles(t *testing.T) {
	src := filepath.Join(t.TempDir(), "large.bin")
	dst := filepath.Join(t.TempDir(), "nested", "large.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 3*1024*1024), 0o750); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := copyFileWithMode(src, dst, 0o750); err != nil {
		t.Fatalf("copy file: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat copied file: %v", err)
	}
	if info.Size() != 3*1024*1024 || info.Mode().Perm() != 0o750 {
		t.Fatalf("copied file size/mode = %d/%#o", info.Size(), info.Mode().Perm())
	}
}

func TestComposeFileReadsAreBounded(t *testing.T) {
	worktree := t.TempDir()
	path := filepath.Join(worktree, "compose.yml")
	content := append([]byte("services:\n  web:\n    image: test\n"), bytes.Repeat([]byte("x"), composeFileReadLimit+1024)...)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}

	raw, ok := readCompose(worktree)
	if !ok {
		t.Fatalf("expected compose file to be read")
	}
	if !strings.Contains(raw, "[truncated]") || len(raw) > composeFileReadLimit+64 {
		t.Fatalf("compose content was not bounded: len=%d", len(raw))
	}
	services := parseComposeServicesFromFile(path)
	if len(services) == 0 || services[0]["name"] != "web" {
		t.Fatalf("expected bounded parser to keep early service, got %#v", services)
	}
}

func TestRunStartupScriptBoundsFailureOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("startup scripts use POSIX sh")
	}
	script := "i=0; while [ $i -lt 70000 ]; do printf x; i=$((i+1)); done; exit 1"
	warnings := runStartupScript(context.Background(), t.TempDir(), "chapter-1", script)
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "Startup script failed:") {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if len(warnings[0]) > 360 {
		t.Fatalf("startup warning should stay bounded, got len=%d", len(warnings[0]))
	}
}

func TestAllocateContainerPortsReleasesExistingChapterPorts(t *testing.T) {
	database := openContainerRuntimeDB(t)
	service := &Service{DB: database}
	ctx := context.Background()
	if _, err := database.Exec(`INSERT INTO port_allocations (port, chapter_id, service_name, allocated_at) VALUES (10000, 'other', 'api', 'now')`); err != nil {
		t.Fatalf("seed port allocation: %v", err)
	}
	first, err := service.allocateContainerPorts(ctx, "chapter-1", []containerPortConfig{{ContainerPort: 3000, ServiceName: "web"}, {ContainerPort: 5432, ServiceName: "db"}})
	if err != nil {
		t.Fatalf("allocate first: %v", err)
	}
	if intValue(first[0]["hostPort"]) != 10001 || intValue(first[1]["hostPort"]) != 10002 {
		t.Fatalf("first mappings = %#v, want 10001/10002", first)
	}
	second, err := service.allocateContainerPorts(ctx, "chapter-1", []containerPortConfig{{ContainerPort: 8080, ServiceName: "web"}})
	if err != nil {
		t.Fatalf("allocate second: %v", err)
	}
	if intValue(second[0]["hostPort"]) != 10001 {
		t.Fatalf("second mapping = %#v, want released chapter port reused at 10001", second)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM port_allocations WHERE chapter_id = 'chapter-1'`).Scan(&count); err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	if count != 1 {
		t.Fatalf("chapter allocations = %d, want 1", count)
	}
}

func TestContainerStartProxyModeRecordsProxyMetadataWithFakePodman(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n    ports:\n      - \"8080:8080\"\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	settings := loadTempSettings(t)
	if _, err := settings.Patch(map[string]any{"containers": map[string]any{"proxy": map[string]any{"enabled": true, "port": 7780}}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`, "dev.example.test")
	prependFakePodman(t, fakePodmanScript())

	handler := NewHandler(&Service{DB: database, Settings: settings})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/c1/containers/start", nil)
	router := newChapterRouter(handler)
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("start response = %#v, want ok", body)
	}
	var serviceName, proxyLabel, containerIP string
	var hostPort sql.NullInt64
	var containerPort int
	if err := database.QueryRow(`SELECT service_name, host_port, container_port, proxy_label, container_ip FROM container_instances WHERE chapter_id = 'c1'`).Scan(&serviceName, &hostPort, &containerPort, &proxyLabel, &containerIP); err != nil {
		t.Fatalf("query container instance: %v", err)
	}
	if serviceName != "web" || hostPort.Valid || containerPort != 8080 || proxyLabel != "c1-web-8080" || containerIP != "10.88.0.2" {
		t.Fatalf("proxy row = service=%s host=%v container=%d label=%s ip=%s", serviceName, hostPort, containerPort, proxyLabel, containerIP)
	}
	var allocations int
	if err := database.QueryRow(`SELECT COUNT(*) FROM port_allocations WHERE chapter_id = 'c1'`).Scan(&allocations); err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	if allocations != 0 {
		t.Fatalf("proxy mode allocations = %d, want 0", allocations)
	}
}

func TestPersistContainerInstancesReturnsPortCleanupError(t *testing.T) {
	database := openContainerRuntimeDB(t)
	if _, err := database.Exec(`INSERT INTO port_allocations (port, chapter_id, service_name, allocated_at) VALUES (10000, 'c1', 'web', 'now')`); err != nil {
		t.Fatalf("seed port allocation: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_port_cleanup BEFORE DELETE ON port_allocations WHEN OLD.chapter_id = 'c1' BEGIN SELECT RAISE(ABORT, 'port cleanup blocked'); END;`); err != nil {
		t.Fatalf("create cleanup trigger: %v", err)
	}
	handler := NewHandler(&Service{DB: database})

	err := handler.persistContainerInstances(context.Background(), "c1", []map[string]any{{"id": "container-1", "serviceName": "web", "status": "running", "containerPort": 8080}})
	if err == nil || !strings.Contains(err.Error(), "port cleanup blocked") {
		t.Fatalf("expected port cleanup error, got %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM container_instances WHERE chapter_id = 'c1'`).Scan(&count); err != nil {
		t.Fatalf("count container instances: %v", err)
	}
	if count != 0 {
		t.Fatalf("port cleanup failure should not persist container instances, count=%d", count)
	}
}

func TestContainerActionBroadcastsContainerStatusChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n    ports:\n      - \"8080:8080\"\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`, "")
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, created_at) VALUES ('nar-1', 'c1', 'primary', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	prependFakePodman(t, fakePodmanScript())
	recorder := &chapterBroadcastRecorder{}
	handler := NewHandler(&Service{DB: database}, recorder)

	recorderHTTP := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/c1/containers/start", nil)
	newChapterRouter(handler).ServeHTTP(recorderHTTP, req)
	if recorderHTTP.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorderHTTP.Code, recorderHTTP.Body.String())
	}

	if !recorder.hasNarratorEvent("nar-1", "container_status_changed", "starting", true) {
		t.Fatalf("missing starting container_status_changed event: %#v", recorder.narratorEvents)
	}
	if !recorder.hasNarratorEvent("nar-1", "container_status_changed", "running", true) {
		t.Fatalf("missing running container_status_changed event: %#v", recorder.narratorEvents)
	}
	if !recorder.hasGlobalEvent("container:starting") || !recorder.hasGlobalEvent("container:started") {
		t.Fatalf("missing compat container events: %#v", recorder.globalEvents)
	}
}

type chapterBroadcastEvent struct {
	NarratorID string
	Payload    map[string]any
}

type chapterBroadcastRecorder struct {
	narratorEvents []chapterBroadcastEvent
	globalEvents   []map[string]any
}

func (r *chapterBroadcastRecorder) BroadcastNarrator(narratorID string, payload any) {
	if decoded, ok := payload.(map[string]any); ok {
		r.narratorEvents = append(r.narratorEvents, chapterBroadcastEvent{NarratorID: narratorID, Payload: decoded})
	}
}

func (r *chapterBroadcastRecorder) BroadcastMergeProgress(payload any) {
	if decoded, ok := payload.(map[string]any); ok {
		r.globalEvents = append(r.globalEvents, decoded)
	}
}

func (r *chapterBroadcastRecorder) hasNarratorEvent(narratorID, eventType, status string, ok bool) bool {
	for _, event := range r.narratorEvents {
		if event.NarratorID == narratorID && event.Payload["type"] == eventType && event.Payload["status"] == status && event.Payload["ok"] == ok {
			return true
		}
	}
	return false
}

func (r *chapterBroadcastRecorder) hasGlobalEvent(eventType string) bool {
	for _, event := range r.globalEvents {
		if event["type"] == eventType {
			return true
		}
	}
	return false
}

func TestPodmanStatusMissingOnLinuxRemainsInstallable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("go_backend container management is Linux-only")
	}
	t.Setenv("PATH", t.TempDir())
	handler := NewHandler(&Service{})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chapters/podman/status", nil)
	if err := handler.podmanStatus(recorder, req); err != nil {
		t.Fatalf("podmanStatus: %v", err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["installed"] != false || body["supported"] != true || body["fallback"] != true || body["platform"] != "linux" {
		t.Fatalf("missing podman should be installable on Linux, got %#v", body)
	}
}

func TestPodmanInstallDoesNotReportSuccessWhenCommandDoesNotInstall(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fake package manager test is Linux-only")
	}
	binDir := t.TempDir()
	for _, name := range []string{"apt-get", "sh"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	t.Setenv("PATH", binDir)
	handler := NewHandler(&Service{})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chapters/podman/install", nil)
	if err := handler.podmanInstall(recorder, req); err != nil {
		t.Fatalf("podmanInstall: %v", err)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["ok"] != false || body["code"] != "PODMAN_INSTALL_NOT_VERIFIED" || body["installed"] != false {
		t.Fatalf("install should not report fake success after no-op command: %#v", body)
	}
}

func TestContainerActionReturnsExplicitFallbackWhenPodmanMissing(t *testing.T) {
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{}`, "")
	t.Setenv("PATH", t.TempDir())

	handler := NewHandler(&Service{DB: database})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/c1/containers/start", nil)
	newChapterRouter(handler).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["fallback"] != true || body["code"] != "PODMAN_NOT_FOUND" || body["supported"] != false {
		t.Fatalf("fallback body = %#v", body)
	}
}

func newChapterRouter(handler *Handler) http.Handler {
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	return router
}

func openContainerRuntimeDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, proxy_domain TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT, merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT, container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT, start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER, anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER, panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT, last_accessed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, container_id TEXT, service_name TEXT, status TEXT, host_port INTEGER, container_port INTEGER, proxy_label TEXT, container_ip TEXT, volume_name TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, variant TEXT, created_at TEXT)`,
		`CREATE TABLE port_allocations (port INTEGER PRIMARY KEY, chapter_id TEXT, service_name TEXT, allocated_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return database
}

func insertContainerRuntimeProjectAndChapter(t *testing.T, database *sql.DB, worktree, containerConfig, proxyDomain string) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, proxy_domain, created_at, updated_at) VALUES ('p1', 'Project', ?, 'main', NULLIF(?, ''), 'now', 'now')`, worktree, proxyDomain); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, container_config, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('c1', 'p1', 'Chapter', 'active', 'branch', 'chapter/test', ?, 'main', ?, 0, 0, 0, 0, 0, 0, 'now', 'now')`, worktree, containerConfig); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
}

func loadTempSettings(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return settings
}

func prependFakePodman(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "podman")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake podman: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func fakePodmanScript() string {
	return `#!/bin/sh
if [ "$1" = "--version" ]; then echo "podman version 5.0.0"; exit 0; fi
if [ "$1" = "info" ]; then echo "pasta"; exit 0; fi
if [ "$1" = "compose" ] && [ "$2" = "version" ]; then echo "podman-compose version 1.0.0"; exit 0; fi
if [ "$1" = "compose" ]; then
  case " $* " in
    *" ps --format json "*) printf '[{"ID":"cid1","Service":"web","State":"running","Publishers":[{"PublishedPort":18080,"TargetPort":8080}]}]'; exit 0 ;;
    *" up -d "*) echo "started"; exit 0 ;;
    *" stop "*|*" pause "*|*" unpause "*|*" down "*) echo "ok"; exit 0 ;;
    *" logs "*) echo "log line"; exit 0 ;;
  esac
fi
if [ "$1" = "inspect" ]; then
  case "$3" in
    *ExposedPorts*) printf '{"8080/tcp":{}}'; exit 0 ;;
    *) printf '10.88.0.2 '; exit 0 ;;
  esac
fi
exit 0
`
}
