import { Worker } from "node:worker_threads";
import {
	TOKEN_REPAIR_MAX_DUMP_BYTES,
	type TokenRepairEvidence,
	type TokenRepairProtocolOverride,
} from "@shared/usage-token-repair";
import type { RepairRequestRow } from "./usage-token-repair-store";
import {
	tokenRepairWorkerEntry,
	tokenRepairWorkerSpecifiers,
} from "./usage-token-repair-worker-paths";

/** Even a synchronous JSON parser is killable: each bounded record has its own isolate. */
export async function analyzeRepairRecord(
	row: RepairRequestRow,
	rawDumpJson: string | null,
	dumpDirectory: string,
	signal: AbortSignal,
	protocolHint?: TokenRepairProtocolOverride["protocol"],
): Promise<TokenRepairEvidence> {
	signal.throwIfAborted();
	const fallback = (reason: string): TokenRepairEvidence => ({
		classification: "insufficient",
		protocol: "unknown",
		...(protocolHint ? { manualProtocol: protocolHint } : {}),
		reason,
		evidence: "No complete, attributable usage evidence was accepted.",
		before: {
			inputTokens: row.inputTokens,
			cachedInputTokens: row.cachedInputTokens,
			cacheCreationInputTokens: row.cacheCreationInputTokens,
			cacheCreation5mTokens: row.cacheCreation5mTokens,
			cacheCreation1hTokens: row.cacheCreation1hTokens,
		},
		after: null,
	});
	if (rawDumpJson !== null && Buffer.byteLength(rawDumpJson) > TOKEN_REPAIR_MAX_DUMP_BYTES)
		return fallback("Evidence exceeds the per-record byte budget.");
	const deadline = Date.now() + 5000;
	for (const specifier of tokenRepairWorkerSpecifiers("usage-token-repair-evidence-worker")) {
		let worker: Worker | undefined;
		let ready = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		let abort = () => {};
		try {
			return await new Promise<TokenRepairEvidence>((resolve, reject) => {
				worker = new Worker(tokenRepairWorkerEntry(specifier));
				abort = () => reject(new Error("Evidence analysis cancelled"));
				signal.addEventListener("abort", abort, { once: true });
				if (signal.aborted) return abort();
				timer = setTimeout(
					() => reject(new Error("Evidence analysis exceeded five seconds")),
					Math.max(1, deadline - Date.now()),
				);
				worker.once("error", reject);
				worker.once("exit", () => reject(new Error("Evidence worker exited")));
				worker.on(
					"message",
					(message: { ready?: string; result?: TokenRepairEvidence; error?: string }) => {
						if (!ready && message.ready === "token-repair-evidence-v1") {
							ready = true;
							worker?.postMessage({ row, rawDumpJson, dumpDirectory, protocolHint });
						} else if (ready && message.result) resolve(message.result);
						else reject(new Error("Evidence analysis failed"));
					},
				);
			});
		} catch {
			if (signal.aborted) signal.throwIfAborted();
			if (Date.now() >= deadline) return fallback("Evidence exceeded the five-second limit.");
			if (ready) throw new Error("Evidence worker failed; start a new analysis.");
		} finally {
			clearTimeout(timer);
			signal.removeEventListener("abort", abort);
			await worker?.terminate();
		}
	}
	// Infrastructure failure is not missing historical evidence: do not mark rows reviewed.
	throw new Error("Evidence worker is unavailable; start a new analysis.");
}
