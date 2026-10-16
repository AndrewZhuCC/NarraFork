import { hotSafe } from "@server/lib/hot-safe";

/**
 * Blocking forms: a `render` with `wait: true` holds its tool call open until the reader
 * submits the form drawn in the chat, closes it, or the wait runs out. The answer is the
 * tool result, so the agent never mistakes it for the reader having said something.
 *
 * Waits live in memory. A server restart interrupts the tool call like any other, and the
 * card then sends a later submit as an ordinary chat message.
 */

export const GEN_UI_FORM_TIMEOUT_MS = 30 * 60_000;

/** What leaves the frame: the values bound controls hold. */
export type GenUiFormValue = string | number | boolean | null | GenUiFormValue[];
export type GenUiFormFields = Record<string, GenUiFormValue>;

export type GenUiFormOutcome =
	| { status: "submitted"; message: string; fields: GenUiFormFields }
	| { status: "closed" }
	| { status: "timeout" }
	| { status: "aborted" };

interface Waiting {
	narratorId: string;
	settle(outcome: GenUiFormOutcome): void;
}

const waits = hotSafe("narrafork:genUiFormWaits", () => new Map<string, Waiting>());

const FIELD_NAME = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;
const LIMITS = { fields: 200, items: 200, text: 2000, message: 2000, bytes: 32 * 1024 };

function readValue(value: unknown, depth: number): GenUiFormValue | undefined {
	if (value === null) return null;
	if (typeof value === "string") return value.slice(0, LIMITS.text);
	if (typeof value === "number") return Number.isFinite(value) ? value : null;
	if (typeof value === "boolean") return value;
	if (Array.isArray(value) && depth === 0) {
		const items: GenUiFormValue[] = [];
		for (const item of value.slice(0, LIMITS.items)) {
			const read = readValue(item, depth + 1);
			if (read === undefined) return undefined;
			items.push(read);
		}
		return items;
	}
	return undefined;
}

/**
 * The submitted fields as plain data, or null when the payload is not a form answer.
 * The reader's browser sent it, so nothing about its shape is trusted.
 */
export function readGenUiFormFields(input: unknown): GenUiFormFields | null {
	if (!input || typeof input !== "object" || Array.isArray(input)) return null;
	const names = Object.keys(input);
	if (names.length > LIMITS.fields) return null;
	const fields: GenUiFormFields = {};
	for (const name of names) {
		if (!FIELD_NAME.test(name)) return null;
		const value = readValue((input as Record<string, unknown>)[name], 0);
		if (value === undefined) return null;
		fields[name] = value;
	}
	return JSON.stringify(fields).length > LIMITS.bytes ? null : fields;
}

/** Wait for the form drawn by this tool call. Resolves; never rejects. */
export function waitForGenUiForm(options: {
	narratorId: string;
	toolUseId: string;
	signal: AbortSignal;
	timeoutMs?: number;
}): Promise<GenUiFormOutcome> {
	const { narratorId, toolUseId, signal } = options;
	if (signal.aborted) return Promise.resolve({ status: "aborted" });
	return new Promise((resolve) => {
		let timer: ReturnType<typeof setTimeout> | undefined;
		const onAbort = () => settle({ status: "aborted" });
		const settle = (outcome: GenUiFormOutcome) => {
			if (waits.get(toolUseId) !== entry) return;
			waits.delete(toolUseId);
			clearTimeout(timer);
			signal.removeEventListener("abort", onAbort);
			resolve(outcome);
		};
		const entry: Waiting = { narratorId, settle };
		// A replayed call with the same id replaces the stale wait instead of sharing it.
		waits.get(toolUseId)?.settle({ status: "aborted" });
		waits.set(toolUseId, entry);
		timer = setTimeout(
			() => settle({ status: "timeout" }),
			options.timeoutMs ?? GEN_UI_FORM_TIMEOUT_MS,
		);
		signal.addEventListener("abort", onAbort, { once: true });
	});
}

/** Whether this tool call is waiting for its form now. */
export function isGenUiFormWaiting(narratorId: string, toolUseId: string): boolean {
	return waits.get(toolUseId)?.narratorId === narratorId;
}

/**
 * Hand the reader's answer to the waiting tool call. False when nothing is waiting: the
 * call already ended, or the id belongs to another conversation.
 */
export function answerGenUiForm(
	narratorId: string,
	toolUseId: string,
	answer: { status: "submitted"; message: string; fields: GenUiFormFields } | { status: "closed" },
): boolean {
	const entry = waits.get(toolUseId);
	if (!entry || entry.narratorId !== narratorId) return false;
	entry.settle(
		answer.status === "submitted"
			? { ...answer, message: answer.message.trim().slice(0, LIMITS.message) }
			: answer,
	);
	return true;
}

/** The tool result for a finished wait. */
export function describeGenUiFormOutcome(outcome: GenUiFormOutcome): {
	output: string;
	isError?: boolean;
} {
	if (outcome.status === "submitted") {
		const names = Object.keys(outcome.fields);
		return {
			output: [
				outcome.message
					? `The user submitted the form ("${outcome.message}").`
					: "The user submitted the form.",
				names.length
					? `Values:\n${JSON.stringify(outcome.fields, null, 1)}`
					: "It has no bound fields, so no values came with it.",
			].join("\n"),
		};
	}
	if (outcome.status === "closed")
		return {
			output:
				"The user closed the form without submitting it. Ask in words or carry on without it; do not render the same form again unless they ask.",
		};
	if (outcome.status === "timeout")
		return {
			output: `The user did not submit the form within ${GEN_UI_FORM_TIMEOUT_MS / 60_000} minutes; the wait ended. The form stays in the chat, and if they submit it later it arrives as a chat message.`,
		};
	return { output: "Stopped while waiting for the form.", isError: true };
}
