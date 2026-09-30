/**
 * {@link InterchangeSession} → Codex rollout JSONL that `codex resume <id>` loads.
 *
 * Follows the turn layout of Codex's own importer
 * (`codex-rs/external-agent-migration/src/sessions/export.rs`), because two readers
 * consume a rollout and each needs different rows:
 *
 *  - the model history is rebuilt from `response_item` rows only;
 *  - the TUI transcript, and the resume picker's preview, are rebuilt from
 *    `event_msg` rows only (`user_message`, `agent_message`, `task_started`/…).
 *
 * Writing only one kind yields either a model that remembers a conversation the
 * user cannot see, or a transcript the model never saw. So every turn writes both.
 *
 * Always the `legacy` history mode, with no `ordinal`: paginated mode requires a
 * contiguous ordinal on every row and stops persisting the legacy `event_msg` rows
 * the TUI reads, which an exporter gains nothing from.
 *
 * Reasoning is never written: Codex replays `encrypted_content`, which only the
 * OpenAI account that produced it can decrypt, and a readable summary alone is not
 * something Codex sends back as reasoning.
 */

import {
	firstUserText,
	type InterchangeMessage,
	type InterchangeSession,
	type InterchangeToolResultMessage,
	type InterchangeUserBlock,
	titleFromText,
	toolResultText,
	WarningCollector,
} from "../model";
import { narraForkToolToCodex } from "../tool-mapping";

export const EXPORTED_CODEX_ORIGINATOR = "narrafork";
export const EXPORTED_CODEX_CLI_VERSION = "0.0.0";

export interface CodexSerializeOptions {
	threadId: string;
	cwd: string;
	/** Must match the user's configured provider for the session to show in `codex resume`. */
	modelProvider: string;
	gitBranch?: string;
	/** Set for a child thread spawned by `spawn_agent`. */
	parent?: { threadId: string; agentRole?: string; agentNickname?: string };
	/** Fresh ids for spawned child threads, keyed by nothing; called once per subagent. */
	newThreadId: () => string;
	/** Turn ids are opaque strings; injected so tests are deterministic. */
	newTurnId: () => string;
}

export interface SerializedCodexThread {
	threadId: string;
	/** Rollout creation time: drives the file name and folders. */
	createdAt: string;
	lines: string[];
}

export interface SerializedCodexSession {
	main: SerializedCodexThread;
	children: SerializedCodexThread[];
	title: string | undefined;
	warnings: ReturnType<WarningCollector["list"]>;
}

// biome-ignore lint/suspicious/noExplicitAny: rollout rows are open-ended external data
type Row = Record<string, any>;

class MonotonicClock {
	private last = 0;

	next(source: string | undefined): string {
		const parsed = source ? Date.parse(source) : Number.NaN;
		const candidate = Number.isFinite(parsed) ? parsed : this.last + 1;
		this.last = Math.max(candidate, this.last + 1);
		return new Date(this.last).toISOString();
	}

	get seconds(): number {
		return Math.floor(this.last / 1000);
	}
}

interface ThreadWriter {
	rows: Row[];
	clock: MonotonicClock;
	warnings: WarningCollector;
	options: CodexSerializeOptions;
	/** tool_use id → child thread id for spawn_agent calls with a transcript. */
	childIdByCallId: Map<string, string>;
}

function push(writer: ThreadWriter, type: string, payload: Row, timestamp?: string): void {
	writer.rows.push({ timestamp: writer.clock.next(timestamp), type, payload });
}

function userText(blocks: InterchangeUserBlock[]): string {
	return blocks
		.filter((block) => block.type === "text")
		.map((block) => (block as { text: string }).text)
		.join("\n");
}

function inputContent(blocks: InterchangeUserBlock[]): Row[] {
	return blocks.map((block) =>
		block.type === "text"
			? { type: "input_text", text: block.text }
			: { type: "input_image", image_url: `data:${block.mediaType};base64,${block.data}` },
	);
}

function functionOutput(output: InterchangeToolResultMessage["output"]): string | Row[] {
	return typeof output === "string" ? output : inputContent(output);
}

interface Turn {
	user: { timestamp: string; blocks: InterchangeUserBlock[] } | null;
	body: InterchangeMessage[];
}

/**
 * Split into turns at each user message. Rows before the first user message (a
 * transcript that opens with an assistant, or a compaction) form a leading turn
 * with no user row, which Codex tolerates.
 */
function splitTurns(messages: InterchangeMessage[]): Turn[] {
	const turns: Turn[] = [];
	let current: Turn = { user: null, body: [] };
	for (const message of messages) {
		if (message.role === "user") {
			if (current.user || current.body.length > 0) turns.push(current);
			current = { user: { timestamp: message.timestamp, blocks: message.blocks }, body: [] };
		} else {
			current.body.push(message);
		}
	}
	if (current.user || current.body.length > 0) turns.push(current);
	return turns;
}

function writeTurn(writer: ThreadWriter, turn: Turn): void {
	const turnId = writer.options.newTurnId();
	const startTimestamp = turn.user?.timestamp ?? turn.body[0]?.timestamp;
	push(
		writer,
		"event_msg",
		{
			type: "task_started",
			turn_id: turnId,
			model_context_window: null,
			collaboration_mode_kind: "default",
		},
		startTimestamp,
	);
	const startedAt = writer.clock.seconds;

	if (turn.user) {
		const text = userText(turn.user.blocks);
		const images = turn.user.blocks
			.filter((block) => block.type === "image")
			.map((block) => {
				const image = block as { mediaType: string; data: string };
				return `data:${image.mediaType};base64,${image.data}`;
			});
		push(
			writer,
			"event_msg",
			{ type: "user_message", message: text, images, local_images: [], text_elements: [] },
			turn.user.timestamp,
		);
		push(
			writer,
			"response_item",
			{ type: "message", role: "user", content: inputContent(turn.user.blocks) },
			turn.user.timestamp,
		);
	}

	const results = new Map<string, InterchangeToolResultMessage>();
	for (const message of turn.body) {
		if (message.role === "tool_result") results.set(message.toolUseId, message);
	}

	let lastAgentMessage: string | null = null;
	for (const message of turn.body) {
		if (message.role === "compact") {
			// A summary with no replacement history: Codex rebuilds the model's view
			// from the user messages it has already seen plus this summary.
			push(writer, "compacted", { message: message.summary }, message.timestamp);
			continue;
		}
		if (message.role !== "assistant") continue;

		// One model response: its text and calls in original order, then every
		// call's output. Parallel calls must all precede their outputs, because a
		// reader starts a new response at the first output it sees.
		let pendingText: string[] = [];
		const flushText = () => {
			const text = pendingText.join("\n\n");
			pendingText = [];
			if (!text) return;
			push(
				writer,
				"event_msg",
				{ type: "agent_message", message: text, phase: null, memory_citation: null },
				message.timestamp,
			);
			push(
				writer,
				"response_item",
				{ type: "message", role: "assistant", content: [{ type: "output_text", text }] },
				message.timestamp,
			);
			lastAgentMessage = text;
		};
		const pendingOutputs: Array<() => void> = [];
		for (const block of message.blocks) {
			if (block.type === "reasoning") {
				writer.warnings.add("dropped_reasoning");
			} else if (block.type === "text") {
				if (block.text) pendingText.push(block.text);
			} else {
				flushText();
				pendingOutputs.push(writeToolCall(writer, block, results.get(block.id), message.timestamp));
			}
		}
		flushText();
		for (const writeOutput of pendingOutputs) writeOutput();
	}

	push(writer, "event_msg", {
		type: "task_complete",
		turn_id: turnId,
		last_agent_message: lastAgentMessage,
		started_at: startedAt,
		completed_at: writer.clock.seconds,
	});
}

/**
 * Write the call row now and return a writer for its output row, so a response's
 * parallel calls can all be written before any of their outputs.
 */
function writeToolCall(
	writer: ThreadWriter,
	block: { id: string; name: string; input: Record<string, unknown> },
	result: InterchangeToolResultMessage | undefined,
	timestamp: string,
): () => void {
	const mapped = narraForkToolToCodex({ name: block.name, input: block.input });
	const childId = writer.childIdByCallId.get(block.id);
	let output: string | Row[];
	if (childId) {
		// Codex links a child thread to its call through spawn_agent's JSON answer.
		// The subagent's report rides along as `result`: without it the resumed model
		// would see only an id, while later turns were reasoned from the report.
		// A synthetic wait call would carry it more natively, but that tool's name
		// and argument shape differ between Codex versions; a plain field reads the
		// same everywhere.
		const report = result ? toolResultText(result.output) : "";
		output = JSON.stringify({
			agent_id: childId,
			nickname: null,
			...(report ? { result: report } : {}),
		});
	} else if (result) {
		output = functionOutput(result.output);
	} else {
		writer.warnings.add("unpaired_tool_use", block.id);
		output = "aborted";
	}
	const outputTimestamp = result?.timestamp ?? timestamp;

	if (mapped.kind === "custom") {
		push(
			writer,
			"response_item",
			{ type: "custom_tool_call", call_id: block.id, name: mapped.name, input: mapped.input },
			timestamp,
		);
		return () =>
			push(
				writer,
				"response_item",
				{
					type: "custom_tool_call_output",
					call_id: block.id,
					output: typeof output === "string" ? output : toolResultText(result?.output ?? ""),
				},
				outputTimestamp,
			);
	}
	push(
		writer,
		"response_item",
		{ type: "function_call", name: mapped.name, arguments: mapped.arguments, call_id: block.id },
		timestamp,
	);
	return () =>
		push(
			writer,
			"response_item",
			{ type: "function_call_output", call_id: block.id, output },
			outputTimestamp,
		);
}

function sessionMetaRow(
	options: CodexSerializeOptions,
	createdAt: string,
	gitBranch: string | undefined,
): Row {
	const source = options.parent
		? {
				subagent: {
					thread_spawn: {
						parent_thread_id: options.parent.threadId,
						depth: 1,
						...(options.parent.agentRole ? { agent_role: options.parent.agentRole } : {}),
						...(options.parent.agentNickname
							? { agent_nickname: options.parent.agentNickname }
							: {}),
					},
				},
			}
		: "cli";
	return {
		timestamp: createdAt,
		type: "session_meta",
		payload: {
			id: options.threadId,
			timestamp: createdAt,
			cwd: options.cwd,
			originator: EXPORTED_CODEX_ORIGINATOR,
			cli_version: EXPORTED_CODEX_CLI_VERSION,
			// Only cli/vscode sessions appear in the default resume picker.
			source,
			model_provider: options.modelProvider,
			history_mode: "legacy",
			...(options.parent ? { parent_thread_id: options.parent.threadId } : {}),
			...(gitBranch ? { git: { branch: gitBranch } } : {}),
		},
	};
}

function writeThread(
	messages: InterchangeMessage[],
	options: CodexSerializeOptions,
	createdAtSource: string,
	gitBranch: string | undefined,
	warnings: WarningCollector,
	childIdByCallId: Map<string, string>,
): SerializedCodexThread {
	const clock = new MonotonicClock();
	const createdAt = clock.next(createdAtSource);
	const writer: ThreadWriter = { rows: [], clock, warnings, options, childIdByCallId };
	for (const turn of splitTurns(messages)) writeTurn(writer, turn);
	const meta = sessionMetaRow(options, createdAt, gitBranch);
	return {
		threadId: options.threadId,
		createdAt,
		lines: [meta, ...writer.rows].map((row) => JSON.stringify(row)),
	};
}

export function serializeCodexSession(
	session: InterchangeSession,
	options: CodexSerializeOptions,
): SerializedCodexSession {
	const warnings = new WarningCollector();
	const childIdByCallId = new Map<string, string>();
	for (const sub of session.subagents)
		childIdByCallId.set(sub.parentToolUseId, options.newThreadId());

	const main = writeThread(
		session.messages,
		options,
		session.createdAt,
		options.gitBranch ?? session.gitBranch,
		warnings,
		childIdByCallId,
	);

	const children: SerializedCodexThread[] = [];
	for (const sub of session.subagents) {
		const threadId = childIdByCallId.get(sub.parentToolUseId);
		if (!threadId) continue;
		children.push(
			writeThread(
				sub.messages,
				{
					...options,
					threadId,
					parent: {
						threadId: options.threadId,
						...(sub.subagentType ? { agentRole: sub.subagentType } : {}),
						...(sub.title ? { agentNickname: sub.title } : {}),
					},
				},
				sub.messages[0]?.timestamp ?? session.createdAt,
				options.gitBranch ?? session.gitBranch,
				warnings,
				new Map(),
			),
		);
	}

	return {
		main,
		children,
		title: session.title?.trim() || titleFromText(firstUserText(session.messages)),
		warnings: warnings.list(),
	};
}

/** One `session_index.jsonl` row naming a thread; the newest row per id wins. */
export function codexSessionIndexLine(threadId: string, name: string, updatedAt: Date): string {
	return JSON.stringify({
		id: threadId,
		thread_name: name,
		updated_at: updatedAt.toISOString().replace(/\.\d{3}Z$/, "Z"),
	});
}
