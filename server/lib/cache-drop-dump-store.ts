import { createHash, randomUUID } from "node:crypto";
import {
	chmod,
	link,
	lstat,
	mkdir,
	opendir,
	readFile,
	rename,
	rm,
	writeFile,
} from "node:fs/promises";
import { join, resolve } from "node:path";

export const CACHE_DROP_MIN_TOKENS = 8192;
export const CACHE_DROP_MAX_REQUEST_BYTES = 8 * 1024 * 1024;
export const CACHE_DROP_MAX_BYTES = 256 * 1024 * 1024;
export const CACHE_DROP_MAX_FILES = 256;
export const CACHE_DROP_MAX_PENDING_BYTES = 32 * 1024 * 1024;
export const CACHE_DROP_TTL_MS = 7 * 24 * 60 * 60 * 1000;
const MAX_METADATA_BYTES = 8192;
const MAX_MANIFEST_BYTES = 64 * 1024;
const MAX_QUEUE = 32;
const OPERATION_TIMEOUT_MS = 5000;
const GROUP_NAME = /^cd-(?:r-[a-f0-9]{64}|[et]-[a-f0-9-]{36})$/;
const SAMPLE_NAME = /^s-[a-f0-9-]{36}\.json$/;

export interface CacheDropSample {
	requestId: string;
	narratorId: string;
	provider: string;
	model: string;
	kind: string;
	conversationId?: string;
	contextEpoch?: number;
	createdAt: string;
	startedAt: number;
	inputTokens: number;
	cachedInputTokens?: number;
	errorMessage?: string | null;
	dump: unknown;
}

interface Options {
	maxBytes?: number;
	maxFiles?: number;
	maxRequestBytes?: number;
	ttlMs?: number;
	operationTimeoutMs?: number;
	now?: () => number;
}

type SampleRef = Omit<CacheDropSample, "dump" | "inputTokens" | "cachedInputTokens"> & {
	role: "before" | "trigger" | "after" | "rolling";
	fileName: string;
	stats: { inputTokens: number; cachedInputTokens?: number };
	missing?: true;
};
interface Manifest {
	schema: "narrafork.cache-diagnostics.v1";
	kind: "rolling" | "event";
	narratorId: string;
	recordedAt: number;
	samples: SampleRef[];
	afterStatus?: "pending" | "captured" | "disabled" | "restart";
	comparison?: {
		previousRequestId: string;
		previousCachedInputTokens: number;
		cachedInputTokens: number;
	};
}
interface Group {
	name: string;
	manifest: Manifest;
}
interface Baseline {
	requestId: string;
	provider: string;
	model: string;
	conversationId?: string;
	contextEpoch?: number;
	cached: number;
}
interface Job {
	run: () => Promise<void>;
	cancel: () => void;
}
const queue: Job[] = [];
let running = false;
let retainedBytes = 0;

function warn(message: string, error?: unknown): void {
	// No logger import: this standalone store must not initialize the application's home/DB.
	console.warn(`[cache-diagnostics] ${message}`, error instanceof Error ? error.message : "");
}

async function drain(): Promise<void> {
	if (running) return;
	running = true;
	try {
		while (queue.length) await queue.shift()?.run();
	} finally {
		running = false;
	}
}

function enqueue(
	work: (signal: AbortSignal) => Promise<void>,
	timeoutMs: number,
	control = false,
	bytes = 0,
): Promise<void> {
	if (
		queue.length >= MAX_QUEUE ||
		(!control && retainedBytes + bytes > CACHE_DROP_MAX_PENDING_BYTES)
	) {
		warn("queue full or byte budget exceeded; diagnostic sample skipped");
		if (!control) return Promise.resolve();
		queue.pop()?.cancel();
	}
	retainedBytes += bytes;
	return new Promise((done) => {
		let started = false;
		let released = false;
		const release = () => {
			if (!released) retainedBytes -= bytes;
			released = true;
		};
		const controller = new AbortController();
		const timer = setTimeout(() => {
			warn("operation timed out; diagnostic work cancelled");
			job.cancel();
			const index = queue.indexOf(job);
			if (index !== -1) queue.splice(index, 1);
		}, timeoutMs);
		const job: Job = {
			cancel: () => {
				controller.abort();
				clearTimeout(timer);
				if (!started) release();
				done();
			},
			run: async () => {
				started = true;
				try {
					controller.signal.throwIfAborted();
					await work(controller.signal);
				} catch (error) {
					warn("operation skipped", error);
				} finally {
					clearTimeout(timer);
					release();
					done();
				}
			},
		};
		queue.push(job);
		// ponytail: one process-wide queue, at most 32 waiting snapshots. Uncancellable FS
		// calls retain the queue lock after timeout; never race a late write with cleanup.
		void drain();
	});
}

function sameIdentity(a: Baseline, b: Omit<CacheDropSample, "dump">): boolean {
	return (
		a.provider === b.provider &&
		a.model === b.model &&
		a.conversationId === b.conversationId &&
		a.contextEpoch === b.contextEpoch
	);
}

/** Private, disposable diagnostics; no settings, database, or API dependencies. */
export class CacheDropDumpStore {
	private readonly root: string;
	private readonly options: Required<Options>;
	private initialized = false;
	private generation = 0;
	private readonly groups = new Map<string, Group>();
	private readonly baselines = new Map<string, Baseline>();
	private readonly latestStartedAt = new Map<string, number>();
	private readonly resetFences = new Map<string, number>();

	constructor(root: string, options: Options = {}) {
		this.root = resolve(root);
		this.options = {
			maxBytes: options.maxBytes ?? CACHE_DROP_MAX_BYTES,
			maxFiles: options.maxFiles ?? CACHE_DROP_MAX_FILES,
			maxRequestBytes: options.maxRequestBytes ?? CACHE_DROP_MAX_REQUEST_BYTES,
			ttlMs: options.ttlMs ?? CACHE_DROP_TTL_MS,
			operationTimeoutMs: options.operationTimeoutMs ?? OPERATION_TIMEOUT_MS,
			now: options.now ?? Date.now,
		};
		for (const value of Object.values(this.options)) {
			if (typeof value === "number" && (!Number.isFinite(value) || value <= 0)) {
				throw new Error("Cache diagnostic budgets must be finite positive numbers");
			}
		}
	}

	async record(input: CacheDropSample): Promise<void> {
		const { dump: _dump, ...sample } = input;
		const { inputTokens, cachedInputTokens, ...meta } = sample;
		let json: string;
		let bytes: number;
		try {
			if (Buffer.byteLength(JSON.stringify(meta)) > MAX_METADATA_BYTES)
				throw new Error("metadata too large");
			// Serialize once before queueing; the queue holds only bounded strings, never dumps.
			json = JSON.stringify({ schema: "narrafork.cache-diagnostic-request.v1", ...input });
			bytes = Buffer.byteLength(json, "utf8");
			if (bytes > Math.min(this.options.maxRequestBytes, CACHE_DROP_MAX_REQUEST_BYTES))
				throw new Error(`request exceeds UTF-8 byte limit (${bytes})`);
		} catch (error) {
			warn("diagnostic sample rejected", error);
			return this.maintain();
		}
		const generation = this.generation;
		return this.execute(
			async (signal) => {
				if (generation !== this.generation) return;
				await this.prune(signal);
				if (
					[...this.groups.values()].some((g) =>
						g.manifest.samples.some(
							(s) => s.requestId === sample.requestId && s.narratorId === sample.narratorId,
						),
					)
				)
					return;
				const rollingName = `cd-r-${createHash("sha256").update(sample.narratorId).digest("hex")}`;
				const ref: SampleRef = {
					...meta,
					stats: { inputTokens, cachedInputTokens },
					role: "rolling",
					fileName: `s-${randomUUID()}.json`,
				};
				const manifest: Manifest = {
					schema: "narrafork.cache-diagnostics.v1",
					kind: "rolling",
					narratorId: sample.narratorId,
					recordedAt: this.options.now(),
					samples: [...(this.groups.get(rollingName)?.manifest.samples.slice(-2) ?? []), ref],
				};
				// Include the temporary manifest alongside the still-live old manifest/request.
				if (
					!(await this.reserve(
						bytes + Buffer.byteLength(JSON.stringify(manifest, null, 2)),
						2,
						signal,
					))
				)
					return;
				// Reservation may evict this narrator's old rolling group and comparison baseline.
				const rolling = this.groups.get(rollingName);
				const before = rolling?.manifest.samples.slice(-2) ?? [];
				manifest.samples = [...before, ref];
				await this.privateDirectory(join(this.root, rollingName));
				const samplePath = join(this.root, rollingName, ref.fileName);
				try {
					await writeFile(samplePath, json, { flag: "wx", mode: 0o600, signal });
					await this.writeManifest(rollingName, manifest, signal);
					this.groups.set(rollingName, { name: rollingName, manifest });
				} catch (error) {
					await rm(samplePath, { force: true });
					if (!rolling) await rm(join(this.root, rollingName), { recursive: true, force: true });
					throw error;
				}
				for (const old of rolling?.manifest.samples ?? []) {
					if (!before.includes(old))
						await rm(join(this.root, rollingName, old.fileName), { force: true });
				}
				const previous = this.baselines.get(sample.narratorId);
				const eligible =
					Number.isFinite(sample.startedAt) &&
					sample.startedAt >= this.options.now() - this.options.ttlMs &&
					sample.startedAt > (this.resetFences.get(sample.narratorId) ?? -Infinity) &&
					sample.startedAt >= (this.latestStartedAt.get(sample.narratorId) ?? -Infinity);
				if (eligible) {
					this.latestStartedAt.set(sample.narratorId, sample.startedAt);
					if (previous && !sameIdentity(previous, sample)) this.baselines.delete(sample.narratorId);
				}
				const valid =
					eligible &&
					sample.errorMessage == null &&
					Number.isFinite(cachedInputTokens) &&
					(cachedInputTokens ?? -1) >= 0 &&
					Number.isFinite(inputTokens) &&
					inputTokens >= 0;
				const trigger =
					valid &&
					previous &&
					sameIdentity(previous, sample) &&
					previous.cached >= CACHE_DROP_MIN_TOKENS &&
					previous.cached - (cachedInputTokens ?? 0) >= CACHE_DROP_MIN_TOKENS;
				if (valid)
					this.baselines.set(sample.narratorId, {
						requestId: sample.requestId,
						provider: sample.provider,
						model: sample.model,
						conversationId: sample.conversationId,
						contextEpoch: sample.contextEpoch,
						cached: cachedInputTokens as number,
					});
				try {
					for (const group of this.groups.values()) {
						if (
							group.manifest.narratorId === sample.narratorId &&
							group.manifest.afterStatus === "pending"
						) {
							await this.appendAfter(group, rollingName, ref, signal);
						}
					}
					if (trigger && previous) await this.freeze(rollingName, before, ref, previous, signal);
				} finally {
					await this.prune(signal);
				}
			},
			false,
			Math.max(bytes, json.length * 2),
		);
	}

	/** Explicit compact/history replacement invalidates comparison, not saved evidence. */
	async resetNarrator(narratorId: string): Promise<void> {
		const fence = this.options.now();
		return this.execute(async (signal) => {
			this.baselines.delete(narratorId);
			this.resetFences.set(narratorId, fence);
			await this.prune(signal);
		}, true);
	}

	/** Disable capture: cancel queued records, remove rolling only, retain frozen events. */
	async clearRolling(): Promise<void> {
		this.generation++;
		return this.execute(async (signal) => {
			this.baselines.clear();
			this.latestStartedAt.clear();
			this.resetFences.clear();
			for (const group of this.groups.values()) {
				if (group.manifest.kind === "rolling") await this.removeGroup(group, signal);
				else if (group.manifest.afterStatus === "pending") {
					const manifest = { ...group.manifest, afterStatus: "disabled" as const };
					await this.writeManifest(group.name, manifest, signal);
					group.manifest = manifest;
				}
			}
			await this.prune(signal);
		}, true);
	}

	/** Call at startup and every ten minutes so TTL also applies while idle. */
	async maintain(): Promise<void> {
		return this.execute((signal) => this.prune(signal), true);
	}

	private execute(
		work: (signal: AbortSignal) => Promise<void>,
		control: boolean,
		bytes = 0,
	): Promise<void> {
		return enqueue(
			async (signal) => {
				await this.initialize(signal);
				await work(signal);
			},
			this.options.operationTimeoutMs,
			control,
			bytes,
		);
	}

	private async privateDirectory(path: string): Promise<void> {
		await mkdir(path, { recursive: true, mode: 0o700 });
		const info = await lstat(path);
		if (!info.isDirectory() || info.isSymbolicLink())
			throw new Error("Unsafe diagnostics directory");
		await chmod(path, 0o700);
	}

	private async initialize(signal: AbortSignal): Promise<void> {
		await this.privateDirectory(this.root);
		if (this.initialized) return;
		this.groups.clear();
		this.baselines.clear();
		this.latestStartedAt.clear();
		this.resetFences.clear();
		const updates: Group[] = [];
		for await (const entry of await opendir(this.root)) {
			signal.throwIfAborted();
			if (!GROUP_NAME.test(entry.name) || !entry.isDirectory() || entry.isSymbolicLink()) continue;
			if (!entry.name.startsWith("cd-e-")) {
				await rm(join(this.root, entry.name), { recursive: true, force: true });
				continue;
			}
			try {
				const path = join(this.root, entry.name, "manifest.json");
				const info = await lstat(path);
				if (!info.isFile() || info.isSymbolicLink() || info.size > MAX_MANIFEST_BYTES)
					throw new Error("Unsafe diagnostics manifest");
				const manifest = JSON.parse(await readFile(path, { encoding: "utf8", signal })) as Manifest;
				if (
					manifest.schema !== "narrafork.cache-diagnostics.v1" ||
					manifest.kind !== "event" ||
					!Number.isFinite(manifest.recordedAt) ||
					!Array.isArray(manifest.samples) ||
					manifest.samples.length > 4 ||
					manifest.samples.some((s) => !SAMPLE_NAME.test(s.fileName))
				)
					throw new Error("Invalid diagnostics manifest");
				let changed = false;
				for (const sample of manifest.samples) {
					try {
						const file = await lstat(join(this.root, entry.name, sample.fileName));
						if (!file.isFile() || file.isSymbolicLink())
							throw new Error("Unsafe diagnostic sample");
					} catch {
						sample.missing = true;
						changed = true;
					}
				}
				if (manifest.afterStatus === "pending") {
					manifest.afterStatus = "restart";
					changed = true;
				}
				const group = { name: entry.name, manifest };
				this.groups.set(entry.name, group);
				if (changed) updates.push(group);
			} catch (error) {
				signal.throwIfAborted();
				warn("unreadable event manifest retained; budget/TTL still apply", error);
				// Keep the original evidence untouched, but include its files in global eviction.
				const info = await lstat(join(this.root, entry.name));
				this.groups.set(entry.name, {
					name: entry.name,
					manifest: {
						schema: "narrafork.cache-diagnostics.v1",
						kind: "event",
						narratorId: "",
						recordedAt: info.mtimeMs,
						samples: [],
					},
				});
			}
		}
		this.initialized = true;
		for (const group of updates) {
			if (!this.groups.has(group.name)) continue;
			try {
				await this.writeManifest(group.name, group.manifest, signal);
			} catch (error) {
				signal.throwIfAborted();
				warn("event recovery annotation skipped; original evidence retained", error);
			}
		}
	}

	private async writeManifest(
		name: string,
		manifest: Manifest,
		signal: AbortSignal,
	): Promise<void> {
		signal.throwIfAborted();
		const directory = join(this.root, name);
		const info = await lstat(directory);
		if (!info.isDirectory() || info.isSymbolicLink()) throw new Error("Unsafe diagnostics group");
		const json = JSON.stringify(manifest, null, 2);
		if (!(await this.reserve(Buffer.byteLength(json), 1, signal, new Set([name]))))
			throw new Error("No room for atomic diagnostic manifest update");
		const temp = join(directory, `m-${randomUUID()}.tmp`);
		try {
			await writeFile(temp, json, { mode: 0o600, flag: "wx", signal });
			signal.throwIfAborted();
			await rename(temp, join(directory, "manifest.json"));
		} finally {
			await rm(temp, { force: true });
		}
	}

	private async freeze(
		source: string,
		before: SampleRef[],
		ref: SampleRef,
		previous: Baseline,
		signal: AbortSignal,
	): Promise<void> {
		const id = randomUUID();
		const temp = `cd-t-${id}`;
		const name = `cd-e-${id}`;
		const samples: SampleRef[] = [
			...before.map((s): SampleRef => ({ ...s, role: "before" })),
			{ ...ref, role: "trigger" },
		];
		const manifest: Manifest = {
			schema: "narrafork.cache-diagnostics.v1",
			kind: "event",
			narratorId: ref.narratorId,
			recordedAt: this.options.now(),
			samples,
			afterStatus: "pending",
			comparison: {
				previousRequestId: previous.requestId,
				previousCachedInputTokens: previous.cached,
				cachedInputTokens: ref.stats.cachedInputTokens as number,
			},
		};
		let allocation = Buffer.byteLength(JSON.stringify(manifest, null, 2));
		for (const sample of samples)
			allocation += (await lstat(join(this.root, source, sample.fileName))).size;
		if (!(await this.reserve(allocation, samples.length + 1, signal, new Set([source])))) return;
		await this.privateDirectory(join(this.root, temp));
		try {
			for (const sample of samples) {
				signal.throwIfAborted();
				await link(
					join(this.root, source, sample.fileName),
					join(this.root, temp, sample.fileName),
				);
			}
			await this.writeManifest(temp, manifest, signal);
			signal.throwIfAborted();
			await rename(join(this.root, temp), join(this.root, name));
			this.groups.set(name, { name, manifest });
			console.info("[cache-diagnostics] cache drop recorded", {
				directory: join(this.root, name),
				narratorId: ref.narratorId,
				requestId: ref.requestId,
				previousCachedInputTokens: previous.cached,
				cachedInputTokens: ref.stats.cachedInputTokens,
			});
		} finally {
			await rm(join(this.root, temp), { recursive: true, force: true });
		}
	}

	private async appendAfter(
		group: Group,
		source: string,
		ref: SampleRef,
		signal: AbortSignal,
	): Promise<void> {
		signal.throwIfAborted();
		const target = join(this.root, group.name, ref.fileName);
		const manifest: Manifest = {
			...group.manifest,
			samples: [...group.manifest.samples, { ...ref, role: "after" }],
			afterStatus: "captured",
		};
		const allocation =
			(await lstat(join(this.root, source, ref.fileName))).size +
			Buffer.byteLength(JSON.stringify(manifest, null, 2));
		if (!(await this.reserve(allocation, 2, signal, new Set([source, group.name])))) return;
		await link(join(this.root, source, ref.fileName), target);
		try {
			await this.writeManifest(group.name, manifest, signal);
			group.manifest = manifest;
		} catch (error) {
			await rm(target, { force: true });
			throw error;
		}
	}

	private async removeGroup(group: Group, signal: AbortSignal): Promise<void> {
		signal.throwIfAborted();
		// Rename first: a crash/partial recursive removal cannot leave a visible broken event.
		const trash = join(this.root, `cd-t-${randomUUID()}`);
		await rename(join(this.root, group.name), trash);
		this.groups.delete(group.name);
		if (group.manifest.kind === "rolling") {
			this.baselines.delete(group.manifest.narratorId);
			this.latestStartedAt.delete(group.manifest.narratorId);
		}
		await rm(trash, { recursive: true, force: true });
	}

	private async reserve(
		extraBytes: number,
		extraFiles: number,
		signal: AbortSignal,
		protectedNames = new Set<string>(),
	): Promise<boolean> {
		if (extraBytes > this.options.maxBytes || extraFiles > this.options.maxFiles) {
			warn("diagnostic write cannot fit the budget; existing evidence retained");
			return false;
		}
		let bytes = extraBytes;
		let files = extraFiles;
		const sizes = new Map<string, { bytes: number; files: number }>();
		// Include incomplete/staging groups and temporary manifests, not only published groups.
		// ponytail: count hardlinks twice and reserve the whole small write before starting it;
		// this sacrifices a little capacity to keep peak usage bounded without a disk index.
		for await (const entry of await opendir(this.root)) {
			signal.throwIfAborted();
			if (!GROUP_NAME.test(entry.name) || !entry.isDirectory() || entry.isSymbolicLink()) continue;
			const size = { bytes: 0, files: 0 };
			for await (const file of await opendir(join(this.root, entry.name))) {
				signal.throwIfAborted();
				size.bytes += (await lstat(join(this.root, entry.name, file.name))).size;
				size.files++;
			}
			sizes.set(entry.name, size);
			bytes += size.bytes;
			files += size.files;
		}
		const victims: Group[] = [];
		for (const group of [...this.groups.values()].sort(
			(a, b) => a.manifest.recordedAt - b.manifest.recordedAt,
		)) {
			if (bytes <= this.options.maxBytes && files <= this.options.maxFiles) break;
			const size = sizes.get(group.name);
			if (!size || protectedNames.has(group.name)) continue;
			victims.push(group);
			bytes -= size.bytes;
			files -= size.files;
		}
		if (bytes > this.options.maxBytes || files > this.options.maxFiles) {
			warn("diagnostic write cannot fit protected evidence; write skipped");
			return false;
		}
		// Plan every eviction before deleting anything. An impossible write must not clear
		// old evidence; a failed deletion must stop the write, not pretend space was freed.
		for (const group of victims) await this.removeGroup(group, signal);
		return true;
	}

	private async prune(signal: AbortSignal): Promise<void> {
		for await (const entry of await opendir(this.root)) {
			signal.throwIfAborted();
			if (
				GROUP_NAME.test(entry.name) &&
				entry.name.startsWith("cd-t-") &&
				entry.isDirectory() &&
				!entry.isSymbolicLink()
			) {
				await rm(join(this.root, entry.name), { recursive: true, force: true });
			}
		}
		for (const [narratorId, fence] of this.resetFences) {
			if (fence < this.options.now() - this.options.ttlMs) this.resetFences.delete(narratorId);
		}
		const groups = [...this.groups.values()].sort(
			(a, b) => a.manifest.recordedAt - b.manifest.recordedAt,
		);
		let bytes = 0;
		let files = 0;
		const sizes = new Map<string, { bytes: number; files: number }>();
		for (const group of groups) {
			signal.throwIfAborted();
			if (this.options.now() - group.manifest.recordedAt >= this.options.ttlMs) {
				await this.removeGroup(group, signal);
				continue;
			}
			const directory = join(this.root, group.name);
			const info = await lstat(directory);
			if (!info.isDirectory() || info.isSymbolicLink()) throw new Error("Unsafe diagnostics group");
			let groupBytes = 0;
			let groupFiles = 0;
			for await (const entry of await opendir(directory)) {
				signal.throwIfAborted();
				const file = await lstat(join(directory, entry.name));
				groupBytes += file.size;
				groupFiles++;
			}
			// ponytail: count hardlinks conservatively as separate bytes/files. At 256 files,
			// a metadata-only scan is bounded; use a disk index only if that ceiling grows.
			sizes.set(group.name, { bytes: groupBytes, files: groupFiles });
			bytes += groupBytes;
			files += groupFiles;
		}
		for (const group of groups) {
			if (bytes <= this.options.maxBytes && files <= this.options.maxFiles) break;
			const size = sizes.get(group.name);
			if (!size) continue;
			await this.removeGroup(group, signal);
			bytes -= size.bytes;
			files -= size.files;
		}
	}
}
