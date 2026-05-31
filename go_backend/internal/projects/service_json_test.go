package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestProjectChapterSettingsResponseDecodesJSON(t *testing.T) {
	sqlDB := openProjectJSONTestDB(t)
	defer sqlDB.Close()
	settings := `{"commands":[{"name":"ship"}],"routines":{"enabledRoutines":["review"]}}`
	insertProjectJSONTestRow(t, sqlDB, settings)

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

func TestProjectUpdateMergesChapterSettingsWithExistingValues(t *testing.T) {
	sqlDB := openProjectJSONTestDB(t)
	defer sqlDB.Close()
	insertProjectJSONTestRow(t, sqlDB, `{"commands":[{"name":"ship"}],"routines":{"enabledRoutines":["review"]},"requireReviewBeforeMerge":true}`)

	project, err := (&Service{DB: sqlDB}).Update(context.Background(), "p1", map[string]any{
		"chapterSettings": map[string]any{
			"routines":             map[string]any{"enabledRoutines": []any{"test"}},
			"autoCreateNarrator":  false,
			"autoStartContainers": true,
		},
	})
	if err != nil {
		t.Fatalf("update project: %v", err)
	}
	settings, ok := project.ChapterSettings.(map[string]any)
	if !ok {
		t.Fatalf("chapterSettings = %#v, want object", project.ChapterSettings)
	}
	if _, ok := settings["commands"].([]any); !ok {
		t.Fatalf("existing commands should be preserved, got %#v", settings)
	}
	if settings["requireReviewBeforeMerge"] != true || settings["autoCreateNarrator"] != false {
		t.Fatalf("boolean settings not merged correctly: %#v", settings)
	}
	if _, ok := settings["autoStartContainers"]; ok {
		t.Fatalf("unsupported chapterSettings keys should be stripped to match TS schema: %#v", settings)
	}
	routines := settings["routines"].(map[string]any)
	enabled := routines["enabledRoutines"].([]any)
	if len(enabled) != 1 || enabled[0] != "test" {
		t.Fatalf("incoming setting should override same top-level key, got %#v", settings)
	}

	var raw string
	if err := sqlDB.QueryRow(`SELECT chapter_settings FROM projects WHERE id = 'p1'`).Scan(&raw); err != nil {
		t.Fatalf("load raw chapter settings: %v", err)
	}
	if strings.Contains(raw, `"chapterSettings"`) || strings.Contains(raw, `"autoStartContainers"`) || !strings.Contains(raw, `"autoCreateNarrator":false`) {
		t.Fatalf("stored chapter_settings should be merged TS-schema object JSON, got %s", raw)
	}
}

func TestProjectUpdateMergesChapterSettingsAfterCorruptJSON(t *testing.T) {
	sqlDB := openProjectJSONTestDB(t)
	defer sqlDB.Close()
	insertProjectJSONTestRow(t, sqlDB, `not-json`)

	project, err := (&Service{DB: sqlDB}).Update(context.Background(), "p1", map[string]any{"chapterSettings": map[string]any{"requireReviewBeforeMerge": true}})
	if err != nil {
		t.Fatalf("update project with corrupt existing settings: %v", err)
	}
	settings := project.ChapterSettings.(map[string]any)
	if len(settings) != 1 || settings["requireReviewBeforeMerge"] != true {
		t.Fatalf("corrupt existing settings should be replaced by incoming object, got %#v", settings)
	}
}

func TestProjectUpdateValidatesTSProjectSchemaEnumsAndLengths(t *testing.T) {
	sqlDB := openProjectJSONTestDB(t)
	defer sqlDB.Close()
	insertProjectJSONTestRow(t, sqlDB, `{}`)
	service := &Service{DB: sqlDB}

	cases := []struct {
		name string
		data map[string]any
		want string
	}{
		{name: "empty name", data: map[string]any{"name": ""}, want: "name must be a non-empty string up to 200 characters"},
		{name: "invalid status", data: map[string]any{"status": "deleted"}, want: "status must be active or archived"},
		{name: "invalid branch", data: map[string]any{"defaultBranch": "--delete main"}, want: "Invalid branch name"},
		{name: "long proxy", data: map[string]any{"proxyDomain": strings.Repeat("x", 201)}, want: "proxyDomain must be a string up to 200 characters"},
		{name: "chapter settings type", data: map[string]any{"chapterSettings": "not-an-object"}, want: "chapterSettings must be an object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Update(context.Background(), "p1", tc.data)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q validation error, got %v", tc.want, err)
			}
		})
	}
}

func TestProjectUpdateIgnoresFlowModeLikeTSSchema(t *testing.T) {
	sqlDB := openProjectJSONTestDB(t)
	defer sqlDB.Close()
	insertProjectJSONTestRow(t, sqlDB, `{}`)

	project, err := (&Service{DB: sqlDB}).Update(context.Background(), "p1", map[string]any{"flowMode": "ruler", "name": "Renamed"})
	if err != nil {
		t.Fatalf("update project: %v", err)
	}
	if project.Name != "Renamed" || project.FlowMode != "classic" {
		t.Fatalf("flowMode should not be mutable through PATCH, got name=%q flowMode=%q", project.Name, project.FlowMode)
	}
}

func openProjectJSONTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = sqlDB.Exec(`CREATE TABLE projects (
		id TEXT PRIMARY KEY, name TEXT, description TEXT, status TEXT, flow_mode TEXT, git_path TEXT,
		remote_url TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT,
		chapter_settings TEXT, proxy_domain TEXT, created_at TEXT, updated_at TEXT
	)`)
	if err != nil {
		sqlDB.Close()
		t.Fatalf("create projects table: %v", err)
	}
	return sqlDB
}

func insertProjectJSONTestRow(t *testing.T, sqlDB *sql.DB, chapterSettings string) {
	t.Helper()
	_, err := sqlDB.Exec(`INSERT INTO projects (id, name, status, flow_mode, chapter_settings, created_at, updated_at) VALUES ('p1', 'Project', 'active', 'classic', ?, 'now', 'now')`, chapterSettings)
	if err != nil {
		t.Fatalf("insert project: %v", err)
	}
}
