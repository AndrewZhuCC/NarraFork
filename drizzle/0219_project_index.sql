PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `narrator_project_links` (
	`narrator_id` text NOT NULL,
	`project_id` text NOT NULL,
	`role` text NOT NULL,
	`source` text NOT NULL,
	`device_id` text DEFAULT 'local' NOT NULL,
	`directory` text,
	`updated_at` text NOT NULL,
	PRIMARY KEY(`narrator_id`, `project_id`),
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `uq_narrator_project_links_primary` ON `narrator_project_links` (`narrator_id`) WHERE "role" = 'primary';
--> statement-breakpoint
CREATE INDEX `idx_narrator_project_links_project` ON `narrator_project_links` (`project_id`,`narrator_id`);
--> statement-breakpoint
ALTER TABLE `projects` ADD `root_key` text;
--> statement-breakpoint
CREATE UNIQUE INDEX `uq_projects_root_key` ON `projects` (`root_key`) WHERE "root_key" IS NOT NULL;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
