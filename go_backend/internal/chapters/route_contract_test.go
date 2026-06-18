package chapters

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSplitRouteValidatesInputBeforeMutation(t *testing.T) {
	h := NewHandler(&Service{})
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "commitSha required",
			body: `{"newFork":{"title":"Fork"}}`,
			want: "commitSha is required",
		},
		{
			name: "title required",
			body: `{"commitSha":"abc123","newFork":{}}`,
			want: "newFork.title is required",
		},
		{
			name: "title max",
			body: `{"commitSha":"abc123","newFork":{"title":"` + strings.Repeat("x", 201) + `"}}`,
			want: "newFork.title must be at most 200 characters",
		},
		{
			name: "description max",
			body: `{"commitSha":"abc123","newFork":{"title":"Fork","description":"` + strings.Repeat("x", 2001) + `"}}`,
			want: "newFork.description must be at most 2000 characters",
		},
		{
			name: "inheritMode enum",
			body: `{"commitSha":"abc123","newFork":{"title":"Fork","inheritMode":"summary"}}`,
			want: "newFork.inheritMode must be full, compressed, or fresh",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serveChapterRouteContractRequest(h, http.MethodPost, "/chapters/source/split", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 split validation, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode split body: %v", err)
			}
			if body["code"] != "BAD_REQUEST" || body["error"] != tc.want {
				t.Fatalf("unexpected split validation body: %#v", body)
			}
		})
	}
}

// TestSplitDefaultsOmittedInheritModeToFull is a regression test for a Zod
// .default() divergence: TS splitChapterSchema newFork.inheritMode is
// z.enum([...]).default("full"), so an omitted value defaults to "full" rather
// than being rejected. Go previously returned 400 for the omitted field.
func TestSplitDefaultsOmittedInheritModeToFull(t *testing.T) {
	// Nil DB/Git: a request that passes validation reaches SplitAtCommit and is
	// rejected with the services-required error, NOT the inheritMode enum error.
	h := NewHandler(&Service{})
	w := serveChapterRouteContractRequest(h, http.MethodPost, "/chapters/source/split",
		`{"commitSha":"abc123","newFork":{"title":"Fork"}}`)
	if strings.Contains(w.Body.String(), "inheritMode must be full") {
		t.Fatalf("omitted inheritMode should default to full, not be rejected: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Chapter split requires database and git services") {
		t.Fatalf("expected to reach SplitAtCommit services guard, got %s", w.Body.String())
	}
}

func TestPodmanInstallRequiresAdminBeforePackageManagerProbe(t *testing.T) {
	h := NewHandler(&Service{})
	w := serveChapterRouteContractRequest(h, http.MethodPost, "/chapters/podman/install", `{}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 admin boundary, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["code"] != "FORBIDDEN" || body["error"] != "Admin access required" {
		t.Fatalf("unexpected forbidden body: %#v", body)
	}
}

func TestChapterCriticalRoutesExposeValidationContracts(t *testing.T) {
	h := NewHandler(&Service{})
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   string
	}{
		{
			name:   "fork malformed body",
			method: http.MethodPost,
			path:   "/chapters/source/fork",
			body:   `{`,
			want:   "invalid JSON",
		},
		{
			name:   "review malformed body",
			method: http.MethodPost,
			path:   "/chapters/source/review",
			body:   `{`,
			want:   "invalid JSON",
		},
		{
			name:   "merge-check requires target",
			method: http.MethodGet,
			path:   "/chapters/source/merge-check",
			want:   "targetChapterId is required",
		},
		{
			name:   "merge validates strategy before touching git",
			method: http.MethodPost,
			path:   "/chapters/source/merge",
			body:   `{"targetChapterId":"target","strategy":"rebase"}`,
			want:   "strategy must be merge, squash, or cherry-pick",
		},
		{
			name:   "ai-resolve validates strategy before touching git",
			method: http.MethodPost,
			path:   "/chapters/source/ai-resolve",
			body:   `{"targetChapterId":"target","strategy":"rebase"}`,
			want:   "strategy must be merge, squash, or cherry-pick",
		},
		{
			name:   "ai-resolve rejects over-long message (same mergeChapterSchema as /merge)",
			method: http.MethodPost,
			path:   "/chapters/source/ai-resolve",
			body:   `{"targetChapterId":"target","message":"` + strings.Repeat("x", 501) + `"}`,
			want:   "message must be at most 500 characters",
		},
		{
			name:   "batch-merge requires base chapter",
			method: http.MethodPost,
			path:   "/chapters/batch-merge",
			body:   `{"sourceChapterIds":["source"]}`,
			want:   "baseChapterId is required",
		},
		{
			name:   "batch-merge requires source list",
			method: http.MethodPost,
			path:   "/chapters/batch-merge",
			body:   `{"baseChapterId":"base","sourceChapterIds":[]}`,
			want:   "sourceChapterIds is required",
		},
		{
			name:   "batch-merge requires title when no targetChapterId",
			method: http.MethodPost,
			path:   "/chapters/batch-merge",
			body:   `{"baseChapterId":"base","sourceChapterIds":["source"]}`,
			want:   "title is required when not merging into an existing chapter",
		},
		{
			name:   "batch-merge rejects overlong title",
			method: http.MethodPost,
			path:   "/chapters/batch-merge",
			body:   `{"baseChapterId":"base","sourceChapterIds":["source"],"title":"` + strings.Repeat("x", 201) + `"}`,
			want:   "title must be at most 200 characters",
		},
		{
			name:   "batch-merge rejects overlong description",
			method: http.MethodPost,
			path:   "/chapters/batch-merge",
			body:   `{"baseChapterId":"base","sourceChapterIds":["source"],"targetChapterId":"target","description":"` + strings.Repeat("d", 2001) + `"}`,
			want:   "description must be at most 2000 characters",
		},
		{
			name:   "batch-merge rejects invalid strategy",
			method: http.MethodPost,
			path:   "/chapters/batch-merge",
			body:   `{"baseChapterId":"base","sourceChapterIds":["source"],"targetChapterId":"target","strategy":"rebase"}`,
			want:   "strategy must be merge, squash, or cherry-pick",
		},
		{
			name:   "cleanup requires non-empty chapterIds",
			method: http.MethodPost,
			path:   "/chapters/cleanup",
			body:   `{}`,
			want:   "chapterIds must contain at least one item",
		},
		{
			name:   "cleanup rejects empty chapterIds array",
			method: http.MethodPost,
			path:   "/chapters/cleanup",
			body:   `{"chapterIds":[]}`,
			want:   "chapterIds must contain at least one item",
		},
		{
			name:   "cleanup rejects empty-string chapterId entry",
			method: http.MethodPost,
			path:   "/chapters/cleanup",
			body:   `{"chapterIds":[""]}`,
			want:   "chapterIds entries must be non-empty strings",
		},
		{
			name:   "create requires projectId",
			method: http.MethodPost,
			path:   "/chapters",
			body:   `{"title":"Chapter"}`,
			want:   "projectId is required",
		},
		{
			name:   "create requires title",
			method: http.MethodPost,
			path:   "/chapters",
			body:   `{"projectId":"p1","title":"  "}`,
			want:   "title is required",
		},
		{
			name:   "create rejects overlong title",
			method: http.MethodPost,
			path:   "/chapters",
			body:   `{"projectId":"p1","title":"` + strings.Repeat("x", 201) + `"}`,
			want:   "title must be at most 200 characters",
		},
		{
			name:   "create rejects overlong description",
			method: http.MethodPost,
			path:   "/chapters",
			body:   `{"projectId":"p1","title":"Chapter","description":"` + strings.Repeat("d", 2001) + `"}`,
			want:   "description must be at most 2000 characters",
		},
		{
			name:   "fork rejects invalid role",
			method: http.MethodPost,
			path:   "/chapters/source/fork",
			body:   `{"role":"trunk"}`,
			want:   "role must be branch or exploration",
		},
		{
			name:   "fork rejects invalid inheritMode",
			method: http.MethodPost,
			path:   "/chapters/source/fork",
			body:   `{"inheritMode":"partial"}`,
			want:   "inheritMode must be full, compressed, or fresh",
		},
		{
			name:   "fork rejects overlong title",
			method: http.MethodPost,
			path:   "/chapters/source/fork",
			body:   `{"title":"` + strings.Repeat("x", 201) + `"}`,
			want:   "title must be at most 200 characters",
		},
		{
			name:   "fork rejects negative crossOffset",
			method: http.MethodPost,
			path:   "/chapters/source/fork",
			body:   `{"crossOffset":-1}`,
			want:   "crossOffset must be greater than or equal to 0",
		},
		{
			name:   "merge rejects overlong message",
			method: http.MethodPost,
			path:   "/chapters/source/merge",
			body:   `{"targetChapterId":"target","message":"` + strings.Repeat("m", 501) + `"}`,
			want:   "message must be at most 500 characters",
		},
		{
			name:   "review rejects invalid locale",
			method: http.MethodPost,
			path:   "/chapters/source/review",
			body:   `{"locale":"fr"}`,
			want:   "locale must be en or zh-CN",
		},
		{
			name:   "review rejects overlong title",
			method: http.MethodPost,
			path:   "/chapters/source/review",
			body:   `{"title":"` + strings.Repeat("x", 201) + `"}`,
			want:   "title must be at most 200 characters",
		},
		{
			name:   "review rejects negative crossOffset",
			method: http.MethodPost,
			path:   "/chapters/source/review",
			body:   `{"crossOffset":-1}`,
			want:   "crossOffset must be greater than or equal to 0",
		},
		{
			name:   "batch-fork requires at least one fork",
			method: http.MethodPost,
			path:   "/chapters/source/batch-fork",
			body:   `{"forks":[]}`,
			want:   "forks is required",
		},
		{
			name:   "batch-fork rejects more than 10 forks",
			method: http.MethodPost,
			path:   "/chapters/source/batch-fork",
			body:   `{"forks":[{"title":"a"},{"title":"b"},{"title":"c"},{"title":"d"},{"title":"e"},{"title":"f"},{"title":"g"},{"title":"h"},{"title":"i"},{"title":"j"},{"title":"k"}]}`,
			want:   "forks must contain at most 10 items",
		},
		{
			name:   "batch-fork rejects empty title item (whole-request 400, not per-item failed[])",
			method: http.MethodPost,
			path:   "/chapters/source/batch-fork",
			body:   `{"forks":[{"title":"ok"},{"title":"  "}]}`,
			want:   "forks[1].title is required",
		},
		{
			name:   "batch-fork rejects over-long title",
			method: http.MethodPost,
			path:   "/chapters/source/batch-fork",
			body:   `{"forks":[{"title":"` + strings.Repeat("x", 201) + `"}]}`,
			want:   "forks[0].title must be at most 200 characters",
		},
		{
			name:   "batch-fork rejects over-long description",
			method: http.MethodPost,
			path:   "/chapters/source/batch-fork",
			body:   `{"forks":[{"title":"ok","description":"` + strings.Repeat("y", 2001) + `"}]}`,
			want:   "forks[0].description must be at most 2000 characters",
		},
		{
			name:   "batch-fork rejects invalid inheritMode",
			method: http.MethodPost,
			path:   "/chapters/source/batch-fork",
			body:   `{"forks":[{"title":"ok","inheritMode":"bogus"}]}`,
			want:   "forks[0].inheritMode must be full, compressed, or fresh",
		},
		{
			name:   "batch-fork rejects invalid role (TS enum [branch, exploration], not silent coerce)",
			method: http.MethodPost,
			path:   "/chapters/source/batch-fork",
			body:   `{"forks":[{"title":"ok","role":"trunk"}]}`,
			want:   "forks[0].role must be branch or exploration",
		},
		{
			name:   "cherry-pick requires sourceChapterId",
			method: http.MethodPost,
			path:   "/chapters/target/cherry-pick",
			body:   `{"commitShas":["abc"]}`,
			want:   "sourceChapterId is required",
		},
		{
			name:   "cherry-pick requires commitShas",
			method: http.MethodPost,
			path:   "/chapters/target/cherry-pick",
			body:   `{"sourceChapterId":"source","commitShas":[]}`,
			want:   "commitShas is required",
		},
		{
			name:   "cherry-pick rejects empty commit sha",
			method: http.MethodPost,
			path:   "/chapters/target/cherry-pick",
			body:   `{"sourceChapterId":"source","commitShas":["  "]}`,
			want:   "commitShas entries must be non-empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serveChapterRouteContractRequest(h, tc.method, tc.path, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body["code"] != "BAD_REQUEST" || !bytes.Contains(w.Body.Bytes(), []byte(tc.want)) {
				t.Fatalf("expected BAD_REQUEST containing %q, got %#v", tc.want, body)
			}
		})
	}
}

func serveChapterRouteContractRequest(h *Handler, method, path, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/chapters", h.RegisterRoutes)
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
