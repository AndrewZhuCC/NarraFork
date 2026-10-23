DROP TRIGGER IF EXISTS model_history_v3_message_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_message_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_message_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_tool_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_tool_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_tool_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_ref_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_ref_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_ref_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_block_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_block_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_block_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_tool_phase_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_tool_phase_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_sync_message_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_sync_message_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_sync_tool_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_sync_tool_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_sender;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_narrator;
--> statement-breakpoint
PRAGMA foreign_keys=OFF;
--> statement-breakpoint
DROP TABLE `chapter_commits`;
--> statement-breakpoint
DROP TABLE `chapter_edges`;
--> statement-breakpoint
DROP TABLE `chapters`;
--> statement-breakpoint
DROP TABLE `exploration_groups`;
--> statement-breakpoint
DROP TABLE `merge_sessions`;
--> statement-breakpoint
DROP TABLE `review_conclusions`;
--> statement-breakpoint
CREATE TABLE `__new_container_instances` (
	`id` text PRIMARY KEY NOT NULL,
	`worktree_resource_id` text,
	`container_id` text,
	`service_name` text NOT NULL,
	`status` text DEFAULT 'created' NOT NULL,
	`host_port` integer,
	`container_port` integer,
	`proxy_label` text,
	`container_ip` text,
	`volume_name` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`worktree_resource_id`) REFERENCES `narrator_worktree_resources`(`id`) ON UPDATE no action ON DELETE restrict
);
--> statement-breakpoint
INSERT INTO `__new_container_instances`("id", "worktree_resource_id", "container_id", "service_name", "status", "host_port", "container_port", "proxy_label", "container_ip", "volume_name", "created_at", "updated_at") SELECT "id", "worktree_resource_id", "container_id", "service_name", "status", "host_port", "container_port", "proxy_label", "container_ip", "volume_name", "created_at", "updated_at" FROM `container_instances`;
--> statement-breakpoint
DROP TABLE `container_instances`;
--> statement-breakpoint
ALTER TABLE `__new_container_instances` RENAME TO `container_instances`;
--> statement-breakpoint
CREATE INDEX `idx_container_instances_container` ON `container_instances` (`container_id`);
--> statement-breakpoint
CREATE INDEX `idx_container_instances_resource_service_status` ON `container_instances` (`worktree_resource_id`,`service_name`,`status`);
--> statement-breakpoint
CREATE TABLE `__new_gateway_session_mappings` (
	`id` text PRIMARY KEY NOT NULL,
	`platform` text NOT NULL,
	`chat_id` text NOT NULL,
	`user_id` text NOT NULL,
	`username` text,
	`narrator_id` text NOT NULL,
	`app_user_id` text,
	`project_id` text,
	`last_message_at` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`app_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_gateway_session_mappings`("id", "platform", "chat_id", "user_id", "username", "narrator_id", "app_user_id", "project_id", "last_message_at", "created_at", "updated_at") SELECT "id", "platform", "chat_id", "user_id", "username", "narrator_id", "app_user_id", "project_id", "last_message_at", "created_at", "updated_at" FROM `gateway_session_mappings`;
--> statement-breakpoint
DROP TABLE `gateway_session_mappings`;
--> statement-breakpoint
ALTER TABLE `__new_gateway_session_mappings` RENAME TO `gateway_session_mappings`;
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_gsm_platform_chat_user` ON `gateway_session_mappings` (`platform`,`chat_id`,`user_id`);
--> statement-breakpoint
CREATE INDEX `idx_gsm_narrator` ON `gateway_session_mappings` (`narrator_id`);
--> statement-breakpoint
CREATE INDEX `idx_gsm_app_user` ON `gateway_session_mappings` (`app_user_id`);
--> statement-breakpoint
CREATE INDEX `idx_gsm_project` ON `gateway_session_mappings` (`project_id`);
--> statement-breakpoint
CREATE TABLE `__new_narrators` (
	`id` text PRIMARY KEY NOT NULL,
	`scheduled_task_id` text,
	`api_conversation_id` text,
	`logical_run_id` text,
	`inbox_sequence` integer DEFAULT 0 NOT NULL,
	`next_seq` integer DEFAULT 0 NOT NULL,
	`fork_message_id` text,
	`type` text DEFAULT 'primary' NOT NULL,
	`subagent_type` text,
	`title` text,
	`inherit_mode` text DEFAULT 'fresh' NOT NULL,
	`parent_narrator_id` text,
	`origin_tool_call_id` text,
	`subagent_origin_kind` text,
	`context_summary` text,
	`context_summary_chars` integer DEFAULT 0 NOT NULL,
	`context_system_chars` integer DEFAULT 0 NOT NULL,
	`context_tools_chars` integer DEFAULT 0 NOT NULL,
	`context_char_revision` integer DEFAULT 0 NOT NULL,
	`context_usage_snapshot_json` text,
	`context_char_cache_json` text,
	`model` text DEFAULT 'claude-sonnet-4.5',
	`pending_model_restore` text,
	`system_prompt` text,
	`permission_mode` text DEFAULT 'default',
	`previous_permission_mode` text,
	`plan_file_id` text,
	`reasoning_effort` text,
	`fast_mode` integer DEFAULT false NOT NULL,
	`fast_mode_override` text DEFAULT 'inherit' NOT NULL,
	`relaxed_plan` integer DEFAULT false NOT NULL,
	`plan_reflection_auto_approve_override` text DEFAULT 'inherit' NOT NULL,
	`danger_reflection_override` text DEFAULT 'inherit' NOT NULL,
	`auto_continuation_override` text DEFAULT 'inherit' NOT NULL,
	`behavior_fence_interval_override` integer,
	`tasks_reminder_interval_override` integer,
	`behavior_fence_attach_override` text DEFAULT 'inherit' NOT NULL,
	`message_count` integer DEFAULT 0,
	`total_cost_usd` real DEFAULT 0,
	`last_message_at` text,
	`last_stop_reason` text,
	`status` text DEFAULT 'idle' NOT NULL,
	`substatus` text DEFAULT '[]' NOT NULL,
	`plan_mode` integer DEFAULT false NOT NULL,
	`cwd` text,
	`workspace_revision` integer DEFAULT 0 NOT NULL,
	`workspace_context` text,
	`error_message` text,
	`error_retryable` integer,
	`refs_inherited_from` text,
	`refs_backfill_cursor` integer,
	`enabled_tools` text,
	`variant` text DEFAULT 'primary' NOT NULL,
	`traits` text DEFAULT '[]' NOT NULL,
	`handle` text,
	`handle_fold` text,
	`is_background` integer DEFAULT false NOT NULL,
	`background_status` text,
	`background_result` text,
	`background_completed_at` text,
	`is_ask_in_passing` integer DEFAULT false NOT NULL,
	`turn_started_at` text,
	`message_version` integer DEFAULT 0 NOT NULL,
	`model_history_revision` integer DEFAULT 0 NOT NULL,
	`message_structure_version` integer DEFAULT 0 NOT NULL,
	`default_device_id` text,
	`oauth_owner_grant_id` text,
	`oauth_provision_key` text,
	`context_project_id` text,
	`oauth_policy_snapshot_json` text,
	`avatar_image_id` text,
	`owner_user_id` text,
	`visibility` text DEFAULT 'private' NOT NULL,
	`write_audience` text DEFAULT 'owner' NOT NULL,
	`acl_root_narrator_id` text,
	`runtime_registration_json` text,
	`policy_source_narrator_id` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`scheduled_task_id`) REFERENCES `scheduled_tasks`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`fork_message_id`) REFERENCES `narrator_messages`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`parent_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`refs_inherited_from`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`oauth_owner_grant_id`) REFERENCES `oauth_grants`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`context_project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`owner_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`acl_root_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`policy_source_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_narrators`("id", "scheduled_task_id", "api_conversation_id", "logical_run_id", "inbox_sequence", "next_seq", "fork_message_id", "type", "subagent_type", "title", "inherit_mode", "parent_narrator_id", "origin_tool_call_id", "subagent_origin_kind", "context_summary", "context_summary_chars", "context_system_chars", "context_tools_chars", "context_char_revision", "context_usage_snapshot_json", "context_char_cache_json", "model", "pending_model_restore", "system_prompt", "permission_mode", "previous_permission_mode", "plan_file_id", "reasoning_effort", "fast_mode", "fast_mode_override", "relaxed_plan", "plan_reflection_auto_approve_override", "danger_reflection_override", "auto_continuation_override", "behavior_fence_interval_override", "tasks_reminder_interval_override", "behavior_fence_attach_override", "message_count", "total_cost_usd", "last_message_at", "last_stop_reason", "status", "substatus", "plan_mode", "cwd", "workspace_revision", "workspace_context", "error_message", "error_retryable", "refs_inherited_from", "refs_backfill_cursor", "enabled_tools", "variant", "traits", "handle", "handle_fold", "is_background", "background_status", "background_result", "background_completed_at", "is_ask_in_passing", "turn_started_at", "message_version", "model_history_revision", "message_structure_version", "default_device_id", "oauth_owner_grant_id", "oauth_provision_key", "context_project_id", "oauth_policy_snapshot_json", "avatar_image_id", "owner_user_id", "visibility", "write_audience", "acl_root_narrator_id", "runtime_registration_json", "policy_source_narrator_id", "created_at", "updated_at") SELECT "id", "scheduled_task_id", "api_conversation_id", "logical_run_id", "inbox_sequence", "next_seq", "fork_message_id", "type", "subagent_type", "title", "inherit_mode", "parent_narrator_id", "origin_tool_call_id", "subagent_origin_kind", "context_summary", "context_summary_chars", "context_system_chars", "context_tools_chars", "context_char_revision", "context_usage_snapshot_json", "context_char_cache_json", "model", "pending_model_restore", "system_prompt", "permission_mode", "previous_permission_mode", "plan_file_id", "reasoning_effort", "fast_mode", "fast_mode_override", "relaxed_plan", "plan_reflection_auto_approve_override", "danger_reflection_override", "auto_continuation_override", "behavior_fence_interval_override", "tasks_reminder_interval_override", "behavior_fence_attach_override", "message_count", "total_cost_usd", "last_message_at", "last_stop_reason", "status", "substatus", "plan_mode", "cwd", "workspace_revision", "workspace_context", "error_message", "error_retryable", "refs_inherited_from", "refs_backfill_cursor", "enabled_tools", "variant", "traits", "handle", "handle_fold", "is_background", "background_status", "background_result", "background_completed_at", "is_ask_in_passing", "turn_started_at", "message_version", "model_history_revision", "message_structure_version", "default_device_id", "oauth_owner_grant_id", "oauth_provision_key", "context_project_id", "oauth_policy_snapshot_json", "avatar_image_id", "owner_user_id", "visibility", "write_audience", "acl_root_narrator_id", "runtime_registration_json", "policy_source_narrator_id", "created_at", "updated_at" FROM `narrators`;
--> statement-breakpoint
DROP TABLE `narrators`;
--> statement-breakpoint
ALTER TABLE `__new_narrators` RENAME TO `narrators`;
--> statement-breakpoint
CREATE INDEX `idx_narrators_scheduled_task_created` ON `narrators` (`scheduled_task_id`,`created_at`,`id`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_parent` ON `narrators` (`parent_narrator_id`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_origin_tool_call` ON `narrators` (`origin_tool_call_id`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_owner` ON `narrators` (`owner_user_id`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_visibility` ON `narrators` (`visibility`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_write_audience` ON `narrators` (`write_audience`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_acl_root` ON `narrators` (`acl_root_narrator_id`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_policy_source` ON `narrators` (`policy_source_narrator_id`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_variant_updated` ON `narrators` (`variant`,`updated_at`,`id`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_handle` ON `narrators` (`handle`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_narrators_handle_fold` ON `narrators` (`handle_fold`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_context_project` ON `narrators` (`context_project_id`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_oauth_owner` ON `narrators` (`oauth_owner_grant_id`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_narrators_oauth_provision` ON `narrators` (`oauth_owner_grant_id`,`oauth_provision_key`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_fork_message` ON `narrators` (`fork_message_id`);
--> statement-breakpoint
CREATE INDEX `idx_narrators_refs_inherited_from` ON `narrators` (`refs_inherited_from`) WHERE "refs_inherited_from" IS NOT NULL;
--> statement-breakpoint
CREATE INDEX `idx_narrators_background_parent_created` ON `narrators` (`parent_narrator_id`,`created_at`,`id`) WHERE "is_background" = 1;
--> statement-breakpoint
CREATE TABLE `__new_port_allocations` (
	`port` integer PRIMARY KEY NOT NULL,
	`worktree_resource_id` text,
	`service_name` text,
	`allocated_at` text NOT NULL,
	FOREIGN KEY (`worktree_resource_id`) REFERENCES `narrator_worktree_resources`(`id`) ON UPDATE no action ON DELETE restrict
);
--> statement-breakpoint
INSERT INTO `__new_port_allocations`("port", "worktree_resource_id", "service_name", "allocated_at") SELECT "port", "worktree_resource_id", "service_name", "allocated_at" FROM `port_allocations`;
--> statement-breakpoint
DROP TABLE `port_allocations`;
--> statement-breakpoint
ALTER TABLE `__new_port_allocations` RENAME TO `port_allocations`;
--> statement-breakpoint
CREATE INDEX `idx_port_allocations_resource` ON `port_allocations` (`worktree_resource_id`);
--> statement-breakpoint
CREATE TABLE `__new_scheduled_tasks` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`enabled` integer DEFAULT true NOT NULL,
	`cron_expr` text NOT NULL,
	`timezone` text,
	`prompt` text NOT NULL,
	`system_prompt` text,
	`model` text,
	`permission_mode` text DEFAULT 'bypassPermissions' NOT NULL,
	`locale` text DEFAULT 'en' NOT NULL,
	`run_context` text DEFAULT 'standalone' NOT NULL,
	`cwd` text,
	`project_id` text,
	`narrator_mode` text DEFAULT 'new' NOT NULL,
	`cleanup_policy` text DEFAULT '{"mode":"none"}' NOT NULL,
	`reuse_narrator_id` text,
	`created_by` text,
	`last_run_at` text,
	`next_run_at` text,
	`last_narrator_id` text,
	`last_status` text,
	`last_error` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`reuse_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`created_by`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_scheduled_tasks`("id", "name", "enabled", "cron_expr", "timezone", "prompt", "system_prompt", "model", "permission_mode", "locale", "run_context", "cwd", "project_id", "narrator_mode", "cleanup_policy", "reuse_narrator_id", "created_by", "last_run_at", "next_run_at", "last_narrator_id", "last_status", "last_error", "created_at", "updated_at") SELECT "id", "name", "enabled", "cron_expr", "timezone", "prompt", "system_prompt", "model", "permission_mode", "locale", "run_context", "cwd", "project_id", "narrator_mode", "cleanup_policy", "reuse_narrator_id", "created_by", "last_run_at", "next_run_at", "last_narrator_id", "last_status", "last_error", "created_at", "updated_at" FROM `scheduled_tasks`;
--> statement-breakpoint
DROP TABLE `scheduled_tasks`;
--> statement-breakpoint
ALTER TABLE `__new_scheduled_tasks` RENAME TO `scheduled_tasks`;
--> statement-breakpoint
CREATE INDEX `idx_scheduled_tasks_enabled` ON `scheduled_tasks` (`enabled`);
--> statement-breakpoint
CREATE INDEX `idx_scheduled_tasks_next_run` ON `scheduled_tasks` (`enabled`,`next_run_at`);
--> statement-breakpoint
CREATE INDEX `idx_scheduled_tasks_project` ON `scheduled_tasks` (`project_id`);
--> statement-breakpoint
CREATE INDEX `idx_scheduled_tasks_created_by` ON `scheduled_tasks` (`created_by`);
--> statement-breakpoint
CREATE INDEX `idx_scheduled_tasks_reuse_narrator` ON `scheduled_tasks` (`reuse_narrator_id`);
--> statement-breakpoint
CREATE INDEX `idx_scheduled_tasks_last_narrator` ON `scheduled_tasks` (`last_narrator_id`);
--> statement-breakpoint
CREATE TABLE `__new_terminal_view_state` (
	`id` text PRIMARY KEY NOT NULL,
	`user_id` text NOT NULL,
	`narrator_id` text,
	`worktree_resource_id` text,
	`layout` text DEFAULT 'single' NOT NULL,
	`active_tab_id` text,
	`panel_assignments` text,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`worktree_resource_id`) REFERENCES `narrator_worktree_resources`(`id`) ON UPDATE no action ON DELETE restrict,
	CONSTRAINT "ck_view_state_resource_owner" CHECK("__new_terminal_view_state"."worktree_resource_id" is null or "__new_terminal_view_state"."narrator_id" is null)
);
--> statement-breakpoint
INSERT INTO `__new_terminal_view_state`("id", "user_id", "narrator_id", "worktree_resource_id", "layout", "active_tab_id", "panel_assignments", "updated_at") SELECT "id", "user_id", "narrator_id", "worktree_resource_id", "layout", "active_tab_id", "panel_assignments", "updated_at" FROM `terminal_view_state`;
--> statement-breakpoint
DROP TABLE `terminal_view_state`;
--> statement-breakpoint
ALTER TABLE `__new_terminal_view_state` RENAME TO `terminal_view_state`;
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_view_state_user_narrator` ON `terminal_view_state` (`user_id`,`narrator_id`);
--> statement-breakpoint
CREATE INDEX `idx_view_state_narrator` ON `terminal_view_state` (`narrator_id`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_view_state_user_resource` ON `terminal_view_state` (`user_id`,`worktree_resource_id`);
--> statement-breakpoint
CREATE INDEX `idx_view_state_resource` ON `terminal_view_state` (`worktree_resource_id`);
--> statement-breakpoint
CREATE TABLE `__new_terminals` (
	`id` text PRIMARY KEY NOT NULL,
	`narrator_id` text,
	`worktree_resource_id` text,
	`name` text NOT NULL,
	`cwd` text,
	`dtach_socket` text,
	`device_id` text,
	`status` text DEFAULT 'running' NOT NULL,
	`exit_code` integer,
	`created_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`worktree_resource_id`) REFERENCES `narrator_worktree_resources`(`id`) ON UPDATE no action ON DELETE restrict,
	CONSTRAINT "ck_terminals_resource_owner" CHECK("__new_terminals"."worktree_resource_id" is null or "__new_terminals"."narrator_id" is null)
);
--> statement-breakpoint
INSERT INTO `__new_terminals`("id", "narrator_id", "worktree_resource_id", "name", "cwd", "dtach_socket", "device_id", "status", "exit_code", "created_at") SELECT "id", "narrator_id", "worktree_resource_id", "name", "cwd", "dtach_socket", "device_id", "status", "exit_code", "created_at" FROM `terminals`;
--> statement-breakpoint
DROP TABLE `terminals`;
--> statement-breakpoint
ALTER TABLE `__new_terminals` RENAME TO `terminals`;
--> statement-breakpoint
CREATE INDEX `idx_terminals_narrator` ON `terminals` (`narrator_id`);
--> statement-breakpoint
CREATE INDEX `idx_terminals_status` ON `terminals` (`status`);
--> statement-breakpoint
CREATE INDEX `idx_terminals_resource_status` ON `terminals` (`worktree_resource_id`,`status`,`id`);
--> statement-breakpoint
CREATE TABLE `__new_volume_snapshot_applications` (
	`id` text PRIMARY KEY NOT NULL,
	`snapshot_id` text NOT NULL,
	`target_worktree_resource_id` text,
	`applied_at` text NOT NULL,
	`applied_by` text,
	FOREIGN KEY (`snapshot_id`) REFERENCES `volume_snapshots`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`target_worktree_resource_id`) REFERENCES `narrator_worktree_resources`(`id`) ON UPDATE no action ON DELETE restrict,
	FOREIGN KEY (`applied_by`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_volume_snapshot_applications`("id", "snapshot_id", "target_worktree_resource_id", "applied_at", "applied_by") SELECT "id", "snapshot_id", "target_worktree_resource_id", "applied_at", "applied_by" FROM `volume_snapshot_applications`;
--> statement-breakpoint
DROP TABLE `volume_snapshot_applications`;
--> statement-breakpoint
ALTER TABLE `__new_volume_snapshot_applications` RENAME TO `volume_snapshot_applications`;
--> statement-breakpoint
CREATE INDEX `idx_snapshot_applications_snapshot` ON `volume_snapshot_applications` (`snapshot_id`);
--> statement-breakpoint
CREATE INDEX `idx_snapshot_applications_target_resource` ON `volume_snapshot_applications` (`target_worktree_resource_id`);
--> statement-breakpoint
CREATE TABLE `__new_volume_snapshots` (
	`id` text PRIMARY KEY NOT NULL,
	`project_id` text NOT NULL,
	`name` text NOT NULL,
	`description` text,
	`source_worktree_resource_id` text,
	`service_name` text NOT NULL,
	`container_path` text NOT NULL,
	`size_bytes` integer,
	`created_by` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`source_worktree_resource_id`) REFERENCES `narrator_worktree_resources`(`id`) ON UPDATE no action ON DELETE restrict,
	FOREIGN KEY (`created_by`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_volume_snapshots`("id", "project_id", "name", "description", "source_worktree_resource_id", "service_name", "container_path", "size_bytes", "created_by", "created_at", "updated_at") SELECT "id", "project_id", "name", "description", "source_worktree_resource_id", "service_name", "container_path", "size_bytes", "created_by", "created_at", "updated_at" FROM `volume_snapshots`;
--> statement-breakpoint
DROP TABLE `volume_snapshots`;
--> statement-breakpoint
ALTER TABLE `__new_volume_snapshots` RENAME TO `volume_snapshots`;
--> statement-breakpoint
CREATE INDEX `idx_volume_snapshots_project` ON `volume_snapshots` (`project_id`);
--> statement-breakpoint
CREATE INDEX `idx_volume_snapshots_source_resource` ON `volume_snapshots` (`source_worktree_resource_id`);
--> statement-breakpoint
ALTER TABLE `notifications` DROP COLUMN `chapter_id`;
--> statement-breakpoint
ALTER TABLE `projects` DROP COLUMN `flow_mode`;
--> statement-breakpoint
ALTER TABLE `projects` DROP COLUMN `startup_script`;
--> statement-breakpoint
ALTER TABLE `projects` DROP COLUMN `copy_files`;
--> statement-breakpoint
ALTER TABLE `projects` DROP COLUMN `proxy_domain`;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_message_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_message_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_message_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_tool_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_tool_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_tool_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_ref_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_ref_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_ref_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_block_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_block_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_block_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_tool_phase_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_tool_phase_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_sync_message_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_sync_message_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_sync_tool_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_sync_tool_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_sender;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v3_narrator;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_message_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_message_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_message_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_tool_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_tool_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_tool_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_ref_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_ref_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_ref_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_sender;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_narrator;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_message_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_message_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_message_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_tool_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_tool_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_tool_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_ref_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_ref_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_ref_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_block_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_block_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_block_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_tool_phase_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_tool_phase_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_sync_message_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_sync_message_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_sync_tool_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_sync_tool_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_sender;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_narrator;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_message_insert AFTER INSERT ON narrator_messages BEGIN
UPDATE narrator_messages SET body_revision = NEW.body_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_tool_insert AFTER INSERT ON narrator_tool_calls BEGIN
UPDATE narrator_tool_calls SET model_revision = NEW.model_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_ref_insert AFTER INSERT ON narrator_message_refs BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (NEW.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_block_insert AFTER INSERT ON narrator_blocks WHEN NEW.phase <> 'open' AND EXISTS (SELECT 1 FROM narrator_messages WHERE id = NEW.message_id AND body_format = 2) BEGIN
UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (NEW.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = NEW.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_message_update AFTER UPDATE ON narrator_messages WHEN (OLD.id IS NOT NEW.id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.sdk_message_uuid IS NOT NEW.sdk_message_uuid OR OLD.parent_tool_use_id IS NOT NEW.parent_tool_use_id OR OLD.role IS NOT NEW.role OR OLD.created_by IS NOT NEW.created_by OR OLD.origin IS NOT NEW.origin OR OLD.origin_label IS NOT NEW.origin_label OR OLD.author_narrator_id IS NOT NEW.author_narrator_id) OR ((OLD.content_json IS NOT NEW.content_json OR OLD.content_text IS NOT NEW.content_text) AND NEW.state_revision = OLD.state_revision) BEGIN
UPDATE narrator_messages SET body_revision = OLD.body_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id, NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_tool_update AFTER UPDATE ON narrator_tool_calls WHEN OLD.id IS NOT NEW.id OR OLD.message_id IS NOT NEW.message_id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.tool_use_id IS NOT NEW.tool_use_id OR OLD.tool_name IS NOT NEW.tool_name OR OLD.input_json IS NOT NEW.input_json OR OLD.output_json IS NOT NEW.output_json OR OLD.status IS NOT NEW.status OR OLD.execution_attempt IS NOT NEW.execution_attempt OR OLD.created_at IS NOT NEW.created_at BEGIN
UPDATE narrator_tool_calls SET model_revision = OLD.model_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id, NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_ref_update AFTER UPDATE ON narrator_message_refs WHEN OLD.id IS NOT NEW.id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.message_id IS NOT NEW.message_id OR OLD.seq IS NOT NEW.seq OR OLD.is_compact IS NOT NEW.is_compact OR OLD.segment_compact_id IS NOT NEW.segment_compact_id BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id, NEW.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_block_update AFTER UPDATE OF payload, model_text, provider_meta, kind, subtype, ordinal, phase, message_id ON narrator_blocks WHEN (OLD.payload IS NOT NEW.payload OR OLD.model_text IS NOT NEW.model_text OR OLD.provider_meta IS NOT NEW.provider_meta OR OLD.kind IS NOT NEW.kind OR OLD.subtype IS NOT NEW.subtype OR OLD.ordinal IS NOT NEW.ordinal OR OLD.phase IS NOT NEW.phase OR OLD.message_id IS NOT NEW.message_id) AND NOT (OLD.phase = 'open' AND NEW.phase = 'open') AND (EXISTS (SELECT 1 FROM narrator_messages WHERE id = OLD.message_id AND body_format = 2) OR EXISTS (SELECT 1 FROM narrator_messages WHERE id = NEW.message_id AND body_format = 2)) BEGIN
UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (OLD.message_id, NEW.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = OLD.message_id), (SELECT narrator_id FROM narrator_messages WHERE id = NEW.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id, NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_message_delete BEFORE DELETE ON narrator_messages BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_tool_delete BEFORE DELETE ON narrator_tool_calls BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_ref_delete AFTER DELETE ON narrator_message_refs BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_block_delete BEFORE DELETE ON narrator_blocks WHEN OLD.phase <> 'open' AND EXISTS (SELECT 1 FROM narrator_messages WHERE id = OLD.message_id AND body_format = 2) BEGIN
UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (OLD.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = OLD.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_tool_phase_insert AFTER INSERT ON narrator_tool_calls WHEN NEW.block_id IS NOT NULL AND NEW.is_internal = 0 AND NEW.execution_attempt >= (SELECT max(execution_attempt) FROM narrator_tool_calls WHERE block_id = NEW.block_id) BEGIN
UPDATE narrator_blocks SET phase = 'open' WHERE id = NEW.block_id AND phase <> 'open';
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_tool_phase_update AFTER UPDATE OF status, output_json ON narrator_tool_calls WHEN NEW.block_id IS NOT NULL AND NEW.is_internal = 0 AND NEW.execution_attempt >= (SELECT max(execution_attempt) FROM narrator_tool_calls WHERE block_id = NEW.block_id) BEGIN
UPDATE narrator_blocks SET phase = CASE WHEN NEW.status IN ('success', 'fail') AND (CASE WHEN json_valid(NEW.output_json) THEN json_extract(NEW.output_json, '$._broken') END) = 1 THEN 'failed' WHEN NEW.status IN ('success', 'fail') THEN 'done' ELSE 'open' END WHERE id = NEW.block_id AND phase <> CASE WHEN NEW.status IN ('success', 'fail') AND (CASE WHEN json_valid(NEW.output_json) THEN json_extract(NEW.output_json, '$._broken') END) = 1 THEN 'failed' WHEN NEW.status IN ('success', 'fail') THEN 'done' ELSE 'open' END;
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_sync_message_insert AFTER INSERT ON narrator_messages WHEN NEW.body_format = 2 BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_sync_message_update AFTER UPDATE OF content_json, content_text, role, origin, body_format ON narrator_messages WHEN NEW.body_format = 2 AND NEW.state_revision = OLD.state_revision BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_sync_tool_insert AFTER INSERT ON narrator_tool_calls WHEN (SELECT body_format FROM narrator_messages WHERE id = NEW.message_id) = 2 BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.message_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_sync_tool_update AFTER UPDATE OF input_json, output_json, status, execution_attempt ON narrator_tool_calls WHEN (SELECT body_format FROM narrator_messages WHERE id = NEW.message_id) = 2 BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.message_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_sender AFTER UPDATE ON users WHEN OLD.username IS NOT NEW.username BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.created_by IN (OLD.id, NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_narrator AFTER UPDATE ON narrators WHEN OLD.title IS NOT NEW.title OR OLD.context_summary IS NOT NEW.context_summary BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = NEW.id OR id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.narrator_id = NEW.id OR m.author_narrator_id = NEW.id);
END;
--> statement-breakpoint
INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', 'model-history-v3') ON CONFLICT (id) DO UPDATE SET installed_version = excluded.installed_version;
