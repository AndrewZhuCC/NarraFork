package narrators

import "testing"

// TestBufferSummariesShapeMatchesTS pins bufferSummaries to the TypeScript
// toBufferSummary (server/services/narrator-buffer.ts):
//   - id / text / bufferedAt / imageCount are always present
//   - creator is ALWAYS present (null when absent), mirroring `creator: m.creator ?? null`
//   - priority is present ONLY when truthy, mirroring `priority: m.priority || undefined`
func TestBufferSummariesShapeMatchesTS(t *testing.T) {
	creator := map[string]any{"id": "u1", "username": "alice"}
	items := []BufferedMessage{
		// No creator, not priority, no images.
		{ID: "a", Text: "hello", BufferedAt: "2026-01-01T00:00:00Z"},
		// With creator + priority + two images.
		{ID: "b", Text: "world", BufferedAt: "2026-01-02T00:00:00Z", CreatorJSON: creator, Priority: true, ImagesJSON: []any{"i1", "i2"}},
	}
	out := bufferSummaries(items)
	if len(out) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(out))
	}

	a := out[0]
	// Always-present fields.
	for _, k := range []string{"id", "text", "bufferedAt", "imageCount", "creator"} {
		if _, ok := a[k]; !ok {
			t.Fatalf("summary[0] missing key %q: %#v", k, a)
		}
	}
	if a["imageCount"].(int) != 0 {
		t.Fatalf("summary[0] imageCount = %v, want 0", a["imageCount"])
	}
	// creator must be present and explicitly nil when absent (matches TS `?? null`).
	if a["creator"] != nil {
		t.Fatalf("summary[0] creator = %#v, want nil", a["creator"])
	}
	// priority must be omitted when falsy (matches TS `|| undefined`).
	if _, ok := a["priority"]; ok {
		t.Fatalf("summary[0] should omit priority when false: %#v", a)
	}

	b := out[1]
	if b["imageCount"].(int) != 2 {
		t.Fatalf("summary[1] imageCount = %v, want 2", b["imageCount"])
	}
	gotCreator, ok := b["creator"].(map[string]any)
	if !ok || gotCreator["username"] != "alice" {
		t.Fatalf("summary[1] creator = %#v, want alice creator object", b["creator"])
	}
	if b["priority"] != true {
		t.Fatalf("summary[1] priority = %#v, want true", b["priority"])
	}
}
