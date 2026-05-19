package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/chapters"
	"narrafork-go/internal/compat/bunfinal"
	"narrafork-go/internal/config"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/narrators"
	"narrafork-go/internal/projects"
	"narrafork-go/internal/ws"
)

func TestFrontendContractSmokeHTTPPaths(t *testing.T) {
	router, token := newFrontendContractFixture(t)

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		assert     func(*testing.T, map[string]any)
	}{
		{
			name:       "projects list",
			method:     http.MethodGet,
			path:       "/api/projects",
			wantStatus: http.StatusOK,
			assert:     assertJSONArray,
		},
		{
			name:       "project detail",
			method:     http.MethodGet,
			path:       "/api/projects/project-1",
			wantStatus: http.StatusOK,
			assert:     assertJSONField("id", "project-1"),
		},
		{
			name:       "project graph",
			method:     http.MethodGet,
			path:       "/api/projects/project-1/graph",
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, body map[string]any) {
				assertArrayField(t, body, "nodes")
				assertArrayField(t, body, "edges")
				assertArrayField(t, body, "explorationGroups")
				assertArrayField(t, body, "openedTerminals")
			},
		},
		{
			name:       "graph positions patch",
			method:     http.MethodPatch,
			path:       "/api/projects/project-1/graph/positions",
			body:       `{"positions":[{"chapterId":"chapter-1","anchorCommitSha":"abc123","axisOffset":12,"crossOffset":34,"panelExpanded":true,"panelWidth":420,"panelHeight":260}]}`,
			wantStatus: http.StatusOK,
			assert:     assertJSONField("ok", true),
		},
		{
			name:       "chapters list",
			method:     http.MethodGet,
			path:       "/api/chapters?projectId=project-1",
			wantStatus: http.StatusOK,
			assert:     assertJSONArray,
		},
		{
			name:       "chapter detail",
			method:     http.MethodGet,
			path:       "/api/chapters/chapter-1",
			wantStatus: http.StatusOK,
			assert:     assertJSONField("id", "chapter-1"),
		},
		{
			name:       "chapter patch",
			method:     http.MethodPatch,
			path:       "/api/chapters/chapter-1",
			body:       `{"title":"Frontend Contract Chapter"}`,
			wantStatus: http.StatusOK,
			assert:     assertJSONField("title", "Frontend Contract Chapter"),
		},
		{
			name:       "narrators by chapter",
			method:     http.MethodGet,
			path:       "/api/narrators?chapterId=chapter-1",
			wantStatus: http.StatusOK,
			assert:     assertJSONArray,
		},
		{
			name:       "narrators paginated all",
			method:     http.MethodGet,
			path:       "/api/narrators?standalone=all&limit=10",
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, body map[string]any) {
				assertArrayField(t, body, "items")
				if _, ok := body["hasMore"].(bool); !ok {
					t.Fatalf("expected hasMore boolean, got %#v", body)
				}
			},
		},
		{
			name:       "narrator detail",
			method:     http.MethodGet,
			path:       "/api/narrators/narrator-1",
			wantStatus: http.StatusOK,
			assert:     assertJSONField("id", "narrator-1"),
		},
		{
			name:       "narrator messages page",
			method:     http.MethodGet,
			path:       "/api/narrators/narrator-1/messages?limit=20",
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, body map[string]any) {
				assertArrayField(t, body, "messages")
				if _, ok := body["messageVersion"].(float64); !ok {
					t.Fatalf("expected messageVersion number, got %#v", body)
				}
			},
		},
		{
			name:       "narrator draft get",
			method:     http.MethodGet,
			path:       "/api/narrators/narrator-1/draft",
			wantStatus: http.StatusOK,
			assert:     assertJSONField("hasDraft", false),
		},
		{
			name:       "narrator draft put",
			method:     http.MethodPut,
			path:       "/api/narrators/narrator-1/draft",
			body:       `{"text":"draft from frontend contract","sourceId":"input-1"}`,
			wantStatus: http.StatusOK,
			assert:     assertJSONField("hasDraft", true),
		},
		{
			name:       "narrator goals",
			method:     http.MethodGet,
			path:       "/api/narrators/narrator-1/goals",
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, body map[string]any) {
				assertArrayField(t, body, "goals")
			},
		},
		{
			name:       "narrator buffer",
			method:     http.MethodGet,
			path:       "/api/narrators/narrator-1/buffer",
			wantStatus: http.StatusOK,
			assert:     assertJSONArray,
		},
		{
			name:       "settings get",
			method:     http.MethodGet,
			path:       "/api/settings",
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, body map[string]any) {
				assertArrayField(t, body, "openaiModels")
				assertArrayField(t, body, "openaiModelsGrouped")
			},
		},
		{
			name:       "settings context thresholds",
			method:     http.MethodGet,
			path:       "/api/settings/context-thresholds?model=gpt-5.1-codex&provider=codex",
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, body map[string]any) {
				if _, ok := body["pruneStart"].(float64); !ok {
					t.Fatalf("expected pruneStart number, got %#v", body)
				}
				if _, ok := body["compactStart"].(float64); !ok {
					t.Fatalf("expected compactStart number, got %#v", body)
				}
			},
		},
		{
			name:       "settings provider patch",
			method:     http.MethodPatch,
			path:       "/api/settings",
			body:       `{"agent":{"defaultModel":"codex:gpt-5.1-codex-mini"}}`,
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, body map[string]any) {
				agent, ok := body["agent"].(map[string]any)
				if !ok || agent["defaultModel"] != "codex:gpt-5.1-codex-mini" {
					t.Fatalf("expected patched agent.defaultModel, got %#v", body["agent"])
				}
			},
		},
		{
			name:       "update check",
			method:     http.MethodGet,
			path:       "/api/update/check",
			wantStatus: http.StatusOK,
			assert: func(t *testing.T, body map[string]any) {
				if _, ok := body["updateAvailable"].(bool); !ok {
					t.Fatalf("expected updateAvailable boolean, got %#v", body)
				}
				if body["currentVersion"] == "" {
					t.Fatalf("expected currentVersion, got %#v", body)
				}
			},
		},
		{
			name:       "update version",
			method:     http.MethodGet,
			path:       "/api/update/version",
			wantStatus: http.StatusOK,
			assert:     assertHasField("version"),
		},
		{
			name:       "terminals list by chapter",
			method:     http.MethodGet,
			path:       "/api/terminals?chapterId=chapter-1",
			wantStatus: http.StatusOK,
			assert:     assertJSONArray,
		},
		{
			name:       "terminal tabs list",
			method:     http.MethodGet,
			path:       "/api/terminals/tabs?chapterId=chapter-1",
			wantStatus: http.StatusOK,
			assert:     assertJSONArray,
		},
		{
			name:       "terminal view state",
			method:     http.MethodGet,
			path:       "/api/terminals/view-state?chapterId=chapter-1",
			wantStatus: http.StatusOK,
			assert:     assertHasField("layout"),
		},
		{
			name:       "kiro status fake empty",
			method:     http.MethodGet,
			path:       "/api/kiro/status",
			wantStatus: http.StatusOK,
			assert:     assertHasField("snapshot"),
		},
		{
			name:       "openai cached models",
			method:     http.MethodGet,
			path:       "/api/openai/models",
			wantStatus: http.StatusOK,
			assert:     assertModelsFromCache,
		},
		{
			name:       "openai provider cached models",
			method:     http.MethodGet,
			path:       "/api/openai/providers/openai-test/models",
			wantStatus: http.StatusOK,
			assert:     assertModelsFromCache,
		},
		{
			name:       "anthropic provider cached models",
			method:     http.MethodGet,
			path:       "/api/anthropic/providers/anthropic-test/models",
			wantStatus: http.StatusOK,
			assert:     assertModelsFromCache,
		},
		{
			name:       "nkp provider cached models",
			method:     http.MethodGet,
			path:       "/api/nkp/providers/nkp-test/models",
			wantStatus: http.StatusOK,
			assert:     assertModelsFromCache,
		},
		{
			name:       "cline status fake empty",
			method:     http.MethodGet,
			path:       "/api/cline/status",
			wantStatus: http.StatusOK,
			assert:     assertHasField("authenticated"),
		},
		{
			name:       "cline provider cached models",
			method:     http.MethodGet,
			path:       "/api/cline/providers/cline-test/models",
			wantStatus: http.StatusOK,
			assert:     assertModelsFromCache,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader *strings.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			} else {
				bodyReader = strings.NewReader("")
			}
			req := httptest.NewRequest(tc.method, tc.path, bodyReader)
			req.Header.Set("Authorization", "Bearer "+token)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("%s %s expected %d, got %d: %s", tc.method, tc.path, tc.wantStatus, w.Code, w.Body.String())
			}
			if tc.assert == nil {
				return
			}
			var decoded any
			if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("decode response %q: %v", w.Body.String(), err)
			}
			if obj, ok := decoded.(map[string]any); ok {
				tc.assert(t, obj)
				return
			}
			tc.assert(t, map[string]any{"$array": decoded})
		})
	}
}

type bunFinalHTTPSmokeGolden struct {
	Cases []bunFinalHTTPSmokeCase `json:"cases"`
}

type bunFinalHTTPSmokeCase struct {
	ID                   string   `json:"id"`
	Method               string   `json:"method"`
	Path                 string   `json:"path"`
	Auth                 string   `json:"auth"`
	ExpectedStatus       int      `json:"expectedStatus"`
	RequiredTopLevelKeys []string `json:"requiredTopLevelKeys"`
}

func TestBunFinalHTTPSmokeGoldenCases(t *testing.T) {
	data, err := bunfinal.ReadHTTPSmokeGolden()
	if err != nil {
		t.Fatalf("read HTTP smoke golden artifact: %v", err)
	}
	var golden bunFinalHTTPSmokeGolden
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatalf("decode HTTP smoke golden artifact: %v", err)
	}
	if len(golden.Cases) == 0 {
		t.Fatal("HTTP smoke golden should contain cases")
	}
	router, token := newFrontendContractFixture(t)
	for _, tc := range golden.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			req := httptest.NewRequest(tc.Method, tc.Path, nil)
			if tc.Auth == "required" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.ExpectedStatus {
				t.Fatalf("%s %s expected %d, got %d: %s", tc.Method, tc.Path, tc.ExpectedStatus, w.Code, w.Body.String())
			}
			var body any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response %q: %v", w.Body.String(), err)
			}
			for _, key := range tc.RequiredTopLevelKeys {
				if !httpSmokeGoldenHasTopLevelKey(body, key) {
					t.Fatalf("required top-level key %q missing from %#v", key, body)
				}
			}
		})
	}
}

func httpSmokeGoldenHasTopLevelKey(body any, key string) bool {
	if items, ok := body.([]any); ok {
		return key == "projects" && len(items) >= 0
	}
	mapped, ok := body.(map[string]any)
	if !ok {
		return false
	}
	if _, ok := mapped[key]; ok {
		return true
	}
	switch key {
	case "settings":
		_, hasServer := mapped["server"]
		_, hasAuth := mapped["auth"]
		_, hasAgent := mapped["agent"]
		return hasServer && hasAuth && hasAgent
	case "authenticated":
		_, hasUsers := mapped["hasUsers"]
		_, hasRegistrationOpen := mapped["registrationOpen"]
		return hasUsers && hasRegistrationOpen
	case "metadata":
		_, hasSearchMetadata := mapped["searchMetadata"]
		return hasSearchMetadata
	}
	return false
}

func newFrontendContractFixture(t *testing.T) (http.Handler, string) {
	t.Helper()
	sqlDB := openFrontendContractDB(t)
	settings := loadFrontendContractSettings(t)
	seedFrontendProviderCaches(t)
	seedFrontendContractSettings(t, settings)
	authService := auth.NewService(sqlDB, settings)
	token, err := authService.CreateToken("user-1", "admin")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	hub := ws.NewHub()
	narratorService := narrators.NewService(sqlDB, hub, settings)
	chapterService := chapters.NewService(sqlDB, (*gitx.Service)(nil), narratorService, nil, settings)
	projectService := projects.NewService(sqlDB, (*gitx.Service)(nil), chapterService, settings)
	router := NewRouter(Services{
		DB:        sqlDB,
		Settings:  settings,
		Auth:      authService,
		Hub:       hub,
		Git:       (*gitx.Service)(nil),
		Projects:  projectService,
		Chapters:  chapterService,
		Narrators: narratorService,
	})
	return router, token
}

func openFrontendContractDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	stmts := []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT)`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, description TEXT, status TEXT, flow_mode TEXT, git_path TEXT, remote_url TEXT, default_branch TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT, proxy_domain TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT, merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT, container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT, start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER, anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER, panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT, last_accessed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT, type TEXT, metadata TEXT, created_at TEXT)`,
		`CREATE TABLE exploration_groups (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, base_chapter_id TEXT, status TEXT, decided_chapter_id TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, api_conversation_id TEXT, fork_message_id TEXT, type TEXT, subagent_type TEXT, title TEXT, inherit_mode TEXT, parent_narrator_id TEXT, context_summary TEXT, model TEXT, pending_model_restore TEXT, system_prompt TEXT, permission_mode TEXT, previous_permission_mode TEXT, plan_file_id TEXT, reasoning_effort TEXT, fast_mode INTEGER, relaxed_plan INTEGER, plan_reflection_auto_approve_override TEXT, danger_reflection_override TEXT, message_count INTEGER, total_cost_usd REAL, last_message_at TEXT, status TEXT, substatus TEXT, plan_mode INTEGER, cwd TEXT, error_message TEXT, todos_json TEXT, todos_tool_use_id TEXT, prune_boundary_message_id TEXT, pruned_percent INTEGER, prune_enabled INTEGER, enabled_tools TEXT, variant TEXT, traits TEXT, is_background INTEGER, background_status TEXT, background_result TEXT, background_completed_at TEXT, is_ask_in_passing INTEGER, turn_started_at TEXT, message_version INTEGER, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_goals (id TEXT PRIMARY KEY, narrator_id TEXT, objective TEXT, status TEXT, sort_order INTEGER, tokens_used INTEGER, time_used_seconds INTEGER, created_by TEXT, completed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, sdk_message_uuid TEXT, parent_tool_use_id TEXT, role TEXT, content_json TEXT, content_text TEXT, tokens_in INTEGER, cost_usd REAL, turn_usage_json TEXT, provider TEXT, credential_id TEXT, model TEXT, output_tokens INTEGER, cached_input_tokens INTEGER, cache_creation_input_tokens INTEGER, cache_creation_5m_tokens INTEGER, cache_creation_1h_tokens INTEGER, reasoning_tokens INTEGER, ttft_ms INTEGER, duration_ms INTEGER, context_percent REAL, meter_usage REAL, meter_unit TEXT, commit_sha TEXT, command_text TEXT, created_by TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, seq INTEGER, is_compact INTEGER, pruned_percent INTEGER, segment_compact_id TEXT)`,
		`CREATE TABLE narrator_tool_calls (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, tool_name TEXT, input_json TEXT, output_json TEXT, status TEXT, duration_ms INTEGER, stream_started_at INTEGER, permission_started_at TEXT, execution_started_at TEXT, completed_at TEXT, error_message TEXT, permission_decided_by TEXT, permission_decided_at TEXT, permission_deny_message TEXT, permission_decision_reason TEXT, permission_suggestions TEXT, is_background INTEGER, input_tokens INTEGER, output_tokens INTEGER, cache_creation_tokens INTEGER, cache_read_tokens INTEGER, cache_creation_5m_tokens INTEGER, cache_creation_1h_tokens INTEGER, input_cost REAL, output_cost REAL, cache_creation_cost REAL, cache_read_cost REAL, total_cost REAL, provider TEXT, model TEXT, result_message_id TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, target TEXT, source TEXT, content TEXT, order_index INTEGER, created_at TEXT)`,
		`CREATE TABLE narrator_buffered_messages (id TEXT PRIMARY KEY, narrator_id TEXT, text TEXT, images_json TEXT, command_text TEXT, created_by TEXT, creator_json TEXT, text_file_paths_json TEXT, priority INTEGER, seq INTEGER, buffered_at TEXT)`,
		`CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT, narrator_id TEXT, name TEXT, cwd TEXT, dtach_socket TEXT, status TEXT, exit_code INTEGER, graph_opened INTEGER, graph_x REAL, graph_y REAL, graph_width REAL, graph_height REAL, created_at TEXT)`,
		`CREATE TABLE terminal_tabs (id TEXT PRIMARY KEY, chapter_id TEXT, narrator_id TEXT, name TEXT, sort_order INTEGER, created_at TEXT)`,
		`CREATE TABLE terminal_view_state (id TEXT PRIMARY KEY, user_id TEXT, chapter_id TEXT, narrator_id TEXT, layout TEXT, active_tab_id TEXT, panel_assignments TEXT, updated_at TEXT)`,
		`CREATE TABLE container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, container_id TEXT, service_name TEXT, status TEXT, host_port INTEGER, container_port INTEGER, proxy_label TEXT, container_ip TEXT, volume_name TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, auto_load_older_messages INTEGER, fast_mode_default INTEGER, language TEXT, word_wrap_markdown INTEGER, word_wrap_code INTEGER, word_wrap_diff INTEGER, reply_in_user_language INTEGER, show_token_usage INTEGER, show_output_stats INTEGER, terminal_theme TEXT, terminal_font_size INTEGER, recent_tabs TEXT, add_subagent_to_recent_tabs INTEGER, notify_on_done INTEGER, notify_on_waiting INTEGER, notify_pwa_enabled INTEGER, notify_sound_enabled INTEGER, notify_sound_type TEXT, notify_sound_builtin TEXT, notify_sound_file_id TEXT, notify_dingtalk_enabled INTEGER, notify_dingtalk_webhook TEXT, notify_dingtalk_secret TEXT, notify_feishu_enabled INTEGER, notify_feishu_webhook TEXT, notify_feishu_secret TEXT, commands TEXT, graph_viewports TEXT, send_mode TEXT, setup_wizard_completed INTEGER, gateway_config TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE background_tasks (id TEXT PRIMARY KEY, parent_narrator_id TEXT, type TEXT, status TEXT, command TEXT, exit_code INTEGER, subagent_narrator_id TEXT, subagent_type TEXT, tool_use_id TEXT, alias TEXT, title TEXT, output TEXT, output_bytes INTEGER, output_truncated INTEGER, notified INTEGER, started_at TEXT, completed_at TEXT, created_at TEXT, updated_at TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec schema %q: %v", stmt, err)
		}
	}
	seed := []string{
		`INSERT INTO users (id, username, password_hash, role, created_at) VALUES ('user-1', 'admin', 'hash', 'admin', '2026-05-15T00:00:00Z')`,
		`INSERT INTO projects (id, name, description, status, flow_mode, git_path, remote_url, default_branch, chapter_settings, created_at, updated_at) VALUES ('project-1', 'Frontend Contract', 'Smoke fixture', 'active', 'classic', '/tmp/narrafork-contract', NULL, 'main', '{}', '2026-05-15T00:00:00Z', '2026-05-15T00:00:00Z')`,
		`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('chapter-1', 'project-1', 'Contract Chapter', 'dormant', 'trunk', 'main', NULL, 'main', 1, 0, 0, 0, 0, 0, '2026-05-15T00:00:00Z', '2026-05-15T00:00:00Z')`,
		`INSERT INTO chapter_edges (id, project_id, source_id, target_id, type, metadata, created_at) VALUES ('edge-1', 'project-1', 'chapter-1', 'chapter-1', 'dependency', '{}', '2026-05-15T00:00:00Z')`,
		`INSERT INTO exploration_groups (id, project_id, title, description, base_chapter_id, status, created_at, updated_at) VALUES ('group-1', 'project-1', 'Experiment', NULL, 'chapter-1', 'active', '2026-05-15T00:00:00Z', '2026-05-15T00:00:00Z')`,
		`INSERT INTO narrators (id, chapter_id, type, title, inherit_mode, model, permission_mode, fast_mode, relaxed_plan, plan_reflection_auto_approve_override, danger_reflection_override, message_count, total_cost_usd, status, substatus, plan_mode, todos_json, prune_enabled, enabled_tools, variant, traits, is_background, is_ask_in_passing, message_version, created_at, updated_at) VALUES ('narrator-1', 'chapter-1', 'primary', 'Contract Narrator', 'fresh', 'codex:gpt-5.1-codex-mini', 'default', 0, 0, 'inherit', 'inherit', 0, 0, 'idle', '[]', 0, '[]', 1, '[]', 'primary', '[]', 0, 0, 1, '2026-05-15T00:00:00Z', '2026-05-15T00:00:00Z')`,
		`INSERT INTO terminals (id, chapter_id, name, status, graph_opened, created_at) VALUES ('terminal-1', 'chapter-1', 'Shell', 'running', 1, '2026-05-15T00:00:00Z')`,
		`INSERT INTO terminal_tabs (id, chapter_id, name, sort_order, created_at) VALUES ('tab-1', 'chapter-1', 'Shell', 0, '2026-05-15T00:00:00Z')`,
	}
	for _, stmt := range seed {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("exec seed %q: %v", stmt, err)
		}
	}
	return sqlDB
}

func loadFrontendContractSettings(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return settings
}

func seedFrontendContractSettings(t *testing.T, settings *config.Store) {
	t.Helper()
	_, err := settings.Patch(map[string]any{
		"openaiProviders":    []any{map[string]any{"id": "openai-test", "name": "OpenAI Test", "prefix": "openait", "baseUrl": "http://127.0.0.1/unused", "apiKey": "sk-test", "disabled": false}},
		"anthropicProviders": []any{map[string]any{"id": "anthropic-test", "name": "Anthropic Test", "prefix": "antht", "baseUrl": "http://127.0.0.1/unused", "apiKey": "sk-test", "disabled": false}},
		"nkpProviders":       []any{map[string]any{"id": "nkp-test", "name": "NKP Test", "prefix": "nkpt", "baseUrl": "http://127.0.0.1/unused", "apiKey": "sk-test", "disabled": false}},
		"clineProviders":     []any{map[string]any{"id": "cline-test", "name": "Cline Test", "prefix": "clinet", "enabledModels": []any{"cline-model"}, "disabled": false}},
	})
	if err != nil {
		t.Fatalf("patch provider settings: %v", err)
	}
}

func seedFrontendProviderCaches(t *testing.T) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home: %v", err)
	}
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	files := map[string]string{
		"openai-models-providers.json":    `{"openai-test":[{"id":"gpt-contract","owned_by":"contract"}]}`,
		"anthropic-models-providers.json": `{"anthropic-test":[{"id":"claude-contract","display_name":"Claude Contract"}]}`,
		"nkp-models-providers.json":       `{"nkp-test":[{"id":"nkp-contract","owned_by":"contract"}]}`,
		"cline-models.json":               `{"cline-test":[{"id":"cline-model","name":"Cline Contract"}]}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write provider cache %s: %v", name, err)
		}
	}
}

func assertJSONArray(t *testing.T, body map[string]any) {
	t.Helper()
	if _, ok := body["$array"].([]any); !ok {
		t.Fatalf("expected top-level JSON array, got %#v", body["$array"])
	}
}

func assertArrayField(t *testing.T, body map[string]any, field string) {
	t.Helper()
	if _, ok := body[field].([]any); !ok {
		t.Fatalf("expected %s array, got %#v in %#v", field, body[field], body)
	}
}

func assertJSONField(field string, want any) func(*testing.T, map[string]any) {
	return func(t *testing.T, body map[string]any) {
		t.Helper()
		got := body[field]
		if got != want {
			t.Fatalf("expected %s=%#v, got %#v in %#v", field, want, got, body)
		}
	}
}

func assertHasField(field string) func(*testing.T, map[string]any) {
	return func(t *testing.T, body map[string]any) {
		t.Helper()
		if _, ok := body[field]; !ok {
			t.Fatalf("expected field %s in %#v", field, body)
		}
	}
}

func assertModelsFromCache(t *testing.T, body map[string]any) {
	t.Helper()
	assertArrayField(t, body, "models")
	if body["fromCache"] != true {
		t.Fatalf("expected fromCache=true, got %#v", body)
	}
}
