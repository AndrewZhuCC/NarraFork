export const CACHE_DROP_MIN_TOKENS = 8192;
export const CACHE_DROP_TTL_MS = 7 * 24 * 60 * 60 * 1000;
const MAX_NARRATORS = 256;

/** Only token statistics and recording identities; request bodies belong to the recorder. */
export interface CacheDropSample {
	requestId: string;
	narratorId: string;
	provider: string;
	model: string;
	kind: string;
	conversationId?: string;
	contextEpoch?: number;
	createdAt: string;
	startedAt: number;
	inputTokens: number;
	cachedInputTokens?: number;
	errorMessage?: string | null;
}

export interface CacheDropIncident {
	role: "trigger" | "after";
	triggerRequestId: string;
	previousRequestId: string;
	beforeRequestIds: string[];
	previousCachedInputTokens: number;
	cachedInputTokens: number;
}

type Baseline = Pick<
	CacheDropSample,
	"requestId" | "provider" | "model" | "conversationId" | "contextEpoch"
> & { cached: number };
interface NarratorState {
	touchedAt: number;
	latestStartedAt: number;
	resetAt: number;
	baseline?: Baseline;
	rolling: string[];
	pending?: CacheDropIncident;
}

function sameIdentity(a: Baseline, b: CacheDropSample): boolean {
	return (
		a.provider === b.provider &&
		a.model === b.model &&
		a.conversationId === b.conversationId &&
		a.contextEpoch === b.contextEpoch
	);
}

/** Metadata-only replacement for the retired JSON dump store. */
export class CacheDropDetector {
	private readonly states = new Map<string, NarratorState>();

	constructor(private readonly now: () => number = Date.now) {}

	private state(narratorId: string): NarratorState {
		const state = this.states.get(narratorId) ?? {
			touchedAt: this.now(),
			latestStartedAt: -Infinity,
			resetAt: -Infinity,
			rolling: [],
		};
		state.touchedAt = this.now();
		this.states.delete(narratorId);
		this.states.set(narratorId, state);
		if (this.states.size > MAX_NARRATORS) {
			const oldest = this.states.keys().next().value;
			if (oldest !== undefined) this.states.delete(oldest);
		}
		return state;
	}

	record(sample: CacheDropSample): CacheDropIncident[] {
		// Internal callers have small IDs; reject pathological metadata rather than retain it.
		if (
			!sample.narratorId ||
			[
				sample.narratorId,
				sample.requestId,
				sample.provider,
				sample.model,
				sample.conversationId,
			].some((value) => value !== undefined && value.length > 1024)
		)
			return [];
		this.maintain();
		const state = this.state(sample.narratorId);
		if (state.rolling.includes(sample.requestId)) return [];
		const incidents: CacheDropIncident[] = [];
		if (state.pending) {
			incidents.push({ ...state.pending, role: "after" });
			state.pending = undefined;
		}
		const previous = state.baseline;
		const eligible =
			Number.isFinite(sample.startedAt) &&
			sample.startedAt >= this.now() - CACHE_DROP_TTL_MS &&
			sample.startedAt > state.resetAt &&
			sample.startedAt >= state.latestStartedAt;
		if (eligible) {
			state.latestStartedAt = sample.startedAt;
			if (previous && !sameIdentity(previous, sample)) state.baseline = undefined;
		}
		const valid =
			eligible &&
			sample.errorMessage == null &&
			Number.isFinite(sample.cachedInputTokens) &&
			(sample.cachedInputTokens ?? -1) >= 0 &&
			Number.isFinite(sample.inputTokens) &&
			sample.inputTokens >= 0;
		if (
			valid &&
			previous &&
			sameIdentity(previous, sample) &&
			previous.cached >= CACHE_DROP_MIN_TOKENS &&
			previous.cached - (sample.cachedInputTokens ?? 0) >= CACHE_DROP_MIN_TOKENS
		) {
			const incident: CacheDropIncident = {
				role: "trigger",
				triggerRequestId: sample.requestId,
				previousRequestId: previous.requestId,
				beforeRequestIds: [...new Set([...state.rolling, previous.requestId])],
				previousCachedInputTokens: previous.cached,
				cachedInputTokens: sample.cachedInputTokens as number,
			};
			incidents.push(incident);
			state.pending = incident;
		}
		if (valid) {
			state.baseline = {
				requestId: sample.requestId,
				provider: sample.provider,
				model: sample.model,
				conversationId: sample.conversationId,
				contextEpoch: sample.contextEpoch,
				cached: sample.cachedInputTokens as number,
			};
		}
		state.rolling = [...state.rolling.slice(-1), sample.requestId];
		return incidents;
	}

	/** Explicit history replacement fences out an already-started old request. */
	resetNarrator(narratorId: string): void {
		if (!narratorId || narratorId.length > 1024) return;
		const state = this.state(narratorId);
		state.baseline = undefined;
		state.resetAt = this.now();
	}

	clearRolling(): void {
		this.states.clear();
	}

	maintain(): void {
		const oldest = this.now() - CACHE_DROP_TTL_MS;
		for (const [narratorId, state] of this.states) {
			if (state.touchedAt <= oldest) this.states.delete(narratorId);
		}
	}
}
