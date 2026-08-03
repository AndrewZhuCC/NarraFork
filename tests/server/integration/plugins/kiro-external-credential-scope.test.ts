import { describe, expect, test } from "bun:test";
import { readdir } from "node:fs/promises";
import { join } from "node:path";

/**
 * Keeps `kiro-external` from writing credentials into the repository.
 *
 * `MultiTokenManager` persists refreshed tokens to the `credentialsPath` it is given. An
 * earlier revision of `credentials.ts` fell back to `process.cwd()` when the host had not
 * supplied a data directory — and the plugin runtime sets cwd to the *package* directory. So
 * a plugin run wrote a real `credentials.json` into `examples/plugins/kiro-external/`, where
 * it was untracked, not ignored, and would have been committed.
 *
 * Two assertions guard the two halves of that failure:
 *
 *  1. no credential file exists in the plugin package (catches the artifact);
 *  2. the source contains no cwd-based data-directory fallback (catches the cause).
 *
 * The second matters because the first only fails after someone has already run the plugin
 * with credentials, which is not something CI does.
 */

const PLUGIN_ROOT = join(process.cwd(), "examples/plugins/kiro-external");

async function entries(dir: string): Promise<string[]> {
	try {
		return await readdir(dir);
	} catch {
		return [];
	}
}

describe("kiro-external credential scope", () => {
	test("the plugin package contains no credential or token files", async () => {
		const names = await entries(PLUGIN_ROOT);
		// A data directory inside the package is the symptom; `.kiro-external` was the name the
		// buggy fallback used, but any of these would be equally wrong.
		for (const forbidden of [".kiro-external", "credentials.json", "config.json"]) {
			expect(names, `${forbidden} must not exist in the plugin package`).not.toContain(forbidden);
		}
	});

	test("no nested directory holds a credentials file", async () => {
		for (const name of await entries(PLUGIN_ROOT)) {
			const nested = await entries(join(PLUGIN_ROOT, name));
			expect(nested, `${name}/ must not contain credentials.json`).not.toContain(
				"credentials.json",
			);
		}
	});

	test("the data directory never falls back to the package directory", async () => {
		const source = await Bun.file(join(PLUGIN_ROOT, "src/credentials.ts")).text();
		// Comments legitimately mention `process.cwd()` to explain why it is wrong, so strip
		// them before checking. Only executable code matters here.
		const code = source
			.replace(/\/\*[\s\S]*?\*\//g, "")
			.split("\n")
			.filter((line) => !line.trim().startsWith("//"))
			.join("\n");
		// `process.cwd()` is the package dir at runtime, so using it for persistence writes
		// into version control for a plugin shipped from this repo.
		expect(code, "credentials.ts must not use process.cwd() for plugin data").not.toInclude(
			"process.cwd()",
		);
		// The host-provided scope is the only correct source, spelled as the runtime spells it.
		expect(code).toInclude("NF_PLUGIN_DATA_DIR");
	});

	test("the built artifact carries the same guarantee", async () => {
		// The committed bundle is what actually runs; a stale artifact could still contain the
		// old fallback even after the source is fixed.
		const bundle = await Bun.file(join(PLUGIN_ROOT, "server/index.js")).text();
		expect(bundle).toInclude("NF_PLUGIN_DATA_DIR");
	});
});
