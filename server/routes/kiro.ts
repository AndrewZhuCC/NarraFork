import type { Context } from "hono";
import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
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
import { kiroCredentialsFromImportPayload } from "../lib/kiro-import-text";
import { logger } from "../lib/logger";
import { normalizeProxyUrl, saveSettings, settings } from "../lib/settings";
import { requireAdmin, requireAuth } from "../middleware/auth";

/**
 * AWS region identifier. Kept permissive (the service adds regions over time)
 * but restricted to the `xx-yyy-N` shape so a typo cannot inject an arbitrary
 * host into `q.<region>.amazonaws.com`.
 */
const regionField = z
	.string()
	.max(50)
	.refine((v) => v === "" || /^[a-z]{2}(-[a-z]+)+-\d+$/.test(v), {
		message: "region must look like us-east-1",
	});

/**
 * Credential patch.
 *
 * Text fields accept an empty string to clear the stored value; secrets treat
 * empty as "leave unchanged" since the API never echoes them back.
 *
 * Exported for tests: the region validator is the boundary that keeps a typo from
 * becoming an arbitrary request host.
 */
export const updateCredentialSchema = z
	.object({
		email: z.string().max(200).optional(),
		displayName: z.string().max(200).optional(),
		region: regionField.optional(),
		authRegion: regionField.optional(),
		apiRegion: regionField.optional(),
		profileArn: z.string().max(500).optional(),
		subscriptionTitle: z.string().max(200).optional(),
		startUrl: z.string().max(500).optional(),
		machineId: z.string().max(200).optional(),
		proxyUrl: z.string().max(500).optional(),
		proxyUsername: z.string().max(200).optional(),
		authMethod: z.enum(["social", "idc", "api_key"]).optional(),
		priority: z.number().int().min(0).max(10_000).optional(),
		refreshToken: z.string().max(8192).optional(),
		apiKey: z.string().max(8192).optional(),
		clientId: z.string().max(500).optional(),
		clientSecret: z.string().max(8192).optional(),
		proxyPassword: z.string().max(500).optional(),
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

/**
 * Cap on a credential-import body.
 *
 * Import parsing (JSON.parse plus the API-key scan) is synchronous main-thread
 * work, so the payload must stay small. Real inputs are a handful of tokens.
 */
const MAX_CREDENTIAL_IMPORT_BYTES = 1024 * 1024;

/**
 * Cap on a single-credential patch body. Every field is bounded by the schema,
 * so a legitimate patch is a few kilobytes at most.
 */
const MAX_CREDENTIAL_PATCH_BYTES = 64 * 1024;

/** Accepted `credentials[]` entry: a credential object or a raw token string. */
function isImportableItem(item: unknown): boolean {
	if (typeof item === "string") return true;
	return typeof item === "object" && item !== null && !Array.isArray(item);
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
//
// Body-capped: parsing and key extraction are SYNCHRONOUS work on the single JS
// thread that also serves every other request and WebSocket, so an oversized paste
// would stall the whole server. A credential list is kilobytes; 1 MiB is generous.
kiroRoutes.post(
	"/credentials/import",
	bodyLimit({
		maxSize: MAX_CREDENTIAL_IMPORT_BYTES,
		onError: (c) =>
			c.json(
				{
					error: "Credential import exceeds the 1 MiB limit",
					code: "CREDENTIAL_IMPORT_TOO_LARGE",
				},
				413,
			),
	}),
	async (c) => {
		let body: Record<string, unknown> | null;
		try {
			body = (await c.req.json()) as Record<string, unknown> | null;
		} catch (err) {
			// A body rejected by the size cap must surface as 413, not as a generic
			// "malformed credentials" 400. When the request carries no Content-Length,
			// bodyLimit enforces the cap by ERRORING THE BODY STREAM, so the overflow
			// arrives here as a failed read; rethrowing lets that middleware turn it
			// into its own 413 response.
			if (err instanceof Error && err.name === "BodyLimitError") throw err;
			body = null;
		}
		if (!body || typeof body !== "object" || Array.isArray(body)) {
			return c.json({ error: "credentials must be an array" }, 400);
		}
		// `credentials` keeps accepting the historical object array. Strings (either in
		// that array or via `text` / `importText`) let callers paste bare "ksk_..." key
		// lists without JSON wrapping.
		const raw = body.credentials;
		if (raw !== undefined && !Array.isArray(raw) && typeof raw !== "string") {
			return c.json({ error: "credentials must be an array" }, 400);
		}
		if (Array.isArray(raw) && raw.some((item) => !isImportableItem(item))) {
			return c.json({ error: "each credential must be a plain object" }, 400);
		}
		const credentials = [
			...kiroCredentialsFromImportPayload(raw),
			...kiroCredentialsFromImportPayload(body.text),
			...kiroCredentialsFromImportPayload(body.importText),
		];
		// An explicitly empty `credentials` array stays a reload trigger for existing
		// credentials; anything else with no usable material is a client error.
		if (credentials.length === 0 && !(Array.isArray(raw) && raw.length === 0)) {
			return c.json({ error: "No credentials provided" }, 400);
		}
		try {
			const result = kiroImportCredentials(credentials);
			// Queue background model-list fetching for newly added credentials.
			// This is fire-and-forget: the route returns immediately with import counts,
			// and models will be available once the background fetch completes. The
			// front-end already polls `["kiro", "status"]` which includes model lists.
			if (result.added > 0) {
				const snap = kiroSnapshot();
				if (snap) {
					for (const entry of snap.entries) {
						if (!entry.disabled && !entry.availableModels?.length) {
							void kiroFetchCredentialModels(entry.id).catch(() => {});
						}
					}
				}
			}
			return c.json(result);
		} catch (err: unknown) {
			const msg = err instanceof Error ? err.message : "Unknown error";
			return c.json({ error: msg }, 500);
		}
	},
);

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

/**
 * Rejected patches are the caller's fault, not a server fault: switching auth
 * methods without the matching secret, or reusing a secret another credential
 * already stores. These must surface as 400 so the editor can show them inline.
 */
function isCredentialPatchRejection(message: string): boolean {
	return (
		message.includes("is required") ||
		message.includes("already used by") ||
		message.includes("must carry")
	);
}

kiroRoutes.patch(
	"/credentials/:id",
	bodyLimit({
		maxSize: MAX_CREDENTIAL_PATCH_BYTES,
		onError: (c) =>
			c.json(
				{ error: "Credential patch exceeds the 64 KiB limit", code: "CREDENTIAL_PATCH_TOO_LARGE" },
				413,
			),
	}),
	async (c) => {
		const guard = requireKiro(c);
		if (guard) return guard;
		try {
			const body = await c.req.json();
			const parsed = updateCredentialSchema.safeParse(body);
			if (!parsed.success) {
				return c.json({ error: parsed.error.issues[0]?.message ?? parsed.error.message }, 400);
			}
			kiroUpdateCredential(c.req.param("id"), parsed.data);
			return c.json({ ok: true });
		} catch (err: unknown) {
			if (err instanceof Error && err.name === "BodyLimitError") throw err;
			const msg = err instanceof Error ? err.message : "Unknown error";
			if (msg.startsWith("Credential not found")) return c.json({ error: msg }, 404);
			return c.json({ error: msg }, isCredentialPatchRejection(msg) ? 400 : 500);
		}
	},
);

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
		.preprocess((v) => {
			if (typeof v !== "string") return v;
			const trimmed = v.trim();
			if (!trimmed) return undefined;
			return normalizeProxyUrl(trimmed) ?? trimmed;
		}, z.string().optional())
		.refine(
			(v) => {
				if (!v) return true;
				try {
					const url = new URL(v);
					return ["http:", "https:"].includes(url.protocol);
				} catch {
					return false;
				}
			},
			{
				message: "Proxy must be a valid URL with http or https protocol",
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
