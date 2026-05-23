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
