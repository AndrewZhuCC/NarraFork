/**
 * Filesystem walk for the storage scan.
 *
 * Directory measurement used to run on the server event loop. A large worktree
 * is hundreds of thousands of `lstat`s; doing that beside HTTP, WebSocket, and
 * agent sessions is what made the storage page look frozen. This thread owns
 * the walk. It imports nothing from `server/db`.
 */

import { parentPort } from "node:worker_threads";
import { DirectoryMeasureAbortedError, measureDirectoryTrees } from "./measure-tree";

interface MeasureMessage {
	type: "measure";
	requestId: string;
	roots: string[];
	maxEntries: number;
	maxDepth: number;
}

interface CancelMessage {
	type: "cancel";
	requestId: string;
}

type Inbound = MeasureMessage | CancelMessage;

const aborts = new Map<string, AbortController>();

function post(message: Record<string, unknown>): void {
	parentPort?.postMessage(message);
}

async function runMeasure(message: MeasureMessage): Promise<void> {
	const controller = new AbortController();
	aborts.set(message.requestId, controller);
	try {
		const roots = await measureDirectoryTrees(message.roots, {
			maxEntries: message.maxEntries,
			maxDepth: message.maxDepth,
			signal: controller.signal,
			onProgress: (progress) => {
				post({ type: "progress", requestId: message.requestId, ...progress });
			},
		});
		post({ type: "result", requestId: message.requestId, roots });
	} catch (error) {
		const aborted = controller.signal.aborted || error instanceof DirectoryMeasureAbortedError;
		post({
			type: "error",
			requestId: message.requestId,
			message: aborted ? "aborted" : error instanceof Error ? error.message : String(error),
			aborted,
		});
	} finally {
		aborts.delete(message.requestId);
	}
}

parentPort?.on("message", (message: Inbound) => {
	if (!message || typeof message !== "object") return;
	if (message.type === "cancel") {
		aborts.get(message.requestId)?.abort();
		return;
	}
	if (message.type === "measure") {
		void runMeasure(message);
	}
});

post({ type: "ready" });
