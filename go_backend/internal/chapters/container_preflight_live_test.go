package chapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// podmanRootlessAvailable reports whether a real rootless podman with a
// pasta/passt network backend is present, so the real-environment container
// preflight test can run. It is intentionally conservative: any probe failure
// causes the test to skip rather than fail, keeping CI without podman green.
func podmanRootlessAvailable(t *testing.T) (podmanVersion string, backend string, ok bool) {
	t.Helper()
	if _, err := exec.LookPath("podman"); err != nil {
		return "", "", false
	}
	ctx := context.Background()
	ver := commandVersion(ctx, "podman", "--version")
	if ver == "" {
		return "", "", false
	}
	out, err := exec.CommandContext(ctx, "podman", "info", "--format", "{{.Host.RootlessNetworkCmd}}").Output()
	if err != nil {
		return ver, "", false
	}
	be := strings.ToLower(strings.Trim(strings.TrimSpace(string(out)), "'\""))
	return ver, be, true
}

// TestContainerSetupReportsRealPodmanPreflight exercises GET /container-setup
// against the REAL podman binary (not the shell fake used by other tests),
// closing the matrix gap "real rootless Podman/cgroup behavior remains
// unverified beyond fake-Podman fixtures" for the preflight path. It asserts the
// payload shape matches the TS getContainerSetupStatus contract
// (podman/podmanCompose/composeProvider/passt/rootlessNetwork/allReady) and that
// a real pasta/passt host reports rootlessNetwork.ok=true.
func TestContainerSetupReportsRealPodmanPreflight(t *testing.T) {
	ver, backend, ok := podmanRootlessAvailable(t)
	if !ok {
		t.Skip("real rootless podman not available; skipping live preflight parity test")
	}

	router := newChapterRouter(NewHandler(&Service{}))
	req := httptest.NewRequest(http.MethodGet, "/container-setup", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("container-setup status = %d body=%s", w.Code, w.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	// Shape parity with TS ContainerSetupStatus: all keys present as objects.
	for _, key := range []string{"podman", "podmanCompose", "composeProvider", "passt", "rootlessNetwork"} {
		sub, isObj := payload[key].(map[string]any)
		if !isObj {
			t.Fatalf("payload[%q] should be an object, got %#v", key, payload[key])
		}
		if _, hasOK := sub["ok"]; !hasOK {
			t.Fatalf("payload[%q] missing ok flag: %#v", key, sub)
		}
	}
	if _, hasAllReady := payload["allReady"].(bool); !hasAllReady {
		t.Fatalf("payload.allReady should be a bool, got %#v", payload["allReady"])
	}

	// Real podman is present, so podman.ok must be true and report a version.
	podmanInfo := payload["podman"].(map[string]any)
	if podmanInfo["ok"] != true {
		t.Fatalf("real podman present but podman.ok != true: %#v", podmanInfo)
	}
	if v, _ := podmanInfo["version"].(string); strings.TrimSpace(v) == "" {
		t.Fatalf("real podman present but version empty: %#v (probe saw %q)", podmanInfo, ver)
	}

	// If the host network backend is pasta/passt, rootlessNetwork.ok must be true
	// and the backend string must round-trip through the handler.
	rootless := payload["rootlessNetwork"].(map[string]any)
	if backend == "pasta" || backend == "passt" {
		if rootless["ok"] != true {
			t.Fatalf("pasta/passt host but rootlessNetwork.ok != true: %#v", rootless)
		}
		if got, _ := rootless["backend"].(string); got != backend {
			t.Fatalf("rootlessNetwork.backend = %q, want %q", got, backend)
		}
	}
}
