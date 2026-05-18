package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"narrafork-go/internal/db"

	_ "modernc.org/sqlite"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dbPath := flag.String("db", "", "SQLite database path")
	outPath := flag.String("out", "", "Output JSON path")
	hashPath := flag.String("hash-out", "", "Output sha256 hash path")
	flag.Parse()
	if *dbPath == "" || *outPath == "" {
		return fmt.Errorf("usage: schema-snapshot -db <path> -out <path> [-hash-out <path>]")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	sqlDB, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	snapshot, err := db.SnapshotSQLiteSchema(ctx, sqlDB, db.DefaultSchemaSnapshotIgnoredObjects())
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(snapshot, "", "\t")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(*outPath, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	if *hashPath != "" {
		if err := os.WriteFile(*hashPath, []byte(snapshot.Hash+"\n"), 0o644); err != nil {
			return fmt.Errorf("write hash: %w", err)
		}
	}
	fmt.Println(snapshot.Hash)
	return nil
}
