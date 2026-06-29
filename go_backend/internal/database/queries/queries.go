// Package queries provides hand-written SQL data access for NarraFork.
// Queries use the database.Dialect abstraction for placeholder differences
// so the same code works against both SQLite and PostgreSQL.
//
// This package deliberately avoids an ORM: queries are explicit SQL strings,
// scanning is explicit, and the dialect layer handles the small set of
// syntactic differences between backends.
package queries

import (
	"github.com/narrafork/narrafork/internal/database"
)

// Queries is the top-level data-access struct. It bundles the database
// handle and exposes per-entity query groups.
type Queries struct {
	db *database.DB

	Projects *ProjectQueries
	Chapters *ChapterQueries
}

// New creates a Queries bound to the given database.
func New(db *database.DB) *Queries {
	return &Queries{
		db:       db,
		Projects: &ProjectQueries{db: db},
		Chapters: &ChapterQueries{db: db},
	}
}

// ph is a short helper to build dialect placeholders $1.. or ?.
// Usage: q.db.Dialect.Placeholder(n)
