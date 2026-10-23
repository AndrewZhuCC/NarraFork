import { beforeAll, beforeEach, expect, test } from "bun:test";
import { existsSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { eq } from "drizzle-orm";
import { Hono } from "hono";
import { db } from "../../db";
import { aclGrants, narratorProjectLinks, narrators, projects, users } from "../../db/schema";
import { AppError } from "../../lib/errors";
import { generateId } from "../../lib/id";
import { getHome } from "../../lib/platform";
import { settings } from "../../lib/settings";
import { getAvailableCommands } from "../../services/command-service";
import { canReadNarrator, canWriteNarrator } from "../../services/narrator-acl";
import { narratorService } from "../../services/narrator-service";
import { narratorRoutes } from "../narrators";

const owner = generateId();
const member = generateId();
const stranger = generateId();
const now = new Date().toISOString();
let projectId: string;
let projectDir: string;

beforeAll(async () => {
	for (const id of [owner, member, stranger]) {
		await db.insert(users).values({
			id,
			username: id,
			passwordHash: "fixture",
			role: "user",
			createdAt: now,
		});
	}
});
beforeEach(async () => {
	settings.agent.defaultNarratorVisibility = "auto";
	settings.agent.defaultNarratorWriteAudience = "auto";
	settings.paths.defaultProjectDir = "";
	projectId = generateId();
	projectDir = join(getHome(), `missing-project-${projectId}`);
	await db.insert(projects).values({
		id: projectId,
		name: "Metadata-only project",
		gitPath: projectDir,
		ownerUserId: owner,
		visibility: "private",
		chapterSettings: { commands: [{ name: "project-command", prompt: "Project prompt" }] },
		createdAt: now,
		updatedAt: now,
	});
});

function appAs(userId: string) {
	return new Hono()
		.use("*", async (c, next) => {
			c.set("user", { sub: userId, role: "user", iat: 0, exp: Number.MAX_SAFE_INTEGER });
			await next();
		})
		.onError((error) => {
			if (error instanceof AppError) {
				return new Response(JSON.stringify({ error: error.message, code: error.code }), {
					status: error.statusCode,
					headers: { "Content-Type": "application/json" },
				});
			}
			throw error;
		})
		.route("/narrators", narratorRoutes);
}
function create(body: unknown, userId = owner) {
	return appAs(userId).request("/narrators", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
	});
}
async function grant(capability: "read" | "write", userId = member) {
	const id = generateId();
	await db.insert(aclGrants).values({
		id,
		scopeType: "project",
		scopeId: projectId,
		principalType: "user",
		principalId: userId,
		capability,
		createdAt: now,
	});
	return id;
}
async function row(id: string) {
	const result = await db.query.narrators.findFirst({ where: eq(narrators.id, id) });
	if (!result) throw new Error("Narrator missing");
	return result;
}

test("HTTP project-only creation uses authenticated owner and metadata cwd without creating paths", async () => {
	const response = await create({ contextProjectId: projectId, ownerUserId: stranger });
	expect(response.status).toBe(201);
	const created = await row((await response.json()).id);
	expect(created.contextProjectId).toBe(projectId);
	expect(created.ownerUserId).toBe(owner);
	expect(created.cwd).toBe(projectDir);
	expect(created.visibility).toBe("project");
	expect(created.writeAudience).toBe("project");
	expect(existsSync(projectDir)).toBe(false);
});

test("explicit cwd wins over the project path, which wins over the global default", async () => {
	settings.paths.defaultProjectDir = join(getHome(), "ignored-global-default");
	const explicit = join(getHome(), "explicit-project-cwd");
	const response = await create({ contextProjectId: projectId, cwd: explicit });
	expect(response.status).toBe(201);
	const created = await row((await response.json()).id);
	expect(created.cwd).toBe(explicit);
	expect(created.contextProjectId).toBe(projectId);
	const defaultResponse = await create({ contextProjectId: projectId });
	expect((await row((await defaultResponse.json()).id)).cwd).toBe(projectDir);
});

test("unbound HTTP creation preserves standalone defaults and does not infer project from cwd", async () => {
	const response = await create({ cwd: projectDir });
	expect(response.status).toBe(201);
	const created = await row((await response.json()).id);
	expect(created.contextProjectId).toBeNull();
	expect(created.visibility).toBe("private");
	expect(created.writeAudience).toBe("owner");
});

test("unbound creation without cwd requires an existing default project directory", async () => {
	for (const configured of ["", join(getHome(), `missing-default-${projectId}`)]) {
		settings.paths.defaultProjectDir = configured;
		const response = await create({});
		expect(response.status).toBe(400);
		expect((await response.json()).code).toBe("PROJECT_DIR_REQUIRED");
	}
	const existing = mkdtempSync(join(tmpdir(), "nf-default-project-"));
	settings.paths.defaultProjectDir = existing;
	const response = await create({});
	expect(response.status).toBe(201);
	expect((await row((await response.json()).id)).cwd).toBe(existing);
});

test("public validator rejects malformed project contexts", async () => {
	for (const contextProjectId of ["", "x".repeat(129), 42]) {
		expect((await create({ contextProjectId })).status).toBe(400);
	}
});

test("a project without a default path falls back to the standalone cwd policy", async () => {
	await db.update(projects).set({ gitPath: null }).where(eq(projects.id, projectId));
	const required = await create({ contextProjectId: projectId });
	expect(required.status).toBe(400);
	expect((await required.json()).code).toBe("PROJECT_DIR_REQUIRED");
	const existing = mkdtempSync(join(tmpdir(), "nf-default-project-"));
	settings.paths.defaultProjectDir = existing;
	const response = await create({ contextProjectId: projectId });
	expect(response.status).toBe(201);
	const created = await row((await response.json()).id);
	expect(created.cwd).toBe(existing);
	expect(created.contextProjectId).toBe(projectId);
});

test("creation needs project write: hidden, nonexistent, public-read and read-only grants cannot bind", async () => {
	expect((await create({ contextProjectId: projectId }, stranger)).status).toBe(404);
	expect((await create({ contextProjectId: generateId() })).status).toBe(404);
	await db.update(projects).set({ visibility: "public" }).where(eq(projects.id, projectId));
	expect((await create({ contextProjectId: projectId }, stranger)).status).toBe(404);
	await grant("read");
	expect((await create({ contextProjectId: projectId }, member)).status).toBe(404);
	expect(
		await db.query.narrators.findFirst({ where: eq(narrators.contextProjectId, projectId) }),
	).toBeUndefined();
});

test("project grant revocation gates creation, non-owner access and listing without changing owner policy", async () => {
	const grantId = await grant("write");
	const response = await create({ contextProjectId: projectId }, member);
	expect(response.status).toBe(201);
	const created = await row((await response.json()).id);
	expect(created.ownerUserId).toBe(member);
	const ownerSession = await row((await (await create({ contextProjectId: projectId })).json()).id);
	expect((await appAs(member).request(`/narrators/${ownerSession.id}`)).status).toBe(200);
	await db.delete(aclGrants).where(eq(aclGrants.id, grantId));
	expect((await create({ contextProjectId: projectId }, member)).status).toBe(404);
	expect((await appAs(member).request(`/narrators/${ownerSession.id}`)).status).toBe(404);
	// Existing narrator ownership remains an explicit authority; do not alter ACL policy here.
	expect(await canWriteNarrator(created, { userId: member, isAdmin: false })).toBe(true);
	expect((await appAs(member).request(`/narrators?projectId=${projectId}`)).status).toBe(404);
});

for (const visibility of ["private", "public"] as const) {
	test(`HTTP project audiences respect global ${visibility} override`, async () => {
		settings.agent.defaultNarratorVisibility = visibility;
		const response = await create({ contextProjectId: projectId });
		const created = await row((await response.json()).id);
		expect(created.visibility).toBe(visibility);
		expect(created.writeAudience).toBe(visibility === "private" ? "owner" : "public");
	});
}
test("HTTP project owner-only global write override remains narrower than project visibility", async () => {
	settings.agent.defaultNarratorWriteAudience = "owner";
	const response = await create({ contextProjectId: projectId });
	expect(response.status).toBe(201);
	const created = await row((await response.json()).id);
	expect(created.visibility).toBe("project");
	expect(created.writeAudience).toBe("owner");
	await grant("write");
	expect(await canReadNarrator(created, { userId: member, isAdmin: false })).toBe(true);
	expect(await canWriteNarrator(created, { userId: member, isAdmin: false })).toBe(false);
});

test("HTTP project write default is clamped while direct external provisioning stays private", async () => {
	settings.agent.defaultNarratorWriteAudience = "public";
	const response = await create({ contextProjectId: projectId });
	const created = await row((await response.json()).id);
	expect(created.visibility).toBe("project");
	expect(created.writeAudience).toBe("project");
	const external = await narratorService.create({
		contextProjectId: projectId,
		ownerUserId: owner,
	});
	expect(external.visibility).toBe("private");
	expect(external.writeAudience).toBe("owner");
	const explicitExternal = await narratorService.create({
		contextProjectId: projectId,
		ownerUserId: owner,
		visibility: "private",
		writeAudience: "owner",
		oauthPolicySnapshotJson: { fixture: "frozen" },
	});
	expect(explicitExternal.oauthPolicySnapshotJson).toEqual({ fixture: "frozen" });
	expect(explicitExternal.visibility).toBe("private");
});

async function link(
	narratorId: string,
	linkedProjectId: string,
	role: "primary" | "additional",
	source: "cwd" | "explicit" = "cwd",
) {
	await db.insert(narratorProjectLinks).values({
		narratorId,
		projectId: linkedProjectId,
		role,
		source,
		directory: source === "cwd" ? projectDir : null,
		updatedAt: now,
	});
}

test("project listing includes indexed sessions and explicit contexts, leaving historical rows unchanged", async () => {
	// A former chapter session: no explicit context, found through its cwd-derived link.
	const indexedId = generateId();
	await db.insert(narrators).values({
		id: indexedId,
		ownerUserId: owner,
		cwd: projectDir,
		visibility: "project",
		writeAudience: "project",
		createdAt: now,
		updatedAt: now,
	});
	await link(indexedId, projectId, "primary");
	const before = await row(indexedId);
	const response = await create({ contextProjectId: projectId });
	const createdId = (await response.json()).id;
	const unboundId = (await (await create({ cwd: join(getHome(), "unindexed-cwd") })).json()).id;
	const listResponse = await appAs(owner).request(`/narrators?projectId=${projectId}`);
	expect(listResponse.status).toBe(200);
	const list = await listResponse.json();
	const ids = list.items.map((item: { id: string }) => item.id);
	expect(ids).toContain(indexedId);
	expect(ids).toContain(createdId);
	expect(ids).not.toContain(unboundId);
	expect(await row(indexedId)).toEqual(before);
	expect((await getAvailableCommands(indexedId, owner))[0]?.name).toBe("project-command");
});

test("project-only children inherit configuration context but delegate authority to root, including after revocation", async () => {
	const grantId = await grant("write");
	const response = await create({ contextProjectId: projectId });
	const root = await row((await response.json()).id);
	const child = await narratorService.createSubagent({
		parentNarratorId: root.id,
		subagentType: "general",
		cwd: projectDir,
	});
	for (const item of [child]) {
		expect(item.contextProjectId).toBe(projectId);
		expect(item.aclRootNarratorId).toBe(root.id);
		expect(item.ownerUserId).toBe(owner);
		expect(item.visibility).toBe("private");
		expect(item.writeAudience).toBe("owner");
		expect(await canReadNarrator(item, { userId: member, isAdmin: false })).toBe(true);
		expect(await canWriteNarrator(item, { userId: member, isAdmin: false })).toBe(true);
		expect((await getAvailableCommands(item.id, owner))[0]?.name).toBe("project-command");
	}
	await db
		.update(narrators)
		.set({ visibility: "private", writeAudience: "owner" })
		.where(eq(narrators.id, root.id));
	expect(await canReadNarrator(child, { userId: member, isAdmin: false })).toBe(false);
	await db
		.update(narrators)
		.set({ visibility: "project", writeAudience: "project" })
		.where(eq(narrators.id, root.id));
	await db.delete(aclGrants).where(eq(aclGrants.id, grantId));
	expect(await canReadNarrator(child, { userId: member, isAdmin: false })).toBe(false);
	expect(await canWriteNarrator(child, { userId: member, isAdmin: false })).toBe(false);
	expect((await getAvailableCommands(root.id, owner))[0]?.name).toBe("project-command");
});

test("commands follow only the primary project; additional links and no project add nothing", async () => {
	const conflictingProjectId = generateId();
	await db.insert(projects).values({
		id: conflictingProjectId,
		name: "Other",
		gitPath: projectDir,
		chapterSettings: { commands: [{ name: "wrong-command", prompt: "Wrong" }] },
		createdAt: now,
		updatedAt: now,
	});
	// Explicit context is the primary project even when a cwd link points elsewhere.
	const explicitId = generateId();
	await db.insert(narrators).values({
		id: explicitId,
		contextProjectId: projectId,
		ownerUserId: owner,
		createdAt: now,
		updatedAt: now,
	});
	await link(explicitId, projectId, "primary", "explicit");
	await link(explicitId, conflictingProjectId, "additional");
	// Without explicit context, the cwd-derived primary link decides.
	const derivedId = generateId();
	await db.insert(narrators).values({
		id: derivedId,
		ownerUserId: owner,
		cwd: projectDir,
		createdAt: now,
		updatedAt: now,
	});
	await link(derivedId, projectId, "primary");
	await link(derivedId, conflictingProjectId, "additional");
	for (const narratorId of [explicitId, derivedId])
		expect((await getAvailableCommands(narratorId, owner)).map((command) => command.name)).toEqual([
			"project-command",
		]);
	const unbound = await narratorService.create({ ownerUserId: owner });
	expect(await getAvailableCommands(unbound.id, owner)).toEqual([]);
});
