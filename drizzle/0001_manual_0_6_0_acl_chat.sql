PRAGMA foreign_keys=OFF;

CREATE TABLE IF NOT EXISTS `narrator_grants` (
	`id` text PRIMARY KEY NOT NULL,
	`narrator_id` text NOT NULL,
	`principal_type` text NOT NULL,
	`principal_id` text NOT NULL,
	`access` text DEFAULT 'read' NOT NULL,
	`granted_by` text,
	`created_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`granted_by`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_narrator_grant_unique` ON `narrator_grants` (`narrator_id`,`principal_type`,`principal_id`);
CREATE INDEX IF NOT EXISTS `idx_narrator_grant_principal` ON `narrator_grants` (`principal_type`,`principal_id`);
CREATE INDEX IF NOT EXISTS `idx_narrator_grant_narrator` ON `narrator_grants` (`narrator_id`);
CREATE INDEX IF NOT EXISTS `idx_narrator_grant_granted_by` ON `narrator_grants` (`granted_by`);

CREATE TABLE IF NOT EXISTS `acl_grants` (
	`id` text PRIMARY KEY NOT NULL,
	`scope_type` text NOT NULL,
	`scope_id` text,
	`principal_type` text NOT NULL,
	`principal_id` text NOT NULL,
	`capability` text NOT NULL,
	`domain_kind` text,
	`domain_value` text,
	`granted_by` text,
	`created_at` text NOT NULL,
	FOREIGN KEY (`granted_by`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_acl_grant_unique_scoped_domain` ON `acl_grants` (`scope_type`,`scope_id`,`principal_type`,`principal_id`,`capability`,`domain_kind`,`domain_value`) WHERE `scope_id` is not null and `domain_value` is not null;
CREATE UNIQUE INDEX IF NOT EXISTS `idx_acl_grant_unique_scoped_capability` ON `acl_grants` (`scope_type`,`scope_id`,`principal_type`,`principal_id`,`capability`) WHERE `scope_id` is not null and `domain_value` is null;
CREATE UNIQUE INDEX IF NOT EXISTS `idx_acl_grant_unique_global_domain` ON `acl_grants` (`scope_type`,`principal_type`,`principal_id`,`capability`,`domain_kind`,`domain_value`) WHERE `scope_id` is null and `domain_value` is not null;
CREATE UNIQUE INDEX IF NOT EXISTS `idx_acl_grant_unique_global_capability` ON `acl_grants` (`scope_type`,`principal_type`,`principal_id`,`capability`) WHERE `scope_id` is null and `domain_value` is null;
CREATE INDEX IF NOT EXISTS `idx_acl_grant_principal` ON `acl_grants` (`principal_type`,`principal_id`);
CREATE INDEX IF NOT EXISTS `idx_acl_grant_scope` ON `acl_grants` (`scope_type`,`scope_id`);
CREATE INDEX IF NOT EXISTS `idx_acl_grant_granted_by` ON `acl_grants` (`granted_by`);

CREATE TABLE IF NOT EXISTS `acl_events` (
	`id` text PRIMARY KEY NOT NULL,
	`actor_user_id` text,
	`actor_role` text,
	`event_type` text NOT NULL,
	`subject_type` text,
	`subject_id` text,
	`scope_type` text NOT NULL,
	`scope_id` text,
	`outcome` text NOT NULL,
	`detail_json` text,
	`created_at` text NOT NULL
);
CREATE INDEX IF NOT EXISTS `idx_acl_event_created` ON `acl_events` (`created_at`,`id`);
CREATE INDEX IF NOT EXISTS `idx_acl_event_scope` ON `acl_events` (`scope_type`,`scope_id`);
CREATE INDEX IF NOT EXISTS `idx_acl_event_subject` ON `acl_events` (`subject_type`,`subject_id`);
CREATE INDEX IF NOT EXISTS `idx_acl_event_actor` ON `acl_events` (`actor_user_id`);

CREATE TABLE IF NOT EXISTS `chat_rooms` (
	`id` text PRIMARY KEY NOT NULL,
	`kind` text NOT NULL,
	`dm_key` text,
	`narrator_id` text,
	`next_seq` integer DEFAULT 1 NOT NULL,
	`last_message_at` text,
	`last_message_preview` text,
	`last_message_sender_id` text,
	`created_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade
);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_chat_rooms_dm_key` ON `chat_rooms` (`dm_key`);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_chat_rooms_narrator` ON `chat_rooms` (`narrator_id`);
CREATE INDEX IF NOT EXISTS `idx_chat_rooms_kind_last` ON `chat_rooms` (`kind`,`last_message_at`);

CREATE TABLE IF NOT EXISTS `chat_room_members` (
	`id` text PRIMARY KEY NOT NULL,
	`room_id` text NOT NULL,
	`user_id` text NOT NULL,
	`last_read_seq` integer DEFAULT 0 NOT NULL,
	`last_read_at` text,
	`muted` integer DEFAULT false NOT NULL,
	`joined_at` text NOT NULL,
	FOREIGN KEY (`room_id`) REFERENCES `chat_rooms`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE cascade
);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_chat_room_members_room_user` ON `chat_room_members` (`room_id`,`user_id`);
CREATE INDEX IF NOT EXISTS `idx_chat_room_members_user_room` ON `chat_room_members` (`user_id`,`room_id`);

CREATE TABLE IF NOT EXISTS `chat_messages` (
	`id` text PRIMARY KEY NOT NULL,
	`room_id` text NOT NULL,
	`seq` integer NOT NULL,
	`sender_user_id` text,
	`kind` text DEFAULT 'text' NOT NULL,
	`content_text` text NOT NULL,
	`reply_to_message_id` text,
	`reply_to_seq` integer,
	`reply_to_sender_user_id` text,
	`reply_to_preview` text,
	`edited_at` text,
	`deleted_at` text,
	`created_at` text NOT NULL,
	FOREIGN KEY (`room_id`) REFERENCES `chat_rooms`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`sender_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
CREATE UNIQUE INDEX IF NOT EXISTS `idx_chat_messages_room_seq` ON `chat_messages` (`room_id`,`seq`);
CREATE INDEX IF NOT EXISTS `idx_chat_messages_sender` ON `chat_messages` (`sender_user_id`);

CREATE TABLE IF NOT EXISTS `chat_attachments` (
	`id` text PRIMARY KEY NOT NULL,
	`room_id` text NOT NULL,
	`message_id` text,
	`uploader_user_id` text,
	`kind` text NOT NULL,
	`filename` text NOT NULL,
	`media_type` text NOT NULL,
	`size_bytes` integer NOT NULL,
	`width` integer,
	`height` integer,
	`stored_name` text NOT NULL,
	`created_at` text NOT NULL,
	`claimed_at` text,
	FOREIGN KEY (`room_id`) REFERENCES `chat_rooms`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`uploader_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
CREATE INDEX IF NOT EXISTS `idx_chat_attachments_message` ON `chat_attachments` (`message_id`);
CREATE INDEX IF NOT EXISTS `idx_chat_attachments_room_unclaimed` ON `chat_attachments` (`room_id`,`claimed_at`);
CREATE INDEX IF NOT EXISTS `idx_chat_attachments_uploader` ON `chat_attachments` (`uploader_user_id`);

PRAGMA foreign_keys=ON;
