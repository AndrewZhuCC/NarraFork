import { FOLLOW_PARENT_MODEL, type SubagentModelInheritance } from "@shared/model-inheritance";
import type { ReasoningEffort } from "../../lib/agent";
import { modelPoolKeyOf, policySourceIdOf, type RuntimeSourceRow } from "../../lib/runtime-source";
import type { ActiveNarrator } from "../narrator-session-state";

/** Model fields plus the explicit source registration; no parent or template fields. */
export type InheritedModelNarrator = RuntimeSourceRow & {
	model?: string | null;
	reasoningEffort?: ReasoningEffort | null;
};

export interface InheritedModelResolution {
	model: string;
	modelRef: string;
	reasoningEffort?: ReasoningEffort;
	parentReasoningEffort?: ReasoningEffort;
	inheritance?: SubagentModelInheritance;
	/** Revision captured before the asynchronous policy resolution, not after it. */
	settingsRevision?: number;
}

interface InheritedModelDependencies {
	getActive(id: string): ActiveNarrator | undefined;
	activeValues(): Iterable<ActiveNarrator>;
	readNarrator(id: string): Promise<InheritedModelNarrator | undefined>;
	resolve(
		narrator: InheritedModelNarrator,
		actingUserId?: string | null,
		stickyProvider?: string,
	): Promise<InheritedModelResolution>;
	apply(active: ActiveNarrator, result: InheritedModelResolution): void;
	reportError(active: ActiveNarrator, error: unknown): void;
}

/**
 * All asynchronous writes use a per-active generation, never a persisted model write.
 * The request boundary awaits the current generation; an obsolete resolution cannot
 * restore inheritance after a manual selection, revive a dead session, or win a race
 * against a newer source change. The source is whatever the row registers; any
 * session may follow any other. Dependencies keep DB/provider work out of this state
 * machine and make out-of-order completions directly testable.
 */
export function createInheritedModelRuntime(deps: InheritedModelDependencies) {
	function invalidate(active: ActiveNarrator): number {
		active._modelRefreshVersion = (active._modelRefreshVersion ?? 0) + 1;
		active._modelRefreshPending = undefined;
		active._modelRefreshError = undefined;
		return active._modelRefreshVersion;
	}

	function select(active: ActiveNarrator, modelRef: string, sourceNarratorId?: string | null) {
		invalidate(active);
		active._modelSelectionRef = modelRef;
		active._modelSourceNarratorId = modelRef === FOLLOW_PARENT_MODEL ? sourceNarratorId : undefined;
		if (modelRef !== FOLLOW_PARENT_MODEL) {
			active._inheritedReasoningEffort = undefined;
			active._sourceReasoningEffort = undefined;
		}
	}

	function refresh(active: ActiveNarrator): void {
		const version = invalidate(active);
		const isCurrent = () =>
			active.alive &&
			deps.getActive(active.narratorId) === active &&
			active._modelRefreshVersion === version &&
			active._modelSelectionRef === FOLLOW_PARENT_MODEL;
		const pending = (async () => {
			try {
				const narrator = await deps.readNarrator(active.narratorId);
				if (!isCurrent()) return;
				if (!narrator || narrator.model !== FOLLOW_PARENT_MODEL) {
					throw new Error("Inherited model selection changed before resolution");
				}
				const sourceId = policySourceIdOf(narrator);
				const poolKey = modelPoolKeyOf(narrator);
				active._modelSourceNarratorId = sourceId;
				const resolved = await deps.resolve(
					narrator,
					active._currentUserId ?? null,
					active.provider,
				);
				if (!isCurrent()) return;
				// Also guard writes that reached the DB but whose synchronous notification
				// has not arrived yet. These are indexed, single-row projection reads.
				const latest = await deps.readNarrator(active.narratorId);
				if (!isCurrent()) return;
				if (
					!latest ||
					latest.model !== FOLLOW_PARENT_MODEL ||
					policySourceIdOf(latest) !== sourceId ||
					modelPoolKeyOf(latest) !== poolKey
				) {
					throw new Error("Inherited model selection changed during resolution");
				}
				if (resolved.model === FOLLOW_PARENT_MODEL || resolved.modelRef === FOLLOW_PARENT_MODEL) {
					throw new Error("Inherited model resolver returned an unresolved source reference");
				}
				deps.apply(active, resolved);
				active._modelUnavailableWaitCancel?.();
			} catch (error) {
				if (!isCurrent()) return;
				// Fail closed at the next boundary; do not abort an in-flight request.
				active._modelRefreshError = error;
				deps.reportError(active, error);
				active._modelUnavailableWaitCancel?.();
			} finally {
				if (active._modelRefreshVersion === version) active._modelRefreshPending = undefined;
			}
		})();
		active._modelRefreshPending = pending;
	}

	async function settle(active: ActiveNarrator): Promise<void> {
		// A second change can replace the promise while the first is awaited.
		while (active._modelRefreshPending) await active._modelRefreshPending;
		// Fail closed once, then clear: a sticky error would block every later
		// request boundary even when a subsequent refresh (settings revision
		// change, source update, manual reselect) could recover.
		if (active._modelRefreshError) {
			const error = active._modelRefreshError;
			active._modelRefreshError = undefined;
			throw error;
		}
	}

	/** A source's model/effort changed: refresh live followers, including nested ones. */
	function sourceChanged(sourceNarratorId: string): void {
		// No historical-dependent query: work is bounded by the live runtime map.
		// A visited set tolerates malformed cycles.
		const changed = new Set([sourceNarratorId]);
		const followers = [...deps.activeValues()].filter(
			(active) => active.alive && active._modelSelectionRef === FOLLOW_PARENT_MODEL,
		);
		for (const sourceId of changed) {
			for (const active of followers) {
				if (
					changed.has(active.narratorId) ||
					(active._modelSourceNarratorId && active._modelSourceNarratorId !== sourceId)
				) {
					continue;
				}
				changed.add(active.narratorId);
				refresh(active);
			}
		}
	}

	return { select, refresh, settle, sourceChanged };
}
