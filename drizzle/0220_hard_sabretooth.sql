PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `body_rows_only` integer DEFAULT 0 NOT NULL;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
