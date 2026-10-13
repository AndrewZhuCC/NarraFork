import { Database, SQLiteError } from "bun:sqlite";
import { afterEach, describe, expect, mock, spyOn, test } from "bun:test";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { z } from "zod/v4";
import * as localFileChangeRuntime from "../../../services/file-change-runtime";
import { specVfsService } from "../../../services/spec-vfs-service";
import { type DiskAssessment, DiskSpaceMonitor, diskSpaceMonitor } from "../../disk-safety";
import { DEFAULT_DISK_SAFETY } from "../../disk-safety-config";
import { logger } from "../../logger";
import { getNarraforkHome } from "../../narrafork-home";
import { settings } from "../../settings";
import { checkToolDiskSafety, diskToolError } from "../disk-safety";
import { executeTool } from "../tool-executor";
import { toolRegistry } from "../tool-registry";
import { editTool } from "../tools/edit";
import { writeTool } from "../tools/write";
import type { AgentConfig, ToolExecutionTarget } from "../types";

const NAME = "__DiskSafetyWrite";
const MB = 1024 * 1024;
const originalSettings = settings.diskSafety;
const originalAssess = diskSpaceMonitor.assess;
const originalNoteDiskFull = diskSpaceMonitor.noteDiskFull;
const originalBash = toolRegistry.get("Bash");
const originalRead = toolRegistry.get("Read");
const originalWrite = toolRegistry.get("Write");
const originalEdit = toolRegistry.get("Edit");
afterEach(() => {
	settings.diskSafety = originalSettings;
	diskSpaceMonitor.assess = originalAssess;
	diskSpaceMonitor.noteDiskFull = originalNoteDiskFull;
	toolRegistry.unregister("Bash");
	if (originalBash) toolRegistry.register(originalBash);
	toolRegistry.unregister(NAME);
	toolRegistry.unregister("Read");
	if (originalRead) toolRegistry.register(originalRead);
	toolRegistry.unregister("Write");
	if (originalWrite) toolRegistry.register(originalWrite);
	toolRegistry.unregister("Edit");
	if (originalEdit) toolRegistry.register(originalEdit);
});

function assessment(path: string, freeMb: number): DiskAssessment {
	return {
		path,
		level: freeMb <= 64 ? "critical" : freeMb <= 256 ? "blocked" : "ok",
		space: {
			key: path === getNarraforkHome() ? "home" : "work",
			mountPath: path,
			freeBytes: freeMb * MB,
			totalBytes: 10000 * MB,
			checkedAt: 1,
		},
	};
}
function config(): AgentConfig {
	return {
		narratorId: "disk-safety-test",
		conversationId: "disk-safety-test",
		provider: "anthropic",
		model: "test",
		cwd: tmpdir(),
		signal: new AbortController().signal,
		locale: "zh-CN",
		permissionHandler: async () => ({ behavior: "allow" }),
	};
}

function nativeSqliteFull(): SQLiteError {
	const database = new Database(":memory:");
	try {
		database.exec("PRAGMA max_page_count=2; CREATE TABLE fixture (value BLOB)");
		try {
			database.query("INSERT INTO fixture VALUES (zeroblob(100000))").run();
		} catch (error) {
			if (error instanceof SQLiteError && error.code === "SQLITE_FULL") return error;
			throw error;
		}
		throw new Error("Fixture did not raise SQLITE_FULL");
	} finally {
		database.close();
	}
}

describe("disk guard tool admission", () => {
	test("permission approval cannot bypass low-space refusal; no snapshot or tool is started", async () => {
		settings.diskSafety = { ...DEFAULT_DISK_SAFETY };
		diskSpaceMonitor.assess = mock(async (path) => assessment(path, 200));
		const execute = mock(async () => ({ output: "wrote" }));
		const before = mock(async () => {});
		const after = mock(async () => {});
		const starting = mock(async (_id, binding) => binding);
		toolRegistry.register({
			name: NAME,
			description: "disk test",
			parameters: z.object({}),
			execute,
		});
		const cfg = config();
		cfg.onToolExecutionBefore = before;
		cfg.onToolExecutionAfter = after;
		cfg.onToolExecutionStarting = starting;
		const result = await executeTool({ name: NAME, toolUseId: "low", input: {} }, cfg);
		expect(result.isError).toBe(true);
		expect(result.output).toContain("本次写入已拒绝");
		expect(result.metadata?.diskSafety).toMatchObject({ code: "DISK_SPACE_LOW", occurred: false });
		expect(execute).not.toHaveBeenCalled();
		expect(before).not.toHaveBeenCalled();
		expect(after).not.toHaveBeenCalled();
		expect(starting).not.toHaveBeenCalled();
	});

	test("critical home marks a mutating tool fatal even if its worktree has ample space", async () => {
		settings.diskSafety = { ...DEFAULT_DISK_SAFETY };
		diskSpaceMonitor.assess = mock(async (path) =>
			assessment(path, path === getNarraforkHome() ? 10 : 2048),
		);
		toolRegistry.register({
			name: NAME,
			description: "disk test",
			parameters: z.object({}),
			execute: async () => ({ output: "write" }),
		});
		const result = await executeTool({ name: NAME, toolUseId: "critical", input: {} }, config());
		expect(result.fatal).toBe(true);
		expect(result.isError).toBe(true);
	});

	test("read-only tools remain available on a critical worktree disk when HOME is healthy", async () => {
		settings.diskSafety = { ...DEFAULT_DISK_SAFETY };
		diskSpaceMonitor.assess = mock(async (path) =>
			assessment(path, path === getNarraforkHome() ? 2048 : 10),
		);
		toolRegistry.register({
			name: "Read",
			description: "disk test read",
			parameters: z.object({}),
			execute: async () => ({ output: "file content" }),
		});
		const cfg = config();
		cfg.narratorId = "disk-read-notice";
		const result = await executeTool({ name: "Read", toolUseId: "read", input: {} }, cfg);
		expect(result.isError).not.toBe(true);
		expect(result.fatal).not.toBe(true);
		expect(result.output).toContain("file content");
		expect(result.output).toContain("磁盘空间警告");
	});

	test("critical HOME also stops read history writes but background cancellation remains available", async () => {
		settings.diskSafety = { ...DEFAULT_DISK_SAFETY };
		diskSpaceMonitor.assess = mock(async (path) => assessment(path, 10));
		const execute = mock(async () => ({ output: "content" }));
		toolRegistry.register({
			name: "Read",
			description: "disk read",
			parameters: z.object({}),
			execute,
		});
		const result = await executeTool(
			{ name: "Read", toolUseId: "critical-read", input: {} },
			config(),
		);
		expect(result.fatal).toBe(true);
		expect(execute).not.toHaveBeenCalled();
		await expect(
			checkToolDiskSafety(
				"Bash",
				{ stop: "background-writer" },
				{ narratorId: "cancel-under-pressure", cwd: "/work", locale: "en" },
			),
		).resolves.toBeDefined();
	});

	test("warn mode explicitly lets the tool run; off performs no probes", async () => {
		settings.diskSafety = { ...DEFAULT_DISK_SAFETY, mode: "warn" };
		const assess = mock(async (path: string) => assessment(path, 10));
		diskSpaceMonitor.assess = assess;
		const execute = mock(async () => ({ output: "override write" }));
		toolRegistry.register({
			name: NAME,
			description: "disk test",
			parameters: z.object({}),
			execute,
		});
		expect(
			(await executeTool({ name: NAME, toolUseId: "override", input: {} }, config())).isError,
		).not.toBe(true);
		expect(execute).toHaveBeenCalledTimes(1);
		assess.mockClear();
		settings.diskSafety.mode = "off";
		await executeTool({ name: NAME, toolUseId: "off", input: {} }, config());
		expect(assess).not.toHaveBeenCalled();
	});
});

describe("tool path coverage and error consistency", () => {
	const local: ToolExecutionTarget = {
		deviceId: "local",
		backendKind: "local",
		cwd: "/work",
		canonicalPath: "/other-volume/file",
		selectionSource: "local_default",
	};
	test("canonical input and multi-target destination partitions are checked", async () => {
		const resolveVolume = mock(async (path: string) => ({ key: path, mountPath: path }));
		const monitor = new DiskSpaceMonitor({
			resolveVolume,
			statVolume: async () => ({ freeBytes: 2048 * MB, totalBytes: 10000 * MB }),
			now: Date.now,
		});
		await checkToolDiskSafety(
			"StructSed",
			{},
			{
				narratorId: "multi",
				cwd: "/work",
				locale: "en",
				executionPlan: {
					kind: "multi",
					primaryKey: "source",
					endpoints: [
						{ key: "source", operation: "write", target: local },
						{
							key: "destination",
							operation: "write",
							target: { ...local, canonicalPath: "/third-volume/dest" },
						},
					],
				},
			},
			monitor,
			DEFAULT_DISK_SAFETY,
			"/home",
		);
		expect(resolveVolume.mock.calls.map((c) => c[0])).toEqual(
			expect.arrayContaining(["/home", "/work", "/other-volume/file", "/third-volume/dest"]),
		);
	});

	test("remote paths are not probed on the host; host home remains covered", async () => {
		const resolveVolume = mock(async (path: string) => ({ key: path, mountPath: path }));
		const monitor = new DiskSpaceMonitor({
			resolveVolume,
			statVolume: async () => ({ freeBytes: 2048 * MB, totalBytes: 10000 * MB }),
			now: Date.now,
		});
		const info = spyOn(logger, "info").mockImplementation(() => {});
		const run = () =>
			checkToolDiskSafety(
				"Read",
				{},
				{
					narratorId: "remote-check",
					cwd: "/remote",
					locale: "zh-CN",
					executionTarget: { ...local, backendKind: "remote", deviceId: "device" },
				},
				monitor,
				DEFAULT_DISK_SAFETY,
				"/home",
			);
		try {
			const result = await run();
			expect(resolveVolume.mock.calls.map((c) => c[0])).toEqual(["/home"]);
			// Operator-only coverage gap: never appended to the model-visible result.
			expect(result.notice).toBe("");
			await run();
			// Logged at most once per TTL host-wide (an earlier test may already own the slot).
			const remoteLogs = info.mock.calls.filter((c) => String(c[0]).includes("remote devices"));
			expect(remoteLogs.length).toBeLessThanOrEqual(1);
		} finally {
			info.mockRestore();
		}
	});

	test("an unavailable probe is logged with its reason, not injected into the result", async () => {
		const monitor = new DiskSpaceMonitor({
			// One volume, as files under one HOME/worktree really share their mount.
			resolveVolume: async () => ({ key: "vol", mountPath: "/probe-unknown" }),
			statVolume: async () => {
				throw Object.assign(new Error("statfs failed"), { code: "EPERM" });
			},
			now: Date.now,
		});
		const warn = spyOn(logger, "warn").mockImplementation(() => {});
		try {
			const home = "/probe-unknown-home";
			const result = await checkToolDiskSafety(
				"Grep",
				{},
				{ narratorId: "probe-unknown", cwd: "/probe-unknown-work", locale: "zh-CN" },
				monitor,
				DEFAULT_DISK_SAFETY,
				home,
			);
			expect(result.notice).toBe("");
			expect(result.assessments.every((a) => a.level === "unknown")).toBe(true);
			const homeLog = warn.mock.calls.find((call) => call[1]?.path === home);
			expect(homeLog?.[1]).toMatchObject({ isHome: true });
			expect(String(homeLog?.[1]?.reason)).toContain("EPERM statfs failed");
			// Throttled by cause, not by file: many more paths in the TTL log nothing new.
			const logged = warn.mock.calls.length;
			for (let i = 0; i < 20; i++)
				await checkToolDiskSafety(
					"Read",
					{ file_path: `/probe-unknown-work/file-${i}.ts` },
					{ narratorId: `probe-unknown-${i}`, cwd: "/probe-unknown-work", locale: "en" },
					monitor,
					DEFAULT_DISK_SAFETY,
					home,
				);
			expect(warn.mock.calls.length).toBe(logged);
		} finally {
			warn.mockRestore();
		}
	});

	test("warn mode with a real low-space reading still tells the agent, without operator text", async () => {
		const monitor = new DiskSpaceMonitor({
			resolveVolume: async () => ({ key: "shared", mountPath: "/vol" }),
			statVolume: async () => ({ freeBytes: 100 * MB, totalBytes: 10000 * MB }),
			now: Date.now,
		});
		const result = await checkToolDiskSafety(
			"Write",
			{},
			{ narratorId: "warn-mode-notice", cwd: "/work", locale: "en" },
			monitor,
			{ ...DEFAULT_DISK_SAFETY, mode: "warn" },
			"/home-warn",
		);
		expect(result.notice).toContain("Disk space warning: /vol has only 100 MiB free");
		expect(result.notice).not.toContain("diskSafety.mode");
	});

	test("failed Bash output cannot inject disk errors, poison the cache or stop the narrator", async () => {
		settings.diskSafety = { ...DEFAULT_DISK_SAFETY };
		diskSpaceMonitor.assess = mock(async (path) => assessment(path, 150 * 1024));
		const noteDiskFull = mock(() => {});
		diskSpaceMonitor.noteDiskFull = noteDiskFull;
		const outputs = [
			"bun test v1.4.2\n(pass) handles SQLITE_FULL\n(fail) unrelated assertion\nExpected: 1\nReceived: 2",
			'Expected: "ENOSPC: no space left on device"\nReceived: "EACCES"',
			"const code = 'EDQUOT'; // disk quota exceeded",
			"Error: ENOSPC: no space left on device",
		];
		for (const [index, output] of outputs.entries()) {
			toolRegistry.register({
				name: "Bash",
				description: "disk test bash",
				parameters: z.object({ command: z.string() }),
				execute: async () => ({ output, isError: true, metadata: { exitCode: 1 } }),
			});
			const result = await executeTool(
				{ name: "Bash", toolUseId: `failed-bash-${index}`, input: { command: "bun test" } },
				config(),
			);
			expect(result.output).toBe(output);
			expect(result.isError).toBe(true);
			expect(result.fatal).not.toBe(true);
			expect(result.metadata?.diskSafety).toBeUndefined();
			expect(result.metadata?.exitCode).toBe(1);
		}
		expect(noteDiskFull).not.toHaveBeenCalled();
		toolRegistry.register({
			name: NAME,
			description: "subsequent tool remains available",
			parameters: z.object({}),
			execute: async () => ({ output: "continued" }),
		});
		const next = await executeTool({ name: NAME, toolUseId: "next", input: {} }, config());
		expect(next.output).toBe("continued");
		expect(next.isError).not.toBe(true);
	});

	test("confirmed thrown storage failures still activate disk protection", async () => {
		settings.diskSafety = { ...DEFAULT_DISK_SAFETY };
		diskSpaceMonitor.assess = mock(async (path) => assessment(path, 150 * 1024));
		const noteDiskFull = mock(() => {});
		diskSpaceMonitor.noteDiskFull = noteDiskFull;
		const actualPath = join(tmpdir(), "actual-disk-failure-file");
		const errors = [
			Object.assign(new Error("native write failure"), {
				code: "ENOSPC",
				syscall: "write",
				path: actualPath,
			}),
			Object.assign(new Error("native quota failure"), {
				code: "EDQUOT",
				syscall: "open",
				path: actualPath,
			}),
		];
		for (const [index, error] of errors.entries()) {
			toolRegistry.register({
				name: NAME,
				description: "real storage exception",
				parameters: z.object({}),
				executionRouting: {
					kind: "single",
					resolve: () => ({ key: "primary", operation: "write", path: actualPath }),
				},
				execute: async () => {
					throw error;
				},
			});
			const result = await executeTool(
				{ name: NAME, toolUseId: `confirmed-${index}`, input: {} },
				config(),
			);
			expect(result.isError).toBe(true);
			expect(result.output).toContain("可能已部分写入");
			expect(result.metadata?.diskSafety).toMatchObject({ occurred: true });
			expect(result.fatal).not.toBe(true);
			expect(noteDiskFull).toHaveBeenLastCalledWith(actualPath);
		}
		expect(noteDiskFull).toHaveBeenCalledTimes(2);
	});
});

describe("confirmed failures and real tool catches", () => {
	for (const tool of [writeTool, editTool]) {
		for (const confirmed of [true, false]) {
			test(`${tool.name} preserves ${confirmed ? "native SQLite FULL" : "ordinary text-only failure"}`, async () => {
				settings.diskSafety = { ...DEFAULT_DISK_SAFETY };
				diskSpaceMonitor.assess = mock(async (path) => assessment(path, 150 * 1024));
				const noteDiskFull = mock(() => {});
				diskSpaceMonitor.noteDiskFull = noteDiskFull;
				const failure = confirmed
					? nativeSqliteFull()
					: new Error("SQLITE_FULL: database or disk is full");
				const read = spyOn(specVfsService, "readSpecFile").mockResolvedValue({
					path: "disk-fixture.txt",
					uri: "spec://disk-fixture.txt",
					content: "before",
					readonly: false,
					uiEditable: true,
					builtin: false,
					namespaceId: "disk-fixture",
				});
				const write = spyOn(specVfsService, "writeSpecFile").mockImplementation(async () => {
					throw failure;
				});
				try {
					toolRegistry.register(tool);
					const input =
						tool.name === "Write"
							? { file_path: "spec://disk-fixture.txt", content: "after" }
							: { file_path: "spec://disk-fixture.txt", old_string: "before", new_string: "after" };
					const result = await executeTool(
						{ name: tool.name, toolUseId: `${tool.name}-${confirmed}`, input },
						config(),
					);
					expect(write).toHaveBeenCalledTimes(1);
					expect(result.isError).toBe(true);
					if (confirmed) {
						expect(result.fatal).toBe(true);
						expect(result.metadata?.diskSafety).toMatchObject({
							occurred: true,
							path: getNarraforkHome(),
						});
						expect(noteDiskFull).toHaveBeenCalledTimes(1);
						expect(noteDiskFull).toHaveBeenCalledWith(getNarraforkHome());
					} else {
						expect(result.output).toContain(failure.message);
						expect(result.fatal).not.toBe(true);
						expect(result.metadata?.diskSafety).toBeUndefined();
						expect(noteDiskFull).not.toHaveBeenCalled();
					}
				} finally {
					read.mockRestore();
					write.mockRestore();
				}
			});
		}
	}

	test("unknown thrown failures do not update host cache or stop the narrator", async () => {
		settings.diskSafety = { ...DEFAULT_DISK_SAFETY };
		diskSpaceMonitor.assess = mock(async (path) => assessment(path, 150 * 1024));
		const noteDiskFull = mock(() => {});
		diskSpaceMonitor.noteDiskFull = noteDiskFull;
		const errors = [
			// A native error still needs a known application-DB boundary before it
			// can poison HOME's cache or stop this narrator.
			nativeSqliteFull(),
			new Error("ENOSPC: no space left on device"),
			Object.assign(new Error("database or disk is full"), { errno: 13 }),
			Object.assign(new Error("SQLITE_FULL"), { code: "SQLITE_FULL" }),
			Object.assign(new Error("unknown storage source"), { code: "ENOSPC" }),
			Object.assign(new Error("watch limit"), { code: "ENOSPC", syscall: "watch", path: tmpdir() }),
		];
		for (const [index, error] of errors.entries()) {
			toolRegistry.register({
				name: NAME,
				description: "unknown failure",
				parameters: z.object({}),
				execute: async () => {
					throw error;
				},
			});
			const result = await executeTool(
				{ name: NAME, toolUseId: `unknown-${index}`, input: {} },
				config(),
			);
			expect(result.isError).toBe(true);
			expect(result.fatal).not.toBe(true);
			expect(result.metadata?.diskSafety).toBeUndefined();
		}
		expect(noteDiskFull).not.toHaveBeenCalled();
	});

	test("remote filesystem errors cannot be attributed to the host", () => {
		const noteDiskFull = mock(() => {});
		diskSpaceMonitor.noteDiskFull = noteDiskFull;
		const error = Object.assign(new Error("remote full"), {
			code: "ENOSPC",
			syscall: "write",
			path: join(tmpdir(), "remote-file"),
		});
		expect(diskToolError(error, "zh-CN", false)).toBeNull();
		expect(noteDiskFull).not.toHaveBeenCalled();
	});
});

describe("real local file tool error boundaries", () => {
	for (const tool of [writeTool, editTool]) {
		for (const confirmed of [true, false]) {
			test(`${tool.name} local catch ${confirmed ? "preserves syscall evidence" : "ignores message-only evidence"}`, async () => {
				settings.diskSafety = { ...DEFAULT_DISK_SAFETY };
				diskSpaceMonitor.assess = mock(async (path) => assessment(path, 150 * 1024));
				const noteDiskFull = mock(() => {});
				diskSpaceMonitor.noteDiskFull = noteDiskFull;
				const operationPath = join(tmpdir(), "disk-fixture.txt");
				const failurePath = join(tmpdir(), "disk-fixture-snapshot");
				const failure = confirmed
					? Object.assign(new Error("local disk write failed"), {
							code: "ENOSPC",
							syscall: "write",
							path: failurePath,
						})
					: new Error("ENOSPC: no space left on device");
				const change = spyOn(localFileChangeRuntime, "executeLocalFileChange").mockImplementation(
					async () => {
						throw failure;
					},
				);
				try {
					toolRegistry.register(tool);
					const input =
						tool.name === "Write"
							? { file_path: operationPath, content: "after" }
							: { file_path: operationPath, old_string: "before", new_string: "after" };
					const result = await executeTool(
						{ name: tool.name, toolUseId: `local-${tool.name}-${confirmed}`, input },
						config(),
					);
					expect(change).toHaveBeenCalledTimes(1);
					expect(result.isError).toBe(true);
					expect(result.fatal).not.toBe(true);
					if (confirmed) {
						expect(result.metadata?.diskSafety).toMatchObject({
							occurred: true,
							path: failurePath,
						});
						expect(noteDiskFull).toHaveBeenCalledTimes(1);
						expect(noteDiskFull).toHaveBeenCalledWith(failurePath);
					} else {
						expect(result.output).toContain(failure.message);
						expect(result.metadata?.diskSafety).toBeUndefined();
						expect(noteDiskFull).not.toHaveBeenCalled();
					}
				} finally {
					change.mockRestore();
				}
			});
		}
	}
});
