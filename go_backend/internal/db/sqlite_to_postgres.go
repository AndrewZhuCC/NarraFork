package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

type SQLiteToPostgresOptions struct {
	SQLitePath          string
	PostgresDSN         string
	PostgresSchema      string
	DryRun              bool
	Verify              bool
	AllowPartialSource  bool
	AllowNonEmptyTarget bool
}

type SQLiteToPostgresReport struct {
	DryRun               bool              `json:"dryRun"`
	SourceSQLitePath     string            `json:"sourceSQLitePath"`
	TargetEngine         Engine            `json:"targetEngine"`
	SourceSchemaHash     string            `json:"sourceSchemaHash"`
	TargetSchemaHash     string            `json:"targetSchemaHash"`
	PlannedTables        []string          `json:"plannedTables"`
	OptionalTables       []string          `json:"optionalTables,omitempty"`
	MissingSourceTables  []string          `json:"missingSourceTables,omitempty"`
	CopiedRows           map[string]int    `json:"copiedRows,omitempty"`
	VerifiedRows         map[string]int    `json:"verifiedRows,omitempty"`
	SourceTableHashes    map[string]string `json:"sourceTableHashes,omitempty"`
	TargetTableHashes    map[string]string `json:"targetTableHashes,omitempty"`
	ConstraintsValidated bool              `json:"constraintsValidated"`
}

func RunSQLiteToPostgres(ctx context.Context, opts SQLiteToPostgresOptions) (SQLiteToPostgresReport, error) {
	if strings.TrimSpace(opts.SQLitePath) == "" {
		return SQLiteToPostgresReport{}, fmt.Errorf("sqlite path is required")
	}
	if strings.TrimSpace(opts.PostgresDSN) == "" {
		return SQLiteToPostgresReport{}, fmt.Errorf("postgres DSN is required")
	}
	report := SQLiteToPostgresReport{
		DryRun:            opts.DryRun,
		SourceSQLitePath:  opts.SQLitePath,
		TargetEngine:      EnginePostgres,
		SourceSchemaHash:  canonicalSchemaContractHash(),
		TargetSchemaHash:  PostgresSchemaContract().PostgresSchemaHash,
		PlannedTables:     postgresMigrationTableNames(),
		OptionalTables:    postgresOptionalMigrationTableNames(),
		CopiedRows:        map[string]int{},
		VerifiedRows:      map[string]int{},
		SourceTableHashes: map[string]string{},
		TargetTableHashes: map[string]string{},
	}

	source, err := sql.Open((SQLiteDialect{}).DriverName(), "file:"+opts.SQLitePath+"?mode=ro")
	if err != nil {
		return report, fmt.Errorf("open sqlite source read-only: %w", err)
	}
	defer source.Close()
	source.SetMaxOpenConns(1)
	source.SetMaxIdleConns(1)
	if err := source.PingContext(ctx); err != nil {
		return report, fmt.Errorf("ping sqlite source: %w", err)
	}

	target, err := sql.Open((PostgresDialect{}).DriverName(), opts.PostgresDSN)
	if err != nil {
		return report, fmt.Errorf("open postgres target: %w", err)
	}
	defer target.Close()
	target.SetMaxOpenConns(1)
	target.SetMaxIdleConns(1)
	if err := target.PingContext(ctx); err != nil {
		return report, fmt.Errorf("ping postgres target: %w", err)
	}

	missing, err := missingSQLiteSourceTables(ctx, source, report.PlannedTables)
	if err != nil {
		return report, err
	}
	report.MissingSourceTables = missing
	if len(missing) > 0 && !opts.AllowPartialSource {
		return report, fmt.Errorf("sqlite source is missing %d planned tables: %s", len(missing), strings.Join(missing, ", "))
	}
	if opts.DryRun {
		return report, nil
	}
	if err := configurePostgresMigrationSchema(ctx, target, opts.PostgresSchema); err != nil {
		return report, err
	}

	if err := initializePostgresMigrationSchema(ctx, target); err != nil {
		return report, err
	}
	nonEmpty, err := nonEmptyPostgresTables(ctx, target, append(append([]string{}, report.PlannedTables...), report.OptionalTables...))
	if err != nil {
		return report, err
	}
	if len(nonEmpty) > 0 && !opts.AllowNonEmptyTarget {
		return report, fmt.Errorf("postgres target has non-empty planned tables: %s", strings.Join(nonEmpty, ", "))
	}

	copied, constraintsValidated, err := copySQLiteRowsToPostgres(ctx, source, target, opts.AllowPartialSource)
	if err != nil {
		return report, err
	}
	report.CopiedRows = copied
	report.ConstraintsValidated = constraintsValidated
	if opts.Verify {
		verified, sourceHashes, targetHashes, err := verifySQLiteToPostgresRows(ctx, source, target, copied)
		if err != nil {
			return report, err
		}
		report.VerifiedRows = verified
		report.SourceTableHashes = sourceHashes
		report.TargetTableHashes = targetHashes
	}
	return report, nil
}

func configurePostgresMigrationSchema(ctx context.Context, target *sql.DB, schema string) error {
	schema = strings.TrimSpace(schema)
	if schema == "" {
		return nil
	}
	quoted := postgresSearchPathIdentifier(schema)
	if _, err := target.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoted); err != nil {
		return fmt.Errorf("create postgres migration schema %s: %w", schema, err)
	}
	if err := SetPostgresSearchPath(ctx, target, schema); err != nil {
		return fmt.Errorf("set postgres migration schema %s: %w", schema, err)
	}
	return nil
}

func initializePostgresMigrationSchema(ctx context.Context, target *sql.DB) error {
	for _, stmt := range PostgresBusinessSchemaDDL() {
		if _, err := target.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("initialize postgres business schema: %w", err)
		}
	}
	for _, stmt := range PostgresOptionalSchemaDDL() {
		if _, err := target.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("initialize postgres optional schema: %w", err)
		}
	}
	for _, stmt := range PostgresMigrationJournalDDL() {
		if _, err := target.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("initialize postgres migration journal: %w", err)
		}
	}
	return nil
}

func copySQLiteRowsToPostgres(ctx context.Context, source *sql.DB, target *sql.DB, allowPartial bool) (map[string]int, bool, error) {
	copied := map[string]int{}
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return copied, false, fmt.Errorf("begin postgres copy transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SET CONSTRAINTS ALL DEFERRED"); err != nil {
		return copied, false, fmt.Errorf("defer postgres constraints: %w", err)
	}
	for _, table := range postgresBusinessTables() {
		exists, err := sqliteTableExists(ctx, source, table.Name)
		if err != nil {
			return copied, false, err
		}
		if !exists && allowPartial {
			continue
		}
		if !exists {
			return copied, false, fmt.Errorf("sqlite source table %s is missing", table.Name)
		}
		count, err := copySQLiteTableToPostgres(ctx, source, tx, table)
		if err != nil {
			return copied, false, err
		}
		copied[table.Name] = count
	}
	for _, table := range postgresOptionalGoTables() {
		exists, err := sqliteTableExists(ctx, source, table.Name)
		if err != nil {
			return copied, false, err
		}
		if !exists {
			continue
		}
		count, err := copySQLiteTableToPostgres(ctx, source, tx, table)
		if err != nil {
			return copied, false, err
		}
		copied[table.Name] = count
	}
	if _, err := tx.ExecContext(ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
		return copied, false, fmt.Errorf("validate postgres constraints: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return copied, false, fmt.Errorf("commit postgres copy transaction: %w", err)
	}
	return copied, true, nil
}

func copySQLiteTableToPostgres(ctx context.Context, source *sql.DB, tx *sql.Tx, table canonicalSchemaHashTable) (int, error) {
	columns := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		columns = append(columns, (SQLiteDialect{}).QuoteIdent(column))
	}
	query := "SELECT " + strings.Join(columns, ", ") + " FROM " + (SQLiteDialect{}).QuoteIdent(table.Name)
	rows, err := source.QueryContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("read sqlite table %s: %w", table.Name, err)
	}
	defer rows.Close()

	placeholders := make([]string, len(table.Columns))
	insertColumns := make([]string, len(table.Columns))
	for i, column := range table.Columns {
		placeholders[i] = (PostgresDialect{}).Placeholder(i + 1)
		insertColumns[i] = (PostgresDialect{}).QuoteIdent(column)
	}
	insert := "INSERT INTO " + (PostgresDialect{}).QuoteIdent(table.Name) + " (" + strings.Join(insertColumns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	count := 0
	for rows.Next() {
		values := make([]any, len(table.Columns))
		dest := make([]any, len(table.Columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return count, fmt.Errorf("scan sqlite table %s: %w", table.Name, err)
		}
		if _, err := tx.ExecContext(ctx, insert, values...); err != nil {
			return count, fmt.Errorf("insert postgres table %s: %w", table.Name, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("iterate sqlite table %s: %w", table.Name, err)
	}
	return count, nil
}

func verifySQLiteToPostgresRows(ctx context.Context, source *sql.DB, target *sql.DB, copied map[string]int) (map[string]int, map[string]string, map[string]string, error) {
	verified := map[string]int{}
	sourceHashes := map[string]string{}
	targetHashes := map[string]string{}
	for table, sourceCount := range copied {
		targetCount, err := postgresTableRowCount(ctx, target, table)
		if err != nil {
			return verified, sourceHashes, targetHashes, err
		}
		if targetCount != sourceCount {
			return verified, sourceHashes, targetHashes, fmt.Errorf("row count mismatch for %s: sqlite=%d postgres=%d", table, sourceCount, targetCount)
		}
		contract, ok := postgresContractTable(table)
		if !ok {
			return verified, sourceHashes, targetHashes, fmt.Errorf("missing table contract for %s", table)
		}
		sourceHash, err := hashSQLiteTableRows(ctx, source, contract)
		if err != nil {
			return verified, sourceHashes, targetHashes, err
		}
		targetHash, err := hashPostgresTableRows(ctx, target, contract)
		if err != nil {
			return verified, sourceHashes, targetHashes, err
		}
		if sourceHash != targetHash {
			return verified, sourceHashes, targetHashes, fmt.Errorf("row hash mismatch for %s: sqlite=%s postgres=%s", table, sourceHash, targetHash)
		}
		verified[table] = targetCount
		sourceHashes[table] = sourceHash
		targetHashes[table] = targetHash
	}
	return verified, sourceHashes, targetHashes, nil
}

func postgresContractTable(name string) (canonicalSchemaHashTable, bool) {
	for _, table := range append(append([]canonicalSchemaHashTable{}, postgresBusinessTables()...), postgresOptionalGoTables()...) {
		if table.Name == name {
			return table, true
		}
	}
	return canonicalSchemaHashTable{}, false
}

func hashSQLiteTableRows(ctx context.Context, source *sql.DB, table canonicalSchemaHashTable) (string, error) {
	columns := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		columns = append(columns, (SQLiteDialect{}).QuoteIdent(column))
	}
	query := "SELECT " + strings.Join(columns, ", ") + " FROM " + (SQLiteDialect{}).QuoteIdent(table.Name) + " ORDER BY " + (SQLiteDialect{}).QuoteIdent(table.Columns[0])
	return hashQueryRows(ctx, source, query, len(table.Columns), table.Name)
}

func hashPostgresTableRows(ctx context.Context, target *sql.DB, table canonicalSchemaHashTable) (string, error) {
	columns := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		columns = append(columns, (PostgresDialect{}).QuoteIdent(column))
	}
	query := "SELECT " + strings.Join(columns, ", ") + " FROM " + (PostgresDialect{}).QuoteIdent(table.Name) + " ORDER BY " + (PostgresDialect{}).QuoteIdent(table.Columns[0])
	return hashQueryRows(ctx, target, query, len(table.Columns), table.Name)
}

func hashQueryRows(ctx context.Context, dbtx DBTX, query string, columnCount int, table string) (string, error) {
	rows, err := dbtx.QueryContext(ctx, query)
	if err != nil {
		return "", fmt.Errorf("hash query rows for %s: %w", table, err)
	}
	defer rows.Close()
	hasher := sha256.New()
	for rows.Next() {
		values := make([]any, columnCount)
		dest := make([]any, columnCount)
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return "", fmt.Errorf("scan rows for hash %s: %w", table, err)
		}
		for _, value := range values {
			hasher.Write([]byte(normalizeMigrationHashValue(value)))
			hasher.Write([]byte{0x1f})
		}
		hasher.Write([]byte{0x1e})
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate rows for hash %s: %w", table, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func normalizeMigrationHashValue(value any) string {
	if value == nil {
		return "<NULL>"
	}
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func postgresMigrationTableNames() []string {
	tables := postgresBusinessTables()
	names := make([]string, 0, len(tables))
	for _, table := range tables {
		names = append(names, table.Name)
	}
	return names
}

func postgresOptionalMigrationTableNames() []string {
	tables := postgresOptionalGoTables()
	names := make([]string, 0, len(tables))
	for _, table := range tables {
		names = append(names, table.Name)
	}
	return names
}

func missingSQLiteSourceTables(ctx context.Context, source *sql.DB, tables []string) ([]string, error) {
	missing := []string{}
	for _, table := range tables {
		exists, err := sqliteTableExists(ctx, source, table)
		if err != nil {
			return nil, err
		}
		if !exists {
			missing = append(missing, table)
		}
	}
	return missing, nil
}

func sqliteTableExists(ctx context.Context, source *sql.DB, table string) (bool, error) {
	var name string
	err := source.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect sqlite table %s: %w", table, err)
	}
	return true, nil
}

func nonEmptyPostgresTables(ctx context.Context, target *sql.DB, tables []string) ([]string, error) {
	nonEmpty := []string{}
	for _, table := range tables {
		count, err := postgresTableRowCount(ctx, target, table)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			nonEmpty = append(nonEmpty, table)
		}
	}
	return nonEmpty, nil
}

func postgresTableRowCount(ctx context.Context, target *sql.DB, table string) (int, error) {
	var count int
	query := "SELECT COUNT(*) FROM " + (PostgresDialect{}).QuoteIdent(table)
	if err := target.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("count postgres table %s: %w", table, err)
	}
	return count, nil
}
