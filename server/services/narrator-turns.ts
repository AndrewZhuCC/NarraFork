/**
 * narrator-turns.ts — one `narrator_turns` row per run (docs/BLOCK_STORAGE-DESIGN.md §3.6,
 * §4.4).
 *
 * A run is one pass of the orchestrator's loop, from the input that woke the narrator to
 * idle or waiting for the user. Its row is written twice: inserted `running` when the
 * run starts and updated once when it ends. Steps, tool calls and usage accumulate in
 * memory meanwhile, never in the table. Every envelope written while the run is active
 * carries its id as `turn_id` (`newEnvelopeColumns`), so a narrator's history groups by
 * run without a lookup.
 */

// Read through the namespace: the handle is a live binding, and importers that mock
// `server/db` without it can still load this module.
import * as database from "../db";
import { hotSafe } from "../lib/hot-safe";
import { generateId } from "../lib/id";
import { logger } from "../lib/logger";

export type TurnStatus = "completed" | "interrupted" | "failed";

interface ModelUsage {
	requests: number;
	inputTokens: number;
	outputTokens: number;
	reasoningTokens: number;
	cachedInputTokens: number;
	cacheCreationInputTokens: number;
}

interface ActiveTurn {
	id: string;
	steps: number;
	toolCalls: number;
	usage: Map<string, ModelUsage>;
}

/** The running turn of each narrator, by narrator id. */
const activeTurns = hotSafe("narrator-turns:active", () => new Map<string, ActiveTurn>());

let turnSchema: boolean | undefined;

/** Whether this database has the turns table (a schema predating it records none). */
function hasTurnSchema(): boolean {
	turnSchema ??= !!database.sqlite
		.prepare("SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'narrator_turns'")
		.get();
	return turnSchema;
}

/** The run a narrator is in, if any: envelopes written now belong to it. */
export function activeTurnId(narratorId: string): string | null {
	return activeTurns.get(narratorId)?.id ?? null;
}

/**
 * Open a run. Its trigger is the input that woke the narrator: the newest envelope in
 * its history when that is a user input not yet claimed by an earlier run, which then
 * joins this run. Returns the turn id, or null without the turns table.
 */
export function startTurn(narratorId: string): string | null {
	if (!hasTurnSchema()) return null;
	const previous = activeTurns.get(narratorId);
	if (previous) endTurn(narratorId, previous.id, { status: "interrupted", stopReason: "replaced" });
	const id = generateId();
	try {
		database.sqlite.transaction(() => {
			const latest = database.sqlite
				.prepare(
					`SELECT m.id, m.role, m.turn_id AS turnId FROM narrator_message_refs r
					 JOIN narrator_messages m ON m.id = r.message_id
					 WHERE r.narrator_id = ? ORDER BY r.seq DESC LIMIT 1`,
				)
				.get(narratorId) as { id: string; role: string; turnId: string | null } | null;
			const trigger = latest?.role === "user" && latest.turnId === null ? latest.id : null;
			database.sqlite
				.prepare(
					`INSERT INTO narrator_turns (id, narrator_id, status, trigger_message_id, started_at)
					 VALUES (?, ?, 'running', ?, ?)`,
				)
				.run(id, narratorId, trigger, new Date().toISOString());
			if (trigger)
				database.sqlite
					.prepare("UPDATE narrator_messages SET turn_id = ? WHERE id = ? AND turn_id IS NULL")
					.run(id, trigger);
		})();
	} catch (error) {
		// Bookkeeping only: a run never fails because its summary row could not be written.
		logger.warn("Failed to record turn start", { narratorId, error: String(error) });
		return null;
	}
	activeTurns.set(narratorId, { id, steps: 0, toolCalls: 0, usage: new Map() });
	return id;
}

/** Count one provider request of the narrator's running turn, with its usage. */
export function recordTurnStep(
	narratorId: string,
	model: string | undefined,
	usage:
		| {
				promptTokens?: number;
				inputTokens?: number;
				completionTokens?: number;
				reasoningTokens?: number;
				cachedInputTokens?: number;
				cacheCreationInputTokens?: number;
		  }
		| undefined,
): void {
	const turn = activeTurns.get(narratorId);
	if (!turn) return;
	turn.steps++;
	const key = model ?? "unknown";
	const total = turn.usage.get(key) ?? {
		requests: 0,
		inputTokens: 0,
		outputTokens: 0,
		reasoningTokens: 0,
		cachedInputTokens: 0,
		cacheCreationInputTokens: 0,
	};
	total.requests++;
	total.inputTokens += usage?.inputTokens ?? usage?.promptTokens ?? 0;
	total.outputTokens += usage?.completionTokens ?? 0;
	total.reasoningTokens += usage?.reasoningTokens ?? 0;
	total.cachedInputTokens += usage?.cachedInputTokens ?? 0;
	total.cacheCreationInputTokens += usage?.cacheCreationInputTokens ?? 0;
	turn.usage.set(key, total);
}

/** Count one tool call of the narrator's running turn. */
export function recordTurnToolCall(narratorId: string): void {
	const turn = activeTurns.get(narratorId);
	if (turn) turn.toolCalls++;
}

/** Close a run with its outcome; a no-op unless `turnId` is the narrator's running turn. */
export function endTurn(
	narratorId: string,
	turnId: string | null,
	outcome: { status: TurnStatus; stopReason: string },
): void {
	const turn = activeTurns.get(narratorId);
	if (!turn || turn.id !== turnId) return;
	activeTurns.delete(narratorId);
	try {
		database.sqlite
			.prepare(
				`UPDATE narrator_turns SET status = ?, stop_reason = ?, ended_at = ?, step_count = ?,
					tool_call_count = ?, usage_json = ?
				 WHERE id = ? AND status = 'running'`,
			)
			.run(
				outcome.status,
				outcome.stopReason,
				new Date().toISOString(),
				turn.steps,
				turn.toolCalls,
				turn.usage.size ? JSON.stringify(Object.fromEntries(turn.usage)) : null,
				turn.id,
			);
	} catch (error) {
		logger.warn("Failed to record turn end", { narratorId, error: String(error) });
	}
}

/**
 * Startup: nothing runs yet, so a turn still `running` was cut off by the previous
 * process. It ends `interrupted` at its last envelope (or its start, when it wrote none).
 */
export function recoverInterruptedTurns(): number {
	if (!hasTurnSchema()) return 0;
	const running = database.sqlite
		.prepare("SELECT id, narrator_id AS narratorId FROM narrator_turns WHERE status = 'running'")
		.all() as Array<{ id: string; narratorId: string }>;
	const lastEnvelope = database.sqlite.prepare(
		`SELECT m.created_at AS createdAt FROM narrator_message_refs r
		 JOIN narrator_messages m ON m.id = r.message_id
		 WHERE r.narrator_id = ? AND m.turn_id = ? ORDER BY r.seq DESC LIMIT 1`,
	);
	const close = database.sqlite.prepare(
		`UPDATE narrator_turns SET status = 'interrupted', stop_reason = 'restart',
			ended_at = coalesce(?, started_at)
		 WHERE id = ? AND status = 'running'`,
	);
	database.sqlite.transaction(() => {
		for (const turn of running) {
			const last = lastEnvelope.get(turn.narratorId, turn.id) as { createdAt: string } | null;
			close.run(last?.createdAt ?? null, turn.id);
		}
	})();
	return running.length;
}
