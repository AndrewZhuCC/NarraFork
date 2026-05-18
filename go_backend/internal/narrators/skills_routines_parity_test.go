package narrators

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandsEndpointIncludesMergedProjectSkills(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeNarratorSkillFixture(t, filepath.Join(home, ".narrafork", "skills", "shared"), "shared", "Global shared", "global")
	writeNarratorSkillFixture(t, filepath.Join(project, ".narrafork", "skills", "shared-project"), "shared", "Project shared", "project")
	writeNarratorSkillFixture(t, filepath.Join(project, ".narrafork", "skills", "only-project"), "only-project", "Only project", "project")

	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	ensureSlashCommandTables(t, sqlDB)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, chapter_settings, git_path) VALUES ('p1', '{}', ?)`, project); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id) VALUES ('c1', 'p1')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE narrators SET chapter_id = 'c1' WHERE id = ?`, narratorID); err != nil {
		t.Fatalf("attach narrator: %v", err)
	}

	w := serveNarratorHandlerRequest(sqlDB, http.MethodGet, "/narrators/"+narratorID+"/commands")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode commands: %v", err)
	}
	byName := map[string]map[string]any{}
	for _, skill := range body.Skills {
		byName[stringValue(skill["name"])] = skill
	}
	if byName["shared"] == nil || byName["shared"]["source"] != "project" || byName["shared"]["description"] != "Project shared" {
		t.Fatalf("project skill should override global in slash menu: %#v", body.Skills)
	}
	if byName["only-project"] == nil || byName["only-project"]["source"] != "project" {
		t.Fatalf("project-only skill missing from slash menu: %#v", body.Skills)
	}
}

func TestGlobalSkillSummariesReadLargeFilesBounded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".narrafork", "skills", "huge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	large := "A bounded skill summary\n" + strings.Repeat("x", 1024*1024+1024)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(large), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	items := loadGlobalSkillSummaries()
	if len(items) != 1 || items[0]["name"] != "huge" {
		t.Fatalf("unexpected skill summaries: %#v", items)
	}
	if items[0]["description"] != "A bounded skill summary" || items[0]["truncated"] != true {
		t.Fatalf("expected bounded/truncated skill summary, got %#v", items[0])
	}
}

func TestAgentSystemPromptInjectsProjectAndGlobalInstructions(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(cwd, "AGENT.md"), []byte("project guidance"), 0o644); err != nil {
		t.Fatalf("write project prompt: %v", err)
	}
	globalDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("mkdir global prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "AGENT.md"), []byte("global guidance"), 0o644); err != nil {
		t.Fatalf("write global prompt: %v", err)
	}

	prompt := (&Service{}).agentSystemPrompt(context.Background(), Narrator{Cwd: &cwd})
	for _, want := range []string{"## Current Working Directory", cwd, "## Project Instructions", "project guidance", "## Global Instructions", "global guidance"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestAgentSystemPromptReportsMissingCwdFallback(t *testing.T) {
	prompt := (&Service{}).agentSystemPrompt(context.Background(), Narrator{})
	if !strings.Contains(prompt, "No narrator working directory is available") {
		t.Fatalf("expected explicit missing-cwd fallback, got:\n%s", prompt)
	}
}

func TestStandaloneSkillRootWalksUpToGitRoot(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir git marker: %v", err)
	}
	subdir := filepath.Join(repo, "nested", "pkg")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	ensureSlashCommandTables(t, sqlDB)
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	if _, err := sqlDB.Exec(`UPDATE narrators SET cwd = ? WHERE id = ?`, subdir, narratorID); err != nil {
		t.Fatalf("set cwd: %v", err)
	}
	root, err := (&Service{DB: sqlDB}).skillRootForNarrator(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("skill root: %v", err)
	}
	if root != repo {
		t.Fatalf("skill root = %q, want git root %q", root, repo)
	}
}

func writeNarratorSkillFixture(t *testing.T, dir, name, description, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	md := "---\nname: \"" + name + "\"\ndescription: \"" + description + "\"\n---\n\n" + content + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}
