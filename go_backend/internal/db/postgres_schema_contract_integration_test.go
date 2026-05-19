package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresBusinessSchemaDDLContract(t *testing.T) {
	ddl := PostgresBusinessSchemaDDL()
	if len(ddl) <= len(PostgresSchemaContract().BusinessTables) {
		t.Fatalf("business schema DDL should include tables plus indexes/FKs, got %d statements", len(ddl))
	}
	joined := strings.ToLower(strings.Join(ddl, "\n"))
	for _, fts := range canonicalFTSObjects {
		if strings.Contains(joined, strings.ToLower(fts.Table)) {
			t.Fatalf("business schema DDL should not include SQLite FTS table %s", fts.Table)
		}
	}
	if !strings.Contains(joined, "deferrable initially deferred") {
		t.Fatalf("business schema DDL should create deferrable FK constraints: %s", joined)
	}
	for _, rule := range []string{"on delete cascade", "on delete set null", "on delete no action"} {
		if !strings.Contains(joined, rule) {
			t.Fatalf("business schema DDL should preserve FK %s rules: %s", rule, joined)
		}
	}
	if strings.Contains(joined, "__drizzle_migrations") {
		t.Fatalf("business schema DDL must not use Drizzle migration journal: %s", joined)
	}
}

func TestPostgresMigrationJournalDDLContract(t *testing.T) {
	ddl := PostgresMigrationJournalDDL()
	if len(ddl) != 2 {
		t.Fatalf("journal DDL count = %d, want 2", len(ddl))
	}
	joined := strings.ToLower(strings.Join(ddl, "\n"))
	for _, table := range []string{postgresSchemaJournalTable, postgresMigrationRunsTable} {
		if !strings.Contains(joined, table) {
			t.Fatalf("journal DDL should mention %s: %s", table, joined)
		}
	}
	if strings.Contains(joined, "__drizzle_migrations") || strings.Contains(joined, "sqlite") {
		t.Fatalf("Postgres journal DDL must not replay or depend on SQLite/Drizzle state: %s", joined)
	}
}

func TestPostgresMigrationJournalDDLAgainstLivePostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("NARRAFORK_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set NARRAFORK_TEST_POSTGRES_DSN to run live Postgres contract test")
	}
	if !sqlDriverRegistered((PostgresDialect{}).DriverName()) {
		t.Fatalf("postgres driver %q is not registered", (PostgresDialect{}).DriverName())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sqlDB, err := sql.Open((PostgresDialect{}).DriverName(), dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer sqlDB.Close()
	if err := sqlDB.PingContext(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	schema := "narrafork_contract_" + strings.ReplaceAll(fmt.Sprintf("%d", time.Now().UnixNano()), "-", "")
	quotedSchema := (PostgresDialect{}).QuoteIdent(schema)
	if _, err := sqlDB.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create temp schema: %v", err)
	}
	defer sqlDB.ExecContext(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	if _, err := sqlDB.ExecContext(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	for _, stmt := range PostgresBusinessSchemaDDL() {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("exec business schema DDL: %v\n%s", err, stmt)
		}
	}
	for _, stmt := range PostgresMigrationJournalDDL() {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("exec journal DDL: %v\n%s", err, stmt)
		}
	}
	for _, table := range []string{postgresSchemaJournalTable, postgresMigrationRunsTable, "projects", "chapters", "narrators", "narrator_messages"} {
		var exists bool
		if err := sqlDB.QueryRowContext(ctx, `SELECT EXISTS (
	SELECT 1 FROM information_schema.tables
	WHERE table_schema = $1 AND table_name = $2
)`, schema, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("expected journal table %s to exist", table)
		}
	}
}
