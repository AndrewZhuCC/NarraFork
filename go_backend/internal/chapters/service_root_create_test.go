package chapters

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	gitx "narrafork-go/internal/git"
)

func TestCreateRootChapterBroadcastsCreatedEventAndSyncsInitialCommits(t *testing.T) {
	ctx := context.Background()
	database := openRootCreateTestDB(t)
	defer database.Close()

	broadcasts := []map[string]any{}
	var logBranch string
	var logLimit int
	service := &Service{
		DB: database,
		RootCommitMetadataReader: func(_ context.Context, gitPath string) (string, int) {
			if gitPath != "/repo" {
				t.Fatalf("metadata gitPath = %q", gitPath)
			}
			return "sha2", 2
		},
		RootCommitLogReader: func(_ context.Context, gitPath, branch string, limit int) ([]gitx.Commit, error) {
			if gitPath != "/repo" {
				t.Fatalf("log gitPath = %q", gitPath)
			}
			logBranch = branch
			logLimit = limit
			return []gitx.Commit{
				{SHA: "sha2", Message: "second", FullMessage: "second body", AuthorName: "Ada", AuthorEmail: "ada@example.com", AuthoredAt: "2026-06-09T02:00:00Z"},
				{SHA: "sha1", Message: "first", FullMessage: "first body", AuthorName: "Ada", AuthorEmail: "ada@example.com", AuthoredAt: "2026-06-09T01:00:00Z"},
			}, nil
		},
		BroadcastChapterLifecycle: func(payload any) {
			if event, ok := payload.(map[string]any); ok {
				broadcasts = append(broadcasts, event)
			}
		},
	}

	chapter, err := service.CreateRootChapter(ctx, RootInput{
		ProjectID:     "project-1",
		Title:         "Root",
		GitPath:       "/repo",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("CreateRootChapter: %v", err)
	}
	if !chapter.IsRoot || chapter.ProjectID != "project-1" || chapter.Branch != "main" || chapter.WorktreePath == nil || *chapter.WorktreePath != "/repo" {
		t.Fatalf("unexpected root chapter: %#v", chapter)
	}
	if chapter.HeadCommitSHA == nil || *chapter.HeadCommitSHA != "sha2" || chapter.CommitCount != 2 {
		t.Fatalf("root commit metadata = head:%#v count:%d", chapter.HeadCommitSHA, chapter.CommitCount)
	}
	if logBranch != "main" || logLimit != rootChapterCommitSyncLimit {
		t.Fatalf("root commit log call = branch:%q limit:%d", logBranch, logLimit)
	}
	if len(broadcasts) != 1 || broadcasts[0]["type"] != "chapter:created" || broadcasts[0]["chapterId"] != chapter.ID || broadcasts[0]["projectId"] != "project-1" {
		t.Fatalf("created broadcasts = %#v", broadcasts)
	}

	var synced int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = ? AND source = 'manual'`, chapter.ID).Scan(&synced); err != nil {
		t.Fatalf("count synced commits: %v", err)
	}
	if synced != 2 {
		t.Fatalf("synced manual commits = %d, want 2", synced)
	}
	var fullMessage, authorName string
	if err := database.QueryRow(`SELECT full_message, author_name FROM chapter_commits WHERE chapter_id = ? AND sha = 'sha2'`, chapter.ID).Scan(&fullMessage, &authorName); err != nil {
		t.Fatalf("query synced commit: %v", err)
	}
	if fullMessage != "second body" || authorName != "Ada" {
		t.Fatalf("synced commit fields = full:%q author:%q", fullMessage, authorName)
	}
}

func TestCreateRootChapterCommitSyncFailureIsNonFatal(t *testing.T) {
	ctx := context.Background()
	database := openRootCreateTestDB(t)
	defer database.Close()

	broadcasts := 0
	service := &Service{
		DB: database,
		RootCommitMetadataReader: func(context.Context, string) (string, int) {
			return "pre-sync-head", 7
		},
		RootCommitLogReader: func(context.Context, string, string, int) ([]gitx.Commit, error) {
			return nil, errors.New("deterministic commit sync failure")
		},
		BroadcastChapterLifecycle: func(payload any) {
			if event, ok := payload.(map[string]any); ok && event["type"] == "chapter:created" {
				broadcasts++
			}
		},
	}

	chapter, err := service.CreateRootChapter(ctx, RootInput{
		ProjectID:     "project-1",
		Title:         "Root",
		GitPath:       "/repo",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("CreateRootChapter should survive sync failure: %v", err)
	}
	if broadcasts != 1 {
		t.Fatalf("created broadcasts = %d, want 1", broadcasts)
	}
	if chapter.HeadCommitSHA == nil || *chapter.HeadCommitSHA != "pre-sync-head" || chapter.CommitCount != 7 {
		t.Fatalf("chapter metadata after failed sync = head:%#v count:%d", chapter.HeadCommitSHA, chapter.CommitCount)
	}
	var rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = ?`, chapter.ID).Scan(&rows); err != nil {
		t.Fatalf("count synced commits: %v", err)
	}
	if rows != 0 {
		t.Fatalf("commit rows after failed sync = %d, want 0", rows)
	}
}

func openRootCreateTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	stmts := []string{
		`CREATE TABLE chapters (
			id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT,
			branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT,
			merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT,
			container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT,
			start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER,
			anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER,
			panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT,
			last_accessed_at TEXT, created_at TEXT, updated_at TEXT
		)`,
		`CREATE TABLE chapter_commits (
			id TEXT PRIMARY KEY, chapter_id TEXT, sha TEXT, message TEXT, full_message TEXT,
			author_name TEXT, author_email TEXT, authored_at TEXT, source TEXT,
			narrator_id TEXT, narrator_message_id TEXT, files_changed INTEGER,
			lines_added INTEGER, lines_removed INTEGER, created_at TEXT
		)`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			database.Close()
			t.Fatalf("create test schema: %v", err)
		}
	}
	return database
}
