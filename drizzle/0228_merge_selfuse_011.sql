PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `outline_pending` (
	`id` text PRIMARY KEY NOT NULL,
	`space_id` text NOT NULL,
	`kind` text NOT NULL,
	`status` text NOT NULL,
	`page_id` text NOT NULL,
	`root_block_id` text,
	`ops_json` text NOT NULL,
	`base_json` text NOT NULL,
	`op_count` integer NOT NULL,
	`subtree_json` text NOT NULL,
	`block_ids_json` text NOT NULL,
	`adds_json` text NOT NULL,
	`summary` text NOT NULL,
	`requested_by_json` text NOT NULL,
	`requester_session` text,
	`decider_json` text NOT NULL,
	`decider_user_id` text,
	`rev` integer DEFAULT 1 NOT NULL,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	`decided_at` text,
	`decided_by_json` text,
	`decision_commit_id` text,
	`reason` text,
	`reported_at` text,
	FOREIGN KEY (`space_id`) REFERENCES `outline_spaces`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `idx_outline_pending_space` ON `outline_pending` (`space_id`);
--> statement-breakpoint
CREATE INDEX `idx_outline_pending_page_open` ON `outline_pending` (`page_id`) WHERE "status" = 'open';
--> statement-breakpoint
CREATE INDEX `idx_outline_pending_root_open` ON `outline_pending` (`root_block_id`) WHERE "status" = 'open';
--> statement-breakpoint
CREATE UNIQUE INDEX `uq_outline_pending_open_proposer` ON `outline_pending` (`requester_session`,`root_block_id`) WHERE "status" = 'open';
--> statement-breakpoint
CREATE INDEX `idx_outline_pending_unreported` ON `outline_pending` (`requester_session`,`page_id`) WHERE "reported_at" IS NULL AND "status" <> 'open';
--> statement-breakpoint
CREATE INDEX `idx_outline_pending_decider_open` ON `outline_pending` (`decider_user_id`) WHERE "status" = 'open';
--> statement-breakpoint
CREATE UNIQUE INDEX `uq_outline_pending_decision_commit` ON `outline_pending` (`decision_commit_id`);
--> statement-breakpoint
CREATE TABLE `outline_task_claims` (
	`task_block_id` text PRIMARY KEY NOT NULL,
	`holder_narrator_id` text,
	`holder_user_id` text,
	`epoch` integer NOT NULL,
	`task_version_at_claim` integer NOT NULL,
	`claimed_at` text NOT NULL,
	`expires_at` text,
	`last_holder_narrator_id` text,
	`last_holder_user_id` text,
	FOREIGN KEY (`task_block_id`) REFERENCES `outline_blocks`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `idx_outline_task_claims_holder` ON `outline_task_claims` (`holder_narrator_id`) WHERE "holder_narrator_id" IS NOT NULL;
--> statement-breakpoint
CREATE INDEX `idx_outline_task_claims_expiry` ON `outline_task_claims` (`expires_at`) WHERE "expires_at" IS NOT NULL;
--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `media_json` text;
--> statement-breakpoint
ALTER TABLE `narrators` ADD `hidden_from_lists` integer DEFAULT false NOT NULL;
--> statement-breakpoint
ALTER TABLE `narrators` ADD `modeltrace_key` text;
--> statement-breakpoint
ALTER TABLE `narrators` ADD `task_backend` text DEFAULT 'spec' NOT NULL;
--> statement-breakpoint
ALTER TABLE `narrators` ADD `reply_context` text;
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_narrators_modeltrace_key` ON `narrators` (`modeltrace_key`);
--> statement-breakpoint
ALTER TABLE `outline_blocks` ADD `in_process` integer DEFAULT false NOT NULL;
--> statement-breakpoint
CREATE INDEX `idx_outline_blocks_open_tasks` ON `outline_blocks` (`space_id`) WHERE json_extract("task_json", '$.status') IN ('todo', 'doing') AND "deleted" = 0;
--> statement-breakpoint
CREATE INDEX `idx_outline_blocks_doing_tasks` ON `outline_blocks` (`id`) WHERE json_extract("task_json", '$.status') = 'doing' AND "deleted" = 0;
--> statement-breakpoint
CREATE INDEX `idx_outline_blocks_locked` ON `outline_blocks` (`page_id`) WHERE json_extract("task_json", '$.lock') IS NOT NULL AND "deleted" = 0;
--> statement-breakpoint
CREATE INDEX `idx_outline_blocks_active_tasks` ON `outline_blocks` (`space_id`) WHERE "deleted" = 0 AND (json_extract("task_json", '$.status') = 'doing' OR (json_extract("task_json", '$.status') = 'todo' AND json_extract("task_json", '$.lock') IS NOT NULL));
--> statement-breakpoint
CREATE INDEX `idx_outline_blocks_finished_tasks` ON `outline_blocks` (`space_id`,`version_seq`,`id`) WHERE json_extract("task_json", '$.status') IN ('done', 'cancelled') AND "deleted" = 0;
--> statement-breakpoint
ALTER TABLE `outline_spaces` ADD `in_process_ready` integer DEFAULT false NOT NULL;
--> statement-breakpoint
ALTER TABLE `outline_spaces` ADD `in_process_epoch` integer DEFAULT 0 NOT NULL;
--> statement-breakpoint
CREATE INDEX `idx_outline_spaces_in_process_pending` ON `outline_spaces` (`id`) WHERE "in_process_ready" = 0;
--> statement-breakpoint
ALTER TABLE `user_preferences` ADD `git_identity_context_menu` integer DEFAULT false NOT NULL;
--> statement-breakpoint
ALTER TABLE `user_preferences` ADD `git_identity_create_modal` integer DEFAULT false NOT NULL;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
