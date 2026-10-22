/**
 * SQL predicates for {@link MessageSearchScope}, shared by the SQLite and PostgreSQL stores.
 *
 * Portable SQL only, and built from the scope's booleans alone, so each result is one of a
 * handful of fixed strings: safe to interpolate and safe to use in a cached template's key.
 * The aliases are the stores' own (`n` narrators, `m` messages, `b` blocks).
 */

import { FULL_MESSAGE_SCOPE, type MessageSearchScope } from "./types";

/** Stable cache-key fragment for a scope. */
export function scopeKey(scope: MessageSearchScope = FULL_MESSAGE_SCOPE): string {
	return `${scope.subagents ? "S" : "s"}${scope.system ? "Y" : "y"}${scope.tools ? "T" : "t"}`;
}

/** `AND …` restricting a narrators row aliased `n`; empty when nothing is excluded. */
export function narratorScopeSql(scope: MessageSearchScope = FULL_MESSAGE_SCOPE): string {
	return scope.subagents ? "" : "AND n.type <> 'subagent'";
}

/**
 * `AND …` restricting a narrator_messages row aliased `m` (joined to `n`).
 *
 * System content is recognized by role (`sys`, `system`, `disp` rows are written by the
 * runtime) and by origin (`system`: automation prompts sent as `user`). A null origin
 * predates the column and counts as a person, as everywhere else. An envelope's text never
 * holds tool input or output, so `tools` has nothing to exclude here; see
 * {@link blockScopeSql}.
 */
export function messageScopeSql(scope: MessageSearchScope = FULL_MESSAGE_SCOPE): string {
	const clauses: string[] = [];
	if (!scope.subagents) clauses.push("n.type <> 'subagent'");
	if (!scope.system) {
		clauses.push("m.role IN ('user', 'assistant')");
		clauses.push("COALESCE(m.origin, 'user') <> 'system'");
	}
	return clauses.length === 0 ? "" : `AND ${clauses.join(" AND ")}`;
}

/** `AND …` restricting a narrator_blocks row aliased `b` and its envelope `m`/`n`. */
export function blockScopeSql(scope: MessageSearchScope = FULL_MESSAGE_SCOPE): string {
	const kinds: string[] = [];
	if (!scope.system) kinds.push("'context'");
	if (!scope.tools) kinds.push("'tool'");
	const kindClause = kinds.length === 0 ? "" : `AND b.kind NOT IN (${kinds.join(", ")})`;
	return [messageScopeSql(scope), kindClause].filter(Boolean).join(" ");
}
