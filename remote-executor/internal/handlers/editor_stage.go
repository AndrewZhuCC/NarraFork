package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func decodeEditorWrite(params map[string]any, out any) error {
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if len(b) > 128*1024 {
		return fmt.Errorf("editor request exceeds frame budget")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(out)
}
func editorPermissionBits(mode os.FileMode) uint32 {
	bits := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		bits |= 0o4000
	}
	if mode&os.ModeSetgid != 0 {
		bits |= 0o2000
	}
	if mode&os.ModeSticky != 0 {
		bits |= 0o1000
	}
	return bits
}
func editorBinding(namespace, resource string) error {
	if !editorValidKey(namespace) || !editorValidKey(resource) {
		return fmt.Errorf("editor namespace/resource binding required")
	}
	return nil
}
func (e *EditorEngine) Capabilities() any {
	return map[string]any{"protocolVersion": 1, "features": []string{FeatureEditorDurableWriteV1}, "maxChunkBytes": e.chunkBytes, "maxBytes": EditorMaxBytes, "runtimeEpoch": e.epoch}
}

// Reserve even failed initialization paths: old recovery evidence is never public.
func (h *Handlers) ReserveEditorPrivateRoot(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("editor private directory must be absolute")
	}
	h.guard.protectPrivateRoot(filepath.Clean(root))
	if canonical, _, err := canonicalGuardCreatePath(root); err == nil {
		h.guard.protectPrivateRoot(canonical)
	}
	return nil
}
func (h *Handlers) SetEditorEngine(e *EditorEngine) {
	if e != nil {
		h.guard.protectPrivateRoot(e.root)
		h.guard.protectPrivateRoot(e.lexicalRoot)
	}
	h.editor = e
}

// A fixture's work epoch is not the engine incarnation that owns RAM stages.
func (e *EditorEngine) SetLocalEpoch(epoch string) error {
	if !editorValidKey(epoch) {
		return fmt.Errorf("invalid local editor epoch")
	}
	e.epoch = epoch
	return nil
}
func (h *Handlers) EditorEpoch() string {
	if h.editor != nil {
		return h.editor.epoch
	}
	return EditorRuntimeEpoch()
}
func (h *Handlers) EditorEnabled() bool { return h.editor != nil }
func (h *Handlers) EditorDispatch(parent context.Context, method string, params map[string]any) (any, error) {
	if h.editor == nil {
		return nil, fmt.Errorf("EDITOR_PROVIDER_UNSUPPORTED: editor writer unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	e := h.editor
	if method == "editor.capabilities" {
		return e.Capabilities(), nil
	}
	release, err := e.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	switch method {
	case "editor.stage.open":
		var req EditorStageOpen
		if err := decodeEditorWrite(params, &req); err != nil {
			return nil, err
		}
		return e.stageOpen(ctx, req)
	case "editor.stage.append":
		var req EditorStageAppend
		if err := decodeEditorWrite(params, &req); err != nil {
			return nil, err
		}
		return e.stageAppend(ctx, req)
	case "editor.stage.seal":
		var req EditorStageSeal
		if err := decodeEditorWrite(params, &req); err != nil {
			return nil, err
		}
		return e.stageSeal(ctx, req)
	case "editor.stage.abort":
		var req EditorStageAbort
		if err := decodeEditorWrite(params, &req); err != nil {
			return nil, err
		}
		return e.stageAbort(req)
	case "editor.apply":
		if _, ok := params["expectedRawHash"]; !ok {
			return nil, fmt.Errorf("expectedRawHash is required (null means absent)")
		}
		var req EditorApplyRequest
		if err := decodeEditorWrite(params, &req); err != nil {
			return nil, err
		}
		return e.apply(ctx, req)
	case "editor.query":
		var req EditorQueryRequest
		if err := decodeEditorWrite(params, &req); err != nil {
			return nil, err
		}
		return e.query(req)
	default:
		return nil, fmt.Errorf("unknown editor write method")
	}
}

func (e *EditorEngine) stageOpen(ctx context.Context, req EditorStageOpen) (any, error) {
	if err := editorBinding(req.Namespace, req.ResourceKey); err != nil {
		return nil, err
	}
	if req.MaxBytes < 0 || req.MaxBytes > EditorMaxBytes || req.IdleTimeoutMs < 1 || req.IdleTimeoutMs > 30000 {
		return nil, fmt.Errorf("invalid editor stage budget")
	}
	count, total, err := e.stageBudget()
	if err != nil {
		return nil, err
	}
	if count >= editorMaxStages || total+req.MaxBytes > editorMaxStageBytes {
		return nil, fmt.Errorf("editor staging quota exceeded")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	nonce, err := editorRandomID()
	if err != nil {
		return nil, err
	}
	id := e.stageEpoch + "-" + nonce
	file, err := os.OpenFile(e.bodyPath(id), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if err = file.Close(); err != nil {
		_ = removeConditionalTemporary(file.Name(), info)
		return nil, err
	}
	s := &editorStage{EditorStageHandle: EditorStageHandle{StageID: id, ResourceKey: req.ResourceKey, ExpiresAt: time.Now().UnixMilli() + req.IdleTimeoutMs}, Namespace: req.Namespace, MaxBytes: req.MaxBytes, IdleTimeoutMs: req.IdleTimeoutMs, BodyInfo: info}
	e.stages[id] = s
	return s.EditorStageHandle, nil
}
func (e *EditorEngine) stageAppend(ctx context.Context, req EditorStageAppend) (any, error) {
	if err := editorBinding(req.Namespace, req.ResourceKey); err != nil {
		return nil, err
	}
	s, err := e.readStage(req.Namespace, req.ResourceKey, req.StageID)
	if err != nil {
		return nil, err
	}
	if req.Bytes < 1 || req.Bytes > e.chunkBytes || len(req.DataB64) > base64.StdEncoding.EncodedLen(int(e.chunkBytes)) || !editorValidDigest(req.Digest) {
		return nil, fmt.Errorf("invalid editor chunk budget/digest")
	}
	b, err := base64.StdEncoding.DecodeString(req.DataB64)
	if err != nil || int64(len(b)) != req.Bytes || editorDigest(b) != req.Digest {
		return nil, fmt.Errorf("EDITOR_STAGE_DIGEST_MISMATCH: invalid chunk")
	}
	chunk := editorChunk{Seq: req.Seq, Offset: req.Offset, Bytes: req.Bytes, Digest: req.Digest}
	if req.Seq >= 0 && req.Seq < int64(len(s.Chunks)) {
		if s.Chunks[req.Seq] != chunk {
			return nil, fmt.Errorf("EDITOR_STAGE_DIGEST_MISMATCH: duplicate chunk differs")
		}
		return map[string]any{"accepted": true, "nextOffset": s.Size}, nil
	}
	if s.Sealing || req.Seq != int64(len(s.Chunks)) || req.Offset != s.Size || s.Size+req.Bytes > s.MaxBytes || len(s.Chunks) >= int(EditorMaxBytes/EditorChunkBytes)+1 {
		return nil, fmt.Errorf("editor chunk sequence/offset/quota mismatch")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(e.bodyPath(s.StageID), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	// RAM records only acknowledged prefixes. A short/cancelled write may leave
	// a suffix; retry overwrites it and seal truncates to the accepted length.
	write := file.WriteAt
	if e.stageWrite != nil {
		write = func(b []byte, offset int64) (int, error) { return e.stageWrite(file, b, offset) }
	}
	n, err := write(b, s.Size)
	if err != nil {
		return nil, err
	}
	if n != len(b) {
		return nil, io.ErrShortWrite
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	s.Size += req.Bytes
	s.Chunks = append(s.Chunks, chunk)
	s.ExpiresAt = time.Now().UnixMilli() + s.IdleTimeoutMs
	return map[string]any{"accepted": true, "nextOffset": s.Size}, nil
}
func (e *EditorEngine) stageSeal(ctx context.Context, req EditorStageSeal) (any, error) {
	if err := editorBinding(req.Namespace, req.ResourceKey); err != nil {
		return nil, err
	}
	s, err := e.readStage(req.Namespace, req.ResourceKey, req.StageID)
	if err != nil {
		return nil, err
	}
	if !editorValidDigest(req.RawDigest) || req.TotalBytes != s.Size || s.RawDigest != "" && s.RawDigest != req.RawDigest {
		return nil, fmt.Errorf("EDITOR_STAGE_SEAL_FAILED: seal identity mismatch")
	}
	if s.Sealed {
		return s.EditorStageHandle, nil
	}
	s.Sealing = true
	file, err := os.OpenFile(e.bodyPath(s.StageID), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if !s.BodySynced {
		if err = file.Truncate(s.Size); err != nil {
			return nil, err
		}
		digest, size, err := hashEditorFile(ctx, file)
		if err != nil {
			return nil, err
		}
		if size != req.TotalBytes || digest != req.RawDigest {
			return nil, fmt.Errorf("EDITOR_STAGE_SEAL_FAILED: body digest mismatch")
		}
		s.RawDigest = digest
		syncBody := file.Sync
		if e.stageSync != nil {
			syncBody = func() error { return e.stageSync(file) }
		}
		if err = syncBody(); err != nil {
			return nil, err
		}
		s.BodySynced = true
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	// No directory or stage-metadata sync: the handle is engine-local. Apply's
	// durable intent syncs this same directory BEFORE any target mutation.
	s.Sealed = true
	s.ExpiresAt = time.Now().UnixMilli() + s.IdleTimeoutMs
	return s.EditorStageHandle, nil
}
func (e *EditorEngine) stageAbort(req EditorStageAbort) (any, error) {
	if err := editorBinding(req.Namespace, req.ResourceKey); err != nil {
		return nil, err
	}
	if req.Reason != "cancel" && req.Reason != "idle_timeout" && req.Reason != "superseded" {
		return nil, fmt.Errorf("invalid editor abort reason")
	}
	s, err := e.readStage(req.Namespace, req.ResourceKey, req.StageID)
	if err != nil {
		return nil, err
	}
	_, receipts, err := e.scanState()
	if err != nil {
		return nil, err
	}
	for _, r := range receipts {
		if r.Request.StageID == s.StageID {
			return nil, fmt.Errorf("cannot abort a dispatched editor stage")
		}
	}
	if err = e.dropStage(s); err != nil {
		return nil, err
	}
	return map[string]any{"aborted": true}, nil
}
