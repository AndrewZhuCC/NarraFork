import { describe, expect, test } from "bun:test";
import type { InterchangeSession } from "../model";
import { isCodexContextualUserText, parseCodexRollout, parseCodexSession } from "./parse";
import { codexRolloutPath, newUuidV7, parseCodexRolloutFileName } from "./paths";
import { codexSessionIndexLine, serializeCodexSession } from "./serialize";

const THREAD = "019e41a4-cfbe-7e80-8052-769b6f1fa3af";
const CHILD = "019e41a4-cfbe-7e80-8052-000000000c01";

function line(
	type: string,
	payload: Record<string, unknown>,
	timestamp = "2026-05-19T19:09:40.721Z",
) {
	return JSON.stringify({ timestamp, type, payload });
}

function meta(extra: Record<string, unknown> = {}) {
	return line("session_meta", {
		id: THREAD,
		timestamp: "2026-05-19T19:09:40.691Z",
		cwd: "/work/app",
		originator: "codex_cli_rs",
		cli_version: "0.111.0",
		source: "cli",
		model_provider: "openai",
		git: { branch: "main" },
		...extra,
	});
}

function userItem(text: string) {
	return line("response_item", {
		type: "message",
		role: "user",
		content: [{ type: "input_text", text }],
	});
}

function counter(prefix: string) {
	let n = 0;
	return () => `${prefix}-${++n}`;
}

describe("codex paths", () => {
	test("parses plain, reverted and compressed rollout names", () => {
		expect(parseCodexRolloutFileName(`rollout-2026-05-20T03-09-40-${THREAD}.jsonl`)).toEqual({
			stamp: "2026-05-20T03-09-40",
			threadId: THREAD,
			rolloutId: THREAD,
			compressed: false,
		});
		expect(
			parseCodexRolloutFileName(`rollout-2026-05-20T03-09-40-${THREAD}_${CHILD}.jsonl.zst`),
		).toMatchObject({
			threadId: THREAD,
			rolloutId: CHILD,
			compressed: true,
		});
		expect(parseCodexRolloutFileName("rollout-bad.jsonl")).toBeNull();
	});

	test("builds sessions/YYYY/MM/DD/rollout-… from date parts", () => {
		expect(
			codexRolloutPath(THREAD, { year: 2026, month: 5, day: 9, hour: 3, minute: 4, second: 5 }),
		).toEqual(["sessions", "2026", "05", "09", `rollout-2026-05-09T03-04-05-${THREAD}.jsonl`]);
	});

	test("mints UUIDv7 ids carrying the timestamp", () => {
		const id = newUuidV7(0x0123456789ab, (n) => new Uint8Array(n).fill(0xff));
		expect(id).toBe("01234567-89ab-7fff-bfff-ffffffffffff");
	});
});

describe("isCodexContextualUserText", () => {
	test("recognizes Codex's injected context and nothing else", () => {
		for (const text of [
			"# AGENTS.md instructions for /work\n\n<INSTRUCTIONS>\nbody\n</INSTRUCTIONS>",
			"<environment_context>\n  <cwd>/work</cwd>\n</environment_context>",
			"<turn_aborted>x</turn_aborted>",
			"<external_browser_info>tab</external_browser_info>",
			'<hook_prompt hook_run_id="h">retry</hook_prompt>',
		]) {
			expect(isCodexContextualUserText(text)).toBe(true);
		}
		expect(isCodexContextualUserText("please read <environment_context> docs")).toBe(false);
		expect(isCodexContextualUserText("<external_a>x</external_b>")).toBe(false);
	});
});

describe("parseCodexRollout", () => {
	test("drops injected context and reads model, cwd and branch", () => {
		const parsed = parseCodexRollout([
			meta(),
			line("response_item", {
				type: "message",
				role: "developer",
				content: [{ type: "input_text", text: "<permissions instructions>…" }],
			}),
			line("response_item", {
				type: "message",
				role: "user",
				content: [
					{
						type: "input_text",
						text: "# AGENTS.md instructions for /work\n<INSTRUCTIONS>x</INSTRUCTIONS>",
					},
					{ type: "input_text", text: "<environment_context>x</environment_context>" },
				],
			}),
			line("turn_context", { model: "gpt-5.1-codex", cwd: "/work/app" }),
			line("event_msg", { type: "user_message", message: "translate this" }),
			userItem("translate this"),
			line("response_item", {
				type: "message",
				role: "assistant",
				content: [{ type: "output_text", text: "done" }],
			}),
		]);
		expect(parsed).toMatchObject({
			threadId: THREAD,
			cwd: "/work/app",
			gitBranch: "main",
			model: "gpt-5.1-codex",
			modelProvider: "openai",
			firstUserMessage: "translate this",
		});
		expect(parsed.messages.map((m) => m.role)).toEqual(["user", "assistant"]);
		expect(parsed.warnings.list()).toEqual([{ kind: "dropped_injected_context", count: 2 }]);
	});

	test("maps calls, groups one response, and infers failures from exit codes", () => {
		const parsed = parseCodexRollout([
			meta(),
			userItem("list and patch"),
			line("response_item", {
				type: "reasoning",
				summary: [{ type: "summary_text", text: "plan" }],
				encrypted_content: "opaque",
			}),
			line("response_item", {
				type: "function_call",
				name: "exec_command",
				arguments: '{"cmd":"ls"}',
				call_id: "c1",
			}),
			line("response_item", {
				type: "custom_tool_call",
				name: "apply_patch",
				input: "*** Begin Patch",
				call_id: "c2",
			}),
			line("response_item", {
				type: "function_call_output",
				call_id: "c1",
				output: "Process exited with code 2\nboom",
			}),
			line("response_item", { type: "custom_tool_call_output", call_id: "c2", output: "Success" }),
			line("response_item", {
				type: "message",
				role: "assistant",
				content: [{ type: "output_text", text: "ok" }],
			}),
		]);
		expect(parsed.messages.map((m) => m.role)).toEqual([
			"user",
			"assistant",
			"tool_result",
			"tool_result",
			"assistant",
		]);
		const response = parsed.messages[1];
		expect(response?.role === "assistant" && response.blocks).toEqual([
			{ type: "reasoning", text: "plan" },
			{
				type: "tool_use",
				id: "c1",
				name: "Bash",
				input: { command: "ls", _originalToolName: "exec_command" },
			},
			{
				type: "tool_use",
				id: "c2",
				name: "apply_patch",
				// Tagged so the export writes it back as a custom_tool_call.
				input: { input: "*** Begin Patch", _originalToolName: "apply_patch" },
			},
		]);
		const [failed, ok] = [parsed.messages[2], parsed.messages[3]];
		expect(failed?.role === "tool_result" && failed.isError).toBe(true);
		expect(ok?.role === "tool_result" && ok.isError).toBe(false);
	});

	test("converts the legacy shell array form", () => {
		const parsed = parseCodexRollout([
			meta(),
			userItem("x"),
			line("response_item", {
				type: "local_shell_call",
				call_id: "s1",
				status: "completed",
				action: { type: "exec", command: ["bash", "-lc", "echo hi"] },
			}),
		]);
		const call = parsed.messages[1];
		expect(call?.role === "assistant" && call.blocks[0]).toMatchObject({
			name: "Bash",
			input: { command: "echo hi" },
		});
	});

	test("applies thread_rolled_back and keeps compaction as a marker", () => {
		const parsed = parseCodexRollout([
			meta(),
			userItem("one"),
			line("response_item", {
				type: "message",
				role: "assistant",
				content: [{ type: "output_text", text: "1" }],
			}),
			userItem("two"),
			line("response_item", {
				type: "message",
				role: "assistant",
				content: [{ type: "output_text", text: "2" }],
			}),
			line("event_msg", { type: "thread_rolled_back", num_turns: 1 }),
			line("compacted", { message: "summary" }),
			userItem("three"),
		]);
		expect(
			parsed.messages.map((m) =>
				m.role === "user" ? (m.blocks[0] as { text: string }).text : m.role,
			),
		).toEqual(["one", "assistant", "compact", "three"]);
		expect(parsed.warnings.list()).toContainEqual({ kind: "rolled_back_turns", count: 1 });
	});
});

describe("parseCodexSession children", () => {
	test("attaches a child thread through spawn_agent's agent_id", () => {
		const session = parseCodexSession({
			main: [
				meta(),
				userItem("delegate"),
				line("response_item", {
					type: "function_call",
					name: "spawn_agent",
					arguments: '{"message":"look","agent_type":"explorer"}',
					call_id: "sp1",
				}),
				line("response_item", {
					type: "function_call_output",
					call_id: "sp1",
					output: `{"agent_id":"${CHILD}","nickname":"Scout"}`,
				}),
			],
			children: [
				{
					lines: [
						line("session_meta", {
							id: CHILD,
							timestamp: "2026-05-19T19:09:41.000Z",
							cwd: "/work/app",
							originator: "codex_cli_rs",
							cli_version: "0.111.0",
							source: {
								subagent: {
									thread_spawn: {
										parent_thread_id: THREAD,
										depth: 1,
										agent_role: "explorer",
										agent_nickname: "Scout",
									},
								},
							},
						}),
						userItem("look"),
						line("response_item", {
							type: "message",
							role: "assistant",
							content: [{ type: "output_text", text: "seen" }],
						}),
					],
				},
			],
		});
		expect(session.subagents).toHaveLength(1);
		expect(session.subagents[0]).toMatchObject({
			ref: CHILD,
			parentToolUseId: "sp1",
			subagentType: "explorer",
			title: "Scout",
		});
		const call = session.messages[1];
		expect(call?.role === "assistant" && call.blocks[0]).toMatchObject({
			name: "Agent",
			subagentRef: CHILD,
		});
	});
});

describe("serializeCodexSession", () => {
	const base: InterchangeSession = {
		source: "narrafork",
		externalId: "nf-1",
		title: "Fix it",
		cwd: "/work/app",
		createdAt: "2026-01-01T00:00:00.000Z",
		updatedAt: "2026-01-01T00:00:00.000Z",
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
					{ type: "reasoning", text: "think" },
					{ type: "text", text: "running" },
					{ type: "tool_use", id: "t1", name: "Bash", input: { command: "ls" } },
					{ type: "tool_use", id: "t2", name: "Read", input: { file_path: "a" } },
				],
			},
			{
				role: "tool_result",
				timestamp: "2026-01-01T00:00:02.000Z",
				toolUseId: "t1",
				output: "a",
				isError: false,
			},
			{
				role: "tool_result",
				timestamp: "2026-01-01T00:00:02.000Z",
				toolUseId: "t2",
				output: "b",
				isError: false,
			},
			{
				role: "assistant",
				timestamp: "2026-01-01T00:00:03.000Z",
				blocks: [{ type: "text", text: "done" }],
			},
		],
		subagents: [],
		warnings: [],
	};
	const options = () => ({
		threadId: THREAD,
		cwd: "/work/app",
		modelProvider: "openai",
		newThreadId: () => CHILD,
		newTurnId: counter("turn"),
	});

	test("writes session_meta first and both event_msg and response_item rows per turn", () => {
		const out = serializeCodexSession(base, options());
		const rows = out.main.lines.map((l) => JSON.parse(l));
		expect(rows[0]).toMatchObject({
			type: "session_meta",
			payload: {
				id: THREAD,
				cwd: "/work/app",
				source: "cli",
				model_provider: "openai",
				history_mode: "legacy",
			},
		});
		expect(rows.every((r) => !("ordinal" in r))).toBe(true);
		const kinds = rows.slice(1).map((r) => `${r.type}:${r.payload.type}`);
		expect(kinds).toEqual([
			"event_msg:task_started",
			"event_msg:user_message",
			"response_item:message",
			"event_msg:agent_message",
			"response_item:message",
			"response_item:function_call",
			"response_item:function_call",
			"response_item:function_call_output",
			"response_item:function_call_output",
			"event_msg:agent_message",
			"response_item:message",
			"event_msg:task_complete",
		]);
		expect(rows[6].payload).toMatchObject({ name: "exec_command", arguments: '{"cmd":"ls"}' });
		const stamps = rows.map((r) => Date.parse(r.timestamp));
		for (let i = 1; i < stamps.length; i++) expect(stamps[i]).toBeGreaterThan(stamps[i - 1] ?? 0);
		expect(out.warnings).toEqual([{ kind: "dropped_reasoning", count: 1 }]);
	});

	test("round-trips through the parser with the same structure", () => {
		const out = serializeCodexSession(base, options());
		const reparsed = parseCodexSession({ main: out.main.lines });
		expect(reparsed.externalId).toBe(THREAD);
		expect(reparsed.messages.map((m) => m.role)).toEqual([
			"user",
			"assistant",
			"tool_result",
			"tool_result",
			"assistant",
		]);
		const call = reparsed.messages[1];
		expect(call?.role === "assistant" && call.blocks.map((b) => b.type)).toEqual([
			"text",
			"tool_use",
			"tool_use",
		]);
	});

	test("writes a child thread linked through spawn_agent", () => {
		const out = serializeCodexSession(
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
								id: "a1",
								name: "Agent",
								input: { prompt: "look", subagent_type: "explore" },
							},
						],
					},
					{
						role: "tool_result",
						timestamp: "2026-01-01T00:00:02.000Z",
						toolUseId: "a1",
						output: "found",
						isError: false,
					},
				],
				subagents: [
					{
						ref: "sub",
						parentToolUseId: "a1",
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
		expect(out.children).toHaveLength(1);
		const childMeta = JSON.parse(out.children[0]?.lines[0] ?? "{}");
		expect(childMeta.payload.source).toEqual({
			subagent: { thread_spawn: { parent_thread_id: THREAD, depth: 1, agent_role: "explore" } },
		});
		const reparsed = parseCodexSession({
			main: out.main.lines,
			children: out.children.map((c) => ({ lines: c.lines })),
		});
		expect(reparsed.subagents.map((s) => s.parentToolUseId)).toEqual(["a1"]);
		// The subagent's report survives next to the child link: the resumed model must
		// still see what the child found, not just its id.
		const spawnOutput = out.main.lines
			.map((l) => JSON.parse(l))
			.find((r) => r.payload?.type === "function_call_output" && r.payload.call_id === "a1");
		expect(JSON.parse(spawnOutput.payload.output)).toMatchObject({
			agent_id: out.children[0]?.threadId,
			result: "found",
		});
	});

	test("a freeform tool such as apply_patch round-trips as a custom_tool_call", () => {
		const patch = "*** Begin Patch\n*** Add File: a.txt\n+hi\n*** End Patch";
		const parsed = parseCodexRollout([
			meta(),
			line("response_item", {
				type: "message",
				role: "user",
				content: [{ type: "input_text", text: "add a file" }],
			}),
			line("response_item", {
				type: "custom_tool_call",
				call_id: "p1",
				name: "apply_patch",
				input: patch,
			}),
			line("response_item", { type: "custom_tool_call_output", call_id: "p1", output: "Done" }),
		]);
		const out = serializeCodexSession({ ...base, messages: parsed.messages }, options());
		const call = out.main.lines
			.map((l) => JSON.parse(l))
			.find((r) => r.payload?.call_id === "p1" && r.payload.type !== "custom_tool_call_output");
		expect(call.payload).toEqual({
			type: "custom_tool_call",
			call_id: "p1",
			name: "apply_patch",
			input: patch,
		});
	});

	test("session index rows use Codex's second-precision timestamp", () => {
		expect(codexSessionIndexLine(THREAD, "name", new Date("2026-05-20T03:09:40.123Z"))).toBe(
			`{"id":"${THREAD}","thread_name":"name","updated_at":"2026-05-20T03:09:40Z"}`,
		);
	});
});
