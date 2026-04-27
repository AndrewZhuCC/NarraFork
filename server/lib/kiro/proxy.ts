// Proxy resolver — decoupled from index.ts to avoid circular imports.

/** Callback to resolve the current proxy URL at runtime (registered by adapter). */
let proxyResolver: (() => string | undefined) | null = null;

/** Register a callback that returns the current proxy URL. */
export function registerProxyResolver(resolver: () => string | undefined): void {
	proxyResolver = resolver;
}

/** Get the current proxy URL (if any). */
export function getProxy(): string | undefined {
	return proxyResolver?.();
}

/**
 * Proxy-aware fetch wrapper. Uses Bun's native `proxy` option when a proxy is configured.
 * Centralised here so all kiro modules share a single implementation.
 */
export function pfetch(input: string | URL | Request, init?: RequestInit): Promise<Response> {
	const proxy = getProxy();
	if (proxy) {
		// Bun's fetch natively supports `proxy` — cast to satisfy the standard RequestInit type.
		return fetch(input, { ...init, proxy } as RequestInit);
	}
	return fetch(input, init);
}
