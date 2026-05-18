package search

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/httpx"
)

type Handler struct{ DB *sql.DB }

type searchFallback struct {
	Entity        string `json:"entity"`
	From          string `json:"from"`
	To            string `json:"to"`
	Reason        string `json:"reason"`
	Error         string `json:"error,omitempty"`
	FallbackError string `json:"fallbackError,omitempty"`
}

type searchDiagnostics struct {
	Degraded          bool             `json:"degraded"`
	Fallbacks         []searchFallback `json:"fallbacks"`
	Mode              string           `json:"mode"`
	FTSReady          *bool            `json:"ftsReady"`
	ShortQuery        bool             `json:"shortQuery"`
	RequestedEntities []string         `json:"requestedEntities"`
}

func (d *searchDiagnostics) addFallback(fallback *searchFallback) {
	if fallback == nil {
		return
	}
	if fallback.Reason != "query_too_short_for_trigram" {
		ready := false
		d.Degraded = true
		d.FTSReady = &ready
	}
	if d.Mode == "fts5-with-like-fallback" && fallback.Reason != "query_too_short_for_trigram" {
		d.Mode = "degraded-like-fallback"
	}
	d.Fallbacks = append(d.Fallbacks, *fallback)
}

func NewHandler(sqlDB *sql.DB) *Handler { return &Handler{DB: sqlDB} }

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(h.search))
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	diagnostics := searchDiagnostics{Fallbacks: []searchFallback{}, Mode: "empty", RequestedEntities: []string{}}
	if q == "" {
		writeSearchJSON(w, map[string]any{
			"results":   []any{},
			"projects":  []any{},
			"chapters":  []any{},
			"narrators": []any{},
		}, diagnostics)
		return nil
	}
	limit := 50
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil {
			limit = parsed
		}
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	entitySet := parseEntities(r.URL.Query().Get("entities"))
	diagnostics.RequestedEntities = requestedEntityList(entitySet)
	safeQuery := sanitizeQuery(q)
	if safeQuery == "" {
		writeSearchJSON(w, map[string]any{
			"results":   []any{},
			"projects":  []any{},
			"chapters":  []any{},
			"narrators": []any{},
		}, diagnostics)
		return nil
	}
	like := "%" + escapeLike(safeQuery) + "%"
	ftsQuery := `"` + strings.ReplaceAll(safeQuery, `"`, `""`) + `"`
	useFts := utf8.RuneCountInString(safeQuery) >= 3
	diagnostics.ShortQuery = !useFts
	if useFts {
		diagnostics.Mode = "fts5-with-like-fallback"
	} else {
		diagnostics.Mode = "like-short-query"
	}

	projects, _ := h.searchTable(r, `SELECT id, name, description FROM projects WHERE (name LIKE ? ESCAPE '\' OR COALESCE(description, '') LIKE ? ESCAPE '\') LIMIT 20`, like)
	results := []map[string]any{}
	chapters := []map[string]any{}
	narrators := []map[string]any{}
	if entitySet["chapters"] {
		var fallback *searchFallback
		chapters, fallback = h.searchChapters(r.Context(), safeQuery, like, ftsQuery, useFts, limit)
		diagnostics.addFallback(fallback)
		results = append(results, chapters...)
	}
	if entitySet["messages"] {
		messages, fallback := h.searchMessages(r.Context(), like, ftsQuery, useFts, limit)
		diagnostics.addFallback(fallback)
		results = append(results, messages...)
	}
	if entitySet["narrators"] {
		var fallback *searchFallback
		narrators, fallback = h.searchNarrators(r.Context(), like, ftsQuery, useFts, limit)
		diagnostics.addFallback(fallback)
		results = append(results, narrators...)
	}
	if useFts && !diagnostics.Degraded {
		ready := true
		diagnostics.FTSReady = &ready
	}
	sort.SliceStable(results, func(i, j int) bool {
		leftScore := numeric(results[i]["matchScore"])
		rightScore := numeric(results[j]["matchScore"])
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		return stringValue(results[i]["updatedAt"], results[i]["createdAt"], results[i]["lastMessageAt"]) > stringValue(results[j]["updatedAt"], results[j]["createdAt"], results[j]["lastMessageAt"])
	})
	if len(results) > limit {
		results = results[:limit]
	}
	writeSearchJSON(w, map[string]any{"results": results, "projects": projects, "chapters": chapters, "narrators": narrators}, diagnostics)
	return nil
}

func (h *Handler) searchChapters(ctx context.Context, safeQuery, like, ftsQuery string, useFts bool, limit int) ([]map[string]any, *searchFallback) {
	if useFts {
		items, err := h.searchChaptersFTS(ctx, safeQuery, ftsQuery, limit)
		if err == nil {
			return items, nil
		}
		fallback := &searchFallback{Entity: "chapters", From: "fts5", To: "like", Reason: classifyFTSError(err), Error: err.Error()}
		items, likeErr := h.searchChaptersLike(ctx, safeQuery, like, limit)
		if likeErr != nil {
			fallback.FallbackError = likeErr.Error()
			return []map[string]any{}, fallback
		}
		return items, fallback
	}
	items, err := h.searchChaptersLike(ctx, safeQuery, like, limit)
	fallback := &searchFallback{Entity: "chapters", From: "fts5", To: "like", Reason: "query_too_short_for_trigram"}
	if err != nil {
		fallback.FallbackError = err.Error()
		return []map[string]any{}, fallback
	}
	return items, fallback
}

func (h *Handler) searchChaptersFTS(ctx context.Context, safeQuery, ftsQuery string, limit int) ([]map[string]any, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT c.id, c.title, c.description, c.status, c.role, c.created_at, c.updated_at, p.name, snippet(chapters_fts, 1, '', '', '...', 96), rank FROM chapters_fts JOIN chapters c ON c.rowid = chapters_fts.rowid JOIN projects p ON p.id = c.project_id WHERE chapters_fts MATCH ? ORDER BY rank LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	needle := strings.ToLower(safeQuery)
	for rows.Next() {
		var id, title, status, role, createdAt, updatedAt, projectName string
		var description, snippet sql.NullString
		var rank sql.NullFloat64
		if err := rows.Scan(&id, &title, &description, &status, &role, &createdAt, &updatedAt, &projectName, &snippet, &rank); err != nil {
			return nil, err
		}
		items = append(items, chapterResult(id, title, description, status, role, createdAt, updatedAt, projectName, needle, sqlString(snippet), scoreFromRank(rank, 760)))
	}
	return items, rows.Err()
}

func (h *Handler) searchChaptersLike(ctx context.Context, safeQuery, like string, limit int) ([]map[string]any, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT c.id, c.title, c.description, c.status, c.role, c.created_at, c.updated_at, p.name FROM chapters c JOIN projects p ON p.id = c.project_id WHERE c.title LIKE ? ESCAPE '\' OR COALESCE(c.description, '') LIKE ? ESCAPE '\' LIMIT ?`, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	needle := strings.ToLower(safeQuery)
	for rows.Next() {
		var id, title, status, role, createdAt, updatedAt, projectName string
		var description sql.NullString
		if err := rows.Scan(&id, &title, &description, &status, &role, &createdAt, &updatedAt, &projectName); err != nil {
			return nil, err
		}
		items = append(items, chapterResult(id, title, description, status, role, createdAt, updatedAt, projectName, needle, "", 520))
	}
	return items, rows.Err()
}

func chapterResult(id, title string, description sql.NullString, status, role, createdAt, updatedAt, projectName, needle, snippet string, score int) map[string]any {
	matchField := "description"
	if strings.Contains(strings.ToLower(title), needle) {
		matchField = "title"
	}
	if snippet == "" {
		snippet = sqlString(description)
	}
	if snippet == "" {
		snippet = title
	}
	return map[string]any{"type": "chapter", "id": id, "title": title, "projectName": projectName, "status": status, "role": role, "createdAt": createdAt, "updatedAt": updatedAt, "matchField": matchField, "matchScore": score, "snippet": snippet}
}

func (h *Handler) searchMessages(ctx context.Context, like, ftsQuery string, useFts bool, limit int) ([]map[string]any, *searchFallback) {
	if useFts {
		items, err := h.searchMessagesFTS(ctx, ftsQuery, limit)
		if err == nil {
			return items, nil
		}
		fallback := &searchFallback{Entity: "messages", From: "fts5", To: "like", Reason: classifyFTSError(err), Error: err.Error()}
		items, likeErr := h.searchMessagesLike(ctx, like, limit)
		if likeErr != nil {
			fallback.FallbackError = likeErr.Error()
			return []map[string]any{}, fallback
		}
		return items, fallback
	}
	items, err := h.searchMessagesLike(ctx, like, limit)
	fallback := &searchFallback{Entity: "messages", From: "fts5", To: "like", Reason: "query_too_short_for_trigram"}
	if err != nil {
		fallback.FallbackError = err.Error()
		return []map[string]any{}, fallback
	}
	return items, fallback
}

func (h *Handler) searchMessagesFTS(ctx context.Context, ftsQuery string, limit int) ([]map[string]any, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT m.id, m.narrator_id, substr(COALESCE(m.content_text, ''), 1, 240), m.role, m.created_at, n.chapter_id, n.title, n.model, c.title, p.name, snippet(narrator_messages_fts, 0, '', '', '...', 96), rank FROM narrator_messages_fts JOIN narrator_messages m ON m.rowid = narrator_messages_fts.rowid JOIN narrators n ON n.id = m.narrator_id LEFT JOIN chapters c ON c.id = n.chapter_id LEFT JOIN projects p ON p.id = c.project_id WHERE narrator_messages_fts MATCH ? ORDER BY rank LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, narratorID, preview, role, createdAt string
		var chapterID, narratorTitle, model, chapterTitle, projectName, snippet sql.NullString
		var rank sql.NullFloat64
		if err := rows.Scan(&id, &narratorID, &preview, &role, &createdAt, &chapterID, &narratorTitle, &model, &chapterTitle, &projectName, &snippet, &rank); err != nil {
			return nil, err
		}
		items = append(items, messageResult(id, narratorID, preview, role, createdAt, chapterID, narratorTitle, model, chapterTitle, projectName, sqlString(snippet), scoreFromRank(rank, 700)))
	}
	return items, rows.Err()
}

func (h *Handler) searchMessagesLike(ctx context.Context, like string, limit int) ([]map[string]any, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT m.id, m.narrator_id, substr(COALESCE(m.content_text, ''), 1, 240), m.role, m.created_at, n.chapter_id, n.title, n.model, c.title, p.name FROM narrator_messages m JOIN narrators n ON n.id = m.narrator_id LEFT JOIN chapters c ON c.id = n.chapter_id LEFT JOIN projects p ON p.id = c.project_id WHERE COALESCE(m.content_text, '') LIKE ? ESCAPE '\' LIMIT ?`, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, narratorID, snippet, role, createdAt string
		var chapterID, narratorTitle, model, chapterTitle, projectName sql.NullString
		if err := rows.Scan(&id, &narratorID, &snippet, &role, &createdAt, &chapterID, &narratorTitle, &model, &chapterTitle, &projectName); err != nil {
			return nil, err
		}
		items = append(items, messageResult(id, narratorID, snippet, role, createdAt, chapterID, narratorTitle, model, chapterTitle, projectName, "", 500))
	}
	return items, rows.Err()
}

func messageResult(id, narratorID, preview, role, createdAt string, chapterID, narratorTitle, model, chapterTitle, projectName sql.NullString, snippet string, score int) map[string]any {
	if snippet == "" {
		snippet = preview
	}
	return map[string]any{"type": "message", "id": id, "narratorId": narratorID, "narratorTitle": nullString(narratorTitle), "chapterId": nullString(chapterID), "chapterTitle": nullString(chapterTitle), "projectName": nullString(projectName), "model": nullString(model), "messageRole": role, "createdAt": createdAt, "matchField": "message", "matchScore": score, "snippet": snippet}
}

func (h *Handler) searchNarrators(ctx context.Context, like, ftsQuery string, useFts bool, limit int) ([]map[string]any, *searchFallback) {
	if useFts {
		items, err := h.searchNarratorsFTS(ctx, ftsQuery, limit)
		if err == nil {
			return items, nil
		}
		fallback := &searchFallback{Entity: "narrators", From: "fts5", To: "like", Reason: classifyFTSError(err), Error: err.Error()}
		items, likeErr := h.searchNarratorsLike(ctx, like, limit)
		if likeErr != nil {
			fallback.FallbackError = likeErr.Error()
			return []map[string]any{}, fallback
		}
		return items, fallback
	}
	items, err := h.searchNarratorsLike(ctx, like, limit)
	fallback := &searchFallback{Entity: "narrators", From: "fts5", To: "like", Reason: "query_too_short_for_trigram"}
	if err != nil {
		fallback.FallbackError = err.Error()
		return []map[string]any{}, fallback
	}
	return items, fallback
}

func (h *Handler) searchNarratorsFTS(ctx context.Context, ftsQuery string, limit int) ([]map[string]any, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT n.id, n.title, n.chapter_id, n.status, n.model, n.last_message_at, n.created_at, n.updated_at, c.title, p.name, snippet(narrators_fts, 0, '', '', '...', 96), rank FROM narrators_fts JOIN narrators n ON n.rowid = narrators_fts.rowid LEFT JOIN chapters c ON c.id = n.chapter_id LEFT JOIN projects p ON p.id = c.project_id WHERE narrators_fts MATCH ? ORDER BY rank LIMIT ?`, ftsQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, status, createdAt, updatedAt string
		var title, chapterID, model, lastMessageAt, chapterTitle, projectName, snippet sql.NullString
		var rank sql.NullFloat64
		if err := rows.Scan(&id, &title, &chapterID, &status, &model, &lastMessageAt, &createdAt, &updatedAt, &chapterTitle, &projectName, &snippet, &rank); err != nil {
			return nil, err
		}
		items = append(items, narratorResult(id, title, chapterID, status, model, lastMessageAt, createdAt, updatedAt, chapterTitle, projectName, sqlString(snippet), scoreFromRank(rank, 820)))
	}
	return items, rows.Err()
}

func (h *Handler) searchNarratorsLike(ctx context.Context, like string, limit int) ([]map[string]any, error) {
	rows, err := h.DB.QueryContext(ctx, `SELECT n.id, n.title, n.chapter_id, n.status, n.model, n.last_message_at, n.created_at, n.updated_at, c.title, p.name FROM narrators n LEFT JOIN chapters c ON c.id = n.chapter_id LEFT JOIN projects p ON p.id = c.project_id WHERE COALESCE(n.title, '') LIKE ? ESCAPE '\' LIMIT ?`, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, status, createdAt, updatedAt string
		var title, chapterID, model, lastMessageAt, chapterTitle, projectName sql.NullString
		if err := rows.Scan(&id, &title, &chapterID, &status, &model, &lastMessageAt, &createdAt, &updatedAt, &chapterTitle, &projectName); err != nil {
			return nil, err
		}
		items = append(items, narratorResult(id, title, chapterID, status, model, lastMessageAt, createdAt, updatedAt, chapterTitle, projectName, "", 620))
	}
	return items, rows.Err()
}

func narratorResult(id string, title, chapterID sql.NullString, status string, model, lastMessageAt sql.NullString, createdAt, updatedAt string, chapterTitle, projectName sql.NullString, snippet string, score int) map[string]any {
	titleValue := sqlString(title)
	if snippet == "" {
		snippet = titleValue
	}
	return map[string]any{"type": "narrator", "id": id, "title": titleValue, "chapterId": nullString(chapterID), "chapterTitle": nullString(chapterTitle), "projectName": nullString(projectName), "status": status, "model": nullString(model), "createdAt": createdAt, "updatedAt": updatedAt, "lastMessageAt": nullString(lastMessageAt), "matchField": "title", "matchScore": score, "snippet": snippet}
}

func (h *Handler) searchTable(r *http.Request, query string, like string) ([]map[string]any, error) {
	args := []any{like, like}
	if strings.Count(query, "?") == 1 {
		args = []any{like}
	}
	rows, err := h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, title string
		var desc sql.NullString
		if err := rows.Scan(&id, &title, &desc); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "title": title, "description": nullString(desc)})
	}
	return items, rows.Err()
}

func requestedEntityList(entitySet map[string]bool) []string {
	entities := make([]string, 0, len(entitySet))
	for entity := range entitySet {
		entities = append(entities, entity)
	}
	sort.Strings(entities)
	return entities
}

func parseEntities(raw string) map[string]bool {
	if strings.TrimSpace(raw) == "" {
		raw = "chapters,messages,narrators"
	}
	allowed := map[string]bool{"chapters": true, "messages": true, "narrators": true}
	out := map[string]bool{}
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if allowed[value] {
			out[value] = true
		}
	}
	return out
}

func writeSearchJSON(w http.ResponseWriter, payload map[string]any, diagnostics searchDiagnostics) {
	if diagnostics.Fallbacks == nil {
		diagnostics.Fallbacks = []searchFallback{}
	}
	payload["degraded"] = diagnostics.Degraded
	payload["fallbacks"] = diagnostics.Fallbacks
	payload["searchMetadata"] = diagnostics
	httpx.WriteJSON(w, http.StatusOK, payload)
}

func sanitizeQuery(value string) string {
	value = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`'"*(){}[]^~@:;!&|,<>\`, r) {
			return -1
		}
		return r
	}, value)
	return strings.TrimSpace(value)
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}

func classifyFTSError(err error) string {
	if err == nil {
		return "fts_query_failed"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "no such table") || strings.Contains(message, "no such module") || strings.Contains(message, "no such column") {
		return "fts_unavailable"
	}
	return "fts_query_failed"
}

func scoreFromRank(rank sql.NullFloat64, fallback int) int {
	if !rank.Valid {
		return fallback
	}
	score := 1000 - int(rank.Float64*1000)
	if score < 0 {
		return 0
	}
	return score
}

func numeric(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func stringValue(values ...any) string {
	for _, value := range values {
		if s, ok := value.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func sqlString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func nullString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}
