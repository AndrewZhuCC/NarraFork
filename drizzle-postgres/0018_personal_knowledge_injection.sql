ALTER TABLE "knowledge_injection_events" ALTER COLUMN "entry_id" DROP NOT NULL;--> statement-breakpoint
ALTER TABLE "knowledge_injection_events" ADD COLUMN "personal_entry_id" text;--> statement-breakpoint
ALTER TABLE "knowledge_injection_events" ADD CONSTRAINT "knowledge_injection_events_personal_entry_id_know_6467dc6ce2_fk" FOREIGN KEY ("personal_entry_id") REFERENCES "public"."knowledge_drafts"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE UNIQUE INDEX "idx_kie_cycle_personal_entry" ON "knowledge_injection_events" USING btree ("narrator_id","compact_seq","personal_entry_id");--> statement-breakpoint
CREATE INDEX "idx_kie_personal_entry" ON "knowledge_injection_events" USING btree ("personal_entry_id");--> statement-breakpoint
ALTER TABLE "knowledge_injection_events" ADD CONSTRAINT "knowledge_injection_exactly_one_entry" CHECK (("entry_id" IS NOT NULL AND "personal_entry_id" IS NULL) OR ("entry_id" IS NULL AND "personal_entry_id" IS NOT NULL));