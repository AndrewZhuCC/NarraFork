/**
 * block-target.ts — the block an action on a message element names
 * (docs/BLOCK_STORAGE-DESIGN.md §5.5, P3d).
 *
 * Delete, rollback and edit act on whole blocks. The block holding the element is read
 * from the message's row when the action starts; a row the server did not name (one
 * built on the client) has no block id, and the action keeps its index route.
 */

import { blockAtIndex, messageBlocks } from "@shared/narrator-blocks/message-blocks";
import type { BlockTarget } from "../../../lib/api/narrators";
import type { MessageListHandle } from "./message-list-handle";

type MessageLookup = Pick<MessageListHandle, "messageOf"> | null | undefined;

/** Element `blockIndex` of the message, with the block holding it when the server named it. */
export function blockTargetOf(
	list: MessageLookup,
	messageId: string,
	blockIndex: number,
): BlockTarget {
	const message = list?.messageOf(messageId);
	const block = message ? blockAtIndex(message, blockIndex) : undefined;
	return block?.named ? { messageId, blockIndex, blockId: block.id } : { messageId, blockIndex };
}

/** The message's input block, when the server named exactly one. */
export function inputBlockIdOf(list: MessageLookup, messageId: string): string | undefined {
	const message = list?.messageOf(messageId);
	const inputs = message ? messageBlocks(message).filter((block) => block.kind === "input") : [];
	return inputs.length === 1 && inputs[0].named ? inputs[0].id : undefined;
}
