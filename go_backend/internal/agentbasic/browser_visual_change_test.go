package agentbasic

import (
	"context"
	"testing"
	"time"
)

// TestNotifyBrowserVisualChangeEmitsEvent verifies notifyBrowserVisualChange
// emits a "visual_change" observer event (which the narrators observer maps to
// the browser_session_visual_change WS frame) for a known session.
func TestNotifyBrowserVisualChangeEmitsEvent(t *testing.T) {
	resetBrowserRuntimeForTest(t)
	recorder := &browserObserverRecorder{}
	SetBrowserSessionObserver(recorder)
	insertFakeBrowserSession("sess-vc", "nar_vc", time.Hour, time.Now().UTC())

	ctx := ContextWithBrowserExecution(context.Background(), BrowserExecutionContext{NarratorID: "nar_vc"})
	notifyBrowserVisualChange(ctx, "sess-vc")

	if len(recorder.events) != 1 {
		t.Fatalf("expected exactly one visual_change event, got %#v", recorder.events)
	}
	ev := recorder.events[0]
	if ev.Type != "visual_change" {
		t.Fatalf("expected event type visual_change, got %q", ev.Type)
	}
	if ev.Snapshot.ID != "sess-vc" || ev.Snapshot.NarratorID != "nar_vc" {
		t.Fatalf("unexpected snapshot identity: %#v", ev.Snapshot)
	}
}

// TestNotifyBrowserVisualChangeNarratorScoped verifies a narrator cannot trigger
// a visual_change event for another narrator's session.
func TestNotifyBrowserVisualChangeNarratorScoped(t *testing.T) {
	resetBrowserRuntimeForTest(t)
	recorder := &browserObserverRecorder{}
	SetBrowserSessionObserver(recorder)
	insertFakeBrowserSession("sess-owner", "nar_owner", time.Hour, time.Now().UTC())

	ctx := ContextWithBrowserExecution(context.Background(), BrowserExecutionContext{NarratorID: "nar_other"})
	notifyBrowserVisualChange(ctx, "sess-owner")

	if len(recorder.events) != 0 {
		t.Fatalf("expected no visual_change event for cross-narrator access, got %#v", recorder.events)
	}
}

// TestNotifyBrowserVisualChangeMissingSession verifies an unknown session id is a
// no-op (no event, no panic).
func TestNotifyBrowserVisualChangeMissingSession(t *testing.T) {
	resetBrowserRuntimeForTest(t)
	recorder := &browserObserverRecorder{}
	SetBrowserSessionObserver(recorder)

	notifyBrowserVisualChange(context.Background(), "does-not-exist")
	notifyBrowserVisualChange(context.Background(), "")

	if len(recorder.events) != 0 {
		t.Fatalf("expected no events for missing/blank session, got %#v", recorder.events)
	}
}

// TestBrowserVisualChangeActionSetMatchesTS pins the visual-change action set to
// the TS touchSessionVisual actions and guards that read-only actions are
// excluded (server/lib/browser/actions.ts).
func TestBrowserVisualChangeActionSetMatchesTS(t *testing.T) {
	visual := []string{"navigate", "click", "fill", "select", "type", "hover", "evaluate", "evaluate_capture", "wait", "scroll"}
	for _, action := range visual {
		if !browserVisualChangeActions[action] {
			t.Fatalf("expected %q to be a visual-change action", action)
		}
	}
	if len(browserVisualChangeActions) != len(visual) {
		t.Fatalf("browserVisualChangeActions has %d entries, expected %d", len(browserVisualChangeActions), len(visual))
	}
	readOnly := []string{"screenshot", "get_text", "dom", "get_attribute", "get_console", "get_network", "network_start", "network_stop", "perf_start", "perf_stop", "list_sessions", "set_ttl", "close", "launch"}
	for _, action := range readOnly {
		if browserVisualChangeActions[action] {
			t.Fatalf("read-only/non-visual action %q must not be a visual-change action", action)
		}
	}
}

// TestBrowserResultIsSuccess verifies success detection distinguishes JSON
// session results from plain-text errors.
func TestBrowserResultIsSuccess(t *testing.T) {
	cases := []struct {
		name   string
		result string
		want   bool
	}{
		{"json with session_id", `{"session_id":"sess-1","snapshot":{}}`, true},
		{"plain error string", "selector is required", false},
		{"json without session_id", `{"error":"boom"}`, false},
		{"empty", "", false},
		{"not json", "browser session not found", false},
	}
	for _, tc := range cases {
		if got := browserResultIsSuccess(tc.result); got != tc.want {
			t.Fatalf("%s: browserResultIsSuccess(%q)=%v, want %v", tc.name, tc.result, got, tc.want)
		}
	}
}
