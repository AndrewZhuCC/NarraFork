import type { Context } from "hono";
import { Hono } from "hono";
import { streamSSE } from "hono/streaming";
import { z } from "zod";
import { resolveModel } from "../lib/agent/resolve-model";
import type { KiroChatRequest, KiroModel } from "../lib/kiro-adapter";
import {
	isKiroAvailable,
	kiroChat,
	kiroChatRaw,
	kiroFetchCredentialModels,
	kiroGetAllUsageCache,
	kiroGetCachedModels,
	kiroGetUsage,
	kiroImportCredentials,
	kiroManualRefresh,
	kiroMcpCall,
	kiroMcpListTools,
	kiroQueueUsageFetch,
	kiroRefreshModelCache,
	kiroRemoveCredential,
	kiroResetAndEnable,
	kiroSetDisabled,
	kiroSetLoadBalancingMode,
	kiroSetPriority,
	kiroSnapshot,
	kiroUpdateCredential,
	kiroUsageQueueSnapshot,
} from "../lib/kiro-adapter";
import { logger } from "../lib/logger";
import { normalizeProxyUrl, saveSettings, settings } from "../lib/settings";
import { requireAdmin, requireAuth } from "../middleware/auth";

const updateCredentialSchema = z
	.object({
		email: z.string().max(200).optional(),
		displayName: z.string().max(200).optional(),
		region: z.string().max(50).optional(),
	})
	.strict();

export const kiroRoutes = new Hono();

kiroRoutes.use("*", requireAuth, requireAdmin);

// Guard: return 503 when kiro plugin is not loaded
function requireKiro(c: Context): Response | null {
	if (!isKiroAvailable()) {
		return c.json({ error: "Kiro plugin is not available" }, 503);
	}
	return null;
}

/** Track in-flight auto-fetch to avoid duplicate requests from polling. */
const autoFetchingUsage = new Set<string>();

kiroRoutes.get("/status", (c) => {
	const snapshot = kiroSnapshot();
	const response = {
		available: isKiroAvailable(),
		snapshot,
		usageCache: kiroGetAllUsageCache(),
		usageQueue: kiroUsageQueueSnapshot(),
		// Backward-compat: surface the custom proxy URL from the global policy.
		globalProxy: settings.proxy?.mode === "custom" ? settings.proxy.url : undefined,
	};

	// Auto-fetch usage for credentials that have never been queried
	if (snapshot) {
		for (const entry of snapshot.entries) {
			if (entry.disabled) continue;
			if (entry.meterUsage == null && !autoFetchingUsage.has(entry.id)) {
				autoFetchingUsage.add(entry.id);
				const timeout = setTimeout(() => autoFetchingUsage.delete(entry.id), 30_000);
				kiroQueueUsageFetch(entry.id);
				setTimeout(() => {
					clearTimeout(timeout);
					autoFetchingUsage.delete(entry.id);
				}, 1000);
			}
		}
	}

	return c.json(response);
});

// Import must be registered before :id routes to avoid param matching
// No requireKiro guard — import is needed precisely when kiro has no credentials yet
kiroRoutes.post("/credentials/import", async (c) => {
	const body = await c.req.json();
	const credentials = body?.credentials;
	if (!Array.isArray(credentials)) {
		return c.json({ error: "credentials must be an array" }, 400);
	}
	// Basic shape validation — each entry must be a plain object
	if (credentials.some((c) => typeof c !== "object" || c === null || Array.isArray(c))) {
		return c.json({ error: "each credential must be a plain object" }, 400);
	}
	try {
		const result = kiroImportCredentials(credentials);
		// Synchronously fetch model lists for newly added credentials
		const modelResults: Record<string, string[] | string> = {};
		if (result.added > 0) {
			const snap = kiroSnapshot();
			if (snap) {
				for (const entry of snap.entries) {
					if (!entry.disabled && !entry.availableModels?.length) {
						try {
							const models = await kiroFetchCredentialModels(entry.id);
							modelResults[entry.id] = models;
						} catch (err) {
							modelResults[entry.id] =
								err instanceof Error ? err.message : "Failed to fetch models";
						}
					}
				}
			}
		}
		return c.json({ ...result, modelResults });
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		return c.json({ error: msg }, 500);
	}
});

kiroRoutes.post("/credentials/:id/disable", (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	kiroSetDisabled(c.req.param("id"), true);
	return c.json({ ok: true });
});

kiroRoutes.post("/credentials/:id/enable", (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	kiroSetDisabled(c.req.param("id"), false);
	return c.json({ ok: true });
});

kiroRoutes.post("/credentials/:id/reset", (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	kiroResetAndEnable(c.req.param("id"));
	return c.json({ ok: true });
});

kiroRoutes.delete("/credentials/:id", (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	try {
		kiroRemoveCredential(c.req.param("id"));
		return c.json({ ok: true });
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		return c.json({ error: msg }, 500);
	}
});

kiroRoutes.post("/credentials/:id/priority", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	const { priority } = await c.req.json<{ priority: number }>();
	kiroSetPriority(c.req.param("id"), priority);
	return c.json({ ok: true });
});

kiroRoutes.post("/load-balancing-mode", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	const { mode } = await c.req.json<{ mode: string }>();
	if (mode !== "priority" && mode !== "balanced") {
		return c.json({ error: "mode must be 'priority' or 'balanced'" }, 400);
	}
	kiroSetLoadBalancingMode(mode);
	return c.json({ ok: true });
});

kiroRoutes.post("/credentials/:id/refresh", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	try {
		await kiroManualRefresh(c.req.param("id"));
		return c.json({ ok: true });
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		return c.json({ error: msg }, 500);
	}
});

kiroRoutes.patch("/credentials/:id", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	try {
		const body = await c.req.json();
		const parsed = updateCredentialSchema.safeParse(body);
		if (!parsed.success) return c.json({ error: parsed.error.message }, 400);
		kiroUpdateCredential(c.req.param("id"), parsed.data);
		return c.json({ ok: true });
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		return c.json({ error: msg }, 500);
	}
});

kiroRoutes.get("/credentials/:id/usage", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	const id = c.req.param("id");
	try {
		const usage = await kiroGetUsage(id);
		return c.json(usage);
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		return c.json({ error: msg }, 500);
	}
});

kiroRoutes.get("/models", (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	return c.json({ models: kiroGetCachedModels(), fromCache: true });
});

kiroRoutes.post("/models/refresh", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	try {
		// Fetch models for all credentials, store per-credential, and cache the deduplicated union.
		const allModels = await kiroRefreshModelCache();
		return c.json({ models: allModels, fromCache: false });
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		logger.error("kiro models refresh failed", { error: msg, stack: (err as Error)?.stack });
		return c.json({ error: msg }, 500);
	}
});

kiroRoutes.get("/mcp-tools", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	try {
		const result = await kiroMcpListTools();
		return c.json(result);
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		return c.json({ error: msg }, 500);
	}
});

kiroRoutes.post("/mcp-search", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	try {
		const { query } = await c.req.json<{ query: string }>();
		const result = await kiroMcpCall("web_search", { query });
		return c.json(result);
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		return c.json({ error: msg }, 500);
	}
});

kiroRoutes.post("/chat", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	const body = await c.req.json();

	// Determine which generator to use:
	// Full request: { conversationState: { ... } }
	// Simple request: { text, model? }
	const isFullRequest = body.conversationState != null;

	return streamSSE(c, async (stream) => {
		try {
			const gen = isFullRequest
				? kiroChatRaw(body as KiroChatRequest)
				: kiroChat(body.text, resolveModel(body.model) as KiroModel | undefined);
			for await (const evt of gen) {
				await stream.writeSSE({
					event: evt.eventType,
					data: JSON.stringify(evt.data),
				});
			}
			await stream.writeSSE({ event: "done", data: "" });
		} catch (err: unknown) {
			const msg = err instanceof Error ? err.message : "Unknown error";
			await stream.writeSSE({ event: "error", data: msg });
		}
	});
});

/**
 * POST /api/kiro/global-proxy
 * Set the global proxy for all Kiro requests.
 */
const proxySchema = z.object({
	proxy: z
		.preprocess((v) => (typeof v === "string" ? normalizeProxyUrl(v) : v), z.string().optional())
		.refine(
			(v) => {
				if (!v) return true;
				try {
					const url = new URL(v);
					return ["http:", "https:", "socks5:", "socks5h:", "socks4:"].includes(url.protocol);
				} catch {
					return false;
				}
			},
			{
				message: "Proxy must be a valid URL with http, https, socks5, socks5h, or socks4 protocol",
			},
		),
});

kiroRoutes.post("/global-proxy", async (c) => {
	const parsed = proxySchema.safeParse(await c.req.json().catch(() => ({})));
	if (!parsed.success) {
		return c.json({ error: parsed.error.issues[0]?.message ?? "Invalid proxy URL" }, 400);
	}

	// Backward-compat: map the legacy per-provider proxy onto the unified global
	// policy. A URL switches to "custom"; clearing it falls back to "system".
	settings.proxy = parsed.data.proxy
		? { mode: "custom", url: parsed.data.proxy }
		: { mode: "system" };
	saveSettings(settings);

	return c.json({ ok: true });
});
