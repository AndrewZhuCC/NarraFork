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

import { loadAllCredentials } from "@server/lib/kiro/config";
import { hasImportableSecret, normalizeImportCredentials } from "@server/lib/kiro/import-normalize";
import type { MultiTokenManager } from "@server/lib/kiro/multi-token-manager";
import type { KiroCredentials } from "@server/lib/kiro/types";
import type { CommandSecretWrite } from "@server/lib/plugins/protocol";
import { serializeBundle } from "./credentials";
import { isRecord } from "./rpc";

/** Vault key for the credential bundle. Must match the manifest's configSchema field. */
const BUNDLE_KEY = "provider.kiro.credentialsBundle";

export interface CommandOutcome {
	output: unknown;
	secretWrites?: CommandSecretWrite[];
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
 * The manager's live credential list, ready to be stored.
 *
 * `MultiTokenManager` has no public credential accessor — `snapshot()` is deliberately
 * metadata-only so display code cannot reach secrets. But every mutation persists the full
 * list to the `credentialsPath` the plugin supplied (its own data directory, never the
 * host's file), so reading that file back is the supported way to obtain the post-mutation
 * state without modifying the built-in module.
 *
 * The file is a staging area, not the durable store: the vault entry the host writes from
 * this bundle is what survives, and the plugin's data directory is temporary when the host
 * provides none.
 */
function currentBundle(manager: MultiTokenManager, credentialsPath: string): CommandSecretWrite[] {
	const { credentials } = loadAllCredentials(credentialsPath);
	// Fall back to the snapshot count only to detect a genuinely empty result; an empty
	// bundle would otherwise silently clear the user's credentials.
	if (credentials.length === 0 && manager.snapshot().entries.length > 0) {
		throw new CommandInputError("Credential state could not be read back after the change");
	}
	return [{ key: BUNDLE_KEY, value: serializeBundle(credentials) }];
}

/** Snapshot plus usage cache — the shape the credential page renders. */
function statusOutput(manager: MultiTokenManager): unknown {
	const snapshot = manager.snapshot();
	return {
		credentialCount: snapshot.entries.length,
		availableCount: snapshot.entries.filter((entry) => !entry.disabled).length,
		loadBalancingMode: snapshot.loadBalancingMode,
		currentId: snapshot.currentId,
		entries: snapshot.entries,
	};
}

export interface CommandContext {
	manager: MultiTokenManager;
	/** Where the manager persists its list; read back to build the bundle. */
	credentialsPath: string;
}

export type CommandHandler = (input: unknown, context: CommandContext) => Promise<CommandOutcome>;

export const COMMAND_HANDLERS: Record<string, CommandHandler> = {
	/** Credential list, per-credential state and load-balancing mode. */
	status: async (_input, { manager }) => ({ output: statusOutput(manager) }),

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
			secretWrites: currentBundle(manager, credentialsPath),
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
		return { output: statusOutput(manager), secretWrites: currentBundle(manager, credentialsPath) };
	},

	"credentials.remove": async (input, { manager, credentialsPath }) => {
		const id = credentialId(input);
		manager.removeCredential(id);
		return { output: statusOutput(manager), secretWrites: currentBundle(manager, credentialsPath) };
	},

	"credentials.setPriority": async (input, { manager, credentialsPath }) => {
		const id = credentialId(input);
		const priority = isRecord(input) ? input.priority : undefined;
		if (typeof priority !== "number" || !Number.isFinite(priority)) {
			throw new CommandInputError("priority must be a number");
		}
		manager.setPriority(id, Math.trunc(priority));
		return { output: statusOutput(manager), secretWrites: currentBundle(manager, credentialsPath) };
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
			secretWrites: currentBundle(manager, credentialsPath),
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
		return { output: statusOutput(manager), secretWrites: currentBundle(manager, credentialsPath) };
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
		// Not a secret, so the host's own config form owns persistence for this one; the
		// command only changes the live manager.
		return { output: { loadBalancingMode: mode } };
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
			secretWrites: currentBundle(manager, credentialsPath),
		};
	},
};

export function findCommand(contributionId: string): CommandHandler | undefined {
	return COMMAND_HANDLERS[contributionId];
}
