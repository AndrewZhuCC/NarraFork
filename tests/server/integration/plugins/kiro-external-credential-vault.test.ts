/**
 * Vault-backed credential storage: one secret per credential, plus an index sentinel.
 *
 * ## What this replaces, and why the shape changed
 *
 * Credentials used to travel as one JSON blob in a single secret, injected into `config` on
 * every provider call. That made credential *count* inflate a single *value*, which collides
 * with the vault's 64 KB per-value limit and the plugin's own `maxConfigBytes` — a few hundred
 * credentials is simply not expressible, and no ceiling adjustment reaches that scale. One key
 * per credential removes the coupling: each gets its own budget, and the per-plugin entry limit
 * (2,000) is the only bound left.
 *
 * ## What is actually asserted here
 *
 * The two properties that are easy to get wrong and expensive to get wrong:
 *
 * - **The sentinel exists if and only if a credential exists.** `isUsable()` and `secretsSet`
 *   both test "key present with a non-empty value", so a stored `{"count":0}` would report the
 *   provider as configured while no credential exists — a failure that only surfaces when a
 *   request is finally made.
 * - **Steady state costs one RPC.** Each host request is a real stdio round trip, not a cheap
 *   in-process call, so reading N credentials per chat request would be N round trips. The
 *   revision comparison is what avoids that, and a test that did not count calls would not
 *   notice it regressing.
 */

import { afterEach, describe, expect, test } from "bun:test";
import type { KiroCredentials } from "@server/lib/kiro/types";
import {
	credentialKey,
	INDEX_KEY,
	isCredentialKey,
	readCredentials,
	readIndex,
	readKeyState,
	setVaultTransport,
	writeCredential,
	writeIndex,
} from "../../../../examples/plugins/kiro-external/src/credential-vault";

/**
 * An in-memory stand-in for the host vault.
 *
 * Counts calls per method, because the cost model — one round trip per call — is itself part of
 * what is being tested.
 */
function fakeVault(initial: Record<string, string> = {}) {
	const store = new Map(Object.entries(initial));
	const counts = new Map<string, number>();
	const restore = setVaultTransport(async (method, params) => {
		counts.set(method, (counts.get(method) ?? 0) + 1);
		const { key, value } = (params ?? {}) as { key?: string; value?: string };
		switch (method) {
			case "secrets.get":
				return { value: key === undefined ? undefined : store.get(key) };
			case "secrets.set":
				if (key !== undefined && value !== undefined) store.set(key, value);
				return { stored: true };
			case "secrets.delete":
				if (key !== undefined) store.delete(key);
				return { deleted: true };
			case "secrets.list":
				return { secrets: [...store.keys()].map((name) => ({ key: name })) };
			default:
				throw new Error(`unexpected method ${method}`);
		}
	});
	return {
		store,
		restore,
		callsTo: (method: string) => counts.get(method) ?? 0,
	};
}

let active: { restore: () => void } | undefined;

function vault(initial: Record<string, string> = {}) {
	const created = fakeVault(initial);
	active = created;
	return created;
}

afterEach(() => {
	active?.restore();
	active = undefined;
});

function credential(id: string, overrides: Partial<KiroCredentials> = {}): KiroCredentials {
	return {
		id,
		authMethod: "social",
		refreshToken: `refresh-${id}`,
		region: "us-east-1",
		...overrides,
	} as KiroCredentials;
}

describe("credential vault: per-credential keys", () => {
	test("stores each credential under its own key", async () => {
		const v = vault();
		await writeCredential(credential("a"));
		await writeCredential(credential("b"));

		expect([...v.store.keys()].sort()).toEqual(["provider.kiro.cred.a", "provider.kiro.cred.b"]);
	});

	test("changing one credential leaves the others byte-identical", async () => {
		// The property the blob could not offer: rewriting one credential rewrote all of them,
		// so any partial-write failure risked the entire set.
		const v = vault();
		await writeCredential(credential("a"));
		await writeCredential(credential("b"));
		const untouched = v.store.get("provider.kiro.cred.b");

		await writeCredential(credential("a", { refreshToken: "rotated" }));

		expect(v.store.get("provider.kiro.cred.b")).toBe(untouched);
		const reread = await readCredentials(["provider.kiro.cred.a"]);
		expect(reread[0]?.refreshToken).toBe("rotated");
	});

	test("reads back every credential in key order", async () => {
		const v = vault();
		for (const id of ["c", "a", "b"]) await writeCredential(credential(id));

		const state = await readKeyState();
		const credentials = await readCredentials(state.credentialKeys);
		expect(credentials.map((entry) => entry.id)).toEqual(["a", "b", "c"]);
		expect(v.callsTo("secrets.get")).toBe(3);
	});

	test("a single unparseable entry does not hide the rest", async () => {
		// One corrupted value must not lock the user out of every other credential.
		const v = vault();
		await writeCredential(credential("a"));
		v.store.set("provider.kiro.cred.broken", "{not json");
		await writeCredential(credential("z"));

		const state = await readKeyState();
		const credentials = await readCredentials(state.credentialKeys);
		expect(credentials.map((entry) => entry.id)).toEqual(["a", "z"]);
	});

	test("refuses to store a credential with no id", async () => {
		vault();
		// Silently skipping it would drop a credential the user just added; the key model has
		// nowhere to put it.
		await expect(writeCredential(credential("") as KiroCredentials)).rejects.toThrow(/id/);
	});

	test("recognises its own keys and nothing else", () => {
		expect(isCredentialKey(credentialKey("x"))).toBe(true);
		expect(isCredentialKey(INDEX_KEY)).toBe(false);
		expect(isCredentialKey("provider.kiro.credentialsBundle")).toBe(false);
	});

	test("scales past the size the single blob could hold", async () => {
		// Sized from what a real credential carries, not from short placeholders: the tokens
		// are JWTs, so an entry is roughly 2.5 KB rather than the ~90 bytes a minimal fixture
		// produces. That distinction decides the test — 400 minimal fixtures serialize to
		// ~37 KB and would have fit in the old single value, proving nothing.
		const v = vault();
		const jwt = `eyJ${"A".repeat(1_000)}`;
		for (let index = 0; index < 400; index += 1) {
			await writeCredential(
				credential(`cred-${index}`, {
					refreshToken: jwt,
					accessToken: jwt,
					profileArn: `arn:aws:codewhisperer:us-east-1:${index}:profile/AAAAAAAAAAAA`,
				}),
			);
		}
		const state = await readKeyState();
		expect(state.credentialKeys).toHaveLength(400);

		// Every entry stays well inside the 64 KB per-value limit on its own...
		const largest = Math.max(...[...v.store.values()].map((value) => value.length));
		expect(largest).toBeLessThan(64 * 1024);

		// ...while the same set as one value would exceed it many times over. This is the
		// number that makes the rework structural rather than a raised ceiling: no adjustment
		// to the per-value limit reaches a few hundred credentials.
		const asOneBlob = JSON.stringify(await readCredentials(state.credentialKeys)).length;
		expect(asOneBlob).toBeGreaterThan(10 * 64 * 1024);
	});
});

describe("credential vault: the index sentinel", () => {
	test("round-trips revision and count", async () => {
		vault();
		await writeIndex({ revision: 42, count: 3 });
		expect(await readIndex()).toEqual({ revision: 42, count: 3 });
	});

	test("a zero count deletes the sentinel instead of storing zero", async () => {
		// The load-bearing invariant. `isUsable()`/`secretsSet` only check that the key exists
		// with a non-empty value, so `{"count":0}` would advertise a configured provider with
		// nothing to serve — and the error would surface only at request time.
		const v = vault();
		await writeIndex({ revision: 1, count: 2 });
		expect(v.store.has(INDEX_KEY)).toBe(true);

		await writeIndex({ revision: 2, count: 0 });

		expect(v.store.has(INDEX_KEY)).toBe(false);
		expect(await readIndex()).toBeUndefined();
	});

	test("a malformed sentinel reads as absent rather than throwing", async () => {
		// "Cannot tell what revision this is" must force a full reload, not fail the request.
		const v = vault();
		v.store.set(INDEX_KEY, "{not json");
		expect(await readIndex()).toBeUndefined();

		v.store.set(INDEX_KEY, JSON.stringify({ count: 3 }));
		expect(await readIndex()).toBeUndefined();
	});

	test("reading the sentinel costs exactly one round trip", async () => {
		// The steady-state cost of every provider request. Each host call is a stdio round
		// trip with its own pending entry and timeout, so this number is the whole point of
		// keeping a revision marker separate from the credentials.
		const v = vault({ [INDEX_KEY]: JSON.stringify({ revision: 7, count: 300 }) });
		await readIndex();
		expect(v.callsTo("secrets.get")).toBe(1);
		expect(v.callsTo("secrets.list")).toBe(0);
	});

	test("distinguishes an interrupted migration from an empty install", async () => {
		// Credential entries with no sentinel means the sentinel write did not land. Treating
		// "no sentinel" as "no credentials" would report the user's credentials as missing;
		// treating "any cred key exists" as "migrated" would call an interrupted migration
		// complete. Both signals are needed, which is why both are reported.
		const v = vault();
		await writeCredential(credential("a"));

		const interrupted = await readKeyState();
		expect(interrupted.credentialKeys).toHaveLength(1);
		expect(interrupted.hasIndex).toBe(false);

		v.store.clear();
		const fresh = await readKeyState();
		expect(fresh.credentialKeys).toHaveLength(0);
		expect(fresh.hasIndex).toBe(false);
	});

	test("reports the legacy blob separately from the new entries", async () => {
		// Migration needs to see the old key without it ever being mistaken for a credential
		// entry, which is why the prefixes do not overlap.
		vault({ "provider.kiro.credentialsBundle": "[]" });
		const state = await readKeyState();
		expect(state.hasLegacyBundle).toBe(true);
		expect(state.credentialKeys).toEqual([]);
	});
});
