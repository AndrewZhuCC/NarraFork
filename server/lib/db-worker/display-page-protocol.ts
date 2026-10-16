/** Display transport only. No ORM, driver or application bootstrap imports. */
export const DISPLAY_PAGE_LIMITS = {
	maxInputIds: 500,
	maxRows: 10_000,
	maxRawBytes: 16 * 1024 * 1024,
	maxOutputBytes: 4 * 1024 * 1024,
	maxPreparedBytes: 64 * 1024 * 1024,
	maxOutputPreparedBytes: 16 * 1024 * 1024,
	maxContainerSlots: 16_384,
	maxIoBudget: 512 * 1024,
	timeoutMs: 10_000,
	concurrency: 2,
	maxPending: 32,
} as const;

/** Explicit test-only exception: never decode a production DB on the HTTP thread. */
export const DISPLAY_FIXTURE_LIMITS = {
	...DISPLAY_PAGE_LIMITS,
	maxInputIds: 500,
	maxRows: 512,
	maxRawBytes: 256 * 1024,
	maxOutputBytes: 256 * 1024,
	maxPreparedBytes: 4 * 1024 * 1024,
	maxOutputPreparedBytes: 4 * 1024 * 1024,
} as const;

export interface DisplayPageMessagesParams {
	kind: "displayPageMessages";
	/** Already authorized by the caller, not a narrator or a SQL expression from HTTP. */
	messageIds: string[];
	ioBudget: number;
	/** Caller may LOWER the admission ceiling (inline aggregation uses 4MiB). */
	maxRawBytes?: number;
	/** Absolute: queueing/startup and execution spend the same budget. */
	deadlineAt: number;
}

export interface PostgresDisplayPageMessagesParams extends Omit<DisplayPageMessagesParams, "kind"> {
	kind: "displayPageMessagesPostgres";
}

export interface NarratorDisplayCreator {
	id: string;
	username: string;
	avatarColor: string | null;
	avatarImageId: string | null;
}

/** Field-compatible with Drizzle rows; JSON fields contain DISPLAY projections, not model input. */
export interface NarratorDisplayToolCall extends Record<string, unknown> {
	id: string;
	narratorId: string;
	messageId: string;
	toolUseId: string;
	toolName: string;
	inputJson: unknown;
	outputJson: unknown;
	executionAttempt: number;
	createdAt: string;
}

export interface NarratorDisplayMessage extends Record<string, unknown> {
	id: string;
	narratorId: string;
	parentToolUseId: string | null;
	role: string;
	contentJson: unknown;
	createdAt: string;
	toolCalls: NarratorDisplayToolCall[];
	creator: NarratorDisplayCreator | null;
}

export interface DisplayPageMessagesResult {
	messages: NarratorDisplayMessage[];
	/** Storage bytes preflighted BEFORE retrieving any raw JSON; not UTF-16 length. */
	rawBytes: number;
	/** UTF-8 serialized display bytes checked in the worker, BEFORE postMessage. */
	outputBytes: number;
}

export type DisplayPageErrorCode =
	| "DISPLAY_PAGE_INVALID_INPUT"
	| "DISPLAY_PAGE_BUDGET_EXCEEDED"
	| "DISPLAY_PAGE_REFERENCES_CHANGED"
	| "DISPLAY_PAGE_INVALID_JSON"
	| "DISPLAY_PAGE_DEADLINE"
	| "DISPLAY_PAGE_CANCELLED"
	| "DISPLAY_PAGE_READER_UNAVAILABLE";

export class DisplayPageReadError extends Error {
	constructor(
		public readonly code: DisplayPageErrorCode,
		message: string,
	) {
		super(message);
		this.name = "DisplayPageReadError";
	}
}

export function validateDisplayPageRequest(
	input: DisplayPageMessagesParams,
	limits:
		| Readonly<typeof DISPLAY_PAGE_LIMITS>
		| Readonly<typeof DISPLAY_FIXTURE_LIMITS> = DISPLAY_PAGE_LIMITS,
): void {
	if (
		!Array.isArray(input.messageIds) ||
		input.messageIds.length > limits.maxInputIds ||
		!Number.isInteger(input.ioBudget) ||
		input.ioBudget < 1 ||
		input.ioBudget > limits.maxIoBudget ||
		(input.maxRawBytes !== undefined &&
			(!Number.isSafeInteger(input.maxRawBytes) ||
				input.maxRawBytes < 1 ||
				input.maxRawBytes > DISPLAY_PAGE_LIMITS.maxRawBytes)) ||
		!Number.isFinite(input.deadlineAt)
	)
		throw new DisplayPageReadError("DISPLAY_PAGE_INVALID_INPUT", "Invalid display page request");
	for (const id of input.messageIds) {
		if (typeof id !== "string" || id.length < 1 || id.length > 128)
			throw new DisplayPageReadError("DISPLAY_PAGE_INVALID_INPUT", "Invalid display message ID");
	}
	if (new Set(input.messageIds).size !== input.messageIds.length)
		throw new DisplayPageReadError("DISPLAY_PAGE_INVALID_INPUT", "Duplicate display message IDs");
}
