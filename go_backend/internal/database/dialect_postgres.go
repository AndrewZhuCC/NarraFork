package database

import (
	"fmt"
	"strings"
)

// PostgresDialect implements the Dialect interface for PostgreSQL.
type PostgresDialect struct{}

var _ Dialect = PostgresDialect{}

func (PostgresDialect) Kind() DialectKind { return DialectPostgres }

func (PostgresDialect) Placeholder(n int) string {
	return fmt.Sprintf("$%d", n)
}

func (PostgresDialect) Placeholders(n int, offset int) string {
	return placeholders(n, offset, func(i int) string {
		return fmt.Sprintf("$%d", i)
	})
}

func (PostgresDialect) BoolExpr(column string, value bool) string {
	if value {
		return column + " = true"
	}
	return column + " = false"
}

func (PostgresDialect) NowExpr() string { return "NOW()" }

func (PostgresDialect) JSONExtract(column, path string) string {
	// PostgreSQL JSONB extraction as text
	return fmt.Sprintf("%s->>'%s'", column, path)
}

func (PostgresDialect) JSONContains(column string, placeholder string) string {
	// PostgreSQL JSONB array membership. The placeholder binds a single bare
	// value (same contract as the SQLite dialect). jsonb_build_array wraps it
	// so the @> containment check stays parameterized and injection-safe.
	return fmt.Sprintf("%s::jsonb @> jsonb_build_array(%s)", column, placeholder)
}

func (PostgresDialect) Upsert(conflictTarget string, updates []string) string {
	setClauses := make([]string, len(updates))
	for i, col := range updates {
		setClauses[i] = col + " = EXCLUDED." + col
	}
	return fmt.Sprintf("ON CONFLICT(%s) DO UPDATE SET %s",
		conflictTarget, strings.Join(setClauses, ", "))
}

func (PostgresDialect) LimitOffset(limit, offset int) string {
	return limitOffsetClause(limit, offset)
}

func (PostgresDialect) ForUpdate() string { return "FOR UPDATE" }

func (PostgresDialect) SupportsReturning() bool { return true }

func (PostgresDialect) TableExists() string {
	return "SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1"
}

func (PostgresDialect) ConcatStrings(parts ...string) string {
	return strings.Join(parts, " || ")
}
