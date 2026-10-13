CREATE TABLE "model_history_revision_contracts" (
	"id" text PRIMARY KEY NOT NULL,
	"installed_version" text NOT NULL
);
--> statement-breakpoint
ALTER TABLE "narrator_messages" ADD COLUMN "body_revision" integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE "narrator_tool_calls" ADD COLUMN "model_revision" integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE "narrators" ADD COLUMN "model_history_revision" integer DEFAULT 0 NOT NULL;
--> statement-breakpoint
CREATE FUNCTION model_history_v1_message() RETURNS trigger LANGUAGE plpgsql AS $model_history$
BEGIN

		IF TG_OP = 'DELETE' THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id)); RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			NEW.body_revision := NEW.body_revision + 1;
			UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.id)); RETURN NEW;
		ELSIF OLD.id IS DISTINCT FROM NEW.id OR OLD.narrator_id IS DISTINCT FROM NEW.narrator_id OR OLD.sdk_message_uuid IS DISTINCT FROM NEW.sdk_message_uuid OR OLD.parent_tool_use_id IS DISTINCT FROM NEW.parent_tool_use_id OR OLD.role IS DISTINCT FROM NEW.role OR OLD.content_json IS DISTINCT FROM NEW.content_json OR OLD.content_text IS DISTINCT FROM NEW.content_text OR OLD.created_by IS DISTINCT FROM NEW.created_by OR OLD.origin IS DISTINCT FROM NEW.origin OR OLD.origin_label IS DISTINCT FROM NEW.origin_label THEN
			NEW.body_revision := OLD.body_revision + 1;
			UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id, NEW.id));
		END IF;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_message BEFORE INSERT OR UPDATE OR DELETE ON narrator_messages FOR EACH ROW EXECUTE FUNCTION model_history_v1_message();
--> statement-breakpoint
CREATE FUNCTION model_history_v1_tool() RETURNS trigger LANGUAGE plpgsql AS $model_history$
BEGIN

		IF TG_OP = 'DELETE' THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id)); RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			NEW.model_revision := NEW.model_revision + 1;
			UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.message_id)); RETURN NEW;
		ELSIF OLD.id IS DISTINCT FROM NEW.id OR OLD.message_id IS DISTINCT FROM NEW.message_id OR OLD.narrator_id IS DISTINCT FROM NEW.narrator_id OR OLD.tool_use_id IS DISTINCT FROM NEW.tool_use_id OR OLD.tool_name IS DISTINCT FROM NEW.tool_name OR OLD.input_json IS DISTINCT FROM NEW.input_json OR OLD.output_json IS DISTINCT FROM NEW.output_json OR OLD.status IS DISTINCT FROM NEW.status OR OLD.execution_attempt IS DISTINCT FROM NEW.execution_attempt OR OLD.created_at IS DISTINCT FROM NEW.created_at THEN
			NEW.model_revision := OLD.model_revision + 1;
			UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id, NEW.message_id));
		END IF;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_tool BEFORE INSERT OR UPDATE OR DELETE ON narrator_tool_calls FOR EACH ROW EXECUTE FUNCTION model_history_v1_tool();
--> statement-breakpoint
CREATE FUNCTION model_history_v1_ref() RETURNS trigger LANGUAGE plpgsql AS $model_history$
BEGIN

		IF TG_OP = 'DELETE' THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = OLD.narrator_id; RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id = NEW.narrator_id;
		ELSIF OLD.id IS DISTINCT FROM NEW.id OR OLD.narrator_id IS DISTINCT FROM NEW.narrator_id OR OLD.message_id IS DISTINCT FROM NEW.message_id OR OLD.seq IS DISTINCT FROM NEW.seq OR OLD.is_compact IS DISTINCT FROM NEW.is_compact OR OLD.segment_compact_id IS DISTINCT FROM NEW.segment_compact_id THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (OLD.narrator_id, NEW.narrator_id);
		END IF;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_ref AFTER INSERT OR UPDATE OR DELETE ON narrator_message_refs FOR EACH ROW EXECUTE FUNCTION model_history_v1_ref();
--> statement-breakpoint
CREATE FUNCTION model_history_v1_sender() RETURNS trigger LANGUAGE plpgsql AS $model_history$
BEGIN

		IF OLD.username IS DISTINCT FROM NEW.username THEN
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.created_by IN (OLD.id, NEW.id));
		END IF;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_sender AFTER UPDATE ON users FOR EACH ROW EXECUTE FUNCTION model_history_v1_sender();
--> statement-breakpoint
CREATE FUNCTION model_history_v1_narrator() RETURNS trigger LANGUAGE plpgsql AS $model_history$
BEGIN

		IF OLD.title IS DISTINCT FROM NEW.title OR OLD.context_summary IS DISTINCT FROM NEW.context_summary THEN
			NEW.model_history_revision := OLD.model_history_revision + 1;
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id <> NEW.id AND id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.narrator_id = NEW.id);
		END IF;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v1_narrator BEFORE UPDATE ON narrators FOR EACH ROW EXECUTE FUNCTION model_history_v1_narrator();
--> statement-breakpoint
INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', 'model-history-v1');
