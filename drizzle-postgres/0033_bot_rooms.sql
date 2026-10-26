DROP INDEX "idx_chat_rooms_narrator";--> statement-breakpoint
ALTER TABLE "chat_messages" ADD COLUMN "sender_narrator_id" text;--> statement-breakpoint
ALTER TABLE "chat_messages" ADD COLUMN "reply_to_sender_narrator_id" text;--> statement-breakpoint
ALTER TABLE "chat_messages" ADD CONSTRAINT "fk_chat_messages_sender_narrator" FOREIGN KEY ("sender_narrator_id") REFERENCES "public"."narrators"("id") ON DELETE set null ON UPDATE no action;--> statement-breakpoint
CREATE INDEX "idx_chat_messages_sender_narrator" ON "chat_messages" USING btree ("sender_narrator_id");--> statement-breakpoint
CREATE INDEX "idx_chat_rooms_narrator_id" ON "chat_rooms" USING btree ("narrator_id");--> statement-breakpoint
CREATE UNIQUE INDEX "idx_chat_rooms_narrator" ON "chat_rooms" USING btree ("narrator_id") WHERE "kind" = 'narrator';