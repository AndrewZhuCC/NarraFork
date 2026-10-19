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
import { activeDatabaseBackend, sqlite } from "../db";
import { observeContextCharacterWrites } from "../lib/context-characters";
import {
	boundedContextSnapshot,
	parseContextSnapshot,
	validInputCharacters,
} from "../lib/context-usage-snapshot";
import { AppError, NotFoundError } from "../lib/errors";
import { logger } from "../lib/logger";
import { type OwnTurnPlacement, ownTurnPlacementOf } from "../lib/runtime-source";
import {
	type ContextQuery,
	postgresContextQuery,
	readContextHistoryEntries,
	sqliteContextQuery,
	yieldContextReader,
} from "./context-composition-history";
import { readContextSegments } from "./context-composition-projection";

/** Post-compact transcripts are shown in one response. This only stops a runaway scan. */
const SEGMENT_CAP = 4096;

type Meta = {
	type: string;
	variant: string;
	parentNarratorId: string | null;
	registration: string | null;
	systemChars: number;
	summaryChars: number;
	toolsChars: number;
	usage: string | null;
};

/** The session's own-turn placement (`ownTurnPlacementOf`, the `isTopLevelMessage` rule). */
function placementOf(meta: Meta): OwnTurnPlacement {
	return ownTurnPlacementOf({
		type: meta.type,
		variant: meta.variant,
		parentNarratorId: meta.parentNarratorId,
		runtimeRegistrationJson: meta.registration ? JSON.parse(meta.registration) : null,
	});
}

function prefixSegments(system: number, summary: number, tools: number): ContextSegment[] {
	const segments: ContextSegment[] = [];
	if (system > 0) segments.push({ category: "system", chars: safeCharacters(system) });
	if (summary > 0) segments.push({ category: "summary", chars: safeCharacters(summary) });
	if (tools > 0) segments.push({ category: "toolDefinition", chars: safeCharacters(tools) });
	return segments;
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
	return createContextCharacterServiceFromQuery(sqliteContextQuery(database));
}

export function createContextCharacterServiceFromQuery(query: ContextQuery) {
	const metadata = (id: string) =>
		query.get<Meta>(
			`SELECT type AS "type", variant AS "variant", parent_narrator_id AS "parentNarratorId",
			 runtime_registration_json AS "registration", context_system_chars AS "systemChars",
			 context_summary_chars AS "summaryChars", context_tools_chars AS "toolsChars",
			 context_usage_snapshot_json AS "usage" FROM narrators WHERE id = ?`,
			[id],
		);

	async function historySegments(
		id: string,
		placement: OwnTurnPlacement,
		signal?: AbortSignal,
	): Promise<{ segments: ContextSegment[]; truncated: boolean }> {
		const segments: ContextSegment[] = [];
		for await (const entry of readContextHistoryEntries(query, id, {
			topLevelToolUseId: placement,
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

	async function writeSnapshot(id: string, snapshot: ContextUsageSnapshot | null) {
		await query.run("UPDATE narrators SET context_usage_snapshot_json = ? WHERE id = ?", [
			snapshot ? JSON.stringify(snapshot) : null,
			id,
		]);
	}

	async function publishPin(id: string, composition: ContextCharCache, segments: ContextSegment[]) {
		// The callback stays synchronous: bun:sqlite commits when it returns, so an async
		// callback would publish the pin outside the transaction. Both statements are queued
		// before the driver replays them; PostgreSQL awaits the returned chain.
		await query.transaction((tx) => {
			return tx
				.run("DELETE FROM narrator_context_pins WHERE narrator_id = ?", [id])
				.then(() =>
					tx.run(
						"INSERT INTO narrator_context_pins (generation, narrator_id, segments_json) VALUES (?,?,?)",
						[composition.generation, id, JSON.stringify(segments)],
					),
				);
		});
	}

	const preparing = new Set<string>();

	return {
		async invalidate(_id?: string, _messageId?: string) {},
		async invalidateBatch(
			_id?: string,
			_messageIds?: readonly string[],
			_options?: { full?: boolean },
		) {},
		async invalidateOverflow() {},
		async cancel() {},
		async dispose() {},
		async settled() {},
		async storeUsage(id: string, snapshot: ContextUsageSnapshot) {
			const value = boundedContextSnapshot(snapshot);
			if (!value) return;
			await writeSnapshot(id, value);
		},
		async storeRuntime(id: string, values: { systemChars?: number; toolsChars?: number }) {
			const systemChars =
				values.systemChars === undefined ? null : safeCharacters(values.systemChars);
			const toolsChars = values.toolsChars === undefined ? null : safeCharacters(values.toolsChars);
			await query.run(
				`UPDATE narrators SET context_system_chars = coalesce(?, context_system_chars),
				 context_tools_chars = coalesce(?, context_tools_chars),
				 context_char_revision = context_char_revision + 1
				 WHERE id = ? AND (context_system_chars != coalesce(?, context_system_chars)
				 OR context_tools_chars != coalesce(?, context_tools_chars))`,
				[systemChars, toolsChars, id, systemChars, toolsChars],
			);
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
			const meta = await metadata(id);
			if (!meta) return null;
			const requestCounts = validInputCharacters(counts);
			const unknown = async () => {
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
				await writeSnapshot(id, snapshot);
				await query.run("DELETE FROM narrator_context_pins WHERE narrator_id = ?", [id]);
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
				await publishPin(id, composition, segments);
				await writeSnapshot(id, snapshot);
				return composition;
			}
			if (
				counts?.compositionSegments === null ||
				(counts?.compositionSegments !== undefined && !requestCounts?.compositionSegments)
			)
				return await unknown();
			preparing.add(id);
			const writes: Array<{ narratorId: string; messageId?: string }> = [];
			const stop = observeContextCharacterWrites((narratorId, messageId) => {
				writes.push({ narratorId, messageId });
			});
			const seen = new Set<string>();
			const relevant = async () => {
				for (const write of writes) {
					const key = `${write.narratorId}:${write.messageId ?? "*"}`;
					if (seen.has(key)) continue;
					seen.add(key);
					if (write.narratorId === id) return true;
					if (!write.messageId) continue;
					const shared = await query.get<{ ok: number }>(
						`SELECT 1 AS "ok" FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ? LIMIT 1`,
						[id, write.messageId],
					);
					if (shared) return true;
				}
				return false;
			};
			try {
				// A write that lands while this request is being pinned belongs to a later input.
				await yieldContextReader();
				if (signal?.aborted) return null;
				if (await relevant()) return await unknown();
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
				const history = await historySegments(id, placementOf(meta), signal);
				if (signal?.aborted) return null;
				if (await relevant()) return await unknown();
				if (history.truncated) return await unknown();
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
				if (!snapshot?.composition) return await unknown();
				await publishPin(id, snapshot.composition, segments);
				await writeSnapshot(id, snapshot);
				return snapshot.composition;
			} finally {
				stop();
				preparing.delete(id);
			}
		},
		async get(id: string, signal?: AbortSignal): Promise<ContextComposition> {
			signal?.throwIfAborted();
			const meta = await metadata(id);
			if (!meta) throw new NotFoundError("Narrator", id);
			const usage = parseContextSnapshot(meta.usage);
			const pinned = usage?.composition;
			if (pinned) {
				const row = await query.get<{ json: string }>(
					`SELECT segments_json AS "json" FROM narrator_context_pins WHERE generation = ? AND narrator_id = ?`,
					[pinned.generation, id],
				);
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
			const history = await historySegments(id, placementOf(meta), signal);
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
let servicePromise: Promise<ReturnType<typeof createContextCharacterService>> | undefined;
function currentService() {
	servicePromise ??= openService().catch((error: unknown) => {
		servicePromise = undefined;
		throw error;
	});
	return servicePromise;
}

async function openService() {
	if (service) return service;
	if (activeDatabaseBackend === "postgres") {
		// Dynamic import: test doubles replace `../db` without this binding, and a static
		// named import would fail those modules to link.
		const runtime = (await import("../db")).postgresRuntime;
		if (!runtime)
			throw new AppError(
				"PostgreSQL runtime is not connected",
				503,
				"CONTEXT_COMPOSITION_UNAVAILABLE",
			);
		service = createContextCharacterServiceFromQuery(postgresContextQuery(runtime.client.sql));
		return service;
	}
	service = createContextCharacterService(sqlite);
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
	return (await currentService()).freezeForRequest(
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
	await (await currentService()).storeUsage(narratorId, snapshot);
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
	await (await currentService()).storeRuntime(narratorId, values);
}

export async function getNarratorContextComposition(
	narratorId: string,
	signal?: AbortSignal,
): Promise<ContextComposition> {
	return (await currentService()).get(narratorId, signal);
}
