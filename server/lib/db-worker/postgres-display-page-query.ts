import { SQL } from "bun";
import {
	DisplayJsonAllocationBudget,
	storedDisplayAllocationBudget,
} from "./display-page-json-budget";
import { projectDisplayToolCall, projectNarratorDisplayMessage } from "./display-page-projection";
import {
	DISPLAY_PAGE_LIMITS,
	type DisplayPageMessagesParams,
	type DisplayPageMessagesResult,
	DisplayPageReadError,
	type NarratorDisplayMessage,
	type NarratorDisplayToolCall,
	validateDisplayPageRequest,
} from "./display-page-protocol";
import {
	DISPLAY_MESSAGE_COLUMNS,
	DISPLAY_TOOL_COLUMNS,
	type DisplayColumn,
	decodeDisplayRow,
} from "./display-page-query";

const CREATOR_COLUMNS = ["id", "username", "avatar_color", "avatar_image_id"];

function selection(alias: string, descriptors: readonly DisplayColumn[]): string {
	// Explicit ::text defeats driver auto-JSON decoding BEFORE our admission preflight.
	return descriptors
		.map(({ column, field, json }) => `${alias}."${column}"${json ? "::text" : ""} AS "${field}"`)
		.join(", ");
}
function storedBytes(alias: string, columns: readonly string[]): string {
	return columns
		.map((column) => `coalesce(octet_length(${alias}."${column}"::text),0)`)
		.join(" + ");
}
function budgetError(reason: string): never {
	throw new DisplayPageReadError(
		"DISPLAY_PAGE_BUDGET_EXCEEDED",
		`Display page exceeded ${reason}; request a smaller history window`,
	);
}

async function closeReadClient(client: SQL | undefined): Promise<void> {
	try {
		await client?.close({ timeout: 0 });
	} catch {
		throw new DisplayPageReadError(
			"DISPLAY_PAGE_READER_UNAVAILABLE",
			"PostgreSQL read-only display worker could not close its connection",
		);
	}
}

/** Independent one-connection readonly worker task. No ORM, migrations, db/index or model runtime. */
export async function runPostgresDisplayPageQuery(
	url: string,
	input: DisplayPageMessagesParams,
	signal: AbortSignal,
): Promise<DisplayPageMessagesResult> {
	validateDisplayPageRequest(input);
	const deadlineAt = Math.min(input.deadlineAt, Date.now() + DISPLAY_PAGE_LIMITS.timeoutMs);
	const check = () => {
		if (signal.aborted)
			throw new DisplayPageReadError("DISPLAY_PAGE_CANCELLED", "Display page read was cancelled");
		if (Date.now() >= deadlineAt)
			throw new DisplayPageReadError(
				"DISPLAY_PAGE_DEADLINE",
				"Display page read exceeded its deadline",
			);
	};
	check();
	if (!input.messageIds.length) return { messages: [], rawBytes: 0, outputBytes: 2 };
	let client: SQL | undefined;
	let backendPid: number | undefined;
	let cancellation: Promise<void> | undefined;
	const cancelRemote = (): Promise<void> => {
		if (!backendPid) return Promise.resolve();
		if (cancellation) return cancellation;
		const pid = backendPid;
		cancellation = (async () => {
			let control: SQL | undefined;
			try {
				// Bun's local Query.cancel() alone need not stop an executing remote
				// pg_sleep/scan. A bounded independent control connection cancels only
				// THIS task's backend PID, never an HTTP-provided process selector.
				control = new SQL(url, { max: 1, connectTimeout: 1, idleTimeout: 1 });
				await control.begin(async (tx) => {
					await tx.unsafe("SET TRANSACTION READ ONLY");
					await tx.unsafe("SET LOCAL statement_timeout='500ms'");
					await tx.unsafe("SELECT pg_cancel_backend($1)", [pid]);
				});
			} catch {
				throw new DisplayPageReadError(
					"DISPLAY_PAGE_READER_UNAVAILABLE",
					"PostgreSQL read-only display cancellation failed",
				);
			} finally {
				await closeReadClient(control);
			}
		})();
		return cancellation;
	};
	try {
		if (typeof url !== "string" || url.length > 8192) throw new Error();
		const parsed = new URL(url);
		if (parsed.protocol !== "postgres:" && parsed.protocol !== "postgresql:") throw new Error();
		client = new SQL(url, { max: 1, connectTimeout: 2, idleTimeout: 1 });
		return await client.begin(async (tx) => {
			const query = async <T extends Record<string, unknown>>(
				sql: string,
				values: unknown[] = input.messageIds,
			): Promise<T[]> => {
				check();
				const pending = tx.unsafe(sql, values);
				const abort = () => {
					void cancelRemote().catch(() => {});
					try {
						pending.cancel();
					} catch {
						/* The deadline/connection close is the backstop. */
					}
				};
				signal.addEventListener("abort", abort, { once: true });
				try {
					const rows = await pending;
					check();
					return rows as T[];
				} finally {
					signal.removeEventListener("abort", abort);
				}
			};
			await query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY", []);
			await query("SELECT set_config('statement_timeout', $1, true)", [
				`${Math.max(1, deadlineAt - Date.now())}ms`,
			]);
			await query("SET LOCAL lock_timeout = '250ms'", []);
			await query("SET LOCAL application_name = 'narrafork-display-page-reader'", []);
			const pidRows = await query<{ pid: number }>("SELECT pg_backend_pid() AS pid", []);
			backendPid = pidRows[0].pid;
			const placeholders = input.messageIds.map((_, index) => `$${index + 1}`).join(", ");
			const messageWhere = `m.id IN (${placeholders})`;
			const toolsWhere = `tc.message_id IN (${placeholders})`;
			const latestWhere = `${toolsWhere} AND NOT EXISTS (
				SELECT 1 FROM narrator_tool_calls newer
				WHERE newer.message_id = tc.message_id AND newer.tool_use_id = tc.tool_use_id AND (
					coalesce(newer.execution_attempt,0) > coalesce(tc.execution_attempt,0) OR (
						coalesce(newer.execution_attempt,0) = coalesce(tc.execution_attempt,0) AND (
							newer.created_at > tc.created_at OR (newer.created_at = tc.created_at AND newer.id > tc.id)
						)
					)
				)
			)`;
			const messageMetadata = await query<{ bytes: number }>(
				`SELECT (${storedBytes(
					"m",
					DISPLAY_MESSAGE_COLUMNS.map(({ column }) => column),
				)} + ${storedBytes("u", CREATOR_COLUMNS)}) AS bytes
				FROM narrator_messages m LEFT JOIN users u ON u.id = m.created_by
				WHERE ${messageWhere} LIMIT ${DISPLAY_PAGE_LIMITS.maxInputIds + 1}`,
			);
			if (messageMetadata.length !== input.messageIds.length)
				throw new DisplayPageReadError(
					"DISPLAY_PAGE_REFERENCES_CHANGED",
					"Display message references changed; reload the history window",
				);
			let rawBytes = 0;
			const maxRawBytes = input.maxRawBytes ?? DISPLAY_PAGE_LIMITS.maxRawBytes;
			const charge = (bytes: number) => {
				if (!Number.isSafeInteger(bytes) || bytes < 0)
					budgetError("invalid storage byte accounting");
				rawBytes += bytes;
				if (rawBytes > maxRawBytes) budgetError("the raw UTF-8 byte budget");
			};
			for (const row of messageMetadata) charge(row.bytes);
			const toolCandidates = await query(
				`SELECT tc.id FROM narrator_tool_calls tc WHERE ${toolsWhere} LIMIT ${DISPLAY_PAGE_LIMITS.maxRows + 1}`,
			);
			if (toolCandidates.length + messageMetadata.length > DISPLAY_PAGE_LIMITS.maxRows)
				budgetError("the tool row budget");
			const toolMetadata = await query<{ bytes: number }>(
				`SELECT (${storedBytes(
					"tc",
					DISPLAY_TOOL_COLUMNS.map(({ column }) => column),
				)}) AS bytes FROM narrator_tool_calls tc WHERE ${latestWhere} LIMIT ${DISPLAY_PAGE_LIMITS.maxRows + 1}`,
			);
			for (const row of toolMetadata) charge(row.bytes);
			check();
			const allocation = storedDisplayAllocationBudget(
				rawBytes,
				messageMetadata.length,
				toolMetadata.length,
				DISPLAY_MESSAGE_COLUMNS.length,
				DISPLAY_TOOL_COLUMNS.length,
				deadlineAt,
				DISPLAY_PAGE_LIMITS.maxPreparedBytes,
			);
			// Only AFTER all raw bytes have been admitted on the SAME snapshot do the
			// server/driver transfer JSON::text (max16MiB total) into this worker.
			const rows = await query<
				Record<string, unknown>
			>(`SELECT ${selection("m", DISPLAY_MESSAGE_COLUMNS)},
				u.id AS "_creatorId", u.username AS "_creatorUsername", u.avatar_color AS "_creatorAvatarColor", u.avatar_image_id AS "_creatorAvatarImageId"
				FROM narrator_messages m LEFT JOIN users u ON u.id = m.created_by WHERE ${messageWhere} LIMIT ${DISPLAY_PAGE_LIMITS.maxInputIds}`);
			const byId = new Map<string, NarratorDisplayMessage>();
			for (const row of rows) {
				check();
				const decoded = decodeDisplayRow(row, DISPLAY_MESSAGE_COLUMNS, allocation);
				const message: NarratorDisplayMessage = {
					...decoded,
					id: decoded.id as string,
					narratorId: decoded.narratorId as string,
					parentToolUseId: decoded.parentToolUseId as string | null,
					role: decoded.role as string,
					contentJson: decoded.contentJson,
					createdAt: decoded.createdAt as string,
					toolCalls: [],
					creator:
						row._creatorId === null
							? null
							: {
									id: row._creatorId as string,
									username: row._creatorUsername as string,
									avatarColor: row._creatorAvatarColor as string | null,
									avatarImageId: row._creatorAvatarImageId as string | null,
								},
				};
				byId.set(message.id, projectNarratorDisplayMessage(message, input.ioBudget));
			}
			const tools = await query<Record<string, unknown>>(
				`SELECT ${selection("tc", DISPLAY_TOOL_COLUMNS)} FROM narrator_tool_calls tc WHERE ${latestWhere} ORDER BY tc.created_at, tc.id LIMIT ${DISPLAY_PAGE_LIMITS.maxRows}`,
			);
			for (const row of tools) {
				check();
				const tc = decodeDisplayRow(
					row,
					DISPLAY_TOOL_COLUMNS,
					allocation,
				) as NarratorDisplayToolCall;
				byId.get(tc.messageId)?.toolCalls.push(projectDisplayToolCall(tc, input.ioBudget));
			}
			const messages: NarratorDisplayMessage[] = [];
			let outputBytes = 2;
			const outputAllocation = new DisplayJsonAllocationBudget(
				DISPLAY_PAGE_LIMITS.maxOutputPreparedBytes,
				deadlineAt,
				256,
			);
			for (const id of input.messageIds) {
				check();
				const message = byId.get(id);
				if (!message)
					throw new DisplayPageReadError(
						"DISPLAY_PAGE_REFERENCES_CHANGED",
						"Display message disappeared during read",
					);
				const serialized = JSON.stringify(message);
				const bytes = Buffer.byteLength(serialized);
				outputBytes += bytes + (messages.length ? 1 : 0);
				if (outputBytes > DISPLAY_PAGE_LIMITS.maxOutputBytes)
					budgetError("the projected UTF-8 byte budget");
				outputAllocation.reserveJson(serialized, bytes);
				messages.push(message);
			}
			return { messages, rawBytes, outputBytes };
		});
	} catch (error) {
		check();
		if (error instanceof DisplayPageReadError) throw error;
		// Native driver errors can contain URLs, passwords, query values or SQL snippets.
		// Fail closed for missing revision/DTO columns; do not bootstrap/patch an old DB.
		throw new DisplayPageReadError(
			"DISPLAY_PAGE_READER_UNAVAILABLE",
			"PostgreSQL read-only display worker failed; verify schema and connection availability",
		);
	} finally {
		// Do not acknowledge a reusable slot before server-side cancellation AND
		// native connection close have completed, even if local Query.cancel rejected.
		try {
			await cancellation;
		} finally {
			await closeReadClient(client);
		}
	}
}
