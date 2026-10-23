/**
 * Seeding order for the shared read fixtures, expressed once for both backends.
 *
 * Kept separate from the row data so each backend only supplies an `insert` that maps a
 * logical table name onto its own Drizzle table. Users go first because projects reference
 * their owners.
 */

import { aclGrantRows, bulkProjectRows, projectRows, userRows } from "./read-fixtures";

export type FixtureTable = "users" | "projects" | "aclGrants";

/** How one backend inserts a batch. Rows are backend-agnostic property bags. */
export type FixtureInsert = (
	table: FixtureTable,
	rows: Array<Record<string, unknown>>,
) => Promise<void>;

/** Chunked so neither driver hits a bound-parameter ceiling on the 205-row batches. */
async function insertChunked(
	insert: FixtureInsert,
	table: FixtureTable,
	rows: ReadonlyArray<Record<string, unknown>>,
): Promise<void> {
	for (let index = 0; index < rows.length; index += 40) {
		await insert(table, rows.slice(index, index + 40));
	}
}

export async function seedFixtures(insert: FixtureInsert): Promise<void> {
	await insertChunked(insert, "users", userRows);
	await insertChunked(insert, "projects", [...projectRows, ...bulkProjectRows()]);
	await insertChunked(insert, "aclGrants", aclGrantRows);
}
