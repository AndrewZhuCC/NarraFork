package chapters

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestComposeInfoRouteParsesServicesAndGuardsMissingWorktree covers the
// GET /chapters/{id}/compose-info route end to end through the router: it
// resolves the compose file under the chapter worktree, parses its services,
// and returns the {services, composeFile} envelope; a chapter without a worktree
// path returns a 400. Only the parser helper was previously tested, not the route.
func TestComposeInfoRouteParsesServicesAndGuardsMissingWorktree(t *testing.T) {
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	// Auto-discovered compose.yml (no explicit composeFile in container_config).
	composePath := filepath.Join(worktree, "compose.yml")
	if err := os.WriteFile(composePath, []byte("services:\n  web:\n    image: nginx\n  db:\n    image: postgres\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, "{}", "")

	router := newChapterRouter(NewHandler(&Service{DB: database}))

	serve := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/c1/compose-info", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	w := serve()
	if w.Code != http.StatusOK {
		t.Fatalf("compose-info status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode compose-info: %v raw=%s", err, w.Body.String())
	}
	if body["composeFile"] == nil {
		t.Fatalf("expected resolved composeFile, got nil (%#v)", body)
	}
	services, ok := body["services"].([]any)
	if !ok || len(services) != 2 {
		t.Fatalf("expected 2 parsed services, got %#v", body["services"])
	}
	names := map[string]bool{}
	for _, raw := range services {
		svc, _ := raw.(map[string]any)
		if name, _ := svc["name"].(string); name != "" {
			names[name] = true
		}
	}
	if !names["web"] || !names["db"] {
		t.Fatalf("expected web and db services, got %#v", services)
	}

	// A chapter without a worktree path is a 400.
	if _, err := database.Exec(`UPDATE chapters SET worktree_path = NULL WHERE id = 'c1'`); err != nil {
		t.Fatalf("clear worktree: %v", err)
	}
	missing := serve()
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("compose-info without worktree should 400, got %d body=%s", missing.Code, missing.Body.String())
	}
}
