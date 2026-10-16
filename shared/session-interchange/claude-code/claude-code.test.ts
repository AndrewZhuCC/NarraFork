import { describe, expect, test } from "bun:test";
import type { InterchangeSession } from "../model";
import { parseClaudeCodeFile, parseClaudeCodeSession } from "./parse";
import {
	claudeCodeSessionIdFromFileName,
	claudeCodeSessionLayout,
	sanitizeClaudeCodePath,
} from "./paths";
import { EXPORT_TAIL_TEXT, serializeClaudeCodeSession } from "./serialize";

const SESSION = "3b1e0c9a-7f2d-4e6b-8a1c-0000000000aa";

function row(fields: Record<string, unknown>): string {
	return JSON.stringify({ isSidechain: false, sessionId: SESSION, cwd: "/work/app", ...fields });
}

function counter(prefix: string) {
	let n = 0;
	return () => `${prefix}-${String(++n).padStart(4, "0")}`;
}

describe("sanitizeClaudeCodePath", () => {
	test("replaces every non-alphanumeric character with a dash", () => {
		expect(sanitizeClaudeCodePath("/home/u/my-project")).toBe("-home-u-my-project");
		expect(sanitizeClaudeCodePath("C:\\Users\\a b")).toBe("C--Users-a-b");
	});

	test("truncates over-long paths and appends the injected hash", () => {
		const long = `/${"a".repeat(250)}`;
		const out = sanitizeClaudeCodePath(long, () => "h4sh");
		expect(out).toBe(`-${"a".repeat(199)}-h4sh`);
	});

	test("only UUID file names are sessions", () => {
		expect(claudeCodeSessionIdFromFileName(`${SESSION}.jsonl`)).toBe(SESSION);
		expect(claudeCodeSessionIdFromFileName("notes.jsonl")).toBeNull();
		expect(claudeCodeSessionIdFromFileName(`${SESSION}.json`)).toBeNull();
	});

	test("layout matches Claude Code's projects/<cwd>/<id>.jsonl", () => {
		const layout = claudeCodeSessionLayout("/work/app", SESSION);
		expect(layout.transcript).toEqual(["projects", "-work-app", `${SESSION}.jsonl`]);
		expect(layout.subagentsDir).toEqual(["projects", "-work-app", SESSION, "subagents"]);
	});
});

describe("parseClaudeCodeFile", () => {
	test("follows the newest leaf and drops an abandoned branch", () => {
		const lines = [
			row({
				uuid: "u1",
				parentUuid: null,
				type: "user",
				message: { role: "user", content: "hi" },
				timestamp: "2026-01-01T00:00:00.000Z",
			}),
			// Abandoned retry off u1.
			row({
				uuid: "a0",
				parentUuid: "u1",
				type: "assistant",
				message: { id: "m0", role: "assistant", content: [{ type: "text", text: "old" }] },
				timestamp: "2026-01-01T00:00:01.000Z",
			}),
			row({
				uuid: "a1",
				parentUuid: "u1",
				type: "assistant",
				message: {
					id: "m1",
					role: "assistant",
					model: "claude-x",
					content: [{ type: "text", text: "new" }],
				},
				timestamp: "2026-01-01T00:00:02.000Z",
			}),
		];
		const parsed = parseClaudeCodeFile(lines);
		expect(parsed.messages.map((m) => m.role)).toEqual(["user", "assistant"]);
		const assistant = parsed.messages[1];
		expect(assistant?.role === "assistant" && assistant.blocks).toEqual([
			{ type: "text", text: "new" },
		]);
		expect(parsed.model).toBe("claude-x");
		expect(parsed.warnings.list()).toEqual([{ kind: "off_chain_line", count: 1 }]);
	});

	test("merges streamed rows sharing message.id and recovers parallel tool results", () => {
		const lines = [
			row({
				uuid: "u1",
				parentUuid: null,
				type: "user",
				message: { role: "user", content: "run both" },
				timestamp: "2026-01-01T00:00:00.000Z",
			}),
			row({
				uuid: "a1",
				parentUuid: "u1",
				type: "assistant",
				message: {
					id: "m1",
					role: "assistant",
					content: [{ type: "tool_use", id: "t1", name: "Read", input: { file_path: "a" } }],
				},
				timestamp: "2026-01-01T00:00:01.000Z",
			}),
			row({
				uuid: "a2",
				parentUuid: "a1",
				type: "assistant",
				message: {
					id: "m1",
					role: "assistant",
					content: [{ type: "tool_use", id: "t2", name: "Read", input: { file_path: "b" } }],
				},
				timestamp: "2026-01-01T00:00:01.001Z",
			}),
			// Each result hangs off its own one-block assistant row: a DAG.
			row({
				uuid: "r1",
				parentUuid: "a1",
				type: "user",
				message: {
					role: "user",
					content: [{ type: "tool_result", tool_use_id: "t1", content: "A" }],
				},
				timestamp: "2026-01-01T00:00:02.000Z",
			}),
			row({
				uuid: "r2",
				parentUuid: "a2",
				type: "user",
				message: {
					role: "user",
					content: [{ type: "tool_result", tool_use_id: "t2", content: "B" }],
				},
				timestamp: "2026-01-01T00:00:02.001Z",
			}),
			row({
				uuid: "a3",
				parentUuid: "r2",
				type: "assistant",
				message: { id: "m2", role: "assistant", content: [{ type: "text", text: "done" }] },
				timestamp: "2026-01-01T00:00:03.000Z",
			}),
		];
		const parsed = parseClaudeCodeFile(lines);
		const roles = parsed.messages.map((m) => m.role);
		expect(roles).toEqual(["user", "assistant", "tool_result", "tool_result", "assistant"]);
		const first = parsed.messages[1];
		expect(
			first?.role === "assistant" && first.blocks.map((b) => (b as { id: string }).id),
		).toEqual(["t1", "t2"]);
		expect(parsed.warnings.list()).toEqual([]);
	});

	test("bridges legacy progress rows in the parent chain", () => {
		const lines = [
			row({
				uuid: "u1",
				parentUuid: null,
				type: "user",
				message: { role: "user", content: "go" },
				timestamp: "2026-01-01T00:00:00.000Z",
			}),
			row({
				uuid: "p1",
				parentUuid: "u1",
				type: "progress",
				data: {},
				timestamp: "2026-01-01T00:00:00.500Z",
			}),
			row({
				uuid: "a1",
				parentUuid: "p1",
				type: "assistant",
				message: { id: "m1", role: "assistant", content: [{ type: "text", text: "ok" }] },
				timestamp: "2026-01-01T00:00:01.000Z",
			}),
		];
		expect(parseClaudeCodeFile(lines).messages.map((m) => m.role)).toEqual(["user", "assistant"]);
	});

	test("crosses compact boundaries, falling back to file order when the logical parent is missing", () => {
		const lines = [
			row({
				uuid: "u1",
				parentUuid: null,
				type: "user",
				message: { role: "user", content: "before" },
				timestamp: "2026-01-01T00:00:00.000Z",
			}),
			row({
				uuid: "a1",
				parentUuid: "u1",
				type: "assistant",
				message: { id: "m1", role: "assistant", content: [{ type: "text", text: "a" }] },
				timestamp: "2026-01-01T00:00:01.000Z",
			}),
			row({
				uuid: "b1",
				parentUuid: null,
				logicalParentUuid: "never-written",
				type: "system",
				subtype: "compact_boundary",
				content: "Conversation compacted",
				timestamp: "2026-01-01T00:00:02.000Z",
			}),
			row({
				uuid: "s1",
				parentUuid: "b1",
				type: "user",
				isCompactSummary: true,
				message: { role: "user", content: "SUMMARY" },
				timestamp: "2026-01-01T00:00:02.001Z",
			}),
			row({
				uuid: "u2",
				parentUuid: "s1",
				type: "user",
				message: { role: "user", content: "after" },
				timestamp: "2026-01-01T00:00:03.000Z",
			}),
		];
		const parsed = parseClaudeCodeFile(lines);
		expect(parsed.messages.map((m) => m.role)).toEqual(["user", "assistant", "compact", "user"]);
		const compact = parsed.messages[2];
		expect(compact?.role === "compact" && compact.summary).toBe("SUMMARY");
	});

	test("skips bookkeeping rows, interrupt markers and synthetic assistants", () => {
		const lines = [
			row({
				uuid: "u0",
				parentUuid: null,
				type: "user",
				isMeta: true,
				message: { role: "user", content: "<local-command-caveat>x</local-command-caveat>" },
				timestamp: "2026-01-01T00:00:00.000Z",
			}),
			row({
				uuid: "u1",
				parentUuid: "u0",
				type: "user",
				message: { role: "user", content: "<command-name>/clear</command-name>" },
				timestamp: "2026-01-01T00:00:00.100Z",
			}),
			row({
				uuid: "u2",
				parentUuid: "u1",
				type: "user",
				message: { role: "user", content: "real prompt" },
				timestamp: "2026-01-01T00:00:00.200Z",
			}),
			row({
				uuid: "u3",
				parentUuid: "u2",
				type: "user",
				message: {
					role: "user",
					content: [{ type: "text", text: "[Request interrupted by user]" }],
				},
				timestamp: "2026-01-01T00:00:00.300Z",
			}),
			row({
				uuid: "a1",
				parentUuid: "u3",
				type: "assistant",
				message: {
					id: "m1",
					role: "assistant",
					model: "<synthetic>",
					content: [{ type: "text", text: "No response requested." }],
				},
				timestamp: "2026-01-01T00:00:00.400Z",
			}),
		];
		const parsed = parseClaudeCodeFile(lines);
		expect(parsed.messages).toHaveLength(1);
		const only = parsed.messages[0];
		expect(only?.role === "user" && only.blocks).toEqual([{ type: "text", text: "real prompt" }]);
	});

	test("keeps thinking with its signature and drops redacted thinking", () => {
		const lines = [
			row({
				uuid: "u1",
				parentUuid: null,
				type: "user",
				message: { role: "user", content: "q" },
				timestamp: "2026-01-01T00:00:00.000Z",
			}),
			row({
				uuid: "a1",
				parentUuid: "u1",
				type: "assistant",
				message: {
					id: "m1",
					role: "assistant",
					content: [
						{ type: "thinking", thinking: "hmm", signature: "SIG" },
						{ type: "redacted_thinking", data: "x" },
						{ type: "text", text: "a" },
					],
				},
				timestamp: "2026-01-01T00:00:01.000Z",
			}),
		];
		const parsed = parseClaudeCodeFile(lines);
		const assistant = parsed.messages[1];
		expect(assistant?.role === "assistant" && assistant.blocks).toEqual([
			{
				type: "reasoning",
				text: "hmm",
				// Claude Code only talks to the official API, so its signatures are official.
				signature: { provider: "anthropic", value: "SIG", officialAnthropic: true },
			},
			{ type: "text", text: "a" },
		]);
		expect(parsed.warnings.list()).toEqual([
			{ kind: "dropped_reasoning", count: 1, sample: "redacted_thinking" },
		]);
	});

	test("reads titles from metadata rows and ignores unparseable lines", () => {
		const lines = [
			"{not json",
			row({
				uuid: "u1",
				parentUuid: null,
				type: "user",
				message: { role: "user", content: "q" },
				timestamp: "2026-01-01T00:00:00.000Z",
			}),
			JSON.stringify({ type: "custom-title", customTitle: "My title", sessionId: SESSION }),
		];
		const parsed = parseClaudeCodeFile(lines);
		expect(parsed.customTitle).toBe("My title");
		expect(parsed.warnings.list().map((w) => w.kind)).toEqual(["unparseable_line"]);
	});
	test("a resumed session is identified by its leaf, not by rows copied from the old one", () => {
		const OLD = "3b1e0c9a-7f2d-4e6b-8a1c-0000000000bb";
		const lines = [
			row({
				sessionId: OLD,
				uuid: "u0",
				parentUuid: null,
				type: "user",
				message: { role: "user", content: "old question" },
				timestamp: "2026-01-01T00:00:00.000Z",
			}),
			JSON.stringify({ type: "custom-title", customTitle: "Old title", sessionId: OLD }),
			row({
				uuid: "u1",
				parentUuid: "u0",
				type: "user",
				message: { role: "user", content: "continue" },
				timestamp: "2026-01-02T00:00:00.000Z",
			}),
			JSON.stringify({ type: "custom-title", customTitle: "New title", sessionId: SESSION }),
		];
		const parsed = parseClaudeCodeFile(lines);
		expect(parsed.sessionId).toBe(SESSION);
		expect(parsed.customTitle).toBe("New title");
		expect(parseClaudeCodeSession({ main: lines }).externalId).toBe(SESSION);
	});
});

describe("parseClaudeCodeSession subagents", () => {
	const main = [
		row({
			uuid: "u1",
			parentUuid: null,
			type: "user",
			message: { role: "user", content: "explore" },
			timestamp: "2026-01-01T00:00:00.000Z",
		}),
		row({
			uuid: "a1",
			parentUuid: "u1",
			type: "assistant",
			message: {
				id: "m1",
				role: "assistant",
				content: [
					{
						type: "tool_use",
						id: "call-1",
						name: "Task",
						input: { description: "Look", prompt: "find it", subagent_type: "Explore" },
					},
				],
			},
			timestamp: "2026-01-01T00:00:01.000Z",
		}),
		row({
			uuid: "r1",
			parentUuid: "a1",
			type: "user",
			message: {
				role: "user",
				content: [{ type: "tool_result", tool_use_id: "call-1", content: "found" }],
			},
			toolUseResult: { agentId: "a0123", status: "completed" },
			timestamp: "2026-01-01T00:00:05.000Z",
		}),
		row({
			uuid: "a2",
			parentUuid: "r1",
			type: "assistant",
			message: { id: "m2", role: "assistant", content: [{ type: "text", text: "ok" }] },
			timestamp: "2026-01-01T00:00:06.000Z",
		}),
	];
	const side = [
		row({
			uuid: "s1",
			parentUuid: null,
			isSidechain: true,
			agentId: "a0123",
			type: "user",
			message: { role: "user", content: "find it" },
			timestamp: "2026-01-01T00:00:01.500Z",
		}),
		row({
			uuid: "s2",
			parentUuid: "s1",
			isSidechain: true,
			agentId: "a0123",
			type: "assistant",
			message: { id: "sm1", role: "assistant", content: [{ type: "text", text: "found" }] },
			timestamp: "2026-01-01T00:00:04.000Z",
		}),
	];

	test("links a sidechain to its spawning call and renames Task to Agent", () => {
		const session = parseClaudeCodeSession({ main, subagents: [{ lines: side }] });
		expect(session.subagents).toHaveLength(1);
		const sub = session.subagents[0];
		expect(sub).toMatchObject({
			ref: "a0123",
			parentToolUseId: "call-1",
			subagentType: "Explore",
			title: "Look",
		});
		expect(sub?.messages.map((m) => m.role)).toEqual(["user", "assistant"]);
		const call = session.messages[1];
		const use = call?.role === "assistant" ? call.blocks[0] : undefined;
		expect(use).toMatchObject({ type: "tool_use", name: "Agent", subagentRef: "a0123" });
	});

	test("reports a sidechain with no parent call", () => {
		const orphan = side.map((line) => line.replaceAll("a0123", "zzz"));
		const session = parseClaudeCodeSession({ main, subagents: [{ lines: orphan }] });
		expect(session.subagents).toEqual([]);
		expect(session.warnings.some((w) => w.kind === "missing_subagent")).toBe(true);
	});
	test("an interrupted Task is still linked to its sidechain through progress rows", () => {
		const main = [
			row({
				uuid: "u1",
				parentUuid: null,
				type: "user",
				message: { role: "user", content: "look around" },
				timestamp: "2026-01-01T00:00:00.000Z",
			}),
			row({
				uuid: "a1",
				parentUuid: "u1",
				type: "assistant",
				message: {
					id: "m1",
					role: "assistant",
					model: "claude-x",
					content: [{ type: "tool_use", id: "t1", name: "Task", input: { prompt: "look" } }],
				},
				timestamp: "2026-01-01T00:00:01.000Z",
			}),
			JSON.stringify({
				type: "progress",
				uuid: "p1",
				parentUuid: "a1",
				parentToolUseID: "t1",
				data: { agentId: "aint" },
				sessionId: SESSION,
				timestamp: "2026-01-01T00:00:01.500Z",
			}),
			// Interrupted: the result names no agentId.
			row({
				uuid: "r1",
				parentUuid: "a1",
				type: "user",
				message: {
					role: "user",
					content: [
						{
							type: "tool_result",
							tool_use_id: "t1",
							content: "[Request interrupted by user for tool use]",
						},
					],
				},
				timestamp: "2026-01-01T00:00:02.000Z",
			}),
		];
		const side = [
			row({
				uuid: "s1",
				parentUuid: null,
				isSidechain: true,
				agentId: "aint",
				type: "user",
				message: { role: "user", content: "look" },
				timestamp: "2026-01-01T00:00:01.100Z",
			}),
		];
		const session = parseClaudeCodeSession({ main, subagents: [{ lines: side }] });
		expect(session.subagents.map((s) => s.parentToolUseId)).toEqual(["t1"]);
	});
});

describe("serializeClaudeCodeSession", () => {
	const base: InterchangeSession = {
		source: "narrafork",
		externalId: "nf-1",
		title: "Fix it",
		cwd: "/work/app",
		createdAt: "2026-01-01T00:00:00.000Z",
		updatedAt: "2026-01-01T00:00:00.000Z",
		messages: [],
		subagents: [],
		warnings: [],
	};
	const options = () => ({
		sessionId: SESSION,
		cwd: "/work/app",
		newUuid: counter("uuid"),
		newAgentId: counter("agent"),
	});

	test("writes a single strictly-increasing chain with metadata rows last", () => {
		const out = serializeClaudeCodeSession(
			{
				...base,
				messages: [
					{
						role: "user",
						timestamp: "2026-01-01T00:00:00.000Z",
						blocks: [{ type: "text", text: "go" }],
					},
					{
						role: "assistant",
						timestamp: "2026-01-01T00:00:00.000Z",
						blocks: [
							{ type: "reasoning", text: "unsigned" },
							{ type: "text", text: "sure" },
							{ type: "tool_use", id: "t1", name: "Bash", input: { command: "ls" } },
						],
					},
					{
						role: "tool_result",
						timestamp: "2026-01-01T00:00:00.000Z",
						toolUseId: "t1",
						output: "a.txt",
						isError: false,
					},
					{
						role: "assistant",
						timestamp: "2026-01-01T00:00:00.000Z",
						blocks: [{ type: "text", text: "done" }],
					},
				],
			},
			options(),
		);
		const rows = out.lines.map((line) => JSON.parse(line));
		const chain = rows.filter((r) => r.uuid);
		expect(chain.map((r) => r.type)).toEqual(["user", "assistant", "user", "assistant"]);
		for (let i = 1; i < chain.length; i++) {
			expect(chain[i].parentUuid).toBe(chain[i - 1].uuid);
			expect(Date.parse(chain[i].timestamp)).toBeGreaterThan(Date.parse(chain[i - 1].timestamp));
		}
		expect(chain[0].parentUuid).toBeNull();
		expect(chain[1].message.content.map((b: { type: string }) => b.type)).toEqual([
			"text",
			"tool_use",
		]);
		expect(chain[2].sourceToolAssistantUUID).toBe(chain[1].uuid);
		expect(out.lines.at(-2)?.startsWith('{"type":"custom-title"')).toBe(true);
		expect(out.lines.at(-1)?.startsWith('{"type":"last-prompt"')).toBe(true);
		expect(out.warnings).toEqual([{ kind: "dropped_reasoning", count: 1 }]);
	});

	test("synthesizes a result for an unpaired call and ends on assistant text", () => {
		const out = serializeClaudeCodeSession(
			{
				...base,
				messages: [
					{
						role: "user",
						timestamp: "2026-01-01T00:00:00.000Z",
						blocks: [{ type: "text", text: "go" }],
					},
					{
						role: "assistant",
						timestamp: "2026-01-01T00:00:01.000Z",
						blocks: [{ type: "tool_use", id: "t1", name: "Bash", input: { command: "sleep 9" } }],
					},
				],
			},
			options(),
		);
		const chain = out.lines.map((line) => JSON.parse(line)).filter((r) => r.uuid);
		expect(chain.map((r) => r.type)).toEqual(["user", "assistant", "user", "assistant"]);
		expect(chain[2].message.content[0]).toMatchObject({ tool_use_id: "t1", is_error: true });
		expect(chain[3].message.content[0].text).toBe(EXPORT_TAIL_TEXT);
		expect(out.warnings).toEqual([{ kind: "unpaired_tool_use", count: 1, sample: "t1" }]);
	});

	test("keeps signed Anthropic thinking and a compact boundary", () => {
		const out = serializeClaudeCodeSession(
			{
				...base,
				messages: [
					{
						role: "user",
						timestamp: "2026-01-01T00:00:00.000Z",
						blocks: [{ type: "text", text: "go" }],
					},
					{
						role: "assistant",
						timestamp: "2026-01-01T00:00:01.000Z",
						blocks: [
							{
								type: "reasoning",
								text: "t",
								signature: { provider: "anthropic", value: "SIG", officialAnthropic: true },
							},
							{ type: "text", text: "a" },
						],
					},
					{ role: "compact", timestamp: "2026-01-01T00:00:02.000Z", summary: "S" },
					{
						role: "user",
						timestamp: "2026-01-01T00:00:03.000Z",
						blocks: [{ type: "text", text: "again" }],
					},
					{
						role: "assistant",
						timestamp: "2026-01-01T00:00:04.000Z",
						blocks: [{ type: "text", text: "b" }],
					},
				],
			},
			options(),
		);
		const chain = out.lines.map((line) => JSON.parse(line)).filter((r) => r.uuid);
		expect(chain[1].message.content[0]).toEqual({
			type: "thinking",
			thinking: "t",
			signature: "SIG",
		});
		const boundary = chain.find((r) => r.subtype === "compact_boundary");
		expect(boundary.parentUuid).toBeNull();
		expect(boundary.logicalParentUuid).toBe(chain[1].uuid);
		// Re-parsing restores the same sequence.
		const reparsed = parseClaudeCodeSession({ main: out.lines });
		expect(reparsed.messages.map((m) => m.role)).toEqual([
			"user",
			"assistant",
			"compact",
			"user",
			"assistant",
		]);
	});

	test("a signature not proven to come from the official API is dropped, not replayed", () => {
		// e.g. minted by a NUG channel or a relay: the official API rejects it with a
		// 400 on every turn of the resumed session.
		const out = serializeClaudeCodeSession(
			{
				...base,
				messages: [
					{
						role: "user",
						timestamp: "2026-01-01T00:00:00.000Z",
						blocks: [{ type: "text", text: "go" }],
					},
					{
						role: "assistant",
						timestamp: "2026-01-01T00:00:01.000Z",
						blocks: [
							{
								type: "reasoning",
								text: "t",
								signature: { provider: "anthropic", value: "RELAY" },
							},
							{ type: "text", text: "a" },
						],
					},
				],
			},
			options(),
		);
		expect(out.lines.join("\n")).not.toContain("RELAY");
		expect(out.warnings.map((w) => w.kind)).toContain("dropped_reasoning");
	});

	test("writes sidechains that re-link to their call", () => {
		const out = serializeClaudeCodeSession(
			{
				...base,
				messages: [
					{
						role: "user",
						timestamp: "2026-01-01T00:00:00.000Z",
						blocks: [{ type: "text", text: "go" }],
					},
					{
						role: "assistant",
						timestamp: "2026-01-01T00:00:01.000Z",
						blocks: [
							{
								type: "tool_use",
								id: "call-1",
								name: "Agent",
								input: { prompt: "look", subagent_type: "explore" },
								subagentRef: "nf-sub",
							},
						],
					},
					{
						role: "tool_result",
						timestamp: "2026-01-01T00:00:02.000Z",
						toolUseId: "call-1",
						output: "found",
						isError: false,
					},
					{
						role: "assistant",
						timestamp: "2026-01-01T00:00:03.000Z",
						blocks: [{ type: "text", text: "ok" }],
					},
				],
				subagents: [
					{
						ref: "nf-sub",
						parentToolUseId: "call-1",
						subagentType: "explore",
						messages: [
							{
								role: "user",
								timestamp: "2026-01-01T00:00:01.100Z",
								blocks: [{ type: "text", text: "look" }],
							},
							{
								role: "assistant",
								timestamp: "2026-01-01T00:00:01.900Z",
								blocks: [{ type: "text", text: "found" }],
							},
						],
					},
				],
			},
			options(),
		);
		expect(out.subagents).toHaveLength(1);
		const sub = out.subagents[0];
		expect(sub?.meta).toBe(JSON.stringify({ agentType: "explore" }));
		const sideRows = sub?.lines.map((line) => JSON.parse(line)) ?? [];
		expect(sideRows.every((r) => r.isSidechain === true && r.agentId === sub?.agentId)).toBe(true);
		const reparsed = parseClaudeCodeSession({
			main: out.lines,
			subagents: [{ agentId: sub?.agentId, lines: sub?.lines ?? [], agentType: sub?.agentType }],
		});
		expect(reparsed.subagents.map((s) => s.parentToolUseId)).toEqual(["call-1"]);
	});
});
