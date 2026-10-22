PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `narrator_block_backfill` (
	`job` text PRIMARY KEY NOT NULL,
	`cursor` integer DEFAULT 0 NOT NULL,
	`complete` integer DEFAULT false NOT NULL,
	`paused` integer DEFAULT false NOT NULL,
	`processed` integer DEFAULT 0 NOT NULL,
	`updated_at` text NOT NULL,
	CONSTRAINT "ck_block_backfill_job" CHECK("narrator_block_backfill"."job" in ('convert', 'verify'))
);
--> statement-breakpoint
CREATE TABLE `narrator_block_diagnostics` (
	`message_id` text PRIMARY KEY NOT NULL,
	`kind` text NOT NULL,
	`detail` text,
	`created_at` text NOT NULL,
	FOREIGN KEY (`message_id`) REFERENCES `narrator_messages`(`id`) ON UPDATE no action ON DELETE cascade,
	CONSTRAINT "ck_block_diagnostics_kind" CHECK("narrator_block_diagnostics"."kind" in ('mismatch', 'undecodable'))
);
--> statement-breakpoint
PRAGMA foreign_keys=ON;
