import type { ResourceMigrationSnapshot } from "../../finalize-sqlite-resource-migration";

const tableColumns = {
	narrators: ["id", "title", "context_summary"],
	narrator_messages: [
		"id",
		"narrator_id",
		"sdk_message_uuid",
		"parent_tool_use_id",
		"role",
		"content_json",
		"content_text",
		"created_by",
		"origin",
		"origin_label",
	],
	narrator_tool_calls: [
		"id",
		"message_id",
		"narrator_id",
		"tool_use_id",
		"tool_name",
		"input_json",
		"output_json",
		"status",
		"execution_attempt",
		"created_at",
	],
	narrator_message_refs: [
		"id",
		"narrator_id",
		"message_id",
		"seq",
		"is_compact",
		"segment_compact_id",
	],
	users: ["id", "username"],
};
export const revisionMigrationSql = `ALTER TABLE narrators ADD model_history_revision integer NOT NULL DEFAULT 0;
ALTER TABLE narrator_messages ADD body_revision integer NOT NULL DEFAULT 0;
ALTER TABLE narrator_tool_calls ADD model_revision integer NOT NULL DEFAULT 0;
CREATE TABLE model_history_revision_contracts(id text PRIMARY KEY, installed_version text NOT NULL);`;

export function modelHistorySnapshots() {
	const before: ResourceMigrationSnapshot = { tables: {} };
	for (const [name, columns] of Object.entries(tableColumns)) {
		before.tables[name] = {
			columns: Object.fromEntries(
				columns.map((column) => [
					column,
					{
						name: column,
						type: "text",
						...(column === "id" ? { primaryKey: true } : {}),
					},
				]),
			),
			foreignKeys: {},
		};
	}
	const after = structuredClone(before);
	for (const [table, column] of [
		["narrators", "model_history_revision"],
		["narrator_messages", "body_revision"],
		["narrator_tool_calls", "model_revision"],
	]) {
		after.tables[table].columns[column] = {
			name: column,
			type: "integer",
			notNull: true,
			default: 0,
		};
	}
	after.tables.model_history_revision_contracts = {
		columns: {
			id: { name: "id", type: "text", primaryKey: true },
			installed_version: { name: "installed_version", type: "text", notNull: true },
		},
		foreignKeys: {},
	};
	const previousDDL = Object.entries(before.tables)
		.map(
			([name, table]) =>
				`CREATE TABLE ${name}(${Object.keys(table.columns)
					.map((column) => `${column} text${column === "id" ? " PRIMARY KEY" : ""}`)
					.join(", ")});`,
		)
		.join("\n");
	return { before, after, previousDDL };
}

/** The v1 `after` snapshot plus what the v2 (block storage) contract reads. */
export function modelHistoryBlockSnapshot(): ResourceMigrationSnapshot {
	const snapshot = structuredClone(modelHistorySnapshots().after);
	const column = (name: string, type: "text" | "integer", extra: Record<string, unknown> = {}) => ({
		name,
		type,
		...extra,
	});
	snapshot.tables.narrator_messages.columns.body_format = column("body_format", "integer", {
		notNull: true,
		default: 1,
	});
	snapshot.tables.narrator_messages.columns.author_narrator_id = column(
		"author_narrator_id",
		"text",
	);
	snapshot.tables.narrator_tool_calls.columns.block_id = column("block_id", "text");
	snapshot.tables.narrator_tool_calls.columns.is_internal = column("is_internal", "integer", {
		notNull: true,
		default: 0,
	});
	snapshot.tables.narrator_block_sync_queue = {
		columns: { message_id: column("message_id", "text", { primaryKey: true }) },
		foreignKeys: {},
	};
	snapshot.tables.narrator_blocks = {
		columns: Object.fromEntries(
			[
				["id", "text", { primaryKey: true }],
				["message_id", "text", { notNull: true }],
				["ordinal", "integer", { notNull: true }],
				["kind", "text", { notNull: true }],
				["subtype", "text", {}],
				["phase", "text", { notNull: true }],
				["model_text", "text", {}],
				["payload", "text", { notNull: true }],
				["state", "text", {}],
				["provider_meta", "text", {}],
			].map(([name, type, extra]) => [
				name as string,
				column(name as string, type as "text" | "integer", extra as Record<string, unknown>),
			]),
		),
		foreignKeys: {},
	};
	return snapshot;
}

/** The v3 contract's schema: the block schema plus the state-write counter. */
export function modelHistoryStateSnapshot(): ResourceMigrationSnapshot {
	const snapshot = modelHistoryBlockSnapshot();
	snapshot.tables.narrator_messages.columns.state_revision = {
		name: "state_revision",
		type: "integer",
		notNull: true,
		default: 0,
	};
	return snapshot;
}
