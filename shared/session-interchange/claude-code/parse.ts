/**
 * Claude Code transcript JSONL → {@link InterchangeSession}.
 *
 * Reconstructs the conversation the way Claude Code itself does on `--resume`, so
 * an imported session contains exactly what the CLI would have replayed:
 *
 *  1. Index every chain entry by `uuid`, bridging legacy `progress` rows (older
 *     transcripts put them in the parent chain; Claude Code rewrites around them).
 *  2. Pick the newest non-sidechain leaf and walk `parentUuid` back to the root.
 *  3. Recover parallel tool siblings the single-parent walk drops: streaming writes
 *     one assistant row per content block, all sharing `message.id`, and each
 *     tool_result hangs off its own one-block row — a DAG, not a list.
 *
 * Mirrors `loadTranscriptFile` / `buildConversationChain` /
 * `recoverOrphanedParallelToolResults` in Claude Code's `utils/sessionStorage.ts`.
 *
 * Pure: the caller supplies already-split lines and has already enforced byte limits.
 */

import {
	firstUserText,
	type InterchangeAssistantBlock,
	type InterchangeLine,
	type InterchangeMessage,
	type InterchangeSession,
	type InterchangeSubagent,
	type InterchangeUserBlock,
	titleFromText,
	WarningCollector,
} from "../model";
import {
	claudeCodeToolToNarraFork,
	isClaudeCodeAgentTool,
	NARRAFORK_AGENT_TOOL,
} from "../tool-mapping";

// biome-ignore lint/suspicious/noExplicitAny: raw JSONL entries are untyped external data
type RawEntry = Record<string, any>;

/** Entry types that participate in the parentUuid chain. */
const CHAIN_TYPES = new Set(["user", "assistant", "system", "attachment"]);

/**
 * User-row text Claude Code writes for its own bookkeeping rather than as a prompt.
 * `<local-command-caveat>` rows are also `isMeta`; command echo and stdout rows are not.
 */
const LOCAL_COMMAND_MARKERS = [
	"<local-command-caveat>",
	"<local-command-stdout>",
	"<local-command-stderr>",
	"<command-name>",
	"<bash-input>",
	"<bash-stdout>",
	"<bash-stderr>",
];

/** Text Claude Code writes into the transcript when a turn is cut off. */
const INTERRUPT_MARKER = /^\[Request interrupted by user.*\]$/;

/** The placeholder assistant Claude Code synthesizes after a user-ended transcript. */
const SYNTHETIC_MODEL = "<synthetic>";

export interface ParsedClaudeCodeFile {
	sessionId: string | undefined;
	/** Present on sidechain files. */
	agentId: string | undefined;
	cwd: string | undefined;
	gitBranch: string | undefined;
	customTitle: string | undefined;
	aiTitle: string | undefined;
	firstTimestamp: string | undefined;
	lastTimestamp: string | undefined;
	model: string | undefined;
	messages: InterchangeMessage[];
	/** tool_use id → sidechain agentId, from Agent/Task tool results. */
	agentIdByToolUseId: Map<string, string>;
	warnings: WarningCollector;
}

function parseLine(line: InterchangeLine, warnings: WarningCollector): RawEntry | null {
	// A pre-decoded row is copied: `parseClaudeCodeFile` rewrites `parentUuid` while
	// bridging progress rows, and the caller's object must not change under it.
	if (typeof line !== "string") return line && typeof line === "object" ? { ...line } : null;
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

/**
 * Parse one Claude Code JSONL file (main transcript or sidechain).
 *
 * `sidechain` selects the leaf rule: the main transcript follows the newest
 * non-sidechain leaf; a sidechain file consists only of sidechain rows, so it
 * follows the newest leaf of any kind.
 */
export function parseClaudeCodeFile(
	lines: Iterable<InterchangeLine>,
	options: { sidechain?: boolean } = {},
): ParsedClaudeCodeFile {
	const warnings = new WarningCollector();
	const byUuid = new Map<string, RawEntry>();
	const order: string[] = [];
	const progressBridge = new Map<string, string | null>();
	const referencedParents = new Set<string>();
	// Titles are keyed by the session they name: a resumed session copies rows (and
	// sometimes title rows) from the one it continued, so only the leaf's own
	// session's title is this file's title. A title row without a sessionId (older
	// CLI versions) is used only when nothing names this session.
	const customTitles = new Map<string, string>();
	const aiTitles = new Map<string, string>();
	let untaggedCustomTitle: string | undefined;
	let untaggedAiTitle: string | undefined;
	let agentId: string | undefined;
	let cwd: string | undefined;
	let gitBranch: string | undefined;
	/** tool_use id → agentId, from `progress` rows; the fallback for an interrupted Task. */
	const progressAgentIds = new Map<string, string>();

	for (const line of lines) {
		const entry = parseLine(line, warnings);
		if (!entry) continue;
		const type = entry.type;

		if (type === "custom-title" && typeof entry.customTitle === "string") {
			if (typeof entry.sessionId === "string") customTitles.set(entry.sessionId, entry.customTitle);
			else untaggedCustomTitle = entry.customTitle;
			continue;
		}
		if (type === "ai-title" && typeof entry.aiTitle === "string") {
			if (typeof entry.sessionId === "string") aiTitles.set(entry.sessionId, entry.aiTitle);
			else untaggedAiTitle = entry.aiTitle;
			continue;
		}
		if (type === "progress" && typeof entry.uuid === "string") {
			const parent = typeof entry.parentUuid === "string" ? entry.parentUuid : null;
			progressBridge.set(
				entry.uuid,
				parent && progressBridge.has(parent) ? (progressBridge.get(parent) ?? null) : parent,
			);
			// A subagent's progress rows name both its agentId and the Task call that
			// spawned it. When the Task is interrupted its result carries no
			// `toolUseResult.agentId`, and this is the only link left.
			const data = entry.data as { agentId?: unknown } | undefined;
			if (typeof entry.parentToolUseID === "string" && typeof data?.agentId === "string") {
				progressAgentIds.set(entry.parentToolUseID, data.agentId);
			}
			continue;
		}
		if (!CHAIN_TYPES.has(type) || typeof entry.uuid !== "string") continue;

		if (typeof entry.parentUuid === "string" && progressBridge.has(entry.parentUuid)) {
			entry.parentUuid = progressBridge.get(entry.parentUuid) ?? null;
		}
		if (!byUuid.has(entry.uuid)) order.push(entry.uuid);
		byUuid.set(entry.uuid, entry);
		if (typeof entry.parentUuid === "string") referencedParents.add(entry.parentUuid);

		agentId ??= typeof entry.agentId === "string" ? entry.agentId : undefined;
		if (typeof entry.cwd === "string") cwd = entry.cwd;
		if (typeof entry.gitBranch === "string" && entry.gitBranch) gitBranch = entry.gitBranch;
	}

	const chain = selectChain(byUuid, order, referencedParents, options.sidechain === true);
	const offChain = byUuid.size - chain.length;
	if (offChain > 0) warnings.add("off_chain_line", undefined, offChain);

	const agentIdByToolUseId = new Map<string, string>();
	const messages = chainToMessages(chain, warnings, agentIdByToolUseId);
	// Explicit `toolUseResult.agentId` links win; progress rows only fill the gaps.
	for (const [toolUseId, id] of progressAgentIds) {
		if (!agentIdByToolUseId.has(toolUseId)) agentIdByToolUseId.set(toolUseId, id);
	}
	const model = chain.findLast(
		(entry) => entry.type === "assistant" && isRealModel(entry.message?.model),
	)?.message?.model;
	// Claude Code identifies a session by its leaf's sessionId. A resumed session's
	// file begins with rows copied from the session it continued, so the FIRST
	// row's sessionId belongs to that older session.
	const leafSessionId = chain.findLast((entry) => typeof entry.sessionId === "string")?.sessionId;
	const sessionId = typeof leafSessionId === "string" ? leafSessionId : undefined;

	return {
		sessionId,
		agentId,
		cwd,
		gitBranch,
		customTitle: (sessionId ? customTitles.get(sessionId) : undefined) ?? untaggedCustomTitle,
		aiTitle: (sessionId ? aiTitles.get(sessionId) : undefined) ?? untaggedAiTitle,
		firstTimestamp: chain[0]?.timestamp,
		lastTimestamp: chain.at(-1)?.timestamp,
		model,
		messages,
		agentIdByToolUseId,
		warnings,
	};
}

function isRealModel(model: unknown): model is string {
	return typeof model === "string" && model !== "" && model !== SYNTHETIC_MODEL;
}

function timestampMs(entry: RawEntry): number {
	const ms = Date.parse(entry.timestamp);
	return Number.isFinite(ms) ? ms : 0;
}

function selectChain(
	byUuid: Map<string, RawEntry>,
	order: string[],
	referencedParents: Set<string>,
	sidechain: boolean,
): RawEntry[] {
	// Leaf = no other entry names it as parent. Newest wins; file order breaks ties
	// so that an append-only writer's last row is the leaf.
	let leaf: RawEntry | undefined;
	let leafMs = Number.NEGATIVE_INFINITY;
	for (const uuid of order) {
		const entry = byUuid.get(uuid);
		if (!entry || referencedParents.has(uuid)) continue;
		if (!sidechain && entry.isSidechain === true) continue;
		const ms = timestampMs(entry);
		if (ms >= leafMs) {
			leaf = entry;
			leafMs = ms;
		}
	}
	if (!leaf) return [];

	const position = new Map(order.map((uuid, index) => [uuid, index]));
	const chain: RawEntry[] = [];
	const seen = new Set<string>();
	let current: RawEntry | undefined = leaf;
	while (current && !seen.has(current.uuid)) {
		seen.add(current.uuid);
		chain.push(current);
		current = chainParent(current, byUuid, order, position, sidechain);
	}
	chain.reverse();
	return recoverParallelSiblings(byUuid, chain, seen);
}

/**
 * The entry this one continues from.
 *
 * Claude Code starts every compaction with a `compact_boundary` whose `parentUuid`
 * is null, so `--resume` stops there and the model only sees what came after. An
 * import wants the whole conversation for display, so the walk crosses the boundary
 * through `logicalParentUuid` (the last pre-compact entry) and keeps the boundary
 * itself as a compact marker; NarraFork's own compact handling then limits what the
 * model sees exactly as Claude Code would.
 *
 * `logicalParentUuid` sometimes names a row that was never written (it lived only in
 * the CLI's memory). The boundary then falls back to the last chain row written
 * before it, which is the pre-compact tail in an append-only file.
 */
function chainParent(
	entry: RawEntry,
	byUuid: Map<string, RawEntry>,
	order: string[],
	position: Map<string, number>,
	sidechain: boolean,
): RawEntry | undefined {
	if (typeof entry.parentUuid === "string") return byUuid.get(entry.parentUuid);
	if (entry.type !== "system" || entry.subtype !== "compact_boundary") return undefined;
	if (typeof entry.logicalParentUuid === "string") {
		const logical = byUuid.get(entry.logicalParentUuid);
		if (logical) return logical;
	}
	const at = position.get(entry.uuid) ?? 0;
	for (let i = at - 1; i >= 0; i--) {
		const candidate = byUuid.get(order[i] ?? "");
		if (!candidate) continue;
		if (!sidechain && candidate.isSidechain === true) continue;
		return candidate;
	}
	return undefined;
}

/** Port of Claude Code's `recoverOrphanedParallelToolResults`. */
function recoverParallelSiblings(
	byUuid: Map<string, RawEntry>,
	chain: RawEntry[],
	seen: Set<string>,
): RawEntry[] {
	const anchorByMessageId = new Map<string, RawEntry>();
	for (const entry of chain) {
		if (entry.type === "assistant" && entry.message?.id) {
			anchorByMessageId.set(entry.message.id, entry);
		}
	}
	if (anchorByMessageId.size === 0) return chain;

	const siblingsByMessageId = new Map<string, RawEntry[]>();
	const toolResultsByParent = new Map<string, RawEntry[]>();
	for (const entry of byUuid.values()) {
		if (entry.type === "assistant" && entry.message?.id) {
			const group = siblingsByMessageId.get(entry.message.id);
			if (group) group.push(entry);
			else siblingsByMessageId.set(entry.message.id, [entry]);
		} else if (
			entry.type === "user" &&
			typeof entry.parentUuid === "string" &&
			Array.isArray(entry.message?.content) &&
			entry.message.content.some((block: RawEntry) => block?.type === "tool_result")
		) {
			const group = toolResultsByParent.get(entry.parentUuid);
			if (group) group.push(entry);
			else toolResultsByParent.set(entry.parentUuid, [entry]);
		}
	}

	const inserts = new Map<string, RawEntry[]>();
	for (const [messageId, anchor] of anchorByMessageId) {
		const group = siblingsByMessageId.get(messageId) ?? [anchor];
		const orphanedSiblings = group.filter((entry) => !seen.has(entry.uuid));
		const orphanedResults: RawEntry[] = [];
		for (const member of group) {
			for (const result of toolResultsByParent.get(member.uuid) ?? []) {
				if (!seen.has(result.uuid)) orphanedResults.push(result);
			}
		}
		if (orphanedSiblings.length === 0 && orphanedResults.length === 0) continue;
		const byTime = (a: RawEntry, b: RawEntry) =>
			String(a.timestamp).localeCompare(String(b.timestamp));
		orphanedSiblings.sort(byTime);
		orphanedResults.sort(byTime);
		const recovered = [...orphanedSiblings, ...orphanedResults];
		for (const entry of recovered) seen.add(entry.uuid);
		inserts.set(anchor.uuid, recovered);
	}
	if (inserts.size === 0) return chain;

	const result: RawEntry[] = [];
	for (const entry of chain) {
		result.push(entry);
		const extra = inserts.get(entry.uuid);
		if (extra) result.push(...extra);
	}
	return result;
}

function isLocalCommandText(text: string): boolean {
	const trimmed = text.trimStart();
	return LOCAL_COMMAND_MARKERS.some((marker) => trimmed.startsWith(marker));
}

function userBlocks(content: unknown, warnings: WarningCollector): InterchangeUserBlock[] {
	if (typeof content === "string") return content ? [{ type: "text", text: content }] : [];
	if (!Array.isArray(content)) return [];
	const blocks: InterchangeUserBlock[] = [];
	for (const raw of content) {
		if (!raw || typeof raw !== "object") continue;
		if (raw.type === "text" && typeof raw.text === "string") {
			blocks.push({ type: "text", text: raw.text });
		} else if (raw.type === "image" && raw.source?.type === "base64") {
			if (typeof raw.source.data === "string" && typeof raw.source.media_type === "string") {
				blocks.push({ type: "image", mediaType: raw.source.media_type, data: raw.source.data });
			}
		} else if (raw.type !== "tool_result") {
			warnings.add("dropped_unsupported_block", String(raw.type));
		}
	}
	return blocks;
}

function toolResultOutput(content: unknown): string | InterchangeUserBlock[] {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return content == null ? "" : JSON.stringify(content);
	const blocks: InterchangeUserBlock[] = [];
	for (const raw of content) {
		if (raw?.type === "text" && typeof raw.text === "string") {
			blocks.push({ type: "text", text: raw.text });
		} else if (raw?.type === "image" && raw.source?.type === "base64") {
			blocks.push({ type: "image", mediaType: raw.source.media_type, data: raw.source.data });
		}
	}
	// A text-only result reads more naturally, and round-trips more cleanly, as a string.
	return blocks.every((block) => block.type === "text")
		? blocks.map((block) => (block as { text: string }).text).join("\n")
		: blocks;
}

function assistantBlocks(
	content: unknown,
	warnings: WarningCollector,
): InterchangeAssistantBlock[] {
	if (typeof content === "string") return content ? [{ type: "text", text: content }] : [];
	if (!Array.isArray(content)) return [];
	const blocks: InterchangeAssistantBlock[] = [];
	for (const raw of content) {
		if (!raw || typeof raw !== "object") continue;
		if (raw.type === "text" && typeof raw.text === "string") {
			if (raw.text) blocks.push({ type: "text", text: raw.text });
		} else if (raw.type === "thinking" && typeof raw.thinking === "string") {
			if (!raw.thinking.trim()) continue;
			blocks.push({
				type: "reasoning",
				text: raw.thinking,
				// Claude Code only talks to the official API, so its signatures are official.
				...(typeof raw.signature === "string" && raw.signature
					? {
							signature: {
								provider: "anthropic" as const,
								value: raw.signature,
								officialAnthropic: true,
							},
						}
					: {}),
			});
		} else if (raw.type === "redacted_thinking") {
			// Opaque to everyone but the upstream that minted it.
			warnings.add("dropped_reasoning", "redacted_thinking");
		} else if (raw.type === "tool_use" && typeof raw.id === "string") {
			const mapped = claudeCodeToolToNarraFork({
				name: typeof raw.name === "string" ? raw.name : "unknown",
				input: raw.input && typeof raw.input === "object" ? raw.input : {},
			});
			blocks.push({ type: "tool_use", id: raw.id, name: mapped.name, input: mapped.input });
		} else {
			warnings.add("dropped_unsupported_block", String(raw.type));
		}
	}
	return blocks;
}

function chainToMessages(
	chain: RawEntry[],
	warnings: WarningCollector,
	agentIdByToolUseId: Map<string, string>,
): InterchangeMessage[] {
	const messages: InterchangeMessage[] = [];
	let currentAssistant: { messageId: string | undefined; message: InterchangeMessage } | null =
		null;

	for (let i = 0; i < chain.length; i++) {
		const entry = chain[i];
		if (!entry) continue;
		const timestamp = typeof entry.timestamp === "string" ? entry.timestamp : "";

		if (entry.type === "system") {
			currentAssistant = null;
			if (entry.subtype !== "compact_boundary") continue;
			// The summary is the next user row flagged isCompactSummary.
			const next = chain[i + 1];
			const summary =
				next?.isCompactSummary === true
					? textOf(next.message?.content)
					: typeof entry.content === "string"
						? entry.content
						: "";
			messages.push({ role: "compact", timestamp, summary });
			if (next?.isCompactSummary === true) i++;
			continue;
		}

		if (entry.type === "attachment") continue;

		if (entry.type === "assistant") {
			const message = entry.message ?? {};
			if (message.model === SYNTHETIC_MODEL) {
				currentAssistant = null;
				continue;
			}
			const blocks = assistantBlocks(message.content, warnings);
			const messageId = typeof message.id === "string" ? message.id : undefined;
			if (
				currentAssistant &&
				messageId &&
				currentAssistant.messageId === messageId &&
				currentAssistant.message.role === "assistant"
			) {
				currentAssistant.message.blocks.push(...blocks);
				continue;
			}
			if (blocks.length === 0) continue;
			const assistant: InterchangeMessage = {
				role: "assistant",
				timestamp,
				...(isRealModel(message.model) ? { model: message.model } : {}),
				blocks,
			};
			messages.push(assistant);
			currentAssistant = { messageId, message: assistant };
			continue;
		}

		// type === "user"
		const content = entry.message?.content;
		const toolResults = Array.isArray(content)
			? content.filter((block: RawEntry) => block?.type === "tool_result")
			: [];
		if (toolResults.length > 0) {
			// Tool results continue the current assistant's message group: a later
			// assistant row with the same message.id must still merge into it.
			for (const block of toolResults) {
				if (typeof block.tool_use_id !== "string") continue;
				messages.push({
					role: "tool_result",
					timestamp,
					toolUseId: block.tool_use_id,
					output: toolResultOutput(block.content),
					isError: block.is_error === true,
				});
			}
			const result = entry.toolUseResult;
			if (
				result &&
				typeof result === "object" &&
				typeof result.agentId === "string" &&
				toolResults.length === 1 &&
				typeof toolResults[0]?.tool_use_id === "string"
			) {
				agentIdByToolUseId.set(toolResults[0].tool_use_id, result.agentId);
			}
			continue;
		}

		currentAssistant = null;
		if (entry.isMeta === true || entry.isCompactSummary === true) continue;
		if (entry.isVisibleInTranscriptOnly === true) continue;
		const blocks = userBlocks(content, warnings).filter(
			(block) =>
				block.type !== "text" ||
				(!isLocalCommandText(block.text) && !INTERRUPT_MARKER.test(block.text.trim())),
		);
		if (blocks.length === 0) continue;
		messages.push({ role: "user", timestamp, blocks });
	}
	return messages;
}

function textOf(content: unknown): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	return content
		.filter((block: RawEntry) => block?.type === "text" && typeof block.text === "string")
		.map((block: RawEntry) => block.text)
		.join("\n");
}

/** Tool_use ids in a message list that belong to the subagent tool. */
function agentToolUseIds(messages: InterchangeMessage[]): Set<string> {
	const ids = new Set<string>();
	for (const message of messages) {
		if (message.role !== "assistant") continue;
		for (const block of message.blocks) {
			if (block.type === "tool_use" && block.name === NARRAFORK_AGENT_TOOL) ids.add(block.id);
		}
	}
	return ids;
}

export interface ClaudeCodeSubagentFile {
	/** From the file name when available; the rows' agentId otherwise. */
	agentId?: string;
	lines: Iterable<InterchangeLine>;
	/** `agentType` from the sibling `.meta.json`. */
	agentType?: string;
}

/**
 * Assemble a full session from a main transcript and any sidechain files.
 *
 * Sidechain files are matched to their spawning tool call through the Agent/Task
 * tool result's `toolUseResult.agentId`; a sidechain with no matching call is
 * dropped and reported, because NarraFork attaches every subagent to a call.
 */
export function parseClaudeCodeSession(input: {
	main: Iterable<InterchangeLine>;
	subagents?: ClaudeCodeSubagentFile[];
	fallbackSessionId?: string;
}): InterchangeSession {
	const main = parseClaudeCodeFile(input.main);
	const warnings = new WarningCollector();
	warnings.merge(main.warnings.list());

	const agentCalls = agentToolUseIds(main.messages);
	const toolUseIdByAgentId = new Map<string, string>();
	for (const [toolUseId, agentId] of main.agentIdByToolUseId) {
		if (agentCalls.has(toolUseId)) toolUseIdByAgentId.set(agentId, toolUseId);
	}
	const agentInputs = agentInputsById(main.messages);

	const subagents: InterchangeSubagent[] = [];
	for (const file of input.subagents ?? []) {
		const parsed = parseClaudeCodeFile(file.lines, { sidechain: true });
		warnings.merge(parsed.warnings.list());
		const agentId = file.agentId ?? parsed.agentId;
		const parentToolUseId = agentId ? toolUseIdByAgentId.get(agentId) : undefined;
		if (!agentId || !parentToolUseId) {
			warnings.add("missing_subagent", `sidechain ${agentId ?? "(unknown)"} has no parent call`);
			continue;
		}
		const callInput = agentInputs.get(parentToolUseId);
		subagents.push({
			ref: agentId,
			parentToolUseId,
			subagentType:
				file.agentType ??
				(typeof callInput?.subagent_type === "string" ? callInput.subagent_type : undefined),
			title: typeof callInput?.description === "string" ? callInput.description : undefined,
			messages: parsed.messages,
		});
	}

	// Link each call to its transcript so the importer knows which calls own a child.
	const refByToolUseId = new Map(subagents.map((sub) => [sub.parentToolUseId, sub.ref]));
	for (const message of main.messages) {
		if (message.role !== "assistant") continue;
		for (const block of message.blocks) {
			if (block.type === "tool_use" && refByToolUseId.has(block.id)) {
				block.subagentRef = refByToolUseId.get(block.id);
			}
		}
	}

	const now = new Date().toISOString();
	return {
		source: "claude-code",
		externalId: main.sessionId ?? input.fallbackSessionId ?? "",
		title: main.customTitle ?? main.aiTitle ?? titleFromText(firstUserText(main.messages)),
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

function agentInputsById(messages: InterchangeMessage[]): Map<string, Record<string, unknown>> {
	const inputs = new Map<string, Record<string, unknown>>();
	for (const message of messages) {
		if (message.role !== "assistant") continue;
		for (const block of message.blocks) {
			if (block.type === "tool_use" && isClaudeCodeAgentTool(block.name)) {
				inputs.set(block.id, block.input);
			}
		}
	}
	return inputs;
}
