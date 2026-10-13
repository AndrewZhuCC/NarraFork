/**
 * {@link InterchangeSession} → Claude Code transcript JSONL that `claude --resume`
 * loads and continues.
 *
 * Written as the simplest shape Claude Code accepts, not the shape it writes while
 * streaming: one linear `parentUuid` chain, one assistant row per model turn (all
 * blocks together), then one user row per tool result. Claude Code's API
 * normalization merges adjacent same-role rows anyway, and a list has none of the
 * DAG edge cases the streaming writer needs a recovery pass for. Results stay one
 * per row because `toolUseResult.agentId` is how a sidechain is found, and that
 * field can only name the single call its row answers.
 *
 * Invariants the CLI depends on, each with the failure it prevents:
 *  - timestamps strictly increase: `--resume` picks the newest non-sidechain leaf;
 *    a tie or an older tail row would resume from the wrong place.
 *  - every `parentUuid` resolves: a dangling parent silently truncates history.
 *  - metadata rows put `type` first: Claude Code finds titles with
 *    `startsWith('{"type":"custom-title"')`, so key order is load-bearing.
 *  - thinking is replayed only with a real signature: an unsigned or foreign one is
 *    sent back verbatim and the API rejects the request with 400.
 *  - the file ends on assistant text: a dangling tool_use makes Claude Code drop the
 *    whole assistant row, and a trailing user row is treated as an interrupted prompt.
 */

import {
	type InterchangeAssistantMessage,
	type InterchangeMessage,
	type InterchangeSession,
	type InterchangeToolResultMessage,
	type InterchangeUserBlock,
	lastUserText,
	WarningCollector,
} from "../model";
import { narraForkToolToClaudeCode } from "../tool-mapping";

/** Claude Code version stamped on exported rows. Nothing on the load path checks it. */
export const EXPORTED_CLAUDE_CODE_VERSION = "2.1.0";

/** Placeholder written when a transcript would otherwise end without assistant text. */
export const EXPORT_TAIL_TEXT = "(Session exported from NarraFork.)";

export interface ClaudeCodeSerializeOptions {
	sessionId: string;
	cwd: string;
	gitBranch?: string;
	/** Fresh UUIDs for each row. Injected so tests are deterministic. */
	newUuid: () => string;
	/** Fresh agent ids for subagent sidechains. */
	newAgentId: () => string;
	/** Drop every reasoning block even when it carries an Anthropic signature. */
	dropAllReasoning?: boolean;
	/** Fallback model name for assistant rows that recorded none. */
	defaultModel?: string;
}

export interface SerializedClaudeCodeSubagent {
	agentId: string;
	agentType: string;
	lines: string[];
	meta: string;
}

export interface SerializedClaudeCodeSession {
	sessionId: string;
	lines: string[];
	subagents: SerializedClaudeCodeSubagent[];
	warnings: ReturnType<WarningCollector["list"]>;
}

// biome-ignore lint/suspicious/noExplicitAny: JSONL rows are open-ended external data
type Row = Record<string, any>;

/**
 * Hands out strictly increasing ISO timestamps, never earlier than the source's own.
 * Two messages recorded in the same millisecond still get distinct, ordered stamps.
 */
class MonotonicClock {
	private last = 0;

	next(source: string | undefined): string {
		const parsed = source ? Date.parse(source) : Number.NaN;
		const candidate = Number.isFinite(parsed) ? parsed : this.last + 1;
		this.last = Math.max(candidate, this.last + 1);
		return new Date(this.last).toISOString();
	}
}

interface ChainContext {
	sessionId: string;
	cwd: string;
	gitBranch?: string;
	agentId?: string;
	isSidechain: boolean;
	options: ClaudeCodeSerializeOptions;
	clock: MonotonicClock;
	warnings: WarningCollector;
	rows: Row[];
	parentUuid: string | null;
	/** tool_use id → spawned sidechain agentId. */
	agentIdByToolUseId: Map<string, string>;
}

function stamp(ctx: ChainContext, row: Row, timestamp: string | undefined): Row {
	const uuid = ctx.options.newUuid();
	const full: Row = {
		parentUuid: ctx.parentUuid,
		isSidechain: ctx.isSidechain,
		userType: "external",
		cwd: ctx.cwd,
		sessionId: ctx.sessionId,
		version: EXPORTED_CLAUDE_CODE_VERSION,
		...(ctx.gitBranch ? { gitBranch: ctx.gitBranch } : {}),
		...(ctx.agentId ? { agentId: ctx.agentId } : {}),
		...row,
		uuid,
		timestamp: ctx.clock.next(timestamp),
	};
	ctx.rows.push(full);
	ctx.parentUuid = uuid;
	return full;
}

function userContent(blocks: InterchangeUserBlock[]): Row[] {
	return blocks.map((block) =>
		block.type === "text"
			? { type: "text", text: block.text }
			: {
					type: "image",
					source: { type: "base64", media_type: block.mediaType, data: block.data },
				},
	);
}

function toolResultContent(output: InterchangeToolResultMessage["output"]): string | Row[] {
	return typeof output === "string" ? output : userContent(output);
}

function writeUser(ctx: ChainContext, blocks: InterchangeUserBlock[], timestamp: string): void {
	if (blocks.length === 0) return;
	const content =
		blocks.length === 1 && blocks[0]?.type === "text" ? blocks[0].text : userContent(blocks);
	stamp(ctx, { type: "user", message: { role: "user", content } }, timestamp);
}

/** Returns the tool_use ids the row contains, in order. */
function writeAssistant(ctx: ChainContext, message: InterchangeAssistantMessage): string[] {
	const content: Row[] = [];
	const toolUseIds: string[] = [];
	for (const block of message.blocks) {
		if (block.type === "text") {
			if (block.text) content.push({ type: "text", text: block.text });
		} else if (block.type === "reasoning") {
			// Only a signature known to come from the official API is replayed: that is
			// the one upstream `claude --resume` talks to, and it rejects anything else
			// with a 400 on every turn. Unproven ones are dropped, never sent unsigned.
			const signature =
				block.signature?.provider === "anthropic" && block.signature.officialAnthropic
					? block.signature.value
					: "";
			if (signature && !ctx.options.dropAllReasoning) {
				content.push({ type: "thinking", thinking: block.text, signature });
			} else {
				ctx.warnings.add("dropped_reasoning");
			}
		} else {
			const mapped = narraForkToolToClaudeCode({ name: block.name, input: block.input });
			content.push({ type: "tool_use", id: block.id, name: mapped.name, input: mapped.input });
			toolUseIds.push(block.id);
		}
	}
	// A row whose only content was dropped reasoning must not exist: Claude Code
	// deletes thinking-only rows on resume and an empty one is an API error.
	if (!content.some((block) => block.type !== "thinking")) {
		if (content.length > 0) ctx.warnings.add("dropped_reasoning", undefined, content.length);
		return [];
	}
	stamp(
		ctx,
		{
			type: "assistant",
			message: {
				id: `msg_nf_${ctx.options.newUuid().replace(/-/g, "")}`,
				type: "message",
				role: "assistant",
				model: message.model ?? ctx.options.defaultModel ?? "claude-sonnet-4-5",
				content,
				stop_reason: toolUseIds.length > 0 ? "tool_use" : "end_turn",
				stop_sequence: null,
				usage: {
					input_tokens: 0,
					output_tokens: 0,
					cache_creation_input_tokens: 0,
					cache_read_input_tokens: 0,
				},
			},
		},
		message.timestamp,
	);
	return toolUseIds;
}

function writeToolResults(
	ctx: ChainContext,
	results: InterchangeToolResultMessage[],
	sourceAssistantUuid: string,
): void {
	if (results.length === 0) return;
	// One row per result, as Claude Code writes them: `toolUseResult` describes the
	// row's single call, so a turn with two Agent calls (or an Agent call next to a
	// Bash call) still links every sidechain to the right parent.
	for (const result of results) {
		const agentId = ctx.agentIdByToolUseId.get(result.toolUseId);
		stamp(
			ctx,
			{
				type: "user",
				message: {
					role: "user",
					content: [
						{
							type: "tool_result",
							tool_use_id: result.toolUseId,
							content: toolResultContent(result.output),
							...(result.isError ? { is_error: true } : {}),
						},
					],
				},
				sourceToolAssistantUUID: sourceAssistantUuid,
				...(agentId ? { toolUseResult: { agentId } } : {}),
			},
			result.timestamp,
		);
	}
}

function writeCompact(ctx: ChainContext, summary: string, timestamp: string): void {
	const logicalParentUuid = ctx.parentUuid;
	ctx.parentUuid = null;
	stamp(
		ctx,
		{
			type: "system",
			subtype: "compact_boundary",
			content: "Conversation compacted",
			isMeta: false,
			level: "info",
			logicalParentUuid,
			compactMetadata: { trigger: "manual", preTokens: 0 },
		},
		timestamp,
	);
	stamp(
		ctx,
		{
			type: "user",
			message: { role: "user", content: summary },
			isCompactSummary: true,
			isVisibleInTranscriptOnly: true,
		},
		timestamp,
	);
}

function writeChain(ctx: ChainContext, messages: InterchangeMessage[]): void {
	const results = new Map<string, InterchangeToolResultMessage>();
	for (const message of messages) {
		if (message.role === "tool_result") results.set(message.toolUseId, message);
	}

	for (const message of messages) {
		if (message.role === "user") {
			writeUser(ctx, message.blocks, message.timestamp);
		} else if (message.role === "compact") {
			writeCompact(ctx, message.summary, message.timestamp);
		} else if (message.role === "assistant") {
			const toolUseIds = writeAssistant(ctx, message);
			if (toolUseIds.length === 0) continue;
			const assistantUuid = ctx.parentUuid ?? "";
			const paired: InterchangeToolResultMessage[] = [];
			for (const id of toolUseIds) {
				const result = results.get(id);
				if (result) {
					paired.push(result);
				} else {
					// Claude Code drops the whole assistant row when a tool_use has no
					// result, so synthesize one rather than lose the row's text.
					ctx.warnings.add("unpaired_tool_use", id);
					paired.push({
						role: "tool_result",
						timestamp: message.timestamp,
						toolUseId: id,
						output: "Tool call did not complete.",
						isError: true,
					});
				}
			}
			writeToolResults(ctx, paired, assistantUuid);
		}
		// tool_result rows were written alongside their assistant.
	}

	const tail = ctx.rows.at(-1);
	const endsOnAssistantText =
		tail?.type === "assistant" &&
		tail.message?.content?.some((block: Row) => block.type === "text");
	if (!endsOnAssistantText) {
		stamp(
			ctx,
			{
				type: "assistant",
				message: {
					id: `msg_nf_${ctx.options.newUuid().replace(/-/g, "")}`,
					type: "message",
					role: "assistant",
					model: ctx.options.defaultModel ?? "claude-sonnet-4-5",
					content: [{ type: "text", text: EXPORT_TAIL_TEXT }],
					stop_reason: "end_turn",
					stop_sequence: null,
					usage: {
						input_tokens: 0,
						output_tokens: 0,
						cache_creation_input_tokens: 0,
						cache_read_input_tokens: 0,
					},
				},
			},
			undefined,
		);
	}
}

/** Metadata rows with `type` as the first key, which is how Claude Code locates them. */
function metadataRow(type: string, fields: Row): string {
	return JSON.stringify({ type, ...fields });
}

export function serializeClaudeCodeSession(
	session: InterchangeSession,
	options: ClaudeCodeSerializeOptions,
): SerializedClaudeCodeSession {
	const warnings = new WarningCollector();
	const agentIdByToolUseId = new Map<string, string>();
	const subagentsByToolUseId = new Map(session.subagents.map((sub) => [sub.parentToolUseId, sub]));
	for (const toolUseId of subagentsByToolUseId.keys()) {
		agentIdByToolUseId.set(toolUseId, options.newAgentId());
	}

	const main: ChainContext = {
		sessionId: options.sessionId,
		cwd: options.cwd,
		gitBranch: options.gitBranch ?? session.gitBranch,
		isSidechain: false,
		options,
		clock: new MonotonicClock(),
		warnings,
		rows: [],
		parentUuid: null,
		agentIdByToolUseId,
	};
	writeChain(main, session.messages);

	const lines = main.rows.map((row) => JSON.stringify(row));
	const title = session.title?.trim();
	if (title)
		lines.push(metadataRow("custom-title", { customTitle: title, sessionId: options.sessionId }));
	const lastPrompt = lastUserText(session.messages);
	if (lastPrompt) {
		lines.push(
			metadataRow("last-prompt", {
				lastPrompt: lastPrompt.replace(/\s+/g, " ").slice(0, 200),
				sessionId: options.sessionId,
			}),
		);
	}

	const subagents: SerializedClaudeCodeSubagent[] = [];
	for (const [toolUseId, sub] of subagentsByToolUseId) {
		const agentId = agentIdByToolUseId.get(toolUseId);
		if (!agentId) continue;
		const ctx: ChainContext = {
			...main,
			agentId,
			isSidechain: true,
			clock: new MonotonicClock(),
			rows: [],
			parentUuid: null,
			// Nested subagents are not modelled; a sidechain's own Agent calls stay plain.
			agentIdByToolUseId: new Map(),
		};
		writeChain(ctx, sub.messages);
		const agentType = sub.subagentType ?? "general-purpose";
		subagents.push({
			agentId,
			agentType,
			lines: ctx.rows.map((row) => JSON.stringify(row)),
			meta: JSON.stringify({ agentType }),
		});
	}

	return { sessionId: options.sessionId, lines, subagents, warnings: warnings.list() };
}
