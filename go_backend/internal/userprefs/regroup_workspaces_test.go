package userprefs

import (
	"reflect"
	"testing"
)

// TestRegroupWorkspacesMatchesTS pins regroupWorkspaces to the TS regroupWorkspaces
// (server/routes/user-preferences.ts):
//   - children are re-inserted right after their workspace header, preserving the
//     children's relative order;
//   - orphan children (whose header is absent) get workspaceId cleared and are
//     appended at the END in FIRST-APPEARANCE order (TS iterates the childrenByWs
//     Map in insertion order), NOT sorted by workspace id.
func TestRegroupWorkspacesMatchesTS(t *testing.T) {
	tab := func(id, typ, wsID string) map[string]any {
		m := map[string]any{"id": id, "type": typ}
		if wsID != "" {
			m["workspaceId"] = wsID
		}
		return m
	}
	ids := func(tabs []map[string]any) []string {
		out := make([]string, len(tabs))
		for i, tb := range tabs {
			out[i] = stringValue(tb["id"])
		}
		return out
	}

	// Children are reattached right after their header even if they drifted away.
	grouped := regroupWorkspaces([]map[string]any{
		tab("p", "project", ""),
		tab("ws", "workspace", ""),
		tab("other", "project", ""),
		tab("child", "chapter", "ws"),
	})
	if !reflect.DeepEqual(ids(grouped), []string{"p", "ws", "child", "other"}) {
		t.Fatalf("child should reattach after header, got %v", ids(grouped))
	}

	// Two orphan workspaces (no headers). First child appearance order is wsB then
	// wsA, which is the REVERSE of alphabetical. TS appends orphans in
	// first-appearance order -> childB* before childA*. An alphabetical sort of
	// workspace ids would produce childA* before childB* and fail this assertion.
	orphans := regroupWorkspaces([]map[string]any{
		tab("top", "project", ""),
		tab("childB1", "chapter", "wsB"),
		tab("childA1", "chapter", "wsA"),
		tab("childB2", "chapter", "wsB"),
	})
	if !reflect.DeepEqual(ids(orphans), []string{"top", "childB1", "childB2", "childA1"}) {
		t.Fatalf("orphans should append in first-appearance order (wsB before wsA), got %v", ids(orphans))
	}
	// Orphan children must have workspaceId cleared so they become top-level.
	for _, tb := range orphans {
		if _, ok := tb["workspaceId"]; ok && stringValue(tb["id"]) != "top" {
			t.Fatalf("orphan child %s should have workspaceId cleared, got %#v", stringValue(tb["id"]), tb)
		}
	}

	// Mixed: one workspace has a header (regrouped under it), another is orphaned
	// (appended at the end with workspaceId cleared).
	mixed := regroupWorkspaces([]map[string]any{
		tab("orphanChild", "chapter", "gone"),
		tab("ws", "workspace", ""),
		tab("realChild", "chapter", "ws"),
	})
	if !reflect.DeepEqual(ids(mixed), []string{"ws", "realChild", "orphanChild"}) {
		t.Fatalf("mixed regroup wrong, got %v", ids(mixed))
	}

	// No workspace children -> returned unchanged.
	none := regroupWorkspaces([]map[string]any{tab("a", "project", ""), tab("b", "chapter", "")})
	if !reflect.DeepEqual(ids(none), []string{"a", "b"}) {
		t.Fatalf("no-children case changed order, got %v", ids(none))
	}
}
