package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func runFileMutationMethod(ctx context.Context, h *Handlers, method, path string, before any) (any, error) {
	switch method {
	case "write":
		return h.FsWriteContext(ctx, map[string]any{
			"path": path, "dataB64": base64.StdEncoding.EncodeToString([]byte("after")),
		})
	case "remove":
		return h.FsRemoveContext(ctx, map[string]any{"path": path})
	case "conditional":
		return h.FsWriteConditional(ctx, conditionalParams(path, before, "after"))
	case "exclusive":
		resolved, err := h.guard.CheckCreate(path)
		if err != nil {
			return nil, err
		}
		return h.FsWriteContext(ctx, map[string]any{
			"path": path, "expectedResolvedPath": resolved, "exclusive": true,
			"dataB64": base64.StdEncoding.EncodeToString([]byte("after")),
		})
	case "transfer":
		// A separate connection context proves that the completing RPC's own
		// cancellation reaches the queue without closing the connection.
		transfers := NewTransfers(h, transferPathNopSender{})
		defer transfers.Close()
		if _, err := transfers.Begin(context.Background(), map[string]any{
			"transferId": "mutation", "direction": "upload", "remotePath": path,
			"fileSize": 5, "totalChunks": 1, "chunkSize": 5,
		}); err != nil {
			return nil, err
		}
		transfers.WriteChunk("mutation", 0, []byte("after"))
		return transfers.Complete(ctx, map[string]any{"transferId": "mutation"})
	default:
		return nil, fmt.Errorf("unknown mutation test method %s", method)
	}
}

func fileMutationMethodError(method string, result any, err error) error {
	if err != nil {
		return err
	}
	if method == "conditional" && result.(map[string]any)["applied"] != true {
		return fmt.Errorf("conditional mutation failed: %v", result)
	}
	if method == "transfer" && result.(map[string]any)["ok"] != true {
		return fmt.Errorf("transfer mutation failed: %v", result)
	}
	return nil
}

func fileMutationDirectoryAlias(t *testing.T, target, alias string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		requireWindowsJunction(t, target, alias)
	} else {
		requireSymlink(t, target, alias)
	}
}

func TestFileMutationHandlersShareCanonicalPathQueue(t *testing.T) {
	for _, method := range []string{"write", "remove", "conditional", "exclusive", "transfer"} {
		t.Run(method, func(t *testing.T) {
			root := canonicalFixturePath(t, t.TempDir())
			real, alias := filepath.Join(root, "real"), filepath.Join(root, "alias")
			if err := os.Mkdir(real, 0700); err != nil {
				t.Fatal(err)
			}
			fileMutationDirectoryAlias(t, real, alias)
			path, aliasPath := filepath.Join(real, "file"), filepath.Join(alias, "file")
			otherPath := filepath.Join(real, "different")
			if method == "remove" {
				for _, p := range []string{path, otherPath} {
					if err := os.WriteFile(p, []byte("before"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			first := acquireTestFileMutation(t, fileMutations, path)
			// A distinct Handler represents another connection to the same executor.
			h := New(NewPathGuard([]string{root}), 4000000)
			pending := make(chan error, 1)
			go func() {
				result, err := runFileMutationMethod(context.Background(), h, method, aliasPath, nil)
				pending <- fileMutationMethodError(method, result, err)
			}()
			waitFileMutationQueue(t, fileMutations, path, 1)
			other := New(NewPathGuard([]string{root}), 4000000)
			result, err := runFileMutationMethod(context.Background(), other, method, otherPath, nil)
			if err := fileMutationMethodError(method, result, err); err != nil {
				t.Fatalf("unrelated path was blocked: %v", err)
			}
			waitFileMutationQueue(t, fileMutations, path, 1)
			select {
			case err := <-pending:
				t.Fatalf("alias escaped canonical path queue: %v", err)
			default:
			}
			first()
			if err := fileMutationResult(t, pending); err != nil {
				t.Fatal(err)
			}
			waitFileMutationQueue(t, fileMutations, path, -1)
		})
	}
}

func TestFileMutationChangedCanonicalPathWhileQueued(t *testing.T) {
	for _, method := range []string{"write", "remove", "conditional", "exclusive", "transfer"} {
		t.Run(method, func(t *testing.T) {
			root := canonicalFixturePath(t, t.TempDir())
			real, other, saved := filepath.Join(root, "real"), filepath.Join(root, "other"), filepath.Join(root, "saved")
			for _, dir := range []string{real, other} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(real, "file")
			var before any
			if method != "exclusive" {
				before = "before"
				if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			otherPath := filepath.Join(other, "file")
			if err := os.WriteFile(otherPath, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			h := New(NewPathGuard([]string{root}), 4000000)
			first := acquireTestFileMutation(t, fileMutations, path)
			result := make(chan error, 1)
			go func() {
				value, err := runFileMutationMethod(context.Background(), h, method, path, before)
				result <- fileMutationMethodError(method, value, err)
			}()
			waitFileMutationQueue(t, fileMutations, path, 1)
			if err := os.Rename(real, saved); err != nil {
				t.Fatal(err)
			}
			fileMutationDirectoryAlias(t, other, real)
			first()
			if err := fileMutationResult(t, result); err == nil {
				t.Fatal("mutation followed a changed canonical path")
			}
			for p, want := range map[string]string{filepath.Join(saved, "file"): "before", otherPath: "untouched"} {
				if before == nil && p != otherPath {
					if _, err := os.Stat(p); !os.IsNotExist(err) {
						t.Fatal("rejected exclusive mutation created the original file")
					}
					continue
				}
				data, err := os.ReadFile(p)
				if err != nil || string(data) != want {
					t.Fatalf("changed alias altered %s: %q, %v", p, data, err)
				}
			}
			waitFileMutationQueue(t, fileMutations, path, -1)
			if _, err := runFileMutationMethod(context.Background(), h, "write", path, nil); err != nil {
				t.Fatalf("fresh request could not use current path: %v", err)
			}
		})
	}
}

func TestFileMutationHandlerFailureIsLocal(t *testing.T) {
	for _, method := range []string{"write", "remove", "conditional", "exclusive", "transfer"} {
		t.Run(method, func(t *testing.T) {
			root := canonicalFixturePath(t, t.TempDir())
			path := filepath.Join(root, "directory")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			h := New(NewPathGuard([]string{root}), 4000000)
			first := acquireTestFileMutation(t, fileMutations, path)
			pending := make(chan error, 1)
			go func() {
				result, err := runFileMutationMethod(context.Background(), h, method, path, nil)
				pending <- fileMutationMethodError(method, result, err)
			}()
			waitFileMutationQueue(t, fileMutations, path, 1)
			first()
			if err := fileMutationResult(t, pending); err == nil {
				t.Fatal("directory mutation unexpectedly succeeded")
			}
			waitFileMutationQueue(t, fileMutations, path, -1)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if _, err := runFileMutationMethod(context.Background(), h, "write", path, nil); err != nil {
				t.Fatalf("failed request poisoned the path: %v", err)
			}
		})
	}
}

func TestFileMutationRemovalLocksEntryInsteadOfSymlinkReferent(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	path, alias := filepath.Join(root, "target"), filepath.Join(root, "link")
	if err := os.WriteFile(path, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, path, alias)
	acquireTestFileMutation(t, fileMutations, path)
	h := New(NewPathGuard([]string{root}), 4000000)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := h.FsRemoveContext(ctx, map[string]any{"path": alias}); err != nil {
		t.Fatalf("removing a separate directory entry waited for the referent: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "untouched" {
		t.Fatalf("symlink removal changed referent: %q, %v", data, err)
	}
	if _, err := os.Lstat(alias); !os.IsNotExist(err) {
		t.Fatal("symlink entry was not removed")
	}
}

func TestFileMutationExclusiveCancellationWaitsForBoundIO(t *testing.T) {
	f := newExclusiveFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	ioFinish, finish := fileMutationBarrier(t)
	ioFailure := errors.New("injected bound-parent IO failure")
	first := make(chan error, 1)
	go func() {
		_, err := f.h.fsWriteExclusiveWithParent(ctx, f.params(), func(ctx context.Context, path string) (*os.Root, string, error) {
			root, _, err := openExclusiveParent(ctx, path)
			close(started)
			if err != nil {
				return nil, "", err
			}
			defer root.Close()
			<-ioFinish
			return nil, "", ioFailure
		})
		first <- err
	}()
	<-started
	second := make(chan error, 1)
	go func() {
		_, err := runFileMutationMethod(context.Background(), f.h, "write", f.path, nil)
		second <- err
	}()
	waitFileMutationQueue(t, fileMutations, f.path, 1)
	cancel()
	waitFileMutationQueue(t, fileMutations, f.path, 1)
	if _, err := runFileMutationMethod(context.Background(), f.h, "write", filepath.Join(f.parent, "different"), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-second:
		t.Fatalf("exclusive cancellation released a running handler: %v", err)
	default:
	}
	finish()
	if err := fileMutationResult(t, first); !errors.Is(err, ioFailure) {
		t.Fatal(err)
	}
	if err := fileMutationResult(t, second); err != nil {
		t.Fatal(err)
	}
	waitFileMutationQueue(t, fileMutations, f.path, -1)
}

func TestFileMutationTransferConnectionCancellation(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	path := filepath.Join(root, "file")
	acquireTestFileMutation(t, fileMutations, path)
	ctx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	h := New(NewPathGuard([]string{root}), 4000000)
	transfers := NewTransfersWithContext(ctx, h, transferPathNopSender{})
	defer transfers.Close()
	if _, err := transfers.Begin(context.Background(), map[string]any{
		"transferId": "disconnect", "direction": "upload", "remotePath": path,
		"fileSize": 5, "totalChunks": 1, "chunkSize": 5,
	}); err != nil {
		t.Fatal(err)
	}
	transfers.WriteChunk("disconnect", 0, []byte("after"))
	rpcCtx, cancelRPC := context.WithCancel(ctx)
	defer cancelRPC()
	pending := make(chan error, 1)
	go func() {
		_, err := transfers.Complete(rpcCtx, map[string]any{"transferId": "disconnect"})
		pending <- err
	}()
	waitFileMutationQueue(t, fileMutations, path, 1)
	disconnect()
	if err := fileMutationResult(t, pending); !errors.Is(err, context.Canceled) {
		t.Fatalf("disconnect did not cancel queued commit: %v", err)
	}
	waitFileMutationQueue(t, fileMutations, path, 0)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("disconnected commit created destination")
	}
	if data, err := os.ReadFile(path + ".nfpart"); err != nil || string(data) != "after" {
		t.Fatalf("cancelled commit lost its staged content: %q, %v", data, err)
	}
}
