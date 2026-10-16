import { describe, expect, test } from "bun:test";
import { readSubmittedForm, submittedText } from "./InlineRender";

describe("what a submit sends", () => {
	test("the confirmed text is built from the values that are sent, not from the frame", () => {
		const form = readSubmittedForm({
			message: " Chosen ",
			fields: { size: "L", count: 2, tags: ["a", ["b"]], approve: true },
		});
		expect(form).toEqual({
			message: "Chosen",
			fields: { size: "L", count: 2, tags: ["a", '["b"]'], approve: true },
		});
		expect(submittedText(form as NonNullable<typeof form>)).toBe(
			'Chosen\nsize: L\ncount: 2\ntags: a, ["b"]\napprove: true',
		);
	});

	test("values are cut to what the server takes instead of the submit failing", () => {
		const fields = Object.fromEntries(
			Array.from({ length: 30 }, (_, i) => [`field${i}`, "x".repeat(2000)]),
		);
		const form = readSubmittedForm({
			message: "",
			fields: { ...fields, "bad name": 1, nested: { deep: 1 } },
		});
		expect(JSON.stringify(form?.fields).length).toBeLessThanOrEqual(32 * 1024);
		expect(form?.fields).not.toHaveProperty("bad name");
		expect(form?.fields.nested).toBe('{"deep":1}');
		expect(submittedText(form as NonNullable<typeof form>).length).toBeLessThanOrEqual(2000);
	});

	test("a submit without a form object is not a form", () => {
		expect(readSubmittedForm({ message: "x", fields: [] })).toBeNull();
		expect(readSubmittedForm({ fields: {} })).toBeNull();
	});
});
