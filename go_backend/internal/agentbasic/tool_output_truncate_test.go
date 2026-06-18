package agentbasic

import (
	"strings"
	"testing"
)

func TestTailCaptureBufferRetainsBoundedTail(t *testing.T) {
	var maxOnWriteLen int
	buf := newTailCaptureBuffer(100, func(s string) {
		if len(s) > maxOnWriteLen {
			maxOnWriteLen = len(s)
		}
	})
	// Write 1000 bytes in 10-byte chunks; only the last 100 bytes must be retained.
	for i := 0; i < 100; i++ {
		if _, err := buf.Write([]byte("0123456789")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got := len(buf.Tail()); got != 100 {
		t.Fatalf("Tail length = %d, want 100 (bounded by cap)", got)
	}
	if buf.TotalBytes() != 1000 {
		t.Fatalf("TotalBytes = %d, want 1000", buf.TotalBytes())
	}
	if !buf.Truncated() {
		t.Fatal("Truncated() should be true after exceeding the byte cap")
	}
	// The onWrite payload must never exceed the cap (no O(n^2) cumulative growth).
	if maxOnWriteLen > 100 {
		t.Fatalf("onWrite payload grew to %d bytes, want <= cap 100", maxOnWriteLen)
	}
}

func TestTailCaptureBufferCountsLines(t *testing.T) {
	buf := newTailCaptureBuffer(toolOutputMaxBytes, nil)
	for i := 0; i < 5; i++ {
		_, _ = buf.Write([]byte("line\n"))
	}
	// 5 newlines + trailing-content rule: each write ends with \n, so total lines
	// counts the 5 newline-terminated lines plus the empty trailing segment = 6,
	// matching JS "a\n".split("\n").length semantics.
	if buf.TotalLines() != 6 {
		t.Fatalf("TotalLines = %d, want 6", buf.TotalLines())
	}
	if buf.Truncated() {
		t.Fatal("small output should not be truncated")
	}
}

func TestTailCaptureBufferSingleLargeChunkKeepsTail(t *testing.T) {
	buf := newTailCaptureBuffer(10, nil)
	_, _ = buf.Write([]byte("ABCDEFGHIJKLMNOP")) // 16 bytes, cap 10
	if got := buf.Tail(); got != "GHIJKLMNOP" {
		t.Fatalf("Tail = %q, want last 10 bytes GHIJKLMNOP", got)
	}
}

func TestTruncateToolOutputNoTruncationWhenSmall(t *testing.T) {
	content := "hello\nworld"
	out, truncated := truncateToolOutput(content, len(content), 2)
	if truncated {
		t.Fatal("small content should not be truncated")
	}
	if out != content {
		t.Fatalf("content should be returned unchanged, got %q", out)
	}
}

func TestTruncateToolOutputByteTruncationKeepsTail(t *testing.T) {
	tail := strings.Repeat("x", toolOutputMaxBytes) + "TAILMARK"
	tail = tail[len(tail)-toolOutputMaxBytes:] // simulate buffer tail of cap size
	out, truncated := truncateToolOutput(tail, toolOutputMaxBytes*2, 1)
	if !truncated {
		t.Fatal("expected truncation when totalBytes exceeds cap")
	}
	if !strings.Contains(out, "OUTPUT TRUNCATED") {
		t.Fatalf("expected truncation hint, got %.120q", out)
	}
	if !strings.Contains(out, "bytes truncated") {
		t.Fatalf("byte truncation should report bytes, got %.120q", out)
	}
}

func TestTruncateToolOutputConvenienceWrapper(t *testing.T) {
	// Small input: returned unchanged, not truncated.
	if out, truncated := TruncateToolOutput("hello\nworld"); truncated || out != "hello\nworld" {
		t.Fatalf("small input should be unchanged/untruncated, got %q truncated=%v", out, truncated)
	}
	// Over the byte cap: tail-keep + hint.
	big := strings.Repeat("x", toolOutputMaxBytes+1000) + "TAILMARK"
	out, truncated := TruncateToolOutput(big)
	if !truncated {
		t.Fatal("over-cap input should be truncated")
	}
	if !strings.Contains(out, "OUTPUT TRUNCATED") {
		t.Fatalf("expected truncation hint, got %.120q", out)
	}
	if !strings.Contains(out, "TAILMARK") {
		t.Fatal("byte truncation should retain the tail (TAILMARK)")
	}
	// Over the line cap but under the byte cap: line truncation.
	var sb strings.Builder
	for i := 0; i < toolOutputMaxLines+500; i++ {
		sb.WriteString("L\n")
	}
	lineOut, lineTrunc := TruncateToolOutput(sb.String())
	if !lineTrunc || !strings.Contains(lineOut, "lines truncated") {
		t.Fatalf("over-line-cap input should report line truncation, got %.120q", lineOut)
	}
}
