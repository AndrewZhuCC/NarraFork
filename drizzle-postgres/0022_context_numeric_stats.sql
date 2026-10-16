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
CREATE INDEX "idx_context_pins_narrator" ON "narrator_context_pins" USING btree ("narrator_id");