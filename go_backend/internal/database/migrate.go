package database

import (
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrationsFS embed.FS

//go:embed migrations/postgres/*.sql
var postgresMigrationsFS embed.FS

// Migrate runs all pending SQL migration files for the current dialect.
// Migrations are ordered by filename (0001_xxx.sql, 0002_xxx.sql, ...).
// Already-applied migrations are tracked in the `_migrations` table.
func Migrate(db *DB) error {
	// Create migrations tracking table (dialect-compatible DDL)
	createSQL := `
		CREATE TABLE IF NOT EXISTS _migrations (
			id      INTEGER PRIMARY KEY,
			name    TEXT NOT NULL UNIQUE,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`
	if db.Dialect.Kind() == DialectPostgres {
		createSQL = `
		CREATE TABLE IF NOT EXISTS _migrations (
			id      SERIAL PRIMARY KEY,
			name    TEXT NOT NULL UNIQUE,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`
	}

	if _, err := db.Exec(createSQL); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	// Select the right embedded FS and directory
	var fs embed.FS
	var dir string
	switch db.Dialect.Kind() {
	case DialectPostgres:
		fs = postgresMigrationsFS
		dir = "migrations/postgres"
	default:
		fs = sqliteMigrationsFS
		dir = "migrations/sqlite"
	}

	// Read migration files
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %s: %w", dir, err)
	}

	// Sort by name
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	// Get already-applied migrations
	applied, err := getAppliedMigrations(db)
	if err != nil {
		return err
	}

	// Run pending migrations
	count := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		if applied[name] {
			continue
		}

		content, err := fs.ReadFile(dir + "/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		if err := runMigration(db, name, string(content)); err != nil {
			return fmt.Errorf("migration %s failed: %w", name, err)
		}

		count++
		slog.Info("migration applied", "name", name, "dialect", string(db.Dialect.Kind()))
	}

	if count == 0 {
		slog.Debug("all migrations up to date")
	}

	return nil
}

func getAppliedMigrations(db *DB) (map[string]bool, error) {
	// Check if table exists first (for brand new databases)
	rows, err := db.Query("SELECT name FROM _migrations")
	if err != nil {
		// Table might not exist yet on very first run before CREATE TABLE above
		return make(map[string]bool), nil
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		applied[name] = true
	}
	return applied, rows.Err()
}

func runMigration(db *DB, name, content string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	// Execute migration SQL (may contain multiple statements)
	if _, execErr := tx.Exec(content); execErr != nil {
		tx.Rollback()
		return execErr
	}

	// Record migration
	var insertSQL string
	switch db.Dialect.Kind() {
	case DialectPostgres:
		insertSQL = "INSERT INTO _migrations (name) VALUES ($1)"
	default:
		insertSQL = "INSERT INTO _migrations (name) VALUES (?)"
	}
	if _, execErr := tx.Exec(insertSQL, name); execErr != nil {
		tx.Rollback()
		return execErr
	}

	return tx.Commit()
}

// MigrationStatus returns info about applied and pending migrations.
type MigrationStatus struct {
	Dialect DialectKind
	Applied []string
	Pending []string
}

// Status returns which migrations have been applied and which are pending.
func Status(db *DB) (*MigrationStatus, error) {
	var fs embed.FS
	var dir string
	switch db.Dialect.Kind() {
	case DialectPostgres:
		fs = postgresMigrationsFS
		dir = "migrations/postgres"
	default:
		fs = sqliteMigrationsFS
		dir = "migrations/sqlite"
	}

	entries, err := fs.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	applied, err := getAppliedMigrations(db)
	if err != nil {
		return nil, err
	}

	var appliedList, pending []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		if applied[name] {
			appliedList = append(appliedList, name)
		} else {
			pending = append(pending, name)
		}
	}

	return &MigrationStatus{
		Dialect: db.Dialect.Kind(),
		Applied: appliedList,
		Pending: pending,
	}, nil
}

// StampExistingDatabase marks all migrations as applied without running them.
// Use when the Go backend opens a database already managed by the TS backend.
func StampExistingDatabase(db *DB) error {
	var fs embed.FS
	var dir string
	switch db.Dialect.Kind() {
	case DialectPostgres:
		fs = postgresMigrationsFS
		dir = "migrations/postgres"
	default:
		fs = sqliteMigrationsFS
		dir = "migrations/sqlite"
	}

	entries, err := fs.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		var insertSQL string
		switch db.Dialect.Kind() {
		case DialectPostgres:
			insertSQL = "INSERT INTO _migrations (name) VALUES ($1) ON CONFLICT DO NOTHING"
		default:
			insertSQL = "INSERT OR IGNORE INTO _migrations (name) VALUES (?)"
		}
		if _, err := db.Exec(insertSQL, name); err != nil {
			return fmt.Errorf("stamp migration %s: %w", name, err)
		}
	}

	slog.Info("existing database stamped", "dialect", string(db.Dialect.Kind()))
	return nil
}
