import { outboundFetch } from "./net/outbound-fetch";
import { resolveProxyForUrl } from "./net/proxy";
import { type CustomApiProviderConfig, settings } from "./settings";

/**
 * OpenCode Go subscription usage cache.
 *
 * OpenCode Go (`https://opencode.ai/zen/go/v1`, and the equivalent
 * `/inference/go/v1`) exposes the account's plan allowance at
 * `GET {base}/usage`:
 *
 *   { "usage": { "rolling":  { "status", "percent", "resetsAt" },
 *                "weekly":   { ... },
 *                "monthly":  { ... } } }
 *
 * `percent` is how much of that window has already been spent, and the three
 * windows are not calendar periods — the rolling window is the last 5 hours
 * counted from the last request, the week runs from Monday 00:00 UTC, and the
 * month is anchored to the subscription's own anniversary. `resetsAt` is
 * therefore the only reliable statement of "when does this refill", which is
 * exactly what the status bar and the details popover need.
 *
 * The endpoint is not part of OpenCode's documented Console API surface, so the
 * parser tolerates a missing/renamed window and an unrecognized field value
 * instead of failing the whole refresh: a shape change costs that one window's
 * numbers rather than the other two.
 *
 * Known gap, shared with the Kimi indicator: the status bar renders its quota
 * block only when the bar text is non-null, and the bar text comes from `rolling`.
 * So an upstream that renamed `rolling` while keeping the other two windows would
 * leave the block hidden instead of showing weekly/monthly. The parser still
 * carries whatever windows it recognized, so fixing that is a display-layer
 * change (in both indicators), not a data-layer one.
 *
 * Cache layout follows the DeepSeek balance cache rather than the Kimi one:
 * entries are MEMORY-ONLY and keyed by a credential identity, because a
 * percentage of a plan belongs to one account. Persisting it would let a
 * restart resurrect an allowance under a different key, and `generation` is what
 * stops a late response from an account the user has since replaced from
 * publishing into the current one.
 */

export interface OpencodeUsageWindow {
	status: "ok" | "rate-limited" | null;
	/** Share of the window's allowance already used, 0-100. */
	percent: number | null;
	/** ISO timestamp when the window resets, as reported upstream. */
	resetsAt: string | null;
}

export interface OpencodeUsagePayload {
	/** 5-hour rolling window; the status bar shows what is left of it. */
	rolling: OpencodeUsageWindow | null;
	weekly: OpencodeUsageWindow | null;
	/** Billing-anniversary month, not a calendar month. */
	monthly: OpencodeUsageWindow | null;
}

export interface OpencodeUsageCache extends OpencodeUsagePayload {
	generation: string;
	fetchedAt: number;
	error: string | null;
}

export const OPENCODE_USAGES_STALE_MS = 60_000;
const MAX_RESPONSE_BYTES = 64 * 1024;
const FETCH_TIMEOUT_MS = 15_000;

/**
 * Paths that serve the Go plan. Both spellings are in use: the provider preset
 * writes `/zen/go/v1`, and deployments routed through the inference service use
 * `/inference/go/v1` (OpenCode's own docs list the former, its client the
 * latter). Only these two are accepted — a relay that merely resells OpenCode
 * models to a different host must not be probed with someone's key.
 */
const GO_PATH = /^\/(?:zen|inference)\/go\/v1$/;

interface Entry {
	identity: string;
	cache: OpencodeUsageCache;
	inFlight?: Promise<OpencodeUsageCache | null>;
	controller?: AbortController;
}

// Deliberately memory-only: a plan allowance describes one credential identity
// and must not survive a restart or be restored under a different account.
const entries = new Map<string, Entry>();

export function isOpencodeCustomApiProvider(config: {
	baseUrl?: string;
	disabled?: boolean;
}): boolean {
	if (config.disabled || !config.baseUrl) return false;
	try {
		const url = new URL(config.baseUrl);
		const host = url.hostname.toLowerCase();
		return (
			url.protocol === "https:" &&
			(host === "opencode.ai" || host.endsWith(".opencode.ai")) &&
			!url.port &&
			!url.username &&
			!url.password &&
			!url.search &&
			!url.hash &&
			GO_PATH.test(url.pathname.replace(/\/$/, ""))
		);
	} catch {
		return false;
	}
}

/** The usage endpoint for a configured Go base URL, e.g. `.../zen/go/v1/usage`. */
export function opencodeUsageUrl(baseUrl: string): string {
	const url = new URL(baseUrl);
	url.pathname = `${url.pathname.replace(/\/+$/, "")}/usage`;
	url.search = "";
	url.hash = "";
	return url.toString();
}

function identity(config: CustomApiProviderConfig): string {
	return JSON.stringify([
		config.baseUrl,
		config.protocol,
		config.apiKey,
		config.proxy,
		config.tlsRejectUnauthorized,
		settings.proxy,
	]);
}

/** Reconcile on every read/refresh as well as settings writes. Old requests lose
 * their entry token, so even an abort-ignoring transport cannot publish a result. */
export function syncOpencodeUsageCache(): void {
	const providers = new Map(
		(settings.customApiProviders ?? [])
			.filter((p) => isOpencodeCustomApiProvider(p) && p.apiKey)
			.map((p) => [p.id, p]),
	);
	for (const [id, entry] of entries) {
		const config = providers.get(id);
		if (!config || identity(config) !== entry.identity) {
			entries.delete(id);
			entry.controller?.abort();
		}
	}
	for (const [id, config] of providers) {
		if (!entries.has(id)) {
			entries.set(id, {
				identity: identity(config),
				cache: {
					generation: crypto.randomUUID(),
					rolling: null,
					weekly: null,
					monthly: null,
					fetchedAt: 0,
					error: null,
				},
			});
		}
	}
}

function normalizePercent(value: unknown): number | null {
	if (typeof value !== "number" || !Number.isFinite(value)) return null;
	return Math.max(0, Math.min(100, value));
}

function normalizeResetsAt(value: unknown): string | null {
	if (typeof value !== "string" || !value.trim()) return null;
	const raw = value.trim();
	return Number.isNaN(Date.parse(raw)) ? null : raw;
}

function normalizeWindow(record: unknown): OpencodeUsageWindow | null {
	if (!record || typeof record !== "object") return null;
	const r = record as Record<string, unknown>;
	const status = r.status === "ok" || r.status === "rate-limited" ? r.status : null;
	const percent = normalizePercent(r.percent);
	const resetsAt = normalizeResetsAt(r.resetsAt);
	if (status == null && percent == null && resetsAt == null) return null;
	return { status, percent, resetsAt };
}

export function parseOpencodeUsageResponse(body: unknown): OpencodeUsagePayload {
	if (!body || typeof body !== "object") throw new Error("Invalid usage response");
	const usage = (body as Record<string, unknown>).usage;
	if (!usage || typeof usage !== "object") throw new Error("Invalid usage response");
	const record = usage as Record<string, unknown>;
	return {
		rolling: normalizeWindow(record.rolling),
		weekly: normalizeWindow(record.weekly),
		monthly: normalizeWindow(record.monthly),
	};
}

async function fetchUsage(
	config: CustomApiProviderConfig,
	controller: AbortController,
): Promise<OpencodeUsagePayload> {
	const endpoint = opencodeUsageUrl(config.baseUrl);
	const response = await outboundFetch(
		endpoint,
		{
			headers: { Authorization: `Bearer ${config.apiKey}`, Accept: "application/json" },
			redirect: "error",
			signal: controller.signal,
		},
		{
			proxyUrl: resolveProxyForUrl(endpoint, config.proxy),
			tlsRejectUnauthorized: config.tlsRejectUnauthorized,
			retryPolicy: "never",
		},
	);
	if (!response.ok) {
		void response.body?.cancel().catch(() => {});
		throw new Error("Usage request failed");
	}
	const reader = response.body?.getReader();
	if (!reader) throw new Error("Invalid usage response");
	const cancelReader = () => {
		void reader.cancel().catch(() => {});
	};
	controller.signal.addEventListener("abort", cancelReader, { once: true });
	if (controller.signal.aborted) cancelReader();
	let size = 0;
	const chunks: Uint8Array[] = [];
	try {
		const declaredSize = Number(response.headers.get("content-length"));
		if (declaredSize > MAX_RESPONSE_BYTES) throw new Error("Usage response too large");
		while (true) {
			const { done, value } = await reader.read();
			if (done) break;
			size += value.byteLength;
			if (size > MAX_RESPONSE_BYTES) throw new Error("Usage response too large");
			chunks.push(value);
		}
		const bytes = new Uint8Array(size);
		let offset = 0;
		for (const chunk of chunks) {
			bytes.set(chunk, offset);
			offset += chunk.byteLength;
		}
		return parseOpencodeUsageResponse(JSON.parse(new TextDecoder().decode(bytes)));
	} finally {
		controller.signal.removeEventListener("abort", cancelReader);
		void reader.cancel().catch(() => {});
		reader.releaseLock();
	}
}

export function refreshOpencodeUsage(providerId: string): Promise<OpencodeUsageCache | null> {
	syncOpencodeUsageCache();
	const entry = entries.get(providerId);
	const config = settings.customApiProviders?.find((p) => p.id === providerId);
	if (!entry || !config) return Promise.resolve(null);
	if (entry.inFlight) return entry.inFlight;
	const snapshot = structuredClone(config);
	const controller = new AbortController();
	entry.controller = controller;
	entry.inFlight = (async () => {
		let timer: ReturnType<typeof setTimeout> | undefined;
		const cancelled = new Promise<never>((_, reject) => {
			controller.signal.addEventListener("abort", () => reject(new Error("Cancelled")), {
				once: true,
			});
			timer = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);
		});
		try {
			const payload = await Promise.race([fetchUsage(snapshot, controller), cancelled]);
			syncOpencodeUsageCache();
			if (entries.get(providerId) !== entry) return null;
			entry.cache = {
				...payload,
				generation: entry.cache.generation,
				fetchedAt: Date.now(),
				error: null,
			};
		} catch {
			syncOpencodeUsageCache();
			if (entries.get(providerId) !== entry) return null;
			// Never copy transport exceptions, response text, or credentials to logs/API.
			entry.cache = {
				...entry.cache,
				fetchedAt: Date.now(),
				error: "OpenCode usage request failed",
			};
		} finally {
			clearTimeout(timer);
			entry.inFlight = undefined;
			entry.controller = undefined;
		}
		return entry.cache;
	})();
	return entry.inFlight;
}

export function getAllOpencodeCachedUsages(): Record<string, OpencodeUsageCache> {
	syncOpencodeUsageCache();
	return Object.fromEntries([...entries].map(([id, entry]) => [id, entry.cache]));
}

/** Opaque, random runtime tokens only; never expose or derive them from credentials. */
export function getOpencodeUsageGenerations(): Record<string, string> {
	syncOpencodeUsageCache();
	return Object.fromEntries([...entries].map(([id, entry]) => [id, entry.cache.generation]));
}

export async function refreshAllOpencodeUsages(): Promise<void> {
	syncOpencodeUsageCache();
	await Promise.allSettled([...entries.keys()].map(refreshOpencodeUsage));
}

export async function refreshStaleOpencodeUsages(
	maxAgeMs = OPENCODE_USAGES_STALE_MS,
): Promise<void> {
	syncOpencodeUsageCache();
	const now = Date.now();
	await Promise.allSettled(
		[...entries]
			.filter(([, entry]) => !entry.cache.fetchedAt || now - entry.cache.fetchedAt >= maxAgeMs)
			.map(([id]) => refreshOpencodeUsage(id)),
	);
}
