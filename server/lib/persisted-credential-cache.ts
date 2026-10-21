import { createHmac, randomBytes } from "node:crypto";
import { existsSync, readFileSync, statSync } from "node:fs";
import { mkdir, rename, writeFile } from "node:fs/promises";
import { dirname } from "node:path";
import { getNarraforkPath } from "./narrafork-home";

/**
 * Disk persistence for per-provider balance / allowance caches.
 *
 * Those caches used to be memory-only because a balance belongs to one credential
 * identity: restoring it after a restart under a different API key, base URL or
 * proxy would show another account's numbers. This store keeps that guarantee
 * by saving each value next to a keyed fingerprint of the identity and only
 * handing it back when the live identity produces the same fingerprint.
 *
 * - The identity string itself (which contains the API key) is never written;
 *   only an HMAC of it, keyed by a random per-file salt.
 * - Values are re-validated by the caller's `parse` on load, so a corrupt or
 *   hand-edited file degrades to "no cache", never to a crash.
 * - The file is small and bounded ({@link MAX_ENTRIES}, {@link MAX_FILE_BYTES});
 *   it is read once lazily and written asynchronously with a debounce.
 */

const SAVE_DEBOUNCE_MS = 250;
const MAX_FILE_BYTES = 1024 * 1024;
const MAX_ENTRIES = 256;

interface StoredEntry {
	fingerprint: string;
	savedAt: number;
	value: unknown;
}

export interface PersistedCredentialCache<T> {
	/** The persisted value for `key`, only when it was saved under the same identity. */
	restore(key: string, identity: string): T | undefined;
	save(key: string, identity: string, value: T): void;
	remove(key: string): void;
	/** Wait for pending writes; for tests and shutdown paths. */
	flush(): Promise<void>;
}

export function createPersistedCredentialCache<T>(
	fileName: string,
	parse: (raw: unknown) => T | null,
): PersistedCredentialCache<T> {
	let loaded = false;
	let salt = "";
	let entries = new Map<string, StoredEntry>();
	let saveTimer: ReturnType<typeof setTimeout> | null = null;
	let saveInFlight: Promise<void> | null = null;
	let saveRequested = false;

	const filePath = () => getNarraforkPath(fileName);

	function load(): void {
		if (loaded) return;
		loaded = true;
		try {
			const path = filePath();
			if (existsSync(path) && statSync(path).size <= MAX_FILE_BYTES) {
				const data = JSON.parse(readFileSync(path, "utf-8")) as {
					version?: unknown;
					salt?: unknown;
					entries?: unknown;
				};
				if (
					data.version === 1 &&
					typeof data.salt === "string" &&
					/^[a-f0-9]{32,128}$/.test(data.salt) &&
					data.entries &&
					typeof data.entries === "object"
				) {
					salt = data.salt;
					for (const [key, raw] of Object.entries(data.entries as Record<string, unknown>)) {
						if (entries.size >= MAX_ENTRIES) break;
						if (!raw || typeof raw !== "object") continue;
						const entry = raw as Partial<StoredEntry>;
						if (typeof entry.fingerprint !== "string") continue;
						entries.set(key, {
							fingerprint: entry.fingerprint,
							savedAt: typeof entry.savedAt === "number" ? entry.savedAt : 0,
							value: entry.value,
						});
					}
				}
			}
		} catch {
			// Corrupt or unreadable cache: start empty, the next refresh rebuilds it.
			entries = new Map();
		}
		if (!salt) salt = randomBytes(32).toString("hex");
	}

	function fingerprint(identity: string): string {
		return createHmac("sha256", salt).update(identity).digest("hex");
	}

	function serialize(): string {
		return JSON.stringify({ version: 1, salt, entries: Object.fromEntries(entries) });
	}

	async function flushLoop(): Promise<void> {
		while (saveRequested) {
			saveRequested = false;
			try {
				const path = filePath();
				const tmp = `${path}.${process.pid}.tmp`;
				await mkdir(dirname(path), { recursive: true });
				await writeFile(tmp, serialize(), { mode: 0o600 });
				await rename(tmp, path);
			} catch {
				// Non-critical: a failed write only costs the cache after a restart.
			}
		}
	}

	function scheduleSave(): void {
		saveRequested = true;
		if (saveTimer || saveInFlight) return;
		saveTimer = setTimeout(() => {
			saveTimer = null;
			saveInFlight = flushLoop().finally(() => {
				saveInFlight = null;
				if (saveRequested) scheduleSave();
			});
		}, SAVE_DEBOUNCE_MS);
		saveTimer.unref?.();
	}

	return {
		restore(key, identity) {
			load();
			const entry = entries.get(key);
			if (!entry || entry.fingerprint !== fingerprint(identity)) return undefined;
			try {
				return parse(entry.value) ?? undefined;
			} catch {
				return undefined;
			}
		},
		save(key, identity, value) {
			load();
			if (!entries.has(key) && entries.size >= MAX_ENTRIES) {
				// Evict the oldest entry rather than growing without bound.
				let oldestKey: string | undefined;
				let oldest = Number.POSITIVE_INFINITY;
				for (const [k, e] of entries) {
					if (e.savedAt < oldest) {
						oldest = e.savedAt;
						oldestKey = k;
					}
				}
				if (oldestKey !== undefined) entries.delete(oldestKey);
			}
			entries.set(key, { fingerprint: fingerprint(identity), savedAt: Date.now(), value });
			scheduleSave();
		},
		remove(key) {
			load();
			if (entries.delete(key)) scheduleSave();
		},
		async flush() {
			if (saveTimer) {
				clearTimeout(saveTimer);
				saveTimer = null;
				saveInFlight = flushLoop().finally(() => {
					saveInFlight = null;
				});
			}
			while (saveInFlight) await saveInFlight;
		},
	};
}
