package chapters

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLiveComposeInstancesThroughRunLimitedEnv verifies the PRODUCTION code path
// (liveComposeContainerInstances -> runLimitedEnv, which combines stdout+stderr)
// recovers container instances from real podman-compose output even when the
// provider emits ANSI/warning noise into the combined stream. Regression for the
// Wave 446 parser fix: previously this returned 0 items, so `compose up`
// persisted nothing.
func TestLiveComposeInstancesThroughRunLimitedEnv(t *testing.T) {
	if !realPodmanForInspect(t) {
		t.Skip("real podman not available")
	}
	if pc, err := exec.LookPath("podman-compose"); err == nil {
		t.Setenv("PODMAN_COMPOSE_PROVIDER", pc)
	}
	ctx := context.Background()
	if len(composeVersion(ctx).command) == 0 {
		t.Skip("no podman compose provider")
	}

	worktree := t.TempDir()
	composeYML := "services:\n  web:\n    image: docker.io/library/golang:1.26-alpine\n    entrypoint: [\"sleep\", \"120\"]\n"
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte(composeYML), 0o644); err != nil {
		t.Fatal(err)
	}
	database := openContainerRuntimeDB(t)
	chapterID := "clivecombined" + filepath.Base(worktree)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, container_config, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES (?, 'p','C','active','branch','b',?,'main',?,0,0,0,0,0,0,'now','now')`, chapterID, worktree, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`); err != nil {
		t.Fatal(err)
	}
	svc := &Service{DB: database}
	cfg := containerConfigFrom(map[string]any{
		"ports": []any{map[string]any{"containerPort": float64(8080), "serviceName": "web"}},
	})
	composeFile := resolveComposeFilePath(worktree, cfg)
	cmd := composeCommandForFile(ctx, composeFile)
	env := buildComposeEnv(chapterID, cfg, []map[string]any{{"hostPort": 13457, "containerPort": 8080, "serviceName": "web"}}, nil)

	upArgs := append(append([]string{}, cmd[1:]...), "up", "-d")
	if _, err := runLimitedEnv(ctx, worktree, env, cmd[0], upArgs...); err != nil {
		t.Skipf("compose up failed (env-gated): %v", err)
	}
	t.Cleanup(func() {
		downArgs := append(append([]string{}, cmd[1:]...), "down")
		_, _ = runLimitedEnv(context.Background(), worktree, env, cmd[0], downArgs...)
	})

	items, err := svc.liveComposeContainerInstances(ctx, chapterID, worktree, cmd, env, cfg, nil)
	if err != nil {
		t.Fatalf("liveComposeContainerInstances err: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("liveComposeContainerInstances returned 0 items through the combined-output production path (parser failed to recover JSON from provider noise)")
	}
}
