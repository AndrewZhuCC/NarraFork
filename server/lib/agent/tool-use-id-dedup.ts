/**
 * Tool-use ID uniqueness and character-set guard.
 *
 * Every tool-calling API (Anthropic Messages, OpenAI Chat Completions/Responses,
 * NUG, Gemini) requires the `tool_use` identifiers inside one request to be unique,
 * and pairs each call with its result purely by that identifier. NarraFork stores the
 * identifier the provider minted, verbatim, and replays the full history on every turn.
 *
 * A stored identifier therefore has to satisfy *every* API the session may later be
 * routed to — not just the one that minted it. Two independent ways an upstream id
 * breaks a later replay:
 *
 *  1. **Not unique.** Some upstreams (observed with grok-4.5 behind an OpenAI-compatible
 *     proxy) mint the *same* identifier for every call — e.g. `call_go_0`. One turn looks
 *     fine, but once several assistant turns accumulate, the replayed history carries
 *     dozens of blocks sharing one id and the API rejects the request:
 *
 *       `messages[55].content[0]: Found duplicate tool_use id "call_go_0"`
 *
 *     The loop's same-turn dedup cannot help: each duplicate belongs to a *different*
 *     assistant message, and dropping them would silently discard real work.
 *
 *  2. **Not wire-safe.** Some upstreams mint ids like `Bash:0` / `Read:0`. Anthropic
 *     accepts them, so the originating turns succeed and the ids land in the DB; but
 *     NUG's strict gateway validates `^[a-zA-Z0-9_-]+$` and rejects the whole replayed
 *     history with a 400 the moment the session is switched to that channel:
 *
 *       `messages.1.content.1.tool_use.id: String should match pattern '^[a-zA-Z0-9_-]+$'`
 *
 *     This surfaces as a hard failure on an *old* session with no bad turn in sight, and
 *     no amount of retrying or model switching within the channel clears it.
 *
 *  3. **Too long.** Some upstreams mint wire-safe but very long ids (observed with
 *     grok-4.6 behind an OpenAI-compatible proxy: 82 characters). The originating
 *     channel accepts them, but the OpenAI Responses API caps `call_id` at 64 chars
 *     and rejects the replayed history on the first turn after a channel switch:
 *
 *       `Invalid 'input[384].call_id': string too long. Expected maximum length 64`
 *
 * All are fixed where every history is assembled — {@link uniquifyDbMessageToolUseIds},
 * run by `buildAdapterHistory` over the rows of a rebuild or of a loop's own transcript.
 * It runs on a copy-on-write message list, so the persisted rows and the UI keep the
 * original ids.
 *
 * Renaming keeps the first occurrence of a duplicate untouched and rewrites later
 * collisions, so the model still sees a stable id for the older calls. Identifiers are
 * opaque to the model, and each rewrite covers the tool_use together with its paired tool
 * result, so pairing is never broken.
 */

import { logger } from "../logger";
import type { DbMessage, DbToolCall } from "./provider";

/**
 * Upper bound for a generated identifier. Several gateways validate `call_id` /
 * `tool_call_id` length (64 is the most common ceiling), so stay clearly below it.
 */
const MAX_GENERATED_ID_CHARS = 56;
/** Marker that makes a rewritten id recognizable in dumps and logs. */
const RENAME_MARKER = "_nfdup";
/**
 * Maximum identifier length any routed-to API accepts on the wire.
 *
 * The OpenAI Responses API rejects `call_id` longer than 64 characters; other
 * channels accept more, but a stored id must satisfy the strictest channel the
 * session may later be switched to, so 64 is the ceiling for replayed history.
 */
const MAX_WIRE_ID_CHARS = 64;
/** Marker for ids rewritten purely because of length. */
const LENGTH_RENAME_MARKER = "_nfh";
/** How many sequential suffixes to probe before falling back to a random tail. */
const MAX_SEQUENTIAL_PROBES = 64;
/**
 * How many random tails to try before giving up on collision avoidance.
 *
 * The random space is 36^6 (~2.2 billion), so exhausting this is not a scenario that
 * reachable input produces. The bound exists because the loop is SYNCHRONOUS on the
 * server's only JS thread: an unbounded `for(;;)` whose exit depends on `Math.random`
 * has no worst case, and the failure mode is not a wrong id but a wedged event loop
 * taking every session with it. A bounded loop degrades to a longer id instead.
 */
const MAX_RANDOM_PROBES = 1000;

/**
 * The character set every tool-calling channel accepts.
 *
 * This is deliberately the *intersection*, not the union: NUG's strict gateway validates
 * `^[a-zA-Z0-9_-]+$`, which is the narrowest of the channels we route to (Anthropic also
 * accepts `:` and `.`). A session can be switched between channels at any time and the
 * whole history is replayed on every turn, so an id is only safe to store if it satisfies
 * the strictest validator — otherwise the failure appears later, on an old turn, in a
 * channel that never minted the id.
 */
const WIRE_SAFE_ID_PATTERN = /^[A-Za-z0-9_-]+$/;

/** Whether an identifier can be replayed to every channel as-is. */
export function isWireSafeToolUseId(id: string): boolean {
	return id.length <= MAX_WIRE_ID_CHARS && WIRE_SAFE_ID_PATTERN.test(id);
}

/**
 * Deterministic short hash for length-truncated identifiers.
 *
 * Synchronous by design: this runs inside history rebuild on the server's only JS
 * thread, so `crypto.subtle` (async) is not an option. FNV-1a is not collision-proof
 * across adversarial inputs, but the result is only a disambiguation suffix — the
 * caller still runs the renamed id through the uniqueness allocator, so a collision
 * degrades to one extra rename, never a broken pairing.
 */
function fnv1aSuffix(value: string): string {
	let hash = 0x811c9dc5;
	for (let i = 0; i < value.length; i++) {
		hash ^= value.charCodeAt(i);
		hash = Math.imul(hash, 0x01000193);
	}
	return (hash >>> 0).toString(36).padStart(7, "0");
}

/**
 * Coerce an identifier into the wire-safe character set and length bound.
 *
 * Pure and deterministic: the same upstream id always yields the same replacement, so an
 * id rewritten during one history rebuild keeps the same replacement on every later
 * rebuild. That stability is what lets the DB keep the original id — nothing has to
 * remember the mapping.
 *
 * Over-length ids keep a recognizable head and gain a hash suffix over the *original*
 * id, so two distinct long ids sharing a prefix do not truncate to the same string
 * (and if the hashes still collide, the uniqueness allocator resolves it).
 *
 * Does NOT consider uniqueness; callers combine it with {@link allocateUniqueToolUseId}.
 */
export function toWireSafeToolUseId(rawId: string): string {
	let cleaned = rawId;
	if (!WIRE_SAFE_ID_PATTERN.test(cleaned)) {
		cleaned = cleaned.replace(/[^A-Za-z0-9_-]/g, "_");
		if (cleaned.length === 0) cleaned = "tool";
	}
	if (cleaned.length <= MAX_WIRE_ID_CHARS) return cleaned;
	const suffix = `${LENGTH_RENAME_MARKER}${fnv1aSuffix(rawId)}`;
	const head = cleaned.slice(0, MAX_WIRE_ID_CHARS - suffix.length);
	return `${head}${suffix}`;
}

/** Strip characters that some gateways reject in tool identifiers. */
function sanitizeIdBase(rawId: string): string {
	return toWireSafeToolUseId(rawId);
}

/**
 * Allocate an identifier that is not in `used`, derived from `rawId` so logs and raw
 * dumps still show which call it came from. The returned id is NOT added to `used` —
 * callers decide when to reserve it.
 */
export function allocateUniqueToolUseId(rawId: string, used: ReadonlySet<string>): string {
	const base = sanitizeIdBase(rawId);
	for (let n = 2; n < MAX_SEQUENTIAL_PROBES; n++) {
		const suffix = `${RENAME_MARKER}${n}`;
		const head = base.slice(0, Math.max(1, MAX_GENERATED_ID_CHARS - suffix.length));
		const candidate = `${head}${suffix}`;
		if (!used.has(candidate)) return candidate;
	}
	// Degenerate case (dozens of collisions on one id): fall back to a random tail.
	for (let attempt = 0; attempt < MAX_RANDOM_PROBES; attempt++) {
		const suffix = `${RENAME_MARKER}${Math.random().toString(36).slice(2, 8)}`;
		const head = base.slice(0, Math.max(1, MAX_GENERATED_ID_CHARS - suffix.length));
		const candidate = `${head}${suffix}`;
		if (!used.has(candidate)) return candidate;
	}
	// Every probe collided, which means `used` is pathological rather than merely large.
	// A UUID tail is not a retry of the same gamble: it is wide enough that a collision
	// is not a case worth coding for, and returning SOMETHING keeps the caller's
	// contract (a wire-safe id) instead of throwing mid-history-rebuild — which would
	// fail the whole turn over a naming detail. Length still respects the cap, so a
	// gateway that validates id length cannot reject it.
	const uniqueSuffix = `${RENAME_MARKER}${crypto.randomUUID().replaceAll("-", "").slice(0, 20)}`;
	const head = base.slice(0, Math.max(1, MAX_GENERATED_ID_CHARS - uniqueSuffix.length));
	logger.warn("tool_use id dedup exhausted random probes; using a UUID tail", {
		rawId,
		usedSize: used.size,
	});
	return `${head}${uniqueSuffix}`;
}

// ---------------------------------------------------------------------------
// DB message path (buildHistory)
// ---------------------------------------------------------------------------

function isRecord(value: unknown): value is Record<string, unknown> {
	return value != null && typeof value === "object" && !Array.isArray(value);
}

/**
 * Rewrite `tool_use` block ids inside a persisted assistant `contentJson`.
 *
 * Providers resolve a stored block against the tool-call rows by id
 * (`toolCallMap.get(block.id)`), so a block left on the old id would be dropped from the
 * replayed turn. `assignments` holds the new ids per original id in tool-call order;
 * blocks consume them in the same order, which also covers the rare case of one message
 * carrying the same original id twice.
 */
function rewriteContentJsonToolUseIds(
	contentJson: unknown,
	assignments: Map<string, string[]>,
): unknown {
	if (!Array.isArray(contentJson)) return contentJson;
	const cursors = new Map<string, number>();
	let mutated = false;
	const blocks = contentJson.map((raw) => {
		if (!isRecord(raw) || raw.type !== "tool_use") return raw;
		const key =
			typeof raw.id === "string" ? "id" : typeof raw.toolUseId === "string" ? "toolUseId" : null;
		if (!key) return raw;
		const originalId = raw[key] as string;
		const list = assignments.get(originalId);
		if (!list || list.length === 0) return raw;
		const cursor = cursors.get(originalId) ?? 0;
		// Reuse the last assignment once the list is exhausted: a stored block without a
		// matching tool-call row is dropped by the providers anyway, and reusing keeps a
		// stale block from resurrecting the duplicate id.
		const nextId = list[Math.min(cursor, list.length - 1)];
		cursors.set(originalId, cursor + 1);
		if (nextId === originalId) return raw;
		mutated = true;
		return { ...raw, [key]: nextId };
	});
	return mutated ? blocks : contentJson;
}

/**
 * Make every `tool_use` identifier unique *and* wire-safe across a rebuilt message list.
 *
 * Returns the original array untouched when there is nothing to fix (the normal case),
 * so well-behaved providers pay only one scan over the tool-call rows.
 */
export function uniquifyDbMessageToolUseIds(
	messages: DbMessage[],
	context?: { narratorId?: string; provider?: string; model?: string },
): DbMessage[] {
	// Fast path — detect a problem before allocating anything. Both defects have to be
	// probed here: an unsafe id is rejected even when it appears exactly once, so
	// short-circuiting on duplicates alone would let `Bash:0` through untouched.
	const probe = new Set<string>();
	let needsRewrite = false;
	for (const msg of messages) {
		for (const tc of msg.toolCalls ?? []) {
			if (probe.has(tc.toolUseId) || !isWireSafeToolUseId(tc.toolUseId)) {
				needsRewrite = true;
				break;
			}
			probe.add(tc.toolUseId);
		}
		if (needsRewrite) break;
	}
	if (!needsRewrite) return messages;

	const used = new Set<string>();
	const out: DbMessage[] = [];
	const renamedOriginals = new Set<string>();
	let renamedCount = 0;
	let unsafeCount = 0;

	for (const msg of messages) {
		if (!msg.toolCalls?.length) {
			out.push(msg);
			continue;
		}

		const assignments = new Map<string, string[]>();
		let messageChanged = false;
		const nextToolCalls: DbToolCall[] = msg.toolCalls.map((tc) => {
			// Sanitize first, then de-duplicate. Doing it in this order means the
			// uniqueness check runs on the ids that will actually go on the wire: two
			// distinct upstream ids can sanitize to the same string (`a:0` and `a.0` both
			// become `a_0`), and checking before sanitizing would let that collide.
			let id = toWireSafeToolUseId(tc.toolUseId);
			if (id !== tc.toolUseId) unsafeCount++;
			if (used.has(id)) id = allocateUniqueToolUseId(id, used);
			used.add(id);
			if (id !== tc.toolUseId) {
				messageChanged = true;
				renamedCount++;
				renamedOriginals.add(tc.toolUseId);
			}
			const list = assignments.get(tc.toolUseId);
			if (list) list.push(id);
			else assignments.set(tc.toolUseId, [id]);
			return id === tc.toolUseId ? tc : { ...tc, toolUseId: id };
		});

		if (!messageChanged) {
			out.push(msg);
			continue;
		}

		out.push({
			...msg,
			toolCalls: nextToolCalls,
			contentJson: rewriteContentJsonToolUseIds(msg.contentJson, assignments),
		});
	}

	logger.warn("Rewrote unusable tool_use IDs while rebuilding model history", {
		narratorId: context?.narratorId,
		provider: context?.provider,
		model: context?.model,
		renamedCount,
		unsafeCount,
		duplicateCount: renamedCount - unsafeCount,
		originalIds: [...renamedOriginals].slice(0, 10),
		note: "Upstream minted a duplicate or non-wire-safe tool_use id; DB rows keep the original.",
	});

	return out;
}
