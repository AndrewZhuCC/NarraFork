/**
 * Every authenticating provider method must apply host hints and write credentials back.
 *
 * These are wiring assertions, not behaviour assertions, and they exist because both
 * omissions are invisible at runtime:
 *
 * - a method that skips `applyHostHints` silently uses whatever proxy the *previous* call
 *   set, so it works in testing and fails only for the user who has no other traffic;
 * - a method that skips `writebackIfChanged` leaves the vault holding a refresh token the
 *   upstream already rotated away, which surfaces one restart later as an auth failure.
 *
 * Both were real gaps: the first implementation wired writeback into `chat`/`generate` only,
 * even though `search` and `listModels` authenticate through the same manager and can
 * trigger the same rotation. Catalog refresh is the worst case — it runs on a schedule the
 * user never sees, so a token could rotate with no other traffic to carry it back.
 *
 * Source inspection is deliberate: importing `server.ts` claims stdio, and a behavioural
 * test would need a full RPC transport plus a live upstream to reach these paths.
 */

import { describe, expect, test } from "bun:test";

const SERVER_SOURCE_PATH = "examples/plugins/kiro-external/src/server.ts";

/** Body of a top-level `async function name(...)` declaration, up to the next one. */
function functionBody(source: string, name: string): string {
	const start = source.indexOf(`async function ${name}(`);
	expect(start, `${name} should exist in server.ts`).toBeGreaterThan(-1);
	const rest = source.slice(start + 1);
	const nextDeclaration = rest.search(/\n(?:async )?function \w+\(/);
	return nextDeclaration === -1 ? rest : rest.slice(0, nextDeclaration);
}

/** Provider methods that resolve a MultiTokenManager and therefore can refresh a token. */
const AUTHENTICATING_METHODS = ["listModels", "search", "startChat"] as const;

describe("provider method wiring", () => {
	test("every authenticating method applies host hints", async () => {
		const source = await Bun.file(SERVER_SOURCE_PATH).text();
		for (const method of AUTHENTICATING_METHODS) {
			expect(
				functionBody(source, method),
				`${method} must call applyHostHints or it will reuse another call's proxy`,
			).toContain("applyHostHints(params)");
		}
	});

	test("every authenticating method writes refreshed credentials back", async () => {
		const source = await Bun.file(SERVER_SOURCE_PATH).text();
		for (const method of AUTHENTICATING_METHODS) {
			const body = functionBody(source, method);
			expect(
				body,
				`${method} must snapshot the credential file before the upstream call`,
			).toContain("credentialFileHash(credPath)");
			expect(body, `${method} must write back if the credential file changed`).toContain(
				"writebackIfChanged(credPath, hashBefore)",
			);
		}
	});

	test("validateConfig applies host hints too", async () => {
		// It does not authenticate against the data plane, so it needs no writeback, but it
		// can still reach the auth endpoint and must honour the host's proxy.
		const source = await Bun.file(SERVER_SOURCE_PATH).text();
		expect(functionBody(source, "validateConfig")).toContain("applyHostHints(params)");
	});

	test("writeback runs even when the upstream call failed", async () => {
		// A refresh can succeed and rotate the token, and the request can fail afterwards.
		// Putting the writeback in a `finally` is what keeps that rotation from being lost.
		const source = await Bun.file(SERVER_SOURCE_PATH).text();
		for (const method of AUTHENTICATING_METHODS) {
			const body = functionBody(source, method);
			const writebackAt = body.indexOf("writebackIfChanged(credPath, hashBefore)");
			const finallyAt = body.lastIndexOf("} finally {", writebackAt);
			expect(finallyAt, `${method} must write back from a finally block`).toBeGreaterThan(-1);
		}
	});
});
