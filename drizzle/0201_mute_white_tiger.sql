PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `usage_token_repair_jobs` ADD `source_job_id` text;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
