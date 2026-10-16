PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `usage_token_repair_jobs` ADD `owner_id` text;
--> statement-breakpoint
ALTER TABLE `usage_token_repair_jobs` ADD `lease_until` text;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
