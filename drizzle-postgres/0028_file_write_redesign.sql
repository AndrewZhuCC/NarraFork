ALTER TABLE "file_change_effects" ALTER COLUMN "scope_revision" DROP NOT NULL;--> statement-breakpoint
ALTER TABLE "file_change_storage_budgets" ALTER COLUMN "used_bytes" SET DATA TYPE bigint;--> statement-breakpoint
ALTER TABLE "file_change_storage_budgets" ALTER COLUMN "reserved_bytes" SET DATA TYPE bigint;--> statement-breakpoint
ALTER TABLE "file_change_storage_budgets" ALTER COLUMN "quota_bytes" SET DATA TYPE bigint;--> statement-breakpoint
ALTER TABLE "revert_operation_files" ALTER COLUMN "compensate_mutation_id" DROP NOT NULL;--> statement-breakpoint
ALTER TABLE "revert_operation_files" ALTER COLUMN "compensate_request_digest" DROP NOT NULL;--> statement-breakpoint
ALTER TABLE "file_change_effects" ADD COLUMN "disk_outcome" text;--> statement-breakpoint
CREATE INDEX "idx_fc_effect_journal" ON "file_change_effects" USING btree ("scope_id","file_key","journal_seq","scope_revision","id");