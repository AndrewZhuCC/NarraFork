/**
 * "Reveal in Explorer" via `narrafork://` protocol activation (Windows).
 *
 * The server is a background process, so an Explorer window it opens is usually denied the
 * foreground. Instead the browser — the foreground process, acting on the user's click — opens
 * `narrafork://reveal/<token>`; Windows starts this same exe as a short-lived protocol client,
 * which claims the path from the running server and opens Explorer with foreground rights.
 *
 * The URI never carries a path: any web page can navigate to `narrafork://`, so it only carries a
 * one-time token the server issued for a path it already validated. Where to reach the server is
 * baked into the registered command line (see reveal-protocol-registration.ts), not the URI, so a
 * page cannot point the client at a server of its choosing.
 *
 * Kept light: the protocol client imports it before anything else loads.
 */

import { generateId } from "./id";

export const REVEAL_PROTOCOL_SCHEME = "narrafork";
/** argv flag carrying the origin of the server that registered the handler. */
export const REVEAL_SERVER_FLAG = "--reveal-server=";
/** Public endpoint (no session auth: the token is the credential) the client claims a path from. */
export const REVEAL_CLAIM_PATH = "/api/reveal-protocol";

const URI_PREFIX = `${REVEAL_PROTOCOL_SCHEME}://reveal/`;
const TOKEN_TTL_MS = 10_000;

export function buildRevealUri(token: string): string {
	return `${URI_PREFIX}${token}`;
}

/** The protocol activation in argv, if this process was started by one. */
export function parseRevealActivation(argv: string[]): { token: string; server: string } | null {
	const uri = argv.find((arg) => arg.toLowerCase().startsWith(`${REVEAL_PROTOCOL_SCHEME}://`));
	if (!uri) return null;
	const token = uri.startsWith(URI_PREFIX) ? uri.slice(URI_PREFIX.length).replace(/\/$/, "") : "";
	const flag = argv.find((arg) => arg.startsWith(REVEAL_SERVER_FLAG));
	return { token, server: flag?.slice(REVEAL_SERVER_FLAG.length) ?? "" };
}

const pending = new Map<string, { path: string; expiresAt: number }>();

/** Authorize one reveal of `path`; the token is good for a single claim within a few seconds. */
export function issueRevealToken(path: string, now = Date.now()): string {
	for (const [token, entry] of pending) {
		if (entry.expiresAt <= now) pending.delete(token);
	}
	const token = generateId();
	pending.set(token, { path, expiresAt: now + TOKEN_TTL_MS });
	return token;
}

/** Consume a token: its path when it is known and unexpired, otherwise null. */
export function claimRevealToken(token: string, now = Date.now()): string | null {
	const entry = pending.get(token);
	pending.delete(token);
	return entry && entry.expiresAt > now ? entry.path : null;
}
