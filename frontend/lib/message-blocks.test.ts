import { describe, expect, test } from "bun:test";
import type { ContentBlock, MessageBlockRef, TreeMessage } from "./api/types";
import { blockAtIndex, messageBlocks } from "./message-blocks";

function row(overrides: Partial<TreeMessage> & { origin?: string }): TreeMessage {
	return {
		id: "m1",
		narratorId: "n1",
		parentToolUseId: null,
		role: "assistant",
		contentJson: [],
		contentText: null,
		toolCalls: [],
		createdAt: "2026-01-01T00:00:00.000Z",
		children: [],
		...overrides,
	} as TreeMessage;
}

const ref = (id: string, kind: string, from: number, to: number): MessageBlockRef => ({
	id,
	kind,
	subtype: null,
	phase: "done",
	revision: 3,
	wireId: null,
	attemptId: "a1",
	hasOriginal: false,
	from,
	to,
});

describe("messageBlocks", () => {
	test("a row from the block protocol is its named blocks over its elements", () => {
		const contentJson = [
			{ type: "reasoning", text: "think" },
			{ type: "text", text: "hello" },
		] as ContentBlock[];
		const blocks = messageBlocks(
			row({ contentJson, blockRefs: [ref("b1", "reasoning", 0, 1), ref("b2", "output", 1, 2)] }),
		);
		expect(blocks.map((block) => [block.id, block.kind, block.revision, block.from])).toEqual([
			["b1", "reasoning", 3, 0],
			["b2", "output", 3, 1],
		]);
		expect(blocks[1].elements).toEqual([{ type: "text", text: "hello" }]);
	});

	test("a client-built row is split as the server splits a legacy envelope", () => {
		const assistant = row({
			contentJson: [
				{ type: "reasoning", text: "think" },
				{ type: "text", text: "hello" },
				{ type: "tool_use", id: "toolu_1", name: "Read", input: { path: "a" } },
			] as ContentBlock[],
		});
		expect(messageBlocks(assistant).map((block) => [block.id, block.kind, block.wireId])).toEqual([
			["m1:0", "reasoning", null],
			["m1:1", "output", null],
			["m1:2", "tool", "toolu_1"],
		]);
		// An injection and its legacy companion text are one context block.
		const injection = row({
			id: "s1",
			role: "sys",
			contentJson: [
				{ type: "text", text: "legacy" },
				{ type: "sidecar", source: "spec_update" },
			] as unknown as ContentBlock[],
		});
		const [context] = messageBlocks(injection);
		expect(messageBlocks(injection)).toHaveLength(1);
		expect(context).toMatchObject({ kind: "context", subtype: "system_injection", from: 0 });
		expect(context.elements).toHaveLength(2);
		// System-written user turns are context; human turns are input.
		const continued = row({
			id: "u1",
			role: "user",
			origin: "system",
			contentJson: [{ type: "text", text: "continue" }] as ContentBlock[],
		});
		expect(messageBlocks(continued)[0]).toMatchObject({ kind: "context" });
		expect(messageBlocks({ ...continued, origin: "user" } as TreeMessage)[0]).toMatchObject({
			kind: "input",
		});
	});

	test("refs that do not cover the elements are not trusted; the row is split", () => {
		const contentJson = [
			{ type: "text", text: "a" },
			{ type: "text", text: "b" },
		] as ContentBlock[];
		const blocks = messageBlocks(row({ contentJson, blockRefs: [ref("b1", "output", 0, 1)] }));
		expect(blocks.map((block) => block.id)).toEqual(["m1:0", "m1:1"]);
	});

	test("the same body and refs give the same array; a new body is split again", () => {
		const contentJson = [{ type: "text", text: "a" }] as ContentBlock[];
		const first = row({ contentJson });
		expect(messageBlocks(first)).toBe(messageBlocks({ ...first }));
		expect(messageBlocks({ ...first, contentJson: [...contentJson] })).not.toBe(
			messageBlocks(first),
		);
		expect(messageBlocks(row({ contentJson: [] }))).toEqual([]);
	});

	test("blockAtIndex names the block holding an element", () => {
		const message = row({
			contentJson: [
				{ type: "reasoning", text: "think" },
				{ type: "text", text: "hello" },
			] as ContentBlock[],
			blockRefs: [ref("b1", "reasoning", 0, 1), ref("b2", "output", 1, 2)],
		});
		expect(blockAtIndex(message, 1)?.id).toBe("b2");
		expect(blockAtIndex(message, 2)).toBeUndefined();
	});
});
