import { outboundFetch } from "./net/outbound-fetch";
import { resolveProxyForUrl } from "./net/proxy";
import { BalanceProbeRequestError, type BalanceRequest } from "./provider-balance-adapters";
import type { CustomApiProviderConfig } from "./settings";
import { getHttpUserAgent, resolveHttpUserAgent } from "./user-agent";

export const BALANCE_MAX_RESPONSE_BYTES = 64 * 1024;
export const BALANCE_REQUEST_TIMEOUT_MS = 8_000;

/** An abort-ignoring transport must not hold a queue slot or publish a late response. */
function abortable<T>(
	promise: Promise<T>,
	signal: AbortSignal,
	late?: (value: T) => void,
): Promise<T> {
	return new Promise((resolve, reject) => {
		let settled = false;
		const abort = () => {
			if (settled) return;
			settled = true;
			reject(new BalanceProbeRequestError("transient"));
		};
		signal.addEventListener("abort", abort, { once: true });
		if (signal.aborted) abort();
		promise.then(
			(value) => {
				signal.removeEventListener("abort", abort);
				if (settled) late?.(value);
				else {
					settled = true;
					resolve(value);
				}
			},
			() => {
				signal.removeEventListener("abort", abort);
				if (!settled) {
					settled = true;
					reject(new BalanceProbeRequestError("transient"));
				}
			},
		);
	});
}

const HOP_BY_HOP_HEADERS = new Set([
	"host",
	"connection",
	"content-length",
	"transfer-encoding",
	"proxy-authorization",
	"proxy-connection",
	"upgrade",
	"keep-alive",
	"trailer",
	"te",
]);

export function balanceRequestHeaders(
	config: CustomApiProviderConfig,
	authenticated: boolean,
): Headers {
	// Public site metadata must not receive even a credential-bearing custom header.
	if (!authenticated) return new Headers({ Accept: "application/json" });
	const headers = new Headers({
		Authorization: `Bearer ${config.apiKey}`,
		"User-Agent": resolveHttpUserAgent({
			mode: config.userAgentMode,
			custom: config.customUserAgent,
			fallback: getHttpUserAgent(),
		}),
	});
	const extra = new Headers(config.extraHeaders);
	const blocked = new Set(HOP_BY_HOP_HEADERS);
	for (const name of (extra.get("Connection") ?? "").split(",")) {
		const normalized = name.trim().toLowerCase();
		if (normalized) blocked.add(normalized);
	}
	for (const [name, value] of extra) {
		if (!blocked.has(name.toLowerCase())) headers.set(name, value);
	}
	for (const name of blocked) headers.delete(name);
	headers.set("Accept", "application/json");
	return headers;
}

export function createBalanceRequest(
	config: CustomApiProviderConfig,
	roundSignal: AbortSignal,
	options: {
		fetch?: typeof outboundFetch;
		requestTimeoutMs?: number;
	} = {},
): BalanceRequest {
	const fetcher = options.fetch ?? outboundFetch;
	return async (url, authenticated = true) => {
		const target = new URL(url);
		if (
			target.origin !== new URL(config.baseUrl).origin ||
			target.username ||
			target.password ||
			target.search ||
			target.hash ||
			url.length > 4096
		) {
			throw new BalanceProbeRequestError("unsupported");
		}
		const controller = new AbortController();
		const signal = AbortSignal.any([roundSignal, controller.signal]);
		const timer = setTimeout(
			() => controller.abort(),
			options.requestTimeoutMs ?? BALANCE_REQUEST_TIMEOUT_MS,
		);
		let response: Response | undefined;
		try {
			if (signal.aborted) throw new BalanceProbeRequestError("transient");
			response = await abortable(
				fetcher(
					url,
					{
						method: "GET",
						headers: balanceRequestHeaders(config, authenticated),
						redirect: "error",
						signal,
					},
					{
						proxyUrl: resolveProxyForUrl(url, config.proxy),
						tlsRejectUnauthorized: config.tlsRejectUnauthorized,
						retryPolicy: "never",
					},
				),
				signal,
				(late) => void late.body?.cancel().catch(() => {}),
			);
			if (!response.ok) {
				throw new BalanceProbeRequestError(
					response.status === 404 || response.status === 405 ? "unsupported" : "transient",
				);
			}
			const contentType = response.headers.get("content-type")?.split(";")[0]?.trim();
			if (contentType && contentType !== "application/json" && !contentType.endsWith("+json")) {
				throw new BalanceProbeRequestError("unsupported");
			}
			const declaredLength = Number(response.headers.get("content-length"));
			if (declaredLength > BALANCE_MAX_RESPONSE_BYTES) {
				throw new BalanceProbeRequestError("transient");
			}
			const reader = response.body?.getReader();
			if (!reader) throw new BalanceProbeRequestError("unsupported");
			const chunks: Uint8Array[] = [];
			let length = 0;
			const cancel = () => void reader.cancel().catch(() => {});
			signal.addEventListener("abort", cancel, { once: true });
			try {
				while (true) {
					const { value, done } = await abortable(reader.read(), signal);
					if (done) break;
					length += value.byteLength;
					if (length > BALANCE_MAX_RESPONSE_BYTES) {
						throw new BalanceProbeRequestError("transient");
					}
					chunks.push(value);
				}
				const bytes = new Uint8Array(length);
				let offset = 0;
				for (const chunk of chunks) {
					bytes.set(chunk, offset);
					offset += chunk.byteLength;
				}
				if (signal.aborted) throw new BalanceProbeRequestError("transient");
				try {
					return JSON.parse(new TextDecoder().decode(bytes)) as unknown;
				} catch {
					throw new BalanceProbeRequestError("unsupported");
				}
			} finally {
				signal.removeEventListener("abort", cancel);
				cancel();
				reader.releaseLock();
			}
		} catch (error) {
			if (error instanceof BalanceProbeRequestError) throw error;
			throw new BalanceProbeRequestError("transient");
		} finally {
			clearTimeout(timer);
			if (response?.body && !response.body.locked) void response.body.cancel().catch(() => {});
		}
	};
}
