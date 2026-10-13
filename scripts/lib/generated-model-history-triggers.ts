import {
	MODEL_HISTORY_REVISION_CONTRACT,
	renderModelHistoryRevisionSql,
} from "../../server/services/agent-runtime/model-history-revision-sql";

export type MigrationDialect = "sqlite" | "postgres";
type Snapshot = { tables?: unknown };
const revisionColumns = {
	narrators: ["model_history_revision"],
	narrator_messages: ["body_revision"],
	narrator_tool_calls: ["model_revision"],
	model_history_revision_contracts: ["id", "installed_version"],
} as const;
export const MODEL_HISTORY_TRIGGER_TABLES = [
	"narrators",
	"narrator_messages",
	"narrator_tool_calls",
	"narrator_message_refs",
	"users",
] as const;

function columnsOf(snapshot: Snapshot, name: string, dialect: MigrationDialect) {
	if (!snapshot.tables || typeof snapshot.tables !== "object") return undefined;
	const tables = snapshot.tables as Record<string, { columns?: Record<string, unknown> }>;
	return tables[dialect === "postgres" ? `public.${name}` : name]?.columns;
}

export function hasModelHistoryRevisionSchema(
	snapshot: Snapshot,
	dialect: MigrationDialect,
): boolean {
	return Object.entries(revisionColumns).every(([name, columns]) => {
		const tableColumns = columnsOf(snapshot, name, dialect);
		return tableColumns && columns.every((column) => Object.hasOwn(tableColumns, column));
	});
}

/** Only a new schema transition installs this version's single-CREATE contract. Never
 * infer installation from a process flag or mutate an already-published migration. */
export function generatedModelHistoryTriggerSuffix(
	before: Snapshot,
	after: Snapshot,
	dialect: MigrationDialect,
): string {
	if (
		hasModelHistoryRevisionSchema(before, dialect) ||
		!hasModelHistoryRevisionSchema(after, dialect)
	)
		return "";
	return modelHistoryTriggerSuffix(dialect);
}

export function modelHistoryTriggerSuffix(dialect: MigrationDialect): string {
	return `\n--> statement-breakpoint\n${renderModelHistoryRevisionSql(dialect).trimEnd()}\n`;
}

/** Byte-exact allowlist, not a CREATE TRIGGER grammar relaxation. The ordinary SQL
 * parser must still reject unknown DDL, extra statements, and altered trigger bodies. */
export function splitGeneratedModelHistoryTriggers(sql: string, suffix: string) {
	if (!suffix) return { schemaSql: sql, triggerSql: "" };
	if (!sql.endsWith(suffix))
		throw new Error("Missing or noncanonical generated model-history trigger suffix");
	return { schemaSql: sql.slice(0, -suffix.length), triggerSql: suffix };
}

export function appendGeneratedModelHistoryTriggers(sql: string, suffix: string): string {
	if (!suffix || sql.endsWith(suffix)) return sql;
	return `${sql.trimEnd()}${suffix}`;
}

export { MODEL_HISTORY_REVISION_CONTRACT, renderModelHistoryRevisionSql };
