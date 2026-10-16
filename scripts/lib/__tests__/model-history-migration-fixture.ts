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
