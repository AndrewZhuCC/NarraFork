import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const directories: string[] = [];
afterEach(async () => {
	for (const path of directories.splice(0)) await rm(path, { recursive: true, force: true });
});
async function runBounded(
	command: string[],
	env: Record<string, string | undefined>,
	timeoutMs: number,
) {
	const proc = Bun.spawn(command, {
		cwd: process.cwd(),
		env: { ...process.env, ...env },
		stdout: "pipe",
		stderr: "pipe",
	});
	const timer = setTimeout(() => proc.kill(), timeoutMs);
	const collect = async (source: ReadableStream<Uint8Array>) => {
		const reader = source.getReader();
		const parts: Uint8Array[] = [];
		let bytes = 0;
		try {
			for (;;) {
				const result = await reader.read();
				if (result.done) break;
				bytes += result.value.byteLength;
				if (bytes > 64 * 1024) {
					proc.kill();
					throw new Error("Fixture output exceeds budget");
				}
				parts.push(result.value);
			}
			return Buffer.concat(parts).toString();
		} finally {
			reader.releaseLock();
		}
	};
	try {
		const [stdout, stderr, code] = await Promise.all([
			collect(proc.stdout),
			collect(proc.stderr),
			proc.exited,
		]);
		return { code, stdout, stderr };
	} finally {
		clearTimeout(timer);
	}
}

describe("compiled request recorder worker", () => {
	test("standalone binary resolves its embedded worker and saves/downloads a ZIP", async () => {
		const directory = await mkdtemp(join(tmpdir(), "recording-compiled-"));
		directories.push(directory);
		const sourceDirectory = await mkdtemp(
			join(resolve(import.meta.dir, "../.."), ".recording-compiled-probe-"),
		);
		directories.push(sourceDirectory);
		const fixture = join(sourceDirectory, "fixture.ts");
		const output = join(directory, process.platform === "win32" ? "fixture.exe" : "fixture");
		const recorder = resolve(import.meta.dir, "../request-recorder.ts");
		const settings = resolve(import.meta.dir, "../settings/index.ts");
		const worker = resolve(import.meta.dir, "../request-recorder-worker.ts");
		await writeFile(
			fixture,
			`import { RequestRecorderClient } from ${JSON.stringify(recorder)};
import { settings } from ${JSON.stringify(settings)};
settings.diskSafety = { ...settings.diskSafety, mode: "off" };
const client = new RequestRecorderClient(process.env.NARRAFORK_HOME!);
try {
 client.configure({ requestDumpRecordingEnabled: true, requestDumpRequestBytes: 1048576, requestDumpResponseBytes: 1048576, requestDumpEnabled: false, requestDumpErrorsOnly: false });
 await client.call({ type: "begin", identity: { requestId: "compiled-request", narratorId: "compiled-narrator", provider: "fixture", model: "fixture" }, continuous: false, errorsOnly: false });
 const data = new TextEncoder().encode("data: compiled-worker-test\\n\\n");
 client.capture({ type: "chunk", requestId: "compiled-request", side: "response", kind: "body", data, packet: 1, offset: 0, total: data.byteLength, time: new Date().toISOString() });
 client.capture({ type: "end", requestId: "compiled-request" });
 const saved = await client.call<any>({ type: "save", narratorId: "compiled-narrator" });
 const archive = await client.call<any>({ type: "archive", narratorId: "compiled-narrator", saveId: saved.id });
 const bytes = new Uint8Array(await Bun.file(archive.path).arrayBuffer());
 console.log(JSON.stringify({ requestCount: saved.requestCount, partial: saved.partial, signature: Array.from(bytes.slice(0, 4)), bytes: archive.bytes }));
} finally { await client.close(); }
`,
		);
		const environment = {
			NARRAFORK_HOME: join(directory, "home"),
			NARRAFORK_ALLOW_MULTIPLE: "1",
			NARRAFORK_TEST: "1",
		};
		const built = await runBounded(
			[
				process.execPath,
				"build",
				"--compile",
				"--minify",
				fixture,
				worker,
				"--root",
				resolve(import.meta.dir, "../.."),
				"--asset-naming=[dir]/[name].[ext]",
				"--outfile",
				output,
			],
			environment,
			90_000,
		);
		expect(built.code, built.stderr).toBe(0);
		const ran = await runBounded([output], environment, 30_000);
		expect(ran.code, ran.stderr).toBe(0);
		const result = JSON.parse(ran.stdout.trim().split("\n").at(-1) ?? "{}");
		expect(result.requestCount).toBe(1);
		expect(result.partial).toBe(false);
		expect(result.signature).toEqual([0x50, 0x4b, 0x03, 0x04]);
		expect(result.bytes).toBeGreaterThan(0);
	}, 120_000);
});
