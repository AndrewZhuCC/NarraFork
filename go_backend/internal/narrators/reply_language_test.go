package narrators

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// openReplyLanguageTestDB builds a minimal in-memory schema with the columns
// narratorReplyLanguage needs: narrator_messages (owner resolution) and
// user_preferences (language + reply_in_user_language).
func openReplyLanguageTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	stmts := []string{
		`CREATE TABLE narrator_messages (
			id TEXT PRIMARY KEY,
			narrator_id TEXT,
			role TEXT,
			parent_tool_use_id TEXT,
			created_by TEXT,
			created_at TEXT
		)`,
		`CREATE TABLE user_preferences (
			user_id TEXT PRIMARY KEY,
			language TEXT,
			reply_in_user_language INTEGER
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return db
}

func insertReplyLangUserMessage(t *testing.T, db *sql.DB, narratorID, createdBy string) {
	t.Helper()
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES (?, ?, 'user', NULL, ?, ?)`, "m-"+createdBy, narratorID, createdBy, createdAt); err != nil {
		t.Fatalf("insert message: %v", err)
	}
}

func insertReplyLangPref(t *testing.T, db *sql.DB, userID, language string, replyInUser int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO user_preferences (user_id, language, reply_in_user_language) VALUES (?, ?, ?)`, userID, language, replyInUser); err != nil {
		t.Fatalf("insert pref: %v", err)
	}
}

func TestReplyLanguageInstructionTextMatchesTS(t *testing.T) {
	if got := replyLanguageInstruction("en"); got != "Always reply in English." {
		t.Fatalf("en instruction = %q", got)
	}
	if got := replyLanguageInstruction("zh-CN"); !strings.Contains(got, "始终使用简体中文回复") {
		t.Fatalf("zh-CN instruction = %q", got)
	}
}

func TestNarratorReplyLanguageResolvesOwnerPrefs(t *testing.T) {
	db := openReplyLanguageTestDB(t)
	svc := &Service{DB: db}

	insertReplyLangPref(t, db, "user-zh", "zh-CN", 1)
	insertReplyLangUserMessage(t, db, "nar-zh", "user-zh")
	if loc, reply := svc.narratorReplyLanguage(context.Background(), "nar-zh"); loc != "zh-CN" || !reply {
		t.Fatalf("zh owner = (%q,%v), want (zh-CN,true)", loc, reply)
	}

	insertReplyLangPref(t, db, "user-en", "en", 1)
	insertReplyLangUserMessage(t, db, "nar-en", "user-en")
	if loc, reply := svc.narratorReplyLanguage(context.Background(), "nar-en"); loc != "en" || !reply {
		t.Fatalf("en owner = (%q,%v), want (en,true)", loc, reply)
	}

	insertReplyLangPref(t, db, "user-en-off", "en", 0)
	insertReplyLangUserMessage(t, db, "nar-en-off", "user-en-off")
	if loc, reply := svc.narratorReplyLanguage(context.Background(), "nar-en-off"); loc != "en" || reply {
		t.Fatalf("en owner reply-off = (%q,%v), want (en,false)", loc, reply)
	}
}

func TestNarratorReplyLanguageDefaultsWhenNoOwner(t *testing.T) {
	db := openReplyLanguageTestDB(t)
	svc := &Service{DB: db}
	if loc, reply := svc.narratorReplyLanguage(context.Background(), "nar-missing"); loc != "en" || !reply {
		t.Fatalf("no owner = (%q,%v), want (en,true) TS default", loc, reply)
	}
	// nil DB also defaults to (en, true).
	if loc, reply := (&Service{}).narratorReplyLanguage(context.Background(), "x"); loc != "en" || !reply {
		t.Fatalf("nil DB = (%q,%v), want (en,true)", loc, reply)
	}
}

func TestAgentSystemPromptAppendsLanguageSectionZh(t *testing.T) {
	db := openReplyLanguageTestDB(t)
	svc := &Service{DB: db}
	insertReplyLangPref(t, db, "user-zh", "zh-CN", 1)
	insertReplyLangUserMessage(t, db, "nar-zh", "user-zh")
	cwd := t.TempDir()
	prompt := svc.agentSystemPrompt(context.Background(), Narrator{ID: "nar-zh", Cwd: &cwd, SystemPrompt: ptr("base")})
	if !strings.Contains(prompt, "## Language") || !strings.Contains(prompt, "始终使用简体中文回复") {
		t.Fatalf("zh narrator prompt missing language section:\n%s", prompt)
	}
}

func TestAgentSystemPromptAppendsEnglishLanguageWhenReplyInUserLanguageDefault(t *testing.T) {
	db := openReplyLanguageTestDB(t)
	svc := &Service{DB: db}
	insertReplyLangPref(t, db, "user-en", "en", 1)
	insertReplyLangUserMessage(t, db, "nar-en", "user-en")
	cwd := t.TempDir()
	prompt := svc.agentSystemPrompt(context.Background(), Narrator{ID: "nar-en", Cwd: &cwd, SystemPrompt: ptr("base")})
	if !strings.Contains(prompt, "## Language") || !strings.Contains(prompt, "Always reply in English.") {
		t.Fatalf("en narrator prompt missing english language section:\n%s", prompt)
	}
}

func TestAgentSystemPromptOmitsLanguageWhenEnAndReplyDisabled(t *testing.T) {
	db := openReplyLanguageTestDB(t)
	svc := &Service{DB: db}
	insertReplyLangPref(t, db, "user-en-off", "en", 0)
	insertReplyLangUserMessage(t, db, "nar-en-off", "user-en-off")
	cwd := t.TempDir()
	prompt := svc.agentSystemPrompt(context.Background(), Narrator{ID: "nar-en-off", Cwd: &cwd, SystemPrompt: ptr("base")})
	if strings.Contains(prompt, "## Language") {
		t.Fatalf("en + reply-disabled must omit language section (TS condition false):\n%s", prompt)
	}
}

func ptr(s string) *string { return &s }
