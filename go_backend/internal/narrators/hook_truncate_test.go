package narrators

import (
	"strings"
	"testing"
)

// TestTruncateToolHookValueMatchesTSUTF16 pins truncateToolHookValue to the TS
// truncateValue (server/lib/agent/tool-executor.ts): a string longer than 500
// UTF-16 code units becomes `${val.slice(0, 500)}… [truncated, ${val.length} chars
// total]`, all measured in UTF-16. Go previously used byte length + a byte slice,
// which for a non-ASCII value diverged on the >500 gate, reported the wrong "chars
// total", and could cut mid-rune (invalid UTF-8).
func TestTruncateToolHookValueMatchesTSUTF16(t *testing.T) {
	// ASCII under 500: unchanged.
	if got := truncateToolHookValue("short"); got != "short" {
		t.Fatalf("short ASCII = %q, want unchanged", got)
	}

	// 600 CJK chars (1800 bytes, 600 UTF-16 units) > 500: sliced to 500 UTF-16
	// units (valid UTF-8), reported total is 600 (UTF-16), not the byte count.
	cjk := strings.Repeat("章", 600)
	got, ok := truncateToolHookValue(cjk).(string)
	if !ok {
		t.Fatalf("expected truncated string, got %T", truncateToolHookValue(cjk))
	}
	want := strings.Repeat("章", 500) + "… [truncated, 600 chars total]"
	if got != want {
		t.Fatalf("cjk truncation mismatch:\n got %q\nwant %q", got, want)
	}
	for _, r := range got {
		if r == 0xFFFD {
			t.Fatalf("cjk truncation produced invalid UTF-8 (U+FFFD): %q", got)
		}
	}

	// A 500-rune-but-1000-byte string (e.g. accented Latin) must NOT be truncated:
	// 500 UTF-16 units is not > 500, while a byte-length gate (1000 > 500) would
	// wrongly truncate it.
	accented := strings.Repeat("é", 500) // é is 1 UTF-16 unit, 2 UTF-8 bytes
	if got := truncateToolHookValue(accented); got != accented {
		t.Fatalf("500-unit accented string should be unchanged, got truncated (len %d)", len(got.(string)))
	}

	// Nested map/array values are recursively truncated.
	nested := map[string]any{"cmd": cjk, "args": []any{"ok", cjk}}
	out, ok := truncateToolHookValue(nested).(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", truncateToolHookValue(nested))
	}
	if s, _ := out["cmd"].(string); !strings.HasSuffix(s, "600 chars total]") {
		t.Fatalf("nested cmd not truncated: %q", s)
	}
	arr, _ := out["args"].([]any)
	if len(arr) != 2 || arr[0] != "ok" {
		t.Fatalf("nested args unexpected: %#v", arr)
	}
	if s, _ := arr[1].(string); !strings.HasSuffix(s, "600 chars total]") {
		t.Fatalf("nested array element not truncated: %q", s)
	}
}

// TestNormalizeBackgroundCompletionNoticeCapsUTF16 pins normalizeBackgroundCompletionNotice
// to the TS capResult (server/services/bg-completion-queue.ts): cap the full result
// at 12000 UTF-16 code units (`result.length` / `result.slice(0, 12000)`). Go
// previously used byte length + a byte slice, which for a non-ASCII result diverged
// on the >12000 gate and could cut mid-rune (invalid UTF-8).
func TestNormalizeBackgroundCompletionNoticeCapsUTF16(t *testing.T) {
	// A 12000-CJK result (36000 bytes, 12000 UTF-16 units) is NOT truncated: 12000
	// is not > 12000, while a byte gate (36000 > 12000) would wrongly truncate it.
	exact := backgroundCompletionNotice{ID: "t1", Result: strings.Repeat("章", 12000)}
	gotExact := normalizeBackgroundCompletionNotice(exact)
	if gotExact.ResultTruncated {
		t.Fatalf("12000-unit CJK result should not be truncated")
	}
	if gotExact.Result != strings.Repeat("章", 12000) {
		t.Fatalf("12000-unit CJK result should be unchanged")
	}

	// A 13000-CJK result (>12000 UTF-16 units) is capped to 12000 units, valid UTF-8.
	over := backgroundCompletionNotice{ID: "t2", Result: strings.Repeat("章", 13000)}
	gotOver := normalizeBackgroundCompletionNotice(over)
	if !gotOver.ResultTruncated {
		t.Fatalf("13000-unit CJK result should be truncated")
	}
	if jsLen := len([]rune(gotOver.Result)); jsLen != 12000 {
		t.Fatalf("capped CJK result kept %d runes, want 12000", jsLen)
	}
	for _, r := range gotOver.Result {
		if r == 0xFFFD {
			t.Fatalf("capped CJK result has invalid UTF-8 (U+FFFD)")
		}
	}
}
