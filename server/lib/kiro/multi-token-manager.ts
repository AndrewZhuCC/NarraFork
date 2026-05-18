// Multi-credential Token Manager — reverse-engineered from kiro CLI chat
// Supports multiple credentials with failover, load balancing, and self-healing

import { createHash, randomUUID } from "node:crypto";
import { generateShortId } from "../id";
import { logger } from "../logger";
import {
	getAllStates,
	getHeadroom,
	getRpmCounts,
	getThrottleCounts,
	getWindowRpm,
	updateIntervalConfig,
	updateLimits,
} from "./concurrency-control";
import {
	effectiveApiRegion,
	loadStats,
	loadUsageCache,
	type StatsEntry,
	saveAllCredentials,
	saveStats,
	saveUsageCache,
} from "./config";
import { generateMachineId } from "./machine-id";
import { pfetch } from "./proxy";
import { refreshCredentials } from "./token-manager";
import {
	type AccountTier,
	type CallContext,
	type CredentialEntry,
	type DisabledReason,
	KiroAuthError,
	type KiroConfig,
	type KiroCredentials,
	type LoadBalancingMode,
	type ManagerSnapshot,
	type UsageCacheEntry,
} from "./types";

const MAX_FAILURES_PER_CREDENTIAL = 3;
const STATS_SAVE_DEBOUNCE_MS = 30_000;
/** Debounce interval for persisting accumulated meter usage to credentials file. */
const METER_PERSIST_DEBOUNCE_MS = 60_000;
const USAGE_CACHE_PERSIST_DEBOUNCE_MS = 60_000;
const SUSPICIOUS_THROTTLE_COOLDOWN_MS = 5 * 60_000;
const RECOVERY_RETRY_MS = 30_000;
const OVERAGE_BONUS_CREDITS = 10_000;
const FREE_TIER_BLOCKED_MODELS = new Set(["auto"]);
const DEFAULT_TIER_PRIORITY: AccountTier[] = ["free", "enterprise", "paid"];

function sha256Hex(input: string): string {
	return createHash("sha256").update(input).digest("hex");
}

function isExpired(creds: KiroCredentials): boolean {
	return isExpiringWithin(creds, 5) ?? true;
}

function isExpiringSoon(creds: KiroCredentials): boolean {
	return isExpiringWithin(creds, 10) ?? false;
}

function isExpiringWithin(creds: KiroCredentials, minutes: number): boolean | null {
	if (!creds.expiresAt) return null;
	try {
		const t = new Date(creds.expiresAt).getTime();
		if (Number.isNaN(t)) return null;
		return t <= Date.now() + minutes * 60_000;
	} catch {
		return null;
	}
}

export function classifyKiroCredentialTier(creds: KiroCredentials): AccountTier {
	if (creds.profileArn || creds.startUrl) return "enterprise";
	const title = creds.subscriptionTitle;
	if (title) {
		const lower = title.toLowerCase();
		if (lower.includes("enterprise") || lower.includes("business")) return "enterprise";
		if (lower.includes("free")) return "free";
		return "paid";
	}
	if (creds.authMethod === "idc" && creds.clientId && creds.clientSecret) return "enterprise";
	return "free";
}

function applyOverageBonus(usageLimit: number, overageStatus?: string | null): number {
	return overageStatus === "ENABLED" ? usageLimit + OVERAGE_BONUS_CREDITS : usageLimit;
}

function parseDisabledAt(creds: KiroCredentials): number {
	if (!creds.disabledAt) return 0;
	const ts = new Date(creds.disabledAt).getTime();
	return Number.isNaN(ts) ? 0 : ts;
}

function shouldPersistDisabledReason(reason?: DisabledReason): boolean {
	return (
		reason === "manual" ||
		reason === "quota_exhausted" ||
		reason === "account_suspended" ||
		reason === "suspicious_throttle"
	);
}

function buildTierOrderMap(priority?: AccountTier[] | null): Record<AccountTier, number> {
	const map: Partial<Record<AccountTier, number>> = {};
	const order = priority?.length ? priority : DEFAULT_TIER_PRIORITY;
	for (let i = 0; i < order.length; i++) map[order[i]] = i;
	for (const tier of DEFAULT_TIER_PRIORITY) {
		if (map[tier] == null) map[tier] = order.length;
	}
	return map as Record<AccountTier, number>;
}

export class MultiTokenManager {
	private config: KiroConfig;
	private entries: CredentialEntry[];
	private currentId: string;
	private credentialsPath: string;
	private isMultipleFormat: boolean;
	private loadBalancingMode: LoadBalancingMode;
	private lastStatsSaveAt = 0;
	private statsDirty = false;
	/** Per-credential refresh promise dedup */
	private refreshPromises = new Map<string, Promise<KiroCredentials>>();
	private lastMeterPersistAt = 0;
	private meterDirty = false;
	private lastUsageCachePersistAt = 0;
	private usageCacheDirty = false;
	private throttleRecoveryTimers = new Map<string, ReturnType<typeof setTimeout>>();
	private usageCache: Record<string, UsageCacheEntry> = {};

	constructor(
		config: KiroConfig,
		credentials: KiroCredentials[],
		credentialsPath: string,
		isMultipleFormat: boolean,
	) {
		this.config = config;
		this.credentialsPath = credentialsPath;
		// MultiTokenManager owns credentials after loading; persist as array to keep
		// generated IDs, refreshes, metering, and disabled state stable across restarts.
		this.isMultipleFormat = true;
		this.loadBalancingMode = config.loadBalancingMode ?? "priority";
		this.usageCache = loadUsageCache(credentialsPath);
		updateLimits(config.concurrency);
		updateIntervalConfig(config.requestInterval);

		let needsPersist = !isMultipleFormat;

		// Migrate legacy numeric IDs to short strings
		for (const c of credentials) {
			if (c.id != null && /^\d+$/.test(String(c.id))) {
				c.id = generateShortId();
				needsPersist = true;
			}
		}

		// SHA-256 dedup based on refreshToken
		const seen = new Set<string>();
		const deduped: KiroCredentials[] = [];
		for (const c of credentials) {
			if (c.refreshToken) {
				const hash = sha256Hex(c.refreshToken);
				if (seen.has(hash)) continue;
				seen.add(hash);
			}
			deduped.push(c);
		}

		this.entries = deduped.map((cred) => {
			if (!cred.id) {
				cred.id = generateShortId();
				needsPersist = true;
			}
			if (!cred.machineId) {
				const mid = generateMachineId(cred, config);
				if (mid) {
					cred.machineId = mid;
					needsPersist = true;
				}
			}

			const disabledState = this.restoreDisabledState(cred);
			if (disabledState.needsPersist) needsPersist = true;
			return {
				id: cred.id,
				credentials: cred,
				failureCount: disabledState.disabled ? MAX_FAILURES_PER_CREDENTIAL : 0,
				disabled: disabledState.disabled,
				disabledReason: disabledState.disabledReason,
				successCount: 0,
			};
		});

		// Pick initial current: highest tier priority, then lowest credential priority.
		this.currentId =
			this.entries.length > 0
				? this.entries.reduce((best, entry) => this.comparePriority(entry, best)).id
				: "";

		this.recoverSuspiciousThrottlesOnStartup();

		// Persist if we assigned new IDs/machineIds or cleaned stale disabled state.
		if (needsPersist) {
			try {
				this.persistCredentials();
			} catch {}
		}

		// Restore stats from disk
		this.restoreStats();

		// Flush stats and meter usage on process exit
		process.on("beforeExit", () => {
			if (this.statsDirty) this.flushStats();
			if (this.meterDirty) this.persistCredentials();
			if (this.usageCacheDirty) this.flushUsageCache();
		});
	}

	get maxRetries(): number {
		return Math.max(1, Math.min(this.entries.length * 3, 9));
	}

	// ==================== Credential Selection ====================

	async acquireContext(model?: string | null, excludeIds?: Set<string>): Promise<CallContext> {
		const total = this.entries.length;
		const triedIds = new Set<string>(excludeIds);

		while (triedIds.size < total) {
			const entry = this.selectEntry(model ?? undefined, triedIds);

			if (!entry) {
				// Try self-healing runtime-only too_many_failures disables.
				if (this.trySelfHeal()) continue;
				break;
			}

			const ctx = await this.tryEnsureToken(entry);
			if (ctx) return ctx;

			// Token refresh failed — skip to next, don't count as API failure.
			triedIds.add(entry.id);
		}

		throw new KiroAuthError(
			`All credentials exhausted (available: ${this.availableCount}/${total})`,
		);
	}

	private selectEntry(model?: string, excludeIds?: Set<string>): CredentialEntry | null {
		const available = this.entries.filter((entry) => {
			if (entry.disabled) return false;
			if (excludeIds?.has(entry.id)) return false;
			return true;
		});
		let candidates = available.filter((entry) => this.supportsModel(entry, model));

		// Fallback: if no paid/enterprise credential advertises the model, allow free-tier
		// credentials to try unknown/non-auto models. This mirrors native-kiro-proxy's
		// pragmatic fallback for stale model caches.
		if (candidates.length === 0 && model && !FREE_TIER_BLOCKED_MODELS.has(model)) {
			candidates = available.filter(
				(entry) => classifyKiroCredentialTier(entry.credentials) === "free",
			);
		}
		if (candidates.length === 0) return null;

		if (this.config.ignoreTierPriority) return this.pickWithinGroup(candidates);

		const tierOrderMap = buildTierOrderMap(this.config.tierPriority);
		const ordered = [...candidates].sort((a, b) => {
			const tierDiff =
				tierOrderMap[classifyKiroCredentialTier(a.credentials)] -
				tierOrderMap[classifyKiroCredentialTier(b.credentials)];
			if (tierDiff !== 0) return tierDiff;
			return this.scoreWithinGroup(a, b);
		});
		return ordered[0] ?? null;
	}

	private supportsModel(entry: CredentialEntry, model?: string): boolean {
		if (!model) return true;
		if (
			classifyKiroCredentialTier(entry.credentials) === "free" &&
			FREE_TIER_BLOCKED_MODELS.has(model)
		) {
			return false;
		}
		const models = entry.credentials.availableModels;
		return !models?.length || models.includes(model);
	}

	private pickWithinGroup(entries: CredentialEntry[]): CredentialEntry {
		return entries.reduce((best, entry) => (this.scoreWithinGroup(entry, best) < 0 ? entry : best));
	}

	private scoreWithinGroup(a: CredentialEntry, b: CredentialEntry): number {
		if (this.loadBalancingMode === "priority") {
			return (a.credentials.priority ?? 0) - (b.credentials.priority ?? 0);
		}

		const aHeadroom = getHeadroom(a.id) > 0 ? 1 : 0;
		const bHeadroom = getHeadroom(b.id) > 0 ? 1 : 0;
		if (aHeadroom !== bHeadroom) return bHeadroom - aHeadroom;

		const rpmDiff = getWindowRpm(a.id) - getWindowRpm(b.id);
		if (rpmDiff !== 0) return rpmDiff;

		const usageDiff = this.currentUsage(a) - this.currentUsage(b);
		if (usageDiff !== 0) return usageDiff;

		return (a.credentials.priority ?? 0) - (b.credentials.priority ?? 0);
	}

	private comparePriority(a: CredentialEntry, b: CredentialEntry): CredentialEntry {
		const tierOrderMap = buildTierOrderMap(this.config.tierPriority);
		const tierDiff =
			tierOrderMap[classifyKiroCredentialTier(a.credentials)] -
			tierOrderMap[classifyKiroCredentialTier(b.credentials)];
		if (tierDiff < 0) return a;
		if (tierDiff > 0) return b;
		return (a.credentials.priority ?? 0) < (b.credentials.priority ?? 0) ? a : b;
	}

	private currentUsage(entry: CredentialEntry): number {
		const meterUsage = entry.credentials.meterUsage ?? 0;
		const cached = this.usageCache[entry.id];
		if (!cached) return meterUsage;
		if (this.isUsageCacheExpired(cached)) return meterUsage;
		return Math.max(cached.currentUsage, meterUsage);
	}

	private isUsageCacheExpired(entry: UsageCacheEntry): boolean {
		if (!entry.nextResetAt) return false;
		const resetAt =
			entry.nextResetAt < 10_000_000_000 ? entry.nextResetAt * 1000 : entry.nextResetAt;
		return resetAt <= Date.now();
	}

	private async tryEnsureToken(entry: CredentialEntry): Promise<CallContext | null> {
		const creds = entry.credentials;
		const needsRefresh = isExpired(creds) || isExpiringSoon(creds);

		if (!needsRefresh) {
			if (!creds.accessToken) return null;
			this.currentId = entry.id;
			return { id: entry.id, credentials: creds, token: creds.accessToken };
		}

		try {
			const refreshed = await this.deduplicatedRefresh(entry.id, creds);
			entry.credentials = refreshed;

			if (isExpired(refreshed)) return null;
			if (!refreshed.accessToken) return null;

			// Persist refreshed credentials
			if (this.isMultipleFormat) {
				try {
					this.persistCredentials();
				} catch {}
			}

			this.currentId = entry.id;
			return {
				id: entry.id,
				credentials: refreshed,
				token: refreshed.accessToken,
			};
		} catch (err) {
			logger.warn("[kiro] token refresh failed", { credentialId: entry.id, error: String(err) });
			return null;
		}
	}

	/** Promise-based dedup: concurrent callers for the same credential share one refresh. */
	private deduplicatedRefresh(id: string, creds: KiroCredentials): Promise<KiroCredentials> {
		const existing = this.refreshPromises.get(id);
		if (existing) return existing;

		const promise = refreshCredentials(creds, this.config).finally(() => {
			this.refreshPromises.delete(id);
		});
		this.refreshPromises.set(id, promise);
		return promise;
	}

	// ==================== Reporting ====================

	reportSuccess(id: string): void {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) return;
		entry.failureCount = 0;
		entry.successCount++;
		entry.lastUsedAt = new Date().toISOString();
		this.saveStatsDebounced();
	}

	reportFailure(id: string, reason?: DisabledReason): boolean {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) return this.entries.some((e) => !e.disabled);

		if (reason === "quota_exhausted") return this.reportQuotaExhausted(id);
		if (reason === "account_suspended") {
			this.disableEntry(entry, "account_suspended", true);
			return this.entries.some((e) => !e.disabled);
		}
		if (reason === "suspicious_throttle") return this.reportSuspiciousThrottle(id);

		entry.failureCount++;
		entry.lastUsedAt = new Date().toISOString();

		if (entry.failureCount >= MAX_FAILURES_PER_CREDENTIAL) {
			this.disableEntry(entry, "too_many_failures", false);
		}

		this.saveStatsDebounced();
		return this.entries.some((e) => !e.disabled);
	}

	reportQuotaExhausted(id: string): boolean {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) return this.entries.some((e) => !e.disabled);
		this.disableEntry(entry, "quota_exhausted", true);
		return this.entries.some((e) => !e.disabled);
	}

	reportSuspiciousThrottle(id: string): boolean {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) return this.entries.some((e) => !e.disabled);
		logger.warn("[kiro] credential suspicious-throttled, cooling down", { credentialId: id });
		this.disableEntry(entry, "suspicious_throttle", true);
		this.scheduleThrottleRecovery(id, SUSPICIOUS_THROTTLE_COOLDOWN_MS);
		return this.entries.some((e) => !e.disabled);
	}

	private disableEntry(entry: CredentialEntry, reason: DisabledReason, persist: boolean): void {
		entry.disabled = true;
		entry.disabledReason = reason;
		entry.failureCount = MAX_FAILURES_PER_CREDENTIAL;
		entry.lastUsedAt = new Date().toISOString();
		if (persist && shouldPersistDisabledReason(reason)) {
			entry.credentials.disabled = true;
			entry.credentials.disabledReason = reason;
			entry.credentials.disabledAt = new Date().toISOString();
			this.persistCredentials();
		}
		this.saveStatsDebounced();
	}

	// ==================== Self-healing ====================

	private trySelfHeal(): boolean {
		const hasTooManyFailures = this.entries.some(
			(e) => e.disabled && e.disabledReason === "too_many_failures",
		);
		if (!hasTooManyFailures) return false;

		for (const e of this.entries) {
			if (e.disabledReason === "too_many_failures") {
				e.disabled = false;
				e.disabledReason = undefined;
				e.failureCount = 0;
			}
		}
		return true;
	}

	private restoreDisabledState(creds: KiroCredentials): {
		disabled: boolean;
		disabledReason?: DisabledReason;
		needsPersist: boolean;
	} {
		if (!creds.disabled || !creds.disabledReason) {
			return { disabled: false, needsPersist: false };
		}
		if (creds.disabledReason === "suspicious_throttle") {
			const disabledAt = parseDisabledAt(creds);
			const elapsed = disabledAt > 0 ? Date.now() - disabledAt : SUSPICIOUS_THROTTLE_COOLDOWN_MS;
			if (elapsed < SUSPICIOUS_THROTTLE_COOLDOWN_MS) {
				return { disabled: true, disabledReason: "suspicious_throttle", needsPersist: false };
			}
			creds.disabled = undefined;
			creds.disabledReason = undefined;
			creds.disabledAt = undefined;
			return { disabled: false, needsPersist: true };
		}
		if (shouldPersistDisabledReason(creds.disabledReason)) {
			return { disabled: true, disabledReason: creds.disabledReason, needsPersist: false };
		}
		creds.disabled = undefined;
		creds.disabledReason = undefined;
		creds.disabledAt = undefined;
		return { disabled: false, needsPersist: true };
	}

	private recoverSuspiciousThrottlesOnStartup(): void {
		for (const entry of this.entries) {
			if (entry.disabledReason !== "suspicious_throttle") continue;
			const disabledAt = parseDisabledAt(entry.credentials);
			const elapsed = disabledAt > 0 ? Date.now() - disabledAt : 0;
			const remaining = Math.max(0, SUSPICIOUS_THROTTLE_COOLDOWN_MS - elapsed);
			this.scheduleThrottleRecovery(entry.id, remaining);
		}
	}

	private scheduleThrottleRecovery(id: string, delayMs: number): void {
		if (this.throttleRecoveryTimers.has(id)) return;
		const timer = setTimeout(() => {
			this.throttleRecoveryTimers.delete(id);
			try {
				this.recoverSuspiciousThrottleCredential(id);
			} catch (err) {
				logger.error("[kiro] failed to recover suspicious-throttled credential", {
					credentialId: id,
					error: String(err),
				});
				this.scheduleThrottleRecovery(id, RECOVERY_RETRY_MS);
			}
		}, delayMs);
		this.throttleRecoveryTimers.set(id, timer);
	}

	private recoverSuspiciousThrottleCredential(id: string): void {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry || entry.disabledReason !== "suspicious_throttle") return;
		entry.disabled = false;
		entry.disabledReason = undefined;
		entry.failureCount = 0;
		entry.credentials.disabled = undefined;
		entry.credentials.disabledReason = undefined;
		entry.credentials.disabledAt = undefined;
		this.persistCredentials();
		logger.info("[kiro] suspicious-throttled credential recovered", { credentialId: id });
	}

	// ==================== Admin API ====================

	snapshot(): ManagerSnapshot {
		const available = this.entries.filter((e) => !e.disabled).length;
		return {
			entries: this.entries.map((e) => ({
				id: e.id,
				priority: e.credentials.priority ?? 0,
				disabled: e.disabled,
				disabledReason: e.disabledReason,
				failureCount: e.failureCount,
				successCount: e.successCount,
				lastUsedAt: e.lastUsedAt,
				email: e.credentials.email,
				displayName: e.credentials.displayName,
				hasProfileArn: !!e.credentials.profileArn,
				hasStartUrl: !!e.credentials.startUrl,
				expiresAt: e.credentials.expiresAt,
				authMethod: e.credentials.authMethod,
				subscriptionTitle: e.credentials.subscriptionTitle,
				region: e.credentials.region,
				meterUsage: e.credentials.meterUsage,
				availableModels: e.credentials.availableModels,
				tier: classifyKiroCredentialTier(e.credentials),
			})),
			currentId: this.currentId,
			loadBalancingMode: this.loadBalancingMode,
			total: this.entries.length,
			available,
			concurrencyStates: getAllStates(),
			rpmCounts: getRpmCounts(),
			throttleCounts: getThrottleCounts(),
		};
	}

	setLoadBalancingMode(mode: LoadBalancingMode): void {
		this.loadBalancingMode = mode;
		this.config.loadBalancingMode = mode;
	}

	setDisabled(id: string, disabled: boolean): void {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) throw new Error(`Credential not found: ${id}`);
		if (!disabled) {
			this.clearRecoveryTimer(id);
			entry.failureCount = 0;
			entry.disabled = false;
			entry.disabledReason = undefined;
			entry.credentials.disabled = undefined;
			entry.credentials.disabledReason = undefined;
			entry.credentials.disabledAt = undefined;
		} else {
			this.disableEntry(entry, "manual", true);
			return;
		}
		this.persistCredentials();
	}

	setPriority(id: string, priority: number): void {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) throw new Error(`Credential not found: ${id}`);
		entry.credentials.priority = priority;
		this.persistCredentials();
	}

	resetAndEnable(id: string): void {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) throw new Error(`Credential not found: ${id}`);
		this.clearRecoveryTimer(id);
		entry.failureCount = 0;
		entry.disabled = false;
		entry.disabledReason = undefined;
		entry.credentials.disabled = undefined;
		entry.credentials.disabledReason = undefined;
		entry.credentials.disabledAt = undefined;
		this.persistCredentials();
	}

	removeCredential(id: string): void {
		const idx = this.entries.findIndex((e) => e.id === id);
		if (idx === -1) throw new Error(`Credential not found: ${id}`);
		this.clearRecoveryTimer(id);
		this.entries.splice(idx, 1);
		if (this.currentId === id && this.entries.length > 0) {
			this.currentId = this.entries[0].id;
		}
		this.persistCredentials();
	}

	private clearRecoveryTimer(id: string): void {
		const timer = this.throttleRecoveryTimers.get(id);
		if (!timer) return;
		clearTimeout(timer);
		this.throttleRecoveryTimers.delete(id);
	}

	// ==================== Manual Refresh ====================

	async manualRefresh(id: string): Promise<void> {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) throw new Error(`Credential not found: ${id}`);
		const refreshed = await this.deduplicatedRefresh(id, entry.credentials);
		entry.credentials = refreshed;
		this.persistCredentials();
	}

	// ==================== Update Credential Fields ====================

	updateCredential(
		id: string,
		fields: { email?: string; displayName?: string; region?: string },
	): void {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) throw new Error(`Credential not found: ${id}`);
		if (fields.email !== undefined) entry.credentials.email = fields.email;
		if (fields.displayName !== undefined) entry.credentials.displayName = fields.displayName;
		if (fields.region !== undefined) entry.credentials.region = fields.region;
		this.persistCredentials();
	}

	// ==================== Import Credentials ====================

	addCredentials(newCreds: KiroCredentials[]): { added: number; duplicates: number } {
		// Build set of existing refreshToken hashes
		const existingHashes = new Set<string>();
		for (const e of this.entries) {
			if (e.credentials.refreshToken) {
				existingHashes.add(sha256Hex(e.credentials.refreshToken));
			}
		}

		let added = 0;
		let duplicates = 0;

		for (const cred of newCreds) {
			if (!cred.refreshToken) {
				duplicates++;
				continue;
			}
			const hash = sha256Hex(cred.refreshToken);
			if (existingHashes.has(hash)) {
				duplicates++;
				continue;
			}
			existingHashes.add(hash);

			cred.id = generateShortId();
			cred.disabled = undefined;
			cred.disabledReason = undefined;
			cred.disabledAt = undefined;
			if (!cred.machineId) {
				const mid = generateMachineId(cred, this.config);
				if (mid) cred.machineId = mid;
			}

			this.entries.push({
				id: cred.id,
				credentials: cred,
				failureCount: 0,
				disabled: false,
				successCount: 0,
			});
			added++;
		}

		if (added > 0) {
			this.isMultipleFormat = true;
			this.persistCredentials();
		}

		return { added, duplicates };
	}

	// ==================== Usage Limits ====================

	async getUsageLimits(id: string): Promise<UsageCacheEntry> {
		logger.debug("[kiro:usage] start", { credentialId: id });
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) throw new Error(`Credential not found: ${id}`);

		logger.debug("[kiro:usage] tryEnsureToken");
		const ctx = await this.tryEnsureToken(entry);
		if (!ctx) throw new KiroAuthError(`Cannot acquire valid token for credential ${id}`);
		logger.debug("[kiro:usage] token ok");

		const creds = entry.credentials;
		const region = effectiveApiRegion(this.config);
		const { kiroVersion } = this.config;
		const host = `q.${region}.amazonaws.com`;
		const url = `https://${host}`;

		const reqBody: Record<string, string> = {
			origin: "AI_EDITOR",
			resourceType: "AGENTIC_REQUEST",
		};
		if (creds.profileArn) {
			reqBody.profileArn = creds.profileArn;
		}

		const headers: Record<string, string> = {
			"Content-Type": "application/x-amz-json-1.0",
			"x-amz-target": "AmazonCodeWhispererService.GetUsageLimits",
			"User-Agent": `AmazonQ-For-CLI/Version/${kiroVersion}`,
			Host: host,
			"amz-sdk-invocation-id": randomUUID(),
			"amz-sdk-request": "attempt=1; max=1",
			Authorization: `Bearer ${ctx.token}`,
		};

		logger.debug("[kiro:usage] fetch POST", { url });
		let res: Response;
		try {
			res = await pfetch(url, { method: "POST", headers, body: JSON.stringify(reqBody) });
		} catch (err) {
			logger.error("[kiro:usage] fetch threw", { error: String(err) });
			throw new Error(
				`getUsageLimits network error: ${err instanceof Error ? err.message : String(err)}`,
			);
		}

		logger.debug("[kiro:usage] response", { status: res.status });
		if (!res.ok) {
			const body = await res.text().catch(() => "");
			logger.warn("[kiro:usage] not ok", { status: res.status, body: body.slice(0, 200) });
			throw new Error(`getUsageLimits failed: ${res.status} ${body}`);
		}

		logger.debug("[kiro:usage] parsing json");
		let data: {
			nextDateReset?: number;
			subscriptionInfo?: { subscriptionTitle?: string };
			usageBreakdownList?: Array<{
				currentUsage?: number;
				currentUsageWithPrecision?: number;
				usageLimit?: number;
				usageLimitWithPrecision?: number;
				nextDateReset?: number;
				overageCap?: number;
				overageRate?: number;
				overageCharges?: number;
				currentOverages?: number;
				bonuses?: Array<{
					currentUsage?: number;
					usageLimit?: number;
					status?: string;
				}>;
				freeTrialInfo?: {
					currentUsage?: number;
					currentUsageWithPrecision?: number;
					usageLimit?: number;
					usageLimitWithPrecision?: number;
					freeTrialStatus?: string;
				};
			}>;
			overageConfiguration?: { overageStatus?: string };
		};
		try {
			data = await res.json();
		} catch {
			throw new Error("getUsageLimits: failed to parse response JSON");
		}
		logger.debug("[kiro:usage] json ok", { breakdowns: data.usageBreakdownList?.length ?? 0 });

		// Parse response: primary breakdown + active bonuses + active free trial
		const breakdown = data.usageBreakdownList?.[0];
		let totalUsage = breakdown?.currentUsageWithPrecision ?? breakdown?.currentUsage ?? 0;
		let totalLimit = breakdown?.usageLimitWithPrecision ?? breakdown?.usageLimit ?? 0;

		if (breakdown?.freeTrialInfo?.freeTrialStatus === "ACTIVE") {
			totalUsage +=
				breakdown.freeTrialInfo.currentUsageWithPrecision ??
				breakdown.freeTrialInfo.currentUsage ??
				0;
			totalLimit +=
				breakdown.freeTrialInfo.usageLimitWithPrecision ?? breakdown.freeTrialInfo.usageLimit ?? 0;
		}
		for (const bonus of breakdown?.bonuses ?? []) {
			if (bonus.status === "ACTIVE") {
				totalUsage += bonus.currentUsage ?? 0;
				totalLimit += bonus.usageLimit ?? 0;
			}
		}

		const subscriptionTitle = data.subscriptionInfo?.subscriptionTitle;
		const nextResetAt = data.nextDateReset ?? breakdown?.nextDateReset;
		const overageStatus = data.overageConfiguration?.overageStatus;
		const effectiveLimit = applyOverageBonus(totalLimit, overageStatus);

		logger.debug("[kiro:usage] parsed", { totalUsage, totalLimit, subscriptionTitle });

		let shouldPersist = false;
		if (subscriptionTitle && subscriptionTitle !== creds.subscriptionTitle) {
			creds.subscriptionTitle = subscriptionTitle;
			shouldPersist = true;
		}
		if (creds.meterUsage !== totalUsage) {
			creds.meterUsage = totalUsage;
			shouldPersist = true;
		}
		if (shouldPersist) this.persistCredentials();

		const remaining = Math.max(0, effectiveLimit - totalUsage);
		const usagePercentage =
			effectiveLimit > 0 ? Math.round((totalUsage / effectiveLimit) * 10000) / 100 : 0;

		const result: UsageCacheEntry = {
			id,
			subscriptionTitle,
			currentUsage: totalUsage,
			usageLimit: effectiveLimit,
			remaining,
			usagePercentage,
			nextResetAt,
			queriedAt: new Date().toISOString(),
			overageStatus,
			overageCap: breakdown?.overageCap,
			overageRate: breakdown?.overageRate,
			overageCharges: breakdown?.overageCharges,
			currentOverages: breakdown?.currentOverages,
		};
		this.usageCache[id] = result;
		this.flushUsageCache();
		logger.debug("[kiro:usage] done", { remaining });
		return result;
	}

	// ==================== List Available Models ====================

	async listAvailableModels(credentialId?: string): Promise<{
		models: Array<Record<string, unknown>>;
		credentialId: string;
	}> {
		const entry = credentialId
			? this.entries.find((e) => e.id === credentialId)
			: this.selectEntry();
		if (!entry)
			throw new Error(
				credentialId ? `Credential not found: ${credentialId}` : "No available credentials",
			);

		const ctx = await this.tryEnsureToken(entry);
		if (!ctx) throw new KiroAuthError(`Cannot acquire valid token for credential ${entry.id}`);

		const creds = entry.credentials;
		const region = effectiveApiRegion(this.config);
		const { kiroVersion } = this.config;
		const host = `q.${region}.amazonaws.com`;
		const url = `https://${host}`;

		const reqBody: Record<string, unknown> = {
			origin: "AI_EDITOR",
			maxResults: 100,
		};
		if (creds.profileArn) reqBody.profileArn = creds.profileArn;

		const allModels: Array<Record<string, unknown>> = [];
		let nextToken: string | undefined;

		logger.debug("[kiro:models] start", { credentialId: entry.id, region });

		do {
			if (nextToken) reqBody.nextToken = nextToken;
			else delete reqBody.nextToken;

			// Build query string (AWS JSON 1.0 dual encoding: query params + JSON body)
			const params = new URLSearchParams();
			params.set("origin", "AI_EDITOR");
			if (reqBody.maxResults != null) params.set("maxResults", String(reqBody.maxResults));
			if (creds.profileArn) params.set("profileArn", creds.profileArn);
			if (nextToken) params.set("nextToken", nextToken);
			const requestUrl = `${url}/?${params.toString()}`;

			const headers: Record<string, string> = {
				"Content-Type": "application/x-amz-json-1.0",
				"x-amz-target": "AmazonCodeWhispererService.ListAvailableModels",
				"User-Agent": `AmazonQ-For-CLI/Version/${kiroVersion}`,
				Host: host,
				"amz-sdk-invocation-id": randomUUID(),
				"amz-sdk-request": "attempt=1; max=1",
				Authorization: `Bearer ${ctx.token}`,
			};

			logger.debug("[kiro:models] fetch POST", { url: requestUrl });
			let res: Response;
			try {
				res = await pfetch(requestUrl, {
					method: "POST",
					headers,
					body: JSON.stringify(reqBody),
				});
			} catch (err) {
				throw new Error(
					`ListAvailableModels network error: ${err instanceof Error ? err.message : String(err)}`,
				);
			}
			logger.debug("[kiro:models] response", { status: res.status });
			if (!res.ok) {
				const body = await res.text().catch(() => "");
				logger.warn("[kiro:models] not ok", { status: res.status, body: body.slice(0, 500) });
				throw new Error(`ListAvailableModels failed: ${res.status} ${body}`);
			}

			let data: Record<string, unknown>;
			try {
				data = await res.json();
			} catch {
				throw new Error("ListAvailableModels: failed to parse response JSON");
			}
			logger.debug("[kiro:models] json ok", {
				models: Array.isArray(data.models) ? data.models.length : typeof data.models,
				keys: Object.keys(data).join(","),
			});
			if (data.models != null && !Array.isArray(data.models)) {
				throw new Error(
					`ListAvailableModels: unexpected response shape: ${JSON.stringify(data).slice(0, 500)}`,
				);
			}
			if (!data.models) {
				// Server returned 200 but no models field — treat as error with response dump
				throw new Error(
					`ListAvailableModels: response missing 'models' field: ${JSON.stringify(data).slice(0, 500)}`,
				);
			}
			if (Array.isArray(data.models)) allModels.push(...data.models);
			nextToken = (data.nextToken as string) || undefined;
		} while (nextToken);

		logger.debug("[kiro:models] done", { total: allModels.length });
		return { models: allModels, credentialId: entry.id };
	}

	/**
	 * Fetch the model list for a single credential and store it on the credential.
	 * Returns the model ID list.
	 */
	async fetchAndStoreModels(credentialId: string): Promise<string[]> {
		const result = await this.listAvailableModels(credentialId);
		const modelIds = result.models
			.map((m) => String(m.model_id ?? m.modelId ?? ""))
			.filter(Boolean);
		const entry = this.entries.find((e) => e.id === credentialId);
		if (entry) {
			entry.credentials.availableModels = modelIds;
			this.persistCredentials();
		}
		return modelIds;
	}

	/**
	 * Fetch model lists for all enabled credentials, store per-credential,
	 * and return a deduplicated union of all models (full objects).
	 */
	async fetchAllCredentialModels(): Promise<Array<Record<string, unknown>>> {
		const seen = new Set<string>();
		const allModels: Array<Record<string, unknown>> = [];

		for (const entry of this.entries) {
			if (entry.disabled) continue;
			try {
				const result = await this.listAvailableModels(entry.id);
				const modelIds: string[] = [];
				for (const m of result.models) {
					const id = String(m.model_id ?? m.modelId ?? "");
					if (!id) continue;
					modelIds.push(id);
					if (!seen.has(id)) {
						seen.add(id);
						allModels.push(m);
					}
				}
				entry.credentials.availableModels = modelIds;
			} catch (err) {
				logger.warn("[kiro:models] failed for credential", {
					credentialId: entry.id,
					error: String(err),
				});
			}
		}

		this.persistCredentials();
		return allModels;
	}

	get availableCount(): number {
		return this.entries.filter((e) => !e.disabled).length;
	}

	get currentConfig(): KiroConfig {
		return this.config;
	}

	// ==================== Stats Persistence ====================

	private restoreStats(): void {
		try {
			const stats = loadStats(this.credentialsPath);
			for (const entry of this.entries) {
				const s = stats[String(entry.id)];
				if (s) {
					entry.successCount = s.successCount ?? 0;
					entry.lastUsedAt = s.lastUsedAt;
				}
			}
			this.lastStatsSaveAt = Date.now();
		} catch {}
	}

	private saveStatsDebounced(): void {
		this.statsDirty = true;
		if (Date.now() - this.lastStatsSaveAt > STATS_SAVE_DEBOUNCE_MS) {
			this.flushStats();
		}
	}

	private flushStats(): void {
		try {
			const stats: Record<string, StatsEntry> = {};
			for (const e of this.entries) {
				stats[String(e.id)] = {
					successCount: e.successCount,
					lastUsedAt: e.lastUsedAt,
				};
			}
			saveStats(this.credentialsPath, stats);
			this.lastStatsSaveAt = Date.now();
			this.statsDirty = false;
		} catch {}
	}

	private saveUsageCacheDebounced(): void {
		this.usageCacheDirty = true;
		if (Date.now() - this.lastUsageCachePersistAt > USAGE_CACHE_PERSIST_DEBOUNCE_MS) {
			this.flushUsageCache();
		}
	}

	private flushUsageCache(): void {
		try {
			saveUsageCache(this.credentialsPath, this.usageCache);
			this.lastUsageCachePersistAt = Date.now();
			this.usageCacheDirty = false;
		} catch {}
	}

	private persistCredentials(): void {
		saveAllCredentials(
			this.credentialsPath,
			this.entries.map((e) => e.credentials),
		);
	}

	// ==================== Meter Usage Accumulation ====================

	/**
	 * Accumulate metering usage for a credential.
	 * The amount is added to `credentials.meterUsage` in memory and
	 * periodically persisted to `credentials.json` (debounced).
	 */
	addMeterUsage(credentialId: string, amount: number): void {
		if (amount <= 0) return;
		const entry = this.entries.find((e) => e.id === credentialId);
		if (!entry) return;
		entry.credentials.meterUsage = (entry.credentials.meterUsage ?? 0) + amount;
		const cached = this.usageCache[credentialId];
		if (cached) {
			cached.currentUsage = Math.max(cached.currentUsage, entry.credentials.meterUsage);
			cached.remaining = Math.max(0, cached.usageLimit - cached.currentUsage);
			cached.usagePercentage =
				cached.usageLimit > 0
					? Math.round((cached.currentUsage / cached.usageLimit) * 10000) / 100
					: 0;
			cached.queriedAt = new Date().toISOString();
			this.saveUsageCacheDebounced();
		}
		this.meterDirty = true;

		if (Date.now() - this.lastMeterPersistAt > METER_PERSIST_DEBOUNCE_MS) {
			this.persistCredentials();
			this.lastMeterPersistAt = Date.now();
			this.meterDirty = false;
		}
	}

	/** Set meterUsage to an absolute value (e.g. after querying the real API). */
	setMeterUsage(credentialId: string, value: number): void {
		const entry = this.entries.find((e) => e.id === credentialId);
		if (!entry) return;
		entry.credentials.meterUsage = value;
		const cached = this.usageCache[credentialId];
		if (cached) {
			cached.currentUsage = value;
			cached.remaining = Math.max(0, cached.usageLimit - value);
			cached.usagePercentage =
				cached.usageLimit > 0 ? Math.round((value / cached.usageLimit) * 10000) / 100 : 0;
			cached.queriedAt = new Date().toISOString();
			this.flushUsageCache();
		}
		this.persistCredentials();
		this.lastMeterPersistAt = Date.now();
		this.meterDirty = false;
	}
}
