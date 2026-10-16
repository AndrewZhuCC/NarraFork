/**
 * Codex rollout JSONL → {@link InterchangeSession}.
 *
 * Reconstructs what Codex feeds its model on resume
 * (`reconstruct_history_from_rollout` in `codex-rs/core/src/session`):
 * `response_item` rows in order, a `compacted` row with `replacement_history` as a
 * reset point, and `thread_rolled_back` events dropping the last N user turns. Unlike
 * Codex, the reset is kept as a compact marker instead of discarding earlier rows,
 * so the imported transcript still shows the whole conversation.
 *
 * Codex injects its own context (AGENTS.md, environment, permissions, skills) as
 * `developer` and `user` messages in the same stream. Those are dropped: they
 * describe Codex's runtime, not the conversation, and NarraFork builds its own.
 *
 * Pure: the caller supplies already-split (and already-decompressed) lines.
 */

import {
	firstUserText,
	type InterchangeAssistantBlock,
	type InterchangeAssistantMessage,
	type InterchangeLine,
	type InterchangeMessage,
	type InterchangeSession,
	type InterchangeSubagent,
	type InterchangeUserBlock,
	titleFromText,
	WarningCollector,
} from "../model";
import { codexToolToNarraFork, NARRAFORK_AGENT_TOOL } from "../tool-mapping";

// biome-ignore lint/suspicious/noExplicitAny: raw JSONL entries are untyped external data
type RawEntry = Record<string, any>;

/**
 * Whole-message markers Codex writes as user-role context.
 * Ported verbatim from `is_known_contextual_user_text` in
 * `codex-rs/thread-store/src/local/rollout_migration/rollback.rs`, which Codex keeps
 * frozen for exactly this purpose: recognizing persisted legacy rollouts.
 */
const CONTEXTUAL_USER_PAIRS: ReadonlyArray<readonly [string, string]> = [
	["# AGENTS.md instructions", "</INSTRUCTIONS>"],
	["<environment_context>", "</environment_context>"],
	["<skill>", "</skill>"],
	["<user_shell_command>", "</user_shell_command>"],
	["<turn_aborted>", "</turn_aborted>"],
	["<subagent_notification>", "</subagent_notification>"],
	["<recommended_plugins>", "</recommended_plugins>"],
	["<goal_context>", "</goal_context>"],
	["<user_instructions>", "</user_instructions>"],
];

const CONTEXTUAL_USER_PREFIXES = [
	"Warning: The maximum number of unified exec processes you can keep open is",
	"Warning: Your account was flagged for potentially high-risk cyber activity",
];

export function isCodexContextualUserText(raw: string): boolean {
	const text = raw.trim();
	if (/^<hook_prompt[\s>]/.test(text) && text.endsWith("</hook_prompt>")) return true;
	if (CONTEXTUAL_USER_PAIRS.some(([start, end]) => text.startsWith(start) && text.endsWith(end))) {
		return true;
	}
	const external = /^<external_([^>\s]+)>/.exec(text);
	if (external && text.endsWith(`</external_${external[1]}>`)) return true;
	if (text.startsWith("<codex_internal_context") && text.endsWith("</codex_internal_context>")) {
		return true;
	}
	if (
		text.startsWith("Warning: apply_patch was requested via ") &&
		text.endsWith("Use the apply_patch tool instead of exec_command.")
	) {
		return true;
	}
	return CONTEXTUAL_USER_PREFIXES.some((prefix) => text.startsWith(prefix));
}

/** Exec output Codex formats as `Process exited with code N`; non-zero marks a failure. */
const EXIT_CODE_PATTERN = /(?:^|\n)(?:Process exited with code|Exit code:?)\s+(-?\d+)/;

function looksLikeFailedExec(output: string): boolean {
	const match = EXIT_CODE_PATTERN.exec(output);
	return match !== null && match[1] !== "0";
}

export interface ParsedCodexRollout {
	threadId: string | undefined;
	cwd: string | undefined;
	gitBranch: string | undefined;
	model: string | undefined;
	modelProvider: string | undefined;
	/** From `session_meta.source.subagent.thread_spawn` on child threads. */
	parentThreadId: string | undefined;
	agentRole: string | undefined;
	agentNickname: string | undefined;
	firstTimestamp: string | undefined;
	lastTimestamp: string | undefined;
	firstUserMessage: string | undefined;
	messages: InterchangeMessage[];
	/** spawn_agent call_id → child thread id, from the call's output. */
	childThreadIdByCallId: Map<string, string>;
	warnings: WarningCollector;
}

function parseLine(line: InterchangeLine, warnings: WarningCollector): RawEntry | null {
	if (typeof line !== "string") return line && typeof line === "object" ? line : null;
	const trimmed = line.trim();
	if (!trimmed) return null;
	try {
		const value = JSON.parse(trimmed);
		return value && typeof value === "object" && !Array.isArray(value) ? value : null;
	} catch {
		warnings.add("unparseable_line", trimmed.slice(0, 80));
		return null;
	}
}

function parseArguments(raw: unknown): { input: Record<string, unknown>; raw?: string } {
	if (typeof raw !== "string") {
		return { input: raw && typeof raw === "object" ? (raw as Record<string, unknown>) : {} };
	}
	try {
		const parsed = JSON.parse(raw);
		if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) return { input: parsed };
	} catch {
		// Freeform tools send a body, not JSON.
	}
	return { input: {}, raw };
}

function outputOf(raw: unknown): string | InterchangeUserBlock[] {
	if (typeof raw === "string") return raw;
	if (!Array.isArray(raw)) {
		// Older rollouts wrapped the body: {"content": "...", "success": bool}.
		if (raw && typeof raw === "object" && typeof (raw as RawEntry).content === "string") {
			return (raw as RawEntry).content;
		}
		return raw == null ? "" : JSON.stringify(raw);
	}
	const blocks: InterchangeUserBlock[] = [];
	for (const item of raw) {
		if (item?.type === "input_text" && typeof item.text === "string") {
			blocks.push({ type: "text", text: item.text });
		} else if (item?.type === "input_image" && typeof item.image_url === "string") {
			const image = dataUrlImage(item.image_url);
			if (image) blocks.push(image);
		}
	}
	return blocks.every((block) => block.type === "text")
		? blocks.map((block) => (block as { text: string }).text).join("\n")
		: blocks;
}

/** Explicit failure flag from older wrapped outputs, when present. */
function outputSuccess(raw: unknown): boolean | undefined {
	if (raw && typeof raw === "object" && !Array.isArray(raw)) {
		const success = (raw as RawEntry).success;
		if (typeof success === "boolean") return success;
	}
	return undefined;
}

function dataUrlImage(url: string): InterchangeUserBlock | null {
	const match = /^data:([^;,]+);base64,(.*)$/s.exec(url);
	if (!match) return null;
	return { type: "image", mediaType: match[1] ?? "image/png", data: match[2] ?? "" };
}

function userContent(content: unknown): InterchangeUserBlock[] {
	if (!Array.isArray(content)) return [];
	const blocks: InterchangeUserBlock[] = [];
	for (const item of content) {
		if (item?.type === "input_text" && typeof item.text === "string") {
			blocks.push({ type: "text", text: item.text });
		} else if (item?.type === "input_image" && typeof item.image_url === "string") {
			const image = dataUrlImage(item.image_url);
			if (image) blocks.push(image);
		}
	}
	return blocks;
}

function assistantText(content: unknown): string {
	if (!Array.isArray(content)) return "";
	return content
		.filter((item: RawEntry) => item?.type === "output_text" && typeof item.text === "string")
		.map((item: RawEntry) => item.text)
		.join("");
}

function reasoningText(payload: RawEntry): string {
	const parts: string[] = [];
	for (const item of Array.isArray(payload.summary) ? payload.summary : []) {
		if (typeof item?.text === "string") parts.push(item.text);
	}
	if (parts.length === 0) {
		for (const item of Array.isArray(payload.content) ? payload.content : []) {
			if (typeof item?.text === "string") parts.push(item.text);
		}
	}
	return parts.join("\n\n").trim();
}

/**
 * The assistant message to append a block to.
 *
 * One model response is a run of consecutive assistant items (text, reasoning,
 * calls). A tool output ends the run, because Codex samples the model again once
 * outputs arrive, so the next item belongs to a new response.
 */
function openAssistant(
	messages: InterchangeMessage[],
	timestamp: string,
): InterchangeAssistantMessage {
	const last = messages.at(-1);
	if (last?.role === "assistant") return last;
	const message: InterchangeAssistantMessage = { role: "assistant", timestamp, blocks: [] };
	messages.push(message);
	return message;
}

/** Index just past the Nth-from-last user message, i.e. where a rollback of N turns cuts. */
function rollbackCut(messages: InterchangeMessage[], turns: number): number {
	let remaining = turns;
	for (let i = messages.length - 1; i >= 0; i--) {
		if (messages[i]?.role === "user" && --remaining === 0) return i;
	}
	return 0;
}

export function parseCodexRollout(lines: Iterable<InterchangeLine>): ParsedCodexRollout {
	const warnings = new WarningCollector();
	const messages: InterchangeMessage[] = [];
	const childThreadIdByCallId = new Map<string, string>();
	const spawnCallIds = new Set<string>();
	let meta: RawEntry | undefined;
	let model: string | undefined;
	let firstTimestamp: string | undefined;
	let lastTimestamp: string | undefined;
	let firstUserMessage: string | undefined;
	let injected = 0;

	for (const line of lines) {
		const entry = parseLine(line, warnings);
		if (!entry) continue;
		const timestamp = typeof entry.timestamp === "string" ? entry.timestamp : "";
		const payload: RawEntry =
			entry.payload && typeof entry.payload === "object" ? entry.payload : {};

		switch (entry.type) {
			case "session_meta":
				// The first session_meta belongs to this rollout; later ones are copied
				// in from a fork's parent history.
				meta ??= payload;
				continue;
			case "turn_context":
				if (typeof payload.model === "string" && payload.model) model = payload.model;
				continue;
			case "event_msg":
				if (payload.type === "user_message" && typeof payload.message === "string") {
					firstUserMessage ??= payload.message;
				} else if (payload.type === "thread_rolled_back") {
					const turns = Number(payload.num_turns);
					if (Number.isInteger(turns) && turns > 0) {
						const cut = rollbackCut(messages, turns);
						messages.splice(cut);
						warnings.add("rolled_back_turns", undefined, turns);
					}
				}
				continue;
			case "compacted": {
				const summary = typeof payload.message === "string" ? payload.message : "";
				messages.push({ role: "compact", timestamp, summary });
				continue;
			}
			case "response_item":
				break;
			default:
				continue;
		}

		firstTimestamp ??= timestamp || undefined;
		if (timestamp) lastTimestamp = timestamp;

		switch (payload.type) {
			case "message": {
				if (payload.role === "developer" || payload.role === "system") {
					injected++;
					continue;
				}
				if (payload.role === "user") {
					const blocks = userContent(payload.content);
					if (
						blocks.some((block) => block.type === "text" && isCodexContextualUserText(block.text))
					) {
						injected++;
						continue;
					}
					if (blocks.length > 0) messages.push({ role: "user", timestamp, blocks });
					continue;
				}
				if (payload.role === "assistant") {
					const text = assistantText(payload.content);
					if (text) openAssistant(messages, timestamp).blocks.push({ type: "text", text });
				}
				continue;
			}
			case "reasoning": {
				const text = reasoningText(payload);
				// encrypted_content is bound to the OpenAI account that produced it; only
				// the readable summary survives a move to another destination.
				if (text) openAssistant(messages, timestamp).blocks.push({ type: "reasoning", text });
				else warnings.add("dropped_reasoning");
				continue;
			}
			case "function_call":
			case "custom_tool_call": {
				const callId = typeof payload.call_id === "string" ? payload.call_id : undefined;
				if (!callId) continue;
				const name = typeof payload.name === "string" ? payload.name : "unknown";
				const args =
					payload.type === "custom_tool_call"
						? { input: {}, raw: typeof payload.input === "string" ? payload.input : "" }
						: parseArguments(payload.arguments);
				const mapped = codexToolToNarraFork({ name, input: args.input, rawArguments: args.raw });
				if (mapped.name === NARRAFORK_AGENT_TOOL) spawnCallIds.add(callId);
				const block: InterchangeAssistantBlock = {
					type: "tool_use",
					id: callId,
					name: mapped.name,
					input: mapped.input,
				};
				openAssistant(messages, timestamp).blocks.push(block);
				continue;
			}
			case "local_shell_call": {
				const callId =
					typeof payload.call_id === "string"
						? payload.call_id
						: typeof payload.id === "string"
							? payload.id
							: undefined;
				if (!callId) continue;
				const mapped = codexToolToNarraFork({
					name: "shell",
					input: {
						command: payload.action?.command,
						...(typeof payload.action?.working_directory === "string"
							? { workdir: payload.action.working_directory }
							: {}),
					},
				});
				openAssistant(messages, timestamp).blocks.push({
					type: "tool_use",
					id: callId,
					name: mapped.name,
					input: mapped.input,
				});
				continue;
			}
			case "function_call_output":
			case "custom_tool_call_output": {
				const callId = typeof payload.call_id === "string" ? payload.call_id : undefined;
				if (!callId) continue;
				const output = outputOf(payload.output);
				const text = typeof output === "string" ? output : "";
				const success = outputSuccess(payload.output);
				messages.push({
					role: "tool_result",
					timestamp,
					toolUseId: callId,
					output,
					isError: success === false || (success === undefined && looksLikeFailedExec(text)),
				});
				if (spawnCallIds.has(callId) && text) {
					const agentId = spawnedAgentId(text);
					if (agentId) childThreadIdByCallId.set(callId, agentId);
				}
				continue;
			}
			default:
				if (typeof payload.type === "string") {
					warnings.add("dropped_unsupported_block", payload.type);
				}
		}
	}
	if (injected > 0) warnings.add("dropped_injected_context", undefined, injected);

	const spawn = meta?.source?.subagent?.thread_spawn;
	return {
		threadId: typeof meta?.id === "string" ? meta.id : undefined,
		cwd: typeof meta?.cwd === "string" ? meta.cwd : undefined,
		gitBranch: typeof meta?.git?.branch === "string" ? meta.git.branch : undefined,
		model,
		modelProvider: typeof meta?.model_provider === "string" ? meta.model_provider : undefined,
		parentThreadId:
			typeof spawn?.parent_thread_id === "string" ? spawn.parent_thread_id : undefined,
		agentRole: typeof spawn?.agent_role === "string" ? spawn.agent_role : undefined,
		agentNickname: typeof spawn?.agent_nickname === "string" ? spawn.agent_nickname : undefined,
		firstTimestamp: typeof meta?.timestamp === "string" ? meta.timestamp : firstTimestamp,
		lastTimestamp,
		firstUserMessage,
		messages,
		childThreadIdByCallId,
		warnings,
	};
}

/** `spawn_agent` answers `{"agent_id": "<thread id>", "nickname": ...}`. */
function spawnedAgentId(output: string): string | undefined {
	try {
		const parsed = JSON.parse(output);
		return typeof parsed?.agent_id === "string" ? parsed.agent_id : undefined;
	} catch {
		return undefined;
	}
}

export interface CodexChildRollout {
	lines: Iterable<InterchangeLine>;
}

/**
 * Assemble a session from a parent rollout and any child-thread rollouts.
 *
 * A child is attached through the parent's `spawn_agent` output (`agent_id`), with
 * the child's own `parent_thread_id` as a cross-check; children that match neither
 * are dropped and reported.
 */
export function parseCodexSession(input: {
	main: Iterable<InterchangeLine>;
	children?: CodexChildRollout[];
	fallbackThreadId?: string;
}): InterchangeSession {
	const main = parseCodexRollout(input.main);
	const warnings = new WarningCollector();
	warnings.merge(main.warnings.list());

	const callIdByChildId = new Map<string, string>();
	for (const [callId, childId] of main.childThreadIdByCallId) callIdByChildId.set(childId, callId);
	const callInputs = new Map<string, Record<string, unknown>>();
	for (const message of main.messages) {
		if (message.role !== "assistant") continue;
		for (const block of message.blocks) {
			if (block.type === "tool_use" && block.name === NARRAFORK_AGENT_TOOL) {
				callInputs.set(block.id, block.input);
			}
		}
	}

	const subagents: InterchangeSubagent[] = [];
	for (const child of input.children ?? []) {
		const parsed = parseCodexRollout(child.lines);
		warnings.merge(parsed.warnings.list());
		const childId = parsed.threadId;
		const callId = childId ? callIdByChildId.get(childId) : undefined;
		if (!childId || !callId || (parsed.parentThreadId && parsed.parentThreadId !== main.threadId)) {
			warnings.add("missing_subagent", `child thread ${childId ?? "(unknown)"} has no parent call`);
			continue;
		}
		const callInput = callInputs.get(callId);
		subagents.push({
			ref: childId,
			parentToolUseId: callId,
			subagentType:
				parsed.agentRole ??
				(typeof callInput?.subagent_type === "string" ? callInput.subagent_type : undefined),
			title:
				parsed.agentNickname ??
				(typeof callInput?.description === "string" ? callInput.description : undefined),
			messages: parsed.messages,
		});
	}

	const refByCallId = new Map(subagents.map((sub) => [sub.parentToolUseId, sub.ref]));
	for (const message of main.messages) {
		if (message.role !== "assistant") continue;
		for (const block of message.blocks) {
			if (block.type === "tool_use" && refByCallId.has(block.id)) {
				block.subagentRef = refByCallId.get(block.id);
			}
		}
	}

	const now = new Date().toISOString();
	return {
		source: "codex",
		externalId: main.threadId ?? input.fallbackThreadId ?? "",
		title: titleFromText(main.firstUserMessage ?? firstUserText(main.messages)),
		cwd: main.cwd,
		gitBranch: main.gitBranch,
		model: main.model,
		createdAt: main.firstTimestamp ?? now,
		updatedAt: main.lastTimestamp ?? main.firstTimestamp ?? now,
		messages: main.messages,
		subagents,
		warnings: warnings.list(),
	};
}
