package chapters

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	gitx "narrafork-go/internal/git"
)

// TestMergeConflictErrorTextMatchesTS pins the merge-failure error string to the
// TS template (server/routes/chapters.ts:163-165): with conflict files it reports
// `Merge conflicts in N file(s): f1, f2, ...` listing at most the first 5 (with a
// "…" suffix when more exist); without conflict files it is "Merge failed".
func TestMergeConflictErrorTextMatchesTS(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  string
	}{
		{"nil", nil, "Merge failed"},
		{"empty []string", []string{}, "Merge failed"},
		{"empty []any", []any{}, "Merge failed"},
		{"one file", []string{"a.txt"}, "Merge conflicts in 1 file(s): a.txt"},
		{"three files", []string{"a", "b", "c"}, "Merge conflicts in 3 file(s): a, b, c"},
		{"exactly five", []string{"a", "b", "c", "d", "e"}, "Merge conflicts in 5 file(s): a, b, c, d, e"},
		{"six files truncates to five with ellipsis", []string{"a", "b", "c", "d", "e", "f"}, "Merge conflicts in 6 file(s): a, b, c, d, e…"},
		// []any (defensive JSON-round-trip path) is coerced the same way.
		{"[]any three", []any{"x", "y", "z"}, "Merge conflicts in 3 file(s): x, y, z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeConflictErrorText(tc.input); got != tc.want {
				t.Fatalf("mergeConflictErrorText(%#v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestMergeRouteReturns409WithErrorTextOnConflict is the end-to-end proof that the
// POST /chapters/{id}/merge handler returns HTTP 409 (not 200) with a TS-shaped
// `error` field when the merge has conflicts, mirroring server/routes/chapters.ts:
// 159-168. Go previously always wrote 200, so the frontend mergeChapter mutation
// never entered its error branch and silently treated the conflicted merge as a
// success (closed the modal + refreshed the graph).
func TestMergeRouteReturns409WithErrorTextOnConflict(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, sourceWT, sourceHead, _ := setupMergeConflictRepo(t)
	database := openMergeParityDB(t)
	insertMergeParityRows(t, database, repo, sourceWT, sourceHead, false)

	handler := NewHandler(&Service{DB: database, Git: &gitx.Service{Timeout: 10 * time.Second}})
	// Merge source -> target: shared.txt diverges ("source" vs "target"), so a
	// non-fast-forward merge conflicts.
	w := serveChapterRouteContractRequest(handler, http.MethodPost, "/chapters/source/merge", `{"targetChapterId":"target","strategy":"merge","message":"merge source"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("conflicted merge should return 409, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode merge conflict body: %v body=%s", err, w.Body.String())
	}
	if success, _ := body["success"].(bool); success {
		t.Fatalf("conflicted merge body should have success=false, got %#v", body)
	}
	errText, _ := body["error"].(string)
	if !strings.HasPrefix(errText, "Merge conflicts in ") || !strings.Contains(errText, "shared.txt") {
		t.Fatalf("conflict error should report 'Merge conflicts in N file(s): ...shared.txt', got %q (full=%#v)", errText, body)
	}
}
