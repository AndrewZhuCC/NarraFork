package db

import "testing"

func TestPostgresSchemaContractFreezesGoOwnedMode(t *testing.T) {
	report := PostgresSchemaContract()
	if report.Mode != "go-owned-postgres-schema" || report.MainSchemaOwner != "go-postgres" {
		t.Fatalf("unexpected Postgres schema ownership contract: %#v", report)
	}
	if !report.GoMainMigrations || report.ReplaysDrizzleSQLiteSQL {
		t.Fatalf("Postgres must use Go-owned migrations and never replay Drizzle SQLite SQL: %#v", report)
	}
	if report.SourceCanonicalHash == "" || report.PostgresSchemaHash == "" {
		t.Fatalf("Postgres schema contract should expose stable hashes: %#v", report)
	}
	if report.SchemaJournalTable != postgresSchemaJournalTable || report.MigrationRunsTable != postgresMigrationRunsTable {
		t.Fatalf("unexpected Postgres journal tables: %#v", report)
	}
}

func TestPostgresSchemaContractExcludesSQLiteFTSAndKeepsBusinessTables(t *testing.T) {
	report := PostgresSchemaContract()
	wantBusinessTables := len(canonicalMainSchema) - len(canonicalFTSObjects)
	if report.BusinessTableCount != wantBusinessTables {
		t.Fatalf("business table count = %d, want %d", report.BusinessTableCount, wantBusinessTables)
	}
	businessNames := postgresContractTableNames(report.BusinessTables)
	for _, fts := range canonicalFTSObjects {
		if _, ok := businessNames[fts.Table]; ok {
			t.Fatalf("Postgres business tables should not include SQLite FTS object %s", fts.Table)
		}
		assertContainsString(t, report.ExcludedSQLiteObjects, fts.Table)
	}
	for table, columns := range canonicalMainSchema {
		if isCanonicalFTSTable(table) {
			continue
		}
		got, ok := postgresContractColumns(report.BusinessTables, table)
		if !ok {
			t.Fatalf("Postgres business tables missing canonical table %s", table)
		}
		if !sameStringSlice(got, columns) {
			t.Fatalf("Postgres table %s columns = %#v, want %#v", table, got, columns)
		}
	}
}

func TestPostgresSchemaContractFreezesLosslessTypeMapping(t *testing.T) {
	report := PostgresSchemaContract()
	expected := map[string]string{
		"sqlite_text":             "postgres_text",
		"sqlite_json_text":        "postgres_text_preserve_raw_json_bytes",
		"sqlite_iso_timestamp":    "postgres_text_preserve_original_timestamp_string",
		"sqlite_timestamp_ms_int": "postgres_bigint",
		"sqlite_integer":          "postgres_bigint_by_default",
		"sqlite_boolean_integer":  "postgres_smallint_check_0_1",
		"sqlite_real":             "postgres_double_precision",
		"sqlite_text_primary_key": "postgres_text_primary_key",
	}
	for source, want := range expected {
		if got := report.TypeMapping[source]; got != want {
			t.Fatalf("type mapping %s = %q, want %q", source, got, want)
		}
	}
}

func TestPostgresSchemaContractRequiresDeferrableForeignKeys(t *testing.T) {
	report := PostgresSchemaContract()
	if !report.RequiresDeferrableFKs || len(report.ForeignKeys) != len(canonicalForeignKeys) {
		t.Fatalf("unexpected FK contract: %#v", report)
	}
	seenRules := map[string]bool{}
	for _, fk := range report.ForeignKeys {
		if !fk.Deferrable || !fk.InitiallyDeferred || !fk.PreserveDeleteRule || fk.DeleteRule == "" {
			t.Fatalf("Postgres FK should be deferrable and preserve delete rules: %#v", fk)
		}
		seenRules[fk.DeleteRule] = true
	}
	for _, rule := range []string{"CASCADE", "SET NULL", "NO ACTION"} {
		if !seenRules[rule] {
			t.Fatalf("Postgres FK contract missing delete rule %s: %#v", rule, report.ForeignKeys)
		}
	}
}

func TestSQLiteAndPostgresSchemaContractsAreDeliberatelySeparate(t *testing.T) {
	sqliteReport := CanonicalSchemaContract()
	postgresReport := PostgresSchemaContract()
	if sqliteReport.MainSchemaOwner == postgresReport.MainSchemaOwner {
		t.Fatalf("SQLite and Postgres owners should differ: sqlite=%q postgres=%q", sqliteReport.MainSchemaOwner, postgresReport.MainSchemaOwner)
	}
	if !sqliteReport.ReadOnlyVerifier || !postgresReport.GoMainMigrations {
		t.Fatalf("SQLite must remain read-only while Postgres is Go-owned: sqlite=%#v postgres=%#v", sqliteReport, postgresReport)
	}
	if sqliteReport.CanonicalFTSTableCount == 0 || len(postgresReport.ExcludedSQLiteObjects) == 0 {
		t.Fatalf("SQLite FTS must be explicit source-only derived data: sqlite=%#v postgres=%#v", sqliteReport, postgresReport)
	}
}

func TestPostgresSchemaContractIncludesGoOwnedOptionalTables(t *testing.T) {
	report := PostgresSchemaContract()
	if report.OptionalGoTableCount != 1 {
		t.Fatalf("optional Go table count = %d, want 1", report.OptionalGoTableCount)
	}
	columns, ok := postgresContractColumns(report.OptionalGoTables, "narrator_browser_sessions")
	if !ok {
		t.Fatal("Postgres optional tables should include narrator_browser_sessions")
	}
	for _, column := range []string{"id", "narrator_id", "status", "screenshot_data_base64", "metadata_json", "created_at", "updated_at"} {
		assertContainsString(t, columns, column)
	}
}

func isCanonicalFTSTable(table string) bool {
	for _, fts := range canonicalFTSObjects {
		if fts.Table == table {
			return true
		}
	}
	return false
}

func postgresContractColumns(tables []canonicalSchemaHashTable, table string) ([]string, bool) {
	for _, item := range tables {
		if item.Name == table {
			return item.Columns, true
		}
	}
	return nil, false
}
