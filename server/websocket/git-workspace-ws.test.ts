import { afterAll, afterEach, expect, mock, test } from "bun:test";
import type { GitWorkspaceCategory } from "@shared/git-workspace-events";
import { AppError } from "../lib/errors";
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
const realPool = new watchModule.GitWorkspaceWatchPool(watchModule.probeGitWatch, 5);
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
		if (denied) throw new Error("denied");
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
		subscribe(target: GitWorkspaceTarget, changed: NonNullable<typeof notify>) {
			if (integrationTarget) {
				integrationCallbacks.add(changed);
				const stop = realPool.subscribe(target, changed);
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
	invalidateGitWorkspace: () => {},
}));
const { handleGitWorkspaceMessage, releaseGitWorkspaceSubscriptions } = await import(
	"./git-workspace-ws"
);
const frames: { type: string; workspaceKey?: string }[] = [];
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
	await new Promise((resolve) => setTimeout(resolve, 3100));
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
	try {
		await handleGitWorkspaceMessage(ws, message);
		await handleGitWorkspaceMessage(other, message);
		await Bun.sleep(20);
		expect(integrationCallbacks.size).toBe(2);
		generation = 2;
		integrationTarget = makeTarget();
		// Both authorized notifications rebind independently: stop A / subscribe
		// fresh A / stop B / subscribe fresh B, while the pool remains shared.
		await Promise.all([...integrationCallbacks].map((callback) => callback(["worktree"])));
		const before = rpcGenerations.length;
		await Bun.sleep(30);
		expect(rpcGenerations.length).toBeGreaterThan(before);
		expect(rpcGenerations.slice(before).every((value) => value === 2)).toBe(true);
		for (const messages of [frames, otherFrames]) {
			expect(messages.filter((frame) => frame.type === "git_workspace_subscribed")).toHaveLength(2);
			expect(messages.some((frame) => frame.type === "git_workspace_error")).toBe(false);
			messages.length = 0;
		}
		content = "edited after reconnect";
		await Bun.sleep(30);
		expect(frames.some((frame) => frame.type === "git_workspace_changed")).toBe(true);
		expect(otherFrames.some((frame) => frame.type === "git_workspace_changed")).toBe(true);
	} finally {
		releaseGitWorkspaceSubscriptions(ws);
		releaseGitWorkspaceSubscriptions(other);
	}
	const before = rpcGenerations.length;
	await Bun.sleep(20);
	expect(rpcGenerations).toHaveLength(before);
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
	expect(stops).toBe(1);
	expect(frames).toHaveLength(0);
});
