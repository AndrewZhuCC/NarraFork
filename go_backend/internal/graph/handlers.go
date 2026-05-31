package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/httpx"
)

type Handler struct {
	DB  *sql.DB
	Git *gitx.Service
}

func NewHandler(sqlDB *sql.DB, gitService ...*gitx.Service) *Handler {
	var git *gitx.Service
	if len(gitService) > 0 {
		git = gitService[0]
	}
	return &Handler{DB: sqlDB, Git: git}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/{id}/graph", httpx.Handler(h.graph))
	r.Patch("/{id}/graph/positions", httpx.Handler(h.updatePositions))
}

type node struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Data     map[string]any `json:"data"`
	Position map[string]any `json:"position"`
}

type edge struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	Type     string `json:"type"`
	Metadata any    `json:"metadata,omitempty"`
}

func (h *Handler) graph(w http.ResponseWriter, r *http.Request) error {
	projectID := chi.URLParam(r, "id")
	chapterRows, err := h.DB.QueryContext(r.Context(), `SELECT id, title, status, branch, role, color, group_label, exploration_group_id, COALESCE(is_root, 0), anchor_commit_sha, COALESCE(axis_offset, 0), COALESCE(cross_offset, 0), COALESCE(commit_count, 0), head_commit_sha, COALESCE(panel_expanded, 0), panel_width, panel_height, worktree_path, container_config, review_source_chapter_id, review_status FROM chapters WHERE project_id = ?`, projectID)
	if err != nil {
		return err
	}
	type chapterRecord struct {
		id, title, status, branch, role             string
		color, groupLabel, explorationGroup         sql.NullString
		anchor, head, worktree                      sql.NullString
		containerConfig, reviewSource, reviewStatus sql.NullString
		isRoot, panelExpanded                       int
		axisOffset, crossOffset                     float64
		commitCount                                 int
		panelWidth, panelHeight                     sql.NullFloat64
	}
	records := []chapterRecord{}
	for chapterRows.Next() {
		var ch chapterRecord
		if err := chapterRows.Scan(&ch.id, &ch.title, &ch.status, &ch.branch, &ch.role, &ch.color, &ch.groupLabel, &ch.explorationGroup, &ch.isRoot, &ch.anchor, &ch.axisOffset, &ch.crossOffset, &ch.commitCount, &ch.head, &ch.panelExpanded, &ch.panelWidth, &ch.panelHeight, &ch.worktree, &ch.containerConfig, &ch.reviewSource, &ch.reviewStatus); err != nil {
			_ = chapterRows.Close()
			return err
		}
		records = append(records, ch)
	}
	if err := chapterRows.Err(); err != nil {
		_ = chapterRows.Close()
		return err
	}
	if err := chapterRows.Close(); err != nil {
		return err
	}

	fallbacks := []map[string]any{}
	narratorCounts, narratorIDs, narratorStatuses, narratorSubstatus, narratorMessageVersions, err := h.narratorMeta(r, projectID)
	if err != nil {
		fallbacks = append(fallbacks, graphFallback("graph.narrators", "narrator_metadata_unavailable", err))
	}
	containerPresence, err := h.containerPresence(r, projectID)
	if err != nil {
		fallbacks = append(fallbacks, graphFallback("graph.containers", "container_presence_unavailable", err))
	}
	for i := range records {
		head, commitCount, refreshErr := h.refreshGitMeta(r.Context(), records[i].id, records[i].status, records[i].worktree, records[i].head, records[i].commitCount)
		if refreshErr != nil {
			fallbacks = append(fallbacks, graphFallback("graph.gitMetadata", "git_metadata_refresh_failed", refreshErr))
		}
		records[i].head = head
		records[i].commitCount = commitCount
	}
	upstreamUpdates, err := h.upstreamUpdates(r, projectID)
	if err != nil {
		fallbacks = append(fallbacks, graphFallback("graph.upstreamUpdates", "dependency_upstream_updates_unavailable", err))
	}
	nodes := []node{}
	for _, ch := range records {
		nodeType := "chapterNode"
		if ch.role == "review" {
			nodeType = "reviewNode"
		}
		nodes = append(nodes, node{
			ID:   ch.id,
			Type: nodeType,
			Data: map[string]any{
				"title":                  ch.title,
				"status":                 ch.status,
				"branch":                 ch.branch,
				"role":                   ch.role,
				"color":                  nullString(ch.color),
				"groupLabel":             nullString(ch.groupLabel),
				"explorationGroupId":     nullString(ch.explorationGroup),
				"narratorCount":          narratorCounts[ch.id],
				"narratorId":             nullableMapString(narratorIDs, ch.id),
				"narratorStatus":         nullableMapString(narratorStatuses, ch.id),
				"narratorSubstatus":      narratorSubstatus[ch.id],
				"narratorMessageVersion": nullableMapInt(narratorMessageVersions, ch.id),
				"messageVersion":         nullableMapInt(narratorMessageVersions, ch.id),
				"hasContainers":          containerPresence[ch.id],
				"hasUpstreamUpdates":     upstreamUpdates[ch.id],
				"isRoot":                 ch.isRoot != 0,
				"commitCount":            ch.commitCount,
				"headCommitSha":          nullString(ch.head),
				"panelExpanded":          ch.panelExpanded != 0,
				"panelWidth":             nullFloat(ch.panelWidth),
				"panelHeight":            nullFloat(ch.panelHeight),
				"worktreePath":           nullString(ch.worktree),
				"reviewSourceChapterId":  nullString(ch.reviewSource),
				"reviewStatus":           nullString(ch.reviewStatus),
			},
			Position: map[string]any{
				"anchorCommitSha": nullString(ch.anchor),
				"axisOffset":      ch.axisOffset,
				"crossOffset":     ch.crossOffset,
			},
		})
	}

	edges, err := h.edges(r, projectID)
	if err != nil {
		return err
	}
	groups, err := h.explorationGroups(r, projectID)
	if err != nil {
		fallbacks = append(fallbacks, graphFallback("graph.explorationGroups", "exploration_groups_unavailable", err))
	}
	openedTerminals, err := h.openedTerminals(r, projectID)
	if err != nil {
		fallbacks = append(fallbacks, graphFallback("graph.openedTerminals", "opened_terminals_unavailable", err))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"nodes":             nodes,
		"edges":             edges,
		"explorationGroups": groups,
		"openedTerminals":   openedTerminals,
		"capabilities":      domainCapabilities(),
		"degraded":          len(fallbacks) > 0,
		"fallbacks":         fallbacks,
	})
	return nil
}

func graphFallback(feature, reason string, err error) map[string]any {
	payload := map[string]any{"feature": feature, "reason": reason}
	if err != nil {
		payload["error"] = err.Error()
	}
	return payload
}

const CompressedAISummaryFallbackReason = "Go split compressed context copies bounded message refs and records a deterministic context note instead of generating a TypeScript-equivalent model summary."

func SplitCapability() map[string]any {
	return map[string]any{
		"supported": true,
		"fallback":  false,
		"mode":      "split-at-commit",
		"routes": map[string]any{
			"splitAtCommit": true,
		},
		"partials": map[string]any{
			"compressedAISummary": map[string]any{
				"supported":             false,
				"fallback":              true,
				"mode":                  "bounded-ref-copy-with-deterministic-context-note",
				"deterministic":         true,
				"copiesMessageRefs":     true,
				"generatesModelSummary": false,
				"reason":                CompressedAISummaryFallbackReason,
			},
			"containerAutostart": map[string]any{
				"supported": true,
				"fallback":  false,
				"mode":      "background-start",
			},
		},
	}
}

func domainCapabilities() map[string]any {
	return map[string]any{
		"split": SplitCapability(),
		"chapterEdges": map[string]any{
			"manualCreateTypes": []string{"dependency"},
			"manualDeleteTypes": []string{"dependency"},
			"systemOwnedTypes":  []string{"fork", "merge", "review", "cherry_pick"},
		},
		"commitSync": map[string]any{
			"supported":                true,
			"manualRecordingSupported": true,
			"mode":                     "manual-route-lightweight-graph-read-and-worktree-watcher",
			"backgroundSync":           true,
			"worktreeWatcher":          true,
			"graphReadRefresh":         true,
			"fallback":                 false,
			"events":                   []string{"git_status", "commits_updated", "commit_sync_error"},
			"sources":                  []string{"manual", "watcher"},
			"metadata":                 "Go records commits created through git routes, refreshes only lightweight chapter head/count metadata when graph data is read, and runs a background worktree watcher for external commit/status sync.",
		},
		"upstreamUpdates": map[string]any{
			"supported": true,
			"fallback":  false,
			"mode":      "db-backed-dependency-last-synced-commit",
			"field":     "nodes[].data.hasUpstreamUpdates",
			"source":    "chapter_edges.metadata.lastSyncedCommit + chapters.headCommitSha",
		},
	}
}

func (h *Handler) updatePositions(w http.ResponseWriter, r *http.Request) error {
	projectID := chi.URLParam(r, "id")
	var body struct {
		Positions []struct {
			ChapterID       string   `json:"chapterId"`
			AnchorCommitSHA *string  `json:"anchorCommitSha"`
			AxisOffset      *float64 `json:"axisOffset"`
			CrossOffset     *float64 `json:"crossOffset"`
			PanelExpanded   *bool    `json:"panelExpanded"`
			PanelWidth      *float64 `json:"panelWidth"`
			PanelHeight     *float64 `json:"panelHeight"`
			Width           *float64 `json:"width"`
			Height          *float64 `json:"height"`
		} `json:"positions"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if len(body.Positions) > 500 {
		return httpx.BadRequest("positions must contain at most 500 items")
	}
	chapterIDs := make([]string, 0, len(body.Positions))
	for _, pos := range body.Positions {
		if strings.TrimSpace(pos.ChapterID) == "" {
			return httpx.BadRequest("chapterId is required")
		}
		if pos.AxisOffset == nil {
			return httpx.BadRequest("axisOffset is required")
		}
		if pos.CrossOffset == nil {
			return httpx.BadRequest("crossOffset is required")
		}
		if *pos.CrossOffset < 0 {
			return httpx.BadRequest("crossOffset must be non-negative")
		}
		chapterIDs = append(chapterIDs, pos.ChapterID)
	}
	if err := h.validatePositionOwnership(r.Context(), projectID, chapterIDs); err != nil {
		return err
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, pos := range body.Positions {
		panelExpanded := any(nil)
		if pos.PanelExpanded != nil {
			if *pos.PanelExpanded {
				panelExpanded = 1
			} else {
				panelExpanded = 0
			}
		}
		panelWidth := pos.PanelWidth
		if panelWidth == nil {
			panelWidth = pos.Width
		}
		panelHeight := pos.PanelHeight
		if panelHeight == nil {
			panelHeight = pos.Height
		}
		_, err := tx.ExecContext(r.Context(), `UPDATE chapters SET anchor_commit_sha = ?, axis_offset = ?, cross_offset = ?, panel_expanded = COALESCE(?, panel_expanded), panel_width = COALESCE(?, panel_width), panel_height = COALESCE(?, panel_height), updated_at = ? WHERE id = ? AND project_id = ?`, nullablePtr(pos.AnchorCommitSHA), *pos.AxisOffset, *pos.CrossOffset, panelExpanded, nullableFloatPtr(panelWidth), nullableFloatPtr(panelHeight), db.NowISO(), pos.ChapterID, projectID)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) refreshGitMeta(ctx context.Context, chapterID, status string, worktree, cachedHead sql.NullString, cachedCount int) (sql.NullString, int, error) {
	if h.Git == nil || status != "active" || !worktree.Valid || strings.TrimSpace(worktree.String) == "" {
		return cachedHead, cachedCount, nil
	}
	head, err := h.Git.HeadCommit(ctx, worktree.String)
	if err != nil {
		return cachedHead, cachedCount, fmt.Errorf("chapter %s head refresh failed: %w", chapterID, err)
	}
	if strings.TrimSpace(head) == "" {
		return cachedHead, cachedCount, fmt.Errorf("chapter %s head refresh returned empty sha", chapterID)
	}
	count, err := h.Git.CommitCount(ctx, worktree.String)
	if err != nil {
		return sql.NullString{String: head, Valid: true}, cachedCount, fmt.Errorf("chapter %s commit count refresh failed: %w", chapterID, err)
	}
	if !cachedHead.Valid || cachedHead.String != head || cachedCount != count {
		if _, err := h.DB.ExecContext(ctx, `UPDATE chapters SET head_commit_sha = ?, commit_count = ?, updated_at = ? WHERE id = ?`, head, count, db.NowISO(), chapterID); err != nil {
			return sql.NullString{String: head, Valid: true}, count, fmt.Errorf("chapter %s git metadata writeback failed: %w", chapterID, err)
		}
	}
	return sql.NullString{String: head, Valid: true}, count, nil
}

func (h *Handler) validatePositionOwnership(ctx context.Context, projectID string, chapterIDs []string) error {
	if len(chapterIDs) == 0 {
		return nil
	}
	placeholders := make([]string, 0, len(chapterIDs))
	args := make([]any, 0, len(chapterIDs)+1)
	args = append(args, projectID)
	for _, chapterID := range chapterIDs {
		placeholders = append(placeholders, "?")
		args = append(args, chapterID)
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT id FROM chapters WHERE project_id = ? AND id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	owned := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		owned[id] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	invalid := []string{}
	seenInvalid := map[string]bool{}
	for _, chapterID := range chapterIDs {
		if !owned[chapterID] && !seenInvalid[chapterID] {
			invalid = append(invalid, chapterID)
			seenInvalid[chapterID] = true
		}
	}
	if len(invalid) > 0 {
		return httpx.BadRequest("Chapters not in project: " + strings.Join(invalid, ", "))
	}
	return nil
}

func (h *Handler) narratorMeta(r *http.Request, projectID string) (map[string]int, map[string]string, map[string]string, map[string][]string, map[string]int, error) {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT n.chapter_id, n.id, n.status, n.substatus, COALESCE(n.message_version, 0) FROM narrators n JOIN chapters c ON c.id = n.chapter_id WHERE c.project_id = ? ORDER BY n.created_at ASC, n.id ASC`, projectID)
	counts := map[string]int{}
	ids := map[string]string{}
	statuses := map[string]string{}
	substatus := map[string][]string{}
	messageVersions := map[string]int{}
	if err != nil {
		return counts, ids, statuses, substatus, messageVersions, err
	}
	defer rows.Close()
	for rows.Next() {
		var chapterID, narratorID string
		var status, raw sql.NullString
		var messageVersion int
		if err := rows.Scan(&chapterID, &narratorID, &status, &raw, &messageVersion); err != nil {
			return counts, ids, statuses, substatus, messageVersions, err
		}
		counts[chapterID]++
		if _, ok := ids[chapterID]; !ok {
			ids[chapterID] = narratorID
			if status.Valid {
				statuses[chapterID] = status.String
			}
			substatus[chapterID] = parseSubstatus(raw)
			messageVersions[chapterID] = messageVersion
		}
	}
	return counts, ids, statuses, substatus, messageVersions, rows.Err()
}

func (h *Handler) containerPresence(r *http.Request, projectID string) (map[string]bool, error) {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT DISTINCT ci.chapter_id FROM container_instances ci JOIN chapters c ON c.id = ci.chapter_id WHERE c.project_id = ? AND ci.status <> 'removed'`, projectID)
	presence := map[string]bool{}
	if err != nil {
		return presence, err
	}
	defer rows.Close()
	for rows.Next() {
		var chapterID string
		if err := rows.Scan(&chapterID); err != nil {
			return presence, err
		}
		presence[chapterID] = true
	}
	return presence, rows.Err()
}

func (h *Handler) upstreamUpdates(r *http.Request, projectID string) (map[string]bool, error) {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT e.target_id, COALESCE(e.metadata, ''), COALESCE(s.head_commit_sha, '') FROM chapter_edges e JOIN chapters s ON s.id = e.source_id WHERE e.project_id = ? AND e.type = 'dependency'`, projectID)
	updates := map[string]bool{}
	if err != nil {
		return updates, err
	}
	defer rows.Close()
	for rows.Next() {
		var targetID, rawMetadata, sourceHead string
		if err := rows.Scan(&targetID, &rawMetadata, &sourceHead); err != nil {
			return updates, err
		}
		lastSynced := graphDependencyLastSyncedCommit(rawMetadata)
		if strings.TrimSpace(sourceHead) != "" && sourceHead != lastSynced {
			updates[targetID] = true
		}
	}
	return updates, rows.Err()
}

func graphDependencyLastSyncedCommit(rawMetadata string) string {
	if strings.TrimSpace(rawMetadata) == "" {
		return ""
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
		return ""
	}
	return graphStringValue(metadata["lastSyncedCommit"])
}

func graphStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return ""
	}
}

func (h *Handler) edges(r *http.Request, projectID string) ([]edge, error) {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id, source_id, target_id, type, metadata FROM chapter_edges WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []edge{}
	for rows.Next() {
		var e edge
		var metadata sql.NullString
		if err := rows.Scan(&e.ID, &e.Source, &e.Target, &e.Type, &metadata); err != nil {
			return nil, err
		}
		if metadata.Valid && metadata.String != "" {
			var decoded any
			if json.Unmarshal([]byte(metadata.String), &decoded) == nil {
				e.Metadata = decoded
			}
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

func (h *Handler) openedTerminals(r *http.Request, projectID string) ([]map[string]any, error) {

	rows, err := h.DB.QueryContext(r.Context(), `SELECT t.id, t.chapter_id, t.name, t.status, t.graph_x, t.graph_y, t.graph_width, t.graph_height FROM terminals t JOIN chapters c ON c.id = t.chapter_id WHERE c.project_id = ? AND COALESCE(t.graph_opened, 0) = 1 AND t.status = 'running' ORDER BY t.created_at ASC`, projectID)
	if err != nil {
		return []map[string]any{}, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, chapterID, name, status string
		var graphX, graphY, graphWidth, graphHeight sql.NullFloat64
		if err := rows.Scan(&id, &chapterID, &name, &status, &graphX, &graphY, &graphWidth, &graphHeight); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "chapterId": chapterID, "name": name, "status": status, "graphX": nullFloat(graphX), "graphY": nullFloat(graphY), "graphWidth": nullFloat(graphWidth), "graphHeight": nullFloat(graphHeight)})
	}
	return items, rows.Err()
}

func (h *Handler) explorationGroups(r *http.Request, projectID string) ([]map[string]any, error) {
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id, project_id, title, description, base_chapter_id, status, decided_chapter_id, created_at, updated_at FROM exploration_groups WHERE project_id = ?`, projectID)
	if err != nil {
		return []map[string]any{}, err
	}
	defer rows.Close()
	groups := []map[string]any{}
	for rows.Next() {
		var id, pid, title, status, createdAt, updatedAt string
		var description, baseChapterID, decidedChapterID sql.NullString
		if err := rows.Scan(&id, &pid, &title, &description, &baseChapterID, &status, &decidedChapterID, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, map[string]any{"id": id, "projectId": pid, "title": title, "description": nullString(description), "baseChapterId": nullString(baseChapterID), "status": status, "decidedChapterId": nullString(decidedChapterID), "createdAt": createdAt, "updatedAt": updatedAt})
	}
	return groups, rows.Err()
}

func nullString(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}

func nullFloat(v sql.NullFloat64) any {
	if !v.Valid {
		return nil
	}
	return v.Float64
}

func nullableMapString(m map[string]string, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	return nil
}

func nullableMapInt(m map[string]int, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	return nil
}

func parseSubstatus(raw sql.NullString) []string {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw.String), &tags); err != nil {
		return []string{}
	}
	return tags
}

func nullablePtr(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableFloatPtr(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
