PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `narrator_event_subscriptions` (
	`id` text PRIMARY KEY NOT NULL,
	`source_narrator_id` text NOT NULL,
	`recipient_narrator_id` text NOT NULL,
	`run_id` text,
	`events` text NOT NULL,
	`wake` integer DEFAULT false NOT NULL,
	`origin` text NOT NULL,
	`authorized_by_user_id` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`source_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`recipient_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`authorized_by_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
CREATE INDEX `idx_narrator_event_sub_source_run` ON `narrator_event_subscriptions` (`source_narrator_id`,`run_id`);
--> statement-breakpoint
CREATE INDEX `idx_narrator_event_sub_recipient` ON `narrator_event_subscriptions` (`recipient_narrator_id`);
--> statement-breakpoint
CREATE INDEX `idx_narrator_event_sub_authorized_by` ON `narrator_event_subscriptions` (`authorized_by_user_id`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_narrator_event_sub_session_unique` ON `narrator_event_subscriptions` (`source_narrator_id`,`recipient_narrator_id`) WHERE "run_id" IS NULL;
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_narrator_event_sub_run_unique` ON `narrator_event_subscriptions` (`source_narrator_id`,`recipient_narrator_id`,`run_id`) WHERE "run_id" IS NOT NULL;
--> statement-breakpoint
ALTER TABLE `narrators` ADD `runtime_registration_json` text;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
