import type {
	KiroChatRequest,
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroImage,
	KiroToolResult,
} from "../kiro-adapter";
import { prepareNkpRequest } from "../nkp-ext";
import { getToolMessage, type Locale } from "../prompt-i18n";
import type { NUGProviderConfig } from "../settings";
import {
	buildKiroHistory,
	buildKiroRequest,
	extractImageFileName,
	parseSSEStream,
} from "./kiro-protocol";
import type { ChatParams, DbMessage, ParsedStreamEvent, ProviderAdapter } from "./provider";
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

/**
 * Resolve the NUG chat endpoint based on the model's channel prefix.
 *
 * Model ID formats:
 *   - "nug:kiro:claude-sonnet-4.5"       → /v1/kiro/chat (Kiro native protocol)
 *   - "nug:openai:gpt-4o"                → /v1/chat/completions (OpenAI format)
 *   - "nug:anthropic:claude-sonnet-4.5"   → /v1/anthropic/messages (Anthropic format)
 *   - "nug:claude-sonnet-4.5"             → /v1/chat (unified routing)
 *   - "nug:codex:gpt-5.3-codex"          → /v1/chat (unified routing)
 */
function resolveNugEndpoint(model: string): {
	endpoint: string;
	channel?: string;
	bareModel: string;
} {
	// Strip provider prefix (e.g. "nug:kiro:claude-sonnet-4.5" → "kiro:claude-sonnet-4.5")
	let rest = model;
	if (rest.startsWith("nug:")) rest = rest.slice(4);

	// Check for channel prefix
	const channelPrefixes = ["kiro", "openai", "anthropic", "codex"] as const;
	for (const ch of channelPrefixes) {
		if (rest.startsWith(`${ch}:`)) {
			const bareModel = rest.slice(ch.length + 1);
			switch (ch) {
				case "kiro":
					return { endpoint: "/v1/kiro/chat", channel: ch, bareModel };
				case "openai":
					return { endpoint: "/v1/chat/completions", channel: ch, bareModel };
				case "anthropic":
					return { endpoint: "/v1/anthropic/messages", channel: ch, bareModel };
				default:
					// codex and others go through unified routing
					return { endpoint: "/v1/chat", channel: ch, bareModel };
			}
		}
	}

	// No channel prefix — use unified routing
	return { endpoint: "/v1/chat", bareModel: rest };
}

// === NUG-specific API response types ===

export interface NugChannelHealthStatus {
	channelType: string;
	totalCredentials: number;
	availableCredentials: number;
	disabledCredentials: number;
	availabilityRate: number;
	currentConcurrency: number;
	maxConcurrency: number;
	queueDepth: number;
}

export interface NugQuota {
	balance: number;
	totalGranted: number;
}

export interface NugUsageEvent {
	id: string;
	channelType: string;
	model: string;
	meterUsage: number;
	quotaCost: number;
	inputTokens: number;
	outputTokens: number;
	cacheCreationInputTokens?: number;
	cacheReadInputTokens?: number;
	status: string;
	createdAt: string;
	[key: string]: unknown;
}

export interface NugUsageSummary {
	[key: string]: unknown;
}

/**
 * NUG (Narrafork Unified Gateway) provider adapter.
 *
 * Communicates with a remote NUG service. For Kiro-channel models, uses the
 * Kiro native protocol (same as NkpProvider). For other channels, uses the
 * unified /v1/chat endpoint or channel-specific endpoints.
 *
 * The Kiro protocol path reuses the same request building, history formatting,
 * and SSE event parsing as KiroProvider / NkpProvider.
 */
export class NugProvider implements ProviderAdapter {
	private config: NUGProviderConfig;

	constructor(config: NUGProviderConfig) {
		this.config = config;
	}

	private get baseUrl(): string {
		return this.config.baseUrl.replace(/\/+$/, "");
	}

	private chatHeaders(conversationId?: string): Record<string, string> {
		const h: Record<string, string> = {
			"Content-Type": "application/json",
			Authorization: `Bearer ${this.config.apiKey}`,
			Accept: "text/event-stream",
		};
		if (conversationId) {
			h["X-Conversation-ID"] = conversationId;
		}
		return h;
	}

	// === ProviderAdapter interface ===

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
		const { endpoint, channel, bareModel } = resolveNugEndpoint(params.model);

		// For Kiro channel, use the Kiro native protocol (same as NkpProvider)
		if (channel === "kiro" || endpoint === "/v1/kiro/chat") {
			yield* this.chatKiro(params);
			return;
		}

		// For other channels, build a Kiro-native request body and add a top-level
		// `model` field so NUG's gateway can route to the correct channel.
		// NUG transparently forwards the entire body to the channel service.
		const request = buildKiroRequest(params);
		prepareNkpRequest(request);

		const body = {
			model: channel ? `${channel}:${bareModel}` : bareModel,
			...request,
		};

		const conversationId = request.conversationState?.conversationId ?? params.conversationId;
		const headers = this.chatHeaders(conversationId);
		params.requestDump?.setRequest({
			transport: "http",
			url: `${this.baseUrl}${endpoint}`,
			headers: sanitizeHeaders(headers),
			body,
		});

		const response = await fetch(`${this.baseUrl}${endpoint}`, {
			method: "POST",
			headers,
			body: JSON.stringify(body),
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
			throw httpError(`NUG chat error ${response.status}: ${errText}`, response.status);
		}

		if (!response.body) {
			throw new Error("NUG returned no response body");
		}

		yield* parseSSEStream(response.body);
		if (responseTextPromise) {
			params.requestDump?.setResponseBodyText(await responseTextPromise);
		}
	}

	private async *chatKiro(params: ChatParams): AsyncGenerator<ParsedStreamEvent> {
		const request = buildKiroRequest(params);
		prepareNkpRequest(request);

		const conversationId = request.conversationState?.conversationId ?? params.conversationId;
		const headers = this.chatHeaders(conversationId);
		params.requestDump?.setRequest({
			transport: "http",
			url: `${this.baseUrl}/v1/kiro/chat`,
			headers: sanitizeHeaders(headers),
			body: request,
		});

		const response = await fetch(`${this.baseUrl}/v1/kiro/chat`, {
			method: "POST",
			headers,
			body: JSON.stringify(request),
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
			throw httpError(`NUG chat error ${response.status}: ${errText}`, response.status);
		}

		if (!response.body) {
			throw new Error("NUG returned no response body");
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
	): Promise<{ text: string; contextPercent?: number }> {
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

		prepareNkpRequest(request);
		const response = await fetch(`${this.baseUrl}/v1/kiro/chat`, {
			method: "POST",
			headers: this.chatHeaders(request.conversationState.conversationId),
			body: JSON.stringify(request),
		});

		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw httpError(`NUG generate error ${response.status}: ${errText}`, response.status);
		}

		const chunks: string[] = [];
		let contextPercent: number | undefined;

		if (response.body) {
			for await (const evt of parseSSEStream(response.body)) {
				if (evt.text != null) chunks.push(evt.text);
				if (evt.contextUsagePercentage != null) contextPercent = evt.contextUsagePercentage;
			}
		}

		return { text: chunks.join(""), contextPercent };
	}

	async generateWithHistory(
		systemInstruction: string,
		content: string,
		model: string,
		locale?: string,
	): Promise<string> {
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

		prepareNkpRequest(request);
		const response = await fetch(`${this.baseUrl}/v1/kiro/chat`, {
			method: "POST",
			headers: this.chatHeaders(request.conversationState.conversationId),
			body: JSON.stringify(request),
		});

		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw httpError(
				`NUG generateWithHistory error ${response.status}: ${errText}`,
				response.status,
			);
		}

		const chunks: string[] = [];
		if (response.body) {
			for await (const evt of parseSSEStream(response.body)) {
				if (evt.text != null) chunks.push(evt.text);
			}
		}

		return chunks.join("");
	}

	// === NUG-specific methods (for frontend proxy routes) ===

	async getChannelsHealth(): Promise<{ channels: NugChannelHealthStatus[] }> {
		const response = await fetch(`${this.baseUrl}/v1/channels/health`, {
			headers: { Authorization: `Bearer ${this.config.apiKey}` },
		});
		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw httpError(`NUG channels/health error ${response.status}: ${errText}`, response.status);
		}
		return (await response.json()) as { channels: NugChannelHealthStatus[] };
	}

	async getQuota(): Promise<NugQuota> {
		const response = await fetch(`${this.baseUrl}/v1/quota`, {
			headers: { Authorization: `Bearer ${this.config.apiKey}` },
		});
		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw httpError(`NUG quota error ${response.status}: ${errText}`, response.status);
		}
		return (await response.json()) as NugQuota;
	}

	async getUsage(limit = 50, offset = 0): Promise<{ events: NugUsageEvent[]; total: number }> {
		const url = new URL(`${this.baseUrl}/v1/usage`);
		url.searchParams.set("limit", String(limit));
		url.searchParams.set("offset", String(offset));
		const response = await fetch(url.toString(), {
			headers: { Authorization: `Bearer ${this.config.apiKey}` },
		});
		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw httpError(`NUG usage error ${response.status}: ${errText}`, response.status);
		}
		return (await response.json()) as { events: NugUsageEvent[]; total: number };
	}

	async getUsageSummary(period?: string): Promise<NugUsageSummary> {
		const url = new URL(`${this.baseUrl}/v1/usage/summary`);
		if (period) url.searchParams.set("period", period);
		const response = await fetch(url.toString(), {
			headers: { Authorization: `Bearer ${this.config.apiKey}` },
		});
		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw httpError(`NUG usage/summary error ${response.status}: ${errText}`, response.status);
		}
		return (await response.json()) as NugUsageSummary;
	}

	async getModels(): Promise<{ models: Array<Record<string, unknown>> }> {
		const response = await fetch(`${this.baseUrl}/v1/models`, {
			headers: { Authorization: `Bearer ${this.config.apiKey}` },
		});
		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw httpError(`NUG models error ${response.status}: ${errText}`, response.status);
		}
		return (await response.json()) as { models: Array<Record<string, unknown>> };
	}
}
