// Config loading — reads kiro.rs credentials.json and config.json

import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import type { KiroConfig, KiroCredentials } from "./types";

const SYSTEM_VERSIONS = ["darwin#24.6.0", "win32#10.0.22631"] as const;

function randomSystemVersion(): string {
	return SYSTEM_VERSIONS[Math.floor(Math.random() * SYSTEM_VERSIONS.length)];
}

const CONFIG_DEFAULTS: KiroConfig = {
	region: "us-east-1",
	kiroVersion: "1.26.1",
	systemVersion: randomSystemVersion(),
	nodeVersion: "22.21.1",
};

function canonicalizeAuthMethod(creds: KiroCredentials): void {
	const m = creds.authMethod;
	if (!m) return;
	const lower = m.toLowerCase();
	if (lower === "builder-id" || lower === "iam") {
		creds.authMethod = "idc";
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
				(a: KiroCredentials, b: KiroCredentials) =>
					(a.priority ?? 0) - (b.priority ?? 0),
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

export function loadConfig(path: string): KiroConfig {
	if (!existsSync(path)) return { ...CONFIG_DEFAULTS };
	try {
		const raw = readFileSync(path, "utf-8").trim();
		if (!raw) return { ...CONFIG_DEFAULTS };
		const parsed = JSON.parse(raw);
		return {
			region: parsed.region ?? CONFIG_DEFAULTS.region,
			authRegion: parsed.authRegion ?? undefined,
			apiRegion: parsed.apiRegion ?? undefined,
			kiroVersion: parsed.kiroVersion ?? CONFIG_DEFAULTS.kiroVersion,
			machineId: parsed.machineId ?? undefined,
			systemVersion: parsed.systemVersion ?? CONFIG_DEFAULTS.systemVersion,
			nodeVersion: parsed.nodeVersion ?? CONFIG_DEFAULTS.nodeVersion,
			loadBalancingMode:
				parsed.loadBalancingMode === "balanced" ? "balanced" : undefined,
		};
	} catch {
		return { ...CONFIG_DEFAULTS };
	}
}

export function saveCredentials(
	path: string,
	creds: KiroCredentials,
): void {
	writeFileSync(path, JSON.stringify(creds, null, 2));
}

/** cred.authRegion > cred.region > config.authRegion > config.region */
export function effectiveAuthRegion(
	creds: KiroCredentials,
	config: KiroConfig,
): string {
	return (
		creds.authRegion ?? creds.region ?? config.authRegion ?? config.region
	);
}

/** cred.apiRegion > config.apiRegion > config.region */
export function effectiveApiRegion(
	creds: KiroCredentials,
	config: KiroConfig,
): string {
	return creds.apiRegion ?? config.apiRegion ?? config.region;
}

// --- Multi-credential support ---

export interface StatsEntry {
	successCount: number;
	lastUsedAt?: string;
}

/** Load all credentials from file. Supports single object and array format. */
export function loadAllCredentials(
	path: string,
): { credentials: KiroCredentials[]; isMultipleFormat: boolean } {
	if (!existsSync(path)) return { credentials: [], isMultipleFormat: false };
	try {
		const raw = readFileSync(path, "utf-8").trim();
		if (!raw) return { credentials: [], isMultipleFormat: false };
		const parsed = JSON.parse(raw);
		if (Array.isArray(parsed)) {
			for (const c of parsed) canonicalizeAuthMethod(c);
			const sorted = parsed.sort(
				(a: KiroCredentials, b: KiroCredentials) =>
					(a.priority ?? 0) - (b.priority ?? 0),
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
export function saveAllCredentials(
	path: string,
	creds: KiroCredentials[],
): void {
	writeFileSync(path, JSON.stringify(creds, null, 2));
}

/** Load stats from kiro_stats.json in the same directory as credentialsPath. */
export function loadStats(
	credentialsPath: string,
): Record<string, StatsEntry> {
	const statsPath = join(dirname(credentialsPath), "kiro_stats.json");
	if (!existsSync(statsPath)) return {};
	try {
		return JSON.parse(readFileSync(statsPath, "utf-8"));
	} catch {
		return {};
	}
}

/** Save stats to kiro_stats.json in the same directory as credentialsPath. */
export function saveStats(
	credentialsPath: string,
	stats: Record<string, StatsEntry>,
): void {
	const statsPath = join(dirname(credentialsPath), "kiro_stats.json");
	writeFileSync(statsPath, JSON.stringify(stats, null, 2));
}

// --- Usage cache ---

import type { UsageCacheEntry } from "./types";

/** Load usage cache from kiro_usage.json in the same directory as credentialsPath. */
export function loadUsageCache(
	credentialsPath: string,
): Record<string, UsageCacheEntry> {
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
