package db

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"testing"
)

func TestGoFirstUsePreservesBunRollbackSchemaCompatibility(t *testing.T) {
	sqlDB := openCanonicalSchemaTestDB(t)
	defer sqlDB.Close()
	createCanonicalSchemaTables(t, sqlDB)
	createDrizzleMigrationsTable(t, sqlDB, embeddedDrizzleMigrationSnapshot, nil)

	beforeSnapshot, err := SnapshotSQLiteSchema(context.Background(), sqlDB, DefaultSchemaSnapshotIgnoredObjects())
	if err != nil {
		t.Fatalf("snapshot before Go first use: %v", err)
	}
	beforeDrizzleHashes := drizzleMigrationHashSet(t, sqlDB)
	beforeDrizzleColumns := tableColumnNamesForTest(t, sqlDB, "__drizzle_migrations")

	if err := VerifyCanonicalSchema(context.Background(), sqlDB); err != nil {
		t.Fatalf("canonical schema should pass before Go first use: %v", err)
	}
	if err := EnsureMigrationCompatibility(context.Background(), sqlDB); err != nil {
		t.Fatalf("Go first-use compatibility check failed: %v", err)
	}

	afterSnapshotIgnoringGoJournal, err := SnapshotSQLiteSchema(context.Background(), sqlDB, DefaultSchemaSnapshotIgnoredObjects())
	if err != nil {
		t.Fatalf("snapshot after Go first use: %v", err)
	}
	if beforeSnapshot.Hash != afterSnapshotIgnoringGoJournal.Hash {
		t.Fatalf("schema changed after Go first use when ignoring Go journal: before=%s after=%s", beforeSnapshot.Hash, afterSnapshotIgnoringGoJournal.Hash)
	}

	afterSnapshotWithGoJournal, err := SnapshotSQLiteSchema(context.Background(), sqlDB, map[string]struct{}{})
	if err != nil {
		t.Fatalf("snapshot after Go first use with Go journal: %v", err)
	}
	newObjects := snapshotObjectNames(afterSnapshotWithGoJournal)
	for _, object := range beforeSnapshot.Objects {
		delete(newObjects, object.Type+":"+object.Name)
	}
	newObjectKeys := sortedMapKeys(newObjects)
	if strings.Join(newObjectKeys, ",") != "table:"+goMigrationJournalTable {
		t.Fatalf("Go first use should only add %s, got %#v", goMigrationJournalTable, newObjectKeys)
	}

	afterDrizzleHashes := drizzleMigrationHashSet(t, sqlDB)
	if !sameStringSet(beforeDrizzleHashes, afterDrizzleHashes) {
		t.Fatalf("__drizzle_migrations hash set changed after Go first use")
	}
	assertStringSlicesEqual(t, beforeDrizzleColumns, tableColumnNamesForTest(t, sqlDB, "__drizzle_migrations"))
	if err := verifyDrizzleMigrationTableShapeForBunRollback(context.Background(), sqlDB); err != nil {
		t.Fatalf("Bun rollback Drizzle journal compatibility failed: %v", err)
	}
}

func verifyDrizzleMigrationTableShapeForBunRollback(ctx context.Context, sqlDB *sql.DB) error {
	exists, columns, err := tableColumns(ctx, sqlDB, "__drizzle_migrations")
	if err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	for _, column := range []string{"hash", "created_at"} {
		if _, ok := columns[column]; !ok {
			return sql.ErrNoRows
		}
	}
	rows, err := sqlDB.QueryContext(ctx, `SELECT hash, created_at FROM __drizzle_migrations LIMIT 1`)
	if err != nil {
		return err
	}
	return rows.Close()
}

func drizzleMigrationHashSet(t *testing.T, sqlDB *sql.DB) map[string]struct{} {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT hash FROM __drizzle_migrations`)
	if err != nil {
		t.Fatalf("read __drizzle_migrations hashes: %v", err)
	}
	defer rows.Close()
	hashes := map[string]struct{}{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatalf("scan migration hash: %v", err)
		}
		hashes[hash] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration hashes: %v", err)
	}
	return hashes
}

func snapshotObjectNames(snapshot SQLiteSchemaSnapshot) map[string]struct{} {
	objects := map[string]struct{}{}
	for _, object := range snapshot.Objects {
		objects[object.Type+":"+object.Name] = struct{}{}
	}
	return objects
}

func sortedMapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sameStringSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if _, ok := b[key]; !ok {
			return false
		}
	}
	return true
}
