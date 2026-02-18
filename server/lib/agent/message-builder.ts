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
 * Follows the kiro.rs build_history() pattern:
 * - Drop the last user message (it's sent as currentMessage)
 * - Merge consecutive user messages into one
 * - user messages always include userInputMessageContext (even if empty)
 * - assistant content uses "." as placeholder when only tool_uses exist
 * - Trailing orphan user messages get an auto-paired assistant "OK"
 * - Tool results from completed tool calls attach to the next user message
 */
export function buildHistory(dbMessages: DbMessage[], model: string): KiroHistoryMessage[] {
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
			const content = Array.isArray(msg.contentJson) ? msg.contentJson : [];
			const textParts = content
				.filter((b: { type: string }) => b.type === "text")
				.map((b: { text: string }) => b.text);
			let text = textParts.join("\n") || msg.contentText || "";

			const toolUses =
				msg.toolCalls
					?.filter((tc) => tc.toolName && tc.toolUseId)
					.map((tc) => ({
						toolUseId: tc.toolUseId,
						name: tc.toolName,
						input: (tc.inputJson as Record<string, unknown>) ?? {},
					})) ?? [];

			// kiro.rs: assistant with only tool_uses and no text uses "." as placeholder
			if (!text && toolUses.length > 0) {
				text = ".";
			}

			const assistantMsg: KiroHistoryAssistantMessage = {
				assistantResponseMessage: {
					content: text,
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
					modelId,
					userInputMessageContext:
						pendingToolResults.length > 0 ? { toolResults: pendingToolResults } : {},
				},
			};
			history.push(userMsg);
			pendingToolResults = [];
		}
	}

	// kiro.rs: trailing orphan user message gets auto-paired assistant "OK"
	if (history.length > 0) {
		const last = history[history.length - 1];
		if ("userInputMessage" in last) {
			history.push({
				assistantResponseMessage: { content: "OK" },
			});
		}
	}

	return history;
}
