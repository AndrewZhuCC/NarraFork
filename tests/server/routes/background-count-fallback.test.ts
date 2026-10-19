import { afterEach, beforeEach, describe, expect, it, mock, spyOn } from "bun:test";
import type { BackgroundTaskActiveCounts } from "@shared/background-task-list";
import { Hono } from "hono";
import { db, sqlite } from "../../../server/db";
import { narrators, users } from "../../../server/db/schema";
import { logger } from "../../../server/lib/logger";
import { narratorRoutes } from "../../../server/routes/narrators";
import { backgroundTaskService } from "../../../server/services/background-task-service";
import {
	broadcastTabsSnapshot,
	enrichTabs,
} from "../../../server/services/user-preferences-service";
import * as narratorWs from "../../../server/websocket/narrator-ws";
import { cleanDb } from "../../setup";

const USER_ID = "count-fallback-user";
const FIRST_ID = "count-fallback-first";
const SECOND_ID = "count-fallback-second";
const COUNTS: BackgroundTaskActiveCounts = {
	activeCount: 4,
	activeWorkCount: 3,
	activeServiceCount: 1,
};
const COUNT_FIELDS = [
	"activeBackgroundTaskCount",
	"activeBackgroundWorkCount",
	"activeBackgroundServiceCount",
] as const;
const VIEWERS = [{ userId: USER_ID, username: "viewer", avatarColor: null, avatarImageId: null }];

const app = new Hono();
app.use("*", async (c, next) => {
	c.set("user", { sub: USER_ID, role: "user", iat: 0, exp: Number.MAX_SAFE_INTEGER });
	await next();
});
app.route("/narrators", narratorRoutes);

beforeEach(async () => {
	await db.insert(users).values({
		id: USER_ID,
		username: USER_ID,
		passwordHash: "test",
		createdAt: "2026-10-01T00:00:00.000Z",
	});
	await db.insert(narrators).values(
		[FIRST_ID, SECOND_ID].map((id, index) => ({
			id,
			title: `Narrator ${index}`,
			status: "working" as const,
			ownerUserId: USER_ID,
			createdAt: "2026-10-01T00:00:00.000Z",
			updatedAt: `2026-10-0${index + 1}T00:00:00.000Z`,
		})),
	);
});

afterEach(() => {
	mock.restore();
	cleanDb(sqlite);
});

function expectUnknownCounts(item: Record<string, unknown> | undefined) {
	if (!item) throw new Error("Expected narrator data");
	for (const field of COUNT_FIELDS) expect(Object.hasOwn(item, field)).toBe(false);
}

function expectCounts(
	item: Record<string, unknown> | undefined,
	counts: BackgroundTaskActiveCounts,
) {
	expect(item).toMatchObject({
		activeBackgroundTaskCount: counts.activeCount,
		activeBackgroundWorkCount: counts.activeWorkCount,
		activeBackgroundServiceCount: counts.activeServiceCount,
	});
}

function countSpy() {
	return spyOn(backgroundTaskService, "countActiveKindsByParentBatch");
}

async function list(path = "?standalone=true&sortOrder=asc") {
	const response = await app.request(`/narrators${path}`);
	expect(response.status).toBe(200);
	return response.json() as Promise<{
		items: Array<Record<string, unknown>>;
		hasMore: boolean;
		nextCursor: string | null;
		totalCount: number;
	}>;
}

describe("auxiliary background count failures", () => {
	it("still enriches and broadcasts tabs, preserving old counts and omitting unknown counts", async () => {
		const counts = countSpy().mockRejectedValue(new Error("occupancy unavailable"));
		const warn = spyOn(logger, "warn").mockImplementation(() => {});
		const presence = spyOn(narratorWs, "getNarratorPresenceBatch").mockReturnValue(
			new Map([[FIRST_ID, VIEWERS]]),
		);
		const broadcast = spyOn(narratorWs, "broadcastToUser").mockImplementation(() => {});
		const tabs: Array<Record<string, unknown>> = [
			{
				type: "narrator",
				id: FIRST_ID,
				status: "idle",
				activeBackgroundTaskCount: 9,
				activeBackgroundWorkCount: 7,
				activeBackgroundServiceCount: 2,
			},
			{ type: "subagent", id: SECOND_ID },
			{ type: "chapter", id: "chapter-tab", narratorId: FIRST_ID },
		];

		const enriched = await broadcastTabsSnapshot(USER_ID, tabs);

		expect(counts).toHaveBeenCalledWith([FIRST_ID, SECOND_ID, FIRST_ID]);
		expect(enriched[0]).toMatchObject({ status: "working", viewers: VIEWERS });
		expectCounts(enriched[0], { activeCount: 9, activeWorkCount: 7, activeServiceCount: 2 });
		for (const tab of enriched.slice(1)) {
			expect(tab.status).toBe("working");
			expectUnknownCounts(tab);
		}
		expect(presence).toHaveBeenCalled();
		expect(broadcast).toHaveBeenCalledWith(
			USER_ID,
			expect.objectContaining({ type: "user:recent_tabs_snapshot", tabs: enriched }),
		);
		expect(warn).toHaveBeenCalledWith("Failed to load background counts for recent tabs", {
			count: 3,
			error: "occupancy unavailable",
		});
	});

	it("only replaces tab counts after a successful read, including missing entries as zero", async () => {
		const counts = countSpy()
			.mockResolvedValueOnce(new Map([[FIRST_ID, COUNTS]]))
			.mockRejectedValueOnce("read failed")
			.mockResolvedValueOnce(new Map());
		const warn = spyOn(logger, "warn").mockImplementation(() => {});
		const tabs: Array<Record<string, unknown>> = [
			{ type: "narrator", id: FIRST_ID },
			{ type: "subagent", id: SECOND_ID },
		];

		await enrichTabs(tabs, USER_ID);
		expectCounts(tabs[0], COUNTS);
		expectCounts(tabs[1], { activeCount: 0, activeWorkCount: 0, activeServiceCount: 0 });
		await enrichTabs(tabs, USER_ID);
		expectCounts(tabs[0], COUNTS);
		expect(warn).toHaveBeenCalledWith("Failed to load background counts for recent tabs", {
			count: 2,
			error: "read failed",
		});
		await enrichTabs(tabs, USER_ID);
		for (const tab of tabs) {
			expectCounts(tab, { activeCount: 0, activeWorkCount: 0, activeServiceCount: 0 });
		}
		expect(counts).toHaveBeenCalledTimes(3);
	});

	it("returns basic narrator rows and intact pagination when occupancy fails", async () => {
		countSpy().mockRejectedValue(new Error("occupancy unavailable"));
		const warn = spyOn(logger, "warn").mockImplementation(() => {});

		const first = await list("?standalone=true&sortOrder=asc&limit=1");
		expect(first).toMatchObject({ totalCount: 2, hasMore: true });
		expect(first.nextCursor).toBeString();
		expect(first.items).toHaveLength(1);
		expect(first.items[0]).toMatchObject({
			id: FIRST_ID,
			status: "working",
			messageCount: 0,
			activeTerminalCount: 0,
			chapter: null,
			viewers: [],
		});
		expectUnknownCounts(first.items[0]);
		const second = await list(`?standalone=true&sortOrder=asc&limit=1&cursor=${first.nextCursor}`);
		expect(second).toMatchObject({ totalCount: 2, hasMore: false, nextCursor: null });
		expect(second.items[0]?.id).toBe(SECOND_ID);
		expectUnknownCounts(second.items[0]);
		expect(warn).toHaveBeenCalledWith("Failed to load background counts for narrator list", {
			count: 1,
			error: "occupancy unavailable",
		});
	});

	it("does not cache fabricated zeros and resumes list enrichment after recovery", async () => {
		const counts = countSpy()
			.mockResolvedValueOnce(new Map([[FIRST_ID, COUNTS]]))
			.mockRejectedValueOnce("read failed")
			.mockResolvedValueOnce(new Map());
		const warn = spyOn(logger, "warn").mockImplementation(() => {});

		const populated = await list();
		expectCounts(populated.items[0], COUNTS);
		expectCounts(populated.items[1], { activeCount: 0, activeWorkCount: 0, activeServiceCount: 0 });
		const failed = await list();
		for (const item of failed.items) expectUnknownCounts(item);
		expect(warn).toHaveBeenCalledWith("Failed to load background counts for narrator list", {
			count: 2,
			error: "read failed",
		});
		const recovered = await list();
		for (const item of recovered.items) {
			expectCounts(item, { activeCount: 0, activeWorkCount: 0, activeServiceCount: 0 });
		}
		expect(counts).toHaveBeenCalledTimes(3);
	});
});
