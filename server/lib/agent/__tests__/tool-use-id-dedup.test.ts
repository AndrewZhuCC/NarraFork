import { describe, expect, test } from "bun:test";
import type { DbMessage } from "../provider";
import {
	allocateUniqueToolUseId,
	isWireSafeToolUseId,
	toWireSafeToolUseId,
	uniquifyDbMessageToolUseIds,
} from "../tool-use-id-dedup";

/** The pattern NUG validates tool ids against. */
const WIRE_PATTERN = /^[a-zA-Z0-9_-]+$/;

function assistantMessage(id: string, toolUseIds: string[]): DbMessage {
	return {
		id,
		role: "assistant",
		contentText: null,
		parentToolUseId: null,
		messageUuid: null,
		contentJson: [
			{ type: "text", text: `msg ${id}` },
			...toolUseIds.map((toolUseId) => ({
				type: "tool_use",
				id: toolUseId,
				name: "Bash",
				input: { command: "echo hi" },
			})),
		],
		toolCalls: toolUseIds.map((toolUseId) => ({
			toolUseId,
			toolName: "Bash",
			inputJson: { command: "echo hi" },
			outputJson: "ok",
			status: "success",
		})),
	};
}

function toolUseIdsOf(msg: DbMessage): string[] {
	return (msg.toolCalls ?? []).map((tc) => tc.toolUseId);
}

function contentToolUseIdsOf(msg: DbMessage): string[] {
	const blocks = Array.isArray(msg.contentJson)
		? (msg.contentJson as Array<Record<string, unknown>>)
		: [];
	return blocks
		.filter((b) => b.type === "tool_use")
		.map((b) => b.id)
		.filter((id): id is string => typeof id === "string");
}

describe("uniquifyDbMessageToolUseIds", () => {
	test("well-behaved history is returned untouched (same array reference)", () => {
		const messages = [assistantMessage("m1", ["call_1"]), assistantMessage("m2", ["call_2"])];
		expect(uniquifyDbMessageToolUseIds(messages)).toBe(messages);
	});

	test("跨消息重复的 tool_use ID 被改名，首次出现保持不变", () => {
		// The grok-4.5 case: every assistant turn reuses "call_go_0".
		const messages = [
			assistantMessage("m1", ["call_go_0"]),
			assistantMessage("m2", ["call_go_0"]),
			assistantMessage("m3", ["call_go_0"]),
		];
		const result = uniquifyDbMessageToolUseIds(messages);

		const all = result.flatMap(toolUseIdsOf);
		expect(all).toHaveLength(3);
		expect(new Set(all).size).toBe(3);
		// First occurrence is stable so older turns keep the id the model already saw.
		expect(all[0]).toBe("call_go_0");
		expect(all[1]).not.toBe("call_go_0");
		expect(all[2]).not.toBe(all[1]);

		// Input is never mutated — the DB rows keep the original ids.
		expect(messages.flatMap(toolUseIdsOf)).toEqual(["call_go_0", "call_go_0", "call_go_0"]);
	});

	test("contentJson 的 tool_use 块与 toolCalls 保持同一 ID", () => {
		const messages = [assistantMessage("m1", ["call_go_0"]), assistantMessage("m2", ["call_go_0"])];
		const result = uniquifyDbMessageToolUseIds(messages);

		for (const msg of result) {
			// Providers look up stored blocks by id against the tool-call rows; a mismatch
			// would silently drop the tool_use from the replayed turn.
			expect(contentToolUseIdsOf(msg)).toEqual(toolUseIdsOf(msg));
		}
	});

	test("同一消息内多个 tool_use 共用一个 ID 时按顺序分配", () => {
		const messages = [assistantMessage("m1", ["call_go_0", "call_go_0", "call_go_0"])];
		const result = uniquifyDbMessageToolUseIds(messages);

		const ids = toolUseIdsOf(result[0]);
		expect(new Set(ids).size).toBe(3);
		expect(contentToolUseIdsOf(result[0])).toEqual(ids);
	});

	test("生成的 ID 保持可辨识且长度受限", () => {
		const long = `call_${"x".repeat(200)}`;
		const id = allocateUniqueToolUseId(long, new Set([long]));
		expect(id.length).toBeLessThanOrEqual(56);
		expect(id).toContain("_nfdup");
	});

	test("大量重复时仍能全部分配到唯一 ID", () => {
		const messages = Array.from({ length: 120 }, (_, i) =>
			assistantMessage(`m${i}`, ["call_go_0"]),
		);
		const all = uniquifyDbMessageToolUseIds(messages).flatMap(toolUseIdsOf);
		expect(all).toHaveLength(120);
		expect(new Set(all).size).toBe(120);
	});

	test("每个候选都被占用时仍然返回，而不是死循环", () => {
		// The allocator's fallback is a random tail, so its exit condition depends on
		// `Math.random` missing a set. That loop runs SYNCHRONOUSLY on the server's only
		// JS thread: unbounded, its worst case is a wedged event loop that takes every
		// session down, and the symptom ("the whole app stopped responding") points
		// nowhere near a tool-id naming helper.
		//
		// A Set that claims to contain EVERYTHING forces the pathological branch on the
		// first probe, which no realistic history can do. Passing means the allocator
		// degrades to a wider id; failing means this test never returns.
		const everything: ReadonlySet<string> = {
			has: () => true,
			size: Number.MAX_SAFE_INTEGER,
			// The allocator only calls `has`; the rest satisfies the type.
			keys: () => [][Symbol.iterator](),
			values: () => [][Symbol.iterator](),
			entries: () => [][Symbol.iterator](),
			forEach: () => {},
			[Symbol.iterator]: () => [][Symbol.iterator](),
		} as unknown as ReadonlySet<string>;

		const id = allocateUniqueToolUseId("call_stuck", everything);

		expect(id).toContain("_nfdup");
		// Still wire-safe and still within the length gateways validate: degrading must
		// not produce an id that the next request rejects.
		expect(id).toMatch(WIRE_PATTERN);
		expect(id.length).toBeLessThanOrEqual(56);
	});

	// --- character set ---

	test("含冒号的 ID 即使不重复也被改写（Bash:0 回归）", () => {
		// Real failure: a session whose early turns were produced by an upstream minting
		// `Bash:0` / `Read:0`. Anthropic accepted them, so they reached the DB; NUG
		// then rejected the whole replayed history with
		// `tool_use.id: String should match pattern '^[a-zA-Z0-9_-]+$'`.
		const messages = [
			assistantMessage("m1", ["Bash:0"]),
			assistantMessage("m2", ["Edit:0"]),
			assistantMessage("m3", ["toolu_01Normal"]),
		];
		const result = uniquifyDbMessageToolUseIds(messages);
		const all = result.flatMap(toolUseIdsOf);

		expect(all).toEqual(["Bash_0", "Edit_0", "toolu_01Normal"]);
		for (const id of all) expect(id).toMatch(WIRE_PATTERN);
		// contentJson must follow, or the provider drops the block when resolving it
		// against the tool-call rows.
		expect(result.flatMap(contentToolUseIdsOf)).toEqual(all);
		// DB rows are untouched.
		expect(messages.flatMap(toolUseIdsOf)).toEqual(["Bash:0", "Edit:0", "toolu_01Normal"]);
	});

	test("净化后撞车的两个不同 ID 仍各自唯一", () => {
		// `a:0` and `a.0` both sanitize to `a_0`; checking uniqueness before sanitizing
		// would let them collide on the wire.
		const messages = [assistantMessage("m1", ["a:0"]), assistantMessage("m2", ["a.0"])];
		const all = uniquifyDbMessageToolUseIds(messages).flatMap(toolUseIdsOf);

		expect(new Set(all).size).toBe(2);
		for (const id of all) expect(id).toMatch(WIRE_PATTERN);
	});

	test("净化与去重同时发生时结果既唯一又合法", () => {
		const messages = [
			assistantMessage("m1", ["Bash:0"]),
			assistantMessage("m2", ["Bash:0"]),
			assistantMessage("m3", ["Bash:0"]),
		];
		const all = uniquifyDbMessageToolUseIds(messages).flatMap(toolUseIdsOf);

		expect(new Set(all).size).toBe(3);
		for (const id of all) expect(id).toMatch(WIRE_PATTERN);
	});

	test("净化是确定性的，同一 ID 每次重建都得到同一替换", () => {
		// Stability is what lets the DB keep the original id: nothing persists the mapping,
		// so a differing replacement between rebuilds would break tool_use ↔ tool_result
		// pairing across a restart.
		const build = () => uniquifyDbMessageToolUseIds([assistantMessage("m1", ["Bash:0"])]);
		expect(build().flatMap(toolUseIdsOf)).toEqual(build().flatMap(toolUseIdsOf));
	});

	test("超长 ID 在 DB 重建路径中被截断，tool_use 块与 tool call 保持配对", () => {
		// End-to-end over the buildHistory path: the Responses API's 64-char call_id
		// ceiling must hold for the whole replayed history, not just freshly minted ids.
		const longId = `call_${"g".repeat(78)}`;
		const messages = [assistantMessage("m1", [longId])];
		const result = uniquifyDbMessageToolUseIds(messages);
		expect(result).not.toBe(messages);
		const [rewrittenId] = toolUseIdsOf(result[0]);
		expect(rewrittenId.length).toBeLessThanOrEqual(64);
		expect(rewrittenId).toMatch(WIRE_PATTERN);
		expect(contentToolUseIdsOf(result[0])).toEqual([rewrittenId]);
	});

	test("已合法的历史保持同一数组引用（无额外拷贝）", () => {
		const messages = [assistantMessage("m1", ["toolu_01a-b_c"]), assistantMessage("m2", ["x"])];
		expect(uniquifyDbMessageToolUseIds(messages)).toBe(messages);
	});
});

describe("toWireSafeToolUseId", () => {
	test("合法 ID 原样返回", () => {
		for (const id of ["toolu_01abc", "call-1", "A_b-9"]) {
			expect(isWireSafeToolUseId(id)).toBe(true);
			expect(toWireSafeToolUseId(id)).toBe(id);
		}
	});

	test("非法字符被替换为下划线", () => {
		expect(toWireSafeToolUseId("Bash:0")).toBe("Bash_0");
		expect(toWireSafeToolUseId("tool.call:1")).toBe("tool_call_1");
		expect(toWireSafeToolUseId("工具:0")).toBe("___0");
	});

	test("全部字符非法时回落到可用名字而非空串", () => {
		// An empty id would be rejected by the same validator (`+` requires ≥1 char).
		expect(toWireSafeToolUseId("")).toBe("tool");
		expect(toWireSafeToolUseId(":::")).toMatch(WIRE_PATTERN);
	});

	test("点号也被视为非法（NUG 通道不接受）", () => {
		expect(isWireSafeToolUseId("call.1")).toBe(false);
		expect(toWireSafeToolUseId("call.1")).toMatch(WIRE_PATTERN);
	});

	test("超长 ID 被确定性截断到 64 字符以内", () => {
		// Observed with grok-4.6 behind an OpenAI-compatible proxy: 82-char call ids that
		// the originating channel accepts but the Responses API (64-char ceiling) rejects
		// on replay after a channel switch.
		const longId = `call_${"a".repeat(77)}`;
		expect(longId.length).toBe(82);
		expect(isWireSafeToolUseId(longId)).toBe(false);
		const rewritten = toWireSafeToolUseId(longId);
		expect(rewritten.length).toBeLessThanOrEqual(64);
		expect(rewritten).toMatch(WIRE_PATTERN);
		expect(rewritten).toContain("_nfh");
		// Deterministic: history rebuilds must produce the same replacement every time.
		expect(toWireSafeToolUseId(longId)).toBe(rewritten);
	});

	test("共享前缀的两个超长 ID 截断后仍然不同", () => {
		// Plain truncation would collapse these; the hash suffix keeps them apart.
		const first = toWireSafeToolUseId(`call_${"a".repeat(76)}x`);
		const second = toWireSafeToolUseId(`call_${"a".repeat(76)}y`);
		expect(first).not.toBe(second);
	});

	test("恰好 64 字符的合法 ID 原样返回", () => {
		const id = "c".repeat(64);
		expect(isWireSafeToolUseId(id)).toBe(true);
		expect(toWireSafeToolUseId(id)).toBe(id);
	});
});
