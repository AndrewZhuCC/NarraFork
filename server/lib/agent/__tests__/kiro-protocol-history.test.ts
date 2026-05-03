import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { setUploadsDirForTests } from "../../uploads";
import { buildKiroHistory, parseSSEStream } from "../kiro-protocol";
import type { DbMessage, ParsedStreamEvent } from "../provider";

const SAMPLE_PNG_BASE64 =
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==";
let testUploadsRoot = "";

function registerTestImage(narratorId: string, imageId: string, ext = ".png"): string {
	const dir = resolve(testUploadsRoot, narratorId);
	mkdirSync(dir, { recursive: true });
	const filePath = resolve(dir, `${imageId}${ext}`);
	writeFileSync(filePath, Buffer.from(SAMPLE_PNG_BASE64, "base64"));
	return filePath;
}

function makeUserMessage(overrides: Partial<DbMessage> = {}): DbMessage {
	return {
		id: "user-1",
		role: "user",
		contentJson: [],
		contentText: null,
		parentToolUseId: null,
		messageUuid: null,
		toolCalls: [],
		...overrides,
	};
}

function makeAssistantMessage(overrides: Partial<DbMessage> = {}): DbMessage {
	return {
		id: "assistant-1",
		role: "assistant",
		contentJson: [],
		contentText: null,
		parentToolUseId: null,
		messageUuid: null,
		toolCalls: [],
		...overrides,
	};
}

function streamFromText(text: string): ReadableStream<Uint8Array> {
	return new ReadableStream({
		start(controller) {
			controller.enqueue(new TextEncoder().encode(text));
			controller.close();
		},
	});
}

beforeEach(() => {
	testUploadsRoot = mkdtempSync(join(tmpdir(), "narrafork-kiro-history-"));
	setUploadsDirForTests(testUploadsRoot);
});

afterEach(() => {
	setUploadsDirForTests(null);
	if (testUploadsRoot) {
		rmSync(testUploadsRoot, { recursive: true, force: true });
		testUploadsRoot = "";
	}
});

describe("buildKiroHistory", () => {
	test("replays persisted user images from the message owner narrator", async () => {
		registerTestImage("source-narrator", "img_hist");
		const dbMessages: DbMessage[] = [
			makeUserMessage({
				id: "user-image",
				narratorId: "source-narrator",
				contentJson: [
					{
						type: "image",
						imageId: "img_hist",
						filename: "sample.png",
						mediaType: "image/png",
					},
					{ type: "text", text: "Inspect this screenshot" },
				],
				contentText: "Inspect this screenshot",
			}),
			makeAssistantMessage({
				id: "assistant-after",
				contentJson: [{ type: "text", text: "done" }],
				contentText: "done",
			}),
		];

		const result = await buildKiroHistory(dbMessages, "kiro:claude-sonnet-4.6", "forked-narrator");
		const historyJson = JSON.stringify(result.history);

		expect(historyJson).toContain("Inspect this screenshot");
		expect(historyJson).toContain(SAMPLE_PNG_BASE64);
		expect(historyJson).toContain('"images"');
	});
});

describe("parseSSEStream", () => {
	test("parses NKP queue events from data-embedded SSE frames", async () => {
		const events: ParsedStreamEvent[] = [];
		const body = streamFromText(
			'data: {"type":"nkpQueueEvent","position":2,"queueDepth":5}\n\n' +
				'data: {"content":"hello"}\n\n',
		);

		for await (const event of parseSSEStream(body)) {
			events.push(event);
		}

		expect(events).toEqual([{ nkpQueue: { position: 2, queueDepth: 5 } }, { text: "hello" }]);
	});

	test("parses generic gateway queue events in Kiro-compatible streams", async () => {
		const events: ParsedStreamEvent[] = [];
		const body = streamFromText(
			"event: queueEvent\n" +
				'data: {"position":3,"queueDepth":8}\n\n' +
				'data: {"content":"ready"}\n\n',
		);

		for await (const event of parseSSEStream(body)) {
			events.push(event);
		}

		expect(events).toEqual([{ queueStatus: { position: 3, queueDepth: 8 } }, { text: "ready" }]);
	});
});
