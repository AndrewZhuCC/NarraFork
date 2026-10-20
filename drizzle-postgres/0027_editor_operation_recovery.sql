ALTER TABLE "file_change_operations" ADD COLUMN "workspace_lease_id" text;--> statement-breakpoint
ALTER TABLE "file_change_operations" ADD COLUMN "editor_raw_snapshot_revision" double precision;