package chapters

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveComposeProxyModeResolvesRoutes verifies the proxy-mode branch of
// liveComposeContainerInstances against a REAL podman compose container: given a
// non-nil proxyInfo, it must resolve one proxy route per configured container
// port (proxyLabel + proxyUrl + containerPort), reading the running container's
// exposed/configured ports through the W447-hardened inspect parsers. The proxy
// branch was previously only exercised by fake-Podman fixtures. proxyInfo is
// passed directly so the test does not need a Settings store (which would force
// a temp HOME and break real podman storage — the W448 lesson).
func TestLiveComposeProxyModeResolvesRoutes(t *testing.T) {
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
	composeYML := "services:\n  web:\n    image: docker.io/library/golang:1.26-alpine\n    entrypoint: [\"sleep\", \"150\"]\n"
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte(composeYML), 0o644); err != nil {
		t.Fatal(err)
	}
	database := openContainerRuntimeDB(t)
	chapterID := "cproxylive" + filepath.Base(worktree)
	svc := &Service{DB: database}
	cfg := containerConfigFrom(map[string]any{
		"ports": []any{map[string]any{"containerPort": float64(8080), "serviceName": "web"}},
	})
	composeFile := resolveComposeFilePath(worktree, cfg)
	cmd := composeCommandForFile(ctx, composeFile)
	env := buildComposeEnv(chapterID, cfg, nil, &proxyRuntimeInfo{Domain: "dev.example.test", Port: 7780, ChapterShortID: shortPrefix(chapterID, 8)})

	upArgs := append(append([]string{}, cmd[1:]...), "up", "-d")
	if _, err := runLimitedEnv(ctx, worktree, env, cmd[0], upArgs...); err != nil {
		t.Skipf("compose up failed (env-gated): %v", err)
	}
	t.Cleanup(func() {
		downArgs := append(append([]string{}, cmd[1:]...), "down")
		_, _ = runLimitedEnv(context.Background(), worktree, env, cmd[0], downArgs...)
	})

	proxyInfo := &proxyRuntimeInfo{Domain: "dev.example.test", Port: 7780, ChapterShortID: shortPrefix(chapterID, 8)}
	items, err := svc.liveComposeContainerInstances(ctx, chapterID, worktree, cmd, env, cfg, proxyInfo)
	if err != nil {
		t.Fatalf("liveComposeContainerInstances (proxy) err: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("proxy mode resolved 0 routes from a running container (port resolution / inspect parse failed)")
	}

	// At least one route for the configured 8080 port with a derived proxy label/url.
	found := false
	for _, item := range items {
		if intValue(item["containerPort"]) != 8080 {
			continue
		}
		label, _ := item["proxyLabel"].(string)
		url, _ := item["proxyUrl"].(string)
		if label == "" || url == "" {
			t.Fatalf("proxy route missing label/url: %#v", item)
		}
		if !strings.Contains(url, "dev.example.test") {
			t.Fatalf("proxy url should embed the domain, got %q", url)
		}
		if item["hostPort"] != nil {
			t.Fatalf("proxy mode route must not carry a host port, got %v", item["hostPort"])
		}
		found = true
	}
	if !found {
		t.Fatalf("no proxy route resolved for configured container port 8080: %#v", items)
	}
}
