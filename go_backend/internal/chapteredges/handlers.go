package chapteredges

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

type Handler struct{ DB *sql.DB }

func NewHandler(sqlDB *sql.DB) *Handler { return &Handler{DB: sqlDB} }

var validEdgeTypes = map[string]bool{
	"fork":        true,
	"merge":       true,
	"dependency":  true,
	"cherry_pick": true,
	"review":      true,
}

var manualEdgeTypes = map[string]bool{
	"dependency": true,
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", chapterEdgeHandler(h.list))
	r.Post("/", chapterEdgeHandler(h.create))
	r.Delete("/{id}", chapterEdgeHandler(h.delete))
}

func chapterEdgeHandler(fn httpx.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			writeChapterEdgeError(w, err)
		}
	}
}

func writeChapterEdgeError(w http.ResponseWriter, err error) {
	var appErr *httpx.AppError
	if errors.As(err, &appErr) && appErr.Code == "VALIDATION_ERROR" {
		httpx.WriteJSON(w, appErr.Status, map[string]any{"error": appErr.Message, "code": appErr.Code})
		return
	}
	httpx.WriteError(w, err)
}

type Edge struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	SourceID  string `json:"sourceId"`
	TargetID  string `json:"targetId"`
	Type      string `json:"type"`
	Metadata  any    `json:"metadata"`
	CreatedAt string `json:"createdAt"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	projectID := strings.TrimSpace(r.URL.Query().Get("projectId"))
	chapterID := strings.TrimSpace(r.URL.Query().Get("chapterId"))
	typeValue := strings.TrimSpace(r.URL.Query().Get("type"))
	if typeValue != "" && !validEdgeTypes[typeValue] {
		return validationError("Invalid type: " + typeValue)
	}
	query := `SELECT id, project_id, source_id, target_id, type, metadata, created_at FROM chapter_edges WHERE 1=1`
	args := []any{}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	if chapterID != "" {
		query += ` AND (source_id = ? OR target_id = ?)`
		args = append(args, chapterID, chapterID)
	}
	if typeValue != "" {
		query += ` AND type = ?`
		args = append(args, typeValue)
	}
	if projectID == "" && chapterID == "" {
		return validationError("projectId or chapterId required")
	}
	rows, err := h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Edge{}
	for rows.Next() {
		edge, err := scanEdge(rows)
		if err != nil {
			return err
		}
		items = append(items, edge)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		SourceID string         `json:"sourceId"`
		TargetID string         `json:"targetId"`
		Type     string         `json:"type"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	sourceID := strings.TrimSpace(body.SourceID)
	targetID := strings.TrimSpace(body.TargetID)
	typeValue := strings.TrimSpace(body.Type)
	if sourceID == "" || targetID == "" {
		return validationError("sourceId and targetId are required")
	}
	if sourceID == targetID {
		return validationError("Cannot create a chapter edge to itself")
	}
	if !validEdgeTypes[typeValue] {
		return validationError("Invalid type: " + typeValue)
	}
	if !manualEdgeTypes[typeValue] {
		return validationError("Manual chapter edge create is only supported for dependency edges")
	}

	sourceProjectID, err := h.chapterProjectID(r, sourceID)
	if err != nil {
		return err
	}
	targetProjectID, err := h.chapterProjectID(r, targetID)
	if err != nil {
		return err
	}
	if sourceProjectID != targetProjectID {
		return validationError("Cannot create chapter edge between chapters of different projects")
	}
	if exists, err := h.edgeExists(r, sourceID, targetID, typeValue); err != nil {
		return err
	} else if exists {
		return validationError("Chapter edge already exists")
	}

	metadataPayload, err := dependencyEdgeMetadata(body.Metadata)
	if err != nil {
		return err
	}
	metadata, _ := json.Marshal(metadataPayload)
	edgeID := id.New()
	now := db.NowISO()
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, edgeID, sourceProjectID, sourceID, targetID, typeValue, string(metadata), now)
	if err != nil {
		return err
	}
	if err := syncRedundantChapterFields(r, tx, sourceID, targetID, typeValue, true); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	edge, err := h.get(r, edgeID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, edge)
	return nil
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	edgeID := chi.URLParam(r, "id")
	edge, err := h.get(r, edgeID)
	if err != nil {
		return err
	}
	if !manualEdgeTypes[edge.Type] {
		return validationError("Manual chapter edge delete is only supported for dependency edges")
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM chapter_edges WHERE id = ?`, edgeID); err != nil {
		return err
	}
	if err := syncRedundantChapterFields(r, tx, edge.SourceID, edge.TargetID, edge.Type, false); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) get(r *http.Request, edgeID string) (Edge, error) {
	row := h.DB.QueryRowContext(r.Context(), `SELECT id, project_id, source_id, target_id, type, metadata, created_at FROM chapter_edges WHERE id = ?`, edgeID)
	edge, err := scanEdge(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Edge{}, httpx.Error(http.StatusNotFound, "NOT_FOUND", "ChapterEdge not found: "+edgeID)
	}
	return edge, err
}

func (h *Handler) chapterProjectID(r *http.Request, chapterID string) (string, error) {
	var projectID string
	err := h.DB.QueryRowContext(r.Context(), `SELECT project_id FROM chapters WHERE id = ?`, chapterID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", httpx.Error(http.StatusNotFound, "NOT_FOUND", "Chapter not found: "+chapterID)
	}
	return projectID, err
}

func (h *Handler) edgeExists(r *http.Request, sourceID, targetID, typeValue string) (bool, error) {
	var existingID string
	err := h.DB.QueryRowContext(r.Context(), `SELECT id FROM chapter_edges WHERE source_id = ? AND target_id = ? AND type = ? LIMIT 1`, sourceID, targetID, typeValue).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func syncRedundantChapterFields(r *http.Request, tx *sql.Tx, sourceID, targetID, typeValue string, create bool) error {
	now := db.NowISO()
	switch typeValue {
	case "fork":
		if create {
			_, err := tx.ExecContext(r.Context(), `UPDATE chapters SET parent_chapter_id = ?, updated_at = ? WHERE id = ?`, sourceID, now, targetID)
			return err
		}
		_, err := tx.ExecContext(r.Context(), `UPDATE chapters SET parent_chapter_id = NULL, updated_at = ? WHERE id = ? AND parent_chapter_id = ?`, now, targetID, sourceID)
		return err
	case "merge":
		if create {
			_, err := tx.ExecContext(r.Context(), `UPDATE chapters SET merged_into_chapter_id = ?, updated_at = ? WHERE id = ?`, targetID, now, sourceID)
			return err
		}
		_, err := tx.ExecContext(r.Context(), `UPDATE chapters SET merged_into_chapter_id = NULL, updated_at = ? WHERE id = ? AND merged_into_chapter_id = ?`, now, sourceID, targetID)
		return err
	default:
		return nil
	}
}

func dependencyEdgeMetadata(raw map[string]any) (map[string]any, error) {
	metadata := map[string]any{"lastSyncedCommit": nil}
	if raw == nil {
		return metadata, nil
	}
	value, ok := raw["description"]
	if !ok || value == nil {
		return metadata, nil
	}
	description, ok := value.(string)
	if !ok {
		return nil, validationError("metadata.description must be a string")
	}
	if len([]rune(description)) > 500 {
		return nil, validationError("metadata.description must be at most 500 characters")
	}
	metadata["description"] = description
	return metadata, nil
}

func validationError(message string) *httpx.AppError {
	return httpx.Error(http.StatusBadRequest, "VALIDATION_ERROR", message)
}

type rowScanner interface{ Scan(dest ...any) error }

func scanEdge(row rowScanner) (Edge, error) {
	var e Edge
	var metadata sql.NullString
	if err := row.Scan(&e.ID, &e.ProjectID, &e.SourceID, &e.TargetID, &e.Type, &metadata, &e.CreatedAt); err != nil {
		return Edge{}, err
	}
	if metadata.Valid && metadata.String != "" {
		_ = json.Unmarshal([]byte(metadata.String), &e.Metadata)
	}
	return e, nil
}
