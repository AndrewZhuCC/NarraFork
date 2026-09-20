import { BLOCK_ID_ATTR } from "./message/MessageSelectionCtx";

export type ChatCommentRole = "user" | "assistant" | "system";

export type ChatCommentAnchor = {
	messageId: string;
	messageSeq?: number;
	role?: ChatCommentRole;
	blockIndex?: number;
	blockId?: string;
};

function parseMessageSeq(value: string | null): number | undefined {
	if (value == null || value.trim() === "") return undefined;
	const parsed = Number(value);
	return Number.isInteger(parsed) && parsed >= 0 ? parsed : undefined;
}

function parseMessageRole(value: string | null): ChatCommentRole | undefined {
	return value === "user" || value === "assistant" || value === "system" ? value : undefined;
}

/**
 * Walk from a selection node up to `container` and pick the nearest chat block.
 * Returns null when the selection is not inside a message (e.g. Spec markdown).
 */
export function resolveChatCommentAnchor(
	node: Node | null,
	container?: HTMLElement | null,
): ChatCommentAnchor | null {
	let el: Element | null = node?.nodeType === 1 ? (node as Element) : (node?.parentElement ?? null);
	while (el && el !== container) {
		const messageId = el.getAttribute("data-message-id");
		if (messageId) {
			const blockIndexStr = el.getAttribute("data-block-index");
			const parsed = blockIndexStr == null ? Number.NaN : Number(blockIndexStr);
			const blockId = el.getAttribute(BLOCK_ID_ATTR);
			const messageSeq = parseMessageSeq(el.getAttribute("data-message-seq"));
			const role = parseMessageRole(el.getAttribute("data-role"));
			return {
				messageId,
				...(messageSeq !== undefined ? { messageSeq } : {}),
				...(role ? { role } : {}),
				...(Number.isInteger(parsed) && parsed >= 0 ? { blockIndex: parsed } : {}),
				...(blockId ? { blockId } : {}),
			};
		}
		el = el.parentElement;
	}
	return null;
}
