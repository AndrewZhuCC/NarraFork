package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type SQLiteSchemaSnapshot struct {
	Objects     []SQLiteSchemaObject       `json:"objects"`
	Tables      []SQLiteTableSnapshot      `json:"tables"`
	Indexes     []SQLiteIndexSnapshot      `json:"indexes"`
	ForeignKeys []SQLiteForeignKeySnapshot `json:"foreignKeys"`
	Hash        string                     `json:"hash,omitempty"`
}

type SQLiteSchemaObject struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Table string `json:"table"`
	SQL   string `json:"sql"`
}

type SQLiteTableSnapshot struct {
	Name    string                 `json:"name"`
	Columns []SQLiteColumnSnapshot `json:"columns"`
}

type SQLiteColumnSnapshot struct {
	CID          int     `json:"cid"`
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	NotNull      bool    `json:"notNull"`
	DefaultValue *string `json:"defaultValue,omitempty"`
	PK           int     `json:"pk"`
}

type SQLiteIndexSnapshot struct {
	Table   string   `json:"table"`
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Origin  string   `json:"origin"`
	Partial bool     `json:"partial"`
	Columns []string `json:"columns"`
}

type SQLiteForeignKeySnapshot struct {
	Table           string `json:"table"`
	ID              int    `json:"id"`
	Seq             int    `json:"seq"`
	ReferencedTable string `json:"referencedTable"`
	From            string `json:"from"`
	To              string `json:"to"`
	OnUpdate        string `json:"onUpdate"`
	OnDelete        string `json:"onDelete"`
	Match           string `json:"match"`
}

func SnapshotSQLiteSchema(ctx context.Context, sqlDB *sql.DB, ignoredObjects map[string]struct{}) (SQLiteSchemaSnapshot, error) {
	objects, err := snapshotSchemaObjects(ctx, sqlDB, ignoredObjects)
	if err != nil {
		return SQLiteSchemaSnapshot{}, err
	}
	tableNames := schemaSnapshotTableNames(objects)
	tables, err := snapshotTables(ctx, sqlDB, tableNames)
	if err != nil {
		return SQLiteSchemaSnapshot{}, err
	}
	indexes, err := snapshotIndexes(ctx, sqlDB, tableNames, ignoredObjects)
	if err != nil {
		return SQLiteSchemaSnapshot{}, err
	}
	foreignKeys, err := snapshotForeignKeys(ctx, sqlDB, tableNames)
	if err != nil {
		return SQLiteSchemaSnapshot{}, err
	}
	snapshot := SQLiteSchemaSnapshot{Objects: objects, Tables: tables, Indexes: indexes, ForeignKeys: foreignKeys}
	snapshot.Hash = HashSQLiteSchemaSnapshot(snapshot)
	return snapshot, nil
}

func HashSQLiteSchemaSnapshot(snapshot SQLiteSchemaSnapshot) string {
	copy := snapshot
	copy.Hash = ""
	encoded, err := json.Marshal(copy)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func DefaultSchemaSnapshotIgnoredObjects() map[string]struct{} {
	return map[string]struct{}{
		goMigrationJournalTable: {},
	}
}

func snapshotSchemaObjects(ctx context.Context, sqlDB *sql.DB, ignoredObjects map[string]struct{}) ([]SQLiteSchemaObject, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT type, name, tbl_name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		return nil, fmt.Errorf("list sqlite schema objects: %w", err)
	}
	defer rows.Close()

	objects := []SQLiteSchemaObject{}
	for rows.Next() {
		var objectType, name, tableName string
		var sqlText sql.NullString
		if err := rows.Scan(&objectType, &name, &tableName, &sqlText); err != nil {
			return nil, fmt.Errorf("scan sqlite schema object: %w", err)
		}
		if _, ignored := ignoredObjects[name]; ignored {
			continue
		}
		objects = append(objects, SQLiteSchemaObject{
			Type:  objectType,
			Name:  name,
			Table: tableName,
			SQL:   normalizeSnapshotSQL(sqlText.String),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite schema objects: %w", err)
	}
	return objects, nil
}

func schemaSnapshotTableNames(objects []SQLiteSchemaObject) []string {
	tables := []string{}
	for _, object := range objects {
		if object.Type == "table" || object.Type == "view" {
			tables = append(tables, object.Name)
		}
	}
	sort.Strings(tables)
	return tables
}

func snapshotTables(ctx context.Context, sqlDB *sql.DB, tableNames []string) ([]SQLiteTableSnapshot, error) {
	tables := []SQLiteTableSnapshot{}
	for _, table := range tableNames {
		rows, err := sqlDB.QueryContext(ctx, `PRAGMA table_info(`+quoteIdentifier(table)+`)`)
		if err != nil {
			return nil, fmt.Errorf("inspect table %s columns: %w", table, err)
		}
		columns := []SQLiteColumnSnapshot{}
		for rows.Next() {
			var cid int
			var name, typeValue string
			var notNull int
			var defaultValue sql.NullString
			var pk int
			if err := rows.Scan(&cid, &name, &typeValue, &notNull, &defaultValue, &pk); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan table %s column: %w", table, err)
			}
			column := SQLiteColumnSnapshot{CID: cid, Name: name, Type: typeValue, NotNull: notNull != 0, PK: pk}
			if defaultValue.Valid {
				value := defaultValue.String
				column.DefaultValue = &value
			}
			columns = append(columns, column)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate table %s columns: %w", table, err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close table %s columns: %w", table, err)
		}
		tables = append(tables, SQLiteTableSnapshot{Name: table, Columns: columns})
	}
	return tables, nil
}

func snapshotIndexes(ctx context.Context, sqlDB *sql.DB, tableNames []string, ignoredObjects map[string]struct{}) ([]SQLiteIndexSnapshot, error) {
	indexes := []SQLiteIndexSnapshot{}
	for _, table := range tableNames {
		rows, err := sqlDB.QueryContext(ctx, `PRAGMA index_list(`+quoteIdentifier(table)+`)`)
		if err != nil {
			return nil, fmt.Errorf("inspect table %s indexes: %w", table, err)
		}
		tableIndexes := []SQLiteIndexSnapshot{}
		for rows.Next() {
			var seq int
			var name string
			var unique int
			var origin string
			var partial int
			if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan table %s index: %w", table, err)
			}
			if _, ignored := ignoredObjects[name]; ignored {
				continue
			}
			if strings.HasPrefix(name, "sqlite_autoindex_") {
				continue
			}
			tableIndexes = append(tableIndexes, SQLiteIndexSnapshot{Table: table, Name: name, Unique: unique != 0, Origin: origin, Partial: partial != 0})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate table %s indexes: %w", table, err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close table %s indexes: %w", table, err)
		}
		for i := range tableIndexes {
			columns, err := indexColumns(ctx, sqlDB, tableIndexes[i].Name)
			if err != nil {
				return nil, fmt.Errorf("inspect index %s columns: %w", tableIndexes[i].Name, err)
			}
			tableIndexes[i].Columns = columns
			indexes = append(indexes, tableIndexes[i])
		}
	}
	sort.Slice(indexes, func(i, j int) bool {
		if indexes[i].Table != indexes[j].Table {
			return indexes[i].Table < indexes[j].Table
		}
		return indexes[i].Name < indexes[j].Name
	})
	return indexes, nil
}

func snapshotForeignKeys(ctx context.Context, sqlDB *sql.DB, tableNames []string) ([]SQLiteForeignKeySnapshot, error) {
	foreignKeys := []SQLiteForeignKeySnapshot{}
	for _, table := range tableNames {
		rows, err := sqlDB.QueryContext(ctx, `PRAGMA foreign_key_list(`+quoteIdentifier(table)+`)`)
		if err != nil {
			return nil, fmt.Errorf("inspect table %s foreign keys: %w", table, err)
		}
		for rows.Next() {
			var fk SQLiteForeignKeySnapshot
			fk.Table = table
			if err := rows.Scan(&fk.ID, &fk.Seq, &fk.ReferencedTable, &fk.From, &fk.To, &fk.OnUpdate, &fk.OnDelete, &fk.Match); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan table %s foreign key: %w", table, err)
			}
			foreignKeys = append(foreignKeys, fk)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate table %s foreign keys: %w", table, err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close table %s foreign keys: %w", table, err)
		}
	}
	sort.Slice(foreignKeys, func(i, j int) bool {
		left := foreignKeys[i]
		right := foreignKeys[j]
		if left.Table != right.Table {
			return left.Table < right.Table
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Seq < right.Seq
	})
	return foreignKeys, nil
}

func normalizeSnapshotSQL(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
