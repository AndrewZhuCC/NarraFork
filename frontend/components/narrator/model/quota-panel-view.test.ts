import { describe, expect, test } from "bun:test";
import {
	formatQuotaBadgeText,
	formatQuotaCountdown,
	pickLimitingWindow,
	type QuotaPanelView,
	quotaLevelForRemaining,
	quotaWindowFromAmounts,
} from "./quota-panel-view";

const t = (key: string, options?: Record<string, unknown>) =>
	options ? `${key}[${Object.values(options).join(",")}]` : key;

function view(overrides: Partial<QuotaPanelView> = {}): QuotaPanelView {
	return {
		title: "title",
		unavailableText: "unavailable",
		windows: [],
		limiting: null,
		level: "unknown",
		rows: [],
		failed: false,
		failureDetail: null,
		fetchedAt: 0,
		...overrides,
	};
}

describe("quota panel view", () => {
	test("levels follow the 25% / 10% remaining thresholds", () => {
		expect(quotaLevelForRemaining(null)).toBe("unknown");
		expect(quotaLevelForRemaining(26)).toBe("ok");
		expect(quotaLevelForRemaining(25)).toBe("warning");
		expect(quotaLevelForRemaining(10)).toBe("critical");
		expect(quotaLevelForRemaining(0)).toBe("exhausted");
		expect(quotaLevelForRemaining(80, true)).toBe("exhausted");
	});

	test("amounts are clamped into a percentage", () => {
		const over = quotaWindowFromAmounts({
			id: "w",
			label: "W",
			shortLabel: "w",
			used: 120,
			limit: 100,
		});
		expect(over.usedPercent).toBe(100);
		expect(over.level).toBe("exhausted");
		const noLimit = quotaWindowFromAmounts({
			id: "w",
			label: "W",
			shortLabel: "w",
			used: 1,
			limit: 0,
		});
		expect(noLimit.usedPercent).toBeNull();
		expect(noLimit.used).toBeNull();
	});

	test("an exhausted window that refills last bounds the account", () => {
		const a = { level: "exhausted" as const, remainingPercent: 0, resetAt: 10 };
		const b = { level: "exhausted" as const, remainingPercent: 0, resetAt: 20 };
		const c = { level: "ok" as const, remainingPercent: 50, resetAt: null };
		expect(pickLimitingWindow([c, a, b])).toBe(b);
		expect(pickLimitingWindow([])).toBeNull();
	});

	test("badge text names the limiting window or falls back to the placeholder", () => {
		const window = quotaWindowFromAmounts({
			id: "w",
			label: "Weekly",
			shortLabel: "Wk",
			used: 30,
			limit: 100,
		});
		expect(formatQuotaBadgeText(view({ windows: [window], limiting: window }), t)).toBe(
			"quotaPanel.badgeRemaining[Wk,70]",
		);
		expect(formatQuotaBadgeText(view(), t)).toBe("unavailable");
	});

	test("countdown units", () => {
		expect(formatQuotaCountdown(0, t)).toBe("quotaPanel.resetDue");
		expect(formatQuotaCountdown(90_000, t)).toBe("quotaPanel.countdownM[2]");
		expect(formatQuotaCountdown(3 * 86_400_000 + 3_600_000, t)).toBe("quotaPanel.countdownDH[3,1]");
	});
});
