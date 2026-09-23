CREATE TABLE "workspace_execution_owners" (
	"owner_epoch" text PRIMARY KEY NOT NULL,
	"identity_json" text,
	"created_at" text NOT NULL
);
--> statement-breakpoint
ALTER TABLE "file_change_scope_recoveries" ADD COLUMN "resolution_authority" text DEFAULT 'execution_proven' NOT NULL;--> statement-breakpoint
ALTER TABLE "file_change_scope_recoveries" ADD COLUMN "maintenance_evidence_json" text;--> statement-breakpoint
ALTER TABLE "scheduled_tasks" ADD COLUMN "reasoning_effort" text;--> statement-breakpoint
ALTER TABLE "workspace_write_leases" ADD COLUMN "execution_class" text DEFAULT 'unknown' NOT NULL;--> statement-breakpoint
ALTER TABLE "workspace_write_leases" ADD COLUMN "termination_evidence_json" text;--> statement-breakpoint
CREATE INDEX "idx_workspace_lease_owner" ON "workspace_write_leases" USING btree ("owner_epoch","lease_id");