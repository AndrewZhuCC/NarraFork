package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func volatileOpen(t *testing.T, f *editorFixture, max int64) EditorStageHandle {
	t.Helper()
	return f.call(t, "editor.stage.open", EditorStageOpen{Namespace: "owner", ResourceKey: "resource", MaxBytes: max, IdleTimeoutMs: 30000}).(EditorStageHandle)
}
func volatileAppend(t *testing.T, f *editorFixture, id string, seq, offset int64, data []byte) any {
	t.Helper()
	return f.call(t, "editor.stage.append", EditorStageAppend{Namespace: "owner", ResourceKey: "resource", StageID: id, Seq: seq, Offset: offset, Bytes: int64(len(data)), Digest: editorDigest(data), DataB64: base64.StdEncoding.EncodeToString(data)})
}
func TestEditorVolatileStageSyncBudget(t *testing.T) {
	f := newEditorFixture(t, nil)
	syncs := 0
	f.e.stageSync = func(file *os.File) error { syncs++; return file.Sync() }
	f.e.fault = func(point string) error {
		if point == "metadata-sync" {
			return fmt.Errorf("upload attempted durable metadata publication")
		}
		return nil
	}
	stage := volatileOpen(t, f, 6)
	if syncs != 0 {
		t.Fatal("open synced body")
	}
	volatileAppend(t, f, stage.StageID, 0, 0, []byte("abc"))
	volatileAppend(t, f, stage.StageID, 0, 0, []byte("abc"))
	volatileAppend(t, f, stage.StageID, 1, 3, []byte("def"))
	if syncs != 0 {
		t.Fatal("append synced body")
	}
	seal := EditorStageSeal{Namespace: "owner", ResourceKey: "resource", StageID: stage.StageID, TotalBytes: 6, RawDigest: editorDigest([]byte("abcdef"))}
	f.call(t, "editor.stage.seal", seal)
	f.call(t, "editor.stage.seal", seal)
	if syncs != 1 {
		t.Fatal("seal did not perform exactly one body sync", syncs)
	}
	files, err := os.ReadDir(f.state)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name() != "engine.lock" && file.Name() != stage.StageID+".body" {
			t.Fatal("stage created persistent metadata", file.Name())
		}
	}
	if _, err := f.h.EditorDispatch(context.Background(), "editor.stage.append", editorParams(t, EditorStageAppend{Namespace: "owner", ResourceKey: "resource", StageID: stage.StageID, Seq: 2, Offset: 6, Bytes: 1, Digest: editorDigest([]byte("g")), DataB64: "Zw=="})); err == nil {
		t.Fatal("sealed body remained mutable")
	}
}
func TestEditorVolatileShortWriteAndCancelledSuffix(t *testing.T) {
	f := newEditorFixture(t, nil)
	stage := volatileOpen(t, f, 16)
	f.e.stageWrite = func(file *os.File, b []byte, offset int64) (int, error) {
		if _, err := file.WriteAt([]byte("abcunaccepted"), offset); err != nil {
			return 0, err
		}
		return 1, nil
	}
	request := EditorStageAppend{Namespace: "owner", ResourceKey: "resource", StageID: stage.StageID, Seq: 0, Offset: 0, Bytes: 3, Digest: editorDigest([]byte("abc")), DataB64: "YWJj"}
	if _, err := f.h.EditorDispatch(context.Background(), "editor.stage.append", editorParams(t, request)); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal("short write was accepted", err)
	}
	if row := f.e.stages[stage.StageID]; row.Size != 0 || len(row.Chunks) != 0 {
		t.Fatal("short write advanced the acknowledged prefix")
	}
	f.e.stageWrite = nil
	volatileAppend(t, f, stage.StageID, 0, 0, []byte("abc"))
	ctx, cancel := context.WithCancel(context.Background())
	f.e.stageWrite = func(file *os.File, b []byte, offset int64) (int, error) {
		n, err := file.WriteAt(b, offset)
		cancel()
		return n, err
	}
	request.Seq = 1
	request.Offset = 3
	request.Digest = editorDigest([]byte("def"))
	request.DataB64 = "ZGVm"
	if _, err := f.h.EditorDispatch(ctx, "editor.stage.append", editorParams(t, request)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled append was accepted", err)
	}
	if f.e.stages[stage.StageID].Size != 3 {
		t.Fatal("cancelled write advanced the prefix")
	}
	f.e.stageWrite = nil
	f.call(t, "editor.stage.seal", EditorStageSeal{Namespace: "owner", ResourceKey: "resource", StageID: stage.StageID, TotalBytes: 3, RawDigest: editorDigest([]byte("abc"))})
	body, err := os.ReadFile(f.e.bodyPath(stage.StageID))
	if err != nil || string(body) != "abc" {
		t.Fatal("seal retained an unacknowledged suffix", string(body), err)
	}
}
func TestEditorFailedSealCannotDispatch(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	stage := volatileOpen(t, f, 5)
	volatileAppend(t, f, stage.StageID, 0, 0, []byte("after"))
	seal := EditorStageSeal{Namespace: "owner", ResourceKey: "resource", StageID: stage.StageID, TotalBytes: 5, RawDigest: editorDigest([]byte("after"))}
	f.e.stageSync = func(*os.File) error { return errors.New("sync failed") }
	if _, err := f.h.EditorDispatch(context.Background(), "editor.stage.seal", editorParams(t, seal)); err == nil {
		t.Fatal("failed body sync was acknowledged")
	}
	before := editorDigest([]byte("before"))
	request := EditorApplyRequest{Namespace: "owner", ResourceKey: "resource", OperationID: "failed-seal", RequestDigest: editorDigest([]byte("failed-seal")), StageID: stage.StageID, ExpectedRawHash: &before, Identity: f.identity, MetadataPolicy: "preserve"}
	if _, err := f.h.EditorDispatch(context.Background(), "editor.apply", editorParams(t, request)); err == nil {
		t.Fatal("unflushed stage was dispatched")
	}
	if f.query(t, request).Outcome != "not_found" {
		t.Fatal("failed seal published an operation")
	}
	f.e.stageSync = nil
	f.call(t, "editor.stage.seal", seal)
	if result := f.call(t, "editor.apply", request).(EditorApplyResult); result.Outcome != "committed" {
		t.Fatal("same sealed bytes could not retry before dispatch", result)
	}
}

func TestEditorVolatileStagesCannotCrossEngineIncarnation(t *testing.T) {
	for _, sealed := range []bool{false, true} {
		t.Run(fmt.Sprint(sealed), func(t *testing.T) {
			f := newEditorFixture(t, []byte("before"))
			stage := volatileOpen(t, f, 5)
			volatileAppend(t, f, stage.StageID, 0, 0, []byte("after"))
			if sealed {
				f.call(t, "editor.stage.seal", EditorStageSeal{Namespace: "owner", ResourceKey: "resource", StageID: stage.StageID, TotalBytes: 5, RawDigest: editorDigest([]byte("after"))})
			}
			workEpoch := f.e.epoch
			f.restart(t)
			if f.e.epoch != workEpoch {
				t.Fatal("fixture changed the work epoch instead of engine incarnation")
			}
			request := EditorStageAppend{Namespace: "owner", ResourceKey: "resource", StageID: stage.StageID, Seq: 0, Offset: 0, Bytes: 5, Digest: editorDigest([]byte("after")), DataB64: "YWZ0ZXI="}
			if _, err := f.h.EditorDispatch(context.Background(), "editor.stage.append", editorParams(t, request)); err == nil || !strings.Contains(err.Error(), "EDITOR_RUNTIME_CHANGED") {
				t.Fatal("stale stage was restored", err)
			}
			before := editorDigest([]byte("before"))
			apply := EditorApplyRequest{Namespace: "owner", ResourceKey: "resource", OperationID: "not-dispatched", RequestDigest: editorDigest([]byte("not-dispatched")), StageID: stage.StageID, ExpectedRawHash: &before, Identity: f.identity, MetadataPolicy: "preserve"}
			if _, err := f.h.EditorDispatch(context.Background(), "editor.apply", editorParams(t, apply)); err == nil {
				t.Fatal("old engine stage was applied")
			}
			if q := f.query(t, apply); q.Outcome != "not_found" {
				t.Fatal("stale stage created execution proof", q)
			}
			body, _ := os.ReadFile(f.path)
			if string(body) != "before" {
				t.Fatal("stale stage changed target")
			}
		})
	}
}
func TestEditorIntentPinsBodyWithoutStageMetadata(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	request := f.request(t, "intent", []byte("before"), []byte("after"))
	f.e.fault = func(point string) error {
		if point == "intent" {
			return errors.New("interrupted")
		}
		return nil
	}
	if _, err := f.h.EditorDispatch(context.Background(), "editor.apply", editorParams(t, request)); err == nil {
		t.Fatal("intent boundary not exercised")
	}
	var r editorReceipt
	if err := editorReadJSON(f.e.receiptPath(request.Namespace, request.OperationID), &r); err != nil {
		t.Fatal(err)
	}
	if r.State != "intent" || r.StageRawDigest != editorDigest([]byte("after")) || r.StageBytes != 5 {
		t.Fatal("receipt did not own sealed body metadata", r)
	}
	f.restart(t)
	// Admission may drop every unrelated volatile body, but never a receipt ref.
	volatileOpen(t, f, 1)
	if _, err := os.Stat(f.e.bodyPath(request.StageID)); err != nil {
		t.Fatal("intent body was discarded", err)
	}
	if err := editorReadJSON(f.e.receiptPath(request.Namespace, request.OperationID), &r); err != nil || r.State != "intent" {
		t.Fatal("upload silently ran durable receipt recovery", err)
	}
}
func TestEditorRejectedBodyReleasedOnReturn(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	request := f.request(t, "rejected-body", []byte("before"), []byte("after"))
	wrong := editorDigest([]byte("wrong baseline"))
	request.ExpectedRawHash = &wrong
	if result := f.call(t, "editor.apply", request).(EditorApplyResult); result.Outcome != "rejected" {
		t.Fatal(result)
	}
	if _, err := os.Stat(f.e.bodyPath(request.StageID)); !os.IsNotExist(err) {
		t.Fatal("rejected body survived apply return", err)
	}
	if _, exists := f.e.stages[request.StageID]; exists {
		t.Fatal("rejected RAM stage was retained")
	}
	if query := f.query(t, request); query.Outcome != "rejected" {
		t.Fatal("body cleanup lost durable rejection", query)
	}
}
func TestEditorTerminalCleanupDoesNotDowngradeProof(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	request := f.request(t, "cleanup-failure", []byte("before"), []byte("after"))
	other := filepath.Join(t.TempDir(), "different-object")
	if err := os.WriteFile(other, nil, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	// Identity-bound cleanup must leave an unrecognized occupant alone. This
	// simulates cleanup not completing without changing the durable outcome.
	f.e.fault = func(point string) error {
		if point == "receipt" {
			f.e.stages[request.StageID].BodyInfo = info
		}
		return nil
	}
	if result := f.call(t, "editor.apply", request).(EditorApplyResult); result.Outcome != "committed" {
		t.Fatal("cleanup downgraded durable success", result)
	}
	if _, err = os.Stat(f.e.bodyPath(request.StageID)); err != nil {
		t.Fatal("cleanup failure fixture was not exercised", err)
	}
	if query := f.query(t, request); query.Outcome != "committed" {
		t.Fatal("cleanup failure changed receipt", query)
	}
	f.e.fault = nil
	volatileOpen(t, f, 0) // bounded inventory may retry cleanup later
	if _, err = os.Stat(f.e.bodyPath(request.StageID)); !os.IsNotExist(err) {
		t.Fatal("inventory could not retry terminal cleanup", err)
	}
}
func TestEditorUnpublishedTerminalKeepsRecoveryBody(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	request := f.request(t, "unpublished-terminal", []byte("before"), []byte("after"))
	terminal := false
	f.e.fault = func(point string) error {
		if point == "receipt" {
			terminal = true
		}
		if terminal && point == "metadata-sync" {
			return errors.New("terminal publication failed")
		}
		return nil
	}
	if _, err := f.h.EditorDispatch(context.Background(), "editor.apply", editorParams(t, request)); err == nil {
		t.Fatal("terminal persistence failure was not exercised")
	}
	if _, err := os.Stat(f.e.bodyPath(request.StageID)); err != nil {
		t.Fatal("in-memory terminal state deleted recovery evidence", err)
	}
	f.e.fault = nil
	if query := f.query(t, request); query.Outcome != "uncertain" {
		t.Fatal("unpublished success was guessed", query)
	}
	if _, err := os.Stat(f.e.bodyPath(request.StageID)); err != nil {
		t.Fatal("uncertain query deleted recovery evidence", err)
	}
}

func TestEditorTerminalBodiesDoNotFillStagingBudget(t *testing.T) {
	f := newEditorFixture(t, []byte("before"))
	before := []byte("before")
	body := bytes.Repeat([]byte("x"), int(EditorMaxBytes))
	for i := 0; i < 8; i++ {
		request := f.request(t, fmt.Sprintf("large-%d", i), before, body)
		if result := f.call(t, "editor.apply", request).(EditorApplyResult); result.Outcome != "committed" {
			t.Fatal("terminal body filled staging quota", i, result)
		}
		if _, err := os.Stat(f.e.bodyPath(request.StageID)); !os.IsNotExist(err) {
			t.Fatal("apply returned before releasing terminal body", i, err)
		}
		if _, exists := f.e.stages[request.StageID]; exists {
			t.Fatal("apply returned with consumed RAM stage", i)
		}
		if query := f.query(t, request); query.Outcome != "committed" {
			t.Fatal("unacknowledged proof was lost with body", query)
		}
		before = body
	}
	bodies, err := filepath.Glob(filepath.Join(f.state, "*.body"))
	if err != nil || len(bodies) != 0 {
		t.Fatal("unneeded terminal bodies were retained", len(bodies), err)
	}
	receipts, err := filepath.Glob(filepath.Join(f.state, "*.op.json"))
	if err != nil || len(receipts) != 8 {
		t.Fatal("body cleanup discarded unacknowledged terminal receipts", len(receipts), err)
	}
}
