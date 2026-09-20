import type { ExecutionBackend, GlobMatches } from "../lib/agent/execution/backend";
import { logger } from "../lib/logger";

const DISCOVERY_PATTERNS = ["*/.git", "*/*/.git", "*/*/*/.git"] as const;
const DISCOVERY_MAX_CANDIDATES = 32;
const DISCOVERY_MAX_BYTES = 64 * 1024;
export const GIT_WORKSPACE_DISCOVERY_BUDGET_MS = 3000;
const DISCOVERY_CACHE_TTL_MS = 5000;
const DISCOVERY_CACHE_MAX_ENTRIES = 64;

type CacheEntry = { paths: string[]; truncated: boolean; expiresAt: number };
const cache = new Map<string, CacheEntry>();

export interface GitWorkspaceDiscovery {
	paths: string[];
	truncated: boolean;
	unsupported: boolean;
}

function cacheKey(backend: ExecutionBackend, cwd: string): string {
	return JSON.stringify([
		backend.deviceId,
		backend.runtimeGeneration,
		backend.paths.identityKey(cwd),
	]);
}

function candidateOrder(
	backend: ExecutionBackend,
	cwd: string,
	left: string,
	right: string,
): number {
	const leftRelative = backend.paths.relative(cwd, left).replaceAll("\\", "/");
	const rightRelative = backend.paths.relative(cwd, right).replaceAll("\\", "/");
	const depth = (path: string) => path.split("/").filter(Boolean).length;
	const depthDifference = depth(leftRelative) - depth(rightRelative);
	if (depthDifference) return depthDifference;
	return leftRelative < rightRelative ? -1 : leftRelative > rightRelative ? 1 : 0;
}

function remember(key: string, entry: CacheEntry): void {
	cache.delete(key);
	cache.set(key, entry);
	while (cache.size > DISCOVERY_CACHE_MAX_ENTRIES)
		cache.delete(cache.keys().next().value as string);
}

function isBoundedGlobUnsupported(error: unknown): boolean {
	return /glob\.bounded\.v1|DeviceCapabilityError/i.test(String(error));
}

function candidatePath(backend: ExecutionBackend, cwd: string, marker: string): string | undefined {
	if (backend.paths.isAbsolute(marker)) return undefined;
	const segments = marker.replaceAll("\\", "/").split("/");
	if (
		segments.length < 2 ||
		segments.length > 4 ||
		segments.at(-1) !== ".git" ||
		segments.some((segment) => !segment || segment === "." || segment === "..")
	)
		return undefined;
	const path = backend.paths.resolve(cwd, backend.paths.dirname(marker));
	return backend.paths.contains(cwd, path) ? path : undefined;
}

/** Returns only lexical candidate paths. Authorization and Git probing remain request-local. */
export async function discoverGitWorkspacePaths(
	backend: ExecutionBackend,
	cwd: string,
	signal?: AbortSignal,
): Promise<GitWorkspaceDiscovery> {
	const started = performance.now();
	const key = cacheKey(backend, cwd);
	const cached = cache.get(key);
	if (cached && cached.expiresAt > Date.now()) {
		return { paths: [...cached.paths], truncated: cached.truncated, unsupported: false };
	}
	if (cached) cache.delete(key);

	const controller = new AbortController();
	const relayAbort = () => controller.abort(signal?.reason);
	signal?.addEventListener("abort", relayAbort, { once: true });
	if (signal?.aborted) relayAbort();
	const timer = setTimeout(
		() => controller.abort(new Error("Git repository discovery timed out")),
		GIT_WORKSPACE_DISCOVERY_BUDGET_MS,
	);
	try {
		const markers: string[] = [];
		let bytes = 2;
		let truncated = false;
		scan: for (const pattern of DISCOVERY_PATTERNS) {
			const remainingResults = DISCOVERY_MAX_CANDIDATES - markers.length;
			const remainingBytes = DISCOVERY_MAX_BYTES - bytes;
			if (remainingResults <= 0 || remainingBytes <= 0) {
				truncated = true;
				break;
			}
			let matches: GlobMatches;
			try {
				matches = await backend.glob(pattern, {
					cwd,
					dot: true,
					includeDirectories: true,
					maxResults: remainingResults,
					maxBytes: remainingBytes,
					timeoutMs: Math.max(
						1,
						Math.floor(GIT_WORKSPACE_DISCOVERY_BUDGET_MS - (performance.now() - started)),
					),
					signal: controller.signal,
				});
			} catch (error) {
				if (backend.kind === "remote" && isBoundedGlobUnsupported(error)) {
					return { paths: [], truncated: false, unsupported: true };
				}
				throw error;
			}
			for (const marker of matches) {
				const size = Buffer.byteLength(JSON.stringify(marker)) + 1;
				if (markers.length >= DISCOVERY_MAX_CANDIDATES || bytes + size > DISCOVERY_MAX_BYTES) {
					truncated = true;
					break scan;
				}
				markers.push(marker);
				bytes += size;
			}
			if (matches.truncated) {
				truncated = true;
				break;
			}
		}
		const paths = [
			...new Map(
				markers.flatMap((marker) => {
					const path = candidatePath(backend, cwd, marker);
					return path ? [[backend.paths.identityKey(path), path] as const] : [];
				}),
			).values(),
		].sort((left, right) => candidateOrder(backend, cwd, left, right));
		const entry = {
			paths,
			truncated,
			expiresAt: Date.now() + DISCOVERY_CACHE_TTL_MS,
		};
		remember(key, entry);
		return { paths: [...paths], truncated: entry.truncated, unsupported: false };
	} finally {
		clearTimeout(timer);
		signal?.removeEventListener("abort", relayAbort);
		const elapsedMs = Math.round(performance.now() - started);
		if (elapsedMs >= 1000)
			logger.warn("Slow Git repository discovery", {
				deviceId: backend.deviceId,
				elapsedMs,
			});
	}
}

export function clearGitWorkspaceDiscoveryCache(): void {
	cache.clear();
}
