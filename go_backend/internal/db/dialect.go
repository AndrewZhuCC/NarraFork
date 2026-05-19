package db

import (
	"context"
	"fmt"
	"strings"
)

type Dialect interface {
	Name() Engine
	DriverName() string
	Placeholder(n int) string
	Rebind(query string) string
	QuoteIdent(identifier string) string
}

func NewDialect(engine Engine) (Dialect, error) {
	switch engine {
	case EngineSQLite:
		return SQLiteDialect{}, nil
	case EnginePostgres:
		return PostgresDialect{}, nil
	default:
		return nil, fmt.Errorf("unsupported database dialect %q", engine)
	}
}

type SQLiteDialect struct{}

func (SQLiteDialect) Name() Engine { return EngineSQLite }

func (SQLiteDialect) DriverName() string { return "sqlite" }

func (SQLiteDialect) Placeholder(int) string { return "?" }

func (SQLiteDialect) Rebind(query string) string { return query }

func (SQLiteDialect) QuoteIdent(identifier string) string { return quoteSQLIdentifier(identifier) }

type PostgresDialect struct{}

func (PostgresDialect) Name() Engine { return EnginePostgres }

func (PostgresDialect) DriverName() string { return postgresRebindDriverName }

func (PostgresDialect) Placeholder(n int) string { return fmt.Sprintf("$%d", n) }

func (PostgresDialect) Rebind(query string) string { return rebindPostgres(query) }

func (PostgresDialect) QuoteIdent(identifier string) string { return quoteSQLIdentifier(identifier) }

func quoteSQLIdentifier(identifier string) string {
	out := `"`
	for _, r := range identifier {
		if r == '"' {
			out += `""`
			continue
		}
		out += string(r)
	}
	return out + `"`
}

func postgresSearchPathIdentifier(schema string) string {
	return (PostgresDialect{}).QuoteIdent(strings.TrimSpace(schema))
}

func postgresSetSearchPathSQL(schema string) string {
	return "SET search_path TO " + postgresSearchPathIdentifier(schema)
}

func SetPostgresSearchPath(ctx context.Context, dbtx DBTX, schema string) error {
	if strings.TrimSpace(schema) == "" || dbtx == nil {
		return nil
	}
	_, err := dbtx.ExecContext(ctx, postgresSetSearchPathSQL(schema))
	return err
}

func TableColumnSet(ctx context.Context, dbtx DBTX, table string) (map[string]bool, error) {
	columns, err := postgresTableColumnSet(ctx, dbtx, table)
	if err == nil {
		return columns, nil
	}
	return sqliteTableColumnSet(ctx, dbtx, table)
}

func postgresTableColumnSet(ctx context.Context, dbtx DBTX, table string) (map[string]bool, error) {
	rows, err := dbtx.QueryContext(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ?`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func sqliteTableColumnSet(ctx context.Context, dbtx DBTX, table string) (map[string]bool, error) {
	rows, err := dbtx.QueryContext(ctx, `PRAGMA table_info(`+(SQLiteDialect{}).QuoteIdent(table)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func InsertOnConflictUpdateSQL(table string, insertColumns []string, conflictColumns []string, updateColumns []string) string {
	dialect := SQLiteDialect{}
	quotedInsert := make([]string, 0, len(insertColumns))
	placeholders := make([]string, 0, len(insertColumns))
	for range insertColumns {
		placeholders = append(placeholders, "?")
	}
	for _, column := range insertColumns {
		quotedInsert = append(quotedInsert, dialect.QuoteIdent(column))
	}
	quotedConflict := make([]string, 0, len(conflictColumns))
	for _, column := range conflictColumns {
		quotedConflict = append(quotedConflict, dialect.QuoteIdent(column))
	}
	updates := make([]string, 0, len(updateColumns))
	for _, column := range updateColumns {
		quoted := dialect.QuoteIdent(column)
		updates = append(updates, quoted+" = excluded."+quoted)
	}
	return "INSERT INTO " + dialect.QuoteIdent(table) + " (" + strings.Join(quotedInsert, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ") ON CONFLICT (" + strings.Join(quotedConflict, ", ") + ") DO UPDATE SET " + strings.Join(updates, ", ")
}
