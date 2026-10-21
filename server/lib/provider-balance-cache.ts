import type { ProviderBalanceAdapter, ProviderBalanceSnapshot } from "@shared/provider-balance";
import { isDeepSeekCustomApiProvider } from "./deepseek-balance-cache";
import { isKimiCustomApiProvider } from "./kimi-usage-cache";
import { logger } from "./logger";
import type { outboundFetch } from "./net/outbound-fetch";
import { isOpencodeCustomApiProvider } from "./opencode-usage-cache";
import {
	buildBalanceRoots,
	probeProviderBalance,
	refreshProviderBalance,
} from "./provider-balance-adapters";
import { createBalanceRequest } from "./provider-balance-transport";
import { type CustomApiProviderConfig, settings } from "./settings";

export const PROVIDER_BALANCE_STALE_MS = 60_000;
export const PROVIDER_BALANCE_MANUAL_THROTTLE_MS = 10_000;
const ROUND_TIMEOUT_MS = 20_000;
const MAX_CONCURRENT = 4;
const MAX_QUEUED = 128;

type Mode = "probe" | "refresh";
interface Task {
	entry: Entry;
	mode: Mode;
	controller: AbortController;
	promise: Promise<ProviderBalanceSnapshot | null>;
	resolve: (value: ProviderBalanceSnapshot | null) => void;
}
interface Entry {
	id: string;
	identity: string;
	config: CustomApiProviderConfig;
	snapshot: ProviderBalanceSnapshot;
	endpoint?: { adapter: ProviderBalanceAdapter; root: string };
	task?: Task;
	nextManualAt: number;
}

export function isProviderBalanceCandidate(config: CustomApiProviderConfig): boolean {
	return !!(
		!config.disabled &&
		config.apiKey &&
		config.baseUrl &&
		!isDeepSeekCustomApiProvider(config) &&
		!isKimiCustomApiProvider(config) &&
		!isOpencodeCustomApiProvider(config)
	);
}

/** Kept server-side only. Public generations are random, not credential hashes. */
function connectionIdentity(config: CustomApiProviderConfig, globalProxy: unknown): string {
	return JSON.stringify([
		config.baseUrl,
		config.apiKey,
		config.protocol,
		config.proxy,
		config.tlsRejectUnauthorized,
		config.userAgentMode,
		config.customUserAgent,
		Object.entries(config.extraHeaders ?? {}).sort(([a], [b]) => a.localeCompare(b)),
		globalProxy,
	]);
}

interface CacheOptions {
	providers: () => CustomApiProviderConfig[];
	globalProxy: () => unknown;
	fetch?: typeof outboundFetch;
	now?: () => number;
	requestTimeoutMs?: number;
	roundTimeoutMs?: number;
	maxQueued?: number;
}

/** Memory-only, bounded background jobs. Read paths never await upstream IO. */
export class ProviderBalanceCache {
	private entries = new Map<string, Entry>();
	private queue: Task[] = [];
	private active = 0;
	private now: () => number;

	constructor(private options: CacheOptions) {
		this.now = options.now ?? Date.now;
	}

	sync(): void {
		const providers = new Map(
			this.options
				.providers()
				.filter(isProviderBalanceCandidate)
				.map((p) => [p.id, p]),
		);
		const globalProxy = this.options.globalProxy();
		for (const [id, entry] of this.entries) {
			const config = providers.get(id);
			if (!config || entry.identity !== connectionIdentity(config, globalProxy)) {
				this.entries.delete(id);
				this.cancel(entry);
			}
		}
		for (const [id, config] of providers) {
			if (this.entries.has(id)) continue;
			let valid = true;
			try {
				buildBalanceRoots(config.baseUrl);
			} catch {
				valid = false;
			}
			this.entries.set(id, {
				id,
				identity: connectionIdentity(config, globalProxy),
				config: structuredClone(config),
				nextManualAt: 0,
				snapshot: {
					generation: crypto.randomUUID(),
					status: valid ? "pending" : "error",
					balance: null,
					fetchedAt: 0,
					attemptedAt: 0,
					error: valid ? null : "invalid_configuration",
				},
			});
		}
	}

	private cancel(entry: Entry): void {
		const task = entry.task;
		if (!task) return;
		task.controller.abort();
		const index = this.queue.indexOf(task);
		if (index !== -1) {
			this.queue.splice(index, 1);
			entry.task = undefined;
			task.resolve(null);
		}
	}

	generations(): Record<string, string> {
		this.sync();
		return Object.fromEntries([...this.entries].map(([id, e]) => [id, e.snapshot.generation]));
	}

	/** Does not schedule network activity; useful immediately after scheduling a mutation. */
	peek(id: string): ProviderBalanceSnapshot | null {
		this.sync();
		const snapshot = this.entries.get(id)?.snapshot;
		return snapshot ? structuredClone(snapshot) : null;
	}

	/** Only pending entries qualify; unsupported and failed discoveries are not polled. */
	discoverPending(): void {
		this.sync();
		this.fillPending();
	}

	private fillPending(): void {
		for (const entry of this.entries.values()) {
			if (this.queue.length >= (this.options.maxQueued ?? MAX_QUEUED)) break;
			if (entry.snapshot.status === "pending" && !entry.task) void this.schedule(entry, "probe");
		}
	}

	read(providerId?: string): Record<string, ProviderBalanceSnapshot> {
		this.sync();
		const selected = [...this.entries].filter(([id]) => !providerId || id === providerId);
		for (const [, entry] of selected) {
			if (entry.snapshot.status === "pending") void this.schedule(entry, "probe");
			else if (
				entry.snapshot.status === "supported" &&
				this.now() - entry.snapshot.attemptedAt >= PROVIDER_BALANCE_STALE_MS
			) {
				void this.schedule(entry, "refresh");
			}
		}
		return Object.fromEntries(selected.map(([id, entry]) => [id, structuredClone(entry.snapshot)]));
	}

	/** Let HTTP callers report cooldown/backpressure instead of claiming a probe started. */
	manualRetryAfterMs(id: string): number {
		this.sync();
		const entry = this.entries.get(id);
		if (!entry || entry.task || entry.snapshot.error === "invalid_configuration") return 0;
		const cooldown = Math.max(0, entry.nextManualAt - this.now());
		return this.queue.length >= (this.options.maxQueued ?? MAX_QUEUED)
			? Math.max(1_000, cooldown)
			: cooldown;
	}

	/** Explicit, throttled admin action; defaults to discovering capability again. */
	request(id: string, mode: Mode = "probe"): Promise<ProviderBalanceSnapshot | null> {
		this.sync();
		const entry = this.entries.get(id);
		if (!entry) return Promise.resolve(null);
		if (entry.task) return entry.task.promise;
		if (entry.snapshot.error === "invalid_configuration" || this.now() < entry.nextManualAt) {
			return Promise.resolve(structuredClone(entry.snapshot));
		}
		return this.schedule(entry, entry.endpoint ? mode : "probe");
	}

	private schedule(entry: Entry, mode: Mode): Promise<ProviderBalanceSnapshot | null> {
		if (entry.task) return entry.task.promise;
		if (this.queue.length >= (this.options.maxQueued ?? MAX_QUEUED)) {
			return Promise.resolve(null);
		}
		let resolve!: Task["resolve"];
		const promise = new Promise<ProviderBalanceSnapshot | null>((done) => {
			resolve = done;
		});
		const task: Task = { entry, mode, promise, resolve, controller: new AbortController() };
		entry.task = task;
		entry.nextManualAt = this.now() + PROVIDER_BALANCE_MANUAL_THROTTLE_MS;
		if (mode === "probe") entry.snapshot = { ...entry.snapshot, status: "pending", error: null };
		this.queue.push(task);
		this.pump();
		return promise;
	}

	private pump(): void {
		while (this.active < MAX_CONCURRENT && this.queue.length) {
			const task = this.queue.shift();
			if (!task) break;
			this.active++;
			void this.execute(task).finally(() => {
				this.active--;
				this.pump();
				this.fillPending();
			});
		}
	}

	private async execute(task: Task): Promise<void> {
		const { entry, mode, controller } = task;
		const startedAt = performance.now();
		const timer = setTimeout(
			() => controller.abort(),
			this.options.roundTimeoutMs ?? ROUND_TIMEOUT_MS,
		);
		const current = () => {
			this.sync();
			// Transport rejects abort-ignoring IO. An optional public metadata timeout
			// may still legitimately return an already validated raw key allowance.
			return this.entries.get(entry.id) === entry;
		};
		try {
			entry.snapshot = {
				...entry.snapshot,
				status: mode === "probe" ? "probing" : "supported",
				attemptedAt: this.now(),
			};
			const request = createBalanceRequest(entry.config, controller.signal, this.options);
			if (mode === "probe") {
				const found = await probeProviderBalance(entry.config.baseUrl, request);
				if (!current()) return;
				entry.endpoint = found ? { adapter: found.adapter, root: found.root } : undefined;
				entry.snapshot = {
					...entry.snapshot,
					status: found ? "supported" : "unsupported",
					adapter: found?.adapter,
					balance: found?.balance ?? null,
					fetchedAt: found ? this.now() : 0,
					error: null,
				};
			} else if (entry.endpoint) {
				const balance = await refreshProviderBalance(
					entry.endpoint.adapter,
					entry.endpoint.root,
					request,
				);
				if (!current()) return;
				entry.snapshot = { ...entry.snapshot, balance, fetchedAt: this.now(), error: null };
			}
		} catch {
			// An aborted round is a failure if this identity still exists. A superseded
			// request is instead discarded, even if its transport ignored cancellation.
			this.sync();
			if (this.entries.get(entry.id) === entry) {
				entry.snapshot = {
					...entry.snapshot,
					status: entry.endpoint && entry.snapshot.balance ? "supported" : "error",
					error: "request_failed",
				};
			}
		} finally {
			clearTimeout(timer);
			if (entry.task === task) entry.task = undefined;
			const elapsed = performance.now() - startedAt;
			if (elapsed >= 2_000) {
				const safeId = /^[\w-]{1,64}$/.test(entry.id) ? entry.id : "custom-provider";
				logger.warn(
					`[provider-balance] slow operation provider=${safeId} mode=${mode} durationMs=${Math.round(elapsed)} status=${entry.snapshot.status}`,
				);
			}
			task.resolve(this.entries.get(entry.id) === entry ? structuredClone(entry.snapshot) : null);
		}
	}
}

export const providerBalanceCache = new ProviderBalanceCache({
	providers: () => settings.customApiProviders ?? [],
	globalProxy: () => settings.proxy,
});

export const getProviderBalanceGenerations = () => providerBalanceCache.generations();
export const syncProviderBalanceCache = () => providerBalanceCache.sync();
export const discoverPendingProviderBalances = () => providerBalanceCache.discoverPending();
