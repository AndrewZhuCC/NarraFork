/**
 * Plan-quota exhaustion: the shared identity of the refusal payload.
 *
 * A quota wall is the one "hard limit" that is also *recoverable on a published
 * schedule*: Kimi's `GET {origin}/coding/v1/usages` and OpenCode Go's
 * `GET …/go/v1/usage` (plus its `retry-after` header) report when a window
 * resets, so a refusal can be waited out instead of failing the turn. When it is
 * NOT waited out — the reset is beyond the wait budget, unknown, or this run has
 * already suspended on quota too often — the wall is reported instead, and this
 * is how both ends recognize that report.
 *
 * It travels in the narrator's `errorMessage` as a JSON payload, matching
 * `payment_required`: that column is the ONLY carrier of a failure that survives
 * to the next page load (`errorCode` and `diagnostics` are not persisted), so a
 * value the UI must read later has to live there.
 *
 * Shared rather than duplicated because a mismatch is silent: the server would
 * serialize a payload the UI never looks for, and the user would be shown the raw
 * upstream 403/429 again — the exact defect this exists to prevent.
 */

/**
 * Also used as the machine `errorCode` and the diagnostics reason for the same
 * refusal, so one string identifies it on every channel.
 */
export const KIMI_QUOTA_EXHAUSTED = "kimi_quota_exhausted";
export const OPENCODE_QUOTA_EXHAUSTED = "opencode_quota_exhausted";

/** Providers whose quota refusals are waited out on a known reset clock. */
export type QuotaProviderKind = "kimi" | "opencode";

/** Display name of a quota provider, for user-facing messages. */
export const QUOTA_PROVIDER_LABELS: Record<QuotaProviderKind, string> = {
	kimi: "Kimi",
	opencode: "OpenCode Go",
};

/**
 * Display name for a quota wait/refusal. Defaults to Kimi: events and payloads
 * written before OpenCode support carry no provider and were always Kimi.
 */
export function quotaProviderLabel(provider: QuotaProviderKind | null | undefined): string {
	return QUOTA_PROVIDER_LABELS[provider ?? "kimi"] ?? QUOTA_PROVIDER_LABELS.kimi;
}

export function quotaExhaustedCode(provider: QuotaProviderKind | undefined): string {
	return provider === "opencode" ? OPENCODE_QUOTA_EXHAUSTED : KIMI_QUOTA_EXHAUSTED;
}

/** The provider a persisted refusal payload type names, or null for any other type. */
export function quotaProviderFromCode(type: unknown): QuotaProviderKind | null {
	if (type === KIMI_QUOTA_EXHAUSTED) return "kimi";
	if (type === OPENCODE_QUOTA_EXHAUSTED) return "opencode";
	return null;
}
