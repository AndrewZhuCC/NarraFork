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

import { splitEnvelope } from "./storage";
import type { EnvelopeRole } from "./types";

/** A block a message names, over its elements `[from, to)` (the block protocol's refs). */
export interface MessageBlockRefLike {
	id: string;
	kind?: string;
	subtype?: string | null;
	phase?: string;
	revision?: number;
	wireId?: string | null;
	attemptId?: string | null;
	hasOriginal?: boolean;
	from: number;
	to: number;
}

/** What a message needs for its blocks: its body, and its refs when it has them. */
export interface BlockSourceMessage<E> {
	id?: string;
	narratorId?: string | null;
	role: string;
	contentJson: readonly E[] | unknown;
	contentText?: string | null;
	parentToolUseId?: string | null;
	messageUuid?: string | null;
	provider?: string | null;
	/** Who wrote the row; server rows carry it, and it decides input versus context. */
	origin?: unknown;
	blockRefs?: readonly MessageBlockRefLike[];
}

/** One block of a message: its identity and its elements. */
export interface MessageBlock<E = unknown> {
	id: string;
	kind: string;
	subtype: string | null;
	phase: string;
	revision: number;
	wireId: string | null;
	attemptId: string | null;
	/** An edited output block keeps its body from before the first edit. */
	hasOriginal: boolean;
	/**
	 * The server named this block, so its id addresses it in the block routes. A
	 * client-built row's blocks are split here and their ids are derived.
	 */
	named: boolean;
	elements: readonly E[];
	/** Where its elements start in the message's `contentJson` (until rows hold blocks). */
	from: number;
}

const EMPTY: readonly never[] = [];

/** The least a row needs for `messageBody`: whatever holds its elements. */
export interface MessageBodySource {
	contentJson?: unknown;
}

/** A row's elements, typed after its body. */
export type MessageBody<M> = M extends { contentJson?: infer B }
	? NonNullable<B> extends readonly (infer E)[]
		? readonly E[]
		: readonly unknown[]
	: readonly unknown[];

/**
 * The message's elements in order: its blocks' elements, one after another. Readers take a
 * message's body through here, never from the row's storage field, so the row can change
 * how it holds its blocks without them.
 */
export function messageBody<M extends MessageBodySource>(
	message: M | null | undefined,
): MessageBody<M> {
	const body = message?.contentJson;
	return (Array.isArray(body) ? body : EMPTY) as unknown as MessageBody<M>;
}

/** The last split of each body, with what it was split for (copies can share a body). */
const cache = new WeakMap<
	readonly unknown[],
	{
		id: string | undefined;
		role: string;
		origin: unknown;
		refs: readonly MessageBlockRefLike[] | undefined;
		blocks: readonly MessageBlock[];
	}
>();

/** The message's blocks in order; the same array while its body and refs are the same. */
export function messageBlocks<E>(message: BlockSourceMessage<E>): readonly MessageBlock<E>[] {
	const elements = messageBody(message) as readonly E[];
	const { id, role, origin, blockRefs: refs } = message;
	const cached = cache.get(elements);
	if (
		cached &&
		cached.refs === refs &&
		cached.id === id &&
		cached.role === role &&
		cached.origin === origin
	)
		return cached.blocks as readonly MessageBlock<E>[];
	const blocks = refsCover(refs, elements.length)
		? fromRefs(refs, elements)
		: split(message, elements);
	if (elements !== EMPTY) cache.set(elements, { id, role, origin, refs, blocks });
	return blocks;
}

/** The message's block holding element `index`. */
export function blockAtIndex<E>(
	message: BlockSourceMessage<E>,
	index: number,
): MessageBlock<E> | undefined {
	return messageBlocks(message).find(
		(block) => index >= block.from && index < block.from + block.elements.length,
	);
}

function refsCover(
	refs: readonly MessageBlockRefLike[] | undefined,
	length: number,
): refs is readonly MessageBlockRefLike[] {
	if (!refs?.length) return false;
	let next = 0;
	for (const ref of refs) {
		if (ref.from !== next || ref.to < ref.from) return false;
		next = ref.to;
	}
	return next === length;
}

function fromRefs<E>(
	refs: readonly MessageBlockRefLike[],
	elements: readonly E[],
): MessageBlock<E>[] {
	return refs.map((ref) => ({
		id: ref.id,
		kind: ref.kind ?? "unknown",
		subtype: ref.subtype ?? null,
		phase: ref.phase ?? "done",
		revision: ref.revision ?? 0,
		wireId: ref.wireId ?? null,
		attemptId: ref.attemptId ?? null,
		hasOriginal: ref.hasOriginal ?? false,
		named: true,
		elements: elements.slice(ref.from, ref.to),
		from: ref.from,
	}));
}

/** A client-built message, split into blocks as the server splits a legacy envelope. */
function split<E>(message: BlockSourceMessage<E>, elements: readonly E[]): MessageBlock<E>[] {
	if (!elements.length) return [];
	const messageId = message.id ?? "msg";
	const { blocks } = splitEnvelope({
		id: messageId,
		narratorId: message.narratorId ?? undefined,
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
		const block: MessageBlock<E> = {
			id: draft.canonical?.id ?? `${messageId}:${from}`,
			kind: draft.kind,
			subtype: draft.subtype,
			phase: draft.phase,
			revision: 0,
			wireId: draft.wireId,
			attemptId: null,
			hasOriginal: false,
			named: false,
			elements: elements.slice(from, from + count),
			from,
		};
		from += count;
		return block;
	});
}
