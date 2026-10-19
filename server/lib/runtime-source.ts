/**
 * Where a narrator's runtime relationships come from: its policy (and model) source,
 * its controller and the model pool it follows a source within.
 *
 * Every relationship is read from explicit storage — the `policy_source_narrator_id`
 * column and the runtime registration — never inferred from parent/variant/type.
 *
 * LEGACY COMPATIBILITY (delete in step 5): rows created before registrations existed
 * carry none. For exactly those rows, and only when they have a delegated shape, the
 * relationship is derived from the creating parent the way the old code did. This is
 * the single place that derivation lives; `trait-layer-service` uses the same shape
 * test for legacy capability ceilings.
 */
import { and, eq, inArray, isNull, like, or, type SQL } from "drizzle-orm";
import { narratorMessages, narrators } from "../db/schema";
import { getSubagentType, isSubagentVariant } from "./narrator-utils";
import {
	CONTROLLER_ACTIONS,
	type RunDeliverTo,
	type RuntimeRegistration,
	readRuntimeRegistrationColumn,
} from "./validators/runtime-registration";

/** Source chains longer than this are refused rather than walked. */
export const MAX_POLICY_SOURCE_DEPTH = 16;

export interface RuntimeSourceRow {
	policySourceNarratorId?: string | null;
	runtimeRegistrationJson?: unknown;
	type?: string | null;
	variant?: string | null;
	parentNarratorId?: string | null;
	subagentType?: string | null;
}

/** Any of the delegated row shapes the legacy code recognised. */
export function hasLegacyDelegatedShape(row: RuntimeSourceRow): boolean {
	return (
		row.type === "subagent" || isSubagentVariant(row.variant ?? "") || row.variant === "subagent"
	);
}

/** A delegated row that predates registrations (step 5: delete with the callers below). */
function isUnregisteredLegacyDelegation(row: RuntimeSourceRow): boolean {
	return row.runtimeRegistrationJson == null && hasLegacyDelegatedShape(row);
}

/** SQL form of the same legacy rule, for dependents of `sourceId` (step 5: delete). */
function legacyDependentsSql(sourceId: string): SQL | undefined {
	return and(
		isNull(narrators.policySourceNarratorId),
		isNull(narrators.runtimeRegistrationJson),
		eq(narrators.parentNarratorId, sourceId),
		or(
			eq(narrators.type, "subagent"),
			like(narrators.variant, "subagent:%"),
			eq(narrators.variant, "subagent"),
		),
	);
}

/** Policy (and model) source of a narrator, or null when it follows nobody. */
export function policySourceIdOf(row: RuntimeSourceRow): string | null {
	// An unreadable registration is never treated as "nothing registered".
	readRuntimeRegistrationColumn(row.runtimeRegistrationJson);
	if (row.policySourceNarratorId) return row.policySourceNarratorId;
	return isUnregisteredLegacyDelegation(row) ? (row.parentNarratorId ?? null) : null;
}

/** Rows whose policy source is `sourceId`. */
export function policyDependentsWhere(sourceId: string): SQL | undefined {
	return or(eq(narrators.policySourceNarratorId, sourceId), legacyDependentsSql(sourceId));
}

/** Session-level control grant. Invalid registrations throw. */
export function controllerOf(row: RuntimeSourceRow): RuntimeRegistration["controller"] | null {
	const registration = readRuntimeRegistrationColumn(row.runtimeRegistrationJson);
	if (registration) return registration.controller ?? null;
	return isUnregisteredLegacyDelegation(row) && row.parentNarratorId
		? { narratorId: row.parentNarratorId, actions: [...CONTROLLER_ACTIONS] }
		: null;
}

/**
 * Model pool a narrator follows its source within, fixed at creation. Null means no
 * pool: following a source then simply uses the source's model.
 */
export function modelPoolKeyOf(row: RuntimeSourceRow): string | null {
	const registration = readRuntimeRegistrationColumn(row.runtimeRegistrationJson);
	if (registration) return registration.modelPool ?? null;
	return isUnregisteredLegacyDelegation(row)
		? getSubagentType(row.variant ?? "") || row.subagentType || "general"
		: null;
}

/**
 * The registration a run is added to. For an unregistered legacy delegation the
 * derived relations above are materialized here, once, so registering its first run
 * does not silently turn it into an unrelated session.
 */
export function registrationBeforeRun(
	row: RuntimeSourceRow,
	legacyToolUseId?: string | null,
): RuntimeRegistration | null {
	const registration = readRuntimeRegistrationColumn(row.runtimeRegistrationJson);
	if (registration || !isUnregisteredLegacyDelegation(row)) return registration;
	const controller = controllerOf(row);
	const modelPool = modelPoolKeyOf(row);
	return {
		version: 1,
		...(controller ? { controller } : {}),
		...(modelPool ? { modelPool } : {}),
		...(legacyToolUseId ? { historyPlacement: { toolUseId: legacyToolUseId } } : {}),
	};
}

/**
 * Where the current run's result is delivered (independent of historical mounts). Legacy
 * delegated rows with no registered run deliver to their creating parent's latest
 * linked tool call (`legacyToolUseId`, looked up by the caller only for those rows).
 */
export function runDeliverToOf(
	row: RuntimeSourceRow,
	legacyToolUseId?: string | null,
): RunDeliverTo | undefined {
	const registration = readRuntimeRegistrationColumn(row.runtimeRegistrationJson);
	if (registration?.run) return registration.run.deliverTo;
	if (
		!needsLegacyDeliverTo(row) ||
		!legacyToolUseId ||
		legacyToolUseId.startsWith(LEGACY_STANDALONE_TRANSPORT_PREFIX)
	)
		return undefined;
	return { narratorId: row.parentNarratorId as string, toolUseId: legacyToolUseId };
}

/**
 * Whether {@link runDeliverToOf} needs the legacy linked tool-use lookup: a delegated
 * row that predates registrations altogether (step 5: delete). A registered row with
 * no run (e.g. a plugin standalone worker before its first run) delivers nowhere.
 */
export function needsLegacyDeliverTo(row: RuntimeSourceRow): boolean {
	return isUnregisteredLegacyDelegation(row) && !!row.parentNarratorId;
}

/**
 * Old standalone workers grouped their messages under `standalone-<id>`: a transport
 * link, never a tool call that receives a result.
 */
const LEGACY_STANDALONE_TRANSPORT_PREFIX = "standalone-";

/**
 * A transcript may remain mounted under a historical tool after its live run has gone
 * away (imports/archives). This fact grants no delivery or control authority. An
 * unregistered legacy delegation owns its historical placements (`"*"`, step 5: delete).
 */
export type OwnTurnPlacement = string | [string, string] | null;

export function ownTurnPlacementOf(row: RuntimeSourceRow): OwnTurnPlacement {
	const registration = readRuntimeRegistrationColumn(row.runtimeRegistrationJson);
	const historical = registration?.historyPlacement?.toolUseId;
	const current = registration?.run?.deliverTo?.toolUseId;
	// History and delivery are independent facts. Never replace the historical mount
	// when a new destination is registered; both own placements remain visible.
	if (historical) return current && current !== historical ? [historical, current] : historical;
	if (needsLegacyDeliverTo(row)) return "*";
	return current ?? null;
}

/**
 * The one "top-level message" rule of a session, for every kind of session alike:
 * a row is the session's own turn when it has no placement, or when it is placed under
 * either this session's historical mount or its current delivery mount
 * (`ownTurnPlacementOf`; `"*"` = every placement). Anything else is a nested transcript. Nothing here depends on how the
 * session was created. SQL form: `topLevelSql` / `ownTurnSql`.
 */
export function isTopLevelMessage(
	message: { parentToolUseId?: string | null },
	placement: OwnTurnPlacement | undefined,
): boolean {
	return (
		!message.parentToolUseId ||
		placement === "*" ||
		(Array.isArray(placement)
			? placement.includes(message.parentToolUseId)
			: !!placement && message.parentToolUseId === placement)
	);
}

/**
 * SQL form of {@link isTopLevelMessage} over `narrator_messages.parent_tool_use_id`.
 * `undefined` for `"*"`: every row is the session's own, so there is nothing to filter.
 */
export function ownTurnSql(placement: OwnTurnPlacement): SQL | undefined {
	if (placement === "*") return undefined;
	if (Array.isArray(placement))
		return or(
			isNull(narratorMessages.parentToolUseId),
			inArray(narratorMessages.parentToolUseId, placement),
		);
	return placement
		? or(isNull(narratorMessages.parentToolUseId), eq(narratorMessages.parentToolUseId, placement))
		: isNull(narratorMessages.parentToolUseId);
}

/**
 * The base system prompt a narrator stores. Runtime adds the effective sections
 * (senders, cwd, devices, summary) and the relationship/capability hints itself.
 *
 * LEGACY COMPATIBILITY (delete in step 5): delegations created before this stage
 * stored the fully built prompt, including those sections and the old reporting
 * hint. Cut such a row back to its base so nothing is stated twice.
 */
export function storedBasePromptOf(
	row: RuntimeSourceRow & { systemPrompt: string | null },
): string | null {
	const prompt = row.systemPrompt;
	if (!prompt || !hasLegacyDelegatedShape(row)) return prompt;
	const cut = [
		"\n\n## Conversation Context\n\n",
		"\n\n## Message Senders\n\n",
		"\n\nCommunicating with the parent narrator:\n",
		"\n\n与父叙述者通信：\n",
	]
		.map((marker) => prompt.indexOf(marker))
		.filter((index) => index >= 0);
	return cut.length ? prompt.slice(0, Math.min(...cut)) : prompt;
}

/** Columns every function above may read. */
export const RUNTIME_SOURCE_COLUMNS = {
	policySourceNarratorId: true,
	runtimeRegistrationJson: true,
	type: true,
	variant: true,
	parentNarratorId: true,
	subagentType: true,
} as const;

/**
 * This session's execution identity, never a message/notice author's identity.
 * `undefined` is unknown; `{ userId: null }` is explicitly anonymous.
 * LEGACY COMPATIBILITY (delete in step 5): before run principals were registered,
 * the row's own owner is the only identity fact available. Never borrow a source's.
 */
export function executionPrincipalOf(row: {
	runtimeRegistrationJson?: unknown;
	ownerUserId?: string | null;
}): { userId: string | null } | undefined {
	const principal = readRuntimeRegistrationColumn(row.runtimeRegistrationJson)?.run?.principal;
	return principal ?? (row.ownerUserId ? { userId: row.ownerUserId } : undefined);
}
