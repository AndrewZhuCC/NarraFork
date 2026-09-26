import { afterEach, expect, setSystemTime, test } from "bun:test";
import { eq } from "drizzle-orm";
import { narrators } from "../../db/schema";

// Keep the established service/persistence initialization order; preload isolates the DB.
await import("../narrator-service");
const { db } = await import("../../db");
const { narratorPersistence } = await import("../narrator-persistence");

const id = "turn-ended-at-regression";
const startedAt = "2026-09-26T10:00:00.000Z";
const endedAt = "2026-09-26T10:02:00.000Z";
const later = "2026-09-26T10:45:00.000Z";

async function seed() {
	setSystemTime(new Date(startedAt));
	await db.insert(narrators).values({ id, createdAt: startedAt, updatedAt: startedAt });
}

async function read() {
	return db.query.narrators.findFirst({ where: eq(narrators.id, id) });
}

afterEach(async () => {
	setSystemTime();
	await db.delete(narrators).where(eq(narrators.id, id));
});

test("completion freezes the end through read acknowledgements and metadata edits", async () => {
	await seed();
	await narratorPersistence.updateStatus(id, "working", { setTurnStart: true });
	expect((await read())?.turnEndedAt).toBeNull();
	setSystemTime(new Date(endedAt));
	expect(
		await narratorPersistence.compareAndSetStatus(id, ["working", "waiting"], "idle", {
			substatus: ["unread"],
		}),
	).toBe(true);
	expect((await read())?.turnEndedAt).toBe(endedAt);

	setSystemTime(new Date(later));
	await narratorPersistence.removeSubstatus(id, "unread");
	await narratorPersistence.updateTitle(id, "renamed while idle");
	await narratorPersistence.updateModel(id, "test-model");
	await narratorPersistence.updateReasoningEffort(id, "high");
	await narratorPersistence.updateStatus(id, "idle");
	expect(await read()).toMatchObject({
		turnStartedAt: startedAt,
		turnEndedAt: endedAt,
		updatedAt: later,
	});

	// A repeated terminal CAS must not turn an idle metadata update into another completion.
	expect(await narratorPersistence.compareAndSetStatus(id, "idle", "idle")).toBe(true);
	expect((await read())?.turnEndedAt).toBe(endedAt);
	await narratorPersistence.updateStatus(id, "working", { setTurnStart: true });
	expect(await read()).toMatchObject({ turnStartedAt: later, turnEndedAt: null });
});

test("interrupt stamps once, waiting stays active, and resume clears the previous end", async () => {
	await seed();
	await narratorPersistence.updateStatus(id, "working", { setTurnStart: true });
	await narratorPersistence.updateStatus(id, "waiting");
	expect((await read())?.turnEndedAt).toBeNull();
	setSystemTime(new Date(endedAt));
	await narratorPersistence.updateStatus(id, "idle", { substatus: ["interrupted"] });
	expect((await read())?.turnEndedAt).toBe(endedAt);

	setSystemTime(new Date(later));
	await narratorPersistence.updateStatus(id, "idle", { substatus: ["interrupted"] });
	expect((await read())?.turnEndedAt).toBe(endedAt);
	await narratorPersistence.updateStatus(id, "working", { resumeTurn: true });
	expect(await read()).toMatchObject({ turnStartedAt: startedAt, turnEndedAt: null });
	expect(await narratorPersistence.compareAndSetStatus(id, "waiting", "idle")).toBe(false);
	expect((await read())?.turnEndedAt).toBeNull();
	expect(await narratorPersistence.compareAndSetStatus(id, "working", "idle")).toBe(true);
	expect((await read())?.turnEndedAt).toBe(later);
});

test("an empty narrator does not acquire a fictitious turn end", async () => {
	await seed();
	await narratorPersistence.updateStatus(id, "idle");
	expect(await read()).toMatchObject({ turnStartedAt: null, turnEndedAt: null });
});
