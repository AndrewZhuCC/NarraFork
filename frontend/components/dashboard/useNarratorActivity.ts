import { useEffect, useState } from "react";
import { narratorWSManager } from "../../lib/narrator-ws-manager";
import { ACTIVITY_EVENT_TYPES, type ActivityState, applyActivityEvent } from "./dashboard-activity";

const EMPTY_ACTIVITY: ActivityState = new Map();

/**
 * Live activity (status, title, latest top-level tool calls) for a set of
 * narrators, over an `activity` WS subscription: one summary line per tool
 * instead of the full message stream.
 */
export function useNarratorActivity(narratorIds: readonly string[]): ActivityState {
	const [state, setState] = useState<ActivityState>(EMPTY_ACTIVITY);
	const key = [...new Set(narratorIds)].sort().join("\n");

	useEffect(() => {
		const ids = key ? key.split("\n") : [];
		if (ids.length === 0) return;
		const handle = narratorWSManager.subscribe(ids, { kind: "activity" });
		const listener = narratorWSManager.addListener(
			{ narratorIds: ids, types: [...ACTIVITY_EVENT_TYPES], subscriptionId: handle._id },
			(data) => setState((previous) => applyActivityEvent(previous, data)),
		);
		return () => {
			narratorWSManager.removeListener(listener);
			narratorWSManager.unsubscribe(handle);
		};
	}, [key]);

	return state;
}
