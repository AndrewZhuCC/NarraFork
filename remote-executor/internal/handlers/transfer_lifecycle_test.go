package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/narrafork/remote-executor/internal/wire"
)

type recordingTransferSender struct {
	frames    [][]byte
	afterSend func()
}

func (s *recordingTransferSender) SendBinary(frame []byte) error {
	s.frames = append(s.frames, append([]byte(nil), frame...))
	if s.afterSend != nil {
		s.afterSend()
	}
	return nil
}
func (s *recordingTransferSender) BufferedAmount() int { return 0 }

func TestTransferSenderRejectsTruncatedChunks(t *testing.T) {
	for _, tc := range []struct {
		name           string
		truncateBefore int64
		truncateAfter  int64
		removeBefore   bool
		want           []string
	}{
		{name: "source-disappeared", truncateBefore: -1, truncateAfter: -1, removeBefore: true},
		{name: "normal-final-chunk", truncateBefore: -1, truncateAfter: -1, want: []string{"abcd", "efgh", "ij"}},
		{name: "initial-zero-eof", truncateBefore: 0, truncateAfter: -1},
		{name: "initial-partial-eof", truncateBefore: 2, truncateAfter: -1},
		{name: "later-zero-eof", truncateBefore: -1, truncateAfter: 4, want: []string{"abcd"}},
		{name: "later-partial-eof", truncateBefore: -1, truncateAfter: 6, want: []string{"abcd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "source")
			if err := os.WriteFile(path, []byte("abcdefghij"), 0600); err != nil {
				t.Fatal(err)
			}
			sender := &recordingTransferSender{}
			transfers := NewTransfers(New(NewPathGuard([]string{root}), 4000000), sender)
			defer transfers.Close()
			ctx, cancel := context.WithCancel(transfers.ctx)
			defer cancel()
			sess := &transferSession{
				transferId: "truncated", direction: "download", path: path,
				chunkSize: 4, totalChunks: 3, fileSize: 10, ctx: ctx, cancel: cancel,
				received: map[int]bool{},
			}
			if err := transfers.register(ctx, sess); err != nil {
				t.Fatal(err)
			}
			if tc.truncateBefore >= 0 {
				if err := os.Truncate(path, tc.truncateBefore); err != nil {
					t.Fatal(err)
				}
			}
			if tc.removeBefore {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if tc.truncateAfter >= 0 {
				sender.afterSend = func() {
					if err := os.Truncate(path, tc.truncateAfter); err != nil {
						t.Fatal(err)
					}
				}
			}
			transfers.sendFile(ctx, sess)
			if len(sender.frames) != len(tc.want) {
				t.Fatalf("sent %d frames, want %d; unread tail was sent", len(sender.frames), len(tc.want))
			}
			for i, frame := range sender.frames {
				header, payload, ok := wire.DecodeChunkFrame(frame)
				if !ok {
					t.Fatal("invalid chunk frame")
				}
				if header.ChunkIndex != i || string(payload) != tc.want[i] {
					t.Fatalf("frame %d: index=%d payload=%q want=%q", i, header.ChunkIndex, payload, tc.want[i])
				}
			}
			if transfers.get(sess.transferId) != nil {
				t.Fatal("finished sender retained session")
			}
		})
	}
}

// Hold the first cancellation check after observing a live RPC. This makes the
// abort-before-registration ordering deterministic without replacing filesystem IO.
type heldBeginContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
	resume  chan struct{}
}

func (c *heldBeginContext) Err() error {
	first := false
	c.once.Do(func() { first = true })
	if first {
		err := c.Context.Err()
		close(c.entered)
		<-c.resume
		return err
	}
	return c.Context.Err()
}

func TestTransferBeginCancelledBeforeRegistrationReleasesCheckpoint(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "destination")
	connection, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	transfers := NewTransfersWithContext(connection, New(NewPathGuard([]string{root}), 4000000), nopSender{})
	defer transfers.Close()
	rpcCtx, cancelRPC := context.WithCancel(connection)
	defer cancelRPC()
	held := &heldBeginContext{Context: rpcCtx, entered: make(chan struct{}), resume: make(chan struct{})}
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(held.resume) }) }
	defer resume()
	finished := make(chan error, 1)
	go func() {
		_, err := transfers.Begin(held, checkpointUploadParams(path, "cancelled-begin"))
		finished <- err
	}()
	select {
	case <-held.entered:
	case <-time.After(time.Second):
		t.Fatal("begin did not enter setup")
	}
	cancelRPC()
	if _, err := transfers.Abort(map[string]any{"transferId": "cancelled-begin"}); err != nil {
		t.Fatal(err)
	}
	resume()
	if err := fileMutationResult(t, finished); !errors.Is(err, context.Canceled) {
		t.Fatalf("begin cancellation lost: %v", err)
	}
	if transfers.get("cancelled-begin") != nil {
		t.Fatal("cancelled startup retained a receiver")
	}
	// Rename proves the abandoned setup closed its handle on Windows too.
	if err := os.Rename(path+".nfpart", path+".abandoned"); err != nil {
		t.Fatal(err)
	}
	nextRPC, cancelNext := context.WithCancel(connection)
	defer cancelNext()
	if _, err := transfers.Begin(nextRPC, checkpointUploadParams(path, "retry")); err != nil {
		t.Fatalf("checkpoint blocked retry: %v", err)
	}
	cancelNext() // The begin RPC has handed off: this must not cancel the session.
	if transfers.get("retry").ctx.Err() != nil {
		t.Fatal("successful handoff still belongs to begin RPC")
	}
	completeRPC, cancelComplete := context.WithCancel(connection)
	defer cancelComplete()
	transfers.WriteChunk("retry", 0, []byte("next"))
	transfers.WriteChunk("retry", 1, []byte("data"))
	result, err := transfers.Complete(completeRPC, map[string]any{"transferId": "retry"})
	if err != nil || result.(map[string]any)["ok"] != true {
		t.Fatalf("retry completion: %v %v", result, err)
	}
}

func TestTransferBeginExpiredRPCNeverOwnsCheckpoint(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "destination")
	transfers := NewTransfers(New(NewPathGuard([]string{root}), 4000000), nopSender{})
	defer transfers.Close()
	ctx, cancel := context.WithDeadline(transfers.ctx, time.Now().Add(-time.Second))
	defer cancel()
	if _, err := transfers.Begin(ctx, checkpointUploadParams(path, "expired")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired begin accepted: %v", err)
	}
	if _, err := os.Stat(path + ".nfpart"); !os.IsNotExist(err) {
		t.Fatalf("expired begin created checkpoint: %v", err)
	}
	if _, err := transfers.Begin(transfers.ctx, checkpointUploadParams(path, "retry")); err != nil {
		t.Fatal(err)
	}
	completeCheckpointUpload(t, transfers, "retry")
}
