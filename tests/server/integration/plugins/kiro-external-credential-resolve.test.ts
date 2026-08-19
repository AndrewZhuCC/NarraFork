/**
 * `resolveManager`: reading credentials from the vault, and the revision cache that makes it
 * affordable.
 *
 * ## Why a cache is mandatory rather than an optimisation
 *
 * Every Plugin→Host call is a real stdio JSON-RPC round trip — its own id, pending-map entry
 * and 30-second timeout — so it is nothing like an in-process read. Rebuilding the manager from
 * the vault costs one call per credential, which at a few hundred credentials would mean
 * hundreds of round trips *per chat request*.
 *
 * The previous cache keyed on a SHA-256 of the credential set, which only worked because the
 * credentials were already inlined in `config`, making the hash free. Reading them from the
 * vault inverts that: computing the key would require the very reads the cache exists to avoid.
 * So the key is the sentinel's `revision`, obtainable in one call before reading anything.
 *
 * ## What is asserted
 *
 * Steady state is one call. A changed revision reloads. And "no sentinel" is resolved into its
 * distinct cases rather than collapsed into "unconfigured" — an interrupted sentinel write must
 * not present a user's credentials as missing.
 */

import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
	credentialKey,
	INDEX_KEY,
	setVaultTransport,
} from "../../../../examples/plugins/kiro-external/src/credential-vault";
import {
	MissingCredentialError,
	resetManager,
	resolveManager,
} from "../../../../examples/plugins/kiro-external/src/credentials";

let dir: string;
let restore: (() => void) | undefined;

/** Counts calls per method so the round-trip cost is itself under test. */
function vault(initial: Record<string, string> = {}) {
	const store = new Map(Object.entries(initial));
	const counts = new Map<string, number>();
	restore = setVaultTransport(async (method, params) => {
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
		callsTo: (method: string) => counts.get(method) ?? 0,
		total: () => [...counts.values()].reduce((sum, n) => sum + n, 0),
		reset: () => counts.clear(),
	};
}

function storedCredential(id: string, refreshToken = `refresh-${id}`): [string, string] {
	return [
		credentialKey(id),
		JSON.stringify({ id, authMethod: "social", refreshToken, region: "us-east-1" }),
	];
}

function sentinel(revision: number, count: number): [string, string] {
	return [INDEX_KEY, JSON.stringify({ revision, count })];
}

beforeEach(() => {
	dir = join(tmpdir(), `kiro-resolve-${process.pid}-${Math.random().toString(36).slice(2)}`);
	mkdirSync(dir, { recursive: true });
	// The manager persists into this directory; without it the plugin would fall back to a
	// path derived from cwd, which for a repo-shipped plugin means writing into source control.
	process.env.NF_PLUGIN_DATA_DIR = dir;
	resetManager();
});

afterEach(() => {
	restore?.();
	restore = undefined;
	resetManager();
	delete process.env.NF_PLUGIN_DATA_DIR;
	rmSync(dir, { recursive: true, force: true });
});

describe("resolveManager: reading from the vault", () => {
	test("loads every stored credential", async () => {
		vault(
			Object.fromEntries([
				storedCredential("a"),
				storedCredential("b"),
				storedCredential("c"),
				sentinel(100, 3),
			]),
		);

		const manager = await resolveManager({});
		expect(manager.snapshot().entries).toHaveLength(3);
	});

	test("steady state costs one round trip, regardless of credential count", async () => {
		const entries = Array.from({ length: 50 }, (_, index) => storedCredential(`cred-${index}`));
		const v = vault(Object.fromEntries([...entries, sentinel(100, 50)]));

		await resolveManager({});
		v.reset();

		// Five more requests against an unchanged revision.
		for (let index = 0; index < 5; index += 1) await resolveManager({});

		// One sentinel read each, and nothing else — not 5 x 50 credential reads.
		expect(v.callsTo("secrets.get")).toBe(5);
		expect(v.callsTo("secrets.list")).toBe(0);
		expect(v.total()).toBe(5);
	});

	test("a changed revision reloads the credentials", async () => {
		const v = vault(Object.fromEntries([storedCredential("a"), sentinel(100, 1)]));
		const first = await resolveManager({});
		expect(first.snapshot().entries).toHaveLength(1);

		// A mutation elsewhere: a second credential, and a bumped revision.
		const [key, value] = storedCredential("b");
		v.store.set(key, value);
		const [indexKey, indexValue] = sentinel(101, 2);
		v.store.set(indexKey, indexValue);

		const second = await resolveManager({});
		expect(second.snapshot().entries).toHaveLength(2);
		expect(second).not.toBe(first);
	});

	test("an unchanged revision returns the very same manager instance", async () => {
		// Identity matters, not just equality: the manager holds live access tokens and
		// in-flight rotation state. Rebuilding it would discard a valid token and re-authenticate
		// for no reason.
		vault(Object.fromEntries([storedCredential("a"), sentinel(100, 1)]));
		const first = await resolveManager({});
		const second = await resolveManager({});
		expect(second).toBe(first);
	});

	test("a load-balancing change rebuilds even though the revision did not move", async () => {
		// The revision tracks credentials, not config. Keying on it alone would silently ignore
		// a load-balancing switch until a credential happened to change.
		vault(Object.fromEntries([storedCredential("a"), sentinel(100, 1)]));
		const priority = await resolveManager({ loadBalancingMode: "priority" });
		const balanced = await resolveManager({ loadBalancingMode: "balanced" });
		expect(balanced).not.toBe(priority);
	});
});

describe("resolveManager: when there is no sentinel", () => {
	test("adopts orphaned credential entries rather than reporting none", async () => {
		// An interrupted write leaves entries with no sentinel. Treating that as "unconfigured"
		// would tell the user their credentials vanished; they are right there in the vault.
		vault(Object.fromEntries([storedCredential("a"), storedCredential("b")]));
		const manager = await resolveManager({});
		expect(manager.snapshot().entries).toHaveLength(2);
	});

	test("falls back to the legacy flat config fields", async () => {
		// A provider configured before multi-credential support keeps working without the user
		// re-entering anything.
		vault();
		const manager = await resolveManager({
			authMethod: "social",
			refreshToken: "legacy-refresh",
		});
		expect(manager.snapshot().entries).toHaveLength(1);
	});

	test("reports genuinely unconfigured as a missing credential", async () => {
		vault();
		await expect(resolveManager({})).rejects.toBeInstanceOf(MissingCredentialError);
	});

	test("names what is missing instead of failing generically", async () => {
		// An `idc` credential needs the client pair; "credentials required" would leave the
		// user guessing which field to fill.
		vault();
		await expect(resolveManager({ authMethod: "idc", refreshToken: "rt" })).rejects.toThrow(
			/clientId/,
		);
	});
});

describe("resolveManager: when credential storage is unavailable", () => {
	test("reports an unconfigured provider rather than a broken one", async () => {
		// A host may serve no `secrets.*` channel at all — GA release validation starts the
		// plugin exactly that way, with an empty config and no vault. Propagating the transport
		// error made a legitimately unconfigured provider look like a crashed plugin, and failed
		// release validation for the whole package. The honest report is "not configured", which
		// callers already render as "needs configuration".
		restore = setVaultTransport(async () => {
			throw new Error("Unknown RPC method: secrets.get");
		});

		await expect(resolveManager({})).rejects.toBeInstanceOf(MissingCredentialError);
	});

	test("still uses legacy config fields when the vault is unreachable", async () => {
		// Losing the vault must not lock out a provider whose credentials are in config.
		restore = setVaultTransport(async () => {
			throw new Error("vault unavailable");
		});

		const manager = await resolveManager({
			authMethod: "social",
			refreshToken: "legacy-refresh",
		});
		expect(manager.snapshot().entries).toHaveLength(1);
	});
});

describe("resolveManager: a sentinel that disagrees with storage", () => {
	test("reports unreadable credentials rather than serving none", async () => {
		// The sentinel says credentials exist but nothing could be read. Reporting "no
		// credentials configured" is the honest answer; silently proceeding with an empty
		// manager would fail later as an upstream error.
		vault({ [INDEX_KEY]: JSON.stringify({ revision: 100, count: 2 }) });
		await expect(resolveManager({})).rejects.toBeInstanceOf(MissingCredentialError);
	});

	test("does not cache that failure", async () => {
		// The next call must retry. Caching an empty state would keep reporting "not
		// configured" after the user fixed it, until the process restarted.
		const v = vault({ [INDEX_KEY]: JSON.stringify({ revision: 100, count: 1 }) });
		await expect(resolveManager({})).rejects.toBeInstanceOf(MissingCredentialError);

		const [key, value] = storedCredential("a");
		v.store.set(key, value);

		const manager = await resolveManager({});
		expect(manager.snapshot().entries).toHaveLength(1);
	});

	test("a malformed sentinel still finds the credentials", async () => {
		// Unreadable revision means "reload", not "fail": the entries are intact.
		vault({ ...Object.fromEntries([storedCredential("a")]), [INDEX_KEY]: "{not json" });
		const manager = await resolveManager({});
		expect(manager.snapshot().entries).toHaveLength(1);
	});
});
