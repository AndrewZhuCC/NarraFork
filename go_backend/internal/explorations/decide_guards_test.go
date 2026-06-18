package explorations

import (
	"net/http"
	"testing"
)

// TestDecideExplorationGroupRejectsChapterNotInGroup verifies the decide guard:
// a chapterId that exists but does not belong to the target exploration group is
// rejected with 400 before any status mutation, and the group stays active.
func TestDecideExplorationGroupRejectsChapterNotInGroup(t *testing.T) {
	router, database := newExplorationTestRouter(t)
	groupID, _ := createExplorationGroupFixture(t, router)

	// "root" is a real chapter but is not a branch of the group.
	w := postExplorationJSON(t, router, "/"+groupID+"/decide", map[string]any{"chapterId": "root"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("decide with foreign chapter should be 400, got %d body=%s", w.Code, w.Body.String())
	}
	var status string
	var decided *string
	if err := database.QueryRow(`SELECT status, decided_chapter_id FROM exploration_groups WHERE id = ?`, groupID).Scan(&status, &decided); err != nil {
		t.Fatalf("query group: %v", err)
	}
	if status != "active" {
		t.Fatalf("group status should stay active after rejected decide, got %q", status)
	}
	if decided != nil {
		t.Fatalf("decided_chapter_id should remain null after rejected decide, got %v", *decided)
	}
}

// TestDecideExplorationGroupRequiresChapterId verifies the decide guard rejects
// an empty/missing chapterId with 400 before mutation.
func TestDecideExplorationGroupRequiresChapterId(t *testing.T) {
	router, database := newExplorationTestRouter(t)
	groupID, _ := createExplorationGroupFixture(t, router)

	for _, body := range []map[string]any{{}, {"chapterId": "   "}} {
		w := postExplorationJSON(t, router, "/"+groupID+"/decide", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("decide without chapterId should be 400, got %d body=%s", w.Code, w.Body.String())
		}
	}
	var status string
	if err := database.QueryRow(`SELECT status FROM exploration_groups WHERE id = ?`, groupID).Scan(&status); err != nil {
		t.Fatalf("query group: %v", err)
	}
	if status != "active" {
		t.Fatalf("group status should stay active after rejected decide, got %q", status)
	}
}

// TestCreateExplorationGroupRejectsBaseChapterFromAnotherProject verifies the
// create guard: a baseChapterId that exists but belongs to a different project
// than the requested projectId is rejected with 400 and no group is created.
func TestCreateExplorationGroupRejectsBaseChapterFromAnotherProject(t *testing.T) {
	router, database := newExplorationTestRouter(t)

	w := postExplorationJSON(t, router, "/", map[string]any{
		"projectId":     "other-project",
		"title":         "Mismatch",
		"baseChapterId": "root", // root belongs to "project", not "other-project"
		"branches": []any{
			map[string]any{"title": "A", "inheritMode": "fresh"},
			map[string]any{"title": "B", "inheritMode": "fresh"},
		},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create with cross-project base chapter should be 400, got %d body=%s", w.Code, w.Body.String())
	}
	var groups int
	if err := database.QueryRow(`SELECT COUNT(*) FROM exploration_groups`).Scan(&groups); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if groups != 0 {
		t.Fatalf("no exploration group should be created on cross-project base, got %d", groups)
	}
}

// TestCreateExplorationGroupRejectsMissingBaseChapter verifies the create guard
// returns 404 when baseChapterId does not exist, and creates no group.
func TestCreateExplorationGroupRejectsMissingBaseChapter(t *testing.T) {
	router, database := newExplorationTestRouter(t)

	w := postExplorationJSON(t, router, "/", map[string]any{
		"projectId":     "project",
		"title":         "Missing Base",
		"baseChapterId": "does-not-exist",
		"branches": []any{
			map[string]any{"title": "A", "inheritMode": "fresh"},
			map[string]any{"title": "B", "inheritMode": "fresh"},
		},
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("create with missing base chapter should be 404, got %d body=%s", w.Code, w.Body.String())
	}
	var groups int
	if err := database.QueryRow(`SELECT COUNT(*) FROM exploration_groups`).Scan(&groups); err != nil {
		t.Fatalf("count groups: %v", err)
	}
	if groups != 0 {
		t.Fatalf("no exploration group should be created on missing base, got %d", groups)
	}
}
