import { afterEach, beforeAll, beforeEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { eq } from "drizzle-orm";
import { db, sqlite } from "../db";
import { chapters, narrators, narratorWorktreeResources, projects, users } from "../db/schema";
import { localBackend } from "../lib/agent/execution/registry";
import { generateId } from "../lib/id";
import { getNarraforkPath } from "../lib/narrafork-home";
import { safeSpawn } from "../lib/spawn";
import { gitWorkspaceIdentity } from "./git-workspace";
import { FileWorktreeJournal, worktreeProposalHash } from "./narrator-worktree-journal";
import { narratorWorktreeResourceRegistry } from "./narrator-worktree-resources";
import { narratorWorktreeService } from "./narrator-worktree-runtime";
import { NarratorWorktreeService, type WorktreeTarget } from "./narrator-worktree-service";
import { maintainProtectionIndex } from "./resource-protection-index";
import { cleanupOrphanedWorktrees } from "./storage-service";
import { withWorkspaceRepositoryLock, workspaceContextService } from "./workspace-context-service";
import { inspect } from "./worktree-lifecycle-guard";

// Isolated preload DB only. This suite does not generate migrations or touch live data.
// Until the main agent generates the new migration, materialize just this table in the
// ephemeral test DB; the FK is real and tested by deleting an actual owner row.
beforeAll(() => {
	if (process.env.NARRAFORK_TEST !== "1") throw new Error("Ephemeral DB required");
	sqlite.exec(`CREATE TABLE IF NOT EXISTS narrator_worktree_resources (
		id TEXT PRIMARY KEY, owner_narrator_id TEXT REFERENCES narrators(id) ON DELETE SET NULL,
		device_id TEXT NOT NULL, repository_key TEXT NOT NULL, worktree_path TEXT NOT NULL,
		state TEXT NOT NULL, create_request_id TEXT NOT NULL,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL
	); CREATE UNIQUE INDEX IF NOT EXISTS uq_narrator_worktree_resource_path
	ON narrator_worktree_resources(device_id, worktree_path)`);
});

let temporary: string;
let source: string;
let owner: string;
let narratorId: string;
let projectId: string;
let target: WorktreeTarget;
let service: NarratorWorktreeService<string>;
const binary = Buffer.from([0, 255, 128, 13, 10, 0, 42]);
const signal = () => new AbortController().signal;
async function removeReceipts() {
	await rm(getNarraforkPath("worktree-requests"), { recursive: true, force: true });
	await rm(getNarraforkPath("worktree-requests-v2"), { recursive: true, force: true });
}
function beginLegacyReceiptImport() {
	// Test-only upgrade boundary: files are injected before this one-time import starts.
	sqlite.exec(`UPDATE resource_protection_sources SET complete = 0, cursor = NULL
		WHERE source = 'receipt' AND source_id = ''`);
}
async function finishProtectionMaintenance() {
	for (let batch = 0; batch < 96; batch++) {
		await maintainProtectionIndex(performance.now() + 5000);
		const pending = sqlite
			.query<{ pending: number }, []>(`SELECT EXISTS(
				SELECT 1 FROM resource_protection_sources
				WHERE (source_id = '' AND complete = 0) OR (source_id != '' AND dirty = 1)
			) AS pending`)
			.get()?.pending;
		if (!pending) return;
	}
	throw new Error("Protection fixture maintenance did not complete");
}
async function git(args: string[], cwd = source) {
	const result = await safeSpawn({
		cmd: ["git", "-C", cwd, ...args],
		timeout: 5000,
		maxOutputBytes: 128 * 1024,
	});
	if (result.exitCode !== 0) throw new Error(result.stderr);
	return result.stdout.trim();
}
function proposal(leaf: string, branch = `chapter/${leaf}`) {
	return {
		expectedRevision: 0,
		workspaceKey: target.workspace.workspaceKey ?? "",
		requestId: leaf,
		destinationPath: join(source, ".worktrees", leaf),
		branch: { kind: "new" as const, name: branch },
	};
}
function registration(leaf: string) {
	return {
		ownerNarratorId: narratorId,
		deviceId: "local",
		repositoryKey: target.workspace.repositoryKey ?? "",
		worktreePath: join(source, ".worktrees", leaf),
		createRequestId: leaf,
	};
}
async function add(leaf: string, branch = `chapter/${leaf}`) {
	await git(["worktree", "add", "-b", branch, "--", join(source, ".worktrees", leaf), "HEAD"]);
	return join(source, ".worktrees", leaf);
}
async function dirty(path: string) {
	await writeFile(join(path, "tracked.txt"), "dirty user code\n");
	await writeFile(join(path, "marker.bin"), binary);
}
async function assertDirty(path: string) {
	expect(await readFile(join(path, "tracked.txt"), "utf8")).toBe("dirty user code\n");
	expect(await readFile(join(path, "marker.bin"))).toEqual(binary);
}

beforeEach(async () => {
	await removeReceipts();
	sqlite.exec(`DELETE FROM resource_protection_paths WHERE source = 'receipt';
		DELETE FROM resource_protection_sources WHERE source = 'receipt' AND source_id != ''`);
	beginLegacyReceiptImport();
	temporary = await mkdtemp(join(tmpdir(), "narrafork-worktree-storage-"));
	source = join(temporary, "repo");
	await mkdir(source);
	await git(["init", "-b", "main"]);
	await writeFile(join(source, "tracked.txt"), "initial\n");
	await git(["add", "tracked.txt"]);
	await git([
		"-c",
		"user.name=Fixture",
		"-c",
		"user.email=fixture@example.test",
		"commit",
		"-m",
		"initial",
	]);
	await mkdir(join(source, ".worktrees"));
	owner = generateId();
	narratorId = generateId();
	projectId = generateId();
	const now = new Date().toISOString();
	await db
		.insert(users)
		.values({ id: owner, username: owner, passwordHash: "fixture", createdAt: now });
	await db.insert(projects).values({
		id: projectId,
		name: "cleanup fixture",
		gitPath: source,
		ownerUserId: owner,
		createdAt: now,
		updatedAt: now,
	});
	await db.insert(narrators).values({
		id: narratorId,
		title: "cleanup fixture",
		cwd: source,
		contextProjectId: projectId,
		ownerUserId: owner,
		createdAt: now,
		updatedAt: now,
	});
	target = {
		workspace: {
			deviceId: "local",
			cwd: source,
			rootPath: source,
			state: "ready",
			workspaceKey: gitWorkspaceIdentity(localBackend, source),
			repositoryKey: gitWorkspaceIdentity(localBackend, join(source, ".git")),
			capabilities: { read: true, write: true },
		},
		backend: localBackend,
		repositoryPath: join(source, ".git"),
	};
	service = new NarratorWorktreeService({
		authorize: async () => target,
		withRevision: async (_id, _revision, action) => action(),
		withRepositoryLock: withWorkspaceRepositoryLock,
		journal: new FileWorktreeJournal(
			getNarraforkPath("worktree-requests-v2"),
			getNarraforkPath("worktree-requests"),
		),
		resources: narratorWorktreeResourceRegistry,
	});
	await finishProtectionMaintenance();
});

afterEach(async () => {
	await db.delete(chapters).where(eq(chapters.projectId, projectId));
	await db.delete(narrators).where(eq(narrators.contextProjectId, projectId));
	await db
		.delete(narratorWorktreeResources)
		.where(eq(narratorWorktreeResources.repositoryKey, target.workspace.repositoryKey ?? ""));
	await db.delete(projects).where(eq(projects.id, projectId));
	await db.delete(users).where(eq(users.id, owner));
	await removeReceipts();
	await rm(temporary, { recursive: true, force: true });
});

describe("production worktree storage cleanup", () => {
	test("actual runtime create before switch survives cleanup, restart and owner deletion", async () => {
		const context = await workspaceContextService.get(narratorId);
		const prepared = await narratorWorktreeService.prepare(
			{ userId: owner, isAdmin: false },
			narratorId,
			{
				expectedRevision: context.revision,
				workspaceKey: context.git?.workspaceKey,
				branchName: "修复登录",
			},
			signal(),
		);
		const request = {
			...proposal("runtime-created"),
			workspaceKey: context.git?.workspaceKey ?? "",
			destinationPath: prepared.destinationPath,
			branch: { kind: "new" as const, name: prepared.branchName },
		};
		const created = await narratorWorktreeService.create(
			{ userId: owner, isAdmin: false },
			narratorId,
			request,
			signal(),
		);
		expect(created.outcome).toBe("created");
		await dirty(prepared.destinationPath);
		expect(
			db.select({ cwd: narrators.cwd }).from(narrators).where(eq(narrators.id, narratorId)).get()
				?.cwd,
		).toBe(source);
		expect(
			db
				.select({ state: narratorWorktreeResources.state })
				.from(narratorWorktreeResources)
				.where(eq(narratorWorktreeResources.worktreePath, prepared.destinationPath))
				.get()?.state,
		).toBe("ready");
		await cleanupOrphanedWorktrees();
		await assertDirty(prepared.destinationPath);
		// Remove receipt evidence, rebuild journal/service objects, then delete owner.
		// Remaining durable DB ownership alone must protect after restart/owner removal.
		await removeReceipts();
		service = new NarratorWorktreeService({
			authorize: async () => target,
			withRevision: async (_id, _revision, action) => action(),
			withRepositoryLock: withWorkspaceRepositoryLock,
			journal: new FileWorktreeJournal(
				getNarraforkPath("worktree-requests-v2"),
				getNarraforkPath("worktree-requests"),
			),
			resources: narratorWorktreeResourceRegistry,
		});
		await db.delete(narrators).where(eq(narrators.id, narratorId));
		expect(
			db
				.select({ owner: narratorWorktreeResources.ownerNarratorId })
				.from(narratorWorktreeResources)
				.where(eq(narratorWorktreeResources.worktreePath, prepared.destinationPath))
				.get()?.owner,
		).toBeNull();
		// A fresh OS process has no parent service objects, cwd sets or live registry cache.
		const restarted = await safeSpawn({
			cmd: [
				"bun",
				"--eval",
				'const { cleanupOrphanedWorktrees } = await import("./server/services/storage-service"); console.log(JSON.stringify(await cleanupOrphanedWorktrees())); process.exit(0);',
			],
			env: { ...process.env, NARRAFORK_ALLOW_MULTIPLE: "1" },
			timeout: 15_000,
			maxOutputBytes: 64 * 1024,
		});
		expect(restarted.exitCode).toBe(0);
		expect(restarted.stdout).toContain('"removed":0');
		expect(
			(
				await service.list(
					"actor",
					narratorId,
					{ workspaceKey: target.workspace.workspaceKey },
					signal(),
				)
			).entries.some((entry) => entry.path === prepared.destinationPath),
		).toBe(true);
		await cleanupOrphanedWorktrees();
		await assertDirty(prepared.destinationPath);
	});

	test("an actually dispatched unknown result and failed state save remain protected after receipt loss", async () => {
		for (const [leaf, failStateSave] of [
			["unknown-Ab1234", false],
			["unfinished-Cd1234", true],
		] as const) {
			let wrote = false;
			const interrupted = new NarratorWorktreeService({
				authorize: async () => target,
				withRevision: async (_id, _revision, action) => action(),
				withRepositoryLock: withWorkspaceRepositoryLock,
				journal: new FileWorktreeJournal(
					getNarraforkPath("worktree-requests-v2"),
					getNarraforkPath("worktree-requests"),
				),
				resources: {
					register: narratorWorktreeResourceRegistry.register,
					setState: async (...args) => {
						if (failStateSave) throw new Error("State write lost");
						await narratorWorktreeResourceRegistry.setState(...args);
					},
				},
				runGit: async (workspace, args, abort, writing) => {
					if (wrote) throw new Error("Verification unavailable after checkout");
					const result = await safeSpawn({
						cmd: ["git", "-C", workspace.workspace.rootPath ?? "", ...args],
						timeout: 5000,
						maxOutputBytes: 128 * 1024,
						signal: abort,
					});
					if (writing) wrote = true;
					return result;
				},
			});
			expect(
				(await interrupted.create("actor", narratorId, proposal(leaf), signal())).outcome,
			).toBe("unknown");
			const row = db
				.select({ state: narratorWorktreeResources.state })
				.from(narratorWorktreeResources)
				.where(eq(narratorWorktreeResources.worktreePath, registration(leaf).worktreePath))
				.get();
			expect(row?.state).toBe(failStateSave ? "preparing" : "unknown");
		}
		await removeReceipts();
		// Both worktrees are clean: protection here cannot be credited to a dirty-check.
		expect((await cleanupOrphanedWorktrees()).removed).toBe(0);
		for (const leaf of ["unknown-Ab1234", "unfinished-Cd1234"]) {
			const path = registration(leaf).worktreePath;
			await dirty(path);
			await cleanupOrphanedWorktrees();
			await assertDirty(path);
		}
	});

	test("every resource state protects even a clean chapter-shaped directory, plus dirty binary markers", async () => {
		const paths: string[] = [];
		for (const [leaf, state] of [
			["ready-Ab1234", "ready"],
			["unknown-Cd1234", "unknown"],
			["preparing-Ef1234", "preparing"],
		] as const) {
			paths.push(await add(leaf));
			await narratorWorktreeResourceRegistry.register(registration(leaf));
			if (state !== "preparing")
				await narratorWorktreeResourceRegistry.setState(registration(leaf), state);
		}
		// A clean positively-identifiable orphan would otherwise be eligible for deletion.
		expect(await cleanupOrphanedWorktrees()).toMatchObject({ removed: 0 });
		for (const path of paths) await dirty(path);
		await db.delete(narrators).where(eq(narrators.id, narratorId));
		await cleanupOrphanedWorktrees();
		for (const path of paths) await assertDirty(path);
	});

	test("legacy durable journal and cwd/context protect old resources without registry after restart", async () => {
		const receiptLeaf = "legacy-Ab1234";
		const receiptPath = await add(receiptLeaf);
		const request = proposal(receiptLeaf);
		const journal = new FileWorktreeJournal(getNarraforkPath("worktree-requests"));
		await journal.claim(narratorId, request.requestId, {
			request,
			destination: receiptPath,
			proposalHash: worktreeProposalHash(request),
			repositoryKey: target.workspace.repositoryKey ?? "",
			expectedHead: await git(["rev-parse", "HEAD"]),
			result: {
				outcome: "unknown",
				worktree: null,
				residuals: { destinationExists: true, branchExists: true },
			},
		});
		expect(
			(
				await new FileWorktreeJournal(getNarraforkPath("worktree-requests")).read(
					narratorId,
					request.requestId,
				)
			)?.destination,
		).toBe(receiptPath);
		const cwdPath = await add("cwd-Cd1234");
		await mkdir(join(cwdPath, "nested"));
		await db
			.update(narrators)
			.set({ cwd: join(cwdPath, "nested") })
			.where(eq(narrators.id, narratorId));
		const contextPath = await add("context-Ef1234");
		const context = await workspaceContextService.get(narratorId);
		await db
			.update(narrators)
			.set({ workspaceContext: { ...context, cwd: contextPath } })
			.where(eq(narrators.id, narratorId));
		beginLegacyReceiptImport();
		await finishProtectionMaintenance();
		await cleanupOrphanedWorktrees();
		for (const path of [receiptPath, cwdPath, contextPath])
			expect(await Bun.file(join(path, "tracked.txt")).exists()).toBe(true);
		for (const path of [cwdPath, contextPath]) {
			const resource = db
				.select({
					createRequestId: narratorWorktreeResources.createRequestId,
					ownerNarratorId: narratorWorktreeResources.ownerNarratorId,
					state: narratorWorktreeResources.state,
				})
				.from(narratorWorktreeResources)
				.where(eq(narratorWorktreeResources.worktreePath, path))
				.get();
			// Cleanup only observes protection evidence; it never invents durable ownership.
			expect(resource).toBeUndefined();
		}
		// Without a durable receipt/inventory, deleted cwd owners leave no ownership
		// evidence. Their actual dirty bytes still disqualify them from orphan cleanup.
		for (const path of [cwdPath, contextPath]) await dirty(path);
		await db.delete(narrators).where(eq(narrators.id, narratorId));
		await cleanupOrphanedWorktrees();
		for (const path of [cwdPath, contextPath]) await assertDirty(path);
		// The migrated pin is frozen: corrupting and then deleting its old file cannot release it.
		const receiptFile = getNarraforkPath(
			"worktree-requests",
			`${createHash("sha256")
				.update(JSON.stringify([narratorId, request.requestId]))
				.digest("hex")}.json`,
		);
		await writeFile(receiptFile, "{");
		await finishProtectionMaintenance();
		expect(await inspect([{ path: receiptPath }], "frozen migrated receipt fixture")).toMatchObject(
			{
				complete: true,
				status: "protected",
			},
		);
		expect((await cleanupOrphanedWorktrees()).removed).toBe(0);
		await removeReceipts();
		const restarted = await safeSpawn({
			cmd: [
				"bun",
				"--eval",
				'const { cleanupOrphanedWorktrees } = await import("./server/services/storage-service"); console.log(JSON.stringify(await cleanupOrphanedWorktrees())); process.exit(0);',
			],
			env: { ...process.env, NARRAFORK_ALLOW_MULTIPLE: "1" },
			timeout: 15_000,
			maxOutputBytes: 64 * 1024,
		});
		expect(restarted.exitCode).toBe(0);
		expect(restarted.stdout).toContain('"removed":0');
		// Receipt protection remains independently durable even for this CLEAN worktree.
		expect(await readFile(join(receiptPath, "tracked.txt"), "utf8")).toBe("initial\n");
	});

	test("ordinary journal publication after migration does not invent a clean orphan owner", async () => {
		const path = await add("journal-Ab1234");
		const request = proposal("journal-Ab1234");
		await new FileWorktreeJournal(getNarraforkPath("worktree-requests")).claim(
			narratorId,
			request.requestId,
			{
				request,
				destination: path,
				proposalHash: worktreeProposalHash(request),
				repositoryKey: target.workspace.repositoryKey ?? "",
				expectedHead: await git(["rev-parse", "HEAD"]),
			},
		);
		expect(await inspect([{ path }], "ordinary journal is not an owner fixture")).toMatchObject({
			complete: true,
			status: "clear",
		});
		expect((await cleanupOrphanedWorktrees()).removed).toBe(1);
		expect(await Bun.file(join(path, "tracked.txt")).exists()).toBe(false);
	});

	test("create/cleanup repository race protects the window before inventory and interrupted preparing registration", async () => {
		const leaf = "race-Ab1234";
		const removable = await add("otherwise-Ef1234");
		let enter!: () => void;
		let release!: () => void;
		const entered = new Promise<void>((resolve) => {
			enter = resolve;
		});
		const released = new Promise<void>((resolve) => {
			release = resolve;
		});
		let writes = 0;
		const racing = new NarratorWorktreeService({
			authorize: async () => target,
			withRevision: async (_id, _revision, action) => action(),
			withRepositoryLock: withWorkspaceRepositoryLock,
			journal: new FileWorktreeJournal(
				getNarraforkPath("worktree-requests-v2"),
				getNarraforkPath("worktree-requests"),
			),
			resources: {
				register: async (resource) => {
					enter();
					await released;
					await narratorWorktreeResourceRegistry.register(resource);
				},
				setState: narratorWorktreeResourceRegistry.setState,
			},
			runGit: async (workspace, args, abort, writing) => {
				if (writing) {
					writes++;
					await git(args);
					enter();
					await released;
					throw new Error("Process response lost");
				}
				return safeSpawn({
					cmd: ["git", "-C", workspace.workspace.rootPath ?? "", ...args],
					timeout: 5000,
					maxOutputBytes: 128 * 1024,
					signal: abort,
				});
			},
		});
		const creation = racing.create("actor", narratorId, proposal(leaf), signal());
		await entered;
		const result = await cleanupOrphanedWorktrees();
		expect(result.removed).toBe(0);
		expect(writes).toBe(0);
		expect(await Bun.file(join(removable, "tracked.txt")).exists()).toBe(true);
		expect(
			db
				.select({ id: narratorWorktreeResources.id })
				.from(narratorWorktreeResources)
				.where(eq(narratorWorktreeResources.worktreePath, registration(leaf).worktreePath))
				.get(),
		).toBeUndefined();
		release();
		expect((await creation).outcome).toBe("created");
		expect(writes).toBe(1);
		await dirty(join(source, ".worktrees", leaf));
		await cleanupOrphanedWorktrees();
		await assertDirty(join(source, ".worktrees", leaf));
	});

	test("only a verified clean old chapter orphan is removed; dirty, ignored, active and unknown paths survive", async () => {
		const clean = await add("clean-Ab1234");
		const dirtyPath = await add("dirty-Cd1234");
		await dirty(dirtyPath);
		const ignored = await add("ignored-Ef1234");
		await writeFile(join(ignored, ".gitignore"), "secret.bin\n");
		await git(["add", ".gitignore"], ignored);
		await git(
			[
				"-c",
				"user.name=Fixture",
				"-c",
				"user.email=fixture@example.test",
				"commit",
				"-m",
				"ignore secret",
			],
			ignored,
		);
		await writeFile(join(ignored, "secret.bin"), binary);
		const active = await add("active-Gh1234");
		await db.insert(chapters).values({
			id: generateId(),
			projectId,
			title: "active",
			branch: "chapter/active-Gh1234",
			baseBranch: "main",
			worktreePath: active,
			createdAt: new Date().toISOString(),
			updatedAt: new Date().toISOString(),
		});
		const unknown = await add("custom-Ij1234", "ordinary-user-branch");
		const random = join(source, ".worktrees", "unregistered-Kl1234");
		await mkdir(random);
		await writeFile(join(random, "marker.bin"), binary);
		const result = await cleanupOrphanedWorktrees();
		expect(result.removed).toBe(1);
		expect(await Bun.file(join(clean, "tracked.txt")).exists()).toBe(false);
		await assertDirty(dirtyPath);
		expect(await readFile(join(ignored, "secret.bin"))).toEqual(binary);
		for (const path of [active, unknown])
			expect(await Bun.file(join(path, "tracked.txt")).exists()).toBe(true);
		expect(await readFile(join(random, "marker.bin"))).toEqual(binary);
	});

	test("corrupt, truncated or oversized legacy evidence fails closed rather than deleting a clean candidate", async () => {
		const path = await add("failclosed-Ab1234");
		const directory = getNarraforkPath("worktree-requests");
		await mkdir(directory);
		const receipt = join(directory, `${"a".repeat(64)}.json`);
		beginLegacyReceiptImport();
		for (const text of ["{", " ".repeat(32 * 1024 + 1)]) {
			await writeFile(receipt, text);
			await maintainProtectionIndex(performance.now() + 5000);
			await maintainProtectionIndex(performance.now() + 5000);
			expect((await inspect([{ path }], "legacy invalid evidence fixture")).complete).toBe(false);
			expect((await cleanupOrphanedWorktrees()).removed).toBe(0);
			expect(await Bun.file(join(path, "tracked.txt")).exists()).toBe(true);
		}
	});

	test("paged legacy receipt import refuses deletion until complete and preserves the last owner", async () => {
		const path = await add("late-Ab1234");
		const directory = getNarraforkPath("worktree-requests");
		await mkdir(directory);
		for (let index = 0; index < 513; index++)
			await writeFile(
				join(directory, `${index.toString(16).padStart(64, "0")}.json`),
				JSON.stringify({
					destination: index === 512 ? path : join(source, `other-${index}`),
					deviceId: "local",
					repositoryKey: target.workspace.repositoryKey,
				}),
			);
		beginLegacyReceiptImport();
		expect((await inspect([{ path }], "legacy pending import fixture")).complete).toBe(false);
		expect((await cleanupOrphanedWorktrees()).removed).toBe(0);
		expect(await Bun.file(join(path, "tracked.txt")).exists()).toBe(true);
		await finishProtectionMaintenance();
		expect(await inspect([{ path }], "legacy imported owner fixture")).toMatchObject({
			complete: true,
			status: "protected",
		});
		expect((await cleanupOrphanedWorktrees()).removed).toBe(0);
		expect(await readFile(join(path, "tracked.txt"), "utf8")).toBe("initial\n");
	});

	test("paged legacy cwd maintenance never makes a clean owned directory appear orphaned", async () => {
		const path = await add("manyowners-Ab1234");
		const now = new Date().toISOString();
		// Keep fixture inserts under SQLite's bind budget, just like production pages.
		for (let batch = 0; batch < 64; batch++) {
			await db.insert(narrators).values(
				Array.from({ length: 32 }, (_value, offset) => ({
					id: `legacy-${(batch * 32 + offset).toString().padStart(5, "0")}`,
					title: "bounded legacy fixture",
					contextProjectId: projectId,
					cwd: path,
					createdAt: now,
					updatedAt: now,
				})),
			);
		}
		// Model a historical DB before its first bounded narrator bootstrap page.
		sqlite.exec(`UPDATE resource_protection_sources SET complete = 0, cursor = NULL
			WHERE source = 'narrator' AND source_id = ''`);
		expect((await inspect([{ path }], "legacy cwd pending maintenance fixture")).complete).toBe(
			false,
		);
		expect((await cleanupOrphanedWorktrees()).removed).toBe(0);
		expect(await Bun.file(join(path, "tracked.txt")).exists()).toBe(true);
		await finishProtectionMaintenance();
		expect(await inspect([{ path }], "legacy cwd maintained owner fixture")).toMatchObject({
			complete: true,
			status: "protected",
		});
		expect((await cleanupOrphanedWorktrees()).removed).toBe(0);
		expect(await readFile(join(path, "tracked.txt"), "utf8")).toBe("initial\n");
	});

	test("a symlink worktrees parent cannot redirect cleanup into another repository", async () => {
		const outside = join(temporary, "outside");
		await mkdir(outside);
		await writeFile(join(outside, "marker.bin"), binary);
		await rm(join(source, ".worktrees"), { recursive: true });
		await symlink(outside, join(source, ".worktrees"));
		expect((await cleanupOrphanedWorktrees()).removed).toBe(0);
		expect(await readFile(join(outside, "marker.bin"))).toEqual(binary);
	});
});
