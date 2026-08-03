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

import { createHash } from "node:crypto";
import { mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { canonicalizeAuthMethodValue } from "@server/lib/kiro/auth-method";
import { loadConfig } from "@server/lib/kiro/config";
import { MultiTokenManager } from "@server/lib/kiro/multi-token-manager";
import type { KiroCredentials } from "@server/lib/kiro/types";
import { isRecord, log } from "./rpc";

/** Config keys this plugin understands. Anything else is ignored. */
export interface KiroPluginConfig {
	/**
	 * JSON array of credentials, stored as a single secret field.
	 *
	 * The vault keys secrets as flat `provider.<contributionId>.<field>` strings, which
	 * cannot express "the refresh token of the Nth credential". Rather than introduce
	 * dynamic secret keys — which would require splitting the backend and iframe method
	 * inventories — the whole list travels as one opaque JSON value. That keeps
	 * multi-credential support inside the existing contract at the cost of a 64 KB ceiling
	 * (about ten kilobytes for a dozen credentials).
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
 * Every credential the config describes, newest storage format first.
 *
 * The bundle wins when present; the flat single-credential fields remain accepted so a
 * provider configured before multi-credential support keeps working without the user
 * re-entering anything.
 */
export function toCredentials(config: KiroPluginConfig): KiroCredentials[] {
	if (config.credentialsBundle) return parseBundle(config.credentialsBundle);
	return [toCredential(config)];
}

/**
 * Stable identity for a credential set, without retaining any secret as a key.
 *
 * Only the fields that affect which upstream identity is used participate, so a cosmetic
 * config edit does not throw away a valid access token.
 */
function fingerprint(credential: KiroCredentials): string {
	const material = [
		credential.authMethod ?? "",
		credential.refreshToken ?? "",
		credential.apiKey ?? "",
		credential.clientId ?? "",
		credential.clientSecret ?? "",
		credential.profileArn ?? "",
		credential.region ?? "",
	].join("\u0000");
	return createHash("sha256").update(material).digest("hex");
}

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

let cached: { key: string; manager: MultiTokenManager } | undefined;

/**
 * Build or reuse the manager for the given config.
 *
 * Throws `MissingCredentialError` when config is incomplete, so callers can report a
 * configuration problem rather than an upstream failure.
 */
export function resolveManager(rawConfig: unknown): MultiTokenManager {
	const config = readConfig(rawConfig);
	const credentials = toCredentials(config);
	// One fingerprint over the whole set: adding, removing or rotating any credential must
	// rebuild the manager, while an unrelated config edit must not discard live access tokens.
	const key = createHash("sha256")
		.update(credentials.map(fingerprint).join("\u0001"))
		.digest("hex");
	if (cached?.key === key) return cached.manager;

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
	cached = { key, manager };
	log("credential manager built", {
		credentialCount: credentials.length,
		keyPrefix: key.slice(0, 8),
	});
	return manager;
}

/** Drop the cached manager. Used on deactivate so a restart re-reads config. */
export function resetManager(): void {
	cached = undefined;
}
