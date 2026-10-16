import { describe, expect, test } from "bun:test";
import {
	browseEmptyState,
	effectiveBrowseSort,
	parseKnowledgeBrowseSearch,
} from "./knowledge-browse-search";

describe("parseKnowledgeBrowseSearch", () => {
	test("keeps known params, drops malformed ones", () => {
		expect(parseKnowledgeBrowseSearch({})).toEqual({});
		expect(
			parseKnowledgeBrowseSearch({
				collection: "col_1",
				q: "  hello ",
				tag: "ops",
				level: "confidential",
				status: "archived",
				sort: "title",
			}),
		).toEqual({
			collection: "col_1",
			q: "hello",
			tag: "ops",
			level: "confidential",
			status: "archived",
			sort: "title",
		});
		expect(
			parseKnowledgeBrowseSearch({
				collection: "../x",
				q: " ",
				tag: "x".repeat(65),
				status: "deleted",
				sort: "size",
				tab: "review",
			}),
		).toEqual({});
	});

	test("the default order is implied and left out of the URL", () => {
		expect(parseKnowledgeBrowseSearch({ sort: "updated" })).toEqual({});
		expect(parseKnowledgeBrowseSearch({ q: "a", sort: "relevance" })).toEqual({ q: "a" });
		expect(parseKnowledgeBrowseSearch({ q: "a", sort: "updated" })).toEqual({
			q: "a",
			sort: "updated",
		});
	});

	test("keeps the admin tab until the admin pages move (P2d)", () => {
		expect(parseKnowledgeBrowseSearch({ tab: "admin" })).toEqual({ tab: "admin" });
	});
});

describe("effectiveBrowseSort", () => {
	test("relevance with a query, updated without; relevance needs a query", () => {
		expect(effectiveBrowseSort({})).toBe("updated");
		expect(effectiveBrowseSort({ q: "x" })).toBe("relevance");
		expect(effectiveBrowseSort({ sort: "title" })).toBe("title");
		expect(effectiveBrowseSort({ sort: "relevance" })).toBe("updated");
	});
});

describe("browseEmptyState", () => {
	test("distinguishes no collections / no results / empty collection", () => {
		expect(browseEmptyState({}, 0)).toBe("noCollections");
		expect(browseEmptyState({ q: "x" }, 2)).toBe("noResults");
		expect(browseEmptyState({ collection: "c", level: "public" }, 2)).toBe("noResults");
		expect(browseEmptyState({ collection: "c" }, 2)).toBe("emptyCollection");
		expect(browseEmptyState({}, 2)).toBe("empty");
	});
});
