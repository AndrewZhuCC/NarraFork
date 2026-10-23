import { describe, expect, test } from "bun:test";
import { type QuoteButtonLayout, quoteButtonPosition } from "./quote-button-position";

const base: QuoteButtonLayout = {
	placement: "selection",
	selection: { top: 200, left: 100, right: 300, bottom: 220 },
	bounds: { top: 50, left: 0, right: 1000, bottom: 700 },
	composer: { top: 600, left: 20, right: 980, bottom: 680 },
	viewport: { width: 1000, height: 800 },
	size: { width: 120, height: 28 },
};

describe("quoteButtonPosition", () => {
	test("sits above the selection end, centred on it", () => {
		expect(quoteButtonPosition(base)).toEqual({ top: 166, left: 240 });
	});

	test("flips below when there is no room above", () => {
		expect(
			quoteButtonPosition({ ...base, selection: { top: 60, left: 100, right: 300, bottom: 80 } }),
		).toEqual({ top: 86, left: 240 });
	});

	test("never overlaps the composer: clamps into the space between the panel top and it", () => {
		const position = quoteButtonPosition({
			...base,
			bounds: { ...base.bounds, top: 560 },
			selection: { top: 570, left: 100, right: 300, bottom: 590 },
		});
		expect(position).toEqual({ top: 560, left: 240 });
	});

	test("hides once the selection end scrolls out of the visible message area", () => {
		expect(
			quoteButtonPosition({ ...base, selection: { top: 10, left: 0, right: 10, bottom: 40 } }),
		).toBeNull();
		expect(
			quoteButtonPosition({ ...base, selection: { top: 610, left: 0, right: 10, bottom: 630 } }),
		).toBeNull();
	});

	test("stays inside the panel and viewport horizontally", () => {
		expect(
			quoteButtonPosition({ ...base, selection: { top: 200, left: 0, right: 10, bottom: 220 } })
				?.left,
		).toBe(4);
		expect(
			quoteButtonPosition({
				...base,
				selection: { top: 200, left: 900, right: 995, bottom: 220 },
			})?.left,
		).toBe(876);
		expect(
			quoteButtonPosition({
				...base,
				bounds: { ...base.bounds, left: 500, right: 800 },
				selection: { top: 200, left: 500, right: 510, bottom: 220 },
			})?.left,
		).toBe(500);
	});

	test("touch placement right-aligns on the composer's top edge", () => {
		expect(quoteButtonPosition({ ...base, placement: "composer" })).toEqual({
			top: 566,
			left: 860,
		});
	});

	test("falls back to the composer edge without a selection rect, hides without a composer", () => {
		expect(quoteButtonPosition({ ...base, selection: null })).toEqual({ top: 566, left: 860 });
		expect(
			quoteButtonPosition({
				...base,
				placement: "composer",
				composer: { top: 0, left: 0, right: 0, bottom: 0 },
			}),
		).toBeNull();
	});
});
