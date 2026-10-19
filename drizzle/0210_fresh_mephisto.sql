PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `narrator_drafts` ADD `previous_json` text;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
