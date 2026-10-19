import { lstat, realpath } from "node:fs/promises";
import { isAbsolute, relative, resolve, sep } from "node:path";
import { normalizePathForOS } from "../lib/platform-path";
import { safeSpawn } from "../lib/spawn";

const MAX_PATHS = 200;
const MAX_PATH_LENGTH = 4096;
const CONCURRENCY = 10;
const BUDGET_MS = 500;
/** Bound even abandoned/unwatched directories whose uncancellable I/O never settles. */
const MAX_INFLIGHT_PROBES = 128;
const inFlight = new Set<string>();

interface MetadataStatus {
	files?: { path: string; oldPath?: string }[];
	totalFiles: number;
	totalFilesExact?: boolean;
	truncated?: boolean;
}

export interface WatcherMetadata {
	fingerprint: string;
	uncertain: boolean;
}

/** Metadata only: never read file contents or ask git for a diff. */
export const worktreeWatcherMetadata = {
	async probe(
		worktreePath: string,
		status: MetadataStatus,
		{ budgetMs = BUDGET_MS }: { budgetMs?: number } = {},
	): Promise<WatcherMetadata> {
		const uncertainResult: WatcherMetadata = { fingerprint: "", uncertain: true };
		// This must be lexical: realpath itself may hang. Preserve POSIX backslashes.
		const key = normalizePathForOS(resolve(worktreePath), process.platform);
		if (inFlight.has(key) || inFlight.size >= MAX_INFLIGHT_PROBES) return uncertainResult;
		inFlight.add(key);
		const controller = new AbortController();
		const deadline = performance.now() + budgetMs;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const timeout = new Promise<WatcherMetadata>((resolveTimeout) => {
			timer = setTimeout(
				() => {
					controller.abort();
					resolveTimeout(uncertainResult);
				},
				Math.max(0, budgetMs),
			);
		});
		const inspect = async (): Promise<WatcherMetadata> => {
			if (performance.now() >= deadline) return uncertainResult;
			// Status paths are relative to the queried CWD, not the repository root.
			// Canonicalize aliases within this same deadline before root containment checks.
			const cwd = await realpath(worktreePath);
			if (controller.signal.aborted || performance.now() >= deadline) return uncertainResult;
			// Git resolves both worktree .git files and calls made from a subdirectory.
			// Both paths are absolute; ambiguous newline-containing paths fail closed.
			const git = await safeSpawn({
				cmd: [
					"git",
					"--no-optional-locks",
					"rev-parse",
					"--path-format=absolute",
					"--show-toplevel",
					"--git-path",
					"index",
				],
				cwd,
				timeout: Math.max(1, Math.ceil(deadline - performance.now())),
				signal: controller.signal,
				maxOutputBytes: 2 * MAX_PATH_LENGTH + 2,
			});
			if (git.exitCode !== 0 || git.stdoutTruncated || git.stderrTruncated || git.terminationReason)
				return uncertainResult;
			const paths = git.stdout.replace(/\r?\n$/, "").split("\n");
			if (paths.length !== 2 || paths.some((path) => !isAbsolute(path))) return uncertainResult;
			const [root, indexPath] = paths as [string, string];
			const files = status.files ?? [];
			let uncertain =
				status.truncated === true ||
				status.totalFilesExact === false ||
				status.totalFiles > files.length ||
				files.length > MAX_PATHS;
			const selected = new Set<string>();
			for (const file of files.slice(0, MAX_PATHS)) {
				for (const path of [file.path, file.oldPath]) {
					if (path === undefined) continue;
					if (!path || path.length > MAX_PATH_LENGTH || isAbsolute(path)) {
						uncertain = true;
						continue;
					}
					const absolute = resolve(cwd, path);
					const rel = relative(root, absolute);
					if (!rel || rel === ".." || rel.startsWith(`..${sep}`) || isAbsolute(rel)) {
						uncertain = true;
						continue;
					}
					if (selected.size >= MAX_PATHS && !selected.has(absolute)) {
						uncertain = true;
						continue;
					}
					selected.add(absolute);
				}
			}
			const targets = [indexPath, ...[...selected].sort()];
			const records = new Array<string>(targets.length);
			let next = 0;
			await Promise.all(
				Array.from({ length: Math.min(CONCURRENCY, targets.length) }, async () => {
					while (next < targets.length && !controller.signal.aborted) {
						if (performance.now() >= deadline) {
							uncertain = true;
							return;
						}
						const position = next++;
						const path = targets[position];
						try {
							const stat = await lstat(path, { bigint: true });
							// A directory (e.g. a dirty submodule) cannot describe descendant edits.
							if (stat.isDirectory()) uncertain = true;
							records[position] = `${stat.size}:${stat.mtimeNs}:${stat.ctimeNs}:${stat.ino}`;
						} catch (error) {
							if ((error as NodeJS.ErrnoException).code !== "ENOENT") uncertain = true;
							records[position] = "missing";
						}
					}
				}),
			);
			if (controller.signal.aborted || performance.now() >= deadline) return uncertainResult;
			return { fingerprint: JSON.stringify([targets, records]), uncertain };
		};
		// A timeout releases the caller, NOT the inspection slot. Later polls return
		// uncertain until every original syscall/subprocess really finishes; they must
		// not pile fresh realpath/lstat work onto a hung filesystem. Handle late errors.
		const inspection = inspect()
			.catch(() => uncertainResult)
			.finally(() => inFlight.delete(key));
		try {
			return await Promise.race([inspection, timeout]);
		} finally {
			if (timer) clearTimeout(timer);
			// lstat cannot be cancelled; stop scheduling more after this bounded batch.
			controller.abort();
		}
	},
};
