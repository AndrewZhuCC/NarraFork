package database

import (
	"testing"
)

// TestMigrate_AllDialects verifies migrations apply cleanly and create the
// expected tables on every configured dialect (SQLite always; PostgreSQL
// when NARRAFORK_TEST_PG_DSN is set).
func TestMigrate_AllDialects(t *testing.T) {
	for _, dc := range TestDialects() {
		t.Run(dc.Name, func(t *testing.T) {
			db := OpenTestDB(t, dc)

			// Verify migrations recorded
			status, err := Status(db)
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			if len(status.Pending) != 0 {
				t.Errorf("expected no pending migrations, got %v", status.Pending)
			}
			if len(status.Applied) < 2 {
				t.Errorf("expected >=2 applied migrations, got %d", len(status.Applied))
			}

			// Verify a core table exists and is queryable
			var n int
			if err := db.QueryRow("SELECT count(*) FROM chapters").Scan(&n); err != nil {
				t.Errorf("query chapters: %v", err)
			}
			if err := db.QueryRow("SELECT count(*) FROM narrators").Scan(&n); err != nil {
				t.Errorf("query narrators: %v", err)
			}
			if err := db.QueryRow("SELECT count(*) FROM projects").Scan(&n); err != nil {
				t.Errorf("query projects: %v", err)
			}
		})
	}
}

// TestMigrate_Idempotent verifies running migrations twice is safe.
func TestMigrate_Idempotent(t *testing.T) {
	for _, dc := range TestDialects() {
		t.Run(dc.Name, func(t *testing.T) {
			db := OpenTestDB(t, dc)

			// Second migrate run should be a no-op (no error)
			if err := Migrate(db); err != nil {
				t.Fatalf("second migrate: %v", err)
			}

			status, err := Status(db)
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			if len(status.Pending) != 0 {
				t.Errorf("expected no pending after re-migrate, got %v", status.Pending)
			}
		})
	}
}

// TestCRUD_AllDialects verifies basic insert/select works across dialects
// using the dialect placeholder abstraction.
func TestCRUD_AllDialects(t *testing.T) {
	for _, dc := range TestDialects() {
		t.Run(dc.Name, func(t *testing.T) {
			db := OpenTestDB(t, dc)
			d := db.Dialect

			// Insert a project using dialect placeholders
			insertSQL := "INSERT INTO projects (id, name, status, created_at, updated_at) VALUES (" +
				d.Placeholder(1) + ", " + d.Placeholder(2) + ", " + d.Placeholder(3) + ", " +
				d.Placeholder(4) + ", " + d.Placeholder(5) + ")"
			now := FormatNow(d.Kind())
			if _, err := db.Exec(insertSQL, "p1", "Proj One", "active", now, now); err != nil {
				t.Fatalf("insert: %v", err)
			}

			// Read it back
			selectSQL := "SELECT name, status FROM projects WHERE id = " + d.Placeholder(1)
			var name, status string
			if err := db.QueryRow(selectSQL, "p1").Scan(&name, &status); err != nil {
				t.Fatalf("select: %v", err)
			}
			if name != "Proj One" || status != "active" {
				t.Errorf("got name=%q status=%q", name, status)
			}
		})
	}
}

// TestFTS_AllDialects verifies full-text search works on both backends.
func TestFTS_AllDialects(t *testing.T) {
	for _, dc := range TestDialects() {
		t.Run(dc.Name, func(t *testing.T) {
			db := OpenTestDB(t, dc)
			d := db.Dialect
			now := FormatNow(d.Kind())

			// Insert a project + chapter
			pSQL := "INSERT INTO projects (id, name, status, created_at, updated_at) VALUES (" +
				d.Placeholder(1) + "," + d.Placeholder(2) + ",'active'," + d.Placeholder(3) + "," + d.Placeholder(4) + ")"
			if _, err := db.Exec(pSQL, "p1", "P", now, now); err != nil {
				t.Fatalf("insert project: %v", err)
			}

			cSQL := "INSERT INTO chapters (id, project_id, title, description, status, role, branch, base_branch, created_at, updated_at) VALUES (" +
				d.Placeholder(1) + "," + d.Placeholder(2) + "," + d.Placeholder(3) + "," + d.Placeholder(4) +
				",'active','branch','feat/x','main'," + d.Placeholder(5) + "," + d.Placeholder(6) + ")"
			if _, err := db.Exec(cSQL, "c1", "p1", "Authentication system", "JWT refactor work", now, now); err != nil {
				t.Fatalf("insert chapter: %v", err)
			}

			// Search differs per dialect — verify the index found the row
			var found int
			switch d.Kind() {
			case DialectSQLite:
				err := db.QueryRow(
					"SELECT count(*) FROM chapters_fts WHERE chapters_fts MATCH ?", "authentication",
				).Scan(&found)
				if err != nil {
					t.Fatalf("sqlite fts query: %v", err)
				}
			case DialectPostgres:
				err := db.QueryRow(
					"SELECT count(*) FROM chapters WHERE search_vector @@ plainto_tsquery('simple', $1)", "authentication",
				).Scan(&found)
				if err != nil {
					t.Fatalf("pg fts query: %v", err)
				}
			}
			if found != 1 {
				t.Errorf("expected 1 FTS match, got %d", found)
			}
		})
	}
}
