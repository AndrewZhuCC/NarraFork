package main

import (
	"context"
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
	"narrafork-go/internal/chapters"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/narrators"
	"narrafork-go/internal/notifications"
	"narrafork-go/internal/projects"
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

func run() error {
	settings, err := config.Load()
	if err != nil {
		return err
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

	ctx := context.Background()
	dbStore, err := db.Open(ctx)
	if err != nil {
		return err
	}
	defer dbStore.Close()

	gitService := gitx.NewService()
	hub := ws.NewHub()
	hub.SetTerminalDB(dbStore.SQL)
	hub.SetMergeDB(dbStore.SQL)
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
	chapterService := chapters.NewService(dbStore.SQL, gitService, narratorService, terminalRuntime, settings)
	chapterService.SetMergeProgressBroadcaster(hub.BroadcastMergeProgress)
	projectService := projects.NewService(dbStore.SQL, gitService, chapterService, settings)
	watcherCtx, stopWatcher := context.WithCancel(context.Background())
	defer stopWatcher()
	go worktreewatcher.New(dbStore.SQL, gitService, hub).Run(watcherCtx)

	router := app.NewRouter(app.Services{
		Settings:        settings,
		DB:              dbStore.SQL,
		Git:             gitService,
		Auth:            authService,
		Hub:             hub,
		Projects:        projectService,
		Chapters:        chapterService,
		Narrators:       narratorService,
		TerminalRuntime: terminalRuntime,
	})

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	tlsEnabled, certFile, keyFile := tlsListenConfig(core)
	serverErr := make(chan error, 1)
	go func() {
		protocol := "http"
		if tlsEnabled {
			protocol = "https"
		}
		slog.Info("NarraFork Go backend listening", "addr", server.Addr, "protocol", protocol, "db", dbStore.Path, "settings", settings.Path())
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
