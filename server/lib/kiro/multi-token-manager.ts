// Multi-credential Token Manager — reverse-engineered from kiro CLI chat
// Supports multiple credentials with failover, load balancing, and self-healing

import { createHash, randomUUID } from "node:crypto";
import { generateShortId } from "../id";
import { logger } from "../logger";
import {
	effectiveApiRegion,
	loadStats,
	type StatsEntry,
	saveAllCredentials,
	saveStats,
} from "./config";
import { generateMachineId } from "./machine-id";
import { refreshCredentials } from "./token-manager";
import {
	type CallContext,
	type CredentialEntry,
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

/**
 * Classify a credential's account tier for selection ordering.
 * Returns 0 = free, 1 = enterprise, 2 = paid.
 * Lower number = higher selection priority (consume free quota first).
 */
function tierOrder(creds: KiroCredentials): number {
	if (creds.authMethod === "idc" || creds.profileArn || creds.startUrl) return 1; // enterprise
	const title = creds.subscriptionTitle;
	if (!title) return 0; // free (unknown defaults to free)
	const lower = title.toLowerCase();
	if (lower.includes("enterprise") || lower.includes("business")) return 1;
	if (lower.includes("free")) return 0;
	return 2; // paid (pro, pro+, power, builder, etc.)
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

	constructor(
		config: KiroConfig,
		credentials: KiroCredentials[],
		credentialsPath: string,
		isMultipleFormat: boolean,
	) {
		this.config = config;
		this.credentialsPath = credentialsPath;
		this.isMultipleFormat = isMultipleFormat;
		this.loadBalancingMode = config.loadBalancingMode ?? "priority";

		let needsPersist = false;

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
			return {
				id: cred.id,
				credentials: cred,
				failureCount: 0,
				disabled: false,
				successCount: 0,
			};
		});

		// Pick initial current: highest priority (lowest number)
		this.currentId =
			this.entries.length > 0
				? this.entries.reduce((best, e) =>
						(e.credentials.priority ?? 0) < (best.credentials.priority ?? 0) ? e : best,
					).id
				: "";

		// Persist if we assigned new IDs or machineIds
		if (needsPersist && isMultipleFormat) {
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
		});
	}

	get maxRetries(): number {
		return Math.min(this.entries.length * 3, 9);
	}

	// ==================== Credential Selection ====================

	async acquireContext(model?: string | null): Promise<CallContext> {
		const total = this.entries.length;
		const triedIds = new Set<string>();

		while (triedIds.size < total) {
			const entry = this.selectEntry(model ?? undefined, triedIds);

			if (!entry) {
				// Try self-healing
				if (this.trySelfHeal()) {
					const healed = this.selectEntry(model ?? undefined, triedIds);
					if (healed) {
						const ctx = await this.tryEnsureToken(healed);
						if (ctx) return ctx;
						triedIds.add(healed.id);
					}
				}
				break;
			}

			const ctx = await this.tryEnsureToken(entry);
			if (ctx) return ctx;

			// Token refresh failed — skip to next, don't count as API failure
			triedIds.add(entry.id);
		}

		throw new KiroAuthError(
			`All credentials exhausted (available: ${this.availableCount}/${total})`,
		);
	}

	private selectEntry(model?: string, excludeIds?: Set<string>): CredentialEntry | null {
		const available = this.entries.filter((e) => {
			if (e.disabled) return false;
			if (excludeIds?.has(e.id)) return false;
			// If the credential has a known model list, skip it when the requested model isn't in it
			if (model && e.credentials.availableModels?.length) {
				if (!e.credentials.availableModels.includes(model)) return false;
			}
			return true;
		});

		if (available.length === 0) return null;

		// Compare two entries: tier first, then mode-specific tiebreaker.
		// Tier order: free (0) → enterprise (1) → paid (2).
		if (this.loadBalancingMode === "balanced") {
			return available.reduce((best, e) => {
				const tBest = tierOrder(best.credentials);
				const tE = tierOrder(e.credentials);
				if (tE < tBest) return e;
				if (tE > tBest) return best;
				// Same tier: least-used, then priority
				if (e.successCount < best.successCount) return e;
				if (
					e.successCount === best.successCount &&
					(e.credentials.priority ?? 0) < (best.credentials.priority ?? 0)
				)
					return e;
				return best;
			});
		}

		// Priority mode: tier first, then lowest priority number
		return available.reduce((best, e) => {
			const tBest = tierOrder(best.credentials);
			const tE = tierOrder(e.credentials);
			if (tE < tBest) return e;
			if (tE > tBest) return best;
			return (e.credentials.priority ?? 0) < (best.credentials.priority ?? 0) ? e : best;
		});
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
		} catch {
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

	reportFailure(id: string): boolean {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) return this.entries.some((e) => !e.disabled);

		entry.failureCount++;
		entry.lastUsedAt = new Date().toISOString();

		if (entry.failureCount >= MAX_FAILURES_PER_CREDENTIAL) {
			entry.disabled = true;
			entry.disabledReason = "too_many_failures";
		}

		this.saveStatsDebounced();
		return this.entries.some((e) => !e.disabled);
	}

	reportQuotaExhausted(id: string): boolean {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) return this.entries.some((e) => !e.disabled);

		if (!entry.disabled) {
			entry.disabled = true;
			entry.disabledReason = "quota_exhausted";
			entry.failureCount = MAX_FAILURES_PER_CREDENTIAL;
			entry.lastUsedAt = new Date().toISOString();
		}

		this.saveStatsDebounced();
		return this.entries.some((e) => !e.disabled);
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
			})),
			currentId: this.currentId,
			loadBalancingMode: this.loadBalancingMode,
			total: this.entries.length,
			available,
		};
	}

	setLoadBalancingMode(mode: LoadBalancingMode): void {
		this.loadBalancingMode = mode;
	}

	setDisabled(id: string, disabled: boolean): void {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) throw new Error(`Credential not found: ${id}`);
		entry.disabled = disabled;
		if (!disabled) {
			entry.failureCount = 0;
			entry.disabledReason = undefined;
		} else {
			entry.disabledReason = "manual";
		}
	}

	setPriority(id: string, priority: number): void {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) throw new Error(`Credential not found: ${id}`);
		entry.credentials.priority = priority;
		if (this.isMultipleFormat) {
			try {
				this.persistCredentials();
			} catch {}
		}
	}

	resetAndEnable(id: string): void {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) throw new Error(`Credential not found: ${id}`);
		entry.failureCount = 0;
		entry.disabled = false;
		entry.disabledReason = undefined;
	}

	removeCredential(id: string): void {
		const idx = this.entries.findIndex((e) => e.id === id);
		if (idx === -1) throw new Error(`Credential not found: ${id}`);
		this.entries.splice(idx, 1);
		if (this.currentId === id && this.entries.length > 0) {
			this.currentId = this.entries[0].id;
		}
		if (this.isMultipleFormat) {
			try {
				this.persistCredentials();
			} catch {}
		}
	}

	// ==================== Manual Refresh ====================

	async manualRefresh(id: string): Promise<void> {
		const entry = this.entries.find((e) => e.id === id);
		if (!entry) throw new Error(`Credential not found: ${id}`);
		const refreshed = await this.deduplicatedRefresh(id, entry.credentials);
		entry.credentials = refreshed;
		if (this.isMultipleFormat) {
			try {
				this.persistCredentials();
			} catch {}
		}
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
		if (this.isMultipleFormat) {
			try {
				this.persistCredentials();
			} catch {}
		}
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
			if (cred.refreshToken) {
				const hash = sha256Hex(cred.refreshToken);
				if (existingHashes.has(hash)) {
					duplicates++;
					continue;
				}
				existingHashes.add(hash);
			}

			cred.id = generateShortId();
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
			try {
				this.persistCredentials();
			} catch {}
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
			res = await fetch(url, { method: "POST", headers, body: JSON.stringify(reqBody) });
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
				currentUsage: number;
				currentUsageWithPrecision: number;
				usageLimit: number;
				usageLimitWithPrecision: number;
				nextDateReset?: number;
				bonuses?: Array<{ currentUsage: number; usageLimit: number; status?: string }>;
				freeTrialInfo?: {
					currentUsage: number;
					currentUsageWithPrecision: number;
					usageLimit: number;
					usageLimitWithPrecision: number;
					freeTrialStatus?: string;
				};
			}>;
		};
		try {
			data = await res.json();
		} catch {
			throw new Error("getUsageLimits: failed to parse response JSON");
		}
		logger.debug("[kiro:usage] json ok", { breakdowns: data.usageBreakdownList?.length ?? 0 });

		// Parse response: primary breakdown + active bonuses + active free trial
		const breakdown = data.usageBreakdownList?.[0];
		let totalUsage = breakdown?.currentUsageWithPrecision ?? 0;
		let totalLimit = breakdown?.usageLimitWithPrecision ?? 0;

		if (breakdown?.freeTrialInfo?.freeTrialStatus === "ACTIVE") {
			totalUsage += breakdown.freeTrialInfo.currentUsageWithPrecision ?? 0;
			totalLimit += breakdown.freeTrialInfo.usageLimitWithPrecision ?? 0;
		}
		for (const bonus of breakdown?.bonuses ?? []) {
			if (bonus.status === "ACTIVE") {
				totalUsage += bonus.currentUsage ?? 0;
				totalLimit += bonus.usageLimit ?? 0;
			}
		}

		const subscriptionTitle = data.subscriptionInfo?.subscriptionTitle;
		const nextResetAt = data.nextDateReset ?? breakdown?.nextDateReset;

		logger.debug("[kiro:usage] parsed", { totalUsage, totalLimit, subscriptionTitle });

		// Update subscriptionTitle on credential if changed
		if (subscriptionTitle && subscriptionTitle !== creds.subscriptionTitle) {
			creds.subscriptionTitle = subscriptionTitle;
			if (this.isMultipleFormat) {
				try {
					this.persistCredentials();
				} catch {}
			}
		}

		const remaining = Math.max(0, totalLimit - totalUsage);
		const usagePercentage =
			totalLimit > 0 ? Math.round((totalUsage / totalLimit) * 10000) / 100 : 0;

		logger.debug("[kiro:usage] done", { remaining });
		return {
			id,
			subscriptionTitle,
			currentUsage: totalUsage,
			usageLimit: totalLimit,
			remaining,
			usagePercentage,
			nextResetAt,
			queriedAt: new Date().toISOString(),
		};
	}

	// ==================== List Available Models ====================

	async listAvailableModels(credentialId?: string): Promise<{
		models: Array<Record<string, unknown>>;
		credentialId: string;
	}> {
		const entry = credentialId
			? this.entries.find((e) => e.id === credentialId)
			: this.entries.find((e) => !e.disabled);
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
				res = await fetch(requestUrl, { method: "POST", headers, body: JSON.stringify(reqBody) });
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

	private persistCredentials(): void {
		if (!this.isMultipleFormat) return;
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
		this.persistCredentials();
		this.lastMeterPersistAt = Date.now();
		this.meterDirty = false;
	}
}
