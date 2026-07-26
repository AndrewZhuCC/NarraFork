/**
 * Regression: the upstream `REQUEST_BODY_INVALID` / "Improperly formed request." rejection
 * observed after a transient EOF retry.
 *
 * Root cause chain (reproduced end-to-end below):
 *  1. A turn dies mid-stream (Kiro "event stream ended unexpectedly: unexpected EOF") after
 *     the model streamed reasoning but before any tool actually executed.
 *  2. `finalizeOrCleanupPartialMessage` keeps that partial (it has meaningful reasoning) and
 *     strips the unexecuted tool_use blocks → `keptPartial = true`.
 *  3. The retry path in narrator-session clears `currentText` because the partial was kept.
 *  4. The kept partial is an assistant message WITHOUT tool_uses, so `buildKiroHistory`
 *     yields NO trailingToolResults.
 *  5. Result: `currentMessage` has empty content, no toolResults and no images, while the
 *     orphan-pairing rule appends a synthetic assistant "OK" to history. Upstream rejects
 *     that body without saying which field was wrong.
 */

import { describe, expect, test } from "bun:test";
import { getToolMessage } from "../../i18n";
import type { KiroChatRequest, KiroToolResult } from "../../kiro-adapter";
import { buildKiroHistory, buildKiroRequest, normalizeKiroChatRequest } from "../kiro-protocol";
import type { ChatParams, DbMessage } from "../provider";

function msg(overrides: Partial<DbMessage> & Pick<DbMessage, "id" | "role">): DbMessage {
	return {
		contentJson: [],
		contentText: null,
		parentToolUseId: null,
		messageUuid: null,
		toolCalls: [],
		...overrides,
	};
}

/**
 * DB state right after the EOF turn: the final assistant message is the KEPT partial —
 * reasoning only, its unexecuted tool_use blocks already stripped.
 */
function messagesAfterKeptReasoningOnlyPartial(): DbMessage[] {
	return [
		msg({
			id: "u1",
			role: "user",
			contentText: "做个功能",
			contentJson: [{ type: "text", text: "做个功能" }],
		}),
		msg({
			id: "a1",
			role: "assistant",
			contentJson: [
				{ type: "reasoning", text: "先看一下现状" },
				{ type: "tool_use", id: "tu1", name: "Bash", input: { command: "ls" } },
			],
			toolCalls: [
				{
					toolUseId: "tu1",
					toolName: "Bash",
					inputJson: { command: "ls" },
					outputJson: "ok",
					status: "success",
				},
			] as DbMessage["toolCalls"],
		}),
		// The kept partial: reasoning survived, the unexecuted tool_use was removed.
		msg({ id: "a2", role: "assistant", contentJson: [{ type: "reasoning", text: "继续思考" }] }),
	];
}

function buildRequest(options: {
	content: string;
	history: unknown[];
	toolResults: unknown[];
	images?: ChatParams["images"];
}): KiroChatRequest {
	return normalizeKiroChatRequest(
		buildKiroRequest({
			conversationId: "conv-eof-retry",
			content: options.content,
			model: "claude-opus-5",
			cwd: "/tmp",
			history: options.history,
			tools: [{ toolSpecification: { name: "Bash", description: "d", inputSchema: { json: {} } } }],
			toolResults: options.toolResults,
			signal: new AbortController().signal,
			...(options.images ? { images: options.images } : {}),
		} satisfies ChatParams),
	);
}

describe("Kiro empty-currentMessage guard (upstream REQUEST_BODY_INVALID)", () => {
	test("a kept reasoning-only partial yields no trailing tool results", async () => {
		const { history, trailingToolResults, trailingUserText } = await buildKiroHistory(
			messagesAfterKeptReasoningOnlyPartial(),
			"claude-opus-5",
		);

		// This is step 4 of the chain: nothing carries the turn forward.
		expect(trailingToolResults).toHaveLength(0);
		expect(trailingUserText).toBeUndefined();
		// History ends on the kept assistant partial (reasoning, no toolUses).
		const last = history.at(-1) as { assistantResponseMessage?: { toolUses?: unknown[] } };
		expect(last.assistantResponseMessage).toBeDefined();
		expect(last.assistantResponseMessage?.toolUses ?? []).toHaveLength(0);
	});

	test("the production scenario no longer produces an empty current message", async () => {
		const { history, trailingToolResults } = await buildKiroHistory(
			messagesAfterKeptReasoningOnlyPartial(),
			"claude-opus-5",
		);

		// The retry path clears currentText because the partial was kept.
		const request = buildRequest({ content: "", history, toolResults: trailingToolResults });
		const current = request.conversationState.currentMessage.userInputMessage;
		const context = current.userInputMessageContext;

		// Guard injected a continuation instruction instead of shipping the rejected shape.
		expect(current.content.trim().length).toBeGreaterThan(0);
		expect(current.content).toBe(getToolMessage("resumeAfterTransientError"));
		// Tool specs still ride along, and no phantom results/images were invented.
		expect(context?.toolResults ?? []).toHaveLength(0);
		expect(current.images ?? []).toHaveLength(0);
		expect((context?.tools ?? []).length).toBeGreaterThan(0);
	});

	test("keeps user/assistant alternation valid after the injection", async () => {
		const { history, trailingToolResults } = await buildKiroHistory(
			[
				...messagesAfterKeptReasoningOnlyPartial(),
				// A trailing user message is dropped into currentMessage, leaving the
				// preceding assistant partial as the last history entry.
				msg({ id: "u2", role: "user", contentText: "", contentJson: [] }),
			],
			"claude-opus-5",
		);
		const request = buildRequest({ content: "", history, toolResults: trailingToolResults });
		const roles = (request.conversationState.history ?? []).map((m) =>
			"userInputMessage" in m ? "U" : "A",
		);

		// History must still end on an assistant turn so the injected user turn alternates.
		expect(roles.at(-1)).toBe("A");
		expect(roles.join("")).not.toContain("UU");
		expect(roles.join("")).not.toContain("AA");
		expect(request.conversationState.currentMessage.userInputMessage.content).toBe(
			getToolMessage("resumeAfterTransientError"),
		);
	});

	test("the same history is valid once text or images carry the turn", async () => {
		const { history } = await buildKiroHistory(
			messagesAfterKeptReasoningOnlyPartial(),
			"claude-opus-5",
		);

		// (a) real user/continuation text
		expect(
			buildRequest({ content: "继续", history, toolResults: [] }).conversationState.currentMessage
				.userInputMessage.content,
		).toBe("继续");

		// (b) images attached to an otherwise empty turn
		const withImages = buildRequest({
			content: "",
			history,
			toolResults: [],
			images: [{ format: "png", base64: "AAAA" }],
		});
		expect(withImages.conversationState.currentMessage.userInputMessage.images ?? []).toHaveLength(
			1,
		);
	});

	test("a genuine pure tool-result replay stays valid (results matched to a pending toolUse)", async () => {
		// Last assistant kept its executed tool_use, so its result rides in currentMessage —
		// this is the shape `isPureToolResultReplay` was written for.
		const { history, trailingToolResults } = await buildKiroHistory(
			[
				msg({
					id: "u1",
					role: "user",
					contentText: "跑一下",
					contentJson: [{ type: "text", text: "跑一下" }],
				}),
				msg({
					id: "a1",
					role: "assistant",
					contentJson: [{ type: "tool_use", id: "tu5", name: "Bash", input: { command: "ls" } }],
					toolCalls: [
						{
							toolUseId: "tu5",
							toolName: "Bash",
							inputJson: { command: "ls" },
							outputJson: "ok",
							status: "success",
						},
					] as DbMessage["toolCalls"],
				}),
			],
			"claude-opus-5",
		);

		expect(trailingToolResults).toHaveLength(1);
		const request = buildRequest({ content: "", history, toolResults: trailingToolResults });
		const current = request.conversationState.currentMessage.userInputMessage;
		expect(current.userInputMessageContext?.toolResults ?? []).toHaveLength(1);
		// The guard must NOT fire here: a pure tool-result replay is a legitimate empty-text
		// turn, and injecting text would change the packet shape the model expects.
		expect(current.content).toBe("");
	});

	test("covers the second route: tool results stripped by sanitization", async () => {
		const { history } = await buildKiroHistory(
			messagesAfterKeptReasoningOnlyPartial(),
			"claude-opus-5",
		);
		// Every toolUse in this history is already paired, so a trailing result with an
		// unknown id has no counterpart and `sanitizeKiroToolPairs` drops it. Passing tool
		// results is therefore NOT sufficient on its own — which is exactly why the guard
		// has to run on the final normalized shape rather than at build time.
		const orphaned: KiroToolResult[] = [
			{ toolUseId: "tu-unknown", content: [{ text: "done" }], status: "success" },
		];
		const request = buildRequest({ content: "", history, toolResults: orphaned });
		const current = request.conversationState.currentMessage.userInputMessage;

		expect(current.userInputMessageContext?.toolResults ?? []).toHaveLength(0);
		expect(current.content).toBe(getToolMessage("resumeAfterTransientError"));
	});

	test("does not touch a normal turn that already has user text", async () => {
		const { history } = await buildKiroHistory(
			messagesAfterKeptReasoningOnlyPartial(),
			"claude-opus-5",
		);
		const request = buildRequest({ content: "接着改一下登录逻辑", history, toolResults: [] });
		expect(request.conversationState.currentMessage.userInputMessage.content).toBe(
			"接着改一下登录逻辑",
		);
	});

	test("treats a whitespace-only current message as empty", async () => {
		const { history } = await buildKiroHistory(
			messagesAfterKeptReasoningOnlyPartial(),
			"claude-opus-5",
		);
		const request = buildRequest({ content: "   \n\t ", history, toolResults: [] });
		expect(request.conversationState.currentMessage.userInputMessage.content).toBe(
			getToolMessage("resumeAfterTransientError"),
		);
	});

	test("an image-only turn keeps its empty text (images carry the turn)", async () => {
		const { history } = await buildKiroHistory(
			messagesAfterKeptReasoningOnlyPartial(),
			"claude-opus-5",
		);
		const request = buildRequest({
			content: "",
			history,
			toolResults: [],
			images: [{ format: "png", base64: "AAAA" }],
		});
		const current = request.conversationState.currentMessage.userInputMessage;
		expect(current.images ?? []).toHaveLength(1);
		expect(current.content).toBe("");
	});

	test("guards an empty first turn with no history at all", () => {
		const request = buildRequest({ content: "", history: [], toolResults: [] });
		expect(request.conversationState.currentMessage.userInputMessage.content).toBe(
			getToolMessage("resumeAfterTransientError"),
		);
	});
});
