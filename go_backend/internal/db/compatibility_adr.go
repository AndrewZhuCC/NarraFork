package db

// DatabaseCompatibilityADR is the executable architecture decision record for
// go_backend database portability. It intentionally lives in code instead of a
// Markdown document so tests can freeze the migration boundaries and ownership
// contract that future database work must respect.
type DatabaseCompatibilityADR struct {
	Status             string
	TargetEngines      []Engine
	DefaultEngine      Engine
	MigrationDirection string
	SchemaOwnership    map[Engine]string
	LosslessBoundary   LosslessMigrationBoundary
	AcceptanceCriteria []string
	NonGoals           []string
}

type LosslessMigrationBoundary struct {
	MustPreserve []string
	OutOfScope   []string
}

func CurrentDatabaseCompatibilityADR() DatabaseCompatibilityADR {
	return DatabaseCompatibilityADR{
		Status: "accepted",
		TargetEngines: []Engine{
			EngineSQLite,
			EnginePostgres,
		},
		DefaultEngine:      EngineSQLite,
		MigrationDirection: "sqlite_to_postgres_one_way_offline",
		SchemaOwnership: map[Engine]string{
			EngineSQLite:   "typescript-drizzle-ts-owned-canonical-schema",
			EnginePostgres: "go-owned-postgres-schema-and-migrations",
		},
		LosslessBoundary: LosslessMigrationBoundary{
			MustPreserve: []string{
				"business_table_rows",
				"text_primary_keys",
				"foreign_key_relationships",
				"null_vs_empty_string",
				"json_text_raw_bytes",
				"iso_timestamp_text",
				"numeric_semantics",
				"go_owned_optional_runtime_tables_when_present",
			},
			OutOfScope: []string{
				"sqlite_wal_and_shm_files",
				"sqlite_internal_rowid_values",
				"sqlite_fts5_virtual_or_shadow_tables",
				"sqlite_internal_catalog_tables",
				"derived_search_indexes",
				"bidirectional_or_live_dual_write_replication",
			},
		},
		AcceptanceCriteria: []string{
			"sqlite_default_behavior_matches_existing_ts_owned_canonical_mode",
			"postgres_driver_dependency_is_explicitly_fail_fast_until_linked",
			"postgres_never_replays_drizzle_sqlite_migrations",
			"postgres_schema_has_independent_go_owned_migration_journal",
			"sqlite_to_postgres_migrator_supports_dry_run_full_copy_verify_and_cutover",
			"migration_verify_checks_row_counts_table_hashes_json_raw_hashes_and_foreign_keys",
			"post_cutover_smoke_covers_health_auth_projects_graph_narrator_messages_terminals_and_search",
			"go_backend_sqlite_test_suite_remains_green",
		},
		NonGoals: []string{
			"making_the_typescript_bun_backend_postgres_compatible_in_this_track",
			"automatic_runtime_dual_write_between_sqlite_and_postgres",
			"converting_json_text_columns_to_jsonb_in_the_lossless_v1_migration",
			"converting_iso_timestamp_text_columns_to_timestamptz_in_the_lossless_v1_migration",
			"copying_sqlite_fts5_implementation_details_into_postgres",
		},
	}
}
