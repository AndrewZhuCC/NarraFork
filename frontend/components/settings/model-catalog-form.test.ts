import { describe, expect, test } from "bun:test";
import { buildCatalogPatch, fieldText, metadataFromPatch } from "./model-catalog-form";

describe("model catalog explicit leaf operations", () => {
	test("opening a form without touching fields is a no-op", () =>
		expect(buildCatalogPatch({})).toBeNull());
	test("zero, unknown, false, empty list and reset remain distinct", () => {
		const patch = buildCatalogPatch({
			input_cost_per_token: { mode: "set", value: "0.000" },
			output_cost_per_token: { mode: "unknown" },
			supports_web_search: { mode: "set", value: "false" },
			reasoning_effort_levels: { mode: "set", value: "" },
			working_context_tokens: { mode: "reset" },
		});
		expect(patch).toEqual({
			set: {
				input_cost_per_token: "0",
				output_cost_per_token: null,
				supports_web_search: false,
				reasoning_effort_levels: [],
			},
			reset: ["working_context_tokens"],
		});
		expect(metadataFromPatch(patch)).toEqual({
			input_cost_per_token: "0",
			output_cost_per_token: null,
			supports_web_search: false,
			reasoning_effort_levels: [],
		});
	});
	test("blank prices and zero token limits never silently become free or inherit", () => {
		for (const value of ["", " ", "-1", "1e2", "NaN"])
			expect(() => buildCatalogPatch({ input_cost_per_token: { mode: "set", value } })).toThrow();
		for (const value of ["", "0", "-1", "1.5", "Infinity"])
			expect(() => buildCatalogPatch({ working_context_tokens: { mode: "set", value } })).toThrow();
		expect(() => buildCatalogPatch({ "limits.contextWindow": { mode: "reset" } })).toThrow();
	});
	test("prices are typed per million and stored exactly per token", () => {
		expect(
			metadataFromPatch(
				buildCatalogPatch({
					input_cost_per_token: { mode: "set", value: "0.000000000000000001" },
					output_cost_per_token: { mode: "set", value: "15" },
				}),
			),
		).toEqual({
			input_cost_per_token: "0.000000000000000000000001",
			output_cost_per_token: "0.000015",
		});
		expect(fieldText(1.5e-5, "price")).toBe("15");
		expect(fieldText("0.000015", "price")).toBe("15");
		expect(fieldText(128000, "integer")).toBe("128000");
		expect(fieldText(null, "price")).toBe("");
	});
});
