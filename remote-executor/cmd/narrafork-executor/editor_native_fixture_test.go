package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Explicit test transport for the real executor engine, never a production Host
// fallback. Only receipt query crosses processes; stages stay in one session.
func testEditorNativeBinary(t *testing.T, binary string) {
	for name, encode := range map[string]func(string) []byte{
		"utf8-bom": func(text string) []byte { return append([]byte{0xef, 0xbb, 0xbf}, []byte(text)...) },
		"utf16le": func(text string) []byte {
			raw := []byte{0xff, 0xfe}
			for _, ch := range []byte(text) {
				raw = append(raw, ch, 0)
			}
			return raw
		},
	} {
		t.Run("native-encoded-"+name, func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(t.TempDir(), "state")
			path := filepath.Join(root, "document")
			before := encode("before\n")
			if err := os.WriteFile(path, before, 0640); err != nil {
				t.Fatal(err)
			}
			first := startEditorPeer(t, binary, root, state, true)
			save := func(peer *stdioPeer, id string, base []byte, text string) (map[string]any, []byte) {
				raw := encode(text)
				rawHash := stdioDigest(raw)
				if rawHash == stdioDigest([]byte(text)) {
					t.Fatal("fixture did not separate decoded text and raw domains")
				}
				req := prepareStdio(t, peer, root, path, id, base, raw)
				peer.must(t, "editor.apply", req) // discard success, recover only through durable query
				query := map[string]any{"namespace": req["namespace"], "resourceKey": req["resourceKey"], "operationId": id, "requestDigest": req["requestDigest"]}
				proof := peer.must(t, "editor.query", query)["result"].(map[string]any)
				if proof["rawDigest"] != rawHash {
					t.Fatal("executor proof used decoded text hash")
				}
				query["acknowledgeTerminal"] = true
				peer.must(t, "editor.query", query)
				delete(query, "acknowledgeTerminal")
				body, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(body, raw) {
					t.Fatal("encoded raw bytes changed", err)
				}
				return query, raw
			}
			query, saved := save(first, "encoded-first", before, "ok\n")
			installed, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = first.finish(); err != nil {
				t.Fatal(err)
			}
			cold := startEditorPeer(t, binary, root, state, true)
			proof := cold.must(t, "editor.query", query)
			if proof["outcome"] != "committed" || proof["result"].(map[string]any)["rawDigest"] != stdioDigest(saved) {
				t.Fatal("cold query lost raw receipt", proof)
			}
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(installed, after) {
				t.Fatal("cold query installed a second copy", err)
			}
			save(cold, "encoded-next", saved, "next\n")
		})
	}
	t.Run("native-private-fs-guard", func(t *testing.T) {
		root := t.TempDir()
		state := filepath.Join(root, "editor-native")
		peer := startEditorPeer(t, binary, root, state, true)
		paths := []string{filepath.Join(state, "engine.lock")}
		if runtime.GOOS != "windows" {
			alias := filepath.Join(root, "private-alias")
			if err := os.Symlink(state, alias); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, filepath.Join(alias, "engine.lock"))
		}
		for _, path := range paths {
			response := peer.call(t, "fs.stat", map[string]any{"path": path, "editorIdentity": true, "editorRoot": root})
			if response.OK || !strings.Contains(response.Error, "private state") {
				t.Fatal("stdio exposed its private engine state", response)
			}
		}
		for _, method := range []string{"editor.hostReceipt.put", "editor.hostReceipt.get", "editor.hostReceipt.settle"} {
			if response := peer.call(t, method, map[string]any{"operationId": "removed"}); response.OK {
				t.Fatal("removed Host store command is still available", method)
			}
		}
		path := filepath.Join(root, "document")
		if err := os.WriteFile(path, []byte("before"), 0640); err != nil {
			t.Fatal(err)
		}
		req := prepareStdio(t, peer, root, path, "normal", []byte("before"), []byte("after"))
		if result := peer.must(t, "editor.apply", req); result["outcome"] != "committed" {
			t.Fatal("ordinary engine writer was blocked", result)
		}
	})
}
