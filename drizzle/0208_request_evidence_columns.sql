PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `api_requests` ADD `diagnostics_json` text;
--> statement-breakpoint
ALTER TABLE `api_requests` ADD `recording_request_id` text;
--> statement-breakpoint
CREATE INDEX `idx_api_requests_recording` ON `api_requests` (`recording_request_id`);
--> statement-breakpoint
PRAGMA foreign_keys=ON;
