import { expect, test } from "bun:test";
import type { ExecutionBackend } from "../../execution/backend";
import { posixPathSemantics } from "../../execution/path-semantics";
import type { ToolContext } from "../../types";
import { editTool } from "../edit";
import { writeTool } from "../write";

function fixture() {
	const files = new Map<string, Uint8Array>([["/remote/a.txt", Buffer.from("old")]]);
	const entered = Promise.withResolvers<void>();
	const release = Promise.withResolvers<void>();
	const reads: string[] = [];
	let hold = true;
	const backend = {
		kind: "remote",
		deviceId: `remote-${crypto.randomUUID()}`,
		pathFlavor: "posix",
		paths: posixPathSemantics,
		runtimeGeneration: 1,
		defaultCwd: "/remote",
		resolvePathIdentity: async (path: string) => ({
			lexicalPath: path,
			canonicalPath: path,
			runtimeGeneration: 1,
		}),
		statFile: async (path: string) =>
			files.has(path)
				? { isDirectory: false, isFile: true, size: files.get(path)?.byteLength ?? 0 }
				: null,
		readFileBytes: async (path: string) => {
			reads.push(path);
			const bytes = files.get(path);
			if (!bytes) throw new Error("missing");
			return { bytes, truncated: false, totalSize: bytes.byteLength };
		},
		mkdirp: async () => {},
		writeFileBytes: async (path: string, bytes: Uint8Array) => {
			if (hold && path === "/remote/a.txt") {
				hold = false;
				entered.resolve();
				await release.promise;
			}
			files.set(path, bytes);
		},
	} as unknown as ExecutionBackend;
	const context = (cwd = "/remote"): ToolContext => ({
		narratorId: "remote-writer",
		cwd,
		locale: "en",
		signal: new AbortController().signal,
		resolveBackend: () => backend,
		requestPermission: async () => ({ behavior: "allow" }),
	});
	return { files, backend, context, entered, release, reads };
}

test("remote Write and Edit serialize the entire same-file read-modify-write across sessions", async () => {
	const f = fixture();
	const first = writeTool.execute({ file_path: "/remote/a.txt", content: "new" }, f.context());
	try {
		await f.entered.promise;
		const second = editTool.execute(
			{ file_path: "/remote/a.txt", old_string: "new", new_string: "edited" },
			f.context("/different"),
		);
		await Promise.resolve();
		expect(f.reads).toEqual(["/remote/a.txt"]);
		f.release.resolve();
		expect((await first).isError).not.toBe(true);
		expect((await second).isError).not.toBe(true);
		expect(new TextDecoder().decode(f.files.get("/remote/a.txt"))).toBe("edited");
	} finally {
		f.release.resolve();
		await first;
	}
});

test("another remote file can finish while the first write is pending", async () => {
	const f = fixture();
	const first = writeTool.execute({ file_path: "/remote/a.txt", content: "new" }, f.context());
	try {
		await f.entered.promise;
		const second = await writeTool.execute(
			{ file_path: "/remote/deep/b.txt", content: "parallel" },
			f.context(),
		);
		expect(second.isError).not.toBe(true);
		expect(new TextDecoder().decode(f.files.get("/remote/deep/b.txt"))).toBe("parallel");
	} finally {
		f.release.resolve();
		await first;
	}
});

test("general remote Write retains payloads above conditional-write's 2 MB cap", async () => {
	const f = fixture();
	const content = "x".repeat(2_000_001);
	const result = await writeTool.execute(
		{ file_path: "/remote/new/deep/large.txt", content },
		f.context(),
	);
	expect(result.isError).not.toBe(true);
	expect(f.files.get("/remote/new/deep/large.txt")?.byteLength).toBe(content.length);
});

test("remote wait cancellation leaves the active writer intact", async () => {
	const f = fixture();
	const first = writeTool.execute({ file_path: "/remote/a.txt", content: "first" }, f.context());
	try {
		await f.entered.promise;
		const controller = new AbortController();
		const second = editTool.execute(
			{ file_path: "/remote/a.txt", old_string: "first", new_string: "cancelled" },
			{ ...f.context(), signal: controller.signal },
		);
		controller.abort(new Error("cancelled"));
		expect((await second).isError).toBe(true);
		expect(new TextDecoder().decode(f.files.get("/remote/a.txt"))).toBe("old");
	} finally {
		f.release.resolve();
		await first;
	}
	expect(new TextDecoder().decode(f.files.get("/remote/a.txt"))).toBe("first");
});
