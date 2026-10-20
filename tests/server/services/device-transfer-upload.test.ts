import { afterAll, afterEach, beforeEach, expect, mock, test } from "bun:test";
import { mkdtempSync, rmSync, truncateSync } from "node:fs";
import type { FileHandle } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { decodeChunkFrame } from "../../../server/lib/agent/execution/rpc-types";
import { settings } from "../../../server/lib/settings";

const CONNECTION_MODULE = "../../../server/services/device-connection-service";
const realConnection = { ...(await import(CONNECTION_MODULE)) };
const realFs = { ...(await import("node:fs/promises")) };
type ReadChunk = (
	buffer: Buffer,
	offset: number,
	length: number,
	position: number,
) => Promise<{ bytesRead: number; buffer: Buffer }>;
let readChunk: ((read: ReadChunk, ...args: Parameters<ReadChunk>) => ReturnType<ReadChunk>) | null;
let onBegin: ((signal?: AbortSignal) => Promise<void>) | null;
let onFrame: (() => void) | null;
let onAbort: (() => Promise<void>) | null;
let openError: Error | null;
let closeError: Error | null;
let source: string;
let sourceOpens: number;
let root: string;
let events: string[];
let frames: NonNullable<ReturnType<typeof decodeChunkFrame>>[];
let calls: { method: string; params: Record<string, unknown> }[];
let pending: Promise<unknown>[];
let releases: (() => void)[];
let originalDevices: typeof settings.devices;

mock.module("node:fs/promises", () => ({
	...realFs,
	open: async (...args: Parameters<typeof realFs.open>) => {
		if (String(args[0]) !== source) return realFs.open(...args);
		// The first open computes content identity; fault injection targets only
		// the second, positional-read handle used by the upload itself.
		if (++sourceOpens % 2 === 1) return realFs.open(...args);
		if (openError) throw openError;
		const handle = await realFs.open(...args);
		events.push("open");
		const read = handle.read.bind(handle) as ReadChunk;
		handle.read = ((...input: Parameters<ReadChunk>) =>
			readChunk ? readChunk(read, ...input) : read(...input)) as FileHandle["read"];
		const close = handle.close.bind(handle);
		handle.close = async () => {
			await close();
			events.push("close");
			if (closeError) throw closeError;
		};
		return handle;
	},
}));
mock.module(CONNECTION_MODULE, () => ({
	...realConnection,
	isDeviceOnline: () => true,
	getConnectedDeviceHello: () => ({ platform: { os: "linux", arch: "x64" } }),
	hasDeviceProtocolFeature: () => true,
	deviceBufferedAmount: () => 0,
	sendChunkFrame: (_deviceId: string, frame: Uint8Array) => {
		const decoded = decodeChunkFrame(frame);
		if (!decoded) throw new Error("invalid upload frame");
		frames.push(decoded);
		onFrame?.();
		return true;
	},
	sendRpc: async (
		_deviceId: string,
		method: string,
		params: Record<string, unknown>,
		options?: { signal?: AbortSignal },
	) => {
		calls.push({ method, params });
		events.push(method);
		if (method === "transfer.begin") {
			await onBegin?.(options?.signal);
			return { completedChunks: [] };
		}
		if (method === "transfer.abort") await onAbort?.();
		return { ok: true };
	},
}));
const { uploadFile, TRANSFER_CANCELLED_ABORT_REASON } = await import(
	"../../../server/services/device-transfer-service"
);

beforeEach(async () => {
	root = mkdtempSync(join(tmpdir(), "narrafork-upload-"));
	source = join(root, "source.bin");
	sourceOpens = 0;
	await realFs.writeFile(source, "abcdefghij");
	readChunk = null;
	onBegin = null;
	onFrame = null;
	onAbort = null;
	openError = null;
	closeError = null;
	events = [];
	frames = [];
	calls = [];
	pending = [];
	releases = [];
	originalDevices = settings.devices;
	settings.devices = {
		...settings.devices,
		transferChunkBytes: 4,
		transferConcurrency: 1,
		transferVerify: "none",
	} as NonNullable<typeof settings.devices>;
});
afterEach(async () => {
	for (const release of releases) release();
	await Promise.all(pending);
	settings.devices = originalDevices;
	rmSync(root, { recursive: true, force: true });
});
afterAll(() => {
	mock.module("node:fs/promises", () => realFs);
	mock.module(CONNECTION_MODULE, () => realConnection);
	mock.restore();
});

function upload(signal?: AbortSignal) {
	const work = uploadFile({
		deviceId: "upload-read-test",
		localPath: source,
		remoteDest: "/remote/destination.bin",
		remotePlatformOs: "linux",
		signal,
	});
	pending.push(work.catch(() => {}));
	return work;
}
function gate() {
	let release!: () => void;
	const promise = new Promise<void>((resolve) => {
		release = resolve;
	});
	releases.push(release);
	return { promise, release };
}
async function waitFor(predicate: () => boolean) {
	const deadline = Date.now() + 2_000;
	while (!predicate()) {
		if (Date.now() >= deadline) throw new Error("upload test did not reach its checkpoint");
		await new Promise<void>((resolve) => setImmediate(resolve));
	}
}

test("upload fills segmented short reads and preserves the smaller final chunk", async () => {
	const reads: { offset: number; position: number; length: number }[] = [];
	readChunk = async (read, buffer, offset, length, position) => {
		reads.push({ offset, length, position });
		return read(buffer, offset, Math.min(2, length), position);
	};
	await expect(upload()).resolves.toMatchObject({ bytes: 10 });
	expect(frames.map((frame) => Buffer.from(frame.payload).toString())).toEqual([
		"abcd",
		"efgh",
		"ij",
	]);
	expect(reads.map((read) => read.position)).toEqual([0, 2, 4, 6, 8]);
	expect(reads.map((read) => read.offset)).toEqual([0, 2, 0, 2, 0]);
	expect(events.indexOf("open")).toBeLessThan(events.indexOf("transfer.begin"));
	const digest = (
		calls.find((call) => call.method === "transfer.begin")?.params.contentIdentity as {
			digest: string;
		}
	).digest;
	expect(digest).toHaveLength(64);
	expect(calls.find((call) => call.method === "transfer.complete")?.params.sha256).toBe(digest);
});

for (const remaining of [0, 2]) {
	test(`upload rejects ${remaining}-byte truncated chunk without sending unread tail`, async () => {
		onFrame = () => truncateSync(source, 4 + remaining);
		await expect(upload()).rejects.toThrow("source ended");
		expect(frames).toHaveLength(1);
		expect(Buffer.from(frames[0].payload).toString()).toBe("abcd");
		expect(calls.some((call) => call.method === "transfer.complete")).toBe(false);
		expect(calls.find((call) => call.method === "transfer.abort")?.params.preservePartial).toBe(
			true,
		);
		expect(events).toContain("close");
	});
}

test("upload fails on an initial zero-byte EOF and sends no uninitialized buffer", async () => {
	readChunk = async (_read, buffer) => ({ bytesRead: 0, buffer });
	await expect(upload()).rejects.toThrow("source ended");
	expect(frames).toHaveLength(0);
	expect(calls.some((call) => call.method === "transfer.complete")).toBe(false);
});

test("failed workers drain actual reads before closing and release no late frame", async () => {
	if (!settings.devices) throw new Error("missing transfer settings");
	settings.devices.transferConcurrency = 2;
	const held = gate();
	let eof = false;
	readChunk = async (read, buffer, offset, length, position) => {
		if (position === 0) {
			await held.promise;
			return read(buffer, offset, length, position);
		}
		eof = true;
		return { bytesRead: 0, buffer };
	};
	let settled = false;
	const work = upload().finally(() => {
		settled = true;
	});
	pending.push(work.catch(() => {}));
	await waitFor(() => eof);
	expect(settled).toBe(false);
	expect(events).not.toContain("close");
	expect(calls.some((call) => call.method === "transfer.abort")).toBe(false);
	held.release();
	await expect(work).rejects.toThrow("source ended");
	expect(frames).toHaveLength(0);
	expect(events).toContain("close");
});

test("local open failure never starts a remote receiver and can be retried", async () => {
	openError = new Error("injected open failure");
	await expect(upload()).rejects.toThrow("open failure");
	expect(calls).toHaveLength(0);
	openError = null;
	await expect(upload()).resolves.toMatchObject({ bytes: 10 });
});

for (const reason of ["timeout", "pause", "cancel"]) {
	test(`failed begin (${reason}) closes and aborts before allowing retry`, async () => {
		const controller = new AbortController();
		onBegin = async () => {
			if (reason !== "timeout") {
				controller.abort(reason === "cancel" ? TRANSFER_CANCELLED_ABORT_REASON : undefined);
			}
			throw new Error(`begin ${reason}`);
		};
		await expect(upload(controller.signal)).rejects.toThrow(`begin ${reason}`);
		expect(events).toContain("close");
		expect(frames).toHaveLength(0);
		expect(calls.find((call) => call.method === "transfer.abort")?.params.preservePartial).toBe(
			reason !== "cancel",
		);
		onBegin = null;
		await expect(upload()).resolves.toMatchObject({ bytes: 10 });
	});
}

test("close failure cannot skip or outlive abort cleanup", async () => {
	const abort = gate();
	onBegin = async () => {
		throw new Error("begin timeout");
	};
	closeError = new Error("injected close failure");
	onAbort = () => abort.promise;
	let settled = false;
	const work = upload().finally(() => {
		settled = true;
	});
	pending.push(work.catch(() => {}));
	await waitFor(() => events.includes("close") && events.includes("transfer.abort"));
	expect(settled).toBe(false);
	abort.release();
	await expect(work).rejects.toThrow("close failure");
	onBegin = null;
	onAbort = null;
	closeError = null;
	await expect(upload()).resolves.toMatchObject({ bytes: 10 });
});
