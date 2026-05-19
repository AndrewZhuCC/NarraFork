package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/ws"
)

func TestPublicShareRoutesOnlyAllowDownloadsAndPreviews(t *testing.T) {
	router := NewRouter(Services{Auth: &auth.Service{}, Hub: ws.NewHub()})

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "download", path: "/api/shares/missing-share"},
		{name: "preview", path: "/api/shares/missing-share/preview"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code == http.StatusUnauthorized {
				t.Fatalf("GET %s should remain public, got 401", tc.path)
			}
			if w.Code != http.StatusNotFound {
				t.Fatalf("GET %s for a missing share should reach the public share handler and return 404, got %d", tc.path, w.Code)
			}
		})
	}

	req := httptest.NewRequest(http.MethodPost, "/api/shares/", strings.NewReader(`{"path":"/tmp/x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("POST /api/shares/ should require auth, got %d", w.Code)
	}
}

func TestHealthExposesVersionCommitAndRuntimeCapabilities(t *testing.T) {
	t.Setenv("NARRAFORK_VERSION", "1.2.3-test")
	t.Setenv("NARRAFORK_COMMIT", "abc123")
	router := NewRouter(Services{Auth: &auth.Service{}, Hub: ws.NewHub()})
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected health 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeRouterJSON(t, w, &body)
	if body["version"] != "1.2.3-test" || body["commit"] != "abc123" || body["backend"] != "go" || body["buildChannel"] != "dev" {
		t.Fatalf("expected version/commit/backend metadata, got %#v", body)
	}
	capabilities, _ := body["capabilities"].(map[string]any)
	vnet, _ := capabilities["vnet"].(map[string]any)
	update, _ := capabilities["update"].(map[string]any)
	terminal, _ := capabilities["terminal"].(map[string]any)
	gateway, _ := capabilities["gateway"].(map[string]any)
	weixinQR, _ := gateway["weixinQr"].(map[string]any)
	storage, _ := capabilities["storage"].(map[string]any)
	vacuum, _ := storage["vacuum"].(map[string]any)
	cleanup, _ := storage["cleanup"].(map[string]any)
	cleanupUploads, _ := cleanup["uploads"].(map[string]any)
	cleanupWorktrees, _ := cleanup["worktrees"].(map[string]any)
	if vnet["supported"] != true || vnet["ws"] != true || vnet["mode"] != "relay" || update["manualOnly"] != true || terminal["multiClientResizeMode"] != "last-input-client" || gateway["persistentRuntimes"] != true || weixinQR["supported"] != false || weixinQR["code"] != "WEIXIN_QR_UNSUPPORTED" || vacuum["supported"] != false || cleanupUploads["supported"] != true || cleanupWorktrees["supported"] != false || cleanupWorktrees["fallback"] != true {
		t.Fatalf("unexpected capability contract: %#v", capabilities)
	}
}

func TestRunShellCommandLimitsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script uses POSIX sh")
	}
	binDir := t.TempDir()
	path := filepath.Join(binDir, "sh")
	script := "#!/bin/sh\ni=0\nwhile [ $i -lt " + strconv.Itoa(maxDependencyInstallOutputBytes+1024) + " ]; do printf x; i=$((i+1)); done\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sh: %v", err)
	}
	t.Setenv("PATH", binDir)

	output, err := runShellCommand(context.Background(), "ignored")
	if err == nil {
		t.Fatalf("expected fake shell to fail")
	}
	if !strings.Contains(output, "[truncated]") {
		t.Fatalf("expected truncated marker, got len=%d", len(output))
	}
	if len(output) > maxDependencyInstallOutputBytes+64 {
		t.Fatalf("output was not capped: len=%d", len(output))
	}
}

func TestInstallDependencyDoesNotReportSuccessWhenCommandDoesNotInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake shell script uses POSIX sh")
	}
	binDir := t.TempDir()
	for _, name := range []string{"apt-get", "sh"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	t.Setenv("PATH", binDir)

	result := installDependency(context.Background(), "rg", nil)
	if result["ok"] != false || result["code"] != "DEPENDENCY_INSTALL_NOT_VERIFIED" {
		t.Fatalf("install should not report fake success after no-op command: %#v", result)
	}
	dependency, _ := result["dependency"].(map[string]any)
	if dependency["installed"] != false {
		t.Fatalf("dependency should remain unavailable: %#v", dependency)
	}
}

func TestChangelogReadsConfiguredDirectoryAndSortsDescending(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("NARRAFORK_CHANGELOG_DIR", dir)
	writeChangelogFixture(t, dir, "v1.0.0.json", `{"version":"1.0.0","date":"2026-01-01","en":"old","zh-CN":"旧"}`)
	writeChangelogFixture(t, dir, "v1.2.0.json", `{"version":"1.2.0","date":"2026-02-01","en":"new","zh-CN":"新"}`)
	router := NewRouter(Services{Auth: &auth.Service{}, Hub: ws.NewHub()})
	req := httptest.NewRequest(http.MethodGet, "/api/changelog", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected changelog 200, got %d: %s", w.Code, w.Body.String())
	}
	var entries []map[string]any
	decodeRouterJSON(t, w, &entries)
	if len(entries) != 2 || entries[0]["version"] != "1.2.0" || entries[1]["version"] != "1.0.0" {
		t.Fatalf("unexpected changelog order: %#v", entries)
	}
}

func TestStaticFrontendHostingAndAPIOnlyFallback(t *testing.T) {
	t.Run("serves bundled frontend", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<div>NarraFork</div>"), 0o600); err != nil {
			t.Fatalf("write index: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "asset.txt"), []byte("asset"), 0o600); err != nil {
			t.Fatalf("write asset: %v", err)
		}
		t.Setenv("NARRAFORK_FRONTEND_DIR", dir)
		router := NewRouter(Services{Auth: &auth.Service{}, Hub: ws.NewHub()})

		assetReq := httptest.NewRequest(http.MethodGet, "/asset.txt", nil)
		assetW := httptest.NewRecorder()
		router.ServeHTTP(assetW, assetReq)
		if assetW.Code != http.StatusOK || assetW.Body.String() != "asset" {
			t.Fatalf("expected static asset, got %d %q", assetW.Code, assetW.Body.String())
		}

		spaReq := httptest.NewRequest(http.MethodGet, "/projects/project-1", nil)
		spaW := httptest.NewRecorder()
		router.ServeHTTP(spaW, spaReq)
		if spaW.Code != http.StatusOK || !strings.Contains(spaW.Body.String(), "NarraFork") {
			t.Fatalf("expected SPA fallback, got %d %q", spaW.Code, spaW.Body.String())
		}
	})

	t.Run("api only fallback", func(t *testing.T) {
		t.Setenv("NARRAFORK_FRONTEND_DIR", filepath.Join(t.TempDir(), "missing"))
		router := NewRouter(Services{Auth: &auth.Service{}, Hub: ws.NewHub()})
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected API-only 404, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		decodeRouterJSON(t, w, &body)
		if body["code"] != "FRONTEND_NOT_BUNDLED" || body["mode"] != "api-only" {
			t.Fatalf("unexpected API-only fallback body: %#v", body)
		}
	})
}

func TestVNetRoutesExposeRelayCapability(t *testing.T) {
	router, authService := newRouterContractFixture(t)
	token, err := authService.CreateToken("user-1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/vnet/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected vnet status 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	decodeRouterJSON(t, w, &body)
	if body["supported"] != true || body["ws"] != true || body["mode"] != "relay" || body["fallback"] != false {
		t.Fatalf("unexpected vnet relay body: %#v", body)
	}
}

func TestNotificationSoundRoutesRequireAuth(t *testing.T) {
	router := NewRouter(Services{Auth: &auth.Service{}, Hub: ws.NewHub()})

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/notification-sounds/"},
		{method: http.MethodGet, path: "/api/notification-sounds/missing-sound"},
		{method: http.MethodDelete, path: "/api/notification-sounds/missing-sound"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s should require auth, got %d", tc.method, tc.path, w.Code)
			}
		})
	}
}

func decodeRouterJSON(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode JSON %q: %v", w.Body.String(), err)
	}
}

func writeChangelogFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write changelog %s: %v", name, err)
	}
}
