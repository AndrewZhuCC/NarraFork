package agentbasic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadToolImageReturnsHonestPlaceholder verifies the Read tool detects an
// image extension and returns an image-aware placeholder (with size + format)
// rather than the misleading "binary file cannot be read as text" error. The
// placeholder honestly states the image bytes are not attached to the model,
// since this backend only threads text tool output (full multimodal tool-result
// image blocks are a tracked capability boundary).
func TestReadToolImageReturnsHonestPlaceholder(t *testing.T) {
	cwd := t.TempDir()
	// A small "PNG" file (content bytes are irrelevant; detection is by extension).
	if err := os.WriteFile(filepath.Join(cwd, "diagram.png"), []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a}, 0o644); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}
	out := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"diagram.png"}`)
	if strings.Contains(out, "binary file cannot be read as text") {
		t.Fatalf("image must not be rejected as unreadable binary, got: %s", out)
	}
	if !strings.HasPrefix(out, "[Image: diagram.png (") {
		t.Fatalf("image output should start with descriptive prefix, got: %s", out)
	}
	if !strings.Contains(out, " KB, png)") {
		t.Fatalf("image output should include size in KB and png format, got: %s", out)
	}
	if !strings.Contains(out, "not attached to the model") {
		t.Fatalf("image output must honestly state bytes are not attached, got: %s", out)
	}
}

// TestReadToolImageTooLarge verifies an image over the 5 MB limit is rejected
// with the TS-matching "too large" message and does NOT fall through to the
// text-streaming branch (which would emit numbered garbled lines).
func TestReadToolImageTooLarge(t *testing.T) {
	cwd := t.TempDir()
	bigPath := filepath.Join(cwd, "huge.jpg")
	f, err := os.Create(bigPath)
	if err != nil {
		t.Fatalf("create big image: %v", err)
	}
	// Sparse 6 MB file (> readMaxImageBytes) without writing 6 MB of bytes.
	if err := f.Truncate(6 * 1024 * 1024); err != nil {
		f.Close()
		t.Fatalf("truncate big image: %v", err)
	}
	f.Close()
	out := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"huge.jpg"}`)
	if !strings.Contains(out, "Image file too large") || !strings.Contains(out, "Maximum supported size is 5 MB") {
		t.Fatalf("oversized image should be rejected with too-large message, got: %s", out)
	}
	// Must not stream as text (no cat -n line numbering / tab-prefixed lines).
	if strings.Contains(out, "\t") {
		t.Fatalf("oversized image must not fall through to text streaming, got: %s", out)
	}
}

// TestReadToolImageFormatMapping verifies the extension→format mapping matches
// the TS IMAGE_EXTENSIONS table (.jpg/.jpeg → jpeg, .gif → gif, .webp → webp).
func TestReadToolImageFormatMapping(t *testing.T) {
	cwd := t.TempDir()
	cases := []struct {
		file   string
		format string
	}{
		{"a.jpg", "jpeg"},
		{"b.jpeg", "jpeg"},
		{"c.gif", "gif"},
		{"d.webp", "webp"},
	}
	for _, tc := range cases {
		if err := os.WriteFile(filepath.Join(cwd, tc.file), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", tc.file, err)
		}
		out := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"`+tc.file+`"}`)
		if !strings.Contains(out, ", "+tc.format+")") {
			t.Fatalf("Read %s should map to format %q, got: %s", tc.file, tc.format, out)
		}
	}
}
