package chapteredges

import (
	"strings"
	"testing"
)

// TestDependencyEdgeMetadataDescriptionUsesJSStringLength pins the chapter-edge
// metadata.description length validation to JavaScript String.prototype.length
// (UTF-16 code units), matching the TS createChapterEdgeSchema
// (server/lib/validators/chapters.ts: metadata.description z.string().max(500)).
// An emoji is 1 rune but 2 UTF-16 units, so 251 emoji = 502 units must be
// rejected even though a rune-count check would see only 251 and accept it.
func TestDependencyEdgeMetadataDescriptionUsesJSStringLength(t *testing.T) {
	// 500 CJK chars = 500 UTF-16 units: must pass max(500).
	if _, err := dependencyEdgeMetadata(map[string]any{"description": strings.Repeat("依", 500)}); err != nil {
		t.Fatalf("500 CJK-char description should pass max(500): %v", err)
	}
	// 501 CJK chars: rejected.
	if _, err := dependencyEdgeMetadata(map[string]any{"description": strings.Repeat("依", 501)}); err == nil {
		t.Fatal("501 CJK-char description should be rejected")
	}
	// 250 emoji = 500 UTF-16 units: passes (boundary).
	if _, err := dependencyEdgeMetadata(map[string]any{"description": strings.Repeat("😀", 250)}); err != nil {
		t.Fatalf("250-emoji description (500 UTF-16 units) should pass max(500): %v", err)
	}
	// 251 emoji = 502 UTF-16 units: rejected. A rune-count check would see 251 and accept.
	if _, err := dependencyEdgeMetadata(map[string]any{"description": strings.Repeat("😀", 251)}); err == nil {
		t.Fatal("251-emoji description (502 UTF-16 units) should be rejected by max(500)")
	}
}
