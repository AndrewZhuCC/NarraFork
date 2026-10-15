PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `usage_token_repair_jobs` ADD `workflow_version` integer;
--> statement-breakpoint
ALTER TABLE `usage_token_repair_jobs` ADD `phase` text;
--> statement-breakpoint
ALTER TABLE `usage_token_repair_jobs` ADD `summary_json` text;
--> statement-breakpoint
ALTER TABLE `usage_token_repair_jobs` ADD `rows_per_second` real;
--> statement-breakpoint
ALTER TABLE `usage_token_repair_jobs` ADD `scratch_retired_at` text;
--> statement-breakpoint
CREATE INDEX `idx_token_repair_scratch_retirement` ON `usage_token_repair_jobs` (`updated_at`,`id`) WHERE "usage_token_repair_jobs"."scratch_retired_at" IS NULL AND "usage_token_repair_jobs"."status" IN ('completed', 'cancelled', 'failed', 'interrupted');
--> statement-breakpoint
PRAGMA foreign_keys=ON;
