import type {
	KiroChatRequest,
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroToolResult,
} from "../kiro-adapter";
import { getToolMessage, type Locale } from "../prompt-i18n";
import type { NKPProviderConfig } from "../settings";
import { buildKiroHistory, buildKiroRequest, parseSSEStream } from "./kiro-protocol";
import type { ChatParams, DbMessage, ParsedStreamEvent, ProviderAdapter } from "./provider";
import { resolveModel } from "./resolve-model";
import { zodToJsonSchema } from "./tool-registry";
import type { AgentToolUse, ResolvedToolDefinition } from "./types";

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
				inputSchema: { json: zodToJsonSchema(tool.parameters) },
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

		const response = await fetch(`${baseUrl}/v1/kiro/chat`, {
			method: "POST",
			headers: {
				"Content-Type": "application/json",
				Authorization: `Bearer ${this.config.apiKey}`,
				Accept: "text/event-stream",
			},
			body: JSON.stringify(request),
			signal: params.signal,
		});

		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw new Error(`NKP chat error ${response.status}: ${errText}`);
		}

		if (!response.body) {
			throw new Error("NKP returned no response body");
		}

		yield* parseSSEStream(response.body);
	}

	formatToolResult(toolUseId: string, output: string, isError: boolean): unknown {
		return {
			toolUseId,
			content: [{ text: output }],
			status: isError ? "error" : "success",
			isError,
		} satisfies KiroToolResult;
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

	pushAssistantTurn(history: unknown[], text: string, toolUses: AgentToolUse[]): void {
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
		_systemInstruction?: string,
	): Promise<{ text: string; contextPercent?: number }> {
		const modelId = resolveModel(model);
		const request: KiroChatRequest = {
			conversationState: {
				conversationId: crypto.randomUUID(),
				chatTriggerType: "MANUAL",
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
		const response = await fetch(`${baseUrl}/v1/kiro/chat`, {
			method: "POST",
			headers: {
				"Content-Type": "application/json",
				Authorization: `Bearer ${this.config.apiKey}`,
				Accept: "text/event-stream",
			},
			body: JSON.stringify(request),
		});

		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw new Error(`NKP generate error ${response.status}: ${errText}`);
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

		const baseUrl = this.config.baseUrl.replace(/\/+$/, "");
		const response = await fetch(`${baseUrl}/v1/kiro/chat`, {
			method: "POST",
			headers: {
				"Content-Type": "application/json",
				Authorization: `Bearer ${this.config.apiKey}`,
				Accept: "text/event-stream",
			},
			body: JSON.stringify(request),
		});

		if (!response.ok) {
			const errText = await response.text().catch(() => "");
			throw new Error(`NKP generateWithHistory error ${response.status}: ${errText}`);
		}

		const chunks: string[] = [];
		if (response.body) {
			for await (const evt of parseSSEStream(response.body)) {
				if (evt.text != null) chunks.push(evt.text);
			}
		}

		return chunks.join("");
	}
}
