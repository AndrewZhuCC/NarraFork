package chapters

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/config"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/narrators"
)

func TestCreateBroadcastsCreatedEventAndSyncsCommits(t *testing.T) {
	ctx := context.Background()
	database := newForkParityDB(t)
	createChapterCommitTableForCreateLifecycle(t, database)
	repo := newForkParityRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("feature"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	runGit(t, repo, "add", "feature.txt")
	runGit(t, repo, "commit", "-m", "add feature")
	defaultBranch := gitOutput(t, repo, "branch", "--show-current")
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, ?, 'now', 'now')`, repo, defaultBranch); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	broadcasts := []map[string]any{}
	service := &Service{
		DB:  database,
		Git: gitx.NewService(),
		BroadcastChapterLifecycle: func(payload any) {
			if event, ok := payload.(map[string]any); ok {
				broadcasts = append(broadcasts, event)
			}
		},
	}
	description := "create lifecycle side effects"
	chapter, err := service.Create(ctx, CreateInput{ProjectID: "p1", Title: "Create Side Effects", Description: &description})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if chapter.ProjectID != "p1" || chapter.Status != "active" || chapter.Role != "branch" || chapter.WorktreePath == nil || *chapter.WorktreePath == "" {
		t.Fatalf("unexpected created chapter: %#v", chapter)
	}
	if len(broadcasts) != 1 || broadcasts[0]["type"] != "chapter:created" || broadcasts[0]["chapterId"] != chapter.ID || broadcasts[0]["projectId"] != "p1" {
		t.Fatalf("created broadcasts = %#v", broadcasts)
	}

	expectedHead := gitOutput(t, repo, "rev-parse", "HEAD")
	expectedCount := gitCommitCountForTest(t, repo)
	if chapter.HeadCommitSHA == nil || *chapter.HeadCommitSHA != expectedHead || chapter.CommitCount != expectedCount {
		t.Fatalf("created chapter commit metadata = head:%#v count:%d, want %s/%d", chapter.HeadCommitSHA, chapter.CommitCount, expectedHead, expectedCount)
	}
	var syncedCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = ?`, chapter.ID).Scan(&syncedCount); err != nil {
		t.Fatalf("count synced commits: %v", err)
	}
	if syncedCount != expectedCount {
		t.Fatalf("synced commits = %d, want %d", syncedCount, expectedCount)
	}
	var message, source string
	if err := database.QueryRow(`SELECT message, source FROM chapter_commits WHERE chapter_id = ? AND sha = ?`, chapter.ID, expectedHead).Scan(&message, &source); err != nil {
		t.Fatalf("query synced head commit: %v", err)
	}
	if message != "add feature" || source != "manual" {
		t.Fatalf("synced head fields = message:%q source:%q", message, source)
	}
}

func TestCreateCommitSyncFailureIsNonFatalAfterCreatedBroadcast(t *testing.T) {
	ctx := context.Background()
	database := newForkParityDB(t)
	createChapterCommitTableForCreateLifecycle(t, database)
	if _, err := database.Exec(`CREATE TRIGGER fail_create_commit_sync BEFORE INSERT ON chapter_commits BEGIN SELECT RAISE(ABORT, 'create commit sync blocked'); END;`); err != nil {
		t.Fatalf("create sync failure trigger: %v", err)
	}
	repo := newForkParityRepo(t)
	defaultBranch := gitOutput(t, repo, "branch", "--show-current")
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, ?, 'now', 'now')`, repo, defaultBranch); err != nil {
		t.Fatalf("insert project: %v", err)
	}

	broadcasts := 0
	service := &Service{
		DB:  database,
		Git: gitx.NewService(),
		BroadcastChapterLifecycle: func(payload any) {
			if event, ok := payload.(map[string]any); ok && event["type"] == "chapter:created" {
				broadcasts++
			}
		},
	}
	chapter, err := service.Create(ctx, CreateInput{ProjectID: "p1", Title: "Commit Sync Failure"})
	if err != nil {
		t.Fatalf("Create should survive commit sync failure: %v", err)
	}
	if broadcasts != 1 {
		t.Fatalf("created broadcasts = %d, want 1", broadcasts)
	}
	var chapterRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapters WHERE id = ?`, chapter.ID).Scan(&chapterRows); err != nil {
		t.Fatalf("count created chapter: %v", err)
	}
	if chapterRows != 1 {
		t.Fatalf("created chapter rows = %d, want 1", chapterRows)
	}
	var commitRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = ?`, chapter.ID).Scan(&commitRows); err != nil {
		t.Fatalf("count synced commits: %v", err)
	}
	if commitRows != 0 {
		t.Fatalf("commit rows after sync failure = %d, want 0", commitRows)
	}
}

func TestCreateDBFailureRollsBackBranchAndWorktree(t *testing.T) {
	ctx := context.Background()
	database := newForkParityDB(t)
	repo := newForkParityRepo(t)
	defaultBranch := gitOutput(t, repo, "branch", "--show-current")
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, ?, 'now', 'now')`, repo, defaultBranch); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_create_chapter_insert BEFORE INSERT ON chapters WHEN NEW.title = 'Broken DB' BEGIN SELECT RAISE(ABORT, 'chapter insert blocked'); END;`); err != nil {
		t.Fatalf("create chapter failure trigger: %v", err)
	}
	broadcasts := 0
	service := &Service{
		DB:  database,
		Git: gitx.NewService(),
		BroadcastChapterLifecycle: func(payload any) {
			broadcasts++
		},
	}

	_, err := service.Create(ctx, CreateInput{ProjectID: "p1", Title: "Broken DB"})
	if err == nil || !strings.Contains(err.Error(), "chapter insert blocked") {
		t.Fatalf("Create error = %v, want trigger failure", err)
	}
	if broadcasts != 0 {
		t.Fatalf("create failure broadcasts = %d, want 0", broadcasts)
	}
	if branches := strings.TrimSpace(gitOutput(t, repo, "branch", "--list", "chapter/broken-db-*")); branches != "" {
		t.Fatalf("rollback left created branch(es): %q", branches)
	}
	worktreesDir := filepath.Join(repo, ".worktrees")
	if entries, readErr := os.ReadDir(worktreesDir); readErr == nil && len(entries) > 0 {
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("rollback left worktree entries: %#v", names)
	} else if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read worktrees dir: %v", readErr)
	}
	var chapterRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapters WHERE title = 'Broken DB'`).Scan(&chapterRows); err != nil {
		t.Fatalf("count failed chapter rows: %v", err)
	}
	if chapterRows != 0 {
		t.Fatalf("failed create persisted %d chapter rows, want 0", chapterRows)
	}
}

func TestCreateSchedulesAutoDormantWhenActiveWorktreesExceedLimit(t *testing.T) {
	ctx := context.Background()
	database := newForkParityDB(t)
	createChapterCommitTableForCreateLifecycle(t, database)
	createDormantTablesForCreateLifecycle(t, database)
	settings := loadTempSettings(t)
	if _, err := settings.Patch(map[string]any{"chapters": map[string]any{"maxActiveWorktrees": float64(1)}}); err != nil {
		t.Fatalf("patch max active worktrees: %v", err)
	}
	repo := newForkParityRepo(t)
	defaultBranch := gitOutput(t, repo, "branch", "--show-current")
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, ?, 'now', 'now')`, repo, defaultBranch); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	service := &Service{
		DB:               database,
		Git:              gitx.NewService(),
		Narrators:        &narrators.Service{DB: database},
		Settings:         settings,
		AutoDormantDelay: 20 * time.Millisecond,
	}
	t.Cleanup(service.ClearAutoDormantTimers)

	first, err := service.Create(ctx, CreateInput{ProjectID: "p1", Title: "Older Active"})
	if err != nil {
		t.Fatalf("Create first chapter: %v", err)
	}
	if first.WorktreePath == nil || *first.WorktreePath == "" {
		t.Fatalf("first chapter missing worktree: %#v", first)
	}
	if _, err := database.Exec(`UPDATE chapters SET last_accessed_at = '2026-01-01T00:00:00.000Z' WHERE id = ?`, first.ID); err != nil {
		t.Fatalf("age first chapter: %v", err)
	}
	second, err := service.Create(ctx, CreateInput{ProjectID: "p1", Title: "Newer Active"})
	if err != nil {
		t.Fatalf("Create second chapter: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var firstStatus, secondStatus string
		var firstWorktree sql.NullString
		if err := database.QueryRow(`SELECT status, worktree_path FROM chapters WHERE id = ?`, first.ID).Scan(&firstStatus, &firstWorktree); err != nil {
			t.Fatalf("query first chapter: %v", err)
		}
		if err := database.QueryRow(`SELECT status FROM chapters WHERE id = ?`, second.ID).Scan(&secondStatus); err != nil {
			t.Fatalf("query second chapter: %v", err)
		}
		if firstStatus == "dormant" && !firstWorktree.Valid && secondStatus == "active" {
			if _, err := os.Stat(*first.WorktreePath); !os.IsNotExist(err) {
				t.Fatalf("first worktree still exists or stat failed unexpectedly: %v", err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("auto-dormant state first=%s worktree=%#v second=%s, want first dormant/null and second active", firstStatus, firstWorktree, secondStatus)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCreateHonorsProjectChapterSettingsAutoCreateNarrator(t *testing.T) {
	ctx := context.Background()
	database := newForkParityDB(t)
	createChapterCommitTableForCreateLifecycle(t, database)
	repo := newForkParityRepo(t)
	defaultBranch := gitOutput(t, repo, "branch", "--show-current")
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, chapter_settings, created_at, updated_at) VALUES ('p_disabled', 'Disabled Project', ?, ?, '{"autoCreateNarrator":false}', 'now', 'now')`, repo, defaultBranch); err != nil {
		t.Fatalf("insert disabled project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, chapter_settings, created_at, updated_at) VALUES ('p_enabled', 'Enabled Project', ?, ?, '{"autoCreateNarrator":true}', 'now', 'now')`, repo, defaultBranch); err != nil {
		t.Fatalf("insert enabled project: %v", err)
	}
	service := &Service{DB: database, Git: gitx.NewService(), Narrators: &narrators.Service{DB: database}}

	disabledChapter, err := service.Create(ctx, CreateInput{ProjectID: "p_disabled", Title: "No Narrator"})
	if err != nil {
		t.Fatalf("Create disabled project chapter: %v", err)
	}
	var disabledNarrators int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrators WHERE chapter_id = ?`, disabledChapter.ID).Scan(&disabledNarrators); err != nil {
		t.Fatalf("count disabled project narrators: %v", err)
	}
	if disabledNarrators != 0 {
		t.Fatalf("disabled autoCreateNarrator created %d narrators, want 0", disabledNarrators)
	}

	enabledChapter, err := service.Create(ctx, CreateInput{ProjectID: "p_enabled", Title: "With Narrator"})
	if err != nil {
		t.Fatalf("Create enabled project chapter: %v", err)
	}
	var narratorModel, narratorPermission string
	var narratorCWD sql.NullString
	if err := database.QueryRow(`SELECT model, permission_mode, cwd FROM narrators WHERE chapter_id = ? AND variant = 'primary'`, enabledChapter.ID).Scan(&narratorModel, &narratorPermission, &narratorCWD); err != nil {
		t.Fatalf("query enabled project narrator: %v", err)
	}
	if narratorModel != config.DefaultAgentModel || narratorPermission != "default" || narratorCWD.Valid {
		t.Fatalf("enabled narrator config = model:%q permission:%q cwd:%#v", narratorModel, narratorPermission, narratorCWD)
	}
}

func createDormantTablesForCreateLifecycle(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE terminal_tabs (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE terminal_view_state (id TEXT PRIMARY KEY, chapter_id TEXT)`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("create dormant lifecycle table: %v\n%s", err, stmt)
		}
	}
}

func createChapterCommitTableForCreateLifecycle(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`CREATE TABLE chapter_commits (
		id TEXT PRIMARY KEY, chapter_id TEXT, sha TEXT, message TEXT, full_message TEXT,
		author_name TEXT, author_email TEXT, authored_at TEXT, source TEXT,
		narrator_id TEXT, narrator_message_id TEXT, files_changed INTEGER,
		lines_added INTEGER, lines_removed INTEGER, created_at TEXT
	)`); err != nil {
		t.Fatalf("create chapter_commits table: %v", err)
	}
}
