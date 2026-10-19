package handlers

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type editorCrashFixture struct {
	Root    string
	State   string
	Point   string
	Request EditorApplyRequest
	Reclaim bool
}

// Re-exec the actual Go test binary, not an in-memory restart: os.Exit bypasses
// cleanup defers and the kernel releases the helper's file claim on process exit.
func TestEditorCrashChild(t *testing.T) {
	path := os.Getenv("NF_EDITOR_CRASH_FIXTURE")
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture editorCrashFixture
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	h := New(NewPathGuard([]string{fixture.Root}), 128*1024)
	e, err := NewEditorEngine(h, fixture.State)
	if err != nil {
		t.Fatal(err)
	}
	h.SetEditorEngine(e)
	fixture.Request.Identity.RuntimeEpoch = EditorRuntimeEpoch()
	if !fixture.Reclaim {
		// Transfer fixture bytes, never a stage capability. Upload/seal and apply
		// must belong to this child engine; only the resulting receipt survives.
		body, err := os.ReadFile(e.bodyPath(fixture.Request.StageID))
		if err != nil {
			t.Fatal(err)
		}
		local := &editorFixture{h: h, e: e, root: fixture.Root, path: fixture.Request.Identity.CanonicalPath, state: fixture.State, identity: fixture.Request.Identity}
		fixture.Request.StageID = local.stage(t, body)
	}
	e.fault = func(point string) error {
		if point == fixture.Point {
			os.Exit(42)
		}
		return nil
	}
	if fixture.Reclaim {
		release, lockErr := e.lock(context.Background())
		if lockErr != nil {
			t.Fatal(lockErr)
		}
		defer release()
		_, _, _, err = e.inventory()
	} else {
		_, err = h.EditorDispatch(context.Background(), "editor.apply", editorParams(t, fixture.Request))
	}
	t.Fatalf("crash boundary not reached: %v", err)
}
func TestEditorReceiptRetirementProcessCrashOrder(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []string{"receipt-stage-retired", "receipt-retired"} {
		t.Run(point, func(t *testing.T) {
			f := newEditorFixture(t, []byte("same"))
			req := f.request(t, "retired-crash", []byte("same"), []byte("same"))
			if result := f.call(t, "editor.apply", req).(EditorApplyResult); result.Outcome != "committed" {
				t.Fatal(result)
			}
			acknowledgeEditorFixture(t, f, req)
			var receipt editorReceipt
			if err := editorReadJSON(f.e.receiptPath(req.Namespace, req.OperationID), &receipt); err != nil {
				t.Fatal(err)
			}
			receipt.AcknowledgedAt = time.Now().Add(-editorReceiptRetention - time.Hour).UnixMilli()
			writeEditorReceiptFixture(t, f, receipt)
			installed, err := os.Stat(f.path)
			if err != nil {
				t.Fatal(err)
			}
			fixture := editorCrashFixture{Root: f.root, State: f.state, Point: point, Request: req, Reclaim: true}
			b, _ := json.Marshal(fixture)
			path := filepath.Join(t.TempDir(), "input.json")
			if err = os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "-test.run=^TestEditorCrashChild$")
			child.Env = append(os.Environ(), "NF_EDITOR_CRASH_FIXTURE="+path)
			output, err := child.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 42 {
				t.Fatalf("retirement crash not exercised: %v %s", err, output)
			}
			f.restart(t)
			q := f.query(t, req)
			want := "committed"
			if point == "receipt-retired" {
				want = "not_found"
			}
			if q.Outcome != want {
				t.Fatalf("retirement crash proof state %+v", q)
			}
			result, err := f.h.EditorDispatch(context.Background(), "editor.apply", editorParams(t, req))
			if point == "receipt-retired" && err == nil {
				t.Fatal("retired request dispatched again")
			}
			if point == "receipt-stage-retired" && (err != nil || result.(EditorApplyResult).Outcome != "committed") {
				t.Fatal("retained proof lost", err)
			}
			after, err := os.Stat(f.path)
			if err != nil || !os.SameFile(installed, after) {
				t.Fatal("retirement crash replay caused a second installation")
			}
			if _, ok := f.e.stages[req.StageID]; ok {
				t.Fatal("retired stage was reconstructed from disk")
			}
			if _, err = os.Stat(f.e.bodyPath(req.StageID)); !os.IsNotExist(err) {
				t.Fatal("retired body survived receipt removal")
			}
		})
	}
}

func TestEditorRealProcessCrashRecoveryAndTemporaryQuota(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []string{"intent", "prepared", "applying", "renamed", "receipt"} {
		t.Run(point, func(t *testing.T) {
			f := newEditorFixture(t, []byte("before"))
			req := f.request(t, "process-crash", []byte("before"), []byte("after"))
			fixture := editorCrashFixture{Root: f.root, State: f.state, Point: point, Request: req}
			b, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "input.json")
			if err = os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "-test.run=^TestEditorCrashChild$")
			child.Env = append(os.Environ(), "NF_EDITOR_CRASH_FIXTURE="+path)
			output, err := child.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 42 {
				t.Fatalf("fixture did not crash at %s: %v %s", point, err, output)
			}
			f.restart(t)
			var durable editorReceipt
			if err := editorReadJSON(f.e.receiptPath(req.Namespace, req.OperationID), &durable); err != nil {
				t.Fatal(err)
			}
			if durable.StageBytes != 5 || durable.StageRawDigest != editorDigest([]byte("after")) {
				t.Fatal("intent omitted its sealed body metadata", durable)
			}
			req = durable.Request
			q := f.query(t, req)
			want := "uncertain"
			if point == "intent" || point == "prepared" {
				want = "rejected"
			}
			if q.Outcome != want {
				t.Fatalf("process recovery %s %+v", point, q)
			}
			// The first query itself publishes the outcome and releases the body:
			// rejected and uncertain proof live in the receipt, never in staging.
			if _, bodyErr := os.Stat(f.e.bodyPath(req.StageID)); !os.IsNotExist(bodyErr) {
				t.Fatal("first recovery query retained the stage body", bodyErr)
			}
			if again := f.query(t, req); again.Outcome != want {
				t.Fatal("unacknowledged recovery receipt was not queryable", again)
			}
			temporaries, err := filepath.Glob(filepath.Join(f.root, ".nf-editor-*"))
			if err != nil {
				t.Fatal(err)
			}
			if point == "prepared" && len(temporaries) != 0 {
				t.Fatal("known rejected preparation leaked", temporaries)
			}
			if point == "applying" && len(temporaries) != 1 {
				t.Fatal("uncertain preparation evidence removed", temporaries)
			}
			got, _ := os.ReadFile(f.path)
			written := point == "renamed" || point == "receipt"
			if (string(got) == "after") != written {
				t.Fatal("crash installed wrong body", string(got))
			}
		})
	}
}
