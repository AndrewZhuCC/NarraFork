PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `usage_token_repair_candidates` (
	`id` text PRIMARY KEY NOT NULL,
	`job_id` text NOT NULL,
	`request_id` text NOT NULL,
	`created_at` text NOT NULL,
	`provider` text,
	`model` text,
	`snapshot_json` text NOT NULL,
	`evidence_json` text NOT NULL,
	`applied_at` text,
	`applied_by` text,
	`totals_status` text DEFAULT 'unchanged' NOT NULL,
	CONSTRAINT "token_repair_snapshot_bound" CHECK(length("usage_token_repair_candidates"."snapshot_json") <= 8192),
	CONSTRAINT "token_repair_evidence_bound" CHECK(length("usage_token_repair_candidates"."evidence_json") <= 16384)
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_token_repair_candidate_request` ON `usage_token_repair_candidates` (`job_id`,`request_id`);
--> statement-breakpoint
CREATE INDEX `idx_token_repair_candidates_page` ON `usage_token_repair_candidates` (`job_id`,`id`);
--> statement-breakpoint
CREATE TABLE `usage_token_repair_jobs` (
	`id` text PRIMARY KEY NOT NULL,
	`status` text NOT NULL,
	`created_by` text NOT NULL,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	`scanned` integer DEFAULT 0 NOT NULL,
	`repaired` integer DEFAULT 0 NOT NULL,
	`conflicts` integer DEFAULT 0 NOT NULL,
	`upper_created_at` text,
	`upper_id` text,
	`cursor_created_at` text,
	`cursor_id` text,
	`include_reviewed` integer NOT NULL,
	`error` text,
	CONSTRAINT "token_repair_error_bound" CHECK("usage_token_repair_jobs"."error" IS NULL OR length("usage_token_repair_jobs"."error") <= 2048)
);
--> statement-breakpoint
CREATE INDEX `idx_token_repair_jobs_created` ON `usage_token_repair_jobs` (`created_at`,`id`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_token_repair_single_active` ON `usage_token_repair_jobs` ((1)) WHERE "usage_token_repair_jobs"."status" IN ('analyzing', 'ready', 'applying');
--> statement-breakpoint
ALTER TABLE `api_requests` ADD `token_accounting_version` integer;
--> statement-breakpoint
ALTER TABLE `api_requests` ADD `token_accounting_review_version` integer;
--> statement-breakpoint
CREATE INDEX `idx_api_requests_token_repair_pending` ON `api_requests` (`created_at`,`id`) WHERE "api_requests"."token_accounting_version" IS NULL AND "api_requests"."token_accounting_review_version" IS NULL AND ("api_requests"."cached_input_tokens" > 0 OR "api_requests"."cache_creation_input_tokens" > 0 OR "api_requests"."cache_creation_5m_tokens" > 0 OR "api_requests"."cache_creation_1h_tokens" > 0);
--> statement-breakpoint
CREATE INDEX `idx_api_requests_token_repair_reviewed` ON `api_requests` (`created_at`,`id`) WHERE "api_requests"."token_accounting_version" IS NULL AND ("api_requests"."cached_input_tokens" > 0 OR "api_requests"."cache_creation_input_tokens" > 0 OR "api_requests"."cache_creation_5m_tokens" > 0 OR "api_requests"."cache_creation_1h_tokens" > 0);
--> statement-breakpoint
PRAGMA foreign_keys=ON;
