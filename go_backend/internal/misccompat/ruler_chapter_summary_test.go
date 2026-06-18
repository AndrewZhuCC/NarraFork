package misccompat

import "testing"

// TestRulerChapterSummariesMatchTS pins rulerChapterSummaries to the TypeScript
// ruler data shapes (server/routes/ruler.ts):
//   - active chapters carry narratorId/narratorStatus and have NO mergeCommitSha key;
//   - merged chapters include mergeCommitSha and force narratorId/narratorStatus to
//     null even when a narrator row still references the chapter;
//   - chapters in other statuses are omitted from both lists.
func TestRulerChapterSummariesMatchTS(t *testing.T) {
	chapters := []map[string]any{
		{
			"id": "c-active", "title": "Active", "branch": "feat", "role": "branch",
			"status": "active", "parentChapterId": "root", "startCommitSha": "sha-a",
			"mergeCommitSha": "should-not-leak", "narratorId": "n1", "narratorStatus": "working",
			"axisOffset": float64(2), "crossOffset": float64(3),
		},
		{
			"id": "c-merged", "title": "Merged", "branch": "feat2", "role": "branch",
			"status": "merged", "parentChapterId": "root", "startCommitSha": "sha-m",
			"mergeCommitSha": "sha-merge", "narratorId": "n2", "narratorStatus": "idle",
			"axisOffset": float64(0), "crossOffset": float64(0),
		},
		{"id": "c-dormant", "title": "Dormant", "status": "dormant"},
		{"id": "c-abandoned", "title": "Abandoned", "status": "abandoned"},
	}

	active, merged := rulerChapterSummaries(chapters)

	if len(active) != 1 {
		t.Fatalf("expected 1 active chapter, got %d: %#v", len(active), active)
	}
	a := active[0]
	if a["id"] != "c-active" || a["narratorId"] != "n1" || a["narratorStatus"] != "working" {
		t.Fatalf("active chapter shape wrong: %#v", a)
	}
	// Active chapters MUST NOT carry a mergeCommitSha key (matches TS).
	if _, ok := a["mergeCommitSha"]; ok {
		t.Fatalf("active chapter must not include mergeCommitSha: %#v", a)
	}
	if a["startCommitSha"] != "sha-a" || a["parentChapterId"] != "root" {
		t.Fatalf("active chapter sha/parent wrong: %#v", a)
	}
	if a["axisOffset"].(float64) != 2 || a["crossOffset"].(float64) != 3 {
		t.Fatalf("active chapter offsets wrong: %#v", a)
	}

	if len(merged) != 1 {
		t.Fatalf("expected 1 merged chapter, got %d: %#v", len(merged), merged)
	}
	m := merged[0]
	if m["id"] != "c-merged" || m["mergeCommitSha"] != "sha-merge" {
		t.Fatalf("merged chapter shape wrong: %#v", m)
	}
	// Merged chapters MUST force narrator fields to null even though c-merged has
	// narratorId/narratorStatus populated (matches TS).
	if _, ok := m["narratorId"]; !ok || m["narratorId"] != nil {
		t.Fatalf("merged chapter narratorId must be nil, got %#v", m["narratorId"])
	}
	if _, ok := m["narratorStatus"]; !ok || m["narratorStatus"] != nil {
		t.Fatalf("merged chapter narratorStatus must be nil, got %#v", m["narratorStatus"])
	}
}

// TestRulerChapterSummariesEmpty verifies empty input yields non-nil empty
// slices (so the JSON response serializes [] rather than null).
func TestRulerChapterSummariesEmpty(t *testing.T) {
	active, merged := rulerChapterSummaries(nil)
	if active == nil || merged == nil {
		t.Fatalf("expected non-nil slices, got active=%#v merged=%#v", active, merged)
	}
	if len(active) != 0 || len(merged) != 0 {
		t.Fatalf("expected empty slices, got active=%d merged=%d", len(active), len(merged))
	}
}
