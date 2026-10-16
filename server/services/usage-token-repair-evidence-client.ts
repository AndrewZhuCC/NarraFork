import { Worker } from "node:worker_threads";
import {
	TOKEN_REPAIR_MAX_DUMP_BYTES,
	type TokenRepairProtocolOverride,
} from "@shared/usage-token-repair";
import type { RepairEvidence } from "../lib/usage-token-repair-evidence";
import type { RepairRequestRow } from "./usage-token-repair-store";
import {
	tokenRepairWorkerEntry,
	tokenRepairWorkerSpecifiers,
} from "./usage-token-repair-worker-paths";

type Hint = TokenRepairProtocolOverride["protocol"];
interface Task {
	id: number;
	row: RepairRequestRow;
	rawDumpJson: string | null;
	signal: AbortSignal;
	hint?: Hint;
	bytes: number;
	resolve: (result: RepairEvidence) => void;
	reject: (error: Error) => void;
	abort: () => void;
}
interface Slot {
	worker?: Worker;
	ready?: Promise<void>;
	task?: Task;
	timer?: ReturnType<typeof setTimeout>;
	generation: number;
	dispatched?: boolean;
}
export interface RepairEvidencePool {
	analyze(
		row: RepairRequestRow,
		raw: string | null,
		signal: AbortSignal,
		hint?: Hint,
	): Promise<RepairEvidence>;
	close(): Promise<void>;
}
/** Test seam only; production always uses real, killable isolates and the 5s deadline. */
export interface RepairEvidencePoolOptions {
	createWorker?: (specifier: string | URL) => Worker;
	timeoutMs?: number;
}

function missingEvidence(row: RepairRequestRow, reason: string): RepairEvidence {
	return {
		classification: "insufficient",
		protocol: "unknown",
		reason,
		evidence: "",
		after: null,
		before: {
			inputTokens: row.inputTokens,
			cachedInputTokens: row.cachedInputTokens,
			cacheCreationInputTokens: row.cacheCreationInputTokens,
			cacheCreation5mTokens: row.cacheCreation5mTokens,
			cacheCreation1hTokens: row.cacheCreation1hTokens,
		},
	};
}

export function createRepairEvidencePool(
	directory: string,
	size = 2,
	options: RepairEvidencePoolOptions = {},
): RepairEvidencePool {
	if (!Number.isInteger(size) || size < 1 || size > 4)
		throw new Error("Evidence pool size must be 1..4");
	const slots: Slot[] = Array.from({ length: size }, () => ({ generation: 0 }));
	const queue: Task[] = [];
	const terminating = new Set<Promise<unknown>>();
	let closed = false;
	let sequence = 0;
	let retainedBytes = 0;
	let active = 0;
	const timeout = Math.min(5000, options.timeoutMs ?? 5000);
	const terminate = (slot: Slot) => {
		const worker = slot.worker;
		slot.worker = undefined;
		slot.ready = undefined;
		slot.generation++;
		if (worker) {
			const termination = worker.terminate().catch(() => {});
			terminating.add(termination);
			void termination.finally(() => terminating.delete(termination));
		}
	};
	const finish = (slot: Slot, error?: Error, result?: RepairEvidence) => {
		const task = slot.task;
		if (!task) return;
		slot.task = undefined;
		clearTimeout(slot.timer);
		task.signal.removeEventListener("abort", task.abort);
		retainedBytes -= TOKEN_REPAIR_MAX_DUMP_BYTES;
		active--;
		if (error) {
			terminate(slot);
			task.reject(error);
		} else if (result) task.resolve(result);
		pump();
	};
	const skipFailedRecord = (slot: Slot, reason: string, error: Error) => {
		const task = slot.task;
		if (!task || !slot.dispatched) return finish(slot, error);
		terminate(slot);
		finish(slot, undefined, missingEvidence(task.row, reason));
	};
	const startWorker = async (slot: Slot): Promise<void> => {
		for (const specifier of tokenRepairWorkerSpecifiers("usage-token-repair-evidence-worker")) {
			if (closed || !slot.task) throw new Error("Evidence analysis cancelled");
			const worker = options.createWorker
				? options.createWorker(tokenRepairWorkerEntry(specifier))
				: new Worker(tokenRepairWorkerEntry(specifier));
			slot.worker = worker;
			const generation = slot.generation;
			let ready = false;
			try {
				await new Promise<void>((resolve, reject) => {
					worker.on("error", (error) => {
						if (!ready) reject(error);
						else if (slot.generation === generation)
							skipFailedRecord(
								slot,
								"evidence_worker_failed",
								error instanceof Error ? error : new Error("Evidence worker failed"),
							);
					});
					worker.on("exit", () => {
						const error = new Error("Evidence worker exited");
						if (!ready) reject(error);
						else if (slot.generation === generation)
							skipFailedRecord(
								slot,
								"evidence_worker_failed",
								error instanceof Error ? error : new Error("Evidence worker failed"),
							);
					});
					worker.on(
						"message",
						(message: { ready?: string; id?: number; result?: RepairEvidence; error?: string }) => {
							if (slot.generation !== generation) return;
							if (!ready && message.ready === "token-repair-evidence-v1") {
								ready = true;
								resolve();
							} else if (ready && message.id === slot.task?.id) {
								if (message.result) finish(slot, undefined, message.result);
								else
									skipFailedRecord(
										slot,
										"evidence_worker_failed",
										new Error(message.error ?? "Evidence analysis failed"),
									);
							}
						},
					);
				});
				return;
			} catch {
				if (slot.generation !== generation) throw new Error("Evidence analysis cancelled");
				terminate(slot);
			}
		}
		throw new Error("Evidence worker is unavailable");
	};
	const pump = () => {
		if (closed) return;
		for (const slot of slots) {
			// Reserve a full 32MiB per active record, including any spill not read yet.
			if (slot.task || active >= 2 || !queue.length) continue;
			if (retainedBytes - queue[0].bytes + TOKEN_REPAIR_MAX_DUMP_BYTES > 64 * 1024 * 1024) continue;
			const task = queue.shift();
			if (!task) continue;
			slot.task = task;
			slot.dispatched = false;
			retainedBytes += TOKEN_REPAIR_MAX_DUMP_BYTES - task.bytes;
			active++;
			slot.timer = setTimeout(
				() =>
					skipFailedRecord(
						slot,
						"evidence_timeout",
						new Error("Evidence exceeded the five-second limit"),
					),
				timeout,
			);
			slot.ready ??= startWorker(slot);
			void slot.ready
				.then(() => {
					if (slot.task !== task) return;
					slot.ready = Promise.resolve();
					slot.dispatched = true;
					slot.worker?.postMessage({
						id: task.id,
						row: task.row,
						rawDumpJson: task.rawDumpJson,
						dumpDirectory: directory,
						protocolHint: task.hint,
					});
				})
				.catch((error: Error) => {
					if (slot.task === task) finish(slot, error);
				});
		}
	};
	return {
		analyze(row, rawDumpJson, signal, hint) {
			if (closed) return Promise.reject(new Error("Evidence pool is closed"));
			if (signal.aborted) return Promise.reject(new Error("Evidence analysis cancelled"));
			if (!rawDumpJson) return Promise.resolve(missingEvidence(row, "dump_missing"));
			const bytes = Buffer.byteLength(rawDumpJson);
			if (
				bytes > TOKEN_REPAIR_MAX_DUMP_BYTES ||
				retainedBytes + bytes > 64 * 1024 * 1024 ||
				queue.length >= 64
			)
				return Promise.reject(new Error("Evidence pool byte or queue budget exceeded"));
			return new Promise<RepairEvidence>((resolve, reject) => {
				const task: Task = {
					id: ++sequence,
					row,
					rawDumpJson,
					signal,
					hint,
					bytes,
					resolve,
					reject,
					abort: () => {},
				};
				task.abort = () => {
					const slot = slots.find((entry) => entry.task === task);
					if (slot) finish(slot, new Error("Evidence analysis cancelled"));
					else {
						const index = queue.indexOf(task);
						if (index < 0) return;
						queue.splice(index, 1);
						retainedBytes -= bytes;
						signal.removeEventListener("abort", task.abort);
						reject(new Error("Evidence analysis cancelled"));
					}
				};
				retainedBytes += bytes;
				queue.push(task);
				signal.addEventListener("abort", task.abort, { once: true });
				if (signal.aborted) task.abort();
				pump();
			});
		},
		async close() {
			closed = true;
			for (const task of queue.splice(0)) {
				retainedBytes -= task.bytes;
				task.signal.removeEventListener("abort", task.abort);
				task.reject(new Error("Evidence pool closed"));
			}
			for (const slot of slots) {
				if (slot.task) finish(slot, new Error("Evidence pool closed"));
				else terminate(slot);
			}
			await Promise.all([...terminating]);
		},
	};
}

/** Legacy one-shot entry point. Bulk scans must retain one pool for their whole task. */
export async function analyzeRepairRecord(
	row: RepairRequestRow,
	raw: string | null,
	directory: string,
	signal: AbortSignal,
	hint?: Hint,
): Promise<RepairEvidence> {
	const pool = createRepairEvidencePool(directory, 1);
	try {
		return await pool.analyze(row, raw, signal, hint);
	} finally {
		await pool.close();
	}
}
