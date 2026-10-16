PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `model_history_revision_contracts` (
	`id` text PRIMARY KEY NOT NULL,
	`installed_version` text NOT NULL
);
--> statement-breakpoint
ALTER TABLE `narrator_messages` ADD `body_revision` integer DEFAULT 0 NOT NULL;
--> statement-breakpoint
ALTER TABLE `narrator_tool_calls` ADD `model_revision` integer DEFAULT 0 NOT NULL;
--> statement-breakpoint
ALTER TABLE `narrators` ADD `model_history_revision` integer DEFAULT 0 NOT NULL;
--> statement-breakpoint
PRAGMA foreign_keys=ON;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_message_insert AFTER INSERT ON narrator_messages BEGIN
UPDATE narrator_messages SET body_revision = NEW.body_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_tool_insert AFTER INSERT ON narrator_tool_calls BEGIN
UPDATE narrator_tool_calls SET model_revision = NEW.model_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_ref_insert AFTER INSERT ON narrator_message_refs BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (NEW.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_message_update AFTER UPDATE ON narrator_messages WHEN OLD.id IS NOT NEW.id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.sdk_message_uuid IS NOT NEW.sdk_message_uuid OR OLD.parent_tool_use_id IS NOT NEW.parent_tool_use_id OR OLD.role IS NOT NEW.role OR OLD.content_json IS NOT NEW.content_json OR OLD.content_text IS NOT NEW.content_text OR OLD.created_by IS NOT NEW.created_by OR OLD.origin IS NOT NEW.origin OR OLD.origin_label IS NOT NEW.origin_label BEGIN
UPDATE narrator_messages SET body_revision = OLD.body_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id, NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_tool_update AFTER UPDATE ON narrator_tool_calls WHEN OLD.id IS NOT NEW.id OR OLD.message_id IS NOT NEW.message_id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.tool_use_id IS NOT NEW.tool_use_id OR OLD.tool_name IS NOT NEW.tool_name OR OLD.input_json IS NOT NEW.input_json OR OLD.output_json IS NOT NEW.output_json OR OLD.status IS NOT NEW.status OR OLD.execution_attempt IS NOT NEW.execution_attempt OR OLD.created_at IS NOT NEW.created_at BEGIN
UPDATE narrator_tool_calls SET model_revision = OLD.model_revision + 1 WHERE id = NEW.id;
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id, NEW.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_ref_update AFTER UPDATE ON narrator_message_refs WHEN OLD.id IS NOT NEW.id OR OLD.narrator_id IS NOT NEW.narrator_id OR OLD.message_id IS NOT NEW.message_id OR OLD.seq IS NOT NEW.seq OR OLD.is_compact IS NOT NEW.is_compact OR OLD.segment_compact_id IS NOT NEW.segment_compact_id BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id, NEW.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_message_delete BEFORE DELETE ON narrator_messages BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_tool_delete BEFORE DELETE ON narrator_tool_calls BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_ref_delete AFTER DELETE ON narrator_message_refs BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id);
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_sender AFTER UPDATE ON users WHEN OLD.username IS NOT NEW.username BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.created_by IN (OLD.id, NEW.id));
END;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_narrator AFTER UPDATE ON narrators WHEN OLD.title IS NOT NEW.title OR OLD.context_summary IS NOT NEW.context_summary BEGIN
UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = NEW.id OR id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.narrator_id = NEW.id);
END;
--> statement-breakpoint
INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', 'model-history-v1');
