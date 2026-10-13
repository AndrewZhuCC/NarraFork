import { modelJsonContainerBytes } from "./model-history-json";
import {
	type HistoryQuery,
	MODEL_HISTORY_BATCH_BYTES,
	MODEL_HISTORY_MAX_BYTES,
	type ModelHistoryCounters,
	type ModelHistoryQueryOptions,
	type ModelHistorySource,
	type ModelSourceRow,
	type ModelToolSourceRow,
} from "./model-history-query";

export interface ModelHistoryManifest {
	compactSeq: number | null;
	messages: Array<{
		id: string;
		seq: number;
		bodyRevision: number;
		jsonContainerBytes: number;
		username: string | null;
		title: string | null;
		tools: Array<{ id: string; modelRevision: number; jsonContainerBytes: number }>;
	}>;
}
export function modelHistoryManifest(source: ModelHistorySource): ModelHistoryManifest {
	return {
		compactSeq: source.compactSeq ?? null,
		messages: source.messages.map((row) => ({
			id: row.id,
			seq: row.seq,
			bodyRevision: row.bodyRevision,
			jsonContainerBytes: row.jsonContainerBytes ?? 0,
			username: row.creator?.username ?? null,
			title: row.narrator?.title ?? null,
			tools: row.toolCalls.map((tool) => ({
				id: tool.id,
				modelRevision: tool.modelRevision,
				jsonContainerBytes: tool.jsonContainerBytes ?? 0,
			})),
		})),
	};
}

/** Metadata still keysets every ref/tool. Only changed immutable bodies/tool rows cross the worker boundary. */
export async function queryModelHistoryDelta(
	query: HistoryQuery,
	options: ModelHistoryQueryOptions,
	previous: ModelHistoryManifest,
	send: (message: unknown) => Promise<void>,
): Promise<{ counters: ModelHistoryCounters; compactSeq: number | null }> {
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
		preparedBytes: 0,
		reusedBodies: 0,
		reusedTools: 0,
	};
	const maxBytes = Math.min(options.maxBytes ?? MODEL_HISTORY_MAX_BYTES, MODEL_HISTORY_MAX_BYTES);
	const remainingBytes = () => maxBytes - (counters.preparedBytes ?? 0);
	const check = (bytes = 0, rows = 0, objectBytes = 0) => {
		if (Date.now() >= options.deadline)
			throw new Error("Model history preparation exceeded 10s budget");
		counters.preparedBytes = (counters.preparedBytes ?? 0) + bytes * 4 + rows * 1024 + objectBytes;
		if (counters.preparedBytes > maxBytes)
			throw new Error("Model history preparation exceeded 128MiB budget");
	};
	const compact = await query<{ seq: number }>(
		"SELECT seq FROM narrator_message_refs WHERE narrator_id=? AND is_compact=1 ORDER BY seq DESC LIMIT 1",
		[options.narratorId],
	);
	const compactSeq = compact[0]?.seq ?? null;
	if (compactSeq !== previous.compactSeq) throw new Error("Model history delta structure changed");
	type Ref = {
		id: string;
		seq: number;
		bodyRevision: number;
		username: string | null;
		title: string | null;
		bytes: number;
	};
	const refs: Ref[] = [];
	let cursor = compactSeq ?? Number.MIN_SAFE_INTEGER;
	for (;;) {
		check();
		const page = await query<Ref>(
			`SELECT m.id, r.seq, m.body_revision AS "bodyRevision", u.username, n.title,
		 length(CAST(m.content_json AS BLOB))+coalesce(length(CAST(m.content_text AS BLOB)),0) AS bytes
		 FROM narrator_message_refs r JOIN narrator_messages m ON m.id=r.message_id
		 LEFT JOIN users u ON u.id=m.created_by LEFT JOIN narrators n ON n.id=m.narrator_id
		 WHERE r.narrator_id=? AND r.seq>? AND r.segment_compact_id IS NULL AND m.role<>'disp'
		 ORDER BY r.seq LIMIT 200`,
			[options.narratorId, cursor],
		);
		counters.refQueries++;
		counters.maxRefsPerQuery = Math.max(counters.maxRefsPerQuery, page.length);
		if (!page.length) break;
		for (const row of page) {
			const old = previous.messages[refs.length];
			if (old && (old.id !== row.id || old.seq !== row.seq))
				throw new Error("Model history delta structure changed");
			check(Number(row.bytes), 1);
			refs.push(row);
		}
		cursor = page[page.length - 1].seq;
	}
	if (refs.length < previous.messages.length)
		throw new Error("Model history delta structure changed");
	counters.refs = refs.length;
	const oldById = new Map(previous.messages.map((row) => [row.id, row]));
	const oldToolOverheads = new Map(
		previous.messages.flatMap((row) =>
			row.tools.map((tool) => [tool.id, tool.jsonContainerBytes] as const),
		),
	);
	const oldToolRevisions = new Map(
		previous.messages.flatMap((row) =>
			row.tools.map((tool) => [tool.id, tool.modelRevision] as const),
		),
	);
	const decode = (value: unknown) => (typeof value === "string" ? JSON.parse(value) : value);
	for (let offset = 0; offset < refs.length; offset += 200) {
		const page = refs.slice(offset, offset + 200);
		const reused = page.filter((row) => {
			const old = oldById.get(row.id);
			return (
				old &&
				old.bodyRevision === row.bodyRevision &&
				old.username === row.username &&
				old.title === row.title
			);
		});
		if (reused.length) {
			for (const row of reused) check(0, 0, oldById.get(row.id)?.jsonContainerBytes ?? 0);
			await send({ type: "reuseBodies", ids: reused.map((row) => row.id) });
			counters.reusedBodies = (counters.reusedBodies ?? 0) + reused.length;
		}
		const reusedIds = new Set(reused.map((row) => row.id));
		const changed = page.filter((row) => !reusedIds.has(row.id));
		for (let index = 0; index < changed.length; ) {
			let end = index;
			let bytes = 0;
			while (
				end < changed.length &&
				(end === index || bytes + Number(changed[end].bytes) <= MODEL_HISTORY_BATCH_BYTES)
			)
				bytes += Number(changed[end++].bytes);
			check();
			const batch = changed.slice(index, end);
			const ids = batch.map((row) => row.id);
			const bodies = await query<
				Omit<ModelSourceRow, "creator" | "narrator" | "toolCalls"> & {
					username: string | null;
					title: string | null;
				}
			>(
				`SELECT m.id,m.narrator_id AS "narratorId",m.body_revision AS "bodyRevision",m.role,m.content_json AS "contentJson",
				 m.content_text AS "contentText",m.parent_tool_use_id AS "parentToolUseId",m.sdk_message_uuid AS "messageUuid",
				 m.created_by AS "createdBy",m.origin,m.origin_label AS "originLabel",u.username,n.title
				 FROM narrator_messages m LEFT JOIN users u ON u.id=m.created_by LEFT JOIN narrators n ON n.id=m.narrator_id
				 WHERE m.id IN (${ids.map(() => "?").join(",")})`,
				ids,
			);
			if (bodies.length !== ids.length)
				throw new Error("Model history snapshot contains missing messages");
			const byId = new Map(
				bodies.map(({ username, title, ...body }) => {
					const jsonContainerBytes = modelJsonContainerBytes(
						body.contentJson,
						options.deadline,
						remainingBytes(),
					);
					check(0, 0, jsonContainerBytes);
					return [
						body.id,
						{
							...body,
							jsonContainerBytes,
							seq: batch.find((row) => row.id === body.id)?.seq ?? 0,
							contentJson: decode(body.contentJson),
							creator: username == null ? null : { username },
							narrator: { title },
							toolCalls: [],
						},
					];
				}),
			);
			await send({ type: "page", rows: ids.map((id) => byId.get(id)) });
			counters.bodyQueries++;
			counters.sourceBytes += bytes;
			counters.maxBodyBatchBytes = Math.max(counters.maxBodyBatchBytes, bytes);
			index = end;
		}
		const ids = page.map((row) => row.id);
		const placeholders = ids.map(() => "?").join(",");
		let messageId = "";
		let toolId = "";
		for (;;) {
			check();
			const tools = await query<{
				id: string;
				messageId: string;
				modelRevision: number;
				bytes: number;
			}>(
				`SELECT id,message_id AS "messageId",model_revision AS "modelRevision",
				 coalesce(length(CAST(input_json AS BLOB)),0)+coalesce(length(CAST(output_json AS BLOB)),0) AS bytes
				 FROM narrator_tool_calls WHERE message_id IN (${placeholders}) AND (message_id>? OR (message_id=? AND id>?))
				 ORDER BY message_id,id LIMIT 200`,
				[...ids, messageId, messageId, toolId],
			);
			counters.toolQueries++;
			if (!tools.length) break;
			const reusedTools = tools.filter(
				(tool) => oldToolRevisions.get(tool.id) === tool.modelRevision,
			);
			if (reusedTools.length) {
				for (const tool of reusedTools) check(0, 0, oldToolOverheads.get(tool.id) ?? 0);
				await send({
					type: "reuseTools",
					rows: reusedTools.map(({ id, messageId }) => ({ id, messageId })),
				});
				counters.reusedTools = (counters.reusedTools ?? 0) + reusedTools.length;
			}
			const kept = new Set(reusedTools.map((tool) => tool.id));
			const changedTools = tools.filter((tool) => !kept.has(tool.id));
			for (const tool of tools) check(Number(tool.bytes), 1);
			for (let index = 0; index < changedTools.length; ) {
				let end = index;
				let bytes = 0;
				while (
					end < changedTools.length &&
					(end === index || bytes + Number(changedTools[end].bytes) <= MODEL_HISTORY_BATCH_BYTES)
				)
					bytes += Number(changedTools[end++].bytes);
				check();
				const toolIds = changedTools.slice(index, end).map((tool) => tool.id);
				const bodies = await query<ModelToolSourceRow & { messageId: string }>(
					`SELECT id,message_id AS "messageId",model_revision AS "modelRevision",execution_attempt AS "executionAttempt",created_at AS "createdAt",
					 tool_use_id AS "toolUseId",tool_name AS "toolName",input_json AS "inputJson",output_json AS "outputJson",status
					 FROM narrator_tool_calls WHERE id IN (${toolIds.map(() => "?").join(",")})`,
					toolIds,
				);
				if (bodies.length !== toolIds.length)
					throw new Error("Model history snapshot contains missing tool rows");
				await send({
					type: "tools",
					rows: bodies.map((tool) => {
						const inputBytes = modelJsonContainerBytes(
							tool.inputJson,
							options.deadline,
							remainingBytes(),
						);
						const jsonContainerBytes =
							inputBytes +
							modelJsonContainerBytes(
								tool.outputJson,
								options.deadline,
								remainingBytes() - inputBytes,
							);
						check(0, 0, jsonContainerBytes);
						return {
							...tool,
							jsonContainerBytes,
							inputJson: decode(tool.inputJson),
							outputJson: decode(tool.outputJson),
						};
					}),
				});
				counters.toolQueries++;
				counters.sourceBytes += bytes;
				counters.maxToolBatchBytes = Math.max(counters.maxToolBatchBytes, bytes);
				index = end;
			}
			counters.toolRows += tools.length;
			messageId = tools[tools.length - 1].messageId;
			toolId = tools[tools.length - 1].id;
		}
	}
	// The main side restores ref order after applying body/tool pages; it holds no provider arrays.
	for (let offset = 0; offset < refs.length; offset += 200)
		await send({
			type: "order",
			reset: offset === 0,
			ids: refs.slice(offset, offset + 200).map((row) => row.id),
		});
	check();
	return { counters, compactSeq };
}
