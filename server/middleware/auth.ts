import {
	isOpaqueSessionToken,
	isSessionTokenVersionCurrent,
	isWithinAbsoluteSessionLimit,
	resolveSessionStart,
	SESSION_RENEWAL_HEADER,
	SESSION_START_CLAIM,
	SESSION_VERSION_CLAIM,
	shouldRenewSessionToken,
} from "@shared/session-auth";
import type { Context, Next } from "hono";
import { JwtTokenExpired } from "hono/utils/jwt/types";
import {
	extendSessionToken,
	isAuthenticSessionTokenIgnoringExpiry,
	type JwtPayload,
	upgradeLegacySessionToken,
	verifyToken,
} from "../lib/auth";
import { AppError, catalogError } from "../lib/errors";
import { eventBus } from "../lib/event-bus";
import { logger } from "../lib/logger";
// The two remaining raw AppErrors below are deliberate: `INVALID_SCOPE` is a programming
// error that never reaches a browser, and `INSUFFICIENT_SCOPE` is read by third-party
// OAuth clients rather than rendered in the UI, so neither needs a localized wording.
import { isManagerTokenAuthorized, readManagerToken } from "../lib/manager-token";
import { validateAccessToken } from "../lib/oauth-provider";
import { authSessionStore } from "../services/auth/store";

/** OAuth grant metadata attached to requests authenticated by an access token. */
export interface OAuthAuthContext {
	tokenId: string;
	clientId: string;
	oauthClientId: string;
	grantId: string | null;
	refreshFamilyId: string | null;
	expiresAt: string;
	scopes: string[];
}

/** The first-party session principal established by a session token. */
export interface SessionAuthPrincipal {
	type: "session";
	user: JwtPayload;
}

/** The external principal established by a NarraFork OAuth access token. */
export interface OAuthAuthPrincipal {
	type: "oauth";
	user: JwtPayload;
	oauth: OAuthAuthContext;
}

export type AuthPrincipal = SessionAuthPrincipal | OAuthAuthPrincipal;

// Extend Hono's context variables for type-safe user access
declare module "hono" {
	interface ContextVariableMap {
		auth: AuthPrincipal;
		user: JwtPayload;
		oauth: OAuthAuthContext;
	}
}

/**
 * Lightweight cache: verified user IDs are remembered for a short window
 * so we don't hit SQLite on every single request.
 */
interface VerifiedUser {
	at: number;
	tokenVersion: number;
}

const verifiedUsers = new Map<string, VerifiedUser>();
const VERIFY_TTL_MS = 60_000; // 1 minute

function getVerifiedUser(userId: string): VerifiedUser | undefined {
	const entry = verifiedUsers.get(userId);
	if (!entry) return undefined;
	if (Date.now() - entry.at > VERIFY_TTL_MS) {
		verifiedUsers.delete(userId);
		return undefined;
	}
	return entry;
}

/**
 * Drop cached existence + token-generation state.
 *
 * MUST be called by anything that bumps `users.token_version`, otherwise the revocation
 * only takes effect once the cache entry ages out (up to `VERIFY_TTL_MS` of continued
 * access for a credential that was supposed to be dead).
 */
export function invalidateUserCache(userId?: string) {
	eventBus.emit({ type: "auth:changed", ...(userId ? { userId } : {}) });
	if (userId) {
		verifiedUsers.delete(userId);
	} else {
		verifiedUsers.clear();
	}
}

function getStoredAuthPrincipal(c: Context): AuthPrincipal | undefined {
	const principal = c.get("auth") as AuthPrincipal | undefined;
	if (principal?.type === "session" || principal?.type === "oauth") return principal;
	return undefined;
}

/**
 * Read the principal established by auth middleware. The legacy `user`/`oauth`
 * variables remain supported for callers that inspect them directly, but the
 * discriminated `auth` value is the source of truth for boundary decisions.
 */
export function getAuthPrincipal(c: Context): AuthPrincipal | undefined {
	const principal = getStoredAuthPrincipal(c);
	if (principal) return principal;

	// Compatibility for middleware tests and older callers that populated the
	// legacy context variables themselves before invoking assertAdmin.
	const user = c.get("user") as JwtPayload | undefined;
	if (!user?.sub) return undefined;
	const oauth = c.get("oauth") as OAuthAuthContext | undefined;
	const inferred = oauth
		? { type: "oauth" as const, user, oauth }
		: { type: "session" as const, user };
	setAuthPrincipal(c, inferred);
	return inferred;
}

function setAuthPrincipal(c: Context, principal: AuthPrincipal): void {
	c.set("auth", principal);
	c.set("user", principal.user);
	if (principal.type === "oauth") c.set("oauth", principal.oauth);
}

/**
 * Whether a verification failure reports an `exp` in the past.
 *
 * Legacy session JWTs are verified with `hono/jwt`, which raises its own
 * `JwtTokenExpired`. The `name` check keeps this working across duplicated
 * module instances where `instanceof` fails.
 *
 * This alone does NOT mean the token was ever ours — `hono/jwt` evaluates `exp`
 * before the signature, so a forged token with a past `exp` lands here too. See
 * `isExpiredSessionJwt` for the signature confirmation.
 */
function reportsExpiredJwt(err: unknown): boolean {
	if (err instanceof JwtTokenExpired) return true;
	const name = (err as { name?: unknown } | null)?.name;
	return name === "JwtTokenExpired";
}

/**
 * Whether a verification failure means a legacy session JWT was well-formed,
 * correctly signed by this instance, and only past its `exp`.
 *
 * The signature is re-checked with `exp` disabled, because otherwise any forged
 * token carrying a past `exp` would be reported as `TOKEN_EXPIRED` — which both
 * confirms token shape to an unauthenticated caller and makes the frontend drop
 * whatever session it currently holds.
 *
 * Getting this right matters beyond the error code. An expired session must
 * short-circuit here; otherwise it falls through to the OAuth access-token
 * lookup, costing a pointless database round-trip and reporting the generic
 * `UNAUTHORIZED` instead of the actionable `TOKEN_EXPIRED`.
 */
async function isExpiredSessionJwt(err: unknown, token: string): Promise<boolean> {
	if (!reportsExpiredJwt(err)) return false;
	return await isAuthenticSessionTokenIgnoringExpiry(token);
}

/**
 * Paths whose responses are served with a `public` or otherwise shared
 * `Cache-Control`, and must therefore never carry a session credential.
 *
 * A replacement-token header attached to `public, max-age=31536000, immutable` is
 * written to the browser's disk cache alongside the asset and is reusable by any
 * intermediary cache (reverse proxy, CDN, corporate proxy). Worse, a later cache
 * hit would replay a days-old header and push a stale token back into
 * localStorage. Excluding these paths is preferred over forcing `no-store` on
 * the response, which would destroy asset caching for no benefit: these routes
 * are polled often enough that some other API call will carry the replacement
 * instead.
 *
 * Verified against every `Cache-Control` set behind the session gate:
 *  - /api/uploads/*            → public, max-age=31536000, immutable
 *  - /api/notification-sounds/:id → public, max-age=86400
 *  - /api/fs/preview          → private, max-age=60
 * `/api/fs/preview` is only `private`, but it is still a cacheable response
 * replayed without hitting the server, so it is excluded on the same grounds.
 */
const NON_RENEWABLE_PATH_PATTERNS: readonly RegExp[] = [
	/^\/api\/uploads(?:\/|$)/,
	/^\/api\/notification-sounds\/[^/]+(?:\/|$)/,
	/^\/api\/fs\/preview(?:\/|$)/,
];

function allowsSessionRenewalHeader(path: string): boolean {
	return !NON_RENEWABLE_PATH_PATTERNS.some((pattern) => pattern.test(path));
}

/**
 * Sliding renewal of an opaque session.
 *
 * Only inside the renewal window (the last 3 days of a 7-day expiry), so ordinary
 * requests write nothing; the next request after a renewal sees the new expiry and
 * stops renewing. The token itself never changes. The absolute ceiling is enforced
 * by `extendSessionToken`. Failures are swallowed: the current request is already
 * authenticated and must not fail just because renewal did.
 */
async function maybeExtendOpaqueSession(payload: JwtPayload): Promise<void> {
	if (!shouldRenewSessionToken(payload.exp, Math.floor(Date.now() / 1000))) return;
	try {
		await extendSessionToken(payload);
	} catch (error) {
		logger.warn("Failed to extend session", {
			userId: payload.sub,
			error: error instanceof Error ? error.name : "UnknownError",
		});
	}
}

/**
 * Replace a legacy session JWT with an opaque session token.
 *
 * Session JWTs are no longer issued, but those issued before the switch stay valid for
 * their remaining lifetime. Every authenticated request still carrying one gets an
 * opaque replacement in `SESSION_RENEWAL_HEADER`; the frontend stores it, and the next
 * request moves onto the opaque session. The replacement inherits the JWT's session
 * start, so the absolute ceiling still counts from the original login.
 *
 * Deliberately narrow:
 *  - only first-party session JWTs (OAuth access tokens have their own lifecycle);
 *  - never past the absolute ceiling anchored at the original login;
 *  - never on responses that are cacheable by the browser or a shared cache;
 *  - bound to the live token generation, so a bump that lands between the version
 *    check and this line cannot be dodged by the replacement;
 *  - failures are swallowed, since the current request is already authenticated
 *    and must not fail just because the upgrade did.
 *
 * Throws when the user row is gone, matching the existence check in
 * `authenticateRequest`: a deleted user must not be upgraded *or* served.
 */
async function maybeUpgradeLegacySession(
	c: Context,
	token: string,
	payload: JwtPayload,
): Promise<void> {
	if (!allowsSessionRenewalHeader(c.req.path)) return;
	const nowSeconds = Math.floor(Date.now() / 1000);
	const sessionStart = resolveSessionStart(payload[SESSION_START_CLAIM], nowSeconds);
	if (!isWithinAbsoluteSessionLimit(sessionStart, nowSeconds)) return;

	// Outside the try/catch below so a vanished user surfaces as a 401 instead of being
	// silently treated as "upgrade failed".
	const row = await authSessionStore.findSessionState(payload.sub);
	if (!row) {
		invalidateUserCache(payload.sub);
		throw catalogError("USER_GONE");
	}

	try {
		const replacement = await upgradeLegacySessionToken(
			token,
			row.id,
			sessionStart,
			row.tokenVersion,
		);
		c.header(SESSION_RENEWAL_HEADER, replacement);
	} catch (error) {
		// Never log the error message itself: hono's JWT errors embed the whole
		// token in `message` (`token (${token}) expired`).
		logger.warn("Failed to upgrade legacy session token", {
			userId: payload.sub,
			error: error instanceof Error ? error.name : "UnknownError",
		});
	}
}

function hasErrorName(err: unknown, name: string): boolean {
	return (err as { name?: unknown } | null)?.name === name;
}

/**
 * Authenticate once per request and return the explicit session/oauth principal.
 * Subsequent auth middleware in the same Hono chain reuses `c.get("auth")`
 * instead of re-verifying or replacing context state.
 */
async function authenticateRequest(c: Context): Promise<AuthPrincipal> {
	const existing = getStoredAuthPrincipal(c);
	if (existing) return existing;

	const header = c.req.header("Authorization");
	if (!header?.startsWith("Bearer ")) {
		throw catalogError("AUTH_REQUIRED");
	}

	const token = header.slice(7);
	let payload: JwtPayload;
	try {
		payload = await verifyToken(token);
	} catch (err) {
		// Error names rather than `instanceof`, to survive duplicated module instances.
		if (hasErrorName(err, "SessionTokenRevoked")) throw catalogError("SESSION_REVOKED");
		if (hasErrorName(err, "SessionTokenExpired")) throw catalogError("TOKEN_EXPIRED");
		if (isOpaqueSessionToken(token)) {
			// An unknown opaque token is not an OAuth token either. Anything other than
			// that verdict (a storage failure) must not read as "your session is gone".
			if (err instanceof AppError && err.statusCode === 401) throw catalogError("TOKEN_INVALID");
			throw err;
		}
		if (await isExpiredSessionJwt(err, token)) {
			throw catalogError("TOKEN_EXPIRED");
		}
		// Not a valid session token — fall through to OAuth access tokens issued by
		// NarraFork's own authorization server (third-party API clients).
		const oauthGrant = await validateAccessToken(token).catch(() => null);
		if (!oauthGrant) {
			throw catalogError("TOKEN_INVALID");
		}
		// OAuth access tokens are short-lived and revocable, so always resolve the
		// live user row instead of using the session existence cache.
		const row = await authSessionStore.findSessionState(oauthGrant.userId);
		if (!row) {
			throw catalogError("USER_GONE");
		}
		const principal: OAuthAuthPrincipal = {
			type: "oauth",
			user: { sub: row.id, role: row.role, iat: 0, exp: 0 },
			oauth: {
				tokenId: oauthGrant.tokenId,
				clientId: oauthGrant.clientId,
				oauthClientId: oauthGrant.oauthClientId,
				grantId: oauthGrant.grantId,
				refreshFamilyId: oauthGrant.refreshFamilyId,
				expiresAt: oauthGrant.expiresAt,
				scopes: oauthGrant.scopes,
			},
		};
		setAuthPrincipal(c, principal);
		return principal;
	}

	if (payload.sid) {
		// Opaque sessions were resolved against the live account row: existence, role and
		// token generation are already current, so neither the cache nor a second read applies.
		const principal: SessionAuthPrincipal = { type: "session", user: payload };
		setAuthPrincipal(c, principal);
		await maybeExtendOpaqueSession(payload);
		return principal;
	}

	// Legacy JWT. Confirm the user still exists (handles DB wipe, user deletion) and that
	// this token's generation has not been revoked. Both facts come from the same row and
	// share one cache entry, so token-version enforcement costs no extra query — the
	// revocation window is the existing 60s TTL, which anything bumping the counter
	// shortens to zero by invalidating the entry (see invalidateUserCache).
	let verified = getVerifiedUser(payload.sub);
	if (!verified) {
		const row = await authSessionStore.findSessionState(payload.sub);
		if (!row) {
			throw catalogError("USER_GONE");
		}
		verified = { at: Date.now(), tokenVersion: row.tokenVersion };
		verifiedUsers.set(payload.sub, verified);
	}
	if (!isSessionTokenVersionCurrent(payload[SESSION_VERSION_CLAIM], verified.tokenVersion)) {
		// Reported as expiry, not as a generic failure: the credential really is finished, and
		// TOKEN_EXPIRED is the code the frontend acts on by clearing its stored token and
		// sending the user back to login.
		throw catalogError("SESSION_REVOKED");
	}

	const principal: SessionAuthPrincipal = { type: "session", user: payload };
	setAuthPrincipal(c, principal);
	await maybeUpgradeLegacySession(c, token, payload);
	return principal;
}

/**
 * Authenticate either a first-party session token or an OAuth access token.
 * This dual-mode helper is reserved for explicitly external/legacy paths and tests;
 * ordinary application routes use requireSessionAuth.
 */
export async function requireAuth(c: Context, next: Next) {
	await authenticateRequest(c);
	await next();
}

/** Explicit name for callers that intentionally accept either external mode. */
export async function requireExternalAuth(c: Context, next: Next) {
	await authenticateRequest(c);
	await next();
}

/** Require an OAuth access token and reject first-party session tokens. */
export async function requireOAuthAuth(c: Context, next: Next) {
	const principal = await authenticateRequest(c);
	if (principal.type !== "oauth") {
		throw catalogError("OAUTH_TOKEN_REQUIRED");
	}
	await next();
}

/**
 * Require a first-party session token. OAuth access tokens authenticate normal API
 * requests through requireExternalAuth/requireAuth, but they must never satisfy
 * user-consent flows.
 */
export async function requireSessionAuth(c: Context, next: Next) {
	const principal = await authenticateRequest(c);
	if (principal.type !== "session" || c.get("oauth")) {
		throw catalogError("SESSION_REQUIRED");
	}
	await next();
}

export function assertAdmin(c: Context): void {
	const principal = getAuthPrincipal(c);
	if (principal?.type === "oauth" || c.get("oauth")) {
		// OAuth grants are intentionally never accepted for administrative actions,
		// even when the live user row currently has the admin role.
		throw catalogError("ADMIN_REQUIRES_SESSION");
	}
	const user = principal?.user ?? (c.get("user") as JwtPayload | undefined);
	if (!user) {
		throw catalogError("AUTH_REQUIRED");
	}
	if (user.role !== "admin") {
		throw catalogError("ADMIN_REQUIRED");
	}
}

export async function requireAdmin(c: Context, next: Next) {
	assertAdmin(c);
	await next();
}

/**
 * 管理员会话，或管理器面板持有的本机令牌。
 *
 * 面板是独立进程、没有用户会话令牌。令牌由面板生成，只放进它拉起的服务进程的环境变量。
 * 对端必须是回环 socket（`peerIp`，不是解析过转发头的 clientIp），并且 Bearer 与令牌一致。
 * 终端里手动启动的服务没有这个环境变量，因此没有旁路。
 */
export async function requireAdminOrManagerToken(c: Context, next: Next) {
	const env = c.env as { peerIp?: string } | undefined;
	if (
		isManagerTokenAuthorized({
			peerIp: env?.peerIp,
			authorization: c.req.header("authorization"),
			expectedToken: readManagerToken(),
		})
	) {
		await next();
		return;
	}
	await authenticateRequest(c);
	assertAdmin(c);
	await next();
}

/**
 * Gate a route on an OAuth scope. Requests authenticated by a session token
 * (first-party UI) always pass — scopes only constrain third-party OAuth
 * access tokens. The auth lookup is cached, so this is safe after requireAuth
 * and can also be used as a standalone boundary.
 */
export function requireExternalScope(scope: string) {
	const requiredScope = scope.trim();
	if (!requiredScope) {
		throw new AppError("Required scope must not be empty", 500, "INVALID_SCOPE");
	}

	return async (c: Context, next: Next) => {
		const principal = await authenticateRequest(c);
		if (principal.type === "session") {
			await next();
			return;
		}
		if (!principal.oauth.scopes.includes(requiredScope)) {
			throw new AppError(`Missing required scope: ${requiredScope}`, 403, "INSUFFICIENT_SCOPE");
		}
		await next();
	};
}

/** Backwards-compatible name used by the existing provision routes. */
export function requireScope(scope: string) {
	return requireExternalScope(scope);
}
