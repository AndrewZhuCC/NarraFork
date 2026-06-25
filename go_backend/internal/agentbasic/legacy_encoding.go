package agentbasic

import (
	"strings"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// legacyEncodingConfidenceThreshold mirrors the TS encoding.ts CONFIDENCE_THRESHOLD
// (server/lib/agent/tools/encoding.ts:14): the minimum chardet confidence (0-100)
// required to trust a detected non-UTF-8 charset. Below this, the file is treated
// as UTF-8.
const legacyEncodingConfidenceThreshold = 70

// legacyTextDetector is the shared ICU-derived charset detector (saintfish/chardet,
// same ICU lineage as the npm `chardet` package TS uses). Constructing it is cheap
// (it just references the static recognizer table), but a package-level instance
// avoids repeated slice allocation on hot Read/Write paths.
var legacyTextDetector = chardet.NewTextDetector()

// legacyDecodeFileText mirrors the TS readFileText legacy-encoding branch
// (encoding.ts:23-29): detect the charset with chardet, and if a non-UTF-8 charset
// is detected with confidence >= threshold, decode the bytes from that charset.
// Otherwise fall back to interpreting the bytes as UTF-8. Returns the decoded text
// plus the canonical encoding name so callers (Write/Edit) can write back in the
// same encoding.
func legacyDecodeFileText(data []byte) (text string, encodingName string) {
	best, err := legacyTextDetector.DetectBest(data)
	if err != nil || best == nil || best.Confidence < legacyEncodingConfidenceThreshold {
		return string(data), "utf-8"
	}
	enc, canonical, isUTF8 := legacyCharsetToEncoding(best.Charset)
	if isUTF8 || enc == nil {
		return string(data), "utf-8"
	}
	decoded, _, decErr := transform.Bytes(enc.NewDecoder(), data)
	if decErr != nil {
		// Decoding failed (malformed bytes for the guessed charset): fall back to
		// UTF-8 rather than returning a partial/garbled decode, matching the
		// conservative spirit of the TS path (iconv.decode is lenient but we prefer
		// not to corrupt on a mis-detect).
		return string(data), "utf-8"
	}
	return string(decoded), canonical
}

// legacyEncodeText mirrors the TS writeFileText legacy-encoding branch
// (encoding.ts:42-48): when the target encoding is not UTF-8, encode the content
// back into that encoding before writing; otherwise write the UTF-8 bytes as-is.
func legacyEncodeText(content, encodingName string) ([]byte, error) {
	enc, _, isUTF8 := legacyCharsetToEncoding(encodingName)
	if isUTF8 || enc == nil {
		return []byte(content), nil
	}
	encoded, _, err := transform.Bytes(enc.NewEncoder(), []byte(content))
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// legacyCharsetToEncoding maps a charset name to a golang.org/x/text Encoding.
// The input names are the IANA-style names emitted by saintfish/chardet's
// recognizers (e.g. "GB-18030" with a hyphen, "Big5", "Shift_JIS", "EUC-JP",
// "EUC-KR", "windows-1252", "ISO-8859-1"); the TS-side normalizeEncoding aliases
// (encoding.ts:56-68, e.g. gb2312 -> gbk, ascii -> utf-8) are also accepted so the
// behavior matches even if a caller passes a TS-style name. Matching is
// case-insensitive. UTF-8/ASCII (and any unknown charset) return isUTF8=true so
// callers treat the bytes as UTF-8 (conservative: never corrupt on a mismatch).
func legacyCharsetToEncoding(name string) (enc encoding.Encoding, canonical string, isUTF8 bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "utf-8", "utf8", "ascii", "us-ascii":
		return nil, "utf-8", true
	// Simplified Chinese: chardet emits "GB-18030"; TS aliases gb2312/gb18030/gbk.
	case "gb-18030", "gb18030", "gb2312", "gbk":
		return simplifiedchinese.GB18030, "gb-18030", false
	// Traditional Chinese.
	case "big5", "big-5":
		return traditionalchinese.Big5, "big5", false
	// Japanese.
	case "shift_jis", "shift-jis", "sjis", "shiftjis":
		return japanese.ShiftJIS, "shift_jis", false
	case "euc-jp", "eucjp":
		return japanese.EUCJP, "euc-jp", false
	case "iso-2022-jp":
		return japanese.ISO2022JP, "iso-2022-jp", false
	// Korean.
	case "euc-kr", "euckr":
		return korean.EUCKR, "euc-kr", false
	// Western / Cyrillic / Greek / Hebrew / Arabic / Turkish single-byte.
	case "windows-1250":
		return charmap.Windows1250, "windows-1250", false
	case "windows-1251":
		return charmap.Windows1251, "windows-1251", false
	case "windows-1252":
		return charmap.Windows1252, "windows-1252", false
	case "windows-1253":
		return charmap.Windows1253, "windows-1253", false
	case "windows-1254":
		return charmap.Windows1254, "windows-1254", false
	case "windows-1255":
		return charmap.Windows1255, "windows-1255", false
	case "windows-1256":
		return charmap.Windows1256, "windows-1256", false
	case "iso-8859-1", "iso8859-1", "latin1":
		return charmap.ISO8859_1, "iso-8859-1", false
	case "iso-8859-2", "iso8859-2":
		return charmap.ISO8859_2, "iso-8859-2", false
	case "iso-8859-5", "iso8859-5":
		return charmap.ISO8859_5, "iso-8859-5", false
	case "iso-8859-6", "iso8859-6":
		return charmap.ISO8859_6, "iso-8859-6", false
	case "iso-8859-7", "iso8859-7":
		return charmap.ISO8859_7, "iso-8859-7", false
	case "iso-8859-8", "iso8859-8":
		return charmap.ISO8859_8, "iso-8859-8", false
	case "iso-8859-9", "iso8859-9":
		return charmap.ISO8859_9, "iso-8859-9", false
	case "koi8-r":
		return charmap.KOI8R, "koi8-r", false
	default:
		// Unknown charset (e.g. UTF-16/UTF-32 BOM-based, ISO-2022-CN/KR, IBM4xx):
		// treat as UTF-8 so we never produce a corrupting transcode on a charset we
		// cannot faithfully round-trip here.
		return nil, "utf-8", true
	}
}
