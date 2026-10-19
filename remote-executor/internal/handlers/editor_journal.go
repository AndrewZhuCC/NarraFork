package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const editorMaxStages = 32
const editorMaxStageBytes int64 = 128 * 1024 * 1024
const editorMaxReceipts = 4096
const editorMaxFences = 4096
const editorReceiptCleanupBatch = 16
const editorReceiptRetention = 7 * 24 * time.Hour
const editorMaxStateFiles = editorMaxReceipts + editorMaxFences + editorMaxStages*2 + 16
const editorMaxMetadata = 128 * 1024
const editorMaxStageIdle = 30 * time.Second

// A stage's ExpiresAt is at most one idle window past its last append (open
// creates, append writes) plus one more for seal. Bodies of another engine
// incarnation sharing this root are only reclaimed once that bound has passed.
const editorForeignStageGrace = 2*editorMaxStageIdle + 5*time.Second

// Stages are volatile, including sealed stages. Only operation receipts are
// durable. Root locking serializes intent/replace/query and protects referenced
// recovery bodies; it does not promise stage transfer between engine instances.
type EditorEngine struct {
	h           *Handlers
	root        string
	lexicalRoot string
	rootInfo    os.FileInfo
	chunkBytes  int64
	epoch       string
	stageEpoch  string
	stages      map[string]*editorStage
	fault       func(string) error
}
type editorChunk struct {
	Seq    int64
	Offset int64
	Bytes  int64
	Digest string
}
type editorStage struct {
	EditorStageHandle
	Namespace     string
	MaxBytes      int64
	IdleTimeoutMs int64
	Size          int64
	Chunks        []editorChunk
	RawDigest     string
	OperationID   string
	Sealing       bool
	Sealed        bool
	BodyInfo      os.FileInfo
}
type editorReceipt struct {
	Version           int                `json:"version"`
	Request           EditorApplyRequest `json:"request"`
	Fingerprint       string             `json:"fingerprint"`
	State             string             `json:"state"`
	Result            *EditorApplyResult `json:"result"`
	TemporaryPath     string             `json:"temporaryPath"`
	TemporaryIdentity string             `json:"temporaryIdentity"`
	AcknowledgedAt    int64              `json:"acknowledgedAt,omitempty"`
	StageRawDigest    string             `json:"stageRawDigest"`
	StageBytes        int64              `json:"stageBytes"`
}
type editorFence struct {
	Version   int    `json:"version"`
	Token     int64  `json:"token"`
	Namespace string `json:"namespace"`
}

func editorDigest(b []byte) string     { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func editorKey(parts ...string) string { b, _ := json.Marshal(parts); return editorDigest(b) }
func editorValidDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}
func editorValidKey(s string) bool {
	return len(s) > 0 && len(s) <= 1024 && !strings.ContainsRune(s, 0)
}
func editorRandomID() (string, error) {
	var b [24]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func (e *EditorEngine) inject(point string) error {
	if e.fault != nil {
		return e.fault(point)
	}
	return nil
}

func NewEditorEngine(h *Handlers, root string) (*EditorEngine, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("editor state directory must be absolute")
	}
	if err := h.ReserveEditorPrivateRoot(root); err != nil {
		return nil, err
	}
	if err := ensureEditorPrivateDirectory(root); err != nil {
		return nil, err
	}
	canonical, err := canonicalGuardPath(root)
	if err != nil {
		return nil, err
	}
	h.guard.protectPrivateRoot(canonical)
	info, err := os.Lstat(canonical)
	if err != nil {
		return nil, err
	}
	chunk := EditorChunkBytes
	if budget := (h.maxRpcBytes - 8192) * 3 / 4; budget < chunk {
		chunk = budget
	}
	if chunk < 1 {
		return nil, fmt.Errorf("editor RPC budget too small")
	}
	stageEpoch, err := editorRandomID()
	if err != nil {
		return nil, err
	}
	return &EditorEngine{h: h, root: canonical, lexicalRoot: filepath.Clean(root), rootInfo: info, chunkBytes: chunk, epoch: EditorRuntimeEpoch(), stageEpoch: stageEpoch, stages: make(map[string]*editorStage)}, nil
}
func (e *EditorEngine) lock(ctx context.Context) (func(), error) {
	release, err := acquireFileMutation(ctx)
	if err != nil {
		return nil, err
	}
	if err = e.verifyPrivateRoot(); err != nil {
		release()
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(e.root, "engine.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		release()
		return nil, err
	}
	if err = lockEditorFile(ctx, file); err != nil {
		file.Close()
		release()
		return nil, err
	}
	return func() { unlockEditorFile(file); file.Close(); release() }, nil
}
func editorReadJSON(path string, out any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > editorMaxMetadata {
		return fmt.Errorf("invalid editor journal size/type")
	}
	b, err := io.ReadAll(io.LimitReader(file, editorMaxMetadata+1))
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("invalid trailing editor journal data")
	}
	return nil
}
func (e *EditorEngine) verifyPrivateRoot() error {
	info, err := os.Lstat(e.root)
	if err != nil || !os.SameFile(e.rootInfo, info) {
		return fmt.Errorf("editor private directory identity changed")
	}
	return nil
}

// This always publishes durable metadata. Volatile stage RPCs never call it.
func (e *EditorEngine) save(path string, value any) error {
	if err := e.verifyPrivateRoot(); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) > editorMaxMetadata {
		return fmt.Errorf("editor metadata quota exceeded")
	}
	file, err := os.CreateTemp(e.root, ".metadata-*")
	if err != nil {
		return err
	}
	name := file.Name()
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	defer func() { file.Close(); _ = removeConditionalTemporary(name, info) }()
	if _, err = file.Write(b); err != nil {
		return err
	}
	if err = e.inject("metadata-sync"); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = e.verifyPrivateRoot(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	if err = e.verifyPrivateRoot(); err != nil {
		return err
	}
	return syncEditorDirectory(e.root)
}
func (e *EditorEngine) bodyPath(id string) string { return filepath.Join(e.root, id+".body") }
func (e *EditorEngine) receiptPath(namespace, op string) string {
	return filepath.Join(e.root, editorKey(namespace, op)+".op.json")
}
func validEditorStageID(id string) bool {
	return len(id) == 97 && id[48] == '-' && editorValidStageToken(id[:48]) && editorValidStageToken(id[49:])
}
func editorReceiptBodyValid(r *editorReceipt) bool {
	return validEditorStageID(r.Request.StageID) && editorValidDigest(r.StageRawDigest) && r.StageBytes >= 0 && r.StageBytes <= EditorMaxBytes
}
func editorValidStageToken(id string) bool {
	if len(id) != 48 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
func (e *EditorEngine) readStage(namespace, resource, id string) (*editorStage, error) {
	if !validEditorStageID(id) {
		return nil, fmt.Errorf("EDITOR_NOT_FOUND: invalid stage handle")
	}
	if !strings.HasPrefix(id, e.stageEpoch+"-") {
		return nil, fmt.Errorf("EDITOR_RUNTIME_CHANGED: stage belongs to another engine incarnation")
	}
	s := e.stages[id]
	if s == nil || s.Namespace != namespace || s.ResourceKey != resource {
		return nil, fmt.Errorf("EDITOR_NOT_FOUND: unknown stage binding")
	}
	if s.OperationID != "" {
		return nil, fmt.Errorf("editor stage already dispatched")
	}
	if s.ExpiresAt < time.Now().UnixMilli() {
		return nil, fmt.Errorf("EDITOR_NOT_FOUND: stage expired")
	}
	return s, nil
}
func (e *EditorEngine) dropStage(s *editorStage) error {
	if err := e.verifyPrivateRoot(); err != nil {
		return err
	}
	if err := removeConditionalTemporary(e.bodyPath(s.StageID), s.BodyInfo); err != nil {
		return err
	}
	delete(e.stages, s.StageID)
	return nil
}
func (e *EditorEngine) removeBody(id string) error {
	if !validEditorStageID(id) {
		return fmt.Errorf("invalid receipt stage binding")
	}
	if s := e.stages[id]; s != nil {
		return e.dropStage(s)
	}
	if err := e.verifyPrivateRoot(); err != nil {
		return err
	}
	info, err := os.Lstat(e.bodyPath(id))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return removeConditionalTemporary(e.bodyPath(id), info)
}

// The caller holds the existing root lock. This scan is bounded and read-only:
// stage admission cannot hide receipt/maintenance syncs in the upload path.
func (e *EditorEngine) scanState() ([]os.DirEntry, map[string]*editorReceipt, error) {
	dir, err := os.Open(e.root)
	if err != nil {
		return nil, nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(editorMaxStateFiles + 1)
	if err != nil && err != io.EOF {
		return nil, nil, err
	}
	if len(entries) > editorMaxStateFiles {
		return nil, nil, fmt.Errorf("editor state inventory quota exceeded")
	}
	receipts := make(map[string]*editorReceipt)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".op.json") {
			continue
		}
		var r editorReceipt
		if err := editorReadJSON(filepath.Join(e.root, entry.Name()), &r); err != nil {
			return nil, nil, err
		}
		if r.Version != 1 || !editorValidKey(r.Request.Namespace) || !editorValidKey(r.Request.ResourceKey) || !editorValidKey(r.Request.OperationID) || !editorValidDigest(r.Request.RequestDigest) || entry.Name() != filepath.Base(e.receiptPath(r.Request.Namespace, r.Request.OperationID)) {
			return nil, nil, fmt.Errorf("invalid editor receipt binding")
		}
		if !editorReceiptBodyValid(&r) {
			return nil, nil, fmt.Errorf("invalid editor receipt body metadata")
		}
		switch r.State {
		case "intent", "applying":
		case "committed", "rejected", "uncertain":
			if r.Result == nil || r.Result.OperationID != r.Request.OperationID || r.Result.Outcome != r.State {
				return nil, nil, fmt.Errorf("invalid editor receipt result")
			}
		default:
			return nil, nil, fmt.Errorf("invalid editor receipt state")
		}
		receipts[entry.Name()] = &r
	}
	return entries, receipts, nil
}

// Only apply reads a body, so only a journal interrupted mid-apply (and not yet
// recovered by loadReceipt) owns one. Terminal and uncertain proof live in the
// receipt; the Host keeps the intended bytes in its own blob store.
func editorBodyRequired(r *editorReceipt) bool {
	return r.State == "intent" || r.State == "applying"
}

// A rejected preparation temporary may survive a crash. Its receipt stays
// unprunable until the identity-bound cleanup succeeds.
func (e *EditorEngine) temporaryPending(r *editorReceipt) bool {
	return r.State == "rejected" && r.TemporaryPath != "" && !e.cleanupRejectedTemporary(r)
}
func (e *EditorEngine) stageBudget() (int, int64, error) {
	entries, receipts, err := e.scanState()
	if err != nil {
		return 0, 0, err
	}
	count, total, foreign, err := e.collectStages(entries, receipts)
	if err != nil {
		return 0, 0, err
	}
	// Orphan ceilings have no operation or live-stage owner. Their removal is
	// cache cleanup; no receipt transition or durability barrier is published.
	return count, total, e.cleanupOrphanFences(entries, receipts, foreign)
}

// The third result reports a live body owned by another engine incarnation.
// Its namespace is unknown here, so orphan fence cleanup must stand down.
func (e *EditorEngine) collectStages(entries []os.DirEntry, receipts map[string]*editorReceipt) (int, int64, bool, error) {
	refs := make(map[string]bool)
	required := make(map[string]int64)
	for _, r := range receipts {
		id := r.Request.StageID
		if !validEditorStageID(id) {
			return 0, 0, false, fmt.Errorf("invalid receipt stage binding")
		}
		refs[id] = true
		if editorBodyRequired(r) {
			required[id] = r.StageBytes
		}
	}
	count := 0
	var total int64
	foreign := false
	counted := make(map[string]bool)
	for id, s := range e.stages {
		if refs[id] {
			if _, keep := required[id]; !keep {
				if err := e.dropStage(s); err != nil {
					return 0, 0, false, err
				}
			}
			continue
		}
		if s.OperationID != "" || s.ExpiresAt < time.Now().UnixMilli() {
			if err := e.dropStage(s); err != nil {
				return 0, 0, false, err
			}
			continue
		}
		count++
		total += s.MaxBytes
		counted[id] = true
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".body") {
			continue
		}
		id := strings.TrimSuffix(name, ".body")
		if !validEditorStageID(id) {
			return 0, 0, false, fmt.Errorf("invalid staging body inventory")
		}
		if counted[id] {
			continue
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, 0, false, err
		}
		if _, keep := required[id]; !keep {
			// Another incarnation may still be appending to or sealing an
			// undispatched body; leave it alone until its idle bound has passed.
			if !refs[id] && !strings.HasPrefix(id, e.stageEpoch+"-") && time.Since(info.ModTime()) < editorForeignStageGrace {
				foreign = true
				count++
				total += info.Size()
				counted[id] = true
				continue
			}
			// No receipt ever dispatched this body, or durable terminal proof no
			// longer needs it.
			if err := e.removeBody(id); err != nil {
				return 0, 0, false, err
			}
			continue
		}
		if !info.Mode().IsRegular() || info.Size() > EditorMaxBytes {
			return 0, 0, false, fmt.Errorf("invalid staging body size/type")
		}
		count++
		total += info.Size()
		counted[id] = true
	}
	for id, size := range required {
		if !counted[id] {
			count++
			total += size
		}
	}
	return count, total, foreign, nil
}

// Recovery and retention run only on the durable-operation path, never open /
// append / seal. Receipt references are examined before any stage body cleanup.
func (e *EditorEngine) inventory() (int, int64, int, error) {
	entries, receipts, err := e.scanState()
	if err != nil {
		return 0, 0, 0, err
	}
	protected := make(map[string]bool)
	for name, r := range receipts {
		if r.State == "intent" || r.State == "applying" {
			r, err = e.loadReceipt(r.Request.Namespace, r.Request.ResourceKey, r.Request.OperationID, r.Request.RequestDigest)
			if err != nil {
				return 0, 0, 0, err
			}
			receipts[name] = r
		}
		if e.temporaryPending(r) {
			protected[r.Request.StageID] = true
		}
	}
	if err = e.pruneReceipts(receipts, protected); err != nil {
		return 0, 0, 0, err
	}
	count, total, foreign, err := e.collectStages(entries, receipts)
	if err != nil {
		return 0, 0, 0, err
	}
	if err = e.cleanupOrphanFences(entries, receipts, foreign); err != nil {
		return 0, 0, 0, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".metadata-") {
			if err := os.Remove(filepath.Join(e.root, entry.Name())); err != nil && !os.IsNotExist(err) {
				return 0, 0, 0, err
			}
		}
	}
	return count, total, len(receipts), syncEditorDirectory(e.root)
}
func (e *EditorEngine) cleanupOrphanFences(entries []os.DirEntry, receipts map[string]*editorReceipt, foreignStages bool) error {
	if foreignStages {
		return nil
	}
	live := make(map[string]bool)
	for _, r := range receipts {
		live[editorKey(r.Request.Namespace, r.Request.Identity.WorkspaceIdentity)+".fence.json"] = true
	}
	unapplied := make(map[string]bool)
	for _, s := range e.stages {
		if s.OperationID == "" {
			unapplied[s.Namespace] = true
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".fence.json") || live[name] {
			continue
		}
		var fence editorFence
		if err := editorReadJSON(filepath.Join(e.root, name), &fence); err != nil {
			return err
		}
		if !unapplied[fence.Namespace] {
			if err := os.Remove(filepath.Join(e.root, name)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (e *EditorEngine) pruneReceipts(receipts map[string]*editorReceipt, protected map[string]bool) error {
	names := make([]string, 0, len(receipts))
	for name, r := range receipts {
		if r.AcknowledgedAt > 0 && (r.State == "committed" || r.State == "rejected") && !protected[r.Request.StageID] {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return receipts[names[i]].AcknowledgedAt < receipts[names[j]].AcknowledgedAt })
	pressure := 0
	if len(receipts) >= editorMaxReceipts {
		pressure = len(receipts) - editorMaxReceipts + editorReceiptCleanupBatch
	}
	cutoff := time.Now().Add(-editorReceiptRetention).UnixMilli()
	for _, name := range names {
		r := receipts[name]
		if r.AcknowledgedAt > cutoff && pressure <= 0 {
			break
		}
		if err := e.removeBody(r.Request.StageID); err != nil {
			return err
		}
		if err := syncEditorDirectory(e.root); err != nil {
			return err
		}
		if err := e.inject("receipt-stage-retired"); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(e.root, name)); err != nil {
			return err
		}
		if err := e.inject("receipt-retired"); err != nil {
			return err
		}
		delete(receipts, name)
		pressure--
	}
	return syncEditorDirectory(e.root)
}
func (e *EditorEngine) cleanupRejectedTemporary(r *editorReceipt) bool {
	if r.State != "rejected" && r.State != "committed" {
		return false
	}
	path := r.TemporaryPath
	if filepath.Dir(path) != filepath.Dir(r.Request.Identity.CanonicalPath) || !strings.HasPrefix(filepath.Base(path), ".nf-editor-") {
		return false
	}
	if err := e.h.VerifyEditorRoot(r.Request.Identity.WorkspaceIdentity); err != nil {
		return false
	}
	canonical, err := e.h.guard.CheckCreate(path)
	if err != nil || !samePath(canonical, path) {
		return false
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	identity, err := editorObjectIdentity(path, info)
	if err != nil || identity != r.TemporaryIdentity {
		return false
	}
	return removeConditionalTemporary(path, info) == nil
}
func (e *EditorEngine) loadReceipt(namespace, resource, op, digest string) (*editorReceipt, error) {
	var r editorReceipt
	if err := editorReadJSON(e.receiptPath(namespace, op), &r); err != nil {
		return nil, err
	}
	if r.Version != 1 || r.Request.Namespace != namespace || r.Request.OperationID != op || r.Request.ResourceKey != resource {
		return nil, fmt.Errorf("editor receipt binding mismatch")
	}
	if r.Request.RequestDigest != digest {
		return nil, fmt.Errorf("EDITOR_APPLY_REJECTED: operation digest mismatch")
	}
	if !editorReceiptBodyValid(&r) {
		return nil, fmt.Errorf("invalid editor receipt body metadata")
	}
	// Holding the root lock proves no writer is live in this incomplete journal.
	if r.State == "intent" || r.State == "applying" {
		outcome, code, message := "rejected", "EDITOR_APPLY_REJECTED", "interrupted before replacement"
		if r.State == "applying" {
			outcome, code, message = "uncertain", "EDITOR_UNCERTAIN", "replacement may have executed"
		}
		r.State = outcome
		r.Result = editorFailure(op, outcome, code, message)
		if err := e.save(e.receiptPath(namespace, op), &r); err != nil {
			return nil, err
		}
		_ = e.removeBody(r.Request.StageID)
		_ = e.temporaryPending(&r)
	}
	if r.Result == nil || r.Result.OperationID != op || r.Result.Outcome != r.State {
		return nil, fmt.Errorf("invalid editor receipt")
	}
	return &r, nil
}
func editorFailure(op, outcome, code, message string) *EditorApplyResult {
	return &EditorApplyResult{OperationID: op, Outcome: outcome, Error: &EditorProviderError{Code: code, Message: message}}
}
