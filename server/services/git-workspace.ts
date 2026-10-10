import { createHash } from "node:crypto";
import { realpath, stat } from "node:fs/promises";
import type { GitWorkspace, GitWorkspaceState } from "@shared/git-workspace";
import { eq } from "drizzle-orm";
import { db } from "../db";
import { chapters, narrators, projects } from "../db/schema";
import type { ExecutionBackend } from "../lib/agent/execution/backend";
import { localBackend, resolveBackend } from "../lib/agent/execution/registry";
import { AppError, NotFoundError, ValidationError } from "../lib/errors";
import { logger } from "../lib/logger";
import { getHome } from "../lib/platform";
import { normalizePathForComparison } from "../lib/platform-path";
import { safeSpawn } from "../lib/spawn";
import { gitDiscoveryCache } from "./git-discovery-cache";
import {
	discoverGitWorkspacePaths,
	GIT_WORKSPACE_DISCOVERY_BUDGET_MS,
} from "./git-workspace-scanner";
import { resolveNarratorSessionCwd } from "./narrator-cwd";
import { activeNarrators } from "./narrator-session-state";
import { createRemoteGitService, supportsRemoteGitWorkspace } from "./remote-git-service";

/** Legacy path consumers retain their local path key; management uses device/root keys. */
export interface ResolvedWorkspace {
	workspacePath: string;
	rawPath: string;
	chapterId?: string;
	projectId?: string;
	baseBranch?: string;
}

export function normalizeWorkspacePath(path: string): string {
	return normalizePathForComparison(path);
}

export const GIT_PROBE_TIMEOUT_MS = 10_000;
export const GIT_PROBE_MAX_BYTES = 32 * 1024;

export interface GitProbe {
	state: GitWorkspaceState;
	rootPath?: string;
	repositoryPath?: string;
	reason?: string;
	branch?: string | null;
}

/** Real Git probing handles unborn repositories, linked worktrees, submodules and bare repos. */
export async function probeLocalGitWorkspace(cwd: string, signal?: AbortSignal): Promise<GitProbe> {
	try {
		if (!(await stat(cwd)).isDirectory()) return { state: "missing_directory" };
	} catch (error) {
		return {
			state:
				(error as NodeJS.ErrnoException).code === "EACCES" ? "access_denied" : "missing_directory",
		};
	}
	try {
		const result = await safeSpawn({
			cmd: [
				"git",
				"--no-optional-locks",
				"-C",
				cwd,
				"rev-parse",
				"--is-inside-work-tree",
				"--is-bare-repository",
				"--path-format=absolute",
				"--show-toplevel",
				"--git-common-dir",
			],
			// Classify Git failures independently of the host's locale.
			env: { ...process.env, LC_ALL: "C", LANG: "C" },
			timeout: GIT_PROBE_TIMEOUT_MS,
			maxOutputBytes: GIT_PROBE_MAX_BYTES,
			signal,
		});
		signal?.throwIfAborted();
		if (result.stdoutTruncated || result.stderrTruncated)
			return { state: "unsupported", reason: "Git workspace probe exceeded its output budget" };
		const [inside, bare, root, repository] = result.stdout.trim().split("\n");
		if (bare === "true")
			return { state: "unsupported", reason: "Bare repositories have no working tree" };
		if (result.exitCode !== 0 || inside !== "true" || !root || !repository) {
			if (/dubious ownership|permission denied|access denied/i.test(result.stderr))
				return { state: "access_denied", reason: "Git refused access to this working tree" };
			return /not a git repository/i.test(result.stderr)
				? { state: "not_git", reason: "Directory is not a Git working tree" }
				: { state: "unsupported", reason: "Git workspace probe did not complete" };
		}
		return {
			state: "ready",
			rootPath: await realpath(root),
			repositoryPath: await realpath(repository),
		};
	} catch (error) {
		signal?.throwIfAborted();
		return {
			state: /ENOENT|not found/i.test(String(error)) ? "git_unavailable" : "unsupported",
			reason: "Git workspace probe failed",
		};
	}
}

export async function probeGitWorkspace(
	backend: ExecutionBackend,
	cwd: string,
	signal?: AbortSignal,
): Promise<GitProbe> {
	const result =
		backend.kind === "local"
			? await probeLocalGitWorkspace(cwd, signal)
			: await createRemoteGitService(backend, signal).probe(cwd);
	return { ...result, state: result.state ?? "unsupported" };
}

function isDiscoveredRepositoryRoot(
	backend: ExecutionBackend,
	basePath: string,
	candidatePath: string,
	rootPath: string,
): boolean {
	if (!backend.paths.equals(candidatePath, rootPath) || !backend.paths.contains(basePath, rootPath))
		return false;
	const relative = backend.paths.relative(basePath, rootPath).replaceAll("\\", "/");
	const depth = relative.split("/").filter(Boolean).length;
	return !!relative && !relative.startsWith("../") && depth >= 1 && depth <= 3;
}

export function gitWorkspaceIdentity(
	backend: Pick<ExecutionBackend, "deviceId" | "paths">,
	root: string,
): string {
	return createHash("sha256")
		.update(JSON.stringify([backend.deviceId, backend.paths.identityKey(root)]))
		.digest("hex");
}

export interface GitWorkspaceTarget {
	workspace: GitWorkspace;
	backend?: ExecutionBackend;
	repositoryPath?: string;
	/** Associated project is for authorization even when cwd overrides the chapter. */
	contextProjectId?: string;
	/** The local session cwd is NOT used as a remote cwd fallback. */
	configuredCwd?: string;
	/** Discovered candidates are internal until each one passes authorization. */
	discoveredRepositories?: GitWorkspaceTarget[];
	discoveredRepositoriesAuthorized?: boolean;
	discoveryTruncated?: boolean;
	discoveryDeadline?: number;
	discoveryBasePath?: string;
	/** Request-bound authorization refresh, never serialized to clients. */
	beforeWrite?: () => Promise<void>;
}

export async function resolveNarratorGitTarget(
	narratorId: string,
	signal?: AbortSignal,
	beforeResolve?: (target: GitWorkspaceTarget) => Promise<void>,
	beforeProbe?: (target: GitWorkspaceTarget) => Promise<void>,
	beforeCandidateProbe?: (target: GitWorkspaceTarget) => Promise<boolean>,
	authorizeCandidate?: (
		target: GitWorkspaceTarget,
		deadline: number,
	) => Promise<"authorized" | "denied" | "budget_exhausted">,
	discoverNested = false,
	/** Display discovery only; authoritative reads/writes keep the uncached probe. */
	cachedDiscovery: boolean | "cache-only" = false,
): Promise<GitWorkspaceTarget> {
	const narrator = await db.query.narrators.findFirst({
		where: eq(narrators.id, narratorId),
		columns: {
			id: true,
			cwd: true,
			workspaceRevision: true,
			workspaceContext: true,
			chapterId: true,
			contextProjectId: true,
			defaultDeviceId: true,
		},
	});
	if (!narrator) throw new NotFoundError("Narrator", narratorId);
	const chapter = narrator.chapterId
		? await db.query.chapters.findFirst({
				where: eq(chapters.id, narrator.chapterId),
				columns: { id: true, projectId: true, worktreePath: true },
			})
		: undefined;
	if (narrator.chapterId && !chapter) throw new NotFoundError("Chapter", narrator.chapterId);
	const projectId = chapter?.projectId ?? narrator.contextProjectId ?? undefined;
	const project = projectId
		? await db.query.projects.findFirst({
				where: eq(projects.id, projectId),
				columns: { gitPath: true },
			})
		: undefined;
	if (projectId && !project) throw new NotFoundError("Project", projectId);
	const active = activeNarrators.get(narratorId);
	const committedContext =
		narrator.workspaceContext?.revision === narrator.workspaceRevision &&
		narrator.workspaceContext.deviceId === "local"
			? narrator.workspaceContext
			: null;
	const localCwd =
		committedContext?.cwd ??
		active?.cwd ??
		resolveNarratorSessionCwd(narrator.cwd, chapter?.worktreePath, project?.gitPath, getHome());
	const deviceId = active
		? (active._defaultDeviceId ?? "local")
		: (narrator.defaultDeviceId ?? "local");
	const workspace: GitWorkspace = {
		narratorId,
		projectId,
		deviceId,
		cwd: deviceId === "local" ? localCwd : "",
		rootPath: null,
		workspaceKey: null,
		repositoryKey: null,
		state: "device_offline",
		capabilities: { read: false, write: false },
	};
	// No backend resolution, device RPC or local Git probe is allowed before this access gate.
	await beforeResolve?.({ workspace, contextProjectId: projectId, configuredCwd: localCwd });
	let backend: ExecutionBackend;
	try {
		backend = resolveBackend({ requested: deviceId });
	} catch {
		return { workspace, contextProjectId: projectId, configuredCwd: localCwd };
	}
	// Match toolBaseCwd: remote tools use the executor's cwd, never the host-side
	// narrator.cwd. Local tools use the live session cwd, then the persisted override.
	workspace.cwd = backend.kind === "remote" ? (backend.defaultCwd ?? "") : localCwd;
	const target: GitWorkspaceTarget = {
		workspace,
		backend,
		contextProjectId: projectId,
		configuredCwd: localCwd,
	};
	// Legacy standalone chats may only inherit Home as the agent's execution fallback.
	// That is not a selected Git workspace: probing it would hit the private-path guard.
	if (
		backend.kind === "local" &&
		!committedContext?.cwd &&
		!narrator.cwd &&
		!chapter?.worktreePath &&
		!project?.gitPath &&
		backend.paths.equals(workspace.cwd, getHome())
	) {
		workspace.state = "unsupported";
		workspace.reason = "Select a working directory to enable Git discovery";
		return target;
	}
	if (!workspace.cwd || !backend.paths.isAbsolute(workspace.cwd)) {
		workspace.state = "missing_directory";
		workspace.reason = "Execution device has no absolute working directory";
		return target;
	}
	const cachedProbe =
		cachedDiscovery === "cache-only"
			? gitDiscoveryCache.peek(
					backend.deviceId,
					backend.runtimeGeneration,
					backend.paths.identityKey(workspace.cwd),
				)
			: undefined;
	// A detail read must not enter path authorization's filesystem work on a miss.
	if (cachedDiscovery === "cache-only" && !cachedProbe)
		throw new AppError("Git discovery is not cached", 409, "GIT_DISCOVERY_CACHE_MISS");
	// Cached facts never substitute for current cwd/root authorization.
	await beforeProbe?.(target);
	if (backend.kind === "remote" && !supportsRemoteGitWorkspace(backend)) {
		workspace.state = "unsupported";
		workspace.reason = "Upgrade the remote executor to support Git workspace management";
		return target;
	}
	let probe: GitProbe;
	try {
		const discover = async (probeSignal?: AbortSignal): Promise<GitProbe> => {
			const result =
				backend.kind === "local"
					? await probeLocalGitWorkspace(workspace.cwd, probeSignal)
					: await createRemoteGitService(backend, probeSignal).probe(
							workspace.cwd,
							probeSignal,
							cachedDiscovery
								? { maxBytes: GIT_PROBE_MAX_BYTES, timeoutMs: GIT_PROBE_TIMEOUT_MS }
								: {},
						);
			const facts: GitProbe = { ...result, state: result.state ?? "unsupported" };
			if (facts.state === "ready" && (!facts.rootPath || !facts.repositoryPath))
				return { state: "unsupported", reason: "Incomplete Git workspace discovery" };
			if (cachedDiscovery && facts.state === "ready" && backend.kind === "local") {
				const head = await safeSpawn({
					cmd: [
						"git",
						"--no-optional-locks",
						"-C",
						workspace.cwd,
						"symbolic-ref",
						"--quiet",
						"--short",
						"HEAD",
					],
					timeout: GIT_PROBE_TIMEOUT_MS,
					maxOutputBytes: GIT_PROBE_MAX_BYTES,
					signal: probeSignal,
				});
				facts.branch = !head.stdoutTruncated && head.exitCode === 0 ? head.stdout.trim() : null;
			}
			probeSignal?.throwIfAborted();
			return facts;
		};
		probe =
			cachedProbe ??
			(cachedDiscovery === true
				? await gitDiscoveryCache.get(
						backend.deviceId,
						backend.runtimeGeneration,
						backend.paths.identityKey(workspace.cwd),
						() => discover(AbortSignal.timeout(GIT_PROBE_TIMEOUT_MS)),
						signal,
					)
				: await discover(signal));
		signal?.throwIfAborted();
	} catch (error) {
		signal?.throwIfAborted();
		workspace.state = /offline|connection|unavailable|disconnect/i.test(String(error))
			? "device_offline"
			: /denied|forbidden|path.*rule|outside/i.test(String(error))
				? "access_denied"
				: "unsupported";
		workspace.reason = "Execution device could not resolve the Git working tree";
		return target;
	}
	workspace.state = probe.state ?? "unsupported";
	workspace.reason = probe.reason;
	if (cachedDiscovery) workspace.branch = probe.branch ?? null;
	if (probe.state === "ready" && probe.rootPath && probe.repositoryPath) {
		workspace.rootPath = probe.rootPath;
		target.repositoryPath = probe.repositoryPath;
		workspace.workspaceKey = gitWorkspaceIdentity(backend, probe.rootPath);
		workspace.repositoryKey = gitWorkspaceIdentity(backend, probe.repositoryPath);
		// A chapter is only a side-effect adapter for its *actual* worktree.
		if (backend.kind === "local" && chapter?.worktreePath) {
			const chapterRoot = await realpath(chapter.worktreePath).catch(() => null);
			if (chapterRoot && backend.paths.equals(chapterRoot, probe.rootPath))
				workspace.chapterId = chapter.id;
		}
		return target;
	}
	if (probe.state !== "not_git" || !discoverNested || cachedDiscovery === "cache-only")
		return target;

	const discoveryStarted = performance.now();
	const discoveryDeadline = discoveryStarted + GIT_WORKSPACE_DISCOVERY_BUDGET_MS;
	target.discoveryDeadline = discoveryDeadline;
	const discoveryController = new AbortController();
	const relayAbort = () => discoveryController.abort(signal?.reason);
	signal?.addEventListener("abort", relayAbort, { once: true });
	if (signal?.aborted) relayAbort();
	const discoveryTimer = setTimeout(
		() => discoveryController.abort(new Error("Git repository discovery timed out")),
		GIT_WORKSPACE_DISCOVERY_BUDGET_MS,
	);
	try {
		const baseIdentity = await backend.resolvePathIdentity(workspace.cwd, {
			signal: discoveryController.signal,
			timeoutMs: GIT_WORKSPACE_DISCOVERY_BUDGET_MS,
		});
		target.discoveryBasePath = baseIdentity.canonicalPath;
		const discovery = await discoverGitWorkspacePaths(
			backend,
			workspace.cwd,
			discoveryController.signal,
		);
		target.discoveryTruncated = discovery.truncated;
		if (discovery.unsupported) return target;
		const repositories: GitWorkspaceTarget[] = [];
		const seenWorkspaceKeys = new Set<string>();
		target.discoveredRepositories = repositories;
		target.discoveredRepositoriesAuthorized = authorizeCandidate !== undefined;
		for (const cwd of discovery.paths) {
			const candidate: GitWorkspaceTarget = {
				backend,
				contextProjectId: projectId,
				configuredCwd: localCwd,
				workspace: {
					narratorId,
					projectId,
					deviceId,
					cwd,
					rootPath: null,
					workspaceKey: null,
					repositoryKey: null,
					state: "not_git",
					capabilities: { read: false, write: false },
				},
			};
			if (beforeCandidateProbe && !(await beforeCandidateProbe(candidate))) continue;
			let candidateIdentity: Awaited<ReturnType<ExecutionBackend["resolvePathIdentity"]>>;
			let candidateProbe: GitProbe;
			try {
				candidateIdentity = await backend.resolvePathIdentity(cwd, {
					signal: discoveryController.signal,
					timeoutMs: Math.max(1, Math.floor(discoveryDeadline - performance.now())),
				});
				candidateProbe = await probeGitWorkspace(backend, cwd, discoveryController.signal);
			} catch {
				discoveryController.signal.throwIfAborted();
				continue;
			}
			if (
				candidateProbe.state !== "ready" ||
				!candidateProbe.rootPath ||
				!candidateProbe.repositoryPath ||
				!candidateIdentity.exists ||
				!isDiscoveredRepositoryRoot(
					backend,
					baseIdentity.canonicalPath,
					candidateIdentity.canonicalPath,
					candidateProbe.rootPath,
				)
			)
				continue;
			const workspaceKey = gitWorkspaceIdentity(backend, candidateProbe.rootPath);
			if (seenWorkspaceKeys.has(workspaceKey)) continue;
			candidate.workspace = {
				...candidate.workspace,
				cwd,
				rootPath: candidateProbe.rootPath,
				workspaceKey,
				repositoryKey: gitWorkspaceIdentity(backend, candidateProbe.repositoryPath),
				state: "ready",
				reason: undefined,
			};
			candidate.repositoryPath = candidateProbe.repositoryPath;
			if (authorizeCandidate) {
				const authorization = await authorizeCandidate(candidate, discoveryDeadline);
				if (authorization === "budget_exhausted") {
					target.discoveryTruncated = true;
					break;
				}
				if (authorization === "denied") continue;
			}
			seenWorkspaceKeys.add(workspaceKey);
			repositories.push(candidate);
		}
		target.discoveredRepositories = repositories;
	} catch (error) {
		signal?.throwIfAborted();
		if (!discoveryController.signal.aborted) throw error;
		target.discoveryTruncated = true;
		workspace.reason = "Directory is not a Git working tree; repository discovery was incomplete";
	} finally {
		clearTimeout(discoveryTimer);
		signal?.removeEventListener("abort", relayAbort);
		const elapsedMs = Math.round(performance.now() - discoveryStarted);
		if (elapsedMs >= 1000)
			logger.warn("Slow Git repository discovery", {
				deviceId: backend.deviceId,
				elapsedMs,
				candidates: target.discoveredRepositories?.length ?? 0,
				truncated: target.discoveryTruncated === true,
			});
	}
	return target;
}

export async function resolveWorkspaceFromChapter(chapterId: string): Promise<ResolvedWorkspace> {
	const ch = await db.query.chapters.findFirst({ where: eq(chapters.id, chapterId) });
	if (!ch) throw new NotFoundError("Chapter", chapterId);
	if (!ch.worktreePath) throw new ValidationError("Chapter has no active worktree");
	return {
		workspacePath: normalizeWorkspacePath(ch.worktreePath),
		rawPath: ch.worktreePath,
		chapterId: ch.id,
		projectId: ch.projectId,
		baseBranch: ch.baseBranch,
	};
}

export async function resolveChapterGitTarget(
	chapterId: string,
	signal?: AbortSignal,
): Promise<GitWorkspaceTarget> {
	const legacy = await resolveWorkspaceFromChapter(chapterId);
	const probe = await probeLocalGitWorkspace(legacy.rawPath, signal);
	const root = probe.rootPath ?? null;
	return {
		backend: localBackend,
		repositoryPath: probe.repositoryPath,
		contextProjectId: legacy.projectId,
		workspace: {
			cwd: legacy.rawPath,
			deviceId: "local",
			rootPath: root,
			workspaceKey: root ? gitWorkspaceIdentity(localBackend, root) : null,
			repositoryKey: probe.repositoryPath
				? gitWorkspaceIdentity(localBackend, probe.repositoryPath)
				: null,
			state: probe.state,
			reason: probe.reason,
			capabilities: { read: false, write: false },
			chapterId,
			projectId: legacy.projectId,
		},
	};
}

export async function resolveWorkspaceFromNarrator(
	narratorId: string,
): Promise<ResolvedWorkspace | null> {
	const { workspace } = await resolveNarratorGitTarget(narratorId);
	if (workspace.state !== "ready" || !workspace.rootPath || !workspace.workspaceKey) return null;
	return {
		rawPath: workspace.rootPath,
		workspacePath:
			workspace.deviceId === "local"
				? normalizeWorkspacePath(workspace.rootPath)
				: workspace.workspaceKey,
		chapterId: workspace.chapterId,
		projectId: workspace.projectId,
	};
}

export function assertGitWorkspaceKey(workspace: GitWorkspace, expected: unknown): void {
	if (typeof expected !== "string" || !expected || expected.length > 256)
		throw new ValidationError("workspaceKey is required");
	if (workspace.workspaceKey !== expected)
		throw new AppError(
			"Git workspace changed; refresh before retrying",
			409,
			"GIT_WORKSPACE_CHANGED",
		);
}

export function resolveWorkspaceFromPath(rawPath: string): ResolvedWorkspace {
	return { workspacePath: normalizeWorkspacePath(rawPath), rawPath };
}
