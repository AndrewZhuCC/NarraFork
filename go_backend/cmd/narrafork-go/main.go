package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"narrafork-go/internal/app"
	"narrafork-go/internal/auth"
	"narrafork-go/internal/buildinfo"
	"narrafork-go/internal/chapters"
	"narrafork-go/internal/config"
	"narrafork-go/internal/containerproxy"
	"narrafork-go/internal/db"
	"narrafork-go/internal/gatewayruntime"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/misccompat"
	"narrafork-go/internal/narrators"
	"narrafork-go/internal/notifications"
	"narrafork-go/internal/projects"
	"narrafork-go/internal/restart"
	"narrafork-go/internal/terminalruntime"
	"narrafork-go/internal/worktreewatcher"
	"narrafork-go/internal/ws"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

func tlsListenConfig(core config.Core) (bool, string, string) {
	certFile := strings.TrimSpace(core.Server.TLS.CertFile)
	keyFile := strings.TrimSpace(core.Server.TLS.KeyFile)
	return core.Server.TLS.Enabled && certFile != "" && keyFile != "", certFile, keyFile
}

func validateSQLiteToPostgresCutoverFlags(dryRun bool, verify bool, cutover bool) error {
	if !cutover {
		return nil
	}
	if dryRun {
		return fmt.Errorf("--cutover cannot be combined with --dry-run")
	}
	if !verify {
		return fmt.Errorf("--cutover requires --verify")
	}
	return nil
}

func sqliteToPostgresCutoverPatch(sqlitePath string, postgresDSN string, postgresSchema string) map[string]any {
	return map[string]any{
		"database": map[string]any{
			"driver":         string(db.EnginePostgres),
			"postgresDsn":    postgresDSN,
			"postgresSchema": strings.TrimSpace(postgresSchema),
			"sqlitePath":     sqlitePath,
			"migratedFromSqlite": map[string]any{
				"path":       sqlitePath,
				"verifiedAt": db.NowISO(),
			},
		},
	}
}

func writeSQLiteToPostgresCutoverSettings(settings *config.Store, sqlitePath string, postgresDSN string, postgresSchema string) (string, error) {
	backupPath := settings.Path() + ".sqlite-to-postgres.bak"
	data, err := os.ReadFile(settings.Path())
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		return "", err
	}
	_, err = settings.Patch(sqliteToPostgresCutoverPatch(sqlitePath, postgresDSN, postgresSchema))
	if err != nil {
		return "", err
	}
	return backupPath, nil
}

func runMigrate(args []string) error {
	if len(args) == 0 || args[0] != "sqlite-to-postgres" {
		return fmt.Errorf("usage: narrafork-go migrate sqlite-to-postgres --sqlite <path> --postgres <dsn> [--schema <schema>] [--dry-run] [--verify] [--cutover]")
	}
	flags := flag.NewFlagSet("migrate sqlite-to-postgres", flag.ContinueOnError)
	sqlitePath := flags.String("sqlite", "", "source SQLite database path")
	postgresDSN := flags.String("postgres", "", "target Postgres DSN")
	postgresSchema := flags.String("schema", "", "optional target Postgres schema")
	dryRun := flags.Bool("dry-run", false, "only preflight and print the migration plan")
	verify := flags.Bool("verify", false, "verify copied row counts after migration")
	cutover := flags.Bool("cutover", false, "write database.postgres settings after a successful verified migration")
	allowPartial := flags.Bool("allow-partial-source", false, "allow missing source tables; intended for contract tests only")
	allowNonEmpty := flags.Bool("allow-nonempty-target", false, "allow copying into non-empty target tables")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*postgresDSN) == "" {
		if value := strings.TrimSpace(os.Getenv("NARRAFORK_DATABASE_URL")); value != "" {
			*postgresDSN = value
		} else if value := strings.TrimSpace(os.Getenv("NARRAFORK_POSTGRES_DSN")); value != "" {
			*postgresDSN = value
		}
	}
	if strings.TrimSpace(*sqlitePath) == "" {
		cfg, err := db.NewConfig(db.ConfigInput{})
		if err != nil {
			return err
		}
		*sqlitePath = cfg.SQLitePath
	}
	if err := validateSQLiteToPostgresCutoverFlags(*dryRun, *verify, *cutover); err != nil {
		return err
	}
	report, err := db.RunSQLiteToPostgres(context.Background(), db.SQLiteToPostgresOptions{
		SQLitePath:          *sqlitePath,
		PostgresDSN:         *postgresDSN,
		PostgresSchema:      *postgresSchema,
		DryRun:              *dryRun,
		Verify:              *verify,
		AllowPartialSource:  *allowPartial,
		AllowNonEmptyTarget: *allowNonEmpty,
	})
	if err != nil {
		return err
	}
	if *cutover {
		settings, err := config.Load()
		if err != nil {
			return err
		}
		if _, err := writeSQLiteToPostgresCutoverSettings(settings, *sqlitePath, *postgresDSN, *postgresSchema); err != nil {
			return err
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "\t")
	return encoder.Encode(report)
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		return runMigrate(os.Args[2:])
	}
	if err := restart.StartupHandoffFromEnv(context.Background(), buildinfo.VersionValue()); err != nil {
		return err
	}
	settings, err := config.Load()
	if err != nil {
		return err
	}
	if removed, err := misccompat.CleanupStaleShares(); err != nil {
		slog.Warn("stale share cleanup failed", "error", err)
	} else if removed > 0 {
		slog.Info("cleaned stale shares", "count", removed)
	}
	core := settings.Core()

	portFlag := flag.Int("port", 0, "HTTP listen port")
	hostFlag := flag.String("host", "", "HTTP listen host")
	flag.Parse()

	port := core.Server.Port
	if envPort := os.Getenv("PORT"); envPort != "" {
		if parsed, err := strconv.Atoi(envPort); err == nil {
			port = parsed
		}
	}
	if *portFlag != 0 {
		port = *portFlag
	}
	host := core.Server.Host
	if envHost := os.Getenv("HOST"); envHost != "" {
		host = envHost
	}
	if *hostFlag != "" {
		host = *hostFlag
	}
	if host == "" {
		host = "localhost"
	}
	tlsEnabled, certFile, keyFile := tlsListenConfig(core)
	protocol := "http"
	if tlsEnabled {
		protocol = "https"
	}

	ctx := context.Background()
	dbConfig, err := db.NewConfig(db.ConfigInput{
		Driver:                 core.Database.Driver,
		SQLitePath:             core.Database.SQLitePath,
		PostgresDSN:            core.Database.PostgresDSN,
		PostgresSchema:         core.Database.PostgresSchema,
		MaxOpenConns:           core.Database.MaxOpenConns,
		MaxIdleConns:           core.Database.MaxIdleConns,
		ConnMaxLifetimeSeconds: core.Database.ConnMaxLifetimeSeconds,
		VerifyCanonical:        db.Bool(core.Database.VerifyCanonical),
		TSOwnedSchema:          db.Bool(core.Database.TSOwnedSchema),
	})
	if err != nil {
		return err
	}
	dbStore, err := db.OpenWithConfig(ctx, dbConfig)
	if err != nil {
		return err
	}
	defer dbStore.Close()

	gitService := gitx.NewService()
	hub := ws.NewHub()
	hub.SetTerminalDB(dbStore.SQL)
	hub.SetMergeDB(dbStore.SQL)
	hub.SetBrowserSessionDB(dbStore.RuntimeSQL)
	terminalRuntime := terminalruntime.NewManager(dbStore.SQL, hub.BroadcastTerminal)
	if err := terminalRuntime.RecoverOnStartup(ctx); err != nil {
		return err
	}
	hub.SetTerminalRuntime(terminalRuntime)
	defer terminalRuntime.StopAll()
	authService := auth.NewService(dbStore.SQL, settings)
	notificationService := notifications.New(dbStore.SQL)
	notificationService.AttachToHub(hub)
	narratorService := narrators.NewService(dbStore.SQL, hub, settings)
	narratorService.SetRuntimeDB(dbStore.RuntimeSQL)
	narratorService.TerminalRuntime = terminalRuntime
	if err := narratorService.RecoverOnStartup(ctx); err != nil {
		return err
	}
	gatewayRuntime := gatewayruntime.New(dbStore.SQL, settings, narratorService, hub)
	if err := gatewayRuntime.Start(ctx); err != nil {
		slog.Warn("IM gateway startup failed", "error", err)
	}
	defer gatewayRuntime.Stop()
	chapterService := chapters.NewService(dbStore.SQL, gitService, narratorService, terminalRuntime, settings)
	chapterService.SetMergeProgressBroadcaster(hub.BroadcastMergeProgress)
	chapterService.SetChapterLifecycleBroadcasters(hub.BroadcastMergeProgress, hub.BroadcastUser)
	chapterService.SetNarratorBroadcaster(hub.BroadcastNarrator)
	if cleaned, err := chapterService.CleanupStaleMergeSessions(ctx); err != nil {
		slog.Warn("merge session cleanup failed", "error", err)
	} else if cleaned > 0 {
		slog.Info("cleaned up stale merge sessions", "count", cleaned)
	}
	projectService := projects.NewService(dbStore.SQL, gitService, chapterService, settings)
	projectService.SetUserBroadcaster(hub.BroadcastUser)
	containerProxy := containerproxy.New(dbStore.SQL, settings)
	projectService.SetProxyCacheRefresher(containerProxy.EnsureCurrent)
	if err := containerProxy.EnsureCurrent(ctx); err != nil {
		slog.Warn("container proxy startup failed", "error", err)
	}
	defer containerProxy.Stop(context.Background())
	watcherCtx, stopWatcher := context.WithCancel(context.Background())
	defer stopWatcher()
	go worktreewatcher.New(dbStore.SQL, gitService, hub).Run(watcherCtx)

	restartManager := restart.NewManager(restart.RuntimeAddress{Protocol: protocol, Host: host, Port: port}, nil)
	router := app.NewRouter(app.Services{
		Settings:        settings,
		DB:              dbStore.SQL,
		RuntimeDB:       dbStore.RuntimeSQL,
		DBEngine:        dbStore.Engine,
		Git:             gitService,
		Auth:            authService,
		Hub:             hub,
		Projects:        projectService,
		Chapters:        chapterService,
		Narrators:       narratorService,
		TerminalRuntime: terminalRuntime,
		Restart:         restartManager,
		Gateway:         gatewayRuntime,
		ContainerProxy:  containerProxy,
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	restartManager.SetShutdown(func(ctx context.Context, request restart.GracefulShutdownRequest) (restart.GracefulShutdownResult, error) {
		started := time.Now()
		err := server.Shutdown(ctx)
		return restart.GracefulShutdownResult{Success: err == nil, Reason: "replacement_started", PID: os.Getpid(), DurationMs: time.Since(started).Milliseconds()}, err
	})

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("NarraFork Go backend listening", "addr", server.Addr, "protocol", protocol, "db", dbStore.Path, "dbEngine", dbStore.Engine, "settings", settings.Path())
		if tlsEnabled {
			serverErr <- server.ListenAndServeTLS(certFile, keyFile)
			return
		}
		serverErr <- server.ListenAndServe()
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sig:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
