import { afterEach, describe, expect, it } from "bun:test";
import { eq } from "drizzle-orm";
import { projects } from "../../../server/db/schema";
import { cleanDb, getTestDb } from "../../setup";

const { db, sqlite } = getTestDb();

afterEach(() => cleanDb(sqlite));

const now = new Date().toISOString();

describe("test DB setup", () => {
	it("can insert and query projects", async () => {
		db.insert(projects).values({ id: "p1", name: "Test", createdAt: now, updatedAt: now }).run();

		const result = await db.query.projects.findFirst({ where: eq(projects.id, "p1") });
		expect(result).toBeDefined();
		expect(result?.name).toBe("Test");
		expect(result?.status).toBe("active");
	});

	it("cleanDb resets between tests", async () => {
		const result = await db.query.projects.findFirst({ where: eq(projects.id, "p1") });
		expect(result).toBeUndefined();
	});
});
