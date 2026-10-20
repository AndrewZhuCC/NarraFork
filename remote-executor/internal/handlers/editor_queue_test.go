package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func editorApplyResultError(result any, err error) error {
	if err != nil {
		return err
	}
	if result.(EditorApplyResult).Outcome != "committed" {
		return fmt.Errorf("editor did not commit: %+v", result)
	}
	return nil
}

func secondEditorForTarget(t *testing.T, f *editorFixture) *editorFixture {
	t.Helper()
	h := New(NewPathGuard([]string{f.root}), 128*1024)
	state := filepath.Join(t.TempDir(), "state")
	e, err := NewEditorEngine(h, state)
	if err != nil {
		t.Fatal(err)
	}
	h.SetEditorEngine(e)
	return &editorFixture{h: h, e: e, root: f.root, path: f.path, state: state, identity: f.identity}
}

func TestEditorTargetQueueSerializesAllWritersUntilRealCompletion(t *testing.T) {
	for _, writer := range []string{"write", "transfer", "editor"} {
		t.Run(writer, func(t *testing.T) {
			f := newEditorFixture(t, []byte("before"))
			req := f.request(t, "first", []byte("before"), []byte("after"))
			params := editorParams(t, req)
			var next func() error
			if writer == "editor" {
				g := secondEditorForTarget(t, f)
				nextParams := editorParams(t, g.request(t, "next", []byte("after"), []byte("after")))
				next = func() error {
					result, err := g.h.EditorDispatch(context.Background(), "editor.apply", nextParams)
					return editorApplyResultError(result, err)
				}
			} else {
				next = func() error {
					result, err := runFileMutationMethod(context.Background(), f.h, writer, f.path, nil)
					return fileMutationMethodError(writer, result, err)
				}
			}
			renamed, finish := make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(finish) }) }
			defer release()
			f.e.fault = func(point string) error {
				if point == "renamed" {
					close(renamed)
					<-finish
				}
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := make(chan error, 1)
			go func() {
				result, err := f.h.EditorDispatch(ctx, "editor.apply", params)
				first <- editorApplyResultError(result, err)
			}()
			select {
			case <-renamed:
			case <-time.After(3 * time.Second):
				t.Fatal("native replacement did not execute")
			}
			cancel() // This RPC ending cannot release native durability/receipt work.
			second := make(chan error, 1)
			go func() { second <- next() }()
			waitFileMutationQueue(t, fileMutations, f.path, 1)
			select {
			case err := <-first:
				t.Fatalf("cancel released unfinished editor IO: %v", err)
			default:
			}
			select {
			case err := <-second:
				t.Fatalf("writer bypassed editor target slot: %v", err)
			default:
			}
			otherCtx, otherCancel := context.WithTimeout(context.Background(), time.Second)
			defer otherCancel()
			if _, err := runFileMutationMethod(otherCtx, f.h, "write", filepath.Join(f.root, "unrelated"), nil); err != nil {
				t.Fatalf("editor globally blocked unrelated file: %v", err)
			}
			release()
			if err := fileMutationResult(t, first); err != nil {
				t.Fatal(err)
			}
			if err := fileMutationResult(t, second); err != nil {
				t.Fatal(err)
			}
			if q := f.query(t, req); q.Outcome != "committed" {
				t.Fatal("cancel hid completed IO", q)
			}
			waitFileMutationQueue(t, fileMutations, f.path, -1)
		})
	}
}

func TestEditorQueuedApplyCancellationReleasesOnlyItsWaiter(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	req := f.request(t, "cancelled-queue", []byte("before"), []byte("after"))
	release := acquireTestFileMutation(t, fileMutations, f.path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	params := editorParams(t, req)
	finished := make(chan error, 1)
	go func() {
		result, err := f.h.EditorDispatch(ctx, "editor.apply", params)
		if err == nil && result.(EditorApplyResult).Outcome != "rejected" {
			err = fmt.Errorf("cancelled waiter was applied: %+v", result)
		}
		finished <- err
	}()
	waitFileMutationQueue(t, fileMutations, f.path, 1)
	cancel()
	if err := fileMutationResult(t, finished); err != nil {
		t.Fatal(err)
	}
	waitFileMutationQueue(t, fileMutations, f.path, 0)
	if data, err := os.ReadFile(f.path); err != nil || string(data) != "before" {
		t.Fatal("cancel changed target", string(data), err)
	}
	release()
	if _, err := runFileMutationMethod(context.Background(), f.h, "write", f.path, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEditorApplyRevalidatesBaselineAfterQueuedWriter(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	req := f.request(t, "stale-baseline", []byte("before"), []byte("editor"))
	release := acquireTestFileMutation(t, fileMutations, f.path)
	params := editorParams(t, req)
	finished := make(chan error, 1)
	go func() {
		result, err := f.h.EditorDispatch(context.Background(), "editor.apply", params)
		if err == nil && result.(EditorApplyResult).Outcome != "rejected" {
			err = fmt.Errorf("stale baseline installed: %+v", result)
		}
		finished <- err
	}()
	waitFileMutationQueue(t, fileMutations, f.path, 1)
	if err := os.WriteFile(f.path, []byte("queued writer"), 0640); err != nil {
		t.Fatal(err)
	}
	release()
	if err := fileMutationResult(t, finished); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(f.path); err != nil || string(data) != "queued writer" {
		t.Fatal("editor overwrote queued writer", string(data), err)
	}
}

func TestEditorApplyRejectsRedirectedTargetAfterQueueAdmission(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	root := filepath.Dir(f.path)
	parent, other := filepath.Join(root, "parent"), filepath.Join(root, "other")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "file")
	if err := os.WriteFile(path, []byte("before"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "file"), []byte("untouched"), 0640); err != nil {
		t.Fatal(err)
	}
	req := f.request(t, "redirected", []byte("before"), []byte("editor"))
	req.Identity.CanonicalPath = path
	release := acquireTestFileMutation(t, fileMutations, path)
	params := editorParams(t, req)
	finished := make(chan error, 1)
	go func() {
		result, err := f.h.EditorDispatch(context.Background(), "editor.apply", params)
		if err == nil && result.(EditorApplyResult).Outcome != "rejected" {
			err = fmt.Errorf("redirected target accepted: %+v", result)
		}
		finished <- err
	}()
	waitFileMutationQueue(t, fileMutations, path, 1)
	if err := os.Rename(parent, parent+"-old"); err != nil {
		t.Fatal(err)
	}
	fileMutationDirectoryAlias(t, other, parent)
	release()
	if err := fileMutationResult(t, finished); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{filepath.Join(parent+"-old", "file"): "before", filepath.Join(other, "file"): "untouched"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Fatal("redirected editor changed a file", path, string(data), err)
		}
	}
}

func TestEditorQueryCannotRecoverLiveIntentOrApplyingJournal(t *testing.T) {
	for _, boundary := range []string{"intent", "applying", "renamed"} {
		t.Run(boundary, func(t *testing.T) {
			f := newEditorFixture(t, []byte("before"))
			req := f.request(t, "live", []byte("before"), []byte("after"))
			entered, finish := make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(finish) }) }
			defer release()
			f.e.fault = func(point string) error {
				if point == boundary {
					close(entered)
					<-finish
				}
				return nil
			}
			params := editorParams(t, req)
			finished := make(chan error, 1)
			go func() {
				result, err := f.h.EditorDispatch(context.Background(), "editor.apply", params)
				finished <- editorApplyResultError(result, err)
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("apply did not enter boundary")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			query := EditorQueryRequest{Namespace: req.Namespace, ResourceKey: req.ResourceKey, OperationID: req.OperationID, RequestDigest: req.RequestDigest}
			if _, err := f.h.EditorDispatch(ctx, "editor.query", editorParams(t, query)); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("query entered live journal: %v", err)
			}
			var receipt editorReceipt
			if err := editorReadJSON(f.e.receiptPath(req.Namespace, req.OperationID), &receipt); err != nil {
				t.Fatal(err)
			}
			want := "applying"
			if boundary == "intent" {
				want = "intent"
			}
			if receipt.State != want || receipt.Result != nil {
				t.Fatalf("query rewrote running journal: %+v", receipt)
			}
			release()
			if err := fileMutationResult(t, finished); err != nil {
				t.Fatal(err)
			}
			if q := f.query(t, req); q.Outcome != "committed" {
				t.Fatal(q)
			}
		})
	}
}

func TestEditorUnremovableRetiredFenceDoesNotBlockApply(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	req := f.request(t, "retired-fence", []byte("before"), []byte("after"))
	retired := filepath.Join(f.state, editorKey(req.Namespace, req.Identity.WorkspaceIdentity)+".fence.json")
	// A nonempty directory makes cleanup fail on every OS without chmod/root
	// assumptions. Like a locked legacy file, it has no mutation authority.
	if err := os.Mkdir(retired, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(retired, "evidence"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := f.h.EditorDispatch(context.Background(), "editor.apply", editorParams(t, req))
	if err := editorApplyResultError(result, err); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(f.path); err != nil || string(data) != "after" {
		t.Fatal("retired metadata blocked target", string(data), err)
	}
	if _, err := os.Stat(retired); err != nil {
		t.Fatal("fixture did not leave unremovable legacy metadata", err)
	}
}

func TestEditorLegacyApplyRejectedWhileHistoricalReceiptRemainsQueryable(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	req := f.request(t, "legacy", []byte("before"), []byte("after"))
	req.Coordination = ""
	if _, err := f.h.EditorDispatch(context.Background(), "editor.apply", editorParams(t, req)); err == nil || !strings.Contains(err.Error(), "target-queue-v1") {
		t.Fatalf("legacy apply admitted: %v", err)
	}
	if _, err := os.Stat(f.e.receiptPath(req.Namespace, req.OperationID)); !os.IsNotExist(err) {
		t.Fatal("rejected legacy dispatch wrote intent", err)
	}
	raw, size := editorDigest([]byte("after")), int64(5)
	encoded, _ := json.Marshal(req)
	receipt := editorReceipt{Version: 1, Request: req, Fingerprint: editorDigest(encoded), State: "committed", Result: &EditorApplyResult{OperationID: req.OperationID, Outcome: "committed", RawDigest: &raw, TotalBytes: &size}}
	if err := f.e.save(f.e.receiptPath(req.Namespace, req.OperationID), receipt); err != nil {
		t.Fatal(err)
	}
	if q := f.query(t, req); q.Outcome != "committed" {
		t.Fatal("legacy query lost proof", q)
	}
	if _, err := f.h.EditorDispatch(context.Background(), "editor.apply", editorParams(t, req)); err == nil {
		t.Fatal("legacy receipt authorized a new apply")
	}
	if data, err := os.ReadFile(f.path); err != nil || string(data) != "before" {
		t.Fatal("legacy apply changed target", string(data), err)
	}
}
