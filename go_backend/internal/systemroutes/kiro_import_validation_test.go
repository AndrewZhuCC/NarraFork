package systemroutes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"narrafork-go/internal/httpx"
)

// TestKiroImportCredentialsValidatesArrayAndObjectShape locks the
// /api/kiro/credentials/import validation against the TS route (server/routes/kiro.ts):
// body.credentials must be an array (else 400 "credentials must be an array"), and
// every entry must be a plain object (else 400 "each credential must be a plain
// object"). These run before any filesystem work. Go previously treated a
// missing/non-array credentials as an empty import (200) and silently skipped
// non-object entries.
func TestKiroImportCredentialsValidatesArrayAndObjectShape(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := &Handler{}
	post := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/kiro/credentials/import", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if err := h.kiroImportCredentials(w, req); err != nil {
			httpx.WriteError(w, err)
		}
		return w
	}

	cases := []struct{ name, body, want string }{
		{"missing credentials key", `{}`, "credentials must be an array"},
		{"credentials not array", `{"credentials":"foo"}`, "credentials must be an array"},
		{"bare array body", `[{"email":"a"}]`, "credentials must be an array"},
		{"non-object entry (number)", `{"credentials":[123]}`, "each credential must be a plain object"},
		{"non-object entry (string)", `{"credentials":["x"]}`, "each credential must be a plain object"},
		{"non-object entry (nested array)", `{"credentials":[[]]}`, "each credential must be a plain object"},
		{"null entry", `{"credentials":[null]}`, "each credential must be a plain object"},
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
