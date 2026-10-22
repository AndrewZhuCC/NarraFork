import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { decodeBlockPage } from "../block-protocol";
import { clearBlockRefs } from "../block-refs";
import { api } from "./index";

/**
 * Actions on a message element act on the whole block holding it, through the block-id
 * routes, once the message arrived in the block-keyed protocol (§5.5); a message seen only
 * from a v1 server keeps the index routes.
 */
describe("block-addressed actions", () => {
	const g = globalThis as typeof globalThis & { localStorage?: Storage; fetch: typeof fetch };
	const originalFetch = g.fetch;
	const originalLocalStorage = g.localStorage;
	let requests: Array<{ url: string; method: string; body: unknown }>;
	let reply: unknown;

	beforeEach(() => {
		clearBlockRefs();
		requests = [];
		reply = { ok: true };
		Object.defineProperty(g, "localStorage", {
			value: { getItem: () => null },
			configurable: true,
		});
		Object.defineProperty(g, "fetch", {
			configurable: true,
			value: async (url: unknown, init: RequestInit = {}) => {
				requests.push({
					url: String(url),
					method: init.method ?? "GET",
					body: typeof init.body === "string" ? JSON.parse(init.body) : init.body,
				});
				return Response.json(reply);
			},
		});
	});
	afterEach(() => {
		Object.defineProperty(g, "fetch", { value: originalFetch, configurable: true });
		if (originalLocalStorage === undefined) Reflect.deleteProperty(g, "localStorage");
		else
			Object.defineProperty(g, "localStorage", { value: originalLocalStorage, configurable: true });
	});

	const envelope = (id: string, role: string, blockIds: string[]) => ({
		id,
		narratorId: "n1",
		role,
		parentToolUseId: null,
		contentText: null,
		blockIds,
		parentMessageId: null,
	});
	const block = (id: string, messageId: string, kind: string, elements: unknown[]) => ({
		id,
		messageId,
		ordinal: 0,
		kind,
		subtype: null,
		phase: "done",
		revision: 1,
		wireId: kind === "tool" ? "call_1" : null,
		attemptId: null,
		elements,
		hasOriginal: false,
	});

	/** A page in the block protocol: an input, then a step of a reasoning run, a tool, a text. */
	function seeV2Page() {
		decodeBlockPage({
			protocol: 2,
			messages: [
				envelope("in1", "user", ["bIn"]),
				envelope("st1", "assistant", ["bR", "bT", "bX"]),
			],
			blocks: [
				block("bIn", "in1", "input", [{ type: "text", text: "go" }]),
				block("bR", "st1", "reasoning", [
					{ type: "reasoning", text: "a" },
					{ type: "reasoning", text: "b" },
				]),
				block("bT", "st1", "tool", [{ type: "tool_use", id: "call_1", name: "Bash", input: {} }]),
				block("bX", "st1", "output", [{ type: "text", text: "done" }]),
			],
		});
	}

	test("a v1 message keeps the index routes", async () => {
		await api.deleteMessageBlock("n1", "st1", 2);
		await api.rollbackToBlock("n1", "st1", 2, { skipRevert: true });
		expect(requests.map((request) => request.url)).toEqual([
			"/api/narrators/n1/messages/st1/blocks/2?skipRevert=1",
			"/api/narrators/n1/rollback/st1",
		]);
	});

	test("deleting an element deletes its whole block by id", async () => {
		seeV2Page();
		reply = {
			ok: true,
			deleted: 1,
			failed: 0,
			results: [{ blockId: "bR", messageId: "st1", messageDeleted: false }],
		};
		expect(await api.deleteMessageBlock("n1", "st1", 1)).toEqual({
			ok: true,
			messageDeleted: false,
		});
		expect(requests[0]).toEqual({
			url: "/api/narrators/n1/blocks",
			method: "DELETE",
			body: { blockIds: ["bR"], skipRevert: true },
		});
	});

	test("a selection of elements deletes each block once", async () => {
		seeV2Page();
		reply = { ok: true, deleted: 2, failed: 0, results: [] };
		await api.deleteMessageBlocks("n1", [
			{ messageId: "st1", blockIndex: 0 },
			{ messageId: "st1", blockIndex: 1 },
			{ messageId: "st1", blockIndex: 3 },
		]);
		expect(requests[0].body).toEqual({ blockIds: ["bR", "bX"], skipRevert: true });
	});

	test("rollback, its previews and the file-restore preview name the block", async () => {
		seeV2Page();
		await api.rollbackToBlock("n1", "st1", 3, { skipRevert: true });
		await api.getRollbackPreview("n1", "st1", 3);
		await api.blockDeletePreview("n1", "st1", 2);
		await api.previewRevertAction("n1", {
			action: "delete_tool_block",
			messageId: "st1",
			blockIndex: 2,
			idempotencyKey: "key-1",
		});
		expect(requests.map(({ url, body }) => [url, body])).toEqual([
			["/api/narrators/n1/blocks/rollback", { blockIds: ["bX"], skipRevert: true }],
			["/api/narrators/n1/blocks/rollback-preview?blockIds=bX", undefined],
			["/api/narrators/n1/blocks/bT/delete-preview", undefined],
			[
				"/api/narrators/n1/blocks/revert-action-preview",
				{ action: "delete_tool_block", blockIds: ["bT"], idempotencyKey: "key-1" },
			],
		]);
	});

	test("editing an input regenerates from its input block", async () => {
		seeV2Page();
		await api.editAndRegenerate("n1", "in1", "go again", { skipRevert: true });
		expect(requests[0].url).toEndWith("/narrators/n1/blocks/bIn/edit-and-regenerate");
	});
});
