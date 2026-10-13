import { stat } from "node:fs/promises";
import { resolve } from "node:path";
import { MAX_PROJECT_INSTRUCTIONS_BYTES, readFileCapped } from "./read-file-capped";

type InstructionFile = NonNullable<Awaited<ReturnType<typeof readFileCapped>>>;
interface FileFacts {
	size: number;
	mtimeMs: number;
	ctimeMs: number;
	ino: number;
	dev: number;
	isFile(): boolean;
}
interface CacheEntry {
	signature: string;
	bytes: number;
	expiresAt: number;
	value?: InstructionFile;
	pending?: Promise<InstructionFile | null>;
}
interface CacheOptions {
	maxEntries?: number;
	maxBytes?: number;
	ttlMs?: number;
	now?: () => number;
	statFile?: (path: string) => Promise<FileFacts>;
	readFile?: typeof readFileCapped;
}

function signature(facts: FileFacts): string {
	return `${facts.dev}:${facts.ino}:${facts.size}:${facts.mtimeMs}:${facts.ctimeMs}`;
}

/** Cache static instruction bytes only, never an assembled prompt or an authorization decision. */
export class InstructionFileCache {
	private readonly entries = new Map<string, CacheEntry>();
	private readonly maxEntries: number;
	private readonly maxBytes: number;
	private readonly ttlMs: number;
	private readonly now: () => number;
	private readonly statFile: (path: string) => Promise<FileFacts>;
	private readonly readFile: typeof readFileCapped;
	private bytes = 0;

	constructor(options: CacheOptions = {}) {
		this.maxEntries = options.maxEntries ?? 128;
		this.maxBytes = options.maxBytes ?? 8 * 1024 * 1024;
		this.ttlMs = options.ttlMs ?? 60_000;
		this.now = options.now ?? Date.now;
		this.statFile = options.statFile ?? stat;
		this.readFile = options.readFile ?? readFileCapped;
	}

	private remove(key: string): void {
		const entry = this.entries.get(key);
		if (!entry) return;
		this.entries.delete(key);
		this.bytes -= entry.bytes;
	}

	private touch(key: string, entry: CacheEntry): void {
		this.entries.delete(key);
		this.entries.set(key, entry);
	}

	async read(
		path: string,
		maxBytes = MAX_PROJECT_INSTRUCTIONS_BYTES,
	): Promise<InstructionFile | null> {
		if (
			!Number.isSafeInteger(maxBytes) ||
			maxBytes < 0 ||
			maxBytes > MAX_PROJECT_INSTRUCTIONS_BYTES
		)
			throw new RangeError("Instruction read exceeds the capped file budget");
		const absolutePath = resolve(path);
		const key = JSON.stringify([absolutePath, maxBytes]);
		let facts: FileFacts;
		try {
			facts = await this.statFile(absolutePath);
		} catch {
			this.remove(key);
			return null;
		}
		if (!facts.isFile()) {
			this.remove(key);
			return null;
		}
		const observed = signature(facts);
		const cached = this.entries.get(key);
		if (cached?.signature === observed) {
			if (cached.value && cached.expiresAt > this.now()) {
				this.touch(key, cached);
				return { ...cached.value };
			}
			if (cached.pending) {
				const value = await cached.pending;
				return value ? { ...value } : null;
			}
		}
		this.remove(key);
		// Reserve decoded-string space before starting an in-flight read. Pending reads
		// count towards both limits, and never leave an unbounded promise map behind.
		const reservation = Math.min(facts.size, maxBytes) * 2 + 256;
		for (const [oldKey, old] of this.entries) {
			if (this.entries.size < this.maxEntries && this.bytes + reservation <= this.maxBytes) break;
			if (!old.pending) this.remove(oldKey);
		}
		const retain = this.entries.size < this.maxEntries && this.bytes + reservation <= this.maxBytes;
		const entry: CacheEntry = { signature: observed, bytes: reservation, expiresAt: 0 };
		if (retain) {
			this.entries.set(key, entry);
			this.bytes += reservation;
		}
		const pending = this.readFile(absolutePath, maxBytes).then(async (value) => {
			// An atomic replacement/change during the read cannot label new bytes with
			// the old signature. Return the capped read, but do not retain the race.
			const after = await this.statFile(absolutePath).catch(() => null);
			if (this.entries.get(key) === entry) {
				if (value && after?.isFile() && signature(after) === observed) {
					entry.value = { ...value };
					entry.expiresAt = this.now() + this.ttlMs;
				} else this.remove(key);
			}
			return value;
		});
		entry.pending = pending;
		try {
			const value = await pending;
			return value ? { ...value } : null;
		} catch (error) {
			if (this.entries.get(key) === entry) this.remove(key);
			throw error;
		} finally {
			entry.pending = undefined;
		}
	}

	clear(): void {
		this.entries.clear();
		this.bytes = 0;
	}

	stats(): { entries: number; bytes: number } {
		return { entries: this.entries.size, bytes: this.bytes };
	}
}

const instructionFileCache = new InstructionFileCache();
export const readInstructionFileCached = (
	path: string,
	maxBytes = MAX_PROJECT_INSTRUCTIONS_BYTES,
) => instructionFileCache.read(path, maxBytes);
