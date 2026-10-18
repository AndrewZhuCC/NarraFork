CREATE TABLE "narrator_event_subscriptions" (
	"id" text PRIMARY KEY NOT NULL,
	"source_narrator_id" text NOT NULL,
	"recipient_narrator_id" text NOT NULL,
	"run_id" text,
	"events" text NOT NULL,
	"wake" boolean DEFAULT false NOT NULL,
	"origin" text NOT NULL,
	"authorized_by_user_id" text,
	"created_at" text NOT NULL,
	"updated_at" text NOT NULL
);
--> statement-breakpoint
ALTER TABLE "narrators" ADD COLUMN "runtime_registration_json" text;--> statement-breakpoint
ALTER TABLE "narrator_event_subscriptions" ADD CONSTRAINT "narrator_event_subscriptions_source_narrator_id_narrators_id_fk" FOREIGN KEY ("source_narrator_id") REFERENCES "public"."narrators"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "narrator_event_subscriptions" ADD CONSTRAINT "narrator_event_subscriptions_authorized_by_user_id_users_id_fk" FOREIGN KEY ("authorized_by_user_id") REFERENCES "public"."users"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "narrator_event_subscriptions" ADD CONSTRAINT "narrator_event_subscriptions_recipient_narrator_i_cea958b39a_fk" FOREIGN KEY ("recipient_narrator_id") REFERENCES "public"."narrators"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "idx_narrator_event_sub_source_run" ON "narrator_event_subscriptions" USING btree ("source_narrator_id","run_id");--> statement-breakpoint
CREATE INDEX "idx_narrator_event_sub_recipient" ON "narrator_event_subscriptions" USING btree ("recipient_narrator_id");--> statement-breakpoint
CREATE INDEX "idx_narrator_event_sub_authorized_by" ON "narrator_event_subscriptions" USING btree ("authorized_by_user_id");--> statement-breakpoint
CREATE UNIQUE INDEX "idx_narrator_event_sub_session_unique" ON "narrator_event_subscriptions" USING btree ("source_narrator_id","recipient_narrator_id") WHERE "run_id" IS NULL;--> statement-breakpoint
CREATE UNIQUE INDEX "idx_narrator_event_sub_run_unique" ON "narrator_event_subscriptions" USING btree ("source_narrator_id","recipient_narrator_id","run_id") WHERE "run_id" IS NOT NULL;