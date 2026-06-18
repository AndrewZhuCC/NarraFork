package misccompat

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"
)

func TestVolumeSnapshotCreateApplyDeleteUsesPodmanArchive(t *testing.T) {
	sqlDB := newVolumeSnapshotTestDB(t)
	h := NewHandler(sqlDB)
	router := volumeSnapshotTestRouter(h)
	snapshotDir := t.TempDir()
	withVolumeSnapshotTestEnv(t, snapshotDir, 1<<20, true)
	appliedPath := filepath.Join(t.TempDir(), "applied.tar.gz")
	t.Setenv("NARRAFORK_FAKE_PODMAN_APPLY", appliedPath)

	createBody := bytes.NewBufferString(`{"chapterId":"c1","serviceName":"db","containerPath":"/data","name":"Seed data"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/projects/p1/volume-snapshots", createBody)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", createRec.Code, createRec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	snapshotID, _ := created["id"].(string)
	if snapshotID == "" {
		t.Fatalf("create response missing id: %#v", created)
	}
	if got := created["sourceChapterId"]; got != "c1" {
		t.Fatalf("sourceChapterId = %#v, want c1", got)
	}
	if got := created["sizeBytes"]; got != float64(len("snapshot-data")) {
		t.Fatalf("sizeBytes = %#v, want %d", got, len("snapshot-data"))
	}
	archivePath := filepath.Join(snapshotDir, "p1", snapshotID+".tar.gz")
	archiveData, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if string(archiveData) != "snapshot-data" {
		t.Fatalf("archive contents = %q", archiveData)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/volume-snapshots/"+snapshotID+"/apply", bytes.NewBufferString(`{"targetChapterId":"c2"}`))
	applyRec := httptest.NewRecorder()
	router.ServeHTTP(applyRec, applyReq)
	if applyRec.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body %s", applyRec.Code, applyRec.Body.String())
	}
	applied, err := os.ReadFile(appliedPath)
	if err != nil {
		t.Fatalf("read applied archive: %v", err)
	}
	if string(applied) != "snapshot-data" {
		t.Fatalf("applied archive = %q", applied)
	}
	if got := countVolumeSnapshotRows(t, sqlDB, "volume_snapshot_applications"); got != 1 {
		t.Fatalf("application rows = %d, want 1", got)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/volume-snapshots/"+snapshotID, nil)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body %s", deleteRec.Code, deleteRec.Body.String())
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("archive still exists or stat failed unexpectedly: %v", err)
	}
	if got := countVolumeSnapshotRows(t, sqlDB, "volume_snapshots"); got != 0 {
		t.Fatalf("snapshot rows = %d, want 0", got)
	}
	if got := countVolumeSnapshotRows(t, sqlDB, "volume_snapshot_applications"); got != 0 {
		t.Fatalf("application rows after delete = %d, want 0", got)
	}
}

func TestVolumeSnapshotCreatePrefersFrontendChapterID(t *testing.T) {
	sqlDB := newVolumeSnapshotTestDB(t)
	h := NewHandler(sqlDB)
	router := volumeSnapshotTestRouter(h)
	withVolumeSnapshotTestEnv(t, t.TempDir(), 1<<20, true)

	createBody := bytes.NewBufferString(`{
		"chapterId":"c1",
		"sourceChapterId":"other",
		"serviceName":"db",
		"containerPath":"/data",
		"name":"Seed data"
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/projects/p1/volume-snapshots", createBody)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", createRec.Code, createRec.Body.String())
	}

	var created map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if got := created["sourceChapterId"]; got != "c1" {
		t.Fatalf("sourceChapterId = %#v, want c1", got)
	}
}

func TestVolumeSnapshotCreateReturnsExplicitFallbackWithoutPodman(t *testing.T) {
	sqlDB := newVolumeSnapshotTestDB(t)
	h := NewHandler(sqlDB)
	router := volumeSnapshotTestRouter(h)
	withVolumeSnapshotTestEnv(t, t.TempDir(), 1<<20, false)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/volume-snapshots", bytes.NewBufferString(`{"chapterId":"c1","serviceName":"db","containerPath":"/data","name":"Seed data"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("fallback status = %d, body %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode fallback: %v", err)
	}
	if payload["fallback"] != true || payload["supported"] != false || payload["code"] != "PODMAN_NOT_FOUND" || payload["reason"] == "" {
		t.Fatalf("unexpected fallback payload: %#v", payload)
	}
	if got := countVolumeSnapshotRows(t, sqlDB, "volume_snapshots"); got != 0 {
		t.Fatalf("snapshot rows = %d, want 0", got)
	}
}

func TestVolumeSnapshotUpdateClearsNullableDescription(t *testing.T) {
	sqlDB := newVolumeSnapshotTestDB(t)
	h := NewHandler(sqlDB)
	router := volumeSnapshotTestRouter(h)
	if _, err := sqlDB.Exec(`INSERT INTO volume_snapshots (id, project_id, name, description, source_chapter_id, service_name, container_path, size_bytes, created_at, updated_at) VALUES ('snap-update', 'p1', 'Before', 'clear me', 'c1', 'db', '/data', 1, 'now', 'now')`); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/volume-snapshots/snap-update", bytes.NewBufferString(`{"description":null}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["description"] != nil {
		t.Fatalf("description should be null after update, got %#v", body)
	}
}

func TestVolumeSnapshotValidatesBeforeUnsupportedRuntimeFallback(t *testing.T) {
	sqlDB := newVolumeSnapshotTestDB(t)
	h := NewHandler(sqlDB)
	router := volumeSnapshotTestRouter(h)
	withVolumeSnapshotTestEnv(t, t.TempDir(), 1<<20, false)

	invalidCreate := httptest.NewRequest(http.MethodPost, "/api/projects/p1/volume-snapshots", bytes.NewBufferString(`{"serviceName":"db","containerPath":"/data","name":"Missing chapter"}`))
	invalidRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRec, invalidCreate)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid create should be validated before fallback, got %d body=%s", invalidRec.Code, invalidRec.Body.String())
	}

	missingApply := httptest.NewRequest(http.MethodPost, "/api/volume-snapshots/missing/apply", bytes.NewBufferString(`{"targetChapterId":"c2"}`))
	missingRec := httptest.NewRecorder()
	router.ServeHTTP(missingRec, missingApply)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing apply should return 404 before fallback, got %d body=%s", missingRec.Code, missingRec.Body.String())
	}
	if got := countVolumeSnapshotRows(t, sqlDB, "volume_snapshots"); got != 0 {
		t.Fatalf("snapshot rows = %d, want 0", got)
	}
}

func TestVolumeSnapshotRejectsUnsafePathAndOversizedArchive(t *testing.T) {
	sqlDB := newVolumeSnapshotTestDB(t)
	h := NewHandler(sqlDB)
	router := volumeSnapshotTestRouter(h)
	withVolumeSnapshotTestEnv(t, t.TempDir(), 4, true)

	unsafeReq := httptest.NewRequest(http.MethodPost, "/api/projects/p1/volume-snapshots", bytes.NewBufferString(`{"chapterId":"c1","serviceName":"db","containerPath":"/data/../etc","name":"Unsafe"}`))
	unsafeRec := httptest.NewRecorder()
	router.ServeHTTP(unsafeRec, unsafeReq)
	if unsafeRec.Code != http.StatusBadRequest {
		t.Fatalf("unsafe path status = %d, body %s", unsafeRec.Code, unsafeRec.Body.String())
	}

	// Mirror the TS refine `!p.includes("..")` which is a pure SUBSTRING check:
	// ".." anywhere (even inside a filename like "my..backup") is rejected, not
	// just a "/.." path-traversal segment. A segment-only Go check would wrongly
	// accept this path while TS returns 400.
	substringReq := httptest.NewRequest(http.MethodPost, "/api/projects/p1/volume-snapshots", bytes.NewBufferString(`{"chapterId":"c1","serviceName":"db","containerPath":"/data/my..backup/file","name":"Substring dots"}`))
	substringRec := httptest.NewRecorder()
	router.ServeHTTP(substringRec, substringReq)
	if substringRec.Code != http.StatusBadRequest {
		t.Fatalf("substring '..' path status = %d, body %s", substringRec.Code, substringRec.Body.String())
	}

	oversizedReq := httptest.NewRequest(http.MethodPost, "/api/projects/p1/volume-snapshots", bytes.NewBufferString(`{"chapterId":"c1","serviceName":"db","containerPath":"/data","name":"Too large"}`))
	oversizedRec := httptest.NewRecorder()
	router.ServeHTTP(oversizedRec, oversizedReq)
	if oversizedRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, body %s", oversizedRec.Code, oversizedRec.Body.String())
	}
	if got := countVolumeSnapshotRows(t, sqlDB, "volume_snapshots"); got != 0 {
		t.Fatalf("snapshot rows = %d, want 0", got)
	}
}

func volumeSnapshotTestRouter(h *Handler) http.Handler {
	router := chi.NewRouter()
	router.Route("/api/projects", h.RegisterVolumeSnapshots)
	router.Route("/api/volume-snapshots", h.RegisterVolumeSnapshotRoot)
	return router
}

func withVolumeSnapshotTestEnv(t *testing.T, snapshotDir string, maxBytes int64, withPodman bool) {
	t.Helper()
	oldBase := volumeSnapshotBaseDir
	oldMax := volumeSnapshotMaxBytes
	volumeSnapshotBaseDir = snapshotDir
	volumeSnapshotMaxBytes = maxBytes
	t.Cleanup(func() {
		volumeSnapshotBaseDir = oldBase
		volumeSnapshotMaxBytes = oldMax
	})
	binDir := t.TempDir()
	if withPodman {
		podmanPath := filepath.Join(binDir, "podman")
		script := `#!/bin/sh
set -eu
if [ "$1" = "exec" ] && [ "$2" = "c1" ] && [ "$3" = "tar" ] && [ "$4" = "czf" ]; then
	printf 'snapshot-data'
	exit 0
fi
if [ "$1" = "exec" ] && [ "$3" = "mkdir" ]; then
	exit 0
fi
if [ "$1" = "exec" ] && [ "$2" = "-i" ] && [ "$4" = "tar" ]; then
	cat > "$NARRAFORK_FAKE_PODMAN_APPLY"
	exit 0
fi
echo "unexpected podman args: $*" >&2
exit 9
`
		if err := os.WriteFile(podmanPath, []byte(script), 0o700); err != nil {
			t.Fatalf("write fake podman: %v", err)
		}
	}
	if withPodman {
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	} else {
		t.Setenv("PATH", binDir)
	}
}

func newVolumeSnapshotTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, title TEXT)`,
		`CREATE TABLE container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, container_id TEXT, service_name TEXT, status TEXT, updated_at TEXT)`,
		`CREATE TABLE volume_snapshots (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, name TEXT NOT NULL, description TEXT, source_chapter_id TEXT, service_name TEXT NOT NULL, container_path TEXT NOT NULL, size_bytes INTEGER, created_by TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE volume_snapshot_applications (id TEXT PRIMARY KEY, snapshot_id TEXT NOT NULL, chapter_id TEXT NOT NULL, applied_at TEXT NOT NULL, applied_by TEXT)`,
		`INSERT INTO projects (id, name) VALUES ('p1', 'Project')`,
		`INSERT INTO chapters (id, project_id, title) VALUES ('c1', 'p1', 'Source'), ('c2', 'p1', 'Target'), ('other', 'p2', 'Other')`,
		`INSERT INTO container_instances (id, chapter_id, container_id, service_name, status, updated_at) VALUES ('ci1', 'c1', 'c1', 'db', 'running', 'now'), ('ci2', 'c2', 'c2', 'db', 'running', 'now')`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	return sqlDB
}

func countVolumeSnapshotRows(t *testing.T, sqlDB *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

// TestValidateContainerSnapshotPathMatchesTSNoTrim pins validateContainerSnapshotPath
// to the TS createVolumeSnapshotSchema.containerPath schema, which is
// z.string().min(1).max(1000).refine(startsWith("/")).refine(!includes(".."))
// with NO .trim(). TS validates the RAW value and stores/execs it verbatim.
// A previous Go version trimmed + filepath.Clean'd the value first, which both
// wrongly accepted whitespace-padded paths TS rejects and mutated the stored path.
func TestValidateContainerSnapshotPathMatchesTSNoTrim(t *testing.T) {
	// Leading whitespace: TS "  /data".startsWith("/") is false -> 400. A trim-first
	// Go check would see "/data" and wrongly accept.
	if _, err := validateContainerSnapshotPath("  /data"); err == nil {
		t.Fatal("leading-whitespace containerPath should be rejected (not absolute), but was accepted")
	}
	// Trailing whitespace is part of the raw value; the path still starts with "/"
	// and contains no "..", so TS accepts it AND preserves it verbatim. A trim-first
	// Go version would strip the trailing space, diverging from the stored TS value.
	got, err := validateContainerSnapshotPath("/data ")
	if err != nil {
		t.Fatalf("trailing-whitespace absolute path should be accepted like TS, got error: %v", err)
	}
	if got != "/data " {
		t.Fatalf("containerPath must be preserved verbatim like TS, got %q want %q", got, "/data ")
	}
	// Trailing slash must be preserved (TS stores raw; filepath.Clean would strip it).
	if got, err := validateContainerSnapshotPath("/data/"); err != nil || got != "/data/" {
		t.Fatalf("trailing-slash path should be preserved verbatim, got %q err %v", got, err)
	}
	// Double slash must be preserved (filepath.Clean would collapse "//" to "/").
	if got, err := validateContainerSnapshotPath("/data//sub"); err != nil || got != "/data//sub" {
		t.Fatalf("double-slash path should be preserved verbatim, got %q err %v", got, err)
	}
	// Sanity: the TS-mirrored rejections still hold on the raw value.
	if _, err := validateContainerSnapshotPath("relative/path"); err == nil {
		t.Fatal("non-absolute path should be rejected")
	}
	if _, err := validateContainerSnapshotPath("/data/../etc"); err == nil {
		t.Fatal("'..' path should be rejected")
	}
	if _, err := validateContainerSnapshotPath(""); err == nil {
		t.Fatal("empty path should be rejected")
	}
}
