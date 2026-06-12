package explorations

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	chaptersvc "narrafork-go/internal/chapters"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type Handler struct {
	DB       *sql.DB
	Chapters *chaptersvc.Service
}

func NewHandler(sqlDB *sql.DB, services ...any) *Handler {
	h := &Handler{DB: sqlDB}
	for _, service := range services {
		switch v := service.(type) {
		case *chaptersvc.Service:
			h.Chapters = v
		}
	}
	return h
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(h.list))
	r.Post("/", httpx.Handler(h.create))
	r.Get("/{id}", httpx.Handler(h.get))
	r.Patch("/{id}", httpx.Handler(h.update))
	r.Post("/{id}/decide", httpx.Handler(h.decide))
	r.Post("/{id}/abandon", httpx.Handler(h.abandon))
}

type Group struct {
	ID               string               `json:"id"`
	ProjectID        string               `json:"projectId"`
	Title            string               `json:"title"`
	Description      *string              `json:"description"`
	BaseChapterID    *string              `json:"baseChapterId"`
	Status           string               `json:"status"`
	DecidedChapterID *string              `json:"decidedChapterId"`
	CreatedAt        string               `json:"createdAt"`
	UpdatedAt        string               `json:"updatedAt"`
	Branches         []chaptersvc.Chapter `json:"branches,omitempty"`
}

type createBranchRequest struct {
	Title       string  `json:"title"`
	Description *string `json:"description"`
	InheritMode string  `json:"inheritMode"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	projectID := r.URL.Query().Get("projectId")
	if projectID == "" {
		return httpx.BadRequest("projectId is required")
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id, project_id, title, description, base_chapter_id, status, decided_chapter_id, created_at, updated_at FROM exploration_groups WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Group{}
	for rows.Next() {
		group, err := scanGroup(rows)
		if err != nil {
			return err
		}
		items = append(items, group)
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ProjectID     string                `json:"projectId"`
		Title         string                `json:"title"`
		Description   *string               `json:"description"`
		BaseChapterID *string               `json:"baseChapterId"`
		Branches      []createBranchRequest `json:"branches"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	body.ProjectID = strings.TrimSpace(body.ProjectID)
	body.Title = strings.TrimSpace(body.Title)
	baseChapterID := strings.TrimSpace(stringPtrValue(body.BaseChapterID))
	if body.ProjectID == "" || body.Title == "" || baseChapterID == "" {
		return httpx.ValidationError("projectId, title, and baseChapterId are required")
	}
	// Mirror the TypeScript createExplorationGroupSchema Zod bounds: title max 200,
	// description max 2000, branches 2..10 with each branch title 1..200 /
	// description max 2000 / inheritMode enum.
	if len(body.Title) > 200 {
		return httpx.ValidationError("title must be at most 200 characters")
	}
	if body.Description != nil && len(*body.Description) > 2000 {
		return httpx.ValidationError("description must be at most 2000 characters")
	}
	if len(body.Branches) < 2 || len(body.Branches) > 10 {
		return httpx.ValidationError("branches must contain 2 to 10 items")
	}
	for _, branch := range body.Branches {
		if strings.TrimSpace(branch.Title) == "" {
			return httpx.ValidationError("branch title is required")
		}
		if len(branch.Title) > 200 {
			return httpx.ValidationError("branch title must be at most 200 characters")
		}
		if branch.Description != nil && len(*branch.Description) > 2000 {
			return httpx.ValidationError("branch description must be at most 2000 characters")
		}
		if branch.InheritMode != "" && branch.InheritMode != "full" && branch.InheritMode != "compressed" && branch.InheritMode != "fresh" {
			return httpx.ValidationError("branch inheritMode must be full, compressed, or fresh")
		}
	}
	if h.Chapters == nil {
		return httpx.Error(http.StatusNotImplemented, "FEATURE_UNAVAILABLE", "Exploration branch creation requires chapter service support")
	}
	var baseProjectID string
	err := h.DB.QueryRowContext(r.Context(), `SELECT project_id FROM chapters WHERE id = ?`, baseChapterID).Scan(&baseProjectID)
	if err == sql.ErrNoRows {
		return httpx.NotFound("Base chapter")
	}
	if err != nil {
		return err
	}
	if baseProjectID != body.ProjectID {
		return httpx.ValidationError("baseChapterId does not belong to projectId")
	}
	groupID := id.New()
	now := db.NowISO()
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO exploration_groups (id, project_id, title, description, base_chapter_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'active', ?, ?)`, groupID, body.ProjectID, body.Title, nullablePtr(body.Description), baseChapterID, now, now)
	if err != nil {
		return err
	}
	created := make([]chaptersvc.Chapter, 0, len(body.Branches))
	cleanup := func() {
		for _, chapter := range created {
			_ = h.Chapters.Delete(r.Context(), chapter.ID)
		}
		_, _ = h.DB.ExecContext(r.Context(), `DELETE FROM exploration_groups WHERE id = ?`, groupID)
	}
	for _, branch := range body.Branches {
		branch.Title = strings.TrimSpace(branch.Title)
		if branch.Title == "" {
			cleanup()
			return httpx.ValidationError("branch title is required")
		}
		chapter, err := h.Chapters.Fork(r.Context(), baseChapterID, chaptersvc.ForkInput{
			Title:       &branch.Title,
			Description: branch.Description,
			Role:        "exploration",
			InheritMode: branch.InheritMode,
		})
		if err != nil {
			cleanup()
			return err
		}
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE chapters SET exploration_group_id = ?, group_label = ?, updated_at = ? WHERE id = ?`, groupID, body.Title, db.NowISO(), chapter.ID); err != nil {
			created = append(created, chapter)
			cleanup()
			return err
		}
		chapter.ExplorationGroupID = &groupID
		chapter.GroupLabel = &body.Title
		created = append(created, chapter)
	}
	group, err := h.getByID(r, groupID)
	if err != nil {
		return err
	}
	group.Branches = created
	httpx.WriteJSON(w, http.StatusCreated, group)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	group, err := h.getByID(r, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, group)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	// Mirror the TypeScript updateExplorationGroupSchema Zod bounds: title min 1 /
	// max 200, description max 2000 (when provided).
	if raw, ok := body["title"]; ok {
		title, isString := raw.(string)
		if !isString || strings.TrimSpace(title) == "" {
			return httpx.ValidationError("title must be a non-empty string")
		}
		if len(title) > 200 {
			return httpx.ValidationError("title must be at most 200 characters")
		}
	}
	if raw, ok := body["description"]; ok && raw != nil {
		description, isString := raw.(string)
		if !isString {
			return httpx.ValidationError("description must be a string")
		}
		if len(description) > 2000 {
			return httpx.ValidationError("description must be at most 2000 characters")
		}
	}
	sets := []string{}
	args := []any{}
	for key, col := range map[string]string{"title": "title", "description": "description"} {
		if val, ok := body[key]; ok {
			sets = append(sets, col+" = ?")
			args = append(args, val)
		}
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = ?")
		args = append(args, db.NowISO(), chi.URLParam(r, "id"))
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE exploration_groups SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return err
		}
	}
	return h.get(w, r)
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		ChapterID string `json:"chapterId"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	groupID := chi.URLParam(r, "id")
	chapterID := strings.TrimSpace(body.ChapterID)
	if chapterID == "" {
		return httpx.ValidationError("chapterId is required")
	}
	var belongs int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM chapters WHERE id = ? AND exploration_group_id = ?`, chapterID, groupID).Scan(&belongs); err != nil {
		return err
	}
	if belongs == 0 {
		return httpx.BadRequest("chapter does not belong to exploration group")
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := db.NowISO()
	if _, err := tx.ExecContext(r.Context(), `UPDATE exploration_groups SET status = 'decided', decided_chapter_id = ?, updated_at = ? WHERE id = ?`, chapterID, now, groupID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE chapters SET status = 'abandoned', updated_at = ? WHERE exploration_group_id = ? AND id <> ? AND status = 'active'`, now, groupID, chapterID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return h.get(w, r)
}

func (h *Handler) abandon(w http.ResponseWriter, r *http.Request) error {
	groupID := chi.URLParam(r, "id")
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := db.NowISO()
	if _, err := tx.ExecContext(r.Context(), `UPDATE exploration_groups SET status = 'abandoned', updated_at = ? WHERE id = ?`, now, groupID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE chapters SET status = 'abandoned', updated_at = ? WHERE exploration_group_id = ? AND status = 'active'`, now, groupID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return h.get(w, r)
}

func (h *Handler) getByID(r *http.Request, id string) (Group, error) {
	row := h.DB.QueryRowContext(r.Context(), `SELECT id, project_id, title, description, base_chapter_id, status, decided_chapter_id, created_at, updated_at FROM exploration_groups WHERE id = ?`, id)
	group, err := scanGroup(row)
	if err == sql.ErrNoRows {
		return Group{}, httpx.NotFound("Exploration group")
	}
	return group, err
}

type rowScanner interface{ Scan(dest ...any) error }

func scanGroup(row rowScanner) (Group, error) {
	var group Group
	var description, baseChapterID, decidedChapterID sql.NullString
	if err := row.Scan(&group.ID, &group.ProjectID, &group.Title, &description, &baseChapterID, &group.Status, &decidedChapterID, &group.CreatedAt, &group.UpdatedAt); err != nil {
		return Group{}, err
	}
	group.Description = nullStringPtr(description)
	group.BaseChapterID = nullStringPtr(baseChapterID)
	group.DecidedChapterID = nullStringPtr(decidedChapterID)
	return group, nil
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func nullablePtr(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}

func stringPtrValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
