/**
 * Settings-independent charset primitives.
 *
 * Split out of `encoding.ts` so code that never consults `agent.legacyEncoding` — notably the
 * editor document worker, which is a fresh worker thread per job — does not load the settings
 * module (and with it the model catalog) just to encode or decode bytes. That import alone cost
 * ~170 ms per editor job. `encoding.ts` re-exports everything here, so existing importers are
 * unaffected.
 */
import chardet from "chardet";
import iconv from "iconv-lite";

/** Minimum confidence from chardet to trust the detected encoding (0–100). */
export const CONFIDENCE_THRESHOLD = 70;

/**
 * Decode bytes using an encoding the caller already knows, without sniffing.
 *
 * The counterpart to `decodeFileBytes` for a round trip that spans two
 * requests: the human file editor is told an encoding when it opens a file and
 * echoes it back on save. Re-detecting at that point would be wrong rather than
 * merely redundant — detection runs on the bytes, and on save the interesting bytes
 * are the ones about to be replaced, so a file's encoding could silently change
 * because the new text sniffed differently.
 *
 * Unknown or unsupported names fall back to UTF-8 instead of throwing: the name
 * arrives from a client, and a bad one must not turn a save into a 500.
 */
export function decodeFileBytesAs(bytes: Uint8Array, encoding: string): string {
	if (isUtf8(encoding)) return new TextDecoder().decode(bytes);
	const buffer = Buffer.isBuffer(bytes) ? bytes : Buffer.from(bytes);
	if (!iconv.encodingExists(encoding)) return new TextDecoder().decode(buffer);
	return iconv.decode(buffer, encoding);
}
/**
 * Encode text to bytes in a named encoding, regardless of `agent.legacyEncoding`.
 *
 * `encodeFileBytes` (in encoding.ts) deliberately ignores a non-UTF-8 encoding when that setting
 * is off, because for the AGENT tools the setting is the switch that decides whether
 * legacy charsets are handled at all. That trade does not transfer to a person saving
 * a file they opened in the browser: the file's encoding is a property of the file,
 * not of a preference, and writing UTF-8 over a GBK file destroys it whichever way
 * the setting happens to be set.
 *
 * Falls back to UTF-8 for a name iconv does not know, matching
 * {@link decodeFileBytesAs} so the pair cannot disagree about what a bad name means.
 */
export function encodeFileBytesAs(content: string, encoding: string): Uint8Array {
	if (isUtf8(encoding)) return new TextEncoder().encode(content);
	if (!iconv.encodingExists(encoding)) return new TextEncoder().encode(content);
	return iconv.encode(content, encoding);
}

/**
 * Sniff the encoding of already-read bytes, ignoring `agent.legacyEncoding`.
 *
 * Same reasoning as {@link encodeFileBytesAs}: a human editor must know the real
 * encoding of the file it is about to overwrite even when the agent-facing legacy
 * switch is off, because the alternative is a silent destructive save.
 *
 * Returns `"utf-8"` whenever detection is not confident, so an ambiguous file is
 * treated as the modern default rather than guessed into a legacy charset.
 */
export function detectFileEncoding(bytes: Uint8Array): string {
	const buffer = Buffer.isBuffer(bytes) ? bytes : Buffer.from(bytes);
	const best = chardet.analyse(buffer)[0];
	if (!best || best.confidence < CONFIDENCE_THRESHOLD) return "utf-8";
	const name = normalizeEncoding(best.name);
	return iconv.encodingExists(name) ? name : "utf-8";
}
/** Normalize encoding names from chardet to iconv-lite compatible names */
export function normalizeEncoding(enc: string | null): string {
	if (!enc) return "utf-8";
	const lower = enc.toLowerCase();
	// chardet may return these aliases
	const map: Record<string, string> = {
		"utf-8": "utf-8",
		ascii: "utf-8",
		"windows-1252": "windows-1252",
		"iso-8859-1": "iso-8859-1",
		gb2312: "gbk",
		gb18030: "gb18030",
		big5: "big5",
		"euc-jp": "euc-jp",
		shift_jis: "shift_jis",
		"euc-kr": "euc-kr",
		"iso-2022-jp": "iso-2022-jp",
	};
	return map[lower] ?? lower;
}

export function isUtf8(encoding: string): boolean {
	const lower = encoding.toLowerCase();
	return lower === "utf-8" || lower === "utf8" || lower === "ascii";
}
