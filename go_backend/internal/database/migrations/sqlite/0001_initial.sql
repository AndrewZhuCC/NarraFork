-- 0001_initial.sql
-- Complete NarraFork schema (exported from live database)
-- FTS5 virtual tables and triggers are in 0002_fts.sql

CREATE TABLE IF NOT EXISTS _migrations (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			name    TEXT NOT NULL UNIQUE,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
CREATE TABLE IF NOT EXISTS "api_requests" (
	id text PRIMARY KEY NOT NULL,
	narrator_id text,
	message_id text,
	kind text DEFAULT 'narrator' NOT NULL,
	provider text,
	credential_id text,
	model text,
	input_tokens integer DEFAULT 0 NOT NULL,
	output_tokens integer DEFAULT 0 NOT NULL,
	cached_input_tokens integer DEFAULT 0 NOT NULL,
	cache_creation_input_tokens integer DEFAULT 0 NOT NULL,
	cache_creation_5m_tokens integer DEFAULT 0 NOT NULL,
	cache_creation_1h_tokens integer DEFAULT 0 NOT NULL,
	reasoning_tokens integer DEFAULT 0 NOT NULL,
	ttft_ms integer,
	duration_ms integer,
	cost_usd real,
	context_percent real,
	meter_usage real,
	meter_unit text,
	error_message text,
	raw_dump_json text,
	created_at text NOT NULL,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (message_id) REFERENCES narrator_messages(id) ON UPDATE no action ON DELETE set null
);
CREATE TABLE IF NOT EXISTS "background_tasks" (
	id text PRIMARY KEY NOT NULL,
	parent_narrator_id text NOT NULL,
	type text NOT NULL,
	status text NOT NULL,
	command text,
	exit_code integer,
	subagent_narrator_id text,
	subagent_type text,
	tool_use_id text,
	alias text,
	title text,
	output text,
	output_bytes integer DEFAULT 0 NOT NULL,
	output_truncated integer DEFAULT false NOT NULL,
	notified integer DEFAULT false NOT NULL,
	started_at text NOT NULL,
	completed_at text,
	created_at text NOT NULL,
	updated_at text NOT NULL,
	FOREIGN KEY (parent_narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (subagent_narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS "benchmark_runs" (
	id text PRIMARY KEY NOT NULL,
	suite_id text NOT NULL,
	name text NOT NULL,
	model text NOT NULL,
	system_prompt text,
	permission_mode text DEFAULT 'bypassPermissions',
	status text DEFAULT 'pending' NOT NULL,
	config text,
	total_tasks integer DEFAULT 0,
	completed_tasks integer DEFAULT 0,
	passed_tasks integer DEFAULT 0,
	failed_tasks integer DEFAULT 0,
	total_cost_usd real DEFAULT 0,
	total_tokens_in integer DEFAULT 0,
	total_tokens_out integer DEFAULT 0,
	total_duration_ms integer DEFAULT 0,
	started_at text,
	completed_at text,
	created_at text NOT NULL,
	FOREIGN KEY (suite_id) REFERENCES benchmark_suites(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS benchmark_suites (
	id text PRIMARY KEY NOT NULL,
	name text NOT NULL,
	version text,
	description text,
	tasks_json text,
	created_at text NOT NULL
);
CREATE TABLE IF NOT EXISTS benchmark_task_results (
	id text PRIMARY KEY NOT NULL,
	run_id text NOT NULL,
	task_id text NOT NULL,
	task_name text NOT NULL,
	narrator_id text,
	status text DEFAULT 'pending' NOT NULL,
	score real,
	max_score real,
	output text,
	eval_output text,
	error_message text,
	tokens_in integer DEFAULT 0,
	tokens_out integer DEFAULT 0,
	cost_usd real DEFAULT 0,
	duration_ms integer DEFAULT 0,
	tool_call_count integer DEFAULT 0,
	message_count integer DEFAULT 0,
	metadata text,
	started_at text,
	completed_at text,
	created_at text NOT NULL,
	FOREIGN KEY (run_id) REFERENCES benchmark_runs(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS chapter_commits (
	id text PRIMARY KEY NOT NULL,
	chapter_id text NOT NULL,
	sha text NOT NULL,
	message text NOT NULL,
	full_message text,
	author_name text,
	author_email text,
	authored_at text NOT NULL,
	source text DEFAULT 'manual' NOT NULL,
	narrator_id text,
	narrator_message_id text,
	files_changed integer,
	lines_added integer,
	lines_removed integer,
	created_at text NOT NULL,
	FOREIGN KEY (chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (narrator_message_id) REFERENCES narrator_messages(id) ON UPDATE no action ON DELETE set null
);
CREATE TABLE IF NOT EXISTS chapter_edges (
	id text PRIMARY KEY NOT NULL,
	project_id text NOT NULL,
	source_id text NOT NULL,
	target_id text NOT NULL,
	type text NOT NULL,
	metadata text,
	created_at text NOT NULL,
	FOREIGN KEY (project_id) REFERENCES projects(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (source_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (target_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS chapters (
	id text PRIMARY KEY NOT NULL,
	project_id text NOT NULL,
	title text NOT NULL,
	description text,
	status text DEFAULT 'active' NOT NULL,
	role text DEFAULT 'branch' NOT NULL,
	branch text NOT NULL,
	worktree_path text,
	base_branch text NOT NULL,
	parent_chapter_id text,
	fork_point text,
	merged_into_chapter_id text,
	merge_commit_sha text,
	merge_strategy text,
	container_config text,
	exploration_group_id text,
	is_root integer DEFAULT 0,
	head_commit_sha text,
	start_commit_sha text,
	commit_count integer DEFAULT 0,
	color text,
	group_label text,
	pinned integer DEFAULT 0,
	panel_expanded integer DEFAULT 0,
	panel_width real,
	panel_height real,
	last_accessed_at text,
	created_at text NOT NULL,
	updated_at text NOT NULL, pre_merge_target_sha text, review_source_chapter_id text REFERENCES chapters(id), review_status text, "anchor_commit_sha" TEXT, "axis_offset" REAL NOT NULL DEFAULT 0, "cross_offset" REAL NOT NULL DEFAULT 0,
	FOREIGN KEY (project_id) REFERENCES projects(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (parent_chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (merged_into_chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (exploration_group_id) REFERENCES exploration_groups(id) ON UPDATE no action ON DELETE set null
);
CREATE TABLE IF NOT EXISTS chat_group_members (
	id text PRIMARY KEY NOT NULL,
	group_id text NOT NULL,
	member_type text NOT NULL,
	user_id text,
	narrator_id text,
	role text DEFAULT 'participant' NOT NULL,
	can_control integer DEFAULT false NOT NULL,
	joined_at text NOT NULL,
	FOREIGN KEY (group_id) REFERENCES chat_groups(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS chat_group_messages (
	id text PRIMARY KEY NOT NULL,
	group_id text NOT NULL,
	sender_type text NOT NULL,
	sender_user_id text,
	sender_narrator_id text,
	content text NOT NULL,
	urgent integer DEFAULT false NOT NULL,
	created_at text NOT NULL,
	FOREIGN KEY (group_id) REFERENCES chat_groups(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (sender_user_id) REFERENCES users(id) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (sender_narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE set null
);
CREATE TABLE IF NOT EXISTS chat_groups (
	id text PRIMARY KEY NOT NULL,
	title text,
	origin_narrator_id text,
	project_id text,
	created_by text,
	status text DEFAULT 'active' NOT NULL,
	created_at text NOT NULL,
	updated_at text NOT NULL,
	FOREIGN KEY (origin_narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (project_id) REFERENCES projects(id) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (created_by) REFERENCES users(id) ON UPDATE no action ON DELETE set null
);
CREATE TABLE IF NOT EXISTS container_instances (
	id text PRIMARY KEY NOT NULL,
	chapter_id text NOT NULL,
	container_id text,
	service_name text NOT NULL,
	status text DEFAULT 'created' NOT NULL,
	host_port integer,
	container_port integer,
	proxy_label text,
	container_ip text,
	volume_name text,
	created_at text NOT NULL,
	updated_at text NOT NULL,
	FOREIGN KEY (chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS custom_subagents (
		name TEXT PRIMARY KEY,
		description TEXT NOT NULL DEFAULT '',
		tool_access TEXT NOT NULL DEFAULT 'readOnly',
		custom_tools TEXT NOT NULL DEFAULT '[]',
		default_model TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL
	);
CREATE TABLE IF NOT EXISTS exploration_groups (
	id text PRIMARY KEY NOT NULL,
	project_id text NOT NULL,
	title text NOT NULL,
	description text,
	base_chapter_id text,
	status text DEFAULT 'active' NOT NULL,
	decided_chapter_id text,
	created_at text NOT NULL,
	updated_at text NOT NULL,
	FOREIGN KEY (project_id) REFERENCES projects(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (base_chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (decided_chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE set null
);
CREATE TABLE IF NOT EXISTS file_attributions (
	id text PRIMARY KEY NOT NULL,
	workspace_path text NOT NULL,
	file_path text NOT NULL,
	narrator_id text,
	subagent_type text,
	action text NOT NULL,
	tool_name text,
	tool_use_id text,
	changed_at text NOT NULL,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE set null
);
CREATE TABLE IF NOT EXISTS gateway_session_mappings (
	id text PRIMARY KEY NOT NULL,
	platform text NOT NULL,
	chat_id text NOT NULL,
	user_id text NOT NULL,
	username text,
	narrator_id text NOT NULL REFERENCES narrators(id) ON DELETE cascade,
	project_id text REFERENCES projects(id) ON DELETE set null,
	chapter_id text REFERENCES chapters(id) ON DELETE set null,
	last_message_at text,
	created_at text NOT NULL,
	updated_at text NOT NULL
, app_user_id text REFERENCES users(id));
CREATE TABLE IF NOT EXISTS hooks (
	id text PRIMARY KEY NOT NULL,
	project_id text,
	event text NOT NULL,
	matcher text DEFAULT '' NOT NULL,
	type text NOT NULL,
	command text,
	url text,
	headers text,
	prompt text,
	model text,
	timeout integer DEFAULT 30 NOT NULL,
	enabled integer DEFAULT true NOT NULL,
	sort_order integer DEFAULT 0 NOT NULL,
	created_at text NOT NULL,
	updated_at text NOT NULL,
	FOREIGN KEY (project_id) REFERENCES projects(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS mcp_servers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		transport TEXT NOT NULL DEFAULT 'stdio',
		command TEXT,
		args TEXT NOT NULL DEFAULT '[]',
		cwd TEXT,
		url TEXT,
		env TEXT NOT NULL DEFAULT '{}',
		headers TEXT NOT NULL DEFAULT '{}',
		enabled INTEGER NOT NULL DEFAULT 1,
		default_behavior TEXT,
		tool_permissions TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
CREATE TABLE IF NOT EXISTS merge_sessions (
	id text PRIMARY KEY NOT NULL,
	target_chapter_id text NOT NULL,
	source_chapter_ids text NOT NULL,
	strategy text DEFAULT 'merge' NOT NULL,
	status text NOT NULL,
	current_index integer DEFAULT 0 NOT NULL,
	merged_count integer DEFAULT 0 NOT NULL,
	current_source_chapter_id text,
	conflict_files text,
	error text,
	locale text,
	created_at text NOT NULL,
	updated_at text NOT NULL,
	FOREIGN KEY (target_chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS narrator_blacklist_cmds (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	pattern text NOT NULL,
	deny_prompt text,
	enabled integer DEFAULT true NOT NULL,
	created_at text NOT NULL,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS narrator_blacklist_dirs (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	path text NOT NULL,
	deny_level text DEFAULT 'denyAll' NOT NULL,
	enabled integer DEFAULT true NOT NULL,
	created_at text NOT NULL,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS narrator_buffered_messages (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	text text NOT NULL,
	images_json text,
	command_text text,
	created_by text,
	creator_json text,
	text_file_paths_json text,
	seq integer NOT NULL,
	buffered_at text NOT NULL
, priority integer DEFAULT false NOT NULL, bash_command text);
CREATE TABLE IF NOT EXISTS narrator_file_snapshots (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	file_path text NOT NULL,
	original_content text,
	created_at text NOT NULL,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS narrator_goals (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	objective text NOT NULL,
	status text DEFAULT 'pending' NOT NULL,
	sort_order integer DEFAULT 0 NOT NULL,
	tokens_used integer DEFAULT 0 NOT NULL,
	time_used_seconds integer DEFAULT 0 NOT NULL,
	created_by text,
	completed_at text,
	created_at text NOT NULL,
	updated_at text NOT NULL,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (created_by) REFERENCES users(id) ON UPDATE no action ON DELETE set null
);
CREATE TABLE IF NOT EXISTS narrator_message_refs (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	message_id text NOT NULL,
	seq integer NOT NULL,
	is_compact integer DEFAULT 0 NOT NULL,
	pruned_percent integer, segment_compact_id text,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (message_id) REFERENCES narrator_messages(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS narrator_messages (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	sdk_message_uuid text,
	parent_tool_use_id text,
	role text NOT NULL,
	content_json text NOT NULL,
	content_text text,
	tokens_in integer,
	cost_usd real,
	turn_usage_json text,
	context_percent real,
	meter_usage real,
	meter_unit text,
	commit_sha text,
	command_text text,
	created_by text,
	created_at text NOT NULL, provider text, model text, output_tokens integer, cached_input_tokens integer, cache_creation_input_tokens integer, cache_creation_5m_tokens integer, cache_creation_1h_tokens integer, reasoning_tokens integer, ttft_ms integer, duration_ms integer, credential_id text, edited_at text, edited_by text REFERENCES users(id), original_content_json text,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (created_by) REFERENCES users(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS narrator_patches (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	message_id text NOT NULL,
	tool_use_id text NOT NULL,
	before_hash text NOT NULL,
	after_hash text NOT NULL,
	files_json text NOT NULL,
	created_at text NOT NULL,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (message_id) REFERENCES narrator_messages(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS narrator_sidecars (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	message_id text,
	tool_use_id text,
	target text NOT NULL,
	source text NOT NULL,
	content text NOT NULL,
	order_index integer DEFAULT 0 NOT NULL,
	created_at text NOT NULL,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (message_id) REFERENCES narrator_messages(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS "narrator_tool_calls" (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	message_id text NOT NULL,
	tool_use_id text NOT NULL,
	tool_name text NOT NULL,
	input_json text,
	output_json text,
	status text DEFAULT 'initializing' NOT NULL,
	duration_ms integer,
	error_message text,
	permission_decided_by text,
	permission_decided_at text,
	permission_deny_message text,
	permission_decision_reason text,
	permission_suggestions text,
	is_background integer DEFAULT false NOT NULL,
	input_tokens integer DEFAULT 0 NOT NULL,
	output_tokens integer DEFAULT 0 NOT NULL,
	cache_creation_tokens integer DEFAULT 0 NOT NULL,
	cache_read_tokens integer DEFAULT 0 NOT NULL,
	cache_creation_5m_tokens integer DEFAULT 0 NOT NULL,
	cache_creation_1h_tokens integer DEFAULT 0 NOT NULL,
	input_cost real DEFAULT 0 NOT NULL,
	output_cost real DEFAULT 0 NOT NULL,
	cache_creation_cost real DEFAULT 0 NOT NULL,
	cache_read_cost real DEFAULT 0 NOT NULL,
	total_cost real DEFAULT 0 NOT NULL,
	provider text,
	model text,
	result_message_id text,
	created_at text NOT NULL, stream_started_at text, permission_started_at text, execution_started_at text, completed_at text,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (message_id) REFERENCES narrator_messages(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS narrator_whitelist_cmds (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	pattern text NOT NULL,
	enabled integer DEFAULT true NOT NULL,
	created_at text NOT NULL,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS narrator_whitelist_dirs (
	id text PRIMARY KEY NOT NULL,
	narrator_id text NOT NULL,
	path text NOT NULL,
	access_level text DEFAULT 'readOnly' NOT NULL,
	enabled integer DEFAULT true NOT NULL,
	created_at text NOT NULL,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS narrators (
	id text PRIMARY KEY NOT NULL,
	chapter_id text,
	api_conversation_id text,
	fork_message_id text,
	type text DEFAULT 'primary' NOT NULL,
	subagent_type text,
	title text,
	inherit_mode text DEFAULT 'fresh' NOT NULL,
	parent_narrator_id text,
	context_summary text,
	model text DEFAULT 'claude-sonnet',
	system_prompt text,
	permission_mode text DEFAULT 'default',
	previous_permission_mode text,
	reasoning_effort text,
	fast_mode integer DEFAULT false NOT NULL,
	message_count integer DEFAULT 0,
	total_cost_usd real DEFAULT 0,
	last_message_at text,
	status text DEFAULT 'idle' NOT NULL,
	plan_mode integer DEFAULT false NOT NULL,
	cwd text,
	error_message text,
	todos_json text,
	todos_tool_use_id text,
	prune_boundary_message_id text,
	pruned_percent integer,
	prune_enabled integer DEFAULT true NOT NULL,
	is_background integer DEFAULT false NOT NULL,
	background_status text,
	background_result text,
	background_completed_at text,
	created_at text NOT NULL,
	updated_at text NOT NULL, relaxed_plan integer DEFAULT false NOT NULL, "enabled_tools" TEXT, turn_started_at text, message_version integer DEFAULT 0 NOT NULL, pending_model_restore text, is_ask_in_passing integer DEFAULT false NOT NULL, substatus text DEFAULT '[]' NOT NULL, variant text DEFAULT 'primary' NOT NULL, traits text DEFAULT '[]' NOT NULL, plan_file_id text, plan_reflection_auto_approve_override text DEFAULT 'inherit' NOT NULL, danger_reflection_override text DEFAULT 'inherit' NOT NULL, handle text,
	FOREIGN KEY (chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (fork_message_id) REFERENCES narrator_messages(id) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (parent_narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (prune_boundary_message_id) REFERENCES narrator_messages(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS port_allocations (
	port integer PRIMARY KEY NOT NULL,
	chapter_id text,
	service_name text,
	allocated_at text NOT NULL,
	FOREIGN KEY (chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS projects (
	id text PRIMARY KEY NOT NULL,
	name text NOT NULL,
	description text,
	status text DEFAULT 'active' NOT NULL,
	git_path text,
	remote_url text,
	default_branch text DEFAULT 'main',
	startup_script text,
	copy_files text,
	chapter_settings text,
	proxy_domain text,
	created_at text NOT NULL,
	updated_at text NOT NULL
, flow_mode text DEFAULT 'classic' NOT NULL);
CREATE TABLE IF NOT EXISTS review_conclusions (
	id text PRIMARY KEY NOT NULL,
	review_chapter_id text NOT NULL,
	source_chapter_id text NOT NULL,
	verdict text NOT NULL,
	findings_json text,
	created_at text NOT NULL,
	FOREIGN KEY (review_chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (source_chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS skill_directory_caches (
	id text PRIMARY KEY NOT NULL,
	root_kind text NOT NULL,
	normalized_root_path text NOT NULL,
	skills_json text NOT NULL,
	signature_json text NOT NULL,
	scanned_at text NOT NULL,
	last_accessed_at text NOT NULL,
	expires_at text NOT NULL
);
CREATE TABLE IF NOT EXISTS terminal_tabs (
	id text PRIMARY KEY NOT NULL,
	chapter_id text,
	narrator_id text,
	name text NOT NULL,
	sort_order integer DEFAULT 0 NOT NULL,
	created_at text NOT NULL,
	FOREIGN KEY (chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS terminal_view_state (
	id text PRIMARY KEY NOT NULL,
	user_id text NOT NULL,
	chapter_id text,
	narrator_id text,
	layout text DEFAULT 'single' NOT NULL,
	active_tab_id text,
	panel_assignments text,
	updated_at text NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS terminals (
	id text PRIMARY KEY NOT NULL,
	chapter_id text,
	narrator_id text,
	name text NOT NULL,
	cwd text,
	dtach_socket text,
	status text DEFAULT 'running' NOT NULL,
	exit_code integer,
	created_at text NOT NULL, graph_opened integer DEFAULT 0 NOT NULL, graph_x real, graph_y real, graph_width real, graph_height real,
	FOREIGN KEY (chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (narrator_id) REFERENCES narrators(id) ON UPDATE no action ON DELETE no action
);
CREATE TABLE IF NOT EXISTS user_favorite_directories (
	id text PRIMARY KEY NOT NULL,
	user_id text NOT NULL,
	path text NOT NULL,
	label text,
	sort_order integer DEFAULT 0 NOT NULL,
	created_at text NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE no action ON DELETE cascade
);
CREATE TABLE IF NOT EXISTS user_preferences (
	id text PRIMARY KEY NOT NULL,
	user_id text NOT NULL,
	auto_load_older_messages integer DEFAULT true NOT NULL,
	language text DEFAULT 'en' NOT NULL,
	word_wrap_markdown integer DEFAULT true NOT NULL,
	word_wrap_code integer DEFAULT true NOT NULL,
	word_wrap_diff integer DEFAULT true NOT NULL,
	reply_in_user_language integer DEFAULT true NOT NULL,
	show_token_usage integer DEFAULT false NOT NULL,
	show_output_stats integer DEFAULT true NOT NULL,
	terminal_theme text DEFAULT 'auto' NOT NULL,
	terminal_font_size integer DEFAULT 14 NOT NULL,
	recent_tabs text DEFAULT '[]' NOT NULL,
	notify_on_done integer DEFAULT true NOT NULL,
	notify_on_waiting integer DEFAULT true NOT NULL,
	notify_pwa_enabled integer DEFAULT false NOT NULL,
	notify_sound_enabled integer DEFAULT true NOT NULL,
	notify_sound_type text DEFAULT 'builtin' NOT NULL,
	notify_sound_builtin text DEFAULT 'gentle' NOT NULL,
	notify_sound_file_id text,
	notify_dingtalk_enabled integer DEFAULT false NOT NULL,
	notify_dingtalk_webhook text DEFAULT '' NOT NULL,
	notify_dingtalk_secret text DEFAULT '' NOT NULL,
	notify_feishu_enabled integer DEFAULT false NOT NULL,
	notify_feishu_webhook text DEFAULT '' NOT NULL,
	notify_feishu_secret text DEFAULT '' NOT NULL,
	commands text DEFAULT '[]' NOT NULL,
	created_at text NOT NULL,
	updated_at text NOT NULL
, setup_wizard_completed integer DEFAULT false NOT NULL, graph_viewports text DEFAULT '{}' NOT NULL, send_mode text DEFAULT 'enter' NOT NULL, gateway_config text DEFAULT '{}' NOT NULL, add_subagent_to_recent_tabs integer DEFAULT true NOT NULL, fast_mode_default integer DEFAULT false NOT NULL);
CREATE TABLE IF NOT EXISTS users (
	id text PRIMARY KEY NOT NULL,
	username text NOT NULL,
	password_hash text NOT NULL,
	role text DEFAULT 'user' NOT NULL,
	avatar_color text,
	avatar_image_id text,
	created_at text NOT NULL
, git_username text, git_email text);
CREATE TABLE IF NOT EXISTS volume_snapshot_applications (
	id text PRIMARY KEY NOT NULL,
	snapshot_id text NOT NULL,
	chapter_id text NOT NULL,
	applied_at text NOT NULL,
	applied_by text,
	FOREIGN KEY (snapshot_id) REFERENCES volume_snapshots(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (applied_by) REFERENCES users(id) ON UPDATE no action ON DELETE set null
);
CREATE TABLE IF NOT EXISTS volume_snapshots (
	id text PRIMARY KEY NOT NULL,
	project_id text NOT NULL,
	name text NOT NULL,
	description text,
	source_chapter_id text,
	service_name text NOT NULL,
	container_path text NOT NULL,
	size_bytes integer,
	created_by text,
	created_at text NOT NULL,
	updated_at text NOT NULL,
	FOREIGN KEY (project_id) REFERENCES projects(id) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (source_chapter_id) REFERENCES chapters(id) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (created_by) REFERENCES users(id) ON UPDATE no action ON DELETE set null
);
CREATE TABLE IF NOT EXISTS workspaces (
	id text PRIMARY KEY NOT NULL,
	user_id text NOT NULL,
	title text NOT NULL,
	tree text NOT NULL,
	created_at integer NOT NULL,
	updated_at integer NOT NULL,
	FOREIGN KEY (user_id) REFERENCES users(id) ON UPDATE no action ON DELETE cascade
);
CREATE INDEX IF NOT EXISTS idx_api_requests_created ON api_requests (created_at);
CREATE INDEX IF NOT EXISTS idx_api_requests_kind ON api_requests (kind,created_at);
CREATE INDEX IF NOT EXISTS idx_api_requests_message ON api_requests (message_id);
CREATE INDEX IF NOT EXISTS idx_api_requests_narrator ON api_requests (narrator_id,created_at);
CREATE INDEX IF NOT EXISTS idx_api_requests_provider ON api_requests (provider,created_at);
CREATE INDEX IF NOT EXISTS idx_bg_tasks_parent ON background_tasks (parent_narrator_id,status);
CREATE INDEX IF NOT EXISTS idx_bg_tasks_subagent ON background_tasks (subagent_narrator_id);
CREATE INDEX IF NOT EXISTS idx_blacklist_cmds_narrator ON narrator_blacklist_cmds (narrator_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_blacklist_cmds_narrator_pattern ON narrator_blacklist_cmds (narrator_id,pattern);
CREATE INDEX IF NOT EXISTS idx_blacklist_dirs_narrator ON narrator_blacklist_dirs (narrator_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_blacklist_dirs_narrator_path ON narrator_blacklist_dirs (narrator_id,path);
CREATE INDEX IF NOT EXISTS idx_chapter_commits_chapter ON chapter_commits (chapter_id,authored_at);
CREATE INDEX IF NOT EXISTS idx_chapter_commits_narrator ON chapter_commits (narrator_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_chapter_commits_sha ON chapter_commits (chapter_id,sha);
CREATE INDEX IF NOT EXISTS idx_chapter_edges_project ON chapter_edges (project_id);
CREATE INDEX IF NOT EXISTS idx_chapter_edges_source ON chapter_edges (source_id);
CREATE INDEX IF NOT EXISTS idx_chapter_edges_src_tgt_type ON chapter_edges (source_id,target_id,type);
CREATE INDEX IF NOT EXISTS idx_chapter_edges_target ON chapter_edges (target_id);
CREATE INDEX IF NOT EXISTS idx_chapters_parent ON chapters (parent_chapter_id);
CREATE INDEX IF NOT EXISTS idx_chapters_project ON chapters (project_id,status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_chapters_project_branch ON chapters (project_id,branch);
CREATE INDEX IF NOT EXISTS idx_chat_group_members_group ON chat_group_members (group_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_group_members_group_narrator ON chat_group_members (group_id,narrator_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_group_members_group_user ON chat_group_members (group_id,user_id);
CREATE INDEX IF NOT EXISTS idx_chat_group_members_narrator ON chat_group_members (narrator_id);
CREATE INDEX IF NOT EXISTS idx_chat_group_messages_group ON chat_group_messages (group_id,created_at);
CREATE INDEX IF NOT EXISTS idx_chat_groups_origin ON chat_groups (origin_narrator_id);
CREATE INDEX IF NOT EXISTS idx_chat_groups_project ON chat_groups (project_id);
CREATE INDEX IF NOT EXISTS idx_chat_groups_status ON chat_groups (status,updated_at);
CREATE INDEX IF NOT EXISTS idx_container_instances_chapter ON container_instances(chapter_id);
CREATE INDEX IF NOT EXISTS idx_container_instances_chapter_service ON container_instances(chapter_id, service_name);
CREATE INDEX IF NOT EXISTS idx_container_instances_container ON container_instances(container_id);
CREATE INDEX IF NOT EXISTS idx_container_instances_status ON container_instances(chapter_id, status);
CREATE INDEX IF NOT EXISTS idx_fav_dirs_user ON user_favorite_directories (user_id,sort_order);
CREATE INDEX IF NOT EXISTS idx_file_attr_narrator ON file_attributions (narrator_id);
CREATE INDEX IF NOT EXISTS idx_file_attr_workspace ON file_attributions (workspace_path,changed_at);
CREATE INDEX IF NOT EXISTS idx_file_attr_workspace_file ON file_attributions (workspace_path,file_path,changed_at);
CREATE INDEX IF NOT EXISTS idx_file_snapshots_narrator ON narrator_file_snapshots (narrator_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_file_snapshots_narrator_file ON narrator_file_snapshots (narrator_id,file_path);
CREATE INDEX IF NOT EXISTS idx_gsm_app_user ON gateway_session_mappings (app_user_id);
CREATE INDEX IF NOT EXISTS idx_gsm_narrator ON gateway_session_mappings (narrator_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_gsm_platform_chat_user ON gateway_session_mappings (platform,chat_id,user_id);
CREATE INDEX IF NOT EXISTS idx_hooks_event ON hooks (event,enabled);
CREATE INDEX IF NOT EXISTS idx_hooks_event_enabled ON hooks(event, enabled);
CREATE INDEX IF NOT EXISTS idx_hooks_project ON hooks (project_id);
CREATE INDEX IF NOT EXISTS idx_messages_narrator ON narrator_messages (narrator_id,created_at);
CREATE INDEX IF NOT EXISTS idx_messages_parent_tool_use ON narrator_messages (narrator_id,parent_tool_use_id);
CREATE INDEX IF NOT EXISTS idx_messages_parent_tool_use_lookup ON narrator_messages (parent_tool_use_id);
CREATE INDEX IF NOT EXISTS idx_messages_toplevel ON narrator_messages (narrator_id,parent_tool_use_id,created_at);
CREATE INDEX IF NOT EXISTS idx_narrator_goals_narrator_order ON narrator_goals (narrator_id,sort_order);
CREATE INDEX IF NOT EXISTS idx_narrator_goals_status ON narrator_goals (narrator_id,status);
CREATE INDEX IF NOT EXISTS idx_narrator_refs_compact_seq ON narrator_message_refs (narrator_id,is_compact,seq);
CREATE INDEX IF NOT EXISTS idx_narrator_refs_message ON narrator_message_refs (message_id);
CREATE INDEX IF NOT EXISTS idx_narrator_refs_segment_compact ON narrator_message_refs (segment_compact_id);
CREATE INDEX IF NOT EXISTS idx_narrator_refs_seq ON narrator_message_refs (narrator_id,seq);
CREATE UNIQUE INDEX IF NOT EXISTS idx_narrator_refs_unique ON narrator_message_refs (narrator_id,message_id);
CREATE INDEX IF NOT EXISTS idx_narrators_chapter ON narrators (chapter_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_narrators_handle ON narrators (handle);
CREATE INDEX IF NOT EXISTS idx_narrators_parent ON narrators (parent_narrator_id);
CREATE INDEX IF NOT EXISTS idx_nbm_narrator_seq ON narrator_buffered_messages (narrator_id,seq);
CREATE INDEX IF NOT EXISTS idx_patches_message ON narrator_patches (message_id);
CREATE INDEX IF NOT EXISTS idx_patches_narrator ON narrator_patches (narrator_id,created_at);
CREATE INDEX IF NOT EXISTS idx_patches_tool_use ON narrator_patches (tool_use_id);
CREATE INDEX IF NOT EXISTS idx_review_conclusions_review ON review_conclusions (review_chapter_id);
CREATE INDEX IF NOT EXISTS idx_review_conclusions_source ON review_conclusions (source_chapter_id);
CREATE INDEX IF NOT EXISTS idx_sidecars_message ON narrator_sidecars (message_id,target,order_index);
CREATE INDEX IF NOT EXISTS idx_sidecars_narrator ON narrator_sidecars (narrator_id,created_at);
CREATE INDEX IF NOT EXISTS idx_sidecars_tool_use ON narrator_sidecars (tool_use_id,target,order_index);
CREATE INDEX IF NOT EXISTS idx_skill_dir_cache_expires ON skill_directory_caches (expires_at);
CREATE INDEX IF NOT EXISTS idx_skill_dir_cache_last_accessed ON skill_directory_caches (last_accessed_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_dir_cache_root ON skill_directory_caches (root_kind,normalized_root_path);
CREATE INDEX IF NOT EXISTS idx_snapshot_applications_applied_by ON volume_snapshot_applications(applied_by);
CREATE INDEX IF NOT EXISTS idx_snapshot_applications_chapter ON volume_snapshot_applications (chapter_id);
CREATE INDEX IF NOT EXISTS idx_snapshot_applications_snapshot ON volume_snapshot_applications (snapshot_id);
CREATE INDEX IF NOT EXISTS idx_task_results_run ON benchmark_task_results (run_id,status);
CREATE INDEX IF NOT EXISTS idx_terminal_tabs_chapter ON terminal_tabs (chapter_id,sort_order);
CREATE INDEX IF NOT EXISTS idx_terminal_tabs_narrator ON terminal_tabs (narrator_id,sort_order);
CREATE INDEX IF NOT EXISTS idx_terminals_chapter ON terminals (chapter_id);
CREATE INDEX IF NOT EXISTS idx_terminals_narrator ON terminals (narrator_id);
CREATE INDEX IF NOT EXISTS idx_terminals_status ON terminals (status);
CREATE INDEX IF NOT EXISTS idx_toolcalls_created ON narrator_tool_calls (narrator_id,created_at);
CREATE INDEX IF NOT EXISTS idx_toolcalls_message ON narrator_tool_calls (message_id);
CREATE INDEX IF NOT EXISTS idx_toolcalls_status ON narrator_tool_calls (narrator_id,status);
CREATE INDEX IF NOT EXISTS idx_toolcalls_status_narrator_created ON narrator_tool_calls (status,narrator_id,created_at);
CREATE INDEX IF NOT EXISTS idx_toolcalls_tool_use_id ON narrator_tool_calls (tool_use_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_view_state_user_chapter ON terminal_view_state (user_id,chapter_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_view_state_user_narrator ON terminal_view_state (user_id,narrator_id);
CREATE INDEX IF NOT EXISTS idx_volume_snapshots_created_by ON volume_snapshots(created_by);
CREATE INDEX IF NOT EXISTS idx_volume_snapshots_project ON volume_snapshots (project_id);
CREATE INDEX IF NOT EXISTS idx_volume_snapshots_project_created ON volume_snapshots(project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_volume_snapshots_project_service ON volume_snapshots(project_id, service_name);
CREATE INDEX IF NOT EXISTS idx_volume_snapshots_source ON volume_snapshots(source_chapter_id);
CREATE INDEX IF NOT EXISTS idx_volume_snapshots_source_chapter ON volume_snapshots (source_chapter_id);
CREATE INDEX IF NOT EXISTS idx_whitelist_cmds_narrator ON narrator_whitelist_cmds (narrator_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_whitelist_cmds_narrator_pattern ON narrator_whitelist_cmds (narrator_id,pattern);
CREATE INDEX IF NOT EXISTS idx_whitelist_dirs_narrator ON narrator_whitelist_dirs (narrator_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_whitelist_dirs_narrator_path ON narrator_whitelist_dirs (narrator_id,path);
CREATE INDEX IF NOT EXISTS idx_workspaces_user ON workspaces (user_id);
CREATE UNIQUE INDEX IF NOT EXISTS user_preferences_user_id_unique ON user_preferences (user_id);
CREATE UNIQUE INDEX IF NOT EXISTS users_username_unique ON users (username);
