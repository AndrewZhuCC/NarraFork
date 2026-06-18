package chapters

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGitStatusRouteNoWorktreeReturnsBaseBranchZeroStats covers the
// GET /chapters/{id}/git-status route's dormant/no-worktree fallback: when the
// chapter has no worktree path the handler returns the baseBranch with zeroed
// commit/line stats and no error (instead of failing). This route was previously
// untested at the router level.
func TestGitStatusRouteNoWorktreeReturnsBaseBranchZeroStats(t *testing.T) {
	database := openContainerRuntimeDB(t)
	// Seed a chapter with a worktree first, then clear it to model a dormant chapter.
	insertContainerRuntimeProjectAndChapter(t, database, t.TempDir(), "{}", "")
	if _, err := database.Exec(`UPDATE chapters SET worktree_path = NULL, base_branch = 'main' WHERE id = 'c1'`); err != nil {
		t.Fatalf("clear worktree: %v", err)
	}

	router := newChapterRouter(NewHandler(&Service{DB: database}))

	req := httptest.NewRequest(http.MethodGet, "/c1/git-status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("git-status status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode git-status: %v raw=%s", err, w.Body.String())
	}
	if body["baseBranch"] != "main" {
		t.Fatalf("expected baseBranch main, got %#v", body["baseBranch"])
	}
	// Without a worktree, commit/line stats must be zero (JSON numbers decode to float64).
	for _, key := range []string{"commitsAhead", "linesAdded", "linesRemoved"} {
		if v, _ := body[key].(float64); v != 0 {
			t.Fatalf("expected %s=0 for dormant chapter, got %#v", key, body[key])
		}
	}
}
