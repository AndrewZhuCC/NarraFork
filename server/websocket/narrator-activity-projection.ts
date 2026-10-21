import { projectSubagentToolInputSummary } from "@shared/subagent-tool-summary";
import type { NarratorServerMessage } from "./narrator-ws-types";

/**
 * Projection for an `activity` subscription: the lowest level of detail a
 * narrator can be watched at, sized for many cards at once (the dashboard).
 *
 * WHY A PROJECTION AND NOT A CLIENT-SIDE FILTER
 * A full subscription streams every text delta, tool argument chunk and tool
 * output. A dashboard watching a dozen sessions would pay that serialization and
 * bandwidth for one line per tool. This keeps the frames that change what an
 * activity card shows — status, title, and top-level tool lifecycle — and strips
 * those down to their summary fields: the tool's input is reduced to the same
 * whitelisted short keys a subagent card's recent-call rows use, and outputs are
 * never sent.
 *
 * Subagent child events (`parentToolUseId`) are dropped: the card shows the
 * session's own calls, and the child's `Agent` call already represents them.
 */
export function projectNarratorActivityEvent(
	message: NarratorServerMessage,
): NarratorServerMessage | null {
	switch (message.type) {
		case "status_change":
		case "substatus_change":
		case "title_updated":
		case "background_task_count_changed":
			return message;
		case "tool_started": {
			if (message.parentToolUseId) return null;
			const inputSummary = message.inputSummary ?? projectSubagentToolInputSummary(message.input);
			return {
				type: "tool_started",
				narratorId: message.narratorId,
				toolCallId: message.toolCallId,
				toolUseId: message.toolUseId,
				toolName: message.toolName,
				...(inputSummary ? { inputSummary } : {}),
			};
		}
		case "tool_executing":
			return message.parentToolUseId ? null : message;
		case "tool_completed":
			if (message.parentToolUseId) return null;
			return {
				type: "tool_completed",
				narratorId: message.narratorId,
				toolCallId: message.toolCallId,
				toolUseId: message.toolUseId,
				status: message.status,
				...(message.toolName ? { toolName: message.toolName } : {}),
				...(message.durationMs != null ? { durationMs: message.durationMs } : {}),
			};
		case "tool_use_discarded":
			return message.parentToolUseId ? null : message;
		default:
			return null;
	}
}
