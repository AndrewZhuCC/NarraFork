package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestStoreRebindDelegatesToDialect(t *testing.T) {
	store := &Store{Dialect: PostgresDialect{}}
	got := store.Rebind("SELECT * FROM projects WHERE id = ? AND name = ?")
	want := "SELECT * FROM projects WHERE id = $1 AND name = $2"
	if got != want {
		t.Fatalf("Store.Rebind() = %q, want %q", got, want)
	}
}

func TestStoreImplementsDBTXAndBeginner(t *testing.T) {
	var _ DBTX = (*Store)(nil)
	var _ Beginner = (*Store)(nil)
}

func TestStoreExecQueryAndBeginTxUseUnderlyingSQLite(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	store := &Store{SQL: sqlDB, Engine: EngineSQLite, Dialect: SQLiteDialect{}}
	if _, err := store.ExecContext(ctx, `CREATE TABLE items (id TEXT PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table through store: %v", err)
	}
	tx, err := store.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx through store: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO items (id, name) VALUES (?, ?)`, "i1", "Item 1"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert through tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
	rows, err := store.QueryContext(ctx, `SELECT id FROM items WHERE name = ?`, "Item 1")
	if err != nil {
		t.Fatalf("query through store: %v", err)
	}
	if !rows.Next() {
		t.Fatal("expected one row")
	}
	var id string
	if err := rows.Scan(&id); err != nil {
		t.Fatalf("scan id: %v", err)
	}
	if id != "i1" {
		t.Fatalf("id = %q, want i1", id)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close rows: %v", err)
	}
	var count int
	if err := store.QueryRowContext(ctx, `SELECT COUNT(*) FROM items`).Scan(&count); err != nil {
		t.Fatalf("query row through store: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}
