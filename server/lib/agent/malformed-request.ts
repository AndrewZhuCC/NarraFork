/**
 * Detection and structural summary for upstream "malformed request" rejections.
 *
 * Some streaming upstreams answer a structurally invalid body with an
 * opaque envelope that carries no field-level detail:
 *
 * ```json
 * {"__type":"...#ValidationException",
 *  "message":"Improperly formed request.","reason":"REQUEST_BODY_INVALID"}
 * ```
 *
 * Nothing in that response says *which* part of the request was rejected, so the loop pins
 * the attempt's request recording (the exact bytes sent) and attaches the bounded summary
 * built here, which usually points straight at the offender (orphaned toolResults, empty
 * content, malformed images, reasoning blocks without a signature). The summary only holds
 * shapes and counts, never conversation content.
 */

/** Lowercased fragments that uniquely identify an upstream malformed-body rejection. */
const MALFORMED_REQUEST_MARKERS = ["request_body_invalid", "improperly formed request"];

const MAX_MATCH_TEXT_CHARS = 32_768;
// Matches the recorder metadata array bound so a summary never reads as truncated evidence.
const MAX_SUMMARY_LIST_ITEMS = 16;
const MAX_ROLE_SEQUENCE_CHARS = 400;

function isRecord(value: unknown): value is Record<string, unknown> {
	return value != null && typeof value === "object" && !Array.isArray(value);
}

/**
 * Collect the error text worth matching against, without serializing an entire
 * provider payload: only the message-ish fields of the error, its cause, and any
 * attached diagnostics.
 */
function collectErrorText(value: unknown, depth = 0): string {
	if (value == null || depth > 3) return "";
	if (typeof value === "string") return value;
	if (typeof value === "number" || typeof value === "boolean") return String(value);
	if (value instanceof Error) {
		return [
			value.message,
			collectErrorText((value as { cause?: unknown }).cause, depth + 1),
			collectErrorText((value as { diagnostics?: unknown }).diagnostics, depth + 1),
			collectErrorText((value as { responseSnippet?: unknown }).responseSnippet, depth + 1),
		]
			.filter(Boolean)
			.join(" ");
	}
	if (!isRecord(value)) return "";
	const fields = [
		"message",
		"reason",
		"code",
		"errorType",
		"__type",
		"responseSnippet",
		"bodyText",
		"error",
		"cause",
		"diagnostics",
	];
	const parts: string[] = [];
	for (const field of fields) {
		const part = collectErrorText(value[field], depth + 1);
		if (part) parts.push(part);
	}
	return parts.join(" ");
}

/**
 * Whether an error (or raw response text) is an upstream malformed-request-body
 * rejection. Accepts anything: a thrown Error, an invalidState-ish record, or the
 * raw response string.
 */
export function isMalformedRequestBodyError(value: unknown): boolean {
	const text = collectErrorText(value).slice(0, MAX_MATCH_TEXT_CHARS).toLowerCase();
	if (!text) return false;
	return MALFORMED_REQUEST_MARKERS.some((marker) => text.includes(marker));
}

export interface MalformedRequestBodySummary {
	bodyChars?: number;
	topLevelKeys?: string[];
	modelId?: string;
	historyLength?: number;
	/** Role sequence of history + current message, e.g. "UAUAU…C". */
	roleSequence?: string;
	/** History indexes whose `content` is an empty string. */
	emptyContentIndexes?: number[];
	toolUseCount?: number;
	toolResultCount?: number;
	unmatchedToolUseIds?: string[];
	unmatchedToolResultIds?: string[];
	duplicateToolUseIds?: string[];
	imageCount?: number;
	imageBase64Chars?: number;
	emptyImageCount?: number;
	reasoningBlocks?: number;
	reasoningWithoutSignature?: number;
	currentMessageToolResults?: number;
	currentMessageContentChars?: number;
	toolSpecCount?: number;
	notes?: string[];
}

function limitList<T>(values: Iterable<T>): T[] {
	const out: T[] = [];
	for (const value of values) {
		if (out.length >= MAX_SUMMARY_LIST_ITEMS) break;
		out.push(value);
	}
	return out;
}

/**
 * Build a bounded structural summary of a chat request body. Purely descriptive —
 * it never mutates the body and never copies conversation content, only shapes/counts.
 *
 * Recognizes the common chat shape (`messages[]` with `role` plus string or
 * content-block payloads, Anthropic-style `tool_use`/`tool_result` blocks and
 * OpenAI-style `tool_calls`/`tool_call_id`), and degrades to a top-level key
 * listing for anything else.
 */
export function summarizeRequestBody(body: unknown): MalformedRequestBodySummary | undefined {
	if (!isRecord(body)) return undefined;
	const summary: MalformedRequestBodySummary = {};
	const notes: string[] = [];
	summary.topLevelKeys = limitList(Object.keys(body));
	if (typeof body.model === "string") summary.modelId = body.model;
	const bodyTools = Array.isArray(body.tools) ? body.tools : [];
	if (bodyTools.length > 0) summary.toolSpecCount = bodyTools.length;

	const messages = Array.isArray(body.messages) ? body.messages : undefined;
	if (!messages) {
		notes.push("body has no messages array — not a standard chat request");
		summary.notes = notes;
		return summary;
	}
	summary.historyLength = messages.length;

	const toolUseIds: string[] = [];
	const toolResultIds = new Set<string>();
	const seenToolUseIds = new Set<string>();
	const duplicateToolUseIds = new Set<string>();
	const emptyContentIndexes: number[] = [];
	let roles = "";

	const countImageBlock = (block: Record<string, unknown>): void => {
		summary.imageCount = (summary.imageCount ?? 0) + 1;
		const source = isRecord(block.source) ? block.source : undefined;
		const data = typeof source?.data === "string" ? source.data : "";
		summary.imageBase64Chars = (summary.imageBase64Chars ?? 0) + data.length;
		if (!data) summary.emptyImageCount = (summary.emptyImageCount ?? 0) + 1;
	};

	messages.forEach((entry, index) => {
		if (!isRecord(entry)) {
			roles += "?";
			return;
		}
		const role = typeof entry.role === "string" ? entry.role : "?";
		roles +=
			role === "user"
				? "U"
				: role === "assistant"
					? "A"
					: role === "tool"
						? "T"
						: role === "system"
							? "S"
							: "?";
		if (entry.content === "") emptyContentIndexes.push(index);

		const blocks = Array.isArray(entry.content) ? entry.content : [];
		for (const block of blocks) {
			if (!isRecord(block)) continue;
			if (block.type === "tool_use" && typeof block.id === "string") {
				toolUseIds.push(block.id);
				if (seenToolUseIds.has(block.id)) duplicateToolUseIds.add(block.id);
				seenToolUseIds.add(block.id);
			}
			if (block.type === "tool_result" && typeof block.tool_use_id === "string") {
				toolResultIds.add(block.tool_use_id);
			}
			if (block.type === "image") countImageBlock(block);
			if (block.type === "thinking" || block.type === "redacted_thinking") {
				summary.reasoningBlocks = (summary.reasoningBlocks ?? 0) + 1;
				if (typeof block.signature !== "string" || block.signature.length === 0) {
					summary.reasoningWithoutSignature = (summary.reasoningWithoutSignature ?? 0) + 1;
				}
			}
		}

		// OpenAI-style tool calls / results.
		const toolCalls = Array.isArray(entry.tool_calls) ? entry.tool_calls : [];
		for (const call of toolCalls) {
			if (!isRecord(call) || typeof call.id !== "string") continue;
			toolUseIds.push(call.id);
			if (seenToolUseIds.has(call.id)) duplicateToolUseIds.add(call.id);
			seenToolUseIds.add(call.id);
		}
		if (typeof entry.tool_call_id === "string") toolResultIds.add(entry.tool_call_id);

		// The last message doubles as the "current" one for the summary.
		if (index === messages.length - 1) {
			summary.currentMessageContentChars =
				typeof entry.content === "string" ? entry.content.length : 0;
			summary.currentMessageToolResults = blocks.filter(
				(block) => isRecord(block) && block.type === "tool_result",
			).length;
		}
	});

	summary.roleSequence =
		roles.length > MAX_ROLE_SEQUENCE_CHARS
			? `${roles.slice(0, MAX_ROLE_SEQUENCE_CHARS)}…`
			: roles || undefined;
	summary.toolUseCount = toolUseIds.length;
	summary.toolResultCount = toolResultIds.size;
	if (emptyContentIndexes.length > 0) {
		summary.emptyContentIndexes = limitList(emptyContentIndexes);
	}
	if (duplicateToolUseIds.size > 0) {
		summary.duplicateToolUseIds = limitList(duplicateToolUseIds);
		notes.push("duplicate toolUseId present in history");
	}

	const unmatchedUses = toolUseIds.filter((id) => !toolResultIds.has(id));
	if (unmatchedUses.length > 0) {
		summary.unmatchedToolUseIds = limitList(new Set(unmatchedUses));
		notes.push("toolUse without a matching toolResult");
	}
	const uniqueUses = new Set(toolUseIds);
	const unmatchedResults = [...toolResultIds].filter((id) => !uniqueUses.has(id));
	if (unmatchedResults.length > 0) {
		summary.unmatchedToolResultIds = limitList(unmatchedResults);
		notes.push("toolResult without a matching toolUse (orphaned result)");
	}
	if (summary.emptyImageCount) notes.push("image with empty source data present");

	if (notes.length > 0) summary.notes = limitList(notes);
	return summary;
}
