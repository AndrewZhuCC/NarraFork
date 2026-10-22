CREATE TABLE "narrator_block_backfill" (
	"job" text PRIMARY KEY NOT NULL,
	"cursor" integer DEFAULT 0 NOT NULL,
	"complete" boolean DEFAULT false NOT NULL,
	"paused" boolean DEFAULT false NOT NULL,
	"processed" integer DEFAULT 0 NOT NULL,
	"updated_at" text NOT NULL,
	CONSTRAINT "ck_block_backfill_job" CHECK ("job" in ('convert', 'verify'))
);
--> statement-breakpoint
CREATE TABLE "narrator_block_diagnostics" (
	"message_id" text PRIMARY KEY NOT NULL,
	"kind" text NOT NULL,
	"detail" text,
	"created_at" text NOT NULL,
	CONSTRAINT "ck_block_diagnostics_kind" CHECK ("kind" in ('mismatch', 'undecodable'))
);
--> statement-breakpoint
ALTER TABLE "narrator_block_diagnostics" ADD CONSTRAINT "narrator_block_diagnostics_message_id_narrator_messages_id_fk" FOREIGN KEY ("message_id") REFERENCES "public"."narrator_messages"("id") ON DELETE cascade ON UPDATE no action;