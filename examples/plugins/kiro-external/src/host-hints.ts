/**
 * Consumption of `hostHints`, the per-call advice the host attaches to provider requests.
 *
 * ## Why this is its own module
 *
 * `server.ts` calls `listen()` and writes a `hello` frame at import time, so importing it
 * from a test would take over stdio. Keeping this logic separate means the real
 * implementation can be tested directly instead of a copy of it re-declared inside a test,
 * which would keep passing after the original changed.
 *
 * ## Why the proxy lives in a module-level variable
 *
 * The host resolves one proxy from its own settings (`resolveProviderHostHints` in
 * `plugin-platform-services.ts`) and sends the same value to every call, so concurrent
 * requests race only to write identical strings. `registerProxyResolver` takes a callback
 * that `pfetch()` invokes synchronously during a request, so the value read is the one the
 * current call just set. If the host ever routes per-request proxies this would need
 * AsyncLocalStorage; it does not, so it does not.
 */

import { registerProxyResolver } from "@server/lib/kiro/proxy";

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

let currentProxyUrl: string | undefined;

/** Wire the Kiro client layer's proxy lookup to whatever the host last told us. */
export function installProxyResolver(): void {
	registerProxyResolver(() => currentProxyUrl);
}

/** The proxy currently in effect. Exposed for assertions; production code uses `pfetch`. */
export function activeProxyUrl(): string | undefined {
	return currentProxyUrl;
}

/**
 * Apply the `hostHints` block of a provider request.
 *
 * SECURITY: `outbound.proxyUrl` may carry credentials in its userinfo component. It is
 * never logged and never leaves this module except through the registered resolver.
 */
export function applyHostHints(params: unknown): void {
	if (!isRecord(params)) return;
	const hostHints = params.hostHints;
	// An absent `hostHints` means the host said nothing on this call, which is not the same
	// as "no proxy". Leave the last known value alone rather than guessing.
	if (!isRecord(hostHints)) return;
	const outbound = hostHints.outbound;
	// A present `hostHints` carrying no proxy is the host stating it has no proxy policy, so
	// the previous value must be cleared. Assigning only on a non-empty string would leave a
	// deleted proxy in effect for the rest of the process lifetime — the plugin would keep
	// routing upstream traffic through a proxy the user already removed.
	currentProxyUrl =
		isRecord(outbound) && typeof outbound.proxyUrl === "string" && outbound.proxyUrl
			? outbound.proxyUrl
			: undefined;
	// `concurrency.maxConcurrentUpstream` is deliberately not consumed: the host does not
	// send it, because the only value it could compute is the one this plugin declared for
	// itself (verified — `resolveProviderHostHints` populates `outbound` only). Reading it
	// into an unused variable would be dead code that looks load-bearing.
}

/** Test seam: forget the applied proxy so cases do not leak into one another. */
export function resetHostHints(): void {
	currentProxyUrl = undefined;
}
