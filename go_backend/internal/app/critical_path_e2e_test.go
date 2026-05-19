package app

import (
	"bytes"
	"context"
	"crypto/sha512"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite"

	"narrafork-go/internal/auth"
	"narrafork-go/internal/chapters"
	"narrafork-go/internal/config"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/narrators"
	"narrafork-go/internal/projects"
	"narrafork-go/internal/ws"
)

func TestG05CriticalPathE2E(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	providerRequests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerRequests++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{
				"role":    "assistant",
				"content": "critical path agent response",
			}}},
		})
	}))
	defer provider.Close()

	sqlDB := openCriticalPathDB(t)
	defer sqlDB.Close()
	settings := loadCriticalPathSettings(t, provider.URL)
	gitSvc := &gitx.Service{Timeout: 10 * time.Second}
	hub := ws.NewHub()
	fakeTerminal := newCriticalPathTerminalRuntime()
	hub.SetTerminalDB(sqlDB)
	hub.SetTerminalRuntime(fakeTerminal)
	hub.SetMergeDB(sqlDB)
	authSvc := auth.NewService(sqlDB, settings)
	narratorSvc := narrators.NewService(sqlDB, hub, settings)
	chapterSvc := chapters.NewService(sqlDB, gitSvc, narratorSvc, nil, settings)
	projectSvc := projects.NewService(sqlDB, gitSvc, chapterSvc, settings)
	chapterSvc.Narrators = narratorSvc
	projectSvc.Chapters = chapterSvc

	router := NewRouter(Services{
		DB:        sqlDB,
		Settings:  settings,
		Git:       gitSvc,
		Auth:      authSvc,
		Hub:       hub,
		Narrators: narratorSvc,
		Chapters:  chapterSvc,
		Projects:  projectSvc,
	})
	server := httptest.NewServer(router)
	defer server.Close()
	token := insertCriticalPathAdmin(t, sqlDB, authSvc)
	client := &http.Client{Timeout: 5 * time.Second}

	repoPath := initCriticalPathRepo(t)
	project := criticalPathDo[map[string]any](t, client, server.URL, token, http.MethodPost, "/api/projects/", map[string]any{
		"name":     "Critical Path",
		"repoMode": "existing",
		"gitPath":  repoPath,
		"flowMode": "classic",
	}, http.StatusCreated)
	projectID := stringField(t, project, "id")

	chaptersList := criticalPathDo[[]map[string]any](t, client, server.URL, token, http.MethodGet, "/api/chapters/?projectId="+projectID, nil, http.StatusOK)
	root := findCriticalPathChapter(t, chaptersList, "trunk")
	rootID := stringField(t, root, "id")

	narratorsList := criticalPathDo[[]map[string]any](t, client, server.URL, token, http.MethodGet, "/api/narrators/?chapterId="+rootID, nil, http.StatusOK)
	if len(narratorsList) == 0 {
		t.Fatalf("project creation did not create a root narrator")
	}
	rootNarratorID := stringField(t, narratorsList[0], "id")

	_ = criticalPathDo[map[string]any](t, client, server.URL, token, http.MethodPost, "/api/narrators/"+rootNarratorID+"/messages", map[string]any{"message": "run the agent"}, http.StatusCreated)
	waitForCriticalPathAssistant(t, sqlDB, rootNarratorID, "critical path agent response")
	if providerRequests == 0 {
		t.Fatalf("POST /messages did not call the httptest agent provider")
	}

	fork := criticalPathDo[map[string]any](t, client, server.URL, token, http.MethodPost, "/api/chapters/"+rootID+"/fork", map[string]any{
		"title":       "Feature branch",
		"inheritMode": "fresh",
	}, http.StatusCreated)
	forkID := stringField(t, fork, "id")
	forkWorktree := stringField(t, fork, "worktreePath")
	writeAndCommitCriticalPathChange(t, forkWorktree)

	review := criticalPathDo[map[string]any](t, client, server.URL, token, http.MethodPost, "/api/chapters/"+forkID+"/review", map[string]any{
		"title":  "Review feature branch",
		"locale": "en",
	}, http.StatusCreated)
	if role := stringField(t, review, "role"); role != "review" {
		t.Fatalf("review chapter role = %q, want review", role)
	}

	mergeCheck := criticalPathDo[map[string]any](t, client, server.URL, token, http.MethodGet, "/api/chapters/"+forkID+"/merge-check?targetChapterId="+rootID, nil, http.StatusOK)
	if mergeCheck["canMerge"] != true {
		t.Fatalf("merge-check canMerge = %#v, body=%#v", mergeCheck["canMerge"], mergeCheck)
	}
	merge := criticalPathDo[map[string]any](t, client, server.URL, token, http.MethodPost, "/api/chapters/"+forkID+"/merge", map[string]any{
		"targetChapterId": rootID,
		"strategy":        "merge",
		"message":         "Merge feature branch",
	}, http.StatusOK)
	if merge["success"] != true {
		t.Fatalf("merge success = %#v, body=%#v", merge["success"], merge)
	}

	criticalPathAssertTerminalWS(t, server.URL, token, rootID, fakeTerminal)
	criticalPathAssertFakePodman(t, client, server.URL, token)
	criticalPathAssertUpdateSSE(t, client, server.URL, token, settings)
}

func openCriticalPathDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, stmt := range criticalPathSchemaStatements() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			sqlDB.Close()
			t.Fatalf("create test schema: %v\n%s", err, stmt)
		}
	}
	return sqlDB
}

func criticalPathSchemaStatements() []string {
	return []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, password_hash TEXT, role TEXT, avatar_color TEXT, avatar_image_id TEXT, git_username TEXT, git_email TEXT, created_at TEXT)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, auto_load_older_messages INTEGER DEFAULT 0, fast_mode_default INTEGER DEFAULT 0, language TEXT, word_wrap_markdown INTEGER DEFAULT 0, word_wrap_code INTEGER DEFAULT 0, word_wrap_diff INTEGER DEFAULT 0, reply_in_user_language INTEGER DEFAULT 0, show_token_usage INTEGER DEFAULT 0, show_output_stats INTEGER DEFAULT 0, terminal_theme TEXT, terminal_font_size INTEGER, recent_tabs TEXT, add_subagent_to_recent_tabs INTEGER DEFAULT 0, notify_on_done INTEGER DEFAULT 0, notify_on_waiting INTEGER DEFAULT 0, notify_pwa_enabled INTEGER DEFAULT 0, notify_sound_enabled INTEGER DEFAULT 0, notify_sound_type TEXT, notify_sound_builtin TEXT, notify_sound_file_id TEXT, notify_dingtalk_enabled INTEGER DEFAULT 0, notify_dingtalk_webhook TEXT, notify_dingtalk_secret TEXT, notify_feishu_enabled INTEGER DEFAULT 0, notify_feishu_webhook TEXT, notify_feishu_secret TEXT, commands TEXT, graph_viewports TEXT, send_mode TEXT, setup_wizard_completed INTEGER DEFAULT 1, gateway_config TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, description TEXT, status TEXT DEFAULT 'active', flow_mode TEXT DEFAULT 'classic', git_path TEXT, remote_url TEXT, default_branch TEXT DEFAULT 'main', startup_script TEXT, copy_files TEXT, chapter_settings TEXT, proxy_domain TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT DEFAULT 'active', role TEXT DEFAULT 'branch', branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT, merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT, container_config TEXT, exploration_group_id TEXT, is_root INTEGER DEFAULT 0, head_commit_sha TEXT, start_commit_sha TEXT, commit_count INTEGER DEFAULT 0, color TEXT, group_label TEXT, pinned INTEGER DEFAULT 0, anchor_commit_sha TEXT, axis_offset REAL DEFAULT 0, cross_offset REAL DEFAULT 0, panel_expanded INTEGER DEFAULT 0, panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT, last_accessed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapter_edges (id TEXT PRIMARY KEY, project_id TEXT, source_id TEXT, target_id TEXT, type TEXT, metadata TEXT, created_at TEXT)`,
		`CREATE TABLE chapter_commits (id TEXT PRIMARY KEY, chapter_id TEXT, sha TEXT, message TEXT, full_message TEXT, author_name TEXT, author_email TEXT, authored_at TEXT, source TEXT, narrator_id TEXT, narrator_message_id TEXT, files_changed INTEGER, lines_added INTEGER, lines_removed INTEGER, created_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, api_conversation_id TEXT, fork_message_id TEXT, type TEXT DEFAULT 'primary', subagent_type TEXT, title TEXT, inherit_mode TEXT DEFAULT 'fresh', parent_narrator_id TEXT, context_summary TEXT, model TEXT, pending_model_restore TEXT, system_prompt TEXT, permission_mode TEXT, previous_permission_mode TEXT, plan_file_id TEXT, reasoning_effort TEXT, fast_mode INTEGER DEFAULT 0, relaxed_plan INTEGER DEFAULT 0, plan_reflection_auto_approve_override TEXT DEFAULT 'inherit', danger_reflection_override TEXT DEFAULT 'inherit', message_count INTEGER DEFAULT 0, total_cost_usd REAL DEFAULT 0, last_message_at TEXT, status TEXT DEFAULT 'idle', substatus TEXT DEFAULT '[]', plan_mode INTEGER DEFAULT 0, cwd TEXT, error_message TEXT, todos_json TEXT, todos_tool_use_id TEXT, prune_boundary_message_id TEXT, pruned_percent INTEGER, prune_enabled INTEGER DEFAULT 1, enabled_tools TEXT, variant TEXT DEFAULT 'primary', traits TEXT DEFAULT '[]', is_background INTEGER DEFAULT 0, background_status TEXT, background_result TEXT, background_completed_at TEXT, is_ask_in_passing INTEGER DEFAULT 0, turn_started_at TEXT, message_version INTEGER DEFAULT 0, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, sdk_message_uuid TEXT, parent_tool_use_id TEXT, role TEXT, content_json TEXT, content_text TEXT, tokens_in INTEGER, cost_usd REAL, turn_usage_json TEXT, provider TEXT, credential_id TEXT, model TEXT, output_tokens INTEGER, cached_input_tokens INTEGER, cache_creation_input_tokens INTEGER, cache_creation_5m_tokens INTEGER, cache_creation_1h_tokens INTEGER, reasoning_tokens INTEGER, ttft_ms INTEGER, duration_ms INTEGER, context_percent REAL, meter_usage REAL, meter_unit TEXT, commit_sha TEXT, command_text TEXT, created_by TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, seq INTEGER, is_compact INTEGER DEFAULT 0, pruned_percent INTEGER, segment_compact_id TEXT)`,
		`CREATE TABLE narrator_tool_calls (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, tool_name TEXT, input_json TEXT, output_json TEXT, status TEXT, duration_ms INTEGER, stream_started_at TEXT, permission_started_at TEXT, execution_started_at TEXT, completed_at TEXT, error_message TEXT, permission_decided_by TEXT, permission_decided_at TEXT, permission_deny_message TEXT, permission_decision_reason TEXT, permission_suggestions TEXT, is_background INTEGER DEFAULT 0, input_tokens INTEGER DEFAULT 0, output_tokens INTEGER DEFAULT 0, cache_creation_tokens INTEGER DEFAULT 0, cache_read_tokens INTEGER DEFAULT 0, cache_creation_5m_tokens INTEGER DEFAULT 0, cache_creation_1h_tokens INTEGER DEFAULT 0, input_cost REAL DEFAULT 0, output_cost REAL DEFAULT 0, cache_creation_cost REAL DEFAULT 0, cache_read_cost REAL DEFAULT 0, total_cost REAL DEFAULT 0, provider TEXT, model TEXT, result_message_id TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_goals (id TEXT PRIMARY KEY, narrator_id TEXT, objective TEXT, status TEXT, sort_order INTEGER DEFAULT 0, tokens_used INTEGER DEFAULT 0, time_used_seconds INTEGER DEFAULT 0, created_by TEXT, completed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_sidecars (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, target TEXT, source TEXT, content TEXT, order_index INTEGER DEFAULT 0, created_at TEXT)`,
		`CREATE TABLE narrator_file_snapshots (id TEXT PRIMARY KEY, narrator_id TEXT, file_path TEXT, original_content TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_patches (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, tool_use_id TEXT, before_hash TEXT, after_hash TEXT, files_json TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_buffered_messages (id TEXT PRIMARY KEY, narrator_id TEXT, text TEXT, images_json TEXT, command_text TEXT, created_by TEXT, creator_json TEXT, text_file_paths_json TEXT, priority INTEGER DEFAULT 0, seq INTEGER DEFAULT 0, buffered_at TEXT)`,
		`CREATE TABLE background_tasks (id TEXT PRIMARY KEY, parent_narrator_id TEXT, type TEXT, status TEXT, command TEXT, exit_code INTEGER, subagent_narrator_id TEXT, subagent_type TEXT, tool_use_id TEXT, alias TEXT, title TEXT, output TEXT, output_bytes INTEGER DEFAULT 0, output_truncated INTEGER DEFAULT 0, notified INTEGER DEFAULT 0, started_at TEXT, completed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT, narrator_id TEXT, name TEXT, cwd TEXT, dtach_socket TEXT, status TEXT DEFAULT 'running', exit_code INTEGER, graph_opened INTEGER DEFAULT 0, graph_x REAL, graph_y REAL, graph_width REAL, graph_height REAL, created_at TEXT)`,
		`CREATE TABLE terminal_tabs (id TEXT PRIMARY KEY, chapter_id TEXT, narrator_id TEXT, name TEXT, sort_order INTEGER DEFAULT 0, created_at TEXT)`,
		`CREATE TABLE terminal_view_state (id TEXT PRIMARY KEY, user_id TEXT, chapter_id TEXT, narrator_id TEXT, layout TEXT DEFAULT 'single', active_tab_id TEXT, panel_assignments TEXT, updated_at TEXT)`,
		`CREATE TABLE container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, container_id TEXT, service_name TEXT, status TEXT DEFAULT 'created', host_port INTEGER, container_port INTEGER, proxy_label TEXT, container_ip TEXT, volume_name TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE port_allocations (port INTEGER PRIMARY KEY, chapter_id TEXT, service_name TEXT, allocated_at TEXT)`,
		`CREATE TABLE review_conclusions (id TEXT PRIMARY KEY, review_chapter_id TEXT, source_chapter_id TEXT, verdict TEXT, findings_json TEXT, created_at TEXT)`,
		`CREATE TABLE exploration_groups (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, base_chapter_id TEXT, status TEXT, decided_chapter_id TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE workspaces (id TEXT PRIMARY KEY, user_id TEXT, title TEXT, tree TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE api_requests (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, kind TEXT, provider TEXT, credential_id TEXT, model TEXT, input_tokens INTEGER, output_tokens INTEGER, cached_input_tokens INTEGER, cache_creation_input_tokens INTEGER, cache_creation_5m_tokens INTEGER, cache_creation_1h_tokens INTEGER, reasoning_tokens INTEGER, ttft_ms INTEGER, duration_ms INTEGER, cost_usd REAL, context_percent REAL, meter_usage REAL, meter_unit TEXT, error_message TEXT, raw_dump_json TEXT, created_at TEXT)`,
		`CREATE TABLE hooks (id TEXT PRIMARY KEY, project_id TEXT, event TEXT, matcher TEXT, type TEXT, command TEXT, url TEXT, headers TEXT, prompt TEXT, model TEXT, timeout INTEGER, enabled INTEGER, sort_order INTEGER, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE gateway_session_mappings (id TEXT PRIMARY KEY, platform TEXT, chat_id TEXT, user_id TEXT, username TEXT, narrator_id TEXT, app_user_id TEXT, project_id TEXT, chapter_id TEXT, last_message_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE benchmark_suites (id TEXT PRIMARY KEY, name TEXT, version TEXT, description TEXT, tasks_json TEXT, created_at TEXT)`,
		`CREATE TABLE benchmark_runs (id TEXT PRIMARY KEY, suite_id TEXT, name TEXT, model TEXT, system_prompt TEXT, permission_mode TEXT, status TEXT, config TEXT, total_tasks INTEGER, completed_tasks INTEGER, passed_tasks INTEGER, failed_tasks INTEGER, total_cost_usd REAL, total_tokens_in INTEGER, total_tokens_out INTEGER, total_duration_ms INTEGER, started_at TEXT, completed_at TEXT, created_at TEXT)`,
		`CREATE TABLE benchmark_task_results (id TEXT PRIMARY KEY, run_id TEXT, task_id TEXT, task_name TEXT, narrator_id TEXT, status TEXT, score REAL, max_score REAL, output TEXT, eval_output TEXT, error_message TEXT, tokens_in INTEGER, tokens_out INTEGER, cost_usd REAL, duration_ms INTEGER, tool_call_count INTEGER, message_count INTEGER, metadata TEXT, started_at TEXT, completed_at TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_whitelist_dirs (id TEXT PRIMARY KEY, narrator_id TEXT, path TEXT, access_level TEXT, enabled INTEGER DEFAULT 1, created_at TEXT)`,
		`CREATE TABLE narrator_blacklist_dirs (id TEXT PRIMARY KEY, narrator_id TEXT, path TEXT, deny_level TEXT, enabled INTEGER DEFAULT 1, created_at TEXT)`,
		`CREATE TABLE narrator_whitelist_cmds (id TEXT PRIMARY KEY, narrator_id TEXT, pattern TEXT, enabled INTEGER DEFAULT 1, created_at TEXT)`,
		`CREATE TABLE narrator_blacklist_cmds (id TEXT PRIMARY KEY, narrator_id TEXT, pattern TEXT, deny_prompt TEXT, enabled INTEGER DEFAULT 1, created_at TEXT)`,
		`CREATE TABLE volume_snapshots (id TEXT PRIMARY KEY, project_id TEXT, name TEXT, description TEXT, source_chapter_id TEXT, service_name TEXT, container_path TEXT, size_bytes INTEGER, created_by TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE volume_snapshot_applications (id TEXT PRIMARY KEY, snapshot_id TEXT, chapter_id TEXT, applied_at TEXT, applied_by TEXT)`,
	}
}

func loadCriticalPathSettings(t *testing.T, providerURL string) *config.Store {
	t.Helper()
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	_, err = settings.Patch(map[string]any{
		"agent": map[string]any{"defaultModel": "openai:gpt-critical", "defaultPermissionMode": "acceptEdits"},
		"openaiProviders": []any{map[string]any{
			"prefix":       "openai",
			"apiKey":       "test-key",
			"baseUrl":      providerURL,
			"defaultModel": "gpt-critical",
		}},
	})
	if err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	return settings
}

func insertCriticalPathAdmin(t *testing.T, sqlDB *sql.DB, authSvc *auth.Service) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := sqlDB.Exec(`INSERT INTO users (id, username, password_hash, role, avatar_color, created_at) VALUES ('admin-1', 'admin', 'hash', 'admin', '#4C6EF5', ?)`, now)
	if err != nil {
		t.Fatalf("insert admin user: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO user_preferences (id, user_id, language, created_at, updated_at) VALUES ('pref-1', 'admin-1', 'en', ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("insert admin preferences: %v", err)
	}
	token, err := authSvc.CreateToken("admin-1", "admin")
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	return token
}

func initCriticalPathRepo(t *testing.T) string {
	t.Helper()
	repoPath := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runCriticalPathGit(t, repoPath, "init")
	if err := os.WriteFile(filepath.Join(repoPath, ".git", "info", "exclude"), []byte(".worktrees/\n"), 0o644); err != nil {
		t.Fatalf("write git exclude: %v", err)
	}
	runCriticalPathGit(t, repoPath, "config", "user.name", "Critical Path")
	runCriticalPathGit(t, repoPath, "config", "user.email", "critical@example.test")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("# Critical Path\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	runCriticalPathGit(t, repoPath, "add", "README.md")
	runCriticalPathGit(t, repoPath, "commit", "-m", "Initial commit")
	return repoPath
}

func writeAndCommitCriticalPathChange(t *testing.T, worktree string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("critical path feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	runCriticalPathGit(t, worktree, "add", "feature.txt")
	runCriticalPathGit(t, worktree, "commit", "-m", "Add critical path feature")
}

func runCriticalPathGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func criticalPathDo[T any](t *testing.T, client *http.Client, baseURL, token, method, path string, body any, want int) T {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, baseURL+path, reader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s got %d want %d: %s", method, path, resp.StatusCode, want, buf.String())
	}
	var out T
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("decode %s %s response: %v body=%s", method, path, err, buf.String())
	}
	return out
}

func findCriticalPathChapter(t *testing.T, chapters []map[string]any, role string) map[string]any {
	t.Helper()
	for _, chapter := range chapters {
		if chapter["role"] == role {
			return chapter
		}
	}
	t.Fatalf("chapter with role %q not found in %#v", role, chapters)
	return nil
}

func stringField(t *testing.T, values map[string]any, key string) string {
	t.Helper()
	value, _ := values[key].(string)
	if strings.TrimSpace(value) == "" {
		t.Fatalf("missing string field %q in %#v", key, values)
	}
	return value
}

func waitForCriticalPathAssistant(t *testing.T, sqlDB *sql.DB, narratorID, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var text string
		err := sqlDB.QueryRow(`SELECT COALESCE(content_text, '') FROM narrator_messages WHERE narrator_id = ? AND role = 'assistant' ORDER BY created_at DESC LIMIT 1`, narratorID).Scan(&text)
		if err == nil && strings.Contains(text, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("assistant message for narrator %s did not contain %q", narratorID, want)
}

type criticalPathTerminalRuntime struct {
	mu        sync.Mutex
	sizes     []criticalPathTerminalSize
	writes    []criticalPathTerminalWrite
	resizes   []criticalPathTerminalSize
	kills     []string
	snapshots map[string]map[string]any
}

type criticalPathTerminalSize struct {
	terminalID string
	cols       int
	rows       int
}

type criticalPathTerminalWrite struct {
	terminalID string
	data       string
}

func newCriticalPathTerminalRuntime() *criticalPathTerminalRuntime {
	return &criticalPathTerminalRuntime{snapshots: map[string]map[string]any{}}
}

func (f *criticalPathTerminalRuntime) Ensure(context.Context, string) error { return nil }

func (f *criticalPathTerminalRuntime) EnsureSize(_ context.Context, terminalID string, cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizes = append(f.sizes, criticalPathTerminalSize{terminalID: terminalID, cols: cols, rows: rows})
	f.snapshots[terminalID] = map[string]any{"data": "fake terminal ready\n", "cols": cols, "rows": rows}
	return nil
}

func (f *criticalPathTerminalRuntime) Write(_ context.Context, terminalID string, data string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, criticalPathTerminalWrite{terminalID: terminalID, data: data})
	return nil
}

func (f *criticalPathTerminalRuntime) Resize(_ context.Context, terminalID string, cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, criticalPathTerminalSize{terminalID: terminalID, cols: cols, rows: rows})
	return nil
}

func (f *criticalPathTerminalRuntime) Kill(_ context.Context, terminalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, terminalID)
	return nil
}

func (f *criticalPathTerminalRuntime) Snapshot(_ context.Context, terminalID string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshot := f.snapshots[terminalID]
	if snapshot == nil {
		return nil, nil
	}
	copy := map[string]any{}
	for key, value := range snapshot {
		copy[key] = value
	}
	return copy, nil
}

func criticalPathAssertTerminalWS(t *testing.T, baseURL, token, chapterID string, runtime *criticalPathTerminalRuntime) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws/terminal?token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial terminal websocket: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "create", "requestId": "term-req-1", "chapterId": chapterID, "name": "Fake shell", "cols": 100, "rows": 32}); err != nil {
		t.Fatalf("send terminal create: %v", err)
	}
	created := readCriticalPathWS(t, conn)
	if created["type"] != "created" || created["requestId"] != "term-req-1" {
		t.Fatalf("unexpected terminal created payload: %#v", created)
	}
	terminal, _ := created["terminal"].(map[string]any)
	terminalID, _ := terminal["id"].(string)
	if terminalID == "" {
		t.Fatalf("terminal id missing from payload: %#v", created)
	}
	ready := readCriticalPathWS(t, conn)
	if ready["type"] != "ready" || ready["terminalId"] != terminalID {
		t.Fatalf("unexpected terminal ready payload: %#v", ready)
	}
	for i := 0; i < 2; i++ {
		msg := readCriticalPathWS(t, conn)
		if msg["type"] != "snapshot" && msg["type"] != "scrollback" {
			t.Fatalf("unexpected terminal snapshot payload: %#v", msg)
		}
	}
	if err := conn.WriteJSON(map[string]any{"type": "input", "terminalId": terminalID, "data": "echo ok\n"}); err != nil {
		t.Fatalf("send terminal input: %v", err)
	}
	requestResize := readCriticalPathWS(t, conn)
	if requestResize["type"] != "requestResize" || requestResize["terminalId"] != terminalID {
		t.Fatalf("unexpected terminal requestResize payload: %#v", requestResize)
	}
	if err := conn.WriteJSON(map[string]any{"type": "resize", "terminalId": terminalID, "cols": 120, "rows": 40}); err != nil {
		t.Fatalf("send terminal resize: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{"type": "kill", "terminalId": terminalID}); err != nil {
		t.Fatalf("send terminal kill: %v", err)
	}
	killed := readCriticalPathWS(t, conn)
	if killed["type"] != "killed" || killed["terminalId"] != terminalID {
		t.Fatalf("unexpected terminal killed payload: %#v", killed)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.sizes) != 1 || runtime.sizes[0] != (criticalPathTerminalSize{terminalID: terminalID, cols: 100, rows: 32}) {
		t.Fatalf("fake terminal EnsureSize calls = %#v", runtime.sizes)
	}
	if len(runtime.writes) != 1 || runtime.writes[0] != (criticalPathTerminalWrite{terminalID: terminalID, data: "echo ok\n"}) {
		t.Fatalf("fake terminal Write calls = %#v", runtime.writes)
	}
	if len(runtime.resizes) != 1 || runtime.resizes[0] != (criticalPathTerminalSize{terminalID: terminalID, cols: 120, rows: 40}) {
		t.Fatalf("fake terminal Resize calls = %#v", runtime.resizes)
	}
	if len(runtime.kills) != 1 || runtime.kills[0] != terminalID {
		t.Fatalf("fake terminal Kill calls = %#v", runtime.kills)
	}
}

func readCriticalPathWS(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	return msg
}

func criticalPathAssertFakePodman(t *testing.T, client *http.Client, baseURL, token string) {
	t.Helper()
	fakeBin := t.TempDir()
	podmanPath := filepath.Join(fakeBin, "podman")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'podman version 9.9.9-fake'; exit 0; fi\nif [ \"$1\" = \"compose\" ] && [ \"$2\" = \"version\" ]; then echo 'podman compose version 9.9.9-fake'; exit 0; fi\nif [ \"$1\" = \"info\" ]; then echo 'pasta'; exit 0; fi\necho 'fake podman'; exit 0\n"
	if err := os.WriteFile(podmanPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake podman: %v", err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	status := criticalPathDo[map[string]any](t, client, baseURL, token, http.MethodGet, "/api/chapters/podman/status", nil, http.StatusOK)
	if status["installed"] != true || !strings.Contains(fmt.Sprint(status["version"]), "9.9.9-fake") {
		t.Fatalf("podman status did not use fake podman: %#v", status)
	}
}

func criticalPathAssertUpdateSSE(t *testing.T, client *http.Client, baseURL, token string, settings *config.Store) {
	t.Helper()
	artifact := []byte("fake update artifact")
	hash := sha512.Sum512(artifact)
	version := "9.9.9-test"
	filename := "narrafork-" + version + "-" + criticalPathUpdatePlatform()
	updateServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/products/narrafork/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"updateAvailable": true,
				"currentVersion":  r.URL.Query().Get("version"),
				"version":         version,
				"releaseDate":     "2026-05-18T00:00:00Z",
				"releaseNotes":    map[string]string{"en": "Trusted test release"},
				"platform":        criticalPathUpdatePlatform(),
				"file": map[string]any{
					"filename": filename,
					"size":     len(artifact),
					"sha512":   fmt.Sprintf("%x", hash[:]),
				},
				"zstdPatch": nil,
			})
		case "/api/v2/products/narrafork/releases/" + version + "/download/" + filename:
			_, _ = w.Write(artifact)
		default:
			http.NotFound(w, r)
		}
	}))
	defer updateServer.Close()
	if _, err := settings.Patch(map[string]any{"update": map[string]any{"serverUrl": updateServer.URL, "product": "narrafork", "channel": "stable"}}); err != nil {
		t.Fatalf("patch trusted update source: %v", err)
	}
	reqBody, _ := json.Marshal(map[string]any{
		"releaseInfo": map[string]any{
			"version": version,
			"files":   []map[string]any{{"url": "https://client.invalid/must-not-be-used"}},
		},
	})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/update/download", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("new update request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("update download request: %v", err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update download status = %d body=%s", resp.StatusCode, buf.String())
	}
	body := buf.String()
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") || !strings.Contains(body, "event: progress") || !strings.Contains(body, "event: complete") {
		t.Fatalf("update download did not return progress+complete SSE, content-type=%q body=%s", resp.Header.Get("Content-Type"), body)
	}
}

func criticalPathUpdatePlatform() string {
	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	switch runtime.GOOS {
	case "darwin":
		return "darwin-" + arch
	case "windows":
		return "win-" + arch
	default:
		return "linux-" + arch
	}
}
