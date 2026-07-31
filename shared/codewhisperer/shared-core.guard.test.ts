import { describe, expect, it } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

/**
 * Bundle-purity boundary for the CodeWhisperer protocol layer.
 *
 * This layer is shared by two consumers that must never share a module graph:
 *
 * - the NarraFork core, for the NUG `kiro` channel;
 * - bundled plugin code, which runs in a separate process and may not import
 *   host internals.
 *
 * A single `server/` import here is enough to drag the settings graph, Drizzle
 * and `bun:sqlite` into a plugin bundle. That regression is invisible in type
 * checking and in unit tests, so it is asserted statically instead. When this
 * layer was extracted, one such import (`resolve-model` reaching `parseModelId`
 * through the settings barrel) accounted for 372 modules / ~2 MB on its own.
 */

const SHARED_DIR = import.meta.dir;

function sharedSourceFiles(): string[] {
	return readdirSync(SHARED_DIR)
		.filter((name) => name.endsWith(".ts") && !name.endsWith(".test.ts"))
		.sort();
}

describe("shared codewhisperer core boundary", () => {
	it("covers every non-test module in the directory", () => {
		// Guards against a new module being added and silently escaping the checks
		// below, which are otherwise driven by the same listing.
		const files = sharedSourceFiles();
		expect(files.length).toBeGreaterThan(0);
		expect(files).toContain("protocol-types.ts");
		expect(files).toContain("runtime.ts");
	});

	it("does not import host modules", () => {
		for (const file of sharedSourceFiles()) {
			const source = readFileSync(join(SHARED_DIR, file), "utf8");
			// Path-alias imports into the host.
			expect(source, `${file} must not import @server/*`).not.toMatch(/from\s+["']@server\//);
			expect(source, `${file} must not import @frontend/*`).not.toMatch(/from\s+["']@frontend\//);
			// Relative escapes out of shared/ (e.g. "../../server/lib/...").
			expect(source, `${file} must not reach into server/ relatively`).not.toMatch(
				/from\s+["'][^"']*\/server\//,
			);
			// Dynamic imports are followed by the bundler just like static ones.
			expect(source, `${file} must not dynamically import host modules`).not.toMatch(
				/import\(\s*["'](?:@server\/|@frontend\/|[^"']*\/server\/)/,
			);
		}
	});

	it("does not reach the database, HTTP server or host settings", () => {
		for (const file of sharedSourceFiles()) {
			const source = readFileSync(join(SHARED_DIR, file), "utf8");
			expect(source, `${file} must not use bun:sqlite`).not.toMatch(/["']bun:sqlite["']/);
			expect(source, `${file} must not use drizzle`).not.toMatch(/from\s+["']drizzle/);
			expect(source, `${file} must not use hono`).not.toMatch(/from\s+["']hono/);
		}
	});
});
