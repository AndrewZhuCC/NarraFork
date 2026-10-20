import { afterEach, beforeEach, describe, expect, mock, spyOn, test } from "bun:test";
import { createHash } from "node:crypto";
import * as fs from "node:fs";
import {
	getHelperAssetName,
	HELPER_CATALOG_VERSION,
	HELPER_MANIFEST_FILENAME,
	HELPER_PLATFORMS,
	HELPER_RELEASE_TAG,
	HELPER_TOOL_VERSIONS,
	HELPER_TOOLS,
	type HelperPlatform,
} from "../../../shared/helper-distribution";
import type { GrepParams } from "../agent/execution/backend";
import {
	downloadHelperBinary,
	HELPER_BIN_DIR,
	resetHelperBinaryDownloadCache,
} from "../helper-binaries";
import { getCliHelperSpec } from "../helper-binary-platform";
import { resetHelperDistributionCache } from "../helper-distribution-runtime";
import { setOutboundFetchOverrideForTest } from "../net/outbound-fetch";
import { settings } from "../settings";
import { DEFAULT_UPDATE_SETTINGS } from "../settings/update-source";

function binary(platform: HelperPlatform): Buffer<ArrayBuffer> {
	const bytes = Buffer.alloc(256);
	const arm = platform.endsWith("arm64");
	if (platform.startsWith("windows-")) {
		bytes.write("MZ");
		bytes.writeUInt32LE(128, 60);
		bytes.writeUInt32LE(0x4550, 128);
		bytes.writeUInt16LE(arm ? 0xaa64 : 0x8664, 132);
	} else if (platform.startsWith("linux-")) {
		bytes.writeUInt32BE(0x7f454c46, 0);
		bytes[4] = 2;
		bytes[5] = 1;
		bytes.writeUInt16LE(arm ? 183 : 62, 18);
	} else {
		bytes.writeUInt32LE(0xfeedfacf, 0);
		bytes.writeUInt32LE(arm ? 0x0100000c : 0x01000007, 4);
	}
	return bytes;
}
function catalog(repository = "fork/repo") {
	return {
		schemaVersion: 1,
		repository,
		tag: HELPER_RELEASE_TAG,
		commit: "a".repeat(40),
		catalogVersion: HELPER_CATALOG_VERSION,
		files: HELPER_TOOLS.flatMap((tool) =>
			HELPER_PLATFORMS.map((platform) => ({
				tool,
				platform,
				toolVersion: HELPER_TOOL_VERSIONS[tool],
				name: getHelperAssetName(tool, platform),
				size: 256,
				sha256: createHash("sha256").update(binary(platform)).digest("hex"),
			})),
		),
		licenses: [{ name: "LICENSE.txt", size: 10, sha256: "a".repeat(64) }],
	};
}
function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => {
		resolve = done;
	});
	return { promise, resolve };
}
const originalExists = fs.existsSync;
const originalWhich = Bun.which;
const originalUpdate = settings.update;
const calls: string[] = [];
const spawnSpy = () => spyOn(Bun, "spawn");
let subprocess: ReturnType<typeof spawnSpy>;
let resolver: typeof import("../ripgrep").resolveRgPath;
let Backend: typeof import("../agent/execution/local-backend").LocalBackend;
const spec = getCliHelperSpec("rg");
if (!spec?.platform) throw new Error("This fixture requires a supported native helper platform");
const nativeBinary = binary(spec.platform as HelperPlatform);
const nativeAssetName = spec.toolName;
const mirrorAssetUrl = `https://personal.example/api/v3/products/narrafork/mirror/tools/helpers/${HELPER_RELEASE_TAG}/assets/${nativeAssetName}`;
const githubAssetUrl = (repository: string, name: string) =>
	`https://github.com/${repository}/releases/download/${HELPER_RELEASE_TAG}/${name}`;
function useRoutes(handler?: (url: string, init?: RequestInit) => Promise<Response>) {
	setOutboundFetchOverrideForTest(async (input, init) => {
		const url = String(input);
		calls.push(url);
		if (handler) return handler(url, init);
		return url.endsWith(".json") ? Response.json(catalog()) : new Response(nativeBinary);
	});
}
function useSelectedMirrorRoutes(repository = "fork/repo", mirrorAvailable = true) {
	useRoutes(async (url) => {
		if (url === githubAssetUrl(repository, HELPER_MANIFEST_FILENAME))
			return Response.json(catalog(repository));
		if (url === mirrorAssetUrl)
			return mirrorAvailable ? new Response(nativeBinary) : new Response(null, { status: 404 });
		if (url === githubAssetUrl(repository, nativeAssetName)) return new Response(nativeBinary);
		throw new Error("Unexpected component source or non-pinned asset request");
	});
}
function grep(signal: AbortSignal) {
	const params: GrepParams = {
		pattern: "needle",
		searchPath: HELPER_BIN_DIR,
		cwd: HELPER_BIN_DIR,
		outputMode: "content",
		showLineNumbers: true,
		maxBytes: 1024,
		timeoutMs: 5_000,
		signal,
	};
	return new Backend().grep(params);
}
function switchSource(repository?: string) {
	settings.update = {
		...(settings.update ?? DEFAULT_UPDATE_SETTINGS),
		source: "update-server",
		serverUrl: "https://personal.example",
		...(repository ? { githubRepository: repository } : {}),
	};
}
beforeEach(async () => {
	settings.update = {
		...(settings.update ?? DEFAULT_UPDATE_SETTINGS),
		source: "github",
		githubRepository: "fork/repo",
		serverUrl: "https://personal.example",
		product: "narrafork",
		proxy: { mode: "direct" },
	};
	calls.length = 0;
	resetHelperBinaryDownloadCache();
	resetHelperDistributionCache();
	fs.rmSync(HELPER_BIN_DIR, { recursive: true, force: true });
	fs.mkdirSync(HELPER_BIN_DIR, { recursive: true });
	// Only tool discovery is hidden. All manifest/hash/stream/cache IO remains real.
	spyOn(fs, "existsSync").mockImplementation((path) =>
		/(?:^|[/\\])rg(?:\.exe)?$/.test(String(path)) ? false : originalExists(path),
	);
	spyOn(Bun, "which").mockImplementation((name, options) =>
		name === "rg" ? null : originalWhich(name, options),
	);
	subprocess = spawnSpy().mockImplementation(() => {
		throw new Error("Unexpected subprocess; no real helpers may execute in this fixture");
	});
	// Import after discovery spies so the module-level PATH fast path is also absent.
	({ resolveRgPath: resolver } = await import("../ripgrep"));
	({ LocalBackend: Backend } = await import("../agent/execution/local-backend"));
	useRoutes();
});
afterEach(() => {
	setOutboundFetchOverrideForTest(null);
	mock.restore();
	settings.update = originalUpdate;
	fs.rmSync(HELPER_BIN_DIR, { recursive: true, force: true });
});
function pendingProbe() {
	const started = deferred<AbortSignal>();
	const exited = deferred<number>();
	subprocess.mockImplementation(((argv: string[], options: { signal?: AbortSignal }) => {
		expect(argv[1]).toBe("--version");
		if (!options.signal) throw new Error("Probe must receive the shared preparation deadline");
		options.signal.addEventListener("abort", () => exited.resolve(-1), { once: true });
		started.resolve(options.signal);
		return { exited: exited.promise };
	}) as unknown as typeof Bun.spawn);
	return { started: started.promise, complete: exited.resolve };
}
describe("ripgrep preparation operation boundaries", () => {
	for (const mirrorAvailable of [true, false]) {
		test(`selected mirror ${mirrorAvailable ? "serves the native rg binary before probing" : "404 falls back to the same GitHub repository and pinned tag"}`, async () => {
			switchSource();
			useSelectedMirrorRoutes("fork/repo", mirrorAvailable);
			subprocess.mockImplementation((() => ({
				exited: Promise.resolve(0),
			})) as unknown as typeof Bun.spawn);
			const path = await resolver();
			expect(path).not.toBeNull();
			if (!path) throw new Error("Selected-source native preparation must succeed");
			expect(fs.readFileSync(path)).toEqual(nativeBinary);
			const expectedCalls = [
				githubAssetUrl("fork/repo", HELPER_MANIFEST_FILENAME),
				mirrorAssetUrl,
				...(mirrorAvailable ? [] : [githubAssetUrl("fork/repo", nativeAssetName)]),
			];
			expect(calls).toEqual(expectedCalls);
			expect(subprocess).toHaveBeenCalledTimes(1);
			expect(await resolver()).toBe(path);
			expect(calls).toEqual(expectedCalls); // Verified cache is reusable only under the same frozen source.
		});
	}
	test("a proxy edit during the manifest does not recapture a new transport for download", async () => {
		useRoutes(async (url) => {
			if (url.endsWith(".json")) {
				settings.update = {
					...(settings.update ?? DEFAULT_UPDATE_SETTINGS),
					proxy: { mode: "custom", url: "http://user:secret@localhost:8080" },
				};
				return Response.json(catalog());
			}
			return new Response(nativeBinary);
		});
		subprocess.mockImplementation((() => ({
			exited: Promise.resolve(0),
		})) as unknown as typeof Bun.spawn);
		expect(await resolver()).not.toBeNull();
		expect(calls).toHaveLength(2);
		expect(calls.every((url) => url.startsWith("https://github.com/fork/repo/"))).toBe(true);
	});
	test.each([
		["mirror delivery", undefined],
		["canonical repository", "other/repo"],
	] as const)("an awaited GitHub manifest respects a %s change", async (_change, repository) => {
		const response = deferred<Response>();
		const started = deferred<void>();
		useRoutes(async (url) => {
			if (!url.endsWith(".json")) return new Response(nativeBinary);
			started.resolve();
			return response.promise;
		});
		subprocess.mockImplementation((() => ({
			exited: Promise.resolve(0),
		})) as unknown as typeof Bun.spawn);
		const result = resolver();
		await started.promise;
		switchSource(repository);
		response.resolve(Response.json(catalog()));
		const path = await result;
		// Delivery is part of the frozen source identity, even when GitHub authority is unchanged.
		expect(path).toBeNull();
		expect(calls).toHaveLength(1);
		expect(subprocess).not.toHaveBeenCalled();
		expect(
			calls.every((url) =>
				url.startsWith(`https://github.com/fork/repo/releases/download/${HELPER_RELEASE_TAG}/`),
			),
		).toBe(true);
		expect(calls.some((url) => url.includes("personal.example"))).toBe(false);
		// A new preparation may capture the selected source; the stale operation must not recapture it.
		const nextRepository = repository ?? "fork/repo";
		useSelectedMirrorRoutes(nextRepository);
		const freshPath = await resolver();
		expect(freshPath).not.toBeNull();
		if (!freshPath) throw new Error("New-source preparation must be retryable");
		expect(fs.readFileSync(freshPath)).toEqual(nativeBinary);
		expect(calls.slice(1)).toEqual([
			githubAssetUrl(nextRepository, HELPER_MANIFEST_FILENAME),
			mirrorAssetUrl,
		]);
		expect(subprocess).toHaveBeenCalledTimes(1);
	});
	test("an already canceled Grep starts no helper fetch or fallback process", async () => {
		const controller = new AbortController();
		controller.abort(new Error("already canceled Grep"));
		await expect(grep(controller.signal)).rejects.toThrow("already canceled Grep");
		expect(calls).toHaveLength(0);
		expect(subprocess).not.toHaveBeenCalled();
	});
	test("Grep cancellation during its first manifest fetch propagates without fallback", async () => {
		const started = deferred<void>();
		const controller = new AbortController();
		useRoutes(async (_url, init) => {
			if (!init?.signal) throw new Error("Manifest missing cancellation signal");
			started.resolve();
			return new Promise<Response>((_resolve, reject) =>
				init.signal?.addEventListener("abort", () => reject(init.signal?.reason), { once: true }),
			);
		});
		const result = grep(controller.signal);
		await started.promise;
		controller.abort(new Error("cancel helper manifest"));
		await expect(result).rejects.toThrow("cancel helper manifest");
		expect(calls).toHaveLength(1);
		expect(subprocess).not.toHaveBeenCalled();
	});
	test("Grep cancellation during the helper body cancels its reader and never falls back", async () => {
		const started = deferred<void>();
		const controller = new AbortController();
		let canceled = false;
		useRoutes(async (url) => {
			if (url.endsWith(".json")) return Response.json(catalog());
			return new Response(
				new ReadableStream(
					{
						pull() {
							started.resolve();
						},
						cancel() {
							canceled = true;
						},
					},
					{ highWaterMark: 0 },
				),
			);
		});
		const result = grep(controller.signal);
		await started.promise;
		controller.abort(new Error("cancel helper body"));
		await expect(result).rejects.toThrow("cancel helper body");
		expect(canceled).toBe(true);
		expect(subprocess).not.toHaveBeenCalled();
	});
	for (const cached of [false, true]) {
		test(`${cached ? "cached" : "downloaded"} version probe cancellation propagates to Grep instead of not-available`, async () => {
			if (cached) expect(await downloadHelperBinary(spec)).not.toBeNull();
			const controller = new AbortController();
			const probe = pendingProbe();
			const result = grep(controller.signal);
			const probeSignal = await probe.started;
			controller.abort(new Error("cancel version probe"));
			await expect(result).rejects.toThrow("cancel version probe");
			expect(probeSignal.aborted).toBe(true);
			expect(subprocess).toHaveBeenCalledTimes(1);
		});
		test.each([
			["mirror delivery", undefined],
			["canonical repository", "other/repo"],
		] as const)(`${cached ? "cached" : "downloaded"} version probe respects a %s change`, async (_change, repository) => {
			const cachedPath = cached ? await downloadHelperBinary(spec) : null;
			if (cached) expect(cachedPath).not.toBeNull();
			const probe = pendingProbe();
			const result = resolver();
			await probe.started;
			switchSource(repository);
			probe.complete(0);
			const path = await result;
			// A successful version probe cannot make its old delivery context current again.
			expect(path).toBeNull();
			expect(calls).toHaveLength(2);
			expect(
				calls.every((url) =>
					url.startsWith(`https://github.com/fork/repo/releases/download/${HELPER_RELEASE_TAG}/`),
				),
			).toBe(true);
			expect(calls.some((url) => url.includes("personal.example"))).toBe(false);
			expect(subprocess).toHaveBeenCalledTimes(1);
			const nextRepository = repository ?? "fork/repo";
			useSelectedMirrorRoutes(nextRepository);
			subprocess.mockImplementation((() => ({
				exited: Promise.resolve(0),
			})) as unknown as typeof Bun.spawn);
			const freshPath = await resolver();
			expect(freshPath).not.toBeNull();
			if (!freshPath)
				throw new Error("New-source preparation must be retryable after the stale probe");
			expect(fs.readFileSync(freshPath)).toEqual(nativeBinary);
			if (cached) expect(freshPath).not.toBe(cachedPath);
			expect(calls.slice(2)).toEqual([
				githubAssetUrl(nextRepository, HELPER_MANIFEST_FILENAME),
				mirrorAssetUrl,
			]);
			expect(subprocess).toHaveBeenCalledTimes(2);
		});
	}
	test("the original 60s preparation deadline survives cache lookup, download and version probe", async () => {
		const originalTimeout = globalThis.setTimeout;
		const timers: { callback: () => void; timer: ReturnType<typeof setTimeout> }[] = [];
		spyOn(globalThis, "setTimeout").mockImplementation(((
			handler: Parameters<typeof setTimeout>[0],
			milliseconds?: number,
			...args: unknown[]
		) => {
			const timer = originalTimeout(handler, milliseconds, ...args);
			if (milliseconds === 60_000 && typeof handler === "function")
				timers.push({ callback: () => handler(...args), timer });
			return timer;
		}) as typeof setTimeout);
		const cleared = spyOn(globalThis, "clearTimeout");
		const probe = pendingProbe();
		const result = resolver();
		const probeSignal = await probe.started;
		expect(calls).toHaveLength(2); // Manifest plus binary; both phases already completed.
		expect(timers.length).toBeGreaterThan(1);
		expect(cleared.mock.calls.some(([timer]) => timer === timers[0].timer)).toBe(false);
		timers[0].callback();
		await expect(result).rejects.toThrow("Distribution timeout");
		expect(probeSignal.aborted).toBe(true);
		expect(subprocess).toHaveBeenCalledTimes(1);
	});
});
