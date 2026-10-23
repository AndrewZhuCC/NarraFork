PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE INDEX `idx_messages_rows_only` ON `narrator_messages` (`body_rows_only`) WHERE "body_rows_only" = 1;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
