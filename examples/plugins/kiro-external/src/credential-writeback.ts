/**
 * Writing refreshed credentials back to the host vault.
 *
 * ## The failure this exists to prevent
 *
 * `token-manager.ts` refreshes with `refreshToken: data.refreshToken ?? creds.refreshToken`,
 * so when the upstream rotates a refresh token the new value is adopted. During a provider
 * operation, `MultiTokenManager.persistCredentials()` writes it to the plugin's own data
 * directory — but the host's vault entry, which is what gets injected into `config` on the
 * next process start, still holds the old value. For IDC accounts, where OIDC `CreateToken`
 * may rotate one-time refresh tokens, that combination is unrecoverable: after a restart the
 * plugin authenticates with a token the upstream already revoked.
 *
 * The credential-management commands already return `secretWrites`, so any manual operation
 * resyncs the vault. This module closes the gap for accounts that only ever see traffic.
 *
 * ## Why its own module
 *
 * `server.ts` calls `listen()` and writes a `hello` frame at import time, so tests importing
 * it would take over stdio. Splitting this out means tests exercise the real implementation
 * rather than a copy of its logic restated in the test file.
 */

import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { loadAllCredentials } from "@server/lib/kiro/config";
import type { KiroCredentials } from "@server/lib/kiro/types";
import { credentialKey, INDEX_KEY } from "./credential-vault";

/**
 * Vault key for the index sentinel.
 *
 * Must stay identical to `INDEX_KEY`: the command path writes it through `secretWrites` and
 * this path through `secrets.set`, and a mismatch would silently produce two competing
 * entries. Re-exported from the single definition rather than restated, because that mismatch
 * is exactly the failure mode a duplicated literal invites.
 */
export { INDEX_KEY };

/**
 * Fingerprint of the credentials file, or null when it does not exist yet.
 *
 * Cost, since this runs on every provider operation: one read of a file that holds at most a
 * few dozen credentials (~10 KB) plus a SHA-256 over it, so well under a millisecond against
 * an upstream round-trip measured in hundreds. Hashing rather than comparing parsed contents
 * keeps it cheap and catches any change the manager makes, including ones this module does
 * not model.
 *
 * Changed from sync `readFileSync` to async `readFile` to avoid blocking the plugin's main
 * thread on file-system IO (NFS, container FS hangs would otherwise stall all RPC handlers).
 */
export async function credentialFileHash(credentialsPath: string): Promise<string | null> {
	try {
		const content = await readFile(credentialsPath, "utf-8");
		return createHash("sha256").update(content).digest("hex");
	} catch {
		// Absent or unreadable: no baseline, so a later comparison simply reports "changed".
		return null;
	}
}

export interface WritebackDeps {
	/** Sends a Plugin→Host request. Injected so tests do not need a live transport. */
	request: (method: string, params: unknown) => Promise<unknown>;
	/** Diagnostic sink. Must never receive credential material. */
	log: (message: string, detail?: Record<string, unknown>) => void;
}

/**
 * Resync the vault if the credentials file changed during an operation.
 *
 * Fire-and-forget by design: a vault write failing must not fail the user's request. The
 * plugin's own file already holds the refreshed token, so it keeps working — only the vault
 * is stale, and the next operation that observes a change retries. That makes this
 * self-healing without a retry queue, which is why a persistent failure is logged once per
 * occurrence rather than escalated.
 *
 * Now async to match the async `credentialFileHash`. Callers that fire-and-forget must wrap
 * in a `.catch()` or `void` to prevent unhandled rejections — see the call sites in
 * server.ts where the `finally` block already uses `void writebackIfChanged(…).catch(…)`.
 */
export async function writebackIfChanged(
	credentialsPath: string,
	hashBefore: string | null,
	deps: WritebackDeps,
): Promise<void> {
	try {
		const hashAfter = await credentialFileHash(credentialsPath);
		if (!hashAfter || hashAfter === hashBefore) return;

		const { credentials } = loadAllCredentials(credentialsPath);
		// An empty read means something went wrong reading a file that just changed. Writing an
		// empty set would clear the user's credentials in the vault, so refuse instead.
		if (credentials.length === 0) return;

		await resyncVault(credentials, deps);
	} catch (error: unknown) {
		// Fire-and-forget: catch at the top level so callers using `void writebackIfChanged()`
		// never produce an unhandled rejection. This is the same resilience the previous sync
		// version had, just explicit.
		deps.log("credential writeback failed", {
			error: error instanceof Error ? error.message : "unknown",
		});
	}
}

/**
 * Write each changed credential to its own key, then bump the revision.
 *
 * Only the *entries* are written, not the deletions a command emits: a token rotation never
 * removes a credential, so a diff against the stored key list would be work with no effect.
 *
 * The sentinel is written last and only if every entry succeeded. That ordering is what makes
 * a partial failure safe: the revision still names the previous state, so the next read
 * reloads from the vault instead of trusting a cached manager that no longer matches. Writing
 * the sentinel first and failing afterwards would do the opposite — advertise a revision whose
 * credentials were never stored, and the rotated (single-use) refresh token would be lost.
 */
async function resyncVault(
	credentials: readonly KiroCredentials[],
	deps: WritebackDeps,
): Promise<void> {
	try {
		for (const credential of credentials) {
			if (!credential.id) continue;
			await deps.request("secrets.set", {
				key: credentialKey(credential.id),
				value: JSON.stringify(credential),
			});
		}
		const raw = await deps.request("secrets.get", { key: INDEX_KEY });
		const previous = readRevision(raw);
		const now = Date.now();
		await deps.request("secrets.set", {
			key: INDEX_KEY,
			value: JSON.stringify({
				revision: previous !== undefined && previous >= now ? previous + 1 : now,
				count: credentials.length,
			}),
		});
	} catch (error: unknown) {
		// Fire-and-forget by design: a vault write failing must not fail the user's request.
		// The plugin's own file already holds the refreshed token, so it keeps working — only
		// the vault is stale, and the next operation that observes a change retries.
		deps.log("credential writeback to the vault failed", {
			error: error instanceof Error ? error.message : "unknown",
		});
	}
}

/** The `revision` inside a `secrets.get` response, when there is a readable one. */
function readRevision(raw: unknown): number | undefined {
	if (typeof raw !== "object" || raw === null) return undefined;
	const value = (raw as { value?: unknown }).value;
	if (typeof value !== "string" || value.length === 0) return undefined;
	try {
		const parsed = JSON.parse(value) as unknown;
		if (typeof parsed !== "object" || parsed === null) return undefined;
		const revision = (parsed as { revision?: unknown }).revision;
		return typeof revision === "number" ? revision : undefined;
	} catch {
		return undefined;
	}
}
