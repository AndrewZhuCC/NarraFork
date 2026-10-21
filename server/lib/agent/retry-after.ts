/**
 * Parse an upstream "when may I try again" hint out of response headers.
 *
 * Pure and dependency-free so both the quota-wait resolvers and the in-loop
 * retry can share one reading of the header family:
 *
 *  - `retry-after-ms` — milliseconds (OpenAI SDK convention);
 *  - `retry-after` — RFC 9110: delta-seconds or an HTTP-date;
 *  - `ratelimit-reset` — IETF draft: delta-seconds;
 *  - `x-ratelimit-reset` — epoch seconds/ms, or delta-seconds when small;
 *  - `x-ratelimit-reset-{requests,tokens}` — OpenAI durations such as `6m0s`.
 *
 * A `Retry-After` only says WHEN a retry may succeed, never WHETHER the failure
 * is transient (see `RETRYABLE_429_PATTERNS` in `error-handling.ts`), so callers
 * must classify the refusal first and use this only for the timing.
 */

/** Smallest plausible epoch-seconds value (2001-09-09); below it a number is a delta. */
const EPOCH_SECONDS_FLOOR = 1_000_000_000;
const EPOCH_MS_FLOOR = 1_000_000_000_000;

const DURATION_UNITS_MS: Record<string, number> = {
	ms: 1,
	s: 1_000,
	m: 60_000,
	h: 3_600_000,
	d: 86_400_000,
};

function lowerCaseHeaders(
	headers: Record<string, string> | undefined,
): Map<string, string> | undefined {
	if (!headers) return undefined;
	const map = new Map<string, string>();
	for (const [name, value] of Object.entries(headers)) {
		if (typeof value === "string" && value.trim()) map.set(name.toLowerCase(), value.trim());
	}
	return map.size > 0 ? map : undefined;
}

function finiteNonNegative(value: number): number | null {
	return Number.isFinite(value) && value >= 0 ? value : null;
}

/** `6m0s`, `1h2m3.5s`, `20ms` → milliseconds; null when the string is not one. */
export function parseCompactDurationMs(value: string): number | null {
	const trimmed = value.trim().toLowerCase();
	if (!/^(?:\d+(?:\.\d+)?(?:ms|s|m|h|d))+$/.test(trimmed)) return null;
	let total = 0;
	for (const match of trimmed.matchAll(/(\d+(?:\.\d+)?)(ms|s|m|h|d)/g)) {
		total += Number(match[1]) * (DURATION_UNITS_MS[match[2] as string] ?? 0);
	}
	return finiteNonNegative(total);
}

function parseRetryAfter(value: string, now: number): number | null {
	if (/^\d+(?:\.\d+)?$/.test(value)) return finiteNonNegative(Number(value) * 1_000);
	const date = Date.parse(value);
	return Number.isNaN(date) ? null : Math.max(0, date - now);
}

function parseResetValue(value: string, now: number): number | null {
	if (/^\d+(?:\.\d+)?$/.test(value)) {
		const n = Number(value);
		if (n >= EPOCH_MS_FLOOR) return Math.max(0, n - now);
		if (n >= EPOCH_SECONDS_FLOOR) return Math.max(0, n * 1_000 - now);
		return finiteNonNegative(n * 1_000);
	}
	return parseCompactDurationMs(value);
}

/**
 * How long the upstream asked the caller to wait, in ms, or null when the
 * headers carry no usable hint.
 *
 * `retry-after-ms` / `retry-after` win when present, since they describe this
 * exact refusal. Otherwise the LATEST of the rate-limit reset headers is used:
 * when several limits are reported, a retry only succeeds once all of them have
 * reset.
 */
export function parseRetryAfterMs(
	headers: Record<string, string> | undefined,
	now = Date.now(),
): number | null {
	const map = lowerCaseHeaders(headers);
	if (!map) return null;

	const ms = map.get("retry-after-ms");
	if (ms && /^\d+(?:\.\d+)?$/.test(ms)) {
		const parsed = finiteNonNegative(Number(ms));
		if (parsed != null) return parsed;
	}
	const retryAfter = map.get("retry-after");
	if (retryAfter) {
		const parsed = parseRetryAfter(retryAfter, now);
		if (parsed != null) return parsed;
	}

	let latest: number | null = null;
	for (const name of [
		"ratelimit-reset",
		"x-ratelimit-reset",
		"x-ratelimit-reset-requests",
		"x-ratelimit-reset-tokens",
	]) {
		const value = map.get(name);
		if (!value) continue;
		const parsed = parseResetValue(value, now);
		if (parsed != null && (latest == null || parsed > latest)) latest = parsed;
	}
	return latest;
}
