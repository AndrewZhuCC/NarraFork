import type {
	KiroChatRequest,
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroImage,
	KiroToolResult,
	StreamEvent,
} from "../kiro-adapter";
import { getToolMessage, type Locale } from "../prompt-i18n";
import { getImagePath, imageToBase64 } from "../uploads";
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

// === Private helpers ===

function buildKiroRequest(params: ChatParams): KiroChatRequest {
	const modelId = resolveModel(params.model);
	const history = params.history as KiroHistoryMessage[];
	const toolResults = params.toolResults as KiroToolResult[];

	// Convert generic images to KiroImage format
	const kiroImages: KiroImage[] | undefined = params.images?.length
		? params.images.map((img) => ({
				format: img.format as "jpeg" | "png" | "gif" | "webp",
				source: { bytes: img.base64 },
			}))
		: undefined;

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
					...(kiroImages ? { images: kiroImages } : {}),
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

function parseKiroStreamEvent(evt: StreamEvent): ParsedStreamEvent {
	const result: ParsedStreamEvent = {};

	if (evt.eventType === "assistantResponseEvent") {
		if (evt.data.content != null) {
			result.text = String(evt.data.content);
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
		const toolUse = evt.data.toolUse as
			| { toolUseId: string; name: string; input: Record<string, unknown> }
			| undefined;
		if (toolUse?.toolUseId) {
			result.toolUses = [
				{
					toolUseId: toolUse.toolUseId,
					name: toolUse.name,
					input: toolUse.input ?? {},
				},
			];
		}

		// Non-streaming tool uses (array)
		const toolUses = evt.data.toolUses as
			| Array<{ toolUseId: string; name: string; input: Record<string, unknown> }>
			| undefined;
		if (Array.isArray(toolUses) && toolUses.length > 0) {
			result.toolUses = toolUses.map((tu) => ({
				toolUseId: tu.toolUseId,
				name: tu.name,
				input: tu.input ?? {},
			}));
		}
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
		const content = evt.data.content ?? evt.data.text;
		if (content != null) {
			result.reasoning = String(content);
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

	return result;
}

// === buildKiroHistory — moved from message-builder.ts ===

async function buildKiroHistory(
	dbMessages: DbMessage[],
	model: string,
	narratorId?: string,
): Promise<{ history: KiroHistoryMessage[]; trailingToolResults: KiroToolResult[] }> {
	const modelId = resolveModel(model);

	// Only top-level user/assistant messages
	const topLevel = dbMessages.filter(
		(m) => !m.parentToolUseId && (m.role === "user" || m.role === "assistant"),
	);

	// Drop the last user message — it's the current message being sent via currentMessage
	if (topLevel.length > 0 && topLevel[topLevel.length - 1].role === "user") {
		topLevel.pop();
	}

	// Merge consecutive user messages (e.g. from previous failed sessions)
	const merged: DbMessage[] = [];
	for (const msg of topLevel) {
		const prev = merged[merged.length - 1];
		if (prev && prev.role === msg.role && msg.role === "user") {
			const prevText = prev.contentText || "";
			const curText = msg.contentText || "";
			prev.contentText = prevText ? `${prevText}\n${curText}` : curText;
		} else {
			merged.push({ ...msg });
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
			const textParts = content
				.filter((b: { type: string }) => b.type === "text")
				.map((b: { text: string }) => b.text);
			const text = textParts.join("\n") || msg.contentText || "";

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
					...(toolUses.length > 0 ? { toolUses } : {}),
				},
			};
			history.push(assistantMsg);

			// Collect tool results from this assistant message's tool calls
			if (msg.toolCalls) {
				for (const tc of msg.toolCalls) {
					if (tc.status === "success" || tc.status === "fail") {
						const outputText =
							typeof tc.outputJson === "string"
								? tc.outputJson
								: tc.outputJson != null
									? JSON.stringify(tc.outputJson)
									: "";
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

			// Extract images from contentJson if narratorId is available
			let images: KiroImage[] | undefined;
			if (narratorId && Array.isArray(msg.contentJson)) {
				const imageBlocks = (
					msg.contentJson as Array<{ type: string; imageId?: string; mediaType?: string }>
				).filter((b) => b.type === "image" && b.imageId);
				if (imageBlocks.length > 0) {
					const resolved: KiroImage[] = [];
					for (const block of imageBlocks) {
						const filePath = getImagePath(narratorId, block.imageId ?? "");
						if (filePath) {
							try {
								const base64 = await imageToBase64(filePath);
								const mimeToFormat: Record<string, string> = {
									"image/png": "png",
									"image/jpeg": "jpeg",
									"image/gif": "gif",
									"image/webp": "webp",
								};
								resolved.push({
									format: (mimeToFormat[block.mediaType ?? ""] ?? "png") as KiroImage["format"],
									source: { bytes: base64 },
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
					content: text,
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

	return { history, trailingToolResults: pendingToolResults };
}
