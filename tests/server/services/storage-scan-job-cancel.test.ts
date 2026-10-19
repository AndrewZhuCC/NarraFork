/**
 * The storage scan job's cancel → rescan sequence, per category.
 *
 * `abort()` only raises a flag: the scan notices it at its next checkpoint, so for a
 * moment afterwards the category is still `running`. Two things went wrong when there
 * was a single job slot:
 *
 *   1. `cancelStorageScan()` returned the still-`running` state;
 *   2. an immediate rescan was deduped onto the dying run.
 *
 * Categories now have their own slots and run together. These tests hold the fake
 * category scan open at a checkpoint and assert on the state a client would receive.
 */

import { beforeEach, describe, expect, it, mock } from "bun:test";
import type { StorageCategoryKey } from "@shared/storage-categories";

/** Resolvers for every fake category scan currently parked on a checkpoint. */
const waiters: Array<() => void> = [];
/** How many times a category scan has been started. */
let scanStarts = 0;

class FakeAbortedError extends Error {}

function fakeScanStorageCategory(_key: StorageCategoryKey, options: { signal?: AbortSignal } = {}) {
	scanStarts++;
	const { signal } = options;
	return (async () => {
		for (;;) {
			await new Promise<void>((resolve) => {
				waiters.push(resolve);
			});
			if (signal?.aborted) throw new FakeAbortedError();
		}
	})();
}

mock.module("@server/services/storage-service", () => ({
	scanStorageCategory: fakeScanStorageCategory,
	StorageScanAbortedError: FakeAbortedError,
	rememberStorageCategory: () => ({}),
	storageService: {},
}));
mock.module("../../../server/services/storage-service", () => ({
	scanStorageCategory: fakeScanStorageCategory,
	StorageScanAbortedError: FakeAbortedError,
	rememberStorageCategory: () => ({}),
	storageService: {},
}));

const { cancelStorageScan, getStorageScanJob, startStorageScan } = await import(
	"../../../server/services/storage-scan-job"
);

const database = "database" satisfies StorageCategoryKey;
const uploads = "uploads" satisfies StorageCategoryKey;

/** Let every parked category scan reach its next checkpoint. */
async function step() {
	for (let i = 0; i < 5 && waiters.length === 0; i++) await Promise.resolve();
	const pending = waiters.splice(0);
	for (const release of pending) release();
	for (let i = 0; i < 10; i++) await Promise.resolve();
}

function categoryStatus(key: StorageCategoryKey) {
	return getStorageScanJob().categoryJobs.find((job) => job.key === key)?.status;
}

beforeEach(async () => {
	if (getStorageScanJob().status === "running") {
		const done = cancelStorageScan();
		await step();
		await done;
	}
	scanStarts = 0;
	waiters.splice(0);
});

describe("storage scan job — cancel settles before it answers", () => {
	it("returns a TERMINAL state, not the running one it just aborted", async () => {
		startStorageScan({ categories: [database] });
		await step();
		expect(categoryStatus(database)).toBe("running");

		const pending = cancelStorageScan({ categories: [database] });
		expect(categoryStatus(database)).toBe("running");
		await step();
		const state = await pending;
		expect(state.categoryJobs.find((job) => job.key === database)?.status).toBe("cancelled");
		expect(categoryStatus(database)).toBe("cancelled");
	});

	it("starts a NEW scan immediately after a cancel, before the old one has landed", async () => {
		startStorageScan({ categories: [database] });
		await step();
		expect(scanStarts).toBe(1);

		void cancelStorageScan({ categories: [database] });
		const second = startStorageScan({ categories: [database] });
		expect(second.started).toBe(true);
		expect(scanStarts).toBe(2);
		expect(categoryStatus(database)).toBe("running");
	});

	it("does not let the cancelled scan overwrite the replacement's state", async () => {
		startStorageScan({ categories: [database] });
		await step();
		void cancelStorageScan({ categories: [database] });
		startStorageScan({ categories: [database] });
		await step();
		await step();
		expect(categoryStatus(database)).toBe("running");
	});

	it("still dedupes a plain double-start (no cancel involved)", async () => {
		startStorageScan({ categories: [database] });
		await step();
		const again = startStorageScan({ categories: [database] });
		expect(again.started).toBe(false);
		expect(scanStarts).toBe(1);
		expect(categoryStatus(database)).toBe("running");
	});

	it("runs two categories without waiting for the first to finish", async () => {
		const started = startStorageScan({ categories: [database, uploads] });
		await step();
		expect(started.started).toBe(true);
		expect(scanStarts).toBe(2);
		expect(categoryStatus(database)).toBe("running");
		expect(categoryStatus(uploads)).toBe("running");
		expect(getStorageScanJob().status).toBe("running");
	});

	it("is a no-op when nothing is running", async () => {
		const state = await cancelStorageScan({ categories: [database] });
		expect(categoryStatus(database)).not.toBe("running");
		expect(state.categoryJobs.find((job) => job.key === database)?.status).not.toBe("running");
		expect(scanStarts).toBe(0);
	});
});
