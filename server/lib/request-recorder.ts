import { unlink } from "node:fs/promises";
import { Worker } from "node:worker_threads";
import {
	DEFAULT_RECORDING_REQUEST_BYTES,
	DEFAULT_RECORDING_RESPONSE_BYTES,
	MAX_RECORDING_REQUEST_BYTES,
	MAX_RECORDING_RESPONSE_BYTES,
	MIN_RECORDING_WINDOW_BYTES,
	type RecordingFrame,
	type RecordingIdentity,
	type RecordingPinReason,
	type RecordingRequest,
	type RecordingSettings,
	type RequestRecording,
	type RequestRecordingDetail,
	type SavedRecording,
} from "../../shared/request-recording";
import { compiledWorkerSpecifiers, workerEntryFromSpecifier } from "./compiled-worker";
import { diskSafetySettings } from "./disk-safety";
import { AppError } from "./errors";
import { hotSafe } from "./hot-safe";
import { logger } from "./logger";
import { getNarraforkHome } from "./narrafork-home";
import {
	RECORDING_CHUNK_BYTES,
	RECORDING_DOWNLOAD_LEASE_MS,
	RECORDING_MAX_DOWNLOADS,
	RECORDING_QUEUE_BYTES,
	type RecorderMessage,
	type RecorderOperation,
	type RecorderReply,
	type RecordingSide,
} from "./request-recorder-protocol";
import { sanitizeRecordingMetadata } from "./request-recorder-sanitize";
import { settings } from "./settings";

export { REQUEST_RECORDING_DIR, REQUEST_RECORDING_SAVED_DIR } from "./request-recorder-protocol";

function capacity(value: unknown, fallback: number, maximum: number): number {
	if (typeof value !== "number" || !Number.isFinite(value)) return fallback;
	const bytes = Math.max(0, Math.min(maximum, Math.floor(value)));
	return bytes > 0 && bytes < MIN_RECORDING_WINDOW_BYTES ? 0 : bytes;
}
export function requestRecordingSettings(): RecordingSettings {
	const evidence = (settings.requestEvidence ?? {}) as Partial<RecordingSettings>;
	return {
		ringEnabled: evidence.ringEnabled !== false,
		ringRequestBytes: capacity(
			evidence.ringRequestBytes,
			DEFAULT_RECORDING_REQUEST_BYTES,
			MAX_RECORDING_REQUEST_BYTES,
		),
		ringResponseBytes: capacity(
			evidence.ringResponseBytes,
			DEFAULT_RECORDING_RESPONSE_BYTES,
			MAX_RECORDING_RESPONSE_BYTES,
		),
		autoPin: evidence.autoPin === "all" || evidence.autoPin === "errors" ? evidence.autoPin : "off",
	};
}
export { sanitizeRecordingMetadata, sanitizeRecordingUrl } from "./request-recorder-sanitize";

interface QueuedOperation {
	id: number;
	op: RecorderOperation;
	bytes: number;
}
interface Waiter {
	resolve: (value: unknown) => void;
	reject: (error: unknown) => void;
	timer: ReturnType<typeof setTimeout>;
	abort?: () => void;
	signal?: AbortSignal;
}

/** A single bounded mailbox. Worker compression never blocks the ingestion queue. */
export class RequestRecorderClient {
	private worker?: Worker;
	private starting?: Promise<void>;
	private ready = false;
	private sequence = 0;
	private queued: QueuedOperation[] = [];
	private queuedBytes = 0;
	private inflight = new Map<number, number>();
	private inflightBytes = 0;
	private waiters = new Map<number, Waiter>();
	private lastConfig = "";
	private lastWarning = 0;
	private retryAt = 0;
	private closed = false;
	private generation = 0;
	// Admission lasts through end ACK, including begins still in the mailbox and closing requests.
	private recordings = new Map<string, { beginId: number; endId?: number; bound?: boolean }>();
	private lifecycle = new Map<number, { type: "begin" | "end"; requestId: string }>();
	private static readonly MAX_RECORDINGS = 128;
	private static readonly MAX_OPERATIONS = 2048;
	private static readonly CONTROL_BYTES = 16 * 1024;
	private static readonly GAP_BYTES = 2048;
	constructor(readonly home: string) {}
	private warn(error: unknown): void {
		if (Date.now() - this.lastWarning < 10_000) return;
		this.lastWarning = Date.now();
		logger.warn("Request recording unavailable or incomplete", { error: String(error) });
	}
	private start(): Promise<void> {
		if (this.starting) return this.starting;
		const generation = ++this.generation;
		this.starting = (async () => {
			const paths = compiledWorkerSpecifiers(
				"lib/request-recorder-worker.ts",
				"./request-recorder-worker.ts",
				import.meta.url,
			);
			let failure: unknown;
			for (const path of paths) {
				if (this.closed || generation !== this.generation) return;
				const worker = new Worker(workerEntryFromSpecifier(path), {
					workerData: { home: this.home },
				});
				this.worker = worker;
				// Bun may emit a probe failure more than once, including during termination.
				// Keep a listener on retired workers too; active failures are handled below.
				worker.on("error", () => {});
				try {
					await new Promise<void>((resolveReady, reject) => {
						const timer = setTimeout(
							() => reject(new Error("Recording worker startup timeout")),
							10_000,
						);
						const onError = (error: Error) => {
							clearTimeout(timer);
							reject(error);
						};
						worker.once("error", onError);
						worker.once("exit", () => onError(new Error("Recording worker exited during startup")));
						worker.once("message", (message: { ready?: boolean }) => {
							clearTimeout(timer);
							worker.removeListener("error", onError);
							if (message.ready) resolveReady();
							else reject(new Error("Recording worker did not become ready"));
						});
					});
					if (this.closed || generation !== this.generation) {
						await worker.terminate();
						return;
					}
					this.ready = true;
					worker.on("message", (message: RecorderReply) => {
						if (this.worker === worker) this.onReply(message);
					});
					worker.on("error", (error) => {
						if (this.worker === worker) this.fail(error);
					});
					worker.on("exit", (code) => {
						if (!this.closed && this.worker === worker)
							this.fail(new Error(`Recording worker exited (${code})`));
					});
					this.pump();
					return;
				} catch (error) {
					failure = error;
					await worker.terminate();
				}
			}
			throw failure ?? new Error("Recording worker unavailable");
		})().catch((error) => {
			if (generation === this.generation) this.fail(error);
			throw error;
		});
		return this.starting;
	}
	private fail(error: unknown): void {
		const worker = this.worker;
		this.worker = undefined;
		this.generation++;
		if (worker) void worker.terminate().catch(() => {});
		this.ready = false;
		this.starting = undefined;
		this.lastConfig = "";
		this.retryAt = Date.now() + 10_000;
		if (!this.closed) this.warn(error);
		for (const id of [...this.waiters.keys()]) this.settle(id, undefined, error);
		this.queued = [];
		this.queuedBytes = 0;
		this.inflight.clear();
		this.inflightBytes = 0;
		this.recordings.clear();
		this.lifecycle.clear();
	}
	private settle(id: number, value?: unknown, error?: unknown): void {
		const waiter = this.waiters.get(id);
		if (!waiter) return;
		clearTimeout(waiter.timer);
		if (waiter.abort && waiter.signal) waiter.signal.removeEventListener("abort", waiter.abort);
		this.waiters.delete(id);
		if (error) waiter.reject(error);
		else waiter.resolve(value);
	}
	private onReply(message: RecorderReply): void {
		this.inflightBytes -= this.inflight.get(message.id) ?? 0;
		this.inflight.delete(message.id);
		const error = message.error
			? new AppError(message.error.message, message.error.status, message.error.code)
			: undefined;
		const lifecycle = this.lifecycle.get(message.id);
		this.lifecycle.delete(message.id);
		if (lifecycle) {
			const recording = this.recordings.get(lifecycle.requestId);
			if (lifecycle.type === "begin" && error && recording?.beginId === message.id)
				this.release(lifecycle.requestId, error);
			else if (lifecycle.type === "end" && recording?.endId === message.id)
				this.recordings.delete(lifecycle.requestId);
		}
		if (error && !this.waiters.has(message.id)) this.warn(error);
		this.settle(message.id, message.value, error);
		this.pump();
	}
	private release(requestId: string, error: unknown): void {
		this.recordings.delete(requestId);
		this.queued = this.queued.filter((item) => {
			if (!("requestId" in item.op) || item.op.requestId !== requestId) return true;
			this.queuedBytes -= item.bytes;
			this.lifecycle.delete(item.id);
			this.settle(item.id, undefined, error);
			return false;
		});
	}
	private pump(): void {
		if (!this.ready || !this.worker) return;
		while (this.queued.length && this.inflight.size < 64 && this.inflightBytes < 512 * 1024) {
			const item = this.queued.shift() as QueuedOperation;
			this.queuedBytes -= item.bytes;
			this.inflight.set(item.id, item.bytes);
			this.inflightBytes += item.bytes;
			this.worker.ref();
			const message: RecorderMessage = { id: item.id, op: item.op };
			try {
				this.worker.postMessage(
					message,
					item.op.type === "chunk" ? [item.op.data.buffer as ArrayBuffer] : [],
				);
			} catch (error) {
				this.fail(error);
				return;
			}
		}
		if (!this.queued.length && !this.inflight.size && !this.waiters.size) this.worker.unref();
	}
	private operationBytes(op: RecorderOperation): number {
		return op.type === "chunk"
			? op.data.byteLength + Buffer.byteLength(JSON.stringify({ ...op, data: undefined }))
			: Buffer.byteLength(JSON.stringify(op));
	}
	/** Reserve future end/gap/bind messages, not a second unbounded retry queue. */
	private limits(op: RecorderOperation): { bytes: number; slots: number } {
		let ending = 0;
		let binding = 0;
		for (const recording of this.recordings.values()) {
			if (!recording.endId) ending++;
			if (!recording.bound) binding++;
		}
		let bytes = ending * RequestRecorderClient.CONTROL_BYTES;
		let slots = ending;
		if (op.type === "end") return { bytes: RECORDING_QUEUE_BYTES, slots: 2048 };
		if (op.type !== "gap") {
			// Include gaps already posted but not ACKed (at most 64).
			bytes += (this.recordings.size * 2 + 64) * RequestRecorderClient.GAP_BYTES;
			slots += this.recordings.size * 2 + 64;
		}
		if (op.type !== "configure" || this.queued.some((item) => item.op.type === "configure")) {
			bytes += RequestRecorderClient.CONTROL_BYTES;
			slots++;
		}
		if (op.type === "bind") {
			const recording = this.recordings.get(op.requestId);
			if (recording && !recording.bound) binding--;
		}
		bytes += binding * RequestRecorderClient.GAP_BYTES;
		slots += binding;
		return {
			bytes: RECORDING_QUEUE_BYTES - bytes,
			slots: RequestRecorderClient.MAX_OPERATIONS - slots,
		};
	}
	private fits(bytes: number, op: RecorderOperation): boolean {
		const limit = this.limits(op);
		return (
			this.queuedBytes + this.inflightBytes + bytes <= limit.bytes &&
			this.queued.length < limit.slots
		);
	}
	private gap(op: Extract<RecorderOperation, { type: "chunk" | "meta" }>): void {
		this.enqueue({
			type: "gap",
			requestId: op.requestId,
			side: op.side,
			bytes: op.type === "chunk" ? op.data.byteLength : 0,
			reason: op.type === "chunk" ? "mailbox overload" : "mailbox overload: metadata missing",
		});
	}
	private drop(index: number): void {
		const [item] = this.queued.splice(index, 1);
		this.queuedBytes -= item.bytes;
		if (item.op.type === "chunk" || item.op.type === "meta") this.gap(item.op);
		this.settle(
			item.id,
			undefined,
			new AppError("Recording evidence dropped by overload", 429, "RECORDING_BUSY"),
		);
	}
	private enqueue(op: RecorderOperation, id = ++this.sequence): boolean {
		if (this.closed || Date.now() < this.retryAt) return false;
		let admitted = false;
		if (op.type === "begin") {
			const requestId = op.identity.requestId;
			if (
				this.recordings.size >= RequestRecorderClient.MAX_RECORDINGS ||
				this.recordings.has(requestId) ||
				Buffer.byteLength(JSON.stringify(requestId)) > 512 ||
				this.operationBytes(op) > RequestRecorderClient.CONTROL_BYTES
			)
				return false;
			this.recordings.set(requestId, { beginId: id });
			admitted = true;
		} else if (
			op.type === "chunk" ||
			op.type === "meta" ||
			op.type === "gap" ||
			op.type === "end"
		) {
			const recording = this.recordings.get(op.requestId);
			if (!recording || (recording.endId && op.type !== "gap")) return false;
			if (op.type === "end") {
				// Even adversarial strings cannot consume another recording's terminal reserve.
				if (this.operationBytes(op) > RequestRecorderClient.CONTROL_BYTES) {
					this.enqueue({
						type: "gap",
						requestId: op.requestId,
						side: "response",
						bytes: 0,
						reason: "terminal metadata truncated",
					});
					op = { ...op, error: op.error?.slice(0, 1024), status: op.status?.slice(0, 128) };
				}
			}
		}
		if (op.type === "gap") {
			op = {
				...op,
				bytes: Math.min(Number.MAX_SAFE_INTEGER, Math.max(0, op.bytes)),
				reason: op.reason.slice(0, 128),
			};
			const { requestId, side } = op;
			const previous = this.queued.find(
				(item) =>
					item.op.type === "gap" &&
					item.op.requestId === requestId &&
					item.op.side === side &&
					!this.waiters.has(item.id),
			);
			if (previous?.op.type === "gap" && !this.waiters.has(id)) {
				const merged = {
					...previous.op,
					bytes: Math.min(Number.MAX_SAFE_INTEGER, previous.op.bytes + op.bytes),
					reason:
						previous.op.reason === op.reason
							? op.reason
							: `${previous.op.reason}; ${op.reason}`.slice(0, 128),
				};
				const bytes = this.operationBytes(merged);
				// The full gap-size allowance is reserved; numerical growth is still accounted.
				this.queuedBytes += bytes - previous.bytes;
				previous.op = merged;
				previous.bytes = bytes;
				return true;
			}
		}
		if (op.type === "chunk" && op.data.buffer.byteLength !== op.data.byteLength)
			op = { ...op, data: Uint8Array.from(op.data) };
		const bytes = this.operationBytes(op);
		const maximum =
			op.type === "gap" || op.type === "bind"
				? RequestRecorderClient.GAP_BYTES
				: RequestRecorderClient.CONTROL_BYTES;
		if (op.type !== "chunk" && op.type !== "meta" && bytes > maximum) {
			if (admitted && op.type === "begin") this.recordings.delete(op.identity.requestId);
			return false;
		}
		while (!this.fits(bytes, op)) {
			const index = this.queued.findIndex(
				(item) => item.op.type === "chunk" || item.op.type === "meta",
			);
			if (index >= 0) {
				this.drop(index);
				continue;
			}
			if (op.type === "configure") {
				// Only coalesce under pressure; never overwrite normal HTTP/WS ordering.
				const configuration = this.queued.findIndex(
					(item) => item.op.type === "configure" && !this.waiters.has(item.id),
				);
				if (configuration >= 0) {
					this.drop(configuration);
					continue;
				}
			}
			if (admitted && op.type === "begin") this.recordings.delete(op.identity.requestId);
			if (op.type === "chunk" || op.type === "meta") this.gap(op);
			this.warn("control mailbox overload");
			return false;
		}
		const item = { id, op, bytes };
		if (op.type === "gap") {
			// Eviction may concern a closing recording; its missing evidence must precede end.
			const end = this.queued.findIndex(
				(queued) => queued.op.type === "end" && queued.op.requestId === op.requestId,
			);
			if (end >= 0) this.queued.splice(end, 0, item);
			else this.queued.push(item);
		} else this.queued.push(item);
		this.queuedBytes += bytes;
		if (op.type === "bind") {
			const recording = this.recordings.get(op.requestId);
			if (recording) recording.bound = true;
		}
		if (op.type === "begin")
			this.lifecycle.set(id, { type: "begin", requestId: op.identity.requestId });
		if (op.type === "end") {
			const recording = this.recordings.get(op.requestId);
			if (recording) recording.endId = id;
			this.lifecycle.set(id, { type: "end", requestId: op.requestId });
		}
		void this.start().catch(() => {});
		this.pump();
		return true;
	}
	configure(value: RecordingSettings): void {
		const configuration = { ...value, diskSafety: diskSafetySettings() };
		const serialized = JSON.stringify(configuration);
		if (serialized === this.lastConfig) return;
		if (this.enqueue({ type: "configure", settings: value, diskSafety: configuration.diskSafety }))
			this.lastConfig = serialized;
	}
	capture(op: RecorderOperation): boolean {
		try {
			return this.enqueue(op);
		} catch (error) {
			this.warn(error);
			if (op.type === "chunk" || op.type === "meta") {
				try {
					this.gap(op);
				} catch {}
			}
			return false;
		}
	}
	async call<T>(op: RecorderOperation, signal?: AbortSignal): Promise<T> {
		signal?.throwIfAborted();
		if (this.waiters.size >= 16)
			throw new AppError("Recording operations are busy", 429, "RECORDING_BUSY");
		const id = ++this.sequence;
		return new Promise<T>((resolveCall, reject) => {
			const timeout = op.type === "archive" ? 65_000 : 30_000;
			const abort = () => {
				const error = new AppError(
					"Recording operation cancelled or timed out",
					504,
					"RECORDING_TIMEOUT",
				);
				const removed = this.queued.find((item) => item.id === id);
				// Cancellation must not erase a terminal message and strand a worker record.
				if (op.type !== "end") {
					if (removed) {
						this.queuedBytes -= removed.bytes;
						this.queued = this.queued.filter((item) => item.id !== id);
						if (op.type === "chunk" || op.type === "meta") this.gap(op);
					}
					if (op.type === "begin") {
						if (removed) {
							this.lifecycle.delete(id);
							this.release(op.identity.requestId, error);
						} else
							this.capture({ type: "end", requestId: op.identity.requestId, status: "aborted" });
					} else if (this.inflight.has(id)) {
						try {
							this.worker?.postMessage({ cancel: id });
						} catch (failure) {
							this.fail(failure);
						}
					}
				}
				this.settle(id, undefined, error);
			};
			const timer = setTimeout(abort, timeout);
			this.waiters.set(id, {
				resolve: (value) => resolveCall(value as T),
				reject,
				timer,
				abort,
				signal,
			});
			signal?.addEventListener("abort", abort, { once: true });
			try {
				if (!this.enqueue(op, id))
					this.settle(
						id,
						undefined,
						new AppError("Recording mailbox is busy", 429, "RECORDING_BUSY"),
					);
			} catch (error) {
				if (op.type === "chunk" || op.type === "meta") {
					try {
						this.gap(op);
					} catch {}
				}
				this.settle(id, undefined, error);
			}
		});
	}
	/** Isolated clients may be closed by tests; never used to restart the application. */
	async close(): Promise<void> {
		this.closed = true;
		const worker = this.worker;
		this.fail(new Error("Recording client closed"));
		await worker?.terminate();
	}
}
const clients = hotSafe(
	"request-recording-clients",
	() => new Map<string, RequestRecorderClient>(),
);
function client(): RequestRecorderClient {
	const home = getNarraforkHome();
	let value = clients.get(home);
	if (!value) {
		if (clients.size >= 4) throw new AppError("Too many recorder homes", 503, "RECORDING_BUSY");
		value = new RequestRecorderClient(home);
		clients.set(home, value);
	}
	value.configure(requestRecordingSettings());
	return value;
}
function sendBytes(
	port: RequestRecorderClient,
	requestId: string,
	side: RecordingSide,
	kind: string,
	value: string | Uint8Array,
	packet: number,
): void {
	const total = typeof value === "string" ? Buffer.byteLength(value, "utf8") : value.byteLength;
	const time = new Date().toISOString();
	if (total === 0) {
		port.capture({
			type: "chunk",
			requestId,
			side,
			kind,
			data: new Uint8Array(0),
			packet,
			offset: 0,
			total,
			time,
		});
		return;
	}
	let offset = 0;
	const inputLimit = RECORDING_QUEUE_BYTES - 512 * 1024;
	if (typeof value === "string") {
		let position = 0;
		if (total > inputLimit) {
			position = Math.max(0, value.length - Math.floor((inputLimit * value.length) / total));
			if (Buffer.byteLength(value.slice(position)) > inputLimit)
				position = Math.max(0, value.length - Math.floor(inputLimit / 3));
			if (
				position > 0 &&
				value.charCodeAt(position) >= 0xdc00 &&
				value.charCodeAt(position) <= 0xdfff &&
				value.charCodeAt(position - 1) >= 0xd800 &&
				value.charCodeAt(position - 1) <= 0xdbff
			)
				position++;
			offset = total - Buffer.byteLength(value.slice(position));
			port.capture({
				type: "gap",
				requestId,
				side,
				bytes: offset,
				reason: "single payload safety limit",
			});
		}
		const encoder = new TextEncoder();
		while (position < value.length) {
			const buffer = new Uint8Array(RECORDING_CHUNK_BYTES);
			let end = Math.min(value.length, position + RECORDING_CHUNK_BYTES);
			if (
				end < value.length &&
				value.charCodeAt(end - 1) >= 0xd800 &&
				value.charCodeAt(end - 1) <= 0xdbff &&
				value.charCodeAt(end) >= 0xdc00 &&
				value.charCodeAt(end) <= 0xdfff
			)
				end--;
			const { read = 0, written = 0 } = encoder.encodeInto(value.slice(position, end), buffer);
			if (!read) break;
			port.capture({
				type: "chunk",
				requestId,
				side,
				kind,
				data: buffer.slice(0, written),
				packet,
				offset,
				total,
				time,
			});
			position += read;
			offset += written;
		}
	} else {
		if (total > inputLimit) {
			offset = total - inputLimit;
			port.capture({
				type: "gap",
				requestId,
				side,
				bytes: offset,
				reason: "single payload safety limit",
			});
		}
		while (offset < value.byteLength) {
			const data = Uint8Array.from(value.subarray(offset, offset + RECORDING_CHUNK_BYTES));
			// capture() may synchronously transfer data.buffer to the worker, which detaches it
			// (byteLength becomes 0). Read the length first, or offset never advances and the
			// same chunk is re-posted until the in-flight window fills.
			const length = data.byteLength;
			port.capture({ type: "chunk", requestId, side, kind, data, packet, offset, total, time });
			offset += length;
		}
	}
}
export function createRequestRecording(identity: RecordingIdentity): RequestRecording | undefined {
	const config = requestRecordingSettings();
	if (
		(!config.ringEnabled && config.autoPin === "off") ||
		(config.autoPin === "off" && !config.ringRequestBytes && !config.ringResponseBytes)
	)
		return undefined;
	let port: RequestRecorderClient;
	try {
		port = client();
	} catch (error) {
		logger.warn("Could not start request recording", { error: String(error) });
		return undefined;
	}
	port.capture({
		type: "begin",
		identity,
		continuous: config.autoPin !== "off",
		errorsOnly: config.autoPin === "errors",
	});
	let packet = 0;
	let finished = false;
	const notices = new Set<string>();
	const noteMissing = (side: RecordingSide, reason: string) => {
		const key = `${side}:${reason}`;
		if (notices.has(key)) return;
		notices.add(key);
		port.capture({ type: "gap", requestId: identity.requestId, side, bytes: 0, reason });
	};
	const capture = (side: RecordingSide, kind: string, text?: string | Uint8Array) => {
		if (finished || text == null) return;
		const live = requestRecordingSettings();
		port.configure(live);
		if (!live.ringEnabled && live.autoPin === "off") {
			noteMissing(side, "recording disabled during request");
			return;
		}
		if (
			config.autoPin !== "all" &&
			!(side === "request" ? live.ringRequestBytes : live.ringResponseBytes)
		) {
			noteMissing(side, `${side} body recording disabled`);
			return;
		}
		sendBytes(port, identity.requestId, side, kind, text, ++packet);
	};
	const meta = (side: RecordingSide, value: Record<string, unknown>) => {
		const live = requestRecordingSettings();
		if (!live.ringEnabled && live.autoPin === "off") {
			if (!finished) noteMissing(side, "recording disabled during request");
			return;
		}
		if (!finished)
			port.capture({
				type: "meta",
				requestId: identity.requestId,
				side,
				value: sanitizeRecordingMetadata(value),
			});
	};
	return {
		requestId: identity.requestId,
		request(request: RecordingRequest) {
			const { bodyText, ...information } = request;
			meta("request", {
				...information,
				bodyEnabled: config.autoPin === "all" || requestRecordingSettings().ringRequestBytes > 0,
			});
			capture("request", "body", bodyText);
		},
		responseMeta: (value) => meta("response", value),
		pin(reason) {
			port.capture({ type: "pin", requestId: identity.requestId, reason });
		},
		responseChunk: (value) => capture("response", "body", value),
		frame(frame: RecordingFrame) {
			if (finished) return;
			// Only an explicit source-loss signal affects completeness. Legacy bodyTruncated
			// describes its bounded preview, even when this recorder received every byte.
			if (frame.sourceTruncated)
				noteMissing(frame.direction, `${frame.sourceTruncated} source truncated`);
			if (frame.metadata) meta(frame.direction, { type: frame.type, ...frame.metadata });
			capture(frame.direction, frame.type, frame.text);
		},
		finish(result = {}) {
			if (finished) return;
			finished = true;
			port.capture({
				type: "end",
				requestId: identity.requestId,
				error: result.error?.slice(0, 2048),
				status: result.status,
			});
		},
	};
}
export function bindRecordingRequest(requestId: string, apiRequestId: string): void {
	clients.get(getNarraforkHome())?.capture({ type: "bind", requestId, apiRequestId });
}
/** Pin a (possibly already finished) recording; a no-op when it already left the ring. */
export function pinRecordingRequest(requestId: string, reason: RecordingPinReason): void {
	clients.get(getNarraforkHome())?.capture({ type: "pin", requestId, reason });
}
export async function saveRequestRecording(narratorId: string): Promise<SavedRecording> {
	return client().call({ type: "save", narratorId });
}
export async function getRequestRecordingDetail(
	narratorId: string,
	requestId: string,
	signal?: AbortSignal,
): Promise<RequestRecordingDetail> {
	return client().call({ type: "detail", narratorId, requestId }, signal);
}
export async function saveSpecificRequestRecording(
	narratorId: string,
	requestId: string,
	signal?: AbortSignal,
): Promise<SavedRecording> {
	return client().call({ type: "save-specific", narratorId, requestId }, signal);
}
export async function listSavedRecordings(
	narratorId: string,
	cursor?: string,
	limit?: number,
): Promise<{ items: SavedRecording[]; nextCursor: string | null }> {
	return client().call({ type: "list", narratorId, cursor, limit });
}
export async function deleteSavedRecording(narratorId: string, saveId: string): Promise<void> {
	await client().call({ type: "delete", narratorId, saveId });
}
const activeDownloads = hotSafe("request-recording-download-leases", () => new Set<symbol>());

/** File transfer also has a lease; a client that never reads cannot retain a ZIP forever. */
export async function recordingArchiveResponse(
	artifact: { path: string; bytes: number },
	saveId: string,
	signal?: AbortSignal,
	leaseMs = RECORDING_DOWNLOAD_LEASE_MS,
	onCleanup: () => void = () => {},
): Promise<Response> {
	const source = Bun.file(artifact.path).stream().getReader();
	let cleanupPromise: Promise<void> | undefined;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let controller: ReadableStreamDefaultController<Uint8Array> | undefined;
	let termination: Error | undefined;
	const cleanup = (): Promise<void> => {
		if (!cleanupPromise)
			cleanupPromise = (async () => {
				if (timer) clearTimeout(timer);
				signal?.removeEventListener("abort", abort);
				try {
					await source.cancel().catch(() => {});
					await unlink(artifact.path).catch(() => {});
				} finally {
					onCleanup();
				}
			})();
		return cleanupPromise;
	};
	const stop = (error: Error) => {
		termination = error;
		void cleanup().then(() => controller?.error(error));
	};
	const abort = () =>
		stop(new AppError("Recording download cancelled", 499, "RECORDING_CANCELLED"));
	if (signal?.aborted) {
		await cleanup();
		signal.throwIfAborted();
	}
	const body = new ReadableStream<Uint8Array>(
		{
			start(value) {
				controller = value;
			},
			async pull(value) {
				try {
					const result = await source.read();
					if (termination) throw termination;
					if (result.done) {
						await cleanup();
						value.close();
					} else value.enqueue(result.value);
				} catch (error) {
					await cleanup();
					value.error(error);
				}
			},
			cancel: cleanup,
		},
		{ highWaterMark: 0 },
	);
	signal?.addEventListener("abort", abort, { once: true });
	timer = setTimeout(
		() => stop(new AppError("Recording download lease expired", 504, "RECORDING_TIMEOUT")),
		leaseMs,
	);
	timer.unref();
	if (signal?.aborted) abort();
	return new Response(body, {
		headers: {
			"Content-Type": "application/zip",
			"Content-Length": String(artifact.bytes),
			"Content-Disposition": `attachment; filename="request-recording-${saveId}.zip"`,
			"Cache-Control": "no-store",
			"X-Content-Type-Options": "nosniff",
		},
	});
}
export async function downloadSavedRecording(
	narratorId: string,
	saveId: string,
	signal?: AbortSignal,
): Promise<Response> {
	if (activeDownloads.size >= RECORDING_MAX_DOWNLOADS)
		throw new AppError(
			"Recording downloads are busy; retry after a transfer completes",
			429,
			"RECORDING_BUSY",
		);
	const lease = Symbol("recording-download");
	activeDownloads.add(lease);
	let handedOff = false;
	try {
		const artifact = await client().call<{ path: string; bytes: number }>(
			{ type: "archive", narratorId, saveId },
			signal,
		);
		const response = await recordingArchiveResponse(
			artifact,
			saveId,
			signal,
			RECORDING_DOWNLOAD_LEASE_MS,
			() => activeDownloads.delete(lease),
		);
		handedOff = true;
		return response;
	} finally {
		if (!handedOff) activeDownloads.delete(lease);
	}
}
