package agentbasic

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// gbkBytes encodes a UTF-8 string into GBK (GB18030) bytes for fixtures, so the
// tests exercise real non-UTF-8 file content without depending on byte literals.
func gbkBytes(t *testing.T, s string) []byte {
	t.Helper()
	encoded, _, err := transform.Bytes(simplifiedchinese.GB18030.NewEncoder(), []byte(s))
	if err != nil {
		t.Fatalf("encode GBK fixture: %v", err)
	}
	return encoded
}

// toolArgsJSON builds a JSON tool-arguments object, correctly escaping CJK/UTF-8
// string values via encoding/json.
func toolArgsJSON(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal tool args: %v", err)
	}
	return string(raw)
}

func legacyEncodingCtx() context.Context {
	return ContextWithToolRuntimeSettings(context.Background(), ToolRuntimeSettings{LegacyEncoding: true})
}

// TestLegacyDecodeDetectsGBK verifies legacyDecodeFileText detects a GBK file and
// decodes it back to the original UTF-8 text, mirroring the TS readFileText
// chardet+iconv path (encoding.ts:23-29).
func TestLegacyDecodeDetectsGBK(t *testing.T) {
	// A longer Chinese sentence gives chardet enough signal to detect GB-18030.
	original := "这是一段用于编码检测的简体中文示例文本，包含足够多的汉字以便字符集识别器给出高置信度的判断结果。"
	data := gbkBytes(t, original)
	text, enc := legacyDecodeFileText(data)
	if text != original {
		t.Fatalf("decoded text mismatch:\n got: %q\nwant: %q", text, original)
	}
	if !strings.Contains(strings.ToLower(enc), "gb") {
		t.Fatalf("expected a GB-family encoding, got %q", enc)
	}
}

// TestLegacyDecodeFallsBackToUTF8 verifies that content which is not a detectable
// non-UTF-8 charset is returned with the bytes interpreted losslessly. Pure ASCII
// may be reported by the ICU detector as ISO-8859-1 (a byte-identical superset of
// ASCII, exactly as TS's chardet does), so the invariant under test is that the
// decoded TEXT is unchanged; genuine UTF-8 content stays UTF-8.
func TestLegacyDecodeFallsBackToUTF8(t *testing.T) {
	asciiText := "plain ascii content\nsecond line\n"
	text, _ := legacyDecodeFileText([]byte(asciiText))
	if text != asciiText {
		t.Fatalf("ascii text should pass through unchanged, got %q", text)
	}
	// Genuine UTF-8 Chinese should be detected as UTF-8 and not mis-transcoded.
	utf8Text := "纯 UTF-8 中文内容，不应被重新编码，保持原样输出即可正确。"
	gotText, gotEnc := legacyDecodeFileText([]byte(utf8Text))
	if gotText != utf8Text {
		t.Fatalf("utf-8 text should pass through, got %q", gotText)
	}
	if gotEnc != "utf-8" {
		t.Fatalf("utf-8 content should map to utf-8, got %q", gotEnc)
	}
}

// TestLegacyEncodeRoundTrip verifies legacyEncodeText encodes content into a
// non-UTF-8 charset and the bytes decode back to the original (mirrors TS
// writeFileText iconv.encode path), while UTF-8 content is written as-is.
func TestLegacyEncodeRoundTrip(t *testing.T) {
	content := "回写时必须保持原始编码的中文内容。"
	encoded, err := legacyEncodeText(content, "gb-18030")
	if err != nil {
		t.Fatalf("legacyEncodeText: %v", err)
	}
	// Bytes must NOT be the raw UTF-8 bytes (i.e. a real transcode happened).
	if string(encoded) == content {
		t.Fatalf("expected GBK-encoded bytes to differ from UTF-8 content")
	}
	decoded, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), encoded)
	if err != nil {
		t.Fatalf("decode round-trip: %v", err)
	}
	if string(decoded) != content {
		t.Fatalf("round-trip mismatch:\n got: %q\nwant: %q", string(decoded), content)
	}
	// utf-8 encoding name returns the raw UTF-8 bytes.
	utf8Bytes, err := legacyEncodeText(content, "utf-8")
	if err != nil {
		t.Fatalf("legacyEncodeText utf-8: %v", err)
	}
	if string(utf8Bytes) != content {
		t.Fatalf("utf-8 encoding should return raw bytes")
	}
}

// TestReadToolDecodesLegacyEncodingFromContext verifies the Read tool decodes a
// GBK file when legacyEncoding is enabled in the context-carried runtime
// settings, and (control) returns garbled output when it is disabled — proving
// the setting is actually consumed.
func TestReadToolDecodesLegacyEncodingFromContext(t *testing.T) {
	cwd := t.TempDir()
	original := "第一行中文内容用于验证遗留编码读取功能是否正确解码。"
	if err := os.WriteFile(filepath.Join(cwd, "gbk.txt"), gbkBytes(t, original), 0o644); err != nil {
		t.Fatalf("write GBK fixture: %v", err)
	}

	// legacyEncoding enabled: the numbered output must contain the decoded text.
	out := ExecuteTool(legacyEncodingCtx(), cwd, "Read", `{"file_path":"gbk.txt"}`)
	if !strings.Contains(out, original) {
		t.Fatalf("legacy Read should decode GBK to original text, got: %q", out)
	}

	// legacyEncoding disabled (default ctx): the GBK bytes are interpreted as
	// UTF-8, so the original Chinese must NOT appear verbatim (garbled).
	control := ExecuteTool(context.Background(), cwd, "Read", `{"file_path":"gbk.txt"}`)
	if strings.Contains(control, original) {
		t.Fatalf("without legacyEncoding the GBK file should not decode to the original text, got: %q", control)
	}
}

// TestWriteToolPreservesLegacyEncoding verifies that overwriting an existing GBK
// file with legacyEncoding enabled writes the new content back in GBK rather than
// silently transcoding the file to UTF-8 (data corruption), mirroring TS write.ts.
func TestWriteToolPreservesLegacyEncoding(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "doc.txt")
	// The existing file must carry enough Chinese for the ICU detector to report
	// GB-18030 with confidence >= 70 (TS chardet has the same minimum-signal need).
	if err := os.WriteFile(path, gbkBytes(t, "这是文件中原有的简体中文内容，需要足够长以便编码检测器能够稳定识别出 GBK 编码。"), 0o644); err != nil {
		t.Fatalf("seed GBK file: %v", err)
	}

	newContent := "全新的中文内容，应当以原始 GBK 编码写回磁盘。"
	out := ExecuteTool(legacyEncodingCtx(), cwd, "Write",
		toolArgsJSON(t, map[string]any{"file_path": "doc.txt", "content": newContent}))
	if !strings.HasPrefix(out, "Wrote ") {
		t.Fatalf("unexpected Write output: %q", out)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// The on-disk bytes must decode as GBK to the new content (not be UTF-8).
	decoded, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw)
	if err != nil {
		t.Fatalf("decode written bytes as GBK: %v", err)
	}
	if string(decoded) != newContent {
		t.Fatalf("written file should be GBK-encoded new content:\n got: %q\nwant: %q", string(decoded), newContent)
	}
	if string(raw) == newContent {
		t.Fatalf("file should not have been written as raw UTF-8 (encoding not preserved)")
	}
}

// TestEditToolPreservesLegacyEncoding verifies Edit decodes a GBK file, applies
// the replacement, and writes the result back in GBK, mirroring TS edit.ts.
func TestEditToolPreservesLegacyEncoding(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "edit.txt")
	if err := os.WriteFile(path, gbkBytes(t, "保留这一行作为上下文内容。\n需要替换的旧文本\n这是结尾行，提供足够多的汉字以便编码检测器稳定识别 GBK。"), 0o644); err != nil {
		t.Fatalf("seed GBK file: %v", err)
	}

	out := ExecuteTool(legacyEncodingCtx(), cwd, "Edit",
		toolArgsJSON(t, map[string]any{"file_path": "edit.txt", "old_string": "需要替换的旧文本", "new_string": "已经替换的新文本"}))
	if !strings.HasPrefix(out, "Edited ") {
		t.Fatalf("unexpected Edit output: %q", out)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	decoded, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw)
	if err != nil {
		t.Fatalf("decode edited bytes as GBK: %v", err)
	}
	want := "保留这一行作为上下文内容。\n已经替换的新文本\n这是结尾行，提供足够多的汉字以便编码检测器稳定识别 GBK。"
	if string(decoded) != want {
		t.Fatalf("edited file should be GBK-encoded updated content:\n got: %q\nwant: %q", string(decoded), want)
	}
	if strings.Contains(string(decoded), "需要替换的旧文本") {
		t.Fatalf("old text should have been replaced")
	}
}

// TestGrepToolDecodesLegacyEncodingFromContext verifies the Grep tool decodes a
// GBK file before matching/printing when legacyEncoding is enabled, so a Chinese
// pattern matches and the printed line is readable text rather than mojibake.
// Mirrors TS grep.ts legacy-encoding path (rg --encoding none + chardet/iconv).
func TestGrepToolDecodesLegacyEncodingFromContext(t *testing.T) {
	cwd := t.TempDir()
	// Multi-line GBK file; the target line contains a distinctive Chinese phrase.
	content := "第一行普通中文内容用于增加编码检测的可信度。\n" +
		"目标行包含需要被检索匹配的关键中文短语。\n" +
		"第三行同样是中文用于补充检测样本的长度。"
	if err := os.WriteFile(filepath.Join(cwd, "zh.txt"), gbkBytes(t, content), 0o644); err != nil {
		t.Fatalf("write GBK fixture: %v", err)
	}

	// content mode with a Chinese pattern: must find the decoded target line.
	out := ExecuteTool(legacyEncodingCtx(), cwd, "Grep",
		toolArgsJSON(t, map[string]any{"pattern": "需要被检索匹配的关键中文短语", "output_mode": "content"}))
	if !strings.Contains(out, "需要被检索匹配的关键中文短语") {
		t.Fatalf("legacy Grep should match and print decoded Chinese line, got: %q", out)
	}
	if !strings.Contains(out, "zh.txt:2:") {
		t.Fatalf("content mode should print path:lineNo: prefix for the match, got: %q", out)
	}

	// control: without legacyEncoding the GBK bytes are treated as UTF-8, so the
	// Chinese pattern cannot match the mojibake (proves the setting is consumed).
	control := ExecuteTool(context.Background(), cwd, "Grep",
		toolArgsJSON(t, map[string]any{"pattern": "需要被检索匹配的关键中文短语", "output_mode": "content"}))
	if strings.Contains(control, "需要被检索匹配的关键中文短语") {
		t.Fatalf("without legacyEncoding the GBK file should not match the Chinese pattern, got: %q", control)
	}
}

// TestGrepToolLegacyEncodingFilesAndCountModes verifies files_with_matches and
// count modes also operate on the decoded GBK content.
func TestGrepToolLegacyEncodingFilesAndCountModes(t *testing.T) {
	cwd := t.TempDir()
	content := "匹配关键字出现在第一行用于测试编码检索功能。\n" +
		"普通文本行补充编码检测所需的样本长度信息。\n" +
		"匹配关键字再次出现在第三行以便统计计数模式。"
	if err := os.WriteFile(filepath.Join(cwd, "doc.txt"), gbkBytes(t, content), 0o644); err != nil {
		t.Fatalf("write GBK fixture: %v", err)
	}

	files := ExecuteTool(legacyEncodingCtx(), cwd, "Grep",
		toolArgsJSON(t, map[string]any{"pattern": "匹配关键字", "output_mode": "files_with_matches"}))
	if !strings.Contains(files, "doc.txt") {
		t.Fatalf("files_with_matches should list the GBK file, got: %q", files)
	}

	count := ExecuteTool(legacyEncodingCtx(), cwd, "Grep",
		toolArgsJSON(t, map[string]any{"pattern": "匹配关键字", "output_mode": "count"}))
	if !strings.Contains(count, "doc.txt:2") {
		t.Fatalf("count mode should report 2 matching lines in the decoded GBK file, got: %q", count)
	}
}
