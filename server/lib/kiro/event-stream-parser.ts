// AWS Event Stream binary parser — state machine decoder
// Reverse-engineered from kiro CLI chat binary protocol
// Uses Bun's built-in CRC32 (ISO-HDLC)

import { readWithTimeout } from "../stream-timeout";
import { KiroApiError, KiroParseError, type ParseErrorKind } from "./types";

const PRELUDE_SIZE = 12;
const MIN_MESSAGE_SIZE = PRELUDE_SIZE + 4; // prelude + msg CRC
const MAX_MESSAGE_SIZE = 16 * 1024 * 1024;
const DEFAULT_MAX_ERRORS = 5;
const DEFAULT_MAX_BUFFER_SIZE = MAX_MESSAGE_SIZE;

function crc32(data: Uint8Array): number {
	return Bun.hash.crc32(data);
}

function readU32BE(buf: Uint8Array, offset: number): number {
	return (
		((buf[offset] << 24) | (buf[offset + 1] << 16) | (buf[offset + 2] << 8) | buf[offset + 3]) >>> 0
	);
}

function readU16BE(buf: Uint8Array, offset: number): number {
	return (buf[offset] << 8) | buf[offset + 1];
}

export interface FrameHeaders {
	messageType?: string;
	eventType?: string;
	exceptionType?: string;
	errorCode?: string;
}

function parseHeaders(data: Uint8Array, headerLength: number): FrameHeaders {
	const result: FrameHeaders = {};
	let offset = 0;

	while (offset < headerLength) {
		if (offset >= data.length) break;
		const nameLen = data[offset];
		offset += 1;
		if (nameLen === 0 || offset + nameLen > data.length) break;

		const name = new TextDecoder().decode(data.subarray(offset, offset + nameLen));
		offset += nameLen;

		if (offset >= data.length) break;
		const valueType = data[offset];
		offset += 1;

		switch (valueType) {
			case 0:
			case 1:
				break;
			case 2:
				offset += 1;
				break;
			case 3:
				offset += 2;
				break;
			case 4:
				offset += 4;
				break;
			case 5:
			case 8:
				offset += 8;
				break;
			case 6: {
				if (offset + 2 > data.length) return result;
				const len = readU16BE(data, offset);
				offset += 2 + len;
				break;
			}
			case 7: {
				if (offset + 2 > data.length) return result;
				const len = readU16BE(data, offset);
				offset += 2;
				if (offset + len > data.length) return result;
				const val = new TextDecoder().decode(data.subarray(offset, offset + len));
				offset += len;
				if (name === ":message-type") result.messageType = val;
				else if (name === ":event-type") result.eventType = val;
				else if (name === ":exception-type") result.exceptionType = val;
				else if (name === ":error-code") result.errorCode = val;
				break;
			}
			case 9:
				offset += 16;
				break;
			default:
				return result;
		}
	}

	return result;
}

// --- Typed frame parser ---

interface ParsedFrame {
	headers: FrameHeaders;
	payload: Uint8Array;
	consumed: number;
}

type FrameResult = { ok: true; frame: ParsedFrame } | { ok: false; error: KiroParseError } | null; // null = need more data

function parseFrameTyped(buffer: Uint8Array): FrameResult {
	if (buffer.length < PRELUDE_SIZE) return null;

	const totalLength = readU32BE(buffer, 0);
	const headerLength = readU32BE(buffer, 4);
	const preludeCrc = readU32BE(buffer, 8);

	if (totalLength < MIN_MESSAGE_SIZE) {
		return {
			ok: false,
			error: new KiroParseError("message_too_small", `Message too small: ${totalLength}`),
		};
	}
	if (totalLength > MAX_MESSAGE_SIZE) {
		return {
			ok: false,
			error: new KiroParseError("message_too_large", `Message too large: ${totalLength}`),
		};
	}
	if (headerLength > totalLength - MIN_MESSAGE_SIZE) {
		return {
			ok: false,
			error: new KiroParseError("header_parse_failed", "Header length exceeds frame"),
		};
	}

	if (buffer.length < totalLength) return null; // Need more data

	const actualPreludeCrc = crc32(buffer.subarray(0, 8));
	if (actualPreludeCrc !== preludeCrc) {
		return { ok: false, error: new KiroParseError("prelude_crc_mismatch", "Prelude CRC mismatch") };
	}

	const messageCrc = readU32BE(buffer, totalLength - 4);
	const actualMessageCrc = crc32(buffer.subarray(0, totalLength - 4));
	if (actualMessageCrc !== messageCrc) {
		return { ok: false, error: new KiroParseError("message_crc_mismatch", "Message CRC mismatch") };
	}

	const headersStart = PRELUDE_SIZE;
	const headersEnd = headersStart + headerLength;
	const headers = parseHeaders(buffer.subarray(headersStart, headersEnd), headerLength);
	const payload = buffer.slice(headersEnd, totalLength - 4);

	return { ok: true, frame: { headers, payload, consumed: totalLength } };
}

// --- State machine decoder ---

type DecoderState = "ready" | "parsing" | "recovering" | "stopped";

export class EventStreamDecoder {
	private buffer = new Uint8Array(0);
	private state: DecoderState = "ready";
	private framesDecoded = 0;
	private errorCount = 0;
	private maxErrors: number;
	private maxBufferSize: number;
	private bytesSkipped = 0;

	constructor(maxErrors = DEFAULT_MAX_ERRORS, maxBufferSize = DEFAULT_MAX_BUFFER_SIZE) {
		this.maxErrors = maxErrors;
		this.maxBufferSize = maxBufferSize;
	}

	/** Append data to internal buffer. */
	feed(data: Uint8Array): void {
		const newSize = this.buffer.length + data.length;
		if (newSize > this.maxBufferSize) {
			throw new KiroParseError(
				"buffer_overflow",
				`Buffer overflow: ${newSize} > ${this.maxBufferSize}`,
			);
		}
		const newBuf = new Uint8Array(newSize);
		newBuf.set(this.buffer);
		newBuf.set(data, this.buffer.length);
		this.buffer = newBuf;

		if (this.state === "recovering") this.state = "ready";
	}

	/** Try to decode the next frame. Returns frame or null. */
	decode(): ParsedFrame | null {
		if (this.state === "stopped") {
			throw new KiroParseError(
				"too_many_errors",
				`Decoder stopped after ${this.errorCount} errors`,
			);
		}

		if (this.buffer.length === 0) {
			this.state = "ready";
			return null;
		}

		this.state = "parsing";
		const result = parseFrameTyped(this.buffer);

		if (result === null) {
			this.state = "ready";
			return null;
		}

		if (result.ok) {
			this.buffer = this.buffer.subarray(result.frame.consumed);
			this.state = "ready";
			this.framesDecoded++;
			this.errorCount = 0;
			return result.frame;
		}

		// Error path
		this.errorCount++;
		if (this.errorCount >= this.maxErrors) {
			this.state = "stopped";
			throw new KiroParseError(
				"too_many_errors",
				`Too many errors (${this.errorCount}): ${result.error.message}`,
			);
		}

		this.tryRecover(result.error.kind);
		this.state = "recovering";
		return null;
	}

	/** Generator that drains all available frames. */
	*decodeAll(): Generator<ParsedFrame> {
		while (true) {
			if (this.state === "stopped" || this.state === "recovering") return;
			const frame = this.decode();
			if (!frame) return;
			yield frame;
		}
	}

	/** Attempt to resume from Stopped state. */
	tryResume(): void {
		if (this.state === "stopped") {
			this.errorCount = 0;
			this.state = "ready";
		}
	}

	/** Reset all state. */
	reset(): void {
		this.buffer = new Uint8Array(0);
		this.state = "ready";
		this.framesDecoded = 0;
		this.errorCount = 0;
		this.bytesSkipped = 0;
	}

	get currentState(): DecoderState {
		return this.state;
	}
	get decodedCount(): number {
		return this.framesDecoded;
	}
	get errors(): number {
		return this.errorCount;
	}
	get skipped(): number {
		return this.bytesSkipped;
	}
	get bufferLength(): number {
		return this.buffer.length;
	}

	private tryRecover(kind: ParseErrorKind): void {
		if (this.buffer.length === 0) return;

		switch (kind) {
			case "prelude_crc_mismatch":
			case "message_too_small":
			case "message_too_large":
				// Skip 1 byte, scan for next frame boundary
				this.buffer = this.buffer.subarray(1);
				this.bytesSkipped++;
				break;

			case "message_crc_mismatch":
			case "header_parse_failed": {
				// Try to skip entire frame using totalLength
				if (this.buffer.length >= PRELUDE_SIZE) {
					const totalLength = readU32BE(this.buffer, 0);
					if (totalLength >= MIN_MESSAGE_SIZE && totalLength <= this.buffer.length) {
						this.buffer = this.buffer.subarray(totalLength);
						this.bytesSkipped += totalLength;
						break;
					}
				}
				// Fallback: skip 1 byte
				this.buffer = this.buffer.subarray(1);
				this.bytesSkipped++;
				break;
			}

			default:
				this.buffer = this.buffer.subarray(1);
				this.bytesSkipped++;
				break;
		}
	}
}

// --- Public API ---

export interface StreamEvent {
	eventType: string;
	data: Record<string, unknown>;
}

/**
 * Parse an AWS Event Stream response body into structured events.
 * Yields all event types with full payload for debugging.
 */
export async function* parseEventStream(
	body: ReadableStream<Uint8Array>,
): AsyncGenerator<StreamEvent> {
	const reader = body.getReader();
	const decoder = new EventStreamDecoder();
	const td = new TextDecoder();

	try {
		while (true) {
			const { done, value } = await readWithTimeout(reader);
			if (done) break;

			decoder.feed(value);

			for (const frame of decoder.decodeAll()) {
				const { headers, payload } = frame;

				if (headers.messageType === "exception") {
					const msg = td.decode(payload);
					throw new KiroApiError(headers.exceptionType ?? "UnknownException", msg);
				}

				if (headers.messageType === "error") {
					const msg = td.decode(payload);
					throw new KiroApiError(headers.errorCode ?? "UnknownError", msg);
				}

				if (headers.messageType === "event" && headers.eventType) {
					try {
						const data = JSON.parse(td.decode(payload));
						yield { eventType: headers.eventType, data };
					} catch {
						// Skip malformed JSON payloads
					}
				}
			}
		}
	} finally {
		reader.releaseLock();
	}
}
