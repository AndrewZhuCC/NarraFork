package narrators

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestUpdateRuleMatchesTSUpdateSchemas pins the four narrator rule PATCH routes to
// their TS update schemas (server/lib/validators/narrators.ts). The previous
// generic updateRule only wrote `enabled` plus one fixed secondary column and ran
// NO validation, so it (a) silently dropped pattern/denyPrompt updates the TS
// routes accept, (b) never rejected overlong/invalid values, and (c) returned ok
// for an empty body instead of the TS "No fields to update" 400.
func TestUpdateRuleMatchesTSUpdateSchemas(t *testing.T) {
	sqlDB := openRuleUpdateTestDB(t)
	defer sqlDB.Close()
	narratorID := "nar-upd-1"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, status, created_at, updated_at) VALUES (?, 'primary', 'active', 'now', 'now')`, narratorID); err != nil {
		t.Fatalf("seed narrator: %v", err)
	}
	// Seed one row per rule table.
	sqlDB.Exec(`INSERT INTO narrator_whitelist_dirs (id, narrator_id, path, access_level, enabled, created_at) VALUES ('wd1', ?, '/a', 'readOnly', 1, 'now')`, narratorID)
	sqlDB.Exec(`INSERT INTO narrator_blacklist_dirs (id, narrator_id, path, deny_level, enabled, created_at) VALUES ('bd1', ?, '/b', 'denyAll', 1, 'now')`, narratorID)
	sqlDB.Exec(`INSERT INTO narrator_whitelist_cmds (id, narrator_id, pattern, enabled, created_at) VALUES ('wc1', ?, 'git status', 1, 'now')`, narratorID)
	sqlDB.Exec(`INSERT INTO narrator_blacklist_cmds (id, narrator_id, pattern, deny_prompt, enabled, created_at) VALUES ('bc1', ?, 'rm -rf', 'no', 1, 'now')`, narratorID)
	service := &Service{DB: sqlDB}

	patch := func(suffix, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, suffix, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return serveNarratorHandlerWithService(service, req)
	}

	// cmd-whitelist: pattern update is now applied (previously dropped) and trimmed.
	if w := patch("/narrators/cmd-whitelist/wc1", `{"pattern":"  npm test  "}`); w.Code != http.StatusOK {
		t.Fatalf("cmd-whitelist pattern update got %d body=%s", w.Code, w.Body.String())
	}
	var wcPattern string
	sqlDB.QueryRow(`SELECT pattern FROM narrator_whitelist_cmds WHERE id='wc1'`).Scan(&wcPattern)
	if wcPattern != "npm test" {
		t.Fatalf("cmd-whitelist pattern should be updated+trimmed, got %q", wcPattern)
	}

	// cmd-blacklist: denyPrompt update is now applied (previously dropped).
	if w := patch("/narrators/cmd-blacklist/bc1", `{"denyPrompt":"blocked by policy"}`); w.Code != http.StatusOK {
		t.Fatalf("cmd-blacklist denyPrompt update got %d body=%s", w.Code, w.Body.String())
	}
	var bcDeny sql.NullString
	sqlDB.QueryRow(`SELECT deny_prompt FROM narrator_blacklist_cmds WHERE id='bc1'`).Scan(&bcDeny)
	if !bcDeny.Valid || bcDeny.String != "blocked by policy" {
		t.Fatalf("cmd-blacklist denyPrompt should be updated, got %#v", bcDeny)
	}

	// cmd-blacklist: denyPrompt null clears it (nullable).
	if w := patch("/narrators/cmd-blacklist/bc1", `{"denyPrompt":null}`); w.Code != http.StatusOK {
		t.Fatalf("cmd-blacklist denyPrompt null clear got %d body=%s", w.Code, w.Body.String())
	}
	sqlDB.QueryRow(`SELECT deny_prompt FROM narrator_blacklist_cmds WHERE id='bc1'`).Scan(&bcDeny)
	if bcDeny.Valid {
		t.Fatalf("cmd-blacklist denyPrompt null should clear to NULL, got %#v", bcDeny)
	}

	// dir routes: accessLevel / denyLevel enum updates apply.
	if w := patch("/narrators/whitelist-dirs/wd1", `{"accessLevel":"full"}`); w.Code != http.StatusOK {
		t.Fatalf("whitelist-dir accessLevel update got %d body=%s", w.Code, w.Body.String())
	}
	var wdLevel string
	sqlDB.QueryRow(`SELECT access_level FROM narrator_whitelist_dirs WHERE id='wd1'`).Scan(&wdLevel)
	if wdLevel != "full" {
		t.Fatalf("whitelist-dir accessLevel should be full, got %q", wdLevel)
	}

	// Validation + empty-body rejections (all 400).
	bad := []struct {
		name, suffix, body string
	}{
		{"overlong cmd pattern", "/cmd-blacklist/bc1", `{"pattern":"` + strings.Repeat("x", 201) + `"}`},
		{"empty cmd pattern", "/cmd-whitelist/wc1", `{"pattern":"   "}`},
		{"overlong denyPrompt", "/cmd-blacklist/bc1", `{"denyPrompt":"` + strings.Repeat("d", 2001) + `"}`},
		{"bad accessLevel", "/whitelist-dirs/wd1", `{"accessLevel":"writeOnly"}`},
		{"accessLevel null", "/whitelist-dirs/wd1", `{"accessLevel":null}`},
		{"bad denyLevel", "/blacklist-dirs/bd1", `{"denyLevel":"maybe"}`},
		{"enabled null", "/cmd-whitelist/wc1", `{"enabled":null}`},
		{"empty body whitelist dir", "/whitelist-dirs/wd1", `{}`},
		{"empty body cmd blacklist", "/cmd-blacklist/bc1", `{}`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if w := patch("/narrators"+tc.suffix, tc.body); w.Code != http.StatusBadRequest {
				t.Fatalf("%s should be 400, got %d body=%s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func openRuleUpdateTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	stmts := []string{
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, type TEXT, status TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_whitelist_dirs (id TEXT PRIMARY KEY, narrator_id TEXT, path TEXT, access_level TEXT, enabled INTEGER, created_at TEXT)`,
		`CREATE TABLE narrator_blacklist_dirs (id TEXT PRIMARY KEY, narrator_id TEXT, path TEXT, deny_level TEXT, enabled INTEGER, created_at TEXT)`,
		`CREATE TABLE narrator_whitelist_cmds (id TEXT PRIMARY KEY, narrator_id TEXT, pattern TEXT, enabled INTEGER, created_at TEXT)`,
		`CREATE TABLE narrator_blacklist_cmds (id TEXT PRIMARY KEY, narrator_id TEXT, pattern TEXT, deny_prompt TEXT, enabled INTEGER, created_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return sqlDB
}
