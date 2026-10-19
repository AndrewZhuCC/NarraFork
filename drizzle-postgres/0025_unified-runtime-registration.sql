ALTER TABLE "narrators" ADD COLUMN "runtime_registration_json" text;--> statement-breakpoint
ALTER TABLE "narrators" ADD COLUMN "policy_source_narrator_id" text;--> statement-breakpoint
ALTER TABLE "narrators" ADD CONSTRAINT "narrators_policy_source_narrator_id_narrators_id_fk" FOREIGN KEY ("policy_source_narrator_id") REFERENCES "public"."narrators"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "idx_narrators_policy_source" ON "narrators" USING btree ("policy_source_narrator_id");