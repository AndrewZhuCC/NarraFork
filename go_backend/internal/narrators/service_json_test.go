package narrators

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/id"

	_ "modernc.org/sqlite"
)

func TestNarratorJSONFieldsResponseDecodeArrays(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	narratorID := id.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
		variant, traits, todos_json, enabled_tools, is_background, is_ask_in_passing, message_version,
		message_count, total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'primary', 'Narrator', 'fresh', 'test-model', 'default', 'idle', ?, 0,
		'primary', ?, ?, ?, 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`,
		narratorID,
		`["calling_model"]`,
		`["standalone","plan"]`,
		`[{"id":"todo-1","content":"Ship parity","status":"pending"}]`,
		`["Bash","Read"]`,
		now,
		now,
	)
	if err != nil {
		t.Fatalf("insert narrator: %v", err)
	}

	narrator, err := (&Service{DB: sqlDB}).Get(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("get narrator: %v", err)
	}
	if _, ok := narrator.TodosJSON.([]any); !ok {
		t.Fatalf("todosJson = %#v, want array", narrator.TodosJSON)
	}
	if _, ok := narrator.EnabledTools.([]any); !ok {
		t.Fatalf("enabledTools = %#v, want array", narrator.EnabledTools)
	}
	if len(narrator.Traits) != 2 || narrator.Traits[0] != "standalone" {
		t.Fatalf("traits = %#v, want decoded string array", narrator.Traits)
	}
	encoded, err := json.Marshal(narrator)
	if err != nil {
		t.Fatalf("marshal narrator: %v", err)
	}
	body := string(encoded)
	if strings.Contains(body, `"todosJson":"`) || strings.Contains(body, `"enabledTools":"`) || strings.Contains(body, `"traits":"`) {
		t.Fatalf("narrator JSON fields encoded as strings: %s", body)
	}
}
