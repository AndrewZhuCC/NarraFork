/**
 * Grok provider — xAI Responses API as a built-in provider.
 *
 * Grok's web search (`web_search`) and X search (`x_search`) are server-side
 * tools: they are declared in the main `/responses` request and executed
 * upstream. The gateway chain (newapi.anzhi.app → Sub2API → grok2api build
 * gateway) passes them through and emits `web_search_call` / `x_search_call`
 * output items with `url_citation` annotations, exactly like xAI's own API.
 *
 * The request/SSE handling mirrors the grok2api creative console's
 * `publicResponsesStream` (the authoritative reference for this gateway chain),
 * mapped onto NarraFork's `ParsedStreamEvent` vocabulary.
 */

import { logger } from "../logger";
import { resolveProxyForUrl } from "../net/proxy";
import { getToolMessage, type Locale } from "../prompt-i18n";
import type { GrokProviderConfig } from "../settings";
import { parseModelId, settings } from "../settings";
import { readWithTimeout } from "../stream-timeout";
import { extractOpenAIUsage } from "../usage-tracking";
import { fetchWithNetworkDiagnostics } from "./diagnostic-fetch";
import { convertHistoryToResponsesApi } from "./openai-provider";
import type {
	ChatParams,
	DbMessage,
	GenerateMetaResult,
	ParsedStreamEvent,
	ProviderAdapter,
} from "./provider";
import { captureDumpResponse, captureResponseStream, sanitizeHeaders } from "./request-dump";
import { resolveToolJsonSchema } from "./tool-registry";
import {
	type AgentToolUse,
	ApiError,
	type ResolvedToolDefinition,
} from "./types";

const DEFAULT_GROK_API_BASE = "https://api.x.ai/v1";
/** xAI Grok 4.x context window (user-verified). */
const DEFAULT_GROK_CONTEXT_WINDOW = 500_000;

// === Grok message types (Responses API shape) ===

interface GrokResponsesMessage {
	role?: string;
	content?: unknown;
	type?: string;
	call_id?: string;
	name?: string;
	arguments?: string;
	output?: string;
}

type GrokContentPart =
	| { type: "text"; text: string }
	| { type: "image_url"; image_url: { url: string } };

interface GrokToolResult {
	call_id: string;
	output: string;
}

interface GrokResponsesToolCall {
	id: string;
	type: "function";
	function: { name: string; arguments: string };
}

interface GrokResponsesItem {
	id?: string;
	type?: string;
	status?: string;
	call_id?: string;
	name?: string;
	arguments?: string;
	input?: string;
	query?: string;
	action?: Record<string, unknown>;
	content?: unknown;
}

interface GrokResponsesChunk {
	type?: string;
	delta?: string;
	text?: string;
	item?: GrokResponsesItem;
	item_id?: string;
	arguments?: string;
	input?: string;
	response?: Record<string, unknown>;
	output_index?: number;
}

export class GrokProvider implements ProviderAdapter {
	readonly mayLeakXmlToolCalls = false;

	private config: GrokProviderConfig;

	constructor(config: GrokProviderConfig) {
		this.config = config;
	}

	/** Context window for a model: upstream default is 500K for Grok 4.x. */
	contextWindowForModel(model: string): number {
		if (this.config.defaultContextWindow)
			return this.config.defaultContextWindow;
		const bare = parseModelId(model).model;
		return /grok-4/i.test(bare) ? DEFAULT_GROK_CONTEXT_WINDOW : 131_072;
	}

	private pfetch(
		input: string | URL | Request,
		init?: RequestInit,
	): Promise<Response> {
		const target = input instanceof Request ? input.url : input;
		const proxy = resolveProxyForUrl(target, this.config.proxy);
		return fetchWithNetworkDiagnostics(input, init, { proxy });
	}

	private getApiKey(): string {
		const apiKey = this.config.apiKey;
		if (!apiKey) {
			throw new Error(
				`Grok API key not configured for provider "${this.config.name}".`,
			);
		}
		return apiKey;
	}

	private buildHeaders(apiKey: string): Record<string, string> {
		return {
			"Content-Type": "application/json",
			Authorization: `Bearer ${apiKey}`,
		};
	}

	/** Responses API function tools: flat `{type:"function", name, description, parameters}`. */
	formatTools(tools: ResolvedToolDefinition[]): unknown[] {
		return tools.map((tool) => ({
			type: "function",
			name: tool.name,
			description: tool.description,
			parameters: resolveToolJsonSchema(tool),
			strict: false,
		}));
	}

	async buildHistory(
		dbMessages: DbMessage[],
		_model: string,
		_narratorId?: string,
	): Promise<{ history: unknown[]; trailingToolResults: unknown[] }> {
		return this.buildGrokHistory(dbMessages);
	}

	injectSystemPrompt(
		history: unknown[],
		systemPrompt: string,
		_model: string,
		_locale?: string,
	): void {
		const h = history as GrokResponsesMessage[];
		const locale = (_locale ?? "en") as Locale;
		const identity = GROK_IDENTITY[locale] ?? GROK_IDENTITY.en;
		h.unshift({ role: "developer", content: `${identity}\n\n${systemPrompt}` });
	}

	async *chat(params: ChatParams): AsyncGenerator<ParsedStreamEvent> {
		const apiKey = this.getApiKey();
		const baseUrl = (this.config.baseUrl || DEFAULT_GROK_API_BASE).replace(
			/\/+$/,
			"",
		);

		// Build the conversation: developer/system prompt + history, then the
		// current user turn and any pending tool results.
		const messages: GrokResponsesMessage[] = [
			...this.toResponsesMessages(params.history as GrokResponsesMessage[]),
		];
		for (const tr of params.toolResults as GrokToolResult[]) {
			messages.push({
				type: "function_call_output",
				call_id: tr.call_id,
				output: tr.output,
			});
		}
		if (params.content && params.content !== ".") {
			if (params.images?.length) {
				const parts: GrokContentPart[] = [
					{ type: "text", text: params.content },
				];
				for (const img of params.images) {
					parts.push({
						type: "image_url",
						image_url: { url: `data:image/${img.format};base64,${img.base64}` },
					});
				}
				messages.push({ role: "user", content: parts });
			} else {
				messages.push({ role: "user", content: params.content });
			}
		} else if (params.toolResults.length === 0) {
			messages.push({ role: "user", content: params.content });
		}

		const model = parseModelId(params.model).model;
		const body: Record<string, unknown> = {
			model,
			// Responses input items keep their full shape: `function_call_output` /
			// `function_call` entries carry type/call_id/name, which the flat
			// {role, content} remap would drop and the gateway rejects
			// ("input[i].content 必须是字符串或数组").
			input: messages,
			stream: true,
			store: false,
		};
		if (params.reasoningEffort) {
			body.reasoning =
				params.reasoningEffort === "none"
					? { effort: "none" }
					: { effort: params.reasoningEffort, summary: "auto" };
		}

		// Function tools from the agent + Grok's built-in search tools.
		const tools: Array<Record<string, unknown>> = [
			...(params.tools as Array<Record<string, unknown>>),
		];
		if (this.config.enableWebSearch) tools.push({ type: "web_search" });
		if (this.config.enableXSearch) tools.push({ type: "x_search" });
		if (tools.length > 0) body.tools = tools;

		const headers = this.buildHeaders(apiKey);
		const dumpRequest = {
			transport: "http",
			url: `${baseUrl}/responses`,
			headers: sanitizeHeaders(headers),
			body,
		};
		const diagnosticDump =
			params.requestDump?.maxCaptureBytes !== undefined ? params.requestDump : undefined;
		if (diagnosticDump) {
			diagnosticDump.beginResponseAttempt(dumpRequest, diagnosticDump.maxCaptureBytes);
		} else {
			params.requestDump?.setRequest(dumpRequest);
		}
		logger.debug("Grok chat request", {
			model,
			baseUrl,
			toolCount: tools.length,
			messageCount: messages.length,
			hasToolResults: (params.toolResults as unknown[]).length > 0,
			webSearch: !!this.config.enableWebSearch,
			xSearch: !!this.config.enableXSearch,
		});

		params.onRequestStart?.();
		const response = await this.pfetch(`${baseUrl}/responses`, {
			method: "POST",
			headers,
			body: JSON.stringify(body),
			signal: params.signal,
		});
		// Only diagnostic capture uses the bounded tap; legacy full-dump buffering is unchanged.
		const responseTextPromise =
			params.requestDump && !diagnosticDump
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
			if (diagnosticDump) {
				diagnosticDump.appendResponseText(errText);
				diagnosticDump.finishResponseCapture(true);
			} else {
				params.requestDump?.setResponseBodyText(errText);
			}
			throw new ApiError(
				response.status,
				`Grok API error ${response.status}: ${errText}`,
			);
		}

		if (!response.body) {
			throw new Error("Grok API returned no body");
		}

		const capture = captureResponseStream(response.body, diagnosticDump);
		try {
			yield* this.parseResponsesStream(capture.stream);
		} finally {
			capture.finish();
		}
		if (responseTextPromise) {
			const bodyText = await responseTextPromise;
			const maxSize = settings.agent?.requestDumpMaxSize ?? 1024 * 1024;
			params.requestDump?.setResponseBodyTextWithLimit(bodyText, maxSize);
		}
	}

	formatToolResult(
		toolUseId: string,
		output: string,
		_isError: boolean,
		_images?: Array<{ format: string; base64: string }>,
	): unknown {
		return { call_id: toolUseId, output } satisfies GrokToolResult;
	}

	pushUserTurn(
		history: unknown[],
		content: string,
		_model: string,
		toolResults: unknown[],
		images?: Array<{ format: string; base64: string }>,
	): void {
		const h = history as GrokResponsesMessage[];
		for (const tr of toolResults as GrokToolResult[]) {
			h.push({
				type: "function_call_output",
				call_id: tr.call_id,
				output: tr.output,
			});
		}
		const hasText = !!content && content !== ".";
		if (images?.length) {
			const parts: GrokContentPart[] = [
				{ type: "text", text: hasText ? content : "[user sent image(s)]" },
			];
			for (const img of images) {
				parts.push({
					type: "image_url",
					image_url: { url: `data:image/${img.format};base64,${img.base64}` },
				});
			}
			h.push({ role: "user", content: parts });
		} else if (hasText) {
			h.push({ role: "user", content });
		}
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
		const h = history as GrokResponsesMessage[];
		if (text) {
			h.push({ role: "assistant", content: [{ type: "output_text", text }] });
		}
		for (const tu of toolUses) {
			h.push({
				type: "function_call",
				call_id: tu.toolUseId,
				name: tu.name,
				arguments: JSON.stringify(tu.input ?? {}),
			});
		}
	}

	async generate(text: string, model: string): Promise<string> {
		const result = await this.generateWithMeta(text, model);
		return result.text;
	}

	async generateWithMeta(
		text: string,
		model: string,
		systemInstruction?: string,
		options?: import("./provider").GenerateOptions,
	): Promise<GenerateMetaResult> {
		const apiKey = this.getApiKey();
		const baseUrl = (this.config.baseUrl || DEFAULT_GROK_API_BASE).replace(
			/\/+$/,
			"",
		);

		const bareModel = parseModelId(model).model;
		const messages: Array<{ role: "system" | "user"; content: string }> = [];
		if (systemInstruction) {
			messages.push({ role: "system", content: systemInstruction });
		}
		messages.push({ role: "user", content: text });

		const url = `${baseUrl}/responses`;
		const headers = this.buildHeaders(apiKey);
		const body = { model: bareModel, input: messages, stream: true, store: false };
		options?.requestDump?.beginResponseAttempt(
			{ transport: "http", url, headers, body },
			settings.agent.requestDumpMaxSize,
		);
		const response = captureDumpResponse(
			await this.pfetch(url, {
				method: "POST",
				headers,
				body: JSON.stringify(body),
				signal: options?.signal,
			}),
			options?.requestDump,
		);

		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw new ApiError(
				response.status,
				`Grok API error ${response.status}: ${errText}`,
			);
		}

		if (!response.body) {
			throw new Error("Grok API returned no body");
		}

		return this.collectStreamTextWithMeta(response.body, options);
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
	): Promise<GenerateMetaResult> {
		const apiKey = this.getApiKey();
		const baseUrl = (this.config.baseUrl || DEFAULT_GROK_API_BASE).replace(
			/\/+$/,
			"",
		);

		const reminder = getToolMessage(
			"titleReminder",
			(locale ?? "en") as Locale,
		);
		const bareModel = parseModelId(model).model;

		const response = await this.pfetch(`${baseUrl}/responses`, {
			method: "POST",
			headers: this.buildHeaders(apiKey),
			body: JSON.stringify({
				model: bareModel,
				input: [
					{ role: "developer", content: systemInstruction },
					{ role: "user", content: `${reminder}\n\n${content}` },
				],
				stream: true,
				store: false,
			}),
			signal: options?.signal,
		});

		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw new ApiError(
				response.status,
				`Grok API error ${response.status}: ${errText}`,
			);
		}

		if (!response.body) {
			throw new Error("Grok API returned no body");
		}

		return this.collectStreamTextWithMeta(response.body, options);
	}

	// === Internal methods ===

	private buildGrokHistory(dbMessages: DbMessage[]): {
		history: GrokResponsesMessage[];
		trailingToolResults: GrokToolResult[];
	} {
		const topLevel = dbMessages.filter(
			(m) =>
				!m.parentToolUseId &&
				(m.role === "user" || m.role === "assistant" || m.role === "sys"),
		);

		if (topLevel.length > 0 && topLevel[topLevel.length - 1].role === "user") {
			topLevel.pop();
		}

		const history: GrokResponsesMessage[] = [];
		let pendingToolResults: GrokToolResult[] = [];

		for (const msg of topLevel) {
			if (msg.role === "assistant") {
				history.push(...this.assistantToResponsesMessage(msg));
				pendingToolResults = [];
			} else if (msg.role === "user") {
				history.push({
					role: "user",
					content: this.messageText(msg) || "[user message]",
				});
				pendingToolResults = [];
			} else if (msg.role === "sys") {
				history.push({ role: "developer", content: this.messageText(msg) });
			}
		}

		return { history, trailingToolResults: pendingToolResults };
	}

	private assistantToResponsesMessage(msg: DbMessage): GrokResponsesMessage[] {
		const text = this.messageText(msg);
		const result: GrokResponsesMessage[] = [];
		if (text) {
			result.push({
				role: "assistant",
				content: [{ type: "output_text", text }],
			});
		}
		for (const block of this.messageToolUses(msg)) {
			result.push({
				type: "function_call",
				call_id: block.toolUseId,
				name: block.name,
				arguments: JSON.stringify(block.input ?? {}),
			});
		}
		if (result.length === 0) result.push({ role: "assistant" });
		return result;
	}

	private messageText(msg: DbMessage): string {
		const content = msg.contentJson;
		if (typeof content === "string") return content;
		if (!Array.isArray(content)) return "";
		const parts: string[] = [];
		for (const block of content) {
			if (!block || typeof block !== "object") continue;
			const b = block as { type?: string; text?: string };
			if (b.type === "text" && typeof b.text === "string") parts.push(b.text);
		}
		return parts.join("\n");
	}

	private messageToolUses(msg: DbMessage): Array<{
		toolUseId: string;
		name: string;
		input?: Record<string, unknown>;
	}> {
		const content = msg.contentJson;
		if (!Array.isArray(content)) return [];
		const result: Array<{
			toolUseId: string;
			name: string;
			input?: Record<string, unknown>;
		}> = [];
		for (const block of content) {
			if (!block || typeof block !== "object") continue;
			const b = block as {
				type?: string;
				toolUseId?: string;
				name?: string;
				input?: unknown;
			};
			if (
				b.type === "tool_use" &&
				typeof b.toolUseId === "string" &&
				typeof b.name === "string"
			) {
				result.push({
					toolUseId: b.toolUseId,
					name: b.name,
					...(b.input && typeof b.input === "object"
						? { input: b.input as Record<string, unknown> }
						: {}),
				});
			}
		}
		return result;
	}

	/** Normalize stored assistant/tool turns into Responses input items. */
	private toResponsesMessages(
		history: GrokResponsesMessage[],
	): GrokResponsesMessage[] {
		return history.flatMap((m) => {
			if (m.type === "function_call_output") return [m];
			if (m.type === "function_call") return [m];
			if (
				m.role === "assistant" &&
				m.content == null &&
				Array.isArray((m as { tool_calls?: unknown }).tool_calls)
			) {
				const calls =
					(m as unknown as { tool_calls?: GrokResponsesToolCall[] })
						.tool_calls ?? [];
				return calls.map(
					(tc): GrokResponsesMessage => ({
						type: "function_call",
						call_id: tc.id,
						name: tc.function.name,
						arguments: tc.function.arguments,
					}),
				);
			}
			// An assistant message with neither content nor tool calls must still
			// carry a content array — the gateway rejects a missing/null content.
			if (m.role === "assistant" && m.content == null) {
				return [{ ...m, content: [] }];
			}
			return [m];
		});
	}

	private async collectStreamTextWithMeta(
		body: ReadableStream<Uint8Array>,
		options?: import("./provider").GenerateOptions,
	): Promise<GenerateMetaResult> {
		const decoder = new TextDecoder();
		let buffer = "";
		let text = "";
		let usage: GenerateMetaResult["usage"] = null;

		const reader = body.getReader();
		try {
			while (true) {
				const { done, value } = await readWithTimeout(reader);
				if (done) break;
				buffer += decoder.decode(value, { stream: true });

				const blocks = buffer.split("\n\n");
				buffer = blocks.pop() ?? "";

				for (const block of blocks) {
					const data = block
						.split("\n")
						.filter((line) => line.startsWith("data:"))
						.map((line) => line.slice(5).trimStart())
						.join("\n");
					if (!data || data === "[DONE]") continue;
					let payload: GrokResponsesChunk;
					try {
						payload = JSON.parse(data) as GrokResponsesChunk;
					} catch {
						continue;
					}
					const type = payload.type ?? "";
					if (
						type === "response.output_text.delta" &&
						typeof payload.delta === "string"
					) {
						text += payload.delta;
						await options?.onTextDelta?.(payload.delta);
					}
					if (
						type === "response.output_text.done" &&
						typeof payload.text === "string"
					) {
						text = payload.text;
					}
					if (type === "response.completed" && payload.response) {
						const response = payload.response;
						if (typeof response.output_text === "string")
							text = response.output_text;
						if (response.usage) usage = extractOpenAIUsage(response.usage);
					}
				}
			}
		} finally {
			reader.releaseLock();
		}

		return { text, usage };
	}

	private async *parseResponsesStream(
		body: ReadableStream<Uint8Array>,
	): AsyncGenerator<ParsedStreamEvent> {
		const decoder = new TextDecoder();
		let buffer = "";
		// Keyed by output_index (like OpenAIProvider): `function_call_arguments.delta`
		// carries item_id but NOT call_id, and item_id differs from the accumulator id.
		const toolAccum = new Map<
			number,
			{ callId: string; name: string; args: string; emitted: boolean }
		>();
		// Tracks whether reasoning text was already streamed via delta events; the
		// trailing `.done` full-text event must then be skipped (mirrors
		// OpenAIProvider's emitOnlyIfAccumulatorEmpty).
		const reasoningSeen = new Set<number>();
		const textOutputIndex = 0;

		const reader = body.getReader();
		try {
			while (true) {
				const { done, value } = await readWithTimeout(reader);
				if (done) break;
				buffer += decoder.decode(value, { stream: true });

				const blocks = buffer.split("\n\n");
				buffer = blocks.pop() ?? "";

				for (const block of blocks) {
					const data = block
						.split("\n")
						.filter((line) => line.startsWith("data:"))
						.map((line) => line.slice(5).trimStart())
						.join("\n");
					if (!data || data === "[DONE]") continue;
					let payload: GrokResponsesChunk;
					try {
						payload = JSON.parse(data) as GrokResponsesChunk;
					} catch {
						continue;
					}
					const type = payload.type ?? "";
					const event: ParsedStreamEvent = {};

					if (
						type === "response.output_text.delta" &&
						typeof payload.delta === "string"
					) {
						event.text = payload.delta;
						yield event;
						continue;
					}
					if (
						type === "response.output_text.done" &&
						typeof payload.text === "string"
					) {
						// Terminal text for this output item; nothing to yield.
						continue;
					}
					if (
						(type === "response.reasoning_summary_text.delta" ||
							type === "response.reasoning_text.delta") &&
						typeof payload.delta === "string"
					) {
						if (payload.output_index != null) {
							reasoningSeen.add(payload.output_index);
						}
						event.reasoning = payload.delta;
						yield event;
						continue;
					}
					if (
						(type === "response.reasoning_summary_text.done" ||
							type === "response.reasoning_text.done") &&
						typeof payload.text === "string"
					) {
						// Delta events already streamed the text; emitting the full
						// `.done` text again duplicates every block (same sentence
						// twice). Only fall back to it when nothing was streamed.
						if (
							payload.output_index != null &&
							reasoningSeen.has(payload.output_index)
						) {
							continue;
						}
						event.reasoning = payload.text;
						yield event;
						continue;
					}
					if (
						type === "response.output_item.added" ||
						type === "response.output_item.done"
					) {
						const item = payload.item;
						if (!item) continue;
						const finalize = type === "response.output_item.done";
						if (item.type === "message") {
							// The text is already streamed via output_text.delta; the
							// finalized item must NOT re-emit the full text or every
							// sentence appears twice. Only surface the stable item id
							// (mirrors OpenAIProvider).
							if (item.id) {
								event.messageId = item.id;
								yield event;
							}
							continue;
						}
						if (item.type === "reasoning") {
							// Reasoning text already streamed via reasoning_text.delta;
							// the finalized item must not re-emit it.
							if (item.id) {
								event.messageId = item.id;
								yield event;
							}
							continue;
						}
						if (
							item.type === "web_search_call" ||
							item.type === "x_search_call"
						) {
							// Server-side search activity; surface for the UI but do not
							// execute. Both web and X search map to the host's webSearch
							// lifecycle events.
							const id = item.id ?? `${item.type}-${item.name ?? "tool"}`;
							const query =
								firstString(item.query, item.action?.query) ??
								item.action?.queries;
							const status =
								item.status === "completed" ? "completed" : "in_progress";
							event.webSearch = {
								id,
								status,
								...(typeof query === "string" && query ? { query } : {}),
								outputIndex: payload.output_index,
							};
							yield event;
							continue;
						}
						if (item.type === "custom_tool_call") {
							// custom_tool_call items are upstream-internal (x_search expands
							// into x_semantic_search / x_keyword_search / x_thread_fetch etc).
							// They are already executed upstream — their results are folded
							// into the assistant text with url_citation annotations — so they
							// must never be executed by the host. Surface as search activity.
							const id =
								firstString(item.id, item.call_id) ??
								`${item.name}-${item.name}`;
							event.webSearch = {
								id,
								status:
									item.status === "completed" ? "completed" : "in_progress",
								query: parseXSearchQuery(item),
								outputIndex: payload.output_index,
							};
							yield event;
							continue;
						}
						if (item.type === "function_call") {
							const idx = payload.output_index;
							const callId = item.call_id ?? item.id ?? "";
							const name = item.name ?? "";
							if (idx == null || !callId || !name) continue;
							const existing = toolAccum.get(idx);
							if (existing && existing.callId === callId) {
								// Finalize on output_item.done when arguments never arrived
								// via the delta/done events (some gateways skip them).
								if (finalize && !existing.emitted) {
									existing.emitted = true;
									event.toolUseChunk = {
										toolUseId: callId,
										name,
										stop: true,
									};
									yield event;
								}
								continue;
							}
							toolAccum.set(idx, {
								callId,
								name,
								args: "",
								emitted: false,
							});
							event.toolUseChunk = {
								toolUseId: callId,
								name,
								input: undefined,
								stop: false,
							};
							yield event;
							continue;
						}
						continue;
					}
					if (
						type === "response.function_call_arguments.delta" ||
						type === "response.custom_tool_call_input.delta"
					) {
						const idx = payload.output_index;
						const acc = idx != null ? toolAccum.get(idx) : undefined;
						if (!acc || acc.emitted) continue;
						const detail = payload.delta ?? "";
						acc.args += detail;
						event.toolUseChunk = {
							toolUseId: acc.callId,
							name: acc.name,
							input: detail,
							stop: false,
						};
						yield event;
						continue;
					}
					if (
						type === "response.function_call_arguments.done" ||
						type === "response.custom_tool_call_input.done"
					) {
						const idx = payload.output_index;
						const acc = idx != null ? toolAccum.get(idx) : undefined;
						if (!acc || acc.emitted) continue;
						const complete = firstString(payload.arguments, payload.input);
						if (typeof complete === "string") acc.args = complete;
						acc.emitted = true;
						event.toolUseChunk = {
							toolUseId: acc.callId,
							name: acc.name,
							stop: true,
						};
						yield event;
						continue;
					}
					if (type === "response.completed" && payload.response) {
						const response = payload.response;
						if (response.usage) {
							const usage = extractOpenAIUsage(response.usage);
							event.usage = {
								...(usage ?? {}),
								contextWindow: this.contextWindowForModel(
									settings.agent?.defaultModel ?? "",
								),
							};
							yield event;
						}
						continue;
					}
					if (type === "response.failed" || type === "error") {
						const error = readError(payload.response ?? payload);
						event.invalidState = {
							reason: error.code ?? "api_error",
							message: error.message ?? "Unknown Grok API error",
						};
						yield event;
						return;
					}
				}
			}
		} finally {
			reader.releaseLock();
		}
	}
}

// === Helpers ===

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function firstString(value: unknown, ...rest: unknown[]): string | undefined {
	for (const v of [value, ...rest]) {
		if (typeof v === "string" && v) return v;
	}
	return undefined;
}

function readContentText(content: unknown): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	const parts: string[] = [];
	for (const part of content) {
		if (!isRecord(part)) continue;
		if (typeof part.text === "string") parts.push(part.text);
	}
	return parts.join("");
}

function readReasoningItem(_item: GrokResponsesItem): string {
	return "";
}

function readError(payload: unknown): { message?: string; code?: string } {
	if (!isRecord(payload)) return {};
	const error = isRecord(payload.error) ? payload.error : payload;
	return {
		message: firstString(error.message, error.type),
		code: firstString(error.code, error.type),
	};
}

/** x_search is executed upstream as internal custom tools with these names. */
function isXSearchInternalTool(name: string | undefined): boolean {
	return name === "x_semantic_search" || name === "x_keyword_search";
}

/** Extract the query from an x-search custom tool call's JSON input. */
function parseXSearchQuery(item: GrokResponsesItem): string | undefined {
	const raw = firstString(item.input, item.arguments);
	if (!raw) return undefined;
	try {
		const parsed = JSON.parse(raw) as Record<string, unknown>;
		return typeof parsed.query === "string" ? parsed.query : undefined;
	} catch {
		return undefined;
	}
}

// === Grok identity prompt ===
const GROK_IDENTITY: Record<string, string> = {
	en: `You are an AI coding assistant with access to tools for reading, writing, and editing files, running shell commands, searching codebases, and more. You MUST use your tools to accomplish tasks — do not just describe what you would do. When the user asks you to do something, take action by calling the appropriate tools.`,
	"zh-CN": `你是一个 AI 编程助手，拥有读取、写入和编辑文件、运行 shell 命令、搜索代码库等工具。你必须使用工具来完成任务——不要只是描述你会做什么。当用户要求你做某事时，请通过调用相应的工具来采取行动。`,
};
