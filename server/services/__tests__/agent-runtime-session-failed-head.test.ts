import { afterAll, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { eq } from "drizzle-orm";
import { cleanDb, getTestDb } from "../../../tests/setup";
import { narratorBufferedMessages, narratorMessages, narrators } from "../../db/schema";

const { db, sqlite } = getTestDb();
const realDbModule = { ...(await import("../../db")) };
mock.module("../../db", () => ({ ...realDbModule, db, sqlite, activeDatabaseBackend: "sqlite" }));
const ws = { ...(await import("../../websocket/narrator-ws")) };
mock.module("../../websocket/narrator-ws", () => ({ ...ws, broadcastToNarrator: () => {} }));
mock.module("../agent-runtime/orchestrator", () => ({
	runAgentLoopUnlocked: async () => ({ started: true, allowInboxWake: false }),
}));
const { resumeNextBufferedMessage, sendMessage, startParentInboundContinuationIfPossible } =
	await import("../narrator-session");
const { enqueueBufferedMessage, getBufferedMessages } = await import("../narrator-buffer");
const { runtimeInbox, wakeInboxIfEligible } = await import("../agent-runtime/inbox");
const { specVfsService } = await import("../spec-vfs-service");
const { getExecutionOwner } = await import("../agent-runtime/ownership");
const { activeNarrators } = await import("../narrator-session-state");
const { narratorService } = await import("../narrator-service");
const time = "2026-09-09T00:00:00.000Z";
beforeEach(() => {
	cleanDb(sqlite);
	db.insert(narrators)
		.values({
			id: "recipient",
			title: "Recipient",
			status: "working",
			createdAt: time,
			updatedAt: time,
		})
		.run();
});
afterAll(() => {
	getExecutionOwner("recipient")?.release();
	mock.module("../../db", () => realDbModule);
	mock.restore();
	sqlite.close();
});
const active = { narratorId: "recipient", locale: "en", _replyInUserLanguage: false } as Parameters<
	typeof resumeNextBufferedMessage
>[0];
async function failedHead() {
	const failed = await enqueueBufferedMessage("recipient", "failed remains visible");
	db.update(narratorBufferedMessages)
		.set({ state: "failed", lastError: "previous preparation failed" })
		.where(eq(narratorBufferedMessages.id, failed.id))
		.run();
	return failed;
}

test("real session resume ignores failed UI head and executes the next claimed queued command", async () => {
	const failed = await failedHead();
	const queued = await enqueueBufferedMessage(
		"recipient",
		"/goal queued work",
		undefined,
		"/goal queued work",
	);
	const append = spyOn(specVfsService, "appendProtectedSpecTask").mockResolvedValue({
		added: false,
		written: {},
	} as never);
	try {
		await resumeNextBufferedMessage(active, "en");
		expect(append).toHaveBeenCalledWith("recipient", "queued work");
	} finally {
		append.mockRestore();
	}
	const rows = db
		.select()
		.from(narratorMessages)
		.where(eq(narratorMessages.narratorId, "recipient"))
		.all();
	expect(rows.filter((row) => row.role === "user").map((row) => row.contentText)).toEqual([
		"/goal queued work",
	]);
	expect(
		db
			.select()
			.from(narratorBufferedMessages)
			.where(eq(narratorBufferedMessages.id, queued.id))
			.get()?.state,
	).toBe("materialized");
	expect(getBufferedMessages("recipient").map((row) => ({ id: row.id, state: row.state }))).toEqual(
		[{ id: failed.id, state: "failed" }],
	);
	expect(getExecutionOwner("recipient")).toBeUndefined();
});

test("real ordinary feed reuses the claimed queued input and cannot enqueue it twice", async () => {
	const failed = await failedHead();
	const queued = await enqueueBufferedMessage("recipient", "ordinary queued text");
	const expectedId = db
		.select()
		.from(narratorBufferedMessages)
		.where(eq(narratorBufferedMessages.id, queued.id))
		.get()?.recipientMessageId;
	const session = {
		...active,
		alive: true,
		abortController: new AbortController(),
		cwd: ".",
	} as Parameters<typeof resumeNextBufferedMessage>[0];
	activeNarrators.set("recipient", session);
	// Stop strictly after the real user+ref+mailbox transaction, without invoking a provider.
	const status = spyOn(narratorService, "updateStatus")
		.mockRejectedValueOnce(new Error("post-commit dispatch fault"))
		.mockResolvedValue(undefined as never);
	try {
		await resumeNextBufferedMessage(session, "en");
	} finally {
		status.mockRestore();
		activeNarrators.delete("recipient");
	}
	const messages = db
		.select()
		.from(narratorMessages)
		.where(eq(narratorMessages.narratorId, "recipient"))
		.all();
	expect(messages.filter((row) => row.role === "user")).toMatchObject([
		{ id: expectedId, contentText: "ordinary queued text", createdBy: null },
	]);
	expect(db.select().from(narratorBufferedMessages).all()).toHaveLength(2);
	expect(getBufferedMessages("recipient").map((row) => row.id)).toEqual([failed.id]);
});

test("real session resume retains an unprojectable agent head before a user command", async () => {
	await failedHead();
	db.insert(narrators).values({ id: "sender", createdAt: time, updatedAt: time }).run();
	runtimeInbox.enqueue({
		kind: "agent_message",
		narratorId: "recipient",
		sourceNarratorId: "sender",
		sourceToolCallId: "tool",
		sourceAttempt: 1,
		sourceKey: "send",
		text: "earlier agent",
		projectedByteSize: 13,
	});
	const queued = await enqueueBufferedMessage("recipient", "/goal must wait");
	const append = spyOn(specVfsService, "appendProtectedSpecTask");
	try {
		await resumeNextBufferedMessage(active, "en");
	} finally {
		append.mockRestore();
	}
	expect(append).not.toHaveBeenCalled();
	expect(
		db
			.select()
			.from(narratorBufferedMessages)
			.where(eq(narratorBufferedMessages.id, queued.id))
			.get()?.state,
	).toBe("queued");
	expect(db.select().from(narratorMessages).all()).toHaveLength(0);
});

function notice(index = 0, text = "cancelled bash") {
	return runtimeInbox.enqueue({
		kind: "task_notice",
		narratorId: "recipient",
		noticeKind: "bash",
		sourceKey: `notice-${index}`,
		text,
		projectedByteSize: Buffer.byteLength(text),
		metadata: { producerKind: "bash", taskId: `bash-${index}`, eventKind: "cancelled" },
	});
}

async function withIdleSession(run: (session: typeof active) => Promise<void>) {
	const session = { ...active, alive: true, abortController: new AbortController(), cwd: "." };
	activeNarrators.set("recipient", session as typeof active);
	// The loop is stubbed; admission and persistence still use the isolated test database.
	const status = spyOn(narratorService, "updateStatus").mockResolvedValue(undefined as never);
	try {
		await run(session as typeof active);
	} finally {
		status.mockRestore();
		activeNarrators.delete("recipient");
	}
}

for (const position of ["front", "back"] as const) {
	test(`plan-mode user input queued at ${position} resumes after interrupt without leaving plan mode`, async () => {
		db.update(narrators)
			.set({ status: "idle", traits: ["standalone", "plan"], substatus: '["interrupted"]' })
			.where(eq(narrators.id, "recipient"))
			.run();
		const queued = await enqueueBufferedMessage(
			"recipient",
			"adjust the plan",
			undefined,
			undefined,
			undefined,
			undefined,
			undefined,
			position,
		);
		await withIdleSession(async () => {
			expect(await wakeInboxIfEligible("recipient", "en")).toBe(true);
			expect(await wakeInboxIfEligible("recipient", "en")).toBe(false);
		});
		expect(
			db
				.select()
				.from(narratorMessages)
				.all()
				.filter((row) => row.role === "user"),
		).toMatchObject([{ contentText: "adjust the plan" }]);
		expect(
			db
				.select()
				.from(narratorBufferedMessages)
				.where(eq(narratorBufferedMessages.id, queued.id))
				.get()?.state,
		).toBe("materialized");
		expect(db.select().from(narrators).where(eq(narrators.id, "recipient")).get()?.traits).toEqual([
			"standalone",
			"plan",
		]);
	});
}

test("plan mode still blocks unsolicited inbound continuation", async () => {
	db.update(narrators)
		.set({ status: "idle", traits: ["standalone", "plan"] })
		.where(eq(narrators.id, "recipient"))
		.run();
	notice();
	expect(await startParentInboundContinuationIfPossible("recipient", "en")).toEqual({
		started: false,
	});
	expect(db.select().from(narratorMessages).all()).toHaveLength(0);
	expect(db.select().from(narratorBufferedMessages).get()?.state).toBe("queued");
});

for (const delivery of ["send", "resume"] as const) {
	test(`${delivery} persists a cancelled bash notice before ordinary input exactly once`, async () => {
		notice();
		await withIdleSession(async (session) => {
			if (delivery === "send") {
				const message = await sendMessage("recipient", "ordinary input");
				expect(message.contentText).toBe("ordinary input");
			} else {
				await enqueueBufferedMessage("recipient", "ordinary input");
				await resumeNextBufferedMessage(session, "en");
			}
			await resumeNextBufferedMessage(session, "en");
		});
		const messages = db.select().from(narratorMessages).all();
		expect(messages).toHaveLength(2);
		expect(messages[0].contentText).toContain("bash-0");
		expect(messages[1].contentText).toBe("ordinary input");
		expect(
			db
				.select()
				.from(narratorBufferedMessages)
				.all()
				.map((row) => row.state),
		).toEqual(["materialized", "materialized"]);
	});
}

test("new send cannot report an older queued input as its own success", async () => {
	notice();
	await enqueueBufferedMessage("recipient", "old input");
	await withIdleSession(async (session) => {
		await expect(sendMessage("recipient", "new input")).rejects.toThrow("queued behind earlier");
		expect(db.select().from(narratorMessages).all()).toHaveLength(1);
		await resumeNextBufferedMessage(session, "en");
	});
	const messages = db.select().from(narratorMessages).all();
	expect(messages[1].contentText).toBe("old input");
	expect(getBufferedMessages("recipient").map((row) => row.text)).toEqual(["new input"]);
});

test("failed notification persistence leaves notice and input retryable in order", async () => {
	notice();
	await enqueueBufferedMessage("recipient", "ordinary input");
	sqlite.exec(`CREATE TRIGGER fail_notice BEFORE INSERT ON narrator_messages
		WHEN NEW.role != 'user' BEGIN SELECT RAISE(ABORT, 'notice persistence fault'); END`);
	try {
		await resumeNextBufferedMessage(active, "en");
		expect(db.select().from(narratorMessages).all()).toHaveLength(0);
		expect(
			db
				.select()
				.from(narratorBufferedMessages)
				.all()
				.map((row) => row.state),
		).toEqual(["queued", "queued"]);
	} finally {
		sqlite.exec("DROP TRIGGER fail_notice");
	}
	await withIdleSession((session) => resumeNextBufferedMessage(session, "en"));
	expect(db.select().from(narratorMessages).all()).toHaveLength(2);
});

test("notification byte budget stops before the following user without claiming the remainder", async () => {
	notice();
	db.insert(narrators).values({ id: "sender", createdAt: time, updatedAt: time }).run();
	runtimeInbox.enqueue({
		kind: "agent_message",
		narratorId: "recipient",
		sourceNarratorId: "sender",
		sourceToolCallId: "tool",
		sourceAttempt: 1,
		sourceKey: "large-projection",
		text: "x".repeat(256 * 1024),
		projectedByteSize: 256 * 1024,
	});
	await enqueueBufferedMessage("recipient", "ordinary input");
	await resumeNextBufferedMessage(active, "en");
	expect(db.select().from(narratorMessages).all()).toHaveLength(1);
	const remaining = db.select().from(narratorBufferedMessages).all().slice(1);
	expect(remaining.map((row) => row.state)).toEqual(["queued", "queued"]);
	expect(remaining[0].lastError).toBeNull();
});

for (const delivery of ["send", "resume"] as const) {
	test(`${delivery} processes at most 16 notification rows per admission`, async () => {
		for (let index = 0; index < 17; index++) notice(index);
		await withIdleSession(async (session) => {
			if (delivery === "send") {
				await expect(sendMessage("recipient", "ordinary input")).rejects.toThrow(
					"queued behind earlier",
				);
			} else {
				await enqueueBufferedMessage("recipient", "ordinary input");
				await resumeNextBufferedMessage(session, "en");
			}
			expect(db.select().from(narratorMessages).all()).toHaveLength(16);
			await resumeNextBufferedMessage(session, "en");
		});
		const messages = db.select().from(narratorMessages).all();
		expect(messages).toHaveLength(18);
		expect(messages[17].contentText).toBe("ordinary input");
	});
}
