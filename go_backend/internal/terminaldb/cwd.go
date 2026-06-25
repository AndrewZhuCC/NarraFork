package terminaldb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
)

var (
	ErrTargetRequired   = errors.New("chapterId or narratorId is required")
	ErrTargetAmbiguous  = errors.New("exactly one of chapterId or narratorId is required")
	ErrChapterNotFound  = errors.New("chapter not found")
	ErrNarratorNotFound = errors.New("narrator not found")
	// ErrChapterDormant mirrors the TS terminal-service.ts:238 ValidationError
	// message verbatim ("Chapter has no worktree (dormant?)", capital C). It is
	// surfaced to the user verbatim via httpx.BadRequest(err.Error()) (HTTP 400)
	// in terminals/handlers.go terminalCwdError, so the casing must match TS.
	ErrChapterDormant = errors.New("Chapter has no worktree (dormant?)")
)

// ResolveCwd mirrors the TypeScript terminalService.create cwd resolution order.
// It never creates or mutates schema; it only reads the existing TS-owned tables.
func ResolveCwd(ctx context.Context, sqlDB *sql.DB, chapterID, narratorID string) (sql.NullString, error) {
	chapterID = strings.TrimSpace(chapterID)
	narratorID = strings.TrimSpace(narratorID)
	if chapterID == "" && narratorID == "" {
		return sql.NullString{}, ErrTargetRequired
	}
	if chapterID != "" && narratorID != "" {
		return sql.NullString{}, ErrTargetAmbiguous
	}
	if chapterID != "" {
		return resolveChapterCwd(ctx, sqlDB, chapterID)
	}
	return resolveNarratorCwd(ctx, sqlDB, narratorID)
}

// ResolveCreateCwd mirrors the TypeScript terminalService.create branch exactly,
// which differs from ResolveCwd in the neither/both cases: providing BOTH a
// chapterId and narratorId is rejected ("Only one of chapterId or narratorId may
// be provided"), while providing NEITHER is allowed and yields a standalone
// terminal rooted at the home directory (used by global flows such as the setup
// wizard). The tab/view-state/tool paths keep using ResolveCwd, which requires
// exactly one target.
func ResolveCreateCwd(ctx context.Context, sqlDB *sql.DB, chapterID, narratorID string) (sql.NullString, error) {
	chapterID = strings.TrimSpace(chapterID)
	narratorID = strings.TrimSpace(narratorID)
	if chapterID != "" && narratorID != "" {
		return sql.NullString{}, ErrTargetAmbiguous
	}
	if chapterID != "" {
		return resolveChapterCwd(ctx, sqlDB, chapterID)
	}
	if narratorID != "" {
		return resolveNarratorCwd(ctx, sqlDB, narratorID)
	}
	// Standalone terminal: no chapter/narrator binding -> home directory.
	return homeCwd(), nil
}

func resolveChapterCwd(ctx context.Context, sqlDB *sql.DB, chapterID string) (sql.NullString, error) {
	var cwd sql.NullString
	err := sqlDB.QueryRowContext(ctx, `SELECT worktree_path FROM chapters WHERE id = ?`, chapterID).Scan(&cwd)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, ErrChapterNotFound
	}
	if err != nil {
		return sql.NullString{}, err
	}
	if !validPath(cwd) {
		return sql.NullString{}, ErrChapterDormant
	}
	return trimNullString(cwd), nil
}

func resolveNarratorCwd(ctx context.Context, sqlDB *sql.DB, narratorID string) (sql.NullString, error) {
	var narratorCwd, chapterWorktree sql.NullString
	err := sqlDB.QueryRowContext(ctx, `
		SELECT n.cwd, c.worktree_path
		FROM narrators n
		LEFT JOIN chapters c ON c.id = n.chapter_id
		WHERE n.id = ?
		LIMIT 1`, narratorID).Scan(&narratorCwd, &chapterWorktree)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, ErrNarratorNotFound
	}
	if err != nil {
		return sql.NullString{}, err
	}
	if validPath(chapterWorktree) {
		return trimNullString(chapterWorktree), nil
	}
	if validPath(narratorCwd) {
		return trimNullString(narratorCwd), nil
	}
	return homeCwd(), nil
}

func validPath(value sql.NullString) bool {
	return value.Valid && strings.TrimSpace(value.String) != ""
}

func trimNullString(value sql.NullString) sql.NullString {
	if !value.Valid {
		return value
	}
	value.String = strings.TrimSpace(value.String)
	if value.String == "" {
		value.Valid = false
	}
	return value
}

func homeCwd() sql.NullString {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: home, Valid: true}
}
