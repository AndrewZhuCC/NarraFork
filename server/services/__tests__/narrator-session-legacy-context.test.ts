import { afterEach, beforeEach, expect, spyOn, test } from "bun:test";
import { join } from "node:path";
import type { WorkspaceContext } from "@shared/workspace-context";
import { eq } from "drizzle-orm";
import { db } from "../../db";
import {
	chapters,
	narratorMessageRefs,
	narratorMessages,
	narrators,
	projects,
	remoteDevices,
	users,
} from "../../db/schema";
import * as provider from "../../lib/agent/provider";
import { AppError } from "../../lib/errors";
import { generateId } from "../../lib/id";
import { getHome } from "../../lib/platform";
import * as deviceConnection from "../device-connection-service";
import { gitService } from "../git-service";
import { narratorService } from "../narrator-service";
import { closeNarrator, ensureNarrator, isLoopRunning } from "../narrator-session";

const now = new Date().toISOString();
const activeIds: string[] = [];
let owner: string;
let projectId: string;
let chapterId: string;
let projectDir: string;
let modelRequests: ReturnType<typeof spyOn<typeof provider, "getProvider">>;
let remoteRequests: ReturnType<typeof spyOn<typeof deviceConnection, "sendRpc">>;
let gitProbe: ReturnType<typeof spyOn<typeof gitService, "isGitRepo">>;

beforeEach(async () => {
	owner = generateId();
	projectId = generateId();
	chapterId = generateId();
	projectDir = join(getHome(), `legacy-project-${projectId}`);
	await db
		.insert(users)
		.values({ id: owner, username: owner, passwordHash: "fixture", createdAt: now });
	await db.insert(projects).values({
		id: projectId,
		name: "Legacy",
		gitPath: projectDir,
		ownerUserId: owner,
		createdAt: now,
		updatedAt: now,
	});
	await db.insert(chapters).values({
		id: chapterId,
		projectId,
		title: "Dormant non-root",
		branch: "legacy",
		baseBranch: "main",
		status: "dormant",
		isRoot: 0,
		worktreePath: null,
		createdAt: now,
		updatedAt: now,
	});
	// Production initialization runs normally; these guards only prevent accidental
	// model/remote dispatch and host git probing from reaching real external systems.
	modelRequests = spyOn(provider, "getProvider").mockImplementation(() => {
		throw new Error("Initialization must not request a model adapter");
	});
	remoteRequests = spyOn(deviceConnection, "sendRpc").mockImplementation(async () => {
		throw new Error("Initialization must not dispatch remote RPC");
	});
	gitProbe = spyOn(gitService, "isGitRepo").mockResolvedValue(false);
});
afterEach(() => {
	for (const id of activeIds.splice(0)) closeNarrator(id);
	expect(modelRequests).not.toHaveBeenCalled();
	expect(remoteRequests).not.toHaveBeenCalled();
	modelRequests.mockRestore();
	remoteRequests.mockRestore();
	gitProbe.mockRestore();
});

function context(deviceId: string, cwd: string, revision = 3): WorkspaceContext {
	return {
		revision,
		deviceId,
		cwd,
		pathFlavor: "posix",
		contextKey: `fixture:${deviceId}:${cwd}`,
		contextProjectId: projectId,
		capabilities: { switchDirectory: true },
	};
}
async function seedNarrator(
	options: { deviceId?: string; committed?: WorkspaceContext; cwd?: string } = {},
) {
	const id = generateId();
	activeIds.push(id);
	await db.insert(narrators).values({
		id,
		chapterId,
		cwd: options.cwd ?? null,
		ownerUserId: owner,
		model: "openai:test-model",
		status: "idle",
		defaultDeviceId: options.deviceId ?? null,
		workspaceRevision: 3,
		workspaceContext: options.committed ?? null,
		createdAt: now,
		updatedAt: now,
	});
	return id;
}
async function unchangedHistory(id: string) {
	const messageId = generateId();
	await db.insert(narratorMessages).values({
		id: messageId,
		narratorId: id,
		role: "assistant",
		contentJson: [{ type: "text", text: "Preserved legacy transcript" }],
		contentText: "Preserved legacy transcript",
		createdAt: now,
	});
	await db
		.insert(narratorMessageRefs)
		.values({ id: generateId(), narratorId: id, messageId, seq: 0 });
	const before = await narratorService.getMessages(id);
	return async () => {
		expect(await narratorService.getMessages(id)).toEqual(before);
		expect((await narratorService.getById(id, { userId: owner, isAdmin: false })).chapterId).toBe(
			chapterId,
		);
		expect((await db.query.chapters.findFirst({ where: eq(chapters.id, chapterId) }))?.status).toBe(
			"dormant",
		);
	};
}

test("ensureNarrator initializes dormant non-root with null host directories and a committed remote workspace", async () => {
	const deviceId = generateId();
	const remoteCwd = "/executor/legacy-workspace";
	await db.insert(remoteDevices).values({
		id: deviceId,
		name: "Fixture executor",
		slug: `fixture-${deviceId.toLowerCase()}`,
		tokenHash: "fixture-only",
		tokenPrefix: "fixture",
		scope: "project",
		projectId,
		ownerScope: "shared",
		createdBy: owner,
		defaultCwd: remoteCwd,
		platformOs: "linux",
		platformArch: "x64",
		createdAt: now,
		updatedAt: now,
	});
	expect(
		(await deviceConnection.getSessionDevices(projectId))?.some((device) => device.id === deviceId),
	).toBe(true);
	const committed = context(deviceId, remoteCwd);
	const id = await seedNarrator({ deviceId, committed });
	const checkHistory = await unchangedHistory(id);
	const active = await ensureNarrator(id, "en");
	expect(active.alive).toBe(true);
	expect(active._defaultDeviceId).toBe(deviceId);
	expect(active.cwd).toBe(projectDir);
	expect(active._projectGitPath).toBe(projectDir);
	expect(active._worktreePath).toBeUndefined();
	expect(isLoopRunning(id)).toBe(false);
	expect((await narratorService.getById(id)).workspaceContext).toEqual(committed);
	await checkHistory();
});

test("ensureNarrator accepts valid committed local cwd ahead of both missing chapter path and stale narrator cwd", async () => {
	const committed = context("local", getHome());
	const id = await seedNarrator({ committed });
	const checkHistory = await unchangedHistory(id);
	const active = await ensureNarrator(id, "en");
	expect(active.alive).toBe(true);
	expect(active.cwd).toBe(getHome());
	expect(active.cwd).not.toBe(projectDir);
	expect(isLoopRunning(id)).toBe(false);
	await checkHistory();
	closeNarrator(id);
	await db
		.update(narrators)
		.set({ cwd: join(getHome(), "stale-host-cwd") })
		.where(eq(narrators.id, id));
	expect((await ensureNarrator(id, "en")).cwd).toBe(getHome());
});

test("ensureNarrator rejects local non-root without a directory with a local 409, while history and row reads still work", async () => {
	const id = await seedNarrator();
	const checkHistory = await unchangedHistory(id);
	let error: unknown;
	try {
		await ensureNarrator(id, "en");
	} catch (caught) {
		error = caught;
	}
	expect(error).toBeInstanceOf(AppError);
	expect((error as AppError).statusCode).toBe(409);
	expect((error as AppError).code).toBe("WORKSPACE_DIRECTORY_REQUIRED");
	expect(isLoopRunning(id)).toBe(false);
	await checkHistory();
	const sibling = await seedNarrator({ committed: context("local", getHome()) });
	expect((await ensureNarrator(sibling, "en")).alive).toBe(true);
});
