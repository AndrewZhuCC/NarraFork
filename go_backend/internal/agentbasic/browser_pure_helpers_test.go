package agentbasic

import (
	"runtime"
	"testing"

	"github.com/go-rod/rod/lib/input"
)

// TestValidateBrowserURL locks the Browser launch URL protocol gate, mirroring
// the TypeScript ALLOWED_PROTOCOLS set (http:, https:, data:) in
// server/lib/agent/tools/browser.ts: data URLs pass without a host, http/https
// require a host, and every other scheme (or an empty URL) is rejected.
func TestValidateBrowserURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"http with host", "http://example.com", false},
		{"https with host and path", "https://example.com/page?q=1", false},
		{"data url no host", "data:text/html,<p>hi</p>", false},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"file scheme rejected", "file:///etc/passwd", true},
		{"ftp scheme rejected", "ftp://example.com/x", true},
		{"http missing host", "http:///just-path", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBrowserURL(tc.url)
			if tc.wantErr && err == nil {
				t.Fatalf("validateBrowserURL(%q): expected error, got nil", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateBrowserURL(%q): unexpected error %v", tc.url, err)
			}
		})
	}
}

// TestBrowserKeysSpecialNames verifies the special-key name mapping (case- and
// alias-insensitive) used by the Browser type action.
func TestBrowserKeysSpecialNames(t *testing.T) {
	cases := []struct {
		input string
		want  input.Key
	}{
		{"Enter", input.Enter},
		{"return", input.Enter},
		{"ESC", input.Escape},
		{"escape", input.Escape},
		{"Tab", input.Tab},
		{"Backspace", input.Backspace},
		{"del", input.Delete},
		{"Delete", input.Delete},
		{"up", input.ArrowUp},
		{"ArrowDown", input.ArrowDown},
		{"left", input.ArrowLeft},
		{"ArrowRight", input.ArrowRight},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			keys, err := browserKeys(tc.input)
			if err != nil {
				t.Fatalf("browserKeys(%q): unexpected error %v", tc.input, err)
			}
			if len(keys) != 1 || keys[0] != tc.want {
				t.Fatalf("browserKeys(%q): expected [%v], got %v", tc.input, tc.want, keys)
			}
		})
	}
}

// TestBrowserKeysLiteralCharacters covers the fall-through path: a non-special
// string is expanded to one input.Key per ASCII rune.
func TestBrowserKeysLiteralCharacters(t *testing.T) {
	keys, err := browserKeys("ab")
	if err != nil {
		t.Fatalf("browserKeys(\"ab\"): unexpected error %v", err)
	}
	if len(keys) != 2 || keys[0] != input.Key('a') || keys[1] != input.Key('b') {
		t.Fatalf("browserKeys(\"ab\"): expected ['a','b'], got %v", keys)
	}
}

// TestBrowserKeysEmptyRejected covers the empty-input guard.
func TestBrowserKeysEmptyRejected(t *testing.T) {
	if _, err := browserKeys("   "); err == nil {
		t.Fatal("browserKeys(blank): expected error, got nil")
	}
}

// TestBrowserKeysNonASCIIRejected covers the non-ASCII guard that steers
// callers toward the value (text input) path for multi-byte characters.
func TestBrowserKeysNonASCIIRejected(t *testing.T) {
	if _, err := browserKeys("中"); err == nil {
		t.Fatal("browserKeys(non-ascii): expected error, got nil")
	}
}

// TestNormalizeBrowserExpression locks the evaluate-expression wrapping: bare
// expressions are wrapped into an arrow function returning the value, while
// already-callable forms (function/async/arrow) are passed through unchanged.
func TestNormalizeBrowserExpression(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare value wrapped", "document.title", "() => (document.title)"},
		{"trims then wraps", "  1 + 1  ", "() => (1 + 1)"},
		{"function passthrough", "function () { return 1; }", "function () { return 1; }"},
		{"async passthrough", "async () => 1", "async () => 1"},
		{"arrow passthrough", "x => x + 1", "x => x + 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeBrowserExpression(tc.in); got != tc.want {
				t.Fatalf("normalizeBrowserExpression(%q): expected %q, got %q", tc.in, tc.want, got)
			}
		})
	}
}

// TestEnsureHeadedBrowserAllowedHeadlessAlwaysOK confirms headless mode never
// requires a graphical environment regardless of DISPLAY/WAYLAND_DISPLAY.
func TestEnsureHeadedBrowserAllowedHeadlessAlwaysOK(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	if err := ensureHeadedBrowserAllowed(true); err != nil {
		t.Fatalf("ensureHeadedBrowserAllowed(headless=true): unexpected error %v", err)
	}
}

// TestEnsureHeadedBrowserAllowedHeadedRequiresDisplay covers the headed-mode
// graphical-environment guard on non-Windows platforms.
func TestEnsureHeadedBrowserAllowedHeadedRequiresDisplay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("headed mode is always allowed on Windows")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	if err := ensureHeadedBrowserAllowed(false); err == nil {
		t.Fatal("ensureHeadedBrowserAllowed(headed, no display): expected error, got nil")
	}
	t.Setenv("DISPLAY", ":0")
	if err := ensureHeadedBrowserAllowed(false); err != nil {
		t.Fatalf("ensureHeadedBrowserAllowed(headed, DISPLAY=:0): unexpected error %v", err)
	}
}
