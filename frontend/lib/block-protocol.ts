/**
 * block-protocol.ts — the block-keyed message protocol v2, decoded at the transport
 * boundary (docs/BLOCK_STORAGE-DESIGN.md §8, P2c-3).
 *
 * The server sends messages as envelopes that name their blocks, plus the blocks. This
 * module turns pages and WS frames back into the v1 shapes the document store renders —
 * `TreeMessage` rows with `contentJson` and nested `children` — and keeps each row's
 * blocks alongside as `blockRefs`: every block's id and the elements `[from, to)` it holds
 * in `contentJson`, which block-addressed actions use. Anything that is not v2 passes
 * through unchanged, so a server with `narratorBlocks.wsV2` off keeps working.
 */

import type { ContentBlock, MessageBlockRef, TreeMessage } from "./api/types";

/** One block as the server sends it. */
interface BlockDTO {
	id: string;
	messageId: string;
	ordinal: number;
	kind: string;
	subtype: string | null;
	phase: string;
	revision: number;
	wireId: string | null;
	attemptId: string | null;
	elements: unknown[];
	hasOriginal: boolean;
}

/** A message row without its body: its blocks, named in order. */
type EnvelopeDTO = Record<string, unknown> & {
	id: string;
	blockIds: string[];
	/** The row it is nested under (v1 `children`); null at the top. */
	parentMessageId: string | null;
};

type Frame = Record<string, unknown>;

const array = <T>(value: unknown): T[] => (Array.isArray(value) ? (value as T[]) : []);
const text = (value: unknown) => (typeof value === "string" ? value : undefined);
const number = (value: unknown) => (typeof value === "number" ? value : undefined);

/**
 * Envelopes + blocks as v1 rows, in envelope order. A row nested under another one in v1
 * goes back into that row's `children`; `withChildren` gives every row a `children` list,
 * as page and catch-up rows have one.
 */
function rowsOf(
	envelopes: EnvelopeDTO[],
	blocks: BlockDTO[],
	withChildren: boolean,
): Array<{ row: TreeMessage; parentMessageId: string | null }> {
	const byBlockId = new Map(blocks.map((block) => [block.id, block]));
	const rows: Array<{ row: TreeMessage; parentMessageId: string | null }> = [];
	const byId = new Map<string, TreeMessage>();
	for (const envelope of envelopes) {
		const { blockIds, parentMessageId, ...fields } = envelope;
		const contentJson: unknown[] = [];
		const blockRefs: MessageBlockRef[] = [];
		for (const blockId of blockIds) {
			const block = byBlockId.get(blockId);
			if (!block) continue;
			const from = contentJson.length;
			contentJson.push(...block.elements);
			blockRefs.push({
				id: block.id,
				kind: block.kind,
				subtype: block.subtype,
				phase: block.phase,
				revision: block.revision,
				wireId: block.wireId,
				attemptId: block.attemptId,
				hasOriginal: block.hasOriginal,
				from,
				to: contentJson.length,
			});
		}
		const row = {
			...fields,
			contentJson: contentJson as ContentBlock[],
			blockRefs,
			...(withChildren ? { children: [] } : {}),
		} as unknown as TreeMessage;
		const parent = parentMessageId ? byId.get(parentMessageId) : undefined;
		if (parent) {
			parent.children ??= [];
			parent.children.push(row);
		} else rows.push({ row, parentMessageId: parentMessageId ?? null });
		byId.set(row.id, row);
	}
	return rows;
}

/** A page from `pretext-document?protocol=2` as the v1 page; a v1 page as it is. */
export function decodeBlockPage<T>(page: T): T {
	const value = page as unknown as Frame;
	if (!value || value.protocol !== 2) return page;
	const { protocol: _protocol, messages, blocks, ...rest } = value;
	const rows = rowsOf(array<EnvelopeDTO>(messages), array<BlockDTO>(blocks), true);
	return { ...rest, messages: rows.map(({ row }) => row) } as unknown as T;
}

/**
 * A streamed delta in v2 as the v1 `stream_event` it maps from. v1 carries a text item's
 * output index on the event and a reasoning item's on the delta.
 */
function streamEvent(frame: Frame): Frame {
	const kind = frame.kind === "reasoning" ? "reasoning" : "output";
	const outputIndex = number(frame.outputIndex);
	const delta: Frame = {
		type: kind === "reasoning" ? "reasoning_delta" : "text_delta",
		text: text(frame.text) ?? "",
		id: frame.elementId,
		...(number(frame.revision) !== undefined ? { revision: frame.revision } : {}),
		...(number(frame.textOffset) !== undefined ? { textOffset: frame.textOffset } : {}),
		...(kind === "reasoning" && outputIndex !== undefined ? { outputIndex } : {}),
	};
	return {
		type: "stream_event",
		narratorId: frame.narratorId,
		event: {
			type: "content_block_delta",
			delta,
			...(kind === "output" && outputIndex !== undefined ? { outputIndex } : {}),
			...(frame.fileReferenceContext !== undefined
				? { fileReferenceContext: frame.fileReferenceContext }
				: {}),
			...(text(frame.subagentToolUseId) ? { subagentToolUseId: frame.subagentToolUseId } : {}),
			...(text(frame.subagentNarratorId) ? { subagentNarratorId: frame.subagentNarratorId } : {}),
		},
	};
}

/**
 * The v1 frames one received frame stands for: a v2 frame decoded, any other frame as
 * it is. A message frame keeps its v1 type (`cause`), so per-frame message-version
 * counting and every listener see exactly what v1 sent.
 */
export function decodeBlockFrame(frame: Frame): Frame[] {
	switch (frame.type) {
		case "message_upserted": {
			const { type: _type, cause, messages, blocks, ...rest } = frame;
			const rows = rowsOf(array<EnvelopeDTO>(messages), array<BlockDTO>(blocks), false);
			return rows.map(({ row }) => ({ ...rest, type: cause, message: row }));
		}
		case "block_delta":
			return [streamEvent(frame)];
		// Its id reaches the store with the block's first checkpoint; nothing to render yet.
		case "block_opened":
		case "message_protocol":
			return [];
		case "catch_up": {
			if (frame.protocol !== 2) return [frame];
			const { protocol: _protocol, messages, blocks, orphanMessageIds, ...rest } = frame;
			const orphans = new Set(array<string>(orphanMessageIds));
			const rows = rowsOf(array<EnvelopeDTO>(messages), array<BlockDTO>(blocks), true);
			return [
				{
					...rest,
					topLevel: rows.filter(({ row }) => !orphans.has(row.id)).map(({ row }) => row),
					orphanChildren: rows.filter(({ row }) => orphans.has(row.id)).map(({ row }) => row),
				},
			];
		}
		default:
			return [frame];
	}
}
