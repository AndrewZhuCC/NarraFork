// Config loading — reads kiro CLI credentials.json and config.json

import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { isApiKeyAuthMethod } from "./auth-method";
import type {
	AccountTier,
	ConcurrencyPlanConfig,
	ConcurrencySettings,
	KiroConfig,
	KiroCredentials,
	RequestIntervalSettings,
	UsageCacheEntry,
} from "./types";

const SYSTEM_VERSIONS = ["darwin#24.6.0", "win32#10.0.22631"] as const;

function randomSystemVersion(): string {
	return SYSTEM_VERSIONS[Math.floor(Math.random() * SYSTEM_VERSIONS.length)];
}

const DEFAULT_CONCURRENCY_PLAN: ConcurrencyPlanConfig = { initialMax: 3, ceiling: 8, floor: 1 };
const DEFAULT_CONCURRENCY: ConcurrencySettings = { default: { ...DEFAULT_CONCURRENCY_PLAN } };
const DEFAULT_REQUEST_INTERVAL: RequestIntervalSettings = { free: 0, enterprise: 0, paid: 0 };
const DEFAULT_TIER_PRIORITY: AccountTier[] = ["free", "enterprise", "paid"];

const CONFIG_DEFAULTS: KiroConfig = {
	region: "us-east-1",
	kiroVersion: "1.26.1",
	systemVersion: randomSystemVersion(),
	nodeVersion: "22.21.1",
	loadBalancingMode: "priority",
	ignoreTierPriority: false,
	tierPriority: [...DEFAULT_TIER_PRIORITY],
	concurrency: { ...DEFAULT_CONCURRENCY },
	requestInterval: { ...DEFAULT_REQUEST_INTERVAL },
};

/**
 * Normalize an on-disk credential in place.
 *
 * - Legacy `iam` auth method maps to `idc`.
 * - Any api_key spelling (`apiKey`, `api-key`, `API_KEY`) maps to `api_key`,
 *   and the stored key is mirrored onto `apiKey`/`accessToken` so both the
 *   bearer-token path and the dedupe path can read it.
 */
function canonicalizeAuthMethod(creds: KiroCredentials): void {
	const lower = creds.authMethod?.toLowerCase();
	if (lower === "iam") {
		creds.authMethod = "idc";
		return;
	}
	if (isApiKeyAuthMethod(lower) || (!creds.refreshToken && creds.apiKey?.trim())) {
		const key = creds.apiKey?.trim() || creds.accessToken?.trim();
		if (!key) return;
		creds.authMethod = "api_key";
		creds.apiKey = key;
		creds.accessToken = key;
	}
}

export function loadCredentials(path: string): KiroCredentials | null {
	if (!existsSync(path)) return null;
	try {
		const raw = readFileSync(path, "utf-8").trim();
		if (!raw) return null;
		const parsed = JSON.parse(raw);
		// Support both single object and array format
		if (Array.isArray(parsed)) {
			// Sort by priority (lower = higher priority), then take first
			const sorted = parsed.sort(
				(a: KiroCredentials, b: KiroCredentials) => (a.priority ?? 0) - (b.priority ?? 0),
			);
			const cred = sorted[0] ?? null;
			if (cred) canonicalizeAuthMethod(cred);
			return cred;
		}
		canonicalizeAuthMethod(parsed);
		return parsed;
	} catch {
		return null;
	}
}

function parseTierPriority(value: unknown): AccountTier[] {
	if (!Array.isArray(value)) return [...DEFAULT_TIER_PRIORITY];
	const result = value.filter(
		(v): v is AccountTier => v === "free" || v === "enterprise" || v === "paid",
	);
	return result.length > 0 ? result : [...DEFAULT_TIER_PRIORITY];
}

function parsePlanConfig(value: unknown): ConcurrencyPlanConfig | undefined {
	if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
	const record = value as Record<string, unknown>;
	const initialMax = Number(record.initialMax);
	const ceiling = Number(record.ceiling);
	const floor = Number(record.floor);
	if (!Number.isFinite(initialMax) || !Number.isFinite(ceiling) || !Number.isFinite(floor)) {
		return undefined;
	}
	return {
		initialMax: Math.max(1, Math.floor(initialMax)),
		ceiling: Math.max(1, Math.floor(ceiling)),
		floor: Math.max(1, Math.floor(floor)),
	};
}

function parseConcurrency(value: unknown): ConcurrencySettings {
	if (!value || typeof value !== "object" || Array.isArray(value)) {
		return { default: { ...DEFAULT_CONCURRENCY_PLAN } };
	}
	const record = value as Record<string, unknown>;
	const parsed: ConcurrencySettings = {
		default: parsePlanConfig(record.default) ?? { ...DEFAULT_CONCURRENCY_PLAN },
	};
	for (const key of ["free", "pro", "pro_plus", "power"] as const) {
		const cfg = parsePlanConfig(record[key]);
		if (cfg) parsed[key] = cfg;
	}
	return parsed;
}

function parseRequestInterval(value: unknown): RequestIntervalSettings {
	if (!value || typeof value !== "object" || Array.isArray(value)) {
		return { ...DEFAULT_REQUEST_INTERVAL };
	}
	const record = value as Record<string, unknown>;
	return {
		free: Math.max(0, Number(record.free) || 0),
		enterprise: Math.max(0, Number(record.enterprise) || 0),
		paid: Math.max(0, Number(record.paid) || 0),
	};
}

export function loadConfig(path: string): KiroConfig {
	if (!existsSync(path)) return { ...CONFIG_DEFAULTS };
	try {
		const raw = readFileSync(path, "utf-8").trim();
		if (!raw) return { ...CONFIG_DEFAULTS };
		const parsed = JSON.parse(raw) as Record<string, unknown>;
		return {
			region: String(parsed.region ?? CONFIG_DEFAULTS.region),
			authRegion: typeof parsed.authRegion === "string" ? parsed.authRegion : undefined,
			apiRegion: typeof parsed.apiRegion === "string" ? parsed.apiRegion : undefined,
			kiroVersion: String(parsed.kiroVersion ?? CONFIG_DEFAULTS.kiroVersion),
			machineId: typeof parsed.machineId === "string" ? parsed.machineId : undefined,
			systemVersion: String(parsed.systemVersion ?? CONFIG_DEFAULTS.systemVersion),
			nodeVersion: String(parsed.nodeVersion ?? CONFIG_DEFAULTS.nodeVersion),
			loadBalancingMode: parsed.loadBalancingMode === "balanced" ? "balanced" : "priority",
			ignoreTierPriority: parsed.ignoreTierPriority === true,
			tierPriority: parseTierPriority(parsed.tierPriority),
			concurrency: parseConcurrency(parsed.concurrency),
			requestInterval: parseRequestInterval(parsed.requestInterval),
		};
	} catch {
		return { ...CONFIG_DEFAULTS };
	}
}

export function saveConfig(path: string, config: KiroConfig): void {
	writeFileSync(path, JSON.stringify(config, null, 2));
}

export function saveCredentials(path: string, creds: KiroCredentials): void {
	writeFileSync(path, JSON.stringify(creds, null, 2));
}

/** cred.authRegion > cred.region > config.authRegion > config.region */
export function effectiveAuthRegion(creds: KiroCredentials, config: KiroConfig): string {
	return creds.authRegion ?? creds.region ?? config.authRegion ?? config.region;
}

/** config.apiRegion > config.region (credential region only used for token refresh) */
export function effectiveApiRegion(config: KiroConfig): string {
	return config.apiRegion ?? config.region;
}

// --- Multi-credential support ---

export interface StatsEntry {
	successCount: number;
	lastUsedAt?: string;
}

/** Load all credentials from file. Supports single object and array format. */
export function loadAllCredentials(path: string): {
	credentials: KiroCredentials[];
	isMultipleFormat: boolean;
} {
	if (!existsSync(path)) return { credentials: [], isMultipleFormat: false };
	try {
		const raw = readFileSync(path, "utf-8").trim();
		if (!raw) return { credentials: [], isMultipleFormat: false };
		const parsed = JSON.parse(raw);
		if (Array.isArray(parsed)) {
			for (const c of parsed) canonicalizeAuthMethod(c);
			const sorted = parsed.sort(
				(a: KiroCredentials, b: KiroCredentials) => (a.priority ?? 0) - (b.priority ?? 0),
			);
			return { credentials: sorted, isMultipleFormat: true };
		}
		canonicalizeAuthMethod(parsed);
		return { credentials: [parsed], isMultipleFormat: false };
	} catch {
		return { credentials: [], isMultipleFormat: false };
	}
}

/** Save all credentials as array format. */
export function saveAllCredentials(path: string, creds: KiroCredentials[]): void {
	writeFileSync(path, JSON.stringify(creds, null, 2));
}

/** Load stats from kiro_stats.json in the same directory as credentialsPath. */
export function loadStats(credentialsPath: string): Record<string, StatsEntry> {
	const statsPath = join(dirname(credentialsPath), "kiro_stats.json");
	if (!existsSync(statsPath)) return {};
	try {
		return JSON.parse(readFileSync(statsPath, "utf-8"));
	} catch {
		return {};
	}
}

/** Save stats to kiro_stats.json in the same directory as credentialsPath. */
export function saveStats(credentialsPath: string, stats: Record<string, StatsEntry>): void {
	const statsPath = join(dirname(credentialsPath), "kiro_stats.json");
	writeFileSync(statsPath, JSON.stringify(stats, null, 2));
}

// --- Usage cache ---

/** Load usage cache from kiro_usage.json in the same directory as credentialsPath. */
export function loadUsageCache(credentialsPath: string): Record<string, UsageCacheEntry> {
	const usagePath = join(dirname(credentialsPath), "kiro_usage.json");
	if (!existsSync(usagePath)) return {};
	try {
		return JSON.parse(readFileSync(usagePath, "utf-8"));
	} catch {
		return {};
	}
}

/** Save usage cache to kiro_usage.json in the same directory as credentialsPath. */
export function saveUsageCache(
	credentialsPath: string,
	cache: Record<string, UsageCacheEntry>,
): void {
	const usagePath = join(dirname(credentialsPath), "kiro_usage.json");
	writeFileSync(usagePath, JSON.stringify(cache, null, 2));
}
