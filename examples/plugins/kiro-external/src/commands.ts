/**
 * The plugin's `handler: "server"` commands — everything the credential page can do.
 *
 * These cover the operations the built-in Kiro adapter exposes as REST endpoints. The
 * mapping is not one-to-one: `credentials.setState` covers disable, enable and reset, and
 * model listing is already served by `provider.listModels`, so nine commands cover eleven
 * built-in endpoints.
 *
 * ## How a change is persisted
 *
 * `MultiTokenManager` mutates its in-memory credential list, but the plugin cannot write the
 * host's vault directly. So every mutating command re-serializes the whole list and returns
 * it as a `secretWrites` entry; the host validates the key against the plugin's own declared
 * secret fields and applies it atomically (`plugin-command-secret-writes.ts`). The plugin
 * therefore holds no write authority of its own — it can only *propose* a new bundle for the
 * one key it owns.
 *
 * ## What is deliberately not returned
 *
 * No command returns credential material. `status` returns the manager's snapshot, which
 * carries ids, priorities, disabled state, counters and display names — never a token. That
 * matters because command output reaches the iframe.
 */

import { loadAllCredentials, loadUsageCache } from "@server/lib/kiro/config";
import { hasImportableSecret, normalizeImportCredentials } from "@server/lib/kiro/import-normalize";
import type { MultiTokenManager } from "@server/lib/kiro/multi-token-manager";
import type { KiroCredentials, SnapshotOptions } from "@server/lib/kiro/types";
import type { CommandConfigWrite, CommandSecretWrite } from "@server/lib/plugins/protocol";
import {
	credentialKey,
	INDEX_KEY,
	isCredentialKey,
	readIndex,
	readKeyState,
} from "./credential-vault";
import { isRecord } from "./rpc";

/**
 * A monotonic revision for the index sentinel.
 *
 * Time-based rather than a counter: the plugin process can restart between mutations, and a
 * counter starting from zero would re-issue a revision the cached manager already saw, so a
 * real change would look like no change. `Date.now()` also survives a restart, and the
 * `previous + 1` guard keeps it strictly increasing even if two writes land in the same
 * millisecond or the clock steps backwards.
 */
function nextRevision(previous: number | undefined): number {
	const now = Date.now();
	return previous !== undefined && previous >= now ? previous + 1 : now;
}

/**
 * Config key for the load-balancing mode. Non-secret, so it travels via `configWrites` rather
 * than the vault; the host refuses a `configWrites` key that names a secret field.
 */
const LOAD_BALANCING_KEY = "provider.kiro.loadBalancingMode";

export interface CommandOutcome {
	output: unknown;
	secretWrites?: CommandSecretWrite[];
	/** Non-secret provider config the host should persist. */
	configWrites?: CommandConfigWrite[];
}

export class CommandInputError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "CommandInputError";
	}
}

function requireString(value: unknown, field: string): string {
	if (typeof value !== "string" || value.trim().length === 0) {
		throw new CommandInputError(`${field} is required`);
	}
	return value.trim();
}

function credentialId(input: unknown): string {
	if (!isRecord(input)) throw new CommandInputError("credentialId is required");
	return requireString(input.credentialId, "credentialId");
}

/**
 * The manager's live credential list, as one vault write per credential plus the sentinel.
 *
 * `MultiTokenManager` has no public credential accessor — `snapshot()` is deliberately
 * metadata-only so display code cannot reach secrets. But every mutation persists the full
 * list to the `credentialsPath` the plugin supplied (its own data directory, never the
 * host's file), so reading that file back is the supported way to obtain the post-mutation
 * state without modifying the built-in module.
 *
 * The file is a staging area, not the durable store: the vault entries the host writes are
 * what survive, and the plugin's data directory is temporary when the host provides none.
 *
 * ## Write ordering, and why it is this way
 *
 * `secretWrites` is applied in order and is **not** atomic: an I/O failure partway through
 * leaves earlier entries persisted (see `plugin-command-secret-writes.ts`). The order below is
 * chosen so that every partial application is still a coherent state:
 *
 * 1. credential entries, 2. deletions of entries no longer present, 3. the sentinel.
 *
 * The sentinel is last because it is the revision marker: if it lands, the credentials it
 * counts are already stored. Failing before it leaves the *old* revision in place, so the next
 * read reloads rather than trusting a cache that no longer matches — stale, but never
 * inconsistent.
 *
 * Deletions come before the sentinel for the same reason, and after the writes so that a
 * rename-shaped change (remove id A, add id B) never has both absent at once.
 */
async function currentBundle(
	manager: MultiTokenManager,
	credentialsPath: string,
): Promise<CommandSecretWrite[]> {
	const { credentials } = loadAllCredentials(credentialsPath);
	// Fall back to the snapshot count only to detect a genuinely empty result; writing an
	// empty set would otherwise silently clear the user's credentials.
	if (credentials.length === 0 && manager.snapshot().entries.length > 0) {
		throw new CommandInputError("Credential state could not be read back after the change");
	}

	const writes: CommandSecretWrite[] = [];
	const liveKeys = new Set<string>();
	for (const credential of credentials) {
		if (!credential.id) {
			// Without an id there is no key to store it under. Refused rather than dropped:
			// silently omitting a credential the user just added is worse than failing.
			throw new CommandInputError("A credential has no id and cannot be stored");
		}
		const key = credentialKey(credential.id);
		liveKeys.add(key);
		writes.push({ key, value: JSON.stringify(credential) });
	}

	// Removing a credential means deleting its key, which requires knowing which keys exist.
	// One `secrets.list` per mutation, not per request: mutations are user-initiated and rare.
	const state = await readKeyState();
	for (const key of state.credentialKeys) {
		if (!liveKeys.has(key) && isCredentialKey(key)) writes.push({ key, value: null });
	}

	// `value: null` deletes. Zero credentials must delete the sentinel rather than store
	// `count: 0`, because `isUsable()`/`secretsSet` only test whether the key exists with a
	// non-empty value — a stored zero would report "configured" with nothing to serve.
	const revision = nextRevision((await readIndex())?.revision);
	writes.push(
		credentials.length === 0
			? { key: INDEX_KEY, value: null }
			: { key: INDEX_KEY, value: JSON.stringify({ revision, count: credentials.length }) },
	);
	return writes;
}

/** A positive integer from untrusted input, or undefined when absent. */
function optionalPositiveInt(value: unknown, field: string, max: number): number | undefined {
	if (value === undefined || value === null) return undefined;
	if (typeof value !== "number" || !Number.isFinite(value) || value < 1) {
		throw new CommandInputError(`${field} must be a positive number`);
	}
	return Math.min(Math.trunc(value), max);
}

/** Tier pagination requested by the credential page. */
function readSnapshotOptions(input: unknown): SnapshotOptions | undefined {
	if (!isRecord(input)) return undefined;
	const pageSize = optionalPositiveInt(input.pageSize, "pageSize", 100);
	// Page numbers are meaningless without a page size, and passing them through would let
	// the caller believe it paginated when `snapshot()` silently returned everything.
	if (pageSize === undefined) return undefined;
	return {
		pageSize,
		paidPage: optionalPositiveInt(input.paidPage, "paidPage", 100_000) ?? 1,
		enterprisePage: optionalPositiveInt(input.enterprisePage, "enterprisePage", 100_000) ?? 1,
		freePage: optionalPositiveInt(input.freePage, "freePage", 100_000) ?? 1,
	};
}

/**
 * The credential page's whole read model.
 *
 * Two things are trimmed rather than returned verbatim, because command output crosses into
 * an iframe and is capped:
 *
 * - `availableModels` becomes a count. The full list is per-credential and can run to dozens
 *   of ids; the page only ever shows the number, and `models.list` serves the detail.
 * - `usageCache` is filtered to the credentials on the current page. The cache covers every
 *   credential ever queried, which for a large pool is mostly rows nothing will render.
 *
 * `entries` is kept alongside the tier groups because a paginated snapshot already defines it
 * as the concatenation of the three pages — dropping it would break nothing but leaves
 * callers without a flat list to search.
 */
function statusOutput(manager: MultiTokenManager, input?: unknown, credentialsPath?: string) {
	const options = readSnapshotOptions(input);
	const snapshot = manager.snapshot(options);

	const trim = (entries: typeof snapshot.entries) =>
		entries.map(({ availableModels, ...rest }) => ({
			...rest,
			modelCount: availableModels?.length ?? 0,
		}));

	const visibleIds = new Set(snapshot.entries.map((entry) => entry.id));
	// `MultiTokenManager` keeps its usage cache private, but persists it next to the
	// credentials file the plugin itself supplied, so reading it back is the supported way to
	// obtain it without modifying the built-in module.
	const usageCache: Record<string, unknown> = {};
	if (credentialsPath) {
		try {
			for (const [id, entry] of Object.entries(loadUsageCache(credentialsPath))) {
				if (visibleIds.has(id)) usageCache[id] = entry;
			}
		} catch {
			// A missing or unreadable cache is normal (nothing queried yet) and must not take
			// the whole page down with it.
		}
	}

	return {
		// Retained for older panels that read these two names.
		credentialCount: snapshot.total,
		availableCount: snapshot.available,
		total: snapshot.total,
		available: snapshot.available,
		loadBalancingMode: snapshot.loadBalancingMode,
		currentId: snapshot.currentId,
		entries: trim(snapshot.entries),
		paidEntries: trim(snapshot.paidEntries),
		enterpriseEntries: trim(snapshot.enterpriseEntries),
		freeEntries: trim(snapshot.freeEntries),
		paidTotal: snapshot.paidTotal,
		enterpriseTotal: snapshot.enterpriseTotal,
		freeTotal: snapshot.freeTotal,
		unhealthyTotal: snapshot.unhealthyTotal,
		usageCache,
	};
}

export interface CommandContext {
	manager: MultiTokenManager;
	/** Where the manager persists its list; read back to build the bundle. */
	credentialsPath: string;
}

export type CommandHandler = (input: unknown, context: CommandContext) => Promise<CommandOutcome>;

export const COMMAND_HANDLERS: Record<string, CommandHandler> = {
	/**
	 * Credential list, per-credential state, tier pagination and load-balancing mode.
	 *
	 * Accepts `{ pageSize, paidPage, enterprisePage, freePage }`; without `pageSize` the whole
	 * pool is returned, which is what an older panel expects.
	 */
	status: async (input, { manager, credentialsPath }) => ({
		output: statusOutput(manager, input, credentialsPath),
	}),

	/**
	 * Add credentials from a pasted export.
	 *
	 * Reuses the built-in normalizer so the accepted formats are identical; anything without
	 * a usable secret is counted as ignored rather than stored as a broken entry.
	 */
	"credentials.import": async (input, { manager, credentialsPath }) => {
		if (!isRecord(input)) throw new CommandInputError("credentials payload is required");
		const raw = input.credentials;
		const records: unknown[] = Array.isArray(raw)
			? raw
			: isRecord(raw)
				? Object.values(raw)
				: (() => {
						throw new CommandInputError("credentials must be an array or object");
					})();
		// `normalizeImportCredentials` reshapes but does not filter, so entries carrying no
		// token material must be dropped here — storing one would create a credential that can
		// never authenticate and would show up as a mysterious failure later.
		const normalized = normalizeImportCredentials(records).filter(hasImportableSecret);
		if (normalized.length === 0) {
			throw new CommandInputError("No importable credentials were found in the payload");
		}
		const result = manager.addCredentials(normalized);
		return {
			output: { added: result.added, duplicates: result.duplicates },
			secretWrites: await currentBundle(manager, credentialsPath),
		};
	},

	/** Disable, enable, or reset-and-enable one credential. */
	"credentials.setState": async (input, { manager, credentialsPath }) => {
		const id = credentialId(input);
		const state = requireString(isRecord(input) ? input.state : undefined, "state");
		switch (state) {
			case "disabled":
				manager.setDisabled(id, true);
				break;
			case "enabled":
				manager.setDisabled(id, false);
				break;
			case "reset":
				// Clears failure counters and the disabled reason, which plain enabling does not.
				manager.resetAndEnable(id);
				break;
			default:
				throw new CommandInputError(`state must be disabled, enabled or reset: ${state}`);
		}
		return {
			output: statusOutput(manager, input, credentialsPath),
			secretWrites: await currentBundle(manager, credentialsPath),
		};
	},

	"credentials.remove": async (input, { manager, credentialsPath }) => {
		const id = credentialId(input);
		manager.removeCredential(id);
		return {
			output: statusOutput(manager, input, credentialsPath),
			secretWrites: await currentBundle(manager, credentialsPath),
		};
	},

	/**
	 * Remove several credentials at once.
	 *
	 * Separate from repeated `credentials.remove` calls because each of those persists the
	 * bundle and returns a full snapshot; a 20-credential selection would mean 20 vault writes
	 * and 20 round trips, with a partially-applied selection if one failed midway.
	 */
	"credentials.removeMany": async (input, { manager, credentialsPath }) => {
		const raw = isRecord(input) ? input.credentialIds : undefined;
		if (!Array.isArray(raw) || raw.length === 0) {
			throw new CommandInputError("credentialIds must be a non-empty array");
		}
		if (raw.length > 500) throw new CommandInputError("credentialIds exceeds the 500 id limit");
		const ids = raw.map((value, index) => requireString(value, `credentialIds[${index}]`));
		const result = manager.removeCredentials(ids);
		return {
			output: {
				removed: result.removed,
				notFound: result.notFound,
				status: statusOutput(manager, input, credentialsPath),
			},
			secretWrites: await currentBundle(manager, credentialsPath),
		};
	},

	/**
	 * Remove every credential the manager disabled for an unrecoverable reason.
	 *
	 * The definition of "unhealthy" stays inside `MultiTokenManager`, which is the point: the
	 * panel must not carry its own copy of that rule, or the button would drift from what the
	 * manager actually considers dead.
	 */
	"credentials.removeUnhealthy": async (input, { manager, credentialsPath }) => {
		const result = manager.removeUnhealthyCredentials();
		return {
			output: {
				removed: result.removed,
				reasons: result.reasons,
				status: statusOutput(manager, input, credentialsPath),
			},
			// Nothing was removed → nothing to persist. Writing anyway would be a pointless
			// vault round trip on a button the user can press when the count is 0.
			...(result.removed.length > 0
				? { secretWrites: await currentBundle(manager, credentialsPath) }
				: {}),
		};
	},

	"credentials.setPriority": async (input, { manager, credentialsPath }) => {
		const id = credentialId(input);
		const priority = isRecord(input) ? input.priority : undefined;
		if (typeof priority !== "number" || !Number.isFinite(priority)) {
			throw new CommandInputError("priority must be a number");
		}
		manager.setPriority(id, Math.trunc(priority));
		return {
			output: statusOutput(manager, input, credentialsPath),
			secretWrites: await currentBundle(manager, credentialsPath),
		};
	},

	/**
	 * Force a token refresh.
	 *
	 * The refreshed access token belongs in the stored bundle, so this is a mutating command
	 * even though the user did not change anything.
	 */
	"credentials.refresh": async (input, { manager, credentialsPath }) => {
		const id = credentialId(input);
		const models = await manager.fetchAndStoreModels(id);
		return {
			output: { credentialId: id, modelCount: models.length },
			secretWrites: await currentBundle(manager, credentialsPath),
		};
	},

	/**
	 * Edit credential fields including secrets.
	 *
	 * Text fields: empty string clears (the manager deletes the key). Secret fields: empty
	 * string means "keep existing" (the UI never echoes stored secrets back, so blanks are
	 * intentional no-ops). `authMethod` changes may trigger secret requirements — the
	 * manager validates internally and throws if switching to api_key without a key, etc.
	 */
	"credentials.update": async (input, { manager, credentialsPath }) => {
		const id = credentialId(input);
		const patch = isRecord(input) && isRecord(input.fields) ? input.fields : undefined;
		if (!patch) throw new CommandInputError("fields is required");
		const allowed: Record<string, unknown> = {};

		// Non-secret text fields: any string (including empty to clear).
		const textFields = [
			"email",
			"displayName",
			"region",
			"authRegion",
			"apiRegion",
			"profileArn",
			"subscriptionTitle",
			"startUrl",
			"machineId",
			"proxyUrl",
			"proxyUsername",
		] as const;
		for (const field of textFields) {
			const value = patch[field];
			if (typeof value === "string") allowed[field] = value;
		}

		// Secret fields: non-empty means "replace", empty/absent means "keep".
		const secretFields = [
			"refreshToken",
			"apiKey",
			"clientId",
			"clientSecret",
			"proxyPassword",
		] as const;
		for (const field of secretFields) {
			const value = patch[field];
			if (typeof value === "string" && value.trim().length > 0) allowed[field] = value;
		}

		// authMethod: validated by the manager's internal logic.
		if (typeof patch.authMethod === "string" && patch.authMethod.trim().length > 0) {
			allowed.authMethod = patch.authMethod.trim();
		}

		// priority: numeric field.
		if (typeof patch.priority === "number" && Number.isFinite(patch.priority)) {
			allowed.priority = Math.trunc(patch.priority);
		}

		if (Object.keys(allowed).length === 0) {
			throw new CommandInputError("No editable fields were provided");
		}
		manager.updateCredential(id, allowed as Partial<KiroCredentials>);
		return {
			output: statusOutput(manager, input, credentialsPath),
			secretWrites: await currentBundle(manager, credentialsPath),
		};
	},

	/** Quota and usage figures for one credential. */
	"credentials.usage": async (input, { manager }) => {
		const id = credentialId(input);
		const usage = await manager.getUsageLimits(id);
		return { output: { credentialId: id, usage } };
	},

	"config.setLoadBalancing": async (input, { manager }) => {
		const mode = requireString(isRecord(input) ? input.mode : undefined, "mode");
		// `balanced` is the built-in's spelling for round-robin across tiers.
		if (mode !== "priority" && mode !== "balanced") {
			throw new CommandInputError(`mode must be priority or balanced: ${mode}`);
		}
		manager.setLoadBalancingMode(mode);
		// Two steps, both required. `setLoadBalancingMode` changes the live manager so the next
		// request routes the new way; `configWrites` asks the host to persist it. Without the
		// second, the switch worked until the next restart and then silently reverted — which is
		// worse than not offering the control at all.
		return {
			output: { loadBalancingMode: mode },
			configWrites: [{ key: LOAD_BALANCING_KEY, value: mode }],
		};
	},

	/** Confirm a stored credential works, without revealing it. */
	"verify-credential": async (_input, { manager }) => {
		const result = await manager.listAvailableModels();
		return { output: { ok: true, modelCount: result.models.length } };
	},

	/**
	 * Fetch available models for one (or the current) credential.
	 *
	 * The host's `provider.listModels` manages the global model catalog; this command
	 * exposes per-credential model lists so the UI can show what each credential sees.
	 * It does NOT manage hide/show visibility — that belongs to the host's catalog layer.
	 */
	"models.list": async (input, { manager }) => {
		const id = isRecord(input) ? (input.credentialId as string | undefined) : undefined;
		const result = await manager.listAvailableModels(id || undefined);
		// Return only safe metadata: model id, display name, context window — never tokens.
		const models = result.models.map((m) => ({
			id: String(m.model_id ?? m.modelId ?? ""),
			displayName: String(m.display_name ?? m.displayName ?? m.model_id ?? m.modelId ?? ""),
			contextWindow: typeof m.context_window === "number" ? m.context_window : undefined,
		}));
		return { output: { credentialId: result.credentialId, models } };
	},

	/**
	 * Refresh model cache for one credential and persist updated list.
	 *
	 * Calls `fetchAndStoreModels` which re-fetches from upstream and writes back
	 * to the credential's `availableModels` field, then persist via bundle write.
	 */
	"models.refresh": async (input, { manager, credentialsPath }) => {
		const id = credentialId(input);
		const modelIds = await manager.fetchAndStoreModels(id);
		return {
			output: { credentialId: id, modelCount: modelIds.length, models: modelIds },
			secretWrites: await currentBundle(manager, credentialsPath),
		};
	},

	/**
	 * Refresh every enabled credential's model list in one pass.
	 *
	 * The panel used to do this by calling `models.refresh` per credential from the iframe,
	 * which meant one command round trip and one vault write each, and a partially-refreshed
	 * pool if any single call failed. `fetchAllCredentialModels` already walks the pool and
	 * skips disabled entries, so the loop belongs here.
	 *
	 * Only the union's size is returned: the panel shows a count, and a large pool's combined
	 * model list is exactly the kind of payload that would push output toward its cap.
	 */
	"models.refreshAll": async (_input, { manager, credentialsPath }) => {
		const models = await manager.fetchAllCredentialModels();
		return {
			output: { modelCount: models.length },
			secretWrites: await currentBundle(manager, credentialsPath),
		};
	},
};

export function findCommand(contributionId: string): CommandHandler | undefined {
	return COMMAND_HANDLERS[contributionId];
}
