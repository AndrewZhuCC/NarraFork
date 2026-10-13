import { parentPort, workerData } from "node:worker_threads";
import { canWriteOptionalDiskData } from "./disk-safety";
import { AppError } from "./errors";
import { logger } from "./logger";
import type { RecorderMessage, RecorderReply } from "./request-recorder-protocol";
import { RequestRecorderStore } from "./request-recorder-store";
import { settings } from "./settings";

const store = new RequestRecorderStore((workerData as { home: string }).home, () =>
	canWriteOptionalDiskData((workerData as { home: string }).home),
);
let queue = Promise.resolve();
const controllers = new Map<number, AbortController>();
const cancelled = new Set<number>();
let activeListings = 0;
function reply(id: number, value?: unknown, error?: unknown): void {
	const message: RecorderReply = error
		? {
				id,
				error: {
					message:
						error instanceof AppError ? error.message.slice(0, 2048) : "Recording operation failed",
					status: error instanceof AppError ? error.statusCode : 500,
					code: error instanceof AppError ? error.code : "RECORDING_FAILED",
				},
			}
		: { id, value };
	parentPort?.postMessage(message);
}
parentPort?.on("message", (message: RecorderMessage | { cancel: number }) => {
	if ("cancel" in message) {
		const controller = controllers.get(message.cancel);
		if (controller) controller.abort();
		else {
			if (cancelled.size >= 128) {
				const oldest = cancelled.values().next().value;
				if (oldest !== undefined) cancelled.delete(oldest);
			}
			cancelled.add(message.cancel);
		}
		return;
	}
	if (message.op.type === "list") {
		if (cancelled.delete(message.id)) {
			reply(
				message.id,
				undefined,
				new AppError("Recording listing cancelled", 499, "RECORDING_CANCELLED"),
			);
			return;
		}
		if (activeListings >= 2) {
			reply(
				message.id,
				undefined,
				new AppError("Recording listings are busy", 429, "RECORDING_BUSY"),
			);
			return;
		}
		const controller = new AbortController();
		controllers.set(message.id, controller);
		activeListings++;
		const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(30_000)]);
		// Read-only directory queries cannot stall the ordered transport capture queue.
		void store
			.list(message.op.narratorId, message.op.cursor, message.op.limit, signal)
			.then(
				(value) => reply(message.id, value),
				(error) => reply(message.id, undefined, error),
			)
			.finally(() => {
				activeListings--;
				controllers.delete(message.id);
			});
		return;
	}
	queue = queue
		.then(async () => {
			if (cancelled.delete(message.id)) {
				reply(message.id, undefined, new Error("Recording operation cancelled"));
				return;
			}
			const started = performance.now();
			try {
				if (message.op.type === "configure" && message.op.diskSafety)
					settings.diskSafety = { ...message.op.diskSafety };
				if (message.op.type === "detail") {
					if (activeListings >= 2)
						throw new AppError("Recording queries are busy", 429, "RECORDING_BUSY");
					const controller = new AbortController();
					controllers.set(message.id, controller);
					activeListings++;
					const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(30_000)]);
					// Preview reads and saved lookups do not block ordered transport capture.
					void store
						.detail(message.op.narratorId, message.op.requestId, signal)
						.then(
							(value) => reply(message.id, value),
							(error) => reply(message.id, undefined, error),
						)
						.finally(() => {
							activeListings--;
							controllers.delete(message.id);
							const elapsedMs = performance.now() - started;
							if (elapsedMs > 1000) logger.warn("Slow request recording preview", { elapsedMs });
						});
				} else if (message.op.type === "save-specific") {
					const controller = new AbortController();
					controllers.set(message.id, controller);
					try {
						const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(30_000)]);
						reply(message.id, await store.execute(message.op, signal));
					} finally {
						controllers.delete(message.id);
					}
				} else if (message.op.type === "archive") {
					const controller = new AbortController();
					controllers.set(message.id, controller);
					const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(60_000)]);
					const job = await store.prepareArchive(message.op.narratorId, message.op.saveId, signal);
					// ZIP compression and I/O are independent of the ordered capture queue.
					void store
						.buildArchive(job, signal)
						.then(
							(value) => reply(message.id, value),
							(error) => reply(message.id, undefined, error),
						)
						.finally(() => controllers.delete(message.id));
				} else reply(message.id, await store.execute(message.op));
			} catch (error) {
				controllers.delete(message.id);
				reply(message.id, undefined, error);
			} finally {
				const elapsedMs = performance.now() - started;
				if (elapsedMs > 1000)
					logger.warn("Slow request recorder operation", { type: message.op.type, elapsedMs });
			}
		})
		.catch((error) => logger.warn("Request recorder queue failed", { error: String(error) }));
});
let flushing = false;
const checkpoint = setInterval(() => {
	if (flushing) return;
	flushing = true;
	queue = queue
		.then(() => store.flush())
		.catch((error) => logger.warn("Request recording checkpoint failed", { error: String(error) }))
		.finally(() => {
			flushing = false;
		});
}, 250);
checkpoint.unref();
parentPort?.postMessage({ ready: true });
