import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { Hono } from "hono";
import { generateShortId } from "../lib/id";
import { logger } from "../lib/logger";
import {
	type GrokProviderConfig,
	grokProviderPrefix,
	narraforkDir,
	registerGrokModelChecker,
	registerGrokModelLister,
	saveSettings,
	settings,
} from "../lib/settings";
import { requireAdmin, requireAuth } from "../middleware/auth";

export const grokRoutes = new Hono();

// All Grok routes require admin privileges
grokRoutes.use("*", requireAuth, requireAdmin);

/** Ensure at least one default Grok provider config exists. */
function ensureDefaultProvider(): GrokProviderConfig {
	if (!settings.grokProviders) {
		settings.grokProviders = [];
	}
	if (settings.grokProviders.length === 0) {
		const defaultProvider: GrokProviderConfig = {
			id: generateShortId(),
			name: "Grok",
			prefix: "grok",
			baseUrl: "https://api.x.ai/v1",
			defaultModel: "grok-4-latest",
			enableWebSearch: true,
			enableXSearch: false,
		};
		settings.grokProviders.push(defaultProvider);
		saveSettings(settings);
		logger.info("Created default Grok provider config", { id: defaultProvider.id });
	}
	return settings.grokProviders[0];
}

// === Model cache ===

export interface GrokModelInfo {
	id: string;
	name?: string;
	contextLength?: number;
}

const cacheDir = narraforkDir;
const modelsCachePath = resolve(cacheDir, "grok-models.json");

/** Cached models keyed by provider config id. */
const cachedModelsByProvider = new Map<string, GrokModelInfo[]>();

function loadCachedModels(): void {
	try {
		if (existsSync(modelsCachePath)) {
			const data = JSON.parse(readFileSync(modelsCachePath, "utf-8")) as Record<
				string,
				GrokModelInfo[]
			>;
			for (const [id, models] of Object.entries(data)) {
				cachedModelsByProvider.set(id, models);
			}
		}
	} catch {
		// corrupt — ignore
	}
}

function saveCachedModels(): void {
	try {
		mkdirSync(cacheDir, { recursive: true });
		const data: Record<string, GrokModelInfo[]> = {};
		for (const [id, models] of cachedModelsByProvider) {
			data[id] = models;
		}
		writeFileSync(modelsCachePath, JSON.stringify(data));
	} catch {
		// non-critical
	}
}

// Load cache on startup and migrate legacy config
loadCachedModels();
ensureDefaultProvider();

/** Remove cached models for providers that no longer exist in settings. */
export function purgeGrokProviderCache(removedIds: string[]): void {
	let changed = false;
	for (const id of removedIds) {
		if (cachedModelsByProvider.delete(id)) changed = true;
	}
	if (changed) saveCachedModels();
}

/** Get all cached models across all providers. */
export function getGrokCachedModels(): GrokModelInfo[] {
	const seen = new Set<string>();
	const result: GrokModelInfo[] = [];
	for (const models of cachedModelsByProvider.values()) {
		for (const m of models) {
			if (m.id && !seen.has(m.id)) {
				seen.add(m.id);
				result.push(m);
			}
		}
	}
	return result;
}

/**
 * Fill context window sizes into settings for newly added models, using cached
 * model data. Grok 4.x defaults to 500K when the gateway omits context sizes.
 */
async function fillContextWindows(
	newModels: string[],
	provider: GrokProviderConfig,
): Promise<void> {
	let cached = getGrokCachedModels();
	if (cached.length === 0) {
		cached = await fetchGrokModels(provider);
		cachedModelsByProvider.set(provider.id, cached);
		saveCachedModels();
	}
	let contextMap = new Map(cached.map((m) => [m.id, m.contextLength]));

	const missing = newModels.filter((id) => !contextMap.has(id));
	if (missing.length > 0) {
		try {
			cached = await fetchGrokModels(provider);
			cachedModelsByProvider.set(provider.id, cached);
			saveCachedModels();
			contextMap = new Map(cached.map((m) => [m.id, m.contextLength]));
		} catch {
			// Non-critical — proceed with whatever we have
		}
	}

	let changed = false;
	const windows = settings.agent.modelContextWindows ?? {};
	settings.agent.modelContextWindows = windows;
	for (const modelId of newModels) {
		const ctx = contextMap.get(modelId);
		const effective = ctx ?? (/grok-4/i.test(modelId) ? 500_000 : undefined);
		if (effective != null && !windows[`${provider.prefix}:${modelId}`]) {
			windows[`${provider.prefix}:${modelId}`] = effective;
			changed = true;
		}
	}
	if (changed) saveSettings(settings);
}

/** Get enabled models grouped by provider. */
export function getGrokModelsGrouped(): Array<{
	providerId: string;
	providerName: string;
	models: GrokModelInfo[];
}> {
	const providers = settings.grokProviders ?? [];
	return providers
		.filter((p) => !p.disabled)
		.map((p) => {
			const poolModels = cachedModelsByProvider.get(p.id) ?? [];
			return {
				providerId: p.id,
				providerName: p.name,
				models: poolModels,
			};
		});
}

// Register model checker and lister — report cached models for enabled providers
registerGrokModelChecker((model) => {
	const providers = settings.grokProviders ?? [];
	for (const p of providers) {
		if (p.disabled) continue;
		for (const m of cachedModelsByProvider.get(p.id) ?? []) {
			if (m.id === model) return true;
		}
	}
	return false;
});

registerGrokModelLister(() => {
	const result: string[] = [];
	const providers = settings.grokProviders ?? [];
	for (const p of providers) {
		if (p.disabled) continue;
		const prefix = grokProviderPrefix(p);
		for (const m of cachedModelsByProvider.get(p.id) ?? []) {
			result.push(`${prefix}:${m.id}`);
		}
	}
	return result;
});

/** Fetch models from the provider's /models endpoint. */
async function fetchGrokModels(config: GrokProviderConfig): Promise<GrokModelInfo[]> {
	const apiKey = config.apiKey;
	if (!apiKey) throw new Error("Grok API key is not configured");
	const baseUrl = (config.baseUrl || "https://api.x.ai/v1").replace(/\/+$/, "");

	const response = await fetch(`${baseUrl}/models`, {
		headers: { Authorization: `Bearer ${apiKey}` },
	});

	if (!response.ok) {
		const errText = await response.text().catch(() => "");
		throw new Error(`Grok models API error ${response.status}: ${errText}`);
	}

	const json = (await response.json()) as {
		data?: Array<{
			id: string;
			name?: string;
			context_window?: number | null;
			context_length?: number | null;
		}>;
	};

	const models: GrokModelInfo[] = (json.data ?? [])
		.filter((m) => m.id)
		.map((m) => ({
			id: m.id,
			name: m.name,
			contextLength:
				m.context_window ?? m.context_length ?? (/grok-4/i.test(m.id) ? 500_000 : undefined),
		}));

	models.sort((a, b) => a.id.localeCompare(b.id));
	return models;
}

// === Routes ===

grokRoutes.post("/refresh", async (c) => {
	const body = (await c.req.json().catch(() => ({}))) as { providerId?: string };
	const providers = settings.grokProviders ?? [];
	const provider =
		providers.find((p) => p.id === body.providerId) ?? providers.find((p) => !p.disabled);
	if (!provider) {
		return c.json({ error: "No Grok provider configured" }, 404);
	}
	if (!provider.apiKey) {
		return c.json({ error: "Grok API key is not configured" }, 400);
	}
	try {
		const models = await fetchGrokModels(provider);
		cachedModelsByProvider.set(provider.id, models);
		saveCachedModels();
		await fillContextWindows(
			models.map((m) => m.id),
			provider,
		);
		return c.json({ providerId: provider.id, modelCount: models.length });
	} catch (error) {
		return c.json({ error: error instanceof Error ? error.message : String(error) }, 502);
	}
});
