package db

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

const (
	postgresSchemaJournalTable = "__narrafork_pg_schema_migrations"
	postgresMigrationRunsTable = "__narrafork_migration_runs"
)

type PostgresSchemaContractReport struct {
	Mode                    string                       `json:"mode"`
	MainSchemaOwner         string                       `json:"mainSchemaOwner"`
	GoMainMigrations        bool                         `json:"goMainMigrations"`
	ReplaysDrizzleSQLiteSQL bool                         `json:"replaysDrizzleSqliteSql"`
	SourceCanonicalHash     string                       `json:"sourceCanonicalHash"`
	PostgresSchemaHash      string                       `json:"postgresSchemaHash"`
	SchemaJournalTable      string                       `json:"schemaJournalTable"`
	MigrationRunsTable      string                       `json:"migrationRunsTable"`
	BusinessTableCount      int                          `json:"businessTableCount"`
	OptionalGoTableCount    int                          `json:"optionalGoTableCount"`
	ExcludedSQLiteObjects   []string                     `json:"excludedSqliteObjects"`
	TypeMapping             map[string]string            `json:"typeMapping"`
	BusinessTables          []canonicalSchemaHashTable   `json:"businessTables"`
	OptionalGoTables        []canonicalSchemaHashTable   `json:"optionalGoTables"`
	Indexes                 []canonicalIndexRequirement  `json:"indexes"`
	ForeignKeys             []PostgresForeignKeyContract `json:"foreignKeys"`
	RequiresDeferrableFKs   bool                         `json:"requiresDeferrableFks"`
}

type PostgresForeignKeyContract struct {
	Table              string `json:"table"`
	Column             string `json:"column"`
	ReferencedTable    string `json:"referencedTable"`
	ReferencedColumn   string `json:"referencedColumn"`
	DeleteRule         string `json:"deleteRule"`
	Deferrable         bool   `json:"deferrable"`
	InitiallyDeferred  bool   `json:"initiallyDeferred"`
	PreserveDeleteRule bool   `json:"preserveDeleteRule"`
}

func PostgresBusinessSchemaDDL() []string {
	statements := []string{}
	for _, table := range postgresBusinessTables() {
		statements = append(statements, postgresCreateTableDDL(table))
	}
	for _, index := range sortedCanonicalIndexes() {
		statements = append(statements, postgresCreateIndexDDL(index))
	}
	for _, fk := range postgresForeignKeys() {
		statements = append(statements, postgresForeignKeyDDL(fk))
	}
	return statements
}

func PostgresOptionalSchemaDDL() []string {
	statements := []string{}
	for _, table := range postgresOptionalGoTables() {
		statements = append(statements, postgresCreateTableDDL(table))
	}
	return statements
}

func PostgresMigrationJournalDDL() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS __narrafork_pg_schema_migrations (
	version BIGINT PRIMARY KEY,
	schema_hash TEXT NOT NULL,
	source_canonical_hash TEXT NOT NULL,
	mapping_hash TEXT NOT NULL,
	status TEXT NOT NULL,
	details_json TEXT NOT NULL,
	applied_at TEXT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS __narrafork_migration_runs (
	id TEXT PRIMARY KEY,
	direction TEXT NOT NULL,
	source_engine TEXT NOT NULL,
	target_engine TEXT NOT NULL,
	source_schema_hash TEXT NOT NULL,
	target_schema_hash TEXT NOT NULL,
	status TEXT NOT NULL,
	started_at TEXT NOT NULL,
	completed_at TEXT,
	details_json TEXT NOT NULL
)`,
	}
}

func PostgresSchemaContract() PostgresSchemaContractReport {
	businessTables := postgresBusinessTables()
	optionalTables := postgresOptionalGoTables()
	foreignKeys := postgresForeignKeys()
	report := PostgresSchemaContractReport{
		Mode:                    "go-owned-postgres-schema",
		MainSchemaOwner:         "go-postgres",
		GoMainMigrations:        true,
		ReplaysDrizzleSQLiteSQL: false,
		SourceCanonicalHash:     canonicalSchemaContractHash(),
		SchemaJournalTable:      postgresSchemaJournalTable,
		MigrationRunsTable:      postgresMigrationRunsTable,
		BusinessTableCount:      len(businessTables),
		OptionalGoTableCount:    len(optionalTables),
		ExcludedSQLiteObjects:   postgresExcludedSQLiteObjects(),
		TypeMapping:             postgresTypeMapping(),
		BusinessTables:          businessTables,
		OptionalGoTables:        optionalTables,
		Indexes:                 sortedCanonicalIndexes(),
		ForeignKeys:             foreignKeys,
		RequiresDeferrableFKs:   true,
	}
	report.PostgresSchemaHash = hashPostgresSchemaContract(report)
	return report
}

func postgresBusinessTables() []canonicalSchemaHashTable {
	excluded := map[string]struct{}{}
	for _, fts := range canonicalFTSObjects {
		excluded[fts.Table] = struct{}{}
	}
	tables := make([]string, 0, len(canonicalMainSchema))
	for table := range canonicalMainSchema {
		if _, skip := excluded[table]; skip {
			continue
		}
		tables = append(tables, table)
	}
	sort.Strings(tables)
	out := make([]canonicalSchemaHashTable, 0, len(tables))
	for _, table := range tables {
		out = append(out, canonicalSchemaHashTable{Name: table, Columns: append([]string(nil), canonicalMainSchema[table]...)})
	}
	return out
}

func postgresCreateTableDDL(table canonicalSchemaHashTable) string {
	columns := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		columnDDL := (PostgresDialect{}).QuoteIdent(column) + " " + postgresColumnType(table.Name, column)
		if postgresPrimaryKeyColumn(table.Name, column) {
			columnDDL += " PRIMARY KEY"
		}
		columns = append(columns, columnDDL)
	}
	return "CREATE TABLE IF NOT EXISTS " + (PostgresDialect{}).QuoteIdent(table.Name) + " (\n\t" + strings.Join(columns, ",\n\t") + "\n)"
}

func postgresCreateIndexDDL(index canonicalIndexRequirement) string {
	columns := make([]string, 0, len(index.Columns))
	for _, column := range index.Columns {
		columns = append(columns, (PostgresDialect{}).QuoteIdent(column))
	}
	unique := ""
	if index.Unique {
		unique = "UNIQUE "
	}
	return "CREATE " + unique + "INDEX IF NOT EXISTS " + (PostgresDialect{}).QuoteIdent(index.Name) + " ON " + (PostgresDialect{}).QuoteIdent(index.Table) + " (" + strings.Join(columns, ", ") + ")"
}

func postgresForeignKeyDDL(fk PostgresForeignKeyContract) string {
	constraint := "fk_" + fk.Table + "_" + fk.Column
	return "ALTER TABLE " + (PostgresDialect{}).QuoteIdent(fk.Table) +
		" ADD CONSTRAINT " + (PostgresDialect{}).QuoteIdent(constraint) +
		" FOREIGN KEY (" + (PostgresDialect{}).QuoteIdent(fk.Column) + ")" +
		" REFERENCES " + (PostgresDialect{}).QuoteIdent(fk.ReferencedTable) +
		" (" + (PostgresDialect{}).QuoteIdent(fk.ReferencedColumn) + ")" +
		" ON DELETE " + fk.DeleteRule +
		" DEFERRABLE INITIALLY DEFERRED"
}

func postgresPrimaryKeyColumn(table string, column string) bool {
	if table == "port_allocations" && column == "port" {
		return true
	}
	return column == "id"
}

func postgresColumnType(table string, column string) string {
	if table == "port_allocations" && column == "port" {
		return "BIGINT"
	}
	switch column {
	case "pinned", "graph_opened", "fast_mode", "relaxed_plan", "plan_mode", "is_background", "is_ask_in_passing", "message_version":
		return "BIGINT"
	}
	if postgresPrimaryKeyColumn(table, column) || strings.HasSuffix(column, "_id") || column == "user_id" || column == "message_id" || column == "narrator_id" || column == "chapter_id" {
		return "TEXT"
	}
	if strings.HasSuffix(column, "_json") || column == "metadata" || column == "config" || column == "output" || column == "eval_output" {
		return "TEXT"
	}
	if strings.HasSuffix(column, "_at") || column == "created_at" || column == "updated_at" || column == "started_at" || column == "completed_at" {
		return "TEXT"
	}
	if strings.Contains(column, "cost") || strings.Contains(column, "percent") || strings.Contains(column, "score") || strings.HasSuffix(column, "_x") || strings.HasSuffix(column, "_y") || strings.HasSuffix(column, "_width") || strings.HasSuffix(column, "_height") || strings.HasSuffix(column, "_offset") {
		return "DOUBLE PRECISION"
	}
	if strings.Contains(column, "count") || strings.Contains(column, "tokens") || strings.HasSuffix(column, "_ms") || strings.HasSuffix(column, "_bytes") || strings.HasSuffix(column, "_port") || strings.HasSuffix(column, "_order") || strings.HasPrefix(column, "is_") || strings.HasPrefix(column, "show_") || strings.HasPrefix(column, "notify_") || strings.HasPrefix(column, "auto_") || strings.HasPrefix(column, "word_wrap_") || strings.HasSuffix(column, "enabled") || strings.HasSuffix(column, "expanded") || strings.HasSuffix(column, "truncated") || strings.HasSuffix(column, "passed") || strings.HasSuffix(column, "failed") {
		return "BIGINT"
	}
	return "TEXT"
}

func postgresOptionalGoTables() []canonicalSchemaHashTable {
	return []canonicalSchemaHashTable{
		{
			Name: "narrator_browser_sessions",
			Columns: []string{
				"id",
				"narrator_id",
				"status",
				"url",
				"title",
				"ttl_seconds",
				"ttl_ms",
				"expires_at",
				"expires_at_ms",
				"last_activity_ms",
				"headless",
				"screenshot_data_base64",
				"screenshot_mime",
				"tracing_status",
				"tracing_json",
				"metadata_json",
				"created_at",
				"updated_at",
				"closed_at",
			},
		},
	}
}

func postgresExcludedSQLiteObjects() []string {
	objects := []string{"sqlite_wal", "sqlite_shm", "sqlite_internal_rowid"}
	for _, fts := range canonicalFTSObjects {
		objects = append(objects, fts.Table)
	}
	for _, trigger := range canonicalFTSTriggers {
		objects = append(objects, trigger.Name)
	}
	sort.Strings(objects)
	return objects
}

func postgresTypeMapping() map[string]string {
	return map[string]string{
		"sqlite_text":             "postgres_text",
		"sqlite_json_text":        "postgres_text_preserve_raw_json_bytes",
		"sqlite_iso_timestamp":    "postgres_text_preserve_original_timestamp_string",
		"sqlite_timestamp_ms_int": "postgres_bigint",
		"sqlite_integer":          "postgres_bigint_by_default",
		"sqlite_boolean_integer":  "postgres_smallint_check_0_1",
		"sqlite_real":             "postgres_double_precision",
		"sqlite_text_primary_key": "postgres_text_primary_key",
	}
}

func postgresForeignKeys() []PostgresForeignKeyContract {
	values := make([]PostgresForeignKeyContract, 0, len(canonicalForeignKeys))
	for _, fk := range sortedCanonicalForeignKeys() {
		values = append(values, PostgresForeignKeyContract{
			Table:              fk.Table,
			Column:             fk.Column,
			ReferencedTable:    fk.ReferencedTable,
			ReferencedColumn:   fk.ReferencedColumn,
			DeleteRule:         postgresForeignKeyDeleteRule(fk),
			Deferrable:         true,
			InitiallyDeferred:  true,
			PreserveDeleteRule: true,
		})
	}
	return values
}

func postgresForeignKeyDeleteRule(fk canonicalForeignKeyRequirement) string {
	switch fk.Table + "." + fk.Column {
	case "api_requests.narrator_id",
		"background_tasks.parent_narrator_id",
		"background_tasks.subagent_narrator_id",
		"chapter_commits.chapter_id",
		"chapter_edges.project_id",
		"chapter_edges.source_id",
		"chapter_edges.target_id",
		"chapters.project_id",
		"exploration_groups.project_id",
		"gateway_session_mappings.narrator_id",
		"narrator_file_snapshots.narrator_id",
		"narrator_goals.narrator_id",
		"narrator_sidecars.message_id",
		"narrator_sidecars.narrator_id",
		"review_conclusions.review_chapter_id",
		"review_conclusions.source_chapter_id":
		return "CASCADE"
	case "api_requests.message_id",
		"chapter_commits.narrator_id",
		"chapter_commits.narrator_message_id",
		"chapters.exploration_group_id",
		"chapters.merged_into_chapter_id",
		"chapters.parent_chapter_id":
		return "SET NULL"
	default:
		return "NO ACTION"
	}
}

func hashPostgresSchemaContract(report PostgresSchemaContractReport) string {
	payload := report
	payload.PostgresSchemaHash = ""
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func postgresContractTableNames(tables []canonicalSchemaHashTable) map[string]struct{} {
	out := map[string]struct{}{}
	for _, table := range tables {
		out[strings.TrimSpace(table.Name)] = struct{}{}
	}
	return out
}
