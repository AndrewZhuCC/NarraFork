import {
	MODEL_HISTORY_REVISION_CONTRACT,
	MODEL_HISTORY_REVISION_CONTRACT_V1,
	MODEL_HISTORY_REVISION_CONTRACT_V2,
	renderModelHistoryReinstallSql,
	renderModelHistoryRevisionSql,
	renderModelHistoryRevisionSqlV1,
	renderModelHistoryRevisionSqlV2,
} from "../../server/services/agent-runtime/model-history-revision-sql";

export type MigrationDialect = "sqlite" | "postgres";
type Snapshot = { tables?: unknown };
const revisionColumns = {
	narrators: ["model_history_revision"],
	narrator_messages: ["body_revision"],
	narrator_tool_calls: ["model_revision"],
	model_history_revision_contracts: ["id", "installed_version"],
} as const;
/** What the v2 contract reads beyond v1: the block table, the envelope format, the ledger's block. */
const blockColumns = {
	narrator_messages: ["body_format", "author_narrator_id"],
	narrator_tool_calls: ["block_id", "is_internal"],
	narrator_block_sync_queue: ["message_id"],
	narrator_blocks: [
		"message_id",
		"payload",
		"model_text",
		"provider_meta",
		"kind",
		"subtype",
		"ordinal",
		"phase",
	],
} as const;
/** Tables the block contracts' (v2, v3) triggers are attached to or name in their bodies. */
export const MODEL_HISTORY_TRIGGER_TABLES = [
	"narrators",
	"narrator_messages",
	"narrator_tool_calls",
	"narrator_message_refs",
	"users",
	"narrator_blocks",
	"narrator_block_sync_queue",
] as const;
/** What v2 added; v1's triggers neither live on nor name these. */
const V2_ONLY_TRIGGER_TABLES: readonly string[] = ["narrator_blocks", "narrator_block_sync_queue"];

function columnsOf(snapshot: Snapshot, name: string, dialect: MigrationDialect) {
	if (!snapshot.tables || typeof snapshot.tables !== "object") return undefined;
	const tables = snapshot.tables as Record<string, { columns?: Record<string, unknown> }>;
	return tables[dialect === "postgres" ? `public.${name}` : name]?.columns;
}

function hasColumns(
	snapshot: Snapshot,
	dialect: MigrationDialect,
	required: Record<string, readonly string[]>,
): boolean {
	return Object.entries(required).every(([name, columns]) => {
		const tableColumns = columnsOf(snapshot, name, dialect);
		return tableColumns && columns.every((column) => Object.hasOwn(tableColumns, column));
	});
}

export function hasModelHistoryRevisionSchema(
	snapshot: Snapshot,
	dialect: MigrationDialect,
): boolean {
	return hasColumns(snapshot, dialect, revisionColumns);
}

/** Whether the schema carries what the v2 contract's triggers read. */
export function hasModelHistoryBlockSchema(snapshot: Snapshot, dialect: MigrationDialect): boolean {
	return (
		hasModelHistoryRevisionSchema(snapshot, dialect) && hasColumns(snapshot, dialect, blockColumns)
	);
}

/** Whether the schema carries what the v3 contract reads beyond v2: the state-write counter. */
export function hasModelHistoryStateSchema(snapshot: Snapshot, dialect: MigrationDialect): boolean {
	return (
		hasModelHistoryBlockSchema(snapshot, dialect) &&
		hasColumns(snapshot, dialect, { narrator_messages: ["state_revision"] })
	);
}

/** Tables a contract's triggers live on or name; a schema rebuild of one needs a reinstall. */
export function modelHistoryTriggerTables(contract: string): readonly string[] {
	return contract === MODEL_HISTORY_REVISION_CONTRACT_V1
		? MODEL_HISTORY_TRIGGER_TABLES.filter((table) => !V2_ONLY_TRIGGER_TABLES.includes(table))
		: MODEL_HISTORY_TRIGGER_TABLES;
}

/**
 * The contract a schema transition installs, if any. Only the migration that introduces a
 * contract's schema carries its triggers; v2 and v3 are versioned reinstalls (each drops the
 * contracts before it). Never inferred from a process flag, never added to an
 * already-published migration.
 */
export function generatedModelHistoryTriggerContract(
	before: Snapshot,
	after: Snapshot,
	dialect: MigrationDialect,
): string | undefined {
	if (hasModelHistoryStateSchema(after, dialect) && !hasModelHistoryStateSchema(before, dialect))
		return MODEL_HISTORY_REVISION_CONTRACT;
	if (hasModelHistoryBlockSchema(after, dialect) && !hasModelHistoryBlockSchema(before, dialect))
		return MODEL_HISTORY_REVISION_CONTRACT_V2;
	if (
		hasModelHistoryRevisionSchema(after, dialect) &&
		!hasModelHistoryRevisionSchema(before, dialect)
	)
		return MODEL_HISTORY_REVISION_CONTRACT_V1;
	return undefined;
}

export function generatedModelHistoryTriggerSuffix(
	before: Snapshot,
	after: Snapshot,
	dialect: MigrationDialect,
): string {
	const contract = generatedModelHistoryTriggerContract(before, after, dialect);
	return contract ? modelHistoryTriggerSuffix(dialect, contract) : "";
}

/** The renderer output of one contract; receipts pin its digest. */
export function renderModelHistoryContractSql(dialect: MigrationDialect, contract: string): string {
	if (contract === MODEL_HISTORY_REVISION_CONTRACT_V1)
		return renderModelHistoryRevisionSqlV1(dialect);
	if (contract === MODEL_HISTORY_REVISION_CONTRACT_V2)
		return renderModelHistoryRevisionSqlV2(dialect);
	if (contract === MODEL_HISTORY_REVISION_CONTRACT) return renderModelHistoryRevisionSql(dialect);
	throw new Error(`Unknown model-history contract ${contract}`);
}

export function modelHistoryTriggerSuffix(
	dialect: MigrationDialect,
	contract: string = MODEL_HISTORY_REVISION_CONTRACT,
): string {
	return `\n--> statement-breakpoint\n${renderModelHistoryContractSql(dialect, contract).trimEnd()}\n`;
}

/**
 * The versioned reinstall a SQLite rebuild of a current-contract trigger table needs: the
 * previous schema already carries the current (v3) contract's schema (an earlier migration
 * installed it) and this transition keeps it. An older contract is never reinstalled: its
 * rebuilds stay refused. The generator wraps the schema SQL in exactly `prefix` + `suffix`; the
 * finalizer accepts such a rebuild only inside this byte-exact wrapper and nowhere else.
 */
export function modelHistoryReinstall(
	before: Snapshot,
	after: Snapshot,
	dialect: MigrationDialect,
): { prefix: string; suffix: string } | undefined {
	if (
		dialect !== "sqlite" ||
		!hasModelHistoryStateSchema(before, dialect) ||
		!hasModelHistoryStateSchema(after, dialect)
	)
		return undefined;
	const { before: drops, after: install } = renderModelHistoryReinstallSql(dialect);
	return {
		prefix: `${drops}\n--> statement-breakpoint\n`,
		suffix: `\n--> statement-breakpoint\n${install.trimEnd()}\n`,
	};
}

/** Identity of every renderer a pending generation may append; a change refuses resume. */
export function modelHistoryRendererFingerprint(dialect: MigrationDialect): string {
	const contracts = [
		MODEL_HISTORY_REVISION_CONTRACT_V1,
		MODEL_HISTORY_REVISION_CONTRACT_V2,
		MODEL_HISTORY_REVISION_CONTRACT,
	]
		.map((contract) => `${contract}\n${renderModelHistoryContractSql(dialect, contract)}`)
		.join("\n");
	if (dialect !== "sqlite") return contracts;
	const reinstall = renderModelHistoryReinstallSql(dialect);
	return `${contracts}\nreinstall\n${reinstall.before}\n${reinstall.after}`;
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

export {
	MODEL_HISTORY_REVISION_CONTRACT,
	MODEL_HISTORY_REVISION_CONTRACT_V1,
	MODEL_HISTORY_REVISION_CONTRACT_V2,
	renderModelHistoryRevisionSql,
	renderModelHistoryRevisionSqlV1,
	renderModelHistoryRevisionSqlV2,
};
