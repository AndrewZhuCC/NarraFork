/**
 * history-build.ts — rows → one adapter's request history.
 *
 * The single way a model history is built, whoever holds the rows: a host rebuilding a
 * narrator's conversation from the database (`buildHistory` in `index.ts`), or a loop
 * projecting its in-memory transcript (`loop-transcript.ts`). The model-only views of
 * the rows are applied here, on copies, then the adapter's builder runs the two-stage
 * projection (neutral turns, then its wire format).
 */

import { projectFileReferencesForModel } from "./file-reference-projection";
import type { BuiltHistory, DbMessage, ProviderAdapter } from "./provider";
import { projectAskUserQuestionMessagesForModel } from "./question-model-input";
import { projectMessageSenderForModel } from "./sender-projection";
import { stripCitationMarkersForModel } from "./strip-citation-markers";
import { stripPlanBodyForModel } from "./strip-plan-body";
import {
	projectToolLoadingHistory,
	resolveLoadableToolDefinition,
	type ToolLoadingState,
	type ToolLoadingStrategy,
} from "./tool-loading";
import { uniquifyDbMessageToolUseIds } from "./tool-use-id-dedup";

export interface AdapterHistoryOptions {
	narratorId?: string;
	/**
	 * Loading state the request's `tools` array is built from, and the strategy
	 * resolved for this model. Tool loads inside the history are rendered for it.
	 */
	toolLoading?: { state: ToolLoadingState; strategy: ToolLoadingStrategy };
}

/** Build `adapter`'s history for `model` (the bare id it resolved) from `rows`. */
export function buildAdapterHistory(
	adapter: Pick<ProviderAdapter, "buildHistory">,
	rows: readonly DbMessage[],
	target: { provider: string; model: string },
	options: AdapterHistoryOptions = {},
): Promise<BuiltHistory> {
	// Replace file-based ExitPlanMode plan bodies with a short path reference so
	// the model history does not carry the full plan text on every rebuild. The
	// persisted DB rows keep the full plan for the UI; this only mutates the
	// in-memory copy passed to the provider adapter.
	const modelMessages = stripPlanBodyForModel(projectAskUserQuestionMessagesForModel(rows));
	// Historical rows may still embed provider-internal citation markers. Replaying
	// them teaches the model the markers are part of its output format, so it keeps
	// producing them; strip on the in-memory copy only.
	// Expand accepted file bytes after prose cleanup: citation-like strings in
	// a source file are data and must not be rewritten by the citation sanitizer.
	const cleanedMessages = projectFileReferencesForModel(
		stripCitationMarkersForModel(modelMessages),
	);
	// Some providers mint the same tool_use id for every call (e.g. "call_go_0"),
	// which only breaks once several turns accumulate: the replayed history then
	// carries duplicate ids and the API rejects the request with 400. Rename the
	// later collisions in this in-memory copy — DB rows keep the original ids.
	// Attribution is a model-only projection, after accepted file material. Roles and
	// stored rows stay unchanged; every provider receives the same sender format.
	const uniqueMessages = uniquifyDbMessageToolUseIds(
		cleanedMessages.map(projectMessageSenderForModel),
		{ narratorId: options.narratorId, provider: target.provider, model: target.model },
	);
	// Last, so builders see exactly these row objects (changes are keyed by row).
	const projectedMessages = options.toolLoading
		? projectToolLoadingHistory(
				uniqueMessages,
				options.toolLoading.state,
				options.toolLoading.strategy,
				resolveLoadableToolDefinition,
			)
		: uniqueMessages;
	return adapter.buildHistory(projectedMessages, target.model, options.narratorId);
}

/** The last top-level row the model reads, and whether it is an answered tool step. */
export function historyTail(rows: readonly DbMessage[]): { toolResults: boolean; input: boolean } {
	for (let index = rows.length - 1; index >= 0; index--) {
		const row = rows[index];
		if (row.parentToolUseId || row.role === "disp" || row.role === "system") continue;
		if (row.role !== "assistant") return { toolResults: false, input: true };
		const toolResults = (row.toolCalls ?? []).some(
			(call) => call.status === "success" || call.status === "fail",
		);
		return { toolResults, input: toolResults };
	}
	return { toolResults: false, input: false };
}
