import { type BigIntStats, constants } from "node:fs";
import { lstat, open } from "node:fs/promises";
import { isAbsolute, resolve, sep } from "node:path";
import { parentPort } from "node:worker_threads";
import type { GitLineStatsFile } from "../../shared/git-line-stats";

export const UNTRACKED_LINE_LIMITS = {
	files: 200,
	fileBytes: 1_000_000,
	totalBytes: 20_000_000,
	cacheEntries: 512,
} as const;

export interface UntrackedLineBatch {
	files: GitLineStatsFile[];
	complete: boolean;
	/** Internal diagnostic, also used to verify that cache hits do not read contents. */
	readFiles: number;
}

interface Measurement {
	lines?: number;
	complete: boolean;
}

function version(stat: BigIntStats): string {
	return `${stat.dev}:${stat.ino}:${stat.size}:${stat.mtimeNs}:${stat.ctimeNs}`;
}

/** Used only inside the worker in production. No database/settings dependency. */
export class UntrackedLineReader {
	private readonly cache = new Map<string, { version: string; value: Measurement }>();

	async measure(cwd: string, paths: string[], signal: AbortSignal): Promise<UntrackedLineBatch> {
		const root = resolve(cwd);
		const files: GitLineStatsFile[] = [];
		let remaining = UNTRACKED_LINE_LIMITS.totalBytes as number;
		let readFiles = 0;
		let complete = paths.length <= UNTRACKED_LINE_LIMITS.files;
		for (const path of paths.slice(0, UNTRACKED_LINE_LIMITS.files)) {
			signal.throwIfAborted();
			let value: Measurement = { complete: false };
			const absolute = resolve(root, path);
			if (
				path &&
				!isAbsolute(path) &&
				!path.includes("\0") &&
				absolute.startsWith(root.endsWith(sep) ? root : `${root}${sep}`)
			) {
				try {
					const stat = await lstat(absolute, { bigint: true });
					if (stat.isFile() && stat.size <= BigInt(UNTRACKED_LINE_LIMITS.fileBytes)) {
						const stamp = version(stat);
						const cached = this.cache.get(absolute);
						if (cached?.version === stamp) {
							value = cached.value;
							// A tiny LRU: keep only metadata and counts, never file contents.
							this.cache.delete(absolute);
							this.cache.set(absolute, cached);
						} else if (stat.size <= BigInt(remaining)) {
							remaining -= Number(stat.size);
							readFiles++;
							value = await this.read(absolute, stat, signal);
							if (value.complete) {
								this.cache.delete(absolute);
								this.cache.set(absolute, { version: stamp, value });
								if (this.cache.size > UNTRACKED_LINE_LIMITS.cacheEntries) {
									const oldest = this.cache.keys().next().value;
									if (oldest !== undefined) this.cache.delete(oldest);
								}
							}
						}
					}
				} catch {
					// Disappearing/unreadable/nonregular paths are unknown, not empty files.
					signal.throwIfAborted();
				}
			}
			complete &&= value.complete;
			files.push({
				path,
				unstagedLinesAdded: value.lines,
				unstagedLinesRemoved: value.complete ? 0 : undefined,
				complete: value.complete,
			});
		}
		signal.throwIfAborted();
		return { files, complete, readFiles };
	}

	private async read(
		path: string,
		expected: BigIntStats,
		signal: AbortSignal,
	): Promise<Measurement> {
		const file = await open(
			path,
			constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0) | (constants.O_NONBLOCK ?? 0),
		);
		try {
			const before = await file.stat({ bigint: true });
			if (!before.isFile() || version(before) !== version(expected)) return { complete: false };
			const buffer = Buffer.allocUnsafe(64 * 1024);
			let bytes = 0;
			let lines = 0;
			let lastByte = -1;
			while (bytes < Number(expected.size)) {
				signal.throwIfAborted();
				const { bytesRead } = await file.read(
					buffer,
					0,
					Math.min(buffer.length, Number(expected.size) - bytes),
					null,
				);
				if (!bytesRead) return { complete: false };
				// Like Git, sample only the leading 8 KiB for binary NULs.
				const sample = Math.min(bytesRead, Math.max(0, 8192 - bytes));
				if (buffer.subarray(0, sample).includes(0)) {
					signal.throwIfAborted();
					return version(await file.stat({ bigint: true })) === version(expected)
						? { lines: 0, complete: true }
						: { complete: false };
				}
				for (let i = 0; i < bytesRead; i++) if (buffer[i] === 10) lines++;
				bytes += bytesRead;
				lastByte = buffer[bytesRead - 1];
			}
			signal.throwIfAborted();
			if (version(await file.stat({ bigint: true })) !== version(expected))
				return { complete: false };
			return { lines: lines + (bytes > 0 && lastByte !== 10 ? 1 : 0), complete: true };
		} finally {
			await file.close();
		}
	}
}

const port = parentPort;
if (port) {
	const reader = new UntrackedLineReader();
	let active: AbortController | undefined;
	port.on(
		"message",
		async (
			message:
				| { action: "read"; cwd: string; paths: string[] }
				| { action: "cancel" }
				| { action: "ping" },
		) => {
			if (message.action === "ping") {
				port.postMessage({ ready: true });
				return;
			}
			if (message.action === "cancel") {
				active?.abort();
				return;
			}
			if (active) {
				port.postMessage({ error: "git_line_stats_reader_busy" });
				return;
			}
			const controller = new AbortController();
			active = controller;
			try {
				const value = await reader.measure(message.cwd, message.paths, controller.signal);
				port.postMessage({ value });
			} catch (error) {
				port.postMessage({
					error: error instanceof Error ? error.message : "git_line_stats_read_failed",
				});
			} finally {
				active = undefined;
			}
		},
	);
}
