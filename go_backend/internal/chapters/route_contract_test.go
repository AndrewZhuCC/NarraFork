package chapters

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSplitRouteDisabledBeforeMutation(t *testing.T) {
	h := NewHandler(&Service{})
	w := serveChapterRouteContractRequest(h, http.MethodPost, "/chapters/source/split", `{"commitSha":"abc123","newFork":{"title":"Split"}}`)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 disabled split, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode split body: %v", err)
	}
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != "chapter.split" || body["supported"] != false {
		t.Fatalf("unexpected split disabled body: %#v", body)
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
