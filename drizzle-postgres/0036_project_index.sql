CREATE TABLE "narrator_project_links" (
	"narrator_id" text NOT NULL,
	"project_id" text NOT NULL,
	"role" text NOT NULL,
	"source" text NOT NULL,
	"device_id" text DEFAULT 'local' NOT NULL,
	"directory" text,
	"updated_at" text NOT NULL,
	CONSTRAINT "narrator_project_links_narrator_id_project_id_pk" PRIMARY KEY("narrator_id","project_id")
);
--> statement-breakpoint
ALTER TABLE "projects" ADD COLUMN "root_key" text;--> statement-breakpoint
ALTER TABLE "narrator_project_links" ADD CONSTRAINT "narrator_project_links_narrator_id_narrators_id_fk" FOREIGN KEY ("narrator_id") REFERENCES "public"."narrators"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
ALTER TABLE "narrator_project_links" ADD CONSTRAINT "narrator_project_links_project_id_projects_id_fk" FOREIGN KEY ("project_id") REFERENCES "public"."projects"("id") ON DELETE cascade ON UPDATE no action;--> statement-breakpoint
CREATE UNIQUE INDEX "uq_narrator_project_links_primary" ON "narrator_project_links" USING btree ("narrator_id") WHERE "role" = 'primary';--> statement-breakpoint
CREATE INDEX "idx_narrator_project_links_project" ON "narrator_project_links" USING btree ("project_id","narrator_id");--> statement-breakpoint
CREATE UNIQUE INDEX "uq_projects_root_key" ON "projects" USING btree ("root_key") WHERE "root_key" IS NOT NULL;