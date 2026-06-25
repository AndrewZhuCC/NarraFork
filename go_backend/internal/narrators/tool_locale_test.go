package narrators

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// openToolLocaleTestDB builds a minimal in-memory schema with just the columns
// toolLocale needs: narrator_messages (owner resolution source) and
// user_preferences (language lookup). The sqlite driver is registered package-wide
// by other narrators tests.
func openToolLocaleTestDB(t *testing.T) *sql.DB {
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
			language TEXT
		)`,
		// narrators WITHOUT a created_by column, mirroring the real schema, so the
		// old (buggy) query can be proven to fail.
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, title TEXT)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("create schema: %v\n%s", err, s)
		}
	}
	return db
}

func insertToolLocaleUserMessage(t *testing.T, db *sql.DB, id, narratorID, role, parentToolUseID, createdBy string, order int) {
	t.Helper()
	createdAt := time.Date(2026, 1, 1, 0, order, 0, 0, time.UTC).Format(time.RFC3339Nano)
	var parent any
	if parentToolUseID != "" {
		parent = parentToolUseID
	}
	if _, err := db.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`, id, narratorID, role, parent, createdBy, createdAt); err != nil {
		t.Fatalf("insert message: %v", err)
	}
}

func insertToolLocalePref(t *testing.T, db *sql.DB, userID, language string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO user_preferences (user_id, language) VALUES (?, ?)`, userID, language); err != nil {
		t.Fatalf("insert pref: %v", err)
	}
}

// TestToolLocaleQueryingNarratorsCreatedByColumnFails is the factual evidence
// (W804) that the previous toolLocale query was a real bug: the narrators table
// has no created_by column, so `SELECT created_by FROM narrators` fails at
// runtime, which made toolLocale always fall back to "en".
func TestToolLocaleQueryingNarratorsCreatedByColumnFails(t *testing.T) {
	db := openToolLocaleTestDB(t)
	if _, err := db.Exec(`INSERT INTO narrators (id, title) VALUES ('nar-1', 'x')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	var createdBy sql.NullString
	err := db.QueryRow(`SELECT created_by FROM narrators WHERE id = ?`, "nar-1").Scan(&createdBy)
	if err == nil {
		t.Fatal("expected the old query to fail (narrators has no created_by column), but it succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "no such column") {
		t.Fatalf("expected a 'no such column' error, got: %v", err)
	}
}

func TestToolLocaleResolvesOwnerFromEarliestUserMessage(t *testing.T) {
	db := openToolLocaleTestDB(t)
	insertToolLocalePref(t, db, "user-zh", "zh-CN")
	insertToolLocaleUserMessage(t, db, "m1", "nar-1", "user", "", "user-zh", 1)
	svc := &Service{DB: db}
	if got := svc.toolLocale(context.Background(), "nar-1"); got != "zh-CN" {
		t.Fatalf("toolLocale = %q, want zh-CN (owner from earliest user message)", got)
	}
}

func TestToolLocaleDefaultsToEnWhenNoOwnerMessage(t *testing.T) {
	db := openToolLocaleTestDB(t)
	svc := &Service{DB: db}
	if got := svc.toolLocale(context.Background(), "nar-empty"); got != "en" {
		t.Fatalf("toolLocale with no user message = %q, want en", got)
	}
}

func TestToolLocaleDefaultsToEnForEnglishUser(t *testing.T) {
	db := openToolLocaleTestDB(t)
	insertToolLocalePref(t, db, "user-en", "en")
	insertToolLocaleUserMessage(t, db, "m1", "nar-1", "user", "", "user-en", 1)
	svc := &Service{DB: db}
	if got := svc.toolLocale(context.Background(), "nar-1"); got != "en" {
		t.Fatalf("toolLocale = %q, want en for english user", got)
	}
}

func TestToolLocaleIgnoresSubagentChildMessages(t *testing.T) {
	db := openToolLocaleTestDB(t)
	insertToolLocalePref(t, db, "user-zh", "zh-CN")
	insertToolLocalePref(t, db, "user-en", "en")
	// Top-level user message owned by the zh-CN user.
	insertToolLocaleUserMessage(t, db, "m1", "nar-1", "user", "", "user-zh", 1)
	// A later subagent message-tree entry (parent_tool_use_id set) owned by another
	// user must NOT override the top-level owner.
	insertToolLocaleUserMessage(t, db, "m2", "nar-1", "user", "tool_abc", "user-en", 2)
	svc := &Service{DB: db}
	if got := svc.toolLocale(context.Background(), "nar-1"); got != "zh-CN" {
		t.Fatalf("toolLocale = %q, want zh-CN (only top-level user message owner counts)", got)
	}
}
