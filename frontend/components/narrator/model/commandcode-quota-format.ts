/**
 * Command Code 的额度视图。
 *
 * 与 TokenDance 共用同一套 `quota` 结构，供会话界面在上下文容量右侧显示。主文本给月度剩余
 * 百分比；悬停详情把两个滚动窗口换算成占月度总额度的比例，并附上窗口内部的使用率：
 *   `5h限额: 1% / 20% (已用3%)`
 * 含义依次是 窗口已用 / 月度总额、窗口上限 / 月度总额、窗口内已用比例。
 */

import type { CommandCodeUsageResponse } from "../../../lib/api/commandcode";

type Translate = (key: string, values?: Record<string, string>) => string;

function windowLine(
	label: string,
	window: { used: number; cap: number } | undefined,
	monthlyTotal: number | undefined,
	t: Translate,
): string | undefined {
	if (!window || window.cap <= 0 || monthlyTotal === undefined || monthlyTotal <= 0) return;
	return t("commandcodeQuotaWindow", {
		label,
		usedOfTotal: String(Math.round((window.used / monthlyTotal) * 100)),
		capOfTotal: String(Math.round((window.cap / monthlyTotal) * 100)),
		usedInWindow: String(Math.round((window.used / window.cap) * 100)),
	});
}

export function commandCodeQuotaView(
	usage: CommandCodeUsageResponse | undefined,
	enabled: boolean,
	t: Translate,
) {
	if (!enabled) return null;
	// 月度总配额 = 本周期已用 + 当前余额；主文本给剩余百分比。
	const remaining = usage?.credits?.monthlyCredits;
	const used = usage?.summary?.totalMonthlyCredits;
	const total = remaining !== undefined && used !== undefined ? remaining + used : undefined;
	const percent =
		remaining !== undefined && total !== undefined && total > 0
			? Math.round((remaining / total) * 100)
			: undefined;
	const balance =
		percent === undefined
			? t("commandcodeQuotaUnknown")
			: t("commandcodeQuota", { percent: String(percent) });

	// 原始数据区块先出，便于核对下面百分比的含义。
	const raw: string[] = [];
	if (used !== undefined && total !== undefined) {
		raw.push(t("commandcodeQuotaRawMonthly", { used: used.toFixed(2), total: total.toFixed(2) }));
	}
	const rawWindow = (label: string, window?: { used: number; cap: number }) => {
		if (!window || window.cap <= 0) return;
		raw.push(
			t("commandcodeQuotaRawWindow", {
				label,
				used: window.used.toFixed(2),
				cap: String(window.cap),
			}),
		);
	};
	rawWindow(t("commandcodeQuotaRawWeekly"), usage?.windowLimits?.weekly);
	rawWindow(t("commandcodeQuotaRawFiveHour"), usage?.windowLimits?.fiveHour);

	const windows = [
		windowLine(t("commandcodeQuotaWeekly"), usage?.windowLimits?.weekly, total, t),
		windowLine(t("commandcodeQuotaFiveHour"), usage?.windowLimits?.fiveHour, total, t),
	].filter((line): line is string => !!line);

	// 原始数据在上、百分比在下，中间空一行。
	const lines = [...raw];
	if (raw.length > 0 && windows.length > 0) lines.push("");
	lines.push(...windows);
	if (lines.length === 0) lines.push(t("commandcodeQuotaUnknown"));

	return {
		balance,
		detailsText: lines.join("\n"),
		hasDetailsPopover: true,
	};
}
