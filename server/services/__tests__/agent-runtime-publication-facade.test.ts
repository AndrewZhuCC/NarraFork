/**
 * The PostgreSQL-mode publication facade (`createUnwiredPublicationFacade`): on the PG
 * backend the module-level `runtimePublication` is this fail-closed stand-in, so
 * importing `publication.ts` and registering producers must survive PG startup while
 * every database operation throws a precise error instead of touching the fail-closed
 * SQLite proxy.
 *
 * This suite runs on SQLite: it constructs the PG facade directly and verifies that
 * the module-level SQLite facade waits for explicit startup activation.
 */
import { afterAll, expect, test } from "bun:test";
import { getTestDb } from "../../../tests/setup";
import { narrators } from "../../db/schema";
import {
	activateSqliteRuntimePublication,
	createRuntimePublicationService,
	createUnwiredPublicationFacade,
	runtimePublication,
} from "../agent-runtime/publication";

const { db, sqlite } = getTestDb();
afterAll(() => sqlite.close());
// Keep the module-level worker stopped once the explicit startup gate constructs it.
runtimePublication.stop();

test("SQLite import stays inert; early registrations survive startup activation exactly once", async () => {
	expect(() => runtimePublication.store).toThrow("not activated");
	const { agentLoop } = await import("../../lib/agent/loop");
	expect(typeof agentLoop).toBe("function");
	expect(() => runtimePublication.store).toThrow("not activated");
	expect(() => runtimePublication.newBashRun("task", "parent")).toThrow("not activated");
	let wakes = 0;
	runtimePublication.setWake(() => {
		wakes++;
	});
	runtimePublication.setLegacyRuntimeAdmissionReader("agent", () => undefined);
	runtimePublication.setLegacyCompletionAdmissionReader(() => undefined);
	expect(() => activateSqliteRuntimePublication(undefined as never)).toThrow(
		"initialized database",
	);
	activateSqliteRuntimePublication(db);
	const store = runtimePublication.store;
	activateSqliteRuntimePublication(db);
	expect(runtimePublication.store).toBe(store);

	const now = new Date().toISOString();
	db.insert(narrators)
		.values({ id: "publication-startup-parent", createdAt: now, updatedAt: now })
		.run();
	const run = runtimePublication.newBashRun(
		"publication-startup-task",
		"publication-startup-parent",
	);
	db.transaction((tx) => {
		runtimePublication.reserve(run, tx);
		runtimePublication.commit(
			{ ...run, eventKind: "completed", resultRef: "startup-result", summary: "done" },
			tx,
		);
	});
	expect(runtimePublication.flushRecipient(run.recipientId)).toBe(1);
	expect(wakes).toBe(1);
	runtimePublication.setWake(undefined);
});

test("the facade exposes the exact synchronous API surface of the real service", () => {
	const real = createRuntimePublicationService(db);
	try {
		const facade = createUnwiredPublicationFacade();
		expect(Object.keys(facade).sort()).toEqual(Object.keys(real).sort());
	} finally {
		real.stop();
	}
});

test("every database operation fails closed with a precise error", () => {
	const facade = createUnwiredPublicationFacade();
	const run = { producerKind: "bash" as const, taskId: "t", logicalRunId: "r", recipientId: "p" };
	const invocations: Record<string, () => unknown> = {
		store: () => facade.store,
		startAgentRun: () => facade.startAgentRun({ narratorId: "n", parentNarratorId: "p" }),
		getAgentRun: () => facade.getAgentRun("n", "p"),
		newBashRun: () => facade.newBashRun("t", "p"),
		reserve: () => facade.reserve(run, undefined as never),
		persistResult: () => facade.persistResult(run, "text", undefined as never),
		commit: () =>
			facade.commit(
				{ ...run, eventKind: "completed", resultRef: "r", summary: "s" },
				undefined as never,
			),
		hasPendingSource: () => facade.hasPendingSource(run),
		consumeAwaitedTerminal: () => facade.consumeAwaitedTerminal(run),
		readAgentTerminalResult: () => facade.readAgentTerminalResult(run),
		flushRecipient: () => facade.flushRecipient("p"),
		flushPage: () => facade.flushPage(),
		schedule: () => facade.schedule(),
		migrateLegacyTaskNotice: () =>
			facade.migrateLegacyTaskNotice("p", {
				kind: "bg_bash",
				task: { id: "t" } as never,
			}),
	};
	for (const [operation, invoke] of Object.entries(invocations)) {
		expect(invoke, operation).toThrow(/not wired/);
		expect(invoke, operation).toThrow(/no SQLite fallback/);
	}
});

test("registrations and stop never throw — module-scope producer wiring survives PG startup", () => {
	const facade = createUnwiredPublicationFacade();
	facade.setWake(() => {});
	facade.setWake(undefined);
	facade.setLegacyRuntimeAdmissionReader("agent", () => undefined);
	facade.setLegacyRuntimeAdmissionReader("bash", () => undefined);
	facade.setLegacyCompletionAdmissionReader(() => undefined);
	facade.stop();
});
