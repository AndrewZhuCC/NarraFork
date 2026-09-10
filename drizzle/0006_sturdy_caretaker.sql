PRAGMA foreign_keys=OFF;--> statement-breakpoint
CREATE TABLE `__new_chapters` (
	`id` text PRIMARY KEY NOT NULL,
	`project_id` text NOT NULL,
	`title` text NOT NULL,
	`description` text,
	`status` text DEFAULT 'active' NOT NULL,
	`role` text DEFAULT 'branch' NOT NULL,
	`branch` text NOT NULL,
	`worktree_path` text,
	`base_branch` text NOT NULL,
	`parent_chapter_id` text,
	`fork_point` text,
	`merged_into_chapter_id` text,
	`merge_commit_sha` text,
	`merge_strategy` text,
	`pre_merge_target_sha` text,
	`merge_snapshot_commit_sha` text,
	`pre_merge_target_snapshot_sha` text,
	`merged_source_snapshot_sha` text,
	`container_config` text,
	`exploration_group_id` text,
	`is_root` integer DEFAULT 0,
	`head_commit_sha` text,
	`start_commit_sha` text,
	`commit_count` integer DEFAULT 0,
	`snapshot_commit_sha` text,
	`snapshot_shadow_key` text,
	`dormant_snapshot_commit_sha` text,
	`parked_snapshot_commit_sha` text,
	`parked_snapshot_base_tree` text,
	`color` text,
	`group_label` text,
	`pinned` integer DEFAULT 0,
	`anchor_commit_sha` text,
	`axis_offset` real DEFAULT 0,
	`cross_offset` real DEFAULT 0,
	`graph_x` real,
	`graph_y` real,
	`panel_expanded` integer DEFAULT 0,
	`panel_width` real,
	`panel_height` real,
	`dock_layout_json` text,
	`detached_panels_json` text,
	`review_source_chapter_id` text,
	`review_status` text,
	`last_accessed_at` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`parent_chapter_id`) REFERENCES `chapters`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`merged_into_chapter_id`) REFERENCES `chapters`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`exploration_group_id`) REFERENCES `exploration_groups`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`review_source_chapter_id`) REFERENCES `chapters`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_chapters`("id", "project_id", "title", "description", "status", "role", "branch", "worktree_path", "base_branch", "parent_chapter_id", "fork_point", "merged_into_chapter_id", "merge_commit_sha", "merge_strategy", "pre_merge_target_sha", "merge_snapshot_commit_sha", "pre_merge_target_snapshot_sha", "merged_source_snapshot_sha", "container_config", "exploration_group_id", "is_root", "head_commit_sha", "start_commit_sha", "commit_count", "snapshot_commit_sha", "snapshot_shadow_key", "dormant_snapshot_commit_sha", "parked_snapshot_commit_sha", "parked_snapshot_base_tree", "color", "group_label", "pinned", "anchor_commit_sha", "axis_offset", "cross_offset", "graph_x", "graph_y", "panel_expanded", "panel_width", "panel_height", "dock_layout_json", "detached_panels_json", "review_source_chapter_id", "review_status", "last_accessed_at", "created_at", "updated_at") SELECT "id", "project_id", "title", "description", "status", "role", "branch", "worktree_path", "base_branch", "parent_chapter_id", "fork_point", "merged_into_chapter_id", "merge_commit_sha", "merge_strategy", "pre_merge_target_sha", "merge_snapshot_commit_sha", "pre_merge_target_snapshot_sha", "merged_source_snapshot_sha", "container_config", "exploration_group_id", "is_root", "head_commit_sha", "start_commit_sha", "commit_count", "snapshot_commit_sha", "snapshot_shadow_key", "dormant_snapshot_commit_sha", "parked_snapshot_commit_sha", "parked_snapshot_base_tree", "color", "group_label", "pinned", "anchor_commit_sha", "axis_offset", "cross_offset", "graph_x", "graph_y", "panel_expanded", "panel_width", "panel_height", "dock_layout_json", "detached_panels_json", "review_source_chapter_id", "review_status", "last_accessed_at", "created_at", "updated_at" FROM `chapters`;--> statement-breakpoint
DROP TABLE `chapters`;--> statement-breakpoint
ALTER TABLE `__new_chapters` RENAME TO `chapters`;--> statement-breakpoint
PRAGMA foreign_keys=ON;--> statement-breakpoint
CREATE INDEX `idx_chapters_project` ON `chapters` (`project_id`,`status`);--> statement-breakpoint
CREATE INDEX `idx_chapters_parent` ON `chapters` (`parent_chapter_id`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_chapters_project_branch` ON `chapters` (`project_id`,`branch`);--> statement-breakpoint
CREATE INDEX `idx_chapters_merged_into` ON `chapters` (`merged_into_chapter_id`);--> statement-breakpoint
CREATE INDEX `idx_chapters_review_source` ON `chapters` (`review_source_chapter_id`);--> statement-breakpoint
CREATE INDEX `idx_chapters_exploration_group` ON `chapters` (`exploration_group_id`);--> statement-breakpoint
CREATE INDEX `idx_chapters_snapshot_shadow_key` ON `chapters` (`snapshot_shadow_key`);--> statement-breakpoint
CREATE INDEX `idx_chapters_worktree_path` ON `chapters` (`worktree_path`);--> statement-breakpoint
CREATE TABLE `__new_container_instances` (
	`id` text PRIMARY KEY NOT NULL,
	`chapter_id` text NOT NULL,
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
	FOREIGN KEY (`chapter_id`) REFERENCES `chapters`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
INSERT INTO `__new_container_instances`("id", "chapter_id", "container_id", "service_name", "status", "host_port", "container_port", "proxy_label", "container_ip", "volume_name", "created_at", "updated_at") SELECT "id", "chapter_id", "container_id", "service_name", "status", "host_port", "container_port", "proxy_label", "container_ip", "volume_name", "created_at", "updated_at" FROM `container_instances`;--> statement-breakpoint
DROP TABLE `container_instances`;--> statement-breakpoint
ALTER TABLE `__new_container_instances` RENAME TO `container_instances`;--> statement-breakpoint
CREATE INDEX `idx_container_instances_chapter` ON `container_instances` (`chapter_id`);--> statement-breakpoint
CREATE INDEX `idx_container_instances_chapter_service` ON `container_instances` (`chapter_id`,`service_name`);--> statement-breakpoint
CREATE INDEX `idx_container_instances_container` ON `container_instances` (`container_id`);--> statement-breakpoint
CREATE INDEX `idx_container_instances_status` ON `container_instances` (`chapter_id`,`status`);--> statement-breakpoint
CREATE TABLE `__new_gateway_session_mappings` (
	`id` text PRIMARY KEY NOT NULL,
	`platform` text NOT NULL,
	`chat_id` text NOT NULL,
	`user_id` text NOT NULL,
	`username` text,
	`narrator_id` text NOT NULL,
	`app_user_id` text,
	`project_id` text,
	`chapter_id` text,
	`last_message_at` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`app_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`chapter_id`) REFERENCES `chapters`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_gateway_session_mappings`("id", "platform", "chat_id", "user_id", "username", "narrator_id", "app_user_id", "project_id", "chapter_id", "last_message_at", "created_at", "updated_at") SELECT "id", "platform", "chat_id", "user_id", "username", "narrator_id", "app_user_id", "project_id", "chapter_id", "last_message_at", "created_at", "updated_at" FROM `gateway_session_mappings`;--> statement-breakpoint
DROP TABLE `gateway_session_mappings`;--> statement-breakpoint
ALTER TABLE `__new_gateway_session_mappings` RENAME TO `gateway_session_mappings`;--> statement-breakpoint
CREATE UNIQUE INDEX `idx_gsm_platform_chat_user` ON `gateway_session_mappings` (`platform`,`chat_id`,`user_id`);--> statement-breakpoint
CREATE INDEX `idx_gsm_narrator` ON `gateway_session_mappings` (`narrator_id`);--> statement-breakpoint
CREATE INDEX `idx_gsm_app_user` ON `gateway_session_mappings` (`app_user_id`);--> statement-breakpoint
CREATE INDEX `idx_gsm_chapter` ON `gateway_session_mappings` (`chapter_id`);--> statement-breakpoint
CREATE INDEX `idx_gsm_project` ON `gateway_session_mappings` (`project_id`);--> statement-breakpoint
CREATE TABLE `__new_merge_sessions` (
	`id` text PRIMARY KEY NOT NULL,
	`target_chapter_id` text NOT NULL,
	`source_chapter_ids` text NOT NULL,
	`strategy` text DEFAULT 'merge' NOT NULL,
	`status` text NOT NULL,
	`current_index` integer DEFAULT 0 NOT NULL,
	`merged_count` integer DEFAULT 0 NOT NULL,
	`current_source_chapter_id` text,
	`conflict_files` text,
	`pre_merge_tree` text,
	`conflict_tree` text,
	`pre_merge_target_snapshot` text,
	`merge_source_snapshot` text,
	`pre_merge_target_sha` text,
	`error` text,
	`locale` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`target_chapter_id`) REFERENCES `chapters`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
INSERT INTO `__new_merge_sessions`("id", "target_chapter_id", "source_chapter_ids", "strategy", "status", "current_index", "merged_count", "current_source_chapter_id", "conflict_files", "pre_merge_tree", "conflict_tree", "pre_merge_target_snapshot", "merge_source_snapshot", "pre_merge_target_sha", "error", "locale", "created_at", "updated_at") SELECT "id", "target_chapter_id", "source_chapter_ids", "strategy", "status", "current_index", "merged_count", "current_source_chapter_id", "conflict_files", "pre_merge_tree", "conflict_tree", "pre_merge_target_snapshot", "merge_source_snapshot", "pre_merge_target_sha", "error", "locale", "created_at", "updated_at" FROM `merge_sessions`;--> statement-breakpoint
DROP TABLE `merge_sessions`;--> statement-breakpoint
ALTER TABLE `__new_merge_sessions` RENAME TO `merge_sessions`;--> statement-breakpoint
CREATE INDEX `idx_merge_sessions_target_chapter` ON `merge_sessions` (`target_chapter_id`);--> statement-breakpoint
CREATE TABLE `__new_narrators` (
	`id` text PRIMARY KEY NOT NULL,
	`chapter_id` text,
	`api_conversation_id` text,
	`fork_message_id` text,
	`type` text DEFAULT 'primary' NOT NULL,
	`subagent_type` text,
	`title` text,
	`inherit_mode` text DEFAULT 'fresh' NOT NULL,
	`parent_narrator_id` text,
	`context_summary` text,
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
	`status` text DEFAULT 'idle' NOT NULL,
	`substatus` text DEFAULT '[]' NOT NULL,
	`plan_mode` integer DEFAULT false NOT NULL,
	`cwd` text,
	`error_message` text,
	`error_retryable` integer,
	`prune_boundary_message_id` text,
	`pruned_percent` integer,
	`prune_enabled` integer DEFAULT true NOT NULL,
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
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`chapter_id`) REFERENCES `chapters`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`fork_message_id`) REFERENCES `narrator_messages`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`parent_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`prune_boundary_message_id`) REFERENCES `narrator_messages`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`refs_inherited_from`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`oauth_owner_grant_id`) REFERENCES `oauth_grants`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`context_project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`owner_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`acl_root_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_narrators`("id", "chapter_id", "api_conversation_id", "fork_message_id", "type", "subagent_type", "title", "inherit_mode", "parent_narrator_id", "context_summary", "model", "pending_model_restore", "system_prompt", "permission_mode", "previous_permission_mode", "plan_file_id", "reasoning_effort", "fast_mode", "fast_mode_override", "relaxed_plan", "plan_reflection_auto_approve_override", "danger_reflection_override", "auto_continuation_override", "behavior_fence_interval_override", "tasks_reminder_interval_override", "behavior_fence_attach_override", "message_count", "total_cost_usd", "last_message_at", "status", "substatus", "plan_mode", "cwd", "error_message", "error_retryable", "prune_boundary_message_id", "pruned_percent", "prune_enabled", "refs_inherited_from", "refs_backfill_cursor", "enabled_tools", "variant", "traits", "handle", "handle_fold", "is_background", "background_status", "background_result", "background_completed_at", "is_ask_in_passing", "turn_started_at", "message_version", "message_structure_version", "default_device_id", "oauth_owner_grant_id", "oauth_provision_key", "context_project_id", "oauth_policy_snapshot_json", "avatar_image_id", "owner_user_id", "visibility", "write_audience", "acl_root_narrator_id", "created_at", "updated_at") SELECT "id", "chapter_id", "api_conversation_id", "fork_message_id", "type", "subagent_type", "title", "inherit_mode", "parent_narrator_id", "context_summary", "model", "pending_model_restore", "system_prompt", "permission_mode", "previous_permission_mode", "plan_file_id", "reasoning_effort", "fast_mode", "fast_mode_override", "relaxed_plan", "plan_reflection_auto_approve_override", "danger_reflection_override", "auto_continuation_override", "behavior_fence_interval_override", "tasks_reminder_interval_override", "behavior_fence_attach_override", "message_count", "total_cost_usd", "last_message_at", "status", "substatus", "plan_mode", "cwd", "error_message", "error_retryable", "prune_boundary_message_id", "pruned_percent", "prune_enabled", "refs_inherited_from", "refs_backfill_cursor", "enabled_tools", "variant", "traits", "handle", "handle_fold", "is_background", "background_status", "background_result", "background_completed_at", "is_ask_in_passing", "turn_started_at", "message_version", "message_structure_version", "default_device_id", "oauth_owner_grant_id", "oauth_provision_key", "context_project_id", "oauth_policy_snapshot_json", "avatar_image_id", "owner_user_id", "visibility", "write_audience", "acl_root_narrator_id", "created_at", "updated_at" FROM `narrators`;--> statement-breakpoint
DROP TABLE `narrators`;--> statement-breakpoint
ALTER TABLE `__new_narrators` RENAME TO `narrators`;--> statement-breakpoint
CREATE INDEX `idx_narrators_chapter` ON `narrators` (`chapter_id`);--> statement-breakpoint
CREATE INDEX `idx_narrators_parent` ON `narrators` (`parent_narrator_id`);--> statement-breakpoint
CREATE INDEX `idx_narrators_owner` ON `narrators` (`owner_user_id`);--> statement-breakpoint
CREATE INDEX `idx_narrators_visibility` ON `narrators` (`visibility`);--> statement-breakpoint
CREATE INDEX `idx_narrators_write_audience` ON `narrators` (`write_audience`);--> statement-breakpoint
CREATE INDEX `idx_narrators_acl_root` ON `narrators` (`acl_root_narrator_id`);--> statement-breakpoint
CREATE INDEX `idx_narrators_variant_updated` ON `narrators` (`variant`,`updated_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_narrators_handle` ON `narrators` (`handle`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_narrators_handle_fold` ON `narrators` (`handle_fold`);--> statement-breakpoint
CREATE INDEX `idx_narrators_context_project` ON `narrators` (`context_project_id`);--> statement-breakpoint
CREATE INDEX `idx_narrators_oauth_owner` ON `narrators` (`oauth_owner_grant_id`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_narrators_oauth_provision` ON `narrators` (`oauth_owner_grant_id`,`oauth_provision_key`);--> statement-breakpoint
CREATE INDEX `idx_narrators_fork_message` ON `narrators` (`fork_message_id`);--> statement-breakpoint
CREATE INDEX `idx_narrators_prune_boundary_message` ON `narrators` (`prune_boundary_message_id`);--> statement-breakpoint
CREATE INDEX `idx_narrators_refs_inherited_from` ON `narrators` (`refs_inherited_from`) WHERE "refs_inherited_from" IS NOT NULL;--> statement-breakpoint
CREATE INDEX `idx_narrators_background_parent_created` ON `narrators` (`parent_narrator_id`,`created_at`,`id`) WHERE "is_background" = 1;--> statement-breakpoint
CREATE TABLE `__new_port_allocations` (
	`port` integer PRIMARY KEY NOT NULL,
	`chapter_id` text,
	`service_name` text,
	`allocated_at` text NOT NULL,
	FOREIGN KEY (`chapter_id`) REFERENCES `chapters`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
INSERT INTO `__new_port_allocations`("port", "chapter_id", "service_name", "allocated_at") SELECT "port", "chapter_id", "service_name", "allocated_at" FROM `port_allocations`;--> statement-breakpoint
DROP TABLE `port_allocations`;--> statement-breakpoint
ALTER TABLE `__new_port_allocations` RENAME TO `port_allocations`;--> statement-breakpoint
CREATE INDEX `idx_port_allocations_chapter` ON `port_allocations` (`chapter_id`);--> statement-breakpoint
CREATE TABLE `__new_projects` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`description` text,
	`status` text DEFAULT 'active' NOT NULL,
	`flow_mode` text DEFAULT 'classic' NOT NULL,
	`git_path` text,
	`remote_url` text,
	`default_branch` text DEFAULT 'main',
	`startup_script` text,
	`copy_files` text,
	`chapter_settings` text,
	`proxy_domain` text,
	`traits` text DEFAULT '[]' NOT NULL,
	`owner_user_id` text,
	`visibility` text DEFAULT 'private' NOT NULL,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`owner_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_projects`("id", "name", "description", "status", "flow_mode", "git_path", "remote_url", "default_branch", "startup_script", "copy_files", "chapter_settings", "proxy_domain", "traits", "owner_user_id", "visibility", "created_at", "updated_at") SELECT "id", "name", "description", "status", "flow_mode", "git_path", "remote_url", "default_branch", "startup_script", "copy_files", "chapter_settings", "proxy_domain", "traits", "owner_user_id", "visibility", "created_at", "updated_at" FROM `projects`;--> statement-breakpoint
DROP TABLE `projects`;--> statement-breakpoint
ALTER TABLE `__new_projects` RENAME TO `projects`;--> statement-breakpoint
CREATE TABLE `__new_remote_devices` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`slug` text NOT NULL,
	`description` text,
	`token_hash` text NOT NULL,
	`token_prefix` text NOT NULL,
	`connection_mode` text DEFAULT 'reverse' NOT NULL,
	`direct_url` text,
	`status` text DEFAULT 'offline' NOT NULL,
	`last_seen_at` text,
	`platform_os` text,
	`platform_arch` text,
	`shell_path` text,
	`default_cwd` text,
	`agent_version` text,
	`capabilities_json` text,
	`path_rules_json` text,
	`reported_path_rules_json` text,
	`owner_scope` text DEFAULT 'shared' NOT NULL,
	`scope` text DEFAULT 'global' NOT NULL,
	`project_id` text,
	`created_by` text NOT NULL,
	`oauth_owner_grant_id` text,
	`oauth_provision_key` text,
	`enrolled_at` text,
	`enrolled_from_ip` text,
	`enrolled_user_agent` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	`revoked_at` text,
	FOREIGN KEY (`project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`oauth_owner_grant_id`) REFERENCES `oauth_grants`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_remote_devices`("id", "name", "slug", "description", "token_hash", "token_prefix", "connection_mode", "direct_url", "status", "last_seen_at", "platform_os", "platform_arch", "shell_path", "default_cwd", "agent_version", "capabilities_json", "path_rules_json", "reported_path_rules_json", "owner_scope", "scope", "project_id", "created_by", "oauth_owner_grant_id", "oauth_provision_key", "enrolled_at", "enrolled_from_ip", "enrolled_user_agent", "created_at", "updated_at", "revoked_at") SELECT "id", "name", "slug", "description", "token_hash", "token_prefix", "connection_mode", "direct_url", "status", "last_seen_at", "platform_os", "platform_arch", "shell_path", "default_cwd", "agent_version", "capabilities_json", "path_rules_json", "reported_path_rules_json", "owner_scope", "scope", "project_id", "created_by", "oauth_owner_grant_id", "oauth_provision_key", "enrolled_at", "enrolled_from_ip", "enrolled_user_agent", "created_at", "updated_at", "revoked_at" FROM `remote_devices`;--> statement-breakpoint
DROP TABLE `remote_devices`;--> statement-breakpoint
ALTER TABLE `__new_remote_devices` RENAME TO `remote_devices`;--> statement-breakpoint
CREATE UNIQUE INDEX `idx_remote_devices_slug` ON `remote_devices` (`slug`);--> statement-breakpoint
CREATE INDEX `idx_remote_devices_status` ON `remote_devices` (`status`);--> statement-breakpoint
CREATE INDEX `idx_remote_devices_project` ON `remote_devices` (`project_id`);--> statement-breakpoint
CREATE INDEX `idx_remote_devices_oauth_owner` ON `remote_devices` (`oauth_owner_grant_id`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_remote_devices_oauth_provision` ON `remote_devices` (`oauth_owner_grant_id`,`oauth_provision_key`);--> statement-breakpoint
CREATE TABLE `__new_terminals` (
	`id` text PRIMARY KEY NOT NULL,
	`chapter_id` text,
	`narrator_id` text,
	`name` text NOT NULL,
	`cwd` text,
	`dtach_socket` text,
	`device_id` text,
	`status` text DEFAULT 'running' NOT NULL,
	`exit_code` integer,
	`graph_opened` integer DEFAULT 0 NOT NULL,
	`graph_x` real,
	`graph_y` real,
	`graph_width` real,
	`graph_height` real,
	`created_at` text NOT NULL,
	FOREIGN KEY (`chapter_id`) REFERENCES `chapters`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
INSERT INTO `__new_terminals`("id", "chapter_id", "narrator_id", "name", "cwd", "dtach_socket", "device_id", "status", "exit_code", "graph_opened", "graph_x", "graph_y", "graph_width", "graph_height", "created_at") SELECT "id", "chapter_id", "narrator_id", "name", "cwd", "dtach_socket", "device_id", "status", "exit_code", "graph_opened", "graph_x", "graph_y", "graph_width", "graph_height", "created_at" FROM `terminals`;--> statement-breakpoint
DROP TABLE `terminals`;--> statement-breakpoint
ALTER TABLE `__new_terminals` RENAME TO `terminals`;--> statement-breakpoint
CREATE INDEX `idx_terminals_chapter` ON `terminals` (`chapter_id`,`status`,`graph_opened`);--> statement-breakpoint
CREATE INDEX `idx_terminals_narrator` ON `terminals` (`narrator_id`);--> statement-breakpoint
CREATE INDEX `idx_terminals_status` ON `terminals` (`status`);