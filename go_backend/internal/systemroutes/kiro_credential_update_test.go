package systemroutes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/httpx"
)

// TestKiroCredentialUpdateValidatesZodBounds covers PATCH /api/kiro/credentials/{id}
// field bounds against the TypeScript updateCredentialSchema (server/routes/kiro.ts):
// email/displayName max 200, region max 50. These bounds are validated after the
// JSON decode and BEFORE any credential-file access, so an over-long or non-string
// value must return a 400 regardless of whether the credential exists.
func TestKiroCredentialUpdateValidatesZodBounds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := &Handler{}
	handler := httpx.Handler(h.kiroCredentialUpdate)

	patch := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/kiro/credentials/cred-1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", "cred-1")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	cases := []struct{ name, body, want string }{
		{"over-long email", `{"email":"` + strings.Repeat("x", 201) + `"}`, "email must be at most 200 characters"},
		{"over-long displayName", `{"displayName":"` + strings.Repeat("y", 201) + `"}`, "displayName must be at most 200 characters"},
		{"over-long region", `{"region":"` + strings.Repeat("z", 51) + `"}`, "region must be at most 50 characters"},
		{"non-string email", `{"email":123}`, "email must be a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := patch(t, tc.body)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("expected 400 %q, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}
