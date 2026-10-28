import { Database } from "bun:sqlite";
import { createHash } from "node:crypto";
import { createReadStream, readFileSync, realpathSync } from "node:fs";
import { resolve } from "node:path";
import { readMigrationFiles } from "drizzle-orm/migrator";
import { applySqliteDataBackfills } from "../server/db/data-backfills";
import { ensureColumns } from "../server/db/ensure-columns";
import { dropRetiredFtsObjects } from "../server/db/fts";
import { runMigrations } from "../server/db/run-migrations";

const ALLOWED_TARGET_TAGS = new Set([
	"0012_curved_bloodstorm",
	"0013_omniscient_dormammu",
	"0014_blue_tomorrow_man",
	"0015_nifty_magma",
	"0016_high_shiva",
	"0017_merge_opensource_083",
	"0018_merge_upstream_090",
	"0204_merge_selfuse_092",
	"0205_merge_upstream_093",
	"0212_file_write_redesign",
	"0225_high_baron_zemo",
	"0226_merge_selfuse_098",
	"0227_merge_selfuse_099",
]);
const JOURNAL_TAIL_TAGS = new Set([
	"0013_omniscient_dormammu",
	"0014_blue_tomorrow_man",
	"0015_nifty_magma",
	"0016_high_shiva",
	"0017_merge_opensource_083",
	"0018_merge_upstream_090",
	"0204_merge_selfuse_092",
	"0205_merge_upstream_093",
	"0212_file_write_redesign",
	"0225_high_baron_zemo",
	"0226_merge_selfuse_098",
	"0227_merge_selfuse_099",
]);
const MAX_REPORT_BYTES = 64 * 1024;
const MERGE_099_NEW_TABLES = [
	"outline_block_versions", "outline_blocks", "outline_commits", "outline_links",
	"outline_live_updates", "outline_spaces", "outline_view_state",
];
const MERGE_090_NEW_TABLES = ["user_git_identities", "narrator_git_identity_bindings"];
const MERGE_092_TAGS = [
	"0196_narrator_model_history_revision", "0197_fluffy_the_professor",
	"0198_broken_viper", "0199_sudden_richard_fisk", "0200_faithful_major_mapleleaf",
	"0201_mute_white_tiger", "0202_personal_knowledge_injection",
	"0203_magical_speed_demon", "0204_merge_selfuse_092",
];
const MERGE_092_NEW_TABLES = [
	"model_history_revision_contracts", "usage_token_repair_candidates", "usage_token_repair_jobs",
	"resource_protection_paths", "resource_protection_sources",
];
const MERGE_093_NEW_TABLES = ["narrator_context_pins", "narrator_message_context_stats"];
const MERGE_093_REMOVED_TABLES = [
	"narrator_context_char_pages",
	"usage_token_repair_candidates",
	"usage_token_repair_jobs",
];
const MERGE_093_REMOVED_API_COLUMNS = [
	"token_accounting_version",
	"token_accounting_review_version",
	"raw_dump_json",
];
const MERGE_093_ADDED_COLUMNS: Record<string, string[]> = {
	api_requests: ["diagnostics_json", "recording_request_id"],
	narrator_drafts: ["composer_document"],
	narrator_messages: ["composer_document"],
};
const MERGE_095_TAGS = [
	"0209_unified-runtime-registration",
	"0210_fresh_mephisto",
	"0211_editor_operation_recovery",
	"0212_file_write_redesign",
];
const MERGE_095_ADDED_COLUMNS: Record<string, string[]> = {
	narrators: ["runtime_registration_json", "policy_source_narrator_id"],
	narrator_drafts: ["previous_json"],
	file_change_operations: ["workspace_lease_id", "editor_raw_snapshot_revision"],
	file_change_effects: ["disk_outcome"],
	revert_operation_files: [],
};
const MERGE_098_TAGS = [
	"0213_block_storage", "0214_block_backfill", "0215_legacy_body_index",
	"0216_block_state_writes", "0217_bot_rooms", "0218_retire_narrative_line",
	"0219_project_index", "0220_hard_sabretooth", "0221_cloudy_kree",
	"0222_clean_thunderbolts", "0223_overconfident_shockwave",
	"0224_auth_sessions", "0225_high_baron_zemo", "0226_merge_selfuse_098",
];
const MERGE_098_TARGETS = new Set(MERGE_098_TAGS.slice(-2));
const MERGE_098_REMOVED_TABLES = [
	"chapter_commits", "chapter_edges", "chapters", "exploration_groups", "merge_sessions", "review_conclusions",
	"chapters_fts", "chapters_fts_data", "chapters_fts_idx", "chapters_fts_docsize", "chapters_fts_config",
];
const MERGE_098_NEW_TABLES = [
	"narrator_block_assets", "narrator_block_sync_queue", "narrator_blocks",
	"narrator_text_snapshots", "narrator_turns", "narrator_block_backfill",
	"narrator_block_diagnostics", "narrator_project_links", "auth_sessions", "narrator_block_index_queue",
];
const MERGE_098_REBUILT_TABLES = [
	"container_instances", "gateway_session_mappings", "narrators", "port_allocations",
	"scheduled_tasks", "terminal_view_state", "terminals", "volume_snapshot_applications",
	"volume_snapshots", "file_attributions",
];
// Exclude only reviewed additions and retired chapter fields from before/after digests.
// The local turn_ended_at and reasoning_effort columns remain included and must keep their values.
const MERGE_098_CHANGED_COLUMNS: Record<string, string[]> = {
	narrators: ["chapter_id"],
	scheduled_tasks: ["chapter_id"],
	container_instances: ["chapter_id"],
	gateway_session_mappings: ["chapter_id"],
	port_allocations: ["chapter_id"],
	terminal_view_state: ["chapter_id"],
	terminals: ["chapter_id", "graph_height", "graph_opened", "graph_width", "graph_x", "graph_y"],
	volume_snapshot_applications: ["chapter_id"],
	volume_snapshots: ["source_chapter_id"],
	notifications: ["chapter_id"],
	projects: ["copy_files", "flow_mode", "proxy_domain", "startup_script", "root_key"],
	chat_messages: ["sender_narrator_id", "reply_to_sender_narrator_id"],
	narrator_messages: [
		"body_format", "turn_id", "author_narrator_id", "cause_block_id", "provider_response_id",
		"body_bytes", "state_revision", "body_rows_only", "blocks_content_hash",
	],
	narrator_tool_calls: ["block_id", "is_internal"],
	file_attributions: [],
};

const LEGACY_GIT_IDENTITY_FILTER = `u.git_username IS NOT NULL AND trim(u.git_username) <> ''
	AND u.git_email IS NOT NULL AND trim(u.git_email) <> ''
	AND instr(u.git_username, '<') = 0 AND instr(u.git_username, '>') = 0
	AND instr(u.git_email, '<') = 0 AND instr(u.git_email, '>') = 0`;
const MERGE_083_NEW_TABLES = [
	"narrator_context_char_pages",
	"narrator_question_events",
	"narrator_worktree_resources",
	"permission_rule_requests",
	"runtime_awaited_terminal_consumptions",
];
const MERGE_083_ADDED_COLUMNS: Record<string, string[]> = {
	api_requests: ["context_usage_snapshot_json"],
	narrator_messages: ["context_chars_json"],
	narrator_questions: ["context", "resolution_json", "withdraw_reason", "summary_json"],
	narrator_tool_calls: ["input_chars", "output_chars"],
	narrators: [
		"scheduled_task_id",
		"context_summary_chars",
		"context_system_chars",
		"context_tools_chars",
		"context_char_revision",
		"context_usage_snapshot_json",
		"context_char_cache_json",
		"last_stop_reason",
		"workspace_revision",
		"workspace_context",
	],
	scheduled_tasks: ["cleanup_policy"],
	user_preferences: ["treat_as_local_access"],
	volume_snapshots: ["source_worktree_resource_id"],
	container_instances: ["worktree_resource_id"],
	volume_snapshot_applications: ["target_worktree_resource_id"],
	port_allocations: ["worktree_resource_id"],
	terminal_view_state: ["worktree_resource_id"],
	terminals: ["worktree_resource_id"],
};

type Args = {
	database: string;
	migrations: string;
	onlineDatabase: string;
	backupOriginal: string;
	backupSha256: string;
	sourceSha256: string;
	commit: string;
	version: string;
};

function parseArgs(argv: string[]): Args {
	const values = new Map<string, string>();
	for (let i = 0; i < argv.length; i += 2) {
		const key = argv[i];
		const value = argv[i + 1];
		if (!key?.startsWith("--") || value === undefined) throw new Error("Invalid arguments");
		values.set(key.slice(2), value);
	}
	const take = (key: string) => {
		const value = values.get(key)?.trim();
		if (!value) throw new Error(`Missing --${key}`);
		return value;
	};
	return {
		database: take("database"),
		migrations: take("migrations"),
		onlineDatabase: take("online-database"),
		backupOriginal: take("backup-original"),
		backupSha256: take("backup-sha256"),
		sourceSha256: take("source-sha256"),
		commit: take("commit"),
		version: take("version"),
	};
}

async function sha256(path: string): Promise<string> {
	const hash = createHash("sha256");
	for await (const chunk of createReadStream(path)) hash.update(chunk);
	return hash.digest("hex");
}

function quoteIdentifier(value: string): string {
	return `"${value.replaceAll('"', '""')}"`;
}

function tableExists(db: Database, table: string): boolean {
	return db.query("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?").get(table) != null;
}

function columns(db: Database, table: string): string[] {
	return (
		db.query(`PRAGMA table_info(${quoteIdentifier(table)})`).all() as Array<{ name: string }>
	).map((row) => row.name);
}

function rowCounts(db: Database): Record<string, number> {
	const tables = db
		.query(
			"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name != '__drizzle_migrations' ORDER BY name",
		)
		.all() as Array<{ name: string }>;
	return Object.fromEntries(
		tables.map(({ name }) => {
			const row = db.query(`SELECT count(*) AS count FROM ${quoteIdentifier(name)}`).get() as {
				count: number;
			};
			return [name, Number(row.count)];
		}),
	);
}

function encodeSqlValue(value: unknown): unknown {
	if (value instanceof Uint8Array) return { blobBase64: Buffer.from(value).toString("base64") };
	if (typeof value === "bigint") return { integer: value.toString() };
	return value;
}

function tableDigest(
	db: Database,
	table: string,
	excluded: ReadonlySet<string> = new Set(),
	key = "id",
): Record<string, unknown> {
	if (!tableExists(db, table)) throw new Error(`Missing required table: ${table}`);
	const selected = columns(db, table).filter((column) => !excluded.has(column)).sort();
	if (!selected.includes(key)) throw new Error(`Missing stable row key: ${table}.${key}`);
	const statement = db.prepare(
		`SELECT ${selected.map(quoteIdentifier).join(", ")} FROM ${quoteIdentifier(table)} ORDER BY ${quoteIdentifier(key)}`,
	);
	const hash = createHash("sha256");
	hash.update(`${JSON.stringify(selected)}\n`);
	let rows = 0;
	try {
		for (const row of statement.iterate() as Iterable<Record<string, unknown>>) {
			hash.update(`${JSON.stringify(selected.map((column) => encodeSqlValue(row[column])))}\n`);
			rows++;
		}
	} finally {
		statement.finalize();
	}
	return { columns: selected, rows, sha256: hash.digest("hex") };
}

function affectedSummary(
	db: Database,
	targetTag: string,
	excludeNew: boolean,
): Record<string, unknown> {
	if (targetTag === "0227_merge_selfuse_099") {
		return {
			api_requests: tableDigest(
				db, "api_requests", excludeNew ? new Set(["token_accounting_version"]) : new Set(),
			),
		};
	}
	if (MERGE_098_TARGETS.has(targetTag)) {
		return Object.fromEntries(
			Object.entries(MERGE_098_CHANGED_COLUMNS).map(([table, changed]) => [
				table,
				tableDigest(db, table, excludeNew ? new Set(changed) : new Set(), table === "port_allocations" ? "port" : "id"),
			]),
		);
	}
	if (targetTag === "0212_file_write_redesign") {
		return Object.fromEntries(
			Object.entries(MERGE_095_ADDED_COLUMNS).map(([table, added]) => [
				table,
				tableDigest(db, table, excludeNew ? new Set(added) : new Set()),
			]),
		);
	}
	if (targetTag === "0205_merge_upstream_093") {
		return Object.fromEntries(
			Object.entries(MERGE_093_ADDED_COLUMNS).map(([table, added]) => [
				table,
				tableDigest(db, table, new Set([
					...(table === "api_requests" ? MERGE_093_REMOVED_API_COLUMNS : []),
					...(excludeNew ? added : []),
				])),
			]),
		);
	}
	if (targetTag === "0204_merge_selfuse_092") {
		return {
			narrators: tableDigest(db, "narrators", excludeNew ? new Set(["model_history_revision"]) : new Set()),
			scheduled_tasks: tableDigest(db, "scheduled_tasks"),
			file_attributions: tableDigest(db, "file_attributions"),
			knowledge_injection_events: tableDigest(db, "knowledge_injection_events", excludeNew ? new Set(["personal_entry_id"]) : new Set()),
		};
	}
	if (targetTag === "0018_merge_upstream_090") {
		return {
			users: tableDigest(db, "users"),
			background_tasks: tableDigest(
				db,
				"background_tasks",
				excludeNew ? new Set(["background_kind"]) : new Set(),
			),
			...(excludeNew
				? {}
				: Object.fromEntries(MERGE_090_NEW_TABLES.map((table) => [table, tableDigest(db, table)]))),
		};
	}
	if (targetTag === "0017_merge_opensource_083") {
		return Object.fromEntries(
			Object.entries(MERGE_083_ADDED_COLUMNS).map(([table, added]) => [
				table,
				tableDigest(
					db,
					table,
					excludeNew ? new Set(added) : new Set(),
					table === "port_allocations" ? "port" : "id",
				),
			]),
		);
	}
	if (targetTag === "0016_high_shiva") {
		return { narrator_message_refs: tableDigest(db, "narrator_message_refs") };
	}
	if (targetTag === "0015_nifty_magma") {
		return {
			narrators: tableDigest(db, "narrators", excludeNew ? new Set(["turn_ended_at"]) : new Set()),
		};
	}
	const is0014 = targetTag === "0014_blue_tomorrow_man";
	const summary: Record<string, unknown> = {
		narrators: tableDigest(db, "narrators", excludeNew ? new Set(["next_seq"]) : new Set()),
		narrator_tool_calls: tableDigest(
			db,
			"narrator_tool_calls",
			excludeNew
				? new Set(
						is0014
							? ["cost_status", "cost_missing_fields"]
							: ["stream_completed_at", "cost_status", "cost_missing_fields"],
					)
				: new Set(),
		),
	};
	if (targetTag === "0013_omniscient_dormammu" || is0014) {
		summary.file_change_scope_recoveries = tableDigest(
			db,
			"file_change_scope_recoveries",
			excludeNew
				? new Set(
						is0014
							? ["resolution_authority", "maintenance_evidence_json"]
							: ["workspace_lease_id", "resolution_authority", "maintenance_evidence_json"],
					)
				: new Set(),
		);
	}
	if (is0014) {
		for (const [table, added, key] of [
			["api_requests", ["user_id", "cost_status", "cost_missing_fields"], "id"],
			["credential_usage_totals", ["partial_request_count"], "id"],
			["narrator_messages", ["cost_status", "cost_missing_fields"], "id"],
			["workspace_write_leases", ["execution_class", "termination_evidence_json"], "lease_id"],
		] as const) {
			summary[table] = tableDigest(db, table, excludeNew ? new Set<string>(added) : new Set(), key);
		}
	}
	return summary;
}

function columnShape(db: Database, table: string, column: string): unknown[] {
	return (
		db.query(`PRAGMA table_info(${quoteIdentifier(table)})`).all() as Array<Record<string, unknown>>
	)
		.filter((row) => row.name === column)
		.map(({ cid: _cid, ...rest }) => rest);
}

function tableSchemaShape(db: Database, table: string): Record<string, unknown> {
	const indexes = (
		db.query(`PRAGMA index_list(${quoteIdentifier(table)})`).all() as Array<
			Record<string, unknown> & { name: string }
		>
	)
		.map(({ seq: _seq, ...index }) => ({
			...index,
			columns: db.query(`PRAGMA index_xinfo(${quoteIdentifier(index.name)})`).all(),
		}))
		.sort((left, right) => String(left.name).localeCompare(String(right.name)));
	return {
		tableXInfo: db.query(`PRAGMA table_xinfo(${quoteIdentifier(table)})`).all(),
		foreignKeys: db.query(`PRAGMA foreign_key_list(${quoteIdentifier(table)})`).all(),
		indexes,
	};
}

function narratorRefsIndexShape(db: Database) {
	const name = "idx_narrator_refs_narrator_id";
	const index = (
		db.query(`PRAGMA index_list(${quoteIdentifier("narrator_message_refs")})`).all() as Array<{
			name: string;
			unique: number;
			partial: number;
		}>
	).find((row) => row.name === name);
	if (!index || index.unique !== 0 || index.partial !== 0) {
		throw new Error(`${name} must exist and be non-unique and non-partial`);
	}
	const indexColumns = (
		db.query(`PRAGMA index_info(${quoteIdentifier(name)})`).all() as Array<{
			seqno: number;
			name: string | null;
		}>
	)
		.sort((left, right) => left.seqno - right.seqno)
		.map((row) => row.name);
	if (JSON.stringify(indexColumns) !== JSON.stringify(["narrator_id", "id"])) {
		throw new Error(`${name} columns must be narrator_id, id in that order`);
	}
	return { name: index.name, unique: index.unique, partial: index.partial, columns: indexColumns };
}

function schemaShape(db: Database, targetTag?: string): Record<string, unknown> {
	if (targetTag === "0227_merge_selfuse_099") {
		return {
			...Object.fromEntries(MERGE_099_NEW_TABLES.map((table) => [table, tableSchemaShape(db, table)])),
			tokenAccountingVersion: columnShape(db, "api_requests", "token_accounting_version"),
			tokenAccountingIndex: db.query("SELECT sql FROM sqlite_master WHERE type='index' AND name=?")
				.get("idx_api_requests_token_accounting_pending"),
		};
	}
	if (targetTag && MERGE_098_TARGETS.has(targetTag)) {
		const shape: Record<string, unknown> = {};
		for (const table of [...MERGE_098_NEW_TABLES, ...MERGE_098_REBUILT_TABLES]) {
			shape[table] = tableSchemaShape(db, table);
		}
		for (const [table, names] of Object.entries(MERGE_098_CHANGED_COLUMNS)) {
			for (const name of names) shape[`${table}.${name}`] = columnShape(db, table, name);
		}
		for (const table of MERGE_098_REMOVED_TABLES) {
			if (tableExists(db, table)) throw new Error(`Removed table still exists: ${table}`);
		}
		for (const name of [
			"idx_messages_author", "idx_messages_legacy_body", "idx_messages_rows_only",
			"idx_toolcalls_block", "idx_chat_rooms_narrator_id", "idx_chat_rooms_narrator",
			"idx_chat_messages_sender_narrator", "uq_projects_root_key",
		]) {
			const index = db.query("SELECT sql FROM sqlite_master WHERE type='index' AND name=?").get(name);
			if (!index) throw new Error(`Missing index: ${name}`);
			shape[name] = index;
		}
		for (const [table, from] of [["narrator_tool_calls", "block_id"], ["chat_messages", "sender_narrator_id"]]) {
			shape[`${table}.${from}.fk`] = (db.query(`PRAGMA foreign_key_list(${quoteIdentifier(table)})`).all() as Array<Record<string, unknown>>)
				.filter((key) => key.from === from).map(({ id: _id, ...key }) => key);
		}
		shape.historyTriggers = db.query("SELECT name, sql FROM sqlite_master WHERE type='trigger' AND name LIKE 'model_history_v3_%' ORDER BY name").all();
		return shape;
	}
	const shape: Record<string, unknown> = {
		fileChangeScopeRecoveries: tableSchemaShape(db, "file_change_scope_recoveries"),
		workspaceWriteLeases: tableSchemaShape(db, "workspace_write_leases"),
		narratorsNextSeq: columnShape(db, "narrators", "next_seq"),
		toolCallsStreamCompletedAt: columnShape(db, "narrator_tool_calls", "stream_completed_at"),
	};
	if (targetTag === "0212_file_write_redesign") {
		for (const [table, added] of Object.entries(MERGE_095_ADDED_COLUMNS)) {
			for (const column of added) shape[`${table}.${column}`] = columnShape(db, table, column);
		}
		for (const table of ["file_change_effects", "revert_operation_files"]) {
			shape[table] = tableSchemaShape(db, table);
		}
		const index = db.query(
			"SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_narrators_policy_source'",
		).get();
		if (!index) throw new Error("Missing index: idx_narrators_policy_source");
		shape.policySourceIndex = index;
		shape.policySourceForeignKey = (
			db.query("PRAGMA foreign_key_list(narrators)").all() as Array<{ from: string }>
		).filter((key) => key.from === "policy_source_narrator_id");
	}
	if (targetTag === "0205_merge_upstream_093") {
		for (const table of MERGE_093_NEW_TABLES) shape[table] = tableSchemaShape(db, table);
		for (const [table, added] of Object.entries(MERGE_093_ADDED_COLUMNS)) {
			for (const column of added) shape[`${table}.${column}`] = columnShape(db, table, column);
		}
		for (const table of MERGE_093_REMOVED_TABLES) {
			if (tableExists(db, table)) throw new Error(`Removed table still exists: ${table}`);
		}
		const apiColumns = columns(db, "api_requests");
		for (const column of MERGE_093_REMOVED_API_COLUMNS) {
			if (apiColumns.includes(column)) throw new Error(`Removed API column still exists: ${column}`);
		}
		for (const name of ["idx_api_requests_token_repair_pending", "idx_api_requests_token_repair_reviewed"]) {
			if (db.query("SELECT 1 FROM sqlite_master WHERE type='index' AND name=?").get(name)) {
				throw new Error(`Removed index still exists: ${name}`);
			}
		}
		for (const name of ["idx_api_requests_recording", "idx_toolcalls_context_id"]) {
			const index = db.query("SELECT sql FROM sqlite_master WHERE type='index' AND name=?").get(name);
			if (!index) throw new Error(`Missing index: ${name}`);
			shape[name] = index;
		}
		const triggers = db.query("SELECT name, sql FROM sqlite_master WHERE type='trigger' AND name IN ('narrator_message_context_stats_insert', 'narrator_message_context_stats_update') ORDER BY name").all();
		if (triggers.length !== 2) throw new Error("Message context stats triggers missing");
		shape.messageContextStatsTriggers = triggers;
	}
	if (targetTag === "0204_merge_selfuse_092") {
		for (const table of [...MERGE_092_NEW_TABLES, "file_attributions", "knowledge_injection_events"]) shape[table] = tableSchemaShape(db, table);
		for (const [table, names] of [
			["narrators", ["model_history_revision", "turn_ended_at"]],
			["narrator_messages", ["body_revision"]],
			["narrator_tool_calls", ["model_revision"]],
			["user_preferences", ["spec_tab_layout"]],
			["api_requests", ["token_accounting_version", "token_accounting_review_version"]],
			["scheduled_tasks", ["reasoning_effort"]],
		] as const) for (const name of names) shape[`${table}.${name}`] = columnShape(db, table, name);
		shape.modelHistoryTriggers = db.query("SELECT name, sql FROM sqlite_master WHERE type='trigger' AND name LIKE 'model_history_v1_%' ORDER BY name").all();
		for (const name of ["idx_api_requests_token_repair_pending", "idx_api_requests_token_repair_reviewed"]) {
			const index = db.query("SELECT sql FROM sqlite_master WHERE type='index' AND name=?").get(name);
			if (!index) throw new Error(`Missing index: ${name}`);
			shape[name] = index;
		}
	}
	if (targetTag === "0018_merge_upstream_090") {
		for (const table of MERGE_090_NEW_TABLES) shape[table] = tableSchemaShape(db, table);
		shape["background_tasks.background_kind"] = columnShape(
			db,
			"background_tasks",
			"background_kind",
		);
	}
	if (targetTag === "0017_merge_opensource_083") {
		for (const table of [
			...MERGE_083_NEW_TABLES,
			"container_instances",
			"volume_snapshot_applications",
			"port_allocations",
			"terminal_view_state",
			"terminals",
		]) {
			shape[table] = tableSchemaShape(db, table);
		}
		for (const [table, added] of Object.entries(MERGE_083_ADDED_COLUMNS)) {
			for (const column of added) shape[`${table}.${column}`] = columnShape(db, table, column);
		}
		for (const name of [
			"idx_narrator_questions_answer_message",
			"idx_narrator_questions_created",
			"idx_toolcalls_context_id",
			"idx_toolcalls_context_order",
			"idx_toolcalls_context_latest",
			"idx_narrators_scheduled_task_created",
			"idx_scheduled_tasks_last_narrator",
			"idx_volume_snapshots_source_resource",
			"idx_fc_segment_input",
		]) {
			const index = db
				.query("SELECT sql FROM sqlite_master WHERE type='index' AND name=?")
				.get(name) as { sql: string } | null;
			if (!index) throw new Error(`Missing index: ${name}`);
			shape[name] = index.sql;
		}
	}
	if (targetTag === "0016_high_shiva" || targetTag === "0017_merge_opensource_083") {
		shape["narrator_message_refs.idx_narrator_refs_narrator_id"] = narratorRefsIndexShape(db);
	}
	if (targetTag === "0015_nifty_magma") {
		shape["narrators.turn_ended_at"] = columnShape(db, "narrators", "turn_ended_at");
	}
	if (targetTag === "0014_blue_tomorrow_man") {
		for (const table of [
			"notifications",
			"user_usage_totals",
			"workspace_execution_owners",
			"api_requests",
			"credential_usage_totals",
			"narrator_tool_calls",
		])
			shape[table] = tableSchemaShape(db, table);
		// Legacy message migrations arrange existing columns differently; compare the
		// two added fields by definition, not their physical cid/index positions.
		for (const column of ["cost_status", "cost_missing_fields"]) {
			shape[`narrator_messages.${column}`] = columnShape(db, "narrator_messages", column);
		}
	}
	return shape;
}

function ledger(db: Database): string[] {
	return (
		db.query("SELECT hash FROM __drizzle_migrations ORDER BY created_at, id").all() as Array<{
			hash: string;
		}>
	).map((row) => row.hash);
}

function assertIntegrity(db: Database): void {
	const quick = db.query("PRAGMA quick_check").all() as Array<Record<string, unknown>>;
	if (quick.length !== 1 || Object.values(quick[0])[0] !== "ok")
		throw new Error("quick_check failed");
	const foreignKeys = db.query("PRAGMA foreign_key_check").all();
	if (foreignKeys.length) throw new Error(`foreign_key_check failed (${foreignKeys.length})`);
}

function canonical(value: unknown): string {
	const normalize = (item: unknown): unknown => {
		if (Array.isArray(item)) return item.map(normalize);
		if (item && typeof item === "object") {
			return Object.fromEntries(
				Object.entries(item as Record<string, unknown>)
					.sort(([left], [right]) => left.localeCompare(right))
					.map(([key, child]) => [key, normalize(child)]),
			);
		}
		return item;
	};
	return JSON.stringify(normalize(value));
}

async function runUpgrade(db: Database): Promise<void> {
	await runMigrations(db);
	ensureColumns(db);
	await applySqliteDataBackfills(db);
}

async function main() {
	const args = parseArgs(Bun.argv.slice(2));
	const database = realpathSync(args.database);
	const online = realpathSync(args.onlineDatabase);
	const backupOriginal = realpathSync(args.backupOriginal);
	if (database === online || database === backupOriginal)
		throw new Error("Validation database is not isolated");
	if (!process.env.NARRAFORK_HOME?.trim()) throw new Error("NARRAFORK_HOME isolation is required");
	if (!/^[0-9a-f]{64}$/.test(args.backupSha256) || !/^[0-9a-f]{64}$/.test(args.sourceSha256)) {
		throw new Error("Invalid sha256 binding");
	}
	if ((await sha256(database)) !== args.backupSha256)
		throw new Error("Validation copy hash mismatch");

	const migrations = realpathSync(args.migrations);
	const repo = resolve(migrations, "..");
	if (migrations !== realpathSync(resolve(repo, "drizzle")))
		throw new Error("Unexpected migrations path");
	process.chdir(repo);
	const migrationFiles = readMigrationFiles({ migrationsFolder: migrations });
	const journal = JSON.parse(readFileSync(resolve(migrations, "meta/_journal.json"), "utf8")) as {
		entries?: Array<{ tag?: string }>;
	};
	const tags = journal.entries?.map((entry) => entry.tag) ?? [];
	if (tags.length !== migrationFiles.length || !JOURNAL_TAIL_TAGS.has(tags.at(-1) ?? "")) {
		throw new Error("Unexpected migration journal tail");
	}
	const migrationsWithTags = migrationFiles.map((migration, index) => ({
		...migration,
		tag: tags[index],
	}));

	const db = new Database(database, { strict: true });
	db.exec("PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON");
	try {
		const beforeLedger = new Set(ledger(db));
		const pending = migrationsWithTags.filter((migration) => !beforeLedger.has(migration.hash));
		const target = pending.at(-1);
		const merge092Chain = canonical(pending.map((migration) => migration.tag)) === canonical(MERGE_092_TAGS);
		const merge095Chain =
			canonical(pending.map((migration) => migration.tag)) === canonical(MERGE_095_TAGS);
		// 0226 can share an already-applied local migration hash; both complete tails are reviewed.
		const merge098Chain = [MERGE_098_TAGS, MERGE_098_TAGS.slice(0, -1)].some(
			(chain) => canonical(pending.map((migration) => migration.tag)) === canonical(chain),
		);
		const merge083Pair =
			pending.length === 2 &&
			pending[0].tag === "0016_high_shiva" &&
			target?.tag === "0017_merge_opensource_083";
		if (
			(!merge083Pair && !merge092Chain && !merge095Chain && !merge098Chain && pending.length !== 1) ||
			!target?.tag ||
			!ALLOWED_TARGET_TAGS.has(target.tag) ||
			(target.tag === "0204_merge_selfuse_092" && !merge092Chain) ||
			(target.tag === "0212_file_write_redesign" && !merge095Chain) ||
			(MERGE_098_TARGETS.has(target.tag) && !merge098Chain)
		) {
			throw new Error(
				"Validation requires a reviewed migration, pair, or complete migration chain",
			);
		}
		const affectedForUpgrade = (excludeNew: boolean) => ({
			...affectedSummary(db, target.tag, excludeNew),
			...(merge083Pair ? affectedSummary(db, "0016_high_shiva", excludeNew) : {}),
		});
		const beforeCounts = rowCounts(db);
		const legacyGitIdentityCount =
			target.tag === "0018_merge_upstream_090" && !("user_git_identities" in beforeCounts)
				? Number(
						(
							db
								.query(`SELECT count(*) AS count FROM users u WHERE ${LEGACY_GIT_IDENTITY_FILTER}`)
								.get() as { count: number }
						).count,
					)
				: null;
		const beforeAffected = affectedForUpgrade(true);
		const hadNextSeq = columns(db, "narrators").includes("next_seq");
		if (hadNextSeq) {
			db.exec("CREATE TEMP TABLE validation_next_seq_before AS SELECT id, next_seq FROM narrators");
		}

		await runUpgrade(db);
		if (merge098Chain) dropRetiredFtsObjects(db);
		assertIntegrity(db);
		const applied = new Set(ledger(db));
		if (!pending.every((migration) => applied.has(migration.hash))) {
			throw new Error("Target migration hash was not applied");
		}
		const expectedCounts = { ...beforeCounts };
		const newTables = target.tag === "0227_merge_selfuse_099" ? MERGE_099_NEW_TABLES :
			merge098Chain ? MERGE_098_NEW_TABLES :
			target.tag === "0012_curved_bloodstorm"
				? ["file_change_scope_recoveries"]
				: target.tag === "0013_omniscient_dormammu"
					? ["workspace_write_leases"]
					: target.tag === "0014_blue_tomorrow_man"
						? ["notifications", "user_usage_totals", "workspace_execution_owners"]
						: target.tag === "0017_merge_opensource_083"
							? MERGE_083_NEW_TABLES
							: target.tag === "0018_merge_upstream_090"
								? MERGE_090_NEW_TABLES
								: target.tag === "0204_merge_selfuse_092" ? MERGE_092_NEW_TABLES
									: target.tag === "0205_merge_upstream_093" ? MERGE_093_NEW_TABLES : [];
		for (const table of newTables) if (!(table in expectedCounts)) expectedCounts[table] = 0;
		if (merge098Chain) for (const table of MERGE_098_REMOVED_TABLES) delete expectedCounts[table];
		if (target.tag === "0205_merge_upstream_093") {
			for (const table of MERGE_093_REMOVED_TABLES) delete expectedCounts[table];
			expectedCounts.narrator_message_context_stats = beforeCounts.narrator_messages ?? 0;
			const mismatchedStats = db.query(`SELECT 1 FROM narrator_messages m
				LEFT JOIN narrator_message_context_stats s ON s.message_id = m.id
				WHERE s.message_id IS NULL OR s.role IS NOT m.role
				OR s.parent_tool_use_id IS NOT m.parent_tool_use_id
				OR s.segments_json IS NOT COALESCE(m.context_chars_json, '{"segments":[]}')
				LIMIT 1`).get();
			if (mismatchedStats) throw new Error("Message context stats backfill changed original values");
		}
		if (merge092Chain) {
			if (!("model_history_revision_contracts" in beforeCounts)) expectedCounts.model_history_revision_contracts = 1;
			const contract = db.query("SELECT installed_version FROM model_history_revision_contracts WHERE id='model-history'").get() as { installed_version: string } | null;
			if (contract?.installed_version !== "model-history-v1") throw new Error("Model history revision contract missing");
		}
		if (legacyGitIdentityCount !== null) {
			expectedCounts.user_git_identities = legacyGitIdentityCount;
			const missingIdentity = db
				.query(`SELECT u.id FROM users u
				WHERE ${LEGACY_GIT_IDENTITY_FILTER} AND NOT EXISTS (
					SELECT 1 FROM user_git_identities i WHERE i.user_id = u.id
					AND i.name = trim(u.git_username) AND i.email = trim(u.git_email) AND i.is_default = 1
				) LIMIT 1`)
				.get();
			if (missingIdentity) throw new Error("Legacy Git identity was not preserved");
		}
		const actualCounts = rowCounts(db);
		if (canonical(actualCounts) !== canonical(expectedCounts)) {
			const changed = Object.keys({ ...expectedCounts, ...actualCounts })
				.filter((table) => expectedCounts[table] !== actualCounts[table])
				.map((table) => ({ table, expected: expectedCounts[table], actual: actualCounts[table] }));
			throw new Error(`Existing table row counts changed: ${JSON.stringify(changed).slice(0, 4096)}`);
		}
		const afterAffected = affectedForUpgrade(true);
		if (canonical(afterAffected) !== canonical(beforeAffected)) {
			const changed = Object.keys(beforeAffected).filter(
				(table) => canonical(beforeAffected[table]) !== canonical(afterAffected[table]),
			);
			throw new Error(`Existing affected-table fields changed: ${changed.join(", ")}`);
		}
		if (target.tag === "0015_nifty_magma") {
			const populatedTurnEnds = db
				.query("SELECT count(*) AS count FROM narrators WHERE turn_ended_at IS NOT NULL")
				.get() as { count: number };
			if (Number(populatedTurnEnds.count) !== 0) {
				throw new Error("Existing narrators gained turn_ended_at values");
			}
		}
		if (target.tag === "0013_omniscient_dormammu") {
			const originalLeaseColumns = columns(db, "workspace_write_leases").filter(
				(column) => column !== "execution_class" && column !== "termination_evidence_json",
			);
			if (originalLeaseColumns.length !== 15) {
				throw new Error("workspace_write_leases does not have 15 original columns");
			}
			const populatedLeaseIds = db
				.query(
					"SELECT count(*) AS count FROM file_change_scope_recoveries WHERE workspace_lease_id IS NOT NULL",
				)
				.get() as { count: number };
			if (Number(populatedLeaseIds.count) !== 0) {
				throw new Error("Existing recovery rows gained workspace_lease_id values");
			}
		}
		// Historical gaps are repaired on the first claim, not during startup migration.
		const badNextSeq = db
			.query("SELECT count(*) AS count FROM narrators WHERE next_seq < 0")
			.get() as {
			count: number;
		};
		if (Number(badNextSeq.count) !== 0) throw new Error("next_seq cannot be negative");
		if (hadNextSeq) {
			const lowered = db
				.query(
					"SELECT count(*) AS count FROM narrators n JOIN validation_next_seq_before b USING(id) WHERE n.next_seq < b.next_seq",
				)
				.get() as { count: number };
			if (Number(lowered.count) !== 0) throw new Error("next_seq was lowered");
		}

		const fresh = new Database(":memory:", { strict: true });
		fresh.exec("PRAGMA foreign_keys=ON");
		try {
			await runUpgrade(fresh);
			if (canonical(schemaShape(db, target.tag)) !== canonical(schemaShape(fresh, target.tag))) {
				throw new Error(`${target.tag} schema does not match a fresh migration schema`);
			}
		} finally {
			fresh.close();
		}

		const firstState = canonical({
			ledger: ledger(db),
			counts: rowCounts(db),
			affected: affectedForUpgrade(false),
			schema: schemaShape(db, target.tag),
		});
		await runUpgrade(db);
		assertIntegrity(db);
		const secondState = canonical({
			ledger: ledger(db),
			counts: rowCounts(db),
			affected: affectedForUpgrade(false),
			schema: schemaShape(db, target.tag),
		});
		if (firstState !== secondState) throw new Error("Upgrade is not idempotent");

		const report = {
			status: "ok",
			validator: 1,
			backupSha256: args.backupSha256,
			sourceSha256: args.sourceSha256,
			commit: args.commit,
			version: args.version,
			targetMigrations: pending.map((migration) => ({ tag: migration.tag, hash: migration.hash })),
			checks: {
				migrationApplied: true,
				quickCheck: true,
				foreignKeyCheck: true,
				schemaMatchesFresh: true,
				rowCountsPreserved: true,
				affectedFieldsPreserved: true,
				nextSeqValid: true,
				idempotent: true,
			},
		};
		const output = JSON.stringify(report);
		if (Buffer.byteLength(output) > MAX_REPORT_BYTES)
			throw new Error("Validation report too large");
		process.stdout.write(`${output}\n`);
	} finally {
		db.close();
	}
}

await main();
