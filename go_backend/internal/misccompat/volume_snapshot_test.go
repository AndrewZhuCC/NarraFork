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

func TestVolumeSnapshotCreateReturnsExplicitFallbackWithoutPodman(t *testing.T) {
	sqlDB := newVolumeSnapshotTestDB(t)
	h := NewHandler(sqlDB)
	router := volumeSnapshotTestRouter(h)
	withVolumeSnapshotTestEnv(t, t.TempDir(), 1<<20, false)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/p1/volume-snapshots", bytes.NewBufferString(`{"chapterId":"c1","serviceName":"db","containerPath":"/data","name":"Seed data"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fallback status = %d, body %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode fallback: %v", err)
	}
	if payload["fallback"] != true || payload["supported"] != false || payload["code"] != "PODMAN_NOT_FOUND" {
		t.Fatalf("unexpected fallback payload: %#v", payload)
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
