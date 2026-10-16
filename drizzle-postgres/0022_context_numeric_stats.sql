CREATE TABLE "narrator_context_pins" (
	"generation" text PRIMARY KEY NOT NULL,
	"narrator_id" text NOT NULL,
	"segments_json" text NOT NULL
);
--> statement-breakpoint
CREATE TABLE "narrator_message_context_stats" (
	"message_id" text PRIMARY KEY NOT NULL,
	"role" text NOT NULL,
	"parent_tool_use_id" text,
	"segments_json" text NOT NULL
);
--> statement-breakpoint
DROP TABLE "narrator_context_char_pages" CASCADE;--> statement-breakpoint
ALTER TABLE "narrator_context_pins" ADD CONSTRAINT "narrator_context_pins_narrator_id_narrators_id_fk" FOREIGN KEY ("narrator_id") REFERENCES "public"."narrators"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "narrator_message_context_stats" ADD CONSTRAINT "narrator_message_context_stats_message_id_narrato_8cbeb8b7e2_fk" FOREIGN KEY ("message_id") REFERENCES "public"."narrator_messages"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "idx_context_pins_narrator" ON "narrator_context_pins" USING btree ("narrator_id");--> statement-breakpoint
INSERT INTO "narrator_message_context_stats" ("message_id", "role", "parent_tool_use_id", "segments_json")
SELECT "id", "role", "parent_tool_use_id", COALESCE("context_chars_json", '{"segments":[]}')
FROM "narrator_messages"
ON CONFLICT ("message_id") DO NOTHING;--> statement-breakpoint
CREATE FUNCTION narrator_message_context_stats_sync() RETURNS trigger LANGUAGE plpgsql AS $context_stats$
BEGIN
	INSERT INTO narrator_message_context_stats (message_id, role, parent_tool_use_id, segments_json)
	VALUES (NEW.id, NEW.role, NEW.parent_tool_use_id, COALESCE(NEW.context_chars_json, '{"segments":[]}'))
	ON CONFLICT (message_id) DO UPDATE SET
		role = EXCLUDED.role,
		parent_tool_use_id = EXCLUDED.parent_tool_use_id,
		segments_json = EXCLUDED.segments_json;
	RETURN NEW;
END;
$context_stats$;--> statement-breakpoint
CREATE TRIGGER narrator_message_context_stats_insert
AFTER INSERT ON narrator_messages
FOR EACH ROW EXECUTE FUNCTION narrator_message_context_stats_sync();--> statement-breakpoint
CREATE TRIGGER narrator_message_context_stats_update
AFTER UPDATE OF role, parent_tool_use_id, context_chars_json ON narrator_messages
FOR EACH ROW EXECUTE FUNCTION narrator_message_context_stats_sync();