package narrators

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestCreateRuleTrimsPathAndPatternLikeTS is a regression test for a parity
// divergence: the TS narrator rule schemas use z.string().trim() for path and
// pattern (server/lib/validators/narrators.ts), which stores the TRIMMED value.
// Go previously inserted the raw (untrimmed) value, so a path like "  /foo  "
// would be stored with stray whitespace and fail to match rules the way TS
// would. createRule now trims path/pattern before persisting.
func TestCreateRuleTrimsPathAndPatternLikeTS(t *testing.T) {
	sqlDB := openRuleTrimTestDB(t)
	defer sqlDB.Close()
	narratorID := "nar-trim-1"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, status, created_at, updated_at) VALUES (?, 'primary', 'active', 'now', 'now')`, narratorID); err != nil {
		t.Fatalf("seed narrator: %v", err)
	}
	service := &Service{DB: sqlDB}

	t.Run("whitelist dir path is trimmed", func(t *testing.T) {
		handler := serveRuleCreate(service, narratorID, "/whitelist-dirs",
			`{"path":"  /home/project/src  ","accessLevel":"readWrite"}`)
		if handler.Code != http.StatusCreated {
			t.Fatalf("create whitelist dir got %d body=%s", handler.Code, handler.Body.String())
		}
		var stored string
		if err := sqlDB.QueryRow(`SELECT path FROM narrator_whitelist_dirs WHERE narrator_id=?`, narratorID).Scan(&stored); err != nil {
			t.Fatalf("read stored path: %v", err)
		}
		if stored != "/home/project/src" {
			t.Fatalf("stored path should be trimmed, got %q", stored)
		}
	})

	t.Run("cmd whitelist pattern is trimmed", func(t *testing.T) {
		handler := serveRuleCreate(service, narratorID, "/cmd-whitelist",
			`{"pattern":"  git status  "}`)
		if handler.Code != http.StatusCreated {
			t.Fatalf("create cmd whitelist got %d body=%s", handler.Code, handler.Body.String())
		}
		var stored string
		if err := sqlDB.QueryRow(`SELECT pattern FROM narrator_whitelist_cmds WHERE narrator_id=?`, narratorID).Scan(&stored); err != nil {
			t.Fatalf("read stored pattern: %v", err)
		}
		if stored != "git status" {
			t.Fatalf("stored pattern should be trimmed, got %q", stored)
		}
	})

	t.Run("whitespace-only path rejected", func(t *testing.T) {
		handler := serveRuleCreate(service, narratorID, "/whitelist-dirs",
			`{"path":"     ","accessLevel":"readWrite"}`)
		if handler.Code == http.StatusCreated {
			t.Fatalf("whitespace-only path should be rejected, got %d", handler.Code)
		}
	})
}

func serveRuleCreate(service *Service, narratorID, suffix, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/narrators/"+narratorID+suffix, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return serveNarratorHandlerWithService(service, req)
}

// TestUpdateCwdTrimsLikeTS is a regression test for the same trim-storage
// divergence on the PATCH /narrators/{id}/cwd route: TS updateNarratorCwdSchema
// uses z.string().trim(), which stores AND echoes the trimmed value. Go
// previously validated the trimmed value but stored/returned the raw value.
func TestUpdateCwdTrimsLikeTS(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}

	// Use a real existing directory so the TS-mirrored isAbsolute + fs.access
	// existence checks pass; the assertion under test is the trim-storage behavior.
	dir := t.TempDir()
	req := httptest.NewRequest(http.MethodPatch, "/narrators/"+narratorID+"/cwd",
		strings.NewReader(`{"cwd":"  `+dir+`  "}`))
	req.Header.Set("Content-Type", "application/json")
	w := serveNarratorHandlerWithService(service, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update cwd got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"cwd":"`+dir+`"`) {
		t.Fatalf("response should echo trimmed cwd, got %s", w.Body.String())
	}
	var stored string
	if err := sqlDB.QueryRow(`SELECT cwd FROM narrators WHERE id=?`, narratorID).Scan(&stored); err != nil {
		t.Fatalf("read stored cwd: %v", err)
	}
	if stored != dir {
		t.Fatalf("stored cwd should be trimmed, got %q", stored)
	}

	// Whitespace-only cwd must be rejected (TS .trim().min(1)).
	badReq := httptest.NewRequest(http.MethodPatch, "/narrators/"+narratorID+"/cwd",
		strings.NewReader(`{"cwd":"   "}`))
	badReq.Header.Set("Content-Type", "application/json")
	if bad := serveNarratorHandlerWithService(service, badReq); bad.Code == http.StatusOK {
		t.Fatalf("whitespace-only cwd should be rejected, got %d", bad.Code)
	}
}

// TestUpdateCwdRejectsRelativeAndMissingAndDetectsUnchanged pins the TS cwd PATCH
// route behavior (server/routes/narrators.ts): after the trim/length schema it
// requires isAbsolute(cwd) and fs.access(cwd) existence, and returns changed:false
// without persisting when the new cwd equals the existing one. Go previously
// skipped all three.
func TestUpdateCwdRejectsRelativeAndMissingAndDetectsUnchanged(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	service := &Service{DB: sqlDB}

	// Relative path -> 400 "cwd must be an absolute path".
	rel := httptest.NewRequest(http.MethodPatch, "/narrators/"+narratorID+"/cwd",
		strings.NewReader(`{"cwd":"relative/dir"}`))
	rel.Header.Set("Content-Type", "application/json")
	if w := serveNarratorHandlerWithService(service, rel); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "absolute") {
		t.Fatalf("relative cwd should be 400 absolute error, got %d body=%s", w.Code, w.Body.String())
	}

	// Absolute but non-existent path -> 400 (fs.access existence check).
	missing := httptest.NewRequest(http.MethodPatch, "/narrators/"+narratorID+"/cwd",
		strings.NewReader(`{"cwd":"/nonexistent/path/`+narratorID+`"}`))
	missing.Header.Set("Content-Type", "application/json")
	if w := serveNarratorHandlerWithService(service, missing); w.Code != http.StatusBadRequest {
		t.Fatalf("missing cwd should be 400, got %d body=%s", w.Code, w.Body.String())
	}

	// First set to a real dir -> changed:true.
	dir := t.TempDir()
	set := httptest.NewRequest(http.MethodPatch, "/narrators/"+narratorID+"/cwd",
		strings.NewReader(`{"cwd":"`+dir+`"}`))
	set.Header.Set("Content-Type", "application/json")
	if w := serveNarratorHandlerWithService(service, set); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"changed":true`) {
		t.Fatalf("first cwd set should be changed:true, got %d body=%s", w.Code, w.Body.String())
	}

	// Re-submit the same dir -> changed:false (no persist/reminder).
	again := httptest.NewRequest(http.MethodPatch, "/narrators/"+narratorID+"/cwd",
		strings.NewReader(`{"cwd":"`+dir+`"}`))
	again.Header.Set("Content-Type", "application/json")
	if w := serveNarratorHandlerWithService(service, again); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"changed":false`) {
		t.Fatalf("unchanged cwd should be changed:false, got %d body=%s", w.Code, w.Body.String())
	}
}

func openRuleTrimTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	stmts := []string{
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, type TEXT, status TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_whitelist_dirs (id TEXT PRIMARY KEY, narrator_id TEXT, path TEXT, access_level TEXT, enabled INTEGER, created_at TEXT)`,
		`CREATE TABLE narrator_whitelist_cmds (id TEXT PRIMARY KEY, narrator_id TEXT, pattern TEXT, enabled INTEGER, created_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return sqlDB
}
