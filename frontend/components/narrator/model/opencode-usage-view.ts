import type { OpencodeUsageCache, OpencodeUsageWindow } from "../../../lib/api/types";
import { opencodeUsageFailed } from "./opencode-usage-format";

/**
 * Display model for an OpenCode Go plan allowance.
 *
 * Pure: everything here is a function of the cached `/usage` payload and the
 * clock, so the status-bar indicator, its popover and the model picker badge
 * agree on the same numbers, and tests can pin them without rendering.
 *
 * OpenCode Go meters each model against three windows (docs: console/go):
 *  - 5-hour rolling — 20% of the monthly allowance, counted from recent usage;
 *  - weekly — 50%, starting Monday 00:00 UTC;
 *  - monthly — 100%, anchored to the subscription's own anniversary.
 * Upstream reports `percent` USED; this layer also derives what is LEFT,
 * because that is what every other quota indicator in the app shows.
 */

export type OpencodeWindowKey = "fiveHour" | "weekly" | "monthly";

/** Ordered by severity; `unknown` is "no number to judge by". */
export type OpencodeQuotaLevel = "ok" | "warning" | "critical" | "exhausted" | "unknown";

const LEVEL_RANK: Record<OpencodeQuotaLevel, number> = {
	unknown: 0,
	ok: 1,
	warning: 2,
	critical: 3,
	exhausted: 4,
};

/** Remaining share at or below which a window is flagged. */
export const OPENCODE_WARNING_REMAINING_PERCENT = 25;
export const OPENCODE_CRITICAL_REMAINING_PERCENT = 10;

/** Mantine colour per level, shared by badge, progress bar and dot. */
export const OPENCODE_LEVEL_COLORS: Record<OpencodeQuotaLevel, string> = {
	ok: "teal",
	warning: "yellow",
	critical: "red",
	exhausted: "red",
	unknown: "gray",
};

export interface OpencodeWindowView {
	key: OpencodeWindowKey;
	usedPercent: number | null;
	remainingPercent: number | null;
	/** Absolute amounts, only when upstream reported both. */
	used: number | null;
	limit: number | null;
	/** Epoch ms the window refills, or null when unknown. */
	resetAt: number | null;
	/** True when `resetAt` comes from the documented rule, not upstream. */
	resetDerived: boolean;
	level: OpencodeQuotaLevel;
}

export interface OpencodeQuotaView {
	/** Windows upstream reported, in display order; missing ones are omitted. */
	windows: OpencodeWindowView[];
	/** The window with the least allowance left, which bounds the next request. */
	limiting: OpencodeWindowView | null;
	/** Worst level across windows. */
	level: OpencodeQuotaLevel;
	balance: number | null;
	/** The last refresh failed; any numbers shown are from an earlier fetch. */
	failed: boolean;
	fetchedAt: number;
}

/** Next Monday 00:00 UTC strictly after `now` — the Go week boundary. */
export function nextOpencodeWeekStartUtc(now: number): number {
	const date = new Date(now);
	const day = date.getUTCDay(); // 0 = Sunday
	const daysUntilMonday = (8 - day) % 7 || 7;
	return Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate() + daysUntilMonday);
}

function clampPercent(value: number): number {
	return Math.max(0, Math.min(100, value));
}

export function levelForWindow(window: OpencodeUsageWindow): OpencodeQuotaLevel {
	if (window.status === "rate-limited") return "exhausted";
	if (window.percent == null) return "unknown";
	const remaining = 100 - clampPercent(window.percent);
	if (remaining <= 0) return "exhausted";
	if (remaining <= OPENCODE_CRITICAL_REMAINING_PERCENT) return "critical";
	if (remaining <= OPENCODE_WARNING_REMAINING_PERCENT) return "warning";
	return "ok";
}

function parseReset(value: string | null): number | null {
	if (!value) return null;
	const parsed = Date.parse(value);
	return Number.isFinite(parsed) ? parsed : null;
}

function windowView(
	key: OpencodeWindowKey,
	window: OpencodeUsageWindow,
	now: number,
): OpencodeWindowView {
	const usedPercent = window.percent == null ? null : clampPercent(window.percent);
	let resetAt = parseReset(window.resetsAt);
	let resetDerived = false;
	// Only the weekly window has a reset derivable from the published rule; the
	// rolling window depends on recent usage and the month on the subscription.
	if (resetAt == null && key === "weekly") {
		resetAt = nextOpencodeWeekStartUtc(now);
		resetDerived = true;
	}
	const hasAmounts = window.used != null && window.limit != null && window.limit > 0;
	return {
		key,
		usedPercent,
		remainingPercent: usedPercent == null ? null : Math.round(100 - usedPercent),
		used: hasAmounts ? (window.used ?? null) : null,
		limit: hasAmounts ? (window.limit ?? null) : null,
		resetAt,
		resetDerived,
		level: levelForWindow(window),
	};
}

export function worstLevel(levels: OpencodeQuotaLevel[]): OpencodeQuotaLevel {
	return levels.reduce<OpencodeQuotaLevel>(
		(worst, level) => (LEVEL_RANK[level] > LEVEL_RANK[worst] ? level : worst),
		"unknown",
	);
}

/**
 * Build the display model, or null when there is nothing to show yet (a cold
 * cache that has never been fetched and has not failed).
 */
export function buildOpencodeQuotaView(
	usage: OpencodeUsageCache | null | undefined,
	now: number,
): OpencodeQuotaView | null {
	if (!usage) return null;
	const entries: Array<[OpencodeWindowKey, OpencodeUsageWindow | null]> = [
		["fiveHour", usage.rolling],
		["weekly", usage.weekly],
		["monthly", usage.monthly],
	];
	const windows = entries.flatMap(([key, window]) =>
		window ? [windowView(key, window, now)] : [],
	);
	const failed = opencodeUsageFailed(usage);
	const balance =
		typeof usage.balance === "number" && Number.isFinite(usage.balance) ? usage.balance : null;
	if (windows.length === 0 && !failed && balance == null) return null;

	let limiting: OpencodeWindowView | null = null;
	for (const window of windows) {
		if (window.level === "exhausted") {
			// An exhausted window bounds the account no matter what the others say;
			// prefer the one that refills LAST, since that is when requests resume.
			if (
				limiting?.level !== "exhausted" ||
				(window.resetAt ?? Number.POSITIVE_INFINITY) >
					(limiting.resetAt ?? Number.POSITIVE_INFINITY)
			) {
				limiting = window;
			}
			continue;
		}
		if (limiting?.level === "exhausted" || window.remainingPercent == null) continue;
		if (limiting?.remainingPercent == null || window.remainingPercent < limiting.remainingPercent) {
			limiting = window;
		}
	}
	return {
		windows,
		limiting: limiting ?? windows[0] ?? null,
		level: worstLevel(windows.map((window) => window.level)),
		balance,
		failed,
		fetchedAt: usage.fetchedAt,
	};
}

/** Minimal translation signature compatible with react-i18next's `t`. */
export type OpencodeViewT = (key: string, options?: Record<string, unknown>) => string;

/** "3d 4h" / "2h 15m" / "12m" / "now", for a reset countdown. */
export function formatOpencodeCountdown(ms: number, t: OpencodeViewT): string {
	if (!Number.isFinite(ms) || ms <= 0) return t("opencode.resetDue");
	const totalMinutes = Math.max(1, Math.ceil(ms / 60_000));
	const days = Math.floor(totalMinutes / 1_440);
	const hours = Math.floor((totalMinutes % 1_440) / 60);
	const minutes = totalMinutes % 60;
	if (days > 0) return t("opencode.countdownDH", { d: days, h: hours });
	if (hours > 0) return t("opencode.countdownHM", { h: hours, m: minutes });
	return t("opencode.countdownM", { m: minutes });
}

const SHORT_LABEL_KEYS: Record<OpencodeWindowKey, string> = {
	fiveHour: "opencode.short5h",
	weekly: "opencode.shortWeekly",
	monthly: "opencode.shortMonthly",
};

const LONG_LABEL_KEYS: Record<OpencodeWindowKey, string> = {
	fiveHour: "opencode.window5h",
	weekly: "opencode.windowWeekly",
	monthly: "opencode.windowMonthly",
};

export function opencodeWindowLabel(key: OpencodeWindowKey, t: OpencodeViewT): string {
	return t(LONG_LABEL_KEYS[key]);
}

/**
 * Compact text for the status bar and picker badge: the limiting window and
 * what is left of it ("5h 63%"), "exhausted" once spent, or the unavailable
 * placeholder when there is no number at all.
 */
export function formatOpencodeBadgeText(view: OpencodeQuotaView, t: OpencodeViewT): string {
	const limiting = view.limiting;
	if (!limiting || limiting.remainingPercent == null) {
		if (limiting?.level === "exhausted") return t("opencode.exhaustedShort");
		return t("opencode.unavailable");
	}
	const label = t(SHORT_LABEL_KEYS[limiting.key]);
	if (limiting.level === "exhausted") return t("opencode.badgeExhausted", { label });
	return t("opencode.badgeRemaining", { label, percent: limiting.remainingPercent });
}

/** Up to two decimals, no trailing zeros, for plan amounts. */
export function formatOpencodeAmount(value: number): string {
	return Number.isInteger(value) ? String(value) : value.toFixed(2).replace(/\.?0+$/, "");
}

/** One plain-text line per window, used as the picker badge tooltip. */
export function formatOpencodeSummaryLines(
	view: OpencodeQuotaView,
	now: number,
	t: OpencodeViewT,
): string[] {
	const lines = view.windows.map((window) => {
		const label = opencodeWindowLabel(window.key, t);
		const remaining =
			window.remainingPercent == null
				? t("opencode.remainingUnknown")
				: t("opencode.remainingPercent", { percent: window.remainingPercent });
		const reset =
			window.resetAt == null
				? ""
				: ` · ${t("opencode.resetsIn", { time: formatOpencodeCountdown(window.resetAt - now, t) })}`;
		return `${label}: ${remaining}${reset}`;
	});
	if (view.balance != null) {
		lines.push(t("opencode.balanceLine", { amount: formatOpencodeAmount(view.balance) }));
	}
	if (view.failed) lines.push(t("opencode.staleHint"));
	return lines;
}
