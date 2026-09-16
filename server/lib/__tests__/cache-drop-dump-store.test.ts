import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import { createHash, randomUUID } from "node:crypto";
import * as fs from "node:fs/promises";
import {
	lstat,
	mkdir,
	mkdtemp,
	readdir,
	readFile,
	rename,
	rm,
	stat,
	symlink,
	writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import {
	CACHE_DROP_TTL_MS,
	CacheDropDumpStore,
	type CacheDropSample,
} from "../cache-drop-dump-store";

interface Manifest {
	kind: "event" | "rolling";
	narratorId: string;
	afterStatus?: string;
	comparison?: {
		previousRequestId: string;
		previousCachedInputTokens: number;
		cachedInputTokens: number;
	};
	samples: {
		requestId: string;
		fileName: string;
		role: string;
		kind: string;
		missing?: boolean;
		stats: { inputTokens: number; cachedInputTokens?: number };
	}[];
}
let root: string;
let clock: number;
let sequence: number;
let warnings: ReturnType<typeof spyOn>;

beforeEach(async () => {
	root = await mkdtemp(join(tmpdir(), "nf-cache-diagnostics-test-"));
	clock = 1_800_000_000_000;
	sequence = 0;
	warnings = spyOn(console, "warn").mockImplementation(() => {});
});
afterEach(async () => {
	warnings.mockRestore();
	await rm(root, { recursive: true, force: true });
});

function sample(overrides: Partial<CacheDropSample> = {}): CacheDropSample {
	clock++;
	return {
		requestId: `request-${++sequence}`,
		narratorId: "narrator-a",
		provider: "anthropic",
		model: "model-a",
		kind: "narrator",
		conversationId: "conversation-a",
		createdAt: new Date(clock).toISOString(),
		startedAt: clock,
		inputTokens: 100_000,
		cachedInputTokens: 93_056,
		dump: {
			request: { messages: [{ role: "user", content: "actual input" }] },
			response: { text: "actual output" },
		},
		...overrides,
	};
}
function store(options: ConstructorParameters<typeof CacheDropDumpStore>[1] = {}) {
	return new CacheDropDumpStore(root, { now: () => clock, ...options });
}
async function groups(kind?: "event" | "rolling") {
	const result: { name: string; manifest: Manifest }[] = [];
	for (const name of await readdir(root)) {
		if (!/^cd-[er]-/.test(name)) continue;
		if (kind && !name.startsWith(kind === "event" ? "cd-e-" : "cd-r-")) continue;
		const info = await lstat(join(root, name));
		if (!info.isDirectory() || info.isSymbolicLink()) continue;
		const manifest = JSON.parse(
			await readFile(join(root, name, "manifest.json"), "utf8"),
		) as Manifest;
		if (!kind || kind === manifest.kind) result.push({ name, manifest });
	}
	return result;
}
async function verifyReferences() {
	for (const group of await groups()) {
		for (const ref of group.manifest.samples) {
			if (ref.missing) continue;
			const request = JSON.parse(await readFile(join(root, group.name, ref.fileName), "utf8"));
			expect(request.requestId).toBe(ref.requestId);
			expect(request.dump).toBeDefined();
		}
	}
}
async function usage() {
	let bytes = 0;
	let files = 0;
	for (const group of await readdir(root)) {
		if (!/^cd-[ert]-/.test(group)) continue;
		const info = await lstat(join(root, group));
		if (!info.isDirectory() || info.isSymbolicLink()) continue;
		for (const name of await readdir(join(root, group))) {
			bytes += (await lstat(join(root, group, name))).size;
			files++;
		}
	}
	return { bytes, files };
}
async function trigger(s: CacheDropDumpStore) {
	await s.record(sample());
	await s.record(sample());
	await s.record(sample({ cachedInputTokens: 3968 }));
}

describe("CacheDropDumpStore", () => {
	test("retains exactly the last three real request/response snapshots per narrator", async () => {
		const s = store();
		for (let i = 0; i < 5; i++) await s.record(sample());
		await s.record(sample({ narratorId: "narrator-b", cachedInputTokens: 0 }));
		const rolling = await groups("rolling");
		expect(rolling).toHaveLength(2);
		expect(
			rolling
				.find((g) => g.manifest.narratorId === "narrator-a")
				?.manifest.samples.map((r) => r.requestId),
		).toEqual(["request-3", "request-4", "request-5"]);
		expect(await groups("event")).toHaveLength(0);
		expect((await usage()).files).toBe(6);
		await verifyReferences();
	});

	test("freezes before2 + 93056->3968 trigger + next same-narrator reflection", async () => {
		const s = store();
		await trigger(s);
		await s.record(sample({ narratorId: "other", cachedInputTokens: 0 }));
		expect((await groups("event"))[0]?.manifest.afterStatus).toBe("pending");
		await s.record(sample({ kind: "reflection", cachedInputTokens: undefined, inputTokens: 0 }));
		const event = (await groups("event"))[0];
		expect(event?.manifest.samples.map((r) => r.role)).toEqual([
			"before",
			"before",
			"trigger",
			"after",
		]);
		expect(event?.manifest.samples.map((r) => r.requestId)).toEqual([
			"request-1",
			"request-2",
			"request-3",
			"request-5",
		]);
		expect(event?.manifest.samples[2]?.stats.cachedInputTokens).toBe(3968);
		expect(event?.manifest.samples[3]?.kind).toBe("reflection");
		expect(event?.manifest.afterStatus).toBe("captured");
		for (let i = 0; i < 5; i++) await s.record(sample({ cachedInputTokens: 3968 }));
		expect((await groups("event"))[0]?.manifest.samples).toHaveLength(4);
		await verifyReferences();
	});

	test("zero uncached input still compares an explicitly present cached metric", async () => {
		const s = store();
		await s.record(sample({ inputTokens: 0 }));
		await s.record(sample({ inputTokens: 0, cachedInputTokens: 3968 }));
		expect(await groups("event")).toHaveLength(1);
	});

	test("increased input without falling cached tokens does not trigger", async () => {
		const s = store();
		await s.record(sample());
		await s.record(sample({ inputTokens: 1_000_000 }));
		await s.record(sample({ inputTokens: 2_000_000, cachedInputTokens: 100_000 }));
		expect(await groups("event")).toHaveLength(0);
	});

	test.each([
		{ model: "model-b" },
		{ provider: "openai" },
		{ conversationId: "conversation-b" },
		{ conversationId: undefined },
		{ contextEpoch: 2 },
	])("identity change does not trigger: %j", async (identity) => {
		const s = store();
		await s.record(sample());
		await s.record(sample({ ...identity, cachedInputTokens: 3968 }));
		await s.record(sample({ cachedInputTokens: 0 }));
		expect(await groups("event")).toHaveLength(0);
	});

	test("compact clears baseline and late old requests cannot rebuild it", async () => {
		const s = store();
		await s.record(sample());
		const old = sample({ cachedInputTokens: 93_056 });
		clock += 100;
		await s.resetNarrator("narrator-a");
		await s.record(old);
		await s.record(sample({ cachedInputTokens: 3968 }));
		expect(await groups("event")).toHaveLength(0);
		await s.record(sample({ cachedInputTokens: 93_056 }));
		await s.record(sample({ cachedInputTokens: 3968 }));
		expect(await groups("event")).toHaveLength(1);
	});

	test("late completion cannot replace a newer baseline", async () => {
		const s = store();
		const old = sample();
		await s.record(sample({ cachedInputTokens: 3968 }));
		await s.record(old);
		await s.record(sample({ cachedInputTokens: 3968 }));
		expect(await groups("event")).toHaveLength(0);
	});

	test("errors and missing usage remain context but do not trigger or replace valid baseline", async () => {
		const s = store();
		await s.record(sample());
		await s.record(sample({ errorMessage: "upstream failure", cachedInputTokens: 0 }));
		await s.record(sample({ cachedInputTokens: undefined, inputTokens: 0 }));
		expect(await groups("event")).toHaveLength(0);
		await s.record(sample({ cachedInputTokens: 3968 }));
		const event = (await groups("event"))[0];
		expect(event?.manifest.samples.map((r) => r.requestId)).toEqual([
			"request-2",
			"request-3",
			"request-4",
		]);
		expect(event?.manifest.comparison).toEqual({
			previousRequestId: "request-1",
			previousCachedInputTokens: 93056,
			cachedInputTokens: 3968,
		});
		await s.record(sample({ errorMessage: "after failed", cachedInputTokens: undefined }));
		expect((await groups("event"))[0]?.manifest.afterStatus).toBe("captured");
		await verifyReferences();
	});

	test("requires minimum absolute drop and half the previous cached count", async () => {
		const s = store();
		await s.record(sample({ cachedInputTokens: 8000 }));
		await s.record(sample({ cachedInputTokens: 0 }));
		await s.record(sample({ cachedInputTokens: 20_000 }));
		await s.record(sample({ cachedInputTokens: 11_000 }));
		expect(await groups("event")).toHaveLength(0);
		await s.record(sample({ cachedInputTokens: 0 }));
		expect(await groups("event")).toHaveLength(1);
	});

	test("consecutive triggers create bounded independent events instead of extending one", async () => {
		const s = store();
		for (const cachedInputTokens of [100_000, 40_000, 16_000, 4000, 3000])
			await s.record(sample({ cachedInputTokens }));
		const events = await groups("event");
		expect(events).toHaveLength(3);
		expect(events.map((e) => e.manifest.samples.length).sort()).toEqual([3, 4, 4]);
		expect(events.every((e) => e.manifest.afterStatus === "captured")).toBe(true);
		await verifyReferences();
	});

	test("global file budget evicts whole old events and idle rolling groups", async () => {
		const s = store({ maxFiles: 9 });
		for (const cachedInputTokens of [93_056, 3968, 93_056, 3968, 3968])
			await s.record(sample({ cachedInputTokens }));
		expect((await usage()).files).toBeLessThanOrEqual(9);
		expect(await groups("event")).toHaveLength(1);
		for (let i = 0; i < 8; i++) await s.record(sample({ narratorId: `idle-${i}` }));
		expect((await usage()).files).toBeLessThanOrEqual(9);
		expect((await groups("rolling")).length).toBeLessThanOrEqual(4);
		await verifyReferences();
	});

	test.each([
		{ maxBytes: 12_000, maxFiles: 10, payload: 300 },
		{ maxBytes: 14_000, maxFiles: 64, payload: 900 },
	])("every request, hardlink and temporary manifest stays within peak budgets: %j", async ({
		maxBytes,
		maxFiles,
		payload,
	}) => {
		const s = store({ maxBytes, maxFiles });
		let peakBytes = 0;
		let peakFiles = 0;
		let temporaryManifests = 0;
		let links = 0;
		const observe = async () => {
			const current = await usage();
			peakBytes = Math.max(peakBytes, current.bytes);
			peakFiles = Math.max(peakFiles, current.files);
		};
		const originalWrite = fs.writeFile;
		const originalLink = fs.link;
		const writes = spyOn(fs, "writeFile").mockImplementation(async (...args) => {
			await originalWrite(...args);
			if (String(args[0]).startsWith(root)) {
				if (String(args[0]).endsWith(".tmp")) temporaryManifests++;
				await observe();
			}
		});
		const hardlinks = spyOn(fs, "link").mockImplementation(async (...args) => {
			await originalLink(...args);
			if (String(args[1]).startsWith(root)) {
				links++;
				await observe();
			}
		});
		try {
			for (const cachedInputTokens of [93056, 93056, 3968, 3968, 93056, 3968, 3968])
				await s.record(sample({ cachedInputTokens, dump: "x".repeat(payload) }));
			await s.clearRolling();
			expect(temporaryManifests).toBeGreaterThan(5);
			expect(links).toBeGreaterThan(3);
			expect(peakBytes).toBeLessThanOrEqual(maxBytes);
			expect(peakFiles).toBeLessThanOrEqual(maxFiles);
			await verifyReferences();
		} finally {
			writes.mockRestore();
			hardlinks.mockRestore();
		}
	});

	test("an impossible new request retains existing evidence instead of clearing the store", async () => {
		const s = store({ maxBytes: 10_000 });
		await trigger(s);
		expect(await groups("event")).toHaveLength(1);
		const before = JSON.stringify(await groups());
		const beforeUsage = await usage();
		await s.record(sample({ dump: "x".repeat(12_000) }));
		expect(JSON.stringify(await groups())).toBe(before);
		expect(await usage()).toEqual(beforeUsage);
		expect(warnings).toHaveBeenCalled();
		await verifyReferences();
	});

	test("rolling eviction during reservation reloads context and resets comparison", async () => {
		const s = store({ maxFiles: 4 });
		await s.record(sample());
		await s.record(sample());
		await s.record(sample({ cachedInputTokens: 3968 }));
		expect((await groups("rolling"))[0]?.manifest.samples.map((ref) => ref.requestId)).toEqual([
			"request-3",
		]);
		expect(await groups("event")).toHaveLength(0);
		expect((await usage()).files).toBe(2);
		await verifyReferences();
	});

	test("failed space reclamation stops before any new request or manifest write", async () => {
		const s = store({ maxBytes: 10_000 });
		await trigger(s);
		const originalRm = fs.rm;
		const failure = spyOn(fs, "rm").mockImplementation((path, options) => {
			if (String(path).startsWith(join(root, "cd-t-")))
				return Promise.reject(new Error("simulated deletion failure"));
			return originalRm(path, options);
		});
		const writes = spyOn(fs, "writeFile");
		try {
			await s.record(sample({ dump: "x".repeat(6000) }));
			expect(failure).toHaveBeenCalled();
			expect(writes).not.toHaveBeenCalled();
			expect((await usage()).bytes).toBeLessThanOrEqual(10_000);
			expect(warnings).toHaveBeenCalled();
		} finally {
			writes.mockRestore();
			failure.mockRestore();
		}
		await s.maintain();
		await verifyReferences();
	});

	test("a one-file budget refuses even the first request and creates no partial files", async () => {
		await store({ maxFiles: 1 }).record(sample());
		expect(await readdir(root)).toEqual([]);
		expect(warnings).toHaveBeenCalled();
	});

	test("global byte budget includes manifests and never leaves dangling references", async () => {
		const s = store({ maxBytes: 15_000 });
		for (let i = 0; i < 10; i++)
			await s.record(
				sample({ cachedInputTokens: i % 2 ? 3968 : 93_056, dump: { text: "x".repeat(1500) } }),
			);
		expect((await usage()).bytes).toBeLessThanOrEqual(15_000);
		await verifyReferences();
	});

	test("TTL maintenance cleans idle evidence and rolling without reading dump bodies", async () => {
		const s = store();
		await trigger(s);
		clock += CACHE_DROP_TTL_MS;
		await s.maintain();
		expect(await readdir(root)).toEqual([]);
		await s.record(sample({ cachedInputTokens: 0 }));
		expect(await groups("event")).toHaveLength(0);
	});

	test("disable cancels queued records and removes rolling, preserving events", async () => {
		const s = store();
		await trigger(s);
		const writes = Array.from({ length: 10 }, () => s.record(sample({ cachedInputTokens: 3968 })));
		await s.clearRolling();
		await Promise.all(writes);
		expect(await groups("rolling")).toHaveLength(0);
		expect(await groups("event")).toHaveLength(1);
		expect((await groups("event"))[0]?.manifest.afterStatus).toBe("disabled");
		await verifyReferences();
	});

	test("restart removes rolling, retains events, and never restores an old baseline", async () => {
		const first = store();
		await trigger(first);
		await first.record(sample());
		const restarted = store();
		await restarted.maintain();
		expect(await groups("rolling")).toHaveLength(0);
		expect(await groups("event")).toHaveLength(1);
		await restarted.record(sample({ cachedInputTokens: 3968 }));
		expect(await groups("event")).toHaveLength(1);
		await verifyReferences();
	});

	test("restart annotates pending after samples and missing files, preserving surviving evidence", async () => {
		await trigger(store());
		const event = (await groups("event"))[0];
		if (!event?.manifest.samples[0]) throw new Error("missing event");
		await rm(join(root, event.name, event.manifest.samples[0].fileName));
		await store().maintain();
		const recovered = (await groups("event"))[0]?.manifest;
		expect(recovered?.afterStatus).toBe("restart");
		expect(recovered?.samples[0]?.missing).toBe(true);
		await verifyReferences();
	});

	test("corrupt manifests do not prevent restart cleanup or overwrite frozen evidence", async () => {
		await trigger(store());
		const event = (await groups("event"))[0];
		if (!event) throw new Error("missing event");
		const corrupt = join(root, `cd-e-${randomUUID()}`);
		await mkdir(corrupt);
		await writeFile(join(corrupt, "manifest.json"), "broken metadata");
		const restarted = store({ now: Date.now });
		await restarted.maintain();
		expect((await readdir(root)).filter((name) => name.startsWith("cd-r-"))).toHaveLength(0);
		expect(await readFile(join(corrupt, "manifest.json"), "utf8")).toBe("broken metadata");
		for (const ref of event.manifest.samples)
			expect((await stat(join(root, event.name, ref.fileName))).size).toBeGreaterThan(0);
		await restarted.record(sample());
		expect((await readdir(root)).filter((name) => name.startsWith("cd-r-"))).toHaveLength(1);
		expect(warnings).toHaveBeenCalled();
	});

	test("maintenance clears only abandoned staging directories", async () => {
		const s = store();
		await trigger(s);
		const staging = join(root, `cd-t-${randomUUID()}`);
		await mkdir(staging);
		await writeFile(join(staging, "partial.json"), "partial write");
		await mkdir(join(root, "cd-t-unrelated"));
		await s.maintain();
		expect((await readdir(root)).some((name) => name === basename(staging))).toBe(false);
		expect(await readdir(root)).toContain("cd-t-unrelated");
		await verifyReferences();
	});

	test("UTF-8 oversized and non-serializable snapshots are visibly rejected without damaging events", async () => {
		const s = store({ maxRequestBytes: 1500 });
		await trigger(s);
		const snapshot = JSON.stringify(await groups("event"));
		await s.record(sample({ dump: "界".repeat(600) }));
		const cycle: Record<string, unknown> = {};
		cycle.self = cycle;
		await s.record(sample({ dump: cycle }));
		expect(warnings).toHaveBeenCalledTimes(2);
		expect(JSON.stringify(await groups("event"))).toBe(snapshot);
		await verifyReferences();
	});

	test("hashed filenames, restrictive permissions, and namespace-only cleanup", async () => {
		const s = store();
		await writeFile(join(root, "unrelated.txt"), "keep");
		await mkdir(join(root, "unrelated-directory"));
		await s.record(
			sample({ narratorId: "../../outside/用户名", requestId: "../../unsafe/request" }),
		);
		const rolling = (await groups("rolling"))[0];
		expect(rolling?.name).toMatch(/^cd-r-[a-f0-9]{64}$/);
		if (!rolling) throw new Error("missing rolling group");
		if (process.platform !== "win32") {
			expect((await stat(root)).mode & 0o777).toBe(0o700);
			expect((await stat(join(root, rolling.name))).mode & 0o777).toBe(0o700);
			for (const file of await readdir(join(root, rolling.name)))
				expect((await stat(join(root, rolling.name, file))).mode & 0o777).toBe(0o600);
		}
		await s.clearRolling();
		expect((await readdir(root)).sort()).toEqual(["unrelated-directory", "unrelated.txt"]);
	});

	test("refuses symlinked roots and rolling paths without changing the target", async () => {
		const outside = join(root, "outside");
		await mkdir(outside);
		const alias = join(root, "alias");
		await symlink(outside, alias, "dir");
		await new CacheDropDumpStore(alias).record(sample());
		expect(await readdir(outside)).toEqual([]);
		const name = `cd-r-${createHash("sha256").update("narrator-a").digest("hex")}`;
		await symlink(outside, join(root, name), "dir");
		await store().record(sample());
		expect(await readdir(outside)).toEqual([]);
		expect(warnings).toHaveBeenCalledTimes(2);
	});

	test("failed atomic rolling update does not change frozen evidence", async () => {
		const s = store();
		await trigger(s);
		const eventsBefore = JSON.stringify(await groups("event"));
		const rolling = (await groups("rolling"))[0];
		if (!rolling) throw new Error("missing rolling");
		const manifestPath = join(root, rolling.name, "manifest.json");
		await rename(manifestPath, join(root, rolling.name, "saved.json"));
		await mkdir(manifestPath);
		await s.record(sample({ cachedInputTokens: 3968 }));
		expect(JSON.stringify(await groups("event"))).toBe(eventsBefore);
		expect(warnings).toHaveBeenCalled();
		await rm(manifestPath, { recursive: true });
		await rename(join(root, rolling.name, "saved.json"), manifestPath);
		await s.record(sample({ cachedInputTokens: 3968 }));
		await verifyReferences();
	});

	test("serialized byte queue budget skips large snapshots before the item limit", async () => {
		const s = store();
		const pending = Array.from({ length: 8 }, () =>
			s.record(sample({ dump: "x".repeat(4 * 1024 * 1024) })),
		);
		await s.clearRolling();
		await Promise.all(pending);
		expect(warnings).toHaveBeenCalled();
		expect(await groups("rolling")).toHaveLength(0);
	});

	test("duplicate completion does not add a second sample or consume the after slot", async () => {
		const s = store();
		await s.record(sample());
		const drop = sample({ cachedInputTokens: 3968 });
		await s.record(drop);
		await s.record(drop);
		expect((await groups("event"))[0]?.manifest.afterStatus).toBe("pending");
		expect((await groups("rolling"))[0]?.manifest.samples).toHaveLength(2);
	});

	test("reset is narrator-local and conservatively excludes same-millisecond old starts", async () => {
		const s = store();
		await s.record(sample());
		await s.record(sample({ narratorId: "narrator-b" }));
		const old = sample();
		await s.resetNarrator("narrator-a");
		await s.record(old);
		await s.record(sample({ cachedInputTokens: 3968 }));
		await s.record(sample({ narratorId: "narrator-b", cachedInputTokens: 3968 }));
		const events = await groups("event");
		expect(events).toHaveLength(1);
		expect(events[0]?.manifest.narratorId).toBe("narrator-b");
	});

	test("queue has finite capacity, warns on overload, and clear remains available", async () => {
		const s = store();
		const pending = Array.from({ length: 80 }, () => s.record(sample()));
		await s.clearRolling();
		await Promise.all(pending);
		expect(warnings).toHaveBeenCalled();
		expect(await groups("rolling")).toHaveLength(0);
	});

	test("timeout resolves caller without releasing a racing writer; next operation recovers", async () => {
		const s = store({ operationTimeoutMs: 1 });
		await Promise.all(Array.from({ length: 24 }, () => s.record(sample())));
		await store().maintain();
		expect(warnings).toHaveBeenCalled();
		await verifyReferences();
	});
});
