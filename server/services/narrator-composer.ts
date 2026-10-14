import {
	CODEX_COMPOSER_MAX_RESPONSE_BYTES,
	CODEX_COMPOSER_MAX_SEED_BYTES,
	type CodexComposerSeed,
	getCodexComposerSettings,
} from "@server/lib/agent/codex-composer";
import { diagnosticsFromError } from "@server/lib/agent/error-diagnostics";
import type { GenerateMetaResult } from "@server/lib/agent/provider";
import { ApiRequestDumpCollector, responseDumpBudget } from "@server/lib/agent/request-dump";
import {
	type ApiRequestHandle,
	finishApiRequest,
	isRequestDumpEnabled,
	isRequestDumpErrorsOnly,
	startApiRequest,
} from "@server/lib/api-request-tracker";
import { generateId } from "@server/lib/id";
import { logger } from "@server/lib/logger";
import { settings } from "@server/lib/settings";
import { broadcastToNarrator } from "@server/websocket/narrator-ws";
import { type CodexComposerSuggestion, parseCodexComposerSuggestion } from "@shared/codex-composer";

const MAX_ENTRIES = 256;
const MAX_SEED_BYTES = 32 * 1024 * 1024;
const RESULT_TTL_MS = 60 * 60 * 1000;
const MAX_CONCURRENT = 2;
const PREDICTION_TIMEOUT_MS = 90_000;
const SHUTDOWN_TIMEOUT_MS = 2_000;

interface PendingSeed {
	seed: CodexComposerSeed;
	bytes: number;
	userId: string | null;
}

interface CachedSuggestion extends CodexComposerSuggestion {
	providerId: string;
	configSignature: string;
	expiresAt: number;
}

interface ComposerRun {
	generation: string;
	started: boolean;
	pending?: PendingSeed;
	result?: CachedSuggestion;
}

interface PredictionTask {
	narratorId: string;
	generation: string;
	providerId: string;
	model: string;
	configSignature: string;
	userId: string | null;
	bytes: number;
	seed?: CodexComposerSeed;
	controller: AbortController;
	promise?: Promise<void>;
}

// Generations share the cache's count bound, including runs which never produce a seed.
const runs = new Map<string, ComposerRun>();
const tasks = new Set<PredictionTask>();
let seedBytes = 0;
let shuttingDown = false;

function notify(narratorId: string): void {
	broadcastToNarrator(narratorId, { type: "composer_suggestion_changed", narratorId });
}

function releasePending(run: ComposerRun): void {
	if (!run.pending) return;
	seedBytes -= run.pending.bytes;
	run.pending = undefined;
}

function cancelPredictionTasks(narratorId: string): void {
	for (const task of tasks) {
		if (task.narratorId === narratorId) {
			task.controller.abort(new Error("Composer prediction cancelled"));
		}
	}
}
function discardRun(narratorId: string): void {
	const run = runs.get(narratorId);
	if (run) releasePending(run);
	runs.delete(narratorId);
	cancelPredictionTasks(narratorId);
	notify(narratorId);
}

function pruneExpiredResults(): void {
	const now = Date.now();
	for (const [narratorId, run] of runs) {
		if (run.result && run.result.expiresAt <= now) discardRun(narratorId);
	}
}

export function beginComposerRun(narratorId: string): string {
	pruneExpiredResults();
	discardRun(narratorId);
	const generation = generateId();
	if (shuttingDown) return generation;
	runs.set(narratorId, { generation, started: false });
	while (runs.size > MAX_ENTRIES) {
		const oldest = runs.keys().next().value;
		if (oldest === undefined) break;
		discardRun(oldest);
	}
	return generation;
}

export function rememberComposerSeed(
	narratorId: string,
	generation: string,
	seed: CodexComposerSeed | undefined,
	userId?: string | null,
): void {
	pruneExpiredResults();
	const run = runs.get(narratorId);
	if (shuttingDown || !run || run.generation !== generation) return;
	// The primary's onRequestStart clears the seed. Foreground traffic wins even
	// when a caller resumes within the same generation rather than starting a new run.
	if (!seed) cancelPredictionTasks(narratorId);
	if (run.started) return;
	// Even an ineligible latest request must release the previous request's snapshot.
	releasePending(run);
	if (
		!seed ||
		!Number.isSafeInteger(seed.bytes) ||
		seed.bytes < 0 ||
		seed.bytes > CODEX_COMPOSER_MAX_SEED_BYTES
	) {
		return;
	}
	if (getCodexComposerSettings(seed.providerId)?.signature !== seed.configSignature) return;
	for (const candidate of runs.values()) {
		if (seedBytes + seed.bytes <= MAX_SEED_BYTES) break;
		releasePending(candidate);
	}
	if (seedBytes + seed.bytes > MAX_SEED_BYTES) return;
	run.pending = { seed, bytes: seed.bytes, userId: userId ?? null };
	seedBytes += seed.bytes;
}

export function startComposerPrediction(narratorId: string, generation: string): void {
	pruneExpiredResults();
	const run = runs.get(narratorId);
	if (shuttingDown || !run || run.generation !== generation || run.started || !run.pending) return;
	const { seed, bytes, userId } = run.pending;
	if (getCodexComposerSettings(seed.providerId)?.signature !== seed.configSignature) {
		discardRun(narratorId);
		return;
	}
	run.started = true;
	if (tasks.size >= MAX_CONCURRENT) {
		releasePending(run);
		return;
	}
	const task: PredictionTask = {
		narratorId,
		generation,
		providerId: seed.providerId,
		model: seed.model,
		configSignature: seed.configSignature,
		userId,
		bytes,
		seed,
		controller: new AbortController(),
	};
	// Transfer ownership without releasing the in-flight snapshot's byte reservation.
	run.pending = undefined;
	tasks.add(task);
	task.promise = predict(task);
}

async function predict(task: PredictionTask): Promise<void> {
	const startedAt = Date.now();
	let handle: ApiRequestHandle | undefined;
	let dump: ApiRequestDumpCollector | undefined;
	let result: GenerateMetaResult | undefined;
	let failure: unknown;
	let errorCode: string | undefined;
	const timeout = setTimeout(() => {
		errorCode = "timeout";
		task.controller.abort(new Error("Composer prediction timed out"));
	}, PREDICTION_TIMEOUT_MS);
	timeout.unref();
	try {
		handle = startApiRequest({
			narratorId: task.narratorId,
			userId: task.userId,
			provider: task.providerId,
			model: task.model,
			kind: "composer",
		});
		if (isRequestDumpEnabled()) {
			dump = new ApiRequestDumpCollector(
				{ provider: task.providerId, model: task.model },
				undefined,
				undefined,
				Math.min(
					responseDumpBudget(settings.agent.requestDumpMaxSize),
					CODEX_COMPOSER_MAX_SEED_BYTES + CODEX_COMPOSER_MAX_RESPONSE_BYTES,
				),
			);
		}
		task.controller.signal.throwIfAborted();
		if (!task.seed) throw new Error("Composer prediction has no seed");
		const response = task.seed.run({ signal: task.controller.signal, requestDump: dump });
		// The request producer owns the snapshot until it settles; caches never retain it.
		task.seed = undefined;
		result = await response;
		task.controller.signal.throwIfAborted();
		errorCode = "invalid_suggestion";
		if (result.outputTruncated) throw new Error("Composer prediction response was incomplete");
		const parsed = parseCodexComposerSuggestion(result.text);
		errorCode = undefined;
		const run = runs.get(task.narratorId);
		if (run?.generation === task.generation) {
			if (getCodexComposerSettings(task.providerId)?.signature !== task.configSignature) {
				discardRun(task.narratorId);
			} else if (!shuttingDown && !task.controller.signal.aborted) {
				run.result = {
					...parsed,
					providerId: task.providerId,
					configSignature: task.configSignature,
					expiresAt: Date.now() + RESULT_TTL_MS,
				};
				notify(task.narratorId);
			}
		}
	} catch (error) {
		failure = error ?? new Error("Composer prediction failed");
		errorCode ??= task.controller.signal.aborted ? "cancelled" : "request_failed";
	} finally {
		clearTimeout(timeout);
		task.seed = undefined;
		seedBytes -= task.bytes;
		task.bytes = 0;
	}
	try {
		if (handle) {
			if (failure !== undefined) dump?.setResponseError(failure);
			await finishApiRequest(handle, {
				usage: result?.usage ?? null,
				credentialId: result?.credentialId ?? null,
				contextPercent: result?.contextPercent ?? null,
				meterUsage: result?.meterUsage ?? null,
				meterUnit: result?.meterUnit ?? null,
				durationMs: Date.now() - startedAt,
				errorMessage:
					failure === undefined
						? null
						: (failure instanceof Error ? failure.message : String(failure)).slice(0, 1024),
				diagnostics: failure === undefined ? undefined : diagnosticsFromError(failure),
				rawDump:
					isRequestDumpEnabled() && (failure !== undefined || !isRequestDumpErrorsOnly())
						? dump?.snapshot()
						: undefined,
			});
		}
	} catch {
		logger.warn("Failed to record Composer API request", {
			model: task.model,
			durationMs: Date.now() - startedAt,
			error: "usage_record_failed",
		});
	} finally {
		tasks.delete(task);
		task.promise = undefined;
	}
	const log = {
		model: task.model,
		durationMs: Date.now() - startedAt,
		inputTokens: result?.usage?.inputTokens ?? 0,
		cachedInputTokens: result?.usage?.cachedInputTokens ?? 0,
		...(errorCode ? { error: errorCode } : {}),
	};
	if (failure === undefined) logger.debug("Composer prediction finished", log);
	else logger.warn("Composer prediction failed", log);
}

export function getComposerSuggestion(narratorId: string): CodexComposerSuggestion {
	pruneExpiredResults();
	const cached = runs.get(narratorId)?.result;
	if (!cached) return { suggestion: null };
	if (getCodexComposerSettings(cached.providerId)?.signature !== cached.configSignature) {
		discardRun(narratorId);
		return { suggestion: null };
	}
	return { suggestion: cached.suggestion };
}

export function invalidateComposerSuggestion(narratorId: string, generation?: string): void {
	if (generation !== undefined && runs.get(narratorId)?.generation !== generation) return;
	discardRun(narratorId);
}

export function invalidateAllComposerSuggestions(): void {
	const narratorIds = new Set([...runs.keys(), ...Array.from(tasks, (task) => task.narratorId)]);
	for (const narratorId of narratorIds) discardRun(narratorId);
}

/** Settings saves unrelated to a captured provider do not erase an empty active generation. */
export function invalidateChangedComposerSettings(): void {
	for (const [narratorId, run] of runs) {
		const source = run.pending?.seed ?? run.result;
		if (
			source &&
			getCodexComposerSettings(source.providerId)?.signature !== source.configSignature
		) {
			discardRun(narratorId);
		}
	}
	for (const task of tasks) {
		if (
			runs.get(task.narratorId)?.generation === task.generation &&
			getCodexComposerSettings(task.providerId)?.signature !== task.configSignature
		)
			discardRun(task.narratorId);
	}
}
export async function shutdownComposerSuggestions(): Promise<void> {
	shuttingDown = true;
	invalidateAllComposerSuggestions();
	const pending = Array.from(tasks, (task) => task.promise);
	if (!pending.length) return;
	let timeout: ReturnType<typeof setTimeout> | undefined;
	try {
		await Promise.race([
			Promise.allSettled(pending),
			new Promise<never>((_resolve, reject) => {
				timeout = setTimeout(
					() => reject(new Error("Composer requests did not stop before shutdown timeout")),
					SHUTDOWN_TIMEOUT_MS,
				);
			}),
		]);
	} finally {
		clearTimeout(timeout);
	}
}
