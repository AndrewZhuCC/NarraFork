package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Observe actual enrollment rather than sleeping and assuming a goroutine ran.
func waitFileMutationQueue(t *testing.T, q *fileMutationQueue, path string, want int) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		q.mu.Lock()
		got := -1
		if queue := q.paths[fileMutationPathKey(path)]; queue != nil {
			got = queue.Len() - 1
		}
		q.mu.Unlock()
		if got == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("waiting mutations for %q: got %d, want %d", path, got, want)
		default:
			runtime.Gosched()
		}
	}
}

func fileMutationResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("mutation did not finish")
		return nil
	}
}

func fileMutationBarrier(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	ch := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(ch) }) }
	t.Cleanup(release)
	return ch, release
}

func acquireTestFileMutation(t *testing.T, q *fileMutationQueue, path string) func() {
	t.Helper()
	release, err := q.acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return release
}

func runQueuedFileMutation(q *fileMutationQueue, ctx context.Context, path string, run func() error) <-chan error {
	result := make(chan error, 1)
	go func() {
		result <- func() error {
			release, err := q.acquire(ctx, path)
			if err != nil {
				return err
			}
			defer release()
			return run()
		}()
	}()
	return result
}

func TestFileMutationQueueFIFOAndIndependentPaths(t *testing.T) {
	q := newFileMutationQueue(4, 8, time.Minute)
	root := canonicalFixturePath(t, t.TempDir())
	path := filepath.Join(root, "same")
	first := acquireTestFileMutation(t, q, path)
	order := make(chan int, 3)
	results := make([]<-chan error, 3)
	releases := make([]func(), 3)
	for i := range results {
		gate, release := fileMutationBarrier(t)
		releases[i] = release
		results[i] = runQueuedFileMutation(q, context.Background(), path, func() error {
			order <- i
			<-gate
			return nil
		})
		waitFileMutationQueue(t, q, path, i+1)
	}
	// A full queue on one file leaves other files immediately available.
	other := filepath.Join(root, "different")
	acquireTestFileMutation(t, q, other)()
	waitFileMutationQueue(t, q, other, -1)
	first()
	first() // releasing twice must not admit an extra same-path operation
	for i := range results {
		select {
		case got := <-order:
			if got != i {
				t.Fatalf("FIFO order: got %d, want %d", got, i)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("FIFO did not advance")
		}
		waitFileMutationQueue(t, q, path, len(results)-i-1)
		select {
		case got := <-order:
			t.Fatalf("same-path mutation %d overlapped %d", got, i)
		default:
		}
		releases[i]()
		if err := fileMutationResult(t, results[i]); err != nil {
			t.Fatal(err)
		}
	}
	waitFileMutationQueue(t, q, path, -1)
	if q.waiting != 0 {
		t.Fatal("queue retained waiting entries")
	}
}

func TestFileMutationQueueRemovesCancelledWaiters(t *testing.T) {
	q := newFileMutationQueue(4, 8, time.Minute)
	path := filepath.Join(t.TempDir(), "file")
	first := acquireTestFileMutation(t, q, path)
	contexts := make([]context.Context, 3)
	cancels := make([]context.CancelFunc, 3)
	results := make([]<-chan error, 3)
	ran := make(chan struct{}, 3)
	for i := range results {
		contexts[i], cancels[i] = context.WithCancel(context.Background())
		t.Cleanup(cancels[i])
		results[i] = runQueuedFileMutation(q, contexts[i], path, func() error {
			ran <- struct{}{}
			return nil
		})
		waitFileMutationQueue(t, q, path, i+1)
	}
	// Cancel the middle and then head while the first IO remains blocked.
	for i, index := range []int{1, 0} {
		cancels[index]()
		if err := fileMutationResult(t, results[index]); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled waiter: %v", err)
		}
		waitFileMutationQueue(t, q, path, 2-i)
	}
	select {
	case <-ran:
		t.Fatal("cancelled waiter ran or released the active operation")
	default:
	}
	first()
	if err := fileMutationResult(t, results[2]); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 {
		t.Fatal("surviving waiter did not run exactly once")
	}
	waitFileMutationQueue(t, q, path, -1)
}

func TestFileMutationQueueBoundsAndTimeout(t *testing.T) {
	t.Run("bounds", func(t *testing.T) {
		q := newFileMutationQueue(1, 2, time.Minute)
		root := t.TempDir()
		a, b, c := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")
		acquireTestFileMutation(t, q, a)
		acquireTestFileMutation(t, q, b)
		acquireTestFileMutation(t, q, c)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		pending := make([]<-chan error, 0, 2)
		for _, path := range []string{a, b} {
			pending = append(pending, runQueuedFileMutation(q, ctx, path, func() error { return nil }))
			waitFileMutationQueue(t, q, path, 1)
		}
		for _, path := range []string{a, c} {
			if release, err := q.acquire(context.Background(), path); err == nil {
				release()
				t.Fatal("queue capacity was exceeded")
			}
		}
		acquireTestFileMutation(t, q, filepath.Join(root, "unrelated"))()
		cancel()
		for _, result := range pending {
			if err := fileMutationResult(t, result); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		if q.waiting != 0 {
			t.Fatal("cancelled waiters retained capacity")
		}
	})
	t.Run("wait deadline only", func(t *testing.T) {
		q := newFileMutationQueue(4, 8, 20*time.Millisecond)
		path := filepath.Join(t.TempDir(), "file")
		first := acquireTestFileMutation(t, q, path)
		if release, err := q.acquire(context.Background(), path); !errors.Is(err, context.DeadlineExceeded) {
			if release != nil {
				release()
			}
			t.Fatalf("waiting deadline: %v", err)
		}
		waitFileMutationQueue(t, q, path, 0)
		first()
		acquireTestFileMutation(t, q, path)()
		waitFileMutationQueue(t, q, path, -1)
	})
}

func TestFileMutationQueueRunningCancellationAndFailures(t *testing.T) {
	for _, failure := range []string{"error", "panic"} {
		t.Run(failure, func(t *testing.T) {
			q := newFileMutationQueue(4, 8, time.Minute)
			path := filepath.Join(t.TempDir(), "file")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ioFinish, finish := fileMutationBarrier(t)
			started := make(chan struct{})
			first := make(chan error, 1)
			go func() {
				first <- func() (err error) {
					defer func() {
						if value := recover(); value != nil {
							err = fmt.Errorf("injected panic: %v", value)
						}
					}()
					release, err := q.acquire(ctx, path)
					if err != nil {
						return err
					}
					defer release()
					close(started)
					<-ioFinish // Model a started filesystem call that ignores cancellation.
					if err := os.WriteFile(path, []byte("first completed"), 0600); err != nil {
						return err
					}
					if failure == "panic" {
						panic("injected IO completion failure")
					}
					return errors.New("injected IO completion failure")
				}()
			}()
			<-started
			second := runQueuedFileMutation(q, context.Background(), path, func() error {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "first completed" {
					return fmt.Errorf("entered before real IO completion: %q, %v", data, err)
				}
				return nil
			})
			waitFileMutationQueue(t, q, path, 1)
			cancel()
			waitFileMutationQueue(t, q, path, 1)
			select {
			case err := <-second:
				t.Fatalf("running cancellation released IO prematurely: %v", err)
			default:
			}
			finish()
			if err := fileMutationResult(t, first); err == nil {
				t.Fatal("injected failure was hidden")
			}
			if err := fileMutationResult(t, second); err != nil {
				t.Fatal(err)
			}
			waitFileMutationQueue(t, q, path, -1)
		})
	}
}
