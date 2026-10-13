import type { ProviderReadiness } from "@shared/provider-readiness";
import { logger } from "../logger";
import type { ProviderAdapter } from "./provider";

/** One registration owns both routing and readiness; neither can be registered alone. */
export interface ProviderSource {
	resolve: (prefix: string, model: string) => ProviderAdapter | null | undefined;
	listReadiness: (
		signal: AbortSignal,
	) => readonly ProviderReadiness[] | Promise<readonly ProviderReadiness[]>;
}

const READINESS_TIMEOUT_MS = 2_000;
const MAX_READINESS_INSTANCES = 1_000;
const MAX_READINESS_BYTES = 512 * 1024;

export class ProviderSourceRegistry {
	private readonly sources = new Map<string, ProviderSource>();

	register(name: string, source: ProviderSource): () => void {
		if (typeof source.resolve !== "function" || typeof source.listReadiness !== "function") {
			throw new TypeError("Provider sources must supply routing and readiness together");
		}
		this.sources.set(name, source);
		return () => {
			if (this.sources.get(name) === source) this.sources.delete(name);
		};
	}

	resolve(prefix: string, model: string): ProviderAdapter | null {
		for (const source of this.sources.values()) {
			const adapter = source.resolve(prefix, model);
			if (adapter) return adapter;
		}
		return null;
	}

	/** Sequential sources bound local I/O concurrency; failures cannot blank other sources. */
	async listReadiness(disabledPrefixes: ReadonlySet<string>): Promise<ProviderReadiness[]> {
		const result = new Map<string, ProviderReadiness>();
		let outputBytes = 2;
		for (const [name, source] of this.sources) {
			const startedAt = performance.now();
			const controller = new AbortController();
			let timer: ReturnType<typeof setTimeout> | undefined;
			try {
				const entries = await Promise.race([
					Promise.resolve().then(() => source.listReadiness(controller.signal)),
					new Promise<never>((_, reject) => {
						timer = setTimeout(() => {
							controller.abort();
							reject(new Error("Provider readiness timed out"));
						}, READINESS_TIMEOUT_MS);
					}),
				]);
				for (const entry of entries.slice(0, MAX_READINESS_INSTANCES)) {
					if (
						typeof entry.id !== "string" ||
						typeof entry.prefix !== "string" ||
						typeof entry.name !== "string" ||
						!entry.id ||
						!entry.prefix ||
						entry.id.length > 256 ||
						entry.prefix.length > 128 ||
						result.has(entry.id)
					)
						continue;
					// Explicit field projection prevents credentials from crossing the API boundary.
					const projection: ProviderReadiness = {
						id: entry.id,
						prefix: entry.prefix,
						name: entry.name.slice(0, 256),
						ready: entry.ready === true && !disabledPrefixes.has(entry.prefix),
					};
					const bytes = Buffer.byteLength(JSON.stringify(projection)) + 1;
					if (outputBytes + bytes > MAX_READINESS_BYTES) return [...result.values()];
					outputBytes += bytes;
					result.set(entry.id, projection);
					if (result.size >= MAX_READINESS_INSTANCES) return [...result.values()];
				}
			} catch {
				// Log no provider config or exception text: these may contain credentials.
				logger.warn("Provider readiness source unavailable", {
					source: name,
					timedOut: controller.signal.aborted,
				});
			} finally {
				if (timer !== undefined) clearTimeout(timer);
				const elapsedMs = performance.now() - startedAt;
				if (elapsedMs >= 500) {
					logger.warn("Slow provider readiness source", {
						source: name,
						elapsedMs: Math.round(elapsedMs),
					});
				}
			}
		}
		return [...result.values()];
	}
}
