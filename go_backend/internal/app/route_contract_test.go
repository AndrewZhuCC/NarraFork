package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
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
		{http.MethodGet, "/api/hooks", ""},
		{http.MethodPost, "/api/benchmarks/suites", `{"name":"Suite","tasks":[{"id":"task-1","name":"Task","prompt":"Prompt"}]}`},
		{http.MethodGet, "/api/benchmarks/suites", ""},
		{http.MethodGet, "/api/benchmarks/suites/suite-1", ""},
		{http.MethodPost, "/api/benchmarks/runs", `{"suiteId":"suite-1","name":"Run","model":"gpt-test"}`},
		{http.MethodGet, "/api/benchmarks/runs", ""},
		{http.MethodGet, "/api/benchmarks/runs/run-1", ""},
		{http.MethodPost, "/api/benchmarks/runs/run-1/start", ""},
		{http.MethodPost, "/api/benchmarks/runs/run-1/cancel", ""},
		{http.MethodPost, "/api/benchmarks/runs/run-1/retry", ""},
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

func TestHooksAndWorkspacesMatchTSBoundaryAndMissingStates(t *testing.T) {
	router, authService, sqlDB := newRouterContractFixtureWithDB(t)
	mustExecRouterContractSQL(t, sqlDB, `CREATE TABLE hooks (
		id TEXT PRIMARY KEY,
		project_id TEXT,
		event TEXT NOT NULL,
		matcher TEXT NOT NULL DEFAULT '',
		type TEXT NOT NULL,
		command TEXT,
		url TEXT,
		headers TEXT,
		prompt TEXT,
		model TEXT,
		timeout INTEGER NOT NULL DEFAULT 30,
		enabled INTEGER NOT NULL DEFAULT 1,
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	mustExecRouterContractSQL(t, sqlDB, `CREATE TABLE workspaces (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		title TEXT NOT NULL,
		tree TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`)
	adminToken, err := authService.CreateToken("user-1", "admin")
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	userToken, err := authService.CreateToken("user-1", "user")
	if err != nil {
		t.Fatalf("create user token: %v", err)
	}
	cases := []struct {
		method string
		path   string
		body   string
		token  string
		status int
	}{
		{http.MethodGet, "/api/hooks", "", adminToken, http.StatusOK},
		{http.MethodGet, "/api/hooks", "", userToken, http.StatusForbidden},
		{http.MethodGet, "/api/hooks/missing", "", adminToken, http.StatusNotFound},
		{http.MethodPost, "/api/hooks", `{}`, adminToken, http.StatusBadRequest},
		{http.MethodDelete, "/api/hooks/missing", `{}`, adminToken, http.StatusNotFound},
		{http.MethodGet, "/api/workspaces/missing", "", adminToken, http.StatusNotFound},
		{http.MethodPost, "/api/workspaces", `{"tree":"x"}`, adminToken, http.StatusBadRequest},
		{http.MethodPatch, "/api/workspaces/missing", `{"title":"Missing"}`, adminToken, http.StatusNotFound},
		{http.MethodDelete, "/api/workspaces/missing", `{}`, adminToken, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+tc.token)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("expected %d, got %d: %s", tc.status, w.Code, w.Body.String())
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
	caps := runtimeCapabilities("linux", nil, db.EnginePostgres, false, false, false)
	assertNestedCapability(t, caps, []string{"database"}, map[string]any{
		"engine":                      "postgres",
		"mainSchemaOwner":             "go-postgres",
		"goMainMigrations":            true,
		"goEnsureColumns":             true,
		"mode":                        "go-owned-postgres-schema",
		"targetMode":                  "pure-go-db-management",
		"tsParityBaseline":            false,
		"sqliteReplacementBaseline":   false,
		"drizzleMigrationParity":      false,
		"schemaContractSource":        "go-postgres-canonical-mapping",
		"crossEngineAPICompatibility": "unverified",
		"crossEngineAPISmoke":         false,
		"crossEngineGoldenParity":     false,
		"searchMode":                  "postgres-like",
	})
	assertNestedCapability(t, caps, []string{"search"}, map[string]any{
		"supported":                  true,
		"mode":                       "postgres-like",
		"ftsRequiresTypeScriptSetup": false,
		"ftsReadinessPerRequest":     false,
		"ftsRepair":                  false,
		"ftsRebuild":                 false,
		"ftsCleanShutdownMarker":     false,
		"ftsOldTokenizerRepair":      false,
		"likeFallback":               true,
		"degradedFallbackMetadata":   false,
		"shortQueryLikeMode":         false,
		"fullTSFTSLifecycleParity":   false,
		"crossEngineGoldenParity":    false,
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
	}, db.EngineSQLite, false, false, false)
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
		"mainSchemaOwner":                    "typescript-drizzle",
		"targetMainSchemaOwner":              "go",
		"goMainMigrations":                   false,
		"goEnsureColumns":                    false,
		"ftsRepair":                          false,
		"mode":                               "ts-owned-db-compatibility",
		"strategyStage":                      "transition",
		"transitionalStrategy":               true,
		"targetMode":                         "pure-go-db-management",
		"currentRepairOwner":                 "typescript-drizzle-and-ts-fts-setup",
		"sqliteDefaultBootstrapMode":         "read-only-ts-owned",
		"sqliteFreshInstallDefaultSupported": false,
		"requiresExistingTypeScriptDatabase": true,
		"businessSchemaCreationDefault":      false,
		"businessSchemaMigrationDefault":     false,
		"startupBackfillRepair":              false,
		"mainDBRepair":                       false,
		"defaultFailureMode":                 "startup-readiness-error",
		"bunFinalCompatibleBootstrapMode":    true,
		"bunFinalCompatibleBootstrapDefault": false,
		"bunFinalCompatibleBootstrapScope":   "explicit-legacy-test-helper",
		"rollbackToTypeScriptSafe":           true,
		"canonicalSchemaHash":                db.CanonicalSchemaContract().CanonicalSchemaHash,
		"canonicalColumnMetadataCount":       float64(db.CanonicalSchemaContract().CanonicalColumnMetadataCount),
		"canonicalTableCount":                float64(db.CanonicalSchemaContract().CanonicalTableCount),
		"canonicalIndexCount":                float64(db.CanonicalSchemaContract().CanonicalIndexCount),
		"canonicalForeignKeyCount":           float64(db.CanonicalSchemaContract().CanonicalForeignKeyCount),
		"canonicalFTSTableCount":             float64(db.CanonicalSchemaContract().CanonicalFTSTableCount),
		"canonicalFTSTriggerCount":           float64(db.CanonicalSchemaContract().CanonicalFTSTriggerCount),
		"expectedMigrationHash":              db.CanonicalSchemaContract().ExpectedMigrationHash,
		"expectedMigrationCount":             float64(db.CanonicalSchemaContract().ExpectedMigrationCount),
		"expectedLastMigration":              db.CanonicalSchemaContract().ExpectedLastMigration,
		"drizzleJournalTable":                db.CanonicalSchemaContract().DrizzleJournalTable,
		"goJournalTable":                     db.CanonicalSchemaContract().GoJournalTable,
		"schemaContractDepth":                "representative-critical-subset",
		"fullColumnMetadataParity":           false,
		"fullForeignKeyActionParity":         false,
		"fullDrizzleRelationParity":          false,
		"readOnlySchemaVerifier":             true,
		"migrationFreshnessStrictMode":       true,
		"migrationStrictEntrypoint":          "EnsureMigrationCompatibilityStrict",
		"migrationStrictCIWired":             false,
		"defaultAllowsExtraDrizzleHashes":    true,
	})
	assertNestedCapability(t, caps, []string{"database", "routeSchemaReadiness"}, map[string]any{
		"supported":                    true,
		"source":                       "VerifyCanonicalSchema",
		"startupGuard":                 true,
		"readOnly":                     true,
		"requiredTablesTracked":        true,
		"requiredColumnsTracked":       true,
		"highValueIndexesTracked":      true,
		"coreForeignKeysTracked":       true,
		"ftsObjectsTracked":            true,
		"representativeColumnMetadata": true,
		"fullRouteColumnParity":        false,
		"fullRouteSQLStaticAnalysis":   false,
		"missingSchemaFailureMode":     "startup-readiness-error",
	})
	assertStringSliceCapability(t, caps, []string{"database", "routeSchemaReadiness"}, "directHighRiskTables", []string{
		"api_requests",
		"background_tasks",
		"gateway_session_mappings",
		"hooks",
		"narrator_buffered_messages",
		"volume_snapshots",
		"workspaces",
	})
	assertStringSliceCapability(t, caps, []string{"database", "routeSchemaReadiness"}, "optionalGuardedTablesExcluded", []string{
		"custom_subagents",
		"mcp_servers",
		"narrator_browser_sessions",
	})
	assertNestedCapability(t, caps, []string{"database", "routeSQLStaticAnalysisContract"}, map[string]any{
		"supported":                            true,
		"source":                               "manual high-risk table readiness list",
		"generatedRouteSQLSnapshot":            false,
		"comparedAgainstDrizzleSnapshot":       false,
		"enabledRouteCoverage":                 "high-risk-table-list-only",
		"startupGuardStopsKnownHighRiskTables": true,
		"fullRouteColumnParity":                false,
		"fullRouteIndexParity":                 false,
		"fullRouteForeignKeyParity":            false,
		"fullRouteSQLStaticAnalysis":           false,
		"adHocSQLFailurePossible":              true,
		"acceptance":                           "generate route-to-schema usage snapshot for every enabled Go route and compare table/column/index/FK/default/check/enum dependencies against the TS Drizzle schema snapshot",
	})
	assertStringSliceCapability(t, caps, []string{"database", "routeSQLStaticAnalysisContract"}, "coveredHighRiskTables", []string{
		"api_requests",
		"background_tasks",
		"gateway_session_mappings",
		"hooks",
		"narrator_buffered_messages",
		"volume_snapshots",
		"workspaces",
	})
	assertStringSliceCapability(t, caps, []string{"database", "routeSQLStaticAnalysisContract"}, "unclaimedDependencyClasses", []string{
		"every-enabled-route-table-dependency",
		"every-enabled-route-column-dependency",
		"per-route-index-dependency",
		"column-default-check-enum-semantics",
		"foreign-key-actions",
		"drizzle-relations",
	})
	assertNestedCapability(t, caps, []string{"database", "enumWriteContract"}, map[string]any{
		"supported":                      true,
		"source":                         "TS Drizzle enum-like values plus Go direct SQL audit",
		"sqliteCheckConstraints":         false,
		"dbLevelValidation":              false,
		"goCentralEnumPackage":           false,
		"representativeWriteAudit":       true,
		"exhaustiveProductionWriteAudit": false,
		"routeBoundaryValidation":        "partial-focused",
		"testOnlyDriftExcluded":          true,
		"fullTSEnumParity":               false,
		"remainingGap":                   "centralize Go enum constants/validators and prove all production direct SQL writes stay within TS enum sets",
	})
	assertStringSliceCapability(t, caps, []string{"database", "enumWriteContract"}, "trackedFamilies", []string{
		"projects.status",
		"chapters.status",
		"chapters.role",
		"chapter_edges.type",
		"narrators.status",
		"narrators.type",
		"narrator_tool_calls.status",
		"terminal_sessions.status",
		"hooks.type",
		"background_tasks.status",
	})
	assertStringSliceCapability(t, caps, []string{"database", "enumWriteContract"}, "representativeWriteTables", []string{
		"chapter_edges",
		"chapters",
		"narrators",
		"narrator_tool_calls",
		"terminal_sessions",
		"hooks",
		"background_tasks",
	})
	assertStringSliceCapability(t, caps, []string{"database", "enumWriteContract"}, "testOnlyDriftExamples", []string{
		"permission_mode=trust-all",
		"narrator_tool_calls.status=approved",
	})
	assertNestedCapability(t, caps, []string{"database", "migrationCompatibilityContract"}, map[string]any{
		"supported":                          true,
		"startupGuard":                       true,
		"defaultEntrypoint":                  "EnsureMigrationCompatibility",
		"strictEntrypoint":                   "EnsureMigrationCompatibilityStrict",
		"drizzleJournalTable":                db.CanonicalSchemaContract().DrizzleJournalTable,
		"goJournalTable":                     db.CanonicalSchemaContract().GoJournalTable,
		"goJournalLocation":                  "runtime-sidecar-for-store-open; main-db-only-for-legacy-helper",
		"goJournalContractVersion":           float64(1),
		"expectedMigrationHash":              db.CanonicalSchemaContract().ExpectedMigrationHash,
		"expectedMigrationCount":             float64(db.CanonicalSchemaContract().ExpectedMigrationCount),
		"expectedLastMigration":              db.CanonicalSchemaContract().ExpectedLastMigration,
		"writesBusinessSchema":               false,
		"writesDrizzleJournal":               false,
		"recordsGoCompatibilityJournal":      true,
		"recordsForwardExtraHashesDefault":   true,
		"defaultAllowsExtraDrizzleHashes":    true,
		"strictRejectsExtraDrizzleHashes":    true,
		"strictCIWired":                      false,
		"fullDrizzleMigrationRunnerParity":   false,
		"rollbackSafeBeforeJournalOnFailure": true,
	})
	assertStringSliceCapability(t, caps, []string{"database", "migrationCompatibilityContract"}, "acceptedJournalFamilies", []string{"bun-final-history", "current-repository-baseline"})
	assertStringSliceCapability(t, caps, []string{"database", "migrationCompatibilityContract"}, "startupOrder", []string{"open-sqlite-file", "configure-pragmas", "open-runtime-sidecar", "VerifyCanonicalSchema", "EnsureMigrationCompatibility"})
	assertNestedCapability(t, caps, []string{"database", "migrationStrictReleaseContract"}, map[string]any{
		"supported":                            true,
		"strictFunctionImplemented":            true,
		"strictEntrypoint":                     "EnsureMigrationCompatibilityStrict",
		"releaseCIWired":                       false,
		"buildScriptWired":                     false,
		"releaseScriptWired":                   false,
		"packageDefaultUsesStrictMode":         false,
		"currentRepoSnapshotEmbedded":          true,
		"expectedMigrationHashEmbedded":        true,
		"expectedMigrationCountEmbedded":       true,
		"expectedLastMigrationEmbedded":        true,
		"canonicalSchemaHashEmbedded":          true,
		"rejectsExtraHashesBeforeGoJournal":    true,
		"defaultTransitionAllowsForwardHashes": true,
		"defaultCutoverRequiresStrictPass":     true,
		"fallbackMode":                         "strict-gate-required-before-default-cutover",
		"manualCICommand":                      "go test ./internal/db -run TestEnsureMigrationCompatibilityStrictRejectsExtraUnknownHashesWithoutWritingGoJournal",
	})
	assertNestedCapability(t, caps, []string{"database", "migrationFailureContract", "missingDatabase"}, map[string]any{

		"mode":       "fail-fast-before-open",
		"goMutation": false,
		"userAction": "start TypeScript backend once to create and migrate the DB",
	})
	assertNestedCapability(t, caps, []string{"database", "migrationFailureContract", "missingDrizzleJournal"}, map[string]any{"mode": "startup-readiness-error", "errorClass": "ErrMigrationCompatibilityMismatch", "userAction": "run bun run db:migrate or start TypeScript backend once"})
	assertNestedCapability(t, caps, []string{"database", "migrationFailureContract", "missingExpectedDrizzleHash"}, map[string]any{"mode": "startup-readiness-error", "errorClass": "ErrMigrationCompatibilityMismatch", "userAction": "start TypeScript backend once so missing migrations are applied"})
	assertNestedCapability(t, caps, []string{"database", "migrationFailureContract", "extraUnknownDrizzleHashDefault"}, map[string]any{"mode": "allow-after-canonical-schema-pass", "recordedIn": db.CanonicalSchemaContract().GoJournalTable, "businessSchema": "unchanged"})
	assertNestedCapability(t, caps, []string{"database", "migrationFailureContract", "extraUnknownDrizzleHashStrict"}, map[string]any{"mode": "startup-readiness-error", "errorClass": "ErrMigrationCompatibilityMismatch", "userAction": "refresh the embedded Go migration snapshot or run compatibility mode intentionally"})
	assertNestedCapability(t, caps, []string{"database", "migrationFailureContract", "canonicalSchemaMismatch"}, map[string]any{"mode": "startup-readiness-error", "errorClass": "ErrCanonicalSchemaMismatch", "userAction": "start TypeScript backend once to run migrations/backfills/FTS setup"})
	assertNestedCapability(t, caps, []string{"database", "migrationFailureContract", "ftsObjectMismatch"}, map[string]any{"mode": "startup-readiness-error", "errorClass": "ErrCanonicalSchemaMismatch", "userAction": "start TypeScript backend once so ensureFts can validate or rebuild objects"})
	assertNestedCapability(t, caps, []string{"database", "repairContract"}, map[string]any{
		"repairOwner":                      "typescript-drizzle-and-ts-fts-setup",
		"goMainSchemaRepair":               false,
		"goEnsureColumns":                  false,
		"goStartupBackfills":               false,
		"goFTSRepair":                      false,
		"goOldDataRepair":                  false,
		"goActionAfterRepair":              "restart go_backend after TypeScript repair completes",
		"remainingFailureAfterRepairMeans": "real schema drift or unsupported future migration",
	})
	assertStringSliceCapability(t, caps, []string{"database", "repairContract"}, "typeScriptRepairSteps", []string{"runMigrations", "ensureColumns", "narrator status/trait backfills", "ensureFts"})
	assertNestedCapability(t, caps, []string{"database", "ftsReadinessContract"}, map[string]any{
		"startupRequiresFTSObjects":        true,
		"source":                           "VerifyCanonicalSchema",
		"requiredFTSTriggersTracked":       true,
		"requiredTokenizer":                "trigram",
		"goCreatesFTSObjects":              false,
		"goRebuildsFTSObjects":             false,
		"goDetectsOldTokenizerForRepair":   false,
		"missingOrIncompatibleFailureMode": "startup-readiness-error",
		"searchLikeFallbackScope":          "request-time fallback only after startup bypass or runtime drift",
		"fullTSFTSLifecycleParity":         false,
	})
	assertStringSliceCapability(t, caps, []string{"database", "ftsReadinessContract"}, "requiredFTSTables", []string{"chapters_fts", "narrator_messages_fts", "narrators_fts"})
	assertNestedCapability(t, caps, []string{"database", "oldDataBackfillContract"}, map[string]any{
		"repairOwner":                       "typescript-ensureColumns-and-startup-backfills",
		"goRowDataBackfill":                 false,
		"goLegacyNarratorStatusRepair":      false,
		"goMalformedTraitJSONRepair":        false,
		"goCleanShutdownFTSRebuildRecovery": false,
		"startupDetectionScope":             "schema-readiness-only; legacy row-shape drift remains TS-owned unless it breaks canonical readiness",
		"fullTSBackfillParity":              false,
	})
	assertNestedCapability(t, caps, []string{"database", "compatibilityBetaProductGate"}, map[string]any{
		"decisionTracked":                         true,
		"acceptedAsCompatibilityBetaDecision":     true,
		"mode":                                    "ts-migrated-db-only",
		"standaloneReplacement":                   false,
		"requiresExistingTypeScriptDatabase":      true,
		"requiresTypeScriptRepairPath":            true,
		"uiReleaseGateRequired":                   true,
		"defaultCutoverBlocked":                   true,
		"rollbackToTypeScriptSafe":                true,
		"searchDBFTS01Implication":                "replacement-acceptance-deferred-to-db-replacement-mode",
		"standaloneReplacementRequiredCapability": "database.replacementContract.fullStandaloneGoReplacementParity",
	})
	assertNestedCapability(t, caps, []string{"database", "replacementContract"}, map[string]any{
		"boundaryTracked":                    true,
		"runtimeRow":                         "DB-BOOTSTRAP-01",
		"mode":                               "ts-owned-sqlite-compatibility",
		"transitionalCompatibility":          true,
		"standaloneGoFreshInstallSupported":  false,
		"requiresExistingTypeScriptDatabase": true,
		"goBusinessSchemaCreation":           false,
		"goBusinessSchemaMigration":          false,
		"goFTSSetupRepair":                   false,
		"goStartupBackfillRepair":            false,
		"strictMigrationReleaseCIWired":      false,
		"bunFinalBootstrapHelperDefault":     false,
		"typeScriptRepairOwner":              true,
		"rollbackToTypeScriptSafe":           true,
		"compatibilityBetaProductDecision":   true,
		"compatibilityBetaMode":              "ts-migrated-db-only",
		"uiReleaseGateRequiresExistingTSDB":  true,
		"fullStandaloneGoReplacementParity":  false,
		"replacementSafe":                    false,
	})
	assertStringSliceCapability(t, caps, []string{"database", "replacementContract"}, "sampledProofs", []string{
		"canonical-schema-startup-guard",
		"migration-compatibility-default-and-strict-contract",
		"failure-mode-user-action-contract",
		"typescript-repair-owner-contract",
		"fts-readiness-startup-contract",
		"legacy-helper-non-default-scope",
		"compatibility-beta-product-gate",
	})
	assertStringSliceCapability(t, caps, []string{"database", "replacementContract"}, "requiredProofs", []string{
		"go-main-schema-migration-runner",
		"fresh-install-business-schema-and-fts-smoke",
		"strict-migration-release-ci-wired",
		"old-data-backfill-repair-parity",
		"fts-clean-shutdown-rebuild-repair-parity",
		"ts-go-ts-shared-db-rollback-drill",
	})
	assertNestedCapability(t, caps, []string{"database", "optionalStores", "customSubagents"}, map[string]any{
		"supported":            true,
		"canonicalDBTable":     false,
		"storage":              "filesystem-home-subagents",
		"fileName":             "SUBAGENT.md",
		"emptyWhenMissingRoot": true,
		"legacyTableName":      "custom_subagents",
		"legacyTableUsed":      false,
	})
	assertNestedCapability(t, caps, []string{"database", "optionalStores", "mcpServers"}, map[string]any{
		"supported":               true,
		"canonicalDBTable":        false,
		"storage":                 "settings.json",
		"legacyTableName":         "mcp_servers",
		"legacyTableUsed":         false,
		"missingSettingsFallback": "FEATURE_DISABLED",
	})
	assertNestedCapability(t, caps, []string{"database", "optionalStores", "browserSessions"}, map[string]any{
		"supported":              true,
		"canonicalDBTable":       false,
		"storage":                "go-owned-optional-table",
		"tableName":              "narrator_browser_sessions",
		"defaultEnabled":         false,
		"createTableWhenEnabled": true,
		"readinessScope":         "native-extension-runtime-db",
	})
	assertNestedCapability(t, caps, []string{"search"}, map[string]any{
		"supported":                  true,
		"mode":                       "sqlite-fts5-with-like-fallback",
		"ftsRequiresTypeScriptSetup": true,
		"ftsReadinessPerRequest":     true,
		"ftsRepair":                  false,
		"ftsRebuild":                 false,
		"ftsCleanShutdownMarker":     false,
		"ftsOldTokenizerRepair":      false,
		"likeFallback":               true,
		"degradedFallbackMetadata":   true,
		"shortQueryLikeMode":         true,
		"fullTSFTSLifecycleParity":   false,
	})
	assertNestedCapability(t, caps, []string{"search", "queryContract"}, map[string]any{"route": "GET /api/search", "queryParam": "q", "entitiesParam": "entities", "limitParam": "limit", "defaultLimit": float64(50), "maxLimit": float64(100), "invalidEntitiesEmpty": true, "emptyQueryReturnsEmpty": true, "sanitizeSpecialChars": true, "shortQueryRuneThreshold": float64(3)})
	assertStringSliceCapability(t, caps, []string{"search", "queryContract"}, "defaultEntities", []string{"chapters", "messages", "narrators"})
	assertStringSliceCapability(t, caps, []string{"search", "queryContract"}, "allowedEntities", []string{"chapters", "messages", "narrators"})
	assertStringSliceCapability(t, caps, []string{"search", "responseContract"}, "topLevelFields", []string{"results", "projects", "chapters", "narrators", "degraded", "fallbacks", "searchMetadata"})
	assertStringSliceCapability(t, caps, []string{"search", "responseContract"}, "resultTypes", []string{"chapter", "message", "narrator"})
	assertStringSliceCapability(t, caps, []string{"search", "responseContract"}, "commonFields", []string{"type", "id", "snippet", "matchField", "matchScore"})
	assertStringSliceCapability(t, caps, []string{"search", "metadataContract"}, "fields", []string{"degraded", "fallbacks", "mode", "ftsReady", "shortQuery", "requestedEntities"})
	assertNestedCapability(t, caps, []string{"search", "metadataContract"}, map[string]any{"requestedEntitiesSorted": true, "shortQueryNotDegraded": true})
	assertStringSliceCapability(t, caps, []string{"search", "fallbackContract"}, "fields", []string{"entity", "from", "to", "reason", "error", "fallbackError"})
	assertNestedCapability(t, caps, []string{"search", "fallbackContract"}, map[string]any{"ftsUnavailableReason": "fts_unavailable", "fromFTS5ToLike": true, "reasonErrorMessage": true, "topLevelFallbackMirror": true})
	assertNestedCapability(t, caps, []string{"search", "rankingContract"}, map[string]any{"ftsRankToPositiveScore": true, "rawFTSRankTieBreakWithinType": true, "stableSortByScoreThenTimestamp": false, "stableSortByScoreRankThenTimestamp": true, "limitCapsResults": true})
	assertNestedCapability(t, caps, []string{"search", "smokeContract"}, map[string]any{
		"tsGoContractHarness":               "go_backend/testtools/ts-go-api-contract-diff.ts",
		"baselineFixtureCoverage":           "minimal-ts-created-cjk-short-query-large-relevance",
		"sampledBaselineTSGoDiffClean":      true,
		"concreteResultFieldsSampled":       true,
		"cjkTrigramSubstringSampled":        true,
		"shortQueryLikeSampled":             true,
		"largerRealisticRelevanceSmoke":     true,
		"degradedLargeDataMetadataSmoke":    true,
		"missingCorruptFTSFixtureSmoke":     true,
		"additiveMetadataFrontendTolerance": "frontend-api-and-runtime-status-smoke",
		"fullDBFTS01Parity":                 false,
	})
	assertStringSliceCapability(t, caps, []string{"search", "smokeContract"}, "sampledEntities", []string{"chapters", "messages", "narrators"})
	assertStringSliceCapability(t, caps, []string{"search", "smokeContract"}, "sampledQueries", []string{"叙事分叉", "Al", "auroraanchor", "chrononeedle"})
	assertNestedCapability(t, caps, []string{"search", "ftsLifecycleProductGate"}, map[string]any{
		"decisionTracked":                true,
		"acceptedAsLifecycleDecision":    true,
		"sqliteCompatibilityOwner":       "typescript-db-fts-startup",
		"goRuntimeAction":                "read-existing-fts-or-like-fallback",
		"goCreatesFTS":                   false,
		"goRepairsFTS":                   false,
		"goRebuildsFTS":                  false,
		"goCleanShutdownMarker":          false,
		"goOldTokenizerRepair":           false,
		"replacementImplication":         "blocks-standalone-db-fts-replacement",
		"fallbackCapability":             "search.largeDataRelevanceContract.no5xxFallbackAtLargeScale",
		"requiresDatabaseLifecycleOwner": "database.replacementContract.goFTSSetupRepair",
	})
	assertNestedCapability(t, caps, []string{"search", "crossEngineProductGate"}, map[string]any{
		"decisionTracked":               true,
		"acceptedAsCrossEngineDecision": true,
		"sqliteBaseline":                "typescript-created-sqlite-fts5-trigram",
		"postgresMode":                  "go-owned-postgres-like-compatibility",
		"nonSQLiteGoldenParity":         false,
		"routeSupported":                true,
		"replacementImplication":        "blocks-engine-agnostic-search-replacement",
		"postgresCapability":            "search.postgresSearchContract",
	})
	assertNestedCapability(t, caps, []string{"search", "largeDataRelevanceContract"}, map[string]any{
		"boundaryTracked":                   true,
		"currentTSGoFixture":                "expanded-ts-created-cjk-short-query-large-relevance",
		"largerRealisticRelevanceSmoke":     true,
		"tsGoEndpointSmoke":                 true,
		"rankingOrderDiffClean":             true,
		"multiEntityTopKOrderAsserted":      true,
		"degradedLargeDataMetadataSmoke":    true,
		"missingFTSLargeFixtureSmoke":       true,
		"corruptFTSFixtureSmoke":            true,
		"no5xxFallbackAtLargeScale":         true,
		"frontendAdditiveMetadataTolerance": "frontend-api-and-runtime-status-smoke",
		"acceptance":                        "complete DB-FTS-01 TS-vs-Go search smoke with a larger realistic TS-created corpus, ranking/order diff, degraded metadata under missing/corrupt FTS copies, and no 5xx fallback responses",
	})
	assertStringSliceCapability(t, caps, []string{"search", "largeDataRelevanceContract"}, "requiredCorpusFeatures", []string{
		"chapter-title-and-description-collisions",
		"narrator-title-collisions",
		"message-body-collisions",
		"cjk-trigram-substrings",
		"timestamp-tie-breakers",
		"missing-or-corrupt-fts-copy",
	})
	assertNestedCapability(t, caps, []string{"search", "replacementContract"}, map[string]any{
		"boundaryTracked":                          true,
		"runtimeRow":                               "DB-FTS-01",
		"route":                                    "GET /api/search",
		"currentBaseline":                          "expanded-ts-created-cjk-short-query-large-relevance",
		"minimalTSGoFixtureClean":                  true,
		"existingTypeScriptFTSReadiness":           true,
		"likeFallbackMetadataSafe":                 true,
		"shortQueryLikeSampled":                    true,
		"largerRealisticRelevanceSmoke":            true,
		"tsGoEndpointSmoke":                        true,
		"rankingOrderDiffClean":                    true,
		"multiEntityTopKOrderAsserted":             true,
		"missingCorruptFTSNo5xxSmoke":              true,
		"ftsLifecycleOwnershipParity":              false,
		"ftsLifecycleExplicitProductDecision":      true,
		"frontendAdditiveMetadataFullSmoke":        true,
		"crossEngineSearchGoldenParity":            false,
		"crossEngineSearchExplicitProductDecision": true,
		"fullDBFTS01Parity":                        false,
		"replacementSafe":                          false,
	})
	assertStringSliceCapability(t, caps, []string{"search", "replacementContract"}, "sampledProofs", []string{
		"minimal-ts-created-cjk-query",
		"minimal-short-query-like-fallback",
		"ready-fts-metadata-rank-snippet",
		"invalid-entity-empty-result",
		"explicit-like-fallback-metadata",
		"large-ts-created-auroraanchor-multi-entity-top-k-order",
		"large-ts-created-auroraanchor-same-type-raw-rank-tie-breaker",
		"chrononeedle-timestamp-tie-breaker",
		"large-missing-fts-no-5xx-degraded-fallback",
		"large-corrupt-fts-no-5xx-degraded-fallback",
		"frontend-search-api-additive-metadata-tolerance",
		"frontend-search-runtime-status-additive-metadata-tolerance",
		"fts-lifecycle-explicit-product-gating",
		"cross-engine-search-explicit-product-gating",
	})
	assertStringSliceCapability(t, caps, []string{"search", "replacementContract"}, "requiredProofs", []string{
		"full-db-fts-01-replacement-acceptance",
	})
	assertNestedCapability(t, caps, []string{"projects", "create"}, map[string]any{
		"supported":                       true,
		"route":                           "POST /api/projects",
		"gitGuarded":                      true,
		"authRequired":                    true,
		"jsonCreateStatus":                float64(201),
		"cloneSSE":                        true,
		"cloneCredentialRetrySurface":     true,
		"defaultBranchDetection":          true,
		"remoteURLCredentialStripping":    true,
		"basicProjectRowCreate":           true,
		"rootChapterCreate":               true,
		"rootChapterBestEffort":           true,
		"rootNarratorCreate":              true,
		"projectDBInitOnCreate":           true,
		"gitignoreCommitParity":           true,
		"cloneAuthCleanupParity":          true,
		"rootChapterEventParity":          true,
		"rootChapterCommitSyncParity":     true,
		"proxyDomainCreateInputSupported": false,
		"proxyCacheRefreshParity":         true,
		"proxyCacheRefreshReason":         "create-schema-excludes-proxy-domain-so-create-has-no-proxy-refresh-side-effect",
		"frontendProjectCreateSmoke":      true,
		"tsGoEndpointDiffSmoke":           true,
		"replacementSafe":                 false,
	})
	assertStringSliceCapability(t, caps, []string{"projects", "create"}, "modes", []string{"existing", "init", "clone"})
	assertStringSliceCapability(t, caps, []string{"projects", "create"}, "cloneSSEEvents", []string{"progress", "credential_required", "error", "complete"})
	assertStringSliceCapability(t, caps, []string{"projects", "create"}, "sampledProofs", []string{
		"auth-boundary-structured-401",
		"existing-init-clone-route-surface",
		"clone-sse-progress-credential-error-complete-events",
		"clone-auth-failure-directory-cleanup",
		"create-time-project-db-init",
		"gitignore-entry-and-commit-parity",
		"root-chapter-created-event-broadcast",
		"root-chapter-initial-commit-sync",
		"frontend-project-create-list-refresh-no-crash",
		"project-create-ts-go-db-git-fs-diff",
		"project-row-create",
		"best-effort-root-chapter-create",
		"root-narrator-create",
		"create-schema-excludes-proxy-domain-no-refresh",
	})
	assertStringSliceCapability(t, caps, []string{"projects", "create"}, "requiredProofs", []string{
		"full-ts-project-create-lifecycle-parity",
	})
	assertNestedCapability(t, caps, []string{"projects", "create", "replacementContract"}, map[string]any{
		"boundaryTracked":                      true,
		"runtimeRow":                           "PROJECT-CREATE-LIFECYCLE-01",
		"routeSurfaceProof":                    true,
		"cloneSSEFocusedProof":                 true,
		"credentialRetrySurfaceProof":          true,
		"defaultBranchDetectionProof":          true,
		"remoteURLCredentialStrippingProof":    true,
		"projectRowCreateProof":                true,
		"rootChapterBestEffortProof":           true,
		"rootNarratorCreateProof":              true,
		"projectDBInitOnCreate":                true,
		"gitignoreCommitParity":                true,
		"cloneAuthCleanupParity":               true,
		"rootChapterEventParity":               true,
		"rootChapterCommitSyncParity":          true,
		"proxyDomainCreateInputSupported":      false,
		"proxyCacheRefreshParity":              true,
		"proxyCacheRefreshReason":              "create-schema-excludes-proxy-domain-so-create-has-no-proxy-refresh-side-effect",
		"frontendProjectCreateSmoke":           true,
		"tsGoProjectCreateE2EDBGitFSDiffSmoke": true,
		"fullTSProjectCreateLifecycleParity":   false,
		"replacementSafe":                      false,
	})
	assertStringSliceCapability(t, caps, []string{"projects", "create", "replacementContract"}, "sampledProofs", []string{
		"auth-boundary-structured-401",
		"existing-init-clone-route-surface",
		"clone-sse-progress-credential-error-complete-events",
		"clone-auth-failure-directory-cleanup",
		"create-time-project-db-init",
		"gitignore-entry-and-commit-parity",
		"root-chapter-created-event-broadcast",
		"root-chapter-initial-commit-sync",
		"frontend-project-create-list-refresh-no-crash",
		"project-create-ts-go-db-git-fs-diff",
		"project-row-create",
		"best-effort-root-chapter-create",
		"root-narrator-create",
		"create-schema-excludes-proxy-domain-no-refresh",
	})
	assertStringSliceCapability(t, caps, []string{"projects", "create", "replacementContract"}, "requiredProofs", []string{
		"full-ts-project-create-lifecycle-parity",
	})
	assertNestedCapability(t, caps, []string{"projects", "delete"}, map[string]any{
		"supported":                                true,
		"route":                                    "DELETE /api/projects/{id}",
		"dbResourceCleanupProof":                   true,
		"broadProjectRowsCleanup":                  true,
		"terminalRuntimeKill":                      true,
		"nonRootWorktreeRemoval":                   true,
		"worktreePruneAttempt":                     true,
		"containerIdRemoval":                       true,
		"recentTabsDBCleanup":                      true,
		"workspaceRecentTabJSONCleanup":            true,
		"externalFailureWarnings":                  true,
		"deterministicExternalCleanupWarningSmoke": true,
		"realGitWorktreeCleanupWarningSmoke":       true,
		"realPodmanCleanupWarningSmoke":            false,
		"realRuntimeCleanupWarningSmoke":           false,
		"realRuntimeCleanupWarningReason":          "real git worktree warning is sampled; live Podman container warning smoke remains environment-gated",
		"proxyCacheInvalidationSmoke":              true,
		"recentTabsSnapshotBroadcastSmoke":         true,
		"frontendProjectListRefreshSmoke":          true,
		"tsAsyncRefreshParity":                     false,
		"externalSideEffectParity":                 false,
		"tsGoProjectDeleteE2EDBFSDiffSmoke":        true,
		"replacementSafe":                          false,
	})
	assertStringSliceCapability(t, caps, []string{"projects", "delete"}, "sampledCleanupScopes", []string{
		"project-row",
		"chapters",
		"chapter-edges",
		"narrators-and-messages",
		"terminals-and-tabs",
		"containers-and-ports",
		"merge-sessions",
		"review-conclusions",
		"volume-snapshots",
		"hooks",
		"gateway-mappings",
		"benchmark-rows",
		"deterministic-container-worktree-warning-contract",
		"real-git-worktree-cleanup-warning-smoke",
		"recent-tabs-json",
		"recent-tabs-snapshot-broadcast",
		"proxy-cache-invalidation",
		"frontend-project-list-refresh-no-crash",
		"ts-go-delete-e2e-db-fs-diff",
	})
	assertStringSliceCapability(t, caps, []string{"projects", "delete"}, "requiredExternalProofs", []string{
		"real-podman-container-cleanup-warning-smoke",
	})
	assertNestedCapability(t, caps, []string{"projects", "delete", "replacementContract"}, map[string]any{
		"boundaryTracked":                          true,
		"runtimeRow":                               "PROJECT-DELETE-EXTERNAL-SIDE-EFFECTS-01",
		"dbResourceCleanupProof":                   true,
		"broadProjectRowsCleanupProof":             true,
		"terminalRuntimeKillProof":                 true,
		"nonRootWorktreeRemovalProof":              true,
		"worktreePruneAttemptProof":                true,
		"containerIdRemovalProof":                  true,
		"recentTabsDBCleanupProof":                 true,
		"workspaceRecentTabJSONCleanupProof":       true,
		"externalFailureWarningsProof":             true,
		"deterministicExternalCleanupWarningSmoke": true,
		"realGitWorktreeCleanupWarningSmoke":       true,
		"realPodmanCleanupWarningSmoke":            false,
		"realRuntimeCleanupWarningSmoke":           false,
		"realRuntimeCleanupWarningReason":          "real git worktree warning is sampled; live Podman container warning smoke remains environment-gated",
		"proxyCacheInvalidationSmoke":              true,
		"recentTabsSnapshotBroadcastSmoke":         true,
		"frontendProjectListRefreshSmoke":          true,
		"tsAsyncRefreshParity":                     false,
		"externalSideEffectParity":                 false,
		"tsGoProjectDeleteE2EDBFSDiffSmoke":        true,
		"fullTSProjectDeleteSideEffectParity":      false,
		"replacementSafe":                          false,
	})
	assertStringSliceCapability(t, caps, []string{"projects", "delete", "replacementContract"}, "sampledProofs", []string{
		"project-row",
		"chapters",
		"chapter-edges",
		"narrators-and-messages",
		"terminals-and-tabs",
		"containers-and-ports",
		"merge-sessions",
		"review-conclusions",
		"volume-snapshots",
		"hooks",
		"gateway-mappings",
		"benchmark-rows",
		"deterministic-container-worktree-warning-contract",
		"real-git-worktree-cleanup-warning-smoke",
		"recent-tabs-json",
		"recent-tabs-snapshot-broadcast",
		"proxy-cache-invalidation",
		"frontend-project-list-refresh-no-crash",
		"ts-go-delete-e2e-db-fs-diff",
	})
	assertStringSliceCapability(t, caps, []string{"projects", "delete", "replacementContract"}, "requiredProofs", []string{
		"real-podman-container-cleanup-warning-smoke",
		"full-ts-project-delete-side-effect-parity",
	})
	assertNestedCapability(t, caps, []string{"projects", "replacementContract"}, map[string]any{
		"boundaryTracked":                          true,
		"runtimeRow":                               "PROJECT-LIFECYCLE-01",
		"createRouteSurfaceProof":                  true,
		"cloneSSEFocusedProof":                     true,
		"projectRowCreateProof":                    true,
		"rootChapterBestEffortProof":               true,
		"rootNarratorCreateProof":                  true,
		"deleteDBResourceCleanupProof":             true,
		"deleteExternalWarningsProof":              true,
		"deleteRealGitWorktreeCleanupWarningSmoke": true,
		"deleteRealPodmanCleanupWarningSmoke":      false,
		"deleteRealRuntimeCleanupWarningReason":    "real git worktree warning is sampled; live Podman container warning smoke remains environment-gated",
		"createProjectDBInitParity":                true,
		"createGitignoreCommitParity":              true,
		"createCloneAuthCleanupParity":             true,
		"createRootEventsCommitSyncParity":         true,
		"createProxyCacheRefreshParity":            true,
		"createProxyCacheRefreshReason":            "create-schema-excludes-proxy-domain-so-create-has-no-proxy-refresh-side-effect",
		"deleteProxyCacheInvalidationSmoke":        true,
		"deleteRecentTabsSnapshotBroadcast":        true,
		"deleteFrontendProjectListSmoke":           true,
		"deleteExternalSideEffectParity":           false,
		"frontendProjectCreateDeleteSmoke":         true,
		"tsGoProjectLifecycleDBGitFSDiff":          true,
		"replacementSafe":                          false,
	})
	assertStringSliceCapability(t, caps, []string{"projects", "replacementContract"}, "sampledProofs", []string{
		"project-create-auth-boundary-and-mode-surface",
		"clone-sse-progress-credential-error-complete-events",
		"clone-auth-failure-directory-cleanup",
		"create-time-project-db-init",
		"gitignore-entry-and-commit-parity",
		"root-chapter-created-event-broadcast",
		"root-chapter-initial-commit-sync",
		"frontend-project-create-list-refresh-no-crash",
		"project-create-ts-go-db-git-fs-diff",
		"project-row-root-chapter-root-narrator-create",
		"create-schema-excludes-proxy-domain-no-refresh",
		"project-delete-db-resource-cleanup",
		"terminal-container-worktree-cleanup-attempts",
		"deterministic-project-delete-cleanup-warning-contract",
		"real-git-worktree-cleanup-warning-smoke",
		"recent-tabs-json-db-cleanup",
		"project-delete-recent-tabs-snapshot-broadcast",
		"project-delete-proxy-cache-invalidation",
		"project-delete-frontend-list-refresh-no-crash",
		"project-delete-ts-go-db-fs-diff",
	})
	assertStringSliceCapability(t, caps, []string{"projects", "replacementContract"}, "requiredProofs", []string{
		"real-podman-container-cleanup-warning-parity",
	})
	assertNestedCapability(t, caps, []string{"contractHarness"}, map[string]any{

		"supported":                          true,
		"harnessPath":                        "go_backend/testtools/ts-go-api-contract-diff.ts",
		"comparisonMode":                     "sampled-json-sse-ws",
		"sameHomeDBToken":                    true,
		"samplesJSON":                        true,
		"samplesSSE":                         true,
		"samplesWS":                          true,
		"samplesHealthCapabilities":          true,
		"keyOrderInsensitiveShape":           true,
		"extraGoFieldsAllowed":               true,
		"semanticAssertions":                 true,
		"latestResultEmbeddedInHealth":       false,
		"fullRouteInventoryCovered":          false,
		"fullFrontendE2ECoverage":            false,
		"liveProviderCoverage":               false,
		"realRuntimeSideEffectCoverage":      false,
		"defaultCutoverRequiresCleanHarness": true,
		"acceptedDisabledByDesignRequired":   true,
	})
	assertNestedCapability(t, caps, []string{"contractHarness", "ciFreshnessContract"}, map[string]any{
		"boundaryTracked":                   true,
		"ciWired":                           false,
		"releaseGateWired":                  false,
		"healthEmbedsLatestResult":          false,
		"staleStaticHealthNotEvidence":      true,
		"requiresFreshRunForDefaultCutover": true,
		"requiresAcceptedDisabledByDesign":  true,
		"fullRouteInventoryCovered":         false,
		"fullFrontendE2ECoverage":           false,
		"liveProviderCoverage":              false,
		"realRuntimeSideEffectCoverage":     false,
		"manualCommand":                     "bun go_backend/testtools/ts-go-api-contract-diff.ts",
	})
	assertStringSliceCapability(t, caps, []string{"contractHarness", "ciFreshnessContract"}, "requiredGates", []string{
		"fresh-ts-go-api-contract-diff-run",
		"full-route-inventory-or-explicit-deferrals",
		"browser-frontend-e2e-smoke",
		"live-provider-or-explicit-credential-deferrals",
		"real-runtime-side-effect-smoke-or-disabled-contracts",
	})
	assertNestedCapability(t, caps, []string{"contractHarness", "replacementContract"}, map[string]any{
		"boundaryTracked":                  true,
		"runtimeRow":                       "API-CONTRACT-01",
		"sampledHarnessMetadata":           true,
		"latestResultEmbeddedInHealth":     false,
		"ciWired":                          false,
		"releaseGateWired":                 false,
		"fullRouteInventoryCovered":        false,
		"fullFrontendE2ECoverage":          false,
		"liveProviderCoverage":             false,
		"realRuntimeSideEffectCoverage":    false,
		"defaultCutoverRequiresFreshRun":   true,
		"acceptedDisabledByDesignRequired": true,
		"freshHarnessArtifactRequired":     true,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"contractHarness", "replacementContract"}, "sampledProofs", []string{
		"sampled-json-sse-ws-contract-harness-metadata",
		"same-home-db-token-harness-scope",
		"required-shape-and-semantic-assertions",
		"additive-go-fields-tolerance",
		"ci-freshness-contract-metadata",
	})
	assertStringSliceCapability(t, caps, []string{"contractHarness", "replacementContract"}, "requiredProofs", []string{
		"fresh-clean-ts-go-contract-run-artifact",
		"full-route-inventory-or-explicit-deferrals",
		"browser-frontend-e2e-smoke",
		"live-provider-or-explicit-credential-deferrals",
		"real-runtime-side-effect-smoke-or-disabled-contracts",
		"ci-release-gate-wiring",
	})
	assertStringSliceCapability(t, caps, []string{"contractHarness"}, "sampledSurfaceGroups", []string{"health/auth/projects/settings/search/update-check", "protected-route-auth-boundaries", "provider-settings-and-local-fixture-flows", "storage-runtime-benchmark-admin-boundaries", "update-ui-json-and-download-sse", "routines-skills-hooks-mcp-ui", "user-preferences-and-favorites"})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "responseContract"}, map[string]any{"uploadShareCleanupTSGoSmoke": true, "invalidTargetTSGoSmoke": true, "emptyDirectoryResponseIsStable": true, "successInvalidatesStorageCache": true})
	assertNestedCapability(t, caps, []string{"storage", "replacementContract"}, map[string]any{"uploadShareCleanupTSGoSmoke": true, "cleanupInvalidTargetTSGoSmoke": true, "supportedCleanupCacheInvalidation": true, "databaseCleanupCacheInvalidation": true, "fullTSStorageCleanupParity": false, "replacementSafe": false})
	assertStringSliceCapability(t, caps, []string{"storage", "replacementContract"}, "sampledProofs", []string{"storage-scan-sse-order-contract", "regular-user-admin-boundary-probes", "storage-worktree-container-unsupported-fallback-fixture", "storage-database-vacuum-disabled-fixture", "upload-share-cleanup-route-contract", "ts-go-storage-upload-share-cleanup-smoke", "ts-go-storage-cleanup-invalid-target-smoke", "storage-cleanup-cache-invalidation-smoke", "storage-database-cleanup-cache-invalidation-smoke", "ts-go-storage-cache-invalidation-flow-smoke"})
	assertStringSliceCapability(t, caps, []string{"contractHarness"}, "unsampledCriticalGroups", []string{"full-route-inventory", "browser-frontend-e2e", "live-external-provider-credentials", "real-podman-runtime-side-effects", "large-db-search-relevance", "long-running-narrator-provider-sessions"})
	assertStringSliceCapability(t, caps, []string{"contractHarness"}, "safetyAssertions", []string{"status-code-content-type-required-fields", "auth-admin-boundaries", "fallback-success-not-parity", "no-raw-secret-exposure", "manual-or-disabled-runtime-actions-remain-gated"})
	assertNestedCapability(t, caps, []string{"releasePackaging"}, map[string]any{"mode": "go-beta-opt-in", "buildInfo": "ldflags-or-env", "frontend": "sidecar-directory-or-env", "changelog": "sidecar-directory-or-env", "singleFileEmbedded": false, "fullReplacementReady": false})
	assertNestedCapability(t, caps, []string{"releasePackaging", "scripts"}, map[string]any{"build": "bun scripts/build-go-backend.ts", "releaseBeta": "bun scripts/release-go-beta.ts", "rollbackDrill": "bun scripts/go-rollback-drill.ts", "devGo": "bun server/dev-all.ts --backend=go", "testGo": "bun scripts/go-task.ts test"})
	assertNestedCapability(t, caps, []string{"releasePackaging", "cutoverContract"}, map[string]any{"defaultDevBackend": "typescript", "defaultStartBackend": "typescript", "goBetaOptIn": true, "tsLegacyPathAvailable": true, "defaultReleaseSwitch": false, "rollbackDrillRequired": true, "tsGoContractCIRequired": true, "tsGoContractCIWired": false, "defaultCutoverBlocked": true, "fullReplacementReady": false})
	assertNestedCapability(t, caps, []string{"releasePackaging", "cutoverFreshnessContract"}, map[string]any{
		"boundaryTracked":                    true,
		"defaultCutoverBlocked":              true,
		"fullReplacementReady":               false,
		"tsGoContractCIWired":                false,
		"contractHarnessFreshCIWired":        false,
		"migrationStrictCIWired":             false,
		"rollbackDrillFreshCIWired":          false,
		"rollbackDrillReportEmbedded":        false,
		"updateSelfApplyFullParity":          false,
		"requiresFreshContractHarness":       true,
		"requiresFreshRollbackDrill":         true,
		"requiresStrictMigrationReleaseGate": true,
		"requiresCompiledUpdateSmoke":        true,
		"manualRollbackCommand":              "bun scripts/go-rollback-drill.ts --dry-run",
		"manualHarnessCommand":               "bun go_backend/testtools/ts-go-api-contract-diff.ts",
	})
	assertStringSliceCapability(t, caps, []string{"releasePackaging", "cutoverFreshnessContract"}, "requiredGates", []string{
		"fresh-ts-go-contract-harness",
		"strict-migration-release-gate-wired",
		"fresh-ts-go-ts-rollback-drill-report",
		"compiled-update-zstd-patch-smoke",
		"full-route-inventory-or-explicit-deferrals",
	})
	assertNestedCapability(t, caps, []string{"releasePackaging", "buildContract"}, map[string]any{"outputDirPattern": "dist/go-backend/{version}", "manifest": "manifest.json", "sha512Encoding": "base64", "cgoEnabled": false, "trimpath": true, "buildvcs": false, "buildChannel": "go-beta"})
	assertStringSliceCapability(t, caps, []string{"releasePackaging", "buildContract"}, "targets", []string{"linux-x64", "linux-arm64", "darwin-x64", "darwin-arm64", "win-x64"})
	assertStringSliceCapability(t, caps, []string{"releasePackaging", "buildContract"}, "manifestFields", []string{"version", "commit", "channel", "backend", "buildChannel", "generatedAt", "changelog", "artifacts"})
	assertStringSliceCapability(t, caps, []string{"releasePackaging", "buildContract"}, "artifactFields", []string{"platform", "goos", "goarch", "filename", "size", "sha512", "channel"})
	assertStringSliceCapability(t, caps, []string{"releasePackaging", "buildContract"}, "ldflags", []string{"Version", "Commit", "Channel"})
	assertNestedCapability(t, caps, []string{"releasePackaging", "releaseContract"}, map[string]any{"product": "narrafork-go-backend", "channel": "beta", "buildChannel": "go-beta", "uploadPolicy": "metadata-plus-zstd-patch-only", "fullBinaryUpload": false, "requiresPreviousArtifact": true, "fromVersionMustBeOlder": true, "requiresUpdateToken": true, "dryRunSupported": true, "dryRunRequiresManifest": true, "dryRunRequiresPreviousArtifact": true, "skipBuildSupported": true, "releaseNotesSource": "changelogs/v{version}.json-or---changelog", "defaultUpdateServer": "https://narrafork-update.b.domexie.cn"})
	assertStringSliceCapability(t, caps, []string{"releasePackaging", "releaseContract"}, "metadataFields", []string{"version", "channel", "platform", "filename", "size", "sha512", "releaseNotes", "zstdPatch", "zstdPatchMeta"})
	assertNestedCapability(t, caps, []string{"releasePackaging", "packagingArtifactContract"}, map[string]any{
		"boundaryTracked":                 true,
		"singleFileEmbedded":              false,
		"fullReplacementPackage":          false,
		"frontendMode":                    "sidecar-directory-or-env",
		"changelogMode":                   "sidecar-directory-or-env",
		"bundledStaticFrontend":           false,
		"embeddedGoChangelog":             false,
		"fullBinaryUpload":                false,
		"metadataPlusZstdPatchOnly":       true,
		"requiresPreviousArtifact":        true,
		"releaseNotesSidecarOrOverride":   true,
		"updateFullArtifactDownloadSmoke": false,
	})
	assertStringSliceCapability(t, caps, []string{"releasePackaging", "packagingArtifactContract"}, "acceptedBetaArtifacts", []string{
		"platform-go-binaries",
		"manifest-json",
		"base64-sha512-artifact-metadata",
		"metadata-plus-zstd-patch-release-records",
		"frontend-sidecar-directory-or-env",
		"changelog-sidecar-directory-or-env",
	})
	assertStringSliceCapability(t, caps, []string{"releasePackaging", "packagingArtifactContract"}, "requiredReplacementProofs", []string{
		"single-file-or-bundled-static-frontend",
		"embedded-or-bundled-go-changelog",
		"full-binary-publication-or-accepted-updater-policy",
		"full-artifact-update-download-smoke",
		"installer-or-service-wrapper-parity-if-required",
	})
	assertNestedCapability(t, caps, []string{"releasePackaging", "rollbackDrill"}, map[string]any{"script": "bun scripts/go-rollback-drill.ts", "temporaryHome": true, "sharedDatabase": true, "registersAdmin": true, "reportPathPattern": "dist/go-backend/{version}/rollback-drill.json", "buildsWhenMissing": true, "skipBuildSupported": true})
	assertStringSliceCapability(t, caps, []string{"releasePackaging", "rollbackDrill"}, "flow", []string{"ts-start", "ts-stop", "go-start", "go-stop", "ts-rollback", "ts-final-stop"})
	assertStringSliceCapability(t, caps, []string{"releasePackaging", "rollbackDrill"}, "healthChecks", []string{"TS backend", "Go backend", "TS backend after rollback"})
	assertNestedCapability(t, caps, []string{"nativeExtensions"}, map[string]any{"defaultEnabled": false, "scope": "go-owned-runtime-extensions"})
	assertNestedCapability(t, caps, []string{"nativeExtensions", "browserSessions"}, map[string]any{"defaultEnabled": false, "storage": "go-runtime-sidecar", "cutover": "explicit-enable", "rollback": "explicit-disable-or-export", "reason": browserSessionsDefaultOffReason})
	assertNestedCapability(t, caps, []string{"nativeExtensions", "containerBrowserToolAutoEnable"}, map[string]any{"defaultEnabled": false, "cutover": "explicit-enable", "rollback": "explicit-disable", "reason": containerBrowserToolAutoEnableReason})
	assertNestedCapability(t, caps, []string{"chapters", "split"}, map[string]any{"supported": true, "fallback": false, "mode": "split-at-commit"})
	assertNestedCapability(t, caps, []string{"chapters", "split", "routes"}, map[string]any{"splitAtCommit": true})
	assertNestedCapability(t, caps, []string{"chapters", "split", "partials", "compressedAISummary"}, map[string]any{"supported": false, "fallback": true, "mode": "bounded-ref-copy-with-deterministic-context-note", "deterministic": true, "copiesMessageRefs": true, "generatesModelSummary": false, "reason": graph.CompressedAISummaryFallbackReason})

	assertNestedCapability(t, caps, []string{"chapters", "split", "partials", "containerAutostart"}, map[string]any{"supported": true, "fallback": false, "mode": "background-start"})
	assertNestedCapability(t, caps, []string{"chapters", "dependencies"}, map[string]any{
		"supported":                     true,
		"edgeValidationProof":           true,
		"manualDependencyOnly":          true,
		"selfEdgeRejected":              true,
		"crossProjectRejected":          true,
		"duplicateEdgeRejected":         true,
		"metadataSanitization":          true,
		"redundantChapterFieldsSync":    true,
		"dependencyStatusSupported":     true,
		"dependencyStatusSyncsSource":   true,
		"newCommitCountFromCommitTable": true,
		"syncUpstreamSupported":         true,
		"updatesLastSyncedMetadata":     true,
		"refreshesTargetGitMetadata":    true,
		"goOnlyStatusSyncRoutes":        true,
		"statusSyncRouteDecision":       true,
		"tsRouteParity":                 false,
		"dependencyEventBusParity":      true,
		"projectDBSyncEventParity":      true,
		"syncConflictParity":            true,
		"syncCleanConflictDBGitProof":   true,
		"frontendGraphRefreshSmoke":     true,
		"tsGoEndpointDiffSmoke":         true,
		"replacementSafe":               true,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "dependencies"}, "edgeRoutes", []string{"GET /api/chapter-edges", "POST /api/chapter-edges", "DELETE /api/chapter-edges/{id}"})
	assertStringSliceCapability(t, caps, []string{"chapters", "dependencies"}, "chapterRoutes", []string{"GET /api/chapters/{id}/dependency-status", "POST /api/chapters/{id}/sync-upstream"})
	assertStringSliceCapability(t, caps, []string{"chapters", "dependencies"}, "syncStrategies", []string{"merge", "rebase"})
	assertNestedCapability(t, caps, []string{"chapters", "dependencies", "statusSyncRouteContract"}, map[string]any{
		"decision":             "go-only-additive-accepted",
		"tsRoutesMounted":      false,
		"goRoutesMounted":      true,
		"frontendReachability": "api-client-only-no-current-ui-callers",
		"capabilityDocumented": true,
		"tsRouteParity":        false,
		"replacementSafe":      false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "dependencies"}, "sampledProofs", []string{
		"self-edge-rejected",
		"cross-project-rejected",
		"manual-dependency-only-create-delete",
		"metadata-description-only-sanitization",
		"graph-has-upstream-updates-field",
		"sync-upstream-last-synced-metadata-update",
		"frontend-dependency-graph-refresh-no-crash",
		"dependency-created-removed-event-broadcasts",
		"project-db-sync-on-dependency-events",
		"status-sync-upstream-go-only-additive-route-contract",
		"sync-upstream-clean-conflict-db-git-contract",
		"ts-go-dependency-edge-lifecycle-e2e-metadata-diff",
		"full-ts-dependency-lifecycle-parity-closed",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "dependencies"}, "requiredProofs", []string{})
	assertNestedCapability(t, caps, []string{"chapters", "dependencies", "replacementContract"}, map[string]any{
		"boundaryTracked":                     true,
		"runtimeRow":                          "CHAPTER-DEPENDENCY-LIFECYCLE-01",
		"edgeValidationProof":                 true,
		"manualDependencyOnlyProof":           true,
		"metadataSanitizationProof":           true,
		"redundantChapterFieldsSyncProof":     true,
		"dependencyStatusProof":               true,
		"syncUpstreamMetadataProof":           true,
		"goOnlyStatusSyncRoutes":              true,
		"statusSyncRouteDecision":             true,
		"tsRouteParity":                       false,
		"dependencyEventBusParity":            true,
		"projectDBSyncEventParity":            true,
		"syncConflictParity":                  true,
		"syncCleanConflictDBGitProof":         true,
		"frontendGraphRefreshSmoke":           true,
		"tsGoDependencyLifecycleE2EDiffSmoke": true,
		"fullTSDependencyLifecycleParity":     true,
		"replacementSafe":                     true,
	})
	assertNestedCapability(t, caps, []string{"chapters", "dependencies", "replacementContract", "statusSyncRouteContract"}, map[string]any{
		"decision":             "go-only-additive-accepted",
		"tsRoutesMounted":      false,
		"goRoutesMounted":      true,
		"frontendReachability": "api-client-only-no-current-ui-callers",
		"capabilityDocumented": true,
		"tsRouteParity":        false,
		"replacementSafe":      false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "dependencies", "replacementContract"}, "sampledProofs", []string{
		"self-edge-rejected",
		"cross-project-rejected",
		"manual-dependency-only-create-delete",
		"metadata-description-only-sanitization",
		"graph-has-upstream-updates-field",
		"sync-upstream-last-synced-metadata-update",
		"frontend-dependency-graph-refresh-no-crash",
		"dependency-created-removed-event-broadcasts",
		"project-db-sync-on-dependency-events",
		"status-sync-upstream-go-only-additive-route-contract",
		"sync-upstream-clean-conflict-db-git-contract",
		"ts-go-dependency-edge-lifecycle-e2e-metadata-diff",
		"full-ts-dependency-lifecycle-parity-closed",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "dependencies", "replacementContract"}, "requiredProofs", []string{})
	assertNestedCapability(t, caps, []string{"chapters", "create"}, map[string]any{
		"supported":                    true,
		"route":                        "POST /api/chapters",
		"gitGuarded":                   true,
		"basicBranchWorktreeCreate":    true,
		"chapterRowCreate":             true,
		"narratorCreate":               true,
		"projectSettingsDefaultOnly":   true,
		"projectChapterSettingsParity": true,
		"eventBusBroadcastParity":      true,
		"autoDormantSchedulingParity":  true,
		"commitSyncParity":             true,
		"dbErrorRollbackTSParity":      true,
		"frontendGraphRefreshSmoke":    true,
		"tsGoEndpointDiffSmoke":        true,
		"replacementSafe":              true,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "create"}, "sampledProofs", []string{
		"git-required-guard",
		"basic-branch-worktree-create",
		"chapter-row-create",
		"narrator-create",
		"frontend-create-graph-refresh-no-crash",
		"create-event-bus-broadcast",
		"project-chapter-settings-auto-create-narrator",
		"create-db-error-branch-worktree-rollback",
		"create-commit-sync-after-create",
		"create-commit-sync-failure-nonfatal",
		"ts-go-create-e2e-db-git-fs-diff",
		"auto-dormant-scheduling",
		"full-ts-create-lifecycle-closure",
	})

	assertNestedCapability(t, caps, []string{"chapters", "create", "replacementContract"}, map[string]any{
		"boundaryTracked":                 true,
		"runtimeRow":                      "CHAPTER-CREATE-LIFECYCLE-01",
		"gitGuardedProof":                 true,
		"basicBranchWorktreeProof":        true,
		"chapterRowCreateProof":           true,
		"narratorCreateProof":             true,
		"projectSettingsDefaultOnlyProof": true,
		"projectChapterSettingsParity":    true,
		"dbErrorRollbackTSParity":         true,
		"eventBusBroadcastParity":         true,
		"autoDormantSchedulingParity":     true,
		"commitSyncParity":                true,
		"frontendGraphRefreshSmoke":       true,
		"tsGoCreateE2EDBGitFSDiffSmoke":   true,
		"fullTSCreateLifecycleParity":     true,
		"replacementSafe":                 true,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "create", "replacementContract"}, "sampledProofs", []string{
		"git-required-guard",
		"basic-branch-worktree-create",
		"chapter-row-create",
		"narrator-create",
		"frontend-create-graph-refresh-no-crash",
		"create-event-bus-broadcast",
		"project-chapter-settings-auto-create-narrator",
		"create-db-error-branch-worktree-rollback",
		"create-commit-sync-after-create",
		"create-commit-sync-failure-nonfatal",
		"ts-go-create-e2e-db-git-fs-diff",
		"auto-dormant-scheduling",
		"full-ts-create-lifecycle-closure",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "create", "replacementContract"}, "requiredProofs", []string{})
	assertNestedCapability(t, caps, []string{"chapters", "delete"}, map[string]any{
		"supported":                          true,
		"route":                              "DELETE /api/chapters/{id}",
		"dbCleanupProof":                     true,
		"archivesNarratorsBeforeDelete":      true,
		"detachesNarratorChapterRefs":        true,
		"recentTabsDBCleanup":                true,
		"recentTabsCleanupNonFatalParity":    true,
		"targetMergeSessionCleanupParity":    true,
		"archiveBeforeResourceCleanupParity": true,
		"missingOptionalColumnsSafe":         true,
		"liveNarratorInterruptSmoke":         true,
		"recentTabsSnapshotBroadcastSmoke":   true,
		"chapterAbandonedBroadcastSmoke":     true,
		"frontendGraphRefreshSmoke":          true,
		"tsGoLiveSessionSmoke":               false,
		"replacementSafe":                    false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "delete"}, "sampledCleanupScopes", []string{
		"chapter-row",
		"live-narrator-interrupt-before-delete",
		"narrator-archive-detach",
		"archive-before-resource-cleanup",
		"target-merge-session-cleanup",
		"recent-tabs-cleanup-nonfatal",
		"recent-tabs-json",
		"recent-tabs-snapshot-broadcast",
		"chapter-abandoned-ws-broadcast",
		"frontend-graph-refresh-no-crash",
		"ts-go-delete-e2e-db-fs-diff",
		"schema-compatible-optional-columns",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "delete"}, "requiredExternalProofs", []string{})
	assertNestedCapability(t, caps, []string{"chapters", "delete", "replacementContract"}, map[string]any{
		"boundaryTracked":                    true,
		"runtimeRow":                         "CHAPTER-DELETE-EXTERNAL-SIDE-EFFECTS-01",
		"dbCleanupProof":                     true,
		"narratorArchiveDetachProof":         true,
		"recentTabsDBCleanupProof":           true,
		"recentTabsCleanupNonFatalParity":    true,
		"targetMergeSessionCleanupParity":    true,
		"archiveBeforeResourceCleanupParity": true,
		"schemaCompatibilityProof":           true,
		"liveNarratorInterruptSmoke":         true,
		"recentTabsSnapshotBroadcastSmoke":   true,
		"chapterAbandonedBroadcastSmoke":     true,
		"frontendGraphRefreshSmoke":          true,
		"tsGoDeleteE2EDBFSDiffSmoke":         true,
		"fullTSDeleteLifecycleParity":        false,
		"replacementSafe":                    false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "delete", "replacementContract"}, "sampledProofs", []string{
		"chapter-row-delete-db-cleanup",
		"live-narrator-interrupt-before-delete",
		"narrator-archive-detach-before-delete",
		"archive-before-resource-cleanup",
		"target-merge-session-cleanup",
		"recent-tabs-cleanup-nonfatal",
		"recent-tabs-json-cleanup",
		"recent-tabs-snapshot-broadcast",
		"chapter-abandoned-ws-broadcast",
		"frontend-graph-refresh-no-crash",
		"ts-go-delete-e2e-db-fs-diff",
		"schema-compatible-optional-columns",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "delete", "replacementContract"}, "requiredProofs", []string{
		"full-ts-delete-lifecycle-parity",
	})
	assertNestedCapability(t, caps, []string{"chapters", "dormantWake"}, map[string]any{
		"supported":                      true,
		"serviceLifecycleProof":          true,
		"routeFakePodmanProof":           true,
		"pauseBeforeWorktreeRemoval":     true,
		"wakeRecreatesWorktree":          true,
		"unpausePausedContainers":        true,
		"freshStartFallback":             true,
		"restoresPortAllocations":        true,
		"routeReturnsUpdatedChapterJSON": true,
		"containerLifecycleEvents":       true,
		"chapterLifecycleEvents":         true,
		"realPodmanSmoke":                false,
		"realisticRepoWorktreeSmoke":     true,
		"autoDormantSchedulingParity":    true,
		"commitSyncParity":               true,
		"tsGoEndpointDiffSmoke":          true,
		"frontendGraphRefreshSmoke":      true,
		"replacementSafe":                false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "dormantWake"}, "routes", []string{"POST /api/chapters/{id}/dormant", "POST /api/chapters/{id}/wake"})
	assertStringSliceCapability(t, caps, []string{"chapters", "dormantWake"}, "sampledProofs", []string{
		"service-fake-podman-pause-before-worktree-removal",
		"route-dormant-null-worktree-json",
		"route-wake-recreated-worktree-json",
		"unpause-paused-containers",
		"fresh-start-fallback-restores-ports",
		"container-and-chapter-lifecycle-events",
		"realistic-repo-worktree-remove-recreate-diff",
		"auto-dormant-scheduling-parity",
		"wake-commit-sync-after-woken-event-nonfatal",
		"frontend-dormant-wake-graph-refresh-no-crash",
		"ts-go-dormant-wake-endpoint-db-fs-diff",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "dormantWake"}, "requiredProofs", []string{
		"real-podman-dormant-wake-smoke",
	})
	assertNestedCapability(t, caps, []string{"chapters", "dormantWake", "replacementContract"}, map[string]any{
		"boundaryTracked":                      true,
		"runtimeRow":                           "CHAPTER-DORMANT-WAKE-REAL-RUNTIME-BOUNDARY-01",
		"serviceLifecycleProof":                true,
		"routeFakePodmanProof":                 true,
		"pauseBeforeWorktreeRemovalProof":      true,
		"wakeRecreatesWorktreeProof":           true,
		"unpausePausedContainersProof":         true,
		"freshStartFallbackPortsProof":         true,
		"routeUpdatedChapterJSONProof":         true,
		"lifecycleEventProof":                  true,
		"realPodmanSmoke":                      false,
		"realisticRepoWorktreeSmoke":           true,
		"autoDormantSchedulingParity":          true,
		"commitSyncParity":                     true,
		"frontendGraphRefreshSmoke":            true,
		"tsGoDormantWakeEndpointDBFSDiffSmoke": true,
		"fullTSDormantWakeLifecycleParity":     false,
		"replacementSafe":                      false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "dormantWake", "replacementContract"}, "sampledProofs", []string{
		"service-fake-podman-pause-before-worktree-removal",
		"route-dormant-null-worktree-json",
		"route-wake-recreated-worktree-json",
		"unpause-paused-containers",
		"fresh-start-fallback-restores-ports",
		"container-and-chapter-lifecycle-events",
		"realistic-repo-worktree-remove-recreate-diff",
		"auto-dormant-scheduling-parity",
		"wake-commit-sync-after-woken-event-nonfatal",
		"frontend-dormant-wake-graph-refresh-no-crash",
		"ts-go-dormant-wake-endpoint-db-fs-diff",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "dormantWake", "replacementContract"}, "requiredProofs", []string{
		"real-podman-dormant-wake-smoke",
		"full-ts-dormant-wake-lifecycle-parity",
	})
	assertNestedCapability(t, caps, []string{"chapters", "batchMerge"}, map[string]any{"supported": true, "fallback": false, "mode": "async-merge-session", "decisionWs": true, "staleSessionCleanup": true, "createdTargetRollback": true, "frontendCompletionMode": "progress-event-or-session-poll"})
	assertNestedCapability(t, caps, []string{"chapters", "batchMerge", "routes"}, map[string]any{"start": true, "session": true})
	assertNestedCapability(t, caps, []string{"chapters", "batchMerge", "response"}, map[string]any{"mergeSessionId": true, "targetChapterId": true, "createdTarget": true, "status": true})
	assertNestedCapability(t, caps, []string{"chapters", "batchMerge", "e2eContract"}, map[string]any{
		"boundaryTracked":                              true,
		"routeFocusedSmoke":                            true,
		"tsGoEndToEndSmoke":                            true,
		"reconnectDecisionSmoke":                       true,
		"conflictContinueCancelSmoke":                  true,
		"aiResolutionFailureSmoke":                     true,
		"sessionCompletedNullCleanupProof":             true,
		"missingSourceRollbackTSGoSmoke":               true,
		"createdTargetMissingSourceRollbackTSGoSmoke":  true,
		"partialSuccessExistingTargetFailureTSGoSmoke": true,
		"dbGitRollbackTSGoSmoke":                       false,
		"existingTargetFailurePreservesTargetProof":    true,
		"projectDBSyncSmoke":                           true,
		"frontendProgressUISmoke":                      true,
		"browserNoCrashSmoke":                          true,
		"replacementSafe":                              false,
	})
	assertNestedCapability(t, caps, []string{"chapters", "batchMerge", "e2eContract", "rollbackPolicy"}, map[string]any{
		"createdTarget":                "delete-created-target-on-failure",
		"existingTarget":               "preserve-existing-target-on-failure",
		"existingTargetPartialSuccess": "preserve-merged-changes-on-later-failure",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "batchMerge", "e2eContract"}, "sampledProofs", []string{
		"async-session-response-fields",
		"merge-event-name-list",
		"decision-ws-supported",
		"created-target-rollback",
		"stale-session-cleanup",
		"session-poll-waiting-decision-state",
		"existing-target-preserve-clean-preflight-policy",
		"batch-merge-project-db-sync-hook",
		"frontend-batch-merge-completed-refresh",
		"frontend-batch-merge-modal-no-crash",
		"ts-go-batch-merge-e2e-db-git-fs-diff",
		"batch-merge-completed-session-null-cleanup",
		"ts-go-batch-merge-missing-source-existing-target-rollback",
		"ts-go-batch-merge-missing-source-created-target-rollback",
		"ts-go-batch-merge-partial-success-existing-target-fallback",
		"conflict-continue-cancel-decisions",
		"ai-resolution-failure-path",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "batchMerge", "e2eContract"}, "requiredProofs", []string{
		"db-git-rollback-diff-clean",
	})
	assertNestedCapability(t, caps, []string{"chapters", "batchMerge", "e2eContract", "replacementContract"}, map[string]any{
		"boundaryTracked":                              true,
		"runtimeRow":                                   "CHAPTER-BATCH-MERGE-E2E-01",
		"routeFocusedSmoke":                            true,
		"asyncSessionResponseProof":                    true,
		"mergeEventNameProof":                          true,
		"decisionWSSupportedProof":                     true,
		"createdTargetRollbackProof":                   true,
		"staleSessionCleanupProof":                     true,
		"tsGoEndToEndSmoke":                            true,
		"reconnectDecisionSmoke":                       true,
		"conflictContinueCancelSmoke":                  true,
		"aiResolutionFailureSmoke":                     true,
		"sessionCompletedNullCleanupProof":             true,
		"missingSourceRollbackTSGoSmoke":               true,
		"createdTargetMissingSourceRollbackTSGoSmoke":  true,
		"partialSuccessExistingTargetFailureTSGoSmoke": true,
		"dbGitRollbackTSGoSmoke":                       false,
		"existingTargetFailurePreservesTargetProof":    true,
		"projectDBSyncSmoke":                           true,
		"frontendProgressUISmoke":                      true,
		"browserNoCrashSmoke":                          true,
		"fullTSBatchMergeE2EParity":                    false,
		"replacementSafe":                              false,
	})
	assertNestedCapability(t, caps, []string{"chapters", "batchMerge", "e2eContract", "replacementContract", "rollbackPolicy"}, map[string]any{
		"createdTarget":                "delete-created-target-on-failure",
		"existingTarget":               "preserve-existing-target-on-failure",
		"existingTargetPartialSuccess": "preserve-merged-changes-on-later-failure",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "batchMerge", "e2eContract", "replacementContract"}, "sampledProofs", []string{
		"async-session-response-fields",
		"merge-event-name-list",
		"decision-ws-supported",
		"created-target-rollback",
		"stale-session-cleanup",
		"session-poll-waiting-decision-state",
		"existing-target-preserve-clean-preflight-policy",
		"batch-merge-project-db-sync-hook",
		"frontend-batch-merge-completed-refresh",
		"frontend-batch-merge-modal-no-crash",
		"ts-go-batch-merge-e2e-db-git-fs-diff",
		"batch-merge-completed-session-null-cleanup",
		"ts-go-batch-merge-missing-source-existing-target-rollback",
		"ts-go-batch-merge-missing-source-created-target-rollback",
		"ts-go-batch-merge-partial-success-existing-target-fallback",
		"conflict-continue-cancel-decisions",
		"ai-resolution-failure-path",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "batchMerge", "e2eContract", "replacementContract"}, "requiredProofs", []string{
		"db-git-rollback-diff-clean",
		"full-ts-batch-merge-e2e-parity",
	})
	assertNestedCapability(t, caps, []string{"chapters", "replacementContract"}, map[string]any{
		"boundaryTracked":                  true,
		"runtimeRow":                       "CHAPTER-LIFECYCLE-01",
		"dependencyLifecycleProof":         true,
		"basicCreateProof":                 true,
		"deleteDBCleanupProof":             true,
		"dormantWakeFakeRuntimeProof":      true,
		"reviewSeedAndRollbackProof":       true,
		"mergeDataIntegrityFocusedProof":   true,
		"unmergeCoreResetProof":            true,
		"batchMergeRouteFocusedProof":      true,
		"splitRouteAdditivePartial":        true,
		"dependencyEventBusParity":         true,
		"projectChapterSettingsParity":     true,
		"deleteLiveNarratorInterruptSmoke": true,
		"dormantWakeRealRuntimeSmoke":      false,
		"reviewAutoStartSessionParity":     true,
		"mergeFullDataIntegrityParity":     true,
		"unmergeLifecycleParity":           true,
		"batchMergeEndToEndSmoke":          false,
		"frontendGraphRefreshSmoke":        true,
		"tsGoDBGitFSDiffSmoke":             true,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "replacementContract"}, "sampledProofs", []string{
		"dependency-edge-validation-and-sync-metadata",
		"basic-branch-worktree-chapter-narrator-create",
		"chapter-create-auto-dormant-ts-go-diff-smoke",
		"chapter-delete-db-cleanup-and-recent-tabs-json",
		"chapter-delete-live-narrator-recent-tabs-event-frontend-smoke",
		"dormant-wake-fake-podman-route-service-proof",
		"review-create-rollback-seed-message-actions",
		"review-commit-history-copy-parity",
		"review-auto-start-session-start-failure-rollback",
		"merge-source-dirty-auto-commit-before-merge",
		"merge-required-review-dirty-source-rejects-auto-commit",
		"merge-source-target-worktree-lock-proof",
		"merge-target-lock-rollback-focused-proof",
		"merge-source-worktree-terminal-cleanup-after-merge",
		"merge-target-commit-sync-after-merge",
		"frontend-merge-graph-refresh-no-crash",
		"merge-chapter-merged-event-broadcast",
		"merge-summary-full-injection",
		"full-ts-merge-data-integrity-parity",
		"unmerge-core-reset-revert-edge-cleanup",
		"unmerge-commit-sync-summary-cleanup-events-frontend-diff",
		"full-ts-unmerge-lifecycle-parity",
		"batch-merge-async-session-route-proof",
		"dependency-event-bus-and-project-db-sync-parity",
		"chapter-create-settings-rollback-event-commit-sync-parity",
		"chapter-aggregate-frontend-graph-refresh-no-crash",
		"chapter-aggregate-ts-go-db-git-fs-diff-suite",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "replacementContract"}, "requiredProofs", []string{
		"dormant-wake-real-podman-realistic-repo-commit-sync-smoke",
		"batch-merge-db-git-rollback-and-full-e2e-parity",
	})
	assertNestedCapability(t, caps, []string{"chapters", "containers"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "routeContract"}, map[string]any{
		"auth":              "api-authenticated",
		"gitGuarded":        false,
		"setup":             "GET /api/chapters/container-setup",
		"podmanStatus":      "GET /api/chapters/podman/status",
		"podmanInstall":     "POST /api/chapters/podman/install",
		"composeInfo":       "GET /api/chapters/{id}/compose-info",
		"list":              "GET /api/chapters/{id}/containers",
		"start":             "POST /api/chapters/{id}/containers/start",
		"stop":              "POST /api/chapters/{id}/containers/stop",
		"pause":             "POST /api/chapters/{id}/containers/pause",
		"unpause":           "POST /api/chapters/{id}/containers/unpause",
		"logs":              "GET /api/chapters/{id}/containers/logs",
		"remove":            "POST /api/chapters/{id}/containers/remove",
		"memberRoutesCount": float64(10),
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "routeContract"}, "adminOnlyRoutes", []string{"POST /api/chapters/podman/install"})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "requestContract"}, map[string]any{
		"removeDeleteVolumesOptional":  true,
		"logsDefaultTail":              float64(200),
		"logsMaxTail":                  float64(1000),
		"logsUnsafeServiceNameIgnored": true,
		"composeInfoRequiresWorktree":  true,
		"actionsRequireWorktree":       true,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "requestContract"}, "removeFields", []string{"deleteVolumes"})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "requestContract"}, "logsQueryParams", []string{"tail", "service"})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "responseContract"}, map[string]any{
		"listDirectArray":     true,
		"listFallbackHeaders": true,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "responseContract"}, "startFields", []string{"ok", "status", "fallback", "supported"})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "responseContract"}, "actionFields", []string{"ok", "status", "output", "fallback", "supported"})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "responseContract"}, "logsFields", []string{"ok", "logs", "truncated"})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "fallbackContract"}, map[string]any{
		"httpStatus":                     float64(http.StatusOK),
		"podmanMissingCode":              "PODMAN_NOT_FOUND",
		"podmanComposeMissingCode":       "PODMAN_COMPOSE_NOT_FOUND",
		"composeFileMissingCode":         "COMPOSE_FILE_NOT_FOUND",
		"proxyBackendUnavailableCode":    "PROXY_BACKEND_UNAVAILABLE",
		"containerStateUnavailableCode":  "CONTAINER_STATE_UNAVAILABLE",
		"listReturnsPersistedState":      true,
		"logsFallbackIncludesEmptyLogs":  true,
		"actionPodmanFallbackBeforeWork": true,
		"composeMissingActionStatus":     float64(http.StatusBadRequest),
		"proxyBackendActionStatus":       float64(http.StatusBadRequest),
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "fallbackContract"}, "fallbackFields", []string{"ok", "code", "supported", "fallback", "reason", "error", "message"})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "fallbackContract"}, "listFallbackHeaders", []string{"X-Narrafork-Fallback", "X-Narrafork-Fallback-Code", "X-Narrafork-Fallback-Reason"})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "runtime"}, map[string]any{
		"syncStartRequest":             false,
		"backgroundStart":              true,
		"backgroundStartReason":        containerBackgroundStartReason,
		"streamingLogs":                true,
		"perChapterLock":               true,
		"commandOutputLimitBytes":      float64(64 * 1024),
		"defaultCommandTimeoutSeconds": float64(60),
		"startTimeoutSeconds":          float64(600),
		"fullTSContainerRuntimeParity": false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "runtime"}, "streamingLogPhases", []string{"build", "start"})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "ports"}, map[string]any{
		"legacyHostPortAllocation":         true,
		"portRelease":                      true,
		"defaultRangeStart":                float64(10000),
		"defaultRangeEnd":                  float64(20000),
		"releaseBeforeAllocate":            true,
		"releaseOnStartFailure":            true,
		"proxyModeSkipsHostPortAllocation": true,
		"invalidMappingStatus":             float64(http.StatusBadRequest),
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "ports"}, "settingsRangeKeys", []string{"portRangeStart", "portRangeEnd"})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "lifecycle"}, map[string]any{"manualControls": true, "autoStartOnFork": true, "pauseOnDormant": true, "unpauseOnWake": true, "deleteVolumes": true})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "validation"}, map[string]any{"serviceFakePodmanProof": true, "routeFakePodmanProof": true, "realPodmanSmoke": false, "realRuntimeParity": "unverified"})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "proxy"}, map[string]any{
		"metadataSupported":            true,
		"requiresPastaPasst":           true,
		"overridePortsReset":           true,
		"temporaryOverrideFileCleanup": true,
		"proxyLabelFormat":             "{chapterShortId}-{sanitizedServiceName}-{containerPort}",
		"proxyURLFormat":               "http://{proxyLabel}.{domain}[:port]",
		"reverseProxyServer":           true,
		"http":                         true,
		"websocket":                    true,
		"dynamicSettingsHook":          true,
	})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "eventContract"}, map[string]any{"statusChangedEvent": true, "fullTSEventParity": false})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "eventContract"}, "legacyCompatEvents", []string{"container:starting", "container:log", "container:started", "container:stopped", "container:paused", "container:resumed", "container:error"})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "narratorIntegration"}, map[string]any{"statusChangedEvent": true, "containerReadyMessage": true, "browserToolAutoEnable": false, "browserToolAutoEnableReason": containerBrowserToolAutoEnableReason, "defaultEnabled": false, "cutover": "explicit-enable", "rollback": "explicit-disable"})
	assertNestedCapability(t, caps, []string{"chapters", "containers", "replacementContract"}, map[string]any{
		"boundaryTracked":                true,
		"runtimeRow":                     "RUNTIME-CONTAINER-01",
		"routeFallbacksSampled":          true,
		"fakePodmanLifecycleProof":       true,
		"proxyMetadataSmoke":             true,
		"portAllocatorSmoke":             true,
		"eventCompatibilitySmoke":        true,
		"backgroundStartCompatibility":   true,
		"realRootlessPodmanSmoke":        false,
		"cgroupBackendDetectionParity":   false,
		"proxyCacheReconciliationParity": false,
		"portAllocatorEdgeCaseParity":    false,
		"lifecycleEventTSParity":         false,
		"browserToolAutoEnableParity":    false,
		"frontendContainerPanelSmoke":    false,
		"tsGoContainerServiceDiffSmoke":  false,
		"fullTSContainerRuntimeParity":   false,
		"replacementSafe":                false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "replacementContract"}, "sampledProofs", []string{
		"fake-podman-service-and-route-lifecycle",
		"bounded-output-and-log-tail-contract",
		"proxy-mode-metadata-smoke",
		"port-release-before-allocate-smoke",
		"container-status-and-legacy-event-list",
		"explicit-runtime-fallback-codes",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "containers", "replacementContract"}, "requiredProofs", []string{
		"real-rootless-podman-cgroup-proxy-smoke",
		"proxy-cache-reconciliation-smoke",
		"port-allocator-edge-case-ts-go-diff",
		"container-lifecycle-event-ts-parity",
		"frontend-container-panel-browser-smoke",
		"browser-tool-auto-enable-product-decision",
		"ts-go-container-service-diff-smoke",
	})
	assertNestedCapability(t, caps, []string{"gitPanel"}, map[string]any{
		"supported":       true,
		"routePrefix":     "/api/chapters/{chapterId}/git",
		"gitGuarded":      true,
		"authRequired":    true,
		"replacementSafe": false,
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel"}, "routes", []string{
		"GET /api/chapters/{chapterId}/git/status",
		"POST /api/chapters/{chapterId}/git/stage",
		"POST /api/chapters/{chapterId}/git/unstage",
		"POST /api/chapters/{chapterId}/git/commit",
		"POST /api/chapters/{chapterId}/git/discard",
		"GET /api/chapters/{chapterId}/git/diff",
		"GET /api/chapters/{chapterId}/git/stash/list",
		"POST /api/chapters/{chapterId}/git/stash",
		"GET /api/chapters/{chapterId}/git/log",
		"POST /api/chapters/{chapterId}/git/reset",
		"POST /api/chapters/{chapterId}/git/ai-commit-message",
	})
	assertNestedCapability(t, caps, []string{"gitPanel", "discardSafety"}, map[string]any{
		"supported":        true,
		"trackedRestore":   true,
		"untrackedCleanup": true,
		"selectedDiscardKeepsUnselectedUntracked": true,
		"discardAllRemovesUntrackedDirectories":   true,
		"pathValidation":                          true,
		"responseReturnsStatusSummary":            true,
		"focusedServiceProof":                     true,
		"replacementSafe":                         true,
	})
	assertNestedCapability(t, caps, []string{"gitPanel", "discardSafety", "replacementContract"}, map[string]any{
		"boundaryTracked":       true,
		"runtimeRow":            "GIT-PANEL-DISCARD-SAFETY-01",
		"supported":             true,
		"trackedRestoreProof":   true,
		"untrackedCleanupProof": true,
		"selectedDiscardKeepsUnselectedUntracked": true,
		"discardAllRemovesUntrackedDirectories":   true,
		"pathValidationProof":                     true,
		"responseReturnsStatusSummaryProof":       true,
		"focusedServiceProof":                     true,
		"tsCompatibleTrackedUntrackedSemantics":   true,
		"broaderGitPanelReplacementStillUnsafe":   true,
		"broaderReplacementContract":              "gitPanel.replacementContract",
		"replacementSafe":                         true,
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "discardSafety", "replacementContract"}, "sampledProofs", []string{
		"discard-all-restores-tracked-and-removes-untracked-directories",
		"discard-files-restores-tracked-and-removes-selected-untracked",
		"selected-discard-keeps-unselected-untracked",
		"path-validation-and-status-summary-response",
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "discardSafety", "replacementContract"}, "requiredProofs", []string{
		"keep-discard-focused-service-proof-green",
		"route-new-discard-scope-through-gitPanel.replacementContract-before-expansion",
	})
	assertNestedCapability(t, caps, []string{"gitPanel", "writeContract"}, map[string]any{
		"stageUnstageCommitDiscardStashResetRoutes": true,
		"statusBroadcast":           true,
		"broadcastSourceField":      true,
		"commitSyncMetadata":        true,
		"resetHeadMetadataSync":     true,
		"worktreeWriteLockParity":   true,
		"readNoOptionalLocksParity": true,
		"fullTSWriteRouteParity":    false,
		"replacementSafe":           false,
	})
	assertNestedCapability(t, caps, []string{"gitPanel", "writeContract", "responseShapeContract"}, map[string]any{
		"stageUnstageDiscardReset":      "StatusSummary",
		"tsRequiredResponseFieldsProof": true,
		"fullExactTSShapeParity":        false,
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "writeContract", "responseShapeContract"}, "commitRequiredFields", []string{"commitSha", "status"})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "writeContract", "responseShapeContract"}, "commitGoAdditiveFields", []string{"commitSync"})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "writeContract", "responseShapeContract"}, "stashRequiredFields", []string{"hasConflicts", "status"})
	assertNestedCapability(t, caps, []string{"gitPanel", "writeContract", "replacementContract"}, map[string]any{
		"boundaryTracked":                    true,
		"runtimeRow":                         "GIT-PANEL-WRITE-01",
		"writeRouteSurfaceProof":             true,
		"statusBroadcastProof":               true,
		"broadcastSourceFieldProof":          true,
		"commitSyncMetadataProof":            true,
		"resetHeadMetadataSyncProof":         true,
		"writeRouteResponseShapeProof":       true,
		"statusSummaryResponseProof":         true,
		"worktreeWriteLockParity":            true,
		"readNoOptionalLocksParity":          true,
		"frontendWriteRouteAPISmoke":         true,
		"frontendWriteRouteComponentSmoke":   true,
		"frontendWriteRouteInteractiveSmoke": true,
		"frontendWriteRouteSmoke":            true,
		"tsGoWriteRouteDiffSmoke":            true,
		"fullTSWriteRouteParity":             false,
		"replacementSafe":                    false,
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "writeContract", "replacementContract"}, "sampledProofs", []string{
		"stage-unstage-commit-discard-stash-reset-route-surface",
		"status-broadcast-source-field",
		"commit-sync-metadata",
		"write-response-status-summary",
		"write-route-ts-required-response-shape-contract",
		"reset-head-metadata-sync",
		"frontend-git-api-consumer-smoke",
		"frontend-git-panel-component-smoke",
		"frontend-git-panel-interactive-write-ai-smoke",
		"ts-go-git-panel-write-route-diff-smoke",
		"per-worktree-write-lock-helper",
		"read-status-no-optional-locks-helper",
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "writeContract", "replacementContract"}, "requiredProofs", []string{
		"full-ts-git-panel-write-route-parity",
	})
	assertNestedCapability(t, caps, []string{"gitPanel", "readEndpointContract"}, map[string]any{
		"statusShapeParity":         true,
		"untrackedFileDiffParity":   true,
		"logFrontendFieldsParity":   true,
		"tsGoReadEndpointDiffSmoke": true,
	})
	assertNestedCapability(t, caps, []string{"gitPanel", "statusContract"}, map[string]any{
		"fileCap":                        float64(200),
		"totalFilesReported":             true,
		"fullLineStatsParity":            true,
		"readNoOptionalLocksParity":      true,
		"untrackedLineStatsBounded":      true,
		"untrackedLineStatsMaxFiles":     float64(200),
		"untrackedLineStatsMaxBytes":     float64(1_000_000),
		"frontendGitStatusAPISmoke":      true,
		"frontendGitPanelComponentSmoke": true,
		"frontendGitStatusSmoke":         false,
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "statusContract"}, "summaryFields", []string{"hasChanges", "staged", "unstaged", "untracked", "files", "totalFiles", "headSha", "branch", "linesAdded", "linesRemoved"})
	assertNestedCapability(t, caps, []string{"gitPanel", "aiCommitMessage"}, map[string]any{
		"route":                            "POST /api/chapters/{chapterId}/git/ai-commit-message",
		"emptyDiffReturnsEmpty":            true,
		"providerGeneration":               "summary-model-with-history",
		"summaryModelSetting":              "agent.summaryModel",
		"historyPromptShape":               "single-user-diff-message-with-system-prompt",
		"fallbackMessage":                  "chore: update files",
		"timeoutSeconds":                   float64(30),
		"summaryModelHistoryParity":        true,
		"providerErrorPropagationParity":   true,
		"fullTSProviderParity":             false,
		"frontendAIAPISmoke":               true,
		"frontendAIButtonRenderSmoke":      true,
		"frontendAICommitInteractiveSmoke": true,
		"frontendAISmoke":                  true,
		"replacementSafe":                  false,
	})
	assertNestedCapability(t, caps, []string{"gitPanel", "aiCommitMessage", "replacementContract"}, map[string]any{
		"boundaryTracked":                  true,
		"runtimeRow":                       "GIT-PANEL-AI-COMMIT-01",
		"emptyDiffProof":                   true,
		"summaryModelProof":                true,
		"historyPromptProof":               true,
		"sanitizeFallbackProof":            true,
		"timeoutProof":                     true,
		"providerErrorFallbackGap":         false,
		"providerErrorPropagationProof":    true,
		"frontendAIAPISmoke":               true,
		"frontendAIButtonRenderSmoke":      true,
		"frontendAICommitInteractiveSmoke": true,
		"frontendAISmoke":                  true,
		"tsGoAICommitDiffSmoke":            true,
		"fullTSProviderParity":             false,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "aiCommitMessage", "replacementContract"}, "sampledProofs", []string{
		"empty-diff-empty-message",
		"summary-model-generator-path",
		"single-user-diff-history-prompt",
		"sanitize-empty-or-long-static-fallback",
		"provider-error-propagation",
		"frontend-ai-button-render-smoke",
		"frontend-git-panel-interactive-write-ai-smoke",
		"frontend-git-api-consumer-smoke",
		"ts-go-ai-commit-empty-diff-endpoint-smoke",
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "aiCommitMessage", "replacementContract"}, "requiredProofs", []string{
		"ts-go-ai-commit-provider-history-diff",
		"full-ts-ai-commit-provider-parity",
	})
	assertNestedCapability(t, caps, []string{"gitPanel", "replacementContract"}, map[string]any{
		"boundaryTracked":                  true,
		"runtimeRow":                       "GIT-PANEL-01",
		"discardSafetyFocusedProof":        true,
		"writeRouteSurfaceProof":           true,
		"writeRouteResponseShapeProof":     true,
		"writeResetHeadMetadataProof":      true,
		"statusSummaryContractProof":       true,
		"statusBroadcastFocusedProof":      true,
		"readEndpointShapeProof":           true,
		"aiCommitFallbackProof":            true,
		"worktreeWriteLockParity":          true,
		"readNoOptionalLocksParity":        true,
		"fullLineStatsParity":              true,
		"tsGoReadEndpointDiffSmoke":        true,
		"aiCommitProviderHistoryParity":    true,
		"aiCommitProviderErrorParity":      true,
		"frontendAPIConsumerSmoke":         true,
		"frontendGitPanelComponentSmoke":   true,
		"frontendGitPanelInteractiveSmoke": true,
		"frontendGitPanelSmoke":            true,
		"tsGoEndpointDiffSmoke":            true,
		"fullTSGitPanelParity":             false,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "replacementContract"}, "sampledProofs", []string{
		"git-required-guard",
		"discard-all-tracked-restore",
		"discard-all-untracked-directory-clean",
		"selected-discard-tracked-restore",
		"selected-discard-untracked-only",
		"path-validation-before-selected-discard",
		"write-response-status-summary",
		"write-route-ts-required-response-shape-contract",
		"reset-head-metadata-sync",
		"git-status-broadcast-route-proof",
		"per-worktree-write-lock-helper",
		"read-status-no-optional-locks-helper",
		"status-summary-staged-unstaged-untracked-line-stats",
		"untracked-file-diff-new-file-shape",
		"git-log-shortSha-author-date-fields",
		"frontend-git-api-consumer-smoke",
		"frontend-git-panel-component-smoke",
		"frontend-git-panel-interactive-write-ai-smoke",
		"ts-go-git-panel-read-write-ai-empty-diff-contract-harness",
		"ai-commit-empty-diff-empty-message",
		"ai-commit-provider-error-propagation",
		"ai-commit-summary-model-history-generator",
		"ai-commit-summary-model-or-static-fallback",
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel", "replacementContract"}, "requiredProofs", []string{
		"full-ts-git-panel-parity",
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel"}, "sampledProofs", []string{
		"git-required-guard",
		"discard-all-tracked-restore",
		"discard-all-untracked-directory-clean",
		"selected-discard-tracked-restore",
		"selected-discard-untracked-only",
		"path-validation-before-selected-discard",
		"write-response-status-summary",
		"write-route-ts-required-response-shape-contract",
		"reset-head-metadata-sync",
		"git-status-broadcast-route-proof",
		"per-worktree-write-lock-helper",
		"read-status-no-optional-locks-helper",
		"status-summary-staged-unstaged-untracked-line-stats",
		"untracked-file-diff-new-file-shape",
		"git-log-shortSha-author-date-fields",
		"frontend-git-api-consumer-smoke",
		"frontend-git-panel-component-smoke",
		"frontend-git-panel-interactive-write-ai-smoke",
		"ts-go-git-panel-read-write-ai-empty-diff-contract-harness",
		"ai-commit-empty-diff-empty-message",
		"ai-commit-provider-error-propagation",
		"ai-commit-summary-model-history-generator",
		"ai-commit-summary-model-or-static-fallback",
	})
	assertStringSliceCapability(t, caps, []string{"gitPanel"}, "requiredProofs", []string{
		"full-ts-git-panel-parity",
	})
	assertNestedCapability(t, caps, []string{"ruler"}, map[string]any{
		"supported":                             true,
		"readRoutes":                            true,
		"segmentRoutes":                         true,
		"positionMutation":                      true,
		"positionValidation":                    true,
		"narratorStatusFolding":                 true,
		"mutationRoutes":                        true,
		"mutationFallbacks":                     true,
		"gitLogDegradedFallback":                true,
		"historicalCommitForkSplit":             true,
		"lifecycleMutationParity":               "partial",
		"tempChapterAIConflictResolution":       false,
		"tempChapterAIFallbackContract":         true,
		"tempChapterAIConflictResolutionMode":   "unsupported-one-shot-fallback",
		"tempChapterAIFallbackCode":             "merge.aiResolveOneShotBasic",
		"tempChapterAIRequiresNarrator":         false,
		"tempChapterPreserved":                  false,
		"tempChapterAIConflictResolutionReason": "TypeScript forks a temporary chapter and preserves it on unresolved AI conflicts; Go currently uses a one-shot fallback on the target worktree and exposes route/capability metadata instead of claiming temp-chapter parity.",
		"fullTSMergeRebaseParity":               false,
		"trunkMergeDirtyCheckParity":            true,
		"rulerBranchDeletionParity":             true,
		"rebaseConflictNarratorParity":          true,
		"aiResolveFallbackContract":             true,
		"mutationLifecycleFocusedProof":         true,
		"mutationLifecycleTSGoDiffSmoke":        true,
		"frontendLifecycleSmoke":                true,
		"tsGoEndpointDiffSmoke":                 true,
		"replacementSafe":                       false,
		"frontendSmoke":                         "read-segment-position-sampled",
		"unsupportedMutationFallbackCode":       "FEATURE_DISABLED",
		"unsupportedMutationFallbackBeforeWork": true,
	})
	assertNestedCapability(t, caps, []string{"ruler", "replacementContract"}, map[string]any{
		"boundaryTracked":                       true,
		"runtimeRow":                            "RULER-LIFECYCLE-01",
		"readRouteProof":                        true,
		"segmentRouteProof":                     true,
		"positionMutationProof":                 true,
		"narratorStatusFoldingProof":            true,
		"mutationFallbackProof":                 true,
		"gitLogDegradedFallbackProof":           true,
		"historicalCommitForkSplitProof":        true,
		"trunkMergeDirtyCheckParity":            true,
		"rulerBranchDeletionParity":             true,
		"rebaseConflictNarratorParity":          true,
		"aiResolveFallbackContract":             true,
		"mutationLifecycleFocusedProof":         true,
		"mutationLifecycleTSGoDiffSmoke":        true,
		"tempChapterAIConflictResolution":       false,
		"tempChapterAIFallbackContract":         true,
		"tempChapterAIConflictResolutionMode":   "unsupported-one-shot-fallback",
		"tempChapterAIFallbackCode":             "merge.aiResolveOneShotBasic",
		"tempChapterAIRequiresNarrator":         false,
		"tempChapterPreserved":                  false,
		"tempChapterAIConflictResolutionReason": "TypeScript forks a temporary chapter and preserves it on unresolved AI conflicts; Go currently uses a one-shot fallback on the target worktree and exposes route/capability metadata instead of claiming temp-chapter parity.",
		"fullTSMergeRebaseParity":               false,
		"frontendLifecycleSmoke":                true,
		"tsGoEndpointDiffSmoke":                 true,
		"fullTSRulerLifecycleParity":            false,
		"replacementSafe":                       false,
	})
	assertStringSliceCapability(t, caps, []string{"ruler", "replacementContract"}, "sampledProofs", []string{
		"read-route",
		"segment-summary-full-detail",
		"position-validation-and-persisted-side-effects",
		"narrator-status-folding",
		"git-log-degraded-fallback",
		"historical-commit-fork-split",
		"frontend-ruler-lifecycle-no-crash",
		"ts-go-ruler-read-segment-position-graph-diff",
		"ruler-merged-branch-deletion",
		"ruler-trunk-dirty-merge-guard",
		"ruler-rebase-conflict-narrator-prompt",
		"ruler-airesolve-fallback-payload-contract",
		"ruler-mutation-lifecycle-db-git-graph-focused-proof",
		"ruler-temp-chapter-ai-fallback-contract",
		"ts-go-ruler-merge-lifecycle-db-git-graph-diff",
	})
	assertStringSliceCapability(t, caps, []string{"ruler", "replacementContract"}, "requiredProofs", []string{
		"temp-chapter-ai-conflict-resolution",
		"full-ts-ruler-lifecycle-parity",
	})
	assertStringSliceCapability(t, caps, []string{"ruler"}, "sampledProofs", []string{
		"read-route",
		"segment-summary-full-detail",
		"position-validation-and-persisted-side-effects",
		"narrator-status-folding",
		"git-log-degraded-fallback",
		"historical-commit-fork-split",
		"frontend-ruler-lifecycle-no-crash",
		"ts-go-ruler-read-segment-position-graph-diff",
		"ruler-merged-branch-deletion",
		"ruler-trunk-dirty-merge-guard",
		"ruler-rebase-conflict-narrator-prompt",
		"ruler-airesolve-fallback-payload-contract",
		"ruler-mutation-lifecycle-db-git-graph-focused-proof",
		"ruler-temp-chapter-ai-fallback-contract",
		"ts-go-ruler-merge-lifecycle-db-git-graph-diff",
	})
	assertStringSliceCapability(t, caps, []string{"ruler"}, "requiredProofs", []string{
		"temp-chapter-ai-conflict-resolution",
		"full-ts-ruler-lifecycle-parity",
	})

	assertNestedCapability(t, caps, []string{"volumeSnapshots"}, map[string]any{
		"supported":                 true,
		"archiveRuntime":            "podman-tar",
		"createArchive":             true,
		"applyArchive":              true,
		"deleteArchive":             true,
		"frontendChapterId":         true,
		"legacySourceChapterId":     true,
		"podmanRequired":            true,
		"podmanMissingFallbackCode": "PODMAN_NOT_FOUND",
		"fallbackHTTPStatus":        float64(http.StatusNotImplemented),
		"fallbackBeforeDBMutation":  true,
		"unsafePathValidation":      true,
		"projectChapterGuard":       true,
		"missingApplyBeforeRuntime": true,
		"maxArchiveBytes":           float64(1 << 30),
		"commandTimeoutSeconds":     float64(300),
		"commandOutputLimitBytes":   float64(64 * 1024),
		"sizeLimitCode":             "SNAPSHOT_TOO_LARGE",
		"eventParity":               "go-events-disabled-ts-events-documented",
		"tsEventParity":             false,
		"concurrencyLocking":        false,
		"frontendPodmanSmoke":       false,
		"frontendFallbackSmoke":     "route-contract-only",
	})
	assertNestedCapability(t, caps, []string{"volumeSnapshots", "routeContract"}, map[string]any{"auth": "api-authenticated", "responseDirectArray": true, "gitGuarded": false, "listSort": "createdAt-desc"})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "routeContract"}, "projectScopedRoutes", []string{"GET /api/projects/{projectId}/volume-snapshots", "POST /api/projects/{projectId}/volume-snapshots"})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "routeContract"}, "rootRoutes", []string{"GET /api/volume-snapshots/{id}", "PATCH /api/volume-snapshots/{id}", "DELETE /api/volume-snapshots/{id}", "POST /api/volume-snapshots/{id}/apply", "GET /api/volume-snapshots/{id}/applications"})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "routeContract"}, "listFilters", []string{"serviceName", "containerPath"})
	assertNestedCapability(t, caps, []string{"volumeSnapshots", "requestContract"}, map[string]any{"createLegacyAlias": "sourceChapterId", "chapterIdPrecedence": true, "nullableDescriptionOnUpdate": true, "nameMaxChars": float64(200), "serviceNameMaxChars": float64(200), "descriptionMaxChars": float64(2000)})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "requestContract"}, "createRequiredFields", []string{"chapterId", "serviceName", "containerPath", "name"})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "requestContract"}, "applyRequiredFields", []string{"targetChapterId"})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "requestContract"}, "updateFields", []string{"name", "description"})
	assertNestedCapability(t, caps, []string{"volumeSnapshots", "responseContract"}, map[string]any{"missingGetStatus": float64(http.StatusNotFound), "missingApplyStatus": float64(http.StatusNotFound)})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "responseContract"}, "snapshotFields", []string{"id", "projectId", "name", "description", "sourceChapterId", "serviceName", "containerPath", "sizeBytes", "createdBy", "createdAt", "updatedAt"})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "responseContract"}, "applicationsFields", []string{"id", "snapshotId", "chapterId", "appliedAt", "appliedBy"})
	assertNestedCapability(t, caps, []string{"volumeSnapshots", "runtimeContract"}, map[string]any{"implementation": "host-streamed-podman-exec-tar", "tsImplementation": "container-tempfile-plus-podman-cp", "requiresRunningContainer": true, "archiveStoredOnHost": true, "partialArchiveCleanup": true, "deleteIgnoresMissingFile": true, "commandTimeoutSeconds": float64(300), "cleanupTimeoutSeconds": float64(10), "outputLimitBytes": float64(64 * 1024)})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "runtimeContract"}, "applyCommands", []string{"podman exec <container> mkdir -p <path>", "podman exec -i <container> tar xzf - -C <path>"})
	assertNestedCapability(t, caps, []string{"volumeSnapshots", "fallbackContract"}, map[string]any{"podmanMissingStatus": float64(http.StatusNotImplemented), "podmanMissingCode": "PODMAN_NOT_FOUND", "createValidatesBeforePodman": true, "applyMissingBeforePodman": true, "fallbackBeforeDBMutation": true, "sizeLimitStatus": float64(http.StatusRequestEntityTooLarge), "sizeLimitCode": "SNAPSHOT_TOO_LARGE", "unsafePathStatus": float64(http.StatusBadRequest)})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "fallbackContract"}, "podmanMissingShape", []string{"ok", "supported", "fallback", "code", "reason"})
	assertNestedCapability(t, caps, []string{"volumeSnapshots", "eventContract"}, map[string]any{"goEventParity": false, "frontendRefresh": "react-query-invalidation-after-mutation"})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "eventContract"}, "tsEvents", []string{"volume-snapshot:creating", "volume-snapshot:created", "volume-snapshot:applying", "volume-snapshot:applied", "volume-snapshot:deleted", "volume-snapshot:error"})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "eventContract"}, "goBroadcasts", []string{})
	assertNestedCapability(t, caps, []string{"volumeSnapshots", "lockingContract"}, map[string]any{"tsContainerLock": true, "tsSnapshotDeleteLock": true, "goContainerLock": false, "goSnapshotDeleteLock": false, "deleteVsApplyRaceGuard": false})
	assertNestedCapability(t, caps, []string{"volumeSnapshots", "frontendSmokeContract"}, map[string]any{
		"boundaryTracked":                  true,
		"runtimeRow":                       "RUNTIME-SNAPSHOT-01",
		"capabilityGateRequired":           true,
		"routeContractOnly":                true,
		"sampledReadMissingState":          true,
		"noPodmanFallbackPayload":          true,
		"noMisleadingSuccessToastSmoke":    false,
		"reactQueryInvalidationSmoke":      false,
		"successFallbackErrorBrowserSmoke": false,
		"realPodmanFrontendSmoke":          false,
		"eventDrivenRefreshParity":         false,
		"fullTSFrontendParity":             false,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "frontendSmokeContract"}, "frontendConsumers", []string{
		"frontend/lib/api/projects.ts",
		"frontend/hooks/useVolumeSnapshots.ts",
	})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "frontendSmokeContract"}, "sampledProofs", []string{
		"seeded-list-detail-update-applications-missing-state",
		"missing-apply-before-podman-fallback",
		"no-podman-501-before-db-mutation",
	})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "frontendSmokeContract"}, "requiredProofs", []string{
		"frontend-success-toast-and-react-query-invalidation-smoke",
		"frontend-podman-missing-fallback-no-success-toast-smoke",
		"frontend-error-state-no-crash-smoke",
		"real-podman-create-apply-delete-browser-smoke",
		"event-or-explicit-poll-refresh-parity-proof",
	})
	assertNestedCapability(t, caps, []string{"volumeSnapshots", "frontendSmokeContract", "replacementContract"}, map[string]any{
		"boundaryTracked":                  true,
		"runtimeRow":                       "RUNTIME-SNAPSHOT-FRONTEND-SMOKE-01",
		"capabilityGateRequired":           true,
		"routeContractOnlyProof":           true,
		"sampledReadMissingStateProof":     true,
		"noPodmanFallbackPayloadProof":     true,
		"noMisleadingSuccessToastSmoke":    false,
		"reactQueryInvalidationSmoke":      false,
		"successFallbackErrorBrowserSmoke": false,
		"realPodmanFrontendSmoke":          false,
		"eventDrivenRefreshParity":         false,
		"fullTSFrontendParity":             false,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "frontendSmokeContract", "replacementContract"}, "frontendConsumers", []string{
		"frontend/lib/api/projects.ts",
		"frontend/hooks/useVolumeSnapshots.ts",
	})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "frontendSmokeContract", "replacementContract"}, "sampledProofs", []string{
		"seeded-list-detail-update-applications-missing-state",
		"missing-apply-before-podman-fallback",
		"no-podman-501-before-db-mutation",
	})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "frontendSmokeContract", "replacementContract"}, "requiredProofs", []string{
		"frontend-success-toast-and-react-query-invalidation-smoke",
		"frontend-podman-missing-fallback-no-success-toast-smoke",
		"frontend-error-state-no-crash-smoke",
		"real-podman-create-apply-delete-browser-smoke",
		"event-or-explicit-poll-refresh-parity-proof",
		"full-ts-frontend-volume-snapshot-parity",
	})
	assertNestedCapability(t, caps, []string{"volumeSnapshots", "replacementContract"}, map[string]any{
		"boundaryTracked":                   true,
		"runtimeRow":                        "RUNTIME-SNAPSHOT-01",
		"routeContractProof":                true,
		"requestResponseContractProof":      true,
		"boundedPodmanTarRuntimeProof":      true,
		"noPodmanFallbackProof":             true,
		"unsafePathAndSizeLimitProof":       true,
		"frontendSmokeBoundaryProof":        true,
		"tsPodmanCPRuntimeParity":           false,
		"tsEventParity":                     false,
		"snapshotMutexParity":               false,
		"realPodmanBrowserSmoke":            false,
		"frontendSuccessFallbackErrorSmoke": false,
		"eventDrivenRefreshParity":          false,
		"fullTSVolumeSnapshotParity":        false,
		"replacementSafe":                   false,
	})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "replacementContract"}, "sampledProofs", []string{
		"volume-snapshot-route-request-response-contract",
		"host-streamed-podman-tar-runtime-contract",
		"no-podman-and-size-path-fallback-contract",
		"seeded-list-detail-update-applications-missing-state",
		"frontend-route-contract-only-boundary",
	})
	assertStringSliceCapability(t, caps, []string{"volumeSnapshots", "replacementContract"}, "requiredProofs", []string{
		"ts-tempfile-podman-cp-runtime-diff-or-accepted-decision",
		"ts-volume-snapshot-event-family-parity",
		"snapshot-container-and-delete-apply-mutex-parity",
		"real-podman-create-apply-delete-browser-smoke",
		"frontend-success-fallback-error-no-misleading-toast-smoke",
		"react-query-invalidation-or-event-driven-refresh-parity-proof",
		"ts-go-volume-snapshot-runtime-e2e-diff",
	})
	assertNestedCapability(t, caps, []string{"vnet"}, map[string]any{"supported": true, "ws": true, "mode": "relay", "peerCleanup": true, "udpRendezvous": false, "udpRendezvousReason": "Go VNet compatibility mode relays WebSocket peer traffic and does not expose UDP rendezvous."})
	assertNestedCapability(t, caps, []string{"vnet", "routeContract"}, map[string]any{"statusRoute": "GET /api/vnet/status", "websocketRoute": "GET /ws/vnet", "statusAuth": "api-authenticated", "statusGitGuarded": false, "adminOnly": false, "nonUpgradeStatusJSON": true, "goDisabledWSBehavior": "upgrade-then-error-close", "tsDisabledWSBehavior": "reject-upgrade-401"})
	assertStringSliceCapability(t, caps, []string{"vnet", "routeContract"}, "websocketAuthModes", []string{"jwt-bearer", "jwt-query-token", "relay-token", "anonymous-when-enabled"})
	assertNestedCapability(t, caps, []string{"vnet", "statusContract"}, map[string]any{"statusDisabledHTTP": float64(http.StatusOK), "wsNonUpgradeStatus": float64(http.StatusOK), "wsDisabledHTTP": float64(http.StatusServiceUnavailable)})
	assertStringSliceCapability(t, caps, []string{"vnet", "statusContract"}, "fields", []string{"ok", "supported", "fallback", "mode", "ws", "peerCleanup", "udpRendezvous", "networks", "peers", "connections", "maxPeersPerNetwork", "maxMessageBytes", "relay", "udp", "network"})
	assertStringSliceCapability(t, caps, []string{"vnet", "statusContract"}, "relayFields", []string{"clients", "rooms", "peers", "running"})
	assertStringSliceCapability(t, caps, []string{"vnet", "statusContract"}, "networkFields", []string{"hostname", "addresses"})
	assertStringSliceCapability(t, caps, []string{"vnet", "statusContract"}, "disabledFields", []string{"ok", "supported", "fallback", "mode", "ws", "peerCleanup", "udpRendezvous", "code", "feature", "reason"})
	assertNestedCapability(t, caps, []string{"vnet", "relayContract"}, map[string]any{
		"mode":                       "websocket-relay",
		"ws":                         true,
		"relayPackets":               true,
		"peerJoinLeaveEvents":        true,
		"peerCleanup":                true,
		"httpStatsWhenNotUpgrade":    true,
		"jwtAuth":                    true,
		"relayTokenAuth":             true,
		"anonymousRelayConfigurable": true,
		"maxMessageBytes":            true,
		"maxPeersPerNetwork":         true,
		"disabledSettingFallback":    "ws-error-vnet-disabled",
		"disabledHTTPStatus":         float64(http.StatusServiceUnavailable),
		"udpRendezvous":              false,
		"udpDataplane":               false,
		"tsUDPRendezvousParity":      false,
		"frontendRelaySmoke":         true,
		"frontendUDPSmoke":           false,
	})
	assertNestedCapability(t, caps, []string{"vnet", "messageContract"}, map[string]any{"punchRequestResponse": "punch_offer", "peerReplacementClose": float64(1000)})
	assertStringSliceCapability(t, caps, []string{"vnet", "messageContract"}, "clientMessageTypes", []string{"hello", "relay_packet", "punch_request", "pong", "ping"})
	assertStringSliceCapability(t, caps, []string{"vnet", "messageContract"}, "helloRequiredFields", []string{"networkId", "peerId", "virtualIp"})
	assertStringSliceCapability(t, caps, []string{"vnet", "messageContract"}, "welcomeFields", []string{"type", "networkId", "peerId", "sessionId", "udpAuthToken", "udp", "peers"})
	assertStringSliceCapability(t, caps, []string{"vnet", "messageContract"}, "serverMessageTypes", []string{"welcome", "peer_joined", "peer_left", "relay_packet", "punch_offer", "pong", "error"})
	assertStringSliceCapability(t, caps, []string{"vnet", "messageContract"}, "relayPacketAddsFields", []string{"fromPeerId", "srcIp", "dstIp"})
	assertNestedCapability(t, caps, []string{"vnet", "limitsContract"}, map[string]any{"defaultMaxPeersPerNetwork": float64(64), "settingsMaxPeersPerNetwork": float64(1024), "defaultMaxMessageBytes": float64(1024 * 1024), "settingsMaxMessageBytes": float64(64 * 1024 * 1024), "websocketReadLimitBytes": float64(1024 * 1024), "rawOversizeMayCloseBeforeJSONErr": true})
	assertNestedCapability(t, caps, []string{"vnet", "fallbackContract"}, map[string]any{"unauthorizedStatus": float64(http.StatusUnauthorized), "disabledWSCode": "VNET_DISABLED", "tsDisabledWSParity": false, "fullTSVNetRuntimeParity": false, "udpStatusReflectsSettings": true, "udpRuntimeImplemented": false})
	assertStringSliceCapability(t, caps, []string{"vnet", "fallbackContract"}, "disabledWSFields", []string{"type", "ok", "supported", "fallback", "code", "feature", "reason", "error", "message"})
	assertStringSliceCapability(t, caps, []string{"vnet", "fallbackContract"}, "relayErrorFields", []string{"type", "code", "message"})
	assertStringSliceCapability(t, caps, []string{"vnet", "fallbackContract"}, "relayErrorCodes", []string{"NOT_ATTACHED", "MESSAGE_TOO_LARGE", "INVALID_MESSAGE", "PEER_REPLACED", "VIRTUAL_IP_IN_USE", "NETWORK_FULL", "NOT_REGISTERED", "PEER_NOT_FOUND", "INVALID_SOURCE_IP", "INVALID_DESTINATION_IP"})
	assertNestedCapability(t, caps, []string{"vnet", "replacementContract"}, map[string]any{
		"boundaryTracked":            true,
		"runtimeRow":                 "VNET-RELAY-01",
		"relayRuntimeProof":          true,
		"statusRouteContractProof":   true,
		"messageContractProof":       true,
		"limitsContractProof":        true,
		"disabledFallbackProof":      true,
		"stableRelayErrorCodesProof": true,
		"tsDisabledWSParity":         false,
		"udpRendezvousParity":        false,
		"udpDataplaneParity":         false,
		"frontendUDPSmoke":           false,
		"rawFrameLimitParity":        false,
		"fullTSVNetRuntimeParity":    false,
		"replacementSafe":            false,
	})
	assertStringSliceCapability(t, caps, []string{"vnet", "replacementContract"}, "sampledProofs", []string{
		"vnet-status-route-auth-and-fields",
		"websocket-relay-auth-modes-and-peer-cleanup",
		"relay-message-contract-and-stable-error-codes",
		"bounded-peer-message-limits",
		"disabled-setting-fallback-payload",
	})
	assertStringSliceCapability(t, caps, []string{"vnet", "replacementContract"}, "requiredProofs", []string{
		"ts-disabled-websocket-upgrade-parity-or-accepted-drift",
		"udp-rendezvous-dataplane-runtime-or-product-deferral",
		"frontend-relay-and-udp-degraded-state-smoke",
		"raw-frame-limit-and-large-message-ts-go-diff",
		"ts-go-vnet-runtime-e2e-diff",
	})
	assertNestedCapability(t, caps, []string{"update"}, map[string]any{"manualOnly": true, "selfUpdateAvailable": false, "canAutoRestart": false})
	assertNestedCapability(t, caps, []string{"update", "routes"}, map[string]any{
		"check":     "GET /api/update/check",
		"version":   "GET /api/update/version",
		"directory": "GET /api/update/directory",
		"status":    "GET /api/update/status",
		"cleanup":   "POST /api/update/cleanup",
		"download":  "POST /api/update/download",
		"apply":     "POST /api/update/apply",
	})
	assertNestedCapability(t, caps, []string{"update", "authContract"}, map[string]any{
		"check":               "authenticated",
		"version":             "authenticated",
		"directory":           "admin",
		"status":              "admin",
		"cleanup":             "admin",
		"download":            "admin-sse-or-json-preflight",
		"apply":               "admin",
		"regularDeniedStatus": float64(403),
	})
	assertNestedCapability(t, caps, []string{"update", "checkContract"}, map[string]any{
		"timeoutSeconds":                 float64(5),
		"trustedSourceRequiredForRemote": true,
		"defaultNoSourceReason":          "settings.update.serverUrl is not configured",
		"failureStatus":                  float64(200),
		"failurePreservesLatestVersion":  true,
		"devBuildNoDownload":             true,
	})
	assertStringSliceCapability(t, caps, []string{"update", "checkContract"}, "trustedSourceMetadata", []string{"product", "channel"})
	assertStringSliceCapability(t, caps, []string{"update", "checkContract"}, "responseFields", []string{"updateAvailable", "currentVersion", "latestVersion", "releaseInfo", "source", "selfUpdateAvailable", "canAutoRestart", "manualOnly", "instructions"})
	assertStringSliceCapability(t, caps, []string{"update", "checkContract"}, "availableFields", []string{"downloadSize", "totalSize", "strategy", "patchChain"})
	assertStringSliceCapability(t, caps, []string{"update", "checkContract"}, "releaseInfoFields", []string{"version", "releaseDate", "path", "sha512", "files"})
	assertStringSliceCapability(t, caps, []string{"update", "versionContract"}, "responseFields", []string{"version", "commit", "backend", "buildChannel", "platform", "arch", "goVersion", "selfUpdateAvailable", "manualOnly", "canAutoRestart"})
	assertNestedCapability(t, caps, []string{"update", "directoryContract"}, map[string]any{"adminOnly": true, "baseDirectory": "~/.narrafork/updates", "manualOnlyDefault": true})
	assertStringSliceCapability(t, caps, []string{"update", "directoryContract"}, "responseFields", []string{"directory", "selfUpdateAvailable", "canAutoRestart", "manualOnly", "instructions"})
	assertNestedCapability(t, caps, []string{"update", "statusContract"}, map[string]any{"adminOnly": true, "versionQuery": true, "metadataFile": "metadata.json", "legacyArtifactFallback": true, "manualOnlyDefault": true, "autoReadyRequiresRestartHandoff": true})
	assertStringSliceCapability(t, caps, []string{"update", "statusContract"}, "responseFields", []string{"ready", "downloaded", "version", "directory", "canAutoRestart", "placed", "selfUpdateAvailable", "manualOnly", "instructions"})
	assertStringSliceCapability(t, caps, []string{"update", "statusContract"}, "readyFields", []string{"updateFile", "updatePath", "artifactPath", "files"})
	assertNestedCapability(t, caps, []string{"update", "cleanupContract"}, map[string]any{"adminOnly": true, "optionalVersion": true, "safePathGuard": true, "baseDirectoryNotRemoved": true, "oldEntryMaxAgeHours": float64(168)})
	assertStringSliceCapability(t, caps, []string{"update", "cleanupContract"}, "responseFields", []string{"success", "removed", "directory"})
	assertNestedCapability(t, caps, []string{"update", "download"}, map[string]any{"supported": true, "sse": true, "sha512": true, "maxBytes": float64(512 * 1024 * 1024), "trustMode": "server-configured-trusted-source"})
	assertNestedCapability(t, caps, []string{"update", "downloadContract"}, map[string]any{
		"supported":                         true,
		"adminOnly":                         true,
		"sseProgress":                       true,
		"terminalErrorEvent":                true,
		"terminalCompleteEvent":             true,
		"jsonPreflightBeforeSSE":            true,
		"noUpdateStatus":                    float64(404),
		"noUpdateJSONError":                 "No update available",
		"requestedVersionMismatchStatus":    float64(409),
		"ignoresClientSuppliedArtifactURLs": true,
		"refetchesTrustedMetadata":          true,
		"sha512MetadataRequired":            true,
		"sha512HexOrBase64":                 true,
		"sha512MismatchCleansArtifact":      true,
		"trustedSourceRequired":             true,
		"devBuildNoDownload":                true,
		"contextTimeoutSeconds":             float64(600),
		"maxBytes":                          float64(512 * 1024 * 1024),
		"metadataFile":                      "metadata.json",
	})
	assertStringSliceCapability(t, caps, []string{"update", "downloadContract"}, "sseEvents", []string{"progress", "complete", "error"})
	assertStringSliceCapability(t, caps, []string{"update", "downloadContract"}, "normalEventOrder", []string{"progress:downloading", "complete"})
	assertStringSliceCapability(t, caps, []string{"update", "downloadContract"}, "requestedVersionMismatchFields", []string{"error", "requestedVersion", "latestVersion"})
	assertStringSliceCapability(t, caps, []string{"update", "downloadContract"}, "completeFields", []string{"success", "version", "downloaded", "ready", "bytes", "updatePath", "artifactPath", "directory", "files", "placed", "selfUpdateAvailable", "canAutoRestart", "manualOnly", "instructions"})
	assertStringSliceCapability(t, caps, []string{"update", "downloadContract"}, "errorFields", []string{"success", "code", "version", "reason", "error", "message", "selfUpdateAvailable", "canAutoRestart", "manualOnly", "instructions"})
	assertNestedCapability(t, caps, []string{"update", "successFixtureContract"}, map[string]any{
		"boundaryTracked":                   true,
		"localTrustedArtifactDownloadSmoke": true,
		"compiledGoArtifactDownloadSmoke":   false,
		"zstdPatchFixtureSmoke":             false,
		"patchChainFixtureSmoke":            false,
		"autoApplyHandoffSmoke":             false,
		"rollbackDrillCIWired":              false,
		"fullTSSelfUpdateParity":            false,
		"releaseUploadPolicy":               "metadata-plus-zstd-patch-only",
	})
	assertStringSliceCapability(t, caps, []string{"update", "successFixtureContract"}, "requiredProofs", []string{
		"compiled-go-artifact-download-sse",
		"zstd-patch-metadata-and-patch-chain-fixture",
		"frontend-success-state-browser-smoke",
		"auto-apply-restart-handoff-smoke",
		"ts-go-ts-rollback-drill-ci-report",
	})
	assertStringSliceCapability(t, caps, []string{"update", "successFixtureContract"}, "sampledProofs", []string{
		"admin-boundary",
		"trusted-check-metadata",
		"trusted-download-error-sse",
		"local-trusted-artifact-download",
		"apply-fallback-manual-only",
	})
	assertNestedCapability(t, caps, []string{"update", "replacementContract"}, map[string]any{
		"boundaryTracked":                   true,
		"runtimeRow":                        "UPDATE-SELF-APPLY-01",
		"manualOnly":                        true,
		"selfUpdateAvailable":               false,
		"downloadRouteContractProof":        true,
		"applyRouteFallbackProof":           true,
		"localTrustedArtifactDownloadSmoke": true,
		"localApplyFallbackSmoke":           true,
		"compiledGoArtifactDownloadSmoke":   false,
		"zstdPatchFixtureSmoke":             false,
		"patchChainFixtureSmoke":            false,
		"frontendSuccessStateSmoke":         false,
		"autoApplyHandoffSmoke":             false,
		"rollbackDrillCIWired":              false,
		"fullTSSelfUpdateParity":            false,
		"replacementSafe":                   false,
	})
	assertStringSliceCapability(t, caps, []string{"update", "replacementContract"}, "sampledProofs", []string{
		"update-route-auth-and-admin-boundary",
		"trusted-check-metadata-shape",
		"trusted-download-error-sse-shape",
		"local-trusted-artifact-download",
		"manual-only-apply-fallback",
	})
	assertStringSliceCapability(t, caps, []string{"update", "replacementContract"}, "requiredProofs", []string{
		"compiled-go-artifact-download-sse",
		"zstd-patch-metadata-and-patch-chain-fixture",
		"frontend-update-success-state-browser-smoke",
		"auto-apply-restart-handoff-smoke",
		"ts-go-ts-rollback-drill-ci-report",
		"release-upload-artifact-policy-decision",
	})
	assertNestedCapability(t, caps, []string{"update", "apply"}, map[string]any{"supported": false, "fallback": true, "code": "FEATURE_DISABLED", "reason": "Automatic self-update requires a configured restart handoff manager and a regular current executable."})
	assertNestedCapability(t, caps, []string{"update", "applyContract"}, map[string]any{
		"supported":                           false,
		"manualOnlyWhenUnavailable":           true,
		"requiresRestartHandoffManager":       true,
		"requiresRegularExecutable":           true,
		"requiresTrustedDownloadedArtifact":   true,
		"requiresSha512Verification":          true,
		"preparedArtifactRequired":            true,
		"preparedBinaryPrefix":                ".narrafork-update-",
		"verifiesArtifactBeforeHandoff":       true,
		"startsReplacementProcess":            false,
		"usesGracefulRestartHandoff":          false,
		"cancelsRestartSessionOnStartFailure": true,
		"fallbackCode":                        "FEATURE_DISABLED",
		"autoApplySmoke":                      false,
		"artifactPathTraversalGuard":          true,
		"artifactRegularFileRequired":         true,
		"artifactSizeVerified":                true,
	})
	assertStringSliceCapability(t, caps, []string{"update", "applyContract"}, "fallbackResponseFields", []string{"success", "serverRestarting", "restarting", "canAutoRestart", "selfUpdateAvailable", "manualOnly", "message", "instructions"})
	assertStringSliceCapability(t, caps, []string{"update", "applyContract"}, "successResponseFields", []string{"success", "serverRestarting", "restarting", "ready", "placed", "canAutoRestart", "selfUpdateAvailable", "manualOnly", "instructions", "message", "version", "newBinaryPath", "replacementPid"})
	assertStringSliceCapability(t, caps, []string{"update", "applyContract"}, "failureResponseFields", []string{"success", "serverRestarting", "restarting", "canAutoRestart", "selfUpdateAvailable", "manualOnly", "error", "message"})
	assertNestedCapability(t, caps, []string{"gateway"}, map[string]any{
		"persistentRuntimes":           false,
		"persistentRuntimeRoutes":      true,
		"persistentRuntimeAttached":    false,
		"mode":                         "fallback-plus-http-webhook",
		"fullTSGatewayParity":          false,
		"statusFallbackWhenUnattached": true,
		"reloadFallbackWhenUnattached": true,
	})
	assertNestedCapability(t, caps, []string{"gateway", "routeContract"}, map[string]any{
		"publicWebhook":       "POST /api/gateway/webhook",
		"authenticatedStatus": "GET /api/gateway/status",
		"authenticatedReload": "POST /api/gateway/reload",
		"publicWebhookAuth":   false,
		"privateRoutesAuth":   true,
		"gitGuarded":          false,
	})
	assertStringSliceCapability(t, caps, []string{"gateway", "routeContract"}, "authenticatedSessions", []string{"GET /api/gateway/sessions", "DELETE /api/gateway/sessions/{id}"})
	assertStringSliceCapability(t, caps, []string{"gateway", "routeContract"}, "authenticatedWeixinQr", []string{"POST /api/gateway/weixin/qr-start", "GET /api/gateway/weixin/qr-poll"})
	assertNestedCapability(t, caps, []string{"gateway", "runtimeContract"}, map[string]any{
		"persistentRuntimeRoutes":      true,
		"persistentRuntimeAttached":    false,
		"statusDelegatesWhenAttached":  true,
		"reloadDelegatesWhenAttached":  true,
		"statusRouteFallbackPayload":   true,
		"reloadRouteFallbackPayload":   true,
		"reloadFiltersPlatforms":       false,
		"unsupportedPlatformsFiltered": true,
		"fullTSGatewayParity":          false,
		"fallbackReason":               "Persistent gateway runtimes are not bundled in go_backend",
	})
	assertNestedCapability(t, caps, []string{"gateway", "replacementContract"}, map[string]any{
		"boundaryTracked":             true,
		"persistentRuntimeRequired":   true,
		"persistentRuntimeAttached":   false,
		"webhookCompatibilityOnly":    true,
		"statusReloadFallbackSafe":    true,
		"frontendGatewaySectionSmoke": false,
		"attachedRuntimeSmoke":        false,
		"multiPlatformRuntimeParity":  false,
		"qrLoginRuntimeParity":        false,
		"sessionRoutingRuntimeParity": false,
		"fullTSGatewayParity":         false,
		"replacementSafe":             false,
	})
	assertStringSliceCapability(t, caps, []string{"gateway", "replacementContract"}, "sampledProofs", []string{
		"status-reload-fallback-envelope",
		"public-hmac-webhook-fallback-diagnostics",
		"weixin-qr-501-json-fallback",
	})
	assertStringSliceCapability(t, caps, []string{"gateway", "replacementContract"}, "requiredProofs", []string{
		"attached-gateway-runtime-status-reload-smoke",
		"frontend-gateway-section-disabled-fallback-smoke",
		"telegram-runtime-session-routing-smoke",
		"qr-login-runtime-or-disabled-product-decision",
		"ts-go-gateway-session-webhook-diff-smoke",
	})
	assertNestedCapability(t, caps, []string{"gateway", "statusContract"}, map[string]any{
		"fallbackStatus":               float64(200),
		"fallbackMode":                 "fallback",
		"fallbackSupported":            false,
		"sessionsRead":                 true,
		"configuredUsersCount":         true,
		"degradedFallbacks":            true,
		"webhookSubstatusReasonFields": true,
	})
	assertStringSliceCapability(t, caps, []string{"gateway", "statusContract"}, "fallbackFields", []string{"started", "platforms", "sessions", "configuredUsers", "mode", "supported", "fallback", "reason", "error", "message", "webhook"})
	assertStringSliceCapability(t, caps, []string{"gateway", "statusContract"}, "queryFailureFallbacks", []string{"gateway_sessions_query_failed", "gateway_user_preferences_table_check_failed", "gateway_configured_users_count_failed"})
	assertNestedCapability(t, caps, []string{"gateway", "reloadContract"}, map[string]any{"fallbackStatus": float64(200), "fallbackOk": true, "fallbackReloadedEmpty": true, "statusPayload": true, "webhookSubstatus": true, "reasonErrorMessage": true})
	assertNestedCapability(t, caps, []string{"gateway", "sessionContract"}, map[string]any{"listReturnsArray": true, "deleteReturnsOkAndDeleted": true, "missingStorageDeleteReturnsOkFalse": true, "storageTable": "gateway_session_mappings"})
	assertNestedCapability(t, caps, []string{"gateway", "webhook"}, map[string]any{
		"supported":                    true,
		"fallback":                     false,
		"mode":                         "http-hmac-autocreate-standalone-narrator",
		"publicRoute":                  true,
		"maxBodyBytes":                 float64(1048576),
		"requiresSecret":               true,
		"requiresNarratorRuntime":      true,
		"narratorRuntimeAttached":      true,
		"notConfiguredCode":            "GATEWAY_WEBHOOK_NOT_CONFIGURED",
		"invalidSignatureCode":         "GATEWAY_WEBHOOK_INVALID_SIGNATURE",
		"missingTextCode":              "GATEWAY_WEBHOOK_MISSING_TEXT",
		"runtimeUnavailableCode":       "GATEWAY_WEBHOOK_RUNTIME_UNAVAILABLE",
		"storageUnavailableCode":       "GATEWAY_WEBHOOK_STORAGE_UNAVAILABLE",
		"staleMappingCode":             "GATEWAY_WEBHOOK_STALE_MAPPING",
		"acceptedFallbackStatus":       float64(202),
		"autocreateStandaloneNarrator": true,
		"standaloneTitlePrefix":        "Webhook: ",
		"messageCreatorPrefix":         "gateway:",
		"sessionMappingTable":          "gateway_session_mappings",
		"updatesLastMessageAt":         true,
	})
	assertStringSliceCapability(t, caps, []string{"gateway", "webhook"}, "signatureHeaders", []string{"X-Hub-Signature-256", "X-Webhook-Signature"})
	assertNestedCapability(t, caps, []string{"gateway", "weixinQr"}, map[string]any{"supported": false, "fallback": true, "code": "WEIXIN_QR_UNSUPPORTED", "status": "unavailable", "httpStatus": float64(501), "qrcodeUrlEmpty": true, "qrcodeTokenEmpty": true, "rejectBeforeRuntime": true, "reason": "Weixin QR gateway runtime is not bundled in go_backend."})
	assertStringSliceCapability(t, caps, []string{"gateway", "weixinQr"}, "startResponseFields", []string{"ok", "status", "supported", "fallback", "code", "qrcodeUrl", "qrcodeToken", "reason", "error", "message"})
	assertStringSliceCapability(t, caps, []string{"gateway", "weixinQr"}, "pollResponseFields", []string{"ok", "status", "supported", "fallback", "code", "reason", "error", "message"})
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
	assertNestedCapability(t, caps, []string{"storage", "scan"}, map[string]any{"supported": true, "readOnly": true, "adminOnly": true, "sse": true, "cache": true, "dbScanMode": "approximate", "containersCategoryRuntimeProbe": false, "tsStorageServiceParity": "sampled-sse-contract-not-full-runtime-smoke"})
	assertNestedCapability(t, caps, []string{"storage", "scan", "routes"}, map[string]any{"scan": true, "cached": true})
	assertStringSliceCapability(t, caps, []string{"storage", "scan"}, "events", []string{"progress", "category", "complete", "error"})
	assertStringSliceCapability(t, caps, []string{"storage", "scan"}, "normalEventOrder", []string{"progress:scanning_database", "category:database", "progress:scanning_uploads", "category:uploads", "progress:scanning_shares", "category:shares", "progress:scanning_worktrees", "category:worktrees", "progress:scanning_containers", "category:containers", "complete"})
	assertStringSliceCapability(t, caps, []string{"storage", "scan"}, "categories", []string{"database", "uploads", "shares", "worktrees", "containers"})
	assertStringSliceCapability(t, caps, []string{"storage", "scan"}, "completeFields", []string{"categories", "totalBytes", "scannedAt"})
	assertNestedCapability(t, caps, []string{"storage", "scan", "cacheResponse", "empty"}, map[string]any{"cached": false})
	assertNestedCapability(t, caps, []string{"storage", "scan", "cacheResponse", "afterComplete"}, map[string]any{"cached": true, "data": "last-complete-payload"})
	assertNestedCapability(t, caps, []string{"storage", "scan", "limits"}, map[string]any{"maxStorageScanEntries": float64(5000), "maxStorageProjectRows": float64(100), "maxDatabaseSummaryTables": float64(50)})
	assertNestedCapability(t, caps, []string{"storage", "cached"}, map[string]any{"supported": true, "cache": true})
	assertNestedCapability(t, caps, []string{"storage", "database"}, map[string]any{"preview": true, "cleanup": true})
	assertNestedCapability(t, caps, []string{"storage", "database", "routeContract"}, map[string]any{"adminOnly": true})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "routeContract"}, "routes", []string{"POST /api/storage/database/preview", "POST /api/storage/database/cleanup", "POST /api/storage/database/vacuum"})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "routeContract"}, "targets", []string{"archivedSessions", "staleSessions", "apiRequestDumps"})
	assertNestedCapability(t, caps, []string{"storage", "database", "requestContract"}, map[string]any{"previewOptionalBody": true, "cleanupOptionalBody": true, "defaultSampleLimit": float64(10), "tsSampleLimitMax": float64(25), "goSampleLimitMaxEnforced": false, "tsOlderThanDaysMax": float64(3650), "goOlderThanDaysMaxEnforced": false, "invalidTargetStatus": float64(http.StatusBadRequest), "tsZodParity": "partial"})
	assertNestedCapability(t, caps, []string{"storage", "database", "cleanupTargets", "apiRequestDumps"}, map[string]any{
		"supported":                   true,
		"clearsRawDumpJson":           true,
		"deletesApiRequestRows":       false,
		"defaultRetentionDays":        float64(30),
		"previewSupported":            true,
		"vacuumRan":                   false,
		"fullTSDatabaseCleanupParity": false,
	})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "cleanupTargets", "apiRequestDumps"}, "warningCodes", []string{"deletesUsageHistory"})
	assertNestedCapability(t, caps, []string{"storage", "database", "cleanupTargets", "archivedSessions"}, map[string]any{
		"supported":                          true,
		"fallback":                           false,
		"archivedOnly":                       true,
		"usesNarratorDelete":                 true,
		"requiresNarratorService":            true,
		"missingNarratorServiceFallbackCode": "FEATURE_DISABLED",
		"previewSupported":                   true,
		"vacuumRan":                          false,
		"fullTSDatabaseCleanupParity":        false,
	})
	assertNestedCapability(t, caps, []string{"storage", "database", "cleanupTargets", "staleSessions"}, map[string]any{
		"supported":            false,
		"fallback":             true,
		"code":                 "FEATURE_DISABLED",
		"previewSupported":     true,
		"cleanupSupported":     false,
		"archivedOnlyPolicy":   true,
		"defaultRetentionDays": float64(90),
		"reason":               disabledStaleNarratorSessionCleanupReason,
	})
	assertNestedCapability(t, caps, []string{"storage", "database", "maintenanceContract"}, map[string]any{
		"supported":                          true,
		"previewRoute":                       true,
		"cleanupRoute":                       true,
		"apiRequestDumpsClearsRawDumpJson":   true,
		"archivedSessionsArchivedOnly":       true,
		"archivedSessionsUsesNarratorDelete": true,
		"staleSessionsCleanupSupported":      false,
		"staleSessionsPreviewSupported":      true,
		"requestPathVacuumSupported":         false,
		"requestPathVacuumStatus":            float64(409),
		"vacuumRan":                          false,
		"checkpointRan":                      false,
		"optimized":                          false,
		"mainThreadVacuumDisabled":           true,
		"fullTSDatabaseMaintenanceParity":    false,
	})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "maintenanceContract"}, "targets", []string{"archivedSessions", "staleSessions", "apiRequestDumps"})
	assertNestedCapability(t, caps, []string{"storage", "database", "previewContract"}, map[string]any{"maxNarratorIDs": float64(500), "truncatedWarning": "cleanupPreviewLimited"})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "previewContract"}, "responseFields", []string{"target", "olderThanDays", "approxBytes", "oldestAt", "counts", "blockedCount", "warningCodes", "fallbacks", "degraded", "samples", "blocked", "count"})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "previewContract"}, "countFields", []string{"sessions", "narrators", "descendantNarrators", "messages", "toolCalls", "apiRequests", "dumpsCleared"})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "previewContract"}, "sampleTypes", []string{"narrator", "apiRequest"})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "previewContract"}, "degradedFields", []string{"degraded", "fallback", "fallbacks", "warningCodes", "reason", "error", "message"})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "previewContract"}, "fallbackCodes", []string{"api_request_dump_stats_failed", "cleanup_narrator_id_query_failed", "cleanup_message_count_failed", "cleanup_tool_call_count_failed", "cleanup_api_request_count_failed", "cleanup_oldest_activity_failed"})
	assertNestedCapability(t, caps, []string{"storage", "database", "executionContract"}, map[string]any{"apiRequestDumpsMutation": "clear-raw_dump_json-only", "apiRequestRowsDeleted": false, "archivedSessionsMutation": "safe-narrator-delete-service", "staleSessionsMutation": "disabled-archived-only-policy", "cleanupInvalidatesStorageCache": true, "storageCacheInvalidationParity": "supported-cleanup-subset", "fullStorageCacheInvalidationParity": false, "vacuumRan": false})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "executionContract"}, "responseFields", []string{"ok", "target", "olderThanDays", "approxBytes", "oldestAt", "counts", "blockedCount", "warningCodes", "samples", "blocked", "count", "beforeBytes", "afterBytes", "freedBytes", "vacuumRan", "changed"})
	assertNestedCapability(t, caps, []string{"storage", "database", "fallbackContract"}, map[string]any{"featureDisabledStatus": float64(http.StatusNotImplemented), "staleSessionsFeature": "storage.database.cleanup.staleSessions", "archivedSessionsMissingNarratorFeature": "storage.database.cleanup.archivedSessions", "previewDegradesInsteadOfThrowing": true, "invalidTargetStatus": float64(http.StatusBadRequest)})
	assertStringSliceCapability(t, caps, []string{"storage", "database", "fallbackContract"}, "featureDisabledFields", []string{"ok", "code", "feature", "supported", "fallback", "reason", "error", "message"})
	assertNestedCapability(t, caps, []string{"storage", "database", "vacuum"}, map[string]any{
		"supported":                false,
		"fallback":                 true,
		"status":                   float64(409),
		"vacuumRan":                false,
		"checkpointRan":            false,
		"optimized":                false,
		"mainThreadDisabled":       true,
		"backgroundJobImplemented": false,
	})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "routeContract"}, map[string]any{"adminOnly": true, "route": "POST /api/storage/cleanup"})
	assertStringSliceCapability(t, caps, []string{"storage", "cleanup", "routeContract"}, "targets", []string{"uploads", "shares", "worktrees", "containers"})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "requestContract"}, map[string]any{"targetRequired": true, "tsZodParity": "target-enum-only", "goLooseJSON": true, "invalidTargetStatus": float64(http.StatusBadRequest), "invalidTargetMessage": "Invalid target"})
	assertStringSliceCapability(t, caps, []string{"storage", "cleanup", "responseContract"}, "uploadsFields", []string{"ok", "removed", "freedBytes"})
	assertStringSliceCapability(t, caps, []string{"storage", "cleanup", "responseContract"}, "sharesFields", []string{"ok", "removed", "freedBytes", "memoryRemoved"})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "responseContract"}, map[string]any{"uploadShareCleanupTSGoSmoke": true, "invalidTargetTSGoSmoke": true, "emptyDirectoryResponseIsStable": true, "successInvalidatesStorageCache": true})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "fallbackContract"}, map[string]any{"unsupportedStatus": float64(http.StatusNotImplemented), "worktreesCode": "STORAGE_CLEANUP_WORKTREES_UNSUPPORTED", "containersCode": "STORAGE_CLEANUP_CONTAINERS_UNSUPPORTED", "rejectBeforeWork": true})
	assertStringSliceCapability(t, caps, []string{"storage", "cleanup", "fallbackContract"}, "unsupportedFields", []string{"ok", "code", "feature", "supported", "fallback", "reason", "error", "message"})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "uploads"}, map[string]any{"supported": true, "preservesMessageImageRefs": true})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "worktrees"}, map[string]any{"supported": false, "fallback": true, "alternative": "runtime.cleanup.worktrees", "runtimeRerouteAvailable": true, "runtimeRerouteTarget": "runtime.cleanup.worktrees"})
	assertNestedCapability(t, caps, []string{"storage", "cleanup", "containers"}, map[string]any{"supported": false, "fallback": true, "alternative": "runtime.cleanup.containers", "runtimeRerouteAvailable": false, "runtimeRerouteTarget": "runtime.cleanup.containers"})
	assertNestedCapability(t, caps, []string{"storage", "replacementContract"}, map[string]any{
		"boundaryTracked":                            true,
		"runtimeRow":                                 "STORAGE-MAINTENANCE-01",
		"scanSSEContractProof":                       true,
		"databaseMaintenanceProof":                   true,
		"storageCleanupFallbackProof":                true,
		"uploadShareCleanupProof":                    true,
		"uploadShareCleanupTSGoSmoke":                true,
		"cleanupInvalidTargetTSGoSmoke":              true,
		"supportedCleanupCacheInvalidation":          true,
		"databaseCleanupCacheInvalidation":           true,
		"supportedCleanupCacheInvalidationTSGoSmoke": true,
		"adminBoundaryProof":                         true,
		"unsupportedWorktreeFallbackProof":           true,
		"unsupportedContainerFallbackProof":          true,
		"fullTSStorageScanParity":                    false,
		"fullTSStorageCleanupParity":                 false,
		"fullTSDatabaseMaintenanceParity":            false,
		"storageCacheInvalidationParity":             "supported-cleanup-subset",
		"fullStorageCacheInvalidationParity":         false,
		"worktreeStorageCleanupSupported":            false,
		"containerStorageCleanupSupported":           false,
		"staleSessionsCleanupSupported":              false,
		"requestPathVacuumSupported":                 false,
		"backgroundCompactionJob":                    false,
		"replacementSafe":                            false,
	})
	assertStringSliceCapability(t, caps, []string{"storage", "replacementContract"}, "sampledProofs", []string{
		"storage-scan-sse-order-contract",
		"regular-user-admin-boundary-probes",
		"storage-worktree-container-unsupported-fallback-fixture",
		"storage-database-vacuum-disabled-fixture",
		"upload-share-cleanup-route-contract",
		"ts-go-storage-upload-share-cleanup-smoke",
		"ts-go-storage-cleanup-invalid-target-smoke",
		"storage-cleanup-cache-invalidation-smoke",
		"storage-database-cleanup-cache-invalidation-smoke",
		"ts-go-storage-cache-invalidation-flow-smoke",
	})
	assertStringSliceCapability(t, caps, []string{"storage", "replacementContract"}, "requiredProofs", []string{
		"broader-storage-service-runtime-smoke",
		"worktree-container-storage-cleanup-product-decision",
		"stale-sessions-cleanup-product-decision",
		"full-storage-cache-invalidation-parity-smoke",
		"background-vacuum-compaction-job-or-decision",
		"full-ts-zod-storage-maintenance-parity",
	})
	assertNestedCapability(t, caps, []string{"runtime"}, map[string]any{"backend": "go", "buildChannel": "dev"})
	assertNestedCapability(t, caps, []string{"chapters", "review"}, map[string]any{
		"supported":                      true,
		"createRollback":                 true,
		"dirtyFileCopy":                  true,
		"reviewEdgeCreate":               true,
		"reviewNarratorCreate":           true,
		"seedReviewStartMessage":         true,
		"diffContextPrompt":              true,
		"concludeReviewTool":             true,
		"dismissPromoteConvertActions":   true,
		"staleMergeGuard":                true,
		"autoStartSession":               true,
		"startMode":                      "auto-start-background-session",
		"autoStartFallbackPayload":       false,
		"autoStartOptOut":                true,
		"autoStartOptOutStartMode":       "seed-message-only",
		"commitHistoryCopyParity":        true,
		"reviewCreatedEventParity":       true,
		"reviewConcludedEventParity":     true,
		"reviewWorktreeResetParity":      true,
		"fullNarratorInitRollbackParity": true,
		"frontendReviewSmoke":            true,
		"tsGoEndpointDiffSmoke":          true,
		"replacementSafe":                false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "review"}, "routes", []string{"POST /api/chapters/{id}/review", "GET /api/reviews/*", "POST /api/reviews/*"})
	assertStringSliceCapability(t, caps, []string{"chapters", "review"}, "autoStartRuntimePayloadShape", []string{"reviewRuntime"})
	assertStringSliceCapability(t, caps, []string{"chapters", "review"}, "sampledProofs", []string{
		"create-rollback",
		"dirty-file-copy",
		"review-edge-create",
		"seed-review-start-message",
		"commit-history-copy-parity",
		"review-created-concluded-event-broadcasts",
		"review-worktree-reset-on-agent-mutation",
		"frontend-review-graph-panel-no-crash",
		"ts-go-review-lifecycle-e2e-db-git-message-diff",
		"full-narrator-init-rollback-breadth",
		"auto-start-narrator-session-and-start-failure-rollback",
		"conclude-review-tool",
		"dismiss-promote-convert-actions",
		"stale-merge-guard",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "review"}, "requiredProofs", []string{})
	assertNestedCapability(t, caps, []string{"chapters", "review", "replacementContract"}, map[string]any{
		"boundaryTracked":                 true,
		"runtimeRow":                      "CHAPTER-REVIEW-LIFECYCLE-01",
		"createRollbackProof":             true,
		"dirtyFileCopyProof":              true,
		"reviewEdgeNarratorProof":         true,
		"seedReviewStartMessageProof":     true,
		"reviewActionsProof":              true,
		"staleMergeGuardProof":            true,
		"autoStartSessionProof":           true,
		"startFailureRollbackProof":       true,
		"autoStartFallbackPayloadProof":   false,
		"autoStartSession":                true,
		"commitHistoryCopyParity":         true,
		"reviewCreatedEventParity":        true,
		"reviewConcludedEventParity":      true,
		"reviewWorktreeResetParity":       true,
		"fullNarratorInitRollbackParity":  true,
		"frontendReviewSmoke":             true,
		"tsGoReviewLifecycleE2EDiffSmoke": true,
		"fullTSReviewLifecycleParity":     false,
		"replacementSafe":                 false,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "review", "replacementContract"}, "sampledProofs", []string{
		"create-rollback",
		"dirty-file-copy",
		"review-edge-create",
		"seed-review-start-message",
		"commit-history-copy-parity",
		"review-created-concluded-event-broadcasts",
		"review-worktree-reset-on-agent-mutation",
		"frontend-review-graph-panel-no-crash",
		"ts-go-review-lifecycle-e2e-db-git-message-diff",
		"full-narrator-init-rollback-breadth",
		"auto-start-narrator-session-and-start-failure-rollback",
		"conclude-review-tool",
		"dismiss-promote-convert-actions",
		"stale-merge-guard",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "review", "replacementContract"}, "requiredProofs", []string{
		"full-ts-review-lifecycle-parity",
	})
	assertNestedCapability(t, caps, []string{"chapters", "merge"}, map[string]any{"supported": true, "aiResolveMode": "one-shot-basic", "aiResolveUsesNarratorSession": false, "aiResolveRequiresFullNarrator": false, "aiResolveFallbackPayload": true, "aiResolveCherryPickContinuation": false, "aiResolveMultiCommitContinuation": false, "aiResolveContinuationFallbackCode": "AI_RESOLVE_CHERRY_PICK_CONFLICT_UNSUPPORTED"})
	assertNestedCapability(t, caps, []string{"chapters", "merge", "dataIntegrityContract"}, map[string]any{
		"boundaryTracked":                       true,
		"dirtySourceRejectsInsteadOfAutoCommit": false,
		"sourceDirtyAutoCommitParity":           true,
		"requiredReviewValidationProof":         true,
		"requiredReviewStaleValidationProof":    true,
		"targetWorktreeLockProof":               true,
		"sourceWorktreeLockProof":               true,
		"sourceWorktreeCleanupAfterMerge":       true,
		"recordMergedProof":                     true,
		"postCommitDBFailureRollbackProof":      true,
		"aiResolveRecordMergedRetryProof":       true,
		"mergeSummaryFocusedProof":              true,
		"mergeSummaryUserIdentityProof":         true,
		"mergeSummaryBroadcastProof":            true,
		"mergeSummaryPreCollectedInputProof":    true,
		"mergeSummaryModelGenerationHookProof":  true,
		"mergeSummaryModelErrorBroadcastProof":  true,
		"mergeSummaryAPIRequestTrackingProof":   true,
		"mergeSummaryRetryWrapperProof":         true,
		"mergeSummaryUnavailableBroadcastProof": true,
		"mergeSummaryPromptLocaleParity":        true,
		"mergeSummaryModelGenerationParity":     true,
		"mergeSummaryFullInjectionParity":       true,
		"commitSyncParity":                      true,
		"eventBroadcastParity":                  true,
		"frontendMergeSmoke":                    true,
		"tsGoMergeE2EDBGitFSDiffSmoke":          true,
		"replacementSafe":                       true,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "merge", "dataIntegrityContract"}, "sampledProofs", []string{
		"source-dirty-auto-commit-before-merge",
		"required-review-dirty-source-rejects-auto-commit",
		"required-review-validation",
		"required-review-stale-validation",
		"target-worktree-lock",
		"source-worktree-lock-during-auto-commit",
		"record-merged-rows",
		"source-worktree-terminal-cleanup-after-merge",
		"target-merge-commit-sync-after-merge",
		"frontend-merge-graph-refresh-no-crash",
		"post-commit-db-failure-git-rollback",
		"ai-resolve-record-merged-retry-rollback",
		"merge-summary-focused-success",
		"merge-summary-username-identity",
		"merge-summary-message-broadcast",
		"merge-summary-pre-collected-input",
		"merge-summary-model-generation-hook",
		"merge-summary-model-error-broadcast",
		"merge-summary-api-request-tracking",
		"merge-summary-transient-retry-wrapper",
		"merge-summary-provider-unavailable-broadcast",
		"merge-summary-ts-prompt-locale",
		"merge-summary-full-injection",
		"chapter-merged-event-broadcast",
		"ts-go-merge-e2e-db-git-fs-diff",
		"full-ts-merge-data-integrity-parity",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "merge", "dataIntegrityContract"}, "requiredProofs", []string{})
	assertNestedCapability(t, caps, []string{"chapters", "merge", "dataIntegrityContract", "replacementContract"}, map[string]any{
		"boundaryTracked":                       true,
		"runtimeRow":                            "CHAPTER-MERGE-DATA-INTEGRITY-01",
		"dirtySourceRejectsInsteadOfAutoCommit": false,
		"requiredReviewValidationProof":         true,
		"requiredReviewStaleValidationProof":    true,
		"targetWorktreeLockProof":               true,
		"recordMergedProof":                     true,
		"postCommitDBFailureRollbackProof":      true,
		"aiResolveRecordMergedRetryProof":       true,
		"mergeSummaryFocusedProof":              true,
		"mergeSummaryUserIdentityProof":         true,
		"mergeSummaryBroadcastProof":            true,
		"mergeSummaryPreCollectedInputProof":    true,
		"mergeSummaryModelGenerationHookProof":  true,
		"mergeSummaryModelErrorBroadcastProof":  true,
		"mergeSummaryAPIRequestTrackingProof":   true,
		"mergeSummaryRetryWrapperProof":         true,
		"mergeSummaryUnavailableBroadcastProof": true,
		"mergeSummaryPromptLocaleParity":        true,
		"sourceDirtyAutoCommitParity":           true,
		"sourceWorktreeLockProof":               true,
		"sourceWorktreeCleanupAfterMerge":       true,
		"commitSyncParity":                      true,
		"eventBroadcastParity":                  true,
		"mergeSummaryModelGenerationParity":     true,
		"mergeSummaryFullInjectionParity":       true,
		"frontendMergeSmoke":                    true,
		"tsGoMergeE2EDBGitFSDiffSmoke":          true,
		"fullTSMergeDataIntegrityParity":        true,
		"replacementSafe":                       true,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "merge", "dataIntegrityContract", "replacementContract"}, "sampledProofs", []string{
		"source-dirty-auto-commit-before-merge",
		"required-review-dirty-source-rejects-auto-commit",
		"required-review-validation",
		"required-review-stale-validation",
		"target-worktree-lock",
		"source-worktree-lock-during-auto-commit",
		"record-merged-rows",
		"source-worktree-terminal-cleanup-after-merge",
		"target-merge-commit-sync-after-merge",
		"frontend-merge-graph-refresh-no-crash",
		"post-commit-db-failure-git-rollback",
		"ai-resolve-record-merged-retry-rollback",
		"merge-summary-focused-success",
		"merge-summary-username-identity",
		"merge-summary-message-broadcast",
		"merge-summary-pre-collected-input",
		"merge-summary-model-generation-hook",
		"merge-summary-model-error-broadcast",
		"merge-summary-api-request-tracking",
		"merge-summary-transient-retry-wrapper",
		"merge-summary-provider-unavailable-broadcast",
		"merge-summary-ts-prompt-locale",
		"merge-summary-full-injection",
		"chapter-merged-event-broadcast",
		"ts-go-merge-e2e-db-git-fs-diff",
		"full-ts-merge-data-integrity-parity",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "merge", "dataIntegrityContract", "replacementContract"}, "requiredProofs", []string{})
	assertNestedCapability(t, caps, []string{"chapters", "unmerge"}, map[string]any{
		"supported":                          true,
		"route":                              "POST /api/chapters/{id}/unmerge",
		"coreResetRevertPath":                true,
		"mergeEdgeCleanupProof":              true,
		"sourceTargetStatusRestoreProof":     true,
		"commitSyncParity":                   true,
		"mergeSummaryCleanupParity":          true,
		"eventBroadcastParity":               true,
		"branchWorktreePathDerivationParity": true,
		"reloadWakeEventParity":              true,
		"frontendGraphRefreshSmoke":          true,
		"replacementSafe":                    true,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "unmerge"}, "sampledProofs", []string{
		"core-reset-revert-path",
		"merge-edge-cleanup",
		"source-target-status-restore",
		"target-source-commit-sync",
		"branch-worktree-path-derivation",
		"merge-summary-cleanup-on-unmerge",
		"reload-wake-event-broadcasts",
		"frontend-graph-refresh-no-crash",
		"ts-go-unmerge-e2e-db-git-fs-diff",
		"full-ts-unmerge-lifecycle-parity",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "unmerge"}, "requiredProofs", []string{})
	assertNestedCapability(t, caps, []string{"chapters", "unmerge", "replacementContract"}, map[string]any{
		"boundaryTracked":                    true,
		"runtimeRow":                         "CHAPTER-UNMERGE-LIFECYCLE-01",
		"coreResetRevertProof":               true,
		"mergeEdgeCleanupProof":              true,
		"sourceTargetStatusRestoreProof":     true,
		"commitSyncParity":                   true,
		"mergeSummaryCleanupParity":          true,
		"eventBroadcastParity":               true,
		"branchWorktreePathDerivationParity": true,
		"reloadWakeEventParity":              true,
		"frontendGraphRefreshSmoke":          true,
		"tsGoUnmergeE2EDBGitFSDiffSmoke":     true,
		"fullTSUnmergeLifecycleParity":       true,
		"replacementSafe":                    true,
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "unmerge", "replacementContract"}, "sampledProofs", []string{
		"core-reset-revert-path",
		"merge-edge-cleanup",
		"source-target-status-restore",
		"target-source-commit-sync",
		"branch-worktree-path-derivation",
		"merge-summary-cleanup-on-unmerge",
		"reload-wake-event-broadcasts",
		"frontend-graph-refresh-no-crash",
		"ts-go-unmerge-e2e-db-git-fs-diff",
		"full-ts-unmerge-lifecycle-parity",
	})
	assertStringSliceCapability(t, caps, []string{"chapters", "unmerge", "replacementContract"}, "requiredProofs", []string{})
	assertNestedCapability(t, caps, []string{"runtime", "routeContract"}, map[string]any{"adminOnly": true, "scan": "GET /api/runtime/scan", "cached": "GET /api/runtime/cached", "cleanup": "POST /api/runtime/cleanup", "staticRoutes": true, "authBoundary": "requireAdmin"})
	assertStringSliceCapability(t, caps, []string{"runtime", "routeContract"}, "tsCleanupTargets", []string{"terminals", "containers", "browsers"})
	assertStringSliceCapability(t, caps, []string{"runtime", "routeContract"}, "goExtensionTargets", []string{"worktrees"})
	assertNestedCapability(t, caps, []string{"runtime", "scan"}, map[string]any{"supported": true, "adminOnly": true, "readOnly": true, "cacheWrites": true, "scannedAtMs": true, "podmanProbeTimeoutSeconds": float64(3), "podmanScanOutputLimitBytes": float64(32 * 1024), "containerScanSource": "podman-ps-a-state-not-container_instances-db", "containerDBCountParity": false, "browserRuntime": "go-rod", "browserHeadedFieldParity": false, "browserPersistsOptionalTable": true, "terminalSchemaDegrades": true, "podmanUnavailableDegrades": true, "fullTSRuntimeScanParity": false})
	assertStringSliceCapability(t, caps, []string{"runtime", "scan"}, "responseFields", []string{"terminals", "containers", "browsers", "scannedAt"})
	assertStringSliceCapability(t, caps, []string{"runtime", "scan"}, "terminalFields", []string{"running", "exited", "orphanSockets", "supported", "degraded"})
	assertStringSliceCapability(t, caps, []string{"runtime", "scan"}, "terminalFallbackFields", []string{"fallback", "fallbacks", "reason", "error", "message"})
	assertStringSliceCapability(t, caps, []string{"runtime", "scan"}, "terminalFallbackCodes", []string{"terminal_table_check_failed", "terminal_running_count_failed", "terminal_exited_count_failed"})
	assertStringSliceCapability(t, caps, []string{"runtime", "scan"}, "containerFields", []string{"running", "stopped", "podmanAvailable", "supported", "fallback"})
	assertStringSliceCapability(t, caps, []string{"runtime", "scan"}, "containerFallbackFields", []string{"reason", "error", "message"})
	assertStringSliceCapability(t, caps, []string{"runtime", "scan"}, "browserFields", []string{"processRunning", "connected", "activeSessions", "memorySessions", "persistedSessions", "supported", "fallback", "runtime", "dbTableAvailable", "requiresChrome", "cleanupSupported", "sessionApiSupported"})
	assertNestedCapability(t, caps, []string{"runtime", "cached"}, map[string]any{"supported": true, "adminOnly": true, "processMemoryOnly": true, "cacheTTLParity": false, "tsCacheTTLSeconds": float64(30), "goCacheTTLSeconds": float64(0)})
	assertNestedCapability(t, caps, []string{"runtime", "cached", "emptyResponse"}, map[string]any{"cached": false})
	assertStringSliceCapability(t, caps, []string{"runtime", "cached"}, "hitFields", []string{"cached", "data"})
	assertStringSliceCapability(t, caps, []string{"runtime", "cached"}, "invalidatedBy", []string{"POST /api/runtime/cleanup"})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "routeContract"}, map[string]any{"adminOnly": true, "route": "POST /api/runtime/cleanup"})
	assertStringSliceCapability(t, caps, []string{"runtime", "cleanup", "routeContract"}, "targets", []string{"terminals", "containers", "browsers", "worktrees", "all"})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "requestContract"}, map[string]any{"goLooseJSON": true, "maxBodyBytes": float64(4 * 1024 * 1024), "targetRequiredInTS": true, "targetRequiredInGo": false, "defaultTarget": "all", "defaultDryRun": false, "executeFlag": true, "dryRunFlag": true, "invalidTargetStatus": float64(http.StatusBadRequest), "oversizedBodyStatus": float64(http.StatusRequestEntityTooLarge), "oversizedBodyCode": "REQUEST_BODY_TOO_LARGE", "tsZodParity": "partial-go-extends-targets-and-mode", "emptyBodyRunsAllInGo": true, "frontendExecuteBodyShape": "{target,execute:true}"})
	assertStringSliceCapability(t, caps, []string{"runtime", "cleanup", "requestContract"}, "modeAliases", []string{"execute", "dry-run", "dry_run", "preview"})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "responseContract"}, map[string]any{"statusOnOperationErrors": float64(http.StatusOK), "operationErrorsUseOKFalse": true, "alwaysIncludesEmptyErrors": true, "scannedAtMs": true, "executedAtMsWhenNotDryRun": true})
	assertStringSliceCapability(t, caps, []string{"runtime", "cleanup", "responseContract"}, "responseFields", []string{"ok", "target", "dryRun", "removed", "removedCounts", "errors", "errorCount", "outputs", "outputCount", "truncated", "supported", "fallback", "scannedAt", "executedAt"})
	assertStringSliceCapability(t, caps, []string{"runtime", "cleanup", "responseContract"}, "removedScopes", []string{"terminals", "containers", "browsers", "worktrees"})
	assertStringSliceCapability(t, caps, []string{"runtime", "cleanup", "responseContract"}, "legacyFields", []string{"killed", "stopped", "closedSessions", "browserClosed"})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "executionContract"}, map[string]any{"cacheInvalidation": true, "events": false, "progressEvents": false, "globalCleanupLock": false, "removedItemLimit": float64(100), "errorItemLimit": float64(100), "outputItemLimit": float64(20), "commandOutputLimitBytes": float64(32 * 1024), "commandTimeoutSeconds": float64(15), "dryRunSkipsMutations": true, "worktreeCleanupGoExtension": true, "fullTSCleanupParity": false})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "fallbackContract"}, map[string]any{"invalidTargetStatus": float64(http.StatusBadRequest), "invalidTargetMessage": "invalid runtime cleanup target", "podmanMissingExecuteStatus": float64(http.StatusOK), "podmanMissingSetsOKFalse": true, "podmanMissingErrorPrefix": "PODMAN_NOT_FOUND", "terminalMissingTableDegrades": true, "missingTablesNoop": true})
	assertStringSliceCapability(t, caps, []string{"runtime", "cleanup", "fallbackContract"}, "structuredErrorFields", []string{"target", "id", "error"})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "terminals"}, map[string]any{"supported": true, "mode": "kill-exited-and-orphan-records", "mutatesRunningRecords": false, "orphanRuntimeSupport": true, "dbMutation": "status=exited exit_code=COALESCE(exit_code,-1)"})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "containers"}, map[string]any{"supported": true, "mode": "stop-running-db-records", "dbOnlyRunningSupported": true, "podmanStopForContainerID": true, "podmanMissingFallback": true, "podmanPrune": false, "storageCleanupReroute": false, "dbMutation": "status=stopped updated_at=now-when-column-exists"})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "browsers"}, map[string]any{"supported": true, "runtime": "go-rod", "closeMemorySessions": true, "closePersistedSessions": true, "optionalTable": "narrator_browser_sessions", "dbMutation": "status=closed closed_at/updated_at-when-columns-exist"})
	assertNestedCapability(t, caps, []string{"runtime", "cleanup", "worktrees"}, map[string]any{"supported": true, "mode": "go-extension", "tsRouteTarget": false, "cleanupTool": "git worktree remove --force", "safePathOnly": true, "pathScope": "<project.git_path>/.worktrees/*", "orphanDirectoryScan": true, "activeWorktreesSkipped": true, "dbMutation": "chapters.worktree_path=NULL"})
	assertStringSliceCapability(t, caps, []string{"runtime", "cleanup", "worktrees"}, "inactiveStatuses", []string{"dormant", "merged", "abandoned", "frozen", "closed", "inactive"})
	assertNestedCapability(t, caps, []string{"runtime", "replacementContract"}, map[string]any{
		"boundaryTracked":           true,
		"runtimeRow":                "RUNTIME-MAINTENANCE-01",
		"adminRouteProof":           true,
		"scanContractProof":         true,
		"cachedContractProof":       true,
		"cleanupContractProof":      true,
		"processMemoryCacheOnly":    true,
		"boundedCleanupOutputProof": true,
		"containerDBCountParity":    false,
		"browserHeadedFieldParity":  false,
		"cacheTTLParity":            false,
		"cleanupEvents":             false,
		"globalCleanupLock":         false,
		"worktreeCleanupTSRoute":    false,
		"largeProjectRuntimeSmoke":  false,
		"fullTSRuntimeScanParity":   false,
		"fullTSCleanupParity":       false,
		"replacementSafe":           false,
	})
	assertStringSliceCapability(t, caps, []string{"runtime", "replacementContract"}, "sampledProofs", []string{
		"regular-user-admin-boundary-probes",
		"runtime-scan-cached-cleanup-contract-fixtures",
		"podman-missing-degraded-payload-fixture",
		"terminal-schema-degraded-payload-fixture",
		"bounded-cleanup-output-fixture",
	})
	assertStringSliceCapability(t, caps, []string{"runtime", "replacementContract"}, "requiredProofs", []string{
		"ts-container-db-count-scan-parity-diff",
		"browser-headed-field-parity-or-product-decision",
		"runtime-cache-ttl-parity-or-product-decision",
		"cleanup-event-and-global-lock-parity",
		"large-real-project-scan-cleanup-smoke",
		"worktree-cleanup-extension-ui-decision",
	})
	assertNestedCapability(t, caps, []string{"mcp", "routes"}, map[string]any{
		"protocol":         "POST /api/mcp",
		"serversList":      "GET /api/mcp/servers",
		"serversCreate":    "POST /api/mcp/servers",
		"serversImport":    "POST /api/mcp/servers/import",
		"serverDetail":     "GET /api/mcp/servers/{id}",
		"serverUpdate":     "PATCH /api/mcp/servers/{id}",
		"serverDelete":     "DELETE /api/mcp/servers/{id}",
		"serverConnect":    "POST /api/mcp/servers/{id}/connect",
		"serverDisconnect": "POST /api/mcp/servers/{id}/disconnect",
		"serverTest":       "POST /api/mcp/servers/test",
		"toolsList":        "GET /api/mcp/tools",
	})
	assertNestedCapability(t, caps, []string{"mcp", "serverContract"}, map[string]any{
		"listEnvelope":              "{servers: []}",
		"createStatus":              float64(201),
		"idLength":                  float64(8),
		"defaultTransport":          "stdio",
		"defaultEnabled":            true,
		"missingUpdateStatus":       float64(404),
		"missingDeleteStatus":       float64(404),
		"settingsMissingStatus":     float64(501),
		"settingsMissingCode":       "FEATURE_DISABLED",
		"enabledCreateProbesStdio":  true,
		"enabledUpdateProbesStdio":  true,
		"disableClearsRuntimeState": true,
	})
	assertStringSliceCapability(t, caps, []string{"mcp", "serverContract"}, "deleteResponse", []string{"ok"})
	assertNestedCapability(t, caps, []string{"mcp", "validationContract"}, map[string]any{
		"strictCreateAndTest":           true,
		"nonStdioCreateReportsDegraded": true,
		"nonStdioTestReportsDegraded":   true,
		"nameRequired":                  true,
		"stdioCommandRequired":          true,
		"argsArrayRequired":             true,
		"envStringMapRequired":          true,
		"headersStringMapRequired":      true,
		"enabledBooleanRequired":        true,
		"toolPermissionsArrayRequired":  true,
		"allowBehaviorNormalizesTo":     "readWrite",
	})
	assertStringSliceCapability(t, caps, []string{"mcp", "validationContract"}, "transports", []string{"stdio", "streamable-http", "sse"})
	assertStringSliceCapability(t, caps, []string{"mcp", "validationContract"}, "implementedTransports", []string{"stdio"})
	assertNestedCapability(t, caps, []string{"mcp", "protocolContract"}, map[string]any{"jsonrpc": "2.0", "protocolVersion": "2024-11-05", "initializeSupported": true, "toolsListSupported": true, "toolsCallSupported": true, "unknownMethodErrorCode": float64(-32601), "notificationsAccepted": true})
	assertNestedCapability(t, caps, []string{"mcp", "stdioRuntimeContract"}, map[string]any{"lineLimitBytes": float64(1 * 1024 * 1024), "totalLimitBytes": float64(4 * 1024 * 1024), "maxResponseMessages": float64(256), "toolOutputLimitBytes": float64(256 * 1024), "stderrLimitBytes": float64(100000), "toolListSource": "external", "agentToolNamePrefix": "mcp__", "manualDisabledConnectInjects": true})
	assertStringSliceCapability(t, caps, []string{"mcp", "stdioRuntimeContract"}, "toolMetadataFields", []string{"serverId", "serverName", "source"})
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
	assertNestedCapability(t, caps, []string{"mcp", "replacementContract"}, map[string]any{
		"boundaryTracked":             true,
		"routeContractProof":          true,
		"serverSettingsStorageProof":  true,
		"validationFallbackProof":     true,
		"stdioRuntimeProof":           true,
		"builtinProtocolProof":        true,
		"externalToolsInjectionProof": true,
		"permissionMappingProof":      true,
		"perCallLifecycle":            true,
		"nonStdioFallbackProof":       true,
		"persistentRuntimeParity":     false,
		"nonStdioTransportsParity":    false,
		"fullTSMCPManagerParity":      false,
		"frontendMCPUISmoke":          false,
		"liveExternalMCPE2E":          false,
		"kiroLiveMCPE2E":              false,
		"replacementSafe":             false,
	})
	assertStringSliceCapability(t, caps, []string{"mcp", "replacementContract"}, "sampledProofs", []string{
		"positive-stdio-create-connect-list-tools",
		"non-stdio-create-test-connect-degraded-fallback",
		"stdio-agent-tool-injection-execution",
		"permission-mapping-and-subagent-filtering",
		"kiro-mcp-populated-bridge-focused-fixture",
	})
	assertStringSliceCapability(t, caps, []string{"mcp", "replacementContract"}, "requiredProofs", []string{
		"persistent-mcp-runtime-lifecycle-parity",
		"sse-and-streamable-http-transport-parity-or-product-decision",
		"full-ts-mcp-manager-service-management-diff",
		"frontend-mcp-permissions-ui-e2e",
		"live-external-mcp-server-e2e",
		"live-kiro-mcp-e2e-or-product-decision",
	})
	assertNestedCapability(t, caps, []string{"settings"}, map[string]any{"secretMasking": true, "providerModelAugmentation": true, "patchResponseAugmentation": true, "tlsGeneration": true, "retryRules": true})
	assertNestedCapability(t, caps, []string{"settings", "validation"}, map[string]any{"tsZodParity": false, "mode": "loose-json-with-normalization", "loadMigrationParity": "partial", "patchValidationParity": "partial", "unsafeSaveGuard": true, "legacyCodexOAuthCredentialPoolMigration": true, "positiveTLSTSGoSmoke": true, "fullZodUpdateSchemaParity": false, "reason": "Go backend now runs a bounded TypeScript-legacy settings migration pass before saving, including custom API provider array sync, stale model reference purge, and legacy Codex OAuth credential-pool migration, but still does not apply the full TypeScript Zod schema."})
	assertNestedCapability(t, caps, []string{"settings", "validation", "patchContract"}, map[string]any{
		"supported":                          true,
		"mode":                               "allowlisted-root-deep-merge-with-selected-validators",
		"route":                              "PATCH /api/settings",
		"validationErrorCode":                "VALIDATION_ERROR",
		"validationBeforeStorePatch":         true,
		"rejectsUnknownTopLevelFields":       true,
		"rejectsAuthJWTSecret":               true,
		"preservesMaskedProviderSecrets":     true,
		"preservesMaskedTLSPassphrase":       true,
		"preservesMaskedVNetRelayToken":      true,
		"sanitizesSecretsInResponse":         true,
		"augmentsResponseLikeGet":            true,
		"providerCompatibilitySync":          true,
		"dangerReflectionLegacySync":         true,
		"proxyURLNormalization":              true,
		"providerPrefixConflictGuard":        true,
		"tlsFileExistenceValidation":         true,
		"positiveTLSTSGoSmoke":               true,
		"positiveTLSRestartSuppression":      "NARRAFORK_CONTRACT_SUPPRESS_RESTART",
		"containerProxySideEffectRollback":   true,
		"proxyRuntimeMayRunBeforeStoreWrite": true,
		"nestedUnknownFieldParity":           "sampled-strip",
		"nestedUnknownFieldParitySmoke":      true,
		"fullZodUpdateSchemaParity":          false,
	})
	assertStringSliceCapability(t, caps, []string{"settings", "validation", "patchContract"}, "sampledValidationGroups", []string{"server", "paths", "agent-scalars", "agent-prompts-and-aggregations", "agent-retry-rules", "agent-model-selection", "agent-policy", "agent-web-fetch-policy", "chapters", "containers", "editor", "codex", "routines", "update", "vnet", "shares", "provider-arrays", "provider-prefix-conflicts", "nested-unknown-strip", "secret-masking"})
	assertStringSliceCapability(t, caps, []string{"settings", "validation", "patchContract"}, "rootAllowlist", []string{"server", "paths", "agent", "chapters", "containers", "editor", "auth", "customApiProviders", "openaiProviders", "anthropicProviders", "nkpProviders", "nugProviders", "clineProviders", "codex", "routines", "update", "vnet", "shares"})
	assertNestedCapability(t, caps, []string{"settings", "validation", "zodGapContract"}, map[string]any{
		"boundaryTracked":                   true,
		"schemaSource":                      "server/lib/settings Zod update schema",
		"goMode":                            "root-allowlist-with-selected-validators",
		"tsZodParity":                       false,
		"fullZodUpdateSchemaParity":         false,
		"nestedUnknownFieldParity":          "sampled-strip",
		"nestedUnknownFieldSmoke":           true,
		"fullNestedSchemaValidation":        false,
		"providerPrefixBroadSmoke":          true,
		"providerArrayElementShapeSmoke":    true,
		"positiveTLSTSGoSmoke":              true,
		"positiveTLSRestartSuppression":     "NARRAFORK_CONTRACT_SUPPRESS_RESTART",
		"legacyCodexCredentialPoolOwner":    "go-compatible-migration",
		"legacyCodexCredentialPoolMigrated": true,
		"acceptance":                        "compare PATCH /api/settings against the full TypeScript Zod update schema; sampled nested unknown-field behavior, provider-array/prefix breadth, positive TLS save/generation, and legacy Codex OAuth credential-pool migration are covered",
	})
	assertStringSliceCapability(t, caps, []string{"settings", "validation", "zodGapContract"}, "unclaimedValidationFamilies", []string{})
	assertStringSliceCapability(t, caps, []string{"settings", "validation", "zodGapContract"}, "sampledValidationFamilies", []string{
		"root-allowlist",
		"nested-unknown-strip",
		"auth-jwt-rejection",
		"masked-provider-secrets",
		"provider-array-element-shapes",
		"provider-prefix-cross-array-breadth",
		"provider-prefix-conflicts",
		"positive-tls-save-and-generate-ts-go-smoke",
		"legacy-codex-oauth-credential-pool-migration",
		"tls-file-existence",
		"container-proxy-rollback",
	})
	assertNestedCapability(t, caps, []string{"settings", "validation"}, map[string]any{"legacyCodexOAuthCredentialPoolMigration": true})
	assertStringSliceCapability(t, caps, []string{"settings", "validation"}, "knownMissingMigrations", []string{})
	assertStringSliceCapability(t, caps, []string{"settings", "validation"}, "migratedLegacyFields", []string{"editor.legacyEncoding", "agent.yoloSkipReadOnlyConfirmations", "agent.smartInterruptionCheck", "agent.defaultPermissionMode=plan", "legacy openai root", "provider prefixes", "agent.subagentAllowedModels[]", "customApiProviders protocol/defaults", "openai/anthropic provider array sync", "stale agent model references", "mcp.behavior=allow", "codex.defaultReasoningEffort=null", "legacy Codex OAuth credential pool", "proxy urls"})
	assertNestedCapability(t, caps, []string{"settings", "storage"}, map[string]any{"supported": true, "path": "~/.narrafork/settings.json"})
	assertNestedCapability(t, caps, []string{"settings", "patch"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"settings", "replacementContract"}, map[string]any{
		"boundaryTracked":                          true,
		"runtimeRow":                               "SETTINGS-PREFERENCES-01",
		"settingsPatchRootAllowlistProof":          true,
		"maskedSecretsPreservationProof":           true,
		"providerCompatibilitySyncProof":           true,
		"patchResponseAugmentationProof":           true,
		"nestedUnknownFieldParitySmoke":            true,
		"userPreferencesRouteProof":                true,
		"recentTabsSnapshotProof":                  true,
		"favoritesValidationReorderProof":          true,
		"fullZodUpdateSchemaParity":                false,
		"providerArrayElementShapeSmoke":           true,
		"providerPrefixBroadSmoke":                 true,
		"positiveTLSTSGoSmoke":                     true,
		"positiveTLSTSGoRestartSuppressed":         true,
		"legacyCodexOAuthCredentialPoolMigration":  true,
		"recentTabsFavoritesBrowserReconnectSmoke": false,
		"replacementSafe":                          false,
	})
	assertStringSliceCapability(t, caps, []string{"settings", "replacementContract"}, "sampledProofs", []string{
		"settings-root-allowlist-and-selected-validators",
		"masked-provider-tls-vnet-secret-preservation",
		"provider-compatibility-sync",
		"settings-patch-response-augmentation",
		"settings-nested-unknown-strip",
		"settings-provider-array-shape-and-prefix-breadth",
		"settings-positive-tls-save-and-generate-ts-go-smoke",
		"legacy-codex-oauth-credential-pool-migration",
		"user-preferences-response-and-patch-contract",
		"recent-tabs-snapshot-event-contract",
		"favorites-validation-and-reorder-contract",
	})
	assertStringSliceCapability(t, caps, []string{"settings", "replacementContract"}, "requiredProofs", []string{
		"full-typescript-zod-update-schema-diff",
		"recent-tabs-favorites-browser-reconnect-and-reorder-smoke",
	})
	assertNestedCapability(t, caps, []string{"userPreferences"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"userPreferences", "routes"}, map[string]any{"get": "GET /api/user-preferences", "patch": "PATCH /api/user-preferences", "recentTabsUpsert": "PUT /api/user-preferences/recent-tabs", "recentTabsRemove": "DELETE /api/user-preferences/recent-tabs/{type}/{id}", "recentTabsMove": "PATCH /api/user-preferences/recent-tabs/move", "recentTabsPin": "PATCH /api/user-preferences/recent-tabs/pin", "recentTabsClear": "POST /api/user-preferences/recent-tabs/clear", "graphViewportsPatch": "PATCH /api/user-preferences/graph-viewports"})
	assertNestedCapability(t, caps, []string{"userPreferences", "responseContract"}, map[string]any{"recentTabsArray": true, "commandsArray": true, "gatewayConfigObject": true, "graphViewportsRawJSON": true, "secretRedaction": true})
	assertNestedCapability(t, caps, []string{"userPreferences", "patchContract"}, map[string]any{"boolFieldValidation": true, "maskedSecretsPreserved": true})
	assertStringSliceCapability(t, caps, []string{"userPreferences", "patchContract"}, "languageEnum", []string{"en", "zh-CN"})
	assertStringSliceCapability(t, caps, []string{"userPreferences", "patchContract"}, "sendModeEnum", []string{"enter", "ctrl+enter"})
	assertStringSliceCapability(t, caps, []string{"userPreferences", "patchContract"}, "notifySoundTypeEnum", []string{"builtin", "custom"})
	assertNestedCapability(t, caps, []string{"userPreferences", "recentTabsContract"}, map[string]any{"maxTabs": float64(20), "responseDirectArray": true, "perUserLock": true, "sessionTypeMigratesToNarrator": true, "updateOnlyNoInsert": true, "unknownFieldsStripped": true, "pinnedOrdering": true, "workspaceGrouping": true, "snapshotEvent": "user:recent_tabs_snapshot"})
	assertStringSliceCapability(t, caps, []string{"userPreferences", "recentTabsContract"}, "tabTypes", []string{"chapter", "narrator", "project", "workspace", "subagent"})
	assertStringSliceCapability(t, caps, []string{"userPreferences", "recentTabsContract"}, "requiredFields", []string{"type", "id", "title", "lastVisitedAt"})
	assertStringSliceCapability(t, caps, []string{"userPreferences", "recentTabsContract"}, "clearScopes", []string{"all", "projects", "inactive_narrators"})
	assertStringSliceCapability(t, caps, []string{"userPreferences", "recentTabsContract"}, "snapshotFields", []string{"type", "tabs", "revision"})
	assertNestedCapability(t, caps, []string{"userPreferences", "graphViewportsContract"}, map[string]any{"projectIDRequired": true, "responseOK": true, "storesRawJSON": true})
	assertNestedCapability(t, caps, []string{"favorites"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"favorites", "routes"}, map[string]any{"list": "GET /api/favorites", "create": "POST /api/favorites", "update": "PATCH /api/favorites/{id}", "delete": "DELETE /api/favorites/{id}", "reorder": "PUT /api/favorites/reorder"})
	assertNestedCapability(t, caps, []string{"favorites", "storage"}, map[string]any{"table": "user_favorite_directories", "userScoped": true})
	assertNestedCapability(t, caps, []string{"favorites", "responseContract"}, map[string]any{"directArrayList": true, "createdStatus": float64(201)})
	assertStringSliceCapability(t, caps, []string{"favorites", "responseContract"}, "fields", []string{"id", "userId", "path", "label", "sortOrder", "createdAt"})
	assertNestedCapability(t, caps, []string{"favorites", "validationContract"}, map[string]any{"pathRequired": true, "maxPathLength": float64(4096), "maxLabelLength": float64(200), "createRejectsNullLabel": true, "duplicatePathStatus": float64(400), "missingUpdateStatus": float64(404), "missingDeleteStatus": float64(404)})
	assertNestedCapability(t, caps, []string{"favorites", "reorderContract"}, map[string]any{"idsRequired": true, "emptyIDsInvalid": true, "transactional": true, "zeroBasedSortOrder": true})
	assertNestedCapability(t, caps, []string{"favorites", "sideEffects"}, map[string]any{"duplicateCheckBeforeInsert": true, "lookupFailureNoInsert": true})
	assertNestedCapability(t, caps, []string{"providers", "routeAuthContract"}, map[string]any{
		"codexQuotaOverviewAuthenticated":   true,
		"sensitiveRouteDeniedStatus":        float64(403),
		"modelRefreshRegularUserCompatible": true,
		"settingsBackedProviderSecrets":     true,
		"providerMissingStatus":             float64(404),
		"providerMissingErrorIncludesID":    true,
	})
	assertStringSliceCapability(t, caps, []string{"providers", "routeAuthContract"}, "authenticatedModelProviders", []string{"openai", "anthropic", "nkp", "nug"})
	assertStringSliceCapability(t, caps, []string{"providers", "routeAuthContract"}, "adminOnlyProviderControlGroups", []string{"kiro", "codex", "cline"})
	assertNestedCapability(t, caps, []string{"providers", "modelRefreshContract"}, map[string]any{
		"cacheReadEnvelope":                       "{models,fromCache}",
		"perProviderRefreshEnvelope":              "{models,fromCache}",
		"aggregateRefreshEnvelope":                "{results,models,fromCache}",
		"missingProviderStatus":                   float64(404),
		"missingKeyAggregateCount":                float64(0),
		"missingKeyProviderSpecificStatus":        float64(500),
		"sortedDeduplicatedModelArrays":           true,
		"openAIModelIDField":                      "id",
		"anthropicModelIDField":                   "id",
		"nkpModelIDField":                         "model_id",
		"nkpNormalizesModelID":                    true,
		"anthropicResolvedBaseURL":                true,
		"nugAggregateProviderNamedErrors":         true,
		"clineProviderRefreshEnvelope":            "{count,fromCache}",
		"clineProviderRefreshPreservesDuplicates": true,
		"clineAggregateReadDeduplicates":          true,
	})
	assertNestedCapability(t, caps, []string{"providers", "quotaRouteContract"}, map[string]any{"openAIAnthropicDisabledCapabilityGateOnly": true, "missingProviderStatus": float64(404), "cachedPublicResponsesOmitFetchedAt": true})
	assertStringSliceCapability(t, caps, []string{"providers", "quotaRouteContract"}, "nkpQuotaPublicFields", []string{"quotaBalance", "quotaTotalGranted"})
	assertStringSliceCapability(t, caps, []string{"providers", "quotaRouteContract"}, "nugQuotaPublicFields", []string{"balance", "totalGranted"})
	assertNestedCapability(t, caps, []string{"providers", "frontendGateContract"}, map[string]any{
		"supported":                            true,
		"explicitMetadataRequired":             true,
		"partialGoMissingMetadataDefault":      "unsupported",
		"legacyNoCapabilitiesDefault":          "typescript-compatible-optimistic",
		"routeMetadataRequired":                true,
		"modelRefreshRequiresRefreshSupported": true,
		"quotaRequiresSupported":               true,
		"agentModeRequiresSupported":           true,
		"disabledByDesignFallbacksGateOnly":    true,
		"fullProviderRouteInventory":           false,
		"liveUpstreamSmoke":                    false,
	})
	assertStringSliceCapability(t, caps, []string{"providers", "frontendGateContract"}, "gatedActions", []string{"provider-routes", "model-refresh", "quota-fetch", "agent-mode-runtime", "credential-control", "kiro-chat-sse"})
	assertStringSliceCapability(t, caps, []string{"providers", "frontendGateContract"}, "sampledProviderFamilies", []string{"openai", "anthropic", "nkp", "nug", "codex", "cline", "kiro"})
	assertNestedCapability(t, caps, []string{"providers", "replacementContract"}, map[string]any{
		"boundaryTracked":                  true,
		"runtimeRow":                       "PROVIDER-ROUTES-01",
		"routeAuthContractProof":           true,
		"modelRefreshShapeProof":           true,
		"quotaPublicFieldProof":            true,
		"frontendGateMetadataProof":        true,
		"disabledByDesignFallbacksVisible": true,
		"fullProviderRouteInventory":       false,
		"liveUpstreamSmoke":                false,
		"liveCredentialRefreshSmoke":       false,
		"liveQuotaFetchParity":             false,
		"kiroChatSSEParity":                false,
		"kiroModelRefreshParity":           false,
		"codexManagerSchedulerParity":      false,
		"codexUsageQueueParity":            false,
		"clineLiveCredentialFlowSmoke":     false,
		"nugLiveOAuthProviderSmoke":        false,
		"fullAgentProviderRuntimeParity":   false,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"providers", "replacementContract"}, "localFixtureProviderFamilies", []string{"openai", "anthropic", "nkp", "nug", "codex", "cline", "kiro"})
	assertStringSliceCapability(t, caps, []string{"providers", "replacementContract"}, "sampledProofs", []string{
		"provider-route-auth-contract",
		"model-refresh-response-shape-contract",
		"quota-public-field-contract",
		"frontend-gate-metadata-contract",
		"local-fixture-provider-families",
		"kiro-chat-and-model-refresh-disabled-fallbacks",
		"codex-manager-disabled-scheduler-and-usage-queue-contract",
	})
	assertStringSliceCapability(t, caps, []string{"providers", "replacementContract"}, "requiredProofs", []string{
		"full-provider-route-inventory-or-explicit-deferrals",
		"live-upstream-model-refresh-and-quota-smoke",
		"live-credential-refresh-oauth-browser-device-flows",
		"kiro-chat-sse-or-disabled-product-decision",
		"kiro-model-refresh-plugin-bridge-live-smoke",
		"codex-manager-scheduler-usage-queue-decision-or-live-parity",
		"cline-live-oauth-browser-auth-balance-smoke",
		"nug-live-login-api-key-oauth-billing-smoke",
		"agent-runtime-provider-specific-live-smoke",
	})
	assertNestedCapability(t, caps, []string{"providers", "kiro", "settingsContract"}, map[string]any{"adminOnlyCredentialManagement": true, "cachedModelsOnly": true, "modelRefreshDisabledStatus": float64(501), "modelRefreshDisabledCode": "FEATURE_DISABLED", "chatDisabledStatus": float64(501), "chatDisabledCode": "FEATURE_DISABLED", "credentialModelSource": "cached-per-credential-model-metadata", "credentialUsageShape": true, "livePluginRefreshParity": false})
	assertStringSliceCapability(t, caps, []string{"providers", "kiro", "settingsContract"}, "statusFields", []string{"snapshot", "usageCache"})
	assertNestedCapability(t, caps, []string{"providers", "nug", "settingsContract"}, map[string]any{"loginNoImplicitSettingsPersistence": true, "apiKeyNoImplicitSettingsPersistence": true, "oauthCallbackPersistsCredentials": true, "oauthCallbackRedirectsSettings": true, "oauthCallbackMasksAPIKeyInSettings": true, "missingLoginStatus": float64(404), "missingAPIKeyStatus": float64(404), "loginMissingFieldsStatus": float64(400), "apiKeyMissingSessionTokenStatus": float64(400), "usageFieldsCamelCase": true, "usageSummaryTotalsNumeric": true})
	assertStringSliceCapability(t, caps, []string{"providers", "nug", "settingsContract"}, "quotaFields", []string{"balance", "totalGranted"})
	assertStringSliceCapability(t, caps, []string{"providers", "nug", "settingsContract"}, "channelsHealthFields", []string{"available", "channels"})
	assertNestedCapability(t, caps, []string{"providers", "codex", "managerParity"}, map[string]any{"tsCodexManagerEquivalent": false, "schedulerParity": "disabled", "schedulerSupported": false, "usageQueueParity": "disabled", "usageQueueClearSupported": false, "usageQueueClearCode": "FEATURE_DISABLED", "liveUsageFetchParity": "route-local-unverified", "liveUsageScheduler": false, "snapshotPaginationParity": "supported", "credentialAffinity": "sticky-session-failover-supported", "credentialPoolFailover": true, "rebuildHistoryRetryCode": "codex_quota_failover_rebuild"})
	assertNestedCapability(t, caps, []string{"providers", "codex", "settingsContract"}, map[string]any{"adminOnlyCredentialManagement": true, "quotaOverviewAuthenticated": true, "statusFiltersInvalidCredentials": true, "statusSkipsDuplicateCredentials": true, "statusDefaultsMissingFields": true, "importRejectsEmptyCredentials": true, "importRejectsNoTokenCredentials": true, "importAcceptsAccessTokenOnly": true, "importParsesTextExports": true, "missingCredentialMutationStatus": float64(404), "missingCredentialUsageStatus": float64(400), "credentialUpdateInvalidJSONNoop": true, "usageQueueClearDisabledCode": "FEATURE_DISABLED"})
	assertNestedCapability(t, caps, []string{"providers", "cline", "settingsContract"}, map[string]any{"adminOnlyCredentialManagement": true, "readEndpointsDoNotMutateProvider": true, "recommendedModelsRefreshAndCache": true, "providerRefreshPreservesDuplicates": true, "aggregateModelsDeduplicate": true, "enabledModelsPersistContextWindows": true, "authCallbackPersistsCredentials": true, "browserAuthPersistsCredentials": true, "autoAddsFreeModelsOnAuth": true, "pastedCallbackSupported": true, "userInfoRefreshSupported": true, "expiredTokenRefreshBeforeBalance": true, "invalidRefreshClearsCredentials": true, "unauthenticatedUserInfoStatus": float64(200), "unauthenticatedBalanceStatus": float64(422)})
	assertStringSliceCapability(t, caps, []string{"providers", "cline", "settingsContract"}, "statusFields", []string{"authenticated", "providers"})
	assertStringSliceCapability(t, caps, []string{"providers", "cline", "settingsContract"}, "providerStatusFields", []string{"id", "name", "prefix", "hasToken"})
	assertNestedCapability(t, caps, []string{"usageHistory", "admin"}, map[string]any{"supported": true, "readOnly": true, "adminOnly": true, "structuredErrors": true, "fullTSBillingParity": false})
	assertNestedCapability(t, caps, []string{"usageHistory", "admin", "routes"}, map[string]any{"list": true, "providers": true, "stats": true, "timeseries": true, "detail": true})
	assertNestedCapability(t, caps, []string{"usageHistory", "admin", "pagination"}, map[string]any{"defaultPage": float64(1), "defaultPageSize": float64(50), "maxPageSize": float64(100)})
	assertNestedCapability(t, caps, []string{"usageHistory", "admin", "schemaAdaptation"}, map[string]any{"apiRequestsTableOptional": true, "missingTableFailOpen": true, "missingColumnFailOpen": true, "joinColumnsConditional": true, "emptyOnUnsupportedFilter": true})
	assertStringSliceCapability(t, caps, []string{"usageHistory", "admin"}, "filters", []string{"narratorId", "provider", "kind", "model", "chapterId", "projectId", "startDate", "endDate"})
	assertStringSliceCapability(t, caps, []string{"usageHistory", "admin"}, "ordering", []string{"createdAt:desc", "id:desc"})
	assertStringSliceCapability(t, caps, []string{"usageHistory", "admin"}, "recordFields", []string{"id", "narratorId", "messageId", "kind", "provider", "credentialId", "credentialName", "model", "inputTokens", "outputTokens", "cachedInputTokens", "cacheCreationInputTokens", "cacheCreation5mTokens", "cacheCreation1hTokens", "reasoningTokens", "ttftMs", "durationMs", "costUsd", "contextPercent", "meterUsage", "meterUnit", "errorMessage", "hasRawDump", "rawDumpBytes", "createdAt", "narratorTitle", "chapterTitle", "chapterId", "projectId"})
	assertStringSliceCapability(t, caps, []string{"usageHistory", "admin"}, "statsFields", []string{"totalRequests", "totalInputTokens", "totalOutputTokens", "totalCacheCreationTokens", "totalCacheReadTokens", "totalCacheCreation5mTokens", "totalCacheCreation1hTokens", "totalReasoningTokens", "totalTokens", "totalCost", "totalCostUsd", "averageDurationMs", "averageTtftMs", "bounded", "effectiveStartDate", "effectiveEndDate"})
	assertNestedCapability(t, caps, []string{"usageHistory", "admin", "timeseries"}, map[string]any{"defaultGranularity": "day", "fillsEmptyBuckets": true, "truncatesLongRanges": true})
	assertStringSliceCapability(t, caps, []string{"usageHistory", "admin", "timeseries"}, "granularities", []string{"day", "hour", "month"})
	assertNestedCapability(t, caps, []string{"usageHistory", "admin", "timeseries", "maxBucketsByGranularity"}, map[string]any{"hour": float64(744), "day": float64(366), "month": float64(60)})
	assertNestedCapability(t, caps, []string{"usageHistory", "admin", "rawDumpPolicy"}, map[string]any{"listMetadataOnly": true, "detailParsesJSON": true, "invalidJSONFallback": "invalidJson/rawText"})
	assertNestedCapability(t, caps, []string{"usageHistory", "replacementContract"}, map[string]any{
		"boundaryTracked":                 true,
		"runtimeRow":                      "NARRATOR-USAGE-01",
		"adminDisplayContractProof":       true,
		"readOnlyRouteProof":              true,
		"dynamicSchemaFailOpenProof":      true,
		"rawDumpPolicyProof":              true,
		"statsTimeseriesShapeProof":       true,
		"apiRequestPersistenceProof":      true,
		"toolCallUsageMetadataParity":     false,
		"credentialCostAccountingBreadth": false,
		"providerRawDumpCaptureBreadth":   false,
		"nonNarratorRequestKindParity":    false,
		"liveProviderUsageHistorySmoke":   false,
		"frontendUsageAnalyticsSmoke":     false,
		"fullTSBillingParity":             false,
		"replacementSafe":                 false,
	})
	assertStringSliceCapability(t, caps, []string{"usageHistory", "replacementContract"}, "sampledProofs", []string{
		"admin-usage-history-list-detail-stats-timeseries-contract",
		"dynamic-api-requests-schema-fail-open",
		"raw-dump-list-metadata-detail-parse-policy",
		"narrator-provider-api-request-persistence",
		"narrator-usage-stats-summary-shape",
	})
	assertStringSliceCapability(t, caps, []string{"usageHistory", "replacementContract"}, "requiredProofs", []string{
		"tool-call-usage-metadata-parity",
		"credential-cost-accounting-breadth",
		"provider-raw-dump-capture-breadth",
		"non-narrator-request-kind-parity",
		"live-provider-usage-history-smoke",
		"frontend-usage-analytics-browser-smoke",
		"full-ts-usage-billing-service-diff",
	})
	assertNestedCapability(t, caps, []string{"benchmark", "lifecycle"}, map[string]any{"supported": true, "adminOnly": true})
	assertNestedCapability(t, caps, []string{"benchmark", "lifecycle", "routes"}, map[string]any{"createSuite": true, "listSuites": true, "getSuite": true, "createRun": true, "listRuns": true, "getRun": true, "startRun": true, "cancelRun": true, "retryRun": true, "logs": false, "artifacts": false, "events": false})
	assertNestedCapability(t, caps, []string{"benchmark", "lifecycle", "requestContract"}, map[string]any{"decodeMode": "loose-json", "suiteTasksRequired": true, "maxTasksPerSuite": float64(1000), "maxPromptChars": float64(200000), "maxSuiteJSONBytes": float64(20000000), "partialTSZodParity": true, "strictTaskIDRegex": false, "strictNameLengthValidation": false, "metadataJSONLimitParity": false, "testCodeRubricValidationParity": false})
	assertNestedCapability(t, caps, []string{"benchmark", "lifecycle", "requestContract", "runDefaults"}, map[string]any{"name": "Run", "model": "", "permissionMode": "bypassPermissions", "config": "{}"})
	assertStringSliceCapability(t, caps, []string{"benchmark", "lifecycle", "responseContract"}, "suiteFields", []string{"id", "name", "version", "description", "tasks", "createdAt"})
	assertStringSliceCapability(t, caps, []string{"benchmark", "lifecycle", "responseContract"}, "runFields", []string{"id", "suiteId", "name", "model", "systemPrompt", "permissionMode", "status", "config", "totalTasks", "completedTasks", "passedTasks", "failedTasks", "totalCostUsd", "totalTokensIn", "totalTokensOut", "totalDurationMs", "createdAt", "startedAt", "completedAt"})
	assertStringSliceCapability(t, caps, []string{"benchmark", "lifecycle", "responseContract"}, "startFields", []string{"ok", "runId", "status", "runnerAvailable", "message"})
	assertStringSliceCapability(t, caps, []string{"benchmark", "lifecycle", "responseContract"}, "cancelFields", []string{"ok", "runId", "status", "message"})
	assertStringSliceCapability(t, caps, []string{"benchmark", "lifecycle", "responseContract"}, "retryFields", []string{"ok", "runId", "status", "message"})
	assertNestedCapability(t, caps, []string{"benchmark", "lifecycle", "responseContract"}, map[string]any{"goAddsCancelRunIdStatus": true, "retryIsGoExtension": true})
	assertNestedCapability(t, caps, []string{"benchmark", "lifecycle", "executionContract"}, map[string]any{"backgroundStart": true, "sequentialTasks": true, "createsStandaloneNarrator": true, "tempWorkDir": true, "tempWorkDirCleanup": true, "outputLimitChars": float64(50000), "errorLimitChars": float64(5000), "defaultTaskTimeoutMs": float64(10 * 60 * 1000), "sourceWorkspaceCopyDefault": false, "sourceWorkspaceCopyRequiresAllowLocalSource": true, "sourceWorkspaceCopyLimitBytes": float64(100 * 1024 * 1024), "tsSourceWorkspaceDefaultParity": false, "staleRunningReconcileOnStartup": false, "fullTSLifecycleParity": false})
	assertNestedCapability(t, caps, []string{"benchmark", "lifecycle", "startContract"}, map[string]any{"claimPendingOnly": true, "notPendingStatus": float64(http.StatusConflict), "notPendingCode": "BENCHMARK_RUN_NOT_PENDING", "missingRunStatus": float64(http.StatusNotFound), "startsBackgroundGoroutine": true, "runnerAvailableField": true})
	assertNestedCapability(t, caps, []string{"benchmark", "lifecycle", "cancelContract"}, map[string]any{"cancelsActiveInMemoryRun": true, "marksRunCancelled": true, "marksPendingRunningTasksCancelled": true, "missingRunStatus": float64(http.StatusNotFound), "completedRunStatus": float64(http.StatusBadRequest), "tsCompletedRunStatus": float64(http.StatusConflict), "fullTSCancelStatusParity": false, "crossRestartActiveCancel": false})
	assertNestedCapability(t, caps, []string{"benchmark", "lifecycle", "retryContract"}, map[string]any{"supported": true, "goExtension": true, "tsRouteExists": false, "runningRetryStatus": float64(http.StatusConflict), "runningRetryCode": "BENCHMARK_RUN_RUNNING", "startsImmediately": true})
	assertStringSliceCapability(t, caps, []string{"benchmark", "lifecycle", "retryContract"}, "resetsStatuses", []string{"failed", "error", "timeout", "cancelled"})
	assertNestedCapability(t, caps, []string{"benchmark", "lifecycle", "eventContract"}, map[string]any{"httpSSE": false, "websocketProgress": false, "pollResultsAndRunStatus": true})
	assertStringSliceCapability(t, caps, []string{"benchmark", "lifecycle"}, "knownGaps", []string{"No benchmark logs/artifacts/events routes are exposed in Go or TS route contract.", "Go does not reconcile running benchmark rows on startup; TS marks stale running rows failed/error at module load.", "Go cancel of already-completed runs returns 400 while TS returns 409.", "Go retry is an admin-only extension and not a TypeScript route.", "Go keeps local source workspace copying disabled unless config.allowLocalSource=true and caps the copy at 100 MiB."})
	assertNestedCapability(t, caps, []string{"benchmark", "containerExecution"}, map[string]any{"supported": true, "fallback": false, "runtime": "podman", "resourceLimits": true, "timeout": true})
	assertNestedCapability(t, caps, []string{"benchmark", "containerExecution", "runtimeBoundary"}, map[string]any{
		"requiresLinux":               true,
		"podmanRequired":              true,
		"fakePodmanRunnerProof":       true,
		"realPodmanSmoke":             false,
		"fullTSSWEBenchParity":        false,
		"routeFallbackBeforeMutation": false,
		"missingPodmanBehavior":       "task-error-recorded",
		"cleanupOnSetupFailure":       true,
		"containerCleanupCommand":     "podman rm -f",
		"taskOutputLimitChars":        float64(50000),
		"taskErrorLimitChars":         float64(5000),
		"copyLimitBytes":              float64(100 * 1024 * 1024),
		"podmanOutputLimitBytes":      float64(1024 * 1024),
	})
	assertStringSliceCapability(t, caps, []string{"benchmark", "containerExecution", "runtimeBoundary"}, "resourceLimits", []string{"memory", "cpus", "pids", "network"})
	assertNestedCapability(t, caps, []string{"benchmark", "results"}, map[string]any{
		"supported":               true,
		"readOnly":                true,
		"adminOnly":               true,
		"fullTSBenchmarkUIParity": false,
	})
	assertNestedCapability(t, caps, []string{"benchmark", "results", "routes"}, map[string]any{"results": true, "summary": true, "compare": true})
	assertNestedCapability(t, caps, []string{"benchmark", "results", "resultLimit"}, map[string]any{"default": float64(1000), "max": float64(1000)})
	assertStringSliceCapability(t, caps, []string{"benchmark", "results"}, "resultOrdering", []string{"createdAt", "taskId"})
	assertStringSliceCapability(t, caps, []string{"benchmark", "results"}, "resultFields", []string{"id", "runId", "taskId", "taskName", "narratorId", "status", "score", "maxScore", "output", "evalOutput", "errorMessage", "tokensIn", "tokensOut", "costUsd", "durationMs", "toolCallCount", "messageCount", "metadata", "startedAt", "completedAt", "createdAt"})
	assertStringSliceCapability(t, caps, []string{"benchmark", "results"}, "summaryFields", []string{"runId", "suiteName", "model", "status", "total", "passed", "failed", "errors", "timeouts", "pending", "passRate", "avgScore", "totalCostUsd", "totalTokensIn", "totalTokensOut", "totalDurationMs", "avgDurationMs"})
	assertNestedCapability(t, caps, []string{"benchmark", "results", "compareDiagnostics"}, map[string]any{"validationErrors": true, "errorShape": "ok:false-code-reason-error-message", "summaryFailureStatus": float64(400)})
	assertStringSliceCapability(t, caps, []string{"benchmark", "results", "compareDiagnostics"}, "codes", []string{"BENCHMARK_COMPARE_RUN_IDS_REQUIRED", "BENCHMARK_COMPARE_MIN_RUNS_REQUIRED", "BENCHMARK_RUN_SUMMARY_FAILED"})
	assertNestedCapability(t, caps, []string{"benchmark", "results", "summaryMissingRun"}, map[string]any{"status": float64(404), "code": "NOT_FOUND"})
	assertNestedCapability(t, caps, []string{"benchmark", "replacementContract"}, map[string]any{
		"boundaryTracked":                true,
		"runtimeRow":                     "RUNTIME-BENCHMARK-01",
		"lifecycleRouteProof":            true,
		"resultReadContractProof":        true,
		"adminBoundaryProof":             true,
		"fakePodmanRunnerProof":          true,
		"boundedExecutionProof":          true,
		"realPodmanSmoke":                false,
		"fullTSSWEBenchParity":           false,
		"routeFallbackBeforeMutation":    false,
		"staleRunningReconcileOnStartup": false,
		"sourceWorkspaceDefaultParity":   false,
		"fullTSCancelStatusParity":       false,
		"logsArtifactsEventsParity":      false,
		"fullTSBenchmarkUIParity":        false,
		"fullTSLifecycleParity":          false,
		"replacementSafe":                false,
	})
	assertStringSliceCapability(t, caps, []string{"benchmark", "replacementContract"}, "sampledProofs", []string{
		"regular-user-admin-boundary-probes",
		"suite-run-empty-error-contract-probes",
		"fake-podman-runner-cleanup-fixture",
		"lifecycle-create-start-cancel-retry-contract",
		"results-summary-compare-read-contract",
	})
	assertStringSliceCapability(t, caps, []string{"benchmark", "replacementContract"}, "requiredProofs", []string{
		"real-podman-benchmark-browser-smoke",
		"ts-go-swe-bench-lifecycle-side-effect-diff",
		"pre-mutation-missing-podman-route-fallback-decision",
		"startup-stale-running-reconcile-parity",
		"logs-artifacts-events-route-or-disabled-ui-decision",
		"populated-benchmark-ui-e2e-smoke",
	})
	assertNestedCapability(t, caps, []string{"narrator", "wsEvents", "p0"}, map[string]any{"supported": true})
	assertStringSliceCapability(t, caps, []string{"narrator", "wsEvents", "p0"}, "events", []string{"message", "user_message", "stream_event", "tool_started", "tool_use_chunk", "tool_completed", "permission_request", "permission_resolved", "status_change", "substatus_change", "catch_up", "sync_ok", "full_reload", "streaming_snapshot"})
	assertNestedCapability(t, caps, []string{"narrator", "wsEvents", "p1"}, map[string]any{"supported": true, "mode": "baseline-plus-phase4-followup"})
	assertStringSliceCapability(t, caps, []string{"narrator", "wsEvents", "p1"}, "events", []string{"background_task_started", "background_task_completed", "background_task_failed", "background_task_cancelled", "background_task_status_changed", "background_task_output", "todos_updated", "goals_set", "context_usage", "git_status", "tool_inventory", "metering", "nkp_quota_balance", "nkp_queue_status", "quota_balance", "queue_status"})
	assertNestedCapability(t, caps, []string{"narrator", "wsEvents", "reconnect"}, map[string]any{"supported": true, "streamingSnapshot": true, "snapshotBeforeCatchUp": true, "queueStatusReplay": true, "nkpQueueStatusReplay": true, "catchUpCursor": true})
	assertNestedCapability(t, caps, []string{"narrator", "wsEvents", "progress"}, map[string]any{"backgroundTaskOutput": true, "toolProgress": false, "longRunningToolOutput": false, "fullTSToolProgress": false})
	assertNestedCapability(t, caps, []string{"narrator", "wsEvents", "unclaimedContract"}, map[string]any{
		"supported":                        true,
		"fullTSWSEventParity":              false,
		"tsGoLiveSessionSmoke":             false,
		"realReconnectUnderActiveRunSmoke": false,
		"messageMutationEventSmoke":        false,
		"compactProgressEventSmoke":        false,
		"foregroundToolProgressSmoke":      false,
		"browserSessionCountSmoke":         false,
		"reason":                           "The health P0/P1 event lists are a bounded frontend baseline, not full TypeScript narrator WebSocket parity across every event family or live active-run scenario.",
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "wsEvents", "unclaimedContract"}, "unclaimedFamilies", []string{"message_updated", "messages_deleted", "reflection", "compact_progress", "subagent_started", "subagent_status_changed", "browser_session_count", "browser_session_lifecycle", "container_status_changed", "foreground_tool_progress", "long_running_tool_output", "active_run_reconnect"})
	assertNestedCapability(t, caps, []string{"narrator", "advancedObservabilityContract"}, map[string]any{
		"supported":                        true,
		"scope":                            "p1-critical-rollup",
		"messageHistoryStructuredContent":  true,
		"subagentRuntimeContract":          true,
		"hooksPrePostContract":             true,
		"sidecarHydrationAndInjection":     true,
		"compactFallbackContract":          true,
		"usageHistoryAdminContract":        true,
		"wsP0P1EventContract":              true,
		"reconnectQueueSnapshot":           true,
		"rollbackEditSideEffectContract":   true,
		"browserSessionLifecycleContract":  true,
		"fullTSSubagentRuntimeParity":      false,
		"fullTSCompactRecoveryParity":      false,
		"fullTSWSEventParity":              false,
		"fullTSBillingParity":              false,
		"providerSpecificLiveSmoke":        false,
		"longSessionTSCreatedFixtureSmoke": false,
		"foregroundToolProgressParity":     false,
		"activeRunReconnectSmoke":          false,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "advancedObservabilityContract"}, "sampledProofs", []string{
		"structured-content-message-history",
		"subagent-foreground-background-await-send-teamstatus",
		"pre-post-tool-hooks-command-http",
		"sidecar-message-tool-history-hydration",
		"compact-marker-fallback-summary",
		"usage-history-admin-display-contract",
		"ws-p0-p1-event-list-and-reconnect-snapshot",
		"rollback-edit-side-effect-contract",
		"browser-session-lifecycle-count-contract",
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "advancedObservabilityContract"}, "requiredProofs", []string{
		"ts-go-long-session-provider-specific-rich-block-smoke",
		"subagent-full-runtime-reblocking-and-abort-controller-parity",
		"compact-background-model-summary-and-recovery-parity",
		"full-narrator-ws-event-family-live-smoke",
		"foreground-tool-progress-and-long-running-output-smoke",
		"usage-cost-credential-toolcall-live-provider-breadth",
		"active-run-reconnect-and-catch-up-under-load-smoke",
	})
	assertNestedCapability(t, caps, []string{"narrator", "advancedObservabilityContract", "replacementContract"}, map[string]any{
		"boundaryTracked":                  true,
		"runtimeRow":                       "NARRATOR-ADVANCED-OBSERVABILITY-01",
		"rollupOnly":                       true,
		"messageHistoryStructuredContent":  true,
		"subagentRuntimeContract":          true,
		"hooksPrePostContract":             true,
		"sidecarHydrationAndInjection":     true,
		"compactFallbackContract":          true,
		"usageHistoryAdminContract":        true,
		"wsP0P1EventContract":              true,
		"reconnectQueueSnapshot":           true,
		"rollbackEditSideEffectContract":   true,
		"browserSessionLifecycleContract":  true,
		"fullTSSubagentRuntimeParity":      false,
		"fullTSCompactRecoveryParity":      false,
		"fullTSWSEventParity":              false,
		"fullTSBillingParity":              false,
		"providerSpecificLiveSmoke":        false,
		"longSessionTSCreatedFixtureSmoke": false,
		"foregroundToolProgressParity":     false,
		"activeRunReconnectSmoke":          false,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "advancedObservabilityContract", "replacementContract"}, "sampledProofs", []string{
		"structured-content-message-history",
		"subagent-foreground-background-await-send-teamstatus",
		"pre-post-tool-hooks-command-http",
		"sidecar-message-tool-history-hydration",
		"compact-marker-fallback-summary",
		"usage-history-admin-display-contract",
		"ws-p0-p1-event-list-and-reconnect-snapshot",
		"rollback-edit-side-effect-contract",
		"browser-session-lifecycle-count-contract",
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "advancedObservabilityContract", "replacementContract"}, "requiredProofs", []string{
		"ts-go-long-session-provider-specific-rich-block-smoke",
		"subagent-full-runtime-reblocking-and-abort-controller-parity",
		"compact-background-model-summary-and-recovery-parity",
		"full-narrator-ws-event-family-live-smoke",
		"foreground-tool-progress-and-long-running-output-smoke",
		"usage-cost-credential-toolcall-live-provider-breadth",
		"active-run-reconnect-and-catch-up-under-load-smoke",
	})
	assertNestedCapability(t, caps, []string{"narrator", "replacementContract"}, map[string]any{
		"boundaryTracked":                    true,
		"runtimeRow":                         "NARRATOR-AGENT-01",
		"messageHistoryStructuredContent":    true,
		"messageSendBusyBufferProof":         true,
		"agentRuntimeFocusedHooks":           true,
		"agentFinalizationCompensationProof": true,
		"wsP0P1ReconnectSnapshot":            true,
		"subagentRuntimeFocusedProof":        true,
		"compactFallbackProof":               true,
		"rollbackEditFocusedSideEffects":     true,
		"browserSessionLifecycleCountProof":  true,
		"fullSessionMiddlewareParity":        false,
		"providerSpecificLiveSmoke":          false,
		"longSessionTSCreatedFixtureSmoke":   false,
		"fullTSWSEventParity":                false,
		"foregroundToolProgressParity":       false,
		"activeRunReconnectSmoke":            false,
		"fullTSSubagentRuntimeParity":        false,
		"fullTSCompactRecoveryParity":        false,
		"fullTSRollbackEditParity":           false,
		"fullTSBrowserSessionParity":         false,
		"fullTSBillingParity":                false,
		"replacementSafe":                    false,
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "replacementContract"}, "sampledProofs", []string{
		"structured-content-message-history",
		"busy-buffer-priority-after-tools-soft-stop",
		"agent-runtime-override-and-provider-switch-focused-fixtures",
		"agent-finalization-db-compensation-fixtures",
		"ws-p0-p1-event-list-and-reconnect-snapshot",
		"subagent-foreground-background-await-send-teamstatus",
		"compact-marker-deterministic-fallback-summary",
		"rollback-edit-side-effect-contract",
		"browser-session-lifecycle-count-contract",
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "replacementContract"}, "requiredProofs", []string{
		"ts-go-long-session-provider-specific-rich-block-smoke",
		"full-session-middleware-and-provider-runtime-parity",
		"full-narrator-ws-event-family-live-smoke",
		"foreground-tool-progress-and-long-running-output-smoke",
		"subagent-full-runtime-reblocking-abort-and-parent-promise-parity",
		"compact-background-model-summary-and-recovery-parity",
		"rollback-edit-long-chain-provider-recovery-diff",
		"browser-session-live-chrome-go-rod-ts-go-smoke",
		"usage-cost-credential-toolcall-live-provider-breadth",
		"active-run-reconnect-and-catch-up-under-load-smoke",
	})
	assertNestedCapability(t, caps, []string{"narrator", "messages"}, map[string]any{"supported": true, "route": "POST /api/narrators/{id}/messages", "readRoute": "GET /api/narrators/{id}/messages", "structuredSendErrors": true, "statusCreated": float64(201), "busyBufferStatus": float64(202), "fullTSMessageSendParity": false})
	assertNestedCapability(t, caps, []string{"narrator", "messages", "busyBuffer"}, map[string]any{"supported": true, "activeRunDetection": true, "priority": true})
	assertStringSliceCapability(t, caps, []string{"narrator", "messages", "busyBuffer"}, "acceptedStatuses", []string{"working", "waiting"})
	assertNestedCapability(t, caps, []string{"narrator", "messages", "busyBuffer", "response"}, map[string]any{"ok": true, "queued": true, "buffered": true, "fallback": false, "id": true, "bufferedAt": true})
	assertNestedCapability(t, caps, []string{"narrator", "messages", "bufferStorageFallback"}, map[string]any{"supported": true, "status": float64(202), "ok": false, "queued": false, "fallback": true, "busyStatus": "busy", "reasonErrorMessage": true})
	assertNestedCapability(t, caps, []string{"narrator", "messages", "priorityBufferAfterTools"}, map[string]any{"supported": true, "sidecarSource": "buffered_user", "sidecarTiming": "after-tools", "softStopBeforeNextProviderCall": true, "preserveToolResults": true, "preserveTurnComplete": true})
	assertNestedCapability(t, caps, []string{"narrator", "messages", "slashCommandSend"}, map[string]any{"loadTool": true, "unloadTool": true, "loadSkill": true, "bashCommand": true, "goalCommand": true, "expandedPrompt": true, "temporaryModel": true})
	assertNestedCapability(t, caps, []string{"narrator", "messages", "replacementContract"}, map[string]any{
		"boundaryTracked":                  true,
		"runtimeRow":                       "NARRATOR-MESSAGES-01",
		"structuredSendErrorsProof":        true,
		"immediateSendRouteProof":          true,
		"busyBufferFocusedProof":           true,
		"bufferStorageFallbackProof":       true,
		"priorityAfterToolsSoftStopProof":  true,
		"slashCommandSendFocusedProof":     true,
		"fullActiveRunSoftStopParity":      false,
		"multipartMessageSubmitBreadth":    false,
		"multiDeviceWSBufferSmoke":         false,
		"providerSpecificLiveSessionSmoke": false,
		"tsGoMessageSendDBWSDiffSmoke":     false,
		"fullTSMessageSendParity":          false,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "messages", "replacementContract"}, "sampledProofs", []string{
		"structured-send-errors",
		"immediate-201-send",
		"busy-buffer-202-response-shape",
		"busy-storage-fallback-diagnostics",
		"priority-buffer-after-tools-soft-stop",
		"slash-command-send-families",
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "messages", "replacementContract"}, "requiredProofs", []string{
		"full-active-run-soft-stop-equivalence",
		"multipart-message-submit-breadth",
		"multi-device-ws-buffer-smoke",
		"provider-specific-live-session-message-send-smoke",
		"ts-go-message-send-e2e-db-ws-diff",
		"full-ts-message-send-parity",
	})
	assertNestedCapability(t, caps, []string{"narrator", "agentRuntime"}, map[string]any{"supported": true, "beforeTurnReplacement": true, "runtimeSettingsOverride": true, "providerPrefixSwitch": true, "defaultModelReResolve": true, "unsupportedProviderFailFast": true, "legacyProviderFallbackOptIn": "agent.allowGoProviderFallback", "modelSwitchedEvent": true, "fullSessionMiddlewareParity": false})
	assertNestedCapability(t, caps, []string{"narrator", "agentFinalization"}, map[string]any{"supported": true, "assistantTranscriptPreservedOnStatusFailure": true, "toolUseTranscriptPreservedOnToolResultFailure": true, "errorBroadcast": "narrator_error", "statusFallback": "idle-error-when-possible", "fullTSFailureParity": false})
	assertNestedCapability(t, caps, []string{"narrator", "messageHistory"}, map[string]any{"supported": true, "catchUp": true, "childOrphans": true, "toolCalls": true, "compactMarkers": true})
	assertNestedCapability(t, caps, []string{"narrator", "delete"}, map[string]any{
		"supported":                       true,
		"fallback":                        false,
		"route":                           "DELETE /api/narrators/{id}",
		"archivedOnly":                    true,
		"rejectBeforeMutation":            true,
		"nonArchivedRejectBeforeMutation": true,
		"childTreeArchivedGuard":          true,
		"runningBackgroundTaskGuard":      true,
		"runningTerminalGuard":            true,
		"transactionalDBDelete":           true,
		"recursive":                       true,
		"sharedMessageHandoff":            true,
		"orphanMessageCleanup":            true,
		"optionalTableCleanup":            true,
		"uploadPreservation":              true,
		"deleteUploadsAfterCommit":        true,
	})
	assertNestedCapability(t, caps, []string{"narrator", "permissions"}, map[string]any{"supported": true, "approveDeny": true, "updatedInput": true, "pauseResume": "db-pending-tool-call"})
	assertStringSliceCapability(t, caps, []string{"narrator", "toolInventory"}, "supportedOptionalTools", []string{"Terminal", "Browser", "ShareFile", "Recall"})
	assertStringSliceCapability(t, caps, []string{"narrator", "toolInventory"}, "unsupportedOptionalTools", []string{"ForkNarrator", "NarraForkAdmin"})
	assertNestedCapability(t, caps, []string{"narrator", "toolInventory", "mcpExternalTools"}, map[string]any{"supported": true, "parity": "partial", "transport": "stdio", "lifecycle": "per-call"})
	assertNestedCapability(t, caps, []string{"narrator", "toolInventory", "recall"}, map[string]any{"supported": true, "readOnly": true, "scope": "self", "crossNarrator": false, "searchMode": "bounded-like", "maxSearchLimit": float64(50), "maxReadLimit": float64(50), "maxBatchQueries": float64(10), "maxToolCallOutputChars": float64(4000)})
	assertNestedCapability(t, caps, []string{"narrator", "toolInventory"}, map[string]any{"capabilityContract": true})
	assertNestedCapability(t, caps, []string{"narrator", "toolInventory", "read"}, map[string]any{"supported": true, "parity": "partial", "schemaCompatible": true, "runtimeCompatible": "partial", "fallbackBehavior": "explicit_error", "frontendRendering": "text_or_directory_listing", "textFiles": true, "directories": true, "offsetLimit": true, "pdfPages": false, "pagesSchemaOnly": true, "imageContent": false})
	assertNestedCapability(t, caps, []string{"narrator", "toolInventory", "webFetch"}, map[string]any{"supported": true, "parity": "partial", "schemaCompatible": true, "runtimeCompatible": "partial", "fallbackBehavior": "explicit_error/raw_text", "frontendRendering": "raw_text", "mode": "raw-only", "defaultMode": "raw", "policy": "permission-policy-and-proxy-consumed", "rawRequiresChrome": false, "screenshotRequiresChrome": true, "smartRequiresProvider": true})
	assertNestedCapability(t, caps, []string{"narrator", "toolInventory", "browser"}, map[string]any{"supported": true, "parity": "partial", "schemaCompatible": true, "runtimeCompatible": "partial", "fallbackBehavior": "file_or_base64_artifact", "frontendRendering": "file_or_base64_artifact", "runtime": "go-rod", "screenshotPreviewMode": "inline-base64-or-file-artifact", "sharePreview": false, "imageContentBlock": false, "fileOutput": true, "traceFormat": "chrome-devtools-json", "traceShare": true, "traceFileOutput": true, "traceShareUrl": "/api/shares/{shareId}"})
	assertNestedCapability(t, caps, []string{"narrator", "planMode"}, map[string]any{"supported": true, "api": true, "toolReflection": true, "previousModeRestore": true})
	assertNestedCapability(t, caps, []string{"narrator", "compact"}, map[string]any{"supported": true, "markers": true, "segmentCompact": true, "contextClear": true, "fallbackSummary": true, "fallbackReason": "TS-equivalent model-generated compaction is not implemented in go_backend; summaries are caller-provided or deterministic from refs.", "mode": "deterministic-fallback-or-caller-summary", "fullTSCompactRecoveryParity": false})
	assertNestedCapability(t, caps, []string{"narrator", "compact", "routes"}, map[string]any{"compact": true, "segmentCompact": true, "segmentSummary": true, "segmentMessages": true, "updateSummary": true, "deleteCompact": true, "deleteSegment": true, "clearContext": true, "legacyPatchUpdate": true})
	assertNestedCapability(t, caps, []string{"narrator", "compact", "mutationContract"}, map[string]any{"markerMessages": true, "setsIsCompact": true, "segmentHidesRefs": true, "clearsConversationState": true, "clearsPruneState": true, "transactionalSummaryUpdate": true, "modelGeneratedSummaries": false})
	assertNestedCapability(t, caps, []string{"narrator", "compact", "responseContract"}, map[string]any{"ok": true, "message": true, "summary": true, "messageCount": true, "fallbackSummary": true, "fallbackReason": true, "modelSummaryGenerated": false})
	assertStringSliceCapability(t, caps, []string{"narrator", "compact", "responseContract"}, "summarySource", []string{"caller", "deterministic_fallback"})
	assertStringSliceCapability(t, caps, []string{"narrator", "compact", "responseContract"}, "summaryQuality", []string{"caller", "deterministic_preview"})
	assertStringSliceCapability(t, caps, []string{"narrator", "compact"}, "events", []string{"compacting", "message", "segment_compact_hide", "compact_done", "compact_failed"})
	assertNestedCapability(t, caps, []string{"narrator", "compact", "summaryGeneration"}, map[string]any{"supported": false, "kind": "model", "reason": "Go backend does not generate model summaries without an external provider."})
	assertNestedCapability(t, caps, []string{"narrator", "compact", "summaryFallback"}, map[string]any{"supported": true, "mode": "caller-summary-or-deterministic-ref-preview", "deterministic": true})
	assertNestedCapability(t, caps, []string{"narrator", "compact", "replacementContract"}, map[string]any{
		"boundaryTracked":                   true,
		"runtimeRow":                        "NARRATOR-COMPACT-01",
		"routeContractProof":                true,
		"markerMessageMutationProof":        true,
		"segmentHiddenRefsProof":            true,
		"contextClearProof":                 true,
		"deterministicFallbackSummaryProof": true,
		"compactWSEventsProof":              true,
		"modelGeneratedSummaryParity":       false,
		"backgroundCompactParity":           false,
		"compactLockParity":                 false,
		"contextOverflowRecoveryParity":     false,
		"retryAfterCompactParity":           false,
		"exactPruneBoundaryParity":          false,
		"tsGoCompactRecoveryDiffSmoke":      false,
		"fullTSCompactRecoveryParity":       false,
		"replacementSafe":                   false,
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "compact", "replacementContract"}, "sampledProofs", []string{
		"compact-route-contract",
		"marker-message-mutations",
		"segment-hidden-refs",
		"context-conversation-prune-clear",
		"deterministic-fallback-summary",
		"compact-ws-events",
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "compact", "replacementContract"}, "requiredProofs", []string{
		"model-generated-summary-parity",
		"background-vs-blocking-compact-parity",
		"compact-lock-parity",
		"context-overflow-recovery-parity",
		"retry-after-compact-parity",
		"exact-prune-boundary-parity",
		"ts-go-compact-recovery-diff-smoke",
		"full-ts-compact-recovery-parity",
	})
	assertNestedCapability(t, caps, []string{"narrator", "retryRecovery"}, map[string]any{"supported": true, "retry": true, "continue": true, "interrupt": true, "manualOverride": true, "rollback": true, "editAndRegenerate": true})
	assertNestedCapability(t, caps, []string{"narrator", "subagents"}, map[string]any{"supported": true, "foreground": true, "background": true, "awaitAgent": true, "awaitBash": true, "awaitBashWaitForText": true, "narratorContextOnly": true, "send": true, "teamStatus": true, "detachAttach": true, "detachUnblocksParent": true, "reattachBlocksParent": false, "reattachFallback": true, "reattachReason": "Go can detach and open subagent sessions, but cannot re-block an already-running parent tool promise after reattach.", "backgroundResultInjection": true, "staleRecovery": true, "fullTSSubagentRuntimeParity": false})
	assertStringSliceCapability(t, caps, []string{"narrator", "subagents"}, "supportedTypes", []string{"agent", "bash"})
	assertNestedCapability(t, caps, []string{"narrator", "subagents", "routes"}, map[string]any{"detach": true, "attach": true, "updateConclusion": true, "backgroundTasks": true, "cancelBackgroundTask": true})
	assertNestedCapability(t, caps, []string{"narrator", "subagents", "agentToolContract"}, map[string]any{"foreground": true, "background": true, "alias": true, "stop": true, "timeoutMaxMs": float64(30000), "backgroundStatus": "running", "childMessageTree": true, "customTypeFallback": true})
	assertNestedCapability(t, caps, []string{"narrator", "subagents", "awaitContract"}, map[string]any{"agent": true, "bash": true, "bashWaitForText": true, "unsupportedTypeFallback": true, "unsupportedTypeStatus": "unsupported", "missingIDCode": "AWAIT_ID_REQUIRED", "bashNotFoundStatus": "not_found", "parentBlockingDetachStatus": "detached"})
	assertStringSliceCapability(t, caps, []string{"narrator", "subagents", "awaitContract"}, "supportedTypes", []string{"agent", "bash"})
	assertNestedCapability(t, caps, []string{"narrator", "subagents", "teamStatusContract"}, map[string]any{"unsupportedActionFallback": true, "reasonErrorMessage": true, "fileChangesLimit": float64(1000), "missingTargetCode": "TEAM_FILE_CHANGES_TARGET_NOT_FOUND", "targetIDRequiredCode": "TEAM_TARGET_ID_REQUIRED"})
	assertStringSliceCapability(t, caps, []string{"narrator", "subagents", "teamStatusContract"}, "actions", []string{"list", "file_changes", "broadcast", "send"})
	assertNestedCapability(t, caps, []string{"narrator", "subagents", "detachAttachContract"}, map[string]any{"detachUnblocksParent": true, "attachParentBlocking": true, "attachStaleRecovery": true, "reattachBlocksParent": false, "reattachFallback": true})
	assertStringSliceCapability(t, caps, []string{"narrator", "subagents", "detachAttachContract"}, "detachBroadcasts", []string{"subagent_detached", "background_task_started"})
	assertNestedCapability(t, caps, []string{"narrator", "subagents", "updateConclusionContract"}, map[string]any{"parentToolCallUpdate": true, "manualOverrideCleared": true, "fallback": true, "reasonErrorMessage": true})
	assertNestedCapability(t, caps, []string{"narrator", "subagents", "replacementContract"}, map[string]any{
		"boundaryTracked":                   true,
		"runtimeRow":                        "NARRATOR-SUBAGENTS-01",
		"agentToolFocusedProof":             true,
		"sendAwaitTeamStatusProof":          true,
		"detachAttachFocusedProof":          true,
		"backgroundResultInjectionProof":    true,
		"updateConclusionFallbackProof":     true,
		"awaitBashNarratorContextProof":     true,
		"staleRecoveryProof":                true,
		"reattachBlocksParent":              false,
		"abortControllerParity":             false,
		"parentPromiseReblockingParity":     false,
		"customSubagentPromptRuntimeParity": false,
		"conclusionFilePermissionParity":    false,
		"richerTeamInboxParity":             false,
		"subagentBufferParity":              false,
		"subagentCompactRecoveryParity":     false,
		"tsGoSubagentRuntimeDiffSmoke":      false,
		"fullTSSubagentRuntimeParity":       false,
		"replacementSafe":                   false,
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "subagents", "replacementContract"}, "sampledProofs", []string{
		"agent-foreground-background-alias-stop-timeout",
		"send-await-teamstatus",
		"detach-attach-parent-blocking",
		"background-result-injection",
		"update-conclusion-manual-override-fallback",
		"await-bash-narrator-context",
		"stale-recovery",
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "subagents", "replacementContract"}, "requiredProofs", []string{
		"abort-controller-source-swapping",
		"parent-promise-reblocking-after-reattach",
		"custom-subagent-prompt-and-default-model-runtime",
		"conclusion-file-permission-exception",
		"richer-team-inbox-file-change-semantics",
		"subagent-buffer-compact-recovery-parity",
		"ts-go-subagent-runtime-diff-smoke",
		"full-ts-subagent-runtime-parity",
	})
	assertNestedCapability(t, caps, []string{"narrator", "reviewTools"}, map[string]any{"supported": true, "concludeReview": true, "feedbackInjection": true, "promote": true, "dismiss": true, "convertToSubagent": true, "staleMergeGuard": true, "createRollback": true, "autoStartSession": true, "startMode": "auto-start-background-session", "autoStartFallbackPayload": false, "autoStartOptOut": true, "autoStartOptOutStartMode": "seed-message-only"})
	assertNestedCapability(t, caps, []string{"narrator", "browserSessions"}, map[string]any{"supported": true, "defaultEnabled": false, "fallback": false, "runtime": "go-rod", "storage": "go-owned-optional-table", "tableName": "narrator_browser_sessions", "cutover": "explicit-enable", "rollback": "explicit-disable-or-export", "reason": browserSessionsDefaultOffReason, "narratorBound": true, "lifecycleEvents": true, "artifactPersistence": true, "resourceLimits": true, "requiresChrome": true, "fullTSBrowserSessionParity": false})
	assertNestedCapability(t, caps, []string{"narrator", "browserSessions", "routes"}, map[string]any{"list": true, "close": true, "ttl": true, "stopTracing": true, "screenshot": true})
	assertNestedCapability(t, caps, []string{"narrator", "browserSessions", "listContract"}, map[string]any{"directArray": true, "liveFallbackWhenNoTable": true, "numericMsFields": true, "networkMetadata": true, "tracingMetadata": true})
	assertNestedCapability(t, caps, []string{"narrator", "browserSessions", "actionContract"}, map[string]any{"liveFirst": true, "dbFallback": true, "missingRowStatus": float64(404), "disabledStatus": float64(501), "disabledCode": "FEATURE_DISABLED", "disabledFeature": "narrator.browserSessions", "reasonErrorMessage": true})
	assertNestedCapability(t, caps, []string{"narrator", "browserSessions", "ttlContract"}, map[string]any{"field": "ttlMs", "minMs": float64(1000), "maxMs": float64(86400000)})
	assertNestedCapability(t, caps, []string{"narrator", "browserSessions", "screenshotContract"}, map[string]any{"contentType": "image/png-or-persisted-mime", "cacheControl": "no-store", "maxPersistedScreenshotBytes": float64(5242880), "oversizedScreenshotsPersisted": false})
	assertNestedCapability(t, caps, []string{"narrator", "browserSessions", "wsEvents"}, map[string]any{"count": "browser_session_count"})
	assertStringSliceCapability(t, caps, []string{"narrator", "browserSessions", "wsEvents"}, "lifecycle", []string{"browser_session_created", "browser_session_updated", "browser_session_artifact_saved", "browser_session_closed"})
	assertNestedCapability(t, caps, []string{"narrator", "browserSessions", "replacementContract"}, map[string]any{
		"boundaryTracked":               true,
		"runtimeRow":                    "NARRATOR-BROWSER-SESSIONS-01",
		"defaultOffChromeRequiredProof": true,
		"routeActionContractProof":      true,
		"listLiveFallbackProof":         true,
		"liveFirstDBFallbackProof":      true,
		"ttlBoundsProof":                true,
		"screenshotNoStoreCapProof":     true,
		"lifecycleCountEventProof":      true,
		"liveChromeTSGoSmoke":           false,
		"frontendBrowserSessionSmoke":   false,
		"crossPlatformChromeSmoke":      false,
		"persistedRuntimeExportParity":  false,
		"screenshotArtifactParity":      false,
		"fullTSBrowserSessionParity":    false,
		"replacementSafe":               false,
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "browserSessions", "replacementContract"}, "sampledProofs", []string{
		"default-off-chrome-required-capability",
		"list-live-fallback-and-db-shape",
		"live-first-db-fallback-actions",
		"ttl-bounds-contract",
		"screenshot-no-store-and-size-cap",
		"browser-session-count-and-lifecycle-events",
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "browserSessions", "replacementContract"}, "requiredProofs", []string{
		"live-chrome-go-rod-ts-go-smoke",
		"frontend-browser-session-panel-smoke",
		"cross-platform-chrome-runtime-smoke",
		"persisted-runtime-export-rollback-parity",
		"screenshot-artifact-parity",
		"full-ts-browser-session-parity",
	})
	assertNestedCapability(t, caps, []string{"narrator", "containerBrowserToolAutoEnable"}, map[string]any{"defaultEnabled": false, "cutover": "explicit-enable", "rollback": "explicit-disable", "reason": containerBrowserToolAutoEnableReason})
	assertNestedCapability(t, caps, []string{"narrator", "rollbackEditRegenerate"}, map[string]any{"supported": true, "fallback": false, "rollback": true, "editAndRegenerate": true, "copyOnWrite": true, "messageRefTruncation": true, "fileStateRebuild": true, "toolCallInvalidation": true, "agentRerun": true, "optionalFileRevert": true, "optionalAgentRerun": true, "wsEvents": true, "fullTSRollbackEditParity": false})
	assertNestedCapability(t, caps, []string{"narrator", "rollbackEditRegenerate", "routes"}, map[string]any{"rollbackPreview": true, "rollback": true, "editAndRegenerate": true})
	assertNestedCapability(t, caps, []string{"narrator", "rollbackEditRegenerate", "requestContract"}, map[string]any{"rollbackBlockIndexRequired": true, "rollbackRevertDefault": true, "editContentRequired": true, "editRollbackDefault": true, "editRegenerateDefault": true, "rerunAlias": true})
	assertNestedCapability(t, caps, []string{"narrator", "rollbackEditRegenerate", "previewContract"}, map[string]any{"affectedFiles": true, "toolCallCount": true, "currentContent": true, "revertedContent": true, "willBeDeleted": true})
	assertNestedCapability(t, caps, []string{"narrator", "rollbackEditRegenerate", "sideEffects"}, map[string]any{"truncateMessageRefs": true, "truncateContentBlocks": true, "invalidateToolCalls": true, "copyOnWriteSharedMessages": true, "preserveImageBlocks": true, "detachChildSubagentTree": true, "preserveCompactBoundary": true, "rebuildFileState": true, "rollbackFileFailureAtomic": true})
	assertStringSliceCapability(t, caps, []string{"narrator", "rollbackEditRegenerate", "responseContract"}, "rollback", []string{"ok", "message", "deletedMessageIds", "affectedFiles", "reverted"})
	assertStringSliceCapability(t, caps, []string{"narrator", "rollbackEditRegenerate", "responseContract"}, "editAndRegenerate", []string{"ok", "message", "messageId", "privateMessageId", "deletedMessageIds", "affectedFiles", "rollback", "regenerate"})
	assertNestedCapability(t, caps, []string{"narrator", "rollbackEditRegenerate", "wsEventContract"}, map[string]any{"messageUpdated": true, "messagesDeleted": true, "fullReloadOnCopyOnWrite": true})
	assertNestedCapability(t, caps, []string{"narrator", "rollbackEditRegenerate", "replacementContract"}, map[string]any{
		"boundaryTracked":                  true,
		"runtimeRow":                       "NARRATOR-ROLLBACK-EDIT-01",
		"routeContractProof":               true,
		"previewContractProof":             true,
		"messageRefTruncationProof":        true,
		"toolCallInvalidationProof":        true,
		"copyOnWriteSharedMessageProof":    true,
		"imageBlockPreservationProof":      true,
		"childSubagentDetachProof":         true,
		"compactBoundaryPreservationProof": true,
		"fileStateRebuildProof":            true,
		"rollbackFileFailureAtomicProof":   true,
		"wsEventContractProof":             true,
		"optionalRerunProof":               true,
		"longChainProviderRecoveryDiff":    false,
		"providerSpecificRerunParity":      false,
		"activeRunEditRecoverySmoke":       false,
		"frontendRollbackEditSmoke":        false,
		"tsGoRollbackEditDBFSDiffSmoke":    false,
		"fullTSRollbackEditParity":         false,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "rollbackEditRegenerate", "replacementContract"}, "sampledProofs", []string{
		"rollback-preview-route-contract",
		"message-ref-and-content-block-truncation",
		"tool-call-invalidation",
		"copy-on-write-shared-message-edit",
		"file-state-rebuild-and-rollback-file-atomicity",
		"compact-boundary-preservation",
		"ws-message-update-delete-full-reload-events",
	})
	assertStringSliceCapability(t, caps, []string{"narrator", "rollbackEditRegenerate", "replacementContract"}, "requiredProofs", []string{
		"long-chain-provider-recovery-diff",
		"provider-specific-rerun-parity",
		"active-run-edit-recovery-smoke",
		"frontend-rollback-edit-regenerate-smoke",
		"ts-go-rollback-edit-db-fs-diff",
		"full-ts-rollback-edit-parity",
	})
	assertNestedCapability(t, caps, []string{"content", "projectDatabase"}, map[string]any{"supported": true, "syncMode": "manual-api", "eventDrivenSync": false, "backupImport": true, "routePrefix": "/api/project-db"})
	assertNestedCapability(t, caps, []string{"content", "projectDatabase", "backupScope"}, map[string]any{
		"mode":                        "project-domain-portability-subset",
		"fullMainDBExport":            false,
		"schemaOwner":                 "go-project-backup-schema",
		"syncTrigger":                 "manual-api-only",
		"includesNarratorTranscripts": true,
		"includesToolCalls":           true,
		"includesReviewConclusions":   true,
		"includesTerminalState":       true,
		"includesContainers":          true,
		"includesVolumeSnapshots":     true,
		"includesHooks":               false,
		"includesAPIRequests":         false,
		"includesWorkspaces":          false,
		"includesGatewayMappings":     false,
		"includesBenchmarks":          false,
	})
	assertStringSliceCapability(t, caps, []string{"content", "projectDatabase", "backupScope"}, "includedRepresentativeTables", []string{
		"projects",
		"chapters",
		"chapter_edges",
		"narrators",
		"narrator_messages",
		"narrator_tool_calls",
		"review_conclusions",
		"terminals",
		"container_instances",
		"volume_snapshots",
	})
	assertStringSliceCapability(t, caps, []string{"content", "projectDatabase", "backupScope"}, "excludedRepresentativeTables", []string{
		"api_requests",
		"benchmark_task_results",
		"gateway_session_mappings",
		"hooks",
		"workspaces",
	})
	assertNestedCapability(t, caps, []string{"content", "projectDatabase", "replacementContract"}, map[string]any{
		"boundaryTracked":                    true,
		"runtimeRow":                         "PROJECT-DB-01",
		"manualSyncProof":                    true,
		"backupImportRouteProof":             true,
		"backupScopeProof":                   true,
		"representativeTableMetadataProof":   true,
		"reviewMergeLifecycleSmokeProof":     true,
		"eventDrivenSyncParity":              false,
		"fullMainDBExport":                   false,
		"versionedBackupSchemaMetadata":      false,
		"hooksWorkspacesAPIRequestsExport":   false,
		"gatewayBenchmarkGlobalStateExport":  false,
		"largeProjectBoundedCopySmoke":       false,
		"tsGoProjectDBImportExportDiffSmoke": false,
		"replacementSafe":                    false,
	})
	assertStringSliceCapability(t, caps, []string{"content", "projectDatabase", "replacementContract"}, "sampledProofs", []string{
		"manual-api-project-db-sync-contract",
		"project-domain-portability-subset-scope",
		"representative-included-excluded-table-metadata",
		"review-merge-fields-backup-import-smoke",
	})
	assertStringSliceCapability(t, caps, []string{"content", "projectDatabase", "replacementContract"}, "requiredProofs", []string{
		"event-driven-project-db-sync-parity-or-product-decision",
		"full-main-db-export-scope-decision",
		"versioned-backup-schema-metadata",
		"hooks-workspaces-api-requests-export-decision",
		"gateway-benchmark-global-state-export-decision",
		"large-project-bounded-copy-smoke",
		"ts-go-project-db-import-export-diff-smoke",
	})
	assertNestedCapability(t, caps, []string{"content", "routines"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"content", "routines", "routes"}, map[string]any{
		"list":                "GET /api/routines",
		"globalToggle":        "POST /api/routines/{id}/toggle",
		"projectList":         "GET /api/routines/project/{projectId}",
		"projectToggle":       "POST /api/routines/project/{projectId}/{id}/toggle",
		"globalPromptGet":     "GET /api/routines/global-prompt",
		"globalPromptReplace": "PUT /api/routines/global-prompt",
	})
	assertNestedCapability(t, caps, []string{"content", "routines", "responseContract"}, map[string]any{
		"listEnvelope": "{routines: []}",
	})
	assertStringSliceCapability(t, caps, []string{"content", "routines", "responseContract"}, "routineFields", []string{"id", "type", "category", "name", "descriptionEn", "descriptionZh", "enabled"})
	assertStringSliceCapability(t, caps, []string{"content", "routines", "responseContract"}, "projectRoutineFields", []string{"id", "type", "category", "name", "descriptionEn", "descriptionZh", "enabled", "globalEnabled", "override"})
	assertStringSliceCapability(t, caps, []string{"content", "routines", "responseContract"}, "projectOverrideValues", []string{"global", "enabled", "disabled"})
	assertStringSliceCapability(t, caps, []string{"content", "routines", "responseContract"}, "toggleResponse", []string{"ok"})
	assertStringSliceCapability(t, caps, []string{"content", "routines", "responseContract"}, "globalPromptResponseFields", []string{"content", "filePath", "contentTruncated", "candidates"})
	assertNestedCapability(t, caps, []string{"content", "routines", "toggleContract"}, map[string]any{
		"globalMissingRoutineStatus":  float64(404),
		"projectMissingRoutineStatus": float64(404),
		"projectMissingProjectStatus": float64(404),
		"projectInvalidActionStatus":  float64(400),
		"projectSettingsStorage":      "projects.chapter_settings.routines",
	})
	assertStringSliceCapability(t, caps, []string{"content", "routines", "toggleContract"}, "projectActions", []string{"enable", "disable", "reset"})
	assertNestedCapability(t, caps, []string{"content", "routines", "globalPromptContract"}, map[string]any{"maxPromptBytes": float64(512 * 1024), "contentTruncatedFlag": true, "allowedCandidatePathsOnly": true, "invalidFilePathStatus": float64(400)})
	assertNestedCapability(t, caps, []string{"content", "projectRoutines"}, map[string]any{"supported": true, "fallback": false, "storage": "main-db-project-chapter-settings", "routePrefix": "/api/routines/project/{projectId}", "noDatabaseFallbackCode": "FEATURE_DISABLED", "noDatabaseFallbackFeature": "project.routines", "artifactScope": "tool-routine-settings-only", "commandRoutineArtifacts": false, "skillRoutineArtifacts": false, "automaticArtifactMaterialization": false})
	assertNestedCapability(t, caps, []string{"content", "projectRoutines", "replacementContract"}, map[string]any{
		"boundaryTracked":                    true,
		"runtimeRow":                         "ROUTINE-ARTIFACT-01",
		"projectOverrideFieldProof":          true,
		"projectToggleRouteProof":            true,
		"noDatabaseFallbackMetadataProof":    true,
		"toolRoutineSettingsOnly":            true,
		"commandRoutineArtifacts":            false,
		"skillRoutineArtifacts":              false,
		"automaticArtifactMaterialization":   false,
		"projectSettingsDBAvailabilitySmoke": false,
		"frontendRoutineArtifactSmoke":       false,
		"fullTSRoutineArtifactParity":        false,
		"replacementSafe":                    false,
	})
	assertStringSliceCapability(t, caps, []string{"content", "projectRoutines", "replacementContract"}, "sampledProofs", []string{
		"project-routine-override-fields",
		"project-routine-enable-disable-reset-toggle-flow",
		"tool-routine-settings-only-artifact-scope",
		"no-database-fallback-metadata",
	})
	assertStringSliceCapability(t, caps, []string{"content", "projectRoutines", "replacementContract"}, "requiredProofs", []string{
		"command-routine-artifact-materialization",
		"skill-routine-artifact-materialization",
		"automatic-artifact-materialization-policy",
		"project-chapter-settings-db-availability-smoke",
		"frontend-routine-artifact-ui-smoke",
		"full-ts-routine-artifact-parity",
	})
	assertNestedCapability(t, caps, []string{"content", "skills"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"content", "skills", "routes"}, map[string]any{
		"globalList":    "GET /api/skills/global",
		"globalDetail":  "GET /api/skills/global/{name}",
		"globalCreate":  "POST /api/skills/global",
		"globalUpdate":  "PUT /api/skills/global/{name}",
		"globalDelete":  "DELETE /api/skills/global/{name}",
		"globalToggle":  "POST /api/skills/global/{name}/toggle",
		"projectList":   "GET /api/skills?projectId=...",
		"projectDetail": "GET /api/skills/{name}?projectId=...",
		"projectFile":   "GET /api/skills/{name}/files/*?projectId=...",
	})
	assertNestedCapability(t, caps, []string{"content", "skills", "globalSkillContract"}, map[string]any{
		"storage":                         "filesystem-home-skills",
		"fileName":                        "SKILL.md",
		"disabledFileName":                "SKILL.md.disabled",
		"listResponseDirectArray":         true,
		"createStatus":                    float64(201),
		"duplicateCreateStatus":           float64(400),
		"missingUpdateStatus":             float64(404),
		"missingDeleteStatus":             float64(404),
		"missingToggleStatus":             float64(404),
		"toggleRequiresBooleanEnabled":    true,
		"toggleMissingEnabledStatus":      float64(400),
		"displayNamePreserved":            true,
		"sanitizedDirectoryName":          true,
		"createRejectsMissingName":        true,
		"createRejectsMissingDescription": true,
	})
	assertStringSliceCapability(t, caps, []string{"content", "skills", "responseContract"}, "summaryFields", []string{"name", "description", "disabled", "source"})
	assertStringSliceCapability(t, caps, []string{"content", "skills", "responseContract"}, "detailFields", []string{"name", "description", "content", "location", "disabled", "source"})
	assertStringSliceCapability(t, caps, []string{"content", "skills", "responseContract"}, "deleteResponse", []string{"ok"})
	assertNestedCapability(t, caps, []string{"content", "skills", "projectSkillContract"}, map[string]any{
		"requiresProjectId":         true,
		"projectMissingStatus":      float64(404),
		"noDatabaseFallbackCode":    "FEATURE_DISABLED",
		"noDatabaseFallbackFeature": "project.skills",
		"fileResponseTextPlain":     true,
		"fileTraversalGuard":        true,
		"fileReadBounded":           true,
	})
	assertNestedCapability(t, caps, []string{"content", "projectSkills"}, map[string]any{
		"supported":                       true,
		"fallback":                        false,
		"storage":                         "project-git-path-scan",
		"projectOverridesGlobal":          true,
		"slashSummaryMergesProjectGlobal": true,
		"disabledSkillsHidden":            true,
		"companionFiles":                  true,
		"companionTraversalGuard":         true,
		"maxSkillMarkdownBytes":           float64(256 * 1024),
		"maxSkillCompanionFileBytes":      float64(512 * 1024),
		"absoluteCompanionBaseDirectory":  true,
		"fullCompanionGoldenParity":       false,
	})
	assertNestedCapability(t, caps, []string{"content", "projectSkills", "replacementContract"}, map[string]any{
		"boundaryTracked":                true,
		"runtimeRow":                     "SKILL-COMPANION-01",
		"projectOverrideProof":           true,
		"slashSummaryMergeProof":         true,
		"disabledSkillHiddenProof":       true,
		"companionTraversalGuardProof":   true,
		"boundedCompanionReadProof":      true,
		"absoluteCompanionBaseDirectory": true,
		"symlinkPathEdgeSmoke":           false,
		"companionDepthCountBreadth":     false,
		"frontendSkillCompanionSmoke":    false,
		"fullCompanionGoldenParity":      false,
		"fullOutputGoldenParity":         false,
		"replacementSafe":                false,
	})
	assertStringSliceCapability(t, caps, []string{"content", "projectSkills", "replacementContract"}, "sampledProofs", []string{
		"project-over-global-skill-override",
		"slash-summary-project-global-merge",
		"disabled-project-skills-hidden",
		"companion-traversal-guard-and-bounded-read",
	})
	assertStringSliceCapability(t, caps, []string{"content", "projectSkills", "replacementContract"}, "requiredProofs", []string{
		"project-skill-symlink-path-edge-smoke",
		"companion-depth-count-breadth-smoke",
		"frontend-project-skill-companion-ui-smoke",
		"full-ts-companion-output-golden",
		"ts-go-project-skill-route-diff-smoke",
	})
	assertNestedCapability(t, caps, []string{"customSubagents"}, map[string]any{"supported": true})
	assertNestedCapability(t, caps, []string{"customSubagents", "routes"}, map[string]any{
		"list":   "GET /api/custom-subagents",
		"create": "POST /api/custom-subagents",
		"detail": "GET /api/custom-subagents/{name}",
		"update": "PUT /api/custom-subagents/{name}",
		"delete": "DELETE /api/custom-subagents/{name}",
	})
	assertNestedCapability(t, caps, []string{"customSubagents", "storage"}, map[string]any{"mode": "filesystem-home-subagents", "fileName": "SUBAGENT.md", "missingRoot": "empty-array", "maxFileBytes": float64(256 * 1024)})
	assertNestedCapability(t, caps, []string{"customSubagents", "responseContract"}, map[string]any{"listResponseDirectArray": true, "createStatus": float64(201)})
	assertStringSliceCapability(t, caps, []string{"customSubagents", "responseContract"}, "fields", []string{"name", "description", "toolAccess", "customTools", "defaultModel", "prompt", "promptTruncated"})
	assertStringSliceCapability(t, caps, []string{"customSubagents", "responseContract"}, "deleteResponse", []string{"ok"})
	assertNestedCapability(t, caps, []string{"customSubagents", "validationContract"}, map[string]any{"rejectsBuiltinNames": true, "rejectsDuplicateDirectory": true, "missingDetailStatus": float64(404), "missingDeleteStatus": float64(404), "toolAccessFallback": "readOnly", "updateRenameSupported": true})
	assertStringSliceCapability(t, caps, []string{"customSubagents", "validationContract"}, "allowedToolAccess", []string{"readOnly", "general", "custom"})
	assertNestedCapability(t, caps, []string{"customSubagents", "replacementContract"}, map[string]any{
		"boundaryTracked":                    true,
		"runtimeRow":                         "CUSTOM-SUBAGENT-01",
		"filesystemStorageProof":             true,
		"positiveCRUDRenameProof":            true,
		"validationMissingStateProof":        true,
		"optionalStoreContractProof":         true,
		"canonicalDBTable":                   false,
		"customPromptRuntimeInjectionSmoke":  false,
		"defaultModelToolAccessBrowserSmoke": false,
		"frontendSettingsUISmoke":            false,
		"tsGoCustomSubagentDiffSmoke":        false,
		"fullTSCustomSubagentParity":         false,
		"replacementSafe":                    false,
	})
	assertStringSliceCapability(t, caps, []string{"customSubagents", "replacementContract"}, "sampledProofs", []string{
		"filesystem-subagent-crud-rename",
		"builtin-name-duplicate-missing-validation",
		"optional-noncanonical-store-contract",
	})
	assertStringSliceCapability(t, caps, []string{"customSubagents", "replacementContract"}, "requiredProofs", []string{
		"custom-subagent-runtime-prompt-injection-smoke",
		"default-model-tool-access-browser-smoke",
		"frontend-custom-subagents-settings-ui-smoke",
		"ts-go-custom-subagent-route-and-runtime-diff-smoke",
	})
	assertNestedCapability(t, caps, []string{"hooks"}, map[string]any{"supported": true, "adminOnly": true})
	assertNestedCapability(t, caps, []string{"hooks", "routes"}, map[string]any{"list": "GET /api/hooks?projectId=...", "all": "GET /api/hooks/all", "create": "POST /api/hooks", "detail": "GET /api/hooks/{id}", "update": "PUT /api/hooks/{id}", "delete": "DELETE /api/hooks/{id}"})
	assertNestedCapability(t, caps, []string{"hooks", "responseContract"}, map[string]any{"listResponseDirectArray": true, "createStatus": float64(201)})
	assertStringSliceCapability(t, caps, []string{"hooks", "responseContract"}, "fields", []string{"id", "projectId", "event", "matcher", "type", "command", "url", "headers", "prompt", "model", "timeout", "enabled", "sortOrder", "createdAt", "updatedAt"})
	assertStringSliceCapability(t, caps, []string{"hooks", "responseContract"}, "deleteResponse", []string{"ok"})
	assertNestedCapability(t, caps, []string{"hooks", "validationContract"}, map[string]any{"defaultMatcher": "", "defaultTimeoutSeconds": float64(30), "defaultEnabled": true, "missingUpdateStatus": float64(404), "missingDeleteStatus": float64(404), "updateMissingChecksBeforeBody": true, "preservesHeaders": true, "allowsEmptyMatcher": true, "allowsExplicitNullInactiveFields": true, "rejectsClearingActivePayload": true})
	assertStringSliceCapability(t, caps, []string{"hooks", "validationContract"}, "events", []string{"PreToolUse", "PostToolUse"})
	assertStringSliceCapability(t, caps, []string{"hooks", "validationContract"}, "types", []string{"command", "http"})
	assertNestedCapability(t, caps, []string{"hooks", "replacementContract"}, map[string]any{
		"boundaryTracked":          true,
		"runtimeRow":               "HOOK-RUNTIME-01",
		"adminOnlyRouteProof":      true,
		"crudValidationProof":      true,
		"preToolUseRuntimeProof":   true,
		"postToolUseRuntimeProof":  true,
		"commandHTTPHookProof":     true,
		"broaderLiveSessionSmoke":  false,
		"eventBusTelemetryParity":  false,
		"frontendHooksUISmoke":     false,
		"tsGoHookRuntimeDiffSmoke": false,
		"fullTSHookRuntimeParity":  false,
		"replacementSafe":          false,
	})
	assertStringSliceCapability(t, caps, []string{"hooks", "replacementContract"}, "sampledProofs", []string{
		"admin-only-hook-crud-and-validation",
		"pretooluse-command-http-blocking-hook-fixtures",
		"posttooluse-command-http-nonblocking-hook-fixtures",
	})
	assertStringSliceCapability(t, caps, []string{"hooks", "replacementContract"}, "requiredProofs", []string{
		"broader-live-session-hook-smoke",
		"hook-event-bus-telemetry-parity",
		"frontend-hooks-settings-ui-smoke",
		"ts-go-hook-runtime-diff-smoke",
		"full-ts-hook-runtime-parity-decision",
	})
	assertNestedCapability(t, caps, []string{"workspaces"}, map[string]any{"supported": true, "userScoped": true})
	assertNestedCapability(t, caps, []string{"workspaces", "routes"}, map[string]any{"list": "GET /api/workspaces", "create": "POST /api/workspaces", "detail": "GET /api/workspaces/{id}", "update": "PATCH /api/workspaces/{id}", "delete": "DELETE /api/workspaces/{id}"})
	assertNestedCapability(t, caps, []string{"workspaces", "responseContract"}, map[string]any{"listResponseDirectArray": true, "createStatus": float64(201), "timestamps": "iso-string-from-timestamp-ms"})
	assertStringSliceCapability(t, caps, []string{"workspaces", "responseContract"}, "fields", []string{"id", "userId", "title", "tree", "createdAt", "updatedAt"})
	assertStringSliceCapability(t, caps, []string{"workspaces", "responseContract"}, "deleteResponse", []string{"ok"})
	assertNestedCapability(t, caps, []string{"workspaces", "validationContract"}, map[string]any{"createRequiresTree": true, "minTreeLength": float64(2), "maxTreeLength": float64(50000), "maxTitleLength": float64(200), "defaultTitlePrefix": "Workspace ", "missingDetailStatus": float64(404), "missingUpdateStatus": float64(404), "missingDeleteStatus": float64(404), "updateMissingAfterBody": true})
	assertNestedCapability(t, caps, []string{"workspaces", "replacementContract"}, map[string]any{
		"boundaryTracked":             true,
		"runtimeRow":                  "WORKSPACE-UI-01",
		"userScopedCRUDProof":         true,
		"validationMissingStateProof": true,
		"timestampShapeProof":         true,
		"recentTabsInteractionSmoke":  false,
		"multiUserIsolationSmoke":     false,
		"frontendWorkspaceUISmoke":    false,
		"tsGoWorkspaceRouteDiffSmoke": false,
		"fullTSWorkspaceParity":       false,
		"replacementSafe":             false,
	})
	assertStringSliceCapability(t, caps, []string{"workspaces", "replacementContract"}, "sampledProofs", []string{
		"user-scoped-workspace-crud",
		"workspace-tree-title-validation",
		"iso-timestamp-response-shape",
	})
	assertStringSliceCapability(t, caps, []string{"workspaces", "replacementContract"}, "requiredProofs", []string{
		"recent-tabs-workspace-interaction-smoke",
		"multi-user-workspace-isolation-smoke",
		"frontend-workspace-ui-smoke",
		"ts-go-workspace-route-diff-smoke",
		"full-ts-workspace-parity-decision",
	})
	assertNestedCapability(t, caps, []string{"fs", "browse"}, map[string]any{"supported": true, "fallback": false})
	assertNestedCapability(t, caps, []string{"fs", "mkdir"}, map[string]any{"supported": true, "fallback": false, "singleDirectoryOnly": true, "nameTraversalGuard": true, "pathOnlyBodyRejected": true})
	assertNestedCapability(t, caps, []string{"fs", "preview"}, map[string]any{"supported": true, "maxTextBytes": float64(1 << 20), "maxBinaryBytes": float64(20 * 1024 * 1024), "tooLargeCode": "FS_PREVIEW_TOO_LARGE", "inlineDisposition": true, "privateCacheSeconds": float64(60)})
	fsRevealExpected := map[string]any{
		"supported":                     false,
		"fallback":                      true,
		"code":                          "FEATURE_DISABLED",
		"reason":                        "Opening file manager is not supported on this platform",
		"linuxXDGOpenParity":            false,
		"validatesDirectoryBeforeSpawn": true,
		"routeUnsupportedStatus":        float64(400),
		"routeUnsupportedCode":          "VALIDATION_ERROR",
		"fullTSFileManagerParity":       false,
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		fsRevealExpected = map[string]any{
			"supported":                     true,
			"fallback":                      false,
			"linuxXDGOpenParity":            false,
			"validatesDirectoryBeforeSpawn": true,
			"routeUnsupportedStatus":        float64(400),
			"routeUnsupportedCode":          "VALIDATION_ERROR",
			"fullTSFileManagerParity":       false,
		}
	}
	assertNestedCapability(t, caps, []string{"fs", "reveal"}, fsRevealExpected)
	assertStringSliceCapability(t, caps, []string{"fs", "reveal"}, "supportedPlatforms", []string{"windows", "macos"})
	assertNestedCapability(t, caps, []string{"fs", "systemActionsContract"}, map[string]any{
		"browseSupported":                     true,
		"shortcutsSupported":                  true,
		"mkdirSupported":                      true,
		"mkdirSingleDirectoryOnly":            true,
		"mkdirNameTraversalGuard":             true,
		"previewSupported":                    true,
		"previewMaxTextBytes":                 float64(1 << 20),
		"previewMaxBinaryBytes":               float64(20 * 1024 * 1024),
		"previewTooLargeCode":                 "FS_PREVIEW_TOO_LARGE",
		"previewInlineDisposition":            true,
		"revealSupported":                     runtime.GOOS == "darwin" || runtime.GOOS == "windows",
		"revealLinuxXDGOpenParity":            false,
		"revealValidatesDirectoryBeforeSpawn": true,
		"revealRouteUnsupportedStatus":        float64(400),
		"revealRouteUnsupportedCode":          "VALIDATION_ERROR",
		"healthFallbackCode":                  "FEATURE_DISABLED",
		"fullTSFilesystemActionParity":        false,
	})
	assertStringSliceCapability(t, caps, []string{"fs", "systemActionsContract"}, "revealSupportedPlatforms", []string{"windows", "macos"})
	assertNestedCapability(t, caps, []string{"fs", "replacementContract"}, map[string]any{
		"boundaryTracked":               true,
		"runtimeRow":                    "FRONTEND-FS-01",
		"browseShortcutsProof":          true,
		"mkdirPathSafetyProof":          true,
		"boundedPreviewProof":           true,
		"revealFallbackProof":           true,
		"systemActionsContractProof":    true,
		"linuxXDGOpenParity":            false,
		"crossPlatformRevealSmoke":      false,
		"frontendFilePickerSmoke":       false,
		"largeFilePreviewBrowserSmoke":  false,
		"tsGoFilesystemActionDiffSmoke": false,
		"fullTSFilesystemActionParity":  false,
		"replacementSafe":               false,
	})
	assertStringSliceCapability(t, caps, []string{"fs", "replacementContract"}, "sampledProofs", []string{
		"shortcuts-default-browse-hidden-dir-fixture",
		"mkdir-name-traversal-and-path-only-validation",
		"bounded-preview-limit-and-cache-contract",
		"linux-reveal-unsupported-validation-before-spawn",
	})
	assertStringSliceCapability(t, caps, []string{"fs", "replacementContract"}, "requiredProofs", []string{
		"windows-macos-reveal-smoke",
		"linux-xdg-open-parity-or-product-decision",
		"frontend-file-picker-browser-smoke",
		"large-file-preview-browser-smoke",
		"ts-go-filesystem-action-diff-smoke",
		"full-ts-filesystem-action-parity",
	})
	assertNestedCapability(t, caps, []string{"uploads", "serveNarratorImages"}, map[string]any{"supported": true, "fallback": false})
	assertNestedCapability(t, caps, []string{"uploads", "serveAvatars"}, map[string]any{"supported": true, "fallback": false})
	assertNestedCapability(t, caps, []string{"uploads", "cleanupPreservesMessageImageRefs"}, map[string]any{"supported": true, "fallback": false})
	assertNestedCapability(t, caps, []string{"uploads", "routeContract"}, map[string]any{
		"authenticated":            true,
		"gitGuarded":               false,
		"serveNarratorImagesRoute": "GET /api/uploads/{narratorId}/{imageId}",
		"serveAvatarsRoute":        "GET /api/uploads/avatars/{userId}/{imageId}",
		"avatarRouteBeforeGeneric": true,
		"missingStatus":            float64(http.StatusNotFound),
		"missingCode":              "NOT_FOUND",
		"testCase":                 "seeded-upload-serving-and-missing-baselines",
	})
	assertNestedCapability(t, caps, []string{"uploads", "serveContract"}, map[string]any{
		"root":                     "~/.narrafork/uploads",
		"pathTraversalGuard":       true,
		"symlinkEscapeGuard":       true,
		"regularFileOnly":          true,
		"imageIDPrefixLookup":      true,
		"contentTypeFromExtension": true,
		"serveFile":                true,
		"cacheHeaderParity":        false,
		"tsCacheControl":           "public, max-age=31536000, immutable",
		"fullTSUploadServeParity":  false,
	})
	assertNestedCapability(t, caps, []string{"uploads", "replacementContract"}, map[string]any{
		"boundaryTracked":                  true,
		"routeContractProof":               true,
		"narratorImageServeProof":          true,
		"avatarServeProof":                 true,
		"missingStateBaselineProof":        true,
		"pathTraversalGuardProof":          true,
		"symlinkEscapeGuardProof":          true,
		"regularFileOnlyProof":             true,
		"imageIDPrefixLookupProof":         true,
		"cleanupPreservesMessageImageRefs": true,
		"cacheHeaderParity":                false,
		"immutableCacheHeaderParity":       false,
		"fullTSUploadServeParity":          false,
		"fullTSUploadLifecycleParity":      false,
		"replacementSafe":                  false,
	})
	assertStringSliceCapability(t, caps, []string{"uploads", "replacementContract"}, "sampledProofs", []string{
		"seeded-upload-serving-and-missing-baselines",
		"avatar-route-before-generic-contract",
		"upload-path-traversal-guard-fixture",
		"upload-symlink-escape-guard-fixture",
		"storage-cleanup-preserves-message-image-refs",
	})
	assertStringSliceCapability(t, caps, []string{"uploads", "replacementContract"}, "requiredProofs", []string{
		"ts-immutable-cache-header-parity-or-product-decision",
		"browser-upload-cache-behavior-smoke",
		"full-ts-upload-create-serve-cleanup-lifecycle-diff",
		"shared-message-upload-owner-lifecycle-smoke",
		"frontend-avatar-image-reconnect-smoke",
	})
	assertNestedCapability(t, caps, []string{"shares", "routeContract"}, map[string]any{
		"publicDownloadRoute":     "GET /api/shares/{shareId}",
		"publicPreviewRoute":      "GET /api/shares/{shareId}/preview",
		"publicRoutesRequireAuth": false,
		"createRoute":             "POST /api/shares",
		"createRequiresAuth":      true,
		"createRouteParity":       "go-extension-api-wrapper",
		"tsCreateRouteMounted":    false,
		"safeShareIDPattern":      "^[A-Za-z0-9_-]{1,128}$",
		"missingStatus":           float64(http.StatusNotFound),
		"missingCode":             "NOT_FOUND",
		"testCase":                "public-share-missing-state-and-focused-share-security",
	})
	assertNestedCapability(t, caps, []string{"shares", "create"}, map[string]any{
		"supported":                 true,
		"authenticated":             true,
		"responseStatus":            float64(http.StatusCreated),
		"defaultExpiryHours":        float64(24),
		"maxFileSizeDefaultMb":      float64(4096),
		"tsMaxFileSizeDefaultMb":    float64(500),
		"regularFileOnly":           true,
		"requestPathTraversalGuard": true,
		"symlinkEscapeGuard":        true,
		"sensitiveHomePathGuard":    true,
		"atomicMetadataWrite":       true,
		"fullTSShareCreateParity":   false,
	})
	assertStringSliceCapability(t, caps, []string{"shares", "create"}, "requestFields", []string{"path", "expiryHours"})
	assertStringSliceCapability(t, caps, []string{"shares", "create"}, "responseFields", []string{"id", "url", "downloadUrl", "previewUrl", "name", "originalName", "size", "expiresAt"})
	assertNestedCapability(t, caps, []string{"shares", "publicDownload"}, map[string]any{
		"supported":             true,
		"authenticated":         false,
		"metadataHydration":     true,
		"expiredDeletesContent": true,
		"attachmentHeaders":     true,
		"cacheControl":          "no-cache",
		"contentTypeFallback":   "application/octet-stream",
		"nosniffHeader":         true,
	})
	assertNestedCapability(t, caps, []string{"shares", "preview"}, map[string]any{
		"supported":              true,
		"authenticated":          false,
		"htmlMode":               "escaped-pre",
		"htmlSanitizeParity":     false,
		"inlineDisposition":      true,
		"maxHTMLPreviewBytes":    float64(2 * 1024 * 1024),
		"unsupportedStatus":      float64(http.StatusNotFound),
		"tooLargeStatus":         float64(http.StatusBadRequest),
		"tooLargeCode":           "BAD_REQUEST",
		"previewContentSecurity": true,
		"reason":                 shareEscapedHTMLPreviewReason,
	})
	assertStringSliceCapability(t, caps, []string{"shares", "preview"}, "previewTypes", []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".avif", ".bmp", ".ico", ".mp4", ".webm", ".mov", ".ogg", ".pdf", ".html", ".htm"})
	assertNestedCapability(t, caps, []string{"shares", "lifecycle"}, map[string]any{
		"storageMode":                "disk-metadata-bridge",
		"tsProcessMemoryParity":      false,
		"crossRestartPersistence":    false,
		"startupCleanup":             true,
		"cleanupStaleShares":         true,
		"cleanupDeletesMetadata":     true,
		"rejectsUnknownDiskShares":   true,
		"expiryMetadataEnforced":     true,
		"expiredDeletesDiskContent":  true,
		"metadataPathTraversalGuard": true,
		"metadataMaxBytes":           float64(16 * 1024),
		"downloadAttachmentHeaders":  true,
		"timerParity":                false,
		"lazyExpiryOnRead":           true,
		"acceptedDifference":         "go-disk-metadata-bridge-ephemeral-on-restart",
		"restartSmoke":               "focused-cleanup-fixture",
		"reason":                     shareEphemeralFallbackReason,
	})
	assertNestedCapability(t, caps, []string{"shares", "lifecycleSmokeContract"}, map[string]any{
		"boundaryTracked":                true,
		"runtimeRow":                     "RUNTIME-SHARE-01",
		"acceptedDesign":                 "go-disk-metadata-bridge-ephemeral-on-restart",
		"focusedCleanupFixture":          true,
		"expiredSharePrivacyFixture":     true,
		"unknownDiskShareFixture":        true,
		"metadataTraversalFixture":       true,
		"browserRestartPrivacySmoke":     false,
		"toolCreatedShareBrowserSmoke":   false,
		"tsGoRestartDiffSmoke":           false,
		"htmlPreviewProductAccepted":     false,
		"storageCleanupPublicRouteSmoke": true,
		"fullTSTimerParity":              false,
		"fullTSProcessMemoryParity":      false,
		"fullTSShareLifecycleParity":     false,
		"replacementSafe":                false,
	})
	assertStringSliceCapability(t, caps, []string{"shares", "lifecycleSmokeContract"}, "sampledProofs", []string{
		"focused-cleanup-fixture",
		"expired-share-deletes-content",
		"unknown-disk-share-rejected",
		"metadata-path-traversal-rejected",
		"storage-cleanup-share-public-route-404",
	})
	assertStringSliceCapability(t, caps, []string{"shares", "lifecycleSmokeContract"}, "requiredProofs", []string{
		"browser-download-preview-before-after-restart-smoke",
		"tool-created-share-consumer-smoke",
		"ts-go-restart-expiry-privacy-diff-smoke",
		"html-preview-sanitize-vs-escaped-product-acceptance",
	})
	assertNestedCapability(t, caps, []string{"shares", "lifecycleSmokeContract", "replacementContract"}, map[string]any{
		"boundaryTracked":                true,
		"runtimeRow":                     "RUNTIME-SHARE-LIFECYCLE-SMOKE-01",
		"acceptedDesign":                 "go-disk-metadata-bridge-ephemeral-on-restart",
		"diskMetadataBridgeProof":        true,
		"focusedCleanupFixtureProof":     true,
		"expiredSharePrivacyProof":       true,
		"unknownDiskShareProof":          true,
		"metadataTraversalProof":         true,
		"browserRestartPrivacySmoke":     false,
		"toolCreatedShareBrowserSmoke":   false,
		"tsGoRestartDiffSmoke":           false,
		"htmlPreviewProductAccepted":     false,
		"storageCleanupPublicRouteSmoke": true,
		"fullTSTimerParity":              false,
		"fullTSProcessMemoryParity":      false,
		"fullTSShareLifecycleParity":     false,
		"replacementSafe":                false,
	})
	assertStringSliceCapability(t, caps, []string{"shares", "lifecycleSmokeContract", "replacementContract"}, "sampledProofs", []string{
		"focused-cleanup-fixture",
		"expired-share-deletes-content",
		"unknown-disk-share-rejected",
		"metadata-path-traversal-rejected",
		"storage-cleanup-share-public-route-404",
	})
	assertStringSliceCapability(t, caps, []string{"shares", "lifecycleSmokeContract", "replacementContract"}, "requiredProofs", []string{
		"browser-download-preview-before-after-restart-smoke",
		"tool-created-share-consumer-smoke",
		"ts-go-restart-expiry-privacy-diff-smoke",
		"html-preview-sanitize-vs-escaped-product-acceptance",
		"full-ts-share-lifecycle-parity",
	})
	assertNestedCapability(t, caps, []string{"shares", "replacementContract"}, map[string]any{
		"boundaryTracked":                   true,
		"runtimeRow":                        "RUNTIME-SHARE-01",
		"routeContractProof":                true,
		"goAuthenticatedCreateWrapperProof": true,
		"publicDownloadPreviewProof":        true,
		"diskMetadataBridgeProof":           true,
		"startupCleanupProof":               true,
		"expiredSharePrivacyProof":          true,
		"unknownDiskShareProof":             true,
		"metadataTraversalProof":            true,
		"tsProcessMemoryParity":             false,
		"tsCreateRouteParity":               false,
		"browserRestartPrivacySmoke":        false,
		"toolCreatedShareBrowserSmoke":      false,
		"tsGoRestartDiffSmoke":              false,
		"htmlPreviewSanitizeParity":         false,
		"htmlPreviewProductAccepted":        false,
		"storageCleanupPublicRouteSmoke":    true,
		"fullTSShareLifecycleParity":        false,
		"replacementSafe":                   false,
	})
	assertStringSliceCapability(t, caps, []string{"shares", "replacementContract"}, "sampledProofs", []string{
		"public-download-preview-missing-state",
		"go-authenticated-share-create-wrapper-contract",
		"focused-cleanup-fixture",
		"expired-share-deletes-content",
		"unknown-disk-share-rejected",
		"metadata-path-traversal-rejected",
		"storage-cleanup-share-public-route-404",
	})
	assertStringSliceCapability(t, caps, []string{"shares", "replacementContract"}, "requiredProofs", []string{
		"browser-download-preview-before-after-restart-smoke",
		"tool-created-share-consumer-smoke",
		"ts-go-restart-expiry-privacy-diff-smoke",
		"html-preview-sanitize-vs-escaped-product-acceptance",
		"ts-share-tool-process-memory-lifecycle-diff",
	})
	assertNestedCapability(t, caps, []string{"shares", "ephemeralOnly"}, map[string]any{"supported": false, "fallback": true, "reason": shareEphemeralFallbackReason})
	assertNestedCapability(t, caps, []string{"terminal"}, map[string]any{"supported": true, "scrollbackReplayMode": "raw-capped-buffer", "bufferStateMode": "raw-capped-buffer", "xtermSerializedReplay": false, "xtermSerializedReplayReason": terminalXtermSerializedReplayReason, "windowsPty": false, "maxSnapshotBytes": float64(1 << 20), "multiClientResizeMode": "last-input-client"})
	assertNestedCapability(t, caps, []string{"terminal", "replayContract"}, map[string]any{
		"mode":                     "raw-capped-buffer",
		"scrollbackReplay":         true,
		"bufferStateReplay":        true,
		"xtermSerializedReplay":    false,
		"xtermSerializedStateKeys": false,
		"tsXtermHeadlessParity":    false,
		"maxSnapshotBytes":         float64(1 << 20),
		"multiClientResizeMode":    "last-input-client",
		"requestResizeAfterInput":  true,
		"frontendContractSmoke":    true,
		"rawSnapshotFixture":       "focused-runtime-snapshot",
		"reason":                   terminalXtermSerializedReplayReason,
	})
	assertNestedCapability(t, caps, []string{"terminal", "dtachFallback"}, map[string]any{
		"supported":                                true,
		"available":                                false,
		"directPtyFallback":                        true,
		"detachedReattachRequiresLiveSocket":       true,
		"startupMarksRunningExitedWhenUnavailable": true,
		"orphanImportRequiresAliveSocket":          true,
		"killOrphanNoopWithoutDtach":               true,
		"reason":                                   terminalDtachFallbackReason,
	})
	assertNestedCapability(t, caps, []string{"terminal", "replacementContract"}, map[string]any{
		"boundaryTracked":                   true,
		"runtimeRow":                        "TERMINAL-WS-01",
		"websocketSurfaceSupported":         true,
		"httpPanelContractSampled":          true,
		"rawReplaySmoke":                    true,
		"rawReplayMode":                     "raw-capped-buffer",
		"snapshotFallbackSafe":              true,
		"lastInputClientResizeSmoke":        true,
		"xtermSerializedReplayParity":       false,
		"durableDtachReattachParity":        false,
		"windowsConPTYSmoke":                false,
		"broaderFrontendTerminalPanelSmoke": false,
		"processSnapshotTSFallbackParity":   false,
		"tsGoTerminalWSHTTPDiffSmoke":       false,
		"fullTSTerminalReplacementParity":   false,
		"replacementSafe":                   false,
	})
	assertStringSliceCapability(t, caps, []string{"terminal", "replacementContract"}, "sampledProofs", []string{
		"raw-capped-reconnect-snapshot",
		"ws-client-server-event-list",
		"nonfatal-snapshot-fallback",
		"last-input-client-resize",
		"http-crud-tabs-view-process-sampled",
		"dtach-direct-pty-fallback-metadata",
	})
	assertStringSliceCapability(t, caps, []string{"terminal", "replacementContract"}, "requiredProofs", []string{
		"ts-xterm-serialized-buffer-replay-or-accepted-raw-replay-product-decision",
		"durable-dtach-live-socket-recovery-smoke",
		"windows-conpty-platform-smoke",
		"broader-terminal-panel-browser-smoke",
		"process-snapshot-empty-error-fallback-parity",
		"ts-go-terminal-ws-http-diff-smoke",
	})
	assertNestedCapability(t, caps, []string{"terminal", "ws"}, map[string]any{"subscribe": true, "create": true, "input": true, "resize": true, "kill": true, "rename": true, "scrollback": true, "bufferState": true, "resizeScope": "last-input-client"})
	assertStringSliceCapability(t, caps, []string{"terminal", "ws"}, "clientMessages", []string{"subscribe", "unsubscribe", "create", "input", "resize", "kill", "rename"})
	assertStringSliceCapability(t, caps, []string{"terminal", "ws"}, "serverEvents", []string{"ready", "snapshot", "scrollback", "bufferState", "requestResize", "created", "renamed", "killed", "output", "exit", "error"})
	assertStringSliceCapability(t, caps, []string{"terminal", "ws"}, "subscribeReplayEvents", []string{"ready", "snapshot", "scrollback", "bufferState", "exit"})
	assertNestedCapability(t, caps, []string{"terminal", "ws", "snapshotFallback"}, map[string]any{"code": "TERMINAL_SNAPSHOT_UNAVAILABLE", "fallback": true, "fatal": false, "reasonErrorMessage": true})
	assertNestedCapability(t, caps, []string{"terminal", "processTree"}, map[string]any{"supported": true, "platform": "unix-ps"})
	assertNestedCapability(t, caps, []string{"terminal", "http"}, map[string]any{"supported": true, "basePath": "/api/terminals", "auth": "api-authenticated", "gitGuarded": false, "collectionRoutes": true, "tabRoutes": true, "viewStateRoutes": true, "processRoute": true, "tsValidationParity": "partial", "validationMode": "go-loose-json-with-explicit-current-gaps", "frontendContractSmoke": "http-crud-tabs-view-process-sampled"})
	assertNestedCapability(t, caps, []string{"terminal", "httpValidationGapContract"}, map[string]any{
		"boundaryTracked":                       true,
		"tsValidationParity":                    "partial",
		"validationMode":                        "go-loose-json-with-explicit-current-gaps",
		"fullTerminalHTTPZodParity":             false,
		"tabCreateRequiresExactlyOneTargetInTS": true,
		"tabCreateRequiresExactlyOneTargetInGo": false,
		"viewStateTargetRequiredInTS":           true,
		"viewStateTargetRequiredInGo":           false,
		"viewStateLayoutEnumValidation":         false,
		"viewStatePanelAssignmentsValidation":   false,
		"processSnapshotErrorFallbackParity":    false,
		"batchProcessSnapshotParity":            false,
		"frontendContractSmoke":                 "http-crud-tabs-view-process-sampled",
		"acceptance":                            "align terminal tab/view-state PATCH/PUT validation with the TypeScript Zod schemas, prove TS-compatible process snapshot fallback behavior, and rerun broader terminal panel TS-vs-Go/frontend smoke before claiming full Terminal HTTP parity",
	})
	assertStringSliceCapability(t, caps, []string{"terminal", "httpValidationGapContract"}, "unclaimedValidationFamilies", []string{
		"tab-create-exactly-one-target",
		"view-state-put-target-required",
		"view-state-layout-enum",
		"view-state-panel-assignments",
		"process-snapshot-ts-empty-error-fallback",
		"broader-terminal-panel-frontend-smoke",
	})
	assertStringSliceCapability(t, caps, []string{"terminal", "httpValidationGapContract"}, "sampledValidationFamilies", []string{
		"crud-create-target-validation",
		"terminal-count-broadcasts",
		"tab-list-create-update-delete-reorder",
		"view-state-default-read-and-upsert",
		"process-empty-orphan-runtime-snapshot",
	})
	assertNestedCapability(t, caps, []string{"terminal", "httpRoutes"}, map[string]any{"list": "GET /api/terminals?chapterId|narratorId", "create": "POST /api/terminals", "get": "GET /api/terminals/{id}", "patch": "PATCH /api/terminals/{id}", "delete": "DELETE /api/terminals/{id}", "listTabs": "GET /api/terminals/tabs?chapterId|narratorId", "createTab": "POST /api/terminals/tabs", "updateTab": "PATCH /api/terminals/tabs/{id}", "deleteTab": "DELETE /api/terminals/tabs/{id}", "reorderTabs": "PUT /api/terminals/tabs/reorder", "getViewState": "GET /api/terminals/view-state?chapterId|narratorId", "putViewState": "PUT /api/terminals/view-state", "getProcesses": "GET /api/terminals/{id}/processes", "staticBeforeID": true})
	assertNestedCapability(t, caps, []string{"terminal", "crudContract"}, map[string]any{"listRequiresScope": true, "listScopePrecedence": "chapterId-over-narratorId", "listOrder": "created_at-asc", "createStatus": float64(201), "createRequiresExactlyOneTarget": true, "createTargetValidation": "exactly-one-chapter-or-narrator", "createDefaultName": "Terminal", "createDefaultCols": float64(80), "createDefaultRows": float64(24), "createUsesResolvedCwd": true, "missingChapterStatus": float64(404), "missingNarratorStatus": float64(404), "dormantChapterStatus": float64(400), "ambiguousTargetStatus": float64(400), "runtimeStartRollback": true, "failedRollbackMayLeaveDiagnosticRow": true, "patchReturnsTerminal": true, "patchStatusBroadcast": true, "deleteReturnsOk": true, "deleteWithoutRuntimeMarksExited": true, "deleteWithRuntimeKills": true, "deleteBroadcastsTerminalCount": true, "tsValidationParity": "partial", "looseGraphValueValidation": true})
	assertNestedCapability(t, caps, []string{"terminal", "tabContract"}, map[string]any{"listRequiresScope": true, "listScopePrecedence": "chapterId-over-narratorId", "listOrder": "sort_order-asc-created_at-asc", "createStatus": float64(201), "createRequiresName": true, "createSortOrder": "max-plus-one", "createSortOrderQueryBeforeInsert": true, "createRequiresExactlyOneTarget": false, "createTargetValidationGap": "go-currently-allows-missing-or-ambiguous-tab-target", "updateReturnsTab": true, "emptyNamePatchNoop": true, "deleteReturnsOk": true, "deleteMissingStatus": float64(404), "reorderTransactional": true, "reorderReturnsOk": true, "tsValidationParity": "partial"})
	assertNestedCapability(t, caps, []string{"terminal", "viewStateContract"}, map[string]any{"authenticated": true, "scopePrecedence": "chapterId-over-narratorId", "getDefaultWhenMissing": true, "getDefaultLayout": "single", "getDefaultActiveTabNull": true, "getDefaultAssignmentsNull": true, "putUpsertByUserAndScope": true, "putDefaultLayout": "single", "targetRequiredOnPut": false, "layoutEnumValidation": false, "panelAssignmentsValidation": false, "tsValidationParity": "partial"})
	assertNestedCapability(t, caps, []string{"terminal", "processTreeContract"}, map[string]any{"route": "GET /api/terminals/{id}/processes", "emptyWhenNotRunning": true, "emptyWhenNoRuntime": true, "emptyWhenNoShellPID": true, "windowsEmptyFallback": true, "unixPsTimeoutSeconds": float64(3), "maxSnapshotBytes": float64(1 << 20), "surfaceSnapshotErrors": true, "tsErrorFallbackParity": false, "batchProcessSnapshotParity": false})
	assertNestedCapability(t, caps, []string{"terminal", "countBroadcastContract"}, map[string]any{"event": "terminal_count_changed", "onCreate": true, "onDelete": true, "onStatusPatch": true, "chapterScopeBroadcastsNarrators": true, "activeTerminalCountField": true, "countAliasField": true, "degradedFallback": true, "queryFailureFallback": "terminal_count_query_failed"})
	assertNestedCapability(t, caps, []string{"narrator", "subagents"}, map[string]any{"awaitAgent": true, "awaitBash": true, "awaitBashWaitForText": true, "narratorContextOnly": true, "awaitBashReason": "Await type=bash and wait_for_text are supported in narrator context; non-narrator ExecuteTool does not support Bash run_in_background."})
	assertStringSliceCapability(t, caps, []string{"narrator", "subagents"}, "supportedTypes", []string{"agent", "bash"})
	assertNestedCapability(t, caps, []string{"providers", "runtimeTelemetry"}, map[string]any{"apiRequestUsage": true, "apiRequestCredentialId": true, "apiRequestContextPercent": true, "apiRequestMeterUsage": true, "assistantMessageContextPercent": true, "assistantMessageMeterUsage": true, "meteringEventCredentialId": true})
	assertNestedCapability(t, caps, []string{"providers", "openai", "quota"}, map[string]any{
		"supported":                  false,
		"fallback":                   true,
		"fallbackScope":              "capability-gate-only",
		"code":                       "FEATURE_DISABLED",
		"routeMounted":               false,
		"routeFallbackImplemented":   false,
		"expectedRouteStatus":        float64(404),
		"expectedRouteCode":          "NOT_FOUND",
		"noUpstreamProbe":            true,
		"noSecretReadRequired":       true,
		"tsLiveQuotaParity":          false,
		"frontendCapabilityGateOnly": true,
	})
	assertNestedCapability(t, caps, []string{"providers", "anthropic", "quota"}, map[string]any{
		"supported":                  false,
		"fallback":                   true,
		"fallbackScope":              "capability-gate-only",
		"code":                       "FEATURE_DISABLED",
		"routeMounted":               false,
		"routeFallbackImplemented":   false,
		"expectedRouteStatus":        float64(404),
		"expectedRouteCode":          "NOT_FOUND",
		"noUpstreamProbe":            true,
		"noSecretReadRequired":       true,
		"tsLiveQuotaParity":          false,
		"frontendCapabilityGateOnly": true,
	})
	assertNestedCapability(t, caps, []string{"providers", "openai", "agentRuntime"}, map[string]any{"supported": true, "chatCompletions": true, "responsesApi": true, "toolCalling": true, "streaming": true})
	assertNestedCapability(t, caps, []string{"providers", "openai", "routes"}, map[string]any{"modelsRefresh": true, "perProviderModels": true, "perProviderModelsRefresh": true, "quota": false, "quotas": false})
	assertNestedCapability(t, caps, []string{"providers", "anthropic", "routes"}, map[string]any{"modelsRefresh": true, "perProviderModels": true, "perProviderModelsRefresh": true, "quota": false, "quotas": false})
	assertNestedCapability(t, caps, []string{"providers", "kiro", "models"}, map[string]any{
		"supported":                 true,
		"source":                    "cached-per-credential-model-metadata",
		"refreshSupported":          false,
		"refreshCode":               "FEATURE_DISABLED",
		"refreshFallbackBody":       true,
		"refreshFallbackHTTPStatus": float64(501),
		"livePluginRefresh":         false,
		"fullPluginRefreshParity":   false,
		"reason":                    "Kiro model refresh requires the TypeScript Kiro plugin bridge; go_backend reads cached per-credential model metadata only.",
	})
	assertNestedCapability(t, caps, []string{"providers", "kiro", "pluginBridge"}, map[string]any{
		"supported":   true,
		"requiresBun": true,
		"reason":      "Kiro plugin bridge is available only when the external Bun bridge/plugin environment is present.",
	})
	assertNestedCapability(t, caps, []string{"providers", "kiro", "agentRuntime"}, map[string]any{
		"supported":                            true,
		"httpCompatible":                       true,
		"sse":                                  true,
		"pluginBridge":                         true,
		"requiresConfiguredHTTPOrPluginBridge": true,
		"fullTSKiroRuntimeParity":              false,
		"localLiveSmoke":                       "manual-env-only",
	})
	assertNestedCapability(t, caps, []string{"providers", "kiro", "mcp"}, map[string]any{"listToolsRoute": true, "searchRoute": true, "agentInjection": false})
	assertNestedCapability(t, caps, []string{"providers", "kiro", "routes"}, map[string]any{
		"modelsRefresh":          false,
		"chat":                   false,
		"chatSSE":                false,
		"chatFallbackBody":       true,
		"chatFallbackHTTPStatus": float64(501),
		"chatCode":               "FEATURE_DISABLED",
		"chatReason":             "Kiro chat streaming is not implemented in go_backend; use the narrator/provider agent routes or the TypeScript backend for Kiro chat streaming.",
		"mcpSearch":              true,
		"globalProxy":            true,
		"loadBalancingMode":      true,
		"credentialsImport":      true,
		"credentialUsage":        true,
		"credentialUpdate":       true,
		"credentialDelete":       true,
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
		"billingConfig":            true,
		"billingCreateOrder":       true,
		"billingGetOrder":          true,
		"billingRepayOrder":        true,
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
	assertNestedCapability(t, caps, []string{"providers", "codex", "models"}, map[string]any{"supported": true, "refreshSupported": false})
	assertNestedCapability(t, caps, []string{"providers", "codex", "routes"}, map[string]any{
		"defaultReasoningEffort":    true,
		"useWebSocket":              true,
		"useWebSearch":              true,
		"useImageGeneration":        true,
		"loadBalancingMode":         true,
		"tierOrder":                 true,
		"globalProxy":               true,
		"usageQueueClear":           false,
		"usageQueueClearCode":       "FEATURE_DISABLED",
		"browserAuthWait":           true,
		"browserAuthCancel":         true,
		"deviceAuthStart":           true,
		"deviceAuthPoll":            true,
		"deviceAuthCancel":          true,
		"credentialBatchDelete":     true,
		"credentialDeleteUnhealthy": true,
	})
	assertNestedCapability(t, caps, []string{"providers", "codex", "managerParity"}, map[string]any{"tsCodexManagerEquivalent": false, "schedulerParity": "disabled", "schedulerSupported": false, "usageQueueParity": "disabled", "usageQueueClearSupported": false, "usageQueueClearCode": "FEATURE_DISABLED", "liveUsageFetchParity": "route-local-unverified", "liveUsageScheduler": false, "snapshotPaginationParity": "supported", "credentialAffinity": "sticky-session-failover-supported", "credentialPoolFailover": true, "rebuildHistoryRetryCode": "codex_quota_failover_rebuild"})
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

func assertStringSliceCapability(t *testing.T, root map[string]any, path []string, key string, want []string) {
	t.Helper()
	current := root
	for _, part := range path {
		next, ok := current[part].(map[string]any)
		if !ok {
			t.Fatalf("expected capability path %v to contain object at %q, got %#v", path, part, current[part])
		}
		current = next
	}
	got, ok := current[key].([]any)
	if !ok || len(got) != len(want) {
		t.Fatalf("expected capability %v.%s=%#v, got %#v", path, key, want, current[key])
	}
	for i, value := range want {
		if got[i] != value {
			t.Fatalf("expected capability %v.%s[%d]=%q, got %#v", path, key, i, value, got[i])
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
	assertNestedCapability(t, caps, []string{"update", "apply"}, map[string]any{"supported": true, "fallback": false, "handoff": "graceful-restart"})
	assertNestedCapability(t, caps, []string{"update", "applyContract"}, map[string]any{
		"supported":                     true,
		"manualOnlyWhenUnavailable":     false,
		"startsReplacementProcess":      true,
		"usesGracefulRestartHandoff":    true,
		"requiresRegularExecutable":     true,
		"preparedArtifactRequired":      true,
		"requiresSha512Verification":    true,
		"preparedBinaryPrefix":          ".narrafork-update-",
		"verifiesArtifactBeforeHandoff": true,
		"autoApplySmoke":                true,
	})
	assertNestedCapability(t, caps, []string{"update", "successFixtureContract"}, map[string]any{
		"boundaryTracked":                 true,
		"compiledGoArtifactDownloadSmoke": false,
		"zstdPatchFixtureSmoke":           false,
		"patchChainFixtureSmoke":          false,
		"autoApplyHandoffSmoke":           true,
		"rollbackDrillCIWired":            false,
		"fullTSSelfUpdateParity":          false,
		"releaseUploadPolicy":             "metadata-plus-zstd-patch-only",
	})
	assertNestedCapability(t, caps, []string{"update", "replacementContract"}, map[string]any{
		"boundaryTracked":                 true,
		"runtimeRow":                      "UPDATE-SELF-APPLY-01",
		"manualOnly":                      false,
		"selfUpdateAvailable":             true,
		"autoApplyHandoffSmoke":           true,
		"compiledGoArtifactDownloadSmoke": false,
		"zstdPatchFixtureSmoke":           false,
		"patchChainFixtureSmoke":          false,
		"rollbackDrillCIWired":            false,
		"fullTSSelfUpdateParity":          false,
		"replacementSafe":                 false,
	})
}

func newRouterContractFixture(t *testing.T) (http.Handler, *auth.Service) {
	t.Helper()
	router, authService, _ := newRouterContractFixtureWithDB(t)
	return router, authService
}

func newRouterContractFixtureWithDB(t *testing.T) (http.Handler, *auth.Service, *sql.DB) {
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
	return router, authService, sqlDB
}

func mustExecRouterContractSQL(t *testing.T, sqlDB *sql.DB, statement string) {
	t.Helper()
	if _, err := sqlDB.Exec(statement); err != nil {
		t.Fatalf("exec fixture SQL: %v", err)
	}
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
