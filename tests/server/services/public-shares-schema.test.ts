import { afterAll, beforeEach, describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { eq } from "drizzle-orm";
import {
	chatMessages,
	chatRooms,
	narratorPublicShares,
	narrators,
	users,
} from "../../../server/db/schema";
import { cleanDb, getTestDb } from "../../setup";

// Replays the real generated migrations in memory, never the running instance's DB.
const { db, sqlite } = getTestDb();
const now = "2026-01-01T00:00:00.000Z";

type TestDb = ReturnType<typeof getTestDb>["db"];

function seed(db: TestDb): void {
	db.insert(users)
		.values({ id: "owner", username: "owner", passwordHash: "x", createdAt: now })
		.run();
	db.insert(narrators)
		.values({
			id: "session",
			title: "Private",
			ownerUserId: "owner",
			createdAt: now,
			updatedAt: now,
		})
		.run();
	db.insert(chatRooms)
		.values({ id: "room", kind: "narrator", narratorId: "session", createdAt: now })
		.run();
	db.insert(narratorPublicShares)
		.values([
			{
				id: "a",
				narratorId: "session",
				tokenHash: "hash-a",
				guestName: "Guest A",
				createdByUserId: "owner",
				createdAt: now,
			},
			{
				id: "b",
				narratorId: "session",
				tokenHash: "hash-b",
				guestName: "Guest B",
				createdByUserId: "owner",
				createdAt: now,
			},
		])
		.run();
	db.insert(chatMessages)
		.values({
			id: "message",
			roomId: "room",
			seq: 1,
			senderShareId: "a",
			senderGuestName: "Guest A",
			contentText: "Discussion only",
			createdAt: now,
		})
		.run();
}

beforeEach(() => {
	cleanDb(sqlite);
	seed(db);
});
afterAll(() => sqlite.close());

describe("generated public share schema", () => {
	test("the generated FK rebuild preserves existing message bodies and references", () => {
		// Replayed on today's schema, the rebuild drops every column added after it, which
		// production never sees (0163 runs before them). So it runs on its own database,
		// and the comparison covers the columns the rebuild carries.
		const replay = getTestDb();
		try {
			seed(replay.db);
			const rows = () =>
				replay.sqlite.query("SELECT * FROM chat_messages").all() as Record<string, unknown>[];
			const before = rows();
			const migration = readFileSync(
				new URL("../../../drizzle/0163_spooky_mad_thinker.sql", import.meta.url),
				"utf8",
			);
			for (const statement of migration.split("--> statement-breakpoint")) {
				if (statement.trim()) replay.sqlite.run(statement);
			}
			const after = rows();
			const carried = Object.keys(after[0] ?? {});
			expect(carried).toContain("content_text");
			expect(after).toEqual(
				before.map((row) => Object.fromEntries(carried.map((column) => [column, row[column]]))),
			);
			expect(replay.sqlite.query("PRAGMA foreign_key_check").all()).toEqual([]);
		} finally {
			replay.sqlite.close();
		}
	});

	test("allows multiple links for one session but never duplicate credentials", () => {
		expect(db.select().from(narratorPublicShares).all()).toHaveLength(2);
		expect(() =>
			db
				.insert(narratorPublicShares)
				.values({
					id: "c",
					narratorId: "session",
					tokenHash: "hash-a",
					guestName: "Other",
					createdAt: now,
				})
				.run(),
		).toThrow();
	});

	test("revocation preserves other links and the guest attribution", () => {
		db.update(narratorPublicShares)
			.set({ revokedAt: now })
			.where(eq(narratorPublicShares.id, "a"))
			.run();
		expect(
			db.select().from(narratorPublicShares).where(eq(narratorPublicShares.id, "b")).get()
				?.revokedAt,
		).toBeNull();
		expect(db.select().from(chatMessages).get()?.senderGuestName).toBe("Guest A");
		expect(db.select().from(narrators).get()?.visibility).toBe("private");
	});

	test("removing a link cannot erase or strand historical guest messages", () => {
		db.delete(narratorPublicShares).where(eq(narratorPublicShares.id, "a")).run();
		const message = db.select().from(chatMessages).get();
		expect(message?.senderShareId).toBeNull();
		expect(message?.senderGuestName).toBe("Guest A");
		expect(message?.contentText).toBe("Discussion only");
	});

	test("existing user messages retain their nullable guest defaults", () => {
		db.insert(chatMessages)
			.values({
				id: "internal",
				roomId: "room",
				seq: 2,
				senderUserId: "owner",
				contentText: "Internal reply",
				createdAt: now,
			})
			.run();
		const message = db.select().from(chatMessages).where(eq(chatMessages.id, "internal")).get();
		expect(message?.senderGuestName).toBeNull();
		expect(message?.senderShareId).toBeNull();
		expect(message?.replyToGuestName).toBeNull();
	});
});
