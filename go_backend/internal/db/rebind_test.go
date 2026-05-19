package db

import "testing"

func TestPostgresRebindConvertsPlaceholders(t *testing.T) {
	query := "SELECT * FROM projects WHERE id = ? AND name = ? LIMIT ?"
	got := PostgresDialect{}.Rebind(query)
	want := "SELECT * FROM projects WHERE id = $1 AND name = $2 LIMIT $3"
	if got != want {
		t.Fatalf("Rebind() = %q, want %q", got, want)
	}
}

func TestPostgresRebindSkipsQuotedQuestionMarksAndComments(t *testing.T) {
	query := "SELECT '?' AS literal, \"?column\" FROM projects -- ? comment\nWHERE id = ? AND note = 'it''s ? ok' /* ? block */ AND name = ?"
	got := PostgresDialect{}.Rebind(query)
	want := "SELECT '?' AS literal, \"?column\" FROM projects -- ? comment\nWHERE id = $1 AND note = 'it''s ? ok' /* ? block */ AND name = $2"
	if got != want {
		t.Fatalf("Rebind() = %q, want %q", got, want)
	}
}

func TestSQLiteRebindLeavesQueryUnchanged(t *testing.T) {
	query := "SELECT * FROM projects WHERE id = ?"
	if got := (SQLiteDialect{}).Rebind(query); got != query {
		t.Fatalf("SQLite Rebind() = %q, want %q", got, query)
	}
}

func TestPostgresDriverIsRegistered(t *testing.T) {
	if !sqlDriverRegistered(PostgresDialect{}.DriverName()) {
		t.Fatalf("postgres driver %q should be registered", PostgresDialect{}.DriverName())
	}
}

func TestDialectFactory(t *testing.T) {
	sqliteDialect, err := NewDialect(EngineSQLite)
	if err != nil {
		t.Fatalf("NewDialect(sqlite) returned error: %v", err)
	}
	if sqliteDialect.DriverName() != "sqlite" || sqliteDialect.Placeholder(1) != "?" {
		t.Fatalf("unexpected sqlite dialect: %#v", sqliteDialect)
	}
	postgresDialect, err := NewDialect(EnginePostgres)
	if err != nil {
		t.Fatalf("NewDialect(postgres) returned error: %v", err)
	}
	if postgresDialect.DriverName() != postgresRebindDriverName || postgresDialect.Placeholder(2) != "$2" {
		t.Fatalf("unexpected postgres dialect: %#v", postgresDialect)
	}
	if _, err := NewDialect(Engine("mysql")); err == nil {
		t.Fatal("NewDialect should reject unsupported engines")
	}
}

func TestQuoteIdentEscapesDoubleQuotes(t *testing.T) {
	if got, want := (SQLiteDialect{}).QuoteIdent(`bad"name`), `"bad""name"`; got != want {
		t.Fatalf("QuoteIdent() = %q, want %q", got, want)
	}
}
