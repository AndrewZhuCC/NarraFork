/**
 * await-agent-tree-injection.test.ts
 *
 * `attachAwaitAgentNarratorIds` is the transport half of the running-Await fix: it
 * writes the server-resolved child narrator id onto the `tool_use` block so a
 * reloaded page can offer "open session" while the wait is still in flight.
 *
 * Two properties are load-bearing and both fail SILENTLY if broken:
 *
 *  - It must NOT write `_metadata.subagentId`. `classifyAwait`
 *    (shared/pretext-layout/tool-detail.ts) renders a `subagent: …` row from that
 *    field, so writing it there would grow every running Await card — a layout
 *    change caused by a menu affordance. The test asserts `_metadata` is untouched.
 *  - It must return the SAME array/message references when it writes nothing, so an
 *    unaffected page pays no copy and no downstream identity check is disturbed.
 */

import { describe, expect, test } from "bun:test";
import {
	AWAIT_AGENT_RESOLVED_FIELD,
	AWAIT_AGENT_TITLE_FIELD,
	attachAwaitAgentNarratorIds,
} from "../await-agent-resolution";

function awaitMessage(toolUseId: string, extra: Record<string, unknown> = {}) {
	return {
		id: `m-${toolUseId}`,
		role: "assistant",
		contentJson: [
			{ type: "text", text: "waiting" },
			{
				type: "tool_use",
				id: toolUseId,
				name: "Await",
				inputJson: { type: "agent", id: "paper-extract" },
				status: "running",
				...extra,
			},
		],
	};
}

describe("attachAwaitAgentNarratorIds", () => {
	test("writes the resolved id onto the matching tool_use block", () => {
		const tree = [awaitMessage("tu-1")];
		const out = attachAwaitAgentNarratorIds(
			tree,
			new Map([["tu-1", { id: "sub-live", title: "Live Worker" }]]),
		);
		const block = out[0].contentJson[1];
		expect(block[AWAIT_AGENT_RESOLVED_FIELD]).toBe("sub-live");
		// The title rides along so the running card can headline the session name.
		expect(block[AWAIT_AGENT_TITLE_FIELD]).toBe("Live Worker");
		// The field name is part of the wire contract with the frontend readers.
		expect(AWAIT_AGENT_RESOLVED_FIELD).toBe("_awaitAgentNarratorId");
	});

	/**
	 * The height-neutrality guarantee. `metadata.subagentId` would render an extra
	 * row; this channel must remain invisible to the measure layer.
	 */
	test("never touches _metadata", () => {
		const tree = [awaitMessage("tu-1", { _metadata: { awaitType: "agent" } })];
		const out = attachAwaitAgentNarratorIds(
			tree,
			new Map([["tu-1", { id: "sub-live", title: null }]]),
		);
		const block = out[0].contentJson[1];
		expect(block._metadata).toEqual({ awaitType: "agent" });
		expect(block._metadata.subagentId).toBeUndefined();
		// A title-less resolution writes no title field, keeping legacy rows identical.
		expect(block[AWAIT_AGENT_TITLE_FIELD]).toBeUndefined();
	});

	test("leaves other blocks and other tool calls alone", () => {
		const tree = [awaitMessage("tu-1")];
		const out = attachAwaitAgentNarratorIds(
			tree,
			new Map([["tu-other", { id: "sub-live", title: null }]]),
		);
		// Nothing resolved for this page → identical reference, no copy.
		expect(out).toBe(tree);
		expect(out[0].contentJson[1][AWAIT_AGENT_RESOLVED_FIELD]).toBeUndefined();
	});

	test("returns the input untouched for an empty resolution map", () => {
		const tree = [awaitMessage("tu-1")];
		expect(attachAwaitAgentNarratorIds(tree, new Map())).toBe(tree);
	});

	test("descends into child messages", () => {
		const tree = [
			{
				id: "m-parent",
				role: "assistant",
				contentJson: [{ type: "tool_use", id: "tu-parent", name: "Agent" }],
				children: [awaitMessage("tu-child")],
			},
		];
		const out = attachAwaitAgentNarratorIds(
			tree,
			new Map([["tu-child", { id: "sub-nested", title: null }]]),
		);
		expect(out[0].children[0].contentJson[1][AWAIT_AGENT_RESOLVED_FIELD]).toBe("sub-nested");
	});

	test("tolerates messages without contentJson", () => {
		const tree = [{ id: "m-1", role: "user", contentText: "hi" }];
		expect(
			attachAwaitAgentNarratorIds(tree, new Map([["tu-1", { id: "sub-live", title: null }]])),
		).toBe(tree);
	});
});
