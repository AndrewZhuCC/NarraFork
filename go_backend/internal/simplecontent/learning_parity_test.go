package simplecontent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func serveLearning(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/learning", NewHandler().RegisterLearning)
	req := httptest.NewRequest(method, target, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLearningDocCatalogCountMatchesHealthContract(t *testing.T) {
	// The health capability (router.go content.learning.docCount) declares a
	// literal 14; keep this guard so the catalog and the contract cannot drift.
	if len(learningDocs) != 14 {
		t.Fatalf("learning doc catalog count = %d, update content.learning.docCount in router.go to match", len(learningDocs))
	}
}

func TestLearningIndexReturnsCategoriesAndDocSummaries(t *testing.T) {
	w := serveLearning(t, http.MethodGet, "/learning/")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Categories []map[string]any `json:"categories"`
		Docs       []map[string]any `json:"docs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode index: %v", err)
	}
	if len(body.Categories) != 5 {
		t.Fatalf("expected 5 categories, got %d", len(body.Categories))
	}
	if len(body.Docs) != len(learningDocs) {
		t.Fatalf("expected %d doc summaries, got %d", len(learningDocs), len(body.Docs))
	}
	first := body.Docs[0]
	for _, key := range []string{"id", "category", "tags", "title", "summary", "actions"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("doc summary missing %q field: %#v", key, first)
		}
	}
}

func TestLearningIndexHonorsLocaleQuery(t *testing.T) {
	en := serveLearning(t, http.MethodGet, "/learning/")
	zh := serveLearning(t, http.MethodGet, "/learning/?lang=zh-CN")
	if en.Code != http.StatusOK || zh.Code != http.StatusOK {
		t.Fatalf("expected 200/200, got %d/%d", en.Code, zh.Code)
	}
	var enBody, zhBody struct {
		Categories []map[string]any `json:"categories"`
	}
	_ = json.Unmarshal(en.Body.Bytes(), &enBody)
	_ = json.Unmarshal(zh.Body.Bytes(), &zhBody)
	if enBody.Categories[0]["label"] == zhBody.Categories[0]["label"] {
		t.Fatalf("locale should change category labels, both = %v", enBody.Categories[0]["label"])
	}
	if zhBody.Categories[0]["label"] != "从这里开始" {
		t.Fatalf("zh-CN locale should localize labels, got %v", zhBody.Categories[0]["label"])
	}
}

func TestLearningSearchFiltersByQueryAndReturnsAllWhenEmpty(t *testing.T) {
	all := serveLearning(t, http.MethodGet, "/learning/search")
	var allBody struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(all.Body.Bytes(), &allBody); err != nil {
		t.Fatalf("decode all search: %v", err)
	}
	if len(allBody.Results) != len(learningDocs) {
		t.Fatalf("empty query should return all docs, got %d", len(allBody.Results))
	}

	filtered := serveLearning(t, http.MethodGet, "/learning/search?q=subagent")
	var filteredBody struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(filtered.Body.Bytes(), &filteredBody); err != nil {
		t.Fatalf("decode filtered search: %v", err)
	}
	if len(filteredBody.Results) == 0 || len(filteredBody.Results) >= len(learningDocs) {
		t.Fatalf("query should narrow results, got %d of %d", len(filteredBody.Results), len(learningDocs))
	}
	foundSubagentDoc := false
	for _, doc := range filteredBody.Results {
		if doc["id"] == "subagents-and-plan-mode" {
			foundSubagentDoc = true
		}
	}
	if !foundSubagentDoc {
		t.Fatalf("subagent query should match subagents doc, got %#v", filteredBody.Results)
	}
}

func TestLearningDocReturnsDetailSectionsAndMissingIs404(t *testing.T) {
	w := serveLearning(t, http.MethodGet, "/learning/overview")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode doc: %v", err)
	}
	for _, key := range []string{"id", "title", "summary", "sections", "workflow", "bestPractices", "pitfalls", "agentHints", "actions"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("doc detail missing %q field: %#v", key, doc)
		}
	}
	if doc["id"] != "overview" {
		t.Fatalf("unexpected doc id: %v", doc["id"])
	}

	missing := serveLearning(t, http.MethodGet, "/learning/does-not-exist")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing doc should return 404, got %d body=%s", missing.Code, missing.Body.String())
	}
}

// TestLearningGuideOutputRendersActionsAndEmptySearchListsAll pins three TS
// parity points in LearningGuideOutput (mirroring
// server/lib/agent/tools/learning-guide.ts): (1) the summary list appends a
// "Shortcuts:"/"入口:" line with "label → href" actions; (2) the detail view
// appends a "## Feature shortcuts"/"## 可跳转功能入口" section plus a localized
// "Document ID"/"文档 ID" footer; (3) an empty search query returns the FULL
// summary list (not a help message). Go previously omitted actions in both
// renders, used an English-only Document ID, and returned a help string for an
// empty query.
func TestLearningGuideOutputRendersActionsAndEmptySearchListsAll(t *testing.T) {
	// list mode: summary shortcuts present.
	list := LearningGuideOutput("list", "", "", "en")
	if !strings.Contains(list, "Shortcuts: ") || !strings.Contains(list, " → /settings") {
		t.Fatalf("list output should include action shortcuts, got:\n%s", list)
	}

	// get mode: detail actions section + Document ID footer.
	detail := LearningGuideOutput("get", "", "overview", "en")
	if !strings.Contains(detail, "## Feature shortcuts") || !strings.Contains(detail, "(/settings)") {
		t.Fatalf("detail output should include the Feature shortcuts section, got:\n%s", detail)
	}
	if !strings.Contains(detail, "Document ID: overview") {
		t.Fatalf("detail output should end with the Document ID footer, got:\n%s", detail)
	}

	// get mode zh: localized footer + section title.
	detailZh := LearningGuideOutput("get", "", "overview", "zh-CN")
	if !strings.Contains(detailZh, "## 可跳转功能入口") || !strings.Contains(detailZh, "文档 ID: overview") {
		t.Fatalf("zh detail should localize shortcuts section and Document ID, got:\n%s", detailZh)
	}

	// search with empty query returns the full list (same as list mode), not a help message.
	emptySearch := LearningGuideOutput("search", "", "", "en")
	if strings.Contains(emptySearch, "Please provide a search query") {
		t.Fatalf("empty search should return the full list, not a help message, got:\n%s", emptySearch)
	}
	if emptySearch != list {
		t.Fatalf("empty search should equal the full list output")
	}
}
