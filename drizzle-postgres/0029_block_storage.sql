CREATE TABLE "narrator_block_assets" (
	"block_id" text NOT NULL,
	"asset_id" text NOT NULL,
	"role" text NOT NULL,
	CONSTRAINT "narrator_block_assets_block_id_asset_id_pk" PRIMARY KEY("block_id","asset_id"),
	CONSTRAINT "ck_block_assets_role" CHECK ("role" in ('input_image', 'input_file', 'output_image', 'tool_image', 'epoch_snapshot'))
);
--> statement-breakpoint
CREATE TABLE "narrator_block_sync_queue" (
	"message_id" text PRIMARY KEY NOT NULL
);
--> statement-breakpoint
CREATE TABLE "narrator_blocks" (
	"id" text PRIMARY KEY NOT NULL,
	"message_id" text NOT NULL,
	"ordinal" integer NOT NULL,
	"kind" text NOT NULL,
	"subtype" text,
	"phase" text DEFAULT 'done' NOT NULL,
	"revision" integer DEFAULT 0 NOT NULL,
	"wire_id" text,
	"attempt_id" text,
	"origin_block_id" text,
	"model_chars" integer DEFAULT 0 NOT NULL,
	"byte_size" integer DEFAULT 0 NOT NULL,
	"created_at" text NOT NULL,
	"completed_at" text,
	"model_text" text,
	"payload" text NOT NULL,
	"state" text,
	"provider_meta" text,
	"original_payload" text,
	CONSTRAINT "ck_blocks_kind" CHECK ("kind" in ('input', 'output', 'reasoning', 'tool', 'context')),
	CONSTRAINT "ck_blocks_phase" CHECK ("phase" in ('open', 'done', 'aborted', 'failed')),
	CONSTRAINT "ck_blocks_subtype" CHECK (("subtype" is not null) = ("kind" in ('tool', 'context'))),
	CONSTRAINT "ck_blocks_aborted" CHECK ("phase" <> 'aborted' or "kind" in ('output', 'reasoning')),
	CONSTRAINT "ck_blocks_input_done" CHECK ("phase" = 'done' or "kind" <> 'input'),
	CONSTRAINT "ck_blocks_model_text" CHECK ("model_text" is null or "kind" in ('input', 'context')),
	CONSTRAINT "ck_blocks_provider_meta" CHECK ("provider_meta" is null or "kind" in ('output', 'reasoning', 'tool')),
	CONSTRAINT "ck_blocks_wire_id" CHECK (("wire_id" is not null) = ("kind" = 'tool')),
	CONSTRAINT "ck_blocks_original_payload" CHECK ("original_payload" is null or "kind" = 'output')
);
--> statement-breakpoint
CREATE TABLE "narrator_text_snapshots" (
	"sha256" text PRIMARY KEY NOT NULL,
	"text" text NOT NULL,
	"byte_size" integer NOT NULL,
	"created_at" text NOT NULL
);
--> statement-breakpoint
CREATE TABLE "narrator_turns" (
	"id" text PRIMARY KEY NOT NULL,
	"narrator_id" text NOT NULL,
	"status" text NOT NULL,
	"trigger_message_id" text,
	"started_at" text NOT NULL,
	"ended_at" text,
	"stop_reason" text,
	"step_count" integer DEFAULT 0 NOT NULL,
	"tool_call_count" integer DEFAULT 0 NOT NULL,
	"usage_json" text,
	CONSTRAINT "ck_turns_status" CHECK ("status" in ('running', 'completed', 'interrupted', 'failed'))
);
--> statement-breakpoint
ALTER TABLE "narrator_messages" ADD COLUMN "body_format" integer DEFAULT 1 NOT NULL;--> statement-breakpoint
ALTER TABLE "narrator_messages" ADD COLUMN "turn_id" text;--> statement-breakpoint
ALTER TABLE "narrator_messages" ADD COLUMN "author_narrator_id" text;--> statement-breakpoint
ALTER TABLE "narrator_messages" ADD COLUMN "cause_block_id" text;--> statement-breakpoint
ALTER TABLE "narrator_messages" ADD COLUMN "provider_response_id" text;--> statement-breakpoint
ALTER TABLE "narrator_messages" ADD COLUMN "body_bytes" integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE "narrator_tool_calls" ADD COLUMN "block_id" text;--> statement-breakpoint
ALTER TABLE "narrator_tool_calls" ADD COLUMN "is_internal" boolean DEFAULT false NOT NULL;--> statement-breakpoint
ALTER TABLE "narrator_block_assets" ADD CONSTRAINT "narrator_block_assets_block_id_narrator_blocks_id_fk" FOREIGN KEY ("block_id") REFERENCES "public"."narrator_blocks"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "narrator_block_sync_queue" ADD CONSTRAINT "narrator_block_sync_queue_message_id_narrator_messages_id_fk" FOREIGN KEY ("message_id") REFERENCES "public"."narrator_messages"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "narrator_blocks" ADD CONSTRAINT "narrator_blocks_message_id_narrator_messages_id_fk" FOREIGN KEY ("message_id") REFERENCES "public"."narrator_messages"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "narrator_turns" ADD CONSTRAINT "narrator_turns_narrator_id_narrators_id_fk" FOREIGN KEY ("narrator_id") REFERENCES "public"."narrators"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "idx_block_assets_asset" ON "narrator_block_assets" USING btree ("asset_id");--> statement-breakpoint
CREATE UNIQUE INDEX "idx_blocks_message_ordinal" ON "narrator_blocks" USING btree ("message_id","ordinal");--> statement-breakpoint
CREATE UNIQUE INDEX "idx_blocks_single" ON "narrator_blocks" USING btree ("message_id") WHERE "kind" in ('input', 'context');--> statement-breakpoint
CREATE INDEX "idx_blocks_open" ON "narrator_blocks" USING btree ("message_id") WHERE "phase" = 'open';--> statement-breakpoint
CREATE INDEX "idx_blocks_origin" ON "narrator_blocks" USING btree ("origin_block_id") WHERE "origin_block_id" is not null;--> statement-breakpoint
CREATE INDEX "idx_blocks_context_subtype" ON "narrator_blocks" USING btree ("subtype") WHERE "kind" = 'context';--> statement-breakpoint
CREATE INDEX "idx_blocks_tool_wire" ON "narrator_blocks" USING btree ("wire_id") WHERE "kind" = 'tool';--> statement-breakpoint
CREATE INDEX "idx_turns_narrator_started" ON "narrator_turns" USING btree ("narrator_id","started_at");--> statement-breakpoint
ALTER TABLE "narrator_tool_calls" ADD CONSTRAINT "narrator_tool_calls_block_id_narrator_blocks_id_fk" FOREIGN KEY ("block_id") REFERENCES "public"."narrator_blocks"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "idx_messages_author" ON "narrator_messages" USING btree ("author_narrator_id") WHERE "author_narrator_id" IS NOT NULL;--> statement-breakpoint
CREATE INDEX "idx_toolcalls_block" ON "narrator_tool_calls" USING btree ("block_id");
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_message ON narrator_messages;
--> statement-breakpoint
DROP FUNCTION IF EXISTS model_history_v1_message();
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_tool ON narrator_tool_calls;
--> statement-breakpoint
DROP FUNCTION IF EXISTS model_history_v1_tool();
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_ref ON narrator_message_refs;
--> statement-breakpoint
DROP FUNCTION IF EXISTS model_history_v1_ref();
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_sender ON users;
--> statement-breakpoint
DROP FUNCTION IF EXISTS model_history_v1_sender();
--> statement-breakpoint
DROP TRIGGER IF EXISTS model_history_v1_narrator ON narrators;
--> statement-breakpoint
DROP FUNCTION IF EXISTS model_history_v1_narrator();
--> statement-breakpoint
CREATE FUNCTION model_history_v2_message() RETURNS trigger LANGUAGE plpgsql AS $model_history$
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
		ELSIF OLD.id IS DISTINCT FROM NEW.id OR OLD.narrator_id IS DISTINCT FROM NEW.narrator_id OR OLD.sdk_message_uuid IS DISTINCT FROM NEW.sdk_message_uuid OR OLD.parent_tool_use_id IS DISTINCT FROM NEW.parent_tool_use_id OR OLD.role IS DISTINCT FROM NEW.role OR OLD.created_by IS DISTINCT FROM NEW.created_by OR OLD.origin IS DISTINCT FROM NEW.origin OR OLD.origin_label IS DISTINCT FROM NEW.origin_label OR OLD.author_narrator_id IS DISTINCT FROM NEW.author_narrator_id OR OLD.content_json IS DISTINCT FROM NEW.content_json OR OLD.content_text IS DISTINCT FROM NEW.content_text THEN
			NEW.body_revision := OLD.body_revision + 1;
			UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN (OLD.narrator_id, NEW.narrator_id) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.id, NEW.id));
		END IF;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_message BEFORE INSERT OR UPDATE OR DELETE ON narrator_messages FOR EACH ROW EXECUTE FUNCTION model_history_v2_message();
--> statement-breakpoint
CREATE FUNCTION model_history_v2_tool() RETURNS trigger LANGUAGE plpgsql AS $model_history$
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
CREATE TRIGGER model_history_v2_tool BEFORE INSERT OR UPDATE OR DELETE ON narrator_tool_calls FOR EACH ROW EXECUTE FUNCTION model_history_v2_tool();
--> statement-breakpoint
CREATE FUNCTION model_history_v2_ref() RETURNS trigger LANGUAGE plpgsql AS $model_history$
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
CREATE TRIGGER model_history_v2_ref AFTER INSERT OR UPDATE OR DELETE ON narrator_message_refs FOR EACH ROW EXECUTE FUNCTION model_history_v2_ref();
--> statement-breakpoint
CREATE FUNCTION model_history_v2_block() RETURNS trigger LANGUAGE plpgsql AS $model_history$
BEGIN

		IF TG_OP = 'DELETE' THEN
			IF OLD.phase <> 'open' AND EXISTS (SELECT 1 FROM narrator_messages WHERE id = OLD.message_id AND body_format = 2) THEN
				UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (OLD.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = OLD.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id));
			END IF;
			RETURN OLD;
		ELSIF TG_OP = 'INSERT' THEN
			IF NEW.phase <> 'open' AND EXISTS (SELECT 1 FROM narrator_messages WHERE id = NEW.message_id AND body_format = 2) THEN
				UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (NEW.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = NEW.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (NEW.message_id));
			END IF;
		ELSIF (OLD.payload IS DISTINCT FROM NEW.payload OR OLD.model_text IS DISTINCT FROM NEW.model_text OR OLD.provider_meta IS DISTINCT FROM NEW.provider_meta OR OLD.kind IS DISTINCT FROM NEW.kind OR OLD.subtype IS DISTINCT FROM NEW.subtype OR OLD.ordinal IS DISTINCT FROM NEW.ordinal OR OLD.phase IS DISTINCT FROM NEW.phase OR OLD.message_id IS DISTINCT FROM NEW.message_id) AND NOT (OLD.phase = 'open' AND NEW.phase = 'open') AND (EXISTS (SELECT 1 FROM narrator_messages WHERE id = OLD.message_id AND body_format = 2) OR EXISTS (SELECT 1 FROM narrator_messages WHERE id = NEW.message_id AND body_format = 2)) THEN
			UPDATE narrator_messages SET body_revision = body_revision + 1 WHERE id IN (OLD.message_id, NEW.message_id);
UPDATE narrators SET model_history_revision = model_history_revision + 1
		WHERE id IN ((SELECT narrator_id FROM narrator_messages WHERE id = OLD.message_id), (SELECT narrator_id FROM narrator_messages WHERE id = NEW.message_id)) OR id IN
		(SELECT narrator_id FROM narrator_message_refs WHERE message_id IN (OLD.message_id, NEW.message_id));
		END IF;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_block AFTER INSERT OR UPDATE OR DELETE ON narrator_blocks FOR EACH ROW EXECUTE FUNCTION model_history_v2_block();
--> statement-breakpoint
CREATE FUNCTION model_history_v2_tool_phase() RETURNS trigger LANGUAGE plpgsql AS $model_history$
DECLARE
	target_phase text;
BEGIN

		IF NEW.block_id IS NULL OR NEW.is_internal OR NEW.execution_attempt <
			(SELECT max(execution_attempt) FROM narrator_tool_calls WHERE block_id = NEW.block_id) THEN
			RETURN NEW;
		END IF;
		IF TG_OP = 'INSERT' OR NEW.status NOT IN ('success', 'fail') THEN
			target_phase := 'open';
		ELSE
			target_phase := 'done';
			BEGIN
				IF (NEW.output_json::jsonb ->> '_broken') = 'true' THEN target_phase := 'failed'; END IF;
			EXCEPTION WHEN others THEN
				NULL;
			END;
		END IF;
		UPDATE narrator_blocks SET phase = target_phase WHERE id = NEW.block_id AND phase <> target_phase;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_tool_phase AFTER INSERT OR UPDATE OF status, output_json ON narrator_tool_calls FOR EACH ROW EXECUTE FUNCTION model_history_v2_tool_phase();
--> statement-breakpoint
CREATE FUNCTION model_history_v2_sync_message() RETURNS trigger LANGUAGE plpgsql AS $model_history$
BEGIN

		IF NEW.body_format = 2 THEN
			INSERT INTO narrator_block_sync_queue (message_id) VALUES (NEW.id) ON CONFLICT DO NOTHING;
		END IF;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_sync_message AFTER INSERT OR UPDATE OF content_json, content_text, role, origin, body_format ON narrator_messages FOR EACH ROW EXECUTE FUNCTION model_history_v2_sync_message();
--> statement-breakpoint
CREATE FUNCTION model_history_v2_sync_tool() RETURNS trigger LANGUAGE plpgsql AS $model_history$
BEGIN

		IF EXISTS (SELECT 1 FROM narrator_messages WHERE id = NEW.message_id AND body_format = 2) THEN
			INSERT INTO narrator_block_sync_queue (message_id) VALUES (NEW.message_id) ON CONFLICT DO NOTHING;
		END IF;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_sync_tool AFTER INSERT OR UPDATE OF input_json, output_json, status, execution_attempt ON narrator_tool_calls FOR EACH ROW EXECUTE FUNCTION model_history_v2_sync_tool();
--> statement-breakpoint
CREATE FUNCTION model_history_v2_sender() RETURNS trigger LANGUAGE plpgsql AS $model_history$
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
CREATE TRIGGER model_history_v2_sender AFTER UPDATE ON users FOR EACH ROW EXECUTE FUNCTION model_history_v2_sender();
--> statement-breakpoint
CREATE FUNCTION model_history_v2_narrator() RETURNS trigger LANGUAGE plpgsql AS $model_history$
BEGIN

		IF OLD.title IS DISTINCT FROM NEW.title OR OLD.context_summary IS DISTINCT FROM NEW.context_summary THEN
			NEW.model_history_revision := OLD.model_history_revision + 1;
			UPDATE narrators SET model_history_revision = model_history_revision + 1 WHERE id <> NEW.id AND id IN (
			 SELECT r.narrator_id FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE m.narrator_id = NEW.id OR m.author_narrator_id = NEW.id);
		END IF;
		RETURN NEW;
END;
$model_history$;
--> statement-breakpoint
CREATE TRIGGER model_history_v2_narrator BEFORE UPDATE ON narrators FOR EACH ROW EXECUTE FUNCTION model_history_v2_narrator();
--> statement-breakpoint
INSERT INTO model_history_revision_contracts (id, installed_version) VALUES ('model-history', 'model-history-v2') ON CONFLICT (id) DO UPDATE SET installed_version = excluded.installed_version;
