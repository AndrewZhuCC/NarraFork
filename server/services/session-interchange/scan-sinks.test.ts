/**
 * Scan, gather and write session files on a real temp directory (local) and on an
 * in-memory device port (remote), without a database.
 */
import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdir, mkdtemp, readFile, rm, stat, utimes, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, posix } from "node:path";
import { zstdCompressSync } from "node:zlib";
import { parseClaudeCodeSession } from "@shared/session-interchange/claude-code/parse";
import type { ReadBytesResult } from "../../lib/agent/execution/backend";
import type { TransferStatResult } from "../../lib/agent/execution/rpc-types";
import type { SessionExportResult } from "./export";
import {
	DecodeBudget,
	decodeSessionBytes,
	SESSION_LINE_MAX_BYTES,
	SessionTooLargeError,
} from "./jsonl";
import { collectSessionFiles, resolveSessionFile, scanSessions } from "./scan";
import {
	createDeviceSessionFs,
	createLocalSessionFs,
	type DeviceSessionFsPort,
	FileExistsError,
	inferRemoteHome,
} from "./session-fs";
import { buildSessionDownload, writeSessionExport } from "./sinks";

const SESSION_A = "11111111-1111-4111-8111-111111111111";
const SESSION_B = "22222222-2222-4222-8222-222222222222";
const THREAD = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b";
const CHILD = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c";
const OTHER = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5d";

let root: string;
const local = createLocalSessionFs();

function jsonl(rows: unknown[]): string {
	return `${rows.map((row) => JSON.stringify(row)).join("\n")}\n`;
}

function ccRow(uuid: string, parentUuid: string | null, type: "user" | "assistant", text: string) {
	return {
		type,
		uuid,
		parentUuid,
		sessionId: SESSION_A,
		cwd: "/work/app",
		timestamp: "2026-01-01T00:00:00.000Z",
		message: { role: type, content: [{ type: "text", text }] },
	};
}

async function put(path: string, content: string | Uint8Array, mtimeSec?: number) {
	await mkdir(join(path, ".."), { recursive: true });
	await writeFile(path, content);
	if (mtimeSec !== undefined) await utimes(path, mtimeSec, mtimeSec);
}

beforeAll(async () => {
	root = await mkdtemp(join(tmpdir(), "nf-session-scan-"));
	const cc = join(root, "claude");
	const project = join(cc, "projects", "-work-app");
	await put(
		join(project, `${SESSION_A}.jsonl`),
		jsonl([
			ccRow("u1", null, "user", "fix the login bug"),
			ccRow("a1", "u1", "assistant", "done"),
			{ type: "custom-title", customTitle: "Login fix", sessionId: SESSION_A },
		]),
		2_000,
	);
	await put(
		join(project, SESSION_A, "subagents", "agent-a0123.jsonl"),
		jsonl([{ ...ccRow("s1", null, "user", "look"), isSidechain: true, agentId: "a0123" }]),
	);
	await put(
		join(project, SESSION_A, "subagents", "agent-a0123.meta.json"),
		JSON.stringify({ agentType: "Explore" }),
	);
	await put(
		join(project, `${SESSION_B}.jsonl`),
		jsonl([ccRow("u1", null, "user", "older session")]),
		1_000,
	);
	// Not session transcripts: ignored by scan.
	await put(join(project, "notes.jsonl"), "{}\n");
	await put(join(project, "empty.jsonl"), "");

	const codex = join(root, "codex");
	const day = join(codex, "sessions", "2026", "01", "01");
	const meta = (id: string, extra: Record<string, unknown> = {}) => ({
		type: "session_meta",
		timestamp: "2026-01-01T00:00:00.000Z",
		payload: { id, cwd: "/work/codex", timestamp: "2026-01-01T00:00:00.000Z", ...extra },
	});
	await put(
		join(day, `rollout-2026-01-01T00-00-00-${THREAD}.jsonl`),
		jsonl([
			meta(THREAD),
			{
				type: "event_msg",
				timestamp: "2026-01-01T00:00:01.000Z",
				payload: { type: "user_message", message: "refactor the parser" },
			},
		]),
	);
	await put(
		join(day, `rollout-2026-01-01T00-00-05-${CHILD}.jsonl`),
		jsonl([meta(CHILD, { source: { subagent: { thread_spawn: { parent_thread_id: THREAD } } } })]),
	);
	await put(join(day, `rollout-2026-01-01T00-00-09-${OTHER}.jsonl`), jsonl([meta(OTHER)]));
	await put(
		join(codex, "archived_sessions", `rollout-2025-12-31T00-00-00-${OTHER}.jsonl.zst`),
		zstdCompressSync(Buffer.from(jsonl([meta(OTHER)]))),
	);
});

afterAll(async () => {
	await rm(root, { recursive: true, force: true });
});

describe("scanSessions (local)", () => {
	test("lists Claude Code transcripts newest first with previews and subagent counts", async () => {
		const page = await scanSessions(local, { format: "claude-code", root: join(root, "claude") });
		expect(page.candidates.map((c) => c.sessionId)).toEqual([SESSION_A, SESSION_B]);
		expect(page.candidates[0]).toMatchObject({
			title: "Login fix",
			cwd: "/work/app",
			subagentCount: 1,
		});
		expect(page.candidates[1]?.title).toBe("older session");
		expect(page.nextCursor).toBeNull();
	});

	test("pages with a cursor and never repeats a candidate", async () => {
		const first = await scanSessions(local, {
			format: "claude-code",
			root: join(root, "claude"),
			limit: 1,
		});
		expect(first.candidates).toHaveLength(1);
		expect(first.nextCursor).not.toBeNull();
		const second = await scanSessions(local, {
			format: "claude-code",
			root: join(root, "claude"),
			limit: 1,
			cursor: first.nextCursor ?? undefined,
		});
		expect(second.candidates.map((c) => c.sessionId)).toEqual([SESSION_B]);
		expect(second.nextCursor).toBeNull();
	});

	test("lists live and archived Codex rollouts; compressed ones list without a preview", async () => {
		const page = await scanSessions(local, { format: "codex", root: join(root, "codex") });
		const byPath = new Map(page.candidates.map((c) => [c.path, c]));
		const main = [...byPath.values()].find((c) => c.sessionId === THREAD);
		expect(main).toMatchObject({ cwd: "/work/codex", title: "refactor the parser" });
		const archived = page.candidates.find((c) => c.archived);
		expect(archived?.sessionId).toBe(OTHER);
		expect(archived?.title).toBeUndefined();
	});

	test("a missing config dir lists nothing instead of failing", async () => {
		const page = await scanSessions(local, { format: "codex", root: join(root, "nowhere") });
		expect(page.candidates).toEqual([]);
	});
});

describe("resolveSessionFile", () => {
	test("refuses paths outside the root or outside the CLI's layout", () => {
		const cc = join(root, "claude");
		const ok = join(cc, "projects", "-work-app", `${SESSION_A}.jsonl`);
		expect(resolveSessionFile(local, "claude-code", cc, ok).path).toBe(ok);
		for (const bad of [
			join(cc, "projects", "..", "..", "etc", `${SESSION_A}.jsonl`),
			join(root, "elsewhere", "projects", "x", `${SESSION_A}.jsonl`),
			join(cc, "projects", "-work-app", "notes.jsonl"),
			join(cc, "settings.json"),
		]) {
			expect(() => resolveSessionFile(local, "claude-code", cc, bad)).toThrow();
		}
		expect(() =>
			resolveSessionFile(local, "codex", join(root, "codex"), join(root, "codex", "config.toml")),
		).toThrow();
	});
});

describe("collectSessionFiles", () => {
	test("Claude Code: gathers sidechains with their agentType and they link on parse", async () => {
		const cc = join(root, "claude");
		const set = await collectSessionFiles(local, {
			format: "claude-code",
			root: cc,
			path: join(cc, "projects", "-work-app", `${SESSION_A}.jsonl`),
		});
		expect(set.subagents).toHaveLength(1);
		expect(set.subagents[0]).toMatchObject({ agentId: "a0123", agentType: "Explore" });
		const main = await decodeSessionBytes(set.main.bytes, {
			label: "main",
			budget: new DecodeBudget(),
		});
		const session = parseClaudeCodeSession({ main: main.rows });
		expect(session.title).toBe("Login fix");
	});

	test("Codex: gathers only rollouts whose session_meta names this thread as parent", async () => {
		const codex = join(root, "codex");
		const set = await collectSessionFiles(local, {
			format: "codex",
			root: codex,
			path: join(
				codex,
				"sessions",
				"2026",
				"01",
				"01",
				`rollout-2026-01-01T00-00-00-${THREAD}.jsonl`,
			),
		});
		expect(set.children.map((c) => c.path.split("/").at(-1))).toEqual([
			`rollout-2026-01-01T00-00-05-${CHILD}.jsonl`,
		]);
	});
});

describe("decodeSessionBytes", () => {
	test("decodes zstd and keeps unparseable rows as text for the parser to report", async () => {
		const decoded = await decodeSessionBytes(
			zstdCompressSync(Buffer.from('{"a":1}\nnot json\n\n{"b":2}\n')),
			{ label: "x", budget: new DecodeBudget() },
		);
		expect(decoded.rows).toEqual([{ a: 1 }, "not json", { b: 2 }]);
		expect(decoded.oversizedLines).toBe(0);
	});

	test("a zstd bomb fails on its output cap", async () => {
		const bomb = zstdCompressSync(Buffer.alloc(4096, 0x61));
		await expect(
			decodeSessionBytes(bomb, { label: "x", budget: new DecodeBudget(1024) }),
		).rejects.toBeInstanceOf(SessionTooLargeError);
	});

	test("the budget is shared: many small files cannot each expand to the per-file cap", async () => {
		// Each file is well under the per-file cap; together they exceed the budget.
		const budget = new DecodeBudget(10_000);
		const file = zstdCompressSync(Buffer.from(`${JSON.stringify({ pad: "x".repeat(3000) })}\n`));
		await decodeSessionBytes(file, { label: "1", budget });
		await decodeSessionBytes(file, { label: "2", budget });
		await decodeSessionBytes(file, { label: "3", budget });
		await expect(decodeSessionBytes(file, { label: "4", budget })).rejects.toBeInstanceOf(
			SessionTooLargeError,
		);
	});

	test("a row over the line cap is skipped and counted, not decoded", async () => {
		const huge = `{"big":"${"y".repeat(SESSION_LINE_MAX_BYTES)}"}`;
		const decoded = await decodeSessionBytes(Buffer.from(`{"a":1}\n${huge}\n{"b":2}\n`), {
			label: "x",
			budget: new DecodeBudget(),
		});
		expect(decoded.rows).toEqual([{ a: 1 }, { b: 2 }]);
		expect(decoded.oversizedLines).toBe(1);
	});
});

// ── Writing ────────────────────────────────────────────────────────────────

function exportResult(files: SessionExportResult["files"], cwd: string): SessionExportResult {
	return {
		format: "claude-code",
		sessionId: SESSION_A,
		cwd,
		title: "Export",
		files,
		resumeCommand: "",
		warnings: [],
	};
}

describe("writeSessionExport (local)", () => {
	test("creates every file, and reports a missing cwd instead of refusing", async () => {
		const target = join(root, "write-ok");
		const written = await writeSessionExport(
			local,
			target,
			exportResult(
				[
					{
						relativePath: ["projects", "-x", `${SESSION_A}.jsonl`],
						content: "a\n",
						mode: "create",
					},
					{
						relativePath: ["projects", "-x", SESSION_A, "subagents", "agent-a1.jsonl"],
						content: "b\n",
						mode: "create",
					},
					{ relativePath: ["session_index.jsonl"], content: "i1\n", mode: "append" },
				],
				join(root, "does-not-exist"),
			),
		);
		expect(written.paths).toHaveLength(3);
		expect(written.cwdExists).toBe(false);
		expect(written.warnings.map((w) => w.kind)).toContain("target_cwd_missing");
		expect(await readFile(join(target, "projects", "-x", `${SESSION_A}.jsonl`), "utf8")).toBe(
			"a\n",
		);

		// The index is appended to, never replaced.
		await writeSessionExport(
			local,
			target,
			exportResult(
				[{ relativePath: ["session_index.jsonl"], content: "i2\n", mode: "append" }],
				root,
			),
		);
		expect(await readFile(join(target, "session_index.jsonl"), "utf8")).toBe("i1\ni2\n");
	});

	test("never overwrites, and removes what it created when a later file fails", async () => {
		const target = join(root, "write-collide");
		await put(join(target, "projects", "-x", "taken.jsonl"), "keep\n");
		await expect(
			writeSessionExport(
				local,
				target,
				exportResult(
					[
						{ relativePath: ["projects", "-x", "new.jsonl"], content: "n\n", mode: "create" },
						{ relativePath: ["projects", "-x", "taken.jsonl"], content: "x\n", mode: "create" },
					],
					root,
				),
			),
		).rejects.toBeInstanceOf(FileExistsError);
		expect(await readFile(join(target, "projects", "-x", "taken.jsonl"), "utf8")).toBe("keep\n");
		await expect(stat(join(target, "projects", "-x", "new.jsonl"))).rejects.toThrow();
	});

	test("refuses a computed path that is not plain names", async () => {
		await expect(
			writeSessionExport(
				local,
				join(root, "write-bad"),
				exportResult([{ relativePath: ["..", "escape.jsonl"], content: "", mode: "create" }], root),
			),
		).rejects.toThrow(/Unsafe session path/);
	});
});

describe("buildSessionDownload", () => {
	test("one file downloads as-is; several download as a zip in config-dir layout", async () => {
		const single = buildSessionDownload(
			exportResult(
				[
					{
						relativePath: ["projects", "-x", `${SESSION_A}.jsonl`],
						content: "a\n",
						mode: "create",
					},
					{ relativePath: ["session_index.jsonl"], content: "i\n", mode: "append" },
				],
				root,
			),
		);
		expect(single.fileName).toBe(`${SESSION_A}.jsonl`);
		// A browser download drops the folders; the path under the config dir travels separately.
		expect(single.placement).toBe(`projects/-x/${SESSION_A}.jsonl`);
		expect(await new Response(single.body).text()).toBe("a\n");

		const zip = buildSessionDownload(
			exportResult(
				[
					{
						relativePath: ["projects", "-x", `${SESSION_A}.jsonl`],
						content: "a\n",
						mode: "create",
					},
					{
						relativePath: ["projects", "-x", SESSION_A, "subagents", "agent-a.jsonl"],
						content: "b\n",
						mode: "create",
					},
				],
				root,
			),
		);
		expect(zip.contentType).toBe("application/zip");
		// The zip carries its layout, so there is no single path to tell the user.
		expect(zip.placement).toBeNull();
		const bytes = new Uint8Array(await new Response(zip.body).arrayBuffer());
		expect([...bytes.subarray(0, 4)]).toEqual([0x50, 0x4b, 0x03, 0x04]);
		const text = new TextDecoder("latin1").decode(bytes);
		expect(text).toContain(`projects/-x/${SESSION_A}/subagents/agent-a.jsonl`);
	});
});

// ── Remote device ──────────────────────────────────────────────────────────

/** A POSIX device held in memory, with a small RPC cap to exercise the chunked path. */
function memoryDevice(maxRpcBytes = 64): DeviceSessionFsPort & { files: Map<string, Uint8Array> } {
	const files = new Map<string, Uint8Array>();
	const dirs = new Set<string>(["/"]);
	const addDirs = (path: string) => {
		for (let dir = posix.dirname(path); dir !== "/"; dir = posix.dirname(dir)) dirs.add(dir);
	};
	return {
		deviceId: "dev-1",
		platformOs: "linux",
		defaultCwd: "/home/alice/work",
		maxRpcBytes,
		files,
		async stat(path, opts): Promise<TransferStatResult> {
			const file = files.get(path);
			if (file) return { exists: true, isDirectory: false, size: file.byteLength, mtimeMs: 1 };
			if (!dirs.has(path)) return { exists: false, isDirectory: false, size: 0, mtimeMs: 0 };
			const entries = opts.recursive
				? [...files.entries()]
						.filter(([p]) => p.startsWith(`${path}/`))
						.slice(0, opts.maxEntries)
						.map(([p, b]) => ({
							relPath: p.slice(path.length + 1),
							size: b.byteLength,
							mtimeMs: 1,
							isDirectory: false,
						}))
				: undefined;
			return { exists: true, isDirectory: true, size: 0, mtimeMs: 1, entries };
		},
		async read(path, maxBytes): Promise<ReadBytesResult> {
			const file = files.get(path);
			if (!file) throw new Error(`no such file: ${path}`);
			if (maxBytes > maxRpcBytes) throw new Error("read over RPC cap");
			return {
				bytes: file.subarray(0, maxBytes),
				truncated: file.byteLength > maxBytes,
				totalSize: file.byteLength,
			};
		},
		async write(path, bytes) {
			if (bytes.byteLength > maxRpcBytes) throw new Error("write over RPC cap");
			addDirs(path);
			files.set(path, bytes);
		},
		async mkdirp(path) {
			dirs.add(path);
			addDirs(`${path}/x`);
		},
		async remove(path) {
			files.delete(path);
		},
		async download(path, _size, _mtime, localDest) {
			await writeFile(localDest, files.get(path) ?? new Uint8Array());
		},
		async upload(localPath, remoteDest) {
			addDirs(remoteDest);
			files.set(remoteDest, new Uint8Array(await readFile(localPath)));
		},
	};
}

describe("device session fs", () => {
	test("home is inferred only from unambiguous layouts", () => {
		expect(inferRemoteHome("/home/alice/work", "linux")).toBe("/home/alice");
		expect(inferRemoteHome("/Users/bob", "darwin")).toBe("/Users/bob");
		expect(inferRemoteHome("/root/src", "linux")).toBe("/root");
		expect(inferRemoteHome("C:\\Users\\Carol\\code", "windows")).toBe("C:\\Users\\Carol");
		expect(inferRemoteHome("/srv/app", "linux")).toBeNull();
		expect(inferRemoteHome(null, "linux")).toBeNull();
	});

	test("writes small files inline and large ones through the chunked path, then scans them", async () => {
		const device = memoryDevice(64);
		const fs = createDeviceSessionFs(device);
		const big = jsonl([
			ccRow("u1", null, "user", "x".repeat(200)),
			ccRow("a1", "u1", "assistant", "ok"),
		]);
		await writeSessionExport(
			fs,
			"/home/alice/.claude",
			exportResult(
				[
					{
						relativePath: ["projects", "-work-app", `${SESSION_A}.jsonl`],
						content: big,
						mode: "create",
					},
					{ relativePath: ["session_index.jsonl"], content: "i\n", mode: "append" },
				],
				"/work/app",
			),
		).then((written) => {
			// No atomic append over RPC: the index is skipped and reported.
			expect(written.warnings.map((w) => w.kind)).toContain("session_index_skipped");
			expect(written.warnings.map((w) => w.kind)).toContain("target_cwd_missing");
		});
		const path = `/home/alice/.claude/projects/-work-app/${SESSION_A}.jsonl`;
		expect(new TextDecoder().decode(device.files.get(path))).toBe(big);

		const page = await scanSessions(fs, { format: "claude-code", root: "/home/alice/.claude" });
		expect(page.candidates.map((c) => c.path)).toEqual([path]);

		const set = await collectSessionFiles(fs, {
			format: "claude-code",
			root: "/home/alice/.claude",
			path,
		});
		expect(new TextDecoder().decode(set.main.bytes)).toBe(big);
	});

	test("refuses to overwrite an existing device file", async () => {
		const device = memoryDevice(1024);
		device.files.set("/r/projects/-x/a.jsonl", new Uint8Array([1]));
		await expect(
			createDeviceSessionFs(device).createFile("/r/projects/-x/a.jsonl", new Uint8Array([2])),
		).rejects.toBeInstanceOf(FileExistsError);
		expect([...(device.files.get("/r/projects/-x/a.jsonl") ?? [])]).toEqual([1]);
	});

	test("a write that fails part-way rolls back the files already on the device", async () => {
		const device = memoryDevice(1024);
		// Model the client going away mid-export: the request's own RPCs start failing
		// after the first file, but the rollback path must still get through.
		const aborted = new AbortController();
		let writes = 0;
		const write = device.write.bind(device);
		device.write = async (path, bytes) => {
			writes += 1;
			if (writes > 1) {
				aborted.abort();
				throw new Error("request aborted");
			}
			await write(path, bytes);
		};
		const remove = device.remove.bind(device);
		device.remove = async (path) => {
			// A remove bound to the aborted request would reject here, as sendRpc does.
			await remove(path);
		};
		await expect(
			writeSessionExport(
				createDeviceSessionFs(device),
				"/r",
				exportResult(
					[
						{
							relativePath: ["projects", "-x", `${SESSION_A}.jsonl`],
							content: "a\n",
							mode: "create",
						},
						{
							relativePath: ["projects", "-x", SESSION_A, "subagents", "agent-a1.jsonl"],
							content: "b\n",
							mode: "create",
						},
					],
					"/r",
				),
			),
		).rejects.toThrow("request aborted");
		expect(aborted.signal.aborted).toBe(true);
		expect([...device.files.keys()].filter((p) => p.endsWith(".jsonl"))).toEqual([]);
	});

	test("a failed chunked upload removes the partial target and its resume state", async () => {
		const device = memoryDevice(16);
		device.upload = async (_local, remoteDest) => {
			device.files.set(remoteDest, new Uint8Array([1]));
			device.files.set(`${remoteDest}.nfpart`, new Uint8Array([1]));
			throw new Error("link dropped");
		};
		await expect(
			createDeviceSessionFs(device).createFile("/r/projects/-x/big.jsonl", new Uint8Array(200)),
		).rejects.toThrow("link dropped");
		expect([...device.files.keys()]).toEqual([]);
	});
});
