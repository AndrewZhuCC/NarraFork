package chapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestContainerStartHostPortModeRunsRealCompose drives the full non-proxy
// container start orchestration through POST /containers/start against a REAL
// podman compose: allocate a host port, build the compose env, run
// `compose up -d`, list instances from the real (combined stdout+stderr) output,
// and persist container_instances + port_allocations rows. This is the
// end-to-end live verification that Wave 442/443 could not complete and Wave 446
// unblocked (the compose-ps parser now tolerates provider ANSI/warning noise).
//
// Determinism controls learned across W442/443/446:
//   - pin PODMAN_COMPOSE_PROVIDER to podman-compose so `podman compose` does not
//     fall back to a daemon-requiring docker-compose plugin;
//   - keep the real HOME (Settings=nil uses default port range) so podman reuses
//     its real image/storage layers;
//   - unique COMPOSE project name via a unique chapter id to avoid cross-run
//     container-name collisions;
//   - generous live deadline (real compose up exceeds the shared 2s eventually()).
func TestContainerStartHostPortModeRunsRealCompose(t *testing.T) {
	if !realPodmanForInspect(t) {
		t.Skip("real podman not available; skipping live container start test")
	}
	pc, err := exec.LookPath("podman-compose")
	if err != nil {
		t.Skip("podman-compose not available to pin compose provider; skipping")
	}
	t.Setenv("PODMAN_COMPOSE_PROVIDER", pc)
	if len(composeVersion(context.Background()).command) == 0 {
		t.Skip("no podman compose provider available; skipping")
	}

	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	composeYML := "services:\n  web:\n    image: docker.io/library/golang:1.26-alpine\n    entrypoint: [\"sleep\", \"150\"]\n"
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte(composeYML), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	// Unique chapter id (project + chapter rows) avoids COMPOSE_PROJECT_NAME
	// collisions; Settings=nil keeps the real HOME and default port range.
	chapterID := "cstartlive" + filepath.Base(worktree)
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('pstartlive','P',?,'main','now','now')`, worktree); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, container_config, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES (?, 'pstartlive','C','active','branch','b',?,'main',?,0,0,0,0,0,0,'now','now')`, chapterID, worktree, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}

	handler := NewHandler(&Service{DB: database})
	router := newChapterRouter(handler)

	t.Cleanup(func() {
		cmd := composeCommandForFile(context.Background(), filepath.Join(worktree, "compose.yml"))
		if len(cmd) > 0 {
			downArgs := append(append([]string{}, cmd[1:]...), "down")
			c := exec.Command(cmd[0], downArgs...)
			c.Dir = worktree
			_ = c.Run()
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/"+chapterID+"/containers/start", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode start body: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("start response = %#v, want ok", body)
	}

	// Start is a background request in Go; poll generously for the real compose up.
	deadline := time.Now().Add(150 * time.Second)
	persisted := false
	for time.Now().Before(deadline) {
		var count int
		if database.QueryRow(`SELECT COUNT(*) FROM container_instances WHERE chapter_id = ?`, chapterID).Scan(&count) == nil && count > 0 {
			persisted = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !persisted {
		t.Fatal("real container instance was not persisted within the live deadline (start orchestration did not complete)")
	}

	var serviceName string
	var containerID sql.NullString
	var instanceHostPort sql.NullInt64
	if err := database.QueryRow(`SELECT service_name, container_id, host_port FROM container_instances WHERE chapter_id = ?`, chapterID).Scan(&serviceName, &containerID, &instanceHostPort); err != nil {
		t.Fatalf("query container instance: %v", err)
	}
	if serviceName == "" {
		t.Fatalf("persisted instance has empty service name")
	}
	if !containerID.Valid || containerID.String == "" {
		t.Fatalf("persisted instance has no real container id")
	}
	// Host-port mode must record exactly one port allocation in the default range,
	// and (W448) the allocated host port must be merged onto the persisted instance
	// so it survives like the TypeScript legacy-mode userMapping fallback.
	var allocations, hostPort int
	if err := database.QueryRow(`SELECT COUNT(*), COALESCE(MIN(port),0) FROM port_allocations WHERE chapter_id = ?`, chapterID).Scan(&allocations, &hostPort); err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	if allocations != 1 {
		t.Fatalf("host-port mode allocations = %d, want 1", allocations)
	}
	if hostPort < 10000 || hostPort > 20000 {
		t.Fatalf("allocated host port %d outside default range 10000-20000", hostPort)
	}
	if !instanceHostPort.Valid || int(instanceHostPort.Int64) != hostPort {
		t.Fatalf("persisted instance host_port = %v, want allocated %d", instanceHostPort, hostPort)
	}

	// W449b: a subsequent GET /{id}/containers re-derives live state from compose
	// ps and re-persists; it must NOT clobber the host port / port allocation that
	// start persisted (TS listByChapter reads persisted rows and preserves them).
	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/"+chapterID+"/containers", nil)
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("container list status = %d body = %s", listRec.Code, listRec.Body.String())
	}
	var afterAllocations, afterHostPort int
	if err := database.QueryRow(`SELECT COUNT(*), COALESCE(MIN(port),0) FROM port_allocations WHERE chapter_id = ?`, chapterID).Scan(&afterAllocations, &afterHostPort); err != nil {
		t.Fatalf("count allocations after list: %v", err)
	}
	if afterAllocations != 1 || afterHostPort != hostPort {
		t.Fatalf("container list clobbered host port allocation: allocations=%d hostPort=%d (want 1/%d)", afterAllocations, afterHostPort, hostPort)
	}
	var afterInstanceHostPort sql.NullInt64
	if err := database.QueryRow(`SELECT host_port FROM container_instances WHERE chapter_id = ?`, chapterID).Scan(&afterInstanceHostPort); err != nil {
		t.Fatalf("read instance host_port after list: %v", err)
	}
	if !afterInstanceHostPort.Valid || int(afterInstanceHostPort.Int64) != hostPort {
		t.Fatalf("container list clobbered instance host_port = %v, want %d", afterInstanceHostPort, hostPort)
	}

	// W452: pause then unpause re-persists live instances but does not re-allocate
	// ports (mappings is empty on unpause). compose ps does not surface the host
	// port in non-proxy mode, so without merging the persisted host ports back in,
	// unpause would wipe host_port to 0 on resume (W450-class state erasure on the
	// unpause path). Assert the host port and port allocation survive a pause/unpause.
	pauseRec := httptest.NewRecorder()
	pauseReq := httptest.NewRequest(http.MethodPost, "/"+chapterID+"/containers/pause", nil)
	router.ServeHTTP(pauseRec, pauseReq)
	if pauseRec.Code != http.StatusOK {
		t.Fatalf("pause status = %d body = %s", pauseRec.Code, pauseRec.Body.String())
	}
	unpauseRec := httptest.NewRecorder()
	unpauseReq := httptest.NewRequest(http.MethodPost, "/"+chapterID+"/containers/unpause", nil)
	router.ServeHTTP(unpauseRec, unpauseReq)
	if unpauseRec.Code != http.StatusOK {
		t.Fatalf("unpause status = %d body = %s", unpauseRec.Code, unpauseRec.Body.String())
	}
	var resumeAllocations, resumeHostPort int
	if err := database.QueryRow(`SELECT COUNT(*), COALESCE(MIN(port),0) FROM port_allocations WHERE chapter_id = ?`, chapterID).Scan(&resumeAllocations, &resumeHostPort); err != nil {
		t.Fatalf("count allocations after unpause: %v", err)
	}
	if resumeAllocations != 1 || resumeHostPort != hostPort {
		t.Fatalf("unpause clobbered host port allocation: allocations=%d hostPort=%d (want 1/%d)", resumeAllocations, resumeHostPort, hostPort)
	}
	var resumeInstanceHostPort sql.NullInt64
	if err := database.QueryRow(`SELECT host_port FROM container_instances WHERE chapter_id = ?`, chapterID).Scan(&resumeInstanceHostPort); err != nil {
		t.Fatalf("read instance host_port after unpause: %v", err)
	}
	if !resumeInstanceHostPort.Valid || int(resumeInstanceHostPort.Int64) != hostPort {
		t.Fatalf("unpause clobbered instance host_port = %v, want %d", resumeInstanceHostPort, hostPort)
	}

	// W454: POST /containers/remove runs `compose down --remove-orphans` and then
	// removeContainerRecordsAndPorts. Verify end-to-end against real podman that
	// (a) the DB state is fully cleared (container_instances + port_allocations)
	// and (b) the real container is actually gone — not just that the DB rows were
	// deleted while a stray container leaked. The compose project name is derived
	// from the chapter id, so the project's containers must no longer be listed.
	removeRec := httptest.NewRecorder()
	removeReq := httptest.NewRequest(http.MethodPost, "/"+chapterID+"/containers/remove", nil)
	router.ServeHTTP(removeRec, removeReq)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("remove status = %d body = %s", removeRec.Code, removeRec.Body.String())
	}
	var instanceRows, allocationRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM container_instances WHERE chapter_id = ?`, chapterID).Scan(&instanceRows); err != nil {
		t.Fatalf("count instances after remove: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM port_allocations WHERE chapter_id = ?`, chapterID).Scan(&allocationRows); err != nil {
		t.Fatalf("count allocations after remove: %v", err)
	}
	if instanceRows != 0 || allocationRows != 0 {
		t.Fatalf("remove did not clear DB state: instances=%d allocations=%d (want 0/0)", instanceRows, allocationRows)
	}
	// Confirm the real container is gone: compose ps under the chapter's project
	// name must return no running/created containers for this chapter.
	psCompose := composeCommandForFile(context.Background(), filepath.Join(worktree, "compose.yml"))
	psArgs := append(append([]string{}, psCompose[1:]...), "ps", "-a", "--format", "json")
	psCmd := exec.Command(psCompose[0], psArgs...)
	psCmd.Dir = worktree
	psCmd.Env = append(os.Environ(), composeEnvSlice(buildComposeEnv(chapterID, cfg8080(), nil, nil))...)
	psOut, _ := psCmd.CombinedOutput()
	if parsed := parseContainerJSONList(stripANSIEscapes(string(psOut))); len(parsed) != 0 {
		t.Fatalf("remove left %d real container(s) behind under the chapter compose project: %s", len(parsed), psOut)
	}
}
