package narrators

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"narrafork-go/internal/config"

	_ "modernc.org/sqlite"
)

// TestGenerateNarratorTitle verifies the W638 generate-title implementation:
// (1) no messages -> "New conversation"; (2) with messages, the stubbed summary
// model receives an (early)/(recent)-annotated conversation and its sanitized
// output becomes the title; (3) when generation fails, it falls back to the
// existing title.
func TestGenerateNarratorTitle(t *testing.T) {
	sqlDB := openTitleGenTestDB(t)
	defer sqlDB.Close()

	// Stub the provider call: echo back a fixed title and capture the prompt.
	var capturedUser string
	orig := titleGenGenerate
	titleGenGenerate = func(_ context.Context, _ *Service, _ /*model*/, _ /*sys*/, userContent, _ /*cwd*/ string) (string, error) {
		capturedUser = userContent
		return `  "Refactor auth flow"  `, nil
	}
	defer func() { titleGenGenerate = orig }()

	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	service := &Service{DB: sqlDB, Settings: settings}

	// No messages -> "New conversation".
	if got := service.generateNarratorTitle(context.Background(), "nar_empty", "", "en", "Old"); got != "New conversation" {
		t.Fatalf("no-message narrator should yield 'New conversation', got %q", got)
	}

	// Seed earliest + recent messages.
	insertTitleGenMessage(t, sqlDB, "nar_1", "m1", 1, "user", "first request about login")
	insertTitleGenMessage(t, sqlDB, "nar_1", "m2", 2, "assistant", "working on it")
	insertTitleGenMessage(t, sqlDB, "nar_1", "m3", 3, "user", "now refactor the auth module")

	got := service.generateNarratorTitle(context.Background(), "nar_1", "/tmp", "en", "Old")
	if got != "Refactor auth flow" {
		t.Fatalf("title should be the sanitized model output, got %q", got)
	}
	// The conversation prompt must annotate (early) and (recent) sections.
	if !containsAll(capturedUser, "(early) [User]: first request about login", "(recent)", "<conversation>") {
		t.Fatalf("conversation prompt missing expected sections: %q", capturedUser)
	}

	// Generation failure -> fall back to the existing title.
	titleGenGenerate = func(_ context.Context, _ *Service, _, _, _, _ string) (string, error) {
		return "", errors.New("provider down")
	}
	if got := service.generateNarratorTitle(context.Background(), "nar_1", "/tmp", "en", "Existing"); got != "Existing" {
		t.Fatalf("generation failure should fall back to existing title, got %q", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func openTitleGenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, role TEXT, content_text TEXT)`,
		`CREATE TABLE narrator_message_refs (narrator_id TEXT, message_id TEXT, seq INTEGER)`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("create title-gen schema: %v", err)
		}
	}
	return sqlDB
}

func insertTitleGenMessage(t *testing.T, sqlDB *sql.DB, narratorID, msgID string, seq int, role, content string) {
	t.Helper()
	if _, err := sqlDB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_text) VALUES (?, ?, ?, ?)`, msgID, narratorID, role, content); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO narrator_message_refs (narrator_id, message_id, seq) VALUES (?, ?, ?)`, narratorID, msgID, seq); err != nil {
		t.Fatalf("insert ref: %v", err)
	}
}

func TestBuildProvisionalTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"Fix the login bug. Then refactor.", "Fix the login bug."},
		// CJK with no whitespace after the first 。: like TS, the sentence regex
		// requires \s|$ after the terminator, so the whole string (ending 。$) matches.
		{"修复登录问题。然后重构。", "修复登录问题。然后重构。"},
		// CJK with a space after the first 。 -> first sentence only.
		{"修复登录问题。 然后重构。", "修复登录问题。"},
		{"no terminator here just a phrase", "no terminator here just a phrase"},
		// 60 chars, no sentence terminator -> first 49 runes + ellipsis.
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa…"},
	}
	for _, c := range cases {
		if got := buildProvisionalTitle(c.in); got != c.want {
			t.Fatalf("buildProvisionalTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestGenerateQuickTitlePromptAndTruncation verifies the quick-title flow builds
// a <user_message> prompt and truncates the user message to 500 UTF-16 units +
// "..." (mirroring TS generateQuickTitle). Persist is best-effort and not asserted
// here (covered by the title-sync tests).
func TestGenerateQuickTitlePromptAndTruncation(t *testing.T) {
	var capturedSys, capturedUser string
	orig := titleGenGenerate
	titleGenGenerate = func(_ context.Context, _ *Service, _, sys, userContent, _ string) (string, error) {
		capturedSys = sys
		capturedUser = userContent
		return "ignored", nil
	}
	defer func() { titleGenGenerate = orig }()

	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("config load: %v", err)
	}
	// generateQuickTitle requires a non-nil DB (guard); persist is best-effort and
	// its UpdateTextField no-ops on this minimal schema without failing the test.
	sqlDB := openTitleGenTestDB(t)
	defer sqlDB.Close()
	service := &Service{DB: sqlDB, Settings: settings}

	long := strings.Repeat("x", 600)
	service.generateQuickTitle(context.Background(), "nar_1", long, "/tmp", "en")

	if capturedUser == "" {
		t.Fatal("quick-title provider call was not made")
	}
	if !strings.Contains(capturedUser, "<user_message>") || !strings.Contains(capturedUser, "...") {
		t.Fatalf("quick-title prompt should wrap and truncate the user message, got %q", capturedUser)
	}
	// 500 'x' + "..." + the surrounding tags; the raw message portion must be capped.
	if strings.Count(capturedUser, "x") != 500 {
		t.Fatalf("user message should be truncated to 500 chars, got %d x's", strings.Count(capturedUser, "x"))
	}
	if !strings.Contains(capturedSys, "title generator") {
		t.Fatalf("quick-title system prompt missing, got %q", capturedSys)
	}
}
