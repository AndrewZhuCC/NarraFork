import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { LocalStorage } from "../update-server/storage/local";

const MIB = 1024 ** 2;
const SIZE = 10 * MIB;
const CHUNK_SIZE = 64 * 1024;
function deferred() {
	let resolve = () => {};
	const promise = new Promise<void>((done) => {
		resolve = done;
	});
	return { promise, resolve };
}
function artifact(value: number) {
	const chunk = Buffer.alloc(CHUNK_SIZE, value);
	const digest = createHash("sha256");
	for (let i = 0; i < SIZE / CHUNK_SIZE; i++) digest.update(chunk);
	const sha256 = digest.digest("hex");
	return { sha256, path: `mirror-objects/${sha256}`, chunk };
}
function stream(
	a: ReturnType<typeof artifact>,
	gate?: ReturnType<typeof deferred>,
	paused?: ReturnType<typeof deferred>,
	canceled?: () => void,
) {
	let count = 0;
	return new ReadableStream<Uint8Array>(
		{
			async pull(controller) {
				if (gate && count === (9 * MIB) / CHUNK_SIZE) {
					paused?.resolve();
					await gate.promise;
				}
				if (count++ < SIZE / CHUNK_SIZE) controller.enqueue(a.chunk);
				else controller.close();
			},
			cancel() {
				canceled?.();
			},
		},
		{ highWaterMark: 0 },
	);
}
function options(
	a: ReturnType<typeof artifact>,
	quotaBytes: number,
	signal = AbortSignal.timeout(5000),
) {
	return { size: SIZE, sha256: a.sha256, signal, timeoutMs: 5000, quotaBytes, minFreeBytes: 0 };
}
async function fixture(run: (storage: LocalStorage) => Promise<void>) {
	const directory = await mkdtemp(join(tmpdir(), "nf-concurrent-quota-"));
	try {
		await run(new LocalStorage(directory));
	} finally {
		await rm(directory, { recursive: true, force: true });
	}
}
async function waitForPartial(storage: LocalStorage, a: ReturnType<typeof artifact>) {
	for (let attempt = 0; attempt < 1000; attempt++) {
		const temporary = (await storage.listFiles("mirror-objects")).find(
			(p) => p.startsWith(a.path) && p.endsWith(".tmp"),
		);
		if (temporary && (await storage.getFileSize(temporary)) === 9 * MIB) return;
		await new Promise((resolve) => setTimeout(resolve, 1));
	}
	throw new Error("Expected the real writer to persist 9MiB before the next admission");
}
async function noTemps(storage: LocalStorage) {
	expect((await storage.listFiles("mirror-objects")).some((p) => p.endsWith(".tmp"))).toBe(false);
}

test("active 9MiB temp is not charged again when three 10MiB objects exactly fit quota30MiB", async () => {
	await fixture(async (storage) => {
		const [a, b, c] = [artifact(1), artifact(2), artifact(3)];
		const gate = deferred();
		const paused = deferred();
		const first = storage.saveVerifiedFile(a.path, stream(a), options(a, 30 * MIB));
		const second = storage.saveVerifiedFile(b.path, stream(b, gate, paused), options(b, 30 * MIB));
		try {
			await first;
			await paused.promise;
			await waitForPartial(storage, b);
			await storage.saveVerifiedFile(c.path, stream(c), options(c, 30 * MIB));
		} finally {
			gate.resolve();
			await second;
		}
		for (const entry of [a, b, c]) expect(await storage.getFileSize(entry.path)).toBe(SIZE);
		await noTemps(storage);
	});
});
test("full quota permits concurrent idempotent replacements of different SHA objects", async () => {
	await fixture(async (storage) => {
		const entries = [artifact(4), artifact(5), artifact(6)];
		for (const a of entries)
			await storage.saveVerifiedFile(a.path, stream(a), options(a, 30 * MIB));
		await Promise.all(
			entries.map((a) => storage.saveVerifiedFile(a.path, stream(a), options(a, 30 * MIB))),
		);
		for (const a of entries) expect(await storage.getFileSize(a.path)).toBe(SIZE);
		await noTemps(storage);
	});
});
test("same SHA remains mutually exclusive at full quota, then can be replayed after cleanup", async () => {
	await fixture(async (storage) => {
		const a = artifact(7);
		await storage.saveVerifiedFile(a.path, stream(a), options(a, SIZE));
		const gate = deferred();
		const paused = deferred();
		const replacing = storage.saveVerifiedFile(a.path, stream(a, gate, paused), options(a, SIZE));
		let canceled = false;
		try {
			await paused.promise;
			await waitForPartial(storage, a);
			await expect(
				storage.saveVerifiedFile(
					a.path,
					stream(a, undefined, undefined, () => {
						canceled = true;
					}),
					options(a, SIZE),
				),
			).rejects.toThrow("duplicate");
			expect(canceled).toBe(true);
		} finally {
			gate.resolve();
			await replacing;
		}
		await storage.saveVerifiedFile(a.path, stream(a), options(a, SIZE));
		expect(await storage.getFileSize(a.path)).toBe(SIZE);
		await noTemps(storage);
	});
});
test("concurrent new objects cannot overbook committed quota", async () => {
	await fixture(async (storage) => {
		const entries = [artifact(8), artifact(9), artifact(10)];
		const results = await Promise.allSettled(
			entries.map((a) => storage.saveVerifiedFile(a.path, stream(a), options(a, 20 * MIB))),
		);
		expect(results.filter((r) => r.status === "fulfilled")).toHaveLength(2);
		expect(results.filter((r) => r.status === "rejected")).toHaveLength(1);
		const files = await storage.listFiles("mirror-objects");
		expect(files).toHaveLength(2);
		for (const path of files) expect(await storage.getFileSize(path)).toBe(SIZE);
		await noTemps(storage);
	});
});
test("crash-like orphan temps are still charged and cannot be hidden by the active-temp exclusion", async () => {
	await fixture(async (storage) => {
		const a = artifact(11);
		const b = artifact(12);
		await storage.saveFile(`mirror-objects/${"f".repeat(64)}.orphan.tmp`, stream(a));
		await storage.saveVerifiedFile(a.path, stream(a), options(a, 20 * MIB));
		await expect(storage.saveVerifiedFile(b.path, stream(b), options(b, 20 * MIB))).rejects.toThrow(
			"quota",
		);
		expect(await storage.getFileSize(b.path)).toBeNull();
		expect(
			(await storage.listFiles("mirror-objects")).filter((p) => p.endsWith(".tmp")),
		).toHaveLength(1);
	});
});
test("failed or cancelled writers release quota, temp ownership and duplicate-object guards", async () => {
	await fixture(async (storage) => {
		const a = artifact(13);
		const bad = artifact(14);
		await expect(storage.saveVerifiedFile(a.path, stream(bad), options(a, SIZE))).rejects.toThrow(
			"SHA256",
		);
		const controller = new AbortController();
		const gate = deferred();
		const paused = deferred();
		const uploading = storage.saveVerifiedFile(
			a.path,
			stream(a, gate, paused),
			options(a, SIZE, controller.signal),
		);
		await paused.promise;
		controller.abort();
		try {
			await expect(uploading).rejects.toThrow("aborted");
		} finally {
			gate.resolve();
		}
		await noTemps(storage);
		await storage.saveVerifiedFile(a.path, stream(a), options(a, SIZE));
		expect(await storage.getFileSize(a.path)).toBe(SIZE);
		await noTemps(storage);
	});
});
