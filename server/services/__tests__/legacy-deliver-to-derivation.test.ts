/**
 * The legacy `deliverTo` derivation (a delegated row that predates registrations)
 * applies to exactly those rows, and never turns a message transport link into a
 * result destination.
 *
 * - A registered session without a run (a plugin standalone worker before its first
 *   run) delivers nowhere, even when its history carries a linked tool-use id.
 * - An old standalone worker's `standalone-<id>` transport link is not a tool call;
 *   deriving one made the finalizer reject the delivery and fail the whole run.
 * - An unregistered legacy delegation with a real linked tool call still derives.
 */
import { afterAll, afterEach, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { eq } from "drizzle-orm";
import { getTestDb } from "../../../tests/setup";
import {
	narratorMessageRefs,
	narratorMessages,
	narrators,
	narratorToolCalls,
} from "../../db/schema";
import type { ProviderAdapter } from "../../lib/agent/provider";

const { db, sqlite } = getTestDb();
const realDbModule = { ...(await import("../../db")) };
mock.module("../../db", () => ({ ...realDbModule, db, sqlite }));
const { registerExternalProviderResolver } = await import("../../lib/agent/provider");

const TEST_PROVIDER = "legacydeliverstub";
const stubAdapter = {
	formatTools: () => [],
	buildHistory: async () => ({ history: [], trailingToolResults: [] }),
	injectSystemPrompt: () => {},
	chat: async function* () {},
	formatToolResult: () => ({}),
} as unknown as ProviderAdapter;
const unregister = registerExternalProviderResolver({
	resolve: (requested) => (requested === TEST_PROVIDER ? stubAdapter : null),
	listReadiness: () => [],
});

const realWs = { ...(await import("../../websocket/narrator-ws")) };
mock.module("../../websocket/narrator-ws", () => ({ ...realWs, broadcastToNarrator: () => {} }));

const executor = await import("../narrator-executor");
const { startNarratorRun } = await import("../agent-runtime/run");
const { readSessionDeliverTo } = await import("../narrator-session");

const PARENT = "legacy-deliver-parent";
const WORKER = "legacy-deliver-worker";
const FOREIGN = "legacy-deliver-foreign-parent";
const now = () => new Date().toISOString();

afterAll(() => {
	unregister();
	mock.module("../../db", () => realDbModule);
	mock.module("../../websocket/narrator-ws", () => realWs);
	mock.restore();
});

function clear(): void {
	for (const table of [
		"narrator_buffered_messages",
		"narrator_message_refs",
		"narrator_tool_calls",
		"narrator_messages",
	])
		sqlite.run(`DELETE FROM "${table}"`);
	sqlite.run("DELETE FROM narrators WHERE id IN (?, ?, ?)", [WORKER, PARENT, FOREIGN]);
}

beforeEach(() => {
	clear();
	db.insert(narrators).values({ id: PARENT, createdAt: now(), updatedAt: now() }).run();
});
afterEach(clear);

/** A worker created through the delegation entry, with a linked user row in history. */
function seedWorker(registration: unknown, linkedToolUseId: string): void {
	db.insert(narrators)
		.values({
			id: WORKER,
			type: "subagent",
			variant: "subagent:general",
			parentNarratorId: PARENT,
			subagentOriginKind: "standalone",
			model: `${TEST_PROVIDER}:stub`,
			cwd: process.env.HOME,
			autoContinuationOverride: "off",
			runtimeRegistrationJson: registration as never,
			createdAt: now(),
			updatedAt: now(),
		})
		.run();
	db.insert(narratorMessages)
		.values({
			id: "legacy-deliver-linked",
			narratorId: WORKER,
			role: "user",
			contentJson: [{ type: "text", text: "earlier" }],
			contentText: "earlier",
			parentToolUseId: linkedToolUseId,
			createdAt: now(),
		})
		.run();
}

async function runOnce() {
	const execute = spyOn(executor, "executeAgentLoop").mockImplementation(async () => ({
		finalText: "done",
		hasError: false,
		shouldUpdateTitle: false,
	}));
	try {
		const handle = await startNarratorRun({
			narratorId: WORKER,
			locale: "en",
			text: "go",
			principal: null,
		});
		expect(handle.started).toBe(true);
		await handle.completion;
		return handle.terminal;
	} finally {
		execute.mockRestore();
	}
}

test("a plugin standalone worker's first run (registered, no run) delivers nowhere and succeeds", async () => {
	seedWorker({ version: 1, controller: { narratorId: PARENT, actions: ["stop"] } }, "toolu_old");
	expect(await readSessionDeliverTo(WORKER)).toBeUndefined();
	const result = await runOnce();
	expect(result.hasError).toBe(false);
	expect(result.output).toBe("done");
});

test("an old standalone worker's transport link is not a destination; the run succeeds", async () => {
	seedWorker(null, `standalone-${WORKER}`);
	expect(await readSessionDeliverTo(WORKER)).toBeUndefined();
	const result = await runOnce();
	expect(result.hasError).toBe(false);
});

test("a legacy worker whose origin is another caller's Agent row is refused, and no row is rewritten", async () => {
	const linked = "toolu_mismatch";
	db.insert(narrators).values({ id: FOREIGN, createdAt: now(), updatedAt: now() }).run();
	// The same tool-use id exists in both callers; only FOREIGN's row is the stored origin.
	for (const owner of [PARENT, FOREIGN]) {
		db.insert(narratorMessages)
			.values({
				id: `${owner}-agent-message`,
				narratorId: owner,
				role: "assistant",
				contentJson: [{ type: "tool_use", id: linked, name: "Agent", input: {} }],
				createdAt: now(),
			})
			.run();
		db.insert(narratorMessageRefs)
			.values({
				id: `${owner}-agent-ref`,
				narratorId: owner,
				messageId: `${owner}-agent-message`,
				seq: 1,
			})
			.run();
		db.insert(narratorToolCalls)
			.values({
				id: `${owner}-agent-row`,
				narratorId: owner,
				messageId: `${owner}-agent-message`,
				toolUseId: linked,
				toolName: "Agent",
				inputJson: {},
				outputJson: `${owner} ORIGINAL OUTPUT`,
				status: "success",
				executionIdentityVersion: 1,
				executionAttempt: 1,
				createdAt: now(),
			})
			.run();
	}
	seedWorker(null, linked);
	db.update(narrators)
		.set({ originToolCallId: `${FOREIGN}-agent-row` })
		.where(eq(narrators.id, WORKER))
		.run();
	expect(await readSessionDeliverTo(WORKER)).toEqual({ narratorId: PARENT, toolUseId: linked });
	const execute = spyOn(executor, "executeAgentLoop").mockImplementation(async () => ({
		finalText: "must not be delivered",
		hasError: false,
		shouldUpdateTitle: false,
	}));
	try {
		const handle = await startNarratorRun({
			narratorId: WORKER,
			locale: "en",
			text: "go",
			principal: null,
		});
		expect(handle.started).toBe(true);
		const failure = await handle.completion.then(
			() => null,
			(error: unknown) => error,
		);
		expect(failure).toBeInstanceOf(Error);
		const terminal = await handle.terminal;
		expect(terminal.hasError).toBe(true);
		expect(terminal.output).toBe(String(failure));
	} finally {
		execute.mockRestore();
	}
	const output = (id: string) =>
		db.select().from(narratorToolCalls).where(eq(narratorToolCalls.id, id)).get()?.outputJson;
	expect(output(`${FOREIGN}-agent-row`)).toBe(`${FOREIGN} ORIGINAL OUTPUT`);
	expect(output(`${PARENT}-agent-row`)).toBe(`${PARENT} ORIGINAL OUTPUT`);
});

test("an unregistered legacy delegation still derives its destination from a real link", async () => {
	seedWorker(null, "toolu_real_origin");
	expect(await readSessionDeliverTo(WORKER)).toEqual({
		narratorId: PARENT,
		toolUseId: "toolu_real_origin",
	});
});
