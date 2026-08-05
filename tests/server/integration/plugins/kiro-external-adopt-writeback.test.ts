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
 */

import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { KiroCredentials } from "@server/lib/kiro/types";
import {
	credentialFileHash,
	VAULT_BUNDLE_KEY,
	writebackIfChanged,
} from "../../../../examples/plugins/kiro-external/src/credential-writeback";

let dir: string;
let credentialsPath: string;

interface RecordedCall {
	method: string;
	params: unknown;
}

function recorder(behaviour: "resolve" | "reject" = "resolve") {
	const calls: RecordedCall[] = [];
	const logs: string[] = [];
	return {
		calls,
		logs,
		deps: {
			request: (method: string, params: unknown) => {
				calls.push({ method, params });
				return behaviour === "resolve"
					? Promise.resolve({ key: VAULT_BUNDLE_KEY, stored: true })
					: Promise.reject(new Error("vault unavailable"));
			},
			log: (message: string) => {
				logs.push(message);
			},
		},
	};
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

/** The bundle value the plugin sent, parsed back into credentials. */
function sentBundle(params: unknown): KiroCredentials[] {
	const record = params as { key?: string; value?: string };
	return JSON.parse(record.value ?? "[]");
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
	test("writes the vault when a refresh rotated the token", () => {
		writeCredentials([credential()]);
		const before = credentialFileHash(credentialsPath);

		// What a refresh does: same credential id, new refresh token.
		writeCredentials([credential({ refreshToken: "refresh-rotated" })]);
		const { calls, deps } = recorder();
		writebackIfChanged(credentialsPath, before, deps);

		expect(calls).toHaveLength(1);
		expect(calls[0]?.method).toBe("secrets.set");
		expect((calls[0]?.params as { key: string }).key).toBe(VAULT_BUNDLE_KEY);
		expect(sentBundle(calls[0]?.params)[0]?.refreshToken).toBe("refresh-rotated");
	});

	test("writes nothing when the file did not change", () => {
		writeCredentials([credential()]);
		const before = credentialFileHash(credentialsPath);
		const { calls, deps } = recorder();
		writebackIfChanged(credentialsPath, before, deps);
		expect(calls).toEqual([]);
	});

	test("writes the vault when the file appears for the first time", () => {
		// No file at the start of the operation, so the baseline is null and anything the
		// manager wrote during the call needs to reach the vault.
		const before = credentialFileHash(credentialsPath);
		expect(before).toBeNull();

		writeCredentials([credential()]);
		const { calls, deps } = recorder();
		writebackIfChanged(credentialsPath, before, deps);
		expect(calls).toHaveLength(1);
	});

	test("refuses to write an empty bundle over stored credentials", () => {
		// A file that changed but reads as empty is a read failure, not a user clearing their
		// credentials. Writing it through would wipe the vault entry.
		writeCredentials([credential()]);
		const before = credentialFileHash(credentialsPath);
		writeFileSync(credentialsPath, "[]", "utf-8");

		const { calls, deps } = recorder();
		writebackIfChanged(credentialsPath, before, deps);
		expect(calls).toEqual([]);
	});

	test("does nothing when the file vanished mid-operation", () => {
		writeCredentials([credential()]);
		const before = credentialFileHash(credentialsPath);
		rmSync(credentialsPath);

		const { calls, deps } = recorder();
		writebackIfChanged(credentialsPath, before, deps);
		expect(calls).toEqual([]);
	});

	test("carries every credential, not just the refreshed one", () => {
		writeCredentials([credential(), credential({ id: "cred-2", refreshToken: "second" })]);
		const before = credentialFileHash(credentialsPath);
		writeCredentials([
			credential({ refreshToken: "rotated" }),
			credential({ id: "cred-2", refreshToken: "second" }),
		]);

		const { calls, deps } = recorder();
		writebackIfChanged(credentialsPath, before, deps);
		// A partial bundle would drop the sibling credential from the vault entirely, since
		// the whole list is stored as one value.
		expect(sentBundle(calls[0]?.params)).toHaveLength(2);
	});

	test("preserves per-credential proxy fields through the round trip", () => {
		// These were silently dropped before `BUNDLE_TEXT_FIELDS` included them; a writeback
		// that loses them would erase the user's proxy config from the vault.
		writeCredentials([credential()]);
		const before = credentialFileHash(credentialsPath);
		writeCredentials([
			credential({
				refreshToken: "rotated",
				proxyUrl: "http://squid.corp:3128",
				proxyUsername: "corp-user",
				proxyPassword: "corp-secret",
			}),
		]);

		const { calls, deps } = recorder();
		writebackIfChanged(credentialsPath, before, deps);
		const sent = sentBundle(calls[0]?.params)[0];
		expect(sent?.proxyUrl).toBe("http://squid.corp:3128");
		expect(sent?.proxyUsername).toBe("corp-user");
		expect(sent?.proxyPassword).toBe("corp-secret");
	});

	test("a vault failure is logged without throwing into the caller", async () => {
		// The request is fire-and-forget: a failed vault write must not fail the user's turn.
		// The plugin's own file still has the refreshed token, so it keeps working.
		writeCredentials([credential()]);
		const before = credentialFileHash(credentialsPath);
		writeCredentials([credential({ refreshToken: "rotated" })]);

		const { logs, deps } = recorder("reject");
		expect(() => writebackIfChanged(credentialsPath, before, deps)).not.toThrow();
		// Let the rejected promise settle so the catch handler runs.
		await Promise.resolve();
		await Promise.resolve();
		expect(logs).toHaveLength(1);
		expect(logs[0]).toContain("secrets.set failed");
	});

	test("the vault key matches the one the command path writes", async () => {
		// Both paths store the same single bundle value. A mismatch would create two vault
		// entries, and whichever the host injects would silently be the stale one.
		const commandsSource = await Bun.file("examples/plugins/kiro-external/src/commands.ts").text();
		expect(commandsSource).toContain(`"${VAULT_BUNDLE_KEY}"`);
	});
});
