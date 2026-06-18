package id

import (
	"strings"
	"testing"
)

// TestGenerateProducesAlphabetOnlyIDsOfRequestedSize verifies the nanoid-style
// generator: requested length is honored, the default (21) applies for non-
// positive sizes, every character is from the URL-safe alphabet, and generated
// IDs are unique across a sample.
func TestGenerateProducesAlphabetOnlyIDsOfRequestedSize(t *testing.T) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ_abcdefghijklmnopqrstuvwxyz-"

	for _, size := range []int{1, 8, 21, 48} {
		got, err := Generate(size)
		if err != nil {
			t.Fatalf("Generate(%d): %v", size, err)
		}
		if len(got) != size {
			t.Fatalf("Generate(%d) length = %d", size, len(got))
		}
		for _, r := range got {
			if !strings.ContainsRune(alphabet, r) {
				t.Fatalf("Generate(%d) produced out-of-alphabet rune %q in %q", size, r, got)
			}
		}
	}

	// Non-positive sizes fall back to the default length of 21.
	for _, size := range []int{0, -5} {
		got, err := Generate(size)
		if err != nil {
			t.Fatalf("Generate(%d): %v", size, err)
		}
		if len(got) != 21 {
			t.Fatalf("Generate(%d) should default to length 21, got %d", size, len(got))
		}
	}
}

// TestNewAndShortLengthsAndUniqueness pins the New()/Short() default lengths and
// basic uniqueness used for primary keys.
func TestNewAndShortLengthsAndUniqueness(t *testing.T) {
	if len(New()) != 21 {
		t.Fatalf("New() should be length 21, got %d", len(New()))
	}
	if len(Short()) != 8 {
		t.Fatalf("Short() should be length 8, got %d", len(Short()))
	}

	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		v := New()
		if seen[v] {
			t.Fatalf("New() produced a duplicate id %q at iteration %d", v, i)
		}
		seen[v] = true
	}
}

// TestMustReturnsValidID confirms Must returns a usable id of the requested size.
func TestMustReturnsValidID(t *testing.T) {
	if got := Must(12); len(got) != 12 {
		t.Fatalf("Must(12) length = %d", len(got))
	}
}
