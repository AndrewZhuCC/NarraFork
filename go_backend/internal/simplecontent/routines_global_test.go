package simplecontent

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"narrafork-go/internal/config"
)

// TestGlobalRoutineListAndToggleRoundTrip covers the global routine list (builtin
// routines with enabled flags) and the toggle enable/disable persistence, plus the
// not-found guard. Existing routines tests only cover project-scoped routines, so
// the global list/toggle handlers were previously unverified.
func TestGlobalRoutineListAndToggleRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	h := NewHandler(store)

	// List returns the builtin routines, each carrying an enabled flag.
	listResp := serveSimpleContentRequest(h, http.MethodGet, "/routines/", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list routines: %d %s", listResp.Code, listResp.Body.String())
	}
	var listBody struct {
		Routines []map[string]any `json:"routines"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listBody.Routines) == 0 {
		t.Fatalf("expected builtin routines, got none")
	}
	var sampleID string
	for _, routine := range listBody.Routines {
		if _, ok := routine["enabled"]; !ok {
			t.Fatalf("routine missing enabled flag: %#v", routine)
		}
		if sampleID == "" {
			sampleID, _ = routine["id"].(string)
		}
	}
	if sampleID == "" {
		t.Fatalf("could not determine a routine id from %#v", listBody.Routines)
	}

	// Toggling an unknown routine is a 404.
	missing := serveSimpleContentRequest(h, http.MethodPost, "/routines/not-a-routine/toggle", map[string]any{"enabled": false})
	if missing.Code != http.StatusNotFound {
		t.Fatalf("toggle unknown routine should 404, got %d", missing.Code)
	}

	// Disable the sample routine, then confirm the list reflects enabled=false.
	off := serveSimpleContentRequest(h, http.MethodPost, "/routines/"+sampleID+"/toggle", map[string]any{"enabled": false})
	if off.Code != http.StatusOK {
		t.Fatalf("disable routine: %d %s", off.Code, off.Body.String())
	}
	if got := routineEnabledInList(t, h, sampleID); got != false {
		t.Fatalf("expected %s disabled after toggle off, got enabled=%v", sampleID, got)
	}

	// Re-enable and confirm.
	on := serveSimpleContentRequest(h, http.MethodPost, "/routines/"+sampleID+"/toggle", map[string]any{"enabled": true})
	if on.Code != http.StatusOK {
		t.Fatalf("enable routine: %d %s", on.Code, on.Body.String())
	}
	if got := routineEnabledInList(t, h, sampleID); got != true {
		t.Fatalf("expected %s enabled after toggle on, got enabled=%v", sampleID, got)
	}

	// Mirror TS `if (typeof body.enabled !== "boolean") throw 400`: a missing or
	// non-boolean `enabled` must reject with 400 (validated before the existence
	// check), not silently decode to false and disable the routine.
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing enabled", map[string]any{}},
		{"string enabled", map[string]any{"enabled": "yes"}},
		{"numeric enabled", map[string]any{"enabled": 1}},
		{"null enabled", map[string]any{"enabled": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := serveSimpleContentRequest(h, http.MethodPost, "/routines/"+sampleID+"/toggle", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("toggle with %s should be 400, got %d body=%s", tc.name, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "enabled must be a boolean") {
				t.Fatalf("toggle with %s should return the boolean error, got %s", tc.name, w.Body.String())
			}
		})
	}
}

func routineEnabledInList(t *testing.T, h *Handler, id string) bool {
	t.Helper()
	resp := serveSimpleContentRequest(h, http.MethodGet, "/routines/", nil)
	var body struct {
		Routines []map[string]any `json:"routines"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list for lookup: %v", err)
	}
	for _, routine := range body.Routines {
		if routine["id"] == id {
			enabled, _ := routine["enabled"].(bool)
			return enabled
		}
	}
	t.Fatalf("routine %s not found in list", id)
	return false
}

// TestGlobalPromptReadWriteAndPathValidation covers the global prompt read/write
// handlers: writing to a known candidate path persists content, reading it back
// reports the content + filePath + candidate existence, and writing to an
// unknown path is rejected.
func TestGlobalPromptReadWriteAndPathValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	h := NewHandler()

	// Initially no prompt file exists: content/filePath null, candidates all absent.
	initial := serveSimpleContentRequest(h, http.MethodGet, "/routines/global-prompt", nil)
	if initial.Code != http.StatusOK {
		t.Fatalf("get global prompt: %d %s", initial.Code, initial.Body.String())
	}
	var initialBody map[string]any
	if err := json.Unmarshal(initial.Body.Bytes(), &initialBody); err != nil {
		t.Fatalf("decode initial: %v", err)
	}
	if initialBody["filePath"] != nil || initialBody["content"] != nil {
		t.Fatalf("expected no prompt initially, got %#v", initialBody)
	}
	candidates, _ := initialBody["candidates"].([]any)
	if len(candidates) == 0 {
		t.Fatalf("expected candidate paths, got %#v", initialBody["candidates"])
	}

	// Write to a known candidate path (~/.claude/CLAUDE.md).
	target := filepath.Join(home, ".claude", "CLAUDE.md")
	writeResp := serveSimpleContentRequest(h, http.MethodPut, "/routines/global-prompt", map[string]any{
		"content":  "be concise\n",
		"filePath": target,
	})
	if writeResp.Code != http.StatusOK {
		t.Fatalf("put global prompt: %d %s", writeResp.Code, writeResp.Body.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected prompt file written at %s: %v", target, err)
	}

	// Reading now returns the content and the resolved filePath.
	after := serveSimpleContentRequest(h, http.MethodGet, "/routines/global-prompt", nil)
	var afterBody map[string]any
	if err := json.Unmarshal(after.Body.Bytes(), &afterBody); err != nil {
		t.Fatalf("decode after: %v", err)
	}
	if afterBody["content"] != "be concise\n" {
		t.Fatalf("expected written content, got %#v", afterBody["content"])
	}
	if afterBody["filePath"] == nil {
		t.Fatalf("expected resolved filePath, got nil")
	}

	// Writing to an unknown path is rejected with 400.
	bad := serveSimpleContentRequest(h, http.MethodPut, "/routines/global-prompt", map[string]any{
		"content":  "x",
		"filePath": filepath.Join(home, "evil", "prompt.md"),
	})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("writing unknown path should 400, got %d %s", bad.Code, bad.Body.String())
	}

	// A path with ".." segments that NORMALIZES to a known candidate must be
	// accepted: the allowlist check runs on filepath.Abs(target) (which cleans
	// "..", matching the TS resolve(targetPath) normalization in
	// server/routes/routines.ts), not on the raw string. Build the raw string with
	// explicit separators (not filepath.Join, which would pre-clean it) so the
	// handler's own normalization is what gets exercised.
	traversalToCandidate := filepath.Join(home, ".claude") + string(filepath.Separator) + ".." + string(filepath.Separator) + ".claude" + string(filepath.Separator) + "CLAUDE.md"
	okTraversal := serveSimpleContentRequest(h, http.MethodPut, "/routines/global-prompt", map[string]any{
		"content":  "normalized\n",
		"filePath": traversalToCandidate,
	})
	if okTraversal.Code != http.StatusOK {
		t.Fatalf("'..'-path normalizing to a candidate should be accepted, got %d %s", okTraversal.Code, okTraversal.Body.String())
	}

	// A path that contains a candidate-looking prefix but ".."-escapes out of the
	// allowlist must be rejected (no traversal write outside the known paths).
	traversalEscape := filepath.Join(home, ".claude", "CLAUDE.md") + string(filepath.Separator) + ".." + string(filepath.Separator) + ".." + string(filepath.Separator) + "evil.md"
	badTraversal := serveSimpleContentRequest(h, http.MethodPut, "/routines/global-prompt", map[string]any{
		"content":  "x",
		"filePath": traversalEscape,
	})
	if badTraversal.Code != http.StatusBadRequest {
		t.Fatalf("'..'-escaping path should 400, got %d %s", badTraversal.Code, badTraversal.Body.String())
	}

	// Mirror TS `if (typeof body.content !== "string") throw 400 "content must be
	// a string"`: a missing or non-string content must reject, not silently write
	// an empty global prompt file.
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing content", map[string]any{"filePath": filepath.Join(home, ".claude", "CLAUDE.md")}},
		{"numeric content", map[string]any{"content": 123}},
		{"null content", map[string]any{"content": nil}},
		{"boolean content", map[string]any{"content": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := serveSimpleContentRequest(h, http.MethodPut, "/routines/global-prompt", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s should be 400, got %d body=%s", tc.name, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "content must be a string") {
				t.Fatalf("%s should return the content-string error, got %s", tc.name, w.Body.String())
			}
		})
	}
}
