export type NarratorLoadingPhase =
	| "switch"
	| "detail-start"
	| "detail-ready"
	| "tail-start"
	| "tail-ready"
	| "cache-restored"
	| "layout-ready"
	| "first-paint";

export interface NarratorLoadingToken {
	narratorId: string;
	generation: number;
}
export interface NarratorLoadingObservation {
	phase: NarratorLoadingPhase;
	elapsedMs: number;
	rows?: number;
	bytes?: number;
}
interface Trace {
	token: NarratorLoadingToken;
	startedAt: number;
	observations: NarratorLoadingObservation[];
}

/** Bounded local diagnostics; never accepts message text, Git paths or credentials. */
export class NarratorLoadingMetrics {
	private readonly traces = new Map<string, Trace>();
	private generation = 0;
	constructor(
		private readonly now: () => number = () => performance.now(),
		private readonly maxTraces = 32,
	) {}

	start(narratorId: string): NarratorLoadingToken {
		const token = { narratorId, generation: ++this.generation };
		this.traces.delete(narratorId);
		this.traces.set(narratorId, {
			token,
			startedAt: this.now(),
			observations: [{ phase: "switch", elapsedMs: 0 }],
		});
		while (this.traces.size > this.maxTraces) {
			const oldest = this.traces.keys().next().value;
			if (oldest === undefined) break;
			this.traces.delete(oldest);
		}
		return token;
	}

	mark(
		token: NarratorLoadingToken,
		phase: NarratorLoadingPhase,
		counts: { rows?: number; bytes?: number } = {},
	): void {
		const trace = this.traces.get(token.narratorId);
		if (!trace || trace.token.generation !== token.generation || trace.observations.length >= 16)
			return;
		if (trace.observations.some((observation) => observation.phase === phase)) return;
		const validCount = (value: number | undefined) =>
			value != null && Number.isSafeInteger(value) && value >= 0 ? value : undefined;
		trace.observations.push({
			phase,
			elapsedMs: Math.max(0, this.now() - trace.startedAt),
			rows: validCount(counts.rows),
			bytes: validCount(counts.bytes),
		});
	}

	current(narratorId: string): NarratorLoadingToken | undefined {
		return this.traces.get(narratorId)?.token;
	}

	snapshot(): Array<{ narratorId: string; observations: NarratorLoadingObservation[] }> {
		return [...this.traces.values()].map((trace) => ({
			narratorId: trace.token.narratorId,
			observations: trace.observations.map((observation) => ({ ...observation })),
		}));
	}

	clear(): void {
		this.traces.clear();
	}
}

export const narratorLoadingMetrics = new NarratorLoadingMetrics();
