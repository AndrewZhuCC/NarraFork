/**
 * Format-neutral intermediate representation for exchanging agent sessions with
 * external CLIs (Claude Code, Codex).
 *
 * Every importer parses its source format into an {@link InterchangeSession}, and
 * every exporter serializes one back out. Keeping the NarraFork mapping on one side
 * of this type and each CLI's wire format on the other means a CC → NarraFork →
 * Codex round trip shares a single set of rules, and each half can be unit tested
 * without a database.
 *
 * Pure data only: no IO, no NarraFork database types. The shapes deliberately mirror
 * the Anthropic content-block vocabulary NarraFork already stores, because that is
 * the richest of the three and the others map into it without loss of structure.
 */

export type InterchangeFormat = "claude-code" | "codex";
export type InterchangeSourceKind = InterchangeFormat | "narrafork";

/**
 * One JSONL row as handed to a parser: raw text, or an object the caller already
 * decoded. Servers decode large files in time slices before parsing, so a 60 MB
 * transcript never becomes one long synchronous `JSON.parse` run.
 */
export type InterchangeLine = string | Record<string, unknown>;

export interface InterchangeTextBlock {
	type: "text";
	text: string;
}

export interface InterchangeImageBlock {
	type: "image";
	mediaType: string;
	/** Standard base64 without a data-URL prefix. */
	data: string;
}

/**
 * Model reasoning. The signature is kept only as evidence of where it came from:
 * whether a destination may replay it is the destination's decision, because a
 * signature minted by one upstream is rejected by any other.
 *
 * `officialAnthropic` records whether the signature is known to come from the
 * official Anthropic API, which is the only upstream `claude --resume` talks to.
 * It is true for signatures read out of a Claude Code transcript (Claude Code
 * only ever talks to that API) and for NarraFork messages minted by a provider
 * configured as the official API. Relays and gateways (NUG channels, proxies)
 * sign with their own keys, and the official API rejects those with a 400 on
 * every turn, so they are false.
 */
export interface InterchangeReasoningBlock {
	type: "reasoning";
	text: string;
	signature?: { provider: "anthropic" | "openai"; value: string; officialAnthropic?: boolean };
}

export interface InterchangeToolUseBlock {
	type: "tool_use";
	id: string;
	name: string;
	input: Record<string, unknown>;
	/**
	 * Set when this call spawned a subagent whose transcript is available; the value
	 * is {@link InterchangeSubagent.ref}.
	 */
	subagentRef?: string;
}

export type InterchangeUserBlock = InterchangeTextBlock | InterchangeImageBlock;
export type InterchangeAssistantBlock =
	| InterchangeTextBlock
	| InterchangeReasoningBlock
	| InterchangeToolUseBlock;

export interface InterchangeUserMessage {
	role: "user";
	timestamp: string;
	blocks: InterchangeUserBlock[];
}

export interface InterchangeAssistantMessage {
	role: "assistant";
	timestamp: string;
	model?: string;
	blocks: InterchangeAssistantBlock[];
}

export interface InterchangeToolResultMessage {
	role: "tool_result";
	timestamp: string;
	toolUseId: string;
	output: string | InterchangeUserBlock[];
	isError: boolean;
}

/**
 * A context-compaction boundary. Everything before it stays in the transcript for
 * display; the destination decides whether its model sees it.
 */
export interface InterchangeCompactMessage {
	role: "compact";
	timestamp: string;
	summary: string;
}

export type InterchangeMessage =
	| InterchangeUserMessage
	| InterchangeAssistantMessage
	| InterchangeToolResultMessage
	| InterchangeCompactMessage;

export interface InterchangeSubagent {
	/** Stable id within this session (CC agentId, Codex child thread id, NarraFork narrator id). */
	ref: string;
	/** The parent tool_use that spawned it. */
	parentToolUseId: string;
	subagentType?: string;
	title?: string;
	messages: InterchangeMessage[];
}

export type InterchangeWarningKind =
	| "unparseable_line"
	| "off_chain_line"
	| "dropped_reasoning"
	| "dropped_injected_context"
	| "dropped_unsupported_block"
	| "unpaired_tool_use"
	| "orphan_tool_result"
	| "missing_subagent"
	| "rolled_back_turns"
	| "truncated_output"
	/** Export: the session's cwd does not exist on the machine it was written to. */
	| "target_cwd_missing"
	/** Export: Codex's session_index.jsonl could not be updated, so no title shows. */
	| "session_index_skipped";

export interface InterchangeWarning {
	kind: InterchangeWarningKind;
	count: number;
	/** A short human-readable example, never message bodies of any size. */
	sample?: string;
}

export interface InterchangeSession {
	source: InterchangeSourceKind;
	/** The source's own id (CC sessionId, Codex thread id, NarraFork narrator id). */
	externalId: string;
	title?: string;
	cwd?: string;
	gitBranch?: string;
	model?: string;
	createdAt: string;
	updatedAt: string;
	messages: InterchangeMessage[];
	subagents: InterchangeSubagent[];
	warnings: InterchangeWarning[];
}

/** Accumulates warnings by kind so a long transcript reports counts, not one row per line. */
export class WarningCollector {
	private readonly byKind = new Map<InterchangeWarningKind, InterchangeWarning>();

	add(kind: InterchangeWarningKind, sample?: string, count = 1): void {
		const existing = this.byKind.get(kind);
		if (existing) {
			existing.count += count;
			return;
		}
		this.byKind.set(kind, {
			kind,
			count,
			...(sample ? { sample: truncateSample(sample) } : {}),
		});
	}

	merge(warnings: readonly InterchangeWarning[]): void {
		for (const warning of warnings) this.add(warning.kind, warning.sample, warning.count);
	}

	list(): InterchangeWarning[] {
		return [...this.byKind.values()];
	}
}

const SAMPLE_MAX_CHARS = 120;

function truncateSample(value: string): string {
	const flat = value.replace(/\s+/g, " ").trim();
	return flat.length > SAMPLE_MAX_CHARS ? `${flat.slice(0, SAMPLE_MAX_CHARS)}…` : flat;
}

/**
 * A copy of `session` with every tool_use id replaced by `newId()`.
 *
 * Provider tool_use ids are only unique within one conversation, and a session can
 * be imported more than once (or exported and imported back). Several NarraFork
 * paths look rows up by `toolUseId` / `parentToolUseId` across narrators, so two
 * narrators sharing an id would see each other's subagent messages and make revert
 * refuse both. Rewriting on import is what keeps those lookups single-owner.
 *
 * Calls, their results and each subagent's `parentToolUseId` are rewritten through
 * one map, so pairing survives. A result or subagent naming an id no call in its
 * thread used keeps a fresh id of its own: it stays unpaired, as it was, instead of
 * colliding with anything.
 */
export function withFreshToolUseIds(
	session: InterchangeSession,
	newId: () => string,
): InterchangeSession {
	const rewriteThread = (messages: InterchangeMessage[], ids: Map<string, string>) => {
		const idFor = (original: string) => {
			let fresh = ids.get(original);
			if (!fresh) {
				fresh = newId();
				ids.set(original, fresh);
			}
			return fresh;
		};
		return messages.map((message): InterchangeMessage => {
			if (message.role === "assistant") {
				return {
					...message,
					blocks: message.blocks.map((block) =>
						block.type === "tool_use" ? { ...block, id: idFor(block.id) } : block,
					),
				};
			}
			if (message.role === "tool_result")
				return { ...message, toolUseId: idFor(message.toolUseId) };
			return message;
		});
	};

	const mainIds = new Map<string, string>();
	const messages = rewriteThread(session.messages, mainIds);
	const subagents = session.subagents.map((sub) => ({
		...sub,
		// The parent call lives in the main thread, so it shares the main map.
		parentToolUseId: mainIds.get(sub.parentToolUseId) ?? newId(),
		messages: rewriteThread(sub.messages, new Map()),
	}));
	return { ...session, messages, subagents };
}

/** Plain text of a tool result, with images reduced to a placeholder. */
export function toolResultText(output: InterchangeToolResultMessage["output"]): string {
	if (typeof output === "string") return output;
	return output.map((block) => (block.type === "text" ? block.text : "[image]")).join("\n");
}

/** First user text in a message list, for titles and previews. */
export function firstUserText(messages: readonly InterchangeMessage[]): string | undefined {
	for (const message of messages) {
		if (message.role !== "user") continue;
		for (const block of message.blocks) {
			if (block.type === "text" && block.text.trim()) return block.text.trim();
		}
	}
	return undefined;
}

/** Last user text in a message list, for "last prompt" metadata. */
export function lastUserText(messages: readonly InterchangeMessage[]): string | undefined {
	for (let i = messages.length - 1; i >= 0; i--) {
		const message = messages[i];
		if (message?.role !== "user") continue;
		for (const block of message.blocks) {
			if (block.type === "text" && block.text.trim()) return block.text.trim();
		}
	}
	return undefined;
}

/** A one-line title derived from free text. */
export function titleFromText(text: string | undefined, maxChars = 80): string | undefined {
	if (!text) return undefined;
	const line = text
		.split("\n")
		.find((l) => l.trim())
		?.trim();
	if (!line) return undefined;
	return line.length > maxChars ? `${line.slice(0, maxChars)}…` : line;
}
