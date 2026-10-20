PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `file_change_operations` ADD `workspace_lease_id` text;
--> statement-breakpoint
ALTER TABLE `file_change_operations` ADD `editor_raw_snapshot_revision` real;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
