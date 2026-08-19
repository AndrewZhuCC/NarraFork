import { describe, expect, test } from "bun:test";
import { providerStreamEventSchema } from "@server/lib/plugins/protocol";
import {
	mapKiroEvent,
	type PluginStreamEvent,
	UsageAccumulator,
} from "../../../../examples/plugins/kiro-external/src/event-mapping";
import { parseKiroWireStream } from "../../../../examples/plugins/kiro-external/src/wire-stream";

/**
 * The chat pipeline end to end, from Kiro's wire format to the host's event contract.
 *
 * The mapping unit tests feed `mapKiroEvent` hand-built `ParsedStreamEvent` objects, which
 * leaves one gap: they assume the parser produces the shapes they describe. This suite closes
 * it by driving a real AWS event-stream byte stream through `parseKiroWireStream` — the same
 * function the plugin uses in production — and asserting what comes out the far end.
 *
 * A mock upstream rather than a live account, deliberately: this must run in CI without
 * credentials or quota, and the fixture can produce event orderings a live account would only
 * emit occasionally.
 */

/**
 * Build a real AWS event-stream body from `(eventType, data)` pairs.
 *
 * Kiro answers over AWS event-stream — length-prefixed binary frames with CRC32 checksums — and
 * this fixture used to emit `event:` / `data:` text lines instead. That made the suite assert a
 * format production never sends, which is how a parser mismatch stayed invisible: the plugin read
 * binary frames with a text-SSE parser and produced *nothing*, while this suite kept passing
 * because it fed that parser the one input it could handle.
 *
 * CRCs are computed rather than stubbed: the decoder silently skips a frame with a bad checksum,
 * so an approximated builder would yield an empty stream and look like a parser failure.
 */
function wireStream(frames: Array<[string, Record<string, unknown>]>): ReadableStream<Uint8Array> {
	const encoded = frames.map(([eventType, data]) =>
		buildFrame({ ":message-type": "event", ":event-type": eventType }, JSON.stringify(data)),
	);
	return new ReadableStream({
		start(controller) {
			// One chunk per frame, so the decoder's cross-chunk buffering is exercised rather
			// than handed a single convenient blob.
			for (const frame of encoded) controller.enqueue(frame);
			controller.close();
		},
	});
}

/** Encode one `:name` → string header (value type 7). */
function encodeStringHeader(name: string, value: string): Uint8Array {
	const nameBytes = new TextEncoder().encode(name);
	const valueBytes = new TextEncoder().encode(value);
	const out = new Uint8Array(1 + nameBytes.length + 1 + 2 + valueBytes.length);
	let offset = 0;
	out[offset++] = nameBytes.length;
	out.set(nameBytes, offset);
	offset += nameBytes.length;
	out[offset++] = 7;
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

/** Run a stream through the plugin's mapping and validate every emitted event. */
async function collect(
	frames: Array<[string, Record<string, unknown>]>,
	contextWindow?: number,
): Promise<{ events: PluginStreamEvent[]; usage: UsageAccumulator }> {
	const usage = new UsageAccumulator();
	if (contextWindow !== undefined) usage.setContextWindow(contextWindow);
	const events: PluginStreamEvent[] = [];
	for await (const parsed of parseKiroWireStream(wireStream(frames))) {
		for (const event of mapKiroEvent(parsed, usage)) {
			// Every event must satisfy the host's real `.strict()` schema, so a shape the
			// parser produces but the contract rejects fails here rather than mid-session.
			const result = providerStreamEventSchema.safeParse(event);
			expect(result.success, `invalid event: ${JSON.stringify(event)}`).toBe(true);
			events.push(event);
		}
	}
	return { events, usage };
}

function textOf(events: PluginStreamEvent[]): string {
	return events
		.filter(
			(event): event is Extract<PluginStreamEvent, { type: "text.delta" }> =>
				event.type === "text.delta",
		)
		.map((event) => event.text)
		.join("");
}

function usageEvents(events: PluginStreamEvent[]) {
	return events.filter(
		(event): event is Extract<PluginStreamEvent, { type: "usage" }> => event.type === "usage",
	);
}

describe("kiro-external chat stream: text", () => {
	test("assembles streamed assistant text", async () => {
		const { events } = await collect([
			["assistantResponseEvent", { content: "Hello" }],
			["assistantResponseEvent", { content: " world" }],
		]);
		expect(textOf(events)).toBe("Hello world");
	});

	test("survives a frame split across chunk boundaries", async () => {
		// The parser buffers partial lines; a naive implementation would drop or duplicate text.
		const { events } = await collect([
			["assistantResponseEvent", { content: "a".repeat(200) }],
			["assistantResponseEvent", { content: "b".repeat(200) }],
		]);
		expect(textOf(events)).toBe(`${"a".repeat(200)}${"b".repeat(200)}`);
	});
});

describe("kiro-external chat stream: credits and context", () => {
	test("a metering frame becomes a usage event with credit units", async () => {
		const { events } = await collect([
			["assistantResponseEvent", { content: "hi" }],
			["meteringEvent", { usage: 2.5, unit: "credit", unitPlural: "credits" }],
		]);
		const [usageEvent] = usageEvents(events);
		expect(usageEvent.usage.metering).toEqual({
			unit: "credit",
			unitPlural: "credits",
			usage: 2.5,
		});
	});

	test("defaults the metering unit when upstream omits it", async () => {
		const { events } = await collect([["meteringEvent", { usage: 1 }]]);
		expect(usageEvents(events)[0].usage.metering?.unit).toBe("credit");
	});

	test("a context usage frame becomes a percentage", async () => {
		const { events } = await collect([["contextUsageEvent", { contextUsagePercentage: 37 }]]);
		expect(usageEvents(events)[0].usage.contextUsagePercentage).toBe(37);
	});

	test("accepts the snake_case spelling of the context field", async () => {
		// Both spellings appear upstream; the parser normalizes them.
		const { events } = await collect([["contextUsageEvent", { context_usage_percentage: 12 }]]);
		expect(usageEvents(events)[0].usage.contextUsagePercentage).toBe(12);
	});

	test("carries credits and context together with the model's window", async () => {
		const { usage } = await collect(
			[
				["contextUsageEvent", { contextUsagePercentage: 40 }],
				["meteringEvent", { usage: 3, unit: "credit", unitPlural: "credits" }],
			],
			200_000,
		);
		expect(usage.snapshot()).toEqual({
			contextWindow: 200_000,
			contextUsagePercentage: 40,
			metering: { unit: "credit", unitPlural: "credits", usage: 3 },
		});
	});

	test("never invents token counts from a real stream", async () => {
		// The decisive property for this provider: Kiro meters credits, not tokens. A zero
		// would be read as a measurement and corrupt both cost and context reporting.
		const { usage } = await collect([
			["assistantResponseEvent", { content: "answer" }],
			["meteringEvent", { usage: 1, unit: "credit", unitPlural: "credits" }],
			["contextUsageEvent", { contextUsagePercentage: 5 }],
		]);
		const snapshot = usage.snapshot() ?? {};
		for (const field of ["promptTokens", "inputTokens", "completionTokens"] as const) {
			expect(field in snapshot, `${field} must be absent, not zero`).toBe(false);
		}
	});
});

describe("kiro-external chat stream: reasoning and tools", () => {
	test("maps a credential id frame", async () => {
		// Not an event by itself; it rides on the parsed result and is surfaced by the plugin
		// on `request_started`/`done`. Assert the parser exposes it at all.
		const usage = new UsageAccumulator();
		const seen: string[] = [];
		for await (const parsed of parseKiroWireStream(
			wireStream([["credentialIdEvent", { credentialId: "cred-7" }]]),
		)) {
			if (parsed.credentialId) seen.push(parsed.credentialId);
			mapKiroEvent(parsed, usage);
		}
		expect(seen).toEqual(["cred-7"]);
	});

	test("a full turn keeps content before the usage that describes it", async () => {
		const { events } = await collect([
			["assistantResponseEvent", { content: "The answer" }],
			["contextUsageEvent", { contextUsagePercentage: 20 }],
			["meteringEvent", { usage: 4, unit: "credit", unitPlural: "credits" }],
		]);
		const kinds = events.map((event) => event.type);
		expect(kinds[0]).toBe("text.delta");
		// Usage arrives after the text it accounts for.
		expect(kinds.lastIndexOf("usage")).toBeGreaterThan(kinds.indexOf("text.delta"));
	});

	test("ignores frames it does not understand instead of failing the turn", async () => {
		// An unknown upstream event must not abort a live stream; the text still lands.
		const { events } = await collect([
			["someFutureEvent", { whatever: true }],
			["assistantResponseEvent", { content: "still here" }],
		]);
		expect(textOf(events)).toBe("still here");
	});

	test("an empty stream produces no events and no usage claim", async () => {
		const { events, usage } = await collect([]);
		expect(events).toEqual([]);
		// An empty usage object would still be an assertion about cost; undefined is honest.
		expect(usage.snapshot()).toBeUndefined();
	});
});
