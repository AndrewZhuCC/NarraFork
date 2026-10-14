import type { Snapshot } from "./pg-baseline-model";

const MiB = 1024 * 1024;
const MAX_SQL_BYTES = MiB;
const MAX_TOTAL_BYTES = 32 * MiB;
const MAX_SNAPSHOT_BYTES = 8 * MiB;
const MAX_FILES = 4096;
const MAX_TOKENS = 200_000;
const MAX_STATEMENTS = 32_768;
const MAX_DEPTH = 128;
const MAX_TABLES = 4096;
const MAX_COLUMNS = 1600;
const MAX_PROBLEMS = 128;

/** `start`/`end` are source offsets, so a statement's exact text (including the opaque body of a
 * dollar-quoted literal) can be recovered for the procedural allow-list digest. */
type Token = {
	kind: "word" | "identifier" | "literal" | "symbol";
	value: string;
	start: number;
	end: number;
};

/** A deliberately limited lexer, not a PostgreSQL expression or DDL generator.
 * Literal contents are opaque, so commas/semicolons/DDL words in them cannot affect layout. */
function lex(sql: string): Token[] {
	const tokens: Token[] = [];
	let tokenStart = 0;
	const push = (kind: Token["kind"], value: string, end: number) => {
		if (tokens.length >= MAX_TOKENS) throw new Error("SQL token limit exceeded");
		tokens.push({ kind, value, start: tokenStart, end });
	};
	for (let i = 0; i < sql.length; ) {
		tokenStart = i;
		const char = sql[i];
		if (/\s/.test(char)) {
			i++;
			continue;
		}
		if (sql.startsWith("--", i)) {
			const end = sql.indexOf("\n", i + 2);
			i = end === -1 ? sql.length : end + 1;
			continue;
		}
		if (sql.startsWith("/*", i)) {
			i += 2;
			let depth = 1;
			while (i < sql.length && depth > 0) {
				if (sql.startsWith("/*", i)) {
					if (++depth > MAX_DEPTH) throw new Error("Comment nesting limit exceeded");
					i += 2;
				} else if (sql.startsWith("*/", i)) {
					depth--;
					i += 2;
				} else i++;
			}
			if (depth !== 0) throw new Error("Unterminated block comment");
			continue;
		}
		if (char === '"' || char === "'") {
			const escaped =
				char === "'" &&
				i > 0 &&
				/[eE]/.test(sql[i - 1]) &&
				(i < 2 || !/[A-Za-z0-9_$]/.test(sql[i - 2]));
			let value = "";
			let closed = false;
			i++;
			while (i < sql.length) {
				if (escaped && sql[i] === "\\") {
					i += 2;
					continue;
				}
				if (sql[i] === char) {
					if (sql[i + 1] === char) {
						if (char === '"') value += char;
						i += 2;
						continue;
					}
					i++;
					closed = true;
					break;
				}
				if (char === '"') value += sql[i];
				i++;
			}
			if (!closed) throw new Error("Unterminated quoted identifier/string");
			push(char === '"' ? "identifier" : "literal", value, i);
			continue;
		}
		if (char === "$") {
			const delimiter = /^\$(?:[A-Za-z_][A-Za-z0-9_]*)?\$/.exec(sql.slice(i))?.[0];
			if (delimiter) {
				const end = sql.indexOf(delimiter, i + delimiter.length);
				if (end === -1) throw new Error("Unterminated dollar string");
				i = end + delimiter.length;
				push("literal", "", i);
				continue;
			}
		}
		if (/[A-Za-z_]/.test(char)) {
			const start = i++;
			while (i < sql.length && /[A-Za-z0-9_$]/.test(sql[i])) i++;
			push("word", sql.slice(start, i).toLowerCase(), i);
			continue;
		}
		push("symbol", char, i + 1);
		i++;
	}
	return tokens;
}

/** Split only at zero grouping depth, including ARRAY[...] defaults. */
function split(tokens: readonly Token[], separator: string): Token[][] {
	const parts: Token[][] = [];
	const stack: string[] = [];
	let current: Token[] = [];
	for (const token of tokens) {
		if (token.kind === "symbol") {
			if (token.value === "(" || token.value === "[") {
				if (stack.length >= MAX_DEPTH) throw new Error("SQL grouping limit exceeded");
				stack.push(token.value === "(" ? ")" : "]");
			} else if (token.value === ")" || token.value === "]") {
				if (stack.pop() !== token.value) throw new Error("Unbalanced SQL grouping");
			} else if (token.value === ";" && stack.length > 0) {
				throw new Error("Semicolon inside SQL grouping");
			}
			if (token.value === separator && stack.length === 0) {
				parts.push(current);
				current = [];
				continue;
			}
		}
		current.push(token);
	}
	if (stack.length > 0) throw new Error("Unbalanced SQL grouping");
	parts.push(current);
	return parts;
}

class Cursor {
	position = 0;
	constructor(readonly tokens: readonly Token[]) {}
	keyword(word: string): boolean {
		const token = this.tokens[this.position];
		if (token?.kind !== "word" || token.value !== word) return false;
		this.position++;
		return true;
	}
	expect(word: string): void {
		if (!this.keyword(word)) throw new Error(`Expected ${word}`);
	}
	symbol(value: string): boolean {
		const token = this.tokens[this.position];
		if (token?.kind !== "symbol" || token.value !== value) return false;
		this.position++;
		return true;
	}
	identifier(): string {
		const token = this.tokens[this.position++];
		if (
			!token ||
			(token.kind !== "word" && token.kind !== "identifier") ||
			!token.value ||
			Buffer.byteLength(token.value) > 63
		)
			throw new Error("Expected supported PostgreSQL identifier (1–63 UTF-8 bytes)");
		return token.value;
	}
	table(): string {
		const name = this.identifier();
		if (!this.symbol(".")) return name;
		if (name !== "public") throw new Error(`Unsupported schema ${name}`);
		return this.identifier();
	}
	end(): void {
		if (this.position !== this.tokens.length) throw new Error("Unsupported trailing SQL syntax");
	}
	rest(): readonly Token[] {
		return this.tokens.slice(this.position);
	}
}

const constraints = new Set(["constraint", "primary", "foreign", "unique", "check", "exclude"]);
function isConstraint(token: Token | undefined): boolean {
	return token?.kind === "word" && constraints.has(token.value);
}
function optionalExists(cursor: Cursor, negated = false): boolean {
	if (!cursor.keyword("if")) return false;
	if (negated) cursor.expect("not");
	cursor.expect("exists");
	return true;
}
function columns(order: Map<string, string[]>, table: string): string[] {
	const list = order.get(table);
	if (!list) throw new Error(`Unknown SQL table ${table}`);
	return list;
}
function append(list: string[], column: string): void {
	if (list.includes(column)) throw new Error(`Duplicate SQL column ${column}`);
	if (list.length >= MAX_COLUMNS) throw new Error("Column count limit exceeded");
	list.push(column);
}
function requireDefinition(tokens: readonly Token[]): void {
	if (!tokens.length || !["word", "identifier"].includes(tokens[0].kind))
		throw new Error("Unsupported/missing column type definition");
}

function createTable(cursor: Cursor, order: Map<string, string[]>): void {
	const ifNotExists = optionalExists(cursor, true);
	const table = cursor.table();
	if (!cursor.symbol("("))
		throw new Error("Unsupported CREATE TABLE AS/OF/LIKE or missing definitions");
	const start = cursor.position;
	let depth = 1;
	while (cursor.position < cursor.tokens.length && depth > 0) {
		if (cursor.symbol("(")) depth++;
		else if (cursor.symbol(")")) depth--;
		else cursor.position++;
	}
	if (depth !== 0) throw new Error("Unbalanced CREATE TABLE definitions");
	const definitions = split(cursor.tokens.slice(start, cursor.position - 1), ",");
	cursor.end(); // INHERITS, partitions, CTAS and typed tables require a different model.
	const list: string[] = [];
	for (const definition of definitions) {
		if (definition.length === 0) throw new Error("Empty CREATE TABLE definition");
		if (isConstraint(definition[0])) continue;
		if (definition[0].kind === "word" && definition[0].value === "like")
			throw new Error("Unsupported CREATE TABLE LIKE");
		const column = new Cursor(definition);
		const name = column.identifier();
		requireDefinition(column.rest());
		append(list, name);
	}
	if (order.has(table)) {
		if (ifNotExists) return;
		throw new Error(`Duplicate SQL table ${table}`);
	}
	if (order.size >= MAX_TABLES) throw new Error("Table count limit exceeded");
	order.set(table, list);
}

function alterTable(cursor: Cursor, order: Map<string, string[]>): void {
	const ifExists = optionalExists(cursor);
	cursor.keyword("only");
	let table = cursor.table();
	if (ifExists && !order.has(table)) return;
	for (const action of split(cursor.rest(), ",")) {
		const part = new Cursor(action);
		const list = columns(order, table);
		if (part.keyword("add")) {
			if (isConstraint(part.tokens[part.position])) continue;
			part.keyword("column");
			const optional = optionalExists(part, true);
			const name = part.identifier();
			requireDefinition(part.rest());
			if (optional && list.includes(name)) continue;
			append(list, name);
		} else if (part.keyword("drop")) {
			if (part.keyword("constraint")) continue;
			part.keyword("column");
			const optional = optionalExists(part);
			const name = part.identifier();
			if (!part.keyword("cascade")) part.keyword("restrict");
			part.end();
			const index = list.indexOf(name);
			if (index === -1 && !optional) throw new Error(`Unknown SQL column ${table}.${name}`);
			if (index !== -1) list.splice(index, 1);
		} else if (part.keyword("rename")) {
			if (part.keyword("constraint")) {
				part.identifier();
				part.expect("to");
				part.identifier();
				part.end();
				continue;
			}
			if (part.keyword("to")) {
				const renamed = part.identifier();
				part.end();
				if (order.has(renamed)) throw new Error(`Duplicate SQL table ${renamed}`);
				order.delete(table);
				order.set(renamed, list);
				table = renamed;
			} else {
				part.keyword("column");
				const old = part.identifier();
				part.expect("to");
				const renamed = part.identifier();
				part.end();
				const index = list.indexOf(old);
				if (index === -1) throw new Error(`Unknown SQL column ${table}.${old}`);
				if (list.includes(renamed)) throw new Error(`Duplicate SQL column ${table}.${renamed}`);
				list[index] = renamed;
			}
		} else if (part.keyword("alter")) {
			if (part.keyword("constraint")) continue;
			part.keyword("column");
			const name = part.identifier();
			if (!list.includes(name)) throw new Error(`Unknown SQL column ${table}.${name}`);
			const words = part.rest().map((token) => (token.kind === "word" ? token.value : "?"));
			const prefix = words.slice(0, 3).join(" ");
			if (
				!words.length ||
				!(
					words[0] === "type" ||
					/^(set|drop) (default|not null|identity|expression|generated|storage|compression|statistics)/.test(
						prefix,
					) ||
					prefix === "set data type" ||
					prefix.startsWith("add generated ")
				)
			)
				throw new Error(`Unsupported ALTER COLUMN action for ${table}.${name}`);
		} else if (part.keyword("validate") && part.keyword("constraint")) {
			part.identifier();
			part.end();
		} else throw new Error(`Unsupported ALTER TABLE action for ${table}`);
	}
}

function dropTable(cursor: Cursor, order: Map<string, string[]>): void {
	const ifExists = optionalExists(cursor);
	const remainder = [...cursor.rest()];
	const last = remainder[remainder.length - 1];
	if (last?.kind === "word" && ["cascade", "restrict"].includes(last.value)) remainder.pop();
	for (const item of split(remainder, ",")) {
		const part = new Cursor(item);
		const table = part.table();
		part.end();
		if (!order.delete(table) && !ifExists) throw new Error(`Unknown SQL table ${table}`);
	}
}

/** The baseline's UPDATE backfill and SELECT setval call only these builtins.
 * Unknown functions/procedural statements could execute dynamic DDL; never infer their effects. */
function dataOnly(tokens: readonly Token[]): void {
	const safe = new Set(["setval", "pg_get_serial_sequence", "coalesce", "max", "in", "values"]);
	for (let i = 0; i < tokens.length - 1; i++) {
		const token = tokens[i];
		if (
			["word", "identifier"].includes(token.kind) &&
			tokens[i + 1].kind === "symbol" &&
			tokens[i + 1].value === "(" &&
			(token.kind !== "word" || !safe.has(token.value) || tokens[i - 1]?.value === ".")
		)
			throw new Error(`Unsupported potentially dynamic SQL function ${token.value}`);
	}
}

/**
 * Reviewed procedural statements, admitted by EXACT text only.
 *
 * Layout replay cannot see inside a dollar-quoted body, so procedural DDL stays fail-closed in
 * general. These are the model-history revision triggers of migration 0012: they bump integer
 * counters and never alter, create or drop a relation or column. Each entry is the SHA-256 of
 * the statement as written in the SQL file (trimmed, without its terminating semicolon); a single
 * changed character falls back to the generic rejection and requires a fresh review here.
 */
const REVIEWED_PROCEDURAL_STATEMENTS = new Map<string, string>([
	// 0012_narrator_model_history_revision.sql
	[
		"4771573f041a0f54e3ea0665ef68ee93aea8f0be9c027c72de57d693732cce33",
		"function model_history_v1_message",
	],
	[
		"9dcbda8218ea5783c1bc304ab0d7472816d21a45a0fb4dc0d9b955a8ba113c39",
		"trigger model_history_v1_message",
	],
	[
		"2a90b0a82ef23a094793b4b8ae322c6725192bcaa2a0f829720cb816c3063f67",
		"function model_history_v1_tool",
	],
	[
		"135b8ca862703e6c811b7f2e7da3585600201e6d6be1f187b9c297a9edf94446",
		"trigger model_history_v1_tool",
	],
	[
		"a1e81f82f84ea6999608cba94b3cb28c6d9e46ec1a62c4538a67e42a70feb16f",
		"function model_history_v1_ref",
	],
	[
		"d59312f6957e2345c014e0f9ea6ec918edf029e7bceb02909bbe8d13220fffcf",
		"trigger model_history_v1_ref",
	],
	[
		"4a4d437cd8296f4b050c4f812bdfab84876a3dd5090821a8da85f70c7fa6e4d8",
		"function model_history_v1_sender",
	],
	[
		"f6f18636aabff4b30eb9d592463bb999f5b249322c936328371b9abbdd928798",
		"trigger model_history_v1_sender",
	],
	[
		"5f257c52cde5633aaee32d32c6afbee74353d707b668edddd38573edea2983bb",
		"function model_history_v1_narrator",
	],
	[
		"5121c08ad3af983c5dbeb029b7bf4f0a43831fc3d945f9455e0d4596b270f2da",
		"trigger model_history_v1_narrator",
	],
	[
		"4549dcc98c05b70107eed1856f9687b2ab67795464f97dd8acd77d38b58d73ae",
		"seed model_history_revision_contracts",
	],
]);

/** The exact source text of one statement, as the allow-list digests were computed. */
function statementText(tokens: readonly Token[], sql: string): string {
	const first = tokens[0];
	const last = tokens[tokens.length - 1];
	return first && last ? sql.slice(first.start, last.end).trim() : "";
}

function reviewedProcedural(tokens: readonly Token[], sql: string): boolean {
	const digest = new Bun.CryptoHasher("sha256").update(statementText(tokens, sql)).digest("hex");
	const label = REVIEWED_PROCEDURAL_STATEMENTS.get(digest);
	if (!label) return false;
	// Defence in depth: the digest pins the text, the label pins what kind of statement it is.
	const cursor = new Cursor(tokens);
	if (label.startsWith("seed ")) return cursor.keyword("insert") && cursor.keyword("into");
	return cursor.keyword("create") && cursor.keyword(label.split(" ")[0]);
}

function replay(tokens: readonly Token[], order: Map<string, string[]>, sql = ""): void {
	if (sql && reviewedProcedural(tokens, sql)) return;
	const cursor = new Cursor(tokens);
	if (cursor.keyword("create")) {
		if (cursor.keyword("table")) {
			createTable(cursor, order);
			return;
		}
		cursor.keyword("unique");
		if (cursor.keyword("index")) return;
	} else if (cursor.keyword("alter")) {
		if (cursor.keyword("table")) {
			alterTable(cursor, order);
			return;
		}
		if (cursor.keyword("index")) return;
	} else if (cursor.keyword("drop")) {
		if (cursor.keyword("table")) {
			dropTable(cursor, order);
			return;
		}
		if (cursor.keyword("index")) return;
	} else if (cursor.keyword("comment")) {
		cursor.expect("on");
		return;
	} else if (["select", "update", "insert", "delete"].some((word) => cursor.keyword(word))) {
		dataOnly(tokens);
		return;
	}
	throw new Error("Unsupported SQL statement (including procedural/dynamic DDL)");
}

/** Derive physical order exclusively from the committed SQL, never catalog observations
 * or snapshot declaration order. Snapshot contributes only the final table/column SETS.
 * Any problem is fatal to the calling baseline gate; partial order is diagnostic only. */
export function physicalColumnOrderFromSql(
	sqls: readonly string[],
	snapshot: Snapshot,
): { order: Map<string, string[]>; problems: string[] } {
	const order = new Map<string, string[]>();
	const problems: string[] = [];
	const problem = (message: string) => {
		if (problems.length < MAX_PROBLEMS) problems.push(message.slice(0, 400));
	};
	try {
		if (sqls.length > MAX_FILES) throw new Error("SQL file count limit exceeded");
		let bytes = 0;
		for (const sql of sqls) {
			const size = Buffer.byteLength(sql);
			if (size > MAX_SQL_BYTES) throw new Error("SQL file exceeds 1 MiB");
			bytes += size;
			if (bytes > MAX_TOTAL_BYTES) throw new Error("SQL total exceeds 32 MiB");
		}
		if (Buffer.byteLength(JSON.stringify(snapshot)) > MAX_SNAPSHOT_BYTES)
			throw new Error("Snapshot exceeds 8 MiB");
		let statements = 0;
		for (const [file, sql] of sqls.entries()) {
			try {
				for (const tokens of split(lex(sql), ";")) {
					if (!tokens.length) continue;
					if (++statements > MAX_STATEMENTS) throw new Error("SQL statement limit exceeded");
					try {
						replay(tokens, order, sql);
					} catch (cause) {
						problem(`SQL ${file}, statement ${statements}: ${String(cause)}`);
					}
				}
			} catch (cause) {
				problem(`SQL ${file}: ${String(cause)}`);
			}
			if (statements > MAX_STATEMENTS || problems.length >= MAX_PROBLEMS) break;
		}
		const expectedTables = new Set<string>();
		for (const table of Object.values(snapshot.tables)) {
			if (expectedTables.size >= MAX_TABLES) throw new Error("Snapshot table count limit exceeded");
			if (table.schema && table.schema !== "public") {
				problem(`Unsupported snapshot schema ${table.schema}`);
				continue;
			}
			if (expectedTables.has(table.name)) problem(`Duplicate snapshot table ${table.name}`);
			expectedTables.add(table.name);
			const expectedColumns = new Set<string>();
			for (const [key, column] of Object.entries(table.columns)) {
				if (expectedColumns.size >= MAX_COLUMNS)
					throw new Error("Snapshot column count limit exceeded");
				if (key !== column.name) problem(`Snapshot column key/name mismatch ${table.name}.${key}`);
				if (expectedColumns.has(column.name))
					problem(`Duplicate snapshot column ${table.name}.${column.name}`);
				expectedColumns.add(column.name);
			}
			const actual = order.get(table.name);
			if (!actual) {
				problem(`Snapshot table missing from SQL replay: ${table.name}`);
				continue;
			}
			for (const name of actual)
				if (!expectedColumns.has(name))
					problem(`SQL column missing from snapshot: ${table.name}.${name}`);
			for (const name of expectedColumns)
				if (!actual.includes(name))
					problem(`Snapshot column missing from SQL: ${table.name}.${name}`);
		}
		for (const table of order.keys())
			if (!expectedTables.has(table)) problem(`SQL table missing from snapshot: ${table}`);
	} catch (cause) {
		problem(String(cause));
	}
	return { order, problems };
}
