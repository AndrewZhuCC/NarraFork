package database

import (
	"fmt"
	"strings"
)

// SQLiteDialect implements the Dialect interface for SQLite.
type SQLiteDialect struct{}

var _ Dialect = SQLiteDialect{}

func (SQLiteDialect) Kind() DialectKind { return DialectSQLite }

func (SQLiteDialect) Placeholder(_ int) string { return "?" }

func (SQLiteDialect) Placeholders(n int, _ int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n)[:n*2-1] // "?,?,?" for n=3
}

func (SQLiteDialect) BoolExpr(column string, value bool) string {
	if value {
		return column + " = 1"
	}
	return column + " = 0"
}

func (SQLiteDialect) NowExpr() string { return "datetime('now')" }

func (SQLiteDialect) JSONExtract(column, path string) string {
	return fmt.Sprintf("json_extract(%s, '$.%s')", column, path)
}

func (SQLiteDialect) JSONContains(column string, placeholder string) string {
	return fmt.Sprintf("EXISTS(SELECT 1 FROM json_each(%s) WHERE value = %s)", column, placeholder)
}

func (SQLiteDialect) Upsert(conflictTarget string, updates []string) string {
	setClauses := make([]string, len(updates))
	for i, col := range updates {
		setClauses[i] = col + " = excluded." + col
	}
	return fmt.Sprintf("ON CONFLICT(%s) DO UPDATE SET %s",
		conflictTarget, strings.Join(setClauses, ", "))
}

func (SQLiteDialect) LimitOffset(limit, offset int) string {
	return limitOffsetClause(limit, offset)
}

func (SQLiteDialect) ForUpdate() string { return "" } // SQLite has no row locking

func (SQLiteDialect) SupportsReturning() bool { return true } // SQLite 3.35+

func (SQLiteDialect) TableExists() string {
	return "SELECT 1 FROM sqlite_master WHERE type='table' AND name = ?"
}

func (SQLiteDialect) ConcatStrings(parts ...string) string {
	return strings.Join(parts, " || ")
}
