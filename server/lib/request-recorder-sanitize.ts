/** Metadata/error display only; request/response bodies intentionally preserve evidence. */
export function sanitizeRecordingDisplayText(value: string, maximum = 2048): string {
	const safe = value
		.slice(0, maximum * 2)
		.replace(/(^|[\s"'(])(?:\/[\w.-]+(?:\/[^\s"')]+)*|[A-Za-z]:\\[^\s"')]+)/g, "$1[path]");
	let text = Buffer.from(safe).subarray(0, maximum).toString("utf8");
	while (Buffer.byteLength(text) > maximum) text = text.slice(0, -1);
	return text;
}

const SECRET_HEADER =
	/^(authorization|proxy-authorization|cookie|set-cookie|x-api-key|api-key|x-goog-api-key)$/i;
const SECRET_QUERY = /^(key|api[-_]?key|token|access[-_]?token|auth|signature|secret)$/i;
export function sanitizeRecordingUrl(value: string): string {
	try {
		const url = new URL(value);
		url.username = "";
		url.password = "";
		for (const key of [...url.searchParams.keys()])
			if (SECRET_QUERY.test(key)) url.searchParams.set(key, "[REDACTED]");
		return url.href.slice(0, 4096);
	} catch {
		return value.slice(0, 4096);
	}
}
export const RECORDING_HEADER_MAX_COUNT = 256;
export const RECORDING_HEADER_MAX_VALUE_BYTES = 8 * 1024;
export const RECORDING_HEADERS_MAX_BYTES = 64 * 1024;

function utf8Prefix(value: string, maximum: number): string {
	if (Buffer.byteLength(value) <= maximum) return value;
	let text = Buffer.from(value).subarray(0, maximum).toString("utf8");
	while (Buffer.byteLength(text) > maximum) text = text.slice(0, -1);
	return text;
}

/**
 * Headers get their own budget instead of sharing the generic 8 KiB metadata budget:
 * gateways that send many rate-limit/trace headers must not lose the tail silently.
 * Order is preserved; whatever exceeds the budget is dropped and flagged.
 */
function sanitizeRecordingHeaders(value: unknown): {
	headers?: Record<string, string>;
	truncated: boolean;
} {
	if (value == null || typeof value !== "object" || Array.isArray(value))
		return { truncated: value != null };
	const headers: Record<string, string> = {};
	let truncated = false;
	let bytes = 2;
	let count = 0;
	for (const [rawName, rawValue] of Object.entries(value)) {
		if (count >= RECORDING_HEADER_MAX_COUNT) {
			truncated = true;
			break;
		}
		const name = rawName.slice(0, 256);
		const text = SECRET_HEADER.test(name)
			? "[REDACTED]"
			: utf8Prefix(String(rawValue), RECORDING_HEADER_MAX_VALUE_BYTES);
		if (name !== rawName || (!SECRET_HEADER.test(name) && text !== String(rawValue)))
			truncated = true;
		const entryBytes =
			Buffer.byteLength(JSON.stringify(name)) + Buffer.byteLength(JSON.stringify(text)) + 2;
		if (bytes + entryBytes > RECORDING_HEADERS_MAX_BYTES) {
			truncated = true;
			break;
		}
		headers[name] = text;
		bytes += entryBytes;
		count++;
	}
	return { headers, truncated };
}

/** Bound metadata BEFORE serializing; body data has its own byte queue. */
export function sanitizeRecordingMetadata(value: Record<string, unknown>): Record<string, unknown> {
	const { headers: rawHeaders, ...rest } = value;
	const safe = sanitizeGenericMetadata(rest);
	if (rawHeaders === undefined) return safe;
	const { headers, truncated } = sanitizeRecordingHeaders(rawHeaders);
	if (headers) safe.headers = headers;
	if (truncated) safe.headersTruncated = true;
	return safe;
}

function sanitizeGenericMetadata(value: Record<string, unknown>): Record<string, unknown> {
	let truncated = false;
	const visit = (item: unknown, key: string, depth: number): unknown => {
		if (SECRET_HEADER.test(key)) return "[REDACTED]";
		if (["filePath", "spill", "body", "bodyText", "events"].includes(key)) return undefined;
		if (typeof item === "string") {
			const limit = /url$/i.test(key) ? 4096 : 1024;
			if (item.length > limit) truncated = true;
			return /url$/i.test(key) ? sanitizeRecordingUrl(item) : item.slice(0, limit);
		}
		if (item == null || typeof item === "number" || typeof item === "boolean") return item;
		if (depth >= 4) {
			truncated = true;
			return "[metadata depth limited]";
		}
		if (Array.isArray(item)) {
			if (item.length > 16) truncated = true;
			return item.slice(0, 16).map((child) => visit(child, "", depth + 1));
		}
		if (typeof item === "object") {
			const entries = Object.entries(item);
			if (entries.length > 32) truncated = true;
			return Object.fromEntries(
				entries.slice(0, 32).map(([name, child]) => {
					if (name.length > 128) truncated = true;
					return [name.slice(0, 128), visit(child, name, depth + 1)];
				}),
			);
		}
		return undefined;
	};
	const safe = visit(value, "", 0) as Record<string, unknown>;
	if (truncated) safe.metadataTruncated = true;
	while (Buffer.byteLength(JSON.stringify(safe)) > 8192) {
		const removable = Object.keys(safe).filter((key) => key !== "metadataTruncated");
		if (!removable.length) break;
		delete safe[removable[removable.length - 1]];
		safe.metadataTruncated = true;
	}
	return safe;
}
