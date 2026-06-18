package misccompat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMiscCompatStringValidationUsesRuneCount is a regression test for the
// byte-vs-character length divergence across misccompat validators. The TS
// schemas (server/lib/validators/hooks.ts, containers.ts, workspaces.ts) use
// Zod .max()/.min() which count UTF-16 code units (String.prototype.length); Go
// previously used len() (UTF-8 bytes) and then utf8.RuneCountInString (runes).
// Both diverge from JS: rune count over-accepts astral characters (an emoji is 1
// rune but 2 UTF-16 units), so these tests also assert emoji boundaries.

func TestStringMapFieldUsesRuneCount(t *testing.T) {
	// headers value: TS z.string().max(2000); 2000 CJK chars = 6000 bytes.
	val2000 := strings.Repeat("头", 2000)
	if _, err := stringMapField(map[string]any{"Authorization": val2000}, "headers", 2000); err != nil {
		t.Fatalf("2000 CJK-char header value should pass: %v", err)
	}
	val2001 := strings.Repeat("头", 2001)
	if _, err := stringMapField(map[string]any{"Authorization": val2001}, "headers", 2000); err == nil {
		t.Fatal("2001 CJK-char header value should be rejected")
	}
	// 1001 emoji = 2002 UTF-16 units: rejected (rune count would see only 1001).
	if _, err := stringMapField(map[string]any{"Authorization": strings.Repeat("😀", 1001)}, "headers", 2000); err == nil {
		t.Fatal("1001-emoji header value (2002 UTF-16 units) should be rejected")
	}
}

func TestValidateVolumeSnapshotDescriptionUsesRuneCount(t *testing.T) {
	// TS z.string().max(2000).
	desc2000 := strings.Repeat("描", 2000)
	if _, err := validateVolumeSnapshotDescription(desc2000, false); err != nil {
		t.Fatalf("2000 CJK-char description should pass: %v", err)
	}
	desc2001 := strings.Repeat("描", 2001)
	if _, err := validateVolumeSnapshotDescription(desc2001, false); err == nil {
		t.Fatal("2001 CJK-char description should be rejected")
	}
	// 1001 emoji = 2002 UTF-16 units: rejected.
	if _, err := validateVolumeSnapshotDescription(strings.Repeat("😀", 1001), false); err == nil {
		t.Fatal("1001-emoji description (2002 UTF-16 units) should be rejected")
	}
}

func TestDecodeCreateHookBodyUsesRuneCount(t *testing.T) {
	// matcher: TS z.string().max(200). 200 CJK chars = 600 bytes, must pass.
	matcher200 := strings.Repeat("配", 200)
	body := `{"event":"PreToolUse","type":"command","command":"echo hi","matcher":"` + matcher200 + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/hooks", strings.NewReader(body))
	parsed, err := decodeCreateHookBody(req)
	if err != nil {
		t.Fatalf("200 CJK-char matcher should pass: %v", err)
	}
	if parsed.Matcher != matcher200 {
		t.Fatalf("matcher not preserved: %q", parsed.Matcher)
	}

	// 201 chars must be rejected.
	matcher201 := strings.Repeat("配", 201)
	body2 := `{"event":"PreToolUse","type":"command","command":"echo hi","matcher":"` + matcher201 + `"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/hooks", strings.NewReader(body2))
	if _, err := decodeCreateHookBody(req2); err == nil {
		t.Fatal("201 CJK-char matcher should be rejected")
	}

	// 101 emoji = 202 UTF-16 units: rejected (rune count would see only 101).
	matcherEmoji := strings.Repeat("😀", 101)
	body3 := `{"event":"PreToolUse","type":"command","command":"echo hi","matcher":"` + matcherEmoji + `"}`
	req3 := httptest.NewRequest(http.MethodPost, "/api/hooks", strings.NewReader(body3))
	if _, err := decodeCreateHookBody(req3); err == nil {
		t.Fatal("101-emoji matcher (202 UTF-16 units) should be rejected")
	}
}

// TestDecodeCreateHookBodyURLMatchesZodURL pins the http-hook url validation to
// the TS createHookSchema url: z.string().url().max(2000), which runs
// new URL(value.trim()) and STORES the trimmed value. Two behaviours must match
// the JS URL constructor (and previously diverged from the Scheme&&Host check):
//   - a whitespace-padded URL is accepted and persisted trimmed; and
//   - a bare special scheme with no host ("https://") is rejected.
func TestDecodeCreateHookBodyURLMatchesZodURL(t *testing.T) {
	// Surrounding whitespace is trimmed: the old url.Parse path 400-ed this, but
	// Zod .url() trims first, so it must be accepted and stored as the trimmed URL.
	padded := `{"event":"PostToolUse","type":"http","url":"  https://hooks.example.com/path  "}`
	req := httptest.NewRequest(http.MethodPost, "/api/hooks", strings.NewReader(padded))
	parsed, err := decodeCreateHookBody(req)
	if err != nil {
		t.Fatalf("whitespace-padded https url should be accepted (Zod trims), got %v", err)
	}
	if parsed.URL != "https://hooks.example.com/path" {
		t.Fatalf("url should be stored trimmed, got %q", parsed.URL)
	}

	// A bare special scheme with no host is rejected (new URL("https://") throws).
	for _, bad := range []string{
		`{"event":"PostToolUse","type":"http","url":"https://"}`,
		`{"event":"PostToolUse","type":"http","url":"not-a-url"}`,
		`{"event":"PostToolUse","type":"http","url":"//example.com"}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/hooks", strings.NewReader(bad))
		if _, err := decodeCreateHookBody(r); err == nil {
			t.Fatalf("invalid hook url should be rejected: %s", bad)
		}
	}
}

func TestDecodeWorkspaceBodyUsesRuneCount(t *testing.T) {
	// title: TS z.string().max(200). 200 CJK chars must pass.
	title200 := strings.Repeat("工", 200)
	body := `{"title":"` + title200 + `","tree":"[]"}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(body))
	parsed, err := decodeWorkspaceBody(req, true)
	if err != nil {
		t.Fatalf("200 CJK-char title should pass: %v", err)
	}
	if parsed.Title != title200 {
		t.Fatalf("title not preserved")
	}

	// title 201 chars rejected.
	title201 := strings.Repeat("工", 201)
	body2 := `{"title":"` + title201 + `","tree":"[]"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(body2))
	if _, err := decodeWorkspaceBody(req2, true); err == nil {
		t.Fatal("201 CJK-char title should be rejected")
	}

	// 101 emoji = 202 UTF-16 units: rejected (rune count would see only 101).
	titleEmoji := strings.Repeat("😀", 101)
	body3 := `{"title":"` + titleEmoji + `","tree":"[]"}`
	req3 := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(body3))
	if _, err := decodeWorkspaceBody(req3, true); err == nil {
		t.Fatal("101-emoji title (202 UTF-16 units) should be rejected")
	}
}
