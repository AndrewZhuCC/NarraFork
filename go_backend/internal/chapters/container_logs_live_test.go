package chapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestContainerLogsUsesComposeProjectName is a regression for the W450-class bug
// in the logs endpoint: GET /{id}/containers/logs previously ran `compose logs`
// with a nil env, so it targeted the DEFAULT compose project instead of the
// chapter's COMPOSE_PROJECT_NAME=narrafork_<chapterId> and returned no logs for
// the chapter's containers. With the fix, logs for a container that prints a
// known marker must contain that marker.
func TestContainerLogsUsesComposeProjectName(t *testing.T) {
	if !realPodmanForInspect(t) {
		t.Skip("real podman not available")
	}
	pc, err := exec.LookPath("podman-compose")
	if err != nil {
		t.Skip("podman-compose not available")
	}
	t.Setenv("PODMAN_COMPOSE_PROVIDER", pc)
	if len(composeVersion(context.Background()).command) == 0 {
		t.Skip("no podman compose provider")
	}

	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	const marker = "NARRAFORK_LOG_MARKER_42"
	composeYML := "services:\n  web:\n    image: docker.io/library/golang:1.26-alpine\n    entrypoint: [\"sh\", \"-c\", \"echo " + marker + "; sleep 150\"]\n"
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte(composeYML), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	chapterID := "cloglive" + filepath.Base(worktree)
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, container_config, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES (?, 'plog','C','active','branch','b',?,'main',?,0,0,0,0,0,0,'now','now')`, chapterID, worktree, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}

	handler := NewHandler(&Service{DB: database})
	router := newChapterRouter(handler)

	// Bring the project up under its real COMPOSE_PROJECT_NAME so the marker is logged.
	cmd := composeCommandForFile(context.Background(), filepath.Join(worktree, "compose.yml"))
	env := buildComposeEnv(chapterID, cfg8080(), nil, nil)
	upArgs := append(append([]string{}, cmd[1:]...), "up", "-d")
	upCmd := exec.Command(cmd[0], upArgs...)
	upCmd.Dir = worktree
	upCmd.Env = append(os.Environ(), composeEnvSlice(env)...)
	if out, err := upCmd.CombinedOutput(); err != nil {
		t.Skipf("compose up failed (env-gated): %v\n%s", err, out)
	}
	t.Cleanup(func() {
		downArgs := append(append([]string{}, cmd[1:]...), "down")
		c := exec.Command(cmd[0], downArgs...)
		c.Dir = worktree
		c.Env = append(os.Environ(), composeEnvSlice(env)...)
		_ = c.Run()
	})

	// Poll the logs endpoint until the marker shows up (echo happens at startup).
	deadline := time.Now().Add(60 * time.Second)
	var lastBody string
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/"+chapterID+"/containers/logs", nil)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("logs status = %d body = %s", rec.Code, rec.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode logs: %v", err)
		}
		lastBody, _ = payload["logs"].(string)
		if strings.Contains(lastBody, marker) {
			return // success: logs found under the correct compose project
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("logs endpoint did not return the container marker %q (wrong compose project?), last logs=%q", marker, lastBody)
}

func cfg8080() chapterContainerConfig {
	return containerConfigFrom(map[string]any{"ports": []any{map[string]any{"containerPort": float64(8080), "serviceName": "web"}}})
}

func composeEnvSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
