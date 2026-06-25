package narrators

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestUserContinueMessageMatchesTS exercises the helper directly, mirroring TS
// tool.userContinue (i18n.ts:235-238).
func TestUserContinueMessageMatchesTS(t *testing.T) {
	if got := userContinueMessage("en"); got != "Continue." {
		t.Fatalf("en user continue = %q, want Continue.", got)
	}
	if got := userContinueMessage("zh-CN"); got != "继续。" {
		t.Fatalf("zh user continue = %q, want 继续。", got)
	}
	if got := userContinueMessage("fr"); got != "Continue." {
		t.Fatalf("unknown locale user continue = %q, want Continue. (en fallback)", got)
	}
}

// continueRouteTestDB builds the schema the continue route needs plus a
// user_preferences table (createNarratorTestSchema does not include one) and an
// idle narrator.
func continueRouteTestDB(t *testing.T, narratorID, systemLanguage string) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE user_preferences (user_id TEXT PRIMARY KEY, language TEXT)`); err != nil {
		t.Fatalf("create user_preferences: %v", err)
	}
	if systemLanguage != "" {
		if _, err := sqlDB.Exec(`INSERT INTO user_preferences (user_id, language) VALUES ('system', ?)`, systemLanguage); err != nil {
			t.Fatalf("insert system pref: %v", err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrators (
		id, type, title, inherit_mode, model, permission_mode, status, substatus, plan_mode,
		cwd, variant, traits, is_background, is_ask_in_passing, message_version, message_count,
		total_cost_usd, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
		danger_reflection_override, created_at, updated_at
	) VALUES (?, 'primary', 'Test', 'fresh', 'openai:test', 'default', 'idle', '[]', 0, ?, 'primary', '[]', 0, 0, 0, 0, 0, 0, 0, 'inherit', 'inherit', ?, ?)`,
		narratorID, t.TempDir(), now, now); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	return sqlDB
}

func continueRunMessageContent(t *testing.T, sqlDB *sql.DB, narratorID string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/narrators/"+narratorID+"/continue", nil)
	w := serveNarratorHandlerHTTPRequest(sqlDB, req)
	if w.Code != http.StatusOK {
		t.Fatalf("continue route status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		OK      bool `json:"ok"`
		Message struct {
			ContentText *string `json:"contentText"`
		} `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}
	if resp.Message.ContentText == nil {
		t.Fatalf("continue response missing message.contentText: %s", w.Body.String())
	}
	return *resp.Message.ContentText
}

// TestContinueRunLocalizesUserMessage verifies the continue route injects a
// locale-aware user message resolved from the requesting user (userID "system"
// when no JWT claims are present), mirroring TS continueNarrator.
func TestContinueRunLocalizesUserMessage(t *testing.T) {
	zhDB := continueRouteTestDB(t, "nar-zh", "zh-CN")
	if got := continueRunMessageContent(t, zhDB, "nar-zh"); got != "继续。" {
		t.Fatalf("zh continue message = %q, want 继续。", got)
	}

	enDB := continueRouteTestDB(t, "nar-en", "en")
	if got := continueRunMessageContent(t, enDB, "nar-en"); got != "Continue." {
		t.Fatalf("en continue message = %q, want Continue.", got)
	}
}
