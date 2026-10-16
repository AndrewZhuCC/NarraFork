import { afterAll, beforeEach, describe, expect, mock, test } from "bun:test";
import type { ComposerDocument } from "@shared/composer-document";
import { and, eq } from "drizzle-orm";
import { cleanDb, getTestDb } from "../../../tests/setup";
import { migrateLegacyNarratorDraftTraits } from "../../db/migrate-narrator-drafts";
import { narratorDrafts, narrators, users } from "../../db/schema";
import { upsertDraftTrait } from "../../lib/narrator-utils";

const { db, sqlite } = getTestDb();
// Snapshot the real db module before mocking so afterAll can re-point it back.
// Bun's mock.module is process-wide and mock.restore() does NOT undo it, so this
// FTS-less test db would otherwise leak into later real-db suites (e.g. the
// knowledge FTS tests fail with "no such table: knowledge_drafts_fts").
const realDbModule = { ...(await import("../../db")) };
mock.module("../../db", () => ({ db, sqlite }));

const {
	getNarratorDraft,
	getNarratorDraftMetadata,
	getNarratorIdsWithDraft,
	narratorHasDraft,
	updateNarratorDraft,
} = await import("../narrator-draft-service");

async function seedUser(id: string): Promise<void> {
	await db.insert(users).values({
		id,
		username: id,
		passwordHash: "hash",
		role: "user",
		createdAt: new Date().toISOString(),
	});
}

async function seedNarrator(id: string, traits: string[] = []): Promise<void> {
	const now = new Date().toISOString();
	await db.insert(narrators).values({ id, traits, createdAt: now, updatedAt: now });
}

beforeEach(() => cleanDb(sqlite));

afterAll(() => {
	mock.module("../../db", () => realDbModule);
	mock.restore();
	cleanDb(sqlite);
});

describe("per-user narrator drafts", () => {
	test.each([
		"",
		" \n\t",
	])("empty rich drafts persist and return the same tombstone: %j", async (text) => {
		await seedUser("rich-user");
		await seedNarrator("rich-narrator");
		const document: ComposerDocument = {
			version: 1,
			paragraphs: [{ children: text ? [{ type: "text", text }] : [] }],
			assets: [],
		};
		const saved = await updateNarratorDraft(
			"rich-user",
			"rich-narrator",
			text,
			"tab",
			0,
			[],
			document,
		);
		expect(saved).toMatchObject({
			hasDraft: false,
			text: "",
			composerDocument: undefined,
			revision: 1,
			previousHasDraft: false,
		});
		if ("conflict" in saved) throw new Error("Unexpected draft conflict");
		const { previousHasDraft: _previous, ...state } = saved;
		expect(await getNarratorDraft("rich-user", "rich-narrator")).toEqual(state);
		expect(await getNarratorDraftMetadata("rich-user", "rich-narrator")).toEqual({
			hasDraft: false,
			revision: 1,
			updatedAt: state.updatedAt,
		});
		expect(await narratorHasDraft("rich-user", "rich-narrator")).toBe(false);
		expect(await getNarratorIdsWithDraft("rich-user", ["rich-narrator"])).toEqual(new Set());
		const row = await db.select().from(narratorDrafts).get();
		expect(row?.composerDocument).toBeNull();
		expect(row?.text).toBe("");
	});

	test("removing the last attachment clears layout in the response, storage and CAS conflicts", async () => {
		await seedUser("rich-user");
		await seedNarrator("rich-narrator");
		const document: ComposerDocument = {
			version: 1,
			paragraphs: [{ children: [{ type: "attachment", id: "node", assetId: "image" }] }],
			assets: [{ id: "image", kind: "image", name: "image.png", size: 9, mimeType: "image/png" }],
		};
		await updateNarratorDraft("rich-user", "rich-narrator", "", "tab", 0, [], document);
		expect(await getNarratorDraftMetadata("rich-user", "rich-narrator")).toMatchObject({
			hasDraft: true,
		});
		const cleared = await updateNarratorDraft("rich-user", "rich-narrator", "", undefined, 1, [], {
			version: 1,
			paragraphs: [{ children: [] }],
			assets: [],
		});
		expect(cleared).toMatchObject({
			hasDraft: false,
			composerDocument: undefined,
			revision: 2,
			previousHasDraft: true,
			sourceId: "tab",
		});
		expect(await getNarratorDraft("rich-user", "rich-narrator")).toMatchObject({
			hasDraft: false,
			composerDocument: undefined,
			revision: 2,
		});
		expect(await getNarratorDraftMetadata("rich-user", "rich-narrator")).toMatchObject({
			hasDraft: false,
		});
		expect(await narratorHasDraft("rich-user", "rich-narrator")).toBe(false);
		expect(await getNarratorIdsWithDraft("rich-user", ["rich-narrator"])).toEqual(new Set());
		expect(
			await updateNarratorDraft("rich-user", "rich-narrator", "stale", "tab", 1),
		).toMatchObject({
			conflict: true,
			current: { hasDraft: false, composerDocument: undefined, revision: 2 },
		});
		expect((await db.select().from(narratorDrafts).get())?.composerDocument).toBeNull();
	});

	test.each([
		{},
		{ version: 99, paragraphs: [], assets: [] },
		{ version: 1, paragraphs: [], assets: { length: 1 } },
		{ version: 1, paragraphs: [], assets: [{ id: "invalid" }] },
	])("empty historical drafts tolerate invalid document JSON: %j", async (historical) => {
		await seedUser("rich-user");
		await seedNarrator("rich-narrator");
		await updateNarratorDraft("rich-user", "rich-narrator", "", "tab", 0);
		sqlite.run("UPDATE narrator_drafts SET composer_document = ?", [JSON.stringify(historical)]);
		expect(await getNarratorDraft("rich-user", "rich-narrator")).toMatchObject({
			hasDraft: false,
			composerDocument: undefined,
			text: "",
			revision: 1,
		});
		// Metadata remains a cheap SQL presence summary, without decoding historical JSON.
		expect(await getNarratorDraftMetadata("rich-user", "rich-narrator")).toMatchObject({
			hasDraft: true,
		});
		expect(await updateNarratorDraft("rich-user", "rich-narrator", "", undefined, 1)).toMatchObject(
			{
				hasDraft: false,
				composerDocument: undefined,
				previousHasDraft: false,
				revision: 2,
			},
		);
		expect(await getNarratorDraftMetadata("rich-user", "rich-narrator")).toMatchObject({
			hasDraft: false,
		});
	});

	test("legacy saves keep matching file references while clearing rich layout", async () => {
		await seedUser("rich-user");
		await seedNarrator("rich-narrator");
		const reference = {
			id: "ref",
			deviceId: "local",
			path: "/a.ts",
			label: "a.ts",
			inputRange: [0, 10] as [number, number],
		};
		const document: ComposerDocument = {
			version: 1,
			paragraphs: [{ children: [{ type: "fileReference", id: "node", reference }] }],
			assets: [],
		};
		await updateNarratorDraft(
			"rich-user",
			"rich-narrator",
			"#file:a.ts",
			"tab",
			0,
			[reference],
			document,
		);
		const saved = await updateNarratorDraft(
			"rich-user",
			"rich-narrator",
			"#file:a.ts",
			undefined,
			1,
		);
		expect(saved).toMatchObject({
			hasDraft: true,
			composerDocument: undefined,
			fileReferences: [reference],
			revision: 2,
			sourceId: "tab",
		});
		expect(await getNarratorDraft("rich-user", "rich-narrator")).toMatchObject({
			hasDraft: true,
			composerDocument: undefined,
			fileReferences: [reference],
			revision: 2,
		});
	});

	test("rich layout participates in CAS, missing blobs remain visible and legacy saves clear layout", async () => {
		await seedUser("rich-user");
		await seedNarrator("rich-narrator");
		const document = {
			version: 1 as const,
			paragraphs: [{ children: [{ type: "attachment" as const, id: "node", assetId: "image" }] }],
			assets: [
				{ id: "image", kind: "image" as const, name: "image.png", size: 9, mimeType: "image/png" },
			],
		};
		const first = await updateNarratorDraft(
			"rich-user",
			"rich-narrator",
			"",
			"tab",
			0,
			[],
			document,
		);
		expect(first).toMatchObject({ hasDraft: true, composerDocument: document, revision: 1 });
		expect(await getNarratorDraft("rich-user", "rich-narrator")).toMatchObject({
			composerDocument: document,
		});
		expect(await narratorHasDraft("rich-user", "rich-narrator")).toBe(true);
		expect(
			await updateNarratorDraft("rich-user", "rich-narrator", "stale", "tab", 0),
		).toMatchObject({ conflict: true, current: { composerDocument: document } });
		await updateNarratorDraft("rich-user", "rich-narrator", "legacy", "tab", 1);
		expect((await getNarratorDraft("rich-user", "rich-narrator")).composerDocument).toBeUndefined();
		await expect(
			updateNarratorDraft("rich-user", "rich-narrator", "different", "tab", 2, [], document),
		).rejects.toThrow("does not match");
	});
	test("isolates text and clear tombstones between users", async () => {
		await seedUser("user-a");
		await seedUser("user-b");
		await seedNarrator("narrator-1");

		await updateNarratorDraft("user-a", "narrator-1", "draft-a", "source-a");
		await updateNarratorDraft("user-b", "narrator-1", "draft-b", "source-b");

		expect(await getNarratorDraft("user-a", "narrator-1")).toMatchObject({
			hasDraft: true,
			text: "draft-a",
			updatedBy: "user-a",
		});
		expect(await getNarratorDraft("user-b", "narrator-1")).toMatchObject({
			hasDraft: true,
			text: "draft-b",
			updatedBy: "user-b",
		});

		const cleared = await updateNarratorDraft("user-a", "narrator-1", "", "source-a");
		expect(cleared).toMatchObject({
			hasDraft: false,
			text: "",
			revision: 2,
			previousHasDraft: true,
		});
		expect("conflict" in cleared ? null : cleared.updatedAt).not.toBeNull();
		expect(await getNarratorDraft("user-b", "narrator-1")).toMatchObject({
			hasDraft: true,
			text: "draft-b",
		});
	});

	test("rejects stale base revisions without overwriting the newer draft", async () => {
		await seedUser("user-a");
		await seedNarrator("narrator-1");

		const first = await updateNarratorDraft("user-a", "narrator-1", "first", "source-a", 0);
		expect(first).toMatchObject({ revision: 1, text: "first" });
		const second = await updateNarratorDraft("user-a", "narrator-1", "second", "source-b", 1);
		expect(second).toMatchObject({ revision: 2, text: "second" });

		const stale = await updateNarratorDraft("user-a", "narrator-1", "stale", "source-a", 1);
		expect(stale).toEqual({
			conflict: true,
			current: expect.objectContaining({ revision: 2, text: "second", sourceId: "source-b" }),
		});
		expect(await getNarratorDraft("user-a", "narrator-1")).toMatchObject({
			revision: 2,
			text: "second",
		});
	});

	test("returns draft markers only for the requested user", async () => {
		await seedUser("user-a");
		await seedUser("user-b");
		await seedNarrator("narrator-1");
		await seedNarrator("narrator-2");
		await updateNarratorDraft("user-a", "narrator-1", "a");
		await updateNarratorDraft("user-b", "narrator-2", "b");

		expect(await getNarratorIdsWithDraft("user-a", ["narrator-1", "narrator-2"])).toEqual(
			new Set(["narrator-1"]),
		);
		expect(await getNarratorIdsWithDraft("user-b", ["narrator-1", "narrator-2"])).toEqual(
			new Set(["narrator-2"]),
		);
	});

	test("narratorHasDraft reflects presence without reading draft text", async () => {
		await seedUser("user-a");
		await seedNarrator("narrator-1");
		expect(await narratorHasDraft("user-a", "narrator-1")).toBe(false);

		await updateNarratorDraft("user-a", "narrator-1", "typing…", "source-a");
		expect(await narratorHasDraft("user-a", "narrator-1")).toBe(true);
		// A cleared (tombstone) draft must read as absent.
		await updateNarratorDraft("user-a", "narrator-1", "", "source-a", 1);
		expect(await narratorHasDraft("user-a", "narrator-1")).toBe(false);
		// Another user's draft never leaks into this user's presence check.
		await seedUser("user-b");
		await updateNarratorDraft("user-b", "narrator-1", "b-draft", "source-b");
		expect(await narratorHasDraft("user-a", "narrator-1")).toBe(false);
	});

	test("preserves the stored sourceId when a later update omits it", async () => {
		await seedUser("user-a");
		await seedNarrator("narrator-1");

		await updateNarratorDraft("user-a", "narrator-1", "first", "source-a", 0);
		// Autosave that does not resend sourceId must keep the prior attribution.
		const kept = await updateNarratorDraft("user-a", "narrator-1", "second", undefined, 1);
		expect(kept).toMatchObject({ text: "second", sourceId: "source-a" });
		expect(await getNarratorDraft("user-a", "narrator-1")).toMatchObject({
			text: "second",
			sourceId: "source-a",
		});
		// An explicit new sourceId still reassigns it.
		const reassigned = await updateNarratorDraft("user-a", "narrator-1", "third", "source-b", 2);
		expect(reassigned).toMatchObject({ sourceId: "source-b" });
	});
});

describe("legacy narrator draft migration", () => {
	test("moves an owned trait to the private table and strips shared content", async () => {
		await seedUser("user-a");
		await seedNarrator(
			"narrator-1",
			upsertDraftTrait(["standalone"], {
				text: "legacy secret",
				updatedAt: "2026-07-13T21:53:05.000Z",
				updatedBy: "user-a",
				sourceId: "legacy-source",
			}),
		);

		expect(migrateLegacyNarratorDraftTraits(sqlite)).toEqual({ migrated: 1, discarded: 0 });
		const draft = await db
			.select()
			.from(narratorDrafts)
			.where(and(eq(narratorDrafts.userId, "user-a"), eq(narratorDrafts.narratorId, "narrator-1")))
			.get();
		expect(draft?.text).toBe("legacy secret");
		const narrator = await db
			.select({ traits: narrators.traits })
			.from(narrators)
			.where(eq(narrators.id, "narrator-1"))
			.get();
		expect(narrator?.traits).toEqual(["standalone"]);
	});

	test("discards a trait whose owner cannot be verified", async () => {
		await seedNarrator(
			"narrator-1",
			upsertDraftTrait([], {
				text: "orphaned secret",
				updatedAt: "2026-07-13T21:53:05.000Z",
				updatedBy: "missing-user",
			}),
		);

		expect(migrateLegacyNarratorDraftTraits(sqlite)).toEqual({ migrated: 0, discarded: 1 });
		expect(await db.select().from(narratorDrafts)).toEqual([]);
	});
});
