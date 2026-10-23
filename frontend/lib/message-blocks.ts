/**
 * message-blocks.ts — a message's blocks as the frontend renders them
 * (docs/BLOCK_STORAGE-DESIGN.md, P3d).
 *
 * Every reader that walks a message's body goes through `messageBlocks`, never through
 * `contentJson` indexes: each block is its identity (id, kind, subtype, phase, revision,
 * wire id) and the elements it holds. A message that arrived in the block protocol names
 * its blocks (`blockRefs`). One built on the client — the streaming row, chat and share
 * rows, test fixtures — is split into blocks exactly as the server splits a legacy
 * envelope (`splitEnvelope`), so both kinds group their elements the same way.
 */

import { splitEnvelope } from "@shared/narrator-blocks/storage";
import type { EnvelopeRole } from "@shared/narrator-blocks/types";
import type { ContentBlock, MessageBlockRef, TreeMessage } from "./api/types";

/** One block of a message: its identity and its elements. */
export interface MessageBlock {
	id: string;
	kind: string;
	subtype: string | null;
	phase: string;
	revision: number;
	wireId: string | null;
	attemptId: string | null;
	/** An edited output block keeps its body from before the first edit. */
	hasOriginal: boolean;
	elements: readonly ContentBlock[];
	/** Where its elements start in the message's `contentJson` (until rows hold blocks). */
	from: number;
}

type BlockSource = Pick<
	TreeMessage,
	"id" | "role" | "contentJson" | "contentText" | "parentToolUseId"
> &
	Partial<Pick<TreeMessage, "narratorId" | "messageUuid" | "provider" | "blockRefs">> & {
		/** Who wrote the row; server rows carry it, and it decides input versus context. */
		origin?: unknown;
	};

/** The last split of each body, with what it was split for (copies can share a body). */
const cache = new WeakMap<
	readonly ContentBlock[],
	{
		id: string;
		role: string;
		origin: unknown;
		refs: readonly MessageBlockRef[] | undefined;
		blocks: readonly MessageBlock[];
	}
>();

/** The message's blocks in order; the same array while its body and refs are the same. */
export function messageBlocks(message: BlockSource): readonly MessageBlock[] {
	const elements = Array.isArray(message.contentJson) ? message.contentJson : [];
	const { id, role, origin, blockRefs: refs } = message;
	const cached = cache.get(elements);
	if (
		cached &&
		cached.refs === refs &&
		cached.id === id &&
		cached.role === role &&
		cached.origin === origin
	)
		return cached.blocks;
	const blocks = refsCover(refs, elements.length)
		? fromRefs(refs, elements)
		: split(message, elements);
	cache.set(elements, { id, role, origin, refs, blocks });
	return blocks;
}

/** The message's block holding element `index`. */
export function blockAtIndex(message: BlockSource, index: number): MessageBlock | undefined {
	return messageBlocks(message).find(
		(block) => index >= block.from && index < block.from + block.elements.length,
	);
}

function refsCover(
	refs: readonly MessageBlockRef[] | undefined,
	length: number,
): refs is readonly MessageBlockRef[] {
	if (!refs?.length) return false;
	let next = 0;
	for (const ref of refs) {
		if (ref.from !== next || ref.to < ref.from) return false;
		next = ref.to;
	}
	return next === length;
}

function fromRefs(
	refs: readonly MessageBlockRef[],
	elements: readonly ContentBlock[],
): MessageBlock[] {
	return refs.map(({ from, to, ...ref }) => ({
		...ref,
		elements: elements.slice(from, to),
		from,
	}));
}

/** A client-built message, split into blocks as the server splits a legacy envelope. */
function split(message: BlockSource, elements: readonly ContentBlock[]): MessageBlock[] {
	if (!elements.length) return [];
	const { blocks } = splitEnvelope({
		id: message.id,
		narratorId: message.narratorId,
		role: message.role as EnvelopeRole,
		contentJson: elements,
		contentText: message.contentText ?? null,
		parentToolUseId: message.parentToolUseId ?? null,
		messageUuid: message.messageUuid ?? null,
		origin: typeof message.origin === "string" ? message.origin : null,
		provider: message.provider ?? null,
	});
	let from = 0;
	return blocks.map((draft) => {
		const count = draft.payload.raw.length;
		const block: MessageBlock = {
			id: draft.canonical?.id ?? `${message.id}:${from}`,
			kind: draft.kind,
			subtype: draft.subtype,
			phase: draft.phase,
			revision: 0,
			wireId: draft.wireId,
			attemptId: null,
			hasOriginal: false,
			elements: elements.slice(from, from + count),
			from,
		};
		from += count;
		return block;
	});
}
