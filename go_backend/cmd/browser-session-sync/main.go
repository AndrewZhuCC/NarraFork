package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"narrafork-go/internal/db"
	"narrafork-go/internal/narrators"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("browser-session-sync", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var direction string
	var sqlitePath string
	flags.StringVar(&direction, "direction", "export", "sync direction: export (main -> runtime) or rollback (runtime -> main)")
	flags.StringVar(&sqlitePath, "sqlite-path", "", "optional SQLite database path override")
	if err := flags.Parse(args); err != nil {
		return err
	}
	direction = strings.ToLower(strings.TrimSpace(direction))
	if direction != "export" && direction != "rollback" {
		return fmt.Errorf("invalid --direction %q: want export or rollback", direction)
	}
	input := db.ConfigInput{
		VerifyCanonical: ptr(false),
		TSOwnedSchema:   ptr(false),
	}
	if strings.TrimSpace(sqlitePath) != "" {
		input.SQLitePath = strings.TrimSpace(sqlitePath)
	}
	store, err := db.NewConfig(input)
	if err != nil {
		return err
	}
	opened, err := db.OpenWithConfig(ctx, store)
	if err != nil {
		return err
	}
	defer opened.Close()
	if opened.RuntimeSQL == nil {
		return fmt.Errorf("runtime sidecar database is not available")
	}

	src := opened.SQL
	dst := opened.RuntimeSQL
	if direction == "rollback" {
		src, dst = opened.RuntimeSQL, opened.SQL
	}
	count, err := narrators.CopyBrowserSessionStorage(ctx, src, dst)
	if err != nil {
		return err
	}
	fmt.Printf("copied %d browser session rows (%s -> %s)\n", count, directionSourceLabel(direction), directionTargetLabel(direction))
	fmt.Printf("source preserved as backup: %s\n", directionBackupLabel(direction, opened))
	return nil
}

func ptr[T any](value T) *T { return &value }

func directionSourceLabel(direction string) string {
	if direction == "rollback" {
		return "runtime"
	}
	return "main"
}

func directionTargetLabel(direction string) string {
	if direction == "rollback" {
		return "main"
	}
	return "runtime"
}

func directionBackupLabel(direction string, store *db.Store) string {
	if direction == "rollback" {
		return store.RuntimePath
	}
	return store.Path
}
