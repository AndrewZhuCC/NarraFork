import type { ApiRequestDumpCollector } from "./agent/request-dump";
import { CacheDropDetector, type CacheDropSample } from "./cache-drop-dump-store";
import { AppError } from "./errors";
import { logger } from "./logger";
import { getRequestRecordingDetail, pinRecordingRequest } from "./request-recorder";
import { settings, subscribeSettingsChanges } from "./settings";

const detector = new CacheDropDetector();
let stopMaintenance: (() => void) | undefined;
function report(error: unknown): void {
	logger.warn("Cache-drop diagnosis failed; normal request is unaffected", {
		error: String(error),
	});
}

/** Called before the actual attempt's recording finishes; never copies its body. */
export async function recordCacheDropDiagnostic(
	sample: CacheDropSample,
	collector?: ApiRequestDumpCollector,
): Promise<void> {
	if (!settings.agent.cacheDropDumpEnabled || !sample.narratorId) return;
	try {
		for (const incident of detector.record(sample)) {
			logger.info("Cache-drop request evidence", {
				requestId: sample.requestId,
				narratorId: sample.narratorId,
				...incident,
				recordingUnavailable: !collector,
			});
			if (!collector) continue;
			collector.pin("request");
			const relatedIds = [
				...new Set([...incident.beforeRequestIds, incident.triggerRequestId]),
			].filter((requestId) => requestId !== sample.requestId);
			// At most three before identities plus the trigger; all lookups share one deadline.
			const signal = AbortSignal.timeout(1000);
			const related = await Promise.all(
				relatedIds.map(async (requestId) => {
					try {
						pinRecordingRequest(requestId, "request");
						const detail = await getRequestRecordingDetail(sample.narratorId, requestId, signal);
						return {
							requestId,
							availability: "available",
							partial: detail.partial,
							truncated: detail.truncated,
						};
					} catch (error) {
						return {
							requestId,
							availability:
								error instanceof AppError && error.statusCode === 404 ? "missing" : "unavailable",
						};
					}
				}),
			);
			collector.frame({
				direction: "response",
				type: "cache_drop",
				metadata: { ...incident, requestId: sample.requestId, related },
			});
		}
	} catch (error) {
		report(error);
	}
}

/** An explicit history replacement establishes a fresh baseline; a reconnect does not. */
export function resetCacheDropBaseline(narratorId: string): void {
	if (settings.agent.cacheDropDumpEnabled && narratorId) detector.resetNarrator(narratorId);
}

/** Reuse the existing maintenance timer for bounded, metadata-only comparison state. */
export function startCacheDropMaintenance(): () => void {
	if (stopMaintenance) return stopMaintenance;
	detector.maintain();
	const timer = setInterval(() => detector.maintain(), 10 * 60_000);
	timer.unref();
	const unsubscribe = subscribeSettingsChanges(() => {
		if (!settings.agent.cacheDropDumpEnabled) detector.clearRolling();
	});
	stopMaintenance = () => {
		clearInterval(timer);
		unsubscribe();
		stopMaintenance = undefined;
	};
	return stopMaintenance;
}
