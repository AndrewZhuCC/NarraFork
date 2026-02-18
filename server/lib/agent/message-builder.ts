import type {
	KiroHistoryAssistantMessage,
	KiroHistoryMessage,
	KiroHistoryUserMessage,
	KiroToolResult,
} from "../kiro/types";
import { resolveModel } from "./resolve-model";

interface DbMessage {
	id: string;
	role: "user" | "assistant" | "system";
	contentJson: unknown;
	contentText: string | null;
	parentToolUseId: string | null;
	sdkMessageUuid: string | null;
	toolCalls?: DbToolCall[];
}

interface DbToolCall {
	toolUseId: string;
	toolName: string;
	inputJson: unknown;
	outputJson: unknown;
	status: string;
}

/**
 * Convert DB narrator messages into KiroHistoryMessage[] for the Kiro API.
 *
 * Strategy:
 * - Filter out system messages and child messages (parentToolUseId != null)
 * - user messages → KiroHistoryUserMessage
 * - assistant messages → KiroHistoryAssistantMessage (with toolUses extracted from contentJson)
 * - Tool results from completed tool calls are attached to the next user message's
 *   userInputMessageContext.toolResults
 */
export function buildHistory(dbMessages: DbMessage[], model: string): KiroHistoryMessage[] {
	// Only top-level user/assistant messages
	const topLevel = dbMessages.filter(
		(m) => !m.parentToolUseId && (m.role === "user" || m.role === "assistant"),
	);

	// Drop the last user message — it's the current message being sent via currentMessage
	if (topLevel.length > 0 && topLevel[topLevel.length - 1].role === "user") {
		topLevel.pop();
	}

	// Merge consecutive same-role messages (e.g. multiple user messages without assistant reply)
	const merged: DbMessage[] = [];
	for (const msg of topLevel) {
		const prev = merged[merged.length - 1];
		if (prev && prev.role === msg.role && msg.role === "user") {
			// Merge consecutive user messages into one
			const prevText = prev.contentText || "";
			const curText = msg.contentText || "";
			prev.contentText = prevText ? `${prevText}\n\n${curText}` : curText;
		} else {
			merged.push({ ...msg });
		}
	}

	const history: KiroHistoryMessage[] = [];
	let pendingToolResults: KiroToolResult[] = [];

	for (const msg of merged) {
		if (msg.role === "assistant") {
			const content = Array.isArray(msg.contentJson) ? msg.contentJson : [];
			const textParts = content
				.filter((b: { type: string }) => b.type === "text")
				.map((b: { text: string }) => b.text);
			const text = textParts.join("\n") || msg.contentText || "";

			const toolUses =
				msg.toolCalls
					?.filter((tc) => tc.toolName && tc.toolUseId)
					.map((tc) => ({
						toolUseId: tc.toolUseId,
						name: tc.toolName,
						input: (tc.inputJson as Record<string, unknown>) ?? {},
					})) ?? [];

			const assistantMsg: KiroHistoryAssistantMessage = {
				assistantResponseMessage: {
					content: text,
					messageId: msg.sdkMessageUuid ?? msg.id,
					...(toolUses.length > 0 ? { toolUses } : {}),
				},
			};
			history.push(assistantMsg);

			// Collect tool results from this assistant message's tool calls
			if (msg.toolCalls) {
				for (const tc of msg.toolCalls) {
					if (tc.status === "completed" || tc.status === "failed") {
						const outputText =
							typeof tc.outputJson === "string"
								? tc.outputJson
								: tc.outputJson != null
									? JSON.stringify(tc.outputJson)
									: "";
						pendingToolResults.push({
							toolUseId: tc.toolUseId,
							content: [{ text: outputText }],
							status: tc.status === "failed" ? "error" : "success",
							isError: tc.status === "failed",
						});
					}
				}
			}
		} else if (msg.role === "user") {
			const text = msg.contentText || "";
			const userMsg: KiroHistoryUserMessage = {
				userInputMessage: {
					content: text,
					modelId: resolveModel(model),
					...(pendingToolResults.length > 0
						? { userInputMessageContext: { toolResults: pendingToolResults } }
						: {}),
				},
			};
			history.push(userMsg);
			pendingToolResults = [];
		}
	}

	return history;
}
