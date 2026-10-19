PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `narrators` ADD `runtime_registration_json` text;
--> statement-breakpoint
ALTER TABLE `narrators` ADD `policy_source_narrator_id` text REFERENCES narrators(id) ON UPDATE no action ON DELETE set null;
--> statement-breakpoint
CREATE INDEX `idx_narrators_policy_source` ON `narrators` (`policy_source_narrator_id`);
--> statement-breakpoint
PRAGMA foreign_keys=ON;
