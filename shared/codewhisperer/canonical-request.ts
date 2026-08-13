/**
 * Canonical plugin DTO → Kiro wire format.
 *
 * The host's plugin provider contract passes conversations in a provider-neutral shape
 * (`ProviderMessage[]` / `ProviderContentBlock[]`, see
 * `server/services/plugin-provider-rpc.ts`). An out-of-process Kiro provider has to turn
 * that back into Kiro's `conversationState` wire format, which the in-core
 * `KiroProvider` builds directly from database rows instead.
 *
 * This module is that translation. It lives in `shared/` because both consumers must agree
 * byte-for-byte and neither may import the other: the host uses it to assert parity in
 * tests, and the `kiro-external` plugin bundles it to run in its own process.
 * `shared-core.guard.test.ts` enforces the no-host-imports rule for every file here.
 *
 * ## Why a dedicated converter rather than passing canonical through `buildKiroRequest`
 *
 * `buildKiroRequest` takes `history`/`tools`/`toolResults` as `unknown[]` and casts them to
 * the Kiro types — it assumes its caller already produced Kiro shapes. Handing it canonical
 * blocks type-checks and then sends `{"role":"assistant","content":[...]}` to a service that
 * expects `{"assistantResponseMessage":{...}}`, which fails as a 400 ValidationException.
 * So the conversion has to happen before that call, which is what {@link toKiroChatRequest}
 * does.
 *
 * ## Division of labour with `normalizeKiroChatRequest`
 *
 * `normalizeKiroChatRequest` is a safety net, not a converter. It repairs *structural*
 * problems in already-Kiro-shaped messages: it merges consecutive same-role turns, inserts
 * synthetic pairs to preserve user/assistant alternation, drops orphan `toolUses` and
 * `toolResults`, and injects a continuation prompt when the current turn ends up empty.
 *
 * It cannot repair anything that is not yet Kiro-shaped, because it dispatches on
 * `"userInputMessage" in message`. Everything below is therefore this module's job:
 *
 * - mapping `role: "system"` (no Kiro equivalent) onto a user + assistant-ack pair;
 * - folding `role: "tool"` messages into the following user turn's `toolResults`;
 * - stamping `modelId` and `origin` on every user message;
 * - emitting reasoning as snake_case `reasoning_content` with source-gated signatures;
 * - lifting images out of tool results, which Kiro's tool results cannot carry;
 * - degrading `web_search` / `image_generation` blocks, which Kiro has no field for.
 *
 * Tool-call pairing is deliberately left to the normalizer: it already owns a global
 * `consumedToolUseIds` ledger and a three-pass compact/sanitize pipeline that this module
 * would only duplicate less well. What this module guarantees is that pairs arrive in the
 * order the normalizer expects, since `sanitizeKiroToolPairs` only matches a `toolResult`
 * against the *immediately preceding* assistant message and silently drops the rest.
 */

import { buildKiroReasoningContent, buildKiroRequest, normalizeKiroChatRequest } from "./protocol";
import type {
	KiroChatRequest,
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroImage,
	KiroReasoningContent,
	KiroTool,
	KiroToolResult,
	KiroUserInputMessageContext,
} from "./protocol-types";
import type { NormalizeRuntime } from "./runtime";
import type { ReasoningProviderMetadata } from "./types";

/**
 * A canonical content block.
 *
 * Mirrors `ProviderContentBlock` from the host's RPC contract. Typed loosely on purpose:
 * these values crossed a process boundary as JSON, so every field is validated at the point
 * of use rather than trusted from a declared type.
 */
export interface CanonicalBlock {
	type: string;
	[key: string]: unknown;
}

/** A canonical message. Mirrors `ProviderMessage`. */
export interface CanonicalMessage {
	role: string;
	content?: unknown;
	messageId?: string;
}

/** A canonical tool definition. Mirrors `ProviderToolDefinition`. */
export interface CanonicalTool {
	name: string;
	description?: string;
	inputSchema?: Record<string, unknown>;
}

/**
 * The single reasoning-source identity this converter uses.
 *
 * `buildKiroReasoningContent` only echoes a thinking signature when the source recorded on
 * the block equals the `currentSource` it is given (`signatureSourcesCompatible`). Both
 * values are produced here, so the constant only has to be self-consistent — it is never
 * compared against a host-side source string.
 *
 * That matters because the host's own source label is not stable across requests. The host
 * derives it from `plugin:<providerTypeId>:<providerInstanceId>` but lets a plugin override
 * it via `request_started`, and the registry builds a fresh adapter per model resolution.
 * Re-labelling here keeps signature round-tripping independent of that.
 *
 * See {@link toKiroChatRequest} for why the plugin must not send `reasoningSource`.
 */
const REASONING_SOURCE = "kiro";

/**
 * Assistant acknowledgement for the injected system prompt.
 *
 * Kiro has no system role: the in-core provider fakes one with a user turn carrying the
 * prompt plus an assistant turn acknowledging it (`KiroProvider.injectSystemPrompt`). The
 * host localises this string, but the chat path never populates
 * `request.options.locale` — `buildChatParams` only forwards `reasoningEffort`,
 * `serviceTier` and `metadata` — so a caller with no locale gets the English text that
 * `getToolMessage("systemPromptAck", "en")` returns.
 */
const SYSTEM_PROMPT_ACK_EN = "I will follow these instructions.";

/** Localised acknowledgements, for the generate path where a locale does arrive. */
const SYSTEM_PROMPT_ACK: Record<string, string> = {
	en: SYSTEM_PROMPT_ACK_EN,
	"zh-CN": "我会遵循这些指示。",
};

/** Resolve the ack text for a locale, falling back to English for anything unknown. */
export function systemPromptAck(locale?: string): string {
	if (!locale) return SYSTEM_PROMPT_ACK_EN;
	return SYSTEM_PROMPT_ACK[locale] ?? SYSTEM_PROMPT_ACK_EN;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asString(value: unknown): string | undefined {
	return typeof value === "string" && value.length > 0 ? value : undefined;
}

function blocksOf(message: CanonicalMessage): CanonicalBlock[] {
	if (!Array.isArray(message.content)) return [];
	return message.content.filter((block): block is CanonicalBlock => {
		return isRecord(block) && typeof block.type === "string";
	});
}

/**
 * Normalise a media type or bare format into a Kiro image format.
 *
 * Kiro accepts exactly four formats. Anything else (svg, bmp, a malformed media type)
 * falls back to `png`, matching what `buildKiroHistory` does for unrecognised mime types:
 * the bytes are still sent and the service sniffs the real format.
 */
function imageFormat(mediaTypeOrFormat: unknown): KiroImage["format"] {
	const raw = asString(mediaTypeOrFormat)?.toLowerCase() ?? "";
	const bare = raw.replace(/^image\//, "");
	if (bare === "jpeg" || bare === "jpg") return "jpeg";
	if (bare === "gif") return "gif";
	if (bare === "webp") return "webp";
	return "png";
}

/** Convert a canonical `image` block into a Kiro image, or skip it when data is missing. */
function toKiroImage(block: CanonicalBlock): KiroImage | undefined {
	const data = asString(block.dataBase64) ?? asString(block.base64);
	if (!data) return undefined;
	return {
		format: imageFormat(block.mediaType ?? block.format),
		source: { bytes: data },
	};
}

/** Concatenate the text blocks of a canonical content array. */
function textOf(blocks: CanonicalBlock[]): string {
	return blocks
		.filter((block) => block.type === "text")
		.map((block) => asString(block.text) ?? "")
		.filter((text) => text.length > 0)
		.join("\n");
}

/**
 * Render `web_search` and `image_generation` blocks as plain text.
 *
 * Kiro's assistant history has no field for either, so the alternative to a text summary is
 * dropping them — which loses the fact that the assistant searched at all. The
 * `[Web search: ...]` wording matches `buildKiroHistory` so both paths replay the same
 * history for the same conversation.
 */
function auxiliaryText(blocks: CanonicalBlock[]): string {
	const parts: string[] = [];
	for (const block of blocks) {
		if (block.type === "web_search") {
			const queries = Array.isArray(block.queries)
				? block.queries.filter((query): query is string => typeof query === "string")
				: [];
			const query = asString(block.query) ?? (queries.length > 0 ? queries.join(", ") : "unknown");
			parts.push(`[Web search: ${query}]`);
			continue;
		}
		if (block.type === "image_generation") {
			const revised = asString(block.revisedPrompt);
			parts.push(revised ? `[Image generated: ${revised}]` : "[Image generated]");
		}
	}
	return parts.join("\n");
}

/**
 * Extract the reasoning content for one assistant turn.
 *
 * Signatures live at `continuation.data.anthropic.signature`: the plugin's event mapper
 * forwards the whole `ReasoningProviderMetadata` as the continuation payload, and the host
 * clones it through unchanged. The `format` check keeps this from mis-reading a payload
 * minted by some other reasoning format that happens to be present.
 *
 * Both this function and `buildKiroReasoningContent` see {@link REASONING_SOURCE}, so a
 * signature that survived the host's own compatibility check is echoed, and a block whose
 * continuation the host already dropped simply arrives without one and replays as unsigned
 * thinking text.
 */
function reasoningContentOf(blocks: CanonicalBlock[]): KiroReasoningContent | undefined {
	const reasoningBlocks: Array<{
		text: string;
		providerMetadata?: ReasoningProviderMetadata;
	}> = [];
	const redactedBlocks: Array<{ data: string; signatureSource?: string }> = [];

	for (const block of blocks) {
		if (block.type === "reasoning") {
			const text = asString(block.text) ?? "";
			const continuation = isRecord(block.continuation) ? block.continuation : undefined;
			const signature =
				continuation?.format === "anthropic" && isRecord(continuation.data)
					? asString(
							(continuation.data.anthropic as Record<string, unknown> | undefined)?.signature,
						)
					: undefined;
			// An unsigned thinking block still has to be replayed; only the signature is
			// conditional. `signatureSource` sits beside `anthropic`, not inside it —
			// nesting it would make the compatibility check read undefined and drop the
			// signature silently.
			reasoningBlocks.push({
				text,
				...(signature
					? {
							providerMetadata: {
								anthropic: { signature },
								signatureSource: REASONING_SOURCE,
							},
						}
					: {}),
			});
			continue;
		}
		if (block.type === "redacted_reasoning") {
			const data = asString(block.data);
			if (!data) continue;
			// The canonical field is `source`; the builder reads `signatureSource`. The host
			// only sets `source` when it verified the block belongs to this upstream, so its
			// presence — not its value — is what gates the replay.
			redactedBlocks.push({
				data,
				...(asString(block.source) ? { signatureSource: REASONING_SOURCE } : {}),
			});
		}
	}

	if (reasoningBlocks.length === 0 && redactedBlocks.length === 0) return undefined;
	return buildKiroReasoningContent(reasoningBlocks, redactedBlocks, REASONING_SOURCE);
}

/**
 * Convert a canonical `tool_result` block into a Kiro tool result.
 *
 * Images are pulled out into `images` rather than embedded: Kiro tool results carry text
 * only. For the current turn the caller lifts them onto `currentMessage.images`, which is
 * how the in-core path handles a `Read` tool returning an image. In history they are
 * degraded to a text marker, matching `buildKiroHistory`, whose tool results are always
 * `[{ text }]`.
 */
function toKiroToolResult(block: CanonicalBlock):
	| {
			result: KiroToolResult;
			images: KiroImage[];
	  }
	| undefined {
	const toolUseId = asString(block.toolUseId) ?? asString(block.tool_call_id);
	if (!toolUseId) return undefined;

	const parts: string[] = [];
	const images: KiroImage[] = [];
	const content = Array.isArray(block.content) ? block.content : [];
	for (const entry of content) {
		if (!isRecord(entry)) continue;
		if (entry.type === "text") {
			const text = asString(entry.text);
			if (text) parts.push(text);
			continue;
		}
		if (entry.type === "image") {
			const image = toKiroImage(entry as CanonicalBlock);
			if (image) images.push(image);
		}
	}
	// A tool result with no content at all still needs the field: the service rejects a
	// tool result whose `content` is absent.
	const isError = block.isError === true || block.is_error === true;
	return {
		result: {
			toolUseId,
			content: [{ text: parts.join("\n") }],
			status: isError ? "error" : "success",
			isError,
		},
		images,
	};
}

/** Convert canonical tool definitions into Kiro tool specifications. */
export function toKiroTools(tools: readonly unknown[] | undefined): KiroTool[] {
	if (!Array.isArray(tools)) return [];
	const result: KiroTool[] = [];
	for (const tool of tools) {
		if (!isRecord(tool)) continue;
		const name = asString(tool.name);
		if (!name) continue;
		const schema = isRecord(tool.inputSchema)
			? tool.inputSchema
			: isRecord(tool.parameters)
				? tool.parameters
				: {};
		result.push({
			toolSpecification: {
				name,
				description: typeof tool.description === "string" ? tool.description : "",
				// Kiro truncates a tool definition whose schema has no required property, so
				// the in-core path runs `ensureNonEmptySchema` here. Mirrored below rather
				// than imported: that helper lives in the host's tool registry, which pulls
				// in zod and must stay out of a plugin bundle.
				inputSchema: { json: ensureRequiredProperty(schema) },
			},
		});
	}
	return result;
}

/**
 * Give a JSON Schema at least one required property.
 *
 * The required property is ALWAYS an injected dummy `confirm`, never an existing
 * field: promoting the first declared property misrepresents the tool's contract
 * (ExitPlanMode once advertised its advisory `allowedPrompts` as the only
 * required parameter while the plan body stayed optional, so models fabricated
 * content for it). A `const: true` dummy leaves no room to invent anything.
 *
 * Behaviourally identical to the host's `ensureNonEmptySchema`
 * (`server/lib/agent/tool-registry.ts`), duplicated because that module imports zod and so
 * cannot be bundled into a plugin. `kiro-external-canonical-request.test.ts` asserts the two
 * agree so this copy cannot drift.
 */
function ensureRequiredProperty(schema: Record<string, unknown>): Record<string, unknown> {
	const required = Array.isArray(schema.required) ? schema.required : undefined;
	if (required && required.length > 0) return schema;
	const properties = isRecord(schema.properties) ? schema.properties : undefined;
	return {
		...schema,
		properties: {
			...(properties ?? {}),
			confirm: {
				type: "boolean",
				description: "Dummy parameter (always pass true)",
				const: true,
				default: true,
			},
		},
		required: ["confirm"],
	};
}

/** A user message under construction, before it is committed to history. */
interface PendingUser {
	text: string[];
	images: KiroImage[];
	toolResults: KiroToolResult[];
}

function emptyPendingUser(): PendingUser {
	return { text: [], images: [], toolResults: [] };
}

function pendingUserHasContent(pending: PendingUser): boolean {
	return (
		pending.text.some((text) => text.trim().length > 0) ||
		pending.images.length > 0 ||
		pending.toolResults.length > 0
	);
}

function commitPendingUser(
	history: KiroHistoryMessage[],
	pending: PendingUser,
	modelId: string,
): void {
	if (!pendingUserHasContent(pending)) return;
	const context: KiroUserInputMessageContext =
		pending.toolResults.length > 0 ? { toolResults: pending.toolResults } : {};
	const message: KiroHistoryUserMessage = {
		userInputMessage: {
			content: pending.text.join("\n"),
			modelId,
			origin: "AI_EDITOR",
			...(pending.images.length > 0 ? { images: pending.images } : {}),
			userInputMessageContext: context,
		},
	};
	history.push(message);
}

export interface CanonicalHistoryResult {
	history: KiroHistoryMessage[];
}

/**
 * Convert canonical history into Kiro history messages.
 *
 * Role handling:
 *
 * - `system` becomes a user turn carrying the prompt plus an assistant ack, matching
 *   `KiroProvider.injectSystemPrompt`. Multiple system messages each get their own pair;
 *   the normalizer merges them if they end up adjacent.
 * - `tool` accumulates into the next user turn's `toolResults`, because Kiro attaches tool
 *   results to a user message instead of giving them their own role.
 * - `user` flushes any accumulated tool results together with its own text and images.
 * - `assistant` flushes pending tool results into a synthetic user turn first, so the
 *   results land *before* the next assistant message and stay adjacent to the tool calls
 *   they answer — which is the only arrangement `sanitizeKiroToolPairs` accepts.
 *
 * Trailing user/tool material is committed at the end, and a trailing user turn is paired
 * with a synthetic `"OK"` assistant so the caller's current message does not follow a user
 * message directly.
 */
export function toKiroHistory(
	messages: readonly unknown[] | undefined,
	modelId: string,
	locale?: string,
	ack?: string,
): CanonicalHistoryResult {
	const history: KiroHistoryMessage[] = [];
	if (!Array.isArray(messages)) return { history };
	const systemAck = ack ?? systemPromptAck(locale);

	let pending = emptyPendingUser();

	for (const raw of messages) {
		if (!isRecord(raw) || typeof raw.role !== "string") continue;
		const message: CanonicalMessage = { ...raw, role: raw.role };
		const blocks = blocksOf(message);

		if (message.role === "system") {
			// Flush first: the system pair must not absorb tool results belonging to an
			// earlier assistant turn.
			commitPendingUser(history, pending, modelId);
			pending = emptyPendingUser();
			const text = textOf(blocks);
			if (!text) continue;
			history.push({
				userInputMessage: { content: text, modelId, userInputMessageContext: {} },
			});
			history.push({ assistantResponseMessage: { content: systemAck } });
			continue;
		}

		if (message.role === "tool") {
			for (const block of blocks) {
				if (block.type !== "tool_result") continue;
				const converted = toKiroToolResult(block);
				if (!converted) continue;
				pending.toolResults.push(converted.result);
				// Kiro history tool results cannot carry images. Record that one existed so
				// the turn does not silently lose the fact, then drop the bytes — the
				// in-core path has the same limitation.
				for (const _image of converted.images) {
					pending.text.push("[Image omitted from tool result]");
				}
			}
			continue;
		}

		if (message.role === "user") {
			const text = textOf(blocks);
			if (text) pending.text.push(text);
			for (const block of blocks) {
				if (block.type === "image") {
					const image = toKiroImage(block);
					if (image) pending.images.push(image);
					continue;
				}
				if (block.type === "tool_result") {
					const converted = toKiroToolResult(block);
					if (!converted) continue;
					pending.toolResults.push(converted.result);
					for (const image of converted.images) pending.images.push(image);
				}
			}
			commitPendingUser(history, pending, modelId);
			pending = emptyPendingUser();
			continue;
		}

		if (message.role !== "assistant") continue;

		// Tool results answer the *previous* assistant turn, so they must be committed
		// before this one is appended.
		commitPendingUser(history, pending, modelId);
		pending = emptyPendingUser();

		const texts: string[] = [];
		const primary = textOf(blocks);
		if (primary) texts.push(primary);
		const auxiliary = auxiliaryText(blocks);
		if (auxiliary) texts.push(auxiliary);

		const toolUses: NonNullable<
			KiroHistoryAssistantMessage["assistantResponseMessage"]["toolUses"]
		> = [];
		for (const block of blocks) {
			if (block.type !== "tool_call") continue;
			const toolUseId = asString(block.toolUseId);
			const name = asString(block.name);
			if (!toolUseId || !name) continue;
			toolUses.push({
				toolUseId,
				name,
				input: isRecord(block.input) ? block.input : {},
			});
		}

		const reasoningContent = reasoningContentOf(blocks);
		history.push({
			assistantResponseMessage: {
				content: texts.join("\n"),
				...(reasoningContent ? { reasoning_content: reasoningContent } : {}),
				...(toolUses.length > 0 ? { toolUses } : {}),
			},
		});
	}

	commitPendingUser(history, pending, modelId);

	// A trailing user turn would sit directly before the caller's current message, which
	// breaks alternation. `buildKiroHistory` pairs it the same way.
	const last = history[history.length - 1];
	if (last && "userInputMessage" in last) {
		history.push({ assistantResponseMessage: { content: "OK" } });
	}

	return { history };
}

/** The current turn, as the canonical contract delivers it. */
export interface CanonicalCurrentTurn {
	text?: unknown;
	images?: unknown;
	toolResults?: unknown;
}

export interface ToKiroChatRequestInput {
	conversationId: string;
	modelId: string;
	history?: readonly unknown[];
	tools?: readonly unknown[];
	current?: CanonicalCurrentTurn;
	/** Working directory reported in `envState`. */
	cwd?: string;
	/** Locale for the synthetic system-prompt acknowledgement. */
	locale?: string;
	/**
	 * Override the assistant acknowledgement that follows an injected system prompt.
	 *
	 * Defaults to the system-prompt ack. The generate path passes its own text so a
	 * title-generation turn reads the same as it does in core.
	 */
	ack?: string;
}

export interface KiroRequestParts {
	history: KiroHistoryMessage[];
	tools: KiroTool[];
	toolResults: KiroToolResult[];
	images: KiroImage[];
	content: string;
}

/**
 * Convert a canonical chat request into the pieces `buildKiroRequest` expects.
 *
 * Split out from {@link toKiroChatRequest} so a caller can assert on the individual
 * conversions, and so the parity test can compare these parts against what the in-core
 * `KiroProvider` produces for the same conversation.
 */
export function toKiroRequestParts(input: ToKiroChatRequestInput): KiroRequestParts {
	const { history } = toKiroHistory(input.history, input.modelId, input.locale, input.ack);
	const tools = toKiroTools(input.tools);

	const current = input.current ?? {};
	const toolResults: KiroToolResult[] = [];
	const images: KiroImage[] = [];

	// User-supplied images come first, matching `buildKiroRequest`'s ordering, so a
	// conversation replays identically on both paths.
	if (Array.isArray(current.images)) {
		for (const entry of current.images) {
			if (!isRecord(entry)) continue;
			const image = toKiroImage(entry as CanonicalBlock);
			if (image) images.push(image);
		}
	}

	if (Array.isArray(current.toolResults)) {
		for (const entry of current.toolResults) {
			if (!isRecord(entry) || typeof entry.type !== "string") continue;
			const converted = toKiroToolResult(entry as CanonicalBlock);
			if (!converted) continue;
			toolResults.push(converted.result);
			// Unlike history, the current turn *can* carry images: they move onto
			// `currentMessage.images`, which is how a `Read` tool image reaches the model.
			for (const image of converted.images) images.push(image);
		}
	}

	return {
		history,
		tools,
		toolResults,
		images,
		content: typeof current.text === "string" ? current.text : "",
	};
}

/**
 * Build a complete, normalized Kiro chat request from a canonical chat request.
 *
 * This is the entry point an out-of-process provider should call for `provider.chat`. The
 * result is ready to send: `normalizeKiroChatRequest` has already run, so alternation is
 * enforced, orphan tool calls and results are gone, and an empty current turn carries a
 * continuation prompt.
 *
 * `cwd` is reported in `envState` for telemetry only. The canonical contract carries no
 * working directory, so a plugin can only report its own — the model learns the real one
 * from the system prompt instead. Nothing should branch on this value.
 */
export function toKiroChatRequest(
	input: ToKiroChatRequestInput,
	runtime?: NormalizeRuntime,
): KiroChatRequest {
	const parts = toKiroRequestParts(input);
	const request = buildKiroRequest({
		conversationId: input.conversationId,
		content: parts.content,
		model: input.modelId,
		cwd: input.cwd ?? process.cwd(),
		history: parts.history,
		tools: parts.tools,
		toolResults: parts.toolResults,
		// `buildKiroRequest` expects `{format, base64}` and rebuilds the Kiro shape itself,
		// so the already-converted images are unwrapped here rather than converted twice.
		images: parts.images.map((image) => ({
			format: image.format,
			base64: image.source.bytes,
		})),
	});
	return normalizeKiroChatRequest(request, runtime ?? {});
}

/** The `provider.generate` request shape, as the canonical contract delivers it. */
export type CanonicalGenerateRequest =
	| { mode: "prompt"; text?: unknown; systemInstruction?: unknown }
	| { mode: "history"; systemInstruction?: unknown; content?: unknown; locale?: unknown };

export interface ToKiroGenerateRequestInput {
	conversationId: string;
	modelId: string;
	request: unknown;
	cwd?: string;
	/**
	 * Prefix prepended to the user content in `history` mode.
	 *
	 * The in-core path uses a localized "reply with only a title" reminder here. Supplied by
	 * the caller so this module stays free of host i18n.
	 */
	reminder?: string;
	/** Assistant acknowledgement for the system instruction in `history` mode. */
	ack?: string;
}

/**
 * Build a Kiro chat request for `provider.generate`.
 *
 * Generate is a different request shape from chat — `{mode, text, systemInstruction}` rather
 * than `{history, current, tools}` — so routing it through the chat converter produces an
 * empty prompt. It gets its own conversion for that reason.
 *
 * Both modes carry no tools: a generate operation that emitted a tool call would be a
 * protocol violation the host rejects outright.
 */
export function toKiroGenerateRequest(
	input: ToKiroGenerateRequestInput,
	runtime?: NormalizeRuntime,
): KiroChatRequest {
	const raw = isRecord(input.request) ? input.request : {};
	const mode = raw.mode === "history" ? "history" : "prompt";
	const systemInstruction = asString(raw.systemInstruction);

	// In history mode the instruction is a separate turn acknowledged by the assistant, so
	// the model treats it as a standing directive rather than part of the content.
	const content =
		mode === "history"
			? [input.reminder, asString(raw.content)].filter(Boolean).join("\n\n")
			: (asString(raw.text) ?? "");

	const history: CanonicalMessage[] = systemInstruction
		? [{ role: "system", content: [{ type: "text", text: systemInstruction }] }]
		: [];

	const locale = mode === "history" ? asString(raw.locale) : undefined;

	return toKiroChatRequest(
		{
			conversationId: input.conversationId,
			modelId: input.modelId,
			history,
			tools: [],
			current: { text: content },
			...(input.cwd ? { cwd: input.cwd } : {}),
			...(locale ? { locale } : {}),
			...(input.ack ? { ack: input.ack } : {}),
		},
		runtime,
	);
}
