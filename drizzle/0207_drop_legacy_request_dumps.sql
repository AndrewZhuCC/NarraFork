PRAGMA foreign_keys=OFF;
--> statement-breakpoint
DROP TABLE `usage_token_repair_candidates`;
--> statement-breakpoint
DROP TABLE `usage_token_repair_jobs`;
--> statement-breakpoint
DROP INDEX `idx_api_requests_token_repair_pending`;
--> statement-breakpoint
DROP INDEX `idx_api_requests_token_repair_reviewed`;
--> statement-breakpoint
ALTER TABLE `api_requests` DROP COLUMN `token_accounting_version`;
--> statement-breakpoint
ALTER TABLE `api_requests` DROP COLUMN `token_accounting_review_version`;
--> statement-breakpoint
ALTER TABLE `api_requests` DROP COLUMN `raw_dump_json`;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
