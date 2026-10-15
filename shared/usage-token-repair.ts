/** Version of the disjoint ordinary-input/cache-read/cache-write accounting contract. */
export const TOKEN_ACCOUNTING_VERSION = 1;
export const TOKEN_REPAIR_PAGE_SIZE = 50;
/** Legacy candidate-store limit only. Full v2 scans are not capped by record count. */
export const TOKEN_REPAIR_MAX_RECORDS = 10_000;
export const TOKEN_REPAIR_MAX_DUMP_BYTES = 32 * 1024 * 1024;
export const TOKEN_REPAIR_TIMEOUT_MS = 30 * 60 * 1000;

export interface TokenRepairCounters {
	inputTokens: number;
	cachedInputTokens: number;
	cacheCreationInputTokens: number;
	cacheCreation5mTokens: number;
	cacheCreation1hTokens: number;
}
export type TokenRepairClassification =
	| "repairable"
	| "correct"
	| "insufficient"
	| "ambiguous"
	| "conflict";
export type TokenRepairProtocol = "openai" | "anthropic" | "gemini" | "unknown";
export const TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES = 32;
export const TOKEN_REPAIR_PROTOCOL_CONFIG_MAX_BYTES = 16 * 1024;
/** Exact identity, scoped to one immutable analysis job, never global model configuration. */
export interface TokenRepairProtocolOverride {
	provider: string | null;
	model: string;
	protocol: Exclude<TokenRepairProtocol, "unknown">;
}
export function tokenRepairProtocolFor(
	overrides: readonly TokenRepairProtocolOverride[] | undefined,
	provider: string | null,
	model: string | null,
): TokenRepairProtocolOverride["protocol"] | undefined {
	return overrides?.find((entry) => entry.provider === provider && entry.model === model)?.protocol;
}
export interface TokenRepairEvidence {
	classification: TokenRepairClassification;
	protocol: TokenRepairProtocol;
	/** Explicit administrator selection frozen on the analysis job, when one was supplied. */
	manualProtocol?: TokenRepairProtocolOverride["protocol"];
	reason: string;
	evidence: string;
	before: TokenRepairCounters;
	after: TokenRepairCounters | null;
}
export type TokenRepairJobStatus =
	| "analyzing"
	| "ready"
	| "applying"
	| "completed"
	| "cancelled"
	| "failed"
	| "interrupted";
export interface TokenRepairJob {
	id: string;
	status: TokenRepairJobStatus;
	createdBy: string;
	createdAt: string;
	updatedAt: string;
	scanned: number;
	repaired: number;
	conflicts: number;
	upperCreatedAt: string | null;
	upperId: string | null;
	cursorCreatedAt: string | null;
	cursorId: string | null;
	includeReviewed: boolean;
	/** Absent on old jobs; otherwise immutable for this job, including resume and apply. */
	protocolOverrides?: TokenRepairProtocolOverride[];
	/** Reanalysis is limited to this earlier job's candidate request IDs, never a fresh full scan. */
	sourceJobId?: string | null;
	error: string | null;
	/** Missing on legacy per-request review jobs. */
	workflowVersion?: 2;
	phase?: TokenRepairPhase;
	rowsPerSecond?: number;
	summary?: TokenRepairSummary | null;
}
export interface TokenRepairCandidate extends TokenRepairEvidence {
	id: string;
	jobId: string;
	requestId: string;
	createdAt: string;
	provider: string | null;
	model: string | null;
	appliedAt: string | null;
	appliedBy: string | null;
	totalsStatus: "unchanged" | "pending" | "synchronized";
}
export interface TokenRepairProbe {
	hasPending: boolean;
	job: TokenRepairJob | null;
}
export interface TokenRepairCandidatePage {
	records: TokenRepairCandidate[];
	nextCursor: string | null;
}
export interface TokenRepairApplyResult {
	applied: number;
	conflicts: number;
}

export type TokenRepairPhase =
	| "scanning"
	| "models"
	| "dry-running"
	| "preview"
	| "applying"
	| "done";
/** Decimal strings keep aggregate sums exact above Number.MAX_SAFE_INTEGER. */
export type TokenRepairTotals = { [K in keyof TokenRepairCounters]: string };
export interface TokenRepairSummary {
	previewVersion: string | null;
	requestCount: number;
	repairable: number;
	correct: number;
	unresolved: number;
	skipped: number;
	conflicts: number;
	modelCount: number;
	reasons: Record<string, number>;
	before: TokenRepairTotals;
	after: TokenRepairTotals;
	delta: TokenRepairTotals;
	repairBefore: TokenRepairTotals;
	repairAfter: TokenRepairTotals;
	/** Actual committed changes, distinct from the predicted dry-run delta. */
	appliedDelta: TokenRepairTotals;
	/** Bounded worker-local measurements, including transaction commit time. */
	writeTransactions?: TokenRepairWriteMetrics;
}
export interface TokenRepairWriteMetrics {
	count: number;
	maxMs: number;
	p99Ms: number;
}
export interface TokenRepairModel {
	provider: string | null;
	model: string;
	requestCount: number;
	protocol: TokenRepairProtocolOverride["protocol"] | null;
}
export interface TokenRepairModelPage {
	models: TokenRepairModel[];
	nextCursor: string | null;
}
