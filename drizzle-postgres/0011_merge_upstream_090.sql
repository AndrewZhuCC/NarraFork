CREATE TABLE "narrator_git_identity_bindings" (
	"id" text PRIMARY KEY NOT NULL,
	"narrator_id" text NOT NULL,
	"user_id" text NOT NULL,
	"identity_id" text NOT NULL,
	"updated_at" text NOT NULL
);
--> statement-breakpoint
CREATE TABLE "user_git_identities" (
	"id" text PRIMARY KEY NOT NULL,
	"user_id" text NOT NULL,
	"name" text NOT NULL,
	"email" text NOT NULL,
	"is_default" boolean DEFAULT false NOT NULL,
	"created_at" text NOT NULL
);
--> statement-breakpoint
ALTER TABLE "background_tasks" ADD COLUMN "background_kind" text DEFAULT 'task' NOT NULL;--> statement-breakpoint
ALTER TABLE "narrator_git_identity_bindings" ADD CONSTRAINT "narrator_git_identity_bindings_narrator_id_narrators_id_fk" FOREIGN KEY ("narrator_id") REFERENCES "public"."narrators"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "narrator_git_identity_bindings" ADD CONSTRAINT "narrator_git_identity_bindings_user_id_users_id_fk" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "narrator_git_identity_bindings" ADD CONSTRAINT "narrator_git_identity_bindings_identity_id_user_g_d1b761527c_fk" FOREIGN KEY ("identity_id") REFERENCES "public"."user_git_identities"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "user_git_identities" ADD CONSTRAINT "user_git_identities_user_id_users_id_fk" FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE UNIQUE INDEX "idx_narrator_git_identity_unique" ON "narrator_git_identity_bindings" USING btree ("narrator_id","user_id");--> statement-breakpoint
CREATE INDEX "idx_narrator_git_identity_user" ON "narrator_git_identity_bindings" USING btree ("user_id");--> statement-breakpoint
CREATE INDEX "idx_user_git_identities_user" ON "user_git_identities" USING btree ("user_id","created_at");