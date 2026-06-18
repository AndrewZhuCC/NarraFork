package fs

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"narrafork-go/internal/config"
)

// TestShortcutsReturnsExistingDirsAndContractFields covers the GET /api/fs/shortcuts
// route used by the frontend directory picker. The handler builds home/desktop/
// documents/downloads candidates, an OS-specific root, and a configured project
// dir, then filters to existing directories. This route previously had no test.
func TestShortcutsReturnsExistingDirsAndContractFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create only some of the standard subdirs so we can prove existence filtering.
	if err := os.Mkdir(filepath.Join(home, "Desktop"), 0o755); err != nil {
		t.Fatalf("mkdir Desktop: %v", err)
	}
	if err := os.Mkdir(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatalf("mkdir Downloads: %v", err)
	}
	// Intentionally do NOT create Documents -> it must be filtered out.

	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	w := serveFSRaw(t, settings, http.MethodGet, "/fs/shortcuts", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode shortcuts body: %v", err)
	}

	// Contract fields: shortcuts array, drives array, sep string.
	shortcuts, ok := body["shortcuts"].([]any)
	if !ok {
		t.Fatalf("shortcuts should be an array, got %#v", body["shortcuts"])
	}
	if _, ok := body["drives"].([]any); !ok {
		t.Fatalf("drives should be an array, got %#v", body["drives"])
	}
	if body["sep"] != string(os.PathSeparator) {
		t.Fatalf("sep should be path separator, got %#v", body["sep"])
	}

	keys := map[string]string{}
	for _, raw := range shortcuts {
		entry, _ := raw.(map[string]any)
		key, _ := entry["key"].(string)
		path, _ := entry["path"].(string)
		keys[key] = path
	}

	// home, desktop, downloads exist -> present. documents does not -> absent.
	if keys["home"] != home {
		t.Fatalf("expected home shortcut %q, got %#v", home, keys)
	}
	if _, ok := keys["desktop"]; !ok {
		t.Fatalf("expected desktop shortcut, got %#v", keys)
	}
	if _, ok := keys["downloads"]; !ok {
		t.Fatalf("expected downloads shortcut, got %#v", keys)
	}
	if _, ok := keys["documents"]; ok {
		t.Fatalf("documents shortcut should be filtered out (dir missing), got %#v", keys)
	}

	// Non-Windows exposes a filesystem root shortcut.
	if runtime.GOOS != "windows" {
		if _, ok := keys["root"]; !ok {
			t.Fatalf("expected root shortcut on non-windows, got %#v", keys)
		}
	}
}

// TestShortcutsIncludesConfiguredProjectDir verifies the configured default
// project directory is surfaced as a "projects" shortcut when it exists.
func TestShortcutsIncludesConfiguredProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectDir := filepath.Join(home, "my-projects")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	// Seed settings.json with paths.defaultProjectDir before config.Load reads it.
	settingsDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir settings dir: %v", err)
	}
	seed := map[string]any{"paths": map[string]any{"defaultProjectDir": projectDir}}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), data, 0o644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if settings.Core().Paths.DefaultProjectDir != projectDir {
		t.Fatalf("expected configured project dir %q, got %q", projectDir, settings.Core().Paths.DefaultProjectDir)
	}

	w := serveFSRaw(t, settings, http.MethodGet, "/fs/shortcuts", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	shortcuts, _ := body["shortcuts"].([]any)
	found := false
	for _, raw := range shortcuts {
		entry, _ := raw.(map[string]any)
		if entry["key"] == "projects" && entry["path"] == projectDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected projects shortcut pointing at %q, got %#v", projectDir, shortcuts)
	}
}
