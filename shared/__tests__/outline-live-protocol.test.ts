import { describe, expect, test } from "bun:test";
import {
	decodeLiveFrame,
	encodeLiveFrame,
	isLiveFrame,
	isLiveTaskChar,
	LIVE_FRAME,
	LIVE_STATUS_OF_TASK,
	LIVE_TASK_OF_STATUS,
	OUTLINE_LIVE_LIMITS,
	OUTLINE_LIVE_MAGIC,
} from "../outline-live-protocol";

describe("live frames", () => {
	test("encode and decode round trip, body as a view", () => {
		const body = new Uint8Array([1, 2, 3, 250]);
		const bytes = encodeLiveFrame({
			kind: LIVE_FRAME.update,
			handle: 300,
			n: 2 ** 40,
			head: { a: [1, "x"] },
			body,
		});
		expect(bytes[0]).toBe(OUTLINE_LIVE_MAGIC);
		expect(isLiveFrame(bytes)).toBe(true);
		const frame = decodeLiveFrame(bytes);
		expect(frame).toMatchObject({
			kind: LIVE_FRAME.update,
			handle: 300,
			n: 2 ** 40,
			head: { a: [1, "x"] },
		});
		expect([...(frame?.body ?? [])]).toEqual([1, 2, 3, 250]);
	});

	test("a frame without head or body is a few bytes", () => {
		const bytes = encodeLiveFrame({ kind: LIVE_FRAME.leave, handle: 1 });
		expect(bytes.length).toBe(5);
		expect(decodeLiveFrame(bytes)).toMatchObject({
			kind: LIVE_FRAME.leave,
			handle: 1,
			n: 0,
			head: null,
		});
	});

	test("malformed frames decode to null", () => {
		const good = encodeLiveFrame({ kind: LIVE_FRAME.tx, handle: 1, n: 1, head: { ops: [] } });
		expect(decodeLiveFrame(new Uint8Array([0x7b, 0x22]))).toBeNull();
		expect(decodeLiveFrame(good.subarray(0, 4))).toBeNull();
		const badJson = good.slice();
		badJson[badJson.length - 1] = 0x7b;
		expect(decodeLiveFrame(badJson)).toBeNull();
		expect(
			decodeLiveFrame(
				new Uint8Array([OUTLINE_LIVE_MAGIC, 1, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff]),
			),
		).toBeNull();
		expect(() => encodeLiveFrame({ kind: 1, handle: -1 })).toThrow();
	});

	test("head limits by direction: a client frame's head is small, a snapshot's may be large", () => {
		const big = { blocks: "x".repeat(OUTLINE_LIVE_LIMITS.maxHeadBytes + 10) };
		const fromClient = encodeLiveFrame({ kind: LIVE_FRAME.join, handle: 1, head: big });
		const fromServer = encodeLiveFrame({ kind: LIVE_FRAME.joined, handle: 1, head: big });
		expect(decodeLiveFrame(fromClient)).toBeNull();
		expect(decodeLiveFrame(fromServer)?.head).toEqual(big);
		// The server reads every frame with the client limit.
		expect(decodeLiveFrame(fromServer, OUTLINE_LIVE_LIMITS.maxHeadBytes)).toBeNull();
	});

	test("task characters and statuses map both ways", () => {
		for (const [status, char] of Object.entries(LIVE_TASK_OF_STATUS)) {
			expect(LIVE_STATUS_OF_TASK[char]).toBe(status as never);
			expect(isLiveTaskChar(char)).toBe(true);
		}
		expect(isLiveTaskChar("?")).toBe(false);
	});
});
