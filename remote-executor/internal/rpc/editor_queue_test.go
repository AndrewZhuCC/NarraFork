package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/narrafork/remote-executor/internal/handlers"
)

func TestEditorReadOpenWaitsForTargetBeforeOpening(t *testing.T) {
	d, req := editorFixture(t)
	release, err := d.h.AcquireEditorFileRead(context.Background(), req.Identity.CanonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	result := make(chan any, 1)
	failed := make(chan error, 1)
	go func() {
		value, err := d.editorReads.open(context.Background(), req)
		if err != nil {
			failed <- err
		} else {
			result <- value
		}
	}()
	select {
	case err := <-failed:
		t.Fatal(err)
	case <-result:
		t.Fatal("raw preflight bypassed target queue")
	case <-time.After(20 * time.Millisecond):
	}
	content := []byte("writer completed")
	if err := os.WriteFile(req.Identity.CanonicalPath, content, 0600); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-failed:
		t.Fatal(err)
	case raw := <-result:
		opened := raw.(EditorRawReadOpen)
		digest := sha256.Sum256(content)
		if opened.RawDigest != hex.EncodeToString(digest[:]) || opened.TotalBytes != int64(len(content)) {
			t.Fatal("preflight opened stale source", opened)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("read did not resume after writer")
	}
}

func TestEditorReadQueueCancellationClosesOnlyItsRead(t *testing.T) {
	for _, method := range []string{"open", "chunk"} {
		t.Run(method, func(t *testing.T) {
			d, req := editorFixture(t)
			var opened EditorRawReadOpen
			var handle *editorRead
			if method == "chunk" {
				opened = editorOpen(t, d, req)
				handle = editorHandle(d, opened.ReadID)
			}
			release, err := d.h.AcquireEditorFileRead(context.Background(), req.Identity.CanonicalPath)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if method == "open" {
				_, err = d.editorReads.open(ctx, req)
			} else {
				_, err = d.editorReads.chunk(ctx, EditorRawReadChunkRequest{ReadID: opened.ReadID, MaxBytes: 1, TimeoutMs: 1000})
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("queued %s ignored cancellation: %v", method, err)
			}
			if handle != nil {
				editorAssertClosed(t, d, handle)
			}
			// Cancelled read admission must not release the current writer's slot.
			otherCtx, otherCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer otherCancel()
			if unlock, err := d.h.AcquireEditorFileRead(otherCtx, req.Identity.CanonicalPath); err == nil {
				unlock()
				t.Fatal("cancelled reader released another owner's slot")
			}
			release()
			fresh := editorOpen(t, d, req)
			if fresh.ReadID == "" {
				t.Fatal("target could not be retried")
			}
		})
	}
}

func TestEditorReadChunkRechecksTargetAfterQueuedReplacement(t *testing.T) {
	d, req := editorFixture(t)
	opened := editorOpen(t, d, req)
	handle := editorHandle(d, opened.ReadID)
	release, err := d.h.AcquireEditorFileRead(context.Background(), req.Identity.CanonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	finished := make(chan error, 1)
	go func() {
		_, err := d.editorReads.chunk(context.Background(), EditorRawReadChunkRequest{ReadID: opened.ReadID, MaxBytes: 1, TimeoutMs: 1000})
		finished <- err
	}()
	select {
	case err := <-finished:
		t.Fatalf("chunk bypassed pending writer: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	// A controlled writer performs this mutation while retaining the shared slot.
	if err := os.WriteFile(req.Identity.CanonicalPath, []byte("changed-length"), 0600); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-finished:
		if err == nil || !strings.Contains(err.Error(), "EDITOR_IDENTITY_CHANGED") {
			t.Fatalf("stale read was delivered: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued chunk did not finish")
	}
	editorAssertClosed(t, d, handle)
}

func TestEditorApplyUsesSameConnectionBindingAsRawRead(t *testing.T) {
	for _, field := range []string{"generation", "epoch", "fencingToken", "root"} {
		t.Run(field, func(t *testing.T) {
			d, req := editorFixture(t)
			h := handlers.New(handlers.NewPathGuard([]string{filepath.Dir(req.Identity.CanonicalPath)}), 128*1024)
			engine, err := handlers.NewEditorEngine(h, filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			h.SetEditorEngine(engine)
			// Keep the already established read-connection tuple and add a writer.
			d.h = h
			editorOpen(t, d, req)
			identity := req.Identity
			switch field {
			case "generation":
				identity.RuntimeGeneration++
			case "epoch":
				identity.RuntimeEpoch = "old"
			case "fencingToken":
				identity.FencingToken++
			case "root":
				identity.WorkspaceIdentity = "invalid"
			}
			params := map[string]any{"coordination": handlers.EditorTargetQueueCoordination, "identity": identity, "expectedRawHash": nil}
			if _, err := d.Dispatch(context.Background(), "editor.apply", params, nil); err == nil || (!strings.Contains(err.Error(), "EDITOR_RUNTIME_CHANGED") && !strings.Contains(err.Error(), "EDITOR_FENCING_REJECTED") && !strings.Contains(err.Error(), "EDITOR_IDENTITY_CHANGED")) {
				t.Fatalf("%s binding not checked before apply: %v", field, err)
			}
		})
	}
}
