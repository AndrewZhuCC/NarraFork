package favorites

import (
	"context"
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
	body, err := decodeCreateFavoriteBody(r)
	if err != nil {
		return err
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
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO user_favorite_directories (id, user_id, path, label, sort_order, created_at) VALUES (?, ?, ?, ?, ?, ?)`, favID, userID, body.Path, nullableLabel(body.Label, body.HasLabel), sortOrder, now)
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
	body, err := decodeUpdateFavoriteBody(r)
	if err != nil {
		return err
	}
	favID := chi.URLParam(r, "id")
	exists, err := h.exists(r.Context(), userID, favID)
	if err != nil {
		return err
	}
	if !exists {
		return httpx.NotFound("Favorite directory")
	}
	sets := []string{}
	args := []any{}
	if body.HasPath {
		sets = append(sets, "path = ?")
		args = append(args, body.Path)
	}
	if body.HasLabel {
		sets = append(sets, "label = ?")
		args = append(args, body.Label)
	}
	if body.ClearLabel {
		sets = append(sets, "label = ?")
		args = append(args, nil)
	}
	if body.HasSortOrder {
		sets = append(sets, "sort_order = ?")
		args = append(args, body.SortOrder)
	}
	if len(sets) > 0 {
		args = append(args, favID, userID)
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
	favID := chi.URLParam(r, "id")
	exists, err := h.exists(r.Context(), userID, favID)
	if err != nil {
		return err
	}
	if !exists {
		return httpx.NotFound("Favorite directory")
	}
	_, err = h.DB.ExecContext(r.Context(), `DELETE FROM user_favorite_directories WHERE id = ? AND user_id = ?`, favID, userID)
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
	ids, err := decodeFavoriteReorderBody(r)
	if err != nil {
		return err
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, favID := range ids {
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

func (h *Handler) exists(ctx context.Context, userID, favID string) (bool, error) {
	var count int
	if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_favorite_directories WHERE id = ? AND user_id = ?`, favID, userID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

type favoriteCreateBody struct {
	Path     string
	Label    string
	HasLabel bool
}

type favoriteUpdateBody struct {
	Path         string
	Label        string
	SortOrder    int
	HasPath      bool
	HasLabel     bool
	ClearLabel   bool
	HasSortOrder bool
}

func decodeCreateFavoriteBody(r *http.Request) (favoriteCreateBody, error) {
	var raw map[string]any
	if err := httpx.DecodeLooseJSON(r, &raw); err != nil {
		return favoriteCreateBody{}, err
	}
	path, err := requiredStringField(raw, "path", 1, 4096)
	if err != nil {
		return favoriteCreateBody{}, err
	}
	body := favoriteCreateBody{Path: path}
	if value, exists := raw["label"]; exists {
		label, err := optionalStringValue(value, "label", 200)
		if err != nil {
			return favoriteCreateBody{}, err
		}
		body.Label = label
		body.HasLabel = true
	}
	return body, nil
}

func decodeUpdateFavoriteBody(r *http.Request) (favoriteUpdateBody, error) {
	var raw map[string]any
	if err := httpx.DecodeLooseJSON(r, &raw); err != nil {
		return favoriteUpdateBody{}, err
	}
	body := favoriteUpdateBody{}
	if value, exists := raw["path"]; exists {
		path, err := optionalStringValue(value, "path", 4096)
		if err != nil {
			return favoriteUpdateBody{}, err
		}
		if path == "" {
			return favoriteUpdateBody{}, httpx.BadRequest("path must be at least 1 character")
		}
		body.Path = path
		body.HasPath = true
	}
	if value, exists := raw["label"]; exists {
		if value == nil {
			body.ClearLabel = true
		} else {
			label, err := optionalStringValue(value, "label", 200)
			if err != nil {
				return favoriteUpdateBody{}, err
			}
			body.Label = label
			body.HasLabel = true
		}
	}
	if value, exists := raw["sortOrder"]; exists {
		sortOrder, err := intField(value, "sortOrder")
		if err != nil {
			return favoriteUpdateBody{}, err
		}
		if sortOrder < 0 {
			return favoriteUpdateBody{}, httpx.BadRequest("sortOrder must be non-negative")
		}
		body.SortOrder = sortOrder
		body.HasSortOrder = true
	}
	return body, nil
}

func decodeFavoriteReorderBody(r *http.Request) ([]string, error) {
	var raw map[string]any
	if err := httpx.DecodeLooseJSON(r, &raw); err != nil {
		return nil, err
	}
	value, exists := raw["ids"]
	if !exists {
		return nil, httpx.BadRequest("ids is required")
	}
	items, ok := value.([]any)
	if !ok {
		return nil, httpx.BadRequest("ids must be an array")
	}
	if len(items) == 0 {
		return nil, httpx.BadRequest("ids must contain at least one item")
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || text == "" {
			return nil, httpx.BadRequest("ids must contain non-empty strings")
		}
		ids = append(ids, text)
	}
	return ids, nil
}

func requiredStringField(raw map[string]any, field string, minLength, maxLength int) (string, error) {
	value, exists := raw[field]
	if !exists {
		return "", httpx.BadRequest(field + " is required")
	}
	text, err := optionalStringValue(value, field, maxLength)
	if err != nil {
		return "", err
	}
	if len(text) < minLength {
		return "", httpx.BadRequest(field + " must be at least 1 character")
	}
	return text, nil
}

func optionalStringValue(value any, field string, maxLength int) (string, error) {
	text, ok := value.(string)
	if !ok {
		return "", httpx.BadRequest(field + " must be a string")
	}
	if len(text) > maxLength {
		return "", httpx.BadRequest(field + " is too long")
	}
	return text, nil
}

func intField(value any, field string) (int, error) {
	switch n := value.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, httpx.BadRequest(field + " must be an integer")
		}
		return int(n), nil
	case int:
		return n, nil
	default:
		return 0, httpx.BadRequest(field + " must be an integer")
	}
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

func nullableLabel(label string, present bool) any {
	if !present {
		return nil
	}
	return label
}
