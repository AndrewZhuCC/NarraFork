/**
 * Bounded directory walk used by the storage scan.
 *
 * The same function runs inside the filesystem worker and, if that worker cannot
 * be spawned, on the main thread. Either way it is async: one `lstat` at a time,
 * so a worker can observe cancellation between entries and the main-thread
 * fallback does not freeze the event loop the way synchronous sqlite does.
 *
 * Symlinks are not followed. A directory link is reported as a directory by
 * `readdir` when its target is a directory, so the walk identifies links with
 * `lstat` and skips them. Otherwise a self-referential link recurses forever.
 */

import type { Dirent } from "node:fs";
import { existsSync } from "node:fs";
import { lstat, readdir } from "node:fs/promises";
import { resolve } from "node:path";

/**
 * Entry budget for one measurement call.
 *
 * A checked-out worktree of a large monorepo is a few hundred thousand files.
 * 2,000,000 leaves headroom and still bounds a pathological tree.
 */
export const DIR_SCAN_MAX_ENTRIES = 2_000_000;

/**
 * Depth budget. Real trees measured here are well under 30 levels; 64 is
 * generous for a nested source tree and far from any stack limit.
 */
export const DIR_SCAN_MAX_DEPTH = 64;

const PROGRESS_EVERY_ENTRIES = 200;

export interface DirectoryTreeMeasurement {
	sizeBytes: number;
	entriesScanned: number;
	truncated: boolean;
}

export class DirectoryMeasureAbortedError extends Error {
	constructor() {
		super("directory measure aborted");
		this.name = "DirectoryMeasureAbortedError";
	}
}

export interface MeasureDirectoryTreesOptions {
	maxEntries?: number;
	maxDepth?: number;
	signal?: AbortSignal;
	onProgress?: (progress: { entriesScanned: number; sizeBytes: number }) => void;
}

/**
 * Measure each root independently for depth, and share one entry budget across
 * the whole call. Roots are walked one after another inside this call; callers
 * that want parallelism run separate calls.
 */
export async function measureDirectoryTrees(
	roots: string[],
	options: MeasureDirectoryTreesOptions = {},
): Promise<DirectoryTreeMeasurement[]> {
	const maxEntries = options.maxEntries ?? DIR_SCAN_MAX_ENTRIES;
	const maxDepth = options.maxDepth ?? DIR_SCAN_MAX_DEPTH;
	const signal = options.signal;
	let entriesScanned = 0;
	let sizeBytes = 0;
	let truncated = false;
	// Depth stops one branch. The entry budget stops the whole call. Mixing them
	// made a deep child abandon siblings that were still inside the budget, so a
	// file next to a too-deep directory disappeared from the total.
	let budgetExhausted = false;
	let sinceProgress = 0;

	const report = (force = false) => {
		if (!force && sinceProgress < PROGRESS_EVERY_ENTRIES) return;
		sinceProgress = 0;
		options.onProgress?.({ entriesScanned, sizeBytes });
	};

	const walk = async (
		current: string,
		depth: number,
		acc: { sizeBytes: number },
	): Promise<void> => {
		if (signal?.aborted) throw new DirectoryMeasureAbortedError();
		if (budgetExhausted) return;
		if (depth > maxDepth) {
			truncated = true;
			return;
		}
		let entries: Dirent[];
		try {
			entries = await readdir(current, { withFileTypes: true });
		} catch {
			return;
		}
		for (const entry of entries) {
			if (budgetExhausted) return;
			if (signal?.aborted) throw new DirectoryMeasureAbortedError();
			if (entriesScanned >= maxEntries) {
				truncated = true;
				budgetExhausted = true;
				return;
			}
			entriesScanned++;
			sinceProgress++;
			report();
			const full = resolve(current, entry.name);
			try {
				// lstat, not stat: a symlink must stay a link even when it points at a directory.
				const info = await lstat(full);
				if (info.isSymbolicLink()) continue;
				if (info.isDirectory()) await walk(full, depth + 1, acc);
				else if (info.isFile()) {
					acc.sizeBytes += info.size;
					sizeBytes += info.size;
				}
			} catch {
				// skip inaccessible entries
			}
		}
	};

	const results: DirectoryTreeMeasurement[] = [];
	for (const root of roots) {
		if (signal?.aborted) throw new DirectoryMeasureAbortedError();
		if (!existsSync(root)) {
			results.push({ sizeBytes: 0, entriesScanned: 0, truncated: false });
			continue;
		}
		if (budgetExhausted) {
			results.push({ sizeBytes: 0, entriesScanned: 0, truncated: true });
			continue;
		}
		const entriesBefore = entriesScanned;
		const truncatedBefore = truncated;
		const acc = { sizeBytes: 0 };
		await walk(root, 0, acc);
		results.push({
			sizeBytes: acc.sizeBytes,
			entriesScanned: entriesScanned - entriesBefore,
			truncated: truncated && !truncatedBefore,
		});
	}
	report(true);
	return results;
}
