package handlers

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileMutationReadWaitsForCompletedWrite(t *testing.T) {
	for _, mode := range []string{"overwrite", "replacement", "create"} {
		for _, readKind := range []string{"ordinary", "protected"} {
			t.Run(mode+"/"+readKind, func(t *testing.T) {
				root := canonicalFixturePath(t, t.TempDir())
				real, alias := filepath.Join(root, "real"), filepath.Join(root, "alias")
				if err := os.Mkdir(real, 0700); err != nil {
					t.Fatal(err)
				}
				fileMutationDirectoryAlias(t, real, alias)
				path := filepath.Join(real, "file")
				release := acquireTestFileMutation(t, fileMutations, path)
				var writer *os.File
				if mode != "create" {
					var err error
					writer, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { writer.Close() })
					if _, err := writer.Write([]byte("partial")); err != nil {
						t.Fatal(err)
					}
					if mode == "replacement" {
						if err := writer.Close(); err != nil {
							t.Fatal(err)
						}
					}
				}
				h := New(NewPathGuard([]string{root}), 4096)
				params := map[string]any{"path": filepath.Join(alias, "file"), "timeoutMs": 2000}
				if readKind == "protected" {
					params["expectedResolvedPath"] = path
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var value any
				result := make(chan error, 1)
				go func() {
					var err error
					value, err = h.FsReadContext(ctx, params)
					result <- err
				}()
				waitFileMutationQueue(t, fileMutations, path, 1)
				// The same-file read waits while another file remains independently readable.
				other := filepath.Join(root, "other")
				if err := os.WriteFile(other, []byte("independent"), 0600); err != nil {
					t.Fatal(err)
				}
				otherValue, err := h.FsReadContext(context.Background(), map[string]any{"path": other, "timeoutMs": 1000})
				if err != nil || otherValue.(map[string]any)["dataB64"] != base64.StdEncoding.EncodeToString([]byte("independent")) {
					t.Fatalf("unrelated read was blocked: %v, %v", otherValue, err)
				}
				select {
				case err := <-result:
					t.Fatalf("read escaped unfinished mutation: %v", err)
				default:
				}
				completed := "partial completed"
				switch mode {
				case "overwrite":
					if _, err := writer.Write([]byte(" completed")); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
				case "replacement":
					staged := filepath.Join(real, "staged")
					if err := os.WriteFile(staged, []byte(completed), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(staged, path); err != nil {
						t.Fatal(err)
					}
				case "create":
					if err := os.WriteFile(path, []byte(completed), 0600); err != nil {
						t.Fatal(err)
					}
				}
				release()
				if err := fileMutationResult(t, result); err != nil {
					t.Fatal(err)
				}
				payload := value.(map[string]any)
				if payload["dataB64"] != base64.StdEncoding.EncodeToString([]byte(completed)) || payload["totalSize"] != int64(len(completed)) || payload["truncated"] != false || !samePath(payload["resolvedPath"].(string), path) {
					t.Fatalf("read returned an unfinished or pre-queue file: %#v", payload)
				}
				waitFileMutationQueue(t, fileMutations, path, -1)
			})
		}
	}
}

func TestFileMutationReadWaitingCancellation(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			root := canonicalFixturePath(t, t.TempDir())
			path := filepath.Join(root, "file")
			if err := os.WriteFile(path, []byte("partial"), 0600); err != nil {
				t.Fatal(err)
			}
			release := acquireTestFileMutation(t, fileMutations, path)
			h := New(NewPathGuard([]string{root}), 4096)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			params := map[string]any{"path": path, "expectedResolvedPath": path, "timeoutMs": 2000}
			if mode == "deadline" {
				params["timeoutMs"] = 20
			}
			var value any
			result := make(chan error, 1)
			go func() {
				var err error
				value, err = h.FsReadContext(ctx, params)
				result <- err
			}()
			want := context.DeadlineExceeded
			if mode == "cancel" {
				waitFileMutationQueue(t, fileMutations, path, 1)
				cancel()
				want = context.Canceled
			}
			if err := fileMutationResult(t, result); !errors.Is(err, want) {
				t.Fatalf("queued read ignored %s: %v", mode, err)
			}
			if value != nil {
				t.Fatalf("cancelled read exposed unfinished content: %#v", value)
			}
			waitFileMutationQueue(t, fileMutations, path, 0)
			release()
			if _, err := h.FsReadContext(context.Background(), map[string]any{"path": path}); err != nil {
				t.Fatalf("cancelled read blocked a fresh request: %v", err)
			}
			waitFileMutationQueue(t, fileMutations, path, -1)
		})
	}
}

func TestFileMutationReadRejectsChangedAliasAfterWaiting(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	real, other, alias := filepath.Join(root, "real"), filepath.Join(root, "other"), filepath.Join(root, "alias")
	for _, dir := range []string{real, other} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file"), []byte("private to this path"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	fileMutationDirectoryAlias(t, real, alias)
	path := filepath.Join(real, "file")
	release := acquireTestFileMutation(t, fileMutations, path)
	h := New(NewPathGuard([]string{root}), 4096)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var value any
	result := make(chan error, 1)
	go func() {
		var err error
		value, err = h.FsReadContext(ctx, map[string]any{"path": filepath.Join(alias, "file")})
		result <- err
	}()
	waitFileMutationQueue(t, fileMutations, path, 1)
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	fileMutationDirectoryAlias(t, other, alias)
	release()
	if err := fileMutationResult(t, result); err == nil || value != nil {
		t.Fatalf("read followed a different path without its queue: %#v, %v", value, err)
	}
	waitFileMutationQueue(t, fileMutations, path, -1)
}
