/**
 * `kiro-external`'s `provider.search` mapping, and the host-side normalizer that receives it.
 *
 * Does not touch the real upstream: that keeps CI off an external account and quota, at the
 * cost of not catching protocol drift (run `bun scripts/verify-kiro-plugin-bundle.ts --live`
 * for that). What it does pin is the contract boundary in both directions — what the plugin
 * produces from an MCP response, and what the host accepts back.
 */

import { describe, expect, test } from "bun:test";
import type { McpResponse } from "@server/lib/kiro/types";
import {
	providerSearchParamsSchema,
	providerSearchResultSchema,
} from "@server/lib/plugins/protocol";
import {
	readSearchParams,
	SearchInputError,
	searchResultFromMcp,
} from "../../../../examples/plugins/kiro-external/src/search";

function mcpResponse(
	result?: { content: Array<{ type: string; text: string }>; isError?: boolean },
	error?: { code?: number; message?: string },
): McpResponse {
	return {
		id: "req-1",
		jsonrpc: "2.0",
		...(result ? { result: { content: result.content, isError: result.isError ?? false } } : {}),
		...(error ? { error } : {}),
	};
}

function mcpText(text: string, isError = false): McpResponse {
	return mcpResponse({ content: [{ type: "text", text }], isError });
}

describe("kiro-external search params", () => {
	test("accepts a query and trims it", () => {
		expect(readSearchParams({ query: "  latest news  " })).toEqual({ query: "latest news" });
	});

	test("carries maxResults through when it is a safe integer", () => {
		expect(readSearchParams({ query: "q", maxResults: 5 })).toEqual({ query: "q", maxResults: 5 });
	});

	test("drops a non-integer maxResults rather than forwarding it upstream", () => {
		expect(readSearchParams({ query: "q", maxResults: 1.5 })).toEqual({ query: "q" });
	});

	test("rejects a missing or blank query", () => {
		expect(() => readSearchParams({})).toThrow(SearchInputError);
		expect(() => readSearchParams({ query: "   " })).toThrow(SearchInputError);
	});

	test("rejects a non-object payload", () => {
		expect(() => readSearchParams(null)).toThrow(SearchInputError);
		expect(() => readSearchParams([{ query: "q" }])).toThrow(SearchInputError);
	});
});

describe("kiro-external MCP response mapping", () => {
	test("flattens text content into the contract's text field", () => {
		const result = searchResultFromMcp(mcpText("Result body"), "q");
		expect(result).toEqual({ text: "Result body" });
		expect(providerSearchResultSchema.safeParse(result).success).toBe(true);
	});

	test("joins multiple text blocks", () => {
		const response = mcpResponse({
			content: [
				{ type: "text", text: "first" },
				{ type: "text", text: "second" },
			],
		});
		expect(searchResultFromMcp(response, "q").text).toBe("first\n\nsecond");
	});

	test("ignores content blocks that are not text", () => {
		// Upstream may interleave non-text blocks; the cast reflects a real payload the
		// declared type does not model.
		const response = mcpResponse({
			content: [
				{ type: "image", data: "..." },
				{ type: "text", text: "usable" },
			] as unknown as Array<{ type: string; text: string }>,
		});
		expect(searchResultFromMcp(response, "q").text).toBe("usable");
	});

	test("raises an upstream error instead of returning it as an answer", () => {
		// A failed channel must fail so the router moves on. Returning the error message as
		// text would look like a successful search that found bad news.
		const response = mcpResponse(undefined, { code: -1, message: "quota exhausted" });
		expect(() => searchResultFromMcp(response, "q")).toThrow(/quota exhausted/);
	});

	test("raises when the tool reports isError", () => {
		expect(() => searchResultFromMcp(mcpText("tool failed", true), "q")).toThrow(/tool failed/);
	});

	test("raises on empty content rather than reporting an empty success", () => {
		// The contract rejects a result with neither text nor results, and it is right to:
		// an empty success stops the fallback chain at a channel that found nothing.
		expect(() => searchResultFromMcp(mcpText("   "), "hello")).toThrow(/no content for: hello/);
		expect(() => searchResultFromMcp(mcpResponse({ content: [] }), "q")).toThrow(/no content/);
	});

	test("every mapped result satisfies the host's schema", () => {
		for (const text of ["short", "a".repeat(5_000), "with\nnewlines\nand  spacing"]) {
			const parsed = providerSearchResultSchema.safeParse(searchResultFromMcp(mcpText(text), "q"));
			expect(parsed.success).toBe(true);
		}
	});
});

describe("provider.search contract", () => {
	test("accepts the params the host sends", () => {
		const parsed = providerSearchParamsSchema.safeParse({
			protocolVersion: "1.0",
			contributionId: "web-search",
			config: { credentialsBundle: "[]" },
			query: "latest news",
			purpose: "verify",
			allowedDomains: ["example.com"],
			blockedDomains: ["spam.test"],
			recencyDays: 7,
			maxResults: 10,
			locale: "zh-CN",
		});
		expect(parsed.success).toBe(true);
	});

	test("rejects unknown params, so a plugin cannot rely on an undeclared field", () => {
		const parsed = providerSearchParamsSchema.safeParse({
			protocolVersion: "1.0",
			contributionId: "web-search",
			query: "q",
			streaming: true,
		});
		expect(parsed.success).toBe(false);
	});

	test("rejects an empty query", () => {
		const parsed = providerSearchParamsSchema.safeParse({
			protocolVersion: "1.0",
			contributionId: "web-search",
			query: "   ",
		});
		expect(parsed.success).toBe(false);
	});

	test("accepts a structured-only result", () => {
		const parsed = providerSearchResultSchema.safeParse({
			results: [{ title: "Result", url: "https://example.com", snippet: "body" }],
		});
		expect(parsed.success).toBe(true);
	});

	test("rejects a result carrying neither text nor results", () => {
		expect(providerSearchResultSchema.safeParse({}).success).toBe(false);
		expect(providerSearchResultSchema.safeParse({ results: [] }).success).toBe(false);
		expect(providerSearchResultSchema.safeParse({ text: "" }).success).toBe(false);
	});

	test("rejects unknown fields on a result item", () => {
		const parsed = providerSearchResultSchema.safeParse({
			results: [{ title: "R", rank: 1 }],
		});
		expect(parsed.success).toBe(false);
	});
});
