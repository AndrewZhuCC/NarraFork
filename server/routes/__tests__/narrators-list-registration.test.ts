import { beforeAll, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { Hono } from "hono";
import { db } from "../../db";
import { chapters, narrators, projects, users } from "../../db/schema";
import { narratorService } from "../../services/narrator-service";
import { narratorRoutes } from "../narrators";

const NOW = "2026-10-01T00:00:00.000Z";
beforeAll(async () => {
	await db
		.insert(users)
		.values({ id: "list-owner", username: "list-owner", passwordHash: "x", createdAt: NOW });
	await db.insert(projects).values({
		id: "list-project",
		name: "List",
		gitPath: "/unused-list-path",
		ownerUserId: "list-owner",
		createdAt: NOW,
		updatedAt: NOW,
	});
	await db.insert(chapters).values({
		id: "list-chapter",
		projectId: "list-project",
		title: "List",
		branch: "list",
		baseBranch: "main",
		createdAt: NOW,
		updatedAt: NOW,
	});
	await db.insert(narrators).values(
		[null, "list-chapter"].map((chapterId, i) => ({
			id: `list-${i}`,
			chapterId,
			ownerUserId: "list-owner",
			handle: i === 0 ? "list-named" : null,
			handleFolded: i === 0 ? "list-named" : null,
			traits: i === 0 ? ["standalone", "named"] : [],
			runtimeRegistrationJson: {
				version: 1 as const,
				run: {
					logicalRunId: "private-run",
					principal: { userId: "private-execution-principal" },
				},
			},
			createdAt: NOW,
			updatedAt: NOW,
		})),
	);
});
test("list queries exclude the registration at SQL projection, not only response serialization", () => {
	const routes = readFileSync(join(import.meta.dir, "../narrators.ts"), "utf-8");
	const service = readFileSync(
		join(import.meta.dir, "../../services/narrator-service.ts"),
		"utf-8",
	);
	for (const site of [
		routes.slice(routes.indexOf("const [list, countResult]"), routes.indexOf("const totalCount")),
		service.slice(service.indexOf("async listNamed()"), service.indexOf("async setHandle(")),
		service.slice(service.indexOf("async listByChapter("), service.indexOf("async remove(")),
	]) {
		expect(site).toMatch(/columns:\s*\{\s*runtimeRegistrationJson:\s*false\s*\}/);
	}
});
test("standalone, chapter and named lists never return live runtime registration", async () => {
	const app = new Hono();
	app.use("*", async (c, next) => {
		c.set("user", { sub: "list-owner", role: "user", iat: 0, exp: Number.MAX_SAFE_INTEGER });
		await next();
	});
	app.route("/narrators", narratorRoutes);
	for (const suffix of ["?standalone=true", "?chapterId=list-chapter", "/named"]) {
		const response = await app.request(`http://localhost/narrators${suffix}`);
		expect(response.status).toBe(200);
		const body = await response.json();
		const items = Array.isArray(body) ? body : body.items;
		expect(items.length).toBeGreaterThan(0);
		for (const item of items) expect(item).not.toHaveProperty("runtimeRegistrationJson");
		expect(JSON.stringify(body)).not.toContain("private-execution-principal");
	}
	for (const items of [
		await narratorService.listNamed(),
		await narratorService.listByChapter("list-chapter"),
	]) {
		expect(items.length).toBeGreaterThan(0);
		for (const item of items) expect(item).not.toHaveProperty("runtimeRegistrationJson");
	}
});
