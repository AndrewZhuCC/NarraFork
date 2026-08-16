/**
 * The plugin must parse the wire format Kiro actually sends.
 *
 * ## The defect this pins
 *
 * `KiroApiClient.sendChatRequest` returns the raw `fetch` Response. Kiro answers
 * `AmazonCodeWhispererStreamingService.GenerateAssistantResponse` over **AWS event-stream** —
 * length-prefixed binary frames with CRC32 checksums — which is why the built-in path reads it
 * with `parseEventStream`.
 *
 * The plugin used `parseSSEStream` instead. That function scans for `event:` / `data:` text
 * lines and belongs to NUG, a gateway that re-emits Kiro content as text SSE. Pointed at binary
 * frames it matches no lines at all and yields **nothing**: no content, no usage, no error. The
 * turn would simply end empty.
 *
 * Nothing caught it. The existing chat-stream suite builds its fixture as `event:`/`data:` text
 * and states it drives "the same function the plugin uses in production" — true, but it fed that
 * function a format production never produces, so the mismatch was invisible on both sides.
 *
 * These tests therefore build **real** frames, CRCs included. A frame with a bad checksum is
 * silently skipped by the decoder, so a builder that got that wrong would produce an empty
 * stream and appear to confirm the bug it was meant to disprove — the reason the byte layout is
 * spelled out here rather than approximated.
 */

import { describe, expect, test } from "bun:test";
import { parseKiroWireStream } from "../../../../examples/plugins/kiro-external/src/wire-stream";

// ── AWS event-stream frame builders ─────────────────────────────────────────

/** Encode one `:name` → string header (value type 7). */
function encodeStringHeader(name: string, value: string): Uint8Array {
	const nameBytes = new TextEncoder().encode(name);
	const valueBytes = new TextEncoder().encode(value);
	const out = new Uint8Array(1 + nameBytes.length + 1 + 2 + valueBytes.length);
	let offset = 0;
	out[offset++] = nameBytes.length;
	out.set(nameBytes, offset);
	offset += nameBytes.length;
	out[offset++] = 7; // string
	out[offset++] = (valueBytes.length >> 8) & 0xff;
	out[offset++] = valueBytes.length & 0xff;
	out.set(valueBytes, offset);
	return out;
}

function writeU32BE(buf: Uint8Array, offset: number, value: number): void {
	buf[offset] = (value >>> 24) & 0xff;
	buf[offset + 1] = (value >>> 16) & 0xff;
	buf[offset + 2] = (value >>> 8) & 0xff;
	buf[offset + 3] = value & 0xff;
}

/** prelude + prelude CRC + headers + payload + message CRC, as the decoder expects. */
function buildFrame(headers: Record<string, string>, payload: string): Uint8Array {
	const headerParts = Object.entries(headers).map(([k, v]) => encodeStringHeader(k, v));
	const headerLength = headerParts.reduce((sum, h) => sum + h.length, 0);
	const payloadBytes = new TextEncoder().encode(payload);
	const totalLength = 12 + headerLength + payloadBytes.length + 4;

	const frame = new Uint8Array(totalLength);
	writeU32BE(frame, 0, totalLength);
	writeU32BE(frame, 4, headerLength);
	writeU32BE(frame, 8, Bun.hash.crc32(frame.subarray(0, 8)));

	let offset = 12;
	for (const part of headerParts) {
		frame.set(part, offset);
		offset += part.length;
	}
	frame.set(payloadBytes, offset);
	writeU32BE(frame, totalLength - 4, Bun.hash.crc32(frame.subarray(0, totalLength - 4)));
	return frame;
}

function eventFrame(eventType: string, data: Record<string, unknown>): Uint8Array {
	return buildFrame({ ":message-type": "event", ":event-type": eventType }, JSON.stringify(data));
}

function exceptionFrame(exceptionType: string, message: string): Uint8Array {
	return buildFrame(
		{ ":message-type": "exception", ":exception-type": exceptionType },
		JSON.stringify({ message }),
	);
}

/** One chunk per frame, so cross-chunk buffering is exercised rather than bypassed. */
function streamOf(...frames: Uint8Array[]): ReadableStream<Uint8Array> {
	return new ReadableStream({
		start(controller) {
			for (const frame of frames) controller.enqueue(frame);
			controller.close();
		},
	});
}

/** Split every frame at arbitrary byte boundaries, as a real socket would. */
function fragmentedStream(frames: Uint8Array[], chunkSize: number): ReadableStream<Uint8Array> {
	const total = frames.reduce((sum, f) => sum + f.length, 0);
	const joined = new Uint8Array(total);
	let offset = 0;
	for (const frame of frames) {
		joined.set(frame, offset);
		offset += frame.length;
	}
	return new ReadableStream({
		start(controller) {
			for (let index = 0; index < joined.length; index += chunkSize) {
				controller.enqueue(joined.subarray(index, index + chunkSize));
			}
			controller.close();
		},
	});
}

async function collect(stream: ReadableStream<Uint8Array>, options = {}) {
	const events: Array<Record<string, unknown>> = [];
	for await (const event of parseKiroWireStream(stream, options)) {
		events.push(event as unknown as Record<string, unknown>);
	}
	return events;
}

describe("plugin wire format: AWS event-stream", () => {
	test("decodes assistant text from real binary frames", async () => {
		// The core regression. The text-SSE parser returned zero events for exactly this input,
		// so a turn produced no content and no error — it just ended empty.
		const events = await collect(
			streamOf(
				eventFrame("assistantResponseEvent", { content: "Hello" }),
				eventFrame("assistantResponseEvent", { content: " world" }),
			),
		);

		expect(events.length).toBeGreaterThan(0);
		const text = events.map((event) => event.text ?? "").join("");
		expect(text).toBe("Hello world");
	});

	test("reassembles frames split across chunk boundaries", async () => {
		// A frame's length prefix can land in one TCP segment and its payload in the next, so
		// the decoder must buffer across reads rather than assume frame-aligned chunks.
		const events = await collect(
			fragmentedStream(
				[
					eventFrame("assistantResponseEvent", { content: "split" }),
					eventFrame("assistantResponseEvent", { content: "-across" }),
				],
				3,
			),
		);

		expect(events.map((event) => event.text ?? "").join("")).toBe("split-across");
	});

	test("surfaces an exception frame instead of ending the turn silently", async () => {
		// A suspension arrives as an `exception` frame inside an HTTP 200 body. Yielding nothing
		// would report a successful, empty turn for what is really an account-level refusal.
		await expect(
			collect(
				streamOf(
					exceptionFrame("AccessDeniedException", "Your account is suspended. Contact support."),
				),
			),
		).rejects.toThrow(/suspend/i);
	});

	test("keeps content emitted before an exception frame", async () => {
		// Whether content already reached the user decides if a replay is safe, so the caller
		// has to be able to tell. Losing the prefix would make a mid-stream failure look like
		// it happened before any output.
		const seen: string[] = [];
		await expect(
			(async () => {
				for await (const event of parseKiroWireStream(
					streamOf(
						eventFrame("assistantResponseEvent", { content: "partial" }),
						exceptionFrame("ThrottlingException", "slow down"),
					),
				)) {
					if (typeof event.text === "string") seen.push(event.text);
				}
			})(),
		).rejects.toThrow();

		expect(seen.join("")).toBe("partial");
	});

	test("recovers leaked XML tool calls from the text stream", async () => {
		// Some Kiro models emit tool calls inline in text instead of the native fields. The
		// plugin declares `mayLeakXmlToolCalls`, and this recovery must survive the parser
		// change — dropping it would leave those tools unexecuted.
		// Split across two frames on purpose: the recovery happens at stream level precisely
		// because a leaked call can straddle text deltas, and a per-event pass would miss it.
		const events = await collect(
			streamOf(
				eventFrame("assistantResponseEvent", { content: '<invoke name="Ba' }),
				eventFrame("assistantResponseEvent", {
					content: 'sh"><parameter name="command">pwd</parameter></invoke>',
				}),
			),
			{ parseTextToolCalls: true, model: "claude-sonnet-4.5" },
		);

		const toolUses = events.flatMap((event) =>
			Array.isArray(event.toolUses) ? event.toolUses : [],
		);
		expect(toolUses).toHaveLength(1);
		expect((toolUses[0] as { name?: string }).name).toBe("Bash");
		// The XML must not also reach the user as literal text.
		expect(events.map((event) => event.text ?? "").join("")).not.toContain("<invoke");
	});

	test("carries streaming tool-use frames through", async () => {
		const events = await collect(
			streamOf(
				eventFrame("toolUseEvent", {
					toolUseId: "tu-1",
					name: "read",
					input: '{"path":"a.ts"}',
					stop: true,
				}),
			),
		);

		const chunk = events.find((event) => event.toolUseChunk !== undefined)?.toolUseChunk as
			| { toolUseId?: string; name?: string; stop?: boolean }
			| undefined;
		expect(chunk?.toolUseId).toBe("tu-1");
		expect(chunk?.name).toBe("read");
		expect(chunk?.stop).toBe(true);
	});

	test("carries the metadataEvent stop reason through the parser", async () => {
		// Observed in every real turn: `metadataEvent {stopReason:"END_TURN"}` closes the stream.
		// The frame was previously unhandled, so the turn's real ending was discarded.
		const events = await collect(
			streamOf(
				eventFrame("assistantResponseEvent", { content: "partial answer" }),
				eventFrame("metadataEvent", { stopReason: "MAX_TOKENS" }),
			),
		);

		// `max_output_tokens` is what the host converts into a visible truncation notice; without
		// it the reply looks like it simply ended.
		expect(events.map((event) => event.stopReason).filter(Boolean)).toEqual(["max_output_tokens"]);
		expect(events.map((event) => event.text ?? "").join("")).toBe("partial answer");
	});

	test("the real frame sequence from a live turn decodes end to end", async () => {
		// Captured verbatim from a chat against q.us-east-1.amazonaws.com (api_key credential,
		// model glm-5), including the leading `initial-response` and the empty first content
		// frame. Reproduced here so the exact shape upstream sends stays covered without needing
		// credentials in CI.
		const events = await collect(
			streamOf(
				eventFrame("initial-response", { conversationId: "" }),
				eventFrame("assistantResponseEvent", { content: "", modelId: "glm-5" }),
				eventFrame("assistantResponseEvent", { content: "PROBE_OK", modelId: "glm-5" }),
				eventFrame("metadataEvent", { stopReason: "END_TURN" }),
				eventFrame("contextUsageEvent", { contextUsagePercentage: 1.851 }),
				eventFrame("meteringEvent", {
					unit: "credit",
					unitPlural: "credits",
					usage: 0.0436742056384743,
				}),
			),
		);

		expect(events.map((event) => event.text ?? "").join("")).toBe("PROBE_OK");
		expect(events.map((event) => event.stopReason).filter(Boolean)).toEqual(["end_turn"]);
		// The metering frame must survive as usage, since it drives credit accounting.
		expect(events.some((event) => event.metering !== undefined)).toBe(true);
		// No frame in this sequence may be mistaken for an error state.
		expect(events.every((event) => event.invalidState === undefined)).toBe(true);
	});

	test("reports an invalid-state frame as invalid state, not as content", async () => {
		const events = await collect(
			streamOf(
				eventFrame("invalidStateEvent", {
					reason: "INVALID_CONVERSATION",
					message: "conversation cannot continue",
				}),
			),
		);

		expect(events.some((event) => event.invalidState !== undefined)).toBe(true);
	});
});
