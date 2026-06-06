package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"narrafork-go/internal/compat/bunfinal"
)

type roundtripFixture struct {
	Rows []roundtripFixtureRow `json:"rows"`
}

type roundtripFixtureRow struct {
	Table  string            `json:"table"`
	Values map[string]string `json:"values"`
}

type bunRoundtripSummary struct {
	ProjectName     string `json:"projectName"`
	ProjectStatus   string `json:"projectStatus"`
	ProjectGitPath  string `json:"projectGitPath"`
	ChapterTitle    string `json:"chapterTitle"`
	ChapterBranch   string `json:"chapterBranch"`
	NarratorTitle   string `json:"narratorTitle"`
	NarratorStatus  string `json:"narratorStatus"`
	GoalObjective   string `json:"goalObjective"`
	GoalStatus      string `json:"goalStatus"`
	MessageText     string `json:"messageText"`
	MessageProvider string `json:"messageProvider"`
	MessageModel    string `json:"messageModel"`
	RefSeq          string `json:"refSeq"`
	TerminalStatus  string `json:"terminalStatus"`
	TerminalCwd     string `json:"terminalCwd"`
	GoJournalStatus string `json:"goJournalStatus"`
	GoJournalTag    string `json:"goJournalTag"`
	ProjectsCount   int    `json:"projectsCount"`
	ChaptersCount   int    `json:"chaptersCount"`
	NarratorsCount  int    `json:"narratorsCount"`
	GoalsCount      int    `json:"goalsCount"`
	MessagesCount   int    `json:"messagesCount"`
	RefsCount       int    `json:"refsCount"`
	TerminalsCount  int    `json:"terminalsCount"`
}

func TestGoRoundtripFixtureSurvivesBunReadAndGoReopen(t *testing.T) {
	dbPath, fixture := prepareRoundtripFixtureDatabase(t)
	store, err := Open(context.Background())
	if err != nil {
		t.Fatalf("open roundtrip fixture database: %v", err)
	}

	insertRoundtripFixtureRows(t, store.SQL, fixture)
	beforeSnapshot, err := SnapshotSQLiteSchema(context.Background(), store.SQL, DefaultSchemaSnapshotIgnoredObjects())
	if err != nil {
		t.Fatalf("snapshot roundtrip database before Bun read: %v", err)
	}
	beforeJournal := readGoMigrationJournalForTest(t, store.RuntimeSQL)
	if err := store.Close(); err != nil {
		t.Fatalf("close roundtrip fixture database before Bun read: %v", err)
	}

	summary := runBunRoundtripSummary(t, dbPath)
	rowsByTable := map[string]map[string]string{}
	for _, row := range fixture.Rows {
		rowsByTable[row.Table] = row.Values
	}
	want := func(table, column string) string {
		value, ok := rowsByTable[table][column]
		if !ok {
			t.Fatalf("roundtrip fixture missing %s.%s", table, column)
		}
		return value
	}
	if summary.ProjectName != want("projects", "name") || summary.ProjectStatus != want("projects", "status") || summary.ProjectGitPath != want("projects", "git_path") {
		t.Fatalf("Bun project summary mismatch: %#v want=%#v", summary, rowsByTable["projects"])
	}
	if summary.ChapterTitle != want("chapters", "title") || summary.ChapterBranch != want("chapters", "branch") {
		t.Fatalf("Bun chapter summary mismatch: %#v want=%#v", summary, rowsByTable["chapters"])
	}
	if summary.NarratorTitle != want("narrators", "title") || summary.NarratorStatus != want("narrators", "status") {
		t.Fatalf("Bun narrator summary mismatch: %#v want=%#v", summary, rowsByTable["narrators"])
	}
	if summary.GoalObjective != want("narrator_goals", "objective") || summary.GoalStatus != want("narrator_goals", "status") {
		t.Fatalf("Bun goal summary mismatch: %#v want=%#v", summary, rowsByTable["narrator_goals"])
	}
	if summary.MessageText != want("narrator_messages", "content_text") || summary.MessageProvider != want("narrator_messages", "provider") || summary.MessageModel != want("narrator_messages", "model") {
		t.Fatalf("Bun message summary mismatch: %#v want=%#v", summary, rowsByTable["narrator_messages"])
	}
	if summary.RefSeq != want("narrator_message_refs", "seq") {
		t.Fatalf("Bun message ref summary mismatch: %#v want=%#v", summary, rowsByTable["narrator_message_refs"])
	}
	if summary.TerminalStatus != want("terminals", "status") || summary.TerminalCwd != want("terminals", "cwd") {
		t.Fatalf("Bun terminal summary mismatch: %#v want=%#v", summary, rowsByTable["terminals"])
	}
	if summary.GoJournalStatus != "sidecar" || summary.GoJournalTag != "sidecar" {
		t.Fatalf("Bun should not read Go sidecar journal from shared DB, got %#v", summary)
	}
	if summary.ProjectsCount != 1 || summary.ChaptersCount != 1 || summary.NarratorsCount != 1 || summary.GoalsCount != 1 || summary.MessagesCount != 1 || summary.RefsCount != 1 || summary.TerminalsCount != 1 {
		t.Fatalf("Bun row counts mismatch: %#v", summary)
	}

	reopened, err := Open(context.Background())
	if err != nil {
		t.Fatalf("reopen roundtrip fixture database after Bun read: %v", err)
	}
	defer reopened.Close()
	afterSnapshot, err := SnapshotSQLiteSchema(context.Background(), reopened.SQL, DefaultSchemaSnapshotIgnoredObjects())
	if err != nil {
		t.Fatalf("snapshot roundtrip database after Bun read: %v", err)
	}
	if beforeSnapshot.Hash != afterSnapshot.Hash {
		t.Fatalf("schema hash changed across Go->Bun->Go roundtrip: before=%s after=%s", beforeSnapshot.Hash, afterSnapshot.Hash)
	}
	afterJournal := readGoMigrationJournalForTest(t, reopened.RuntimeSQL)
	if beforeJournal.ExpectedJournalHash != afterJournal.ExpectedJournalHash || beforeJournal.CanonicalSchemaHash != afterJournal.CanonicalSchemaHash || beforeJournal.Status != afterJournal.Status || beforeJournal.ExpectedLastTag != afterJournal.ExpectedLastTag {
		t.Fatalf("Go migration journal changed across Bun roundtrip: before=%#v after=%#v", beforeJournal, afterJournal)
	}
	if err := verifyDrizzleMigrationTableShapeForBunRollback(context.Background(), reopened.SQL); err != nil {
		t.Fatalf("Bun rollback journal shape check failed after roundtrip: %v", err)
	}
	get := func(query string, args ...any) string {
		var got string
		if err := reopened.SQL.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		return got
	}
	if got := get(`SELECT name FROM projects WHERE id = ?`, want("projects", "id")); got != want("projects", "name") {
		t.Fatalf("Go reopened project name = %q, want %q", got, want("projects", "name"))
	}
	if got := get(`SELECT title FROM chapters WHERE id = ?`, want("chapters", "id")); got != want("chapters", "title") {
		t.Fatalf("Go reopened chapter title = %q, want %q", got, want("chapters", "title"))
	}
	if got := get(`SELECT content_text FROM narrator_messages WHERE id = ?`, want("narrator_messages", "id")); got != want("narrator_messages", "content_text") {
		t.Fatalf("Go reopened narrator message text = %q, want %q", got, want("narrator_messages", "content_text"))
	}
	if got := get(`SELECT objective FROM narrator_goals WHERE id = ?`, want("narrator_goals", "id")); got != want("narrator_goals", "objective") {
		t.Fatalf("Go reopened narrator goal objective = %q, want %q", got, want("narrator_goals", "objective"))
	}
}

func prepareRoundtripFixtureDatabase(t *testing.T) (string, roundtripFixture) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dbPath := filepath.Join(home, ".narrafork", "narrafork.db")
	cfg, err := NewConfig(ConfigInput{
		SQLitePath:          dbPath,
		SQLiteBootstrapMode: string(SQLiteBootstrapModeBunFinalCompatible),
	})
	if err != nil {
		t.Fatalf("create legacy bootstrap config for roundtrip fixture: %v", err)
	}
	store, err := OpenWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("bootstrap roundtrip fixture database in explicit legacy mode: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close bootstrapped roundtrip fixture database: %v", err)
	}
	return dbPath, loadRoundtripFixture(t)
}

func loadRoundtripFixture(t *testing.T) roundtripFixture {
	data, err := bunfinal.ReadRoundtripFixture()
	if err != nil {
		t.Fatalf("read bun-final roundtrip fixture: %v", err)
	}
	var fixture roundtripFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode roundtrip fixture: %v", err)
	}
	if len(fixture.Rows) == 0 {
		t.Fatal("roundtrip fixture should contain rows")
	}
	return fixture
}

func insertRoundtripFixtureRows(t *testing.T, sqlDB *sql.DB, fixture roundtripFixture) {
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin roundtrip fixture transaction: %v", err)
	}
	defer tx.Rollback()
	for _, row := range fixture.Rows {
		columns := make([]string, 0, len(row.Values))
		for column := range row.Values {
			columns = append(columns, column)
		}
		sort.Strings(columns)
		placeholders := make([]string, len(columns))
		args := make([]any, len(columns))
		for i, column := range columns {
			placeholders[i] = "?"
			args[i] = row.Values[column]
		}
		stmt := "INSERT INTO " + row.Table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
		if _, err := tx.ExecContext(context.Background(), stmt, args...); err != nil {
			t.Fatalf("insert roundtrip fixture row into %s: %v", row.Table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit roundtrip fixture transaction: %v", err)
	}
}

func runBunRoundtripSummary(t *testing.T, dbPath string) bunRoundtripSummary {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is required for TS/Bun roundtrip fixture verification")
	}
	scriptPath := writeBunRoundtripScript(t)
	cmd := exec.Command("bun", scriptPath, dbPath)
	cmd.Dir = repositoryRootWithDrizzle(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run Bun roundtrip verifier: %v output=%s", err, string(output))
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	payload := ""
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "ROUNDTRIP:") {
			payload = strings.TrimPrefix(line, "ROUNDTRIP:")
			break
		}
	}
	if payload == "" {
		t.Fatalf("Bun roundtrip summary not found in output: %s", string(output))
	}
	var summary bunRoundtripSummary
	if err := json.Unmarshal([]byte(payload), &summary); err != nil {
		t.Fatalf("decode Bun roundtrip summary: %v output=%s", err, string(output))
	}
	return summary
}

func writeBunRoundtripScript(t *testing.T) string {
	repoRoot := repositoryRootWithDrizzle(t)
	importPath := "file://" + filepath.ToSlash(filepath.Join(repoRoot, "server", "db", "index.ts"))
	content := strings.ReplaceAll(`import { markDatabaseCleanShutdown, sqlite } from "__DB_INDEX_IMPORT__";

const project = sqlite.prepare("SELECT id, name, status, flow_mode, git_path FROM projects WHERE id = ?").get("proj_roundtrip") as Record<string, unknown> | undefined;
const chapter = sqlite.prepare("SELECT id, project_id, title, status, branch FROM chapters WHERE id = ?").get("chap_roundtrip") as Record<string, unknown> | undefined;
const narrator = sqlite.prepare("SELECT id, chapter_id, title, status, message_count FROM narrators WHERE id = ?").get("narr_roundtrip") as Record<string, unknown> | undefined;
const goal = sqlite.prepare("SELECT id, narrator_id, objective, status FROM narrator_goals WHERE id = ?").get("goal_roundtrip") as Record<string, unknown> | undefined;
const message = sqlite.prepare("SELECT id, narrator_id, role, content_text, provider, model FROM narrator_messages WHERE id = ?").get("msg_roundtrip") as Record<string, unknown> | undefined;
const ref = sqlite.prepare("SELECT id, narrator_id, message_id, seq, is_compact FROM narrator_message_refs WHERE id = ?").get("ref_roundtrip") as Record<string, unknown> | undefined;
const terminal = sqlite.prepare("SELECT id, chapter_id, narrator_id, name, cwd, status FROM terminals WHERE id = ?").get("term_roundtrip") as Record<string, unknown> | undefined;

if (!project || !chapter || !narrator || !goal || !message || !ref || !terminal) {
	throw new Error("roundtrip fixture rows are missing after TS/Bun startup");
}

const summary = {
	projectName: String(project.name ?? ""),
	projectStatus: String(project.status ?? ""),
	projectGitPath: String(project.git_path ?? ""),
	chapterTitle: String(chapter.title ?? ""),
	chapterBranch: String(chapter.branch ?? ""),
	narratorTitle: String(narrator.title ?? ""),
	narratorStatus: String(narrator.status ?? ""),
	goalObjective: String(goal.objective ?? ""),
	goalStatus: String(goal.status ?? ""),
	messageText: String(message.content_text ?? ""),
	messageProvider: String(message.provider ?? ""),
	messageModel: String(message.model ?? ""),
	refSeq: String(ref.seq ?? ""),
	terminalStatus: String(terminal.status ?? ""),
	terminalCwd: String(terminal.cwd ?? ""),
	goJournalStatus: "sidecar",
	goJournalTag: "sidecar",
	projectsCount: Number(sqlite.prepare("SELECT COUNT(*) AS count FROM projects").get()?.count ?? 0),
	chaptersCount: Number(sqlite.prepare("SELECT COUNT(*) AS count FROM chapters").get()?.count ?? 0),
	narratorsCount: Number(sqlite.prepare("SELECT COUNT(*) AS count FROM narrators").get()?.count ?? 0),
	goalsCount: Number(sqlite.prepare("SELECT COUNT(*) AS count FROM narrator_goals").get()?.count ?? 0),
	messagesCount: Number(sqlite.prepare("SELECT COUNT(*) AS count FROM narrator_messages").get()?.count ?? 0),
	refsCount: Number(sqlite.prepare("SELECT COUNT(*) AS count FROM narrator_message_refs").get()?.count ?? 0),
	terminalsCount: Number(sqlite.prepare("SELECT COUNT(*) AS count FROM terminals").get()?.count ?? 0),
};

console.log("ROUNDTRIP:" + JSON.stringify(summary));
markDatabaseCleanShutdown();
`, "__DB_INDEX_IMPORT__", importPath)
	path := filepath.Join(t.TempDir(), "roundtrip-fixture.ts")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write Bun roundtrip script: %v", err)
	}
	return path
}
