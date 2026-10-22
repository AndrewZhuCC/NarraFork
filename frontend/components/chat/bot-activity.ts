/**
 * bot-activity.ts — What a bot's chat header says about the work behind the room.
 *
 * The room shows only what the bot sent, so the header is the reader's one window
 * onto a turn in progress. Derived from the narrator's status + substatus, the same
 * pair the recent-tabs list reads, and with the same exception: a run parked until a
 * model or its quota recovers is `waiting`, but there is nothing for a person to decide.
 */

export type BotActivity = "thinking" | "decision" | "model" | "error" | null;

export function botActivity(
	status: string | undefined,
	substatus: readonly string[] | undefined,
): BotActivity {
	const tags = substatus ?? [];
	if (status === "working") return "thinking";
	if (status === "waiting") {
		return tags.includes("model_unavailable") || tags.includes("quota_exhausted")
			? "model"
			: "decision";
	}
	// A failed turn otherwise looks like silence: the person's message just gets no answer.
	if (status === "idle" && (tags.includes("error") || tags.includes("timeout"))) return "error";
	return null;
}
