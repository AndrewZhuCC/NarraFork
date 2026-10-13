/**
 * Tool name and argument translation between NarraFork, Claude Code and Codex.
 *
 * NarraFork's core tools are deliberately named and shaped after Claude Code's, so
 * that direction is nearly an identity. The one historical rename is the subagent
 * tool: Claude Code wrote `Task` before it became `Agent`, and both spellings exist
 * in real transcripts.
 *
 * Codex uses different tools (a unified `exec_command`, a freeform `apply_patch`,
 * `spawn_agent`). Imported Codex calls are rewritten to NarraFork's names where the
 * meaning is the same, so a model continuing the session sees one vocabulary — the
 * original name is kept in the input under {@link ORIGINAL_TOOL_NAME_KEY} for the UI
 * and for a later re-export. Calls with no counterpart keep their original name;
 * neither CLI filters unknown tool names on resume.
 */

/** Input key that records a tool's name in the format it was imported from. */
export const ORIGINAL_TOOL_NAME_KEY = "_originalToolName";

/** NarraFork's subagent tool. */
export const NARRAFORK_AGENT_TOOL = "Agent";

/** Claude Code subagent tool spellings, newest first. */
const CLAUDE_CODE_AGENT_TOOLS = new Set(["Agent", "Task"]);

/** Codex subagent spawn tool. */
export const CODEX_SPAWN_AGENT_TOOL = "spawn_agent";

export function isClaudeCodeAgentTool(name: string): boolean {
	return CLAUDE_CODE_AGENT_TOOLS.has(name);
}

export interface MappedToolCall {
	name: string;
	input: Record<string, unknown>;
}

// ── Claude Code ⇄ NarraFork ─────────────────────────────────────────────────

/** Claude Code tool call → NarraFork. Only the subagent tool needs a rename. */
export function claudeCodeToolToNarraFork(call: MappedToolCall): MappedToolCall {
	if (call.name === "Task") {
		return {
			name: NARRAFORK_AGENT_TOOL,
			input: { ...call.input, [ORIGINAL_TOOL_NAME_KEY]: "Task" },
		};
	}
	return call;
}

/**
 * NarraFork tool call → Claude Code. Strips the import bookkeeping key so Claude
 * Code sees the input it would have produced itself.
 */
export function narraForkToolToClaudeCode(call: MappedToolCall): MappedToolCall {
	const { [ORIGINAL_TOOL_NAME_KEY]: original, ...input } = call.input;
	if (original === "Task" && call.name === NARRAFORK_AGENT_TOOL) return { name: "Task", input };
	return { name: call.name, input };
}

// ── Codex ⇄ NarraFork ───────────────────────────────────────────────────────

/**
 * Codex tool call → NarraFork.
 *
 * `rawArguments` is the unparsed argument string for freeform tools (`apply_patch`
 * sends a patch body, not JSON); `input` is the parsed JSON object for function tools.
 */
export function codexToolToNarraFork(call: {
	name: string;
	input: Record<string, unknown>;
	rawArguments?: string;
}): MappedToolCall {
	const tag = { [ORIGINAL_TOOL_NAME_KEY]: call.name };
	switch (call.name) {
		case "exec_command":
		case "shell_command": {
			const command = stringArg(call.input.cmd) ?? stringArg(call.input.command);
			if (command === undefined) break;
			return {
				name: "Bash",
				input: {
					command,
					...(stringArg(call.input.workdir) ? { workdir: call.input.workdir } : {}),
					...tag,
				},
			};
		}
		case "shell": {
			// Legacy array form: {"command": ["bash", "-lc", "ls"]}.
			const argv = Array.isArray(call.input.command)
				? call.input.command.filter((part): part is string => typeof part === "string")
				: undefined;
			if (!argv?.length) break;
			return {
				name: "Bash",
				input: {
					command: shellCommandFromArgv(argv),
					...(stringArg(call.input.workdir) ? { workdir: call.input.workdir } : {}),
					...tag,
				},
			};
		}
		case CODEX_SPAWN_AGENT_TOOL: {
			const prompt = stringArg(call.input.message) ?? "";
			return {
				name: NARRAFORK_AGENT_TOOL,
				input: {
					prompt,
					...(stringArg(call.input.task_name) ? { description: call.input.task_name } : {}),
					...(stringArg(call.input.agent_type) ? { subagent_type: call.input.agent_type } : {}),
					...tag,
				},
			};
		}
		default:
			break;
	}
	// No counterpart: keep the name, and keep a freeform body where there was one.
	// The freeform case is tagged so the export side can tell a body that must go
	// back as a `custom_tool_call` (apply_patch) from an ordinary function input.
	if (call.rawArguments !== undefined && Object.keys(call.input).length === 0) {
		return { name: call.name, input: { input: call.rawArguments, ...tag } };
	}
	return { name: call.name, input: call.input };
}

export type CodexToolCall =
	| { kind: "function"; name: string; arguments: string }
	| { kind: "custom"; name: string; input: string };

/** NarraFork tool call → Codex. */
export function narraForkToolToCodex(call: MappedToolCall): CodexToolCall {
	const { [ORIGINAL_TOOL_NAME_KEY]: original, ...input } = call.input;
	// A Codex freeform tool (apply_patch, or any other custom tool) came in as
	// `{input: <body>}` tagged with its own name; it must leave the same way, as a
	// custom_tool_call carrying the raw body rather than a JSON-wrapped function.
	if (
		typeof original === "string" &&
		original === call.name &&
		typeof input.input === "string" &&
		Object.keys(input).length === 1
	) {
		return { kind: "custom", name: original, input: input.input };
	}
	if (call.name === "Bash" && typeof input.command === "string") {
		return {
			kind: "function",
			name: "exec_command",
			arguments: JSON.stringify({
				cmd: input.command,
				...(typeof input.workdir === "string" ? { workdir: input.workdir } : {}),
			}),
		};
	}
	if (call.name === NARRAFORK_AGENT_TOOL) {
		return {
			kind: "function",
			name: CODEX_SPAWN_AGENT_TOOL,
			arguments: JSON.stringify({
				message: typeof input.prompt === "string" ? input.prompt : "",
				...(typeof input.description === "string" ? { task_name: input.description } : {}),
				...(typeof input.subagent_type === "string" ? { agent_type: input.subagent_type } : {}),
			}),
		};
	}
	const name = typeof original === "string" && original ? original : call.name;
	return { kind: "function", name, arguments: JSON.stringify(input) };
}

function stringArg(value: unknown): string | undefined {
	return typeof value === "string" ? value : undefined;
}

/**
 * Collapse Codex's legacy `["bash", "-lc", "<script>"]` into the script itself;
 * anything else is shell-quoted so it runs identically under `bash -c`.
 */
export function shellCommandFromArgv(argv: readonly string[]): string {
	if (argv.length === 3 && /^(ba|z)?sh$/.test(argv[0] ?? "") && /^-l?c$/.test(argv[1] ?? "")) {
		return argv[2] ?? "";
	}
	return argv.map(shellQuote).join(" ");
}

function shellQuote(part: string): string {
	return /^[A-Za-z0-9_@%+=:,./-]+$/.test(part) ? part : `'${part.replace(/'/g, `'\\''`)}'`;
}
