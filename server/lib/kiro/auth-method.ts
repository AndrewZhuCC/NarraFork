// Canonical Kiro auth-method helpers.
//
// Mirrors NUG's `canonicalKiroAuthMethod` / `isAPIKeyAuthMethod` semantics so
// credentials imported from or exported to the gateway behave identically here.
//
// Static API key credentials (KIRO_API_KEY, "ksk_..." style) are long-lived
// bearer tokens: they are used directly as the access token, are never
// refreshed, and never expire. Kiro requires them to carry an extra
// `TokenType: API_KEY` header, otherwise the service does not recognize the
// token type and rejects data-plane calls.

import type { KiroCredentials } from "./types";

export type CanonicalKiroAuthMethod = "api_key" | "idc" | "social";

/** Header value Kiro expects for static API key bearer tokens. */
export const KIRO_API_KEY_TOKEN_TYPE = "API_KEY";

/** Whether an auth-method string denotes a static Kiro API key credential. */
export function isApiKeyAuthMethod(method?: string | null): boolean {
	switch (method?.trim().toLowerCase()) {
		case "api_key":
		case "apikey":
		case "api-key":
			return true;
		default:
			return false;
	}
}

/** Normalize any accepted auth-method spelling to its canonical form. */
export function canonicalizeAuthMethodValue(
	method?: string | null,
): CanonicalKiroAuthMethod | null {
	const normalized = method?.trim().toLowerCase();
	if (!normalized) return null;
	if (isApiKeyAuthMethod(normalized)) return "api_key";
	if (normalized === "idc" || normalized === "builder-id" || normalized === "iam") return "idc";
	if (normalized === "social") return "social";
	return null;
}

/** Resolve the effective auth method of a credential, falling back like NUG does. */
export function canonicalKiroAuthMethod(creds: KiroCredentials): CanonicalKiroAuthMethod {
	const explicit = canonicalizeAuthMethodValue(creds.authMethod);
	if (explicit) return explicit;
	if (!creds.refreshToken && apiKeyOf(creds)) return "api_key";
	if (creds.clientId && creds.clientSecret) return "idc";
	return "social";
}

/** Static API key value carried by a credential, if any. */
export function apiKeyOf(creds: KiroCredentials): string | undefined {
	const key = creds.apiKey?.trim();
	if (key) return key;
	// API key credentials store the key as their access token as well, so a
	// credential explicitly marked api_key can also carry it there only.
	if (isApiKeyAuthMethod(creds.authMethod)) {
		const token = creds.accessToken?.trim();
		if (token) return token;
	}
	return undefined;
}

/** Whether a credential is a static API key credential with a usable key. */
export function isApiKeyCredential(creds: KiroCredentials): boolean {
	return canonicalKiroAuthMethod(creds) === "api_key" && !!apiKeyOf(creds);
}

/**
 * Apply the `TokenType` header required for non-default bearer token types.
 * Social and IdC OAuth credentials omit it.
 */
export function applyKiroTokenTypeHeader(
	headers: Record<string, string>,
	creds: KiroCredentials,
): void {
	if (canonicalKiroAuthMethod(creds) === "api_key") {
		headers.TokenType = KIRO_API_KEY_TOKEN_TYPE;
	}
}
