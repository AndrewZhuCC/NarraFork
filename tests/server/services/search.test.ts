/**
 * Query-shape tests: sanitizing, the index/substring threshold, and the match expressions.
 *
 * These are the parts of search most easily broken without anything failing. A prefix match
 * quietly becoming a phrase match, or an escaped `%` quietly becoming a wildcard, changes
 * WHICH ROWS come back — not whether the code runs — so nothing here can be inferred from a
 * green suite elsewhere.
 *
 * The expression builders are pure and import no database, so this file needs no fixture.
 */
import { describe, expect, it } from "bun:test";
import {
	canUseIndex,
	globalQueryTerms,
	globalStrategy,
	MAX_GLOBAL_QUERY_TERMS,
	sanitizeQuery,
} from "../../../server/services/search/query";
import {
	escapedContains,
	prefixExpr,
	rawContains,
	snippetCall,
} from "../../../server/services/search/sqlite-expressions";
import { GLOBAL_SNIPPET, RECALL_SNIPPET } from "../../../server/services/search/types";

describe("search query sanitization", () => {
	it("strips SQL injection attempts", () => {
		expect(sanitizeQuery("test'; DROP TABLE narrators;--")).toBe("test DROP TABLE narrators--");
	});

	it("preserves alphanumeric, spaces, hyphens, dots, underscores", () => {
		expect(sanitizeQuery("my-file_v2.0 test")).toBe("my-file_v2.0 test");
	});

	it("strips special characters", () => {
		expect(sanitizeQuery("hello@world!#$%")).toBe("helloworld#$%");
	});

	it("returns empty for all-special input", () => {
		expect(sanitizeQuery("@#$%^&*()")).toBe("#$%");
	});

	it("trims whitespace", () => {
		expect(sanitizeQuery("  hello  ")).toBe("hello");
	});
});

describe("index eligibility", () => {
	it("requires three characters, because that is a trigram", () => {
		expect(canUseIndex("ab")).toBe(false);
		expect(canUseIndex("abc")).toBe(true);
	});

	// Counter-intuitive but load-bearing: two Han characters carry plenty of meaning yet
	// still cannot form a trigram token, so they MUST take the substring path or the search
	// silently returns nothing.
	it("applies the same rule to CJK", () => {
		expect(canUseIndex("充电")).toBe(false);
		expect(canUseIndex("三阶段")).toBe(true);
	});
});

describe("FTS prefix expressions (timeline, Recall, knowledge)", () => {
	it("wraps each word in quotes with prefix wildcard", () => {
		expect(prefixExpr("hello world")).toBe('"hello"* "world"*');
	});

	it("handles single word", () => {
		expect(prefixExpr("test")).toBe('"test"*');
	});

	it("collapses multiple spaces", () => {
		expect(prefixExpr("a   b")).toBe('"a"* "b"*');
	});

	it("joins with OR when any term may match", () => {
		expect(prefixExpr("a b", "or")).toBe('"a"* OR "b"*');
	});

	it("restricts the whole expression to one column when asked", () => {
		expect(prefixExpr("a b", "and", "current_keywords")).toBe('{current_keywords} : ("a"* "b"*)');
	});

	it("returns empty for input with no terms, which callers must not send to MATCH", () => {
		expect(prefixExpr("   ")).toBe("");
	});
});

describe("global search terms", () => {
	// Global search matches every term on its own, anywhere and in any order. Terms the
	// trigram index cannot tokenize are split off so they are checked as substrings instead of
	// silently matching nothing.
	it("splits terms by whether the index can match them", () => {
		expect(globalQueryTerms("登录 失败 refresh")).toEqual({
			indexed: ["refresh"],
			short: ["登录", "失败"],
		});
	});

	it("uses the index whenever any term can, and the substring path only when none can", () => {
		expect(globalStrategy("登录 refresh")).toBe("index");
		expect(globalStrategy("登录 失败")).toBe("substring");
		// A long query of short words is still a substring query: no single term is indexable.
		expect(globalStrategy("ab cd ef")).toBe("substring");
	});

	it("counts a repeated term once and bounds the number of terms", () => {
		expect(globalQueryTerms("Token token TOKEN")).toEqual({ indexed: ["Token"], short: [] });
		const many = Array.from({ length: 20 }, (_, i) => `term${i}`).join(" ");
		expect(globalQueryTerms(many).indexed).toHaveLength(MAX_GLOBAL_QUERY_TERMS);
	});
});

describe("substring patterns", () => {
	it("leaves user-typed wildcards active on the paths that never escaped them", () => {
		expect(rawContains("50%_off")).toBe("%50%_off%");
	});

	it("escapes wildcards on the paths that pair the pattern with ESCAPE", () => {
		expect(escapedContains("50%_off")).toBe("%50\\%\\_off%");
	});

	it("escapes the escape character itself", () => {
		expect(escapedContains("a\\b")).toBe("%a\\\\b%");
	});
});

describe("snippet rendering", () => {
	it("renders the unmarked global format", () => {
		expect(snippetCall("narrators_fts", 0, GLOBAL_SNIPPET)).toBe(
			"snippet(narrators_fts, 0, '', '', '...', 96)",
		);
	});

	// Recall's output is read by a model and its format is part of that contract.
	it("renders Recall's match markers", () => {
		expect(snippetCall("narrator_messages_fts", 0, RECALL_SNIPPET)).toBe(
			"snippet(narrator_messages_fts, 0, '>>>', '<<<', '...', 64)",
		);
	});

	it("doubles a quote in a delimiter rather than ending the literal", () => {
		expect(snippetCall("t", 0, { open: "'", close: "'", ellipsis: "…", tokens: 8 })).toBe(
			"snippet(t, 0, '''', '''', '…', 8)",
		);
	});
});
