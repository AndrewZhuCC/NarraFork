import { describe, expect, test } from "bun:test";
import { parseHTML } from "linkedom";
import { resolveHeadingPath, resolveHeadingPathFromDom } from "./heading-path";

describe("resolveHeadingPath", () => {
	test("keeps nested headings and handles skipped levels", () => {
		expect(
			resolveHeadingPath([
				{ level: 1, text: "One" },
				{ level: 3, text: "Three" },
				{ level: 2, text: "Two" },
				{ level: 4, text: "Four" },
			]),
		).toEqual(["One", "Two", "Four"]);
	});

	test("preserves empty headings and ignores invalid levels", () => {
		expect(
			resolveHeadingPath([
				{ level: 1, text: "Root" },
				{ level: 2, text: "" },
				{ level: 0, text: "invalid" },
			]),
		).toEqual(["Root", ""]);
	});
});

describe("resolveHeadingPathFromDom", () => {
	test("resolves the nearest stack before the anchor", () => {
		const { document } = parseHTML(
			"<main><p id='first'>first</p><h1 data-heading='Root' data-level='1'>Root</h1><p id='before'>before</p><h3 data-heading='Nested' data-level='3'>Nested</h3><p id='anchor'>anchor</p><h2 data-heading='Later' data-level='2'>Later</h2></main>",
		);
		const container = document.querySelector("main");
		const first = document.querySelector("#first")?.firstChild ?? null;
		const before = document.querySelector("#before")?.firstChild ?? null;
		const anchor = document.querySelector("#anchor")?.firstChild ?? null;
		expect(resolveHeadingPathFromDom(first, container)).toEqual([]);
		expect(resolveHeadingPathFromDom(before, container)).toEqual(["Root"]);
		expect(resolveHeadingPathFromDom(anchor, container)).toEqual(["Root", "Nested"]);
	});

	test("does not resolve a node outside the bounded container", () => {
		const { document } = parseHTML("<main><h1>Inside</h1></main><h1 id='outside'>Outside</h1>");
		const container = document.querySelector("main");
		expect(
			resolveHeadingPathFromDom(document.querySelector("#outside")?.firstChild ?? null, container),
		).toEqual([]);
	});
});
