PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `narrator_context_pins` (
	`generation` text PRIMARY KEY NOT NULL,
	`narrator_id` text NOT NULL,
	`segments_json` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `idx_context_pins_narrator` ON `narrator_context_pins` (`narrator_id`);
--> statement-breakpoint
CREATE TABLE `narrator_message_context_stats` (
	`message_id` text PRIMARY KEY NOT NULL,
	`role` text NOT NULL,
	`parent_tool_use_id` text,
	`segments_json` text NOT NULL,
	FOREIGN KEY (`message_id`) REFERENCES `narrator_messages`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
DROP TABLE `narrator_context_char_pages`;
--> statement-breakpoint
DROP TABLE `usage_token_repair_candidates`;
--> statement-breakpoint
DROP TABLE `usage_token_repair_jobs`;
--> statement-breakpoint
DROP INDEX `idx_api_requests_token_repair_pending`;
--> statement-breakpoint
DROP INDEX `idx_api_requests_token_repair_reviewed`;
--> statement-breakpoint
ALTER TABLE `api_requests` ADD `diagnostics_json` text;
--> statement-breakpoint
ALTER TABLE `api_requests` ADD `recording_request_id` text;
--> statement-breakpoint
CREATE INDEX `idx_api_requests_recording` ON `api_requests` (`recording_request_id`);
--> statement-breakpoint
ALTER TABLE `api_requests` DROP COLUMN `token_accounting_version`;
--> statement-breakpoint
ALTER TABLE `api_requests` DROP COLUMN `token_accounting_review_version`;
--> statement-breakpoint
ALTER TABLE `api_requests` DROP COLUMN `raw_dump_json`;
--> statement-breakpoint
DROP INDEX `idx_toolcalls_context_id`;
--> statement-breakpoint
CREATE INDEX `idx_toolcalls_context_id` ON `narrator_tool_calls` (`message_id`,`id`,`tool_use_id`,`created_at`,`execution_attempt`,`input_chars`,`output_chars`);
--> statement-breakpoint
ALTER TABLE `narrator_drafts` ADD `composer_document` text;
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `composer_document` text;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
--> statement-breakpoint
INSERT INTO `narrator_message_context_stats` (`message_id`, `role`, `parent_tool_use_id`, `segments_json`)
SELECT `id`, `role`, `parent_tool_use_id`, COALESCE(`context_chars_json`, '{"segments":[]}')
FROM `narrator_messages`
WHERE true
ON CONFLICT(`message_id`) DO NOTHING;
--> statement-breakpoint
CREATE TRIGGER `narrator_message_context_stats_insert`
AFTER INSERT ON `narrator_messages`
BEGIN
	INSERT INTO `narrator_message_context_stats` (`message_id`, `role`, `parent_tool_use_id`, `segments_json`)
	VALUES (
		`NEW`.`id`,
		`NEW`.`role`,
		`NEW`.`parent_tool_use_id`,
		COALESCE(`NEW`.`context_chars_json`, '{"segments":[]}')
	)
	ON CONFLICT(`message_id`) DO UPDATE SET
		`role` = `excluded`.`role`,
		`parent_tool_use_id` = `excluded`.`parent_tool_use_id`,
		`segments_json` = `excluded`.`segments_json`;
END;
--> statement-breakpoint
CREATE TRIGGER `narrator_message_context_stats_update`
AFTER UPDATE OF `role`, `parent_tool_use_id`, `context_chars_json` ON `narrator_messages`
BEGIN
	INSERT INTO `narrator_message_context_stats` (`message_id`, `role`, `parent_tool_use_id`, `segments_json`)
	VALUES (
		`NEW`.`id`,
		`NEW`.`role`,
		`NEW`.`parent_tool_use_id`,
		COALESCE(`NEW`.`context_chars_json`, '{"segments":[]}')
	)
	ON CONFLICT(`message_id`) DO UPDATE SET
		`role` = `excluded`.`role`,
		`parent_tool_use_id` = `excluded`.`parent_tool_use_id`,
		`segments_json` = `excluded`.`segments_json`;
END;
