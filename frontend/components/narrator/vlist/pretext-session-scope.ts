import { getToken, onTokenChange } from "@frontend/lib/api/client";
import { SESSION_START_CLAIM, SESSION_VERSION_CLAIM } from "@shared/session-auth";

/** Presentation caches are session-scoped, not authentication/authorization evidence. */
function sessionScope(token: string | null): string | null {
	if (!token) return null;
	try {
		const payload = JSON.parse(atob(token.split(".")[1].replace(/-/g, "+").replace(/_/g, "/")));
		if (typeof payload.sub === "string")
			return JSON.stringify([
				payload.sub,
				payload.role,
				payload[SESSION_START_CLAIM],
				payload[SESSION_VERSION_CLAIM],
			]);
	} catch {
		// Opaque/legacy tokens still isolate sessions, but cannot share a renewal.
	}
	return token;
}
function readScope(): string | null {
	try {
		return sessionScope(getToken());
	} catch {
		return null;
	}
}
let scope = readScope();
let epoch = 0;
const listeners = new Set<() => void>();
function updateScope(next: string | null) {
	if (next === scope) return;
	scope = next;
	epoch++;
	for (const listener of listeners) listener();
}
const unsubscribe = onTokenChange((token) => updateScope(sessionScope(token)));
export function pretextSessionEpoch(): number {
	updateScope(readScope());
	return epoch;
}
export function onPretextSessionChange(listener: () => void): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}
if (import.meta.hot) import.meta.hot.dispose(unsubscribe);
