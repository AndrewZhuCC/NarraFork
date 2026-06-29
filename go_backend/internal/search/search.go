// Package search defines the full-text search interface that abstracts
// SQLite FTS5 and PostgreSQL tsvector implementations.
package search

import "context"

// Result represents a single search hit.
type Result struct {
	ID      string  // entity ID
	Type    string  // "chapter", "narrator", "message"
	Title   string  // display title or snippet
	Snippet string  // highlighted match context
	Score   float64 // relevance score (higher = better)
}

// Options controls search behavior.
type Options struct {
	Limit  int      // max results per type (default 20)
	Offset int      // pagination offset
	Types  []string // filter by entity type; empty = all
}

// Store is the full-text search interface.
// Implementations: SQLite FTS5 (internal/search/sqlite.go)
//
//	PostgreSQL tsvector (internal/search/postgres.go)
type Store interface {
	// Search performs a full-text search across indexed entities.
	Search(ctx context.Context, query string, opts Options) ([]Result, error)

	// IndexChapter updates the search index for a chapter.
	IndexChapter(ctx context.Context, id, title, description string) error

	// IndexNarrator updates the search index for a narrator.
	IndexNarrator(ctx context.Context, id, title string) error

	// IndexMessage updates the search index for a message.
	IndexMessage(ctx context.Context, id, contentText string) error

	// RemoveChapter removes a chapter from the search index.
	RemoveChapter(ctx context.Context, id string) error

	// RemoveNarrator removes a narrator from the search index.
	RemoveNarrator(ctx context.Context, id string) error

	// RemoveMessage removes a message from the search index.
	RemoveMessage(ctx context.Context, id string) error
}
