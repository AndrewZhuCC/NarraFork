/**
 * Mid-stream account suspension → credential failover.
 *
 * ── The bug this pins ───────────────────────────────────────────────────────
 * Kiro answers over AWS event-stream, so a suspension can arrive as an
 * `exception` FRAME inside an HTTP 200 body. `sendChatRequest` has returned by
 * then and has already called `reportSuccess`, so its retry loop cannot react:
 * the suspended credential was never marked and never excluded, and every later
 * request — each compact retry included — reselected it and surfaced the raw AWS
 * JSON to the user.
 *
 * These tests drive the REAL `kiroChatRaw` against real binary frames, because
 * the whole failure lived in the seam between the HTTP client and the stream
 * parser; a unit test on either side alone would have passed throughout.
 */

import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { __resetKiroConcurrencyForTests } from "../concurrency-control";
import { initKiro, kiroChatRaw, kiroSnapshot } from "../index";
import type { KiroChatRequest, KiroCredentials } from "../types";

// ── AWS event-stream frame builders ─────────────────────────────────────────

/** Encode one `:name` → string header (value type 7). */
function encodeStringHeader(name: string, value: string): Uint8Array {
	const nameBytes = new TextEncoder().encode(name);
	const valueBytes = new TextEncoder().encode(value);
	const out = new Uint8Array(1 + nameBytes.length + 1 + 2 + valueBytes.length);
	let o = 0;
	out[o++] = nameBytes.length;
	out.set(nameBytes, o);
	o += nameBytes.length;
	out[o++] = 7; // string
	out[o++] = (valueBytes.length >> 8) & 0xff;
	out[o++] = valueBytes.length & 0xff;
	out.set(valueBytes, o);
	return out;
}

function writeU32BE(buf: Uint8Array, offset: number, value: number): void {
	buf[offset] = (value >>> 24) & 0xff;
	buf[offset + 1] = (value >>> 16) & 0xff;
	buf[offset + 2] = (value >>> 8) & 0xff;
	buf[offset + 3] = value & 0xff;
}

/**
 * Build one framed message: prelude + prelude CRC + headers + payload + msg CRC.
 * Mirrors the layout `EventStreamDecoder` expects, CRC32 included — a frame with a
 * bad checksum would be silently skipped and the test would prove nothing.
 */
function buildFrame(headers: Record<string, string>, payload: string): Uint8Array {
	const headerParts = Object.entries(headers).map(([k, v]) => encodeStringHeader(k, v));
	const headerLength = headerParts.reduce((sum, h) => sum + h.length, 0);
	const payloadBytes = new TextEncoder().encode(payload);
	const totalLength = 12 + headerLength + payloadBytes.length + 4;

	const frame = new Uint8Array(totalLength);
	writeU32BE(frame, 0, totalLength);
	writeU32BE(frame, 4, headerLength);
	writeU32BE(frame, 8, Bun.hash.crc32(frame.subarray(0, 8)));

	let o = 12;
	for (const part of headerParts) {
		frame.set(part, o);
		o += part.length;
	}
	frame.set(payloadBytes, o);
	writeU32BE(frame, totalLength - 4, Bun.hash.crc32(frame.subarray(0, totalLength - 4)));
	return frame;
}

function assistantTextFrame(text: string): Uint8Array {
	return buildFrame(
		{ ":message-type": "event", ":event-type": "assistantResponseEvent" },
		JSON.stringify({ content: text }),
	);
}

const SUSPENSION_PAYLOAD = JSON.stringify({
	__type: "com.amazon.aws.codewhisperer#AccessDeniedException",
	message:
		"Your User ID (e498f488-e0e1-70b4-9543-44c437a0089f) temporarily is suspended. We've locked your account as a security precaution. To restore access, please contact our support team to verify your identity.",
});

function suspensionExceptionFrame(): Uint8Array {
	return buildFrame(
		{
			":message-type": "exception",
			":exception-type": "com.amazon.aws.codewhisperer#AccessDeniedException",
		},
		SUSPENSION_PAYLOAD,
	);
}

function concat(...parts: Uint8Array[]): Uint8Array {
	const total = parts.reduce((sum, p) => sum + p.length, 0);
	const out = new Uint8Array(total);
	let o = 0;
	for (const p of parts) {
		out.set(p, o);
		o += p.length;
	}
	return out;
}

function streamResponse(body: Uint8Array): Response {
	return new Response(
		new ReadableStream<Uint8Array>({
			start(controller) {
				controller.enqueue(body);
				controller.close();
			},
		}),
		{ status: 200 },
	);
}

// ── Fixture ─────────────────────────────────────────────────────────────────

let tempDirs: string[] = [];
let originalFetch: typeof fetch;

function makeCredential(id: string, priority: number): KiroCredentials {
	return {
		id,
		authMethod: "social",
		refreshToken: `rt-${id}`.padEnd(120, "x"),
		accessToken: `access-${id}`,
		subscriptionTitle: "Free",
		priority,
		expiresAt: new Date(Date.now() + 60 * 60_000).toISOString(),
	};
}

/** Boot the real plugin (token manager + api client) over a temp credentials file. */
function initPlugin(credentials: KiroCredentials[]): void {
	const dir = mkdtempSync(join(tmpdir(), "narrafork-kiro-suspend-"));
	tempDirs.push(dir);
	const credentialsPath = join(dir, "credentials.json");
	writeFileSync(credentialsPath, JSON.stringify(credentials, null, 2));
	writeFileSync(join(dir, "kiro_usage.json"), "{}");
	const configPath = join(dir, "config.json");
	writeFileSync(
		configPath,
		JSON.stringify({
			region: "us-east-1",
			kiroVersion: "1.26.1",
			systemVersion: "darwin#24.6.0",
			nodeVersion: "22.21.1",
			loadBalancingMode: "priority",
			tierPriority: ["free", "enterprise", "paid"],
			concurrency: { default: { initialMax: 3, ceiling: 8, floor: 1 } },
			requestInterval: { free: 0, enterprise: 0, paid: 0 },
		}),
	);
	expect(initKiro({ credentialsPath, configPath })).toBe(true);
}

const REQUEST: KiroChatRequest = {
	conversationState: {
		conversationId: "conv-suspend",
		currentMessage: {
			userInputMessage: { content: "hi", modelId: "claude-sonnet-4.5" },
		},
	},
};

/** Drain kiroChatRaw, collecting assistant text and any thrown error. */
async function drain(): Promise<{ text: string; error: Error | null }> {
	const chunks: string[] = [];
	try {
		for await (const evt of kiroChatRaw(REQUEST)) {
			if (evt.eventType === "assistantResponseEvent" && evt.data.content != null) {
				chunks.push(String(evt.data.content));
			}
		}
		return { text: chunks.join(""), error: null };
	} catch (err) {
		return { text: chunks.join(""), error: err instanceof Error ? err : new Error(String(err)) };
	}
}

function snapshotEntry(id: string) {
	const entry = kiroSnapshot()?.entries.find((e) => e.id === id);
	if (!entry) throw new Error(`credential ${id} missing from snapshot`);
	return entry;
}

beforeEach(() => {
	__resetKiroConcurrencyForTests();
	tempDirs = [];
	originalFetch = globalThis.fetch;
});

afterEach(() => {
	globalThis.fetch = originalFetch;
	__resetKiroConcurrencyForTests();
	for (const dir of tempDirs.splice(0)) {
		rmSync(dir, { recursive: true, force: true });
	}
});

describe("mid-stream suspension failover", () => {
	test("marks the suspended credential and replays on a healthy one", async () => {
		initPlugin([makeCredential("first", 0), makeCredential("second", 1)]);
		const tokens: string[] = [];
		globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
			tokens.push(new Headers(init?.headers).get("Authorization") ?? "");
			return tokens.length === 1
				? streamResponse(suspensionExceptionFrame())
				: streamResponse(assistantTextFrame("hello from the healthy account"));
		}) as typeof fetch;

		const { text, error } = await drain();

		expect(error).toBeNull();
		expect(text).toBe("hello from the healthy account");
		// The replay must go to a DIFFERENT credential, not the suspended one again.
		expect(tokens).toEqual(["Bearer access-first", "Bearer access-second"]);
		expect(snapshotEntry("first").disabled).toBe(true);
		expect(snapshotEntry("first").disabledReason).toBe("account_suspended");
		expect(snapshotEntry("second").disabled).toBe(false);
	});

	test("suspension is durable, so a later request skips the bad credential outright", async () => {
		initPlugin([makeCredential("first", 0), makeCredential("second", 1)]);
		let calls = 0;
		globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
			calls++;
			const auth = new Headers(init?.headers).get("Authorization") ?? "";
			if (auth === "Bearer access-first") return streamResponse(suspensionExceptionFrame());
			return streamResponse(assistantTextFrame("ok"));
		}) as typeof fetch;

		await drain();
		const callsAfterFirstTurn = calls;
		const second = await drain();

		expect(second.error).toBeNull();
		expect(second.text).toBe("ok");
		// One dispatch only: the suspended credential is no longer a candidate, so
		// the second turn does not pay another failover round-trip.
		expect(calls - callsAfterFirstTurn).toBe(1);
	});

	test("does not replay once content was already emitted", async () => {
		initPlugin([makeCredential("first", 0), makeCredential("second", 1)]);
		let calls = 0;
		globalThis.fetch = (async (_input: RequestInfo | URL, _init?: RequestInit) => {
			calls++;
			return streamResponse(
				concat(assistantTextFrame("partial answer"), suspensionExceptionFrame()),
			);
		}) as unknown as typeof fetch;

		const { text, error } = await drain();

		// Replaying here would duplicate "partial answer" in the transcript.
		expect(calls).toBe(1);
		expect(text).toBe("partial answer");
		expect(error?.message).toContain("temporarily is suspended");
		// Still marked, so the NEXT request avoids it.
		expect(snapshotEntry("first").disabledReason).toBe("account_suspended");
	});

	test("a single suspended credential fails with a bounded number of attempts", async () => {
		initPlugin([makeCredential("only", 0)]);
		let calls = 0;
		globalThis.fetch = (async (_input: RequestInfo | URL, _init?: RequestInit) => {
			calls++;
			if (calls > 8) throw new Error("replayed unboundedly");
			return streamResponse(suspensionExceptionFrame());
		}) as unknown as typeof fetch;

		const { error } = await drain();

		expect(error).not.toBeNull();
		expect(error?.message).toContain("temporarily is suspended");
		// No alternative exists, so there is nothing to replay onto.
		expect(calls).toBe(1);
		expect(snapshotEntry("only").disabledReason).toBe("account_suspended");
	});

	test("an ordinary stream exception is not treated as a suspension", async () => {
		initPlugin([makeCredential("first", 0), makeCredential("second", 1)]);
		let calls = 0;
		globalThis.fetch = (async (_input: RequestInfo | URL, _init?: RequestInit) => {
			calls++;
			return streamResponse(
				buildFrame(
					{ ":message-type": "exception", ":exception-type": "ValidationException" },
					JSON.stringify({ message: "malformed input" }),
				),
			);
		}) as unknown as typeof fetch;

		const { error } = await drain();

		expect(error?.message).toContain("malformed input");
		expect(calls).toBe(1);
		// Must NOT durably disable a credential over an unrelated error.
		expect(snapshotEntry("first").disabled).toBe(false);
		expect(snapshotEntry("first").disabledReason).toBeUndefined();
	});
});
