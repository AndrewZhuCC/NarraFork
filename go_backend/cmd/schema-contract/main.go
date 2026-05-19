package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"narrafork-go/internal/db"
)

func main() {
	out := flag.String("out", "", "write canonical schema contract JSON to this path instead of stdout")
	flag.Parse()

	encoded, err := json.MarshalIndent(db.CanonicalSchemaContract(), "", "\t")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode canonical schema contract: %v\n", err)
		os.Exit(1)
	}
	encoded = append(encoded, '\n')

	if *out == "" {
		_, _ = os.Stdout.Write(encoded)
		return
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create output directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, encoded, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write canonical schema contract: %v\n", err)
		os.Exit(1)
	}
}
