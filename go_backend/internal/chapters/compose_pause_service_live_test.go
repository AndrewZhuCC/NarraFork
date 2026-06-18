package chapters

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPauseUnpauseServiceMethodsDriveRealPodmanAndDB exercises the dormant/wake
// container service methods (pauseChapterContainersLocked +
// unpauseOrStartChapterContainersLocked) against REAL podman, closing the
// "Real Podman smoke" gap for the dormant/wake container state machine. Earlier
// live coverage (compose_pause_live_test) ran compose directly; this drives the
// actual Service methods so their buildComposeEnv project-name derivation, real
// State.Status transition, and DB status update are all proven end-to-end.
func TestPauseUnpauseServiceMethodsDriveRealPodmanAndDB(t *testing.T) {
	if !realPodmanForInspect(t) {
		t.Skip("real podman not available; skipping live pause/unpause service test")
	}
	pc, err := exec.LookPath("podman-compose")
	if err != nil {
		t.Skip("podman-compose not available to pin compose provider; skipping")
	}
	t.Setenv("PODMAN_COMPOSE_PROVIDER", pc)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	if len(composeVersion(ctx).command) == 0 {
		t.Skip("no podman compose provider available; skipping")
	}

	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	composeYML := "services:\n  web:\n    image: docker.io/library/golang:1.26-alpine\n    entrypoint: [\"sleep\", \"150\"]\n"
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte(composeYML), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	chapterID := "cpauselive" + filepath.Base(worktree)
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('ppauselive','P',?,'main','now','now')`, worktree); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, container_config, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES (?, 'ppauselive','C','active','branch','b',?,'main',?,0,0,0,0,0,0,'now','now')`, chapterID, worktree, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}

	svc := &Service{DB: database}
	ch, err := svc.Get(ctx, chapterID)
	if err != nil {
		t.Fatalf("get chapter: %v", err)
	}

	// Bring containers up via the service start path so the compose project name
	// matches what pause/unpause derive.
	if err := svc.startChapterContainersLocked(ctx, ch); err != nil {
		t.Skipf("startChapterContainersLocked failed (registry/storage unavailable?): %v", err)
	}
	cmd := composeCommandForFile(ctx, filepath.Join(worktree, "compose.yml"))
	env := buildComposeEnv(chapterID, cfg8080(), nil, nil)
	t.Cleanup(func() {
		downArgs := append(append([]string{}, cmd[1:]...), "down")
		c := exec.Command(cmd[0], downArgs...)
		c.Dir = worktree
		c.Env = append(os.Environ(), composeEnvSlice(env)...)
		_ = c.Run()
	})

	// Real container state via the chapter's compose project must be running.
	if got := liveComposeStatus(ctx, t, worktree, cmd, env); got != "running" {
		t.Fatalf("after start, real compose status = %q, want running", got)
	}

	// Dormant path: pauseChapterContainersLocked must pause the real container and
	// set DB status=paused.
	paused, err := svc.pauseChapterContainersLocked(ctx, ch)
	if err != nil {
		t.Fatalf("pauseChapterContainersLocked: %v", err)
	}
	if !paused {
		t.Fatal("pauseChapterContainersLocked returned attempted=false with real podman + compose file")
	}
	if got := liveComposeStatus(ctx, t, worktree, cmd, env); got != "paused" {
		t.Fatalf("after pause, real compose status = %q, want paused (wrong project name?)", got)
	}
	if got := dbContainerStatus(ctx, t, database, chapterID); got != "paused" {
		t.Fatalf("after pause, DB status = %q, want paused", got)
	}

	// Wake path: unpauseOrStartChapterContainersLocked must resume the real
	// container and set DB status=running.
	event, err := svc.unpauseOrStartChapterContainersLocked(ctx, chapterID)
	if err != nil {
		t.Fatalf("unpauseOrStartChapterContainersLocked: %v", err)
	}
	if event != "container:resumed" {
		t.Fatalf("wake event = %q, want container:resumed (unpause should succeed, not fall back to fresh start)", event)
	}
	if got := liveComposeStatus(ctx, t, worktree, cmd, env); got != "running" {
		t.Fatalf("after unpause, real compose status = %q, want running", got)
	}
	if got := dbContainerStatus(ctx, t, database, chapterID); got != "running" {
		t.Fatalf("after unpause, DB status = %q, want running", got)
	}
}

// liveComposeStatus returns the normalized status of the first instance reported
// by `compose ps` under the chapter's compose project env.
func liveComposeStatus(ctx context.Context, t *testing.T, worktree string, cmd []string, env map[string]string) string {
	t.Helper()
	psArgs := append(append([]string{}, cmd[1:]...), "ps", "--format", "json")
	c := exec.CommandContext(ctx, cmd[0], psArgs...)
	c.Dir = worktree
	c.Env = append(os.Environ(), composeEnvSlice(env)...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("compose ps failed: %v\n%s", err, out)
	}
	raw := parseContainerJSONList(stripANSIEscapes(string(out)))
	if len(raw) == 0 {
		return ""
	}
	status, _ := normalizeContainerInstance("x", raw[0])["status"].(string)
	return status
}

// dbContainerStatus reads the persisted container_instances status for a chapter.
func dbContainerStatus(ctx context.Context, t *testing.T, database *sql.DB, chapterID string) string {
	t.Helper()
	var status string
	if err := database.QueryRowContext(ctx, `SELECT status FROM container_instances WHERE chapter_id = ? LIMIT 1`, chapterID).Scan(&status); err != nil {
		t.Fatalf("read db container status: %v", err)
	}
	return status
}
