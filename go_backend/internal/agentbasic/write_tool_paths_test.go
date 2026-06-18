package agentbasic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteToolCreatesFileAndParentDirs covers the Write tool core success path
// that was only indirectly exercised: writing content creates missing parent
// directories and persists the exact bytes, and a path escaping the working
// directory is rejected.
func TestWriteToolCreatesFileAndParentDirs(t *testing.T) {
	cwd := t.TempDir()

	// Write into a nested path whose parent dirs do not exist yet.
	out := ExecuteTool(context.Background(), cwd, "Write",
		`{"file_path":"nested/dir/note.txt","content":"hello world\n"}`)
	if strings.Contains(out, "escapes") || strings.Contains(out, "too large") {
		t.Fatalf("write should succeed, got: %s", out)
	}
	data, err := os.ReadFile(filepath.Join(cwd, "nested", "dir", "note.txt"))
	if err != nil {
		t.Fatalf("written file should exist: %v", err)
	}
	if string(data) != "hello world\n" {
		t.Fatalf("unexpected written content: %q", string(data))
	}

	// Overwriting an existing file replaces its content.
	out = ExecuteTool(context.Background(), cwd, "Write",
		`{"file_path":"nested/dir/note.txt","content":"replaced"}`)
	if strings.Contains(out, "escapes") {
		t.Fatalf("overwrite should succeed, got: %s", out)
	}
	data, _ = os.ReadFile(filepath.Join(cwd, "nested", "dir", "note.txt"))
	if string(data) != "replaced" {
		t.Fatalf("overwrite did not replace content: %q", string(data))
	}

	// A path escaping the working directory is rejected and writes nothing.
	out = ExecuteTool(context.Background(), cwd, "Write",
		`{"file_path":"../escape.txt","content":"nope"}`)
	if !strings.Contains(out, "path escapes working directory") {
		t.Fatalf("expected path escape rejection, got: %s", out)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cwd), "escape.txt")); err == nil {
		t.Fatalf("escaping write must not create a file outside cwd")
	}
}

// TestWriteToolSuccessMessageMatchesTS pins the Write success message to the TS
// write tool: `Wrote ${content.length} bytes to ${file_path}` where the count is
// the JS string length (UTF-16 code units) and file_path is the ORIGINAL arg, not
// the resolved absolute path. Go previously returned `wrote <abspath>`.
func TestWriteToolSuccessMessageMatchesTS(t *testing.T) {
	cwd := t.TempDir()
	// "héllo" + CJK: 'h','é'(1 UTF-16),'l','l','o' = 5, plus 2 CJK (1 each) = 7.
	out := ExecuteTool(context.Background(), cwd, "Write",
		`{"file_path":"msg.txt","content":"héllo配置"}`)
	if out != "Wrote 7 bytes to msg.txt" {
		t.Fatalf("unexpected write message: %q (want UTF-16 length + original path)", out)
	}
	// Astral char counts as 2 UTF-16 units (surrogate pair).
	out = ExecuteTool(context.Background(), cwd, "Write",
		`{"file_path":"emoji.txt","content":"😀"}`)
	if out != "Wrote 2 bytes to emoji.txt" {
		t.Fatalf("astral char should count as 2 UTF-16 units, got: %q", out)
	}
}
