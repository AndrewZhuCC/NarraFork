/**
 * block-refs.ts — the blocks each message was last seen with, for block-addressed actions
 * (docs/BLOCK_STORAGE-DESIGN.md §5.5, P2c-3).
 *
 * The block-keyed protocol decoder (`block-protocol.ts`) records every row's blocks here
 * as it decodes them. An action that names a message element — "delete block 3 of
 * message m", "roll back to here" — resolves the block holding that element and calls the
 * block-id route, so it acts on the whole block whatever else shares the message. A
 * message seen only from a v1 server has no entry, and the action keeps its index route.
 */

import type { MessageBlockRef } from "./api/types";

/** Messages remembered at most; the oldest are dropped first. */
const LIMIT = 20_000;
const byMessage = new Map<string, MessageBlockRef[]>();

/** Record the blocks a decoded message row arrived with (its latest view wins). */
export function rememberBlockRefs(messageId: string, refs: MessageBlockRef[]): void {
	byMessage.delete(messageId);
	byMessage.set(messageId, refs);
	if (byMessage.size > LIMIT) byMessage.delete(byMessage.keys().next().value as string);
}

/** The block of `messageId` holding element `index`, when the message was seen in v2. */
export function blockAt(messageId: string, index: number): MessageBlockRef | undefined {
	return byMessage.get(messageId)?.find((block) => index >= block.from && index < block.to);
}

/** The message's single block of `kind` (an input's block), when it has exactly one. */
export function onlyBlockOfKind(messageId: string, kind: string): MessageBlockRef | undefined {
	const blocks = byMessage.get(messageId)?.filter((block) => block.kind === kind) ?? [];
	return blocks.length === 1 ? blocks[0] : undefined;
}

/** Forget every remembered message (tests). */
export function clearBlockRefs(): void {
	byMessage.clear();
}
