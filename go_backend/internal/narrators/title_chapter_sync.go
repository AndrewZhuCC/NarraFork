package narrators

import (
	"context"
	"database/sql"
	"strings"

	"narrafork-go/internal/db"
)

// syncTitleToChapter mirrors the TypeScript syncTitleToChapter
// (server/services/narrator-title.ts): when a chapter-bound primary narrator's
// title changes, propagate it to the bound chapter's title. Only applies to
// type="primary" narrators with a chapterId, and skips the write when the
// chapter title already matches. Best-effort: any DB error is swallowed so a
// narrator title update is never failed by the chapter sync.
func (s *Service) syncTitleToChapter(ctx context.Context, narratorID, title string) {
	if s == nil || s.DB == nil || strings.TrimSpace(narratorID) == "" {
		return
	}
	var chapterID sql.NullString
	var narratorType string
	if err := s.DB.QueryRowContext(ctx, `SELECT chapter_id, type FROM narrators WHERE id = ?`, narratorID).Scan(&chapterID, &narratorType); err != nil {
		return
	}
	if narratorType != "primary" || !chapterID.Valid || strings.TrimSpace(chapterID.String) == "" {
		return
	}
	var current sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT title FROM chapters WHERE id = ?`, chapterID.String).Scan(&current); err != nil {
		return
	}
	if current.Valid && current.String == title {
		return // already in sync
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE chapters SET title = ?, updated_at = ? WHERE id = ?`, title, db.NowISO(), chapterID.String)
}

// SyncTitleToNarrator mirrors the TypeScript syncTitleToNarrator
// (server/services/narrator-title.ts), called from the chapter PATCH route when a
// chapter title changes: the chapter's primary-variant narrator's title is updated
// directly (NOT via the title PATCH path, to avoid a circular
// chapter<->narrator sync loop), then the same title_updated broadcast +
// recent_tabs sync that persistTitle performs are emitted. Best-effort.
func (s *Service) SyncTitleToNarrator(ctx context.Context, chapterID, title string) {
	if s == nil || s.DB == nil || strings.TrimSpace(chapterID) == "" {
		return
	}
	var narratorID string
	var current sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, title FROM narrators WHERE chapter_id = ? AND variant = 'primary' LIMIT 1`, chapterID).Scan(&narratorID, &current)
	if err != nil {
		return // no primary narrator (sql.ErrNoRows) or DB error -> skip
	}
	if current.Valid && current.String == title {
		return // already in sync
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrators SET title = ?, updated_at = ? WHERE id = ?`, title, db.NowISO(), narratorID); err != nil {
		return
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "title_updated", "narratorId": narratorID, "title": title})
	}
	// Mirror persistTitle's narrator:title_updated -> syncNarratorTitleToRecentTabs.
	s.syncTitleToRecentTabs(ctx, narratorID, title)
}
