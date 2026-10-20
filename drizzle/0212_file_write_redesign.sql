PRAGMA foreign_keys=OFF;
--> statement-breakpoint
CREATE TABLE `__new_file_change_effects` (
	`id` text PRIMARY KEY NOT NULL,
	`operation_id` text NOT NULL,
	`journal_seq` integer,
	`scope_id` text NOT NULL,
	`file_key` text NOT NULL,
	`identity_json` text NOT NULL,
	`scope_revision` integer,
	`mutation_id` text NOT NULL,
	`request_digest` text NOT NULL,
	`phase` text NOT NULL,
	`before_state_json` text NOT NULL,
	`intended_after_state_json` text NOT NULL,
	`observed_after_state_json` text NOT NULL,
	`before_blob_digest` text,
	`intended_after_blob_digest` text,
	`observed_after_blob_digest` text,
	`disk_outcome` text,
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
INSERT INTO `__new_file_change_effects`("id", "operation_id", "journal_seq", "scope_id", "file_key", "identity_json", "scope_revision", "mutation_id", "request_digest", "phase", "before_state_json", "intended_after_state_json", "observed_after_state_json", "before_blob_digest", "intended_after_blob_digest", "observed_after_blob_digest", "outcome", "settlement", "attribution_grade", "attribution_ceiling", "execution_confirmed", "execution_receipt_json", "execution_receipt_digest", "lines_added", "lines_removed", "created_at", "updated_at") SELECT "id", "operation_id", "journal_seq", "scope_id", "file_key", "identity_json", "scope_revision", "mutation_id", "request_digest", "phase", "before_state_json", "intended_after_state_json", "observed_after_state_json", "before_blob_digest", "intended_after_blob_digest", "observed_after_blob_digest", "outcome", "settlement", "attribution_grade", "attribution_ceiling", "execution_confirmed", "execution_receipt_json", "execution_receipt_digest", "lines_added", "lines_removed", "created_at", "updated_at" FROM `file_change_effects`;
--> statement-breakpoint
DROP TABLE `file_change_effects`;
--> statement-breakpoint
ALTER TABLE `__new_file_change_effects` RENAME TO `file_change_effects`;
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_fc_effect_mutation` ON `file_change_effects` (`mutation_id`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_fc_effect_operation_file` ON `file_change_effects` (`operation_id`,`file_key`,`phase`);
--> statement-breakpoint
CREATE INDEX `idx_fc_effect_file` ON `file_change_effects` (`scope_id`,`file_key`,`scope_revision`,`id`);
--> statement-breakpoint
CREATE INDEX `idx_fc_effect_journal` ON `file_change_effects` (`scope_id`,`file_key`,`journal_seq`,`scope_revision`,`id`);
--> statement-breakpoint
CREATE INDEX `idx_fc_effect_pending` ON `file_change_effects` (`settlement`,`updated_at`);
--> statement-breakpoint
CREATE INDEX `idx_fc_effect_before_blob` ON `file_change_effects` (`before_blob_digest`);
--> statement-breakpoint
CREATE INDEX `idx_fc_effect_intended_blob` ON `file_change_effects` (`intended_after_blob_digest`);
--> statement-breakpoint
CREATE INDEX `idx_fc_effect_observed_blob` ON `file_change_effects` (`observed_after_blob_digest`);
--> statement-breakpoint
CREATE TABLE `__new_revert_operation_files` (
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
	`compensate_mutation_id` text,
	`compensate_request_digest` text,
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
INSERT INTO `__new_revert_operation_files`("id", "revert_operation_id", "scope_id", "file_key", "identity_json", "sequence", "expected_state_json", "desired_state_json", "observed_after_state_json", "before_blob_digest", "desired_blob_digest", "observed_after_blob_digest", "compensation_after_state_json", "compensation_after_blob_digest", "apply_mutation_id", "apply_request_digest", "compensate_mutation_id", "compensate_request_digest", "status", "receipt_json", "reason", "updated_at") SELECT "id", "revert_operation_id", "scope_id", "file_key", "identity_json", "sequence", "expected_state_json", "desired_state_json", "observed_after_state_json", "before_blob_digest", "desired_blob_digest", "observed_after_blob_digest", "compensation_after_state_json", "compensation_after_blob_digest", "apply_mutation_id", "apply_request_digest", "compensate_mutation_id", "compensate_request_digest", "status", "receipt_json", "reason", "updated_at" FROM `revert_operation_files`;
--> statement-breakpoint
DROP TABLE `revert_operation_files`;
--> statement-breakpoint
ALTER TABLE `__new_revert_operation_files` RENAME TO `revert_operation_files`;
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_revert_file_identity` ON `revert_operation_files` (`revert_operation_id`,`file_key`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_revert_file_apply` ON `revert_operation_files` (`apply_mutation_id`);
--> statement-breakpoint
CREATE UNIQUE INDEX `idx_revert_file_compensate` ON `revert_operation_files` (`compensate_mutation_id`);
--> statement-breakpoint
CREATE INDEX `idx_revert_file_pending` ON `revert_operation_files` (`revert_operation_id`,`status`,`sequence`);
--> statement-breakpoint
CREATE INDEX `idx_revert_file_before_blob` ON `revert_operation_files` (`before_blob_digest`);
--> statement-breakpoint
CREATE INDEX `idx_revert_file_desired_blob` ON `revert_operation_files` (`desired_blob_digest`);
--> statement-breakpoint
CREATE INDEX `idx_revert_file_observed_blob` ON `revert_operation_files` (`observed_after_blob_digest`);
--> statement-breakpoint
CREATE INDEX `idx_revert_file_compensation_blob` ON `revert_operation_files` (`compensation_after_blob_digest`);
--> statement-breakpoint
PRAGMA foreign_keys=ON;
