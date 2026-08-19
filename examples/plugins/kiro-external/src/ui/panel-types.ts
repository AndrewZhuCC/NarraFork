/**
 * Shapes the panel reads, plus the command bridge.
 *
 * These mirror what `src/commands.ts` returns rather than importing it: the panel is browser
 * code and the command module pulls in the whole Kiro client layer, so a shared import would
 * drag `MultiTokenManager` into the iframe bundle.
 *
 * Every field here is metadata. No credential value is representable in these types, which is
 * the property that makes it safe for command output to reach a document at all.
 */

export interface PluginUiSdk {
	request(method: string, params?: unknown): Promise<unknown>;
	notify(event: string, payload?: unknown): void;
}

export type AccountTier = "free" | "paid" | "enterprise";

export interface CredentialEntry {
	id: string;
	priority: number;
	disabled: boolean;
	disabledReason?: string;
	failureCount?: number;
	successCount?: number;
	lastUsedAt?: string;
	email?: string;
	displayName?: string;
	hasProfileArn?: boolean;
	hasStartUrl?: boolean;
	expiresAt?: string;
	authMethod?: string;
	isApiKey?: boolean;
	subscriptionTitle?: string;
	region?: string;
	authRegion?: string;
	apiRegion?: string;
	profileArn?: string;
	startUrl?: string;
	machineId?: string;
	hasClientId?: boolean;
	hasClientSecret?: boolean;
	proxyUrl?: string;
	proxyUsername?: string;
	hasProxyPassword?: boolean;
	meterUsage?: number;
	/** Count only: the backend trims the model list to keep command output bounded. */
	modelCount?: number;
	tier?: AccountTier;
}

export interface UsageEntry {
	id?: string;
	subscriptionTitle?: string;
	currentUsage?: number;
	usageLimit?: number;
	remaining?: number;
	usagePercentage?: number;
	nextResetAt?: number;
	overageStatus?: string;
	currentOverages?: number;
	queriedAt?: string;
}

export interface StatusOutput {
	total: number;
	available: number;
	/** Legacy names, still sent so an older panel keeps working. */
	credentialCount?: number;
	availableCount?: number;
	loadBalancingMode: "priority" | "balanced";
	currentId?: string;
	entries: CredentialEntry[];
	paidEntries: CredentialEntry[];
	enterpriseEntries: CredentialEntry[];
	freeEntries: CredentialEntry[];
	paidTotal: number;
	enterpriseTotal: number;
	freeTotal: number;
	unhealthyTotal: number;
	usageCache?: Record<string, UsageEntry>;
}

export interface ModelEntry {
	id: string;
	displayName: string;
	contextWindow?: number;
}

/**
 * Editable fields, matching the backend's `credentials.update` whitelist.
 *
 * The backend (see `commands.ts` "credentials.update" handler) only accepts these specific
 * fields: text fields (email, displayName, region, authRegion, apiRegion, profileArn,
 * subscriptionTitle, startUrl, machineId, proxyUrl, proxyUsername), secret fields
 * (refreshToken, apiKey, clientId, clientSecret, proxyPassword), authMethod (string),
 * and priority (number). Unknown keys are silently discarded.
 *
 * This type uses a union key to document the exact whitelist while remaining compatible
 * with the EditForm's computed-key access pattern.
 */
export type CredentialPatchKey =
	| "email"
	| "displayName"
	| "region"
	| "authRegion"
	| "apiRegion"
	| "profileArn"
	| "subscriptionTitle"
	| "startUrl"
	| "machineId"
	| "proxyUrl"
	| "proxyUsername"
	| "refreshToken"
	| "apiKey"
	| "clientId"
	| "clientSecret"
	| "proxyPassword"
	| "authMethod"
	| "priority";

export type CredentialPatch = Partial<Record<CredentialPatchKey, string | number>>;

export function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Run a backend command.
 *
 * The host wraps a successful result as `{ status, output }`; unwrapping here keeps that
 * envelope out of every call site.
 */
export async function runCommand(
	sdk: PluginUiSdk,
	commandId: string,
	input?: Record<string, unknown>,
): Promise<unknown> {
	const raw = await sdk.request("commands.execute", {
		commandId,
		...(input === undefined ? {} : { input }),
	});
	if (isRecord(raw) && "output" in raw) return raw.output;
	return raw;
}

/** Readable message from a rejected command, whose errors carry a `code` and `message`. */
export function errorMessage(error: unknown): string {
	if (error instanceof Error) return error.message;
	if (isRecord(error) && typeof error.message === "string") return error.message;
	return String(error);
}
