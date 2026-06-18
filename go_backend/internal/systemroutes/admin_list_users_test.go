package systemroutes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAdminListUsersReturnsSafeFieldsWithoutPasswordHash covers GET /admin/users
// (admin panel user list): it returns id/username/role/avatar/createdAt for every
// user and must never leak password_hash. This list route previously had no test
// (only update/delete user routes were covered).
func TestAdminListUsersReturnsSafeFieldsWithoutPasswordHash(t *testing.T) {
	router, token, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{
		{"admin-1", "root", "admin"},
		{"user-1", "member", "user"},
	}, "admin-1")

	req := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list users status=%d body=%s", w.Code, w.Body.String())
	}

	// The raw body must not contain the password hash column under any key.
	if body := w.Body.String(); strings.Contains(body, "password") || strings.Contains(body, "hash") {
		t.Fatalf("list users response must not leak password material, got %s", body)
	}

	var users []map[string]any
	decodeJSONBody(t, w, &users)
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d (%#v)", len(users), users)
	}

	byName := map[string]map[string]any{}
	for _, u := range users {
		name, _ := u["username"].(string)
		byName[name] = u
		// Every entry must expose the safe contract fields and no password key.
		for _, field := range []string{"id", "username", "role", "createdAt"} {
			if _, ok := u[field]; !ok {
				t.Fatalf("user entry missing %q: %#v", field, u)
			}
		}
		if _, ok := u["passwordHash"]; ok {
			t.Fatalf("user entry leaked passwordHash: %#v", u)
		}
		if _, ok := u["password_hash"]; ok {
			t.Fatalf("user entry leaked password_hash: %#v", u)
		}
	}
	if byName["root"]["role"] != "admin" || byName["member"]["role"] != "user" {
		t.Fatalf("unexpected roles: %#v", byName)
	}
}

// TestAdminUpdateUserPasswordLengthUsesJSStringLength covers PATCH /admin/users/{id}
// password length validation, which must count UTF-16 code units (JavaScript
// String.prototype.length) to match the TS admin user-update password .min(8).max(128).
// An emoji is 1 rune but 2 UTF-16 units, so a 65-emoji password is 130 units
// (rejected by max 128) even though a rune-count check would see only 65; and a
// 3-emoji password is 6 units (rejected by min 8) even though it is 3 runes.
func TestAdminUpdateUserPasswordLengthUsesJSStringLength(t *testing.T) {
	patchPassword := func(t *testing.T, pw string) *httptest.ResponseRecorder {
		t.Helper()
		router, token, _ := newAdminRoutesTestRouter(t, []struct{ id, username, role string }{
			{"admin-1", "root", "admin"},
			{"user-1", "member", "user"},
		}, "admin-1")
		body := `{"password":"` + pw + `"}`
		req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// 65 emoji = 130 UTF-16 units: exceeds max(128), must be rejected. (A success
	// case at the max boundary is omitted because 64 emoji is 256 bytes, which
	// exceeds bcrypt's 72-byte input limit and is an unrelated failure mode.)
	if w := patchPassword(t, strings.Repeat("😀", 65)); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "password must be 8-128 characters") {
		t.Fatalf("65-emoji password (130 UTF-16 units) should be rejected, got %d %s", w.Code, w.Body.String())
	}
	// 4 emoji = 8 UTF-16 units (16 bytes): meets min(8), should succeed. A
	// rune-count check would see only 4 and wrongly reject (4 < 8).
	if w := patchPassword(t, strings.Repeat("😀", 4)); w.Code != http.StatusOK {
		t.Fatalf("4-emoji password (8 UTF-16 units) should pass min(8), got %d %s", w.Code, w.Body.String())
	}
	// 3 emoji = 6 UTF-16 units: below min(8), must be rejected.
	if w := patchPassword(t, strings.Repeat("😀", 3)); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "password must be 8-128 characters") {
		t.Fatalf("3-emoji password (6 UTF-16 units) should be rejected, got %d %s", w.Code, w.Body.String())
	}
}
