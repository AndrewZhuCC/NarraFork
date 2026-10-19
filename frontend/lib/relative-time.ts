/** Format an ISO timestamp as relative time (e.g. "5m ago" or "in 5m"). */
export function relativeTime(iso: string | undefined): string {
	if (!iso) return "-";
	const timestamp = new Date(iso).getTime();
	if (!Number.isFinite(timestamp)) return "-";

	const diff = Date.now() - timestamp;
	const absDiff = Math.abs(diff);
	const future = diff < 0;
	const suffix = future ? "" : " ago";
	const prefix = future ? "in " : "";

	if (absDiff < 60_000) return future ? "in <1m" : "<1m ago";
	if (absDiff < 3600_000) return `${prefix}${Math.floor(absDiff / 60_000)}m${suffix}`;
	if (absDiff < 86400_000) return `${prefix}${Math.floor(absDiff / 3600_000)}h${suffix}`;
	return `${prefix}${Math.floor(absDiff / 86400_000)}d${suffix}`;
}

/**
 * Countdown to a future timestamp, e.g. "2h 30m" — what a usage window's reset
 * time wants, where "in 2h" (relative time) reads wrong for a deadline.
 *
 * Returns undefined for a past or unparseable instant so each caller can pick its
 * own wording for "expired" versus "unknown".
 */
export function formatCountdown(targetMs: number): string | undefined {
	const diff = targetMs - Date.now();
	if (!Number.isFinite(diff) || diff <= 0) return undefined;
	const totalMinutes = Math.floor(diff / 60_000);
	const days = Math.floor(totalMinutes / 1440);
	const hours = Math.floor((totalMinutes % 1440) / 60);
	const minutes = totalMinutes % 60;
	if (days > 0) return `${days}d ${hours}h ${minutes}m`;
	if (hours > 0) return `${hours}h ${minutes}m`;
	return `${minutes}m`;
}
