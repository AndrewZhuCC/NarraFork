package narrators

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/config"

	_ "modernc.org/sqlite"
)

func TestDefaultNarratorModelFallsBackToGoRuntimeDefault(t *testing.T) {
	service := &Service{}
	if got := service.defaultNarratorModel(); got != config.DefaultAgentModel {
		t.Fatalf("defaultNarratorModel = %q, want %q", got, config.DefaultAgentModel)
	}
}

func TestGoalCompletionEvidenceValid(t *testing.T) {
	if goalCompletionEvidenceValid("too short") {
		t.Fatalf("short evidence should not satisfy completion gate")
	}
	if !goalCompletionEvidenceValid("Verified tests passed and schema was not modified.") {
		t.Fatalf("concrete evidence should satisfy completion gate")
	}
}

func TestEndPipelineRejectsUnsupportedFormat(t *testing.T) {
	call := agentbasic.ToolCall{ID: "end-pipeline-1", Type: "function"}
	call.Function.Name = "EndPipeline"
	call.Function.Arguments = `{"format":"markdown"}`
	run, handled, err := (&Service{}).executeNarratorTool(context.Background(), "narrator-1", Narrator{}, nil, &agentPipelineState{}, call, nil)
	if err != nil || !handled {
		t.Fatalf("expected handled EndPipeline without service error, handled=%v err=%v", handled, err)
	}
	if run.Status != "error" || !strings.Contains(run.Output, "unsupported pipeline format") {
		t.Fatalf("expected unsupported format error, got status=%s output=%q", run.Status, run.Output)
	}
}

func TestNormalizeExitPlanAllowedPrompts(t *testing.T) {
	normalized, reason := normalizeExitPlanAllowedPrompts([]any{map[string]any{"tool": "Bash", "prompt": "run tests"}})
	if reason != "" || normalized == nil {
		t.Fatalf("expected valid allowedPrompts to normalize, normalized=%#v reason=%q", normalized, reason)
	}
	if _, reason := normalizeExitPlanAllowedPrompts([]any{map[string]any{"tool": "Edit", "prompt": "change code"}}); !strings.Contains(reason, "must be Bash") {
		t.Fatalf("expected unsupported tool rejection, got %q", reason)
	}
	if _, reason := normalizeExitPlanAllowedPrompts([]any{map[string]any{"tool": "Bash", "prompt": "run tests", "extra": true}}); !strings.Contains(reason, "unsupported property") {
		t.Fatalf("expected extra property rejection, got %q", reason)
	}
}

func TestAddGoalRejectsOversizedObjectiveBeforeDB(t *testing.T) {
	call := agentbasic.ToolCall{ID: "add-goal-1", Type: "function"}
	call.Function.Name = "AddGoal"
	call.Function.Arguments = `{"objective":"` + strings.Repeat("a", 4001) + `"}`
	run, handled, err := (&Service{}).executeNarratorTool(context.Background(), "narrator-1", Narrator{}, nil, &agentPipelineState{}, call, nil)
	if err != nil || !handled {
		t.Fatalf("expected handled AddGoal without DB, handled=%v err=%v", handled, err)
	}
	if !strings.Contains(run.Output, "at most 4000") {
		t.Fatalf("expected max length rejection, got %q", run.Output)
	}
}

func TestNormalizeReviewFindingsRejectsExtraProperties(t *testing.T) {
	_, err := normalizeReviewFindings([]any{map[string]any{"severity": "major", "message": "Fix it", "extra": true}})
	if err == nil || !strings.Contains(err.Error(), "unsupported property") {
		t.Fatalf("expected extra property rejection, got %v", err)
	}
}

func TestExitPlanTextValid(t *testing.T) {
	if exitPlanTextValid("short plan") {
		t.Fatalf("short plan should not satisfy exit-plan gate")
	}
	if !exitPlanTextValid("Implement the feature by updating the service, adding tests, and running validation.") {
		t.Fatalf("complete plan should satisfy exit-plan gate")
	}
}

func TestCreateStoresDangerReflectionLevelOverride(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narrator, err := service.Create(context.Background(), CreateInput{Model: "test-model", DangerReflectionOverride: "strict"})
	if err != nil {
		t.Fatalf("create narrator: %v", err)
	}
	loaded, err := service.Get(context.Background(), narrator.ID)
	if err != nil {
		t.Fatalf("load narrator: %v", err)
	}
	if loaded.DangerReflectionOverride != "strict" {
		t.Fatalf("dangerReflectionOverride = %q, want strict", loaded.DangerReflectionOverride)
	}
}

func TestPlanModeStoresPlanStateAndRestoresPermission(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	permission := "bypassPermissions"
	narrator, err := service.Create(context.Background(), CreateInput{Model: "test-model", PermissionMode: permission})
	if err != nil {
		t.Fatalf("create narrator: %v", err)
	}
	entered, err := service.setNarratorPlanMode(context.Background(), narrator.ID, true)
	if err != nil {
		t.Fatalf("enter plan mode: %v", err)
	}
	planFileID, _ := entered["planFileId"].(string)
	if planFileID == "" {
		t.Fatalf("expected planFileId in enter payload: %#v", entered)
	}
	inPlan, err := service.Get(context.Background(), narrator.ID)
	if err != nil {
		t.Fatalf("load in plan: %v", err)
	}
	if !inPlan.PlanMode || inPlan.PermissionMode == nil || *inPlan.PermissionMode != "readOnly" {
		t.Fatalf("expected readOnly plan mode, got plan=%v permission=%v", inPlan.PlanMode, inPlan.PermissionMode)
	}
	previousPermission := "<nil>"
	if inPlan.PreviousPermissionMode != nil {
		previousPermission = *inPlan.PreviousPermissionMode
	}
	if previousPermission != permission {
		t.Fatalf("previous permission = %q, want %q", previousPermission, permission)
	}
	if inPlan.PlanFileID == nil || *inPlan.PlanFileID != planFileID {
		t.Fatalf("planFileId = %v, want %q", inPlan.PlanFileID, planFileID)
	}
	planModeSoftDenyAsked.Store(narrator.ID, true)
	if _, err := service.setNarratorPlanMode(context.Background(), narrator.ID, false); err != nil {
		t.Fatalf("exit plan mode: %v", err)
	}
	exited, err := service.Get(context.Background(), narrator.ID)
	if err != nil {
		t.Fatalf("load exited: %v", err)
	}
	if exited.PlanMode || exited.PermissionMode == nil || *exited.PermissionMode != permission {
		t.Fatalf("expected restored permission, got plan=%v permission=%v", exited.PlanMode, exited.PermissionMode)
	}
	if exited.PreviousPermissionMode != nil || exited.PlanFileID != nil {
		t.Fatalf("expected cleared plan state, got previous=%v planFile=%v", exited.PreviousPermissionMode, exited.PlanFileID)
	}
	if _, ok := planModeSoftDenyAsked.Load(narrator.ID); ok {
		t.Fatalf("expected exiting plan mode to reset soft-deny prompt state")
	}
}

func TestCompleteExitPlanModeSkipsPlanCompactWithoutCompactAfter(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	start := true
	narrator, err := service.Create(context.Background(), CreateInput{Model: "test-model", PermissionMode: "default", StartInPlanMode: &start})
	if err != nil {
		t.Fatalf("create narrator: %v", err)
	}
	plan := "Implement the approved plan by changing the service, updating tests, and validating focused packages."
	payload, err := service.completeExitPlanModeTool(context.Background(), narrator.ID, map[string]any{"plan": plan})
	if err != nil {
		t.Fatalf("complete ExitPlanMode: %v", err)
	}
	compact, _ := payload["planCompact"].(map[string]any)
	if compact["skipped"] != true {
		t.Fatalf("expected plan compact skipped without compactAfter, payload=%#v", payload)
	}
	if payload["continueMode"] != "continue" || !strings.Contains(stringFromAny(payload["continuePrompt"]), "approved your plan") {
		t.Fatalf("expected non-compact continuation prompt, payload=%#v", payload)
	}
	var contextSummary sql.NullString
	if err := sqlDB.QueryRow(`SELECT context_summary FROM narrators WHERE id = ?`, narrator.ID).Scan(&contextSummary); err != nil {
		t.Fatalf("load context summary: %v", err)
	}
	if contextSummary.Valid && strings.TrimSpace(contextSummary.String) != "" {
		t.Fatalf("expected no plan compact summary without compactAfter, got %q", contextSummary.String)
	}
}

func TestExitPlanContinuePromptIncludesDiffAndCompactMode(t *testing.T) {
	diffPrompt := exitPlanContinuePrompt(false, "- old\n+ new", "en")
	if !strings.Contains(diffPrompt, "- old") || !strings.Contains(diffPrompt, "+ new") || !strings.Contains(diffPrompt, "edited your plan") {
		t.Fatalf("expected diff continuation prompt, got %q", diffPrompt)
	}
	compactPrompt := exitPlanContinuePrompt(true, "- ignored\n+ ignored", "en")
	if !strings.Contains(compactPrompt, "context has been reset") || strings.Contains(compactPrompt, "ignored") {
		t.Fatalf("expected compact continuation prompt without diff body, got %q", compactPrompt)
	}
}

func TestResolveExitPlanModeRejectsOversizedPlanFile(t *testing.T) {
	cwd := t.TempDir()
	planFileID := "oversized"
	planPath := filepath.Join(cwd, planFileRelativePath(planFileID))
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	if err := os.WriteFile(planPath, bytes.Repeat([]byte("x"), exitPlanFileMaxBytes+1), 0o644); err != nil {
		t.Fatalf("write oversized plan file: %v", err)
	}
	narrator := Narrator{Cwd: &cwd, PlanFileID: &planFileID}
	_, _, err := (&Service{}).resolveExitPlanModePlan(narrator, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected oversized plan error, got %v", err)
	}
}

func TestPlanModeRestrictsWriteEditToPlanFile(t *testing.T) {
	planFileID := "plan1234"
	narrator := Narrator{ID: "nar_plan_restrict", PlanMode: true, PlanFileID: &planFileID}
	cwd := t.TempDir()
	call := agentbasic.ToolCall{}
	call.Function.Name = "Write"
	call.Function.Arguments = `{"file_path":"src/main.go","content":"package main"}`
	blocked, output := planModeToolRestriction(cwd, narrator, call)
	if blocked {
		t.Fatalf("expected non-plan write to flow into soft-deny permission, output=%q", output)
	}
	decision, err := (&Service{DB: sqlDBForPlanRestrictionTest(t)}).resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve plan soft deny permission: %v", err)
	}
	if decision.Action != "ask" || !strings.Contains(decision.Reason, planFileRelativePath(planFileID)) {
		t.Fatalf("expected first non-plan write to ask with plan file hint, got %#v", decision)
	}
	decision, err = (&Service{DB: sqlDBForPlanRestrictionTest(t)}).resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve repeated plan soft deny permission: %v", err)
	}
	if decision.Action != "deny" || !strings.Contains(decision.Reason, planFileRelativePath(planFileID)) {
		t.Fatalf("expected repeated non-plan write denied with plan file hint, got %#v", decision)
	}
	call.Function.Arguments = `{"file_path":".narrafork/plan-plan1234.md","content":"implementation plan with details"}`
	blocked, output = planModeToolRestriction(cwd, narrator, call)
	if blocked {
		t.Fatalf("expected designated plan file write allowed, output=%q", output)
	}
}

func TestResolveToolPermissionReadOnlyAgentMatchesTSRestrictions(t *testing.T) {
	sqlDB := sqlDBForPlanRestrictionTest(t)
	service := &Service{DB: sqlDB}
	cwd := t.TempDir()
	outside := t.TempDir()
	readOnly := "readOnly"
	narrator := Narrator{ID: "nar_agent_perm", PermissionMode: &readOnly}
	call := agentbasic.ToolCall{}
	call.Function.Name = "Agent"

	call.Function.Arguments = `{"subagent_type":"general"}`
	decision, err := service.resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve general agent in readOnly: %v", err)
	}
	if decision.Action != "deny" {
		t.Fatalf("readOnly must deny general Agent subagents, got %#v", decision)
	}

	call.Function.Arguments = `{"subagent_type":"explore"}`
	decision, err = service.resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve explore agent in readOnly: %v", err)
	}
	if decision.Action != "allow" {
		t.Fatalf("readOnly should allow explore Agent inside cwd, got %#v", decision)
	}

	call.Function.Arguments = `{"subagent_type":"plan","workdir":"` + filepath.Join(cwd, "docs") + `"}`
	decision, err = service.resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve plan agent in readOnly inside cwd: %v", err)
	}
	if decision.Action != "allow" {
		t.Fatalf("readOnly should allow plan Agent inside cwd, got %#v", decision)
	}

	call.Function.Arguments = `{"subagent_type":"explore","workdir":"` + outside + `"}`
	decision, err = service.resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve explore agent outside cwd: %v", err)
	}
	if decision.Action != "ask" {
		t.Fatalf("readOnly explore Agent outside cwd should ask, got %#v", decision)
	}

	if _, err := sqlDB.Exec(`INSERT INTO narrator_blacklist_dirs (narrator_id, path, deny_level, enabled) VALUES (?, ?, 'denyWrite', 1)`, narrator.ID, outside); err != nil {
		t.Fatalf("insert blacklist: %v", err)
	}
	bypass := "bypassPermissions"
	narrator.PermissionMode = &bypass
	call.Function.Arguments = `{"subagent_type":"general","workdir":"` + outside + `"}`
	decision, err = service.resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve blacklisted general agent: %v", err)
	}
	if decision.Action != "deny" || !strings.Contains(decision.Reason, "blacklist") {
		t.Fatalf("denyWrite blacklist should deny write-capable Agent even in bypass, got %#v", decision)
	}
}

func TestResolveToolPermissionSendMatchesTSWithinScopeRank(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	cwd := t.TempDir()
	if _, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, created_at, updated_at
	) VALUES ('parent-send', 'primary', 'Parent', 'fresh', 'openai:test', 'default', 'idle', '[]', 0, ?, 'primary', '[]', 0, 0, 0, 'now', 'now')`, cwd); err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	parent, err := service.Get(context.Background(), "parent-send")
	if err != nil {
		t.Fatalf("load parent: %v", err)
	}
	sender, err := service.createSubagentNarrator(context.Background(), parent, "explore", "Sender", "openai:test", "", cwd, "sender", false)
	if err != nil {
		t.Fatalf("create sender: %v", err)
	}
	target, err := service.createSubagentNarrator(context.Background(), parent, "general", "Target", "openai:test", "", cwd, "target", false)
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	call := agentbasic.ToolCall{}
	call.Function.Name = "Send"
	call.Function.Arguments = `{"id":"` + target.ID + `","message":"hello"}`

	if _, err := sqlDB.Exec(`UPDATE narrators SET permission_mode = 'acceptEdits' WHERE id = ?`, target.ID); err != nil {
		t.Fatalf("raise target permission: %v", err)
	}
	sender, _ = service.Get(context.Background(), sender.ID)
	decision, err := service.resolveToolPermission(context.Background(), sender, cwd, call)
	if err != nil {
		t.Fatalf("resolve readOnly sender to acceptEdits target: %v", err)
	}
	if decision.Action != "deny" {
		t.Fatalf("readOnly Send to higher-permission target should deny, got %#v", decision)
	}

	if _, err := sqlDB.Exec(`UPDATE narrators SET permission_mode = 'readOnly' WHERE id = ?`, target.ID); err != nil {
		t.Fatalf("lower target permission: %v", err)
	}
	decision, err = service.resolveToolPermission(context.Background(), sender, cwd, call)
	if err != nil {
		t.Fatalf("resolve readOnly sender to readOnly target: %v", err)
	}
	if decision.Action != "allow" {
		t.Fatalf("readOnly Send within equal permission scope should allow, got %#v", decision)
	}

	if _, err := sqlDB.Exec(`UPDATE narrators SET permission_mode = 'default' WHERE id = ?`, sender.ID); err != nil {
		t.Fatalf("set sender default: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE narrators SET permission_mode = 'acceptEdits' WHERE id = ?`, target.ID); err != nil {
		t.Fatalf("raise target permission again: %v", err)
	}
	sender, _ = service.Get(context.Background(), sender.ID)
	decision, err = service.resolveToolPermission(context.Background(), sender, cwd, call)
	if err != nil {
		t.Fatalf("resolve default sender to acceptEdits target: %v", err)
	}
	if decision.Action != "ask" {
		t.Fatalf("default Send to higher-permission target should ask, got %#v", decision)
	}

	if _, err := sqlDB.Exec(`UPDATE narrators SET permission_mode = 'dontAsk' WHERE id = ?`, sender.ID); err != nil {
		t.Fatalf("set sender dontAsk: %v", err)
	}
	sender, _ = service.Get(context.Background(), sender.ID)
	decision, err = service.resolveToolPermission(context.Background(), sender, cwd, call)
	if err != nil {
		t.Fatalf("resolve dontAsk sender to acceptEdits target: %v", err)
	}
	if decision.Action != "deny" {
		t.Fatalf("dontAsk Send outside permission scope should deny, got %#v", decision)
	}
}

func TestResolveToolPermissionHardDeniesGitInternalWrites(t *testing.T) {
	sqlDB := sqlDBForPlanRestrictionTest(t)
	service := &Service{DB: sqlDB}
	cwd := t.TempDir()
	bypass := "bypassPermissions"
	narrator := Narrator{ID: "nar_git_protect", PermissionMode: &bypass}

	for _, tc := range []struct {
		name string
		args string
	}{
		{name: "Write", args: `{"file_path":".git/config","content":"bad"}`},
		{name: "Edit", args: `{"file_path":"` + filepath.Join(cwd, ".git", "config") + `","old_string":"a","new_string":"b"}`},
		{name: "Browser", args: `{"action":"screenshot","file_path":".git/screen.png"}`},
	} {
		call := agentbasic.ToolCall{}
		call.Function.Name = tc.name
		call.Function.Arguments = tc.args
		decision, err := service.resolveToolPermission(context.Background(), narrator, cwd, call)
		if err != nil {
			t.Fatalf("resolve %s protected path: %v", tc.name, err)
		}
		if decision.Action != "deny" || !strings.Contains(decision.Reason, ".git") {
			t.Fatalf("%s targeting .git should hard-deny even in bypass mode, got %#v", tc.name, decision)
		}
	}

	call := agentbasic.ToolCall{}
	call.Function.Name = "Browser"
	call.Function.Arguments = `{"action":"get_text","file_path":".git/ignored"}`
	decision, err := service.resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve non-screenshot Browser: %v", err)
	}
	if decision.Action != "allow" {
		t.Fatalf("non-screenshot Browser should not trip .git write hard-deny in bypass mode, got %#v", decision)
	}
}

func TestResolveToolPermissionHardDeniesBashGitInternalWrites(t *testing.T) {
	sqlDB := sqlDBForPlanRestrictionTest(t)
	service := &Service{DB: sqlDB}
	cwd := t.TempDir()
	bypass := "bypassPermissions"
	narrator := Narrator{ID: "nar_bash_git_protect", PermissionMode: &bypass}

	for _, command := range []string{
		"rm -rf .git",
		"shred .git/config",
		"printf bad > .git/config",
		"printf bad 2>.git/error.log",
	} {
		call := agentbasic.ToolCall{}
		call.Function.Name = "Bash"
		call.Function.Arguments = `{"command":"` + command + `"}`
		decision, err := service.resolveToolPermission(context.Background(), narrator, cwd, call)
		if err != nil {
			t.Fatalf("resolve Bash protected command %q: %v", command, err)
		}
		if decision.Action != "deny" || !strings.Contains(decision.Reason, ".git") {
			t.Fatalf("Bash command %q targeting .git should hard-deny even in bypass mode, got %#v", command, decision)
		}
	}

	call := agentbasic.ToolCall{}
	call.Function.Name = "Bash"
	call.Function.Arguments = `{"command":"rm -rf build"}`
	decision, err := service.resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve safe bypass Bash: %v", err)
	}
	if decision.Action != "allow" {
		t.Fatalf("non-.git Bash should still follow bypassPermissions, got %#v", decision)
	}
}

func TestResolveToolPermissionHardDeniesCatastrophicBashCommands(t *testing.T) {
	sqlDB := sqlDBForPlanRestrictionTest(t)
	service := &Service{DB: sqlDB}
	cwd := t.TempDir()
	bypass := "bypassPermissions"
	narrator := Narrator{ID: "nar_bash_catastrophic", PermissionMode: &bypass}

	for _, command := range []string{
		"rm -rf /",
		"rm -rf $HOME",
		"dd if=/dev/zero of=/dev/sda",
		"chmod -R 777 /etc",
		":(){ :|:& };:",
	} {
		call := agentbasic.ToolCall{}
		call.Function.Name = "Bash"
		call.Function.Arguments = `{"command":"` + command + `"}`
		decision, err := service.resolveToolPermission(context.Background(), narrator, cwd, call)
		if err != nil {
			t.Fatalf("resolve catastrophic Bash command %q: %v", command, err)
		}
		if decision.Action != "deny" || !strings.Contains(decision.Reason, "Catastrophic") {
			t.Fatalf("Bash command %q should hard-deny as catastrophic, got %#v", command, decision)
		}
	}

	call := agentbasic.ToolCall{}
	call.Function.Name = "Bash"
	call.Function.Arguments = `{"command":"rm -rf build"}`
	decision, err := service.resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve non-catastrophic Bash: %v", err)
	}
	if decision.Action != "allow" {
		t.Fatalf("non-catastrophic Bash should still follow bypassPermissions, got %#v", decision)
	}
}

func TestResolveToolPermissionHardDeniesBashStructuralProjectPaths(t *testing.T) {
	sqlDB := sqlDBForPlanRestrictionTest(t)
	if _, err := sqlDB.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY, git_path TEXT); CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT)`); err != nil {
		t.Fatalf("create project tables: %v", err)
	}
	projectRoot := t.TempDir()
	cwd := filepath.Join(projectRoot, ".worktrees", "chapter-a")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("create cwd: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, git_path) VALUES ('p1', ?)`, projectRoot); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id) VALUES ('c1', 'p1')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	service := &Service{DB: sqlDB}
	bypass := "bypassPermissions"
	chapterID := "c1"
	narrator := Narrator{ID: "nar_bash_structural_protect", ChapterID: &chapterID, PermissionMode: &bypass}

	for _, command := range []string{
		"rm -rf " + projectRoot,
		"rmdir " + filepath.Join(projectRoot, ".worktrees"),
		"rm -rf " + filepath.Dir(projectRoot),
	} {
		call := agentbasic.ToolCall{}
		call.Function.Name = "Bash"
		call.Function.Arguments = `{"command":"` + command + `"}`
		decision, err := service.resolveToolPermission(context.Background(), narrator, cwd, call)
		if err != nil {
			t.Fatalf("resolve Bash structural command %q: %v", command, err)
		}
		if decision.Action != "deny" || !strings.Contains(decision.Reason, "Destructive operation") {
			t.Fatalf("Bash command %q targeting structural path should hard-deny, got %#v", command, decision)
		}
	}

	workdirCall := agentbasic.ToolCall{}
	workdirCall.Function.Name = "Bash"
	workdirCall.Function.Arguments = `{"command":"rm -rf .","workdir":"` + projectRoot + `"}`
	decision, err := service.resolveToolPermission(context.Background(), narrator, t.TempDir(), workdirCall)
	if err != nil {
		t.Fatalf("resolve Bash structural command with workdir: %v", err)
	}
	if decision.Action != "deny" || !strings.Contains(decision.Reason, "project root") {
		t.Fatalf("Bash command targeting workdir project root should hard-deny, got %#v", decision)
	}

	call := agentbasic.ToolCall{}
	call.Function.Name = "Bash"
	call.Function.Arguments = `{"command":"rm -rf ` + filepath.Join(projectRoot, "build") + `"}`
	decision, err = service.resolveToolPermission(context.Background(), narrator, cwd, call)
	if err != nil {
		t.Fatalf("resolve non-structural Bash: %v", err)
	}
	if decision.Action != "allow" {
		t.Fatalf("non-structural Bash should still follow bypassPermissions, got %#v", decision)
	}
}

func TestResolveToolPermissionPathAwareFallbackMatchesTS(t *testing.T) {
	sqlDB := sqlDBForPlanRestrictionTest(t)
	service := &Service{DB: sqlDB}
	cwd := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	inside := filepath.Join(cwd, "inside.txt")
	defaultMode := "default"
	readOnly := "readOnly"
	acceptEdits := "acceptEdits"

	call := agentbasic.ToolCall{}
	call.Function.Name = "Read"
	call.Function.Arguments = `{"file_path":"` + inside + `"}`
	decision, err := service.resolveToolPermission(context.Background(), Narrator{ID: "nar_read_default", PermissionMode: &defaultMode}, cwd, call)
	if err != nil {
		t.Fatalf("resolve default internal Read: %v", err)
	}
	if decision.Action != "allow" {
		t.Fatalf("default internal Read should auto-allow, got %#v", decision)
	}

	call.Function.Arguments = `{"file_path":"` + outside + `"}`
	decision, err = service.resolveToolPermission(context.Background(), Narrator{ID: "nar_read_default_ext", PermissionMode: &defaultMode}, cwd, call)
	if err != nil {
		t.Fatalf("resolve default external Read: %v", err)
	}
	if decision.Action != "ask" {
		t.Fatalf("default external Read should ask like TS, got %#v", decision)
	}
	decision, err = service.resolveToolPermission(context.Background(), Narrator{ID: "nar_read_ro_ext", PermissionMode: &readOnly}, cwd, call)
	if err != nil {
		t.Fatalf("resolve readOnly external Read: %v", err)
	}
	if decision.Action != "deny" {
		t.Fatalf("readOnly external Read should deny like TS, got %#v", decision)
	}
	decision, err = service.resolveToolPermission(context.Background(), Narrator{ID: "nar_read_accept_ext", PermissionMode: &acceptEdits}, cwd, call)
	if err != nil {
		t.Fatalf("resolve acceptEdits external Read: %v", err)
	}
	if decision.Action != "ask" {
		t.Fatalf("acceptEdits external Read should ask like TS, got %#v", decision)
	}

	call.Function.Name = "Browser"
	call.Function.Arguments = `{"action":"get_text"}`
	decision, err = service.resolveToolPermission(context.Background(), Narrator{ID: "nar_browser_default", PermissionMode: &defaultMode}, cwd, call)
	if err != nil {
		t.Fatalf("resolve default Browser: %v", err)
	}
	if decision.Action != "ask" {
		t.Fatalf("default Browser should ask like TS fallback, got %#v", decision)
	}
	decision, err = service.resolveToolPermission(context.Background(), Narrator{ID: "nar_browser_ro", PermissionMode: &readOnly}, cwd, call)
	if err != nil {
		t.Fatalf("resolve readOnly Browser: %v", err)
	}
	if decision.Action != "deny" {
		t.Fatalf("readOnly Browser should deny like TS fallback, got %#v", decision)
	}

	call.Function.Name = "WebSearch"
	call.Function.Arguments = `{"query":"narrafork"}`
	decision, err = service.resolveToolPermission(context.Background(), Narrator{ID: "nar_websearch_ro", PermissionMode: &readOnly}, cwd, call)
	if err != nil {
		t.Fatalf("resolve readOnly WebSearch: %v", err)
	}
	if decision.Action != "allow" {
		t.Fatalf("WebSearch should remain always-allow, got %#v", decision)
	}
}

func TestResolveToolPermissionModeOnlySpecialToolsMatchTS(t *testing.T) {
	sqlDB := sqlDBForPlanRestrictionTest(t)
	service := &Service{DB: sqlDB}
	cwd := t.TempDir()
	for _, toolName := range []string{"TeamStatus", "AddGoal", "Terminal"} {
		call := agentbasic.ToolCall{}
		call.Function.Name = toolName
		call.Function.Arguments = `{}`

		defaultMode := "default"
		decision, err := service.resolveToolPermission(context.Background(), Narrator{ID: "nar_" + toolName, PermissionMode: &defaultMode}, cwd, call)
		if err != nil {
			t.Fatalf("resolve %s in default: %v", toolName, err)
		}
		if decision.Action != "ask" {
			t.Fatalf("%s should ask in default mode like TS, got %#v", toolName, decision)
		}

		readOnly := "readOnly"
		decision, err = service.resolveToolPermission(context.Background(), Narrator{ID: "nar_" + toolName, PermissionMode: &readOnly}, cwd, call)
		if err != nil {
			t.Fatalf("resolve %s in readOnly: %v", toolName, err)
		}
		if decision.Action != "deny" {
			t.Fatalf("%s should deny in readOnly mode like TS, got %#v", toolName, decision)
		}

		acceptEdits := "acceptEdits"
		decision, err = service.resolveToolPermission(context.Background(), Narrator{ID: "nar_" + toolName, PermissionMode: &acceptEdits}, cwd, call)
		if err != nil {
			t.Fatalf("resolve %s in acceptEdits: %v", toolName, err)
		}
		if decision.Action != "ask" {
			t.Fatalf("%s should ask in acceptEdits mode like TS, got %#v", toolName, decision)
		}

		bypass := "bypassPermissions"
		decision, err = service.resolveToolPermission(context.Background(), Narrator{ID: "nar_" + toolName, PermissionMode: &bypass}, cwd, call)
		if err != nil {
			t.Fatalf("resolve %s in bypassPermissions: %v", toolName, err)
		}
		if decision.Action != "allow" {
			t.Fatalf("%s should allow in bypassPermissions mode, got %#v", toolName, decision)
		}
	}
}

func TestToolRequiresApprovalRecognizesPermissionGatedSpecialTools(t *testing.T) {
	defaultMode := "default"
	acceptEdits := "acceptEdits"
	readOnly := "readOnly"
	dontAsk := "dontAsk"
	bypass := "bypassPermissions"
	for _, toolName := range []string{"Agent", "Send", "TeamStatus", "AddGoal", "Browser", "Read"} {
		if !toolRequiresApproval(&defaultMode, toolName) {
			t.Fatalf("%s should suppress pre-permission running records in default mode", toolName)
		}
		if !toolRequiresApproval(&acceptEdits, toolName) {
			t.Fatalf("%s should suppress pre-permission running records in acceptEdits mode", toolName)
		}
		if toolRequiresApproval(&readOnly, toolName) {
			t.Fatalf("%s should not await approval in readOnly mode because resolveToolPermission denies/auto-allows synchronously", toolName)
		}
	}
	for _, toolName := range []string{"WebSearch", "Await", "TaskCreate", "UpdateGoal"} {
		if toolRequiresApproval(&defaultMode, toolName) || toolRequiresApproval(&acceptEdits, toolName) {
			t.Fatalf("%s should keep pre-permission running records because it is always-allow", toolName)
		}
	}
	for _, toolName := range []string{"AskUserQuestion", "ExitPlanMode"} {
		for _, mode := range []*string{&defaultMode, &acceptEdits, &readOnly, &dontAsk, &bypass} {
			if !toolRequiresApproval(mode, toolName) {
				t.Fatalf("%s should suppress pre-permission running records in mode %s", toolName, *mode)
			}
		}
	}
}

func TestResolveToolPermissionAlwaysAskToolsMatchTS(t *testing.T) {
	service := &Service{DB: sqlDBForPlanRestrictionTest(t)}
	cwd := t.TempDir()
	for _, tc := range []struct {
		name string
		args string
	}{
		{name: "AskUserQuestion", args: `{"questions":[{"question":"Choose?","header":"Choice","options":[{"label":"A","description":"A"},{"label":"B","description":"B"}],"multiSelect":false}]}`},
		{name: "ExitPlanMode", args: `{"plan":"This is a complete implementation plan with enough detail for approval."}`},
	} {
		for _, mode := range []string{"default", "acceptEdits", "readOnly", "dontAsk", "bypassPermissions"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				call := agentbasic.ToolCall{}
				call.Function.Name = tc.name
				call.Function.Arguments = tc.args
				decision, err := service.resolveToolPermission(context.Background(), Narrator{ID: "nar_always_ask_" + mode, PermissionMode: &mode}, cwd, call)
				if err != nil {
					t.Fatalf("resolve %s: %v", tc.name, err)
				}
				if decision.Action != "ask" {
					t.Fatalf("%s should always ask like TS, got %#v", tc.name, decision)
				}
			})
		}
	}
}

func sqlDBForPlanRestrictionTest(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec(`CREATE TABLE narrator_whitelist_dirs (narrator_id TEXT, path TEXT, access_level TEXT, enabled INTEGER); CREATE TABLE narrator_blacklist_dirs (narrator_id TEXT, path TEXT, deny_level TEXT, enabled INTEGER); CREATE TABLE narrator_whitelist_cmds (narrator_id TEXT, pattern TEXT, enabled INTEGER); CREATE TABLE narrator_blacklist_cmds (narrator_id TEXT, pattern TEXT, deny_prompt TEXT, enabled INTEGER)`); err != nil {
		t.Fatalf("create permission rule tables: %v", err)
	}
	return sqlDB
}

func TestResolveExitPlanModeReadsDesignatedPlanFile(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	cwd := t.TempDir()
	start := true
	narrator, err := service.Create(context.Background(), CreateInput{Model: "test-model", PermissionMode: "default", StartInPlanMode: &start, Cwd: &cwd})
	if err != nil {
		t.Fatalf("create narrator: %v", err)
	}
	if narrator.PlanFileID == nil {
		t.Fatalf("expected start-in-plan narrator to have a plan file id")
	}
	planRel := planFileRelativePath(*narrator.PlanFileID)
	planPath := filepath.Join(cwd, planRel)
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	filePlan := "Implement the change by updating Go plan mode state, restricting writes, and adding tests."
	if err := os.WriteFile(planPath, []byte(filePlan), 0o644); err != nil {
		t.Fatalf("write plan file: %v", err)
	}
	plan, source, err := service.resolveExitPlanModePlan(narrator, map[string]any{"plan": "inline fallback should not win"})
	if err != nil {
		t.Fatalf("resolve plan: %v", err)
	}
	if plan != filePlan || source != "planFile" {
		t.Fatalf("plan/source = %q/%q, want file plan/planFile", plan, source)
	}
}

func TestReflectionDecisionFromToolCall(t *testing.T) {
	decision, output := reflectionDecisionFromToolCall("GoalCompleteConfirm", `{"confirm":true,"evidence":"Verified tests passed and schema stayed untouched.","reflection":"safe"}`)
	if decision.Action != "confirm" || decision.Evidence == "" || output == "" {
		t.Fatalf("expected goal completion confirm decision, got %#v output=%q", decision, output)
	}
	decision, _ = reflectionDecisionFromToolCall("GoalCompleteRevise", `{"confirm":true,"feedback":"Need to run the final validation.","nextSteps":"Run the final validation and retry UpdateGoal if it passes."}`)
	if decision.Action != "revise" || decision.Feedback == "" || decision.NextSteps == "" {
		t.Fatalf("expected goal completion revise decision with next steps, got %#v", decision)
	}
	decision, output = reflectionDecisionFromToolCall("GoalCompleteRevise", `{"confirm":true,"feedback":"Need to run the final validation."}`)
	if decision.Action != "" || !strings.Contains(output, "nextSteps") {
		t.Fatalf("expected missing nextSteps to reject goal completion revise, got %#v output=%q", decision, output)
	}
	decision, _ = reflectionDecisionFromToolCall("ExitPlanConfirm", `{"confirm":true,"reflection":"plan is actionable"}`)
	if decision.Action != "confirm" || decision.ToolName != "ExitPlanConfirm" {
		t.Fatalf("expected exit plan confirm decision, got %#v", decision)
	}
	decision, _ = reflectionDecisionFromToolCall("ExitPlanRevise", `{"confirm":true,"feedback":"Add concrete file changes."}`)
	if decision.Action != "revise" || decision.Feedback == "" {
		t.Fatalf("expected exit plan revise decision, got %#v", decision)
	}
	// Go always routes ExitPlanMode through explicit user approval after readiness reflection
	// and does not implement the TypeScript planReflectionAutoApprove skip path, so the
	// auto-compact reflection tool (ExitPlanConfirmAndCompact / confirm_compact) is intentionally
	// not a recognized reflection action. User-driven compactAfter is handled at approval time.
	decision, output = reflectionDecisionFromToolCall("ExitPlanConfirmAndCompact", `{"confirm":true,"reflection":"reset context"}`)
	if decision.Action != "" || !strings.Contains(output, "not supported") {
		t.Fatalf("expected ExitPlanConfirmAndCompact to be unsupported by the reflection loop, got %#v output=%q", decision, output)
	}
	decision, output = reflectionDecisionFromToolCall("GoalCompleteConfirm", `{"confirm":false,"evidence":"Verified tests passed and schema stayed untouched."}`)
	if decision.Action != "" || output == "" {
		t.Fatalf("expected invalid decision without confirm=true, got %#v output=%q", decision, output)
	}
}

// TestUpdateTodosResultMatchesTSFormat pins the TaskCreate tool result to the TS
// taskCreateTool format (server/lib/agent/tools/todo.ts): a header
// "Updated todos: N total (C completed, I in progress, P pending)" followed by a
// blank line and the per-item formatted list (status icon + [id] + content +
// optional [priority]). Go previously returned only the single summary line and
// omitted the formatted list, a model-visible divergence.
func TestUpdateTodosResultMatchesTSFormat(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narratorID := "nar_todos"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, narratorID); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	todos := []any{
		map[string]any{"id": "p1", "content": "Audit parity", "status": "completed", "priority": "high"},
		map[string]any{"id": "p2", "content": "Write tests", "status": "in_progress"},
		map[string]any{"id": "p3", "content": "Sync spec", "status": "pending", "priority": "low"},
	}
	out, err := service.updateTodos(context.Background(), narratorID, "tool_todo_1", todos)
	if err != nil {
		t.Fatalf("updateTodos: %v", err)
	}
	header := "Updated todos: 3 total (1 completed, 1 in progress, 1 pending)"
	if !strings.HasPrefix(out, header+"\n\n") {
		t.Fatalf("expected TS-style header + blank line, got:\n%s", out)
	}
	for _, want := range []string{
		"✓ [p1] Audit parity [high]",
		"→ [p2] Write tests",
		"○ [p3] Sync spec [low]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected formatted todo line %q in output:\n%s", want, out)
		}
	}
	// A medium-priority todo must NOT carry a priority suffix.
	if strings.Contains(out, "Write tests [medium]") {
		t.Fatalf("medium priority should be suppressed, got:\n%s", out)
	}
}

// TestGoalToolOutputsMatchTSShape pins the GetGoals/AddGoal/UpdateGoal tool
// result envelopes to the TS goal tools (server/lib/agent/tools/goal.ts):
// formatGoals renders "N. [status] objective (time=Ts tokens=K)"; AddGoal maps
// to {added, alreadyExists, goal, goals, summary}; GetGoals adds {active,
// summary}. Go previously omitted active/summary and used {goal,goals,created}.
func TestGoalToolOutputsMatchTSShape(t *testing.T) {
	// formatGoals empty + populated.
	if got := formatGoals(nil); got != "No goals are currently set." {
		t.Fatalf("empty formatGoals: %q", got)
	}
	goals := []map[string]any{
		{"status": "active", "objective": "Ship parity", "timeUsedSeconds": 12, "tokensUsed": 345},
		{"status": "pending", "objective": "Write docs", "timeUsedSeconds": 0, "tokensUsed": 0},
	}
	want := "1. [active] Ship parity (time=12s tokens=345)\n2. [pending] Write docs (time=0s tokens=0)"
	if got := formatGoals(goals); got != want {
		t.Fatalf("formatGoals mismatch:\n got=%q\nwant=%q", got, want)
	}

	// addGoalToolOutput: created path.
	created := addGoalToolOutput(map[string]any{"created": true, "goal": goals[0], "goals": goals})
	if created["added"] == nil || created["alreadyExists"] != false {
		t.Fatalf("created addGoal output should set added + alreadyExists=false: %#v", created)
	}
	if _, ok := created["summary"].(string); !ok {
		t.Fatalf("addGoal output must include summary string: %#v", created)
	}
	if strings.Contains(created["summary"].(string), "already exists") {
		t.Fatalf("created summary should not carry the duplicate preamble: %#v", created)
	}

	// addGoalToolOutput: duplicate path.
	dup := addGoalToolOutput(map[string]any{"created": false, "goal": goals[0], "goals": goals})
	if dup["added"] != nil || dup["alreadyExists"] != true {
		t.Fatalf("duplicate addGoal output should set added=nil + alreadyExists=true: %#v", dup)
	}
	if !strings.HasPrefix(dup["summary"].(string), "A matching open goal already exists; no duplicate was added.\n") {
		t.Fatalf("duplicate summary should carry the preamble: %#v", dup)
	}
}

// TestFormatAskUserAnswersPreservesQuestionOrder pins the AskUserQuestion tool
// result to TS (server/lib/agent/tools/ask-user-question.ts): "User answered:"
// header followed by "- key: value" lines in the JSON answers insertion order
// (question order), with arrays joined by ", ". Empty answers yield the TS
// "No answers were provided by the user." Go previously sorted keys
// alphabetically, reordering multi-question answers vs TS.
func TestFormatAskUserAnswersPreservesQuestionOrder(t *testing.T) {
	if got := formatAskUserAnswers(map[string]any{}, nil); got != "No answers were provided by the user." {
		t.Fatalf("empty answers: %q", got)
	}
	// Keys deliberately NOT alphabetical: zeta before alpha. The order slice
	// (from jsonObjectKeyOrder) must drive the output.
	answers := map[string]any{
		"zeta":  "Use Redis",
		"alpha": []any{"TypeScript", "Go"},
	}
	order := []string{"zeta", "alpha"}
	want := "User answered:\n- zeta: Use Redis\n- alpha: TypeScript, Go"
	if got := formatAskUserAnswers(answers, order); got != want {
		t.Fatalf("ordered answers mismatch:\n got=%q\nwant=%q", got, want)
	}
	// Without an order slice, fall back to alphabetical (deterministic).
	wantSorted := "User answered:\n- alpha: TypeScript, Go\n- zeta: Use Redis"
	if got := formatAskUserAnswers(answers, nil); got != wantSorted {
		t.Fatalf("fallback sort mismatch:\n got=%q\nwant=%q", got, wantSorted)
	}
}

// TestJSONObjectKeyOrderExtractsInsertionOrder confirms jsonObjectKeyOrder reads
// the textual key order of a nested object, which encoding/json's map decode
// would otherwise discard.
func TestJSONObjectKeyOrderExtractsInsertionOrder(t *testing.T) {
	raw := []byte(`{"questions":[{"question":"q1"}],"answers":{"zeta":"a","alpha":["b","c"],"mid":"d"},"metadata":{"source":"x"}}`)
	order := jsonObjectKeyOrder(raw, "answers")
	want := []string{"zeta", "alpha", "mid"}
	if len(order) != len(want) {
		t.Fatalf("key order length mismatch: got %v want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("key order mismatch at %d: got %v want %v", i, order, want)
		}
	}
	if jsonObjectKeyOrder(raw, "missing") != nil {
		t.Fatalf("absent field should yield nil order")
	}
}

// TestPipelineStartAndNotActiveMessagesMatchTS pins the StartPipeline started
// message and the EndPipeline not-active guard to the TS pipeline tool
// (server/lib/agent/tools/pipeline.ts). Go previously returned terse
// "Pipeline capture started" / "No active pipeline." strings.
func TestPipelineStartAndNotActiveMessagesMatchTS(t *testing.T) {
	p := &agentPipelineState{}
	started := p.start("audit", 0)
	wantStart := `Pipeline mode started (audit). Future tool outputs will be captured as p1, p2, ... with previews capped at 100 characters. Use EndPipeline with a rule such as: from p1 | grep -i "error|failed" | head -n 20`
	if started != wantStart {
		t.Fatalf("start message mismatch:\n got=%q\nwant=%q", started, wantStart)
	}
	// No-label variant must omit the parenthetical.
	noLabel := (&agentPipelineState{}).start("", 50)
	if strings.Contains(noLabel, "()") || !strings.HasPrefix(noLabel, "Pipeline mode started. ") {
		t.Fatalf("no-label start should omit label parens and use default sentence, got %q", noLabel)
	}
	if !strings.Contains(noLabel, "capped at 50 characters") {
		t.Fatalf("start should report the effective preview cap, got %q", noLabel)
	}
	// Not-active EndPipeline guard.
	if got := (&agentPipelineState{}).end("", nil, "sections", 0); got != "Pipeline mode is not active." {
		t.Fatalf("not-active guard message mismatch: %q", got)
	}
}

// TestPipelineRuleEngineMatchesTS exercises the ported pipeline rule engine
// (mirroring server/lib/agent/pipeline-rules.ts + pipeline.ts) end-to-end via
// agentPipelineState.end: from-selection, grep -i/-v + regex, head/tail -n,
// sort -r, uniq, cut -d/-f, the "Pipeline result" envelope, plain format, and
// rule errors.
func TestPipelineRuleEngineMatchesTS(t *testing.T) {
	newState := func() *agentPipelineState {
		p := &agentPipelineState{}
		p.start("", 100)
		p.Captures = []agentPipelineCapture{
			{Alias: "p1", Tool: "Bash", Text: "alpha\nBeta error\ngamma\nbeta\nalpha", Bytes: 28},
			{Alias: "p2", Tool: "Grep", Text: "x:1\ny:2\nz:3", Bytes: 11},
		}
		p.NextAlias = 3
		return p
	}

	// plain grep -i with alternation regex.
	out := newState().end(`from p1 | grep -i "error|gamma"`, nil, "plain", 0)
	if out != "Beta error\ngamma" {
		t.Fatalf("grep -i regex mismatch: %q", out)
	}

	// grep -v inverts.
	out = newState().end(`from p1 | grep -v alpha`, nil, "plain", 0)
	if out != "Beta error\ngamma\nbeta" {
		t.Fatalf("grep -v mismatch: %q", out)
	}

	// sort then uniq (sorted: alpha, alpha, beta, gamma, Beta error -> capital sorts before lowercase).
	out = newState().end(`from p1 | sort | uniq`, nil, "plain", 0)
	if out != "Beta error\nalpha\nbeta\ngamma" {
		t.Fatalf("sort|uniq mismatch: %q", out)
	}

	// sort -r reverses.
	out = newState().end(`from p1 | sort -r | head -n 1`, nil, "plain", 0)
	if out != "gamma" {
		t.Fatalf("sort -r|head mismatch: %q", out)
	}

	// tail -n 2.
	out = newState().end(`from p1 | tail -n 2`, nil, "plain", 0)
	if out != "beta\nalpha" {
		t.Fatalf("tail -n mismatch: %q", out)
	}

	// cut -d : -f 1 on p2.
	out = newState().end(`from p2 | cut -d : -f 1`, nil, "plain", 0)
	if out != "x\ny\nz" {
		t.Fatalf("cut mismatch: %q", out)
	}

	// sections envelope.
	out = newState().end(`from p1 | head -n 1`, nil, "sections", 0)
	for _, want := range []string{"Pipeline result", "Aliases used: p1", "Captured: p1=Bash(28B), p2=Grep(11B)", "Rule: from p1 | head -n 1", "\n\nalpha"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sections envelope missing %q in:\n%s", want, out)
		}
	}

	// unknown alias -> rule error.
	out = newState().end(`from p9 | cat`, nil, "plain", 0)
	if !strings.HasPrefix(out, "Pipeline rule error: Unknown pipeline alias: p9") {
		t.Fatalf("unknown alias should surface a rule error, got: %q", out)
	}

	// unsupported command -> rule error.
	out = newState().end(`from p1 | wc -l`, nil, "plain", 0)
	if !strings.Contains(out, "Pipeline rule error: Unsupported pipeline command: wc") {
		t.Fatalf("unsupported command should surface a rule error, got: %q", out)
	}
}

// TestPipelineCaptureFormatMatchesTS pins the per-capture preview block to the
// TS formatCapturedPreview (pipeline-state.ts) shape.
func TestPipelineCaptureFormatMatchesTS(t *testing.T) {
	p := &agentPipelineState{}
	p.start("", 10)
	run := &agentbasic.ToolRun{Name: "Bash", Output: "0123456789ABCDEF"}
	p.capture(run)
	// alias line + Tool + Bytes + Preview header + clipped preview (first 9 + …).
	want := "Pipeline capture alias: p1\nTool: Bash\nBytes: 16\nPreview:\n012345678…"
	if run.Output != want {
		t.Fatalf("capture preview mismatch:\n got=%q\nwant=%q", run.Output, want)
	}
	// Empty output renders "(empty)".
	emptyRun := &agentbasic.ToolRun{Name: "Bash", Output: ""}
	p.capture(emptyRun)
	if !strings.HasSuffix(emptyRun.Output, "Preview:\n(empty)") || !strings.Contains(emptyRun.Output, "alias: p2") {
		t.Fatalf("empty capture should render (empty) for p2, got:\n%s", emptyRun.Output)
	}
}

// TestEnterPlanModeOutputMatchesTSText pins the EnterPlanMode tool result to the
// TS enterPlanModeTool text (i18n tool.enterPlanModeOutput) instead of the Go
// structured plan-mode JSON payload. The real state change (plan_mode column +
// plan_mode_changed broadcast) still happens; only the model-visible run.Output
// text is asserted here.
func TestEnterPlanModeOutputMatchesTSText(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narrator, err := service.Create(context.Background(), CreateInput{Model: "test-model"})
	if err != nil {
		t.Fatalf("create narrator: %v", err)
	}
	call := agentbasic.ToolCall{ID: "enter-plan-1", Type: "function"}
	call.Function.Name = "EnterPlanMode"
	call.Function.Arguments = `{}`
	run, handled, err := service.executeNarratorTool(context.Background(), narrator.ID, narrator, nil, &agentPipelineState{}, call, nil)
	if err != nil || !handled {
		t.Fatalf("expected handled EnterPlanMode, handled=%v err=%v", handled, err)
	}
	if run.Output != "Entered plan mode. Analyze and plan before making changes." {
		t.Fatalf("EnterPlanMode output should be the TS confirmation text, got %q", run.Output)
	}
	// State change must still have happened.
	updated, err := service.Get(context.Background(), narrator.ID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	if !updated.PlanMode {
		t.Fatalf("EnterPlanMode should still flip plan_mode on the narrator")
	}
}

// TestBackgroundBashTitleMatchesTS pins the background bash task title to the TS
// derivation (server/lib/agent/tools/bash.ts:182:
// `description || command.slice(0, 80)`): a non-empty description is used
// verbatim (NOT truncated); only the command fallback is sliced to 80 UTF-16
// code units. Go previously truncated both to 80 runes.
func TestBackgroundBashTitleMatchesTS(t *testing.T) {
	// A long description is used verbatim, not capped at 80.
	longDesc := strings.Repeat("d", 200)
	if got := backgroundBashTitle(longDesc, "echo hi"); got != longDesc {
		t.Fatalf("provided description should be used verbatim (uncapped), got len=%d", len(got))
	}
	// Empty description -> command sliced to 80 UTF-16 units.
	longCmd := strings.Repeat("x", 100)
	if got := backgroundBashTitle("", longCmd); got != strings.Repeat("x", 80) {
		t.Fatalf("command fallback should slice to 80, got len=%d", len(got))
	}
	// CJK command: 100 chars -> 80 chars (UTF-16), valid UTF-8, not 80 bytes.
	cjkCmd := strings.Repeat("令", 100)
	got := backgroundBashTitle("   ", cjkCmd) // whitespace-only description falls back
	if r := []rune(got); len(r) != 80 {
		t.Fatalf("CJK command fallback should keep 80 chars (UTF-16), got %d chars %q", len(r), got)
	}
}

// TestBackgroundTaskPreviewUsesUTF16 pins backgroundTaskPreview to the TS
// outputPreview (background-task-service.ts:220): slice(0, 200) over UTF-16 code
// units + "…". Go previously used []rune (Unicode code points), cutting
// astral-character output at a different boundary than TS.
func TestBackgroundTaskPreviewUsesUTF16(t *testing.T) {
	// <=200 chars: unchanged.
	short := strings.Repeat("a", 200)
	if got := backgroundTaskPreview(short); got != short {
		t.Fatalf("200-char output should be unchanged")
	}
	// 201 ASCII: truncated to 200 + "…".
	if got := backgroundTaskPreview(strings.Repeat("a", 201)); got != strings.Repeat("a", 200)+"…" {
		t.Fatalf("201-char output should truncate to 200+…")
	}
	// Astral (U+1D400, 2 UTF-16 units each): 150 of them = 300 UTF-16 units ->
	// sliced to 200 units = 100 runes + "…". A rune-count cap would keep 150.
	astral := strings.Repeat("\U0001D400", 150)
	trimmed := strings.TrimSuffix(backgroundTaskPreview(astral), "…")
	if r := []rune(trimmed); len(r) != 100 {
		t.Fatalf("astral output preview should keep 100 runes (200 UTF-16 units), got %d", len(r))
	}
}
