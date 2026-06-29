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

// ProjectQueries holds project-related data access.
type ProjectQueries struct {
	db *database.DB
}

// projectColumns lists all project columns in a fixed order for SELECT/scan.
const projectColumns = `id, name, description, status, flow_mode, git_path, remote_url,
	default_branch, startup_script, copy_files, chapter_settings, proxy_domain,
	created_at, updated_at`

// scanProject scans a single project row in projectColumns order.
func scanProject(row interface{ Scan(...any) error }) (*model.Project, error) {
	var p model.Project
	err := row.Scan(
		&p.ID, &p.Name, &p.Description, &p.Status, &p.FlowMode, &p.GitPath, &p.RemoteURL,
		&p.DefaultBranch, &p.StartupScript, &p.CopyFiles, &p.ChapterSettings, &p.ProxyDomain,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetByID returns a project by ID, or a NotFound AppError.
func (q *ProjectQueries) GetByID(ctx context.Context, id string) (*model.Project, error) {
	d := q.db.Dialect
	query := "SELECT " + projectColumns + " FROM projects WHERE id = " + d.Placeholder(1)
	row := q.db.QueryRowContext(ctx, query, id)
	p, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFoundf("project %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	return p, nil
}

// List returns projects ordered by updated_at descending, with optional
// status filter (empty = all) and pagination.
func (q *ProjectQueries) List(ctx context.Context, status string, limit, offset int) ([]*model.Project, error) {
	d := q.db.Dialect
	query := "SELECT " + projectColumns + " FROM projects"
	args := []any{}
	if status != "" {
		query += " WHERE status = " + d.Placeholder(1)
		args = append(args, status)
	}
	query += " ORDER BY updated_at DESC"
	if lo := d.LimitOffset(limit, offset); lo != "" {
		query += " " + lo
	}

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var projects []*model.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// Create inserts a new project.
func (q *ProjectQueries) Create(ctx context.Context, p *model.Project) error {
	d := q.db.Dialect
	query := "INSERT INTO projects (" + projectColumns + ") VALUES (" +
		d.Placeholders(14, 1) + ")"
	_, err := q.db.ExecContext(ctx, query,
		p.ID, p.Name, p.Description, p.Status, p.FlowMode, p.GitPath, p.RemoteURL,
		p.DefaultBranch, p.StartupScript, p.CopyFiles, p.ChapterSettings, p.ProxyDomain,
		p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	return nil
}

// Update updates mutable project fields by ID.
func (q *ProjectQueries) Update(ctx context.Context, p *model.Project) error {
	d := q.db.Dialect
	query := "UPDATE projects SET " +
		"name = " + d.Placeholder(1) + ", " +
		"description = " + d.Placeholder(2) + ", " +
		"status = " + d.Placeholder(3) + ", " +
		"flow_mode = " + d.Placeholder(4) + ", " +
		"git_path = " + d.Placeholder(5) + ", " +
		"remote_url = " + d.Placeholder(6) + ", " +
		"default_branch = " + d.Placeholder(7) + ", " +
		"startup_script = " + d.Placeholder(8) + ", " +
		"copy_files = " + d.Placeholder(9) + ", " +
		"chapter_settings = " + d.Placeholder(10) + ", " +
		"proxy_domain = " + d.Placeholder(11) + ", " +
		"updated_at = " + d.Placeholder(12) + " " +
		"WHERE id = " + d.Placeholder(13)
	res, err := q.db.ExecContext(ctx, query,
		p.Name, p.Description, p.Status, p.FlowMode, p.GitPath, p.RemoteURL,
		p.DefaultBranch, p.StartupScript, p.CopyFiles, p.ChapterSettings, p.ProxyDomain,
		p.UpdatedAt, p.ID,
	)
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFoundf("project %s not found", p.ID)
	}
	return nil
}

// Delete removes a project by ID (cascades to chapters via FK).
func (q *ProjectQueries) Delete(ctx context.Context, id string) error {
	d := q.db.Dialect
	query := "DELETE FROM projects WHERE id = " + d.Placeholder(1)
	res, err := q.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFoundf("project %s not found", id)
	}
	return nil
}
