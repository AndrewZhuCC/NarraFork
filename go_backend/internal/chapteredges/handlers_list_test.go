package chapteredges

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestListChapterEdgesFiltersByProjectChapterAndType covers the list success
// path with real rows: project filter, chapter (source-or-target) filter, type
// filter, and the missing-filter guard. Existing list coverage only asserts the
// invalid-type 400, so the filtering query logic was previously unverified.
func TestListChapterEdgesFiltersByProjectChapterAndType(t *testing.T) {
	sqlDB := openChapterEdgeTestDB(t)
	defer sqlDB.Close()

	// Seed edges across two projects with different types and endpoints.
	seed := []string{
		`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('e-fork', 'project-1', 'chapter-a', 'chapter-b', 'fork', '{}', '2026-05-14T00:00:01Z')`,
		`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('e-dep', 'project-1', 'chapter-b', 'chapter-a', 'dependency', '{}', '2026-05-14T00:00:02Z')`,
		`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('e-merge', 'project-1', 'chapter-a', 'chapter-other', 'merge', '{}', '2026-05-14T00:00:03Z')`,
		`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('e-p2', 'project-2', 'chapter-other', 'chapter-a', 'fork', '{}', '2026-05-14T00:00:04Z')`,
	}
	for _, stmt := range seed {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("seed edge: %v", err)
		}
	}

	router := chapterEdgeTestRouter(sqlDB)

	listIDs := func(path string) []string {
		t.Helper()
		w := performChapterEdgeRequest(router, http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d: %s", path, w.Code, w.Body.String())
		}
		var edges []Edge
		if err := json.Unmarshal(w.Body.Bytes(), &edges); err != nil {
			t.Fatalf("decode edges for %s: %v raw=%s", path, err, w.Body.String())
		}
		ids := make([]string, 0, len(edges))
		for _, e := range edges {
			ids = append(ids, e.ID)
		}
		return ids
	}

	// projectId filter returns all three project-1 edges (not the project-2 one).
	if got := listIDs("/?projectId=project-1"); !sameSet(got, []string{"e-fork", "e-dep", "e-merge"}) {
		t.Fatalf("projectId filter returned %#v", got)
	}

	// type filter narrows within project.
	if got := listIDs("/?projectId=project-1&type=dependency"); !sameSet(got, []string{"e-dep"}) {
		t.Fatalf("type filter returned %#v", got)
	}

	// chapterId filter matches edges where the chapter is source OR target.
	// chapter-b is source of e-dep and target of e-fork (both project-1).
	if got := listIDs("/?chapterId=chapter-b"); !sameSet(got, []string{"e-fork", "e-dep"}) {
		t.Fatalf("chapterId source-or-target filter returned %#v", got)
	}

	// chapter-other is target of e-merge (project-1) and source of e-p2 (project-2).
	if got := listIDs("/?chapterId=chapter-other"); !sameSet(got, []string{"e-merge", "e-p2"}) {
		t.Fatalf("chapterId cross-project filter returned %#v", got)
	}

	// When chapterId is present, the TypeScript route returns ALL edges touching
	// that chapter and IGNORES projectId entirely (chapterId branch wins). So
	// projectId+chapterId returns the same cross-project set as chapterId alone.
	if got := listIDs("/?projectId=project-1&chapterId=chapter-other"); !sameSet(got, []string{"e-merge", "e-p2"}) {
		t.Fatalf("projectId+chapterId filter returned %#v", got)
	}

	// When chapterId is present, type is also ignored (chapterId branch does not
	// filter by type), matching the TypeScript getEdgesByChapter behavior.
	if got := listIDs("/?chapterId=chapter-other&type=fork"); !sameSet(got, []string{"e-merge", "e-p2"}) {
		t.Fatalf("chapterId+type filter should ignore type, returned %#v", got)
	}

	// Missing both filters returns a validation error.
	w := performChapterEdgeRequest(router, http.MethodGet, "/", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when no filter provided, got %d: %s", w.Code, w.Body.String())
	}
	decoded := decodeJSONBody(t, w)
	if decoded["code"] != "VALIDATION_ERROR" || decoded["error"] != "projectId or chapterId required" {
		t.Fatalf("unexpected missing-filter error: %#v", decoded)
	}
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			return false
		}
		seen[w]--
	}
	return true
}
