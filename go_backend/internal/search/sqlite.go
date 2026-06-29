package search

import (
	"context"
	"fmt"

	"github.com/narrafork/narrafork/internal/database"
)

// SQLiteStore implements full-text search using FTS5 virtual tables.
// FTS5 indexes are maintained by SQLite triggers (defined in 0002_fts.sql),
// so IndexXxx/RemoveXxx are no-ops here — data flows through triggers.
type SQLiteStore struct {
	db *database.DB
}

// NewSQLiteStore creates an FTS5-based search store.
func NewSQLiteStore(db *database.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) Search(ctx context.Context, query string, opts Options) ([]Result, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	var results []Result

	// Determine which types to search
	searchTypes := opts.Types
	if len(searchTypes) == 0 {
		searchTypes = []string{"chapter", "narrator", "message"}
	}

	for _, typ := range searchTypes {
		switch typ {
		case "chapter":
			rows, err := s.searchChapters(ctx, query, opts.Limit)
			if err != nil {
				return nil, fmt.Errorf("search chapters: %w", err)
			}
			results = append(results, rows...)
		case "narrator":
			rows, err := s.searchNarrators(ctx, query, opts.Limit)
			if err != nil {
				return nil, fmt.Errorf("search narrators: %w", err)
			}
			results = append(results, rows...)
		case "message":
			rows, err := s.searchMessages(ctx, query, opts.Limit)
			if err != nil {
				return nil, fmt.Errorf("search messages: %w", err)
			}
			results = append(results, rows...)
		}
	}

	return results, nil
}

func (s *SQLiteStore) searchChapters(ctx context.Context, query string, limit int) ([]Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.title,
			snippet(chapters_fts, 0, '<b>', '</b>', '...', 32) as snip,
			rank
		FROM chapters_fts f
		JOIN chapters c ON c.rowid = f.rowid
		WHERE chapters_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		var rank float64
		if err := rows.Scan(&r.ID, &r.Title, &r.Snippet, &rank); err != nil {
			return nil, err
		}
		r.Type = "chapter"
		r.Score = -rank // FTS5 rank is negative (lower = better)
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *SQLiteStore) searchNarrators(ctx context.Context, query string, limit int) ([]Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.title,
			snippet(narrators_fts, 0, '<b>', '</b>', '...', 32) as snip,
			rank
		FROM narrators_fts f
		JOIN narrators n ON n.rowid = f.rowid
		WHERE narrators_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		var rank float64
		if err := rows.Scan(&r.ID, &r.Title, &r.Snippet, &rank); err != nil {
			return nil, err
		}
		r.Type = "narrator"
		r.Score = -rank
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *SQLiteStore) searchMessages(ctx context.Context, query string, limit int) ([]Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id,
			snippet(narrator_messages_fts, 0, '<b>', '</b>', '...', 64) as snip,
			rank
		FROM narrator_messages_fts f
		JOIN narrator_messages m ON m.rowid = f.rowid
		WHERE narrator_messages_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		var rank float64
		if err := rows.Scan(&r.ID, &r.Snippet, &rank); err != nil {
			return nil, err
		}
		r.Type = "message"
		r.Title = r.Snippet // Messages don't have titles
		r.Score = -rank
		results = append(results, r)
	}
	return results, rows.Err()
}

// IndexChapter is a no-op for SQLite — FTS5 triggers handle synchronization.
func (s *SQLiteStore) IndexChapter(_ context.Context, _, _, _ string) error { return nil }

// IndexNarrator is a no-op for SQLite — FTS5 triggers handle synchronization.
func (s *SQLiteStore) IndexNarrator(_ context.Context, _, _ string) error { return nil }

// IndexMessage is a no-op for SQLite — FTS5 triggers handle synchronization.
func (s *SQLiteStore) IndexMessage(_ context.Context, _, _ string) error { return nil }

// RemoveChapter is a no-op for SQLite — FTS5 triggers handle synchronization.
func (s *SQLiteStore) RemoveChapter(_ context.Context, _ string) error { return nil }

// RemoveNarrator is a no-op for SQLite — FTS5 triggers handle synchronization.
func (s *SQLiteStore) RemoveNarrator(_ context.Context, _ string) error { return nil }

// RemoveMessage is a no-op for SQLite — FTS5 triggers handle synchronization.
func (s *SQLiteStore) RemoveMessage(_ context.Context, _ string) error { return nil }
