import { generateId } from "../id";
import type {
	KiroChatRequest,
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroImage,
	KiroReasoningContent,
	KiroToolResult,
	StreamEvent,
} from "../kiro-adapter";
import { getImagePath, imageToBase64 } from "../uploads";
import { parseGatewaySSEEvent } from "./gateway-events";
import type { ChatParams, DbMessage, ParsedStreamEvent } from "./provider";
import { signatureSourcesCompatible } from "./reasoning-source";
import { resolveModel } from "./resolve-model";
import {
	appendSideCarsForApi,
	outputToText,
	sideCarsForToolResult,
	sideCarsForUserMessage,
} from "./sidecar";

/**
 * Shared Kiro protocol helpers used by KiroProvider and the NUG Kiro channel.
 */

/** A reasoning block as produced by the agent loop / stored in contentJson. */
export interface KiroReasoningBlockInput {
	text: string;
	providerMetadata?: import("./types").ReasoningProviderMetadata;
	outputIndex?: number;
}

/** A redacted (encrypted) thinking block. */
export interface KiroRedactedThinkingInput {
	data: string;
	outputIndex?: number;
	/** Upstream identity that produced this encrypted block (see reasoning-source.ts). */
	signatureSource?: string;
}

export interface KiroStreamParseOptions {
	/**
	 * Some Kiro agent responses leak XML-formatted tool calls into
	 * assistantResponseEvent.content instead of the native toolUse/toolUses fields.
	 * Enable this only for agent chats with tools; plain generation should keep text verbatim.
	 */
	parseTextToolCalls?: boolean;
}

/**
 * Merge streamed reasoning / redacted-thinking blocks into the single
 * `reasoningContent` object that Kiro expects on an assistant history message.
 *
 * Kiro's `ReasoningContentForHistory` is a single object (not an array), so when
 * a turn produced multiple reasoning blocks we concatenate their text and keep
 * the signature of the last signed block (the signature covers the full thinking
 * for that turn). A redacted block, when present, is carried alongside.
 *
 * `currentSource` identifies the upstream that will receive this history. A
 * stored signature (or encrypted redacted block) is only echoed back when it
 * was minted by that same upstream — replaying it to a different server (e.g. a
 * different NUG channel) fails signature verification. Mismatched or unknown
 * (legacy) sources drop the signature/redacted payload but keep the thinking
 * text.
 *
 * Returns `undefined` when there is no reasoning to echo back.
 */
export function buildKiroReasoningContent(
	reasoningBlocks?: KiroReasoningBlockInput[],
	redactedThinkingBlocks?: KiroRedactedThinkingInput[],
	currentSource?: string,
): KiroReasoningContent | undefined {
	const texts: string[] = [];
	let signature: string | undefined;
	for (const rb of reasoningBlocks ?? []) {
		if (rb.text) texts.push(rb.text);
		const sig = rb.providerMetadata?.anthropic?.signature;
		// Only carry the signature when it belongs to the current upstream.
		if (sig && signatureSourcesCompatible(rb.providerMetadata?.signatureSource, currentSource)) {
			signature = sig;
		}
	}

	const redacted = redactedThinkingBlocks?.find(
		(rb) => rb.data && signatureSourcesCompatible(rb.signatureSource, currentSource),
	)?.data;

	const hasText = texts.length > 0;
	if (!hasText && !redacted) return undefined;

	const reasoningContent: KiroReasoningContent = {};
	if (hasText) {
		reasoningContent.reasoningText = {
			text: texts.join("\n"),
			...(signature ? { signature } : {}),
		};
	}
	if (redacted) {
		reasoningContent.redactedContent = redacted;
	}
	return reasoningContent;
}

export function buildKiroRequest(params: ChatParams): KiroChatRequest {
	const modelId = resolveModel(params.model);
	const history = params.history as KiroHistoryMessage[];
	const toolResults = params.toolResults as KiroToolResult[];

	// Collect all images for the current message:
	// 1. User-uploaded images from params.images
	// 2. Images extracted from tool results (Read tool image output)
	const allImages: KiroImage[] = [];

	if (params.images?.length) {
		for (const img of params.images) {
			allImages.push({
				format: img.format as "jpeg" | "png" | "gif" | "webp",
				source: { bytes: img.base64 },
			});
		}
	}

	// Extract images attached to tool results (Read tool image output).
	// Kiro protocol doesn't support images inside tool results, so we strip
	// the _images marker and merge them into the currentMessage images.
	for (const tr of toolResults) {
		const tagged = tr as KiroToolResult & { _images?: KiroImage[]; _imageLabel?: string };
		if (tagged._images?.length) {
			allImages.push(...tagged._images);
			// Clean up internal markers before sending to API
			delete tagged._images;
			delete tagged._imageLabel;
		}
	}

	return {
		conversationState: {
			conversationId: params.conversationId,
			chatTriggerType: "MANUAL",
			agentTaskType: "vibe",
			agentContinuationId: params.conversationId,
			history: history.length > 0 ? history : undefined,
			currentMessage: {
				userInputMessage: {
					content: params.content,
					modelId,
					origin: "AI_EDITOR",
					...(allImages.length > 0 ? { images: allImages } : {}),
					userInputMessageContext: {
						// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
						tools: params.tools as any[],
						...(toolResults.length > 0 ? { toolResults } : {}),
					},
				},
			},
			envState: {
				operatingSystem:
					process.platform === "win32"
						? "WINDOWS"
						: process.platform === "darwin"
							? "MAC"
							: "LINUX",
				currentWorkingDirectory: params.cwd,
			},
			shellState: {
				shellName: process.env.SHELL?.split("/").pop() ?? "bash",
			},
		},
	};
}

type KiroRequestMessage = KiroHistoryMessage;
type KiroRequestMessageWithMarker = {
	message: KiroRequestMessage;
	isCurrent: boolean;
};

function isKiroUserMessage(message: KiroRequestMessage): message is KiroHistoryUserMessage {
	return "userInputMessage" in message;
}

function isKiroAssistantMessage(
	message: KiroRequestMessage,
): message is KiroHistoryAssistantMessage {
	return "assistantResponseMessage" in message;
}

function cloneToolResult(result: KiroToolResult): KiroToolResult {
	return {
		...result,
		content: result.content.map((block) => ({ ...block })),
	};
}

function cloneUserMessage(message: KiroHistoryUserMessage): KiroHistoryUserMessage {
	const user = message.userInputMessage;
	const ctx = user.userInputMessageContext;
	return {
		userInputMessage: {
			...user,
			...(user.images ? { images: user.images.map((image) => ({ ...image })) } : {}),
			userInputMessageContext: ctx
				? {
						...ctx,
						...(ctx.tools ? { tools: ctx.tools.map((tool) => ({ ...tool })) } : {}),
						...(ctx.toolResults
							? { toolResults: ctx.toolResults.map((result) => cloneToolResult(result)) }
							: {}),
					}
				: {},
		},
	};
}

function cloneAssistantMessage(message: KiroHistoryAssistantMessage): KiroHistoryAssistantMessage {
	const assistant = message.assistantResponseMessage;
	return {
		assistantResponseMessage: {
			...assistant,
			...(assistant.toolUses
				? {
						toolUses: assistant.toolUses.map((toolUse) => ({
							...toolUse,
							input: { ...toolUse.input },
						})),
					}
				: {}),
		},
	};
}

function isEmptyAssistant(message: KiroHistoryAssistantMessage): boolean {
	const assistant = message.assistantResponseMessage;
	return !assistant.content?.trim() && (assistant.toolUses?.length ?? 0) === 0;
}

function hasUserPayload(message: KiroHistoryUserMessage): boolean {
	const user = message.userInputMessage;
	const ctx = user.userInputMessageContext;
	return Boolean(
		user.content.trim() ||
			(user.images?.length ?? 0) > 0 ||
			(ctx?.tools?.length ?? 0) > 0 ||
			(ctx?.toolResults?.length ?? 0) > 0,
	);
}

function createSyntheticUser(modelId: string): KiroHistoryUserMessage {
	return {
		userInputMessage: {
			content: "",
			modelId,
			origin: "AI_EDITOR",
			userInputMessageContext: {},
		},
	};
}

function createSyntheticAssistantAck(): KiroHistoryAssistantMessage {
	return { assistantResponseMessage: { content: "OK" } };
}

function mergeUserMessages(target: KiroHistoryUserMessage, source: KiroHistoryUserMessage): void {
	const targetUser = target.userInputMessage;
	const sourceUser = source.userInputMessage;
	const sourceContent = sourceUser.content ?? "";
	if (sourceContent) {
		targetUser.content = targetUser.content
			? `${targetUser.content}\n${sourceContent}`
			: sourceContent;
	}
	if (sourceUser.images?.length) {
		targetUser.images = [...(targetUser.images ?? []), ...sourceUser.images];
	}

	const targetCtx = targetUser.userInputMessageContext ?? {};
	const sourceCtx = sourceUser.userInputMessageContext ?? {};
	const mergedToolResults = [...(targetCtx.toolResults ?? []), ...(sourceCtx.toolResults ?? [])];
	targetUser.userInputMessageContext = {
		...targetCtx,
		...sourceCtx,
		...(sourceCtx.tools ? { tools: sourceCtx.tools } : {}),
		...(mergedToolResults.length > 0 ? { toolResults: mergedToolResults } : {}),
	};
}

function mergeAssistantMessages(
	target: KiroHistoryAssistantMessage,
	source: KiroHistoryAssistantMessage,
): void {
	const targetAssistant = target.assistantResponseMessage;
	const sourceAssistant = source.assistantResponseMessage;
	const sourceContent = sourceAssistant.content ?? "";
	if (sourceContent) {
		targetAssistant.content = targetAssistant.content
			? `${targetAssistant.content}\n${sourceContent}`
			: sourceContent;
	}
	if (sourceAssistant.toolUses?.length) {
		targetAssistant.toolUses = [...(targetAssistant.toolUses ?? []), ...sourceAssistant.toolUses];
	}
}

function cleanupUserContext(message: KiroHistoryUserMessage): void {
	const user = message.userInputMessage;
	const ctx = user.userInputMessageContext;
	if (!ctx) {
		user.userInputMessageContext = {};
		return;
	}
	if (ctx.toolResults?.length === 0) delete ctx.toolResults;
	user.userInputMessageContext = ctx;
}

function compactKiroMessages(
	messages: KiroRequestMessageWithMarker[],
	modelId: string,
): KiroRequestMessageWithMarker[] {
	const compacted: KiroRequestMessageWithMarker[] = [];
	for (const entry of messages) {
		const msg = entry.message;
		if (isKiroAssistantMessage(msg) && isEmptyAssistant(msg)) continue;
		if (isKiroUserMessage(msg) && !entry.isCurrent && !hasUserPayload(msg)) continue;

		const prev = compacted[compacted.length - 1];
		if (isKiroUserMessage(msg)) {
			cleanupUserContext(msg);
			if (prev && isKiroUserMessage(prev.message)) {
				if (entry.isCurrent) {
					compacted.push({ message: createSyntheticAssistantAck(), isCurrent: false });
					compacted.push(entry);
				} else {
					mergeUserMessages(prev.message, msg);
					prev.isCurrent = prev.isCurrent || entry.isCurrent;
				}
				continue;
			}
			compacted.push(entry);
			continue;
		}

		if (prev && isKiroAssistantMessage(prev.message)) {
			mergeAssistantMessages(prev.message, msg);
			continue;
		}
		if (!prev) {
			compacted.push({ message: createSyntheticUser(modelId), isCurrent: false });
		}
		compacted.push(entry);
	}
	return compacted;
}

function sanitizeKiroToolPairs(
	messages: KiroRequestMessageWithMarker[],
): KiroRequestMessageWithMarker[] {
	const consumedToolUseIds = new Set<string>();
	const keep = new Array(messages.length).fill(true) as boolean[];

	for (let i = 0; i < messages.length; i++) {
		const current = messages[i].message;
		if (!isKiroUserMessage(current)) continue;

		const user = current.userInputMessage;
		const ctx = user.userInputMessageContext ?? {};
		const incomingResults = ctx.toolResults ?? [];
		const prev = i > 0 ? messages[i - 1].message : undefined;
		const prevAssistant = prev && isKiroAssistantMessage(prev) ? prev : undefined;
		const expectedToolUses = prevAssistant?.assistantResponseMessage.toolUses ?? [];
		const expectedIds = new Set(
			expectedToolUses
				.map((toolUse) => toolUse.toolUseId)
				.filter((toolUseId) => !consumedToolUseIds.has(toolUseId)),
		);
		const keptResults: KiroToolResult[] = [];
		const matchedIds = new Set<string>();

		for (const result of incomingResults) {
			if (!expectedIds.has(result.toolUseId) || matchedIds.has(result.toolUseId)) continue;
			keptResults.push(result);
			matchedIds.add(result.toolUseId);
			consumedToolUseIds.add(result.toolUseId);
		}

		if (keptResults.length > 0) {
			ctx.toolResults = keptResults;
		} else {
			delete ctx.toolResults;
		}
		user.userInputMessageContext = ctx;

		if (prevAssistant && expectedToolUses.length > 0) {
			const filteredToolUses = expectedToolUses.filter((toolUse) =>
				matchedIds.has(toolUse.toolUseId),
			);
			if (filteredToolUses.length > 0) {
				prevAssistant.assistantResponseMessage.toolUses = filteredToolUses;
			} else {
				delete prevAssistant.assistantResponseMessage.toolUses;
			}
			if (isEmptyAssistant(prevAssistant)) keep[i - 1] = false;
		}

		if (!messages[i].isCurrent && !hasUserPayload(current)) keep[i] = false;
	}

	return messages.filter((_, index) => keep[index]);
}

/**
 * Normalize the Kiro native request immediately before it leaves Kiro/NUG providers.
 * This is intentionally Kiro-protocol scoped: other providers keep their own history logic.
 *
 * The Kiro API is strict about user/assistant alternation and requires every toolResult
 * to immediately answer the previous assistant toolUse exactly once.  Compaction,
 * dynamic pruning, output-limit recovery, and in-memory multi-turn replay can leave
 * stale toolResults or empty assistant placeholders in the assembled request; sanitize
 * those final-wire-shape issues here without touching persisted messages.
 */
export function normalizeKiroChatRequest(request: KiroChatRequest): KiroChatRequest {
	const cs = request.conversationState;
	const currentMessage = cloneUserMessage(cs.currentMessage);
	const modelId = currentMessage.userInputMessage.modelId;
	const initialMessages: KiroRequestMessageWithMarker[] = [
		...(cs.history ?? []).map((message) => ({
			message: isKiroUserMessage(message)
				? cloneUserMessage(message)
				: cloneAssistantMessage(message),
			isCurrent: false,
		})),
		{ message: currentMessage, isCurrent: true },
	];

	let normalized = compactKiroMessages(initialMessages, modelId);
	normalized = sanitizeKiroToolPairs(normalized);
	normalized = compactKiroMessages(normalized, modelId);
	normalized = sanitizeKiroToolPairs(normalized);
	normalized = compactKiroMessages(normalized, modelId);

	const currentIndex = normalized.findIndex((entry) => entry.isCurrent);
	const effectiveCurrentIndex = currentIndex >= 0 ? currentIndex : normalized.length - 1;
	const beforeCurrent = normalized.slice(0, effectiveCurrentIndex);
	const current = normalized[effectiveCurrentIndex]?.message;

	if (!current || !isKiroUserMessage(current)) {
		cs.history = beforeCurrent.map((entry) => entry.message);
		cs.currentMessage = { userInputMessage: currentMessage.userInputMessage };
		return request;
	}

	cs.history = beforeCurrent.length > 0 ? beforeCurrent.map((entry) => entry.message) : undefined;
	cs.currentMessage = { userInputMessage: current.userInputMessage };
	return request;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null;
}

function numberFrom(value: unknown): number | undefined {
	if (typeof value === "number" && Number.isFinite(value)) return value;
	if (typeof value === "string" && value.trim() !== "") {
		const parsed = Number(value);
		if (Number.isFinite(parsed)) return parsed;
	}
	return undefined;
}

function firstNumber(source: Record<string, unknown>, keys: string[]): number | undefined {
	for (const key of keys) {
		const value = numberFrom(source[key]);
		if (value != null) return value;
	}
	return undefined;
}

function parseUsagePayload(data: Record<string, unknown>): ParsedStreamEvent["usage"] | undefined {
	const source = isRecord(data.usage) ? data.usage : data;
	const inputTokens = firstNumber(source, [
		"inputTokens",
		"input_tokens",
		"promptTokens",
		"prompt_tokens",
	]);
	const completionTokens = firstNumber(source, [
		"completionTokens",
		"completion_tokens",
		"outputTokens",
		"output_tokens",
	]);
	const cachedInputTokens = firstNumber(source, [
		"cachedInputTokens",
		"cached_input_tokens",
		"cacheReadInputTokens",
		"cache_read_input_tokens",
	]);
	const cacheCreationInputTokens = firstNumber(source, [
		"cacheCreationInputTokens",
		"cache_creation_input_tokens",
		"cacheWriteInputTokens",
		"cache_write_input_tokens",
	]);
	const cacheCreation5mTokens = firstNumber(source, [
		"cacheCreation5mTokens",
		"cache_creation_5m_tokens",
		"ephemeral5mInputTokens",
		"ephemeral_5m_input_tokens",
	]);
	const cacheCreation1hTokens = firstNumber(source, [
		"cacheCreation1hTokens",
		"cache_creation_1h_tokens",
		"ephemeral1hInputTokens",
		"ephemeral_1h_input_tokens",
	]);
	const reasoningTokens = firstNumber(source, ["reasoningTokens", "reasoning_tokens"]);

	const hasAny = [
		inputTokens,
		completionTokens,
		cachedInputTokens,
		cacheCreationInputTokens,
		cacheCreation5mTokens,
		cacheCreation1hTokens,
		reasoningTokens,
	].some((value) => value != null);
	if (!hasAny) return undefined;

	const derivedCacheCreation =
		cacheCreationInputTokens ?? (cacheCreation5mTokens ?? 0) + (cacheCreation1hTokens ?? 0);
	const promptTokens =
		firstNumber(source, ["promptTokens", "prompt_tokens"]) ??
		(inputTokens ?? 0) + (cachedInputTokens ?? 0) + derivedCacheCreation;

	return {
		promptTokens,
		inputTokens,
		completionTokens,
		reasoningTokens,
		cachedInputTokens,
		cacheCreationInputTokens: derivedCacheCreation,
		cacheCreation5mTokens,
		cacheCreation1hTokens,
	};
}

type ParsedKiroTextToolUse = NonNullable<ParsedStreamEvent["toolUses"]>[number];

function readXmlAttribute(attrs: string, name: string): string | undefined {
	const match = attrs.match(new RegExp(`\\b${name}\\s*=\\s*(["'])([\\s\\S]*?)\\1`, "i"));
	return match ? decodeXmlEntities(match[2]) : undefined;
}

function decodeXmlEntities(value: string): string {
	return value.replace(/&(#x?[0-9a-f]+|[a-z]+);/gi, (entity, body: string) => {
		const lower = body.toLowerCase();
		if (lower === "amp") return "&";
		if (lower === "lt") return "<";
		if (lower === "gt") return ">";
		if (lower === "quot") return '"';
		if (lower === "apos") return "'";
		if (lower.startsWith("#x")) {
			const code = Number.parseInt(lower.slice(2), 16);
			return Number.isInteger(code) && code >= 0 && code <= 0x10ffff
				? String.fromCodePoint(code)
				: entity;
		}
		if (lower.startsWith("#")) {
			const code = Number.parseInt(lower.slice(1), 10);
			return Number.isInteger(code) && code >= 0 && code <= 0x10ffff
				? String.fromCodePoint(code)
				: entity;
		}
		return entity;
	});
}

const MAX_MALFORMED_INVOKE_MARKER_LENGTH = 10;
const MALFORMED_INVOKE_MARKERS = new Set(["call", "count"]);
const XML_BOOLEAN_PARAMETER_NAMES = new Set([
	"replace_all",
	"run_in_background",
	"strict_serial",
	"compress",
	"preview",
	"await",
	"doInterrupt",
	"multiSelect",
	// Reflection/goal gate tools (DangerConfirm/DangerCancel, ExitPlanConfirm/Revise,
	// GoalCompleteConfirm/Revise, UpdateGoal, GetGoals) use a boolean `confirm` parameter,
	// usually typed as z.literal(true). Without coercion, leaked XML `<parameter
	// name="confirm">true</parameter>` arrives as the string "true", fails schema
	// validation, and the gate silently falls back to cancelling the operation.
	"confirm",
]);
const XML_NUMBER_PARAMETER_NAMES = new Set([
	"timeout",
	"limit",
	"offset",
	"head_limit",
	"maxChars",
	"max_length",
	"pageno",
	"duration",
	"count",
]);
const XML_JSON_PARAMETER_NAMES = new Set([
	"ids",
	"names",
	"paths",
	"todos",
	"questions",
	"answers",
	"annotations",
	"metadata",
	"headers",
	"options",
	"tool_uses",
]);

function stripMalformedInvokeMarker(text: string): string {
	// Kiro can prefix leaked XML tool calls with a short standalone marker line
	// (for example "call" or "count"). The observed boundary is:
	//   normal text + "\n\n" + short marker + "\n" + "<invoke"
	// Keep the blank-line separator and remove only the short marker line.
	return text.replace(
		/(\r?\n[ \t]*\r?\n[ \t]*)([^\r\n]*?)(?:\r?\n[ \t]*)*$/u,
		(match, blankSeparator: string, marker: string) => {
			const trimmed = marker.trim();
			const normalized = trimmed.toLowerCase();
			const isKnownMarker = MALFORMED_INVOKE_MARKERS.has(normalized);
			if (
				trimmed.length === 0 ||
				(trimmed.length > MAX_MALFORMED_INVOKE_MARKER_LENGTH && !isKnownMarker)
			) {
				return match;
			}
			return blankSeparator;
		},
	);
}

function coerceKiroXmlParameter(paramName: string, value: string): unknown {
	const trimmed = value.trim();
	if (XML_BOOLEAN_PARAMETER_NAMES.has(paramName)) {
		if (/^(true|false)$/i.test(trimmed)) return trimmed.toLowerCase() === "true";
	}
	if (XML_NUMBER_PARAMETER_NAMES.has(paramName)) {
		if (/^-?(?:\d+|\d*\.\d+)$/.test(trimmed)) {
			const parsed = Number(trimmed);
			if (Number.isFinite(parsed)) return parsed;
		}
	}
	if (
		XML_JSON_PARAMETER_NAMES.has(paramName) &&
		(trimmed.startsWith("[") || trimmed.startsWith("{"))
	) {
		try {
			return JSON.parse(trimmed);
		} catch {
			// Keep the original string when the model emitted JSON-like prose.
		}
	}
	return value;
}

/** Build a tool use from a leaked `<invoke>` open-tag attribute string and body. */
function buildInvokeToolUse(attrs: string, body: string): ParsedKiroTextToolUse | null {
	const name = readXmlAttribute(attrs, "name");
	if (!name) return null;

	const input: Record<string, unknown> = {};
	const parameterRe = /<parameter\b([^>]*)>([\s\S]*?)<\/parameter>/gi;
	for (const parameterMatch of body.matchAll(parameterRe)) {
		const paramName = readXmlAttribute(parameterMatch[1] ?? "", "name");
		if (!paramName) continue;
		input[paramName] = coerceKiroXmlParameter(
			paramName,
			decodeXmlEntities(parameterMatch[2] ?? ""),
		);
	}

	return { toolUseId: `kiro_xml_${generateId(12)}`, name, input };
}

export function parseKiroTextToolCalls(text: string): {
	text: string;
	toolUses: ParsedKiroTextToolUse[];
} {
	const invokeRe = /<invoke\b([^>]*)>([\s\S]*?)<\/invoke>/gi;
	let cursor = 0;
	let rewrittenText = "";
	const toolUses: ParsedKiroTextToolUse[] = [];

	for (const invokeMatch of text.matchAll(invokeRe)) {
		const matchIndex = invokeMatch.index ?? 0;
		const rawInvoke = invokeMatch[0];
		const toolUse = buildInvokeToolUse(invokeMatch[1] ?? "", invokeMatch[2] ?? "");
		if (!toolUse) continue;

		rewrittenText += stripMalformedInvokeMarker(text.slice(cursor, matchIndex));
		toolUses.push(toolUse);
		cursor = matchIndex + rawInvoke.length;
	}

	if (toolUses.length === 0) return { text, toolUses };
	rewrittenText += text.slice(cursor);
	return { text: rewrittenText, toolUses };
}

const INVOKE_OPEN_RE = /<invoke\b/i;
const INVOKE_BLOCK_FROM_START_RE = /^<invoke\b([^>]*)>([\s\S]*?)<\/invoke>/i;

/**
 * Stateful parser for leaked XML tool calls that may be split across multiple
 * streaming text deltas.
 *
 * Kiro/NUG stream `assistantResponseEvent.content` as incremental chunks, so a
 * single `<invoke>...</invoke>` block frequently spans several deltas. The stateless
 * {@link parseKiroTextToolCalls} only matches blocks fully contained in one chunk, so
 * split blocks would leak to the UI as raw text and never execute. This parser buffers
 * text across `push()` calls, only emitting text that is provably outside any pending
 * `<invoke>` block, and surfaces a tool use as soon as its closing tag arrives.
 */
export class KiroTextToolCallStreamParser {
	private buffer = "";

	/** Feed a streamed text delta. Returns text safe to emit plus any completed tool uses. */
	push(delta: string): { text: string; toolUses: ParsedKiroTextToolUse[] } {
		this.buffer += delta;
		return this.drain(false);
	}

	/** Flush any buffered text/tool calls at end of stream. */
	flush(): { text: string; toolUses: ParsedKiroTextToolUse[] } {
		return this.drain(true);
	}

	private drain(final: boolean): { text: string; toolUses: ParsedKiroTextToolUse[] } {
		let emittedText = "";
		const toolUses: ParsedKiroTextToolUse[] = [];

		while (this.buffer.length > 0) {
			const open = INVOKE_OPEN_RE.exec(this.buffer);
			if (!open) {
				// No `<invoke` start in the buffer. Flush everything except a tail that
				// could still become the start of a leaked tool call (or its marker).
				const keep = final ? this.buffer.length : this.retentionStart(this.buffer);
				emittedText += this.buffer.slice(0, keep);
				this.buffer = this.buffer.slice(keep);
				break;
			}

			const openIdx = open.index;
			const block = INVOKE_BLOCK_FROM_START_RE.exec(this.buffer.slice(openIdx));
			if (block) {
				emittedText += stripMalformedInvokeMarker(this.buffer.slice(0, openIdx));
				const toolUse = buildInvokeToolUse(block[1] ?? "", block[2] ?? "");
				if (toolUse) {
					toolUses.push(toolUse);
				} else {
					// Malformed (e.g. missing name) — keep the raw block as text, like the
					// stateless parser, and advance past it.
					emittedText += block[0];
				}
				this.buffer = this.buffer.slice(openIdx + block[0].length);
				continue;
			}

			// An `<invoke` start without a closing tag yet.
			if (final) {
				// Stream ended mid-block — emit the remainder verbatim rather than dropping it.
				emittedText += this.buffer;
				this.buffer = "";
				break;
			}
			emittedText += stripMalformedInvokeMarker(this.buffer.slice(0, openIdx));
			this.buffer = this.buffer.slice(openIdx);
			break;
		}

		return { text: emittedText, toolUses };
	}

	/**
	 * Compute the index from which the buffer tail must be retained because it could
	 * still grow into a leaked `<invoke>` block (a partial `<invoke` prefix) or its
	 * malformed marker line. Everything before this index is safe to emit.
	 */
	private retentionStart(buf: string): number {
		let start = buf.length;

		// Partial "<invoke" prefix at the very end (e.g. the chunk ended on "<inv").
		const target = "<invoke";
		const max = Math.min(target.length - 1, buf.length);
		for (let len = max; len >= 1; len--) {
			if (buf.slice(buf.length - len).toLowerCase() === target.slice(0, len).toLowerCase()) {
				start = buf.length - len;
				break;
			}
		}

		// A short marker line after a trailing blank-line boundary. Kiro sometimes emits
		// a stray "call"/"count" marker between text and a leaked `<invoke>`; if it lands
		// in its own delta we must hold it back so it can be stripped once the block opens.
		const markerTail = /(\r?\n[ \t]*\r?\n[ \t]*)([^\r\n]*)((?:\r?\n[ \t]*)*)$/.exec(buf);
		if (markerTail && markerTail.index < start) {
			const marker = markerTail[2].trim();
			const isKnown = MALFORMED_INVOKE_MARKERS.has(marker.toLowerCase());
			if (marker.length === 0 || marker.length <= MAX_MALFORMED_INVOKE_MARKER_LENGTH || isKnown) {
				start = markerTail.index;
			}
		}

		return start;
	}
}

/**
 * Stateful accumulator that moves leaked XML tool calls out of streamed assistant text.
 *
 * Wraps {@link KiroTextToolCallStreamParser} at the {@link ParsedStreamEvent} level so the
 * Kiro/NUG streaming paths can reuse identical logic. Callers must parse each raw event
 * with `parseTextToolCalls: false` (so the per-event regex does not run) and route the result
 * through {@link process}; after the stream ends they must emit {@link finalize}.
 */
export class KiroStreamToolCallAccumulator {
	private readonly parser: KiroTextToolCallStreamParser | null;

	constructor(options: KiroStreamParseOptions = {}) {
		this.parser = options.parseTextToolCalls ? new KiroTextToolCallStreamParser() : null;
	}

	/** Adjust a parsed event in place, lifting any completed leaked tool calls out of text. */
	process(parsed: ParsedStreamEvent): ParsedStreamEvent {
		if (!this.parser || parsed.text == null) return parsed;
		const { text, toolUses } = this.parser.push(parsed.text);
		parsed.text = text;
		appendDedupedToolUses(parsed, toolUses);
		return parsed;
	}

	/** Return a trailing event for any buffered text/tool calls, or null if nothing remains. */
	finalize(): ParsedStreamEvent | null {
		if (!this.parser) return null;
		const { text, toolUses } = this.parser.flush();
		if (!text && toolUses.length === 0) return null;
		const parsed: ParsedStreamEvent = {};
		if (text) parsed.text = text;
		appendDedupedToolUses(parsed, toolUses);
		return parsed;
	}
}

function stableJson(value: unknown): string {
	if (Array.isArray(value)) return `[${value.map((item) => stableJson(item)).join(",")}]`;
	if (value && typeof value === "object") {
		const entries = Object.entries(value as Record<string, unknown>).sort(([a], [b]) =>
			a.localeCompare(b),
		);
		return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${stableJson(item)}`).join(",")}}`;
	}
	return JSON.stringify(value);
}

function isDuplicateToolUse(
	candidate: ParsedKiroTextToolUse,
	existing: ParsedKiroTextToolUse,
): boolean {
	return (
		candidate.name === existing.name && stableJson(candidate.input) === stableJson(existing.input)
	);
}

function appendDedupedToolUses(
	result: ParsedStreamEvent,
	toolUses: ParsedKiroTextToolUse[] | undefined,
): void {
	if (!toolUses?.length) return;
	const next = [...(result.toolUses ?? [])];
	for (const toolUse of toolUses) {
		if (!next.some((item) => isDuplicateToolUse(toolUse, item))) next.push(toolUse);
	}
	result.toolUses = next;
}

export function parseKiroStreamEvent(
	evt: StreamEvent,
	options: KiroStreamParseOptions = {},
): ParsedStreamEvent {
	const gatewayEvent = parseGatewaySSEEvent(evt.eventType, evt.data);
	if (gatewayEvent) return gatewayEvent;

	const result: ParsedStreamEvent = {};
	const usage = parseUsagePayload(evt.data);
	if (usage) result.usage = usage;

	if (evt.eventType === "assistantResponseEvent") {
		const toolUse = evt.data.toolUse as
			| { toolUseId: string; name: string; input: Record<string, unknown> }
			| undefined;
		const toolUses = evt.data.toolUses as
			| Array<{ toolUseId: string; name: string; input: Record<string, unknown> }>
			| undefined;
		let parsedTextToolUses: ParsedKiroTextToolUse[] | undefined;

		if (evt.data.content != null) {
			const contentText = String(evt.data.content);
			if (options.parseTextToolCalls) {
				const parsedContent = parseKiroTextToolCalls(contentText);
				result.text = parsedContent.text;
				parsedTextToolUses = parsedContent.toolUses;
			} else {
				result.text = contentText;
			}
		}
		if (evt.data.messageId) {
			result.messageId = String(evt.data.messageId);
		}
		if (evt.data.conversationId) {
			result.conversationId = String(evt.data.conversationId);
		}

		// Detect output truncation via stopReason (if the API provides it)
		const stopReason = evt.data.stopReason ?? evt.data.stop_reason;
		if (
			stopReason === "max_tokens" ||
			stopReason === "max_output_tokens" ||
			stopReason === "length"
		) {
			result.invalidState = {
				reason: "max_tokens",
				message: "Response truncated: model reached maximum token limit.",
			};
		}

		// Non-streaming tool use (single)
		if (toolUse?.toolUseId) {
			appendDedupedToolUses(result, [
				{
					toolUseId: toolUse.toolUseId,
					name: toolUse.name,
					input: toolUse.input ?? {},
				},
			]);
		}

		// Non-streaming tool uses (array)
		if (Array.isArray(toolUses) && toolUses.length > 0) {
			appendDedupedToolUses(
				result,
				toolUses.map((tu) => ({
					toolUseId: tu.toolUseId,
					name: tu.name,
					input: tu.input ?? {},
				})),
			);
		}
		appendDedupedToolUses(result, parsedTextToolUses);
	}

	// Streaming tool use chunks
	if (evt.eventType === "toolUseEvent") {
		const id = evt.data.toolUseId as string | undefined;
		if (id) {
			result.toolUseChunk = {
				toolUseId: id,
				name: evt.data.name as string | undefined,
				input: typeof evt.data.input === "string" ? evt.data.input : undefined,
				stop: evt.data.stop === true,
			};
		}
	}

	if (evt.eventType === "reasoningContentEvent") {
		// Kiro reasoning chunks carry the visible thinking text plus, on the
		// Anthropic-backed channels, a `signature` and/or a base64
		// `redactedContent` block. All three must survive so the assistant turn
		// can be echoed back verbatim on the next request (see buildKiroHistory
		// and the providers' pushAssistantTurn).
		const content = evt.data.content ?? evt.data.text;
		if (content != null) {
			result.reasoning = String(content);
		}
		const signature = evt.data.signature ?? evt.data.reasoningSignature;
		if (signature != null) {
			result.reasoningMetadata = {
				...result.reasoningMetadata,
				anthropic: {
					...result.reasoningMetadata?.anthropic,
					signature: String(signature),
				},
			};
		}
		const redacted = evt.data.redactedContent ?? evt.data.redacted_content;
		if (redacted != null) {
			result.redactedThinking = { data: String(redacted) };
		}
	}

	if (evt.eventType === "contextUsageEvent") {
		const pct = evt.data.contextUsagePercentage ?? evt.data.context_usage_percentage;
		if (pct != null) {
			result.contextUsagePercentage = Number(pct);
		}
	}

	if (evt.eventType === "meteringEvent") {
		const usage = evt.data.usage;
		if (usage != null) {
			result.metering = {
				unit: String(evt.data.unit ?? "credit"),
				unitPlural: String(evt.data.unitPlural ?? "credits"),
				usage: Number(usage),
			};
		}
	}

	if (evt.eventType === "credentialIdEvent") {
		const id = evt.data.credentialId;
		if (id != null) {
			result.credentialId = String(id);
		}
	}

	if (evt.eventType === "invalidStateEvent") {
		result.invalidState = {
			reason: String(evt.data.reason ?? "unknown"),
			message: String(evt.data.message ?? evt.data.content ?? "Invalid state"),
		};
	}

	// Proxy-layer error event (harmless for kiro-provider since kiro won't send it).
	if (evt.eventType === "error") {
		result.invalidState = {
			reason: "gateway_error",
			message: String(evt.data.message ?? "Gateway proxy error"),
		};
	}

	return result;
}

/**
 * Parse a raw HTTP SSE response body into a stream of ParsedStreamEvents.
 * Used by Kiro-protocol providers to avoid duplicating SSE line-parsing logic across
 * chat(), generateWithMeta(), and generateWithHistory().
 */
export async function* parseSSEStream(
	body: ReadableStream<Uint8Array>,
	options: KiroStreamParseOptions = {},
): AsyncGenerator<ParsedStreamEvent> {
	const reader = body.getReader();
	const decoder = new TextDecoder();
	// Leaked XML tool calls can be split across streamed text deltas, so buffer the
	// text at the stream level and lift completed tool calls out of it. The per-event
	// regex must stay off here (parseTextToolCalls: false) to avoid double parsing.
	const toolCallAccumulator = new KiroStreamToolCallAccumulator(options);
	let buf = "";
	let currentEventType = "";

	try {
		while (true) {
			const { done, value } = await reader.read();
			if (done) break;
			buf += decoder.decode(value, { stream: true });

			const lines = buf.split("\n");
			buf = lines.pop() ?? "";

			for (const line of lines) {
				if (line.startsWith("event:")) {
					currentEventType = line.slice(6).trim();
				} else if (line.startsWith("data:")) {
					const dataStr = line.slice(5).trimStart();
					if (!dataStr || dataStr === "[DONE]") continue;
					try {
						const data = JSON.parse(dataStr);
						const embeddedEventType = data && typeof data.type === "string" ? data.type : undefined;
						const evt: StreamEvent = {
							eventType: currentEventType || embeddedEventType || "assistantResponseEvent",
							data,
						};
						yield toolCallAccumulator.process(
							parseKiroStreamEvent(evt, { ...options, parseTextToolCalls: false }),
						);
					} catch {
						// Malformed JSON — skip
					}
				} else if (line.trim() === "") {
					currentEventType = "";
				}
			}
		}
		const trailing = toolCallAccumulator.finalize();
		if (trailing) yield trailing;
	} finally {
		reader.releaseLock();
	}
}

function dbMessageText(msg: DbMessage): string {
	const content = Array.isArray(msg.contentJson) ? msg.contentJson : [];
	const textParts = content
		.filter((b): b is { type: string; text: string } => {
			if (!b || typeof b !== "object") return false;
			const block = b as { type?: unknown; text?: unknown };
			return block.type === "text" && typeof block.text === "string";
		})
		.map((b) => b.text);
	return textParts.join("\n") || msg.contentText || "";
}

export async function buildKiroHistory(
	dbMessages: DbMessage[],
	model: string,
	narratorId?: string,
	currentSource?: string,
): Promise<{
	history: KiroHistoryMessage[];
	trailingToolResults: KiroToolResult[];
	trailingUserText?: string;
}> {
	const modelId = resolveModel(model);

	// Filter model-visible messages: user, assistant, sys (system context)
	// Exclude: system (legacy, kept for backward compatibility), disp (UI-only display messages)
	const topLevel = dbMessages.filter(
		(m) => !m.parentToolUseId && (m.role === "user" || m.role === "assistant" || m.role === "sys"),
	);

	// A trailing sys message is fresh model-visible context (for example goal continuation).
	// Kiro history requires user→assistant pairs, so keeping it in history would make us
	// append a synthetic assistant "OK" below and the real currentMessage would be empty.
	// Move it into trailingUserText so the caller sends it as the current user content.
	const trailingSysMessages: DbMessage[] = [];
	while (topLevel.length > 0 && topLevel[topLevel.length - 1].role === "sys") {
		const msg = topLevel.pop();
		if (msg) trailingSysMessages.unshift(msg);
	}
	const trailingSysText = trailingSysMessages.map(dbMessageText).filter(Boolean).join("\n\n");

	// Drop the last user message — it's the current message being sent via currentMessage
	if (topLevel.length > 0 && topLevel[topLevel.length - 1].role === "user") {
		topLevel.pop();
	}

	// Normalize sys → user for Kiro-compatible replay, then merge consecutive user messages.
	const merged: DbMessage[] = [];
	for (const msg of topLevel) {
		const normalizedMsg: DbMessage = msg.role === "sys" ? { ...msg, role: "user" } : { ...msg };
		const prev = merged[merged.length - 1];
		if (prev && prev.role === normalizedMsg.role && normalizedMsg.role === "user") {
			const prevText = prev.contentText || "";
			const curText = normalizedMsg.contentText || "";
			prev.contentText = prevText ? `${prevText}\n${curText}` : curText;
		} else {
			merged.push(normalizedMsg);
		}
	}

	const history: KiroHistoryMessage[] = [];
	let pendingToolResults: KiroToolResult[] = [];
	let pendingUserSideCars: NonNullable<DbMessage["sideCars"]> = [];

	for (const msg of merged) {
		if (msg.role === "assistant") {
			// Kiro protocol requires user→assistant alternation.
			// When we have pending tool results (from a previous assistant's tool calls)
			// but the next DB message is another assistant (no intervening user message),
			// insert a synthetic user message to carry the tool results.
			if (pendingToolResults.length > 0) {
				history.push({
					userInputMessage: {
						content: appendSideCarsForApi("", pendingUserSideCars),
						modelId,
						origin: "AI_EDITOR",
						userInputMessageContext: { toolResults: pendingToolResults },
					},
				});
				pendingToolResults = [];
				pendingUserSideCars = [];
			}

			const content = Array.isArray(msg.contentJson) ? msg.contentJson : [];

			// Web search blocks — inject as user context before the assistant message
			const webSearchBlocks = content.filter(
				(b: { type: string }) => b.type === "web_search",
			) as Array<{ type: "web_search"; query?: string; queries?: string[] }>;
			if (webSearchBlocks.length > 0) {
				const searchSummary = webSearchBlocks
					.map((ws) => {
						const q = ws.query || ws.queries?.join(", ") || "unknown";
						return `[Web search: ${q}]`;
					})
					.join("\n");
				// Flush pending tool results first if any
				if (pendingToolResults.length > 0) {
					history.push({
						userInputMessage: {
							content: appendSideCarsForApi(searchSummary, pendingUserSideCars),
							modelId,
							origin: "AI_EDITOR",
							userInputMessageContext: { toolResults: pendingToolResults },
						},
					});
					pendingToolResults = [];
					pendingUserSideCars = [];
				} else {
					history.push({
						userInputMessage: {
							content: searchSummary,
							modelId,
							userInputMessageContext: {},
						},
					});
					// Kiro requires alternation — add a synthetic assistant ack
					history.push({
						assistantResponseMessage: { content: "OK" },
					});
				}
			}

			const textParts = content
				.filter((b: { type: string }) => b.type === "text")
				.map((b: { text: string }) => b.text);
			const text = textParts.join("\n") || msg.contentText || "";

			// Rebuild reasoning content from stored blocks so previous thinking
			// (and its signature) is echoed back across turns. Signatures/redacted
			// blocks are gated by source inside buildKiroReasoningContent so we
			// never replay one server's signature to another.
			// NOTE: upstream Kiro service requires snake_case "reasoning_content".
			const reasoningBlocks = content
				.filter((b: { type: string }) => b.type === "reasoning")
				.map(
					(b: {
						text?: string;
						providerMetadata?: import("./types").ReasoningProviderMetadata;
					}) => ({
						text: b.text ?? "",
						providerMetadata: b.providerMetadata,
					}),
				);
			const redactedThinkingBlocks = content
				.filter((b: { type: string }) => b.type === "redacted_thinking")
				.map((b: { data?: string; signatureSource?: string }) => ({
					data: b.data ?? "",
					signatureSource: b.signatureSource,
				}))
				.filter((b: { data: string }) => b.data);
			const reasoningContent = buildKiroReasoningContent(
				reasoningBlocks,
				redactedThinkingBlocks,
				currentSource,
			);

			// Only include tool uses that have completed results (success/fail).
			// Kiro rejects tool_uses without matching tool_results.
			const completedToolUseIds = new Set(
				msg.toolCalls
					?.filter((tc) => tc.status === "success" || tc.status === "fail")
					.map((tc) => tc.toolUseId) ?? [],
			);

			const toolUses =
				msg.toolCalls
					?.filter((tc) => tc.toolName && tc.toolUseId && completedToolUseIds.has(tc.toolUseId))
					.map((tc) => ({
						toolUseId: tc.toolUseId,
						name: tc.toolName,
						input: (tc.inputJson as Record<string, unknown>) ?? {},
					})) ?? [];

			const assistantMsg: KiroHistoryAssistantMessage = {
				assistantResponseMessage: {
					content: text || "",
					...(reasoningContent ? { reasoning_content: reasoningContent } : {}),
					...(toolUses.length > 0 ? { toolUses } : {}),
				},
			};
			history.push(assistantMsg);

			// Collect tool results from this assistant message's tool calls
			if (msg.toolCalls) {
				for (const tc of msg.toolCalls) {
					if (tc.status === "success" || tc.status === "fail") {
						const outputText = appendSideCarsForApi(
							outputToText(tc.outputJson),
							sideCarsForToolResult(msg.sideCars, tc.toolUseId),
						);
						pendingToolResults.push({
							toolUseId: tc.toolUseId,
							content: [{ text: outputText }],
							status: tc.status === "fail" ? "error" : "success",
							isError: tc.status === "fail",
						});
					}
				}
			}
		} else if (msg.role === "user") {
			const text = msg.contentText || "";
			const ownerNarratorId = msg.narratorId ?? narratorId;

			const userText = appendSideCarsForApi(text, pendingUserSideCars);

			// Extract images from contentJson if narrator ownership is available
			let images: KiroImage[] | undefined;
			if (ownerNarratorId && Array.isArray(msg.contentJson)) {
				const imageBlocks = (
					msg.contentJson as Array<{
						type: string;
						imageId?: string;
						mediaType?: string;
						uploadNarratorId?: string;
					}>
				).filter((b) => b.type === "image" && b.imageId);
				if (imageBlocks.length > 0) {
					const resolved: KiroImage[] = [];
					for (const block of imageBlocks) {
						const uploadNarratorId = block.uploadNarratorId ?? ownerNarratorId;
						const filePath = getImagePath(uploadNarratorId, block.imageId ?? "");
						if (filePath) {
							try {
								const result = await imageToBase64(filePath);
								const mimeToFormat: Record<string, string> = {
									"image/png": "png",
									"image/jpeg": "jpeg",
									"image/gif": "gif",
									"image/webp": "webp",
								};
								// Prefer detected real format over stored mediaType
								const effectiveMime = result.detectedMediaType ?? block.mediaType ?? "";
								resolved.push({
									format: (mimeToFormat[effectiveMime] ?? "png") as KiroImage["format"],
									source: { bytes: result.base64 },
								});
							} catch {
								// Image file may have been deleted — skip silently
							}
						}
					}
					if (resolved.length > 0) images = resolved;
				}
			}

			const userMsg: KiroHistoryUserMessage = {
				userInputMessage: {
					content: userText,
					modelId,
					origin: "AI_EDITOR",
					...(images ? { images } : {}),
					userInputMessageContext:
						pendingToolResults.length > 0 ? { toolResults: pendingToolResults } : {},
				},
			};
			history.push(userMsg);
			pendingToolResults = [];
			pendingUserSideCars = [];
		}
		if (msg.role === "assistant") {
			pendingUserSideCars = sideCarsForUserMessage(msg.sideCars);
		}
	}

	// Kiro protocol: trailing orphan user message gets auto-paired assistant "OK"
	if (history.length > 0) {
		const last = history[history.length - 1];
		if ("userInputMessage" in last) {
			history.push({
				assistantResponseMessage: { content: "OK" },
			});
		}
	}

	return {
		history,
		trailingToolResults: pendingToolResults,
		trailingUserText: appendSideCarsForApi(trailingSysText, pendingUserSideCars) || undefined,
	};
}

// ── Shared image helpers for Kiro-protocol providers ──

/** Extract a human-readable file name from the Read tool's image output placeholder. */
export function extractImageFileName(output: string): string | null {
	const m = output.match(/\[Image:\s*(.+?)\s*\(/);
	return m?.[1] ?? null;
}
