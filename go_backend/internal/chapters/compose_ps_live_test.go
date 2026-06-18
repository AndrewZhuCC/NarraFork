package chapters

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestComposePsParsesRealPodmanComposeOutput runs a REAL `podman compose up -d`
// lifecycle and verifies parseContainerJSONList + normalizeContainerInstance
// correctly parse podman-compose's actual `ps --format json` output. This closes
// the fixture-only gap for the compose instance-listing parse path: real podman
// 5.x emits a JSON array of objects with Id/Image/Names/State/Labels, and the
// runtime must extract containerId/serviceName/status/image from that exact
// shape (fake fixtures only synthesize an approximation).
func TestComposePsParsesRealPodmanComposeOutput(t *testing.T) {
	if !realPodmanForInspect(t) {
		t.Skip("real podman not available; skipping live compose-ps parity test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	composeCmd := composeVersion(ctx)
	if len(composeCmd.command) == 0 {
		t.Skip("no podman compose provider available; skipping")
	}

	// Work inside the package dir (a real temp subdir under the repo, never /tmp).
	work, err := os.MkdirTemp(".", "compose-live-")
	if err != nil {
		t.Fatalf("mkdir temp work: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	composeFile := filepath.Join(work, "compose.yml")
	composeYML := "services:\n  app:\n    image: docker.io/library/golang:1.26-alpine\n    entrypoint: [\"sleep\", \"120\"]\n"
	if err := os.WriteFile(composeFile, []byte(composeYML), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	cmd := composeCommandForFile(ctx, composeFile)
	if len(cmd) == 0 {
		t.Skip("composeCommandForFile resolved empty; skipping")
	}

	upArgs := append(append([]string{}, cmd[1:]...), "up", "-d")
	if out, err := exec.CommandContext(ctx, cmd[0], upArgs...).CombinedOutput(); err != nil {
		t.Skipf("compose up failed (registry/storage unavailable?): %v\n%s", err, out)
	}
	t.Cleanup(func() {
		downArgs := append(append([]string{}, cmd[1:]...), "down")
		_ = exec.Command(cmd[0], downArgs...).Run()
	})

	psArgs := append(append([]string{}, cmd[1:]...), "ps", "--format", "json")
	psOut, err := exec.CommandContext(ctx, cmd[0], psArgs...).Output()
	if err != nil {
		t.Fatalf("compose ps failed: %v", err)
	}

	raw := parseContainerJSONList(string(psOut))
	if len(raw) == 0 {
		t.Fatalf("parseContainerJSONList returned no items from real compose ps output:\n%s", string(psOut))
	}

	// Find the app service container among parsed instances.
	var appInstance map[string]any
	for _, item := range raw {
		norm := normalizeContainerInstance("chap-live", item)
		if norm["status"] == "running" {
			appInstance = norm
			break
		}
	}
	if appInstance == nil {
		// Fall back to the first instance if none reported running yet.
		appInstance = normalizeContainerInstance("chap-live", raw[0])
	}

	if cid, _ := appInstance["containerId"].(string); strings.TrimSpace(cid) == "" {
		t.Fatalf("normalizeContainerInstance did not extract a containerId from real output: %#v", appInstance)
	}
	if appInstance["chapterId"] != "chap-live" {
		t.Fatalf("chapterId not stamped: %#v", appInstance["chapterId"])
	}
	if svc, _ := appInstance["serviceName"].(string); strings.TrimSpace(svc) == "" {
		t.Fatalf("serviceName should be non-empty: %#v", appInstance)
	}
	if img, _ := appInstance["image"].(string); !strings.Contains(img, "golang") {
		t.Fatalf("image should reflect the compose image, got %#v", appInstance["image"])
	}
	// status must be one of the normalized container states.
	switch appInstance["status"] {
	case "running", "created", "paused", "stopped", "removed":
	default:
		t.Fatalf("unexpected normalized status %#v", appInstance["status"])
	}
}
