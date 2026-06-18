package agentbasic

import (
	"context"
	"strings"
	"testing"
)

// TestWebFetchDataURLErrorPaths covers the malformed data: URL error contracts
// that are not exercised by the success-path data URL tests: a data URL without
// the required comma separator, and a base64 data URL with an undecodable
// payload. Both raw and dom modes share the decode logic and must report the
// error rather than crash or emit garbage.
func TestWebFetchDataURLErrorPaths(t *testing.T) {
	cwd := t.TempDir()

	// Missing comma separator -> invalid data URL (raw mode).
	out := ExecuteTool(context.Background(), cwd, "WebFetch", `{"url":"data:text/plain"}`)
	if !strings.Contains(out, "invalid data URL") {
		t.Fatalf("missing-comma data URL should be rejected in raw mode, got: %s", out)
	}

	// Undecodable base64 payload -> invalid data URL (raw mode).
	out = ExecuteTool(context.Background(), cwd, "WebFetch", `{"url":"data:text/plain;base64,!!!not-base64!!!"}`)
	if !strings.Contains(out, "invalid data URL") {
		t.Fatalf("bad base64 data URL should be rejected in raw mode, got: %s", out)
	}

	// dom mode shares the same decode path -> same rejection.
	out = ExecuteTool(context.Background(), cwd, "WebFetch", `{"url":"data:text/html","mode":"dom"}`)
	if !strings.Contains(out, "invalid data URL") {
		t.Fatalf("missing-comma data URL should be rejected in dom mode, got: %s", out)
	}
}
