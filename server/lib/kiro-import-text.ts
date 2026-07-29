// Plain-text Kiro credential import parsing.
//
// The credential import endpoint historically only accepted JSON objects. Kiro
// static API keys ("ksk_...") are usually distributed as bare token lists, so
// this module turns pasted text into `api_key` credentials without requiring a
// JSON wrapper. Object payloads are passed through untouched — alias handling
// and account wrappers stay in the kiro plugin's `normalizeImportCredentials`.

import type { ImportCredentialInput } from "./kiro-adapter";

/**
 * Static Kiro API keys as they appear in pasted text. `ksk_` is the current
 * prefix; `ksk-` is accepted for older exports.
 */
const KIRO_API_KEY_PATTERN = /ksk[_-][A-Za-z0-9._-]+/g;

const EMAIL_PATTERN = /[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/i;

function isPlainRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Whether a text blob carries at least one static Kiro API key. */
export function containsKiroApiKey(text: string): boolean {
	// Fresh regex per call: the shared pattern is global and carries lastIndex.
	return new RegExp(KIRO_API_KEY_PATTERN.source).test(text);
}

/**
 * Extract credentials from a text blob.
 *
 * Accepts JSON (delegated to {@link kiroCredentialsFromImportPayload}) and
 * newline/comma separated records containing `ksk_...` keys. Extra columns in a
 * record (password, notes, ...) are ignored apart from an email, which becomes
 * the credential label.
 */
export function kiroCredentialsFromImportText(text: string): ImportCredentialInput[] {
	const trimmed = text.trim();
	if (!trimmed) return [];

	if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
		try {
			return kiroCredentialsFromImportPayload(JSON.parse(trimmed));
		} catch {
			// Not JSON after all — fall through to API key extraction.
		}
	}

	return trimmed.split(/[,\n]+/).flatMap((record) => {
		const email = record.match(EMAIL_PATTERN)?.[0];
		return [...record.matchAll(KIRO_API_KEY_PATTERN)].map((match) => ({
			apiKey: match[0],
			authMethod: "api_key",
			...(email ? { email, displayName: email } : {}),
		}));
	});
}

/**
 * Normalize an arbitrary import payload into credential inputs.
 *
 * Strings are parsed as text, arrays are flattened, and objects are forwarded
 * as-is so the kiro plugin keeps ownership of key aliases and `{ accounts: [] }`
 * wrappers.
 */
export function kiroCredentialsFromImportPayload(value: unknown): ImportCredentialInput[] {
	if (typeof value === "string") return kiroCredentialsFromImportText(value);
	if (Array.isArray(value)) {
		return value.flatMap((item) => kiroCredentialsFromImportPayload(item));
	}
	if (isPlainRecord(value)) return [value as ImportCredentialInput];
	return [];
}
