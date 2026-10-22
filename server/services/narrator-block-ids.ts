/**
 * narrator-block-ids.ts — block ids handed out before a block's row exists
 * (docs/BLOCK_STORAGE-DESIGN.md §3.2, §5.2).
 *
 * A streaming item is addressed by its block id from the moment it opens: the first delta
 * of a text or reasoning element, the first input chunk of a tool call. Its row is only
 * written at the step's next checkpoint, so the id is kept here, under the narrator and
 * the element's own identity — `${attempt}:${order}` for output and reasoning, the call id
 * for a tool. Whichever side needs it first takes it:
 *   - the WS protocol v2 adapter, to address deltas and the reconnect snapshot;
 *   - the store's streaming sync, when it inserts the element's row.
 * The other side then finds the same id. Element identities do not repeat: a retried
 * attempt streams under a new attempt id. Entries are only needed while a step streams,
 * so the map is bounded and drops its oldest entries.
 */

import { generateId } from "../lib/id";

const LIMIT = 4096;
const ids = new Map<string, string>();

const keyOf = (narratorId: string, elementId: string) => `${narratorId}\u0000${elementId}`;

/** The block id of a streaming element, handed out now if it has none yet. */
export function blockIdForElement(
	narratorId: string,
	elementId: string,
): { blockId: string; opened: boolean } {
	const key = keyOf(narratorId, elementId);
	const known = ids.get(key);
	if (known) return { blockId: known, opened: false };
	const blockId = generateId();
	ids.set(key, blockId);
	if (ids.size > LIMIT) ids.delete(ids.keys().next().value as string);
	return { blockId, opened: true };
}

/** The block id already handed out for a streaming element, if any. */
export function knownBlockIdForElement(narratorId: string, elementId: string): string | undefined {
	return ids.get(keyOf(narratorId, elementId));
}

/** Point an element at a different block id (its handed-out id was taken by another row). */
export function reassignBlockIdForElement(
	narratorId: string,
	elementId: string,
	blockId: string,
): void {
	ids.set(keyOf(narratorId, elementId), blockId);
}

/** Forget every handed-out id (tests). */
export function clearBlockIdsForElements(): void {
	ids.clear();
}
