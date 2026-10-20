/**
 * The dialect-neutral mailbox kernel: pure functions only, no database import. This
 * suite exists so the extraction from `mailbox.ts` is pinned — the PostgreSQL adapter
 * imports these DIRECTLY, so a behavioral drift here would split the two backends'
 * dedupe and bounding semantics.
 */
import { expect, test } from "bun:test";
import { MAILBOX_LIMITS } from "./limits";
import {
	AWAITED_TERMINAL_CONSUMED_REASON,
	boundedError,
	boundedJson,
	isAwaitedTerminalConsumption,
	mailboxDedupeKey,
	nextCurrentRevision,
	pointer,
	recipientDeletedReceiptPatch,
	semanticEditReceiptPatch,
} from "./mailbox-shared";

test("only exact Await cancellation of an unmaterialized task notice is a benign skip", () => {
	const row = {
		state: "cancelled",
		kind: "task_notice",
		lastError: AWAITED_TERMINAL_CONSUMED_REASON,
		currentMessageId: null,
	};
	expect(isAwaitedTerminalConsumption(row)).toBe(true);
	for (const other of [
		undefined,
		{ ...row, kind: "agent_message" },
		{ ...row, kind: "user_input" },
		{ ...row, lastError: "cancelled by user" },
		{ ...row, state: "failed" },
		{ ...row, state: "claimed" },
		{ ...row, currentMessageId: "persisted-history" },
	])
		expect(isAwaitedTerminalConsumption(other)).toBe(false);
});

test("recipient deletion receipt is one shared, fixed three-field patch", () => {
	const at = "2026-09-09T00:00:00.000Z";
	const patch = recipientDeletedReceiptPatch(at);
	expect(patch).toEqual({
		currentMessageId: null,
		receiptDisposition: "recipient_deleted",
		updatedAt: at,
	});
	expect(recipientDeletedReceiptPatch(at)).not.toBe(patch);
});

test("semantic edit supersedes the current content without touching the original adoption", () => {
	const at = "2026-09-09T00:00:00.000Z";
	expect(semanticEditReceiptPatch("copy", 4, at)).toEqual({
		currentMessageId: "copy",
		receiptDisposition: "superseded",
		currentRevision: 4,
		currentAdoptedRevision: null,
		currentAdoptedAt: null,
		updatedAt: at,
	});
	// Original-delivery facts are deliberately absent from the patch.
	const keys = Object.keys(semanticEditReceiptPatch("copy", 4, at));
	for (const original of ["adoptedRevision", "adoptedAt", "contentRevision", "state"])
		expect(keys).not.toContain(original);
	// The value form matches the adapters' max/greatest(current, content) + 1.
	expect(nextCurrentRevision(3, 1)).toBe(4);
	expect(nextCurrentRevision(1, 5)).toBe(6);
	expect(nextCurrentRevision(2, 2)).toBe(3);
});

test("boundedJson encodes within the byte budget and rejects overflow", () => {
	expect(boundedJson({ a: 1 }, 1024)).toBe('{"a":1}');
	// The budget counts BYTES, not UTF-16 units.
	const oversized = "中".repeat(1024);
	expect(() => boundedJson(oversized, 1024)).toThrow(/exceeds 1024 bytes/);
});

test("boundedError truncates to the error budget in bytes", () => {
	const long = "x".repeat(MAILBOX_LIMITS.errorBytes + 500);
	const bounded = boundedError(long);
	expect(Buffer.byteLength(bounded)).toBeLessThanOrEqual(MAILBOX_LIMITS.errorBytes);
	expect(boundedError("short")).toBe("short");
});

test("pointer accepts short opaque identities and rejects the rest", () => {
	expect(pointer("abc-123")).toBe("abc-123");
	expect(() => pointer("")).toThrow("Invalid identity pointer");
	expect(() => pointer("y".repeat(513))).toThrow("Invalid identity pointer");
});

test("mailboxDedupeKey is stable per kind and rejects malformed execution identity", () => {
	const agentInput = {
		kind: "agent_message" as const,
		narratorId: "recipient",
		text: "hello",
		projectedByteSize: 5,
		sourceNarratorId: "sender",
		sourceToolCallId: "call-1",
		sourceAttempt: 2,
		sourceKey: "receipt-1",
	};
	const key = mailboxDedupeKey(agentInput);
	expect(key).toBe(mailboxDedupeKey({ ...agentInput }));
	const parsed = JSON.parse(key);
	expect(parsed).toEqual(["send", "sender", "call-1", 2, "receipt-1", "recipient"]);
	// A different attempt is a different delivery.
	expect(mailboxDedupeKey({ ...agentInput, sourceAttempt: 3 })).not.toBe(key);
	expect(() => mailboxDedupeKey({ ...agentInput, sourceAttempt: 0 })).toThrow(
		"Exact execution attempt required",
	);

	const noticeKey = mailboxDedupeKey({
		kind: "task_notice",
		narratorId: "recipient",
		text: "summary",
		projectedByteSize: 7,
		noticeKind: "bash",
		sourceKey: "publication-key",
	});
	expect(noticeKey).toBe("publication-key");

	const userKey = mailboxDedupeKey({
		kind: "user_input",
		narratorId: "recipient",
		text: "hi",
		projectedByteSize: 2,
		requestKey: "request-1",
	});
	expect(userKey).toBe(JSON.stringify(["user", "request-1"]));
	// Without a caller request key each call is a fresh identity (generated id).
	const generated = mailboxDedupeKey({
		kind: "user_input",
		narratorId: "recipient",
		text: "hi",
		projectedByteSize: 2,
	});
	expect(JSON.parse(generated)[0]).toBe("user");
	expect(generated).not.toBe(
		mailboxDedupeKey({
			kind: "user_input",
			narratorId: "recipient",
			text: "hi",
			projectedByteSize: 2,
		}),
	);
});
