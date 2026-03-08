import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { resolve } from "node:path";
import { Hono } from "hono";
import { logger } from "../lib/logger";
import {
	type NKPProviderConfig,
	nkpProviderPrefix,
	registerNkpModelChecker,
	registerNkpModelLister,
	settings,
} from "../lib/settings";

export const nkpRoutes = new Hono();

const cacheDir = resolve(homedir(), ".narrafork");

/** NKP model info — same shape as Kiro's ListAvailableModels response items. */
export type NkpModelInfo = Record<string, unknown>;

/** Per-provider cached model lists. Key = provider id. */
const cachedModelsByProvider = new Map<string, NkpModelInfo[]>();

// === In-memory NKP quota cache (key = provider id) ===

interface NkpQuotaCache {
	quotaBalance: number | null;
	quotaTotalGranted: number | null;
	fetchedAt: number;
}

const cachedQuotaByProvider = new Map<string, NkpQuotaCache>();

/** Fetch quota from a single NKP provider. */
async function fetchNkpQuota(
	config: NKPProviderConfig,
): Promise<{ quotaBalance: number; quotaTotalGranted: number } | null> {
	const baseUrl = (config.baseUrl || "").replace(/\/+$/, "");
	if (!baseUrl || !config.apiKey) return null;
	try {
		const response = await fetch(`${baseUrl}/v1/quota`, {
			headers: { Authorization: `Bearer ${config.apiKey}` },
		});
		if (!response.ok) return null;
		return (await response.json()) as { quotaBalance: number; quotaTotalGranted: number };
	} catch {
		return null;
	}
}

/** Fetch quota for all configured NKP providers (called on startup). */
export async function fetchAllNkpQuotas(): Promise<void> {
	const providers = settings.nkpProviders ?? [];
	await Promise.allSettled(
		providers.map(async (p) => {
			const data = await fetchNkpQuota(p);
			if (data) {
				cachedQuotaByProvider.set(p.id, {
					quotaBalance: data.quotaBalance,
					quotaTotalGranted: data.quotaTotalGranted,
					fetchedAt: Date.now(),
				});
				logger.debug("NKP quota fetched on startup", {
					provider: p.name,
					quotaBalance: data.quotaBalance,
				});
			}
		}),
	);
}

/** Update cached quota balance for a provider identified by prefix. */
export function updateNkpQuotaByPrefix(prefix: string, quotaBalance: number | null): void {
	const providers = settings.nkpProviders ?? [];
	const config = providers.find((p) => p.prefix === prefix);
	if (!config) return;
	const existing = cachedQuotaByProvider.get(config.id);
	cachedQuotaByProvider.set(config.id, {
		quotaBalance,
		quotaTotalGranted: existing?.quotaTotalGranted ?? null,
		fetchedAt: Date.now(),
	});
}

/** Get cached quota for a provider by ID. */
export function getNkpCachedQuota(providerId: string): NkpQuotaCache | undefined {
	return cachedQuotaByProvider.get(providerId);
}

/** Get all cached quotas keyed by provider ID. */
export function getAllNkpCachedQuotas(): Record<
	string,
	{ quotaBalance: number | null; quotaTotalGranted: number | null }
> {
	const result: Record<string, { quotaBalance: number | null; quotaTotalGranted: number | null }> =
		{};
	for (const [id, cache] of cachedQuotaByProvider) {
		result[id] = {
			quotaBalance: cache.quotaBalance,
			quotaTotalGranted: cache.quotaTotalGranted,
		};
	}
	return result;
}

// Load cache on startup
loadAllCachedModels();

function loadAllCachedModels(): void {
	const cachePath = resolve(cacheDir, "nkp-models-providers.json");
	try {
		if (existsSync(cachePath)) {
			const data = JSON.parse(readFileSync(cachePath, "utf-8")) as Record<string, NkpModelInfo[]>;
			for (const [id, models] of Object.entries(data)) {
				cachedModelsByProvider.set(id, models);
			}
		}
	} catch {
		// corrupt — ignore
	}
}

function saveAllCachedModels(): void {
	try {
		mkdirSync(cacheDir, { recursive: true });
		const data: Record<string, NkpModelInfo[]> = {};
		for (const [id, models] of cachedModelsByProvider) {
			data[id] = models;
		}
		writeFileSync(resolve(cacheDir, "nkp-models-providers.json"), JSON.stringify(data));
	} catch {
		// non-critical
	}
}

/** Get cached models for a specific provider. */
export function getNkpCachedModelsByProvider(providerId: string): NkpModelInfo[] {
	return cachedModelsByProvider.get(providerId) ?? [];
}

/** Get all cached models across all providers. */
export function getNkpCachedModels(): NkpModelInfo[] {
	const seen = new Set<string>();
	const result: NkpModelInfo[] = [];
	for (const models of cachedModelsByProvider.values()) {
		for (const m of models) {
			const id = String(m.model_id ?? m.modelId ?? "");
			if (id && !seen.has(id)) {
				seen.add(id);
				result.push(m);
			}
		}
	}
	return result;
}

/** Get all cached models grouped by provider. */
export function getNkpCachedModelsGrouped(): Array<{
	providerId: string;
	providerName: string;
	models: NkpModelInfo[];
}> {
	const providers = settings.nkpProviders ?? [];
	return providers
		.filter((p) => cachedModelsByProvider.has(p.id))
		.map((p) => ({
			providerId: p.id,
			providerName: p.name,
			models: cachedModelsByProvider.get(p.id) ?? [],
		}));
}

// Register model checker and lister
registerNkpModelChecker((model) => {
	for (const models of cachedModelsByProvider.values()) {
		if (models.some((m) => String(m.model_id ?? m.modelId ?? "") === model)) return true;
	}
	return false;
});

registerNkpModelLister(() => {
	const result: string[] = [];
	const providers = settings.nkpProviders ?? [];
	for (const p of providers) {
		const prefix = nkpProviderPrefix(p);
		const models = cachedModelsByProvider.get(p.id) ?? [];
		for (const m of models) {
			result.push(`${prefix}:${String(m.model_id ?? m.modelId ?? "")}`);
		}
	}
	return result;
});

/** Fetch models from NKP service (Kiro-compatible /v1/kiro/models endpoint). */
async function fetchNkpModels(config: NKPProviderConfig): Promise<NkpModelInfo[]> {
	const baseUrl = (config.baseUrl || "").replace(/\/+$/, "");
	if (!baseUrl) {
		throw new Error(`NKP base URL not configured for provider "${config.name}"`);
	}
	if (!config.apiKey) {
		throw new Error(`NKP API key not configured for provider "${config.name}"`);
	}

	const response = await fetch(`${baseUrl}/v1/kiro/models`, {
		headers: {
			Authorization: `Bearer ${config.apiKey}`,
		},
	});

	if (!response.ok) {
		const errText = await response.text().catch(() => "");
		throw new Error(`NKP API error ${response.status}: ${errText}`);
	}

	// NKP returns OpenAI-list envelope with Kiro-style fields:
	// { object: "list", data: [{ id, model_name, model_short_name, rate_multiplier, ... }] }
	const json = (await response.json()) as {
		data?: Array<Record<string, unknown>>;
	};
	const models = json.data ?? [];
	// Normalize: copy `id` → `model_id` so downstream code can use the Kiro field name uniformly
	for (const m of models) {
		if (m.id && !m.model_id) {
			m.model_id = m.id;
		}
		if (!m.model_short_name) {
			const name = String(m.model_name ?? "");
			m.model_short_name = name.replace(/^Claude\s+/i, "");
		}
	}
	const seen = new Set<string>();
	const unique = models.filter((m) => {
		const id = String(m.model_id ?? "");
		if (!id || seen.has(id)) return false;
		seen.add(id);
		return true;
	});
	unique.sort((a, b) => {
		const aId = String(a.model_id ?? "");
		const bId = String(b.model_id ?? "");
		return aId.localeCompare(bId);
	});
	return unique;
}

// Routes
nkpRoutes.get("/models", (c) => {
	return c.json({ models: getNkpCachedModels(), fromCache: true });
});

nkpRoutes.post("/models/refresh", async (c) => {
	const providers = settings.nkpProviders ?? [];
	const results: Array<{ providerId: string; name: string; count: number; error?: string }> = [];
	for (const p of providers) {
		try {
			const models = await fetchNkpModels(p);
			cachedModelsByProvider.set(p.id, models);
			results.push({ providerId: p.id, name: p.name, count: models.length });
		} catch (err: unknown) {
			const msg = err instanceof Error ? err.message : "Unknown error";
			logger.error("NKP listModels refresh failed", { error: msg, provider: p.name });
			results.push({ providerId: p.id, name: p.name, count: 0, error: msg });
		}
	}
	saveAllCachedModels();
	return c.json({ results, models: getNkpCachedModels(), fromCache: false });
});

nkpRoutes.get("/providers/:id/models", (c) => {
	const id = c.req.param("id");
	return c.json({ models: getNkpCachedModelsByProvider(id), fromCache: true });
});

nkpRoutes.post("/providers/:id/models/refresh", async (c) => {
	const id = c.req.param("id");
	const providers = settings.nkpProviders ?? [];
	const config = providers.find((p) => p.id === id);
	if (!config) {
		return c.json({ error: `Provider "${id}" not found` }, 404);
	}
	try {
		const models = await fetchNkpModels(config);
		cachedModelsByProvider.set(id, models);
		saveAllCachedModels();
		return c.json({ models, fromCache: false });
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		logger.error("NKP listModels refresh failed", { error: msg, provider: config.name });
		return c.json({ error: msg }, 500);
	}
});

/** Proxy quota balance from NKP service (updates cache). */
nkpRoutes.get("/providers/:id/quota", async (c) => {
	const id = c.req.param("id");
	const providers = settings.nkpProviders ?? [];
	const config = providers.find((p) => p.id === id);
	if (!config) {
		return c.json({ error: `Provider "${id}" not found` }, 404);
	}
	const baseUrl = (config.baseUrl || "").replace(/\/+$/, "");
	if (!baseUrl || !config.apiKey) {
		return c.json({ error: "Provider not fully configured" }, 400);
	}
	try {
		const response = await fetch(`${baseUrl}/v1/quota`, {
			headers: { Authorization: `Bearer ${config.apiKey}` },
		});
		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			return c.json({ error: `NKP API error ${response.status}: ${errText}` }, 502);
		}
		const data = (await response.json()) as {
			quotaBalance?: number;
			quotaTotalGranted?: number;
		};
		// Update cache
		cachedQuotaByProvider.set(id, {
			quotaBalance: data.quotaBalance ?? null,
			quotaTotalGranted: data.quotaTotalGranted ?? null,
			fetchedAt: Date.now(),
		});
		return c.json(data);
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		logger.error("NKP quota fetch failed", { error: msg, provider: config.name });
		return c.json({ error: msg }, 502);
	}
});

/** Get cached quotas for all NKP providers. */
nkpRoutes.get("/quotas", (c) => {
	return c.json(getAllNkpCachedQuotas());
});
