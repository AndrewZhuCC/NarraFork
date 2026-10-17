import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { chmod, lstat, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { Hono } from "hono";
import puppeteer from "puppeteer-core";
import type * as Fixture from "../../scripts/smoke-editor-raw-fixture";
import type { FixtureAsset } from "../../scripts/smoke-monaco-large-file-bundle";
import { testEnvironment } from "../../tests/preload";
import { db } from "../db";
import { narrators, users } from "../db/schema";
import { generateId } from "../lib/id";
import { getNarraforkHome } from "../lib/narrafork-home";
import { safeSpawn } from "../lib/spawn";
import { getDeviceConnectionGeneration } from "../services/device-connection-service";
import { createRemoteBackend } from "../services/device-remote-backend";
import {
	getEditorDocumentService,
	withEditorOperationStore,
} from "../services/editor-document-runtime";
import { editorHelperAvailable, resolveEditorHelper } from "../services/editor-helper";
import {
	EDITOR_OPERATION_STORE_LIMITS,
	EditorRemoteOperationStore,
} from "../services/editor-remote-operation-store";
import { createEditorDocumentRoutes } from "./editor-documents";
import { nativeEditorFixture } from "./editor-native-fixture";

type RawWindow = Window & { rawFixture: typeof Fixture };
const CHROME =
	process.env.NF_TEST_CHROMIUM_PATH ??
	process.env.PUPPETEER_EXECUTABLE_PATH ??
	"/home/fulcrum/.cache/puppeteer/chrome/linux-146.0.7680.31/chrome-linux64/chrome";
const hash = (bytes: Uint8Array) => createHash("sha256").update(bytes).digest("hex");

test.each([
	"local",
	"remote",
] as const)("Chrome production writer, actual verified binary and authenticated executor WS (%s)", async (device) => {
	const project = resolve(import.meta.dir, "../.."),
		root = await mkdtemp(join(testEnvironment.isolatedHome, "native-browser-"));
	const bundleRoot = await mkdtemp(join(project, ".tmp-narrafork-home.native-browser-"));
	const workspace = join(root, "workspace");
	await mkdir(workspace);
	const target = join(workspace, "twenty.txt");
	const original = Buffer.alloc(20 * 1024 * 1024 - 1, 65);
	// A realistic large source: bounded lines exercise the 100k-line plaintext profile.
	// The separate raw browser suite retains the 20 MiB single-long-line boundary.
	for (let offset = 127; offset < original.length; offset += 128) original[offset] = 10;
	original[0] = 97;
	original[1] = 10;
	await writeFile(target, original);
	await chmod(target, 0o640);
	const initialInode = (await lstat(target)).ino;
	const fixture = await nativeEditorFixture(root, workspace);
	const userId = generateId(),
		narratorId = generateId(),
		now = new Date().toISOString();
	db.insert(users)
		.values({ id: userId, username: userId, passwordHash: "fixture", role: "user", createdAt: now })
		.run();
	db.insert(narrators)
		.values({
			id: narratorId,
			ownerUserId: userId,
			cwd: workspace,
			defaultDeviceId: device === "remote" ? fixture.deviceId : "local",
			visibility: "private",
			createdAt: now,
			updatedAt: now,
		})
		.run();
	let server: ReturnType<typeof Bun.serve> | undefined;
	let browser: Awaited<ReturnType<typeof puppeteer.launch>> | undefined;
	let commits = 0;
	try {
		if (device === "remote") await fixture.start();
		else {
			const artifact = await resolveEditorHelper(AbortSignal.timeout(15_000));
			expect(artifact.sha256).toBe(fixture.binarySha256);
			const end = Date.now() + 15_000;
			while (!editorHelperAvailable()) {
				if (Date.now() > end) throw new Error("Verified native helper feature probe failed");
				await new Promise((resolve) => setTimeout(resolve, 10));
			}
		}
		const service = getEditorDocumentService();
		const nativeStore =
			device === "remote"
				? new EditorRemoteOperationStore(
						join(root, "host-native-receipts"),
						EDITOR_OPERATION_STORE_LIMITS,
						{ root: getNarraforkHome(), stateDir: join(getNarraforkHome(), "editor-native") },
					)
				: undefined;
		const app = new Hono();
		// Same synthetic HTTP identity boundary as existing browser fixtures; WS authentication is real.
		app.use("*", async (c, next) => {
			c.set("user", { sub: userId, role: "user", iat: 1, exp: 9999999999 });
			await next();
		});
		app.route(
			"/api/narrators",
			createEditorDocumentRoutes(() => service),
		);
		const manifestPath = join(bundleRoot, "manifest.json"),
			html =
				'<!doctype html><html><head><meta charset="utf-8"></head><body><div id="root"></div><script type="module" src="/scripts/smoke-editor-raw-fixture.tsx"></script></body></html>';
		const built = await safeSpawn({
			cmd: [
				process.execPath,
				"--eval",
				`import { buildFixtureBundle } from ${JSON.stringify(join(project, "scripts/smoke-monaco-large-file-bundle.ts"))}; const bundle = await buildFixtureBundle(${JSON.stringify(project)}, ${JSON.stringify(bundleRoot)}, "/", ${JSON.stringify(html)}); await Bun.write(${JSON.stringify(manifestPath)}, JSON.stringify({ ...bundle, assets: [...bundle.assets] }));`,
			],
			cwd: project,
			timeout: 60_000,
			maxOutputBytes: 65536,
		});
		if (built.exitCode) throw new Error(`Native Chrome bundle failed: ${built.stderr}`);
		if (Bun.file(manifestPath).size > 1024 * 1024) throw new Error("Fixture manifest budget");
		const data = (await Bun.file(manifestPath).json()) as {
			html: string;
			assets: [string, FixtureAsset][];
		};
		const assets = new Map(data.assets);
		server = Bun.serve({
			hostname: "127.0.0.1",
			port: 0,
			async fetch(request) {
				const path = new URL(request.url).pathname;
				if (path === "/fixture-config")
					return Response.json({
						narratorId,
						deviceId: device === "remote" ? fixture.deviceId : "local",
					});
				if (path.startsWith("/api/")) {
					if (path.endsWith("/commit")) commits++;
					return nativeStore
						? withEditorOperationStore(nativeStore, () => app.fetch(request))
						: app.fetch(request);
				}
				if (path === "/")
					return new Response(data.html, { headers: { "Content-Type": "text/html" } });
				const asset = assets.get(path);
				return asset
					? new Response(Bun.file(asset.path), { headers: { "Content-Type": asset.mime } })
					: new Response("not found", { status: 404 });
			},
		});
		browser = await puppeteer.launch({
			executablePath: CHROME,
			headless: true,
			args: ["--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage"],
			timeout: 15_000,
		});
		const page = await browser.newPage();
		await page.goto(`http://127.0.0.1:${server.port}`, { waitUntil: "domcontentloaded" });
		await page.waitForFunction(() => !!(window as unknown as RawWindow).rawFixture, {
			timeout: 30_000,
		});
		const opened = await page.evaluate(
			(path) => (window as unknown as RawWindow).rawFixture.open(path),
			target,
		);
		expect(opened.session?.capabilities).toMatchObject({
			stage: true,
			durableApply: true,
			durableQuery: true,
		});
		await page.evaluate(() => {
			const fixture = (window as unknown as RawWindow).rawFixture;
			fixture.dualViews();
			fixture.edit("Z");
		});
		const saved = await page.evaluate(() => (window as unknown as RawWindow).rawFixture.saveRaw());
		expect(saved.session?.phase).toBe("idle");
		expect(saved.dirty).toBe(false);
		const expected = Buffer.concat([Buffer.from("Z"), original]);
		expect(hash(await readFile(target))).toBe(hash(expected));
		expect((await lstat(target)).ino).not.toBe(initialInode);
		expect((await lstat(target)).mode & 0o777).toBe(0o640);
		expect(commits).toBe(1);
		if (device === "remote") {
			const before = getDeviceConnectionGeneration(fixture.deviceId);
			if (before === null) throw new Error("Fixture device disconnected unexpectedly");
			const stale = createRemoteBackend(fixture.deviceId, {
				connectionGeneration: before,
				defaultCwd: workspace,
				platform: { os: "linux", arch: "amd64" },
			});
			// A one-byte replacement keeps the same 20 MiB budget for another actual save.
			await page.evaluate(() =>
				(window as unknown as RawWindow).rawFixture.rawBatch([
					{
						range: { startLineNumber: 1, startColumn: 1, endLineNumber: 1, endColumn: 2 },
						text: "Y",
					},
				]),
			);
			fixture.dropSuccess();
			const unknown = await page.evaluate(() =>
				(window as unknown as RawWindow).rawFixture.saveRaw(),
			);
			expect(unknown.session?.phase).toBe("unknown");
			expect(fixture.applies).toBe(2);
			await fixture.reconnect();
			expect(getDeviceConnectionGeneration(fixture.deviceId)).not.toBe(before);
			expect(stale.supportsEditorDurableWrite).toBe(false);
			const reconciled = await page.evaluate(() =>
				(window as unknown as RawWindow).rawFixture.reconcileRaw(),
			);
			expect(reconciled.session?.phase).toBe("idle");
			expect(reconciled.dirty).toBe(false);
			expect(fixture.applies).toBe(2);
			expect(commits).toBe(2);
			await fixture.restart();
			await page.evaluate(() => (window as unknown as RawWindow).rawFixture.close());
			await page.evaluate((path) => (window as unknown as RawWindow).rawFixture.open(path), target);
			await page.evaluate(() =>
				(window as unknown as RawWindow).rawFixture.rawBatch([
					{
						range: { startLineNumber: 1, startColumn: 1, endLineNumber: 1, endColumn: 2 },
						text: "X",
					},
				]),
			);
			const nextSave = await page.evaluate(() =>
				(window as unknown as RawWindow).rawFixture.saveRaw(),
			);
			expect(nextSave.session?.phase).toBe("idle");
			expect(nextSave.dirty).toBe(false);
			expect(fixture.applies).toBe(3);
			expect(commits).toBe(3);
			await fixture.reconnect(true);
			await page.evaluate(() => (window as unknown as RawWindow).rawFixture.close());
			const readonly = await page.evaluate(
				(path) => (window as unknown as RawWindow).rawFixture.open(path),
				target,
			);
			expect(readonly.session?.capabilities).toMatchObject({
				stage: false,
				durableApply: false,
				durableQuery: false,
				writeUnavailableReason: "durable_executor_unavailable",
			});
		}
		console.info(
			"EDITOR_NATIVE_CHROMIUM",
			JSON.stringify({
				device,
				binarySha256: fixture.binarySha256,
				bytes: expected.length,
				commits,
				actualExecutorApplies: fixture.applies,
				atomicMode: (await lstat(target)).mode & 0o777,
			}),
		);
		await page.evaluate(() => (window as unknown as RawWindow).rawFixture.close());
	} finally {
		await browser?.close();
		await server?.stop(true);
		await fixture.dispose();
		await rm(root, { recursive: true, force: true });
		await rm(bundleRoot, { recursive: true, force: true });
	}
}, 240_000);
