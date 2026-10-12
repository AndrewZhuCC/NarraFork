PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `narrator_git_identity_bindings` (
	`id` text PRIMARY KEY NOT NULL,
	`narrator_id` text NOT NULL,
	`user_id` text NOT NULL,
	`identity_id` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`identity_id`) REFERENCES `user_git_identities`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_narrator_git_identity_unique` ON `narrator_git_identity_bindings` (`narrator_id`,`user_id`);
--> statement-breakpoint
CREATE INDEX `idx_narrator_git_identity_user` ON `narrator_git_identity_bindings` (`user_id`);
--> statement-breakpoint
CREATE TABLE `user_git_identities` (
	`id` text PRIMARY KEY NOT NULL,
	`user_id` text NOT NULL,
	`name` text NOT NULL,
	`email` text NOT NULL,
	`is_default` integer DEFAULT false NOT NULL,
	`created_at` text NOT NULL,
	FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `idx_user_git_identities_user` ON `user_git_identities` (`user_id`,`created_at`);
--> statement-breakpoint
ALTER TABLE `background_tasks` ADD `background_kind` text DEFAULT 'task' NOT NULL;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
