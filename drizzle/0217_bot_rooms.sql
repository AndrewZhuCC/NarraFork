PRAGMA foreign_keys=OFF;
--> statement-breakpoint
DROP INDEX `idx_chat_rooms_narrator`;
--> statement-breakpoint
CREATE INDEX `idx_chat_rooms_narrator_id` ON `chat_rooms` (`narrator_id`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_chat_rooms_narrator` ON `chat_rooms` (`narrator_id`) WHERE "kind" = 'narrator';
--> statement-breakpoint
ALTER TABLE `chat_messages` ADD `sender_narrator_id` text REFERENCES narrators(id) ON UPDATE no action ON DELETE set null;
--> statement-breakpoint
ALTER TABLE `chat_messages` ADD `reply_to_sender_narrator_id` text;
--> statement-breakpoint
CREATE INDEX `idx_chat_messages_sender_narrator` ON `chat_messages` (`sender_narrator_id`);
--> statement-breakpoint
PRAGMA foreign_keys=ON;
