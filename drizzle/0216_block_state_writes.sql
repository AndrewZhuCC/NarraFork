PRAGMA foreign_keys=OFF;
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `state_revision` integer DEFAULT 0 NOT NULL;
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
DROP TRIGGER IF EXISTS model_history_v2_message_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_message_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_message_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_tool_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_tool_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_tool_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_ref_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_ref_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_ref_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_block_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_block_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_block_delete;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_tool_phase_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_tool_phase_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_sync_message_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_sync_message_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_sync_tool_insert;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_sync_tool_update;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_sender;
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v2_narrator;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_message_insert AFTER INSERT ON narrator_messages BEGIN
UPDATE narrator_messages SET body_revision = NEW.body_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_tool_insert AFTER INSERT ON narrator_tool_calls BEGIN
UPDATE narrator_tool_calls SET model_revision = NEW.model_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_ref_insert AFTER INSERT ON narrator_message_refs BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (NEW.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_block_insert AFTER INSERT ON narrator_blocks WHEN NEW.phase <> 'open' AND EXISTS (SELECT 1 FROM narrator_messages WHERE id = NEW.message_id AND body_format = 2) BEGIN
UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (NEW.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = NEW.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_message_update AFTER UPDATE ON narrator_messages WHEN (OLD.id IS NOT NEW.id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.sdk_message_uuid IS NOT NEW.sdk_message_uuid OR OLD.parent_tool_use_id IS NOT NEW.parent_tool_use_id OR OLD.role IS NOT NEW.role OR OLD.created_by IS NOT NEW.created_by OR OLD.origin IS NOT NEW.origin OR OLD.origin_label IS NOT NEW.origin_label OR OLD.author_narrator_id IS NOT NEW.author_narrator_id) OR ((OLD.content_json IS NOT NEW.content_json OR OLD.content_text IS NOT NEW.content_text) AND NEW.state_revision = OLD.state_revision) BEGIN
UPDATE narrator_messages SET body_revision = OLD.body_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id, NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_tool_update AFTER UPDATE ON narrator_tool_calls WHEN OLD.id IS NOT NEW.id OR OLD.message_id IS NOT NEW.message_id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.tool_use_id IS NOT NEW.tool_use_id OR OLD.tool_name IS NOT NEW.tool_name OR OLD.input_json IS NOT NEW.input_json OR OLD.output_json IS NOT NEW.output_json OR OLD.status IS NOT NEW.status OR OLD.execution_attempt IS NOT NEW.execution_attempt OR OLD.created_at IS NOT NEW.created_at BEGIN
UPDATE narrator_tool_calls SET model_revision = OLD.model_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id, NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_ref_update AFTER UPDATE ON narrator_message_refs WHEN OLD.id IS NOT NEW.id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.message_id IS NOT NEW.message_id OR OLD.seq IS NOT NEW.seq OR OLD.is_compact IS NOT NEW.is_compact OR OLD.segment_compact_id IS NOT NEW.segment_compact_id BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id, NEW.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_block_update AFTER UPDATE OF payload, model_text, provider_meta, kind, subtype, ordinal, phase, message_id ON narrator_blocks WHEN (OLD.payload IS NOT NEW.payload OR OLD.model_text IS NOT NEW.model_text OR OLD.provider_meta IS NOT NEW.provider_meta OR OLD.kind IS NOT NEW.kind OR OLD.subtype IS NOT NEW.subtype OR OLD.ordinal IS NOT NEW.ordinal OR OLD.phase IS NOT NEW.phase OR OLD.message_id IS NOT NEW.message_id) AND NOT (OLD.phase = 'open' AND NEW.phase = 'open') AND (EXISTS (SELECT 1 FROM narrator_messages WHERE id = OLD.message_id AND body_format = 2) OR EXISTS (SELECT 1 FROM narrator_messages WHERE id = NEW.message_id AND body_format = 2)) BEGIN
UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (OLD.message_id, NEW.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = OLD.message_id), (SELECT narrator_id FROM narrator_messages WHERE id = NEW.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id, NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_message_delete BEFORE DELETE ON narrator_messages BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_tool_delete BEFORE DELETE ON narrator_tool_calls BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_ref_delete AFTER DELETE ON narrator_message_refs BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_block_delete BEFORE DELETE ON narrator_blocks WHEN OLD.phase <> 'open' AND EXISTS (SELECT 1 FROM narrator_messages WHERE id = OLD.message_id AND body_format = 2) BEGIN
UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (OLD.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = OLD.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_tool_phase_insert AFTER INSERT ON narrator_tool_calls WHEN NEW.block_id IS NOT NULL AND NEW.is_internal = 0 AND NEW.execution_attempt >= (SELECT max(execution_attempt) FROM narrator_tool_calls WHERE block_id = NEW.block_id) BEGIN
UPDATE narrator_blocks SET phase = 'open' WHERE id = NEW.block_id AND phase <> 'open';
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_tool_phase_update AFTER UPDATE OF status, output_json ON narrator_tool_calls WHEN NEW.block_id IS NOT NULL AND NEW.is_internal = 0 AND NEW.execution_attempt >= (SELECT max(execution_attempt) FROM narrator_tool_calls WHERE block_id = NEW.block_id) BEGIN
UPDATE narrator_blocks SET phase = CASE WHEN NEW.status IN ('success', 'fail') AND (CASE WHEN json_valid(NEW.output_json) THEN json_extract(NEW.output_json, '$._broken') END) = 1 THEN 'failed' WHEN NEW.status IN ('success', 'fail') THEN 'done' ELSE 'open' END WHERE id = NEW.block_id AND phase <> CASE WHEN NEW.status IN ('success', 'fail') AND (CASE WHEN json_valid(NEW.output_json) THEN json_extract(NEW.output_json, '$._broken') END) = 1 THEN 'failed' WHEN NEW.status IN ('success', 'fail') THEN 'done' ELSE 'open' END;
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_sync_message_insert AFTER INSERT ON narrator_messages WHEN NEW.body_format = 2 BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_sync_message_update AFTER UPDATE OF content_json, content_text, role, origin, body_format ON narrator_messages WHEN NEW.body_format = 2 AND NEW.state_revision = OLD.state_revision BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_sync_tool_insert AFTER INSERT ON narrator_tool_calls WHEN (SELECT body_format FROM narrator_messages WHERE id = NEW.message_id) = 2 BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.message_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_sync_tool_update AFTER UPDATE OF input_json, output_json, status, execution_attempt ON narrator_tool_calls WHEN (SELECT body_format FROM narrator_messages WHERE id = NEW.message_id) = 2 BEGIN
INSERT OR IGNORE INTO narrator_block_sync_queue (message_id) VALUES (NEW.message_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_sender AFTER UPDATE ON users WHEN OLD.username IS NOT NEW.username BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.created_by IN (OLD.id, NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v3_narrator AFTER UPDATE ON narrators WHEN OLD.title IS NOT NEW.title OR OLD.context_summary IS NOT NEW.context_summary BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = NEW.id OR id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.narrator_id = NEW.id OR m.author_narrator_id = NEW.id);
END;
--> statement-breakpoint
INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', 'model-history-v3') ON CONFLICT (id) DO UPDATE SET installed_version = excluded.installed_version;
