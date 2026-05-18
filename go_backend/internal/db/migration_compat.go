package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	goMigrationJournalTable           = "__narrafork_go_migration_journal"
	goMigrationJournalContractVersion = 1
)

var ErrMigrationCompatibilityMismatch = errors.New("TS migration compatibility mismatch")

type migrationCompatibilityDetails struct {
	DrizzleJournalTable string   `json:"drizzleJournalTable"`
	GoJournalTable      string   `json:"goJournalTable"`
	ObservedCount       int      `json:"observedCount"`
	ExpectedCount       int      `json:"expectedCount"`
	ExpectedLastTag     string   `json:"expectedLastTag"`
	ExpectedLastWhen    int64    `json:"expectedLastWhen"`
	ExtraHashes         []string `json:"extraHashes,omitempty"`
}

func (s *Store) EnsureMigrationCompatibility(ctx context.Context) error {
	if s == nil || s.SQL == nil {
		return fmt.Errorf("%w: database is not open", ErrMigrationCompatibilityMismatch)
	}
	return EnsureMigrationCompatibility(ctx, s.SQL)
}

// EnsureMigrationCompatibility verifies that the existing TS-owned Drizzle
// migration journal contains every migration hash embedded into this Go build.
// It never executes Drizzle SQL and only writes the Go-owned compatibility
// journal table after the canonical schema verifier has already passed.
func EnsureMigrationCompatibility(ctx context.Context, sqlDB *sql.DB) error {
	if sqlDB == nil {
		return fmt.Errorf("%w: database is not open", ErrMigrationCompatibilityMismatch)
	}
	if len(embeddedDrizzleMigrationSnapshot) == 0 {
		return fmt.Errorf("%w: no embedded Drizzle migration snapshot", ErrMigrationCompatibilityMismatch)
	}

	appliedHashes, err := readAppliedDrizzleMigrationHashes(ctx, sqlDB)
	if err != nil {
		return err
	}
	missing := missingExpectedDrizzleMigrations(appliedHashes)
	if len(missing) > 0 {
		return fmt.Errorf(
			"%w: missing expected Drizzle migrations: %s. Start the TypeScript backend once to run migrations before using go_backend",
			ErrMigrationCompatibilityMismatch,
			strings.Join(missing, ", "),
		)
	}

	extraHashes := extraAppliedDrizzleHashes(appliedHashes)
	last := embeddedDrizzleMigrationSnapshot[len(embeddedDrizzleMigrationSnapshot)-1]
	details := migrationCompatibilityDetails{
		DrizzleJournalTable: "__drizzle_migrations",
		GoJournalTable:      goMigrationJournalTable,
		ObservedCount:       len(appliedHashes),
		ExpectedCount:       len(embeddedDrizzleMigrationSnapshot),
		ExpectedLastTag:     last.Tag,
		ExpectedLastWhen:    last.When,
		ExtraHashes:         extraHashes,
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal Go migration compatibility journal details: %w", err)
	}

	return writeGoMigrationJournal(ctx, sqlDB, last, string(detailsJSON))
}

func readAppliedDrizzleMigrationHashes(ctx context.Context, sqlDB *sql.DB) (map[string]struct{}, error) {
	exists, columns, err := tableColumns(ctx, sqlDB, "__drizzle_migrations")
	if err != nil {
		return nil, fmt.Errorf("%w: inspect __drizzle_migrations: %v", ErrMigrationCompatibilityMismatch, err)
	}
	if !exists {
		return nil, fmt.Errorf(
			"%w: missing __drizzle_migrations table. Start the TypeScript backend once to run Drizzle migrations before using go_backend",
			ErrMigrationCompatibilityMismatch,
		)
	}
	if _, ok := columns["hash"]; !ok {
		return nil, fmt.Errorf("%w: __drizzle_migrations.hash column is missing", ErrMigrationCompatibilityMismatch)
	}

	rows, err := sqlDB.QueryContext(ctx, `SELECT hash FROM __drizzle_migrations`)
	if err != nil {
		return nil, fmt.Errorf("%w: read __drizzle_migrations: %v", ErrMigrationCompatibilityMismatch, err)
	}
	defer rows.Close()

	hashes := map[string]struct{}{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, fmt.Errorf("%w: scan __drizzle_migrations.hash: %v", ErrMigrationCompatibilityMismatch, err)
		}
		hash = strings.TrimSpace(hash)
		if hash != "" {
			hashes[hash] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate __drizzle_migrations: %v", ErrMigrationCompatibilityMismatch, err)
	}
	return hashes, nil
}

func missingExpectedDrizzleMigrations(appliedHashes map[string]struct{}) []string {
	missing := []string{}
	for _, expected := range embeddedDrizzleMigrationSnapshot {
		if _, ok := appliedHashes[expected.Hash]; !ok {
			missing = append(missing, expected.Tag+"("+expected.Hash+")")
		}
	}
	return missing
}

func extraAppliedDrizzleHashes(appliedHashes map[string]struct{}) []string {
	expected := map[string]struct{}{}
	for _, entry := range embeddedDrizzleMigrationSnapshot {
		expected[entry.Hash] = struct{}{}
	}
	extra := []string{}
	for hash := range appliedHashes {
		if _, ok := expected[hash]; !ok {
			extra = append(extra, hash)
		}
	}
	sort.Strings(extra)
	return extra
}

func createGoMigrationJournal(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS __narrafork_go_migration_journal (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		contract_version INTEGER NOT NULL,
		expected_count INTEGER NOT NULL,
		expected_last_tag TEXT NOT NULL,
		expected_last_when INTEGER NOT NULL,
		expected_journal_hash TEXT NOT NULL,
		canonical_schema_hash TEXT NOT NULL,
		status TEXT NOT NULL,
		details_json TEXT NOT NULL,
		verified_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create Go migration compatibility journal: %w", err)
	}
	return nil
}

func writeGoMigrationJournal(
	ctx context.Context,
	sqlDB *sql.DB,
	last drizzleMigrationSnapshotEntry,
	detailsJSON string,
) error {
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Go migration compatibility journal transaction: %w", err)
	}
	defer tx.Rollback()

	if err := createGoMigrationJournal(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO __narrafork_go_migration_journal (
			id,
			contract_version,
			expected_count,
			expected_last_tag,
			expected_last_when,
			expected_journal_hash,
			canonical_schema_hash,
			status,
			details_json,
			verified_at
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			contract_version = excluded.contract_version,
			expected_count = excluded.expected_count,
			expected_last_tag = excluded.expected_last_tag,
			expected_last_when = excluded.expected_last_when,
			expected_journal_hash = excluded.expected_journal_hash,
			canonical_schema_hash = excluded.canonical_schema_hash,
			status = excluded.status,
			details_json = excluded.details_json,
			verified_at = excluded.verified_at
	`,
		goMigrationJournalContractVersion,
		len(embeddedDrizzleMigrationSnapshot),
		last.Tag,
		last.When,
		expectedDrizzleJournalHash(),
		canonicalSchemaContractHash(),
		"compatible",
		detailsJSON,
		NowISO(),
	); err != nil {
		return fmt.Errorf("write Go migration compatibility journal: %w", err)
	}
	return tx.Commit()
}

func expectedDrizzleJournalHash() string {
	encoded, err := json.Marshal(embeddedDrizzleMigrationSnapshot)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
