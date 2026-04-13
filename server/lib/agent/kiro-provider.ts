import type {
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroImage,
	KiroToolResult,
} from "../kiro-adapter";
import { getToolMessage, type Locale } from "../prompt-i18n";
import {
	buildKiroHistory,
	buildKiroRequest,
	extractImageFileName,
	parseKiroStreamEvent,
} from "./kiro-protocol";
import type { ChatParams, DbMessage, ParsedStreamEvent, ProviderAdapter } from "./provider";
import { resolveModel } from "./resolve-model";
import { ensureNonEmptySchema, resolveToolJsonSchema } from "./tool-registry";
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
		const { kiroChatRaw } = await import("../kiro-adapter");
		const request = buildKiroRequest(params) as import("../kiro-adapter").KiroChatRequest;
		params.requestDump?.setRequest({
			transport: "kiro-plugin",
			body: request,
		});
		const rawEvents: import("../kiro-adapter").StreamEvent[] = [];
		for await (const evt of kiroChatRaw(request, params.signal)) {
			rawEvents.push(evt);
			yield parseKiroStreamEvent(evt);
		}
		params.requestDump?.setResponseEvents(rawEvents);
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
		systemInstruction?: string,
	): Promise<{ text: string; contextPercent?: number }> {
		const modelId = resolveModel(model);
		if (systemInstruction) {
			// Kiro API has no system prompt field — simulate via history
			const kiroAdapter = await import("../kiro-adapter");
			const ack = "Understood.";
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
			for await (const evt of kiroAdapter.kiroChatRaw(request)) {
				if (evt.eventType === "assistantResponseEvent" && evt.data.content != null) {
					chunks.push(String(evt.data.content));
				}
				if (evt.eventType === "contextUsageEvent") {
					const pct = evt.data.contextUsagePercentage ?? evt.data.context_usage_percentage;
					if (pct != null) contextPercent = Number(pct);
				}
			}
			return { text: chunks.join(""), contextPercent };
		}
		const { kiroGenerateWithMeta } = await import("../kiro-adapter");
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
