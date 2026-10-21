/**
 * OpenCode Go endpoint identification, shared by server and frontend.
 *
 * OpenCode Go is served under two path roots on `opencode.ai`, verified against
 * the live gateway and OpenCode's own console source (`inference-proxy.ts`):
 *
 *  - `zen` — the standard route documented for OpenCode clients:
 *      `/zen/go/v1/{chat/completions,responses,messages,models,usage}`.
 *    It is a thin proxy: every call is forwarded to the matching `inference`
 *    path, so authentication, quota accounting and the error bodies are the
 *    inference service's. Two differences matter to a client:
 *      * there is NO Gemini route (`/zen/go/v1/models/<m>:generateContent` 404s);
 *      * `/messages` reads the key from `x-api-key` only. A Bearer-only request
 *        is rejected as "Go requires a credential".
 *  - `inference` — the Go-specific inference service itself:
 *      `/inference/go/v1/{chat/completions,responses,messages,models,usage}`
 *    plus per-family roots, which the Go docs list per model:
 *      `/inference/go/openai/v1/{chat/completions,responses}`,
 *      `/inference/go/anthropic/v1/messages`,
 *      `/inference/go/google/v1beta/models/<m>:generateContent`.
 *    It accepts either Bearer or `x-api-key`. The family roots have no
 *    `/usage` or `/models`; those live only under `/inference/go/v1`.
 *
 * Quota state is per account, not per path, so every Go root maps to one
 * `/usage` endpoint, which is how the quota-wait path can read the reset clock
 * whichever root a provider was configured with.
 */

export type OpencodeGoRoute = "zen" | "inference";

/** Wire family a Go base URL is pinned to; `unified` serves every family. */
export type OpencodeGoFamily = "unified" | "openai" | "anthropic" | "google";

export interface OpencodeGoEndpoint {
	route: OpencodeGoRoute;
	family: OpencodeGoFamily;
	/** `GET` endpoint that reports the plan's rolling/weekly/monthly windows. */
	usageUrl: string;
}

/**
 * Accepted paths, after trailing slashes are removed. Only these exact roots
 * qualify: a relay on another host, or a path like `/zen/v1` (pay-as-you-go
 * Zen, which has no plan allowance), must never be probed with the user's key.
 */
const GO_PATHS: ReadonlyArray<[RegExp, OpencodeGoRoute, OpencodeGoFamily]> = [
	[/^\/zen\/go\/v1$/, "zen", "unified"],
	[/^\/inference\/go\/v1$/, "inference", "unified"],
	[/^\/inference\/go\/openai\/v1$/, "inference", "openai"],
	[/^\/inference\/go\/anthropic\/v1$/, "inference", "anthropic"],
	[/^\/inference\/go\/google\/v1beta$/, "inference", "google"],
];

/** Whether a hostname is OpenCode's own gateway (apex or subdomain). */
export function isOpencodeHost(hostname: string): boolean {
	const host = hostname.toLowerCase();
	return host === "opencode.ai" || host.endsWith(".opencode.ai");
}

/**
 * Parse a configured base URL into its Go route, or null when it is not one.
 *
 * Strict on purpose — https only, default port, no credentials, query or hash —
 * because a match makes the server send the provider's API key to the derived
 * usage URL.
 */
export function parseOpencodeGoBaseUrl(baseUrl: string | undefined): OpencodeGoEndpoint | null {
	if (!baseUrl) return null;
	let url: URL;
	try {
		url = new URL(baseUrl);
	} catch {
		return null;
	}
	if (
		url.protocol !== "https:" ||
		!isOpencodeHost(url.hostname) ||
		url.port ||
		url.username ||
		url.password ||
		url.search ||
		url.hash
	) {
		return null;
	}
	const path = url.pathname.replace(/\/+$/, "");
	for (const [pattern, route, family] of GO_PATHS) {
		if (!pattern.test(path)) continue;
		const usage = new URL(url.origin);
		usage.pathname = route === "zen" ? "/zen/go/v1/usage" : "/inference/go/v1/usage";
		return { route, family, usageUrl: usage.toString() };
	}
	return null;
}

/** True when the base URL is any OpenCode Go plan root. */
export function isOpencodeGoBaseUrl(baseUrl: string | undefined): boolean {
	return parseOpencodeGoBaseUrl(baseUrl) !== null;
}
