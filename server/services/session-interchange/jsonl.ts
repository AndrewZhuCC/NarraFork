/**
 * Session file bytes → JSONL rows, without monopolizing the main thread.
 *
 * A Claude Code transcript reaches tens of megabytes (the largest seen locally is
 * ~20 MB, ~3 000 rows, some over 200 KB each). Decoding and `JSON.parse`-ing that in
 * one synchronous run is a visible stall for every other request, so rows are
 * decoded one at a time with the event loop handed back every few milliseconds.
 * The parsers then receive already-decoded objects (see `InterchangeLine`).
 *
 * Two ceilings make that slicing mean something:
 *  - a **per-row** cap ({@link SESSION_LINE_MAX_BYTES}): the loop only yields
 *    between rows, so one 60 MB row would still be a single synchronous parse.
 *    An over-long row is skipped (and reported) before it is ever decoded.
 *  - a **decoded-bytes budget shared by every file of one import**
 *    ({@link DecodeBudget}): the per-file cap alone let 200 tiny zstd files each
 *    expand to the cap, which is gigabytes from a few kilobytes of upload.
 *
 * Decompression runs on the zlib thread pool with its output capped at whatever
 * the shared budget has left, so a compression bomb fails before it fills the heap.
 */

import { promisify } from "node:util";
import { zstdDecompress } from "node:zlib";
import type { InterchangeLine } from "@shared/session-interchange/model";
import { AppError, ValidationError } from "../../lib/errors";

/** Largest single session file accepted, after decompression. */
export const SESSION_FILE_MAX_BYTES = 64 * 1024 * 1024;

/** Largest session accepted across its main file and every subagent file, decoded. */
export const SESSION_IMPORT_MAX_TOTAL_BYTES = 128 * 1024 * 1024;

/**
 * Longest JSONL row decoded. The largest real row seen is ~200 KB (a big tool
 * output); 8 MiB leaves ample room while keeping one row's parse well under the
 * main-thread budget.
 */
export const SESSION_LINE_MAX_BYTES = 8 * 1024 * 1024;

/** Bytes read from each end of a file to build a scan preview. */
export const SESSION_PREVIEW_BYTES = 64 * 1024;

/** Longest synchronous decode run before the event loop is handed back. */
const SLICE_MS = 8;

const zstdDecompressAsync = promisify(zstdDecompress) as (
	input: Uint8Array,
	options: { maxOutputLength: number },
) => Promise<Buffer>;

const ZSTD_MAGIC = [0x28, 0xb5, 0x2f, 0xfd] as const;
const NEWLINE = 0x0a;

export class SessionTooLargeError extends AppError {
	constructor(label: string, maxBytes: number) {
		super(
			`${label} exceeds the ${Math.round(maxBytes / (1024 * 1024))} MiB session limit`,
			413,
			"SESSION_TOO_LARGE",
		);
	}
}

/**
 * Decoded bytes one import may still produce, shared by all of its files.
 * Create one per import and pass it to every {@link decodeSessionBytes} call.
 */
export class DecodeBudget {
	private remaining: number;

	constructor(readonly limit = SESSION_IMPORT_MAX_TOTAL_BYTES) {
		this.remaining = limit;
	}

	/** What the next file may expand to: the per-file cap or what is left, whichever is smaller. */
	nextFileCap(): number {
		return Math.min(SESSION_FILE_MAX_BYTES, this.remaining);
	}

	charge(bytes: number, label: string): void {
		if (bytes > this.remaining) throw new SessionTooLargeError(label, this.limit);
		this.remaining -= bytes;
	}
}

export function isZstd(bytes: Uint8Array): boolean {
	return ZSTD_MAGIC.every((value, index) => bytes[index] === value);
}

function yieldToLoop(): Promise<void> {
	return new Promise((resolve) => setImmediate(resolve));
}

/**
 * One row: a decoded object when the text is a JSON object, otherwise the raw text
 * so the parser can count it as unparseable. Blank rows are dropped by the caller.
 */
function toLine(text: string): InterchangeLine {
	const trimmed = text.trim();
	try {
		const value = JSON.parse(trimmed);
		return value && typeof value === "object" && !Array.isArray(value) ? value : trimmed;
	} catch {
		return trimmed;
	}
}

/** Blank-aware line boundaries; `\r\n` is handled by `trim()` in `toLine`. */
function* lineRanges(bytes: Uint8Array): Generator<[number, number]> {
	let start = 0;
	// A UTF-8 BOM would otherwise make the first row unparseable.
	if (bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) start = 3;
	while (start < bytes.length) {
		let end = bytes.indexOf(NEWLINE, start);
		if (end < 0) end = bytes.length;
		if (end > start) yield [start, end];
		start = end + 1;
	}
}

export async function decompressIfNeeded(
	bytes: Uint8Array,
	label: string,
	maxBytes = SESSION_FILE_MAX_BYTES,
): Promise<Uint8Array> {
	if (!isZstd(bytes)) {
		if (bytes.byteLength > maxBytes) throw new SessionTooLargeError(label, maxBytes);
		return bytes;
	}
	if (maxBytes <= 0) throw new SessionTooLargeError(label, maxBytes);
	try {
		return new Uint8Array(await zstdDecompressAsync(bytes, { maxOutputLength: maxBytes }));
	} catch (error) {
		if ((error as { code?: string }).code === "ERR_BUFFER_TOO_LARGE") {
			throw new SessionTooLargeError(label, maxBytes);
		}
		throw new ValidationError(`${label} is not a valid zstd file`);
	}
}

export interface DecodedSession {
	rows: InterchangeLine[];
	/** Rows over {@link SESSION_LINE_MAX_BYTES}, skipped without being decoded. */
	oversizedLines: number;
}

/**
 * Decode a whole session file into rows, in time slices.
 *
 * Invalid UTF-8 is replaced rather than rejected: one corrupt byte in a tool
 * output should cost that row's fidelity, not the whole import.
 */
export async function decodeSessionBytes(
	input: Uint8Array,
	options: { label: string; budget: DecodeBudget; signal?: AbortSignal },
): Promise<DecodedSession> {
	const bytes = await decompressIfNeeded(input, options.label, options.budget.nextFileCap());
	options.budget.charge(bytes.byteLength, options.label);
	const decoder = new TextDecoder("utf-8");
	const rows: InterchangeLine[] = [];
	let oversizedLines = 0;
	let sliceStart = performance.now();
	for (const [start, end] of lineRanges(bytes)) {
		if (end - start > SESSION_LINE_MAX_BYTES) {
			oversizedLines += 1;
			continue;
		}
		const text = decoder.decode(bytes.subarray(start, end));
		if (text.trim()) rows.push(toLine(text));
		if (performance.now() - sliceStart > SLICE_MS) {
			await yieldToLoop();
			options.signal?.throwIfAborted();
			sliceStart = performance.now();
		}
	}
	return { rows, oversizedLines };
}

/**
 * Rows of a file excerpt, for previews. The excerpt is small, so this is
 * synchronous. A cut line at either edge is dropped rather than reported as
 * corrupt: it is only cut because the excerpt ended there.
 */
export function decodeExcerpt(
	bytes: Uint8Array,
	cut: { atStart: boolean; atEnd: boolean },
): InterchangeLine[] {
	const ranges = [...lineRanges(bytes)];
	if (cut.atStart) ranges.shift();
	if (cut.atEnd && bytes[bytes.length - 1] !== NEWLINE) ranges.pop();
	const decoder = new TextDecoder("utf-8");
	return ranges
		.map(([start, end]) => decoder.decode(bytes.subarray(start, end)))
		.filter((text) => text.trim())
		.map(toLine);
}
