package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func checkpointUploadParams(path, id string) map[string]any {
	return map[string]any{
		"transferId": id, "direction": "upload", "remotePath": path,
		"fileSize": 8, "chunkSize": 4, "totalChunks": 2,
	}
}

func completeCheckpointUpload(t *testing.T, transfers *Transfers, id string) {
	t.Helper()
	transfers.WriteChunk(id, 0, []byte("next"))
	transfers.WriteChunk(id, 1, []byte("data"))
	result, err := transfers.Complete(context.Background(), map[string]any{"transferId": id})
	if err != nil || result.(map[string]any)["ok"] != true {
		t.Fatalf("complete checkpoint: %v, %v", result, err)
	}
}

func requireCheckpointBusy(t *testing.T, transfers *Transfers, path, id string) {
	t.Helper()
	if _, err := transfers.Begin(context.Background(), checkpointUploadParams(path, id)); err == nil || !strings.Contains(err.Error(), "checkpoint is busy") {
		t.Fatalf("shared checkpoint was accepted: %v", err)
	}
	if transfers.get(id) != nil {
		t.Fatal("busy request retained a receiver")
	}
}

func TestFileMutationTransferCheckpointSingleReceiverAcrossConnections(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	real, alias := filepath.Join(root, "real"), filepath.Join(root, "alias")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	fileMutationDirectoryAlias(t, real, alias)
	path := filepath.Join(real, "file")
	h := New(NewPathGuard([]string{root}), 4000000)
	first := NewTransfers(h, transferPathNopSender{})
	second := NewTransfers(h, transferPathNopSender{})
	defer first.Close()
	defer second.Close()
	if _, err := first.Begin(context.Background(), checkpointUploadParams(path, "first")); err != nil {
		t.Fatal(err)
	}
	first.WriteChunk("first", 0, []byte("AAAA"))
	requireCheckpointBusy(t, second, filepath.Join(alias, "file"), "competing")
	if data, err := os.ReadFile(path + ".nfpart"); err != nil || string(data) != "AAAA" {
		t.Fatalf("competing receiver changed checkpoint: %q, %v", data, err)
	}
	// Another destination and ordinary writes remain independent of this receive.
	if _, err := second.Begin(context.Background(), checkpointUploadParams(filepath.Join(root, "other"), "other")); err != nil {
		t.Fatal(err)
	}
	completeCheckpointUpload(t, second, "other")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := runFileMutationMethod(ctx, h, "write", path, nil); err != nil {
		t.Fatalf("receiving occupied the target file queue: %v", err)
	}
	first.WriteChunk("first", 1, []byte("BBBB"))
	release := acquireTestFileMutation(t, fileMutations, path)
	finished := make(chan error, 1)
	go func() {
		result, err := first.Complete(context.Background(), map[string]any{"transferId": "first"})
		if err == nil && result.(map[string]any)["ok"] != true {
			err = fmt.Errorf("commit failed: %v", result)
		}
		finished <- err
	}()
	waitFileMutationQueue(t, fileMutations, path, 1)
	requireCheckpointBusy(t, second, path, "while-committing")
	release()
	if err := fileMutationResult(t, finished); err != nil {
		t.Fatal(err)
	}
	first.WriteChunk("first", 0, []byte("late"))
	if data, err := os.ReadFile(path); err != nil || string(data) != "AAAABBBB" {
		t.Fatalf("late receiver modified committed target: %q, %v", data, err)
	}
	if _, err := second.Begin(context.Background(), checkpointUploadParams(path, "fresh")); err != nil {
		t.Fatalf("completed transfer retained checkpoint: %v", err)
	}
	completeCheckpointUpload(t, second, "fresh")
}

func TestFileMutationTransferCheckpointReleasedOnStopAndFailure(t *testing.T) {
	for _, stage := range []string{"cancel", "pause", "close", "size-failure", "open-failure", "registration-failure"} {
		t.Run(stage, func(t *testing.T) {
			root := canonicalFixturePath(t, t.TempDir())
			path := filepath.Join(root, "file")
			h := New(NewPathGuard([]string{root}), 4000000)
			first := NewTransfers(h, transferPathNopSender{})
			second := NewTransfers(h, transferPathNopSender{})
			defer first.Close()
			defer second.Close()
			if stage == "open-failure" {
				if err := os.Mkdir(path+".nfpart", 0700); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "registration-failure" {
				first.Close()
			}
			_, err := first.Begin(context.Background(), checkpointUploadParams(path, "first"))
			if stage == "open-failure" || stage == "registration-failure" {
				if err == nil {
					t.Fatal("injected begin failure succeeded")
				}
				if stage == "open-failure" {
					if err := os.Remove(path + ".nfpart"); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				first.WriteChunk("first", 0, []byte("part"))
				switch stage {
				case "cancel", "pause":
					if _, err := first.Abort(map[string]any{"transferId": "first", "preservePartial": stage == "pause"}); err != nil {
						t.Fatal(err)
					}
				case "close":
					first.Close()
				case "size-failure":
					result, err := first.Complete(context.Background(), map[string]any{"transferId": "first"})
					if err != nil || result.(map[string]any)["ok"] != false {
						t.Fatalf("expected incomplete upload failure: %v, %v", result, err)
					}
				}
			}
			if _, err := second.Begin(context.Background(), checkpointUploadParams(path, "next")); err != nil {
				t.Fatalf("%s retained checkpoint ownership: %v", stage, err)
			}
			completeCheckpointUpload(t, second, "next")
		})
	}
}

func TestFileMutationTransferCheckpointHeldUntilReceiverIOCloses(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	path := filepath.Join(root, "file")
	h := New(NewPathGuard([]string{root}), 4000000)
	first := NewTransfers(h, transferPathNopSender{})
	second := NewTransfers(h, transferPathNopSender{})
	defer first.Close()
	defer second.Close()
	if _, err := first.Begin(context.Background(), checkpointUploadParams(path, "first")); err != nil {
		t.Fatal(err)
	}
	sess := first.get("first")
	// WriteChunk holds this mutex until actual WriteAt finishes. Abort may take
	// the session from its map, but must keep ownership until this IO can close.
	sess.recvMu.Lock()
	locked := true
	defer func() {
		if locked {
			sess.recvMu.Unlock()
		}
	}()
	stopped := make(chan error, 1)
	go func() {
		_, err := first.Abort(map[string]any{"transferId": "first"})
		stopped <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for first.get("first") != nil {
		if time.Now().After(deadline) {
			t.Fatal("abort did not start")
		}
		runtime.Gosched()
	}
	requireCheckpointBusy(t, second, path, "during-io")
	sess.recvMu.Unlock()
	locked = false
	if err := fileMutationResult(t, stopped); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Begin(context.Background(), checkpointUploadParams(path, "after-io")); err != nil {
		t.Fatal(err)
	}
	completeCheckpointUpload(t, second, "after-io")
}

func TestFileMutationTransferCheckpointOldReleaseCannotClearNewOwner(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	path := filepath.Join(root, "file")
	h := New(NewPathGuard([]string{root}), 4000000)
	first := NewTransfers(h, transferPathNopSender{})
	second := NewTransfers(h, transferPathNopSender{})
	third := NewTransfers(h, transferPathNopSender{})
	defer first.Close()
	defer second.Close()
	defer third.Close()
	if _, err := first.Begin(context.Background(), checkpointUploadParams(path, "first")); err != nil {
		t.Fatal(err)
	}
	oldOwner := first.get("first")
	if _, err := first.Abort(map[string]any{"transferId": "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Begin(context.Background(), checkpointUploadParams(path, "second")); err != nil {
		t.Fatal(err)
	}
	// A delayed duplicate cleanup must never delete the new receiver's entry.
	oldOwner.releaseCheckpoint()
	oldOwner.releaseCheckpoint()
	requireCheckpointBusy(t, third, path, "third")
	completeCheckpointUpload(t, second, "second")
	if _, err := third.Begin(context.Background(), checkpointUploadParams(path, "third")); err != nil {
		t.Fatal(err)
	}
	completeCheckpointUpload(t, third, "third")
}

func TestFileMutationTransferCheckpointReleasedAfterQueuedCommitCancellation(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	path := filepath.Join(root, "file")
	h := New(NewPathGuard([]string{root}), 4000000)
	first := NewTransfers(h, transferPathNopSender{})
	second := NewTransfers(h, transferPathNopSender{})
	defer first.Close()
	defer second.Close()
	if _, err := first.Begin(context.Background(), checkpointUploadParams(path, "first")); err != nil {
		t.Fatal(err)
	}
	first.WriteChunk("first", 0, []byte("part"))
	first.WriteChunk("first", 1, []byte("data"))
	release := acquireTestFileMutation(t, fileMutations, path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := first.Complete(ctx, map[string]any{"transferId": "first"})
		finished <- err
	}()
	waitFileMutationQueue(t, fileMutations, path, 1)
	requireCheckpointBusy(t, second, path, "competing")
	cancel()
	if err := fileMutationResult(t, finished); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := second.Begin(context.Background(), checkpointUploadParams(path, "after-cancel")); err != nil {
		t.Fatal(err)
	}
	release()
	completeCheckpointUpload(t, second, "after-cancel")
}
