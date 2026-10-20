import type { Database } from "bun:sqlite";
import {
	SELECT_ROWS_MAX_BYTES,
	SELECT_ROWS_MAX_PARAMS,
	SELECT_ROWS_MAX_PARAMS_BYTES,
	SELECT_ROWS_MAX_ROWS,
	SELECT_ROWS_MAX_SQL_BYTES,
	type SelectRowsParams,
	type SelectRowsResult,
} from "./protocol";

export function validateSelectRows(input: SelectRowsParams): void {
	if (
		typeof input.sql !== "string" ||
		Buffer.byteLength(input.sql) > SELECT_ROWS_MAX_SQL_BYTES ||
		!/^\s*(?:select|with)\b/i.test(input.sql) ||
		input.sql.includes(";") ||
		!Array.isArray(input.params) ||
		input.params.length > SELECT_ROWS_MAX_PARAMS ||
		!Number.isInteger(input.maxRows) ||
		input.maxRows < 1 ||
		input.maxRows > SELECT_ROWS_MAX_ROWS
	)
		throw new Error("Invalid or oversized read query");

	// Stop before serializing an entire oversized parameter array on the HTTP thread.
	// for..of also observes holes as undefined, rather than skipping malformed sparse arrays.
	let bytes = 2;
	let separatorBytes = 0;
	for (const value of input.params) {
		if (
			value !== null &&
			((typeof value !== "string" && typeof value !== "number") ||
				(typeof value === "number" && !Number.isFinite(value)) ||
				(typeof value === "string" && Buffer.byteLength(value) > SELECT_ROWS_MAX_PARAMS_BYTES))
		)
			throw new Error("Invalid or oversized read query");
		bytes += Buffer.byteLength(JSON.stringify(value)) + separatorBytes;
		separatorBytes = 1;
		if (bytes > SELECT_ROWS_MAX_PARAMS_BYTES) throw new Error("Invalid or oversized read query");
	}
}

function valueBytes(value: unknown): number {
	if (typeof value === "string") return Buffer.byteLength(value);
	if (value instanceof Uint8Array) return value.byteLength;
	return 8;
}

/**
 * Worker-only bounded SELECT executor, with no application DB/bootstrap dependencies.
 *
 * Rows come back positionally (`values` shape) so callers can map them by selection order: joins
 * that select two `id` columns would collide in object mode.
 */
export function runSelectRows(sqlite: Database, input: SelectRowsParams): SelectRowsResult {
	validateSelectRows(input);

	const inner = sqlite.prepare(input.sql);
	const columns = inner.columnNames;
	inner.finalize();
	// A CTE body accepts only SELECT, including when a WITH prefix disguises a write. Positional
	// aliases keep duplicate column names apart. Keep snippet() in the original FTS SELECT context:
	// do not move it to the outer projection. LIMIT bounds iteration even if the query has none.
	const aliases = columns.map((_, index) => `c${index}`);
	const statement = sqlite.prepare<Record<string, unknown>, Array<string | number | null>>(
		`WITH nf_rows(${aliases.join(",")}) AS (${input.sql}) SELECT * FROM nf_rows LIMIT ${input.maxRows + 1}`,
	);
	const rows: unknown[][] = [];
	let bytes = 0;
	try {
		for (const record of statement.iterate(...input.params)) {
			if (rows.length >= input.maxRows) throw new Error("Read query exceeded its row budget");
			const row = aliases.map((alias) => record[alias]);
			for (const value of row) bytes += valueBytes(value);
			if (bytes > SELECT_ROWS_MAX_BYTES) throw new Error("Read query exceeded its byte budget");
			rows.push(row);
		}
		return { columns, rows };
	} finally {
		statement.finalize();
	}
}
