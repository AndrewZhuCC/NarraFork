import { afterAll, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { eq } from "drizzle-orm";
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
// Observe both builders without replacing their implementation or inputs.
const historyBuild = spyOn(agent, "buildHistory");
const { runCustomCompact } = await import("../narrator-compact");

const now = "2026-09-06T00:00:00.000Z";
const cipher = (round: number) => `opaque/+==\n密文-${round}`;
const item = (round: number) => ({
	type: "compaction",
	id: `cmp_${round}`,
	encrypted_content: cipher(round),
});
let requests: Array<{ input: Array<Record<string, unknown>> }> = [];
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
		return Response.json({ output: [item(requests.length)] });
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
		for (const excluded of [...pairs.flat(), "boundary", "tail"].filter(
			(value) => !pairs[round].includes(value),
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
				expect(text).not.toContain(`child-user-${previous}`);
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

test("remote request failure preserves the existing local-summary input", async () => {
	// Unit 1 only locks the old behavior; this unsafe downgrade belongs to unit 2,
	// and passing this assertion is not a claim that the fallback is safe.
	await seedNarrator();
	await message("checkpoint", 1, marker(7), true);
	await message("new", 2);
	await message("boundary", 3);
	globalThis.fetch = (async (_url: unknown, init?: RequestInit) => {
		requests.push(JSON.parse(String(init?.body)));
		return new Response("test failure", { status: 400 });
	}) as typeof fetch;
	expect(await runCustomCompact("n1", "en", "boundary", { mode: "blocking" })).toBe(true);
	expect(requests[0].input.filter((entry) => entry.type === "compaction")).toEqual([item(7)]);
	expect(localInputs).toEqual([["new"]]);
});

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
