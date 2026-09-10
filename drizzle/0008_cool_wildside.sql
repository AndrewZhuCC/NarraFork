CREATE TABLE `file_change_blob_reservations` (
	`id` text PRIMARY KEY NOT NULL,
	`budget_id` text NOT NULL,
	`owner_epoch` text NOT NULL,
	`generation` integer DEFAULT 0 NOT NULL,
	`expected_size` integer NOT NULL,
	`status` text DEFAULT 'reserved' NOT NULL,
	`blob_digest` text,
	`published` integer,
	`created_at` text NOT NULL,
	`settled_at` text,
	FOREIGN KEY (`budget_id`) REFERENCES `file_change_storage_budgets`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
CREATE INDEX `idx_fc_reservation_budget` ON `file_change_blob_reservations` (`budget_id`,`status`,`created_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_fc_reservation_owner` ON `file_change_blob_reservations` (`owner_epoch`,`status`);--> statement-breakpoint
CREATE INDEX `idx_fc_reservation_blob` ON `file_change_blob_reservations` (`blob_digest`);--> statement-breakpoint
CREATE TABLE `file_change_blobs` (
	`id` text PRIMARY KEY NOT NULL,
	`digest` text NOT NULL,
	`size_bytes` integer NOT NULL,
	`storage_key` text NOT NULL,
	`status` text DEFAULT 'staging' NOT NULL,
	`lease_until` text,
	`gc_generation` integer DEFAULT 0 NOT NULL,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_fc_blob_digest` ON `file_change_blobs` (`digest`);--> statement-breakpoint
CREATE INDEX `idx_fc_blob_gc` ON `file_change_blobs` (`status`,`lease_until`,`updated_at`);--> statement-breakpoint
CREATE TABLE `file_change_effects` (
	`id` text PRIMARY KEY NOT NULL,
	`operation_id` text NOT NULL,
	`scope_id` text NOT NULL,
	`file_key` text NOT NULL,
	`identity_json` text NOT NULL,
	`scope_revision` integer NOT NULL,
	`mutation_id` text NOT NULL,
	`request_digest` text NOT NULL,
	`phase` text NOT NULL,
	`before_state_json` text NOT NULL,
	`intended_after_state_json` text NOT NULL,
	`observed_after_state_json` text NOT NULL,
	`before_blob_digest` text,
	`intended_after_blob_digest` text,
	`observed_after_blob_digest` text,
	`outcome` text DEFAULT 'pending' NOT NULL,
	`settlement` text DEFAULT 'preparing' NOT NULL,
	`attribution_grade` text DEFAULT 'unknown' NOT NULL,
	`attribution_ceiling` text,
	`execution_confirmed` integer DEFAULT false NOT NULL,
	`execution_receipt_json` text,
	`execution_receipt_digest` text,
	`lines_added` integer,
	`lines_removed` integer,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`operation_id`) REFERENCES `file_change_operations`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`scope_id`) REFERENCES `file_change_scopes`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`before_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`intended_after_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`observed_after_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_fc_effect_mutation` ON `file_change_effects` (`mutation_id`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_fc_effect_operation_file` ON `file_change_effects` (`operation_id`,`file_key`,`phase`);--> statement-breakpoint
CREATE INDEX `idx_fc_effect_file` ON `file_change_effects` (`scope_id`,`file_key`,`scope_revision`,`id`);--> statement-breakpoint
CREATE INDEX `idx_fc_effect_pending` ON `file_change_effects` (`settlement`,`updated_at`);--> statement-breakpoint
CREATE INDEX `idx_fc_effect_before_blob` ON `file_change_effects` (`before_blob_digest`);--> statement-breakpoint
CREATE INDEX `idx_fc_effect_intended_blob` ON `file_change_effects` (`intended_after_blob_digest`);--> statement-breakpoint
CREATE INDEX `idx_fc_effect_observed_blob` ON `file_change_effects` (`observed_after_blob_digest`);--> statement-breakpoint
CREATE TABLE `file_change_operations` (
	`id` text PRIMARY KEY NOT NULL,
	`evidence_version` integer DEFAULT 2 NOT NULL,
	`source_instance_id` text NOT NULL,
	`source_kind` text NOT NULL,
	`source_id` text NOT NULL,
	`attempt` integer NOT NULL,
	`request_digest` text,
	`expected_effect_count` integer,
	`prepared_effect_count` integer DEFAULT 0 NOT NULL,
	`evidence_bytes` integer DEFAULT 0 NOT NULL,
	`settled_effect_count` integer DEFAULT 0 NOT NULL,
	`unresolved_effect_count` integer DEFAULT 0 NOT NULL,
	`tool_call_id` text,
	`tool_use_id` text,
	`background_task_id` text,
	`narrator_id` text,
	`project_id` text,
	`owner_user_id` text,
	`actor_subject_key` text NOT NULL,
	`actor_json` text NOT NULL,
	`initiator_subject_key` text,
	`execution_binding_json` text,
	`execution_outcome` text DEFAULT 'running' NOT NULL,
	`effect_outcome` text DEFAULT 'pending' NOT NULL,
	`settlement` text DEFAULT 'preparing' NOT NULL,
	`attribution_grade` text DEFAULT 'unknown' NOT NULL,
	`coverage` text DEFAULT 'unavailable' NOT NULL,
	`parent_operation_id` text,
	`reason` text,
	`lease_until` text,
	`started_at` text NOT NULL,
	`finished_at` text,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`owner_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_fc_operation_attempt` ON `file_change_operations` (`source_instance_id`,`source_kind`,`source_id`,`attempt`);--> statement-breakpoint
CREATE INDEX `idx_fc_operation_narrator` ON `file_change_operations` (`narrator_id`,`started_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_fc_operation_actor` ON `file_change_operations` (`actor_subject_key`,`started_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_fc_operation_pending` ON `file_change_operations` (`settlement`,`updated_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_fc_operation_task` ON `file_change_operations` (`background_task_id`,`attempt`);--> statement-breakpoint
CREATE INDEX `idx_fc_operation_parent` ON `file_change_operations` (`parent_operation_id`);--> statement-breakpoint
CREATE TABLE `file_change_rollups` (
	`id` text PRIMARY KEY NOT NULL,
	`scope_id` text NOT NULL,
	`file_key` text NOT NULL,
	`actor_subject_key` text NOT NULL,
	`projection_kind` text NOT NULL,
	`attempt_key` text DEFAULT '' NOT NULL,
	`change_count` integer DEFAULT 0 NOT NULL,
	`lines_added` integer DEFAULT 0 NOT NULL,
	`lines_removed` integer DEFAULT 0 NOT NULL,
	`unmeasured_count` integer DEFAULT 0 NOT NULL,
	`has_external_change` integer DEFAULT false NOT NULL,
	`has_imprecise_attribution` integer DEFAULT true NOT NULL,
	`complete` integer DEFAULT false NOT NULL,
	`as_of_revision` integer,
	`last_effect_id` text,
	`head_fingerprint` text,
	`index_fingerprint` text,
	`worktree_fingerprint` text,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`scope_id`) REFERENCES `file_change_scopes`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`last_effect_id`) REFERENCES `file_change_effects`(`id`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_fc_rollup_dimension` ON `file_change_rollups` (`scope_id`,`file_key`,`actor_subject_key`,`projection_kind`,`attempt_key`);--> statement-breakpoint
CREATE INDEX `idx_fc_rollup_actor` ON `file_change_rollups` (`actor_subject_key`,`projection_kind`,`updated_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_fc_rollup_effect` ON `file_change_rollups` (`last_effect_id`);--> statement-breakpoint
CREATE TABLE `file_change_scopes` (
	`id` text PRIMARY KEY NOT NULL,
	`source_instance_id` text NOT NULL,
	`device_id` text NOT NULL,
	`workspace_instance_id` text NOT NULL,
	`canonical_root` text NOT NULL,
	`display_root` text NOT NULL,
	`path_flavor` text NOT NULL,
	`status` text DEFAULT 'needs_verification' NOT NULL,
	`root_identity_json` text,
	`revision` integer DEFAULT 0 NOT NULL,
	`fencing_token` integer DEFAULT 0 NOT NULL,
	`active_lease_id` text,
	`active_lease_epoch` text,
	`active_lease_started_at` text,
	`active_mutation_count` integer DEFAULT 0 NOT NULL,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_fc_scope_instance` ON `file_change_scopes` (`source_instance_id`,`device_id`,`workspace_instance_id`);--> statement-breakpoint
CREATE INDEX `idx_fc_scope_root` ON `file_change_scopes` (`source_instance_id`,`device_id`,`canonical_root`);--> statement-breakpoint
CREATE INDEX `idx_fc_scope_status` ON `file_change_scopes` (`status`,`updated_at`);--> statement-breakpoint
CREATE INDEX `idx_fc_scope_active_lease` ON `file_change_scopes` (`device_id`,`active_lease_id`,`canonical_root`);--> statement-breakpoint
CREATE TABLE `file_change_storage_budgets` (
	`id` text PRIMARY KEY NOT NULL,
	`namespace_key` text NOT NULL,
	`status` text DEFAULT 'unverified' NOT NULL,
	`used_bytes` integer DEFAULT 0 NOT NULL,
	`reserved_bytes` integer DEFAULT 0 NOT NULL,
	`quota_bytes` integer NOT NULL,
	`generation` integer DEFAULT 0 NOT NULL,
	`reconciled_at` text,
	`updated_at` text NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_fc_storage_namespace` ON `file_change_storage_budgets` (`namespace_key`);--> statement-breakpoint
CREATE TABLE `narrator_public_shares` (
	`id` text PRIMARY KEY NOT NULL,
	`narrator_id` text NOT NULL,
	`token_hash` text NOT NULL,
	`guest_name` text NOT NULL,
	`label` text,
	`created_by_user_id` text,
	`created_at` text NOT NULL,
	`revoked_at` text,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`created_by_user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_narrator_public_shares_token_hash` ON `narrator_public_shares` (`token_hash`);--> statement-breakpoint
CREATE INDEX `idx_narrator_public_shares_narrator_created` ON `narrator_public_shares` (`narrator_id`,`created_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_narrator_public_shares_created_by` ON `narrator_public_shares` (`created_by_user_id`);--> statement-breakpoint
CREATE TABLE `revert_operation_files` (
	`id` text PRIMARY KEY NOT NULL,
	`revert_operation_id` text NOT NULL,
	`scope_id` text NOT NULL,
	`file_key` text NOT NULL,
	`identity_json` text NOT NULL,
	`sequence` integer NOT NULL,
	`expected_state_json` text NOT NULL,
	`desired_state_json` text NOT NULL,
	`observed_after_state_json` text,
	`before_blob_digest` text,
	`desired_blob_digest` text,
	`observed_after_blob_digest` text,
	`compensation_after_state_json` text,
	`compensation_after_blob_digest` text,
	`apply_mutation_id` text NOT NULL,
	`apply_request_digest` text NOT NULL,
	`compensate_mutation_id` text NOT NULL,
	`compensate_request_digest` text NOT NULL,
	`status` text DEFAULT 'prepared' NOT NULL,
	`receipt_json` text,
	`reason` text,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`revert_operation_id`) REFERENCES `revert_operations`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`scope_id`) REFERENCES `file_change_scopes`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`before_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`desired_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`observed_after_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`compensation_after_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_revert_file_identity` ON `revert_operation_files` (`revert_operation_id`,`file_key`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_revert_file_apply` ON `revert_operation_files` (`apply_mutation_id`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_revert_file_compensate` ON `revert_operation_files` (`compensate_mutation_id`);--> statement-breakpoint
CREATE INDEX `idx_revert_file_pending` ON `revert_operation_files` (`revert_operation_id`,`status`,`sequence`);--> statement-breakpoint
CREATE INDEX `idx_revert_file_before_blob` ON `revert_operation_files` (`before_blob_digest`);--> statement-breakpoint
CREATE INDEX `idx_revert_file_desired_blob` ON `revert_operation_files` (`desired_blob_digest`);--> statement-breakpoint
CREATE INDEX `idx_revert_file_observed_blob` ON `revert_operation_files` (`observed_after_blob_digest`);--> statement-breakpoint
CREATE INDEX `idx_revert_file_compensation_blob` ON `revert_operation_files` (`compensation_after_blob_digest`);--> statement-breakpoint
CREATE TABLE `revert_operations` (
	`id` text PRIMARY KEY NOT NULL,
	`protocol_version` integer DEFAULT 2 NOT NULL,
	`narrator_id` text,
	`project_id` text,
	`requested_by_subject_key` text NOT NULL,
	`idempotency_key` text NOT NULL,
	`request_digest` text NOT NULL,
	`kind` text NOT NULL,
	`scope` text NOT NULL,
	`selector_kind` text NOT NULL,
	`selector_blob_digest` text,
	`plan_blob_digest` text,
	`history_manifest_blob_digest` text,
	`plan_hash` text,
	`expected_message_version` integer,
	`parent_revert_id` text,
	`status` text DEFAULT 'planned' NOT NULL,
	`file_count` integer DEFAULT 0 NOT NULL,
	`applied_file_count` integer DEFAULT 0 NOT NULL,
	`coverage_complete` integer DEFAULT false NOT NULL,
	`reason` text,
	`expires_at` text NOT NULL,
	`lease_until` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`project_id`) REFERENCES `projects`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`selector_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`plan_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`history_manifest_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_revert_operation_request` ON `revert_operations` (`requested_by_subject_key`,`idempotency_key`);--> statement-breakpoint
CREATE INDEX `idx_revert_operation_narrator` ON `revert_operations` (`narrator_id`,`created_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_revert_operation_pending` ON `revert_operations` (`status`,`updated_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_revert_operation_owner_pending` ON `revert_operations` (`requested_by_subject_key`,`narrator_id`,`project_id`,`status`,`updated_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_revert_operation_parent` ON `revert_operations` (`parent_revert_id`);--> statement-breakpoint
CREATE INDEX `idx_revert_operation_plan_blob` ON `revert_operations` (`plan_blob_digest`);--> statement-breakpoint
CREATE INDEX `idx_revert_operation_selector_blob` ON `revert_operations` (`selector_blob_digest`);--> statement-breakpoint
CREATE INDEX `idx_revert_operation_history_blob` ON `revert_operations` (`history_manifest_blob_digest`);--> statement-breakpoint
CREATE TABLE `runtime_publication_outbox` (
	`id` text PRIMARY KEY NOT NULL,
	`producer_kind` text NOT NULL,
	`task_id` text NOT NULL,
	`logical_run_id` text NOT NULL,
	`event_kind` text NOT NULL,
	`recipient_id` text NOT NULL,
	`state` text DEFAULT 'reserved' NOT NULL,
	`arrival_seq` integer,
	`result_ref` text,
	`summary` text,
	`delivery_id` text NOT NULL,
	`dedupe_key` text NOT NULL,
	`last_error` text,
	`created_at` text NOT NULL,
	`updated_at` text NOT NULL
);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_runtime_outbox_event` ON `runtime_publication_outbox` (`producer_kind`,`task_id`,`logical_run_id`,`event_kind`,`recipient_id`);--> statement-breakpoint
CREATE INDEX `idx_runtime_outbox_order` ON `runtime_publication_outbox` (`recipient_id`,`producer_kind`,`state`,`arrival_seq`);--> statement-breakpoint
CREATE INDEX `idx_runtime_outbox_recipient` ON `runtime_publication_outbox` (`recipient_id`);--> statement-breakpoint
CREATE INDEX `idx_runtime_outbox_state` ON `runtime_publication_outbox` (`state`,`id`);--> statement-breakpoint
CREATE TABLE `snapshot_captures` (
	`id` text PRIMARY KEY NOT NULL,
	`scope_id` text NOT NULL,
	`operation_id` text,
	`tree_hash` text,
	`snapshot_commit_sha` text,
	`coverage` text DEFAULT 'unavailable' NOT NULL,
	`temporal_consistency` text DEFAULT 'unknown' NOT NULL,
	`policy_version` integer NOT NULL,
	`ignore_policy_digest` text,
	`manifest_blob_digest` text,
	`omitted_count` integer,
	`reason` text,
	`started_at` text NOT NULL,
	`finished_at` text,
	FOREIGN KEY (`scope_id`) REFERENCES `file_change_scopes`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`operation_id`) REFERENCES `file_change_operations`(`id`) ON UPDATE no action ON DELETE no action,
	FOREIGN KEY (`manifest_blob_digest`) REFERENCES `file_change_blobs`(`digest`) ON UPDATE no action ON DELETE no action
);
--> statement-breakpoint
CREATE INDEX `idx_snapshot_capture_scope` ON `snapshot_captures` (`scope_id`,`started_at`,`id`);--> statement-breakpoint
CREATE INDEX `idx_snapshot_capture_tree` ON `snapshot_captures` (`scope_id`,`tree_hash`);--> statement-breakpoint
CREATE INDEX `idx_snapshot_capture_operation` ON `snapshot_captures` (`operation_id`);--> statement-breakpoint
CREATE INDEX `idx_snapshot_capture_manifest` ON `snapshot_captures` (`manifest_blob_digest`);--> statement-breakpoint
ALTER TABLE `background_tasks` ADD `logical_run_id` text;--> statement-breakpoint
ALTER TABLE `background_tasks` ADD `tool_call_id` text;--> statement-breakpoint
ALTER TABLE `background_tasks` ADD `execution_attempt` integer;--> statement-breakpoint
CREATE UNIQUE INDEX `idx_bg_tasks_tool_attempt` ON `background_tasks` (`tool_call_id`,`execution_attempt`);--> statement-breakpoint
ALTER TABLE `chat_messages` ADD `sender_share_id` text REFERENCES narrator_public_shares(id);--> statement-breakpoint
ALTER TABLE `chat_messages` ADD `sender_guest_name` text;--> statement-breakpoint
ALTER TABLE `chat_messages` ADD `reply_to_guest_name` text;--> statement-breakpoint
CREATE INDEX `idx_chat_messages_sender_share` ON `chat_messages` (`sender_share_id`);--> statement-breakpoint
PRAGMA foreign_keys=OFF;--> statement-breakpoint
CREATE TABLE `__new_file_attributions` (
	`id` text PRIMARY KEY NOT NULL,
	`device_id` text DEFAULT 'local' NOT NULL,
	`workspace_path` text NOT NULL,
	`file_path` text NOT NULL,
	`narrator_id` text,
	`user_id` text,
	`subagent_type` text,
	`action` text NOT NULL,
	`tool_name` text,
	`tool_use_id` text,
	`operation_id` text,
	`effect_id` text,
	`scope_id` text,
	`file_key` text,
	`actor_subject_key` text,
	`actor_snapshot_json` text,
	`attribution_grade` text,
	`lines_added` integer,
	`lines_removed` integer,
	`changed_at` text NOT NULL,
	FOREIGN KEY (`narrator_id`) REFERENCES `narrators`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`user_id`) REFERENCES `users`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`operation_id`) REFERENCES `file_change_operations`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`effect_id`) REFERENCES `file_change_effects`(`id`) ON UPDATE no action ON DELETE set null,
	FOREIGN KEY (`scope_id`) REFERENCES `file_change_scopes`(`id`) ON UPDATE no action ON DELETE set null,
	CONSTRAINT "ck_file_attr_line_counts" CHECK(
			("lines_added" IS NULL OR (typeof("lines_added") = 'integer' AND "lines_added" >= 0))
			AND ("lines_removed" IS NULL OR (typeof("lines_removed") = 'integer' AND "lines_removed" >= 0))
		)
);
--> statement-breakpoint
INSERT INTO `__new_file_attributions`("id", "device_id", "workspace_path", "file_path", "narrator_id", "user_id", "subagent_type", "action", "tool_name", "tool_use_id", "operation_id", "effect_id", "scope_id", "file_key", "actor_subject_key", "actor_snapshot_json", "attribution_grade", "lines_added", "lines_removed", "changed_at") SELECT "id", "device_id", "workspace_path", "file_path", "narrator_id", "user_id", "subagent_type", "action", "tool_name", "tool_use_id", "operation_id", "effect_id", "scope_id", "file_key", "actor_subject_key", "actor_snapshot_json", "attribution_grade", "lines_added", "lines_removed", "changed_at" FROM `file_attributions`;--> statement-breakpoint
DROP TABLE `file_attributions`;--> statement-breakpoint
ALTER TABLE `__new_file_attributions` RENAME TO `file_attributions`;--> statement-breakpoint
PRAGMA foreign_keys=ON;--> statement-breakpoint
CREATE INDEX `idx_file_attr_workspace_file` ON `file_attributions` (`workspace_path`,`file_path`,`changed_at`);--> statement-breakpoint
CREATE INDEX `idx_file_attr_device_workspace_file` ON `file_attributions` (`device_id`,`workspace_path`,`file_path`,`changed_at`);--> statement-breakpoint
CREATE INDEX `idx_file_attr_narrator` ON `file_attributions` (`narrator_id`);--> statement-breakpoint
CREATE INDEX `idx_file_attr_workspace` ON `file_attributions` (`workspace_path`,`changed_at`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_file_attr_effect` ON `file_attributions` (`effect_id`);--> statement-breakpoint
CREATE INDEX `idx_file_attr_operation` ON `file_attributions` (`operation_id`);--> statement-breakpoint
CREATE INDEX `idx_file_attr_scope_file` ON `file_attributions` (`scope_id`,`file_key`,`changed_at`,`id`);--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `file_references_json` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `kind` text DEFAULT 'user_input' NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `notice_kind` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `envelope_version` integer DEFAULT 1 NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `metadata_json` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `source_narrator_id` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `source_tool_call_id` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `source_attempt` integer;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `source_key` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `dedupe_key` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `delivery_id` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `recipient_message_id` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `recipient_ref_id` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `current_message_id` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `content_revision` integer DEFAULT 1 NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `adopted_revision` integer;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `adopted_at` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `current_revision` integer DEFAULT 1 NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `current_adopted_revision` integer;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `current_adopted_at` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `receipt_disposition` text DEFAULT 'active' NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `arrival_seq` integer;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `state` text DEFAULT 'queued' NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `claim_token` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `claim_epoch` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `claimed_at` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `claim_attempts` integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `last_error` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `byte_size` integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `projected_byte_size` integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `payload_ref_json` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `dedupe_expires_at` text;--> statement-breakpoint
ALTER TABLE `narrator_buffered_messages` ADD `updated_at` text;--> statement-breakpoint
CREATE INDEX `idx_nbm_state_arrival` ON `narrator_buffered_messages` (`narrator_id`,`state`,`arrival_seq`);--> statement-breakpoint
CREATE INDEX `idx_nbm_claim_recovery` ON `narrator_buffered_messages` (`state`,`id`);--> statement-breakpoint
CREATE INDEX `idx_nbm_quota` ON `narrator_buffered_messages` (`narrator_id`,`kind`,`notice_kind`,`state`);--> statement-breakpoint
CREATE INDEX `idx_nbm_legacy` ON `narrator_buffered_messages` (`narrator_id`,`arrival_seq`,`seq`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_nbm_dedupe` ON `narrator_buffered_messages` (`narrator_id`,`dedupe_key`);--> statement-breakpoint
CREATE UNIQUE INDEX `idx_nbm_delivery` ON `narrator_buffered_messages` (`delivery_id`);--> statement-breakpoint
CREATE INDEX `idx_nbm_ref` ON `narrator_buffered_messages` (`narrator_id`,`recipient_ref_id`);--> statement-breakpoint
CREATE INDEX `idx_nbm_reserved` ON `narrator_buffered_messages` (`narrator_id`,`recipient_message_id`);--> statement-breakpoint
CREATE INDEX `idx_nbm_source` ON `narrator_buffered_messages` (`source_narrator_id`,`source_tool_call_id`,`source_attempt`);--> statement-breakpoint
ALTER TABLE `narrator_drafts` ADD `file_references_json` text;--> statement-breakpoint
ALTER TABLE `narrator_message_refs` ADD `injection_consumed_at` integer;--> statement-breakpoint
ALTER TABLE `narrator_questions` ADD `execution_principal_json` text;--> statement-breakpoint
ALTER TABLE `narrator_tool_calls` ADD `execution_identity_version` integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_tool_calls` ADD `execution_origin_tool_call_id` text;--> statement-breakpoint
ALTER TABLE `narrator_tool_calls` ADD `execution_attempt` integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE `narrator_tool_calls` ADD `file_change_operation_id` text REFERENCES file_change_operations(id);--> statement-breakpoint
CREATE INDEX `idx_toolcalls_attempt` ON `narrator_tool_calls` (`narrator_id`,`tool_use_id`,`message_id`,`execution_attempt`);--> statement-breakpoint
CREATE INDEX `idx_toolcalls_file_change_operation` ON `narrator_tool_calls` (`file_change_operation_id`);--> statement-breakpoint
ALTER TABLE `narrators` ADD `logical_run_id` text;--> statement-breakpoint
ALTER TABLE `narrators` ADD `inbox_sequence` integer DEFAULT 0 NOT NULL;--> statement-breakpoint
ALTER TABLE `narrators` ADD `origin_tool_call_id` text;--> statement-breakpoint
ALTER TABLE `narrators` ADD `subagent_origin_kind` text;--> statement-breakpoint
CREATE INDEX `idx_narrators_origin_tool_call` ON `narrators` (`origin_tool_call_id`);