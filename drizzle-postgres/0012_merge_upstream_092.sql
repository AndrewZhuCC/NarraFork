CREATE TABLE "model_history_revision_contracts" (
	"id" text PRIMARY KEY NOT NULL,
	"installed_version" text NOT NULL
);
--> statement-breakpoint
CREATE TABLE "resource_protection_paths" (
	"source" text NOT NULL,
	"source_id" text NOT NULL,
	"slot" integer NOT NULL,
	"device_id" text NOT NULL,
	"canonical_path" text,
	"repository_key" text,
	"shadow_key" text,
	"scope_project_id" text,
	"alias_sensitive" integer DEFAULT 0 NOT NULL,
	"claim_json" text NOT NULL,
	CONSTRAINT "resource_protection_paths_source_source_id_slot_pk" PRIMARY KEY("source","source_id","slot")
);
--> statement-breakpoint
CREATE TABLE "resource_protection_sources" (
	"source" text NOT NULL,
	"source_id" text NOT NULL,
	"revision" integer DEFAULT 1 NOT NULL,
	"dirty" integer DEFAULT 1 NOT NULL,
	"cursor" text,
	"complete" integer DEFAULT 0 NOT NULL,
	CONSTRAINT "resource_protection_sources_source_source_id_pk" PRIMARY KEY("source","source_id")
);
--> statement-breakpoint
CREATE TABLE "usage_token_repair_candidates" (
	"id" text PRIMARY KEY NOT NULL,
	"job_id" text NOT NULL,
	"request_id" text NOT NULL,
	"created_at" text NOT NULL,
	"provider" text,
	"model" text,
	"snapshot_json" text NOT NULL,
	"evidence_json" text NOT NULL,
	"applied_at" text,
	"applied_by" text,
	"totals_status" text DEFAULT 'unchanged' NOT NULL,
	CONSTRAINT "token_repair_snapshot_bound" CHECK (length("snapshot_json") <= 8192),
	CONSTRAINT "token_repair_evidence_bound" CHECK (length("evidence_json") <= 16384)
);
--> statement-breakpoint
CREATE TABLE "usage_token_repair_jobs" (
	"id" text PRIMARY KEY NOT NULL,
	"status" text NOT NULL,
	"created_by" text NOT NULL,
	"created_at" text NOT NULL,
	"updated_at" text NOT NULL,
	"owner_id" text,
	"lease_until" text,
	"scanned" integer DEFAULT 0 NOT NULL,
	"repaired" integer DEFAULT 0 NOT NULL,
	"conflicts" integer DEFAULT 0 NOT NULL,
	"upper_created_at" text,
	"upper_id" text,
	"cursor_created_at" text,
	"cursor_id" text,
	"include_reviewed" boolean NOT NULL,
	"protocol_overrides_json" text DEFAULT '[]' NOT NULL,
	"source_job_id" text,
	"error" text,
	CONSTRAINT "token_repair_protocol_config_bound" CHECK (length("protocol_overrides_json") <= 16384),
	CONSTRAINT "token_repair_error_bound" CHECK ("error" IS NULL OR length("error") <= 2048)
);
--> statement-breakpoint
ALTER TABLE "knowledge_injection_events" ALTER COLUMN "entry_id" DROP NOT NULL;--> statement-breakpoint
ALTER TABLE "api_requests" ADD COLUMN "token_accounting_version" integer;--> statement-breakpoint
ALTER TABLE "api_requests" ADD COLUMN "token_accounting_review_version" integer;--> statement-breakpoint
ALTER TABLE "knowledge_injection_events" ADD COLUMN "personal_entry_id" text;--> statement-breakpoint
ALTER TABLE "narrator_messages" ADD COLUMN "body_revision" integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE "narrator_tool_calls" ADD COLUMN "model_revision" integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE "narrators" ADD COLUMN "model_history_revision" integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE "user_preferences" ADD COLUMN "spec_tab_layout" text DEFAULT '{}' NOT NULL;--> statement-breakpoint
CREATE INDEX "idx_protection_path" ON "resource_protection_paths" USING btree ("device_id","canonical_path","source","source_id","slot");--> statement-breakpoint
CREATE INDEX "idx_protection_repository" ON "resource_protection_paths" USING btree ("device_id","repository_key","source","source_id","slot");--> statement-breakpoint
CREATE INDEX "idx_protection_shadow" ON "resource_protection_paths" USING btree ("shadow_key","source","source_id","slot");--> statement-breakpoint
CREATE INDEX "idx_protection_scope" ON "resource_protection_paths" USING btree ("scope_project_id","source","source_id","slot");--> statement-breakpoint
CREATE INDEX "idx_protection_alias" ON "resource_protection_paths" USING btree ("alias_sensitive","source","source_id","slot");--> statement-breakpoint
CREATE INDEX "idx_protection_dirty" ON "resource_protection_sources" USING btree ("dirty","source","source_id");--> statement-breakpoint
CREATE UNIQUE INDEX "idx_token_repair_candidate_request" ON "usage_token_repair_candidates" USING btree ("job_id","request_id");--> statement-breakpoint
CREATE INDEX "idx_token_repair_candidates_page" ON "usage_token_repair_candidates" USING btree ("job_id","id");--> statement-breakpoint
CREATE INDEX "idx_token_repair_jobs_created" ON "usage_token_repair_jobs" USING btree ("created_at","id");--> statement-breakpoint
CREATE UNIQUE INDEX "idx_token_repair_single_active" ON "usage_token_repair_jobs" USING btree ((1)) WHERE "status" IN ('analyzing', 'ready', 'applying');--> statement-breakpoint
ALTER TABLE "knowledge_injection_events" ADD CONSTRAINT "knowledge_injection_events_personal_entry_id_know_6467dc6ce2_fk" FOREIGN KEY ("personal_entry_id") REFERENCES "public"."knowledge_drafts"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "idx_api_requests_token_repair_pending" ON "api_requests" USING btree ("created_at","id") WHERE "token_accounting_version" IS NULL AND "token_accounting_review_version" IS NULL AND ("cached_input_tokens" > 0 OR "cache_creation_input_tokens" > 0 OR "cache_creation_5m_tokens" > 0 OR "cache_creation_1h_tokens" > 0);--> statement-breakpoint
CREATE INDEX "idx_api_requests_token_repair_reviewed" ON "api_requests" USING btree ("created_at","id") WHERE "token_accounting_version" IS NULL AND ("cached_input_tokens" > 0 OR "cache_creation_input_tokens" > 0 OR "cache_creation_5m_tokens" > 0 OR "cache_creation_1h_tokens" > 0);--> statement-breakpoint
CREATE UNIQUE INDEX "idx_kie_cycle_personal_entry" ON "knowledge_injection_events" USING btree ("narrator_id","compact_seq","personal_entry_id");--> statement-breakpoint
CREATE INDEX "idx_kie_personal_entry" ON "knowledge_injection_events" USING btree ("personal_entry_id");--> statement-breakpoint
ALTER TABLE "knowledge_injection_events" ADD CONSTRAINT "knowledge_injection_exactly_one_entry" CHECK (("entry_id" IS NOT NULL AND "personal_entry_id" IS NULL) OR ("entry_id" IS NULL AND "personal_entry_id" IS NOT NULL));
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
