PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `__new_narrator_block_backfill` (
	`job` text PRIMARY KEY NOT NULL,
	`cursor` integer DEFAULT 0 NOT NULL,
	`complete` integer DEFAULT false NOT NULL,
	`paused` integer DEFAULT false NOT NULL,
	`processed` integer DEFAULT 0 NOT NULL,
	`updated_at` text NOT NULL,
	CONSTRAINT "ck_block_backfill_job" CHECK("__new_narrator_block_backfill"."job" in ('convert', 'verify', 'clear', 'restore'))
);
--> statement-breakpoint
INSERT INTO `__new_narrator_block_backfill`("job", "cursor", "complete", "paused", "processed", "updated_at") SELECT "job", "cursor", "complete", "paused", "processed", "updated_at" FROM `narrator_block_backfill`;
--> statement-breakpoint
DROP TABLE `narrator_block_backfill`;
--> statement-breakpoint
ALTER TABLE `__new_narrator_block_backfill` RENAME TO `narrator_block_backfill`;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
