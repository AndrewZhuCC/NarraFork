/**
 * narrator-epochs.ts — epochs of a narrator's request prefix (docs/BLOCK_STORAGE-DESIGN.md
 * §7.7, P1 scope).
 *
 * Within an epoch the request's tool array only grows by appended history, so the
 * provider's prompt cache survives tool loads. An epoch opens at a compact (the compact
 * element records `epoch`) or at an epoch marker row the runtime writes when the target
 * changes or its cache has certainly gone cold. Either one records the optional tools
 * loaded when it opened; that set, not a backwards derivation, is then the array's share.
 *
 * `reconcileEpoch` runs before each pass's history is built and writes what the
 * request must say, as rows:
 *   - at a boundary, a new epoch marker, but only when rebuilding would actually change
 *     the tool layout (otherwise the bytes are the same and the cache is kept anyway);
 *   - inside a frozen epoch, a system notice for every tool loaded or unloaded without
 *     one (resident mode, preloads), so the array never shifts under the cache.
 *
 * P1 freezes the tool set only. Frozen tool definitions, frozen system sections with
 * reminders, and the prefix check are the rest of §7.7.
 */

import type { DbMessage } from "@shared/agent-protocol/types";
import { activeDatabaseBackend, sqlite } from "../db";
import { resolveEpochTarget } from "../lib/agent";
import {
	deriveToolLoadingState,
	type EpochRecord,
	epochRecordOf,
	latestToolLoadingEpoch,
	type ToolLoadingEpoch,
	toolLoadingDrift,
} from "../lib/agent/tool-loading";
import { getBuiltinToolNames, getBuiltinToolRoutines } from "../lib/builtin-routines";
import type { Locale } from "../lib/i18n";
import { logger } from "../lib/logger";
import { narratorStore } from "./narrator-store";

/** What a reconciled load notice says a tool does: its routine's description, else its name. */
export function optionalToolDescription(toolName: string, locale: Locale): string {
	const routine = getBuiltinToolRoutines().find(
		(candidate) => candidate.tool && getBuiltinToolNames(candidate.tool).includes(toolName),
	);
	return (
		(locale === "zh-CN" ? routine?.tool?.descriptionZh : undefined) ??
		routine?.tool?.descriptionEn ??
		toolName
	);
}

/**
 * What a compact's epoch records: the loaded tools and target of the running session.
 * Undefined when the session is not running; the epoch then derives its tool set.
 */
export function compactEpochRecord(
	active:
		| { alive: boolean; model: string; provider: string; _enabledOptionalTools: Set<string> }
		| undefined,
): EpochRecord | undefined {
	if (!active?.alive) return undefined;
	return {
		reason: "compact",
		target: resolveEpochTarget(active.model, active.provider).fingerprint,
		toolNames: [...active._enabledOptionalTools].sort(),
	};
}

/** The epoch the latest compact opened, when it recorded one (SQLite; PG derives). */
export function compactToolLoadingEpoch(narratorId: string): ToolLoadingEpoch | undefined {
	if (activeDatabaseBackend !== "sqlite") return undefined;
	// Only the epoch: the summary beside it can be large.
	const row = sqlite
		.prepare(
			`SELECT json_extract(m.content_json, '$[0].type') AS type,
				json_extract(m.content_json, '$[0].epoch') AS epoch
			 FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id
			 WHERE r.narrator_id = ? AND r.is_compact = 1 ORDER BY r.seq DESC LIMIT 1`,
		)
		.get(narratorId) as { type: string | null; epoch: string | null } | null;
	if (row?.type !== "compact" || !row.epoch) return undefined;
	let recorded: unknown;
	try {
		recorded = JSON.parse(row.epoch);
	} catch {
		return undefined;
	}
	const epoch = epochRecordOf({ type: "compact", epoch: recorded });
	return epoch
		? { toolNames: epoch.toolNames, ...(epoch.target ? { target: epoch.target } : {}) }
		: undefined;
}

/** The epoch a history window is in: its latest marker row, else the compact before it. */
export function toolLoadingEpochOf(
	narratorId: string,
	messages: readonly DbMessage[],
): ToolLoadingEpoch | undefined {
	return latestToolLoadingEpoch(messages) ?? compactToolLoadingEpoch(narratorId);
}

/** The newest assistant step this narrator holds: when and through what it was sent. */
function lastStep(
	narratorId: string,
): { provider: string | null; model: string | null; createdAt: string } | undefined {
	if (activeDatabaseBackend !== "sqlite") return undefined;
	return (
		(sqlite
			.prepare(
				`SELECT m.provider, m.model, m.created_at AS createdAt FROM narrator_message_refs r
				 JOIN narrator_messages m ON m.id = r.message_id
				 WHERE r.narrator_id = ? AND m.role = 'assistant' AND m.parent_tool_use_id IS NULL
				 ORDER BY r.seq DESC LIMIT 1`,
			)
			.get(narratorId) as
			| { provider: string | null; model: string | null; createdAt: string }
			| null
			| undefined) ?? undefined
	);
}

const sameSet = (a: ReadonlySet<string>, b: ReadonlySet<string>) =>
	a.size === b.size && [...a].every((name) => b.has(name));

/**
 * Write what this pass's request must carry before its history is built. Returns
 * whether rows were written (the caller reloads the history then). Best effort: a
 * failure is logged and the request goes out with the history as it is.
 */
export async function reconcileEpoch(input: {
	narratorId: string;
	messages: readonly DbMessage[];
	managed: ReadonlySet<string>;
	current: Iterable<string>;
	model: string;
	provider: string;
	/** Notice text for a reconciled load or unload of `toolName`. */
	noticeText: (kind: "tool_loaded" | "tool_unloaded", toolName: string) => string;
	now?: number;
	resolveTarget?: typeof resolveEpochTarget;
}): Promise<boolean> {
	const { narratorId, messages, managed } = input;
	if (managed.size === 0 || activeDatabaseBackend !== "sqlite") return false;
	try {
		const resolveTarget = input.resolveTarget ?? resolveEpochTarget;
		const target = resolveTarget(input.model, input.provider);
		const epoch = toolLoadingEpochOf(narratorId, messages);
		const state = deriveToolLoadingState(messages, managed, input.current, epoch);
		const last = lastStep(narratorId);
		const sentTo =
			last?.model && last.provider
				? resolveTarget(last.model, last.provider).fingerprint
				: epoch?.target;
		const targetChanged = sentTo !== undefined && sentTo !== target.fingerprint;
		const cold =
			target.cacheTtlMs !== null &&
			!!last &&
			(input.now ?? Date.now()) - Date.parse(last.createdAt) > target.cacheTtlMs;
		const layoutChanges = state.loaded.length > 0 || !sameSet(state.base, state.current);
		if ((targetChanged || cold) && layoutChanges) {
			await narratorStore.openEpoch(narratorId, {
				reason: targetChanged ? "target" : "cold",
				target: target.fingerprint,
				toolNames: [...state.current].sort(),
			});
			return true;
		}
		if (!epoch) return false;
		const drift = toolLoadingDrift(messages, state);
		for (const [type, names] of [
			["tool_loaded", drift.loads],
			["tool_unloaded", drift.unloads],
		] as const)
			for (const toolName of names)
				await narratorStore.appendToolChange(
					narratorId,
					{ type, toolName, toolNames: [toolName], text: input.noticeText(type, toolName) },
					"system",
				);
		return drift.loads.length > 0 || drift.unloads.length > 0;
	} catch (error) {
		logger.warn("Epoch reconcile failed; the request keeps the history as it is", {
			narratorId,
			error: String(error),
		});
		return false;
	}
}
