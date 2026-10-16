package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitStatusSchedulerQueue(t *testing.T, scheduler *gitStatusScheduler, want int) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		scheduler.mu.Lock()
		count := len(scheduler.queue)
		scheduler.mu.Unlock()
		if count == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("queued %d, expected %d", count, want)
		case <-tick.C:
		}
	}
}

func TestGitStatusSchedulerCancelledOwnerHoldsRootUntilRelease(t *testing.T) {
	scheduler := newGitStatusScheduler(4, 128)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release, err := scheduler.acquire(ctx, "root-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	waitingCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	type acquired struct {
		release func()
		err     error
	}
	next := make(chan acquired, 1)
	go func() { release, err := scheduler.acquire(waitingCtx, "root-a"); next <- acquired{release, err} }()
	waitStatusSchedulerQueue(t, scheduler, 1)
	// Other roots may use a free slot, even with root-a queued first.
	other, err := scheduler.acquire(waitingCtx, "root-b")
	if err != nil {
		t.Fatal(err)
	}
	other()
	cancel() // The transport now sees rejection, but cmd.Wait may still be running.
	select {
	case <-next:
		t.Fatal("cancellation released a running root before cmd.Wait/release")
	default:
	}
	scheduler.mu.Lock()
	active := scheduler.active["root-a"]
	scheduler.mu.Unlock()
	if !active {
		t.Fatal("cancelled owner lost its execution slot")
	}
	release() // Only the owner finishing all subprocess waits can release.
	result := <-next
	if result.err != nil {
		t.Fatal(result.err)
	}
	result.release()
	waitStatusSchedulerQueue(t, scheduler, 0)
}

func TestGitStatusSchedulerBoundsQueueAndCancelsWaiters(t *testing.T) {
	scheduler := newGitStatusScheduler(4, 128)
	for i := 0; i < 4; i++ {
		release, err := scheduler.acquire(context.Background(), fmt.Sprintf("running-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, 128)
	for i := 0; i < 128; i++ {
		go func(index int) {
			release, err := scheduler.acquire(ctx, fmt.Sprintf("queued-%d", index))
			if release != nil {
				release()
			}
			results <- err
		}(i)
	}
	waitStatusSchedulerQueue(t, scheduler, 128)
	overflowCtx, stopOverflow := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopOverflow()
	if _, err := scheduler.acquire(overflowCtx, "overflow"); err == nil || !strings.Contains(err.Error(), "queue is full") {
		t.Fatalf("queue overflow accepted: %v", err)
	}
	scheduler.mu.Lock()
	active := len(scheduler.active)
	scheduler.mu.Unlock()
	if active != 4 {
		t.Fatalf("running limit violated: %d", active)
	}
	cancel()
	for i := 0; i < 128; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("queued cancel: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled queued request did not exit promptly")
		}
	}
	waitStatusSchedulerQueue(t, scheduler, 0)
	deadlineCtx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, err := scheduler.acquire(deadlineCtx, "deadline"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued deadline: %v", err)
	}
	waitStatusSchedulerQueue(t, scheduler, 0)
}

func TestGitWorkspaceStatusUsesExecutorScheduler(t *testing.T) {
	root, handlers := newGitFixture(t)
	// A different cwd within the same repository must use the canonical root lock.
	cwd := filepath.Join(root, "nested")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	release, err := gitStatusQueries.acquire(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := handlers.GitWorkspace(ctx, map[string]any{"cwd": cwd, "expectedRoot": root, "operation": "status"})
		finished <- err
	}()
	waitStatusSchedulerQueue(t, gitStatusQueries, 1)
	select {
	case err := <-finished:
		t.Fatalf("same-root status bypassed scheduler: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued handler cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued handler did not cancel")
	}
	waitStatusSchedulerQueue(t, gitStatusQueries, 0)
	release()
	gitCall(t, handlers, root, "status", nil)
}

func assertStatusAdmissionActive(t *testing.T, want int) {
	t.Helper()
	gitStatusAdmissions.mu.Lock()
	active := len(gitStatusAdmissions.active)
	gitStatusAdmissions.mu.Unlock()
	if active != want {
		t.Fatalf("global status admission active=%d, want %d", active, want)
	}
}

func TestGitWorkspaceStatusAdmissionPrecedesAllGitCommands(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	handlers := NewWithOptions(NewPathGuard([]string{root}), gitWorkspaceMaxBytes, true)
	installGitStatusTestHelper(t)
	marker := filepath.Join(t.TempDir(), "git-started")
	t.Setenv("NF_GIT_STATUS_TEST_HELPER", "probe-marker")
	t.Setenv("NF_GIT_STATUS_TEST_MARKER", marker)
	var releases []func()
	for i := 0; i < 4; i++ {
		release, err := gitStatusAdmissions.admit(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		releases = append(releases, release)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	params := map[string]any{"cwd": root, "expectedRoot": root, "operation": "status"}
	results := make(chan error, 128)
	for i := 0; i < 128; i++ {
		go func() { _, err := handlers.GitWorkspace(ctx, params); results <- err }()
	}
	waitStatusSchedulerQueue(t, gitStatusAdmissions, 128)
	assertStatusAdmissionActive(t, 4)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("queued status started a Git probe: %v", err)
	}
	overflowCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if _, err := handlers.GitWorkspace(overflowCtx, params); err == nil || !strings.Contains(err.Error(), "queue is full") {
		t.Fatalf("full admission queue was not rejected: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("rejected status started a Git probe: %v", err)
	}
	cancel()
	for i := 0; i < 128; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("queued status cancellation: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("queued status did not cancel promptly")
		}
	}
	waitStatusSchedulerQueue(t, gitStatusAdmissions, 0)
	assertStatusAdmissionActive(t, 4)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("cancelled queued status started Git: %v", err)
	}
	for _, release := range releases {
		release()
	}
	// With admission available, the helper intentionally fails root discovery.
	// Failure must release the global slot even though no root lock was acquired.
	if _, err := handlers.GitWorkspace(overflowCtx, params); err == nil || !strings.Contains(err.Error(), "git rev-parse failed") {
		t.Fatalf("expected discovery failure: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("root failure did not exercise a real subprocess: %v", err)
	}
	assertStatusAdmissionActive(t, 0)
}

func TestGitStatusAdmissionCancellationDoesNotReleaseRunningOwner(t *testing.T) {
	scheduler := newGitStatusScheduler(1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release, err := scheduler.admit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	waitingCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	type acquired struct {
		release func()
		err     error
	}
	next := make(chan acquired, 1)
	go func() { release, err := scheduler.admit(waitingCtx); next <- acquired{release, err} }()
	waitStatusSchedulerQueue(t, scheduler, 1)
	cancel()
	select {
	case <-next:
		t.Fatal("cancelled owner released admission before subprocess wait completed")
	default:
	}
	scheduler.mu.Lock()
	active := len(scheduler.active)
	scheduler.mu.Unlock()
	if active != 1 {
		t.Fatalf("running admission was lost on cancellation: %d", active)
	}
	release()
	result := <-next
	if result.err != nil {
		t.Fatal(result.err)
	}
	result.release()
}

func TestGitWorkspaceStatusCancelledDiscoveryReleasesAdmission(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	handlers := NewWithOptions(NewPathGuard([]string{root}), gitWorkspaceMaxBytes, true)
	installGitStatusTestHelper(t)
	marker := filepath.Join(t.TempDir(), "probe-running")
	t.Setenv("NF_GIT_STATUS_TEST_HELPER", "probe-block")
	t.Setenv("NF_GIT_STATUS_TEST_MARKER", marker)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := handlers.GitWorkspace(ctx, map[string]any{"cwd": root, "expectedRoot": root, "operation": "status"})
		finished <- err
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case err := <-finished:
			t.Fatalf("probe exited before cancellation: %v", err)
		case <-deadline.C:
			t.Fatal("probe helper did not start")
		case <-tick.C:
		}
	}
	assertStatusAdmissionActive(t, 1)
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("discovery cancellation lost: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled probe did not exit")
	}
	assertStatusAdmissionActive(t, 0)
}
