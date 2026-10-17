ALTER TABLE "api_requests" ADD COLUMN "diagnostics_json" text;--> statement-breakpoint
ALTER TABLE "api_requests" ADD COLUMN "recording_request_id" text;--> statement-breakpoint
CREATE INDEX "idx_api_requests_recording" ON "api_requests" USING btree ("recording_request_id");