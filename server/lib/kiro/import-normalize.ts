// Kiro credential import normalization.
//
// Mirrors NUG's `parseKiroImportPayload` key-alias handling so payloads exported
// by other tools (kiro.rs API-key files, NKP account dumps, NUG exports) import
// cleanly. Only the alias/inference layer lives here; persistence and dedupe stay
// in MultiTokenManager.

import { isApiKeyAuthMethod } from "./auth-method";
import type { KiroCredentials } from "./types";

/**
 * Compact (lower-cased, separator-stripped) keys that map to a canonical
 * camelCase field. Only names that differ beyond snake_case→camelCase need an
 * entry; the rest is handled by `snakeToCamelKey`.
 */
const KEY_ALIASES: Record<string, string> = {
	kiroapikey: "apiKey",
	key: "apiKey",
	expired: "expiresAt",
	expiry: "expiresAt",
	issuerurl: "startUrl",
	issuer: "startUrl",
	nickname: "displayName",
};

function snakeToCamelKey(key: string): string {
	if (!/[_\-\s]/.test(key)) return key;
	const parts = key.split(/[_\-\s]+/).filter(Boolean);
	if (parts.length === 0) return key;
	return parts
		.map((part, i) =>
			i === 0 ? part.toLowerCase() : part[0].toUpperCase() + part.slice(1).toLowerCase(),
		)
		.join("");
}

function canonicalKey(key: string): string {
	const compact = key
		.trim()
		.toLowerCase()
		.replace(/[_\-\s]/g, "");
	return KEY_ALIASES[compact] ?? snakeToCamelKey(key.trim());
}

function isTruthy(value: unknown): boolean {
	if (value == null) return false;
	if (typeof value === "string") return value.trim() !== "";
	if (typeof value === "number") return value !== 0 && !Number.isNaN(value);
	if (typeof value === "boolean") return value;
	return true;
}

function isPlainRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function optionalString(value: unknown): string | undefined {
	if (typeof value === "string") {
		const trimmed = value.trim();
		return trimmed || undefined;
	}
	if (typeof value === "number" && Number.isFinite(value)) return String(value);
	return undefined;
}

function firstString(...values: unknown[]): string | undefined {
	for (const value of values) {
		const str = optionalString(value);
		if (str) return str;
	}
	return undefined;
}

/** Rewrite arbitrary record keys into the canonical camelCase fields. */
function canonicalizeRecord(record: Record<string, unknown>): Record<string, unknown> {
	const out: Record<string, unknown> = {};
	for (const [key, value] of Object.entries(record)) {
		const canonical = canonicalKey(key);
		// When several source keys collapse onto one canonical field, keep the
		// first truthy value instead of letting an empty duplicate clobber it.
		if (canonical in out && isTruthy(out[canonical]) && !isTruthy(value)) continue;
		out[canonical] = value;
	}
	return out;
}

/**
 * Apply static API key semantics: mirror the key onto `apiKey`/`accessToken` and
 * force `authMethod: "api_key"`. Also infers api_key when a payload carries a
 * key but no refresh token.
 */
function applyApiKeySemantics(cred: Record<string, unknown>): void {
	const apiKey = firstString(cred.apiKey);
	const refreshToken = firstString(cred.refreshToken);
	const accessToken = firstString(cred.accessToken);
	const declaredApiKey = isApiKeyAuthMethod(optionalString(cred.authMethod));

	if (!declaredApiKey && !(apiKey && !refreshToken)) return;

	const key = apiKey ?? accessToken;
	if (!key) return;
	cred.authMethod = "api_key";
	cred.apiKey = key;
	cred.accessToken = key;
	// Static keys never expire; drop any inherited expiry so they are not
	// mistaken for stale OAuth tokens.
	delete cred.expiresAt;
}

/** Normalize an `{ accounts: [...] }` wrapper entry into a flat credential. */
function fromAccount(account: unknown): Record<string, unknown> | null {
	if (!isPlainRecord(account)) return null;
	const creds = isPlainRecord(account.credentials) ? account.credentials : {};
	const subscription = isPlainRecord(account.subscription) ? account.subscription : {};

	const merged: Record<string, unknown> = {
		...canonicalizeRecord(creds),
	};
	const apiKey = firstString(
		merged.apiKey,
		(account as Record<string, unknown>).kiroApiKey,
		(account as Record<string, unknown>).kiro_api_key,
		(account as Record<string, unknown>).apiKey,
	);
	if (apiKey) merged.apiKey = apiKey;

	const email = firstString(account.email, merged.email);
	if (email) merged.email = email;
	const displayName = firstString(account.nickname, merged.displayName);
	if (displayName) merged.displayName = displayName;
	const subscriptionTitle = firstString(subscription.title, merged.subscriptionTitle);
	if (subscriptionTitle) merged.subscriptionTitle = subscriptionTitle;
	const machineId = firstString(account.machineId, merged.machineId);
	if (machineId) merged.machineId = machineId;
	const startUrl = firstString(account.startUrl, merged.startUrl);
	if (startUrl) merged.startUrl = startUrl;

	return merged;
}

/**
 * Normalize a list of raw import records into credentials.
 *
 * Accepts flat credential objects, `{ credentials: [...] }`, and
 * `{ accounts: [{ credentials: {...} }] }` wrappers. Records that carry neither a
 * refresh token nor an API key are returned as-is so the caller can count them
 * as skipped.
 */
export function normalizeImportCredentials(records: unknown[]): KiroCredentials[] {
	const out: KiroCredentials[] = [];
	for (const record of records) {
		if (!isPlainRecord(record)) continue;

		// { accounts: [...] } wrapper (only when no explicit credentials field)
		if (Array.isArray(record.accounts) && !isTruthy(record.credentials)) {
			for (const account of record.accounts) {
				const flat = fromAccount(account);
				if (!flat) continue;
				applyApiKeySemantics(flat);
				out.push(flat as KiroCredentials);
			}
			continue;
		}

		// { credentials: {...} | [...] } wrapper
		if (isTruthy(record.credentials)) {
			const nested = Array.isArray(record.credentials) ? record.credentials : [record.credentials];
			for (const item of nested) {
				if (!isPlainRecord(item)) continue;
				const flat = canonicalizeRecord(item);
				applyApiKeySemantics(flat);
				out.push(flat as KiroCredentials);
			}
			continue;
		}

		const flat = canonicalizeRecord(record);
		applyApiKeySemantics(flat);
		out.push(flat as KiroCredentials);
	}
	return out;
}

/** Whether a normalized credential carries usable token material. */
export function hasImportableSecret(creds: KiroCredentials): boolean {
	if (creds.refreshToken?.trim()) return true;
	return !!creds.apiKey?.trim();
}
