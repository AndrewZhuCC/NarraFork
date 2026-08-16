/**
 * Credential writeback to the host vault after a token refresh.
 *
 * Asserts against the real `credential-writeback.ts` rather than restating its logic here —
 * a test that reimplements the hash comparison keeps passing no matter what the shipped code
 * does. The module is separate from `server.ts` for exactly this reason (`server.ts` claims
 * stdio at import time).
 *
 * The failure being guarded: `token-manager.ts` adopts a rotated refresh token, the manager
 * persists it to the plugin's own data directory, but the host vault keeps the old value and
 * re-injects it after a restart. For IDC accounts that revoke rotated tokens, that is
 * unrecoverable.
 *
 * Credentials are now stored one per vault key, so this also pins the *ordering* the writeback
 * uses: entries first, sentinel last. `secretWrites`-style batches are not atomic, and that
 * order is what makes every partial failure a stale-but-consistent state rather than a
 * sentinel advertising credentials that were never written.
 */

import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { KiroCredentials } from "@server/lib/kiro/types";
import { INDEX_KEY } from "../../../../examples/plugins/kiro-external/src/credential-vault";
import {
	credentialFileHash,
	writebackIfChanged,
} from "../../../../examples/plugins/kiro-external/src/credential-writeback";

let dir: string;
let credentialsPath: string;

interface RecordedCall {
	method: string;
	params: unknown;
}

/**
 * Records requests and lets the fire-and-forget chain finish.
 *
 * `storedRevision` seeds what `secrets.get` reports for the sentinel, so revision behaviour
 * can be exercised without a real vault.
 */
function recorder(
	behaviour: "resolve" | "reject" = "resolve",
	storedRevision?: number,
): {
	calls: RecordedCall[];
	logs: string[];
	deps: {
		request: (method: string, params: unknown) => Promise<unknown>;
		log: (message: string) => void;
	};
} {
	const calls: RecordedCall[] = [];
	const logs: string[] = [];
	return {
		calls,
		logs,
		deps: {
			request: (method: string, params: unknown) => {
				calls.push({ method, params });
				if (behaviour === "reject") return Promise.reject(new Error("vault unavailable"));
				if (method === "secrets.get") {
					return Promise.resolve({
						value:
							storedRevision === undefined
								? undefined
								: JSON.stringify({ revision: storedRevision, count: 1 }),
					});
				}
				return Promise.resolve({ stored: true });
			},
			log: (message: string) => {
				logs.push(message);
			},
		},
	};
}

/**
 * Let the writeback's promise chain run to completion.
 *
 * The chain awaits sequentially, so it needs more than one microtask tick. A macrotask
 * boundary drains the whole microtask queue, which is enough because every stubbed request
 * resolves immediately.
 */
async function settle(): Promise<void> {
	await Bun.sleep(0);
}

function credential(overrides: Partial<KiroCredentials> = {}): KiroCredentials {
	return {
		id: "cred-1",
		authMethod: "social",
		refreshToken: "refresh-original",
		region: "us-east-1",
		...overrides,
	} as KiroCredentials;
}

function writeCredentials(credentials: KiroCredentials[]): void {
	writeFileSync(credentialsPath, JSON.stringify(credentials), "utf-8");
}

/** The credential-entry writes, keyed by vault key. */
function credentialWrites(calls: RecordedCall[]): Map<string, KiroCredentials> {
	const byKey = new Map<string, KiroCredentials>();
	for (const call of calls) {
		if (call.method !== "secrets.set") continue;
		const { key, value } = call.params as { key: string; value: string };
		if (key === INDEX_KEY) continue;
		byKey.set(key, JSON.parse(value) as KiroCredentials);
	}
	return byKey;
}

/** The sentinel value that was written, if any. */
function sentinelWrite(calls: RecordedCall[]): { revision: number; count: number } | undefined {
	for (const call of calls) {
		if (call.method !== "secrets.set") continue;
		const { key, value } = call.params as { key: string; value: string };
		if (key === INDEX_KEY) return JSON.parse(value) as { revision: number; count: number };
	}
	return undefined;
}

beforeEach(() => {
	dir = join(tmpdir(), `kiro-writeback-${process.pid}-${Math.random().toString(36).slice(2)}`);
	mkdirSync(dir, { recursive: true });
	credentialsPath = join(dir, "credentials.json");
});

afterEach(() => {
	rmSync(dir, { recursive: true, force: true });
});

describe("credential writeback", () => {
	test("writes the rotated credential to its own key", async () => {
		writeCredentials([credential()]);
		const before = await credentialFileHash(credentialsPath);

		// What a refresh does: same credential id, new refresh token.
		writeCredentials([credential({ refreshToken: "refresh-rotated" })]);
		const { calls, deps } = recorder();
		await writebackIfChanged(credentialsPath, before, deps);
		await settle();

		const writes = credentialWrites(calls);
		expect([...writes.keys()]).toEqual(["provider.kiro.cred.cred-1"]);
		expect(writes.get("provider.kiro.cred.cred-1")?.refreshToken).toBe("refresh-rotated");
	});

	test("writes the sentinel last, after every credential entry", async () => {
		writeCredentials([credential(), credential({ id: "cred-2" })]);
		const before = await credentialFileHash(credentialsPath);
		writeCredentials([credential({ refreshToken: "rotated" }), credential({ id: "cred-2" })]);

		const { calls, deps } = recorder();
		await writebackIfChanged(credentialsPath, before, deps);
		await settle();

		// The ordering is the safety property: if the sentinel lands, the credentials it
		// counts are already stored. The reverse would advertise a revision whose (single-use)
		// rotated token was never persisted.
		const setKeys = calls
			.filter((call) => call.method === "secrets.set")
			.map((call) => (call.params as { key: string }).key);
		expect(setKeys).toEqual(["provider.kiro.cred.cred-1", "provider.kiro.cred.cred-2", INDEX_KEY]);
		expect(sentinelWrite(calls)?.count).toBe(2);
	});

	test("advances the revision past the stored one", async () => {
		// The cached manager is reused while the revision is unchanged, so a writeback that
		// did not advance it would leave the process serving the pre-rotation token.
		writeCredentials([credential()]);
		const before = await credentialFileHash(credentialsPath);
		writeCredentials([credential({ refreshToken: "rotated" })]);

		// A revision far in the future, as a clock step backwards would produce.
		const stored = Date.now() + 60_000;
		const { calls, deps } = recorder("resolve", stored);
		await writebackIfChanged(credentialsPath, before, deps);
		await settle();

		expect(sentinelWrite(calls)?.revision).toBeGreaterThan(stored);
	});

	test("writes nothing when the file did not change", async () => {
		writeCredentials([credential()]);
		const before = await credentialFileHash(credentialsPath);
		const { calls, deps } = recorder();
		await writebackIfChanged(credentialsPath, before, deps);
		await settle();
		expect(calls).toEqual([]);
	});

	test("writes the vault when the file appears for the first time", async () => {
		// No file at the start of the operation, so the baseline is null and anything the
		// manager wrote during the call needs to reach the vault.
		const before = await credentialFileHash(credentialsPath);
		expect(before).toBeNull();

		writeCredentials([credential()]);
		const { calls, deps } = recorder();
		await writebackIfChanged(credentialsPath, before, deps);
		await settle();
		expect(credentialWrites(calls).size).toBe(1);
	});

	test("refuses to write an empty set over stored credentials", async () => {
		// A file that changed but reads as empty is a read failure, not a user clearing their
		// credentials. Writing it through would delete the sentinel and orphan the entries.
		writeCredentials([credential()]);
		const before = await credentialFileHash(credentialsPath);
		writeFileSync(credentialsPath, "[]", "utf-8");

		const { calls, deps } = recorder();
		await writebackIfChanged(credentialsPath, before, deps);
		await settle();
		expect(calls).toEqual([]);
	});

	test("does nothing when the file vanished mid-operation", async () => {
		writeCredentials([credential()]);
		const before = await credentialFileHash(credentialsPath);
		rmSync(credentialsPath);

		const { calls, deps } = recorder();
		await writebackIfChanged(credentialsPath, before, deps);
		await settle();
		expect(calls).toEqual([]);
	});

	test("carries every credential, not just the refreshed one", async () => {
		writeCredentials([credential(), credential({ id: "cred-2", refreshToken: "second" })]);
		const before = await credentialFileHash(credentialsPath);
		writeCredentials([
			credential({ refreshToken: "rotated" }),
			credential({ id: "cred-2", refreshToken: "second" }),
		]);

		const { calls, deps } = recorder();
		await writebackIfChanged(credentialsPath, before, deps);
		await settle();
		// Per-key storage means a sibling is no longer at risk of being dropped by a partial
		// serialization, but a writeback that only sent the changed entry would leave the
		// sentinel's count disagreeing with what is stored.
		expect(credentialWrites(calls).size).toBe(2);
	});

	test("preserves per-credential proxy fields through the round trip", async () => {
		// These were silently dropped before `BUNDLE_TEXT_FIELDS` included them; a writeback
		// that loses them would erase the user's proxy config from the vault.
		writeCredentials([credential()]);
		const before = await credentialFileHash(credentialsPath);
		writeCredentials([
			credential({
				refreshToken: "rotated",
				proxyUrl: "http://squid.corp:3128",
				proxyUsername: "corp-user",
				proxyPassword: "corp-secret",
			}),
		]);

		const { calls, deps } = recorder();
		await writebackIfChanged(credentialsPath, before, deps);
		await settle();
		const sent = credentialWrites(calls).get("provider.kiro.cred.cred-1");
		expect(sent?.proxyUrl).toBe("http://squid.corp:3128");
		expect(sent?.proxyUsername).toBe("corp-user");
		expect(sent?.proxyPassword).toBe("corp-secret");
	});

	test("a vault failure is logged without throwing into the caller", async () => {
		// The request is fire-and-forget: a failed vault write must not fail the user's turn.
		// The plugin's own file still has the refreshed token, so it keeps working.
		writeCredentials([credential()]);
		const before = await credentialFileHash(credentialsPath);
		writeCredentials([credential({ refreshToken: "rotated" })]);

		const { logs, deps } = recorder("reject");
		// writebackIfChanged is now async but handles its own errors — it never rejects.
		await expect(writebackIfChanged(credentialsPath, before, deps)).resolves.toBeUndefined();
		await settle();
		expect(logs).toHaveLength(1);
		expect(logs[0]).toContain("writeback");
	});

	test("no module hardcodes a credential key literal of its own", async () => {
		// Both write paths must agree on the key shape. Previously each restated the literal,
		// which is precisely how two competing vault entries would appear — with the host
		// injecting whichever one happened to be stale. The key shape now lives in exactly
		// one module, so a mismatch is not expressible.
		for (const file of ["commands.ts", "credential-writeback.ts"]) {
			const source = await Bun.file(`examples/plugins/kiro-external/src/${file}`).text();
			expect(source).not.toContain('"provider.kiro.cred.');
			expect(source).not.toContain('"provider.kiro.credentialsIndex"');
			expect(source).toContain("credential-vault");
		}
	});
});
