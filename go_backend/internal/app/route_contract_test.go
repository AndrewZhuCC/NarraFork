package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/graph"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/restart"
	"narrafork-go/internal/ws"
)

func TestMissingAPIRouteReturnsJSONFallbackWithoutAuthChallenge(t *testing.T) {
	router := NewRouter(Services{Auth: &auth.Service{}, Hub: ws.NewHub()})
	req := httptest.NewRequest(http.MethodGet, "/api/definitely-missing", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 fallback, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("expected JSON fallback, got content-type %q", w.Header().Get("Content-Type"))
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode fallback body: %v", err)
	}
	if body["code"] != "NOT_FOUND" || body["error"] != "API route not found" {
		t.Fatalf("unexpected fallback body: %#v", body)
	}
}

func TestAuthStatusReturnsErrorWhenUserCountFails(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	handler := authStatusHandler(sqlDB, loadRouterContractSettings(t))
	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected auth status DB failure to return 500, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"] != "Internal server error" {
		t.Fatalf("unexpected error body: %#v", body)
	}
}

func TestPublicAPIRouteBoundariesDoNotRequireAuth(t *testing.T) {
	router, _ := newRouterContractFixture(t)
	cases := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/api/health", "", http.StatusOK},
		{http.MethodGet, "/api/auth/status", "", http.StatusOK},
		{http.MethodGet, "/api/changelog", "", http.StatusOK},
		{http.MethodPost, "/api/gateway/webhook", `{"text":"hello"}`, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("expected %d without auth, got %d: %s", tc.status, w.Code, w.Body.String())
			}
		})
	}
}

func TestCriticalAPIRoutesRequireAuthAtBoundary(t *testing.T) {
	router, _ := newRouterContractFixture(t)
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/projects", ""},
		{http.MethodGet, "/api/chapters", ""},
		{http.MethodGet, "/api/narrators", ""},
		{http.MethodPatch, "/api/settings", `{}`},
		{http.MethodGet, "/api/update/check", ""},
		{http.MethodGet, "/api/runtime/cached", ""},
		{http.MethodGet, "/api/openai/models", ""},
		{http.MethodPost, "/api/dependencies/git/install", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 auth boundary, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestProjectCreateRouteAuthBoundaryReturnsStructuredUnauthorized(t *testing.T) {
	router, _ := newRouterContractFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"repoMode":"clone","gitPath":"/tmp/project","cloneUrl":"https://example.test/repo.git","name":"Project"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 auth boundary, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	if body["code"] != "UNAUTHORIZED" || body["message"] != "Authentication required" || body["error"] != "Authentication required" {
		t.Fatalf("unexpected unauthorized diagnostics: %#v", body)
	}
}

func TestAvatarUploadRouteAuthBoundaryReturnsStructuredUnauthorized(t *testing.T) {
	router, _ := newRouterContractFixture(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/auth/me/avatar", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 auth boundary, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	if body["code"] != "UNAUTHORIZED" || body["message"] != "Authentication required" || body["error"] != "Authentication required" {
		t.Fatalf("unexpected avatar unauthorized diagnostics: %#v", body)
	}
}

func TestUpdateDownloadRequiresAdminRoleAtRouteBoundary(t *testing.T) {
	router, authService := newRouterContractFixture(t)
	token, err := authService.CreateToken("user-1", "user")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin update download should be forbidden, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminOnlyRuntimeAndDependencyRoutesRejectMemberTokens(t *testing.T) {
	router, authService := newRouterContractFixture(t)
	token, err := authService.CreateToken("user-1", "user")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/runtime/cached", ""},
		{http.MethodGet, "/api/admin/users", ""},
		{http.MethodPost, "/api/dependencies/git/install", `{}`},
		{http.MethodPost, "/api/chapters/podman/install", `{}`},
		{http.MethodGet, "/api/storage/cached", ""},
		{http.MethodGet, "/api/storage/scan", ""},
		{http.MethodPost, "/api/storage/cleanup", `{"target":"shares"}`},
		{http.MethodPost, "/api/storage/database/preview", `{"target":"apiRequestDumps"}`},
		{http.MethodPost, "/api/storage/database/cleanup", `{"target":"apiRequestDumps"}`},
		{http.MethodPost, "/api/storage/database/vacuum", `{}`},
		{http.MethodGet, "/api/benchmarks/runs/run-1/results", ""},
		{http.MethodGet, "/api/benchmarks/runs/run-1/summary", ""},
		{http.MethodGet, "/api/benchmarks/compare?runIds=run-1,run-2", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 admin boundary, got %d: %s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body["code"] != "FORBIDDEN" || body["error"] != "Admin access required" {
				t.Fatalf("unexpected forbidden body: %#v", body)
			}
		})
	}
}

func TestH02RequiresGitMatchesTSGuardRules(t *testing.T) {
	guarded := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/projects"},
		{http.MethodGet, "/api/projects/project-1/ruler"},
		{http.MethodPost, "/api/chapters"},
		{http.MethodDelete, "/api/chapters/chapter-1"},
		{http.MethodPost, "/api/chapters/cleanup"},
		{http.MethodPost, "/api/chapters/batch-merge"},
		{http.MethodGet, "/api/chapters/chapter-1/merge-check"},
		{http.MethodPost, "/api/chapters/chapter-1/fork"},
		{http.MethodPost, "/api/chapters/chapter-1/batch-fork"},
		{http.MethodPost, "/api/chapters/chapter-1/split"},
		{http.MethodPost, "/api/chapters/chapter-1/cherry-pick"},
		{http.MethodGet, "/api/chapters/chapter-1/dependency-status"},
		{http.MethodPost, "/api/chapters/chapter-1/sync-upstream"},
		{http.MethodGet, "/api/chapters/chapter-1/git/status"},
		{http.MethodGet, "/api/chapters/chapter-1/commits/abc/files"},
	}
	for _, tc := range guarded {
		if !requiresGit(tc.method, tc.path) {
			t.Fatalf("expected %s %s to require git", tc.method, tc.path)
		}
	}

	notGuarded := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/projects"},
		{http.MethodGet, "/api/settings"},
		{http.MethodGet, "/api/openai/models"},
		{http.MethodGet, "/api/search"},
		{http.MethodGet, "/api/narrators"},
		{http.MethodGet, "/api/narrators/standalone-1"},
		{http.MethodGet, "/api/chapters/chapter-1/commits"},
		{http.MethodGet, "/api/dependencies"},
	}
	for _, tc := range notGuarded {
		if requiresGit(tc.method, tc.path) {
			t.Fatalf("expected %s %s to remain usable without git", tc.method, tc.path)
		}
	}
}

func TestH02GitMissingGuardReturnsTSShapeOnlyForGitRoutes(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	router, authService := newRouterContractFixture(t)
	token, err := authService.CreateToken("user-1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/chapters/chapter-1/git/status", ""},
		{http.MethodPost, "/api/chapters/chapter-1/batch-fork", `{"forks":[{"title":"Fork"}]}`},
		{http.MethodPost, "/api/chapters/chapter-1/split", `{"commitSha":"abc123","newFork":{"title":"Fork"}}`},
		{http.MethodPost, "/api/chapters/chapter-1/cherry-pick", `{"commitShas":["abc123"]}`},
		{http.MethodGet, "/api/chapters/chapter-1/dependency-status", ""},
		{http.MethodPost, "/api/chapters/chapter-1/sync-upstream", `{"edgeId":"edge-1"}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+token)
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected git-missing 503 for %s %s, got %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode git guard body: %v", err)
		}
		if body["code"] != "GIT_NOT_INSTALLED" || body["error"] != "Git is not installed. Please install git and retry this Git-dependent action." {
			t.Fatalf("unexpected git guard body: %#v", body)
		}
	}

	for _, path := range []string{"/api/settings", "/api/openai/models", "/api/search?q=anything", "/api/narrators?standalone=true"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code == http.StatusServiceUnavailable && strings.Contains(w.Body.String(), "GIT_NOT_INSTALLED") {
			t.Fatalf("%s should not be blocked by git-missing guard: %s", path, w.Body.String())
		}
	}
}

func TestPostgresHealthCapabilitiesExposeGoOwnedDatabaseMode(t *testing.T) {
	caps := runtimeCapabilities("linux", nil, db.EnginePostgres, false)
	assertNestedCapability(t, caps, []string{"database"}, map[string]any{
		"engine":           "postgres",
		"mainSchemaOwner":  "go-postgres",
		"goMainMigrations": true,
		"goEnsureColumns":  true,
		"mode":             "go-owned-postgres-schema",
		"searchMode":       "postgres-like",
	})
}

func TestWindowsTerminalRuntimeCapabilitiesOverrideFallbackReason(t *testing.T) {
	caps := runtimeCapabilities("windows", map[string]any{
		"supported":             true,
		"directPty":             true,
		"windowsPty":            true,
		"runtime":               "conpty",
		"dtachSupported":        false,
		"orphanRecovery":        false,
		"bufferStateMode":       "raw-capped-buffer",
		"xtermSerializedReplay": false,
	}, db.EngineSQLite, false)
	assertNestedCapability(t, caps, []string{"terminal"}, map[string]any{
		"supported":                   true,
		"directPty":                   true,
		"windowsPty":                  true,
		"runtime":                     "conpty",
		"bufferStateMode":             "raw-capped-buffer",
		"xtermSerializedReplay":       false,
		"xtermSerializedReplayReason": terminalXtermSerializedReplayReason,
	})
	terminal := caps["terminal"].(map[string]any)
	if reason, ok := terminal["reason"].(string); ok && strings.TrimSpace(reason) != "" {
		t.Fatalf("supported Windows terminal runtime should not keep unsupported reason: %#v", terminal)
	}
}

func TestPhase0GenericNotImplementedUsesFeatureDisabledFallbackShape(t *testing.T) {
	w := httptest.NewRecorder()
	httpx.WriteError(w, httpx.NotImplemented("Provider authentication"))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 feature disabled, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode fallback body: %v", err)
	}
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected fallback body: %#v", body)
	}
	if body["feature"] != "Provider authentication" {
		t.Fatalf("fallback feature should remain stable, got %#v", body)
	}
	reason, _ := body["reason"].(string)
	if !strings.Contains(reason, "Provider authentication is not implemented in go_backend yet") || body["error"] != reason || body["message"] != reason {
		t.Fatalf("fallback should expose reason/error/message, got %#v", body)
	}
}

func TestPhase0HealthCapabilitiesExposeKnownGoFallbacks(t *testing.T) {
	router, _ := newRouterContractFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected health 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	caps, ok := body["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected capabilities object, got %#v", body["capabilities"])
	}
	assertNestedCapability(t, caps, []string{"database"}, map[string]any{
		"mainSchemaOwner":  "typescript-drizzle",
		"goMainMigrations": false,
		"goEnsureColumns":  false,
		"ftsRepair":        false,
	})
	assertNestedCapability(t, caps, []string{"releasePackaging"}, map[string]any{"buildInfo": "ldflags-or-env", "frontend": "sidecar-directory-or-env", "changelog": "sidecar-directory-or-env", "singleFileEmbedded": false})
	assertNestedCapability(t, caps, []string{"nativeExtensions"}, map[string]any{"defaultEnabled": false, "scope": "go-owned-runtime-extensions"})
	assertNestedCapability(t, caps, []string{"nativeExtensions", "browserSessions"}, map[string]any{"defaultEnabled": false, "storage": "go-runtime-sidecar", "cutover": "explicit-enable", "rollback": "explicit-disable-or-export", "reason": browserSessionsDefaultOffReason})
	assertNestedCapability(t, caps, []string{"nativeExtensions", "containerBrowserToolAutoEnable"}, map[string]any{"defaultEnabled": false, "cutover": "explicit-enable", "rollback": "explicit-disable", "reason": containerBrowserToolAutoEnableReason})
	assertNestedCapability(t, caps, []string{"chapters", "split"}, map[string]any{"supported": true, "fallback": false, "mode": "go-basic-split"})
	assertNestedCapability(t, caps, []string{"chapters", "split", "partials", "compressedAISummary"}, map[string]any{"supported": false, "fallback": true, "mode": "deterministic-summary-placeholder", "reason": graph.CompressedAISummaryFallbackReason})
	assertNestedCapability(t, caps, []string{"chapters", "split", "partials", "containerAutostart"}, map[string]any{"supported": true, "fallback": false, "mode": "background-start"})
	assertNestedCapability(t, caps, []string{"chapters", "containers"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "runtime"}, map[string]any{"syncStartRequest": false, "backgroundStart": true, "backgroundStartReason": containerBackgroundStartReason, "streamingLogs": true, "perChapterLock": true})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "lifecycle"}, map[string]any{"manualControls": true, "autoStartOnFork": true, "pauseOnDormant": true, "unpauseOnWake": true, "deleteVolumes": true})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "proxy"}, map[string]any{"metadataSupported": true, "reverseProxyServer": true, "http": true, "websocket": true, "dynamicSettingsHook": true})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "narratorIntegration"}, map[string]any{"statusChangedEvent": true, "containerReadyMessage": true, "browserToolAutoEnable": false, "browserToolAutoEnableReason": containerBrowserToolAutoEnableReason, "defaultEnabled": false, "cutover": "explicit-enable", "rollback": "explicit-disable"})
	assertNestedCapability(t, caps, []string{"vnet"}, map[string]any{"supported": true, "ws": true, "mode": "relay", "peerCleanup": true, "udpRendezvous": false, "udpRendezvousReason": "Go VNet compatibility mode relays WebSocket peer traffic and does not expose UDP rendezvous."})
	assertNestedCapability(t, caps, []string{"update"}, map[string]any{"manualOnly": true, "selfUpdateAvailable": false, "canAutoRestart": false})
	assertNestedCapability(t, caps, []string{"update", "download"}, map[string]any{"supported": true, "sse": true, "sha512": true, "maxBytes": float64(512 * 1024 * 1024), "trustMode": "client-or-env-manifest-url"})
	assertNestedCapability(t, caps, []string{"update", "apply"}, map[string]any{"supported": false, "fallback": true, "code": "FEATURE_DISABLED", "reason": "Automatic self-update requires a configured restart handoff manager and a regular current executable."})
	assertNestedCapability(t, caps, []string{"gateway", "weixinQr"}, map[string]any{"supported": false, "fallback": true, "code": "WEIXIN_QR_UNSUPPORTED", "reason": "Weixin QR gateway runtime is not bundled in go_backend."})
	gateway, ok := caps["gateway"].(map[string]any)
	if !ok {
		t.Fatalf("expected gateway capability object, got %#v", caps["gateway"])
	}
	supportedPlatforms, ok := gateway["supportedPlatforms"].([]any)
	if !ok || len(supportedPlatforms) != 2 || supportedPlatforms[0] != "telegram" || supportedPlatforms[1] != "webhook" {
		t.Fatalf("unexpected supportedPlatforms: %#v", gateway["supportedPlatforms"])
	}
	unsupportedPlatforms, ok := gateway["unsupportedPlatforms"].(map[string]any)
	weixinReason, _ := unsupportedPlatforms["weixin"].(string)
	discordReason, _ := unsupportedPlatforms["discord"].(string)
	if !ok || strings.TrimSpace(weixinReason) == "" || strings.TrimSpace(discordReason) == "" {
		t.Fatalf("unexpected unsupportedPlatforms: %#v", gateway["unsupportedPlatforms"])
	}
	assertNestedCapability(t, caps, []string{"storage", "scan"}, map[string]any{"supported": true, "sse": true, "cache": true})
	assertNestedCapability(t, caps, []string{"storage", "cached"}, map[string]any{"supported": true, "cache": true})
	assertNestedCapability(t, caps, []string{"storage", "database"}, map[string]any{"preview": true, "cleanup": true})
	assertNestedCapability(t, caps, []string{"storage", "database", "cleanupTargets", "apiRequestDumps"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"storage", "database", "cleanupTargets", "archivedSessions"}, map[string]any{"supported": false, "fallback": true, "code": "FEATURE_DISABLED", "reason": disabledNarratorSessionCleanupReason})
	assertNestedCapability(t, caps, []string{"storage", "database", "cleanupTargets", "staleSessions"}, map[string]any{"supported": false, "fallback": true, "code": "FEATURE_DISABLED", "reason": disabledNarratorSessionCleanupReason})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "uploads"}, map[string]any{"supported": true, "preservesMessageImageRefs": true})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "worktrees"}, map[string]any{"supported": false, "fallback": true, "alternative": "runtime.cleanup.worktrees", "reason": "Storage cleanup does not remove git worktrees in go_backend; use chapter/runtime cleanup so git state and chapter metadata stay consistent."})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "containers"}, map[string]any{"supported": false, "fallback": true, "alternative": "runtime.cleanup.containers", "reason": "Podman prune is disabled from storage cleanup in go_backend; use per-chapter container controls or an external maintenance command."})
	assertNestedCapability(t, caps, []string{"runtime"}, map[string]any{"backend": "go", "buildChannel": "dev"})
	assertNestedCapability(t, caps, []string{"runtime", "scan"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "containers"}, map[string]any{"supported": true, "mode": "stop-running-db-records"})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "worktrees"}, map[string]any{"supported": true, "mode": "go-extension"})
	assertNestedCapability(t, caps, []string{"mcp", "serverSettingsStorage"}, map[string]any{"supported": true, "fallback": false, "storage": "settings.json"})
	assertNestedCapability(t, caps, []string{"mcp", "builtinProtocol"}, map[string]any{"supported": true, "initialize": true, "toolsList": true, "toolsCall": true})
	assertNestedCapability(t, caps, []string{"mcp", "builtinTools"}, map[string]any{"supported": true, "parity": "partial"})
	assertNestedCapability(t, caps, []string{"mcp", "externalServerManagement"}, map[string]any{"supported": true, "storage": "settings.json", "permissions": true, "import": true})
	assertNestedCapability(t, caps, []string{"mcp", "transports", "stdio"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"mcp", "transports", "sse"}, map[string]any{"supported": false, "fallback": true, "code": "FEATURE_DISABLED", "reason": mcpNonStdioTransportReason})
	assertNestedCapability(t, caps, []string{"mcp", "transports", "streamableHttp"}, map[string]any{"supported": false, "fallback": true, "code": "FEATURE_DISABLED", "reason": mcpNonStdioTransportReason})
	assertNestedCapability(t, caps, []string{"mcp", "toolsList"}, map[string]any{"supported": true, "fallback": false})
	assertNestedCapability(t, caps, []string{"mcp", "toolsCall"}, map[string]any{"supported": true, "fallback": false, "scope": "go-embedded-compatibility-tools-and-stdio-external-tools"})
	assertNestedCapability(t, caps, []string{"mcp", "externalToolsInjection"}, map[string]any{"supported": true, "fallback": false, "parity": "partial", "transport": "stdio", "lifecycle": "per-call"})
	assertNestedCapability(t, caps, []string{"mcp", "externalAgentInjection"}, map[string]any{"supported": true, "fallback": false, "parity": "partial"})
	assertNestedCapability(t, caps, []string{"settings", "validation"}, map[string]any{"tsZodParity": false, "mode": "loose-json-with-normalization", "reason": "Go backend normalizes settings before saving but does not apply the full TypeScript Zod schema strictly."})
	assertNestedCapability(t, caps, []string{"settings", "storage"}, map[string]any{"supported": true, "path": "~/.narrafork/settings.json"})
	assertNestedCapability(t, caps, []string{"providers", "codex", "managerParity"}, map[string]any{"tsCodexManagerEquivalent": false, "usageQueueParity": "partial", "usageQueueClearSupported": true, "snapshotPaginationParity": "supported", "reason": "Go exposes Codex compatibility routes/runtime but not the full TS CodexManager scheduler/usage queue semantics."})
	assertNestedCapability(t, caps, []string{"benchmark", "containerExecution"}, map[string]any{"supported": true, "fallback": false, "runtime": "podman", "resourceLimits": true, "timeout": true})
	assertNestedCapability(t, caps, []string{"narrator", "wsEvents", "p0"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"narrator", "messageHistory"}, map[string]any{"supported": true, "catchUp": true, "childOrphans": true, "toolCalls": true, "compactMarkers": true})
	assertNestedCapability(t, caps, []string{"narrator", "permissions"}, map[string]any{"supported": true, "approveDeny": true, "updatedInput": true, "pauseResume": "db-pending-tool-call"})
	assertNestedCapability(t, caps, []string{"narrator", "toolInventory", "mcpExternalTools"}, map[string]any{"supported": true, "parity": "partial", "transport": "stdio", "lifecycle": "per-call"})
	assertNestedCapability(t, caps, []string{"narrator", "toolInventory", "webFetch"}, map[string]any{"supported": true, "parity": "partial", "mode": "raw-only", "defaultMode": "raw", "policy": "permission-policy-and-proxy-consumed"})
	assertNestedCapability(t, caps, []string{"narrator", "toolInventory", "browser"}, map[string]any{"supported": true, "parity": "partial", "runtime": "go-rod", "screenshotPreviewMode": "inline-base64-or-file-artifact", "sharePreview": false, "imageContentBlock": false, "fileOutput": true, "traceFormat": "chrome-devtools-json", "traceShare": true, "traceFileOutput": true, "traceShareUrl": "/api/shares/{shareId}"})
	assertNestedCapability(t, caps, []string{"narrator", "planMode"}, map[string]any{"supported": true, "api": true, "toolReflection": true, "previousModeRestore": true})
	assertNestedCapability(t, caps, []string{"narrator", "compact"}, map[string]any{"supported": true, "segmentCompact": true, "contextClear": true, "fallbackSummary": true, "fallbackReason": "TS-equivalent model-generated compaction is not implemented in go_backend; summaries are caller-provided or deterministic from refs."})
	assertNestedCapability(t, caps, []string{"narrator", "retryRecovery"}, map[string]any{"supported": true, "retry": true, "continue": true, "interrupt": true, "manualOverride": true, "rollback": true, "editAndRegenerate": true})
	assertNestedCapability(t, caps, []string{"narrator", "subagents"}, map[string]any{"supported": true, "foreground": true, "background": true, "awaitAgent": true, "send": true, "teamStatus": true, "detachAttach": true, "detachUnblocksParent": true, "reattachBlocksParent": false, "reattachFallback": true, "reattachReason": "Go can detach and open subagent sessions, but cannot re-block an already-running parent tool promise after reattach.", "backgroundResultInjection": true, "staleRecovery": true})
	assertNestedCapability(t, caps, []string{"narrator", "reviewTools"}, map[string]any{"supported": true, "concludeReview": true, "feedbackInjection": true, "promote": true, "dismiss": true, "convertToSubagent": true, "staleMergeGuard": true})
	assertNestedCapability(t, caps, []string{"narrator", "browserSessions"}, map[string]any{"supported": true, "defaultEnabled": false, "fallback": false, "runtime": "go-rod", "storage": "go-owned-optional-table", "cutover": "explicit-enable", "rollback": "explicit-disable-or-export", "reason": browserSessionsDefaultOffReason, "narratorBound": true, "lifecycleEvents": true, "artifactPersistence": true, "resourceLimits": true, "requiresChrome": true})
	assertNestedCapability(t, caps, []string{"narrator", "containerBrowserToolAutoEnable"}, map[string]any{"defaultEnabled": false, "cutover": "explicit-enable", "rollback": "explicit-disable", "reason": containerBrowserToolAutoEnableReason})
	assertNestedCapability(t, caps, []string{"narrator", "rollbackEditRegenerate"}, map[string]any{"supported": true, "fallback": false, "rollback": true, "editAndRegenerate": true, "copyOnWrite": true, "messageRefTruncation": true, "fileStateRebuild": true, "toolCallInvalidation": true, "agentRerun": true, "optionalFileRevert": true, "optionalAgentRerun": true, "wsEvents": true})
	assertNestedCapability(t, caps, []string{"content", "projectRoutines"}, map[string]any{"supported": true, "fallback": false, "storage": "main-db-project-chapter-settings"})
	assertNestedCapability(t, caps, []string{"content", "projectSkills"}, map[string]any{"supported": true, "fallback": false, "storage": "project-git-path-scan"})
	assertNestedCapability(t, caps, []string{"fs", "browse"}, map[string]any{"supported": true, "fallback": false})
	assertNestedCapability(t, caps, []string{"fs", "preview"}, map[string]any{"supported": true, "maxTextBytes": float64(1 << 20), "maxBinaryBytes": float64(20 * 1024 * 1024)})
	assertNestedCapability(t, caps, []string{"fs", "reveal"}, map[string]any{"supported": true, "fallback": false})
	assertNestedCapability(t, caps, []string{"uploads", "serveNarratorImages"}, map[string]any{"supported": true, "fallback": false})
	assertNestedCapability(t, caps, []string{"uploads", "cleanupPreservesMessageImageRefs"}, map[string]any{"supported": true, "fallback": false})
	assertNestedCapability(t, caps, []string{"shares", "preview"}, map[string]any{"supported": true, "htmlMode": "escaped-pre", "reason": shareEscapedHTMLPreviewReason})
	assertNestedCapability(t, caps, []string{"shares", "ephemeralOnly"}, map[string]any{"supported": false, "fallback": true, "reason": shareEphemeralFallbackReason})
	assertNestedCapability(t, caps, []string{"terminal"}, map[string]any{"scrollbackReplayMode": "raw-capped-buffer", "bufferStateMode": "raw-capped-buffer", "xtermSerializedReplay": false, "xtermSerializedReplayReason": terminalXtermSerializedReplayReason, "windowsPty": false, "maxSnapshotBytes": float64(1 << 20), "multiClientResizeMode": "last-input-client"})
	assertNestedCapability(t, caps, []string{"terminal", "ws"}, map[string]any{"subscribe": true, "input": true, "resize": true, "kill": true, "rename": true, "scrollback": true, "bufferState": true})
	assertNestedCapability(t, caps, []string{"terminal", "processTree"}, map[string]any{"supported": true, "platform": "unix-ps"})
	assertNestedCapability(t, caps, []string{"narrator", "subagents"}, map[string]any{"awaitAgent": true, "awaitBash": false, "awaitBashWaitForText": false, "awaitBashReason": "Go Await currently supports agent/subagent background tasks only; Bash background tasks and wait_for_text are not implemented in go_backend."})
	assertNestedCapability(t, caps, []string{"providers", "openai", "quota"}, map[string]any{"supported": false, "fallback": true, "code": "FEATURE_DISABLED", "reason": "OpenAI-compatible quota inspection is not available in go_backend."})
	assertNestedCapability(t, caps, []string{"providers", "anthropic", "quota"}, map[string]any{"supported": false, "fallback": true, "code": "FEATURE_DISABLED", "reason": "Anthropic quota inspection is not available in go_backend."})
	assertNestedCapability(t, caps, []string{"providers", "openai", "agentRuntime"}, map[string]any{"supported": true, "chatCompletions": true, "responsesApi": true, "toolCalling": true, "streaming": true})
	assertNestedCapability(t, caps, []string{"providers", "openai", "routes"}, map[string]any{"modelsRefresh": true, "perProviderModels": true, "perProviderModelsRefresh": true})
	assertNestedCapability(t, caps, []string{"providers", "anthropic", "routes"}, map[string]any{"modelsRefresh": true, "perProviderModels": true, "perProviderModelsRefresh": true})
	assertNestedCapability(t, caps, []string{"providers", "kiro", "pluginBridge"}, map[string]any{"supported": true, "requiresBun": true, "reason": "Kiro plugin bridge is available only when the external Bun bridge/plugin environment is present."})
	assertNestedCapability(t, caps, []string{"providers", "kiro", "mcp"}, map[string]any{"listToolsRoute": true, "searchRoute": true, "agentInjection": false})
	assertNestedCapability(t, caps, []string{"providers", "kiro", "routes"}, map[string]any{
		"modelsRefresh":     true,
		"mcpSearch":         true,
		"globalProxy":       true,
		"loadBalancingMode": true,
		"credentialsImport": true,
		"credentialUsage":   true,
		"credentialUpdate":  true,
		"credentialDelete":  true,
	})
	assertNestedCapability(t, caps, []string{"providers", "nkp", "routes"}, map[string]any{
		"modelsRefresh":            true,
		"perProviderModels":        true,
		"perProviderModelsRefresh": true,
		"quota":                    true,
		"quotas":                   true,
	})
	assertNestedCapability(t, caps, []string{"providers", "nug", "routes"}, map[string]any{
		"modelsRefresh":            true,
		"perProviderModels":        true,
		"perProviderModelsRefresh": true,
		"login":                    true,
		"apiKey":                   true,
		"oauthStart":               true,
		"oauthCallback":            true,
		"channelsHealth":           true,
		"usageSummary":             true,
		"quotas":                   true,
	})
	assertNestedCapability(t, caps, []string{"providers", "cline", "routes"}, map[string]any{
		"modelsRefresh":            true,
		"perProviderModels":        true,
		"perProviderModelsRefresh": true,
		"recommendedModels":        true,
		"poolSearch":               true,
		"poolCount":                true,
		"enabledModels":            true,
		"userInfoRefresh":          true,
		"authBrowser":              true,
		"authCancel":               true,
		"authCallback":             true,
	})
	assertNestedCapability(t, caps, []string{"providers", "codex", "auth"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"providers", "codex", "routes"}, map[string]any{
		"defaultReasoningEffort": true,
		"useWebSocket":           true,
		"loadBalancingMode":      true,
		"tierOrder":              true,
		"globalProxy":            true,
		"usageQueueClear":        true,
		"browserAuthWait":        true,
		"browserAuthCancel":      true,
		"deviceAuthStart":        true,
		"deviceAuthPoll":         true,
		"deviceAuthCancel":       true,
		"credentialBatchDelete":  true,
	})
	assertNestedCapability(t, caps, []string{"providers", "codex", "managerParity"}, map[string]any{"tsCodexManagerEquivalent": false, "usageQueueParity": "partial", "snapshotPaginationParity": "supported", "reason": "Go exposes Codex compatibility routes/runtime but not the full TS CodexManager scheduler/usage queue semantics."})
}

func TestH01AdminOnlyRoutesAllowAdminBoundary(t *testing.T) {
	router, authService := newRouterContractFixture(t)
	token, err := authService.CreateToken("user-1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	cases := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/api/storage/cached", "", http.StatusOK},
		{http.MethodGet, "/api/benchmarks/compare", "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("expected admin request to reach handler with %d, got %d: %s", tc.status, w.Code, w.Body.String())
			}
		})
	}
}

func assertNestedCapability(t *testing.T, root map[string]any, path []string, expected map[string]any) {
	t.Helper()
	current := root
	for _, part := range path {
		next, ok := current[part].(map[string]any)
		if !ok {
			t.Fatalf("expected capability path %v to contain object at %q, got %#v", path, part, current[part])
		}
		current = next
	}
	for key, want := range expected {
		if got := current[key]; got != want {
			t.Fatalf("expected capability %v.%s=%#v, got %#v in %#v", path, key, want, got, current)
		}
	}
}

func openRouterContractDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = sqlDB.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY,
		username TEXT,
		password_hash TEXT,
		role TEXT,
		avatar_color TEXT,
		avatar_image_id TEXT,
		git_username TEXT,
		git_email TEXT,
		created_at TEXT
	)`)
	if err != nil {
		t.Fatalf("create users: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('user-1', 'member', 'hash', 'user', '2026-05-14T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return sqlDB
}

func TestHealthReportsAutoRestartWhenRestartManagerConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	router := NewRouter(Services{
		Auth:    &auth.Service{},
		Git:     gitx.NewService(),
		Hub:     ws.NewHub(),
		Restart: restart.NewManager(restart.RuntimeAddress{Protocol: "http", Host: "127.0.0.1", Port: 7778}, nil),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected health 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	caps, ok := body["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected capabilities object, got %#v", body["capabilities"])
	}
	assertNestedCapability(t, caps, []string{"update"}, map[string]any{"manualOnly": false, "selfUpdateAvailable": true, "canAutoRestart": true})
}

func newRouterContractFixture(t *testing.T) (http.Handler, *auth.Service) {
	t.Helper()
	sqlDB := openRouterContractDB(t)
	t.Cleanup(func() { _ = sqlDB.Close() })
	settings := loadRouterContractSettings(t)
	authService := auth.NewService(sqlDB, settings)
	router := NewRouter(Services{
		DB:       sqlDB,
		Auth:     authService,
		Settings: settings,
		Git:      gitx.NewService(),
		Hub:      ws.NewHub(),
	})
	return router, authService
}

func loadRouterContractSettings(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return settings
}
