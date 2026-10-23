import { describe, expect, test } from "bun:test";
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
	test("a v1 page and v1 frames pass through untouched", () => {
		const page = { messages: [{ id: "m1", contentJson: [] }], hasPrev: false };
		expect(decodeBlockPage(page)).toBe(page);
		const frame = { type: "message_updated", narratorId: "n1", message: { id: "m1" } };
		expect(decodeBlockFrame(frame)).toEqual([frame]);
		const catchUp = { type: "catch_up", narratorId: "n1", topLevel: [], orphanChildren: [] };
		expect(decodeBlockFrame(catchUp)).toEqual([catchUp]);
	});

	test("a block holding several elements maps to their range of contentJson", () => {
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
		}) as unknown as {
			hasPrev: boolean;
			messages: Array<{ contentJson: unknown[]; blockRefs: Array<Record<string, unknown>> }>;
		};
		expect(decoded.hasPrev).toBe(true);
		expect(decoded.messages[0].contentJson).toHaveLength(3);
		expect(decoded.messages[0].blockRefs).toEqual([
			expect.objectContaining({ id: "b1", kind: "reasoning", from: 0, to: 2 }),
			expect.objectContaining({ id: "b2", kind: "output", from: 2, to: 3 }),
		]);
	});

	test("protocol bookkeeping frames reach no listener", () => {
		expect(
			decodeBlockFrame({ type: "message_protocol", narratorIds: ["n1"], protocol: 2 }),
		).toEqual([]);
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

	test("a message frame decodes to the v1 frame named by its cause", () => {
		const [frame] = decodeBlockFrame({
			type: "message_upserted",
			narratorId: "n1",
			cause: "user_message",
			messages: [envelope("m1", ["b1"])],
			blocks: [block("b1", "m1", [{ type: "text", text: "hi" }], "input")],
		});
		expect(frame).toMatchObject({
			type: "user_message",
			narratorId: "n1",
			message: { id: "m1", contentJson: [{ type: "text", text: "hi" }] },
		});
		expect(frame).not.toHaveProperty("cause");
	});
});

describe("the WS manager", () => {
	test("asks for the block protocol on message subscriptions only", () => {
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
		expect(frames.find((frame) => frame.kind === "messages")?.protocol).toBe(2);
		expect(frames.find((frame) => frame.kind === "panel")).not.toHaveProperty("protocol");
	});
});
