package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func TestSplitDrizzleStatements(t *testing.T) {
	sqlText := "  CREATE TABLE a (id text);  \n--> statement-breakpoint\n\n--> statement-breakpoint\nCREATE INDEX idx_a_id ON a(id);--> statement-breakpoint\n  "

	got := splitDrizzleStatements(sqlText)
	want := []string{
		"CREATE TABLE a (id text);",
		"CREATE INDEX idx_a_id ON a(id);",
	}
	if len(got) != len(want) {
		t.Fatalf("statement count = %d, want %d; got %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("statement[%d] = %q, want %q; got %#v", i, got[i], want[i], got)
		}
	}
}

func TestHashMigrationSQL(t *testing.T) {
	got := hashMigrationSQL("abc")
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Fatalf("hashMigrationSQL = %q, want %q", got, want)
	}

	withNewline := "CREATE TABLE t (id text);\n"
	sum := sha256.Sum256([]byte(withNewline))
	if got := hashMigrationSQL(withNewline); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("hashMigrationSQL should hash exact SQL bytes, got %q", got)
	}
}

func TestRunBunCompatibleDrizzleMigrationsIdempotent(t *testing.T) {
	sqlDB := openSQLiteMigrationTestDB(t)
	defer sqlDB.Close()
	migrations := []SQLiteDrizzleMigration{
		{
			Tag:  "0000_create_alpha",
			When: 101,
			SQL:  "CREATE TABLE alpha (id text PRIMARY KEY);",
		},
		{
			Tag:  "0001_add_beta",
			When: 102,
			SQL: strings.Join([]string{
				"CREATE TABLE beta (id text PRIMARY KEY, alpha_id text);",
				"CREATE INDEX idx_beta_alpha ON beta(alpha_id);",
			}, "--> statement-breakpoint"),
		},
	}

	ctx := context.Background()
	if err := RunBunCompatibleDrizzleMigrations(ctx, sqlDB, migrations); err != nil {
		t.Fatalf("first RunBunCompatibleDrizzleMigrations returned error: %v", err)
	}
	firstHashes := sqliteMigrationJournalHashes(t, sqlDB)
	if err := RunBunCompatibleDrizzleMigrations(ctx, sqlDB, migrations); err != nil {
		t.Fatalf("second RunBunCompatibleDrizzleMigrations returned error: %v", err)
	}
	secondHashes := sqliteMigrationJournalHashes(t, sqlDB)
	assertStringSlicesEqual(t, firstHashes, secondHashes)
	assertStringSlicesEqual(t, []string{hashMigrationSQL(migrations[0].SQL), hashMigrationSQL(migrations[1].SQL)}, secondHashes)

	assertSQLiteObjectExists(t, sqlDB, "alpha", "table")
	assertSQLiteObjectExists(t, sqlDB, "beta", "table")
	assertSQLiteObjectExists(t, sqlDB, "idx_beta_alpha", "index")

	var createdAt int64
	if err := sqlDB.QueryRow(`SELECT created_at FROM __drizzle_migrations WHERE hash = ?`, hashMigrationSQL(migrations[1].SQL)).Scan(&createdAt); err != nil {
		t.Fatalf("read created_at: %v", err)
	}
	if createdAt != migrations[1].When {
		t.Fatalf("created_at = %d, want %d", createdAt, migrations[1].When)
	}
}

func TestRunBunCompatibleDrizzleMigrationsAlreadyExistsStamp(t *testing.T) {
	sqlDB := openSQLiteMigrationTestDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`CREATE TABLE existing_table (id text PRIMARY KEY)`); err != nil {
		t.Fatalf("precreate existing_table: %v", err)
	}

	migration := SQLiteDrizzleMigration{
		Tag:  "0000_partial",
		When: 201,
		SQL: strings.Join([]string{
			"CREATE TABLE existing_table (id text PRIMARY KEY);",
			"CREATE TABLE created_after_already_exists (id text PRIMARY KEY);",
		}, "--> statement-breakpoint"),
	}
	if err := RunBunCompatibleDrizzleMigrations(context.Background(), sqlDB, []SQLiteDrizzleMigration{migration}); err != nil {
		t.Fatalf("RunBunCompatibleDrizzleMigrations returned error: %v", err)
	}

	assertSQLiteObjectExists(t, sqlDB, "created_after_already_exists", "table")
	assertStringSlicesEqual(t, []string{hashMigrationSQL(migration.SQL)}, sqliteMigrationJournalHashes(t, sqlDB))
}

func TestRunBunCompatibleDrizzleMigrationsNonCompatibleError(t *testing.T) {
	sqlDB := openSQLiteMigrationTestDB(t)
	defer sqlDB.Close()
	migration := SQLiteDrizzleMigration{
		Tag:  "0000_bad_insert",
		When: 301,
		SQL:  "INSERT INTO missing_table (id) VALUES ('x');",
	}

	err := RunBunCompatibleDrizzleMigrations(context.Background(), sqlDB, []SQLiteDrizzleMigration{migration})
	if err == nil {
		t.Fatal("expected non-compatible migration error")
	}
	if isSQLiteAlreadyAppliedError(err) {
		t.Fatalf("non-compatible error should not be classified as already applied: %v", err)
	}
	if !strings.Contains(err.Error(), "missing_table") {
		t.Fatalf("expected original SQLite error context, got %q", err.Error())
	}

	if hasSQLiteObject(t, sqlDB, drizzleMigrationsTable, "table") {
		if got := sqliteMigrationJournalHashes(t, sqlDB); len(got) != 0 {
			t.Fatalf("journal should not be stamped after non-compatible error, got %#v", got)
		}
	}
}

func TestSQLiteMigrationAlreadyAppliedErrorClassifier(t *testing.T) {
	cases := []error{
		fmt.Errorf("SQL logic error: table alpha already exists"),
		fmt.Errorf("wrapped: %w", fmt.Errorf("duplicate column name: title")),
	}
	for _, err := range cases {
		if !isSQLiteAlreadyAppliedError(err) {
			t.Fatalf("expected already-applied classification for %v", err)
		}
	}
	if isSQLiteAlreadyAppliedError(fmt.Errorf("no such table: missing_table")) {
		t.Fatal("unexpected already-applied classification for no such table")
	}
}

func openSQLiteMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	return sqlDB
}

func sqliteMigrationJournalHashes(t *testing.T, sqlDB *sql.DB) []string {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT hash FROM __drizzle_migrations ORDER BY created_at, hash`)
	if err != nil {
		t.Fatalf("read __drizzle_migrations: %v", err)
	}
	defer rows.Close()

	hashes := []string{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatalf("scan migration hash: %v", err)
		}
		hashes = append(hashes, hash)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration hashes: %v", err)
	}
	return hashes
}

func assertSQLiteObjectExists(t *testing.T, sqlDB *sql.DB, name string, objectType string) {
	t.Helper()
	if !hasSQLiteObject(t, sqlDB, name, objectType) {
		t.Fatalf("expected %s %s to exist", objectType, name)
	}
}

func hasSQLiteObject(t *testing.T, sqlDB *sql.DB, name string, objectType string) bool {
	t.Helper()
	var found string
	err := sqlDB.QueryRow(`SELECT name FROM sqlite_master WHERE name = ? AND type = ?`, name, objectType).Scan(&found)
	if err != nil {
		if err == sql.ErrNoRows {
			return false
		}
		t.Fatalf("query sqlite_master for %s %s: %v", objectType, name, err)
	}
	return true
}
