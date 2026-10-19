ALTER TABLE "usage_token_repair_jobs" ADD COLUMN "workflow_version" integer;--> statement-breakpoint
ALTER TABLE "usage_token_repair_jobs" ADD COLUMN "phase" text;--> statement-breakpoint
ALTER TABLE "usage_token_repair_jobs" ADD COLUMN "summary_json" text;--> statement-breakpoint
ALTER TABLE "usage_token_repair_jobs" ADD COLUMN "rows_per_second" double precision;--> statement-breakpoint
ALTER TABLE "usage_token_repair_jobs" ADD COLUMN "scratch_retired_at" text;--> statement-breakpoint
CREATE INDEX "idx_token_repair_scratch_retirement" ON "usage_token_repair_jobs" USING btree ("updated_at","id") WHERE "scratch_retired_at" IS NULL AND "status" IN ('completed', 'cancelled', 'failed', 'interrupted');