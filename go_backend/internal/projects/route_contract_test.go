package projects

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestProjectBackupRoutesExposeStatusAndSyncContracts(t *testing.T) {
	gitPath := t.TempDir()
	sqlDB := openProjectBackupTestDB(t)
	defer sqlDB.Close()
	seedFullScopeProject(t, sqlDB, gitPath)
	h := NewHandler(&Service{DB: sqlDB})

	status := serveProjectRouteContractJSON(t, h, http.MethodGet, "/projects/p1/backup/status", "")
	if status["projectId"] != "p1" || status["backupExists"] != false {
		t.Fatalf("unexpected pre-sync status: %#v", status)
	}
	scope, _ := status["scope"].([]any)
	if !containsProjectRouteValue(scope, "narrator_messages") || !containsProjectRouteValue(scope, "merge_sessions") {
		t.Fatalf("backup status scope missing critical tables: %#v", status["scope"])
	}

	sync := serveProjectRouteContractJSON(t, h, http.MethodPost, "/projects/p1/backup/sync", "")
	if sync["projectId"] != "p1" || sync["backupPath"] == "" {
		t.Fatalf("unexpected sync response: %#v", sync)
	}
	synced, _ := sync["synced"].(map[string]any)
	if synced["projects"] != float64(1) || synced["narrator_messages"] != float64(1) || synced["review_conclusions"] != float64(1) {
		t.Fatalf("sync response missing full critical scope counts: %#v", synced)
	}

	status = serveProjectRouteContractJSON(t, h, http.MethodGet, "/projects/p1/backup/status", "")
	if status["backupExists"] != true {
		t.Fatalf("expected backupExists=true after sync: %#v", status)
	}
}

func TestProjectDeleteRouteReturnsCleanupContract(t *testing.T) {
	sqlDB := newProjectDeleteTestDB(t)
	h := NewHandler(&Service{DB: sqlDB})

	body := serveProjectRouteContractJSON(t, h, http.MethodDelete, "/projects/p1", "")
	if body["ok"] != true || body["projectId"] != "p1" {
		t.Fatalf("unexpected delete response: %#v", body)
	}
	if got := countWhere(t, sqlDB, "projects", "id = 'p1'"); got != 0 {
		t.Fatalf("project p1 should be deleted, count=%d", got)
	}
	if got := countWhere(t, sqlDB, "narrators", "id IN ('n1','n_child')"); got != 0 {
		t.Fatalf("project narrators should be deleted, count=%d", got)
	}
}

func TestProjectImportRouteRestoresBackupContract(t *testing.T) {
	gitPath := t.TempDir()
	source := openProjectBackupTestDB(t)
	defer source.Close()
	seedFullScopeProject(t, source, gitPath)
	if _, err := (&Service{DB: source}).SyncBackup(t.Context(), "p1"); err != nil {
		t.Fatalf("seed project backup: %v", err)
	}

	target := openProjectBackupTestDB(t)
	defer target.Close()
	h := NewHandler(&Service{DB: target})
	body := serveProjectRouteContractJSON(t, h, http.MethodPost, "/projects/import", `{"gitPath":"`+gitPath+`"}`)
	if body["skipped"] != false || body["projectId"] != "p1" {
		t.Fatalf("unexpected import response: %#v", body)
	}
	if got := countProjectRouteRows(t, target, "projects"); got != 1 {
		t.Fatalf("imported projects count=%d", got)
	}
	if got := countProjectRouteRows(t, target, "narrator_messages"); got != 1 {
		t.Fatalf("imported narrator_messages count=%d", got)
	}
}

// Wave 291: Go project create must mirror the TypeScript createProjectSchema Zod
// bounds — name min 1 / max 200, description max 2000, cloneUsername/clonePassword
// max 200 — and reject out-of-range values with 400 before touching git.
func TestCreateProjectValidatesZodBoundsLikeTypeScript(t *testing.T) {
	h := NewHandler(&Service{})
	r := chi.NewRouter()
	r.Route("/projects", h.RegisterRoutes)

	longName := strings.Repeat("x", 201)
	longText := strings.Repeat("d", 2001)
	longCred := strings.Repeat("c", 201)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"empty name", `{"name":"  ","repoMode":"existing","gitPath":"/tmp/x"}`},
		{"overlong name", `{"name":"` + longName + `","repoMode":"existing","gitPath":"/tmp/x"}`},
		{"overlong description", `{"name":"Proj","description":"` + longText + `","repoMode":"existing","gitPath":"/tmp/x"}`},
		{"overlong cloneUsername", `{"name":"Proj","repoMode":"existing","gitPath":"/tmp/x","cloneUsername":"` + longCred + `"}`},
		{"overlong clonePassword", `{"name":"Proj","repoMode":"existing","gitPath":"/tmp/x","clonePassword":"` + longCred + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/projects", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("create project %q status = %d body=%s, want 400", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func serveProjectRouteContractJSON(t *testing.T, h *Handler, method, path, body string) map[string]any {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/projects", h.RegisterRoutes)
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code < 200 || w.Code >= 300 {
		t.Fatalf("%s %s returned %d: %s", method, path, w.Code, w.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	return decoded
}

func countProjectRouteRows(t *testing.T, db rowCounter, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

type rowCounter interface {
	QueryRow(query string, args ...any) *sql.Row
}

func containsProjectRouteValue(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
