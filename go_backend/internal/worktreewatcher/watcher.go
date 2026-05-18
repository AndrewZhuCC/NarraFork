package worktreewatcher

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/id"
)

const (
	DefaultInterval = 5 * time.Second
	defaultLimit    = 500
	commitSyncLimit = 20
)

type Broadcaster interface {
	BroadcastNarrator(narratorID string, payload any)
}

type Service struct {
	DB          *sql.DB
	Git         *gitx.Service
	Broadcaster Broadcaster
	Interval    time.Duration
	Limit       int
	state       map[string]chapterState
}

type chapterState struct {
	HeadSHA   string
	Signature string
}

type chapterRow struct {
	ID           string
	WorktreePath string
	BaseBranch   string
}

func New(sqlDB *sql.DB, git *gitx.Service, broadcaster Broadcaster) *Service {
	return &Service{
		DB:          sqlDB,
		Git:         git,
		Broadcaster: broadcaster,
		Interval:    DefaultInterval,
		Limit:       defaultLimit,
		state:       map[string]chapterState{},
	}
}

func (s *Service) Run(ctx context.Context) {
	if s == nil || s.DB == nil || s.Git == nil || s.Broadcaster == nil {
		return
	}
	interval := s.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	_ = s.PollOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.PollOnce(ctx); err != nil {
				slog.Debug("worktree watcher poll failed", "error", err)
			}
		}
	}
}

func (s *Service) PollOnce(ctx context.Context) error {
	chapters, err := s.activeChapters(ctx)
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, chapter := range chapters {
		seen[chapter.ID] = struct{}{}
		s.pollChapter(ctx, chapter)
	}
	for chapterID := range s.state {
		if _, ok := seen[chapterID]; !ok {
			delete(s.state, chapterID)
		}
	}
	return nil
}

func (s *Service) activeChapters(ctx context.Context) ([]chapterRow, error) {
	limit := s.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, worktree_path, COALESCE(base_branch, '') FROM chapters WHERE status = 'active' AND worktree_path IS NOT NULL AND worktree_path != '' ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chapters := []chapterRow{}
	for rows.Next() {
		var chapter chapterRow
		if err := rows.Scan(&chapter.ID, &chapter.WorktreePath, &chapter.BaseBranch); err == nil && strings.TrimSpace(chapter.WorktreePath) != "" {
			chapters = append(chapters, chapter)
		}
	}
	return chapters, rows.Err()
}

func (s *Service) pollChapter(ctx context.Context, chapter chapterRow) {
	status, err := s.Git.StatusSummary(ctx, chapter.WorktreePath)
	if err != nil {
		slog.Debug("worktree watcher status failed", "chapterId", chapter.ID, "error", err)
		return
	}
	sig := statusSignature(status)
	previous, known := s.state[chapter.ID]
	next := chapterState{HeadSHA: status.HeadSHA, Signature: sig}
	s.state[chapter.ID] = next
	if !known {
		return
	}
	narratorIDs := s.narratorIDs(ctx, chapter.ID)
	if sig != previous.Signature {
		s.broadcastGitStatus(ctx, chapter, status, narratorIDs)
	}
	if status.HeadSHA != "" && status.HeadSHA != previous.HeadSHA {
		newCount, syncErr := s.syncRecentCommits(ctx, chapter, status.HeadSHA)
		if syncErr != nil {
			s.broadcastCommitSyncError(chapter.ID, narratorIDs, syncErr)
		}
		if newCount > 0 {
			s.broadcastCommitsUpdated(chapter.ID, narratorIDs, newCount)
		}
	}
}

func (s *Service) broadcastGitStatus(ctx context.Context, chapter chapterRow, status gitx.StatusSummary, narratorIDs []string) {
	commitsAhead := 0
	if strings.TrimSpace(chapter.BaseBranch) != "" {
		if count, err := s.Git.CountCommitsBetween(ctx, chapter.WorktreePath, chapter.BaseBranch, "HEAD"); err == nil {
			commitsAhead = count
		}
	}
	for _, narratorID := range narratorIDs {
		s.Broadcaster.BroadcastNarrator(narratorID, map[string]any{
			"type":           "git_status",
			"narratorId":     narratorID,
			"chapterId":      chapter.ID,
			"toolUseId":      "",
			"source":         "worktree-watcher",
			"status":         gitStatusWithoutFiles(status),
			"commitsAhead":   commitsAhead,
			"baseBranch":     chapter.BaseBranch,
			"linesAdded":     status.LinesAdded,
			"linesRemoved":   status.LinesRemoved,
			"backgroundSync": true,
		})
	}
}

func (s *Service) broadcastCommitsUpdated(chapterID string, narratorIDs []string, newCount int) {
	for _, narratorID := range narratorIDs {
		s.Broadcaster.BroadcastNarrator(narratorID, map[string]any{
			"type":       "commits_updated",
			"narratorId": narratorID,
			"chapterId":  chapterID,
			"newCount":   newCount,
		})
	}
}

func (s *Service) broadcastCommitSyncError(chapterID string, narratorIDs []string, err error) {
	for _, narratorID := range narratorIDs {
		s.Broadcaster.BroadcastNarrator(narratorID, map[string]any{
			"type":           "commit_sync_error",
			"narratorId":     narratorID,
			"chapterId":      chapterID,
			"code":           "WORKTREE_WATCHER_COMMIT_SYNC_FAILED",
			"message":        err.Error(),
			"backgroundSync": true,
			"fallback":       true,
			"fatal":          false,
		})
	}
}

func (s *Service) narratorIDs(ctx context.Context, chapterID string) []string {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ?`, chapterID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var narratorID string
		if err := rows.Scan(&narratorID); err == nil && strings.TrimSpace(narratorID) != "" {
			ids = append(ids, narratorID)
		}
	}
	return ids
}

func (s *Service) syncRecentCommits(ctx context.Context, chapter chapterRow, headSHA string) (int, error) {
	count, err := s.Git.CommitCount(ctx, chapter.WorktreePath)
	if err != nil {
		return 0, fmt.Errorf("commit count failed: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE chapters SET head_commit_sha = ?, commit_count = ?, updated_at = ? WHERE id = ?`, nullableString(headSHA), count, db.NowISO(), chapter.ID); err != nil {
		return 0, fmt.Errorf("chapter metadata update failed: %w", err)
	}
	commits, err := s.Git.Log(ctx, chapter.WorktreePath, commitSyncLimit)
	if err != nil {
		return 0, fmt.Errorf("git log failed: %w", err)
	}
	inserted := 0
	now := db.NowISO()
	for _, commit := range commits {
		if strings.TrimSpace(commit.SHA) == "" {
			continue
		}
		exists := 0
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = ? AND sha = ?`, chapter.ID, commit.SHA).Scan(&exists); err != nil {
			return inserted, fmt.Errorf("commit existence check failed: %w", err)
		}
		if exists > 0 {
			continue
		}
		filesChanged, linesAdded, linesRemoved, _ := s.Git.CommitDiffStats(ctx, chapter.WorktreePath, commit.SHA)
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO chapter_commits (id, chapter_id, sha, message, full_message, author_name, author_email, authored_at, source, files_changed, lines_added, lines_removed, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'watcher', ?, ?, ?, ?)`, id.New(), chapter.ID, commit.SHA, firstNonEmpty(commit.Message, commit.SHA), nullableString(commit.FullMessage), nullableString(commit.AuthorName), nullableString(commit.AuthorEmail), firstNonEmpty(commit.AuthoredAt, now), filesChanged, linesAdded, linesRemoved, now); err != nil {
			return inserted, fmt.Errorf("commit metadata insert failed: %w", err)
		}
		inserted++
	}
	return inserted, nil
}

func statusSignature(status gitx.StatusSummary) string {
	files := make([]map[string]any, 0, len(status.Files))
	for _, file := range status.Files {
		files = append(files, map[string]any{
			"status":               file.Status,
			"path":                 file.Path,
			"linesAdded":           file.LinesAdded,
			"linesRemoved":         file.LinesRemoved,
			"stagedLinesAdded":     file.StagedLinesAdded,
			"stagedLinesRemoved":   file.StagedLinesRemoved,
			"unstagedLinesAdded":   file.UnstagedLinesAdded,
			"unstagedLinesRemoved": file.UnstagedLinesRemoved,
		})
	}
	payload := map[string]any{
		"hasChanges":   status.HasChanges,
		"staged":       status.Staged,
		"unstaged":     status.Unstaged,
		"untracked":    status.Untracked,
		"totalFiles":   status.TotalFiles,
		"headSha":      status.HeadSHA,
		"branch":       status.Branch,
		"linesAdded":   status.LinesAdded,
		"linesRemoved": status.LinesRemoved,
		"files":        files,
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func gitStatusWithoutFiles(status gitx.StatusSummary) map[string]any {
	return map[string]any{
		"hasChanges":   status.HasChanges,
		"staged":       status.Staged,
		"unstaged":     status.Unstaged,
		"untracked":    status.Untracked,
		"totalFiles":   status.TotalFiles,
		"headSha":      status.HeadSHA,
		"branch":       status.Branch,
		"linesAdded":   status.LinesAdded,
		"linesRemoved": status.LinesRemoved,
	}
}

func nullableString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: strings.TrimSpace(value) != ""}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
