PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `__new_knowledge_injection_events` (
	`id` text PRIMARY KEY NOT NULL,
	`narrator_id` text NOT NULL,
	`compact_seq` integer NOT NULL,
	`entry_id` text,
	`personal_entry_id` text,
	`entry_revision_id` text,
	`source` text NOT NULL,
	`trigger_message_id` text,
	`trigger_tool_call_id` text,
	`summary` text,
	`created_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`entry_id`) REFERENCES `knowledge_entries`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`personal_entry_id`) REFERENCES `knowledge_drafts`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`entry_revision_id`) REFERENCES `knowledge_revisions`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`trigger_message_id`) REFERENCES `narrator_messages`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`trigger_tool_call_id`) REFERENCES `narrator_tool_calls`(`id`) ON UPDATE no action ON DELETE set null,
	CONSTRAINT "knowledge_injection_exactly_one_entry" CHECK(("__new_knowledge_injection_events"."entry_id" IS NOT NULL AND "__new_knowledge_injection_events"."personal_entry_id" IS NULL) OR ("__new_knowledge_injection_events"."entry_id" IS NULL AND "__new_knowledge_injection_events"."personal_entry_id" IS NOT NULL))
);
--> statement-breakpoint
INSERT INTO `__new_knowledge_injection_events`("id", "narrator_id", "compact_seq", "entry_id", "entry_revision_id", "source", "trigger_message_id", "trigger_tool_call_id", "summary", "created_at") SELECT "id", "narrator_id", "compact_seq", "entry_id", "entry_revision_id", "source", "trigger_message_id", "trigger_tool_call_id", "summary", "created_at" FROM `knowledge_injection_events`;
--> statement-breakpoint
DROP TABLE `knowledge_injection_events`;
--> statement-breakpoint
ALTER TABLE `__new_knowledge_injection_events` RENAME TO `knowledge_injection_events`;
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_kie_cycle_entry` ON `knowledge_injection_events` (`narrator_id`,`compact_seq`,`entry_id`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_kie_cycle_personal_entry` ON `knowledge_injection_events` (`narrator_id`,`compact_seq`,`personal_entry_id`);
--> statement-breakpoint
CREATE INDEX `idx_kie_personal_entry` ON `knowledge_injection_events` (`personal_entry_id`);
--> statement-breakpoint
CREATE INDEX `idx_kie_narrator_cycle` ON `knowledge_injection_events` (`narrator_id`,`compact_seq`);
--> statement-breakpoint
CREATE INDEX `idx_kie_entry` ON `knowledge_injection_events` (`entry_id`);
--> statement-breakpoint
CREATE INDEX `idx_kie_trigger_message` ON `knowledge_injection_events` (`trigger_message_id`);
--> statement-breakpoint
CREATE INDEX `idx_kie_trigger_tool_call` ON `knowledge_injection_events` (`trigger_tool_call_id`);
--> statement-breakpoint
CREATE INDEX `idx_kie_entry_revision` ON `knowledge_injection_events` (`entry_revision_id`);
--> statement-breakpoint
PRAGMA foreign_keys=ON;
