PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `narrator_drafts` ADD `composer_document` text;
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `composer_document` text;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
