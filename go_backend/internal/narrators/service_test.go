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

func TestExitPlanTextValid(t *testing.T) {
	if exitPlanTextValid("short plan") {
		t.Fatalf("short plan should not satisfy exit-plan gate")
	}
	if !exitPlanTextValid("Implement the feature by updating the service, adding tests, and running validation.") {
		t.Fatalf("complete plan should satisfy exit-plan gate")
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
	narrator := Narrator{PlanMode: true, PlanFileID: &planFileID}
	cwd := t.TempDir()
	call := agentbasic.ToolCall{}
	call.Function.Name = "Write"
	call.Function.Arguments = `{"file_path":"src/main.go","content":"package main"}`
	blocked, output := planModeToolRestriction(cwd, narrator, call)
	if !blocked || !strings.Contains(output, planFileRelativePath(planFileID)) {
		t.Fatalf("expected non-plan write denied with plan file hint, blocked=%v output=%q", blocked, output)
	}
	call.Function.Arguments = `{"file_path":".narrafork/plan-plan1234.md","content":"implementation plan with details"}`
	blocked, output = planModeToolRestriction(cwd, narrator, call)
	if blocked {
		t.Fatalf("expected designated plan file write allowed, output=%q", output)
	}
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
	decision, output = reflectionDecisionFromToolCall("GoalCompleteConfirm", `{"confirm":false,"evidence":"Verified tests passed and schema stayed untouched."}`)
	if decision.Action != "" || output == "" {
		t.Fatalf("expected invalid decision without confirm=true, got %#v output=%q", decision, output)
	}
}
