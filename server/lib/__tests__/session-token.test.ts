import { afterAll, describe, expect, mock, test } from "bun:test";

/**
 * Legacy session-JWT verification, exercised without the database.
 *
 * Session JWTs are no longer issued, but those issued before opaque session tokens
 * stay valid for their remaining lifetime. `server/lib/auth.ts` imports `../db` for
 * the session store, which would run the whole migration stack. Only the JWT layer
 * is under test here, so the module is stubbed out.
 *
 * Snapshot the real module first. Bun's mock.module is process-wide and
 * mock.restore() does NOT undo it, so afterAll re-points the specifier back —
 * otherwise this `{ db: {} }` stub leaks into every later-loaded suite that
 * touches the database (oauth-provider, oauth-rate-limit, …) and even breaks the
 * `sqlite` named export other suites re-mock.
 */
const realDbModule = { ...(await import("../../db")) };
mock.module("../../db", () => ({ db: {} }));

afterAll(() => {
	mock.module("../../db", () => realDbModule);
	mock.restore();
});

const [
	{ isAuthenticSessionTokenIgnoringExpiry, verifyToken },
	{ settings },
	{ SESSION_START_CLAIM },
	{ sign },
] = await Promise.all([
	import("../auth"),
	import("../settings"),
	import("@shared/session-auth"),
	import("hono/jwt"),
]);

type Claims = Record<string, unknown>;

describe("verifyToken shape checks", () => {
	const now = () => Math.floor(Date.now() / 1000);

	test("rejects a correctly signed token with no exp", async () => {
		const token = await sign({ sub: "user-1", role: "user", iat: now() }, settings.auth.jwtSecret);
		// hono's verify skips an absent exp entirely, which would make this a
		// permanent credential that never enters renewal or the absolute ceiling.
		expect(verifyToken(token)).rejects.toThrow();
	});

	test("rejects a correctly signed token with a non-numeric exp", async () => {
		const token = await sign(
			{ sub: "user-1", role: "user", iat: now(), exp: "later" } as unknown as Claims,
			settings.auth.jwtSecret,
		);
		expect(verifyToken(token)).rejects.toThrow();
	});

	test("rejects a correctly signed token with a missing or blank sub", async () => {
		const noSub = await sign(
			{ role: "admin", iat: now(), exp: now() + 3600 },
			settings.auth.jwtSecret,
		);
		expect(verifyToken(noSub)).rejects.toThrow();

		const blankSub = await sign(
			{ sub: "   ", role: "admin", iat: now(), exp: now() + 3600 },
			settings.auth.jwtSecret,
		);
		expect(verifyToken(blankSub)).rejects.toThrow();
	});

	test("accepts a correctly signed legacy session JWT", async () => {
		const token = await sign(
			{ sub: "user-1", role: "user", iat: now(), exp: now() + 3600 },
			settings.auth.jwtSecret,
		);
		expect((await verifyToken(token)).sub).toBe("user-1");
	});

	test("rejects a JWT that claims an opaque session id", async () => {
		const token = await sign(
			{ sub: "user-1", role: "user", iat: now(), exp: now() + 3600, sid: "x" },
			settings.auth.jwtSecret,
		);
		expect(verifyToken(token)).rejects.toThrow();
	});

	test("still rejects an intermediate MFA-stage token", async () => {
		const staged = await sign(
			{ sub: "user-1", role: "user", stage: "mfa", iat: now(), exp: now() + 300 },
			settings.auth.jwtSecret,
		);
		expect(verifyToken(staged)).rejects.toThrow();
	});
});

describe("isAuthenticSessionTokenIgnoringExpiry", () => {
	const now = () => Math.floor(Date.now() / 1000);

	test("accepts a token that only failed on exp", async () => {
		const expired = await sign(
			{
				sub: "user-1",
				role: "user",
				iat: now() - 7200,
				exp: now() - 3600,
				[SESSION_START_CLAIM]: now() - 7200,
			},
			settings.auth.jwtSecret,
		);
		expect(await isAuthenticSessionTokenIgnoringExpiry(expired)).toBe(true);
	});

	test("refuses a past-exp token signed with a foreign key", async () => {
		const forged = await sign(
			{ sub: "user-1", role: "admin", iat: now() - 7200, exp: now() - 3600 },
			"an-attacker-controlled-secret",
		);
		// This is what keeps a forgery from being reported as TOKEN_EXPIRED.
		expect(await isAuthenticSessionTokenIgnoringExpiry(forged)).toBe(false);
	});

	test("refuses garbage, OAuth access tokens and opaque session tokens", async () => {
		expect(await isAuthenticSessionTokenIgnoringExpiry("not-a-jwt")).toBe(false);
		expect(await isAuthenticSessionTokenIgnoringExpiry(`nfs_${"A".repeat(43)}`)).toBe(false);
		expect(await isAuthenticSessionTokenIgnoringExpiry("nfat_abc123")).toBe(false);
	});

	test("refuses an unexpiring token even with a valid signature", async () => {
		const noExp = await sign({ sub: "user-1", role: "user", iat: now() }, settings.auth.jwtSecret);
		expect(await isAuthenticSessionTokenIgnoringExpiry(noExp)).toBe(false);
	});
});
