package narrators

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// TestTodoWriteOutputHeaderMatchesTS exercises the helper directly across
// locales, mirroring the TS tool.todoWriteOutput i18n message (i18n.ts:164-167)
// with the {total}/{completed}/{inProgress}/{pending} placeholders substituted.
func TestTodoWriteOutputHeaderMatchesTS(t *testing.T) {
	if got := todoWriteOutputHeader("en", 3, 1, 1, 1); got != "Updated todos: 3 total (1 completed, 1 in progress, 1 pending)" {
		t.Fatalf("en header mismatch: %q", got)
	}
	if got := todoWriteOutputHeader("zh-CN", 3, 1, 1, 1); got != "已更新待办事项：共 3 项（1 已完成，1 进行中，1 待处理）" {
		t.Fatalf("zh header mismatch: %q", got)
	}
	// Unknown locale falls back to English (mirrors getToolMessage ?? en).
	if got := todoWriteOutputHeader("fr", 0, 0, 0, 0); got != "Updated todos: 0 total (0 completed, 0 in progress, 0 pending)" {
		t.Fatalf("unknown locale should fall back to en, got %q", got)
	}
}

// TestUpdateTodosUsesOwnerLocaleHeader verifies updateTodos localizes the header
// using the narrator owner's locale (resolved via toolLocale from the earliest
// top-level user message's created_by), matching TS ctx.locale.
func TestUpdateTodosUsesOwnerLocaleHeader(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE user_preferences (user_id TEXT PRIMARY KEY, language TEXT)`); err != nil {
		t.Fatalf("create user_preferences: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO user_preferences (user_id, language) VALUES ('user-zh', 'zh-CN')`); err != nil {
		t.Fatalf("insert pref: %v", err)
	}
	service := &Service{DB: sqlDB}
	narratorID := "nar_todos_zh"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, narratorID); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES ('owner-msg', ?, 'user', NULL, 'user-zh', ?)`, narratorID, now); err != nil {
		t.Fatalf("insert owner message: %v", err)
	}
	todos := []any{
		map[string]any{"id": "p1", "content": "审计 parity", "status": "completed", "priority": "high"},
		map[string]any{"id": "p2", "content": "写测试", "status": "in_progress"},
		map[string]any{"id": "p3", "content": "同步 spec", "status": "pending", "priority": "low"},
	}
	out, err := service.updateTodos(context.Background(), narratorID, "tool_todo_zh", todos)
	if err != nil {
		t.Fatalf("updateTodos: %v", err)
	}
	if !strings.HasPrefix(out, "已更新待办事项：共 3 项（1 已完成，1 进行中，1 待处理）\n\n") {
		t.Fatalf("expected zh-CN owner-locale header, got:\n%s", out)
	}
	if strings.Contains(out, "Updated todos:") {
		t.Fatalf("zh-CN owner header must not be English, got:\n%s", out)
	}
	// The formatted list (locale-independent icons/ids) is still appended.
	if !strings.Contains(out, "✓ [p1] 审计 parity [high]") {
		t.Fatalf("expected formatted todo list in output:\n%s", out)
	}
}
