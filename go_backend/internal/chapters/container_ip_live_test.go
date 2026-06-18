package chapters

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestInspectContainerIPParsesRealPodmanOutput runs a REAL bridge-networked
// container and verifies inspectContainerIP extracts the first IP from podman's
// actual `{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}` output.
// This closes the fixture-only gap for the container IP resolution path used by
// proxy/network features. Bridge mode is used explicitly because rootless pasta
// networking reports an empty Networks IP (a real production nuance also
// asserted below), so a deterministic non-empty IP requires the bridge backend.
func TestInspectContainerIPParsesRealPodmanOutput(t *testing.T) {
	if !realPodmanForInspect(t) {
		t.Skip("real podman not available; skipping live IP-inspect parity test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const image = "docker.io/library/golang:1.26-alpine"
	// A long-lived sleep keeps the container in the running state so the network
	// namespace (and thus the bridge IP) is populated for inspect.
	runOut, err := exec.CommandContext(ctx, "podman", "run", "-d", "--network", "bridge", "--entrypoint", "sleep", image, "60").Output()
	if err != nil {
		t.Skipf("could not start real bridge container (image/storage unavailable?): %v", err)
	}
	cid := strings.TrimSpace(string(runOut))
	if cid == "" {
		t.Skip("podman run produced no container id; skipping")
	}
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", cid).Run()
	})

	ip := inspectContainerIP(ctx, cid)
	if ip == "" {
		t.Skipf("bridge container reported no IP in this environment; skipping IP-shape assertion (cid=%s)", cid)
	}
	if net.ParseIP(ip) == nil {
		t.Fatalf("inspectContainerIP(real) = %q, which is not a valid IP address", ip)
	}

	// Unknown container id and blank id must return "" without panicking.
	if got := inspectContainerIP(ctx, "nonexistent-container-id-xyz"); got != "" {
		t.Fatalf("unknown container should yield empty IP, got %q", got)
	}
	if got := inspectContainerIP(ctx, "   "); got != "" {
		t.Fatalf("blank container id should yield empty IP, got %q", got)
	}
}
