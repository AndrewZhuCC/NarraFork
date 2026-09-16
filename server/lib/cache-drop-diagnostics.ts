import { type ApiRequestDump, CACHE_DIAGNOSTIC_DUMP_MAX_BYTES } from "./agent/request-dump";
import { CacheDropDumpStore, type CacheDropSample } from "./cache-drop-dump-store";
import { logger } from "./logger";
import { getNarraforkPath } from "./narrafork-home";
import { settings, subscribeSettingsChanges } from "./settings";

let store: CacheDropDumpStore | undefined;
let stopMaintenance: (() => void) | undefined;
function getStore(): CacheDropDumpStore {
	store ??= new CacheDropDumpStore(getNarraforkPath("request-dumps", "cache-diagnostics"));
	return store;
}
function report(error: unknown): void {
	logger.warn("Cache-drop diagnosis failed; normal request is unaffected", {
		error: String(error),
	});
}

/** Called with the bounded snapshot of the REAL transport request, before usage is normalized. */
export function recordCacheDropDiagnostic(
	sample: CacheDropSample & { dump: ApiRequestDump },
): Promise<void> {
	if (!settings.agent.cacheDropDumpEnabled || !sample.narratorId || !sample.dump.request) {
		return Promise.resolve();
	}
	try {
		return getStore().record(sample).catch(report);
	} catch (error) {
		report(error);
		return Promise.resolve();
	}
}

/** An explicit history replacement establishes a fresh baseline; a transport reconnect does not. */
export function resetCacheDropBaseline(narratorId: string): void {
	if (settings.agent.cacheDropDumpEnabled && narratorId) {
		void getStore().resetNarrator(narratorId).catch(report);
	}
}

/** Explicit server startup hook: merely importing this module in a test never touches disk. */
export function startCacheDropMaintenance(): () => void {
	if (stopMaintenance) return stopMaintenance;
	const maintain = () => void getStore().maintain().catch(report);
	// Store initialization discards old rolling baselines, retaining bounded incident files.
	maintain();
	const timer = setInterval(maintain, 10 * 60_000);
	timer.unref();
	const unsubscribe = subscribeSettingsChanges(() => {
		if (!settings.agent.cacheDropDumpEnabled) void getStore().clearRolling().catch(report);
	});
	stopMaintenance = () => {
		clearInterval(timer);
		unsubscribe();
		stopMaintenance = undefined;
	};
	return stopMaintenance;
}

export { CACHE_DIAGNOSTIC_DUMP_MAX_BYTES };
