/**
 * Credential storage: one vault secret per credential, plus an index sentinel.
 *
 * ## Why not one JSON blob
 *
 * This used to be a single secret (`provider.kiro.credentialsBundle`) holding a JSON array of
 * every credential, injected into `config` on every provider call. That made credential
 * *count* grow a single *value*, which collides with three separate ceilings: the plugin's own
 * `maxConfigBytes`, the vault's 64 KB per-value limit, and — worst — the cost of serializing
 * the whole set on every chat request. A few hundred credentials is simply not expressible.
 *
 * The vault has no such constraint. Its key space is free-form (any name up to 256 bytes) and
 * its per-plugin entry limit is 2,000, so one credential per key supports far more than the
 * blob ever could while giving each credential its own 64 KB budget. `secretWrites` explicitly
 * sanctions this shape: "a plugin can manage credential keys it could not have enumerated in a
 * static manifest — rotating tokens, or one entry per signed-in account."
 *
 * ## The index sentinel
 *
 * `provider.kiro.credentialsIndex` carries `{ revision, count }` and does four jobs:
 *
 * 1. **`requiresConfig` / `secretsSet` support.** Both host mechanisms only look at keys
 *    declared in `configSchema`, and a per-credential key cannot be declared statically (its
 *    id is generated at runtime). The sentinel is a fixed two-segment key, so the existing
 *    host logic keeps working with no host change.
 * 2. **Revision marker.** Reading N credentials means N RPC round trips, so the steady-state
 *    path reads only the sentinel and reuses the cached manager when `revision` is unchanged.
 * 3. **Migration completion marker.** The sentinel is written *last*, and only after the
 *    entry count is verified, so its presence means "migration finished".
 * 4. **Emptiness signal.** The sentinel is *deleted* when the last credential goes away — see
 *    below, this one is load-bearing.
 *
 * ## Why an empty index must delete the key rather than store zero
 *
 * `isUsable()` and `secretsSet` test whether the key exists with a non-empty value. A stored
 * `{"revision":7,"count":0}` is a non-empty string, so the search channel and the admin UI
 * would both report "configured" while no credential exists — and the failure would only
 * surface when a request is actually made. Invariant: **the sentinel exists if and only if at
 * least one credential exists.**
 */

import type { KiroCredentials } from "@server/lib/kiro/types";
import { isRecord, log, request as rpcRequest } from "./rpc";

/** Sends a Plugin→Host request. */
export type VaultTransport = (method: string, params: unknown) => Promise<unknown>;

let transport: VaultTransport = rpcRequest;

/**
 * Replace the transport, returning a function that restores the previous one.
 *
 * A seam for tests only. The real `request` writes framed JSON-RPC to stdout, where stdout
 * *is* the protocol channel — so a test that exercised it would corrupt the stream it shares
 * with the test runner's own output. Injection is therefore not a convenience here; there is
 * no way to test this module against the real transport at all.
 */
export function setVaultTransport(next: VaultTransport): () => void {
	const previous = transport;
	transport = next;
	return () => {
		transport = previous;
	};
}

function request(method: string, params: unknown): Promise<unknown> {
	return transport(method, params);
}

/** Prefix for per-credential secrets. The suffix is the credential id. */
const CREDENTIAL_KEY_PREFIX = "provider.kiro.cred.";

/** Fixed key carrying `{ revision, count }`. Absent when there are no credentials. */
export const INDEX_KEY = "provider.kiro.credentialsIndex";

/**
 * Legacy single-blob key. Read only by migration, never on the request path.
 *
 * Kept as a constant rather than inlined because migration both reads it and renames it, and
 * the two must agree.
 */
export const LEGACY_BUNDLE_KEY = "provider.kiro.credentialsBundle";

/** Where the legacy blob is parked after a successful migration. */
export const MIGRATED_BUNDLE_KEY = `${LEGACY_BUNDLE_KEY}.migrated`;

export interface CredentialIndex {
	revision: number;
	count: number;
}

export function credentialKey(id: string): string {
	return `${CREDENTIAL_KEY_PREFIX}${id}`;
}

export function isCredentialKey(key: string): boolean {
	return key.startsWith(CREDENTIAL_KEY_PREFIX);
}

/** Snapshot of which keys exist, taken with a single `secrets.list`. */
export interface VaultKeyState {
	credentialKeys: string[];
	hasIndex: boolean;
	hasLegacyBundle: boolean;
}

/**
 * List the plugin's own secret keys.
 *
 * One RPC call. Note the host caps a `secrets.list` response at 64 KB; a few hundred keys of
 * this shape is roughly 20 KB, so the practical ceiling is around a thousand credentials. Past
 * that the host would need to paginate this method.
 */
export async function readKeyState(): Promise<VaultKeyState> {
	const result = await request("secrets.list", {});
	const raw = isRecord(result) && Array.isArray(result.secrets) ? result.secrets : [];
	const keys = raw
		.map((item) => (isRecord(item) && typeof item.key === "string" ? item.key : undefined))
		.filter((key): key is string => key !== undefined);
	return {
		credentialKeys: keys.filter(isCredentialKey).sort(),
		hasIndex: keys.includes(INDEX_KEY),
		hasLegacyBundle: keys.includes(LEGACY_BUNDLE_KEY),
	};
}

async function readSecret(key: string): Promise<string | undefined> {
	const result = await request("secrets.get", { key });
	const value = isRecord(result) ? result.value : undefined;
	return typeof value === "string" && value.length > 0 ? value : undefined;
}

export async function writeSecret(key: string, value: string): Promise<void> {
	await request("secrets.set", { key, value });
}

export async function deleteSecret(key: string): Promise<void> {
	await request("secrets.delete", { key });
}

/**
 * Read the index, or `undefined` when it is absent or unreadable.
 *
 * A malformed index is treated as absent rather than fatal: that forces a full reload, which
 * is correct behaviour for "we cannot tell what revision this is".
 */
export async function readIndex(): Promise<CredentialIndex | undefined> {
	const raw = await readSecret(INDEX_KEY);
	if (!raw) return undefined;
	try {
		const parsed = JSON.parse(raw) as unknown;
		if (!isRecord(parsed)) return undefined;
		const revision = typeof parsed.revision === "number" ? parsed.revision : undefined;
		const count = typeof parsed.count === "number" ? parsed.count : undefined;
		if (revision === undefined || count === undefined) return undefined;
		return { revision, count };
	} catch {
		log("credential index is not valid JSON; treating it as absent");
		return undefined;
	}
}

/**
 * Write the index, or delete it when the count is zero.
 *
 * Centralised so the "zero means delete" invariant cannot be violated by a caller that
 * forgets it.
 */
export async function writeIndex(index: CredentialIndex): Promise<void> {
	if (index.count <= 0) {
		await deleteSecret(INDEX_KEY);
		return;
	}
	await writeSecret(INDEX_KEY, JSON.stringify(index));
}

/**
 * Read the legacy single-blob value.
 *
 * The only consumer is migration. The request path never reads this: it recognises credential
 * entries and the sentinel, nothing else.
 */
export async function readLegacyBundle(): Promise<string | undefined> {
	return readSecret(LEGACY_BUNDLE_KEY);
}

/** Read one credential. Returns `undefined` when missing or unparseable. */
export async function readCredential(key: string): Promise<KiroCredentials | undefined> {
	const raw = await readSecret(key);
	if (!raw) return undefined;
	try {
		const parsed = JSON.parse(raw) as unknown;
		return isRecord(parsed) ? (parsed as KiroCredentials) : undefined;
	} catch {
		// Named, never valued: a credential is the one thing that must not reach a log.
		log(`stored credential could not be parsed: ${key}`);
		return undefined;
	}
}

/**
 * Read every credential named by `keys`.
 *
 * Sequential rather than parallel: each call is a stdio round trip on one connection, and
 * flooding it gains nothing while making a slow host look like a hang. Unreadable entries are
 * skipped rather than failing the batch — one corrupted credential should not lock the user
 * out of the rest.
 */
export async function readCredentials(keys: readonly string[]): Promise<KiroCredentials[]> {
	const credentials: KiroCredentials[] = [];
	for (const key of keys) {
		const credential = await readCredential(key);
		if (credential) credentials.push(credential);
	}
	return credentials;
}

/** Persist one credential under its own key. */
export async function writeCredential(credential: KiroCredentials): Promise<void> {
	if (!credential.id) throw new Error("Cannot store a credential without an id");
	await writeSecret(credentialKey(credential.id), JSON.stringify(credential));
}
