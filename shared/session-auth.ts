/**
 * Shared contract between the server auth middleware and the frontend API client
 * for first-party session tokens.
 *
 * Session tokens are opaque random strings prefixed with `SESSION_TOKEN_PREFIX`. The
 * server stores only their SHA-256 in `auth_sessions`, so a session cannot be forged
 * from any instance secret, and a single session can be revoked on its own.
 *
 * Session JWTs issued before opaque tokens existed are still accepted for one token
 * lifetime. The first authenticated request carrying one gets an opaque replacement in
 * `SESSION_RENEWAL_HEADER`; the client swaps it in transparently. Opaque sessions slide
 * their expiry server-side and never need a replacement token.
 *
 * Not every 401 means "your session is gone". Second-factor verification failures,
 * OAuth-only boundaries and consent-page login hints all answer 401 while the caller's
 * session is still perfectly valid. Clearing the stored token on those responses logs
 * the user out for no reason, so the client only discards a token when the error code
 * is in `SESSION_INVALID_ERROR_CODES`.
 */

/** Prefix that marks a first-party opaque session token. */
export const SESSION_TOKEN_PREFIX = "nfs_";

/** Whether a bearer string is an opaque first-party session token (by shape only). */
export function isOpaqueSessionToken(token: string): boolean {
	return token.startsWith(SESSION_TOKEN_PREFIX);
}

/**
 * Error codes that mean the presented session credential itself is no longer
 * usable. Only these justify dropping the locally stored token.
 */
export const SESSION_INVALID_ERROR_CODES = [
	// Session expired past its lifetime, or was revoked.
	"TOKEN_EXPIRED",
	// Missing/invalid Authorization header, unverifiable token, or the user row
	// backing the token no longer exists.
	"UNAUTHORIZED",
	// A session JWT was required but the request presented an OAuth access token,
	// so no usable first-party session exists for this client.
	"SESSION_REQUIRED",
] as const;

export type SessionInvalidErrorCode = (typeof SESSION_INVALID_ERROR_CODES)[number];

const SESSION_INVALID_ERROR_CODE_SET: ReadonlySet<string> = new Set(SESSION_INVALID_ERROR_CODES);

/**
 * Whether a 401 response body indicates the stored session token is dead.
 *
 * A 401 without a recognizable `code` is treated as session loss: legacy routes
 * and non-JSON gateway responses (e.g. a reverse proxy rejecting the request)
 * carry no code, and for those the safest reading of 401 is still "not
 * authenticated". Codes we do recognize but that are not session failures
 * (`MFA_CODE_INVALID`, `PASSKEY_AUTH_FAILED`, `OAUTH_REQUIRED`, …) keep the
 * session intact.
 */
export function isSessionInvalidResponse(
	data: Record<string, unknown> | null | undefined,
): boolean {
	const code = data?.code;
	if (typeof code !== "string" || !code.trim()) return true;
	return SESSION_INVALID_ERROR_CODE_SET.has(code);
}

/** Response header carrying the opaque replacement for a legacy session JWT. */
export const SESSION_RENEWAL_HEADER = "X-NarraFork-Session-Token";

/** Lifetime of a session without activity. */
export const SESSION_TOKEN_TTL_SECONDS = 7 * 24 * 60 * 60;

/**
 * Renew a session once its remaining lifetime drops below this threshold.
 * At 3 days against a 7-day TTL, any user who touches the app at least once a
 * week keeps a valid session indefinitely, while an abandoned session still
 * expires within the original window.
 */
export const SESSION_RENEWAL_THRESHOLD_SECONDS = 3 * 24 * 60 * 60;

/**
 * Absolute ceiling on a session, measured from the original login instant
 * (`auth_sessions.session_start`, or `SESSION_START_CLAIM` on a legacy JWT).
 *
 * Sliding renewal alone turns a 7-day session into an unbounded bearer credential:
 * any request inside the renewal window extends it, so a stolen token stays alive
 * forever as long as it is touched occasionally. This ceiling caps
 * the whole chain — after 30 days the user must authenticate again, no matter
 * how active the session was.
 */
export const ABSOLUTE_SESSION_MAX_SECONDS = 30 * 24 * 60 * 60;

/**
 * Claim carrying the session-start time (unix seconds) of the original login.
 * Legacy JWTs carry it; the verified payload of an opaque session carries the
 * stored value under the same name. It must never be reset by a renewal,
 * otherwise the absolute ceiling could be slid forward forever.
 */
export const SESSION_START_CLAIM = "sst";

/**
 * Claim carrying `users.token_version` as it stood when the session was issued.
 *
 * Bumping the column leaves every
 * outstanding token behind, so a password change or an explicit "sign out everywhere"
 * takes effect on the next request instead of waiting out the token's lifetime.
 */
export const SESSION_VERSION_CLAIM = "sv";

/**
 * Whether a token's generation claim still matches the user's current one.
 *
 * Both sides default to 0 so the column's default and a token predating the claim
 * agree: existing sessions survive the deploy and only stop working once that user's
 * counter is actually bumped. A non-numeric claim is treated as stale rather than
 * current — a malformed credential must fail closed.
 */
export function isSessionTokenVersionCurrent(claim: unknown, current: number): boolean {
	const presented = claim === undefined ? 0 : claim;
	if (typeof presented !== "number" || !Number.isFinite(presented)) return false;
	return presented >= current;
}

/** Whether a token with this `exp` (unix seconds) should be re-issued now. */
export function shouldRenewSessionToken(exp: number, nowSeconds: number): boolean {
	if (!Number.isFinite(exp) || exp <= 0) return false;
	const remaining = exp - nowSeconds;
	// Already expired tokens never reach this point (verification rejects them),
	// and a token whose remaining life exceeds the threshold needs nothing.
	return remaining > 0 && remaining < SESSION_RENEWAL_THRESHOLD_SECONDS;
}

/**
 * Resolve the session-start anchor to use when renewing a token.
 *
 * Tokens issued before the absolute ceiling existed carry no `sst`. Rejecting
 * them outright would log every current user out on deploy, so a missing anchor
 * is treated as "the ceiling starts now": such a session gets at most one more
 * full 30-day window instead of an immediate logout. The trade-off is that a
 * token stolen before this change can still be extended for one final window —
 * acceptable because the alternative is a forced mass re-login, and every token
 * minted from here on is anchored.
 */
export function resolveSessionStart(sessionStart: unknown, nowSeconds: number): number {
	return typeof sessionStart === "number" &&
		Number.isFinite(sessionStart) &&
		sessionStart > 0 &&
		sessionStart <= nowSeconds
		? sessionStart
		: nowSeconds;
}

/** Whether the renewal chain anchored at `sessionStart` is still inside the ceiling. */
export function isWithinAbsoluteSessionLimit(sessionStart: number, nowSeconds: number): boolean {
	return nowSeconds - sessionStart < ABSOLUTE_SESSION_MAX_SECONDS;
}
