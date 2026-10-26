import {
	type QueryClient,
	skipToken,
	useMutation,
	useQuery,
	useQueryClient,
} from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useSyncExternalStore } from "react";
import type { CurrentDiffView } from "../../server/services/git-current-diff-view";
import type {
	FileChangeActorKind,
	FileChangeAttributionGrade,
	FileChangeProjectionCompleteness,
} from "../../shared/file-change-protocol";
import {
	GIT_COMMIT_PREVIEW_UNSUPPORTED,
	type GitCommitDetail,
	type GitCommitPatch,
} from "../../shared/git-commit-preview";
import type { GitLineStats } from "../../shared/git-line-stats";
import type { GitWorkspaceSummary } from "../../shared/git-workspace";
import type { GitWorkspaceSnapshot, GitWorkspaceView } from "../../shared/git-workspace-events";
import { type ApiError, api } from "../lib/api";
import {
	type GitLogEntry,
	type GitTarget,
	type GitWorkspace,
	gitBasePath,
	gitTargetKey,
} from "../lib/api/git";
import {
	GitWorkspaceSubscriptions,
	gitFactKey,
	gitSnapshotKey,
} from "../lib/git-workspace-subscription";
import { type ListenerHandle, narratorWSManager } from "../lib/narrator-ws-manager";
import { observePageLifecycle } from "../lib/page-lifecycle";
import { useNarrator } from "./useNarrator";
import { useWorkspaceContext } from "./useWorkspaceContext";

export type {
	CurrentDiffFile,
	CurrentDiffTarget,
	CurrentDiffView,
} from "../../server/services/git-current-diff-view";
export type { GitLogEntry, GitTarget, GitWorkspace } from "../lib/api/git";

// Types
export interface GitStatusSummary {
	/** The file list is incomplete; totalFilesExact separately indicates scan completeness. */
	truncated?: boolean;
	hasChanges: boolean;
	staged: number;
	unstaged: number;
	untracked: number;
	files: Array<{
		/** Two-character porcelain status, e.g. "M ", " M", "MM", "??". */
		status: string;
		path: string;
		linesAdded?: number;
		linesRemoved?: number;
		stagedLinesAdded?: number;
		stagedLinesRemoved?: number;
		unstagedLinesAdded?: number;
		unstagedLinesRemoved?: number;
	}>;
	/** Total changed files (may exceed files.length when capped server-side). */
	totalFiles: number;
	/** False means the scan stopped early; undefined preserves older responses. */
	totalFilesExact?: boolean;
	headSha: string;
	branch: string;
	linesAdded?: number;
	linesRemoved?: number;
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
const GIT_WORKSPACE_RECOVERY_INTERVAL_MS = 30_000;
export const GIT_FACT_QUERIES = [
	"gitStatus",
	"gitLineStats",
	"gitWorkspaceSnapshot",
	"gitModifications",
	"gitDiff",
	"gitLog",
	"gitStashList",
	"gitCommitDetail",
	"gitCommitDiff",
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

/**
 * Live facts invalidated by a WS `git_status` push. Derived from
 * GIT_FACT_QUERIES so a new live fact cannot drift out of the push path;
 * commit previews are immutable and excluded.
 */
export const GIT_LIVE_FACT_QUERIES = GIT_FACT_QUERIES.filter(
	(prefix) => prefix !== "gitCommitDetail" && prefix !== "gitCommitDiff",
);

export function gitWorkspaceTarget(narratorId: string, workspace?: GitWorkspace): GitTarget | null {
	if (
		workspace?.state !== "ready" ||
		gitSummarySeeds.has(workspace) ||
		!workspace.capabilities.read ||
		!workspace.workspaceKey
	)
		return null;
	return {
		narratorId,
		workspaceKey: workspace.workspaceKey,
		repositoryKey: workspace.repositoryKey,
		canWrite: workspace.capabilities.write,
		rootPath: workspace.rootPath,
	};
}

const gitWorkspaceSubscriptions = new WeakMap<QueryClient, GitWorkspaceSubscriptions>();
// One-shot explicit refresh flags. Ordinary HTTP fallback keeps the server's short cache.
const freshGitReads = new WeakMap<QueryClient, Set<string>>();
function requestFreshGitRead(qc: QueryClient, key: string | undefined, kind: string) {
	let reads = freshGitReads.get(qc);
	if (!reads) {
		reads = new Set();
		freshGitReads.set(qc, reads);
	}
	reads.add(JSON.stringify([key, kind]));
}
function takeFreshGitRead(qc: QueryClient, key: string | undefined, kind: string) {
	return freshGitReads.get(qc)?.delete(JSON.stringify([key, kind])) ?? false;
}

function workspaceSubscriptions(qc: QueryClient) {
	let subscriptions = gitWorkspaceSubscriptions.get(qc);
	if (!subscriptions) {
		subscriptions = new GitWorkspaceSubscriptions(qc, narratorWSManager, undefined, (target) => {
			blockGitSummary(qc, target.narratorId);
			void qc.cancelQueries({ queryKey: ["gitWorkspace", target.narratorId] });
			qc.setQueriesData<GitWorkspace>(
				{ queryKey: ["gitWorkspace", target.narratorId] },
				(workspace) =>
					workspace
						? { ...workspace, state: "access_denied", capabilities: { read: false, write: false } }
						: undefined,
			);
		});
		gitWorkspaceSubscriptions.set(qc, subscriptions);
	}
	return subscriptions;
}

/** All consumers share a subscription; opening a panel upgrades its projection. */
export function useGitWorkspaceSubscription(
	target: GitTarget | null | undefined,
	view: GitWorkspaceView = "details",
) {
	const subscriptions = workspaceSubscriptions(useQueryClient());
	const narratorId = target?.narratorId;
	const workspaceKey = gitTargetKey(target);
	useSyncExternalStore(subscriptions.watch, subscriptions.getRevision, subscriptions.getRevision);
	useEffect(() => {
		if (!workspaceKey || !narratorId) return;
		return subscriptions.subscribe({ narratorId, workspaceKey, canWrite: false }, view);
	}, [subscriptions, narratorId, workspaceKey, view]);
	return subscriptions;
}

// One permission subscription per QueryClient, shared by all mounted workspace consumers.
const deniedGitSummaries = new WeakMap<QueryClient, Set<string>>();
// Presentation summaries may paint immediately, but can never become Git API targets.
const gitSummarySeeds = new WeakSet<GitWorkspace>();
const gitDetailSummaryScopes = new WeakMap<GitWorkspaceSummary, string>();
export type GitDisplayCounts = Pick<
	GitStatusSummary,
	"totalFiles" | "totalFilesExact" | "truncated" | "linesAdded" | "linesRemoved"
>;
interface GitDisplaySnapshot {
	scope: string;
	workspace: GitWorkspace;
	counts?: GitDisplayCounts;
	storedAt: number;
}
const gitDisplayCaches = new WeakMap<
	QueryClient,
	{ epoch: number; entries: Map<string, GitDisplaySnapshot>; principal: unknown }
>();
const GIT_DISPLAY_TTL_MS = 10 * 60_000;
function gitDisplayCache(qc: QueryClient) {
	const principal = qc.getQueryData<{ id?: unknown }>(["auth", "me"])?.id;
	let cache = gitDisplayCaches.get(qc);
	if (!cache) {
		cache = { epoch: 0, entries: new Map(), principal };
		gitDisplayCaches.set(qc, cache);
	} else if (cache.principal !== principal) {
		cache.epoch++;
		cache.entries.clear();
		cache.principal = principal;
	}
	return cache;
}
function clearGitDisplay(qc: QueryClient, narratorId?: string) {
	const cache = gitDisplayCache(qc);
	cache.epoch++;
	if (narratorId) cache.entries.delete(narratorId);
	else cache.entries.clear();
}
function peekGitDisplay(qc: QueryClient, narratorId: string, scope: string) {
	const cache = gitDisplayCache(qc);
	const entry = cache.entries.get(narratorId);
	if (!entry || entry.scope !== scope) return undefined;
	if (Date.now() - entry.storedAt > GIT_DISPLAY_TTL_MS) {
		cache.entries.delete(narratorId);
		return undefined;
	}
	cache.entries.delete(narratorId);
	cache.entries.set(narratorId, entry);
	return entry;
}
function storeGitDisplay(
	qc: QueryClient,
	narratorId: string,
	scope: string,
	workspace: GitWorkspace,
	counts?: GitDisplayCounts,
) {
	const cache = gitDisplayCache(qc);
	// Never retain Git status's diff file list or grant capabilities from a snapshot.
	const display = { ...workspace, capabilities: { read: false, write: false } };
	gitSummarySeeds.add(display);
	const previous = cache.entries.get(narratorId);
	const retainedCounts =
		previous?.scope === scope && previous.workspace.workspaceKey === workspace.workspaceKey
			? previous.counts
			: undefined;
	cache.entries.delete(narratorId);
	cache.entries.set(narratorId, {
		scope,
		workspace: display,
		counts: counts ?? retainedCounts,
		storedAt: Date.now(),
	});
	while (cache.entries.size > 16) {
		const key = cache.entries.keys().next().value;
		if (key !== undefined) cache.entries.delete(key);
	}
}
// Monotonic count of authoritative grants (non-denied probe results) per narrator. A refusal
// observed by a render may only be applied if no newer grant has arrived since that render.
const gitAccessGrants = new WeakMap<QueryClient, Map<string, number>>();
function gitAccessGrantCount(qc: QueryClient, narratorId: string): number {
	return gitAccessGrants.get(qc)?.get(narratorId) ?? 0;
}
function recordGitAccessGrant(qc: QueryClient, narratorId: string) {
	let grants = gitAccessGrants.get(qc);
	if (!grants) {
		grants = new Map();
		gitAccessGrants.set(qc, grants);
	}
	grants.set(narratorId, gitAccessGrantCount(qc, narratorId) + 1);
}

function blockGitSummary(qc: QueryClient, narratorId: string) {
	let denied = deniedGitSummaries.get(qc);
	if (!denied) {
		denied = new Set();
		deniedGitSummaries.set(qc, denied);
	}
	denied.add(narratorId);
	clearGitDisplay(qc, narratorId);
	void qc.cancelQueries({ queryKey: ["gitSummary", narratorId] });
	qc.removeQueries({ queryKey: ["gitSummary", narratorId] });
	void qc.cancelQueries({ queryKey: ["narrators", narratorId], exact: true });
	qc.setQueryData<Record<string, unknown>>(["narrators", narratorId], (detail) =>
		detail ? { ...detail, gitSummary: null } : detail,
	);
}
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
				clearGitDisplay(qc);
				void qc.cancelQueries({ queryKey: ["gitSummary"] });
				qc.removeQueries({ queryKey: ["gitSummary"] });
				// Cancel in-flight detail reads before clearing presentation-only summaries.
				// A late response must not revive private facts after access changed.
				const details = {
					predicate: (q: { queryKey: readonly unknown[] }) =>
						q.queryKey[0] === "narrators" &&
						q.queryKey.length === 2 &&
						typeof q.queryKey[1] === "string",
				};
				void qc.cancelQueries(details);
				qc.setQueriesData<Record<string, unknown>>(details, (detail) =>
					detail ? { ...detail, gitSummary: null } : detail,
				);
				void qc.invalidateQueries(details);
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
	reprobeUnavailable = true,
): Promise<T> {
	try {
		return await read();
	} catch (error) {
		const failure = error as ApiError;
		// An older executor missing an optional read is not a workspace change.
		// Re-probing would unmount/reopen its consumer and repeat the same failure.
		const unsupportedOperation =
			(failure?.status === 409 && failure.data?.code === GIT_COMMIT_PREVIEW_UNSUPPORTED) ||
			failure?.data?.code === "GIT_LINE_STATS_UNSUPPORTED";
		if (
			target &&
			!unsupportedOperation &&
			([401, 403, 409].includes(failure?.status) || (reprobeUnavailable && failure?.status === 503))
		) {
			if ([401, 403].includes(failure.status)) blockGitSummary(qc, target.narratorId);
			void qc.cancelQueries({ queryKey: ["narrators", target.narratorId], exact: true });
			qc.setQueryData<Record<string, unknown>>(["narrators", target.narratorId], (detail) =>
				detail ? { ...detail, gitSummary: null } : detail,
			);
			qc.resetQueries({ queryKey: ["gitWorkspace", target.narratorId] });
		}
		throw error;
	}
}

/** Detail summaries are presentation hints only, never authorization for a Git API. */
export function gitSummaryWorkspace(
	narrator: Record<string, unknown> | undefined,
	contextRevision: number,
): GitWorkspace | undefined {
	const summary = narrator?.gitSummary as GitWorkspaceSummary | null | undefined;
	if (!summary || summary.revision !== contextRevision) return undefined;
	// cwd may be a host path while the executor uses its own defaultCwd, or a
	// symlink resolved by the probe. Directory switches must advance the revision;
	// comparing these differently normalized paths would reject valid summaries.
	if (narrator?.defaultDeviceId && narrator.defaultDeviceId !== summary.workspace.deviceId)
		return undefined;
	return { ...summary.workspace, branch: summary.workspace.branch ?? summary.branch };
}

/** Display and authoritative operation facts have separate lifetimes. */
export function useGitWorkspace(narratorId: string | null | undefined, revision?: unknown) {
	const qc = useQueryClient();
	const { data: narrator, isError: narratorError } = useNarrator(narratorId ?? "");
	const { data: executionContext } = useWorkspaceContext(narratorId ?? "");
	const detection = useGitDetectionPreference(narratorId);
	useEffect(
		() => (narratorId && detection.enabled ? subscribeGitAccess(qc) : undefined),
		[qc, narratorId, detection.enabled],
	);
	const workspaceRevision = Math.max(
		typeof narrator?.workspaceRevision === "number" ? narrator.workspaceRevision : 0,
		executionContext?.revision ?? 0,
	);
	const context = revision ?? workspaceRevision;
	const selectionScope = JSON.stringify([narratorId, context]);
	const selection = useGitWorkspaceSelection(selectionScope);
	const eligibleSummary = (workspace: GitWorkspace | undefined) =>
		detection.enabled &&
		(!selection.workspaceKey || selection.workspaceKey === workspace?.workspaceKey)
			? workspace
			: undefined;
	// Revision owns normalized executor cwd. Raw narrator fields also guard legacy
	// switches which did not advance it; hydration of executionContext is not a switch.
	const scope = JSON.stringify([
		context,
		narrator?.cwd ?? null,
		narrator?.defaultDeviceId ?? null,
		selection.workspaceKey,
	]);
	const cache = gitDisplayCache(qc);
	const snapshot = narratorId ? peekGitDisplay(qc, narratorId, scope) : undefined;
	const denied = !!narratorId && !!deniedGitSummaries.get(qc)?.has(narratorId);
	const detailSummary = narrator?.gitSummary as GitWorkspaceSummary | undefined;
	const recordedScope = detailSummary ? gitDetailSummaryScopes.get(detailSummary) : undefined;
	if (detailSummary && !recordedScope) gitDetailSummaryScopes.set(detailSummary, scope);
	const legacySummary =
		recordedScope && recordedScope !== scope
			? undefined
			: gitSummaryWorkspace(narrator, workspaceRevision);
	const summary = useQuery({
		queryKey: ["gitSummary", narratorId, detection.enabled, context, scope],
		queryFn: async ({ signal }) => {
			const epoch = cache.epoch;
			try {
				const result = await api.getNarratorGitSummary(narratorId as string, signal);
				if (signal.aborted || epoch !== gitDisplayCache(qc).epoch)
					throw new DOMException("Git summary superseded", "AbortError");
				const display = gitSummaryWorkspace({ ...narrator, gitSummary: result }, workspaceRevision);
				if (display?.state === "access_denied") blockGitSummary(qc, narratorId as string);
				else if (
					display &&
					(!selection.workspaceKey || selection.workspaceKey === display.workspaceKey)
				)
					storeGitDisplay(qc, narratorId as string, scope, display);
				return result;
			} catch (error) {
				if ([401, 403].includes((error as ApiError)?.status))
					blockGitSummary(qc, narratorId as string);
				throw error;
			}
		},
		enabled: !!narratorId && detection.enabled && !!narrator && !denied,
		retry: false,
		staleTime: 5_000,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
	const incoming = summary.data?.revision === workspaceRevision ? summary.data : undefined;
	const summaryWorkspace = eligibleSummary(
		incoming
			? gitSummaryWorkspace({ ...narrator, gitSummary: incoming }, workspaceRevision)
			: legacySummary,
	);
	if (summaryWorkspace) gitSummarySeeds.add(summaryWorkspace);
	// Only metadata hydration at the same revision may carry authoritative facts.
	// Directory/device/repository switches and refusals must start without old facts.
	const carryHydratedScope = useCallback(
		(
			previousData: GitWorkspace | undefined,
			previousQuery: { queryKey: readonly unknown[] } | undefined,
		): GitWorkspace | undefined => {
			if (!detection.enabled || !narratorId || deniedGitSummaries.get(qc)?.has(narratorId))
				return undefined;
			if (!previousData || previousData.state === "access_denied") return undefined;
			if (gitSummarySeeds.has(previousData)) return undefined;
			const key = previousQuery?.queryKey;
			if (key?.[0] !== "gitWorkspace" || key[1] !== narratorId || key[2] !== true) return undefined;
			if (JSON.stringify(key[3]) !== JSON.stringify(context) || key[4] !== selection.workspaceKey)
				return undefined;
			const previous = JSON.parse(String(key[5])) as unknown[];
			const current = JSON.parse(scope) as unknown[];
			const completes = current.every(
				(value, index) => previous[index] === null || previous[index] === value,
			);
			return completes ? previousData : undefined;
		},
		[qc, narratorId, detection.enabled, context, selection.workspaceKey, scope],
	);
	const query = useQuery<GitWorkspace>({
		queryKey: [
			"gitWorkspace",
			narratorId,
			detection.enabled,
			context,
			selection.workspaceKey,
			scope,
		],
		placeholderData: carryHydratedScope,
		queryFn: async ({ signal }) => {
			const epoch = cache.epoch;
			let result: GitWorkspace;
			try {
				result = await api.getGitWorkspace(
					narratorId as string,
					signal,
					selection.workspaceKey ?? undefined,
					true,
				);
			} catch (error) {
				if (!selection.workspaceKey || (error as ApiError)?.status !== 409) throw error;
				selection.clearIfCurrent(selection.workspaceKey);
				result = await api.getGitWorkspace(narratorId as string, signal, undefined, true);
			}
			if (signal.aborted || epoch !== gitDisplayCache(qc).epoch)
				throw new DOMException("Git workspace superseded", "AbortError");
			if (result.state !== "access_denied") {
				deniedGitSummaries.get(qc)?.delete(narratorId as string);
				recordGitAccessGrant(qc, narratorId as string);
				storeGitDisplay(qc, narratorId as string, scope, result);
			}
			return result;
		},
		// Summary data never seeds this query: only this probe authorizes operations.
		enabled:
			!!narratorId &&
			detection.enabled &&
			(!!narrator || narratorError) &&
			summaryWorkspace?.state !== "unsupported",
		retry: false,
		staleTime: 5_000,
		structuralSharing: false,
		refetchInterval: (query) => {
			if (query.state.status === "error") {
				const status = (query.state.error as ApiError)?.status;
				return !status || status >= 500 ? GIT_WORKSPACE_RECOVERY_INTERVAL_MS : false;
			}
			return ["not_git", "missing_directory", "git_unavailable", "device_offline"].includes(
				query.state.data?.state ?? "",
			)
				? GIT_WORKSPACE_RECOVERY_INTERVAL_MS
				: false;
		},
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
	useEffect(() => {
		if (!narratorId || detection.enabled) return;
		gitDisplayCache(qc).entries.delete(narratorId);
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
			if (prefix === "gitWorkspace" || prefix === "gitSummary")
				return scope === narratorId && kind !== false;
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
	const accessFailure =
		(query.isError && [401, 403].includes((query.error as ApiError)?.status)) ||
		(summary.isError && [401, 403].includes((summary.error as ApiError)?.status)) ||
		query.data?.state === "access_denied" ||
		summaryWorkspace?.state === "access_denied";
	// The refusal below is applied in a passive effect, which can run after a newer probe has
	// already granted access (e.g. a retry resolving before the effect of the refused render).
	// Remember which grant this render observed so a stale refusal cannot re-block a newer grant.
	const grantsAtRender = narratorId ? gitAccessGrantCount(qc, narratorId) : 0;
	useEffect(() => {
		if (narratorId && accessFailure && gitAccessGrantCount(qc, narratorId) === grantsAtRender) {
			blockGitSummary(qc, narratorId);
			void qc.cancelQueries({ queryKey: ["gitWorkspace", narratorId] });
			qc.removeQueries({ predicate: (q) => GIT_FACT_QUERIES.includes(String(q.queryKey[0])) });
		}
	}, [qc, narratorId, accessFailure, grantsAtRender]);
	let data =
		!detection.enabled || accessFailure || denied
			? undefined
			: (query.data ?? summaryWorkspace ?? snapshot?.workspace);
	if (
		data &&
		data.branch === undefined &&
		data.workspaceKey &&
		data.workspaceKey === summaryWorkspace?.workspaceKey &&
		data.deviceId === summaryWorkspace.deviceId &&
		data.rootPath === summaryWorkspace.rootPath
	)
		data = { ...data, branch: summaryWorkspace.branch };
	const target = !query.isError && data ? gitWorkspaceTarget(narratorId as string, data) : null;
	const rememberCounts = useCallback(
		(counts: GitDisplayCounts) => {
			if (!narratorId || !data || accessFailure || denied) return;
			storeGitDisplay(qc, narratorId, scope, data, {
				totalFiles: counts.totalFiles,
				totalFilesExact: counts.totalFilesExact,
				truncated: counts.truncated,
			});
		},
		[qc, narratorId, scope, data, accessFailure, denied],
	);
	return {
		...query,
		data,
		/**
		 * True once the server has refused this principal. `data` is then withheld (no branch,
		 * path or counts may leak), so consumers need this to explain the refusal instead of
		 * rendering an empty or generic state.
		 */
		accessDenied: accessFailure || denied,
		authoritativeData: !detection.enabled || accessFailure || denied ? undefined : query.data,
		displayCounts: !detection.enabled || accessFailure || denied ? undefined : snapshot?.counts,
		rememberCounts,
		// Retained for legacy consumers. Fixed slots, not Git, own first-screen readiness.
		layoutReady: true,
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
	manual = true,
): void {
	const subscriptions = gitWorkspaceSubscriptions.get(qc);
	if (subscriptions && !subscriptions.isVisible()) return;
	const pushed = manual
		? subscriptions?.refresh(target)
		: subscriptions?.owns(gitTargetKey(target) as string);
	if (manual && !pushed) {
		for (const kind of ["status", "lineStats"]) requestFreshGitRead(qc, gitTargetKey(target), kind);
	}
	const keys = new Set([gitTargetKey(target)]);
	const workspaces = qc
		.getQueriesData<GitWorkspace>({ queryKey: ["gitWorkspace"] })
		.map(([, workspace]) => workspace);
	const repositoryKey = target.repositoryKey;
	for (const workspace of workspaces) {
		if (workspace?.workspaceKey && repositoryKey && workspace.repositoryKey === repositoryKey) {
			keys.add(workspace.workspaceKey);
		}
	}
	for (const key of keys) {
		for (const prefix of GIT_FACT_QUERIES) {
			if (
				prefix === "gitWorkspaceSnapshot" ||
				(pushed && ["gitStatus", "gitLineStats"].includes(prefix))
			)
				continue;
			qc.invalidateQueries({ queryKey: [prefix, key] });
		}
	}
	// Re-probe after success AND failure: remote writes may have completed before disconnecting.
	qc.invalidateQueries({ queryKey: ["gitWorkspace", target.narratorId] });
}

/**
 * A WS `git_status` push marks the badge/fact queries stale so active observers
 * refetch. Unlike {@link invalidateWorkspaceQueries} this never re-probes the
 * workspace itself: pushes arrive up to ~1/s during tool bursts, and a
 * discovery probe per push would be pure waste. Commit previews are immutable,
 * so only the live facts are invalidated.
 */
export function invalidateGitStatusPushQueries(
	qc: ReturnType<typeof useQueryClient>,
	narratorId: string,
): void {
	const keys = new Set<string>();
	for (const [, workspace] of qc.getQueriesData<GitWorkspace>({
		queryKey: ["gitWorkspace", narratorId],
	})) {
		if (workspace?.workspaceKey) keys.add(workspace.workspaceKey);
	}
	for (const key of keys) {
		for (const prefix of GIT_LIVE_FACT_QUERIES) {
			if (
				prefix === "gitWorkspaceSnapshot" ||
				(gitWorkspaceSubscriptions.get(qc)?.owns(key) &&
					["gitStatus", "gitLineStats"].includes(prefix))
			)
				continue;
			void qc.invalidateQueries({ queryKey: [prefix, key] });
		}
	}
}

type GitProjection<T, V extends GitWorkspaceView> = V extends "summary" ? Omit<T, "files"> : T;

function useGitFact<T>(
	target: GitTarget | null | undefined,
	view: GitWorkspaceView,
	kind: "status" | "lineStats",
	read: (target: GitTarget, signal: AbortSignal, fresh: boolean) => Promise<T>,
) {
	const qc = useQueryClient();
	const subscriptions = useGitWorkspaceSubscription(target, view);
	const key = gitTargetKey(target);
	const pushed = !!target && subscriptions.isPush(target);
	const denied = !!target && subscriptions.isDenied(target);
	const snapshot = useQuery<GitWorkspaceSnapshot>({
		queryKey: gitSnapshotKey(key, view),
		queryFn: skipToken,
		enabled: false,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
	const query = useQuery<T>({
		// HTTP returns a complete details response, so summary + panel share that read too.
		queryKey: gitFactKey(
			kind === "status" ? "gitStatus" : "gitLineStats",
			key,
			pushed ? view : "details",
		),
		queryFn: ({ signal }) =>
			readWorkspace(
				qc,
				target,
				() => read(target as GitTarget, signal, takeFreshGitRead(qc, key, kind)),
				kind === "status",
			),
		enabled: !!target && !denied && !pushed && subscriptions.isVisible(),
		staleTime: 5_000,
		refetchOnWindowFocus: false,
		refetchOnReconnect: false,
		retry: false,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
	const code = denied
		? "access_denied"
		: pushed
			? snapshot.data?.[kind === "status" ? "statusError" : "lineStatsError"]
			: undefined;
	const error = code ? Object.assign(new Error(code), { data: { code } }) : query.error;
	const data = denied ? undefined : query.data;
	const updating = !!target && !denied && pushed && (!snapshot.data || snapshot.data.updating);
	return {
		...query,
		data,
		error,
		isError: !!error,
		isFetching: query.isFetching || updating,
		isLoading: !data && !error && (query.isLoading || updating),
		updating,
		refetch: () => {
			if (target && subscriptions.refresh(target))
				return Promise.resolve({ ...query, data, error });
			requestFreshGitRead(qc, key, kind);
			return query.refetch();
		},
	};
}

export function useGitLineStats<V extends GitWorkspaceView = "details">(
	target: GitTarget | undefined | null,
	view: V = "details" as V,
) {
	return useGitFact<GitProjection<GitLineStats, V>>(
		target,
		view,
		"lineStats",
		(target, signal, fresh) =>
			api.getGitLineStats(target, signal, fresh) as Promise<GitProjection<GitLineStats, V>>,
	);
}

export function useGitStatus<V extends GitWorkspaceView = "details">(
	target: GitTarget | undefined | null,
	view: V = "details" as V,
) {
	return useGitFact<GitProjection<GitStatusSummary, V>>(
		target,
		view,
		"status",
		(target, signal, fresh) => api.getGitStatus(target, signal, fresh),
	);
}

/**
 * Minimum interval between two badge refreshes (ms). `focus` and
 * `visibilitychange` co-fire on a single return gesture, and a foreground return
 * usually also triggers the WS reconnect that raises the second signal
 * milliseconds later — without the throttle one user action buys two or three
 * invalidations of the same queries.
 */
const GIT_BADGE_REFRESH_MIN_INTERVAL_MS = 2_000;

/**
 * App-shell catch-up for legacy consumers and disconnected HTTP fallback only.
 * Subscribed statistics recover through snapshots, never ACK/focus/reconnect GETs.
 */
export function useGitBadgeRefresh() {
	const qc = useQueryClient();
	useEffect(() => {
		let lastRefreshAt = 0;
		const refresh = () => {
			const now = Date.now();
			if (now - lastRefreshAt < GIT_BADGE_REFRESH_MIN_INTERVAL_MS) return;
			lastRefreshAt = now;
			if (gitWorkspaceSubscriptions.get(qc)?.isVisible() === false) return;
			for (const prefix of ["gitStatus", "gitLineStats"])
				void qc.invalidateQueries({
					queryKey: [prefix],
					predicate: (query) => !gitWorkspaceSubscriptions.get(qc)?.owns(String(query.queryKey[1])),
				});
		};
		const unobserve = observePageLifecycle({ onForeground: refresh });
		const offConnection = narratorWSManager.onConnectionChange((connected, isReconnect) => {
			if (connected && isReconnect) refresh();
		});
		return () => {
			unobserve();
			offConnection();
		};
	}, [qc]);
}

/** Git change events invalidate this sooner; the window only absorbs repeated hovers. */
const GIT_FILE_ATTRIBUTION_STALE_MS = 10_000;

/**
 * One file's attribution, read only while its hover card is open. Mount it inside the
 * open card: unmounting the last observer aborts the request through `signal`, without
 * cancelling another open card that shares the key (index and worktree rows do).
 * A failure stays local to the card: 503 does not re-probe the whole workspace.
 */
export function useGitFileModifications(target: GitTarget | undefined | null, filePath: string) {
	const qc = useQueryClient();
	return useQuery<WorkspaceModificationView>({
		queryKey: ["gitModifications", gitTargetKey(target), "uncommitted", "byFile", filePath],
		queryFn: ({ signal }) =>
			readWorkspace(
				qc,
				target,
				() =>
					api.getGitModifications(
						target as GitTarget,
						{ scope: "uncommitted", projection: "byFile", filePath },
						signal,
					) as Promise<WorkspaceModificationView>,
				false,
			),
		enabled: !!target,
		retry: false,
		staleTime: GIT_FILE_ATTRIBUTION_STALE_MS,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
}

export function useGitLog(
	target: GitTarget | undefined | null,
	limit = 50,
	skip = 0,
	enabled = true,
) {
	const qc = useQueryClient();
	return useQuery<GitLogEntry[]>({
		queryKey: ["gitLog", gitTargetKey(target), limit, skip],
		queryFn: ({ signal }) =>
			readWorkspace(qc, target, () => api.getGitLog(target as GitTarget, limit, skip, signal)),
		enabled: !!target && enabled,
		retry: false,
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
		gcTime: 30_000,
	});
}

// Commit objects are immutable, but the route granting access is not. Keep the
// workspace prefix for invalidation, and scope previews to their narrator.
export function useGitCommitDetail(target: GitTarget | undefined | null, sha: string | null) {
	const qc = useQueryClient();
	return useQuery<GitCommitDetail>({
		queryKey: ["gitCommitDetail", gitTargetKey(target), sha, target ? gitBasePath(target) : null],
		queryFn: ({ signal }) =>
			readWorkspace(qc, target, () =>
				api.getGitCommitDetail(target as GitTarget, sha as string, signal),
			),
		enabled: !!target && !!sha,
		retry: false,
		staleTime: Number.POSITIVE_INFINITY,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
}

export function useGitCommitDiff(
	target: GitTarget | undefined | null,
	sha: string | null,
	file: string | null,
	oldPath?: string,
) {
	const qc = useQueryClient();
	return useQuery<GitCommitPatch>({
		queryKey: [
			"gitCommitDiff",
			gitTargetKey(target),
			sha,
			file,
			oldPath ?? null,
			target ? gitBasePath(target) : null,
		],
		queryFn: ({ signal }) =>
			readWorkspace(qc, target, () =>
				api.getGitCommitDiff(target as GitTarget, sha as string, file as string, oldPath, signal),
			),
		enabled: !!target && !!sha && !!file,
		retry: false,
		staleTime: Number.POSITIVE_INFINITY,
		gcTime: GIT_QUERY_GC_TIME_MS,
	});
}

function useGitMutation<T, R>(target: GitTarget, run: (value: T) => Promise<R>) {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: run,
		retry: false,
		onSettled: () => {
			invalidateWorkspaceQueries(qc, target, false);
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
	return useGitMutation(target, (message: string) => api.gitCommit(target, message));
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
	return useGitMutation(target, (params: { target: string; mode: "soft" | "hard" }) =>
		api.gitReset(target, params.target, params.mode),
	);
}
export function useGitAiCommitMessage(target: GitTarget) {
	return useGitMutation<void, { message: string }>(target, () => api.gitAiCommitMessage(target));
}
