package narrators

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"narrafork-go/internal/db"
)

// syncDraftPromoteToRecentTabs mirrors the TypeScript syncNarratorDraftToRecentTabs
// (server/services/user-preferences-service.ts), invoked by the draft PUT route
// with promote = (!previousDraft && newDraft). When a draft is newly created for a
// narrator, every user whose recent_tabs contains a tab representing that narrator
// has that tab promoted to the front of the unpinned section (respecting pins and
// workspace grouping), and the updated snapshot is broadcast to that user.
//
// This follows the chapters package precedent (cleanupChapterRecentTabs) of a
// self-contained recent_tabs mutation in the owning service rather than reaching
// into the userprefs package, whose helpers are unexported. Best-effort: any
// per-user JSON/DB error is skipped so a draft save is never failed by tab sync.
func (s *Service) syncDraftPromoteToRecentTabs(ctx context.Context, narratorID string, promote bool) {
	if !promote || s == nil || s.DB == nil {
		return
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id, recent_tabs FROM user_preferences`)
	if err != nil {
		return
	}
	type pending struct {
		userID string
		tabs   []map[string]any
	}
	var updates []pending
	func() {
		defer rows.Close()
		for rows.Next() {
			var userID, rawTabs string
			if err := rows.Scan(&userID, &rawTabs); err != nil {
				return
			}
			var tabs []map[string]any
			if json.Unmarshal([]byte(rawTabs), &tabs) != nil {
				continue
			}
			migrateRecentTabTypes(tabs)
			if recentTabIndexByNarrator(tabs, narratorID) < 0 {
				continue
			}
			next := promoteDraftRecentTab(tabs, narratorID)
			if next == nil {
				continue // unchanged
			}
			next = regroupRecentTabWorkspaces(next)
			updates = append(updates, pending{userID: userID, tabs: next})
		}
	}()

	for _, u := range updates {
		data, err := json.Marshal(u.tabs)
		if err != nil {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE user_preferences SET recent_tabs = ?, updated_at = ? WHERE user_id = ?`, string(data), db.NowISO(), u.userID); err != nil {
			continue
		}
		if s.Hub != nil {
			s.Hub.BroadcastUser(u.userID, map[string]any{
				"type":     "user:recent_tabs_snapshot",
				"tabs":     u.tabs,
				"revision": time.Now().UnixMilli(),
			})
		}
	}
}

// syncTitleToRecentTabs mirrors the TypeScript syncNarratorTitleToRecentTabs
// (server/services/user-preferences-service.ts), triggered on every narrator
// title update: every user whose recent_tabs contains a tab representing the
// narrator has that tab's title field updated (when different) and the snapshot
// rebroadcast. Best-effort, like syncDraftPromoteToRecentTabs.
func (s *Service) syncTitleToRecentTabs(ctx context.Context, narratorID, title string) {
	if s == nil || s.DB == nil || strings.TrimSpace(narratorID) == "" {
		return
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id, recent_tabs FROM user_preferences`)
	if err != nil {
		return
	}
	type pending struct {
		userID string
		tabs   []map[string]any
	}
	var updates []pending
	func() {
		defer rows.Close()
		for rows.Next() {
			var userID, rawTabs string
			if err := rows.Scan(&userID, &rawTabs); err != nil {
				return
			}
			var tabs []map[string]any
			if json.Unmarshal([]byte(rawTabs), &tabs) != nil {
				continue
			}
			migrateRecentTabTypes(tabs)
			changed := false
			for _, tab := range tabs {
				if recentTabNarratorID(tab) == narratorID && stringValue(tab["title"]) != title {
					tab["title"] = title
					changed = true
				}
			}
			if !changed {
				continue
			}
			updates = append(updates, pending{userID: userID, tabs: tabs})
		}
	}()

	for _, u := range updates {
		data, err := json.Marshal(u.tabs)
		if err != nil {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE user_preferences SET recent_tabs = ?, updated_at = ? WHERE user_id = ?`, string(data), db.NowISO(), u.userID); err != nil {
			continue
		}
		if s.Hub != nil {
			s.Hub.BroadcastUser(u.userID, map[string]any{
				"type":     "user:recent_tabs_snapshot",
				"tabs":     u.tabs,
				"revision": time.Now().UnixMilli(),
			})
		}
	}
}

// ── Self-contained recent_tabs helpers (faithful ports of the userprefs
// equivalents; kept local to avoid exporting userprefs internals) ───────────

func migrateRecentTabTypes(tabs []map[string]any) {
	for _, tab := range tabs {
		if stringValue(tab["type"]) == "session" {
			tab["type"] = "narrator"
		}
	}
}

func recentTabNarratorID(tab map[string]any) string {
	switch stringValue(tab["type"]) {
	case "narrator", "subagent":
		return stringValue(tab["id"])
	case "chapter":
		return stringValue(tab["narratorId"])
	default:
		return ""
	}
}

func recentTabIndexByNarrator(tabs []map[string]any, narratorID string) int {
	for i, tab := range tabs {
		if recentTabNarratorID(tab) == narratorID {
			return i
		}
	}
	return -1
}

// promoteDraftRecentTab mirrors the TS promoteDraftTab: find the tab representing
// the narrator (or its workspace header) and move it to the front of the unpinned
// section. Returns nil when nothing changed (so the caller can skip the write).
func promoteDraftRecentTab(tabs []map[string]any, narratorID string) []map[string]any {
	idx := recentTabIndexByNarrator(tabs, narratorID)
	if idx < 0 {
		return nil
	}
	tab := tabs[idx]
	if wsID := stringValue(tab["workspaceId"]); wsID != "" {
		headerIdx := -1
		for i, candidate := range tabs {
			if stringValue(candidate["type"]) == "workspace" && stringValue(candidate["id"]) == wsID {
				headerIdx = i
				break
			}
		}
		if headerIdx >= 0 {
			return promoteTopLevelRecentTabRespectingPins(tabs, headerIdx)
		}
	}
	return promoteTopLevelRecentTabRespectingPins(tabs, idx)
}

func promoteTopLevelRecentTabRespectingPins(tabs []map[string]any, idx int) []map[string]any {
	if idx < 0 || idx >= len(tabs) {
		return nil
	}
	tab := tabs[idx]
	if stringValue(tab["workspaceId"]) != "" || boolValue(tab["pinned"]) {
		return nil
	}
	before := recentTabsKeyJoin(tabs)
	moved, remaining := extractRecentTabGroup(tabs, idx)
	result := insertRecentTabs(remaining, recentTabsPinnedSectionEndIndex(remaining), moved)
	if recentTabsKeyJoin(result) == before {
		return nil
	}
	return result
}

func extractRecentTabGroup(tabs []map[string]any, idx int) ([]map[string]any, []map[string]any) {
	if idx < 0 || idx >= len(tabs) {
		return nil, tabs
	}
	end := idx + 1
	if stringValue(tabs[idx]["type"]) == "workspace" {
		wsID := stringValue(tabs[idx]["id"])
		for end < len(tabs) && stringValue(tabs[end]["workspaceId"]) == wsID {
			end++
		}
	}
	moved := append([]map[string]any{}, tabs[idx:end]...)
	remaining := make([]map[string]any, 0, len(tabs)-len(moved))
	remaining = append(remaining, tabs[:idx]...)
	remaining = append(remaining, tabs[end:]...)
	return moved, remaining
}

func insertRecentTabs(tabs []map[string]any, index int, items []map[string]any) []map[string]any {
	if len(items) == 0 {
		return tabs
	}
	if index < 0 {
		index = 0
	}
	if index > len(tabs) {
		index = len(tabs)
	}
	out := make([]map[string]any, 0, len(tabs)+len(items))
	out = append(out, tabs[:index]...)
	out = append(out, items...)
	out = append(out, tabs[index:]...)
	return out
}

func recentTabsPinnedSectionEndIndex(tabs []map[string]any) int {
	idx := 0
	for idx < len(tabs) {
		tab := tabs[idx]
		if stringValue(tab["workspaceId"]) != "" {
			idx++
			continue
		}
		if !boolValue(tab["pinned"]) {
			break
		}
		idx++
		if stringValue(tab["type"]) == "workspace" {
			wsID := stringValue(tab["id"])
			for idx < len(tabs) && stringValue(tabs[idx]["workspaceId"]) == wsID {
				idx++
			}
		}
	}
	return idx
}

func recentTabsKeyJoin(tabs []map[string]any) string {
	out := ""
	for i, tab := range tabs {
		if i > 0 {
			out += "|"
		}
		out += stringValue(tab["type"]) + ":" + stringValue(tab["id"])
	}
	return out
}

func regroupRecentTabWorkspaces(tabs []map[string]any) []map[string]any {
	childrenByWorkspace := map[string][]map[string]any{}
	orphanOrder := make([]string, 0)
	for _, tab := range tabs {
		if wsID := stringValue(tab["workspaceId"]); wsID != "" {
			if _, seen := childrenByWorkspace[wsID]; !seen {
				orphanOrder = append(orphanOrder, wsID)
			}
			childrenByWorkspace[wsID] = append(childrenByWorkspace[wsID], tab)
		}
	}
	if len(childrenByWorkspace) == 0 {
		return tabs
	}
	topLevel := make([]map[string]any, 0, len(tabs))
	for _, tab := range tabs {
		if stringValue(tab["workspaceId"]) == "" {
			topLevel = append(topLevel, tab)
		}
	}
	result := make([]map[string]any, 0, len(tabs))
	headerIDs := map[string]struct{}{}
	for _, tab := range topLevel {
		result = append(result, tab)
		if stringValue(tab["type"]) != "workspace" {
			continue
		}
		wsID := stringValue(tab["id"])
		headerIDs[wsID] = struct{}{}
		result = append(result, childrenByWorkspace[wsID]...)
	}
	for _, wsID := range orphanOrder {
		if _, ok := headerIDs[wsID]; ok {
			continue
		}
		for _, child := range childrenByWorkspace[wsID] {
			delete(child, "workspaceId")
			result = append(result, child)
		}
	}
	return result
}
