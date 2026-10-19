import { describe, expect, test } from "bun:test";
import { quoteDetailMarkdown } from "./QuoteDetailModal";

describe("quoteDetailMarkdown", () => {
	test("renders a message excerpt as markdown", () => {
		const text = "**bold** and `code`";
		expect(quoteDetailMarkdown({ label: "bold and code", text, anchor: "m:m1" })).toBe(text);
		expect(quoteDetailMarkdown({ label: "no anchor", text })).toBe(text);
	});

	test("renders a file excerpt as code in the file's language, without the name line", () => {
		expect(
			quoteDetailMarkdown({
				label: "knowledge-notify.ts · const a = 1;",
				text: "knowledge-notify.ts\nconst a = 1;",
				anchor: "t:tool1",
			}),
		).toBe("```typescript\nconst a = 1;\n```");
	});

	test("renders other tool output as a plain code block", () => {
		expect(quoteDetailMarkdown({ label: "ok", text: "ok\ndone", anchor: "t:tool1" })).toBe(
			"```\nok\ndone\n```",
		);
	});

	test("uses a fence longer than any backtick run in the excerpt", () => {
		expect(quoteDetailMarkdown({ label: "x", text: "a ``` b", anchor: "t:tool1" })).toBe(
			"````\na ``` b\n````",
		);
	});
});
