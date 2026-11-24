import { afterAll, afterEach, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { eq } from "drizzle-orm";
import { cleanDb } from "../../../tests/setup";
import { backgroundTasks, deviceTransferTasks, narrators, remoteDevices } from "../../db/schema";
import type { ToolContext } from "../../lib/agent/types";
import { delegationRegistration } from "../../lib/validators/runtime-registration";
import { getNarratorWorkerTestDb } from "./narrator-worker-fixture";

const fixture = getNarratorWorkerTestDb();
const { db, sqlite } = fixture;
await fixture.ready;
afterAll(() => fixture.close());
const realDbModule = { ...(await import("../../db")) };
mock.module("../../db", () => ({ db, sqlite }));
const { eventBus } = await import("../../lib/event-bus");
const { backgroundTaskService } = await import("../background-task-service");
const { narratorService } = await import("../narrator-service");
const { awaitAgentResultDetailed, waitForSubagentResult } = await import("../agent-communication");
const { activateSqliteRuntimePublication, getRuntimePublicationService, runtimePublication } =
	await import("../agent-runtime/publication");
activateSqliteRuntimePublication(db);
const { awaitTool, listRunningAwaits, updateAwaitTimeout } = await import(
	"../../lib/agent/tools/await"
);
const { setRunHold } = await import("../agent-runtime/ownership");
const markTakenOver = (id: string) => setRunHold(id, { released: false });
const clearTakenOver = (id: string) => setRunHold(id, undefined);
const parent = "await-receipt-parent";
const child = "await-receipt-child";
const taskId = "await-receipt-bash";
const run = {
	producerKind: "bash" as const,
	taskId,
	recipientId: parent,
	logicalRunId: "bash-run-1",
};
const agentRun = {
	producerKind: "agent" as const,
	taskId: child,
	recipientId: parent,
	logicalRunId: "agent-run-1",
};

beforeEach(async () => {
	const now = new Date().toISOString();
	await db.insert(narrators).values([
		{ id: parent, variant: "primary", type: "primary", createdAt: now, updatedAt: now },
		{
			id: child,
			variant: "subagent:general",
			type: "subagent",
			parentNarratorId: parent,
			policySourceNarratorId: parent,
			runtimeRegistrationJson: delegationRegistration(parent, "general"),
			title: "Receipt child",
			status: "idle",
			logicalRunId: agentRun.logicalRunId,
			createdAt: now,
			updatedAt: now,
		},
	]);
	await db.insert(backgroundTasks).values({
		id: taskId,
		type: "bash",
		parentNarratorId: parent,
		status: "completed",
		output: "final needle",
		logicalRunId: run.logicalRunId,
		startedAt: now,
		createdAt: now,
		updatedAt: now,
	});
});
afterEach(() => {
	mock.restore();
	clearTakenOver(child);
	cleanDb(sqlite);
});
afterAll(() => {
	mock.module("../../db", () => realDbModule);
});
const ctx = () => ({ narratorId: parent, signal: new AbortController().signal }) as ToolContext;

function waitForSubscription(eventType: Parameters<typeof eventBus.on>[0]): Promise<void> {
	const subscribed = Promise.withResolvers<void>();
	const subscribe = eventBus.on.bind(eventBus);
	spyOn(eventBus, "on").mockImplementation((type, handler) => {
		subscribe(type, handler);
		if (type === eventType) subscribed.resolve();
	});
	return subscribed.promise;
}

function commitAgentResult(source = agentRun, output = "current final") {
	db.transaction((tx) => {
		runtimePublication.reserve(source, tx);
		runtimePublication.commit(
			{
				...source,
				eventKind: "completed",
				resultRef: runtimePublication.persistResult(source, output, tx),
				summary: "completed",
			},
			tx,
		);
	});
}

test("Await rejects terminal targets before creating a waiter", async () => {
	const wait = spyOn(backgroundTaskService, "waitForCompletion");
	const text = spyOn(backgroundTaskService, "waitForText");
	const timer = spyOn(globalThis, "setTimeout");
	const result = await awaitTool.execute({ type: "terminal", id: taskId }, ctx());
	expect(result.isError).toBe(true);
	expect(result.output).toContain('unsupported wait type "terminal"');
	expect(wait).not.toHaveBeenCalled();
	expect(text).not.toHaveBeenCalled();
	expect(timer).not.toHaveBeenCalled();
});

test("Await reads a completed Bash by alias and consumes only its matching receipt", async () => {
	await db
		.update(backgroundTasks)
		.set({ alias: "local-bash" })
		.where(eq(backgroundTasks.id, taskId));
	const consume = spyOn(getRuntimePublicationService(), "consumeAwaitedTerminal");
	const result = await awaitTool.execute({ type: "bash", id: "local-bash" }, ctx());
	expect(result.isError).not.toBe(true);
	expect(result.output).toContain("final needle");
	expect(consume).toHaveBeenCalledWith(run);
});

test("Await rejects a Bash owned by another session", async () => {
	const wait = spyOn(backgroundTaskService, "waitForCompletion");
	const args = { type: "bash", id: taskId };
	expect(await awaitTool.execute(args, { ...ctx(), narratorId: child })).toMatchObject({
		isError: true,
	});
	expect(wait).not.toHaveBeenCalled();
});

test("an early output match returns without consuming Bash completion", async () => {
	await db.update(backgroundTasks).set({ status: "running" }).where(eq(backgroundTasks.id, taskId));
	spyOn(backgroundTaskService, "getOutputBuffer").mockReturnValue("needle");
	const consume = spyOn(getRuntimePublicationService(), "consumeAwaitedTerminal");
	const result = await awaitTool.execute(
		{ type: "bash", id: taskId, wait_for_text: "needle" },
		ctx(),
	);
	expect(result.metadata?.status).toBe("found");
	expect(consume).not.toHaveBeenCalled();
});
test("Await(agent) returns and consumes the child's terminal result", async () => {
	commitAgentResult(agentRun, "child final report");
	const consume = spyOn(getRuntimePublicationService(), "consumeAwaitedTerminal");
	const result = await awaitTool.execute({ type: "agent", id: child, timeout: 1000 }, ctx());
	expect(result.output).toContain("child final report");
	expect(result.metadata).toMatchObject({
		awaitType: "agent",
		subagentId: child,
		status: "completed",
	});
	expect(consume).toHaveBeenCalledWith(agentRun);
});

test("Await(transfer) returns the old paused result by transfer ID", async () => {
	const now = new Date().toISOString();
	await db.insert(remoteDevices).values({
		id: "await-test-device",
		name: "Test device",
		slug: "await-test-device",
		tokenHash: "unused",
		tokenPrefix: "fixture",
		createdBy: "fixture",
		createdAt: now,
		updatedAt: now,
	});
	await db.insert(deviceTransferTasks).values({
		id: "test-transfer",
		deviceId: "await-test-device",
		parentNarratorId: parent,
		direction: "upload",
		localPath: "/local/sample",
		remotePath: "/remote/sample",
		status: "paused",
		createdAt: now,
		updatedAt: now,
	});
	await db.insert(backgroundTasks).values({
		// The projection keeps the durable transfer ID as a separate selector.
		id: "transfer-projection",
		type: "transfer",
		transferTaskId: "test-transfer",
		parentNarratorId: parent,
		status: "paused",
		output: "waiting for device",
		startedAt: now,
		createdAt: now,
		updatedAt: now,
	});
	const result = await awaitTool.execute({ type: "transfer", id: "test-transfer" }, ctx());
	expect(result.output).toContain("waiting for device");
	expect(result.metadata).toMatchObject({ awaitType: "transfer", status: "paused" });
});

test("Await restores the live timeout and guidance only interrupts the wait", async () => {
	await db.update(backgroundTasks).set({ status: "running" }).where(eq(backgroundTasks.id, taskId));
	const entered = Promise.withResolvers<void>();
	const waitForCompletion = backgroundTaskService.waitForCompletion.bind(backgroundTaskService);
	spyOn(backgroundTaskService, "waitForCompletion").mockImplementation((...args) => {
		entered.resolve();
		return waitForCompletion(...args);
	});
	const guidance = new AbortController();
	const pending = awaitTool.execute(
		{ type: "bash", id: taskId, timeout: 60_000 },
		{
			...ctx(),
			currentToolUseId: "live-await",
			guidanceSignal: guidance.signal,
		},
	);
	try {
		await entered.promise;
		expect(updateAwaitTimeout("live-await", 120_000, child)).toBeNull();
		expect(updateAwaitTimeout("live-await", 120_000, parent)).toBe(120_000);
		expect(listRunningAwaits()).toMatchObject([{ toolUseId: "live-await", timeoutMs: 120_000 }]);
	} finally {
		guidance.abort();
	}
	expect((await pending).metadata).toMatchObject({ status: "aborted", endedForGuidance: true });
	expect(listRunningAwaits()).toEqual([]);
	expect((await backgroundTaskService.getById(taskId))?.status).toBe("running");
});
for (const method of ["completion", "text"] as const) {
	test(`bash ${method} fast path returns a same-row terminal receipt`, async () => {
		const result =
			method === "text"
				? await backgroundTaskService.waitForText(taskId, "needle", 100)
				: await backgroundTaskService.waitForCompletion(taskId, 100);
		expect(result).toMatchObject({
			terminalResultReceived: true,
			publicationRun: run,
			output: "final needle",
		});
	});
	test(`bash ${method} event keeps old result/run even after a continuation`, async () => {
		await db
			.update(backgroundTasks)
			.set({ status: "running", output: null })
			.where(eq(backgroundTasks.id, taskId));
		const row = await backgroundTaskService.getById(taskId);
		const initialRead = Promise.withResolvers<typeof row>();
		spyOn(backgroundTaskService, "getById").mockReturnValueOnce(initialRead.promise);
		const subscribed = waitForSubscription("background_task:completed");
		const waiting =
			method === "text"
				? backgroundTaskService.waitForText(taskId, "needle", 1000)
				: backgroundTaskService.waitForCompletion(taskId, 1000);
		// Worker reads may outlast a fixed sleep; publish only after the actual subscription.
		initialRead.resolve(row);
		await subscribed;
		await db
			.update(backgroundTasks)
			.set({ logicalRunId: "bash-run-2" })
			.where(eq(backgroundTasks.id, taskId));
		eventBus.emit({
			type: "background_task:completed",
			taskId,
			parentNarratorId: parent,
			taskType: "bash",
			output: "old needle",
			publicationRun: run,
		});
		expect(await waiting).toMatchObject({
			publicationRun: run,
			terminalResultReceived: true,
			output: "old needle",
		});
	});
	test(`bash ${method} subscribe recheck binds its terminal row`, async () => {
		const row = await backgroundTaskService.getById(taskId);
		if (!row) throw new Error("Missing seeded task");
		spyOn(backgroundTaskService, "getById")
			.mockResolvedValueOnce({ ...row, status: "running" })
			.mockResolvedValueOnce(row);
		const result =
			method === "text"
				? await backgroundTaskService.waitForText(taskId, "needle", 1000)
				: await backgroundTaskService.waitForCompletion(taskId, 1000);
		expect(result).toMatchObject({ publicationRun: run, terminalResultReceived: true });
	});
}

for (const cause of ["timeout", "parent"] as const) {
	test(`agent completed→restart fast path obeys ${cause} signal without leaking waiter`, async () => {
		const now = new Date().toISOString();
		await db.update(narrators).set({ isBackground: true }).where(eq(narrators.id, child));
		await db.insert(backgroundTasks).values({
			id: child,
			type: "agent",
			subagentNarratorId: child,
			parentNarratorId: parent,
			status: "completed",
			output: "OLD RESULT",
			logicalRunId: agentRun.logicalRunId,
			startedAt: now,
			createdAt: now,
			updatedAt: now,
		});
		const entered = Promise.withResolvers<void>();
		const realWait = backgroundTaskService.waitForCompletion.bind(backgroundTaskService);
		spyOn(backgroundTaskService, "waitForCompletion").mockImplementation(async (...args) => {
			await db
				.update(backgroundTasks)
				.set({ status: "running", logicalRunId: "restarted-run", output: null })
				.where(eq(backgroundTasks.id, child));
			entered.resolve();
			return realWait(...args);
		});
		const parentAbort = new AbortController(),
			deadline = new AbortController();
		const subscribe = spyOn(eventBus, "on");
		const unsubscribe = spyOn(eventBus, "off");
		const started = Date.now();
		const pending = awaitAgentResultDetailed({
			callerNarratorId: parent,
			id: child,
			timeoutMs: 1500,
			signal: parentAbort.signal,
			timeoutSignal: deadline.signal,
		});
		await entered.promise;
		(cause === "timeout" ? deadline : parentAbort).abort();
		const result = await pending;
		expect(result.status).toBe(cause === "timeout" ? "timeout" : "aborted");
		expect(Date.now() - started).toBeLessThan(500);
		expect(result.publicationRun).toBeUndefined();
		expect(result.terminalResultReceived).toBeUndefined();
		for (const [type, listener] of subscribe.mock.calls)
			expect(unsubscribe).toHaveBeenCalledWith(type, listener);
	});
}

test("running text match and aborted/deadline waits have no terminal receipt", async () => {
	await db.update(backgroundTasks).set({ status: "running" }).where(eq(backgroundTasks.id, taskId));
	spyOn(backgroundTaskService, "getOutputBuffer").mockReturnValue("needle");
	expect(
		(await backgroundTaskService.waitForText(taskId, "needle", 10)).terminalResultReceived,
	).toBeUndefined();
	expect((await backgroundTaskService.waitForCompletion(taskId, 1)).publicationRun).toBeUndefined();
	const controller = new AbortController();
	controller.abort();
	expect(
		(await backgroundTaskService.waitForCompletion(taskId, 10, controller.signal))
			.terminalResultReceived,
	).toBeUndefined();
});

test("foreground output read racing a continuation never guesses the later run", async () => {
	spyOn(getRuntimePublicationService(), "readAgentTerminalResult").mockImplementation(async () => {
		await db
			.update(narrators)
			.set({ logicalRunId: "agent-run-2", status: "working" })
			.where(eq(narrators.id, child));
		return { output: "old output", source: "terminal" as const };
	});
	const result = await waitForSubagentResult({ subagentId: child, recipientId: parent });
	expect(result.status).toBe("superseded");
	expect(result.output).not.toContain("old output");
	expect(result.publicationRun).toBeUndefined();
});

for (const event of ["completion", "status"] as const) {
	test(`foreground ${event} wakeup carries the completed continuation run`, async () => {
		await db
			.update(narrators)
			.set({ status: "working", logicalRunId: "agent-run-2" })
			.where(eq(narrators.id, child));
		const subscribed = waitForSubscription("narrator:status_changed");
		const waiting = waitForSubagentResult({
			subagentId: child,
			recipientId: parent,
			timeoutMs: 1000,
		});
		await subscribed;
		commitAgentResult({ ...agentRun, logicalRunId: "agent-run-2" });
		await db.update(narrators).set({ status: "idle" }).where(eq(narrators.id, child));
		if (event === "completion")
			eventBus.emit({
				type: "narrator:subagent_completed",
				narratorId: child,
				parentNarratorId: parent,
				toolUseId: "tool",
			});
		else eventBus.emit({ type: "narrator:status_changed", narratorId: child, status: "idle" });
		expect(await waiting).toMatchObject({
			terminalResultReceived: true,
			publicationRun: { ...agentRun, logicalRunId: "agent-run-2" },
		});
	});
}

for (const outcome of ["failed", "timeout", "cancelled"] as const) {
	test(`bash ${outcome} producer event binds the committed run`, async () => {
		db.transaction((tx) => runtimePublication.reserve(run, tx));
		await db
			.update(backgroundTasks)
			.set({ status: "running", output: null })
			.where(eq(backgroundTasks.id, taskId));
		const subscribed = waitForSubscription("background_task:completed");
		const waiting = backgroundTaskService.waitForCompletion(taskId, 1000);
		await subscribed;
		if (outcome === "failed") await backgroundTaskService.markFailed(taskId, "failure");
		else if (outcome === "timeout")
			await backgroundTaskService.markTimedOut(taskId, "execution limit");
		else await backgroundTaskService.markCancelled(taskId);
		expect(await waiting).toMatchObject({
			status: outcome === "timeout" ? "timed_out" : outcome,
			terminalResultReceived: true,
			publicationRun: run,
		});
	});
}

test("background agent fast result uses its terminal task row, not current run lookup", async () => {
	const now = new Date().toISOString();
	await db
		.update(narrators)
		.set({
			isBackground: true,
			backgroundStatus: "completed",
			backgroundResult: "narrator projection",
		})
		.where(eq(narrators.id, child));
	await db.insert(backgroundTasks).values({
		id: child,
		type: "agent",
		subagentNarratorId: child,
		parentNarratorId: parent,
		status: "completed",
		logicalRunId: agentRun.logicalRunId,
		output: "task final",
		startedAt: now,
		createdAt: now,
		updatedAt: now,
	});
	const result = await awaitAgentResultDetailed({
		callerNarratorId: parent,
		id: child,
		signal: new AbortController().signal,
	});
	expect(result).toMatchObject({
		output: "task final",
		publicationRun: agentRun,
		terminalResultReceived: true,
	});
});

test("foreground subscribe recheck binds the terminal continuation", async () => {
	commitAgentResult();
	const current = await narratorService.getById(child);
	spyOn(narratorService, "getById").mockResolvedValueOnce({ ...current, status: "working" });
	expect(
		await waitForSubagentResult({ subagentId: child, recipientId: parent, timeoutMs: 1000 }),
	).toMatchObject({ terminalResultReceived: true, publicationRun: agentRun });
});

test("interrupt during foreground result read wins without consumption", async () => {
	await db.update(narrators).set({ status: "working" }).where(eq(narrators.id, child));
	const controller = new AbortController();
	const read = spyOn(getRuntimePublicationService(), "readAgentTerminalResult").mockImplementation(
		async () => {
			controller.abort();
			return { output: "finished text", source: "terminal" as const };
		},
	);
	const subscribed = waitForSubscription("narrator:status_changed");
	const waiting = waitForSubagentResult({
		subagentId: child,
		recipientId: parent,
		signal: controller.signal,
		timeoutMs: 1000,
	});
	await subscribed;
	await db.update(narrators).set({ status: "idle" }).where(eq(narrators.id, child));
	eventBus.emit({ type: "narrator:status_changed", narratorId: child, status: "idle" });
	const result = await waiting;
	expect(read).toHaveBeenCalledTimes(1);
	expect(result.status).toBe("aborted");
	expect(result.publicationRun).toBeUndefined();
});

test("taken-over foreground result is not final", async () => {
	markTakenOver(child);
	const result = await waitForSubagentResult({ subagentId: child, recipientId: parent });
	expect(result.status).toBe("taken_over");
	expect(result.publicationRun).toBeUndefined();
});
