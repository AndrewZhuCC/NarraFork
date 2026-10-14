PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `user_preferences` ADD `spec_tab_layout` text DEFAULT '{}' NOT NULL;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
