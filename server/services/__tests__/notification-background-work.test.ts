/**
 * `notification-service` listener gating on `backgroundWorkActive`.
 *
 * A `narrator:attention` done event raised while non-service background work is
 * still running must NOT fan out to IM webhooks (DingTalk/Feishu) — the session
 * is not actually finished. The unflagged event (e.g. the wake-up turn after the
 * last background task completes) still notifies exactly as before.
 */

import { afterAll, afterEach, beforeEach, describe, expect, it, mock } from "bun:test";
import { cleanDb, getTestDb } from "../../../tests/setup";
import {
	narrators,
	userPreferences,
	userRecentTabs,
	userRecentTabsMeta,
	users,
} from "../../db/schema";

const { db, sqlite } = getTestDb();
const realDbModule = { ...(await import("../../db")) };
const realNarratorWsModule = { ...(await import("../../websocket/narrator-ws")) };
mock.module("../../db", () => ({ db, sqlite }));
mock.module("../../websocket/narrator-ws", () => ({
	broadcastToUser: () => {},
	getNarratorPresenceBatch: () => new Map(),
}));

// Importing the service registers its `narrator:attention` listener as a
// module side effect — the gating under test lives in that listener.
await import("../notification-service");
const { eventBus } = await import("../../lib/event-bus");

const NOW = "2026-10-19T00:00:00.000Z";
const originalFetch = globalThis.fetch;
const requests: Array<{ url: string; init?: RequestInit }> = [];

function seedFixture(): void {
	db.insert(users)
		.values({
			id: "bg-notify-user",
			username: "bg-notify-user",
			passwordHash: "test-password-hash",
			role: "user",
			createdAt: NOW,
		})
		.run();
	db.insert(userPreferences)
		.values({
			id: "pref-bg-notify-user",
			userId: "bg-notify-user",
			recentTabs: "[]",
			notifyDingtalkEnabled: true,
			notifyDingtalkWebhook: "https://example.test/hook?access=1",
			createdAt: NOW,
			updatedAt: NOW,
		})
		.run();
	db.insert(narrators)
		.values({
			id: "narrator-bg-notify",
			title: "Background-work narrator",
			status: "idle",
			visibility: "public",
			createdAt: NOW,
			updatedAt: NOW,
		})
		.run();
	db.insert(userRecentTabsMeta)
		.values({
			userId: "bg-notify-user",
			revision: 1,
			migratedAt: NOW,
			createdAt: NOW,
			updatedAt: NOW,
		})
		.run();
	db.insert(userRecentTabs)
		.values({
			id: "recent-bg-notify",
			userId: "bg-notify-user",
			tabKey: "narrator:narrator-bg-notify",
			section: "work",
			type: "narrator",
			entityId: "narrator-bg-notify",
			representedNarratorId: "narrator-bg-notify",
			title: "Background-work narrator",
			lastVisitedAt: 1,
			sortOrder: 0,
			createdAt: NOW,
			updatedAt: NOW,
		})
		.run();
}

/** The listener handler is async; give it a few macrotasks before asserting. */
async function settleListener(): Promise<void> {
	for (let i = 0; i < 5; i++) await new Promise<void>((resolve) => setImmediate(resolve));
}

beforeEach(() => {
	requests.length = 0;
	globalThis.fetch = mock(async (input: string | URL | Request, init?: RequestInit) => {
		requests.push({ url: String(input), init });
		return new Response("ok", { status: 200 });
	}) as unknown as typeof fetch;
});

afterEach(() => {
	globalThis.fetch = originalFetch;
	cleanDb(sqlite);
});

afterAll(() => {
	mock.module("../../db", () => realDbModule);
	mock.module("../../websocket/narrator-ws", () => realNarratorWsModule);
	mock.restore();
});

describe("notification-service backgroundWorkActive gating", () => {
	it("suppresses the IM webhook when the done event carries backgroundWorkActive", async () => {
		seedFixture();

		eventBus.emit({
			type: "narrator:attention",
			narratorId: "narrator-bg-notify",
			reason: "done",
			backgroundWorkActive: true,
		});
		await settleListener();

		expect(requests).toHaveLength(0);
	});

	it("still notifies on an unflagged done event", async () => {
		seedFixture();

		eventBus.emit({
			type: "narrator:attention",
			narratorId: "narrator-bg-notify",
			reason: "done",
		});
		await settleListener();

		expect(requests).toHaveLength(1);
		expect(requests[0]?.url).toContain("https://example.test/hook?access=1");
	});
});
