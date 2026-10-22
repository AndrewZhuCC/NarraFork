/**
 * storage.ts — how an envelope's stored content maps onto block rows (P1).
 *
 * P1 writes every envelope as rows of `narrator_blocks` next to the compatible
 * `content_json` that readers still read (docs/BLOCK_STORAGE-DESIGN.md §10.1). A row's
 * payload is the exact stored elements the block came from, with the reader-only fields
 * moved to `state`:
 *
 *   payload = { raw: [element, …] }      state = { raw: [fields | null, …] }
 *
 * so concatenating the rows in ordinal order and merging each state back reproduces the
 * stored array (`assembleContent`): the two representations can never drift apart, and
 * a state-only change (a translation, a card's status) is not a body change. The columns
 * the database indexes and triggers read — kind, subtype, phase, model_text, wire_id —
 * are what `decodeLegacyContent` makes of the same elements.
 *
 * Canonical payloads (parts, provider_meta) replace `raw` once every reader reads blocks
 * (P2); until then they would only store every body twice.
 *
 * Pure and deterministic, like the decoder: no server imports.
 */

import { type DecodableMessage, decodeLegacyContent } from "./decode";
import type { BlockKind, BlockPhase, CanonicalBlock, CanonicalEnvelope } from "./types";

export interface StoredBlockPayload {
	raw: unknown[];
}

/** Reader-only fields of each element in `payload.raw`, index-aligned; null when none. */
export interface StoredBlockState {
	raw: Array<Record<string, unknown> | null>;
}

/** One block of an envelope, before the store gives it an id and an ordinal. */
export interface BlockDraft {
	/** Identity within the envelope, stable across rewrites of the same content. */
	key: string;
	kind: BlockKind;
	subtype: string | null;
	phase: BlockPhase;
	wireId: string | null;
	modelText: string | null;
	payload: StoredBlockPayload;
	state: StoredBlockState | null;
	/** The canonical block decoded from the same elements; null for leftover elements. */
	canonical: CanonicalBlock | null;
}

export interface SplitOptions {
	/**
	 * The step is still streaming: an output or reasoning element checkpointed as
	 * incomplete is `open`, not `aborted` (§3.5).
	 */
	streaming?: boolean;
}

/** Fields readers patch in place without changing what the block says (§3.4, §5.4). */
const STATE_KEYS: Readonly<Record<string, readonly string[]>> = {
	text: ["translatedText"],
	reasoning: ["translatedText"],
	thinking: ["translatedText"],
	review_feedback: ["applied"],
	compact: ["status", "attempts", "mode", "error"],
	segment_compact: ["status", "error"],
};
const CARD_STATE_KEYS = ["isLatest"];

type RawElement = Record<string, unknown>;

function record(value: unknown): RawElement | undefined {
	return value && typeof value === "object" && !Array.isArray(value)
		? (value as RawElement)
		: undefined;
}

function stateKeys(element: RawElement): readonly string[] {
	const type = typeof element.type === "string" ? element.type : "";
	return [...(STATE_KEYS[type] ?? []), ...CARD_STATE_KEYS];
}

/** An element without its reader-only fields, and those fields (null when none). */
function separateState(element: unknown): [unknown, Record<string, unknown> | null] {
	const value = record(element);
	if (!value) return [element, null];
	let fields: Record<string, unknown> | null = null;
	let rest: RawElement | null = null;
	for (const key of stateKeys(value)) {
		if (!Object.hasOwn(value, key)) continue;
		rest ??= { ...value };
		fields ??= {};
		fields[key] = value[key];
		delete rest[key];
	}
	return [rest ?? element, fields];
}

/** The decoder's block ids are `${messageId}:${index}` for blocks read from an element. */
function decodedIndex(messageId: string, block: CanonicalBlock): number | undefined {
	const prefix = `${messageId}:`;
	if (!block.id.startsWith(prefix)) return undefined;
	const index = Number(block.id.slice(prefix.length));
	return Number.isSafeInteger(index) && index >= 0 ? index : undefined;
}

function identityOf(element: unknown): string | undefined {
	const value = record(element);
	if (!value) return undefined;
	const id = value.id ?? value.toolUseId;
	return typeof id === "string" && id ? id : undefined;
}

function typeOf(element: unknown): string {
	const value = record(element);
	return value && typeof value.type === "string" ? value.type : typeof element;
}

function draftOf(
	key: string,
	elements: readonly unknown[],
	block: CanonicalBlock | null,
	options: SplitOptions,
): BlockDraft {
	const raw: unknown[] = [];
	const state: Array<Record<string, unknown> | null> = [];
	for (const element of elements) {
		const [rest, fields] = separateState(element);
		raw.push(rest);
		state.push(fields);
	}
	const kind: BlockKind = block?.kind ?? "output";
	let phase: BlockPhase = block?.phase ?? "done";
	if (
		options.streaming &&
		(kind === "output" || kind === "reasoning") &&
		phase === "aborted" &&
		record(elements.find((element) => record(element)))?.completed === false
	)
		phase = "open";
	return {
		key,
		kind,
		subtype: block?.kind === "tool" || block?.kind === "context" ? (block.subtype as string) : null,
		phase,
		wireId: block?.kind === "tool" ? block.wireId : null,
		modelText: block?.kind === "input" || block?.kind === "context" ? block.modelText : null,
		payload: { raw },
		state: state.some(Boolean) ? { raw: state } : null,
		canonical: block,
	};
}

/**
 * The blocks of one stored envelope, in order. Every element belongs to exactly one
 * block: an element the decoder gives no block of its own (a display-only `/bash`
 * result, an unknown type) rides with the block before it, or the first one.
 */
export function splitEnvelope(
	message: DecodableMessage,
	options: SplitOptions = {},
): { blocks: BlockDraft[]; envelope: CanonicalEnvelope } {
	const elements: unknown[] = Array.isArray(message.contentJson) ? message.contentJson : [];
	const envelope = decodeLegacyContent({ ...message, contentJson: elements });
	if (message.role !== "assistant") {
		const block = envelope.blocks[0] ?? null;
		return { blocks: [draftOf("envelope", elements, block, options)], envelope };
	}
	// The decoder skips non-object elements, so its indexes count record elements only.
	const recordPositions: number[] = [];
	elements.forEach((element, position) => {
		if (record(element)) recordPositions.push(position);
	});
	const starts: Array<{ position: number; block: CanonicalBlock }> = [];
	for (const block of envelope.blocks) {
		const index = decodedIndex(message.id, block);
		const position = index === undefined ? undefined : recordPositions[index];
		if (position !== undefined) starts.push({ position, block });
	}
	starts.sort((a, b) => a.position - b.position);
	if (!starts.length)
		return elements.length
			? { blocks: [draftOf("leftover", elements, null, options)], envelope }
			: { blocks: [], envelope };
	const seen = new Map<string, number>();
	const blocks = starts.map(({ position, block }, index) => {
		const from = index === 0 ? 0 : position;
		const to = starts[index + 1]?.position ?? elements.length;
		const element = elements[position];
		const identity = identityOf(element);
		const base = identity
			? `${typeOf(element)}:${identity}`
			: `${typeOf(element)}#${seen.get(typeOf(element)) ?? 0}`;
		if (!identity) seen.set(typeOf(element), (seen.get(typeOf(element)) ?? 0) + 1);
		return draftOf(base, elements.slice(from, to), block, options);
	});
	// Identities are unique per envelope in practice; keep keys unique regardless.
	const used = new Map<string, number>();
	for (const block of blocks) {
		const count = used.get(block.key) ?? 0;
		used.set(block.key, count + 1);
		if (count) block.key = `${block.key}~${count}`;
	}
	return { blocks, envelope };
}

/** The stored array the rows say, in ordinal order: the inverse of `splitEnvelope`. */
export function assembleContent(
	rows: ReadonlyArray<{ payload: unknown; state: unknown }>,
): unknown[] {
	const content: unknown[] = [];
	for (const row of rows) {
		const raw = record(row.payload)?.raw;
		if (!Array.isArray(raw)) continue;
		const state = record(row.state)?.raw;
		raw.forEach((element, index) => {
			const fields = Array.isArray(state) ? record(state[index]) : undefined;
			content.push(fields && record(element) ? { ...record(element), ...fields } : element);
		});
	}
	return content;
}
