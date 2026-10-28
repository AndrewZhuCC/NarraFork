import { afterAll, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { eq, sql } from "drizzle-orm";
import { cleanDb, getTestDb } from "../../../tests/setup";
import { narratorMessageRefs, narratorMessages, narrators } from "../../db/schema";

// tests/preload isolates HOME and NARRAFORK_HOME before service imports.
// Business queries below use tests/setup's in-memory DB.
const { db, sqlite } = getTestDb();
const { ensureColumns } = await import("../../db/ensure-columns");
ensureColumns(sqlite);
const realDb = { ...(await import("../../db")) };
mock.module("../../db", () => ({ ...realDb, db, sqlite }));
const { settings } = await import("../../lib/settings");
const originalProviders = settings.openaiProviders;
const originalFetch = globalThis.fetch;
const { narratorService } = await import("../narrator-service");
const { narratorContext } = await import("../narrator-context");
const originalSummary = narratorContext.generateCompactSummary;
const agent = await import("../../lib/agent");
const { OpenAIProvider } = await import("../../lib/agent/openai-provider");
// Observe both builders without replacing their implementation or inputs.
const historyBuild = spyOn(agent, "buildHistory");
const compactRemote = spyOn(OpenAIProvider.prototype, "compactRemote");
const { runCustomCompact, retryFailedCompact, __setCompactWatchdogTimingsForTests } = await import(
	"../narrator-compact"
);

const now = "2026-09-06T00:00:00.000Z";
const cipher = (round: number) => `opaque/+==\n密文-${round}`;
const item = (round: number) => ({
	type: "compaction",
	id: `cmp_${round}`,
	encrypted_content: cipher(round),
});
let requests: Array<{
	input: Array<Record<string, unknown>>;
	model: string;
	reasoning: { effort: string };
}> = [];
let localInputs: string[][] = [];

async function seedNarrator(id = "n1", parentNarratorId?: string) {
	await db.insert(narrators).values({
		id,
		...(parentNarratorId
			? { type: "subagent" as const, variant: "subagent:general", parentNarratorId }
			: {}),
		model: "test-relay:gpt-5.3-codex",
		createdAt: now,
		updatedAt: now,
	});
}

async function message(
	id: string,
	seq: number,
	block?: unknown,
	isCompact = false,
	owner = "n1",
	role: "user" | "assistant" = "assistant",
	parentToolUseId: string | null = null,
) {
	await db.insert(narratorMessages).values({
		id,
		narratorId: owner,
		parentToolUseId,
		// Completed replies keep the bounded history assistant-ended, as in
		// automatic compaction (the provider intentionally pops a trailing user).
		role: block ? "system" : role,
		contentJson: [block ?? { type: "text", text: id }],
		contentText: block ? null : id,
		createdAt: now,
	});
	await db.insert(narratorMessageRefs).values({
		id: `ref-${id}`,
		narratorId: owner,
		messageId: id,
		seq,
		isCompact: isCompact ? 1 : 0,
	});
	// This fixture inserts refs directly, so keep the production next-seq allocator's
	// watermark aligned with the manually assigned sequence.
	await db
		.update(narrators)
		.set({ nextSeq: sql`max(${narrators.nextSeq}, ${seq + 1})` })
		.where(eq(narrators.id, owner));
}

function marker(round: number, status = "compacted") {
	return {
		type: "compact",
		status,
		summary: "Remote Codex compact completed.",
		remoteCompact: {
			provider: "codex",
			version: 1,
			items: [item(round)],
			model: "test-relay:gpt-5.3-codex",
			completedAt: now,
			source: "remote",
		},
	};
}

beforeEach(() => {
	cleanDb(sqlite);
	requests = [];
	localInputs = [];
	historyBuild.mockClear();
	compactRemote.mockClear();
	settings.openaiProviders = [
		{
			id: "test-relay",
			name: "Test relay",
			prefix: "test-relay",
			apiKey: "not-a-real-key",
			baseUrl: "https://compact.invalid/v1",
			defaultModel: "gpt-5.3-codex",
			apiMode: "codex",
			codexRemoteCompact: true,
		},
	];
	// Never delegate to native fetch: no real network or paid upstream is allowed.
	globalThis.fetch = (async (url: unknown, init?: RequestInit) => {
		expect(String(url)).toBe("https://compact.invalid/v1/responses");
		requests.push(JSON.parse(String(init?.body)));
		return Response.json({ status: "completed", output: [item(requests.length)] });
	}) as typeof fetch;
	narratorContext.generateCompactSummary = async (_id, _locale, messages) => {
		localInputs.push((messages ?? []).map((entry) => entry.id));
		return { summary: "local summary" };
	};
});

afterAll(() => {
	globalThis.fetch = originalFetch;
	settings.openaiProviders = originalProviders;
	narratorContext.generateCompactSummary = originalSummary;
	mock.module("../../db", () => realDb);
	mock.restore();
	sqlite.close();
});

for (const model of [null, "", "   ", "__default__", "test-relay:gpt-5.3-codex"]) {
	for (const effort of [null, "medium", "none"] as const) {
		test(`remote model ${JSON.stringify(model)} and effort ${effort} match effective settings`, async () => {
			await seedNarrator();
			await db
				.update(narrators)
				.set({ model, reasoningEffort: effort })
				.where(eq(narrators.id, "n1"));
			await message("new", 1);
			settings.agent.defaultModel = "test-relay:gpt-5.3-codex";
			settings.agent.defaultReasoningEffort = "high";
			expect(await runCustomCompact("n1", "en", undefined, { mode: "blocking" })).toBe(true);
			expect(requests).toHaveLength(1);
			expect(requests[0].model).toBe("gpt-5.3-codex");
			expect(requests[0].reasoning.effort).toBe(effort ?? "high");
			expect(localInputs).toEqual([]);
		});
	}
}

for (const kind of ["primary", "subagent", "stale-primary"] as const) {
	test(`remote compact follows ${kind} runtime model, effort and conversation`, async () => {
		const { activeNarrators } = await import("../narrator-session-state");
		const { getSettingsRevision } = await import("../../lib/settings");
		if (kind === "subagent") await seedNarrator("runtime-parent");
		await seedNarrator("n1", kind === "subagent" ? "runtime-parent" : undefined);
		await db
			.update(narrators)
			.set({ apiConversationId: "persisted-api-conversation" })
			.where(eq(narrators.id, "n1"));
		await message("new", 1);
		settings.agent.defaultModel = "test-relay:gpt-5.4";
		settings.agent.defaultReasoningEffort = "high";
		activeNarrators.set("n1", {
			narratorId: "n1",
			conversationId: "live-api-conversation",
			alive: true,
			model: kind === "stale-primary" ? "test-relay:gpt-5.3-codex" : "test-relay:gpt-5.4",
			provider: "openai",
			reasoningEffort: "low",
			_modelRef: "__default__",
			_reasoningEffortRef: "medium",
			_settingsRevision: getSettingsRevision() - (kind === "stale-primary" ? 1 : 0),
			_substatus: new Set(),
			events: new (await import("node:events")).EventEmitter(),
		} as import("../narrator-session-state").ActiveNarrator);
		try {
			expect(await runCustomCompact("n1", "en", undefined, { mode: "background" })).toBe(true);
			expect(requests[0].model).toBe("gpt-5.4");
			expect(requests[0].reasoning.effort).toBe(kind === "stale-primary" ? "medium" : "low");
			expect(compactRemote).toHaveBeenCalledTimes(1);
			expect(compactRemote.mock.calls[0][2]?.conversationId).toBe("live-api-conversation");
		} finally {
			activeNarrators.delete("n1");
		}
	});
}

test("idle remote compact reuses the persisted API conversation id", async () => {
	const { activeNarrators } = await import("../narrator-session-state");
	await seedNarrator();
	await db
		.update(narrators)
		.set({ apiConversationId: "persisted-api-conversation" })
		.where(eq(narrators.id, "n1"));
	await message("new", 1);
	expect(activeNarrators.has("n1")).toBe(false);
	expect(await runCustomCompact("n1", "en", undefined, { mode: "blocking" })).toBe(true);
	expect(compactRemote).toHaveBeenCalledTimes(1);
	expect(compactRemote.mock.calls[0][2]?.conversationId).toBe("persisted-api-conversation");
	expect(localInputs).toEqual([]);
});

test("second and third bounded compacts replay only the immediate encrypted predecessor", async () => {
	await seedNarrator();
	const pairs = ["first", "second", "third"].map((prefix) => [
		`${prefix}-user-original`,
		`${prefix}-assistant-reply`,
	]);
	for (const [round, [user, assistant]] of pairs.entries()) {
		await message(user, round * 2 + 1, undefined, false, "n1", "user");
		await message(assistant, round * 2 + 2);
	}
	await message("boundary", 7, undefined, false, "n1", "user");
	await message("tail", 8);
	const providers = settings.openaiProviders;
	for (const [round, boundary] of [pairs[1][0], pairs[2][0], "boundary"].entries()) {
		// First-time prompt setup may reload isolated settings from disk.
		settings.openaiProviders = providers;
		expect(
			await runCustomCompact("n1", "en", boundary, {
				mode: round === 1 ? "background" : "blocking",
			}),
		).toBe(true);
	}
	expect(requests).toHaveLength(3);
	for (let round = 0; round < 3; round++) {
		const input = requests[round].input;
		expect(input.filter((entry) => entry.type === "compaction")).toEqual(
			round === 0 ? [] : [item(round)],
		);
		const text = JSON.stringify(input);
		for (const included of pairs[round]) expect(text).toContain(included);
		for (const [user] of pairs.slice(0, round)) expect(text.split(user)).toHaveLength(2);
		for (const excluded of [...pairs.flat(), "boundary", "tail"].filter(
			(value) =>
				!pairs[round].includes(value) && !pairs.slice(0, round).some(([user]) => user === value),
		)) {
			expect(text).not.toContain(excluded);
		}
	}
	expect(localInputs).toEqual([]);
});

for (const bounded of [true, false]) {
	test(`subagent ${bounded ? "bounded" : "unbounded"} compact preserves three rounds without changing stored parents`, async () => {
		await seedNarrator();
		await seedNarrator("child", "n1");
		await seedNarrator("sibling", "n1");
		await message("parent-secret", 1);
		await message("sibling-secret", 1, undefined, false, "sibling", "assistant", "sibling-task");
		const providers = settings.openaiProviders;
		for (let round = 0; round < 3; round++) {
			await message(
				`child-user-${round}`,
				round * 10 + 1,
				undefined,
				false,
				"child",
				"user",
				"child-task",
			);
			await message(
				`child-reply-${round}`,
				round * 10 + 2,
				undefined,
				false,
				"child",
				"assistant",
				"child-task",
			);
			if (bounded) {
				await message(
					`boundary-${round}`,
					round * 10 + 3,
					undefined,
					false,
					"child",
					"user",
					"child-task",
				);
			}
			settings.openaiProviders = providers;
			const buildOffset = historyBuild.mock.calls.length;
			expect(
				await runCustomCompact("child", "en", bounded ? `boundary-${round}` : undefined, {
					mode: "blocking",
				}),
			).toBe(true);
			// Preview and actual remote input must both receive normalized copies.
			const builds = historyBuild.mock.calls.slice(buildOffset, buildOffset + 2);
			expect(builds).toHaveLength(2);
			for (const [messages] of builds) {
				expect(messages.every((entry) => entry.parentToolUseId === null)).toBe(true);
				expect(messages.map((entry) => entry.id)).toContain(`child-user-${round}`);
				expect(messages.map((entry) => entry.id)).toContain(`child-reply-${round}`);
			}
			expect(requests).toHaveLength(round + 1);
			const input = requests[round].input;
			expect(input.filter((entry) => entry.type === "compaction")).toEqual(
				round ? [item(round)] : [],
			);
			const text = JSON.stringify(input);
			expect(text).toContain(`child-user-${round}`);
			expect(text).toContain(`child-reply-${round}`);
			expect(text).not.toContain(`boundary-${round}`);
			if (bounded && round > 0) expect(text).toContain(`boundary-${round - 1}`);
			expect(text).not.toContain("parent-secret");
			expect(text).not.toContain("sibling-secret");
			for (let previous = 0; previous < round; previous++) {
				expect(text.split(`child-user-${previous}`)).toHaveLength(2);
				expect(text).not.toContain(`child-reply-${previous}`);
			}
		}
		expect(localInputs).toEqual([]);
		const rows = await db
			.select()
			.from(narratorMessages)
			.where(eq(narratorMessages.narratorId, "child"));
		for (const row of rows.filter((entry) => entry.role !== "system")) {
			expect(row.parentToolUseId).toBe("child-task");
		}
	});
}

test("primary compact still filters child messages even when referenced in its history", async () => {
	await seedNarrator();
	await seedNarrator("child", "n1");
	await message("primary-user", 1, undefined, false, "n1", "user");
	await message("child-secret", 1, undefined, false, "child", "assistant", "child-task");
	await db
		.insert(narratorMessageRefs)
		.values({ id: "shared-child-ref", narratorId: "n1", messageId: "child-secret", seq: 2 });
	await message("primary-reply", 3);
	expect(await runCustomCompact("n1", "en", undefined, { mode: "blocking" })).toBe(true);
	expect(requests).toHaveLength(1);
	expect(JSON.stringify(requests[0].input)).toContain("primary-user");
	expect(JSON.stringify(requests[0].input)).toContain("primary-reply");
	expect(JSON.stringify(requests[0].input)).not.toContain("child-secret");
});

test("legacy version 1 checkpoint uses current refs and excludes failed/in-progress markers", async () => {
	await seedNarrator();
	await seedNarrator("other");
	await message("other-checkpoint", 5, marker(99), true, "other");
	await message("old-checkpoint", 1, marker(6), true);
	await message("checkpoint", 2, marker(7), true);
	await message("failed", 3, marker(8, "failed"));
	await message("in-progress", 4, marker(9, "compacting"));
	await message("new", 5);
	await message("boundary", 6);
	await message("tail", 7);
	expect((await narratorService.getMessagesBefore("n1", "boundary")).map((m) => m.id)).toEqual([
		"new",
	]);
	expect(await runCustomCompact("n1", "en", "boundary", { mode: "blocking" })).toBe(true);
	expect(requests[0].input.filter((entry) => entry.type === "compaction")).toEqual([item(7)]);
	expect(JSON.stringify(requests[0].input)).toContain("new");
	expect(JSON.stringify(requests[0].input)).not.toContain("tail");
	await expect(narratorService.getMessagesBefore("other", "boundary")).rejects.toThrow();
});

test("unbounded manual compact includes its predecessor exactly once", async () => {
	await seedNarrator();
	await message("checkpoint", 1, marker(7), true);
	await message("new", 2);
	expect(await runCustomCompact("n1", "en", undefined, { mode: "blocking" })).toBe(true);
	expect(requests[0].input.filter((entry) => entry.type === "compaction")).toEqual([item(7)]);
	expect(compactRemote.mock.calls[0][2]?.conversationId).toBe("n1");
});

for (const summary of ["local summary", ""]) {
	test(`nearest ${summary || "cleared"} boundary blocks older remote checkpoints`, async () => {
		await seedNarrator();
		await message("old-remote", 1, marker(7), true);
		await message("local-boundary", 2, { type: "compact", status: "compacted", summary }, true);
		await message("new", 3);
		await message("boundary", 4);
		expect(await runCustomCompact("n1", "en", "boundary", { mode: "blocking" })).toBe(true);
		expect(requests[0].input.filter((entry) => entry.type === "compaction")).toEqual([]);
		expect(JSON.stringify(requests[0].input)).toContain("new");
	});
}

test("predecessor without new messages does not request or persist another compact", async () => {
	await seedNarrator();
	await message("checkpoint", 1, marker(7), true);
	await message("boundary", 2, undefined, false, "n1", "user");
	const before = await db.select().from(narratorMessageRefs);
	expect(await runCustomCompact("n1", "en", "boundary", { mode: "background" })).toBe(false);
	expect(requests).toEqual([]);
	expect(localInputs).toEqual([]);
	expect(await db.select().from(narratorMessageRefs)).toEqual(before);
	expect((await db.select().from(narratorMessages)).map((entry) => entry.id).sort()).toEqual([
		"boundary",
		"checkpoint",
	]);
});

for (const bounded of [true, false]) {
	for (const failure of [
		"http",
		"network",
		"cancel",
		"timeout",
		"http-background",
		"sse-added",
		"sse-failed",
		"sse-double",
	] as const) {
		if (!bounded && failure === "http-background") continue;
		test(`${bounded ? "primary bounded" : "subagent unbounded"} ${failure} preserves checkpoint and can retry`, async () => {
			await seedNarrator();
			const owner = bounded ? "n1" : "child";
			if (!bounded) await seedNarrator(owner, "n1");
			const parent = bounded ? null : "child-task";
			await message(
				"checkpoint",
				1,
				{
					...marker(7),
					remoteCompact: {
						...marker(7).remoteCompact,
						retainedUsers: [
							{
								id: "old-user",
								narratorId: owner,
								contentJson: [{ type: "text", text: "retained requirement" }],
							},
						],
					},
				},
				true,
				owner,
			);
			await message("new-user", 2, undefined, false, owner, "user", parent);
			await message("new-reply", 3, undefined, false, owner, "assistant", parent);
			if (bounded) {
				await message("boundary", 4, undefined, false, owner, "user", parent);
				await message("tail", 5, undefined, false, owner, "assistant", parent);
			}
			await db
				.update(narrators)
				.set({ contextSummary: "old summary" })
				.where(eq(narrators.id, owner));
			const beforeRows = await db.select().from(narratorMessages);
			const beforeRefs = await db.select().from(narratorMessageRefs);
			const successFetch = globalThis.fetch;
			const controller = new AbortController();
			globalThis.fetch = (async (_url: unknown, init?: RequestInit) => {
				requests.push(JSON.parse(String(init?.body)));
				if (failure === "http" || failure === "http-background")
					return new Response("test failure", { status: 400 });
				if (failure === "network") throw new TypeError("network failure");
				if (failure.startsWith("sse-")) {
					const event = (type: string, fields: Record<string, unknown> = {}) =>
						`data: ${JSON.stringify({ type, ...fields })}\n\n`;
					const done = event("response.output_item.done", { item: item(1) });
					return new Response(
						(failure === "sse-added"
							? event("response.output_item.added", { item: item(1) })
							: done) +
							(failure === "sse-double" ? done : "") +
							event(failure === "sse-failed" ? "response.failed" : "response.completed", {
								response: { output: [item(1)] },
							}),
						{ headers: { "content-type": "text/event-stream" } },
					);
				}
				return new Promise<Response>((_resolve, reject) => {
					init?.signal?.addEventListener(
						"abort",
						() => reject(new DOMException("Aborted", "AbortError")),
						{ once: true },
					);
					if (failure === "cancel") controller.abort();
				});
			}) as typeof fetch;
			if (failure === "timeout")
				__setCompactWatchdogTimingsForTests({ stallMs: 30, maxTotalMs: 1000 });
			try {
				const run = runCustomCompact(owner, "en", bounded ? "boundary" : undefined, {
					mode: failure === "http-background" ? "background" : "blocking",
					signal: controller.signal,
				});
				await expect(run).rejects.toThrow(
					failure === "cancel"
						? "Aborted"
						: failure === "timeout"
							? "progress"
							: failure === "network"
								? "Network request failed"
								: failure.startsWith("sse-")
									? "Codex remote compact"
									: "test failure",
				);
			} finally {
				__setCompactWatchdogTimingsForTests(null);
			}
			// Assert the database, not just the return value: failed markers are not boundaries.
			expect(localInputs).toEqual([]);
			const rows = await db.select().from(narratorMessages);
			const refs = await db.select().from(narratorMessageRefs);
			for (const row of beforeRows) expect(rows.find((entry) => entry.id === row.id)).toEqual(row);
			for (const ref of beforeRefs) {
				const after = refs.find((entry) => entry.id === ref.id);
				if (!after) throw new Error(`Missing original ref: ${ref.id}`);
				// Inserting a bounded marker legitimately shifts tail seq numbers.
				expect(after).toEqual({ ...ref, seq: after?.seq });
				if (!["boundary", "tail"].includes(ref.messageId)) expect(after?.seq).toBe(ref.seq);
			}
			expect(refs.filter((ref) => ref.isCompact).map((ref) => ref.messageId)).toEqual([
				"checkpoint",
			]);
			const state = await db.query.narrators.findFirst({ where: eq(narrators.id, owner) });
			expect(state?.contextSummary).toBe("old summary");
			const history = await narratorService.getModelHistorySinceLastCompact(owner);
			const built = await agent.buildHistory(
				history.map((entry) => ({ ...entry, parentToolUseId: null })),
				"test-relay:gpt-5.3-codex",
				"openai",
				owner,
			);
			expect(
				built.history.filter((entry) => (entry as Record<string, unknown>).type === "compaction"),
			).toEqual([item(7)]);
			expect(JSON.stringify(built.history)).toContain("new-reply");
			const failed = rows.find((row) => !beforeRows.some((old) => old.id === row.id));
			if (failure === "cancel") expect(failed).toBeUndefined();
			else {
				expect(Array.isArray(failed?.contentJson) ? failed.contentJson[0] : null).toMatchObject({
					status: "failed",
				});
				if (failure === "timeout")
					expect(JSON.stringify(failed?.contentJson)).toContain("progress");
			}
			globalThis.fetch = successFetch;
			if (failed) {
				expect((await narratorService.getCompactSummary(owner, failed.id)).canRetry).toBe(true);
				const retry = await retryFailedCompact(owner, "en", failed.id);
				expect(await retry.promise).toBe(true);
			} else
				expect(
					await runCustomCompact(owner, "en", bounded ? "boundary" : undefined, {
						mode: "blocking",
					}),
				).toBe(true);
			const retriedHistory = await narratorService.getModelHistorySinceLastCompact(owner);
			expect(retriedHistory[0].contentJson).toEqual(
				expect.arrayContaining([
					expect.objectContaining({
						status: "compacted",
						remoteCompact: expect.objectContaining({ items: [item(2)] }),
					}),
				]),
			);
			expect(requests).toHaveLength(2);
			for (const request of requests) {
				expect(request.input.filter((entry) => entry.type === "compaction")).toEqual([item(7)]);
				const text = JSON.stringify(request.input);
				expect(text.split("retained requirement")).toHaveLength(2);
				expect(text).toContain("new-user");
				expect(text).toContain("new-reply");
				expect(text).not.toContain("boundary");
				expect(text).not.toContain("tail");
			}
			expect(localInputs).toEqual([]);
		});
	}
}

for (const behavior of ["advancing", "stalled", "ceiling"] as const) {
	test(`remote body ${behavior} uses the existing compact watchdog`, async () => {
		await seedNarrator();
		await message("checkpoint", 1, marker(7), true);
		await message("new", 2);
		let interval: ReturnType<typeof setInterval> | undefined;
		let finish: ReturnType<typeof setTimeout> | undefined;
		let cancelled = false;
		let chunks = 0;
		const providers = settings.openaiProviders;
		globalThis.fetch = (async () =>
			new Response(
				new ReadableStream<Uint8Array>({
					start(controller) {
						const encode = (text: string) => new TextEncoder().encode(text);
						controller.enqueue(encode(": initial activity\n\n"));
						if (behavior !== "stalled")
							interval = setInterval(() => {
								chunks++;
								controller.enqueue(encode(": still processing\n\n"));
							}, 60);
						if (behavior === "advancing")
							finish = setTimeout(() => {
								controller.enqueue(
									encode(
										`data: ${JSON.stringify({ type: "response.output_item.done", item: item(8) })}\n\n` +
											`data: ${JSON.stringify({ type: "response.completed", response: { output: [item(8)] } })}\n\n`,
									),
								);
							}, 750);
					},
					cancel() {
						cancelled = true;
						if (interval) clearInterval(interval);
						if (finish) clearTimeout(finish);
					},
				}),
				{ headers: { "content-type": "text/event-stream" } },
			)) as unknown as typeof fetch;
		__setCompactWatchdogTimingsForTests({
			stallMs: 300,
			maxTotalMs: behavior === "ceiling" ? 700 : 2000,
		});
		try {
			settings.openaiProviders = providers;
			const run = runCustomCompact("n1", "en", undefined, { mode: "background" });
			if (behavior === "advancing") {
				expect(await run).toBe(true);
				expect(chunks).toBeGreaterThan(3);
				const history = await narratorService.getModelHistorySinceLastCompact("n1");
				expect(history[0].contentJson).toEqual(
					expect.arrayContaining([
						expect.objectContaining({
							remoteCompact: expect.objectContaining({ items: [item(8)] }),
						}),
					]),
				);
			} else {
				await expect(run).rejects.toThrow(behavior === "stalled" ? "no progress" : "ceiling");
				const refs = await db.select().from(narratorMessageRefs);
				expect(refs.filter((ref) => ref.isCompact).map((ref) => ref.messageId)).toEqual([
					"checkpoint",
				]);
			}
			expect(localInputs).toEqual([]);
			expect(cancelled).toBe(true);
		} finally {
			__setCompactWatchdogTimingsForTests(null);
			if (interval) clearInterval(interval);
			if (finish) clearTimeout(finish);
		}
	}, 4000);
}

for (const boundary of ["first", "local", "clear"] as const) {
	for (const bounded of [true, false]) {
		test(`${boundary} ${bounded ? "bounded" : "unbounded"} HTTP failure safely falls back`, async () => {
			await seedNarrator();
			if (boundary !== "first") {
				await message("old-remote", 1, marker(7), true);
				await message(
					"local-boundary",
					2,
					{
						type: "compact",
						status: "compacted",
						summary: boundary === "local" ? "local summary" : "",
					},
					true,
				);
			}
			await message("new", 3);
			if (bounded) await message("boundary", 4);
			globalThis.fetch = (async (_url: unknown, init?: RequestInit) => {
				requests.push(JSON.parse(String(init?.body)));
				return new Response("test failure", { status: 400 });
			}) as typeof fetch;
			expect(
				await runCustomCompact("n1", "en", bounded ? "boundary" : undefined, { mode: "blocking" }),
			).toBe(true);
			expect(requests[0].input.filter((entry) => entry.type === "compaction")).toEqual([]);
			expect(localInputs).toEqual([["new"]]);
		});
	}
}

for (const cancel of [true, false]) {
	test(`first compact ${cancel ? "cancellation" : "timeout"} never calls local summary`, async () => {
		await seedNarrator();
		await message("new", 1);
		const controller = new AbortController();
		globalThis.fetch = (async (_url: unknown, init?: RequestInit) => {
			return new Promise<Response>((_resolve, reject) => {
				init?.signal?.addEventListener("abort", () => reject(new Error("transport closed")), {
					once: true,
				});
				if (cancel) controller.abort();
			});
		}) as typeof fetch;
		if (!cancel) __setCompactWatchdogTimingsForTests({ stallMs: 30, maxTotalMs: 1000 });
		try {
			await expect(
				runCustomCompact("n1", "en", undefined, { mode: "blocking", signal: controller.signal }),
			).rejects.toThrow(cancel ? "Aborted" : "progress");
		} finally {
			__setCompactWatchdogTimingsForTests(null);
		}
		expect(localInputs).toEqual([]);
		expect((await db.select().from(narratorMessageRefs)).filter((ref) => ref.isCompact)).toEqual(
			[],
		);
		const history = await narratorService.getModelHistorySinceLastCompact("n1");
		expect(history.map((row) => row.id)).toContain("new");
		// Failure status may append a system reminder, but not the failed marker.
		expect(
			history.some(
				(row) =>
					Array.isArray(row.contentJson) &&
					row.contentJson.some((block) => block.type === "compact"),
			),
		).toBe(false);
	});
}

for (const bounded of [true, false]) {
	for (const checkpoint of [true, false]) {
		test(`unavailable chat provider: bounded=${bounded}, checkpoint=${checkpoint}`, async () => {
			await seedNarrator();
			await db
				.update(narrators)
				.set({ model: "missing-provider:model" })
				.where(eq(narrators.id, "n1"));
			if (checkpoint) await message("checkpoint", 1, marker(7), true);
			await message("new", 2);
			if (bounded) await message("boundary", 3);
			const run = runCustomCompact("n1", "en", bounded ? "boundary" : undefined, {
				mode: "blocking",
			});
			if (checkpoint) {
				await expect(run).rejects.toThrow("not configured");
				expect(localInputs).toEqual([]);
				const refs = await db.select().from(narratorMessageRefs);
				expect(refs.filter((ref) => ref.isCompact).map((ref) => ref.messageId)).toEqual([
					"checkpoint",
				]);
			} else {
				expect(await run).toBe(true);
				expect(localInputs).toEqual([["new"]]);
			}
			expect(requests).toEqual([]);
		});
	}
}

for (const mode of ["disabled", "non-codex"] as const) {
	test(`${mode} compact keeps the existing local-summary input`, async () => {
		await seedNarrator();
		await message("checkpoint", 1, marker(7), true);
		await message("new", 2);
		await message("boundary", 3);
		const provider = settings.openaiProviders?.[0];
		if (!provider) throw new Error("Test provider missing");
		if (mode === "disabled") provider.codexRemoteCompact = false;
		else provider.apiMode = "responses";
		expect(await runCustomCompact("n1", "en", "boundary", { mode: "blocking" })).toBe(true);
		expect(requests).toEqual([]);
		expect(localInputs).toEqual([["new"]]);
	});
}

test("remote compact replays dynamically loaded tool declarations with their frozen schema", async () => {
	await seedNarrator();
	await db
		.update(narrators)
		.set({ model: "test-relay:gpt-6-luna", enabledTools: ["GenUI"] })
		.where(eq(narrators.id, "n1"));
	await message("load-genui", 1, {
		type: "tool_loaded",
		toolName: "gen_ui",
		toolNames: ["GenUI"],
		text: "GenUI loaded",
		definitionSnapshots: [
			{
				name: "GenUI",
				description: "frozen GenUI definition",
				parameters: { type: "object", properties: { action: { type: "string" } } },
			},
		],
	});
	await db
		.update(narratorMessages)
		.set({ role: "user" })
		.where(eq(narratorMessages.id, "load-genui"));
	await message("after-load", 2);
	expect(await runCustomCompact("n1", "en", undefined, { mode: "blocking" })).toBe(true);
	expect(requests).toHaveLength(1);
	expect(requests[0].input.filter((entry) => entry.type === "additional_tools")).toEqual([
		{
			type: "additional_tools",
			role: "developer",
			tools: [
				{
					type: "function",
					name: "GenUI",
					description: "frozen GenUI definition",
					parameters: { type: "object", properties: { action: { type: "string" } } },
					strict: false,
				},
			],
		},
	]);
	expect(localInputs).toEqual([]);
});
