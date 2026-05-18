package db

import "context"

func (s *Store) EnsureSchema(ctx context.Context) error {
	// Compatibility rule: the Go backend must use the database created by the
	// original TypeScript/Bun backend. Schema creation/migration is intentionally
	// disabled here to avoid mutating ~/.narrafork/narrafork.db.
	return nil
}
