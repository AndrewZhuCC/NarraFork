package db

import (
	"encoding/json"
	"testing"

	"narrafork-go/internal/compat/bunfinal"
)

func TestSavedSchemaSnapshotManifestIsValid(t *testing.T) {
	manifest, err := bunfinal.ReadManifest()
	if err != nil {
		t.Fatalf("read bun-final manifest: %v", err)
	}
	if err := bunfinal.ValidateManifest(manifest); err != nil {
		t.Fatalf("validate bun-final manifest: %v", err)
	}
	if manifest.Source != "bun-final" {
		t.Fatalf("manifest source = %q, want bun-final", manifest.Source)
	}
}

func TestSavedSchemaSnapshotHashMatchesFile(t *testing.T) {
	snapshot := loadSavedSchemaSnapshot(t)
	wantHash := readSnapshotHashFile(t)
	if snapshot.Hash != wantHash {
		t.Fatalf("snapshot hash field = %s, want %s", snapshot.Hash, wantHash)
	}
	if computed := HashSQLiteSchemaSnapshot(snapshot); computed != wantHash {
		t.Fatalf("computed snapshot hash = %s, want %s", computed, wantHash)
	}
}

func TestCanonicalSchemaContractCoveredBySavedSnapshot(t *testing.T) {
	snapshot := loadSavedSchemaSnapshot(t)
	tables := savedSnapshotTables(snapshot)
	indexes := savedSnapshotIndexes(snapshot)
	foreignKeys := savedSnapshotForeignKeys(snapshot)
	objects := savedSnapshotObjects(snapshot)

	for table, expectedColumns := range canonicalMainSchema {
		actualColumns, ok := tables[table]
		if !ok {
			t.Fatalf("saved snapshot is missing canonical table %s", table)
		}
		for _, column := range expectedColumns {
			if _, ok := actualColumns[column]; !ok {
				t.Fatalf("saved snapshot is missing canonical column %s.%s", table, column)
			}
		}
	}
	for _, expected := range canonicalMainIndexes {
		actual, ok := indexes[expected.Name]
		if !ok {
			t.Fatalf("saved snapshot is missing canonical index %s", expected.Name)
		}
		if actual.Table != expected.Table {
			t.Fatalf("index %s table = %s, want %s", expected.Name, actual.Table, expected.Table)
		}
		if expected.Unique && !actual.Unique {
			t.Fatalf("index %s is not unique in saved snapshot", expected.Name)
		}
		assertStringSlicesEqual(t, expected.Columns, actual.Columns)
	}
	for _, expected := range canonicalForeignKeys {
		key := expected.Table + "." + expected.Column + "->" + expected.ReferencedTable + "." + expected.ReferencedColumn
		if _, ok := foreignKeys[key]; !ok {
			t.Fatalf("saved snapshot is missing canonical FK %s", key)
		}
	}
	for _, expected := range canonicalFTSObjects {
		object, ok := objects["table:"+expected.Table]
		if !ok {
			t.Fatalf("saved snapshot is missing canonical FTS table %s", expected.Table)
		}
		if !containsAllSQLFragments(object.SQL, expected.Fragments) {
			t.Fatalf("FTS table %s SQL does not contain expected fragments: %s", expected.Table, object.SQL)
		}
	}
	for _, expected := range canonicalFTSTriggers {
		object, ok := objects["trigger:"+expected.Name]
		if !ok {
			t.Fatalf("saved snapshot is missing canonical FTS trigger %s", expected.Name)
		}
		if object.Table != expected.Table {
			t.Fatalf("trigger %s table = %s, want %s", expected.Name, object.Table, expected.Table)
		}
		if !containsAllSQLFragments(object.SQL, expected.Fragments) {
			t.Fatalf("FTS trigger %s SQL does not contain expected fragments: %s", expected.Name, object.SQL)
		}
	}
}

func TestSavedSchemaSnapshotFreezesCanonicalColumnMetadata(t *testing.T) {
	snapshot := loadSavedSchemaSnapshot(t)
	columns := savedSnapshotColumnMetadata(snapshot)
	for _, expected := range canonicalColumnMetadata {
		actual, ok := columns[expected.Table+"."+expected.Column]
		if !ok {
			t.Fatalf("saved snapshot is missing canonical metadata column %s.%s", expected.Table, expected.Column)
		}
		columnName := expected.Table + "." + expected.Column
		if expected.Type != "" && actual.Type != expected.Type {
			t.Fatalf("saved snapshot column %s type = %q, want %q", columnName, actual.Type, expected.Type)
		}
		if expected.NotNull != nil && actual.NotNull != *expected.NotNull {
			t.Fatalf("saved snapshot column %s notNull = %t, want %t", columnName, actual.NotNull, *expected.NotNull)
		}
		if expected.DefaultValue != nil {
			actualDefault := "<none>"
			if actual.DefaultValue != nil {
				actualDefault = *actual.DefaultValue
			}
			if actualDefault != *expected.DefaultValue {
				t.Fatalf("saved snapshot column %s default = %q, want %q", columnName, actualDefault, *expected.DefaultValue)
			}
		}
		if expected.PrimaryKey != nil && actual.PK != *expected.PrimaryKey {
			t.Fatalf("saved snapshot column %s pk = %d, want %d", columnName, actual.PK, *expected.PrimaryKey)
		}
	}
}

func TestSavedSnapshotDoesNotContainGoMigrationJournal(t *testing.T) {
	snapshot := loadSavedSchemaSnapshot(t)
	objects := savedSnapshotObjects(snapshot)
	for _, objectType := range []string{"table", "view", "index", "trigger"} {
		if _, ok := objects[objectType+":"+goMigrationJournalTable]; ok {
			t.Fatalf("saved TS baseline snapshot must not contain Go journal object %s:%s", objectType, goMigrationJournalTable)
		}
	}
}

func TestSavedSnapshotDocumentsLegacyOptionalExtensionObjects(t *testing.T) {
	snapshot := loadSavedSchemaSnapshot(t)
	objects := savedSnapshotObjects(snapshot)
	legacyOptionalObjects := []string{"custom_subagents", "mcp_servers"}
	for _, name := range legacyOptionalObjects {
		if _, ok := objects["table:"+name]; !ok {
			t.Fatalf("saved baseline should document existing legacy optional table %s", name)
		}
	}
	if _, ok := objects["table:narrator_browser_sessions"]; ok {
		t.Fatalf("saved baseline unexpectedly contains narrator_browser_sessions")
	}
}

func loadSavedSchemaSnapshot(t *testing.T) SQLiteSchemaSnapshot {
	t.Helper()
	data, err := bunfinal.ReadSQLiteSchemaSnapshot()
	if err != nil {
		t.Fatalf("read saved schema snapshot: %v", err)
	}
	var snapshot SQLiteSchemaSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("parse saved schema snapshot: %v", err)
	}
	return snapshot
}

func readSnapshotHashFile(t *testing.T) string {
	t.Helper()
	hash, err := bunfinal.ReadSQLiteSchemaSnapshotHash()
	if err != nil {
		t.Fatalf("read saved schema snapshot hash: %v", err)
	}
	return normalizeSnapshotSQL(hash)
}

func savedSnapshotTables(snapshot SQLiteSchemaSnapshot) map[string]map[string]struct{} {
	tables := map[string]map[string]struct{}{}
	for _, table := range snapshot.Tables {
		columns := map[string]struct{}{}
		for _, column := range table.Columns {
			columns[column.Name] = struct{}{}
		}
		tables[table.Name] = columns
	}
	return tables
}

func savedSnapshotColumnMetadata(snapshot SQLiteSchemaSnapshot) map[string]SQLiteColumnSnapshot {
	columns := map[string]SQLiteColumnSnapshot{}
	for _, table := range snapshot.Tables {
		for _, column := range table.Columns {
			columns[table.Name+"."+column.Name] = column
		}
	}
	return columns
}

func savedSnapshotIndexes(snapshot SQLiteSchemaSnapshot) map[string]SQLiteIndexSnapshot {
	indexes := map[string]SQLiteIndexSnapshot{}
	for _, index := range snapshot.Indexes {
		indexes[index.Name] = index
	}
	return indexes
}

func savedSnapshotForeignKeys(snapshot SQLiteSchemaSnapshot) map[string]SQLiteForeignKeySnapshot {
	foreignKeys := map[string]SQLiteForeignKeySnapshot{}
	for _, fk := range snapshot.ForeignKeys {
		foreignKeys[fk.Table+"."+fk.From+"->"+fk.ReferencedTable+"."+fk.To] = fk
	}
	return foreignKeys
}

func savedSnapshotObjects(snapshot SQLiteSchemaSnapshot) map[string]SQLiteSchemaObject {
	objects := map[string]SQLiteSchemaObject{}
	for _, object := range snapshot.Objects {
		objects[object.Type+":"+object.Name] = object
	}
	return objects
}
