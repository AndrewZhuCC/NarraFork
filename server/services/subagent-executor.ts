/** Display-only projection for the tool card receiving a run's result. */

/** Clear the receiving card's transient retry badge after a successful pass. */
export function subagentRetryRecoveredBroadcast(
	transientRetries: number,
	parentNarratorId: string,
	subagentNarratorId: string,
): {
	type: "subagent_status_changed";
	narratorId: string;
	subagentNarratorId: string;
	status: string;
} | null {
	if (transientRetries <= 0) return null;
	return {
		type: "subagent_status_changed",
		narratorId: parentNarratorId,
		subagentNarratorId,
		status: "working",
	};
}
