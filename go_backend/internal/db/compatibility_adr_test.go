package db

import "testing"

func TestCurrentDatabaseCompatibilityADRFreezesDualBackendGoal(t *testing.T) {
	adr := CurrentDatabaseCompatibilityADR()
	if adr.Status != "accepted" {
		t.Fatalf("ADR status = %q, want accepted", adr.Status)
	}
	if adr.DefaultEngine != EngineSQLite {
		t.Fatalf("default engine = %q, want sqlite", adr.DefaultEngine)
	}
	assertContainsEngine(t, adr.TargetEngines, EngineSQLite)
	assertContainsEngine(t, adr.TargetEngines, EnginePostgres)
	if adr.MigrationDirection != "sqlite_to_postgres_one_way_offline" {
		t.Fatalf("migration direction = %q", adr.MigrationDirection)
	}
}

func TestCurrentDatabaseCompatibilityADRFreezesSchemaOwnership(t *testing.T) {
	adr := CurrentDatabaseCompatibilityADR()
	if adr.SchemaOwnership[EngineSQLite] != "typescript-drizzle-ts-owned-canonical-schema" {
		t.Fatalf("sqlite owner = %q", adr.SchemaOwnership[EngineSQLite])
	}
	if adr.SchemaOwnership[EnginePostgres] != "go-owned-postgres-schema-and-migrations" {
		t.Fatalf("postgres owner = %q", adr.SchemaOwnership[EnginePostgres])
	}
}

func TestCurrentDatabaseCompatibilityADRFreezesLosslessMigrationBoundary(t *testing.T) {
	adr := CurrentDatabaseCompatibilityADR()
	for _, item := range []string{
		"business_table_rows",
		"text_primary_keys",
		"foreign_key_relationships",
		"null_vs_empty_string",
		"json_text_raw_bytes",
		"iso_timestamp_text",
		"numeric_semantics",
		"go_owned_optional_runtime_tables_when_present",
	} {
		assertContainsString(t, adr.LosslessBoundary.MustPreserve, item)
	}
	for _, item := range []string{
		"sqlite_wal_and_shm_files",
		"sqlite_internal_rowid_values",
		"sqlite_fts5_virtual_or_shadow_tables",
		"derived_search_indexes",
		"bidirectional_or_live_dual_write_replication",
	} {
		assertContainsString(t, adr.LosslessBoundary.OutOfScope, item)
	}
}

func TestCurrentDatabaseCompatibilityADRFreezesAcceptanceCriteria(t *testing.T) {
	adr := CurrentDatabaseCompatibilityADR()
	for _, item := range []string{
		"sqlite_default_behavior_matches_existing_ts_owned_canonical_mode",
		"postgres_driver_dependency_is_explicitly_fail_fast_until_linked",
		"postgres_never_replays_drizzle_sqlite_migrations",
		"postgres_schema_has_independent_go_owned_migration_journal",
		"sqlite_to_postgres_migrator_supports_dry_run_full_copy_verify_and_cutover",
		"migration_verify_checks_row_counts_table_hashes_json_raw_hashes_and_foreign_keys",
		"post_cutover_smoke_covers_health_auth_projects_graph_narrator_messages_terminals_and_search",
	} {
		assertContainsString(t, adr.AcceptanceCriteria, item)
	}
}

func assertContainsEngine(t *testing.T, haystack []Engine, needle Engine) {
	t.Helper()
	for _, value := range haystack {
		if value == needle {
			return
		}
	}
	t.Fatalf("expected %#v to contain %q", haystack, needle)
}

func assertContainsString(t *testing.T, haystack []string, needle string) {
	t.Helper()
	for _, value := range haystack {
		if value == needle {
			return
		}
	}
	t.Fatalf("expected %#v to contain %q", haystack, needle)
}
