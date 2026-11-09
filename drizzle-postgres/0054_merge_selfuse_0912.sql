CREATE TABLE "outline_questions" (
	"block_id" text PRIMARY KEY NOT NULL,
	"space_id" text NOT NULL,
	"state" text NOT NULL,
	"proceed" boolean DEFAULT false NOT NULL,
	"to_user_id" text,
	"asker_narrator_id" text,
	"asker_user_id" text,
	"asker_tool_call_id" text,
	"created_at" text NOT NULL,
	"answered_at" text,
	"answer_commit_id" text,
	"delivered_at" text
);
--> statement-breakpoint
ALTER TABLE "api_requests" ADD COLUMN "decode_ms" integer;--> statement-breakpoint
ALTER TABLE "outline_block_versions" ADD COLUMN "ask_json" text;--> statement-breakpoint
ALTER TABLE "outline_blocks" ADD COLUMN "ask_json" text;--> statement-breakpoint
ALTER TABLE "outline_pending" ADD COLUMN "digest_seen_at" text;--> statement-breakpoint
ALTER TABLE "user_preferences" ADD COLUMN "narrator_line_height_v2_percent" integer;--> statement-breakpoint
ALTER TABLE "user_recent_tabs" ADD COLUMN "folder_id" text;--> statement-breakpoint
ALTER TABLE "user_recent_tabs" ADD COLUMN "folder_icon" text;--> statement-breakpoint
ALTER TABLE "user_recent_tabs" ADD COLUMN "folder_color" text;--> statement-breakpoint
ALTER TABLE "user_recent_tabs" ADD COLUMN "folder_preview_count" integer;--> statement-breakpoint
ALTER TABLE "user_recent_tabs_meta" ADD COLUMN "folders_migrated" boolean DEFAULT false NOT NULL;--> statement-breakpoint
ALTER TABLE "outline_questions" ADD CONSTRAINT "outline_questions_block_id_outline_blocks_id_fk" FOREIGN KEY ("block_id") REFERENCES "public"."outline_blocks"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "idx_outline_questions_to_open" ON "outline_questions" USING btree ("to_user_id") WHERE "state" = 'open';--> statement-breakpoint
CREATE INDEX "idx_outline_questions_space_open" ON "outline_questions" USING btree ("space_id") WHERE "state" = 'open';--> statement-breakpoint
CREATE INDEX "idx_outline_questions_asker_open" ON "outline_questions" USING btree ("asker_narrator_id") WHERE "state" = 'open';--> statement-breakpoint
CREATE INDEX "idx_outline_questions_undelivered" ON "outline_questions" USING btree ("answered_at") WHERE "state" = 'answered' AND "delivered_at" IS NULL;--> statement-breakpoint
CREATE INDEX "idx_outline_pending_digest" ON "outline_pending" USING btree ("decider_user_id") WHERE "status" = 'confirmed' AND "digest_seen_at" IS NULL AND ("decided_by_json"::jsonb #>> '{kind}') = 'agent';--> statement-breakpoint
CREATE INDEX "idx_user_recent_tabs_user_folder" ON "user_recent_tabs" USING btree ("user_id","folder_id","sort_order");