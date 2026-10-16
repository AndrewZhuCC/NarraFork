PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `__new_usage_token_repair_jobs` (
	`id` text PRIMARY KEY NOT NULL,
	`status` text NOT NULL,
	`created_by` text NOT NULL,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	`owner_id` text,
	`lease_until` text,
	`scanned` integer DEFAULT 0 NOT NULL,
	`repaired` integer DEFAULT 0 NOT NULL,
	`conflicts` integer DEFAULT 0 NOT NULL,
	`upper_created_at` text,
	`upper_id` text,
	`cursor_created_at` text,
	`cursor_id` text,
	`include_reviewed` integer NOT NULL,
	`protocol_overrides_json` text DEFAULT '[]' NOT NULL,
	`error` text,
	CONSTRAINT "token_repair_protocol_config_bound" CHECK(length("__new_usage_token_repair_jobs"."protocol_overrides_json") <= 16384),
	CONSTRAINT "token_repair_error_bound" CHECK("__new_usage_token_repair_jobs"."error" IS NULL OR length("__new_usage_token_repair_jobs"."error") <= 2048)
);
--> statement-breakpoint
INSERT INTO `__new_usage_token_repair_jobs`("id", "status", "created_by", "created_at", "updated_at", "owner_id", "lease_until", "scanned", "repaired", "conflicts", "upper_created_at", "upper_id", "cursor_created_at", "cursor_id", "include_reviewed", "error") SELECT "id", "status", "created_by", "created_at", "updated_at", "owner_id", "lease_until", "scanned", "repaired", "conflicts", "upper_created_at", "upper_id", "cursor_created_at", "cursor_id", "include_reviewed", "error" FROM `usage_token_repair_jobs`;
--> statement-breakpoint
DROP TABLE `usage_token_repair_jobs`;
--> statement-breakpoint
ALTER TABLE `__new_usage_token_repair_jobs` RENAME TO `usage_token_repair_jobs`;
--> statement-breakpoint
CREATE INDEX `idx_token_repair_jobs_created` ON `usage_token_repair_jobs` (`created_at`,`id`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_token_repair_single_active` ON `usage_token_repair_jobs` (1) WHERE "usage_token_repair_jobs"."status" IN ('analyzing', 'ready', 'applying');
--> statement-breakpoint
PRAGMA foreign_keys=ON;
