package narrators

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"narrafork-go/internal/agentbasic"

	_ "modernc.org/sqlite"
)

func TestToolPermissionModesMatchTSValues(t *testing.T) {
	service := newPermissionTestService(t)
	cwd := t.TempDir()

	cases := []struct {
		mode string
		tool string
		args string
		want string
	}{
		{mode: "default", tool: "Read", args: `{"file_path":"note.txt"}`, want: "allow"},
		{mode: "default", tool: "Write", args: `{"file_path":"note.txt","content":"x"}`, want: "ask"},
		{mode: "acceptEdits", tool: "Write", args: `{"file_path":"note.txt","content":"x"}`, want: "allow"},
		{mode: "acceptEdits", tool: "Edit", args: `{"file_path":"note.txt","old_string":"x","new_string":"y"}`, want: "allow"},
		{mode: "acceptEdits", tool: "Bash", args: `{"command":"touch note.txt"}`, want: "ask"},
		{mode: "bypassPermissions", tool: "Bash", args: `{"command":"touch note.txt"}`, want: "allow"},
		{mode: "readOnly", tool: "Write", args: `{"file_path":"note.txt","content":"x"}`, want: "deny"},
		{mode: "readOnly", tool: "Bash", args: `{"command":"pwd"}`, want: "deny"},
		{mode: "dontAsk", tool: "Write", args: `{"file_path":"note.txt","content":"x"}`, want: "deny"},
	}

	for _, tc := range cases {
		t.Run(tc.mode+"/"+tc.tool, func(t *testing.T) {
			narrator := permissionTestNarrator(cwd, tc.mode)
			decision, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall(tc.tool, tc.args))
			if err != nil {
				t.Fatalf("resolveToolPermission: %v", err)
			}
			if decision.Action != tc.want {
				t.Fatalf("decision = %s (%s), want %s", decision.Action, decision.Reason, tc.want)
			}
		})
	}
}

func TestToolPermissionNarratorDirectoryRules(t *testing.T) {
	service := newPermissionTestService(t)
	cwd := t.TempDir()
	narrator := permissionTestNarrator(cwd, "readOnly")
	allowedDir := filepath.Join(cwd, "allowed")
	blockedDir := filepath.Join(cwd, "blocked")

	if _, err := service.DB.Exec(`INSERT INTO narrator_whitelist_dirs (id, narrator_id, path, access_level, enabled, created_at) VALUES ('wl', ?, ?, 'readWrite', 1, 'now')`, narrator.ID, allowedDir); err != nil {
		t.Fatalf("insert whitelist dir: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO narrator_blacklist_dirs (id, narrator_id, path, deny_level, enabled, created_at) VALUES ('bl', ?, ?, 'denyAll', 1, 'now')`, narrator.ID, blockedDir); err != nil {
		t.Fatalf("insert blacklist dir: %v", err)
	}

	allowed, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Write", `{"file_path":"allowed/out.txt","content":"ok"}`))
	if err != nil {
		t.Fatalf("resolve allowed write: %v", err)
	}
	if allowed.Action != "allow" {
		t.Fatalf("whitelisted write decision = %s (%s), want allow", allowed.Action, allowed.Reason)
	}

	blocked, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Read", `{"file_path":"blocked/secret.txt"}`))
	if err != nil {
		t.Fatalf("resolve blocked read: %v", err)
	}
	if blocked.Action != "deny" {
		t.Fatalf("blacklisted read decision = %s (%s), want deny", blocked.Action, blocked.Reason)
	}
}

func TestToolPermissionNarratorCommandRules(t *testing.T) {
	service := newPermissionTestService(t)
	cwd := t.TempDir()
	narrator := permissionTestNarrator(cwd, "readOnly")

	if _, err := service.DB.Exec(`INSERT INTO narrator_whitelist_cmds (id, narrator_id, pattern, enabled, created_at) VALUES ('cw', ?, 'pwd', 1, 'now')`, narrator.ID); err != nil {
		t.Fatalf("insert command whitelist: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO narrator_blacklist_cmds (id, narrator_id, pattern, deny_prompt, enabled, created_at) VALUES ('cb', ?, 'rm *', 'do not remove files', 1, 'now')`, narrator.ID); err != nil {
		t.Fatalf("insert command blacklist: %v", err)
	}

	allowed, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"pwd"}`))
	if err != nil {
		t.Fatalf("resolve whitelisted command: %v", err)
	}
	if allowed.Action != "allow" {
		t.Fatalf("whitelisted command decision = %s (%s), want allow", allowed.Action, allowed.Reason)
	}

	blocked, err := service.resolveToolPermission(context.Background(), narrator, cwd, permissionToolCall("Bash", `{"command":"rm file.txt"}`))
	if err != nil {
		t.Fatalf("resolve blacklisted command: %v", err)
	}
	if blocked.Action != "deny" || blocked.Reason != "do not remove files" {
		t.Fatalf("blacklisted command decision = %s (%s), want deny with prompt", blocked.Action, blocked.Reason)
	}
}

func TestPermissionModeNormalizationUsesTSValues(t *testing.T) {
	cases := map[string]string{
		"default":            "default",
		"acceptEdits":        "acceptEdits",
		"bypassPermissions":  "bypassPermissions",
		"readOnly":           "readOnly",
		"dontAsk":            "dontAsk",
		"allowByDefault":     "acceptEdits",
		"denyByDefault":      "dontAsk",
		"trust-all":          "bypassPermissions",
		"danger-full-access": "bypassPermissions",
		"plan":               "default",
		"unknown":            "readOnly",
	}
	for input, want := range cases {
		if got := normalizePermissionMode(input, "readOnly"); got != want {
			t.Fatalf("normalizePermissionMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestToolCallDBStatusUsesTSAllowedValues(t *testing.T) {
	cases := map[string]string{
		"pending":   "pending",
		"running":   "running",
		"completed": "success",
		"approved":  "success",
		"denied":    "fail",
		"error":     "fail",
	}
	for input, want := range cases {
		if got := toolCallDBStatus(input); got != want {
			t.Fatalf("toolCallDBStatus(%q) = %q, want %q", input, got, want)
		}
	}
}

func newPermissionTestService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	stmts := []string{
		`CREATE TABLE narrator_whitelist_dirs (id TEXT PRIMARY KEY, narrator_id TEXT, path TEXT, access_level TEXT, enabled INTEGER, created_at TEXT)`,
		`CREATE TABLE narrator_blacklist_dirs (id TEXT PRIMARY KEY, narrator_id TEXT, path TEXT, deny_level TEXT, enabled INTEGER, created_at TEXT)`,
		`CREATE TABLE narrator_whitelist_cmds (id TEXT PRIMARY KEY, narrator_id TEXT, pattern TEXT, enabled INTEGER, created_at TEXT)`,
		`CREATE TABLE narrator_blacklist_cmds (id TEXT PRIMARY KEY, narrator_id TEXT, pattern TEXT, deny_prompt TEXT, enabled INTEGER, created_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create permission test table: %v", err)
		}
	}
	return &Service{DB: db}
}

func permissionTestNarrator(cwd, mode string) Narrator {
	return Narrator{ID: "nar-perm", Cwd: &cwd, PermissionMode: &mode, Variant: "primary"}
}

func permissionToolCall(name, arguments string) agentbasic.ToolCall {
	var call agentbasic.ToolCall
	call.ID = "tool-perm"
	call.Type = "function"
	call.Function.Name = name
	call.Function.Arguments = arguments
	return call
}
