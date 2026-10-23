import { describe, expect, test } from "bun:test";
import { messageBlocks, messageBody } from "@shared/narrator-blocks/message-blocks";
import type { TreeMessage } from "./api/types";
import { decodeBlockFrame, decodeBlockPage } from "./block-protocol";
import { NarratorWSManager } from "./narrator-ws-manager";

const envelope = (id: string, blockIds: string[], parentMessageId: string | null = null) => ({
	id,
	narratorId: "n1",
	role: "assistant",
	parentToolUseId: null,
	contentText: null,
	blockIds,
	parentMessageId,
});

const block = (id: string, messageId: string, elements: unknown[], kind = "output") => ({
	id,
	messageId,
	ordinal: 0,
	kind,
	subtype: null,
	phase: "done",
	revision: 1,
	wireId: null,
	attemptId: null,
	elements,
	hasOriginal: false,
});

describe("block protocol decoding", () => {
	test("frames that are not message-shaped pass through untouched", () => {
		const frame = { type: "status_changed", narratorId: "n1", status: "idle" };
		expect(decodeBlockFrame(frame)).toEqual([frame]);
	});

	test("each row holds its named blocks, a block holding several elements", () => {
		const decoded = decodeBlockPage({
			protocol: 2,
			messages: [envelope("m1", ["b1", "b2"])],
			blocks: [
				block(
					"b1",
					"m1",
					[{ type: "reasoning", text: "a" }, { type: "bash_display" }],
					"reasoning",
				),
				block("b2", "m1", [{ type: "text", text: "b" }]),
			],
			hasPrev: true,
		}) as unknown as { hasPrev: boolean; messages: TreeMessage[] };
		expect(decoded.hasPrev).toBe(true);
		expect(messageBody(decoded.messages[0])).toHaveLength(3);
		expect(messageBlocks(decoded.messages[0])).toEqual([
			expect.objectContaining({ id: "b1", kind: "reasoning", named: true, from: 0 }),
			expect.objectContaining({ id: "b2", kind: "output", named: true, from: 2 }),
		]);
		expect(decoded.messages[0]).not.toHaveProperty("contentJson");
	});

	test("a block opening reaches no listener", () => {
		expect(
			decodeBlockFrame({
				type: "block_opened",
				narratorId: "n1",
				blockId: "b1",
				kind: "output",
				elementId: "a:0",
				messageId: null,
			}),
		).toEqual([]);
	});

	test("a message frame decodes to the frame named by its cause", () => {
		const [frame] = decodeBlockFrame({
			type: "message_upserted",
			narratorId: "n1",
			cause: "user_message",
			messages: [envelope("m1", ["b1"])],
			blocks: [block("b1", "m1", [{ type: "text", text: "hi" }], "input")],
		});
		expect(frame).toMatchObject({ type: "user_message", narratorId: "n1", message: { id: "m1" } });
		expect(messageBody(frame.message as TreeMessage)).toEqual([{ type: "text", text: "hi" }]);
		expect(frame).not.toHaveProperty("cause");
	});
});

describe("the WS manager", () => {
	test("subscriptions name no protocol: v2 is the only one", () => {
		const manager = new NarratorWSManager();
		const sent: Array<Record<string, unknown>> = [];
		const internals = manager as unknown as {
			ws: { readyState: number; send: (payload: string) => void };
			_sendSubscribe: (subscription: unknown, narratorIds: string[]) => void;
		};
		internals.ws = {
			readyState: WebSocket.OPEN,
			send: (payload) => sent.push(JSON.parse(payload) as Record<string, unknown>),
		};
		const messages = manager.subscribe(["n1"], { kind: "messages" });
		const panel = manager.subscribe(["n1"], { kind: "panel" });
		internals._sendSubscribe(messages, ["n1"]);
		internals._sendSubscribe(panel, ["n1"]);
		const frames = sent.filter((frame) => frame.type === "subscribe");
		expect(frames.map((frame) => frame.kind)).toEqual(["messages", "panel"]);
		for (const frame of frames) expect(frame).not.toHaveProperty("protocol");
	});
});
