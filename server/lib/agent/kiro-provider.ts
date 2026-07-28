import type {
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroImage,
	KiroToolResult,
} from "../kiro-adapter";
import { getToolMessage, type Locale } from "../prompt-i18n";
import { settings } from "../settings";
import type { UsageData } from "../usage-tracking";
import {
	buildKiroHistory,
	buildKiroReasoningContent,
	buildKiroRequest,
	extractImageFileName,
	KiroStreamToolCallAccumulator,
	normalizeKiroChatRequest,
	parseKiroStreamEvent,
} from "./kiro-protocol";
import type { ChatParams, DbMessage, ParsedStreamEvent, ProviderAdapter } from "./provider";
import { DEFAULT_DUMP_MAX_BYTES, MAX_DUMP_EVENT_COUNT } from "./request-dump";
import { resolveModel } from "./resolve-model";
import { ensureNonEmptySchema, resolveToolJsonSchema } from "./tool-registry";
import type { AgentToolUse, ResolvedToolDefinition } from "./types";

/**
 * Kiro CLI chat protocol adapter.
 * Encapsulates all Kiro-specific request building, history formatting,
 * stream parsing, and tool result formatting.
 */
function toUsageData(usage: ParsedStreamEvent["usage"]): UsageData | undefined {
	if (!usage) return undefined;
	return {
		inputTokens: usage.inputTokens ?? usage.promptTokens ?? 0,
		outputTokens: usage.completionTokens ?? 0,
		cachedInputTokens: usage.cachedInputTokens ?? 0,
		cacheCreationInputTokens: usage.cacheCreationInputTokens ?? 0,
		cacheCreation5mInputTokens: usage.cacheCreation5mTokens ?? 0,
		cacheCreation1hInputTokens: usage.cacheCreation1hTokens ?? 0,
		reasoningTokens: usage.reasoningTokens ?? 0,
	};
}

export class KiroProvider implements ProviderAdapter {
	readonly mayLeakXmlToolCalls = true;

	formatTools(tools: ResolvedToolDefinition[]): unknown[] {
		return tools.map((tool) => ({
			toolSpecification: {
				name: tool.name,
				description: tool.description,
				inputSchema: { json: ensureNonEmptySchema(resolveToolJsonSchema(tool)) },
			},
		}));
	}

	async buildHistory(
		dbMessages: DbMessage[],
		model: string,
		narratorId?: string,
	): Promise<{ history: unknown[]; trailingToolResults: unknown[] }> {
		return buildKiroHistory(dbMessages, model, narratorId, this.getActiveReasoningSource());
	}

	getActiveReasoningSource(): string | undefined {
		// A direct Kiro provider is a single upstream — its own identity.
		return "kiro";
	}

	injectSystemPrompt(
		history: unknown[],
		systemPrompt: string,
		model: string,
		locale?: string,
	): void {
		const modelId = resolveModel(model);
		const h = history as KiroHistoryMessage[];
		const ack = getToolMessage("systemPromptAck", (locale ?? "en") as Locale);
		h.unshift(
			{
				userInputMessage: {
					content: systemPrompt,
					modelId,
					userInputMessageContext: {},
				},
			},
			{
				assistantResponseMessage: {
					content: ack,
				},
			},
		);
	}

	async *chat(params: ChatParams): AsyncGenerator<ParsedStreamEvent> {
		const { kiroChatRaw } = await import("../kiro-adapter");
		const request = normalizeKiroChatRequest(buildKiroRequest(params));
		params.requestDump?.setRequest({
			transport: "kiro-plugin",
			body: request,
		});
		// Only retain raw events when a dump collector is present, and cap the buffer so a
		// long stream cannot grow an unbounded array on the main thread (see CLAUDE.md).
		const dump = params.requestDump;
		const rawEvents: import("../kiro-adapter").StreamEvent[] | undefined = dump ? [] : undefined;
		// Leaked XML tool calls may span multiple streamed text deltas, so buffer text
		// at the stream level and lift completed tool calls out of it. Parse each raw
		// event with parseTextToolCalls disabled to avoid double parsing.
		const toolCallAccumulator = new KiroStreamToolCallAccumulator({
			parseTextToolCalls: params.tools.length > 0,
			model: params.model,
		});
		params.onRequestStart?.();
		for await (const evt of kiroChatRaw(request, params.signal)) {
			if (rawEvents && rawEvents.length < MAX_DUMP_EVENT_COUNT) rawEvents.push(evt);
			yield toolCallAccumulator.process(parseKiroStreamEvent(evt, { parseTextToolCalls: false }));
		}
		const trailing = toolCallAccumulator.finalize();
		if (trailing) yield trailing;
		if (dump && rawEvents) {
			dump.setResponseEventsWithLimit(
				rawEvents,
				MAX_DUMP_EVENT_COUNT,
				settings.agent?.requestDumpMaxSize ?? DEFAULT_DUMP_MAX_BYTES,
			);
		}
	}

	formatToolResult(
		toolUseId: string,
		output: string,
		isError: boolean,
		images?: Array<{ format: string; base64: string }>,
	): unknown {
		const result: KiroToolResult & { _images?: KiroImage[]; _imageLabel?: string } = {
			toolUseId,
			content: [{ text: output }],
			status: isError ? "error" : "success",
			isError,
		};
		if (images?.length) {
			result._images = images.map((img) => ({
				format: img.format as KiroImage["format"],
				source: { bytes: img.base64 },
			}));
			result._imageLabel = extractImageFileName(output) ?? "image";
		}
		return result;
	}
	pushUserTurn(
		history: unknown[],
		content: string,
		model: string,
		toolResults: unknown[],
		images?: Array<{ format: string; base64: string }>,
	): void {
		const modelId = resolveModel(model);
		const h = history as KiroHistoryMessage[];
		const kiroImages = images?.map((img) => ({
			format: img.format as KiroImage["format"],
			source: { bytes: img.base64 },
		}));

		const userMsg: KiroHistoryUserMessage = {
			userInputMessage: {
				content,
				modelId,
				origin: "AI_EDITOR",
				...(kiroImages?.length ? { images: kiroImages } : {}),
				userInputMessageContext:
					toolResults.length > 0 ? { toolResults: toolResults as KiroToolResult[] } : {},
			},
		};
		h.push(userMsg);
	}

	pushAssistantTurn(
		history: unknown[],
		text: string,
		toolUses: AgentToolUse[],
		reasoningBlocks?: Array<{
			text: string;
			providerMetadata?: import("./types").ReasoningProviderMetadata;
			outputIndex?: number;
		}>,
		_webSearches?: Array<{
			id: string;
			query?: string;
			queries?: string[];
			outputIndex?: number;
		}>,
		_messageId?: string,
		_imageGenerations?: Array<{
			id: string;
			revisedPrompt?: string;
			result?: string;
			outputIndex?: number;
		}>,
		_textOutputIndex?: number,
		redactedThinkingBlocks?: Array<{
			data: string;
			outputIndex?: number;
			signatureSource?: string;
		}>,
	): void {
		const h = history as KiroHistoryMessage[];
		// NOTE: upstream Kiro service requires snake_case "reasoning_content".
		const reasoningContent = buildKiroReasoningContent(
			reasoningBlocks,
			redactedThinkingBlocks,
			this.getActiveReasoningSource(),
		);
		const assistantMsg: KiroHistoryAssistantMessage = {
			assistantResponseMessage: {
				content: text || "",
				...(reasoningContent ? { reasoning_content: reasoningContent } : {}),
				...(toolUses.length > 0
					? {
							toolUses: toolUses.map((tu) => ({
								toolUseId: tu.toolUseId,
								name: tu.name,
								input: tu.input,
							})),
						}
					: {}),
			},
		};
		h.push(assistantMsg);
	}

	async generate(text: string, model: string): Promise<string> {
		return (await this.generateWithMeta(text, model)).text;
	}

	async generateWithMeta(
		text: string,
		model: string,
		systemInstruction?: string,
		options?: import("./provider").GenerateOptions,
	): Promise<import("./provider").GenerateMetaResult> {
		if (options?.signal?.aborted) {
			throw new DOMException("Kiro generation aborted", "AbortError");
		}
		const modelId = resolveModel(model);
		const kiroAdapter = await import("../kiro-adapter");
		const request: import("../kiro-adapter").KiroChatRequest = {
			conversationState: {
				conversationId: crypto.randomUUID(),
				chatTriggerType: "MANUAL",
				...(systemInstruction
					? {
							history: [
								{
									userInputMessage: {
										content: systemInstruction,
										modelId,
										origin: "AI_EDITOR",
									},
								},
								{
									assistantResponseMessage: {
										content: "Understood.",
									},
								},
							] as KiroHistoryMessage[],
						}
					: {}),
				currentMessage: {
					userInputMessage: {
						content: text,
						modelId,
						userInputMessageContext: {},
						origin: "AI_EDITOR",
					},
				},
			},
		};
		const chunks: string[] = [];
		let contextPercent: number | undefined;
		let usage: UsageData | undefined;
		let credentialId: string | undefined;
		let meterUsage: number | undefined;
		let meterUnit: string | undefined;
		// Pass the signal into kiroChatRaw so the underlying HTTP request is
		// actually aborted; the in-loop check is a fast-path guard for events
		// already buffered before the abort propagates.
		for await (const rawEvent of kiroAdapter.kiroChatRaw(request, options?.signal)) {
			if (options?.signal?.aborted) {
				throw new DOMException("Kiro generation aborted", "AbortError");
			}
			const evt = parseKiroStreamEvent(rawEvent);
			if (evt.text != null) {
				chunks.push(evt.text);
				await options?.onTextDelta?.(evt.text);
			}
			if (evt.reasoning) await options?.onReasoningDelta?.(evt.reasoning);
			if (evt.contextUsagePercentage != null) contextPercent = evt.contextUsagePercentage;
			if (evt.usage) usage = toUsageData(evt.usage);
			if (evt.credentialId) credentialId = evt.credentialId;
			if (evt.metering) {
				meterUsage = evt.metering.usage;
				meterUnit = evt.metering.unit;
			}
		}
		return { text: chunks.join(""), contextPercent, usage, credentialId, meterUsage, meterUnit };
	}

	async generateWithHistory(
		systemInstruction: string,
		content: string,
		model: string,
		locale?: string,
		options?: import("./provider").GenerateOptions,
	): Promise<string> {
		const result = await this.generateWithHistoryWithMeta(
			systemInstruction,
			content,
			model,
			locale,
			options,
		);
		return result.text;
	}

	async generateWithHistoryWithMeta(
		systemInstruction: string,
		content: string,
		model: string,
		locale?: string,
		options?: import("./provider").GenerateOptions,
	): Promise<import("./provider").GenerateMetaResult> {
		const kiroAdapter = await import("../kiro-adapter");
		const modelId = resolveModel(model);
		const ack = getToolMessage("titleAck", (locale ?? "en") as Locale);
		const reminder = getToolMessage("titleReminder", (locale ?? "en") as Locale);

		const request: import("../kiro-adapter").KiroChatRequest = {
			conversationState: {
				conversationId: crypto.randomUUID(),
				chatTriggerType: "MANUAL",
				history: [
					{
						userInputMessage: {
							content: systemInstruction,
							modelId,
							origin: "AI_EDITOR",
						},
					},
					{
						assistantResponseMessage: {
							content: ack,
						},
					},
				] as KiroHistoryMessage[],
				currentMessage: {
					userInputMessage: {
						content: `${reminder}\n\n${content}`,
						modelId,
						userInputMessageContext: {},
						origin: "AI_EDITOR",
					},
				},
			},
		};

		const chunks: string[] = [];
		let contextPercent: number | undefined;
		let usage: UsageData | undefined;
		let credentialId: string | undefined;
		let meterUsage: number | undefined;
		let meterUnit: string | undefined;
		for await (const rawEvent of kiroAdapter.kiroChatRaw(request, options?.signal)) {
			if (options?.signal?.aborted) {
				throw new DOMException("Kiro generation aborted", "AbortError");
			}
			const evt = parseKiroStreamEvent(rawEvent);
			if (evt.text != null) {
				chunks.push(evt.text);
				await options?.onTextDelta?.(evt.text);
			}
			if (evt.reasoning) await options?.onReasoningDelta?.(evt.reasoning);
			if (evt.contextUsagePercentage != null) contextPercent = evt.contextUsagePercentage;
			if (evt.usage) usage = toUsageData(evt.usage);
			if (evt.credentialId) credentialId = evt.credentialId;
			if (evt.metering) {
				meterUsage = evt.metering.usage;
				meterUnit = evt.metering.unit;
			}
		}
		return { text: chunks.join(""), contextPercent, usage, credentialId, meterUsage, meterUnit };
	}
}
