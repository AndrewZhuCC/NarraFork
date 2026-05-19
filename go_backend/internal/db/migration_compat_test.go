package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"narrafork-go/internal/compat/bunfinal"
)

func TestEnsureMigrationCompatibilityCreatesOnlyGoJournalAfterCanonicalSchemaReady(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)
	createDrizzleMigrationsTable(t, sqlDB, embeddedDrizzleMigrationSnapshot, nil)

	projectColumnsBefore := tableColumnNamesForTest(t, sqlDB, "projects")
	schemaObjectsBefore := schemaObjectSet(t, sqlDB)
	if err := VerifyCanonicalSchema(context.Background(), sqlDB); err != nil {
		t.Fatalf("canonical schema fixture should be ready: %v", err)
	}
	if err := EnsureMigrationCompatibility(context.Background(), sqlDB); err != nil {
		t.Fatalf("EnsureMigrationCompatibility returned error: %v", err)
	}

	schemaObjectsAfter := schemaObjectSet(t, sqlDB)
	newObjects := []string{}
	for name := range schemaObjectsAfter {
		if _, existed := schemaObjectsBefore[name]; !existed {
			newObjects = append(newObjects, name)
		}
	}
	sort.Strings(newObjects)
	if strings.Join(newObjects, ",") != goMigrationJournalTable {
		t.Fatalf("expected only %s to be created, got %#v", goMigrationJournalTable, newObjects)
	}
	assertStringSlicesEqual(t, projectColumnsBefore, tableColumnNamesForTest(t, sqlDB, "projects"))

	journal := readGoMigrationJournalForTest(t, sqlDB)
	last := embeddedDrizzleMigrationSnapshot[len(embeddedDrizzleMigrationSnapshot)-1]
	if journal.ExpectedCount != len(embeddedDrizzleMigrationSnapshot) {
		t.Fatalf("expected_count = %d, want %d", journal.ExpectedCount, len(embeddedDrizzleMigrationSnapshot))
	}
	if journal.ExpectedLastTag != last.Tag || journal.ExpectedLastWhen != last.When {
		t.Fatalf("last migration = %s/%d, want %s/%d", journal.ExpectedLastTag, journal.ExpectedLastWhen, last.Tag, last.When)
	}
	if journal.ExpectedJournalHash != expectedDrizzleJournalHash() {
		t.Fatalf("expected_journal_hash = %q, want %q", journal.ExpectedJournalHash, expectedDrizzleJournalHash())
	}
	if journal.CanonicalSchemaHash != canonicalSchemaContractHash() {
		t.Fatalf("canonical_schema_hash = %q, want %q", journal.CanonicalSchemaHash, canonicalSchemaContractHash())
	}
	if journal.Status != "compatible" {
		t.Fatalf("status = %q, want compatible", journal.Status)
	}
	var details migrationCompatibilityDetails
	if err := json.Unmarshal([]byte(journal.DetailsJSON), &details); err != nil {
		t.Fatalf("unmarshal details_json: %v", err)
	}
	if len(details.ExtraHashes) != 0 {
		t.Fatalf("expected no extra hashes, got %#v", details.ExtraHashes)
	}
}

func TestEnsureMigrationCompatibilityRejectsMissingDrizzleJournal(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)

	err := EnsureMigrationCompatibility(context.Background(), sqlDB)
	if !errors.Is(err, ErrMigrationCompatibilityMismatch) {
		t.Fatalf("expected ErrMigrationCompatibilityMismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "missing __drizzle_migrations") {
		t.Fatalf("expected missing journal guidance, got %q", err.Error())
	}
	assertTableMissing(t, sqlDB, goMigrationJournalTable)
}

func TestEnsureMigrationCompatibilityRejectsMissingExpectedMigrationHash(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)
	entries := embeddedDrizzleMigrationSnapshot[:len(embeddedDrizzleMigrationSnapshot)-1]
	createDrizzleMigrationsTable(t, sqlDB, entries, nil)

	err := EnsureMigrationCompatibility(context.Background(), sqlDB)
	if !errors.Is(err, ErrMigrationCompatibilityMismatch) {
		t.Fatalf("expected ErrMigrationCompatibilityMismatch, got %v", err)
	}
	last := embeddedDrizzleMigrationSnapshot[len(embeddedDrizzleMigrationSnapshot)-1]
	if !strings.Contains(err.Error(), last.Tag) || !strings.Contains(err.Error(), last.Hash) {
		t.Fatalf("expected missing last migration in error, got %q", err.Error())
	}
	assertTableMissing(t, sqlDB, goMigrationJournalTable)
}

func TestEnsureMigrationCompatibilityRejectsMalformedDrizzleJournal(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)
	if _, err := sqlDB.Exec(`CREATE TABLE __drizzle_migrations (id INTEGER PRIMARY KEY, created_at numeric)`); err != nil {
		t.Fatalf("create malformed __drizzle_migrations: %v", err)
	}

	err := EnsureMigrationCompatibility(context.Background(), sqlDB)
	if !errors.Is(err, ErrMigrationCompatibilityMismatch) {
		t.Fatalf("expected ErrMigrationCompatibilityMismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "hash column is missing") {
		t.Fatalf("expected missing hash column error, got %q", err.Error())
	}
	assertTableMissing(t, sqlDB, goMigrationJournalTable)
}

func TestEnsureMigrationCompatibilityIsIdempotent(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)
	createDrizzleMigrationsTable(t, sqlDB, embeddedDrizzleMigrationSnapshot, nil)

	if err := EnsureMigrationCompatibility(context.Background(), sqlDB); err != nil {
		t.Fatalf("first EnsureMigrationCompatibility returned error: %v", err)
	}
	first := readGoMigrationJournalForTest(t, sqlDB)
	if err := EnsureMigrationCompatibility(context.Background(), sqlDB); err != nil {
		t.Fatalf("second EnsureMigrationCompatibility returned error: %v", err)
	}
	second := readGoMigrationJournalForTest(t, sqlDB)
	if first.ExpectedJournalHash != second.ExpectedJournalHash || first.CanonicalSchemaHash != second.CanonicalSchemaHash {
		t.Fatalf("journal hashes changed across idempotent run: first=%#v second=%#v", first, second)
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM __narrafork_go_migration_journal`).Scan(&count); err != nil {
		t.Fatalf("count Go journal rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("Go journal row count = %d, want 1", count)
	}
}

func TestEnsureMigrationCompatibilityRecordsExtraUnknownHashesWithoutBlocking(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)
	extraHashes := []string{
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	createDrizzleMigrationsTable(t, sqlDB, embeddedDrizzleMigrationSnapshot, extraHashes)

	if err := EnsureMigrationCompatibility(context.Background(), sqlDB); err != nil {
		t.Fatalf("extra hashes should be recorded, not blocked: %v", err)
	}
	journal := readGoMigrationJournalForTest(t, sqlDB)
	var details migrationCompatibilityDetails
	if err := json.Unmarshal([]byte(journal.DetailsJSON), &details); err != nil {
		t.Fatalf("unmarshal details_json: %v", err)
	}
	sort.Strings(extraHashes)
	assertStringSlicesEqual(t, extraHashes, details.ExtraHashes)
}

func TestCanonicalSchemaHashIsStableAndDoesNotDependOnJournalTable(t *testing.T) {
	before := canonicalSchemaContractHash()
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)
	createDrizzleMigrationsTable(t, sqlDB, embeddedDrizzleMigrationSnapshot, nil)
	if err := EnsureMigrationCompatibility(context.Background(), sqlDB); err != nil {
		t.Fatalf("EnsureMigrationCompatibility returned error: %v", err)
	}
	after := canonicalSchemaContractHash()
	if before != after {
		t.Fatalf("canonical schema hash changed after journal write: before=%s after=%s", before, after)
	}
}

func TestEmbeddedDrizzleJournalSnapshotMatchesRepository(t *testing.T) {
	root := repositoryRootWithDrizzle(t)
	journalPath := filepath.Join(root, "drizzle", "meta", "_journal.json")
	journalBytes, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("read %s: %v", journalPath, err)
	}
	var journal struct {
		Entries []struct {
			Tag  string `json:"tag"`
			When int64  `json:"when"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(journalBytes, &journal); err != nil {
		t.Fatalf("parse Drizzle journal: %v", err)
	}
	if len(journal.Entries) != len(embeddedDrizzleMigrationSnapshot) {
		t.Fatalf("snapshot has %d entries, repository journal has %d", len(embeddedDrizzleMigrationSnapshot), len(journal.Entries))
	}
	for i, entry := range journal.Entries {
		sqlPath := filepath.Join(root, "drizzle", entry.Tag+".sql")
		sqlBytes, err := os.ReadFile(sqlPath)
		if err != nil {
			t.Fatalf("read %s: %v", sqlPath, err)
		}
		sum := sha256.Sum256(sqlBytes)
		want := drizzleMigrationSnapshotEntry{
			Tag:  entry.Tag,
			When: entry.When,
			Hash: hex.EncodeToString(sum[:]),
		}
		if embeddedDrizzleMigrationSnapshot[i] != want {
			t.Fatalf("snapshot[%d] = %#v, want %#v", i, embeddedDrizzleMigrationSnapshot[i], want)
		}
	}
}

func TestBunFinalDrizzleJournalArtifactMatchesEmbeddedSnapshot(t *testing.T) {
	data, err := bunfinal.ReadDrizzleJournalSnapshot()
	if err != nil {
		t.Fatalf("read bun-final Drizzle journal artifact: %v", err)
	}
	var artifact struct {
		LastTag                string `json:"lastTag"`
		ExpectedMigrationCount int    `json:"expectedMigrationCount"`
		ExpectedJournalHash    string `json:"expectedJournalHash"`
		Entries                []struct {
			Idx  int    `json:"idx"`
			Tag  string `json:"tag"`
			When int64  `json:"when"`
			Hash string `json:"hash"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("parse bun-final Drizzle journal artifact: %v", err)
	}
	if artifact.ExpectedMigrationCount != len(embeddedDrizzleMigrationSnapshot) || len(artifact.Entries) != len(embeddedDrizzleMigrationSnapshot) {
		t.Fatalf("artifact migration count = %d/%d, want %d", artifact.ExpectedMigrationCount, len(artifact.Entries), len(embeddedDrizzleMigrationSnapshot))
	}
	if artifact.ExpectedJournalHash != expectedDrizzleJournalHash() {
		t.Fatalf("artifact expectedJournalHash = %q, want %q", artifact.ExpectedJournalHash, expectedDrizzleJournalHash())
	}
	last := embeddedDrizzleMigrationSnapshot[len(embeddedDrizzleMigrationSnapshot)-1]
	if artifact.LastTag != last.Tag {
		t.Fatalf("artifact lastTag = %q, want %q", artifact.LastTag, last.Tag)
	}
	for i, entry := range artifact.Entries {
		want := embeddedDrizzleMigrationSnapshot[i]
		if entry.Idx != i || entry.Tag != want.Tag || entry.When != want.When || entry.Hash != want.Hash {
			t.Fatalf("artifact entry[%d] = %#v, want idx=%d snapshot=%#v", i, entry, i, want)
		}
	}
}

type goMigrationJournalRow struct {
	ExpectedCount       int
	ExpectedLastTag     string
	ExpectedLastWhen    int64
	ExpectedJournalHash string
	CanonicalSchemaHash string
	Status              string
	DetailsJSON         string
}

func createDrizzleMigrationsTable(
	t *testing.T,
	sqlDB *sql.DB,
	entries []drizzleMigrationSnapshotEntry,
	extraHashes []string,
) {
	t.Helper()
	if _, err := sqlDB.Exec(`CREATE TABLE __drizzle_migrations (
		id INTEGER PRIMARY KEY,
		hash text NOT NULL,
		created_at numeric
	)`); err != nil {
		t.Fatalf("create __drizzle_migrations: %v", err)
	}
	id := 1
	for _, entry := range entries {
		if _, err := sqlDB.Exec(
			`INSERT INTO __drizzle_migrations (id, hash, created_at) VALUES (?, ?, ?)`,
			id,
			entry.Hash,
			entry.When,
		); err != nil {
			t.Fatalf("insert migration %s: %v", entry.Tag, err)
		}
		id++
	}
	for _, hash := range extraHashes {
		if _, err := sqlDB.Exec(
			`INSERT INTO __drizzle_migrations (id, hash, created_at) VALUES (?, ?, ?)`,
			id,
			hash,
			int64(0),
		); err != nil {
			t.Fatalf("insert extra migration hash: %v", err)
		}
		id++
	}
}

func readGoMigrationJournalForTest(t *testing.T, sqlDB *sql.DB) goMigrationJournalRow {
	t.Helper()
	var row goMigrationJournalRow
	if err := sqlDB.QueryRow(`SELECT
		expected_count,
		expected_last_tag,
		expected_last_when,
		expected_journal_hash,
		canonical_schema_hash,
		status,
		details_json
	FROM __narrafork_go_migration_journal WHERE id = 1`).Scan(
		&row.ExpectedCount,
		&row.ExpectedLastTag,
		&row.ExpectedLastWhen,
		&row.ExpectedJournalHash,
		&row.CanonicalSchemaHash,
		&row.Status,
		&row.DetailsJSON,
	); err != nil {
		t.Fatalf("read Go migration journal: %v", err)
	}
	return row
}

func tableColumnNamesForTest(t *testing.T, sqlDB *sql.DB, table string) []string {
	t.Helper()
	exists, columns, err := tableColumns(context.Background(), sqlDB, table)
	if err != nil {
		t.Fatalf("inspect %s: %v", table, err)
	}
	if !exists {
		t.Fatalf("table %s does not exist", table)
	}
	names := make([]string, 0, len(columns))
	for name := range columns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func schemaObjectSet(t *testing.T, sqlDB *sql.DB) map[string]struct{} {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT name FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list schema objects: %v", err)
	}
	defer rows.Close()
	objects := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan schema object: %v", err)
		}
		objects[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema objects: %v", err)
	}
	return objects
}

func assertTableMissing(t *testing.T, sqlDB *sql.DB, table string) {
	t.Helper()
	exists, _, err := tableColumns(context.Background(), sqlDB, table)
	if err != nil {
		t.Fatalf("inspect %s: %v", table, err)
	}
	if exists {
		t.Fatalf("expected table %s to be absent", table)
	}
}

func assertStringSlicesEqual(t *testing.T, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("slice length = %d, want %d; got %#v want %#v", len(got), len(want), got, want)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("slice[%d] = %q, want %q; got %#v want %#v", i, got[i], want[i], got, want)
		}
	}
}

func repositoryRootWithDrizzle(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		journalPath := filepath.Join(cwd, "drizzle", "meta", "_journal.json")
		if _, err := os.Stat(journalPath); err == nil {
			return cwd
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			break
		}
		cwd = parent
	}
	t.Skip("repository Drizzle journal is not available")
	return ""
}
