import { Worker } from "node:worker_threads";
import { isCompiledRuntime } from "./runtime-target";

/**
 * How to load a worker inside `bun build --compile`.
 *
 * Bun's own docs list `new Worker("./worker.ts")`, `new Worker(new URL(..., import.meta.url))`,
 * and the `.href` form as equivalent. In a compiled binary they are not:
 *
 * - Every module's `import.meta.url` is the executable (`file:///$bunfs/root/<name>` on
 *   Linux/macOS, `file:///C:/%7EBUN/root/<name>.exe` on Windows), not that module's source
 *   path. A sibling `./worker.ts` URL therefore does not name the worker file.
 * - Extra entry points are stored under the common directory of the compile entries, with
 *   `.ts` rewritten to `.js`. Production entries all live under `server/`, so
 *   `server/services/foo.ts` becomes `/$bunfs/root/services/foo.js`. A probe whose main
 *   entry sits outside `server/` keeps the prefix: `/$bunfs/root/server/services/foo.js`.
 *   One hard-coded path fails with `Cannot find module '/$bunfs/root/...'`.
 * - `new Worker("file://...")` throws synchronously ("Wrap file:// URLs with `new URL`").
 *   A `URL` object works for `$bunfs` on POSIX when the embedded key matches.
 * - On Windows, `new Worker(fileURL)` converts the URL to a backslash path and misses Bun's
 *   virtual key. Pass `C:/~BUN/root/...` with forward slashes and no scheme.
 * - A relative `./file.js` string is resolved against the process cwd on Bun 1.4, not against
 *   the embedded graph (oven-sh/bun#40619).
 *
 * See oven-sh/bun#15981, #29124, #36286, #40619, and #32730. The build list is
 * `shared/compiled-worker-entries.ts`.
 */

const WINDOWS_VIRTUAL_ROOT = /^\/[a-z]:\/(?:~BUN|%7eBUN)\/root\//i;

/**
 * Specifiers to try, most likely first.
 *
 * `serverRelativePath` is the worker under `server/` (`services/foo.ts`).
 * `devRelativePath` is relative to the caller's `moduleUrl` and is the only candidate in source
 * runs. Callers must pass their own `import.meta.url`: a default here would be this file.
 */
export function compiledWorkerSpecifiers(
	serverRelativePath: string,
	devRelativePath: string,
	moduleUrl: string,
	compiled = isCompiledRuntime(),
): string[] {
	if (!compiled) return [new URL(devRelativePath, moduleUrl).href];
	const embedded = serverRelativePath.replace(/\.tsx?$/, ".js");
	const base = embedded.slice(embedded.lastIndexOf("/") + 1);
	const relative = [`./${embedded}`, `./server/${embedded}`, `./${base}`];
	return [...new Set(relative)].map((path) => new URL(path, moduleUrl).href);
}

/**
 * Turn a specifier into the value `new Worker` accepts.
 * Real `file:` URLs stay URL objects. Windows `~BUN` keys stay forward-slash strings.
 * Absolute filesystem paths and `./` relatives pass through unchanged.
 */
export function workerEntryFromSpecifier(specifier: string): string | URL {
	let url: URL;
	try {
		url = new URL(specifier);
	} catch {
		return specifier;
	}
	if (url.protocol !== "file:") return specifier;
	if (WINDOWS_VIRTUAL_ROOT.test(url.pathname)) return decodeURIComponent(url.pathname.slice(1));
	return url;
}

export interface ConnectedWorker {
	worker: Worker;
	specifier: string;
}

/**
 * Start the first candidate that reports ready. A missing embedded module fails on the
 * worker's `error` event (or a synchronous throw for a rejected filename) and the next
 * layout is tried. The returned worker has had its startup listeners removed.
 */
export async function connectCompiledWorker(
	specifiers: readonly string[],
	options: {
		signal?: AbortSignal;
		timeoutMs?: number;
		isReady?: (message: unknown) => boolean;
	} = {},
): Promise<ConnectedWorker> {
	const timeoutMs = options.timeoutMs ?? 5_000;
	const isReady =
		options.isReady ??
		((message: unknown) =>
			!!message && typeof message === "object" && (message as { ready?: boolean }).ready === true);
	const failures: string[] = [];
	for (const specifier of specifiers) {
		options.signal?.throwIfAborted();
		try {
			const worker = await startOne(specifier, timeoutMs, isReady, options.signal);
			return { worker, specifier };
		} catch (error) {
			if (options.signal?.aborted) throw error;
			failures.push(`${specifier}: ${error instanceof Error ? error.message : String(error)}`);
		}
	}
	throw new Error(`No embedded worker entry loaded (${failures.join(" | ")})`);
}

function startOne(
	specifier: string,
	timeoutMs: number,
	isReady: (message: unknown) => boolean,
	signal?: AbortSignal,
): Promise<Worker> {
	return new Promise((resolve, reject) => {
		let settled = false;
		let thread: Worker | undefined;
		const finish = (error: unknown, value?: Worker) => {
			if (settled) return;
			settled = true;
			clearTimeout(timer);
			signal?.removeEventListener("abort", onAbort);
			thread?.off("error", onError);
			thread?.off("exit", onExit);
			thread?.off("message", onMessage);
			if (error) {
				// Keep a listener so termination after a missed entry cannot surface as unhandled.
				thread?.on("error", () => {});
				if (thread) void thread.terminate();
				reject(error);
			} else if (value) resolve(value);
		};
		const onAbort = () => finish(signal?.reason ?? new Error("worker start aborted"));
		const onError = (error: Error) => finish(error);
		const onExit = () => finish(new Error("worker exited before ready"));
		const onMessage = (message: unknown) => {
			if (isReady(message)) finish(undefined, thread);
		};
		const timer = setTimeout(() => finish(new Error("worker did not become ready")), timeoutMs);
		try {
			thread = new Worker(workerEntryFromSpecifier(specifier));
		} catch (error) {
			settled = true;
			clearTimeout(timer);
			reject(error);
			return;
		}
		thread.on("error", onError);
		thread.on("exit", onExit);
		thread.on("message", onMessage);
		signal?.addEventListener("abort", onAbort, { once: true });
		// Ask after the listeners exist. A startup message can be lost if the worker
		// thread posts it before the parent subscribes; a missing module errors instead.
		try {
			thread.postMessage({ action: "ping" });
		} catch (error) {
			finish(error);
		}
	});
}
