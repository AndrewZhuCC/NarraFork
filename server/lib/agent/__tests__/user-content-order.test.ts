import { describe, expect, test } from "bun:test";
import { orderUserContent } from "../user-content-order";

describe("orderUserContent", () => {
	test("places each named image right after its placeholder", () => {
		expect(
			orderUserContent("看[image #2: b.png]和[image #1: a.png]区别", ["A", "B"], "before"),
		).toEqual([
			{ type: "text", text: "看[image #2: b.png]" },
			{ type: "image", image: "B" },
			{ type: "text", text: "和[image #1: a.png]" },
			{ type: "image", image: "A" },
			{ type: "text", text: "区别" },
		]);
	});
	test("keeps the provider's historical slot for unplaced images", () => {
		expect(orderUserContent("hi", ["A"], "before")).toEqual([
			{ type: "image", image: "A" },
			{ type: "text", text: "hi" },
		]);
		expect(orderUserContent("hi[image #1: a.png]", ["A", "B"], "after")).toEqual([
			{ type: "text", text: "hi[image #1: a.png]" },
			{ type: "image", image: "A" },
			{ type: "image", image: "B" },
		]);
	});
	test("a missing image keeps later images on their own placeholders", () => {
		const images = [
			{ format: "png", base64: "B", position: 1 },
			{ format: "png", base64: "C", position: 2 },
		];
		const parts = orderUserContent("[image #1: a.png]x[image #2: b.png]", images, "before");
		expect(parts).toEqual([
			{ type: "image", image: images[1] },
			{ type: "text", text: "[image #1: a.png]x[image #2: b.png]" },
			{ type: "image", image: images[0] },
		]);
		expect(orderUserContent("[image #1: a.png]", [null, "B"], "after")).toEqual([
			{ type: "text", text: "[image #1: a.png]" },
			{ type: "image", image: "B" },
		]);
	});
	test("never emits a whitespace-only text part", () => {
		expect(orderUserContent("看[image #1: a.png]\n ", ["A"], "before")).toEqual([
			{ type: "text", text: "看[image #1: a.png]\n " },
			{ type: "image", image: "A" },
		]);
		expect(orderUserContent("[image #1: a.png]", ["A"], "after")).toEqual([
			{ type: "text", text: "[image #1: a.png]" },
			{ type: "image", image: "A" },
		]);
	});
	test("ignores placeholder look-alikes inside quoted excerpts", () => {
		const text = "> [quote] Q\n> see [image #1: a.png]\n\n看[image #1: a.png]";
		expect(orderUserContent(text, ["A"], "before")).toEqual([
			{ type: "text", text },
			{ type: "image", image: "A" },
		]);
	});
	test("skips placeholders the projection escaped", () => {
		expect(orderUserContent("\\[image #1: a.png] then [image #1: a.png]", ["A"], "before")).toEqual(
			[
				{ type: "text", text: "\\[image #1: a.png] then [image #1: a.png]" },
				{ type: "image", image: "A" },
			],
		);
	});
});
