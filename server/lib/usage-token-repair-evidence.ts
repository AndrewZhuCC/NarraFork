import { constants } from "node:fs";
import { lstat, open, realpath } from "node:fs/promises";
import { dirname, isAbsolute, resolve } from "node:path";
import {
	TOKEN_REPAIR_MAX_DUMP_BYTES,
	type TokenRepairCounters,
	type TokenRepairEvidence,
	type TokenRepairProtocol,
} from "@shared/usage-token-repair";
import { getNarraforkPath } from "./narrafork-home";

type RecordValue = Record<string, unknown>;
type Row = TokenRepairCounters & { id: string; provider: string | null; model: string | null };
const KEYS = [
	"inputTokens",
	"cachedInputTokens",
	"cacheCreationInputTokens",
	"cacheCreation5mTokens",
	"cacheCreation1hTokens",
] as const;
const record = (value: unknown): RecordValue =>
	value !== null && typeof value === "object" && !Array.isArray(value)
		? (value as RecordValue)
		: {};
const count = (value: unknown): number => {
	if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0)
		throw new Error("invalid_token_counter");
	return value;
};
const optionalCount = (value: unknown): number => (value == null ? 0 : count(value));
const equal = (a: TokenRepairCounters, b: TokenRepairCounters): boolean =>
	KEYS.every((key) => a[key] === b[key]);
const counters = (input = 0, read = 0, write = 0, five = 0, hour = 0): TokenRepairCounters => ({
	inputTokens: input,
	cachedInputTokens: read,
	cacheCreationInputTokens: write,
	cacheCreation5mTokens: five,
	cacheCreation1hTokens: hour,
});
function checkAbort(signal?: AbortSignal): void {
	if (signal?.aborted) throw new Error("analysis_cancelled");
}
function parseJson(text: string): unknown {
	if (Buffer.byteLength(text) > TOKEN_REPAIR_MAX_DUMP_BYTES) throw new Error("dump_over_budget");
	return JSON.parse(text);
}

/** The directory is a worker/server option, never a value accepted from dump JSON. */
async function loadSpill(
	pointer: RecordValue,
	id: string,
	directory: string,
	signal?: AbortSignal,
): Promise<RecordValue> {
	if (
		pointer.schema !== "narrafork.api-request-dump-spill.v1" ||
		pointer.truncated ||
		pointer.inlineTruncated !== true ||
		pointer.writtenForRequestId !== id ||
		(pointer.reusedFromRequestId != null && pointer.reusedFromRequestId !== id) ||
		typeof pointer.filePath !== "string" ||
		!isAbsolute(pointer.filePath) ||
		pointer.filePath.split(/[\\/]/).includes("..")
	)
		throw new Error("spill_unverified");
	const bytes = count(pointer.bytes);
	if (bytes > TOKEN_REPAIR_MAX_DUMP_BYTES) throw new Error("dump_over_budget");
	const root = resolve(directory);
	const path = resolve(pointer.filePath);
	if (dirname(path) !== root) throw new Error("spill_unverified");
	checkAbort(signal);
	const directoryInfo = await lstat(root);
	if (
		!directoryInfo.isDirectory() ||
		directoryInfo.isSymbolicLink() ||
		(await realpath(root)) !== root
	)
		throw new Error("spill_unverified");
	const dirHandle = await open(root, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
	try {
		const dirStat = await dirHandle.stat();
		if (dirStat.ino !== directoryInfo.ino || dirStat.dev !== directoryInfo.dev)
			throw new Error("spill_unverified");
		const info = await lstat(path);
		if (!info.isFile() || info.isSymbolicLink() || (await realpath(path)) !== path)
			throw new Error("spill_unverified");
		const file = await open(
			path,
			constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0) | (constants.O_NONBLOCK ?? 0),
		);
		try {
			const stat = await file.stat();
			if (!stat.isFile() || stat.ino !== info.ino || stat.dev !== info.dev || stat.size !== bytes)
				throw new Error("spill_unverified");
			if (stat.size > TOKEN_REPAIR_MAX_DUMP_BYTES) throw new Error("dump_over_budget");
			const buffer = Buffer.alloc(stat.size + 1);
			let offset = 0;
			while (offset < buffer.length) {
				checkAbort(signal);
				const { bytesRead } = await file.read(
					buffer,
					offset,
					Math.min(65536, buffer.length - offset),
					offset,
				);
				if (!bytesRead) break;
				offset += bytesRead;
			}
			const end = await file.stat();
			const currentRoot = await lstat(root);
			const currentFile = await lstat(path);
			if (
				offset !== bytes ||
				end.size !== bytes ||
				end.mtimeMs !== stat.mtimeMs ||
				currentRoot.ino !== dirStat.ino ||
				currentRoot.dev !== dirStat.dev ||
				currentRoot.isSymbolicLink() ||
				currentFile.isSymbolicLink() ||
				currentFile.ino !== stat.ino ||
				currentFile.dev !== stat.dev ||
				(await realpath(root)) !== root ||
				(await realpath(path)) !== path
			)
				throw new Error("spill_unverified");
			checkAbort(signal);
			const envelope = record(parseJson(buffer.subarray(0, offset).toString("utf8")));
			if (
				envelope.schema !== "narrafork.api-request-dump.v1" ||
				envelope.requestId !== id ||
				envelope.truncated
			)
				throw new Error("spill_unverified");
			return record(envelope.dump);
		} finally {
			await file.close();
		}
	} finally {
		await dirHandle.close();
	}
}

interface ParsedResponse {
	items: RecordValue[];
	stream: boolean;
	done: boolean;
}
function responseItems(response: RecordValue): ParsedResponse {
	if (
		Array.isArray(response.events) &&
		response.events.some(
			(event) => record(event)._truncated != null || record(event).truncated === true,
		)
	)
		throw new Error("response_incomplete");
	if (typeof response.bodyText === "string" && response.bodyText.trim()) {
		const text = response.bodyText.trim();
		try {
			const json = parseJson(text);
			return {
				items: (Array.isArray(json) ? json : [json]).map(record),
				stream: Array.isArray(json),
				done: true,
			};
		} catch {
			// Only a fully parseable SSE stream is evidence; never salvage a broken tail.
			if (!/^(?:data:|event:|:)/m.test(text)) throw new Error("response_unparseable");
			const items: RecordValue[] = [];
			let done = false;
			for (const frame of text.split(/\r?\n\r?\n/)) {
				const data = frame
					.split(/\r?\n/)
					.filter((line) => line.startsWith("data:"))
					.map((line) => line.slice(5).trimStart())
					.join("\n");
				if (!data) continue;
				if (data.trim() === "[DONE]") {
					done = true;
					continue;
				}
				const parsed = record(parseJson(data));
				const namedEvent = frame
					.split(/\r?\n/)
					.find((line) => line.startsWith("event:"))
					?.slice(6)
					.trim();
				items.push(
					namedEvent && !parsed.type && !parsed.event_type
						? { ...parsed, type: namedEvent }
						: parsed,
				);
			}
			return { items, stream: true, done };
		}
	}
	if (Array.isArray(response.events)) {
		let done = false;
		const items: RecordValue[] = [];
		for (const event of response.events) {
			const item = record(event);
			if (event === "[DONE]" || item.data === "[DONE]") {
				done = true;
				continue;
			}
			const parsed =
				typeof event === "string"
					? record(parseJson(event))
					: typeof item.data === "string"
						? record(parseJson(item.data))
						: Object.keys(record(item.data)).length
							? record(item.data)
							: item;
			const namedEvent = item.event ?? item.type;
			items.push(
				namedEvent && !parsed.type && !parsed.event_type ? { ...parsed, type: namedEvent } : parsed,
			);
		}
		return { items, stream: true, done };
	}
	throw new Error("response_missing");
}

interface UsageEvidence {
	protocol: TokenRepairProtocol;
	after: TokenRepairCounters;
	legacy: TokenRepairCounters[];
	raw: number;
}
function validateUsage(usage: RecordValue, depth = 0): void {
	if (depth > 8) throw new Error("invalid_token_counter");
	for (const [key, value] of Object.entries(usage)) {
		if (typeof value === "number") count(value);
		else if (value != null && typeof value === "object") validateUsage(record(value), depth + 1);
		else if (value != null && /token/i.test(key)) throw new Error("invalid_token_counter");
	}
}
function firstPositive(...values: unknown[]): number {
	const numbers = values.map(optionalCount);
	return numbers.find((value) => value > 0) ?? 0;
}
function openai(usage: RecordValue): UsageEvidence {
	validateUsage(usage);
	const input = record(usage.input_tokens_details);
	const prompt = record(usage.prompt_tokens_details);
	const raw = count(usage.input_tokens ?? usage.prompt_tokens);
	const read = optionalCount(
		input.cached_tokens ??
			prompt.cached_tokens ??
			firstPositive(
				usage.cache_read_input_tokens,
				usage.cache_read_tokens,
				usage.cached_input_tokens,
				usage.cached_tokens,
			),
	);
	const write = optionalCount(
		input.cache_write_tokens ??
			prompt.cache_write_tokens ??
			input.cache_creation_tokens ??
			prompt.cache_creation_tokens ??
			firstPositive(
				usage.cache_write_tokens,
				usage.cache_creation_input_tokens,
				usage.cache_write_input_tokens,
				usage.cache_creation_tokens,
			),
	);
	count(read + write);
	const oldRead = optionalCount(input.cached_tokens ?? prompt.cached_tokens);
	return {
		protocol: "openai",
		raw,
		after: counters(Math.max(raw - read - write, 0), read, write),
		legacy: [
			counters(raw, read, write),
			counters(raw > read ? raw - read : raw, read, write),
			counters(raw, oldRead),
			counters(raw > oldRead ? raw - oldRead : raw, oldRead),
		],
	};
}
function assertCompletePayload(message: RecordValue, protocol: TokenRepairProtocol): void {
	// Validate only the selected final payload, not cumulative stream snapshots.
	if (
		(Object.hasOwn(message, "stop_reason") &&
			(typeof message.stop_reason !== "string" ||
				message.stop_reason.length === 0 ||
				["in_progress", "incomplete", "failed", "cancelled", "error"].includes(
					message.stop_reason,
				))) ||
		(Object.hasOwn(message, "status") &&
			message.status !== "completed" &&
			!(protocol === "gemini" && message.status === "requires_action")) ||
		message.incomplete_details != null ||
		message.done === false ||
		message.completed === false
	)
		throw new Error("response_incomplete");
}
function anthropic(usage: RecordValue): UsageEvidence {
	validateUsage(usage);
	const raw = count(usage.input_tokens);
	const read = optionalCount(usage.cache_read_input_tokens);
	const ttl = record(usage.cache_creation);
	const five = optionalCount(ttl.ephemeral_5m_input_tokens);
	const hour = optionalCount(ttl.ephemeral_1h_input_tokens);
	const aggregate = optionalCount(usage.cache_creation_input_tokens);
	const write = Math.max(aggregate, count(five + hour));
	return {
		protocol: "anthropic",
		raw,
		after: counters(raw, read, write, five, hour),
		legacy: [counters(raw, read, aggregate, five, hour)],
	};
}
function gemini(usage: RecordValue, metadata: boolean): UsageEvidence {
	validateUsage(usage);
	const raw = count(
		metadata
			? usage.promptTokenCount
			: (usage.total_input_tokens ?? usage.input_tokens ?? usage.prompt_tokens),
	);
	const read = optionalCount(
		metadata
			? usage.cachedContentTokenCount
			: (usage.total_cached_tokens ?? usage.cached_input_tokens),
	);
	return {
		protocol: "gemini",
		raw,
		after: counters(Math.max(raw - read, 0), read),
		legacy: [counters(raw, read), counters(raw > read ? raw - read : raw, read)],
	};
}

type ProtocolHint = Exclude<TokenRepairProtocol, "unknown">;

function identify(items: ParsedResponse, url: unknown, hint?: ProtocolHint): UsageEvidence {
	const path = typeof url === "string" ? url.split("?")[0] : "";
	const oaPath = /\/(?:responses|(?:chat\/)?completions)\/?$/.test(path);
	const antPath = /\/messages\/?$/.test(path);
	const gemPath = /:(?:streamGenerateContent|generateContent)$|\/interactions(?:\/[^/]+)?$/.test(
		path,
	);
	const candidates: UsageEvidence[] = [];
	const provisional = new Set<UsageEvidence>();
	let finalFrames: RecordValue[] = [];
	const accept = (usage: UsageEvidence, ...frames: RecordValue[]): void => {
		candidates.push(usage);
		if (items.stream && frames.some((frame) => frame.status === "in_progress"))
			provisional.add(usage);
		finalFrames = frames;
	};
	const assertHint = (protocol: ProtocolHint): void => {
		if (hint && hint !== protocol) throw new Error("protocol_conflict");
	};
	if (oaPath) assertHint("openai");
	if (antPath) assertHint("anthropic");
	if (gemPath) assertHint("gemini");
	let antUsage: RecordValue | undefined;
	let antStarted = false;
	let antStop: RecordValue | undefined;
	let terminal = items.done || !items.stream;
	const responseIds = new Set<string>();
	for (const event of items.items) {
		const type = event.type ?? event.event_type;
		if (hint) {
			// A manual selection disambiguates missing structure, never overrides it.
			const rawUsage = record(
				event.usage ?? record(event.interaction).usage ?? record(event.metadata).total_usage,
			);
			if (
				(typeof type === "string" && type.startsWith("response.")) ||
				event.object === "response" ||
				/^(?:chat\.|text_)?completion(?:\.chunk)?$/.test(String(event.object)) ||
				rawUsage.input_tokens_details != null ||
				rawUsage.prompt_tokens_details != null
			)
				assertHint("openai");
			if (
				["message", "message_start", "message_delta", "message_stop"].includes(String(type)) ||
				rawUsage.cache_creation != null
			)
				assertHint("anthropic");
			if (
				(typeof type === "string" && type.startsWith("interaction.")) ||
				event.object === "interaction" ||
				event.usageMetadata != null ||
				rawUsage.total_input_tokens != null ||
				rawUsage.total_cached_tokens != null
			)
				assertHint("gemini");
		}
		if (event._truncated != null || event.truncated === true)
			throw new Error("response_incomplete");
		const responseId =
			record(event.response).id ??
			record(event.interaction).id ??
			record(event.message).id ??
			event.responseId ??
			event.id;
		if (typeof responseId === "string") responseIds.add(responseId);
		if (responseIds.size > 1) throw new Error("multiple_requests");
		if (
			(oaPath || String(event.object).includes("completion")) &&
			Array.isArray(event.choices) &&
			event.choices.some((choice) => record(choice).finish_reason)
		)
			terminal = true;
		if (
			type === "error" ||
			type === "response.failed" ||
			type === "response.incomplete" ||
			event.error
		)
			throw new Error("response_incomplete");
		if (type === "message_start") {
			if (antStarted) throw new Error("multiple_requests");
			antStarted = true;
			antUsage = { ...record(record(event.message).usage) };
		} else if (type === "message_delta" && antUsage) {
			const next = record(event.usage);
			// Anthropic's input/cache zeroes in message_delta do not erase message_start.
			for (const [key, value] of Object.entries(next)) {
				if (
					["input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"].includes(
						key,
					) &&
					optionalCount(value) === 0
				)
					continue;
				antUsage[key] =
					key === "cache_creation"
						? { ...record(antUsage.cache_creation), ...record(value) }
						: value;
			}
		} else if (type === "message_stop") {
			antStop = event;
		}
		if (type === "response.completed") {
			const completed = record(event.response);
			accept(openai(record(completed.usage)), event, completed);
			terminal = true;
			continue;
		}
		if (typeof type === "string" && type.startsWith("response.")) continue;
		const metadata = record(event.usageMetadata);
		const interaction = record(event.interaction);
		const usage = record(event.usage ?? interaction.usage ?? record(event.metadata).total_usage);
		if (
			typeof type === "string" &&
			/^interaction\.(?:failed|incomplete|cancelled|budget_exceeded)$/.test(type)
		)
			throw new Error("response_incomplete");
		if (metadata.promptTokenCount != null && (gemPath || Array.isArray(event.candidates))) {
			accept(gemini(metadata, true), event);
			if (
				Array.isArray(event.candidates) &&
				event.candidates.some((candidate) => record(candidate).finishReason)
			)
				terminal = true;
		} else if (
			(usage.total_input_tokens != null ||
				usage.input_tokens != null ||
				usage.prompt_tokens != null) &&
			(gemPath || type === "interaction.completed" || event.object === "interaction")
		) {
			if (
				items.stream &&
				typeof type === "string" &&
				type.startsWith("interaction.") &&
				!["interaction.completed", "interaction.requires_action"].includes(type)
			)
				continue;
			accept(gemini(usage, false), event, interaction);
			if (
				type === "interaction.completed" ||
				event.status === "completed" ||
				interaction.status === "completed"
			)
				terminal = true;
		} else if (usage.input_tokens != null && (event.type === "message" || antPath)) {
			if (event.type === "message_delta" || event.type === "message_start") continue;
			if (items.stream) continue;
			accept(anthropic(usage), event);
		} else if (
			(usage.input_tokens != null || usage.prompt_tokens != null) &&
			(oaPath ||
				event.object === "response" ||
				/^(?:chat\.|text_)?completion(?:\.chunk)?$/.test(String(event.object)))
		) {
			accept(openai(usage), event);
			if (
				Array.isArray(event.choices) &&
				event.choices.some((choice) => record(choice).finish_reason)
			)
				terminal = true;
		} else if (hint) {
			// Only existing upstream fields are interpreted. Missing totals stay missing;
			// context snapshots and arbitrary numeric fields are never substitutes.
			if (hint === "openai" && (usage.input_tokens != null || usage.prompt_tokens != null)) {
				accept(openai(usage), event);
			} else if (hint === "anthropic" && usage.input_tokens != null && !items.stream) {
				accept(anthropic(usage), event);
			} else if (
				hint === "gemini" &&
				(metadata.promptTokenCount != null ||
					usage.total_input_tokens != null ||
					usage.input_tokens != null ||
					usage.prompt_tokens != null)
			) {
				accept(
					gemini(
						metadata.promptTokenCount != null ? metadata : usage,
						metadata.promptTokenCount != null,
					),
					event,
					interaction,
				);
			}
		}
	}
	if (antStarted) {
		if (!antStop || !antUsage) throw new Error("response_incomplete");
		accept(anthropic(antUsage), antStop);
		terminal = true;
	}
	if (!terminal) throw new Error("response_incomplete");
	if (!candidates.length) throw new Error("usage_missing");
	const protocols = new Set(candidates.map((candidate) => candidate.protocol));
	if (
		protocols.size !== 1 ||
		(oaPath && !protocols.has("openai")) ||
		(antPath && !protocols.has("anthropic")) ||
		(gemPath && !protocols.has("gemini"))
	)
		throw new Error("protocol_conflict");
	const selected = candidates[candidates.length - 1];
	for (const frame of finalFrames) assertCompletePayload(frame, selected.protocol);
	// Ignore explicitly provisional snapshots, never sum cumulative usage.
	if (
		selected.protocol === "openai" &&
		candidates.some(
			(candidate) => !provisional.has(candidate) && !equal(candidate.after, selected.after),
		)
	)
		throw new Error("multiple_usage_totals");
	return selected;
}

/** Pure evidence analysis: no DB, pricing, provider construction, or snapshot fallback. */
export async function analyzeTokenRepairEvidence(
	row: Row,
	rawDumpJson: string | null,
	options?: { signal?: AbortSignal; dumpDirectory?: string; protocolHint?: ProtocolHint },
): Promise<TokenRepairEvidence> {
	const before = Object.fromEntries(
		KEYS.map((key) => [key, row[key]]),
	) as unknown as TokenRepairCounters;
	const result = (
		classification: TokenRepairEvidence["classification"],
		reason: string,
		protocol: TokenRepairProtocol = "unknown",
		after: TokenRepairCounters | null = null,
		evidence = "",
	): TokenRepairEvidence => ({
		classification,
		protocol,
		reason,
		before,
		after,
		...(options?.protocolHint ? { manualProtocol: options.protocolHint } : {}),
		evidence: options?.protocolHint
			? `manualProtocol=${options.protocolHint}; ${evidence}`.trimEnd()
			: evidence,
	});
	try {
		checkAbort(options?.signal);
		for (const value of Object.values(before)) count(value);
		if (!rawDumpJson) return result("insufficient", "dump_missing");
		let dump = record(parseJson(rawDumpJson));
		if (dump.schema === "narrafork.api-request-dump.v1") {
			if (dump.requestId !== row.id || dump.truncated)
				return result("insufficient", "envelope_unverified");
			dump = record(dump.dump);
		}
		if (
			dump.spill != null &&
			optionalCount(record(dump.spill).bytes) + Buffer.byteLength(rawDumpJson) >
				TOKEN_REPAIR_MAX_DUMP_BYTES
		)
			return result("insufficient", "dump_over_budget");
		if (dump.spill != null)
			dump = await loadSpill(
				record(dump.spill),
				row.id,
				options?.dumpDirectory ?? getNarraforkPath("request-dumps"),
				options?.signal,
			);
		if (
			dump.spill != null ||
			dump.truncated ||
			dump.inlineTruncated ||
			dump.attemptsTruncated ||
			dump.initialRequest ||
			(Array.isArray(dump.attempts) && dump.attempts.length)
		)
			return result("insufficient", "dump_partial_or_retried");
		const response = record(dump.response);
		if (
			response.bodyTruncated ||
			response.bodyIncomplete ||
			response.error ||
			response.errorTruncated ||
			(typeof response.status === "number" && (response.status < 200 || response.status >= 300)) ||
			(typeof response.bodyBytesReceived === "number" &&
				typeof response.bodyBytesKept === "number" &&
				response.bodyBytesReceived > response.bodyBytesKept)
		)
			return result("insufficient", "response_incomplete");
		const usage = identify(
			responseItems(response),
			record(dump.request).url,
			options?.protocolHint,
		);
		checkAbort(options?.signal);
		for (const value of Object.values(usage.after)) count(value);
		const evidence = `${usage.protocol}: rawInput=${usage.raw}; netInput=${usage.after.inputTokens}; cacheRead=${usage.after.cachedInputTokens}; cacheWrite=${usage.after.cacheCreationInputTokens}; cache5m=${usage.after.cacheCreation5mTokens}; cache1h=${usage.after.cacheCreation1hTokens}`;
		if (equal(before, usage.after))
			return result("correct", "matches_protocol_usage", usage.protocol, usage.after, evidence);
		if (usage.legacy.some((legacy) => equal(before, legacy)))
			return result(
				"repairable",
				"matches_known_legacy_conversion",
				usage.protocol,
				usage.after,
				evidence,
			);
		return result("ambiguous", "unrecognized_previous_conversion", usage.protocol, null, evidence);
	} catch (error) {
		const message = error instanceof Error ? error.message : "";
		const reasons = [
			"analysis_cancelled",
			"invalid_token_counter",
			"dump_over_budget",
			"spill_unverified",
			"response_unparseable",
			"response_missing",
			"response_incomplete",
			"usage_missing",
			"protocol_conflict",
			"multiple_requests",
			"multiple_usage_totals",
		];
		const reason = reasons.includes(message) ? message : "dump_unavailable_or_invalid";
		return result(
			[
				"protocol_conflict",
				"multiple_requests",
				"multiple_usage_totals",
				"invalid_token_counter",
			].includes(reason)
				? "ambiguous"
				: "insufficient",
			reason,
		);
	}
}
