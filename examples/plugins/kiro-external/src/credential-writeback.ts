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
import { readFileSync } from "node:fs";
import { loadAllCredentials } from "@server/lib/kiro/config";
import { serializeBundle } from "./credentials";

/**
 * Vault key for the credential bundle.
 *
 * Must stay identical to `BUNDLE_KEY` in `commands.ts`: the command path writes it through
 * `secretWrites` and this path through `secrets.set`, and a mismatch would silently produce
 * two competing vault entries.
 */
export const VAULT_BUNDLE_KEY = "provider.kiro.credentialsBundle";

/**
 * Fingerprint of the credentials file, or null when it does not exist yet.
 *
 * Cost, since this runs on every provider operation: one read of a file that holds at most a
 * few dozen credentials (~10 KB) plus a SHA-256 over it, so well under a millisecond against
 * an upstream round-trip measured in hundreds. Hashing rather than comparing parsed contents
 * keeps it cheap and catches any change the manager makes, including ones this module does
 * not model.
 */
export function credentialFileHash(credentialsPath: string): string | null {
	try {
		return createHash("sha256").update(readFileSync(credentialsPath, "utf-8")).digest("hex");
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
 */
export function writebackIfChanged(
	credentialsPath: string,
	hashBefore: string | null,
	deps: WritebackDeps,
): void {
	const hashAfter = credentialFileHash(credentialsPath);
	if (!hashAfter || hashAfter === hashBefore) return;

	const { credentials } = loadAllCredentials(credentialsPath);
	// An empty read means something went wrong reading a file that just changed. Writing an
	// empty bundle would clear the user's credentials in the vault, so refuse instead.
	if (credentials.length === 0) return;

	deps
		.request("secrets.set", { key: VAULT_BUNDLE_KEY, value: serializeBundle(credentials) })
		.catch((error: unknown) => {
			deps.log("secrets.set failed (credential writeback)", {
				error: error instanceof Error ? error.message : "unknown",
			});
		});
}
