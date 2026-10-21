import { describe, expect, test } from "bun:test";
import type { CommandCodeUsageResponse } from "../../../lib/api/commandcode";
import { commandCodeQuotaView } from "./commandcode-quota-format";

const t = (key: string) => key;

const USAGE: CommandCodeUsageResponse = {
	loggedIn: true,
	credits: {
		monthlyCredits: 60,
		purchasedCredits: 0,
		freeCredits: 0,
		creditThreshold: 0,
		belowThreshold: false,
	},
	summary: {
		totalCount: 0,
		totalTokens: 0,
		totalTokensIn: 0,
		totalTokensOut: 0,
		successRate: 1,
		completedCount: 0,
		failedCount: 0,
		totalMonthlyCredits: 40,
	},
	windowLimits: {
		limited: false,
		fiveHour: { used: 18, cap: 20, exceeded: false, resetAt: 1_800_000_000_000 },
		weekly: { used: 10, cap: 50, exceeded: false },
	},
};

describe("commandCodeQuotaView", () => {
	test("disabled or not yet loaded shows nothing", () => {
		expect(commandCodeQuotaView(USAGE, false, t)).toBeNull();
		expect(commandCodeQuotaView(undefined, true, t)).toBeNull();
	});

	test("5h / weekly / monthly windows, limited by the least left", () => {
		const view = commandCodeQuotaView(USAGE, true, t);
		expect(view?.windows.map((w) => [w.id, w.used, w.limit, w.remainingPercent])).toEqual([
			["fiveHour", 18, 20, 10],
			["weekly", 10, 50, 80],
			["monthly", 40, 100, 60],
		]);
		expect(view?.windows[0]?.resetAt).toBe(1_800_000_000_000);
		expect(view?.limiting?.id).toBe("fiveHour");
		expect(view?.level).toBe("critical");
	});

	test("an exceeded window is exhausted even below its cap", () => {
		const view = commandCodeQuotaView(
			{
				...USAGE,
				windowLimits: {
					limited: true,
					weekly: { used: 49, cap: 50, exceeded: true },
				},
			},
			true,
			t,
		);
		expect(view?.limiting?.id).toBe("weekly");
		expect(view?.level).toBe("exhausted");
	});

	test("logged out is a failed refresh with no windows", () => {
		const view = commandCodeQuotaView({ loggedIn: false, reason: "expired" }, true, t);
		expect(view?.windows).toEqual([]);
		expect(view?.failed).toBe(true);
	});
});
