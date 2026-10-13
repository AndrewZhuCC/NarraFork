/**
 * Session import/export routes and who may use them.
 *
 * Pinned here rather than trusted to review:
 *  - server session dirs (scan, import, export-to) are admin-only;
 *  - a device needs its manager AND use authorization; someone else's device is a
 *    404, indistinguishable from a missing one;
 *  - upload needs only a signed-in user, and the imported narrator is theirs;
 *  - export needs read access to the narrator, not write, and a stranger gets 404;
 *  - export-to authorizes the target on its own: read access to a narrator does
 *    not lend its owner's device, an admin may not use another user's private
 *    device, and a project device only takes that project's narrators;
 *  - an import path outside the CLI's layout is refused before any read.
 */
import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, posix } from "node:path";
import { eq } from "drizzle-orm";
import { Hono } from "hono";
import { db } from "../../db";
import { aclGrants, narrators, projects, remoteDevices, users } from "../../db/schema";
import type { ReadBytesResult } from "../../lib/agent/execution/backend";
import type { TransferStatResult } from "../../lib/agent/execution/rpc-types";
import { buildAppErrorResponse } from "../../lib/app-error-response";
import { generateId } from "../../lib/id";
import { narratorService } from "../../services/narrator-service";
import { importInterchangeSession } from "../../services/session-interchange/import";
import { setSessionDevicePortFactory } from "../../services/session-interchange/locations";
import type { DeviceSessionFsPort } from "../../services/session-interchange/session-fs";
import { narratorRoutes } from "../narrators";
import { sessionInterchangeRoutes as sessionRoutes } from "../session-interchange";

const TAG = Date.now().toString(36);
const OWNER = `si-owner-${TAG}`;
const OTHER = `si-other-${TAG}`;
const VIEWER = `si-viewer-${TAG}`;
const ADMIN = `si-admin-${TAG}`;
const SESSION = "33333333-3333-4333-8333-333333333333";

let root: string;
let narratorId: string;
let ownDeviceId: string;
let otherDeviceId: string;
const deviceFiles = new Map<string, Uint8Array>();

/** In-memory POSIX device; every device id resolves to the same store. */
function memoryPort(deviceId: string): DeviceSessionFsPort {
	const dirsOf = () => {
		const dirs = new Set<string>(["/"]);
		for (const path of deviceFiles.keys()) {
			for (let d = posix.dirname(path); d !== "/"; d = posix.dirname(d)) dirs.add(d);
		}
		return dirs;
	};
	return {
		deviceId,
		platformOs: "linux",
		defaultCwd: "/home/dev/work",
		maxRpcBytes: 1024 * 1024,
		async stat(path, opts): Promise<TransferStatResult> {
			const file = deviceFiles.get(path);
			if (file) return { exists: true, isDirectory: false, size: file.byteLength, mtimeMs: 1 };
			if (!dirsOf().has(path)) return { exists: false, isDirectory: false, size: 0, mtimeMs: 0 };
			return {
				exists: true,
				isDirectory: true,
				size: 0,
				mtimeMs: 1,
				entries: opts.recursive
					? [...deviceFiles.entries()]
							.filter(([p]) => p.startsWith(`${path}/`))
							.map(([p, b]) => ({
								relPath: p.slice(path.length + 1),
								size: b.byteLength,
								mtimeMs: 1,
								isDirectory: false,
							}))
					: undefined,
			};
		},
		async read(path, maxBytes): Promise<ReadBytesResult> {
			const file = deviceFiles.get(path);
			if (!file) throw new Error(`no such file: ${path}`);
			return {
				bytes: file.subarray(0, maxBytes),
				truncated: file.byteLength > maxBytes,
				totalSize: file.byteLength,
			};
		},
		async write(path, bytes) {
			deviceFiles.set(path, bytes);
		},
		async mkdirp() {},
		async remove(path) {
			deviceFiles.delete(path);
		},
	};
}

// Every device id resolves to the in-memory store; restored in afterAll.
setSessionDevicePortFactory(memoryPort);

function appAs(userId: string, role: "admin" | "user" = "user") {
	const app = new Hono();
	app.use("*", async (c, next) => {
		c.set("user", { sub: userId, role, iat: 0, exp: Number.MAX_SAFE_INTEGER });
		await next();
	});
	app.route("/api/session-interchange", sessionRoutes);
	app.route("/api/narrators", narratorRoutes);
	app.onError((err, c) => buildAppErrorResponse(err, c) ?? c.json({ error: String(err) }, 500));
	return app;
}

function call(
	userId: string,
	path: string,
	init: RequestInit & { json?: unknown } = {},
	role: "admin" | "user" = "user",
) {
	const { json, ...rest } = init;
	return appAs(userId, role).request(`http://localhost${path}`, {
		...rest,
		...(json !== undefined
			? {
					method: rest.method ?? "POST",
					headers: { "content-type": "application/json" },
					body: JSON.stringify(json),
				}
			: {}),
	});
}

function transcript(): string {
	const row = (
		uuid: string,
		parentUuid: string | null,
		type: "user" | "assistant",
		text: string,
	) => ({
		type,
		uuid,
		parentUuid,
		sessionId: SESSION,
		cwd: "/work/app",
		timestamp: "2026-01-01T00:00:00.000Z",
		message: { role: type, content: [{ type: "text", text }] },
	});
	return `${[row("u1", null, "user", "hello there"), row("a1", "u1", "assistant", "hi")]
		.map((r) => JSON.stringify(r))
		.join("\n")}\n`;
}

async function makeDevice(
	createdBy: string,
	ownerScope: "private" | "shared" = "private",
	projectId: string | null = null,
) {
	const now = new Date().toISOString();
	const id = generateId();
	await db.insert(remoteDevices).values({
		id,
		name: `Device ${id.slice(0, 6)}`,
		slug: `si-${id.slice(0, 10).toLowerCase()}`,
		tokenHash: "hash",
		tokenPrefix: "rdev_x",
		connectionMode: "reverse",
		createdBy,
		scope: projectId ? "project" : "global",
		projectId,
		ownerScope,
		createdAt: now,
		updatedAt: now,
	});
	return id;
}

beforeAll(async () => {
	root = await mkdtemp(join(tmpdir(), "nf-si-routes-"));
	await mkdir(join(root, "projects", "-work-app"), { recursive: true });
	await writeFile(join(root, "projects", "-work-app", `${SESSION}.jsonl`), transcript());
	await writeFile(join(root, "secret.jsonl"), transcript());

	const now = new Date().toISOString();
	for (const [id, role] of [
		[OWNER, "user"],
		[OTHER, "user"],
		[VIEWER, "user"],
		[ADMIN, "admin"],
	] as const) {
		await db.insert(users).values({ id, username: id, passwordHash: "x", role, createdAt: now });
	}

	const imported = await importInterchangeSession(
		{
			source: "claude-code",
			externalId: "fixture",
			title: "Fixture",
			cwd: root,
			createdAt: now,
			updatedAt: now,
			messages: [
				{ role: "user", timestamp: now, blocks: [{ type: "text", text: "export me" }] },
				{ role: "assistant", timestamp: now, blocks: [{ type: "text", text: "ok" }] },
			],
			subagents: [],
			warnings: [],
		},
		{ ownerUserId: OWNER, source: { format: "claude-code" } },
	);
	narratorId = imported.narratorId;
	await db.insert(aclGrants).values({
		id: generateId(),
		scopeType: "narrator",
		scopeId: narratorId,
		principalType: "user",
		principalId: VIEWER,
		capability: "read",
		grantedBy: OWNER,
		createdAt: now,
	});

	ownDeviceId = await makeDevice(OWNER);
	otherDeviceId = await makeDevice(OTHER);
	deviceFiles.set(
		`/home/dev/.claude/projects/-work-app/${SESSION}.jsonl`,
		new TextEncoder().encode(transcript()),
	);
});

afterAll(async () => {
	setSessionDevicePortFactory(null);
	await rm(root, { recursive: true, force: true });
});

const localScan = (r = root) =>
	`/api/session-interchange/scan?format=claude-code&target=local&root=${encodeURIComponent(r)}`;

describe("server session directories are admin-only", () => {
	test("a non-admin cannot scan, import from, or export to the server", async () => {
		expect((await call(OWNER, localScan())).status).toBe(403);
		expect(
			(
				await call(OWNER, "/api/session-interchange/import/path", {
					json: {
						format: "claude-code",
						location: { target: "local", root },
						path: join(root, "projects", "-work-app", `${SESSION}.jsonl`),
					},
				})
			).status,
		).toBe(403);
		expect(
			(
				await call(OWNER, `/api/narrators/${narratorId}/session-export-to`, {
					json: { format: "claude-code", location: { target: "local", root } },
				})
			).status,
		).toBe(403);
	});

	test("an admin scans and imports; the narrator belongs to the admin who imported it", async () => {
		const scan = await call(ADMIN, localScan(), {}, "admin");
		expect(scan.status).toBe(200);
		const page = (await scan.json()) as { candidates: Array<{ path: string; title?: string }> };
		expect(page.candidates).toHaveLength(1);
		expect(page.candidates[0]?.title).toBe("hello there");

		const res = await call(
			ADMIN,
			"/api/session-interchange/import/path",
			{
				json: {
					format: "claude-code",
					location: { target: "local", root },
					path: page.candidates[0]?.path,
					title: "From server",
				},
			},
			"admin",
		);
		expect(res.status).toBe(201);
		const body = (await res.json()) as { narratorId: string; messageCount: number };
		expect(body.messageCount).toBe(2);
		const narrator = await narratorService.getById(body.narratorId);
		expect(narrator).toMatchObject({ ownerUserId: ADMIN, title: "From server" });
		expect(narrator.traits).toContain("imported");
	});

	test("an import path outside the CLI's layout is refused, even for an admin", async () => {
		for (const path of [
			join(root, "secret.jsonl"),
			join(root, "projects", "..", "secret.jsonl"),
			"/etc/passwd",
		]) {
			const res = await call(
				ADMIN,
				"/api/session-interchange/import/path",
				{ json: { format: "claude-code", location: { target: "local", root }, path } },
				"admin",
			);
			expect(res.status).toBe(400);
		}
	});
});

describe("devices", () => {
	test("the device's manager scans and imports with the home inferred from its cwd", async () => {
		const scan = await call(
			OWNER,
			`/api/session-interchange/scan?format=claude-code&target=device&deviceId=${ownDeviceId}`,
		);
		expect(scan.status).toBe(200);
		const page = (await scan.json()) as { root: string; candidates: Array<{ path: string }> };
		expect(page.root).toBe("/home/dev/.claude");
		expect(page.candidates).toHaveLength(1);

		const res = await call(OWNER, "/api/session-interchange/import/path", {
			json: {
				format: "claude-code",
				location: { target: "device", deviceId: ownDeviceId },
				path: page.candidates[0]?.path,
			},
		});
		expect(res.status).toBe(201);
	});

	test("someone else's private device is a 404, like a missing one", async () => {
		for (const id of [otherDeviceId, "does-not-exist"]) {
			const res = await call(
				OWNER,
				`/api/session-interchange/scan?format=claude-code&target=device&deviceId=${id}`,
			);
			expect(res.status).toBe(404);
		}
	});

	test("export-to writes the session on the device and reports the missing cwd", async () => {
		const res = await call(OWNER, `/api/narrators/${narratorId}/session-export-to`, {
			json: { format: "claude-code", location: { target: "device", deviceId: ownDeviceId } },
		});
		expect(res.status).toBe(200);
		const body = (await res.json()) as {
			paths: string[];
			cwdExists: boolean;
			resumeCommand: string;
			warnings: Array<{ kind: string }>;
		};
		expect(body.paths).toHaveLength(1);
		expect(body.paths[0]).toStartWith("/home/dev/.claude/projects/");
		expect(deviceFiles.has(body.paths[0] ?? "")).toBe(true);
		// The narrator's cwd is a server temp dir, which the device does not have.
		expect(body.cwdExists).toBe(false);
		expect(body.warnings.map((w) => w.kind)).toContain("target_cwd_missing");
		expect(body.resumeCommand).toContain("claude --resume");
	});
});

describe("upload", () => {
	test("any signed-in user can upload; the format is detected", async () => {
		const form = new FormData();
		form.append("files", new File([transcript()], `${SESSION}.jsonl`));
		const res = await call(OTHER, "/api/session-interchange/import/upload", {
			method: "POST",
			body: form,
		});
		expect(res.status).toBe(201);
		const body = (await res.json()) as { narratorId: string };
		const [row] = await db.select().from(narrators).where(eq(narrators.id, body.narratorId));
		expect(row?.ownerUserId).toBe(OTHER);
	});

	test("an upload with no files is a 400", async () => {
		const res = await call(OTHER, "/api/session-interchange/import/upload", {
			method: "POST",
			body: new FormData(),
		});
		expect(res.status).toBe(400);
	});

	test("many small zstd files share one decoded budget instead of each getting the file cap", async () => {
		// ~65 MB of newline-free text per file compresses to a few KB. Without a shared
		// budget, 201 of these would expand to ~13 GB in memory.
		const { zstdCompressSync } = await import("node:zlib");
		const bomb = zstdCompressSync(Buffer.alloc(40 * 1024 * 1024, 0x61));
		const form = new FormData();
		for (let i = 0; i < 5; i++) form.append("files", new File([bomb], `bomb-${i}.jsonl.zst`));
		const res = await call(OTHER, "/api/session-interchange/import/upload", {
			method: "POST",
			body: form,
		});
		expect(res.status).toBe(413);
		expect(((await res.json()) as { code?: string }).code).toBe("SESSION_TOO_LARGE");
	});

	test("sidechain agentType comes from an uploaded .meta.json companion", async () => {
		const form = new FormData();
		const agentCall = {
			type: "assistant",
			uuid: "a1",
			parentUuid: "u1",
			sessionId: SESSION,
			timestamp: "2026-01-01T00:00:01.000Z",
			message: {
				role: "assistant",
				content: [{ type: "tool_use", id: "t-agent", name: "Agent", input: { prompt: "look" } }],
			},
		};
		const agentResult = {
			type: "user",
			uuid: "r1",
			parentUuid: "a1",
			sessionId: SESSION,
			timestamp: "2026-01-01T00:00:02.000Z",
			toolUseResult: { agentId: "a0f00" },
			message: {
				role: "user",
				content: [{ type: "tool_result", tool_use_id: "t-agent", content: "done" }],
			},
		};
		const main = `${transcript().split("\n")[0]}\n${JSON.stringify(agentCall)}\n${JSON.stringify(agentResult)}\n`;
		const side = `${JSON.stringify({
			type: "user",
			uuid: "s1",
			parentUuid: null,
			isSidechain: true,
			agentId: "a0f00",
			sessionId: SESSION,
			timestamp: "2026-01-01T00:00:01.100Z",
			message: { role: "user", content: "look" },
		})}\n`;
		form.append("files", new File([main], `${SESSION}.jsonl`));
		form.append("files", new File([side], "agent-a0f00.jsonl"));
		form.append(
			"files",
			new File([JSON.stringify({ agentType: "Explore" })], "agent-a0f00.meta.json"),
		);
		const res = await call(OTHER, "/api/session-interchange/import/upload", {
			method: "POST",
			body: form,
		});
		expect(res.status).toBe(201);
		const body = (await res.json()) as { subagentIds: string[] };
		expect(body.subagentIds).toHaveLength(1);
		const [sub] = await db
			.select({ subagentType: narrators.subagentType })
			.from(narrators)
			.where(eq(narrators.id, body.subagentIds[0] ?? ""));
		expect(sub?.subagentType).toBe("explore");
	});
});

describe("backend guard", () => {
	test("the import router refuses the PostgreSQL backend before reading a body", async () => {
		const { requireSqliteSessionInterchange } = await import("../session-interchange");
		expect(() => requireSqliteSessionInterchange("postgres")).toThrow(
			expect.objectContaining({ statusCode: 503, code: "POSTGRES_UNSUPPORTED" }),
		);
		expect(() => requireSqliteSessionInterchange("sqlite")).not.toThrow();
	});
});

describe("export needs read access to the narrator", () => {
	test("a read-granted user can download; a stranger gets 404", async () => {
		const ok = await call(VIEWER, `/api/narrators/${narratorId}/session-export?format=codex`);
		expect(ok.status).toBe(200);
		expect(ok.headers.get("content-disposition")).toContain("rollout-");
		expect(decodeURIComponent(ok.headers.get("x-resume-command") ?? "")).toContain("codex resume");
		// Tells the UI where the downloaded rollout goes under ~/.codex.
		expect(decodeURIComponent(ok.headers.get("x-session-path") ?? "")).toMatch(
			/^sessions\/\d{4}\/\d{2}\/\d{2}\/rollout-.+\.jsonl$/,
		);
		const first = (await ok.text()).split("\n")[0] ?? "";
		expect(JSON.parse(first).type).toBe("session_meta");

		const denied = await call(OTHER, `/api/narrators/${narratorId}/session-export?format=codex`);
		expect(denied.status).toBe(404);
	});

	test("a read-granted admin exports to the server and the files land under the root", async () => {
		const target = join(root, "export-target");
		const res = await call(
			ADMIN,
			`/api/narrators/${narratorId}/session-export-to`,
			{ json: { format: "claude-code", location: { target: "local", root: target } } },
			"admin",
		);
		expect(res.status).toBe(200);
		const body = (await res.json()) as { paths: string[]; cwdExists: boolean };
		expect(body.cwdExists).toBe(true);
		expect(body.paths[0]).toStartWith(join(target, "projects"));
		expect((await readFile(body.paths[0] ?? "", "utf8")).length).toBeGreaterThan(0);
	});

	test("a malformed provider id is rejected before anything is read", async () => {
		const res = await call(
			VIEWER,
			`/api/narrators/${narratorId}/session-export?format=codex&codexModelProvider=${encodeURIComponent("a b")}`,
		);
		expect(res.status).toBe(400);
	});
});

describe("export-to authorizes the target separately from the narrator", () => {
	const exportTo = (userId: string, deviceId: string, id = narratorId, role?: "admin") =>
		call(
			userId,
			`/api/narrators/${id}/session-export-to`,
			{ json: { format: "claude-code", location: { target: "device", deviceId } } },
			role,
		);

	test("read access is enough to write to your own device, not to the owner's", async () => {
		const viewerDevice = await makeDevice(VIEWER);
		expect((await exportTo(VIEWER, viewerDevice)).status).toBe(200);
		// Reading someone's narrator does not lend you their device.
		expect((await exportTo(VIEWER, ownDeviceId)).status).toBe(404);
	});

	test("a stranger gets 404 before the device is even looked at", async () => {
		const strangerDevice = await makeDevice(OTHER);
		expect((await exportTo(OTHER, strangerDevice)).status).toBe(404);
		expect((await exportTo(OTHER, otherDeviceId)).status).toBe(404);
	});

	test("an admin manages every device but may not use another user's private one", async () => {
		// Admins can see the device (so it is 403, not 404), but the owner axis still binds.
		expect((await exportTo(ADMIN, ownDeviceId, narratorId, "admin")).status).toBe(403);
		const scan = await call(
			ADMIN,
			`/api/session-interchange/scan?format=claude-code&target=device&deviceId=${ownDeviceId}`,
			{},
			"admin",
		);
		expect(scan.status).toBe(403);
		const shared = await makeDevice(OWNER, "shared");
		expect((await exportTo(ADMIN, shared, narratorId, "admin")).status).toBe(200);
	});

	test("a project device takes exports only from that project's narrators", async () => {
		const now = new Date().toISOString();
		const projectA = `si-pa-${TAG}`;
		const projectB = `si-pb-${TAG}`;
		for (const id of [projectA, projectB]) {
			await db.insert(projects).values({ id, name: id, createdAt: now, updatedAt: now });
		}
		const deviceA = await makeDevice(OWNER, "private", projectA);
		const deviceB = await makeDevice(OWNER, "private", projectB);

		const inProject = await importInterchangeSession(
			{
				source: "claude-code",
				externalId: "project-fixture",
				title: "Project fixture",
				cwd: root,
				createdAt: now,
				updatedAt: now,
				messages: [
					{ role: "user", timestamp: now, blocks: [{ type: "text", text: "hi" }] },
					{ role: "assistant", timestamp: now, blocks: [{ type: "text", text: "ok" }] },
				],
				subagents: [],
				warnings: [],
			},
			{ ownerUserId: OWNER, source: { format: "claude-code" } },
		);
		await db
			.update(narrators)
			.set({ contextProjectId: projectA })
			.where(eq(narrators.id, inProject.narratorId));

		expect((await exportTo(OWNER, deviceA, inProject.narratorId)).status).toBe(200);
		expect((await exportTo(OWNER, deviceB, inProject.narratorId)).status).toBe(403);
		// A standalone narrator belongs to no project, so no project device qualifies.
		expect((await exportTo(OWNER, deviceA)).status).toBe(403);
		// Import creates standalone narrators: project devices are not import sources.
		const scan = await call(
			OWNER,
			`/api/session-interchange/scan?format=claude-code&target=device&deviceId=${deviceA}`,
		);
		expect(scan.status).toBe(403);
	});
});
