package systemroutes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAdminUpdateSettingsRequiresBooleanRegistrationOpen covers PATCH /admin/settings,
// which mirrors the TS adminUpdateSettingsSchema (z.object({registrationOpen:
// z.boolean()})): registrationOpen is REQUIRED and must be a boolean. A missing or
// non-boolean value must be a 400 VALIDATION_ERROR and must NOT mutate persisted
// settings. Go previously passed body["registrationOpen"] straight into
// Settings.Patch, so a missing field clobbered the persisted boolean to null and a
// non-boolean wrote garbage.
func TestAdminUpdateSettingsRequiresBooleanRegistrationOpen(t *testing.T) {
	// Valid boolean → 200 and the response echoes the new value.
	t.Run("boolean accepted", func(t *testing.T) {
		router, token, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{
			{"admin-1", "root", "admin"},
		}, "admin-1")
		w := doAdminSettingsPatchWithRouter(t, router, token, `{"registrationOpen":false}`)
		if w.Code != http.StatusOK {
			t.Fatalf("boolean registrationOpen should be 200, got %d %s", w.Code, w.Body.String())
		}
		if body := w.Body.String(); !strings.Contains(body, `"registrationOpen":false`) {
			t.Fatalf("response should echo registrationOpen=false, got %s", body)
		}
	})

	// Missing field → 400 VALIDATION_ERROR (TS Zod required).
	t.Run("missing rejected", func(t *testing.T) {
		router, token, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{
			{"admin-1", "root", "admin"},
		}, "admin-1")
		w := doAdminSettingsPatchWithRouter(t, router, token, `{}`)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "registrationOpen must be a boolean") {
			t.Fatalf("missing registrationOpen should be 400, got %d %s", w.Code, w.Body.String())
		}
	})

	// Non-boolean (string) → 400 (TS Zod boolean type check).
	t.Run("non-boolean rejected", func(t *testing.T) {
		router, token, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{
			{"admin-1", "root", "admin"},
		}, "admin-1")
		w := doAdminSettingsPatchWithRouter(t, router, token, `{"registrationOpen":"true"}`)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "registrationOpen must be a boolean") {
			t.Fatalf("string registrationOpen should be 400, got %d %s", w.Code, w.Body.String())
		}
	})
}

func doAdminSettingsPatchWithRouter(t *testing.T, router http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/admin/settings", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
