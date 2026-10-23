PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE INDEX `idx_messages_legacy_body` ON `narrator_messages` (`body_format`) WHERE "body_format" = 1;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
