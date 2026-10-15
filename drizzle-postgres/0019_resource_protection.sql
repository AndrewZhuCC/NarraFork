CREATE TABLE "resource_protection_paths" (
	"source" text NOT NULL,
	"source_id" text NOT NULL,
	"slot" integer NOT NULL,
	"device_id" text NOT NULL,
	"canonical_path" text,
	"repository_key" text,
	"shadow_key" text,
	"scope_project_id" text,
	"alias_sensitive" integer DEFAULT 0 NOT NULL,
	"claim_json" text NOT NULL,
	CONSTRAINT "resource_protection_paths_source_source_id_slot_pk" PRIMARY KEY("source","source_id","slot")
);
--> statement-breakpoint
CREATE TABLE "resource_protection_sources" (
	"source" text NOT NULL,
	"source_id" text NOT NULL,
	"revision" integer DEFAULT 1 NOT NULL,
	"dirty" integer DEFAULT 1 NOT NULL,
	"cursor" text,
	"complete" integer DEFAULT 0 NOT NULL,
	CONSTRAINT "resource_protection_sources_source_source_id_pk" PRIMARY KEY("source","source_id")
);
--> statement-breakpoint
CREATE INDEX "idx_protection_path" ON "resource_protection_paths" USING btree ("device_id","canonical_path","source","source_id","slot");--> statement-breakpoint
CREATE INDEX "idx_protection_repository" ON "resource_protection_paths" USING btree ("device_id","repository_key","source","source_id","slot");--> statement-breakpoint
CREATE INDEX "idx_protection_shadow" ON "resource_protection_paths" USING btree ("shadow_key","source","source_id","slot");--> statement-breakpoint
CREATE INDEX "idx_protection_scope" ON "resource_protection_paths" USING btree ("scope_project_id","source","source_id","slot");--> statement-breakpoint
CREATE INDEX "idx_protection_alias" ON "resource_protection_paths" USING btree ("alias_sensitive","source","source_id","slot");--> statement-breakpoint
CREATE INDEX "idx_protection_dirty" ON "resource_protection_sources" USING btree ("dirty","source","source_id");