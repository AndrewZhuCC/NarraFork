/**
 * narrator-store.ts — the write layer for what the model reads of a conversation
 * (docs/BLOCK_STORAGE-DESIGN.md §5.1).
 *
 * Producers write the model-visible parts of a narrator's history through these
 * operations: input and notice rows, assistant steps and their items, tool calls and
 * their results. Each operation is one transaction, so the history a request is
 * projected from never holds half of a write:
 *   - a tool_use block commits with the ledger row (and execution segment) its result
 *     settles;
 *   - a result commits with the input the call actually ran with;
 *   - a discarded attempt's unexecuted rows disappear with its blocks.
 *
 * Storage is still `narrator_messages.content_json` plus `narrator_tool_calls` (P0); the
 * persistence layer implements these operations and moving them onto block tables (P1)
 * happens behind this interface. Direct writes to the history tables elsewhere are
 * frozen by `narrator-history-writers.guard.test.ts`.
 *
 * Operations call through `narratorPersistence` when invoked, not when this module loads,
 * so tests that replace a persistence method see every producer use the replacement.
 */

import { narratorPersistence } from "./narrator-persistence";

type Persistence = typeof narratorPersistence;

export const narratorStore = {
	/** A user input row: a human turn, or a user turn the system wrote on someone's behalf. */
	appendInput: (...args: Parameters<Persistence["persistUserMessage"]>) =>
		narratorPersistence.persistUserMessage(...args),
	/** A runtime notice the model reads at its seq (a `sys` row). */
	appendNotice: (...args: Parameters<Persistence["persistSystemMessage"]>) =>
		narratorPersistence.persistSystemMessage(...args),
	/** The notice of a manual tool load or unload. */
	appendToolChange: (...args: Parameters<Persistence["persistToolChangeMessage"]>) =>
		narratorPersistence.persistToolChangeMessage(...args),
	openEpoch: (...args: Parameters<Persistence["persistEpochMarker"]>) =>
		narratorPersistence.persistEpochMarker(...args),
	/** Open an assistant step whose items arrive as they stream. */
	openStep: (...args: Parameters<Persistence["createPartialAssistantMessage"]>) =>
		narratorPersistence.createPartialAssistantMessage(...args),
	/** Write or update one item of an open step; a tool_use also creates its ledger row. */
	writeStepItem: (...args: Parameters<Persistence["appendBlockToMessage"]>) =>
		narratorPersistence.appendBlockToMessage(...args),
	/** Write a whole step at once, with the ledger rows of its calls. */
	appendStep: (...args: Parameters<Persistence["persistAssistantMessage"]>) =>
		narratorPersistence.persistAssistantMessage(...args),
	/** Drop what a discarded provider attempt wrote into an open step. */
	discardAttempt: (...args: Parameters<Persistence["discardAttemptBlocks"]>) =>
		narratorPersistence.discardAttemptBlocks(...args),
	/** Final metadata of a sealed step; its incomplete items become interrupted. */
	sealStepMeta: (...args: Parameters<Persistence["patchStepMeta"]>) =>
		narratorPersistence.patchStepMeta(...args),
	/** Seal a step kept after a stop: its incomplete items are interrupted, not streaming. */
	sealStep: (...args: Parameters<Persistence["sealStepBlocks"]>) =>
		narratorPersistence.sealStepBlocks(...args),
	/** Put a sealed step's calls into the model's call order. */
	orderStepCalls: (...args: Parameters<Persistence["reorderStepToolBlocks"]>) =>
		narratorPersistence.reorderStepToolBlocks(...args),
	/** A user-run tool's card with its running ledger row (`/bash`). */
	callManualTool: (...args: Parameters<Persistence["persistManualToolCall"]>) =>
		narratorPersistence.persistManualToolCall(...args),
	/** Settle a call: its result, and the input it actually ran with. */
	settleTool: (...args: Parameters<Persistence["updateToolCallResult"]>) =>
		narratorPersistence.updateToolCallResult(...args),
	/** Create the next attempt of a call (re-execution). */
	retryTool: (...args: Parameters<Persistence["prepareToolCallAttempt"]>) =>
		narratorPersistence.prepareToolCallAttempt(...args),
	/**
	 * Reader-only fields of an envelope (a translation, a card's status): no model-history
	 * bump, no COW, no search index (§5.4).
	 */
	patchState: (...args: Parameters<Persistence["patchState"]>) =>
		narratorPersistence.patchState(...args),
};
