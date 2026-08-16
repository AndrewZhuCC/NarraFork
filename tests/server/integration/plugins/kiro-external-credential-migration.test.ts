/**
 * Migration from the single credential blob to one vault entry per credential.
 *
 * ## Why this file is the most safety-critical of the set
 *
 * IDC refresh tokens are single-use: once rotated, the old value is dead. A migration bug that
 * drops a credential is therefore not recoverable by re-running anything — the user has to
 * re-authenticate an account they may not control. So the tests here concentrate on the
 * *interrupted* runs rather than the happy path, which is the easy case.
 *
 * ## The specific failure being designed against
 *
 * Treating "any `cred.*` key exists" as "already migrated" looks reasonable and is catastrophic.
 * With 400 credentials, if the process dies after 200 entries but before the sentinel:
 *
 * - migration would report done, so the remaining 200 are never written;
 * - no sentinel means the read path sees no index;
 * - the blob still holds all 400, but the read path no longer consults it.
 *
 * Half the credentials are stranded, the provider reports unconfigured, and nothing self-heals.
 * The sentinel-plus-count marker turns that into a resumable state instead, and
 * `resumes an interrupted run` below is the test that pins it.
 */

import { afterEach, describe, expect, test } from "bun:test";
import type { KiroCredentials } from "@server/lib/kiro/types";
import {
	ensureMigrated,
	resetMigrationState,
} from "../../../../examples/plugins/kiro-external/src/credential-migration";
import {
	credentialKey,
	INDEX_KEY,
	LEGACY_BUNDLE_KEY,
	MIGRATED_BUNDLE_KEY,
	setVaultTransport,
} from "../../../../examples/plugins/kiro-external/src/credential-vault";

let restore: (() => void) | undefined;

/**
 * An in-memory vault that can be told to fail on a given write.
 *
 * `failSetAfter` makes the Nth `secrets.set` throw, which is how an interrupted migration is
 * reproduced: a batch is not atomic, so earlier writes stay persisted.
 */
function vault(initial: Record<string, string> = {}, options: { failSetAfter?: number } = {}) {
	const store = new Map(Object.entries(initial));
	let sets = 0;
	const counts = new Map<string, number>();
	restore = setVaultTransport(async (method, params) => {
		counts.set(method, (counts.get(method) ?? 0) + 1);
		const { key, value } = (params ?? {}) as { key?: string; value?: string };
		switch (method) {
			case "secrets.get":
				return { value: key === undefined ? undefined : store.get(key) };
			case "secrets.set":
				sets += 1;
				if (options.failSetAfter !== undefined && sets > options.failSetAfter) {
					throw new Error("vault write failed");
				}
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
		callsTo: (method: string) => counts.get(method) ?? 0,
		credentialIds: () =>
			[...store.keys()]
				.filter((key) => key.startsWith("provider.kiro.cred."))
				.map((key) => key.slice("provider.kiro.cred.".length))
				.sort(),
		index: () => {
			const raw = store.get(INDEX_KEY);
			return raw ? (JSON.parse(raw) as { revision: number; count: number }) : undefined;
		},
	};
}

afterEach(() => {
	restore?.();
	restore = undefined;
	resetMigrationState();
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

function blobOf(...credentials: KiroCredentials[]): Record<string, string> {
	return { [LEGACY_BUNDLE_KEY]: JSON.stringify(credentials) };
}

describe("credential migration: from the legacy blob", () => {
	test("splits the blob into one entry per credential", async () => {
		const v = vault(blobOf(credential("a"), credential("b"), credential("c")));

		const result = await ensureMigrated();

		expect(result).toMatchObject({ migrated: true, empty: false, count: 3, changed: true });
		expect(v.credentialIds()).toEqual(["a", "b", "c"]);
		expect(v.index()?.count).toBe(3);
	});

	test("keeps the blob under a new name instead of deleting it", async () => {
		// It is the only copy of credentials predating the migration, and a rotated IDC token
		// cannot be recovered if this turns out to have gone wrong.
		const v = vault(blobOf(credential("a")));
		await ensureMigrated();

		expect(v.store.has(LEGACY_BUNDLE_KEY)).toBe(false);
		expect(v.store.has(MIGRATED_BUNDLE_KEY)).toBe(true);
		expect(JSON.parse(v.store.get(MIGRATED_BUNDLE_KEY) ?? "[]")).toHaveLength(1);
	});

	test("preserves per-credential fields rather than only the secret", async () => {
		const v = vault(
			blobOf(
				credential("a", {
					priority: 5,
					disabled: true,
					proxyUrl: "http://squid.corp:3128",
				}),
			),
		);
		await ensureMigrated();

		const stored = JSON.parse(v.store.get(credentialKey("a")) ?? "{}") as KiroCredentials;
		expect(stored.priority).toBe(5);
		expect(stored.disabled).toBe(true);
		expect(stored.proxyUrl).toBe("http://squid.corp:3128");
	});
});

describe("credential migration: idempotence", () => {
	test("a completed migration does no further work", async () => {
		const v = vault(blobOf(credential("a"), credential("b")));
		await ensureMigrated();
		resetMigrationState();

		const before = v.callsTo("secrets.set");
		const second = await ensureMigrated();

		expect(second).toMatchObject({ migrated: true, changed: false, count: 2 });
		expect(v.callsTo("secrets.set")).toBe(before);
	});

	test("concurrent callers share one run", async () => {
		// Activation and an early provider request can arrive together; running twice would
		// double every write.
		const v = vault(blobOf(credential("a"), credential("b")));

		const [first, second, third] = await Promise.all([
			ensureMigrated(),
			ensureMigrated(),
			ensureMigrated(),
		]);

		expect(first).toBe(second);
		expect(second).toBe(third);
		// Two credentials, one sentinel, one blob copy.
		expect(v.callsTo("secrets.set")).toBe(4);
	});
});

describe("credential migration: interrupted runs", () => {
	test("resumes an interrupted run instead of reporting it complete", async () => {
		// The core case. Entries written, sentinel never reached — which is exactly what "any
		// cred key exists ⇒ migrated" would misread as finished, stranding the rest forever.
		const v = vault({
			...blobOf(credential("a"), credential("b"), credential("c")),
			[credentialKey("a")]: JSON.stringify(credential("a")),
			[credentialKey("b")]: JSON.stringify(credential("b")),
		});
		expect(v.index()).toBeUndefined();

		const result = await ensureMigrated();

		expect(result).toMatchObject({ migrated: true, count: 3, changed: true });
		expect(v.credentialIds()).toEqual(["a", "b", "c"]);
		expect(v.index()?.count).toBe(3);
	});

	test("an already-written entry wins over the blob's copy of it", async () => {
		// A token rotation may have refreshed the stored entry after the blob was written.
		// Letting the blob overwrite it would reinstate a dead single-use refresh token.
		const v = vault({
			...blobOf(credential("a", { refreshToken: "stale-from-blob" })),
			[credentialKey("a")]: JSON.stringify(credential("a", { refreshToken: "rotated" })),
		});

		await ensureMigrated();

		const stored = JSON.parse(v.store.get(credentialKey("a")) ?? "{}") as KiroCredentials;
		expect(stored.refreshToken).toBe("rotated");
	});

	test("a sentinel whose count disagrees with storage is treated as unfinished", async () => {
		// A count larger than what is stored means entries went missing after the sentinel was
		// written. Trusting the sentinel would leave the gap permanent, so the blob is consulted
		// again and the missing entry restored.
		const v = vault({
			...blobOf(credential("a"), credential("b")),
			[credentialKey("a")]: JSON.stringify(credential("a")),
			[INDEX_KEY]: JSON.stringify({ revision: 1, count: 2 }),
		});

		const result = await ensureMigrated();

		expect(result.changed).toBe(true);
		expect(v.credentialIds()).toEqual(["a", "b"]);
		expect(v.index()?.count).toBe(2);
	});

	test("finishes a rename that was interrupted after the sentinel was written", async () => {
		// The sentinel lands before the blob is renamed, so a blob still under its original name
		// with a consistent sentinel means the run stopped between those steps. The credentials
		// are safe, but leaving the key would keep a full duplicate of every secret in the vault
		// indefinitely — so the rename is completed here, not only on the path that created it.
		const v = vault({
			...blobOf(credential("a")),
			[credentialKey("a")]: JSON.stringify(credential("a")),
			[INDEX_KEY]: JSON.stringify({ revision: 1, count: 1 }),
		});

		const result = await ensureMigrated();

		expect(result.migrated).toBe(true);
		expect(v.store.has(LEGACY_BUNDLE_KEY)).toBe(false);
		expect(v.store.has(MIGRATED_BUNDLE_KEY)).toBe(true);
		// The credentials were already correct, so nothing about them changed.
		expect(v.credentialIds()).toEqual(["a"]);
		expect(v.index()?.count).toBe(1);
	});

	test("a failure partway leaves the blob intact and no sentinel", async () => {
		// No rollback: the guarantee is that a failed run leaves a state the next run can
		// resume from, and never one where the blob is gone before the entries are complete.
		const v = vault(blobOf(credential("a"), credential("b"), credential("c")), {
			failSetAfter: 2,
		});

		await expect(ensureMigrated()).rejects.toThrow();

		expect(v.store.has(LEGACY_BUNDLE_KEY)).toBe(true);
		expect(v.store.has(MIGRATED_BUNDLE_KEY)).toBe(false);
		expect(v.index()).toBeUndefined();
		expect(v.credentialIds()).toEqual(["a", "b"]);
	});

	test("a retry after that failure converges", async () => {
		// The whole failure strategy in one assertion: idempotent retry, no compensation logic.
		const initial = blobOf(credential("a"), credential("b"), credential("c"));
		const failing = vault({ ...initial }, { failSetAfter: 2 });
		await expect(ensureMigrated()).rejects.toThrow();
		const partial = Object.fromEntries(failing.store);
		restore?.();
		resetMigrationState();

		const healthy = vault(partial);
		const result = await ensureMigrated();

		expect(result).toMatchObject({ migrated: true, count: 3 });
		expect(healthy.credentialIds()).toEqual(["a", "b", "c"]);
		expect(healthy.index()?.count).toBe(3);
	});

	test("refuses to remove the blob when verification fails", async () => {
		// The one genuinely unsafe window — blob deleted, entries incomplete — is closed by
		// re-reading before the rename. Here the sentinel write fails, so the blob must stay.
		const v = vault(blobOf(credential("a"), credential("b")), { failSetAfter: 2 });

		await expect(ensureMigrated()).rejects.toThrow();

		expect(v.store.has(LEGACY_BUNDLE_KEY)).toBe(true);
		expect(v.index()).toBeUndefined();
	});
});

describe("credential migration: nothing to migrate", () => {
	test("writes nothing at all when storage is empty", async () => {
		const v = vault();

		const result = await ensureMigrated();

		expect(result).toMatchObject({ migrated: false, empty: true, count: 0 });
		expect(v.callsTo("secrets.set")).toBe(0);
		expect(v.store.size).toBe(0);
	});

	test("an empty blob does not produce a sentinel", async () => {
		// A sentinel with nothing behind it would report the provider as configured, and the
		// failure would only surface when a request was finally made.
		const v = vault({ [LEGACY_BUNDLE_KEY]: "[]" });

		const result = await ensureMigrated();

		expect(result.empty).toBe(true);
		expect(v.store.has(INDEX_KEY)).toBe(false);
		// The blob is left alone: refusing to act is the correct response to an empty read,
		// which may be a read failure rather than a user with no credentials.
		expect(v.store.has(LEGACY_BUNDLE_KEY)).toBe(true);
	});

	test("an unparseable blob still finishes the entries already written", async () => {
		// A corrupt blob must not block credentials that are already in the new layout.
		const v = vault({
			[LEGACY_BUNDLE_KEY]: "{not json",
			[credentialKey("a")]: JSON.stringify(credential("a")),
		});

		const result = await ensureMigrated();

		expect(result).toMatchObject({ migrated: true, count: 1 });
		expect(v.index()?.count).toBe(1);
	});

	test("an unparseable blob with nothing else writes no sentinel", async () => {
		const v = vault({ [LEGACY_BUNDLE_KEY]: "{not json" });

		const result = await ensureMigrated();

		expect(result.empty).toBe(true);
		expect(v.store.has(INDEX_KEY)).toBe(false);
	});
});

describe("credential migration: scale", () => {
	test("migrates a set far larger than the blob limit allowed", async () => {
		// The point of the rework. 400 credentials with JWT-sized tokens is over 1 MB as one
		// value: 16x the vault's per-value limit and 60x the plugin's config budget.
		const jwt = `eyJ${"A".repeat(1_000)}`;
		const many = Array.from({ length: 400 }, (_, index) =>
			credential(`cred-${index}`, { refreshToken: jwt, accessToken: jwt }),
		);
		const v = vault(blobOf(...many));

		const result = await ensureMigrated();

		expect(result.count).toBe(400);
		expect(v.credentialIds()).toHaveLength(400);
		const largest = Math.max(
			...[...v.store.entries()]
				.filter(([key]) => key.startsWith("provider.kiro.cred."))
				.map(([, value]) => value.length),
		);
		expect(largest).toBeLessThan(64 * 1024);
	});
});
