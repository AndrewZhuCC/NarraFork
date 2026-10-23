/**
 * message-blocks.ts — a message's blocks as the frontend holds and renders them
 * (docs/BLOCK_STORAGE-DESIGN.md, P3d).
 *
 * A row holds its body as blocks (`blocks`): each block is its identity (id, kind,
 * subtype, phase, revision, wire id) and the elements it holds. The protocol decoder
 * gives every row the blocks the server named. A row built on the client — the streaming
 * row, chat and share rows — is split into blocks exactly as the server splits a legacy
 * envelope (`splitRowBlocks`), so both kinds group their elements the same way.
 *
 * Readers take the body through `messageBody` (the elements) or `messageBlocks` (the
 * blocks); code that rewrites a row's body goes through `mapRowElements`,
 * `filterRowElements` or `rowWithElements`, which keep each block's identity.
 */

import { splitEnvelope } from "./storage";
import type { EnvelopeRole } from "./types";

/** One block a row holds: its identity and its elements. */
export interface RowBlock<E = unknown> {
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
}

/** One block of a message, with where its elements start in the message's body. */
export interface MessageBlock<E = unknown> extends RowBlock<E> {
	from: number;
}

/** The least a row needs for `messageBody`: whatever holds its elements. */
export interface MessageBodySource {
	blocks?: readonly RowBlock<unknown>[] | null;
	/** A row not yet built with blocks (test fixtures, until P3d 7b-2). */
	contentJson?: unknown;
}

/** A row whose body holds elements of type `E` — what structural row types extend. */
export interface MessageWithBody<E> extends MessageBodySource {
	blocks?: readonly RowBlock<E>[] | null;
}

/** What a message needs for its blocks: its body, and what a client-built row splits by. */
export interface BlockSourceMessage<E> extends MessageWithBody<E> {
	id?: string;
	narratorId?: string | null;
	role: string;
	contentText?: string | null;
	parentToolUseId?: string | null;
	messageUuid?: string | null;
	provider?: string | null;
	/** Who wrote the row; server rows carry it, and it decides input versus context. */
	origin?: unknown;
}

type ElementsOfContent<M> = M extends { contentJson?: infer B }
	? NonNullable<B> extends readonly (infer E)[]
		? readonly E[]
		: readonly unknown[]
	: readonly unknown[];

/** A row's elements, typed after its blocks. */
export type MessageBody<M> = M extends { blocks?: infer R }
	? NonNullable<R> extends readonly RowBlock<infer E>[]
		? readonly E[]
		: ElementsOfContent<M>
	: ElementsOfContent<M>;

const EMPTY: readonly never[] = [];

/** Each block list's elements, one after another. */
const bodies = new WeakMap<readonly RowBlock<unknown>[], readonly unknown[]>();

/**
 * The message's elements in order: its blocks' elements, one after another. The same
 * array while the row's blocks are the same. Readers take a message's body through here,
 * never from the row's storage field.
 */
export function messageBody<M extends MessageBodySource>(
	message: M | null | undefined,
): MessageBody<M> {
	const blocks = message?.blocks;
	if (Array.isArray(blocks)) return flatten(blocks) as unknown as MessageBody<M>;
	const body = message?.contentJson;
	return (Array.isArray(body) ? body : EMPTY) as unknown as MessageBody<M>;
}

function flatten(blocks: readonly RowBlock<unknown>[]): readonly unknown[] {
	if (blocks.length === 0) return EMPTY;
	if (blocks.length === 1) return blocks[0].elements;
	let body = bodies.get(blocks);
	if (!body) {
		body = blocks.flatMap((block) => block.elements);
		bodies.set(blocks, body);
	}
	return body;
}

/** Each block list with its starting positions. */
const placed = new WeakMap<readonly RowBlock<unknown>[], readonly MessageBlock[]>();

/** The last split of each body, with what it was split for (copies can share a body). */
const splits = new WeakMap<
	readonly unknown[],
	{ id: string | undefined; role: string; origin: unknown; blocks: readonly MessageBlock[] }
>();

/** The message's blocks in order; the same array while its blocks (or body) are the same. */
export function messageBlocks<E>(message: BlockSourceMessage<E>): readonly MessageBlock<E>[] {
	const held = message.blocks;
	if (Array.isArray(held)) {
		let blocks = placed.get(held);
		if (!blocks) {
			blocks = place(held);
			placed.set(held, blocks);
		}
		return blocks as readonly MessageBlock<E>[];
	}
	const elements = messageBody(message) as readonly E[];
	const { id, role, origin } = message;
	const cached = splits.get(elements);
	if (cached && cached.id === id && cached.role === role && cached.origin === origin)
		return cached.blocks as readonly MessageBlock<E>[];
	const blocks = place(splitRowBlocks(message, elements));
	if (elements !== EMPTY) splits.set(elements, { id, role, origin, blocks });
	return blocks;
}

function place<E>(blocks: readonly RowBlock<E>[]): MessageBlock<E>[] {
	let from = 0;
	return blocks.map((block) => {
		const at = from;
		from += block.elements.length;
		return { ...block, from: at };
	});
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

/** What `splitRowBlocks` needs of a client-built row besides its elements. */
export type SplitSource = Omit<BlockSourceMessage<unknown>, "blocks" | "contentJson">;

/**
 * A client-built row's elements as blocks, split as the server splits a legacy envelope:
 * a block's id is its element's identity where it has one, else `<messageId>:<position>`.
 */
export function splitRowBlocks<E>(message: SplitSource, elements: readonly E[]): RowBlock<E>[] {
	if (!elements.length) return [];
	const messageId = message.id ?? "msg";
	const { blocks } = splitEnvelope({
		id: messageId,
		narratorId: message.narratorId ?? undefined,
		role: message.role as EnvelopeRole,
		contentJson: elements as unknown[],
		contentText: message.contentText ?? null,
		parentToolUseId: message.parentToolUseId ?? null,
		messageUuid: message.messageUuid ?? null,
		origin: typeof message.origin === "string" ? message.origin : null,
		provider: message.provider ?? null,
	});
	let from = 0;
	return blocks.map((draft) => {
		const count = draft.payload.raw.length;
		const block: RowBlock<E> = {
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
		};
		from += count;
		return block;
	});
}

/** The row's fields other than its body, to merge another row's body into. */
export function withoutBody<M extends MessageBodySource>(row: M): Omit<M, keyof MessageBodySource> {
	const { blocks: _blocks, contentJson: _contentJson, ...rest } = row;
	return rest;
}

/** `row` holding `blocks` as its body (and no longer any body it was built with). */
function withBlocks<M extends MessageBodySource>(row: M, blocks: readonly RowBlock<unknown>[]): M {
	return { ...withoutBody(row), blocks } as unknown as M;
}

/**
 * A client-built row's body replaced by `elements`, split again into blocks. For a row
 * built on the client only: a server row's blocks keep their ids through
 * `mapRowElements` / `filterRowElements`.
 */
export function rowWithElements<M extends BlockSourceMessage<unknown>>(
	row: M,
	elements: MessageBody<M>,
): M {
	return withBlocks(row, splitRowBlocks(row, elements));
}

type ElementOf<M> = MessageBody<M>[number];

/**
 * Each element of the row through `map` (`index` is its position in the body). A block
 * keeps its identity; the row and every unchanged block stay the same objects.
 */
export function mapRowElements<M extends BlockSourceMessage<unknown>>(
	row: M,
	map: (element: ElementOf<M>, index: number) => ElementOf<M>,
): M {
	let changed = false;
	const blocks = messageBlocks(row).map((block, index) => {
		let blockChanged = false;
		const elements = block.elements.map((element, offset) => {
			const next = map(element as ElementOf<M>, block.from + offset);
			if (next !== element) blockChanged = true;
			return next;
		});
		if (!blockChanged) return unplaced(row, block, index);
		changed = true;
		const { from: _from, ...rest } = block;
		return { ...rest, elements };
	});
	return changed ? withBlocks(row, blocks) : row;
}

/**
 * The row with only the elements `keep` accepts (`index` is the position in the body). A
 * block left with no elements is dropped; the others keep their identity. The same row
 * when every element is kept.
 */
export function filterRowElements<M extends BlockSourceMessage<unknown>>(
	row: M,
	keep: (element: ElementOf<M>, index: number) => boolean,
): M {
	let changed = false;
	const blocks: RowBlock<unknown>[] = [];
	for (const [index, block] of messageBlocks(row).entries()) {
		const elements = block.elements.filter((element, offset) =>
			keep(element as ElementOf<M>, block.from + offset),
		);
		if (elements.length === block.elements.length) {
			blocks.push(unplaced(row, block, index));
			continue;
		}
		changed = true;
		if (!elements.length) continue;
		const { from: _from, ...rest } = block;
		blocks.push({ ...rest, elements });
	}
	return changed ? withBlocks(row, blocks) : row;
}

/** The row's own block at `index` (`messageBlocks` keeps the order); a copy for a split row. */
function unplaced<E>(
	row: BlockSourceMessage<E>,
	block: MessageBlock<E>,
	index: number,
): RowBlock<E> {
	const own = row.blocks?.[index];
	if (own) return own;
	const { from: _from, ...rest } = block;
	return rest;
}
