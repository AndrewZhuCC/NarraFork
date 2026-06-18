package chapters

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// realPodmanForInspect returns true when a real podman binary is present and
// can run/inspect a container, so the live inspect-parser parity test can run.
func realPodmanForInspect(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("podman"); err != nil {
		return false
	}
	if commandVersion(context.Background(), "podman", "--version") == "" {
		return false
	}
	return true
}

// TestInspectContainerExposedPortsParsesRealPodmanOutput runs a REAL container
// with known exposed ports and verifies inspectContainerExposedPorts parses
// podman's actual `{{json .Config.ExposedPorts}}` output (e.g.
// {"8080/tcp":{},"9090/tcp":{}}) into the sorted int slice the runtime relies
// on. This closes the matrix concern that container inspect parsing was only
// exercised against fake-Podman fixtures whose output format may drift from the
// real podman JSON shape.
func TestInspectContainerExposedPortsParsesRealPodmanOutput(t *testing.T) {
	if !realPodmanForInspect(t) {
		t.Skip("real podman not available; skipping live inspect parity test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// hello-world is tiny, already pulled, and exits immediately — we only need
	// the container record for inspect, not a running process.
	const image = "docker.io/library/hello-world"
	runOut, err := exec.CommandContext(ctx, "podman", "run", "-d", "--expose", "8080", "--expose", "9090", image).Output()
	if err != nil {
		t.Skipf("could not start real container (registry/storage unavailable?): %v", err)
	}
	cid := strings.TrimSpace(string(runOut))
	if cid == "" {
		t.Skip("podman run produced no container id; skipping")
	}
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", cid).Run()
	})

	got := inspectContainerExposedPorts(ctx, cid)
	want := []int{8080, 9090}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inspectContainerExposedPorts(real) = %v, want %v", got, want)
	}

	// An unknown container id must yield nil (inspect failure path) without panic.
	if ports := inspectContainerExposedPorts(ctx, "nonexistent-container-id-xyz"); ports != nil {
		t.Fatalf("unknown container should yield nil ports, got %v", ports)
	}
	// Empty id short-circuits to nil.
	if ports := inspectContainerExposedPorts(ctx, "   "); ports != nil {
		t.Fatalf("blank container id should yield nil ports, got %v", ports)
	}
}
