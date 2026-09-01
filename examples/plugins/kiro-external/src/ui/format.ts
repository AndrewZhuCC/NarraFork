/**
 * Display helpers shared by the panel's table and card layouts.
 *
 * The host has equivalents (`frontend/lib/relative-time.ts`, `formatRegions` in
 * `KiroSection.tsx`), but those are host modules the iframe cannot import. The behaviour is
 * matched deliberately so the same credential reads the same way in both pages.
 */

import type { AccountTier, CredentialEntry } from "./panel-types";
import { activeLocale, t } from "./strings";

/** Tier badge colors, matching the host's `TIER_CONFIG`. */
export interface TierMeta {
	color: string;
	label: string;
	desc: string;
}

/**
 * Tier badge strings, resolved PER CALL.
 *
 * A module-level `TIER_META` table called `t()` once, at import time — before the host SDK
 * was guaranteed to exist, and never again afterwards. The label froze at the host's locale
 * (or the fallback) for the life of the panel: switching the app's language mid-session
 * re-rendered every other string through `onLocaleChange` while the tier badges kept the old
 * words. `t()` already resolves per call for exactly this reason; the table around it was
 * the last place holding a translation still.
 */
export function getTierMeta(tier: AccountTier): TierMeta {
	switch (tier) {
		case "paid":
			return { color: "blue", label: t("tierPaid"), desc: t("tierPaidDesc") };
		case "enterprise":
			return { color: "violet", label: t("tierEnterprise"), desc: t("tierEnterpriseDesc") };
		default:
			return { color: "gray", label: t("tierFree"), desc: t("tierFreeDesc") };
	}
}

/** Whether an entry is a static API key, which is a bearer token with nothing to refresh. */
export function isApiKeyEntry(entry: CredentialEntry): boolean {
	if (entry.isApiKey === true) return true;
	const method = entry.authMethod?.trim().toLowerCase();
	return method === "api_key" || method === "apikey";
}

/** Kiro API keys in pasted text, mirroring the server-side pattern. */
const API_KEY_PATTERN = /ksk[_-][A-Za-z0-9._-]+/;

export function containsApiKey(text: string): boolean {
	return API_KEY_PATTERN.test(text);
}

/**
 * Parse plain-text `ksk_` keys into importable records, one per line.
 *
 * Kept because pasting a bare key list is the common case and requiring users to wrap it in
 * JSON would be a regression against the host page, which accepts the same input.
 */
export function parseApiKeyLines(text: string): Array<Record<string, string>> {
	const results: Array<Record<string, string>> = [];
	for (const line of text.split("\n")) {
		const trimmed = line.trim();
		if (trimmed.startsWith("ksk_")) results.push({ authMethod: "api_key", apiKey: trimmed });
	}
	return results;
}

/** Absolute timestamp, from an ISO string or a unix time in seconds or milliseconds. */
export function formatTimestamp(value?: string | number): string {
	if (!value) return "-";
	const date = new Date(
		typeof value === "number" ? (value < 10_000_000_000 ? value * 1000 : value) : value,
	);
	if (Number.isNaN(date.getTime())) return String(value);
	// Same source as `relativeTime`: an absolute timestamp rendered in the browser's
	// language next to a relative one in the app's is the same split, at hour scale.
	return date.toLocaleString(activeLocale(), {
		dateStyle: "medium",
		timeStyle: "short",
	});
}

const RELATIVE_UNITS: Array<[Intl.RelativeTimeFormatUnit, number]> = [
	["year", 365 * 24 * 60 * 60 * 1000],
	["month", 30 * 24 * 60 * 60 * 1000],
	["day", 24 * 60 * 60 * 1000],
	["hour", 60 * 60 * 1000],
	["minute", 60 * 1000],
];

/**
 * "3 hours ago" / "in 2 days".
 *
 * `Intl.RelativeTimeFormat` rather than a hand-rolled table: it localizes for free.
 *
 * The locale comes from the HOST (`activeLocale`), not `navigator.language`. Mixing the
 * two sources is what produced a panel whose labels were Chinese while its timestamps
 * read "2 hours ago" — the strings had already moved to the host's locale and the dates
 * had not.
 */
export function relativeTime(value?: string | number): string {
	if (!value) return "-";
	const date = new Date(
		typeof value === "number" ? (value < 10_000_000_000 ? value * 1000 : value) : value,
	);
	if (Number.isNaN(date.getTime())) return String(value);
	const deltaMs = date.getTime() - Date.now();
	const formatter = new Intl.RelativeTimeFormat(activeLocale(), { numeric: "auto" });
	for (const [unit, ms] of RELATIVE_UNITS) {
		if (Math.abs(deltaMs) >= ms) return formatter.format(Math.round(deltaMs / ms), unit);
	}
	return formatter.format(Math.round(deltaMs / 1000), "second");
}

/**
 * Base region plus any auth/API overrides.
 *
 * All three are shown because they resolve independently: `authRegion` overrides the
 * token-refresh endpoint while `apiRegion` overrides the data-plane endpoint.
 */
export function formatRegions(entry: CredentialEntry): string {
	const parts: string[] = [];
	if (entry.region) parts.push(entry.region);
	if (entry.authRegion) parts.push(`auth ${entry.authRegion}`);
	if (entry.apiRegion) parts.push(`api ${entry.apiRegion}`);
	return parts.length > 0 ? parts.join(" · ") : "-";
}

/** Two decimal places, matching the host's usage figures. */
export function formatUsageNumber(value: number): string {
	return value.toFixed(2);
}
