/**
 * Command Code 的额度视图，映射到与 OpenCode Go 相同的进度条面板（QuotaPanelIndicator）。
 *
 * 三个窗口各画一条进度条：5 小时与周窗口直接用上游的 used / cap（含重置时间），月度窗口的
 * 总额 = 本周期已用 + 当前余额。徽标显示剩余比例最少的窗口。
 */

import type { CommandCodeUsageResponse, CommandCodeWindow } from "../../../lib/api/commandcode";
import {
	pickLimitingWindow,
	type QuotaPanelT,
	type QuotaPanelView,
	type QuotaWindowView,
	quotaWindowFromAmounts,
	worstQuotaLevel,
} from "./quota-panel-view";

function rollingWindow(
	id: string,
	label: string,
	shortLabel: string,
	window: CommandCodeWindow | undefined,
): QuotaWindowView | null {
	if (!window || window.cap <= 0) return null;
	const view = quotaWindowFromAmounts({
		id,
		label,
		shortLabel,
		used: window.used,
		limit: window.cap,
		resetAt: window.resetAt ?? null,
	});
	return window.exceeded ? { ...view, level: "exhausted" } : view;
}

export function commandCodeQuotaView(
	usage: CommandCodeUsageResponse | undefined,
	enabled: boolean,
	t: QuotaPanelT,
): QuotaPanelView | null {
	if (!enabled || !usage) return null;
	// 月度总配额 = 本周期已用 + 当前余额。
	const remaining = usage.credits?.monthlyCredits;
	const used = usage.summary?.totalMonthlyCredits;
	const monthly =
		remaining !== undefined && used !== undefined && remaining + used > 0
			? quotaWindowFromAmounts({
					id: "monthly",
					label: t("commandcodeQuotaMonthly"),
					shortLabel: t("quotaPanel.shortMonthly"),
					used,
					limit: remaining + used,
				})
			: null;
	const windows = [
		rollingWindow(
			"fiveHour",
			t("commandcodeQuotaFiveHour"),
			t("quotaPanel.short5h"),
			usage.windowLimits?.fiveHour,
		),
		rollingWindow(
			"weekly",
			t("commandcodeQuotaWeekly"),
			t("quotaPanel.shortWeekly"),
			usage.windowLimits?.weekly,
		),
		monthly,
	].filter((window): window is QuotaWindowView => window != null);
	const failed = usage.loggedIn === false || usage.error != null;
	return {
		title: t("commandcodeQuotaTitle"),
		unavailableText: t("commandcodeQuotaUnknown"),
		windows,
		limiting: pickLimitingWindow(windows),
		level: worstQuotaLevel(windows.map((window) => window.level)),
		rows: [],
		failed,
		failureDetail: null,
		fetchedAt: 0,
	};
}
