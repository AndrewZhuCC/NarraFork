import { type Locale, normalizeLocale } from "@shared/i18n-locales";
import {
	ABSOLUTE_SESSION_MAX_SECONDS,
	isOpaqueSessionToken,
	isSessionTokenVersionCurrent,
	SESSION_START_CLAIM,
	SESSION_TOKEN_TTL_SECONDS,
	SESSION_VERSION_CLAIM,
} from "@shared/session-auth";
import { verify } from "hono/jwt";
import { authSessionStore } from "../services/auth/store";
import { registrationAccountStore as accountStore } from "../services/registration/store";
import { authAttemptLimiter, fingerprintAuthIdentifier } from "./auth-attempt-limiter";
import { randomAvatarColor } from "./avatar-colors";
import { AppError, RateLimitError } from "./errors";
import { eventBus } from "./event-bus";
import { hotTimer } from "./hot-safe";
import { generateId } from "./id";
import { logger } from "./logger";
import { issueMfaToken } from "./mfa";
import { generateSessionToken, hashSessionToken } from "./session-token-hash";
import { saveSettings, settings } from "./settings";

/** Read JWT secret lazily so it picks up the auto-generated value even when
 *  the module is imported before settings finishes initialization. */
function getJwtSecret(): string {
	return settings.auth.jwtSecret;
}

// A fixed cost-10 bcrypt hash used only to equalize the unknown-user path with
// a normal password failure. The plaintext is intentionally public and is not
// an account credential.
const INVALID_LOGIN_PADDING_HASH = "$2b$10$aF/evsAjCJZn2KANblVddeLsw19IUoALQbTSxTaCJeNxztOQHrWWm";

/** The verified claims of a first-party session, whichever token kind carried them. */
export interface JwtPayload {
	sub: string;
	role: "admin" | "user";
	iat: number;
	exp: number;
	/**
	 * Session start (unix seconds) of the original login. Optional because legacy JWTs
	 * issued before the absolute session ceiling existed do not carry it; see
	 * `resolveSessionStart` for how those are anchored.
	 */
	[SESSION_START_CLAIM]?: number;
	/**
	 * `users.token_version` at issuance. Optional for the same reason as the anchor above:
	 * legacy JWTs predating the column do not carry it. A missing value reads as 0 (see
	 * `isSessionTokenVersionCurrent`).
	 */
	[SESSION_VERSION_CLAIM]?: number;
	/** `auth_sessions.id` of an opaque session. Absent on a legacy JWT. */
	sid?: string;
}

/** An opaque session token that is known but past its expiry or absolute ceiling. */
export class SessionTokenExpiredError extends Error {
	override name = "SessionTokenExpired";
}

/** An opaque session token that was revoked, alone or by a token-generation bump. */
export class SessionTokenRevokedError extends Error {
	override name = "SessionTokenRevoked";
}

function nowSeconds(): number {
	return Math.floor(Date.now() / 1000);
}

/** The expiry a session may slide to at `now`: one TTL ahead, never past its ceiling. */
function slidExpiry(sessionStart: number, now: number): number {
	return Math.min(now + SESSION_TOKEN_TTL_SECONDS, sessionStart + ABSOLUTE_SESSION_MAX_SECONDS);
}

async function issueSession(
	userId: string,
	tokenVersion: number,
	sessionStart: number,
): Promise<string> {
	const now = nowSeconds();
	const token = generateSessionToken();
	await authSessionStore.createSession({
		id: generateId(),
		userId,
		tokenHash: hashSessionToken(token),
		tokenVersion,
		sessionStart,
		createdAt: now,
		expiresAt: slidExpiry(sessionStart, now),
	});
	return token;
}

/**
 * Issue a session token for a *fresh login*: the absolute-session anchor starts now.
 * `tokenVersion` must be the user's current `users.token_version`, or the session is
 * born revoked.
 */
export async function createToken(userId: string, tokenVersion = 0): Promise<string> {
	return issueSession(userId, tokenVersion, nowSeconds());
}

/** Recently issued replacements, keyed by the legacy JWT's hash (see below). */
const legacyUpgrades = new Map<string, { token: Promise<string>; at: number }>();
const LEGACY_UPGRADE_REUSE_MS = 5 * 60 * 1000;

/**
 * Replace a verified legacy session JWT with an opaque session, keeping its original
 * session-start anchor so the upgrade cannot extend the absolute ceiling.
 *
 * A page fires many requests in parallel with the same JWT before the first replacement
 * lands, and each would otherwise mint (and leave behind) its own session. Requests that
 * present the same JWT within a few minutes share one replacement instead.
 */
export async function upgradeLegacySessionToken(
	legacyToken: string,
	userId: string,
	sessionStart: number,
	tokenVersion: number,
): Promise<string> {
	const key = hashSessionToken(legacyToken);
	const now = Date.now();
	for (const [entryKey, entry] of legacyUpgrades) {
		if (now - entry.at > LEGACY_UPGRADE_REUSE_MS) legacyUpgrades.delete(entryKey);
	}
	const existing = legacyUpgrades.get(key);
	if (existing) return existing.token;
	const token = issueSession(userId, tokenVersion, sessionStart);
	legacyUpgrades.set(key, { token, at: now });
	token.catch(() => legacyUpgrades.delete(key));
	return token;
}

/**
 * Slide an opaque session's expiry forward. Callers decide *when* (only inside the
 * renewal window, so ordinary requests write nothing); this decides how far.
 */
export async function extendSessionToken(payload: JwtPayload): Promise<void> {
	const sessionStart = payload[SESSION_START_CLAIM];
	if (!payload.sid || sessionStart === undefined) return;
	const expiresAt = slidExpiry(sessionStart, nowSeconds());
	if (expiresAt <= payload.exp) return;
	await authSessionStore.extendSession(payload.sid, expiresAt);
}

/** Revoke the one session a request authenticated with (logout). No-op for legacy JWTs. */
export async function revokeSessionToken(payload: JwtPayload): Promise<void> {
	if (!payload.sid) return;
	await authSessionStore.revokeSession(payload.sid, nowSeconds());
	eventBus.emit({ type: "auth:changed", userId: payload.sub });
}

/**
 * Bump a user's token generation, invalidating every session they hold.
 *
 * Returns the new value so a caller that is also issuing a replacement token
 * (a self-service password change, say) can bind it to the bumped counter
 * instead of locking itself out. The increment itself is one atomic statement
 * owned by the session store — this layer holds no transaction handle and
 * names no table, so a second backend implements the same capability.
 */
export async function revokeUserSessions(userId: string): Promise<number> {
	const version = await authSessionStore.bumpTokenVersion(userId);
	eventBus.emit({ type: "auth:changed", userId });
	return version;
}

/**
 * Reject payloads that are signed correctly but are not usable session
 * credentials. `hono/jwt` skips `exp`/`sub` when they are absent, so a token
 * without them would otherwise be an unexpiring credential that never even
 * enters the renewal (and therefore ceiling) logic.
 */
function assertSessionPayload(payload: JwtPayload & { stage?: string }): void {
	// Reject intermediate tokens (e.g. MFA challenge tokens carry `stage`).
	// Only fully-authenticated session tokens are accepted as credentials.
	if (payload.stage) {
		throw new AppError("Invalid token", 401, "UNAUTHORIZED");
	}
	if (typeof payload.sub !== "string" || !payload.sub.trim()) {
		throw new AppError("Invalid token", 401, "UNAUTHORIZED");
	}
	if (typeof payload.exp !== "number" || !Number.isFinite(payload.exp)) {
		throw new AppError("Invalid token", 401, "UNAUTHORIZED");
	}
	// Only opaque sessions carry `sid`; a JWT claiming one is not ours.
	if (payload.sid !== undefined) {
		throw new AppError("Invalid token", 401, "UNAUTHORIZED");
	}
}

/**
 * Resolve an opaque session token. Unlike a JWT this observes the live database, so the
 * role is the account's current role and revocation is immediate.
 */
async function verifyOpaqueSessionToken(token: string): Promise<JwtPayload> {
	const session = await authSessionStore.findSessionByTokenHash(hashSessionToken(token));
	if (!session) {
		throw new AppError("Invalid token", 401, "UNAUTHORIZED");
	}
	if (
		session.revokedAt !== null ||
		!isSessionTokenVersionCurrent(session.sessionTokenVersion, session.userTokenVersion)
	) {
		throw new SessionTokenRevokedError("Session revoked");
	}
	const now = nowSeconds();
	if (session.expiresAt <= now || now - session.sessionStart >= ABSOLUTE_SESSION_MAX_SECONDS) {
		throw new SessionTokenExpiredError("Session expired");
	}
	return {
		sub: session.userId,
		role: session.role,
		iat: session.createdAt,
		exp: session.expiresAt,
		[SESSION_START_CLAIM]: session.sessionStart,
		[SESSION_VERSION_CLAIM]: session.sessionTokenVersion,
		sid: session.id,
	};
}

/**
 * Verify a first-party session token: an opaque session, or a legacy HS256 session JWT
 * still inside its lifetime. Legacy JWTs are no longer issued; remove the JWT branch
 * once every one of them has expired (one `SESSION_TOKEN_TTL_SECONDS` after release).
 */
export async function verifyToken(token: string): Promise<JwtPayload> {
	if (isOpaqueSessionToken(token)) return verifyOpaqueSessionToken(token);
	const payload = (await verify(token, getJwtSecret(), "HS256")) as unknown as JwtPayload & {
		stage?: string;
	};
	assertSessionPayload(payload);
	return payload;
}

/**
 * Verify a legacy session JWT's signature and shape while ignoring `exp`.
 *
 * `hono/jwt` checks `exp` *before* the signature, so an arbitrarily forged token
 * with a past `exp` raises `JwtTokenExpired`. Reporting that as "your session
 * expired" would confirm to an unauthenticated caller that the token was once
 * ours and would make the client drop its stored token. This re-verification
 * tells a genuinely expired session apart from a forgery.
 */
export async function isAuthenticSessionTokenIgnoringExpiry(token: string): Promise<boolean> {
	if (isOpaqueSessionToken(token)) return false;
	try {
		const payload = (await verify(token, getJwtSecret(), {
			alg: "HS256",
			exp: false,
		})) as unknown as JwtPayload & { stage?: string };
		assertSessionPayload(payload);
		return true;
	} catch {
		return false;
	}
}

/** Keep expired/revoked rows around this long, so a late request still reads "expired". */
const STALE_SESSION_RETENTION_SECONDS = 24 * 60 * 60;
const STALE_SESSION_BATCH = 500;
const STALE_SESSION_MAX_BATCHES = 20;
const SESSION_CLEANUP_INTERVAL_MS = 60 * 60 * 1000;
const SESSION_CLEANUP_TIMER_KEY = "narrafork.authSessionCleanupTimer";

/** Delete stale sessions in bounded batches, yielding between them. */
export async function pruneStaleSessions(): Promise<number> {
	const before = nowSeconds() - STALE_SESSION_RETENTION_SECONDS;
	let total = 0;
	try {
		for (let batch = 0; batch < STALE_SESSION_MAX_BATCHES; batch++) {
			const deleted = await authSessionStore.deleteStaleSessions(before, STALE_SESSION_BATCH);
			total += deleted;
			if (deleted < STALE_SESSION_BATCH) break;
			await Bun.sleep(0);
		}
	} catch (error) {
		logger.warn("Failed to prune stale auth sessions", {
			error: error instanceof Error ? error.message : String(error),
		});
	}
	return total;
}

/** Sweep on startup, then hourly. `hotTimer` keeps --hot reloads from stacking intervals. */
export function startSessionCleanupTimer(): ReturnType<typeof setInterval> {
	void pruneStaleSessions();
	return hotTimer(SESSION_CLEANUP_TIMER_KEY, () =>
		setInterval(() => {
			void pruneStaleSessions();
		}, SESSION_CLEANUP_INTERVAL_MS),
	);
}

export interface RegisterUserInput {
	username: string;
	password: string;
	language?: string;
	/**
	 * A single-use registration code. When present it authorizes the signup on its
	 * own, independently of `auth.registrationOpen`, and decides the new account's
	 * role. That is the whole point of issuing one: an administrator can keep public
	 * registration closed and still let a specific person create their own account.
	 */
	code?: string;
}

export async function registerUser(input: RegisterUserInput) {
	const { username, password, language } = input;
	const code = input.code?.trim() || undefined;
	const isFirstUser = (await accountStore.countAccounts()) === 0;

	if (!isFirstUser && !code && !settings.auth.registrationOpen) {
		throw new AppError("Registration is closed", 403, "REGISTRATION_CLOSED");
	}

	if (await accountStore.isUsernameTaken(username)) {
		throw new AppError("Username already taken", 409, "USERNAME_TAKEN");
	}

	// Hashing is deliberately outside the atomic section: it is ~100ms of CPU, and holding a
	// transaction open across it would serialize unrelated writes for no benefit.
	const passwordHash = await Bun.password.hash(password, {
		algorithm: "bcrypt",
		cost: 10,
	});
	const resolvedLang = normalizeLocale(language);

	// One indivisible business fact: validate the invitation, create the account, burn the
	// invitation. Split across writes it would either spend an invitation on a signup that
	// then failed, or create the account while leaving the code reusable. `accountStore`
	// guarantees the all-or-nothing property and owns how — this layer holds no transaction
	// handle and names no table, so a second backend implements the same capability rather
	// than reproducing SQLite's transaction shape.
	//
	// The first user is always an administrator regardless of any code (the bootstrap account
	// must be able to administer the instance), which is why `invitationCode` is dropped in
	// that case instead of being resolved and overridden.
	let user: RegisteredAccountShape;
	try {
		const created = await accountStore.createAccount({
			userId: generateId(),
			username,
			passwordHash,
			avatarColor: randomAvatarColor(),
			language: resolvedLang,
			defaultRole: isFirstUser ? "admin" : "user",
			invitationCode: isFirstUser ? undefined : code,
			nowIso: new Date().toISOString(),
		});
		user = created.account;
	} catch (error) {
		throw await mapAccountCreationFailure(error, username);
	}

	// The first account is the instance administrator, created through an
	// unauthenticated endpoint that has to stay open while no user exists. Once it
	// does exist that endpoint is a standing signup hole, so close registration
	// immediately instead of relying on the admin to flip the switch later. An
	// admin can reopen it from settings.
	if (isFirstUser) {
		closeRegistrationAfterFirstAdmin();
	}

	const token = await createToken(user.id);
	return { user, token, language: resolvedLang };
}

/** The account shape `registerUser` returns, taken from the store contract. */
type RegisteredAccountShape = Awaited<ReturnType<typeof accountStore.createAccount>>["account"];

/**
 * Give a failed account creation its domain error identity.
 *
 * The pre-check above (`isUsernameTaken`) and the account insert are not one atomic
 * observation: two concurrent registrations of the SAME username can both pass the
 * check, and then exactly one of them wins the insert — on PostgreSQL the loser
 * surfaces as a uniqueness conflict from inside the atomic section (a
 * `WriteConflictError` in port vocabulary), on SQLite as the engine's own constraint
 * error. Either way the domain fact is the same one the pre-check reports, so it gets
 * the same answer: `USERNAME_TAKEN` (409), on both backends, never a raw 500.
 *
 * The decision is made by RE-READING the domain state (`isUsernameTaken`) rather than
 * by recognizing driver error text — this layer holds no dialect. A failure whose
 * cause is not a lost username race (the invitation rejection codes, storage faults)
 * propagates unchanged, which is what keeps a storage failure from being reshaped
 * into a policy error. If the re-read itself fails, the original error wins: it is
 * the better evidence.
 */
async function mapAccountCreationFailure(error: unknown, username: string): Promise<unknown> {
	if (error instanceof AppError) return error;
	try {
		if (await accountStore.isUsernameTaken(username)) {
			return new AppError("Username already taken", 409, "USERNAME_TAKEN");
		}
	} catch {
		// The re-read failed too; the original rejection is the one to surface.
	}
	return error;
}

/**
 * Persist `auth.registrationOpen = false` right after the bootstrap admin exists.
 *
 * Failures are logged, not thrown: the account is already committed, and failing
 * the request would leave the caller without the token for an account that does
 * exist. The in-memory flag is flipped first so a failed write still denies
 * registration for the life of the process (it reverts on restart).
 */
function closeRegistrationAfterFirstAdmin(): void {
	if (!settings.auth.registrationOpen) return;
	try {
		settings.auth.registrationOpen = false;
		saveSettings(settings);
		logger.info("Registration closed automatically after the first administrator was created");
	} catch (error) {
		logger.error("Failed to close registration after first administrator", { error });
	}
}

export interface LoginSuccess {
	user: {
		id: string;
		username: string;
		role: string;
		avatarColor: string | null;
		avatarImageId: string | null;
		createdAt: string;
	};
	token: string;
	language: Locale;
}

export interface MfaChallenge {
	mfaRequired: true;
	mfaToken: string;
	methods: Array<"totp" | "backup_code" | "passkey">;
}

/**
 * Build a fully-authenticated session result (session token + profile + language)
 * for a user id. Shared by password login (no second factor) and the MFA
 * verify step (after the second factor is proven).
 */
export async function buildSessionResult(userId: string): Promise<LoginSuccess> {
	const profile = await authSessionStore.findSessionProfile(userId);
	if (!profile) {
		throw new AppError("User not found", 404, "NOT_FOUND");
	}
	// tokenVersion stays out of the returned user object: it is an internal revocation
	// counter, not profile data the frontend has any use for.
	const { tokenVersion, language: preferredLanguage, gitUsername, gitEmail, ...user } = profile;
	const token = await createToken(user.id, tokenVersion);
	return { user, token, language: normalizeLocale(preferredLanguage) };
}

/**
 * Verify username + password. When the account has an active second factor,
 * return an MFA challenge (no session token yet) instead of a session result.
 */
export async function loginUser(
	username: string,
	password: string,
	sourceIp: string,
): Promise<LoginSuccess | MfaChallenge> {
	const attempt = authAttemptLimiter.beginPassword(username, sourceIp);
	if (!attempt.allowed) {
		throw new RateLimitError(
			attempt.reason === "busy" ? "AUTH_BUSY" : "LOGIN_THROTTLED",
			attempt.retryAfterMs,
		);
	}

	let attemptCompleted = false;
	try {
		const user = await authSessionStore.findLoginCredential(username);
		const valid = await Bun.password.verify(
			password,
			user?.passwordHash ?? INVALID_LOGIN_PADDING_HASH,
		);
		if (!valid || !user) {
			const after = attempt.failure();
			attemptCompleted = true;
			logger.warn("Failed password login attempt", {
				userId: user?.id,
				accountFingerprint: fingerprintAuthIdentifier(username),
				sourceIp,
				remaining: after.remaining,
				locked: after.locked,
			});
			if (after.locked) {
				throw new RateLimitError("LOGIN_THROTTLED", after.retryAfterMs);
			}
			throw new AppError("Invalid credentials", 401, "INVALID_CREDENTIALS");
		}

		attempt.success();
		attemptCompleted = true;

		// Gate on a second factor only when the user has opted into MFA. Merely
		// having a passkey (a passwordless login method) or a dormant TOTP secret
		// does NOT force a second step — `users.mfaEnabled` is the explicit switch.
		// The available methods still depend on which factors are actually enrolled.
		if (user.mfaEnabled) {
			// Lazy import: the MFA service pulls in otpauth, which must not load for
			// plain JWT verify/create consumers.
			const { mfaService } = await import("../services/mfa-service");
			const [totpActive, hasPasskey] = await Promise.all([
				mfaService.isTotpActive(user.id),
				mfaService.hasAnyPasskey(user.id),
			]);
			const methods: MfaChallenge["methods"] = [];
			if (totpActive) methods.push("totp", "backup_code");
			if (hasPasskey) methods.push("passkey");
			// Defensive: if MFA is flagged on but no usable factor remains, fall
			// through to a normal session rather than locking the user out.
			if (methods.length > 0) {
				const mfaToken = await issueMfaToken(user.id);
				return { mfaRequired: true, mfaToken, methods };
			}
		}

		return buildSessionResult(user.id);
	} catch (error) {
		if (!attemptCompleted) attempt.cancel();
		throw error;
	}
}
