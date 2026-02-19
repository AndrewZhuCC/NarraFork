import type { Context } from "hono";
import { Hono } from "hono";
import { streamSSE } from "hono/streaming";
import { resolveModel } from "../lib/agent/resolve-model";
import type { KiroChatRequest, KiroModel } from "../lib/kiro-adapter";
import {
	isKiroAvailable,
	kiroChat,
	kiroChatRaw,
	kiroGetAllUsageCache,
	kiroGetUsage,
	kiroImportCredentials,
	kiroManualRefresh,
	kiroMcpCall,
	kiroMcpListTools,
	kiroRemoveCredential,
	kiroResetAndEnable,
	kiroSetDisabled,
	kiroSetPriority,
	kiroSnapshot,
} from "../lib/kiro-adapter";
import { requireAdmin, requireAuth } from "../middleware/auth";

export const kiroRoutes = new Hono();

kiroRoutes.use("*", requireAuth, requireAdmin);

// Guard: return 503 when kiro plugin is not loaded
function requireKiro(c: Context): Response | null {
	if (!isKiroAvailable()) {
		return c.json({ error: "Kiro plugin is not available" }, 503);
	}
	return null;
}

kiroRoutes.get("/status", (c) => {
	return c.json({
		available: isKiroAvailable(),
		snapshot: kiroSnapshot(),
		usageCache: kiroGetAllUsageCache(),
	});
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
		return c.json(result);
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		return c.json({ error: msg }, 500);
	}
});

kiroRoutes.post("/credentials/:id/disable", (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	kiroSetDisabled(Number(c.req.param("id")), true);
	return c.json({ ok: true });
});

kiroRoutes.post("/credentials/:id/enable", (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	kiroSetDisabled(Number(c.req.param("id")), false);
	return c.json({ ok: true });
});

kiroRoutes.post("/credentials/:id/reset", (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	kiroResetAndEnable(Number(c.req.param("id")));
	return c.json({ ok: true });
});

kiroRoutes.delete("/credentials/:id", (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	try {
		kiroRemoveCredential(Number(c.req.param("id")));
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
	kiroSetPriority(Number(c.req.param("id")), priority);
	return c.json({ ok: true });
});

kiroRoutes.post("/credentials/:id/refresh", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	try {
		await kiroManualRefresh(Number(c.req.param("id")));
		return c.json({ ok: true });
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
		return c.json({ error: msg }, 500);
	}
});

kiroRoutes.get("/credentials/:id/usage", async (c) => {
	const guard = requireKiro(c);
	if (guard) return guard;
	const id = Number(c.req.param("id"));
	try {
		const usage = await kiroGetUsage(id);
		return c.json(usage);
	} catch (err: unknown) {
		const msg = err instanceof Error ? err.message : "Unknown error";
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
