package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/chapters"
	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/narrators"
	"narrafork-go/internal/projects"
	"narrafork-go/internal/ws"
)

func TestPostgresPostMigrationFrontendSmoke(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("NARRAFORK_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("set NARRAFORK_TEST_POSTGRES_DSN to run live Postgres post-migration smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sqlDB, err := sql.Open((db.PostgresDialect{}).DriverName(), dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	schema := "narrafork_router_" + strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
	quotedSchema := (db.PostgresDialect{}).QuoteIdent(schema)
	if _, err := sqlDB.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer sqlDB.ExecContext(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	if _, err := sqlDB.ExecContext(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	for _, stmt := range append(db.PostgresBusinessSchemaDDL(), db.PostgresOptionalSchemaDDL()...) {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("exec postgres smoke schema: %v\n%s", err, stmt)
		}
	}
	insertPostgresSmokeFixtures(t, ctx, sqlDB)
	settings := loadRouterContractSettings(t)
	gitService := gitx.NewService()
	hub := ws.NewHub()
	narratorService := narrators.NewService(sqlDB, hub, settings)
	chapterService := chapters.NewService(sqlDB, gitService, narratorService, nil, settings)
	projectService := projects.NewService(sqlDB, gitService, chapterService, settings)
	if _, err := projectService.List(ctx, ""); err != nil {
		t.Fatalf("project list smoke failed before router: %v", err)
	}
	if _, err := projectService.Get(ctx, "project-1"); err != nil {
		t.Fatalf("project get smoke failed before router: %v", err)
	}
	if _, err := chapterService.List(ctx, "project-1"); err != nil {
		t.Fatalf("chapter list smoke failed before router: %v", err)
	}
	if _, err := chapterService.Get(ctx, "chapter-1"); err != nil {
		t.Fatalf("chapter get smoke failed before router: %v", err)
	}
	if _, err := narratorService.List(ctx, "chapter-1", "", false); err != nil {
		t.Fatalf("narrator list smoke failed before router: %v", err)
	}
	authService := auth.NewService(sqlDB, settings)
	router := NewRouter(Services{
		DB:        sqlDB,
		DBEngine:  db.EnginePostgres,
		Auth:      authService,
		Settings:  settings,
		Git:       gitService,
		Hub:       hub,
		Projects:  projectService,
		Chapters:  chapterService,
		Narrators: narratorService,
	})
	token, err := authService.CreateToken("user-1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	cases := []struct {
		name string
		path string
	}{
		{"health", "/api/health"},
		{"projects", "/api/projects"},
		{"project", "/api/projects/project-1"},
		{"graph", "/api/projects/project-1/graph"},
		{"chapters", "/api/chapters/?projectId=project-1"},
		{"chapter", "/api/chapters/chapter-1"},
		{"messages", "/api/narrators/narrator-1/messages?limit=20"},
		{"terminals", "/api/terminals?chapterId=chapter-1"},
		{"terminal-tabs", "/api/terminals/tabs?chapterId=chapter-1"},
		{"search", "/api/search?q=Alpha&entities=chapters,messages,narrators"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.path != "/api/health" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
			var body any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v\n%s", err, w.Body.String())
			}
		})
	}
}

func insertPostgresSmokeFixtures(t *testing.T, ctx context.Context, sqlDB *sql.DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('user-1', 'admin', 'hash', 'admin', '2026-05-18T00:00:00Z')`,
		`INSERT INTO projects (id, name, description, status, flow_mode, git_path, created_at, updated_at) VALUES ('project-1', 'Alpha Project', 'demo', 'active', 'classic', '/tmp/narrafork-alpha', '2026-05-18T00:00:00Z', '2026-05-18T00:00:00Z')`,
		`INSERT INTO chapters (id, project_id, title, description, status, role, branch, base_branch, worktree_path, is_root, commit_count, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('chapter-1', 'project-1', 'Alpha Chapter', 'demo chapter', 'active', 'branch', 'main', 'main', '/tmp/narrafork-alpha', 1, 1, 0, 0, 0, '2026-05-18T00:00:00Z', '2026-05-18T00:00:00Z')`,
		`INSERT INTO narrators (id, chapter_id, title, type, inherit_mode, status, model, plan_reflection_auto_approve_override, danger_reflection_override, message_count, message_version, total_cost_usd, permission_mode, variant, fast_mode, relaxed_plan, plan_mode, is_background, is_ask_in_passing, substatus, traits, background_status, background_result, created_at, updated_at) VALUES ('narrator-1', 'chapter-1', 'Alpha Narrator', 'primary', 'full', 'idle', 'test-model', 'inherit', 'inherit', 1, 1, 0, 'acceptEdits', 'primary', 0, 0, 0, 0, 0, '[]', '[]', '', '', '2026-05-18T00:00:00Z', '2026-05-18T00:00:00Z')`,
		`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('message-1', 'narrator-1', 'user', '[{"type":"text","text":"Alpha message"}]', 'Alpha message body', '2026-05-18T00:00:00Z')`,
		`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES ('ref-1', 'narrator-1', 'message-1', 1, 0)`,
		`INSERT INTO terminals (id, chapter_id, narrator_id, name, cwd, status, graph_opened, created_at) VALUES ('terminal-1', 'chapter-1', 'narrator-1', 'Shell', '/tmp/narrafork-alpha', 'running', 0, '2026-05-18T00:00:00Z')`,
		`INSERT INTO terminal_tabs (id, chapter_id, narrator_id, name, sort_order, created_at) VALUES ('tab-1', 'chapter-1', 'narrator-1', 'Shell', 0, '2026-05-18T00:00:00Z')`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("insert smoke fixture: %v\n%s", err, stmt)
		}
	}
}
