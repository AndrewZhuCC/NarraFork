import { describe, expect, test } from "bun:test";
import { parseHTML } from "linkedom";
import { resolveChatCommentAnchor } from "./comment-anchor";
import { BLOCK_ID_ATTR } from "./message/MessageSelectionCtx";

function makeTree() {
	const { document } = parseHTML("<div id='root'></div>");
	const root = document.getElementById("root");
	if (!root) throw new Error("missing test root");
	return { document, root };
}

describe("resolveChatCommentAnchor", () => {
	test("returns null when the node is not inside a message", () => {
		const { document, root } = makeTree();
		const spec = document.createElement("p");
		spec.textContent = "spec markdown";
		root.appendChild(spec);

		expect(resolveChatCommentAnchor(spec, root as unknown as HTMLElement)).toBeNull();
		expect(resolveChatCommentAnchor(spec.firstChild, root as unknown as HTMLElement)).toBeNull();
		expect(resolveChatCommentAnchor(null, root as unknown as HTMLElement)).toBeNull();
	});

	test("picks the nearest message block and optional block index/id", () => {
		const { document, root } = makeTree();
		const block = document.createElement("div");
		block.setAttribute("data-message-id", "m1");
		block.setAttribute("data-block-index", "4");
		block.setAttribute("data-message-seq", "12");
		block.setAttribute("data-role", "assistant");
		block.setAttribute(BLOCK_ID_ATTR, "msg-m1-4");
		const inner = document.createElement("span");
		inner.textContent = "quoted";
		block.appendChild(inner);
		root.appendChild(block);

		expect(resolveChatCommentAnchor(inner.firstChild, root as unknown as HTMLElement)).toEqual({
			messageId: "m1",
			messageSeq: 12,
			role: "assistant",
			blockIndex: 4,
			blockId: "msg-m1-4",
		});
	});

	test("omits blockIndex when the attribute is missing or not an integer", () => {
		const { document, root } = makeTree();
		const block = document.createElement("div");
		block.setAttribute("data-message-id", "m2");
		block.setAttribute("data-block-index", "nope");
		root.appendChild(block);

		expect(resolveChatCommentAnchor(block, root as unknown as HTMLElement)).toEqual({
			messageId: "m2",
		});
	});

	test("omits invalid message identity attributes", () => {
		const { document, root } = makeTree();
		const block = document.createElement("div");
		block.setAttribute("data-message-id", "m3");
		block.setAttribute("data-message-seq", "-1");
		block.setAttribute("data-role", "tool");
		root.appendChild(block);

		expect(resolveChatCommentAnchor(block, root as unknown as HTMLElement)).toEqual({
			messageId: "m3",
		});
	});

	test("stops at the container so a message outside the chat is ignored", () => {
		const { document, root } = makeTree();
		const outside = document.createElement("div");
		outside.setAttribute("data-message-id", "outside");
		const container = document.createElement("div");
		const inner = document.createElement("p");
		inner.textContent = "plain";
		container.appendChild(inner);
		outside.appendChild(container);
		root.appendChild(outside);

		expect(resolveChatCommentAnchor(inner, container as unknown as HTMLElement)).toBeNull();
	});
});
