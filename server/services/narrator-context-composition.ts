import type { Database } from "bun:sqlite";
import {
	type ContextCharCache,
	type ContextComposition,
	type ContextSegment,
	groupContextSegments,
	safeCharacters,
} from "@shared/context-composition";
import {
	type ContextInputCharacters,
	type ContextUsageSnapshot,
	MAX_INPUT_COMPOSITION_SEGMENTS,
} from "@shared/context-usage";
import { eq } from "drizzle-orm";
import { activeDatabaseBackend, db, sqlite } from "../db";
import { narrators } from "../db/schema";
import { observeContextCharacterWrites } from "../lib/context-characters";
import {
	boundedContextSnapshot,
	parseContextSnapshot,
	validInputCharacters,
} from "../lib/context-usage-snapshot";
import { AppError, NotFoundError } from "../lib/errors";
import { logger } from "../lib/logger";
import { readContextHistoryEntries, yieldContextReader } from "./context-composition-history";
import { readContextSegments } from "./context-composition-projection";

/** Post-compact transcripts are shown in one response. This only stops a runaway scan. */
const SEGMENT_CAP = 4096;

type Meta = {
	type: string;
	variant: string;
	systemChars: number;
	summaryChars: number;
	toolsChars: number;
	usage: string | null;
};

function profileOf(type: string, variant: string): "primary" | "subagent" {
	return type === "subagent" || variant.startsWith("subagent:") ? "subagent" : "primary";
}

function prefixSegments(system: number, summary: number, tools: number): ContextSegment[] {
	return [
		{ category: "system", chars: safeCharacters(system) },
		{ category: "summary", chars: safeCharacters(summary) },
		{ category: "toolDefinition", chars: safeCharacters(tools) },
	].filter((segment) => segment.chars > 0);
}

function compositionOf(
	generation: string,
	revision: string,
	segments: ContextSegment[],
): ContextCharCache {
	const totals = groupContextSegments(segments);
	return {
		generation,
		revision,
		pageCount: segments.length > 0 ? 1 : 0,
		totalChars: totals.reduce((sum, segment) => sum + segment.chars, 0),
		totals,
	};
}

/** Dependency injection keeps tests on disposable SQLite schemas. */
export function createContextCharacterService(database: Database) {
	const metadata = (id: string) =>
		database
			.query<Meta, [string]>(
				`SELECT type, variant, context_system_chars AS systemChars,
			 context_summary_chars AS summaryChars, context_tools_chars AS toolsChars,
			 context_usage_snapshot_json AS usage FROM narrators WHERE id = ?`,
			)
			.get(id);

	async function historySegments(
		id: string,
		profile: "primary" | "subagent",
		signal?: AbortSignal,
	): Promise<{ segments: ContextSegment[]; truncated: boolean }> {
		const segments: ContextSegment[] = [];
		for await (const entry of readContextHistoryEntries(database, id, {
			profile,
			check: () => {
				if (signal?.aborted) throw new DOMException("Context composition cancelled", "AbortError");
			},
		})) {
			for (const segment of entry.segments) {
				if (segments.length >= SEGMENT_CAP) {
					logger.warn("Context composition segment cap reached", { narratorId: id });
					return { segments, truncated: true };
				}
				segments.push({ category: segment.category, chars: safeCharacters(segment.chars) });
			}
		}
		return { segments, truncated: false };
	}

	function liveSegments(meta: Meta, history: ContextSegment[]): ContextSegment[] {
		return [...prefixSegments(meta.systemChars, meta.summaryChars, meta.toolsChars), ...history];
	}

	function writeSnapshot(id: string, snapshot: ContextUsageSnapshot | null) {
		database
			.query("UPDATE narrators SET context_usage_snapshot_json = ? WHERE id = ?")
			.run(snapshot ? JSON.stringify(snapshot) : null, id);
	}

	function publishPin(id: string, composition: ContextCharCache, segments: ContextSegment[]) {
		database.transaction(() => {
			database.query("DELETE FROM narrator_context_pins WHERE narrator_id = ?").run(id);
			database
				.query(
					"INSERT INTO narrator_context_pins (generation, narrator_id, segments_json) VALUES (?,?,?)",
				)
				.run(composition.generation, id, JSON.stringify(segments));
		})();
	}

	const preparing = new Set<string>();

	return {
		async invalidate() {},
		async invalidateBatch() {},
		async invalidateOverflow() {},
		async cancel() {},
		async dispose() {},
		async settled() {},
		storeUsage(id: string, snapshot: ContextUsageSnapshot) {
			const value = boundedContextSnapshot(snapshot);
			if (!value) return;
			writeSnapshot(id, value);
		},
		async storeRuntime(id: string, values: { systemChars?: number; toolsChars?: number }) {
			const systemChars =
				values.systemChars === undefined ? null : safeCharacters(values.systemChars);
			const toolsChars = values.toolsChars === undefined ? null : safeCharacters(values.toolsChars);
			database
				.query(
					`UPDATE narrators SET context_system_chars = coalesce(?, context_system_chars),
				 context_tools_chars = coalesce(?, context_tools_chars),
				 context_char_revision = context_char_revision + 1
				 WHERE id = ? AND (context_system_chars != coalesce(?, context_system_chars)
				 OR context_tools_chars != coalesce(?, context_tools_chars))`,
				)
				.run(systemChars, toolsChars, id, systemChars, toolsChars);
		},
		async freezeForRequest(
			id: string,
			counts: ContextInputCharacters | null,
			requestId: string,
			startedAt: string,
			runtimeSummaryChars = 0,
			signal?: AbortSignal,
		): Promise<ContextCharCache | null> {
			if (signal?.aborted || preparing.has(id)) return null;
			const meta = metadata(id);
			if (!meta) return null;
			const requestCounts = validInputCharacters(counts);
			const unknown = () => {
				const snapshot = boundedContextSnapshot({
					requestId,
					startedAt,
					source: "estimate",
					percentage: null,
					contextWindow: null,
					occupiedTokens: null,
					inputCharacters: counts,
					composition: null,
				});
				writeSnapshot(id, snapshot);
				database.query("DELETE FROM narrator_context_pins WHERE narrator_id = ?").run(id);
				return null;
			};
			if (requestCounts?.compositionSegments) {
				let summaryRemaining =
					Number.isSafeInteger(runtimeSummaryChars) &&
					runtimeSummaryChars >= 0 &&
					runtimeSummaryChars <= requestCounts.systemChars &&
					requestCounts.compositionSegments.length < MAX_INPUT_COMPOSITION_SEGMENTS &&
					!requestCounts.compositionSegments.some((segment) => segment.category === "summary")
						? runtimeSummaryChars
						: 0;
				const segments: ContextSegment[] = [];
				for (const segment of requestCounts.compositionSegments) {
					if (segment.category === "system" && summaryRemaining > 0) {
						const summaryChars = Math.min(summaryRemaining, segment.chars);
						if (segment.chars > summaryChars)
							segments.push({ category: "system", chars: segment.chars - summaryChars });
						segments.push({ category: "summary", chars: summaryChars });
						summaryRemaining -= summaryChars;
					} else segments.push(segment);
				}
				const composition = compositionOf(crypto.randomUUID(), `request:${requestId}`, segments);
				const snapshot = boundedContextSnapshot({
					requestId,
					startedAt,
					source: "estimate",
					percentage: null,
					contextWindow: null,
					occupiedTokens: null,
					inputCharacters: requestCounts,
					composition,
				});
				if (!snapshot) return null;
				publishPin(id, composition, segments);
				writeSnapshot(id, snapshot);
				return composition;
			}
			if (
				counts?.compositionSegments === null ||
				(counts?.compositionSegments !== undefined && !requestCounts?.compositionSegments)
			)
				return unknown();
			preparing.add(id);
			const writes: Array<{ narratorId: string; messageId?: string }> = [];
			const stop = observeContextCharacterWrites((narratorId, messageId) => {
				writes.push({ narratorId, messageId });
			});
			const seen = new Set<string>();
			const relevant = () => {
				for (const write of writes) {
					const key = `${write.narratorId}:${write.messageId ?? "*"}`;
					if (seen.has(key)) continue;
					seen.add(key);
					if (write.narratorId === id) return true;
					if (!write.messageId) continue;
					const shared = database
						.query<{ ok: number }, [string, string]>(
							"SELECT 1 AS ok FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ? LIMIT 1",
						)
						.get(id, write.messageId);
					if (shared) return true;
				}
				return false;
			};
			try {
				// A write that lands while this request is being pinned belongs to a later input.
				await yieldContextReader();
				if (signal?.aborted) return null;
				if (relevant()) return unknown();
				const summary =
					requestCounts &&
					Number.isSafeInteger(runtimeSummaryChars) &&
					runtimeSummaryChars >= 0 &&
					runtimeSummaryChars <= requestCounts.systemChars
						? runtimeSummaryChars
						: 0;
				const fixed = requestCounts
					? prefixSegments(requestCounts.systemChars - summary, summary, requestCounts.toolsChars)
					: prefixSegments(meta.systemChars, meta.summaryChars, meta.toolsChars);
				const history = await historySegments(id, profileOf(meta.type, meta.variant), signal);
				if (signal?.aborted) return null;
				if (relevant()) return unknown();
				if (history.truncated) return unknown();
				const segments = [...fixed, ...history.segments];
				const composition = compositionOf(crypto.randomUUID(), `request:${requestId}`, segments);
				const snapshot = boundedContextSnapshot({
					requestId,
					startedAt,
					source: "estimate",
					percentage: null,
					contextWindow: null,
					occupiedTokens: null,
					inputCharacters: counts,
					composition,
				});
				if (!snapshot?.composition) return unknown();
				publishPin(id, snapshot.composition, segments);
				writeSnapshot(id, snapshot);
				return snapshot.composition;
			} finally {
				stop();
				preparing.delete(id);
			}
		},
		async get(id: string, signal?: AbortSignal): Promise<ContextComposition> {
			signal?.throwIfAborted();
			const meta = metadata(id);
			if (!meta) throw new NotFoundError("Narrator", id);
			const usage = parseContextSnapshot(meta.usage);
			const pinned = usage?.composition;
			if (pinned) {
				const row = database
					.query<{ json: string }, [string, string]>(
						"SELECT segments_json AS json FROM narrator_context_pins WHERE generation = ? AND narrator_id = ?",
					)
					.get(pinned.generation, id);
				if (row) {
					return {
						usage,
						generation: pinned.generation,
						totalChars: pinned.totalChars,
						totals: pinned.totals,
						segments: readContextSegments(row.json),
						nextCursor: null,
						pending: false,
					};
				}
			}
			const history = await historySegments(id, profileOf(meta.type, meta.variant), signal);
			const segments = liveSegments(meta, history.segments);
			const totals = groupContextSegments(segments);
			return {
				usage,
				generation: null,
				totalChars: totals.reduce((sum, segment) => sum + segment.chars, 0),
				totals,
				segments,
				nextCursor: null,
				pending: false,
			};
		},
	};
}

let service: ReturnType<typeof createContextCharacterService> | undefined;
function currentService() {
	if (activeDatabaseBackend === "postgres")
		throw new AppError(
			"Context composition is not yet available on PostgreSQL",
			501,
			"CONTEXT_COMPOSITION_UNAVAILABLE",
		);
	service ??= createContextCharacterService(sqlite);
	return service;
}

export async function freezeNarratorContextComposition(
	narratorId: string,
	counts: ContextInputCharacters | null,
	requestId: string,
	startedAt: string,
	runtimeSummaryChars = 0,
	signal?: AbortSignal,
): Promise<ContextCharCache | null> {
	if (activeDatabaseBackend === "postgres") return null;
	return currentService().freezeForRequest(
		narratorId,
		counts,
		requestId,
		startedAt,
		runtimeSummaryChars,
		signal,
	);
}

export async function storeNarratorContextUsage(
	narratorId: string,
	snapshot: ContextUsageSnapshot,
): Promise<void> {
	if (activeDatabaseBackend === "postgres") {
		const value = boundedContextSnapshot(snapshot);
		if (value)
			await db
				.update(narrators)
				.set({ contextUsageSnapshotJson: value })
				.where(eq(narrators.id, narratorId));
		return;
	}
	currentService().storeUsage(narratorId, snapshot);
}

export async function invalidateContextCharacterCache(
	_narratorId: string,
	_messageId?: string,
): Promise<void> {}

export async function invalidateContextCharacterBatch(
	_narratorId: string,
	_messageIds?: readonly string[],
	_options?: { full?: boolean },
): Promise<void> {}

export async function invalidateContextCharacterOverflow(): Promise<void> {}

export async function storeContextRuntimeCharacters(
	narratorId: string,
	values: { systemChars?: number; toolsChars?: number },
): Promise<void> {
	if (activeDatabaseBackend === "postgres") return;
	await currentService().storeRuntime(narratorId, values);
}

export async function getNarratorContextComposition(
	narratorId: string,
	signal?: AbortSignal,
): Promise<ContextComposition> {
	return currentService().get(narratorId, signal);
}
