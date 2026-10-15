import { Database } from "bun:sqlite";
import { afterEach, describe, expect, mock, spyOn, test } from "bun:test";
import { createWalUpkeepTick, optimizeDatabase } from "../db-resilience";
import { logger } from "../logger";

const databases: Database[] = [];
afterEach(() => {
	mock.restore();
	for (const db of databases.splice(0)) db.close();
});

function logs() {
	return {
		info: spyOn(logger, "info").mockImplementation(() => {}),
		warn: spyOn(logger, "warn").mockImplementation(() => {}),
	};
}

function fakeDatabase(
	checkpoint: () => { busy: number; log: number; checkpointed: number },
	run: (sql: string) => void = () => {},
): Database {
	return {
		query: (sql: string) => {
			expect(sql).toBe("PRAGMA wal_checkpoint(PASSIVE)");
			return { get: checkpoint };
		},
		run,
	} as unknown as Database;
}

function completedCalls(spy: ReturnType<typeof logs>["info"]) {
	return spy.mock.calls
		.filter(([message]) => message === "SQLite maintenance completed")
		.map(([, data]) => data);
}

describe("SQLite maintenance timing", () => {
	test("real SQLite still checkpoints every tick and optimizes every sixth tick", () => {
		const { info } = logs();
		const db = new Database(":memory:");
		databases.push(db);
		const tick = createWalUpkeepTick(db);
		for (let i = 0; i < 12; i++) tick();
		const completed = completedCalls(info);
		expect(completed.filter((data) => data?.operation === "wal_checkpoint(PASSIVE)")).toHaveLength(
			12,
		);
		expect(completed.filter((data) => data?.operation === "PRAGMA optimize")).toEqual([
			expect.objectContaining({ tick: 6, source: "periodic", success: true, analysisLimit: 1000 }),
			expect.objectContaining({ tick: 12, source: "periodic", success: true, analysisLimit: 1000 }),
		]);
		expect(completed[0]).toMatchObject({ logPages: -1, remainingPages: null });
	});

	test("records WAL positions without calling them new write volume", () => {
		const { info } = logs();
		createWalUpkeepTick(fakeDatabase(() => ({ busy: 0, log: 396, checkpointed: 211 })))();
		expect(completedCalls(info)[0]).toMatchObject({
			operation: "wal_checkpoint(PASSIVE)",
			tick: 1,
			busy: 0,
			logPages: 396,
			checkpointedPagesTotal: 211,
			remainingPages: 185,
			success: true,
		});
	});

	test("warns for a slow checkpoint with its measured duration", () => {
		const { warn } = logs();
		let elapsed = 0;
		spyOn(performance, "now").mockImplementation(() => elapsed);
		createWalUpkeepTick(
			fakeDatabase(() => {
				elapsed += 45_000;
				return { busy: 0, log: 1, checkpointed: 1 };
			}),
		)();
		expect(completedCalls(warn)[0]).toMatchObject({ durationMs: 45_000, success: true });
	});

	test("checkpoint failure remains best-effort and does not skip sixth-tick optimize", () => {
		const { info, warn } = logs();
		const run = mock((_sql: string) => {});
		const tick = createWalUpkeepTick(
			fakeDatabase(() => {
				throw new Error("checkpoint failure");
			}, run),
		);
		for (let i = 0; i < 6; i++) expect(tick).not.toThrow();
		expect(completedCalls(warn)).toHaveLength(6);
		expect(completedCalls(warn)[0]).toMatchObject({ success: false, logPages: null });
		expect(completedCalls(info)[0]).toMatchObject({ operation: "PRAGMA optimize", tick: 6 });
		expect(run.mock.calls).toEqual([["PRAGMA analysis_limit = 1000"], ["PRAGMA optimize"]]);
	});

	test("records startup optimize and preserves its sampling configuration", () => {
		const { info } = logs();
		const run = mock((_sql: string) => {});
		optimizeDatabase(
			fakeDatabase(() => ({ busy: 0, log: 0, checkpointed: 0 }), run),
			{
				source: "startup",
			},
		);
		expect(completedCalls(info)[0]).toMatchObject({
			operation: "PRAGMA optimize",
			source: "startup",
			analysisLimit: 1000,
			success: true,
		});
		expect(run.mock.calls).toEqual([["PRAGMA analysis_limit = 1000"], ["PRAGMA optimize"]]);
	});

	test("warns for slow optimize and still reports failures without throwing", () => {
		const { warn } = logs();
		let elapsed = 0;
		spyOn(performance, "now").mockImplementation(() => elapsed);
		const db = fakeDatabase(
			() => ({ busy: 0, log: 0, checkpointed: 0 }),
			(sql) => {
				if (sql === "PRAGMA optimize") {
					elapsed += 500;
					throw new Error("optimize failure");
				}
			},
		);
		expect(() => optimizeDatabase(db)).not.toThrow();
		expect(completedCalls(warn)[0]).toMatchObject({
			operation: "PRAGMA optimize",
			durationMs: 500,
			success: false,
		});
	});
});
