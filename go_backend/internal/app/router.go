package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/buildinfo"
	"narrafork-go/internal/chapteredges"
	"narrafork-go/internal/chapters"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	"narrafork-go/internal/explorations"
	"narrafork-go/internal/favorites"
	"narrafork-go/internal/fs"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/gitroutes"
	"narrafork-go/internal/graph"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/mcpcompat"
	"narrafork-go/internal/misccompat"
	"narrafork-go/internal/narrators"
	"narrafork-go/internal/projects"
	"narrafork-go/internal/restart"
	"narrafork-go/internal/search"
	settingshandler "narrafork-go/internal/settings"
	"narrafork-go/internal/simplecontent"
	"narrafork-go/internal/systemroutes"
	"narrafork-go/internal/terminalruntime"
	"narrafork-go/internal/terminals"
	"narrafork-go/internal/userprefs"
	"narrafork-go/internal/ws"
)

type Services struct {
	Settings        *config.Store
	DB              *sql.DB
	RuntimeDB       *sql.DB
	DBEngine        db.Engine
	Git             *gitx.Service
	Auth            *auth.Service
	Hub             *ws.Hub
	Projects        *projects.Service
	Chapters        *chapters.Service
	Narrators       *narrators.Service
	TerminalRuntime *terminalruntime.Manager
	Restart         *restart.Manager
	Gateway         systemroutes.GatewayRuntime
	ContainerProxy  settingshandler.ContainerProxyRuntime
}

func NewRouter(s Services) http.Handler {
	if s.Narrators != nil && s.RuntimeDB != nil {
		s.Narrators.SetRuntimeDB(s.RuntimeDB)
	}
	if s.Hub != nil && s.RuntimeDB != nil {
		s.Hub.SetBrowserSessionDB(s.RuntimeDB)
	}
	r := chi.NewRouter()
	r.Use(httpx.Recover)
	r.Use(httpx.RequestLogger)
	r.Use(httpx.CORS)

	r.Get("/api/health", healthHandler(s.Git, s.TerminalRuntime, s.DBEngine, s.Restart, s.Gateway, s.Narrators))
	r.Post("/api/gracefully_shutdown", httpx.Handler(gracefullyShutdownHandler(s.Restart)))
	r.Get("/api/auth/status", authStatusHandler(s.DB, s.Settings))

	authHandler := auth.NewHandler(s.Auth)
	r.Route("/api/auth", authHandler.RegisterRoutes)

	publicMisc := misccompat.NewHandler(s.DB, s.Settings)
	r.Route("/api/shares", func(sr chi.Router) {
		publicMisc.RegisterPublicShares(sr)
		sr.Group(func(private chi.Router) {
			private.Use(s.Auth.Middleware)
			publicMisc.RegisterShareCreate(private)
		})
	})
	r.Get("/api/changelog", httpx.Handler(changelogHandler))
	registerDependencyRoutes(r, s.Auth, s.Git)
	publicSystem := systemroutes.NewHandler(s.DB, s.Settings)
	publicSystem.RuntimeDB = s.RuntimeDB
	publicSystem.Narrators = s.Narrators
	publicSystem.Gateway = s.Gateway
	r.Get("/api/nug/oauth/callback", httpx.Handler(publicSystem.NugOAuthCallback))
	r.Route("/api/gateway", func(gr chi.Router) {
		publicSystem.RegisterGatewayPublic(gr)
		gr.Group(func(private chi.Router) {
			private.Use(s.Auth.Middleware)
			publicSystem.RegisterGateway(private)
		})
	})

	r.Group(func(api chi.Router) {
		api.Use(s.Auth.Middleware)
		api.Use(gitRequiredGuard(s.Git))

		if s.Chapters != nil && s.Hub != nil {
			s.Chapters.SetChapterLifecycleBroadcasters(s.Hub.BroadcastMergeProgress, s.Hub.BroadcastUser)
		}
		if s.Projects != nil && s.Hub != nil {
			s.Projects.SetUserBroadcaster(s.Hub.BroadcastUser)
		}
		if s.Projects != nil && s.ContainerProxy != nil {
			if proxyRefresher, ok := s.ContainerProxy.(interface{ EnsureCurrent(context.Context) error }); ok {
				s.Projects.SetProxyCacheRefresher(proxyRefresher.EnsureCurrent)
			}
		}
		projectHandler := projects.NewHandler(s.Projects)
		chapterHandler := chapters.NewHandler(s.Chapters, s.Hub)
		narratorHandler := narrators.NewHandler(s.Narrators)
		if s.Hub != nil {
			narratorHandler.Presence = s.Hub
		}
		settingsHandler := settingshandler.NewHandler(s.Settings)
		settingsHandler.ProxyRuntime = s.ContainerProxy
		settingsHandler.Restart = s.Restart
		graphHandler := graph.NewHandler(s.DB, s.Git)
		gitHandler := gitroutes.NewHandler(s.DB, s.Git, s.Settings)
		gitHandler.Broadcaster = s.Hub
		fsHandler := fs.NewHandler(s.Settings)
		searchHandler := search.NewHandlerWithEngine(s.DB, s.DBEngine)
		terminalHandler := terminals.NewHandler(s.DB, s.TerminalRuntime, s.Hub)
		userPrefsHandler := userprefs.NewHandler(s.DB, s.Hub)
		chapterEdgeHandler := chapteredges.NewHandler(s.DB)
		if s.Hub != nil {
			chapterEdgeHandler.BroadcastDependencyEvent = s.Hub.BroadcastProjectEvent
		}
		if s.Projects != nil {
			chapterEdgeHandler.SyncProjectDBAfterDependencyChange = func(projectID string) {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					if _, err := s.Projects.SyncBackup(ctx, projectID); err != nil {
						log.Printf("chapteredges: dependency project DB sync failed for %s: %v", projectID, err)
					}
				}()
			}
		}
		explorationHandler := explorations.NewHandler(s.DB, s.Chapters)
		favoriteHandler := favorites.NewHandler(s.DB)
		simpleHandler := simplecontent.NewHandlerWithDB(s.Settings, s.DB)
		systemHandler := systemroutes.NewHandler(s.DB, s.Settings, s.TerminalRuntime)
		systemHandler.RuntimeDB = s.RuntimeDB
		systemHandler.Narrators = s.Narrators
		systemHandler.VNet = s.Hub
		systemHandler.Restart = s.Restart
		systemHandler.Gateway = s.Gateway
		miscHandler := misccompat.NewHandler(s.DB, s.Git, s.Chapters, s.Narrators, s.Settings)
		mcpHandler := mcpcompat.NewHandler(s.DB, s.Chapters, s.Narrators, s.Settings)
		if s.Narrators != nil {
			s.Narrators.ExternalTools = mcpHandler
		}

		api.Route("/api/projects", func(r chi.Router) {
			projectHandler.RegisterRoutes(r)
			graphHandler.RegisterRoutes(r)
			miscHandler.RegisterRuler(r)
			miscHandler.RegisterVolumeSnapshots(r)
		})
		api.Route("/api/chapters", func(r chi.Router) {
			chapterHandler.RegisterRoutes(r)
			gitHandler.RegisterRoutes(r)
		})
		api.Route("/api/narrators", narratorHandler.RegisterRoutes)
		api.Route("/api/settings", settingsHandler.RegisterRoutes)
		api.Route("/api/fs", fsHandler.RegisterRoutes)
		api.Route("/api/search", searchHandler.RegisterRoutes)
		api.Route("/api/terminals", terminalHandler.RegisterRoutes)
		api.Route("/api/user-preferences", userPrefsHandler.RegisterRoutes)
		api.Route("/api/chapter-edges", chapterEdgeHandler.RegisterRoutes)
		api.Route("/api/exploration-groups", explorationHandler.RegisterRoutes)
		api.Route("/api/favorites", favoriteHandler.RegisterRoutes)
		api.Route("/api/learning", simpleHandler.RegisterLearning)
		api.Route("/api/routines", simpleHandler.RegisterRoutines)
		api.Route("/api/skills", simpleHandler.RegisterSkills)
		api.Route("/api/workspaces", miscHandler.RegisterWorkspaces)
		api.Route("/api/hooks", func(r chi.Router) {
			r.Use(adminContextOnly)
			miscHandler.RegisterHooks(r)
		})
		api.Route("/api/custom-subagents", miscHandler.RegisterCustomSubagents)
		api.Route("/api/uploads", miscHandler.RegisterUploads)
		api.Route("/api/storage", miscHandler.RegisterStorage)
		api.Route("/api/notifications", miscHandler.RegisterNotifications)
		api.Route("/api/notification-sounds", miscHandler.RegisterNotificationSounds)
		api.Route("/api/reviews", miscHandler.RegisterReviews)
		api.Route("/api/volume-snapshots", miscHandler.RegisterVolumeSnapshotRoot)
		api.Route("/api/mcp", mcpHandler.RegisterRoutes)
		api.Route("/api/admin", systemHandler.RegisterAdmin)
		api.Route("/api/update", systemHandler.RegisterUpdate)
		api.Route("/api/runtime", systemHandler.RegisterRuntime)
		api.Route("/api/usage-history", systemHandler.RegisterUsageHistory)
		api.Route("/api/vnet", systemHandler.RegisterVNet)
		api.Route("/api/benchmarks", systemHandler.RegisterBenchmarks)
		api.Route("/api/project-db", systemHandler.RegisterProjectDB)
		systemHandler.RegisterProviders(api)
		providerRouter := chi.NewRouter()
		systemHandler.RegisterProviders(providerRouter)
		for _, prefix := range []string{"/api/kiro", "/api/openai", "/api/anthropic", "/api/nkp", "/api/nug", "/api/codex", "/api/cline"} {
			api.Handle(prefix+"/*", http.StripPrefix("/api", providerRouter))
		}

		registerKnownPlaceholders(api)
	})

	r.Get("/ws/narrator", s.Hub.NarratorHandler(s.Auth))
	r.Get("/ws/terminal", s.Hub.TerminalHandler(s.Auth))
	r.Get("/ws/vnet", s.Hub.VNetHandler(s.Auth, s.Settings))

	r.Handle("/*", staticFrontendHandler())

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "API route not found", "code": "NOT_FOUND"})
			return
		}
		http.NotFound(w, r)
	})

	return r
}

func adminContextOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.UserFromContext(r.Context())
		if claims == nil || claims.Role != "admin" {
			httpx.WriteError(w, httpx.Forbidden("Admin access required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func gitRequiredGuard(git *gitx.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !requiresGit(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if git != nil && git.Available(ctx) {
				next.ServeHTTP(w, r)
				return
			}
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "Git is not installed. Please install git and retry this Git-dependent action.",
				"code":  "GIT_NOT_INSTALLED",
			})
		})
	}
}

func requiresGit(method string, path string) bool {
	upperMethod := strings.ToUpper(method)
	path = strings.TrimSuffix(path, "/")
	if upperMethod == http.MethodPost && path == "/api/projects" {
		return true
	}
	if strings.HasPrefix(path, "/api/projects/") && strings.Contains(path, "/ruler") {
		parts := strings.Split(path, "/")
		return len(parts) >= 5 && parts[1] == "api" && parts[2] == "projects" && parts[4] == "ruler"
	}
	if upperMethod == http.MethodPost && path == "/api/chapters" {
		return true
	}
	if upperMethod == http.MethodPost && (path == "/api/chapters/cleanup" || path == "/api/chapters/batch-merge") {
		return true
	}
	if strings.HasPrefix(path, "/api/chapters/") {
		parts := strings.Split(path, "/")
		if len(parts) == 4 && upperMethod == http.MethodDelete && parts[1] == "api" && parts[2] == "chapters" && parts[3] != "" {
			return true
		}
		if len(parts) >= 5 && parts[1] == "api" && parts[2] == "chapters" && parts[3] != "" {
			action := parts[4]
			if action == "git" {
				return true
			}
			if upperMethod == http.MethodGet || upperMethod == http.MethodPost {
				switch action {
				case "fork", "batch-fork", "split", "cherry-pick", "dependency-status", "sync-upstream", "review", "merge-check", "merge", "ai-resolve", "unmerge", "dormant", "wake", "git-status":
					return true
				}
			}
			if upperMethod == http.MethodGet && action == "commits" && len(parts) >= 7 && parts[6] == "files" {
				return true
			}
		}
	}
	return false
}

func healthHandler(git *gitx.Service, terminalRuntime *terminalruntime.Manager, engine db.Engine, restartManager *restart.Manager, gatewayRuntime systemroutes.GatewayRuntime, gatewayNarrators systemroutes.GatewayNarratorRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		platform := runtime.GOOS
		switch platform {
		case "darwin":
			platform = "macos"
		case "windows":
			platform = "windows"
		default:
			platform = "linux"
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		gitAvailable := false
		if git != nil {
			gitAvailable = git.Available(ctx)
		}
		var terminalCapabilities map[string]any
		if terminalRuntime != nil {
			terminalCapabilities = terminalRuntime.Capabilities()
		}
		canApplySelfUpdate := canApplySelfUpdateFromHealth(restartManager)
		gatewayRuntimeAttached := gatewayRuntime != nil
		gatewayNarratorRuntimeAttached := gatewayNarrators != nil
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":       "ok",
			"version":      buildinfo.VersionValue(),
			"commit":       buildinfo.CommitValue(),
			"backend":      buildinfo.Backend,
			"buildChannel": buildinfo.ChannelValue(),
			"platform":     platform,
			"gitAvailable": gitAvailable,
			"capabilities": runtimeCapabilities(platform, terminalCapabilities, engine, canApplySelfUpdate, gatewayRuntimeAttached, gatewayNarratorRuntimeAttached),
		})
	}
}

const terminalXtermSerializedReplayReason = "Go backend replays capped raw PTY output instead of TS xterm-headless serialized buffer state."

const terminalDtachFallbackReason = "Go uses dtach only when the platform/runtime has a live dtach socket; otherwise it falls back to direct PTY or marks stale running terminals exited on startup."

func phase4TerminalCapabilities(platform string, runtimeCaps map[string]any) map[string]any {
	terminal := map[string]any{
		"supported":                   platform != "windows",
		"directPty":                   platform != "windows",
		"windowsPty":                  false,
		"dtachSupported":              platform != "windows",
		"dtachAvailable":              false,
		"detachedReattach":            platform != "windows",
		"orphanRecovery":              platform != "windows",
		"scrollbackReplay":            true,
		"scrollbackReplayMode":        "raw-capped-buffer",
		"bufferStateReplay":           true,
		"bufferStateMode":             "raw-capped-buffer",
		"xtermSerializedReplay":       false,
		"xtermSerializedReplayReason": terminalXtermSerializedReplayReason,
		"maxSnapshotBytes":            1 << 20,
		"multiClientResizeMode":       "last-input-client",
		"ws": map[string]any{
			"subscribe": true, "create": true, "input": true, "resize": true,
			"kill": true, "rename": true, "scrollback": true, "bufferState": true,
			"clientMessages": []string{
				"subscribe",
				"unsubscribe",
				"create",
				"input",
				"resize",
				"kill",
				"rename",
			},
			"serverEvents": []string{
				"ready",
				"snapshot",
				"scrollback",
				"bufferState",
				"requestResize",
				"created",
				"renamed",
				"killed",
				"output",
				"exit",
				"error",
			},
			"subscribeReplayEvents": []string{"ready", "snapshot", "scrollback", "bufferState", "exit"},
			"snapshotFallback": map[string]any{
				"code":               "TERMINAL_SNAPSHOT_UNAVAILABLE",
				"fallback":           true,
				"fatal":              false,
				"reasonErrorMessage": true,
			},
			"resizeScope": "last-input-client",
		},
		"processTree": map[string]any{
			"supported": platform != "windows",
			"platform":  map[bool]string{true: "unix-ps", false: "unsupported"}[platform != "windows"],
		},
	}
	terminal["http"] = map[string]any{
		"supported":             true,
		"basePath":              "/api/terminals",
		"auth":                  "api-authenticated",
		"gitGuarded":            false,
		"collectionRoutes":      true,
		"tabRoutes":             true,
		"viewStateRoutes":       true,
		"processRoute":          true,
		"tsValidationParity":    "partial",
		"validationMode":        "go-loose-json-with-explicit-current-gaps",
		"frontendContractSmoke": "http-crud-tabs-view-process-sampled",
	}
	terminal["httpValidationGapContract"] = map[string]any{
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
		"unclaimedValidationFamilies": []string{
			"tab-create-exactly-one-target",
			"view-state-put-target-required",
			"view-state-layout-enum",
			"view-state-panel-assignments",
			"process-snapshot-ts-empty-error-fallback",
			"broader-terminal-panel-frontend-smoke",
		},
		"sampledValidationFamilies": []string{
			"crud-create-target-validation",
			"terminal-count-broadcasts",
			"tab-list-create-update-delete-reorder",
			"view-state-default-read-and-upsert",
			"process-empty-orphan-runtime-snapshot",
		},
	}
	terminal["httpRoutes"] = map[string]any{
		"list":           "GET /api/terminals?chapterId|narratorId",
		"create":         "POST /api/terminals",
		"get":            "GET /api/terminals/{id}",
		"patch":          "PATCH /api/terminals/{id}",
		"delete":         "DELETE /api/terminals/{id}",
		"listTabs":       "GET /api/terminals/tabs?chapterId|narratorId",
		"createTab":      "POST /api/terminals/tabs",
		"updateTab":      "PATCH /api/terminals/tabs/{id}",
		"deleteTab":      "DELETE /api/terminals/tabs/{id}",
		"reorderTabs":    "PUT /api/terminals/tabs/reorder",
		"getViewState":   "GET /api/terminals/view-state?chapterId|narratorId",
		"putViewState":   "PUT /api/terminals/view-state",
		"getProcesses":   "GET /api/terminals/{id}/processes",
		"staticBeforeID": true,
	}
	terminal["crudContract"] = map[string]any{
		"listRequiresScope":                   true,
		"listScopePrecedence":                 "chapterId-over-narratorId",
		"listOrder":                           "created_at-asc",
		"createStatus":                        201,
		"createRequiresExactlyOneTarget":      true,
		"createTargetValidation":              "exactly-one-chapter-or-narrator",
		"createDefaultName":                   "Terminal",
		"createDefaultCols":                   80,
		"createDefaultRows":                   24,
		"createUsesResolvedCwd":               true,
		"missingChapterStatus":                404,
		"missingNarratorStatus":               404,
		"dormantChapterStatus":                400,
		"ambiguousTargetStatus":               400,
		"runtimeStartRollback":                true,
		"failedRollbackMayLeaveDiagnosticRow": true,
		"patchReturnsTerminal":                true,
		"patchStatusBroadcast":                true,
		"deleteReturnsOk":                     true,
		"deleteWithoutRuntimeMarksExited":     true,
		"deleteWithRuntimeKills":              true,
		"deleteBroadcastsTerminalCount":       true,
		"tsValidationParity":                  "partial",
		"looseGraphValueValidation":           true,
	}
	terminal["tabContract"] = map[string]any{
		"listRequiresScope":                true,
		"listScopePrecedence":              "chapterId-over-narratorId",
		"listOrder":                        "sort_order-asc-created_at-asc",
		"createStatus":                     201,
		"createRequiresName":               true,
		"createSortOrder":                  "max-plus-one",
		"createSortOrderQueryBeforeInsert": true,
		"createRequiresExactlyOneTarget":   false,
		"createTargetValidationGap":        "go-currently-allows-missing-or-ambiguous-tab-target",
		"updateReturnsTab":                 true,
		"emptyNamePatchNoop":               true,
		"deleteReturnsOk":                  true,
		"deleteMissingStatus":              404,
		"reorderTransactional":             true,
		"reorderReturnsOk":                 true,
		"tsValidationParity":               "partial",
	}
	terminal["viewStateContract"] = map[string]any{
		"authenticated":              true,
		"scopePrecedence":            "chapterId-over-narratorId",
		"getDefaultWhenMissing":      true,
		"getDefaultLayout":           "single",
		"getDefaultActiveTabNull":    true,
		"getDefaultAssignmentsNull":  true,
		"putUpsertByUserAndScope":    true,
		"putDefaultLayout":           "single",
		"targetRequiredOnPut":        false,
		"layoutEnumValidation":       false,
		"panelAssignmentsValidation": false,
		"tsValidationParity":         "partial",
	}
	terminal["processTreeContract"] = map[string]any{
		"route":                      "GET /api/terminals/{id}/processes",
		"emptyWhenNotRunning":        true,
		"emptyWhenNoRuntime":         true,
		"emptyWhenNoShellPID":        true,
		"windowsEmptyFallback":       true,
		"unixPsTimeoutSeconds":       3,
		"maxSnapshotBytes":           1 << 20,
		"surfaceSnapshotErrors":      true,
		"tsErrorFallbackParity":      false,
		"batchProcessSnapshotParity": false,
	}
	terminal["countBroadcastContract"] = map[string]any{
		"event":                           "terminal_count_changed",
		"onCreate":                        true,
		"onDelete":                        true,
		"onStatusPatch":                   true,
		"chapterScopeBroadcastsNarrators": true,
		"activeTerminalCountField":        true,
		"countAliasField":                 true,
		"degradedFallback":                true,
		"queryFailureFallback":            "terminal_count_query_failed",
	}
	for key, value := range runtimeCaps {
		terminal[key] = value
	}
	dtachSupported, _ := terminal["dtachSupported"].(bool)
	dtachAvailable, _ := terminal["dtachAvailable"].(bool)
	terminal["replayContract"] = map[string]any{
		"mode":                     "raw-capped-buffer",
		"scrollbackReplay":         true,
		"bufferStateReplay":        true,
		"xtermSerializedReplay":    false,
		"xtermSerializedStateKeys": false,
		"tsXtermHeadlessParity":    false,
		"maxSnapshotBytes":         1 << 20,
		"multiClientResizeMode":    "last-input-client",
		"requestResizeAfterInput":  true,
		"frontendContractSmoke":    true,
		"rawSnapshotFixture":       "focused-runtime-snapshot",
		"reason":                   terminalXtermSerializedReplayReason,
	}
	terminal["dtachFallback"] = map[string]any{
		"supported":                                dtachSupported,
		"available":                                dtachAvailable,
		"directPtyFallback":                        true,
		"detachedReattachRequiresLiveSocket":       true,
		"startupMarksRunningExitedWhenUnavailable": true,
		"orphanImportRequiresAliveSocket":          true,
		"killOrphanNoopWithoutDtach":               true,
		"reason":                                   terminalDtachFallbackReason,
	}
	terminal["replacementContract"] = map[string]any{
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
		"sampledProofs": []string{
			"raw-capped-reconnect-snapshot",
			"ws-client-server-event-list",
			"nonfatal-snapshot-fallback",
			"last-input-client-resize",
			"http-crud-tabs-view-process-sampled",
			"dtach-direct-pty-fallback-metadata",
		},
		"requiredProofs": []string{
			"ts-xterm-serialized-buffer-replay-or-accepted-raw-replay-product-decision",
			"durable-dtach-live-socket-recovery-smoke",
			"windows-conpty-platform-smoke",
			"broader-terminal-panel-browser-smoke",
			"process-snapshot-empty-error-fallback-parity",
			"ts-go-terminal-ws-http-diff-smoke",
		},
		"reason": "Wave 118 keeps Terminal replacement unsafe until raw-capped replay, opportunistic dtach fallback, partial HTTP validation, process snapshot fallback drift, Windows/ConPTY behavior, and broader frontend terminal panel smoke are either TS-equivalent or explicitly accepted as degraded product behavior.",
	}
	if platform == "windows" && terminal["supported"] == false {
		terminal["reason"] = "Go terminal PTY runtime is not enabled on Windows; use the TypeScript backend or a Unix-like host for interactive terminals."
	}
	return terminal
}

const disabledStaleNarratorSessionCleanupReason = "staleSessions cleanup is disabled by the archived-only deletion policy: explicitly archive narrators first, then run archivedSessions cleanup. Preview remains available."

const shareEscapedHTMLPreviewReason = "Go public share preview escapes HTML and renders it in a preformatted block instead of using the TypeScript sanitize-html preview pipeline."

const shareEphemeralFallbackReason = "Go ShareFile uses disk metadata as an in-process bridge for public share routes; startup cleanup removes previous-run share directories and metadata."

const mcpNonStdioTransportReason = "Go compatibility mode only connects stdio MCP servers."

const browserSessionsDefaultOffReason = "Go browser sessions are opt-in native extensions backed by go-rod and require Chrome/Chromium; enable sessions explicitly and disable or export artifacts to roll back."

const containerBackgroundStartReason = "Go container start requests return immediately and continue compose startup in the background while progress is reported through logs and status updates."

const containerBrowserToolAutoEnableReason = "Go native container Browser tool auto-enable is default-off; enable browser tools explicitly after validating the native extension/container browser runtime."

func storageDatabaseMaintenanceCapability() map[string]any {
	return map[string]any{
		"preview": true,
		"cleanup": true,
		"routeContract": map[string]any{
			"adminOnly": true,
			"routes":    []string{"POST /api/storage/database/preview", "POST /api/storage/database/cleanup", "POST /api/storage/database/vacuum"},
			"targets":   []string{"archivedSessions", "staleSessions", "apiRequestDumps"},
		},
		"requestContract": map[string]any{
			"previewOptionalBody":        true,
			"cleanupOptionalBody":        true,
			"defaultSampleLimit":         float64(10),
			"tsSampleLimitMax":           float64(25),
			"goSampleLimitMaxEnforced":   false,
			"tsOlderThanDaysMax":         float64(3650),
			"goOlderThanDaysMaxEnforced": false,
			"invalidTargetStatus":        float64(http.StatusBadRequest),
			"tsZodParity":                "partial",
		},
		"cleanupTargets": map[string]any{
			"apiRequestDumps": map[string]any{
				"supported":                   true,
				"clearsRawDumpJson":           true,
				"deletesApiRequestRows":       false,
				"defaultRetentionDays":        30,
				"previewSupported":            true,
				"warningCodes":                []string{"deletesUsageHistory"},
				"vacuumRan":                   false,
				"fullTSDatabaseCleanupParity": false,
			},
			"archivedSessions": map[string]any{
				"supported":                          true,
				"fallback":                           false,
				"archivedOnly":                       true,
				"usesNarratorDelete":                 true,
				"requiresNarratorService":            true,
				"missingNarratorServiceFallbackCode": "FEATURE_DISABLED",
				"previewSupported":                   true,
				"vacuumRan":                          false,
				"fullTSDatabaseCleanupParity":        false,
			},
			"staleSessions": map[string]any{
				"supported":            false,
				"fallback":             true,
				"code":                 "FEATURE_DISABLED",
				"previewSupported":     true,
				"cleanupSupported":     false,
				"archivedOnlyPolicy":   true,
				"defaultRetentionDays": 90,
				"reason":               disabledStaleNarratorSessionCleanupReason,
			},
		},
		"previewContract": map[string]any{
			"responseFields":   []string{"target", "olderThanDays", "approxBytes", "oldestAt", "counts", "blockedCount", "warningCodes", "fallbacks", "degraded", "samples", "blocked", "count"},
			"countFields":      []string{"sessions", "narrators", "descendantNarrators", "messages", "toolCalls", "apiRequests", "dumpsCleared"},
			"sampleTypes":      []string{"narrator", "apiRequest"},
			"degradedFields":   []string{"degraded", "fallback", "fallbacks", "warningCodes", "reason", "error", "message"},
			"fallbackCodes":    []string{"api_request_dump_stats_failed", "cleanup_narrator_id_query_failed", "cleanup_message_count_failed", "cleanup_tool_call_count_failed", "cleanup_api_request_count_failed", "cleanup_oldest_activity_failed"},
			"maxNarratorIDs":   float64(500),
			"truncatedWarning": "cleanupPreviewLimited",
		},
		"executionContract": map[string]any{
			"responseFields":                 []string{"ok", "target", "olderThanDays", "approxBytes", "oldestAt", "counts", "blockedCount", "warningCodes", "samples", "blocked", "count", "beforeBytes", "afterBytes", "freedBytes", "vacuumRan", "changed"},
			"apiRequestDumpsMutation":        "clear-raw_dump_json-only",
			"apiRequestRowsDeleted":          false,
			"archivedSessionsMutation":       "safe-narrator-delete-service",
			"staleSessionsMutation":          "disabled-archived-only-policy",
			"storageCacheInvalidationParity": false,
			"vacuumRan":                      false,
		},
		"fallbackContract": map[string]any{
			"featureDisabledStatus":                  float64(http.StatusNotImplemented),
			"featureDisabledFields":                  []string{"ok", "code", "feature", "supported", "fallback", "reason", "error", "message"},
			"staleSessionsFeature":                   "storage.database.cleanup.staleSessions",
			"archivedSessionsMissingNarratorFeature": "storage.database.cleanup.archivedSessions",
			"previewDegradesInsteadOfThrowing":       true,
			"invalidTargetStatus":                    float64(http.StatusBadRequest),
		},
		"maintenanceContract": map[string]any{
			"supported":                          true,
			"previewRoute":                       true,
			"cleanupRoute":                       true,
			"targets":                            []string{"archivedSessions", "staleSessions", "apiRequestDumps"},
			"apiRequestDumpsClearsRawDumpJson":   true,
			"archivedSessionsArchivedOnly":       true,
			"archivedSessionsUsesNarratorDelete": true,
			"staleSessionsCleanupSupported":      false,
			"staleSessionsPreviewSupported":      true,
			"requestPathVacuumSupported":         false,
			"requestPathVacuumStatus":            409,
			"vacuumRan":                          false,
			"checkpointRan":                      false,
			"optimized":                          false,
			"mainThreadVacuumDisabled":           true,
			"fullTSDatabaseMaintenanceParity":    false,
			"reason":                             "Go database maintenance supports bounded preview and selected cleanup targets, but request-path VACUUM is disabled and stale session cleanup is kept behind the archived-only deletion policy.",
		},
		"vacuum": map[string]any{
			"supported":                false,
			"fallback":                 true,
			"status":                   409,
			"vacuumRan":                false,
			"checkpointRan":            false,
			"optimized":                false,
			"mainThreadDisabled":       true,
			"backgroundJobImplemented": false,
			"reason":                   "SQLite VACUUM is disabled in the request path and must be implemented as a background maintenance job before Go can claim TS-equivalent database compaction.",
		},
	}
}

func runtimeCapabilities(platform string, terminalCapabilities map[string]any, engine db.Engine, canApplySelfUpdate bool, gatewayRuntimeAttached bool, gatewayNarratorRuntimeAttached bool) map[string]any {
	frontendDir, frontendHosted := frontendStaticDir()
	terminal := phase4TerminalCapabilities(platform, terminalCapabilities)
	frontend := map[string]any{"staticHosted": frontendHosted, "mode": "api-only"}
	if frontendHosted {
		frontend["mode"] = "static"
		frontend["directory"] = frontendDir
	}
	fsRevealSupported := platform == "windows" || platform == "macos"
	fsReveal := map[string]any{
		"supported":                     fsRevealSupported,
		"fallback":                      !fsRevealSupported,
		"platform":                      platform,
		"supportedPlatforms":            []string{"windows", "macos"},
		"linuxXDGOpenParity":            false,
		"validatesDirectoryBeforeSpawn": true,
		"routeUnsupportedStatus":        400,
		"routeUnsupportedCode":          "VALIDATION_ERROR",
		"fullTSFileManagerParity":       false,
	}
	if !fsRevealSupported {
		fsReveal["code"] = "FEATURE_DISABLED"
		fsReveal["reason"] = "Opening file manager is not supported on this platform"
	}
	gatewayMode := "fallback-plus-http-webhook"
	if gatewayRuntimeAttached {
		gatewayMode = "persistent-plus-http-webhook"
	}
	sqliteSchemaContract := db.CanonicalSchemaContract()
	database := map[string]any{
		"engine":                             string(db.EngineSQLite),
		"mainSchemaOwner":                    "typescript-drizzle",
		"targetMainSchemaOwner":              "go",
		"currentRepairOwner":                 "typescript-drizzle-and-ts-fts-setup",
		"goMainMigrations":                   false,
		"goEnsureColumns":                    false,
		"ftsRepair":                          false,
		"mode":                               "ts-owned-db-compatibility",
		"strategyStage":                      "transition",
		"transitionalStrategy":               true,
		"targetMode":                         "pure-go-db-management",
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
		"futureGoResponsibilities":           []string{"main schema migrations", "startup backfills and repair", "FTS/search lifecycle", "schema drift fixtures and release gates"},
		"canonicalSchemaHash":                sqliteSchemaContract.CanonicalSchemaHash,
		"canonicalColumnMetadataCount":       sqliteSchemaContract.CanonicalColumnMetadataCount,
		"canonicalTableCount":                sqliteSchemaContract.CanonicalTableCount,
		"canonicalIndexCount":                sqliteSchemaContract.CanonicalIndexCount,
		"canonicalForeignKeyCount":           sqliteSchemaContract.CanonicalForeignKeyCount,
		"canonicalFTSTableCount":             sqliteSchemaContract.CanonicalFTSTableCount,
		"canonicalFTSTriggerCount":           sqliteSchemaContract.CanonicalFTSTriggerCount,
		"expectedMigrationHash":              sqliteSchemaContract.ExpectedMigrationHash,
		"expectedMigrationCount":             sqliteSchemaContract.ExpectedMigrationCount,
		"expectedLastMigration":              sqliteSchemaContract.ExpectedLastMigration,
		"drizzleJournalTable":                sqliteSchemaContract.DrizzleJournalTable,
		"goJournalTable":                     sqliteSchemaContract.GoJournalTable,
		"schemaContractDepth":                "representative-critical-subset",
		"fullColumnMetadataParity":           false,
		"fullForeignKeyActionParity":         false,
		"fullDrizzleRelationParity":          false,
		"readOnlySchemaVerifier":             true,
		"schemaContractDepthReason":          "Go verifies canonical tables/columns plus representative high-risk column metadata, high-value indexes, core FKs, FTS trigram tables, and triggers, but not every TS column/default/check/enum/FK action/relation yet.",
		"routeSchemaReadiness": map[string]any{
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
			"directHighRiskTables": []string{
				"api_requests",
				"background_tasks",
				"gateway_session_mappings",
				"hooks",
				"narrator_buffered_messages",
				"volume_snapshots",
				"workspaces",
			},
			"optionalGuardedTablesExcluded": []string{
				"custom_subagents",
				"mcp_servers",
				"narrator_browser_sessions",
			},
		},
		"routeSQLStaticAnalysisContract": map[string]any{
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
			"coveredHighRiskTables": []string{
				"api_requests",
				"background_tasks",
				"gateway_session_mappings",
				"hooks",
				"narrator_buffered_messages",
				"volume_snapshots",
				"workspaces",
			},
			"unclaimedDependencyClasses": []string{
				"every-enabled-route-table-dependency",
				"every-enabled-route-column-dependency",
				"per-route-index-dependency",
				"column-default-check-enum-semantics",
				"foreign-key-actions",
				"drizzle-relations",
			},
		},
		"enumWriteContract": map[string]any{
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
			"trackedFamilies": []string{
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
			},
			"representativeWriteTables": []string{
				"chapter_edges",
				"chapters",
				"narrators",
				"narrator_tool_calls",
				"terminal_sessions",
				"hooks",
				"background_tasks",
			},
			"testOnlyDriftExamples": []string{
				"permission_mode=trust-all",
				"narrator_tool_calls.status=approved",
			},
			"remainingGap": "centralize Go enum constants/validators and prove all production direct SQL writes stay within TS enum sets",
		},
		"migrationCompatibilityContract": map[string]any{
			"supported":                          true,
			"startupGuard":                       true,
			"startupOrder":                       []string{"open-sqlite-file", "configure-pragmas", "open-runtime-sidecar", "VerifyCanonicalSchema", "EnsureMigrationCompatibility"},
			"defaultEntrypoint":                  "EnsureMigrationCompatibility",
			"strictEntrypoint":                   "EnsureMigrationCompatibilityStrict",
			"drizzleJournalTable":                sqliteSchemaContract.DrizzleJournalTable,
			"goJournalTable":                     sqliteSchemaContract.GoJournalTable,
			"goJournalLocation":                  "runtime-sidecar-for-store-open; main-db-only-for-legacy-helper",
			"goJournalContractVersion":           1,
			"expectedMigrationHash":              sqliteSchemaContract.ExpectedMigrationHash,
			"expectedMigrationCount":             sqliteSchemaContract.ExpectedMigrationCount,
			"expectedLastMigration":              sqliteSchemaContract.ExpectedLastMigration,
			"acceptedJournalFamilies":            []string{"bun-final-history", "current-repository-baseline"},
			"writesBusinessSchema":               false,
			"writesDrizzleJournal":               false,
			"recordsGoCompatibilityJournal":      true,
			"recordsForwardExtraHashesDefault":   true,
			"defaultAllowsExtraDrizzleHashes":    true,
			"strictRejectsExtraDrizzleHashes":    true,
			"strictCIWired":                      false,
			"fullDrizzleMigrationRunnerParity":   false,
			"rollbackSafeBeforeJournalOnFailure": true,
		},
		"migrationStrictReleaseContract": map[string]any{
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
		},
		"migrationFailureContract": map[string]any{
			"missingDatabase": map[string]any{
				"mode":       "fail-fast-before-open",
				"goMutation": false,
				"userAction": "start TypeScript backend once to create and migrate the DB",
			},
			"missingDrizzleJournal": map[string]any{
				"mode":       "startup-readiness-error",
				"errorClass": "ErrMigrationCompatibilityMismatch",
				"userAction": "run bun run db:migrate or start TypeScript backend once",
			},
			"missingExpectedDrizzleHash": map[string]any{
				"mode":       "startup-readiness-error",
				"errorClass": "ErrMigrationCompatibilityMismatch",
				"userAction": "start TypeScript backend once so missing migrations are applied",
			},
			"extraUnknownDrizzleHashDefault": map[string]any{
				"mode":           "allow-after-canonical-schema-pass",
				"recordedIn":     sqliteSchemaContract.GoJournalTable,
				"businessSchema": "unchanged",
			},
			"extraUnknownDrizzleHashStrict": map[string]any{
				"mode":       "startup-readiness-error",
				"errorClass": "ErrMigrationCompatibilityMismatch",
				"userAction": "refresh the embedded Go migration snapshot or run compatibility mode intentionally",
			},
			"canonicalSchemaMismatch": map[string]any{
				"mode":       "startup-readiness-error",
				"errorClass": "ErrCanonicalSchemaMismatch",
				"userAction": "start TypeScript backend once to run migrations/backfills/FTS setup",
			},
			"ftsObjectMismatch": map[string]any{
				"mode":       "startup-readiness-error",
				"errorClass": "ErrCanonicalSchemaMismatch",
				"userAction": "start TypeScript backend once so ensureFts can validate or rebuild objects",
			},
		},
		"repairContract": map[string]any{
			"repairOwner":                      "typescript-drizzle-and-ts-fts-setup",
			"goMainSchemaRepair":               false,
			"goEnsureColumns":                  false,
			"goStartupBackfills":               false,
			"goFTSRepair":                      false,
			"goOldDataRepair":                  false,
			"typeScriptRepairSteps":            []string{"runMigrations", "ensureColumns", "narrator status/trait backfills", "ensureFts"},
			"goActionAfterRepair":              "restart go_backend after TypeScript repair completes",
			"remainingFailureAfterRepairMeans": "real schema drift or unsupported future migration",
		},
		"ftsReadinessContract": map[string]any{
			"startupRequiresFTSObjects":        true,
			"source":                           "VerifyCanonicalSchema",
			"requiredFTSTables":                []string{"chapters_fts", "narrator_messages_fts", "narrators_fts"},
			"requiredFTSTriggersTracked":       true,
			"requiredTokenizer":                "trigram",
			"goCreatesFTSObjects":              false,
			"goRebuildsFTSObjects":             false,
			"goDetectsOldTokenizerForRepair":   false,
			"missingOrIncompatibleFailureMode": "startup-readiness-error",
			"searchLikeFallbackScope":          "request-time fallback only after startup bypass or runtime drift",
			"fullTSFTSLifecycleParity":         false,
		},
		"oldDataBackfillContract": map[string]any{
			"repairOwner":                       "typescript-ensureColumns-and-startup-backfills",
			"goRowDataBackfill":                 false,
			"goLegacyNarratorStatusRepair":      false,
			"goMalformedTraitJSONRepair":        false,
			"goCleanShutdownFTSRebuildRecovery": false,
			"startupDetectionScope":             "schema-readiness-only; legacy row-shape drift remains TS-owned unless it breaks canonical readiness",
			"fullTSBackfillParity":              false,
		},
		"compatibilityBetaProductGate": map[string]any{
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
		},
		"replacementContract": map[string]any{
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
			"sampledProofs": []string{
				"canonical-schema-startup-guard",
				"migration-compatibility-default-and-strict-contract",
				"failure-mode-user-action-contract",
				"typescript-repair-owner-contract",
				"fts-readiness-startup-contract",
				"legacy-helper-non-default-scope",
				"compatibility-beta-product-gate",
			},
			"requiredProofs": []string{
				"go-main-schema-migration-runner",
				"fresh-install-business-schema-and-fts-smoke",
				"strict-migration-release-ci-wired",
				"old-data-backfill-repair-parity",
				"fts-clean-shutdown-rebuild-repair-parity",
				"ts-go-ts-shared-db-rollback-drill",
			},
			"reason": "Wave 178 records the database compatibility-beta product gate: default Go SQLite is TS-migrated-DB-only, requires the TypeScript repair path and UI/release gates, and remains unsafe for standalone replacement until Go schema migrations, FTS setup/repair, old-data backfills, strict release CI, and rollback drill proof are available.",
		},
		"optionalStores": map[string]any{
			"customSubagents": map[string]any{
				"supported":            true,
				"canonicalDBTable":     false,
				"storage":              "filesystem-home-subagents",
				"fileName":             "SUBAGENT.md",
				"emptyWhenMissingRoot": true,
				"legacyTableName":      "custom_subagents",
				"legacyTableUsed":      false,
			},
			"mcpServers": map[string]any{
				"supported":               true,
				"canonicalDBTable":        false,
				"storage":                 "settings.json",
				"legacyTableName":         "mcp_servers",
				"legacyTableUsed":         false,
				"missingSettingsFallback": "FEATURE_DISABLED",
			},
			"browserSessions": map[string]any{
				"supported":              true,
				"canonicalDBTable":       false,
				"storage":                "go-owned-optional-table",
				"tableName":              "narrator_browser_sessions",
				"defaultEnabled":         false,
				"createTableWhenEnabled": true,
				"readinessScope":         "native-extension-runtime-db",
			},
		},
		"migrationFreshnessStrictMode":    true,
		"migrationStrictEntrypoint":       "EnsureMigrationCompatibilityStrict",
		"migrationStrictCIWired":          false,
		"migrationStrictCIReason":         "Strict current-repository migration compatibility exists for CI/release gates, but this health payload does not claim it is wired into every release pipeline yet.",
		"defaultAllowsExtraDrizzleHashes": true,
		"searchMode":                      "sqlite-fts5-with-like-fallback",
		"reason":                          "Transition strategy: SQLite currently uses the TypeScript/Drizzle-created main database with read-only Go compatibility checks; the target is pure Go DB management.",
	}
	search := map[string]any{
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
		"queryContract": map[string]any{
			"route":                   "GET /api/search",
			"queryParam":              "q",
			"entitiesParam":           "entities",
			"limitParam":              "limit",
			"defaultLimit":            50,
			"maxLimit":                100,
			"defaultEntities":         []string{"chapters", "messages", "narrators"},
			"allowedEntities":         []string{"chapters", "messages", "narrators"},
			"invalidEntitiesEmpty":    true,
			"emptyQueryReturnsEmpty":  true,
			"sanitizeSpecialChars":    true,
			"shortQueryRuneThreshold": 3,
		},
		"responseContract": map[string]any{
			"topLevelFields": []string{
				"results",
				"projects",
				"chapters",
				"narrators",
				"degraded",
				"fallbacks",
				"searchMetadata",
			},
			"resultTypes":  []string{"chapter", "message", "narrator"},
			"commonFields": []string{"type", "id", "snippet", "matchField", "matchScore"},
			"chapterFields": []string{
				"title",
				"projectName",
				"status",
				"role",
				"createdAt",
				"updatedAt",
			},
			"messageFields": []string{
				"narratorId",
				"narratorTitle",
				"chapterId",
				"chapterTitle",
				"projectName",
				"model",
				"messageRole",
				"createdAt",
			},
			"narratorFields": []string{
				"title",
				"chapterId",
				"chapterTitle",
				"projectName",
				"status",
				"model",
				"createdAt",
				"updatedAt",
				"lastMessageAt",
			},
		},
		"metadataContract": map[string]any{
			"fields":                  []string{"degraded", "fallbacks", "mode", "ftsReady", "shortQuery", "requestedEntities"},
			"requestedEntitiesSorted": true,
			"shortQueryNotDegraded":   true,
		},
		"fallbackContract": map[string]any{
			"fields":                 []string{"entity", "from", "to", "reason", "error", "fallbackError"},
			"ftsUnavailableReason":   "fts_unavailable",
			"fromFTS5ToLike":         true,
			"reasonErrorMessage":     true,
			"topLevelFallbackMirror": true,
		},
		"rankingContract": map[string]any{
			"ftsRankToPositiveScore":         true,
			"stableSortByScoreThenTimestamp": true,
			"limitCapsResults":               true,
		},
		"smokeContract": map[string]any{
			"tsGoContractHarness":               "go_backend/testtools/ts-go-api-contract-diff.ts",
			"baselineFixtureCoverage":           "minimal-ts-created-cjk-short-query-large-relevance",
			"sampledBaselineTSGoDiffClean":      true,
			"sampledEntities":                   []string{"chapters", "messages", "narrators"},
			"sampledQueries":                    []string{"叙事分叉", "Al", "auroraanchor", "chrononeedle"},
			"concreteResultFieldsSampled":       true,
			"cjkTrigramSubstringSampled":        true,
			"shortQueryLikeSampled":             true,
			"largerRealisticRelevanceSmoke":     true,
			"degradedLargeDataMetadataSmoke":    true,
			"missingCorruptFTSFixtureSmoke":     true,
			"additiveMetadataFrontendTolerance": "frontend-api-and-runtime-status-smoke",
			"fullDBFTS01Parity":                 false,
		},
		"ftsLifecycleProductGate": map[string]any{
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
		},
		"crossEngineProductGate": map[string]any{
			"decisionTracked":               true,
			"acceptedAsCrossEngineDecision": true,
			"sqliteBaseline":                "typescript-created-sqlite-fts5-trigram",
			"postgresMode":                  "go-owned-postgres-like-compatibility",
			"nonSQLiteGoldenParity":         false,
			"routeSupported":                true,
			"replacementImplication":        "blocks-engine-agnostic-search-replacement",
			"postgresCapability":            "search.postgresSearchContract",
		},
		"largeDataRelevanceContract": map[string]any{
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
			"requiredCorpusFeatures": []string{
				"chapter-title-and-description-collisions",
				"narrator-title-collisions",
				"message-body-collisions",
				"cjk-trigram-substrings",
				"timestamp-tie-breakers",
				"missing-or-corrupt-fts-copy",
			},
			"unclaimedDimensions": []string{},
		},
		"fullTSFTSLifecycleParity": false,
		"reason":                   "Go search can use existing TypeScript-created SQLite FTS5/trigram tables and falls back to LIKE with explicit degraded metadata, but it does not create, rebuild, repair, or clean-shutdown-manage FTS objects.",
	}
	if engine == db.EnginePostgres {
		database = map[string]any{
			"engine":                      string(db.EnginePostgres),
			"mainSchemaOwner":             "go-postgres",
			"goMainMigrations":            true,
			"goEnsureColumns":             true,
			"ftsRepair":                   false,
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
			"reason":                      "Postgres is experimental and Go-owned; it is not a TS/Drizzle parity baseline for the SQLite main database, and cross-engine API response compatibility remains unverified.",
		}
		search = map[string]any{
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
			"reason":                     "Postgres search is Go-owned LIKE-based compatibility mode and is not a TypeScript SQLite FTS lifecycle parity baseline.",
		}
	}
	search["replacementContract"] = map[string]any{
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
		"sampledProofs": []string{
			"minimal-ts-created-cjk-query",
			"minimal-short-query-like-fallback",
			"ready-fts-metadata-rank-snippet",
			"invalid-entity-empty-result",
			"explicit-like-fallback-metadata",
			"large-ts-created-auroraanchor-multi-entity-top-k-order",
			"chrononeedle-timestamp-tie-breaker",
			"large-missing-fts-no-5xx-degraded-fallback",
			"large-corrupt-fts-no-5xx-degraded-fallback",
			"frontend-search-api-additive-metadata-tolerance",
			"frontend-search-runtime-status-additive-metadata-tolerance",
			"fts-lifecycle-explicit-product-gating",
			"cross-engine-search-explicit-product-gating",
		},
		"requiredProofs": []string{
			"full-db-fts-01-replacement-acceptance",
		},
		"reason": "Wave 177 adds an explicit cross-engine product gate after Wave 176's FTS lifecycle product gate, while keeping replacement unsafe until full DB-FTS-01 replacement acceptance closes.",
	}
	caps := map[string]any{
		"database": database,
		"search":   search,
		"frontend": frontend,
		"projects": map[string]any{
			"create": map[string]any{
				"supported":                       true,
				"route":                           "POST /api/projects",
				"gitGuarded":                      true,
				"authRequired":                    true,
				"modes":                           []string{"existing", "init", "clone"},
				"jsonCreateStatus":                201,
				"cloneSSE":                        true,
				"cloneSSEEvents":                  []string{"progress", "credential_required", "error", "complete"},
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
				"acceptance":                      "seeded TypeScript-vs-Go project create DB/Git/FS diffs are Wave192-proven; broader full TS project create lifecycle parity is still required before treating Go project create as replacement-safe",
				"sampledProofs": []string{
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
				},
				"requiredProofs": []string{
					"full-ts-project-create-lifecycle-parity",
				},
				"replacementContract": map[string]any{
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
					"sampledProofs": []string{
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
					},
					"requiredProofs": []string{
						"full-ts-project-create-lifecycle-parity",
					},
				},
			},
			"delete": map[string]any{
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
				"acceptance":                               "prove TypeScript-vs-Go project delete side effects across live frontend sessions, external cleanup warning behavior, and DB/FS diffs before treating Go delete as full replacement parity",
				"sampledCleanupScopes": []string{
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
				},
				"requiredExternalProofs": []string{
					"real-podman-container-cleanup-warning-smoke",
				},
				"replacementContract": map[string]any{
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
					"sampledProofs": []string{
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
					},
					"requiredProofs": []string{
						"real-podman-container-cleanup-warning-smoke",
						"full-ts-project-delete-side-effect-parity",
					},
				},
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
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
				},
				"requiredProofs": []string{
					"real-podman-container-cleanup-warning-parity",
				},
				"acceptance": "seeded TypeScript-vs-Go project create/delete DB/Git/FS diffs are Wave192-proven and real git worktree cleanup-warning behavior is Wave193-sampled; live Podman container cleanup-warning parity remains required before treating Go project lifecycle as replacement-safe",
			},
		},
		"contractHarness": map[string]any{
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
			"ciFreshnessContract": map[string]any{
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
				"acceptance":                        "wire the TS-vs-Go harness into CI/release gates, require a fresh clean run before default cutover, and keep any non-zero diff paired with explicit disabled-by-design capability metadata",
				"requiredGates": []string{
					"fresh-ts-go-api-contract-diff-run",
					"full-route-inventory-or-explicit-deferrals",
					"browser-frontend-e2e-smoke",
					"live-provider-or-explicit-credential-deferrals",
					"real-runtime-side-effect-smoke-or-disabled-contracts",
				},
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"sampled-json-sse-ws-contract-harness-metadata",
					"same-home-db-token-harness-scope",
					"required-shape-and-semantic-assertions",
					"additive-go-fields-tolerance",
					"ci-freshness-contract-metadata",
				},
				"requiredProofs": []string{
					"fresh-clean-ts-go-contract-run-artifact",
					"full-route-inventory-or-explicit-deferrals",
					"browser-frontend-e2e-smoke",
					"live-provider-or-explicit-credential-deferrals",
					"real-runtime-side-effect-smoke-or-disabled-contracts",
					"ci-release-gate-wiring",
				},
				"reason": "Wave 122 keeps API/frontend replacement unsafe until sampled health metadata is backed by a fresh clean TS-Go contract artifact, full route inventory or explicit deferrals, browser E2E, live-provider deferrals, real-runtime side-effect smoke or disabled contracts, and CI/release gates.",
			},
			"sampledSurfaceGroups": []string{
				"health/auth/projects/settings/search/update-check",
				"protected-route-auth-boundaries",
				"provider-settings-and-local-fixture-flows",
				"storage-runtime-benchmark-admin-boundaries",
				"update-ui-json-and-download-sse",
				"routines-skills-hooks-mcp-ui",
				"user-preferences-and-favorites",
			},
			"unsampledCriticalGroups": []string{
				"full-route-inventory",
				"browser-frontend-e2e",
				"live-external-provider-credentials",
				"real-podman-runtime-side-effects",
				"large-db-search-relevance",
				"long-running-narrator-provider-sessions",
			},
			"safetyAssertions": []string{
				"status-code-content-type-required-fields",
				"auth-admin-boundaries",
				"fallback-success-not-parity",
				"no-raw-secret-exposure",
				"manual-or-disabled-runtime-actions-remain-gated",
			},
		},
		"releasePackaging": map[string]any{
			"mode":                 "go-beta-opt-in",
			"buildInfo":            "ldflags-or-env",
			"frontend":             "sidecar-directory-or-env",
			"changelog":            "sidecar-directory-or-env",
			"singleFileEmbedded":   false,
			"fullReplacementReady": false,
			"scripts": map[string]any{
				"build":         "bun scripts/build-go-backend.ts",
				"releaseBeta":   "bun scripts/release-go-beta.ts",
				"rollbackDrill": "bun scripts/go-rollback-drill.ts",
				"devGo":         "bun server/dev-all.ts --backend=go",
				"testGo":        "bun scripts/go-task.ts test",
			},
			"cutoverContract": map[string]any{
				"defaultDevBackend":      "typescript",
				"defaultStartBackend":    "typescript",
				"goBetaOptIn":            true,
				"tsLegacyPathAvailable":  true,
				"defaultReleaseSwitch":   false,
				"rollbackDrillRequired":  true,
				"tsGoContractCIRequired": true,
				"tsGoContractCIWired":    false,
				"defaultCutoverBlocked":  true,
				"fullReplacementReady":   false,
			},
			"cutoverFreshnessContract": map[string]any{
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
				"acceptance":                         "default dev/start/release cutover requires fresh contract harness, strict migration release gate, rollback drill, compiled update smoke, and explicit deferrals for any remaining non-TS-equivalent runtime paths",
				"requiredGates": []string{
					"fresh-ts-go-contract-harness",
					"strict-migration-release-gate-wired",
					"fresh-ts-go-ts-rollback-drill-report",
					"compiled-update-zstd-patch-smoke",
					"full-route-inventory-or-explicit-deferrals",
				},
			},
			"buildContract": map[string]any{
				"targets":          []string{"linux-x64", "linux-arm64", "darwin-x64", "darwin-arm64", "win-x64"},
				"outputDirPattern": "dist/go-backend/{version}",
				"manifest":         "manifest.json",
				"manifestFields":   []string{"version", "commit", "channel", "backend", "buildChannel", "generatedAt", "changelog", "artifacts"},
				"artifactFields":   []string{"platform", "goos", "goarch", "filename", "size", "sha512", "channel"},
				"sha512Encoding":   "base64",
				"ldflags":          []string{"Version", "Commit", "Channel"},
				"cgoEnabled":       false,
				"trimpath":         true,
				"buildvcs":         false,
				"buildChannel":     "go-beta",
			},
			"releaseContract": map[string]any{
				"product":                        "narrafork-go-backend",
				"channel":                        "beta",
				"buildChannel":                   "go-beta",
				"uploadPolicy":                   "metadata-plus-zstd-patch-only",
				"fullBinaryUpload":               false,
				"requiresPreviousArtifact":       true,
				"fromVersionMustBeOlder":         true,
				"requiresUpdateToken":            true,
				"dryRunSupported":                true,
				"dryRunRequiresManifest":         true,
				"dryRunRequiresPreviousArtifact": true,
				"skipBuildSupported":             true,
				"releaseNotesSource":             "changelogs/v{version}.json-or---changelog",
				"metadataFields":                 []string{"version", "channel", "platform", "filename", "size", "sha512", "releaseNotes", "zstdPatch", "zstdPatchMeta"},
				"defaultUpdateServer":            "https://narrafork-update.b.domexie.cn",
			},
			"packagingArtifactContract": map[string]any{
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
				"acceptance":                      "prove a complete Go replacement package with embedded or bundled frontend/changelog assets, full binary publication or accepted updater semantics, and full-artifact update/download smoke before treating Go beta artifacts as a TypeScript release replacement",
				"acceptedBetaArtifacts": []string{
					"platform-go-binaries",
					"manifest-json",
					"base64-sha512-artifact-metadata",
					"metadata-plus-zstd-patch-release-records",
					"frontend-sidecar-directory-or-env",
					"changelog-sidecar-directory-or-env",
				},
				"requiredReplacementProofs": []string{
					"single-file-or-bundled-static-frontend",
					"embedded-or-bundled-go-changelog",
					"full-binary-publication-or-accepted-updater-policy",
					"full-artifact-update-download-smoke",
					"installer-or-service-wrapper-parity-if-required",
				},
			},
			"rollbackDrill": map[string]any{
				"script":             "bun scripts/go-rollback-drill.ts",
				"flow":               []string{"ts-start", "ts-stop", "go-start", "go-stop", "ts-rollback", "ts-final-stop"},
				"temporaryHome":      true,
				"sharedDatabase":     true,
				"registersAdmin":     true,
				"healthChecks":       []string{"TS backend", "Go backend", "TS backend after rollback"},
				"reportPathPattern":  "dist/go-backend/{version}/rollback-drill.json",
				"buildsWhenMissing":  true,
				"skipBuildSupported": true,
			},
		},
		"nativeExtensions": map[string]any{
			"defaultEnabled": false,
			"scope":          "go-owned-runtime-extensions",
			"browserSessions": map[string]any{
				"defaultEnabled": false,
				"storage":        "go-runtime-sidecar",
				"cutover":        "explicit-enable",
				"rollback":       "explicit-disable-or-export",
				"reason":         browserSessionsDefaultOffReason,
			},
			"containerBrowserToolAutoEnable": map[string]any{
				"defaultEnabled": false,
				"cutover":        "explicit-enable",
				"rollback":       "explicit-disable",
				"reason":         containerBrowserToolAutoEnableReason,
			},
		},
		"fs": map[string]any{
			"browse": map[string]any{
				"supported": true,
				"fallback":  false,
			},
			"shortcuts": map[string]any{
				"supported": true,
				"fallback":  false,
			},
			"mkdir": map[string]any{
				"supported":            true,
				"fallback":             false,
				"singleDirectoryOnly":  true,
				"nameTraversalGuard":   true,
				"pathOnlyBodyRejected": true,
			},
			"preview": map[string]any{
				"supported":           true,
				"maxTextBytes":        1 << 20,
				"maxBinaryBytes":      20 * 1024 * 1024,
				"tooLargeCode":        "FS_PREVIEW_TOO_LARGE",
				"inlineDisposition":   true,
				"privateCacheSeconds": 60,
			},
			"reveal": fsReveal,
			"systemActionsContract": map[string]any{
				"browseSupported":                     true,
				"shortcutsSupported":                  true,
				"mkdirSupported":                      true,
				"mkdirSingleDirectoryOnly":            true,
				"mkdirNameTraversalGuard":             true,
				"previewSupported":                    true,
				"previewMaxTextBytes":                 1 << 20,
				"previewMaxBinaryBytes":               20 * 1024 * 1024,
				"previewTooLargeCode":                 "FS_PREVIEW_TOO_LARGE",
				"previewInlineDisposition":            true,
				"revealSupported":                     fsRevealSupported,
				"revealSupportedPlatforms":            []string{"windows", "macos"},
				"revealLinuxXDGOpenParity":            false,
				"revealValidatesDirectoryBeforeSpawn": true,
				"revealRouteUnsupportedStatus":        400,
				"revealRouteUnsupportedCode":          "VALIDATION_ERROR",
				"healthFallbackCode":                  "FEATURE_DISABLED",
				"fullTSFilesystemActionParity":        false,
				"reason":                              "Go filesystem browsing, shortcuts, mkdir, and bounded preview are supported; system file-manager reveal is limited to Windows/macOS and Linux xdg-open parity is not implemented.",
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"shortcuts-default-browse-hidden-dir-fixture",
					"mkdir-name-traversal-and-path-only-validation",
					"bounded-preview-limit-and-cache-contract",
					"linux-reveal-unsupported-validation-before-spawn",
				},
				"requiredProofs": []string{
					"windows-macos-reveal-smoke",
					"linux-xdg-open-parity-or-product-decision",
					"frontend-file-picker-browser-smoke",
					"large-file-preview-browser-smoke",
					"ts-go-filesystem-action-diff-smoke",
					"full-ts-filesystem-action-parity",
				},
				"reason": "Wave 138 rolls up filesystem browse, shortcuts, mkdir path-safety, bounded preview, and reveal fallback metadata without claiming full TypeScript filesystem action replacement safety. Linux xdg-open parity, cross-platform reveal smoke, frontend file-picker/browser smoke, large-file preview browser behavior, and TS-Go filesystem action diffs remain unproven.",
			},
		},
		"content": map[string]any{
			"routines": map[string]any{
				"supported": true,
				"routes": map[string]any{
					"list":                "GET /api/routines",
					"globalToggle":        "POST /api/routines/{id}/toggle",
					"projectList":         "GET /api/routines/project/{projectId}",
					"projectToggle":       "POST /api/routines/project/{projectId}/{id}/toggle",
					"globalPromptGet":     "GET /api/routines/global-prompt",
					"globalPromptReplace": "PUT /api/routines/global-prompt",
				},
				"responseContract": map[string]any{
					"listEnvelope":               "{routines: []}",
					"routineFields":              []string{"id", "type", "category", "name", "descriptionEn", "descriptionZh", "enabled"},
					"projectRoutineFields":       []string{"id", "type", "category", "name", "descriptionEn", "descriptionZh", "enabled", "globalEnabled", "override"},
					"projectOverrideValues":      []string{"global", "enabled", "disabled"},
					"toggleResponse":             []string{"ok"},
					"globalPromptResponseFields": []string{"content", "filePath", "contentTruncated", "candidates"},
				},
				"toggleContract": map[string]any{
					"globalMissingRoutineStatus":  float64(404),
					"projectMissingRoutineStatus": float64(404),
					"projectMissingProjectStatus": float64(404),
					"projectActions":              []string{"enable", "disable", "reset"},
					"projectInvalidActionStatus":  float64(400),
					"projectSettingsStorage":      "projects.chapter_settings.routines",
				},
				"globalPromptContract": map[string]any{
					"maxPromptBytes":            float64(512 * 1024),
					"contentTruncatedFlag":      true,
					"allowedCandidatePathsOnly": true,
					"invalidFilePathStatus":     float64(400),
				},
			},
			"projectDatabase": map[string]any{
				"supported":       true,
				"syncMode":        "manual-api",
				"eventDrivenSync": false,
				"backupImport":    true,
				"routePrefix":     "/api/project-db",
				"backupScope": map[string]any{
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
					"includedRepresentativeTables": []string{
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
					},
					"excludedRepresentativeTables": []string{
						"api_requests",
						"benchmark_task_results",
						"gateway_session_mappings",
						"hooks",
						"workspaces",
					},
				},
				"reason": "Go exposes project database backup/import/sync routes as a manual project-domain portability subset, not TypeScript-style event-driven sync or full main-DB export.",
				"replacementContract": map[string]any{
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
					"sampledProofs": []string{
						"manual-api-project-db-sync-contract",
						"project-domain-portability-subset-scope",
						"representative-included-excluded-table-metadata",
						"review-merge-fields-backup-import-smoke",
					},
					"requiredProofs": []string{
						"event-driven-project-db-sync-parity-or-product-decision",
						"full-main-db-export-scope-decision",
						"versioned-backup-schema-metadata",
						"hooks-workspaces-api-requests-export-decision",
						"gateway-benchmark-global-state-export-decision",
						"large-project-bounded-copy-smoke",
						"ts-go-project-db-import-export-diff-smoke",
					},
					"reason": "Wave 139 rolls up manual project database sync plus the project-domain backup/import subset without claiming full TypeScript project DB replacement safety. Go still does not provide event-driven project-db sync, full main-DB export/import, versioned backup schema metadata, global/cross-project table export, large-project bounded-copy smoke, or TS-Go project DB diff proof.",
				},
			},
			"projectRoutines": map[string]any{
				"supported":                        true,
				"storage":                          "main-db-project-chapter-settings",
				"fallback":                         false,
				"routePrefix":                      "/api/routines/project/{projectId}",
				"noDatabaseFallbackCode":           "FEATURE_DISABLED",
				"noDatabaseFallbackFeature":        "project.routines",
				"artifactScope":                    "tool-routine-settings-only",
				"commandRoutineArtifacts":          false,
				"skillRoutineArtifacts":            false,
				"automaticArtifactMaterialization": false,
				"replacementContract": map[string]any{
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
					"sampledProofs": []string{
						"project-routine-override-fields",
						"project-routine-enable-disable-reset-toggle-flow",
						"tool-routine-settings-only-artifact-scope",
						"no-database-fallback-metadata",
					},
					"requiredProofs": []string{
						"command-routine-artifact-materialization",
						"skill-routine-artifact-materialization",
						"automatic-artifact-materialization-policy",
						"project-chapter-settings-db-availability-smoke",
						"frontend-routine-artifact-ui-smoke",
						"full-ts-routine-artifact-parity",
					},
					"reason": "Wave 140 keeps project routines replacement-unsafe beyond the current tool-routine settings scope. Project routine override fields and toggle flows are sampled, but command/skill routine artifacts, automatic materialization, DB-availability smoke, frontend artifact UI smoke, and full TypeScript routine artifact parity remain unproven.",
				},
			},
			"skills": map[string]any{
				"supported": true,
				"routes": map[string]any{
					"globalList":    "GET /api/skills/global",
					"globalDetail":  "GET /api/skills/global/{name}",
					"globalCreate":  "POST /api/skills/global",
					"globalUpdate":  "PUT /api/skills/global/{name}",
					"globalDelete":  "DELETE /api/skills/global/{name}",
					"globalToggle":  "POST /api/skills/global/{name}/toggle",
					"projectList":   "GET /api/skills?projectId=...",
					"projectDetail": "GET /api/skills/{name}?projectId=...",
					"projectFile":   "GET /api/skills/{name}/files/*?projectId=...",
				},
				"globalSkillContract": map[string]any{
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
				},
				"responseContract": map[string]any{
					"summaryFields":  []string{"name", "description", "disabled", "source"},
					"detailFields":   []string{"name", "description", "content", "location", "disabled", "source"},
					"deleteResponse": []string{"ok"},
				},
				"projectSkillContract": map[string]any{
					"requiresProjectId":         true,
					"projectMissingStatus":      float64(404),
					"noDatabaseFallbackCode":    "FEATURE_DISABLED",
					"noDatabaseFallbackFeature": "project.skills",
					"fileResponseTextPlain":     true,
					"fileTraversalGuard":        true,
					"fileReadBounded":           true,
				},
			},
			"projectSkills": map[string]any{
				"supported":                       true,
				"storage":                         "project-git-path-scan",
				"fallback":                        false,
				"projectOverridesGlobal":          true,
				"slashSummaryMergesProjectGlobal": true,
				"disabledSkillsHidden":            true,
				"companionFiles":                  true,
				"companionTraversalGuard":         true,
				"maxSkillMarkdownBytes":           256 * 1024,
				"maxSkillCompanionFileBytes":      512 * 1024,
				"absoluteCompanionBaseDirectory":  true,
				"fullCompanionGoldenParity":       false,
				"replacementContract": map[string]any{
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
					"sampledProofs": []string{
						"project-over-global-skill-override",
						"slash-summary-project-global-merge",
						"disabled-project-skills-hidden",
						"companion-traversal-guard-and-bounded-read",
					},
					"requiredProofs": []string{
						"project-skill-symlink-path-edge-smoke",
						"companion-depth-count-breadth-smoke",
						"frontend-project-skill-companion-ui-smoke",
						"full-ts-companion-output-golden",
						"ts-go-project-skill-route-diff-smoke",
					},
					"reason": "Wave 140 keeps project skills replacement-unsafe beyond focused companion-file safety proof. Project/global merge, override, disabled hiding, traversal guard, and bounded reads are sampled, but symlink/path edge cases, companion depth/count breadth, frontend companion UI smoke, and full TypeScript companion output golden parity remain unproven.",
				},
			},
		},
		"customSubagents": map[string]any{
			"supported": true,
			"routes": map[string]any{
				"list":   "GET /api/custom-subagents",
				"create": "POST /api/custom-subagents",
				"detail": "GET /api/custom-subagents/{name}",
				"update": "PUT /api/custom-subagents/{name}",
				"delete": "DELETE /api/custom-subagents/{name}",
			},
			"storage": map[string]any{
				"mode":         "filesystem-home-subagents",
				"fileName":     "SUBAGENT.md",
				"missingRoot":  "empty-array",
				"maxFileBytes": float64(256 * 1024),
			},
			"responseContract": map[string]any{
				"listResponseDirectArray": true,
				"createStatus":            float64(201),
				"fields":                  []string{"name", "description", "toolAccess", "customTools", "defaultModel", "prompt", "promptTruncated"},
				"deleteResponse":          []string{"ok"},
			},
			"validationContract": map[string]any{
				"rejectsBuiltinNames":       true,
				"rejectsDuplicateDirectory": true,
				"missingDetailStatus":       float64(404),
				"missingDeleteStatus":       float64(404),
				"toolAccessFallback":        "readOnly",
				"allowedToolAccess":         []string{"readOnly", "general", "custom"},
				"updateRenameSupported":     true,
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"filesystem-subagent-crud-rename",
					"builtin-name-duplicate-missing-validation",
					"optional-noncanonical-store-contract",
				},
				"requiredProofs": []string{
					"custom-subagent-runtime-prompt-injection-smoke",
					"default-model-tool-access-browser-smoke",
					"frontend-custom-subagents-settings-ui-smoke",
					"ts-go-custom-subagent-route-and-runtime-diff-smoke",
				},
				"reason": "Wave 141 keeps custom-subagent replacement unsafe beyond sampled filesystem CRUD/rename and validation proof. Go stores custom subagents in non-canonical filesystem SUBAGENT.md files and still lacks frontend settings UI smoke, runtime prompt/default-model/tool-access browser smoke, and TS-Go route/runtime diff proof.",
			},
		},
		"hooks": map[string]any{
			"supported": true,
			"adminOnly": true,
			"routes": map[string]any{
				"list":   "GET /api/hooks?projectId=...",
				"all":    "GET /api/hooks/all",
				"create": "POST /api/hooks",
				"detail": "GET /api/hooks/{id}",
				"update": "PUT /api/hooks/{id}",
				"delete": "DELETE /api/hooks/{id}",
			},
			"responseContract": map[string]any{
				"listResponseDirectArray": true,
				"createStatus":            float64(201),
				"fields": []string{
					"id", "projectId", "event", "matcher", "type", "command", "url", "headers", "prompt", "model", "timeout", "enabled", "sortOrder", "createdAt", "updatedAt",
				},
				"deleteResponse": []string{"ok"},
			},
			"validationContract": map[string]any{
				"events":                           []string{"PreToolUse", "PostToolUse"},
				"types":                            []string{"command", "http"},
				"defaultMatcher":                   "",
				"defaultTimeoutSeconds":            float64(30),
				"timeoutRangeSeconds":              []float64{1, 600},
				"defaultEnabled":                   true,
				"missingUpdateStatus":              float64(404),
				"missingDeleteStatus":              float64(404),
				"updateMissingChecksBeforeBody":    true,
				"preservesHeaders":                 true,
				"allowsEmptyMatcher":               true,
				"allowsExplicitNullInactiveFields": true,
				"rejectsClearingActivePayload":     true,
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"admin-only-hook-crud-and-validation",
					"pretooluse-command-http-blocking-hook-fixtures",
					"posttooluse-command-http-nonblocking-hook-fixtures",
				},
				"requiredProofs": []string{
					"broader-live-session-hook-smoke",
					"hook-event-bus-telemetry-parity",
					"frontend-hooks-settings-ui-smoke",
					"ts-go-hook-runtime-diff-smoke",
					"full-ts-hook-runtime-parity-decision",
				},
				"reason": "Wave 141 keeps hooks replacement unsafe beyond sampled admin CRUD/validation and focused PreToolUse/PostToolUse command/http runtime proof. Broader live-session smoke, hook telemetry/event-bus parity, frontend settings UI smoke, and TS-Go hook runtime diffs remain unproven.",
			},
		},
		"workspaces": map[string]any{
			"supported":  true,
			"userScoped": true,
			"routes": map[string]any{
				"list":   "GET /api/workspaces",
				"create": "POST /api/workspaces",
				"detail": "GET /api/workspaces/{id}",
				"update": "PATCH /api/workspaces/{id}",
				"delete": "DELETE /api/workspaces/{id}",
			},
			"responseContract": map[string]any{
				"listResponseDirectArray": true,
				"createStatus":            float64(201),
				"fields":                  []string{"id", "userId", "title", "tree", "createdAt", "updatedAt"},
				"timestamps":              "iso-string-from-timestamp-ms",
				"deleteResponse":          []string{"ok"},
			},
			"validationContract": map[string]any{
				"createRequiresTree":     true,
				"minTreeLength":          float64(2),
				"maxTreeLength":          float64(50000),
				"maxTitleLength":         float64(200),
				"defaultTitlePrefix":     "Workspace ",
				"missingDetailStatus":    float64(404),
				"missingUpdateStatus":    float64(404),
				"missingDeleteStatus":    float64(404),
				"updateMissingAfterBody": true,
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"user-scoped-workspace-crud",
					"workspace-tree-title-validation",
					"iso-timestamp-response-shape",
				},
				"requiredProofs": []string{
					"recent-tabs-workspace-interaction-smoke",
					"multi-user-workspace-isolation-smoke",
					"frontend-workspace-ui-smoke",
					"ts-go-workspace-route-diff-smoke",
					"full-ts-workspace-parity-decision",
				},
				"reason": "Wave 141 keeps workspaces replacement unsafe beyond sampled user-scoped CRUD, validation, and timestamp-shape proof. Recent-tab interactions, multi-user isolation breadth, frontend workspace UI smoke, and TS-Go route diff proof remain unproven.",
			},
		},
		"mcp": map[string]any{
			"routes": map[string]any{
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
			},
			"serverContract": map[string]any{
				"listEnvelope":              "{servers: []}",
				"createStatus":              float64(201),
				"idLength":                  float64(8),
				"defaultTransport":          "stdio",
				"defaultEnabled":            true,
				"deleteResponse":            []string{"ok"},
				"missingUpdateStatus":       float64(404),
				"missingDeleteStatus":       float64(404),
				"settingsMissingStatus":     float64(501),
				"settingsMissingCode":       "FEATURE_DISABLED",
				"enabledCreateProbesStdio":  true,
				"enabledUpdateProbesStdio":  true,
				"disableClearsRuntimeState": true,
			},
			"validationContract": map[string]any{
				"strictCreateAndTest":           true,
				"transports":                    []string{"stdio", "streamable-http", "sse"},
				"implementedTransports":         []string{"stdio"},
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
			},
			"protocolContract": map[string]any{
				"jsonrpc":                "2.0",
				"protocolVersion":        "2024-11-05",
				"initializeSupported":    true,
				"toolsListSupported":     true,
				"toolsCallSupported":     true,
				"unknownMethodErrorCode": float64(-32601),
				"notificationsAccepted":  true,
			},
			"stdioRuntimeContract": map[string]any{
				"lineLimitBytes":               float64(1 * 1024 * 1024),
				"totalLimitBytes":              float64(4 * 1024 * 1024),
				"maxResponseMessages":          float64(256),
				"toolOutputLimitBytes":         float64(256 * 1024),
				"stderrLimitBytes":             float64(100000),
				"toolListSource":               "external",
				"toolMetadataFields":           []string{"serverId", "serverName", "source"},
				"agentToolNamePrefix":          "mcp__",
				"manualDisabledConnectInjects": true,
			},
			"serverSettingsStorage": map[string]any{
				"supported": true,
				"fallback":  false,
				"storage":   "settings.json",
				"reason":    "MCP server definitions are persisted through the Go settings store when it is available; handlers return FEATURE_DISABLED only in runtimes without a settings store.",
			},
			"builtinProtocol": map[string]any{
				"supported":  true,
				"initialize": true,
				"toolsList":  true,
				"toolsCall":  true,
			},
			"builtinTools": map[string]any{
				"supported": true,
				"parity":    "partial",
				"missing":   []string{"generatedContextSummary"},
			},
			"externalServerManagement": map[string]any{
				"supported":   true,
				"storage":     "settings.json",
				"permissions": true,
				"import":      true,
			},
			"transports": map[string]any{
				"stdio": map[string]any{
					"supported": true,
				},
				"sse": map[string]any{
					"supported": false,
					"fallback":  true,
					"code":      "FEATURE_DISABLED",
					"reason":    mcpNonStdioTransportReason,
				},
				"streamableHttp": map[string]any{
					"supported": false,
					"fallback":  true,
					"code":      "FEATURE_DISABLED",
					"reason":    mcpNonStdioTransportReason,
				},
			},
			"toolsList": map[string]any{
				"supported": true,
				"source":    "go-embedded-compatibility-tools-plus-discovered-metadata",
				"fallback":  false,
			},
			"toolsCall": map[string]any{
				"supported": true,
				"scope":     "go-embedded-compatibility-tools-and-stdio-external-tools",
				"fallback":  false,
			},
			"externalToolsInjection": map[string]any{
				"supported": true,
				"fallback":  false,
				"parity":    "partial",
				"transport": "stdio",
				"lifecycle": "per-call",
				"reason":    "Connected stdio MCP tools are injected into Go narrator provider tool lists and executed through per-call stdio JSON-RPC sessions.",
			},
			"externalAgentInjection": map[string]any{
				"supported": true,
				"fallback":  false,
				"parity":    "partial",
				"reason":    "Go narrator agent loops inject connected stdio MCP tools as mcp__server__tool provider tools; persistent MCP runtimes and non-stdio transports remain out of scope.",
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"positive-stdio-create-connect-list-tools",
					"non-stdio-create-test-connect-degraded-fallback",
					"stdio-agent-tool-injection-execution",
					"permission-mapping-and-subagent-filtering",
					"kiro-mcp-populated-bridge-focused-fixture",
				},
				"requiredProofs": []string{
					"persistent-mcp-runtime-lifecycle-parity",
					"sse-and-streamable-http-transport-parity-or-product-decision",
					"full-ts-mcp-manager-service-management-diff",
					"frontend-mcp-permissions-ui-e2e",
					"live-external-mcp-server-e2e",
					"live-kiro-mcp-e2e-or-product-decision",
				},
				"reason": "Wave 136 rolls up stdio MCP routes, validation fallbacks, settings-backed server storage, builtin protocol support, per-call stdio runtime limits, external tool injection, and permission mapping without claiming full TypeScript MCP replacement safety. Persistent MCP runtime lifecycle, non-stdio transports, full TS mcpManager service-management parity, frontend MCP permissions UI E2E, and live external/Kiro MCP coverage remain unproven.",
			},
		},
		"userPreferences": map[string]any{
			"supported": true,
			"routes": map[string]any{
				"get":                 "GET /api/user-preferences",
				"patch":               "PATCH /api/user-preferences",
				"recentTabsUpsert":    "PUT /api/user-preferences/recent-tabs",
				"recentTabsRemove":    "DELETE /api/user-preferences/recent-tabs/{type}/{id}",
				"recentTabsMove":      "PATCH /api/user-preferences/recent-tabs/move",
				"recentTabsPin":       "PATCH /api/user-preferences/recent-tabs/pin",
				"recentTabsClear":     "POST /api/user-preferences/recent-tabs/clear",
				"graphViewportsPatch": "PATCH /api/user-preferences/graph-viewports",
			},
			"responseContract": map[string]any{
				"recentTabsArray":       true,
				"commandsArray":         true,
				"gatewayConfigObject":   true,
				"graphViewportsRawJSON": true,
				"secretRedaction":       true,
			},
			"patchContract": map[string]any{
				"boolFieldValidation":    true,
				"languageEnum":           []string{"en", "zh-CN"},
				"sendModeEnum":           []string{"enter", "ctrl+enter"},
				"notifySoundTypeEnum":    []string{"builtin", "custom"},
				"maskedSecretsPreserved": true,
			},
			"recentTabsContract": map[string]any{
				"maxTabs":                       20,
				"responseDirectArray":           true,
				"perUserLock":                   true,
				"sessionTypeMigratesToNarrator": true,
				"tabTypes":                      []string{"chapter", "narrator", "project", "workspace", "subagent"},
				"requiredFields":                []string{"type", "id", "title", "lastVisitedAt"},
				"optionalFields":                []string{"narratorId", "parentNarratorId", "workspaceId", "subtitle", "status", "pinned", "updateOnly"},
				"updateOnlyNoInsert":            true,
				"unknownFieldsStripped":         true,
				"pinnedOrdering":                true,
				"workspaceGrouping":             true,
				"clearScopes":                   []string{"all", "projects", "inactive_narrators"},
				"snapshotEvent":                 "user:recent_tabs_snapshot",
				"snapshotFields":                []string{"type", "tabs", "revision"},
			},
			"graphViewportsContract": map[string]any{
				"projectIDRequired": true,
				"responseOK":        true,
				"storesRawJSON":     true,
			},
		},
		"favorites": map[string]any{
			"supported": true,
			"routes": map[string]any{
				"list":    "GET /api/favorites",
				"create":  "POST /api/favorites",
				"update":  "PATCH /api/favorites/{id}",
				"delete":  "DELETE /api/favorites/{id}",
				"reorder": "PUT /api/favorites/reorder",
			},
			"storage": map[string]any{
				"table":      "user_favorite_directories",
				"userScoped": true,
			},
			"responseContract": map[string]any{
				"directArrayList": true,
				"fields":          []string{"id", "userId", "path", "label", "sortOrder", "createdAt"},
				"createdStatus":   201,
			},
			"validationContract": map[string]any{
				"pathRequired":           true,
				"maxPathLength":          4096,
				"maxLabelLength":         200,
				"createRejectsNullLabel": true,
				"duplicatePathStatus":    400,
				"missingUpdateStatus":    404,
				"missingDeleteStatus":    404,
			},
			"reorderContract": map[string]any{
				"idsRequired":        true,
				"emptyIDsInvalid":    true,
				"transactional":      true,
				"zeroBasedSortOrder": true,
			},
			"sideEffects": map[string]any{
				"duplicateCheckBeforeInsert": true,
				"lookupFailureNoInsert":      true,
			},
		},
		"settings": map[string]any{
			"storage": map[string]any{
				"supported": true,
				"path":      "~/.narrafork/settings.json",
			},
			"patch": map[string]any{
				"supported": true,
			},
			"replacementContract": map[string]any{
				"boundaryTracked":                          true,
				"runtimeRow":                               "SETTINGS-PREFERENCES-01",
				"settingsPatchRootAllowlistProof":          true,
				"maskedSecretsPreservationProof":           true,
				"providerCompatibilitySyncProof":           true,
				"userPreferencesRouteProof":                true,
				"recentTabsSnapshotProof":                  true,
				"favoritesValidationReorderProof":          true,
				"fullZodUpdateSchemaParity":                false,
				"nestedUnknownFieldParitySmoke":            false,
				"providerArrayElementShapeSmoke":           false,
				"providerPrefixBroadSmoke":                 false,
				"positiveTLSTSGoSmoke":                     false,
				"legacyCodexOAuthCredentialPoolMigration":  false,
				"recentTabsFavoritesBrowserReconnectSmoke": false,
				"replacementSafe":                          false,
				"sampledProofs": []string{
					"settings-root-allowlist-and-selected-validators",
					"masked-provider-tls-vnet-secret-preservation",
					"provider-compatibility-sync",
					"user-preferences-response-and-patch-contract",
					"recent-tabs-snapshot-event-contract",
					"favorites-validation-and-reorder-contract",
				},
				"requiredProofs": []string{
					"full-typescript-zod-update-schema-diff",
					"nested-unknown-field-ts-go-smoke",
					"provider-array-element-shape-and-prefix-breadth-smoke",
					"positive-tls-generation-and-save-ts-go-smoke",
					"legacy-codex-oauth-credential-pool-migration-decision",
					"recent-tabs-favorites-browser-reconnect-and-reorder-smoke",
				},
				"acceptance": "prove TypeScript-vs-Go settings/preferences replacement parity across the full Zod update schema, nested unknown-field behavior, provider array/prefix breadth, positive TLS save/generation, legacy Codex OAuth credential-pool migration, and recent-tabs/favorites browser reconnect/reorder smoke before treating Go settings as replacement-safe",
			},
			"validation": map[string]any{
				"tsZodParity":           false,
				"mode":                  "loose-json-with-normalization",
				"loadMigrationParity":   "partial",
				"patchValidationParity": "partial",
				"unsafeSaveGuard":       true,
				"patchContract": map[string]any{
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
					"providerCompatibilitySync":          true,
					"dangerReflectionLegacySync":         true,
					"proxyURLNormalization":              true,
					"providerPrefixConflictGuard":        true,
					"tlsFileExistenceValidation":         true,
					"containerProxySideEffectRollback":   true,
					"proxyRuntimeMayRunBeforeStoreWrite": true,
					"nestedUnknownFieldParity":           "unverified",
					"fullZodUpdateSchemaParity":          false,
					"sampledValidationGroups": []string{
						"server",
						"paths",
						"agent-scalars",
						"agent-prompts-and-aggregations",
						"agent-retry-rules",
						"agent-model-selection",
						"agent-policy",
						"agent-web-fetch-policy",
						"chapters",
						"containers",
						"editor",
						"codex",
						"routines",
						"update",
						"vnet",
						"shares",
						"provider-arrays",
						"provider-prefix-conflicts",
						"secret-masking",
					},
					"rootAllowlist": []string{
						"server",
						"paths",
						"agent",
						"chapters",
						"containers",
						"editor",
						"auth",
						"customApiProviders",
						"openaiProviders",
						"anthropicProviders",
						"nkpProviders",
						"nugProviders",
						"clineProviders",
						"codex",
						"routines",
						"update",
						"vnet",
						"shares",
					},
					"reason": "Go PATCH /api/settings applies bounded root allowlist and selected validators before Store.Patch, preserves masked secrets, and synchronizes provider compatibility fields, but it is not a full TypeScript Zod update schema implementation and nested unknown-field parity remains unverified.",
				},
				"zodGapContract": map[string]any{
					"boundaryTracked":                   true,
					"schemaSource":                      "server/lib/settings Zod update schema",
					"goMode":                            "root-allowlist-with-selected-validators",
					"tsZodParity":                       false,
					"fullZodUpdateSchemaParity":         false,
					"nestedUnknownFieldParity":          "unverified",
					"nestedUnknownFieldSmoke":           false,
					"fullNestedSchemaValidation":        false,
					"providerPrefixBroadSmoke":          false,
					"providerArrayElementShapeSmoke":    false,
					"positiveTLSTSGoSmoke":              false,
					"legacyCodexCredentialPoolOwner":    "typescript",
					"legacyCodexCredentialPoolMigrated": false,
					"acceptance":                        "compare PATCH /api/settings against the full TypeScript Zod update schema, including nested unknown-field behavior, provider-array element shapes, provider-prefix conflict breadth, positive TLS save/generation cases, and legacy Codex OAuth credential-pool migration",
					"unclaimedValidationFamilies": []string{
						"nested-unknown-field-behavior",
						"provider-array-element-shapes",
						"provider-prefix-cross-array-breadth",
						"positive-tls-ts-go-smoke",
						"legacy-codex-oauth-credential-pool-migration",
					},
					"sampledValidationFamilies": []string{
						"root-allowlist",
						"auth-jwt-rejection",
						"masked-provider-secrets",
						"provider-prefix-conflicts",
						"tls-file-existence",
						"container-proxy-rollback",
					},
				},
				"knownMissingMigrations": []string{
					"legacyCodexOAuthCredentialPool",
				},
				"missingMigrationDetails": map[string]any{
					"legacyCodexOAuthCredentialPool": map[string]any{
						"supported": false,
						"owner":     "typescript",
						"fallback":  "Use the TypeScript settings migration/import path before relying on Go for legacy Codex OAuth credential-pool settings.",
						"reason":    "Go does not yet migrate legacy per-provider Codex OAuth credential-pool settings into the current provider settings shape.",
					},
				},
				"legacyCodexOAuthCredentialPoolMigration": false,
				"fullZodUpdateSchemaParity":               false,
				"migratedLegacyFields": []string{
					"editor.legacyEncoding",
					"agent.yoloSkipReadOnlyConfirmations",
					"agent.smartInterruptionCheck",
					"agent.defaultPermissionMode=plan",
					"legacy openai root",
					"provider prefixes",
					"agent.subagentAllowedModels[]",
					"customApiProviders protocol/defaults",
					"openai/anthropic provider array sync",
					"stale agent model references",
					"mcp.behavior=allow",
					"codex.defaultReasoningEffort=null",
					"proxy urls",
				},
				"reason": "Go backend now runs a bounded TypeScript-legacy settings migration pass before saving, including custom API provider array sync and stale model reference purge, but still does not apply the full TypeScript Zod schema or the legacy Codex OAuth credential-pool migration.",
			},

			"secretMasking":             true,
			"providerModelAugmentation": true,
			"tlsGeneration":             true,
			"retryRules":                true,
		},
		"providers": providerCapabilities(),
		"usageHistory": map[string]any{
			"admin": map[string]any{
				"supported":        true,
				"readOnly":         true,
				"adminOnly":        true,
				"structuredErrors": true,
				"routes": map[string]any{
					"list":       true,
					"providers":  true,
					"stats":      true,
					"timeseries": true,
					"detail":     true,
				},
				"pagination": map[string]any{
					"defaultPage":     1,
					"defaultPageSize": 50,
					"maxPageSize":     100,
				},
				"schemaAdaptation": map[string]any{
					"apiRequestsTableOptional": true,
					"missingTableFailOpen":     true,
					"missingColumnFailOpen":    true,
					"joinColumnsConditional":   true,
					"emptyOnUnsupportedFilter": true,
				},
				"filters": []string{
					"narratorId",
					"provider",
					"kind",
					"model",
					"chapterId",
					"projectId",
					"startDate",
					"endDate",
				},
				"ordering": []string{"createdAt:desc", "id:desc"},
				"recordFields": []string{
					"id",
					"narratorId",
					"messageId",
					"kind",
					"provider",
					"credentialId",
					"credentialName",
					"model",
					"inputTokens",
					"outputTokens",
					"cachedInputTokens",
					"cacheCreationInputTokens",
					"cacheCreation5mTokens",
					"cacheCreation1hTokens",
					"reasoningTokens",
					"ttftMs",
					"durationMs",
					"costUsd",
					"contextPercent",
					"meterUsage",
					"meterUnit",
					"errorMessage",
					"hasRawDump",
					"rawDumpBytes",
					"createdAt",
					"narratorTitle",
					"chapterTitle",
					"chapterId",
					"projectId",
				},
				"statsFields": []string{
					"totalRequests",
					"totalInputTokens",
					"totalOutputTokens",
					"totalCacheCreationTokens",
					"totalCacheReadTokens",
					"totalCacheCreation5mTokens",
					"totalCacheCreation1hTokens",
					"totalReasoningTokens",
					"totalTokens",
					"totalCost",
					"totalCostUsd",
					"averageDurationMs",
					"averageTtftMs",
					"bounded",
					"effectiveStartDate",
					"effectiveEndDate",
				},
				"timeseries": map[string]any{
					"granularities":           []string{"day", "hour", "month"},
					"defaultGranularity":      "day",
					"maxBucketsByGranularity": map[string]any{"hour": 744, "day": 366, "month": 60},
					"fillsEmptyBuckets":       true,
					"truncatesLongRanges":     true,
				},
				"rawDumpPolicy": map[string]any{
					"listMetadataOnly":    true,
					"detailParsesJSON":    true,
					"invalidJSONFallback": "invalidJson/rawText",
				},
				"fullTSBillingParity": false,
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"admin-usage-history-list-detail-stats-timeseries-contract",
					"dynamic-api-requests-schema-fail-open",
					"raw-dump-list-metadata-detail-parse-policy",
					"narrator-provider-api-request-persistence",
					"narrator-usage-stats-summary-shape",
				},
				"requiredProofs": []string{
					"tool-call-usage-metadata-parity",
					"credential-cost-accounting-breadth",
					"provider-raw-dump-capture-breadth",
					"non-narrator-request-kind-parity",
					"live-provider-usage-history-smoke",
					"frontend-usage-analytics-browser-smoke",
					"full-ts-usage-billing-service-diff",
				},
				"reason": "Wave 137 rolls up admin usage-history display routes, dynamic api_requests schema fail-open behavior, raw-dump list/detail policy, stats/timeseries shape, and bounded narrator API request persistence without claiming full TypeScript billing or usage analytics replacement safety. Tool-call usage metadata, credential/cost accounting breadth, provider raw-dump capture breadth, non-narrator request kinds, live-provider smoke, and frontend usage analytics smoke remain unproven.",
			},
		},
		"benchmark": map[string]any{
			"lifecycle": map[string]any{
				"supported": true,
				"adminOnly": true,
				"routes": map[string]any{
					"createSuite": true,
					"listSuites":  true,
					"getSuite":    true,
					"createRun":   true,
					"listRuns":    true,
					"getRun":      true,
					"startRun":    true,
					"cancelRun":   true,
					"retryRun":    true,
					"logs":        false,
					"artifacts":   false,
					"events":      false,
				},
				"requestContract": map[string]any{
					"decodeMode":                     "loose-json",
					"suiteTasksRequired":             true,
					"maxTasksPerSuite":               1000,
					"maxPromptChars":                 200000,
					"maxSuiteJSONBytes":              20000000,
					"partialTSZodParity":             true,
					"strictTaskIDRegex":              false,
					"strictNameLengthValidation":     false,
					"metadataJSONLimitParity":        false,
					"testCodeRubricValidationParity": false,
					"runDefaults": map[string]any{
						"name":           "Run",
						"model":          "",
						"permissionMode": "bypassPermissions",
						"config":         "{}",
					},
				},
				"responseContract": map[string]any{
					"suiteFields":             []string{"id", "name", "version", "description", "tasks", "createdAt"},
					"runFields":               []string{"id", "suiteId", "name", "model", "systemPrompt", "permissionMode", "status", "config", "totalTasks", "completedTasks", "passedTasks", "failedTasks", "totalCostUsd", "totalTokensIn", "totalTokensOut", "totalDurationMs", "createdAt", "startedAt", "completedAt"},
					"startFields":             []string{"ok", "runId", "status", "runnerAvailable", "message"},
					"cancelFields":            []string{"ok", "runId", "status", "message"},
					"retryFields":             []string{"ok", "runId", "status", "message"},
					"goAddsCancelRunIdStatus": true,
					"retryIsGoExtension":      true,
				},
				"executionContract": map[string]any{
					"backgroundStart":                             true,
					"sequentialTasks":                             true,
					"createsStandaloneNarrator":                   true,
					"tempWorkDir":                                 true,
					"tempWorkDirCleanup":                          true,
					"outputLimitChars":                            50000,
					"errorLimitChars":                             5000,
					"defaultTaskTimeoutMs":                        10 * 60 * 1000,
					"sourceWorkspaceCopyDefault":                  false,
					"sourceWorkspaceCopyRequiresAllowLocalSource": true,
					"sourceWorkspaceCopyLimitBytes":               100 * 1024 * 1024,
					"tsSourceWorkspaceDefaultParity":              false,
					"staleRunningReconcileOnStartup":              false,
					"fullTSLifecycleParity":                       false,
				},
				"startContract": map[string]any{
					"claimPendingOnly":          true,
					"notPendingStatus":          float64(http.StatusConflict),
					"notPendingCode":            "BENCHMARK_RUN_NOT_PENDING",
					"missingRunStatus":          float64(http.StatusNotFound),
					"startsBackgroundGoroutine": true,
					"runnerAvailableField":      true,
				},
				"cancelContract": map[string]any{
					"cancelsActiveInMemoryRun":          true,
					"marksRunCancelled":                 true,
					"marksPendingRunningTasksCancelled": true,
					"missingRunStatus":                  float64(http.StatusNotFound),
					"completedRunStatus":                float64(http.StatusBadRequest),
					"tsCompletedRunStatus":              float64(http.StatusConflict),
					"fullTSCancelStatusParity":          false,
					"crossRestartActiveCancel":          false,
				},
				"retryContract": map[string]any{
					"supported":          true,
					"goExtension":        true,
					"tsRouteExists":      false,
					"resetsStatuses":     []string{"failed", "error", "timeout", "cancelled"},
					"runningRetryStatus": float64(http.StatusConflict),
					"runningRetryCode":   "BENCHMARK_RUN_RUNNING",
					"startsImmediately":  true,
				},
				"eventContract": map[string]any{
					"httpSSE":                 false,
					"websocketProgress":       false,
					"pollResultsAndRunStatus": true,
				},
				"knownGaps": []string{
					"No benchmark logs/artifacts/events routes are exposed in Go or TS route contract.",
					"Go does not reconcile running benchmark rows on startup; TS marks stale running rows failed/error at module load.",
					"Go cancel of already-completed runs returns 400 while TS returns 409.",
					"Go retry is an admin-only extension and not a TypeScript route.",
					"Go keeps local source workspace copying disabled unless config.allowLocalSource=true and caps the copy at 100 MiB.",
				},
			},
			"containerExecution": map[string]any{
				"supported":        true,
				"fallback":         false,
				"runtime":          "podman",
				"resourceLimits":   true,
				"timeout":          true,
				"outputLimitBytes": 1024 * 1024,
				"runtimeBoundary": map[string]any{
					"requiresLinux":               true,
					"podmanRequired":              true,
					"fakePodmanRunnerProof":       true,
					"realPodmanSmoke":             false,
					"fullTSSWEBenchParity":        false,
					"routeFallbackBeforeMutation": false,
					"missingPodmanBehavior":       "task-error-recorded",
					"cleanupOnSetupFailure":       true,
					"containerCleanupCommand":     "podman rm -f",
					"resourceLimits":              []string{"memory", "cpus", "pids", "network"},
					"taskOutputLimitChars":        50000,
					"taskErrorLimitChars":         5000,
					"copyLimitBytes":              100 * 1024 * 1024,
					"podmanOutputLimitBytes":      1024 * 1024,
					"reason":                      "Go benchmark container execution is implemented with bounded Podman calls and fake-runner fixtures, but live Podman/SWE-bench parity remains unverified and missing Podman is recorded as task failure rather than a pre-run route fallback.",
				},
			},
			"results": map[string]any{
				"supported": true,
				"readOnly":  true,
				"adminOnly": true,
				"routes": map[string]any{
					"results": true,
					"summary": true,
					"compare": true,
				},
				"resultLimit": map[string]any{
					"default": 1000,
					"max":     1000,
				},
				"resultOrdering": []string{"createdAt", "taskId"},
				"resultFields": []string{
					"id",
					"runId",
					"taskId",
					"taskName",
					"narratorId",
					"status",
					"score",
					"maxScore",
					"output",
					"evalOutput",
					"errorMessage",
					"tokensIn",
					"tokensOut",
					"costUsd",
					"durationMs",
					"toolCallCount",
					"messageCount",
					"metadata",
					"startedAt",
					"completedAt",
					"createdAt",
				},
				"summaryFields": []string{
					"runId",
					"suiteName",
					"model",
					"status",
					"total",
					"passed",
					"failed",
					"errors",
					"timeouts",
					"pending",
					"passRate",
					"avgScore",
					"totalCostUsd",
					"totalTokensIn",
					"totalTokensOut",
					"totalDurationMs",
					"avgDurationMs",
				},
				"compareDiagnostics": map[string]any{
					"validationErrors": true,
					"errorShape":       "ok:false-code-reason-error-message",
					"codes": []string{
						"BENCHMARK_COMPARE_RUN_IDS_REQUIRED",
						"BENCHMARK_COMPARE_MIN_RUNS_REQUIRED",
						"BENCHMARK_RUN_SUMMARY_FAILED",
					},
					"summaryFailureStatus": 400,
				},
				"summaryMissingRun": map[string]any{
					"status": 404,
					"code":   "NOT_FOUND",
				},
				"fullTSBenchmarkUIParity": false,
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"regular-user-admin-boundary-probes",
					"suite-run-empty-error-contract-probes",
					"fake-podman-runner-cleanup-fixture",
					"lifecycle-create-start-cancel-retry-contract",
					"results-summary-compare-read-contract",
				},
				"requiredProofs": []string{
					"real-podman-benchmark-browser-smoke",
					"ts-go-swe-bench-lifecycle-side-effect-diff",
					"pre-mutation-missing-podman-route-fallback-decision",
					"startup-stale-running-reconcile-parity",
					"logs-artifacts-events-route-or-disabled-ui-decision",
					"populated-benchmark-ui-e2e-smoke",
				},
				"reason": "Wave 132 rolls up benchmark admin boundaries, suite/run lifecycle, fake-Podman cleanup, bounded execution, and read-route contracts without claiming TypeScript benchmark replacement safety. Real Podman browser smoke, TS-Go SWE-bench lifecycle side-effect diffs, missing-Podman pre-mutation fallback behavior, stale-running startup reconciliation, logs/artifacts/events handling, populated UI E2E, and full TS benchmark lifecycle/UI parity remain unproven.",
			},
		},
		"terminal": terminal,
		"vnet": map[string]any{
			"supported":           true,
			"mode":                "relay",
			"ws":                  true,
			"peerCleanup":         true,
			"udpRendezvous":       false,
			"udpRendezvousReason": "Go VNet compatibility mode relays WebSocket peer traffic and does not expose UDP rendezvous.",
			"routeContract": map[string]any{
				"statusRoute":          "GET /api/vnet/status",
				"websocketRoute":       "GET /ws/vnet",
				"statusAuth":           "api-authenticated",
				"statusGitGuarded":     false,
				"websocketAuthModes":   []string{"jwt-bearer", "jwt-query-token", "relay-token", "anonymous-when-enabled"},
				"adminOnly":            false,
				"nonUpgradeStatusJSON": true,
				"goDisabledWSBehavior": "upgrade-then-error-close",
				"tsDisabledWSBehavior": "reject-upgrade-401",
			},
			"statusContract": map[string]any{
				"fields":             []string{"ok", "supported", "fallback", "mode", "ws", "peerCleanup", "udpRendezvous", "networks", "peers", "connections", "maxPeersPerNetwork", "maxMessageBytes", "relay", "udp", "network"},
				"relayFields":        []string{"clients", "rooms", "peers", "running"},
				"networkFields":      []string{"hostname", "addresses"},
				"disabledFields":     []string{"ok", "supported", "fallback", "mode", "ws", "peerCleanup", "udpRendezvous", "code", "feature", "reason"},
				"statusDisabledHTTP": float64(http.StatusOK),
				"wsNonUpgradeStatus": float64(http.StatusOK),
				"wsDisabledHTTP":     float64(http.StatusServiceUnavailable),
			},
			"relayContract": map[string]any{
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
				"reason":                     "Go VNet supports bounded authenticated WebSocket relay semantics only; UDP rendezvous/dataplane parity remains disabled.",
			},
			"messageContract": map[string]any{
				"clientMessageTypes":    []string{"hello", "relay_packet", "punch_request", "pong", "ping"},
				"helloRequiredFields":   []string{"networkId", "peerId", "virtualIp"},
				"welcomeFields":         []string{"type", "networkId", "peerId", "sessionId", "udpAuthToken", "udp", "peers"},
				"peerFields":            []string{"peerId", "virtualIp", "displayName", "endpoints", "capabilities", "lastSeenAt"},
				"serverMessageTypes":    []string{"welcome", "peer_joined", "peer_left", "relay_packet", "punch_offer", "pong", "error"},
				"relayPacketAddsFields": []string{"fromPeerId", "srcIp", "dstIp"},
				"punchRequestResponse":  "punch_offer",
				"peerReplacementClose":  float64(1000),
			},
			"limitsContract": map[string]any{
				"defaultMaxPeersPerNetwork":        float64(64),
				"settingsMaxPeersPerNetwork":       float64(1024),
				"defaultMaxMessageBytes":           float64(1024 * 1024),
				"settingsMaxMessageBytes":          float64(64 * 1024 * 1024),
				"websocketReadLimitBytes":          float64(1024 * 1024),
				"rawOversizeMayCloseBeforeJSONErr": true,
			},
			"fallbackContract": map[string]any{
				"unauthorizedStatus":        float64(http.StatusUnauthorized),
				"disabledWSCode":            "VNET_DISABLED",
				"disabledWSFields":          []string{"type", "ok", "supported", "fallback", "code", "feature", "reason", "error", "message"},
				"relayErrorFields":          []string{"type", "code", "message"},
				"relayErrorCodes":           []string{"NOT_ATTACHED", "MESSAGE_TOO_LARGE", "INVALID_MESSAGE", "PEER_REPLACED", "VIRTUAL_IP_IN_USE", "NETWORK_FULL", "NOT_REGISTERED", "PEER_NOT_FOUND", "INVALID_SOURCE_IP", "INVALID_DESTINATION_IP"},
				"tsDisabledWSParity":        false,
				"fullTSVNetRuntimeParity":   false,
				"udpStatusReflectsSettings": true,
				"udpRuntimeImplemented":     false,
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"vnet-status-route-auth-and-fields",
					"websocket-relay-auth-modes-and-peer-cleanup",
					"relay-message-contract-and-stable-error-codes",
					"bounded-peer-message-limits",
					"disabled-setting-fallback-payload",
				},
				"requiredProofs": []string{
					"ts-disabled-websocket-upgrade-parity-or-accepted-drift",
					"udp-rendezvous-dataplane-runtime-or-product-deferral",
					"frontend-relay-and-udp-degraded-state-smoke",
					"raw-frame-limit-and-large-message-ts-go-diff",
					"ts-go-vnet-runtime-e2e-diff",
				},
				"acceptance": "prove TypeScript-vs-Go VNet replacement parity across disabled websocket behavior, UDP rendezvous/dataplane or accepted product deferral, frontend relay/UDP degraded states, raw-frame/message limits, and runtime E2E diff before treating Go VNet as replacement-safe",
			},
		},
		"update": updateRuntimeCapability(canApplySelfUpdate),
		"uploads": map[string]any{
			"serveNarratorImages": map[string]any{
				"supported": true,
				"fallback":  false,
			},
			"serveAvatars": map[string]any{
				"supported": true,
				"fallback":  false,
			},
			"cleanupPreservesMessageImageRefs": map[string]any{
				"supported": true,
				"fallback":  false,
			},
			"routeContract": map[string]any{
				"authenticated":            true,
				"gitGuarded":               false,
				"serveNarratorImagesRoute": "GET /api/uploads/{narratorId}/{imageId}",
				"serveAvatarsRoute":        "GET /api/uploads/avatars/{userId}/{imageId}",
				"avatarRouteBeforeGeneric": true,
				"missingStatus":            float64(http.StatusNotFound),
				"missingCode":              "NOT_FOUND",
				"testCase":                 "seeded-upload-serving-and-missing-baselines",
			},
			"serveContract": map[string]any{
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
				"reason":                   "Go serves narrator images and avatars from the uploads root with path/symlink guards; unlike TS upload routes it does not add long-lived immutable cache headers.",
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"seeded-upload-serving-and-missing-baselines",
					"avatar-route-before-generic-contract",
					"upload-path-traversal-guard-fixture",
					"upload-symlink-escape-guard-fixture",
					"storage-cleanup-preserves-message-image-refs",
				},
				"requiredProofs": []string{
					"ts-immutable-cache-header-parity-or-product-decision",
					"browser-upload-cache-behavior-smoke",
					"full-ts-upload-create-serve-cleanup-lifecycle-diff",
					"shared-message-upload-owner-lifecycle-smoke",
					"frontend-avatar-image-reconnect-smoke",
				},
				"reason": "Wave 135 rolls up upload narrator-image/avatar route metadata, path and symlink guards, regular-file serving, imageId prefix lookup, missing-state baselines, and cleanup preservation without claiming TypeScript upload replacement safety. Immutable cache-header parity, browser cache behavior, full upload create/serve/cleanup lifecycle diffs, shared-message upload ownership lifecycle, and frontend avatar/image reconnect smoke remain unproven.",
			},
		},
		"shares": map[string]any{
			"routeContract": map[string]any{
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
			},
			"create": map[string]any{
				"supported":                 true,
				"authenticated":             true,
				"requestFields":             []string{"path", "expiryHours"},
				"responseStatus":            float64(http.StatusCreated),
				"responseFields":            []string{"id", "url", "downloadUrl", "previewUrl", "name", "originalName", "size", "expiresAt"},
				"defaultExpiryHours":        float64(24),
				"maxFileSizeDefaultMb":      float64(4096),
				"tsMaxFileSizeDefaultMb":    float64(500),
				"regularFileOnly":           true,
				"requestPathTraversalGuard": true,
				"symlinkEscapeGuard":        true,
				"sensitiveHomePathGuard":    true,
				"atomicMetadataWrite":       true,
				"fullTSShareCreateParity":   false,
				"reason":                    "Go exposes an authenticated POST /api/shares wrapper for compatibility tooling; TS creates shares inside tools, defaults to a smaller max size, and keeps records process-memory only.",
			},
			"publicDownload": map[string]any{
				"supported":             true,
				"authenticated":         false,
				"metadataHydration":     true,
				"expiredDeletesContent": true,
				"attachmentHeaders":     true,
				"cacheControl":          "no-cache",
				"contentTypeFallback":   "application/octet-stream",
				"nosniffHeader":         true,
			},
			"preview": map[string]any{
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
				"previewTypes": []string{
					".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".avif", ".bmp", ".ico",
					".mp4", ".webm", ".mov", ".ogg", ".pdf", ".html", ".htm",
				},
				"reason": shareEscapedHTMLPreviewReason,
			},
			"lifecycle": map[string]any{
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
			},
			"lifecycleSmokeContract": map[string]any{
				"boundaryTracked":              true,
				"runtimeRow":                   "RUNTIME-SHARE-01",
				"acceptedDesign":               "go-disk-metadata-bridge-ephemeral-on-restart",
				"focusedCleanupFixture":        true,
				"expiredSharePrivacyFixture":   true,
				"unknownDiskShareFixture":      true,
				"metadataTraversalFixture":     true,
				"browserRestartPrivacySmoke":   false,
				"toolCreatedShareBrowserSmoke": false,
				"tsGoRestartDiffSmoke":         false,
				"htmlPreviewProductAccepted":   false,
				"fullTSTimerParity":            false,
				"fullTSProcessMemoryParity":    false,
				"fullTSShareLifecycleParity":   false,
				"replacementSafe":              false,
				"sampledProofs": []string{
					"focused-cleanup-fixture",
					"expired-share-deletes-content",
					"unknown-disk-share-rejected",
					"metadata-path-traversal-rejected",
				},
				"requiredProofs": []string{
					"browser-download-preview-before-after-restart-smoke",
					"tool-created-share-consumer-smoke",
					"ts-go-restart-expiry-privacy-diff-smoke",
					"html-preview-sanitize-vs-escaped-product-acceptance",
					"storage-cleanup-share-public-route-smoke",
				},
				"replacementContract": map[string]any{
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
					"storageCleanupPublicRouteSmoke": false,
					"fullTSTimerParity":              false,
					"fullTSProcessMemoryParity":      false,
					"fullTSShareLifecycleParity":     false,
					"replacementSafe":                false,
					"sampledProofs": []string{
						"focused-cleanup-fixture",
						"expired-share-deletes-content",
						"unknown-disk-share-rejected",
						"metadata-path-traversal-rejected",
					},
					"requiredProofs": []string{
						"browser-download-preview-before-after-restart-smoke",
						"tool-created-share-consumer-smoke",
						"ts-go-restart-expiry-privacy-diff-smoke",
						"html-preview-sanitize-vs-escaped-product-acceptance",
						"storage-cleanup-share-public-route-smoke",
						"full-ts-share-lifecycle-parity",
					},
				},
				"reason": "Wave 115 keeps Go share support as a bounded disk-metadata bridge: focused cleanup, expiry, unknown-directory, and traversal fixtures are present, but broad browser restart/privacy smoke, tool-created share consumer smoke, TS-vs-Go restart diff evidence, and product acceptance of escaped HTML preview are still required before replacement safety.",
			},
			"replacementContract": map[string]any{
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
				"storageCleanupPublicRouteSmoke":    false,
				"fullTSShareLifecycleParity":        false,
				"replacementSafe":                   false,
				"sampledProofs": []string{
					"public-download-preview-missing-state",
					"go-authenticated-share-create-wrapper-contract",
					"focused-cleanup-fixture",
					"expired-share-deletes-content",
					"unknown-disk-share-rejected",
					"metadata-path-traversal-rejected",
				},
				"requiredProofs": []string{
					"browser-download-preview-before-after-restart-smoke",
					"tool-created-share-consumer-smoke",
					"ts-go-restart-expiry-privacy-diff-smoke",
					"html-preview-sanitize-vs-escaped-product-acceptance",
					"storage-cleanup-share-public-route-smoke",
					"ts-share-tool-process-memory-lifecycle-diff",
				},
				"reason": "Wave 131 rolls up share route/create/download/preview, disk-metadata lifecycle, startup cleanup, expiry, unknown-disk-share, and traversal fixtures without claiming TypeScript replacement safety. TS process-memory share lifecycle, TS share-tool create semantics, browser restart/privacy smoke, tool-created share consumer smoke, TS-Go restart/expiry/privacy diffs, sanitize-html product acceptance, storage-cleanup public-route smoke, and full share lifecycle parity remain unproven.",
			},
			"ephemeralOnly": map[string]any{
				"supported": false,
				"fallback":  true,
				"reason":    shareEphemeralFallbackReason,
			},
		},
		"runtime": map[string]any{
			"backend":      buildinfo.Backend,
			"buildChannel": buildinfo.ChannelValue(),
			"routeContract": map[string]any{
				"adminOnly":    true,
				"scan":         "GET /api/runtime/scan",
				"cached":       "GET /api/runtime/cached",
				"cleanup":      "POST /api/runtime/cleanup",
				"staticRoutes": true,
				"authBoundary": "requireAdmin",
				"goExtensionTargets": []string{
					"worktrees",
				},
				"tsCleanupTargets": []string{
					"terminals",
					"containers",
					"browsers",
				},
			},
			"scan": map[string]any{
				"supported":                    true,
				"adminOnly":                    true,
				"readOnly":                     true,
				"cacheWrites":                  true,
				"scannedAtMs":                  true,
				"podmanProbeTimeoutSeconds":    3,
				"podmanScanOutputLimitBytes":   32 * 1024,
				"containerScanSource":          "podman-ps-a-state-not-container_instances-db",
				"containerDBCountParity":       false,
				"browserRuntime":               "go-rod",
				"browserHeadedFieldParity":     false,
				"browserPersistsOptionalTable": true,
				"terminalSchemaDegrades":       true,
				"podmanUnavailableDegrades":    true,
				"fullTSRuntimeScanParity":      false,
				"responseFields":               []string{"terminals", "containers", "browsers", "scannedAt"},
				"terminalFields":               []string{"running", "exited", "orphanSockets", "supported", "degraded"},
				"terminalFallbackFields":       []string{"fallback", "fallbacks", "reason", "error", "message"},
				"terminalFallbackCodes":        []string{"terminal_table_check_failed", "terminal_running_count_failed", "terminal_exited_count_failed"},
				"containerFields":              []string{"running", "stopped", "podmanAvailable", "supported", "fallback"},
				"containerFallbackFields":      []string{"reason", "error", "message"},
				"browserFields":                []string{"processRunning", "connected", "activeSessions", "memorySessions", "persistedSessions", "supported", "fallback", "runtime", "dbTableAvailable", "requiresChrome", "cleanupSupported", "sessionApiSupported"},
			},
			"cached": map[string]any{
				"supported":         true,
				"adminOnly":         true,
				"processMemoryOnly": true,
				"emptyResponse":     map[string]any{"cached": false},
				"hitFields":         []string{"cached", "data"},
				"cacheTTLParity":    false,
				"tsCacheTTLSeconds": 30,
				"goCacheTTLSeconds": 0,
				"invalidatedBy":     []string{"POST /api/runtime/cleanup"},
			},
			"cleanup": map[string]any{
				"routeContract": map[string]any{
					"adminOnly": true,
					"route":     "POST /api/runtime/cleanup",
					"targets":   []string{"terminals", "containers", "browsers", "worktrees", "all"},
				},
				"requestContract": map[string]any{
					"goLooseJSON":              true,
					"maxBodyBytes":             4 * 1024 * 1024,
					"targetRequiredInTS":       true,
					"targetRequiredInGo":       false,
					"defaultTarget":            "all",
					"defaultDryRun":            false,
					"executeFlag":              true,
					"dryRunFlag":               true,
					"modeAliases":              []string{"execute", "dry-run", "dry_run", "preview"},
					"invalidTargetStatus":      float64(http.StatusBadRequest),
					"oversizedBodyStatus":      float64(http.StatusRequestEntityTooLarge),
					"oversizedBodyCode":        "REQUEST_BODY_TOO_LARGE",
					"tsZodParity":              "partial-go-extends-targets-and-mode",
					"emptyBodyRunsAllInGo":     true,
					"frontendExecuteBodyShape": "{target,execute:true}",
				},
				"responseContract": map[string]any{
					"statusOnOperationErrors":   float64(http.StatusOK),
					"operationErrorsUseOKFalse": true,
					"alwaysIncludesEmptyErrors": true,
					"scannedAtMs":               true,
					"executedAtMsWhenNotDryRun": true,
					"legacyFields":              []string{"killed", "stopped", "closedSessions", "browserClosed"},
					"responseFields":            []string{"ok", "target", "dryRun", "removed", "removedCounts", "errors", "errorCount", "outputs", "outputCount", "truncated", "supported", "fallback", "scannedAt", "executedAt"},
					"removedScopes":             []string{"terminals", "containers", "browsers", "worktrees"},
				},
				"executionContract": map[string]any{
					"cacheInvalidation":          true,
					"events":                     false,
					"progressEvents":             false,
					"globalCleanupLock":          false,
					"removedItemLimit":           100,
					"errorItemLimit":             100,
					"outputItemLimit":            20,
					"commandOutputLimitBytes":    32 * 1024,
					"commandTimeoutSeconds":      15,
					"dryRunSkipsMutations":       true,
					"worktreeCleanupGoExtension": true,
					"fullTSCleanupParity":        false,
				},
				"fallbackContract": map[string]any{
					"invalidTargetStatus":          float64(http.StatusBadRequest),
					"invalidTargetMessage":         "invalid runtime cleanup target",
					"podmanMissingExecuteStatus":   float64(http.StatusOK),
					"podmanMissingSetsOKFalse":     true,
					"podmanMissingErrorPrefix":     "PODMAN_NOT_FOUND",
					"terminalMissingTableDegrades": true,
					"missingTablesNoop":            true,
					"structuredErrorFields":        []string{"target", "id", "error"},
				},
				"terminals": map[string]any{
					"supported":             true,
					"mode":                  "kill-exited-and-orphan-records",
					"mutatesRunningRecords": false,
					"orphanRuntimeSupport":  true,
					"dbMutation":            "status=exited exit_code=COALESCE(exit_code,-1)",
				},
				"containers": map[string]any{
					"supported":                true,
					"mode":                     "stop-running-db-records",
					"dbOnlyRunningSupported":   true,
					"podmanStopForContainerID": true,
					"podmanMissingFallback":    true,
					"podmanPrune":              false,
					"storageCleanupReroute":    false,
					"dbMutation":               "status=stopped updated_at=now-when-column-exists",
				},
				"browsers": map[string]any{
					"supported":              true,
					"runtime":                "go-rod",
					"closeMemorySessions":    true,
					"closePersistedSessions": true,
					"optionalTable":          "narrator_browser_sessions",
					"dbMutation":             "status=closed closed_at/updated_at-when-columns-exist",
				},
				"worktrees": map[string]any{
					"supported":              true,
					"mode":                   "go-extension",
					"tsRouteTarget":          false,
					"cleanupTool":            "git worktree remove --force",
					"safePathOnly":           true,
					"pathScope":              "<project.git_path>/.worktrees/*",
					"inactiveStatuses":       []string{"dormant", "merged", "abandoned", "frozen", "closed", "inactive"},
					"orphanDirectoryScan":    true,
					"activeWorktreesSkipped": true,
					"dbMutation":             "chapters.worktree_path=NULL",
				},
			},
			"replacementContract": map[string]any{
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
				"sampledProofs": []string{
					"regular-user-admin-boundary-probes",
					"runtime-scan-cached-cleanup-contract-fixtures",
					"podman-missing-degraded-payload-fixture",
					"terminal-schema-degraded-payload-fixture",
					"bounded-cleanup-output-fixture",
				},
				"requiredProofs": []string{
					"ts-container-db-count-scan-parity-diff",
					"browser-headed-field-parity-or-product-decision",
					"runtime-cache-ttl-parity-or-product-decision",
					"cleanup-event-and-global-lock-parity",
					"large-real-project-scan-cleanup-smoke",
					"worktree-cleanup-extension-ui-decision",
				},
				"reason": "Wave 133 rolls up runtime scan/cached/cleanup route, fallback, and side-effect metadata without claiming TypeScript runtime maintenance replacement safety. Go still scans containers from Podman rather than TS container_instances counts, uses Go Rod browser fields, keeps process-memory cache without TS TTL parity, lacks cleanup events/global locks, adds a Go-only worktree cleanup target, and needs large real-project smoke before full runtime maintenance parity.",
			},
		},
		"gateway": map[string]any{
			"persistentRuntimes":           gatewayRuntimeAttached,
			"persistentRuntimeRoutes":      true,
			"persistentRuntimeAttached":    gatewayRuntimeAttached,
			"mode":                         gatewayMode,
			"fullTSGatewayParity":          false,
			"statusFallbackWhenUnattached": true,
			"reloadFallbackWhenUnattached": true,
			"supportedPlatforms":           []string{"telegram", "webhook"},
			"unsupportedPlatforms":         map[string]string{"discord": "not implemented in go_backend", "slack": "not implemented in go_backend", "feishu": "not implemented in go_backend", "weixin": "QR/runtime not implemented in go_backend", "qqbot": "not implemented in go_backend"},
			"routeContract": map[string]any{
				"publicWebhook":       "POST /api/gateway/webhook",
				"authenticatedStatus": "GET /api/gateway/status",
				"authenticatedSessions": []string{
					"GET /api/gateway/sessions",
					"DELETE /api/gateway/sessions/{id}",
				},
				"authenticatedReload":   "POST /api/gateway/reload",
				"authenticatedWeixinQr": []string{"POST /api/gateway/weixin/qr-start", "GET /api/gateway/weixin/qr-poll"},
				"publicWebhookAuth":     false,
				"privateRoutesAuth":     true,
				"gitGuarded":            false,
			},
			"runtimeContract": map[string]any{
				"persistentRuntimeRoutes":      true,
				"persistentRuntimeAttached":    gatewayRuntimeAttached,
				"statusDelegatesWhenAttached":  true,
				"reloadDelegatesWhenAttached":  true,
				"statusRouteFallbackPayload":   true,
				"reloadRouteFallbackPayload":   true,
				"reloadFiltersPlatforms":       false,
				"unsupportedPlatformsFiltered": true,
				"fullTSGatewayParity":          false,
				"fallbackReason":               "Persistent gateway runtimes are not bundled in go_backend",
				"reason":                       "Go exposes gateway compatibility routes and webhook ingestion, but full persistent TypeScript gateway runtimes are only available when an external GatewayRuntime is attached.",
			},
			"replacementContract": map[string]any{
				"boundaryTracked":             true,
				"persistentRuntimeRequired":   true,
				"persistentRuntimeAttached":   gatewayRuntimeAttached,
				"webhookCompatibilityOnly":    true,
				"statusReloadFallbackSafe":    true,
				"frontendGatewaySectionSmoke": false,
				"attachedRuntimeSmoke":        false,
				"multiPlatformRuntimeParity":  false,
				"qrLoginRuntimeParity":        false,
				"sessionRoutingRuntimeParity": false,
				"fullTSGatewayParity":         false,
				"replacementSafe":             false,
				"sampledProofs": []string{
					"status-reload-fallback-envelope",
					"public-hmac-webhook-fallback-diagnostics",
					"weixin-qr-501-json-fallback",
				},
				"requiredProofs": []string{
					"attached-gateway-runtime-status-reload-smoke",
					"frontend-gateway-section-disabled-fallback-smoke",
					"telegram-runtime-session-routing-smoke",
					"qr-login-runtime-or-disabled-product-decision",
					"ts-go-gateway-session-webhook-diff-smoke",
				},
				"reason": "Wave 117 keeps gateway replacement unsafe until an attached GatewayRuntime and frontend disabled/fallback UI are proven. Current Go provides status/reload fallback envelopes, public HMAC webhook compatibility, and Weixin QR 501 JSON fallback, but not full TypeScript persistent runtime parity.",
			},
			"statusContract": map[string]any{
				"fallbackStatus":               200,
				"fallbackMode":                 "fallback",
				"fallbackSupported":            false,
				"fallbackFields":               []string{"started", "platforms", "sessions", "configuredUsers", "mode", "supported", "fallback", "reason", "error", "message", "webhook"},
				"sessionsRead":                 true,
				"configuredUsersCount":         true,
				"degradedFallbacks":            true,
				"queryFailureFallbacks":        []string{"gateway_sessions_query_failed", "gateway_user_preferences_table_check_failed", "gateway_configured_users_count_failed"},
				"webhookSubstatusReasonFields": true,
			},
			"reloadContract": map[string]any{
				"fallbackStatus":        200,
				"fallbackOk":            true,
				"fallbackReloadedEmpty": true,
				"statusPayload":         true,
				"webhookSubstatus":      true,
				"reasonErrorMessage":    true,
			},
			"sessionContract": map[string]any{
				"listReturnsArray":                   true,
				"deleteReturnsOkAndDeleted":          true,
				"missingStorageDeleteReturnsOkFalse": true,
				"storageTable":                       "gateway_session_mappings",
			},
			"webhook": map[string]any{
				"supported":                    true,
				"fallback":                     false,
				"mode":                         "http-hmac-autocreate-standalone-narrator",
				"publicRoute":                  true,
				"maxBodyBytes":                 1048576,
				"requiresSecret":               true,
				"signatureHeaders":             []string{"X-Hub-Signature-256", "X-Webhook-Signature"},
				"requiresNarratorRuntime":      true,
				"narratorRuntimeAttached":      gatewayNarratorRuntimeAttached,
				"notConfiguredCode":            "GATEWAY_WEBHOOK_NOT_CONFIGURED",
				"invalidSignatureCode":         "GATEWAY_WEBHOOK_INVALID_SIGNATURE",
				"missingTextCode":              "GATEWAY_WEBHOOK_MISSING_TEXT",
				"runtimeUnavailableCode":       "GATEWAY_WEBHOOK_RUNTIME_UNAVAILABLE",
				"storageUnavailableCode":       "GATEWAY_WEBHOOK_STORAGE_UNAVAILABLE",
				"staleMappingCode":             "GATEWAY_WEBHOOK_STALE_MAPPING",
				"acceptedFallbackStatus":       202,
				"autocreateStandaloneNarrator": true,
				"standaloneTitlePrefix":        "Webhook: ",
				"messageCreatorPrefix":         "gateway:",
				"sessionMappingTable":          "gateway_session_mappings",
				"updatesLastMessageAt":         true,
			},
			"weixinQr": map[string]any{
				"supported":  false,
				"fallback":   true,
				"code":       "WEIXIN_QR_UNSUPPORTED",
				"status":     "unavailable",
				"httpStatus": 501,
				"startResponseFields": []string{
					"ok",
					"status",
					"supported",
					"fallback",
					"code",
					"qrcodeUrl",
					"qrcodeToken",
					"reason",
					"error",
					"message",
				},
				"pollResponseFields":  []string{"ok", "status", "supported", "fallback", "code", "reason", "error", "message"},
				"qrcodeUrlEmpty":      true,
				"qrcodeTokenEmpty":    true,
				"rejectBeforeRuntime": true,
				"reason":              "Weixin QR gateway runtime is not bundled in go_backend.",
			},
		},
		"storage": map[string]any{
			"scan": map[string]any{
				"supported": true,
				"readOnly":  true,
				"adminOnly": true,
				"sse":       true,
				"cache":     true,
				"routes": map[string]any{
					"scan":   true,
					"cached": true,
				},
				"events": []string{"progress", "category", "complete", "error"},
				"normalEventOrder": []string{
					"progress:scanning_database",
					"category:database",
					"progress:scanning_uploads",
					"category:uploads",
					"progress:scanning_shares",
					"category:shares",
					"progress:scanning_worktrees",
					"category:worktrees",
					"progress:scanning_containers",
					"category:containers",
					"complete",
				},
				"categories":     []string{"database", "uploads", "shares", "worktrees", "containers"},
				"completeFields": []string{"categories", "totalBytes", "scannedAt"},
				"cacheResponse": map[string]any{
					"empty":         map[string]any{"cached": false},
					"afterComplete": map[string]any{"cached": true, "data": "last-complete-payload"},
				},
				"limits": map[string]any{
					"maxStorageScanEntries":    5000,
					"maxStorageProjectRows":    100,
					"maxDatabaseSummaryTables": 50,
				},
				"dbScanMode":                     "approximate",
				"containersCategoryRuntimeProbe": false,
				"tsStorageServiceParity":         "sampled-sse-contract-not-full-runtime-smoke",
			},
			"cached": map[string]any{
				"supported": true,
				"cache":     true,
			},
			"database": storageDatabaseMaintenanceCapability(),
			"vacuum": map[string]any{
				"supported": false,
				"reason":    "Main-thread SQLite VACUUM is disabled in go_backend; use an explicit maintenance window/tool outside request handling.",
			},
			"cleanup": map[string]any{
				"routeContract": map[string]any{
					"adminOnly": true,
					"route":     "POST /api/storage/cleanup",
					"targets":   []string{"uploads", "shares", "worktrees", "containers"},
				},
				"requestContract": map[string]any{
					"targetRequired":       true,
					"tsZodParity":          "target-enum-only",
					"goLooseJSON":          true,
					"invalidTargetStatus":  float64(http.StatusBadRequest),
					"invalidTargetMessage": "Invalid target",
				},
				"responseContract": map[string]any{
					"uploadsFields": []string{"ok", "removed", "freedBytes"},
					"sharesFields":  []string{"ok", "removed", "freedBytes", "memoryRemoved"},
				},
				"fallbackContract": map[string]any{
					"unsupportedStatus": float64(http.StatusNotImplemented),
					"unsupportedFields": []string{"ok", "code", "feature", "supported", "fallback", "reason", "error", "message"},
					"worktreesCode":     "STORAGE_CLEANUP_WORKTREES_UNSUPPORTED",
					"containersCode":    "STORAGE_CLEANUP_CONTAINERS_UNSUPPORTED",
					"rejectBeforeWork":  true,
				},
				"uploads": map[string]any{
					"supported":                 true,
					"preservesMessageImageRefs": true,
				},
				"shares": map[string]any{
					"supported": true,
				},
				"worktrees": map[string]any{
					"supported":               false,
					"fallback":                true,
					"alternative":             "runtime.cleanup.worktrees",
					"runtimeRerouteAvailable": true,
					"runtimeRerouteTarget":    "runtime.cleanup.worktrees",
					"reason":                  "Storage cleanup does not remove git worktrees in go_backend; use chapter/runtime cleanup so git state and chapter metadata stay consistent.",
				},
				"containers": map[string]any{
					"supported":               false,
					"fallback":                true,
					"alternative":             "runtime.cleanup.containers",
					"runtimeRerouteAvailable": false,
					"runtimeRerouteTarget":    "runtime.cleanup.containers",
					"reason":                  "Podman prune is disabled from storage cleanup in go_backend; keep storage cleanup disabled and use per-chapter container controls or explicit runtime cleanup instead of automatic storage-side reroute.",
				},
			},
			"replacementContract": map[string]any{
				"boundaryTracked":                   true,
				"runtimeRow":                        "STORAGE-MAINTENANCE-01",
				"scanSSEContractProof":              true,
				"databaseMaintenanceProof":          true,
				"storageCleanupFallbackProof":       true,
				"uploadShareCleanupProof":           true,
				"adminBoundaryProof":                true,
				"unsupportedWorktreeFallbackProof":  true,
				"unsupportedContainerFallbackProof": true,
				"fullTSStorageScanParity":           false,
				"fullTSStorageCleanupParity":        false,
				"fullTSDatabaseMaintenanceParity":   false,
				"storageCacheInvalidationParity":    false,
				"worktreeStorageCleanupSupported":   false,
				"containerStorageCleanupSupported":  false,
				"staleSessionsCleanupSupported":     false,
				"requestPathVacuumSupported":        false,
				"backgroundCompactionJob":           false,
				"replacementSafe":                   false,
				"sampledProofs": []string{
					"storage-scan-sse-order-contract",
					"regular-user-admin-boundary-probes",
					"storage-worktree-container-unsupported-fallback-fixture",
					"storage-database-vacuum-disabled-fixture",
					"upload-share-cleanup-route-contract",
				},
				"requiredProofs": []string{
					"broader-storage-service-runtime-smoke",
					"worktree-container-storage-cleanup-product-decision",
					"stale-sessions-cleanup-product-decision",
					"storage-cache-invalidation-parity-smoke",
					"background-vacuum-compaction-job-or-decision",
					"full-ts-zod-storage-maintenance-parity",
				},
				"reason": "Wave 134 rolls up storage scan, storage cleanup, and database maintenance metadata without claiming TypeScript storage maintenance replacement safety. Go keeps storage worktree/container cleanup disabled or rerouted, stale-session cleanup disabled, request-path VACUUM disabled, apiRequestDumps as raw-dump clearing, cache invalidation parity incomplete, and broader storage-service runtime smoke unproven.",
			},
		},
	}
	for key, value := range domainRuntimeCapabilities() {
		caps[key] = value
	}
	return caps
}

func canApplySelfUpdateFromHealth(restartManager *restart.Manager) bool {
	if restartManager == nil {
		return false
	}
	execPath, err := os.Executable()
	if err != nil || strings.TrimSpace(execPath) == "" {
		return false
	}
	info, err := os.Stat(execPath)
	return err == nil && !info.IsDir() && info.Mode().IsRegular()
}

func updateRuntimeCapability(canApplySelfUpdate bool) map[string]any {
	apply := map[string]any{
		"supported": canApplySelfUpdate,
		"fallback":  !canApplySelfUpdate,
	}
	if canApplySelfUpdate {
		apply["handoff"] = "graceful-restart"
	} else {
		apply["code"] = "FEATURE_DISABLED"
		apply["reason"] = "Automatic self-update requires a configured restart handoff manager and a regular current executable."
	}
	return map[string]any{
		"selfUpdateAvailable": canApplySelfUpdate,
		"manualOnly":          !canApplySelfUpdate,
		"canAutoRestart":      canApplySelfUpdate,
		"routes": map[string]any{
			"check":     "GET /api/update/check",
			"version":   "GET /api/update/version",
			"directory": "GET /api/update/directory",
			"status":    "GET /api/update/status",
			"cleanup":   "POST /api/update/cleanup",
			"download":  "POST /api/update/download",
			"apply":     "POST /api/update/apply",
		},
		"authContract": map[string]any{
			"check":               "authenticated",
			"version":             "authenticated",
			"directory":           "admin",
			"status":              "admin",
			"cleanup":             "admin",
			"download":            "admin-sse-or-json-preflight",
			"apply":               "admin",
			"regularDeniedStatus": float64(403),
		},
		"checkContract": map[string]any{
			"timeoutSeconds":                 float64(5),
			"trustedSourceRequiredForRemote": true,
			"defaultNoSourceReason":          "settings.update.serverUrl is not configured",
			"failureStatus":                  float64(200),
			"failurePreservesLatestVersion":  true,
			"devBuildNoDownload":             true,
			"trustedSourceMetadata":          []string{"product", "channel"},
			"responseFields":                 []string{"updateAvailable", "currentVersion", "latestVersion", "releaseInfo", "source", "selfUpdateAvailable", "canAutoRestart", "manualOnly", "instructions"},
			"availableFields":                []string{"downloadSize", "totalSize", "strategy", "patchChain"},
			"releaseInfoFields":              []string{"version", "releaseDate", "path", "sha512", "files"},
		},
		"versionContract": map[string]any{
			"responseFields": []string{"version", "commit", "backend", "buildChannel", "platform", "arch", "goVersion", "selfUpdateAvailable", "manualOnly", "canAutoRestart"},
		},
		"directoryContract": map[string]any{
			"adminOnly":         true,
			"responseFields":    []string{"directory", "selfUpdateAvailable", "canAutoRestart", "manualOnly", "instructions"},
			"baseDirectory":     "~/.narrafork/updates",
			"manualOnlyDefault": !canApplySelfUpdate,
		},
		"statusContract": map[string]any{
			"adminOnly":                       true,
			"versionQuery":                    true,
			"metadataFile":                    "metadata.json",
			"legacyArtifactFallback":          true,
			"responseFields":                  []string{"ready", "downloaded", "version", "directory", "canAutoRestart", "placed", "selfUpdateAvailable", "manualOnly", "instructions"},
			"readyFields":                     []string{"updateFile", "updatePath", "artifactPath", "files"},
			"manualOnlyDefault":               !canApplySelfUpdate,
			"autoReadyRequiresRestartHandoff": true,
		},
		"cleanupContract": map[string]any{
			"adminOnly":               true,
			"optionalVersion":         true,
			"safePathGuard":           true,
			"baseDirectoryNotRemoved": true,
			"oldEntryMaxAgeHours":     float64(168),
			"responseFields":          []string{"success", "removed", "directory"},
		},
		"download": map[string]any{
			"supported": true,
			"sse":       true,
			"sha512":    true,
			"maxBytes":  512 * 1024 * 1024,
			"trustMode": "server-configured-trusted-source",
		},
		"downloadContract": map[string]any{
			"supported":                         true,
			"adminOnly":                         true,
			"sseProgress":                       true,
			"sseEvents":                         []string{"progress", "complete", "error"},
			"normalEventOrder":                  []string{"progress:downloading", "complete"},
			"terminalErrorEvent":                true,
			"terminalCompleteEvent":             true,
			"jsonPreflightBeforeSSE":            true,
			"noUpdateStatus":                    float64(404),
			"noUpdateJSONError":                 "No update available",
			"requestedVersionMismatchStatus":    float64(409),
			"requestedVersionMismatchFields":    []string{"error", "requestedVersion", "latestVersion"},
			"ignoresClientSuppliedArtifactURLs": true,
			"refetchesTrustedMetadata":          true,
			"sha512MetadataRequired":            true,
			"sha512HexOrBase64":                 true,
			"sha512MismatchCleansArtifact":      true,
			"trustedSourceRequired":             true,
			"devBuildNoDownload":                true,
			"contextTimeoutSeconds":             float64(600),
			"maxBytes":                          512 * 1024 * 1024,
			"metadataFile":                      "metadata.json",
			"completeFields":                    []string{"success", "version", "downloaded", "ready", "bytes", "updatePath", "artifactPath", "directory", "files", "placed", "selfUpdateAvailable", "canAutoRestart", "manualOnly", "instructions"},
			"errorFields":                       []string{"success", "code", "version", "reason", "error", "message", "selfUpdateAvailable", "canAutoRestart", "manualOnly", "instructions"},
			"reason":                            "Go downloads only server-configured trusted release artifacts with SHA512 metadata and bounded size; dev builds stay non-downloadable/manual-only.",
		},
		"successFixtureContract": map[string]any{
			"boundaryTracked":                   true,
			"localTrustedArtifactDownloadSmoke": true,
			"localApplyFallbackSmoke":           true,
			"compiledGoArtifactDownloadSmoke":   false,
			"zstdPatchFixtureSmoke":             false,
			"patchChainFixtureSmoke":            false,
			"autoApplyHandoffSmoke":             canApplySelfUpdate,
			"rollbackDrillCIWired":              false,
			"fullTSSelfUpdateParity":            false,
			"releaseUploadPolicy":               "metadata-plus-zstd-patch-only",
			"acceptance":                        "prove download success and apply/rollback against compiled Go artifacts, zstd patch metadata, patch-chain fixtures, restart handoff, and a required rollback drill before promoting update self-update beyond manual/guarded compatibility",
			"requiredProofs": []string{
				"compiled-go-artifact-download-sse",
				"zstd-patch-metadata-and-patch-chain-fixture",
				"frontend-success-state-browser-smoke",
				"auto-apply-restart-handoff-smoke",
				"ts-go-ts-rollback-drill-ci-report",
			},
			"sampledProofs": []string{
				"admin-boundary",
				"trusted-check-metadata",
				"trusted-download-error-sse",
				"local-trusted-artifact-download",
				"apply-fallback-manual-only",
			},
		},
		"replacementContract": map[string]any{
			"boundaryTracked":                   true,
			"runtimeRow":                        "UPDATE-SELF-APPLY-01",
			"manualOnly":                        !canApplySelfUpdate,
			"selfUpdateAvailable":               canApplySelfUpdate,
			"downloadRouteContractProof":        true,
			"applyRouteFallbackProof":           true,
			"localTrustedArtifactDownloadSmoke": true,
			"localApplyFallbackSmoke":           true,
			"compiledGoArtifactDownloadSmoke":   false,
			"zstdPatchFixtureSmoke":             false,
			"patchChainFixtureSmoke":            false,
			"frontendSuccessStateSmoke":         false,
			"autoApplyHandoffSmoke":             canApplySelfUpdate,
			"rollbackDrillCIWired":              false,
			"fullTSSelfUpdateParity":            false,
			"replacementSafe":                   false,
			"sampledProofs": []string{
				"update-route-auth-and-admin-boundary",
				"trusted-check-metadata-shape",
				"trusted-download-error-sse-shape",
				"local-trusted-artifact-download",
				"manual-only-apply-fallback",
			},
			"requiredProofs": []string{
				"compiled-go-artifact-download-sse",
				"zstd-patch-metadata-and-patch-chain-fixture",
				"frontend-update-success-state-browser-smoke",
				"auto-apply-restart-handoff-smoke",
				"ts-go-ts-rollback-drill-ci-report",
				"release-upload-artifact-policy-decision",
			},
			"acceptance": "prove TypeScript-vs-Go update replacement parity across compiled Go artifact downloads, zstd patch-chain metadata, frontend success state, auto-apply restart handoff, TS-Go-TS rollback drill CI/report evidence, and release upload artifact policy before treating Go self-update as replacement-safe",
		},
		"apply": apply,

		"applyContract": map[string]any{
			"supported":                           canApplySelfUpdate,
			"manualOnlyWhenUnavailable":           !canApplySelfUpdate,
			"requiresRestartHandoffManager":       true,
			"requiresRegularExecutable":           true,
			"requiresTrustedDownloadedArtifact":   true,
			"requiresSha512Verification":          true,
			"preparedArtifactRequired":            true,
			"preparedBinaryPrefix":                ".narrafork-update-",
			"verifiesArtifactBeforeHandoff":       true,
			"startsReplacementProcess":            canApplySelfUpdate,
			"usesGracefulRestartHandoff":          canApplySelfUpdate,
			"cancelsRestartSessionOnStartFailure": true,
			"fallbackCode":                        "FEATURE_DISABLED",
			"autoApplySmoke":                      canApplySelfUpdate,
			"fallbackResponseFields":              []string{"success", "serverRestarting", "restarting", "canAutoRestart", "selfUpdateAvailable", "manualOnly", "message", "instructions"},
			"successResponseFields":               []string{"success", "serverRestarting", "restarting", "ready", "placed", "canAutoRestart", "selfUpdateAvailable", "manualOnly", "instructions", "message", "version", "newBinaryPath", "replacementPid"},
			"failureResponseFields":               []string{"success", "serverRestarting", "restarting", "canAutoRestart", "selfUpdateAvailable", "manualOnly", "error", "message"},
			"artifactPathTraversalGuard":          true,
			"artifactRegularFileRequired":         true,
			"artifactSizeVerified":                true,
			"reason":                              "Automatic Go self-update is conditional on a restart handoff manager, regular current executable, and a trusted downloaded artifact; otherwise apply stays manual-only with an explicit disabled payload.",
		},
	}
}

func providerCapabilities() map[string]any {
	models := func(refresh bool) map[string]any {
		return map[string]any{"supported": true, "refreshSupported": refresh}
	}
	routes := func(names ...string) map[string]any {
		payload := map[string]any{"supported": true}
		for _, name := range names {
			payload[name] = true
		}
		return payload
	}
	mergeRouteCapabilities := func(base map[string]any, extra map[string]any) map[string]any {
		for key, value := range extra {
			base[key] = value
		}
		return base
	}
	agentRuntime := func(features ...string) map[string]any {
		payload := map[string]any{"supported": true}
		for _, feature := range features {
			payload[feature] = true
		}
		return payload
	}
	disabledQuota := func(reason string) map[string]any {
		return map[string]any{
			"supported":                  false,
			"fallback":                   true,
			"fallbackScope":              "capability-gate-only",
			"code":                       "FEATURE_DISABLED",
			"routeMounted":               false,
			"routeFallbackImplemented":   false,
			"expectedRouteStatus":        404,
			"expectedRouteCode":          "NOT_FOUND",
			"noUpstreamProbe":            true,
			"noSecretReadRequired":       true,
			"tsLiveQuotaParity":          false,
			"frontendCapabilityGateOnly": true,
			"reason":                     reason,
		}
	}
	auth := func(supported bool, reason string) map[string]any {
		payload := map[string]any{"supported": supported}
		if !supported {
			payload["fallback"] = true
			payload["code"] = "FEATURE_DISABLED"
		}
		if reason != "" {
			payload["reason"] = reason
		}
		return payload
	}
	return map[string]any{
		"routeAuthContract": map[string]any{
			"authenticatedModelProviders":       []string{"openai", "anthropic", "nkp", "nug"},
			"adminOnlyProviderControlGroups":    []string{"kiro", "codex", "cline"},
			"codexQuotaOverviewAuthenticated":   true,
			"sensitiveRouteDeniedStatus":        float64(403),
			"modelRefreshRegularUserCompatible": true,
			"settingsBackedProviderSecrets":     true,
			"providerMissingStatus":             float64(404),
			"providerMissingErrorIncludesID":    true,
		},
		"modelRefreshContract": map[string]any{
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
		},
		"quotaRouteContract": map[string]any{
			"openAIAnthropicDisabledCapabilityGateOnly": true,
			"nkpQuotaPublicFields":                      []string{"quotaBalance", "quotaTotalGranted"},
			"nugQuotaPublicFields":                      []string{"balance", "totalGranted"},
			"missingProviderStatus":                     float64(404),
			"cachedPublicResponsesOmitFetchedAt":        true,
		},
		"frontendGateContract": map[string]any{
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
			"gatedActions": []string{
				"provider-routes",
				"model-refresh",
				"quota-fetch",
				"agent-mode-runtime",
				"credential-control",
				"kiro-chat-sse",
			},
			"sampledProviderFamilies": []string{
				"openai",
				"anthropic",
				"nkp",
				"nug",
				"codex",
				"cline",
				"kiro",
			},
		},
		"replacementContract": map[string]any{
			"boundaryTracked":                  true,
			"runtimeRow":                       "PROVIDER-ROUTES-01",
			"routeAuthContractProof":           true,
			"modelRefreshShapeProof":           true,
			"quotaPublicFieldProof":            true,
			"frontendGateMetadataProof":        true,
			"localFixtureProviderFamilies":     []string{"openai", "anthropic", "nkp", "nug", "codex", "cline", "kiro"},
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
			"sampledProofs": []string{
				"provider-route-auth-contract",
				"model-refresh-response-shape-contract",
				"quota-public-field-contract",
				"frontend-gate-metadata-contract",
				"local-fixture-provider-families",
				"kiro-chat-and-model-refresh-disabled-fallbacks",
				"codex-manager-disabled-scheduler-and-usage-queue-contract",
			},
			"requiredProofs": []string{
				"full-provider-route-inventory-or-explicit-deferrals",
				"live-upstream-model-refresh-and-quota-smoke",
				"live-credential-refresh-oauth-browser-device-flows",
				"kiro-chat-sse-or-disabled-product-decision",
				"kiro-model-refresh-plugin-bridge-live-smoke",
				"codex-manager-scheduler-usage-queue-decision-or-live-parity",
				"cline-live-oauth-browser-auth-balance-smoke",
				"nug-live-login-api-key-oauth-billing-smoke",
				"agent-runtime-provider-specific-live-smoke",
			},
			"acceptance": "prove TypeScript-vs-Go provider replacement parity across full route inventory, live upstream model/quota/credential flows, Kiro chat/model-refresh decisions, Codex manager scheduler/usage queue behavior, Cline/NUG live credential flows, and provider-specific agent runtime smoke before treating Go provider integrations as replacement-safe",
		},
		"runtimeTelemetry": map[string]any{
			"apiRequestUsage":                true,
			"apiRequestCredentialId":         true,
			"apiRequestContextPercent":       true,
			"apiRequestMeterUsage":           true,
			"assistantMessageContextPercent": true,
			"assistantMessageMeterUsage":     true,
			"meteringEventCredentialId":      true,
		},
		"kiro": map[string]any{
			"auth": auth(true, "Credential import and credential management routes are implemented in go_backend compatibility mode."),
			"models": map[string]any{
				"supported":                 true,
				"source":                    "cached-per-credential-model-metadata",
				"refreshSupported":          false,
				"refreshCode":               "FEATURE_DISABLED",
				"refreshFallbackBody":       true,
				"refreshFallbackHTTPStatus": 501,
				"livePluginRefresh":         false,
				"fullPluginRefreshParity":   false,
				"reason":                    "Kiro model refresh requires the TypeScript Kiro plugin bridge; go_backend reads cached per-credential model metadata only.",
			},
			"quota":     map[string]any{"supported": true, "fallback": false},
			"agentMode": map[string]any{"supported": true, "fallback": false},
			"routes": mergeRouteCapabilities(routes(
				"status",
				"models",
				"mcpTools",
				"mcpSearch",
				"globalProxy",
				"loadBalancingMode",
				"credentials",
				"credentialsImport",
				"credentialDisable",
				"credentialEnable",
				"credentialReset",
				"credentialPriority",
				"credentialRefresh",
				"credentialUsage",
				"credentialUpdate",
				"credentialDelete",
			), map[string]any{
				"modelsRefresh":          false,
				"chat":                   false,
				"chatSSE":                false,
				"chatFallbackBody":       true,
				"chatFallbackHTTPStatus": 501,
				"chatCode":               "FEATURE_DISABLED",
				"chatReason":             "Kiro chat streaming is not implemented in go_backend; use the narrator/provider agent routes or the TypeScript backend for Kiro chat streaming.",
				"reason":                 "Some Kiro routes are disabled in go_backend compatibility mode; model refresh and chat streaming require TypeScript Kiro runtimes.",
			}),
			"agentRuntime": mergeRouteCapabilities(agentRuntime(
				"httpCompatible", "sse", "pluginBridge",
			), map[string]any{
				"requiresConfiguredHTTPOrPluginBridge": true,
				"fullTSKiroRuntimeParity":              false,
				"localLiveSmoke":                       "manual-env-only",
			}),
			"pluginBridge": map[string]any{
				"supported":   true,
				"requiresBun": true,
				"reason":      "Kiro plugin bridge is available only when the external Bun bridge/plugin environment is present.",
			},
			"settingsContract": map[string]any{
				"adminOnlyCredentialManagement": true,
				"cachedModelsOnly":              true,
				"modelRefreshDisabledStatus":    float64(501),
				"modelRefreshDisabledCode":      "FEATURE_DISABLED",
				"chatDisabledStatus":            float64(501),
				"chatDisabledCode":              "FEATURE_DISABLED",
				"statusFields":                  []string{"snapshot", "usageCache"},
				"credentialModelSource":         "cached-per-credential-model-metadata",
				"credentialUsageShape":          true,
				"livePluginRefreshParity":       false,
			},
			"mcp": map[string]any{
				"listToolsRoute": true,
				"searchRoute":    true,
				"agentInjection": false,
			},
		},
		"openai": map[string]any{
			"auth":      auth(true, "OpenAI-compatible credentials are managed through settings providers."),
			"models":    models(true),
			"quota":     disabledQuota("OpenAI-compatible quota inspection is not available in go_backend; use provider-specific upstream dashboards or TypeScript/live-provider tooling."),
			"agentMode": map[string]any{"supported": true, "fallback": false},
			"routes": mergeRouteCapabilities(
				routes("models", "modelsRefresh", "perProviderModels", "perProviderModelsRefresh", "refresh"),
				map[string]any{"quota": false, "quotas": false},
			),
			"agentRuntime": agentRuntime(
				"chatCompletions", "responsesApi", "toolCalling", "streaming",
			),
		},
		"anthropic": map[string]any{
			"auth":      auth(true, "Anthropic credentials are managed through settings providers."),
			"models":    models(true),
			"quota":     disabledQuota("Anthropic quota inspection is not available in go_backend; use provider-specific upstream dashboards or TypeScript/live-provider tooling."),
			"agentMode": map[string]any{"supported": true, "fallback": false},
			"routes": mergeRouteCapabilities(
				routes("models", "modelsRefresh", "perProviderModels", "perProviderModelsRefresh", "refresh"),
				map[string]any{"quota": false, "quotas": false},
			),
			"agentRuntime": agentRuntime(
				"messagesApi", "toolCalling", "streaming",
			),
		},
		"nkp": map[string]any{
			"auth":      auth(true, "NKP provider configuration is managed through settings providers."),
			"models":    models(true),
			"quota":     map[string]any{"supported": true, "fallback": false},
			"agentMode": map[string]any{"supported": true, "fallback": false},
			"routes":    routes("models", "modelsRefresh", "perProviderModels", "perProviderModelsRefresh", "quota", "quotas", "refresh"),
			"agentRuntime": agentRuntime(
				"kiroCompatible", "requestToken", "streaming",
			),
		},
		"nug": map[string]any{
			"auth":      auth(true, "NUG login, API key, and OAuth helper routes are implemented in go_backend compatibility mode."),
			"models":    models(true),
			"quota":     map[string]any{"supported": true, "fallback": false},
			"agentMode": map[string]any{"supported": true, "fallback": false},
			"routes": routes(
				"models",
				"modelsRefresh",
				"perProviderModels",
				"perProviderModelsRefresh",
				"auth",
				"login",
				"apiKey",
				"oauthStart",
				"oauthCallback",
				"quota",
				"quotas",
				"billingConfig",
				"billingCreateOrder",
				"billingGetOrder",
				"billingRepayOrder",
				"channelsHealth",
				"usage",
				"usageSummary",
			),
			"agentRuntime": agentRuntime(
				"openaiCompatible", "streaming",
			),
			"settingsContract": map[string]any{
				"loginNoImplicitSettingsPersistence":  true,
				"apiKeyNoImplicitSettingsPersistence": true,
				"oauthCallbackPersistsCredentials":    true,
				"oauthCallbackRedirectsSettings":      true,
				"oauthCallbackMasksAPIKeyInSettings":  true,
				"missingLoginStatus":                  float64(404),
				"missingAPIKeyStatus":                 float64(404),
				"loginMissingFieldsStatus":            float64(400),
				"apiKeyMissingSessionTokenStatus":     float64(400),
				"quotaFields":                         []string{"balance", "totalGranted"},
				"channelsHealthFields":                []string{"available", "channels"},
				"usageFieldsCamelCase":                true,
				"usageSummaryTotalsNumeric":           true,
			},
		},
		"codex": map[string]any{
			"auth":      auth(true, "Codex browser/device/import credential routes are implemented in go_backend compatibility mode."),
			"models":    map[string]any{"supported": true, "refreshSupported": false},
			"quota":     map[string]any{"supported": true, "fallback": false},
			"agentMode": map[string]any{"supported": true, "fallback": false},
			"routes": mergeRouteCapabilities(routes(
				"status",
				"defaultReasoningEffort",
				"useWebSocket",
				"useWebSearch",
				"useImageGeneration",
				"loadBalancingMode",
				"tierOrder",
				"globalProxy",
				"import",
				"browserAuth",
				"browserAuthWait",
				"browserAuthCancel",
				"deviceAuth",
				"deviceAuthStart",
				"deviceAuthPoll",
				"deviceAuthCancel",
				"credentialManagement",
				"credentialDisable",
				"credentialEnable",
				"credentialReset",
				"credentialRefresh",
				"credentialUsage",
				"credentialUpdate",
				"credentialBatchDelete",
				"credentialDeleteUnhealthy",
				"credentialDelete",
				"quotaOverview",
			), map[string]any{
				"usageQueueClear":       false,
				"usageQueueClearCode":   "FEATURE_DISABLED",
				"usageQueueClearReason": "Go compatibility mode does not run the TypeScript CodexManager usage queue; clearing it would be a false success.",
			}),
			"agentRuntime": agentRuntime(
				"responsesWebSocket", "responsesHttpFallback", "credentialPoolFailover",
			),
			"managerParity": map[string]any{
				"tsCodexManagerEquivalent": false,
				"schedulerParity":          "disabled",
				"schedulerSupported":       false,
				"usageQueueParity":         "disabled",
				"usageQueueClearSupported": false,
				"usageQueueClearCode":      "FEATURE_DISABLED",
				"liveUsageFetchParity":     "route-local-unverified",
				"liveUsageScheduler":       false,
				"snapshotPaginationParity": "supported",
				"credentialAffinity":       "sticky-session-failover-supported",
				"credentialPoolFailover":   true,
				"rebuildHistoryRetryCode":  "codex_quota_failover_rebuild",
				"reason":                   "Go exposes Codex compatibility routes/runtime but not the full TS CodexManager scheduler/usage queue/live usage-fetch semantics; usage queue clear and scheduler behavior are disabled instead of returning false success. Credential-pool failover, sticky-session affinity, and rebuild-history retry are supported by focused runtime fixtures.",
			},
			"settingsContract": map[string]any{
				"adminOnlyCredentialManagement":   true,
				"quotaOverviewAuthenticated":      true,
				"statusFiltersInvalidCredentials": true,
				"statusSkipsDuplicateCredentials": true,
				"statusDefaultsMissingFields":     true,
				"importRejectsEmptyCredentials":   true,
				"importRejectsNoTokenCredentials": true,
				"importAcceptsAccessTokenOnly":    true,
				"importParsesTextExports":         true,
				"missingCredentialMutationStatus": float64(404),
				"missingCredentialUsageStatus":    float64(400),
				"credentialUpdateInvalidJSONNoop": true,
				"usageQueueClearDisabledCode":     "FEATURE_DISABLED",
			},
		},
		"cline": map[string]any{
			"auth":      auth(true, "Cline browser/callback/logout routes are implemented in go_backend compatibility mode."),
			"models":    models(true),
			"quota":     map[string]any{"supported": true, "fallback": false},
			"agentMode": map[string]any{"supported": true, "fallback": false},
			"routes": routes(
				"status",
				"models",
				"modelsRefresh",
				"perProviderModels",
				"perProviderModelsRefresh",
				"balance",
				"recommendedModels",
				"poolSearch",
				"poolCount",
				"enabledModels",
				"userInfoRefresh",
				"auth",
				"authBrowser",
				"authCancel",
				"authCallback",
				"logout",
			),
			"agentRuntime": agentRuntime(
				"openaiCompatible", "streaming",
			),
			"settingsContract": map[string]any{
				"adminOnlyCredentialManagement":      true,
				"readEndpointsDoNotMutateProvider":   true,
				"statusFields":                       []string{"authenticated", "providers"},
				"providerStatusFields":               []string{"id", "name", "prefix", "hasToken"},
				"recommendedModelsRefreshAndCache":   true,
				"providerRefreshPreservesDuplicates": true,
				"aggregateModelsDeduplicate":         true,
				"enabledModelsPersistContextWindows": true,
				"authCallbackPersistsCredentials":    true,
				"browserAuthPersistsCredentials":     true,
				"autoAddsFreeModelsOnAuth":           true,
				"pastedCallbackSupported":            true,
				"userInfoRefreshSupported":           true,
				"expiredTokenRefreshBeforeBalance":   true,
				"invalidRefreshClearsCredentials":    true,
				"unauthenticatedUserInfoStatus":      float64(200),
				"unauthenticatedBalanceStatus":       float64(422),
			},
		},
	}
}

func gracefullyShutdownHandler(manager *restart.Manager) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var body restart.GracefulShutdownRequest
		if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
			return err
		}
		status, payload := manager.Handle(r.Context(), body)
		httpx.WriteJSON(w, status, payload)
		return nil
	}
}

func authStatusHandler(sqlDB *sql.DB, settings *config.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var count int
		if err := sqlDB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
			httpx.WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"hasUsers": count > 0, "registrationOpen": settings.Core().Auth.RegistrationOpen})
	}
}

func staticFrontendHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "API route not found", "code": "NOT_FOUND"})
			return
		}
		dir, ok := frontendStaticDir()
		if !ok {
			httpx.WriteJSON(w, http.StatusNotFound, map[string]any{
				"ok":      false,
				"code":    "FRONTEND_NOT_BUNDLED",
				"feature": "static_frontend",
				"mode":    "api-only",
				"reason":  "No built frontend directory was found; go_backend is running in API-only mode.",
			})
			return
		}
		path := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if path == "." || path == "" {
			path = "index.html"
		}
		candidate := filepath.Join(dir, path)
		cleanDir := filepath.Clean(dir)
		if rel, err := filepath.Rel(cleanDir, candidate); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			http.NotFound(w, r)
			return
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			http.ServeFile(w, r, candidate)
			return
		}
		index := filepath.Join(dir, "index.html")
		if info, err := os.Stat(index); err == nil && !info.IsDir() {
			http.ServeFile(w, r, index)
			return
		}
		http.NotFound(w, r)
	}
}

func frontendStaticDir() (string, bool) {
	candidates := []string{}
	if envDir := strings.TrimSpace(os.Getenv("NARRAFORK_FRONTEND_DIR")); envDir != "" {
		candidates = append(candidates, envDir)
	}
	cwd, _ := os.Getwd()
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)
	candidates = append(candidates,
		filepath.Join(cwd, "dist", "frontend"),
		filepath.Join(cwd, "..", "dist", "frontend"),
		filepath.Join(exeDir, "dist", "frontend"),
		filepath.Join(exeDir, "..", "dist", "frontend"),
	)
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !info.IsDir() {
			return dir, true
		}
	}
	return "", false
}

func changelogHandler(w http.ResponseWriter, r *http.Request) error {
	entries := []map[string]any{}
	for _, dir := range changelogCandidateDirs() {
		files, err := filepath.Glob(filepath.Join(dir, "v*.json"))
		if err != nil || len(files) == 0 {
			continue
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			entry := map[string]any{}
			if err := json.Unmarshal(data, &entry); err != nil {
				continue
			}
			if version, _ := entry["version"].(string); version != "" {
				entries = append(entries, entry)
			}
		}
		break
	}
	sort.Slice(entries, func(i, j int) bool {
		return compareSemverDesc(stringValue(entries[i]["version"]), stringValue(entries[j]["version"])) < 0
	})
	httpx.WriteJSON(w, http.StatusOK, entries)
	return nil
}

func changelogCandidateDirs() []string {
	candidates := []string{}
	if envDir := strings.TrimSpace(os.Getenv("NARRAFORK_CHANGELOG_DIR")); envDir != "" {
		candidates = append(candidates, envDir)
	}
	cwd, _ := os.Getwd()
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)
	candidates = append(candidates,
		filepath.Join(cwd, "changelogs"),
		filepath.Join(cwd, "..", "changelogs"),
		filepath.Join(exeDir, "changelogs"),
		filepath.Join(exeDir, "..", "changelogs"),
	)
	return candidates
}

func compareSemverDesc(a, b string) int {
	av := semverParts(a)
	bv := semverParts(b)
	for i := 0; i < 3; i++ {
		if av[i] != bv[i] {
			if bv[i] > av[i] {
				return 1
			}
			return -1
		}
	}
	return strings.Compare(b, a)
}

func semverParts(value string) [3]int {
	value = strings.TrimPrefix(value, "v")
	parts := strings.Split(value, ".")
	out := [3]int{}
	for i := 0; i < len(parts) && i < 3; i++ {
		part := ""
		for _, ch := range parts[i] {
			if ch < '0' || ch > '9' {
				break
			}
			part += string(ch)
		}
		if part != "" {
			_, _ = fmt.Sscanf(part, "%d", &out[i])
		}
	}
	return out
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func registerDependencyRoutes(r chi.Router, authService *auth.Service, git *gitx.Service) {
	r.Get("/api/dependencies", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		httpx.WriteJSON(w, http.StatusOK, dependencyStatus(ctx, git))
	})
	r.With(authService.AdminOnly).Post("/api/dependencies/{name}/install", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		name := chi.URLParam(r, "name")
		result := installDependency(ctx, name, git)
		status := http.StatusOK
		if ok, _ := result["ok"].(bool); !ok {
			status = http.StatusInternalServerError
		}
		httpx.WriteJSON(w, status, result)
	})
}

const maxDependencyInstallOutputBytes = 128 * 1024

var dependencyInstallCommands = map[string]map[string]string{
	"git": {
		"apt": "sudo apt-get update && sudo apt-get install -y git", "dnf": "sudo dnf install -y git",
		"pacman": "sudo pacman -S --noconfirm git", "zypper": "sudo zypper install -y git",
		"apk": "sudo apk add git", "brew": "brew install git", "winget": "winget install -e --id Git.Git",
		"scoop": "scoop install git", "choco": "choco install git -y",
	},
	"rg": {
		"apt": "sudo apt-get update && sudo apt-get install -y ripgrep", "dnf": "sudo dnf install -y ripgrep",
		"pacman": "sudo pacman -S --noconfirm ripgrep", "zypper": "sudo zypper install -y ripgrep",
		"apk": "sudo apk add ripgrep", "brew": "brew install ripgrep", "winget": "winget install BurntSushi.ripgrep.MSVC",
		"scoop": "scoop install ripgrep", "choco": "choco install ripgrep -y",
	},
	"dtach": {
		"apt": "sudo apt-get update && sudo apt-get install -y dtach", "dnf": "sudo dnf install -y dtach",
		"pacman": "sudo pacman -S --noconfirm dtach", "zypper": "sudo zypper install -y dtach",
		"apk": "sudo apk add dtach", "brew": "brew install dtach",
	},
}

func dependencyStatus(ctx context.Context, git *gitx.Service) map[string]any {
	deps := []map[string]any{checkDependency(ctx, "git", git), checkDependency(ctx, "rg", git), checkDependency(ctx, "dtach", git)}
	allRequiredMet := true
	for _, dep := range deps {
		if required, _ := dep["required"].(bool); required {
			installed, _ := dep["installed"].(bool)
			if !installed {
				allRequiredMet = false
			}
		}
	}
	payload := map[string]any{"platform": dependencyPlatform(), "dependencies": deps, "allRequiredMet": allRequiredMet}
	if pm := detectPackageManager(); pm != "" {
		payload["packageManager"] = pm
	}
	return payload
}

func installDependency(ctx context.Context, name string, git *gitx.Service) map[string]any {
	commands := dependencyInstallCommands[name]
	if len(commands) == 0 {
		return map[string]any{"ok": false, "error": fmt.Sprintf("Unknown dependency: %s", name)}
	}
	pm := detectPackageManager()
	cmd := commands[pm]
	if pm == "" || cmd == "" {
		return map[string]any{"ok": false, "error": fmt.Sprintf("No install command available for package manager: %s", ternary(pm != "", pm, "none")), "dependency": checkDependency(ctx, name, git)}
	}
	output, err := runShellCommand(ctx, cmd)
	if err != nil {
		msg := strings.TrimSpace(output)
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return map[string]any{"ok": false, "error": msg, "dependency": checkDependency(ctx, name, git)}
	}
	dependency := checkDependency(ctx, name, git)
	if installed, _ := dependency["installed"].(bool); !installed {
		return map[string]any{
			"ok":         false,
			"code":       "DEPENDENCY_INSTALL_NOT_VERIFIED",
			"error":      "Install command completed, but the dependency is still unavailable on PATH.",
			"dependency": dependency,
		}
	}
	return map[string]any{"ok": true, "dependency": dependency}
}

func checkDependency(ctx context.Context, name string, git *gitx.Service) map[string]any {
	switch name {
	case "git":
		installed := false
		if git != nil {
			installed = git.Available(ctx)
		} else if _, err := exec.LookPath("git"); err == nil {
			installed = true
		}
		return map[string]any{"name": "git", "required": true, "installed": installed, "version": commandVersion(ctx, "git", "--version"), "platformSupported": true, "installCommands": dependencyInstallCommands["git"]}
	case "rg":
		_, err := exec.LookPath("rg")
		return map[string]any{"name": "rg", "required": false, "installed": err == nil, "version": firstLine(commandVersion(ctx, "rg", "--version")), "platformSupported": true, "installCommands": dependencyInstallCommands["rg"]}
	case "dtach":
		if runtime.GOOS == "windows" {
			return map[string]any{"name": "dtach", "required": false, "installed": false, "platformSupported": false, "installCommands": map[string]string{}}
		}
		_, err := exec.LookPath("dtach")
		return map[string]any{"name": "dtach", "required": false, "installed": err == nil, "platformSupported": true, "installCommands": dependencyInstallCommands["dtach"]}
	default:
		return map[string]any{"name": name, "required": false, "installed": false, "platformSupported": false, "installCommands": map[string]string{}}
	}
}

func dependencyPlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	default:
		return "linux"
	}
}

func runShellCommand(ctx context.Context, command string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "cmd", "/c", command)
	} else {
		cmd = exec.CommandContext(cctx, "sh", "-c", command)
	}
	output := &limitedCommandOutput{limit: maxDependencyInstallOutputBytes}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	return output.String(), err
}

type limitedCommandOutput struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (w *limitedCommandOutput) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		w.truncated = true
		return len(p), nil
	}
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = w.buf.Write(p[:remaining])
		w.truncated = true
		return len(p), nil
	}
	_, _ = w.buf.Write(p)
	return len(p), nil
}

func (w *limitedCommandOutput) String() string {
	out := w.buf.String()
	if w.truncated {
		out += "\n[truncated]"
	}
	return out
}

func ternary(cond bool, yes, no string) string {

	if cond {
		return yes
	}
	return no
}

func detectPackageManager() string {
	if runtime.GOOS == "windows" {
		for _, name := range []string{"winget", "scoop", "choco"} {
			if _, err := exec.LookPath(name); err == nil {
				return name
			}
		}
		return ""
	}
	if runtime.GOOS == "darwin" {
		for _, name := range []string{"brew", "port"} {
			if _, err := exec.LookPath(name); err == nil {
				return name
			}
		}
		return ""
	}
	for _, candidate := range []struct{ binary, name string }{{"apt-get", "apt"}, {"dnf", "dnf"}, {"pacman", "pacman"}, {"zypper", "zypper"}, {"apk", "apk"}} {
		if _, err := exec.LookPath(candidate.binary); err == nil {
			return candidate.name
		}
	}
	return ""
}

func commandVersion(ctx context.Context, name string, args ...string) string {
	if _, err := exec.LookPath(name); err != nil {
		return ""
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if idx := strings.IndexByte(value, '\n'); idx >= 0 {
		return strings.TrimSpace(value[:idx])
	}
	return value
}

func registerKnownPlaceholders(r chi.Router) {
	groups := []string{}
	for _, prefix := range groups {
		p := prefix
		r.HandleFunc(p, httpx.Handler(notImplemented(p[5:])))
		r.HandleFunc(p+"/*", httpx.Handler(notImplemented(p[5:])))
	}
}

func notImplemented(feature string) httpx.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error { return httpx.NotImplemented(feature) }
}
