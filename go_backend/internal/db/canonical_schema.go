package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// canonicalMainSchema is a transitional, read-only compatibility contract with
// the TypeScript/Drizzle-created main database. It intentionally contains only
// object requirements and must never be used to create or migrate the main DB
// from Go while SQLite remains TS-owned. The long-term target is pure Go DB
// management, including main schema migrations, startup backfills/repair, and
// FTS/search lifecycle ownership.
var canonicalMainSchema = map[string][]string{
	"projects":                     {"id", "name", "description", "status", "flow_mode", "git_path", "remote_url", "default_branch", "startup_script", "copy_files", "chapter_settings", "proxy_domain", "created_at", "updated_at"},
	"exploration_groups":           {"id", "project_id", "title", "description", "base_chapter_id", "status", "decided_chapter_id", "created_at", "updated_at"},
	"chapters":                     {"id", "project_id", "title", "description", "status", "role", "branch", "worktree_path", "base_branch", "parent_chapter_id", "fork_point", "merged_into_chapter_id", "merge_commit_sha", "merge_strategy", "pre_merge_target_sha", "container_config", "exploration_group_id", "is_root", "head_commit_sha", "start_commit_sha", "commit_count", "color", "group_label", "pinned", "anchor_commit_sha", "axis_offset", "cross_offset", "panel_expanded", "panel_width", "panel_height", "review_source_chapter_id", "review_status", "last_accessed_at", "created_at", "updated_at"},
	"chapter_edges":                {"id", "project_id", "source_id", "target_id", "type", "metadata", "created_at"},
	"chapter_commits":              {"id", "chapter_id", "sha", "message", "full_message", "author_name", "author_email", "authored_at", "source", "narrator_id", "narrator_message_id", "files_changed", "lines_added", "lines_removed", "created_at"},
	"narrators":                    {"id", "chapter_id", "api_conversation_id", "fork_message_id", "type", "subagent_type", "title", "inherit_mode", "parent_narrator_id", "context_summary", "model", "pending_model_restore", "system_prompt", "permission_mode", "previous_permission_mode", "plan_file_id", "reasoning_effort", "fast_mode", "relaxed_plan", "plan_reflection_auto_approve_override", "danger_reflection_override", "message_count", "total_cost_usd", "last_message_at", "status", "substatus", "plan_mode", "cwd", "error_message", "todos_json", "todos_tool_use_id", "prune_boundary_message_id", "pruned_percent", "prune_enabled", "enabled_tools", "variant", "traits", "is_background", "background_status", "background_result", "background_completed_at", "is_ask_in_passing", "turn_started_at", "message_version", "created_at", "updated_at"},
	"narrator_goals":               {"id", "narrator_id", "objective", "status", "sort_order", "tokens_used", "time_used_seconds", "created_by", "completed_at", "created_at", "updated_at"},
	"narrator_messages":            {"id", "narrator_id", "sdk_message_uuid", "parent_tool_use_id", "role", "content_json", "content_text", "tokens_in", "cost_usd", "turn_usage_json", "provider", "credential_id", "model", "output_tokens", "cached_input_tokens", "cache_creation_input_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "reasoning_tokens", "ttft_ms", "duration_ms", "context_percent", "meter_usage", "meter_unit", "commit_sha", "command_text", "created_by", "created_at"},
	"narrator_message_refs":        {"id", "narrator_id", "message_id", "seq", "is_compact", "pruned_percent", "segment_compact_id"},
	"narrator_sidecars":            {"id", "narrator_id", "message_id", "tool_use_id", "target", "source", "content", "order_index", "created_at"},
	"narrator_tool_calls":          {"id", "narrator_id", "message_id", "tool_use_id", "tool_name", "input_json", "output_json", "status", "duration_ms", "stream_started_at", "permission_started_at", "execution_started_at", "completed_at", "error_message", "permission_decided_by", "permission_decided_at", "permission_deny_message", "permission_decision_reason", "permission_suggestions", "is_background", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "input_cost", "output_cost", "cache_creation_cost", "cache_read_cost", "total_cost", "provider", "model", "result_message_id", "created_at"},
	"terminals":                    {"id", "chapter_id", "narrator_id", "name", "cwd", "dtach_socket", "status", "exit_code", "graph_opened", "graph_x", "graph_y", "graph_width", "graph_height", "created_at"},
	"terminal_tabs":                {"id", "chapter_id", "narrator_id", "name", "sort_order", "created_at"},
	"terminal_view_state":          {"id", "user_id", "chapter_id", "narrator_id", "layout", "active_tab_id", "panel_assignments", "updated_at"},
	"container_instances":          {"id", "chapter_id", "container_id", "service_name", "status", "host_port", "container_port", "proxy_label", "container_ip", "volume_name", "created_at", "updated_at"},
	"port_allocations":             {"port", "chapter_id", "service_name", "allocated_at"},
	"user_preferences":             {"id", "user_id", "auto_load_older_messages", "fast_mode_default", "language", "word_wrap_markdown", "word_wrap_code", "word_wrap_diff", "reply_in_user_language", "show_token_usage", "show_output_stats", "terminal_theme", "terminal_font_size", "recent_tabs", "add_subagent_to_recent_tabs", "notify_on_done", "notify_on_waiting", "notify_pwa_enabled", "notify_sound_enabled", "notify_sound_type", "notify_sound_builtin", "notify_sound_file_id", "notify_dingtalk_enabled", "notify_dingtalk_webhook", "notify_dingtalk_secret", "notify_feishu_enabled", "notify_feishu_webhook", "notify_feishu_secret", "commands", "graph_viewports", "send_mode", "setup_wizard_completed", "gateway_config", "created_at", "updated_at"},
	"users":                        {"id", "username", "password_hash", "role", "avatar_color", "avatar_image_id", "git_username", "git_email", "created_at"},
	"user_favorite_directories":    {"id", "user_id", "path", "label", "sort_order", "created_at"},
	"narrator_file_snapshots":      {"id", "narrator_id", "file_path", "original_content", "created_at"},
	"narrator_patches":             {"id", "narrator_id", "message_id", "tool_use_id", "before_hash", "after_hash", "files_json", "created_at"},
	"merge_sessions":               {"id", "target_chapter_id", "source_chapter_ids", "strategy", "status", "current_index", "merged_count", "current_source_chapter_id", "conflict_files", "error", "locale", "created_at", "updated_at"},
	"narrator_whitelist_dirs":      {"id", "narrator_id", "path", "access_level", "enabled", "created_at"},
	"narrator_blacklist_dirs":      {"id", "narrator_id", "path", "deny_level", "enabled", "created_at"},
	"narrator_whitelist_cmds":      {"id", "narrator_id", "pattern", "enabled", "created_at"},
	"narrator_blacklist_cmds":      {"id", "narrator_id", "pattern", "deny_prompt", "enabled", "created_at"},
	"volume_snapshots":             {"id", "project_id", "name", "description", "source_chapter_id", "service_name", "container_path", "size_bytes", "created_by", "created_at", "updated_at"},
	"volume_snapshot_applications": {"id", "snapshot_id", "chapter_id", "applied_at", "applied_by"},
	"review_conclusions":           {"id", "review_chapter_id", "source_chapter_id", "verdict", "findings_json", "created_at"},
	"workspaces":                   {"id", "user_id", "title", "tree", "created_at", "updated_at"},
	"api_requests":                 {"id", "narrator_id", "message_id", "kind", "provider", "credential_id", "model", "input_tokens", "output_tokens", "cached_input_tokens", "cache_creation_input_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "reasoning_tokens", "ttft_ms", "duration_ms", "cost_usd", "context_percent", "meter_usage", "meter_unit", "error_message", "raw_dump_json", "created_at"},
	"narrator_buffered_messages":   {"id", "narrator_id", "text", "images_json", "command_text", "created_by", "creator_json", "text_file_paths_json", "priority", "seq", "buffered_at"},
	"hooks":                        {"id", "project_id", "event", "matcher", "type", "command", "url", "headers", "prompt", "model", "timeout", "enabled", "sort_order", "created_at", "updated_at"},
	"gateway_session_mappings":     {"id", "platform", "chat_id", "user_id", "username", "narrator_id", "app_user_id", "project_id", "chapter_id", "last_message_at", "created_at", "updated_at"},
	"benchmark_suites":             {"id", "name", "version", "description", "tasks_json", "created_at"},
	"benchmark_runs":               {"id", "suite_id", "name", "model", "system_prompt", "permission_mode", "status", "config", "total_tasks", "completed_tasks", "passed_tasks", "failed_tasks", "total_cost_usd", "total_tokens_in", "total_tokens_out", "total_duration_ms", "started_at", "completed_at", "created_at"},
	"benchmark_task_results":       {"id", "run_id", "task_id", "task_name", "narrator_id", "status", "score", "max_score", "output", "eval_output", "error_message", "tokens_in", "tokens_out", "cost_usd", "duration_ms", "tool_call_count", "message_count", "metadata", "started_at", "completed_at", "created_at"},
	"background_tasks":             {"id", "parent_narrator_id", "type", "status", "command", "exit_code", "subagent_narrator_id", "subagent_type", "tool_use_id", "alias", "title", "output", "output_bytes", "output_truncated", "notified", "started_at", "completed_at", "created_at", "updated_at"},
	"chapters_fts":                 {"title", "description"},
	"narrator_messages_fts":        {"content_text"},
	"narrators_fts":                {"title"},
}

type canonicalIndexRequirement struct {
	Table   string
	Name    string
	Columns []string
	Unique  bool
}

type canonicalForeignKeyRequirement struct {
	Table            string
	Column           string
	ReferencedTable  string
	ReferencedColumn string
}

type canonicalColumnMetadataRequirement struct {
	Table        string  `json:"table"`
	Column       string  `json:"column"`
	Type         string  `json:"type"`
	NotNull      *bool   `json:"notNull,omitempty"`
	DefaultValue *string `json:"defaultValue,omitempty"`
	PrimaryKey   *int    `json:"primaryKey,omitempty"`
}

type canonicalFTSRequirement struct {
	Table     string
	Columns   []string
	Fragments []string
}

type canonicalTriggerRequirement struct {
	Name      string
	Table     string
	Fragments []string
}

// canonicalMainIndexes covers the high-value TS indexes used by graph, narrator,
// search-adjacent, terminal, hooks, usage, and background-task routes. It is a
// readiness contract only; Go must not create any missing index on the main DB.
var canonicalMainIndexes = []canonicalIndexRequirement{
	{Table: "chapters", Name: "idx_chapters_project", Columns: []string{"project_id", "status"}},
	{Table: "chapters", Name: "idx_chapters_parent", Columns: []string{"parent_chapter_id"}},
	{Table: "chapters", Name: "idx_chapters_project_branch", Columns: []string{"project_id", "branch"}, Unique: true},
	{Table: "chapter_edges", Name: "idx_chapter_edges_src_tgt_type", Columns: []string{"source_id", "target_id", "type"}},
	{Table: "chapter_edges", Name: "idx_chapter_edges_project", Columns: []string{"project_id"}},
	{Table: "chapter_edges", Name: "idx_chapter_edges_source", Columns: []string{"source_id"}},
	{Table: "chapter_edges", Name: "idx_chapter_edges_target", Columns: []string{"target_id"}},
	{Table: "chapter_commits", Name: "idx_chapter_commits_sha", Columns: []string{"chapter_id", "sha"}, Unique: true},
	{Table: "chapter_commits", Name: "idx_chapter_commits_chapter", Columns: []string{"chapter_id", "authored_at"}},
	{Table: "chapter_commits", Name: "idx_chapter_commits_narrator", Columns: []string{"narrator_id"}},
	{Table: "narrators", Name: "idx_narrators_chapter", Columns: []string{"chapter_id"}},
	{Table: "narrators", Name: "idx_narrators_parent", Columns: []string{"parent_narrator_id"}},
	{Table: "narrator_goals", Name: "idx_narrator_goals_narrator_order", Columns: []string{"narrator_id", "sort_order"}},
	{Table: "narrator_goals", Name: "idx_narrator_goals_status", Columns: []string{"narrator_id", "status"}},
	{Table: "narrator_messages", Name: "idx_messages_narrator", Columns: []string{"narrator_id", "created_at"}},
	{Table: "narrator_messages", Name: "idx_messages_parent_tool_use_lookup", Columns: []string{"parent_tool_use_id"}},
	{Table: "narrator_messages", Name: "idx_messages_parent_tool_use", Columns: []string{"narrator_id", "parent_tool_use_id"}},
	{Table: "narrator_messages", Name: "idx_messages_toplevel", Columns: []string{"narrator_id", "parent_tool_use_id", "created_at"}},
	{Table: "narrator_message_refs", Name: "idx_narrator_refs_unique", Columns: []string{"narrator_id", "message_id"}, Unique: true},
	{Table: "narrator_message_refs", Name: "idx_narrator_refs_seq", Columns: []string{"narrator_id", "seq"}},
	{Table: "narrator_message_refs", Name: "idx_narrator_refs_message", Columns: []string{"message_id"}},
	{Table: "narrator_message_refs", Name: "idx_narrator_refs_segment_compact", Columns: []string{"segment_compact_id"}},
	{Table: "narrator_tool_calls", Name: "idx_toolcalls_message", Columns: []string{"message_id"}},
	{Table: "narrator_tool_calls", Name: "idx_toolcalls_tool_use_id", Columns: []string{"tool_use_id"}},
	{Table: "narrator_tool_calls", Name: "idx_toolcalls_status", Columns: []string{"narrator_id", "status"}},
	{Table: "narrator_tool_calls", Name: "idx_toolcalls_created", Columns: []string{"narrator_id", "created_at"}},
	{Table: "terminals", Name: "idx_terminals_chapter", Columns: []string{"chapter_id"}},
	{Table: "terminals", Name: "idx_terminals_narrator", Columns: []string{"narrator_id"}},
	{Table: "terminals", Name: "idx_terminals_status", Columns: []string{"status"}},
	{Table: "terminal_tabs", Name: "idx_terminal_tabs_chapter", Columns: []string{"chapter_id", "sort_order"}},
	{Table: "terminal_tabs", Name: "idx_terminal_tabs_narrator", Columns: []string{"narrator_id", "sort_order"}},
	{Table: "terminal_view_state", Name: "idx_view_state_user_chapter", Columns: []string{"user_id", "chapter_id"}, Unique: true},
	{Table: "terminal_view_state", Name: "idx_view_state_user_narrator", Columns: []string{"user_id", "narrator_id"}, Unique: true},
	{Table: "narrator_file_snapshots", Name: "idx_file_snapshots_narrator", Columns: []string{"narrator_id"}},
	{Table: "narrator_file_snapshots", Name: "idx_file_snapshots_narrator_file", Columns: []string{"narrator_id", "file_path"}, Unique: true},
	{Table: "hooks", Name: "idx_hooks_project", Columns: []string{"project_id"}},
	{Table: "hooks", Name: "idx_hooks_event", Columns: []string{"event", "enabled"}},
	{Table: "api_requests", Name: "idx_api_requests_narrator", Columns: []string{"narrator_id", "created_at"}},
	{Table: "api_requests", Name: "idx_api_requests_message", Columns: []string{"message_id"}},
	{Table: "api_requests", Name: "idx_api_requests_provider", Columns: []string{"provider", "created_at"}},
	{Table: "api_requests", Name: "idx_api_requests_kind", Columns: []string{"kind", "created_at"}},
	{Table: "api_requests", Name: "idx_api_requests_created", Columns: []string{"created_at"}},
	{Table: "narrator_buffered_messages", Name: "idx_nbm_narrator_seq", Columns: []string{"narrator_id", "seq"}},
	{Table: "gateway_session_mappings", Name: "idx_gsm_platform_chat_user", Columns: []string{"platform", "chat_id", "user_id"}, Unique: true},
	{Table: "gateway_session_mappings", Name: "idx_gsm_narrator", Columns: []string{"narrator_id"}},
	{Table: "gateway_session_mappings", Name: "idx_gsm_app_user", Columns: []string{"app_user_id"}},
	{Table: "background_tasks", Name: "idx_bg_tasks_parent", Columns: []string{"parent_narrator_id", "status"}},
	{Table: "background_tasks", Name: "idx_bg_tasks_subagent", Columns: []string{"subagent_narrator_id"}},
}

// canonicalColumnMetadata checks representative high-risk TS/Drizzle column
// metadata that affects compatibility-sensitive writes and frontend reads. It is
// a readiness core, not yet an exhaustive dump of every TS column.
var canonicalColumnMetadata = []canonicalColumnMetadataRequirement{
	{Table: "projects", Column: "id", Type: "TEXT", NotNull: canonicalBool(true), PrimaryKey: canonicalInt(1)},
	{Table: "projects", Column: "name", Type: "TEXT", NotNull: canonicalBool(true)},
	{Table: "projects", Column: "flow_mode", Type: "TEXT", NotNull: canonicalBool(true), DefaultValue: canonicalString("'classic'")},
	{Table: "chapters", Column: "id", Type: "TEXT", NotNull: canonicalBool(true), PrimaryKey: canonicalInt(1)},
	{Table: "chapters", Column: "status", Type: "TEXT", NotNull: canonicalBool(true), DefaultValue: canonicalString("'active'")},
	{Table: "chapters", Column: "is_root", Type: "INTEGER", NotNull: canonicalBool(false), DefaultValue: canonicalString("0")},
	{Table: "chapters", Column: "panel_width", Type: "REAL", NotNull: canonicalBool(false)},
	{Table: "chapter_edges", Column: "type", Type: "TEXT", NotNull: canonicalBool(true)},
	{Table: "chapter_commits", Column: "source", Type: "TEXT", NotNull: canonicalBool(true), DefaultValue: canonicalString("'manual'")},
	{Table: "narrator_message_refs", Column: "seq", Type: "INTEGER", NotNull: canonicalBool(true)},
	{Table: "narrator_message_refs", Column: "is_compact", Type: "INTEGER", NotNull: canonicalBool(true), DefaultValue: canonicalString("0")},
	{Table: "narrator_tool_calls", Column: "status", Type: "TEXT", NotNull: canonicalBool(true), DefaultValue: canonicalString("'initializing'")},
	{Table: "narrator_tool_calls", Column: "is_background", Type: "INTEGER", NotNull: canonicalBool(true), DefaultValue: canonicalString("false")},
	{Table: "api_requests", Column: "cost_usd", Type: "REAL", NotNull: canonicalBool(false)},
}

// canonicalForeignKeys checks high-value relational links that protect graph,
// narrator tree, permissions, terminal, usage, and background-task data. It is a
// representative readiness core, not yet an exhaustive dump of every TS FK.
var canonicalForeignKeys = []canonicalForeignKeyRequirement{
	{Table: "exploration_groups", Column: "project_id", ReferencedTable: "projects", ReferencedColumn: "id"},
	{Table: "chapters", Column: "project_id", ReferencedTable: "projects", ReferencedColumn: "id"},
	{Table: "chapters", Column: "parent_chapter_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "chapters", Column: "merged_into_chapter_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "chapters", Column: "exploration_group_id", ReferencedTable: "exploration_groups", ReferencedColumn: "id"},
	{Table: "chapter_edges", Column: "project_id", ReferencedTable: "projects", ReferencedColumn: "id"},
	{Table: "chapter_edges", Column: "source_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "chapter_edges", Column: "target_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "chapter_commits", Column: "chapter_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "chapter_commits", Column: "narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "chapter_commits", Column: "narrator_message_id", ReferencedTable: "narrator_messages", ReferencedColumn: "id"},
	{Table: "narrators", Column: "chapter_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "narrators", Column: "fork_message_id", ReferencedTable: "narrator_messages", ReferencedColumn: "id"},
	{Table: "narrators", Column: "parent_narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "narrator_goals", Column: "narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "narrator_messages", Column: "narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "narrator_message_refs", Column: "narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "narrator_message_refs", Column: "message_id", ReferencedTable: "narrator_messages", ReferencedColumn: "id"},
	{Table: "narrator_sidecars", Column: "narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "narrator_sidecars", Column: "message_id", ReferencedTable: "narrator_messages", ReferencedColumn: "id"},
	{Table: "narrator_tool_calls", Column: "narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "narrator_tool_calls", Column: "message_id", ReferencedTable: "narrator_messages", ReferencedColumn: "id"},
	{Table: "terminals", Column: "chapter_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "terminals", Column: "narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "terminal_view_state", Column: "user_id", ReferencedTable: "users", ReferencedColumn: "id"},
	{Table: "container_instances", Column: "chapter_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "port_allocations", Column: "chapter_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "narrator_file_snapshots", Column: "narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "review_conclusions", Column: "review_chapter_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "review_conclusions", Column: "source_chapter_id", ReferencedTable: "chapters", ReferencedColumn: "id"},
	{Table: "api_requests", Column: "narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "api_requests", Column: "message_id", ReferencedTable: "narrator_messages", ReferencedColumn: "id"},
	{Table: "gateway_session_mappings", Column: "narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "background_tasks", Column: "parent_narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
	{Table: "background_tasks", Column: "subagent_narrator_id", ReferencedTable: "narrators", ReferencedColumn: "id"},
}

var canonicalFTSObjects = []canonicalFTSRequirement{
	{Table: "chapters_fts", Columns: []string{"title", "description"}, Fragments: []string{"using fts5", "content='chapters'", "content_rowid=rowid", "tokenize='trigram'"}},
	{Table: "narrator_messages_fts", Columns: []string{"content_text"}, Fragments: []string{"using fts5", "content='narrator_messages'", "content_rowid=rowid", "tokenize='trigram'"}},
	{Table: "narrators_fts", Columns: []string{"title"}, Fragments: []string{"using fts5", "content='narrators'", "content_rowid=rowid", "tokenize='trigram'"}},
}

func canonicalBool(value bool) *bool {
	return &value
}

func canonicalInt(value int) *int {
	return &value
}

func canonicalString(value string) *string {
	return &value
}

var canonicalFTSTriggers = []canonicalTriggerRequirement{
	{Name: "chapters_fts_insert", Table: "chapters", Fragments: []string{"after insert on chapters", "insert into chapters_fts(rowid, title, description)", "new.rowid"}},
	{Name: "chapters_fts_update", Table: "chapters", Fragments: []string{"after update on chapters", "insert into chapters_fts(chapters_fts, rowid, title, description)", "'delete'", "insert into chapters_fts(rowid, title, description)"}},
	{Name: "chapters_fts_delete", Table: "chapters", Fragments: []string{"after delete on chapters", "insert into chapters_fts(chapters_fts, rowid, title, description)", "'delete'"}},
	{Name: "narrator_messages_fts_insert", Table: "narrator_messages", Fragments: []string{"after insert on narrator_messages", "insert into narrator_messages_fts(rowid, content_text)", "new.rowid"}},
	{Name: "narrator_messages_fts_update", Table: "narrator_messages", Fragments: []string{"after update on narrator_messages", "insert into narrator_messages_fts(narrator_messages_fts, rowid, content_text)", "'delete'", "insert into narrator_messages_fts(rowid, content_text)"}},
	{Name: "narrator_messages_fts_delete", Table: "narrator_messages", Fragments: []string{"after delete on narrator_messages", "insert into narrator_messages_fts(narrator_messages_fts, rowid, content_text)", "'delete'"}},
	{Name: "narrators_fts_insert", Table: "narrators", Fragments: []string{"after insert on narrators", "when new.title is not null", "insert into narrators_fts(rowid, title)"}},
	{Name: "narrators_fts_update", Table: "narrators", Fragments: []string{"after update of title on narrators", "when old.title is not null or new.title is not null", "insert into narrators_fts(narrators_fts, rowid, title)", "insert or ignore into narrators_fts(rowid, title)"}},
	{Name: "narrators_fts_delete", Table: "narrators", Fragments: []string{"after delete on narrators", "when old.title is not null", "insert into narrators_fts(narrators_fts, rowid, title)"}},
}

var ErrCanonicalSchemaMismatch = errors.New("canonical TS database schema mismatch")

type CanonicalSchemaContractReport struct {
	Mode                         string   `json:"mode"`
	StrategyStage                string   `json:"strategyStage"`
	TransitionalStrategy         bool     `json:"transitionalStrategy"`
	TargetMode                   string   `json:"targetMode"`
	MainSchemaOwner              string   `json:"mainSchemaOwner"`
	TargetMainSchemaOwner        string   `json:"targetMainSchemaOwner"`
	CurrentRepairOwner           string   `json:"currentRepairOwner"`
	FutureGoResponsibilities     []string `json:"futureGoResponsibilities"`
	GoMainMigrations             bool     `json:"goMainMigrations"`
	GoEnsureColumns              bool     `json:"goEnsureColumns"`
	GoFTSRepair                  bool     `json:"goFtsRepair"`
	CanonicalSchemaHash          string   `json:"canonicalSchemaHash"`
	ExpectedMigrationHash        string   `json:"expectedMigrationHash"`
	ExpectedMigrationCount       int      `json:"expectedMigrationCount"`
	ExpectedLastMigration        string   `json:"expectedLastMigration"`
	GoJournalTable               string   `json:"goJournalTable"`
	DrizzleJournalTable          string   `json:"drizzleJournalTable"`
	ReadOnlyVerifier             bool     `json:"readOnlyVerifier"`
	CanonicalTableCount          int      `json:"canonicalTableCount"`
	CanonicalIndexCount          int      `json:"canonicalIndexCount"`
	CanonicalColumnMetadataCount int      `json:"canonicalColumnMetadataCount"`
	CanonicalForeignKeyCount     int      `json:"canonicalForeignKeyCount"`
	CanonicalFTSTableCount       int      `json:"canonicalFtsTableCount"`
	CanonicalFTSTriggerCount     int      `json:"canonicalFtsTriggerCount"`
}

func CanonicalSchemaContract() CanonicalSchemaContractReport {
	lastMigration := ""
	if len(embeddedDrizzleMigrationSnapshot) > 0 {
		lastMigration = embeddedDrizzleMigrationSnapshot[len(embeddedDrizzleMigrationSnapshot)-1].Tag
	}
	return CanonicalSchemaContractReport{
		Mode:                         "ts-owned-db-compatibility",
		StrategyStage:                "transition",
		TransitionalStrategy:         true,
		TargetMode:                   "pure-go-db-management",
		MainSchemaOwner:              "typescript-drizzle",
		TargetMainSchemaOwner:        "go",
		CurrentRepairOwner:           "typescript-drizzle-and-ts-fts-setup",
		FutureGoResponsibilities:     []string{"main schema migrations", "startup backfills and repair", "FTS/search lifecycle", "schema drift fixtures and release gates"},
		GoMainMigrations:             false,
		GoEnsureColumns:              false,
		GoFTSRepair:                  false,
		CanonicalSchemaHash:          canonicalSchemaContractHash(),
		ExpectedMigrationHash:        expectedDrizzleJournalHash(),
		ExpectedMigrationCount:       len(embeddedDrizzleMigrationSnapshot),
		ExpectedLastMigration:        lastMigration,
		GoJournalTable:               goMigrationJournalTable,
		DrizzleJournalTable:          "__drizzle_migrations",
		ReadOnlyVerifier:             true,
		CanonicalTableCount:          len(canonicalMainSchema),
		CanonicalIndexCount:          len(canonicalMainIndexes),
		CanonicalColumnMetadataCount: len(canonicalColumnMetadata),
		CanonicalForeignKeyCount:     len(canonicalForeignKeys),
		CanonicalFTSTableCount:       len(canonicalFTSObjects),
		CanonicalFTSTriggerCount:     len(canonicalFTSTriggers),
	}
}

// VerifyCanonicalSchema validates that the already-existing main database has
// the TS-owned tables, columns, indexes, foreign keys, FTS trigram objects, and
// FTS sync triggers required by go_backend. It is intentionally read-only: do
// not add repair logic here.
func (s *Store) VerifyCanonicalSchema(ctx context.Context) error {
	if s == nil || s.SQL == nil {
		return fmt.Errorf("%w: database is not open", ErrCanonicalSchemaMismatch)
	}
	return VerifyCanonicalSchema(ctx, s.SQL)
}

func VerifyCanonicalSchema(ctx context.Context, sqlDB *sql.DB) error {
	missingTables := []string{}
	missingColumns := []string{}
	readinessFailures := []string{}

	tables := make([]string, 0, len(canonicalMainSchema))
	for table := range canonicalMainSchema {
		tables = append(tables, table)
	}
	sort.Strings(tables)

	for _, table := range tables {
		exists, columns, err := tableColumns(ctx, sqlDB, table)
		if err != nil {
			return fmt.Errorf("%w: inspect %s: %v", ErrCanonicalSchemaMismatch, table, err)
		}
		if !exists {
			missingTables = append(missingTables, table)
			continue
		}
		for _, column := range canonicalMainSchema[table] {
			if _, ok := columns[column]; !ok {
				missingColumns = append(missingColumns, table+"."+column)
			}
		}
	}

	if len(missingTables) == 0 && len(missingColumns) == 0 {
		var err error
		readinessFailures, err = verifyCanonicalReadinessObjects(ctx, sqlDB)
		if err != nil {
			return fmt.Errorf("%w: inspect readiness objects: %v", ErrCanonicalSchemaMismatch, err)
		}
	}

	if len(missingTables) == 0 && len(missingColumns) == 0 && len(readinessFailures) == 0 {
		return nil
	}

	parts := []string{}
	if len(missingTables) > 0 {
		parts = append(parts, "missing tables: "+strings.Join(missingTables, ", "))
	}
	if len(missingColumns) > 0 {
		parts = append(parts, "missing columns: "+strings.Join(missingColumns, ", "))
	}
	if len(readinessFailures) > 0 {
		parts = append(parts, "missing or incompatible schema objects: "+strings.Join(readinessFailures, "; "))
	}
	return fmt.Errorf("%w: %s. Start the TypeScript backend once to run Drizzle migrations and FTS setup before using go_backend; Go readiness checks are read-only and will not repair the main database", ErrCanonicalSchemaMismatch, strings.Join(parts, "; "))
}

func verifyCanonicalReadinessObjects(ctx context.Context, sqlDB *sql.DB) ([]string, error) {
	failures := []string{}
	metadataFailures, err := verifyCanonicalColumnMetadata(ctx, sqlDB)
	if err != nil {
		return nil, err
	}
	failures = append(failures, metadataFailures...)

	for _, expected := range canonicalMainIndexes {
		actual, err := indexDetails(ctx, sqlDB, expected.Table, expected.Name)
		if err != nil {
			return nil, err
		}
		if !actual.Exists {
			failures = append(failures, "missing index "+expected.Name+" on "+expected.Table)
			continue
		}
		if expected.Unique && !actual.Unique {
			failures = append(failures, "index "+expected.Name+" on "+expected.Table+" is not unique")
		}
		if !sameStringSlice(actual.Columns, expected.Columns) {
			failures = append(failures, "index "+expected.Name+" columns = "+strings.Join(actual.Columns, ",")+", want "+strings.Join(expected.Columns, ","))
		}
	}

	for _, expected := range canonicalForeignKeys {
		ok, err := hasForeignKey(ctx, sqlDB, expected)
		if err != nil {
			return nil, err
		}
		if !ok {
			failures = append(failures, fmt.Sprintf("missing FK %s.%s -> %s.%s", expected.Table, expected.Column, expected.ReferencedTable, expected.ReferencedColumn))
		}
	}

	for _, expected := range canonicalFTSObjects {
		ok, err := ftsObjectReady(ctx, sqlDB, expected)
		if err != nil {
			return nil, err
		}
		if !ok {
			failures = append(failures, "FTS table "+expected.Table+" is missing TS trigram external-content SQL")
		}
	}

	for _, expected := range canonicalFTSTriggers {
		ok, err := triggerReady(ctx, sqlDB, expected)
		if err != nil {
			return nil, err
		}
		if !ok {
			failures = append(failures, "FTS trigger "+expected.Name+" is missing or incompatible")
		}
	}
	return failures, nil
}

func verifyCanonicalColumnMetadata(ctx context.Context, sqlDB *sql.DB) ([]string, error) {
	failures := []string{}
	cache := map[string]map[string]SQLiteColumnSnapshot{}
	for _, expected := range canonicalColumnMetadata {
		columns, ok := cache[expected.Table]
		if !ok {
			var err error
			columns, err = tableColumnMetadata(ctx, sqlDB, expected.Table)
			if err != nil {
				return nil, err
			}
			cache[expected.Table] = columns
		}
		actual, ok := columns[expected.Column]
		if !ok {
			failures = append(failures, "missing column metadata "+expected.Table+"."+expected.Column)
			continue
		}
		columnName := expected.Table + "." + expected.Column
		if expected.Type != "" && !strings.EqualFold(strings.TrimSpace(actual.Type), expected.Type) {
			failures = append(failures, fmt.Sprintf("column %s type = %s, want %s", columnName, actual.Type, expected.Type))
		}
		if expected.NotNull != nil && actual.NotNull != *expected.NotNull {
			failures = append(failures, fmt.Sprintf("column %s notNull = %t, want %t", columnName, actual.NotNull, *expected.NotNull))
		}
		if expected.DefaultValue != nil {
			actualDefault := "<none>"
			if actual.DefaultValue != nil {
				actualDefault = strings.TrimSpace(*actual.DefaultValue)
			}
			wantDefault := strings.TrimSpace(*expected.DefaultValue)
			if actualDefault != wantDefault {
				failures = append(failures, fmt.Sprintf("column %s default = %s, want %s", columnName, actualDefault, wantDefault))
			}
		}
		if expected.PrimaryKey != nil && actual.PK != *expected.PrimaryKey {
			failures = append(failures, fmt.Sprintf("column %s pk = %d, want %d", columnName, actual.PK, *expected.PrimaryKey))
		}
	}
	return failures, nil
}

func tableColumnMetadata(ctx context.Context, sqlDB *sql.DB, table string) (map[string]SQLiteColumnSnapshot, error) {
	rows, err := sqlDB.QueryContext(ctx, `PRAGMA table_info(`+quoteIdentifier(table)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := map[string]SQLiteColumnSnapshot{}
	for rows.Next() {
		var column SQLiteColumnSnapshot
		var notNull int
		var defaultValue sql.NullString
		if err := rows.Scan(&column.CID, &column.Name, &column.Type, &notNull, &defaultValue, &column.PK); err != nil {
			return nil, err
		}
		column.NotNull = notNull != 0
		if defaultValue.Valid {
			value := defaultValue.String
			column.DefaultValue = &value
		}
		columns[column.Name] = column
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

type indexInfo struct {
	Exists  bool
	Unique  bool
	Columns []string
}

func indexDetails(ctx context.Context, sqlDB *sql.DB, table, name string) (indexInfo, error) {
	rows, err := sqlDB.QueryContext(ctx, `PRAGMA index_list(`+quoteIdentifier(table)+`)`)
	if err != nil {
		return indexInfo{}, err
	}

	actual := indexInfo{}
	for rows.Next() {
		var seq int
		var indexName string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &indexName, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return indexInfo{}, err
		}
		if indexName == name {
			actual.Exists = true
			actual.Unique = unique != 0
			break
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return indexInfo{}, err
	}
	if err := rows.Close(); err != nil {
		return indexInfo{}, err
	}
	if !actual.Exists {
		return actual, nil
	}

	cols, err := indexColumns(ctx, sqlDB, name)
	if err != nil {
		return indexInfo{}, err
	}
	actual.Columns = cols
	return actual, nil
}

func indexColumns(ctx context.Context, sqlDB *sql.DB, indexName string) ([]string, error) {
	rows, err := sqlDB.QueryContext(ctx, `PRAGMA index_info(`+quoteStringLiteral(indexName)+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := []string{}
	for rows.Next() {
		var seqno int
		var cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func hasForeignKey(ctx context.Context, sqlDB *sql.DB, expected canonicalForeignKeyRequirement) (bool, error) {
	rows, err := sqlDB.QueryContext(ctx, `PRAGMA foreign_key_list(`+quoteIdentifier(expected.Table)+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var seq int
		var table string
		var from string
		var to string
		var onUpdate string
		var onDelete string
		var match string
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return false, err
		}
		if table == expected.ReferencedTable && from == expected.Column && to == expected.ReferencedColumn {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func ftsObjectReady(ctx context.Context, sqlDB *sql.DB, expected canonicalFTSRequirement) (bool, error) {
	exists, columns, err := tableColumns(ctx, sqlDB, expected.Table)
	if err != nil || !exists {
		return false, err
	}
	for _, column := range expected.Columns {
		if _, ok := columns[column]; !ok {
			return false, nil
		}
	}
	sqlText, ok, err := schemaSQL(ctx, sqlDB, "table", expected.Table)
	if err != nil || !ok {
		return false, err
	}
	return containsAllSQLFragments(sqlText, expected.Fragments), nil
}

func triggerReady(ctx context.Context, sqlDB *sql.DB, expected canonicalTriggerRequirement) (bool, error) {
	sqlText, ok, err := schemaSQL(ctx, sqlDB, "trigger", expected.Name)
	if err != nil || !ok {
		return false, err
	}
	return strings.EqualFold(schemaTableForObject(ctx, sqlDB, "trigger", expected.Name), expected.Table) && containsAllSQLFragments(sqlText, expected.Fragments), nil
}

func schemaTableForObject(ctx context.Context, sqlDB *sql.DB, objectType, name string) string {
	var tableName string
	_ = sqlDB.QueryRowContext(ctx, `SELECT tbl_name FROM sqlite_master WHERE type = ? AND name = ?`, objectType, name).Scan(&tableName)
	return tableName
}

func schemaSQL(ctx context.Context, sqlDB *sql.DB, objectType, name string) (string, bool, error) {
	var sqlText string
	err := sqlDB.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = ? AND name = ?`, objectType, name).Scan(&sqlText)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return sqlText, true, nil
}

func containsAllSQLFragments(sqlText string, fragments []string) bool {
	normalized := normalizeSQLForCompare(sqlText)
	compact := strings.ReplaceAll(normalized, " ", "")
	for _, fragment := range fragments {
		want := normalizeSQLForCompare(fragment)
		if strings.Contains(normalized, want) || strings.Contains(compact, strings.ReplaceAll(want, " ", "")) {
			continue
		}
		return false
	}
	return true
}

func normalizeSQLForCompare(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func tableColumns(ctx context.Context, sqlDB *sql.DB, table string) (bool, map[string]struct{}, error) {
	var tableName string
	err := sqlDB.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE name = ? AND type IN ('table', 'view')`, table).Scan(&tableName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil, nil
		}
		return false, nil, err
	}

	rows, err := sqlDB.QueryContext(ctx, `PRAGMA table_info(`+quoteIdentifier(table)+`)`)
	if err != nil {
		return true, nil, err
	}
	defer rows.Close()

	columns := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name string
		var typeValue string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typeValue, &notNull, &defaultValue, &pk); err != nil {
			return true, nil, err
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return true, nil, err
	}
	return true, columns, nil
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteStringLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
