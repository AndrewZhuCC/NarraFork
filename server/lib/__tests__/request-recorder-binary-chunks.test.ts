import { afterAll, beforeEach, describe, expect, test } from "bun:test";
import type { Dirent } from "node:fs";
import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";
import { getNarraforkHome } from "../narrafork-home";
import { createRequestRecording, type RequestRecorderClient } from "../request-recorder";
import { RECORDING_CHUNK_BYTES, REQUEST_RECORDING_DIR } from "../request-recorder-protocol";
import { settings } from "../settings";

interface Frame {
	requestId: string;
	direction: string;
	packet: number;
	offset: number;
	data: string;
}

function publishedFrames(text: string, requestId: string): Frame[] {
	// A live segment may be observed partway through a write. Only a trailing
	// newline publishes a frame; malformed complete lines must still fail loudly.
	const end = text.lastIndexOf("\n");
	if (end < 0) return [];
	const frames: Frame[] = [];
	for (const line of text.slice(0, end).split("\n")) {
		if (!line) continue;
		const frame = JSON.parse(line) as Frame;
		if (frame.requestId === requestId) frames.push(frame);
	}
	return frames;
}

const originalEvidence = settings.requestEvidence;

// The shared preload restores settings after every test, including parser unit cases.
beforeEach(() => {
	settings.requestEvidence = {
		ringEnabled: true,
		ringRequestBytes: 4 * 1024 * 1024,
		ringResponseBytes: 4 * 1024 * 1024,
		autoPin: "off",
	};
});
afterAll(async () => {
	settings.requestEvidence = originalEvidence;
	// Stop the shared worker so the test process can exit.
	const clients = (globalThis as Record<symbol, unknown>)[
		Symbol.for("request-recording-clients")
	] as Map<string, RequestRecorderClient> | undefined;
	for (const client of clients?.values() ?? []) await client.close();
});

async function framesFor(requestId: string): Promise<Frame[]> {
	const ring = join(getNarraforkHome(), REQUEST_RECORDING_DIR);
	const frames: Frame[] = [];
	let entries: Dirent[];
	try {
		entries = await readdir(ring, { withFileTypes: true });
	} catch {
		return frames;
	}
	for (const entry of entries) {
		if (!entry.isDirectory() || entry.name.startsWith(".")) continue;
		const chunks = join(ring, entry.name, "chunks");
		let files: string[];
		try {
			files = await readdir(chunks);
		} catch {
			continue;
		}
		for (const file of files) {
			frames.push(...publishedFrames(await readFile(join(chunks, file), "utf8"), requestId));
		}
	}
	return frames;
}

async function waitForEnd(requestId: string, expected: number): Promise<Frame[]> {
	const deadline = Date.now() + 15_000;
	let frames: Frame[] = [];
	while (Date.now() < deadline) {
		frames = await framesFor(requestId);
		if (frames.length >= expected) {
			// Give a duplicated (buggy) stream time to show up before asserting.
			await Bun.sleep(300);
			return framesFor(requestId);
		}
		await Bun.sleep(50);
	}
	return frames;
}

describe("二进制响应块录制", () => {
	test("live segment reader waits for a newline without accepting a partial frame", () => {
		const frame: Frame = {
			requestId: "target",
			direction: "response",
			packet: 1,
			offset: 0,
			data: "QQ==",
		};
		const line = JSON.stringify(frame);
		expect(publishedFrames(line, "target")).toEqual([]);
		expect(publishedFrames(`${line}\n{"requestId":"target`, "target")).toEqual([frame]);
		expect(publishedFrames(`${line}\n${line}\n`, "target")).toEqual([frame, frame]);
		expect(publishedFrames(`${line}\n`, "other")).toEqual([]);
	});
	test("live segment reader still rejects corrupted completed frames", () => {
		expect(() => publishedFrames('{"requestId":"target"\n', "target")).toThrow();
	});
	test("worker 就绪后每个二进制分块只写一帧（transfer 不得阻止 offset 前进）", async () => {
		// Warm the worker so later postMessage calls transfer synchronously.
		const warm = createRequestRecording({
			requestId: "binary-warm",
			narratorId: "n1",
			provider: "fixture",
			model: "fixture",
		});
		expect(warm).toBeDefined();
		warm?.responseChunk(new Uint8Array([1]));
		warm?.finish({ status: "completed" });
		await waitForEnd("binary-warm", 1);

		const recording = createRequestRecording({
			requestId: "binary-once",
			narratorId: "n1",
			provider: "fixture",
			model: "fixture",
		});
		expect(recording).toBeDefined();
		const small = new Uint8Array(1000).fill(0x41);
		// Spans three RECORDING_CHUNK_BYTES windows within one packet.
		const large = new Uint8Array(RECORDING_CHUNK_BYTES * 2 + 10).map((_, index) => index % 251);
		recording?.responseChunk(small);
		recording?.responseChunk(large);
		recording?.finish({ status: "completed" });

		const frames = (await waitForEnd("binary-once", 4)).filter(
			(frame) => frame.direction === "response",
		);
		const keys = frames.map((frame) => `${frame.packet}:${frame.offset}`);
		expect(keys.sort()).toEqual(
			["1:0", "2:0", `2:${RECORDING_CHUNK_BYTES}`, `2:${RECORDING_CHUNK_BYTES * 2}`].sort(),
		);
		const packet2 = Buffer.concat(
			frames
				.filter((frame) => frame.packet === 2)
				.sort((a, b) => a.offset - b.offset)
				.map((frame) => Buffer.from(frame.data, "base64")),
		);
		expect(packet2.equals(Buffer.from(large))).toBe(true);
		expect(Buffer.from(frames.find((f) => f.packet === 1)?.data ?? "", "base64")).toEqual(
			Buffer.from(small),
		);
		// The caller's buffer is copied, never detached.
		expect(large.byteLength).toBe(RECORDING_CHUNK_BYTES * 2 + 10);
	}, 30_000);
});
