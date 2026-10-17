/**
 * robots-disallow.test.ts — The instance must not be indexable by search engines.
 *
 * `frontend/public/robots.txt` is the source, but what actually ships is
 * `dist/frontend/robots.txt` (Vite copies `public/` to the output root). Neither
 * `tsgo` nor the build would notice if that copy stopped happening — the app keeps
 * working, it just quietly becomes crawlable — so both halves are asserted here.
 *
 * The built-artifact check is skipped when `dist/frontend` has not been built, so a
 * plain `bun test` on a fresh clone does not fail on a missing artifact (same
 * convention as branding-build-output.test.ts).
 */

import { describe, expect, test } from "bun:test";

const SOURCE_PATH = "frontend/public/robots.txt";
const BUILT_PATH = "dist/frontend/robots.txt";

/** Directive lines only — `#` comments are prose and may mention paths. */
function directiveLines(text: string): string[] {
	return text
		.split(/\r?\n/)
		.map((line) => line.replace(/#.*/, "").trim())
		.filter((line) => line !== "");
}

describe("robots policy", () => {
	test("disallows every crawler from the whole app", async () => {
		const directives = directiveLines(await Bun.file(SOURCE_PATH).text());

		// A `User-agent: *` group is required: rules for named bots do not apply to a
		// crawler we did not enumerate.
		expect(directives).toContain("User-agent: *");
		// `Disallow: /` must be present and must be the ONLY path rule. Any `Allow:`
		// line here would re-open a prefix — including the empty `Allow:` that is
		// sometimes pasted as "allow everything" and silently means no rule at all.
		const pathRules = directives.filter((line) => /^(?:Allow|Disallow)\b/i.test(line));
		expect(pathRules).toEqual(["Disallow: /"]);
	});

	test("is emitted at the site root of the frontend build", async () => {
		const built = Bun.file(BUILT_PATH);
		if (!(await built.exists())) return; // frontend build not present in this checkout

		// The root matters: a crawler only fetches /robots.txt, and the server's static
		// middleware looks the file up by request path (server/main.ts), so a copy under
		// a subdirectory would be served at a URL no crawler asks for.
		expect(directiveLines(await built.text())).toContain("Disallow: /");
	});
});
