import type { Database } from "bun:sqlite";
import type { SQL } from "bun";
import {
	MODEL_HISTORY_PG_TRIGGERS,
	MODEL_HISTORY_REVISION_CONTRACT,
	MODEL_HISTORY_SQLITE_TRIGGERS,
} from "./model-history-revision-sql";

const tableOf = (name: string) => {
	const kind = name.replace(/^model_history_v\d+_/, "");
	return kind.startsWith("message") || kind.startsWith("sync_message")
		? "narrator_messages"
		: kind.startsWith("tool") || kind.startsWith("sync_tool")
			? "narrator_tool_calls"
			: kind.startsWith("ref")
				? "narrator_message_refs"
				: kind.startsWith("block")
					? "narrator_blocks"
					: kind === "sender"
						? "users"
						: "narrators";
};
const sqliteTriggerPredicate = MODEL_HISTORY_SQLITE_TRIGGERS.map(
	(name) => `(name='${name}' AND tbl_name='${tableOf(name)}')`,
).join(" OR ");
const pgTriggerPredicate = MODEL_HISTORY_PG_TRIGGERS.map(
	([name, table]) => `(tgname='${name}' AND tgrelid='${table}'::regclass)`,
).join(" OR ");

export interface ModelHistorySignature {
	revision: string;
	trusted: boolean;
	/** Catalog epoch invalidates row-level deltas after trigger disable/reinstall/re-enable. */
	catalogIdentity?: string;
}

/** A tiny, persisted signature on EVERY hit; never use a process-local epoch or WS version. */
export function readSqliteModelHistorySignature(
	sqlite: Database,
	narratorId: string,
): ModelHistorySignature {
	try {
		const row = sqlite
			.query<
				{ revision: number; contract: string | null; triggers: number; catalogIdentity: number },
				[string]
			>(`
			SELECT n.model_history_revision AS revision,
			 (SELECT schema_version FROM pragma_schema_version) AS "catalogIdentity",
			 (SELECT installed_version FROM model_history_revision_contracts WHERE id = 'model-history') AS contract,
			 (SELECT count(*) FROM sqlite_schema WHERE type = 'trigger' AND (${sqliteTriggerPredicate})) AS triggers
			 FROM narrators n WHERE n.id = ? LIMIT 1`)
			.get(narratorId);
		return {
			revision: String(row?.revision ?? -1),
			catalogIdentity: String(row?.catalogIdentity ?? -1),
			trusted:
				row?.contract === MODEL_HISTORY_REVISION_CONTRACT &&
				row.triggers === MODEL_HISTORY_SQLITE_TRIGGERS.length,
		};
	} catch (error) {
		// An old schema is still readable, but cannot ever admit cached source rows.
		if (!/no such (column|table)/.test(String(error))) throw error;
		return { revision: "unversioned", trusted: false };
	}
}

export async function readPostgresModelHistorySignature(
	sql: Pick<SQL, "unsafe">,
	narratorId: string,
	options: { signal?: AbortSignal; deadline?: number } = {},
): Promise<ModelHistorySignature> {
	const deadline = Math.min(options.deadline ?? Number.POSITIVE_INFINITY, Date.now() + 10_000);
	options.signal?.throwIfAborted();
	try {
		const pending = sql.unsafe(
			`SELECT n.model_history_revision AS revision,
			(SELECT string_agg(oid::text || ':' || xmin::text || ':' || tgenabled::text, ',' ORDER BY tgname)
			 FROM pg_trigger WHERE NOT tgisinternal AND (${pgTriggerPredicate})) AS "catalogIdentity",
			(SELECT installed_version FROM model_history_revision_contracts WHERE id = 'model-history') AS contract,
			(SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgenabled IN ('O', 'A') AND (${pgTriggerPredicate})) AS triggers
			FROM narrators n WHERE n.id = $1 LIMIT 1`,
			[narratorId],
		);
		let abort: () => void = () => {};
		let timer: ReturnType<typeof setTimeout> | undefined;
		const stopped = new Promise<never>((_, reject) => {
			abort = () => {
				pending.cancel();
				reject(new Error("Model history preparation cancelled"));
			};
			timer = setTimeout(
				() => {
					pending.cancel();
					reject(new Error("Model history preparation exceeded 10s budget"));
				},
				Math.max(1, deadline - Date.now()),
			);
			options.signal?.addEventListener("abort", abort, { once: true });
		});
		let rows: Awaited<typeof pending>;
		try {
			rows = await Promise.race([pending, stopped]);
		} finally {
			clearTimeout(timer);
			options.signal?.removeEventListener("abort", abort);
		}
		const row = rows[0];
		return {
			revision: String(row?.revision ?? -1),
			catalogIdentity: String(row?.catalogIdentity ?? "missing"),
			trusted:
				row?.contract === MODEL_HISTORY_REVISION_CONTRACT &&
				Number(row.triggers) === MODEL_HISTORY_PG_TRIGGERS.length,
		};
	} catch (error) {
		if (error instanceof Error && error.message.startsWith("Model history preparation"))
			throw error;
		if (!["42703", "42P01"].includes(String((error as { code?: string }).code)))
			throw new Error("Model history PostgreSQL signature failed");
		return { revision: "unversioned", trusted: false };
	}
}
