package misccompat

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestCustomSubagentsUseFilesystemStorage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	list, err := listCustomSubagentDefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list for missing filesystem directory, got %#v", list)
	}

	createBody := `{"name":"Security Reviewer","description":"Finds security bugs","toolAccess":"custom","customTools":["Read","Grep"],"defaultModel":"sonnet","prompt":"Review for vulnerabilities."}`
	created, err := createCustomSubagentDef(jsonRequest(t, http.MethodPost, createBody))
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Security Reviewer" || created.ToolAccess != "custom" || created.Prompt != "Review for vulnerabilities." {
		t.Fatalf("unexpected create response: %#v", created)
	}

	root, err := customSubagentsRoot()
	if err != nil {
		t.Fatal(err)
	}
	createdPath := filepath.Join(root, "security-reviewer", customSubagentFileName)
	if _, err := os.Stat(createdPath); err != nil {
		t.Fatalf("expected SUBAGENT.md at %s: %v", createdPath, err)
	}

	got, err := loadCustomSubagentDef("Security Reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultModel != "sonnet" {
		t.Fatalf("expected defaultModel from filesystem, got %#v", got)
	}

	updateBody := `{"name":"Security Audit","description":"Audit code","toolAccess":"general","prompt":"Audit thoroughly."}`
	updated, err := updateCustomSubagentDef(jsonRequest(t, http.MethodPut, updateBody), "Security Reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Security Audit" || updated.ToolAccess != "general" {
		t.Fatalf("unexpected update response: %#v", updated)
	}
	if _, err := os.Stat(filepath.Join(root, "security-reviewer")); !os.IsNotExist(err) {
		t.Fatalf("old directory still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "security-audit", customSubagentFileName)); err != nil {
		t.Fatalf("expected renamed SUBAGENT.md: %v", err)
	}

	if err := deleteCustomSubagentDef("Security Audit"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "security-audit")); !os.IsNotExist(err) {
		t.Fatalf("deleted directory still exists or stat failed unexpectedly: %v", err)
	}
}

func TestCustomSubagentRoutesMatchTSContract(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	router := chi.NewRouter()
	h := NewHandler(nil)
	router.Route("/api/custom-subagents", h.RegisterCustomSubagents)

	status, body := requestCustomSubagentRoute(t, router, http.MethodGet, "/api/custom-subagents", "")
	if status != http.StatusOK || body != "[]" {
		t.Fatalf("list missing dir = (%d, %s), want 200 []", status, body)
	}

	status, body = requestCustomSubagentRoute(t, router, http.MethodPost, "/api/custom-subagents", `{"name":"Docs Helper","description":"Helps docs","toolAccess":"custom","customTools":["Read","Glob"],"defaultModel":"haiku","prompt":"Write docs."}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d body = %s, want 201", status, body)
	}
	created := decodeObject(t, body)
	if created["name"] != "Docs Helper" || created["toolAccess"] != "custom" || created["prompt"] != "Write docs." {
		t.Fatalf("unexpected create body: %#v", created)
	}
	if _, ok := created["location"]; ok {
		t.Fatalf("route response must not expose location: %#v", created)
	}

	status, body = requestCustomSubagentRoute(t, router, http.MethodGet, "/api/custom-subagents/docs-helper", "")
	if status != http.StatusOK {
		t.Fatalf("get status = %d body = %s, want 200", status, body)
	}
	got := decodeObject(t, body)
	if got["defaultModel"] != "haiku" {
		t.Fatalf("unexpected get body: %#v", got)
	}

	status, body = requestCustomSubagentRoute(t, router, http.MethodPut, "/api/custom-subagents/docs-helper", `{"name":"Docs Auditor","description":"Audits docs","toolAccess":"general","prompt":"Audit docs."}`)
	if status != http.StatusOK {
		t.Fatalf("update status = %d body = %s, want 200", status, body)
	}
	updated := decodeObject(t, body)
	if updated["name"] != "Docs Auditor" || updated["toolAccess"] != "general" || updated["prompt"] != "Audit docs." {
		t.Fatalf("unexpected update body: %#v", updated)
	}

	status, body = requestCustomSubagentRoute(t, router, http.MethodDelete, "/api/custom-subagents/docs-auditor", "")
	if status != http.StatusOK {
		t.Fatalf("delete status = %d body = %s, want 200", status, body)
	}
	deleted := decodeObject(t, body)
	if deleted["ok"] != true {
		t.Fatalf("unexpected delete body: %#v", deleted)
	}

	status, _ = requestCustomSubagentRoute(t, router, http.MethodGet, "/api/custom-subagents/docs-auditor", "")
	if status != http.StatusNotFound {
		t.Fatalf("get deleted status = %d, want 404", status)
	}
}

func TestCustomSubagentFileReadIsBounded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, err := customSubagentsRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "huge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := append([]byte("---\nname: Huge\ndescription: Huge prompt\n---\n\n"), bytes.Repeat([]byte("x"), maxCustomSubagentFileBytes+1024)...)
	if err := os.WriteFile(filepath.Join(dir, customSubagentFileName), content, 0o644); err != nil {
		t.Fatal(err)
	}

	def, err := loadCustomSubagentDef("huge")
	if err != nil {
		t.Fatal(err)
	}
	if !def.PromptTruncated || !strings.Contains(def.Prompt, "[truncated]") {
		t.Fatalf("expected truncated custom subagent prompt, got %#v", def)
	}
	if len(def.Prompt) > maxCustomSubagentFileBytes+64 {
		t.Fatalf("custom subagent prompt was not bounded: len=%d", len(def.Prompt))
	}
	response := customSubagentResponse(def)
	if response["promptTruncated"] != true {
		t.Fatalf("response should expose promptTruncated: %#v", response)
	}
}

func TestCustomSubagentLoadsTSCompatibleMarkdown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, err := customSubagentsRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "docs-helper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: Docs Helper\ndescription: Helps with docs\ntoolAccess: custom\ncustomTools: [Read, Glob]\ndefaultModel: haiku\n---\n\nWrite documentation.\n"
	if err := os.WriteFile(filepath.Join(dir, customSubagentFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	def, err := loadCustomSubagentDef("docs-helper")
	if err != nil {
		t.Fatal(err)
	}
	if def.Name != "Docs Helper" || def.Description != "Helps with docs" || def.ToolAccess != "custom" || def.DefaultModel != "haiku" {
		t.Fatalf("unexpected def: %#v", def)
	}
	if len(def.CustomTools) != 2 || def.CustomTools[0] != "Read" || def.CustomTools[1] != "Glob" {
		t.Fatalf("unexpected custom tools: %#v", def.CustomTools)
	}
	if def.Prompt != "Write documentation." {
		t.Fatalf("unexpected prompt: %q", def.Prompt)
	}
}

func jsonRequest(t *testing.T, method, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, "/api/custom-subagents", strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func requestCustomSubagentRoute(t *testing.T, router http.Handler, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	return res.Code, strings.TrimSpace(res.Body.String())
}

func decodeObject(t *testing.T, body string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("decode object: %v; body = %s", err, body)
	}
	return obj
}
