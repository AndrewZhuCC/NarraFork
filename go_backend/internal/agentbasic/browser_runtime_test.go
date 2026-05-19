package agentbasic

import (
	"context"
	"strings"
	"testing"
	"time"
)

type browserObserverRecorder struct {
	events []BrowserSessionEvent
}

func (r *browserObserverRecorder) BrowserSessionEvent(ctx context.Context, event BrowserSessionEvent) {
	r.events = append(r.events, event)
}

func resetBrowserRuntimeForTest(t *testing.T) {
	t.Helper()
	SetBrowserSessionObserver(nil)
	browserSessions.Lock()
	for id := range browserSessions.items {
		delete(browserSessions.items, id)
	}
	if browserSessions.cleanupTimer != nil {
		browserSessions.cleanupTimer.Stop()
		browserSessions.cleanupTimer = nil
	}
	browserSessions.Unlock()
	t.Cleanup(func() {
		SetBrowserSessionObserver(nil)
		CloseAllBrowserSessions()
	})
}

func insertFakeBrowserSession(idValue, narratorID string, ttl time.Duration, lastActivity time.Time) {
	browserSessions.Lock()
	browserSessions.items[idValue] = &browserSession{
		ID:                    idValue,
		NarratorID:            narratorID,
		URL:                   "https://example.test/" + idValue,
		Title:                 idValue,
		Headless:              true,
		CreatedAt:             lastActivity,
		UpdatedAt:             lastActivity,
		LastActivity:          lastActivity,
		TtlMs:                 int64(ttl / time.Millisecond),
		NetworkCaptureEnabled: true,
	}
	browserSessions.Unlock()
}

func TestBrowserRuntimeBindsSessionsToNarratorContext(t *testing.T) {
	resetBrowserRuntimeForTest(t)
	now := time.Now().UTC()
	insertFakeBrowserSession("sess-a", "nar_a", time.Hour, now)
	insertFakeBrowserSession("sess-b", "nar_b", time.Hour, now)

	ctxA := ContextWithBrowserExecution(context.Background(), BrowserExecutionContext{NarratorID: "nar_a"})
	if _, err := browserRequireSession(ctxA, "sess-a"); err != nil {
		t.Fatalf("expected narrator A to access own session: %v", err)
	}
	if _, err := browserRequireSession(ctxA, "sess-b"); err == nil || !strings.Contains(err.Error(), "different narrator") {
		t.Fatalf("expected narrator isolation error, got %v", err)
	}
	listed := browserListSessions(ctxA)
	if !strings.Contains(listed, "sess-a") || strings.Contains(listed, "sess-b") {
		t.Fatalf("list_sessions should be narrator-scoped, got %s", listed)
	}
}

func TestBrowserRuntimeEnforcesGlobalLimit(t *testing.T) {
	resetBrowserRuntimeForTest(t)
	now := time.Now().UTC()
	for i := 0; i < browserMaxTotalSessions; i++ {
		insertFakeBrowserSession("sess-global-"+string(rune('a'+i)), "nar_global", time.Hour, now)
	}
	if err := enforceBrowserSessionLimits("nar_new"); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("expected global browser limit error, got %v", err)
	}
}

func TestBrowserRuntimeEnforcesPerNarratorLimit(t *testing.T) {
	resetBrowserRuntimeForTest(t)
	now := time.Now().UTC()
	for i := 0; i < browserMaxNarratorSessions; i++ {
		insertFakeBrowserSession("sess-limit-"+string(rune('a'+i)), "nar_limit", time.Hour, now)
	}
	if err := enforceBrowserSessionLimits("nar_limit"); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("expected per-narrator browser limit error, got %v", err)
	}
	if err := enforceBrowserSessionLimits("nar_other"); err != nil {
		t.Fatalf("different narrator should still be able to launch: %v", err)
	}
}

func TestBrowserRuntimeExpirationNotifiesObserver(t *testing.T) {
	resetBrowserRuntimeForTest(t)
	recorder := &browserObserverRecorder{}
	SetBrowserSessionObserver(recorder)
	insertFakeBrowserSession("sess-expired", "nar_exp", time.Millisecond, time.Now().Add(-time.Minute))

	ctx := ContextWithBrowserExecution(context.Background(), BrowserExecutionContext{NarratorID: "nar_exp"})
	_ = browserListSessions(ctx)
	if len(recorder.events) != 1 || recorder.events[0].Type != "expired" || recorder.events[0].Snapshot.ID != "sess-expired" {
		t.Fatalf("expected expired observer event, got %#v", recorder.events)
	}
	if ids := ActiveBrowserSessionIDs(); len(ids) != 0 {
		t.Fatalf("expired session should be removed, got %#v", ids)
	}
}
