import { tokenTenthsForCodeUnit } from "./estimate-tokens";

export interface JsonTokenEstimateOptions {
	signal?: AbortSignal;
	deadline?: number;
	maxBytes?: number;
}

/**
 * Estimate the exact compact JSON representation without ever materializing that whole string.
 * JSON escapes and UTF-16 CJK weights match estimateTokens(JSON.stringify(value)); ceil ONCE.
 * Display-only: budget exhaustion/unsupported JSON returns unknown, never a partial estimate.
 */
export async function estimateJsonTokens(
	value: unknown,
	options: JsonTokenEstimateOptions = {},
): Promise<number | undefined> {
	const deadline = Math.min(options.deadline ?? Number.POSITIVE_INFINITY, Date.now() + 10_000);
	const maxBytes = Math.min(options.maxBytes ?? 128 * 1024 * 1024, 128 * 1024 * 1024);
	let tenths = 0;
	let bytes = 0;
	let work = 0;
	const seen = new Set<object>();
	const check = async () => {
		if (options.signal?.aborted || Date.now() >= deadline || bytes > maxBytes)
			throw new Error("unknown");
		if (work >= 16_384) {
			work = 0;
			await Bun.sleep(0);
		}
	};
	const ascii = (length: number) => {
		tenths += length * 3;
		bytes += length;
		work += length;
	};
	const string = async (text: string) => {
		ascii(2);
		for (let index = 0; index < text.length; index++) {
			const code = text.charCodeAt(index);
			if (
				code === 34 ||
				code === 92 ||
				code === 8 ||
				code === 9 ||
				code === 10 ||
				code === 12 ||
				code === 13
			)
				ascii(2);
			else if (code < 32) ascii(6);
			else if (
				code >= 0xd800 &&
				code <= 0xdbff &&
				index + 1 < text.length &&
				text.charCodeAt(index + 1) >= 0xdc00 &&
				text.charCodeAt(index + 1) <= 0xdfff
			) {
				tenths += 6;
				bytes += 4;
				work += 2;
				index++;
			} else if (code >= 0xd800 && code <= 0xdfff) ascii(6);
			else {
				tenths += tokenTenthsForCodeUnit(code);
				bytes += code < 128 ? 1 : code < 2048 ? 2 : 3;
				work++;
			}
			if (work >= 16_384) await check();
		}
	};
	const normalize = (entry: unknown, key: string): unknown => {
		if (
			entry &&
			typeof entry === "object" &&
			typeof (entry as { toJSON?: unknown }).toJSON === "function"
		)
			entry = (entry as { toJSON(key: string): unknown }).toJSON(key);
		if (entry instanceof Number || entry instanceof String || entry instanceof Boolean)
			return entry.valueOf();
		return entry;
	};
	const omitted = (entry: unknown) =>
		entry === undefined || typeof entry === "function" || typeof entry === "symbol";
	const visit = async (entry: unknown, depth: number): Promise<void> => {
		if (depth > 512) throw new Error("unknown");
		if (entry == null) {
			ascii(4);
			return;
		}
		if (typeof entry === "string") return string(entry);
		if (typeof entry === "number") {
			ascii(Number.isFinite(entry) ? String(entry).length : 4);
			return;
		}
		if (typeof entry === "boolean") {
			ascii(entry ? 4 : 5);
			return;
		}
		if (typeof entry !== "object") throw new Error("unknown");
		if (seen.has(entry)) throw new Error("unknown");
		seen.add(entry);
		ascii(1);
		if (Array.isArray(entry)) {
			for (let index = 0; index < entry.length; index++) {
				if (index) ascii(1);
				const child = normalize(entry[index], String(index));
				if (omitted(child)) ascii(4);
				else await visit(child, depth + 1);
				if (work >= 16_384) await check();
			}
		} else {
			let count = 0;
			for (const key of Object.keys(entry)) {
				const child = normalize((entry as Record<string, unknown>)[key], key);
				if (omitted(child)) continue;
				if (count++) ascii(1);
				await string(key);
				ascii(1);
				await visit(child, depth + 1);
				if (work >= 16_384) await check();
			}
		}
		ascii(1);
		seen.delete(entry);
	};
	try {
		await check();
		const root = normalize(value, "");
		if (omitted(root)) return undefined;
		await visit(root, 0);
		await check();
		return Math.ceil(tenths / 10);
	} catch {
		return undefined;
	}
}
