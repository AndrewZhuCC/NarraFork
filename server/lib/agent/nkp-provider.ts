import type {
	KiroChatRequest,
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroImage,
	KiroToolResult,
} from "../kiro-adapter";
import { prepareNkpRequest } from "../nkp-ext";
import { buildNkpRequestToken } from "../nkp-token";
import { getToolMessage, type Locale } from "../prompt-i18n";
import type { NKPProviderConfig } from "../settings";
import type { UsageData } from "../usage-tracking";
import {
	buildKiroHistory,
	buildKiroRequest,
	extractImageFileName,
	parseSSEStream,
} from "./kiro-protocol";
import type {
	ChatParams,
	DbMessage,
	GenerateMetaResult,
	ParsedStreamEvent,
	ProviderAdapter,
} from "./provider";
import { sanitizeHeaders } from "./request-dump";
import { resolveModel } from "./resolve-model";
import { ensureNonEmptySchema, resolveToolJsonSchema } from "./tool-registry";
import type { AgentToolUse, ResolvedToolDefinition } from "./types";

/** Create an Error with an attached HTTP status code for retry detection. */
function httpError(message: string, status: number): Error {
	const err = new Error(message);
	(err as Error & { status: number }).status = status;
	return err;
}

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

/**
 * NKP (Native Kiro Proxy) provider adapter.
 * Communicates with a remote NKP service using the Kiro native protocol over HTTP SSE.
 * Reuses the same request building, history formatting, and event parsing as KiroProvider,
 * but sends requests to a remote NKP endpoint instead of the local kiro-adapter.
 */
export class NkpProvider implements ProviderAdapter {
	private config: NKPProviderConfig;

	constructor(config: NKPProviderConfig) {
		this.config = config;
	}

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
		return buildKiroHistory(dbMessages, model, narratorId);
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
		const request = buildKiroRequest(params);
		const baseUrl = this.config.baseUrl.replace(/\/+$/, "");

		prepareNkpRequest(request);
		const headers = {
			"Content-Type": "application/json",
			Authorization: `Bearer ${this.config.apiKey}`,
			Accept: "text/event-stream",
			"X-NKP-Request-Token": buildNkpRequestToken(this.config.apiKey),
		};
		params.requestDump?.setRequest({
			transport: "http",
			url: `${baseUrl}/v1/kiro/chat`,
			headers: sanitizeHeaders(headers),
			body: request,
		});

		const bodyText = JSON.stringify(request);
		params.onRequestStart?.();
		const response = await fetch(`${baseUrl}/v1/kiro/chat`, {
			method: "POST",
			headers,
			body: bodyText,
			signal: params.signal,
		});
		const responseTextPromise = params.requestDump
			? response
					.clone()
					.text()
					.catch((error) => {
						params.requestDump?.setResponseError(error);
						return "";
					})
			: undefined;
		params.requestDump?.setResponseMeta({
			status: response.status,
			headers: sanitizeHeaders(response.headers),
		});

		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			params.requestDump?.setResponseBodyText(errText);
			throw httpError(`NKP chat error ${response.status}: ${errText}`, response.status);
		}

		if (!response.body) {
			throw new Error("NKP returned no response body");
		}

		yield* parseSSEStream(response.body);
		if (responseTextPromise) {
			params.requestDump?.setResponseBodyText(await responseTextPromise);
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

	pushUserTurn(history: unknown[], content: string, model: string, toolResults: unknown[]): void {
		const modelId = resolveModel(model);
		const h = history as KiroHistoryMessage[];

		const userMsg: KiroHistoryUserMessage = {
			userInputMessage: {
				content,
				modelId,
				origin: "AI_EDITOR",
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
		_reasoningBlocks?: Array<{
			text: string;
			providerMetadata?: import("./types").ReasoningProviderMetadata;
		}>,
		_webSearches?: Array<{
			id: string;
			query?: string;
			queries?: string[];
			outputIndex?: number;
		}>,
		_messageId?: string,
	): void {
		const h = history as KiroHistoryMessage[];
		const assistantMsg: KiroHistoryAssistantMessage = {
			assistantResponseMessage: {
				content: text || "",
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
		const result = await this.generateWithMeta(text, model);
		return result.text;
	}

	async generateWithMeta(
		text: string,
		model: string,
		systemInstruction?: string,
	): Promise<GenerateMetaResult> {
		const modelId = resolveModel(model);
		const request: KiroChatRequest = {
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
						origin: "AI_EDITOR",
						userInputMessageContext: {},
					},
				},
			},
		};

		const baseUrl = this.config.baseUrl.replace(/\/+$/, "");
		prepareNkpRequest(request);
		const response = await fetch(`${baseUrl}/v1/kiro/chat`, {
			method: "POST",
			headers: {
				"Content-Type": "application/json",
				Authorization: `Bearer ${this.config.apiKey}`,
				Accept: "text/event-stream",
				"X-NKP-Request-Token": buildNkpRequestToken(this.config.apiKey),
			},
			body: JSON.stringify(request),
		});

		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw httpError(`NKP generate error ${response.status}: ${errText}`, response.status);
		}

		const chunks: string[] = [];
		let contextPercent: number | undefined;
		let usage: UsageData | undefined;
		let credentialId: string | undefined;
		let meterUsage: number | undefined;
		let meterUnit: string | undefined;

		if (response.body) {
			for await (const evt of parseSSEStream(response.body)) {
				if (evt.text != null) chunks.push(evt.text);
				if (evt.contextUsagePercentage != null) contextPercent = evt.contextUsagePercentage;
				if (evt.usage) usage = toUsageData(evt.usage);
				if (evt.credentialId) credentialId = evt.credentialId;
				if (evt.metering) {
					meterUsage = evt.metering.usage;
					meterUnit = evt.metering.unit;
				}
			}
		}

		return { text: chunks.join(""), contextPercent, usage, credentialId, meterUsage, meterUnit };
	}

	async generateWithHistory(
		systemInstruction: string,
		content: string,
		model: string,
		locale?: string,
	): Promise<string> {
		const result = await this.generateWithHistoryWithMeta(
			systemInstruction,
			content,
			model,
			locale,
		);
		return result.text;
	}

	async generateWithHistoryWithMeta(
		systemInstruction: string,
		content: string,
		model: string,
		locale?: string,
	): Promise<GenerateMetaResult> {
		const modelId = resolveModel(model);
		const ack = getToolMessage("titleAck", (locale ?? "en") as Locale);
		const reminder = getToolMessage("titleReminder", (locale ?? "en") as Locale);

		const request: KiroChatRequest = {
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

		const baseUrl = this.config.baseUrl.replace(/\/+$/, "");
		prepareNkpRequest(request);
		const response = await fetch(`${baseUrl}/v1/kiro/chat`, {
			method: "POST",
			headers: {
				"Content-Type": "application/json",
				Authorization: `Bearer ${this.config.apiKey}`,
				Accept: "text/event-stream",
				"X-NKP-Request-Token": buildNkpRequestToken(this.config.apiKey),
			},
			body: JSON.stringify(request),
		});

		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw httpError(
				`NKP generateWithHistory error ${response.status}: ${errText}`,
				response.status,
			);
		}

		const chunks: string[] = [];
		let contextPercent: number | undefined;
		let usage: UsageData | undefined;
		let credentialId: string | undefined;
		let meterUsage: number | undefined;
		let meterUnit: string | undefined;
		if (response.body) {
			for await (const evt of parseSSEStream(response.body)) {
				if (evt.text != null) chunks.push(evt.text);
				if (evt.contextUsagePercentage != null) contextPercent = evt.contextUsagePercentage;
				if (evt.usage) usage = toUsageData(evt.usage);
				if (evt.credentialId) credentialId = evt.credentialId;
				if (evt.metering) {
					meterUsage = evt.metering.usage;
					meterUnit = evt.metering.unit;
				}
			}
		}

		return { text: chunks.join(""), contextPercent, usage, credentialId, meterUsage, meterUnit };
	}
}
