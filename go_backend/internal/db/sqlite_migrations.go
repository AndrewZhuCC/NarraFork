package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

const drizzleMigrationsTable = "__drizzle_migrations"

type SQLiteDrizzleMigration struct {
	Tag       string
	Name      string
	When      int64
	CreatedAt int64
	SQL       string
	Content   string
}

func splitDrizzleStatements(sqlText string) []string {
	parts := strings.Split(sqlText, "--> statement-breakpoint")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt != "" {
			statements = append(statements, stmt)
		}
	}
	return statements
}

func hashMigrationSQL(sqlText string) string {
	sum := sha256.Sum256([]byte(sqlText))
	return hex.EncodeToString(sum[:])
}

func isSQLiteAlreadyAppliedError(err error) bool {
	if err == nil {
		return false
	}
	return containsSQLiteAlreadyAppliedMessage(err)
}

func containsSQLiteAlreadyAppliedMessage(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "already exists") || strings.Contains(msg, "duplicate column name") {
		return true
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		return containsSQLiteAlreadyAppliedMessage(unwrapped)
	}
	type multiUnwrapper interface {
		Unwrap() []error
	}
	if wrapped, ok := err.(multiUnwrapper); ok {
		for _, child := range wrapped.Unwrap() {
			if containsSQLiteAlreadyAppliedMessage(child) {
				return true
			}
		}
	}
	return false
}

func RunBunCompatibleDrizzleMigrations(ctx context.Context, sqlDB *sql.DB, migrations any) error {
	if sqlDB == nil {
		return fmt.Errorf("run SQLite Drizzle migrations: database is not open")
	}

	normalized, err := normalizeSQLiteDrizzleMigrations(migrations)
	if err != nil {
		return err
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite Drizzle migration transaction: %w", err)
	}
	defer tx.Rollback()

	if err := ensureBunCompatibleDrizzleJournal(ctx, tx); err != nil {
		return err
	}

	for _, migration := range normalized {
		hash := hashMigrationSQL(migration.SQL)
		applied, err := drizzleMigrationHashApplied(ctx, tx, hash)
		if err != nil {
			return fmt.Errorf("check Drizzle migration %s: %w", migrationLabel(migration), err)
		}
		if applied {
			continue
		}

		for _, stmt := range splitDrizzleStatements(migration.SQL) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				if isSQLiteAlreadyAppliedError(err) {
					continue
				}
				return fmt.Errorf("run Drizzle migration %s statement: %w", migrationLabel(migration), err)
			}
		}

		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO __drizzle_migrations (hash, created_at) VALUES (?, ?)`,
			hash,
			migration.When,
		); err != nil {
			return fmt.Errorf("stamp Drizzle migration %s: %w", migrationLabel(migration), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite Drizzle migrations: %w", err)
	}
	return nil
}

func ensureBunCompatibleDrizzleJournal(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS __drizzle_migrations (
		id SERIAL PRIMARY KEY,
		hash text NOT NULL,
		created_at numeric
	)`); err != nil {
		return fmt.Errorf("create __drizzle_migrations journal: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(__drizzle_migrations)`)
	if err != nil {
		return fmt.Errorf("inspect __drizzle_migrations journal: %w", err)
	}
	defer rows.Close()

	columns := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name string
		var typeValue string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typeValue, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("scan __drizzle_migrations journal column: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate __drizzle_migrations journal columns: %w", err)
	}
	for _, column := range []string{"hash", "created_at"} {
		if _, ok := columns[column]; !ok {
			return fmt.Errorf("__drizzle_migrations.%s column is missing", column)
		}
	}
	return nil
}

func drizzleMigrationHashApplied(ctx context.Context, tx *sql.Tx, hash string) (bool, error) {
	var one int
	err := tx.QueryRowContext(
		ctx,
		`SELECT 1 FROM __drizzle_migrations WHERE hash = ? LIMIT 1`,
		hash,
	).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func migrationLabel(migration SQLiteDrizzleMigration) string {
	if migration.Tag != "" {
		return migration.Tag
	}
	return hashMigrationSQL(migration.SQL)
}

func normalizeSQLiteDrizzleMigrations(migrations any) ([]SQLiteDrizzleMigration, error) {
	if migrations == nil {
		return nil, nil
	}
	value := reflect.ValueOf(migrations)
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return nil, fmt.Errorf("SQLite Drizzle migrations must be a slice, got %T", migrations)
	}

	out := make([]SQLiteDrizzleMigration, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		migration, err := normalizeSQLiteDrizzleMigration(value.Index(i))
		if err != nil {
			return nil, fmt.Errorf("normalize SQLite Drizzle migration %d: %w", i, err)
		}
		out = append(out, migration)
	}
	return out, nil
}

func normalizeSQLiteDrizzleMigration(value reflect.Value) (SQLiteDrizzleMigration, error) {
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return SQLiteDrizzleMigration{}, fmt.Errorf("migration is nil")
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return SQLiteDrizzleMigration{}, fmt.Errorf("migration must be a struct, got %s", value.Kind())
	}

	sqlText, ok := migrationSQLField(value)
	if !ok {
		return SQLiteDrizzleMigration{}, fmt.Errorf("migration SQL field is missing")
	}
	tag, _ := firstNonEmptyStringField(value, "Tag", "Name")
	when, _ := firstNonZeroInt64Field(value, "When", "CreatedAt", "Created_at")
	return SQLiteDrizzleMigration{Tag: tag, When: when, SQL: sqlText}, nil
}

func migrationSQLField(value reflect.Value) (string, bool) {
	if sqlText, ok := stringField(value, "SQL"); ok && sqlText != "" {
		return sqlText, true
	}
	if content, ok := stringField(value, "Content"); ok {
		return content, true
	}
	return stringField(value, "SQL")
}

func firstNonEmptyStringField(value reflect.Value, names ...string) (string, bool) {
	var fallback string
	found := false
	for _, name := range names {
		fieldValue, ok := stringField(value, name)
		if !ok {
			continue
		}
		if !found {
			fallback = fieldValue
			found = true
		}
		if fieldValue != "" {
			return fieldValue, true
		}
	}
	return fallback, found
}

func firstNonZeroInt64Field(value reflect.Value, names ...string) (int64, bool) {
	var fallback int64
	found := false
	for _, name := range names {
		fieldValue, ok := int64Field(value, name)
		if !ok {
			continue
		}
		if !found {
			fallback = fieldValue
			found = true
		}
		if fieldValue != 0 {
			return fieldValue, true
		}
	}
	return fallback, found
}

func stringField(value reflect.Value, names ...string) (string, bool) {
	for _, name := range names {
		field := value.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		switch field.Kind() {
		case reflect.String:
			return field.String(), true
		case reflect.Slice:
			if field.Type().Elem().Kind() == reflect.Uint8 {
				return string(field.Bytes()), true
			}
		}
	}
	return "", false
}

func int64Field(value reflect.Value, names ...string) (int64, bool) {
	for _, name := range names {
		field := value.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return field.Int(), true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return int64(field.Uint()), true
		}
	}
	return 0, false
}
