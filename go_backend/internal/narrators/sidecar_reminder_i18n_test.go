package narrators

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"narrafork-go/internal/agentbasic"
)

// TestRelaxedPlanToolReminderTextMatchesTS exercises the helper directly across
// locales, mirroring TS tool.relaxedPlanToolReminder (i18n.ts:152-159).
func TestRelaxedPlanToolReminderTextMatchesTS(t *testing.T) {
	en := relaxedPlanToolReminder("en")
	for _, want := range []string{
		"<relaxed_plan_reminder>",
		"You are still in relaxed plan mode.",
		"call ExitPlanMode to submit the complete plan for approval.",
		"</relaxed_plan_reminder>",
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("en relaxed plan reminder missing %q:\n%s", want, en)
		}
	}
	zh := relaxedPlanToolReminder("zh-CN")
	for _, want := range []string{
		"<relaxed_plan_reminder>",
		"你仍处于宽松计划模式。",
		"调用 ExitPlanMode 提交完整计划供用户批准。",
		"</relaxed_plan_reminder>",
	} {
		if !strings.Contains(zh, want) {
			t.Fatalf("zh relaxed plan reminder missing %q:\n%s", want, zh)
		}
	}
	if strings.Contains(zh, "You are still in relaxed plan mode") {
		t.Fatalf("zh relaxed plan reminder must not contain english text:\n%s", zh)
	}
}

// openSidecarReminderTestDB builds the minimal schema buildTodoToolResultReminder
// needs (narrators with todos_json, narrator_messages for owner resolution,
// user_preferences for language).
func openSidecarReminderTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, todos_json TEXT, plan_mode INTEGER DEFAULT 0, relaxed_plan INTEGER DEFAULT 0)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, role TEXT, parent_tool_use_id TEXT, created_by TEXT, created_at TEXT)`,
		`CREATE TABLE user_preferences (user_id TEXT PRIMARY KEY, language TEXT, reply_in_user_language INTEGER)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return db
}

func seedTodoNarratorOwner(t *testing.T, db *sql.DB, narratorID, todosJSON, ownerID, language string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO narrators (id, todos_json) VALUES (?, ?)`, narratorID, todosJSON); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if ownerID != "" {
		if _, err := db.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES (?, ?, 'user', NULL, ?, '2026-01-01T00:00:00Z')`, "m-"+narratorID, narratorID, ownerID); err != nil {
			t.Fatalf("insert message: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO user_preferences (user_id, language, reply_in_user_language) VALUES (?, ?, 1)`, ownerID, language); err != nil {
			t.Fatalf("insert pref: %v", err)
		}
	}
}

// TestBuildTodoToolResultReminderLocalized verifies the todo reminder body is
// localized from the narrator owner, mirroring TS tool.todoReminder.
func TestBuildTodoToolResultReminderLocalized(t *testing.T) {
	todos := `[{"id":"p1","content":"PARITY_TODO","status":"pending","priority":"high"}]`

	dbEn := openSidecarReminderTestDB(t)
	seedTodoNarratorOwner(t, dbEn, "nar-en", todos, "user-en", "en")
	svcEn := &Service{DB: dbEn}
	en := svcEn.buildTodoToolResultReminder(context.Background(), "nar-en")
	for _, want := range []string{
		"<todo_reminder>",
		"Current active todos:",
		"PARITY_TODO",
		"If any todo state changes, call TaskCreate with the complete updated list.",
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("en todo reminder missing %q:\n%s", want, en)
		}
	}

	dbZh := openSidecarReminderTestDB(t)
	seedTodoNarratorOwner(t, dbZh, "nar-zh", todos, "user-zh", "zh-CN")
	svcZh := &Service{DB: dbZh}
	zh := svcZh.buildTodoToolResultReminder(context.Background(), "nar-zh")
	for _, want := range []string{
		"当前未完成待办：",
		"PARITY_TODO",
		"如果待办状态发生变化，请调用 TaskCreate 并传入完整更新后的列表。",
	} {
		if !strings.Contains(zh, want) {
			t.Fatalf("zh todo reminder missing %q:\n%s", want, zh)
		}
	}
	if strings.Contains(zh, "Current active todos:") {
		t.Fatalf("zh todo reminder must not contain english body:\n%s", zh)
	}

	// Empty todos -> empty string (behavior unchanged).
	dbEmpty := openSidecarReminderTestDB(t)
	seedTodoNarratorOwner(t, dbEmpty, "nar-empty", `[]`, "user-en", "en")
	svcEmpty := &Service{DB: dbEmpty}
	if got := svcEmpty.buildTodoToolResultReminder(context.Background(), "nar-empty"); got != "" {
		t.Fatalf("empty todos should yield empty reminder, got %q", got)
	}
}

// TestAgentSideCarCollectorRelaxedPlanReminderLocalized verifies the relaxed-plan
// sidecar injected through agentSideCarCollector is localized from the narrator
// owner.
func TestAgentSideCarCollectorRelaxedPlanReminderLocalized(t *testing.T) {
	db := openSidecarReminderTestDB(t)
	if _, err := db.Exec(`INSERT INTO narrators (id, todos_json, plan_mode, relaxed_plan) VALUES ('nar-zh', '[]', 1, 1)`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES ('m1', 'nar-zh', 'user', NULL, 'user-zh', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_preferences (user_id, language, reply_in_user_language) VALUES ('user-zh', 'zh-CN', 1)`); err != nil {
		t.Fatalf("insert pref: %v", err)
	}
	svc := &Service{DB: db}
	collector := svc.agentSideCarCollector("nar-zh")
	sideCars := collector(context.Background(), agentbasic.SideCarRequest{Phase: "tool_result", ToolName: "Write", ToolUseID: "w1", ToolInput: map[string]any{"file_path": "main.go"}, CompletedToolCount: 1})
	if len(sideCars) == 0 {
		t.Fatal("expected a relaxed plan sidecar")
	}
	found := false
	for _, sc := range sideCars {
		if sc.Source == "relaxed_plan" {
			found = true
			if !strings.Contains(sc.Content, "你仍处于宽松计划模式。") {
				t.Fatalf("relaxed plan sidecar not localized to zh-CN:\n%s", sc.Content)
			}
			if strings.Contains(sc.Content, "You are still in relaxed plan mode") {
				t.Fatalf("relaxed plan sidecar must not contain english text:\n%s", sc.Content)
			}
		}
	}
	if !found {
		t.Fatalf("relaxed_plan sidecar not found in %#v", sideCars)
	}
}
