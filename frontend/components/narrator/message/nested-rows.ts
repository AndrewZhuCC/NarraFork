/**
 * nested-rows.ts — rows nested under another row.
 *
 * The server nests one kind of message under another: a legacy inline tool's reply
 * goes under the row holding the tool call (`buildMessageTree`; subagent sessions are
 * never nested, their parent row carries only an activity summary). Rows reach the
 * document flat, in order: each row, then the rows nested under it. A nested row names
 * its parent (`parentMessageId`).
 *
 * The conversation is the rows that are not nested. A nested row is read only through
 * its parent's tool call (`nestedRowsByParent`), and leaves the document with its parent.
 */

/** The fields this module reads. */
export interface NestableRow {
	id?: unknown;
	parentMessageId?: unknown;
}

/** Whether `row` is nested under another row. */
export function isNestedRow(row: { parentMessageId?: unknown }): boolean {
	return typeof row.parentMessageId === "string" && row.parentMessageId.length > 0;
}

/** The rows directly under each row, in document order. */
export function nestedRowsByParent<T extends NestableRow>(rows: readonly T[]): Map<string, T[]> {
	const byParent = new Map<string, T[]>();
	for (const row of rows) {
		if (!isNestedRow(row)) continue;
		const parentId = row.parentMessageId as string;
		const nested = byParent.get(parentId);
		if (nested) nested.push(row);
		else byParent.set(parentId, [row]);
	}
	return byParent;
}

/** `ids` and the ids of every row nested under one of them, at any depth. */
export function withNestedRowIds(
	rows: readonly NestableRow[],
	ids: ReadonlySet<string>,
): Set<string> {
	const all = new Set(ids);
	// A nested row follows its parent, so one forward pass reaches every depth.
	for (const row of rows) {
		if (isNestedRow(row) && all.has(row.parentMessageId as string) && typeof row.id === "string")
			all.add(row.id);
	}
	return all;
}

/** The index just past `rows[index]` and the rows nested under it. */
export function endOfNestedRows(rows: readonly NestableRow[], index: number): number {
	const ids = new Set<string>();
	const id = rows[index]?.id;
	if (typeof id === "string") ids.add(id);
	let end = index + 1;
	while (end < rows.length) {
		const row = rows[end];
		if (!isNestedRow(row) || !ids.has(row.parentMessageId as string)) break;
		if (typeof row.id === "string") ids.add(row.id);
		end++;
	}
	return end;
}
