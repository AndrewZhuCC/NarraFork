/**
 * A concluded review has to reach the MODEL, not just the transcript.
 *
 * `review-event-handler` writes the conclusion as a `role: "user"` row and starts a bare
 * `runAgentLoop(active, "")`: the row is supposed to already be in history. Builders used
 * to pop the last top-level user row as "the current turn", so with an empty prompt the
 * findings reached the model in neither place, silently.
 *
 * Histories are now complete: the trailing user row is part of the built history, so a
 * pass with no prompt of its own still sends it, once.
 */

import { describe, expect, test } from "bun:test";
import { AnthropicProvider } from "../../lib/agent/anthropic-provider";

const CONCLUSION_TEXT =
	"## Code Review: Changes Requested\n\n- 🚨 **[critical]** `server/db/schema.ts` — the index has no migration\n";

interface Row {
	id: string;
	narratorId?: string;
	role: string;
	contentJson: unknown;
	contentText: string;
	createdAt: string;
	parentToolUseId?: string | null;
}

function row(id: string, role: string, blocks: unknown[], text: string): Row {
	return {
		id,
		narratorId: "n1",
		role,
		contentJson: blocks,
		contentText: text,
		createdAt: `2026-08-18T00:00:0${id.slice(-1)}.000Z`,
	};
}

/** The row `review-event-handler` writes: model text + the reader's card, on one row. */
function conclusionRow(id = "m3"): Row {
	return row(
		id,
		"user",
		[
			{ type: "text", text: CONCLUSION_TEXT },
			{
				type: "review_feedback",
				verdict: "request_changes",
				findings: [{ severity: "critical", message: "the index has no migration" }],
				reviewChapterId: "rc1",
				text: CONCLUSION_TEXT,
			},
		],
		CONCLUSION_TEXT,
	);
}

const CONVERSATION = [
	row("m1", "user", [{ type: "text", text: "implement the feature" }], "implement the feature"),
	row("m2", "assistant", [{ type: "text", text: "done" }], "done"),
];

function provider(): AnthropicProvider {
	// Only `buildHistory` is exercised; it makes no network call, so a placeholder
	// config is enough and keeps the test off the wire.
	return new AnthropicProvider({
		apiKey: "test-key",
		baseUrl: "https://example.invalid",
	} as never);
}

describe("a trailing user row stays in the built history", () => {
	test("the conclusion is the last input of the history, sent once", async () => {
		const built = await provider().buildHistory(
			[...CONVERSATION, conclusionRow()] as never,
			"claude-sonnet-4-5",
			"n1",
		);
		const last = built.history.at(-1) as { role: string; content: unknown };
		expect(last.role).toBe("user");
		expect(JSON.stringify(last.content)).toContain("the index has no migration");
		// The model-facing text only, never the card's structured fields.
		expect(JSON.stringify(built.history).split("the index has no migration")).toHaveLength(2);
	});

	test("a legacy row without a text block falls back to its flat text", async () => {
		const legacy = row("m3", "user", [{ type: "review_feedback", verdict: "approve" }], "Approved");
		const built = await provider().buildHistory(
			[...CONVERSATION, legacy] as never,
			"claude-sonnet-4-5",
			"n1",
		);
		expect(JSON.stringify(built.history.at(-1))).toContain("Approved");
	});

	test("subagent rows are not part of the session's own history", async () => {
		const subagentRow = { ...conclusionRow(), parentToolUseId: "tu_1" };
		const built = await provider().buildHistory(
			[...CONVERSATION, subagentRow] as never,
			"claude-sonnet-4-5",
			"n1",
		);
		expect(JSON.stringify(built.history)).not.toContain("the index has no migration");
	});
});
