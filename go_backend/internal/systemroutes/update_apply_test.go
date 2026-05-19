package systemroutes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"narrafork-go/internal/restart"
)

func TestUpdateApplyStartsReplacementWithVerifiedArtifact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	currentExe := filepath.Join(home, "narrafork-go")
	if err := os.WriteFile(currentExe, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := []byte("new-binary")
	versionDir := filepath.Join(home, ".narrafork", "updates", "3.0.0")
	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(versionDir, "artifact")
	if err := os.WriteFile(artifactPath, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	mustWriteUpdateMetadata(t, versionDir, updateDownloadMetadata{Version: "3.0.0", Directory: versionDir, Files: []updateDownloadedFileInfo{{Name: "artifact", Path: artifactPath, Size: int64(len(artifact)), SHA512: sha512Hex(artifact)}}})

	origExec := updateExecutablePath
	origStart := updateStartReplacement
	updateExecutablePath = func() (string, error) { return currentExe, nil }
	var startedPath string
	var startedEnv []string
	updateStartReplacement = func(ctx context.Context, path string, args []string, env []string) (int, error) {
		startedPath = path
		startedEnv = env
		return 12345, nil
	}
	t.Cleanup(func() { updateExecutablePath = origExec; updateStartReplacement = origStart })
	h := NewHandler(nil, nil)
	h.Restart = restart.NewManager(restart.RuntimeAddress{Protocol: "http", Host: "127.0.0.1", Port: 7778}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", strings.NewReader(`{"version":"3.0.0"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.updateApply(w, req); err != nil {
		t.Fatalf("updateApply: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["success"] != true || int(body["replacementPid"].(float64)) != 12345 || startedPath == "" {
		t.Fatalf("unexpected apply body=%#v startedPath=%q", body, startedPath)
	}
	if body["ready"] != true || body["placed"] != true || body["selfUpdateAvailable"] != true || body["canAutoRestart"] != true || body["manualOnly"] != false {
		t.Fatalf("unexpected auto apply contract: %#v", body)
	}
	instructions, _ := body["instructions"].(map[string]any)
	if instructions["manual"] != false || instructions["action"] != "apply" {
		t.Fatalf("expected automatic apply instructions, got %#v", instructions)
	}
	prepared, err := os.ReadFile(startedPath)
	if err != nil || string(prepared) != string(artifact) {
		t.Fatalf("prepared artifact mismatch data=%q err=%v", string(prepared), err)
	}
	if !envContainsPrefix(startedEnv, restart.EnvURL+"=") || !envContainsPrefix(startedEnv, restart.EnvToken+"=") {
		t.Fatalf("missing handoff env: %#v", startedEnv)
	}
}

func TestUpdateApplyRejectsEscapedArtifactPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	currentExe := filepath.Join(home, "narrafork-go")
	if err := os.WriteFile(currentExe, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	versionDir := filepath.Join(home, ".narrafork", "updates", "escape")
	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "outside")
	if err := os.WriteFile(outside, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustWriteUpdateMetadata(t, versionDir, updateDownloadMetadata{Version: "escape", Directory: versionDir, Files: []updateDownloadedFileInfo{{Name: "artifact", Path: outside, Size: 3, SHA512: sha512Hex([]byte("bad"))}}})
	origExec := updateExecutablePath
	updateExecutablePath = func() (string, error) { return currentExe, nil }
	t.Cleanup(func() { updateExecutablePath = origExec })
	h := NewHandler(nil, nil)
	h.Restart = restart.NewManager(restart.RuntimeAddress{Protocol: "http", Host: "127.0.0.1", Port: 7778}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/update/apply", strings.NewReader(`{"version":"escape"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.updateApply(w, req); err != nil {
		t.Fatalf("updateApply: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["success"] != false || !strings.Contains(body["error"].(string), "outside") {
		t.Fatalf("expected escaped path rejection, got %#v", body)
	}
	if body["selfUpdateAvailable"] != true || body["canAutoRestart"] != false || body["manualOnly"] != false {
		t.Fatalf("expected failure to preserve auto-capable runtime contract, got %#v", body)
	}
	instructions, _ := body["instructions"].(map[string]any)
	if instructions["manual"] != true {
		t.Fatalf("expected failed apply to return manual instructions, got %#v", instructions)
	}
}

func mustWriteUpdateMetadata(t *testing.T, dir string, metadata updateDownloadMetadata) {
	t.Helper()
	if err := writeUpdateDownloadMetadata(dir, metadata); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
}

func envContainsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
