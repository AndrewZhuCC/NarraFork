package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/narrafork/narrafork/internal/apperr"
	"github.com/narrafork/narrafork/internal/database"
	"github.com/narrafork/narrafork/internal/model"
)

// ChapterQueries holds chapter-related data access.
type ChapterQueries struct {
	db *database.DB
}

// chapterColumns lists all chapter columns in a fixed order for SELECT/scan.
const chapterColumns = `id, project_id, title, description, status, role,
	branch, worktree_path, base_branch,
	parent_chapter_id, fork_point, merged_into_chapter_id, merge_commit_sha,
	merge_strategy, pre_merge_target_sha, container_config, exploration_group_id,
	is_root, head_commit_sha, start_commit_sha, commit_count,
	color, group_label, pinned, anchor_commit_sha, axis_offset, cross_offset,
	panel_expanded, panel_width, panel_height,
	review_source_chapter_id, review_status,
	last_accessed_at, created_at, updated_at`

// scanChapter scans a chapter row. INTEGER-bool and nullable numeric columns
// go through the cross-dialect scanner types, then are copied to the model's
// plain Go types.
func scanChapter(row interface{ Scan(...any) error }) (*model.Chapter, error) {
	var c model.Chapter
	var (
		isRoot        database.Bool
		pinned        database.Bool
		panelExpanded database.Bool
		commitCount   database.NullInt64
		axisOffset    database.NullFloat64
		crossOffset   database.NullFloat64
		panelWidth    database.NullFloat64
		panelHeight   database.NullFloat64
	)
	err := row.Scan(
		&c.ID, &c.ProjectID, &c.Title, &c.Description, &c.Status, &c.Role,
		&c.Branch, &c.WorktreePath, &c.BaseBranch,
		&c.ParentChapterID, &c.ForkPoint, &c.MergedIntoChapterID, &c.MergeCommitSha,
		&c.MergeStrategy, &c.PreMergeTargetSha, &c.ContainerConfig, &c.ExplorationGroupID,
		&isRoot, &c.HeadCommitSha, &c.StartCommitSha, &commitCount,
		&c.Color, &c.GroupLabel, &pinned, &c.AnchorCommitSha, &axisOffset, &crossOffset,
		&panelExpanded, &panelWidth, &panelHeight,
		&c.ReviewSourceChapterID, &c.ReviewStatus,
		&c.LastAccessedAt, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	c.IsRoot = bool(isRoot)
	c.Pinned = bool(pinned)
	c.PanelExpanded = bool(panelExpanded)
	c.CommitCount = int(commitCount.Int64)
	c.AxisOffset = axisOffset.Float64
	c.CrossOffset = crossOffset.Float64
	if panelWidth.Valid {
		c.PanelWidth = &panelWidth.Float64
	}
	if panelHeight.Valid {
		c.PanelHeight = &panelHeight.Float64
	}
	return &c, nil
}

// GetByID returns a chapter by ID, or a NotFound AppError.
func (q *ChapterQueries) GetByID(ctx context.Context, id string) (*model.Chapter, error) {
	d := q.db.Dialect
	query := "SELECT " + chapterColumns + " FROM chapters WHERE id = " + d.Placeholder(1)
	row := q.db.QueryRowContext(ctx, query, id)
	c, err := scanChapter(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFoundf("chapter %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get chapter: %w", err)
	}
	return c, nil
}

// ListByProject returns chapters for a project, optionally filtered by status.
func (q *ChapterQueries) ListByProject(ctx context.Context, projectID, status string, limit, offset int) ([]*model.Chapter, error) {
	d := q.db.Dialect
	query := "SELECT " + chapterColumns + " FROM chapters WHERE project_id = " + d.Placeholder(1)
	args := []any{projectID}
	if status != "" {
		query += " AND status = " + d.Placeholder(2)
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC"
	if lo := d.LimitOffset(limit, offset); lo != "" {
		query += " " + lo
	}

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list chapters: %w", err)
	}
	defer rows.Close()

	var chapters []*model.Chapter
	for rows.Next() {
		c, err := scanChapter(rows)
		if err != nil {
			return nil, fmt.Errorf("scan chapter: %w", err)
		}
		chapters = append(chapters, c)
	}
	return chapters, rows.Err()
}

// Create inserts a new chapter. The caller must populate all required fields.
func (q *ChapterQueries) Create(ctx context.Context, c *model.Chapter) error {
	d := q.db.Dialect
	query := "INSERT INTO chapters (" + chapterColumns + ") VALUES (" +
		d.Placeholders(35, 1) + ")"
	_, err := q.db.ExecContext(ctx, query,
		c.ID, c.ProjectID, c.Title, c.Description, c.Status, c.Role,
		c.Branch, c.WorktreePath, c.BaseBranch,
		c.ParentChapterID, c.ForkPoint, c.MergedIntoChapterID, c.MergeCommitSha,
		c.MergeStrategy, c.PreMergeTargetSha, c.ContainerConfig, c.ExplorationGroupID,
		boolToInt(c.IsRoot), c.HeadCommitSha, c.StartCommitSha, c.CommitCount,
		c.Color, c.GroupLabel, boolToInt(c.Pinned), c.AnchorCommitSha, c.AxisOffset, c.CrossOffset,
		boolToInt(c.PanelExpanded), c.PanelWidth, c.PanelHeight,
		c.ReviewSourceChapterID, c.ReviewStatus,
		c.LastAccessedAt, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create chapter: %w", err)
	}
	return nil
}

// UpdateStatus updates a chapter's status and updated_at timestamp.
func (q *ChapterQueries) UpdateStatus(ctx context.Context, id, status, updatedAt string) error {
	d := q.db.Dialect
	query := "UPDATE chapters SET status = " + d.Placeholder(1) +
		", updated_at = " + d.Placeholder(2) + " WHERE id = " + d.Placeholder(3)
	res, err := q.db.ExecContext(ctx, query, status, updatedAt, id)
	if err != nil {
		return fmt.Errorf("update chapter status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFoundf("chapter %s not found", id)
	}
	return nil
}

// Delete removes a chapter by ID.
func (q *ChapterQueries) Delete(ctx context.Context, id string) error {
	d := q.db.Dialect
	query := "DELETE FROM chapters WHERE id = " + d.Placeholder(1)
	res, err := q.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete chapter: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFoundf("chapter %s not found", id)
	}
	return nil
}

// boolToInt converts a bool to 0/1 for INTEGER columns (both dialects store
// booleans as INTEGER in NarraFork's schema).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
