import { afterAll, afterEach, beforeAll, beforeEach, expect, mock, spyOn, test } from "bun:test";
import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { lstat, mkdir, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { eq } from "drizzle-orm";
import { db } from "../../../server/db";
import { remoteDevices } from "../../../server/db/schema";
import { localBackend } from "../../../server/lib/agent/execution/local-backend";
import { encodeChunkFrame } from "../../../server/lib/agent/execution/rpc-types";
import { settings } from "../../../server/lib/settings";
import type { setChunkFrameHandler } from "../../../server/services/device-connection-service";
import type { FileWriteOptions } from "../../../server/services/file-write-queue";
import type { FileHistoryTarget } from "../../../shared/file-change-protocol";

const CONNECTION_MODULE = "../../../server/services/device-connection-service";
const QUEUE_MODULE = "../../../server/services/file-write-queue";
const realConnection = { ...(await import(CONNECTION_MODULE)) };
const realQueue = { ...(await import(QUEUE_MODULE)) };
const DEVICE_ID = "download-commit-test";
const CONTENT = Buffer.from("completed download");
let receiveChunk: Parameters<typeof setChunkFrameHandler>[0] = null;
let completeDownloads = true;
let beginError: Error | null = null;
let admissions: FileHistoryTarget[] = [];
type RpcOptions = { signal?: AbortSignal; timeoutMs?: number };
let rpcCalls: {
	deviceId: string;
	method: string;
	params: Record<string, unknown>;
	options?: RpcOptions;
}[] = [];
let abortWait: Promise<void> | null = null;
let pending: Promise<unknown>[] = [];
let controllers: AbortController[] = [];
let releaseWriters: (() => void)[] = [];
let fileRoot: string;

function track<T>(work: Promise<T>): Promise<T> {
	pending.push(work.catch(() => {}));
	return work;
}

// Observe real admission synchronously after the shared queue has enqueued the
// commit. The queue itself is never mocked; local Write uses this same instance.
mock.module(QUEUE_MODULE, () => ({
	...realQueue,
	withFileWrite<T>(
		target: FileHistoryTarget,
		body: () => T | Promise<T>,
		options?: FileWriteOptions,
	) {
		const work = realQueue.withFileWrite(target, body, options);
		admissions.push(target);
		return work;
	},
}));
mock.module(CONNECTION_MODULE, () => ({
	...realConnection,
	isDeviceOnline: () => true,
	getConnectedDeviceHello: () => ({ platform: { os: "linux", arch: "x64" } }),
	hasDeviceProtocolFeature: () => true,
	deviceBufferedAmount: () => 0,
	sendChunkFrame: () => true,
	setChunkFrameHandler: (handler: typeof receiveChunk) => {
		receiveChunk = handler;
	},
	sendRpc: async (
		deviceId: string,
		method: string,
		params: Record<string, unknown>,
		options?: RpcOptions,
	) => {
		rpcCalls.push({ deviceId, method, params, options });
		if (method === "transfer.abort") await abortWait;
		if (method === "transfer.stat") {
			return {
				exists: true,
				isDirectory: false,
				size: CONTENT.byteLength,
				mtimeMs: 1_700_000_000_000,
			};
		}
		if (method === "transfer.begin") {
			if (beginError) throw beginError;
			if (completeDownloads) {
				if (!receiveChunk) throw new Error("download receiver was not registered");
				track(
					Promise.resolve(
						receiveChunk(
							deviceId,
							encodeChunkFrame({ transferId: String(params.transferId), chunkIndex: 0 }, CONTENT),
						),
					),
				);
			}
			return { completedChunks: [] };
		}
		return { ok: true };
	},
}));

const service = await import("../../../server/services/device-transfer-service");
const { downloadFile, initDeviceTransferService, TRANSFER_CANCELLED_ABORT_REASON } = service;

beforeAll(() => initDeviceTransferService());
beforeEach(() => {
	fileRoot = mkdtempSync(join(tmpdir(), "narrafork-download-commit-"));
	completeDownloads = true;
	beginError = null;
	abortWait = null;
	admissions = [];
	rpcCalls = [];
	pending = [];
	controllers = [];
	releaseWriters = [];
});
afterEach(async () => {
	for (const controller of controllers) controller.abort(TRANSFER_CANCELLED_ABORT_REASON);
	for (const release of releaseWriters) release();
	await Promise.all(pending);
	rmSync(fileRoot, { recursive: true, force: true });
});
afterAll(() => {
	mock.module(CONNECTION_MODULE, () => realConnection);
	mock.module(QUEUE_MODULE, () => realQueue);
	mock.restore();
});

function gate() {
	let release!: () => void;
	const promise = new Promise<void>((resolve) => {
		release = resolve;
	});
	return { promise, release };
}

async function targetFor(path: string): Promise<FileHistoryTarget> {
	const target = await localBackend.resolvePathIdentity(path);
	return {
		deviceId: localBackend.deviceId,
		pathFlavor: localBackend.pathFlavor as "posix" | "windows",
		canonicalPath: target.canonicalPath,
	};
}

async function holdWriter(path: string) {
	const target = await targetFor(path);
	const started = gate();
	const held = gate();
	releaseWriters.push(held.release);
	const work = track(
		realQueue.withFileWrite(target, async () => {
			await writeFile(target.canonicalPath, "writer before");
			started.release();
			await held.promise;
			await writeFile(target.canonicalPath, "writer completed");
		}),
	);
	await started.promise;
	return { target, work, release: held.release };
}

function startDownload(
	destination: string,
	options: { deviceId?: string; remoteSize?: number } = {},
) {
	const controller = new AbortController();
	controllers.push(controller);
	const work = track(
		downloadFile({
			deviceId: options.deviceId ?? DEVICE_ID,
			remotePath: "/remote/source.bin",
			localDest: destination,
			remoteSize: options.remoteSize ?? CONTENT.byteLength,
			remoteMtimeMs: 1_700_000_000_000,
			remotePlatformOs: "linux",
			signal: controller.signal,
		}),
	);
	return { controller, work };
}

async function waitFor(predicate: () => boolean | Promise<boolean>) {
	const deadline = Date.now() + 2_000;
	while (!(await predicate())) {
		if (Date.now() >= deadline) throw new Error("Timed out waiting for download commit admission");
		await new Promise<void>((resolve) => setImmediate(resolve));
	}
}

function admitted(target: FileHistoryTarget) {
	return admissions.some(
		(entry) =>
			entry.deviceId === target.deviceId &&
			localBackend.paths.equals(entry.canonicalPath, target.canonicalPath),
	);
}

async function directoryAlias(target: string, alias: string) {
	await symlink(target, alias, process.platform === "win32" ? "junction" : "dir");
}

test("download commit waits for the canonical local Write path while other destinations complete", async () => {
	const real = join(fileRoot, "real");
	const alias = join(fileRoot, "alias");
	await mkdir(real);
	await directoryAlias(real, alias);
	const destination = join(alias, "download.bin");
	const writer = await holdWriter(join(real, "download.bin"));
	const download = startDownload(destination);
	let settled = false;
	track(
		download.work.finally(() => {
			settled = true;
		}),
	);
	await waitFor(() => admitted(writer.target));
	expect(await readFile(writer.target.canonicalPath, "utf8")).toBe("writer before");
	expect(await readFile(`${destination}.nfpart`, "utf8")).toBe(CONTENT.toString());
	expect(settled).toBe(false);
	const competing = startDownload(writer.target.canonicalPath, { deviceId: "second-device" });
	await expect(competing.work).rejects.toThrow("Download checkpoint is busy");
	expect(await readFile(`${destination}.nfpart`, "utf8")).toBe(CONTENT.toString());

	const otherPath = join(fileRoot, "independent.bin");
	const other = startDownload(otherPath);
	await expect(other.work).resolves.toMatchObject({ bytes: CONTENT.byteLength });
	expect(await readFile(otherPath, "utf8")).toBe(CONTENT.toString());
	expect(settled).toBe(false);

	writer.release();
	await writer.work;
	await expect(download.work).resolves.toMatchObject({ bytes: CONTENT.byteLength });
	expect(await readFile(writer.target.canonicalPath, "utf8")).toBe(CONTENT.toString());
	expect(existsSync(`${destination}.nfpart`)).toBe(false);
});

test("network receive and temporary-file writes leave the destination queue available", async () => {
	completeDownloads = false;
	const destination = join(fileRoot, "streaming.bin");
	const download = startDownload(destination);
	await waitFor(() => rpcCalls.some((call) => call.method === "transfer.begin"));
	expect(existsSync(`${destination}.nfpart`)).toBe(true);
	expect(admissions).toHaveLength(0);
	await realQueue.withFileWrite(
		await targetFor(destination),
		() => writeFile(destination, "unblocked during network receive"),
		{ waitTimeoutMs: 0 },
	);
	download.controller.abort(TRANSFER_CANCELLED_ABORT_REASON);
	await expect(download.work).rejects.toThrow("transfer cancelled");
	expect(await readFile(destination, "utf8")).toBe("unblocked during network receive");
});

for (const mode of ["cancel", "pause"] as const) {
	test(`queued download ${mode} preserves the established partial-file semantics`, async () => {
		const destination = join(fileRoot, `${mode}.bin`);
		const writer = await holdWriter(destination);
		const download = startDownload(destination);
		await waitFor(() => admitted(writer.target));
		download.controller.abort(mode === "cancel" ? TRANSFER_CANCELLED_ABORT_REASON : undefined);
		await expect(download.work).rejects.toThrow(
			mode === "cancel" ? "transfer cancelled" : "transfer paused",
		);
		expect(await readFile(destination, "utf8")).toBe("writer before");
		expect(existsSync(`${destination}.nfpart`)).toBe(mode === "pause");
		expect(existsSync(`${destination}.nfmeta`)).toBe(mode === "pause");
		expect(rpcCalls.find((call) => call.method === "transfer.abort")?.params.preservePartial).toBe(
			mode === "pause",
		);
		if (mode === "pause") {
			expect(JSON.parse(await readFile(`${destination}.nfmeta`, "utf8"))).toMatchObject({
				fileSize: CONTENT.byteLength,
				completedChunks: [0],
			});
		}
		writer.release();
		await writer.work;
		await realQueue.withFileWrite(writer.target, () => writeFile(destination, "fresh write"), {
			waitTimeoutMs: 0,
		});
		expect(await readFile(destination, "utf8")).toBe("fresh write");
		const next = startDownload(destination, { deviceId: "after-stop-device" });
		await expect(next.work).resolves.toMatchObject({ bytes: CONTENT.byteLength });
		expect(await readFile(destination, "utf8")).toBe(CONTENT.toString());
	});
}

test("different devices cannot share an active download checkpoint or keep an FD to its committed file", async () => {
	completeDownloads = false;
	const real = join(fileRoot, "real");
	const alias = join(fileRoot, "alias");
	await mkdir(real);
	await directoryAlias(real, alias);
	const destination = join(real, "shared.bin");
	const chunkBytes = settings.devices?.transferChunkBytes ?? 1024 * 1024;
	const firstPayload = Buffer.alloc(chunkBytes, 0x41);
	const first = startDownload(destination, { remoteSize: chunkBytes + CONTENT.byteLength });
	await waitFor(() => rpcCalls.some((call) => call.method === "transfer.begin"));
	const firstBegin = rpcCalls.find((call) => call.method === "transfer.begin");
	if (!receiveChunk || !firstBegin) throw new Error("missing download receiver");
	const transferId = String(firstBegin.params.transferId);
	await receiveChunk(DEVICE_ID, encodeChunkFrame({ transferId, chunkIndex: 0 }, firstPayload));

	completeDownloads = true;
	const second = startDownload(join(alias, "shared.bin"), { deviceId: "second-device" });
	await expect(second.work).rejects.toThrow("Download checkpoint is busy");
	expect(
		rpcCalls.some((call) => call.deviceId === "second-device" && call.method === "transfer.begin"),
	).toBe(false);
	expect(await readFile(`${destination}.nfpart`)).toEqual(firstPayload);
	expect(existsSync(destination)).toBe(false);

	await receiveChunk(DEVICE_ID, encodeChunkFrame({ transferId, chunkIndex: 1 }, CONTENT));
	await expect(first.work).resolves.toMatchObject({
		bytes: firstPayload.byteLength + CONTENT.byteLength,
	});
	expect(await readFile(destination)).toEqual(Buffer.concat([firstPayload, CONTENT]));
	const fresh = startDownload(destination, { deviceId: "second-device" });
	await expect(fresh.work).resolves.toMatchObject({ bytes: CONTENT.byteLength });
	expect(await readFile(destination)).toEqual(CONTENT);
});

for (const stage of ["open", "begin", "commit"] as const) {
	test(`download checkpoint ownership is released after ${stage} failure`, async () => {
		const destination = join(fileRoot, `${stage}.bin`);
		if (stage === "open") await mkdir(`${destination}.nfpart`);
		if (stage === "begin") beginError = new Error("injected begin failure");
		if (stage === "commit") await mkdir(destination);
		const failed = startDownload(destination);
		await expect(failed.work).rejects.toThrow();
		if (stage === "open") await rm(`${destination}.nfpart`, { recursive: true });
		if (stage === "commit") await rm(destination, { recursive: true });
		beginError = null;
		const next = startDownload(destination, { deviceId: "after-failure-device" });
		await expect(next.work).resolves.toMatchObject({ bytes: CONTENT.byteLength });
		expect(await readFile(destination)).toEqual(CONTENT);
	});
}

test("cancelling waits for an in-flight chunk manifest before releasing its checkpoint", async () => {
	const devices = settings.devices;
	if (!devices) throw new Error("missing transfer settings");
	const oldChunkBytes = devices.transferChunkBytes;
	devices.transferChunkBytes = 4;
	completeDownloads = false;
	const destination = join(fileRoot, "draining.bin");
	const finishManifest = gate();
	releaseWriters.push(finishManifest.release);
	let manifestStarted = false;
	const originalWrite = Bun.write;
	const write = spyOn(Bun, "write").mockImplementation((async (
		...args: Parameters<typeof Bun.write>
	) => {
		if (args[0] === `${destination}.nfmeta` && !manifestStarted) {
			manifestStarted = true;
			await finishManifest.promise;
		}
		return originalWrite(...args);
	}) as typeof Bun.write);
	try {
		const download = startDownload(destination, { remoteSize: 33 * 4 });
		await waitFor(() => rpcCalls.some((call) => call.method === "transfer.begin"));
		const begin = rpcCalls.find((call) => call.method === "transfer.begin");
		if (!receiveChunk || !begin) throw new Error("missing download receiver");
		const transferId = String(begin.params.transferId);
		for (let index = 0; index < 31; index++) {
			await receiveChunk(
				DEVICE_ID,
				encodeChunkFrame({ transferId, chunkIndex: index }, Buffer.from("data")),
			);
		}
		const lastChunk = track(
			receiveChunk(
				DEVICE_ID,
				encodeChunkFrame({ transferId, chunkIndex: 31 }, Buffer.from("data")),
			),
		);
		await waitFor(() => manifestStarted);
		download.controller.abort(TRANSFER_CANCELLED_ABORT_REASON);
		await waitFor(() => rpcCalls.some((call) => call.method === "transfer.abort"));
		const competing = startDownload(destination, { deviceId: "while-draining-device" });
		await expect(competing.work).rejects.toThrow("Download checkpoint is busy");
		finishManifest.release();
		await lastChunk;
		await expect(download.work).rejects.toThrow("transfer cancelled");
		expect(existsSync(`${destination}.nfpart`)).toBe(false);
		expect(existsSync(`${destination}.nfmeta`)).toBe(false);
		devices.transferChunkBytes = oldChunkBytes;
		completeDownloads = true;
		await expect(
			startDownload(destination, { deviceId: "after-drain-device" }).work,
		).resolves.toMatchObject({ bytes: CONTENT.byteLength });
	} finally {
		finishManifest.release();
		write.mockRestore();
		devices.transferChunkBytes = oldChunkBytes;
	}
});

test("late callbacks from an old download cannot release a newer checkpoint owner", async () => {
	const destination = join(fileRoot, "late-owner.bin");
	const first = startDownload(destination);
	const addAbortListener = spyOn(first.controller.signal, "addEventListener");
	let oldAbort: EventListenerOrEventListenerObject | null | undefined;
	try {
		await first.work;
		oldAbort = addAbortListener.mock.calls.find(([kind]) => kind === "abort")?.[1];
	} finally {
		addAbortListener.mockRestore();
	}
	const oldId = String(
		rpcCalls.find((call) => call.method === "transfer.begin")?.params.transferId,
	);
	completeDownloads = false;
	const second = startDownload(destination, { deviceId: "new-owner-device" });
	await waitFor(() =>
		rpcCalls.some(
			(call) => call.deviceId === "new-owner-device" && call.method === "transfer.begin",
		),
	);
	first.controller.abort(TRANSFER_CANCELLED_ABORT_REASON);
	if (typeof oldAbort !== "function" || !receiveChunk)
		throw new Error("old receive callbacks were not captured");
	oldAbort.call(first.controller.signal, new Event("abort"));
	await receiveChunk(
		DEVICE_ID,
		encodeChunkFrame({ transferId: oldId, chunkIndex: 0 }, Buffer.from("late")),
	);
	expect(existsSync(`${destination}.nfpart`)).toBe(true);
	const third = startDownload(destination, { deviceId: "third-device" });
	await expect(third.work).rejects.toThrow("Download checkpoint is busy");
	second.controller.abort(TRANSFER_CANCELLED_ABORT_REASON);
	await expect(second.work).rejects.toThrow("transfer cancelled");
	completeDownloads = true;
	await expect(
		startDownload(destination, { deviceId: "third-device" }).work,
	).resolves.toMatchObject({ bytes: CONTENT.byteLength });
});

test("a paused on-disk checkpoint carries no persistent ownership ban", async () => {
	const destination = join(fileRoot, "resumed.bin");
	await writeFile(`${destination}.nfpart`, CONTENT);
	await writeFile(
		`${destination}.nfmeta`,
		JSON.stringify({
			chunkSize: settings.devices?.transferChunkBytes ?? 1024 * 1024,
			fileSize: CONTENT.byteLength,
			mtimeMs: 1_700_000_000_000,
			completedChunks: [0],
		}),
	);
	const resumed = startDownload(destination);
	await expect(resumed.work).resolves.toMatchObject({ bytes: CONTENT.byteLength });
	expect(await readFile(destination)).toEqual(CONTENT);
});

test("queued download rejects a redirected parent instead of committing into another file", async () => {
	const real = join(fileRoot, "real");
	const other = join(fileRoot, "other");
	const alias = join(fileRoot, "alias");
	await mkdir(real);
	await mkdir(other);
	await directoryAlias(real, alias);
	const writer = await holdWriter(join(real, "download.bin"));
	const otherPath = join(other, "download.bin");
	await writeFile(otherPath, "untouched");
	const download = startDownload(join(alias, "download.bin"));
	await waitFor(() => admitted(writer.target));
	await rm(alias, { force: true });
	await directoryAlias(other, alias);
	writer.release();
	await writer.work;
	await expect(download.work).rejects.toThrow("destination changed while waiting");
	expect(await readFile(writer.target.canonicalPath, "utf8")).toBe("writer completed");
	expect(await readFile(otherPath, "utf8")).toBe("untouched");
});

test("download rename replaces a final symlink without following its referent", async () => {
	const referent = join(fileRoot, "referent.bin");
	const destination = join(fileRoot, "link.bin");
	const writer = await holdWriter(referent);
	await symlink(referent, destination, "file");
	const download = startDownload(destination);
	await expect(download.work).resolves.toMatchObject({ bytes: CONTENT.byteLength });
	expect((await lstat(destination)).isSymbolicLink()).toBe(false);
	expect(await readFile(destination, "utf8")).toBe(CONTENT.toString());
	expect(await readFile(referent, "utf8")).toBe("writer before");
	writer.release();
	await writer.work;
});

let reloadIndex = 0;
async function reloadTransferService(): Promise<typeof service> {
	const reloaded = await import(
		`../../../server/services/device-transfer-service.ts?reload-transfer=${++reloadIndex}`
	);
	expect(reloaded.downloadFile).not.toBe(service.downloadFile);
	reloaded.initDeviceTransferService();
	await reloaded.recoverInterruptedTransferTasks();
	return reloaded;
}

test("reloaded frame handler completes an existing receiver without losing its checkpoint", async () => {
	completeDownloads = false;
	const destination = join(fileRoot, "hot-complete.bin");
	const first = startDownload(destination);
	await waitFor(() => rpcCalls.some((call) => call.method === "transfer.begin"));
	const oldHandler = receiveChunk;
	const transferId = String(
		rpcCalls.find((call) => call.method === "transfer.begin")?.params.transferId,
	);
	const reloaded = await reloadTransferService();
	expect(receiveChunk).not.toBe(oldHandler);
	await expect(
		reloaded.downloadFile({
			deviceId: "hot-competitor",
			remotePath: "/remote/source.bin",
			localDest: destination,
			remoteSize: CONTENT.byteLength,
			remoteMtimeMs: 1_700_000_000_000,
			remotePlatformOs: "linux",
		}),
	).rejects.toThrow("Download checkpoint is busy");
	if (!receiveChunk) throw new Error("missing reloaded receiver");
	await receiveChunk(DEVICE_ID, encodeChunkFrame({ transferId, chunkIndex: 0 }, CONTENT));
	await expect(first.work).resolves.toMatchObject({ bytes: CONTENT.byteLength });
	expect(await readFile(destination)).toEqual(CONTENT);
	completeDownloads = true;
	await expect(startDownload(destination).work).resolves.toMatchObject({
		bytes: CONTENT.byteLength,
	});
});

test("reload preserves cancellation and late old callbacks cannot affect a retried receiver", async () => {
	completeDownloads = false;
	const destination = join(fileRoot, "hot-cancel.bin");
	const first = startDownload(destination);
	await waitFor(() => rpcCalls.some((call) => call.method === "transfer.begin"));
	const oldHandler = receiveChunk;
	const oldId = String(
		rpcCalls.find((call) => call.method === "transfer.begin")?.params.transferId,
	);
	await reloadTransferService();
	first.controller.abort(TRANSFER_CANCELLED_ABORT_REASON);
	await expect(first.work).rejects.toThrow("transfer cancelled");
	expect(existsSync(`${destination}.nfpart`)).toBe(false);
	const next = startDownload(destination, { deviceId: "hot-next" });
	await waitFor(() =>
		rpcCalls.some((call) => call.deviceId === "hot-next" && call.method === "transfer.begin"),
	);
	const nextId = String(
		rpcCalls.find((call) => call.deviceId === "hot-next" && call.method === "transfer.begin")
			?.params.transferId,
	);
	if (!oldHandler || !receiveChunk) throw new Error("missing captured handlers");
	await oldHandler(
		DEVICE_ID,
		encodeChunkFrame({ transferId: oldId, chunkIndex: 0 }, Buffer.from("late")),
	);
	await expect(startDownload(destination, { deviceId: "hot-third" }).work).rejects.toThrow(
		"Download checkpoint is busy",
	);
	await receiveChunk("hot-next", encodeChunkFrame({ transferId: nextId, chunkIndex: 0 }, CONTENT));
	await expect(next.work).resolves.toMatchObject({ bytes: CONTENT.byteLength });
	expect(await readFile(destination)).toEqual(CONTENT);
});

for (const action of ["pause", "cancel", "cancelById"] as const) {
	test(`reloaded task ${action} reaches the original run without recovering it as interrupted`, async () => {
		completeDownloads = false;
		const destination = join(fileRoot, `hot-task-${action}.bin`);
		const deviceId = `hot-task-${action}`;
		const now = new Date().toISOString();
		await db.insert(remoteDevices).values({
			id: deviceId,
			name: deviceId,
			slug: deviceId,
			tokenHash: "test",
			tokenPrefix: "test",
			createdBy: "test",
			createdAt: now,
			updatedAt: now,
		});
		const task = await service.startDeviceTransferTask({
			deviceId,
			direction: "download",
			remotePath: "/remote/source.bin",
			localPath: destination,
		});
		try {
			await waitFor(() =>
				rpcCalls.some((call) => call.deviceId === deviceId && call.method === "transfer.begin"),
			);
			const reloaded = await reloadTransferService();
			expect((await reloaded.getDeviceTransferTask(deviceId, task.id))?.status).toBe("running");
			if (action === "pause") await reloaded.pauseDeviceTransferTask(deviceId, task.id);
			else if (action === "cancel") await reloaded.cancelDeviceTransferTask(deviceId, task.id);
			else await reloaded.cancelDeviceTransferTaskById(task.id);
			await waitFor(() =>
				rpcCalls.some((call) => call.deviceId === deviceId && call.method === "transfer.abort"),
			);
			expect(
				rpcCalls.find((call) => call.deviceId === deviceId && call.method === "transfer.abort")
					?.params.preservePartial,
			).toBe(action === "pause");
			if (action === "pause") {
				completeDownloads = true;
				await reloaded.resumeDeviceTransferTask(deviceId, task.id);
				await waitFor(
					async () =>
						(await reloaded.getDeviceTransferTask(deviceId, task.id))?.status === "completed",
				);
				expect(await readFile(destination)).toEqual(CONTENT);
			} else {
				await waitFor(
					() => !existsSync(`${destination}.nfpart`) && !existsSync(`${destination}.nfmeta`),
				);
				expect((await reloaded.getDeviceTransferTask(deviceId, task.id))?.status).toBe("cancelled");
			}
		} finally {
			await service.cancelDeviceTransferTask(deviceId, task.id);
			await db.delete(remoteDevices).where(eq(remoteDevices.id, deviceId));
		}
	});
}

function idleClock(budget = 1_000) {
	const previous = settings.devices;
	if (!previous) throw new Error("missing device settings");
	settings.devices = { ...previous, rpcTimeoutMs: budget, transferChunkBytes: 4 };
	const clock = spyOn(performance, "now").mockReturnValue(0);
	const intervals = spyOn(globalThis, "setInterval");
	return {
		set: (now: number) => clock.mockReturnValue(now),
		tick() {
			const callback = intervals.mock.calls.find(([, delay]) => delay === 200)?.[0];
			if (typeof callback !== "function") throw new Error("receive ACK timer was not registered");
			callback();
		},
		restore() {
			clock.mockRestore();
			intervals.mockRestore();
			settings.devices = previous;
		},
	};
}

async function sendTestChunk(transferId: string, chunkIndex: number, content = "data") {
	if (!receiveChunk) throw new Error("missing receiver");
	await receiveChunk(DEVICE_ID, encodeChunkFrame({ transferId, chunkIndex }, Buffer.from(content)));
}
async function waitForBegin(deviceId = DEVICE_ID) {
	await waitFor(() =>
		rpcCalls.some((call) => call.deviceId === deviceId && call.method === "transfer.begin"),
	);
	return String(
		rpcCalls.find((call) => call.deviceId === deviceId && call.method === "transfer.begin")?.params
			.transferId,
	);
}

for (const chunksBeforeFailure of [0, 1]) {
	test(`a sender stopping after ${chunksBeforeFailure} chunks times out across reload and frees its target`, async () => {
		completeDownloads = false;
		const destination = join(fileRoot, `sender-failure-${chunksBeforeFailure}.bin`);
		const clock = idleClock();
		const remoteAbort = gate();
		releaseWriters.push(remoteAbort.release);
		abortWait = remoteAbort.promise;
		try {
			const failed = startDownload(destination, { remoteSize: 8 });
			const transferId = await waitForBegin();
			if (chunksBeforeFailure) await sendTestChunk(transferId, 0);
			await reloadTransferService();
			clock.set(1_000);
			clock.tick();
			await expect(failed.work).rejects.toThrow("no progress for 1000ms");
			expect(existsSync(destination)).toBe(false);
			const abort = rpcCalls.find((call) => call.method === "transfer.abort");
			expect(abort?.params.transferId).toBe(transferId);
			expect(abort?.options?.timeoutMs).toBe(1_000);
			expect(abort?.options?.signal).toBeUndefined();
			// The remote abort is deliberately unresolved. Only real local IO may
			// retain this checkpoint, not the remote's acknowledgement.
			clock.restore();
			completeDownloads = true;
			await expect(startDownload(destination).work).resolves.toMatchObject({
				bytes: CONTENT.byteLength,
			});
			expect(await readFile(destination)).toEqual(CONTENT);
		} finally {
			remoteAbort.release();
			clock.restore();
		}
	});
}

test("new valid chunks renew the idle budget without imposing a total transfer deadline", async () => {
	completeDownloads = false;
	const destination = join(fileRoot, "continuous.bin");
	const clock = idleClock();
	try {
		const download = startDownload(destination, { remoteSize: 12 });
		const transferId = await waitForBegin();
		for (let index = 0; index < 3; index++) {
			clock.set((index + 1) * 900);
			clock.tick();
			expect(rpcCalls.some((call) => call.method === "transfer.abort")).toBe(false);
			await sendTestChunk(transferId, index);
		}
		await expect(download.work).resolves.toMatchObject({ bytes: 12 });
		expect(await readFile(destination, "utf8")).toBe("datadatadata");
	} finally {
		clock.restore();
	}
});

test("duplicate, malformed and out-of-range chunks cannot renew an idle receiver", async () => {
	completeDownloads = false;
	const destination = join(fileRoot, "invalid-activity.bin");
	const clock = idleClock();
	try {
		const download = startDownload(destination, { remoteSize: 12 });
		const transferId = await waitForBegin();
		await sendTestChunk(transferId, 0);
		clock.set(900);
		await sendTestChunk(transferId, 0, "late");
		await sendTestChunk(transferId, -1);
		await sendTestChunk(transferId, 3);
		await sendTestChunk(transferId, 0.5);
		await sendTestChunk(transferId, 1, "x");
		if (!receiveChunk) throw new Error("missing receiver");
		await receiveChunk(DEVICE_ID, new Uint8Array([0]));
		clock.set(1_000);
		clock.tick();
		await expect(download.work).rejects.toThrow("no progress");
		expect(await readFile(`${destination}.nfpart`, "utf8")).toBe("data");
	} finally {
		clock.restore();
	}
});

test("idle failure keeps its checkpoint until a real in-flight manifest write finishes", async () => {
	completeDownloads = false;
	const destination = join(fileRoot, "idle-draining.bin");
	const clock = idleClock();
	const finishManifest = gate();
	releaseWriters.push(finishManifest.release);
	let manifestStarted = false;
	const originalWrite = Bun.write;
	const write = spyOn(Bun, "write").mockImplementation((async (
		...args: Parameters<typeof Bun.write>
	) => {
		if (args[0] === `${destination}.nfmeta` && !manifestStarted) {
			manifestStarted = true;
			await finishManifest.promise;
		}
		return originalWrite(...args);
	}) as typeof Bun.write);
	try {
		const download = startDownload(destination, { remoteSize: 33 * 4 });
		const transferId = await waitForBegin();
		for (let index = 0; index < 31; index++) await sendTestChunk(transferId, index);
		const heldChunk = track(sendTestChunk(transferId, 31));
		await waitFor(() => manifestStarted);
		let settled = false;
		track(
			download.work.finally(() => {
				settled = true;
			}),
		).catch(() => {});
		clock.set(1_000);
		clock.tick();
		await waitFor(() => rpcCalls.some((call) => call.method === "transfer.abort"));
		expect(settled).toBe(false);
		await expect(startDownload(destination, { deviceId: "idle-competing" }).work).rejects.toThrow(
			"Download checkpoint is busy",
		);
		finishManifest.release();
		await heldChunk;
		await expect(download.work).rejects.toThrow("no progress");
		clock.restore();
		completeDownloads = true;
		await expect(startDownload(destination).work).resolves.toMatchObject({
			bytes: CONTENT.byteLength,
		});
	} finally {
		finishManifest.release();
		write.mockRestore();
		clock.restore();
	}
});

for (const budget of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
	test(`invalid idle budget ${budget} falls back to a finite 120 seconds`, async () => {
		completeDownloads = false;
		const clock = idleClock(budget);
		try {
			const download = startDownload(join(fileRoot, "invalid-budget.bin"));
			await waitForBegin();
			clock.set(119_999);
			clock.tick();
			expect(rpcCalls.some((call) => call.method === "transfer.abort")).toBe(false);
			clock.set(120_000);
			clock.tick();
			await expect(download.work).rejects.toThrow("no progress for 120000ms");
			expect(rpcCalls.find((call) => call.method === "transfer.abort")?.options?.timeoutMs).toBe(
				5_000,
			);
		} finally {
			clock.restore();
		}
	});
}
