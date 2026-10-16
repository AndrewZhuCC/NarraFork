import type { Database } from "bun:sqlite";
import { modelJsonContainerBytes } from "./model-history-json";

export const MODEL_HISTORY_REF_BATCH = 200;
export const MODEL_HISTORY_BATCH_BYTES = 4 * 1024 * 1024;
export const MODEL_HISTORY_MAX_BYTES = 128 * 1024 * 1024;
export const MODEL_HISTORY_TIMEOUT_MS = 10_000;

export interface ModelHistoryQueryOptions {
	narratorId: string;
	limit?: number;
	deadline: number;
	maxBytes?: number;
	/** In-memory unit fixtures have no worker identity; reject large inline JSON before decoding. */
	inlineRowBytes?: number;
	versioned?: boolean;
}
export interface ModelHistoryCounters {
	refQueries: number;
	bodyQueries: number;
	toolQueries: number;
	refs: number;
	toolRows: number;
	sourceBytes: number;
	maxRefsPerQuery: number;
	maxBodyBatchBytes: number;
	maxToolBatchBytes: number;
	preparedBytes?: number;
	reusedBodies?: number;
	reusedTools?: number;
}
export type ModelSourceRow = {
	id: string;
	seq: number;
	bodyRevision: number;
	jsonContainerBytes?: number;
	narratorId: string;
	role: "user" | "assistant" | "system" | "sys" | "disp";
	contentJson: unknown;
	contentText: string | null;
	parentToolUseId: string | null;
	messageUuid: string | null;
	createdBy: string | null;
	origin: string | null;
	originLabel: string | null;
	creator: { username: string } | null;
	narrator: { title: string | null } | null;
	toolCalls: ModelToolSourceRow[];
};
export type ModelToolSourceRow = {
	id: string;
	modelRevision: number;
	jsonContainerBytes?: number;
	executionAttempt: number;
	createdAt: string;
	toolUseId: string;
	toolName: string;
	inputJson: unknown;
	outputJson: unknown;
	status: string;
};
export type HistoryQuery = <T>(sql: string, parameters?: unknown[]) => Promise<T[]>;
export interface ModelHistorySource {
	messages: ModelSourceRow[];
	counters: ModelHistoryCounters;
	compactSeq?: number | null;
}

/** SQLite work runs in a read-only worker; this adapter is also useful for tiny in-memory tests. */
export function sqliteHistoryQuery(sqlite: Database): HistoryQuery {
	return async <T>(sql: string, parameters: unknown[] = []) =>
		sqlite.query(sql).all(...(parameters as Array<string | number | null>)) as T[];
}

function decode(value: unknown): unknown {
	return typeof value === "string" ? JSON.parse(value) : value;
}

/** No ORM relation joins: both message bodies and tools have their own metadata/byte preflight. */
export async function queryModelHistory(
	query: HistoryQuery,
	options: ModelHistoryQueryOptions,
	onPage?: (rows: ModelSourceRow[]) => void | Promise<void>,
	onTools?: (rows: Array<ModelToolSourceRow & { messageId: string }>) => void | Promise<void>,
): Promise<ModelHistorySource> {
	const counters: ModelHistoryCounters = {
		refQueries: 0,
		bodyQueries: 0,
		toolQueries: 0,
		refs: 0,
		toolRows: 0,
		sourceBytes: 0,
		maxRefsPerQuery: 0,
		maxBodyBatchBytes: 0,
		maxToolBatchBytes: 0,
	};
	const maxBytes = Math.min(options.maxBytes ?? MODEL_HISTORY_MAX_BYTES, MODEL_HISTORY_MAX_BYTES);
	// Reserve decoded UTF-16 strings, detached provider projections and object overhead, not
	// only wire bytes. Exhaustion is an error, never a silently shortened model conversation.
	let preparedBytes = 0;
	const check = (bytes = 0, rows = 0, objectBytes = 0) => {
		if (Date.now() >= options.deadline)
			throw new Error("Model history preparation exceeded 10s budget");
		preparedBytes += bytes * 4 + rows * 1024 + objectBytes;
		counters.preparedBytes = preparedBytes;
		counters.sourceBytes += bytes;
		if (preparedBytes > maxBytes)
			throw new Error("Model history preparation exceeded 128MiB budget");
	};
	const boundary = await query<{ seq: number }>(
		"SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND is_compact = 1 ORDER BY seq DESC LIMIT 1",
		[options.narratorId],
	);
	// Include the boundary row: Codex remote compaction stores its payload on that marker.
	let cursor = boundary[0] ? boundary[0].seq - 1 : Number.MIN_SAFE_INTEGER;
	let remaining =
		options.limit == null
			? Number.POSITIVE_INFINITY
			: Math.min(Math.max(Math.trunc(options.limit), 1), 2000);
	const messages: ModelSourceRow[] = [];
	while (remaining > 0) {
		check();
		const refs = await query<{ messageId: string; seq: number; bytes: number }>(
			`SELECT r.message_id AS "messageId", r.seq,
			 length(CAST(m.content_json AS BLOB)) + coalesce(length(CAST(m.content_text AS BLOB)), 0) AS bytes
			 FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE r.narrator_id = ? AND r.seq > ? AND r.segment_compact_id IS NULL AND m.role <> 'disp'
			 ORDER BY r.seq LIMIT ?`,
			[options.narratorId, cursor, Math.min(MODEL_HISTORY_REF_BATCH, remaining)],
		);
		counters.refQueries++;
		counters.maxRefsPerQuery = Math.max(counters.maxRefsPerQuery, refs.length);
		if (!refs.length) break;
		let offset = 0;
		while (offset < refs.length) {
			let end = offset;
			let batchBytes = 0;
			while (
				end < refs.length &&
				(end === offset || batchBytes + Number(refs[end].bytes) <= MODEL_HISTORY_BATCH_BYTES)
			) {
				batchBytes += Number(refs[end++].bytes);
			}
			if (
				options.inlineRowBytes != null &&
				refs
					.slice(offset, end)
					.some((row) => Number(row.bytes) > (options.inlineRowBytes ?? Number.POSITIVE_INFINITY))
			)
				throw new Error("Model history large JSON requires a read-only worker");
			check(batchBytes, end - offset);
			counters.maxBodyBatchBytes = Math.max(counters.maxBodyBatchBytes, batchBytes);
			const batchRefs = refs.slice(offset, end);
			const ids = batchRefs.map((ref) => ref.messageId);
			const placeholders = ids.map(() => "?").join(",");
			const bodies = await query<
				Omit<ModelSourceRow, "creator" | "narrator" | "toolCalls"> & {
					username: string | null;
					title: string | null;
				}
			>(
				`SELECT m.id, ${options.versioned ? "m.body_revision" : "0"} AS "bodyRevision", m.narrator_id AS "narratorId", m.role, m.content_json AS "contentJson",
				 m.content_text AS "contentText", m.parent_tool_use_id AS "parentToolUseId",
				 m.sdk_message_uuid AS "messageUuid", m.created_by AS "createdBy", m.origin,
				 m.origin_label AS "originLabel", u.username, n.title
				 FROM narrator_messages m LEFT JOIN users u ON u.id = m.created_by
				 LEFT JOIN narrators n ON n.id = m.narrator_id WHERE m.id IN (${placeholders})`,
				ids,
			);
			counters.bodyQueries++;
			if (bodies.length !== ids.length)
				throw new Error("Model history snapshot contains missing messages");
			const byId = new Map<string, ModelSourceRow>();
			for (const { username, title, ...body } of bodies) {
				const jsonContainerBytes = modelJsonContainerBytes(
					body.contentJson,
					options.deadline,
					maxBytes - preparedBytes,
				);
				check(0, 0, jsonContainerBytes);
				byId.set(body.id, {
					jsonContainerBytes,
					...body,
					seq: batchRefs.find((ref) => ref.messageId === body.id)?.seq ?? 0,
					contentJson: decode(body.contentJson),
					creator: username == null ? null : { username },
					narrator: { title },
					toolCalls: [],
				});
			}
			const page = batchRefs.map((ref) => byId.get(ref.messageId) as ModelSourceRow);
			// Worker transport sends body and tool pages independently, so one tool-heavy
			// message cannot turn an otherwise 4MiB page into a giant structured clone.
			if (onTools && onPage) await onPage(page);
			let toolMessageId = "";
			let toolId = "";
			for (;;) {
				check();
				const toolMeta = await query<{ id: string; messageId: string; bytes: number }>(
					`SELECT id, message_id AS "messageId",
					 coalesce(length(CAST(input_json AS BLOB)), 0) + coalesce(length(CAST(output_json AS BLOB)), 0) AS bytes
					 FROM narrator_tool_calls WHERE message_id IN (${placeholders})
					 AND (message_id > ? OR (message_id = ? AND id > ?))
					 ORDER BY message_id, id LIMIT ${MODEL_HISTORY_REF_BATCH}`,
					[...ids, toolMessageId, toolMessageId, toolId],
				);
				counters.toolQueries++;
				if (!toolMeta.length) break;
				let selected = 0;
				let toolBytes = 0;
				while (
					selected < toolMeta.length &&
					(selected === 0 ||
						toolBytes + Number(toolMeta[selected].bytes) <= MODEL_HISTORY_BATCH_BYTES)
				) {
					toolBytes += Number(toolMeta[selected++].bytes);
				}
				if (
					options.inlineRowBytes != null &&
					toolMeta
						.slice(0, selected)
						.some((row) => Number(row.bytes) > (options.inlineRowBytes ?? Number.POSITIVE_INFINITY))
				)
					throw new Error("Model history large JSON requires a read-only worker");
				check(toolBytes, selected);
				counters.maxToolBatchBytes = Math.max(counters.maxToolBatchBytes, toolBytes);
				const selectedIds = toolMeta.slice(0, selected).map((row) => row.id);
				const tools = await query<ModelToolSourceRow & { messageId: string }>(
					`SELECT id, ${options.versioned ? "model_revision" : "0"} AS "modelRevision", message_id AS "messageId", execution_attempt AS "executionAttempt", created_at AS "createdAt",
					 tool_use_id AS "toolUseId", tool_name AS "toolName", input_json AS "inputJson", output_json AS "outputJson", status
					 FROM narrator_tool_calls WHERE id IN (${selectedIds.map(() => "?").join(",")})`,
					selectedIds,
				);
				counters.toolQueries++;
				if (tools.length !== selectedIds.length)
					throw new Error("Model history snapshot contains missing tool rows");
				const decodedTools = tools.map((tool) => {
					const inputBytes = modelJsonContainerBytes(
						tool.inputJson,
						options.deadline,
						maxBytes - preparedBytes,
					);
					const jsonContainerBytes =
						inputBytes +
						modelJsonContainerBytes(
							tool.outputJson,
							options.deadline,
							maxBytes - preparedBytes - inputBytes,
						);
					check(0, 0, jsonContainerBytes);
					return {
						...tool,
						jsonContainerBytes,
						inputJson: decode(tool.inputJson),
						outputJson: decode(tool.outputJson),
					};
				});
				if (onTools) await onTools(decodedTools);
				else
					for (const { messageId, ...tool } of decodedTools)
						byId.get(messageId)?.toolCalls.push(tool);
				counters.toolRows += tools.length;
				const tail = toolMeta[selected - 1];
				toolMessageId = tail.messageId;
				toolId = tail.id;
			}
			counters.refs += page.length;
			if (onPage && !onTools) await onPage(page);
			else if (!onPage) messages.push(...page);
			offset = end;
		}
		cursor = refs[refs.length - 1].seq;
		remaining -= refs.length;
	}
	check();
	return { messages, counters, compactSeq: boundary[0]?.seq ?? null };
}

/** Shared SQL has no dialect-specific JSON parsing; PG byte preflight stays server-side. */
export function postgresHistorySql(sql: string): string {
	let parameter = 0;
	return sql
		.replace(/\?/g, () => `$${++parameter}`)
		.replace(/length\(CAST\(([^)]+) AS BLOB\)\)/g, "octet_length(CAST($1 AS TEXT))");
}
