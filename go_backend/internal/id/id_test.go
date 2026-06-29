package id

import (
	"strings"
	"testing"
)

func TestNewLength(t *testing.T) {
	if got := len(New()); got != DefaultLength {
		t.Errorf("New() length = %d, want %d", got, DefaultLength)
	}
	if got := len(NewShort()); got != ShortLength {
		t.Errorf("NewShort() length = %d, want %d", got, ShortLength)
	}
}

func TestNewUsesAlphabetOnly(t *testing.T) {
	// Every character of every generated ID must come from the alphabet — this
	// guards the byte&63 masking against indexing out of range.
	for i := 0; i < 1000; i++ {
		for _, c := range New() {
			if !strings.ContainsRune(alphabet, c) {
				t.Fatalf("ID contains char %q outside alphabet", c)
			}
		}
	}
}

func TestNewIsRandom(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		v := New()
		if _, dup := seen[v]; dup {
			t.Fatalf("duplicate ID generated: %q", v)
		}
		seen[v] = struct{}{}
	}
}

// TestAlphabetIsPowerOfTwo documents the invariant the masking relies on:
// the alphabet must be exactly 64 chars so byte&63 is uniform.
func TestAlphabetIsPowerOfTwo(t *testing.T) {
	if len(alphabet) != 64 {
		t.Fatalf("alphabet length = %d, want 64 (byte&63 indexing requires it)", len(alphabet))
	}
}
