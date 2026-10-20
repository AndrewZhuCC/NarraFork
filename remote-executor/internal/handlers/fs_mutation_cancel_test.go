package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileMutationCancelledWhileQueued(t *testing.T) {
	for _, method := range []string{"write", "remove", "conditional", "exclusive", "transfer"} {
		t.Run(method, func(t *testing.T) {
			root := canonicalFixturePath(t, t.TempDir())
			path := filepath.Join(root, "file")
			if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			h := New(NewPathGuard([]string{root}), 4000000)
			release := acquireTestFileMutation(t, fileMutations, path)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := runFileMutationMethod(ctx, h, method, path, "before")
				result <- err
			}()
			waitFileMutationQueue(t, fileMutations, path, 1)
			cancel()
			if err := fileMutationResult(t, result); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v", err)
			}
			waitFileMutationQueue(t, fileMutations, path, 0)
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "before" {
				t.Fatalf("cancelled mutation altered original: %q %v", data, err)
			}
			release()
			if _, err := runFileMutationMethod(context.Background(), h, "write", path, nil); err != nil {
				t.Fatalf("cancelled request blocked a new write: %v", err)
			}
			waitFileMutationQueue(t, fileMutations, path, -1)
		})
	}
}

func TestAcquireFileMutationAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "file")
	for i := 0; i < 100; i++ {
		release, err := acquireFileMutation(ctx, path)
		if release != nil {
			release()
			t.Fatal("cancelled context acquired queue")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	}
	waitFileMutationQueue(t, fileMutations, path, -1)
}

func TestConditionalWriteTimeoutWhileQueued(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	path := filepath.Join(root, "file")
	acquireTestFileMutation(t, fileMutations, path)
	h := New(NewPathGuard([]string{root}), 4000000)
	p := conditionalParams(path, nil, "after")
	p["timeoutMs"] = 1
	if _, err := h.FsWriteConditional(context.Background(), p); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("timed out write created file")
	}
	for _, timeout := range []int{0, -1, 30001} {
		p["timeoutMs"] = timeout
		if _, err := h.FsWriteConditional(context.Background(), p); err == nil {
			t.Fatal("invalid timeout accepted")
		}
	}
}
