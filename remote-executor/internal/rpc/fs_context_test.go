package rpc

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/narrafork/remote-executor/internal/handlers"
)

func TestDispatchFileMutationTransferCompletePassesCancellation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	h := handlers.New(handlers.NewPathGuard([]string{root}), 4000000)
	d := NewDispatcher(h)
	transfers := handlers.NewTransfers(h, nil)
	defer transfers.Close()
	d.SetTransfers(transfers)
	if _, err := d.Dispatch(context.Background(), "transfer.begin", map[string]any{
		"transferId": "cancel-complete", "direction": "upload", "remotePath": path,
		"fileSize": 5, "totalChunks": 1, "chunkSize": 5,
	}, nil); err != nil {
		t.Fatal(err)
	}
	transfers.WriteChunk("cancel-complete", 0, []byte("after"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	params := map[string]any{"transferId": "cancel-complete"}
	if _, err := d.Dispatch(ctx, "transfer.complete", params, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("dispatch discarded transfer.complete cancellation: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "before" {
		t.Fatalf("cancelled transfer changed destination: %q, %v", data, err)
	}
	// This request was rejected before starting; explicit completion remains
	// available with the existing wire parameters and result shape.
	result, err := d.Dispatch(context.Background(), "transfer.complete", params, nil)
	if err != nil || result.(map[string]any)["ok"] != true || result.(map[string]any)["fileSize"] != int64(5) {
		t.Fatalf("explicit completion failed: %v, %v", result, err)
	}
}

func TestDispatchFileMutationPassesCancellation(t *testing.T) {
	for _, method := range []string{"fs.write", "fs.remove"} {
		t.Run(method, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "file")
			if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			d := NewDispatcher(handlers.New(handlers.NewPathGuard([]string{root}), 4000000))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			params := map[string]any{"path": path, "dataB64": base64.StdEncoding.EncodeToString([]byte("after"))}
			if _, err := d.Dispatch(ctx, method, params, nil); !errors.Is(err, context.Canceled) {
				t.Fatalf("dispatch discarded cancellation: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "before" {
				t.Fatal("cancelled dispatch altered original")
			}
		})
	}
}
