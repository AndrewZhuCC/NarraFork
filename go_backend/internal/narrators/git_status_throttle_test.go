package narrators

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// countBroadcastsOfType counts captured broadcasts of eventType for narratorID.
func countBroadcastsOfType(events []capturedBroadcast, narratorID, eventType string) int {
	n := 0
	for _, ev := range events {
		if ev.NarratorID == narratorID && ev.Payload["type"] == eventType {
			n++
		}
	}
	return n
}

func newGitTrackTestService(t *testing.T) (*Service, string, func() []capturedBroadcast) {
	t.Helper()
	repo := t.TempDir()
	runGitNarratorAgent(t, repo, "init", "-b", "main")
	runGitNarratorAgent(t, repo, "config", "user.email", "test@example.com")
	runGitNarratorAgent(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write tracked fixture: %v", err)
	}
	runGitNarratorAgent(t, repo, "add", "tracked.txt")
	runGitNarratorAgent(t, repo, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("before\nafter\n"), 0o644); err != nil {
		t.Fatalf("mutate tracked fixture: %v", err)
	}
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", "http://127.0.0.1")
	if _, err := service.DB.Exec(`CREATE TABLE chapters (id TEXT PRIMARY KEY, worktree_path TEXT, base_branch TEXT)`); err != nil {
		t.Fatalf("create chapters table: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO chapters (id, worktree_path, base_branch) VALUES ('chapter-throttle', ?, 'main')`, repo); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := service.DB.Exec(`UPDATE narrators SET chapter_id = ?, cwd = ? WHERE id = ?`, "chapter-throttle", repo, narratorID); err != nil {
		t.Fatalf("bind narrator to chapter: %v", err)
	}
	hub, drain := captureNarratorBroadcasts()
	service.Hub = hub
	return service, narratorID, drain
}

// TestBroadcastGitStatusAfterToolThrottlesRapidCalls verifies a burst of rapid
// file-mutating tool calls collapses into a single trailing git_status broadcast
// referencing the most recent toolUseID, mirroring the TS 800ms trailing
// throttle (narrator-session.ts:1339).
func TestBroadcastGitStatusAfterToolThrottlesRapidCalls(t *testing.T) {
	service, narratorID, drain := newGitTrackTestService(t)
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}

	service.broadcastGitStatusAfterTool(context.Background(), narratorID, narrator, "Write", "tool-1")
	service.broadcastGitStatusAfterTool(context.Background(), narratorID, narrator, "Edit", "tool-2")
	service.broadcastGitStatusAfterTool(context.Background(), narratorID, narrator, "Write", "tool-3")

	// No broadcast should fire immediately (still within the throttle window).
	if n := countBroadcastsOfType(drain(), narratorID, "git_status"); n != 0 {
		t.Fatalf("expected no immediate git_status during throttle window, got %d", n)
	}

	event := waitForBroadcast(t, drain, narratorID, "git_status")
	if event == nil {
		t.Fatal("expected a trailing git_status broadcast after the throttle window")
	}
	if event["toolUseId"] != "tool-3" {
		t.Fatalf("trailing git_status toolUseId = %v, want tool-3 (latest)", event["toolUseId"])
	}
	// Give any erroneously-scheduled extra timers a chance to fire, then ensure
	// the burst collapsed into exactly one broadcast.
	time.Sleep(gitStatusTrackThrottle + 200*time.Millisecond)
	if n := countBroadcastsOfType(drain(), narratorID, "git_status"); n != 1 {
		t.Fatalf("expected exactly one collapsed git_status broadcast, got %d", n)
	}
}

// TestBroadcastGitStatusAfterToolNonTrackingToolNoBroadcast verifies a
// non-file-mutating tool neither schedules a timer nor broadcasts git_status.
func TestBroadcastGitStatusAfterToolNonTrackingToolNoBroadcast(t *testing.T) {
	service, narratorID, drain := newGitTrackTestService(t)
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}

	service.broadcastGitStatusAfterTool(context.Background(), narratorID, narrator, "Read", "tool-read-1")
	time.Sleep(gitStatusTrackThrottle + 200*time.Millisecond)
	if n := countBroadcastsOfType(drain(), narratorID, "git_status"); n != 0 {
		t.Fatalf("non-tracking tool must not broadcast git_status, got %d", n)
	}
}

// TestClearGitTrackTimerCancelsPendingBroadcast verifies clearing the timer
// (as happens on run finalize) cancels a pending trailing broadcast.
func TestClearGitTrackTimerCancelsPendingBroadcast(t *testing.T) {
	service, narratorID, drain := newGitTrackTestService(t)
	narrator, err := service.Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}

	service.broadcastGitStatusAfterTool(context.Background(), narratorID, narrator, "Write", "tool-1")
	service.clearGitTrackTimer(narratorID)
	time.Sleep(gitStatusTrackThrottle + 200*time.Millisecond)
	if n := countBroadcastsOfType(drain(), narratorID, "git_status"); n != 0 {
		t.Fatalf("cleared timer must not broadcast git_status, got %d", n)
	}
}
