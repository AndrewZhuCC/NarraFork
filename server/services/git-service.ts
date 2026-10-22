import { AsyncLocalStorage } from "node:async_hooks";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	GIT_COMMIT_PREVIEW_PATCH_MAX_BYTES,
	GIT_COMMIT_SHA_PATTERN,
	type GitCommitDetail,
	type GitCommitPatch,
} from "@shared/git-commit-preview";
import {
	GIT_WORKSPACE_MAX_BYTES,
	GIT_WORKSPACE_TIMEOUT_MS,
	GIT_WORKSPACE_WRITE_TIMEOUT_MS,
} from "../lib/agent/execution/git-workspace-rpc";
import { worktreeLock } from "../lib/async-mutex";
import { AppError, GitAuthError, GitError, UnbornHeadError, ValidationError } from "../lib/errors";
import type { GitIdentityEnv } from "../lib/git-identity";
import { logger } from "../lib/logger";
import { envWithAmbientProxy } from "../lib/net/proxy-env";
import { DEV_NULL } from "../lib/platform";
import { safeSpawn } from "../lib/spawn";
import {
	assertSingleFilePatch,
	buildCommitDetail,
	COMMIT_META_FORMAT,
	COMMIT_META_MAX_BYTES,
	type CommitMeta,
	commitListArgs,
	commitNotFound,
	commitPatchArgs,
	parseCommitMeta,
	parseNameStatusRecords,
	requireCommitFile,
} from "./git-commit-preview-parse";
import { coordinateGitStatusQuery, getLocalGitStatusQueryKey } from "./git-status-query";
import { GIT_STATUS_TIMEOUT_MS, readGitStatusStream } from "./git-status-stream";
import { lifecycleCreationPath } from "./worktree-lifecycle-guard";

const gitRequestContext = new AsyncLocalStorage<{
	signal?: AbortSignal;
	beforeWrite?: () => Promise<void>;
}>();
/** HTTP management budget without changing long-running lifecycle/network callers. */
export function withGitRequestContext<T>(
	signal: AbortSignal | undefined,
	run: () => Promise<T>,
	beforeWrite?: () => Promise<void>,
): Promise<T> {
	return gitRequestContext.run({ signal, beforeWrite }, run);
}

interface ExecResult {
	stdout: string;
	stderr: string;
	exitCode: number;
	/**
	 * Output exceeded the capture limit, so `stdout` is a prefix of what git printed.
	 *
	 * Exposed rather than hidden because for a parser the difference matters: a
	 * truncated `-z` listing ends mid-record and a truncated history page silently
	 * loses commits. Callers that can be wrong silently must check this; callers
	 * that only show text to a human can ignore it.
	 */
	truncated?: boolean;
}

interface ExecOptions {
	silent?: boolean;
	optionalLocks?: boolean;
	/**
	 * Hard timeout in ms. Left undefined for write operations (commit, stash, reset),
	 * where a legitimate run on a large worktree can be slow and killing it mid-way is
	 * worse than waiting.
	 */
	timeout?: number;
	maxOutputBytes?: number;
	/**
	 * `GIT_AUTHOR_*`/`GIT_COMMITTER_*` overrides that attribute a commit to the
	 * acting person instead of the host machine's global git config.
	 *
	 * Only meaningful for commands that write commit objects. Null/undefined is the
	 * documented "inherit the host identity" case and must remain byte-for-byte
	 * identical to the historical behaviour, so no env is passed at all in that case
	 * — see the note in {@link exec}.
	 */
	identity?: GitIdentityEnv | null;
}

/** Commit-writing methods take this to attribute the commit to a real person. */
export type GitCommitIdentity = GitIdentityEnv | null | undefined;

/**
 * Ceiling for read-only git commands.
 *
 * These all run on the server's single JS thread and are awaited by HTTP handlers,
 * so an unbounded `git log`/`diff` against a pathological repository stalls every
 * other request. 60 s is far above any healthy local invocation while still
 * guaranteeing the request eventually returns.
 */
const READ_TIMEOUT_MS = 60_000;
/**
 * Capture ceiling for read-only git commands, an order of magnitude below
 * safeSpawn's 10 MB default.
 *
 * Nothing here feeds a client more than ~100 KB (diffs are truncated, listings are
 * capped at 200 entries), so retaining megabytes only inflates peak heap. The
 * streams are still drained — only the retained string is bounded.
 */
const READ_MAX_OUTPUT_BYTES = 1024 * 1024;

/**
 * Depth of the walk behind {@link gitService.getLastCommitTimes}.
 *
 * Measured on this repository: 200 commits resolved 90 of 127 changed paths, and 400
 * and 800 resolved the same 90 — the walk stops finding new paths well before it stops
 * producing output (40 KB at 200, 88 KB at 800). Anything unresolved falls back to the
 * window boundary, which is safe by construction, so extra depth buys nothing.
 */
const COMMIT_BOUNDARY_MAX_COMMITS = 200;

/**
 * Path ceiling for one boundary walk.
 *
 * Matched to the 200-entry cap on `getStatusSummary().files`: the caller cannot show
 * more changed files than that, so a larger pathspec would describe rows nobody
 * renders. Also keeps the argument vector far below ARG_MAX (128 paths measured at
 * 5 KB here, against a 2 MB limit).
 */
const COMMIT_BOUNDARY_MAX_PATHS = 200;

/**
 * Prefix marking a commit line in the boundary walk.
 *
 * `--name-only` prints paths bare on their own lines, so commit lines need a sentinel
 * that cannot occur at the start of a repo-relative path. NUL is the one byte git
 * forbids in a path, which makes misparsing impossible rather than merely unlikely.
 *
 * It cannot be written literally in the format argument — spawn rejects an argv entry
 * containing NUL (`ERR_INVALID_ARG_VALUE`) — so the format uses git's `%x00` escape and
 * git emits the byte itself. Note that `String.prototype.trim` does NOT strip NUL, so
 * the marker survives line trimming.
 */
const COMMIT_BOUNDARY_MARKER = "\0";
/** The `%x00` escape that makes git print {@link COMMIT_BOUNDARY_MARKER}. */
const COMMIT_BOUNDARY_MARKER_FORMAT = "%x00";

/**
 * Convert a git ISO-8601 timestamp to the UTC `...Z` form.
 *
 * `%cI` carries the committer's local offset (`2026-08-14T18:19:20+08:00`) while
 * `file_attributions.changedAt` is stored as UTC with a `Z` suffix. These boundaries are
 * compared against that column as STRINGS, and lexicographic order across different
 * offsets is meaningless: the example above sorts after `2026-08-14T11:00:00Z` despite
 * being two hours earlier. Normalizing here keeps every consumer on one representation.
 *
 * Returns null for an unparseable value rather than a wrong instant, so a caller can
 * skip the boundary instead of silently filtering on garbage.
 */
function toUtcIso(raw: string): string | null {
	const ms = Date.parse(raw);
	if (Number.isNaN(ms)) return null;
	return new Date(ms).toISOString();
}

/**
 * Serialize git write operations for one worktree.
 *
 * Read-only operations (status, diff, log, etc.) usually don't need this
 * application-level mutex, but Git may still refresh the index and briefly
 * create `.git/index.lock`. Use execRead() for those commands so Git disables
 * optional index writes via `--no-optional-locks`.
 *
 * Every write method below wraps its whole command sequence in this, so two writes
 * on one worktree (a stage racing a commit, a discard racing a stash) run one after
 * the other instead of interleaving their `git` invocations on the same index. The
 * lock is `lib/async-mutex`'s path-normalized {@link worktreeLock}, so `/a/wt` and
 * `/a/wt/` queue on one key while unrelated worktrees proceed independently.
 *
 * The mutex is not re-entrant: `acquire` chains behind the current holder without
 * checking who owns it, so a write method called from inside a `worktreeLock` block
 * waits on the lock its own caller holds — a deterministic self-deadlock whose symptom
 * is a request that never returns, with nothing thrown or logged. Nothing outside this
 * module acquires `worktreeLock`; a caller that needs to hold a worktree across several
 * commands must run lock-free command bodies inside its own block instead of calling
 * these methods. `git-service-worktree-lock.test.ts` pins the hazard.
 *
 * Inside {@link withGitRequestContext} (HTTP management requests) the lock is only
 * tried for 500 ms: a busy worktree answers 409 `GIT_WORKSPACE_BUSY` instead of
 * queueing the request behind a long-running write.
 */
async function withWorktreeLock<T>(worktreePath: string, fn: () => Promise<T>): Promise<T> {
	const request = gitRequestContext.getStore();
	if (!request) return worktreeLock.acquire(worktreePath, fn);
	const result = await worktreeLock.tryAcquire(
		worktreePath,
		async () => {
			request.signal?.throwIfAborted();
			await request.beforeWrite?.();
			return fn();
		},
		500,
	);
	if (!result.acquired)
		throw new AppError(
			"Git working tree is busy; retry after the current operation completes",
			409,
			"GIT_WORKSPACE_BUSY",
		);
	return result.value;
}

export interface GitStatusFile {
	/** Two-character porcelain status, e.g. "M ", " M", "MM", "??". */
	status: string;
	path: string;
	/**
	 * Pre-rename path, for an `R`/`C` entry. Undefined for every other status.
	 *
	 * Porcelain already reports it (`R  old\0new`) and the parser already extracts it; it
	 * is surfaced here because consumers keyed on `path` alone cannot find anything
	 * recorded before the rename. File attribution is the concrete case: rows are written
	 * under the path that existed at the time, so a renamed file's entire history sits
	 * under a path that no longer appears in the diff.
	 */
	oldPath?: string;
	/** Omitted by lightweight status; explicit diff APIs provide line statistics. */
	linesAdded?: number;
	linesRemoved?: number;
	stagedLinesAdded?: number;
	stagedLinesRemoved?: number;
	unstagedLinesAdded?: number;
	unstagedLinesRemoved?: number;
}

export interface GitStatusSummary {
	/** File details or the status scan were capped; totals may still be exact. */
	truncated?: boolean;
	hasChanges: boolean;
	staged: number;
	unstaged: number;
	untracked: number;
	/** Capped at 200 entries. Use `totalFiles` for the real count. */
	files: GitStatusFile[];
	/** Number of complete scanned records; a lower bound when totalFilesExact is false. */
	totalFiles: number;
	totalFilesExact?: boolean;
	headSha: string;
	branch: string;
	/** Total lines added across all uncommitted changes (staged + unstaged + untracked). */
	linesAdded?: number;
	/** Total lines removed across all uncommitted changes (staged + unstaged + untracked). */
	linesRemoved?: number;
}

const MAX_GIT_FAILURE_OUTPUT_CHARS = 4000;

function stripTrailingLineBreaks(text: string): string {
	return text.replace(/[\r\n]+$/, "");
}

function combinedCommandOutput(result: ExecResult): string {
	return [result.stderr.trim(), result.stdout.trim()].filter(Boolean).join("\n");
}

function truncateGitFailureOutput(output: string): string {
	if (output.length <= MAX_GIT_FAILURE_OUTPUT_CHARS) return output;
	return `${output.slice(0, MAX_GIT_FAILURE_OUTPUT_CHARS)}\n...[truncated]`;
}

function gitFailureMessage(prefix: string, result: ExecResult): string {
	const output = combinedCommandOutput(result);
	if (!output) return `${prefix} (exit code ${result.exitCode})`;
	return `${prefix}: ${truncateGitFailureOutput(output)}`;
}

function commandOutputMentionsConflict(result: ExecResult): boolean {
	const output = combinedCommandOutput(result).toLowerCase();
	return output.includes("conflict") || output.includes("冲突");
}

function normalizeExecOptions(options: boolean | ExecOptions = {}): ExecOptions {
	if (typeof options === "boolean") return { silent: options, optionalLocks: true };
	return {
		silent: options.silent ?? false,
		optionalLocks: options.optionalLocks ?? true,
		timeout: options.timeout,
		maxOutputBytes: options.maxOutputBytes,
		identity: options.identity,
	};
}

/**
 * Marker safeSpawn appends to a truncated stream.
 *
 * Removed here instead of being left in place because git output is *parsed*: a
 * `-z` listing would gain the marker as a bogus path and a NUL-separated history
 * line would gain it as a bogus field. The fact of truncation is carried by
 * `ExecResult.truncated`, which is a signal a parser can act on.
 */
const SPAWN_TRUNCATION_MARKER = "[safeSpawn output truncated — exceeded capture limit]";

function stripTruncationMarker(text: string): string {
	const at = text.lastIndexOf(SPAWN_TRUNCATION_MARKER);
	return at === -1 ? text : text.slice(0, at);
}

async function exec(
	args: string[],
	cwd: string,
	options: boolean | ExecOptions = {},
): Promise<ExecResult> {
	// Inside a worktree-lifecycle protection reservation, dispatch against the frozen
	// canonical cwd, so a path alias re-pointed during an await cannot redirect the
	// command. Outside a reservation the path passes through unchanged.
	cwd = await lifecycleCreationPath(cwd);
	const { silent, optionalLocks, timeout, maxOutputBytes, identity } =
		normalizeExecOptions(options);
	const request = gitRequestContext.getStore();
	request?.signal?.throwIfAborted();
	if (request && args[0] === "diff")
		args = ["diff", "--no-ext-diff", "--no-textconv", "--no-color", ...args.slice(1)];
	const prefix = request
		? [
				"git",
				"--literal-pathspecs",
				"-c",
				"core.fsmonitor=false",
				"-c",
				"diff.external=",
				"-c",
				"diff.trustExitCode=false",
			]
		: ["git"];
	const cmd = optionalLocks ? [...prefix, ...args] : [...prefix, "--no-optional-locks", ...args];
	try {
		const result = await safeSpawn({
			cmd,
			cwd,
			timeout: request
				? Math.min(timeout ?? GIT_WORKSPACE_WRITE_TIMEOUT_MS, GIT_WORKSPACE_WRITE_TIMEOUT_MS)
				: timeout,
			maxOutputBytes: request
				? Math.min(maxOutputBytes ?? GIT_WORKSPACE_MAX_BYTES, GIT_WORKSPACE_MAX_BYTES)
				: maxOutputBytes,
			signal: request?.signal,
			// `Bun.spawn`'s `env` REPLACES the environment rather than merging into it,
			// so an identity has to be layered over `process.env` explicitly — passing
			// the four `GIT_*` variables alone would strip PATH/HOME and break git.
			// Omitted entirely when there is no identity: that keeps the no-identity
			// path byte-for-byte identical to the behaviour before this existed.
			env: identity ? { ...process.env, ...identity } : undefined,
		});
		const truncated = result.stdoutTruncated === true || result.stderrTruncated === true;
		const trimmedStdout = stripTrailingLineBreaks(
			result.stdoutTruncated ? stripTruncationMarker(result.stdout) : result.stdout,
		);
		const trimmedStderr = stripTrailingLineBreaks(
			result.stderrTruncated ? stripTruncationMarker(result.stderr) : result.stderr,
		);
		if (result.exitCode !== 0 && !silent) {
			logger.error("git command failed", {
				args: request ? args[0] : args.join(" "),
				cwd: request ? undefined : cwd,
				stdout: !request && trimmedStdout ? truncateGitFailureOutput(trimmedStdout) : undefined,
				stderr: !request && trimmedStderr ? truncateGitFailureOutput(trimmedStderr) : undefined,
				exitCode: result.exitCode,
				optionalLocks,
				truncated: truncated || undefined,
			});
		}
		if (truncated) {
			logger.warn("git command output truncated", {
				args: args.join(" "),
				cwd,
				limit: maxOutputBytes,
			});
		}
		return {
			stdout: trimmedStdout,
			stderr: trimmedStderr,
			exitCode: result.exitCode,
			truncated,
		};
	} catch (err) {
		request?.signal?.throwIfAborted();
		if (request) throw err;
		// When silent, swallow spawn errors (e.g. git not found) and return a
		// synthetic failure result so callers that check exitCode still work.
		if (silent) {
			const msg = err instanceof Error ? err.message : String(err);
			logger.debug("git command spawn failed (silent)", {
				args: args.join(" "),
				cwd,
				error: msg,
				optionalLocks,
			});
			return { stdout: "", stderr: msg, exitCode: -1 };
		}
		throw err;
	}
}

/**
 * Run a read-only git command.
 *
 * Read operations get a timeout and a capture ceiling by default; writes deliberately
 * do not, since a slow commit or stash on a large worktree is normal and killing it
 * half-done leaves the repository in a worse state than waiting does.
 */
function execRead(
	args: string[],
	cwd: string,
	silent = false,
	options?: { timeout?: number; maxOutputBytes?: number },
): Promise<ExecResult> {
	return exec(args, cwd, {
		silent,
		optionalLocks: false,
		timeout: options?.timeout ?? READ_TIMEOUT_MS,
		maxOutputBytes: options?.maxOutputBytes ?? READ_MAX_OUTPUT_BYTES,
	});
}

async function detectUnmergedFiles(worktreePath: string): Promise<string[]> {
	const statusResult = await execRead(
		["diff", "--name-only", "--diff-filter=U"],
		worktreePath,
		true,
	);
	if (statusResult.exitCode !== 0) return [];
	return statusResult.stdout.split("\n").filter(Boolean);
}

/**
 * Whether the repository has no commits at all (unborn HEAD).
 *
 * Distinguishes "brand new repository" from "git is broken or the ref is wrong",
 * which `git log` reports identically as exit 128. Checked with
 * `rev-parse --verify` because it exits 1 (not 128) for an unresolvable HEAD and
 * prints nothing, so a genuine spawn failure still surfaces as an error elsewhere.
 */
async function hasNoCommits(repoPath: string): Promise<boolean> {
	const result = await execRead(["rev-parse", "--verify", "--quiet", "HEAD"], repoPath, true);
	return result.exitCode === 1 && !result.stdout.trim();
}

/**
 * Branch name HEAD is attached to, including a repository that has no commits yet.
 *
 * `rev-parse --abbrev-ref HEAD` exits 128 on an unborn branch (Git prints the literal
 * `HEAD` and an "ambiguous argument" fatal). `symbolic-ref --short` reads the ref
 * name without requiring a commit, and fails only for a detached HEAD — where
 * `--abbrev-ref` then prints `HEAD` and exits 0.
 */
async function resolveCurrentBranch(
	repoPath: string,
	options?: { timeout?: number; maxOutputBytes?: number },
): Promise<string> {
	const symbolic = await execRead(
		["symbolic-ref", "--quiet", "--short", "HEAD"],
		repoPath,
		true,
		options,
	);
	if (symbolic.truncated) throw new GitError("Git branch metadata failed");
	if (symbolic.exitCode === 0 && symbolic.stdout.trim()) return symbolic.stdout.trim();
	const abbrev = await execRead(["rev-parse", "--abbrev-ref", "HEAD"], repoPath, true, options);
	if (abbrev.truncated) throw new GitError("Git branch metadata failed");
	if (abbrev.exitCode === 0 && abbrev.stdout.trim()) return abbrev.stdout.trim();
	if (await hasNoCommits(repoPath)) throw new UnbornHeadError();
	logger.error("git command failed", {
		args: "rev-parse --abbrev-ref HEAD",
		cwd: repoPath,
		stdout: abbrev.stdout || undefined,
		stderr: abbrev.stderr || symbolic.stderr || undefined,
		exitCode: abbrev.exitCode,
		optionalLocks: false,
	});
	throw new GitError(gitFailureMessage("Failed to read the current branch", abbrev));
}

function parseNulSeparatedPaths(output: string): string[] {
	return output.split("\0").filter(Boolean);
}

function stripGitPathPrefix(path: string, prefix: string): string {
	if (!prefix) return path;
	return path.startsWith(prefix) ? path.slice(prefix.length) : path;
}

/**
 * `git status --porcelain` arguments that list untracked files INDIVIDUALLY.
 *
 * ── The bug this closes ──────────────────────────────────────────────────────
 * By default git COLLAPSES an untracked directory into a single entry with a
 * trailing slash: a brand-new `src/feature/` holding three files is reported as
 * one line, `?? src/feature/`. That is a display convenience for the terminal,
 * and it is actively wrong for both consumers here, which read an entry as a
 * file path:
 *   - `getFileDiff` would not see a file inside a new directory as untracked,
 *     fall through to `git diff --`, and return nothing at all;
 *   - `discardFiles` would hand `newdir/` to `git clean`, deleting every sibling
 *     of the one file the user asked to discard.
 * Both are the same root cause: a directory entry pretending to be a file. (The
 * status summary streams its own `-uall` listing; see `git-status-stream`.)
 *
 * `-uall` makes git do the expansion itself, which is the only place it can be
 * done correctly — walking the directory in our own code would re-implement
 * gitignore semantics, and getting those subtly wrong is how ignored build
 * output ends up in a diff.
 *
 * COST: a huge untracked directory (a stray `node_modules`, a build output folder
 * that is not ignored) is enumerated file by file instead of as one line. Both
 * callers pass an explicit pathspec, so the expansion is bounded by what the caller
 * asked about.
 */
const PORCELAIN_UNTRACKED_ALL = ["status", "--porcelain", "-uall"];

/** One deadline covers all subprocesses in a preview, not 60s per command. */
async function withCommitPreviewRead<T>(run: () => Promise<T>): Promise<T> {
	const current = gitRequestContext.getStore();
	const controller = new AbortController();
	const timer = setTimeout(
		() =>
			controller.abort(
				new AppError("Git commit preview timed out", 504, "GIT_COMMIT_PREVIEW_TIMEOUT"),
			),
		GIT_WORKSPACE_TIMEOUT_MS,
	);
	const signal = current?.signal
		? AbortSignal.any([current.signal, controller.signal])
		: controller.signal;
	try {
		return await withGitRequestContext(signal, run, current?.beforeWrite);
	} finally {
		clearTimeout(timer);
	}
}
/** Verify `sha` names a commit, then read its bounded metadata. */
async function readCommitMeta(repoPath: string, sha: string): Promise<CommitMeta> {
	if (!GIT_COMMIT_SHA_PATTERN.test(sha)) throw new ValidationError("Invalid commit SHA");
	const verify = await execRead(
		["rev-parse", "--verify", "--quiet", `${sha}^{commit}`],
		repoPath,
		true,
		{
			maxOutputBytes: 256,
		},
	);
	if (verify.exitCode === 1) throw commitNotFound(sha);
	if (verify.exitCode !== 0)
		throw new GitError(gitFailureMessage("Git commit lookup failed", verify));
	const result = await execRead(
		["log", "-1", "--no-show-signature", COMMIT_META_FORMAT, sha, "--"],
		repoPath,
		false,
		{ maxOutputBytes: COMMIT_META_MAX_BYTES },
	);
	if (result.exitCode !== 0)
		throw new GitError(gitFailureMessage("Git commit metadata failed", result));
	return parseCommitMeta(result.stdout, result.truncated === true);
}

export const gitService = {
	async getCurrentBranch(repoPath: string): Promise<string> {
		return resolveCurrentBranch(repoPath);
	},

	async isGitRepo(path: string): Promise<boolean> {
		const result = await execRead(["rev-parse", "--is-inside-work-tree"], path);
		return result.exitCode === 0;
	},

	/** Resolve the repository root for a path, or null when it is not inside Git. */
	async getRepositoryRoot(path: string): Promise<string | null> {
		const result = await execRead(["rev-parse", "--show-toplevel"], path, true);
		if (result.exitCode !== 0) return null;
		return result.stdout.trim() || null;
	},

	async getHeadCommit(repoPath: string): Promise<string> {
		// `--verify --quiet` exits 1 with no output when HEAD has no commit. Plain
		// `rev-parse HEAD` exits 128 and is logged as a hard failure for that same state.
		const result = await execRead(["rev-parse", "--verify", "--quiet", "HEAD"], repoPath, true);
		const sha = result.stdout.trim();
		if (result.exitCode === 0 && sha) return sha;
		if (result.exitCode === 1 && !sha && !result.stderr.trim()) throw new UnbornHeadError();
		logger.error("git command failed", {
			args: "rev-parse --verify --quiet HEAD",
			cwd: repoPath,
			stdout: result.stdout || undefined,
			stderr: result.stderr || undefined,
			exitCode: result.exitCode,
			optionalLocks: false,
		});
		throw new GitError(gitFailureMessage("Failed to get HEAD commit", result));
	},

	async getStatusSummary(
		worktreePath: string,
		options: { ttlMs?: number } = {},
	): Promise<GitStatusSummary> {
		return coordinateGitStatusQuery(
			getLocalGitStatusQueryKey(worktreePath),
			async (signal) => {
				const queryController = new AbortController();
				const boundedSignal = AbortSignal.any([
					signal,
					queryController.signal,
					AbortSignal.timeout(GIT_STATUS_TIMEOUT_MS),
				]);
				return withGitRequestContext(boundedSignal, async () => {
					const startedAt = Date.now();
					// Freeze lifecycle cwd once for streaming and all metadata commands alike.
					const cwd = await lifecycleCreationPath(worktreePath);
					const metadataOptions = { timeout: GIT_STATUS_TIMEOUT_MS, maxOutputBytes: 4096 };
					const queries = [
						execRead(["rev-parse", "--show-prefix"], cwd, true, metadataOptions),
						readGitStatusStream(cwd, boundedSignal),
						execRead(["rev-parse", "--verify", "--quiet", "HEAD"], cwd, true, metadataOptions),
						execRead(["symbolic-ref", "--quiet", "--short", "HEAD"], cwd, true, metadataOptions),
					] as const;
					const [prefix, status, head, branch] = await Promise.all(queries).catch(async (error) => {
						// Keep the coordinator slot until every sibling process has exited.
						queryController.abort(error);
						await Promise.allSettled(queries);
						throw error;
					});
					boundedSignal.throwIfAborted();
					if (prefix.exitCode !== 0 || prefix.truncated || head.truncated || branch.truncated)
						throw new GitError("Git status metadata failed");
					// symbolic-ref already named an attached branch, including one with no commits.
					// It fails for detached HEAD, where abbrev-ref prints the literal "HEAD".
					const branchResult =
						branch.exitCode === 0 && branch.stdout.trim()
							? branch
							: await execRead(["rev-parse", "--abbrev-ref", "HEAD"], cwd, true, metadataOptions);
					boundedSignal.throwIfAborted();
					if (branchResult.exitCode !== 0 || branchResult.truncated)
						throw new GitError("Git status branch metadata failed");
					const { parser, budgetReason } = status;
					const files = parser.files.map((entry) => ({
						...entry,
						path: stripGitPathPrefix(entry.path, prefix.stdout),
						...(entry.oldPath ? { oldPath: stripGitPathPrefix(entry.oldPath, prefix.stdout) } : {}),
					}));
					const truncated = !!budgetReason || parser.totalFiles > files.length;
					const elapsedMs = Date.now() - startedAt;
					if (elapsedMs > 1000)
						logger.warn("Slow Git status query", {
							elapsedMs,
							scanned: parser.totalFiles,
							returned: files.length,
							truncationReason: budgetReason ?? (truncated ? "details" : "none"),
						});
					return {
						hasChanges: parser.totalFiles > 0 || !!budgetReason,
						staged: parser.staged,
						unstaged: parser.unstaged,
						untracked: parser.untracked,
						files,
						totalFiles: parser.totalFiles,
						totalFilesExact: !budgetReason,
						headSha: head.exitCode === 0 ? head.stdout : "",
						branch: branchResult.stdout,
						truncated,
					};
				});
			},
			{ signal: gitRequestContext.getStore()?.signal, ttlMs: options.ttlMs },
		);
	},

	/** Get full diff of all uncommitted changes (staged + unstaged + untracked).
	 *  Truncates at ~100KB to avoid blowing up AI token budgets.
	 *  Binary file *content* never appears — see the note on binary handling below. */
	async getFullDiff(worktreePath: string, maxBytes = 100_000): Promise<string> {
		// Diff of tracked files (staged + unstaged combined against HEAD).
		//
		// Binary content is kept out by doing nothing: git's default is a single
		// "Binary files a/x and b/x differ" line, and only an explicit `--binary`
		// (or `--text`) makes it emit content. There is no `--no-binary` — passing
		// one makes git print usage and exit 129, which silently emptied every diff
		// this method produced, including the one the commit-message generator reads.
		// -D/--irreversible-delete: omit full content of deleted files.
		const diffResult = await execRead(
			["diff", (await hasNoCommits(worktreePath)) ? "--cached" : "HEAD", "-D", "--no-color"],
			worktreePath,
			true,
		);
		const parts: string[] = [];
		let totalLen = 0;

		const addPart = (text: string): boolean => {
			if (totalLen + text.length > maxBytes) {
				const remaining = maxBytes - totalLen;
				if (remaining > 0) parts.push(text.slice(0, remaining));
				parts.push("\n\n[diff truncated — exceeded size limit]");
				return false;
			}
			parts.push(text);
			totalLen += text.length;
			return true;
		};

		if (diffResult.stdout && !addPart(diffResult.stdout)) {
			return parts.join("\n");
		}

		// List untracked files and show their content (skip binary files)
		const untrackedResult = await execRead(
			["ls-files", "--others", "--exclude-standard", "-z"],
			worktreePath,
			true,
		);
		const untrackedFiles = parseNulSeparatedPaths(untrackedResult.stdout);
		// Cap the number of untracked files we diff to avoid spawning too many processes
		const MAX_UNTRACKED_DIFFS = 50;
		const filesToDiff = untrackedFiles.slice(0, MAX_UNTRACKED_DIFFS);
		for (const file of filesToDiff) {
			// --no-index always exits 1 when diff is found — silence the expected error log.
			// Binary files again produce only a "Binary files ... differ" line by default.
			const showResult = await execRead(
				["diff", "--no-index", "--no-color", "--", DEV_NULL, file],
				worktreePath,
				true,
			);
			if (showResult.stdout && !addPart(showResult.stdout)) break;
		}
		if (untrackedFiles.length > MAX_UNTRACKED_DIFFS) {
			addPart(
				`\n\n[... ${untrackedFiles.length - MAX_UNTRACKED_DIFFS} more untracked files omitted]`,
			);
		}

		return parts.join("\n");
	},

	/**
	 * Commit preview: metadata plus the changed-file list against the FIRST parent
	 * (root commits against the empty tree). Reads only the object database.
	 */
	async getCommitDetail(repoPath: string, sha: string): Promise<GitCommitDetail> {
		return withCommitPreviewRead(async () => {
			const meta = await readCommitMeta(repoPath, sha);
			const base = meta.parents[0] ?? null;
			const [numstat, nameStatus] = await Promise.all([
				execRead(commitListArgs("numstat", sha, base), repoPath),
				execRead(commitListArgs("name-status", sha, base), repoPath),
			]);
			for (const result of [numstat, nameStatus])
				if (result.exitCode !== 0)
					throw new GitError(gitFailureMessage("Git commit file list failed", result));
			return buildCommitDetail(meta, {
				numstat: numstat.stdout,
				numstatTruncated: numstat.truncated === true,
				nameStatus: nameStatus.stdout,
				nameStatusTruncated: nameStatus.truncated === true,
			});
		});
	},

	/** Patch of one file within a commit, capped at the process output. */
	async getCommitPatch(
		repoPath: string,
		sha: string,
		file: string,
		oldPath?: string,
	): Promise<GitCommitPatch> {
		return withCommitPreviewRead(async () => {
			const meta = await readCommitMeta(repoPath, sha);
			const base = meta.parents[0] ?? null;
			const names = await execRead(commitListArgs("name-status", sha, base), repoPath);
			if (names.exitCode !== 0)
				throw new GitError(gitFailureMessage("Git commit file list failed", names));
			const entry = requireCommitFile(names.stdout, names.truncated === true, file, oldPath);
			const paths = entry.oldPath ? [entry.oldPath, entry.path] : [entry.path];
			// Literal pathspecs still match descendants. A rename source may have become
			// a directory in the new tree, so validate the *scoped* list before truncating
			// a patch, rather than counting headers after later files have been discarded.
			const scoped = await execRead(
				[...commitListArgs("name-status", sha, base), ...paths],
				repoPath,
				false,
				{ maxOutputBytes: 64 * 1024 },
			);
			if (scoped.exitCode !== 0)
				throw new GitError(gitFailureMessage("Git commit file lookup failed", scoped));
			if (scoped.truncated)
				throw new AppError(
					"Commit path exceeds the preview budget",
					413,
					"GIT_COMMIT_PREVIEW_TOO_LARGE",
				);
			const records = parseNameStatusRecords(scoped.stdout);
			if (
				records.length !== 1 ||
				records[0]?.path !== entry.path ||
				records[0]?.oldPath !== entry.oldPath ||
				records[0]?.status !== entry.status
			)
				throw new ValidationError("Commit preview path must name a single changed file");
			const result = await execRead(
				commitPatchArgs(sha, base, file, entry.oldPath),
				repoPath,
				false,
				{ maxOutputBytes: GIT_COMMIT_PREVIEW_PATCH_MAX_BYTES },
			);
			if (result.exitCode !== 0)
				throw new GitError(gitFailureMessage("Git commit patch failed", result));
			assertSingleFilePatch(result.stdout, file, records[0]);
			return { diff: result.stdout, truncated: result.truncated === true };
		});
	},

	async initRepo(repoPath: string, identity?: GitCommitIdentity): Promise<void> {
		mkdirSync(repoPath, { recursive: true });
		const result = await exec(["init"], repoPath);
		if (result.exitCode !== 0) throw new GitError(`Failed to init repo: ${result.stderr}`);
		// Create initial empty commit so branches can be created
		const commitResult = await exec(["commit", "--allow-empty", "-m", "Initial commit"], repoPath, {
			identity: identity ?? null,
		});
		if (commitResult.exitCode !== 0) {
			throw new GitError(`Failed to create initial commit: ${commitResult.stderr}`);
		}
	},

	/**
	 * Clone a repo with `--progress`, streaming stderr lines to a callback.
	 * Git writes progress (counting objects, compressing, receiving, resolving)
	 * to stderr using `\r` for in-place updates.
	 */
	async cloneRepoStreaming(
		url: string,
		destPath: string,
		branch: string | undefined,
		onProgress: (line: string) => void,
		credentials?: { username: string; password: string },
	): Promise<void> {
		// If credentials provided, use a temporary GIT_ASKPASS script so that
		// the password never appears in process arguments or git's stderr output.
		let askPassScript: string | undefined;
		if (credentials) {
			const id = Math.random().toString(36).slice(2, 10);
			askPassScript = join(tmpdir(), `narrafork-askpass-${id}.sh`);
			// Git calls GIT_ASKPASS with a single argument like "Username for '...': "
			// or "Password for '...': ". We match on the prompt to return the right value.
			const script = [
				"#!/bin/sh",
				`case "$1" in`,
				`  *[Uu]sername*) echo '${credentials.username.replace(/'/g, "'\\''")}';;`,
				`  *) echo '${credentials.password.replace(/'/g, "'\\''")}';;`,
				"esac",
			].join("\n");
			writeFileSync(askPassScript, script, { mode: 0o700 });
		}

		const args = ["clone", "--progress"];
		if (branch) args.push("--branch", branch);
		args.push(url, destPath);

		const proc = Bun.spawn(["git", ...args], {
			cwd: ".",
			stdin: "ignore",
			stdout: "pipe",
			stderr: "pipe",
			windowsHide: true,
			// git talks to the user's remotes, so it keeps the user's ambient proxy
			// configuration rather than NarraFork's blanked one.
			env: envWithAmbientProxy({
				GIT_TERMINAL_PROMPT: "0",
				...(askPassScript ? { GIT_ASKPASS: askPassScript } : {}),
			}),
		});

		// Read stderr in streaming fashion — git progress uses \r for in-place updates
		const reader = proc.stderr.getReader();
		const decoder = new TextDecoder();
		let buffer = "";
		const recentStderrLines: string[] = [];

		try {
			while (true) {
				const { done, value } = await reader.read();
				if (done) break;
				buffer += decoder.decode(value, { stream: true });
				// Split on \r or \n — git uses \r for progress updates
				const parts = buffer.split(/[\r\n]+/);
				buffer = parts.pop() ?? "";
				for (const part of parts) {
					const trimmed = part.trim();
					if (trimmed) {
						onProgress(trimmed);
						recentStderrLines.push(trimmed);
						if (recentStderrLines.length > 5) recentStderrLines.shift();
					}
				}
			}
			// Flush remaining
			const final = decoder.decode();
			buffer += final;
			if (buffer.trim()) {
				onProgress(buffer.trim());
				recentStderrLines.push(buffer.trim());
				if (recentStderrLines.length > 5) recentStderrLines.shift();
			}
		} finally {
			reader.releaseLock();
		}

		// Drain stdout to avoid pipe deadlock
		await new Response(proc.stdout).text();

		// Clean up temporary askpass script before checking exit code
		if (askPassScript) {
			try {
				rmSync(askPassScript, { force: true });
			} catch {}
		}

		const exitCode = await proc.exited;
		if (exitCode !== 0) {
			const detail =
				recentStderrLines.find((l) => l.startsWith("fatal:")) ||
				recentStderrLines[recentStderrLines.length - 1] ||
				"";

			// Detect authentication failure
			const allStderr = recentStderrLines.join("\n").toLowerCase();
			if (
				allStderr.includes("authentication failed") ||
				allStderr.includes("could not read username") ||
				allStderr.includes("terminal prompts disabled") ||
				allStderr.includes("could not read password") ||
				allStderr.includes("invalid credentials") ||
				allStderr.includes("logon failed")
			) {
				throw new GitAuthError(detail || "Git authentication required");
			}

			const suffix = detail ? `: ${detail}` : "";
			throw new GitError(`Failed to clone repo (exit code ${exitCode})${suffix}`);
		}
	},

	// === Stage / Unstage ===

	async stageFiles(worktreePath: string, files: string[]): Promise<void> {
		if (files.length === 0) return;
		return withWorktreeLock(worktreePath, async () => {
			const result = await exec(["add", "--", ...files], worktreePath);
			if (result.exitCode !== 0) throw new GitError(`git add failed: ${result.stderr}`);
		});
	},

	async stageAll(worktreePath: string): Promise<void> {
		return withWorktreeLock(worktreePath, async () => {
			const result = await exec(["add", "-A"], worktreePath);
			if (result.exitCode !== 0) throw new GitError(`git add -A failed: ${result.stderr}`);
		});
	},

	async unstageFiles(worktreePath: string, files: string[]): Promise<void> {
		if (files.length === 0) return;
		return withWorktreeLock(worktreePath, async () => {
			const args = (await hasNoCommits(worktreePath))
				? ["rm", "--cached", "-r", "--ignore-unmatch", "--", ...files]
				: ["reset", "HEAD", "--", ...files];
			const result = await exec(args, worktreePath);
			if (result.exitCode !== 0) throw new GitError(`git reset failed: ${result.stderr}`);
		});
	},

	async unstageAll(worktreePath: string): Promise<void> {
		return withWorktreeLock(worktreePath, async () => {
			const result = await exec(
				(await hasNoCommits(worktreePath)) ? ["read-tree", "--empty"] : ["reset", "HEAD"],
				worktreePath,
			);
			if (result.exitCode !== 0) throw new GitError(`git reset failed: ${result.stderr}`);
		});
	},

	async commit(
		worktreePath: string,
		message: string,
		identity?: GitCommitIdentity,
	): Promise<string> {
		return withWorktreeLock(worktreePath, async () => {
			const result = await exec(["commit", "-m", message], worktreePath, {
				identity: identity ?? null,
			});
			if (result.exitCode !== 0) throw new GitError(`git commit failed: ${result.stderr}`);
			return this.getHeadCommit(worktreePath);
		});
	},

	async discardFiles(worktreePath: string, files: string[]): Promise<void> {
		if (files.length === 0) return;
		return withWorktreeLock(worktreePath, async () => {
			// `-uall` is a SAFETY requirement here, not just a display one. Collapsed
			// output would report `?? newdir/` for a request to discard
			// `newdir/one.txt`, and the `git clean` below would then delete the entire
			// directory — every sibling the user never asked to touch.
			const statusResult = await execRead(
				[...PORCELAIN_UNTRACKED_ALL, "--", ...files],
				worktreePath,
			);
			const tracked: string[] = [];
			const untracked: string[] = [];
			for (const line of statusResult.stdout.split("\n").filter(Boolean)) {
				const path = line.slice(3);
				if (line[0] === "?" && line[1] === "?") {
					untracked.push(path);
				} else {
					tracked.push(path);
				}
			}
			if (tracked.length > 0) {
				const r = await exec(["checkout", "HEAD", "--", ...tracked], worktreePath);
				if (r.exitCode !== 0) throw new GitError(`git checkout failed: ${r.stderr}`);
			}
			if (untracked.length > 0) {
				const r = await exec(["clean", "-f", "--", ...untracked], worktreePath);
				if (r.exitCode !== 0) throw new GitError(`git clean failed: ${r.stderr}`);
			}
		});
	},

	async discardAll(worktreePath: string): Promise<void> {
		return withWorktreeLock(worktreePath, async () => {
			const r1 = await exec(["checkout", "HEAD", "--", "."], worktreePath);
			if (r1.exitCode !== 0) throw new GitError(`git checkout failed: ${r1.stderr}`);
			const r2 = await exec(["clean", "-fd"], worktreePath);
			if (r2.exitCode !== 0) throw new GitError(`git clean failed: ${r2.stderr}`);
		});
	},

	/** Stash the working tree. `identity` authors the underlying stash commit objects. */
	async stash(worktreePath: string, message?: string, identity?: GitCommitIdentity): Promise<void> {
		return withWorktreeLock(worktreePath, async () => {
			const args = ["stash", "push", "--include-untracked"];
			if (message) args.push("-m", message);
			const result = await exec(args, worktreePath, { identity: identity ?? null });
			if (result.exitCode !== 0) throw new GitError(`git stash failed: ${result.stderr}`);
		});
	},

	async stashPop(worktreePath: string): Promise<{ hasConflicts: boolean }> {
		return withWorktreeLock(worktreePath, async () => {
			const result = await exec(["stash", "pop"], worktreePath, true);
			if (result.exitCode !== 0) {
				const hasUnmerged = (await detectUnmergedFiles(worktreePath)).length > 0;
				if (hasUnmerged || commandOutputMentionsConflict(result)) {
					return { hasConflicts: true };
				}
				throw new GitError(gitFailureMessage("git stash pop failed", result));
			}
			return { hasConflicts: false };
		});
	},

	async stashList(
		worktreePath: string,
	): Promise<Array<{ index: number; message: string; date: string }>> {
		const result = await execRead(
			["stash", "list", "--format=%gd%x00%gs%x00%ai"],
			worktreePath,
			true,
		);
		if (result.truncated) throw new GitError("Git stash list exceeds its output budget");
		if (result.exitCode !== 0)
			throw new GitError(gitFailureMessage("Git stash list failed", result));
		if (!result.stdout.trim()) return [];
		return result.stdout
			.trim()
			.split("\n")
			.map((line) => {
				const [ref, message, date] = line.split("\0");
				const index = Number.parseInt(ref?.replace("stash@{", "").replace("}", "") ?? "0", 10);
				return { index, message: message ?? "", date: date ?? "" };
			});
	},

	async stashDrop(worktreePath: string, index: number): Promise<void> {
		return withWorktreeLock(worktreePath, async () => {
			const result = await exec(["stash", "drop", `stash@{${index}}`], worktreePath);
			if (result.exitCode !== 0) throw new GitError(`git stash drop failed: ${result.stderr}`);
		});
	},

	// === File diff (working tree) ===

	async getFileDiff(
		worktreePath: string,
		filePath: string,
		staged = false,
		maxBytes = 200_000,
	): Promise<{ diff: string; truncated: boolean }> {
		// Check if file is untracked. `-uall` so a file inside a brand-new directory
		// is reported as itself rather than as its collapsed parent directory.
		const statusResult = await execRead(
			[...PORCELAIN_UNTRACKED_ALL, "--", filePath],
			worktreePath,
			true,
		);
		const statusLine = statusResult.stdout.trim();

		let diff: string;
		let truncated = statusResult.truncated ?? false;
		if (statusLine.startsWith("??")) {
			// Untracked file — show full content as "new file" diff
			const r = await execRead(
				["diff", "--no-index", "--", DEV_NULL, filePath],
				worktreePath,
				true,
			);
			diff = r.stdout;
			truncated ||= r.truncated ?? false;
		} else if (staged) {
			const r = await execRead(["diff", "--cached", "--", filePath], worktreePath, true);
			diff = r.stdout;
			truncated ||= r.truncated ?? false;
		} else {
			const r = await execRead(["diff", "--", filePath], worktreePath, true);
			diff = r.stdout;
			truncated ||= r.truncated ?? false;
		}

		if (diff.length > maxBytes) {
			diff = diff.slice(0, maxBytes);
			truncated = true;
		}
		return { diff, truncated };
	},

	// === Commit boundaries ===

	/**
	 * When each of `paths` was last committed, plus the walk's oldest boundary.
	 *
	 * This answers "where does the current uncommitted change to this file begin",
	 * which is what separates *this* round of edits from the file's whole history. A
	 * single repository-wide HEAD timestamp cannot answer it: in this repository 90 of
	 * 127 changed files were last committed BEFORE HEAD, by a median of 164 hours, so a
	 * global boundary silently discards real attributions.
	 *
	 * One bounded walk serves every path. `--max-count` caps the work: beyond a few
	 * hundred commits the walk stops finding new paths (200 and 800 both resolved the
	 * same 90 files here) while output keeps growing.
	 *
	 * A path absent from `byPath` has no commit in the window. That is either a file
	 * that was never committed (untracked — every attribution belongs to the current
	 * change) or one whose last commit is older than the window. The two are
	 * indistinguishable from the walk alone, so callers use `oldestInWindow` for
	 * tracked paths: being NEWER than the true boundary, it can only under-count, and
	 * over-crediting a file with unrelated history is the failure that matters.
	 *
	 * Precision caveat: `%cI` is second-resolution, so a change recorded in the SAME
	 * second as the commit cannot be placed on either side of it and will be counted as
	 * part of the current round. The error is bounded by one second and errs toward
	 * showing a contributor rather than hiding one, which is the safer direction for a
	 * display that answers "who touched this".
	 *
	 * @param paths  Repo-relative paths. Capped at {@link COMMIT_BOUNDARY_MAX_PATHS};
	 *               the excess simply falls back to `oldestInWindow`.
	 */
	async getLastCommitTimes(
		worktreePath: string,
		paths: string[],
	): Promise<{ byPath: Map<string, string>; oldestInWindow: string | null }> {
		const byPath = new Map<string, string>();
		if (paths.length === 0) return { byPath, oldestInWindow: null };
		// An unborn HEAD has nothing to walk, and `git log` would fail rather than
		// report zero commits.
		if (await hasNoCommits(worktreePath)) return { byPath, oldestInWindow: null };

		const capped = paths.slice(0, COMMIT_BOUNDARY_MAX_PATHS);
		const result = await execRead(
			[
				// Prevent git from quoting non-ASCII paths as octal escape sequences
				// (e.g. "\344\270\255..."). Without this, CJK and other non-ASCII filenames
				// won't match the bare path strings callers pass in, causing boundary misses
				// that fall back to the less precise `oldestInWindow`.
				"-c",
				"core.quotePath=false",
				"log",
				"HEAD",
				`--max-count=${COMMIT_BOUNDARY_MAX_COMMITS}`,
				// A commit line is prefixed so it cannot be confused with a file path;
				// `--name-only` prints paths bare.
				`--format=${COMMIT_BOUNDARY_MARKER_FORMAT}%cI`,
				"--name-only",
				"--",
				...capped,
			],
			worktreePath,
			true,
		);
		if (result.exitCode !== 0) return { byPath, oldestInWindow: null };

		let current: string | null = null;
		let oldestInWindow: string | null = null;
		for (const rawLine of result.stdout.split("\n")) {
			const line = rawLine.trim();
			if (!line) continue;
			if (line.startsWith(COMMIT_BOUNDARY_MARKER)) {
				current = toUtcIso(line.slice(COMMIT_BOUNDARY_MARKER.length));
				// Walk order is newest-first, so the last commit seen is the oldest.
				if (current) oldestInWindow = current;
				continue;
			}
			// First sighting wins: the newest commit that touched this path.
			if (current && !byPath.has(line)) byPath.set(line, current);
		}

		return { byPath, oldestInWindow };
	},

	// === Log ===

	async getLog(
		worktreePath: string,
		opts: { limit?: number; skip?: number; branch?: string } = {},
	): Promise<
		Array<{
			sha: string;
			shortSha: string;
			message: string;
			author: string;
			date: string;
			/** git %P：父提交完整 SHA 数组；根提交为 []。 */
			parents: string[];
		}>
	> {
		const args = [
			"log",
			`--max-count=${opts.limit ?? 50}`,
			`--skip=${opts.skip ?? 0}`,
			"--format=%H%x00%h%x00%s%x00%an%x00%aI%x00%P",
		];
		if (opts.branch) args.push(opts.branch);
		const result = await execRead(args, worktreePath, true);
		if (result.truncated)
			throw new GitError("Git history exceeds its output budget; reduce the page size");
		if (result.exitCode !== 0) {
			if (await hasNoCommits(worktreePath)) return [];
			throw new GitError(gitFailureMessage("Git history failed", result));
		}
		if (!result.stdout.trim()) return [];
		return result.stdout
			.trim()
			.split("\n")
			.map((line) => {
				const [sha, shortSha, message, author, date, parentsRaw] = line.split("\0");
				return {
					sha: sha ?? "",
					shortSha: shortSha ?? "",
					message: message ?? "",
					author: author ?? "",
					date: date ?? "",
					parents: (parentsRaw ?? "").trim().split(/\s+/).filter(Boolean),
				};
			});
	},

	// === Reset ===

	async resetSoft(worktreePath: string, target: string): Promise<void> {
		return withWorktreeLock(worktreePath, async () => {
			const result = await exec(["reset", "--soft", target], worktreePath);
			if (result.exitCode !== 0) throw new GitError(`git reset --soft failed: ${result.stderr}`);
		});
	},

	async resetHard(worktreePath: string, target: string): Promise<void> {
		return withWorktreeLock(worktreePath, async () => {
			const result = await exec(["reset", "--hard", target], worktreePath);
			if (result.exitCode !== 0) throw new GitError(`git reset --hard failed: ${result.stderr}`);
		});
	},
};
