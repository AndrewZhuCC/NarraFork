import type {
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroToolResult,
} from "../kiro-adapter";
import { getToolMessage, type Locale } from "../prompt-i18n";
import { buildKiroHistory, buildKiroRequest, parseKiroStreamEvent } from "./kiro-protocol";
import type { ChatParams, DbMessage, ParsedStreamEvent, ProviderAdapter } from "./provider";
import { resolveModel } from "./resolve-model";
import { zodToJsonSchema } from "./tool-registry";
import type { AgentToolUse, ResolvedToolDefinition } from "./types";

/**
 * Kiro CLI chat protocol adapter.
 * Encapsulates all Kiro-specific request building, history formatting,
 * stream parsing, and tool result formatting.
 */
export class KiroProvider implements ProviderAdapter {
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
		const { kiroChatRaw } = await import("../kiro-adapter");
		const request = buildKiroRequest(params) as import("../kiro-adapter").KiroChatRequest;
		for await (const evt of kiroChatRaw(request, params.signal)) {
			yield parseKiroStreamEvent(evt);
		}
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
		const { kiroGenerate } = await import("../kiro-adapter");
		const modelId = resolveModel(model);
		// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
		return kiroGenerate(text, modelId as any);
	}

	async generateWithMeta(
		text: string,
		model: string,
		_systemInstruction?: string,
	): Promise<{ text: string; contextPercent?: number }> {
		const { kiroGenerateWithMeta } = await import("../kiro-adapter");
		const modelId = resolveModel(model);
		// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
		return kiroGenerateWithMeta(text, modelId as any);
	}

	async generateWithHistory(
		systemInstruction: string,
		content: string,
		model: string,
		locale?: string,
	): Promise<string> {
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
		for await (const evt of kiroAdapter.kiroChatRaw(request)) {
			if (evt.eventType === "assistantResponseEvent" && evt.data.content != null) {
				chunks.push(String(evt.data.content));
			}
		}
		return chunks.join("");
	}
}
