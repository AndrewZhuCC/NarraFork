/**
 * One-time migration from the single credential blob to one vault entry per credential.
 *
 * ## Why the plugin runs this, not the host
 *
 * The host knows nothing about the blob's internal format — a 22-field credential shape with
 * per-method requirements and legacy aliases. A host-side migration would have to copy that
 * table into the host, where it would drift from the plugin's own copy. There is also no
 * per-plugin migration hook. The plugin's binding is established *before* its process starts
 * (`plugin-manager.ts` injects the dispatcher first), so `secrets.*` is already available
 * during activation.
 *
 * ## The completion marker is the sentinel, and this is a data-safety decision
 *
 * It is tempting to treat "any `cred.*` key exists" as "already migrated". That is wrong in a
 * way that destroys credentials. Consider 400 credentials where the process dies after 200
 * entries are written but before the sentinel:
 *
 * - "any cred key exists" ⇒ migration reports done ⇒ the remaining 200 are never written;
 * - meanwhile no sentinel means `resolveManager` sees no index;
 * - the old blob is still there, but the read path does not consult it.
 *
 * Half the credentials are stranded and nothing ever self-heals. Both safety nets fail at once.
 * With the sentinel plus a `count` that must agree with the number of stored entries, that
 * state is correctly identified as *unfinished*, and the resume path merges the 200 already
 * written with the blob to converge.
 *
 * Since IDC refresh tokens are single-use and unrecoverable once rotated, losing a credential
 * is not a cosmetic failure. That is why the marker is the sentinel and why step 6 re-reads
 * before anything is deleted.
 *
 * ## Failure handling: no rollback, idempotent retry
 *
 * Each `secrets.set` is its own vault transaction and a batch is not atomic. So any interrupted
 * run leaves "some `cred.*` entries + no sentinel + the blob still present" — exactly the state
 * this function recognises and resumes. The only genuinely dangerous window is "old blob
 * deleted, new entries incomplete", which the verify-then-rename ordering closes.
 *
 * What must never happen: writing a sentinel or removing the blob when no credential could be
 * read. An empty read is a read failure, not a user clearing their credentials.
 */

import type { KiroCredentials } from "@server/lib/kiro/types";
import {
	credentialKey,
	deleteSecret,
	LEGACY_BUNDLE_KEY,
	MIGRATED_BUNDLE_KEY,
	readCredentials,
	readIndex,
	readKeyState,
	readLegacyBundle,
	writeIndex,
	writeSecret,
} from "./credential-vault";
import { parseBundle } from "./credentials";
import { log } from "./rpc";

export interface MigrationResult {
	/** True when storage ends in the migrated shape: entries present and sentinel written. */
	migrated: boolean;
	/** True when there was genuinely nothing to migrate. Nothing was written. */
	empty: boolean;
	/** How many credentials are stored. Zero when `empty`. */
	count: number;
	/** True when this call did work, as opposed to finding the migration already complete. */
	changed: boolean;
}

/** In-flight run, so concurrent callers share one migration instead of racing. */
let inFlight: Promise<MigrationResult> | undefined;

/**
 * Migrate if needed. Safe to call repeatedly and from several places at once.
 *
 * Called at activation and from `resolveManager` when no sentinel is found. Both callers can
 * arrive concurrently — a provider request can land while activation is still running — so the
 * promise is shared rather than the work repeated.
 */
export function ensureMigrated(): Promise<MigrationResult> {
	if (!inFlight) {
		inFlight = runMigration().finally(() => {
			inFlight = undefined;
		});
	}
	return inFlight;
}

/** Drop the shared run. Tests only, so each case starts from a clean slate. */
export function resetMigrationState(): void {
	inFlight = undefined;
}

async function runMigration(): Promise<MigrationResult> {
	// 1. One listing tells us everything about which keys exist.
	const state = await readKeyState();

	// 2. Sentinel present *and* agreeing with what is stored ⇒ the credentials are migrated. A
	//    disagreeing count means an interrupted run, so fall through and converge rather than
	//    trusting it.
	if (state.hasIndex) {
		const index = await readIndex();
		if (index && index.count === state.credentialKeys.length) {
			// The sentinel is written before the blob is renamed, so a blob still sitting under
			// its original name means the run stopped between those two steps. The credentials
			// are safe — they were verified before the sentinel — but leaving the old key in
			// place keeps a duplicate copy of every secret in the vault indefinitely, so finish
			// the rename here rather than only on the path that created it.
			const renamed = state.hasLegacyBundle ? await retireLegacyBundle() : false;
			return { migrated: true, empty: false, count: index.count, changed: renamed };
		}
		log("credential index disagrees with stored entries; resuming migration", {
			indexCount: index?.count,
			storedEntries: state.credentialKeys.length,
		});
	}

	// 3. Build the source set. Entries already written win over the blob: a token rotation may
	//    have refreshed them since, and the blob would reintroduce a stale (single-use) token.
	const byId = new Map<string, KiroCredentials>();
	for (const credential of await readCredentials(state.credentialKeys)) {
		if (credential.id) byId.set(credential.id, credential);
	}
	const alreadyStored = new Set(byId.keys());

	if (state.hasLegacyBundle) {
		const blob = await readLegacyBundle();
		if (blob) {
			for (const credential of parseBundleSafely(blob)) {
				if (credential.id && !byId.has(credential.id)) byId.set(credential.id, credential);
			}
		}
	}

	// 4. Nothing to migrate. Write nothing — in particular, no sentinel, which would advertise
	//    a configured provider with nothing behind it.
	if (byId.size === 0) {
		return { migrated: false, empty: true, count: 0, changed: false };
	}

	// 5. Write the entries that are not already stored.
	let written = 0;
	for (const [id, credential] of byId) {
		if (alreadyStored.has(id)) continue;
		await writeSecret(credentialKey(id), JSON.stringify(credential));
		written += 1;
	}

	// 6. Re-read and verify before anything is removed. This is the step that closes the one
	//    unsafe window: deleting the blob while the new entries are incomplete.
	const after = await readKeyState();
	if (after.credentialKeys.length !== byId.size) {
		throw new Error(
			`Credential migration did not converge: expected ${byId.size} entries, found ${after.credentialKeys.length}. The original bundle was left untouched.`,
		);
	}

	// 7. The sentinel, written last, is the completion marker.
	await writeIndex({ revision: Date.now(), count: byId.size });

	// 8. Retire the blob, only now that the entries are verified and the sentinel is written.
	if (state.hasLegacyBundle) await retireLegacyBundle();

	log("credential migration complete", {
		total: byId.size,
		written,
		resumed: alreadyStored.size > 0,
	});
	return { migrated: true, empty: false, count: byId.size, changed: true };
}

/**
 * Whether storage still needs migrating, without doing any work.
 *
 * Used by the resolve path to decide whether an absent sentinel is worth acting on.
 */
export async function migrationPending(): Promise<boolean> {
	const state = await readKeyState();
	if (!state.hasIndex) return state.hasLegacyBundle || state.credentialKeys.length > 0;
	const index = await readIndex();
	return index === undefined || index.count !== state.credentialKeys.length;
}

/**
 * Move the blob aside, keeping its contents under a different key.
 *
 * Kept rather than deleted: it is the only copy of credentials predating the migration, and a
 * rotated IDC refresh token cannot be recovered if the new layout turns out to be wrong. Copy
 * first, then remove the original, so a failure between the two steps leaves the original
 * readable and the next run repeats this harmlessly.
 *
 * Returns whether anything was moved.
 */
async function retireLegacyBundle(): Promise<boolean> {
	const blob = await readLegacyBundle();
	if (!blob) return false;
	await writeSecret(MIGRATED_BUNDLE_KEY, blob);
	await deleteSecret(LEGACY_BUNDLE_KEY);
	return true;
}

/**
 * Parse the blob without letting a malformed one abort the run.
 *
 * A blob that cannot be parsed must not prevent already-written entries from being finished and
 * marked. Reported rather than swallowed, and never valued.
 */
function parseBundleSafely(blob: string): KiroCredentials[] {
	try {
		return parseBundle(blob);
	} catch (error) {
		log("legacy credential bundle could not be parsed; migrating what is already stored", {
			error: error instanceof Error ? error.message : "unknown",
		});
		return [];
	}
}
