PRAGMA foreign_keys=OFF;--> statement-breakpoint
CREATE TABLE `__new_background_tasks` (
	`id` text PRIMARY KEY NOT NULL,
	`parent_narrator_id` text NOT NULL,
	`type` text NOT NULL,
	`status` text NOT NULL,
	`command` text,
	`exit_code` integer,
	`subagent_narrator_id` text,
	`subagent_type` text,
	`transfer_task_id` text,
	`tool_use_id` text,
	`alias` text,
	`title` text,
	`output` text,
	`output_bytes` integer DEFAULT 0 NOT NULL,
	`output_truncated` integer DEFAULT false NOT NULL,
	`notified` integer DEFAULT false NOT NULL,
	`started_at` text NOT NULL,
	`completed_at` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`parent_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`subagent_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`transfer_task_id`) REFERENCES `device_transfer_tasks`(`id`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
INSERT INTO `__new_background_tasks`("id", "parent_narrator_id", "type", "status", "command", "exit_code", "subagent_narrator_id", "subagent_type", "transfer_task_id", "tool_use_id", "alias", "title", "output", "output_bytes", "output_truncated", "notified", "started_at", "completed_at", "created_at", "updated_at") SELECT "id", "parent_narrator_id", "type", "status", "command", "exit_code", "subagent_narrator_id", "subagent_type", "transfer_task_id", "tool_use_id", "alias", "title", "output", "output_bytes", "output_truncated", "notified", "started_at", "completed_at", "created_at", "updated_at" FROM `background_tasks`;--> statement-breakpoint
DROP TABLE `background_tasks`;--> statement-breakpoint
ALTER TABLE `__new_background_tasks` RENAME TO `background_tasks`;--> statement-breakpoint
PRAGMA foreign_keys=ON;--> statement-breakpoint
CREATE INDEX `idx_bg_tasks_parent` ON `background_tasks` (`parent_narrator_id`,`status`);--> statement-breakpoint
CREATE INDEX `idx_bg_tasks_subagent` ON `background_tasks` (`subagent_narrator_id`);--> statement-breakpoint
CREATE INDEX `idx_bg_tasks_transfer` ON `background_tasks` (`transfer_task_id`);--> statement-breakpoint
CREATE INDEX `idx_bg_tasks_parent_created` ON `background_tasks` (`parent_narrator_id`,`created_at`,`id`);--> statement-breakpoint
CREATE TABLE `__new_device_transfer_tasks` (
	`id` text PRIMARY KEY NOT NULL,
	`device_id` text NOT NULL,
	`direction` text NOT NULL,
	`remote_path` text NOT NULL,
	`local_path` text NOT NULL,
	`recursive` integer DEFAULT false NOT NULL,
	`run_generation` integer DEFAULT 0 NOT NULL,
	`status` text DEFAULT 'queued' NOT NULL,
	`files_transferred` integer DEFAULT 0 NOT NULL,
	`bytes_transferred` integer DEFAULT 0 NOT NULL,
	`total_files` integer,
	`total_bytes` integer,
	`current_file` text,
	`error` text,
	`created_by` text,
	`parent_narrator_id` text,
	`tool_use_id` text,
	`alias` text,
	`created_at` text NOT NULL,
	`started_at` text,
	`updated_at` text NOT NULL,
	`completed_at` text,
	FOREIGN KEY (`device_id`) REFERENCES `remote_devices`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`created_by`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`parent_narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
INSERT INTO `__new_device_transfer_tasks`("id", "device_id", "direction", "remote_path", "local_path", "recursive", "run_generation", "status", "files_transferred", "bytes_transferred", "total_files", "total_bytes", "current_file", "error", "created_by", "parent_narrator_id", "tool_use_id", "alias", "created_at", "started_at", "updated_at", "completed_at") SELECT "id", "device_id", "direction", "remote_path", "local_path", "recursive", "run_generation", "status", "files_transferred", "bytes_transferred", "total_files", "total_bytes", "current_file", "error", "created_by", "parent_narrator_id", "tool_use_id", "alias", "created_at", "started_at", "updated_at", "completed_at" FROM `device_transfer_tasks`;--> statement-breakpoint
DROP TABLE `device_transfer_tasks`;--> statement-breakpoint
ALTER TABLE `__new_device_transfer_tasks` RENAME TO `device_transfer_tasks`;--> statement-breakpoint
CREATE INDEX `idx_device_transfer_tasks_device_created` ON `device_transfer_tasks` (`device_id`,`created_at`);--> statement-breakpoint
CREATE INDEX `idx_device_transfer_tasks_status_updated` ON `device_transfer_tasks` (`status`,`updated_at`);--> statement-breakpoint
CREATE INDEX `idx_device_transfer_tasks_created_by` ON `device_transfer_tasks` (`created_by`);--> statement-breakpoint
CREATE INDEX `idx_device_transfer_tasks_parent_narrator` ON `device_transfer_tasks` (`parent_narrator_id`);--> statement-breakpoint
CREATE TABLE `__new_file_attributions` (
	`id` text PRIMARY KEY NOT NULL,
	`device_id` text DEFAULT 'local' NOT NULL,
	`workspace_path` text NOT NULL,
	`file_path` text NOT NULL,
	`narrator_id` text,
	`user_id` text,
	`subagent_type` text,
	`action` text NOT NULL,
	`tool_name` text,
	`tool_use_id` text,
	`lines_added` integer,
	`lines_removed` integer,
	`changed_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
INSERT INTO `__new_file_attributions`("id", "device_id", "workspace_path", "file_path", "narrator_id", "user_id", "subagent_type", "action", "tool_name", "tool_use_id", "lines_added", "lines_removed", "changed_at") SELECT "id", "device_id", "workspace_path", "file_path", "narrator_id", "user_id", "subagent_type", "action", "tool_name", "tool_use_id", "lines_added", "lines_removed", "changed_at" FROM `file_attributions`;--> statement-breakpoint
DROP TABLE `file_attributions`;--> statement-breakpoint
ALTER TABLE `__new_file_attributions` RENAME TO `file_attributions`;--> statement-breakpoint
CREATE INDEX `idx_file_attr_workspace_file` ON `file_attributions` (`workspace_path`,`file_path`,`changed_at`);--> statement-breakpoint
CREATE INDEX `idx_file_attr_device_workspace_file` ON `file_attributions` (`device_id`,`workspace_path`,`file_path`,`changed_at`);--> statement-breakpoint
CREATE INDEX `idx_file_attr_narrator` ON `file_attributions` (`narrator_id`);--> statement-breakpoint
CREATE INDEX `idx_file_attr_workspace` ON `file_attributions` (`workspace_path`,`changed_at`);