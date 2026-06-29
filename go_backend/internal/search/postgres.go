package search

import (
	"context"
	"fmt"

	"github.com/narrafork/narrafork/internal/database"
)

// PostgresStore implements full-text search using tsvector + GIN indexes.
// Unlike SQLite FTS5 (trigger-maintained), PG search vectors must be
// explicitly updated via IndexXxx methods.
type PostgresStore struct {
	db *database.DB
}

// NewPostgresStore creates a tsvector-based search store.
func NewPostgresStore(db *database.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Search(ctx context.Context, query string, opts Options) ([]Result, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	var results []Result

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

func (s *PostgresStore) searchChapters(ctx context.Context, query string, limit int) ([]Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title,
			ts_headline('simple', COALESCE(title,'') || ' ' || COALESCE(description,''),
				plainto_tsquery('simple', $1),
				'StartSel=<b>, StopSel=</b>, MaxFragments=2') as snippet,
			ts_rank(search_vector, plainto_tsquery('simple', $1)) as score
		FROM chapters
		WHERE search_vector @@ plainto_tsquery('simple', $1)
		ORDER BY score DESC
		LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ID, &r.Title, &r.Snippet, &r.Score); err != nil {
			return nil, err
		}
		r.Type = "chapter"
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *PostgresStore) searchNarrators(ctx context.Context, query string, limit int) ([]Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title,
			ts_headline('simple', COALESCE(title,''),
				plainto_tsquery('simple', $1),
				'StartSel=<b>, StopSel=</b>') as snippet,
			ts_rank(search_vector, plainto_tsquery('simple', $1)) as score
		FROM narrators
		WHERE search_vector @@ plainto_tsquery('simple', $1)
		ORDER BY score DESC
		LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ID, &r.Title, &r.Snippet, &r.Score); err != nil {
			return nil, err
		}
		r.Type = "narrator"
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *PostgresStore) searchMessages(ctx context.Context, query string, limit int) ([]Result, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id,
			ts_headline('simple', COALESCE(content_text,''),
				plainto_tsquery('simple', $1),
				'StartSel=<b>, StopSel=</b>, MaxFragments=2, MaxWords=30') as snippet,
			ts_rank(search_vector, plainto_tsquery('simple', $1)) as score
		FROM narrator_messages
		WHERE search_vector @@ plainto_tsquery('simple', $1)
		ORDER BY score DESC
		LIMIT $2`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ID, &r.Snippet, &r.Score); err != nil {
			return nil, err
		}
		r.Type = "message"
		r.Title = r.Snippet
		results = append(results, r)
	}
	return results, rows.Err()
}

// IndexChapter is a no-op — PostgreSQL triggers (0002_fts.sql) maintain
// search_vector automatically on INSERT/UPDATE, mirroring SQLite's FTS5 triggers.
func (s *PostgresStore) IndexChapter(_ context.Context, _, _, _ string) error { return nil }

// IndexNarrator is a no-op — maintained by trigger.
func (s *PostgresStore) IndexNarrator(_ context.Context, _, _ string) error { return nil }

// IndexMessage is a no-op — maintained by trigger.
func (s *PostgresStore) IndexMessage(_ context.Context, _, _ string) error { return nil }

func (s *PostgresStore) RemoveChapter(_ context.Context, _ string) error {
	return nil // Row deletion removes the vector automatically
}

func (s *PostgresStore) RemoveNarrator(_ context.Context, _ string) error {
	return nil
}

func (s *PostgresStore) RemoveMessage(_ context.Context, _ string) error {
	return nil
}
