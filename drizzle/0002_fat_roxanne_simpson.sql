CREATE TABLE `workspace_panels` (
	`id` text PRIMARY KEY NOT NULL,
	`workspace_id` text NOT NULL,
	`kind` text NOT NULL,
	`narrator_id` text,
	`config_json` text,
	`sort_order` integer NOT NULL,
	`created_at` integer NOT NULL,
	`updated_at` integer NOT NULL,
	FOREIGN KEY (`workspace_id`) REFERENCES `workspaces`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `idx_workspace_panels_workspace` ON `workspace_panels` (`workspace_id`,`sort_order`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_workspace_panels_narrator` ON `workspace_panels` (`workspace_id`,`narrator_id`);--> statement-breakpoint
CREATE INDEX `idx_workspace_panels_narrator_lookup` ON `workspace_panels` (`narrator_id`);--> statement-breakpoint
ALTER TABLE `api_requests` ADD `agent_label` text;--> statement-breakpoint
ALTER TABLE `background_tasks` ADD `transfer_task_id` text REFERENCES device_transfer_tasks(id);--> statement-breakpoint
CREATE INDEX `idx_bg_tasks_transfer` ON `background_tasks` (`transfer_task_id`);--> statement-breakpoint
ALTER TABLE `device_transfer_tasks` ADD `parent_narrator_id` text REFERENCES narrators(id);--> statement-breakpoint
ALTER TABLE `device_transfer_tasks` ADD `tool_use_id` text;--> statement-breakpoint
ALTER TABLE `device_transfer_tasks` ADD `alias` text;--> statement-breakpoint
CREATE INDEX `idx_device_transfer_tasks_parent_narrator` ON `device_transfer_tasks` (`parent_narrator_id`);--> statement-breakpoint
ALTER TABLE `file_attributions` ADD `user_id` text REFERENCES users(id);--> statement-breakpoint
ALTER TABLE `file_attributions` ADD `lines_added` integer;--> statement-breakpoint
ALTER TABLE `file_attributions` ADD `lines_removed` integer;--> statement-breakpoint
ALTER TABLE `user_preferences` ADD `narrator_font_scale_percent` integer DEFAULT 100 NOT NULL;--> statement-breakpoint
ALTER TABLE `user_preferences` ADD `narrator_letter_spacing_percent` integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE `user_preferences` ADD `narrator_line_height_scale_percent` integer DEFAULT 100 NOT NULL;--> statement-breakpoint
ALTER TABLE `user_preferences` ADD `narrator_paragraph_scale_percent` integer DEFAULT 100 NOT NULL;--> statement-breakpoint
ALTER TABLE `user_preferences` ADD `tutorial_progress` text DEFAULT '{}' NOT NULL;--> statement-breakpoint
ALTER TABLE `workspaces` ADD `layout_revision` integer DEFAULT 0 NOT NULL;