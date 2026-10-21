import { describe, expect, it } from "bun:test";
import type { HumanAttentionItem } from "@shared/human-attention";
import {
	buildDashboardSessions,
	type DashboardNarrator,
	directoryLabel,
} from "./dashboard-sessions";

function narrator(id: string, extra: Partial<DashboardNarrator> = {}): DashboardNarrator {
	return { id, title: `title-${id}`, status: "idle", lastMessageAt: null, ...extra };
}

function decision(narratorId: string, extra: Partial<HumanAttentionItem> = {}): HumanAttentionItem {
	return {
		id: `permission:${narratorId}:${Math.random()}`,
		kind: "permission",
		source: "permission",
		requestId: "r",
		toolCallId: "t",
		toolName: "Bash",
		narratorId,
		narratorTitle: `owner-${narratorId}`,
		parentNarratorId: null,
		rootNarratorId: null,
		chapterId: null,
		createdAt: "2026-01-01T00:00:00Z",
		blocking: false,
		canAct: true,
		summary: "",
		...extra,
	};
}

const empty = { attentionItems: [], working: [], waiting: [], recent: [] };

describe("buildDashboardSessions", () => {
	it("puts a working session with an open decision under attention, not running", () => {
		const working = [narrator("a", { status: "working" }), narrator("b", { status: "working" })];
		const groups = buildDashboardSessions({
			...empty,
			working,
			attentionItems: [decision("a", { blocking: true })],
		});
		expect(groups.attention.map((r) => r.narratorId)).toEqual(["a"]);
		expect(groups.attention[0]?.title).toBe("title-a");
		expect(groups.running.map((r) => r.narratorId)).toEqual(["b"]);
	});

	it("attributes subagent decisions to the root session and counts them", () => {
		const groups = buildDashboardSessions({
			...empty,
			attentionItems: [
				decision("sub1", { rootNarratorId: "root" }),
				decision("sub2", { rootNarratorId: "root", blocking: true, summary: "rm -rf build" }),
			],
		});
		expect(groups.attention).toHaveLength(1);
		expect(groups.attention[0]).toMatchObject({
			narratorId: "root",
			decisionCount: 2,
			blocking: true,
			detail: "rm -rf build",
		});
	});

	it("orders decisions before waiting before errors, blocking first", () => {
		const groups = buildDashboardSessions({
			attentionItems: [decision("d1"), decision("d2", { blocking: true })],
			working: [],
			waiting: [narrator("w", { status: "waiting" })],
			recent: [narrator("e", { errorMessage: "boom" })],
		});
		expect(groups.attention.map((r) => r.narratorId)).toEqual(["d2", "d1", "w", "e"]);
		expect(groups.attention.map((r) => r.reason)).toEqual([
			"decision",
			"decision",
			"waiting",
			"error",
		]);
	});

	it("does not flag a working session as errored, and keeps recent to idle leftovers", () => {
		const groups = buildDashboardSessions({
			...empty,
			working: [narrator("x", { status: "working", errorMessage: "old" })],
			recent: [
				narrator("x", { status: "working", errorMessage: "old" }),
				narrator("i1", { lastMessageAt: "2026-01-02T00:00:00Z" }),
				narrator("i2", { lastMessageAt: "2026-01-03T00:00:00Z" }),
			],
		});
		expect(groups.attention).toHaveLength(0);
		expect(groups.running.map((r) => r.narratorId)).toEqual(["x"]);
		expect(groups.recent.map((r) => r.narratorId)).toEqual(["i2", "i1"]);
	});
});

describe("directoryLabel", () => {
	it("returns the last path segment", () => {
		expect(directoryLabel("/home/me/repo/")).toBe("repo");
		expect(directoryLabel("C:\\work\\app")).toBe("app");
		expect(directoryLabel("/")).toBe("/");
	});
});
