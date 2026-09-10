PRAGMA foreign_keys=OFF;--> statement-breakpoint
CREATE TABLE `__new_chat_messages` (
	`id` text PRIMARY KEY NOT NULL,
	`room_id` text NOT NULL,
	`seq` integer NOT NULL,
	`sender_user_id` text,
	`sender_share_id` text,
	`sender_guest_name` text,
	`kind` text DEFAULT 'text' NOT NULL,
	`content_text` text NOT NULL,
	`reply_to_message_id` text,
	`reply_to_seq` integer,
	`reply_to_sender_user_id` text,
	`reply_to_guest_name` text,
	`reply_to_preview` text,
	`edited_at` text,
	`deleted_at` text,
	`created_at` text NOT NULL,
	FOREIGN KEY (`room_id`) REFERENCES `chat_rooms`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`sender_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`sender_share_id`) REFERENCES `narrator_public_shares`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_chat_messages`("id", "room_id", "seq", "sender_user_id", "sender_share_id", "sender_guest_name", "kind", "content_text", "reply_to_message_id", "reply_to_seq", "reply_to_sender_user_id", "reply_to_guest_name", "reply_to_preview", "edited_at", "deleted_at", "created_at") SELECT "id", "room_id", "seq", "sender_user_id", "sender_share_id", "sender_guest_name", "kind", "content_text", "reply_to_message_id", "reply_to_seq", "reply_to_sender_user_id", "reply_to_guest_name", "reply_to_preview", "edited_at", "deleted_at", "created_at" FROM `chat_messages`;--> statement-breakpoint
DROP TABLE `chat_messages`;--> statement-breakpoint
ALTER TABLE `__new_chat_messages` RENAME TO `chat_messages`;--> statement-breakpoint
PRAGMA foreign_keys=ON;--> statement-breakpoint
CREATE UNIQUE INDEX `idx_chat_messages_room_seq` ON `chat_messages` (`room_id`,`seq`);--> statement-breakpoint
CREATE INDEX `idx_chat_messages_sender` ON `chat_messages` (`sender_user_id`);--> statement-breakpoint
CREATE INDEX `idx_chat_messages_sender_share` ON `chat_messages` (`sender_share_id`);--> statement-breakpoint
CREATE TABLE `__new_narrator_tool_calls` (
	`id` text PRIMARY KEY NOT NULL,
	`narrator_id` text NOT NULL,
	`message_id` text NOT NULL,
	`tool_use_id` text NOT NULL,
	`tool_name` text NOT NULL,
	`input_json` text,
	`output_json` text,
	`execution_device_id` text,
	`execution_cwd` text,
	`execution_path_flavor` text,
	`resolved_file_path` text,
	`canonical_file_path` text,
	`runtime_generation` integer,
	`execution_targets_json` text,
	`device_selection_source` text,
	`status` text DEFAULT 'initializing' NOT NULL,
	`duration_ms` integer,
	`stream_started_at` text,
	`permission_started_at` text,
	`execution_started_at` text,
	`completed_at` text,
	`started_at` text GENERATED ALWAYS AS (coalesce("execution_started_at", "permission_started_at", "stream_started_at", "created_at")) VIRTUAL,
	`error_message` text,
	`permission_decided_by` text,
	`permission_decided_at` text,
	`permission_deny_message` text,
	`permission_decision_reason` text,
	`permission_suggestions` text,
	`is_background` integer DEFAULT false NOT NULL,
	`execution_identity_version` integer DEFAULT 0 NOT NULL,
	`execution_origin_tool_call_id` text,
	`execution_attempt` integer DEFAULT 0 NOT NULL,
	`file_change_operation_id` text,
	`is_file_history_checkpoint` integer DEFAULT false NOT NULL,
	`tree_hash_before` text,
	`tree_hash_after` text,
	`owned_paths_json` text,
	`input_tokens` integer DEFAULT 0 NOT NULL,
	`output_tokens` integer DEFAULT 0 NOT NULL,
	`cache_creation_tokens` integer DEFAULT 0 NOT NULL,
	`cache_read_tokens` integer DEFAULT 0 NOT NULL,
	`cache_creation_5m_tokens` integer DEFAULT 0 NOT NULL,
	`cache_creation_1h_tokens` integer DEFAULT 0 NOT NULL,
	`input_cost` real DEFAULT 0 NOT NULL,
	`output_cost` real DEFAULT 0 NOT NULL,
	`cache_creation_cost` real DEFAULT 0 NOT NULL,
	`cache_read_cost` real DEFAULT 0 NOT NULL,
	`total_cost` real DEFAULT 0 NOT NULL,
	`provider` text,
	`model` text,
	`result_message_id` text,
	`created_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`message_id`) REFERENCES `narrator_messages`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`file_change_operation_id`) REFERENCES `file_change_operations`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
INSERT INTO `__new_narrator_tool_calls`("id", "narrator_id", "message_id", "tool_use_id", "tool_name", "input_json", "output_json", "execution_device_id", "execution_cwd", "execution_path_flavor", "resolved_file_path", "canonical_file_path", "runtime_generation", "execution_targets_json", "device_selection_source", "status", "duration_ms", "stream_started_at", "permission_started_at", "execution_started_at", "completed_at", "started_at", "error_message", "permission_decided_by", "permission_decided_at", "permission_deny_message", "permission_decision_reason", "permission_suggestions", "is_background", "execution_identity_version", "execution_origin_tool_call_id", "execution_attempt", "file_change_operation_id", "is_file_history_checkpoint", "tree_hash_before", "tree_hash_after", "owned_paths_json", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "input_cost", "output_cost", "cache_creation_cost", "cache_read_cost", "total_cost", "provider", "model", "result_message_id", "created_at") SELECT "id", "narrator_id", "message_id", "tool_use_id", "tool_name", "input_json", "output_json", "execution_device_id", "execution_cwd", "execution_path_flavor", "resolved_file_path", "canonical_file_path", "runtime_generation", "execution_targets_json", "device_selection_source", "status", "duration_ms", "stream_started_at", "permission_started_at", "execution_started_at", "completed_at", "started_at", "error_message", "permission_decided_by", "permission_decided_at", "permission_deny_message", "permission_decision_reason", "permission_suggestions", "is_background", "execution_identity_version", "execution_origin_tool_call_id", "execution_attempt", "file_change_operation_id", "is_file_history_checkpoint", "tree_hash_before", "tree_hash_after", "owned_paths_json", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "input_cost", "output_cost", "cache_creation_cost", "cache_read_cost", "total_cost", "provider", "model", "result_message_id", "created_at" FROM `narrator_tool_calls`;--> statement-breakpoint
DROP TABLE `narrator_tool_calls`;--> statement-breakpoint
ALTER TABLE `__new_narrator_tool_calls` RENAME TO `narrator_tool_calls`;--> statement-breakpoint
CREATE INDEX `idx_toolcalls_message` ON `narrator_tool_calls` (`message_id`);--> statement-breakpoint
CREATE INDEX `idx_toolcalls_tool_use_id` ON `narrator_tool_calls` (`tool_use_id`);--> statement-breakpoint
CREATE INDEX `idx_toolcalls_execution_device` ON `narrator_tool_calls` (`execution_device_id`,`created_at`);--> statement-breakpoint
CREATE INDEX `idx_toolcalls_status` ON `narrator_tool_calls` (`narrator_id`,`status`);--> statement-breakpoint
CREATE INDEX `idx_toolcalls_status_narrator_created` ON `narrator_tool_calls` (`status`,`narrator_id`,`created_at`);--> statement-breakpoint
CREATE INDEX `idx_toolcalls_created` ON `narrator_tool_calls` (`narrator_id`,`created_at`);--> statement-breakpoint
CREATE INDEX `idx_toolcalls_attempt` ON `narrator_tool_calls` (`narrator_id`,`tool_use_id`,`message_id`,`execution_attempt`);--> statement-breakpoint
CREATE INDEX `idx_toolcalls_file_change_operation` ON `narrator_tool_calls` (`file_change_operation_id`);--> statement-breakpoint
CREATE INDEX `idx_toolcalls_started_at` ON `narrator_tool_calls` (`started_at`,`id`);