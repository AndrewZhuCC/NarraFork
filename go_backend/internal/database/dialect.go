package database

import "fmt"

// DialectKind identifies the database backend.
type DialectKind string

const (
	DialectSQLite   DialectKind = "sqlite"
	DialectPostgres DialectKind = "postgres"
)

// Dialect abstracts SQL differences between SQLite and PostgreSQL.
// The interface is intentionally minimal — most NarraFork queries are
// standard SQL that works on both. Only the known divergence points
// are abstracted here.
type Dialect interface {
	// Kind returns the dialect identifier.
	Kind() DialectKind

	// Placeholder returns the parameter placeholder for position n (1-based).
	// SQLite: always "?"
	// PostgreSQL: "$1", "$2", ...
	Placeholder(n int) string

	// Placeholders returns n comma-separated placeholders starting from offset.
	// Useful for IN clauses or multi-value INSERTs.
	// Example (PG, n=3, offset=2): "$2, $3, $4"
	Placeholders(n int, offset int) string

	// BoolExpr returns a boolean comparison expression.
	// SQLite stores booleans as INTEGER 0/1.
	// PostgreSQL has native BOOLEAN.
	// Example: BoolExpr("is_root", true) → "is_root = 1" (sqlite) / "is_root = true" (pg)
	BoolExpr(column string, value bool) string

	// NowExpr returns the SQL expression for current timestamp.
	// SQLite: "datetime('now')"
	// PostgreSQL: "NOW()"
	NowExpr() string

	// JSONExtract returns the SQL expression to extract a text value from a JSON column.
	// SQLite: json_extract(col, '$.path')
	// PostgreSQL: col->>'path'
	JSONExtract(column, path string) string

	// JSONContains returns a boolean expression checking if a JSON array contains a value.
	// SQLite: EXISTS(SELECT 1 FROM json_each(col) WHERE value = ?)
	// PostgreSQL: col::jsonb @> '["value"]'::jsonb
	JSONContains(column string, placeholder string) string

	// Upsert returns the ON CONFLICT clause for an upsert operation.
	// conflictTarget: column(s) that define the conflict (e.g. "id" or "narrator_id, file_path")
	// updates: column names to update on conflict
	// startPlaceholder: the placeholder offset for update values (for PG numbering)
	Upsert(conflictTarget string, updates []string) string

	// LimitOffset returns the LIMIT/OFFSET clause.
	// Both dialects use the same syntax, but this ensures consistency.
	LimitOffset(limit, offset int) string

	// ForUpdate returns "FOR UPDATE" (PG) or "" (SQLite, no row locking).
	ForUpdate() string

	// SupportsReturning reports whether the dialect supports RETURNING clauses.
	// SQLite 3.35+ and PostgreSQL both do.
	SupportsReturning() bool

	// TableExists returns a query that checks if a table exists.
	// Returns a query string; bind the table name as the first parameter.
	TableExists() string

	// ConcatStrings returns the SQL expression to concatenate string expressions.
	// Both use || but this ensures it's explicit.
	ConcatStrings(parts ...string) string
}

// placeholders is a helper to generate n placeholders from offset using a format function.
func placeholders(n int, offset int, fmtFn func(int) string) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmtFn(offset + i)
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += ", " + parts[i]
	}
	return result
}

// limitOffsetClause generates the standard LIMIT/OFFSET SQL.
func limitOffsetClause(limit, offset int) string {
	if limit <= 0 && offset <= 0 {
		return ""
	}
	if offset <= 0 {
		return fmt.Sprintf("LIMIT %d", limit)
	}
	if limit <= 0 {
		return fmt.Sprintf("OFFSET %d", offset)
	}
	return fmt.Sprintf("LIMIT %d OFFSET %d", limit, offset)
}
