package systemroutes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"narrafork-go/internal/httpx"
)

// TestValidateBenchmarkSuiteBodyMatchesTSSchema locks the create-suite input
// validation against the TypeScript createSuiteSchema + benchmarkTaskSchema
// (server/routes/benchmarks.ts): suite name 1-120, version/description bounds,
// per-task id (1-120 + charset + unique), name 1-300, prompt 1-200000, language
// 1-50, expectedOutput/testCode max bounds, rubric items, timeoutMs int positive.
// Go previously validated almost none of these (only tasks-present + prompt), so
// these cases guard against regressing to the looser behavior.
func TestValidateBenchmarkSuiteBodyMatchesTSSchema(t *testing.T) {
	validTask := func() map[string]any {
		return map[string]any{"id": "t1", "name": "Task One", "prompt": "do the thing"}
	}
	suite := func(tasks ...map[string]any) map[string]any {
		items := make([]any, len(tasks))
		for i, t := range tasks {
			items[i] = t
		}
		return map[string]any{"name": "Suite", "tasks": items}
	}

	// Happy path.
	if err := validateBenchmarkSuiteBody(suite(validTask())); err != nil {
		t.Fatalf("valid suite should pass, got %v", err)
	}
	// Happy path with valid metadata object.
	if err := validateBenchmarkSuiteBody(suite(map[string]any{"id": "t1", "name": "n", "prompt": "p", "metadata": map[string]any{"k": "v", "n": float64(1)}})); err != nil {
		t.Fatalf("valid suite with metadata should pass, got %v", err)
	}

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"suite name missing", map[string]any{"tasks": []any{validTask()}}, "suite name must be 1-120 characters"},
		{"suite name too long", func() map[string]any { b := suite(validTask()); b["name"] = strings.Repeat("x", 121); return b }(), "suite name must be 1-120 characters"},
		{"version too long", func() map[string]any { b := suite(validTask()); b["version"] = strings.Repeat("v", 121); return b }(), "version must be 1-120 characters"},
		{"description too long", func() map[string]any { b := suite(validTask()); b["description"] = strings.Repeat("d", 2001); return b }(), "description must be <= 2000 characters"},
		{"no tasks", map[string]any{"name": "Suite", "tasks": []any{}}, "tasks are required"},
		{"task missing id", suite(map[string]any{"name": "n", "prompt": "p"}), "id must be 1-120 characters"},
		{"task bad id charset", suite(map[string]any{"id": "bad id!", "name": "n", "prompt": "p"}), "may only contain letters"},
		{"duplicate task id", suite(validTask(), map[string]any{"id": "t1", "name": "n2", "prompt": "p2"}), "Duplicate task ID: t1"},
		{"task missing name", suite(map[string]any{"id": "t1", "prompt": "p"}), "name must be 1-300 characters"},
		{"task name too long", suite(map[string]any{"id": "t1", "name": strings.Repeat("n", 301), "prompt": "p"}), "name must be 1-300 characters"},
		{"task missing prompt", suite(map[string]any{"id": "t1", "name": "n"}), "prompt is required"},
		{"task language too long", suite(map[string]any{"id": "t1", "name": "n", "prompt": "p", "language": strings.Repeat("l", 51)}), "language must be 1-50 characters"},
		{"rubric maxPoints out of range", suite(map[string]any{"id": "t1", "name": "n", "prompt": "p", "rubric": []any{map[string]any{"name": "c", "description": "d", "maxPoints": float64(1001)}}}), "maxPoints must be a positive number <= 1000"},
		{"timeoutMs not positive int", suite(map[string]any{"id": "t1", "name": "n", "prompt": "p", "timeoutMs": float64(-5)}), "timeoutMs must be a positive integer"},
		{"metadata not object", suite(map[string]any{"id": "t1", "name": "n", "prompt": "p", "metadata": "nope"}), "metadata must be an object"},
		{"metadata key too long", suite(map[string]any{"id": "t1", "name": "n", "prompt": "p", "metadata": map[string]any{strings.Repeat("k", 101): "v"}}), "metadata keys must be <= 100 characters"},
		{"metadata json too large", suite(map[string]any{"id": "t1", "name": "n", "prompt": "p", "metadata": map[string]any{"big": strings.Repeat("x", 200_001)}}), "metadata must be <= 200000 JSON characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBenchmarkSuiteBody(tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// TestCreateBenchmarkRunValidatesCreateRunSchema locks POST /benchmarks/runs
// input validation against the TS createRunSchema (suiteId 1-120, name 1-120,
// model 1-200, systemPrompt max100000, permissionMode strict 5-enum). These run
// BEFORE the suite DB lookup, so invalid input must 400 (not fall through to a
// 404). Go previously read these via stringMap defaults with no bounds/enum.
func TestCreateBenchmarkRunValidatesCreateRunSchema(t *testing.T) {
	sqlDB := newBenchmarkTestDB(t)
	h := &Handler{DB: sqlDB}

	post := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/benchmarks/runs", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if err := h.createBenchmarkRun(w, req); err != nil {
			httpx.WriteError(w, err)
		}
		return w
	}

	cases := []struct{ name, body, want string }{
		{"empty suiteId", `{"name":"Run","model":"m"}`, "suiteId must be 1-120 characters"},
		{"missing name", `{"suiteId":"s1","model":"m"}`, "name must be 1-120 characters"},
		{"missing model", `{"suiteId":"s1","name":"Run"}`, "model must be 1-200 characters"},
		{"over-long model", `{"suiteId":"s1","name":"Run","model":"` + strings.Repeat("m", 201) + `"}`, "model must be 1-200 characters"},
		{"over-long systemPrompt", `{"suiteId":"s1","name":"Run","model":"m","systemPrompt":"` + strings.Repeat("p", 100001) + `"}`, "systemPrompt must be"},
		{"invalid permissionMode", `{"suiteId":"s1","name":"Run","model":"m","permissionMode":"bogus"}`, "permissionMode must be one of"},
		{"config not object", `{"suiteId":"s1","name":"Run","model":"m","config":"nope"}`, "config must be an object"},
		{"config key too long", `{"suiteId":"s1","name":"Run","model":"m","config":{"` + strings.Repeat("k", 101) + `":1}}`, "config keys must be"},
		{"config json too large", `{"suiteId":"s1","name":"Run","model":"m","config":{"big":"` + strings.Repeat("x", 200001) + `"}}`, "config must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := post(t, tc.body)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("expected 400 %q, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}
