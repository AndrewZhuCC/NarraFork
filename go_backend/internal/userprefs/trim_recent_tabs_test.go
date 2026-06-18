package userprefs

import (
	"reflect"
	"testing"
)

// TestTrimRecentTabsMatchesTS pins trimRecentTabs to the TS trimRecentTabs
// (server/routes/user-preferences.ts): over the cap it drops from the END but
// pin/type-aware - last unpinned subagent first, then last unpinned, then (all
// pinned) the very last - preserving pinned tabs rather than naively truncating.
func TestTrimRecentTabsMatchesTS(t *testing.T) {
	tab := func(id, typ string, pinned bool) map[string]any {
		m := map[string]any{"id": id, "type": typ}
		if pinned {
			m["pinned"] = true
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

	// Under/at cap -> unchanged.
	in := []map[string]any{tab("a", "narrator", false), tab("b", "narrator", false)}
	if got := trimRecentTabs(in, 5); !reflect.DeepEqual(ids(got), []string{"a", "b"}) {
		t.Fatalf("under cap changed: %v", ids(got))
	}

	// Over cap: prefer removing the last UNPINNED SUBAGENT first.
	// [pinned-a, sub-b, narr-c, sub-d] cap 3 -> drop last unpinned subagent (d).
	over := []map[string]any{
		tab("a", "narrator", true),
		tab("b", "subagent", false),
		tab("c", "narrator", false),
		tab("d", "subagent", false),
	}
	if got := trimRecentTabs(over, 3); !reflect.DeepEqual(ids(got), []string{"a", "b", "c"}) {
		t.Fatalf("expected to drop last unpinned subagent d, got %v", ids(got))
	}

	// No subagents: drop the last UNPINNED tab (pinned tail preserved).
	// [narr-a, pinned-b(tail)] over cap 1 -> drop a (unpinned), keep pinned b.
	noSub := []map[string]any{tab("a", "narrator", false), tab("b", "narrator", true)}
	if got := trimRecentTabs(noSub, 1); !reflect.DeepEqual(ids(got), []string{"b"}) {
		t.Fatalf("expected to preserve pinned tail b and drop unpinned a, got %v", ids(got))
	}

	// All pinned: last resort pops the very last.
	allPinned := []map[string]any{tab("a", "narrator", true), tab("b", "narrator", true), tab("c", "narrator", true)}
	if got := trimRecentTabs(allPinned, 2); !reflect.DeepEqual(ids(got), []string{"a", "b"}) {
		t.Fatalf("all-pinned should pop the last, got %v", ids(got))
	}

	// Mixed: subagents removed before non-subagent unpinned, pinned preserved.
	// cap 2 over [pinned-a, sub-b, sub-c, narr-d] -> drop c then b -> [pinned-a, narr-d].
	mixed := []map[string]any{
		tab("a", "narrator", true),
		tab("b", "subagent", false),
		tab("c", "subagent", false),
		tab("d", "narrator", false),
	}
	if got := trimRecentTabs(mixed, 2); !reflect.DeepEqual(ids(got), []string{"a", "d"}) {
		t.Fatalf("mixed trim wrong, got %v", ids(got))
	}
}
