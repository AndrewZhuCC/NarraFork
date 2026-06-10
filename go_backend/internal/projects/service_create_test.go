package projects

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitx "narrafork-go/internal/git"
)

func TestCreateInitializesProjectBackupDBGitignoreAndCommit(t *testing.T) {
	ctx := context.Background()
	gitPath := filepath.Join(t.TempDir(), "repo")
	sqlDB := openProjectBackupTestDB(t)
	defer sqlDB.Close()

	var committed []string
	svc := &Service{
		DB: sqlDB,
		CloneStreamer: func(
			_ context.Context,
			_ string,
			path string,
			_ string,
			_ *gitx.CloneCredentials,
			_ func(string),
		) error {
			return os.MkdirAll(path, 0o755)
		},
		GitignoreCommitter: func(_ context.Context, path string) error {
			committed = append(committed, path)
			return nil
		},
	}

	project, err := svc.Create(ctx, CreateInput{
		Name:     "Project Backup Init",
		RepoMode: "clone",
		GitPath:  gitPath,
		CloneURL: "https://example.com/repo.git",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if project.ID == "" {
		t.Fatalf("project id is empty")
	}

	backup, err := sql.Open("sqlite", projectDBPath(gitPath))
	if err != nil {
		t.Fatalf("open project backup: %v", err)
	}
	defer backup.Close()
	if got := countWhere(t, backup, "projects", "id = '"+project.ID+"'"); got != 1 {
		t.Fatalf("backup project rows = %d, want 1", got)
	}
	gitignore, err := os.ReadFile(filepath.Join(gitPath, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(gitignore)
	if !strings.Contains(content, ".narrafork/\n") || !strings.Contains(content, ".worktrees/\n") {
		t.Fatalf(".gitignore content = %q", content)
	}
	if len(committed) != 1 || committed[0] != gitPath {
		t.Fatalf("gitignore commit calls = %#v", committed)
	}
}

func TestCreateCloneAuthFailureRemovesPartialDirectory(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	gitPath := filepath.Join(parent, "partial-clone")
	if err := os.MkdirAll(filepath.Join(gitPath, ".git"), 0o755); err != nil {
		t.Fatalf("seed partial clone: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitPath, "FETCH_HEAD"), []byte("partial"), 0o644); err != nil {
		t.Fatalf("seed partial clone file: %v", err)
	}

	var removed []string
	svc := &Service{
		CloneStreamer: func(
			_ context.Context,
			cloneURL string,
			path string,
			branch string,
			credentials *gitx.CloneCredentials,
			progress func(string),
		) error {
			if cloneURL != "https://example.com/private/repo.git" {
				t.Fatalf("cloneURL = %q", cloneURL)
			}
			if path != gitPath {
				t.Fatalf("git path = %q, want %q", path, gitPath)
			}
			if branch != "main" {
				t.Fatalf("branch = %q", branch)
			}
			if credentials == nil || credentials.Username != "u" || credentials.Password != "p" {
				t.Fatalf("credentials = %#v", credentials)
			}
			if progress != nil {
				progress("auth failed")
			}
			return errors.New("authentication failed: terminal prompts disabled")
		},
		CloneFailureDirectoryRemover: func(path string) error {
			removed = append(removed, path)
			return os.RemoveAll(path)
		},
	}

	progress := []string{}
	_, err := svc.Create(ctx, CreateInput{
		Name:          "Private Project",
		RepoMode:      "clone",
		GitPath:       gitPath,
		CloneURL:      "https://example.com/private/repo.git",
		CloneBranch:   "main",
		CloneUsername: "u",
		ClonePassword: "p",
		Progress: func(message string) {
			progress = append(progress, message)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("Create error = %v", err)
	}
	if len(removed) != 1 || removed[0] != gitPath {
		t.Fatalf("removed paths = %#v", removed)
	}
	if _, statErr := os.Stat(gitPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial clone directory still exists or stat failed with different error: %v", statErr)
	}
	if len(progress) != 1 || progress[0] != "auth failed" {
		t.Fatalf("progress = %#v", progress)
	}
}
