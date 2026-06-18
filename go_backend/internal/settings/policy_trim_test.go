package settings

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPatchTrimsAgentPolicyPathAndPattern is a regression test for the Zod
// .trim() storage divergence: TS whitelist/blacklist dir `path` and command
// `pattern` schemas (server/lib/validators/common.ts) use z.string().trim(),
// which stores the trimmed value. settingsRequiredTrimmedString previously
// validated the trimmed value but persisted the raw (untrimmed) string.
func TestPatchTrimsAgentPolicyPathAndPattern(t *testing.T) {
	store := newTestStore(t)
	h := &Handler{Store: store}

	payload := `{"agent":{` +
		`"whitelistDirs":[{"path":"  /home/project/src  ","accessLevel":"readWrite"}],` +
		`"commandWhitelist":[{"pattern":"  git status  "}]` +
		`}}`
	req := httptest.NewRequest(http.MethodPatch, "/api/settings", bytes.NewReader([]byte(payload)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.patch(w, req); err != nil {
		t.Fatalf("patch returned error: %v", err)
	}

	agent, ok := store.Raw()["agent"].(map[string]any)
	if !ok {
		t.Fatalf("agent settings missing: %#v", store.Raw())
	}

	dirs, ok := agent["whitelistDirs"].([]any)
	if !ok || len(dirs) != 1 {
		t.Fatalf("whitelistDirs not persisted: %#v", agent["whitelistDirs"])
	}
	dir := dirs[0].(map[string]any)
	if dir["path"] != "/home/project/src" {
		t.Fatalf("stored path should be trimmed, got %q", dir["path"])
	}

	cmds, ok := agent["commandWhitelist"].([]any)
	if !ok || len(cmds) != 1 {
		t.Fatalf("commandWhitelist not persisted: %#v", agent["commandWhitelist"])
	}
	cmd := cmds[0].(map[string]any)
	if cmd["pattern"] != "git status" {
		t.Fatalf("stored pattern should be trimmed, got %q", cmd["pattern"])
	}
}
