package explorations

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type Handler struct{ DB *sql.DB }

func NewHandler(sqlDB *sql.DB) *Handler { return &Handler{DB: sqlDB} }

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(h.list))
	r.Post("/", httpx.Handler(h.create))
	r.Get("/{id}", httpx.Handler(h.get))
	r.Patch("/{id}", httpx.Handler(h.update))
	r.Post("/{id}/decide", httpx.Handler(h.decide))
	r.Post("/{id}/abandon", httpx.Handler(h.abandon))
}

type Group struct {
	ID               string  `json:"id"`
	ProjectID        string  `json:"projectId"`
	Title            string  `json:"title"`
	Description      *string `json:"description"`
	BaseChapterID    *string `json:"baseChapterId"`
	Status           string  `json:"status"`
	DecidedChapterID *string `json:"decidedChapterId"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
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
		ProjectID     string  `json:"projectId"`
		Title         string  `json:"title"`
		Description   *string `json:"description"`
		BaseChapterID *string `json:"baseChapterId"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if body.ProjectID == "" || strings.TrimSpace(body.Title) == "" {
		return httpx.BadRequest("projectId and title are required")
	}
	groupID := id.New()
	now := db.NowISO()
	_, err := h.DB.ExecContext(r.Context(), `INSERT INTO exploration_groups (id, project_id, title, description, base_chapter_id, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 'active', ?, ?)`, groupID, body.ProjectID, body.Title, nullablePtr(body.Description), nullablePtr(body.BaseChapterID), now, now)
	if err != nil {
		return err
	}
	group, err := h.getByID(r, groupID)
	if err != nil {
		return err
	}
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
	_, err := h.DB.ExecContext(r.Context(), `UPDATE exploration_groups SET status = 'decided', decided_chapter_id = ?, updated_at = ? WHERE id = ?`, body.ChapterID, db.NowISO(), chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	return h.get(w, r)
}

func (h *Handler) abandon(w http.ResponseWriter, r *http.Request) error {
	_, err := h.DB.ExecContext(r.Context(), `UPDATE exploration_groups SET status = 'abandoned', updated_at = ? WHERE id = ?`, db.NowISO(), chi.URLParam(r, "id"))
	if err != nil {
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
