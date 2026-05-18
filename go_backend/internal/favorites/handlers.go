package favorites

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type Handler struct{ DB *sql.DB }

func NewHandler(sqlDB *sql.DB) *Handler { return &Handler{DB: sqlDB} }

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(h.list))
	r.Post("/", httpx.Handler(h.create))
	r.Patch("/{id}", httpx.Handler(h.update))
	r.Delete("/{id}", httpx.Handler(h.delete))
	r.Put("/reorder", httpx.Handler(h.reorder))
}

type Favorite struct {
	ID        string  `json:"id"`
	UserID    string  `json:"userId"`
	Path      string  `json:"path"`
	Label     *string `json:"label"`
	SortOrder int     `json:"sortOrder"`
	CreatedAt string  `json:"createdAt"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id, user_id, path, label, sort_order, created_at FROM user_favorite_directories WHERE user_id = ? ORDER BY sort_order ASC, created_at ASC`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Favorite{}
	for rows.Next() {
		fav, err := scanFavorite(rows)
		if err != nil {
			return err
		}
		items = append(items, fav)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body struct {
		Path  string  `json:"path"`
		Label *string `json:"label"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Path) == "" {
		return httpx.BadRequest("path is required")
	}
	var exists int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM user_favorite_directories WHERE user_id = ? AND path = ?`, userID, body.Path).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return httpx.BadRequest("Directory already in favorites")
	}
	var sortOrder int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(sort_order), -1) + 1 FROM user_favorite_directories WHERE user_id = ?`, userID).Scan(&sortOrder); err != nil {
		return err
	}
	favID := id.New()
	now := db.NowISO()
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO user_favorite_directories (id, user_id, path, label, sort_order, created_at) VALUES (?, ?, ?, ?, ?, ?)`, favID, userID, body.Path, nullablePtr(body.Label), sortOrder, now)
	if err != nil {
		return err
	}
	fav, err := h.get(r, userID, favID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, fav)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	sets := []string{}
	args := []any{}
	for key, col := range map[string]string{"path": "path", "label": "label", "sortOrder": "sort_order"} {
		if val, ok := body[key]; ok {
			sets = append(sets, col+" = ?")
			args = append(args, val)
		}
	}
	if len(sets) > 0 {
		args = append(args, chi.URLParam(r, "id"), userID)
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE user_favorite_directories SET `+strings.Join(sets, ", ")+` WHERE id = ? AND user_id = ?`, args...); err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	_, err = h.DB.ExecContext(r.Context(), `DELETE FROM user_favorite_directories WHERE id = ? AND user_id = ?`, chi.URLParam(r, "id"), userID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) reorder(w http.ResponseWriter, r *http.Request) error {
	userID, err := currentUserID(r)
	if err != nil {
		return err
	}
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, favID := range body.IDs {
		if _, err := tx.ExecContext(r.Context(), `UPDATE user_favorite_directories SET sort_order = ? WHERE id = ? AND user_id = ?`, i, favID, userID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) get(r *http.Request, userID, favID string) (Favorite, error) {
	row := h.DB.QueryRowContext(r.Context(), `SELECT id, user_id, path, label, sort_order, created_at FROM user_favorite_directories WHERE id = ? AND user_id = ?`, favID, userID)
	return scanFavorite(row)
}

type rowScanner interface{ Scan(dest ...any) error }

func scanFavorite(row rowScanner) (Favorite, error) {
	var fav Favorite
	var label sql.NullString
	if err := row.Scan(&fav.ID, &fav.UserID, &fav.Path, &label, &fav.SortOrder, &fav.CreatedAt); err != nil {
		return Favorite{}, err
	}
	if label.Valid {
		fav.Label = &label.String
	}
	return fav, nil
}

func currentUserID(r *http.Request) (string, error) {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		return "", httpx.Unauthorized("Authentication required")
	}
	return claims.Subject, nil
}

func nullablePtr(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}
