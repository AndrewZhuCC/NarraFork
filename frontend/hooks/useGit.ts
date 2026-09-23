import { type QueryClient, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useSyncExternalStore } from "react";
import type { CurrentDiffView } from "../../server/services/git-current-diff-view";
import type {
	FileChangeActorKind,
	FileChangeAttributionGrade,
	FileChangeProjectionCompleteness,
} from "../../shared/file-change-protocol";
import { type ApiError, api } from "../lib/api";
import { type GitTarget, type GitWorkspace, gitTargetKey } from "../lib/api/git";
import { type ListenerHandle, narratorWSManager } from "../lib/narrator-ws-manager";
import { useNarrator } from "./useNarrator";

export type {
	CurrentDiffFile,
	CurrentDiffTarget,
	CurrentDiffView,
} from "../../server/services/git-current-diff-view";
export type { GitTarget, GitWorkspace } from "../lib/api/git";

// Types
export interface GitStatusSummary {
	/** The executor reached its output budget; counts may be lower bounds. */
	truncated?: boolean;
	hasChanges: boolean;
	staged: number;
	unstaged: number;
	untracked: number;
	files: Array<{
		/** Two-character porcelain status, e.g. "M ", " M", "MM", "??". */
		status: string;
		path: string;
		linesAdded: number;
		linesRemoved: number;
		stagedLinesAdded: number;
		stagedLinesRemoved: number;
		unstagedLinesAdded: number;
		unstagedLinesRemoved: number;
	}>;
	/** Total changed files (may exceed files.length when capped server-side). */
	totalFiles: number;
	headSha: string;
	branch: string;
	linesAdded: number;
	linesRemoved: number;
}

export interface GitLogEntry {
	sha: string;
	shortSha: string;
	message: string;
	author: string;
	date: string;
}

export interface GitStashEntry {
	index: number;
	message: string;
	date: string;
}

export type AttributionAction = "write" | "edit" | "bash" | "external" | "human";

/** Observed subject, not an owner of the current net diff. */
export interface AttributionActor {
	/** v1 may lose narrator subtype information together with its FK. */
	kind: FileChangeActorKind | "narrator_unknown";
	narratorId: string | null;
	userId: string | null;
	title: string | null;
	subagentType: string | null;
	parentTitle: string | null;
	exists: boolean;
	/** Null means v1 cannot distinguish missing identity from deletion. */
	deleted: boolean | null;
	identityKnown: boolean;
	subjectKey?: string;
}

export interface ModificationEventSummary {
	id: string;
	changedAt: string;
	action: AttributionAction;
	actor: AttributionActor;
	evidence: "legacy" | "v2";
	/** NULL means unmeasured, never zero. */
	linesAdded?: number | null;
	linesRemoved?: number | null;
}

export interface FileModificationGroup {
	filePath: string;
	changeCount: number;
	lastChangedAt: string;
	/** Actor and action of the same latest observed event, never guessed from group flags. */
	lastActor: AttributionActor;
	lastAction: AttributionAction;
	/** Known subjects and anonymous buckets seen in the returned row window. */
	actors: AttributionActor[];
	/** Bounded newest-first history for the status-badge hover card. */
	recentEvents: ModificationEventSummary[];
	/** False requires a complete scan; null means absence is unverified. */
	hasExternalChange: boolean | null;
	hasImpreciseAttribution: boolean | null;
	hasDeletedActor: boolean | null;
	completeness: FileChangeProjectionCompleteness;
	evidence: "legacy" | "v2" | "mixed";
	attributionGrade: FileChangeAttributionGrade;
}

export interface WorkspaceModificationView {
	source?: "history";
	currentDiff?: CurrentDiffView;
	/** Historical page position only; rowId is not a business ID or execution order. */
	nextCursor?: { changedAt: string; rowId: string } | null;
	workspacePath: string;
	deviceId: string;
	byFile: FileModificationGroup[];
	hasMore: boolean;
	actors: AttributionActor[];
	/** Post-filter observed rows, independent of whether the query was truncated. */
	windowCount?: number;
	completeness: FileChangeProjectionCompleteness;
	evidence: "legacy" | "v2" | "mixed";
	baselineStatus: "unverified";
}

// Workspace facts share a cache across narrators, never across devices/worktrees.
const GIT_QUERY_GC_TIME_MS = 60_000;
export const GIT_FACT_QUERIES = [
	"gitStatus",
	"gitModifications",
	"gitDiff",
	"gitLog",
	"gitStashList",
];

const GIT_DETECTION_PREF_PREFIX = "narrafork_git_detection_enabled:";
const gitDetectionListeners = new Set<() => void>();
let gitDetectionStorageListener: ((event: StorageEvent) => void) | null = null;
let gitDetectionStorageWindow: Window | null = null;
const gitDetectionFallback = new Map<string, boolean>();

function gitDetectionKey(narratorId: string): string {
	return `${GIT_DETECTION_PREF_PREFIX}${narratorId}`;
}

function readGitDetectionOverride(narratorId: string | null | undefined): boolean | null {
	if (!narratorId) return null;
	if (gitDetectionFallback.has(narratorId)) return gitDetectionFallback.get(narratorId) ?? null;
	try {
		const stored = localStorage.getItem(gitDetectionKey(narratorId));
		if (stored === "1") return true;
		if (stored === "0") return false;
		return null;
	} catch {
		return null;
	}
}

function notifyGitDetectionListeners() {
	for (const listener of gitDetectionListeners) listener();
}

function ensureGitDetectionStorageListener() {
	if (gitDetectionStorageListener || typeof window === "undefined") return;
	gitDetectionStorageListener = (event) => {
		if (event.key !== null && !event.key.startsWith(GIT_DETECTION_PREF_PREFIX)) return;
		notifyGitDetectionListeners();
	};
	gitDetectionStorageWindow = window;
	window.addEventListener("storage", gitDetectionStorageListener);
}

function subscribeGitDetection(listener: () => void) {
	gitDetectionListeners.add(listener);
	ensureGitDetectionStorageListener();
	return () => {
		gitDetectionListeners.delete(listener);
		if (gitDetectionListeners.size > 0 || !gitDetectionStorageListener) return;
		gitDetectionStorageWindow?.removeEventListener("storage", gitDetectionStorageListener);
		gitDetectionStorageListener = null;
		gitDetectionStorageWindow = null;
	};
}

function persistGitDetection(narratorId: string, enabled: boolean) {
	try {
		localStorage.setItem(gitDetectionKey(narratorId), enabled ? "1" : "0");
		gitDetectionFallback.delete(narratorId);
	} catch {
		gitDetectionFallback.set(narratorId, enabled);
	}
	notifyGitDetectionListeners();
}

function useGitDetectionPreference(narratorId: string | null | undefined) {
	const override = useSyncExternalStore(
		subscribeGitDetection,
		() => readGitDetectionOverride(narratorId),
		() => null,
	);
	const { data: settings, isError } = useQuery({
		queryKey: ["settings"],
		queryFn: api.getSettings,
		enabled: !!narratorId,
	});
	const enabled = override ?? (!isError && settings?.git?.narratorDetectionDefaultEnabled === true);
	const setEnabled = useCallback(
		(nextEnabled: boolean) => {
			if (narratorId) persistGitDetection(narratorId, nextEnabled);
		},
		[narratorId],
	);
	return { enabled, setEnabled };
}

const GIT_WORKSPACE_SELECTIONS_KEY = "narrafork_git_workspace_selections";
const gitWorkspaceSelectionListeners = new Set<() => void>();

function subscribeGitWorkspaceSelections(listener: () => void) {
	gitWorkspaceSelectionListeners.add(listener);
	return () => gitWorkspaceSelectionListeners.delete(listener);
}

function readGitWorkspaceSelectionsRaw(): string | null {
	try {
		return sessionStorage.getItem(GIT_WORKSPACE_SELECTIONS_KEY);
	} catch {
		return null;
	}
}

function parseGitWorkspaceSelections(raw: string | null): Record<string, string> {
	if (!raw) return {};
	try {
		const parsed = JSON.parse(raw) as unknown;
		if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
		return Object.fromEntries(
			Object.entries(parsed).filter(
				(entry): entry is [string, string] => typeof entry[1] === "string",
			),
		);
	} catch {
		return {};
	}
}

function persistGitWorkspaceSelection(
	scope: string,
	workspaceKey: string | null,
	expected?: string,
) {
	const selections = parseGitWorkspaceSelections(readGitWorkspaceSelectionsRaw());
	if (expected !== undefined && selections[scope] !== expected) return;
	if (workspaceKey) selections[scope] = workspaceKey;
	else delete selections[scope];
	try {
		sessionStorage.setItem(GIT_WORKSPACE_SELECTIONS_KEY, JSON.stringify(selections));
	} catch {
		return;
	}
	for (const listener of gitWorkspaceSelectionListeners) listener();
}

function useGitWorkspaceSelection(scope: string) {
	const raw = useSyncExternalStore(
		subscribeGitWorkspaceSelections,
		readGitWorkspaceSelectionsRaw,
		() => null,
	);
	const workspaceKey = useMemo(() => parseGitWorkspaceSelections(raw)[scope] ?? null, [raw, scope]);
	const selectWorkspace = useCallback(
		(nextWorkspaceKey: string | null) => persistGitWorkspaceSelection(scope, nextWorkspaceKey),
		[scope],
	);
	const clearIfCurrent = useCallback(
		(expected: string) => persistGitWorkspaceSelection(scope, null, expected),
		[scope],
	);
	return { workspaceKey, selectWorkspace, clearIfCurrent };
}

export function gitWorkspaceTarget(narratorId: string, workspace?: GitWorkspace): GitTarget | null {
	if (workspace?.state !== "ready" || !workspace.capabilities.read || !workspace.workspaceKey)
		return null;
	return {
		narratorId,
		workspaceKey: workspace.workspaceKey,
		repositoryKey: workspace.repositoryKey,
		canWrite: workspace.capabilities.write,
		rootPath: workspace.rootPath,
		chapterId: workspace.chapterId,
	};
}

// One permission subscription per QueryClient, shared by all mounted workspace consumers.
const gitAccessSubscriptions = new WeakMap<
	QueryClient,
	{ count: number; handle: ListenerHandle }
>();
function subscribeGitAccess(qc: QueryClient) {
	let entry = gitAccessSubscriptions.get(qc);
	if (!entry) {
		const handle = narratorWSManager.addListener(
			{ narratorIds: "*", types: ["narrator_access_changed"] },
			() => {
				qc.resetQueries({ queryKey: ["gitWorkspace"] });
				qc.removeQueries({ predicate: (q) => GIT_FACT_QUERIES.includes(String(q.queryKey[0])) });
			},
		);
		entry = { count: 0, handle };
		gitAccessSubscriptions.set(qc, entry);
	}
	entry.count++;
	return () => {
		if (--entry.count === 0) {
			narratorWSManager.removeListener(entry.handle);
			gitAccessSubscriptions.delete(qc);
		}
	};
}

async function readWorkspace<T>(
	qc: QueryClient,
	target: GitTarget | null | undefined,
	read: () => Promise<T>,
): Promise<T> {
	try {
		return await read();
	} catch (error) {
		if (
			target &&
			typeof target !== "string" &&
			[401, 403, 409, 503].includes((error as ApiError)?.status)
		) {
			qc.resetQueries({ queryKey: ["gitWorkspace", target.narratorId] });
		}
		throw error;
	}
}

/** The execution-context revision is a cache key, not a client-supplied path. */
export function useGitWorkspace(narratorId: string | null | undefined, revision?: unknown) {
	const qc = useQueryClient();
	const { data: narrator } = useNarrator(narratorId ?? "");
	const detection = useGitDetectionPreference(narratorId);
	useEffect(
		() => (narratorId && detection.enabled ? subscribeGitAccess(qc) : undefined),
		[qc, narratorId, detection.enabled],
	);
	const context = revision ?? [
		narrator?.cwd,
		narrator?.chapterId,
		narrator?.contextProjectId,
		narrator?.defaultDeviceId,
	];
	const selectionScope = JSON.stringify([narratorId, context]);
	const selection = useGitWorkspaceSelection(selectionScope);
	const query = useQuery({
		queryKey: ["gitWorkspace", narratorId, detection.enabled, context, selection.workspaceKey],
		queryFn: async ({ signal }) => {
			try {
				return await api.getGitWorkspace(
					narratorId as string,
					signal,
					selection.workspaceKey ?? undefined,
					true,
				);
			} catch (error) {
				if (selection.workspaceKey && (error as ApiError)?.status === 409) {
					selection.clearIfCurrent(selection.workspaceKey);
					return api.getGitWorkspace(narratorId as string, signal, undefined, true);
				}
				throw error;
			}
		},
		enabled: !!narratorId && detection.enabled,
		retry: false,
		staleTime: 5_000,
		refetchInterval: 30_000,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
	useEffect(() => {
		if (!narratorId || detection.enabled) return;
		const workspaces = qc.getQueriesData<GitWorkspace>({ queryKey: ["gitWorkspace"] });
		const disabledWorkspaceKeys = new Set(
			workspaces
				.filter(([key, workspace]) => key[1] === narratorId && workspace?.workspaceKey)
				.map(([, workspace]) => workspace?.workspaceKey as string),
		);
		const retainedWorkspaceKeys = new Set(
			workspaces
				.filter(
					([key, workspace]) => key[1] !== narratorId && key[2] === true && workspace?.workspaceKey,
				)
				.map(([, workspace]) => workspace?.workspaceKey as string),
		);
		const shouldClearQuery = (candidate: { queryKey: readonly unknown[] }) => {
			const [prefix, scope, kind] = candidate.queryKey;
			if (prefix === "gitWorkspace") return scope === narratorId && kind !== false;
			if (prefix === "narrators") {
				return scope === narratorId && kind === "file-tree-status";
			}
			return (
				GIT_FACT_QUERIES.includes(String(prefix)) &&
				disabledWorkspaceKeys.has(String(scope)) &&
				!retainedWorkspaceKeys.has(String(scope))
			);
		};
		void qc.cancelQueries({ predicate: shouldClearQuery });
		qc.removeQueries({ predicate: shouldClearQuery });
	}, [qc, narratorId, detection.enabled]);
	// Do not leave private facts visible after an authorization/capability failure.
	useEffect(() => {
		if (!query.isError && query.data?.capabilities.read !== false) return;
		qc.removeQueries({ predicate: (q) => GIT_FACT_QUERIES.includes(String(q.queryKey[0])) });
	}, [qc, query.isError, query.data?.capabilities.read]);
	const target =
		detection.enabled && !query.isError
			? gitWorkspaceTarget(narratorId as string, query.data)
			: null;
	return {
		...query,
		detectionEnabled: detection.enabled,
		setDetectionEnabled: detection.setEnabled,
		workspaceKey: target && typeof target !== "string" ? target.workspaceKey : null,
		target,
		selectWorkspace: selection.selectWorkspace,
	};
}

export function invalidateWorkspaceQueries(
	qc: ReturnType<typeof useQueryClient>,
	target: GitTarget,
): void {
	const keys = new Set([gitTargetKey(target)]);
	const workspaces = qc
		.getQueriesData<GitWorkspace>({ queryKey: ["gitWorkspace"] })
		.map(([, workspace]) => workspace);
	const chapterId = typeof target === "string" ? target : target.chapterId;
	const repositoryKey =
		typeof target === "string"
			? workspaces.find((workspace) => workspace?.chapterId === target)?.repositoryKey
			: target.repositoryKey;
	if (chapterId) {
		keys.add(chapterId);
		qc.invalidateQueries({ queryKey: ["chapterGitStatus", chapterId] });
	}
	for (const workspace of workspaces) {
		if (
			workspace?.workspaceKey &&
			((repositoryKey && workspace.repositoryKey === repositoryKey) ||
				(chapterId && workspace.chapterId === chapterId))
		) {
			keys.add(workspace.workspaceKey);
			if (workspace.chapterId) keys.add(workspace.chapterId);
		}
	}
	for (const key of keys) {
		for (const prefix of GIT_FACT_QUERIES) qc.invalidateQueries({ queryKey: [prefix, key] });
	}
	// Re-probe after success AND failure: remote writes may have completed before disconnecting.
	if (typeof target !== "string")
		qc.invalidateQueries({ queryKey: ["gitWorkspace", target.narratorId] });
}

export function useGitStatus(target: GitTarget | undefined | null) {
	const qc = useQueryClient();
	return useQuery<GitStatusSummary>({
		queryKey: ["gitStatus", gitTargetKey(target)],
		queryFn: ({ signal }) =>
			readWorkspace(qc, target, () => api.getGitStatus(target as GitTarget, signal)),
		enabled: !!target,
		retry: false,
		refetchInterval: 30_000,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
}

export function useGitModifications(target: GitTarget | undefined | null) {
	const qc = useQueryClient();
	return useQuery<WorkspaceModificationView>({
		queryKey: ["gitModifications", gitTargetKey(target), "uncommitted"],
		queryFn: ({ signal }) =>
			readWorkspace(
				qc,
				target,
				() =>
					api.getGitModifications(
						target as GitTarget,
						{
							scope: "uncommitted",
							projection: "byFile",
						},
						signal,
					) as Promise<WorkspaceModificationView>,
			),
		enabled: !!target,
		retry: false,
		refetchInterval: 30_000,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
}

export function useGitLog(target: GitTarget | undefined | null, limit = 50, skip = 0) {
	const qc = useQueryClient();
	return useQuery<GitLogEntry[]>({
		queryKey: ["gitLog", gitTargetKey(target), limit, skip],
		queryFn: ({ signal }) =>
			readWorkspace(qc, target, () => api.getGitLog(target as GitTarget, limit, skip, signal)),
		enabled: !!target,
		retry: false,
		refetchInterval: 30_000,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
}

export function useGitStashList(target: GitTarget | undefined | null) {
	const qc = useQueryClient();
	return useQuery<GitStashEntry[]>({
		queryKey: ["gitStashList", gitTargetKey(target)],
		queryFn: ({ signal }) =>
			readWorkspace(qc, target, () => api.getGitStashList(target as GitTarget, signal)),
		enabled: !!target,
		retry: false,
		refetchInterval: 30_000,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
}

export function useGitDiff(
	target: GitTarget | undefined | null,
	file: string | null,
	staged = false,
) {
	const qc = useQueryClient();
	return useQuery({
		queryKey: ["gitDiff", gitTargetKey(target), file, staged],
		queryFn: ({ signal }) =>
			readWorkspace(qc, target, () =>
				api.getGitDiff(target as GitTarget, file as string, staged, signal),
			),
		enabled: !!target && !!file,
		retry: false,
		refetchInterval: 30_000,
		gcTime: 30_000,
	});
}

function useGitMutation<T, R>(target: GitTarget, run: (value: T) => Promise<R>, graph = false) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: run,
		retry: false,
		onSettled: () => {
			invalidateWorkspaceQueries(qc, target);
			if (graph) qc.invalidateQueries({ queryKey: ["narraFlow"] });
		},
	});
}

export function useGitStage(target: GitTarget) {
	return useGitMutation(target, (body: { files?: string[]; all?: boolean }) =>
		api.gitStage(target, body),
	);
}
export function useGitUnstage(target: GitTarget) {
	return useGitMutation(target, (body: { files?: string[]; all?: boolean }) =>
		api.gitUnstage(target, body),
	);
}
export function useGitCommit(target: GitTarget) {
	return useGitMutation(target, (message: string) => api.gitCommit(target, message), true);
}
export function useGitDiscard(target: GitTarget) {
	return useGitMutation(target, (body: { files?: string[]; all?: boolean }) =>
		api.gitDiscard(target, body),
	);
}
export function useGitStash(target: GitTarget) {
	return useGitMutation(target, (body: { action: string; message?: string; index?: number }) =>
		api.gitStash(target, body),
	);
}
export function useGitReset(target: GitTarget) {
	return useGitMutation(
		target,
		(params: { target: string; mode: "soft" | "hard" }) =>
			api.gitReset(target, params.target, params.mode),
		true,
	);
}
export function useGitAiCommitMessage(target: GitTarget) {
	return useGitMutation<void, { message: string }>(target, () => api.gitAiCommitMessage(target));
}
