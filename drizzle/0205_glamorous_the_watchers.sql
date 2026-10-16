PRAGMA foreign_keys=OFF;
--> statement-breakpoint
DROP INDEX `idx_toolcalls_context_id`;
--> statement-breakpoint
CREATE INDEX `idx_toolcalls_context_id` ON `narrator_tool_calls` (`message_id`,`id`,`tool_use_id`,`created_at`,`execution_attempt`,`input_chars`,`output_chars`);
--> statement-breakpoint
PRAGMA foreign_keys=ON;
