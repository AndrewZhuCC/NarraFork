/**
 * Per-credential adaptive concurrency and request pacing for Kiro.
 *
 * This is a local, single-process limiter. It keeps NarraFork from sending
 * bursts through the same Kiro credential, shrinks the window on 429, and
 * slowly grows it after sustained success.
 */

import type {
	AccountTier,
	ConcurrencyPlanConfig,
	ConcurrencySettings,
	RequestIntervalSettings,
} from "./types";

const INITIAL_BACKOFF_MS = 1000;
const MAX_BACKOFF_MS = 30_000;
const SLOT_WAIT_TIMEOUT_MS = 300_000;
const MAX_QUEUE_LENGTH = 20;
const RPM_WINDOW_MS = 60_000;
const THROTTLE_WINDOW_MS = 60_000;

const DEFAULT_PLAN_CONFIG: ConcurrencyPlanConfig = { initialMax: 3, ceiling: 8, floor: 1 };
const DEFAULT_CONCURRENCY_CONFIG: ConcurrencySettings = { default: { ...DEFAULT_PLAN_CONFIG } };
const DEFAULT_INTERVAL_CONFIG: RequestIntervalSettings = { free: 0, enterprise: 0, paid: 0 };

let concurrencyConfig: ConcurrencySettings = { ...DEFAULT_CONCURRENCY_CONFIG };
let intervalConfig: RequestIntervalSettings = { ...DEFAULT_INTERVAL_CONFIG };

/** Map credentialId → subscriptionTitle for plan resolution. */
const credentialPlanMap = new Map<string, string | undefined>();

type PlanKey = "free" | "pro" | "pro_plus" | "power";

function classifyPlan(subscriptionTitle?: string | null): PlanKey | undefined {
	if (!subscriptionTitle) return "free";
	const lower = subscriptionTitle.toLowerCase();
	if (lower.includes("power")) return "power";
	if (lower.includes("pro+") || lower.includes("pro plus")) return "pro_plus";
	if (lower.includes("pro")) return "pro";
	if (lower.includes("free")) return "free";
	return undefined;
}

function resolveConfig(subscriptionTitle?: string | null): ConcurrencyPlanConfig {
	const plan = classifyPlan(subscriptionTitle);
	if (plan && concurrencyConfig[plan]) return concurrencyConfig[plan];
	return concurrencyConfig.default;
}

function resolveConfigForCredential(credentialId: string): ConcurrencyPlanConfig {
	return resolveConfig(credentialPlanMap.get(credentialId));
}

export interface CredentialConcurrencyState {
	maxConcurrency: number;
	activeConcurrency: number;
	queueDepth: number;
	lastThrottleTs: number;
	consecutiveSuccess: number;
	backoffMs: number;
	lastRequestTs: number;
	/** Requests for this credential cannot leave the queue before this timestamp. */
	blockedUntil: number;
}

export type QueueProgressCallback = (position: number, queueDepth: number) => void;

interface QueueEntry {
	resolve: () => void;
	reject: (err: Error) => void;
	timer: ReturnType<typeof setTimeout>;
	onProgress?: QueueProgressCallback;
}

interface InternalState {
	maxConcurrency: number;
	activeConcurrency: number;
	lastThrottleTs: number;
	consecutiveSuccess: number;
	backoffMs: number;
	queue: QueueEntry[];
	lastRequestTs: number;
	blockedUntil: number;
	tier: AccountTier;
	intervalTimer: ReturnType<typeof setTimeout> | null;
}

const states = new Map<string, InternalState>();
const rpmWindows = new Map<string, number[]>();
const throttleTotalCounts = new Map<string, number>();
const throttleWindows = new Map<string, number[]>();

function getOrCreate(credentialId: string, subscriptionTitle?: string | null): InternalState {
	if (subscriptionTitle !== undefined) {
		credentialPlanMap.set(credentialId, subscriptionTitle ?? undefined);
	}
	let state = states.get(credentialId);
	if (!state) {
		const cfg = resolveConfig(subscriptionTitle ?? credentialPlanMap.get(credentialId));
		state = {
			maxConcurrency: cfg.initialMax,
			activeConcurrency: 0,
			lastThrottleTs: 0,
			consecutiveSuccess: 0,
			backoffMs: INITIAL_BACKOFF_MS,
			queue: [],
			lastRequestTs: 0,
			blockedUntil: 0,
			tier: "free",
			intervalTimer: null,
		};
		states.set(credentialId, state);
	}
	return state;
}

export function updateLimits(newConfig?: ConcurrencySettings): void {
	concurrencyConfig = newConfig ?? { ...DEFAULT_CONCURRENCY_CONFIG };
	if (!concurrencyConfig.default) concurrencyConfig.default = { ...DEFAULT_PLAN_CONFIG };
	for (const [id, state] of states) {
		const cfg = resolveConfigForCredential(id);
		state.maxConcurrency = Math.max(cfg.floor, Math.min(state.maxConcurrency, cfg.ceiling));
	}
}

export function updateIntervalConfig(newConfig?: RequestIntervalSettings): void {
	intervalConfig = newConfig ?? { ...DEFAULT_INTERVAL_CONFIG };
}

function getIntervalWaitMs(state: InternalState): number {
	const now = Date.now();
	const blockedWait = Math.max(0, state.blockedUntil - now);
	const minInterval = intervalConfig[state.tier] ?? 0;
	if (minInterval <= 0 || state.lastRequestTs <= 0) return blockedWait;
	const elapsed = now - state.lastRequestTs;
	const intervalWait = elapsed >= minInterval ? 0 : minInterval - elapsed;
	return Math.max(blockedWait, intervalWait);
}

function tryImmediateAcquire(state: InternalState): boolean {
	if (
		state.activeConcurrency < Math.floor(state.maxConcurrency) &&
		getIntervalWaitMs(state) === 0
	) {
		state.activeConcurrency++;
		state.lastRequestTs = Date.now();
		return true;
	}
	return false;
}

export function acquireSlot(
	credentialId: string,
	tier?: AccountTier,
	subscriptionTitle?: string | null,
): Promise<void> {
	const state = getOrCreate(credentialId, subscriptionTitle);
	if (tier !== undefined) state.tier = tier;
	if (tryImmediateAcquire(state)) return Promise.resolve();
	if (state.queue.length >= MAX_QUEUE_LENGTH) {
		return Promise.reject(new Error("Credential concurrency queue full"));
	}
	return enqueueWaiter(state, false);
}

export function acquireSlotWithProgress(
	credentialId: string,
	onProgress?: QueueProgressCallback,
	tier?: AccountTier,
	subscriptionTitle?: string | null,
): Promise<void> {
	const state = getOrCreate(credentialId, subscriptionTitle);
	if (tier !== undefined) state.tier = tier;
	if (tryImmediateAcquire(state)) return Promise.resolve();
	if (state.queue.length >= MAX_QUEUE_LENGTH) {
		return Promise.reject(new Error("Credential concurrency queue full"));
	}
	return enqueueWaiter(state, false, onProgress);
}

export function acquireSlotPriority(
	credentialId: string,
	tier?: AccountTier,
	subscriptionTitle?: string | null,
): Promise<void> {
	const state = getOrCreate(credentialId, subscriptionTitle);
	if (tier !== undefined) state.tier = tier;
	if (tryImmediateAcquire(state)) return Promise.resolve();
	if (state.queue.length >= MAX_QUEUE_LENGTH) {
		return Promise.reject(new Error("Credential concurrency queue full"));
	}
	return enqueueWaiter(state, true);
}

function enqueueWaiter(
	state: InternalState,
	priority: boolean,
	onProgress?: QueueProgressCallback,
): Promise<void> {
	return new Promise<void>((resolve, reject) => {
		const timer = setTimeout(() => {
			const idx = state.queue.findIndex((entry) => entry.resolve === resolve);
			if (idx !== -1) state.queue.splice(idx, 1);
			reject(new Error("Slot wait timeout"));
		}, SLOT_WAIT_TIMEOUT_MS);
		const entry: QueueEntry = { resolve, reject, timer, onProgress };
		if (priority) state.queue.unshift(entry);
		else state.queue.push(entry);
		onProgress?.(state.queue.indexOf(entry) + 1, state.queue.length);
		scheduleIntervalDequeue(state);
	});
}

export function releaseSlot(credentialId: string): void {
	const state = states.get(credentialId);
	if (!state) return;
	state.activeConcurrency = Math.max(0, state.activeConcurrency - 1);
	tryDequeue(state);
	scheduleIntervalDequeue(state);
}

export function onThrottle(credentialId: string): void {
	const state = getOrCreate(credentialId);
	const now = Date.now();
	const cfg = resolveConfigForCredential(credentialId);
	state.maxConcurrency = Math.max(cfg.floor, state.maxConcurrency - 1);
	state.consecutiveSuccess = 0;
	if (now - state.lastThrottleTs < 5000) {
		state.backoffMs = Math.min(state.backoffMs * 2, MAX_BACKOFF_MS);
	} else {
		state.backoffMs = INITIAL_BACKOFF_MS;
	}
	state.lastThrottleTs = now;
	state.blockedUntil = Math.max(state.blockedUntil, now + state.backoffMs);
}

export function onSuccess(credentialId: string): void {
	const state = getOrCreate(credentialId);
	const cfg = resolveConfigForCredential(credentialId);
	state.consecutiveSuccess++;
	state.backoffMs = Math.max(500, state.backoffMs * 0.8);
	const threshold = Math.floor(state.maxConcurrency) * 3;
	if (threshold > 0 && state.consecutiveSuccess >= threshold) {
		state.maxConcurrency = Math.min(state.maxConcurrency + 1, cfg.ceiling);
		state.consecutiveSuccess = 0;
	}
}

export function getBackoffMs(credentialId: string): number {
	return states.get(credentialId)?.backoffMs ?? INITIAL_BACKOFF_MS;
}

export function touchLastRequest(credentialId: string): void {
	const state = states.get(credentialId);
	if (state) state.lastRequestTs = Date.now();
}

export function getHeadroom(credentialId: string): number {
	const state = states.get(credentialId);
	if (!state) return resolveConfigForCredential(credentialId).initialMax;
	return Math.max(0, Math.floor(state.maxConcurrency) - state.activeConcurrency);
}

export function getState(credentialId: string): CredentialConcurrencyState {
	const state = states.get(credentialId);
	if (!state) {
		const cfg = resolveConfigForCredential(credentialId);
		return {
			maxConcurrency: cfg.initialMax,
			activeConcurrency: 0,
			queueDepth: 0,
			lastThrottleTs: 0,
			consecutiveSuccess: 0,
			backoffMs: INITIAL_BACKOFF_MS,
			lastRequestTs: 0,
			blockedUntil: 0,
		};
	}
	return {
		maxConcurrency: state.maxConcurrency,
		activeConcurrency: state.activeConcurrency,
		queueDepth: state.queue.length,
		lastThrottleTs: state.lastThrottleTs,
		consecutiveSuccess: state.consecutiveSuccess,
		backoffMs: state.backoffMs,
		lastRequestTs: state.lastRequestTs,
		blockedUntil: state.blockedUntil,
	};
}

export function getAllStates(): Record<string, CredentialConcurrencyState> {
	const result: Record<string, CredentialConcurrencyState> = {};
	for (const [id] of states) result[id] = getState(id);
	return result;
}

function tryDequeue(state: InternalState): void {
	let dequeued = false;
	while (state.queue.length > 0 && state.activeConcurrency < Math.floor(state.maxConcurrency)) {
		if (getIntervalWaitMs(state) > 0) break;
		const waiter = state.queue.shift();
		if (!waiter) break;
		clearTimeout(waiter.timer);
		state.activeConcurrency++;
		state.lastRequestTs = Date.now();
		waiter.resolve();
		dequeued = true;
	}
	if (dequeued) notifyQueuePositions(state);
}

function scheduleIntervalDequeue(state: InternalState): void {
	if (state.intervalTimer) return;
	if (state.queue.length === 0) return;
	if (state.activeConcurrency >= Math.floor(state.maxConcurrency)) return;
	const waitMs = getIntervalWaitMs(state);
	if (waitMs <= 0) return;
	state.intervalTimer = setTimeout(() => {
		state.intervalTimer = null;
		tryDequeue(state);
		scheduleIntervalDequeue(state);
	}, waitMs);
}

function notifyQueuePositions(state: InternalState): void {
	const depth = state.queue.length;
	for (let i = 0; i < depth; i++) {
		state.queue[i].onProgress?.(i + 1, depth);
	}
}

export function recordRequest(credentialId: string): void {
	const now = Date.now();
	const timestamps = rpmWindows.get(credentialId) ?? [];
	timestamps.push(now);
	pruneWindow(timestamps, now - RPM_WINDOW_MS);
	rpmWindows.set(credentialId, timestamps);
}

export function getWindowRpm(credentialId: string): number {
	const timestamps = rpmWindows.get(credentialId);
	if (!timestamps) return 0;
	pruneWindow(timestamps, Date.now() - RPM_WINDOW_MS);
	return timestamps.length;
}

export function getRpmCounts(): Record<string, number> {
	const result: Record<string, number> = {};
	const cutoff = Date.now() - RPM_WINDOW_MS;
	for (const [id, timestamps] of rpmWindows) {
		pruneWindow(timestamps, cutoff);
		if (timestamps.length > 0) result[id] = timestamps.length;
	}
	return result;
}

export function recordThrottle(credentialId: string): void {
	throttleTotalCounts.set(credentialId, (throttleTotalCounts.get(credentialId) ?? 0) + 1);
	const now = Date.now();
	const timestamps = throttleWindows.get(credentialId) ?? [];
	timestamps.push(now);
	pruneWindow(timestamps, now - THROTTLE_WINDOW_MS);
	throttleWindows.set(credentialId, timestamps);
}

export function getThrottleCounts(): Record<string, { recent: number; total: number }> {
	const result: Record<string, { recent: number; total: number }> = {};
	const cutoff = Date.now() - THROTTLE_WINDOW_MS;
	const ids = new Set([...throttleTotalCounts.keys(), ...throttleWindows.keys()]);
	for (const id of ids) {
		const timestamps = throttleWindows.get(id) ?? [];
		pruneWindow(timestamps, cutoff);
		const total = throttleTotalCounts.get(id) ?? 0;
		if (total > 0 || timestamps.length > 0) {
			result[id] = { recent: timestamps.length, total };
		}
	}
	return result;
}

function pruneWindow(timestamps: number[], cutoff: number): void {
	while (timestamps.length > 0 && timestamps[0] < cutoff) timestamps.shift();
}

/** Test helper. */
export function __resetKiroConcurrencyForTests(): void {
	for (const state of states.values()) {
		if (state.intervalTimer) clearTimeout(state.intervalTimer);
		for (const waiter of state.queue) clearTimeout(waiter.timer);
	}
	states.clear();
	rpmWindows.clear();
	throttleTotalCounts.clear();
	throttleWindows.clear();
	credentialPlanMap.clear();
	concurrencyConfig = { default: { ...DEFAULT_PLAN_CONFIG } };
	intervalConfig = { ...DEFAULT_INTERVAL_CONFIG };
}
