import { afterAll, afterEach, expect, mock, test } from "bun:test";
import type { GitWorkspaceCategory, GitWorkspaceSnapshot } from "@shared/git-workspace-events";
import { posixPathSemantics } from "../lib/agent/execution/path-semantics";
import { AppError } from "../lib/errors";

function required<T>(value: T | undefined): T {
	if (value === undefined) throw new Error("Missing expected snapshot fixture value");
	return value;
}
async function waitForState(predicate: () => boolean, timeoutMs = 2000): Promise<void> {
	const deadline = performance.now() + timeoutMs;
	while (!predicate()) {
		if (performance.now() >= deadline)
			throw new Error("Timed out waiting for observable Git watch state");
		await new Promise<void>((resolve) => setTimeout(resolve, 1));
	}
}

import type { GitWorkspaceTarget } from "../services/git-workspace";

const accessModule = await import("../services/git-workspace-access");
const watchModule = await import("../services/git-workspace-watch");
const managementModule = await import("../services/git-management-service");
const sessionStoreModule = await import("../services/auth/store");
const originalStores = {
	session: sessionStoreModule.authSessionStore,
	mfa: sessionStoreModule.authMfaStore,
};
let liveRole: "admin" | "user" = "user";
let liveVersion = 0;
let sessionMissing = false;
let sessionFailure: unknown;
sessionStoreModule.setAuthStores({
	...originalStores,
	session: {
		...originalStores.session,
		async findSessionState(userId) {
			if (sessionFailure) throw sessionFailure;
			return sessionMissing ? null : { id: userId, role: liveRole, tokenVersion: liveVersion };
		},
	},
});
const completedSamples: Array<{ generation: number; worktree?: string }> = [];
const snapshotFacts = (revision = 1) => ({
	revision,
	updating: false,
	status: {
		hasChanges: true,
		staged: 0,
		unstaged: 1,
		untracked: 0,
		totalFiles: 1,
		totalFilesExact: true,
		headSha: "sha",
		branch: "main",
		files: [{ status: " M", path: "private-file" }],
	},
	lineStats: {
		linesAdded: 12,
		linesRemoved: 2,
		complete: false,
		files: [{ path: "private-file", complete: false }],
	},
	detailsTruncated: true,
});
let subscriptions = 0;
let invalidations = 0;
const refreshed: GitWorkspaceTarget[] = [];
const snapshotCallbacks: Array<(snapshot: GitWorkspaceSnapshot) => void> = [];
const deniedUsers = new Set<string>();
const realPool = new watchModule.GitWorkspaceWatchPool(
	async (target, signal) => {
		const sample = await watchModule.probeGitWatch(target, signal);
		completedSamples.push({
			generation: target.backend?.runtimeGeneration ?? 0,
			worktree: sample.fingerprints.worktree,
		});
		return sample;
	},
	5,
	{
		status: async () => snapshotFacts().status,
		lineStats: async () => snapshotFacts().lineStats,
		invalidate: () => {},
		validate: () => {},
	},
);
let integrationTarget: GitWorkspaceTarget | undefined;
const integrationCallbacks = new Set<(categories: GitWorkspaceCategory[]) => Promise<void>>();
let denied = false;
let failure: unknown;
let authorizations = 0;
let hold: Promise<void> | undefined;
let notify: ((categories: GitWorkspaceCategory[]) => Promise<void>) | undefined;
let stops = 0;
let principal = "";
let principalIsAdmin = false;
let adminOnly = false;
let requestedKey: string | undefined;
mock.module("../services/git-workspace-access", () => ({
	...accessModule,
	authorizeGitTargetForPrincipal: async (
		user: { userId: string; isAdmin: boolean },
		_origin: unknown,
		_need: unknown,
		_signal: AbortSignal,
		workspaceKey?: string,
	) => {
		authorizations++;
		principal = user.userId;
		principalIsAdmin = user.isAdmin;
		requestedKey = workspaceKey;
		await hold;
		if (adminOnly && !user.isAdmin)
			throw new AppError("denied", 403, "GIT_WORKSPACE_ACCESS_DENIED");
		if (denied || deniedUsers.has(user.userId)) throw new Error("denied");
		if (failure) throw failure;
		if (integrationTarget) return integrationTarget;
		return {
			workspace: {
				workspaceKey: workspaceKey ?? "key",
				repositoryKey: "repo",
				state: "ready",
				capabilities: { read: true },
			},
			backend: { runtimeGeneration: 1 },
		};
	},
	requireReadyGitTarget: () => {},
}));
mock.module("../services/git-workspace-watch", () => ({
	...watchModule,
	GitWorkspaceWatchPool: class {
		refresh(target: GitWorkspaceTarget) {
			refreshed.push(target);
			if (integrationTarget) realPool.refresh(target);
		}
		subscribe(
			target: GitWorkspaceTarget,
			changed: NonNullable<typeof notify>,
			snapshot?: (value: GitWorkspaceSnapshot) => void,
		) {
			subscriptions++;
			if (snapshot) snapshotCallbacks.push(snapshot);
			if (integrationTarget) {
				integrationCallbacks.add(changed);
				const stop = realPool.subscribe(target, changed, snapshot);
				return () => {
					integrationCallbacks.delete(changed);
					stop();
				};
			}
			notify = changed;
			return () => {
				stops++;
			};
		}
	},
}));
mock.module("../services/git-management-service", () => ({
	...managementModule,
	invalidateGitWorkspace: () => {
		invalidations++;
	},
}));
const { handleGitWorkspaceMessage, releaseGitWorkspaceSubscriptions } = await import(
	"./git-workspace-ws"
);
const frames: {
	type: string;
	workspaceKey?: string;
	snapshot?: GitWorkspaceSnapshot;
	view?: string;
	version?: number;
}[] = [];
const ws = {
	data: {
		userId: "owner",
		userRole: "user",
		sessionUserId: "owner",
		sessionExp: Math.ceil(Date.now() / 1000) + 3600,
		sessionTokenVersion: 0,
	},
	send(raw: string) {
		frames.push(JSON.parse(raw));
	},
};
const message = {
	type: "git_workspace_subscribe" as const,
	subscriptionId: "panel",
	narratorId: "narrator",
};
afterEach(() => {
	releaseGitWorkspaceSubscriptions(ws);
	denied = false;
	failure = undefined;
	authorizations = 0;
	hold = undefined;
	integrationTarget = undefined;
	integrationCallbacks.clear();
	completedSamples.length = 0;
	frames.length = 0;
	stops = 0;
	liveRole = "user";
	liveVersion = 0;
	sessionMissing = false;
	sessionFailure = undefined;
	adminOnly = false;
	principalIsAdmin = false;
	requestedKey = undefined;
	ws.data.userRole = "user";
	ws.data.sessionExp = Math.ceil(Date.now() / 1000) + 3600;
	ws.data.sessionTokenVersion = 0;
	subscriptions = 0;
	invalidations = 0;
	refreshed.length = 0;
	snapshotCallbacks.length = 0;
	deniedUsers.clear();
	notify = undefined;
});
afterAll(() => {
	sessionStoreModule.setAuthStores(originalStores);
	mock.module("../services/git-workspace-access", () => accessModule);
	mock.module("../services/git-workspace-watch", () => watchModule);
	mock.module("../services/git-management-service", () => managementModule);
});
test("ack contains server-resolved identity; notification rechecks principal", async () => {
	await handleGitWorkspaceMessage(ws, message);
	expect(principal).toBe("owner");
	expect(principalIsAdmin).toBe(false);
	expect(frames[0]).toMatchObject({ type: "git_workspace_subscribed", workspaceKey: "key" });
	await notify?.(["worktree"]);
	expect(frames.at(-1)?.type).toBe("git_workspace_changed");
});
test("a live role downgrade revokes admin-only change metadata on the same socket", async () => {
	adminOnly = true;
	liveRole = "admin";
	ws.data.userRole = "admin";
	await handleGitWorkspaceMessage(ws, message);
	expect(principalIsAdmin).toBe(true);
	expect(frames.map((frame) => frame.type)).toEqual(["git_workspace_subscribed"]);

	liveRole = "user";
	await notify?.(["refs"]);
	expect(principalIsAdmin).toBe(false);
	expect(authorizations).toBe(2);
	expect(frames.map((frame) => frame.type)).toEqual([
		"git_workspace_subscribed",
		"git_workspace_error",
	]);
	expect(frames.at(-1)).toMatchObject({ code: "GIT_WORKSPACE_ACCESS_DENIED" });
	expect(stops).toBe(1);
});
test("demotion revokes the old session even when the socket still holds its admin role", async () => {
	liveRole = "admin";
	ws.data.userRole = "admin";
	await handleGitWorkspaceMessage(ws, message);
	liveRole = "user";
	liveVersion++;
	await notify?.(["worktree"]);
	expect(frames.map((frame) => frame.type)).toEqual([
		"git_workspace_subscribed",
		"git_workspace_error",
	]);
	expect(stops).toBe(1);
});
test("a renewed session with the current token version still subscribes", async () => {
	liveVersion = 2;
	ws.data.sessionTokenVersion = 2;
	await handleGitWorkspaceMessage(ws, message);
	expect(frames[0]).toMatchObject({ type: "git_workspace_subscribed", workspaceKey: "key" });
	await notify?.(["worktree"]);
	expect(frames.at(-1)?.type).toBe("git_workspace_changed");
});
test("a selected child Git workspace key reaches the target authorizer", async () => {
	await handleGitWorkspaceMessage(ws, { ...message, workspaceKey: "child-key" });
	expect(requestedKey).toBe("child-key");
	expect(frames[0]).toMatchObject({ type: "git_workspace_subscribed", workspaceKey: "child-key" });
	await notify?.(["index"]);
	expect(requestedKey).toBe("child-key");
	expect(frames.at(-1)).toMatchObject({ type: "git_workspace_changed", workspaceKey: "child-key" });
});
test("denied subscription never acks; ambiguous target fails closed", async () => {
	denied = true;
	await handleGitWorkspaceMessage(ws, message);
	expect(frames.map((frame) => frame.type)).toEqual(["git_workspace_error"]);
	frames.length = 0;
	await handleGitWorkspaceMessage(ws, { ...message, chapterId: "chapter" });
	expect(frames.map((frame) => frame.type)).toEqual(["git_workspace_error"]);
});
test("revocation stops subscription without disclosing changes", async () => {
	await handleGitWorkspaceMessage(ws, message);
	denied = true;
	await notify?.(["refs"]);
	expect(frames.at(-1)?.type).toBe("git_workspace_error");
	expect(stops).toBe(1);
});
test("deleted users and expired sessions cannot subscribe", async () => {
	sessionMissing = true;
	await handleGitWorkspaceMessage(ws, message);
	expect(frames.map((frame) => frame.type)).toEqual(["git_workspace_error"]);
	expect(authorizations).toBe(0);
	frames.length = 0;
	sessionMissing = false;
	ws.data.sessionExp = Math.floor(Date.now() / 1000) - 1;
	await handleGitWorkspaceMessage(ws, message);
	expect(frames.map((frame) => frame.type)).toEqual(["git_workspace_error"]);
	expect(authorizations).toBe(0);
});
test("a DB failure never falls back to the socket's cached admin role", async () => {
	liveRole = "admin";
	ws.data.userRole = "admin";
	await handleGitWorkspaceMessage(ws, message);
	sessionFailure = new Error("DB unavailable");
	await notify?.(["refs"]);
	expect(frames.map((frame) => frame.type)).toEqual([
		"git_workspace_subscribed",
		"git_workspace_error",
	]);
	expect(frames.at(-1)).toMatchObject({ code: "GIT_WORKSPACE_UNAVAILABLE" });
	expect(authorizations).toBe(1);
	expect(stops).toBe(1);
});
test("unsubscribe during authorization prevents late ack and watcher", async () => {
	let finish = () => {};
	hold = new Promise<void>((resolve) => {
		finish = resolve;
	});
	const subscribing = handleGitWorkspaceMessage(ws, message);
	await handleGitWorkspaceMessage(ws, {
		type: "git_workspace_unsubscribe",
		subscriptionId: "panel",
	});
	finish();
	await subscribing;
	expect(frames).toHaveLength(0);
});
test("disconnect disposes watcher and suppresses stale callbacks", async () => {
	await handleGitWorkspaceMessage(ws, message);
	releaseGitWorkspaceSubscriptions(ws);
	await notify?.(["index"]);
	expect(stops).toBe(1);
	expect(frames).toHaveLength(1);
});

test("idle heartbeats do not repeat the expensive authorization scan", async () => {
	await handleGitWorkspaceMessage(ws, message);
	await notify?.([]);
	await notify?.([]);
	expect(authorizations).toBe(1);
	expect(frames).toHaveLength(1);
	await notify?.(["worktree"]);
	expect(authorizations).toBe(2);
});

test("temporary device outage retries the subscription without browser polling", async () => {
	await handleGitWorkspaceMessage(ws, message);
	failure = new AppError("offline", 503, "GIT_WATCH_RETRYABLE");
	await notify?.(["worktree"]);
	expect(frames.map((frame) => frame.type)).toEqual(["git_workspace_subscribed"]);
	expect(stops).toBe(1);
	failure = undefined;
	await waitForState(
		() =>
			frames.filter((frame) => frame.type === "git_workspace_subscribed").length === 2 &&
			frames.at(-1)?.type === "git_workspace_changed",
		4000,
	);
	expect(frames.filter((frame) => frame.type === "git_workspace_subscribed")).toHaveLength(2);
	expect(frames.at(-1)?.type).toBe("git_workspace_changed");
}, 10_000);

test("two sockets rebind a real shared watch pool after a generation change without offline", async () => {
	const otherFrames: typeof frames = [];
	const other = {
		...ws,
		send(raw: string) {
			otherFrames.push(JSON.parse(raw));
		},
	};
	let generation = 1;
	let content = "initial";
	const rpcGenerations: number[] = [];
	const makeTarget = (): GitWorkspaceTarget => {
		const boundGeneration = generation;
		return {
			workspace: {
				deviceId: "remote",
				rootPath: "/repo",
				workspaceKey: "key",
				repositoryKey: "repo",
				state: "ready",
				capabilities: { read: true },
			},
			backend: {
				kind: "remote",
				deviceId: "remote",
				pathFlavor: "posix",
				paths: posixPathSemantics,
				runtimeGeneration: boundGeneration,
				supportsGitWorkspaceWatch: true,
				gitWorkspace: async () => {
					rpcGenerations.push(boundGeneration);
					if (boundGeneration !== generation) throw new Error("Stale device generation");
					return { outputs: { worktree: content, index: "index", head: "head", stash: "stash" } };
				},
			},
		} as unknown as GitWorkspaceTarget;
	};
	integrationTarget = makeTarget();
	let disposedCallbacks: Array<(categories: GitWorkspaceCategory[]) => Promise<void>> = [];
	try {
		await handleGitWorkspaceMessage(ws, message);
		await handleGitWorkspaceMessage(other, message);
		await waitForState(
			() =>
				integrationCallbacks.size === 2 &&
				completedSamples.filter((sample) => sample.generation === 1).length >= 2,
		);
		expect(integrationCallbacks.size).toBe(2);
		generation = 2;
		integrationTarget = makeTarget();
		// Both authorized notifications rebind independently: stop A / subscribe
		// fresh A / stop B / subscribe fresh B, while the pool remains shared.
		await Promise.all([...integrationCallbacks].map((callback) => callback(["worktree"])));
		const before = rpcGenerations.length;
		await waitForState(
			() =>
				completedSamples.filter((sample) => sample.generation === 2).length >= 2 &&
				rpcGenerations.length > before &&
				[frames, otherFrames].every(
					(messages) =>
						messages.filter((frame) => frame.type === "git_workspace_subscribed").length === 2,
				),
		);
		expect(rpcGenerations.length).toBeGreaterThan(before);
		expect(rpcGenerations.slice(before).every((value) => value === 2)).toBe(true);
		for (const messages of [frames, otherFrames]) {
			expect(messages.filter((frame) => frame.type === "git_workspace_subscribed")).toHaveLength(2);
			expect(messages.some((frame) => frame.type === "git_workspace_error")).toBe(false);
			messages.length = 0;
		}
		content = "edited after reconnect";
		await waitForState(
			() =>
				frames.some((frame) => frame.type === "git_workspace_changed") &&
				otherFrames.some((frame) => frame.type === "git_workspace_changed"),
		);
		expect(frames.some((frame) => frame.type === "git_workspace_changed")).toBe(true);
		expect(otherFrames.some((frame) => frame.type === "git_workspace_changed")).toBe(true);
		disposedCallbacks = [...integrationCallbacks];
	} finally {
		releaseGitWorkspaceSubscriptions(ws);
		releaseGitWorkspaceSubscriptions(other);
	}
	await waitForState(() => integrationCallbacks.size === 0);
	const before = rpcGenerations.length;
	const beforeAuthorization = authorizations;
	const beforeFrames = JSON.stringify([frames, otherFrames]);
	await Promise.all(disposedCallbacks.map((callback) => callback(["worktree"])));
	await new Promise<void>((resolve) => setImmediate(resolve));
	expect(rpcGenerations).toHaveLength(before);
	expect(authorizations).toBe(beforeAuthorization);
	expect(JSON.stringify([frames, otherFrames])).toBe(beforeFrames);
});

test("backpressure closes the socket instead of silently losing its only change", async () => {
	let closed = false;
	const congested = {
		...ws,
		getBufferedAmount: () => 300 * 1024,
		close: () => {
			closed = true;
		},
	};
	await handleGitWorkspaceMessage(congested, message);
	expect(closed).toBe(true);
	expect(stops).toBe(0);
	expect(subscriptions).toBe(0);
	expect(snapshotCallbacks).toHaveLength(0);
	expect(frames).toHaveLength(0);
});

test("subscription attaches after ACK without invalidating shared facts", async () => {
	await handleGitWorkspaceMessage(ws, message);
	expect(frames.map((frame) => frame.type)).toEqual(["git_workspace_subscribed"]);
	expect(subscriptions).toBe(1);
	expect(snapshotCallbacks).toHaveLength(1);
	expect(invalidations).toBe(0);
	expect(refreshed).toHaveLength(0);
});

test("summary and details independently authorize their shared snapshot projections", async () => {
	await handleGitWorkspaceMessage(ws, { ...message, view: "summary" });
	await handleGitWorkspaceMessage(ws, { ...message, subscriptionId: "details", view: "details" });
	const before = authorizations;
	for (const callback of snapshotCallbacks) callback(snapshotFacts());
	await waitForState(
		() => frames.filter((frame) => frame.type === "git_workspace_snapshot").length === 2,
	);
	expect(authorizations).toBe(before + 2);
	const summary = required(frames.find((frame) => frame.view === "summary")?.snapshot);
	const details = required(frames.find((frame) => frame.view === "details")?.snapshot);
	expect(summary.status?.files).toBeUndefined();
	expect(summary.lineStats?.files).toBeUndefined();
	expect(summary.detailsTruncated).toBeUndefined();
	expect(summary.lineStats).toEqual({ linesAdded: 12, linesRemoved: 2, complete: false });
	expect(details).toEqual(snapshotFacts());
	expect(invalidations).toBe(0);
});

test("one subscription switches summary to details and back by replaying its authorized snapshot", async () => {
	await handleGitWorkspaceMessage(ws, { ...message, view: "summary" });
	const publish = required(snapshotCallbacks[0]);
	publish(snapshotFacts(42));
	await waitForState(() => frames.length === 2);
	const initial = required(frames.at(-1)?.snapshot);
	expect(initial.status?.files).toBeUndefined();
	expect(initial.lineStats?.files).toBeUndefined();
	expect(authorizations).toBe(2);

	await handleGitWorkspaceMessage(ws, { ...message, view: "details" });
	expect(authorizations).toBe(3);
	expect(frames.at(-1)).toMatchObject({
		type: "git_workspace_snapshot",
		view: "details",
		snapshot: snapshotFacts(42),
	});
	expect(subscriptions).toBe(1);
	expect(stops).toBe(0);
	expect(invalidations).toBe(0);
	expect(refreshed).toHaveLength(0);

	await handleGitWorkspaceMessage(ws, { ...message, view: "summary" });
	expect(authorizations).toBe(4);
	expect(frames.at(-1)).toMatchObject({
		type: "git_workspace_snapshot",
		view: "summary",
		snapshot: initial,
	});
	expect(frames.at(-1)?.snapshot?.detailsTruncated).toBeUndefined();
	expect(frames.at(-1)?.snapshot?.status?.files).toBeUndefined();
	expect(frames.at(-1)?.snapshot?.lineStats?.files).toBeUndefined();
	expect(frames.map((frame) => frame.type)).toEqual([
		"git_workspace_subscribed",
		"git_workspace_snapshot",
		"git_workspace_snapshot",
		"git_workspace_snapshot",
	]);
	expect(frames.map((frame) => frame.version)).toEqual([1, 2, 3, 4]);
	expect(subscriptions).toBe(1);
	expect(snapshotCallbacks).toEqual([publish]);
	expect(stops).toBe(0);
	expect(invalidations).toBe(0);
	expect(refreshed).toHaveLength(0);

	// The original pool callback remains live and uses the newly selected projection.
	publish(snapshotFacts(43));
	await waitForState(() => frames.length === 5);
	expect(frames.at(-1)).toMatchObject({ view: "summary", version: 5, snapshot: { revision: 43 } });
	expect(frames.at(-1)?.snapshot?.status?.files).toBeUndefined();
	expect(frames.at(-1)?.snapshot?.lineStats?.files).toBeUndefined();
	expect(subscriptions).toBe(1);
});

test("revoked access rejects a same-id details upgrade without disclosing its retained snapshot", async () => {
	await handleGitWorkspaceMessage(ws, { ...message, view: "summary" });
	const publish = required(snapshotCallbacks[0]);
	publish(snapshotFacts());
	await waitForState(() => frames.length === 2);
	expect(JSON.stringify(frames)).not.toContain("private-file");
	denied = true;
	await handleGitWorkspaceMessage(ws, { ...message, view: "details" });
	expect(authorizations).toBe(3);
	expect(frames.map((frame) => frame.type)).toEqual([
		"git_workspace_subscribed",
		"git_workspace_snapshot",
		"git_workspace_error",
	]);
	expect(JSON.stringify(frames)).not.toContain("private-file");
	expect(frames.some((frame) => frame.view === "details")).toBe(false);
	expect(subscriptions).toBe(1);
	expect(stops).toBe(1);
	expect(invalidations).toBe(0);
	expect(refreshed).toHaveLength(0);
	publish(snapshotFacts(2));
	await new Promise<void>((resolve) => setImmediate(resolve));
	expect(frames).toHaveLength(3);
	expect(authorizations).toBe(3);
});

test("a shared snapshot never bypasses another principal's revoked access", async () => {
	const otherFrames: typeof frames = [];
	const other = {
		data: { userId: "viewer", userRole: "user" },
		send: (raw: string) => {
			otherFrames.push(JSON.parse(raw));
		},
	};
	try {
		await handleGitWorkspaceMessage(ws, message);
		await handleGitWorkspaceMessage(other, message);
		deniedUsers.add("viewer");
		for (const callback of snapshotCallbacks) callback(snapshotFacts());
		await waitForState(() => frames.length === 2 && otherFrames.length === 2);
		expect(frames.at(-1)?.type).toBe("git_workspace_snapshot");
		expect(otherFrames.map((frame) => frame.type)).toEqual([
			"git_workspace_subscribed",
			"git_workspace_error",
		]);
		expect(JSON.stringify(otherFrames)).not.toContain("private-file");
		expect(stops).toBe(1);
	} finally {
		releaseGitWorkspaceSubscriptions(other);
	}
});

test("manual refresh reauthorizes; a revoked or unknown subscription cannot refresh", async () => {
	await handleGitWorkspaceMessage(ws, message);
	await handleGitWorkspaceMessage(ws, { type: "git_workspace_refresh", subscriptionId: "missing" });
	expect(refreshed).toHaveLength(0);
	expect(authorizations).toBe(1);
	await handleGitWorkspaceMessage(ws, { type: "git_workspace_refresh", subscriptionId: "panel" });
	expect(refreshed).toHaveLength(1);
	expect(authorizations).toBe(2);
	denied = true;
	await handleGitWorkspaceMessage(ws, { type: "git_workspace_refresh", subscriptionId: "panel" });
	expect(authorizations).toBe(3);
	expect(refreshed).toHaveLength(1);
	expect(stops).toBe(1);
	expect(frames.at(-1)?.type).toBe("git_workspace_error");
});

test("pending snapshots coalesce to newest revision and its latest phase during authorization", async () => {
	await handleGitWorkspaceMessage(ws, message);
	let finish = () => {};
	hold = new Promise<void>((resolve) => {
		finish = resolve;
	});
	const publish = required(snapshotCallbacks[0]);
	try {
		publish({ ...snapshotFacts(10), updating: true });
		publish({ ...snapshotFacts(12), updating: true });
		publish(snapshotFacts(11));
		publish(snapshotFacts(12));
		expect(frames).toHaveLength(1);
		finish();
		await waitForState(() => frames.length === 2);
		expect(frames[1]?.snapshot).toEqual(snapshotFacts(12));
		expect(authorizations).toBe(2);
		expect(frames[1]?.version).toBeGreaterThan(frames[0]?.version ?? 0);
	} finally {
		finish();
	}
});

test("snapshot backpressure disposes an attached watcher and suppresses late snapshots", async () => {
	let buffered = 0;
	let closed = false;
	const congested = {
		...ws,
		getBufferedAmount: () => buffered,
		close: () => {
			closed = true;
		},
	};
	try {
		await handleGitWorkspaceMessage(congested, message);
		expect(subscriptions).toBe(1);
		buffered = 300 * 1024;
		const publish = required(snapshotCallbacks[0]);
		publish(snapshotFacts());
		await waitForState(() => closed);
		expect(stops).toBe(1);
		expect(frames).toHaveLength(1);
		const checked = authorizations;
		publish(snapshotFacts(2));
		await new Promise<void>((resolve) => setImmediate(resolve));
		expect(authorizations).toBe(checked);
		expect(frames).toHaveLength(1);
	} finally {
		releaseGitWorkspaceSubscriptions(congested);
	}
});
