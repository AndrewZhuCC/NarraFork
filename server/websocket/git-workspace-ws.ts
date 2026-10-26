import type {
	GitWorkspaceCategory,
	GitWorkspaceClientMessage,
	GitWorkspaceServerMessage,
	GitWorkspaceSnapshot,
	GitWorkspaceSubscribe,
	GitWorkspaceView,
} from "@shared/git-workspace-events";
import { resolveLiveSession } from "../lib/auth";
import { AppError } from "../lib/errors";
import type { GitWorkspaceTarget } from "../services/git-workspace";
import { assertGitWorkspaceKey } from "../services/git-workspace";
import {
	authorizeGitTargetForPrincipal,
	requireReadyGitTarget,
} from "../services/git-workspace-access";
import { GitWorkspaceWatchPool, projectGitSnapshot } from "../services/git-workspace-watch";

interface Socket {
	data: {
		userId?: string;
		sessionId?: string;
		sessionUserId?: string;
		sessionExp?: number;
	};
	send(message: string): unknown;
	getBufferedAmount?(): number;
	close?(code?: number, reason?: string): void;
}
interface Subscription {
	target: GitWorkspaceSubscribe;
	view: GitWorkspaceView;
	updateView?: (view: GitWorkspaceView) => Promise<void>;
	controller: AbortController;
	stop?: () => void;
	checking: boolean;
	pending: Set<GitWorkspaceCategory>;
	pendingSnapshot?: GitWorkspaceSnapshot;
	refreshRequested?: boolean;
	refresh?: () => Promise<void>;
	retryTimer?: ReturnType<typeof setTimeout>;
}
const sockets = new Map<Socket, Map<string, Subscription>>();
const pool = new GitWorkspaceWatchPool();
function send(ws: Socket, message: GitWorkspaceServerMessage) {
	// Never silently lose an ack/change: force reconnect and snapshot recovery.
	if ((ws.getBufferedAmount?.() ?? 0) > 256 * 1024) {
		releaseGitWorkspaceSubscriptions(ws);
		ws.close?.(1013, "Git workspace backpressure; reconnect");
		return;
	}
	try {
		ws.send(JSON.stringify(message));
	} catch {
		releaseGitWorkspaceSubscriptions(ws);
	}
}
export function releaseGitWorkspaceSubscriptions(ws: Socket): void {
	for (const sub of sockets.get(ws)?.values() ?? []) {
		sub.controller.abort();
		clearTimeout(sub.retryTimer);
		sub.stop?.();
	}
	sockets.delete(ws);
}
function cancel(ws: Socket, id: string) {
	const entries = sockets.get(ws);
	const sub = entries?.get(id);
	sub?.controller.abort();
	clearTimeout(sub?.retryTimer);
	sub?.stop?.();
	entries?.delete(id);
	if (!entries?.size) sockets.delete(ws);
}
async function authorize(ws: Socket, message: GitWorkspaceSubscribe, signal: AbortSignal) {
	const userId = ws.data.userId;
	if (!userId || !message.narratorId) throw new Error("Invalid Git subscription target");
	if (!ws.data.sessionId || ws.data.sessionUserId !== userId)
		throw new AppError("Git workspace access denied", 403, "GIT_WORKSPACE_ACCESS_DENIED");
	const session = await resolveLiveSession(ws.data.sessionId);
	if (!session || session.sub !== userId)
		throw new AppError("Git workspace access denied", 403, "GIT_WORKSPACE_ACCESS_DENIED");
	ws.data.sessionExp = session.exp;
	const origin = { narratorId: message.narratorId };
	const target = await authorizeGitTargetForPrincipal(
		{ userId, isAdmin: session.role === "admin" },
		origin,
		"read",
		signal,
		message.workspaceKey,
	);
	if (target.workspace.state === "device_offline")
		throw new AppError("Git device temporarily offline", 503, "GIT_WATCH_RETRYABLE");
	requireReadyGitTarget(target, "read");
	if (message.workspaceKey) assertGitWorkspaceKey(target.workspace, message.workspaceKey);
	return target;
}
export async function handleGitWorkspaceMessage(
	ws: Socket,
	message: GitWorkspaceClientMessage,
): Promise<void> {
	if (message.type === "git_workspace_refresh") {
		await sockets.get(ws)?.get(message.subscriptionId)?.refresh?.();
		return;
	}
	const existing = sockets.get(ws)?.get(message.subscriptionId);
	if (
		message.type === "git_workspace_subscribe" &&
		existing &&
		existing.target.narratorId === message.narratorId &&
		existing.target.workspaceKey === message.workspaceKey
	) {
		await existing.updateView?.(message.view ?? "details");
		return;
	}
	cancel(ws, message.subscriptionId);
	if (message.type !== "git_workspace_subscribe") return;
	let entries = sockets.get(ws);
	if (!entries) {
		entries = new Map();
		sockets.set(ws, entries);
	}
	const fail = (error?: unknown) =>
		send(ws, {
			type: "git_workspace_error",
			subscriptionId: message.subscriptionId,
			code: error instanceof AppError ? error.code : "GIT_WORKSPACE_UNAVAILABLE",
			message: "Git workspace subscription unavailable; refresh workspace and retry",
		});
	if (entries.size >= 16) {
		fail();
		return;
	}
	const sub: Subscription = {
		target: message,
		view: message.view ?? "details",
		controller: new AbortController(),
		checking: false,
		pending: new Set(),
	};
	entries.set(message.subscriptionId, sub);
	const signal = sub.controller.signal;
	let currentTarget: GitWorkspaceTarget | undefined;
	let expectedKey = message.workspaceKey;
	let lastAuthorizedAt = 0;
	let failures = 0;
	let version = 0;
	let lastSnapshot: GitWorkspaceSnapshot | undefined;
	const run = async (categories: GitWorkspaceCategory[], snapshot?: GitWorkspaceSnapshot) => {
		if (signal.aborted) return;
		for (const category of categories) sub.pending.add(category);
		if (snapshot && (!sub.pendingSnapshot || snapshot.revision >= sub.pendingSnapshot.revision)) {
			sub.pendingSnapshot = snapshot;
			lastSnapshot = snapshot;
		}
		if (sub.checking || sub.retryTimer) return;
		// Quiet workspaces do not redo the full ACL/path scan every three seconds.
		// Every actual notification still passes a fresh authorization check.
		if (
			currentTarget &&
			!sub.pending.size &&
			!sub.pendingSnapshot &&
			!sub.refreshRequested &&
			Date.now() - lastAuthorizedAt < 30_000
		)
			return;
		sub.checking = true;
		try {
			const fresh = await authorize(
				ws,
				{ ...message, workspaceKey: expectedKey },
				AbortSignal.any([signal, AbortSignal.timeout(20_000)]),
			);
			if (signal.aborted) return;
			const { workspaceKey, repositoryKey } = fresh.workspace;
			if (!workspaceKey || !repositoryKey) throw new Error("Missing Git workspace identity");
			const identity = {
				subscriptionId: message.subscriptionId,
				narratorId: message.narratorId,
				workspaceKey,
				repositoryKey,
			};
			const rebind =
				!currentTarget ||
				currentTarget.backend?.runtimeGeneration !== fresh.backend?.runtimeGeneration;
			if (rebind) {
				sub.stop?.();
				currentTarget = fresh;
				expectedKey = workspaceKey;
				sub.pendingSnapshot = undefined;
				lastSnapshot = undefined;
				// New readers consume the shared snapshot; subscribing is not a filesystem change.
				send(ws, { type: "git_workspace_subscribed", ...identity, version: ++version });
				if (signal.aborted) return;
				sub.stop = pool.subscribe(fresh, run, (value) => void run([], value));
			}
			lastAuthorizedAt = Date.now();
			failures = 0;
			if (sub.refreshRequested) {
				sub.refreshRequested = false;
				pool.refresh(fresh);
			}
			if (sub.pending.size) {
				send(ws, {
					type: "git_workspace_changed",
					...identity,
					categories: [...sub.pending],
					version: ++version,
				});
				sub.pending.clear();
			}
			if (sub.pendingSnapshot && !signal.aborted) {
				const value = sub.pendingSnapshot;
				sub.pendingSnapshot = undefined;
				const view = sub.view;
				send(ws, {
					type: "git_workspace_snapshot",
					...identity,
					version: ++version,
					view,
					snapshot: projectGitSnapshot(value, view),
				});
			}
		} catch (error) {
			if (signal.aborted) return;
			if (
				(error instanceof AppError && error.statusCode >= 500) ||
				(error instanceof Error && error.name === "TimeoutError")
			) {
				// Device/network failures keep the subscription alive with bounded backoff;
				// browser REST polling is not needed to discover recovery.
				failures = Math.min(failures + 1, 4);
				lastAuthorizedAt = 0;
				sub.stop?.();
				sub.stop = undefined;
				currentTarget = undefined;
				sub.retryTimer = setTimeout(
					() => {
						sub.retryTimer = undefined;
						void run([]);
					},
					Math.min(60_000, 3000 * 2 ** (failures - 1)),
				);
				sub.retryTimer.unref?.();
			} else {
				fail(error);
				cancel(ws, message.subscriptionId);
			}
		} finally {
			sub.checking = false;
		}
	};
	// Projection changes reuse the directory observer and its existing results.
	sub.updateView = async (view) => {
		sub.view = view;
		await run([], lastSnapshot);
	};
	sub.refresh = async () => {
		sub.refreshRequested = true;
		clearTimeout(sub.retryTimer);
		sub.retryTimer = undefined;
		await run([]);
	};
	await run([]);
}
