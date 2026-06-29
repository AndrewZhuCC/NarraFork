package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/narrafork/narrafork/internal/buildinfo"
	"github.com/narrafork/narrafork/internal/config"
	"github.com/narrafork/narrafork/internal/database"
	"github.com/narrafork/narrafork/internal/logging"
)

// shutdownTimeout bounds how long graceful shutdown of background services may
// take once a termination signal is received.
const shutdownTimeout = 30 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// Initialize logger
	logger := logging.NewLogger(os.Stderr, slog.LevelInfo)
	slog.SetDefault(logger)

	// Setup root context with signal cancellation
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Open database (routes to SQLite or PostgreSQL based on DSN)
	db, err := database.Open(cfg.DatabaseDSN())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// Run migrations
	if err := database.Migrate(db); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// For SQLite: FTS health check after migrations
	if db.Dialect.Kind() == database.DialectSQLite {
		database.EnsureFTSHealth(db)
	}

	slog.Info("NarraFork started",
		"version", buildinfo.Version,
		"dialect", string(db.Dialect.Kind()),
		"dsn", cfg.DatabaseDSN(),
	)

	// Wait for shutdown signal
	<-ctx.Done()
	fmt.Println() // newline after ^C
	slog.Info("shutting down")

	// Bound graceful shutdown of background services with a timeout. There are
	// no long-running services to drain yet; shutdownCtx is the seam they will
	// hang off (e.g. httpServer.Shutdown(shutdownCtx)) as they are added.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	_ = shutdownCtx

	// Graceful shutdown: mark clean for SQLite FTS
	if db.Dialect.Kind() == database.DialectSQLite {
		database.MarkCleanShutdown(db)
	}

	return nil
}
