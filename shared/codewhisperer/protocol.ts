import type {
	KiroChatRequest,
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroImage,
	KiroReasoningContent,
	KiroToolResult,
	StreamEvent,
} from "@shared/codewhisperer/protocol-types";
import {
	DEFAULT_CONTINUATION_PROMPT,
	type HistoryRuntime,
	type NormalizeRuntime,
	silentProtocolLogger,
} from "@shared/codewhisperer/runtime";
import { generateSyntheticId } from "@shared/codewhisperer/synthetic-id";
import { readWithTimeout } from "@shared/stream-timeout";
import type { ChatParams } from "./chat-params";
import { parseErrorDiagnostics } from "./error-diagnostics";
import { parseGatewaySSEEvent } from "./gateway-events";
import { signatureSourcesCompatible } from "./reasoning-source";
import { resolveModel } from "./resolve-model";
import { outputToText } from "./tool-output";
import type { DbMessage, ParsedStreamEvent } from "./types";

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
	/**
	 * The model driving this stream (raw, provider-prefixed name is fine). Used to enable
	 * model-specific stream repairs — currently the opus-4.8 / opus-5 hallucinated line-head
	 * marker filter (see {@link KiroHallucinationMarkerFilter}).
	 */
	model?: string;
	/**
	 * Per-read inactivity budget (ms) for {@link parseSSEStream}. Omitted → the
	 * `readWithTimeout` default. A half-open SSE connection never delivers FIN, so
	 * without this guard the read would hang forever and the in-flight tool call
	 * would stay at "initializing" with no deadline of its own.
	 */
	staleTimeoutMs?: number;
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

/**
 * Whether the message that will be sent as `currentMessage` actually carries something
 * for the model to act on.
 *
 * Deliberately different from {@link hasUserPayload}: that helper counts `tools` as
 * payload (correct for history entries), but `currentMessage` always ships the tool
 * specs, so counting them here would make every empty turn look valid.
 */
function currentMessageCarriesTurn(message: KiroHistoryUserMessage): boolean {
	const user = message.userInputMessage;
	const ctx = user.userInputMessageContext;
	return Boolean(
		user.content.trim() || (user.images?.length ?? 0) > 0 || (ctx?.toolResults?.length ?? 0) > 0,
	);
}

/**
 * Last-resort guard against an empty `currentMessage`.
 *
 * The Kiro service rejects a request whose current message has no content, no tool
 * results and no images with an opaque
 * `{"__type":"…#ValidationException","message":"Improperly formed request.","reason":"REQUEST_BODY_INVALID"}`
 * — it never says which field was at fault.
 *
 * That shape is reachable from at least two directions:
 *  1. A transient failure (e.g. Kiro "unexpected EOF") leaves a kept reasoning-only
 *     partial assistant message. The retry clears the user text because the partial was
 *     kept, but a reasoning-only partial has no tool_uses, so no tool results are
 *     produced either — nothing carries the turn.
 *  2. `sanitizeKiroToolPairs` strips tool results that no longer answer the immediately
 *     preceding assistant toolUse. A caller can pass tool results in good faith and
 *     still end up with an empty current message *after* sanitization.
 *
 * Because route 2 only materializes after sanitization, this must run on the final
 * normalized shape rather than at request-build time. Injecting a minimal continuation
 * instruction keeps the turn semantically correct (the model resumes where it stopped)
 * and is strictly better than a request the upstream refuses outright.
 */
function ensureCurrentMessageCarriesTurn(
	message: KiroHistoryUserMessage,
	continuationPrompt: string,
): boolean {
	if (currentMessageCarriesTurn(message)) return false;
	message.userInputMessage.content = continuationPrompt;
	return true;
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
export function normalizeKiroChatRequest(
	request: KiroChatRequest,
	runtime: NormalizeRuntime = {},
): KiroChatRequest {
	const log = runtime.logger ?? silentProtocolLogger;
	const continuationPrompt = runtime.continuationPrompt ?? DEFAULT_CONTINUATION_PROMPT;
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
		// Sanitization consumed the current turn entirely (or left an assistant message in
		// its place). Fall back to the pre-normalization current message, which may itself
		// be empty — hence the same guard applies.
		const fallbackCurrent = currentMessage;
		const injected = ensureCurrentMessageCarriesTurn(fallbackCurrent, continuationPrompt);
		if (injected) {
			log.warn("Kiro request had an empty current message; injected a continuation prompt", {
				model: modelId,
				route: "current_turn_not_user",
				historyLength: beforeCurrent.length,
			});
		}
		cs.history = beforeCurrent.map((entry) => entry.message);
		cs.currentMessage = { userInputMessage: fallbackCurrent.userInputMessage };
		return request;
	}

	const injected = ensureCurrentMessageCarriesTurn(current, continuationPrompt);
	if (injected) {
		log.warn("Kiro request had an empty current message; injected a continuation prompt", {
			model: modelId,
			route: "empty_after_normalize",
			historyLength: beforeCurrent.length,
		});
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

const PARAMETER_OPEN_PATTERN = /<parameter\b([^>]*)>/gi;
const PARAMETER_CLOSE_PATTERN = /<\/parameter\s*>/i;

/**
 * Trim the artifacts left behind when a `<parameter>` value was never closed.
 *
 * The observed malformation ends the value with a stray `">` — the model closing an
 * attribute quote it never opened — followed by a newline before the next tag. Neither
 * the quote/bracket nor the trailing newline belongs to the intended value.
 */
function normalizeUnterminatedParameterValue(value: string): string {
	return value.replace(/\s+$/u, "").replace(/"?>$/u, "").replace(/\s+$/u, "");
}

/**
 * Extract the `name`/value pairs from a leaked `<invoke>` body.
 *
 * A single `<parameter ...>(.*?)</parameter>` regex is not enough: Kiro/NUG sometimes
 * drop one parameter's closing tag, and the lazy body then swallows every following
 * parameter tag into that value. Observed in the wild as a Bash `workdir` that became
 * `D:/Some Project">\n<parameter name="description">…`, a path no filesystem can
 * resolve — the tool reported a missing working directory for a directory that exists.
 *
 * So scan the open tags and terminate each value at whichever comes first: its own
 * closing tag, the next parameter's open tag, or the end of the body. The last case also
 * recovers a trailing parameter whose closing tag never arrived instead of dropping it.
 */
function parseInvokeParameters(body: string): Record<string, unknown> {
	const input: Record<string, unknown> = {};
	// Fresh regex instance: the shared literal is stateful (`g`) and this parser can
	// run re-entrantly from both the streaming and stateless paths.
	const openRe = new RegExp(PARAMETER_OPEN_PATTERN.source, PARAMETER_OPEN_PATTERN.flags);

	let openMatch = openRe.exec(body);
	while (openMatch) {
		const valueStart = openMatch.index + openMatch[0].length;
		const nextOpenMatch = openRe.exec(body);
		const nextOpenIndex = nextOpenMatch?.index ?? body.length;

		const rest = body.slice(valueStart, nextOpenIndex);
		const closeMatch = PARAMETER_CLOSE_PATTERN.exec(rest);
		const rawValue = closeMatch ? rest.slice(0, closeMatch.index) : rest;
		const value = closeMatch ? rawValue : normalizeUnterminatedParameterValue(rawValue);

		const paramName = readXmlAttribute(openMatch[1] ?? "", "name");
		if (paramName) {
			input[paramName] = coerceKiroXmlParameter(paramName, decodeXmlEntities(value));
		}

		openMatch = nextOpenMatch;
	}

	return input;
}

/** Build a tool use from a leaked `<invoke>` open-tag attribute string and body. */
function buildInvokeToolUse(attrs: string, body: string): ParsedKiroTextToolUse | null {
	const name = readXmlAttribute(attrs, "name");
	if (!name) return null;

	return {
		toolUseId: `kiro_xml_${generateSyntheticId(12)}`,
		name,
		input: parseInvokeParameters(body),
	};
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
 * opus-4.8 / opus-5 (via Kiro/NUG) share a known failure mode where the model compulsively
 * repeats a short junk token at the start of freshly wrapped lines — e.g. a run of lines each
 * beginning with "course", "card", "call", "count", or the Chinese "课". This is a decoding
 * hallucination, not real content, so we strip the junk token when it immediately follows a
 * newline.
 *
 * The filter is intentionally narrow but aggressive for the Latin markers. When a *lowercase*
 * "course" / "card" / "call" / "count" sits at a line head (right after a newline, with optional
 * inline indentation), it is stripped no matter what follows — a space, a CJK character, an ASCII
 * period ("course. 验证…"), other punctuation, or end-of-text. This is safe enough because the
 * filter only ever sees streamed *assistant prose*, and a genuine English sentence would capitalize
 * a word at the start of a line; a lowercase marker there is almost always the hallucination. The
 * ONLY exception is when the marker is really the prefix of a longer word (e.g. "coursework" /
 * "called" / "cards" / "counts"): if the next character is an ASCII letter or digit, the token is
 * left untouched. A single trailing ASCII sentence-punctuation mark (". , ; : ! ?") is also consumed
 * so lines like "course. 验证…" become "验证…" instead of leaving a stray ". " behind.
 *
 * The Chinese marker "课" keeps a stricter whitespace/newline/EOF boundary so that legitimate
 * Chinese words such as "课程" / "课件" / "功课" are never mangled.
 */
const HALLUCINATION_LATIN_MARKERS = ["course", "card", "call", "count"] as const;
const HALLUCINATION_CJK_MARKERS = ["课"] as const;
const HALLUCINATION_LINE_HEAD_MARKERS = [
	...HALLUCINATION_LATIN_MARKERS,
	...HALLUCINATION_CJK_MARKERS,
] as const;
// The `5` branch needs a right boundary: a bare `opus-5` prefix would also match a future
// `opus-50`. `(?!\d)` keeps `opus-5`, `opus-5.1`, `opus-5-thinking` matching while rejecting
// any longer version number.
const HALLUCINATION_MODEL_PATTERN = /opus-(?:4[.-]8|5(?!\d))/i;

/** Whether the opus-4.8 / opus-5 line-head marker filter should run for the given model. */
export function shouldFilterHallucinatedMarkers(model?: string): boolean {
	return model != null && HALLUCINATION_MODEL_PATTERN.test(model);
}

const escapeMarker = (m: string) => m.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const LATIN_MARKER_ALTERNATION = HALLUCINATION_LATIN_MARKERS.map(escapeMarker).join("|");
const CJK_MARKER_ALTERNATION = HALLUCINATION_CJK_MARKERS.map(escapeMarker).join("|");

// Match one known marker at the very start of a line, then drop it. Only the leading indentation
// (group 1) is kept. Latin markers are stripped unless the next char is an ASCII letter/digit (a
// real longer word like "coursework"); a single trailing ASCII punctuation mark plus the run of
// horizontal whitespace is also consumed. The CJK marker "课" only matches when followed by
// whitespace/newline/EOF. So "course Look" → "Look", "course还有" → "还有", "course. 验证" → "验证",
// "    course foo" → "    foo", and a bare "course\n" → "\n". The filter applies this only to lines
// that follow a newline and sit outside a fenced code block.
const HALLUCINATION_LINE_HEAD_RE = new RegExp(
	`^([ \\t]*)(?:(?:${LATIN_MARKER_ALTERNATION})(?![a-zA-Z0-9])[.,;:!?]?|(?:${CJK_MARKER_ALTERNATION})(?=$|[ \\t\\r\\n]))[ \\t]*`,
	"u",
);

/**
 * A fenced code block delimiter: up to 3 leading spaces/tabs, then a run of 3+ backticks or
 * tildes, then the rest of the line (the info string for an opener). Markers are never stripped
 * from lines inside a fenced block, so code the model shows us survives verbatim.
 */
const CODE_FENCE_RE = /^[ \t]{0,3}(`{3,}|~{3,})([^\r\n]*)$/;

/**
 * An unterminated line at a line head that could still turn out to be a fence delimiter: either a
 * short run of 1-2 fence chars with nothing after it (the next delta may extend it to 3+), or an
 * already-complete run of 3+ whose info string is still arriving (an info string means "opener",
 * an empty one may mean "closer", so the decision needs the whole line).
 */
const PARTIAL_CODE_FENCE_RE = /^[ \t]{0,3}(?:(?:`{1,2}|~{1,2})$|(?:`{3,}|~{3,})[^\r\n]*$)/;

interface OpenCodeFence {
	char: string;
	length: number;
}

// A line head that is a *complete* Latin marker plus an optional trailing sentence-punctuation
// mark and run of horizontal whitespace, with no real content yet. While streaming we must keep
// holding such a tail: the punctuation/whitespace run may still grow, or real content may arrive
// on the next delta, and only then is the exact span the marker regex will consume settled.
const LATIN_MARKER_UNSETTLED_TAIL_RE = new RegExp(
	`^(?:${LATIN_MARKER_ALTERNATION})[.,;:!?]?[ \\t]*$`,
);

/**
 * Stateful filter that removes opus-4.8 / opus-5 hallucinated line-head markers from streamed text.
 *
 * Because markers land at the start of a line and the offending token can be split across
 * streaming deltas (e.g. "\nco" + "urse Look"), the filter buffers the shortest tail that could
 * still grow into a marker line and only emits text proven to be safe.
 *
 * Fenced code blocks (``` / ~~~) are exempt: inside a fence, `count = 0` or `call(foo)` at a line
 * head is real code, so lines are passed through untouched until the fence closes. The fence state
 * is tracked line by line across deltas, and only a matching-or-longer run of the same fence
 * character closes a block (CommonMark rules), so a ``` block can contain ~~~ lines and vice versa.
 * The very first line of the stream is also never filtered, since a marker must follow a newline.
 */
export class KiroHallucinationMarkerFilter {
	private buffer = "";
	/** Whether the next buffered line starts a fresh line (i.e. a newline was already emitted). */
	private atLineStart = false;
	/** The currently open fence, or null when outside a fenced code block. */
	private openFence: OpenCodeFence | null = null;

	push(delta: string): string {
		this.buffer += delta;
		let out = "";
		// Consume every complete line first, so fence/line-start state is current before we judge
		// the unterminated tail.
		const lastNewline = this.buffer.lastIndexOf("\n");
		if (lastNewline >= 0) {
			out += this.consume(this.buffer.slice(0, lastNewline + 1));
			this.buffer = this.buffer.slice(lastNewline + 1);
		}
		// Emit the trailing partial line too, unless it could still change meaning.
		if (this.canEmitPartialLine(this.buffer)) {
			out += this.consume(this.buffer);
			this.buffer = "";
		}
		return out;
	}

	flush(): string {
		const emit = this.consume(this.buffer);
		this.buffer = "";
		return emit;
	}

	/**
	 * Strip markers from every line in `text`, tracking code-fence state as we go. `text` is only
	 * ever the settled prefix of the buffer, so a trailing line without a newline here is either
	 * final (flush) or already proven safe to emit (push).
	 */
	private consume(text: string): string {
		if (!text) return text;
		let out = "";
		let pos = 0;
		while (pos < text.length) {
			const nl = text.indexOf("\n", pos);
			const lineEnd = nl < 0 ? text.length : nl + 1;
			const raw = text.slice(pos, lineEnd);
			out += this.consumeLine(raw);
			// A marker only ever sits at the head of a line that follows a newline.
			this.atLineStart = nl >= 0;
			pos = lineEnd;
		}
		return out;
	}

	/** Handle one line (including its trailing newline, when present). */
	private consumeLine(raw: string): string {
		// Line content without its trailing newline, for fence detection.
		const content = raw.replace(/\r?\n$/, "");
		const fence = CODE_FENCE_RE.exec(content);

		if (this.openFence) {
			// Inside a fence everything is verbatim; a bare matching delimiter closes the block.
			if (fence && this.closesFence(fence[1], fence[2])) this.openFence = null;
			return raw;
		}
		if (fence) {
			this.openFence = { char: fence[1][0], length: fence[1].length };
			return raw;
		}
		if (!this.atLineStart) return raw;
		return raw.replace(HALLUCINATION_LINE_HEAD_RE, (_m, indent: string) => indent);
	}

	/** Whether a fence delimiter closes the currently open fence (CommonMark rules). */
	private closesFence(run: string, info: string): boolean {
		const open = this.openFence;
		if (!open) return false;
		// Same fence character, at least as long, and no info string.
		return run[0] === open.char && run.length >= open.length && info.trim().length === 0;
	}

	/**
	 * Whether the buffered partial line (no trailing newline) can be emitted now, or must be held
	 * because the next delta could still change how it is treated — by completing a marker or by
	 * turning the line into a code-fence delimiter.
	 */
	private canEmitPartialLine(tail: string): boolean {
		if (tail.length === 0) return true;
		// A trailing CR belongs with a not-yet-arrived LF; holding it keeps CRLF splitting correct.
		if (tail === "\r") return false;
		// A tail continuing a line whose head was already emitted holds no marker or fence position,
		// so it goes out immediately — mid-line inline code like "use `x`" must not stall the stream.
		if (!this.atLineStart) return true;
		// Hold a tail that might still become a fence delimiter: opening or closing a block changes
		// whether *subsequent* lines get filtered, so the full delimiter line must be settled first.
		if (PARTIAL_CODE_FENCE_RE.test(tail)) return false;
		// Inside a fenced block nothing is stripped, so no marker retention is needed.
		if (this.openFence) return true;
		// Line head without its inline indentation.
		const head = tail.replace(/^[ \t]*/, "");
		// Hold back while the line head is still an incomplete marker prefix (e.g. "cou") with
		// nothing after it — the next delta may complete it into a marker. couldBeMarkerPrefix
		// requires the whole segment to be a prefix, so it already fails once real content follows.
		if (couldBeMarkerPrefix(head)) return false;
		// Also hold a *complete* Latin marker plus optional trailing punctuation/whitespace when no
		// real content has arrived yet: the exact span the marker regex will consume (including the
		// trailing whitespace run separating it from content) is not settled until content appears.
		if (LATIN_MARKER_UNSETTLED_TAIL_RE.test(head)) return false;
		return true;
	}
}

/** Whether `text` is a (possibly empty) prefix of one of the known hallucination markers. */
function couldBeMarkerPrefix(text: string): boolean {
	if (text.length === 0) return true;
	for (const marker of HALLUCINATION_LINE_HEAD_MARKERS) {
		if (marker.startsWith(text) && text.length <= marker.length) return true;
	}
	return false;
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
	private readonly markerFilter: KiroHallucinationMarkerFilter | null;

	constructor(options: KiroStreamParseOptions = {}) {
		this.parser = options.parseTextToolCalls ? new KiroTextToolCallStreamParser() : null;
		this.markerFilter = shouldFilterHallucinatedMarkers(options.model)
			? new KiroHallucinationMarkerFilter()
			: null;
	}

	/** Adjust a parsed event in place, lifting any completed leaked tool calls out of text. */
	process(parsed: ParsedStreamEvent): ParsedStreamEvent {
		if (parsed.text != null && this.markerFilter) {
			parsed.text = this.markerFilter.push(parsed.text);
		}
		if (!this.parser || parsed.text == null) return parsed;
		const { text, toolUses } = this.parser.push(parsed.text);
		parsed.text = text;
		appendDedupedToolUses(parsed, toolUses);
		return parsed;
	}

	/** Return a trailing event for any buffered text/tool calls, or null if nothing remains. */
	finalize(): ParsedStreamEvent | null {
		let markerTail = "";
		if (this.markerFilter) markerTail = this.markerFilter.flush();

		if (!this.parser) {
			if (!markerTail) return null;
			return { text: markerTail };
		}

		// Feed any buffered marker-filter tail through the tool-call parser before flushing so a
		// leaked tool call that straddled the two buffers is still recovered.
		let pushedText = "";
		const pushedToolUses: ParsedKiroTextToolUse[] = [];
		if (markerTail) {
			const pushed = this.parser.push(markerTail);
			pushedText = pushed.text;
			pushedToolUses.push(...pushed.toolUses);
		}
		const { text: flushedText, toolUses: flushedToolUses } = this.parser.flush();
		const text = pushedText + flushedText;
		const toolUses = [...pushedToolUses, ...flushedToolUses];
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
			diagnostics: parseErrorDiagnostics(evt.data, {
				source: "provider",
				phase: "invalid_state_event",
			}),
		};
	}

	// Proxy-layer error event (harmless for the native kiro provider; NUG emits it).
	if (evt.eventType === "error") {
		const diagnostics = parseErrorDiagnostics(evt.data, {
			source: "gateway",
			phase: "sse_error",
		});
		result.invalidState = {
			reason: diagnostics?.reason ?? "gateway_error",
			message: diagnostics?.message ?? String(evt.data.message ?? "Gateway proxy error"),
			diagnostics,
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
			// Guarded read, matching the native Kiro binary parser (kiro/event-stream-parser.ts)
			// and every other provider. A stalled gateway stream raises StreamStaleError, which
			// isRetryableError() always treats as retryable, instead of hanging the turn.
			const { done, value } = await readWithTimeout(reader, options.staleTimeoutMs);
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
	runtime: HistoryRuntime = {},
): Promise<{
	history: KiroHistoryMessage[];
	trailingToolResults: KiroToolResult[];
	trailingUserText?: string;
}> {
	const modelId = resolveModel(model);
	const resolveImage = runtime.resolveImage;

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

	for (const msg of merged) {
		if (msg.role === "assistant") {
			// Kiro protocol requires user→assistant alternation.
			// When we have pending tool results (from a previous assistant's tool calls)
			// but the next DB message is another assistant (no intervening user message),
			// insert a synthetic user message to carry the tool results.
			if (pendingToolResults.length > 0) {
				history.push({
					userInputMessage: {
						content: "",
						modelId,
						origin: "AI_EDITOR",
						userInputMessageContext: { toolResults: pendingToolResults },
					},
				});
				pendingToolResults = [];
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
							content: searchSummary,
							modelId,
							origin: "AI_EDITOR",
							userInputMessageContext: { toolResults: pendingToolResults },
						},
					});
					pendingToolResults = [];
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
						pendingToolResults.push({
							toolUseId: tc.toolUseId,
							content: [{ text: outputToText(tc.outputJson) }],
							status: tc.status === "fail" ? "error" : "success",
							isError: tc.status === "fail",
						});
					}
				}
			}
		} else if (msg.role === "user") {
			const userText = msg.contentText || "";
			const ownerNarratorId = msg.narratorId ?? narratorId;

			// Extract images from contentJson if narrator ownership is available.
			// Image storage is host-owned, so resolution goes through the injected
			// resolver; without one, image blocks are simply not attached.
			let images: KiroImage[] | undefined;
			if (ownerNarratorId && resolveImage && Array.isArray(msg.contentJson)) {
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
						// A missing or deleted image resolves to null and is skipped.
						const result = await resolveImage({
							narratorId: uploadNarratorId,
							imageId: block.imageId ?? "",
						});
						if (!result) continue;
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

	// `trailingUserText` survives the side-car removal: it carries the trailing `sys`
	// rows this builder lifts out of history (see the `trailingSysMessages` loop above),
	// which Kiro needs as the current message because a trailing sys row would otherwise
	// force a synthetic assistant "OK" and leave the real current message empty.
	return {
		history,
		trailingToolResults: pendingToolResults,
		trailingUserText: trailingSysText || undefined,
	};
}

// ── Shared image helpers for Kiro-protocol providers ──

/** Extract a human-readable file name from the Read tool's image output placeholder. */
export function extractImageFileName(output: string): string | null {
	const m = output.match(/\[Image:\s*(.+?)\s*\(/);
	return m?.[1] ?? null;
}
