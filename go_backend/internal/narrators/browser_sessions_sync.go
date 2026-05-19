package narrators

import (
	"context"
	"database/sql"
	"strings"
)

func CopyBrowserSessionStorage(ctx context.Context, src *sql.DB, dst *sql.DB) (int, error) {
	return syncBrowserSessionStorage(ctx, src, dst, true)
}

func syncBrowserSessionStorage(ctx context.Context, src *sql.DB, dst *sql.DB, replace bool) (int, error) {
	if src == nil || dst == nil || src == dst {
		return 0, nil
	}
	if err := (&Service{RuntimeDB: dst, BrowserSessionPersistenceEnabled: true}).EnsureBrowserSessionStorage(ctx); err != nil {
		return 0, err
	}
	rows, err := src.QueryContext(ctx, `SELECT * FROM narrator_browser_sessions`)
	if err != nil {
		if isMissingBrowserSessionTable(err) {
			return 0, nil
		}
		return 0, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	if len(columns) == 0 {
		return 0, nil
	}
	tx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if replace {
		if _, err := tx.ExecContext(ctx, `DELETE FROM narrator_browser_sessions`); err != nil {
			return 0, err
		}
	}
	placeholders := make([]string, len(columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	stmt := `INSERT INTO narrator_browser_sessions (` + strings.Join(columns, ", ") + `) VALUES (` + strings.Join(placeholders, ", ") + `)`
	count := 0
	for rows.Next() {
		values := make([]any, len(columns))
		scans := make([]any, len(columns))
		for i := range scans {
			scans[i] = &values[i]
		}
		if err := rows.Scan(scans...); err != nil {
			return 0, err
		}
		for i, value := range values {
			if raw, ok := value.([]byte); ok {
				values[i] = string(raw)
			}
		}
		if _, err := tx.ExecContext(ctx, stmt, values...); err != nil {
			return 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
