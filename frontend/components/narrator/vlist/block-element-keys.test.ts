import { beforeEach, describe, expect, test } from "bun:test";
import type { TreeMessage } from "@frontend/lib/api/types";
import { clearBlockRefs, rememberBlockRefs } from "@frontend/lib/block-refs";
import { segmentMessages } from "../message/message-segments";
import { type AdapterSegment, adaptSegments } from "./segment-adapter";
import { resolveVListBlockTarget } from "./vlist-block-target";

/**
 * A message from the block-keyed protocol keys its elements by block id (§8.3), so a row
 * keeps its key when a block before it is deleted; a message from a v1 server keeps the
 * positional keys.
 */
type Ref = NonNullable<TreeMessage["blockRefs"]>[number];
const ref = (id: string, from: number, to = from + 1): Ref => ({
	id,
	kind: "output",
	subtype: null,
	phase: "done",
	revision: 1,
	wireId: null,
	attemptId: null,
	hasOriginal: false,
	from,
	to,
});

function message(texts: string[], blockRefs?: Ref[]): TreeMessage {
	return {
		id: "m1",
		narratorId: "n1",
		parentToolUseId: null,
		role: "assistant",
		contentText: null,
		createdAt: "2026-10-01T00:00:00.000Z",
		toolCalls: [],
		children: [],
		contentJson: texts.map((text) => ({ type: "text", text })),
		...(blockRefs ? { blockRefs } : {}),
	} as unknown as TreeMessage;
}

const keysOf = (msg: TreeMessage) =>
	adaptSegments(segmentMessages([msg]) as unknown as AdapterSegment[], { lod: 5 })
		.map((spec) => spec.key)
		.filter((key) => key.startsWith("m1-b"));

beforeEach(clearBlockRefs);

describe("element keys", () => {
	test("block-keyed rows keep their key when a block before them goes", () => {
		const before = keysOf(message(["first", "second"], [ref("bA", 0), ref("bB", 1)]));
		expect(before).toEqual(["m1-blk[bA]", "m1-blk[bB]"]);
		const after = keysOf(message(["second"], [ref("bB", 0)]));
		expect(after).toEqual(["m1-blk[bB]"]);
	});

	test("rows from a v1 server keep their positional keys", () => {
		expect(keysOf(message(["first", "second"]))).toEqual(["m1-b0", "m1-b1"]);
	});

	test("a block-keyed row resolves to the element index it renders", () => {
		const refs = [ref("bA", 0, 2), ref("bB", 2)];
		rememberBlockRefs("m1", refs);
		expect(resolveVListBlockTarget("markdown", "m1-blk[bB]", ["m1"])).toMatchObject({
			messageId: "m1",
			blockIndex: 2,
		});
		expect(resolveVListBlockTarget("markdown", "m1-blk[bA]+1", ["m1"])).toMatchObject({
			blockIndex: 1,
		});
		// A block that is not one of the row's message's resolves to no target.
		expect(resolveVListBlockTarget("markdown", "m1-blk[elsewhere]", ["m1"])).toBeNull();
	});
});
