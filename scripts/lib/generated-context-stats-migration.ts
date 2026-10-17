import type { ResourceMigrationSnapshot } from "../finalize-sqlite-resource-migration";

export const CONTEXT_STATS_MIGRATION_CONTRACT = "context-numeric-stats-v1";
// Preserve the data migration and sync contract from upstream 0206_context_numeric_stats.
export const CONTEXT_STATS_MIGRATION_SQL = `INSERT INTO \`narrator_message_context_stats\` (\`message_id\`, \`role\`, \`parent_tool_use_id\`, \`segments_json\`)
SELECT \`id\`, \`role\`, \`parent_tool_use_id\`, COALESCE(\`context_chars_json\`, '{"segments":[]}')
FROM \`narrator_messages\`
WHERE true
ON CONFLICT(\`message_id\`) DO NOTHING;
--> statement-breakpoint
CREATE TRIGGER \`narrator_message_context_stats_insert\`
AFTER INSERT ON \`narrator_messages\`
BEGIN
	INSERT INTO \`narrator_message_context_stats\` (\`message_id\`, \`role\`, \`parent_tool_use_id\`, \`segments_json\`)
	VALUES (
		\`NEW\`.\`id\`,
		\`NEW\`.\`role\`,
		\`NEW\`.\`parent_tool_use_id\`,
		COALESCE(\`NEW\`.\`context_chars_json\`, '{"segments":[]}')
	)
	ON CONFLICT(\`message_id\`) DO UPDATE SET
		\`role\` = \`excluded\`.\`role\`,
		\`parent_tool_use_id\` = \`excluded\`.\`parent_tool_use_id\`,
		\`segments_json\` = \`excluded\`.\`segments_json\`;
END;
--> statement-breakpoint
CREATE TRIGGER \`narrator_message_context_stats_update\`
AFTER UPDATE OF \`role\`, \`parent_tool_use_id\`, \`context_chars_json\` ON \`narrator_messages\`
BEGIN
	INSERT INTO \`narrator_message_context_stats\` (\`message_id\`, \`role\`, \`parent_tool_use_id\`, \`segments_json\`)
	VALUES (
		\`NEW\`.\`id\`,
		\`NEW\`.\`role\`,
		\`NEW\`.\`parent_tool_use_id\`,
		COALESCE(\`NEW\`.\`context_chars_json\`, '{"segments":[]}')
	)
	ON CONFLICT(\`message_id\`) DO UPDATE SET
		\`role\` = \`excluded\`.\`role\`,
		\`parent_tool_use_id\` = \`excluded\`.\`parent_tool_use_id\`,
		\`segments_json\` = \`excluded\`.\`segments_json\`;
END;`;

/** Only the receipt-bound introduction of the narrow table installs this contract. */
export function generatedContextStatsMigrationSuffix(
	before: ResourceMigrationSnapshot,
	after: ResourceMigrationSnapshot,
): string {
	if (before.tables.narrator_message_context_stats || !after.tables.narrator_message_context_stats)
		return "";
	for (const snapshot of [before, after]) {
		if (
			["id", "role", "parent_tool_use_id", "context_chars_json"].some(
				(column) => !snapshot.tables.narrator_messages?.columns[column],
			)
		)
			throw new Error("Context stats migration requires existing message metadata columns");
	}
	return `\n--> statement-breakpoint\n${CONTEXT_STATS_MIGRATION_SQL}\n`;
}

/** Strip only the byte-exact allowlisted suffix; arbitrary DML/triggers remain rejected. */
export function splitGeneratedContextStatsMigration(sql: string, suffix: string): string {
	if (!suffix) return sql;
	if (!sql.endsWith(suffix))
		throw new Error("Missing or noncanonical generated context-stats migration suffix");
	return sql.slice(0, -suffix.length);
}
