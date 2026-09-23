CREATE TABLE `notifications` (
	`id` text PRIMARY KEY NOT NULL,
	`user_id` text NOT NULL,
	`kind` text NOT NULL,
	`project_id` text,
	`chapter_id` text,
	`narrator_id` text,
	`title` text NOT NULL,
	`preview` text DEFAULT '' NOT NULL,
	`link_json` text NOT NULL,
	`source_key` text NOT NULL,
	`status` text DEFAULT 'unread' NOT NULL,
	`created_at` integer NOT NULL,
	`read_at` integer,
	FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_notifications_user_kind_source` ON `notifications` (`user_id`,`kind`,`source_key`);--> statement-breakpoint
CREATE INDEX `idx_notifications_user_created` ON `notifications` (`user_id`,`created_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_notifications_user_status_created` ON `notifications` (`user_id`,`status`,`created_at`,`id`);--> statement-breakpoint
CREATE TABLE `user_usage_totals` (
	`user_id` text PRIMARY KEY NOT NULL,
	`request_count` integer DEFAULT 0 NOT NULL,
	`input_tokens` integer DEFAULT 0 NOT NULL,
	`output_tokens` integer DEFAULT 0 NOT NULL,
	`cached_input_tokens` integer DEFAULT 0 NOT NULL,
	`cache_creation_tokens` integer DEFAULT 0 NOT NULL,
	`reasoning_tokens` integer DEFAULT 0 NOT NULL,
	`cost_usd` real DEFAULT 0 NOT NULL,
	`unpriced_request_count` integer DEFAULT 0 NOT NULL,
	`partial_request_count` integer DEFAULT 0 NOT NULL,
	`first_used_at` text NOT NULL,
	`last_used_at` text NOT NULL
);
--> statement-breakpoint
CREATE TABLE `workspace_execution_owners` (
	`owner_epoch` text PRIMARY KEY NOT NULL,
	`identity_json` text,
	`created_at` text NOT NULL
);
--> statement-breakpoint
ALTER TABLE `api_requests` ADD `user_id` text;--> statement-breakpoint
ALTER TABLE `api_requests` ADD `cost_status` text;--> statement-breakpoint
ALTER TABLE `api_requests` ADD `cost_missing_fields` text;--> statement-breakpoint
CREATE INDEX `idx_api_requests_user_created` ON `api_requests` (`user_id`,`created_at`,`id`);--> statement-breakpoint
ALTER TABLE `credential_usage_totals` ADD `partial_request_count` integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE `file_change_scope_recoveries` ADD `resolution_authority` text DEFAULT 'execution_proven' NOT NULL;--> statement-breakpoint
ALTER TABLE `file_change_scope_recoveries` ADD `maintenance_evidence_json` text;--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `cost_status` text;--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `cost_missing_fields` text;--> statement-breakpoint
ALTER TABLE `narrator_tool_calls` ADD `cost_status` text;--> statement-breakpoint
ALTER TABLE `narrator_tool_calls` ADD `cost_missing_fields` text;--> statement-breakpoint
ALTER TABLE `workspace_write_leases` ADD `execution_class` text DEFAULT 'unknown' NOT NULL;--> statement-breakpoint
ALTER TABLE `workspace_write_leases` ADD `termination_evidence_json` text;--> statement-breakpoint
CREATE INDEX `idx_workspace_lease_owner` ON `workspace_write_leases` (`owner_epoch`,`lease_id`);