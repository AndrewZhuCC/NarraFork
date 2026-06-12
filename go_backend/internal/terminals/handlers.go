package terminals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/terminaldb"
)

var processTreeCommandContext = exec.CommandContext

type Runtime interface {
	Ensure(ctx context.Context, terminalID string) error
	Kill(ctx context.Context, terminalID string) error
}

type shellPIDRuntime interface {
	ShellPID(terminalID string) (int, bool)
}

type sizedRuntime interface {
	EnsureSize(ctx context.Context, terminalID string, cols, rows int) error
}

type NarratorBroadcaster interface {
	BroadcastNarrator(narratorID string, payload any)
}

type Handler struct {
	DB          *sql.DB
	Runtime     Runtime
	Broadcaster NarratorBroadcaster
}

func NewHandler(sqlDB *sql.DB, runtime Runtime, broadcaster ...NarratorBroadcaster) *Handler {
	h := &Handler{DB: sqlDB, Runtime: runtime}
	if len(broadcaster) > 0 {
		h.Broadcaster = broadcaster[0]
	}
	return h
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/", httpx.Handler(h.list))
	r.Post("/", httpx.Handler(h.create))
	r.Get("/tabs", httpx.Handler(h.listTabs))
	r.Post("/tabs", httpx.Handler(h.createTab))
	r.Patch("/tabs/{id}", httpx.Handler(h.updateTab))
	r.Delete("/tabs/{id}", httpx.Handler(h.deleteTab))
	r.Put("/tabs/reorder", httpx.Handler(h.reorderTabs))
	r.Get("/view-state", httpx.Handler(h.getViewState))
	r.Put("/view-state", httpx.Handler(h.putViewState))
	r.Get("/{id}", httpx.Handler(h.get))
	r.Get("/{id}/processes", httpx.Handler(h.processes))
	r.Patch("/{id}", httpx.Handler(h.update))
	r.Delete("/{id}", httpx.Handler(h.delete))
}

type Terminal struct {
	ID          string   `json:"id"`
	ChapterID   *string  `json:"chapterId"`
	NarratorID  *string  `json:"narratorId"`
	Name        string   `json:"name"`
	Cwd         *string  `json:"cwd"`
	DtachSocket *string  `json:"dtachSocket"`
	Status      string   `json:"status"`
	ExitCode    *int     `json:"exitCode"`
	GraphOpened bool     `json:"graphOpened"`
	GraphX      *float64 `json:"graphX"`
	GraphY      *float64 `json:"graphY"`
	GraphWidth  *float64 `json:"graphWidth"`
	GraphHeight *float64 `json:"graphHeight"`
	CreatedAt   string   `json:"createdAt"`
}

type Tab struct {
	ID         string  `json:"id"`
	ChapterID  *string `json:"chapterId"`
	NarratorID *string `json:"narratorId"`
	Name       string  `json:"name"`
	SortOrder  int     `json:"sortOrder"`
	CreatedAt  string  `json:"createdAt"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	chapterID := r.URL.Query().Get("chapterId")
	narratorID := r.URL.Query().Get("narratorId")
	if chapterID == "" && narratorID == "" {
		return httpx.BadRequest("chapterId or narratorId query parameter is required")
	}
	query := `SELECT id, chapter_id, narrator_id, name, cwd, dtach_socket, status, exit_code, graph_opened, graph_x, graph_y, graph_width, graph_height, created_at FROM terminals WHERE `
	args := []any{}
	if chapterID != "" {
		query += `chapter_id = ?`
		args = append(args, chapterID)
	} else {
		query += `narrator_id = ?`
		args = append(args, narratorID)
	}
	query += ` ORDER BY created_at ASC`
	rows, err := h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Terminal{}
	for rows.Next() {
		item, err := scanTerminal(rows)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ChapterID  *string `json:"chapterId"`
		NarratorID *string `json:"narratorId"`
		Name       *string `json:"name"`
		Cols       *int    `json:"cols"`
		Rows       *int    `json:"rows"`
	}
	if err := httpx.DecodeLooseJSON(r, &req); err != nil {
		return err
	}
	if req.ChapterID == nil && req.NarratorID == nil {
		return httpx.BadRequest("chapterId or narratorId is required")
	}
	// Mirror the TypeScript createTerminalSchema Zod bounds: name max length 100,
	// cols int 10..500, rows int 2..200 when provided.
	if req.Name != nil && len(*req.Name) > 100 {
		return httpx.BadRequest("Invalid terminal create request")
	}
	if req.Cols != nil && (*req.Cols < 10 || *req.Cols > 500) {
		return httpx.BadRequest("Invalid terminal create request")
	}
	if req.Rows != nil && (*req.Rows < 2 || *req.Rows > 200) {
		return httpx.BadRequest("Invalid terminal create request")
	}
	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if name == "" {
		name = "Terminal"
	}
	termID := id.New()
	now := db.NowISO()
	cwd, err := terminaldb.ResolveCwd(r.Context(), h.DB, ptrValue(req.ChapterID), ptrValue(req.NarratorID))
	if err != nil {
		return terminalCwdError(err)
	}
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO terminals (id, chapter_id, narrator_id, name, cwd, status, created_at) VALUES (?, ?, ?, ?, ?, 'running', ?)`, termID, nullablePtr(req.ChapterID), nullablePtr(req.NarratorID), name, nullableNullString(cwd), now)
	if err != nil {
		return err
	}
	if h.Runtime != nil {
		cols := 80
		if req.Cols != nil && *req.Cols > 0 {
			cols = *req.Cols
		}
		rows := 24
		if req.Rows != nil && *req.Rows > 0 {
			rows = *req.Rows
		}
		if runtime, ok := h.Runtime.(sizedRuntime); ok {
			err = runtime.EnsureSize(r.Context(), termID, cols, rows)
		} else {
			err = h.Runtime.Ensure(r.Context(), termID)
		}
		if err != nil {
			if _, cleanupErr := h.DB.ExecContext(r.Context(), `DELETE FROM terminals WHERE id = ?`, termID); cleanupErr != nil {
				return fmt.Errorf("start terminal runtime: %w; rollback terminal row: %v", err, cleanupErr)
			}
			return err
		}
	}
	term, err := h.getByID(r, termID)
	if err != nil {
		return err
	}
	h.broadcastTerminalCountChanged(r.Context(), term.NarratorID, term.ChapterID)
	httpx.WriteJSON(w, http.StatusCreated, term)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	term, err := h.getByID(r, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, term)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	var body map[string]any
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	sets := []string{}
	args := []any{}
	// Mirror the TypeScript PATCH name validation: when `name` is provided it
	// must be a non-empty (trimmed) string, otherwise return 400 rather than
	// silently ignoring the field.
	if rawName, present := body["name"]; present {
		name, ok := rawName.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return httpx.BadRequest("name must be a non-empty string")
		}
		sets = append(sets, "name = ?")
		args = append(args, strings.TrimSpace(name))
	}
	for jsonKey, col := range map[string]string{"graphOpened": "graph_opened", "graphX": "graph_x", "graphY": "graph_y", "graphWidth": "graph_width", "graphHeight": "graph_height"} {
		if value, ok := body[jsonKey]; ok {
			sets = append(sets, col+" = ?")
			if jsonKey == "graphOpened" {
				if value == true {
					args = append(args, 1)
				} else {
					args = append(args, 0)
				}
			} else {
				args = append(args, value)
			}
		}
	}
	statusChanged := false
	if status, ok := body["status"].(string); ok {
		status = strings.TrimSpace(status)
		if status != "" {
			sets = append(sets, "status = ?")
			args = append(args, status)
			statusChanged = true
		}
	}
	if exitCode, ok := body["exitCode"]; ok {
		sets = append(sets, "exit_code = ?")
		args = append(args, exitCode)
	}
	if len(sets) > 0 {
		args = append(args, chi.URLParam(r, "id"))
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE terminals SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return err
		}
	}
	term, err := h.getByID(r, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	if statusChanged {
		h.broadcastTerminalCountChanged(r.Context(), term.NarratorID, term.ChapterID)
	}
	httpx.WriteJSON(w, http.StatusOK, term)
	return nil
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	terminalID := chi.URLParam(r, "id")
	term, err := h.getByID(r, terminalID)
	if err != nil {
		return err
	}
	if h.Runtime != nil {
		if err := h.Runtime.Kill(r.Context(), terminalID); err != nil {
			return err
		}
	} else {
		_, err := h.DB.ExecContext(r.Context(), `UPDATE terminals SET status = 'exited', exit_code = COALESCE(exit_code, 0) WHERE id = ?`, terminalID)
		if err != nil {
			return err
		}
	}
	h.broadcastTerminalCountChanged(r.Context(), term.NarratorID, term.ChapterID)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) processes(w http.ResponseWriter, r *http.Request) error {
	terminalID := chi.URLParam(r, "id")
	term, err := h.getByID(r, terminalID)
	if err != nil {
		return err
	}
	if (term.Status != "running" && term.Status != "orphan") || h.Runtime == nil {
		httpx.WriteJSON(w, http.StatusOK, []any{})
		return nil
	}
	pidProvider, ok := h.Runtime.(shellPIDRuntime)
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, []any{})
		return nil
	}
	shellPID, ok := pidProvider.ShellPID(terminalID)
	if !ok || shellPID <= 0 {
		httpx.WriteJSON(w, http.StatusOK, []any{})
		return nil
	}
	items, err := ProcessTree(r.Context(), shellPID)
	if err != nil {
		// Mirror the TypeScript getProcessInfoBatch behavior: process snapshot
		// (`ps`) failures are swallowed and fall back to an empty list rather
		// than surfacing a 500. Log non-fatally for diagnostics.
		slog.Warn("terminal process snapshot failed; returning empty list",
			"terminalId", terminalID, "shellPid", shellPID, "error", err)
		httpx.WriteJSON(w, http.StatusOK, []any{})
		return nil
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func ProcessTree(ctx context.Context, rootPID int) ([]map[string]any, error) {
	if runtime.GOOS == "windows" {
		return []map[string]any{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := processTreeCommandContext(ctx, "ps", "-o", "pid=,ppid=,comm=,stat=,rss=,%cpu=,etime=", "-e")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("prepare process snapshot: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start process snapshot: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, 1<<20))
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("read process snapshot: %w", readErr)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("collect process snapshot: %w", waitErr)
	}
	byPID := map[int]map[string]any{}
	children := map[int][]int{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		rss, _ := strconv.Atoi(fields[4])
		cpu, _ := strconv.ParseFloat(fields[5], 64)
		info := map[string]any{"pid": pid, "ppid": ppid, "command": fields[2], "state": fields[3], "rss": rss, "cpu": cpu, "elapsed": fields[6]}
		byPID[pid] = info
		children[ppid] = append(children[ppid], pid)
	}
	ordered := []int{}
	queue := []int{rootPID}
	seen := map[int]bool{}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if _, ok := byPID[pid]; ok {
			ordered = append(ordered, pid)
		}
		queue = append(queue, children[pid]...)
	}
	items := make([]map[string]any, 0, len(ordered))
	for _, pid := range ordered {
		items = append(items, byPID[pid])
	}
	return items, nil
}

func (h *Handler) getByID(r *http.Request, terminalID string) (Terminal, error) {
	row := h.DB.QueryRowContext(r.Context(), `SELECT id, chapter_id, narrator_id, name, cwd, dtach_socket, status, exit_code, graph_opened, graph_x, graph_y, graph_width, graph_height, created_at FROM terminals WHERE id = ?`, terminalID)
	term, err := scanTerminal(row)
	if err == sql.ErrNoRows {
		return Terminal{}, httpx.NotFound("Terminal")
	}
	return term, err
}

func (h *Handler) broadcastTerminalCountChanged(ctx context.Context, narratorID, chapterID *string) {
	if h.Broadcaster == nil {
		return
	}
	seen := map[string]bool{}
	if id := trimmedPtr(narratorID); id != "" {
		h.broadcastTerminalCountForNarrator(ctx, id)
		seen[id] = true
	}
	chapter := trimmedPtr(chapterID)
	if chapter == "" {
		return
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ?`, chapter)
	if err != nil {
		return
	}
	narratorIDs := []string{}
	for rows.Next() {
		var idValue string
		if err := rows.Scan(&idValue); err == nil && strings.TrimSpace(idValue) != "" {
			narratorIDs = append(narratorIDs, idValue)
		}
	}
	_ = rows.Close()
	for _, idValue := range narratorIDs {
		if seen[idValue] {
			continue
		}
		h.broadcastTerminalCountForNarrator(ctx, idValue)
	}
}

func (h *Handler) broadcastTerminalCountForNarrator(ctx context.Context, narratorID string) {
	id := strings.TrimSpace(narratorID)
	if id == "" {
		return
	}
	count := 0
	fallbacks := []string{}
	if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM terminals WHERE narrator_id = ? AND status = 'running'`, id).Scan(&count); err != nil {
		fallbacks = append(fallbacks, "terminal_count_query_failed")
	}
	h.Broadcaster.BroadcastNarrator(id, map[string]any{
		"type":                "terminal_count_changed",
		"narratorId":          id,
		"activeTerminalCount": count,
		"count":               count,
		"degraded":            len(fallbacks) > 0,
		"fallbacks":           fallbacks,
	})
}

func trimmedPtr(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (h *Handler) listTabs(w http.ResponseWriter, r *http.Request) error {
	chapterID := r.URL.Query().Get("chapterId")
	narratorID := r.URL.Query().Get("narratorId")
	if chapterID == "" && narratorID == "" {
		return httpx.BadRequest("chapterId or narratorId query parameter is required")
	}
	query := `SELECT id, chapter_id, narrator_id, name, sort_order, created_at FROM terminal_tabs WHERE `
	args := []any{}
	if chapterID != "" {
		query += `chapter_id = ?`
		args = append(args, chapterID)
	} else {
		query += `narrator_id = ?`
		args = append(args, narratorID)
	}
	query += ` ORDER BY sort_order ASC, created_at ASC`
	rows, err := h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Tab{}
	for rows.Next() {
		item, err := scanTab(rows)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, items)
	return nil
}

func (h *Handler) createTab(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ChapterID  *string `json:"chapterId"`
		NarratorID *string `json:"narratorId"`
		Name       string  `json:"name"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("name is required")
	}
	// Mirror the TypeScript createTerminalTabSchema refine: exactly one of
	// chapterId or narratorId is required (XOR), not zero and not both.
	hasChapter := req.ChapterID != nil && strings.TrimSpace(*req.ChapterID) != ""
	hasNarrator := req.NarratorID != nil && strings.TrimSpace(*req.NarratorID) != ""
	if hasChapter == hasNarrator {
		return httpx.BadRequest("Exactly one of chapterId or narratorId is required")
	}
	tabID := id.New()
	now := db.NowISO()
	var sortOrder int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(sort_order), -1) + 1 FROM terminal_tabs WHERE (chapter_id IS ? OR narrator_id IS ?)`, nullablePtr(req.ChapterID), nullablePtr(req.NarratorID)).Scan(&sortOrder); err != nil {
		return err
	}
	_, err := h.DB.ExecContext(r.Context(), `INSERT INTO terminal_tabs (id, chapter_id, narrator_id, name, sort_order, created_at) VALUES (?, ?, ?, ?, ?, ?)`, tabID, nullablePtr(req.ChapterID), nullablePtr(req.NarratorID), req.Name, sortOrder, now)
	if err != nil {
		return err
	}
	tab, err := h.getTab(r, tabID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, tab)
	return nil
}

func (h *Handler) updateTab(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) != "" {
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE terminal_tabs SET name = ? WHERE id = ?`, strings.TrimSpace(req.Name), chi.URLParam(r, "id")); err != nil {
			return err
		}
	}
	tab, err := h.getTab(r, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, tab)
	return nil
}

func (h *Handler) deleteTab(w http.ResponseWriter, r *http.Request) error {
	result, err := h.DB.ExecContext(r.Context(), `DELETE FROM terminal_tabs WHERE id = ?`, chi.URLParam(r, "id"))
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return httpx.NotFound("Terminal tab")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) reorderTabs(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, tabID := range req.IDs {
		if _, err := tx.ExecContext(r.Context(), `UPDATE terminal_tabs SET sort_order = ? WHERE id = ?`, i, tabID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) getTab(r *http.Request, tabID string) (Tab, error) {
	row := h.DB.QueryRowContext(r.Context(), `SELECT id, chapter_id, narrator_id, name, sort_order, created_at FROM terminal_tabs WHERE id = ?`, tabID)
	tab, err := scanTab(row)
	if err == sql.ErrNoRows {
		return Tab{}, httpx.NotFound("Terminal tab")
	}
	return tab, err
}

func (h *Handler) getViewState(w http.ResponseWriter, r *http.Request) error {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		return httpx.Unauthorized("Authentication required")
	}
	state, err := h.loadViewState(r, claims.Subject, r.URL.Query().Get("chapterId"), r.URL.Query().Get("narratorId"))
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows {
		state = map[string]any{"layout": "single", "activeTabId": nil, "panelAssignments": nil}
	}
	httpx.WriteJSON(w, http.StatusOK, state)
	return nil
}

// validTerminalViewStateLayout mirrors the TypeScript
// `z.enum(["single", "split-h", "split-v", "triple", "quad"])` layout values.
func validTerminalViewStateLayout(layout string) bool {
	switch layout {
	case "single", "split-h", "split-v", "triple", "quad":
		return true
	default:
		return false
	}
}

// validTerminalPanelAssignments mirrors the TypeScript panelAssignments record
// schema: each value must be a string or an array of strings. A nil/absent map
// (omitted or JSON null) is valid; arrays may only contain strings.
func validTerminalPanelAssignments(assignments map[string]any) bool {
	for _, value := range assignments {
		switch v := value.(type) {
		case string:
			continue
		case []any:
			for _, item := range v {
				if _, ok := item.(string); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func (h *Handler) putViewState(w http.ResponseWriter, r *http.Request) error {
	claims := auth.UserFromContext(r.Context())
	if claims == nil {
		return httpx.Unauthorized("Authentication required")
	}
	var body struct {
		ChapterID        *string        `json:"chapterId"`
		NarratorID       *string        `json:"narratorId"`
		Layout           string         `json:"layout"`
		ActiveTabID      *string        `json:"activeTabId"`
		PanelAssignments map[string]any `json:"panelAssignments"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	// Mirror the TypeScript updateTerminalViewStateSchema: at least one of
	// chapterId or narratorId is required, and layout (when provided) must be
	// one of the allowed enum values.
	hasChapter := body.ChapterID != nil && strings.TrimSpace(*body.ChapterID) != ""
	hasNarrator := body.NarratorID != nil && strings.TrimSpace(*body.NarratorID) != ""
	if !hasChapter && !hasNarrator {
		return httpx.BadRequest("Either chapterId or narratorId is required")
	}
	if body.Layout != "" && !validTerminalViewStateLayout(body.Layout) {
		return httpx.BadRequest("Invalid terminal view state")
	}
	// Mirror the TypeScript panelAssignments schema:
	// z.record(z.string(), z.union([z.string(), z.array(z.string())])).nullable().optional().
	// Each value must be a string or an array of strings when provided.
	if !validTerminalPanelAssignments(body.PanelAssignments) {
		return httpx.BadRequest("Invalid terminal view state")
	}
	if body.Layout == "" {
		body.Layout = "single"
	}
	assignments, _ := json.Marshal(body.PanelAssignments)
	now := db.NowISO()
	viewID := id.New()
	query := `INSERT INTO terminal_view_state (id, user_id, chapter_id, narrator_id, layout, active_tab_id, panel_assignments, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, chapter_id) DO UPDATE SET layout = excluded.layout, active_tab_id = excluded.active_tab_id, panel_assignments = excluded.panel_assignments, updated_at = excluded.updated_at`
	if body.ChapterID == nil && body.NarratorID != nil {
		query = `INSERT INTO terminal_view_state (id, user_id, chapter_id, narrator_id, layout, active_tab_id, panel_assignments, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(user_id, narrator_id) DO UPDATE SET layout = excluded.layout, active_tab_id = excluded.active_tab_id, panel_assignments = excluded.panel_assignments, updated_at = excluded.updated_at`
	}
	_, err := h.DB.ExecContext(r.Context(), query, viewID, claims.Subject, nullablePtr(body.ChapterID), nullablePtr(body.NarratorID), body.Layout, nullablePtr(body.ActiveTabID), string(assignments), now)
	if err != nil {
		return err
	}
	state, err := h.loadViewState(r, claims.Subject, ptrValue(body.ChapterID), ptrValue(body.NarratorID))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, state)
	return nil
}

func (h *Handler) loadViewState(r *http.Request, userID, chapterID, narratorID string) (map[string]any, error) {
	query := `SELECT id, user_id, chapter_id, narrator_id, layout, active_tab_id, panel_assignments, updated_at FROM terminal_view_state WHERE user_id = ? AND `
	args := []any{userID}
	if chapterID != "" {
		query += `chapter_id = ?`
		args = append(args, chapterID)
	} else {
		query += `narrator_id = ?`
		args = append(args, narratorID)
	}
	var idValue, userIDValue, layout, updatedAt string
	var chapterIDValue, narratorIDValue, activeTabID, panelAssignments sql.NullString
	if err := h.DB.QueryRowContext(r.Context(), query, args...).Scan(&idValue, &userIDValue, &chapterIDValue, &narratorIDValue, &layout, &activeTabID, &panelAssignments, &updatedAt); err != nil {
		return nil, err
	}
	var assignments any
	if panelAssignments.Valid && panelAssignments.String != "" && panelAssignments.String != "null" {
		_ = json.Unmarshal([]byte(panelAssignments.String), &assignments)
	}
	return map[string]any{"id": idValue, "userId": userIDValue, "chapterId": nullString(chapterIDValue), "narratorId": nullString(narratorIDValue), "layout": layout, "activeTabId": nullString(activeTabID), "panelAssignments": assignments, "updatedAt": updatedAt}, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanTerminal(row rowScanner) (Terminal, error) {
	var t Terminal
	var chapterID, narratorID, cwd, dtach sql.NullString
	var exitCode sql.NullInt64
	var graphOpened int
	var graphX, graphY, graphWidth, graphHeight sql.NullFloat64
	if err := row.Scan(&t.ID, &chapterID, &narratorID, &t.Name, &cwd, &dtach, &t.Status, &exitCode, &graphOpened, &graphX, &graphY, &graphWidth, &graphHeight, &t.CreatedAt); err != nil {
		return Terminal{}, err
	}
	t.ChapterID = nullStringPtr(chapterID)
	t.NarratorID = nullStringPtr(narratorID)
	t.Cwd = nullStringPtr(cwd)
	t.DtachSocket = nullStringPtr(dtach)
	if exitCode.Valid {
		v := int(exitCode.Int64)
		t.ExitCode = &v
	}
	t.GraphOpened = graphOpened != 0
	t.GraphX = nullFloatPtr(graphX)
	t.GraphY = nullFloatPtr(graphY)
	t.GraphWidth = nullFloatPtr(graphWidth)
	t.GraphHeight = nullFloatPtr(graphHeight)
	return t, nil
}

func scanTab(row rowScanner) (Tab, error) {
	var tab Tab
	var chapterID, narratorID sql.NullString
	if err := row.Scan(&tab.ID, &chapterID, &narratorID, &tab.Name, &tab.SortOrder, &tab.CreatedAt); err != nil {
		return Tab{}, err
	}
	tab.ChapterID = nullStringPtr(chapterID)
	tab.NarratorID = nullStringPtr(narratorID)
	return tab, nil
}

func nullString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func nullFloatPtr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

func terminalCwdError(err error) error {
	switch {
	case errors.Is(err, terminaldb.ErrTargetRequired), errors.Is(err, terminaldb.ErrTargetAmbiguous), errors.Is(err, terminaldb.ErrChapterDormant):
		return httpx.BadRequest(err.Error())
	case errors.Is(err, terminaldb.ErrChapterNotFound):
		return httpx.NotFound("Chapter")
	case errors.Is(err, terminaldb.ErrNarratorNotFound):
		return httpx.NotFound("Narrator")
	default:
		return err
	}
}

func nullablePtr(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}

func nullableNullString(v sql.NullString) any {
	if !v.Valid || v.String == "" {
		return nil
	}
	return v.String
}

func ptrValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
