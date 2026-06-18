package misccompat

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestReviewConclusionRoutesByReviewAndBySource covers the two conclusion HTTP
// routes through the router: GET /{id}/conclusion (keyed on review_chapter_id)
// and GET /by-source/{sourceChapterId}/conclusion (keyed on source_chapter_id),
// including the {"conclusion": ...} envelope and the null-when-absent case.
// Existing coverage calls latestReviewConclusion directly, not the routes.
func TestReviewConclusionRoutesByReviewAndBySource(t *testing.T) {
	sqlDB := openReviewCompatDB(t)
	defer sqlDB.Close()
	if _, err := sqlDB.Exec(`INSERT INTO review_conclusions (id, review_chapter_id, source_chapter_id, verdict, findings_json, created_at) VALUES ('rc1', 'review-1', 'source-1', 'approve', '[{"severity":"minor","message":"ok"}]', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert conclusion: %v", err)
	}

	router := chi.NewRouter()
	(&Handler{DB: sqlDB}).RegisterReviews(router)

	conclusionFrom := func(path string) map[string]any {
		t.Helper()
		w := serveReviewCompat(router, http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", path, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v raw=%s", path, err, w.Body.String())
		}
		if _, ok := body["conclusion"]; !ok {
			t.Fatalf("response for %s missing conclusion envelope: %#v", path, body)
		}
		return body
	}

	// By review chapter id: resolves the seeded conclusion.
	byReview := conclusionFrom("/review-1/conclusion")
	rc, ok := byReview["conclusion"].(map[string]any)
	if !ok {
		t.Fatalf("by-review conclusion should be an object, got %#v", byReview["conclusion"])
	}
	if rc["verdict"] != "approve" || rc["reviewChapterId"] != "review-1" || rc["findingsJson"] == nil {
		t.Fatalf("unexpected by-review conclusion: %#v", rc)
	}

	// By source chapter id: same row, resolved through the source_chapter_id column.
	bySource := conclusionFrom("/by-source/source-1/conclusion")
	src, ok := bySource["conclusion"].(map[string]any)
	if !ok {
		t.Fatalf("by-source conclusion should be an object, got %#v", bySource["conclusion"])
	}
	if src["verdict"] != "approve" || src["sourceChapterId"] != "source-1" {
		t.Fatalf("unexpected by-source conclusion: %#v", src)
	}

	// Unknown ids resolve to a null conclusion (not an error).
	missingReview := conclusionFrom("/does-not-exist/conclusion")
	if missingReview["conclusion"] != nil {
		t.Fatalf("expected null conclusion for unknown review id, got %#v", missingReview["conclusion"])
	}
	missingSource := conclusionFrom("/by-source/does-not-exist/conclusion")
	if missingSource["conclusion"] != nil {
		t.Fatalf("expected null conclusion for unknown source id, got %#v", missingSource["conclusion"])
	}
}
