package agentbasic

import (
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
)

// Bash/tool output truncation limits, mirroring the TS truncate.ts constants
// (server/lib/agent/truncate.ts): a dual line + byte cap, keeping the TAIL of the
// output because the most recent output (e.g. a build/test error at the end) is
// usually the most relevant.
const (
	toolOutputMaxLines = 2000
	toolOutputMaxBytes = 50 * 1024
)

// tailCaptureBuffer captures streamed tool output while retaining only the most
// recent toolOutputMaxBytes bytes (the tail). It tracks the total byte and line
// counts seen so truncation can be reported accurately, and forwards the bounded
// tail to onWrite on every write.
//
// Unlike a growing buffer that copies the whole accumulated string on each write
// (an O(n^2) trap that also violates the "do not broadcast ever-growing
// cumulative strings" backend rule), this buffer's onWrite payload and internal
// retained slice are bounded by cap, so streaming stays O(total) regardless of
// how much output a command produces.
type tailCaptureBuffer struct {
	cap        int
	mu         sync.Mutex
	tail       []byte
	totalBytes int
	totalLines int
	onWrite    func(string)
}

func newTailCaptureBuffer(capBytes int, onWrite func(string)) *tailCaptureBuffer {
	if capBytes <= 0 {
		capBytes = toolOutputMaxBytes
	}
	return &tailCaptureBuffer{cap: capBytes, onWrite: onWrite}
}

func (b *tailCaptureBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.totalBytes += len(p)
	for _, c := range p {
		if c == '\n' {
			b.totalLines++
		}
	}
	if len(p) >= b.cap {
		// The new chunk alone fills/overflows the cap: keep only its tail.
		b.tail = append(b.tail[:0], p[len(p)-b.cap:]...)
	} else {
		combined := len(b.tail) + len(p)
		if combined > b.cap {
			// Drop the oldest bytes to make room for the new chunk.
			drop := combined - b.cap
			b.tail = b.tail[drop:]
		}
		b.tail = append(b.tail, p...)
	}
	current := string(b.tail)
	b.mu.Unlock()
	if b.onWrite != nil {
		b.onWrite(current)
	}
	return len(p), nil
}

// Tail returns the retained tail content (bounded by cap).
func (b *tailCaptureBuffer) Tail() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.tail)
}

// TotalBytes returns the total number of bytes written (including dropped).
func (b *tailCaptureBuffer) TotalBytes() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalBytes
}

// TotalLines returns the total number of newline-delimited lines seen.
func (b *tailCaptureBuffer) TotalLines() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Number of lines is newline count + 1 when there is trailing content, matching
	// the way `text.split("\n").length` counts in the TS truncate helper.
	if b.totalBytes == 0 {
		return 0
	}
	return b.totalLines + 1
}

// Truncated reports whether any output was dropped (byte or line cap exceeded).
func (b *tailCaptureBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalBytes > b.cap || (b.totalLines+1) > toolOutputMaxLines
}

// TruncateToolOutput is a convenience wrapper around truncateToolOutput for
// callers that hold the full output as a plain string (e.g. the Terminal tool's
// last_n_lines branch) rather than a tailCaptureBuffer. It returns the input
// unchanged when it is within the line/byte caps; otherwise it tail-truncates to
// the last toolOutputMaxBytes bytes and applies the same dual-cap algorithm.
func TruncateToolOutput(text string) (string, bool) {
	totalBytes := len(text)
	totalLines := strings.Count(text, "\n")
	if totalBytes > 0 {
		totalLines++
	}
	if totalBytes <= toolOutputMaxBytes && totalLines <= toolOutputMaxLines {
		return text, false
	}
	tail := text
	if totalBytes > toolOutputMaxBytes {
		tail = text[totalBytes-toolOutputMaxBytes:]
	}
	return truncateToolOutput(tail, totalBytes, totalLines)
}

// truncateToolOutput applies the TS truncateOutput dual line/byte cap to tail
// content that was captured by a tailCaptureBuffer. tailContent is already the
// last <=toolOutputMaxBytes bytes; totalBytes/totalLines describe the full
// (pre-truncation) output so the hint can report accurate omitted counts.
//
// It mirrors server/lib/agent/truncate.ts: keep the tail, cap by lines (last
// toolOutputMaxLines lines) when the line count is the binding limit, character-
// truncate a single oversized line by UTF-16 code units (JS string slice
// semantics), and prepend an omitted-count header plus a recovery hint. Unlike
// TS it does NOT persist the full output to a temp file, because the Go Read tool
// sandboxes paths to the working directory and could not read a /tmp file; the
// hint therefore tells the model to re-run with a narrower filter.
func truncateToolOutput(tailContent string, totalBytes, totalLines int) (string, bool) {
	byteTruncated := totalBytes > toolOutputMaxBytes
	lineTruncated := totalLines > toolOutputMaxLines
	if !byteTruncated && !lineTruncated {
		return tailContent, false
	}

	content := tailContent
	keptLines := strings.Count(content, "\n")
	if content != "" {
		keptLines++
	}

	// If the line count is the binding limit, keep only the last N lines of the
	// already-byte-bounded tail.
	if lineTruncated {
		lines := strings.Split(content, "\n")
		if len(lines) > toolOutputMaxLines {
			lines = lines[len(lines)-toolOutputMaxLines:]
			content = strings.Join(lines, "\n")
			keptLines = toolOutputMaxLines
		}
	}

	// If a single retained line still exceeds the byte cap (e.g. minified output
	// with no newlines), keep its UTF-16 tail so the model still sees something.
	if len(content) > toolOutputMaxBytes && !strings.Contains(content, "\n") {
		units := utf16.Encode([]rune(content))
		// Find the largest tail (in code units) whose UTF-8 size fits the cap.
		cut := len(units)
		for cut > 0 && len(string(utf16.Decode(units[len(units)-cut:]))) > toolOutputMaxBytes {
			cut = cut * 9 / 10
		}
		if cut > 0 {
			content = "[line truncated]…" + string(utf16.Decode(units[len(units)-cut:]))
			keptLines = 1
		}
	}

	var header string
	if lineTruncated {
		header = "..." + strconv.Itoa(totalLines-keptLines) + " lines truncated..."
	} else {
		header = "..." + strconv.Itoa(totalBytes-len(content)) + " bytes truncated..."
	}
	hint := "\n\n⚠️ OUTPUT TRUNCATED — only the last " + strconv.Itoa(keptLines) + " of " + strconv.Itoa(totalLines) +
		" lines shown (earlier output omitted). The full output was NOT saved. " +
		"Re-run the command with a narrower filter (e.g. pipe through tail/grep/head) to see the omitted output."
	return header + "\n\n" + content + hint, true
}
