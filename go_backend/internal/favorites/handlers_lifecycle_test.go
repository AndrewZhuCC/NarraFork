package favorites

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/auth"
)

// TestFavoriteCRUDLifecycleSuccessPath exercises the create -> list -> update ->
// reorder -> delete happy path with a real database, asserting the actual data
// effects (auto-incrementing sort_order, label update/clear, reorder persistence,
// delete removal). Existing favorites tests only cover validation/missing/error
// paths, so the success path was previously unverified.
func TestFavoriteCRUDLifecycleSuccessPath(t *testing.T) {
	sqlDB := openFavoritesParityTestDB(t)
	settings := loadFavoritesTestSettings(t)
	authService := auth.NewService(sqlDB, settings)
	result, err := authService.Register(context.Background(), "member", "password123", "en")
	if err != nil {
		t.Fatalf("register user: %v", err)
	}
	token := result.Token

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(authService.Middleware)
		NewHandler(sqlDB).RegisterRoutes(r)
	})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// Create two favorites; sort_order should auto-increment 0, 1.
	first := do(http.MethodPost, "/", `{"path":"/repo/alpha","label":"Alpha"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("create first: %d %s", first.Code, first.Body.String())
	}
	var firstFav Favorite
	if err := json.Unmarshal(first.Body.Bytes(), &firstFav); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if firstFav.SortOrder != 0 || firstFav.Label == nil || *firstFav.Label != "Alpha" {
		t.Fatalf("unexpected first favorite: %#v", firstFav)
	}

	second := do(http.MethodPost, "/", `{"path":"/repo/beta","label":"Beta"}`)
	if second.Code != http.StatusCreated {
		t.Fatalf("create second: %d %s", second.Code, second.Body.String())
	}
	var secondFav Favorite
	if err := json.Unmarshal(second.Body.Bytes(), &secondFav); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if secondFav.SortOrder != 1 {
		t.Fatalf("expected second sort_order 1, got %d", secondFav.SortOrder)
	}

	// List returns both, ordered by sort_order.
	listResp := do(http.MethodGet, "/", "")
	var list []Favorite
	if err := json.Unmarshal(listResp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 2 || list[0].ID != firstFav.ID || list[1].ID != secondFav.ID {
		t.Fatalf("unexpected list ordering: %#v", list)
	}

	// Update the first favorite's label and path.
	upd := do(http.MethodPatch, "/"+firstFav.ID, `{"path":"/repo/alpha-renamed","label":"Alpha Renamed"}`)
	if upd.Code != http.StatusOK {
		t.Fatalf("update: %d %s", upd.Code, upd.Body.String())
	}
	afterUpdate := favoriteByID(t, do, firstFav.ID)
	if afterUpdate.Path != "/repo/alpha-renamed" || afterUpdate.Label == nil || *afterUpdate.Label != "Alpha Renamed" {
		t.Fatalf("update did not apply: %#v", afterUpdate)
	}

	// Clear the label via clearLabel semantics.
	clr := do(http.MethodPatch, "/"+firstFav.ID, `{"label":null}`)
	if clr.Code != http.StatusOK {
		t.Fatalf("clear label: %d %s", clr.Code, clr.Body.String())
	}
	afterClear := favoriteByID(t, do, firstFav.ID)
	if afterClear.Label != nil {
		t.Fatalf("expected label cleared to null, got %#v", afterClear.Label)
	}

	// Reorder: put second before first.
	reorder := do(http.MethodPut, "/reorder", `{"ids":["`+secondFav.ID+`","`+firstFav.ID+`"]}`)
	if reorder.Code != http.StatusOK {
		t.Fatalf("reorder: %d %s", reorder.Code, reorder.Body.String())
	}
	listResp2 := do(http.MethodGet, "/", "")
	var list2 []Favorite
	if err := json.Unmarshal(listResp2.Body.Bytes(), &list2); err != nil {
		t.Fatalf("decode list2: %v", err)
	}
	if len(list2) != 2 || list2[0].ID != secondFav.ID || list2[1].ID != firstFav.ID {
		t.Fatalf("reorder did not persist: %#v", list2)
	}

	// Delete the first favorite; list shrinks to one.
	del := do(http.MethodDelete, "/"+firstFav.ID, "")
	if del.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", del.Code, del.Body.String())
	}
	listResp3 := do(http.MethodGet, "/", "")
	var list3 []Favorite
	if err := json.Unmarshal(listResp3.Body.Bytes(), &list3); err != nil {
		t.Fatalf("decode list3: %v", err)
	}
	if len(list3) != 1 || list3[0].ID != secondFav.ID {
		t.Fatalf("expected only second favorite after delete, got %#v", list3)
	}
}

func favoriteByID(t *testing.T, do func(method, path, body string) *httptest.ResponseRecorder, id string) Favorite {
	t.Helper()
	resp := do(http.MethodGet, "/", "")
	var list []Favorite
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list for lookup: %v", err)
	}
	for _, fav := range list {
		if fav.ID == id {
			return fav
		}
	}
	t.Fatalf("favorite %s not found in list %#v", id, list)
	return Favorite{}
}
