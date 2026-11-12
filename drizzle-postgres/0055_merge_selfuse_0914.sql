CREATE TABLE "folder_git_identity_bindings" (
	"id" text PRIMARY KEY NOT NULL,
	"folder_id" text NOT NULL,
	"user_id" text NOT NULL,
	"identity_id" text NOT NULL,
	"updated_at" text NOT NULL
);
--> statement-breakpoint
CREATE TABLE "outline_digests" (
	"id" text PRIMARY KEY NOT NULL,
	"user_id" text NOT NULL,
	"kind" text NOT NULL,
	"period_key" text NOT NULL,
	"time_zone" text NOT NULL,
	"period_start" text NOT NULL,
	"period_end" text NOT NULL,
	"counts_json" text NOT NULL,
	"item_count" integer DEFAULT 0 NOT NULL,
	"items_json" text NOT NULL,
	"ranges_json" text NOT NULL,
	"truncated" boolean DEFAULT false NOT NULL,
	"created_at" text NOT NULL,
	"seen_at" text
);
--> statement-breakpoint
CREATE TABLE "outline_task_seen" (
	"user_id" text NOT NULL,
	"task_block_id" text NOT NULL,
	"seen_seq" integer NOT NULL,
	"seen_at" text NOT NULL,
	CONSTRAINT "outline_task_seen_user_id_task_block_id_pk" PRIMARY KEY("user_id","task_block_id")
);
--> statement-breakpoint
CREATE TABLE "recent_tab_archive_marks" (
	"id" text PRIMARY KEY NOT NULL,
	"user_id" text NOT NULL,
	"narrator_id" text NOT NULL,
	"folder_id" text,
	"archived_at" text NOT NULL
);
--> statement-breakpoint
CREATE TABLE "usage_daily" (
	"id" text PRIMARY KEY NOT NULL,
	"day" text NOT NULL,
	"billing_user_id" text NOT NULL,
	"project_id" text NOT NULL,
	"provider" text NOT NULL,
	"credential_id" text NOT NULL,
	"model" text NOT NULL,
	"request_count" integer DEFAULT 0 NOT NULL,
	"input_tokens" integer DEFAULT 0 NOT NULL,
	"output_tokens" integer DEFAULT 0 NOT NULL,
	"cached_input_tokens" integer DEFAULT 0 NOT NULL,
	"cache_creation_tokens" integer DEFAULT 0 NOT NULL,
	"reasoning_tokens" integer DEFAULT 0 NOT NULL,
	"cost_usd" double precision DEFAULT 0 NOT NULL,
	"unpriced_request_count" integer DEFAULT 0 NOT NULL
);
--> statement-breakpoint
ALTER TABLE "api_requests" ADD COLUMN "billing_user_id" text;--> statement-breakpoint
ALTER TABLE "api_requests" ADD COLUMN "project_id" text;--> statement-breakpoint
ALTER TABLE "knowledge_submissions" ADD COLUMN "author_read_at" text;--> statement-breakpoint
ALTER TABLE "narrators" ADD COLUMN "question_reflection_override" text DEFAULT 'inherit' NOT NULL;--> statement-breakpoint
ALTER TABLE "narrators" ADD COLUMN "workspace_directories" text DEFAULT '{}'::text NOT NULL;--> statement-breakpoint
ALTER TABLE "outline_spaces" ADD COLUMN "last_commit_at" text;--> statement-breakpoint
ALTER TABLE "outline_view_state" ADD COLUMN "expanded_json" text;--> statement-breakpoint
ALTER TABLE "user_preferences" ADD COLUMN "compact_auxiliary_content" boolean DEFAULT true NOT NULL;--> statement-breakpoint
ALTER TABLE "user_preferences" ADD COLUMN "recent_tabs_show_directory" boolean DEFAULT false NOT NULL;--> statement-breakpoint
ALTER TABLE "user_preferences" ADD COLUMN "git_identity_folder_context_menu" boolean DEFAULT false NOT NULL;--> statement-breakpoint
ALTER TABLE "user_preferences" ADD COLUMN "outline_digest_time_zone" text;--> statement-breakpoint
ALTER TABLE "user_preferences" ADD COLUMN "outline_digest_time" text;--> statement-breakpoint
ALTER TABLE "workspaces" ADD COLUMN "title_source" text DEFAULT 'manual' NOT NULL;--> statement-breakpoint
ALTER TABLE "workspaces" ADD COLUMN "title_revision" integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE "folder_git_identity_bindings" ADD CONSTRAINT "folder_git_identity_bindings_user_id_users_id_fk" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "folder_git_identity_bindings" ADD CONSTRAINT "folder_git_identity_bindings_identity_id_user_git_b105c2a3b2_fk" FOREIGN KEY ("identity_id") REFERENCES "public"."user_git_identities"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "outline_digests" ADD CONSTRAINT "outline_digests_user_id_users_id_fk" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "outline_task_seen" ADD CONSTRAINT "outline_task_seen_user_id_users_id_fk" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "outline_task_seen" ADD CONSTRAINT "outline_task_seen_task_block_id_outline_blocks_id_fk" FOREIGN KEY ("task_block_id") REFERENCES "public"."outline_blocks"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "recent_tab_archive_marks" ADD CONSTRAINT "recent_tab_archive_marks_user_id_users_id_fk" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "recent_tab_archive_marks" ADD CONSTRAINT "recent_tab_archive_marks_narrator_id_narrators_id_fk" FOREIGN KEY ("narrator_id") REFERENCES "public"."narrators"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE UNIQUE INDEX "idx_folder_git_identity_unique" ON "folder_git_identity_bindings" USING btree ("folder_id","user_id");--> statement-breakpoint
CREATE INDEX "idx_folder_git_identity_user" ON "folder_git_identity_bindings" USING btree ("user_id");--> statement-breakpoint
CREATE UNIQUE INDEX "uq_outline_digests_user_period" ON "outline_digests" USING btree ("user_id","kind","period_key");--> statement-breakpoint
CREATE INDEX "idx_outline_digests_unseen" ON "outline_digests" USING btree ("user_id") WHERE "seen_at" IS NULL;--> statement-breakpoint
CREATE INDEX "idx_outline_task_seen_block" ON "outline_task_seen" USING btree ("task_block_id");--> statement-breakpoint
CREATE INDEX "idx_outline_task_seen_user_at" ON "outline_task_seen" USING btree ("user_id","seen_at");--> statement-breakpoint
CREATE UNIQUE INDEX "idx_recent_tab_archive_mark" ON "recent_tab_archive_marks" USING btree ("user_id","narrator_id");--> statement-breakpoint
CREATE UNIQUE INDEX "idx_usage_daily_key" ON "usage_daily" USING btree ("day","billing_user_id","project_id","provider","credential_id","model");--> statement-breakpoint
CREATE INDEX "idx_ks_submitter_author_read" ON "knowledge_submissions" USING btree ("submitter_user_id","author_read_at");--> statement-breakpoint
CREATE INDEX "idx_narrators_status" ON "narrators" USING btree ("status");--> statement-breakpoint
CREATE INDEX "idx_narrators_variant_list" ON "narrators" USING btree ("variant","hidden_from_lists","status","updated_at","id");--> statement-breakpoint
CREATE INDEX "idx_outline_spaces_last_commit" ON "outline_spaces" USING btree ("last_commit_at","id");--> statement-breakpoint
CREATE INDEX "idx_outline_spaces_unstamped" ON "outline_spaces" USING btree ("id") WHERE "last_commit_at" IS NULL AND "next_seq" > 1;--> statement-breakpoint
CREATE INDEX "idx_workspace_panels_kind_workspace" ON "workspace_panels" USING btree ("kind","workspace_id","id");