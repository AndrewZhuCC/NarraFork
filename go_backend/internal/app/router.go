package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

	r.Get("/api/health", healthHandler(s.Git, s.TerminalRuntime, s.DBEngine, s.Restart))
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

func healthHandler(git *gitx.Service, terminalRuntime *terminalruntime.Manager, engine db.Engine, restartManager *restart.Manager) http.HandlerFunc {
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
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"status":       "ok",
			"version":      buildinfo.VersionValue(),
			"commit":       buildinfo.CommitValue(),
			"backend":      buildinfo.Backend,
			"buildChannel": buildinfo.ChannelValue(),
			"platform":     platform,
			"gitAvailable": gitAvailable,
			"capabilities": runtimeCapabilities(platform, terminalCapabilities, engine, canApplySelfUpdate),
		})
	}
}

const terminalXtermSerializedReplayReason = "Go backend replays capped raw PTY output instead of TS xterm-headless serialized buffer state."

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
		},
		"processTree": map[string]any{
			"supported": platform != "windows",
			"platform":  map[bool]string{true: "unix-ps", false: "unsupported"}[platform != "windows"],
		},
	}
	for key, value := range runtimeCaps {
		terminal[key] = value
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
		"cleanupTargets": map[string]any{
			"apiRequestDumps": map[string]any{
				"supported": true,
			},
			"archivedSessions": map[string]any{
				"supported":          true,
				"fallback":           false,
				"archivedOnly":       true,
				"usesNarratorDelete": true,
				"vacuumRan":          false,
			},
			"staleSessions": map[string]any{
				"supported": false,
				"fallback":  true,
				"code":      "FEATURE_DISABLED",
				"reason":    disabledStaleNarratorSessionCleanupReason,
			},
		},
	}
}

func runtimeCapabilities(platform string, terminalCapabilities map[string]any, engine db.Engine, canApplySelfUpdate bool) map[string]any {
	frontendDir, frontendHosted := frontendStaticDir()
	terminal := phase4TerminalCapabilities(platform, terminalCapabilities)
	frontend := map[string]any{"staticHosted": frontendHosted, "mode": "api-only"}
	if frontendHosted {
		frontend["mode"] = "static"
		frontend["directory"] = frontendDir
	}
	fsRevealSupported := platform == "windows" || platform == "macos"
	fsReveal := map[string]any{
		"supported": fsRevealSupported,
		"fallback":  !fsRevealSupported,
	}
	if !fsRevealSupported {
		fsReveal["code"] = "FEATURE_DISABLED"
		fsReveal["reason"] = "Opening file manager is not supported on this platform"
	}
	database := map[string]any{
		"engine":           string(db.EngineSQLite),
		"mainSchemaOwner":  "typescript-drizzle",
		"goMainMigrations": false,
		"goEnsureColumns":  false,
		"ftsRepair":        false,
		"mode":             "ts-owned-db-compatibility",
		"searchMode":       "sqlite-fts5-with-like-fallback",
		"reason":           "go_backend uses the TypeScript/Drizzle-created main database and only performs read-only schema compatibility checks.",
	}
	if engine == db.EnginePostgres {
		database = map[string]any{
			"engine":           string(db.EnginePostgres),
			"mainSchemaOwner":  "go-postgres",
			"goMainMigrations": true,
			"goEnsureColumns":  true,
			"ftsRepair":        false,
			"mode":             "go-owned-postgres-schema",
			"searchMode":       "postgres-like",
			"reason":           "go_backend uses Go-owned Postgres schema/migrations and Postgres LIKE search fallback; SQLite FTS5 objects are not migrated.",
		}
	}
	return map[string]any{
		"database": database,
		"frontend": frontend,
		"releasePackaging": map[string]any{
			"buildInfo":          "ldflags-or-env",
			"frontend":           "sidecar-directory-or-env",
			"changelog":          "sidecar-directory-or-env",
			"singleFileEmbedded": false,
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
				"supported": true,
				"fallback":  false,
			},
			"preview": map[string]any{
				"supported":      true,
				"maxTextBytes":   1 << 20,
				"maxBinaryBytes": 20 * 1024 * 1024,
			},
			"reveal": fsReveal,
		},
		"content": map[string]any{
			"projectRoutines": map[string]any{
				"supported": true,
				"storage":   "main-db-project-chapter-settings",
				"fallback":  false,
			},
			"projectSkills": map[string]any{
				"supported": true,
				"storage":   "project-git-path-scan",
				"fallback":  false,
			},
		},
		"chapters": map[string]any{
			"split": graph.SplitCapability(),
			"batchMerge": map[string]any{
				"supported":              true,
				"fallback":               false,
				"mode":                   "async-merge-session",
				"routes":                 map[string]any{"start": true, "session": true},
				"response":               map[string]any{"mergeSessionId": true, "targetChapterId": true, "createdTarget": true, "status": true},
				"events":                 []string{"merge:started", "merge:step_ok", "merge:conflict", "merge:ai_resolving", "merge:completed", "merge:cancelled", "merge:error"},
				"decisionWs":             true,
				"staleSessionCleanup":    true,
				"createdTargetRollback":  true,
				"frontendCompletionMode": "progress-event-or-session-poll",
			},
			"containers": map[string]any{
				"supported": true,
				"routes": map[string]any{
					"setup": true, "podmanStatus": true, "podmanInstall": true, "composeInfo": true,
					"list": true, "start": true, "stop": true, "pause": true, "unpause": true,
					"logs": true, "remove": true,
				},
				"runtime": map[string]any{
					"podmanCompose": true, "podmanComposeFallbackCommand": true, "boundedOutput": true,
					"syncStartRequest": false, "backgroundStart": true, "backgroundStartReason": containerBackgroundStartReason, "streamingLogs": true,
					"perChapterLock": true,
				},
				"ports": map[string]any{
					"legacyHostPortAllocation": true,
					"portRelease":              true,
				},
				"proxy": map[string]any{
					"metadataSupported":   true,
					"requiresPastaPasst":  true,
					"overridePortsReset":  true,
					"reverseProxyServer":  true,
					"http":                true,
					"websocket":           true,
					"dynamicSettingsHook": true,
				},
				"lifecycle": map[string]any{
					"manualControls":       true,
					"autoStartOnFork":      true,
					"pauseOnDormant":       true,
					"unpauseOnWake":        true,
					"removeOnDelete":       true,
					"removeOnMergeCleanup": true,
					"deleteVolumes":        true,
				},
				"narratorIntegration": map[string]any{
					"statusChangedEvent":          true,
					"containerReadyMessage":       true,
					"browserToolAutoEnable":       false,
					"browserToolAutoEnableReason": containerBrowserToolAutoEnableReason,
					"defaultEnabled":              false,
					"cutover":                     "explicit-enable",
					"rollback":                    "explicit-disable",
				},
			},
		},
		"narrator": map[string]any{
			"wsEvents": map[string]any{
				"p0": map[string]any{
					"supported": true,
					"events":    []string{"message", "user_message", "stream_event", "tool_started", "tool_use_chunk", "tool_completed", "permission_request", "permission_resolved", "status_change", "substatus_change", "catch_up", "sync_ok", "full_reload", "streaming_snapshot"},
				},
				"p1": map[string]any{
					"supported": true,
					"mode":      "baseline-plus-phase4-followup",
					"events":    []string{"background_task_started", "background_task_completed", "background_task_failed", "background_task_cancelled", "background_task_status_changed", "background_task_output", "todos_updated", "goals_set", "context_usage", "git_status", "tool_inventory", "metering", "nkp_quota_balance", "nkp_queue_status", "quota_balance", "queue_status"},
				},
			},
			"messageHistory": map[string]any{
				"supported":         true,
				"catchUp":           true,
				"messageVersion":    true,
				"childOrphans":      true,
				"toolCalls":         true,
				"compactMarkers":    true,
				"structuredContent": true,
			},
			"delete": map[string]any{
				"supported":            true,
				"fallback":             false,
				"archivedOnly":         true,
				"recursive":            true,
				"sharedMessageHandoff": true,
				"uploadPreservation":   true,
			},
			"permissions": map[string]any{
				"supported":    true,
				"modes":        []string{"readOnly", "default", "acceptEdits", "bypassPermissions", "dontAsk"},
				"approveDeny":  true,
				"updatedInput": true,
				"pauseResume":  "db-pending-tool-call",
				"reflections":  []string{"danger", "plan", "goal"},
			},
			"toolInventory": map[string]any{
				"supported":                true,
				"categories":               []string{"core", "optional", "review", "reflection", "mcp"},
				"supportedOptionalTools":   []string{"Terminal", "Browser", "ShareFile", "Recall"},
				"unsupportedOptionalTools": []string{"ForkNarrator", "NarraForkAdmin"},
				"recall": map[string]any{
					"supported":              true,
					"readOnly":               true,
					"scope":                  "self",
					"crossNarrator":          false,
					"searchMode":             "bounded-like",
					"maxSearchLimit":         50,
					"maxReadLimit":           50,
					"maxBatchQueries":        10,
					"maxToolCallOutputChars": 4000,
				},
				"read": map[string]any{
					"supported":       true,
					"parity":          "partial",
					"textFiles":       true,
					"directories":     true,
					"offsetLimit":     true,
					"pdfPages":        false,
					"pagesSchemaOnly": true,
					"imageContent":    false,
				},
				"webFetch": map[string]any{
					"supported":                true,
					"parity":                   "partial",
					"mode":                     "raw-only",
					"defaultMode":              "raw",
					"supportedModes":           []string{"raw"},
					"unsupportedModes":         []string{"readability", "dom", "smart", "screenshot"},
					"protocols":                []string{"http", "https", "data"},
					"policy":                   "permission-policy-and-proxy-consumed",
					"rawRequiresChrome":        false,
					"screenshotRequiresChrome": true,
					"smartRequiresProvider":    true,
					"reason":                   "Go WebFetch performs raw HTTP/HTTPS/data fetches only; TS extraction modes are rejected explicitly instead of being silently ignored.",
				},
				"browser": map[string]any{
					"supported":             true,
					"parity":                "partial",
					"runtime":               "go-rod",
					"screenshotPreviewMode": "inline-base64-or-file-artifact",
					"sharePreview":          false,
					"imageContentBlock":     false,
					"fileOutput":            true,
					"traceFormat":           "chrome-devtools-json",
					"traceShare":            true,
					"traceFileOutput":       true,
					"traceShareUrl":         "/api/shares/{shareId}",
					"reason":                "Go Browser screenshots return JSON/base64 or an explicit file_path artifact; TS additionally returns an image content block and temporary share preview metadata. Browser perf_stop writes Chrome trace JSON and creates a functional share URL when file_path is omitted.",
				},
				"mcpExternalTools": map[string]any{
					"supported": true,
					"parity":    "partial",
					"transport": "stdio",
					"lifecycle": "per-call",
				},
			},
			"planMode": map[string]any{
				"supported":           true,
				"api":                 true,
				"toolReflection":      true,
				"previousModeRestore": true,
			},
			"compact": map[string]any{
				"supported":       true,
				"markers":         true,
				"segmentCompact":  true,
				"contextClear":    true,
				"fallbackSummary": true,
				"fallbackReason":  "TS-equivalent model-generated compaction is not implemented in go_backend; summaries are caller-provided or deterministic from refs.",
				"mode":            "deterministic-fallback-or-caller-summary",
				"summaryGeneration": map[string]any{
					"supported": false,
					"kind":      "model",
					"reason":    "Go backend does not generate model summaries without an external provider.",
				},
				"summaryFallback": map[string]any{
					"supported":     true,
					"mode":          "caller-summary-or-deterministic-ref-preview",
					"deterministic": true,
				},
			},
			"retryRecovery": map[string]any{
				"supported":         true,
				"retry":             true,
				"continue":          true,
				"interrupt":         true,
				"manualOverride":    true,
				"rollback":          true,
				"editAndRegenerate": true,
			},
			"subagents": map[string]any{
				"supported":                 true,
				"foreground":                true,
				"background":                true,
				"awaitAgent":                true,
				"awaitBash":                 true,
				"awaitBashWaitForText":      true,
				"supportedTypes":            []string{"agent", "bash"},
				"narratorContextOnly":       true,
				"awaitBashReason":           "Await type=bash and wait_for_text are supported in narrator context; non-narrator ExecuteTool does not support Bash run_in_background.",
				"send":                      true,
				"teamStatus":                true,
				"detachAttach":              true,
				"detachUnblocksParent":      true,
				"reattachBlocksParent":      false,
				"reattachFallback":          true,
				"reattachReason":            "Go can detach and open subagent sessions, but cannot re-block an already-running parent tool promise after reattach.",
				"backgroundResultInjection": true,
				"staleRecovery":             true,
			},
			"reviewTools": map[string]any{
				"supported":         true,
				"concludeReview":    true,
				"feedbackInjection": true,
				"promote":           true,
				"dismiss":           true,
				"convertToSubagent": true,
				"staleMergeGuard":   true,
			},
			"browserSessions": map[string]any{
				"supported":           true,
				"defaultEnabled":      false,
				"fallback":            false,
				"runtime":             "go-rod",
				"storage":             "go-owned-optional-table",
				"cutover":             "explicit-enable",
				"rollback":            "explicit-disable-or-export",
				"reason":              browserSessionsDefaultOffReason,
				"narratorBound":       true,
				"lifecycleEvents":     true,
				"artifactPersistence": true,
				"resourceLimits":      true,
				"requiresChrome":      true,
			},
			"containerBrowserToolAutoEnable": map[string]any{
				"defaultEnabled": false,
				"cutover":        "explicit-enable",
				"rollback":       "explicit-disable",
				"reason":         containerBrowserToolAutoEnableReason,
			},
			"rollbackEditRegenerate": map[string]any{
				"supported":            true,
				"fallback":             false,
				"rollback":             true,
				"editAndRegenerate":    true,
				"copyOnWrite":          true,
				"messageRefTruncation": true,
				"fileStateRebuild":     true,
				"toolCallInvalidation": true,
				"agentRerun":           true,
				"optionalFileRevert":   true,
				"optionalAgentRerun":   true,
				"wsEvents":             true,
			},
		},
		"mcp": map[string]any{
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
		},
		"settings": map[string]any{
			"storage": map[string]any{
				"supported": true,
				"path":      "~/.narrafork/settings.json",
			},
			"patch": map[string]any{
				"supported": true,
			},
			"validation": map[string]any{
				"tsZodParity": false,
				"mode":        "loose-json-with-normalization",
				"reason":      "Go backend normalizes settings before saving but does not apply the full TypeScript Zod schema strictly.",
			},
			"secretMasking":             true,
			"providerModelAugmentation": true,
			"tlsGeneration":             true,
			"retryRules":                true,
		},
		"providers": providerCapabilities(),
		"benchmark": map[string]any{
			"containerExecution": map[string]any{
				"supported":        true,
				"fallback":         false,
				"runtime":          "podman",
				"resourceLimits":   true,
				"timeout":          true,
				"outputLimitBytes": 1024 * 1024,
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
		},
		"shares": map[string]any{
			"create": map[string]any{
				"supported": true,
			},
			"publicDownload": map[string]any{
				"supported": true,
			},
			"preview": map[string]any{
				"supported": true,
				"htmlMode":  "escaped-pre",
				"reason":    shareEscapedHTMLPreviewReason,
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
			"scan": map[string]any{
				"supported": true,
			},
			"cached": map[string]any{
				"supported": true,
			},
			"cleanup": map[string]any{
				"terminals": map[string]any{"supported": true},
				"containers": map[string]any{
					"supported": true,
					"mode":      "stop-running-db-records",
				},
				"browsers":  map[string]any{"supported": true},
				"worktrees": map[string]any{"supported": true, "mode": "go-extension"},
			},
		},
		"gateway": map[string]any{
			"persistentRuntimes":   true,
			"mode":                 "persistent-plus-http-webhook",
			"supportedPlatforms":   []string{"telegram", "webhook"},
			"unsupportedPlatforms": map[string]string{"discord": "not implemented in go_backend", "slack": "not implemented in go_backend", "feishu": "not implemented in go_backend", "weixin": "QR/runtime not implemented in go_backend", "qqbot": "not implemented in go_backend"},
			"webhook": map[string]any{
				"supported": true,
				"fallback":  false,
				"mode":      "http-hmac-autocreate-standalone-narrator",
			},
			"weixinQr": map[string]any{
				"supported": false,
				"fallback":  true,
				"code":      "WEIXIN_QR_UNSUPPORTED",
				"reason":    "Weixin QR gateway runtime is not bundled in go_backend.",
			},
		},
		"storage": map[string]any{
			"scan": map[string]any{
				"supported": true,
				"sse":       true,
				"cache":     true,
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
				"uploads": map[string]any{
					"supported":                 true,
					"preservesMessageImageRefs": true,
				},
				"shares": map[string]any{
					"supported": true,
				},
				"worktrees": map[string]any{
					"supported":   false,
					"fallback":    true,
					"alternative": "runtime.cleanup.worktrees",
					"reason":      "Storage cleanup does not remove git worktrees in go_backend; use chapter/runtime cleanup so git state and chapter metadata stay consistent.",
				},
				"containers": map[string]any{
					"supported":   false,
					"fallback":    true,
					"alternative": "runtime.cleanup.containers",
					"reason":      "Podman prune is disabled from storage cleanup in go_backend; use per-chapter container controls or an external maintenance command.",
				},
			},
		},
	}
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
		"download": map[string]any{
			"supported": true,
			"sse":       true,
			"sha512":    true,
			"maxBytes":  512 * 1024 * 1024,
			"trustMode": "server-configured-trusted-source",
		},
		"apply": apply,
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
		"kiro": map[string]any{
			"auth": auth(true, "Credential import and credential management routes are implemented in go_backend compatibility mode."),
			"models": map[string]any{
				"supported":        true,
				"refreshSupported": false,
				"reason":           "Kiro model refresh requires the TypeScript Kiro plugin bridge; go_backend reads cached per-credential model metadata only.",
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
				"modelsRefresh": false,
				"chat":          false,
				"chatCode":      "FEATURE_DISABLED",
				"chatReason":    "Kiro chat streaming is not implemented in go_backend; use the narrator/provider agent routes or the TypeScript backend for Kiro chat streaming.",
				"reason":        "Some Kiro routes are disabled in go_backend compatibility mode; model refresh and chat streaming require TypeScript Kiro runtimes.",
			}),
			"agentRuntime": agentRuntime(
				"httpCompatible", "sse", "pluginBridge",
			),
			"pluginBridge": map[string]any{
				"supported":   true,
				"requiresBun": true,
				"reason":      "Kiro plugin bridge is available only when the external Bun bridge/plugin environment is present.",
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
			"quota":     map[string]any{"supported": false, "fallback": true, "code": "FEATURE_DISABLED", "reason": "OpenAI-compatible quota inspection is not available in go_backend."},
			"agentMode": map[string]any{"supported": true, "fallback": false},
			"routes":    routes("models", "modelsRefresh", "perProviderModels", "perProviderModelsRefresh", "refresh"),
			"agentRuntime": agentRuntime(
				"chatCompletions", "responsesApi", "toolCalling", "streaming",
			),
		},
		"anthropic": map[string]any{
			"auth":      auth(true, "Anthropic credentials are managed through settings providers."),
			"models":    models(true),
			"quota":     map[string]any{"supported": false, "fallback": true, "code": "FEATURE_DISABLED", "reason": "Anthropic quota inspection is not available in go_backend."},
			"agentMode": map[string]any{"supported": true, "fallback": false},
			"routes":    routes("models", "modelsRefresh", "perProviderModels", "perProviderModelsRefresh", "refresh"),
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
		},
		"codex": map[string]any{
			"auth":      auth(true, "Codex browser/device/import credential routes are implemented in go_backend compatibility mode."),
			"models":    map[string]any{"supported": true, "refreshSupported": false},
			"quota":     map[string]any{"supported": true, "fallback": false},
			"agentMode": map[string]any{"supported": true, "fallback": false},
			"routes": routes(
				"status",
				"defaultReasoningEffort",
				"useWebSocket",
				"loadBalancingMode",
				"tierOrder",
				"globalProxy",
				"import",
				"usageQueueClear",
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
				"credentialDelete",
				"quotaOverview",
			),
			"agentRuntime": agentRuntime(
				"responsesWebSocket", "responsesHttpFallback", "credentialPoolFailover",
			),
			"managerParity": map[string]any{
				"tsCodexManagerEquivalent": false,
				"usageQueueParity":         "partial",
				"usageQueueClearSupported": true,
				"snapshotPaginationParity": "supported",
				"reason":                   "Go exposes Codex compatibility routes/runtime but not the full TS CodexManager scheduler/usage queue semantics.",
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
