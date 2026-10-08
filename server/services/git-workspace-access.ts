import { realpath } from "node:fs/promises";
import { and, asc, eq, gt, inArray, isNull, ne, or, sql } from "drizzle-orm";
import type { Context } from "hono";
import { db } from "../db";
import { chapters, projects, remoteDevices } from "../db/schema";
import { AppError } from "../lib/errors";
import { isSecretPlatformPath, isSecretUserPath } from "../lib/fs-secret-paths";
import { logger } from "../lib/logger";
import { narratorPrincipalOf } from "../lib/narrator-access";
import { getHome } from "../lib/platform";
import { narraforkDir } from "../lib/settings";
import { isDeviceAuthorized } from "./device-service";
import { executionPolicyEngine } from "./execution-policy/engine";
import type { ExecutionDeviceClass, ExecutionTargetContext } from "./execution-policy/types";
import {
	type GitWorkspaceTarget,
	resolveChapterGitTarget,
	resolveNarratorGitTarget,
} from "./git-workspace";
import { ACCESS_PAGE_SIZE, GitAccessScan } from "./git-workspace-access-scan";
import { gitPathPolicyAllows } from "./git-workspace-path-policy";
import { integrationResourceBindingService } from "./integration-resource-binding-service";
import { canWriteNarrator, loadNarratorForAccess, type NarratorPrincipal } from "./narrator-acl";
import { resolveOAuthDeviceRuntimeAuthorization } from "./oauth-device-runtime-policy";
import { resolveOAuthNarratorRuntimePolicy } from "./oauth-narrator-runtime-policy";
import { assertChapterProjectAccess, hasProjectAccess, loadProjectForAccess } from "./project-acl";

const denied = () =>
	new AppError("Git workspace access denied", 403, "GIT_WORKSPACE_ACCESS_DENIED");

function isAccessScanUnavailable(error: unknown): boolean {
	return error instanceof AppError && error.code === "GIT_WORKSPACE_ACCESS_CHECK_UNAVAILABLE";
}

function redactDeniedTarget(target: GitWorkspaceTarget): void {
	const { workspace } = target;
	workspace.state = "access_denied";
	workspace.reason = "Current permissions do not allow management of the Git working tree";
	workspace.capabilities = { read: false, write: false };
	// A public narrator must not disclose a private repository path or stable hash.
	workspace.cwd = "";
	workspace.rootPath = null;
	workspace.workspaceKey = null;
	workspace.repositoryKey = null;
	delete workspace.chapterId;
	delete workspace.projectId;
	delete target.repositoryPath;
	delete workspace.branch;
}

async function requireSafeLocalGitPath(target: GitWorkspaceTarget, path: string): Promise<void> {
	const backend = target.backend;
	if (!backend || backend.kind !== "local") return;
	const [home, nf] = await Promise.all([
		realpath(getHome()).catch(() => getHome()),
		realpath(narraforkDir).catch(() => narraforkDir),
	]);
	if (
		isSecretPlatformPath(path, nf) ||
		isSecretUserPath(path, home) ||
		backend.paths.contains(path, nf) ||
		[".ssh", ".aws", ".gnupg", ".kube"].some((part) =>
			backend.paths.contains(path, backend.paths.resolve(home, part)),
		)
	)
		throw denied();
}

export { gitPathPolicyAllows } from "./git-workspace-path-policy";

function ancestors(path: string, target: GitWorkspaceTarget): string[] {
	const paths = target.backend?.paths;
	if (!paths) return [path];
	const result = [path];
	while (result.length < 128) {
		const parent = paths.dirname(result[result.length - 1]);
		if (paths.equals(parent, result[result.length - 1])) break;
		result.push(parent);
	}
	return result;
}

/** Bound both rows and fields. Standalone cwd must not bypass a protected project's ACL. */
async function relatedProjects(
	target: GitWorkspaceTarget,
	scan: GitAccessScan,
	principal: NarratorPrincipal,
) {
	if (
		principal.isAdmin ||
		!target.backend ||
		target.backend.kind !== "local" ||
		!target.workspace.rootPath
	)
		return [];
	// Owner/admin always pass both project gates. Their associations cannot narrow
	// capabilities, so do not enumerate/realpath their entire project inventory on
	// first paint. Foreign (including null-owner) aliases still fail closed and
	// receive fresh ACL checks; no permission verdict is cached.
	const foreignProject = or(
		isNull(projects.ownerUserId),
		ne(projects.ownerUserId, principal.userId),
	);
	const root = target.workspace.rootPath;
	const candidates = new Set([
		...ancestors(root, target),
		...ancestors(target.workspace.cwd, target),
		...(target.repositoryPath
			? ancestors(target.backend.paths.dirname(target.repositoryPath), target)
			: []),
	]);
	const escaped = `${root.replace(/[\\%_]/g, "\\$&")}/%`;
	const fields = {
		id: projects.id,
		ownerUserId: projects.ownerUserId,
		visibility: projects.visibility,
		gitPath: projects.gitPath,
	};
	type Project = Pick<typeof projects.$inferSelect, keyof typeof fields>;
	const matches = new Map<string, Project>();
	for await (const page of scan.pagesOf((cursor) =>
		db
			.select(fields)
			.from(projects)
			.where(
				and(
					foreignProject,
					cursor ? gt(projects.id, cursor) : undefined,
					or(
						inArray(projects.gitPath, [...candidates]),
						sql`${projects.gitPath} like ${escaped} escape '\\'`,
					),
				),
			)
			.orderBy(asc(projects.id))
			.limit(ACCESS_PAGE_SIZE),
	)) {
		for (const row of page) matches.set(row.id, row);
	}
	for await (const page of scan.pagesOf((cursor) =>
		db
			.select({ id: chapters.id, projectId: chapters.projectId })
			.from(chapters)
			.where(
				and(
					cursor ? gt(chapters.id, cursor) : undefined,
					inArray(chapters.worktreePath, [...candidates]),
				),
			)
			.orderBy(asc(chapters.id))
			.limit(ACCESS_PAGE_SIZE),
	)) {
		const missing = [...new Set(page.map((row) => row.projectId))].filter((id) => !matches.has(id));
		if (!missing.length) continue;
		const rows = await scan.run(() =>
			db
				.select(fields)
				.from(projects)
				.where(and(foreignProject, inArray(projects.id, missing)))
				.limit(ACCESS_PAGE_SIZE),
		);
		for (const row of rows) matches.set(row.id, row);
	}
	// Registered symlink aliases can occur anywhere in the project table.
	const paths = target.backend.paths;
	for await (const page of scan.pagesOf((cursor) =>
		db
			.select(fields)
			.from(projects)
			.where(and(foreignProject, cursor ? gt(projects.id, cursor) : undefined))
			.orderBy(asc(projects.id))
			.limit(ACCESS_PAGE_SIZE),
	)) {
		await scan.canonicalize(
			page.filter((row) => !matches.has(row.id)),
			(project, path) => {
				if (
					paths.contains(path, root) ||
					paths.contains(root, path) ||
					(target.repositoryPath && paths.contains(path, target.repositoryPath))
				)
					matches.set(project.id, project);
			},
		);
	}
	return [...matches.values()];
}

function selectGitTarget(target: GitWorkspaceTarget, selected: GitWorkspaceTarget): void {
	const cwd = target.workspace.cwd;
	target.workspace = {
		...selected.workspace,
		cwd,
		repositories: target.workspace.repositories,
		discoveryTruncated: target.workspace.discoveryTruncated,
	};
	target.repositoryPath = selected.repositoryPath;
}

async function authorizeResolvedGitCandidate(
	target: GitWorkspaceTarget,
	need: "read" | "write",
	principal: ReturnType<typeof narratorPrincipalOf>,
	scan: GitAccessScan,
	canWriteBase: boolean,
	narratorExecution: {
		context: ExecutionTargetContext | null;
		policy: Awaited<ReturnType<typeof executionPolicyEngine.compile>> | null;
	},
	policy: Awaited<ReturnType<typeof resolveOAuthNarratorRuntimePolicy>>,
): Promise<boolean> {
	const { workspace, backend } = target;
	if (workspace.state !== "ready" || !backend || !workspace.rootPath) return false;
	let canWrite = canWriteBase;
	try {
		for (const project of await relatedProjects(target, scan, principal)) {
			if (!(await scan.run(() => hasProjectAccess(project, principal, "read")))) throw denied();
			canWrite &&= await scan.run(() => hasProjectAccess(project, principal, "write"));
		}
		await scan.run(() => requireSafeLocalGitPath(target, workspace.rootPath as string));
		if (narratorExecution.context || narratorExecution.policy) {
			const context = narratorExecution.context;
			const compiled = narratorExecution.policy;
			if (!context || !compiled) throw denied();
			if (!gitPathPolicyAllows(compiled, context, workspace.rootPath, "read")) throw denied();
			canWrite &&= gitPathPolicyAllows(compiled, context, workspace.rootPath, "write");
			if (target.repositoryPath) {
				if (!gitPathPolicyAllows(compiled, context, target.repositoryPath, "read")) throw denied();
				canWrite &&= gitPathPolicyAllows(compiled, context, target.repositoryPath, "write");
			}
			if (policy && policy.permissionMode !== "bypassPermissions") {
				if (
					compiled.evaluatePath({ path: workspace.rootPath, operation: "read" }).decision !==
						"allow" &&
					!backend.paths.contains(context.target.cwd, workspace.rootPath)
				)
					throw denied();
				canWrite &&=
					compiled.evaluatePath({ path: workspace.rootPath, operation: "write" }).decision ===
					"allow";
			}
		}
		if (need === "write" && !canWrite) throw denied();
		scan.check();
		workspace.capabilities = { read: true, write: canWrite };
		return true;
	} catch (error) {
		if (!(error instanceof AppError) || error.statusCode !== 403) throw error;
		return false;
	}
}

export async function authorizeGitTarget(
	c: Context,
	source: { narratorId: string } | { chapterId: string },
	need: "read" | "write",
	requestedWorkspaceKey?: string,
	discoverNested = false,
	cachedDiscovery: boolean | "cache-only" = false,
): Promise<GitWorkspaceTarget> {
	return authorizeGitTargetForPrincipal(
		narratorPrincipalOf(c),
		source,
		need,
		c.req.raw.signal,
		requestedWorkspaceKey,
		discoverNested,
		cachedDiscovery,
	);
}

/** Shared by HTTP and authenticated workspace WebSocket subscriptions. */
export async function authorizeGitTargetForPrincipal(
	principal: NarratorPrincipal,
	source: { narratorId: string } | { chapterId: string },
	need: "read" | "write",
	signal: AbortSignal,
	requestedWorkspaceKey?: string,
	discoverNested = false,
	cachedDiscovery: boolean | "cache-only" = false,
): Promise<GitWorkspaceTarget> {
	let canWriteBase = true;
	let policy: Awaited<ReturnType<typeof resolveOAuthNarratorRuntimePolicy>> = null;
	let deviceClass: ExecutionDeviceClass | null = null;
	const narratorExecution: {
		context: ExecutionTargetContext | null;
		policy: Awaited<ReturnType<typeof executionPolicyEngine.compile>> | null;
	} = { context: null, policy: null };
	let discoveryAccessScan: GitAccessScan | undefined;
	let target: GitWorkspaceTarget;
	if ("narratorId" in source) {
		const narrator = await loadNarratorForAccess(source.narratorId, principal, need);
		canWriteBase = await canWriteNarrator(narrator, principal);
		policy = await resolveOAuthNarratorRuntimePolicy(source.narratorId);
		target = await resolveNarratorGitTarget(
			source.narratorId,
			signal,
			async (initial) => {
				if (initial.contextProjectId) {
					const project = await loadProjectForAccess(initial.contextProjectId, principal, need);
					canWriteBase &&= await hasProjectAccess(project, principal, "write");
				}
				const id = initial.workspace.deviceId;
				if (id !== "local") {
					const device = await db.query.remoteDevices.findFirst({
						where: eq(remoteDevices.id, id),
					});
					if (
						!device ||
						device.revokedAt ||
						!isDeviceAuthorized(device, {
							userId: principal.userId,
							projectId: initial.contextProjectId,
						}) ||
						!(await resolveOAuthDeviceRuntimeAuthorization(device)).allowed
					)
						throw denied();
				}
				if (policy) {
					if (id === "local" ? !policy.allowLocalExecution : !policy.deviceIds.includes(id))
						throw denied();
					const binding =
						id === "local" ? null : await integrationResourceBindingService.get("device", id);
					deviceClass =
						id === "local"
							? "host"
							: binding?.sourceType === "oauth_client" &&
									binding.sourceId === policy.clientId &&
									binding.authorityId === policy.grantId
								? "selfRegistered"
								: "global";
					const level = policy.policy.deviceAccess[deviceClass];
					if (level === "denied" || !policy.allowedTools.has("Read")) throw denied();
					canWriteBase &&= level === "readWrite" && policy.allowedTools.has("Bash");
					if (need === "write" && !canWriteBase) throw denied();
				}
			},
			async (initial) => {
				const { backend, workspace } = initial;
				if (!backend) throw denied();
				await requireSafeLocalGitPath(initial, workspace.cwd);
				narratorExecution.context = {
					backend,
					paths: backend.paths,
					deviceClass,
					target: {
						deviceId: workspace.deviceId,
						backendKind: backend.kind,
						cwd: workspace.cwd,
						lexicalPath: workspace.cwd,
						canonicalPath: workspace.cwd,
						pathFlavor: backend.pathFlavor,
						runtimeGeneration: backend.runtimeGeneration,
						selectionSource: "session_default",
					},
				};
				narratorExecution.policy = await executionPolicyEngine.compile(
					source.narratorId,
					narratorExecution.context,
					policy?.useRobotDiagnosticPreset ? ["robotDiagnostic"] : [],
				);
				if (
					!gitPathPolicyAllows(
						narratorExecution.policy,
						narratorExecution.context,
						workspace.cwd,
						"read",
					)
				)
					throw denied();
				canWriteBase &&= gitPathPolicyAllows(
					narratorExecution.policy,
					narratorExecution.context,
					workspace.cwd,
					"write",
				);
				if (need === "write" && !canWriteBase) throw denied();
			},
			async (candidate) => {
				try {
					if (!candidate.backend || !narratorExecution.context || !narratorExecution.policy)
						return false;
					await requireSafeLocalGitPath(candidate, candidate.workspace.cwd);
					return gitPathPolicyAllows(
						narratorExecution.policy,
						narratorExecution.context,
						candidate.workspace.cwd,
						"read",
					);
				} catch (error) {
					if (error instanceof AppError && error.statusCode === 403) return false;
					throw error;
				}
			},
			async (candidate, deadline) => {
				if (performance.now() >= deadline) return "budget_exhausted";
				discoveryAccessScan ??= new GitAccessScan(
					signal,
					Math.max(1, deadline - performance.now()),
				);
				try {
					return (await authorizeResolvedGitCandidate(
						candidate,
						need,
						principal,
						discoveryAccessScan,
						canWriteBase,
						narratorExecution,
						policy,
					))
						? "authorized"
						: "denied";
				} catch (error) {
					if (isAccessScanUnavailable(error)) return "budget_exhausted";
					throw error;
				}
			},
			cachedDiscovery !== "cache-only" && (discoverNested || requestedWorkspaceKey !== undefined),
			need === "read" ? cachedDiscovery : false,
		);
	} else {
		await assertChapterProjectAccess(source.chapterId, principal, need);
		target = await resolveChapterGitTarget(source.chapterId, signal);
		if (target.contextProjectId)
			canWriteBase = await hasProjectAccess(
				await loadProjectForAccess(target.contextProjectId, principal, "read"),
				principal,
				"write",
			);
	}

	const authorizationBudgetMs = target.discoveryDeadline
		? Math.max(1, target.discoveryDeadline - performance.now())
		: 5000;
	const scan = discoveryAccessScan ?? new GitAccessScan(signal, authorizationBudgetMs);
	try {
		if (target.workspace.state === "ready") {
			if (
				!(await authorizeResolvedGitCandidate(
					target,
					need,
					principal,
					scan,
					canWriteBase,
					narratorExecution,
					policy,
				))
			)
				redactDeniedTarget(target);
			return target;
		}
		if (target.workspace.state === "access_denied") {
			redactDeniedTarget(target);
			return target;
		}

		const readable: GitWorkspaceTarget[] = target.discoveredRepositoriesAuthorized
			? [...(target.discoveredRepositories ?? [])]
			: [];
		if (!target.discoveredRepositoriesAuthorized)
			for (const candidate of target.discoveredRepositories ?? []) {
				try {
					if (
						await authorizeResolvedGitCandidate(
							candidate,
							need,
							principal,
							scan,
							canWriteBase,
							narratorExecution,
							policy,
						)
					)
						readable.push(candidate);
				} catch (error) {
					if (!isAccessScanUnavailable(error)) throw error;
					target.discoveryTruncated = true;
					break;
				}
			}
		target.workspace.repositories = readable.map(({ workspace }) => ({
			workspaceKey: workspace.workspaceKey as string,
			rootPath: workspace.rootPath as string,
			relativePath:
				target.backend?.paths.relative(
					target.discoveryBasePath ?? target.workspace.cwd,
					workspace.rootPath as string,
				) ?? "",
		}));
		target.workspace.discoveryTruncated = target.discoveryTruncated === true;
		const selected = requestedWorkspaceKey
			? readable.find(({ workspace }) => workspace.workspaceKey === requestedWorkspaceKey)
			: readable[0];
		if (selected) selectGitTarget(target, selected);
		else if (target.discoveryTruncated)
			target.workspace.reason =
				"Directory is not a Git working tree; repository discovery was truncated";
		return target;
	} finally {
		scan.dispose();
		const elapsedMs = Math.round(performance.now() - scan.started);
		if (elapsedMs >= 1000)
			logger.warn("Slow Git workspace access check", {
				elapsedMs,
				pages: scan.pages,
				rows: scan.rows,
			});
	}
	return target;
}

export function requireReadyGitTarget(
	target: GitWorkspaceTarget,
	need: "read" | "write",
): asserts target is GitWorkspaceTarget & { backend: NonNullable<GitWorkspaceTarget["backend"]> } {
	if (target.workspace.state !== "ready" || !target.workspace.capabilities[need])
		throw new AppError(
			target.workspace.reason ?? "Git workspace unavailable",
			target.workspace.state === "access_denied" ? 403 : 409,
			"GIT_WORKSPACE_UNAVAILABLE",
		);
}
