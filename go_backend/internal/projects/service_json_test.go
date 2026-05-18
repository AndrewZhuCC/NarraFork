package projects

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"database/sql"
	_ "modernc.org/sqlite"
)

func TestProjectChapterSettingsResponseDecodesJSON(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	_, err = sqlDB.Exec(`CREATE TABLE projects (
		id TEXT PRIMARY KEY, name TEXT, description TEXT, status TEXT, flow_mode TEXT, git_path TEXT,
		remote_url TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT,
		chapter_settings TEXT, proxy_domain TEXT, created_at TEXT, updated_at TEXT
	)`)
	if err != nil {
		t.Fatalf("create projects table: %v", err)
	}
	settings := `{"commands":[{"name":"ship"}],"routines":{"enabledRoutines":["review"]}}`
	_, err = sqlDB.Exec(`INSERT INTO projects (id, name, status, flow_mode, chapter_settings, created_at, updated_at) VALUES ('p1', 'Project', 'active', 'classic', ?, 'now', 'now')`, settings)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}

	project, err := (&Service{DB: sqlDB}).Get(context.Background(), "p1")
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if _, ok := project.ChapterSettings.(map[string]any); !ok {
		t.Fatalf("chapterSettings = %#v, want object", project.ChapterSettings)
	}
	encoded, err := json.Marshal(project)
	if err != nil {
		t.Fatalf("marshal project: %v", err)
	}
	if strings.Contains(string(encoded), `"chapterSettings":"`) {
		t.Fatalf("chapterSettings encoded as JSON string: %s", encoded)
	}
}
