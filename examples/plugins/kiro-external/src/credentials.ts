/**
 * Turns the config the host injects into a live `MultiTokenManager`.
 *
 * The host resolves this plugin's secret fields from its vault and sends them in
 * `config` with every `provider.chat` / `generate` / `listModels` call (see
 * `docs/plugin-system/04-server-rpc-and-provider.md` D-04). So credentials arrive as
 * *data*, per request, and this module never reads or writes host state.
 *
 * ## Why the manager is cached, and keyed by credential content
 *
 * `MultiTokenManager` owns a refreshed access token, adaptive concurrency state and
 * per-credential failure counts. Rebuilding it per request would discard the access token
 * and force a refresh on every call — slow, and enough of them look like credential abuse.
 *
 * But it cannot be cached indefinitely either: the host re-resolves secrets per request
 * precisely so a rotated or revoked credential takes effect immediately. The compromise is
 * a cache keyed by a hash of the credential material: same credentials reuse the manager,
 * changed credentials build a new one. The key is a SHA-256 digest so no secret value is
 * held as a plain map key.
 *
 * ## Why credentials are never written to disk
 *
 * `MultiTokenManager` persists refreshed tokens to `credentialsPath`. The built-in adapter
 * points that at the user's real `~/.narrafork/kiro/credentials.json`; this plugin points
 * it at its own plugin-data directory instead. Two reasons:
 *
 * - the plugin has no permission to touch host files, and the sandbox contract forbids a
 *   plugin from persisting credentials itself;
 * - sharing the file with the built-in adapter would mean two token managers refreshing
 *   the same credential and overwriting each other's refresh token — real data loss, which
 *   is why the plan chose independent credential storage over reuse.
 */

import { mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { canonicalizeAuthMethodValue } from "@server/lib/kiro/auth-method";
import { loadConfig } from "@server/lib/kiro/config";
import { MultiTokenManager } from "@server/lib/kiro/multi-token-manager";
import type { KiroCredentials } from "@server/lib/kiro/types";
import { ensureMigrated } from "./credential-migration";
import { readCredentials, readIndex, readKeyState } from "./credential-vault";
import { isRecord, log } from "./rpc";

/** Config keys this plugin understands. Anything else is ignored. */
export interface KiroPluginConfig {
	/**
	 * The old single-blob field, no longer part of `configSchema` and therefore never injected.
	 *
	 * Retained on the type only so migration can accept a config that still carries it — a
	 * host that has not yet reloaded the descriptor could still send one. It is *not* read by
	 * `resolveManager`; the durable blob is read from the vault by `ensureMigrated`, which is
	 * the only consumer left. Credentials travelling through `config` is the shape this
	 * rework exists to remove.
	 */
	credentialsBundle?: string;
	/** Legacy single-credential fields, still accepted. See `readConfig`. */
	authMethod?: string;
	refreshToken?: string;
	apiKey?: string;
	clientId?: string;
	clientSecret?: string;
	profileArn?: string;
	region?: string;
	loadBalancingMode?: string;
}

export class MissingCredentialError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "MissingCredentialError";
	}
}

/** Raised when a bundle exists but cannot be read, so it is never silently ignored. */
export class InvalidBundleError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "InvalidBundleError";
	}
}

function text(value: unknown): string | undefined {
	if (typeof value !== "string") return undefined;
	const trimmed = value.trim();
	return trimmed.length > 0 ? trimmed : undefined;
}

export function readConfig(raw: unknown): KiroPluginConfig {
	if (!isRecord(raw)) return {};
	return {
		credentialsBundle: text(raw.credentialsBundle),
		authMethod: text(raw.authMethod),
		refreshToken: text(raw.refreshToken),
		apiKey: text(raw.apiKey),
		clientId: text(raw.clientId),
		clientSecret: text(raw.clientSecret),
		profileArn: text(raw.profileArn),
		region: text(raw.region),
		loadBalancingMode: text(raw.loadBalancingMode),
	};
}

/** Fields a bundle entry may carry. Anything else is dropped rather than trusted. */
const BUNDLE_TEXT_FIELDS = [
	"id",
	"authMethod",
	"refreshToken",
	"accessToken",
	"apiKey",
	"clientId",
	"clientSecret",
	"profileArn",
	"region",
	"authRegion",
	"apiRegion",
	"expiresAt",
	"machineId",
	"email",
	"displayName",
	"subscriptionTitle",
	"startUrl",
	"disabledReason",
	// Per-credential proxy settings. `proxyPassword` is a secret but receives the same
	// treatment as `refreshToken`/`apiKey`/`clientSecret` above: it round-trips through
	// the bundle (a single writeOnly secret field) and never appears in log output.
	// `serializeBundle` writes the full object, so without these the read path silently
	// dropped proxy config that the write path preserved — a one-way loss.
	"proxyUrl",
	"proxyUsername",
	"proxyPassword",
] as const;

/**
 * Parse the stored bundle into credentials.
 *
 * A malformed bundle throws rather than degrading to "no credentials": silently ignoring it
 * would present a configured provider as unconfigured, sending the user to re-enter
 * credentials that are actually still there.
 */
export function parseBundle(bundle: string): KiroCredentials[] {
	let parsed: unknown;
	try {
		parsed = JSON.parse(bundle);
	} catch {
		throw new InvalidBundleError("Stored Kiro credentials are not valid JSON");
	}
	const entries = Array.isArray(parsed) ? parsed : [parsed];
	const credentials: KiroCredentials[] = [];
	for (const entry of entries) {
		if (!isRecord(entry)) continue;
		const credential: Record<string, unknown> = {};
		for (const field of BUNDLE_TEXT_FIELDS) {
			const value = text(entry[field]);
			if (value !== undefined) credential[field] = value;
		}
		if (typeof entry.priority === "number" && Number.isFinite(entry.priority)) {
			credential.priority = Math.trunc(entry.priority);
		}
		if (typeof entry.disabled === "boolean") credential.disabled = entry.disabled;
		if (typeof entry.meterUsage === "number" && Number.isFinite(entry.meterUsage)) {
			credential.meterUsage = entry.meterUsage;
		}
		if (Array.isArray(entry.availableModels)) {
			credential.availableModels = entry.availableModels.filter(
				(model): model is string => typeof model === "string",
			);
		}
		if (hasUsableSecret(credential as KiroCredentials)) {
			credentials.push(credential as KiroCredentials);
		}
	}
	if (credentials.length === 0) {
		throw new MissingCredentialError("Stored Kiro credentials contain no usable entry");
	}
	return credentials;
}

/** Whether an entry carries enough material to authenticate at all. */
function hasUsableSecret(credential: KiroCredentials): boolean {
	const method = canonicalizeAuthMethodValue(credential.authMethod) ?? "social";
	if (method === "api_key") return Boolean(credential.apiKey);
	if (!credential.refreshToken) return false;
	if (method === "idc") return Boolean(credential.clientId && credential.clientSecret);
	return true;
}

/** Serialize credentials back into a bundle for storage via `secretWrites`. */
export function serializeBundle(credentials: readonly KiroCredentials[]): string {
	return JSON.stringify(credentials);
}

/**
 * Validate config into a credential, or explain exactly what is missing.
 *
 * Requirements differ per auth method, so a single "credentials required" message would be
 * unhelpful. `api_key` needs only the key; `social` needs a refresh token; `idc` also needs
 * the client pair.
 */
export function toCredential(config: KiroPluginConfig): KiroCredentials {
	const authMethod = canonicalizeAuthMethodValue(config.authMethod) ?? "social";
	const missing: string[] = [];

	if (authMethod === "api_key") {
		if (!config.apiKey) missing.push("apiKey");
	} else {
		if (!config.refreshToken) missing.push("refreshToken");
		if (authMethod === "idc") {
			if (!config.clientId) missing.push("clientId");
			if (!config.clientSecret) missing.push("clientSecret");
		}
	}
	if (missing.length > 0) {
		throw new MissingCredentialError(
			`Kiro credentials are incomplete for authMethod "${authMethod}": missing ${missing.join(", ")}`,
		);
	}

	const region = config.region ?? "us-east-1";
	return {
		authMethod,
		region,
		authRegion: region,
		apiRegion: region,
		...(config.refreshToken ? { refreshToken: config.refreshToken } : {}),
		...(config.apiKey ? { apiKey: config.apiKey } : {}),
		...(config.clientId ? { clientId: config.clientId } : {}),
		...(config.clientSecret ? { clientSecret: config.clientSecret } : {}),
		...(config.profileArn ? { profileArn: config.profileArn } : {}),
	};
}

/**
 * Every credential a *config* describes. The vault, not this, is the durable store.
 *
 * The blob branch remains because a host that has not reloaded the provider descriptor could
 * still inject `credentialsBundle`, and refusing it would strand those credentials. It is no
 * longer the primary path: `resolveManager` consults the vault first and only reaches here
 * when neither a sentinel nor any credential entry exists.
 *
 * Throws `MissingCredentialError` naming the absent fields when there is nothing to build.
 */
export function toCredentials(config: KiroPluginConfig): KiroCredentials[] {
	if (config.credentialsBundle) return parseBundle(config.credentialsBundle);
	return [toCredential(config)];
}

// A per-credential content fingerprint used to live here, hashing the identity-bearing fields
// so a cosmetic config edit would not discard a live access token. It is gone because the cache
// key changed: hashing content requires reading every credential first, which is the expense
// the revision marker exists to avoid. See `cached` above.

/**
 * Plugin-owned data directory, used only for `MultiTokenManager`'s refreshed-token and
 * usage-stats persistence. Never the host's credential file.
 *
 * `NF_PLUGIN_DATA_DIR` is the writable scope the runtime provides (see `SAFE_ENV_KEYS` in
 * `plugin-runtime.ts`). Falling back to `process.cwd()` would be wrong: the runtime sets cwd
 * to the *package* directory, so a fallback writes credentials into the plugin's own source
 * tree — which, for a plugin shipped from this repo, means into version control. An earlier
 * revision did exactly that and left a real credential file in `examples/`.
 *
 * With no data directory the manager falls back to the OS temp dir, so tokens live only as
 * long as the process and nothing lands anywhere durable or tracked.
 */
function pluginDataDir(): string {
	const base = process.env.NF_PLUGIN_DATA_DIR?.trim();
	const dir = base ? join(base, "kiro") : join(tmpdir(), `kiro-external-${process.pid}`);
	try {
		mkdirSync(dir, { recursive: true, mode: 0o700 });
	} catch {
		// Non-fatal: the manager degrades to in-memory stats if it cannot persist.
	}
	return dir;
}

/**
 * Where the manager persists its credential list for this config.
 *
 * Exposed so a mutating command can read the post-change list back: `MultiTokenManager` has
 * no public credential accessor, and adding one would mean editing the built-in module.
 */
export function credentialsPathFor(_rawConfig?: unknown): string {
	return join(pluginDataDir(), "credentials.json");
}

/**
 * The live manager, keyed by the index revision plus the config that shapes it.
 *
 * `revision` comes from the sentinel, not from the credentials, and that is the whole point:
 * the previous cache key was a SHA-256 over the credential set, which only worked because the
 * credentials were already in `config` and hashing them cost nothing. Now that they live in
 * the vault, computing that hash would require reading all N first — precisely the expense the
 * cache exists to avoid. So the key must be something obtainable *before* reading: the
 * sentinel's revision, which the writer bumps on every change.
 *
 * `configKey` is folded in because `loadBalancingMode` and `region` also shape the manager but
 * do not touch the credential index; without it, a load-balancing change would be ignored
 * until a credential happened to change.
 */
let cached:
	| { revision: number; configKey: string; count: number; manager: MultiTokenManager }
	| undefined;

/** The config fields that shape the manager, so a change to them rebuilds it. */
function managerConfigKey(config: KiroPluginConfig): string {
	return `${config.loadBalancingMode ?? ""}\u0000${config.region ?? ""}`;
}

/**
 * Build or reuse the manager, reading credentials from the vault.
 *
 * Steady state is **one** RPC: read the sentinel, compare `revision`, reuse the cached
 * manager. Only a changed (or absent) revision triggers the N reads needed to rebuild.
 *
 * Throws `MissingCredentialError` when no credential is stored, so callers report a
 * configuration problem rather than an upstream failure.
 */
export async function resolveManager(rawConfig: unknown): Promise<MultiTokenManager> {
	const config = readConfig(rawConfig);
	const configKey = managerConfigKey(config);

	try {
		return await resolveFromVault(config, configKey);
	} catch (error) {
		// `MissingCredentialError` is a real answer — "nothing is stored" — and must reach the
		// caller, which reports an unconfigured provider.
		if (error instanceof MissingCredentialError || error instanceof InvalidBundleError) throw error;
		// Anything else means credential storage itself is unavailable: a host that serves no
		// `secrets.*` channel (release validation starts the plugin exactly that way), or a vault
		// that failed to read. Fall back to whatever the config carries and let that decide.
		// Propagating a transport error here would present a configuration state as a broken
		// plugin, which is how a legitimately unconfigured provider came to fail GA validation.
		log("credential storage is unavailable; falling back to config", {
			error: error instanceof Error ? error.message : "unknown",
		});
		return buildManager(config, toCredentials(config), -1, configKey);
	}
}

async function resolveFromVault(
	config: KiroPluginConfig,
	configKey: string,
): Promise<MultiTokenManager> {
	const index = await readIndex();

	if (index) {
		if (cached && cached.revision === index.revision && cached.configKey === configKey) {
			return cached.manager;
		}
		const state = await readKeyState();
		const credentials = await readCredentials(state.credentialKeys);
		if (credentials.length === 0) {
			// The sentinel says credentials exist but none could be read. Reporting "not
			// configured" is the honest answer, and it must not be cached: the next call
			// should retry rather than inherit a wrong empty state.
			throw new MissingCredentialError(
				"Kiro credentials are recorded but could not be read. Re-add a credential.",
			);
		}
		return buildManager(config, credentials, index.revision, configKey);
	}

	// No sentinel. This does **not** by itself mean "no credentials", and conflating the two is
	// the mistake that would strand a user's credentials. Three distinct states land here:
	//
	//   | no sentinel, no entries, old blob present | never migrated      | migrate
	//   | no sentinel, entries present              | migration interrupted | resume
	//   | no sentinel, no entries, no blob          | genuinely empty     | report missing
	//
	// So the sentinel — not "does any cred key exist" — is the completion marker, and resolving
	// which state this is belongs to `ensureMigrated`. It is idempotent and deduplicated, so
	// calling it here costs nothing when activation already ran it.
	const migration = await ensureMigrated();
	if (migration.migrated) {
		const state = await readKeyState();
		const credentials = await readCredentials(state.credentialKeys);
		if (credentials.length > 0) {
			const revision = (await readIndex())?.revision ?? -1;
			return buildManager(config, credentials, revision, configKey);
		}
	}

	// Legacy flat config fields are the last resort, so a provider configured before
	// multi-credential support keeps working. `toCredentials` throws `MissingCredentialError`
	// naming what is absent when there is nothing to build from, which is the right answer
	// here: no sentinel, nothing migrated, no legacy fields means genuinely unconfigured.
	return buildManager(config, toCredentials(config), -1, configKey);
}

function buildManager(
	config: KiroPluginConfig,
	credentials: KiroCredentials[],
	revision: number,
	configKey: string,
): MultiTokenManager {
	const credentialsPath = join(pluginDataDir(), "credentials.json");
	const kiroConfig = loadConfig(join(pluginDataDir(), "config.json"));
	const region = config.region ?? credentials[0]?.region ?? kiroConfig.region;
	const manager = new MultiTokenManager(
		{
			...kiroConfig,
			region,
			...(config.loadBalancingMode === "balanced" || config.loadBalancingMode === "priority"
				? { loadBalancingMode: config.loadBalancingMode }
				: {}),
		},
		credentials,
		credentialsPath,
		true,
	);
	cached = { revision, configKey, count: credentials.length, manager };
	log("credential manager built", { credentialCount: credentials.length, revision });
	return manager;
}

/** Drop the cached manager. Used on deactivate so a restart re-reads config. */
export function resetManager(): void {
	cached = undefined;
}
