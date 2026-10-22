PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `narrator_block_assets` (
	`block_id` text NOT NULL,
	`asset_id` text NOT NULL,
	`role` text NOT NULL,
	PRIMARY KEY(`block_id`, `asset_id`),
	FOREIGN KEY (`block_id`) REFERENCES `narrator_blocks`(`id`) ON UPDATE no action ON DELETE cascade,
	CONSTRAINT "ck_block_assets_role" CHECK("narrator_block_assets"."role" in ('input_image', 'input_file', 'output_image', 'tool_image', 'epoch_snapshot'))
);
--> statement-breakpoint
CREATE INDEX `idx_block_assets_asset` ON `narrator_block_assets` (`asset_id`);
--> statement-breakpoint
CREATE TABLE `narrator_block_sync_queue` (
	`message_id` text PRIMARY KEY NOT NULL,
	FOREIGN KEY (`message_id`) REFERENCES `narrator_messages`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE TABLE `narrator_blocks` (
	`id` text PRIMARY KEY NOT NULL,
	`message_id` text NOT NULL,
	`ordinal` integer NOT NULL,
	`kind` text NOT NULL,
	`subtype` text,
	`phase` text DEFAULT 'done' NOT NULL,
	`revision` integer DEFAULT 0 NOT NULL,
	`wire_id` text,
	`attempt_id` text,
	`origin_block_id` text,
	`model_chars` integer DEFAULT 0 NOT NULL,
	`byte_size` integer DEFAULT 0 NOT NULL,
	`created_at` text NOT NULL,
	`completed_at` text,
	`model_text` text,
	`payload` text NOT NULL,
	`state` text,
	`provider_meta` text,
	`original_payload` text,
	FOREIGN KEY (`message_id`) REFERENCES `narrator_messages`(`id`) ON UPDATE no action ON DELETE cascade,
	CONSTRAINT "ck_blocks_kind" CHECK("narrator_blocks"."kind" in ('input', 'output', 'reasoning', 'tool', 'context')),
	CONSTRAINT "ck_blocks_phase" CHECK("narrator_blocks"."phase" in ('open', 'done', 'aborted', 'failed')),
	CONSTRAINT "ck_blocks_subtype" CHECK(("narrator_blocks"."subtype" is not null) = ("narrator_blocks"."kind" in ('tool', 'context'))),
	CONSTRAINT "ck_blocks_aborted" CHECK("narrator_blocks"."phase" <> 'aborted' or "narrator_blocks"."kind" in ('output', 'reasoning')),
	CONSTRAINT "ck_blocks_input_done" CHECK("narrator_blocks"."phase" = 'done' or "narrator_blocks"."kind" <> 'input'),
	CONSTRAINT "ck_blocks_model_text" CHECK("narrator_blocks"."model_text" is null or "narrator_blocks"."kind" in ('input', 'context')),
	CONSTRAINT "ck_blocks_provider_meta" CHECK("narrator_blocks"."provider_meta" is null or "narrator_blocks"."kind" in ('output', 'reasoning', 'tool')),
	CONSTRAINT "ck_blocks_wire_id" CHECK(("narrator_blocks"."wire_id" is not null) = ("narrator_blocks"."kind" = 'tool')),
	CONSTRAINT "ck_blocks_original_payload" CHECK("narrator_blocks"."original_payload" is null or "narrator_blocks"."kind" = 'output')
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_blocks_message_ordinal` ON `narrator_blocks` (`message_id`,`ordinal`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_blocks_single` ON `narrator_blocks` (`message_id`) WHERE "narrator_blocks"."kind" in ('input', 'context');
--> statement-breakpoint
CREATE INDEX `idx_blocks_open` ON `narrator_blocks` (`message_id`) WHERE "narrator_blocks"."phase" = 'open';
--> statement-breakpoint
CREATE INDEX `idx_blocks_origin` ON `narrator_blocks` (`origin_block_id`) WHERE "narrator_blocks"."origin_block_id" is not null;
--> statement-breakpoint
CREATE INDEX `idx_blocks_context_subtype` ON `narrator_blocks` (`subtype`) WHERE "narrator_blocks"."kind" = 'context';
--> statement-breakpoint
CREATE INDEX `idx_blocks_tool_wire` ON `narrator_blocks` (`wire_id`) WHERE "narrator_blocks"."kind" = 'tool';
--> statement-breakpoint
CREATE TABLE `narrator_text_snapshots` (
	`sha256` text PRIMARY KEY NOT NULL,
	`text` text NOT NULL,
	`byte_size` integer NOT NULL,
	`created_at` text NOT NULL
);
--> statement-breakpoint
CREATE TABLE `narrator_turns` (
	`id` text PRIMARY KEY NOT NULL,
	`narrator_id` text NOT NULL,
	`status` text NOT NULL,
	`trigger_message_id` text,
	`started_at` text NOT NULL,
	`ended_at` text,
	`stop_reason` text,
	`step_count` integer DEFAULT 0 NOT NULL,
	`tool_call_count` integer DEFAULT 0 NOT NULL,
	`usage_json` text,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	CONSTRAINT "ck_turns_status" CHECK("narrator_turns"."status" in ('running', 'completed', 'interrupted', 'failed'))
);
--> statement-breakpoint
CREATE INDEX `idx_turns_narrator_started` ON `narrator_turns` (`narrator_id`,`started_at`);
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `body_format` integer DEFAULT 1 NOT NULL;
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `turn_id` text;
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `author_narrator_id` text;
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `cause_block_id` text;
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `provider_response_id` text;
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `body_bytes` integer DEFAULT 0 NOT NULL;
--> statement-breakpoint
CREATE INDEX `idx_messages_author` ON `narrator_messages` (`author_narrator_id`) WHERE "author_narrator_id" IS NOT NULL;
--> statement-breakpoint
ALTER TABLE `narrator_tool_calls` ADD `block_id` text REFERENCES narrator_blocks(id) ON UPDATE no action ON DELETE set null;
--> statement-breakpoint
ALTER TABLE `narrator_tool_calls` ADD `is_internal` integer DEFAULT false NOT NULL;
--> statement-breakpoint
CREATE INDEX `idx_toolcalls_block` ON `narrator_tool_calls` (`block_id`);
--> statement-breakpoint
PRAGMA foreign_keys=ON;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_message_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_message_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_message_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_tool_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_tool_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_tool_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_ref_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_ref_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_ref_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_sender;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_narrator;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_message_insert AFTER INSERT ON narrator_messages BEGIN
UPDATE narrator_messages SET body_revision = NEW.body_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_tool_insert AFTER INSERT ON narrator_tool_calls BEGIN
UPDATE narrator_tool_calls SET model_revision = NEW.model_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_ref_insert AFTER INSERT ON narrator_message_refs BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (NEW.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_block_insert AFTER INSERT ON narrator_blocks WHEN NEW.phase <> 'open' AND EXISTS (SELECT 1 FROM narrator_messages WHERE id = NEW.message_id AND body_format = 2) BEGIN
UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (NEW.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = NEW.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_message_update AFTER UPDATE ON narrator_messages WHEN OLD.id IS NOT NEW.id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.sdk_message_uuid IS NOT NEW.sdk_message_uuid OR OLD.parent_tool_use_id IS NOT NEW.parent_tool_use_id OR OLD.role IS NOT NEW.role OR OLD.created_by IS NOT NEW.created_by OR OLD.origin IS NOT NEW.origin OR OLD.origin_label IS NOT NEW.origin_label OR OLD.author_narrator_id IS NOT NEW.author_narrator_id OR OLD.content_json IS NOT NEW.content_json OR OLD.content_text IS NOT NEW.content_text BEGIN
UPDATE narrator_messages SET body_revision = OLD.body_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id, NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_tool_update AFTER UPDATE ON narrator_tool_calls WHEN OLD.id IS NOT NEW.id OR OLD.message_id IS NOT NEW.message_id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.tool_use_id IS NOT NEW.tool_use_id OR OLD.tool_name IS NOT NEW.tool_name OR OLD.input_json IS NOT NEW.input_json OR OLD.output_json IS NOT NEW.output_json OR OLD.status IS NOT NEW.status OR OLD.execution_attempt IS NOT NEW.execution_attempt OR OLD.created_at IS NOT NEW.created_at BEGIN
UPDATE narrator_tool_calls SET model_revision = OLD.model_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id, NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_ref_update AFTER UPDATE ON narrator_message_refs WHEN OLD.id IS NOT NEW.id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.message_id IS NOT NEW.message_id OR OLD.seq IS NOT NEW.seq OR OLD.is_compact IS NOT NEW.is_compact OR OLD.segment_compact_id IS NOT NEW.segment_compact_id BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id, NEW.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_block_update AFTER UPDATE OF payload, model_text, provider_meta, kind, subtype, ordinal, phase, message_id ON narrator_blocks WHEN (OLD.payload IS NOT NEW.payload OR OLD.model_text IS NOT NEW.model_text OR OLD.provider_meta IS NOT NEW.provider_meta OR OLD.kind IS NOT NEW.kind OR OLD.subtype IS NOT NEW.subtype OR OLD.ordinal IS NOT NEW.ordinal OR OLD.phase IS NOT NEW.phase OR OLD.message_id IS NOT NEW.message_id) AND NOT (OLD.phase = 'open' AND NEW.phase = 'open') AND (EXISTS (SELECT 1 FROM narrator_messages WHERE id = OLD.message_id AND body_format = 2) OR EXISTS (SELECT 1 FROM narrator_messages WHERE id = NEW.message_id AND body_format = 2)) BEGIN
UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (OLD.message_id, NEW.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = OLD.message_id), (SELECT narrator_id FROM narrator_messages WHERE id = NEW.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id, NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_message_delete BEFORE DELETE ON narrator_messages BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_tool_delete BEFORE DELETE ON narrator_tool_calls BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_ref_delete AFTER DELETE ON narrator_message_refs BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_block_delete BEFORE DELETE ON narrator_blocks WHEN OLD.phase <> 'open' AND EXISTS (SELECT 1 FROM narrator_messages WHERE id = OLD.message_id AND body_format = 2) BEGIN
UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (OLD.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = OLD.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_tool_phase_insert AFTER INSERT ON narrator_tool_calls WHEN NEW.block_id IS NOT NULL AND NEW.is_internal = 0 AND NEW.execution_attempt >= (SELECT max(execution_attempt) FROM narrator_tool_calls WHERE block_id = NEW.block_id) BEGIN
UPDATE narrator_blocks SET phase = 'open' WHERE id = NEW.block_id AND phase <> 'open';
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_tool_phase_update AFTER UPDATE OF status, output_json ON narrator_tool_calls WHEN NEW.block_id IS NOT NULL AND NEW.is_internal = 0 AND NEW.execution_attempt >= (SELECT max(execution_attempt) FROM narrator_tool_calls WHERE block_id = NEW.block_id) BEGIN
UPDATE narrator_blocks SET phase = CASE WHEN NEW.status IN ('success', 'fail') AND (CASE WHEN json_valid(NEW.output_json) THEN json_extract(NEW.output_json, '$._broken') END) = 1 THEN 'failed' WHEN NEW.status IN ('success', 'fail') THEN 'done' ELSE 'open' END WHERE id = NEW.block_id AND phase <> CASE WHEN NEW.status IN ('success', 'fail') AND (CASE WHEN json_valid(NEW.output_json) THEN json_extract(NEW.output_json, '$._broken') END) = 1 THEN 'failed' WHEN NEW.status IN ('success', 'fail') THEN 'done' ELSE 'open' END;
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_sync_message_insert AFTER INSERT ON narrator_messages WHEN NEW.body_format = 2 BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_sync_message_update AFTER UPDATE OF content_json, content_text, role, origin, body_format ON narrator_messages WHEN NEW.body_format = 2 BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_sync_tool_insert AFTER INSERT ON narrator_tool_calls WHEN (SELECT body_format FROM narrator_messages WHERE id = NEW.message_id) = 2 BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.message_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_sync_tool_update AFTER UPDATE OF input_json, output_json, status, execution_attempt ON narrator_tool_calls WHEN (SELECT body_format FROM narrator_messages WHERE id = NEW.message_id) = 2 BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.message_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_sender AFTER UPDATE ON users WHEN OLD.username IS NOT NEW.username BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.created_by IN (OLD.id, NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_narrator AFTER UPDATE ON narrators WHEN OLD.title IS NOT NEW.title OR OLD.context_summary IS NOT NEW.context_summary BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = NEW.id OR id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.narrator_id = NEW.id OR m.author_narrator_id = NEW.id);
END;
--> statement-breakpoint
INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', 'model-history-v2') ON CONFLICT (id) DO UPDATE SET installed_version = excluded.installed_version;
