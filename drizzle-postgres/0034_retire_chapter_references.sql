ALTER TABLE "container_instances" DROP CONSTRAINT "ck_container_instances_owner";--> statement-breakpoint
ALTER TABLE "port_allocations" DROP CONSTRAINT "ck_port_allocations_owner";--> statement-breakpoint
ALTER TABLE "terminal_view_state" DROP CONSTRAINT "ck_view_state_resource_owner";--> statement-breakpoint
ALTER TABLE "terminals" DROP CONSTRAINT "ck_terminals_resource_owner";--> statement-breakpoint
ALTER TABLE "volume_snapshot_applications" DROP CONSTRAINT "ck_snapshot_applications_owner";--> statement-breakpoint
ALTER TABLE "container_instances" DROP CONSTRAINT "container_instances_chapter_id_chapters_id_fk";
--> statement-breakpoint
ALTER TABLE "gateway_session_mappings" DROP CONSTRAINT "gateway_session_mappings_chapter_id_chapters_id_fk";
--> statement-breakpoint
ALTER TABLE "narrators" DROP CONSTRAINT "narrators_chapter_id_chapters_id_fk";
--> statement-breakpoint
ALTER TABLE "port_allocations" DROP CONSTRAINT "port_allocations_chapter_id_chapters_id_fk";
--> statement-breakpoint
ALTER TABLE "scheduled_tasks" DROP CONSTRAINT "scheduled_tasks_chapter_id_chapters_id_fk";
--> statement-breakpoint
ALTER TABLE "terminal_view_state" DROP CONSTRAINT "terminal_view_state_chapter_id_chapters_id_fk";
--> statement-breakpoint
ALTER TABLE "terminals" DROP CONSTRAINT "terminals_chapter_id_chapters_id_fk";
--> statement-breakpoint
ALTER TABLE "volume_snapshot_applications" DROP CONSTRAINT "volume_snapshot_applications_chapter_id_chapters_id_fk";
--> statement-breakpoint
ALTER TABLE "volume_snapshots" DROP CONSTRAINT "volume_snapshots_source_chapter_id_chapters_id_fk";
--> statement-breakpoint
DROP INDEX "idx_container_instances_chapter";--> statement-breakpoint
DROP INDEX "idx_container_instances_chapter_service";--> statement-breakpoint
DROP INDEX "idx_container_instances_status";--> statement-breakpoint
DROP INDEX "idx_gsm_chapter";--> statement-breakpoint
DROP INDEX "idx_narrators_chapter";--> statement-breakpoint
DROP INDEX "idx_port_allocations_chapter";--> statement-breakpoint
DROP INDEX "idx_scheduled_tasks_chapter";--> statement-breakpoint
DROP INDEX "idx_view_state_user_chapter";--> statement-breakpoint
DROP INDEX "idx_view_state_chapter";--> statement-breakpoint
DROP INDEX "idx_terminals_chapter";--> statement-breakpoint
DROP INDEX "idx_snapshot_applications_chapter";--> statement-breakpoint
DROP INDEX "idx_volume_snapshots_source_chapter";--> statement-breakpoint
ALTER TABLE "container_instances" DROP COLUMN "chapter_id";--> statement-breakpoint
ALTER TABLE "gateway_session_mappings" DROP COLUMN "chapter_id";--> statement-breakpoint
ALTER TABLE "narrators" DROP COLUMN "chapter_id";--> statement-breakpoint
ALTER TABLE "notifications" DROP COLUMN "chapter_id";--> statement-breakpoint
ALTER TABLE "port_allocations" DROP COLUMN "chapter_id";--> statement-breakpoint
ALTER TABLE "projects" DROP COLUMN "flow_mode";--> statement-breakpoint
ALTER TABLE "projects" DROP COLUMN "startup_script";--> statement-breakpoint
ALTER TABLE "projects" DROP COLUMN "copy_files";--> statement-breakpoint
ALTER TABLE "projects" DROP COLUMN "proxy_domain";--> statement-breakpoint
ALTER TABLE "scheduled_tasks" DROP COLUMN "chapter_id";--> statement-breakpoint
ALTER TABLE "terminal_view_state" DROP COLUMN "chapter_id";--> statement-breakpoint
ALTER TABLE "terminals" DROP COLUMN "chapter_id";--> statement-breakpoint
ALTER TABLE "terminals" DROP COLUMN "graph_opened";--> statement-breakpoint
ALTER TABLE "terminals" DROP COLUMN "graph_x";--> statement-breakpoint
ALTER TABLE "terminals" DROP COLUMN "graph_y";--> statement-breakpoint
ALTER TABLE "terminals" DROP COLUMN "graph_width";--> statement-breakpoint
ALTER TABLE "terminals" DROP COLUMN "graph_height";--> statement-breakpoint
ALTER TABLE "volume_snapshot_applications" DROP COLUMN "chapter_id";--> statement-breakpoint
ALTER TABLE "volume_snapshots" DROP COLUMN "source_chapter_id";--> statement-breakpoint
ALTER TABLE "terminal_view_state" ADD CONSTRAINT "ck_view_state_resource_owner" CHECK ("worktree_resource_id" is null or "narrator_id" is null);--> statement-breakpoint
ALTER TABLE "terminals" ADD CONSTRAINT "ck_terminals_resource_owner" CHECK ("worktree_resource_id" is null or "narrator_id" is null);