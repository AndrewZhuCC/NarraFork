/**
 * narrator-body-length.ts — the size of a block-format envelope's body, measured in SQL
 * (docs/BLOCK_STORAGE-DESIGN.md §10.5).
 *
 * Readers that clamp or budget a body before reading it (transcript export's subagent
 * excerpts, the project archive's row limit) measured `content_json` with `length()`. A
 * block-format body is the array its rows assemble to (`assembleContent`), and its length
 * follows from the rows' stored text without parsing a payload:
 *
 *   - a payload is `{"raw":[…]}`: 10 characters around its elements;
 *   - the rows' elements join with commas inside `[…]`;
 *   - each reader-only field set in `state` merges into its element, adding its own length
 *     less one (the merge drops two braces and adds a comma).
 *
 * Every character this adds or drops is ASCII, so the same arithmetic counts characters
 * (`length()` of text, as the column was measured) or UTF-8 bytes. Worker-safe: no imports.
 */

/**
 * The length of the array `messageId`'s block rows assemble to, as an SQL expression.
 * `messageId` is an SQL expression naming the envelope (a column of the outer query).
 */
export function assembledBodyLengthSql(messageId: string, unit: "chars" | "bytes"): string {
	const length = (value: string) =>
		unit === "bytes" ? `length(CAST(${value} AS BLOB))` : `length(${value})`;
	return `(SELECT max(2, 1 + coalesce(sum(
		CASE WHEN ${length("b.payload")} > 10 THEN ${length("b.payload")} - 9 + coalesce((
			SELECT sum(${length("s.value")} - 1)
			FROM json_each(CASE WHEN json_valid(b.state) THEN b.state END, '$.raw') AS s
			WHERE s.type = 'object'
		), 0) ELSE 0 END
	), 0))
	FROM narrator_blocks b WHERE b.message_id = ${messageId})`;
}
