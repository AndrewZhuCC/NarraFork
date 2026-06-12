package simplecontent

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/config"
)

func TestSkillFileReadsAreBounded(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")
	largeBody := bytes.Repeat([]byte("x"), maxSkillMarkdownBytes+1024)
	content := append([]byte("---\nname: huge\ndescription: Huge skill\n---\n\n"), largeBody...)
	if err := os.WriteFile(skillPath, content, 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	skill, err := loadSkillFile(skillPath, dir, false)
	if err != nil {
		t.Fatalf("load skill: %v", err)
	}
	if skill["truncated"] != true || !strings.Contains(skill["content"].(string), "[truncated]") {
		t.Fatalf("expected truncated skill content, got %#v", skill)
	}
}

// Wave 293: Go global skill create must mirror the TypeScript project skill Zod
// bounds — name max 100, description max 2000, content max 200000 — and reject
// out-of-range values with 400.
func TestCreateGlobalSkillValidatesZodBoundsLikeTypeScript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := NewHandler()
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"overlong name", map[string]any{"name": strings.Repeat("x", 101), "description": "ok"}},
		{"overlong description", map[string]any{"name": "Skill", "description": strings.Repeat("d", 2001)}},
		{"overlong content", map[string]any{"name": "Skill", "description": "ok", "content": strings.Repeat("c", 200001)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := serveSimpleSkillsRequest(h, http.MethodPost, "/skills/global", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("create global skill %q status = %d body=%s, want 400", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestSkillCompanionFileReadsAreBounded(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: demo\ndescription: Demo\n---\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	largePath := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(largePath, bytes.Repeat([]byte("x"), maxSkillCompanionFileBytes+1024), 0o644); err != nil {
		t.Fatalf("write companion: %v", err)
	}
	text, err := readSkillCompanionFile(skillPath, "large.txt")
	if err != nil {
		t.Fatalf("read companion: %v", err)
	}
	if !strings.Contains(text, "[truncated]") || len(text) > maxSkillCompanionFileBytes+64 {
		t.Fatalf("companion content was not bounded: len=%d", len(text))
	}
}

func TestSkillToolOutputUsesProjectOverrideAndCompanionFilePaths(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkillFixture(t, filepath.Join(home, ".narrafork", "skills", "demo"), "demo", "Global demo", "global instructions", nil)
	writeSkillFixture(t, filepath.Join(project, ".narrafork", "skills", "demo"), "demo", "Project demo", "project instructions", map[string]string{"notes/guide.md": "guide"})

	out := SkillToolOutput("demo", "extra args", project)
	if !strings.Contains(out, "project instructions") || strings.Contains(out, "global instructions") {
		t.Fatalf("project skill should override global skill, got:\n%s", out)
	}
	if !strings.Contains(out, "<skill_args>extra args</skill_args>") {
		t.Fatalf("skill args should be emitted in TS-compatible XML form, got:\n%s", out)
	}
	wantFile := filepath.Join(project, ".narrafork", "skills", "demo", "notes", "guide.md")
	if !strings.Contains(out, "Base directory for this skill:") || !strings.Contains(out, wantFile) {
		t.Fatalf("expected absolute companion file path %q in output:\n%s", wantFile, out)
	}
}

func TestSlashSkillSummariesMergeProjectAndGlobalSources(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkillFixture(t, filepath.Join(home, ".narrafork", "skills", "global"), "global", "Global skill", "global", nil)
	writeSkillFixture(t, filepath.Join(project, ".narrafork", "skills", "project"), "project", "Project skill", "project", nil)
	writeSkillFixture(t, filepath.Join(project, ".narrafork", "skills", "disabled"), "disabled", "Disabled skill", "disabled", nil)
	if err := os.Rename(filepath.Join(project, ".narrafork", "skills", "disabled", "SKILL.md"), filepath.Join(project, ".narrafork", "skills", "disabled", "SKILL.md.disabled")); err != nil {
		t.Fatalf("disable skill: %v", err)
	}

	summaries := SlashSkillSummaries(project)
	byName := map[string]map[string]any{}
	for _, item := range summaries {
		byName[stringValue(item["name"])] = item
	}
	if byName["global"] == nil || byName["global"]["source"] != "global" {
		t.Fatalf("missing global summary/source: %#v", summaries)
	}
	if byName["project"] == nil || byName["project"]["source"] != "project" {
		t.Fatalf("missing project summary/source: %#v", summaries)
	}
	if byName["disabled"] != nil {
		t.Fatalf("disabled skills should be hidden from slash summaries: %#v", summaries)
	}
}

func TestReadSkillCompanionFileRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	writeSkillFixture(t, dir, "demo", "Demo", "content", map[string]string{"safe.txt": "safe"})
	content, err := readSkillCompanionFile(filepath.Join(dir, "SKILL.md"), "safe.txt")
	if err != nil || content != "safe" {
		t.Fatalf("read safe companion file: content=%q err=%v", content, err)
	}
	if _, err := readSkillCompanionFile(filepath.Join(dir, "SKILL.md"), "../escape.txt"); err == nil {
		t.Fatalf("expected traversal rejection")
	}
}

func TestProjectRoutinesWithoutDatabaseExposeExplicitFallback(t *testing.T) {
	handler := NewHandler()
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{method: http.MethodGet, path: "/routines/project/p1"},
		{method: http.MethodPost, path: "/routines/project/p1/terminal/toggle", body: map[string]any{"action": "disable"}},
	} {
		w := serveSimpleContentRequest(handler, tc.method, tc.path, tc.body)
		assertSimpleContentFallback(t, w, "project.routines")
	}
}

func TestProjectSkillsWithoutDatabaseExposeExplicitFallback(t *testing.T) {
	handler := NewHandler()
	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/skills/?projectId=p1"},
		{method: http.MethodGet, path: "/skills/demo?projectId=p1"},
		{method: http.MethodGet, path: "/skills/demo/files/SKILL.md?projectId=p1"},
	} {
		w := serveSimpleSkillsRequest(handler, tc.method, tc.path, nil)
		assertSimpleContentFallback(t, w, "project.skills")
	}
}

func TestGlobalSkillCRUDMatchesTSStatusAndSideEffects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handler := NewHandler()

	create := serveSimpleSkillsRequest(handler, http.MethodPost, "/skills/global", map[string]any{"name": "Contract Skill", "description": "Helps parity", "content": "Initial content."})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created["name"] != "Contract Skill" || created["content"] != "Initial content." {
		t.Fatalf("unexpected create body: %#v", created)
	}

	duplicate := serveSimpleSkillsRequest(handler, http.MethodPost, "/skills/global", map[string]any{"name": "Contract Skill", "description": "Duplicate", "content": "Duplicate"})
	if duplicate.Code != http.StatusBadRequest {
		t.Fatalf("duplicate create status = %d body=%s", duplicate.Code, duplicate.Body.String())
	}
	missingToggleBody := serveSimpleSkillsRequest(handler, http.MethodPost, "/skills/global/Contract%20Skill/toggle", map[string]any{})
	if missingToggleBody.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled toggle status = %d body=%s", missingToggleBody.Code, missingToggleBody.Body.String())
	}

	disable := serveSimpleSkillsRequest(handler, http.MethodPost, "/skills/global/Contract%20Skill/toggle", map[string]any{"enabled": false})
	if disable.Code != http.StatusOK {
		t.Fatalf("disable status = %d body=%s", disable.Code, disable.Body.String())
	}
	var disabled map[string]any
	if err := json.Unmarshal(disable.Body.Bytes(), &disabled); err != nil {
		t.Fatalf("decode disable: %v", err)
	}
	if disabled["disabled"] != true {
		t.Fatalf("expected disabled skill, got %#v", disabled)
	}

	missingUpdate := serveSimpleSkillsRequest(handler, http.MethodPut, "/skills/global/missing", map[string]any{"name": "Missing", "description": "Missing", "content": "Missing"})
	if missingUpdate.Code != http.StatusNotFound {
		t.Fatalf("missing update status = %d body=%s", missingUpdate.Code, missingUpdate.Body.String())
	}
	missingDelete := serveSimpleSkillsRequest(handler, http.MethodDelete, "/skills/global/missing", map[string]any{})
	if missingDelete.Code != http.StatusNotFound {
		t.Fatalf("missing delete status = %d body=%s", missingDelete.Code, missingDelete.Body.String())
	}

	update := serveSimpleSkillsRequest(handler, http.MethodPut, "/skills/global/Contract%20Skill", map[string]any{"name": "Contract Skill Updated", "description": "Updated", "content": "Updated content."})
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", update.Code, update.Body.String())
	}
	oldDetail := serveSimpleSkillsRequest(handler, http.MethodGet, "/skills/global/Contract%20Skill", nil)
	if oldDetail.Code != http.StatusNotFound {
		t.Fatalf("old detail after rename status = %d body=%s", oldDetail.Code, oldDetail.Body.String())
	}
	newDetail := serveSimpleSkillsRequest(handler, http.MethodGet, "/skills/global/Contract%20Skill%20Updated", nil)
	if newDetail.Code != http.StatusOK {
		t.Fatalf("new detail after rename status = %d body=%s", newDetail.Code, newDetail.Body.String())
	}

	deleted := serveSimpleSkillsRequest(handler, http.MethodDelete, "/skills/global/Contract%20Skill%20Updated", map[string]any{})
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", deleted.Code, deleted.Body.String())
	}
	afterDelete := serveSimpleSkillsRequest(handler, http.MethodGet, "/skills/global/Contract%20Skill%20Updated", nil)
	if afterDelete.Code != http.StatusNotFound {
		t.Fatalf("detail after delete status = %d body=%s", afterDelete.Code, afterDelete.Body.String())
	}
}

func assertSimpleContentFallback(t *testing.T, w *httptest.ResponseRecorder, feature string) {
	t.Helper()
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 fallback, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode fallback: %v", err)
	}
	if body["ok"] != false || body["code"] != "FEATURE_DISABLED" || body["feature"] != feature || body["supported"] != false || body["fallback"] != true {
		t.Fatalf("unexpected fallback payload: %#v", body)
	}
	reason, _ := body["reason"].(string)
	if reason == "" || body["error"] != reason || body["message"] != reason {
		t.Fatalf("fallback should expose reason/error/message, got %#v", body)
	}
}

func TestProjectRoutineStatusIncludesOverrideAndGlobalEnabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := store.Patch(map[string]any{"routines": map[string]any{"enabledRoutines": []any{"terminal"}, "disabledRoutines": []any{}}}); err != nil {
		t.Fatalf("patch routines: %v", err)
	}
	sqlDB := openSimpleContentTestDB(t)
	defer sqlDB.Close()
	settings := `{"routines":{"disabledRoutines":["terminal"],"enabledRoutines":["browser"]}}`
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, chapter_settings, git_path, updated_at) VALUES ('p1', ?, '', 'now')`, settings); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	w := serveSimpleContentRequest(NewHandlerWithDB(store, sqlDB), http.MethodGet, "/routines/project/p1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Routines []map[string]any `json:"routines"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode routines: %v", err)
	}
	byID := map[string]map[string]any{}
	for _, routine := range body.Routines {
		byID[stringValue(routine["id"])] = routine
	}
	if byID["terminal"]["enabled"] != false || byID["terminal"]["globalEnabled"] != true || byID["terminal"]["override"] != "disabled" {
		t.Fatalf("terminal override mismatch: %#v", byID["terminal"])
	}
	if byID["browser"]["enabled"] != true || byID["browser"]["globalEnabled"] != false || byID["browser"]["override"] != "enabled" {
		t.Fatalf("browser override mismatch: %#v", byID["browser"])
	}
}

func writeSkillFixture(t *testing.T, dir, name, description, content string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	md := "---\nname: \"" + name + "\"\ndescription: \"" + description + "\"\n---\n\n" + content + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	for rel, body := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir companion: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write companion: %v", err)
		}
	}
}

func openSimpleContentTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY, chapter_settings TEXT, git_path TEXT, updated_at TEXT)`); err != nil {
		t.Fatalf("create projects: %v", err)
	}
	return sqlDB
}

func serveSimpleContentRequest(h *Handler, method, path string, body any) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/routines", h.RegisterRoutines)
	return serveSimpleRequest(r, method, path, body)
}

func serveSimpleSkillsRequest(h *Handler, method, path string, body any) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/skills", h.RegisterSkills)
	return serveSimpleRequest(r, method, path, body)
}

func serveSimpleRequest(r *chi.Mux, method, path string, body any) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		data, _ := json.Marshal(body)
		reader = strings.NewReader(string(data))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func serveSimpleRoutinesRequest(h *Handler, method, path string, body any) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/routines", h.RegisterRoutines)
	return serveSimpleRequest(r, method, path, body)
}

// Wave 307: PUT /global-prompt with no filePath must write to the first *existing*
// candidate (~/.claude/CLAUDE.md when ~/.agents/AGENT.md is absent), matching the
// TypeScript write path, instead of always defaulting to ~/.agents/AGENT.md.
func TestPutGlobalPromptDefaultsToFirstExistingCandidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	agentPath := filepath.Join(home, ".agents", "AGENT.md")
	claudePath := filepath.Join(home, ".claude", "CLAUDE.md")

	// Seed only the second candidate (CLAUDE.md); leave AGENT.md absent.
	if err := os.MkdirAll(filepath.Dir(claudePath), 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}
	if err := os.WriteFile(claudePath, []byte("old claude content\n"), 0o644); err != nil {
		t.Fatalf("seed claude file: %v", err)
	}

	handler := NewHandler()
	w := serveSimpleRoutinesRequest(handler, http.MethodPut, "/routines/global-prompt", map[string]any{"content": "updated content"})
	if w.Code != http.StatusOK {
		t.Fatalf("put global prompt status = %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode put response: %v", err)
	}
	if resp["filePath"] != claudePath {
		t.Fatalf("expected write to existing CLAUDE.md %q, got %#v", claudePath, resp["filePath"])
	}
	got, err := os.ReadFile(claudePath)
	if err != nil || string(got) != "updated content" {
		t.Fatalf("CLAUDE.md not updated in place: content=%q err=%v", string(got), err)
	}
	if _, err := os.Stat(agentPath); err == nil {
		t.Fatalf("AGENT.md should not be created when CLAUDE.md exists")
	}
}

// When no candidate exists, the write falls back to the first candidate (~/.agents/AGENT.md).
func TestPutGlobalPromptFallsBackToFirstCandidateWhenNoneExist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agentPath := filepath.Join(home, ".agents", "AGENT.md")

	handler := NewHandler()
	w := serveSimpleRoutinesRequest(handler, http.MethodPut, "/routines/global-prompt", map[string]any{"content": "fresh"})
	if w.Code != http.StatusOK {
		t.Fatalf("put global prompt status = %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode put response: %v", err)
	}
	if resp["filePath"] != agentPath {
		t.Fatalf("expected fallback to AGENT.md %q, got %#v", agentPath, resp["filePath"])
	}
}

// Wave 309: Go performs a live filesystem scan on each skill list (no persistent
// skill_directory_caches DB cache like TypeScript), so a skill added after a first
// scan is immediately visible on the next scan with no cache invalidation step.
func TestProjectSkillScanIsLiveAndReflectsNewlyAddedSkills(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeSkillFixture(t, filepath.Join(project, ".narrafork", "skills", "first"), "first", "First skill", "first", nil)

	first := loadProjectSkills(project)
	if len(first) != 1 || stringValue(first[0]["name"]) != "first" {
		t.Fatalf("expected one initial skill, got %#v", first)
	}

	// Add a second skill after the first scan; a live scan must pick it up
	// immediately without any cache-invalidation step.
	writeSkillFixture(t, filepath.Join(project, ".narrafork", "skills", "second"), "second", "Second skill", "second", nil)
	second := loadProjectSkills(project)
	names := map[string]bool{}
	for _, item := range second {
		names[stringValue(item["name"])] = true
	}
	if len(second) != 2 || !names["first"] || !names["second"] {
		t.Fatalf("live re-scan should reflect the newly added skill, got %#v", second)
	}

	// Removing a skill is likewise reflected on the next scan (no stale cache row).
	if err := os.RemoveAll(filepath.Join(project, ".narrafork", "skills", "first")); err != nil {
		t.Fatalf("remove first skill: %v", err)
	}
	third := loadProjectSkills(project)
	if len(third) != 1 || stringValue(third[0]["name"]) != "second" {
		t.Fatalf("live re-scan should drop the removed skill, got %#v", third)
	}
}

// serveProjectSkillsRequest mounts the skills routes against a DB-backed handler so
// project-scoped create/update/delete routes resolve a real project git path.
func serveProjectSkillsRequest(h *Handler, method, path string, body any) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Route("/skills", h.RegisterSkills)
	return serveSimpleRequest(r, method, path, body)
}

// Wave 316: Go must expose the project-skill write routes (POST/PUT/DELETE /api/skills)
// with the same Zod bounds, duplicate handling, status codes, and path safety as the
// TypeScript skill routes/service.
func TestProjectSkillWriteRoutesMatchTSStatusAndSideEffects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	db := openSimpleContentTestDB(t)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO projects (id, chapter_settings, git_path, updated_at) VALUES ('p1', '{}', ?, 'now')`, project); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	handler := NewHandlerWithDB(nil, db)

	// Create returns 201 and the skill is readable through list/get.
	create := serveProjectSkillsRequest(handler, http.MethodPost, "/skills?projectId=p1", map[string]any{"name": "Project Skill", "description": "Helps project parity", "content": "Initial project content."})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created["name"] != "Project Skill" || created["content"] != "Initial project content." {
		t.Fatalf("unexpected create body: %#v", created)
	}
	skillFile := filepath.Join(project, ".narrafork", "skills", "project-skill", "SKILL.md")
	if _, err := os.Stat(skillFile); err != nil {
		t.Fatalf("expected SKILL.md on disk: %v", err)
	}
	get := serveProjectSkillsRequest(handler, http.MethodGet, "/skills/Project%20Skill?projectId=p1", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get after create status = %d body=%s", get.Code, get.Body.String())
	}

	// Duplicate create is rejected.
	duplicate := serveProjectSkillsRequest(handler, http.MethodPost, "/skills?projectId=p1", map[string]any{"name": "Project Skill", "description": "Duplicate", "content": "dup"})
	if duplicate.Code != http.StatusBadRequest {
		t.Fatalf("duplicate create status = %d body=%s", duplicate.Code, duplicate.Body.String())
	}

	// Zod bounds: name>100, description>2000, content>200000 are rejected.
	overName := serveProjectSkillsRequest(handler, http.MethodPost, "/skills?projectId=p1", map[string]any{"name": strings.Repeat("a", 101), "description": "ok", "content": "ok"})
	if overName.Code != http.StatusBadRequest {
		t.Fatalf("overlong name status = %d", overName.Code)
	}
	overDesc := serveProjectSkillsRequest(handler, http.MethodPost, "/skills?projectId=p1", map[string]any{"name": "Bound Name", "description": strings.Repeat("d", 2001), "content": "ok"})
	if overDesc.Code != http.StatusBadRequest {
		t.Fatalf("overlong description status = %d", overDesc.Code)
	}
	overContent := serveProjectSkillsRequest(handler, http.MethodPost, "/skills?projectId=p1", map[string]any{"name": "Bound Name", "description": "ok", "content": strings.Repeat("c", 200001)})
	if overContent.Code != http.StatusBadRequest {
		t.Fatalf("overlong content status = %d", overContent.Code)
	}

	// A name that sanitizes to empty is rejected as an invalid skill name.
	invalidName := serveProjectSkillsRequest(handler, http.MethodPost, "/skills?projectId=p1", map[string]any{"name": "---", "description": "ok", "content": "ok"})
	if invalidName.Code != http.StatusBadRequest {
		t.Fatalf("invalid name status = %d body=%s", invalidName.Code, invalidName.Body.String())
	}

	// Update missing skill returns 404.
	missingUpdate := serveProjectSkillsRequest(handler, http.MethodPut, "/skills/missing?projectId=p1", map[string]any{"description": "x", "content": "x"})
	if missingUpdate.Code != http.StatusNotFound {
		t.Fatalf("missing update status = %d body=%s", missingUpdate.Code, missingUpdate.Body.String())
	}

	// Update succeeds and the new content is reflected.
	update := serveProjectSkillsRequest(handler, http.MethodPut, "/skills/Project%20Skill?projectId=p1", map[string]any{"description": "Updated description", "content": "Updated project content."})
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", update.Code, update.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(update.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated["content"] != "Updated project content." || updated["description"] != "Updated description" {
		t.Fatalf("unexpected update body: %#v", updated)
	}

	// Delete missing returns 404; delete existing returns ok and removes the directory.
	missingDelete := serveProjectSkillsRequest(handler, http.MethodDelete, "/skills/missing?projectId=p1", nil)
	if missingDelete.Code != http.StatusNotFound {
		t.Fatalf("missing delete status = %d body=%s", missingDelete.Code, missingDelete.Body.String())
	}
	deleted := serveProjectSkillsRequest(handler, http.MethodDelete, "/skills/Project%20Skill?projectId=p1", nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", deleted.Code, deleted.Body.String())
	}
	if _, err := os.Stat(filepath.Dir(skillFile)); !os.IsNotExist(err) {
		t.Fatalf("expected skill directory removed, stat err=%v", err)
	}
}

func TestProjectSkillWriteRoutesWithoutDatabaseExposeExplicitFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	handler := NewHandler()
	create := serveProjectSkillsRequest(handler, http.MethodPost, "/skills?projectId=p1", map[string]any{"name": "x", "description": "y", "content": "z"})
	assertSimpleContentFallback(t, create, "project.skills")
}

func TestSanitizeProjectSkillDirNameMatchesTS(t *testing.T) {
	cases := map[string]string{
		"Project Skill":   "project-skill",
		"  Hello World  ": "hello-world",
		"a@@@b":           "a-b",
		"---":             "",
		"中文 技能":           "中文-技能",
		"UPPER_case-1":    "upper_case-1",
	}
	for input, want := range cases {
		if got := sanitizeProjectSkillDirName(input); got != want {
			t.Fatalf("sanitizeProjectSkillDirName(%q) = %q, want %q", input, got, want)
		}
	}
}
