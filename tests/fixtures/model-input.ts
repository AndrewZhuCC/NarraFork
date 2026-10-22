/**
 * What one request gives the model, read without depending on a provider's wire shape.
 *
 * Built histories are complete: a persisted input, its trailing notices and unanswered
 * tool results all live in `history`, and `userText` only carries text no row holds.
 * Assertions about "what the model was sent" therefore read both.
 */

/** Every string in a provider history, joined: model text without JSON escaping. */
export function historyStrings(history: unknown): string {
	const strings: string[] = [];
	const visit = (value: unknown): void => {
		if (typeof value === "string") strings.push(value);
		else if (Array.isArray(value)) for (const entry of value) visit(entry);
		else if (value && typeof value === "object")
			for (const entry of Object.values(value)) visit(entry);
	};
	visit(history);
	return strings.join("\n");
}

/**
 * The history a request sends. An executor request carries `{ rows, built }`: the host's
 * build when it made one, otherwise the rows the loop projects; a chat request carries
 * the provider history itself.
 */
export function sentHistory(history: unknown): unknown {
	if (!history || typeof history !== "object" || Array.isArray(history) || !("rows" in history))
		return history;
	const { rows, built } = history as { rows: unknown; built?: unknown };
	return built ?? rows;
}

/** The history's strings followed by the current text of one executor or chat request. */
export function modelInputText(
	request: { history?: unknown; userText?: string } | undefined,
): string {
	return `${historyStrings(sentHistory(request?.history))}\n${request?.userText ?? ""}`;
}

/** How many times `needle` reaches the model in this request. */
export function modelInputCount(
	request: { history?: unknown; userText?: string } | undefined,
	needle: string,
): number {
	return modelInputText(request).split(needle).length - 1;
}

/**
 * A stand-in for a provider history builder in tests that do not exercise a real one:
 * every top-level model row becomes one `{ role, content }` message, in order, so a
 * persisted input still reaches the "model" the way it does with real builders.
 */
export async function rowsAsHistory(
	messages: ReadonlyArray<{
		role: string;
		contentText?: string | null;
		parentToolUseId?: string | null;
	}>,
): Promise<{ history: unknown[] }> {
	return {
		history: messages
			.filter(
				(message) =>
					!message.parentToolUseId &&
					(message.role === "user" || message.role === "assistant" || message.role === "sys"),
			)
			.map((message) => ({ role: message.role, content: message.contentText ?? "" })),
	};
}

/**
 * The input a request ends with: its current text when it has one, otherwise the final
 * input row of a `rowsAsHistory` history (what the old builders sent as the current turn).
 */
export function requestInput(params: { history: unknown[]; content: string }): string {
	if (params.content) return params.content;
	const last = params.history.at(-1) as { role?: string; content?: unknown } | undefined;
	return last && last.role !== "assistant" && typeof last.content === "string" ? last.content : "";
}
