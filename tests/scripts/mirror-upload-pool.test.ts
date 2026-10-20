import { expect, test } from "bun:test";
import {
	groupMirrorUploads,
	MirrorUploadPool,
	mirrorUploadDiskBytes,
} from "../../scripts/lib/mirror-upload-pool";
import { mirrorUploadConcurrency } from "../../shared/mirror-relay";

function pause(signal: AbortSignal, ms: number) {
	return new Promise<void>((resolve, reject) => {
		const abort = () => {
			clearTimeout(timer);
			reject(signal.reason);
		};
		const timer = setTimeout(() => {
			signal.removeEventListener("abort", abort);
			resolve();
		}, ms);
		signal.addEventListener("abort", abort, { once: true });
		if (signal.aborted) abort();
	});
}

test("upload concurrency defaults to direct2/webdav3 and accepts only bounded integer deployment values", () => {
	expect(mirrorUploadConcurrency(undefined, "direct")).toBe(2);
	expect(mirrorUploadConcurrency("", "direct")).toBe(2);
	expect(mirrorUploadConcurrency(undefined, "webdav")).toBe(3);
	expect(mirrorUploadConcurrency()).toBe(3);
	expect(mirrorUploadConcurrency("")).toBe(3);
	for (const value of [1, 3, 8, "1", "8"])
		expect(mirrorUploadConcurrency(value)).toBe(Number(value));
	for (const value of [
		0,
		9,
		-1,
		1.5,
		Number.NaN,
		Number.POSITIVE_INFINITY,
		"NaN",
		"0",
		"9",
		" 3",
		"1e0",
		"03",
		null,
		true,
	])
		expect(() => mirrorUploadConcurrency(value)).toThrow();
});
for (const concurrency of [1, 3, 8])
	test(`whole asset tasks are bounded by ${concurrency}, not only upload calls`, async () => {
		const pool = new MirrorUploadPool(concurrency, AbortSignal.timeout(1000));
		let active = 0;
		let peak = 0;
		await pool.run(
			Array.from({ length: 17 }, (_, i) => i),
			async () => {
				active++;
				peak = Math.max(peak, active);
				try {
					await pause(pool.signal, 3);
					await pause(pool.signal, 3);
				} finally {
					active--;
				}
			},
		);
		expect(peak).toBe(concurrency);
		expect(active).toBe(0);
	});
test("first failure cancels in-flight work, stops scheduling and joins cleanup before rejection", async () => {
	const pool = new MirrorUploadPool(3, AbortSignal.timeout(1000));
	const started: number[] = [];
	const cleaned: number[] = [];
	let canceled = 0;
	await expect(
		pool.run([0, 1, 2, 3, 4, 5], async (value) => {
			started.push(value);
			try {
				if (!value) {
					await pause(pool.signal, 5);
					throw new Error("first physical upload failed");
				}
				await pause(pool.signal, 500);
			} catch (error) {
				if (value && pool.signal.aborted) canceled++;
				throw error;
			} finally {
				await new Promise((r) => setTimeout(r, 5));
				cleaned.push(value);
			}
		}),
	).rejects.toThrow("first physical upload failed");
	expect(started).toEqual([0, 1, 2]);
	expect(cleaned.sort()).toEqual([0, 1, 2]);
	expect(canceled).toBe(2);
});
test("parent cancellation stops the pool without scheduling pending items", async () => {
	const controller = new AbortController();
	const pool = new MirrorUploadPool(2, controller.signal);
	const started: number[] = [];
	const running = pool.run([1, 2, 3, 4], async (n) => {
		started.push(n);
		await pause(pool.signal, 500);
	});
	controller.abort(new Error("user stop"));
	await expect(running).rejects.toThrow("user stop");
	expect(started).toEqual([1, 2]);
});
test("same-hash tasks deduplicate and disk reserves the largest N physical files plus headroom", () => {
	const assets = [
		{ name: "a", size: 7, sha256: "a" },
		{ name: "alias", size: 7, sha256: "a" },
		{ name: "b", size: 9, sha256: "b" },
		{ name: "c", size: 5, sha256: "c" },
	];
	const groups = groupMirrorUploads(assets);
	expect(groups).toHaveLength(3);
	expect(groups[0].names).toEqual(["a", "alias"]);
	expect(
		mirrorUploadDiskBytes(
			groups.map((g) => g.asset),
			2,
		),
	).toBe(64 * 1024 ** 2 + 16);
	expect(() => groupMirrorUploads([...assets, { name: "bad", size: 3, sha256: "a" }])).toThrow();
});
